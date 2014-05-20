package mup

import (
	. "gopkg.in/check.v1"
)

var parseTests = []struct {
	nick string
	bang string
	line string
	msg  Message
}{
	{
		"",
		"",
		"CMD",
		Message{
			Cmd: "CMD",
		},
	}, {
		"",
		"",
		":prefix CMD",
		Message{
			Prefix: "prefix",
			Cmd:    "CMD",
		},
	}, {
		"",
		"",
		":prefix CMD Yo",
		Message{
			Cmd:    "CMD",
			Prefix: "prefix",
			Params: []string{"Yo"},
		},
	}, {
		"",
		"",
		":prefix CMD Hi there",
		Message{
			Cmd:    "CMD",
			Prefix: "prefix",
			Params: []string{"Hi", "there"},
		},
	}, {
		"",
		"",
		"CMD :Some text",
		Message{
			Cmd:    "CMD",
			Params: []string{":Some text"},
			Text:   "Some text",
		},
	}, {
		"",
		"",
		"CMD Hi:there :Some text",
		Message{
			Cmd:    "CMD",
			Params: []string{"Hi:there", ":Some text"},
			Text:   "Some text",
		},
	}, {
		"",
		"",
		":nick!user@host CMD",
		Message{
			Prefix: "nick!user@host",
			Nick:   "nick",
			User:   "user",
			Host:   "host",
			Cmd:    "CMD",
		},
	}, {
		"",
		"",
		"PRIVMSG #channel :Text",
		Message{
			Cmd:    "PRIVMSG",
			Params: []string{"#channel", ":Text"},
			Target: "#channel",
			Text:   "Text",
		},
	}, {
		"",
		"",
		"NOTICE #channel :Text",
		Message{
			Cmd:    "NOTICE",
			Params: []string{"#channel", ":Text"},
			Target: "#channel",
			Text:   "Text",
		},
	},

	// Empty nick shouldn't be interpreted.
	{
		"",
		"",
		"PRIVMSG #channel :: Text",
		Message{
			Cmd:    "PRIVMSG",
			Params: []string{"#channel", ":: Text"},
			Target: "#channel",
			Text:   ": Text",
		},
	},

	// MupNick interpretation
	{
		"mup",
		"",
		"CMD",
		Message{
			Cmd:     "CMD",
			MupNick: "mup",
		},
	}, {
		"mup",
		"",
		"PRIVMSG #channel :Text",
		Message{
			Cmd:    "PRIVMSG",
			Target: "#channel",
			Params: []string{"#channel", ":Text"},
			Text:   "Text",

			MupNick: "mup",
		},
	}, {
		"mup",
		"",
		"PRIVMSG mup :Hello there",
		Message{
			Cmd:    "PRIVMSG",
			Params: []string{"mup", ":Hello there"},
			Target: "mup",
			Text:   "Hello there",

			MupNick: "mup",
			MupChat: true,
			MupText: "Hello there",
		},
	}, {
		"mup",
		"",
		"PRIVMSG mup :mup: Hello there",
		Message{
			Cmd:    "PRIVMSG",
			Params: []string{"mup", ":mup: Hello there"},
			Target: "mup",
			Text:   "mup: Hello there",

			MupNick: "mup",
			MupChat: true,
			MupText: "Hello there",
		},
	}, {
		"mup",
		"",
		"PRIVMSG mup :mup, Hello there",
		Message{
			Cmd:    "PRIVMSG",
			Params: []string{"mup", ":mup, Hello there"},
			Target: "mup",
			Text:   "mup, Hello there",

			MupNick: "mup",
			MupChat: true,
			MupText: "Hello there",
		},
	}, {
		"mup",
		"",
		"PRIVMSG #channel :mup: Hello there",
		Message{
			Cmd:    "PRIVMSG",
			Params: []string{"#channel", ":mup: Hello there"},
			Target: "#channel",
			Text:   "mup: Hello there",

			MupNick: "mup",
			MupChat: true,
			MupText: "Hello there",
		},
	}, {
		"mup",
		"",
		"PRIVMSG mup :mup, Hello there",
		Message{
			Cmd:    "PRIVMSG",
			Params: []string{"mup", ":mup, Hello there"},
			Target: "mup",
			Text:   "mup, Hello there",

			MupNick: "mup",
			MupChat: true,
			MupText: "Hello there",
		},
	},

	// Bang prefix handling
	{
		"",
		"!",
		"CMD",
		Message{
			Cmd:  "CMD",
			Bang: "!",
		},
	}, {
		"",
		"!",
		"PRIVMSG #channel :Text",
		Message{
			Cmd:    "PRIVMSG",
			Target: "#channel",
			Params: []string{"#channel", ":Text"},
			Text:   "Text",
			Bang:   "!",
		},
	}, {
		"",
		"!",
		"PRIVMSG #channel :!Hello there",
		Message{
			Cmd:    "PRIVMSG",
			Params: []string{"#channel", ":!Hello there"},
			Target: "#channel",
			Text:   "!Hello there",
			Bang:   "!",

			MupChat: true,
			MupText: "Hello there",
		},
	}, {
		"mup",
		"!",
		"PRIVMSG mup :mup: !Hello there",
		Message{
			Cmd:    "PRIVMSG",
			Params: []string{"mup", ":mup: !Hello there"},
			Target: "mup",
			Text:   "mup: !Hello there",
			Bang:   "!",

			MupNick: "mup",
			MupChat: true,
			MupText: "Hello there",
		},
	},
}

func (s *S) TestParseMessage(c *C) {
	for _, test := range parseTests {
		c.Assert(ParseMessage(test.nick, test.bang, test.line), Equals, &test.msg)
	}
}

var stringTests = []struct {
	msg  Message
	line string
}{
	{
		Message{Cmd: "CMD"},
		"CMD",
	}, {
		Message{Cmd: "PRIVMSG", Target: "mup", Text: "Text"},
		"PRIVMSG mup :Text",
	}, {
		Message{Cmd: "PRIVMSG", Params: []string{"mup", ":Text"}},
		"PRIVMSG mup :Text",
	}, {
		Message{Cmd: "CMD", Nick: "nick", User: "user", Host: "host"},
		":nick!user@host CMD",
	}, {
		Message{Cmd: "CMD", Prefix: "nick!user@host"},
		":nick!user@host CMD",
	},
}

func (s *S) TestMessageString(c *C) {
	for _, test := range stringTests {
		c.Assert(test.msg.String(), Equals, test.line)
	}
	for _, test := range parseTests {
		c.Assert(test.msg.String(), Equals, test.line)
	}
}
