package mup_test

import (
	"fmt"

	. "gopkg.in/check.v1"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
	"gopkg.in/mup.v0"
	"gopkg.in/mup.v0/ldap"
	"strings"
)

var _ = Suite(&PluggerSuite{})

type PluggerSuite struct {
	dbserver mup.DBServerHelper
	sent     []string
	ldap     map[string]ldap.Conn
}

func (s *PluggerSuite) SetUpSuite(c *C) {
	s.dbserver.SetPath(c.MkDir())
}

func (s *PluggerSuite) TearDownSuite(c *C) {
	s.dbserver.Stop()
}

func (s *PluggerSuite) SetUpTest(c *C) {
	mup.SetLogger(c)
	mup.SetDebug(true)
}

func (s *PluggerSuite) TearDownTest(c *C) {
	s.dbserver.Reset()
	s.dbserver.AssertClosed()
	mup.SetLogger(nil)
	mup.SetDebug(false)
}

func (s *PluggerSuite) plugger(db *mgo.Database, config, targets interface{}) *mup.Plugger {
	s.sent = nil
	s.ldap = make(map[string]ldap.Conn)
	send := func(msg *mup.Message) error {
		s.sent = append(s.sent, "["+msg.Account+"] "+msg.String())
		return nil
	}
	ldap := func(name string) (ldap.Conn, error) {
		if conn, ok := s.ldap[name]; ok {
			return conn, nil
		}
		return nil, fmt.Errorf("test suite has no %q LDAP connection", name)
	}
	return mup.NewPlugger("theplugin/label", db, send, ldap, config, targets)
}

func (s *PluggerSuite) TestName(c *C) {
	p := s.plugger(nil, nil, nil)
	c.Assert(p.Name(), Equals, "theplugin/label")
}

func (s *PluggerSuite) TestLogf(c *C) {
	p := s.plugger(nil, nil, nil)
	p.Logf("<%s>", "text")
	c.Assert(c.GetTestLog(), Matches, `(?m).*\[theplugin/label\] <text>.*`)
}

func (s *PluggerSuite) TestDebugf(c *C) {
	p := s.plugger(nil, nil, nil)
	mup.SetDebug(false)
	p.Debugf("<%s>", "one")
	mup.SetDebug(true)
	p.Debugf("<%s>", "two")
	c.Assert(c.GetTestLog(), Matches, `(?m).*\[theplugin/label\] <two>.*`)
	c.Assert(c.GetTestLog(), Not(Matches), `(?m).*\[theplugin/label\] <one>.*`)
}

func (s *PluggerSuite) TestCollection(c *C) {
	master := s.dbserver.Session()
	defer master.Close()
	p := s.plugger(master.DB("mup"), nil, nil)
	session, coll := p.Collection("thecoll")
	defer session.Close()
	c.Assert(coll.Name, Equals, "plugin.theplugin_label.thecoll")
	c.Assert(coll.Database.Session, Equals, session)
	c.Assert(master, Not(Equals), session)
}

func (s *PluggerSuite) TestSharedCollection(c *C) {
	master := s.dbserver.Session()
	defer master.Close()
	p := s.plugger(master.DB("mup"), nil, nil)
	session, coll := p.SharedCollection("thecoll")
	defer session.Close()
	c.Assert(coll.Name, Equals, "shared.thecoll")
	c.Assert(coll.Database.Session, Equals, session)
	c.Assert(master, Not(Equals), session)
}

func (s *PluggerSuite) TestSendfPrivate(c *C) {
	p := s.plugger(nil, nil, nil)
	msg := mup.ParseIncoming("origin", "mup", "!", ":nick!~user@host PRIVMSG mup :query")
	p.Sendf(msg, "<%s>", "reply")
	c.Assert(s.sent, DeepEquals, []string{"[origin] PRIVMSG nick :<reply>"})
}

func (s *PluggerSuite) TestSendfChannel(c *C) {
	p := s.plugger(nil, nil, nil)
	msg := mup.ParseIncoming("origin", "mup", "!", ":nick!~user@host PRIVMSG #channel :mup: query")
	p.Sendf(msg, "<%s>", "reply")
	c.Assert(s.sent, DeepEquals, []string{"[origin] PRIVMSG #channel :nick: <reply>"})
}

func (s *PluggerSuite) TestSendfNoNick(c *C) {
	p := s.plugger(nil, nil, nil)
	msg := mup.ParseIncoming("origin", "mup", "!", "PRIVMSG #channel :mup: query")
	p.Sendf(msg, "<%s>", "reply")
	c.Assert(s.sent, DeepEquals, []string{"[origin] PRIVMSG #channel :<reply>"})
}

func (s *PluggerSuite) TestSend(c *C) {
	p := s.plugger(nil, nil, nil)
	msg := &mup.Message{Account: "myaccount", Command: "TEST", Params: []string{"some", "params"}}
	p.Send(msg)
	c.Assert(s.sent, DeepEquals, []string{"[myaccount] TEST some params"})
}

func (s *PluggerSuite) TestDirectfPrivate(c *C) {
	p := s.plugger(nil, nil, nil)
	msg := mup.ParseIncoming("origin", "mup", "!", ":nick!~user@host PRIVMSG mup :query")
	p.Directf(msg, "<%s>", "reply")
	c.Assert(s.sent, DeepEquals, []string{"[origin] PRIVMSG nick :<reply>"})
}

func (s *PluggerSuite) TestDirectfChannel(c *C) {
	p := s.plugger(nil, nil, nil)
	msg := mup.ParseIncoming("origin", "mup", "!", ":nick!~user@host PRIVMSG #channel :mup: query")
	p.Directf(msg, "<%s>", "reply")
	c.Assert(s.sent, DeepEquals, []string{"[origin] PRIVMSG nick :<reply>"})
}

func (s *PluggerSuite) TestChannelfPrivate(c *C) {
	p := s.plugger(nil, nil, nil)
	msg := mup.ParseIncoming("origin", "mup", "!", ":nick!~user@host PRIVMSG mup :query")
	p.Channelf(msg, "<%s>", "reply")
	c.Assert(s.sent, DeepEquals, []string{"[origin] PRIVMSG nick :<reply>"})
}

func (s *PluggerSuite) TestChannelfChannel(c *C) {
	p := s.plugger(nil, nil, nil)
	msg := mup.ParseIncoming("origin", "mup", "!", ":nick!~user@host PRIVMSG #channel :mup: query")
	p.Channelf(msg, "<%s>", "reply")
	c.Assert(s.sent, DeepEquals, []string{"[origin] PRIVMSG #channel :<reply>"})
}

func (s *PluggerSuite) TestConfig(c *C) {
	p := s.plugger(nil, bson.M{"key": "value"}, nil)
	var config struct{ Key string }
	p.Config(&config)
	c.Assert(config.Key, Equals, "value")
}

func (s *PluggerSuite) TestTargets(c *C) {
	p := s.plugger(nil, nil, []bson.M{
		{"account": "one", "channel": "#chan"},
		{"account": "two", "nick": "nick"},
		{"account": "three", "channel": "#other", "nick": "nick"},
		{"account": "four"},
	})
	targets := p.Targets()
	c.Assert(targets[0].Address(), Equals, mup.Address{Account: "one", Channel: "#chan"})
	c.Assert(targets[1].Address(), Equals, mup.Address{Account: "two", Nick: "nick"})
	c.Assert(targets[2].Address(), Equals, mup.Address{Account: "three", Channel: "#other", Nick: "nick"})
	c.Assert(targets[3].Address(), Equals, mup.Address{Account: "four"})
	c.Assert(targets, HasLen, 4)

	c.Assert(p.Target(&mup.Message{Account: "one", Channel: "#chan"}), Equals, &targets[0])
	c.Assert(p.Target(&mup.Message{Account: "two", Nick: "nick"}), Equals, &targets[1])
	c.Assert(p.Target(&mup.Message{Account: "three", Channel: "#other", Nick: "nick"}), Equals, &targets[2])
	c.Assert(p.Target(&mup.Message{Account: "four", Nick: "nick"}), Equals, &targets[3])
	c.Assert(p.Target(&mup.Message{Account: "four", Channel: "#chan"}), Equals, &targets[3])
	c.Assert(p.Target(&mup.Message{Account: "one", Nick: "nick"}), IsNil)
	c.Assert(p.Target(&mup.Message{Account: "two", Channel: "#chan"}), IsNil)
	c.Assert(p.Target(&mup.Message{Account: "three", Channel: "#other"}), IsNil)
	c.Assert(p.Target(&mup.Message{Account: "three", Nick: "nick"}), IsNil)

	c.Assert(targets[0].CanSend(), Equals, true)
	c.Assert(targets[1].CanSend(), Equals, true)
	c.Assert(targets[2].CanSend(), Equals, true)
	c.Assert(targets[3].CanSend(), Equals, false)
}

func (s *PluggerSuite) TestBroadcastf(c *C) {
	p := s.plugger(nil, nil, []bson.M{{"account": "one", "channel": "#chan"}, {"account": "two", "nick": "nick"}})
	p.Broadcastf("<%s>", "text")
	c.Assert(s.sent, DeepEquals, []string{"[one] PRIVMSG #chan :<text>", "[two] PRIVMSG nick :<text>"})
}

func (s *PluggerSuite) TestBroadcast(c *C) {
	p := s.plugger(nil, nil, []bson.M{{"account": "one", "channel": "#chan"}, {"account": "two", "nick": "nick"}})
	p.Broadcast(&mup.Message{Command: "PRIVMSG", Text: "<text>"})
	c.Assert(s.sent, DeepEquals, []string{"[one] PRIVMSG #chan :<text>", "[two] PRIVMSG nick :<text>"})
	s.sent = nil
	p.Broadcast(&mup.Message{Command: "TEST", Params: []string{"some", "params"}})
	c.Assert(s.sent, DeepEquals, []string{"[one] TEST some params", "[two] TEST some params"})
}

func (s *PluggerSuite) TestLDAP(c *C) {
	p := s.plugger(nil, nil, nil)
	conn := &ldapConn{}
	s.ldap["test"] = conn
	res, err := p.LDAP("test")
	c.Assert(err, IsNil)
	c.Assert(res, Equals, conn)
	_, err = p.LDAP("unknown")
	c.Assert(err, ErrorMatches, `test suite has no "unknown" LDAP connection`)
}

type ldapConn struct{}

func (c *ldapConn) Close() error { return nil }

func (c *ldapConn) Search(s *ldap.Search) ([]ldap.Result, error) {
	return []ldap.Result{{DN: "test-dn"}}, nil
}

var lineBreakTests = []struct{
	text string
	sent []string
}{{
	text: strings.Repeat("123456789 ", 60),
	sent: []string{
		"[one] PRIVMSG nick :" + strings.Repeat("123456789 ", 30)[:299],
		"[one] PRIVMSG nick :" + strings.Repeat("123456789 ", 30)[:299],
	},
}, {
	text: strings.Repeat("123456789 ", 30) + "A",
	sent: []string{
		"[one] PRIVMSG nick :" + strings.Repeat("123456789 ", 16)[:159],
		"[one] PRIVMSG nick :" + strings.Repeat("123456789 ", 14) + "A",
	},
}, {
	text: "A" + strings.Repeat("1234567890", 30),
	sent: []string{
		"[one] PRIVMSG nick :A" + strings.Repeat("1234567890", 15),
		"[one] PRIVMSG nick :" + strings.Repeat("1234567890", 15),
	},
}, {
	text: strings.Repeat("123456789 ", 30) + "          ",
	sent: []string{
		"[one] PRIVMSG nick :" + strings.Repeat("123456789 ", 30)[:299],
	},
}}

func (s *PluggerSuite) TestTextLineBreak(c *C) {
	p := s.plugger(nil, nil, nil)
	for _, test := range lineBreakTests {
		err := p.Send(&mup.Message{Account: "one", Nick: "nick", Text: test.text})
		c.Assert(err, IsNil)
		c.Assert(s.sent, DeepEquals, test.sent)
		s.sent = nil
	}
}
