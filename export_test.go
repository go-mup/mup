package mup

import (
	"database/sql"

	"gopkg.in/mup.v0/ldap"
)

func NewPlugger(name string, db *sql.DB, send, handle func(msg *Message) error, ldap func(name string) (ldap.Conn, error), config map[string]interface{}, targets []Target) *Plugger {
	p := newPlugger(name, send, handle, ldap)
	p.setDatabase(db)
	p.setConfig(marshalRaw(config))
	p.setTargets(targets)
	return p
}
