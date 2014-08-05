package log

import (
	"gopkg.in/mup.v0"
)

var Plugin = mup.PluginSpec{
	Name:  "log",
	Help:  `Stores observed messages persistently.
	
	Messages are stored in the collection "shared.log", either in
	the main bot database, or in the database name defined via the
	"database" configuration option.
	`,
	Start: start,
}

func init() {
	mup.RegisterPlugin(&Plugin)
}

type logPlugin struct {
	plugger *mup.Plugger
}

func start(plugger *mup.Plugger) mup.Stopper {
	return &logPlugin{plugger: plugger}
}

func (p *logPlugin) Stop() error {
	return nil
}

func (p *logPlugin) HandleMessage(msg *mup.Message) {
	session, c := p.plugger.Collection("", mup.Shared|mup.Bulk)
	defer session.Close()
	err := c.Insert(msg)
	if err != nil {
		p.plugger.Logf("Error writing to log collection: %v", err)
	}
}
