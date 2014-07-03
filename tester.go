package mup

import (
	"fmt"
	"labix.org/v2/mgo/bson"
	"sync"
	"time"
)

type PluginTester struct {
	mu       sync.Mutex
	cond     sync.Cond
	stopped  bool
	plugin   Plugin
	plugger  *Plugger
	replies  []string
	settings interface{}
}

func StartPluginTest(name string, settings interface{}) *PluginTester {
	pluginFunc, ok := registeredPlugins[name]
	if !ok {
		panic(fmt.Sprintf("plugin not registered: %q", name))
	}
	tester := &PluginTester{}
	tester.cond.L = &tester.mu
	tester.settings = settings
	tester.plugger = newPlugger(tester.enqueueReply, tester.loadSettings)
	tester.plugin = pluginFunc(tester.plugger)
	return tester
}

func (t *PluginTester) enqueueReply(msg *Message) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped {
		panic("plugin attempted to send message after being stopped")
	}
	t.replies = append(t.replies, msg.String())
	t.cond.Signal()
	return nil
}

func (t *PluginTester) loadSettings(value interface{}) {
	if t.settings == nil {
		return
	}
	data, err := bson.Marshal(t.settings)
	if err != nil {
		panic("cannot marshal provided settings: " + err.Error())
	}
	err = bson.Unmarshal(data, value)
	if err != nil {
		panic("cannot unmarshal provided settings: " + err.Error())
	}
}

func (t *PluginTester) Stop() error {
	err := t.plugin.Stop()
	t.mu.Lock()
	t.stopped = true
	t.cond.Broadcast()
	t.mu.Unlock()
	return err
}

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

func (t *PluginTester) RecvAll() []string {
	t.mu.Lock()
	replies := t.replies
	t.replies = nil
	t.mu.Unlock()
	return replies
}

func (t *PluginTester) Sendf(target, format string, args ...interface{}) error {
	if target == "" {
		target = "mup"
	}
	msg := ParseMessage("mup", "!", fmt.Sprintf(":nick!~user@host PRIVMSG "+target+" :"+format, args...))
	return t.plugin.Handle(msg)
}

func (t *PluginTester) SendAll(target string, text []string) error {
	for _, texti := range text {
		err := t.Sendf(target, "%s", texti)
		if err != nil {
			return err
		}
	}
	return nil
}
