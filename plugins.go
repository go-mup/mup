package mup

import (
	"strings"
)

var registeredPlugins = map[string]func(*Plugger) Plugin{
	"echo": newEchoPlugin,
}

type echoPlugin struct {
	plugger *Plugger
}

func newEchoPlugin(plugger *Plugger) Plugin {
	return &echoPlugin{plugger}
}

func (p *echoPlugin) Start() error { return nil }
func (p *echoPlugin) Stop() error  { return nil }
func (p *echoPlugin) Handle(msg *Message) error {
	if !msg.ToMup || !strings.HasPrefix(msg.MupText, "echo ") {
		return nil
	}
	return p.plugger.Replyf(msg, "%s", strings.TrimSpace(msg.MupText[5:]))
}
