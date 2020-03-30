package admin_test

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	. "gopkg.in/check.v1"
	//"gopkg.in/mgo.v2/dbtest"
	"gopkg.in/mup.v0"
	"gopkg.in/mup.v0/plugins/admin"
)

func Test(t *testing.T) { TestingT(t) }

var _ = Suite(&AdminSuite{})

type AdminSuite struct {
	dbdir string
	db    *sql.DB
}

func (s *AdminSuite) SetUpSuite(c *C) {
	s.dbdir = c.MkDir()
}

func (s *AdminSuite) SetUpTest(c *C) {
	mup.SetLogger(c)
	mup.SetDebug(true)
}

func (s *AdminSuite) TearDownTest(c *C) {
	mup.SetLogger(nil)
	mup.SetDebug(false)
}

type adminTest struct {
	summary string
	send    []string
	recv    []string
	users   []userInfo
	login   bool
}

var adminTests = []adminTest{
	{
		summary: "Good login",
		users:   []userInfo{testUser},
		send:    []string{"login thesecret"},
		recv:    []string{"PRIVMSG nick :Okay."},
	}, {
		summary: "Attempt login with missing user",
		send:    []string{"login thesecret"},
		recv:    []string{"PRIVMSG nick :Nope."},
	}, {
		summary: "Attempt login with bad password",
		users:   []userInfo{testUser},
		send:    []string{"login badsecret"},
		recv:    []string{"PRIVMSG nick :Nope."},
	}, {
		summary: "Attempt login with bad account",
		users:   []userInfo{{Account: "other", Nick: "nick"}},
		send:    []string{"login thesecret"},
		recv:    []string{"PRIVMSG nick :Nope."},
	}, {
		summary: "Burst control: quota limit",
		users: []userInfo{{
			Account:           "test",
			Nick:              "nick",
			AttemptStartDelta: -admin.BurstWindow + 2*time.Second,
			AttemptCount:      admin.BurstQuota - 1,
		}},
		send: []string{"login badsecret", "login thesecret"},
		recv: []string{"PRIVMSG nick :Nope.", "PRIVMSG nick :Slow down."},
	}, {
		summary: "Burst control: successful logins do not affect quota",
		users: []userInfo{{
			Account:      "test",
			Nick:         "nick",
			AttemptCount: admin.BurstQuota - 1,
		}},
		send: []string{"login thesecret", "login thesecret"},
		recv: []string{"PRIVMSG nick :Okay.", "PRIVMSG nick :Okay."},
	}, {
		summary: "Burst control: normal window expired",
		users: []userInfo{{
			Account:           "test",
			Nick:              "nick",
			AttemptStartDelta: -admin.BurstWindow - 1*time.Second,
			AttemptCount:      admin.BurstQuota - 1,
		}},
		send: []string{"login badsecret", "login badsecret"},
		recv: []string{"PRIVMSG nick :Nope.", "PRIVMSG nick :Nope."},
	}, {
		summary: "Burst control: penalty window expired",
		users: []userInfo{{
			Account:           "test",
			Nick:              "nick",
			AttemptStartDelta: -admin.BurstPenalty - 1*time.Second,
			AttemptCount:      admin.BurstQuota,
		}},
		send: []string{"login thesecret"},
		recv: []string{"PRIVMSG nick :Okay."},
	}, {
		summary: "Burst control: penalty window",
		users: []userInfo{{
			Account:           "test",
			Nick:              "nick",
			AttemptStartDelta: -admin.BurstPenalty + 2*time.Second,
			AttemptCount:      admin.BurstQuota,
		}},
		send: []string{"login thesecret"},
		recv: []string{"PRIVMSG nick :Slow down."},
	}, {
		summary: "Burst control: smoke test",
		users:   []userInfo{testUser},
		send: []string{
			"login badsecret",
			"login badsecret",
			"login badsecret",
			"login thesecret",
			"login thesecret",
		},
		recv: []string{
			"PRIVMSG nick :Nope.",
			"PRIVMSG nick :Nope.",
			"PRIVMSG nick :Nope.",
			"PRIVMSG nick :Slow down.",
			"PRIVMSG nick :Slow down.",
		},
	}, {
		summary: "Register first user as an admin",
		send: []string{
			"register mysecret",
			"login mysecret",
			"sendraw PRIVMSG nick :Echo.",
		},
		recv: []string{
			"PRIVMSG nick :Registered as an admin (first user).",
			"PRIVMSG nick :Okay.",
			"PRIVMSG nick :Echo.",
			"PRIVMSG nick :Done.",
		},
	}, {
		summary: "Cannot register twice",
		send: []string{
			"register mysecret1",
			"register mysecret2",
			"login mysecret2",
			"login mysecret1",
		},
		recv: []string{
			"PRIVMSG nick :Registered as an admin (first user).",
			"PRIVMSG nick :Nick previously registered.",
			"PRIVMSG nick :Nope.",
			"PRIVMSG nick :Okay.",
		},
	}, {
		summary: "Register other users as normal users",
		users:   []userInfo{{Account: "other", Nick: "other"}},
		send: []string{
			"register mysecret",
			"login mysecret",
			"sendraw PRIVMSG nick :Echo.",
			"PRIVMSG nick :Must be an admin for that.",
		},
		recv: []string{
			"PRIVMSG nick :Registered.",
			"PRIVMSG nick :Okay.",
			"PRIVMSG nick :Must be an admin for that.",
		},
	}, {
		summary: "Different accounts have independent users",
		login:   true,
		send: []string{
			"login thesecret",
			"[@other] sendraw PRIVMSG nick :Echo.",
		},
		recv: []string{
			"PRIVMSG nick :Okay.",
			"[@other] PRIVMSG nick :Must login for that.",
		},
	}, {
		summary: "QUIT logs the user out",
		login:   true,
		send: []string{
			"login thesecret",
			"[,raw] :nick!user@host QUIT",
			"sendraw PRIVMSG nick :Echo.",
		},
		recv: []string{
			"PRIVMSG nick :Okay.",
			"PRIVMSG nick :Must login for that.",
		},
	}, {
		summary: "Changing the nick also logs the user out",
		login:   true,
		send: []string{
			"login thesecret",
			"[,raw] :nick!user@host NICK :other",
			"sendraw PRIVMSG nick :Echo.",
		},
		recv: []string{
			"PRIVMSG nick :Okay.",
			"PRIVMSG nick :Must login for that.",
		},
	},

	{
		send: []string{"sendraw"},
		recv: []string{"PRIVMSG nick :Oops: missing input for argument: text"},
	}, {
		send: []string{"sendraw PRIVMSG foo :text"},
		recv: []string{"PRIVMSG nick :Must login for that."},
	}, {
		login: true,
		send:  []string{"sendraw NOTICE foo :text"},
		recv:  []string{"NOTICE foo :text", "PRIVMSG nick :Done."},
	}, {
		login: true,
		send:  []string{"sendraw -account=other PRIVMSG bar :text"},
		recv:  []string{"[@other] PRIVMSG bar :text", "PRIVMSG nick :Done."},
	},
}

// Data for "thesecret"
var testSalt = "salt"
var testHash = "04e36fcd7a7b2677f41005670058a56fcb751a05fea3a531c68f83c5f9c3ac80"
var testUser = userInfo{Account: "test", Nick: "nick", Admin: true, PasswordHash: testHash, PasswordSalt: testSalt}

type userInfo struct {
	Account           string
	Nick              string
	Admin             bool
	PasswordHash      string
	PasswordSalt      string
	AttemptStartDelta time.Duration
	AttemptStart      time.Time
	AttemptCount      int
}

func (s *AdminSuite) TestAdmin(c *C) {
	for i, test := range adminTests {
		summary := test.summary
		if summary == "" {
			summary = fmt.Sprintf("%v", test.send)
		}
		c.Logf("Test #%d: %s", i, summary)
		s.testAdmin(c, &test)
	}
}

func (s *AdminSuite) testAdmin(c *C, test *adminTest) {
	db, err := mup.OpenDB(c.MkDir())
	c.Assert(err, IsNil)
	defer db.Close()

	tester := mup.NewPluginTester("admin")
	tester.SetDatabase(db)

	accounts := map[string]bool{"test": true}
	for _, user := range test.users {
		accounts[user.Account] = true
	}
	for account := range accounts {
		_, err = db.Exec("INSERT INTO account (name) VALUES (?)", account)
		c.Assert(err, IsNil)
	}

	now := time.Now()
	if test.login && len(test.users) == 0 {
		test.users = append(test.users, testUser)
	}
	for _, user := range test.users {
		user.AttemptStart = now.Add(user.AttemptStartDelta)
		if user.PasswordHash == "" {
			user.PasswordHash = testHash
			user.PasswordSalt = testSalt
		}

		_, err := db.Exec("INSERT INTO user (account,nick,password_hash,password_salt,attempt_start,attempt_count,admin) VALUES (?,?,?,?,?,?,?)",
			user.Account, user.Nick, user.PasswordHash, user.PasswordSalt, user.AttemptStart, user.AttemptCount, user.Admin)
		c.Assert(err, IsNil)
	}

	tester.Start()

	if test.login {
		tester.Sendf("login thesecret")
		c.Assert(tester.Recv(), Equals, "PRIVMSG nick :Okay.")
	}

	tester.SendAll(test.send)
	tester.Stop()
	c.Assert(tester.RecvAll(), DeepEquals, test.recv)
}
