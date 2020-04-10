package phonenick_test

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	"gopkg.in/mup.v0"
	"gopkg.in/mup.v0/ldap"
	_ "gopkg.in/mup.v0/plugins/phonenick"

	. "gopkg.in/check.v1"
)

func Test(t *testing.T) { TestingT(t) }

var _ = Suite(&S{})

type S struct {
	db     *sql.DB
	ldap   *ldapConn
	tester *mup.PluginTester
}

func (s *S) SetUpTest(c *C) {
	mup.SetLogger(c)
	mup.SetDebug(true)

	db, err := mup.OpenDB(c.MkDir())
	c.Assert(err, IsNil)
	s.db = db
	s.ldap = newLdapConn()

	_, err = db.Exec("INSERT INTO account (name) VALUES ('test')")
	c.Assert(err, IsNil)

	s.tester = mup.NewPluginTester("phonenick")
	s.tester.SetDB(s.db)
	s.tester.SetConfig(mup.Map{"ldap": "test"})
	s.tester.SetLDAP("test", s.ldap)
}

func (s *S) TearDownTest(c *C) {
	s.tester.Stop()
	s.db.Close()

	mup.SetLogger(nil)
	mup.SetDebug(false)
}

func (s *S) TestIgnoreNonPhone(c *C) {
	s.ldap.phone = "ignore"
	s.ldap.nick = "monika"

	s.tester.Start()
	s.tester.Sendf("[,raw] :ignore!~user@signal PRIVMSG #chan :Hello")
	time.Sleep(100 * time.Millisecond)
	s.tester.Stop()

	select {
	case <-s.ldap.searched:
		c.Fatalf("Attempted to search LDAP for non-phone nick")
	default:
	}

	monikers := s.monikers(c)
	c.Assert(monikers, DeepEquals, []moniker(nil))
}

func (s *S) TestUpdateDelayWorks(c *C) {
	s.tester.Start()

	s.ldap.phone = "+12345"
	s.ldap.nick = "monika"
	s.tester.Sendf("[,raw] :+12345!~user@signal PRIVMSG #chan :Hello")
	s.ldap.waitSearch(c)

	s.ldap.nick = "ignore"
	s.tester.Sendf("[,raw] :+12345!~user@signal PRIVMSG #chan :Hello")
	time.Sleep(100 * time.Millisecond)

	s.tester.Stop()

	monikers := s.monikers(c)
	c.Assert(monikers, DeepEquals, []moniker{
		{account: "test", channel: "", nick: "+12345", name: "monika"},
	})
}

func (s *S) TestUpdateDelayZero(c *C) {
	s.tester.SetConfig(mup.Map{"ldap": "test", "updatedelay": "0"})

	s.tester.Start()

	s.ldap.phone = "+12345"
	s.ldap.nick = "monika"
	s.tester.Sendf("[,raw] :+12345!~user@signal PRIVMSG #chan :Hello")
	s.ldap.waitSearch(c)

	s.ldap.nick = "monike"
	s.tester.Sendf("[,raw] :+12345!~user@signal PRIVMSG #chan :Hello")
	s.ldap.waitSearch(c)

	s.tester.Stop()

	monikers := s.monikers(c)
	c.Assert(monikers, DeepEquals, []moniker{
		{account: "test", channel: "", nick: "+12345", name: "monike"},
	})
}

type moniker struct {
	account, channel, nick, name string
}

func (s *S) monikers(c *C) []moniker {
	var result []moniker
	rows, err := s.db.Query("SELECT account,channel,nick,name FROM moniker ORDER by account,channel,nick,name")
	c.Assert(err, IsNil)
	defer rows.Close()
	for rows.Next() {
		var m moniker
		err = rows.Scan(&m.account, &m.channel, &m.nick, &m.name)
		c.Assert(err, IsNil)
		result = append(result, m)
	}
	return result
}

type ldapConn struct {
	phone, nick string
	searched    chan bool
}

func newLdapConn() *ldapConn {
	return &ldapConn{
		searched: make(chan bool, 10),
	}
}

func (l ldapConn) waitSearch(c *C) {
	select {
	case <-l.searched:
	case <-time.After(3 * time.Second):
		c.Fatalf("Timeout waiting for plugin to perform LDAP search")
	}
}

func (l ldapConn) Search(s *ldap.Search) ([]ldap.Result, error) {
	filter := fmt.Sprintf(`(|(telephoneNumber=%s)(mobile=%s)(homePhone=%s)(voidPhone=%s)(skypePhone=%s))`,
		l.phone, l.phone, l.phone, l.phone, l.phone)
	if s.Filter != filter {
		println("Bad search:", s.Filter, "!=", filter)
		return nil, nil
	}
	l.searched <- true
	return []ldap.Result{l.result()}, nil
}

func (l ldapConn) result() ldap.Result {
	return ldap.Result{Attrs: []ldap.Attr{
		{Name: "mozillaNickname", Values: []string{l.nick}},
	}}
}

func (l ldapConn) Close() error { return nil }
