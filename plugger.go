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
	handle  func(msg *Message) error
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

func newPlugger(name string, send, handle func(msg *Message) error, ldap func(name string) (ldap.Conn, error)) *Plugger {
	return &Plugger{
		name:   name,
		send:   send,
		handle: handle,
		ldap:   ldap,
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

// CollKind flags tune the behavior of the Plugger.Collection method.
type CollKind int

const (
	Shared CollKind = 1 << iota
	Bulk
)

// Collection returns a mgo session and a database collection for plugin-specific data.
// The returned session must be closed after the collection use is finished.
//
// By default the returned collection is stored in the main bot database and
// is named "unique.<plugin name>.<suffix>", or "unique.<plugin name>" if the
// suffix is empty. If the plugin name is followed by a label (as in "name/label")
// the slash is replaced by an underline ("name_label").
//
// The kind argument may be used to tune these details, and may consist of
// zero or more of the following flags ORed together:
//
//    mup.Shared
//
//       The returned collection is named "shared.<suffix>", or "shared.<plugin name>"
//       if the suffix is empty, so it may be shared across multiple instances of the
//       same plugin or across multiple plugins.
//
//    mup.Bulk
//
//       The returned collection is written to the database "<main db name>_bulk".
//       This should be used by plugins that intend to read or write a significant
//       amount of data, to prevent fragmenting the main bot database.
//
func (p *Plugger) Collection(suffix string, kind CollKind) (*mgo.Session, *mgo.Collection) {
	if p.db == nil {
		panic("plugger has no database available")
	}
	session := p.db.Session.Copy()
	var name string
	if kind&Shared == Shared {
		if suffix == "" {
			suffix = pluginKey(p.Name())
		}
		name = "shared." + suffix
	} else {
		name = strings.Replace(p.Name(), "/", "_", -1)
		if suffix == "" {
			name = "unique." + name
		} else {
			name = "unique." + name + "." + suffix
		}
	}
	var c *mgo.Collection
	if kind&Bulk == Bulk {
		c = session.DB(p.db.Name + "_bulk").C(name)
	} else {
		c = p.db.C(name).With(session)
	}
	return session, c
}

// Handle inserts the provided message on the incoming queue for processing.
func (p *Plugger) Handle(msg *Message) error {
	copy := *msg
	for _, target := range p.Targets() {
		if msg.Account == "" {
			copy.Account = target.address.Account
		}
		if target.address.Account == "" || !target.address.Contains(copy.Address()) {
			continue
		}
		if err := p.handle(&copy); err != nil {
			logf("Cannot put message in incoming queue: %v", err)
			return fmt.Errorf("cannot put message in incoming queue: %v", err)
		}
	}
	return nil
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
	msg := &Message{Account: a.Account, Channel: a.Channel, Nick: a.Nick, Text: replyText(a, text)}
	return p.Send(msg)
}

func replyText(a Address, text string) string {
	if a.Channel != "" && a.Channel[0] != '@' && a.Nick != "" {
		if a.Host == "telegram" || a.Host == "webhook" {
			text = "@" + a.Nick + " " + text
		} else {
			text = a.Nick + ": " + text
		}
	}
	return text
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
		copy.Text = replyText(t.address, copy.Text)
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
