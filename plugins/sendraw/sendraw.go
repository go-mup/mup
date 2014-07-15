package sendraw

import (
	"strings"

	"gopkg.in/niemeyer/mup.v0"
)

var Plugin = mup.PluginSpec{
	Name:  "sendraw",
	Help:  `Exposes the sendraw command for raw IRC message sending.

	This is an administration tool, and must be enabled with great care. People
	with access can have the bot communicating arbitrarily with the server.
	`,
	Start: startPlugin,
}

func init() {
	mup.RegisterPlugin(&Plugin)
}

type sendrawPlugin struct {
	plugger *mup.Plugger
	prefix  string
	stopped bool
	config  struct {
		Command string
		Error   string
	}
}

func startPlugin(plugger *mup.Plugger) (mup.Stopper, error) {
	p := &sendrawPlugin{plugger: plugger, prefix: "sendraw "}
	plugger.Config(&p.config)
	if p.config.Command != "" {
		p.prefix = p.config.Command + " "
	}
	return p, nil
}

func (p *sendrawPlugin) Stop() error {
	return nil
}

func (p *sendrawPlugin) HandleMessage(msg *mup.Message) error {
	if !msg.ToMup || !strings.HasPrefix(msg.MupText, p.prefix) {
		return nil
	}
	text := strings.TrimLeft(msg.MupText[len(p.prefix):], " ")
	i := strings.Index(text, " ")
	if i < 1 {
		p.plugger.Sendf(msg, "Usage: sendraw <account> <raw IRC message>")
		return nil
	}
	p.plugger.Send(mup.ParseOutgoing(text[:i], strings.TrimLeft(text[i+1:], " ")))
	return p.plugger.Sendf(msg, "Done.")
}
