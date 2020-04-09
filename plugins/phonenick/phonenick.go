package phonenick

import (
	"fmt"
	"regexp"
	"sync"
	"time"

	"gopkg.in/mup.v0"
	"gopkg.in/mup.v0/ldap"
	"gopkg.in/tomb.v2"
)

var Plugin = mup.PluginSpec{
	Name:     "phonenick",
	Help:     `Replaces Signal phone numbers by user nicks from LDAP directory.`,
	Start:    start,
	Commands: nil,
}

func init() {
	mup.RegisterPlugin(&Plugin)
}

type phonenickPlugin struct {
	mu       sync.Mutex
	tomb     tomb.Tomb
	plugger  *mup.Plugger
	messages chan *mup.Message
	updated  map[string]time.Time
	err      error
	config   struct {
		LDAP string

		UpdateDelay mup.DurationString
	}
}

const (
	defaultUpdateDelay = 24 * time.Hour
)

func start(plugger *mup.Plugger) mup.Stopper {
	p := &phonenickPlugin{
		plugger:  plugger,
		messages: make(chan *mup.Message, 10),
		updated:  make(map[string]time.Time),
	}
	p.config.UpdateDelay.Duration = -1
	err := plugger.UnmarshalConfig(&p.config)
	if err != nil {
		plugger.Logf("%v", err)
	}
	if p.config.UpdateDelay.Duration < 0 {
		p.config.UpdateDelay.Duration = defaultUpdateDelay
	}
	p.tomb.Go(p.loop)
	return p
}

func (p *phonenickPlugin) Stop() error {
	close(p.messages)
	p.tomb.Kill(nil)
	return p.tomb.Wait()
}

func (p *phonenickPlugin) HandleMessage(msg *mup.Message) {
	select {
	case p.messages <- msg:
	default:
		p.plugger.Logf("LDAP server seems a bit sluggish right now. Skipping message.")
	}
}

func (p *phonenickPlugin) loop() error {
	for {
		select {
		case msg, ok := <-p.messages:
			if !ok {
				return nil
			}
			p.handle(msg)
		}
	}
}

var phoneRegexp = regexp.MustCompile(`^\+?[0-9-]{2,}$`)

func (p *phonenickPlugin) handle(msg *mup.Message) {
	now := time.Now()
	if !phoneRegexp.MatchString(msg.Nick) || now.Sub(p.updated[msg.Nick]) < p.config.UpdateDelay.Duration {
		return
	}
	p.updated[msg.Nick] = now

	conn, err := p.plugger.LDAP(p.config.LDAP)
	if err != nil {
		p.plugger.Logf("Plugin configuration error: %s", err)
		return
	}
	defer conn.Close()

	query := ldap.EscapeFilter(msg.Nick)
	search := &ldap.Search{
		Filter: fmt.Sprintf("(|(telephoneNumber=%s)(mobile=%s)(homePhone=%s)(voidPhone=%s)(skypePhone=%s))", query, query, query, query, query),
		Attrs:  []string{"mozillaNickname"},
	}
	results, err := conn.Search(search)
	if err != nil {
		p.plugger.Logf("Cannot search LDAP server: %v", err)
		return
	}
	if len(results) == 0 {
		p.plugger.Logf("Cannot find requested nick in LDAP server: %q", msg.Nick)
		return
	}
	receiver := results[0]
	moniker := receiver.Value("mozillaNickname")
	if moniker == "" {
		p.plugger.Logf("Phone %q has no associated nick in LDAP server.", msg.Nick)
		return
	}

	db := p.plugger.DB()
	_, err = db.Exec("INSERT OR REPLACE INTO moniker (account,nick,name) VALUES (?,?,?)",
		msg.Account, msg.Nick, moniker)
	if err != nil {
		p.plugger.Logf("Cannot update moniker for %q to %q: %v", msg.Nick, moniker, err)
		return
	}
}
