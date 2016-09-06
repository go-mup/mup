package mup_test

import (
	"testing"

	. "gopkg.in/check.v1"
	"gopkg.in/mgo.v2/bson"
	"gopkg.in/mup.v0"
)

func Test(t *testing.T) { TestingT(t) }

var _ = Suite(&TesterSuite{})

type TesterSuite struct{}

func (s *TesterSuite) SetUpTest(c *C) {
	mup.SetLogger(c)
	mup.SetDebug(true)
}

func (s *TesterSuite) TearDownTest(c *C) {
	mup.SetLogger(nil)
	mup.SetDebug(false)
}

func (s *TesterSuite) TestSendfRecv(c *C) {
	tester := mup.NewPluginTester("echoA")
	tester.Start()
	tester.Sendf("echoAcmd <%s>", "repeat")
	c.Check(tester.Recv(), Equals, "PRIVMSG nick :[cmd] <repeat>")
	tester.Sendf("echoAcmd <%s>", "repeat again")
	c.Check(tester.Recv(), Equals, "PRIVMSG nick :[cmd] <repeat again>")
	tester.Sendf("echoAcmd one")
	tester.Sendf("echoAcmd two")
	tester.Sendf("echoAcmd three")
	c.Check(tester.Recv(), Equals, "PRIVMSG nick :[cmd] one")
	c.Check(tester.Recv(), Equals, "PRIVMSG nick :[cmd] two")
	tester.Stop()
	c.Check(tester.Recv(), Equals, "PRIVMSG nick :[cmd] three")
	c.Check(tester.Recv(), Equals, "")

	// Ensure the outgoing handler is being properly called.
	log := c.GetTestLog()
	c.Assert(log, Matches, `(?s).*\[echoA\] \[out\] \[cmd\] <repeat>.*`)
	c.Assert(log, Matches, `(?s).*\[echoA\] \[out\] \[cmd\] <repeat again>.*`)
}

func (s *TesterSuite) TestSendfTarget(c *C) {
	tester := mup.NewPluginTester("echoA")
	tester.Start()

	tester.Sendf("echoAcmd private")
	c.Check(tester.Recv(), Equals, "PRIVMSG nick :[cmd] private")

	tester.Sendf("[#chan] mup: echoAcmd channel")
	c.Check(tester.Recv(), Equals, "PRIVMSG #chan :nick: [cmd] channel")

	tester.Sendf("[@acct] echoAcmd account")
	c.Check(tester.Recv(), Equals, "[@acct] PRIVMSG nick :[cmd] account")

	tester.Sendf("[#chan@acct] mup: echoAcmd account")
	c.Check(tester.Recv(), Equals, "[@acct] PRIVMSG #chan :nick: [cmd] account")

	tester.Stop()
}

func (s *TesterSuite) TestSendfRaw(c *C) {
	tester := mup.NewPluginTester("echoA")
	tester.Start()
	tester.Sendf("[@,] echoAcmd <%s>", "repeat")
	c.Check(tester.Recv(), Equals, "PRIVMSG nick :[cmd] <repeat>")
	tester.Sendf("[,raw] :other!~other@host PRIVMSG mup :echoAcmd <%s>", "repeat")
	c.Check(tester.Recv(), Equals, "PRIVMSG other :[cmd] <repeat>")
	tester.Sendf("[@acct,raw] :other!~other@host PRIVMSG #chan :mup: echoAcmd <%s>", "repeat")
	c.Check(tester.Recv(), Equals, "[@acct] PRIVMSG #chan :other: [cmd] <repeat>")
	tester.Stop()
}

func (s *TesterSuite) TestUnknownPlugin(c *C) {
	c.Assert(func() { mup.NewPluginTester("unknown").Start() }, PanicMatches, `plugin not registered: "unknown"`)
}

func (s *TesterSuite) TestPlugger(c *C) {
	tester := mup.NewPluginTester("echoA")
	c.Assert(tester.Plugger().Name(), Equals, "echoA")
}

func (s *TesterSuite) TestPluginLabel(c *C) {
	tester := mup.NewPluginTester("echoA/label")
	tester.Start()
	c.Assert(tester.Plugger().Name(), Equals, "echoA/label")
	tester.Sendf("echoAcmd repeat")
	tester.Stop()
	c.Assert(tester.Recv(), Equals, "PRIVMSG nick :[cmd] repeat")
}

func (s *TesterSuite) TestConfig(c *C) {
	tester := mup.NewPluginTester("echoA")
	tester.SetConfig(bson.M{"prefix": "[prefix] "})
	tester.Start()
	tester.Sendf("echoAcmd repeat")
	tester.Stop()
	c.Assert(tester.Recv(), Equals, "PRIVMSG nick :[cmd] [prefix] repeat")
}

func (s *TesterSuite) TestTargets(c *C) {
	tester := mup.NewPluginTester("echoA")
	tester.SetTargets([]bson.M{{"account": "one", "channel": "#one"}})
	targets := tester.Plugger().Targets()
	c.Assert(targets, HasLen, 1)
	c.Assert(targets[0].Address(), Equals, mup.Address{Account: "one", Channel: "#one"})
}

func (s *TesterSuite) TestSendRecvAll(c *C) {
	tester := mup.NewPluginTester("echoA")
	tester.Start()
	tester.SendAll([]string{"echoAcmd One", "echoAcmd Two"})
	c.Assert(tester.RecvAll(), DeepEquals, []string{"PRIVMSG nick :[cmd] One", "PRIVMSG nick :[cmd] Two"})
	c.Assert(tester.RecvAll(), IsNil)
	tester.Sendf("echoAcmd Three")
	tester.Stop()
	c.Assert(tester.RecvAll(), DeepEquals, []string{"PRIVMSG nick :[cmd] Three"})
	c.Assert(tester.RecvAll(), IsNil)
}

func (s *TesterSuite) TestRecvOtherAccount(c *C) {
	tester := mup.NewPluginTester("echoA")
	tester.SetConfig(bson.M{"prefix": "[prefix] "})
	tester.Start()
	tester.Plugger().Send(&mup.Message{Account: "other", Channel: "#chan", Text: "text"})
	tester.Stop()
	c.Assert(tester.Recv(), Equals, "[@other] PRIVMSG #chan :text")
}

func (s *TesterSuite) TestHandleRecvIncoming(c *C) {
	tester := mup.NewPluginTester("echoA")
	tester.SetTargets([]bson.M{
		{"account": "test"},
		{"account": "one", "channel": "#one"},
		{"account": "two", "channel": "#two"},
	})
	tester.Start()
	p := tester.Plugger()
	p.Handle(&mup.Message{Account: "test", Channel: "mup", Text: "text"})
	p.Handle(&mup.Message{Account: "one", Channel: "#one", Text: "text"})
	p.Handle(&mup.Message{Account: "two", Channel: "#two", Text: "text"})
	tester.Stop()
	c.Assert(tester.RecvIncoming(), Equals, "PRIVMSG mup :text")
	c.Assert(tester.RecvIncoming(), Equals, "[@one] PRIVMSG #one :text")
	c.Assert(tester.RecvIncoming(), Equals, "[@two] PRIVMSG #two :text")
}

func (s *TesterSuite) TestStop(c *C) {
	tester := mup.NewPluginTester("echoA")
	tester.Start()
	tester.Stop()
	c.Assert(c.GetTestLog(), Matches, "(?s).*testPlugin.Stop called.*")
}

func (s *TesterSuite) TestSetLDAP(c *C) {
	conn := &ldapConn{}
	tester := mup.NewPluginTester("echoA")
	tester.SetLDAP("test", conn)
	res, err := tester.Plugger().LDAP("test")
	c.Assert(err, IsNil)
	c.Assert(res, Equals, conn)
	_, err = tester.Plugger().LDAP("unknown")
	c.Assert(err, ErrorMatches, `LDAP connection "unknown" not found`)
}
