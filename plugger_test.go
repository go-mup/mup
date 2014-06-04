package mup

import (
	. "gopkg.in/check.v1"
)

var _ = Suite(&PluggerSuite{})

func newTestPlugger(replies *[]string) *Plugger {
	*replies = nil
	p := newPlugger(func(msg *Message) error {
		*replies = append(*replies, msg.String())
		return nil
	})
	return p
}

type PluggerSuite struct {
	plugger *Plugger
	replies []string
}

func (s *PluggerSuite) SetUpTest(c *C) {
	s.plugger = newTestPlugger(&s.replies)
}

func parse(line string) *Message {
	return ParseMessage("mup", "!", line)
}

func (s *PluggerSuite) TestReplyfPrivate(c *C) {
	s.plugger.Replyf(parse(":nick!~user@host PRIVMSG mup :query"), "<%s>", "reply")
	c.Assert(s.replies, DeepEquals, []string{"PRIVMSG nick :<reply>"})
}

func (s *PluggerSuite) TestReplyfChannel(c *C) {
	s.plugger.Replyf(parse(":nick!~user@host PRIVMSG #channel :mup: query"), "<%s>", "reply")
	c.Assert(s.replies, DeepEquals, []string{"PRIVMSG #channel :nick: <reply>"})
}
