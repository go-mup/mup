package mup

import (
	"bytes"
	"fmt"
	"time"

	"gopkg.in/tomb.v2"
	"labix.org/v2/mgo"
	"labix.org/v2/mgo/bson"
	"strings"
	"sync"
)

type Plugin interface {
	Stop() error
	Handle(msg *Message) error
}

var registeredPlugins = make(map[string]func(*Plugger) Plugin)

func RegisterPlugin(name string, startPlugin func(*Plugger) Plugin) {
	registeredPlugins[name] = startPlugin
}

type pluginInfo struct {
	Name     string
	LastId   bson.ObjectId `bson:",omitempty"`
	Settings bson.Raw
	Targets  bson.Raw
	State    bson.Raw
}

type pluginHandler struct {
	info    pluginInfo
	plugger *Plugger
	plugin  Plugin
}

type pluginManager struct {
	tomb     tomb.Tomb
	config   Config
	session  *mgo.Session
	database *mgo.Database
	plugins  map[string]*pluginHandler
	requests chan interface{}
	incoming chan *Message
	outgoing *mgo.Collection
	rollback chan bson.ObjectId
}

func startPluginManager(config Config) (*pluginManager, error) {
	logf("Starting plugins...")
	m := &pluginManager{
		config:   config,
		plugins:  make(map[string]*pluginHandler),
		requests: make(chan interface{}),
		incoming: make(chan *Message),
		rollback: make(chan bson.ObjectId),
	}
	m.session = config.Database.Session.Copy()
	m.database = config.Database.With(m.session)
	m.outgoing = m.database.C("outgoing")
	m.tomb.Go(m.loop)
	m.tomb.Go(m.tail)
	return m, nil
}

type pluginRequestStop struct{}

func (m *pluginManager) Stop() error {
	logf("Plugin manager stop requested. Waiting...")
	m.tomb.Kill(errStop)
	err := m.tomb.Wait()
	m.session.Close()
	logf("Plugin manager stopped (%v).", err)
	if err != errStop {
		return err
	}
	return nil
}

type pluginRequestRefresh struct{ done chan struct{} }

// Refresh forces reloading all plguins information from the database.
func (m *pluginManager) Refresh() {
	req := pluginRequestRefresh{make(chan struct{})}
	m.requests <- req
	<-req.done
}

func (m *pluginManager) die() {
	var wg sync.WaitGroup
	wg.Add(len(m.plugins))
	for _, handler := range m.plugins {
		plugin := handler.plugin
		go func() {
			plugin.Stop()
			wg.Done()
		}()
	}
	wg.Wait()
}

func (m *pluginManager) loop() error {
	defer m.die()

	m.handleRefresh()
	var refresh <-chan time.Time
	if m.config.Refresh > 0 {
		ticker := time.NewTicker(m.config.Refresh)
		defer ticker.Stop()
		refresh = ticker.C
	}
	plugins := m.database.C("plugins")
	for m.tomb.Alive() {
		m.session.Refresh()
		select {
		case msg := <-m.incoming:
			if msg.Cmd == cmdPong {
				continue
			}
			for name, handler := range m.plugins {
				if handler.info.LastId >= msg.Id || handler.plugger.Target(msg) == nil {
					continue
				}
				handler.info.LastId = msg.Id
				err := handler.plugin.Handle(msg)
				if err != nil {
					logf("Plugin %q failed to handle message: %s: %v", name, msg, err)
				}
				err = plugins.Update(bson.D{{"name", name}}, bson.D{{"$set", bson.D{{"lastid", msg.Id}}}})
				if err != nil {
					logf("Cannot update last message id for plugin %q: %v", name, err)
					// TODO How to recover properly from this?
				}
			}
		case req := <-m.requests:
			switch r := req.(type) {
			case pluginRequestRefresh:
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
	return nil
}

func pluginChanged(a, b *pluginInfo) bool {
	return !bytes.Equal(a.Settings.Data, b.Settings.Data) || !bytes.Equal(a.Targets.Data, b.Targets.Data)
}

func (m *pluginManager) handleRefresh() {
	var infos []pluginInfo
	err := m.database.C("plugins").Find(nil).All(&infos)
	if err != nil {
		// TODO Reduce frequency of logged messages if the database goes down.
		logf("Cannot fetch server information from the database: %v", err)
		return
	}

	// Start new plugins, and stop/restart updated ones.
	var known = len(m.plugins)
	var found int
	var rollbackId bson.ObjectId
	for i := range infos {
		info := &infos[i]
		if handler, ok := m.plugins[info.Name]; ok {
			found++
			if !pluginChanged(&handler.info, info) {
				continue
			}
			logf("Plugin %q settings or targets changed. Stopping and restarting it.", info.Name)
			err := handler.plugin.Stop()
			if err != nil {
				logf("Plugin %q stopped with an error: %v", info.Name, err)
			}
			delete(m.plugins, info.Name)
		} else {
			logf("Plugin %q starting.", info.Name)
		}

		handler, err := m.startPlugin(info)
		if err != nil {
			logf("Plugin %q failed to start: %v", info.Name, err)
			continue
		}
		m.plugins[info.Name] = handler
		if rollbackId == "" || rollbackId > handler.info.LastId {
			rollbackId = handler.info.LastId
		}
	}

	// If there are known plugins that were not observed in the current
	// set of plugins, they must be stopped and removed.
	if known != found {
	NextPlugin:
		for name, handler := range m.plugins {
			for i := range infos {
				if infos[i].Name == name {
					continue NextPlugin
				}
			}
			logf("Plugin %q removed. Stopping it.", handler.info.Name)
			err := handler.plugin.Stop()
			if err != nil {
				logf("Plugin %q stopped with an error: %v", handler.info.Name, err)
			}
			delete(m.plugins, name)
		}
	}

	// If the last id observed by a plugin is older than the current
	// position of the tail iterator, the iterator must be restarted
	// at a previous position to avoid losing messages, so that plugins
	// may be restarted at any point without losing incoming messages.
	if rollbackId != "" {
		// Wake up tail iterator by injecting a dummy message. The iterator
		// won't be able to deliver this message because incoming is
		// consumed by this goroutine after this method returns.
		err := m.database.C("incoming").Insert(&Message{Cmd: cmdPong, Account: rollbackAccount, Text: rollbackText})
		if err != nil {
			logf("Cannot insert wake up message in incoming queue: %v", err)
			return
		}

		// Send oldest observed id to the tail loop for a potential rollback.
		select {
		case m.rollback <- rollbackId:
		case <-m.tomb.Dying():
			return
		}
	}
}

// rollbackLimit defines how long messages can be waiting in the
// incoming queue while still being submitted to plugins.
const (
	rollbackLimit   = 5 * time.Second
	rollbackAccount = "<rollback>"
	rollbackText    = "<rollback>"
)

func pluginKey(pluginName string) string {
	if i := strings.Index(pluginName, ":"); i >= 0 {
		return pluginName[:i]
	}
	return pluginName
}

func (m *pluginManager) startPlugin(info *pluginInfo) (*pluginHandler, error) {
	newPlugin, ok := registeredPlugins[pluginKey(info.Name)]
	if !ok {
		logf("Plugin is not registered: %s", pluginKey(info.Name))
		return nil, fmt.Errorf("plugin %q not registered", pluginKey(info.Name))
	}
	plugger := newPlugger(info.Name, m.sendMessage)
	plugger.setSettings(info.Settings)
	plugger.setTargets(info.Targets)
	plugin := newPlugin(plugger)
	handler := &pluginHandler{
		info:    *info,
		plugger: plugger,
		plugin:  plugin,
	}

	lastId := bson.NewObjectIdWithTime(time.Now().Add(-rollbackLimit))
	if !handler.info.LastId.Valid() || handler.info.LastId < lastId {
		handler.info.LastId = lastId
	}
	return handler, nil
}

func (m *pluginManager) sendMessage(msg *Message) error {
	return m.outgoing.Insert(msg)
}

const zeroId = bson.ObjectId("\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00")

func (m *pluginManager) tail() error {
	session := m.session.Copy()
	defer session.Close()
	database := m.database.With(session)
	incoming := database.C("incoming")

	// See comment on the bridge.tail for more details on this procedure.

	lastId := bson.NewObjectIdWithTime(time.Now().Add(-rollbackLimit))

NextTail:
	for m.tomb.Alive() {
		// Prepare a new tailing iterator.
		session.Refresh()
		query := incoming.Find(bson.D{{"_id", bson.D{{"$gt", lastId}}}})
		iter := query.Sort("$natural").Tail(2 * time.Second)

		// Loop while iterator remains valid.
		for {
			var msg *Message
			for iter.Next(&msg) {
				debugf("[%s] Tail iterator got incoming message: %s", msg.Account, msg.String())
			DeliverMsg:
				select {
				case m.incoming <- msg:
					lastId = msg.Id
					msg = nil
				case rollbackId := <-m.rollback:
					if rollbackId < lastId {
						logf("Rolling back tail iterator to consider older incoming messages")
						lastId = rollbackId
						iter.Close()
						continue NextTail
					}
					goto DeliverMsg
				case <-m.tomb.Dying():
					iter.Close()
					return nil
				}
			}
			if iter.Err() == nil && iter.Timeout() && m.tomb.Alive() {
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
		if m.tomb.Alive() {
			time.Sleep(100 * time.Millisecond)
		}
	}

	return nil
}
