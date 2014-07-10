package mup

import (
	. "gopkg.in/check.v1"
	"labix.org/v2/mgo/bson"
)

var _ = Suite(&PluggerSuite{})

type PluggerSuite struct {
	sent []string
}

func parse(line string) *Message {
	msg := ParseMessage("mup", "!", line)
	msg.Account = "origin"
	return msg
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

func (s *PluggerSuite) TestReplyfPrivate(c *C) {
	p := s.plugger(nil, nil)
	p.Replyf(parse(":nick!~user@host PRIVMSG mup :query"), "<%s>", "reply")
	c.Assert(s.sent, DeepEquals, []string{"[origin] PRIVMSG nick :<reply>"})
}

func (s *PluggerSuite) TestReplyfChannel(c *C) {
	p := s.plugger(nil, nil)
	p.Replyf(parse(":nick!~user@host PRIVMSG #channel :mup: query"), "<%s>", "reply")
	c.Assert(s.sent, DeepEquals, []string{"[origin] PRIVMSG #channel :nick: <reply>"})
}

func (s *PluggerSuite) TestSendf(c *C) {
	p := s.plugger(nil, nil)
	p.Sendf("myaccount", "nick", "<%s>", "text")
	c.Assert(s.sent, DeepEquals, []string{"[myaccount] PRIVMSG nick :<text>"})
}

func (s *PluggerSuite) TestSend(c *C) {
	p := s.plugger(nil, nil)
	msg := &Message{
		Account: "myaccount",
		Cmd:     "TEST",
		Target:  "nick",
		Text:    "query",
	}
	p.Send(msg)
	c.Assert(s.sent, DeepEquals, []string{"[myaccount] TEST nick :query"})
}

func (s *PluggerSuite) TestConfig(c *C) {
	p := s.plugger(bson.M{"key": "value"}, nil)
	var config struct{ Key string }
	p.Config(&config)
	c.Assert(config.Key, Equals, "value")
}

func (s *PluggerSuite) TestTargets(c *C) {
	p := s.plugger(nil, []bson.M{{"account": "one", "target": "#chan"}, {"account": "two", "target": "nick"}, {"account": "three"}})
	targets := p.Targets()
	c.Assert(targets, HasLen, 3)
	c.Assert(targets[0].Account, Equals, "one")
	c.Assert(targets[0].Target, Equals, "#chan")
	c.Assert(targets[1].Account, Equals, "two")
	c.Assert(targets[1].Target, Equals, "nick")
	c.Assert(targets[2].Account, Equals, "three")
	c.Assert(targets[2].Target, Equals, "")

	c.Assert(p.Target(&Message{Account: "one", Target: "#chan"}), Equals, &targets[0])
	c.Assert(p.Target(&Message{Account: "two", Target: "nick"}), Equals, &targets[1])
	c.Assert(p.Target(&Message{Account: "one", Target: "#chan"}), Equals, &targets[0])
	c.Assert(p.Target(&Message{Account: "three", Target: "nick"}), Equals, &targets[2])
	c.Assert(p.Target(&Message{Account: "three", Target: "#chan"}), Equals, &targets[2])
	c.Assert(p.Target(&Message{Account: "one", Target: "nick"}), IsNil)
	c.Assert(p.Target(&Message{Account: "two", Target: "#chan"}), IsNil)
}

func (s *PluggerSuite) TestBroadcastf(c *C) {
	p := s.plugger(nil, []bson.M{{"account": "one", "target": "#chan"}, {"account": "two", "target": "nick"}})
	p.Broadcastf("<%s>", "text")
	c.Assert(s.sent, DeepEquals, []string{"[one] PRIVMSG #chan :<text>", "[two] PRIVMSG nick :<text>"})
}

func (s *PluggerSuite) TestBroadcast(c *C) {
	p := s.plugger(nil, []bson.M{{"account": "one", "target": "#chan"}, {"account": "two", "target": "nick"}})
	p.Broadcast(&Message{Cmd: "TEST", Text: "<text>"})
	c.Assert(s.sent, DeepEquals, []string{"[one] TEST #chan :<text>", "[two] TEST nick :<text>"})
}
