package help_test

import (
	"database/sql"
	"testing"

	"gopkg.in/mup.v0"
	"gopkg.in/mup.v0/schema"

	. "gopkg.in/check.v1"
)

func Test(t *testing.T) { TestingT(t) }

var _ = Suite(&HelpSuite{})

type HelpSuite struct {
	dbdir string
	db    *sql.DB
}

func (s *HelpSuite) SetUpSuite(c *C) {
	s.dbdir = c.MkDir()
}

type helpTest struct {
	send    string
	recv    string
	sendAll []string
	recvAll []string
	cmds    schema.Commands
	targets []mup.Address
	config  mup.Map
}

var helpTests = []helpTest{{
	send:    "help",
	recvAll: []string{"PRIVMSG nick :No known commands available. Go load some plugins."},
}, {
	send: "help",
	recv: `PRIVMSG nick :Run "help <cmdname>" for details on: cmd1, cmd2`,
	cmds: schema.Commands{{Name: "cmd1"}, {Name: "cmd2"}, {Name: "cmd3", Hide: true}},
}, {
	send: "start",
	recv: `PRIVMSG nick :Run "help <cmdname>" for details on: cmd1, cmd2`,
	cmds: schema.Commands{{Name: "cmd1"}, {Name: "cmd2"}, {Name: "cmd3", Hide: true}},
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
	send: "help cmdname",
	recv: `PRIVMSG nick :cmdname — Does nothing.`,
	cmds: schema.Commands{{Name: "cmdname", Help: "Does nothing."}},
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
	config: mup.Map{"boring": true},
}, {
	send:    "cmdname",
	recv:    `PRIVMSG nick :Plugin "test" is not enabled here.`,
	targets: []mup.Address{{Account: "other"}},
	cmds:    schema.Commands{{Name: "cmdname"}},
}, {
	send: "cmdname",
	recv: `PRIVMSG nick :Plugin "test" is not running.`,
	cmds: schema.Commands{{Name: "cmdname"}},
}, {
	send:   "[#chan] mup: foo",
	recv:   `PRIVMSG #chan :nick: Command "foo" not found.`,
	config: mup.Map{"boring": true},
}, {
	send:    "[#chan] !foo",
	recvAll: []string{},
	config:  mup.Map{"boring": true},
}}

func (s *HelpSuite) TestHelp(c *C) {
	for _, test := range helpTests {
		c.Logf("Running test: %#v\n", test)
		s.testHelp(c, &test)
	}
}

var testPlugin = mup.PluginSpec{Name: "test"}

func init() {
	mup.RegisterPlugin(&testPlugin)
}

func (s *HelpSuite) testHelp(c *C, test *helpTest) {
	db, err := mup.OpenDB(c.MkDir())
	c.Assert(err, IsNil)
	defer db.Close()

	tester := mup.NewPluginTester("help")
	tester.SetDatabase(db)
	tester.SetConfig(test.config)

	testPlugin.Commands = test.cmds
	tester.AddSchema("test")

	_, err = db.Exec("INSERT INTO account (name) VALUES ('test')")
	c.Assert(err, IsNil)
	_, err = db.Exec("INSERT INTO plugin (name) VALUES ('help')")
	c.Assert(err, IsNil)
	_, err = db.Exec("INSERT INTO target (plugin,account) VALUES ('help','test')")
	c.Assert(err, IsNil)

	if test.targets != nil {
		_, err = db.Exec("INSERT INTO plugin (name) VALUES ('test')")
		c.Assert(err, IsNil)
		for _, t := range test.targets {
			if t.Account != "" {
				_, err = db.Exec("INSERT OR IGNORE INTO account (name) VALUES (?)", t.Account)
				c.Assert(err, IsNil)
			}
			_, err = db.Exec("INSERT INTO target (plugin,account,channel,nick) VALUES ('test',?,?,?)", t.Account, t.Channel, t.Nick)
			c.Assert(err, IsNil)
		}
	}

	tester.Start()
	if test.send != "" {
		tester.Sendf(test.send)
	}
	if test.sendAll != nil {
		tester.SendAll(test.sendAll)
	}
	tester.Stop()

	if test.recv != "" {
		c.Assert(tester.Recv(), Equals, test.recv)
	}

	if test.recvAll != nil {
		if len(test.recvAll) == 0 {
			c.Assert(tester.RecvAll(), IsNil)
		} else {
			c.Assert(tester.RecvAll(), DeepEquals, test.recvAll)
		}
	}
}
