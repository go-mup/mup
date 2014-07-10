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
		"sendraw foo",
		[]string{"PRIVMSG nick :Usage: sendraw <account> <raw IRC message>"},
	}, {
		"sendraw one NOTICE foo :Heya!",
		[]string{"NOTICE foo :Heya!", "PRIVMSG nick :Done."},
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
