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

type bridge struct {
	tomb     tomb.Tomb
	config   Config
	session  *mgo.Session
	database *mgo.Database
	servers  map[string]*ircServer
	requests chan interface{}
	incoming chan *Message
}

func startBridge(config Config) (*bridge, error) {
	logf("Starting bridge...")
	b := &bridge{
		config:   config,
		servers:  make(map[string]*ircServer),
		requests: make(chan interface{}),
		incoming: make(chan *Message),
	}
	b.session = config.Database.Session.Copy()
	b.database = config.Database.With(b.session)
	if err := b.createCollections(); err != nil {
		logf("Cannot create collections: %v", err)
		return nil, fmt.Errorf("cannot create collections: %v", err)
	}
	go b.loop()
	return b, nil
}

const mb = 1024 * 1024

func (b *bridge) createCollections() error {
	capped := mgo.CollectionInfo{
		Capped:   true,
		MaxBytes: 4 * mb,
	}
	for _, c := range []string{"incoming", "outgoing"} {
		err := b.database.C(c).Create(&capped)
		if err != nil && err.Error() != "collection already exists" {
			return err
		}
	}
	return nil
}

type sreqStop struct{}

func (b *bridge) Stop() error {
	log("Bridge stop requested. Waiting...")
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

// Refresh forces reloading all server information from the database.
func (b *bridge) Refresh() {
	req := sreqRefresh{make(chan struct{})}
	b.requests <- req
	<-req.done
}

func (b *bridge) die() {
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

func (b *bridge) loop() {
	defer b.die()

	b.handleRefresh()
	var refresh <-chan time.Time
	if b.config.Refresh > 0 {
		ticker := time.NewTicker(b.config.Refresh)
		defer ticker.Stop()
		refresh = ticker.C
	}
	var incoming = b.database.C("incoming")
	var servers = b.database.C("servers")
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

func (b *bridge) handleRefresh() {
	var infos []serverInfo
	err := b.database.C("servers").Find(nil).All(&infos)
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

func (b *bridge) tail(server *ircServer) {
	session := b.session.Copy()
	defer session.Close()
	database := b.database.With(session)
	outgoing := database.C("outgoing")

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
