package mup_test

import (
	"strings"
	"testing"
	"time"

	. "gopkg.in/check.v1"
	"gopkg.in/mgo.v2/bson"
	"gopkg.in/mup.v0"
	"gopkg.in/mup.v0/ldap"
	_ "gopkg.in/mup.v0/plugins/poke"
)

func Test(t *testing.T) { TestingT(t) }

var _ = Suite(&LDAPSuite{})

type LDAPSuite struct{}

var pokeTests = []struct {
	target string
	send   []string
	recv   []string
	config bson.M
}{
	{
		send: []string{"poke notfound"},
		recv: []string{"PRIVMSG nick :Cannot find anyone matching this. :-("},
	}, {
		send: []string{"poke noldap"},
		recv: []string{`PRIVMSG nick :Plugin configuration error: LDAP connection "unknown" not found.`},
		config: bson.M{"ldap": "unknown"},
	}, {
		send: []string{"poke tesla"},
		recv: []string{"PRIVMSG nick :tesla is Nikola Tesla <tesla@example.com> <mobile:+11> <mobile:+22> <home:+33> <voip:+44> <skype:+55>"},
	}, {
		send: []string{"poke nikola tesla"},
		recv: []string{"PRIVMSG nick :tesla is Nikola Tesla <tesla@example.com> <mobile:+11> <mobile:+22> <home:+33> <voip:+44> <skype:+55>"},
	}, {
		send: []string{"poke euler"},
		recv: []string{"PRIVMSG nick :euler is Leonhard Euler <euler@example.com>"},
	}, {
		send: []string{"poke eu"},
		recv: []string{"PRIVMSG nick :euler is Leonhard Euler, euclid is Euclid of Alexandria"},
	}, {
		send: []string{"poke e"},
		recv: []string{"PRIVMSG nick :tesla is Nikola Tesla, euler is Leonhard Euler, euclid is Euclid of Alexandria, riemann is Bernhard Riemann, einstein is Albert Einstein, newton is Isaac Newton, galileo is Galileo Galilei, plus 2 more people."},
	}, {
		send: []string{"poke riémann"},
		recv: []string{"PRIVMSG nick :riemann is Bernhard Riemann <riemann@example.com>"},
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
	ldapResult("galileo", "Galileo Galilei"),
	ldapResult("jonvon", "Jon von Neumann"),
	ldapResult("gauss", "Carl Friedrich Gauss"),
}

var ldapResults = map[string][]ldap.Result{
	"(|(mozillaNickname=tesla)(cn=*tesla*))":               {ldapEntries[0]},
	"(|(mozillaNickname=nikola tesla)(cn=*nikola tesla*))": {ldapEntries[0]},
	"(|(mozillaNickname=euler)(cn=*euler*))":               {ldapEntries[1]},
	"(|(mozillaNickname=eu)(cn=*eu*))":                     {ldapEntries[1], ldapEntries[2]},
	"(|(mozillaNickname=e)(cn=*e*))":                       ldapEntries,
	`(|(mozillaNickname=ri\c3\a9mann)(cn=*ri\c3\a9mann*))`: {ldapEntries[3]},
}

type ldapConn struct{}

func (l ldapConn) Search(s *ldap.Search) ([]ldap.Result, error) {
	if strings.Contains(s.Filter, "=slow)") {
		time.Sleep(150 * time.Millisecond)
	}
	return ldapResults[s.Filter], nil
}

func (l ldapConn) Close() error { return nil }

func (s *LDAPSuite) SetUpSuite(c *C) {
	mup.SetLogger(c)
	mup.SetDebug(true)
}

func (s *LDAPSuite) TearDownSuite(c *C) {
	mup.SetLogger(nil)
	mup.SetDebug(false)
}

func (s *LDAPSuite) TestLDAP(c *C) {
	for i, test := range pokeTests {
		c.Logf("Starting test %d with messages: %v", i, test.send)
		tester := mup.NewPluginTester("poke")
		if test.config == nil {
			test.config = bson.M{}
		}
		if test.config["ldap"] == nil {
			test.config["ldap"] = "test"
		}
		tester.SetConfig(test.config)
		tester.SetLDAP("test", ldapConn{})
		tester.Start()
		tester.SendAll(test.target, test.send)
		c.Assert(tester.Stop(), IsNil)
		c.Assert(tester.RecvAll(), DeepEquals, test.recv)
	}
}
