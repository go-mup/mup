package mup

import (
	"fmt"
	"sync"
	"time"

	"gopkg.in/mgo.v2/bson"
	"gopkg.in/mup.v0/schema"
)

type Tester struct {
	mu      sync.Mutex
	cond    sync.Cond
	stopped bool
	state   pluginState
	replies []string
}

// NewTest creates a new tester for interacting with the named plugin.
func NewTest(pluginName string) *Tester {
	spec, ok := registeredPlugins[pluginKey(pluginName)]
	if !ok {
		panic(fmt.Sprintf("plugin not registered: %q", pluginKey(pluginName)))
	}
	t := &Tester{}
	t.cond.L = &t.mu
	t.state.spec = spec
	t.state.plugger = newPlugger(pluginName, t.appendMessage)
	return t
}

func (t *Tester) appendMessage(msg *Message) error {
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

// Plugger returns the plugger that is provided to the plugin.
func (t *Tester) Plugger() *Plugger {
	return t.state.plugger
}

// Start starts the plugin being tested.
func (t *Tester) Start() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state.plugin != nil {
		panic("Tester.Start called more than once")
	}
	var err error
	t.state.plugin, err = t.state.spec.Start(t.state.plugger)
	return err
}

// SetConfig changes the configuration of the plugin being tested.
func (t *Tester) SetConfig(value interface{}) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state.plugin != nil {
		panic("Tester.SetConfig called after Start")
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
func (t *Tester) SetTargets(value interface{}) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state.plugin != nil {
		panic("Tester.SetTargets called after Start")
	}
	t.state.plugger.setTargets(marshalRaw(value))
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
func (t *Tester) Stop() error {
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
func (t *Tester) Recv() string {
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
func (t *Tester) RecvAll() []string {
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
func (t *Tester) Sendf(target, format string, args ...interface{}) error {
	if target == "" {
		target = "mup"
	}
	msg := ParseIncoming("test", "mup", "!", fmt.Sprintf(":nick!~user@host PRIVMSG "+target+" :"+format, args...))
	return t.state.handle(msg, schema.CommandName(msg.MupText))
}

// SendAll sends each entry in text as an individual message to the bot.
//
// See Sendf for more details.
func (t *Tester) SendAll(target string, text []string) error {
	for _, texti := range text {
		err := t.Sendf(target, "%s", texti)
		if err != nil {
			return err
		}
	}
	return nil
}
