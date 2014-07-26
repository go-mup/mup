package log

import (
	"gopkg.in/mup.v0"
	"gopkg.in/mgo.v2"
)

var Plugin = mup.PluginSpec{
	Name:  "log",
	Help:  `Stores observed messages persistently.
	
	Messages are stored in the collection "shared.log.<account name>".
	If "database" is provided in the configuration, it will be used for
	these collections. Otherwise, they go into the default one.
	`,
	Start: start,
}

func init() {
	mup.RegisterPlugin(&Plugin)
}

type logPlugin struct {
	plugger *mup.Plugger
	config  struct {
		Database string
	}
}

func start(plugger *mup.Plugger) mup.Stopper {
	p := &logPlugin{
		plugger: plugger,
	}
	plugger.Config(&p.config)
	return p
}

func (p *logPlugin) Stop() error {
	return nil
}

func (p *logPlugin) HandleMessage(msg *mup.Message) {
	var c *mgo.Collection
	var session *mgo.Session
	session, c = p.plugger.SharedCollection("log." + msg.Account)
	defer session.Close()
	if p.config.Database != "" {
		c = session.DB(p.config.Database).C(c.Name)
	}
	if err := c.Insert(msg); err != nil {
		p.plugger.Logf("Error writing to log collection: %v", err)
	}
}
