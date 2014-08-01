package mup

import (
	"fmt"

	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
	"gopkg.in/mup.v0/ldap"
	"strings"
	"time"
)

// Plugger provides the interface between a plugin and the bot infrastructure.
type Plugger struct {
	name    string
	send    func(msg *Message) error
	ldap    func(name string) (ldap.Conn, error)
	config  bson.Raw
	targets []PluginTarget
	db      *mgo.Database
}

// PluginTarget defines an Account, Channel, and/or Nick that the
// plugin will observe messages from, and may choose to broadcast
// messages to. Empty fields are ignored when deciding whether a
// message matches the plugin target.
//
// A PluginTarget may also define per-target configuration options.
type PluginTarget struct {
	address Address
	config  bson.Raw
}

// Address returns the address for the plugin target.
func (t *PluginTarget) Address() Address {
	return t.address
}

// Config unmarshals into result the plugin target configuration using the bson package.
func (t *PluginTarget) Config(result interface{}) {
	t.config.Unmarshal(result)
}

// CanSend returns whether the plugin target may have messages sent to it.
// For that, it must have an Account set, and at least one of Channel and Nick.
func (t *PluginTarget) CanSend() bool {
	return t.address.Account != "" && (t.address.Nick != "" || t.address.Channel != "")
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

func (p *Plugger) setDatabase(db *mgo.Database) {
	p.db = db
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

// Name returns the plugin name including the label, if any ("name/label").
func (p *Plugger) Name() string {
	return p.name
}

// Logf logs a message assembled by providing format and args to fmt.Sprintf.
func (p *Plugger) Logf(format string, args ...interface{}) {
	logf("["+p.name+"] "+format, args...)
}

// Debugf logs a debug message assembled by providing format and args to fmt.Sprintf.
func (p *Plugger) Debugf(format string, args ...interface{}) {
	debugf("["+p.name+"] "+format, args...)
}

// Config unmarshals into result the plugin configuration using the bson package.
func (p *Plugger) Config(result interface{}) {
	p.config.Unmarshal(result)
}

// SharedCollection returns a database collection that may be shared
// across multiple instances of the same plugin, or across multiple plugins.
// The collection is named "shared.<suffix>".
//
// See Plugger.UniqueCollection.
func (p *Plugger) SharedCollection(suffix string) (*mgo.Session, *mgo.Collection) {
	if p.db == nil {
		panic("plugger has no database available")
	}
	session := p.db.Session.Copy()
	return session, p.db.C("shared." + suffix).With(session)
}

// UniqueCollection returns a unique database collection that the plugin may
// use to store data. The collection is named "unique.<plugin name>.<suffix>",
// or "unique.<plugin name>" if the suffix is empty. If the plugin name has
// a label ("echo/label") the slash is replaced by an underline ("echo_label").
//
// See Plugger.SharedCollection.
func (p *Plugger) UniqueCollection(suffix string) (*mgo.Session, *mgo.Collection) {
	if p.db == nil {
		panic("plugger has no database available")
	}
	session := p.db.Session.Copy()
	name := strings.Replace(p.Name(), "/", "_", -1)
	if suffix == "" {
		name = "plugin." + name
	} else {
		name = "plugin." + name + "." + suffix
	}
	return session, p.db.C(name).With(session)
}

// Targets returns all targets enabled for the plugin.
func (p *Plugger) Targets() []PluginTarget {
	return p.targets
}

// Target returns the plugin target that matches the provided message.
// All messages provided to the plugin for handling are guaranteed
// to have a matching target.
func (p *Plugger) Target(msg *Message) *PluginTarget {
	addr := msg.Address()
	for i := range p.targets {
		if p.targets[i].address.Contains(addr) {
			return &p.targets[i]
		}
	}
	return nil
}

// LDAP returns the LDAP connection with the given name from the pool.
// The returned connection must be closed after its use.
func (p *Plugger) LDAP(name string) (ldap.Conn, error) {
	return p.ldap(name)
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

// SendNoticef sends a notice to the address obtained from the provided addressable.
// The message text is formed by providing format and args to fmt.Sprintf, and by
// prefixing the result with "nick: " if the message is addressed to a nick in
// a channel.
func (p *Plugger) SendNoticef(to Addressable, format string, args ...interface{}) error {
	text := fmt.Sprintf(format, args...)
	a := to.Address()
	if a.Channel != "" && a.Nick != "" {
		text = a.Nick + ": " + text
	}
	msg := &Message{Account: a.Account, Channel: a.Channel, Nick: a.Nick, Text: text, Command: "NOTICE"}
	return p.Send(msg)
}

// SendDirectf sends a direct message to the address obtained from the provided addressable.
// The message is sent privately if the address has a Nick, or to its Channel otherwise.
// The message text is formed by providing format and args to fmt.Sprintf.
func (p *Plugger) SendDirectf(to Addressable, format string, args ...interface{}) error {
	a := to.Address()
	if a.Nick != "" {
		a.Channel = ""
	}
	msg := &Message{Account: a.Account, Channel: a.Channel, Nick: a.Nick, Text: fmt.Sprintf(format, args...)}
	return p.Send(msg)
}

// SendDirectNoticef sends a direct notice to the address obtained from the provided addressable.
// The message is sent privately if the address has a Nick, or to its Channel otherwise.
// The message text is formed by providing format and args to fmt.Sprintf.
func (p *Plugger) SendDirectNoticef(to Addressable, format string, args ...interface{}) error {
	a := to.Address()
	if a.Nick != "" {
		a.Channel = ""
	}
	msg := &Message{Account: a.Account, Channel: a.Channel, Nick: a.Nick, Text: fmt.Sprintf(format, args...), Command: "NOTICE"}
	return p.Send(msg)
}

// SendChannelf sends a channel message to the address obtained from the provided addressable,
// or privately to the Nick if the address Channel is unset.
// The message text is formed by providing format and args to fmt.Sprintf.
func (p *Plugger) SendChannelf(to Addressable, format string, args ...interface{}) error {
	a := to.Address()
	if a.Channel != "" {
		a.Nick = ""
	}
	msg := &Message{Account: a.Account, Channel: a.Channel, Nick: a.Nick, Text: fmt.Sprintf(format, args...)}
	return p.Send(msg)
}

// SendChannelNoticef sends a channel notice to the address obtained from the provided addressable,
// or privately to the Nick if the address Channel is unset.
// The message text is formed by providing format and args to fmt.Sprintf.
func (p *Plugger) SendChannelNoticef(to Addressable, format string, args ...interface{}) error {
	a := to.Address()
	if a.Channel != "" {
		a.Nick = ""
	}
	msg := &Message{Account: a.Account, Channel: a.Channel, Nick: a.Nick, Text: fmt.Sprintf(format, args...), Command: "NOTICE"}
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

// BroadcastNoticef sends a notice to all configured plugin targets.
// The message text is formed by providing format and args to fmt.Sprintf, and by
// prefixing the result with "nick: " if the message is addressed to a nick in
// a channel.
func (p *Plugger) BroadcastNoticef(format string, args ...interface{}) error {
	msg := &Message{Command: "NOTICE", Text: fmt.Sprintf(format, args...)}
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

// MaxTextLen is the maximum amount of text accepted on the Text field
// of a message before the line is automatically broken down into
// multiple messages. The line breaking algorithm attempts to break the
// line on spaces, and attempts to preserve a minimum amount of content
// on the last line to prevent the output from looking awkward.
const MaxTextLen = 300

// minTextLen defines the minimum amount of content to attempt
// to preserve on the last line when the auto-line-breaking
// algorithm takes place to enforce MaxTextLen.
const minTextLen = 50

// Send sends msg to its defined address.
func (p *Plugger) Send(msg *Message) error {
	copy := *msg
	copy.Time = time.Now()
	copy.Text = strings.TrimRight(copy.Text, " \t")
	if len(copy.Text) <= MaxTextLen {
		if err := p.send(&copy); err != nil {
			logf("Cannot put message in outgoing queue: %v", err)
			return fmt.Errorf("cannot put message in outgoing queue: %v", err)
		}
		return nil
	}

	text := copy.Text
	for len(text) > MaxTextLen {
		split := MaxTextLen
		if i := strings.LastIndex(text[:split], " "); i > 0 {
			split = i
			if len(text)-split < minTextLen {
				suffix := text[(len(text)+1)/2:]
				if j := strings.Index(suffix, " "); j >= 0 {
					split = len(text) - len(suffix) + j
				}
			}
		} else if len(text)-MaxTextLen < minTextLen {
			split = (len(text) + 1) / 2
		}
		copy.Text = strings.TrimRight(text[:split], " ")
		text = strings.TrimLeft(text[split:], " ")
		if err := p.Send(&copy); err != nil {
			return err
		}
	}
	if len(text) > 0 {
		copy.Text = text
		return p.Send(&copy)
	}
	return nil
}
