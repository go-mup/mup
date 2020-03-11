package mup_test

import (
	"fmt"
	"strings"
	"time"

	"database/sql"
	. "gopkg.in/check.v1"
	"gopkg.in/mup.v0"
	"gopkg.in/mup.v0/ldap"
	_ "gopkg.in/mup.v0/plugins/help"
	"gopkg.in/mup.v0/schema"
	"sync"
)

type ServerSuite struct {
	LineServerSuite

	config  *mup.Config
	server  *mup.Server
	lserver *LineServer

	dbdir string
	db    *sql.DB
}

var _ = Suite(&ServerSuite{})

func (s *ServerSuite) SetUpSuite(c *C) {
	s.LineServerSuite.SetUpSuite(c)

	s.dbdir = c.MkDir()
}

func (s *ServerSuite) TearDownSuite(c *C) {
	s.LineServerSuite.TearDownSuite(c)
}

func (s *ServerSuite) SetUpTest(c *C) {
	s.LineServerSuite.SetUpTest(c)

	mup.SetDebug(true)
	mup.SetLogger(c)

	var err error
	s.db, err = mup.OpenDB(s.dbdir)
	c.Assert(err, IsNil)

	s.config = &mup.Config{
		DB:      s.db,
		Refresh: -1, // Manual refreshing for testing.
	}

	_, err = s.db.Exec("INSERT INTO account (name,host,password) VALUES ('one',?,'password')", s.Addr.String())
	c.Assert(err, IsNil)

	s.RestartServer(c)
}

func (s *ServerSuite) TearDownTest(c *C) {
	mup.SetDebug(false)
	mup.SetLogger(nil)

	s.StopServer(c)

	s.db.Close()
	s.db = nil
	s.dbdir = c.MkDir()
	//c.Assert(mup.WipeDB(s.dbdir), IsNil)

	s.LineServerSuite.TearDownTest(c)
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
	if (strings.HasPrefix(line, "PRIVMSG ") || strings.HasPrefix(line, "NOTICE ")) && !strings.Contains(line, "nickserv") {
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

func (s *ServerSuite) TestNickChange(c *C) {
	s.SendWelcome(c)

	currentNick := "mup"
	for _, nickPrefix := range []string{"", ":"} {
		s.SendLine(c, fmt.Sprintf(":%s!~user@host NICK %s%s_", currentNick, nickPrefix, currentNick))

		s.SendLine(c, ":nick!~user@host PRIVMSG mup :Hello mup!")
		s.Roundtrip(c)
		time.Sleep(50 * time.Millisecond)

		row := s.db.QueryRow("SELECT text,as_nick FROM incoming ORDER BY id DESC")
		var text, nick string
		c.Assert(row.Scan(&text, &nick), IsNil)
		c.Assert(text, Equals, "Hello mup!")
		c.Assert(nick, Equals, currentNick+"_")

		currentNick += "_"
	}
}

func (s *ServerSuite) TestIdentify(c *C) {
	s.StopServer(c)

	_, err := s.db.Exec("UPDATE account SET identify='nickpass' WHERE name='one'")
	c.Assert(err, IsNil)

	s.RestartServer(c)

	s.SendWelcome(c)
	s.ReadLine(c, "PRIVMSG nickserv :IDENTIFY mup nickpass")

	s.server.RefreshAccounts()
	s.Roundtrip(c)

	_, err = s.db.Exec("UPDATE account SET identify='other' WHERE name='one'")
	c.Assert(err, IsNil)

	s.server.RefreshAccounts()
	s.ReadLine(c, "PRIVMSG nickserv :IDENTIFY mup other")
	s.Roundtrip(c)
}

func (s *ServerSuite) TestIdentifyNickInUse(c *C) {
	s.StopServer(c)

	_, err := s.db.Exec("UPDATE account SET identify='nickpass' WHERE name='one'")
	c.Assert(err, IsNil)

	s.RestartServer(c)

	s.SendLine(c, ":n.net 433 * mup :Nickname is already in use.")
	s.ReadLine(c, "NICK mup_")
	s.SendLine(c, ":n.net 001 mup_ :Welcome!")

	s.ReadLine(c, "PRIVMSG nickserv :IDENTIFY mup nickpass")
	s.ReadLine(c, "PRIVMSG nickserv :GHOST mup nickpass")
	s.ReadLine(c, "NICK mup")
	s.Roundtrip(c)
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

	tx, err := s.db.Begin()
	c.Assert(err, IsNil)
	_, err = tx.Exec("INSERT INTO channel (account,name) VALUES ('one','#c1')")
	c.Assert(err, IsNil)
	_, err = tx.Exec("INSERT INTO channel (account,name) VALUES ('one','#c2')")
	c.Assert(err, IsNil)
	_, err = tx.Exec("INSERT INTO channel (account,name) VALUES ('one','#c3')")
	c.Assert(err, IsNil)
	_, err = tx.Exec("INSERT INTO channel (account,name) VALUES ('one','#c4')")
	c.Assert(err, IsNil)
	c.Assert(tx.Commit(), IsNil)

	s.server.RefreshAccounts()
	s.ReadLine(c, "JOIN #c1,#c2,#c3,#c4")

	// Confirm it joined both channels.
	s.SendLine(c, ":mup!~mup@10.0.0.1 JOIN #c1")  // Some servers do this and
	s.SendLine(c, ":mup!~mup@10.0.0.1 JOIN :#C2") // some servers do that.
	s.SendLine(c, ":mup!~mup@10.0.0.1 JOIN #c3")
	s.SendLine(c, ":mup!~mup@10.0.0.1 JOIN #c3") // Ignore doubles.
	s.SendLine(c, ":mup!~mup@10.0.0.1 JOIN #c4")
	s.Roundtrip(c)

	tx, err = s.db.Begin()
	c.Assert(err, IsNil)
	_, err = s.db.Exec("INSERT INTO channel (account,name) VALUES ('one','#c5')")
	c.Assert(err, IsNil)
	_, err = s.db.Exec("DELETE FROM channel WHERE name='#c3' OR name='#c4'")
	c.Assert(err, IsNil)
	c.Assert(tx.Commit(), IsNil)

	s.server.RefreshAccounts()
	s.ReadLine(c, "JOIN #c5")
	s.ReadLine(c, "PART #c3,#c4")

	// Do not confirm, forcing it to retry.
	s.server.RefreshAccounts()
	s.ReadLine(c, "JOIN #c5")
	s.ReadLine(c, "PART #c3,#c4")

	// Confirm departures only, to test they're properly handled.
	s.SendLine(c, ":mup!~mup@10.0.0.1 PART #c3")  // Again, some servers do this and
	s.SendLine(c, ":mup!~mup@10.0.0.1 PART :#C4") // some servers do that.
	s.SendLine(c, ":mup!~mup@10.0.0.1 PART #c3")  // Ignore doubles.
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
	before := time.Now().Add(-2 * time.Second)

	s.SendWelcome(c)
	s.SendLine(c, ":nick!~user@host PRIVMSG mup :Hello mup!")
	s.Roundtrip(c)
	time.Sleep(500 * time.Millisecond)

	var msg mup.Message

	rows, err := s.db.Query("SELECT time,account,nick,user,host,command,text,bot_text,bang,as_nick FROM incoming ORDER BY id")
	c.Assert(err, IsNil)
	c.Assert(rows.Next(), Equals, true)
	err = rows.Scan(&msg.Time, &msg.Account, &msg.Nick, &msg.User, &msg.Host, &msg.Command, &msg.Text, &msg.BotText, &msg.Bang, &msg.AsNick)
	c.Assert(err, IsNil)

	after := time.Now().Add(2 * time.Second)

	c.Logf("Message time: %s", msg.Time)

	c.Assert(msg.Time.After(before), Equals, true)
	c.Assert(msg.Time.Before(after), Equals, true)

	msg.Time = time.Time{}
	msg.Id = ""
	c.Assert(msg, DeepEquals, mup.Message{
		Account: "one",
		Nick:    "nick",
		User:    "~user",
		Host:    "host",
		Command: "PRIVMSG",
		Text:    "Hello mup!",
		BotText: "Hello mup!",
		Bang:    "!",
		AsNick:  "mup",
	})
}

func exec(c *C, db *sql.DB, stmts ...string) {
	tx, err := db.Begin()
	c.Assert(err, IsNil)
	defer tx.Rollback()
	for _, stmt := range stmts {
		_, err := tx.Exec(stmt)
		c.Assert(err, IsNil)
	}
	tx.Commit()
}

func (s *ServerSuite) TestOutgoing(c *C) {
	// Stop default server to test the behavior of outgoing messages on start up.
	s.StopServer(c)

	exec(c, s.db,
		"INSERT INTO channel (account,name) VALUES ('one','#test')",
		"INSERT INTO outgoing (id,account,nick,text,command) VALUES ('000000000001','one','someone','Implicit PRIVMSG.','')",
		"INSERT INTO outgoing (id,account,nick,text,command) VALUES ('000000000002','one','someone','Explicit PRIVMSG.','PRIVMSG')",
		"INSERT INTO outgoing (id,account,nick,text,command) VALUES ('000000000003','one','someone','Explicit NOTICE.','NOTICE')",
	)

	s.RestartServer(c)
	s.SendWelcome(c)

	// The initial JOINs are sent before any messages.
	s.ReadLine(c, "JOIN #test")

	// Then the messages and their PINGs asking for confirmation, handled by ReadLine.
	s.ReadLine(c, "PRIVMSG someone :Implicit PRIVMSG.")
	s.ReadLine(c, "PRIVMSG someone :Explicit PRIVMSG.")
	s.ReadLine(c, "NOTICE someone :Explicit NOTICE.")

	// Ensure the confirmations are received before restarting.
	s.Roundtrip(c)

	// This must be ignored. Different account.
	exec(c, s.db, "INSERT INTO outgoing (id,account,nick,text) VALUES ('000000000004','two','someone','Ignore me.')")

	println("BEFORE")
	// Send another message with the server running.
	exec(c, s.db, "INSERT INTO outgoing (id,account,nick,text) VALUES ('000000000005','one','someone','Hello again!')")
	println("AFTER")

	// Do not use the s.ReadLine helper as the message won't be confirmed.
	c.Assert(s.lserver.ReadLine(), Equals, "PRIVMSG someone :Hello again!")
	c.Assert(s.lserver.ReadLine(), Matches, "PING :sent:[0-9a-f]+")
	s.RestartServer(c)
	s.SendWelcome(c)
	println("AFTER 2")

	// The unconfirmed message is resent.
	c.Assert(s.lserver.ReadLine(), Equals, "JOIN #test")
	c.Assert(s.lserver.ReadLine(), Equals, "PRIVMSG someone :Hello again!")
	c.Assert(s.lserver.ReadLine(), Matches, "PING :sent:[0-9a-f]+")

	println("AFTER 3")
}

func (s *ServerSuite) TestPlugin(c *C) {
	s.SendWelcome(c)

	s.SendLine(c, ":nick!~user@host PRIVMSG mup :echoAcmd A1")
	s.SendLine(c, ":nick!~user@host PRIVMSG mup :echoAmsg A1")
	s.SendLine(c, ":nick!~user@host PRIVMSG mup :echoBcmd B1")
	s.SendLine(c, ":nick!~user@host PRIVMSG mup :echoBmsg B1")
	s.Roundtrip(c)

	exec(c, s.db,
		`INSERT INTO plugin (name,config) VALUES ('echoA','{"prefix": "A."}')`,
		`INSERT INTO target (plugin,account) VALUES ('echoA','one')`,
	)
	s.server.RefreshPlugins()

	s.SendLine(c, ":nick!~user@host PRIVMSG mup :echoAcmd A2")
	s.SendLine(c, ":nick!~user@host PRIVMSG mup :echoAmsg A2")
	s.SendLine(c, ":nick!~user@host PRIVMSG mup :echoBcmd B2")
	s.SendLine(c, ":nick!~user@host PRIVMSG mup :echoBmsg B2")

	s.ReadLine(c, "PRIVMSG nick :[cmd] A.A1")
	s.ReadLine(c, "PRIVMSG nick :[msg] A.A1")
	s.ReadLine(c, "PRIVMSG nick :[cmd] A.A2")
	s.ReadLine(c, "PRIVMSG nick :[msg] A.A2")

	exec(c, s.db,
		`INSERT INTO plugin (name,config) VALUES ('echoB','{"prefix": "B."}')`,
		`INSERT INTO target (plugin,account) VALUES ('echoB','one')`,
	)
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

	// Ensure the outgoing handler is being properly called.
	log := c.GetTestLog()
	c.Assert(log, Matches, `(?s).*\[echoA\] \[out\] \[cmd\] A\.A2\n.*`)
	c.Assert(log, Matches, `(?s).*\[echoB\] \[out\] \[cmd\] A\.A2\n.*`)
}

func (s *ServerSuite) TestPluginTarget(c *C) {
	s.SendWelcome(c)

	exec(c, s.db,
		`INSERT INTO plugin (name,config) VALUES ('echoA','{"prefix": "A."}')`,
		`INSERT INTO plugin (name,config) VALUES ('echoB','{"prefix": "B."}')`,
		`INSERT INTO plugin (name,config) VALUES ('echoC','{"prefix": "C."}')`,
		`INSERT INTO target (plugin,account,channel) VALUES ('echoA','one','#chan1')`,
		`INSERT INTO target (plugin,account,channel) VALUES ('echoB','one','#chan2')`,
		`INSERT INTO target (plugin,account) VALUES ('echoC','one')`,
	)
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

	exec(c, s.db,
		`INSERT INTO plugin (name,config) VALUES ('echoA','{"prefix": "A."}')`,
		`INSERT INTO plugin (name,config) VALUES ('echoB','{"prefix": "B."}')`,
		`INSERT INTO plugin (name,config) VALUES ('echoC','{"prefix": "C."}')`,
		`INSERT INTO plugin (name,config) VALUES ('echoD','{"prefix": "D."}')`,
		`INSERT INTO target (plugin,account) VALUES ('echoA','one')`,
		`INSERT INTO target (plugin,account) VALUES ('echoB','one')`,
		`INSERT INTO target (plugin,account,channel) VALUES ('echoC','one','#chan')`,
		`INSERT INTO target (plugin,account,channel) VALUES ('echoD','one','none')`,
	)
	s.server.RefreshPlugins()
	s.Roundtrip(c)

	exec(c, s.db,
		`DELETE FROM plugin WHERE name='echoA'`,
		`UPDATE plugin SET config='{"prefix": "D2."}' WHERE name='echoD'`,
		`UPDATE target SET channel='none' WHERE plugin='echoC'`,
		`UPDATE target SET channel='#chan' WHERE plugin='echoD'`,
	)
	s.server.RefreshPlugins()
	s.Roundtrip(c)

	time.Sleep(500 * time.Millisecond)

	s.SendLine(c, ":nick!~user@host PRIVMSG #chan :mup: echoAcmd A")
	s.SendLine(c, ":nick!~user@host PRIVMSG #chan :mup: echoBcmd B")
	s.SendLine(c, ":nick!~user@host PRIVMSG #chan :mup: echoCcmd C")
	s.SendLine(c, ":nick!~user@host PRIVMSG #chan :mup: echoDcmd D")

	s.ReadLine(c, "PRIVMSG #chan :nick: [cmd] B.B")
	s.ReadLine(c, "PRIVMSG #chan :nick: [cmd] D2.D")
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

	var mu sync.Mutex
	var dials = make(map[string]*ldap.Config)
	var dialn int
	ldap.TestDial = func(config *ldap.Config) (ldap.Conn, error) {
		mu.Lock()
		dials[config.URL] = config
		dialn++
		mu.Unlock()
		return &ldapConn{}, nil
	}
	defer func() {
		ldap.TestDial = nil
	}()

	exec(c, s.db,
		`INSERT INTO ldap (name,url,base_dn,bind_dn,bind_pass) VALUES ('test1','the-url1','the-basedn','the-binddn','the-bindpass')`,
		`INSERT INTO ldap (name,url) VALUES ('test2','the-url2')`,
		`INSERT INTO plugin (name) VALUES ('testldap')`,
		`INSERT INTO target (plugin,account) VALUES ('testldap','one')`,
	)
	s.server.RefreshPlugins()
	s.Roundtrip(c)

	s.SendLine(c, ":nick!~user@host PRIVMSG mup :testldap test1")
	s.ReadLine(c, "PRIVMSG nick :LDAP works fine.")
	s.SendLine(c, ":nick!~user@host PRIVMSG mup :testldap test1")
	s.ReadLine(c, "PRIVMSG nick :LDAP works fine.")
	s.SendLine(c, ":nick!~user@host PRIVMSG mup :testldap test2")
	s.ReadLine(c, "PRIVMSG nick :LDAP works fine.")

	exec(c, s.db,
		`INSERT INTO ldap (name,url) VALUES ('test3','the-url3')`,
		`UPDATE ldap SET url='the-url4' WHERE name='test1'`,
		`DELETE FROM ldap WHERE name='test2'`,
	)
	s.server.RefreshPlugins()
	s.Roundtrip(c)

	s.SendLine(c, ":nick!~user@host PRIVMSG mup :testldap test1")
	s.ReadLine(c, "PRIVMSG nick :LDAP works fine.")
	s.SendLine(c, ":nick!~user@host PRIVMSG mup :testldap test3")
	s.ReadLine(c, "PRIVMSG nick :LDAP works fine.")
	s.SendLine(c, ":nick!~user@host PRIVMSG mup :testldap test2")
	s.ReadLine(c, `PRIVMSG nick :LDAP method error: LDAP connection "test2" not found`)

	s.StopServer(c)

	mu.Lock()
	defer mu.Unlock()

	c.Assert(dials["the-url1"], DeepEquals, &ldap.Config{URL: "the-url1", BaseDN: "the-basedn", BindDN: "the-binddn", BindPass: "the-bindpass"})
	c.Assert(dials["the-url2"], DeepEquals, &ldap.Config{URL: "the-url2"})
	c.Assert(dials["the-url4"], DeepEquals, &ldap.Config{URL: "the-url4", BaseDN: "the-basedn", BindDN: "the-binddn", BindPass: "the-bindpass"})
	c.Assert(dials["the-url3"], DeepEquals, &ldap.Config{URL: "the-url3"})

	c.Assert(dialn, Equals, 4)
}

var testDBSpec = mup.PluginSpec{
	Name:     "testdb",
	Start:    testDBStart,
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
	var n int
	db := p.plugger.DB()
	rows, err := db.Query("SELECT count(*) FROM account")
	if err == nil {
		defer rows.Close()
		rows.Next()
		err = rows.Scan(&n)
	}
	p.plugger.Sendf(cmd, "Number of accounts found: %d (err=%v)", n, err)
}

func (s *ServerSuite) TestDatabase(c *C) {
	s.SendWelcome(c)

	exec(c, s.db,
		`INSERT INTO plugin (name) VALUES ('testdb')`,
		`INSERT INTO target (plugin,account) VALUES ('testdb','one')`,
	)
	s.server.RefreshPlugins()
	s.Roundtrip(c)

	s.SendLine(c, ":nick!~user@host PRIVMSG mup :testdb")
	s.ReadLine(c, "PRIVMSG nick :Number of accounts found: 1 (err=<nil>)")
}

func (s *ServerSuite) TestHelp(c *C) {
	s.SendWelcome(c)

	exec(c, s.db,
		`INSERT INTO plugin (name) VALUES ('help')`,
		`INSERT INTO target (plugin,account) VALUES ('help','one')`,
	)
	s.server.RefreshPlugins()

	s.SendLine(c, ":nick!~user@host PRIVMSG mup :help help")
	s.ReadLine(c, "PRIVMSG nick :help [<cmdname>] — Displays available commands or details for a specific command.")

	s.SendLine(c, ":nick!~user@host PRIVMSG mup :testdb")
	s.ReadLine(c, `PRIVMSG nick :Plugin "testdb" is not running.`)

	exec(c, s.db,
		`INSERT INTO plugin (name) VALUES ('testdb')`,
		`INSERT INTO account (name) VALUES ('other')`,
		`INSERT INTO target (plugin,account) VALUES ('testdb','other')`,
	)
	s.server.RefreshPlugins()

	s.SendLine(c, ":nick!~user@host PRIVMSG mup :testdb")
	s.ReadLine(c, `PRIVMSG nick :Plugin "testdb" is not enabled here.`)
}

func (s *ServerSuite) TestPluginSelection(c *C) {
	s.StopServer(c)

	// Must exist for the following logic to be meaningful.
	row := s.db.QueryRow("SELECT COUNT(*) FROM plugin_schema WHERE plugin='testdb'")
	var n int
	c.Assert(row.Scan(&n), IsNil)
	c.Assert(n, Equals, 1)

	var on bool
	err := s.db.QueryRow("PRAGMA foreign_keys").Scan(&on)
	c.Assert(err, IsNil)
	c.Assert(on, Equals, true)

	exec(c, s.db,
		`INSERT INTO plugin (name,config) VALUES ('help', '{"boring": true}')`,
		`INSERT INTO plugin (name) VALUES ('testdb')`,
		`INSERT INTO target (plugin,account) VALUES ('help','one')`,
		`INSERT INTO target (plugin,account) VALUES ('testdb','one')`,
		`DELETE FROM plugin_schema`,
	)

	s.config.Plugins = []string{"help"}
	s.RestartServer(c)
	s.SendWelcome(c)
	s.Roundtrip(c)

	s.SendLine(c, ":nick!~user@host PRIVMSG mup :help help")
	s.ReadLine(c, "PRIVMSG nick :help [<cmdname>] — Displays available commands or details for a specific command.")

	rows, err := s.db.Query("SELECT plugin FROM plugin_schema")
	c.Assert(err, IsNil)
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		c.Assert(rows.Scan(&name), IsNil)
		names = append(names, name)
	}
	c.Assert(rows.Err(), IsNil)
	c.Assert(names, DeepEquals, []string{"help"})

	// The following test ensures that the help plugin is properly loaded
	// and that the testdb is not loaded nor is known. The message is sent
	// twice to ensure a roundtrip, giving a chance for both plugins to reply.
	s.SendLine(c, ":nick!~user@host PRIVMSG mup :testdb")
	s.SendLine(c, ":nick!~user@host PRIVMSG mup :testdb")
	s.ReadLine(c, `PRIVMSG nick :Command "testdb" not found.`)
	s.ReadLine(c, `PRIVMSG nick :Command "testdb" not found.`)
}

func (s *ServerSuite) TestAccountSelection(c *C) {
	s.StopServer(c)

	exec(c, s.db,
		`INSERT INTO account (name,host,password) VALUES  ('two','`+s.Addr.String()+`','password')`,
		`INSERT INTO plugin (name,config) VALUES ('echoA/one', '{"prefix": "one:"}')`,
		`INSERT INTO plugin (name,config) VALUES ('echoA/two', '{"prefix": "two:"}')`,
		`INSERT INTO target (plugin,account) VALUES ('echoA/one','one')`,
		`INSERT INTO target (plugin,account) VALUES ('echoA/two','two')`,
	)

	s.config.Accounts = []string{"two"}
	s.RestartServer(c)
	s.SendWelcome(c)

	s.SendLine(c, ":nick!~user@host PRIVMSG mup :echoAcmd A1")
	s.ReadLine(c, "PRIVMSG nick :[cmd] two:A1")

	s.StopServer(c)
	s.config.Accounts = []string{"one"}
	s.RestartServer(c)
	s.SendWelcome(c)

	s.SendLine(c, ":nick!~user@host PRIVMSG mup :echoAcmd A2")
	s.ReadLine(c, "PRIVMSG nick :[cmd] one:A2")
}
