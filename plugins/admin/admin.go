package admin

import (
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
	"gopkg.in/mup.v0"
	"gopkg.in/mup.v0/schema"
	"time"
)

var Plugin = mup.PluginSpec{
	Name:     "admin",
	Help:     "Exposes the bot administration commands.",
	Start:    start,
	Commands: Commands,
}

var Commands = schema.Commands{{
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

type adminPlugin struct {
	plugger *mup.Plugger

	loggedIn map[string]bool

	loginAttemptStart time.Time
	loginAttemptCount int
}

func start(plugger *mup.Plugger) mup.Stopper {
	return &adminPlugin{
		plugger:  plugger,
		loggedIn: make(map[string]bool),
	}
}

func (p *adminPlugin) Stop() error {
	return nil
}

func (p *adminPlugin) HandleCommand(cmd *mup.Command) {
	switch cmd.Name() {
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
	Password     string
	AttemptStart time.Time
	AttemptCount int
}

func (p *adminPlugin) login(cmd *mup.Command) {
	var args struct{ Password string }
	cmd.Args(&args)

	session, c := p.plugger.Collection("", 0)
	defer session.Close()

	users := session.DB(c.Database.Name).C("users")
	query := bson.D{{"account", cmd.Account}, {"nick", cmd.Nick}}

	var user userInfo
	err := users.Find(query).One(&user)
	if err == mgo.ErrNotFound {
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
		p.plugger.Logf("resetting quota")
		user.AttemptCount = 0
		user.AttemptStart = time.Now()
	}
	user.AttemptCount++
	if user.AttemptCount > burstQuota {
		p.plugger.Sendf(cmd, "Slow down.")
		return
	}
	// TODO Use scrypt for passwords instead.
	if user.Password != args.Password {
		err := users.Update(query, bson.D{{"$set", bson.D{{"attemptstart", user.AttemptStart}, {"attemptcount", user.AttemptCount}}}})
		if err != nil {
			p.plugger.Logf("Cannot update login attempt for nick %q at %s: %v", cmd.Nick, cmd.Account, err)
			p.plugger.Sendf(cmd, "Oops: there was an error while recording your login attempt.")
		} else {
			p.plugger.Sendf(cmd, "Nope.")
		}
		return
	}
	p.plugger.Sendf(cmd, "Okay.")
	p.loggedIn[cmd.Nick] = true
}

func (p *adminPlugin) checkLogin(cmd *mup.Command) bool {
	if p.loggedIn[cmd.Nick] {
		return true
	}
	p.plugger.Sendf(cmd, "Must login for that.")
	return false
}

func (p *adminPlugin) sendraw(cmd *mup.Command) {
	if !p.checkLogin(cmd) {
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
