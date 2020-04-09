package mup

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"gopkg.in/mup.v0/ldap"
	"gopkg.in/mup.v0/schema"
	"strings"
)

// NewPluginTester interacts with an internally managed instance of a
// registered plugin for testing purposes.
type PluginTester struct {
	mu       sync.Mutex
	cond     sync.Cond
	stopped  bool
	state    pluginState
	replies  []string
	incoming []string
	ldaps    map[string]ldap.Conn
}

// NewPluginTester creates a new tester for interacting with an internally
// managed instance of the named plugin.
func NewPluginTester(pluginName string) *PluginTester {
	spec, ok := registeredPlugins[pluginKey(pluginName)]
	if !ok {
		panic(fmt.Sprintf("plugin %q not registered", pluginKey(pluginName)))
	}
	t := &PluginTester{}
	t.cond.L = &t.mu
	t.ldaps = make(map[string]ldap.Conn)
	t.state.spec = spec
	t.state.plugger = newPlugger(pluginName, t.sendMessage, t.handleMessage, t.ldap)
	return t
}

func (t *PluginTester) sendMessage(msg *Message) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped {
		panic("plugin attempted to send message after being stopped")
	}
	msgstr := msg.String()
	if msg.Account != "test" {
		msgstr = "[@" + msg.Account + "] " + msgstr
	}
	t.replies = append(t.replies, msgstr)
	t.cond.Signal()
	t.state.handle(msg, "")
	return nil
}

func (t *PluginTester) handleMessage(msg *Message) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped {
		panic("plugin attempted to enqueue incoming message after being stopped")
	}
	msgstr := msg.String()
	if msg.Account != "test" {
		msgstr = "[@" + msg.Account + "] " + msgstr
	}
	t.incoming = append(t.incoming, msgstr)
	t.cond.Signal()
	return nil
}

func (t *PluginTester) ldap(name string) (ldap.Conn, error) {
	t.mu.Lock()
	conn, ok := t.ldaps[name]
	t.mu.Unlock()
	if ok {
		return conn, nil
	}
	return nil, fmt.Errorf("LDAP connection %q not found", name)
}

// Plugger returns the plugger that is provided to the plugin.
func (t *PluginTester) Plugger() *Plugger {
	return t.state.plugger
}

// Start starts the plugin being tested.
func (t *PluginTester) Start() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state.plugin != nil {
		panic("PluginTester.Start called more than once")
	}
	var err error
	t.state.plugin = t.state.spec.Start(t.state.plugger)
	return err
}

// FIXME Rename to SetDB to conform to the database/sql terminology?

// SetDatabase sets the database to offer the plugin being tested.
func (t *PluginTester) SetDatabase(db *sql.DB) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state.plugin != nil {
		panic("PluginTester.SetDatabase called after Start")
	}
	t.state.plugger.setDatabase(db)

	t.mu.Unlock()
	defer t.mu.Lock()
	t.AddSchema(t.state.spec.Name)
}

// AddSchema adds the schema for the provided plugin to the database.
func (t *PluginTester) AddSchema(pluginName string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state.plugin != nil {
		panic("PluginTester.SetSchema called after Start")
	}
	spec, ok := registeredPlugins[pluginKey(pluginName)]
	if !ok {
		panic(fmt.Sprintf("PluginTester.SetSchema: plugin %q not registered", pluginKey(pluginName)))
	}
	db := t.state.plugger.DB()
	tx, err := db.Begin()
	if err == nil {
		err = setSchema(tx, spec.Name, spec.Help, spec.Commands)
	}
	if err != nil {
		tx.Rollback()
		panic("Cannot change schema: " + err.Error())
	}
	tx.Commit()
}

// Map is a generic map alias to improve code writing and reading.
type Map = map[string]interface{}

// SetConfig changes the configuration of the plugin being tested.
func (t *PluginTester) SetConfig(value map[string]interface{}) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state.plugin != nil {
		panic("PluginTester.SetConfig called after Start")
	}
	t.state.plugger.setConfig(marshalRaw(value))
}

// SetTargets changes the targets of the plugin being tested.
//
// These targets affect message broadcasts performed by the plugin,
// and also the list of targets that the plugin may observe by
// explicitly querying the provided Plugger about them. Changing
// targets does not prevent the tester's Sendf and SendAll functions
// from delivering messages to the plugin, though, as it doesn't
// make sense to feed the plugin with test messages that it cannot
// observe.
func (t *PluginTester) SetTargets(targets []Target) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state.plugin != nil {
		panic("PluginTester.SetTargets called after Start")
	}
	t.state.plugger.setTargets(targets)
}

// SetLDAP makes the provided LDAP connection available to the plugin.
func (t *PluginTester) SetLDAP(name string, conn ldap.Conn) {
	t.mu.Lock()
	t.ldaps[name] = conn
	t.mu.Unlock()
}

func marshalRaw(value interface{}) json.RawMessage {
	if value == nil {
		return emptyDoc
	}
	data, err := json.Marshal(value)
	if err != nil {
		panic("cannot marshal provided value: " + err.Error())
	}
	return json.RawMessage(data)
}

// Stop stops the tester and the plugin being tested.
func (t *PluginTester) Stop() error {
	t.mu.Lock()
	stopped := t.stopped
	t.mu.Unlock()
	if stopped {
		return nil
	}
	err := t.state.plugin.Stop()
	t.mu.Lock()
	t.stopped = true
	t.cond.Broadcast()
	t.mu.Unlock()
	return err
}

// Recv receives the next message dispatched by the plugin being tested. If no
// message is currently pending, Recv waits up to a few seconds for a message
// to arrive. If no messages arrive even then, an empty string is returned.
//
// The message is formatted as a raw IRC protocol message, and optionally prefixed
// by the account name under brackets ("[@<account>] ") if the message is delivered
// to any other account besides the default "test" one.
//
// Recv may be used after the tester is stopped.
func (t *PluginTester) Recv() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	timeout := time.Now().Add(3 * time.Second)
	for !t.stopped && len(t.replies) == 0 && time.Now().Before(timeout) {
		t.cond.Wait()
	}
	if len(t.replies) == 0 {
		return ""
	}
	reply := t.replies[0]
	copy(t.replies, t.replies[1:])
	t.replies = t.replies[0 : len(t.replies)-1]
	return reply
}

// RecvAll receives all currently pending messages dispatched by the plugin being tested.
//
// All messages are formatted as raw IRC protocol messages, and optionally prefixed
// by the account name under brackets ("[@<account>] ") if the message is delivered
// to any other account besides the default "test" one.
//
// RecvAll may be used after the tester is stopped.
func (t *PluginTester) RecvAll() []string {
	t.mu.Lock()
	replies := t.replies
	t.replies = nil
	t.mu.Unlock()
	return replies
}

// RecvIncoming receives the next message enqueued as incoming by the plugin being tested.
// If no message is currently pending, RecvIncoming waits up to a few seconds for a
// message to arrive. If no messages arrive even then, an empty string is returned.
//
// The message is formatted as a raw IRC protocol message, and optionally prefixed
// by the account name under brackets ("[@<account>] ") if the message is delivered
// to any other account besides the default "test" one.
//
// RecvIncoming may be used after the tester is stopped.
func (t *PluginTester) RecvIncoming() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	timeout := time.Now().Add(3 * time.Second)
	for !t.stopped && len(t.incoming) == 0 && time.Now().Before(timeout) {
		t.cond.Wait()
	}
	if len(t.incoming) == 0 {
		return ""
	}
	in := t.incoming[0]
	copy(t.incoming, t.incoming[1:])
	t.incoming = t.incoming[0 : len(t.incoming)-1]
	return in
}

// RecvAllIncoming receives all currently pending messages enqueued as incoming by
// the plugin being tested.
//
// All messages are formatted as raw IRC protocol messages, and optionally prefixed
// by the account name under brackets ("[@<account>] ") if the message is delivered
// to any other account besides the default "test" one.
//
// RecvAllIncoming may be used after the tester is stopped.
func (t *PluginTester) RecvAllIncoming() []string {
	t.mu.Lock()
	incoming := t.incoming
	t.incoming = nil
	t.mu.Unlock()
	return incoming
}

// Sendf formats a PRIVMSG coming from "nick!~user@host" and delivers to the plugin
// being tested for handling as a message, as a command, or both, depending on the
// plugin specification and implementation.
//
// The formatted message may be prefixed by "[<target>@<account>,<option>] " to define
// the channel or bot nick the message was addressed to, the account name it was
// observed on, and a list of comma-separated options. All fields are optional, and
// default to "[mup@test] ". The only supported option at the moment is "raw", which
// causes the message text to be taken as a raw IRC protocol message. When providing
// a target without an account the "@" may be omitted, and the comma may be omitted
// if there are no options.
//
// Sendf always delivers the message to the plugin, irrespective of which targets
// are currently setup, as it doesn't make sense to test the plugin with a message
// that it cannot observe.
func (t *PluginTester) Sendf(format string, args ...interface{}) {
	account, message := parseSendfText(fmt.Sprintf(format, args...))
	msg := ParseIncoming(account, "mup", "!", message)
	t.state.handle(msg, schema.CommandName(msg.BotText))
}

func parseSendfText(text string) (account, message string) {
	account = "test"

	close := strings.Index(text, "] ")
	if !strings.HasPrefix(text, "[") || close < 0 {
		return account, ":nick!~user@host PRIVMSG mup :" + text
	}

	prefix := text[1:close]
	text = text[close+2:]

	raw := false
	comma := strings.Index(prefix, ",")
	if comma >= 0 {
		for _, option := range strings.Split(prefix[comma+1:], ",") {
			if option == "raw" {
				raw = true
			} else if option != "" {
				panic("unknown option for Tester.Sendf: " + option)
			}
		}
		prefix = prefix[:comma]
	}

	at := strings.Index(prefix, "@")
	if at >= 0 {
		if at < len(prefix)-1 {
			account = prefix[at+1:]
		}
		prefix = prefix[:at]
	}

	if raw {
		if prefix != "" {
			panic("Sendf prefix cannot contain both a target and the raw option")
		}
		return account, text
	}

	target := "mup"
	if prefix != "" {
		target = prefix
	}
	return account, ":nick!~user@host PRIVMSG " + target + " :" + text
}

// SendAll sends each entry in text as an individual message to the bot.
//
// See Sendf for more details.
func (t *PluginTester) SendAll(text []string) {
	for _, texti := range text {
		t.Sendf("%s", texti)
	}
}
