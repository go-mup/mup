package mup_test

import (
	"strings"
	"testing"
	"time"

	. "gopkg.in/check.v1"
	"gopkg.in/niemeyer/mup.v0"
	"gopkg.in/niemeyer/mup.v0/ldap"
	_ "gopkg.in/niemeyer/mup.v0/plugins/ldap"
)

func Test(t *testing.T) { TestingT(t) }

var _ = Suite(&LDAPSuite{})

type LDAPSuite struct{}

var ldapTests = []struct {
	target   string
	send     []string
	recv     []string
	settings interface{}
}{
	{
		"mup",
		[]string{"poke notfound"},
		[]string{"PRIVMSG nick :Cannot find anyone matching this. :-("},
		nil,
	}, {
		"mup",
		[]string{"poke slow", "poke notfound"},
		[]string{
			"PRIVMSG nick :The LDAP server seems a bit sluggish right now. Please try again soon.",
			"PRIVMSG nick :Cannot find anyone matching this. :-(",
		},
		map[string]int{"handletimeout": 100},
	}, {
		"mup",
		[]string{"poke tesla"},
		[]string{"PRIVMSG nick :tesla is Nikola Tesla <tesla@example.com> <mobile:+11> <mobile:+22> <home:+33> <voip:+44> <skype:+55>"},
		nil,
	}, {
		"mup",
		[]string{"poke euler"},
		[]string{"PRIVMSG nick :euler is Leonhard Euler <euler@example.com>"},
		nil,
	}, {
		"mup",
		[]string{"poke eu"},
		[]string{"PRIVMSG nick :euler is Leonhard Euler, euclid is Euclid of Alexandria"},
		nil,
	}, {
		"mup",
		[]string{"poke e"},
		[]string{"PRIVMSG nick :tesla is Nikola Tesla, euler is Leonhard Euler, euclid is Euclid of Alexandria, riemann is Bernhard Riemann, einstein is Albert Einstein, newton is Isaac Newton, galieleo is Galileo Galilei, plus 2 more people."},
		nil,
	},
}

var nikolaTesla = ldap.Result{
	Attrs: []ldap.Attr{
		{Name: "mozillaNickname", Values: []string{"tesla"}},
		{Name: "cn", Values: []string{"Nikola Tesla"}},
		{Name: "mail", Values: []string{"tesla@example.com"}},
		{Name: "mobile", Values: []string{"+11", "+22"}},
		{Name: "homePhone", Values: []string{"+33"}},
		{Name: "voipPhone", Values: []string{"+44"}},
		{Name: "skypePhone", Values: []string{"+55"}},
	},
}

func ldapResult(nick, name string) ldap.Result {
	return ldap.Result{Attrs: []ldap.Attr{
		{Name: "mozillaNickname", Values: []string{nick}},
		{Name: "cn", Values: []string{name}},
		{Name: "mail", Values: []string{nick + "@example.com"}},
	}}
}

var ldapEntries = []ldap.Result{
	nikolaTesla,
	ldapResult("euler", "Leonhard Euler"),
	ldapResult("euclid", "Euclid of Alexandria"),
	ldapResult("riemann", "Bernhard Riemann"),
	ldapResult("einstein", "Albert Einstein"),
	ldapResult("newton", "Isaac Newton"),
	ldapResult("galieleo", "Galileo Galilei"),
	ldapResult("jonvon", "Jon von Neumann"),
	ldapResult("gauss", "Carl Friedrich Gauss"),
}

var ldapResults = map[string][]ldap.Result{
	"(mozillaNickname=tesla)":                {ldapEntries[0]},
	"(|(mozillaNickname=tesla)(cn=*tesla*))": {ldapEntries[0]},
	"(|(mozillaNickname=euler)(cn=*euler*))": {ldapEntries[1]},
	"(|(mozillaNickname=eu)(cn=*eu*))":       {ldapEntries[1], ldapEntries[2]},
	"(|(mozillaNickname=e)(cn=*e*))":         ldapEntries,
}

type ldapConn struct {
	s *ldap.Settings
}

func (l *ldapConn) Search(s *ldap.Search) ([]ldap.Result, error) {
	if strings.Contains(s.Filter, "=slow)") {
		time.Sleep(150 * time.Millisecond)
	}
	return ldapResults[s.Filter], nil
}

func (l *ldapConn) Ping() error  { return nil }
func (l *ldapConn) Close() error { return nil }

func (s *LDAPSuite) SetUpSuite(c *C) {
	ldap.TestDial = func(s *ldap.Settings) (ldap.Conn, error) { return &ldapConn{s}, nil }
	mup.SetLogger(c)
	mup.SetDebug(true)
}

func (s *LDAPSuite) TearDownSuite(c *C) {
	ldap.TestDial = nil
	mup.SetLogger(nil)
	mup.SetDebug(false)
}

func (s *LDAPSuite) TestLDAP(c *C) {
	for i, test := range ldapTests {
		c.Logf("Starting test %d with messages: %v", i, test.send)
		tester := mup.StartPluginTest("ldap", test.settings)
		err := tester.SendAll(test.target, test.send)
		c.Assert(err, IsNil)
		c.Assert(tester.Stop(), IsNil)
		c.Assert(tester.RecvAll(), DeepEquals, test.recv)
	}
}
