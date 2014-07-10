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
	tester := mup.NewTest("echo")
	tester.Start()
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

func (s *TesterSuite) TestUnknownPlugin(c *C) {
	c.Assert(func() { mup.NewTest("unknown").Start() }, PanicMatches, `plugin not registered: "unknown"`)
}

func (s *TesterSuite) TestPlugger(c *C) {
	tester := mup.NewTest("echo")
	c.Assert(tester.Plugger().Name(), Equals, "echo")
}

func (s *TesterSuite) TestPluginLabel(c *C) {
	tester := mup.NewTest("echo:label")
	tester.Start()
	c.Assert(tester.Plugger().Name(), Equals, "echo:label")
	tester.Sendf("mup", "echo Hi there")
	tester.Stop()
	c.Assert(tester.Recv(), Equals, "PRIVMSG nick :Hi there")
}

func (s *TesterSuite) TestConfig(c *C) {
	tester := mup.NewTest("echo")
	tester.SetConfig(bson.M{"command": "myecho"})
	tester.Start()
	tester.Sendf("mup", "myecho Hi there")
	tester.Stop()
	c.Assert(tester.Recv(), Equals, "PRIVMSG nick :Hi there")
}

func (s *TesterSuite) TestTargets(c *C) {
	tester := mup.NewTest("echo")
	tester.SetTargets([]bson.M{{"account": "one", "target": "#one"}})
	targets := tester.Plugger().Targets()
	c.Assert(targets, HasLen, 1)
	c.Assert(targets[0].Account, Equals, "one")
	c.Assert(targets[0].Target, Equals, "#one")
}

func (s *TesterSuite) TestStop(c *C) {
	tester := mup.NewTest("echo")
	tester.Start()
	tester.Stop()
	err := tester.Sendf("mup", "echo Hi there")
	c.Assert(err, ErrorMatches, "plugin stopped")
}

func (s *TesterSuite) TestSendRecvAll(c *C) {
	tester := mup.NewTest("echo")
	tester.Start()
	tester.SendAll("mup", []string{"echo One", "echo Two"})
	c.Assert(tester.RecvAll(), DeepEquals, []string{"PRIVMSG nick :One", "PRIVMSG nick :Two"})
	c.Assert(tester.RecvAll(), IsNil)
	tester.Sendf("mup", "echo Three")
	tester.Stop()
	c.Assert(tester.RecvAll(), DeepEquals, []string{"PRIVMSG nick :Three"})
	c.Assert(tester.RecvAll(), IsNil)
}

func (s *TesterSuite) TestSendError(c *C) {
	tester := mup.NewTest("echo")
	tester.SetConfig(bson.M{"error": "Error message."})
	tester.Start()
	err := tester.Sendf("mup", "echo foo")
	tester.Stop()
	c.Assert(err, ErrorMatches, "Error message.")
}
