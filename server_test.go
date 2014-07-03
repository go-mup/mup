package mup

import (
	"time"

	. "gopkg.in/check.v1"
	"labix.org/v2/mgo"
	"strings"
)

type ServerSuite struct {
	LineServerSuite
	MgoSuite

	session *mgo.Session
	config  *Config
	server  *Server
	lserver *LineServer

	c *C
}

var _ = Suite(&ServerSuite{})

func (s *ServerSuite) SetUpSuite(c *C) {
	s.LineServerSuite.SetUpSuite(c)
	s.MgoSuite.SetUpSuite(c)
}

func (s *ServerSuite) TearDownSuite(c *C) {
	s.LineServerSuite.TearDownSuite(c)
	s.MgoSuite.TearDownSuite(c)
}

func (s *ServerSuite) SetUpTest(c *C) {
	s.LineServerSuite.SetUpTest(c)
	s.MgoSuite.SetUpTest(c)

	SetDebug(true)
	SetLogger(c)

	var err error
	s.session, err = mgo.Dial("localhost:50017")
	c.Assert(err, IsNil)

	s.config = &Config{
		Database: s.session.DB("mup"),
		Refresh:  -1, // Manual refreshing for testing.
	}

	err = s.config.Database.C("accounts").Insert(M{
		"name":     "testserver",
		"host":     s.Addr.String(),
		"password": "password",
	})
	c.Assert(err, IsNil)

	s.c = c
	s.RestartServer()
}

func (s *ServerSuite) TearDownTest(c *C) {
	s.StopServer()
	s.LineServerSuite.TearDownTest(c)
	s.session.Close()
	s.config.Database.Session.Close()
	s.MgoSuite.TearDownTest(c)
}

func (s *ServerSuite) StopServer() {
	if s.lserver != nil {
		s.lserver.Close()
		s.lserver = nil
	}
	if s.server != nil {
		s.server.Stop()
		s.server = nil
	}
}

func (s *ServerSuite) RestartServer() {
	s.StopServer()
	n := s.NextLineServer()
	var err error
	s.server, err = Start(s.config)
	s.c.Assert(err, IsNil)
	s.lserver = s.LineServer(n)
	s.ReadUser()
}

func (s *ServerSuite) ReadUser() {
	s.ReadLine("PASS password")
	s.ReadLine("NICK mup")
	s.ReadLine("USER mup 0 0 :Mup Pet")
}

func (s *ServerSuite) SendWelcome() {
	s.SendLine(":n.net 001 mup :Welcome!")
}

func (s *ServerSuite) Handshake() {
	s.ReadUser()
	s.SendWelcome()
}

func (s *ServerSuite) SendLine(line string) {
	s.lserver.SendLine(line)
}

func (s *ServerSuite) ReadLine(line string) {
	s.c.Assert(s.lserver.ReadLine(), Equals, line)

	// Confirm read message.
	if strings.HasPrefix(line, "PRIVMSG ") {
		ping := s.lserver.ReadLine()
		s.c.Assert(ping, Matches, "PING :sent:.*")
		s.lserver.SendLine("PONG " + ping[5:])
	}
}

func (s *ServerSuite) Roundtrip() {
	s.lserver.SendLine("PING :roundtrip")
	s.c.Assert(s.lserver.ReadLine(), Equals, "PONG :roundtrip")
}

func (s *ServerSuite) TestConnect(c *C) {
	// SetUpTest does it all.
}

func (s *ServerSuite) TestNickInUse(c *C) {
	s.SendLine(":n.net 433 * mup :Nickname is already in use.")
	s.ReadLine("NICK mup_")
	s.SendLine(":n.net 433 * mup_ :Nickname is already in use.")
	s.ReadLine("NICK mup__")
}

func (s *ServerSuite) TestPingPong(c *C) {
	s.SendLine("PING :foo")
	s.ReadLine("PONG :foo")
}

func (s *ServerSuite) TestPingPongPostAuth(c *C) {
	s.SendWelcome()
	s.SendLine("PING :foo")
	s.ReadLine("PONG :foo")
}

func (s *ServerSuite) TestQuit(c *C) {
	s.server.Stop()
	s.ReadLine("<LineServer closed: <nil>>")
}

func (s *ServerSuite) TestQuitPostAuth(c *C) {
	s.SendWelcome()
	s.Roundtrip()
	stopped := make(chan error)
	go func() {
		stopped <- s.server.Stop()
	}()
	s.ReadLine("QUIT :brb")
	s.lserver.Close()
	c.Assert(<-stopped, IsNil)
}

func (s *ServerSuite) TestJoinChannel(c *C) {
	s.SendWelcome()

	accounts := s.Session.DB("").C("accounts")
	err := accounts.Update(
		M{"name": "testserver"},
		M{"$set": M{"channels": []M{{"name": "#c1"}, {"name": "#c2"}}}},
	)
	c.Assert(err, IsNil)

	s.server.RefreshAccounts()
	s.ReadLine("JOIN #c1,#c2")

	// Confirm it joined both channels.
	s.SendLine(":mup!~mup@10.0.0.1 JOIN #c1")
	s.SendLine(":mup!~mup@10.0.0.1 JOIN #c2")

	err = accounts.Update(
		M{"name": "testserver"},
		M{"$set": M{"channels": []M{{"name": "#c1"}, {"name": "#c3"}}}},
	)
	c.Assert(err, IsNil)

	s.server.RefreshAccounts()
	s.ReadLine("JOIN #c3")
	s.ReadLine("PART #c2")

	// Do not confirm, forcing it to retry.
	s.server.RefreshAccounts()
	s.ReadLine("JOIN #c3")
	s.ReadLine("PART #c2")
}

func waitFor(condition func() bool) {
	now := time.Now()
	end := now.Add(1 * time.Second)
	for now.Before(end) {
		if condition() {
			return
		}
		time.Sleep(50 * time.Millisecond)
		now = time.Now()
	}
}

func (s *ServerSuite) TestIncoming(c *C) {
	s.SendWelcome()
	s.SendLine(":somenick!~someuser@somehost PRIVMSG mup :Hello mup!")
	s.Roundtrip()
	time.Sleep(100 * time.Millisecond)

	var msg Message
	incoming := s.Session.DB("").C("incoming")
	err := incoming.Find(nil).Sort("$natural").One(&msg)
	c.Assert(err, IsNil)

	msg.Id = ""
	c.Assert(msg, DeepEquals, Message{
		Account: "testserver",
		Prefix:  "somenick!~someuser@somehost",
		Nick:    "somenick",
		User:    "~someuser",
		Host:    "somehost",
		Cmd:     "PRIVMSG",
		Params:  []string{"mup", ":Hello mup!"},
		Target:  "mup",
		Text:    "Hello mup!",
		Bang:    "!",
		ToMup:   true,
		MupNick: "mup",
		MupText: "Hello mup!",
	})
}

func (s *ServerSuite) TestOutgoing(c *C) {
	// Stop default server to test the behavior of outgoing messages on start up.
	s.StopServer()

	accounts := s.Session.DB("").C("accounts")
	err := accounts.Update(
		M{"name": "testserver"},
		M{"$set": M{"channels": []M{{"name": "#test"}}}},
	)
	c.Assert(err, IsNil)

	outgoing := s.Session.DB("").C("outgoing")
	err = outgoing.Insert(&Message{
		Account: "testserver",
		Target:  "someone",
		Text:    "Hello there!",
	})
	c.Assert(err, IsNil)

	s.RestartServer()
	s.SendWelcome()

	// The initial JOINs is sent before any messages.
	s.ReadLine("JOIN #test")

	// Then the message and the PING asking for confirmation, handled by ReadLine.
	s.ReadLine("PRIVMSG someone :Hello there!")

	// Send another message with the server running.
	err = outgoing.Insert(&Message{
		Account: "testserver",
		Target:  "someone",
		Text:    "Hello again!",
	})
	c.Assert(err, IsNil)

	// Do not use the s.ReadLine helper as the message won't be confirmed.
	c.Assert(s.lserver.ReadLine(), Equals, "PRIVMSG someone :Hello again!")
	c.Assert(s.lserver.ReadLine(), Matches, "PING :sent:[0-9a-f]+")
	s.RestartServer()
	s.SendWelcome()

	// The unconfirmed message is resent.
	c.Assert(s.lserver.ReadLine(), Equals, "JOIN #test")
	c.Assert(s.lserver.ReadLine(), Equals, "PRIVMSG someone :Hello again!")
	c.Assert(s.lserver.ReadLine(), Matches, "PING :sent:[0-9a-f]+")
}

func (s *ServerSuite) TestPlugin(c *C) {
	s.SendWelcome()

	s.SendLine(":somenick!~someuser@somehost PRIVMSG mup :echoA A1")
	s.SendLine(":somenick!~someuser@somehost PRIVMSG mup :echoB B1")
	s.Roundtrip()

	plugins := s.Session.DB("").C("plugins")
	err := plugins.Insert(M{"name": "echo:A", "settings": M{"command": "echoA"}})
	c.Assert(err, IsNil)
	s.server.RefreshPlugins()

	s.SendLine(":somenick!~someuser@somehost PRIVMSG mup :echoA A2")
	s.SendLine(":somenick!~someuser@somehost PRIVMSG mup :echoB B2")

	s.ReadLine("PRIVMSG somenick :A1")
	s.ReadLine("PRIVMSG somenick :A2")

	err = plugins.Insert(M{"name": "echo:B", "settings": M{"command": "echoB"}})
	c.Assert(err, IsNil)
	s.server.RefreshPlugins()

	s.ReadLine("PRIVMSG somenick :B1")
	s.ReadLine("PRIVMSG somenick :B2")
	s.Roundtrip()

	s.RestartServer()
	s.SendWelcome()

	s.lserver.SendLine(":somenick!~someuser@somehost PRIVMSG mup :echoA A3")
	s.lserver.SendLine(":somenick!~someuser@somehost PRIVMSG mup :echoB B3")

	s.ReadLine("PRIVMSG somenick :A3")
	s.ReadLine("PRIVMSG somenick :B3")
}
