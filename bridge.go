package mup

import (
	"fmt"
	"time"

	"labix.org/v2/mgo"
	"labix.org/v2/mgo/bson"
	"launchpad.net/tomb"
	"strings"
	"sync"
)

type BridgeConfig struct {
	Database    string
	AutoRefresh time.Duration // Every few seconds by default; set to -1 to disable.
}

type Bridge struct {
	tomb     tomb.Tomb
	stopping bool
	config   BridgeConfig
	session  *mgo.Session
	servers  map[string]*ircServer
	requests chan interface{}
	incoming chan *Message
}

func StartBridge(config *BridgeConfig) (*Bridge, error) {
	logf("Starting server...")
	b := &Bridge{
		config:   *config,
		servers:  make(map[string]*ircServer),
		requests: make(chan interface{}),
		incoming: make(chan *Message),
	}
	if b.config.AutoRefresh == 0 {
		b.config.AutoRefresh = 3 * time.Second
	}
	var err error
	b.session, err = mgo.Dial(b.config.Database)
	if err != nil {
		logf("Could not connect to database %s: %v", config.Database, err)
		return nil, err
	}
	err = b.createCollections()
	if err != nil {
		logf("Cannot create collections: %v", err)
		return nil, fmt.Errorf("cannot create collections: %v", err)
	}
	logf("Connected to database: %s", config.Database)
	go b.loop()
	return b, nil
}

const mb = 1024 * 1024

func (b *Bridge) createCollections() error {
	capped := mgo.CollectionInfo{
		Capped:   true,
		MaxBytes: 4 * mb,
	}
	for _, c := range []string{"incoming", "outgoing"} {
		err := b.session.DB("").C(c).Create(&capped)
		if err != nil && err.Error() != "collection already exists" {
			return err
		}
	}
	return nil
}

type sreqStop struct{}

func (b *Bridge) Stop() error {
	log("Stop requested. Waiting...")
	b.tomb.Kill(errStop)
	err := b.tomb.Wait()
	b.session.Close()
	logf("Bridge stopped (%v).", err)
	if err != errStop {
		return err
	}
	return nil
}

type sreqRefresh struct{ done chan struct{} }

// Refresh forces reloading of the server information from the database.
func (b *Bridge) Refresh() {
	req := sreqRefresh{make(chan struct{})}
	b.requests <- req
	<-req.done
}

func (b *Bridge) die() {
	defer b.tomb.Done()

	var wg sync.WaitGroup
	wg.Add(len(b.servers))
	for _, server := range b.servers {
		server := server
		go func() {
			server.Stop()
			wg.Done()
		}()
	}
	wg.Wait()
}

func (b *Bridge) loop() {
	defer b.die()

	b.handleRefresh()
	var refresh <-chan time.Time
	if b.config.AutoRefresh > 0 {
		ticker := time.NewTicker(b.config.AutoRefresh)
		defer ticker.Stop()
		refresh = ticker.C
	}
	var incoming = b.session.DB("").C("incoming")
	var servers = b.session.DB("").C("servers")
	var ticker = time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for b.tomb.Err() == tomb.ErrStillAlive {
		b.session.Refresh()
		select {
		case msg := <-b.incoming:
			if msg.Cmd == cmdPong {
				if strings.HasPrefix(msg.Text, "sent:") {
					// TODO Ensure it's a valid ObjectId.
					lastId := bson.ObjectIdHex(msg.Text[5:])
					err := servers.Update(bson.D{{"name", msg.Server}}, bson.D{{"$set", bson.D{{"lastid", lastId}}}})
					if err != nil {
						logf("Cannot update server with last sent message id: %v", err)
						b.tomb.Kill(err)
					}
				}
			} else {
				err := incoming.Insert(msg)
				if err != nil {
					logf("Cannot insert incoming message: %v", err)
					b.tomb.Kill(err)
				}
			}
		case req := <-b.requests:
			switch r := req.(type) {
			case sreqRefresh:
				b.handleRefresh()
				close(r.done)
			}
		case <-refresh:
			b.handleRefresh()
		case <-b.tomb.Dying():
		}
	}

}

func (b *Bridge) handleRefresh() {
	if b.stopping {
		return
	}
	var infos []serverInfo
	err := b.session.DB("").C("servers").Find(nil).All(&infos)
	if err != nil {
		// TODO Reduce frequency of logged messages if the database goes down.
		logf("Cannot fetch server information from the database: %v", err)
		return
	}

	// Drop dead or deleted servers.
NextServer:
	for _, server := range b.servers {
		select {
		case <-server.Dying:
		default:
			for i := range infos {
				if server.Name == infos[i].Name {
					continue NextServer
				}
			}
		}
		server.Stop()
		delete(b.servers, server.Name)
	}

	// Bring new servers up and update existing ones.
	for i := range infos {
		info := &infos[i]
		if info.Nick == "" {
			info.Nick = "mup"
		}
		if server, ok := b.servers[info.Name]; !ok {
			server = startIrcServer(info, b.incoming)
			b.servers[info.Name] = server
			go b.tail(server)
		} else {
			server.UpdateInfo(info)
		}
	}
}

func (b *Bridge) tail(server *ircServer) {
	session := b.session.Copy()
	defer session.Close()

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

	lastId := server.LastId
	if lastId == "" {
		lastId = bson.ObjectId("\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00")
	}

	for b.tomb.Err() == tomb.ErrStillAlive {

		// Prepare a new tailing iterator.
		session.Refresh()
		outgoing := session.DB("").C("outgoing")
		query := outgoing.Find(bson.D{{"_id", bson.D{{"$gt", lastId}}}, {"server", server.Name}})
		iter := query.Sort("$natural").Tail(2 * time.Second)

		// Loop while iterator remains valid.
		for {
			var msg *Message
			for iter.Next(&msg) {
				debugf("[%s] Tail iterator got outgoing message: %s", msg.Server, msg.String())
				select {
				case server.Outgoing <- msg:
					lastId = msg.Id
					msg = nil
				case <-server.Dying:
					iter.Close()
					return
				}
			}
			if iter.Err() == nil && iter.Timeout() && b.tomb.Err() == tomb.ErrStillAlive {
				// Iterator has timed out, but is still good for a retry.
				continue
			}
			break
		}

		// Iterator is not valid anymore.
		if err := iter.Close(); err != nil {
			logf("Error iterating over outgoing collection: %v", err)
		}

		// Only sleep if a stop was not requested. Speeds tests up a bit.
		if b.tomb.Err() == tomb.ErrStillAlive {
			time.Sleep(100 * time.Millisecond)
		}
	}
}
