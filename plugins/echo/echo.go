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
	Args: schema.Args{{
		Name: "text",
		Help: "Text to echo back.",
		Flag: schema.Trailing | schema.Required,
	}},
}}

func init() {
	mup.RegisterPlugin(&Plugin)
}

type echoPlugin struct {
	plugger *mup.Plugger
	config  struct {
		Prefix  string
	}
}

func start(plugger *mup.Plugger) (mup.Stopper, error) {
	p := &echoPlugin{plugger: plugger}
	plugger.Config(&p.config)
	return p, nil
}

func (p *echoPlugin) Stop() error {
	return nil
}

func (p *echoPlugin) HandleCommand(cmd *mup.Command) error {
	var args struct{ Text string }
	cmd.Args(&args)
	if p.config.Prefix != "" {
		return p.plugger.Sendf(cmd, "%s%s", p.config.Prefix, args.Text)
	}
	return p.plugger.Sendf(cmd, "%s", args.Text)
}
