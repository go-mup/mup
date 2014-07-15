package mup

import (
	"fmt"
	"labix.org/v2/mgo/bson"
	"sync"
	"time"
)

type Tester struct {
	mu      sync.Mutex
	cond    sync.Cond
	stopped bool
	plugin  Plugin
	plugger *Plugger
	replies []string
}

func NewTest(plugin string) *Tester {
	_, ok := registeredPlugins[pluginKey(plugin)]
	if !ok {
		panic(fmt.Sprintf("plugin not registered: %q", pluginKey(plugin)))
	}
	t := &Tester{}
	t.cond.L = &t.mu
	t.plugger = newPlugger(plugin, t.enqueueReply)
	return t
}

func (t *Tester) Plugger() *Plugger {
	return t.plugger
}

func (t *Tester) Start() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.plugin != nil {
		panic("Tester.Start called more than once")
	}
	t.plugin = registeredPlugins[pluginKey(t.plugger.Name())](t.plugger)
	return nil
}

func (t *Tester) SetConfig(value interface{}) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.plugin != nil {
		panic("Tester.SetConfig called after Start")
	}
	t.plugger.setConfig(marshalRaw(value))
}

func (t *Tester) SetTargets(value interface{}) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.plugin != nil {
		panic("Tester.SetTargets called after Start")
	}
	t.plugger.setTargets(marshalRaw(value))
}

func (t *Tester) enqueueReply(msg *Message) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped {
		panic("plugin attempted to send message after being stopped")
	}
	t.replies = append(t.replies, msg.String())
	t.cond.Signal()
	return nil
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

func (t *Tester) Stop() error {
	err := t.plugin.Stop()
	t.mu.Lock()
	t.stopped = true
	t.cond.Broadcast()
	t.mu.Unlock()
	return err
}

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

func (t *Tester) RecvAll() []string {
	t.mu.Lock()
	replies := t.replies
	t.replies = nil
	t.mu.Unlock()
	return replies
}

func (t *Tester) Sendf(target, format string, args ...interface{}) error {
	if target == "" {
		target = "mup"
	}
	msg := ParseIncoming("account", "mup", "!", fmt.Sprintf(":nick!~user@host PRIVMSG "+target+" :"+format, args...))
	return t.plugin.Handle(msg)
}

func (t *Tester) SendAll(target string, text []string) error {
	for _, texti := range text {
		err := t.Sendf(target, "%s", texti)
		if err != nil {
			return err
		}
	}
	return nil
}
