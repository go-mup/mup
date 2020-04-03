package echo

import (
	"gopkg.in/mup.v0"
	"gopkg.in/mup.v0/schema"
)

var Plugin = mup.PluginSpec{
	Name:     "echo",
	Help:     "Exposes a trivial echo command.",
	Start:    start,
	Commands: Commands,
}

var Commands = schema.Commands{{
	Name: "echo",
	Help: "Repeats the provided text back at you.",
	Args: schema.Args{{
		Name: "text",
		Flag: schema.Trailing | schema.Required,
	}},
}}

func init() {
	mup.RegisterPlugin(&Plugin)
}

type echoPlugin struct {
	plugger *mup.Plugger
	config  struct {
		Prefix string
	}
}

func start(plugger *mup.Plugger) mup.Stopper {
	p := &echoPlugin{plugger: plugger}
	err := plugger.UnmarshalConfig(&p.config)
	if err != nil {
		plugger.Logf("%v", err)
	}
	return p
}

func (p *echoPlugin) Stop() error {
	return nil
}

func (p *echoPlugin) HandleCommand(cmd *mup.Command) {
	var args struct{ Text string }
	cmd.Args(&args)
	if p.config.Prefix != "" {
		p.plugger.Sendf(cmd, "%s%s", p.config.Prefix, args.Text)
	}
	p.plugger.Sendf(cmd, "%s", args.Text)
}
