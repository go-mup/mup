package sendraw

import (
	"gopkg.in/mup.v0"
	"gopkg.in/mup.v0/schema"
)

var Plugin = mup.PluginSpec{
	Name: "sendraw",
	Help: `Exposes the sendraw command for raw IRC message sending.

	This is an administration tool, and must be enabled with great care. People
	with access can have the bot communicating arbitrarily with the server.
	`,
	Start:    start,
	Commands: Commands,
}

var Commands = schema.Commands{{
	Name: "sendraw",
	Help: `Sends the provided text as a raw IRC protocol message.
	
	If an account name is not provided, it defaults to the current one.
	`,
	Args: schema.Args{{
		Name: "-account",
		Type: schema.String,
	}, {
		Name: "text",
		Type: schema.String,
		Flag: schema.Required | schema.Trailing,
	}},
}}

func init() {
	mup.RegisterPlugin(&Plugin)
}

type sendrawPlugin struct {
	plugger *mup.Plugger
}

func start(plugger *mup.Plugger) mup.Stopper {
	return &sendrawPlugin{plugger: plugger}
}

func (p *sendrawPlugin) Stop() error {
	return nil
}

func (p *sendrawPlugin) HandleCommand(cmd *mup.Command) {
	var args struct{ Account, Text string }
	cmd.Args(&args)
	if args.Account == "" {
		args.Account = cmd.Account
	}
	p.plugger.Send(mup.ParseOutgoing(args.Account, args.Text))
	p.plugger.Sendf(cmd, "Done.")
}
