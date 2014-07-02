package ldap

import (
	"fmt"
	"strings"

	"github.com/johnweldon/ldap"
)

type Settings struct {
	LDAP     string
	BaseDN   string
	BindDN   string
	BindPass string
}

type Conn interface {
	Close() error
	Ping() error
	Search(search *Search) ([]Result, error)
}

type Search struct {
	Filter string
	Attrs  []string
}

type Result struct {
	DN    string
	Attrs []Attr
}

type Attr struct {
	Name   string
	Values []string
}

func (r *Result) Values(name string) []string {
	for _, attr := range r.Attrs {
		if attr.Name == name {
			return attr.Values
		}
	}
	return nil
}

func (r *Result) Value(name string) string {
	values := r.Values(name)
	if len(values) > 0 {
		return values[0]
	}
	return ""
}

type ldapConn struct {
	conn   *ldap.Conn
	baseDN string
}

var TestDial func(*Settings) (Conn, error)

func Dial(settings *Settings) (Conn, error) {
	if TestDial != nil {
		return TestDial(settings)
	}
	var conn *ldap.Conn
	var err error
	if strings.HasPrefix(settings.LDAP, "ldaps://") {
		conn, err = ldap.DialTLS("tcp", settings.LDAP[8:], nil)
	} else if strings.HasPrefix(settings.LDAP, "ldap://") {
		conn, err = ldap.Dial("tcp", settings.LDAP[7:])
	} else {
		conn, err = ldap.Dial("tcp", settings.LDAP)
	}
	if err != nil {
		return nil, fmt.Errorf("cannot dial LDAP server: %v", err)
	}
	if err := conn.Bind(settings.BindDN, settings.BindPass); err != nil {
		conn.Close()
		s := strings.Replace(err.Error(), settings.BindPass, "********", -1)
		return nil, fmt.Errorf("cannot bind to LDAP server: %s", s)
	}
	return &ldapConn{conn, settings.BaseDN}, nil
}

func (c *ldapConn) Close() error {
	c.conn.Close()
	return nil
}

func (c *ldapConn) Ping() error {
	search := ldap.NewSearchRequest(
		c.baseDN,
		ldap.ScopeWholeSubtree,
		ldap.NeverDerefAliases,
		0, 0, false,
		"(mozillaNickname=this-query-is-just-a-ping)",
		[]string{"mozillaNickname"},
		nil,
	)
	_, err := c.conn.Search(search)
	return err
}

func (c *ldapConn) Search(s *Search) ([]Result, error) {
	search := ldap.NewSearchRequest(
		c.baseDN,
		ldap.ScopeWholeSubtree,
		ldap.NeverDerefAliases,
		0, 0, false,
		s.Filter,
		s.Attrs,
		nil,
	)
	result, err := c.conn.Search(search)
	if err != nil {
		return nil, fmt.Errorf("cannot search LDAP server: %v", err)
	}
	r := make([]Result, len(result.Entries))
	for ei, entry := range result.Entries {
		ri := &r[ei]
		ri.DN = entry.DN
		ri.Attrs = make([]Attr, len(entry.Attributes))
		for ai, attr := range entry.Attributes {
			ri.Attrs[ai] = Attr{attr.Name, attr.Values}
		}
	}
	return r, nil
}
