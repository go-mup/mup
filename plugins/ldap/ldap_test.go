package mup_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"gopkg.in/mup.v0"
	"gopkg.in/mup.v0/ldap"
	_ "gopkg.in/mup.v0/plugins/ldap"

	. "gopkg.in/check.v1"
)

func Test(t *testing.T) { TestingT(t) }

var _ = Suite(&LDAPSuite{})

type LDAPSuite struct{}

var ldapTests = []struct {
	send   []string
	recv   []string
	config mup.Map
}{
	{
		send: []string{"poke notfound"},
		recv: []string{"PRIVMSG nick :Cannot find anyone matching this. :-("},
	}, {
		send:   []string{"poke noldap"},
		recv:   []string{`PRIVMSG nick :Plugin configuration error: LDAP connection "unknown" not found.`},
		config: mup.Map{"ldap": "unknown"},
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
	}, {
		send: []string{"poke +11"},
		recv: []string{"PRIVMSG nick :tesla is Nikola Tesla <tesla@example.com> <mobile:+11> <mobile:+22> <home:+33> <voip:+44> <skype:+55>"},
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

var johnNash = ldap.Result{
	Attrs: []ldap.Attr{
		{Name: "mozillaNickname", Values: []string{"jnash"}},
		{Name: "cn", Values: []string{"John Nash"}},
		{Name: "mail", Values: []string{"nash@example.com"}},
		{Name: "mozillaCustom4", Values: []string{"-0400"}},
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
	"(|(mozillaNickname=jnash)(cn=*jnash*))":               {johnNash},

	"(|(telephoneNumber=*+11*)(mobile=*+11*)(homePhone=*+11*)(voidPhone=*+11*)(skypePhone=*+11*))": {ldapEntries[0]},
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
	for i, test := range ldapTests {
		c.Logf("Starting test %d with messages: %v", i, test.send)
		tester := mup.NewPluginTester("ldap")
		if test.config == nil {
			test.config = mup.Map{}
		}
		if test.config["ldap"] == nil {
			test.config["ldap"] = "test"
		}
		tester.SetConfig(test.config)
		tester.SetLDAP("test", ldapConn{})
		tester.Start()
		tester.SendAll(test.send)
		c.Assert(tester.Stop(), IsNil)
		c.Assert(tester.RecvAll(), DeepEquals, test.recv)
	}
}

func (s *LDAPSuite) TestTimeFormat(c *C) {
	tester := mup.NewPluginTester("ldap")
	tester.SetConfig(mup.Map{"ldap": "test"})
	tester.SetLDAP("test", ldapConn{})
	tester.Start()
	tester.Sendf("poke jnash")
	c.Assert(tester.Stop(), IsNil)
	line := tester.Recv()

	t1 := time.Now().UTC().Add(-4 * time.Hour)
	t2 := t1.Add(-1 * time.Minute)
	f1 := fmt.Sprintf("<time:%s-0400>", t1.Format("15h04"))
	f2 := fmt.Sprintf("<time:%s-0400>", t2.Format("15h04"))
	if !strings.Contains(line, f1) && !strings.Contains(line, f2) {
		c.Fatalf("Reply should contain either %q or %q: %s", f2, f1, line)
	}
}
