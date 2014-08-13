package log_test

import (
	"testing"

	. "gopkg.in/check.v1"
	"gopkg.in/mgo.v2"
	"gopkg.in/mup.v0"
	_ "gopkg.in/mup.v0/plugins/log"
)

func Test(t *testing.T) { TestingT(t) }

var _ = Suite(&HelpSuite{})

type HelpSuite struct {
	dbserver mup.DBServerHelper
}

func (s *HelpSuite) SetUpSuite(c *C) {
	s.dbserver.SetPath(c.MkDir())
}

func (s *HelpSuite) TearDownSuite(c *C) {
	s.dbserver.Stop()
}

func (s *HelpSuite) TearDownTest(c *C) {
	s.dbserver.Wipe()
}

type logTest struct {
	send     string
	outgoing string
	stored   string
}

var logTests = []logTest{{
	send:   "Text.",
	stored: ":nick!~user@host PRIVMSG mup :Text.",
}, {
	outgoing: "Text.",
	stored:   "PRIVMSG nick :Text.",
}}

func (s *HelpSuite) TestLog(c *C) {
	for _, test := range logTests {
		session := s.dbserver.Session()
		defer session.Close()
		db := session.DB("")

		tester := mup.NewPluginTester("log")
		tester.SetDatabase(db)
		tester.Start()
		if test.send != "" {
			tester.Sendf(test.send)
		}
		if test.outgoing != "" {
			tester.Plugger().Send(&mup.Message{Account: "test", Nick: "nick", Text: test.outgoing})
		}
		tester.Stop()

		var msg mup.Message
		var dbname = db.Name + "_bulk"
		coll := session.DB(dbname).C("shared.log")
		err := coll.Find(nil).One(&msg)
		if err == mgo.ErrNotFound {
			c.Fatalf(`Collection "shared.log" in database %q is empty.`, dbname)
		}
		c.Assert(err, IsNil)
		c.Assert(msg.String(), Equals, test.stored)

		session.Close()
		s.dbserver.Wipe()
	}
}
