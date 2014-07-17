package mup

import (
	"gopkg.in/mgo.v2/bson"
	"strings"
	"sync"
	"unicode"
)

type Message struct {
	Id bson.ObjectId `bson:"_id,omitempty"`

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

	// The bang prefix setting used to address messages to mup
	// that was in place when the message was received.
	Bang string `bson:",omitempty"`

	// The mup nick that was in place when the message was received.
	AsNick string `bson:",omitempty"`

	// TODO Drop these.
	ToMup   bool   `bson:",omitempty"`
	MupText string `bson:",omitempty"`
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
	m := &Message{Account: account, AsNick: asnick, Bang: bang}
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
			m.Nick = line[mark+1:i]
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
				m.User = line[mark+1:i]
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
				m.Host = line[mark+1:i]
			}
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

		if asnick != "" {
			// ToMup, MupText
			text := m.Text
			nl := len(m.AsNick)
			if nl > 0 && len(m.Text) > nl+1 && (m.Text[nl] == ':' || m.Text[nl] == ',') && m.Text[:nl] == m.AsNick {
				m.ToMup = true
				m.MupText = strings.TrimSpace(m.Text[nl+1:])
				text = m.MupText
			} else if m.Channel == "" {
				m.ToMup = true
				m.MupText = strings.TrimSpace(m.Text)
				text = m.MupText
			}

			// Bang
			bl := len(m.Bang)
			if bl > 0 && len(text) >= bl && text[:bl] == m.Bang && (len(text) == bl || unicode.IsLetter(rune(text[bl]))) {
				m.ToMup = true
				m.MupText = text[bl:]
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
