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
	send   string
	recv   string
	config interface{}
}

var echoTests = []echoTest{{
	send: "echo repeat",
	recv: "PRIVMSG nick :repeat",
}, {
	send: "echo",
	recv: "PRIVMSG nick :Oops: missing input for argument: text",
}, {
	send:   "echo repeat",
	recv:   "PRIVMSG nick :[prefix]repeat",
	config: bson.M{"prefix": "[prefix]"},
}, {
	send: "[#chan] mup: echo repeat",
	recv: "PRIVMSG #chan :nick: repeat",
}, {
	send: "[#chan] echo repeat",
	recv: "",
}}

func (s *EchoSuite) TestEcho(c *C) {
	for i, test := range echoTests {
		c.Logf("Testing message #%d: %s", i, test.send)
		tester := mup.NewPluginTester("echo")
		tester.SetConfig(test.config)
		tester.Start()
		tester.Sendf(test.send)
		tester.Stop()
		c.Assert(tester.Recv(), Equals, test.recv)
	}
}
