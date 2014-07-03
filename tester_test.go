package mup_test

import (
	"testing"

	. "gopkg.in/check.v1"
	"gopkg.in/niemeyer/mup.v0"
	"labix.org/v2/mgo/bson"

	_ "gopkg.in/niemeyer/mup.v0/plugins/echo"
)

func Test(t *testing.T) { TestingT(t) }

var _ = Suite(&TesterSuite{})

type TesterSuite struct{}

func (s *TesterSuite) TestSendfRecv(c *C) {
	tester := mup.StartPluginTest("echo", nil)
	tester.Sendf("mup", "echo Hi %s", "there")
	c.Check(tester.Recv(), Equals, "PRIVMSG nick :Hi there")
	tester.Sendf("mup", "echo Hi %s", "again")
	c.Check(tester.Recv(), Equals, "PRIVMSG nick :Hi again")
	tester.Sendf("mup", "echo One")
	tester.Sendf("mup", "echo Two")
	tester.Sendf("mup", "echo Three")
	c.Check(tester.Recv(), Equals, "PRIVMSG nick :One")
	c.Check(tester.Recv(), Equals, "PRIVMSG nick :Two")
	tester.Stop()
	c.Check(tester.Recv(), Equals, "PRIVMSG nick :Three")
	c.Check(tester.Recv(), Equals, "")
}

func (s *TesterSuite) TestSettings(c *C) {
	tester := mup.StartPluginTest("echo", bson.M{"command": "myecho"})
	tester.Sendf("mup", "myecho Hi there")
	tester.Stop()
	c.Assert(tester.Recv(), Equals, "PRIVMSG nick :Hi there")
}

func (s *TesterSuite) TestStop(c *C) {
	tester := mup.StartPluginTest("echo", nil)
	tester.Stop()
	err := tester.Sendf("mup", "echo Hi there")
	c.Assert(err, ErrorMatches, "plugin stopped")
}

func (s *TesterSuite) TestSendRecvAll(c *C) {
	tester := mup.StartPluginTest("echo", nil)
	tester.SendAll("mup", []string{"echo One", "echo Two"})
	c.Assert(tester.RecvAll(), DeepEquals, []string{"PRIVMSG nick :One", "PRIVMSG nick :Two"})
	c.Assert(tester.RecvAll(), IsNil)
	tester.Sendf("mup", "echo Three")
	tester.Stop()
	c.Assert(tester.RecvAll(), DeepEquals, []string{"PRIVMSG nick :Three"})
	c.Assert(tester.RecvAll(), IsNil)
}

func (s *TesterSuite) TestSendError(c *C) {
	tester := mup.StartPluginTest("echo", bson.M{"error": "Error message."})
	err := tester.Sendf("mup", "echo foo")
	tester.Stop()
	c.Assert(err, ErrorMatches, "Error message.")
}
