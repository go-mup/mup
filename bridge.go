package mup

import (
	"time"

	"labix.org/v2/mgo"
	"launchpad.net/tomb"
)

type BridgeConfig struct {
	Database string
	Refresh  time.Duration
}

type Bridge struct {
	config   BridgeConfig
	session  *mgo.Session
	servers  map[string]*ircServer
	tomb     tomb.Tomb
	requests chan interface{}
	r        chan *Message
	w        chan *Message
}

func StartBridge(config *BridgeConfig) (*Bridge, error) {
	logf("Starting server...")
	b := &Bridge{
		config:   *config,
		servers:  make(map[string]*ircServer),
		requests: make(chan interface{}),
		r:        make(chan *Message),
	}
	session, err := mgo.Dial(b.config.Database)
	if err != nil {
		logf("Could not connect to database %s: %v", config.Database, err)
		return nil, err
	}
	logf("Connected to database: %s", config.Database)
	b.session = session
	go b.loop()
	return b, nil
}

func (b *Bridge) Stop() error {
	log("Stop requested. Waiting...")
	b.tomb.Kill(nil)
	for _, server := range b.servers {
		server.Stop()
	}
	err := b.tomb.Wait()
	b.session.Close()
	logf("Stopped (%v).", err)
	return err
}

func (b *Bridge) loop() {
	b.handleRefresh()
	var refresh <-chan time.Time
	if b.config.Refresh > 0 {
		ticker := time.NewTicker(b.config.Refresh)
		defer ticker.Stop()
		refresh = ticker.C
	}
	for b.tomb.Err() == tomb.ErrStillAlive {
		select {
		case msg := <-b.r:
			err := b.session.DB("").C("incoming").Insert(msg)
			if err != nil {
				logf("Cannot insert incoming message: %v", err)
				b.tomb.Kill(err)
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
	b.tomb.Done()
}

type sreqRefresh struct {
	done chan struct{}
}

// Refresh forces reloading of the server information from the database.
func (b *Bridge) Refresh() {
	req := sreqRefresh{make(chan struct{})}
	b.requests <- req
	<-req.done
}

func (b *Bridge) handleRefresh() {
	var infos []serverInfo
	err := b.session.DB("").C("servers").Find(nil).All(&infos)
	if err != nil {
		// TODO Reduce frequency of logged messages if the database goes down.
		logf("Cannot fetch server information from the database: %v", err)
		return
	}
	logf("Got %d servers", len(infos))

	for _, info := range infos {
		if info.Nick == "" {
			info.Nick = "mup"
		}
		if server, ok := b.servers[info.Name]; !ok {
			b.servers[info.Name] = startIrcServer(&info, b.r)
		} else {
			server.UpdateInfo(&info)
		}
	}
}
