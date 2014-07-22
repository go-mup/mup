package mup

import (
	"gopkg.in/mup.v0/ldap"
)

func NewPlugger(name string, send func(msg *Message) error, ldap func(name string) (ldap.Conn, error), config, targets interface{}) *Plugger {
	p := newPlugger(name, send, ldap)
	p.setConfig(marshalRaw(config))
	p.setTargets(marshalRaw(targets))
	return p
}
