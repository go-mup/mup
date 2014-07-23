package mup_test

import (

	. "gopkg.in/check.v1"
	"gopkg.in/mup.v0"
	"gopkg.in/mup.v0/schema"
	"gopkg.in/mgo.v2/bson"
	"strings"
)

var _ = Suite(&PluginSuite{})

type PluginSuite struct{}

type pluginTest struct {
	target string
	send   string
	recv   string
	config interface{}
}

var pluginTests = []pluginTest{
	{
		"mup",
		"unknown repeat",
		"",
		nil,
	},

	// Command.
	{
		"mup",
		"echoAcmd repeat",
		"PRIVMSG nick :[cmd] repeat",
		nil,
	}, {
		"mup",
		"echoAnospace",
		"",
		nil,
	}, {
		"mup",
		"echoAcmd",
		"PRIVMSG nick :Oops: missing input for argument: text",
		nil,
	}, {
		"mup",
		"echoAcmd repeat",
		"PRIVMSG nick :[cmd] [prefix] repeat",
		bson.M{"prefix": "[prefix] "},
	}, {
		"#channel",
		"mup: echoAcmd repeat",
		"PRIVMSG #channel :nick: [cmd] repeat",
		nil,
	}, {
		"#channel",
		"echoAcmd repeat",
		"",
		nil,
	},

	// Message.
	{
		"mup",
		"echoAmsg repeat",
		"PRIVMSG nick :[msg] repeat",
		nil,
	}, {
		"mup",
		"echoAmsg repeat",
		"PRIVMSG nick :[msg] [prefix] repeat",
		bson.M{"prefix": "[prefix] "},
	}, {
		"#channel",
		"mup: echoAmsg repeat",
		"PRIVMSG #channel :nick: [msg] repeat",
		nil,
	}, {
		"#channel",
		"echoAmsg repeat",
		"",
		nil,
	},
}

func (s *PluginSuite) TestPlugin(c *C) {
	for i, test := range pluginTests {
		c.Logf("Testing message #%d: %s", i, test.send)
		tester := mup.NewPluginTester("echoA")
		tester.SetConfig(test.config)
		tester.Start()
		tester.Sendf(test.target, test.send)
		tester.Stop()
		c.Assert(tester.Recv(), Equals, test.recv)
		c.Assert(tester.Recv(), Equals, "")
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
			Help: "Text to echo back.",
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
		Prefix  string
	}
}

func pluginStart(plugger *mup.Plugger) mup.Stopper {
	p := &testPlugin{plugger: plugger}
	plugger.Config(&p.config)
	return p
}

func (p *testPlugin) Stop() error {
	p.plugger.Logf("testPlugin.Stop called")
	return nil
}

func (p *testPlugin) HandleMessage(msg *mup.Message) {
	prefix := p.plugger.Name() + "msg "
	if strings.HasPrefix(msg.MupText, prefix) {
		p.echo(msg, "[msg] ", msg.MupText[len(prefix):])
	}
}

func (p *testPlugin) HandleCommand(cmd *mup.Command) {
	var args struct{ Text string }
	cmd.Args(&args)
	p.echo(cmd, "[cmd] ", args.Text)
}

func (p *testPlugin) echo(to mup.Addressable, prefix, text string) {
	if p.config.Prefix != "" {
		prefix += p.config.Prefix
	}
	p.plugger.Sendf(to, "%s%s", prefix, text)
}
