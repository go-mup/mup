package mup_test

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	. "gopkg.in/check.v1"
	"gopkg.in/mup.v0"
	"gopkg.in/mup.v0/ldap"
)

var _ = Suite(&PluggerSuite{})

type PluggerSuite struct {
	sent    []string
	msgs    []*mup.Message
	handled []string
	ldap    map[string]ldap.Conn

	dbdir string
	db    *sql.DB
}

func (s *PluggerSuite) SetUpSuite(c *C) {
	s.dbdir = c.MkDir()
}

func (s *PluggerSuite) SetUpTest(c *C) {
	mup.SetLogger(c)
	mup.SetDebug(true)

	var err error
	s.db, err = mup.OpenDB(s.dbdir)
	c.Assert(err, IsNil)
}

func (s *PluggerSuite) TearDownTest(c *C) {
	mup.SetLogger(nil)
	mup.SetDebug(false)

	s.db.Close()
	s.db = nil
	//c.Assert(mup.WipeDB(s.dbdir), IsNil)
	s.dbdir = c.MkDir()
}

func (s *PluggerSuite) plugger(db *sql.DB, config map[string]interface{}, targets []mup.Target) *mup.Plugger {
	s.sent = nil
	s.msgs = nil
	s.ldap = make(map[string]ldap.Conn)
	send := func(msg *mup.Message) error {
		s.sent = append(s.sent, "[@"+msg.Account+"] "+msg.String())
		s.msgs = append(s.msgs, msg)
		return nil
	}
	handle := func(msg *mup.Message) error {
		s.handled = append(s.handled, "[@"+msg.Account+"] "+msg.String())
		return nil
	}
	ldap := func(name string) (ldap.Conn, error) {
		if conn, ok := s.ldap[name]; ok {
			return conn, nil
		}
		return nil, fmt.Errorf("test suite has no %q LDAP connection", name)
	}
	return mup.NewPlugger("theplugin/label", db, send, handle, ldap, config, targets)
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

func (s *PluggerSuite) TestDB(c *C) {
	p := s.plugger(s.db, nil, nil)
	c.Assert(p.DB(), Equals, s.db)
}

func (s *PluggerSuite) TestHandle(c *C) {
	p := s.plugger(nil, nil, []mup.Target{
		{Account: "one", Channel: "#chan"},
		{Account: "two", Nick: "nick"},
		{Account: ""},
	})

	err := p.Handle(mup.ParseIncoming("one", "mup", "!", ":nick!~user@host PRIVMSG #other :text"))
	c.Assert(err, IsNil)
	err = p.Handle(mup.ParseIncoming("two", "mup", "!", ":other!~user@host PRIVMSG mup :text"))
	c.Assert(err, IsNil)
	err = p.Handle(mup.ParseIncoming("one", "mup", "!", ":nick!~user@host PRIVMSG #chan :text"))
	c.Assert(err, IsNil)
	err = p.Handle(mup.ParseIncoming("two", "mup", "!", ":nick!~user@host PRIVMSG mup :text"))
	c.Assert(err, IsNil)

	c.Assert(s.handled[0], Equals, "[@one] :nick!~user@host PRIVMSG #chan :text")
	c.Assert(s.handled[1], Equals, "[@two] :nick!~user@host PRIVMSG mup :text")
	c.Assert(s.handled, HasLen, 2)
}

func (s *PluggerSuite) TestSendfPrivate(c *C) {
	p := s.plugger(nil, nil, nil)
	msg := mup.ParseIncoming("origin", "mup", "!", ":nick!~user@host PRIVMSG mup :query")
	p.Sendf(msg, "<%s>", "reply")
	c.Assert(s.sent, DeepEquals, []string{"[@origin] PRIVMSG nick :<reply>"})
}

func (s *PluggerSuite) TestSendfChannel(c *C) {
	p := s.plugger(nil, nil, nil)
	msg := mup.ParseIncoming("origin", "mup", "!", ":nick!~user@host PRIVMSG #channel :mup: query")
	p.Sendf(msg, "<%s>", "reply")
	c.Assert(s.sent, DeepEquals, []string{"[@origin] PRIVMSG #channel :nick: <reply>"})
}

func (s *PluggerSuite) TestSendfChannelTelegram(c *C) {
	p := s.plugger(nil, nil, nil)
	msg := mup.ParseIncoming("origin", "mup", "!", ":nick!~user@telegram PRIVMSG #channel :mup: query")
	p.Sendf(msg, "<%s>", "reply")
	c.Assert(s.sent, DeepEquals, []string{"[@origin] PRIVMSG #channel :@nick <reply>"})
}

func (s *PluggerSuite) TestSendfNoNick(c *C) {
	p := s.plugger(nil, nil, nil)
	msg := mup.ParseIncoming("origin", "mup", "!", "PRIVMSG #channel :mup: query")
	p.Sendf(msg, "<%s>", "reply")
	c.Assert(s.sent, DeepEquals, []string{"[@origin] PRIVMSG #channel :<reply>"})
}

func (s *PluggerSuite) TestSendfUserChannel(c *C) {
	p := s.plugger(nil, nil, nil)
	msg := mup.ParseIncoming("origin", "mup", "!", ":nick!~user@host PRIVMSG @user:123 :mup: query")
	p.Sendf(msg, "<%s>", "reply")
	c.Assert(s.sent, DeepEquals, []string{"[@origin] PRIVMSG @user:123 :<reply>"})
}

func (s *PluggerSuite) TestSend(c *C) {
	p := s.plugger(nil, nil, nil)
	msg := &mup.Message{Account: "myaccount", Command: "TEST", Params: []string{"some", "params"}}
	before := time.Now()
	p.Send(msg)
	after := time.Now()
	c.Assert(s.msgs, HasLen, 1)
	sent := s.msgs[0]
	c.Assert(sent.Time.After(before), Equals, true)
	c.Assert(sent.Time.Before(after), Equals, true)
	c.Assert(msg.Time.IsZero(), Equals, true)
	sent.Time = time.Time{}
	c.Assert(sent, DeepEquals, msg)
}

func (s *PluggerSuite) TestDirectfPrivate(c *C) {
	p := s.plugger(nil, nil, nil)
	msg := mup.ParseIncoming("origin", "mup", "!", ":nick!~user@host PRIVMSG mup :query")
	p.SendDirectf(msg, "<%s>", "reply")
	c.Assert(s.sent, DeepEquals, []string{"[@origin] PRIVMSG nick :<reply>"})
}

func (s *PluggerSuite) TestDirectfChannel(c *C) {
	p := s.plugger(nil, nil, nil)
	msg := mup.ParseIncoming("origin", "mup", "!", ":nick!~user@host PRIVMSG #channel :mup: query")
	p.SendDirectf(msg, "<%s>", "reply")
	c.Assert(s.sent, DeepEquals, []string{"[@origin] PRIVMSG nick :<reply>"})
}

func (s *PluggerSuite) TestChannelfPrivate(c *C) {
	p := s.plugger(nil, nil, nil)
	msg := mup.ParseIncoming("origin", "mup", "!", ":nick!~user@host PRIVMSG mup :query")
	p.SendChannelf(msg, "<%s>", "reply")
	c.Assert(s.sent, DeepEquals, []string{"[@origin] PRIVMSG nick :<reply>"})
}

func (s *PluggerSuite) TestChannelfChannel(c *C) {
	p := s.plugger(nil, nil, nil)
	msg := mup.ParseIncoming("origin", "mup", "!", ":nick!~user@host PRIVMSG #channel :mup: query")
	p.SendChannelf(msg, "<%s>", "reply")
	c.Assert(s.sent, DeepEquals, []string{"[@origin] PRIVMSG #channel :<reply>"})
}

func (s *PluggerSuite) TestUnmarshalConfig(c *C) {
	p := s.plugger(nil, mup.Map{"key": "value"}, nil)
	var config struct{ Key string }
	err := p.UnmarshalConfig(&config)
	c.Assert(err, IsNil)
	c.Assert(config.Key, Equals, "value")
}

func (s *PluggerSuite) TestTargets(c *C) {
	p := s.plugger(nil, nil, []mup.Target{
		{Account: "one", Channel: "#chan"},
		{Account: "two", Nick: "nick"},
		{Account: "three", Channel: "#other", Nick: "nick"},
		{Account: "four"},
		{Channel: "#other"},
		{Account: ""},
	})
	targets := p.Targets()
	c.Assert(targets[0].Address(), Equals, mup.Address{Account: "one", Channel: "#chan"})
	c.Assert(targets[1].Address(), Equals, mup.Address{Account: "two", Nick: "nick"})
	c.Assert(targets[2].Address(), Equals, mup.Address{Account: "three", Channel: "#other", Nick: "nick"})
	c.Assert(targets[3].Address(), Equals, mup.Address{Account: "four"})
	c.Assert(targets[4].Address(), Equals, mup.Address{Channel: "#other"})
	c.Assert(targets[5].Address(), Equals, mup.Address{})
	c.Assert(targets, HasLen, 6)

	c.Assert(p.Target(&mup.Message{Account: "one", Channel: "#chan"}), Equals, targets[0])
	c.Assert(p.Target(&mup.Message{Account: "two", Nick: "nick"}), Equals, targets[1])
	c.Assert(p.Target(&mup.Message{Account: "three", Channel: "#other", Nick: "nick"}), Equals, targets[2])
	c.Assert(p.Target(&mup.Message{Account: "four", Nick: "nick"}), Equals, targets[3])
	c.Assert(p.Target(&mup.Message{Account: "four", Channel: "#chan"}), Equals, targets[3])
	c.Assert(p.Target(&mup.Message{Account: "one", Nick: "nick"}), Equals, targets[5])
	c.Assert(p.Target(&mup.Message{Account: "two", Channel: "#chan"}), Equals, targets[5])
	c.Assert(p.Target(&mup.Message{Account: "three", Channel: "#other"}), Equals, targets[4])
	c.Assert(p.Target(&mup.Message{Account: "three", Nick: "nick"}), Equals, targets[5])

	c.Assert(targets[0].CanSend(), Equals, true)
	c.Assert(targets[1].CanSend(), Equals, true)
	c.Assert(targets[2].CanSend(), Equals, true)
	c.Assert(targets[3].CanSend(), Equals, false)
	c.Assert(targets[4].CanSend(), Equals, false)
	c.Assert(targets[5].CanSend(), Equals, false)
}

func (s *PluggerSuite) TestBroadcastf(c *C) {
	p := s.plugger(nil, nil, []mup.Target{
		{Account: "one", Channel: "#chan"},
		{Account: "two", Nick: "nick"},
		{Account: "two", Channel: "#chan", Nick: "nick"},
		{Account: "two", Channel: "@user:123", Nick: "nick"},
	})
	p.Broadcastf("<%s>", "text")
	c.Assert(s.sent, DeepEquals, []string{
		"[@one] PRIVMSG #chan :<text>",
		"[@two] PRIVMSG nick :<text>",
		"[@two] PRIVMSG #chan :nick: <text>",
		"[@two] PRIVMSG @user:123 :<text>",
	})
}

func (s *PluggerSuite) TestBroadcast(c *C) {
	p := s.plugger(nil, nil, []mup.Target{
		{Account: "one", Channel: "#chan"},
		{Account: "two", Nick: "nick"},
	})
	p.Broadcast(&mup.Message{Command: "PRIVMSG", Text: "<text>"})
	c.Assert(s.sent, DeepEquals, []string{"[@one] PRIVMSG #chan :<text>", "[@two] PRIVMSG nick :<text>"})
	s.sent = nil
	p.Broadcast(&mup.Message{Command: "TEST", Params: []string{"some", "params"}})
	c.Assert(s.sent, DeepEquals, []string{"[@one] TEST some params", "[@two] TEST some params"})
}

func (s *PluggerSuite) TestMoniker(c *C) {
	execSQL(c, s.db,
		`INSERT INTO account (name) VALUES ('one')`,
		`INSERT INTO moniker (account,channel,nick,name) VALUES ('one','','nick','moniker')`,
		`INSERT INTO moniker (account,channel,nick,name) VALUES ('one','#channel2','nick','moniker2')`,
	)

	p := s.plugger(s.db, nil, nil)
	msg := mup.ParseIncoming("one", "mup", "!", ":nick!~user@host PRIVMSG #channel :mup: query")
	p.Sendf(msg, "<%s>", "reply")
	msg = mup.ParseIncoming("one", "mup", "!", ":nick!~user@host PRIVMSG #channel2 :mup: query")
	p.Sendf(msg, "<%s>", "reply")

	msg = mup.ParseIncoming("two", "mup", "!", ":nick!~user@host PRIVMSG #channel :mup: query")
	p.Sendf(msg, "<%s>", "reply")
	msg = mup.ParseIncoming("two", "mup", "!", ":nick!~user@host PRIVMSG #channel2 :mup: query")
	p.Sendf(msg, "<%s>", "reply")

	msg = mup.ParseIncoming("one", "mup", "!", ":nick2!~user@host PRIVMSG #channel :mup: query")
	p.Sendf(msg, "<%s>", "reply")
	msg = mup.ParseIncoming("one", "mup", "!", ":nick2!~user@host PRIVMSG #channel2 :mup: query")
	p.Sendf(msg, "<%s>", "reply")

	c.Assert(s.sent, DeepEquals, []string{
		"[@one] PRIVMSG #channel :moniker: <reply>",
		"[@one] PRIVMSG #channel2 :moniker2: <reply>",
		"[@two] PRIVMSG #channel :nick: <reply>",
		"[@two] PRIVMSG #channel2 :nick: <reply>",
		"[@one] PRIVMSG #channel :nick2: <reply>",
		"[@one] PRIVMSG #channel2 :nick2: <reply>",
	})
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

var lineBreakTests = []struct {
	text string
	sent []string
}{{
	text: strings.Repeat("123456789 ", 60),
	sent: []string{
		"[@one] PRIVMSG nick :" + strings.Repeat("123456789 ", 30)[:299],
		"[@one] PRIVMSG nick :" + strings.Repeat("123456789 ", 30)[:299],
	},
}, {
	text: strings.Repeat("123456789 ", 30) + "A",
	sent: []string{
		"[@one] PRIVMSG nick :" + strings.Repeat("123456789 ", 16)[:159],
		"[@one] PRIVMSG nick :" + strings.Repeat("123456789 ", 14) + "A",
	},
}, {
	text: "A" + strings.Repeat("1234567890", 30),
	sent: []string{
		"[@one] PRIVMSG nick :A" + strings.Repeat("1234567890", 15),
		"[@one] PRIVMSG nick :" + strings.Repeat("1234567890", 15),
	},
}, {
	text: strings.Repeat("123456789 ", 30) + "          ",
	sent: []string{
		"[@one] PRIVMSG nick :" + strings.Repeat("123456789 ", 30)[:299],
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
