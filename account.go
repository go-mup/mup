package mup

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"gopkg.in/mgo.v2/bson"
	"gopkg.in/tomb.v2"
)

type accountManager struct {
	tomb     tomb.Tomb
	config   Config
	db       *sql.DB
	clients  map[string]accountClient
	requests chan interface{}
	incoming chan *Message
}

type accountClient interface {
	Alive() bool
	Stop() error

	AccountName() string
	Dying() <-chan struct{}
	Outgoing() chan *Message
	LastId() bson.ObjectId
	UpdateInfo(info *accountInfo)
}

type accountInfo struct {
	Name        string `bson:"_id"`
	Kind        string
	Endpoint    string
	Host        string
	TLS         bool
	TLSInsecure bool
	Nick        string
	Identify    string
	Password    string
	LastId      bson.ObjectId

	Channels []channelInfo
}

const accountColumns = "name,kind,endpoint,host,tls,tls_insecure,nick,identify,password,last_id"
const accountPlacers = "?,?,?,?,?,?,?,?,?,?"

func (ai *accountInfo) refs() []interface{} {
	return []interface{}{&ai.Name, &ai.Kind, &ai.Endpoint, &ai.Host, &ai.TLS, &ai.TLSInsecure, &ai.Nick, &ai.Identify, &ai.Password, &ai.LastId}
}

// NetworkTimeout's value is used as a timeout in a number of network-related activities.
// Plugins are encouraged to use that same value internally for consistent behavior.
var NetworkTimeout = 15 * time.Second

type channelInfo struct {
	Account string
	Name    string
	Key     string
}

const channelColumns = "account,name,key"
const channelPlacers = "?,?,?"

func (ci *channelInfo) refs() []interface{} {
	return []interface{}{&ci.Account, &ci.Name, &ci.Key}
}

func startAccountManager(config Config) (*accountManager, error) {
	logf("Starting account manager...")
	am := &accountManager{
		config:   config,
		clients:  make(map[string]accountClient),
		requests: make(chan interface{}),
		incoming: make(chan *Message),
	}
	am.db = config.DB
	am.tomb.Go(am.loop)
	return am, nil
}

const mb = 1024 * 1024

func (am *accountManager) Stop() error {
	logf("Account manager stop requested. Waiting...")
	am.tomb.Kill(errStop)
	err := am.tomb.Wait()
	logf("Account manager stopped (%v).", err)
	if err != errStop {
		return err
	}
	return nil
}

type accountRequestRefresh struct{ done chan struct{} }

// Refresh forces reloading all account information from the database.
func (am *accountManager) Refresh() {
	req := accountRequestRefresh{make(chan struct{})}
	am.requests <- req
	<-req.done
}

func (am *accountManager) die() {
	pending := len(am.clients)
	stopped := make(chan bool, pending)
	for _, client := range am.clients {
		client := client
		go func() {
			client.Stop()
			stopped <- true
		}()
	}

	// The clients have a reference to am.incoming, and if they're blocked
	// attempting to send a message into it, their only alternative to
	// force a stop would be to throw out that message. Instead, accept
	// incoming messages while there are still clients alive.
	for pending > 0 {
		select {
		case msg := <-am.incoming:
			am.handleIncoming(msg)
		case <-stopped:
			pending--
		}
	}
}

func (am *accountManager) loop() error {
	defer am.die()

	if am.config.Accounts != nil && len(am.config.Accounts) == 0 {
		<-am.tomb.Dying()
		return nil
	}

	am.handleRefresh()
	var refresh <-chan time.Time
	if am.config.Refresh > 0 {
		ticker := time.NewTicker(am.config.Refresh)
		defer ticker.Stop()
		refresh = ticker.C
	}
	for am.tomb.Alive() {
		select {
		case msg := <-am.incoming:
			am.handleIncoming(msg)
		case req := <-am.requests:
			switch r := req.(type) {
			case accountRequestRefresh:
				am.handleRefresh()
				close(r.done)
			default:
				panic("unknown request received by account manager")
			}
		case <-refresh:
			am.handleRefresh()
		case <-am.tomb.Dying():
		}
	}

	return nil
}

func (am *accountManager) accountOn(name string) bool {
	if am.config.Accounts == nil {
		return true
	}
	for _, cname := range am.config.Accounts {
		if name == cname {
			return true
		}
	}
	return false
}

func (am *accountManager) handleIncoming(msg *Message) {
	if msg.Command == cmdPong {
		if strings.HasPrefix(msg.Text, "sent:") {
			// TODO Ensure it's a valid ObjectId.
			lastId := bson.ObjectIdHex(msg.Text[5:])
			_, err := am.db.Exec("UPDATE account SET last_id=? WHERE name=?", lastId, msg.Account)
			if err != nil {
				logf("Cannot update account with last sent message id: %v", err)
				am.tomb.Kill(err)
			}
		}
	} else {
		// FIXME Rework that ID logic.
		if msg.Id == "" {
			msg.Id = bson.NewObjectId()
		}
		_, err := am.db.Exec("INSERT INTO incoming ("+messageColumns+") VALUES ("+messagePlacers+")", msg.refs()...)
		if err != nil {
			logf("Cannot insert incoming message: %v", err)
			am.tomb.Kill(err)
		}
	}
}

func (am *accountManager) handleRefresh() {
	tx, err := am.db.Begin()
	if err != nil {
		logf("Cannot begin database transaction: %v", err)
		return
	}
	defer tx.Rollback()

	var infos []accountInfo
	var cinfos = make(map[string][]channelInfo)

	rows, err := tx.Query("SELECT " + accountColumns + " FROM account")
	if err != nil {
		logf("Cannot fetch account information from the database: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var info accountInfo
		err = rows.Scan(info.refs()...)
		if err != nil {
			logf("Cannot parse database account information: %v", err)
			return
		}
		infos = append(infos, info)
	}

	rows, err = tx.Query("SELECT " + channelColumns + " FROM channel")
	if err != nil {
		logf("Cannot fetch channel information from the database: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var cinfo channelInfo
		err = rows.Scan(cinfo.refs()...)
		if err != nil {
			logf("Cannot parse database channel information: %v", err)
			return
		}
		cinfos[cinfo.Account] = append(cinfos[cinfo.Account], cinfo)
	}

	good := make(map[string]bool)
	for i := range infos {
		info := &infos[i]
		info.Channels = cinfos[info.Name]
		if am.accountOn(info.Name) {
			good[info.Name] = true
		}
	}

	// Drop clients for dead or deleted accounts.
	for _, client := range am.clients {
		select {
		case <-client.Dying():
		default:
			if good[client.AccountName()] {
				continue
			}
		}
		client.Stop()
		delete(am.clients, client.AccountName())
	}

	// Bring new clients up and update existing ones.
	for i := range infos {
		info := &infos[i]
		if !good[info.Name] {
			continue
		}
		if info.Nick == "" {
			info.Nick = "mup"
		}
		if client, ok := am.clients[info.Name]; !ok {
			switch info.Kind {
			case "irc", "":
				client = startIrcClient(info, am.incoming)
			case "telegram":
				client = startTgClient(info, am.incoming)
			case "webhook":
				client = startWebHookClient(info, am.incoming)
			default:
				continue
			}
			am.clients[info.Name] = client
			go am.tail(client)
		} else {
			client.UpdateInfo(info)
		}
	}
}

func (am *accountManager) tail(client accountClient) error {
	lastId := client.LastId()
	if lastId == "" {
		lastId = bson.ObjectId("\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00")
	}

	for am.tomb.Alive() && client.Alive() {

		// TODO Prepare this statement.
		rows, err := am.db.Query("SELECT "+messageColumns+" FROM outgoing WHERE id>? AND account=? ORDER BY id", lastId, client.AccountName())
		if err != nil {
			logf("Error retrieving outgoing messages: %v", err)
		} else {
			for rows.Next() {
				var msg Message
				err := rows.Scan(msg.refs()...)
				if err != nil {
					logf("Error parsing outgoing messages: %v", err)
				}
				debugf("[%s] Tail iterator got outgoing message: %s", msg.Account, msg.String())
				select {
				case client.Outgoing() <- &msg:
					// Send back to plugins for outgoing message handling.
					_, err := am.db.Exec("INSERT INTO incoming ("+messageColumns+") VALUES ("+messagePlacers+")", msg.refs()...)
					// FIXME These will be duped on resends, so the error needs to be ignored.
					//       Also, this logic means we must make sure IDs are unique across incoming
					//       and outgoing so the conflict is indeed for the exact same message, that
					//       was already attempted to be sent before.
					if err != nil {
						fmt.Printf("[%s] Cannot insert outgoing message (%q) for plugin handling: %v\n", msg.Account, msg.String(), err)
						panic("BOOM")
						logf("[%s] Cannot insert outgoing message for plugin handling: %v", msg.Account, err)
						am.tomb.Kill(err)
					}
					lastId = msg.Id
				case <-client.Dying():
					return nil
				}
			}
			err := rows.Close()
			if err != nil && am.tomb.Alive() {
				logf("Error iterating over outgoing collection: %v", err)
			}
		}

		select {
		case <-time.After(100 * time.Millisecond):
		case <-am.tomb.Dying():
			return nil
		case <-client.Dying():
			return nil
		}
	}
	return nil
}
