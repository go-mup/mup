package admin_test

import (
	"fmt"
	"testing"
	"time"

	. "gopkg.in/check.v1"
	"gopkg.in/mup.v0"
	"gopkg.in/mup.v0/plugins/admin"
)

func Test(t *testing.T) { TestingT(t) }

var _ = Suite(&AdminSuite{})

type AdminSuite struct {
	dbserver mup.DBServerHelper
}

func (s *AdminSuite) SetUpSuite(c *C) {
	s.dbserver.SetPath(c.MkDir())
}

func (s *AdminSuite) TearDownSuite(c *C) {
	s.dbserver.Stop()
}

func (s *AdminSuite) SetUpTest(c *C) {
	mup.SetLogger(c)
	mup.SetDebug(true)
}

func (s *AdminSuite) TearDownTest(c *C) {
	s.dbserver.Wipe()
	mup.SetLogger(nil)
	mup.SetDebug(false)
}

type adminTest struct {
	summary string
	target  string
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
		summary: "Burst control: quota limit",
		users: []userInfo{{
			Account:           "test",
			Nick:              "nick",
			Password:          "thesecret",
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
			Password:     "thesecret",
			AttemptCount: admin.BurstQuota - 1,
		}},
		send: []string{"login thesecret", "login thesecret"},
		recv: []string{"PRIVMSG nick :Okay.", "PRIVMSG nick :Okay."},
	}, {
		summary: "Burst control: normal window expired",
		users: []userInfo{{
			Account:           "test",
			Nick:              "nick",
			Password:          "thesecret",
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
			Password:          "thesecret",
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
			Password:          "thesecret",
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
		recv:  []string{"[other] PRIVMSG bar :text", "PRIVMSG nick :Done."},
	},
}

type userInfo struct {
	Account           string
	Nick              string
	Password          string
	AttemptStartDelta time.Duration
	AttemptStart      time.Time
	AttemptCount      int
}

var testUser = userInfo{Account: "test", Nick: "nick", Password: "thesecret"}

func (s *AdminSuite) TestAdmin(c *C) {
	for i, test := range adminTests {
		summary := test.summary
		if summary == "" {
			summary = fmt.Sprint("%q", test.send)
		}
		c.Logf("Test #%d: %s", i, summary)
		s.testAdmin(c, &test)
	}
}

func (s *AdminSuite) testAdmin(c *C, test *adminTest) {
	defer s.dbserver.Wipe()

	session := s.dbserver.Session()
	defer session.Close()

	db := session.DB("")
	users := db.C("users")

	tester := mup.NewPluginTester("admin")
	tester.SetDatabase(db)

	now := time.Now()
	for _, user := range test.users {
		user.AttemptStart = now.Add(user.AttemptStartDelta)
		err := users.Insert(user)
		c.Assert(err, IsNil)
	}
	if test.login && len(test.users) == 0 {
		err := users.Insert(testUser)
		c.Assert(err, IsNil)
	}

	tester.Start()

	if test.login {
		tester.Sendf(test.target, "login thesecret")
		c.Assert(tester.Recv(), Equals, "PRIVMSG nick :Okay.")
	}

	tester.SendAll("", test.send)
	tester.Stop()
	c.Assert(tester.RecvAll(), DeepEquals, test.recv)
}
