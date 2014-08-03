package help_test

import (
	"testing"

	. "gopkg.in/check.v1"
	"gopkg.in/mgo.v2/bson"
	"gopkg.in/mup.v0"
	"gopkg.in/mup.v0/plugins/help"
	"gopkg.in/mup.v0/schema"
)

func Test(t *testing.T) { TestingT(t) }

var _ = Suite(&HelpSuite{})

type HelpSuite struct {
	dbserver mup.DBServerHelper
}

func (s *HelpSuite) SetUpSuite(c *C) {
	s.dbserver.SetPath(c.MkDir())
}

func (s *HelpSuite) TearDownSuite(c *C) {
	s.dbserver.Stop()
}

func (s *HelpSuite) TearDownTest(c *C) {
	s.dbserver.Wipe()
}

type helpTest struct {
	send    string
	recv    string
	sendAll []string
	recvAll []string
	cmds    schema.Commands
	targets []bson.M
	known   bool
	config  bson.M
}

var helpTests = []helpTest{{
	send: "help",
	recv: "PRIVMSG nick :No known commands available. Go load some plugins.",
}, {
	send: "help",
	recv: `PRIVMSG nick :Run "help <cmdname>" for details on: cmd1, cmd2`,
	known: true,
	cmds:  schema.Commands{{Name: "cmd1"}, {Name: "cmd2"}},
}, {
	send: "help cmdname",
	recv: `PRIVMSG nick :Command "cmdname" not found.`,
}, {
	send: "help cmdname",
	recv: `PRIVMSG nick :cmdname — The author of this command is unhelpful.`,
	cmds: schema.Commands{{Name: "cmdname"}},
}, {
	send: "help cmdname",
	recv: `PRIVMSG nick :cmdname — Does nothing.`,
	cmds: schema.Commands{{Name: "cmdname", Help: "Does nothing."}},
}, {
	send:  "help cmdname",
	recv:  `PRIVMSG nick :cmdname — Does nothing.`,
	cmds:  schema.Commands{{Name: "cmdname", Help: "Does nothing."}},
	known: true,
}, {
	send: "help cmdname",
	recvAll: []string{
		`PRIVMSG nick :cmdname — Line one.`,
		`PRIVMSG nick :Line two. Line three.`,
		`PRIVMSG nick :Line five. Line six.`,
		`PRIVMSG nick :Line nine.`,
	},
	cmds: schema.Commands{{Name: "cmdname", Help: "\n \tLine one.\n \tLine two.\n Line three.\n \n \t Line five.\n Line six.\n \n \nLine nine.\n\n"}},
}, {
	send: "help cmdname",
	recvAll: []string{
		`PRIVMSG nick :cmdname -arg0 [-arg1=<string>] [-arg2=<hint>] <arg3> [<arg4>] [<arg5 ...>]`,
		`PRIVMSG nick :Does nothing.`,
	},
	cmds: schema.Commands{{
		Name: "cmdname",
		Help: "Does nothing.",
		Args: schema.Args{{
			Name: "-arg0",
			Flag: schema.Required,
			Type: schema.Bool,
		}, {
			Name: "-arg1",
		}, {
			Name: "-arg2",
			Hint: "hint",
		}, {
			Name: "arg3",
			Flag: schema.Required,
		}, {
			Name: "arg4",
		}, {
			Name: "arg5",
			Flag: schema.Trailing,
		}},
	}},
}, {
	sendAll: []string{"foo", "foo"},
	recvAll: []string{
		"PRIVMSG nick :I apologize, but I'm pretty strict about only responding to known commands.",
		"PRIVMSG nick :In-com-pre-hen-si-ble-ness.",
	},
}, {
	send:   "foo",
	recv:   `PRIVMSG nick :Command "foo" not found.`,
	config: bson.M{"boring": true},
}, {
	send:    "cmdname",
	recv:    `PRIVMSG nick :Plugin "test" is not enabled here.`,
	targets: []bson.M{{"account": "other"}},
	cmds:    schema.Commands{{Name: "cmdname"}},
}, {
	send:  "cmdname",
	recv:  `PRIVMSG nick :Plugin "test" is not running.`,
	cmds:  schema.Commands{{Name: "cmdname"}},
	known: true,
}}

func (s *HelpSuite) TestHelp(c *C) {
	for _, test := range helpTests {
		s.testHelp(c, &test)
	}
}

func (s *HelpSuite) testHelp(c *C, test *helpTest) {
	defer s.dbserver.Wipe()

	session := s.dbserver.Session()
	defer session.Close()

	db := session.DB("")
	plugins := db.C("plugins")
	known := db.C("plugins.known")

	tester := mup.NewPluginTester("help")
	tester.SetDatabase(db)

	if test.known {
		err := known.Insert(bson.M{"_id": "test", "commands": test.cmds})
		c.Assert(err, IsNil)
	} else {
		err := plugins.Insert(bson.M{"_id": "test", "commands": test.cmds, "targets": []bson.M{{"account": "test"}}})
		c.Assert(err, IsNil)
		if test.targets != nil {
			err = plugins.UpdateId("test", bson.M{"$set": bson.M{"targets": test.targets}})
			c.Assert(err, IsNil)
		}
	}
	err := plugins.Insert(bson.M{"_id": "help", "commands": help.Plugin.Commands, "targets": []bson.M{{"account": "test"}}})
	c.Assert(err, IsNil)

	tester.SetConfig(test.config)
	tester.Start()
	if test.send != "" {
		tester.Sendf("", test.send)
	}
	if test.sendAll != nil {
		tester.SendAll("", test.sendAll)
	}
	tester.Stop()

	if test.recv != "" {
		c.Assert(tester.Recv(), Equals, test.recv)
	}
	if test.recvAll != nil {
		c.Assert(tester.RecvAll(), DeepEquals, test.recvAll)
	}
}
