package mup

import (
	"gopkg.in/mgo.v2"
	"gopkg.in/mup.v0/ldap"
)

func NewPlugger(name string, db *mgo.Database, send, handle func(msg *Message) error, ldap func(name string) (ldap.Conn, error), config, targets interface{}) *Plugger {
	p := newPlugger(name, send, handle, ldap)
	p.setDatabase(db)
	p.setConfig(marshalRaw(config))
	p.setTargets(marshalRaw(targets))
	return p
}
