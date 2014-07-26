package log_test

import (
	"testing"

	. "gopkg.in/check.v1"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
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
	s.dbserver.Reset()
}

type logTest struct {
	send       string
	config     bson.M
	stored     string
	database   string
}

var logTests = []logTest{{
	send:       "Text.",
	stored:     ":nick!~user@host PRIVMSG mup :Text.",
	database:   "mup",
}, {
	send:       "Text.",
	config:     bson.M{"database": "other"},
	stored:     ":nick!~user@host PRIVMSG mup :Text.",
	database:   "other",
}}

func (s *HelpSuite) TestLog(c *C) {
	session := s.dbserver.Session()
	defer session.Close()

	db := session.DB("mup")

	for _, test := range logTests {
		tester := mup.NewPluginTester("log")
		tester.SetConfig(test.config)
		tester.SetDatabase(db)
		tester.Start()
		tester.Sendf("", test.send)
		tester.Stop()

		var msg mup.Message
		coll := session.DB(test.database).C("shared.log")
		err := coll.Find(nil).One(&msg)
		if err == mgo.ErrNotFound {
			c.Fatalf(`Collection "shared.log" in database %q is empty.`, test.database)
		}
		c.Assert(err, IsNil)
		c.Assert(msg.String(), Equals, test.stored)
	}
}
