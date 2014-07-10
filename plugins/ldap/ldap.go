package mup

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"time"

	"gopkg.in/niemeyer/mup.v0"
	"gopkg.in/niemeyer/mup.v0/ldap"
	"gopkg.in/tomb.v2"
	"labix.org/v2/mgo/bson"
)

func init() {
	mup.RegisterPlugin("ldap", startPlugin)
}

type ldapPlugin struct {
	mu       sync.Mutex
	tomb     tomb.Tomb
	plugger  *mup.Plugger
	prefix   string
	messages chan *mup.Message
	err      error
	config   struct {
		ldap.Config   `bson:",inline"`
		Command       string
		HandleTimeout bson.DurationString
	}
}

const (
	defaultCommand       = "poke"
	defaultHandleTimeout = 500 * time.Millisecond
)

func startPlugin(plugger *mup.Plugger) mup.Plugin {
	p := &ldapPlugin{
		plugger:  plugger,
		prefix:   defaultCommand,
		messages: make(chan *mup.Message),
	}
	plugger.Config(&p.config)
	if p.config.Command != "" {
		p.prefix = p.config.Command
	}
	p.prefix += " "
	if p.config.HandleTimeout.Duration == 0 {
		p.config.HandleTimeout.Duration = defaultHandleTimeout
	}
	p.tomb.Go(p.loop)
	return p
}

func (p *ldapPlugin) Stop() error {
	p.tomb.Kill(nil)
	return p.tomb.Wait()
}

func (p *ldapPlugin) Handle(msg *mup.Message) error {
	if !msg.ToMup || !strings.HasPrefix(msg.MupText, p.prefix) {
		return nil
	}
	select {
	case p.messages <- msg:
	case <-time.After(p.config.HandleTimeout.Duration):
		reply := "The LDAP server seems a bit sluggish right now. Please try again soon."
		p.mu.Lock()
		err := p.err
		p.mu.Unlock()
		if err != nil {
			reply = err.Error()
		}
		p.plugger.Replyf(msg, "%s", reply)
	}
	return nil
}

func (p *ldapPlugin) loop() error {
	for {
		err := p.dial()
		if !p.tomb.Alive() {
			return nil
		}
		p.mu.Lock()
		p.err = err
		p.mu.Unlock()
		for i := 0; i < 10 && p.tomb.Alive(); i++ {
			time.Sleep(500 * time.Millisecond)
		}
	}
}

func (p *ldapPlugin) dial() error {
	conn, err := ldap.Dial(&p.config.Config)
	if err != nil {
		p.plugger.Logf("%v", err)
		return err
	}
	defer conn.Close()
	p.mu.Lock()
	p.err = nil
	p.mu.Unlock()
	for err == nil {
		select {
		case msg := <-p.messages:
			err = p.handle(conn, msg)
			if err != nil {
				p.plugger.Replyf(msg, "Error talking to LDAP server: %v", err)
			}
		case <-time.After(mup.NetworkTimeout):
			err = conn.Ping()
		case <-p.tomb.Dying():
			err = tomb.ErrDying
		}
	}
	return err
}

var ldapAttributes = []string{
	"cn",
	"mozillaNickname",
	"mail",
	"mobile",
	"telephoneNumber",
	"homePhone",
	"skypePhone",
	"voipPhone",
	"mozillaCustom4",
}

var ldapFormat = []struct {
	attr   string
	format string
	filter func(string) string
}{
	{"mail", "<%s>", nil},
	{"mozillaCustom4", "<time:%s>", nil}, //ldapLocalTime},
	{"telephoneNumber", "<phone:%s>", nil},
	{"mobile", "<mobile:%s>", nil},
	{"homePhone", "<home:%s>", nil},
	{"voipPhone", "<voip:%s>", nil},
	{"skypePhone", "<skype:%s>", nil},
}

func (p *ldapPlugin) handle(conn ldap.Conn, msg *mup.Message) error {
	query := ldap.EscapeFilter(strings.TrimSpace(msg.MupText[len(p.prefix):]))
	search := ldap.Search{
		Filter: fmt.Sprintf("(|(mozillaNickname=%s)(cn=*%s*))", query, query),
		Attrs:  ldapAttributes,
	}
	result, err := conn.Search(&search)
	if err != nil {
		p.plugger.Replyf(msg, "Cannot search LDAP server right now: %v", err)
		return fmt.Errorf("cannot search LDAP server: %v", err)
	}
	if len(result) > 1 {
		p.plugger.Replyf(msg, "%s", p.formatEntries(result))
	} else if len(result) > 0 {
		p.plugger.Replyf(msg, "%s", p.formatEntry(&result[0]))
	} else {
		p.plugger.Replyf(msg, "Cannot find anyone matching this. :-(")
	}
	return nil
}

func (p *ldapPlugin) formatEntry(result *ldap.Result) string {
	var buf bytes.Buffer
	buf.Grow(250)
	cn := result.Value("cn")
	nick := result.Value("mozillaNickname")
	if nick != "" {
		buf.WriteString(nick)
		buf.WriteString(" is ")
		buf.WriteString(cn)
	} else {
		buf.WriteString(cn)
	}
	for _, item := range ldapFormat {
		for _, value := range result.Values(item.attr) {
			if value == "" {
				continue
			}
			if item.filter != nil {
				value = item.filter(value)
			}
			buf.WriteByte(' ')
			buf.WriteString(fmt.Sprintf(item.format, value))
		}
	}
	return buf.String()
}

func (p *ldapPlugin) formatEntries(results []ldap.Result) string {
	var buf bytes.Buffer
	buf.Grow(250)
	sizehint := 200
	i := 0
	for i < len(results) {
		result := &results[i]
		cn := result.Value("cn")
		nick := result.Value("mozillaNickname")
		maxsize := len(nick) + len(cn) + 6
		if maxsize > sizehint && i+1 < len(results) {
			break
		}
		if i > 0 {
			buf.WriteString(", ")
		}
		if nick != "" {
			buf.WriteString(nick)
			buf.WriteString(" is ")
			buf.WriteString(cn)
			sizehint -= maxsize
		} else {
			buf.WriteString(cn)
			sizehint -= len(cn)
		}
		i++
	}
	if i < len(results) {
		buf.WriteString(fmt.Sprintf(", plus %d more people.", len(results)-i))
	}
	return buf.String()
}
