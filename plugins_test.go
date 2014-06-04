package mup

import (
	. "gopkg.in/check.v1"
)

var _ = Suite(&EchoSuite{})

type EchoSuite struct {
	plugger *Plugger
	replies []string
	plugin  Plugin
}

func (s *EchoSuite) SetUpTest(c *C) {
	s.plugger = newTestPlugger(&s.replies)
	s.plugin = registeredPlugins["echo"](s.plugger)
}

var echoTests = []struct{ msg, reply string }{
	{":nick!~user@host PRIVMSG mup :echo repeat", "PRIVMSG nick :repeat"},
	{":nick!~user@host PRIVMSG #channel :mup: echo repeat", "PRIVMSG #channel :nick: repeat"},
	{":nick!~user@host PRIVMSG #channel :echo repeat", ""},
	{":nick!~user@host PRIVMSG mup :notecho repeat", ""},
	{":nick!~user@host PRIVMSG mup :echonospace", ""},
}

func (s *EchoSuite) TestEcho(c *C) {
	for i, test := range echoTests {
		s.replies = nil
		c.Logf("Feeding message #%d: %s", i, test.msg)
		err := s.plugin.Handle(parse(test.msg))
		c.Assert(err, IsNil)
		if test.reply == "" {
			c.Assert(s.replies, IsNil)
		} else {
			c.Assert(s.replies, DeepEquals, []string{test.reply})
		}
	}
}
