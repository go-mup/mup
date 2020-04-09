package mup

import (
	"bytes"
	"fmt"
	"regexp"
	"sync"
	"time"

	"gopkg.in/mup.v0"
	"gopkg.in/mup.v0/ldap"
	"gopkg.in/mup.v0/schema"
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
	Help: `Searches people in the LDAP directory.
	
	The provided query will be searched for as an exact IRC nick ("mozillaNickname")
	or part of a name ("cn").
	`,
	Args: schema.Args{{
		Name: "query",
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
		LDAP string
	}
}

func start(plugger *mup.Plugger) mup.Stopper {
	p := &ldapPlugin{
		plugger:  plugger,
		commands: make(chan *mup.Command, 5),
	}
	err := plugger.UnmarshalConfig(&p.config)
	if err != nil {
		plugger.Logf("%v", err)
	}
	p.tomb.Go(p.loop)
	return p
}

func (p *ldapPlugin) Stop() error {
	close(p.commands)
	return p.tomb.Wait()
}

func (p *ldapPlugin) HandleCommand(cmd *mup.Command) {
	select {
	case p.commands <- cmd:
	default:
		p.plugger.Sendf(cmd, "The LDAP server seems a bit sluggish right now. Please try again soon.")
	}
}

func (p *ldapPlugin) loop() error {
	for {
		cmd, ok := <-p.commands
		if !ok {
			return nil
		}
		conn, err := p.plugger.LDAP(p.config.LDAP)
		if err != nil {
			p.plugger.Sendf(cmd, "Plugin configuration error: %s.", err)
		} else {
			p.handle(conn, cmd)
			conn.Close()
		}
	}
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
	{"mozillaCustom4", "<time:%s>", ldapLocalTime},
	{"telephoneNumber", "<phone:%s>", nil},
	{"mobile", "<mobile:%s>", nil},
	{"homePhone", "<home:%s>", nil},
	{"voipPhone", "<voip:%s>", nil},
	{"skypePhone", "<skype:%s>", nil},
}

func ldapLocalTime(v string) string {
	t, err := time.Parse("2006 -0700", "0001 "+v)
	if err != nil {
		return v
	}
	t = t.UTC()
	there := time.Now().UTC().Add(time.Time{}.Sub(t))
	return there.Format("15h04") + v
}

var phoneRegexp = regexp.MustCompile(`^\+?[0-9-]{2,}$`)

func (p *ldapPlugin) handle(conn ldap.Conn, cmd *mup.Command) {
	var args struct{ Query string }
	cmd.Args(&args)
	query := ldap.EscapeFilter(args.Query)
	search := ldap.Search{Attrs: ldapAttributes}
	if phoneRegexp.MatchString(args.Query) {
		search.Filter = fmt.Sprintf("(|(telephoneNumber=*%s*)(mobile=*%s*)(homePhone=*%s*)(voidPhone=*%s*)(skypePhone=*%s*))", query, query, query, query, query)
	} else {
		search.Filter = fmt.Sprintf("(|(mozillaNickname=%s)(cn=*%s*))", query, query)
	}
	result, err := conn.Search(&search)
	if err != nil {
		p.plugger.Logf("Cannot search LDAP server: %v", err)
		p.plugger.Sendf(cmd, "Cannot search LDAP server: %v", err)
	} else if len(result) > 1 {
		p.plugger.Sendf(cmd, "%s", p.formatEntries(result))
	} else if len(result) > 0 {
		p.plugger.Sendf(cmd, "%s", p.formatEntry(&result[0]))
	} else {
		p.plugger.Sendf(cmd, "Cannot find anyone matching this. :-(")
	}
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
