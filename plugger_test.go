package mup

import (
	. "gopkg.in/check.v1"
)

var _ = Suite(&PluggerSuite{})

func newTestPlugger(sent *[]string, settings func(result interface{})) *Plugger {
	*sent = nil
	send := func(msg *Message) error {
		*sent = append(*sent, "["+msg.Account+"] "+msg.String())
		return nil
	}
	return newPlugger(send, settings)
}

type PluggerSuite struct {
	plugger *Plugger
	sent    []string
	settings interface{}
}

func (s *PluggerSuite) SetUpTest(c *C) {
	s.plugger = newTestPlugger(&s.sent, s.loadSettings)
}

func (s *PluggerSuite) loadSettings(v interface{}) {
	s.settings = v
}

func parse(line string) *Message {
	msg := ParseMessage("mup", "!", line)
	msg.Account = "origin"
	return msg
}

func (s *PluggerSuite) TestReplyfPrivate(c *C) {
	s.plugger.Replyf(parse(":nick!~user@host PRIVMSG mup :query"), "<%s>", "reply")
	c.Assert(s.sent, DeepEquals, []string{"[origin] PRIVMSG nick :<reply>"})
}

func (s *PluggerSuite) TestReplyfChannel(c *C) {
	s.plugger.Replyf(parse(":nick!~user@host PRIVMSG #channel :mup: query"), "<%s>", "reply")
	c.Assert(s.sent, DeepEquals, []string{"[origin] PRIVMSG #channel :nick: <reply>"})
}

func (s *PluggerSuite) TestSendf(c *C) {
	s.plugger.Sendf("myaccount", "nick", "<%s>", "text")
	c.Assert(s.sent, DeepEquals, []string{"[myaccount] PRIVMSG nick :<text>"})
}

func (s *PluggerSuite) TestSend(c *C) {
	msg := &Message{
		Account: "myaccount",
		Cmd:     "PRIVMSG",
		Target:  "nick",
		Text:    "query",
	}
	s.plugger.Send(msg)
	c.Assert(s.sent, DeepEquals, []string{"[myaccount] PRIVMSG nick :query"})
}

func (s *PluggerSuite) TestSettings(c *C) {
	s.plugger.Settings("value")
	c.Assert(s.settings, Equals, "value")
}
