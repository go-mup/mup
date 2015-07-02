package mup

import (
	"fmt"
	"time"

	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
	"gopkg.in/tomb.v2"
	"strings"
	"sync"
)

type accountManager struct {
	tomb     tomb.Tomb
	config   Config
	session  *mgo.Session
	database *mgo.Database
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
	Host        string
	TLS         bool
	TLSInsecure bool
	Nick        string
	Password    string
	Channels    []channelInfo
	LastId      bson.ObjectId
}

// NetworkTimeout's value is used as a timeout in a number of network-related activities.
// Plugins are encouraged to use that same value internally for consistent behavior.
var NetworkTimeout = 15 * time.Second

type channelInfo struct {
	Name string
	Key  string
}

func startAccountManager(config Config) (*accountManager, error) {
	logf("Starting account manager...")
	am := &accountManager{
		config:   config,
		clients:  make(map[string]accountClient),
		requests: make(chan interface{}),
		incoming: make(chan *Message),
	}
	am.session = config.Database.Session.Copy()
	am.database = config.Database.With(am.session)
	if err := createCollections(am.database); err != nil {
		logf("Cannot create collections: %v", err)
		return nil, fmt.Errorf("cannot create collections: %v", err)
	}
	am.tomb.Go(am.loop)
	return am, nil
}

const mb = 1024 * 1024

func createCollections(db *mgo.Database) error {
	capped := mgo.CollectionInfo{
		Capped:   true,
		MaxBytes: 4 * mb,
	}
	for _, name := range []string{"incoming", "outgoing"} {
		coll := db.C(name)
		err := coll.Create(&capped)
		if err != nil {
			if err.Error() == "collection already exists" {
				err = db.C("system.namespaces").Find(bson.M{"name": coll.FullName, "options.capped": true}).One(nil)
				if err == mgo.ErrNotFound {
					return fmt.Errorf("MongoDB collection %q already exists but is not capped", coll.FullName)
				}
			} else {
				return err
			}
		}
	}
	return nil
}

func (am *accountManager) Stop() error {
	logf("Account manager stop requested. Waiting...")
	am.tomb.Kill(errStop)
	err := am.tomb.Wait()
	am.session.Close()
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
	var wg sync.WaitGroup
	wg.Add(len(am.clients))
	for _, client := range am.clients {
		client := client
		go func() {
			client.Stop()
			wg.Done()
		}()
	}
	wg.Wait()
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
	var incoming = am.database.C("incoming")
	var accounts = am.database.C("accounts")
	for am.tomb.Alive() {
		am.session.Refresh()
		select {
		case msg := <-am.incoming:
			if msg.Command == cmdPong {
				if strings.HasPrefix(msg.Text, "sent:") {
					// TODO Ensure it's a valid ObjectId.
					lastId := bson.ObjectIdHex(msg.Text[5:])
					err := accounts.UpdateId(msg.Account, bson.D{{"$set", bson.D{{"lastid", lastId}}}})
					if err != nil {
						logf("Cannot update account with last sent message id: %v", err)
						am.tomb.Kill(err)
					}
				}
			} else {
				err := incoming.Insert(msg)
				if err != nil {
					logf("Cannot insert incoming message: %v", err)
					am.tomb.Kill(err)
				}
			}
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

func (m *accountManager) accountOn(name string) bool {
	if m.config.Accounts == nil {
		return true
	}
	for _, cname := range m.config.Accounts {
		if name == cname {
			return true
		}
	}
	return false
}

func (am *accountManager) handleRefresh() {
	var infos []accountInfo
	err := am.database.C("accounts").Find(nil).All(&infos)
	if err != nil {
		// TODO Reduce frequency of logged messages if the database goes down.
		logf("Cannot fetch account information from the database: %v", err)
		return
	}

	good := make(map[string]bool)
	for i := range infos {
		info := &infos[i]
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
			client = startIrcClient(info, am.incoming)
			am.clients[info.Name] = client
			go am.tail(client)
		} else {
			client.UpdateInfo(info)
		}
	}
}

func (am *accountManager) tail(client accountClient) error {
	session := am.session.Copy()
	defer session.Close()
	database := am.database.With(session)
	outgoing := database.C("outgoing")
	incoming := database.C("incoming")

	// Tailing is more involved than it ought to be. The complexity comes
	// from the fact that there are three ways to look for a new message,
	// from cheapest to most expensive:
	//
	// - The tail got a new message before the timeout
	// - The tail has timed out, but the cursor is still valid
	// - The tail has failed and the cursor is now invalid
	//
	// The logic below knows how to retry on all three, and also when there
	// are arbitrary communication errors.

	lastId := client.LastId()
	if lastId == "" {
		lastId = bson.ObjectId("\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00")
	}

	for am.tomb.Alive() && client.Alive() {

		// Prepare a new tailing iterator.
		session.Refresh()
		query := outgoing.Find(bson.D{{"_id", bson.D{{"$gt", lastId}}}, {"account", client.AccountName()}})
		iter := query.Sort("$natural").Tail(2 * time.Second)

		// Loop while iterator remains valid.
		for am.tomb.Alive() && client.Alive() && iter.Err() == nil {
			var msg *Message
			for iter.Next(&msg) {
				debugf("[%s] Tail iterator got outgoing message: %s", msg.Account, msg.String())
				select {
				case client.Outgoing() <- msg:
					// Send back to plugins for outgoing message handling.
					msg.Time = time.Now()
					err := incoming.Insert(msg)
					if err != nil && !mgo.IsDup(err) {
						logf("[%s] Cannot insert outgoing message for plugin handling: %v", msg.Account, err)
					}
					lastId = msg.Id
					msg = nil
				case <-client.Dying():
					iter.Close()
					return nil
				}
			}
			if !iter.Timeout() {
				break
			}
		}

		err := iter.Close()
		if err != nil && am.tomb.Alive() {
			logf("Error iterating over outgoing collection: %v", err)
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
