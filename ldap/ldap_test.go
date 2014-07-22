package ldap_test

import (
	"fmt"
	"testing"

	. "gopkg.in/check.v1"
	"gopkg.in/mup.v0/ldap"
)

func Test(t *testing.T) { TestingT(t) }

var _ = Suite(&S{})

type S struct{}

// These tests use the LDAP server kindly made available by Forum Systems:
//
// http://www.forumsys.com/tutorials/integration-how-to/ldap/online-ldap-test-server

var config = &ldap.Config{
	URL:      "ldap.forumsys.com:389",
	BaseDN:   "dc=example,dc=com",
	BindDN:   "cn=read-only-admin,dc=example,dc=com",
	BindPass: "password",
}

func (s *S) TestSearch(c *C) {
	conn, err := ldap.Dial(config)
	c.Assert(err, IsNil)
	defer conn.Close()

	var search = &ldap.Search{
		Filter: "(|(uid=einstein)(uid=tesla))",
		Attrs:  []string{"cn", "mail", "objectClass"},
	}
	results, err := conn.Search(search)
	c.Assert(err, IsNil)
	for _, result := range results {
		switch result.DN {
		case "uid=einstein,dc=example,dc=com":
			c.Assert(result.Value("cn"), Equals, "Albert Einstein")
			c.Assert(result.Value("mail"), Equals, "einstein@ldap.forumsys.com")
			c.Assert(result.Value("objectClass"), Equals, "inetOrgPerson")
			c.Assert(result.Values("objectClass"), DeepEquals, []string{"inetOrgPerson", "organizationalPerson", "person", "top"})
			c.Assert(result.Attrs, HasLen, 3)
		case "uid=tesla,dc=example,dc=com":
			c.Assert(result.Value("cn"), Equals, "Nikola Tesla")
			c.Assert(result.Value("mail"), Equals, "tesla@ldap.forumsys.com")
			c.Assert(result.Value("objectClass"), Equals, "inetOrgPerson")
			c.Assert(result.Values("objectClass"), DeepEquals, []string{"inetOrgPerson", "organizationalPerson", "person", "top"})
			c.Assert(result.Attrs, HasLen, 3)
		default:
			c.Fatalf("Unexpected result: %#v", result)
		}
	}
	c.Assert(results, HasLen, 2)
	c.Assert(conn.Close(), IsNil)
}

type ldapConn struct {
	config *ldap.Config
	search *ldap.Search
	closed bool
	fail   bool
}

func (c *ldapConn) Search(s *ldap.Search) ([]ldap.Result, error) {
	c.search = s
	if c.fail {
		return nil, fmt.Errorf("test-error")
	}
	return []ldap.Result{{DN: "test-dn"}}, nil
}

func (c *ldapConn) Close() error {
	if c.closed {
		panic("closed twice")
	}
	c.closed = true
	return nil
}

func (s *S) TestManaged(c *C) {
	conns := make([]*ldapConn, 0, 3)
	dials := 0
	ldap.TestDial = func(c *ldap.Config) (ldap.Conn, error) {
		dials++
		if dials == 1 {
			return nil, fmt.Errorf("temporary error")
		}
		conn := &ldapConn{config: c}
		if dials == 2 {
			conn.fail = true
		}
		conns = append(conns, conn)
		return conn, nil
	}
	defer func() {
		ldap.TestDial = nil
	}()

	mconn := ldap.DialManaged(config)

	conn := mconn.Conn()
	defer conn.Close()
	res, err := conn.Search(&ldap.Search{Filter: "test-filter1"})
	c.Assert(err, ErrorMatches, "test-error")
	c.Assert(res, IsNil)

	c.Assert(conn.Close(), IsNil)
	c.Assert(conn.Close(), IsNil)

	_, err = conn.Search(&ldap.Search{})
	c.Assert(err, ErrorMatches, "LDAP connection already closed")

	conn = mconn.Conn()
	defer conn.Close()

	c.Assert(mconn.Close(), IsNil)
	c.Assert(mconn.Close(), IsNil)

	res, err = conn.Search(&ldap.Search{Filter: "test-filter2"})
	c.Assert(err, IsNil)
	c.Assert(res, HasLen, 1)
	c.Assert(res[0].DN, Equals, "test-dn")

	c.Assert(conn.Close(), IsNil)

	c.Assert(func() { mconn.Conn() }, PanicMatches, "ManagedConn.Conn called after closing connection")

	c.Assert(conns, HasLen, 2)
	c.Assert(conns[0].closed, Equals, true)
	c.Assert(conns[0].config, DeepEquals, config)
	c.Assert(conns[0].search.Filter, Equals, "test-filter1")
	c.Assert(conns[1].closed, Equals, true)
	c.Assert(conns[1].config, DeepEquals, config)
	c.Assert(conns[1].search.Filter, Equals, "test-filter2")
}

func (s *S) TestEscapeFilter(c *C) {
	c.Assert(ldap.EscapeFilter("a\x00b(c)d*e\\f"), Equals, `a\00b\28c\29d\2ae\5cf`)
	c.Assert(ldap.EscapeFilter("Lučić"), Equals, `Lu\c4\8di\c4\87`)
}
