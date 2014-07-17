package mup

import (
	. "gopkg.in/check.v1"
	"gopkg.in/mgo.v2/bson"
)

var _ = Suite(&PluggerSuite{})

type PluggerSuite struct {
	sent []string
}

func (s *PluggerSuite) SetUpTest(c *C) {
	SetLogger(c)
	SetDebug(true)
}

func (s *PluggerSuite) TearDownTest(c *C) {
	SetLogger(nil)
	SetDebug(false)
}

func (s *PluggerSuite) plugger(config, targets interface{}) *Plugger {
	s.sent = nil
	send := func(msg *Message) error {
		s.sent = append(s.sent, "["+msg.Account+"] "+msg.String())
		return nil
	}
	p := newPlugger("plugin:name", send)
	p.setConfig(marshalRaw(config))
	p.setTargets(marshalRaw(targets))
	return p
}

func (s *PluggerSuite) TestName(c *C) {
	p := s.plugger(nil, nil)
	c.Assert(p.Name(), Equals, "plugin:name")
}

func (s *PluggerSuite) TestLogf(c *C) {
	p := s.plugger(nil, nil)
	p.Logf("<%s>", "text")
	c.Assert(c.GetTestLog(), Matches, `(?m).*\[plugin:name\] <text>.*`)
}

func (s *PluggerSuite) TestDebugf(c *C) {
	p := s.plugger(nil, nil)
	SetDebug(false)
	p.Debugf("<%s>", "one")
	SetDebug(true)
	p.Debugf("<%s>", "two")
	c.Assert(c.GetTestLog(), Matches, `(?m).*\[plugin:name\] <two>.*`)
	c.Assert(c.GetTestLog(), Not(Matches), `(?m).*\[plugin:name\] <one>.*`)
}

func (s *PluggerSuite) TestSendfPrivate(c *C) {
	p := s.plugger(nil, nil)
	msg := ParseIncoming("origin", "mup", "!", ":nick!~user@host PRIVMSG mup :query")
	p.Sendf(msg, "<%s>", "reply")
	c.Assert(s.sent, DeepEquals, []string{"[origin] PRIVMSG nick :<reply>"})
}

func (s *PluggerSuite) TestSendfChannel(c *C) {
	p := s.plugger(nil, nil)
	msg := ParseIncoming("origin", "mup", "!", ":nick!~user@host PRIVMSG #channel :mup: query")
	p.Sendf(msg, "<%s>", "reply")
	c.Assert(s.sent, DeepEquals, []string{"[origin] PRIVMSG #channel :nick: <reply>"})
}

func (s *PluggerSuite) TestSendfNoNick(c *C) {
	p := s.plugger(nil, nil)
	msg := ParseIncoming("origin", "mup", "!", "PRIVMSG #channel :mup: query")
	p.Sendf(msg, "<%s>", "reply")
	c.Assert(s.sent, DeepEquals, []string{"[origin] PRIVMSG #channel :<reply>"})
}

func (s *PluggerSuite) TestSend(c *C) {
	p := s.plugger(nil, nil)
	msg := &Message{Account: "myaccount", Command: "TEST", Params: []string{"some", "params"}}
	p.Send(msg)
	c.Assert(s.sent, DeepEquals, []string{"[myaccount] TEST some params"})
}

func (s *PluggerSuite) TestDirectfPrivate(c *C) {
	p := s.plugger(nil, nil)
	msg := ParseIncoming("origin", "mup", "!", ":nick!~user@host PRIVMSG mup :query")
	p.Directf(msg, "<%s>", "reply")
	c.Assert(s.sent, DeepEquals, []string{"[origin] PRIVMSG nick :<reply>"})
}

func (s *PluggerSuite) TestDirectfChannel(c *C) {
	p := s.plugger(nil, nil)
	msg := ParseIncoming("origin", "mup", "!", ":nick!~user@host PRIVMSG #channel :mup: query")
	p.Directf(msg, "<%s>", "reply")
	c.Assert(s.sent, DeepEquals, []string{"[origin] PRIVMSG nick :<reply>"})
}

func (s *PluggerSuite) TestChannelfPrivate(c *C) {
	p := s.plugger(nil, nil)
	msg := ParseIncoming("origin", "mup", "!", ":nick!~user@host PRIVMSG mup :query")
	p.Channelf(msg, "<%s>", "reply")
	c.Assert(s.sent, DeepEquals, []string{"[origin] PRIVMSG nick :<reply>"})
}

func (s *PluggerSuite) TestChannelfChannel(c *C) {
	p := s.plugger(nil, nil)
	msg := ParseIncoming("origin", "mup", "!", ":nick!~user@host PRIVMSG #channel :mup: query")
	p.Channelf(msg, "<%s>", "reply")
	c.Assert(s.sent, DeepEquals, []string{"[origin] PRIVMSG #channel :<reply>"})
}

func (s *PluggerSuite) TestConfig(c *C) {
	p := s.plugger(bson.M{"key": "value"}, nil)
	var config struct{ Key string }
	p.Config(&config)
	c.Assert(config.Key, Equals, "value")
}

func (s *PluggerSuite) TestTargets(c *C) {
	p := s.plugger(nil, []bson.M{
		{"account": "one", "channel": "#chan"},
		{"account": "two", "nick": "nick"},
		{"account": "three", "channel": "#other", "nick": "nick"},
		{"account": "four"},
	})
	targets := p.Targets()
	c.Assert(targets[0].Address(), Equals, Address{Account: "one", Channel: "#chan"})
	c.Assert(targets[1].Address(), Equals, Address{Account: "two", Nick: "nick"})
	c.Assert(targets[2].Address(), Equals, Address{Account: "three", Channel: "#other", Nick: "nick"})
	c.Assert(targets[3].Address(), Equals, Address{Account: "four"})
	c.Assert(targets, HasLen, 4)

	c.Assert(p.Target(&Message{Account: "one", Channel: "#chan"}), Equals, &targets[0])
	c.Assert(p.Target(&Message{Account: "two", Nick: "nick"}), Equals, &targets[1])
	c.Assert(p.Target(&Message{Account: "three", Channel: "#other", Nick: "nick"}), Equals, &targets[2])
	c.Assert(p.Target(&Message{Account: "four", Nick: "nick"}), Equals, &targets[3])
	c.Assert(p.Target(&Message{Account: "four", Channel: "#chan"}), Equals, &targets[3])
	c.Assert(p.Target(&Message{Account: "one", Nick: "nick"}), IsNil)
	c.Assert(p.Target(&Message{Account: "two", Channel: "#chan"}), IsNil)
	c.Assert(p.Target(&Message{Account: "three", Channel: "#other"}), IsNil)
	c.Assert(p.Target(&Message{Account: "three", Nick: "nick"}), IsNil)

	c.Assert(targets[0].CanSend(), Equals, true)
	c.Assert(targets[1].CanSend(), Equals, true)
	c.Assert(targets[2].CanSend(), Equals, true)
	c.Assert(targets[3].CanSend(), Equals, false)
}

func (s *PluggerSuite) TestBroadcastf(c *C) {
	p := s.plugger(nil, []bson.M{{"account": "one", "channel": "#chan"}, {"account": "two", "nick": "nick"}})
	p.Broadcastf("<%s>", "text")
	c.Assert(s.sent, DeepEquals, []string{"[one] PRIVMSG #chan :<text>", "[two] PRIVMSG nick :<text>"})
}

func (s *PluggerSuite) TestBroadcast(c *C) {
	p := s.plugger(nil, []bson.M{{"account": "one", "channel": "#chan"}, {"account": "two", "nick": "nick"}})
	p.Broadcast(&Message{Command: "PRIVMSG", Text: "<text>"})
	c.Assert(s.sent, DeepEquals, []string{"[one] PRIVMSG #chan :<text>", "[two] PRIVMSG nick :<text>"})
	s.sent = nil
	p.Broadcast(&Message{Command: "TEST", Params: []string{"some", "params"}})
	c.Assert(s.sent, DeepEquals, []string{"[one] TEST some params", "[two] TEST some params"})
}
