package sendraw_test

import (
	"testing"

	. "gopkg.in/check.v1"
	"gopkg.in/niemeyer/mup.v0"
	_ "gopkg.in/niemeyer/mup.v0/plugins/sendraw"
)

func Test(t *testing.T) { TestingT(t) }

var _ = Suite(&SendRawSuite{})

type SendRawSuite struct{}

type sendrawTest struct {
	send   string
	recv   []string
}

var sendrawTests = []sendrawTest{
	{
		"sendraw",
		[]string{"PRIVMSG nick :Oops: missing input for argument: message"},
	}, {
		"sendraw NOTICE foo :text",
		[]string{"NOTICE foo :text", "PRIVMSG nick :Done."},
	}, {
		"sendraw -account=other PRIVMSG bar :text",
		[]string{"[other] PRIVMSG bar :text", "PRIVMSG nick :Done."},
	},
}

func (s *SendRawSuite) TestSendRaw(c *C) {
	for i, test := range sendrawTests {
		c.Logf("Testing message #%d: %s", i, test.send)
		tester := mup.NewTest("sendraw")
		tester.Start()
		tester.Sendf("mup", test.send)
		tester.Stop()
		c.Assert(tester.RecvAll(), DeepEquals, test.recv)
	}
}
