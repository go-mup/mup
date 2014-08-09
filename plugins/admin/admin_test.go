package admin_test

import (
	"testing"

	. "gopkg.in/check.v1"
	"gopkg.in/mup.v0"
	_ "gopkg.in/mup.v0/plugins/admin"
)

func Test(t *testing.T) { TestingT(t) }

var _ = Suite(&SendRawSuite{})

type SendRawSuite struct{}

type adminTest struct {
	send []string
	recv []string
}

var adminTests = []adminTest{
	{
		[]string{"sendraw"},
		[]string{"PRIVMSG nick :Oops: missing input for argument: text"},
	}, {
		[]string{"sendraw NOTICE foo :text"},
		[]string{"NOTICE foo :text", "PRIVMSG nick :Done."},
	}, {
		[]string{"sendraw -account=other PRIVMSG bar :text"},
		[]string{"[other] PRIVMSG bar :text", "PRIVMSG nick :Done."},
	},
}

func (s *SendRawSuite) TestSendRaw(c *C) {
	for i, test := range adminTests {
		c.Logf("Testing message #%d: %s", i, test.send)
		tester := mup.NewPluginTester("admin")
		tester.Start()
		tester.SendAll("", test.send)
		tester.Stop()
		c.Assert(tester.RecvAll(), DeepEquals, test.recv)
	}
}
