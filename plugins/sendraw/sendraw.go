package sendraw

import (
	"strings"

	"gopkg.in/niemeyer/mup.v0"
)

type sendrawPlugin struct {
	plugger *mup.Plugger
	prefix  string
	stopped bool
	config  struct {
		Command string
		Error   string
	}
}

func init() {
	mup.RegisterPlugin("sendraw", startPlugin)
}

func startPlugin(plugger *mup.Plugger) mup.Plugin {
	p := &sendrawPlugin{plugger: plugger, prefix: "sendraw "}
	plugger.Config(&p.config)
	if p.config.Command != "" {
		p.prefix = p.config.Command + " "
	}
	return p
}

func (p *sendrawPlugin) Stop() error {
	return nil
}

func (p *sendrawPlugin) Handle(msg *mup.Message) error {
	if !msg.ToMup || !strings.HasPrefix(msg.MupText, p.prefix) {
		return nil
	}
	text := strings.TrimLeft(msg.MupText[len(p.prefix):], " ")
	i := strings.Index(text, " ")
	if i < 1 {
		p.plugger.Replyf(msg, "Usage: sendraw <account> <raw IRC message>")
		return nil
	}
	raw := mup.ParseMessage("mup", "!", strings.TrimLeft(text[i+1:], " "))
	raw.Account = text[:i]
	p.plugger.Send(raw)
	return p.plugger.Replyf(msg, "Done.")
}
