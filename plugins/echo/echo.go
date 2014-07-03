package echo

import (
	"errors"
	"fmt"
	"strings"

	"gopkg.in/niemeyer/mup.v0"
)

type echoPlugin struct {
	plugger  *mup.Plugger
	prefix   string
	stopped  bool
	settings struct {
		Command string
		Error   string
	}
}

func init() {
	mup.RegisterPlugin("echo", startPlugin)
}

func startPlugin(plugger *mup.Plugger) mup.Plugin {
	p := &echoPlugin{plugger: plugger, prefix: "echo "}
	plugger.Settings(&p.settings)
	if p.settings.Command != "" {
		p.prefix = p.settings.Command + " "
	}
	return p
}

func (p *echoPlugin) Stop() error {
	p.stopped = true
	return nil
}

func (p *echoPlugin) Handle(msg *mup.Message) error {
	if p.stopped {
		return fmt.Errorf("plugin stopped")
	}
	if !msg.ToMup || !strings.HasPrefix(msg.MupText, p.prefix) {
		return nil
	}
	if p.settings.Error != "" {
		return errors.New(p.settings.Error)
	}
	return p.plugger.Replyf(msg, "%s", strings.TrimSpace(msg.MupText[len(p.prefix):]))
}
