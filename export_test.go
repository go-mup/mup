package mup

import (
	"database/sql"

	"gopkg.in/mup.v0/ldap"
)

func NewPlugger(name string, db *sql.DB, send, handle func(msg *Message) error, ldap func(name string) (ldap.Conn, error), config, targets interface{}) *Plugger {
	p := newPlugger(name, send, handle, ldap)
	p.setDatabase(db)
	p.setConfig(marshalRaw(config))

	// FIXME Needs a better API than this.
	raw := marshalRaw(targets)
	var tinfos []targetInfo
	err := raw.Unmarshal(&tinfos)
	if err != nil {
		panic("NewPlugger cannot handle the targets format: " + err.Error())
	}
	p.setTargets(tinfos)

	return p
}
