package mup

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"time"

	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
	"gopkg.in/mup.v0/ldap"
	"gopkg.in/mup.v0/schema"
	"gopkg.in/tomb.v2"
)

// PluginSpec holds the specification of a plugin that may be registered with mup.
type PluginSpec struct {
	Name     string
	Help     string
	Start    func(p *Plugger) (Stopper, error)
	Commands schema.Commands
}

// Stopper is implemented by types that can run arbitrary background
// activities that can be stopped on request.
type Stopper interface {
	Stop() error
}

// MessageHandler is implemented by plugins that can handle raw messages.
type MessageHandler interface {
	HandleMessage(msg *Message) error
}

// CommandHandler is implemented by plugins that can handle commands.
type CommandHandler interface {
	HandleCommand(cmd *Command) error
}

type Command struct {
	*Message

	schema *schema.Command
	args   bson.Raw
}

func (c *Command) Schema() *schema.Command {
	return c.schema
}

func (c *Command) Args(result interface{}) {
	c.args.Unmarshal(result)
}

var registeredPlugins = make(map[string]*PluginSpec)

// RegisterPlugin registers with mup the plugin defined via the provided
// specification, so that it may be loaded when configured to do so.
func RegisterPlugin(spec *PluginSpec) {
	if spec.Name == "" {
		panic("cannot register plugin with an empty name")
	}
	if _, ok := registeredPlugins[spec.Name]; ok {
		panic("plugin already registered: " + spec.Name)
	}
	registeredPlugins[spec.Name] = spec
}

type pluginInfo struct {
	Name    string        `bson:"_id"`
	LastId  bson.ObjectId `bson:",omitempty"`
	Config  bson.Raw
	Targets bson.Raw
	State   bson.Raw
}

type pluginState struct {
	info    pluginInfo
	spec    *PluginSpec
	plugger *Plugger
	plugin  Stopper
}

type ldapInfo struct {
	Name   string      `bson:"_id"`
	Config ldap.Config `bson:",inline"`
}

type ldapState struct {
	raw  bson.Raw
	info ldapInfo
	conn *ldap.ManagedConn
}

type pluginManager struct {
	tomb     tomb.Tomb
	config   Config
	session  *mgo.Session
	database *mgo.Database
	requests chan interface{}
	incoming chan *Message
	outgoing *mgo.Collection
	rollback chan bson.ObjectId
	plugins  map[string]*pluginState
	ldaps    map[string]*ldapState

	ldapConns      map[string]*ldap.ManagedConn
	ldapConnsMutex sync.Mutex
}

func startPluginManager(config Config) (*pluginManager, error) {
	logf("Starting plugins...")
	m := &pluginManager{
		config:   config,
		plugins:  make(map[string]*pluginState),
		ldaps:    make(map[string]*ldapState),
		requests: make(chan interface{}),
		incoming: make(chan *Message),
		rollback: make(chan bson.ObjectId),
	}
	m.session = config.Database.Session.Copy()
	m.database = config.Database.With(m.session)
	m.outgoing = m.database.C("outgoing")
	m.tomb.Go(m.loop)
	return m, nil
}

type pluginRequestStop struct{}

func (m *pluginManager) Stop() error {
	if !m.tomb.Alive() {
		return m.tomb.Err()
	}
	logf("Plugin manager stop requested. Waiting...")
	select {
	case m.requests <- pluginRequestStop{}:
	case <-m.tomb.Dying():
	}
	err := m.tomb.Wait()
	m.session.Close()
	logf("Plugin manager stopped (%v).", err)
	if err != errStop {
		return err
	}
	return nil
}

type pluginRequestRefresh struct {
	done chan struct{}
}

// Refresh forces reloading all plugin information from the database.
func (m *pluginManager) Refresh() {
	req := pluginRequestRefresh{make(chan struct{})}
	select {
	case m.requests <- req:
		<-req.done
	case <-m.tomb.Dying():
	}
}

func (m *pluginManager) die() {
	var wg sync.WaitGroup
	wg.Add(len(m.plugins))
	for _, state := range m.plugins {
		stop := state.plugin.Stop
		go func() {
			stop()
			wg.Done()
		}()
	}

	// Clean this up first so m.ldapConn will never get a connection
	// after its managed connection loop has already terminated.
	m.ldapConnsMutex.Lock()
	m.ldapConns = nil
	m.ldapConnsMutex.Unlock()

	wg.Add(len(m.ldaps))
	for _, state := range m.ldaps {
		close := state.conn.Close
		go func() {
			close()
			wg.Done()
		}()
	}
	wg.Wait()
	m.tomb.Kill(errStop)
}

func (m *pluginManager) loop() error {
	defer m.die()

	m.tomb.Go(m.tail)

	m.handleRefresh()
	var refresh <-chan time.Time
	if m.config.Refresh > 0 {
		ticker := time.NewTicker(m.config.Refresh)
		defer ticker.Stop()
		refresh = ticker.C
	}
	plugins := m.database.C("plugins")
	for {
		m.session.Refresh()
		select {
		case msg := <-m.incoming:
			if msg.Command == cmdPong {
				continue
			}
			cmdName := schema.CommandName(msg.MupText)
			for name, state := range m.plugins {
				if state.info.LastId >= msg.Id || state.plugger.Target(msg) == nil {
					continue
				}
				state.info.LastId = msg.Id
				state.handle(msg, cmdName)
				err := plugins.UpdateId(name, bson.D{{"$set", bson.D{{"lastid", msg.Id}}}})
				if err != nil {
					logf("Cannot update last message id for plugin %q: %v", name, err)
					// TODO How to recover properly from this?
				}
			}
		case req := <-m.requests:
			switch req := req.(type) {
			case pluginRequestStop:
				return nil
			case pluginRequestRefresh:
				m.handleRefresh()
				close(req.done)
			default:
				panic("unknown request received by plugin manager")
			}
		case <-refresh:
			m.handleRefresh()
		}
	}
	return nil
}

func (m *pluginManager) handleRefresh() {
	m.refreshLdaps()
	m.refreshPlugins()
}

func (m *pluginManager) refreshLdaps() {
	changed := false
	defer func() {
		if changed {
			m.ldapConnsMutex.Lock()
			m.ldapConns = make(map[string]*ldap.ManagedConn)
			for name, state := range m.ldaps {
				m.ldapConns[name] = state.conn
			}
			m.ldapConnsMutex.Unlock()
		}
	}()

	// Start new LDAP instances, and stop/restart updated ones.
	var raw bson.Raw
	var infos = make([]ldapInfo, 0, len(m.ldaps))
	var found int
	var known = len(m.ldaps)
	iter := m.database.C("ldap").Find(nil).Iter()
	for iter.Next(&raw) {
		var info ldapInfo
		if err := raw.Unmarshal(&info); err != nil {
			logf("Cannot unmarshal LDAP document: %v", err)
			continue
		}
		infos = append(infos, info)
		if state, ok := m.ldaps[info.Name]; ok {
			found++
			if bytes.Equal(state.raw.Data, raw.Data) {
				continue
			}
			logf("LDAP connection %q changed. Closing and restarting it.", info.Name)
			err := state.conn.Close()
			if err != nil {
				logf("LDAP connection %q closed with an error: %v", info.Name, err)
			}
			delete(m.ldaps, info.Name)
		} else {
			logf("LDAP %q starting.", info.Name)
		}

		m.ldaps[info.Name] = &ldapState{
			raw:  raw,
			info: info,
			conn: ldap.DialManaged(&info.Config),
		}
		changed = true
	}
	if iter.Err() != nil {
		// TODO Reduce frequency of logged messages if the database goes down.
		logf("Cannot fetch LDAP connection information from the database: %v", iter.Err())
		return
	}

	// If there are known LDAPs that were not observed in the current
	// set of LDAPs, they must be stopped and removed.
	if known != found {
	NextLDAP:
		for name, state := range m.ldaps {
			for i := range infos {
				if infos[i].Name == name {
					continue NextLDAP
				}
			}
			logf("LDAP connection %q removed. Closing it.", state.info.Name)
			err := state.conn.Close()
			if err != nil {
				logf("LDAP connection %q closed with an error: %v", state.info.Name, err)
			}
			delete(m.ldaps, name)
			changed = true
		}
	}
}

func pluginChanged(a, b *pluginInfo) bool {
	return !bytes.Equal(a.Config.Data, b.Config.Data) || !bytes.Equal(a.Targets.Data, b.Targets.Data)
}

func (m *pluginManager) refreshPlugins() {
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
		if state, ok := m.plugins[info.Name]; ok {
			found++
			if !pluginChanged(&state.info, info) {
				continue
			}
			logf("Plugin %q config or targets changed. Stopping and restarting it.", info.Name)
			err := state.plugin.Stop()
			if err != nil {
				logf("Plugin %q stopped with an error: %v", info.Name, err)
			}
			delete(m.plugins, info.Name)
		} else {
			logf("Plugin %q starting.", info.Name)
		}

		state, err := m.startPlugin(info)
		if err != nil {
			logf("Plugin %q failed to start: %v", info.Name, err)
			continue
		}
		m.plugins[info.Name] = state
		if rollbackId == "" || rollbackId > state.info.LastId {
			rollbackId = state.info.LastId
		}
	}

	// If there are known plugins that were not observed in the current
	// set of plugins, they must be stopped and removed.
	if known != found {
	NextPlugin:
		for name, state := range m.plugins {
			for i := range infos {
				if infos[i].Name == name {
					continue NextPlugin
				}
			}
			logf("Plugin %q removed. Stopping it.", state.info.Name)
			err := state.plugin.Stop()
			if err != nil {
				logf("Plugin %q stopped with an error: %v", state.info.Name, err)
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
		err := m.database.C("incoming").Insert(&Message{Command: cmdPong, Account: rollbackAccount, Text: rollbackText})
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
	if i := strings.Index(pluginName, "/"); i >= 0 {
		return pluginName[:i]
	}
	return pluginName
}

func (m *pluginManager) startPlugin(info *pluginInfo) (*pluginState, error) {
	spec, ok := registeredPlugins[pluginKey(info.Name)]
	if !ok {
		logf("Plugin is not registered: %s", pluginKey(info.Name))
		return nil, fmt.Errorf("plugin %q not registered", pluginKey(info.Name))
	}
	plugger := newPlugger(info.Name, m.sendMessage, m.ldapConn)
	plugger.setConfig(info.Config)
	plugger.setTargets(info.Targets)
	plugin, err := spec.Start(plugger)
	if err != nil {
		logf("Cannot start plugin %q: %v", info.Name, err)
		return nil, fmt.Errorf("cannot start plugin %q: %v", info.Name, err)
	}
	state := &pluginState{
		info:    *info,
		spec:    spec,
		plugger: plugger,
		plugin:  plugin,
	}

	lastId := bson.NewObjectIdWithTime(time.Now().Add(-rollbackLimit))
	if !state.info.LastId.Valid() || state.info.LastId < lastId {
		state.info.LastId = lastId
	}
	return state, nil
}

func (m *pluginManager) sendMessage(msg *Message) error {
	if !m.tomb.Alive() {
		panic("plugin attempted to send message after its Stop method returned")
	}
	return m.outgoing.Insert(msg)
}

func (m *pluginManager) ldapConn(name string) (ldap.Conn, error) {
	if !m.tomb.Alive() {
		panic("plugin requested an LDAP connection after its Stop method returned")
	}
	var conn ldap.Conn
	m.ldapConnsMutex.Lock()
	if mconn, ok := m.ldapConns[name]; ok {
		conn = mconn.Conn()
	}
	m.ldapConnsMutex.Unlock()
	if conn != nil {
		return conn, nil
	}
	return nil, fmt.Errorf("LDAP connection %q not found", name)
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

func (state *pluginState) handle(msg *Message, cmdName string) error {
	err1 := state.handleCommand(msg, cmdName)
	err2 := state.handleMessage(msg)
	if err1 != nil {
		return err1
	}
	return err2
}

func (state *pluginState) handleCommand(msg *Message, cmdName string) error {
	if cmdName == "" {
		return nil
	}
	handler, ok := state.plugin.(CommandHandler)
	if !ok {
		return nil
	}
	cmdSchema := state.spec.Commands.Command(cmdName)
	if cmdSchema == nil {
		return nil
	}
	args, err := cmdSchema.Parse(msg.MupText)
	if err != nil {
		state.plugger.Sendf(msg, "Oops: %v", err)
		return err
	}
	cmd := &Command{
		Message: msg,
		schema:  cmdSchema,
		args:    marshalRaw(args),
	}
	err = handler.HandleCommand(cmd)
	if err != nil {
		logf("Plugin %q cannot handle message %q: %v", state.plugger.Name(), msg, err)
		return fmt.Errorf("plugin %q cannot handle message %q: %v", state.plugger.Name(), msg, err)
	}
	return nil
}

func (state *pluginState) handleMessage(msg *Message) error {
	if handler, ok := state.plugin.(MessageHandler); ok {
		err := handler.HandleMessage(msg)
		if err != nil {
			logf("Plugin %q cannot handle message %q: %v", state.plugger.Name(), msg, err)
			return fmt.Errorf("plugin %q cannot handle message %q: %v", state.plugger.Name(), msg, err)
		}
	}
	return nil
}
