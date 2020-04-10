package log_test

import (
	"testing"

	. "gopkg.in/check.v1"
	"gopkg.in/mup.v0"
	"gopkg.in/mup.v0/plugins/log"
)

func Test(t *testing.T) { TestingT(t) }

var _ = Suite(&HelpSuite{})

type HelpSuite struct {
}

func (s *HelpSuite) SetUpTest(c *C) {
	mup.SetLogger(c)
	mup.SetDebug(true)
}

func (s *HelpSuite) TearDownTest(c *C) {
	mup.SetLogger(nil)
	mup.SetDebug(false)
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
		db, err := mup.OpenDB(c.MkDir())
		c.Assert(err, IsNil)
		defer db.Close()

		tester := mup.NewPluginTester("log")
		tester.SetDB(db)
		tester.Start()
		if test.send != "" {
			tester.Sendf(test.send)
		}
		if test.outgoing != "" {
			tester.Plugger().Send(&mup.Message{Account: "test", Nick: "nick", Text: test.outgoing})
		}
		tester.Stop()

		var msg mup.Message
		err = db.QueryRow("SELECT " + log.MessageColumns + " FROM log").Scan(log.MessageRefs(&msg)...)
		c.Assert(err, IsNil)
		c.Assert(msg.String(), Equals, test.stored)
	}
}
