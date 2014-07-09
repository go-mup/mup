package echo_test

import (
	"testing"

	. "gopkg.in/check.v1"
	_ "gopkg.in/niemeyer/mup.v0/plugins/echo"
	"gopkg.in/niemeyer/mup.v0"

	"labix.org/v2/mgo/bson"
)

func Test(t *testing.T) { TestingT(t) }

var _ = Suite(&EchoSuite{})

type EchoSuite struct{}

type echoTest struct {
	target   string
	send     string
	recv     string
	settings interface{}
}

var echoTests = []echoTest{
	{"mup", "echo repeat", "PRIVMSG nick :repeat", nil},
	{"mup", "notecho repeat", "", nil},
	{"mup", "echonospace", "", nil},
	{"mup", "myecho hi", "PRIVMSG nick :hi", bson.M{"command": "myecho"}},
	{"#channel", "mup: echo repeat", "PRIVMSG #channel :nick: repeat", nil},
	{"#channel", "echo repeat", "", nil},
}

func (s *EchoSuite) TestEcho(c *C) {
	for i, test := range echoTests {
		c.Logf("Testing message #%d: %s", i, test.send)
		tester := mup.NewTest("echo")
		tester.SetSettings(test.settings)
		tester.Start()
		tester.Sendf(test.target, test.send)
		tester.Stop()
		c.Assert(tester.Recv(), Equals, test.recv)
	}
}
