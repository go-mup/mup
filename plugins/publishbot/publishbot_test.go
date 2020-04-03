package publishbot_test

import (
	"net"
	"testing"
	"time"

	"gopkg.in/mup.v0"
	_ "gopkg.in/mup.v0/plugins/publishbot"

	. "gopkg.in/check.v1"
)

func Test(t *testing.T) { TestingT(t) }

var _ = Suite(&PBotSuite{})

type PBotSuite struct{}

func (s *PBotSuite) SetUpSuite(c *C) {
	mup.SetLogger(c)
	mup.SetDebug(true)
}

func (s *PBotSuite) TearDownSuite(c *C) {
	mup.SetLogger(nil)
	mup.SetDebug(false)
}

func (s *PBotSuite) TestPublishBot(c *C) {
	tester := mup.NewPluginTester("publishbot")
	tester.SetConfig(mup.Map{"addr": ":10423"})
	tester.SetTargets([]mup.Target{
		{Account: "one", Channel: "#one", Config: `{"accept": ["pass:#one"]}`},
		{Account: "two", Channel: "#two", Config: `{"accept": ["pass:#one", "pass:#two"]}`},
	})
	tester.Start()
	time.Sleep(500 * time.Millisecond)
	defer tester.Stop()

	conn, err := net.DialTimeout("tcp", "localhost:10423", 1*time.Second)
	c.Assert(err, IsNil)
	_, err = conn.Write([]byte("pass:#one:A\n"))
	_, err = conn.Write([]byte("nono:#one:B\n"))
	_, err = conn.Write([]byte("pass:#huh:C\n"))
	_, err = conn.Write([]byte("pass:#two:D\n"))
	c.Assert(err, IsNil)
	err = conn.Close()
	c.Assert(err, IsNil)

	tester.Stop()

	c.Assert(tester.RecvAll(), DeepEquals, []string{"[@one] PRIVMSG #one :A", "[@two] PRIVMSG #two :A", "[@two] PRIVMSG #two :D"})
}
