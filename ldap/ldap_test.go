package ldap_test

import (
	"testing"

	. "gopkg.in/check.v1"
	"gopkg.in/niemeyer/mup.v0/ldap"
)

func Test(t *testing.T) { TestingT(t) }

var _ = Suite(&S{})

type S struct{}

// These tests use the LDAP server kindly made available by Forum Systems:
//
// http://www.forumsys.com/tutorials/integration-how-to/ldap/online-ldap-test-server

var settings = &ldap.Settings{
	LDAP:     "ldap.forumsys.com:389",
	BaseDN:   "dc=example,dc=com",
	BindDN:   "cn=read-only-admin,dc=example,dc=com",
	BindPass: "password",
}

func (s *S) TestSearch(c *C) {
	conn, err := ldap.Dial(settings)
	c.Assert(err, IsNil)

	search := &ldap.Search{
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
}

func (s *S) TestEscapeFilter(c *C) {
	c.Assert(ldap.EscapeFilter("a\x00b(c)d*e\\f"), Equals, `a\00b\28c\29d\2ae\5cf`)
	c.Assert(ldap.EscapeFilter("Lučić"), Equals, `Lu\c4\8di\c4\87`)
}
