package log

import (
	"gopkg.in/mup.v0"
)

var Plugin = mup.PluginSpec{
	Name:  "log",
	Help:  `Stores observed messages persistently.`,
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
	db := p.plugger.DB()
	_, err := db.Exec("INSERT INTO log ("+messageColumns+") VALUES ("+messagePlacers+")", messageRefs(msg)...)
	if err != nil {
		p.plugger.Logf("Cannot insert message in log: %v", err)
	}
}

func (p *logPlugin) HandleOutgoing(msg *mup.Message) {
	p.HandleMessage(msg)
}

// TODO These were copied from message.go. We need a reasonable way of not duplicating that.
const messageColumns = "id,nonce,lane,time,account,channel,nick,user,host,command,param0,param1,param2,param3,text,bottext,bang,asnick"
const messagePlacers = "?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?"

func messageRefs(m *mup.Message) []interface{} {
	return []interface{}{&m.Id, &m.Nonce, &m.Lane, &m.Time, &m.Account, &m.Channel, &m.Nick, &m.User, &m.Host, &m.Command, &m.Param0, &m.Param1, &m.Param2, &m.Param3, &m.Text, &m.BotText, &m.Bang, &m.AsNick}
}
