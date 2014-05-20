package mup

import (
	//"labix.org/v2/mgo/bson"
	. "gopkg.in/check.v1"
)

type BridgeSuite struct {
	LineServerSuite
	MgoSuite
	Bridge *Bridge
}

var _ = Suite(&BridgeSuite{})

func (s *BridgeSuite) SetUpSuite(c *C) {
	s.LineServerSuite.SetUpSuite(c)
	s.MgoSuite.SetUpSuite(c)
}

func (s *BridgeSuite) TearDownSuite(c *C) {
	s.LineServerSuite.TearDownSuite(c)
	s.MgoSuite.TearDownSuite(c)
}

func (s *BridgeSuite) SetUpTest(c *C) {
	s.MgoSuite.SetUpTest(c)

	SetDebug(true)
	SetLogger(c)

	config := &BridgeConfig{
		Database: "localhost:50017/mup",
	}

	err := s.Session.DB("").C("servers").Insert(M{
		"name": "testserver",
		"host": s.Addr.String(),
		"password": "password",
	})
	c.Assert(err, IsNil)

	s.Bridge, err = StartBridge(config)
	c.Assert(err, IsNil)

	c.Assert(s.ReadLine(), Equals, "PASS password")
	c.Assert(s.ReadLine(), Equals, "NICK mup")
	c.Assert(s.ReadLine(), Equals, "USER mup 0 0 :Mup Pet")
}

func (s *BridgeSuite) TearDownTest(c *C) {
	s.Bridge.Stop()

	s.LineServerSuite.TearDownTest(c)
	s.MgoSuite.TearDownTest(c)
}

func (s *BridgeSuite) TestConnect(c *C) {
	// SetUpTest does it all.
}

func (s *BridgeSuite) TestNickInUse(c *C) {
	s.SendLine(":n.net 433 * mup :Nickname is already in use.")
	c.Assert(s.ReadLine(), Equals, "NICK mup_")
	s.SendLine(":n.net 433 * mup_ :Nickname is already in use.")
	c.Assert(s.ReadLine(), Equals, "NICK mup__")
}

func (s *BridgeSuite) TestPingPong(c *C) {
	s.SendLine("PING :foo")
	c.Assert(s.ReadLine(), Equals, "PONG :foo")
}

func (s *BridgeSuite) TestPingPongPostAuth(c *C) {
	s.SendLine(":n.net 001 mynick :Welcome!")
	s.SendLine("PING :foo")
	c.Assert(s.ReadLine(), Equals, "PONG :foo")
}

func (s *BridgeSuite) TestJoinChannel(c *C) {
	s.SendLine(":n.net 001 mup :Welcome!")

	servers := s.Session.DB("").C("servers")
	err := servers.Update(
		M{"name": "testserver"},
		M{"$set": M{"channels": []M{{"name": "#c1"}, {"name": "#c2"}}}},
	)
	c.Assert(err, IsNil)

	s.Bridge.Refresh()
	c.Assert(s.ReadLine(), Equals, "JOIN #c1,#c2")

	// Confirm it joined both channels.
	s.SendLine(":mup!~mup@10.0.0.1 JOIN #c1")
	s.SendLine(":mup!~mup@10.0.0.1 JOIN #c2")

	err = servers.Update(
		M{"name": "testserver"},
		M{"$set": M{"channels": []M{{"name": "#c1"}, {"name": "#c3"}}}},
	)
	c.Assert(err, IsNil)

	s.Bridge.Refresh()
	c.Assert(s.ReadLine(), Equals, "JOIN #c3")
	c.Assert(s.ReadLine(), Equals, "PART #c2")

	// Do not confirm, forcing it to retry.
	s.Bridge.Refresh()
	c.Assert(s.ReadLine(), Equals, "JOIN #c3")
	c.Assert(s.ReadLine(), Equals, "PART #c2")
}
