package help_test

import (
	"testing"

	. "gopkg.in/check.v1"
	"gopkg.in/mgo.v2/bson"
	"gopkg.in/mup.v0"
	_ "gopkg.in/mup.v0/plugins/help"
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
	s.dbserver.Reset()
}

type helpTest struct {
	send string
	recv []string
	cmds schema.Commands
}

var helpTests = []helpTest{{
	send: "help cmdname",
	recv: []string{
		`PRIVMSG nick :Command "cmdname" not found.`,
	},
}, {
	send: "help cmdname",
	recv: []string{
		`PRIVMSG nick :cmdname — The author of this command is unhelpful.`,
	},
	cmds: schema.Commands{{Name: "cmdname"}},
}, {
	send: "help cmdname",
	recv: []string{
		`PRIVMSG nick :cmdname — Does nothing.`,
	},
	cmds: schema.Commands{{Name: "cmdname", Help: "Does nothing."}},
}, {
	send: "help cmdname",
	recv: []string{
		`PRIVMSG nick :cmdname — Line one.`,
		`PRIVMSG nick :Line two. Line three.`,
		`PRIVMSG nick :Line five. Line six.`,
		`PRIVMSG nick :Line nine.`,
	},
	cmds: schema.Commands{{Name: "cmdname", Help: "\n \tLine one.\n \tLine two.\n Line three.\n \n \t Line five.\n Line six.\n \n \nLine nine.\n\n"}},
}, {
	send: "help cmdname",
	recv: []string{
		`PRIVMSG nick :cmdname -arg0 [-arg1=<string>] [-arg2=<hint>] <arg3> [<arg4>] [<arg5 ...>] — Does nothing.`,
		`PRIVMSG nick :           -arg0 — Argument zero.`,
		`PRIVMSG nick :  -arg1=<string> — Argument one.`,
		`PRIVMSG nick :    -arg2=<hint> — Argument two.`,
		`PRIVMSG nick :          <arg3> — Argument three.`,
	},
	cmds: schema.Commands{{
		Name: "cmdname",
		Help: "Does nothing.",
		Args: schema.Args{{
			Name: "-arg0",
			Flag: schema.Required,
			Type: schema.Bool,
			Help: "Argument zero.",
		}, {
			Name: "-arg1",
			Help: "Argument one.",
		}, {
			Name: "-arg2",
			Hint: "hint",
			Help: "Argument two.",
		}, {
			Name: "arg3",
			Flag: schema.Required,
			Help: "Argument three.",
		}, {
			Name: "arg4",
		}, {
			Name: "arg5",
			Flag: schema.Trailing,
		}},
	}},
}}

func (s *HelpSuite) TestHelp(c *C) {
	db := s.dbserver.Session().DB("mup")
	for _, test := range helpTests {
		err := db.C("plugins").Insert(bson.M{"_id": "test", "commands": test.cmds})
		c.Assert(err, IsNil)

		tester := mup.NewPluginTester("help")
		tester.SetDatabase(db)
		tester.Start()
		tester.Sendf("", test.send)
		tester.Stop()
		c.Assert(tester.RecvAll(), DeepEquals, test.recv)

		s.dbserver.Reset()
	}
}
