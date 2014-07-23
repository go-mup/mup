package mup_test

import (
	"strings"
	"time"

	. "gopkg.in/check.v1"
	"gopkg.in/mgo.v2"
	"gopkg.in/mup.v0"
	"gopkg.in/mup.v0/ldap"
	"gopkg.in/mup.v0/schema"
)

type ServerSuite struct {
	LineServerSuite

	dbserver mup.DBServerHelper
	session  *mgo.Session
	config   *mup.Config
	server   *mup.Server
	lserver  *LineServer
}

var _ = Suite(&ServerSuite{})

func (s *ServerSuite) SetUpSuite(c *C) {
	s.dbserver.SetPath(c.MkDir())
	s.LineServerSuite.SetUpSuite(c)
}

func (s *ServerSuite) TearDownSuite(c *C) {
	s.LineServerSuite.TearDownSuite(c)
	s.dbserver.Stop()
}

func (s *ServerSuite) SetUpTest(c *C) {
	s.LineServerSuite.SetUpTest(c)

	mup.SetDebug(true)
	mup.SetLogger(c)

	s.session = s.dbserver.Session()

	db := s.session.DB("mup")
	s.config = &mup.Config{
		Database: db,
		Refresh:  -1, // Manual refreshing for testing.
	}

	err := db.C("accounts").Insert(M{"_id": "one", "host": s.Addr.String(), "password": "password"})
	c.Assert(err, IsNil)

	s.RestartServer(c)
}

func (s *ServerSuite) TearDownTest(c *C) {
	s.session.Close()

	mup.SetDebug(false)
	mup.SetLogger(nil)

	s.StopServer(c)
	s.LineServerSuite.TearDownTest(c)
	s.dbserver.Reset()
	s.dbserver.AssertClosed()
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
	s.server, err = mup.Start(s.config)
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

	s.SendLine(c, ":n.net 001 mup__ :Welcome!")
	s.server.RefreshAccounts()

	s.ReadLine(c, "NICK mup")
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

	accounts := s.session.DB("").C("accounts")
	err := accounts.UpdateId("one", M{"$set": M{"channels": []M{{"name": "#c1"}, {"name": "#c2"}, {"name": "#c3"}, {"name": "#c4"}}}})
	c.Assert(err, IsNil)

	s.server.RefreshAccounts()
	s.ReadLine(c, "JOIN #c1,#c2,#c3,#c4")

	// Confirm it joined both channels.
	s.SendLine(c, ":mup!~mup@10.0.0.1 JOIN #c1")  // Some servers do this and
	s.SendLine(c, ":mup!~mup@10.0.0.1 JOIN :#c2") // some servers do that.
	s.SendLine(c, ":mup!~mup@10.0.0.1 JOIN #c3")
	s.SendLine(c, ":mup!~mup@10.0.0.1 JOIN #c4")
	s.Roundtrip(c)

	err = accounts.UpdateId("one", M{"$set": M{"channels": []M{{"name": "#c1"}, {"name": "#c2"}, {"name": "#c5"}}}})
	c.Assert(err, IsNil)

	s.server.RefreshAccounts()
	s.ReadLine(c, "JOIN #c5")
	s.ReadLine(c, "PART #c3,#c4")

	// Do not confirm, forcing it to retry.
	s.server.RefreshAccounts()
	s.ReadLine(c, "JOIN #c5")
	s.ReadLine(c, "PART #c3,#c4")

	// Confirm departures only, to test they're properly handled.
	s.SendLine(c, ":mup!~mup@10.0.0.1 PART #c3")  // Again, some servers do this and
	s.SendLine(c, ":mup!~mup@10.0.0.1 PART :#c4") // some servers do that.
	s.Roundtrip(c)

	// Do it twice to ensure there are no further lines to read.
	s.server.RefreshAccounts()
	s.ReadLine(c, "JOIN #c5")
	s.server.RefreshAccounts()
	s.ReadLine(c, "JOIN #c5")
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
	time.Sleep(500 * time.Millisecond)

	var msg mup.Message
	incoming := s.session.DB("").C("incoming")
	err := incoming.Find(nil).Sort("$natural").One(&msg)
	c.Assert(err, IsNil)

	msg.Id = ""
	c.Assert(msg, DeepEquals, mup.Message{
		Account: "one",
		Nick:    "nick",
		User:    "~user",
		Host:    "host",
		Command: "PRIVMSG",
		Text:    "Hello mup!",
		Bang:    "!",
		AsNick:  "mup",

		ToMup:   true,
		MupText: "Hello mup!",
	})
}

func (s *ServerSuite) TestOutgoing(c *C) {
	// Stop default server to test the behavior of outgoing messages on start up.
	s.StopServer(c)

	accounts := s.session.DB("").C("accounts")
	err := accounts.UpdateId("one", M{"$set": M{"channels": []M{{"name": "#test"}}}})
	c.Assert(err, IsNil)

	outgoing := s.session.DB("").C("outgoing")
	err = outgoing.Insert(&mup.Message{
		Account: "one",
		Nick:    "someone",
		Text:    "Hello there!",
	})
	c.Assert(err, IsNil)

	s.RestartServer(c)
	s.SendWelcome(c)

	// The initial JOINs are sent before any messages.
	s.ReadLine(c, "JOIN #test")

	// Then the message and the PING asking for confirmation, handled by ReadLine.
	s.ReadLine(c, "PRIVMSG someone :Hello there!")

	// Send another message with the server running.
	err = outgoing.Insert(&mup.Message{
		Account: "one",
		Nick:    "someone",
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

	s.SendLine(c, ":nick!~user@host PRIVMSG mup :echoAcmd A1")
	s.SendLine(c, ":nick!~user@host PRIVMSG mup :echoAmsg A1")
	s.SendLine(c, ":nick!~user@host PRIVMSG mup :echoBcmd B1")
	s.SendLine(c, ":nick!~user@host PRIVMSG mup :echoBmsg B1")
	s.Roundtrip(c)

	plugins := s.session.DB("").C("plugins")
	err := plugins.Insert(M{"_id": "echoA", "config": M{"prefix": "A."}, "targets": []M{{"account": "one"}}})
	c.Assert(err, IsNil)
	s.server.RefreshPlugins()

	s.SendLine(c, ":nick!~user@host PRIVMSG mup :echoAcmd A2")
	s.SendLine(c, ":nick!~user@host PRIVMSG mup :echoAmsg A2")
	s.SendLine(c, ":nick!~user@host PRIVMSG mup :echoBcmd B2")
	s.SendLine(c, ":nick!~user@host PRIVMSG mup :echoBmsg B2")

	s.ReadLine(c, "PRIVMSG nick :[cmd] A.A1")
	s.ReadLine(c, "PRIVMSG nick :[msg] A.A1")
	s.ReadLine(c, "PRIVMSG nick :[cmd] A.A2")
	s.ReadLine(c, "PRIVMSG nick :[msg] A.A2")

	err = plugins.Insert(M{"_id": "echoB", "config": M{"prefix": "B."}, "targets": []M{{"account": "one"}}})
	c.Assert(err, IsNil)
	s.server.RefreshPlugins()

	s.ReadLine(c, "PRIVMSG nick :[cmd] B.B1")
	s.ReadLine(c, "PRIVMSG nick :[msg] B.B1")
	s.ReadLine(c, "PRIVMSG nick :[cmd] B.B2")
	s.ReadLine(c, "PRIVMSG nick :[msg] B.B2")
	s.Roundtrip(c)

	s.RestartServer(c)
	s.SendWelcome(c)

	s.lserver.SendLine(":nick!~user@host PRIVMSG mup :echoAcmd A3")
	s.lserver.SendLine(":nick!~user@host PRIVMSG mup :echoAmsg A3")
	s.lserver.SendLine(":nick!~user@host PRIVMSG mup :echoBcmd B3")
	s.lserver.SendLine(":nick!~user@host PRIVMSG mup :echoBmsg B3")

	s.ReadLine(c, "PRIVMSG nick :[cmd] A.A3")
	s.ReadLine(c, "PRIVMSG nick :[msg] A.A3")
	s.ReadLine(c, "PRIVMSG nick :[cmd] B.B3")
	s.ReadLine(c, "PRIVMSG nick :[msg] B.B3")
}

func (s *ServerSuite) TestPluginTarget(c *C) {
	s.SendWelcome(c)

	plugins := s.session.DB("").C("plugins")
	err := plugins.Insert(
		M{"_id": "echoA", "config": M{"prefix": "A."}, "targets": []M{{"account": "one", "channel": "#chan1"}}},
		M{"_id": "echoB", "config": M{"prefix": "B."}, "targets": []M{{"account": "one", "channel": "#chan2"}}},
		M{"_id": "echoC", "config": M{"prefix": "C."}, "targets": []M{{"account": "one"}}},
	)
	c.Assert(err, IsNil)
	s.server.RefreshPlugins()

	s.SendLine(c, ":nick!~user@host PRIVMSG #chan1 :mup: echoAcmd A1")
	s.SendLine(c, ":nick!~user@host PRIVMSG #chan2 :mup: echoAcmd A2")
	s.SendLine(c, ":nick!~user@host PRIVMSG #chan1 :mup: echoBcmd B1")
	s.SendLine(c, ":nick!~user@host PRIVMSG #chan2 :mup: echoBcmd B2")
	s.SendLine(c, ":nick!~user@host PRIVMSG #chan1 :mup: echoCcmd C1")
	s.SendLine(c, ":nick!~user@host PRIVMSG #chan2 :mup: echoCcmd C2")

	s.ReadLine(c, "PRIVMSG #chan1 :nick: [cmd] A.A1")
	s.ReadLine(c, "PRIVMSG #chan2 :nick: [cmd] B.B2")
	s.ReadLine(c, "PRIVMSG #chan1 :nick: [cmd] C.C1")
	s.ReadLine(c, "PRIVMSG #chan2 :nick: [cmd] C.C2")
}

func (s *ServerSuite) TestPluginUpdates(c *C) {
	s.SendWelcome(c)

	plugins := s.session.DB("").C("plugins")
	err := plugins.Insert(
		M{"_id": "echoA", "config": M{"prefix": "A."}, "targets": []M{{"account": "one"}}},
		M{"_id": "echoB", "config": M{"prefix": "B."}, "targets": []M{{"account": "one", "target": "none"}}},
		M{"_id": "echoC", "config": M{"prefix": "C."}, "targets": []M{{"account": "one", "target": "#chan"}}},
		M{"_id": "echoD", "config": M{"prefix": "D."}, "targets": []M{{"account": "one", "target": "#chan"}}},
	)
	c.Assert(err, IsNil)
	s.server.RefreshPlugins()
	s.Roundtrip(c)

	err = plugins.Remove(M{"_id": "echoA"})
	c.Assert(err, IsNil)
	err = plugins.UpdateId("echoB", M{"$set": M{"targets.0.target": "#chan"}})
	c.Assert(err, IsNil)
	err = plugins.UpdateId("echoD", M{"$set": M{"config.prefix": "E."}})
	c.Assert(err, IsNil)
	s.server.RefreshPlugins()
	s.Roundtrip(c)

	time.Sleep(500 * time.Millisecond)

	s.SendLine(c, ":nick!~user@host PRIVMSG #chan :mup: echoAcmd A")
	s.SendLine(c, ":nick!~user@host PRIVMSG #chan :mup: echoBcmd B")
	s.SendLine(c, ":nick!~user@host PRIVMSG #chan :mup: echoCcmd C")
	s.SendLine(c, ":nick!~user@host PRIVMSG #chan :mup: echoDcmd D")

	s.ReadLine(c, "PRIVMSG #chan :nick: [cmd] B.B")
	s.ReadLine(c, "PRIVMSG #chan :nick: [cmd] C.C")
	s.ReadLine(c, "PRIVMSG #chan :nick: [cmd] E.D")
}

var testLDAPSpec = mup.PluginSpec{
	Name:  "testldap",
	Start: testLdapStart,
	Commands: schema.Commands{{
		Name: "testldap",
		Args: schema.Args{{
			Name: "ldapname",
			Flag: schema.Required,
		}},
	}},
}

func init() {
	mup.RegisterPlugin(&testLDAPSpec)
}

type testLdapPlugin struct {
	plugger *mup.Plugger
}

func testLdapStart(plugger *mup.Plugger) mup.Stopper {
	return &testLdapPlugin{plugger}
}

func (p *testLdapPlugin) Stop() error {
	return nil
}

func (p *testLdapPlugin) HandleCommand(cmd *mup.Command) {
	var args struct{ LDAPName string }
	cmd.Args(&args)
	conn, err := p.plugger.LDAP(args.LDAPName)
	if err != nil {
		p.plugger.Sendf(cmd, "LDAP method error: %v", err)
		return
	}
	defer conn.Close()
	results, err := conn.Search(&ldap.Search{})
	if len(results) != 1 || results[0].DN != "test-dn" || err != nil {
		p.plugger.Sendf(cmd, "Search method results=%#v err=%v", results, err)
		return
	}
	p.plugger.Sendf(cmd, "LDAP works fine.")
}

func (s *ServerSuite) TestLDAP(c *C) {
	s.SendWelcome(c)

	var dials []*ldap.Config
	ldap.TestDial = func(config *ldap.Config) (ldap.Conn, error) {
		dials = append(dials, config)
		return &ldapConn{}, nil
	}
	defer func() {
		ldap.TestDial = nil
	}()

	ldaps := s.session.DB("").C("ldap")
	plugins := s.session.DB("").C("plugins")
	err := ldaps.Insert(M{"_id": "test1", "url": "the-url1", "basedn": "the-basedn", "binddn": "the-binddn", "bindpass": "the-bindpass"})
	c.Assert(err, IsNil)
	err = ldaps.Insert(M{"_id": "test2", "url": "the-url2"})
	c.Assert(err, IsNil)
	err = plugins.Insert(M{"_id": "testldap", "targets": []M{{"account": "one"}}})
	c.Assert(err, IsNil)
	s.server.RefreshPlugins()
	s.Roundtrip(c)

	s.SendLine(c, ":nick!~user@host PRIVMSG mup :testldap test1")
	s.ReadLine(c, "PRIVMSG nick :LDAP works fine.")
	s.SendLine(c, ":nick!~user@host PRIVMSG mup :testldap test1")
	s.ReadLine(c, "PRIVMSG nick :LDAP works fine.")
	s.SendLine(c, ":nick!~user@host PRIVMSG mup :testldap test2")
	s.ReadLine(c, "PRIVMSG nick :LDAP works fine.")

	err = ldaps.Insert(M{"_id": "test3", "url": "the-url3"})
	c.Assert(err, IsNil)
	err = ldaps.UpdateId("test1", M{"$set": M{"url": "the-url4"}})
	c.Assert(err, IsNil)
	err = ldaps.RemoveId("test2")
	c.Assert(err, IsNil)
	s.server.RefreshPlugins()
	s.Roundtrip(c)

	s.SendLine(c, ":nick!~user@host PRIVMSG mup :testldap test1")
	s.ReadLine(c, "PRIVMSG nick :LDAP works fine.")
	s.SendLine(c, ":nick!~user@host PRIVMSG mup :testldap test3")
	s.ReadLine(c, "PRIVMSG nick :LDAP works fine.")
	s.SendLine(c, ":nick!~user@host PRIVMSG mup :testldap test2")
	s.ReadLine(c, `PRIVMSG nick :LDAP method error: LDAP connection "test2" not found`)

	s.StopServer(c)

	c.Assert(dials, HasLen, 4)
	c.Assert(dials[0], DeepEquals, &ldap.Config{URL: "the-url1", BaseDN: "the-basedn", BindDN: "the-binddn", BindPass: "the-bindpass"})
	c.Assert(dials[1], DeepEquals, &ldap.Config{URL: "the-url2"})
	c.Assert(dials[2], DeepEquals, &ldap.Config{URL: "the-url4", BaseDN: "the-basedn", BindDN: "the-binddn", BindPass: "the-bindpass"})
	c.Assert(dials[3], DeepEquals, &ldap.Config{URL: "the-url3"})
}

var testDBSpec = mup.PluginSpec{
	Name:  "testdb",
	Start: testDBStart,
	Commands: schema.Commands{{Name: "testdb"}},
}


func init() {
	mup.RegisterPlugin(&testDBSpec)
}

type testDBPlugin struct {
	plugger *mup.Plugger
}

func testDBStart(plugger *mup.Plugger) mup.Stopper {
	return &testDBPlugin{plugger}
}

func (p *testDBPlugin) Stop() error {
	return nil
}

func (p *testDBPlugin) HandleCommand(cmd *mup.Command) {
	session, c := p.plugger.Collection("mine")
	defer session.Close()
	n, err := c.Database.C("accounts").Count()
	p.plugger.Sendf(cmd, "Number of accounts found: %d (err=%v)", n, err)
}

func (s *ServerSuite) TestDatabase(c *C) {
	s.SendWelcome(c)

	plugins := s.session.DB("").C("plugins")
	err := plugins.Insert(M{"_id": "testdb", "targets": []M{{"account": "one"}}})
	c.Assert(err, IsNil)
	s.server.RefreshPlugins()
	s.Roundtrip(c)

	s.SendLine(c, ":nick!~user@host PRIVMSG mup :testdb")
	s.ReadLine(c, "PRIVMSG nick :Number of accounts found: 1 (err=<nil>)")
}
