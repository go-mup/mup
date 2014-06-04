package mup

import (
	"fmt"
	"time"

	"labix.org/v2/mgo"
	"labix.org/v2/mgo/bson"
	"launchpad.net/tomb"
	"sync"
)

type Plugin interface {
	Start() error
	Stop() error
	Handle(msg *Message) error
}

type pluginManager struct {
	tomb     tomb.Tomb
	config   Config
	session  *mgo.Session
	database *mgo.Database
	plugins  map[string]Plugin
	requests chan interface{}
	incoming chan *Message
	outgoing *mgo.Collection
}

func startPluginManager(config Config) (*pluginManager, error) {
	logf("Starting plugins...")
	m := &pluginManager{
		config:   config,
		plugins:  make(map[string]Plugin),
		requests: make(chan interface{}),
		incoming: make(chan *Message),
	}
	m.session = config.Database.Session.Copy()
	m.database = config.Database.With(m.session)
	m.outgoing = m.database.C("outgoing")
	go m.loop()
	go m.tail()
	return m, nil
}

type mreqStop struct{}

func (m *pluginManager) Stop() error {
	log("Plugins stop requested. Waiting...")
	m.tomb.Kill(errStop)
	err := m.tomb.Wait()
	m.session.Close()
	logf("Plugins stopped (%v).", err)
	if err != errStop {
		return err
	}
	return nil
}

type mreqRefresh struct{ done chan struct{} }

// Refresh forces reloading all plguins information from the database.
func (m *pluginManager) Refresh() {
	req := mreqRefresh{make(chan struct{})}
	m.requests <- req
	<-req.done
}

func (m *pluginManager) die() {
	defer m.tomb.Done()

	var wg sync.WaitGroup
	wg.Add(len(m.plugins))
	for _, p := range m.plugins {
		p := p
		go func() {
			p.Stop()
			wg.Done()
		}()
	}
	wg.Wait()
}

func (m *pluginManager) loop() {
	defer m.die()

	m.handleRefresh()
	var refresh <-chan time.Time
	if m.config.Refresh > 0 {
		ticker := time.NewTicker(m.config.Refresh)
		defer ticker.Stop()
		refresh = ticker.C
	}
	for m.tomb.Err() == tomb.ErrStillAlive {
		m.session.Refresh()
		select {
		case msg := <-m.incoming:
			for name, plugin := range m.plugins {
				// TODO Check if msg.Id > plugin's last handled id
				err := plugin.Handle(msg)
				if err != nil {
					logf("Plugin %q failed to handle message: %s", name, msg)
				}
				// TODO Record last handled id in plugin.
			}
		case req := <-m.requests:
			switch r := req.(type) {
			case mreqRefresh:
				m.handleRefresh()
				close(r.done)
			default:
				panic("unknown request received by plugin manager")
			}
		case <-refresh:
			m.handleRefresh()
		case <-m.tomb.Dying():
		}
	}

}

type pluginInfo struct {
	Name string
}

func (m *pluginManager) handleRefresh() {
	var infos []pluginInfo
	err := m.database.C("plugins").Find(nil).All(&infos)
	if err != nil {
		// TODO Reduce frequency of logged messages if the database goes down.
		logf("Cannot fetch server information from the database: %v", err)
		return
	}

	// TODO Stop and remove all plugins that were removed or changed.

	// Add all plugins that are new or were changed.
	for i := range infos {
		info := &infos[i]
		if plugin, ok := m.plugins[info.Name]; !ok {
			plugin, err = m.startPlugin(info)
			if err != nil {
				logf("Cannot start plugin %q: %v", info.Name, err)
				continue
			}
			m.plugins[info.Name] = plugin
		}
		// TODO The changed bit.
	}
}

func (m *pluginManager) startPlugin(info *pluginInfo) (Plugin, error) {
	// TODO Send plugin's last id to tail for a potential rollback.
	newPlugin, ok := registeredPlugins[info.Name]
	if !ok {
		logf("Enabled plugin is not registered: %s", info.Name)
		return nil, fmt.Errorf("plugin %q not registered", info.Name)
	}
	plugger := newPlugger(m.send)
	return newPlugin(plugger), nil
}

func (m *pluginManager) send(msg *Message) error {
	return m.outgoing.Insert(msg)
}

func (m *pluginManager) tail() {
	session := m.session.Copy()
	defer session.Close()
	database := m.database.With(session)
	incoming := database.C("incoming")

	// See comment on the bridge.tail for more details on this procedure.

	// TODO Start iteration from the oldest lastid for all plugins.
	lastId := bson.ObjectId("\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00")

	for m.tomb.Err() == tomb.ErrStillAlive {

		// Prepare a new tailing iterator.
		session.Refresh()
		query := incoming.Find(bson.D{{"_id", bson.D{{"$gt", lastId}}}})
		iter := query.Sort("$natural").Tail(2 * time.Second)

		// Loop while iterator remains valid.
		for {
			var msg *Message
			for iter.Next(&msg) {
				debugf("[%s] Tail iterator got incoming message: %s", msg.Server, msg.String())
				select {
				case m.incoming <- msg:
					lastId = msg.Id
					msg = nil
				case <-m.tomb.Dying():
					iter.Close()
					return
				}
			}
			if iter.Err() == nil && iter.Timeout() && m.tomb.Err() == tomb.ErrStillAlive {
				// Iterator has timed out, but is still good for a retry.
				continue
			}
			break
		}

		// Iterator is not valid anymore.
		if err := iter.Close(); err != nil {
			logf("Error iterating over incoming collection: %v", err)
		}

		// Only sleep if a stop was not requested. Speeds tests up a bit.
		if m.tomb.Err() == tomb.ErrStillAlive {
			time.Sleep(100 * time.Millisecond)
		}
	}
}
