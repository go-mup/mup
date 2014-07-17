package echo_test

import (
	"testing"

	. "gopkg.in/check.v1"
	"gopkg.in/mgo.v2/bson"
	"gopkg.in/mup.v0"
	_ "gopkg.in/mup.v0/plugins/echo"
)

func Test(t *testing.T) { TestingT(t) }

var _ = Suite(&EchoSuite{})

type EchoSuite struct{}

type echoTest struct {
	target string
	send   string
	recv   string
	config interface{}
}

var echoTests = []echoTest{
	{"mup", "echo repeat", "PRIVMSG nick :repeat", nil},
	{"mup", "echo", "PRIVMSG nick :Oops: missing input for argument: text", nil},
	{"mup", "echo repeat", "PRIVMSG nick :[prefix]repeat", bson.M{"prefix": "[prefix]"}},
	{"#channel", "mup: echo repeat", "PRIVMSG #channel :nick: repeat", nil},
	{"#channel", "echo repeat", "", nil},
}

func (s *EchoSuite) TestEcho(c *C) {
	for i, test := range echoTests {
		c.Logf("Testing message #%d: %s", i, test.send)
		tester := mup.NewPluginTester("echo")
		tester.SetConfig(test.config)
		tester.Start()
		tester.Sendf(test.target, test.send)
		tester.Stop()
		c.Assert(tester.Recv(), Equals, test.recv)
	}
}
