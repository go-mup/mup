package mup_test

import (
	. "gopkg.in/check.v1"
	"gopkg.in/mup.v0"
	"time"
)

type MessageSuite struct{}

var _ = Suite(&MessageSuite{})

type parseTest struct {
	line string
	msg  mup.Message
}

var parseIncomingTests = []parseTest{
	{
		"CMD",
		mup.Message{
			Command: "CMD",
		},
	}, {
		"CMD some params",
		mup.Message{
			Command: "CMD",
			Params:  []string{"some", "params"},
		},
	}, {
		"CMD some params :Some text",
		mup.Message{
			Command: "CMD",
			Params:  []string{"some", "params"},
			Text:    "Some text",
		},
	}, {
		"PRIVMSG #channel :Some text",
		mup.Message{
			Channel: "#channel",
			Command: "PRIVMSG",
			Text:    "Some text",
		},
	}, {
		"NOTICE #channel :Some text",
		mup.Message{
			Channel: "#channel",
			Command: "NOTICE",
			Text:    "Some text",
		},
	}, {
		"CMD some:param :Some text",
		mup.Message{
			Command: "CMD",
			Params:  []string{"some:param"},
			Text:    "Some text",
		},
	}, {
		":nick!user CMD",
		mup.Message{
			Nick:    "nick",
			User:    "user",
			Command: "CMD",
			AsNick:  "mup",
		},
	}, {
		":nick@host CMD",
		mup.Message{
			Nick:    "nick",
			Host:    "host",
			Command: "CMD",
			AsNick:  "mup",
		},
	}, {
		":nick!user@host CMD",
		mup.Message{
			Nick:    "nick",
			User:    "user",
			Host:    "host",
			Command: "CMD",
			AsNick:  "mup",
		},
	}, {
		":host.com CMD",
		mup.Message{
			Host:    "host.com",
			Command: "CMD",
			AsNick:  "mup",
		},
	},

	// Empty nick shouldn't be interpreted.
	{
		"PRIVMSG #channel :: Text",
		mup.Message{
			Command: "PRIVMSG",
			Channel: "#channel",
			Text:    ": Text",
		},
	},

	// AsNick interpretation
	{
		"CMD",
		mup.Message{
			Command: "CMD",
			AsNick:  "mup",
		},
	}, {
		"PRIVMSG #channel :Text",
		mup.Message{
			Command: "PRIVMSG",
			Channel: "#channel",
			Text:    "Text",
			AsNick:  "mup",
		},
	}, {
		"PRIVMSG mup :Hello there",
		mup.Message{
			Command: "PRIVMSG",
			Text:    "Hello there",
			BotText: "Hello there",
			AsNick:  "mup",
		},
	}, {
		"PRIVMSG mup :mup: Hello there",
		mup.Message{
			Command: "PRIVMSG",
			Text:    "mup: Hello there",
			BotText: "Hello there",
			AsNick:  "mup",
		},
	}, {
		"PRIVMSG mup :mup, Hello there",
		mup.Message{
			Command: "PRIVMSG",
			Text:    "mup, Hello there",
			BotText: "Hello there",
			AsNick:  "mup",
		},
	}, {
		"PRIVMSG #channel :mup: Hello there",
		mup.Message{
			Command: "PRIVMSG",
			Channel: "#channel",
			Text:    "mup: Hello there",
			BotText: "Hello there",
			AsNick:  "mup",
		},
	}, {
		"PRIVMSG mup :mup, Hello there",
		mup.Message{
			Command: "PRIVMSG",
			Text:    "mup, Hello there",
			BotText: "Hello there",
			AsNick:  "mup",
		},
	},

	// Bang prefix handling
	{
		"CMD",
		mup.Message{
			Command: "CMD",
			Bang:    "!",
		},
	}, {
		"PRIVMSG #channel :Text",
		mup.Message{
			Command: "PRIVMSG",
			Channel: "#channel",
			Text:    "Text",
			Bang:    "!",
		},
	}, {
		"PRIVMSG #channel :!Hello there",
		mup.Message{
			Command: "PRIVMSG",
			Channel: "#channel",
			Text:    "!Hello there",
			BotText: "Hello there",
			Bang:    "!",
		},
	}, {
		"PRIVMSG mup :mup: !Hello there",
		mup.Message{
			Command: "PRIVMSG",
			Text:    "mup: !Hello there",
			BotText: "Hello there",
			AsNick:  "mup",
			Bang:    "!",
		},
	},

	// @ prefix also qualifies message as personal.
	{
		"PRIVMSG #chan :@mup Hello there",
		mup.Message{
			Command: "PRIVMSG",
			Channel: "#chan",
			Text:    "@mup Hello there",
			BotText: "Hello there",
			AsNick:  "mup",
		},
	}, {
		"PRIVMSG #chan :@mup: Hello there",
		mup.Message{
			Command: "PRIVMSG",
			Channel: "#chan",
			Text:    "@mup: Hello there",
			BotText: "Hello there",
			AsNick:  "mup",
		},
	}, {
		"PRIVMSG #chan :@mup: !Hello there",
		mup.Message{
			Command: "PRIVMSG",
			Channel: "#chan",
			Text:    "@mup: !Hello there",
			BotText: "Hello there",
			AsNick:  "mup",
			Bang:    "!",
		},
	},

	// Don't take notices personally.
	{
		"NOTICE mup :Hello there",
		mup.Message{
			Command: "NOTICE",
			Text:    "Hello there",
			AsNick:  "mup",
		},
	}, {
		"NOTICE #chan :mup, Hello there",
		mup.Message{
			Command: "NOTICE",
			Channel: "#chan",
			Text:    "mup, Hello there",
		},
	}, {
		"NOTICE #chan :!Hello there",
		mup.Message{
			Command: "NOTICE",
			Channel: "#chan",
			Text:    "!Hello there",
		},
	},
}

var parseOutgoingTests = []parseTest{
	{
		"PRIVMSG nick :mup: !Hello there",
		mup.Message{
			Command: "PRIVMSG",
			Text:    "mup: !Hello there",
			Nick:    "nick",
		},
	},
}

func (s *MessageSuite) TestParseIncoming(c *C) {
	for _, test := range parseIncomingTests {
		c.Logf("Parsing incoming line: %s", test.line)
		before := time.Now().Add(-1 * time.Second)
		msg := mup.ParseIncoming("", "mup", "!", test.line)
		after := time.Now().Add(1 * time.Second)
		c.Assert(msg.Time.After(before), Equals, true)
		c.Assert(msg.Time.Before(after), Equals, true)
		msg.Time = time.Time{}
		test.msg.AsNick = "mup"
		test.msg.Bang = "!"
		c.Assert(msg, DeepEquals, &test.msg)
	}
}

func (s *MessageSuite) TestParseOutgoing(c *C) {
	for _, test := range parseOutgoingTests {
		c.Logf("Parsing outgoing line: %s", test.line)
		before := time.Now().Add(-1 * time.Second)
		msg := mup.ParseOutgoing("", test.line)
		after := time.Now().Add(1 * time.Second)
		c.Assert(msg.Time.After(before), Equals, true)
		c.Assert(msg.Time.Before(after), Equals, true)
		msg.Time = time.Time{}
		c.Assert(msg, DeepEquals, &test.msg)
	}
}

func (s *MessageSuite) TestParseIncomingAccount(c *C) {
	msg := mup.ParseIncoming("account", "", "", "CMD")
	c.Assert(msg.Account, Equals, "account")
}

func (s *MessageSuite) TestParseOutgoingAccount(c *C) {
	msg := mup.ParseOutgoing("account", "CMD")
	c.Assert(msg.Account, Equals, "account")
}

var stringTests = []struct {
	msg  mup.Message
	line string
}{
	{
		mup.Message{Command: "CMD"},
		"CMD",
	}, {
		mup.Message{Text: "Text", AsNick: "mup"},
		"PRIVMSG mup :Text",
	}, {
		mup.Message{Command: "PRIVMSG", Text: "Text", AsNick: "mup"},
		"PRIVMSG mup :Text",
	}, {
		mup.Message{Command: "PRIVMSG", Params: []string{"ignored"}, Text: "Text", AsNick: "mup"},
		"PRIVMSG mup :Text",
	}, {
		mup.Message{Command: "CMD", Nick: "nick", User: "user", Host: "host"},
		"CMD",
	}, {
		mup.Message{Command: "CMD", Nick: "nick", AsNick: "mup"},
		":nick CMD",
	}, {
		mup.Message{Command: "CMD", Nick: "nick", User: "user", AsNick: "mup"},
		":nick!user CMD",
	}, {
		mup.Message{Command: "CMD", Nick: "nick", Host: "host", AsNick: "mup"},
		":nick@host CMD",
	}, {
		mup.Message{Command: "CMD", Nick: "nick", User: "user", Host: "host", AsNick: "mup"},
		":nick!user@host CMD",
	}, {
		mup.Message{Command: "PING", Text: "text"},
		"PING :text",
	}, {
		mup.Message{Command: "CMD", Params: []string{"some", "params"}, Text: "some text"},
		"CMD some params :some text",
	}, {
		mup.Message{Command: "A\rB\n", Params: []string{"\rC\nD"}, Text: "E\rF\nG\x00"},
		"A_B_ _C_D :E_F_G_",
	}, {
		mup.Message{Command: "PRIVMSG", Channel: "\rC\nD", Text: "E\rF\nG\x00"},
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

var addrContainsTests = []struct {
	contains  mup.Address
	contained mup.Address
	result    bool
}{{
	mup.Address{},
	mup.Address{},
	true,
}, {
	mup.Address{},
	mup.Address{Account: "one"},
	true,
}, {
	mup.Address{Account: "one"},
	mup.Address{Account: "one"},
	true,
}, {
	mup.Address{Account: "one"},
	mup.Address{Account: "two"},
	false,
}, {
	mup.Address{Account: "two"},
	mup.Address{},
	false,
}, {
	mup.Address{},
	mup.Address{Channel: "#one"},
	true,
}, {
	mup.Address{Channel: "#one"},
	mup.Address{Channel: "#one"},
	true,
}, {
	mup.Address{Channel: "#one"},
	mup.Address{Channel: "#two"},
	false,
}, {
	mup.Address{Channel: "#two"},
	mup.Address{},
	false,
}, {
	mup.Address{},
	mup.Address{Nick: "one"},
	true,
}, {
	mup.Address{Nick: "one"},
	mup.Address{Nick: "one"},
	true,
}, {
	mup.Address{Nick: "one"},
	mup.Address{Nick: "two"},
	false,
}, {
	mup.Address{Nick: "two"},
	mup.Address{},
	false,
}, {
	mup.Address{},
	mup.Address{Account: "one", Channel: "#one", Nick: "nickone"},
	true,
}, {
	mup.Address{Account: "one"},
	mup.Address{Account: "one", Channel: "#one", Nick: "nickone"},
	true,
}, {
	mup.Address{Channel: "#one"},
	mup.Address{Account: "one", Channel: "#one", Nick: "nickone"},
	true,
}, {
	mup.Address{Nick: "nickone"},
	mup.Address{Account: "one", Channel: "#one", Nick: "nickone"},
	true,
}, {
	mup.Address{Account: "one", Channel: "#one"},
	mup.Address{Account: "one", Channel: "#one", Nick: "nickone"},
	true,
}, {
	mup.Address{Account: "one", Nick: "nickone"},
	mup.Address{Account: "one", Channel: "#one", Nick: "nickone"},
	true,
}, {
	mup.Address{Account: "one", Channel: "#one", Nick: "nickone"},
	mup.Address{Account: "one", Channel: "#one", Nick: "nickone"},
	true,
}, {
	mup.Address{Account: "two", Channel: "#one", Nick: "nickone"},
	mup.Address{Account: "one", Channel: "#one", Nick: "nickone"},
	false,
}, {
	mup.Address{Account: "one", Channel: "#two", Nick: "nickone"},
	mup.Address{Account: "one", Channel: "#one", Nick: "nickone"},
	false,
}, {
	mup.Address{Account: "one", Channel: "#one", Nick: "nicktwo"},
	mup.Address{Account: "one", Channel: "#one", Nick: "nickone"},
	false,
}}

func (s *MessageSuite) TestAddressContains(c *C) {
	for _, test := range addrContainsTests {
		if test.contains.Contains(test.contained) != test.result {
			c.Fatalf("%#v.Contains(%#v) returned %v", test.contains, test.contained, !test.result)
		}
		c.Assert(test.contains.Contains(test.contained), Equals, test.result)
	}
}
