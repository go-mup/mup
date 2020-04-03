package mup

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"gopkg.in/mup.v0/ldap"
	"gopkg.in/mup.v0/schema"
	"gopkg.in/tomb.v2"
)

// PluginSpec holds the specification of a plugin that may be registered with mup.
type PluginSpec struct {
	Name     string
	Help     string
	Start    func(p *Plugger) Stopper
	Commands schema.Commands
}

// Stopper is implemented by types that can run arbitrary background
// activities that can be stopped on request.
type Stopper interface {
	Stop() error
}

// MessageHandler is implemented by plugins that can handle raw messages.
//
// See CommandHandler.
type MessageHandler interface {
	HandleMessage(msg *Message)
}

// OutgoingHandler is implemented by plugins that want to observe
// outgoing messages being sent out by the bot.
type OutgoingHandler interface {
	HandleOutgoing(msg *Message)
}

// CommandHandler is implemented by plugins that can handle commands.
type CommandHandler interface {
	HandleCommand(cmd *Command)
}

// Command holds a message that was properly parsed as an existing command.
type Command struct {
	*Message

	name   string
	schema *schema.Command
	args   json.RawMessage
}

// Name returns the command name.
func (c *Command) Name() string {
	return c.name
}

// Schema returns the command schema.
func (c *Command) Schema() *schema.Command {
	return c.schema
}

// Args unmarshals into result the command arguments parsed.
// The unmarshaling is performed by the json package.
func (c *Command) Args(result interface{}) error {
	err := json.Unmarshal(c.args, result)
	if err != nil {
		return fmt.Errorf("cannot parse command %q arguments: %v", c.name, err)
	}
	return nil
}

var registeredPlugins = make(map[string]*PluginSpec)

// RegisterPlugin registers with mup the plugin defined via the provided
// specification, so that it may be loaded when configured to be.
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
	Name   string
	LastId int64
	Config []byte
	State  []byte

	Targets []Target
}

const pluginColumns = "name,last_id,config,state"
const pluginPlacers = "?,?,?,?"

func (pi *pluginInfo) refs() []interface{} {
	return []interface{}{&pi.Name, &pi.LastId, &pi.Config, &pi.State}
}

type pluginState struct {
	info    pluginInfo
	spec    *PluginSpec
	plugger *Plugger
	plugin  Stopper
}

type ldapInfo struct {
	Name   string
	Config ldap.Config
}

const ldapColumns = "name,url,base_dn,bind_dn,bind_pass"
const ldapPlacers = "?,?,?,?,?"

func (li *ldapInfo) refs() []interface{} {
	return []interface{}{&li.Name, &li.Config.URL, &li.Config.BaseDN, &li.Config.BindDN, &li.Config.BindPass}
}

type ldapState struct {
	info ldapInfo
	conn *ldap.ManagedConn
}

type pluginManager struct {
	tomb     tomb.Tomb
	config   Config
	db       *sql.DB
	requests chan interface{}
	incoming chan *Message
	rollback chan int64
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
		rollback: make(chan int64),
	}
	if config.DB == nil {
		panic("config.DB is NIL")
	}
	m.db = config.DB
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

func setSchema(tx *sql.Tx, plugin, help string, cmds schema.Commands) error {
	_, err := tx.Exec("DELETE FROM plugin_schema WHERE plugin=?", plugin)
	if err != nil {
		return fmt.Errorf("cannot delete old schema for %q plugin: %v", plugin, err)
	}
	_, err = tx.Exec("INSERT INTO plugin_schema (plugin,help) VALUES (?,?)", plugin, help)
	if err != nil {
		return fmt.Errorf("cannot add schema for %q plugin: %v", plugin, err)
	}

	for _, cmd := range cmds {
		_, err := tx.Exec("INSERT INTO command_schema (plugin,command,help,hide) VALUES (?,?,?,?)",
			plugin, cmd.Name, cmd.Help, cmd.Hide)
		if err != nil {
			return fmt.Errorf("cannot add schema for %q plugin, %q command: %v", plugin, cmd.Name, err)
		}
		for _, arg := range cmd.Args {
			_, err := tx.Exec("INSERT INTO argument_schema (plugin,command,argument,hint,type,flag) VALUES (?,?,?,?,?,?)",
				plugin, cmd.Name, arg.Name, arg.Hint, arg.Type, arg.Flag)
			if err != nil {
				return fmt.Errorf("cannot add schema for %q plugin, %q command, %q argument: %v", plugin, cmd.Name, arg.Name, err)
			}
		}
	}
	return nil
}

func (m *pluginManager) updateSchema() {
	tx, err := m.db.Begin()
	if err != nil {
		logf("Cannot begin database transaction: %v", err)
		return
	}
	defer tx.Rollback()

	for name, spec := range registeredPlugins {
		if !m.pluginOn(name) {
			continue
		}
		err = setSchema(tx, name, spec.Help, spec.Commands)
		if err != nil {
			break
		}
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		logf("Cannot update schema for plugins: %v", err)
	}
}

func (m *pluginManager) loop() error {
	defer m.die()

	if m.config.Plugins != nil && len(m.config.Plugins) == 0 {
		<-m.tomb.Dying()
		return nil
	}

	m.updateSchema()

	m.tomb.Go(m.tail)

	m.handleRefresh()
	var refresh <-chan time.Time
	if m.config.Refresh > 0 {
		ticker := time.NewTicker(m.config.Refresh)
		defer ticker.Stop()
		refresh = ticker.C
	}
	for {
		select {
		case msg := <-m.incoming:
			if msg.Command == cmdPong {
				continue
			}
			cmdName := schema.CommandName(msg.BotText)
			for name, state := range m.plugins {
				if state.info.LastId >= msg.Id || state.plugger.Target(msg).Account == "" {
					continue
				}
				state.info.LastId = msg.Id
				state.handle(msg, cmdName)
				_, err := m.db.Exec("UPDATE plugin SET last_id=? WHERE name=?", msg.Id, name)
				if err != nil {
					logf("Cannot update plugin with last sent message id: %v", err)
					// TODO How to recover properly from this?
					//m.tomb.Kill(err)
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

func ldapChanged(a, b *ldapInfo) bool {
	return a.Name != b.Name || a.Config != b.Config
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
	tx, err := m.db.Begin()
	if err != nil {
		logf("Cannot begin database transaction: %v", err)
		return
	}
	defer tx.Rollback()

	var infos []ldapInfo

	rows, err := tx.Query("SELECT " + ldapColumns + " FROM ldap")
	if err != nil {
		logf("Cannot fetch LDAP information from database: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var info ldapInfo
		err = rows.Scan(info.refs()...)
		if err != nil {
			logf("Cannot parse database LDAP information: %v", err)
			return
		}
		infos = append(infos, info)
	}
	if rows.Err() != nil {
		logf("Cannot fetch LDAP connection information from database: %v", rows.Err())
		return
	}

	var found int
	var known = len(m.ldaps)
	for _, info := range infos {
		if state, ok := m.ldaps[info.Name]; ok {
			found++
			if !ldapChanged(&state.info, &info) {
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
			info: info,
			conn: ldap.DialManaged(&info.Config),
		}
		changed = true
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
	if !bytes.Equal(a.Config, b.Config) {
		return true
	}
	if len(a.Targets) != len(b.Targets) {
		return true
	}
	for i := range a.Targets {
		if a.Targets[i] != b.Targets[i] {
			return true
		}
	}
	return false
}

func (m *pluginManager) pluginOn(name string) bool {
	if m.config.Plugins == nil {
		return true
	}
	for _, cname := range m.config.Plugins {
		if name == cname || len(name) > len(cname) && name[len(cname)] == '/' && name[:len(cname)] == cname {
			return true
		}
	}
	return false
}

func (m *pluginManager) refreshPlugins() {
	rollbackId, err := rollbackMsgId(m.db)
	if err != nil {
		logf("%v", err)
		return
	}
	latestId, err := latestMsgId(m.db)
	if err != nil {
		logf("%v", err)
		return
	}

	tx, err := m.db.Begin()
	if err != nil {
		logf("Cannot begin database transaction: %v", err)
		return
	}
	defer tx.Rollback()

	var infos []pluginInfo
	var targets = make(map[string][]Target)

	rows, err := tx.Query("SELECT " + pluginColumns + " FROM plugin")
	if err != nil {
		logf("Cannot fetch plugin information from database: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var info pluginInfo
		err = rows.Scan(info.refs()...)
		if err != nil {
			logf("Cannot parse database plugin information: %v", err)
			return
		}
		infos = append(infos, info)
	}
	if rows.Err() != nil {
		logf("Cannot fetch plugin information from database: %v", rows.Err())
		return
	}

	rows, err = tx.Query("SELECT " + targetColumns + " FROM target")
	if err != nil {
		logf("Cannot fetch target information from database: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var t Target
		err = rows.Scan(t.refs()...)
		if err != nil {
			logf("Cannot parse database target information: %v", err)
			return
		}
		targets[t.Plugin] = append(targets[t.Plugin], t)
	}
	if rows.Err() != nil {
		logf("Cannot fetch target information from database: %v", rows.Err())
		return
	}

	for i := range infos {
		info := &infos[i]
		info.Targets = targets[info.Name]
	}

	// Start new plugins, and stop/restart updated ones.
	var known = len(m.plugins)
	var seen = make(map[string]bool)
	var found int
	var lowestId = latestId
	var changed = false
	for i := range infos {
		info := &infos[i]
		if !m.pluginOn(info.Name) {
			continue
		}
		seen[info.Name] = true
		if state, ok := m.plugins[info.Name]; ok {
			found++
			if !pluginChanged(&state.info, info) {
				continue
			}
			changed = true
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

		// If the plugin has never seen any messages, start from the tip. Otherwise
		// only allow the plugin to go as far back as the rollbackLimit.
		if state.info.LastId == 0 {
			state.info.LastId = latestId
		} else if state.info.LastId < rollbackId {
			state.info.LastId = rollbackId
		}
		if state.info.LastId < lowestId {
			lowestId = state.info.LastId
		}

		m.plugins[info.Name] = state
	}

	// If there are known plugins that were not observed in the current
	// set of plugins, they must be stopped and removed.
	if known != found {
		changed = true
		for name, state := range m.plugins {
			if seen[name] {
				continue
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
	if changed {
		// If the tail iterator ever blocks waiting for the database to
		// respond with new messages, this logic needs to wake it up by
		// injecting a message into the incoming lane that looks something
		// like the following. The iterator won't be able to deliver
		// this message because incoming is consumed by this goroutine
		// after this method returns.
		//
		// &Message{Command: cmdPong, Account: rollbackAccount, Text: rollbackText})
		//
		// For now we don't need this.

		// Send oldest observed id to the tail loop for a potential rollback.
		select {
		case m.rollback <- lowestId:
		case <-m.tomb.Dying():
			return
		}
	}
}

// rollbackLimit defines how long messages can be waiting in the
// incoming queue while still being submitted to plugins.
const (
	rollbackLimit   = 10 * time.Second
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
	plugger := newPlugger(info.Name, m.sendMessage, m.handleMessage, m.ldapConn)
	plugger.setDatabase(m.db)
	plugger.setConfig(info.Config)
	plugger.setTargets(info.Targets)
	plugin := spec.Start(plugger)
	state := &pluginState{
		info:    *info,
		spec:    spec,
		plugger: plugger,
		plugin:  plugin,
	}
	return state, nil
}

func (m *pluginManager) sendMessage(msg *Message) error {
	if !m.tomb.Alive() {
		panic("plugin attempted to send message after its Stop method returned")
	}
	_, err := m.db.Exec("INSERT INTO message ("+messageColumns+") VALUES ("+messagePlacers+")", msg.refs(Outgoing)...)
	return err
}

func (m *pluginManager) handleMessage(msg *Message) error {
	if !m.tomb.Alive() {
		panic("plugin attempted to enqueue incoming message after its Stop method returned")
	}
	_, err := m.db.Exec("INSERT INTO message ("+messageColumns+") VALUES ("+messagePlacers+")", msg.refs(Incoming)...)
	return err
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

func latestMsgId(db *sql.DB) (int64, error) {
	var id int64
	row := db.QueryRow(`SELECT seq FROM sqlite_sequence WHERE name='message' LIMIT 1`)
	err := row.Scan(&id)
	if err != nil && err != sql.ErrNoRows {
		return 0, fmt.Errorf("cannot fetch latest message ID from database: %v", err)
	}
	if id == 0 {
		id = -1 // Means no messages at all.
	}
	return id, nil
}

func rollbackMsgId(db *sql.DB) (int64, error) {
	var id int64
	row := db.QueryRow(`SELECT id FROM message WHERE time>=? ORDER BY id LIMIT 1`, time.Now().Add(-rollbackLimit))
	err := row.Scan(&id)
	if err != nil && err != sql.ErrNoRows {
		return 0, fmt.Errorf("cannot fetch rollback message ID from database: %v", err)
	}
	return id, nil
}

func (m *pluginManager) tail() error {
	lastId, err := rollbackMsgId(m.db)
	if err != nil {
		return err
	}

NextTail:
	for m.tomb.Alive() {

		// Must be able to rollback even if iteration is failing so
		// that the main loop doesn't get blocked on the channel.
		select {
		case rollbackId := <-m.rollback:
			if rollbackId < lastId {
				logf("Rolling back tail iterator to consider older incoming messages.")
				lastId = rollbackId
			}
		default:
		}

		rows, err := m.db.Query("SELECT "+messageColumns+" FROM message WHERE id>? AND lane=1 ORDER BY id", lastId)
		if err != nil {
			logf("Error selecting incoming messages: %v", err)
		} else {
			for rows.Next() {
				var msg Message
				err := rows.Scan(msg.refs(0)...)
				if err != nil {
					logf("Error parsing incoming messages: %v", err)
				}
				debugf("[%s] Iterator got incoming message: %s", msg.Account, msg.String())
			DeliverMsg:
				select {
				case m.incoming <- &msg:
					lastId = msg.Id
				case rollbackId := <-m.rollback:
					if rollbackId < lastId {
						logf("Rolling back tail iterator to consider older incoming messages.")
						lastId = rollbackId
						rows.Close()
						continue NextTail
					}
					goto DeliverMsg
				case <-m.tomb.Dying():
					rows.Close()
					return nil
				}
			}
			err = rows.Close()
			if err != nil && m.tomb.Alive() {
				logf("Error iterating over incoming collection: %v", err)
			}
		}
		select {
		case <-time.After(100 * time.Millisecond):
		case <-m.tomb.Dying():
			return nil
		}
	}
	return nil
}

func (state *pluginState) handle(msg *Message, cmdName string) {
	if msg.AsNick == "" {
		state.handleOutgoing(msg)
	} else {
		state.handleCommand(msg, cmdName)
		state.handleMessage(msg)
	}
}

func (state *pluginState) handleMessage(msg *Message) {
	if handler, ok := state.plugin.(MessageHandler); ok {
		handler.HandleMessage(msg)
	}
}

func (state *pluginState) handleOutgoing(msg *Message) {
	if handler, ok := state.plugin.(OutgoingHandler); ok {
		handler.HandleOutgoing(msg)
	}
}

func (state *pluginState) handleCommand(msg *Message, cmdName string) {
	if cmdName == "" {
		return
	}
	handler, ok := state.plugin.(CommandHandler)
	if !ok {
		return
	}
	cmdSchema := state.spec.Commands.Command(cmdName)
	if cmdSchema == nil {
		return
	}
	args, err := cmdSchema.Parse(msg.BotText)
	if err != nil {
		state.plugger.Sendf(msg, "Oops: %v", err)
		return
	}
	cmd := &Command{
		Message: msg,
		name:    cmdName,
		schema:  cmdSchema,
		args:    marshalRaw(args),
	}
	handler.HandleCommand(cmd)
}

// DurationString represents a time.Duration that marshals and unmarshals
// using the standard string representation for that type.
type DurationString struct {
	time.Duration
}

func (d DurationString) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

func (d *DurationString) UnmarshalJSON(raw []byte) error {
	var s string
	err := json.Unmarshal(raw, &s)
	if err != nil || s == "" {
		d.Duration = 0
		return err
	}
	d.Duration, err = time.ParseDuration(s)
	return err
}
