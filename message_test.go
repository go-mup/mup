package mup

import (
	. "gopkg.in/check.v1"
)

type MessageSuite struct{}

var _ = Suite(&MessageSuite{})

type parseTest struct {
	line string
	msg  Message
}

var parseIncomingTests = []parseTest{
	{
		"CMD",
		Message{
			Command: "CMD",
		},
	}, {
		"CMD some params",
		Message{
			Command: "CMD",
			Params:  []string{"some", "params"},
		},
	}, {
		"CMD some params :Some text",
		Message{
			Command: "CMD",
			Params:  []string{"some", "params"},
			Text:    "Some text",
		},
	}, {
		"PRIVMSG #channel :Some text",
		Message{
			Channel: "#channel",
			Command: "PRIVMSG",
			Text:    "Some text",
		},
	}, {
		"NOTICE #channel :Some text",
		Message{
			Channel: "#channel",
			Command: "NOTICE",
			Text:    "Some text",
		},
	}, {
		"CMD some:param :Some text",
		Message{
			Command: "CMD",
			Params:  []string{"some:param"},
			Text:    "Some text",
		},
	}, {
		":nick!user CMD",
		Message{
			Nick:    "nick",
			User:    "user",
			Command: "CMD",
			AsNick:  "mup",
		},
	}, {
		":nick@host CMD",
		Message{
			Nick:    "nick",
			Host:    "host",
			Command: "CMD",
			AsNick:  "mup",
		},
	}, {
		":nick!user@host CMD",
		Message{
			Nick:    "nick",
			User:    "user",
			Host:    "host",
			Command: "CMD",
			AsNick:  "mup",
		},
	},

	// Empty nick shouldn't be interpreted.
	{
		"PRIVMSG #channel :: Text",
		Message{
			Command: "PRIVMSG",
			Channel: "#channel",
			Text:    ": Text",
		},
	},

	// AsNick interpretation
	{
		"CMD",
		Message{
			Command: "CMD",
			AsNick:  "mup",
		},
	}, {
		"PRIVMSG #channel :Text",
		Message{
			Command: "PRIVMSG",
			Channel: "#channel",
			Text:    "Text",
			AsNick:  "mup",
		},
	}, {
		"PRIVMSG mup :Hello there",
		Message{
			Command: "PRIVMSG",
			Text:    "Hello there",
			AsNick:  "mup",

			ToMup:   true,
			MupText: "Hello there",
		},
	}, {
		"PRIVMSG mup :mup: Hello there",
		Message{
			Command: "PRIVMSG",
			Text:    "mup: Hello there",
			AsNick:  "mup",

			ToMup:   true,
			MupText: "Hello there",
		},
	}, {
		"PRIVMSG mup :mup, Hello there",
		Message{
			Command: "PRIVMSG",
			Text:    "mup, Hello there",
			AsNick:  "mup",

			ToMup:   true,
			MupText: "Hello there",
		},
	}, {
		"PRIVMSG #channel :mup: Hello there",
		Message{
			Command: "PRIVMSG",
			Channel: "#channel",
			Text:    "mup: Hello there",

			ToMup:   true,
			AsNick:  "mup",
			MupText: "Hello there",
		},
	}, {
		"PRIVMSG mup :mup, Hello there",
		Message{
			Command: "PRIVMSG",
			Text:    "mup, Hello there",
			AsNick:  "mup",

			ToMup:   true,
			MupText: "Hello there",
		},
	},

	// Bang prefix handling
	{
		"CMD",
		Message{
			Command: "CMD",
			Bang:    "!",
		},
	}, {
		"PRIVMSG #channel :Text",
		Message{
			Command: "PRIVMSG",
			Channel: "#channel",
			Text:    "Text",
			Bang:    "!",
		},
	}, {
		"PRIVMSG #channel :!Hello there",
		Message{
			Command: "PRIVMSG",
			Channel: "#channel",
			Text:    "!Hello there",
			Bang:    "!",

			ToMup:   true,
			MupText: "Hello there",
		},
	}, {
		"PRIVMSG mup :mup: !Hello there",
		Message{
			Command: "PRIVMSG",
			Text:    "mup: !Hello there",
			Bang:    "!",

			ToMup:   true,
			AsNick:  "mup",
			MupText: "Hello there",
		},
	},
}

var parseOutgoingTests = []parseTest{
	{
		"PRIVMSG nick :mup: !Hello there",
		Message{
			Command: "PRIVMSG",
			Text:    "mup: !Hello there",
			Nick:    "nick",
		},
	},
}

func (s *MessageSuite) TestParseIncoming(c *C) {
	for _, test := range parseIncomingTests {
		c.Logf("Parsing incoming line: %s", test.line)
		msg := ParseIncoming("", "mup", "!", test.line)
		test.msg.AsNick = "mup"
		test.msg.Bang = "!"
		c.Assert(msg, DeepEquals, &test.msg)
	}
}

func (s *MessageSuite) TestParseOutgoing(c *C) {
	for _, test := range parseOutgoingTests {
		c.Logf("Parsing outgoing line: %s", test.line)
		msg := ParseOutgoing("", test.line)
		c.Assert(msg, DeepEquals, &test.msg)
	}
}

func (s *MessageSuite) TestParseIncomingAccount(c *C) {
	msg := ParseIncoming("account", "", "", "CMD")
	c.Assert(msg.Account, Equals, "account")
}

func (s *MessageSuite) TestParseOutgoingAccount(c *C) {
	msg := ParseOutgoing("account", "CMD")
	c.Assert(msg.Account, Equals, "account")
}

var stringTests = []struct {
	msg  Message
	line string
}{
	{
		Message{Command: "CMD"},
		"CMD",
	}, {
		Message{Text: "Text", AsNick: "mup"},
		"PRIVMSG mup :Text",
	}, {
		Message{Command: "PRIVMSG", Text: "Text", AsNick: "mup"},
		"PRIVMSG mup :Text",
	}, {
		Message{Command: "PRIVMSG", Params: []string{"ignored"}, Text: "Text", AsNick: "mup"},
		"PRIVMSG mup :Text",
	}, {
		Message{Command: "CMD", Nick: "nick", User: "user", Host: "host"},
		"CMD",
	}, {
		Message{Command: "CMD", Nick: "nick", AsNick: "mup"},
		":nick CMD",
	}, {
		Message{Command: "CMD", Nick: "nick", User: "user", AsNick: "mup"},
		":nick!user CMD",
	}, {
		Message{Command: "CMD", Nick: "nick", Host: "host", AsNick: "mup"},
		":nick@host CMD",
	}, {
		Message{Command: "CMD", Nick: "nick", User: "user", Host: "host", AsNick: "mup"},
		":nick!user@host CMD",
	}, {
		Message{Command: "PING", Text: "text"},
		"PING :text",
	}, {
		Message{Command: "CMD", Params: []string{"some", "params"}, Text: "some text"},
		"CMD some params :some text",
	}, {
		Message{Command: "A\rB\n", Params: []string{"\rC\nD"}, Text: "E\rF\nG\x00"},
		"A_B_ _C_D :E_F_G_",
	}, {
		Message{Command: "PRIVMSG", Channel: "\rC\nD", Text: "E\rF\nG\x00"},
		"PRIVMSG _C_D :E_F_G_",
	},
}

func (s *MessageSuite) TestMessageString(c *C) {
	for _, test := range stringTests {
		c.Assert(test.msg.String(), Equals, test.line)
	}
	for _, test := range parseIncomingTests {
		c.Assert(test.msg.String(), Equals, test.line)
	}
	for _, test := range parseOutgoingTests {
		c.Assert(test.msg.String(), Equals, test.line)
	}
}
