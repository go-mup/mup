package admin

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"time"

	"gopkg.in/mup.v0"
	"gopkg.in/mup.v0/schema"

	"golang.org/x/crypto/scrypt"

	"github.com/mattn/go-sqlite3"
)

var Plugin = mup.PluginSpec{
	Name:     "admin",
	Help:     "Exposes the bot administration commands.",
	Start:    start,
	Commands: Commands,
}

var Commands = schema.Commands{{
	Name: "register",
	Help: `Registers ownership of the current nick with the bot.
	
	The first nick registered becomes the bot admin.
	`,
	Args: schema.Args{{
		Name: "password",
		Flag: schema.Required | schema.Trailing,
	}},
}, {
	Name: "login",
	Help: "Authenticates with the bot.",
	Args: schema.Args{{
		Name: "password",
		Flag: schema.Required | schema.Trailing,
	}},
}, {
	Name: "sendraw",
	Help: `Sends the provided text as a raw IRC protocol message.
	
	If an account name is not provided, it defaults to the current one.
	`,
	Args: schema.Args{{
		Name: "-account",
	}, {
		Name: "text",
		Flag: schema.Required | schema.Trailing,
	}},
}}

func init() {
	mup.RegisterPlugin(&Plugin)
}

type userKind int

const (
	unknownUser userKind = 0
	normalUser  userKind = 1
	adminUser   userKind = 2
)

type userKey struct {
	Account, Nick string
}

type adminPlugin struct {
	plugger *mup.Plugger
	logins  map[userKey]userKind
}

func start(plugger *mup.Plugger) mup.Stopper {
	return &adminPlugin{
		plugger: plugger,
		logins:  make(map[userKey]userKind),
	}
}

func (p *adminPlugin) Stop() error {
	return nil
}

func (p *adminPlugin) HandleMessage(msg *mup.Message) {
	if msg.Command == "QUIT" || msg.Command == "NICK" {
		delete(p.logins, userKey{msg.Account, msg.Nick})
	}
}

func (p *adminPlugin) HandleCommand(cmd *mup.Command) {
	switch cmd.Name() {
	case "register":
		p.register(cmd)
	case "login":
		p.login(cmd)
	case "sendraw":
		p.sendraw(cmd)
	default:
		p.plugger.Sendf(cmd, "I have a bug. Command %q exists and I don't know how to handle it.", cmd.Name())
	}
}

const (
	burstQuota   = 3
	burstWindow  = 15 * time.Second
	burstPenalty = 30 * time.Second
)

type userInfo struct {
	Account      string
	Nick         string
	PasswordHash string
	PasswordSalt string
	AttemptStart time.Time
	AttemptCount int
	Admin        bool
}

const userColumns = "account,nick,passwordhash,passwordsalt,attemptstart,attemptcount,admin"
const userPlacers = "?,?,?,?,?,?,?"

func (u *userInfo) refs() []interface{} {
	return []interface{}{&u.Account, &u.Nick, &u.PasswordHash, &u.PasswordSalt, &u.AttemptStart, &u.AttemptCount, &u.Admin}
}

func (u *userInfo) key() userKey {
	return userKey{u.Account, u.Nick}
}

func (p *adminPlugin) register(cmd *mup.Command) {
	var args struct{ Password string }
	cmd.Args(&args)

	tx, err := p.plugger.DB().Begin()
	if err != nil {
		p.plugger.Logf("Cannot begin database transaction: %v", err)
		p.plugger.Sendf(cmd, "Oops: cannot begin database transaction: %v", err)
		return
	}
	defer tx.Rollback()

	saltBytes := make([]byte, 8)
	_, err = rand.Read(saltBytes)
	if err != nil {
		p.plugger.Logf("Cannot obtain random bytes from system: %v", err)
		p.plugger.Sendf(cmd, "Oops: cannot obtain random bytes from system: %v", err)
		return
	}
	salt := hex.EncodeToString(saltBytes)

	hash, ok := p.scryptHash(cmd, args.Password, salt)
	if !ok {
		return
	}

	row := tx.QueryRow("SELECT COUNT(*) FROM user")
	var count int64
	err = row.Scan(&count)
	if err != nil {
		p.plugger.Logf("Cannot obtain number of registered users: %v", err)
		p.plugger.Sendf(cmd, "Oops: cannot obtain number of registered users: %v", err)
		return
	}

	user := &userInfo{
		Account:      cmd.Account,
		Nick:         cmd.Nick,
		PasswordHash: hash,
		PasswordSalt: salt,
		Admin:        count == 0,
	}

	_, err = tx.Exec("INSERT INTO user ("+userColumns+") VALUES ("+userPlacers+")", user.refs()...)
	if e, ok := err.(sqlite3.Error); ok && e.Code == 19 && e.ExtendedCode == 1555 {
		p.plugger.Logf("Nick %q at account %s attempted to register again.", cmd.Nick, cmd.Account)
		p.plugger.Sendf(cmd, "Nick previously registered.")
		return
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		p.plugger.Logf("Cannot insert registering user: %#v", err)
		p.plugger.Sendf(cmd, "Oops: there was an error while registering your details.")
		return
	}
	if count == 0 {
		p.plugger.Sendf(cmd, "Registered as an admin (first user).")
	} else {
		p.plugger.Sendf(cmd, "Registered.")
	}
}

func (p *adminPlugin) login(cmd *mup.Command) {
	var args struct{ Password string }
	cmd.Args(&args)

	db := p.plugger.DB()

	var user userInfo
	err := db.QueryRow("SELECT "+userColumns+" FROM user WHERE account=? AND nick=?", cmd.Account, cmd.Nick).Scan(user.refs()...)
	if err == sql.ErrNoRows {
		p.plugger.Sendf(cmd, "Nope.")
		return
	}
	if err != nil {
		p.plugger.Logf("Cannot get user details for nick %q at %s: %v", cmd.Nick, cmd.Account, err)
		p.plugger.Sendf(cmd, "Oops: there was an error while checking your details.")
		return
	}
	window := burstWindow
	if user.AttemptCount >= burstQuota {
		window = burstPenalty
	}
	if user.AttemptStart.Before(time.Now().Add(-window)) {
		user.AttemptCount = 0
		user.AttemptStart = time.Now()
	}
	user.AttemptCount++
	if user.AttemptCount > burstQuota {
		p.plugger.Sendf(cmd, "Slow down.")
		return
	}

	equal, ok := p.scryptHashCompare(cmd, args.Password, user.PasswordSalt, user.PasswordHash)
	if !ok {
		return
	}
	if !equal {
		_, err = db.Exec("UPDATE user SET attemptstart=?,attemptcount=? WHERE account=? AND nick=?",
			user.AttemptStart, user.AttemptCount, user.Account, user.Nick)
		if err != nil {
			p.plugger.Logf("Cannot update login attempt for nick %q at %s: %v", cmd.Nick, cmd.Account, err)
			p.plugger.Sendf(cmd, "Oops: there was an error while recording your login attempt.")
		} else {
			p.plugger.Sendf(cmd, "Nope.")
		}
		return
	}
	p.plugger.Sendf(cmd, "Okay.")
	if user.Admin {
		p.logins[user.key()] = adminUser
	} else {
		p.logins[user.key()] = normalUser
	}
}

func (p *adminPlugin) scryptHash(cmd *mup.Command, password, salt string) (hash string, ok bool) {
	key, err := scrypt.Key([]byte(password), []byte(salt), 16384, 8, 1, 32)
	if err != nil {
		p.plugger.Logf("scrypt.Key failed: %v", err)
		p.plugger.Sendf(cmd, "Internal error hashing password. Sorry.")
	}
	return hex.EncodeToString(key), err == nil
}

func (p *adminPlugin) scryptHashCompare(cmd *mup.Command, password, salt string, candidateHash string) (equal, ok bool) {
	hash, ok := p.scryptHash(cmd, password, salt)
	return hash == candidateHash, ok
}

func (p *adminPlugin) checkLogin(cmd *mup.Command, want userKind) bool {
	kind := p.logins[userKey{cmd.Account, cmd.Nick}]
	if kind == unknownUser {
		p.plugger.Sendf(cmd, "Must login for that.")
		return false
	}
	if want > kind {
		p.plugger.Sendf(cmd, "Must be an admin for that.")
		return false
	}
	return true
}

func (p *adminPlugin) sendraw(cmd *mup.Command) {
	if !p.checkLogin(cmd, adminUser) {
		return
	}

	var args struct{ Account, Text string }
	cmd.Args(&args)
	if args.Account == "" {
		args.Account = cmd.Account
	}
	p.plugger.Send(mup.ParseOutgoing(args.Account, args.Text))
	p.plugger.Sendf(cmd, "Done.")
}
