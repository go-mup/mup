package mup

import (
	"time"
	"strings"

	. "gopkg.in/check.v1"
	"labix.org/v2/mgo"
)

type ServerSuite struct {
	LineServerSuite
	MgoSuite

	session *mgo.Session
	config  *Config
	server  *Server
	lserver *LineServer
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
		"name":     "one",
		"host":     s.Addr.String(),
		"password": "password",
	})
	c.Assert(err, IsNil)

	s.RestartServer(c)
}

func (s *ServerSuite) TearDownTest(c *C) {
	s.StopServer(c)
	s.LineServerSuite.TearDownTest(c)
	s.session.Close()
	s.config.Database.Session.Close()
	s.MgoSuite.TearDownTest(c)
}

func (s *ServerSuite) StopServer(c *C) {
	if s.lserver != nil {
		s.lserver.Close()
		s.lserver = nil
	}
	if s.server != nil {
		s.server.Stop()
		s.server = nil
	}
}

func (s *ServerSuite) RestartServer(c *C) {
	s.StopServer(c)
	n := s.NextLineServer()
	var err error
	s.server, err = Start(s.config)
	c.Assert(err, IsNil)
	s.lserver = s.LineServer(n)
	s.ReadUser(c)
}

func (s *ServerSuite) ReadUser(c *C) {
	s.ReadLine(c, "PASS password")
	s.ReadLine(c, "NICK mup")
	s.ReadLine(c, "USER mup 0 0 :Mup Pet")
}

func (s *ServerSuite) SendWelcome(c *C) {
	s.SendLine(c, ":n.net 001 mup :Welcome!")
}

func (s *ServerSuite) Handshake(c *C) {
	s.ReadUser(c)
	s.SendWelcome(c)
}

func (s *ServerSuite) SendLine(c *C, line string) {
	s.lserver.SendLine(line)
}

func (s *ServerSuite) ReadLine(c *C, line string) {
	c.Assert(s.lserver.ReadLine(), Equals, line)

	// Confirm read message.
	if strings.HasPrefix(line, "PRIVMSG ") {
		ping := s.lserver.ReadLine()
		c.Assert(ping, Matches, "PING :sent:.*")
		s.lserver.SendLine("PONG " + ping[5:])
	}
}

func (s *ServerSuite) Roundtrip(c *C) {
	s.lserver.SendLine("PING :roundtrip")
	c.Assert(s.lserver.ReadLine(), Equals, "PONG :roundtrip")
}

func (s *ServerSuite) TestConnect(c *C) {
	// SetUpTest does it all.
}

func (s *ServerSuite) TestNickInUse(c *C) {
	s.SendLine(c, ":n.net 433 * mup :Nickname is already in use.")
	s.ReadLine(c, "NICK mup_")
	s.SendLine(c, ":n.net 433 * mup_ :Nickname is already in use.")
	s.ReadLine(c, "NICK mup__")
}

func (s *ServerSuite) TestPingPong(c *C) {
	s.SendLine(c, "PING :foo")
	s.ReadLine(c, "PONG :foo")
}

func (s *ServerSuite) TestPingPongPostAuth(c *C) {
	s.SendWelcome(c)
	s.SendLine(c, "PING :foo")
	s.ReadLine(c, "PONG :foo")
}

func (s *ServerSuite) TestQuit(c *C) {
	s.server.Stop()
	s.ReadLine(c, "<LineServer closed: <nil>>")
}

func (s *ServerSuite) TestQuitPostAuth(c *C) {
	s.SendWelcome(c)
	s.Roundtrip(c)
	stopped := make(chan error)
	go func() {
		stopped <- s.server.Stop()
	}()
	s.ReadLine(c, "QUIT :brb")
	s.lserver.Close()
	c.Assert(<-stopped, IsNil)
}

func (s *ServerSuite) TestJoinChannel(c *C) {
	s.SendWelcome(c)

	accounts := s.Session.DB("").C("accounts")
	err := accounts.Update(
		M{"name": "one"},
		M{"$set": M{"channels": []M{{"name": "#c1"}, {"name": "#c2"}}}},
	)
	c.Assert(err, IsNil)

	s.server.RefreshAccounts()
	s.ReadLine(c, "JOIN #c1,#c2")

	// Confirm it joined both channels.
	s.SendLine(c, ":mup!~mup@10.0.0.1 JOIN #c1")
	s.SendLine(c, ":mup!~mup@10.0.0.1 JOIN #c2")

	err = accounts.Update(
		M{"name": "one"},
		M{"$set": M{"channels": []M{{"name": "#c1"}, {"name": "#c3"}}}},
	)
	c.Assert(err, IsNil)

	s.server.RefreshAccounts()
	s.ReadLine(c, "JOIN #c3")
	s.ReadLine(c, "PART #c2")

	// Do not confirm, forcing it to retry.
	s.server.RefreshAccounts()
	s.ReadLine(c, "JOIN #c3")
	s.ReadLine(c, "PART #c2")
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
	s.SendWelcome(c)
	s.SendLine(c, ":nick!~user@host PRIVMSG mup :Hello mup!")
	s.Roundtrip(c)
	time.Sleep(100 * time.Millisecond)

	var msg Message
	incoming := s.Session.DB("").C("incoming")
	err := incoming.Find(nil).Sort("$natural").One(&msg)
	c.Assert(err, IsNil)

	msg.Id = ""
	c.Assert(msg, DeepEquals, Message{
		Account: "one",
		Prefix:  "nick!~user@host",
		Nick:    "nick",
		User:    "~user",
		Host:    "host",
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
	s.StopServer(c)

	accounts := s.Session.DB("").C("accounts")
	err := accounts.Update(
		M{"name": "one"},
		M{"$set": M{"channels": []M{{"name": "#test"}}}},
	)
	c.Assert(err, IsNil)

	outgoing := s.Session.DB("").C("outgoing")
	err = outgoing.Insert(&Message{
		Account: "one",
		Target:  "someone",
		Text:    "Hello there!",
	})
	c.Assert(err, IsNil)

	s.RestartServer(c)
	s.SendWelcome(c)

	// The initial JOINs is sent before any messages.
	s.ReadLine(c, "JOIN #test")

	// Then the message and the PING asking for confirmation, handled by ReadLine.
	s.ReadLine(c, "PRIVMSG someone :Hello there!")

	// Send another message with the server running.
	err = outgoing.Insert(&Message{
		Account: "one",
		Target:  "someone",
		Text:    "Hello again!",
	})
	c.Assert(err, IsNil)

	// Do not use the s.ReadLine helper as the message won't be confirmed.
	c.Assert(s.lserver.ReadLine(), Equals, "PRIVMSG someone :Hello again!")
	c.Assert(s.lserver.ReadLine(), Matches, "PING :sent:[0-9a-f]+")
	s.RestartServer(c)
	s.SendWelcome(c)

	// The unconfirmed message is resent.
	c.Assert(s.lserver.ReadLine(), Equals, "JOIN #test")
	c.Assert(s.lserver.ReadLine(), Equals, "PRIVMSG someone :Hello again!")
	c.Assert(s.lserver.ReadLine(), Matches, "PING :sent:[0-9a-f]+")
}

func (s *ServerSuite) TestPlugin(c *C) {
	s.SendWelcome(c)

	s.SendLine(c, ":nick!~user@host PRIVMSG mup :echoA A1")
	s.SendLine(c, ":nick!~user@host PRIVMSG mup :echoB B1")
	s.Roundtrip(c)

	plugins := s.Session.DB("").C("plugins")
	err := plugins.Insert(M{"name": "echo:A", "settings": M{"command": "echoA"}, "targets": []M{{"account": "one"}}})
	c.Assert(err, IsNil)
	s.server.RefreshPlugins()

	s.SendLine(c, ":nick!~user@host PRIVMSG mup :echoA A2")
	s.SendLine(c, ":nick!~user@host PRIVMSG mup :echoB B2")

	s.ReadLine(c, "PRIVMSG nick :A1")
	s.ReadLine(c, "PRIVMSG nick :A2")

	err = plugins.Insert(M{"name": "echo:B", "settings": M{"command": "echoB"}, "targets": []M{{"account": "one"}}})
	c.Assert(err, IsNil)
	s.server.RefreshPlugins()

	s.ReadLine(c, "PRIVMSG nick :B1")
	s.ReadLine(c, "PRIVMSG nick :B2")
	s.Roundtrip(c)

	s.RestartServer(c)
	s.SendWelcome(c)

	s.lserver.SendLine(":nick!~user@host PRIVMSG mup :echoA A3")
	s.lserver.SendLine(":nick!~user@host PRIVMSG mup :echoB B3")

	s.ReadLine(c, "PRIVMSG nick :A3")
	s.ReadLine(c, "PRIVMSG nick :B3")
}

func (s *ServerSuite) TestPluginTarget(c *C) {
	s.SendWelcome(c)

	plugins := s.Session.DB("").C("plugins")
	err := plugins.Insert(
		M{"name": "echo:A", "settings": M{"command": "echoA"}, "targets": []M{{"account": "one", "target": "#chan1"}}},
		M{"name": "echo:B", "settings": M{"command": "echoB"}, "targets": []M{{"account": "one", "target": "#chan2"}}},
		M{"name": "echo:C", "settings": M{"command": "echoC"}, "targets": []M{{"account": "one"}}},
	)
	c.Assert(err, IsNil)
	s.server.RefreshPlugins()

	s.SendLine(c, ":nick!~user@host PRIVMSG #chan1 :mup: echoA A1")
	s.SendLine(c, ":nick!~user@host PRIVMSG #chan2 :mup: echoA A2")
	s.SendLine(c, ":nick!~user@host PRIVMSG #chan1 :mup: echoB B1")
	s.SendLine(c, ":nick!~user@host PRIVMSG #chan2 :mup: echoB B2")
	s.SendLine(c, ":nick!~user@host PRIVMSG #chan1 :mup: echoC C1")
	s.SendLine(c, ":nick!~user@host PRIVMSG #chan2 :mup: echoC C2")

	s.ReadLine(c, "PRIVMSG #chan1 :nick: A1")
	s.ReadLine(c, "PRIVMSG #chan2 :nick: B2")
	s.ReadLine(c, "PRIVMSG #chan1 :nick: C1")
	s.ReadLine(c, "PRIVMSG #chan2 :nick: C2")
}
