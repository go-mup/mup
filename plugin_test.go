package mup_test

import (
	"errors"
	"fmt"

	. "gopkg.in/check.v1"
	"gopkg.in/niemeyer/mup.v0"
	"gopkg.in/niemeyer/mup.v0/schema"
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
		tester := mup.NewTest("echoA")
		tester.SetConfig(test.config)
		tester.Start()
		tester.Sendf(test.target, test.send)
		tester.Stop()
		c.Assert(tester.Recv(), Equals, test.recv)
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
	stopped bool
	config  struct {
		Prefix  string
		Error   string
	}
}

func pluginStart(plugger *mup.Plugger) (mup.Stopper, error) {
	p := &testPlugin{plugger: plugger}
	plugger.Config(&p.config)
	return p, nil
}

func (p *testPlugin) Stop() error {
	p.stopped = true
	return nil
}

func (p *testPlugin) HandleMessage(msg *mup.Message) error {
	if p.stopped {
		return fmt.Errorf("[msg] plugin stopped")
	}
	if p.config.Error != "" {
		return errors.New("[msg] " + p.config.Error)
	}
	prefix := p.plugger.Name() + "msg "
	if strings.HasPrefix(msg.MupText, prefix) {
		return p.echo(msg, "[msg] ", msg.MupText[len(prefix):])
	}
	return nil
}

func (p *testPlugin) HandleCommand(cmd *mup.Command) error {
	if p.stopped {
		return fmt.Errorf("[cmd] plugin stopped")
	}
	if p.config.Error != "" {
		return errors.New("[cmd] " + p.config.Error)
	}
	var args struct{ Text string }
	cmd.Args(&args)
	return p.echo(cmd, "[cmd] ", args.Text)
}

func (p *testPlugin) echo(to mup.Addressable, prefix, text string) error {
	if p.config.Prefix != "" {
		prefix += p.config.Prefix
	}
	return p.plugger.Sendf(to, "%s%s", prefix, text)
}
