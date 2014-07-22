package mup

import (
	"fmt"

	"gopkg.in/mgo.v2/bson"
	"gopkg.in/mup.v0/ldap"
)

type Plugger struct {
	name    string
	send    func(msg *Message) error
	ldap    func(name string) (ldap.Conn, error)
	config  bson.Raw
	targets []PluginTarget
}

type PluginTarget struct {
	address Address
	config  bson.Raw
}

// Address returns the address for the plugin target.
//
// Note that PluginTarget addresses may have both Channel and Nick empty when
// the target is configured to listen to messages on the entire account.
func (t *PluginTarget) Address() Address {
	return t.address
}

// Config unmarshals into result the plugin target configuration for t.
func (t *PluginTarget) Config(result interface{}) {
	t.config.Unmarshal(result)
}

// CanSend returns whether the plugin target may have messages sent to it.
// Plugin targets that have both Nick and Channel unset act only as an
// incoming message selector.
func (t *PluginTarget) CanSend() bool {
	return t.address.Nick != "" || t.address.Channel != ""
}

// String returns a string representation of the plugin target suitable for log messages.
func (t *PluginTarget) String() string {
	if t.address.Nick != "" {
		if t.address.Channel == "" {
			return fmt.Sprintf("account %q, nick %q", t.address.Account, t.address.Nick)
		} else {
			return fmt.Sprintf("account %q, channel %q, nick %q", t.address.Account, t.address.Channel, t.address.Nick)
		}
	} else if t.address.Channel != "" {
		return fmt.Sprintf("account %q, channel %q", t.address.Account, t.address.Channel)
	}
	return fmt.Sprintf("account %q", t.address.Account)
}

var emptyDoc = bson.Raw{3, []byte("\x05\x00\x00\x00\x00")}

func newPlugger(name string, send func(msg *Message) error, ldap func(name string) (ldap.Conn, error)) *Plugger {
	return &Plugger{
		name: name,
		send: send,
		ldap: ldap,
	}
}

func (p *Plugger) setConfig(config bson.Raw) {
	if config.Kind == 0 {
		p.config = emptyDoc
	} else {
		p.config = config
	}
}

func (p *Plugger) setTargets(targets bson.Raw) {
	if targets.Kind == 0 {
		p.targets = nil
		return
	}
	var slice []struct {
		Account string
		Channel string
		Nick    string
		Config  bson.Raw
	}
	err := targets.Unmarshal(&slice)
	if err != nil {
		panic("cannot unmarshal plugin targets: " + err.Error())
	}
	p.targets = make([]PluginTarget, len(slice))
	for i, item := range slice {
		p.targets[i] = PluginTarget{Address{Account: item.Account, Channel: item.Channel, Nick: item.Nick}, item.Config}
	}
}

func (p *Plugger) Name() string {
	return p.name
}

func (p *Plugger) Logf(format string, args ...interface{}) {
	logf("["+p.name+"] "+format, args...)
}

func (p *Plugger) Debugf(format string, args ...interface{}) {
	debugf("["+p.name+"] "+format, args...)
}

func (p *Plugger) Config(result interface{}) {
	p.config.Unmarshal(result)
}

func (p *Plugger) Targets() []PluginTarget {
	return p.targets
}

func (p *Plugger) LDAP(name string) (ldap.Conn, error) {
	return p.ldap(name)
}

func (p *Plugger) Target(msg *Message) *PluginTarget {
	for i := range p.targets {
		t := &p.targets[i]
		if t.address.Account != msg.Account {
			continue
		}
		if t.address.Nick != "" && t.address.Nick != msg.Nick {
			continue
		}
		if t.address.Channel != "" && t.address.Channel != msg.Channel {
			continue
		}
		return t
	}
	return nil
}

// Sendf sends a message to the address obtained from the provided addressable.
// The message text is formed by providing format and args to fmt.Sprintf, and by
// prefixing the result with "nick: " if the message is addressed to a nick in
// a channel.
func (p *Plugger) Sendf(to Addressable, format string, args ...interface{}) error {
	text := fmt.Sprintf(format, args...)
	a := to.Address()
	if a.Channel != "" && a.Nick != "" {
		text = a.Nick + ": " + text
	}
	msg := &Message{Account: a.Account, Channel: a.Channel, Nick: a.Nick, Text: text}
	return p.Send(msg)
}

// Directf sends a direct message to the address obtained from the provided addressable.
// The message is sent privately if the address has a Nick, or to its Channel otherwise.
// The message text is formed by providing format and args to fmt.Sprintf.
func (p *Plugger) Directf(to Addressable, format string, args ...interface{}) error {
	a := to.Address()
	if a.Nick != "" {
		a.Channel = ""
	}
	msg := &Message{Account: a.Account, Channel: a.Channel, Nick: a.Nick, Text: fmt.Sprintf(format, args...)}
	return p.Send(msg)
}

// Channelf sends a channel message to the address obtained from the provided addressable,
// or privately to the Nick if the address Channel is unset.
// The message text is formed by providing format and args to fmt.Sprintf.
func (p *Plugger) Channelf(to Addressable, format string, args ...interface{}) error {
	a := to.Address()
	if a.Channel != "" {
		a.Nick = ""
	}
	msg := &Message{Account: a.Account, Channel: a.Channel, Nick: a.Nick, Text: fmt.Sprintf(format, args...)}
	return p.Send(msg)
}

// Broadcastf sends a message to all configured plugin targets.
// The message text is formed by providing format and args to fmt.Sprintf, and by
// prefixing the result with "nick: " if the message is addressed to a nick in
// a channel.
func (p *Plugger) Broadcastf(format string, args ...interface{}) error {
	msg := &Message{Text: fmt.Sprintf(format, args...)}
	return p.Broadcast(msg)
}

// Broadcast sends a message to all configured plugin targets.
// The message text is prefixed by "nick: " if the message is addressed to
// a nick in a channel.
func (p *Plugger) Broadcast(msg *Message) error {
	var first error
	for i := range p.targets {
		t := &p.targets[i]
		if !t.CanSend() {
			continue
		}
		copy := *msg
		copy.Account = t.address.Account
		copy.Channel = t.address.Channel
		copy.Nick = t.address.Nick
		if copy.Text != "" && copy.Channel != "" && copy.Nick != "" {
			copy.Text = copy.Nick + ": " + copy.Text
		}
		err := p.Send(&copy)
		if err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (p *Plugger) Send(msg *Message) error {
	err := p.send(msg)
	if err != nil {
		logf("Cannot put message in outgoing queue: %v", err)
		return fmt.Errorf("cannot put message in outgoing queue: %v", err)
	}
	return nil
}
