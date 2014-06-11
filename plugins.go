package mup

import (
	"strings"
)

var registeredPlugins = map[string]func(*Plugger) Plugin{
	"echo": newEchoPlugin,
	"ldap": newLdapPlugin,
}

type echoPlugin struct {
	plugger  *Plugger
	prefix   string
	settings struct {
		Command string
	}
}

func newEchoPlugin(plugger *Plugger) Plugin {
	p := &echoPlugin{plugger: plugger, prefix: "echo "}
	plugger.Settings(&p.settings)
	if p.settings.Command != "" {
		p.prefix = p.settings.Command + " "
	}
	return p
}

func (p *echoPlugin) Stop() error {
	return nil
}

func (p *echoPlugin) Handle(msg *Message) error {
	if !msg.ToMup || !strings.HasPrefix(msg.MupText, p.prefix) {
		return nil
	}
	return p.plugger.Replyf(msg, "%s", strings.TrimSpace(msg.MupText[len(p.prefix):]))
}
