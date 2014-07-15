package publishbot_test

import (
	"net"
	"testing"
	"time"

	. "gopkg.in/check.v1"
	"gopkg.in/niemeyer/mup.v0"
	_ "gopkg.in/niemeyer/mup.v0/plugins/publishbot"
	"labix.org/v2/mgo/bson"
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
	tester := mup.NewTest("publishbot")
	tester.SetConfig(bson.M{"addr": ":10423"})
	tester.SetTargets([]bson.M{
		{"account": "one", "channel": "#one", "config": bson.M{"accept": []string{"pass:#one"}}},
		{"account": "one", "channel": "#two", "config": bson.M{"accept": []string{"pass:#one", "pass:#two"}}},
	})
	tester.Start()
	time.Sleep(500 * time.Millisecond)
	defer tester.Stop()

	conn, err := net.DialTimeout("tcp", "localhost:10423", 1 * time.Second)
	c.Assert(err, IsNil)
	_, err = conn.Write([]byte("pass:#one:A\n"))
	_, err = conn.Write([]byte("nono:#one:B\n"))
	_, err = conn.Write([]byte("pass:#huh:C\n"))
	_, err = conn.Write([]byte("pass:#two:D\n"))
	c.Assert(err, IsNil)
	err = conn.Close()
	c.Assert(err, IsNil)

	tester.Stop()

	c.Assert(tester.RecvAll(), DeepEquals, []string{"PRIVMSG #one :A", "PRIVMSG #two :A", "PRIVMSG #two :D"})
}
