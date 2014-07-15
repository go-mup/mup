package echo

import (
	"errors"
	"fmt"
	"strings"

	"gopkg.in/niemeyer/mup.v0"
)

var Plugin = mup.PluginSpec{
	Name:  "echo",
	Help:  "Exposes a trivial echo command.",
	Start: startPlugin,
}

func init() {
	mup.RegisterPlugin(&Plugin)
}

type echoPlugin struct {
	plugger *mup.Plugger
	prefix  string
	stopped bool
	config  struct {
		Command string
		Error   string
	}
}

func startPlugin(plugger *mup.Plugger) (mup.Stopper, error) {
	p := &echoPlugin{plugger: plugger, prefix: "echo "}
	plugger.Config(&p.config)
	if p.config.Command != "" {
		p.prefix = p.config.Command + " "
	}
	return p, nil
}

func (p *echoPlugin) Stop() error {
	p.stopped = true
	return nil
}

func (p *echoPlugin) HandleMessage(msg *mup.Message) error {
	if p.stopped {
		return fmt.Errorf("plugin stopped")
	}
	if !msg.ToMup || !strings.HasPrefix(msg.MupText, p.prefix) {
		return nil
	}
	if p.config.Error != "" {
		return errors.New(p.config.Error)
	}
	return p.plugger.Sendf(msg, "%s", strings.TrimSpace(msg.MupText[len(p.prefix):]))
}
