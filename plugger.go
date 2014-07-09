package mup

import (
	"fmt"
	"labix.org/v2/mgo/bson"
)

type Plugger struct {
	name     string
	send     func(*Message) error
	settings bson.Raw
	targets  []PluginTarget
}

type PluginTarget struct {
	Account string
	Target  string

	settings bson.Raw
}

func (t *PluginTarget) Settings(result interface{}) {
	t.settings.Unmarshal(result)
}

var emptyDoc = bson.Raw{3, []byte("\x05\x00\x00\x00\x00")}

func newPlugger(name string, send func(msg *Message) error) *Plugger {
	return &Plugger{
		name:     name,
		send:     send,
	}
}

func (p *Plugger) setSettings(settings bson.Raw) {
	if settings.Kind == 0 {
		p.settings = emptyDoc
	} else {
		p.settings = settings
	}
}

func (p *Plugger) setTargets(targets bson.Raw) {
	if targets.Kind == 0 {
		p.targets = nil
		return
	}
	var slice []struct {
		Account  string
		Target   string
		Settings bson.Raw
	}
	err := targets.Unmarshal(&slice)
	if err != nil {
		panic("cannot unmarshal plugin targets: " + err.Error())
	}
	p.targets = make([]PluginTarget, len(slice))
	for i, item := range slice {
		p.targets[i] = PluginTarget{item.Account, item.Target, item.Settings}
	}
}

func (p *Plugger) Name() string {
	return p.name
}

func (p *Plugger) Settings(result interface{}) {
	p.settings.Unmarshal(result)
}

func (p *Plugger) Targets() []PluginTarget {
	return p.targets
}

func (p *Plugger) Target(msg *Message) *PluginTarget {
	for i := range p.targets {
		t := &p.targets[i]
		if t.Account == msg.Account && (t.Target == msg.Target || t.Target == "") {
			return t
		}
	}
	return nil
}

func (p *Plugger) Replyf(msg *Message, format string, args ...interface{}) error {
	text := fmt.Sprintf(format, args...)
	target := msg.Target
	if msg.Target == msg.MupNick {
		target = msg.Nick
	} else {
		text = msg.Nick + ": " + text
	}
	reply := &Message{Account: msg.Account, Target: target, Text: text}
	return p.Send(reply)
}

func (p *Plugger) Sendf(account, target, format string, args ...interface{}) error {
	msg := &Message{Account: account, Target: target, Text: fmt.Sprintf(format, args...)}
	return p.Send(msg)
}

func (p *Plugger) Send(msg *Message) error {
	err := p.send(msg)
	if err != nil {
		Logf("Cannot put message in outgoing queue: %v", err)
		return fmt.Errorf("cannot put message in outgoing queue: %v", err)
	}
	return nil
}

func (p *Plugger) Broadcastf(format string, args ...interface{}) error {
	msg := &Message{Text: fmt.Sprintf(format, args...)}
	return p.Broadcast(msg)
}

func (p *Plugger) Broadcast(msg *Message) error {
	var first error
	for i := range p.targets {
		t := &p.targets[i]
		if t.Target == "" {
			continue
		}
		copy := *msg
		copy.Account = t.Account
		copy.Target = t.Target
		err := p.Send(&copy)
		if err != nil && first == nil {
			first = err
		}
	}
	return first
}
