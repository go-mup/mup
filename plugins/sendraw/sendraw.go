package sendraw

import (
	"gopkg.in/niemeyer/mup.v0"
	"gopkg.in/niemeyer/mup.v0/schema"
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
	Help: "Send the provided text as a raw IRC protocol messagae.",
	Args: schema.Args{{
		Name: "-account",
		Help: "Account to send the message to. Defaults to the current one.",
		Type: schema.String,
	}, {
		Name: "message",
		Help: "Raw IRC message to send.",
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

func start(plugger *mup.Plugger) (mup.Stopper, error) {
	return &sendrawPlugin{plugger: plugger}, nil
}

func (p *sendrawPlugin) Stop() error {
	return nil
}

func (p *sendrawPlugin) HandleCommand(cmd *mup.Command) error {
	var args struct{ Account, Message string }
	cmd.Args(&args)
	if args.Account == "" {
		args.Account = cmd.Account
	}
	p.plugger.Send(mup.ParseOutgoing(args.Account, args.Message))
	return p.plugger.Sendf(cmd, "Done.")
}
