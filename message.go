package mup

import (
	"fmt"
	"labix.org/v2/mgo/bson"
	"strings"
	"unicode"
)

type Message struct {
	Id      bson.ObjectId `bson:"_id,omitempty"`
	Account string        `bson:",omitempty"`
	Prefix  string        `bson:",omitempty"`
	Nick    string        `bson:",omitempty"`
	User    string        `bson:",omitempty"`
	Host    string        `bson:",omitempty"`
	Cmd     string        `bson:",omitempty"`
	Params  []string      `bson:",omitempty"`
	Target  string        `bson:",omitempty"`
	Text    string        `bson:",omitempty"`
	Bang    string        `bson:",omitempty"`
	ToMup   bool          `bson:",omitempty"`
	MupText string        `bson:",omitempty"`
	MupNick string        `bson:",omitempty"`
}

func (m *Message) String() string {
	cmd := m.Cmd
	if cmd == "" {
		cmd = "PRIVMSG"
	}
	if len(m.Prefix) > 0 {
		cmd = fmt.Sprint(":", m.Prefix, " ", m.Cmd)
	} else if len(m.Nick) > 0 || len(m.User) > 0 || len(m.Host) > 0 {
		cmd = fmt.Sprint(":", m.Nick, "!", m.User, "@", m.Host, " ", m.Cmd)
	}
	if len(m.Params) > 0 {
		return fmt.Sprint(cmd, " ", strings.Join(m.Params, " "))
	} else if m.Target != "" {
		return fmt.Sprint(cmd, " ", m.Target, " :", m.Text)
	} else if m.Text != "" {
		return fmt.Sprint(cmd, " :", m.Text)
	}
	return cmd
}

func isChannel(name string) bool {
	return name != "" && (name[0] == '#' || name[0] == '&') && !strings.ContainsAny(name, " ,\x07")
}

func (m *Message) ReplyTarget() string {
	if m.Target == m.MupNick {
		return m.Nick
	}
	return m.Target
}

func ParseMessage(mupnick, bang, line string) *Message {
	m := &Message{MupNick: mupnick, Bang: bang}
	i := 0
	l := len(line)
	for i < l && line[i] == ' ' {
		i++
	}

	// Prefix, Nick, User, Host
	if i < l && line[i] == ':' {
		i++
		prefix := i
		part := i
		for i < l && line[i] != ' ' {
			if line[i] == '!' && m.Nick == "" {
				m.Nick = line[part:i]
				part = i + 1
			}
			if line[i] == '@' && m.Nick != "" && m.User == "" {
				m.User = line[part:i]
				part = i + 1
			}
			i++
		}
		if m.User != "" && m.Host == "" {
			m.Host = line[part:i]
		}
		m.Prefix = line[prefix:i]
	}
	for i < l && line[i] == ' ' {
		i++
	}

	// Cmd
	command := i
	for i < l && line[i] != ' ' {
		i++
	}
	m.Cmd = line[command:i]
	for i < l && line[i] == ' ' {
		i++
	}

	// Params, Text
	for i < l {
		if line[i] == ':' {
			m.Text = line[i+1:]
			m.Params = append(m.Params, line[i:])
			break
		}
		param := i
		for i < l && line[i] != ' ' {
			i++
		}
		m.Params = append(m.Params, line[param:i])
		for i < l && line[i] == ' ' {
			i++
		}
	}

	if m.Cmd == cmdPrivMsg || m.Cmd == cmdNotice {
		// Target
		if len(m.Params) > 0 {
			m.Target = m.Params[0]
		}

		// ToMup, MupText
		text := m.Text
		nl := len(m.MupNick)
		if nl > 0 && len(m.Text) > nl+1 && (m.Text[nl] == ':' || m.Text[nl] == ',') && m.Text[:nl] == m.MupNick {
			m.ToMup = true
			m.MupText = strings.TrimSpace(m.Text[nl+1:])
			text = m.MupText
		} else if m.Target != "" && m.Target == m.MupNick {
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

	return m
}
