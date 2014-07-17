package mup

import (
	"bytes"
	"fmt"
	"sync"
	"time"

	"gopkg.in/mgo.v2/bson"
	"gopkg.in/niemeyer/mup.v0"
	"gopkg.in/niemeyer/mup.v0/ldap"
	"gopkg.in/niemeyer/mup.v0/schema"
	"gopkg.in/tomb.v2"
)

var Plugin = mup.PluginSpec{
	Name: "ldap",
	Help: `Exposes the poke command for searching people on an LDAP directory.

	The search happens against the name (cn) and nick (mozillaNickname)
	registered in the directory. Information displayed includes nick, name,
	email, time, and phone numbers.
	`,
	Start:    start,
	Commands: Commands,
}

var Commands = schema.Commands{{
	Name: "poke",
	Help: "Searches people in the LDAP directory.",
	Args: schema.Args{{
		Name: "query",
		Help: "Exact IRC nick (mozillaNickname) or part of the name (cn).",
		Flag: schema.Required | schema.Trailing,
	}},
}}

func init() {
	mup.RegisterPlugin(&Plugin)
}

type ldapPlugin struct {
	mu       sync.Mutex
	tomb     tomb.Tomb
	plugger  *mup.Plugger
	commands chan *mup.Command
	err      error
	config   struct {
		ldap.Config   `bson:",inline"`
		HandleTimeout bson.DurationString
	}
}

const defaultHandleTimeout = 500 * time.Millisecond

func start(plugger *mup.Plugger) (mup.Stopper, error) {
	p := &ldapPlugin{
		plugger:  plugger,
		commands: make(chan *mup.Command),
	}
	plugger.Config(&p.config)
	if p.config.HandleTimeout.Duration == 0 {
		p.config.HandleTimeout.Duration = defaultHandleTimeout
	}
	p.tomb.Go(p.loop)
	return p, nil
}

func (p *ldapPlugin) Stop() error {
	p.tomb.Kill(nil)
	return p.tomb.Wait()
}

func (p *ldapPlugin) HandleCommand(cmd *mup.Command) error {
	select {
	case p.commands <- cmd:
	case <-time.After(p.config.HandleTimeout.Duration):
		reply := "The LDAP server seems a bit sluggish right now. Please try again soon."
		p.mu.Lock()
		err := p.err
		p.mu.Unlock()
		if err != nil {
			reply = err.Error()
		}
		p.plugger.Sendf(cmd, "%s", reply)
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
		select {
		case <-p.tomb.Dying():
			return nil
		case <-time.After(5 * time.Second):
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
		case cmd := <-p.commands:
			err = p.handle(conn, cmd)
			if err != nil {
				p.plugger.Sendf(cmd, "Error talking to LDAP server: %v", err)
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

func (p *ldapPlugin) handle(conn ldap.Conn, cmd *mup.Command) error {
	var args struct{ Query string }
	cmd.Args(&args)
	query := ldap.EscapeFilter(args.Query)
	search := ldap.Search{
		Filter: fmt.Sprintf("(|(mozillaNickname=%s)(cn=*%s*))", query, query),
		Attrs:  ldapAttributes,
	}
	result, err := conn.Search(&search)
	if err != nil {
		p.plugger.Sendf(cmd, "Cannot search LDAP server right now: %v", err)
		return fmt.Errorf("cannot search LDAP server: %v", err)
	}
	if len(result) > 1 {
		p.plugger.Sendf(cmd, "%s", p.formatEntries(result))
	} else if len(result) > 0 {
		p.plugger.Sendf(cmd, "%s", p.formatEntry(&result[0]))
	} else {
		p.plugger.Sendf(cmd, "Cannot find anyone matching this. :-(")
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
