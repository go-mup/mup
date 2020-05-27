package mup

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gopkg.in/mup.v0/ldap"
)

// Plugger provides the interface between a plugin and the bot infrastructure.
type Plugger struct {
	name    string
	send    func(msg *Message) error
	handle  func(msg *Message) error
	ldap    func(name string) (ldap.Conn, error)
	config  json.RawMessage
	targets []Target
	db      *sql.DB
}

// Target defines an Account, Channel, and/or Nick that the given
// Plugin will observe messages from, and may choose to broadcast
// messages to. Empty fields are ignored when deciding whether a
// message matches the plugin target.
//
// A Target may also include configuration options that when
// understood by the plugin will only be considered for this
// particular target.
type Target struct {
	Plugin  string
	Account string
	Channel string
	Nick    string
	Config  string // JSON document
}

const targetColumns = "plugin,account,channel,nick,config"
const targetPlacers = "?,?,?,?,?"

func (t *Target) refs() []interface{} {
	return []interface{}{&t.Plugin, &t.Account, &t.Channel, &t.Nick, &t.Config}
}

// Address returns the address for the plugin target.
func (t Target) Address() Address {
	return Address{Account: t.Account, Channel: t.Channel, Nick: t.Nick}
}

// UnmarshalConfig unmarshals into result the plugin target configuration using the json package.
func (t Target) UnmarshalConfig(result interface{}) error {
	if t.Config == "" {
		return nil
	}
	err := json.Unmarshal([]byte(t.Config), result)
	if err != nil {
		return fmt.Errorf("cannot parse config for %s: %v", t, err)
	}
	return nil
}

// CanSend returns whether the plugin target may have messages sent to it.
// For that, it must have an Account set, and at least one of Channel and Nick.
func (t Target) CanSend() bool {
	return t.Account != "" && (t.Nick != "" || t.Channel != "")
}

// String returns a string representation of the plugin target suitable for log messages.
func (t Target) String() string {
	// The plugin name is not included in the result because it is the prefix
	// of every message logged by a plugin via the plugger, which is the
	// fundamental way in which targets are handled.
	if t.Nick != "" {
		if t.Channel == "" {
			return fmt.Sprintf("account %q, nick %q", t.Account, t.Nick)
		} else {
			return fmt.Sprintf("account %q, channel %q, nick %q", t.Account, t.Channel, t.Nick)
		}
	} else if t.Channel != "" {
		return fmt.Sprintf("account %q, channel %q", t.Account, t.Channel)
	}
	return fmt.Sprintf("account %q", t.Account)
}

var emptyDoc = json.RawMessage("{}")

func newPlugger(name string, send, handle func(msg *Message) error, ldap func(name string) (ldap.Conn, error)) *Plugger {
	return &Plugger{
		name:   name,
		send:   send,
		handle: handle,
		ldap:   ldap,
		config: emptyDoc,
	}
}

func (p *Plugger) setDatabase(db *sql.DB) {
	p.db = db
}

func (p *Plugger) setConfig(config json.RawMessage) {
	if len(config) == 0 || string(config) == "null" {
		p.config = emptyDoc
	} else {
		p.config = config
	}
}

func (p *Plugger) setTargets(targets []Target) {
	for i := range targets {
		t := &targets[i]
		if t.Plugin == "" {
			t.Plugin = p.name
		} else if t.Plugin != p.name {
			panic(fmt.Sprintf("Plugger for %q got Target for wrong plugin %q: %s", p.name, t.Plugin, t))
		}
	}
	p.targets = targets
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

// UnmarshalConfig unmarshals into result the plugin configuration using the json package.
func (p *Plugger) UnmarshalConfig(result interface{}) error {
	// The plugin name is not included in the message because it is the prefix
	// of every message logged by a plugin via the plugger.
	err := json.Unmarshal(p.config, result)
	if err != nil {
		return fmt.Errorf("cannot parse plugin config: %v", err)
	}
	return err
}

// DB returns a reference to the underlying database.
func (p *Plugger) DB() *sql.DB {
	return p.db
}

// Handle inserts the provided message on the incoming queue for processing.
func (p *Plugger) Handle(msg *Message) error {
	copy := *msg
	for _, target := range p.Targets() {
		if msg.Account == "" {
			copy.Account = target.Account
		}
		if target.Account == "" || !target.Address().Contains(copy.Address()) {
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
func (p *Plugger) Targets() []Target {
	return p.targets
}

// Target returns the plugin target that matches the provided message.
// All messages provided to the plugin for handling are guaranteed
// to have a matching target.
func (p *Plugger) Target(msg *Message) Target {
	addr := msg.Address()
	for i := range p.targets {
		if p.targets[i].Address().Contains(addr) {
			return p.targets[i]
		}
	}
	return Target{}
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
	msg := &Message{Account: a.Account, Channel: a.Channel, Nick: a.Nick, Text: p.replyText(a, text)}
	return p.Send(msg)
}

func (p *Plugger) replyText(a Address, text string) string {
	if a.Nick != "" {
		if p.db != nil {
			var moniker string
			row := p.db.QueryRow("SELECT name FROM moniker "+
				" WHERE account=? AND nick=? AND name!='' AND (channel='' OR channel=?)"+
				" ORDER BY channel DESC",
				a.Account, a.Nick, a.Channel)
			err := row.Scan(&moniker)
			if err == nil {
				a.Nick = moniker
			} else if err != sql.ErrNoRows {
				p.Logf("Cannot check for moniker on reply: %v", err)
			}
		}
		if a.Channel != "" && a.Channel[0] != '@' {
			if a.Host == "telegram" || a.Host == "webhook" {
				text = "@" + a.Nick + " " + text
			} else {
				text = a.Nick + ": " + text
			}
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
		copy.Account = t.Account
		copy.Channel = t.Channel
		copy.Nick = t.Nick
		copy.Text = p.replyText(t.Address(), copy.Text)
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
