package mup

import (
	"fmt"
	"sync"
	"time"

	"gopkg.in/mgo.v2/bson"
	"gopkg.in/mup.v0/ldap"
	"gopkg.in/mup.v0/schema"
)

// NewPluginTester interacts with an internally managed instance of a
// registered plugin for testing purposes.
type PluginTester struct {
	mu      sync.Mutex
	cond    sync.Cond
	stopped bool
	state   pluginState
	replies []string
	ldaps   map[string]ldap.Conn
}

// NewPluginTester creates a new tester for interacting with an internally
// managed instance of the named plugin.
func NewPluginTester(pluginName string) *PluginTester {
	spec, ok := registeredPlugins[pluginKey(pluginName)]
	if !ok {
		panic(fmt.Sprintf("plugin not registered: %q", pluginKey(pluginName)))
	}
	t := &PluginTester{}
	t.cond.L = &t.mu
	t.ldaps = make(map[string]ldap.Conn)
	t.state.spec = spec
	t.state.plugger = newPlugger(pluginName, t.appendMessage, t.ldap)
	return t
}

func (t *PluginTester) appendMessage(msg *Message) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped {
		panic("plugin attempted to send message after being stopped")
	}
	msgstr := msg.String()
	if msg.Account != "test" {
		msgstr = "[" + msg.Account + "] " + msgstr
	}
	t.replies = append(t.replies, msgstr)
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

// SetConfig changes the configuration of the plugin being tested.
func (t *PluginTester) SetConfig(value interface{}) {
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
func (t *PluginTester) SetTargets(value interface{}) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state.plugin != nil {
		panic("PluginTester.SetTargets called after Start")
	}
	t.state.plugger.setTargets(marshalRaw(value))
}

// SetLDAP makes the provided LDAP connection available to the plugin.
func (t *PluginTester) SetLDAP(name string, conn ldap.Conn) {
	t.mu.Lock()
	t.ldaps[name] = conn
	t.mu.Unlock()
}

func marshalRaw(value interface{}) bson.Raw {
	if value == nil {
		return emptyDoc
	}
	data, err := bson.Marshal(bson.D{{"value", value}})
	if err != nil {
		panic("cannot marshal provided value: " + err.Error())
	}
	var raw struct{ Value bson.Raw }
	err = bson.Unmarshal(data, &raw)
	if err != nil {
		panic("cannot unmarshal provided value: " + err.Error())
	}
	return raw.Value
}

// Stop stops the tester and the plugin being tested.
func (t *PluginTester) Stop() error {
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
// by the account name under brackets ("[account] ") if the message is delivered
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
// by the account name under brackets ("[account] ") if the message is delivered
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

// Sendf formats a PRIVMSG coming from "nick!~user@host" and delivers to the plugin
// being tested for handling as a message, as a command, or both, depending on the
// plugin specification and implementation.
//
// The message is setup to come from account "test", and the target parameter
// defines the channel or bot nick the message was addressed to. If empty, target
// defaults to "mup", which means the message is received by the plugin as if it
// had been privately delivered to the bot under that nick.
//
// Sendf always delivers the message to the plugin, irrespective of which targets
// are currently setup, as it doesn't make sense to test the plugin with a message
// that it cannot observe.
func (t *PluginTester) Sendf(target, format string, args ...interface{}) {
	if target == "" {
		target = "mup"
	}
	msg := ParseIncoming("test", "mup", "!", fmt.Sprintf(":nick!~user@host PRIVMSG "+target+" :"+format, args...))
	t.state.handle(msg, schema.CommandName(msg.MupText))
}

// SendAll sends each entry in text as an individual message to the bot.
//
// See Sendf for more details.
func (t *PluginTester) SendAll(target string, text []string) {
	for _, texti := range text {
		t.Sendf(target, "%s", texti)
	}
}
