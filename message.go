package mup

import (
	"gopkg.in/mgo.v2/bson"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	cmdWelcome   = "001"
	cmdNickInUse = "433"
	cmdPrivMsg   = "PRIVMSG"
	cmdNotice    = "NOTICE"
	cmdNick      = "NICK"
	cmdPing      = "PING"
	cmdPong      = "PONG"
	cmdJoin      = "JOIN"
	cmdPart      = "PART"
	cmdQuit      = "QUIT"
)

type Message struct {
	Id bson.ObjectId `bson:"_id,omitempty"`

	// When the message was received or queued out.
	Time time.Time

	// These fields form the message Address.
	Account string `bson:",omitempty"`
	Channel string `bson:",omitempty"`
	Nick    string `bson:",omitempty"`
	User    string `bson:",omitempty"`
	Host    string `bson:",omitempty"`

	// The IRC protocol command.
	Command string `bson:",omitempty"`

	// Raw parameters when not a PRIVMSG or NOTICE, and excluding Text.
	Params []string `bson:",omitempty"`

	// The trailing message text for all relevant commands.
	Text string `bson:",omitempty"`

	// The text that was targetted at the bot in a direct message or
	// a channel message prefixed by the bot's nick or the bang string.
	// The bot nick and the bang string prefixes are stripped out.
	BotText string `bson:",omitempty"`

	// The bang prefix setting used to address messages to mup
	// that was in place when the message was received.
	Bang string `bson:",omitempty"`

	// The bot nick that was in place when the message was received.
	AsNick string `bson:",omitempty"`
}

// Address holds the fully qualified address of an incoming or outgoing message.
type Address struct {
	Account string `bson:",omitempty"`
	Channel string `bson:",omitempty"`
	Nick    string `bson:",omitempty"`
	User    string `bson:",omitempty"`
	Host    string `bson:",omitempty"`
}

// Address returns a itself so it also implements Addressable.
func (a Address) Address() Address {
	return a
}

// Contains returns whether address a contains address b.
// For containment purposes an empty value on address a is considered
// as a wildcard, and User and Host are both ignored.
func (a Address) Contains(b Address) bool {
	return (a.Account == "" || a.Account == b.Account) &&
		(a.Nick == "" || a.Nick == b.Nick) &&
		(a.Channel == "" || a.Channel == b.Channel)
}

// Addressable is implemented by types that have a meaningful message address.
type Addressable interface {
	Address() Address
}

// Address returns the message origin or destination address.
func (m *Message) Address() Address {
	return Address{
		Account: m.Account,
		Channel: m.Channel,
		Nick:    m.Nick,
		User:    m.User,
		Host:    m.Host,
	}
}

var linePool = sync.Pool{New: func() interface{} { return make([]byte, 0, 512) }}

// String returns the message as an IRC protocol line.
func (m *Message) String() string {
	line := linePool.Get().([]byte)
	if m.Nick != "" && m.AsNick != "" {
		line = append(line, ':')
		line = append(line, m.Nick...)
		if m.User != "" {
			line = append(line, '!')
			line = append(line, m.User...)
		}
		if m.Host != "" {
			line = append(line, '@')
			line = append(line, m.Host...)
		}
		line = append(line, ' ')
	} else if m.Host != "" && m.AsNick != "" {
		line = append(line, ':')
		line = append(line, m.Host...)
		line = append(line, ' ')
	}
	cmd := m.Command
	if cmd == "" {
		cmd = cmdPrivMsg
	}
	line = append(line, cmd...)
	if cmd == cmdPrivMsg || cmd == cmdNotice {
		target := m.Channel
		if target == "" {
			if m.AsNick != "" {
				target = m.AsNick
			} else {
				target = m.Nick
			}
		}
		line = append(line, ' ')
		line = append(line, target...)
	} else if len(m.Params) > 0 {
		for _, param := range m.Params {
			line = append(line, ' ')
			line = append(line, param...)
		}
	}
	if m.Text != "" {
		line = append(line, ' ', ':')
		line = append(line, m.Text...)
	}
	for i, c := range line {
		switch c {
		case '\r', '\n', '\x00':
			line[i] = '_'
		}
	}
	linestr := string(line)
	linePool.Put(line[:0])
	return linestr
}

func isChannel(name string) bool {
	return name != "" && (name[0] == '#' || name[0] == '&') && !strings.ContainsAny(name, " ,\x07")
}

// ParseIncoming parses line as an incoming IRC protocol message line.
// The provided account, nick, and bang string inform the respective connection
// settings in use when the message was received, so that messages addressed
// to mup's nick via the IRC command, via a nick prefix in the message text,
// or via the bang string (as in "!echo bar"), may be properly processed.
func ParseIncoming(account, asnick, bang, line string) *Message {
	return parse(account, asnick, bang, line)
}

// ParseOutgoing parses line as an outgoing IRC protocol message line.
func ParseOutgoing(account, line string) *Message {
	return parse(account, "", "", line)
}

func parse(account, asnick, bang, line string) *Message {
	m := &Message{Account: account, AsNick: asnick, Bang: bang, Time: time.Now()}
	i := 0
	l := len(line)
	for i < l && line[i] == ' ' {
		i++
	}

	// Nick, User, Host
	if i < l && line[i] == ':' {
		mark := i
		for i++; i < l; i++ {
			c := line[i]
			if c == ' ' || c == '!' || c == '@' {
				break
			}
		}
		if asnick != "" {
			m.Nick = line[mark+1 : i]
		}
		if i < l && line[i] == '!' {
			mark := i
			for i++; i < l; i++ {
				c := line[i]
				if c == ' ' || c == '@' {
					break
				}
			}
			if asnick != "" {
				m.User = line[mark+1 : i]
			}
		}
		if i < l && line[i] == '@' {
			mark := i
			for i++; i < l; i++ {
				c := line[i]
				if c == ' ' {
					break
				}
			}
			if asnick != "" {
				m.Host = line[mark+1 : i]
			}
		}
		if m.User == "" && m.Host == "" && strings.Contains(m.Nick, ".") {
			m.Host = m.Nick
			m.Nick = ""
		}
	}
	for i < l && line[i] == ' ' {
		i++
	}

	// Command
	mark := i
	for i < l && line[i] != ' ' {
		i++
	}
	m.Command = line[mark:i]
	for i < l && line[i] == ' ' {
		i++
	}

	if m.Command == cmdPrivMsg || m.Command == cmdNotice {
		// Target
		mark = i
		for i < l && line[i] != ' ' {
			i++
		}
		target := line[mark:i]
		if isChannel(target) {
			m.Channel = target
		} else if asnick == "" {
			m.Nick = target
		}

		// Text
		for i < l && line[i] == ' ' {
			i++
		}
		if i < l && line[i] == ':' {
			m.Text = line[i+1:]
		}

		if asnick != "" && m.Command == cmdPrivMsg {
			// BotText
			t1 := m.Text
			t2 := m.Text
			if len(t1) > 0 && t1[0] == '@' {
				t1 = t1[1:]
			}
			nl := len(m.AsNick)
			if nl > 0 && len(t1) > nl+1 && (t1[nl] == ':' || t1[nl] == ',' || len(t1) != len(m.Text)) && t1[:nl] == m.AsNick {
				m.BotText = strings.TrimSpace(t1[nl+1:])
				t2 = m.BotText
			} else if m.Channel == "" {
				m.BotText = strings.TrimSpace(m.Text)
				t2 = m.BotText
			}

			// Bang
			bl := len(m.Bang)
			if bl > 0 && len(t2) >= bl && t2[:bl] == m.Bang && (len(t2) == bl || unicode.IsLetter(rune(t2[bl]))) {
				m.BotText = t2[bl:]
			}
		}
	} else {
		// Params, Text
		for i < l {
			if line[i] == ':' {
				m.Text = line[i+1:]
				break
			}
			mark = i
			for i < l && line[i] != ' ' {
				i++
			}
			m.Params = append(m.Params, line[mark:i])
			for i < l && line[i] == ' ' {
				i++
			}
		}
	}

	return m
}
