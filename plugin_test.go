package mup_test

import (
	"fmt"
	"strings"

	. "gopkg.in/check.v1"
	"gopkg.in/mup.v0"
	"gopkg.in/mup.v0/schema"
)

var _ = Suite(&PluginSuite{})

type PluginSuite struct{}

func (s *PluginSuite) SetUpTest(c *C) {
	mup.SetLogger(c)
	mup.SetDebug(true)
}

func (s *PluginSuite) TearDownTest(c *C) {
	mup.SetLogger(nil)
	mup.SetDebug(false)
}

type pluginTest struct {
	send   string
	recv   string
	log    string
	config map[string]interface{}
}

var pluginTests = []pluginTest{
	{
		send: "unknown repeat",
		recv: "",
	},

	// Command.
	{
		send: "echoAcmd repeat",
		recv: "PRIVMSG nick :[cmd] repeat",
	}, {
		send: "echoAnospace",
		recv: "",
	}, {
		send: "echoAcmd",
		recv: "PRIVMSG nick :Oops: missing input for argument: text",
	}, {
		send:   "echoAcmd repeat",
		recv:   "PRIVMSG nick :[cmd] [prefix] repeat",
		config: map[string]interface{}{"prefix": "[prefix] "},
	}, {
		send: "[#chan] mup: echoAcmd repeat",
		recv: "PRIVMSG #chan :nick: [cmd] repeat",
	}, {
		send: "[#chan] echoAcmd repeat",
		recv: "",
	},

	// Message.
	{
		send: "echoAmsg repeat",
		recv: "PRIVMSG nick :[msg] repeat",
	}, {
		send:   "echoAmsg repeat",
		recv:   "PRIVMSG nick :[msg] [prefix] repeat",
		config: map[string]interface{}{"prefix": "[prefix] "},
	}, {
		send: "[#chan] mup: echoAmsg repeat",
		recv: "PRIVMSG #chan :nick: [msg] repeat",
	}, {
		send: "[#chan] echoAmsg repeat",
		recv: "",
	},

	// Outgoing.
	{
		send: "echoAmsg repeat",
		recv: "PRIVMSG nick :[msg] repeat",
		log:  "[out] [msg] repeat\n",
	},

	// Test Command.Name.
	{
		send:   "echoAcmd repeat",
		recv:   "PRIVMSG nick :[cmd:echoAcmd] repeat",
		config: map[string]interface{}{"showcmdname": true},
	},
}

func (s *PluginSuite) TestPlugin(c *C) {
	for i, test := range pluginTests {
		c.Logf("Testing message #%d: %s", i, test.send)
		tester := mup.NewPluginTester("echoA")
		tester.SetConfig(test.config)
		tester.Start()
		tester.Sendf(test.send)
		tester.Stop()
		c.Assert(tester.Recv(), Equals, test.recv)
		c.Assert(tester.Recv(), Equals, "")
		if log := c.GetTestLog(); test.log != "" && !strings.Contains(log, test.log) {
			c.Fatalf("Test log should contain %q, but consists of:\n%s", test.log, c.GetTestLog())
		}
	}
}

func pluginSpec(name string) *mup.PluginSpec {
	return &mup.PluginSpec{
		Name:     name,
		Help:     "Tests the hooking up of a real plugin.",
		Start:    pluginStart,
		Commands: pluginCommands(name + "cmd"),
	}
}

func pluginCommands(name string) schema.Commands {
	return schema.Commands{{
		Name: name,
		Args: schema.Args{{
			Name: "text",
			Flag: schema.Trailing | schema.Required,
		}},
	}}
}

func init() {
	for _, c := range "ABCD" {
		mup.RegisterPlugin(pluginSpec("echo" + string(c)))
	}
}

type testPlugin struct {
	plugger *mup.Plugger
	config  struct {
		Prefix      string
		ShowCmdName bool
	}
}

func pluginStart(plugger *mup.Plugger) mup.Stopper {
	p := &testPlugin{plugger: plugger}
	err := plugger.UnmarshalConfig(&p.config)
	if err != nil {
		panic(err)
	}
	return p
}

func (p *testPlugin) Stop() error {
	p.plugger.Logf("testPlugin.Stop called")
	return nil
}

func (p *testPlugin) HandleMessage(msg *mup.Message) {
	prefix := p.plugger.Name() + "msg "
	if strings.HasPrefix(msg.BotText, prefix) {
		p.echo(msg, "[msg] ", msg.BotText[len(prefix):])
	}
}

func (p *testPlugin) HandleCommand(cmd *mup.Command) {
	var args struct{ Text string }
	cmd.Args(&args)
	if p.config.ShowCmdName {
		p.echo(cmd, fmt.Sprintf("[cmd:%s] ", cmd.Name()), args.Text)
	} else {
		p.echo(cmd, "[cmd] ", args.Text)
	}
}

func (p *testPlugin) HandleOutgoing(msg *mup.Message) {
	p.plugger.Logf("[out] %s", msg.Text)
}

func (p *testPlugin) echo(to mup.Addressable, prefix, text string) {
	if p.config.Prefix != "" {
		prefix += p.config.Prefix
	}
	p.plugger.Sendf(to, "%s%s", prefix, text)
}
