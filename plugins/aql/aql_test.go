package mup_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	. "gopkg.in/check.v1"

	"gopkg.in/niemeyer/mup.v0"
	"gopkg.in/niemeyer/mup.v0/ldap"
	_ "gopkg.in/niemeyer/mup.v0/plugins/aql"

	"labix.org/v2/mgo/bson"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func Test(t *testing.T) { TestingT(t) }

var _ = Suite(&S{})

type S struct {
	ldapSettings *ldap.Settings
}

type smsTest struct {
	target       string
	send         []string
	recv         []string
	fail         bool
	settings     bson.M
	messages     []aqlMessage
	gatewayForm  url.Values
	retrieveForm url.Values
	deletedKeys  []int
}

var smsTests = []smsTest{{
	send: []string{"sms notfound Hey there"},
	recv: []string{"PRIVMSG nick :Cannot find anyone with that IRC nick in the directory. :-("},
}, {
	send: []string{"sms tesla Fail please"},
	recv: []string{"PRIVMSG nick :SMS delivery failed: Error message from gateway."},
	fail: true,
}, {
	target: "#channel",
	send: []string{"sms tesla Ignore me."},
}, {
	send: []string{"sms tesla Hey there"},
	recv: []string{"PRIVMSG nick :SMS is on the way!"},
	settings: bson.M{
		"aqluser": "myuser",
		"aqlpass": "mypass",
	},
	gatewayForm: url.Values{
		"destination": {"+11223344"},
		"message":     {"nick> Hey there"},
		"originator":  {"+447766404142"},
		"username":    {"myuser"},
		"password":    {"mypass"},
	},
}, {
	target: "#channel",
	send:   []string{"mup: sms tesla Hey there"},
	recv:   []string{"PRIVMSG #channel :nick: SMS is on the way!"},
	settings: bson.M{
		"aqluser": "myuser",
		"aqlpass": "mypass",
	},
	gatewayForm: url.Values{
		"destination": {"+11223344"},
		"message":     {"#channel nick> Hey there"},
		"originator":  {"+447766404142"},
		"username":    {"myuser"},
		"password":    {"mypass"},
	},
}, {
	recv: []string{
		"PRIVMSG mup :[SMS] <++99> One",
		"PRIVMSG #ch :[SMS] <tesla> Two",
		"PRIVMSG #ch :Answer with: !sms tesla <your message>",
	},
	settings: bson.M{
		"aqlkeyword": "yo",
		"polldelay":  100,
	},
	messages: []aqlMessage{
		{Key: 12, Message: "mup One", Sender: "+99"},
		{Key: 34, Message: "#ch Two", Sender: "+55"},
	},
	retrieveForm: url.Values{
		"keyword": {"yo"},
	},
	deletedKeys: []int{12, 34},
}}

func (s *S) SetUpSuite(c *C) {
	ldap.TestDial = func(settings *ldap.Settings) (ldap.Conn, error) {
		s.ldapSettings = settings
		return &ldapConn{settings}, nil
	}
	mup.SetLogger(c)
	mup.SetDebug(true)
}

func (s *S) TearDownSuite(c *C) {
	ldap.TestDial = nil
	mup.SetLogger(nil)
	mup.SetDebug(false)
}

func (s *S) TestSMS(c *C) {
	for i, test := range smsTests {
		c.Logf("Running test %d with messages: %v", i, test.send)

		server := &aqlServer{
			fail:     test.fail,
			messages: test.messages,
		}
		server.Start()

		if test.settings == nil {
			test.settings = bson.M{}
		}
		test.settings["aqlgateway"] = server.URL() + "/gateway"
		test.settings["aqlproxy"] = server.URL() + "/proxy"
		test.settings["ldap"] = "the-ldap-server"

		tester := mup.StartPluginTest("aql", test.settings)
		tester.SendAll(test.target, test.send)

		if test.messages != nil {
			time.Sleep(200 * time.Millisecond)
		}

		c.Check(tester.Stop(), IsNil)
		c.Check(tester.RecvAll(), DeepEquals, test.recv)

		server.Stop()

		if test.gatewayForm != nil {
			c.Assert(server.gatewayForm, DeepEquals, test.gatewayForm)
		}
		if test.retrieveForm != nil {
			c.Assert(server.retrieveForm, DeepEquals, test.retrieveForm)
		}
		if test.deletedKeys != nil {
			c.Assert(server.deletedKeys, DeepEquals, test.deletedKeys)
		}

		c.Assert(s.ldapSettings.LDAP, Equals, "the-ldap-server")

		if c.Failed() {
			c.FailNow()
		}
	}
}

type ldapConn struct {
	s *ldap.Settings
}

var nikolaTesla = ldap.Result{
	Attrs: []ldap.Attr{
		{Name: "mozillaNickname", Values: []string{"tesla"}},
		{Name: "cn", Values: []string{"Nikola Tesla"}},
		{Name: "mail", Values: []string{"tesla@example.com"}},
		{Name: "mobile", Values: []string{"+11 (22) 33-44", "+55"}},
	},
}

func ldapResult(nick, name string) ldap.Result {
	return ldap.Result{Attrs: []ldap.Attr{
		{Name: "mozillaNickname", Values: []string{nick}},
		{Name: "cn", Values: []string{name}},
		{Name: "mail", Values: []string{nick + "@example.com"}},
	}}
}

var ldapResults = map[string][]ldap.Result{
	"(mozillaNickname=tesla)": {nikolaTesla},
	"(mobile=*5*5*)":          {nikolaTesla},
}

func (l *ldapConn) Search(s *ldap.Search) ([]ldap.Result, error) {
	if strings.Contains(s.Filter, "=slow)") {
		time.Sleep(150 * time.Millisecond)
	}
	return ldapResults[s.Filter], nil
}

func (l *ldapConn) Ping() error  { return nil }
func (l *ldapConn) Close() error { return nil }

type aqlServer struct {
	fail     bool
	messages []aqlMessage

	gatewayForm  url.Values
	retrieveForm url.Values
	deletedKeys  []int

	server *httptest.Server
}

type aqlMessage struct {
	Key     int    `json:"key"`
	Message string `json:"message"`
	Sender  string `json:"sender"`
	Time    string `json:"time"`
}

func (s *aqlServer) Start() {
	s.server = httptest.NewServer(s)
}

func (s *aqlServer) Stop() {
	s.server.Close()
}

func (s *aqlServer) URL() string {
	return s.server.URL
}

func (s *aqlServer) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	req.ParseForm()
	switch req.URL.Path {
	case "/gateway":
		s.serveGateway(w, req)
	case "/proxy/retrieve":
		s.serveRetrieve(w, req)
	case "/proxy/delete":
		s.serveDelete(w, req)
	default:
		panic("Got unexpected request for " + req.URL.Path + " in test aqlServer")
	}
}

func (s *aqlServer) serveGateway(w http.ResponseWriter, req *http.Request) {
	s.gatewayForm = req.Form
	if s.fail {
		w.Write([]byte("2:0 Error message from gateway."))
		return
	}
	w.Write([]byte("1:1 Okay."))
}

func (s *aqlServer) serveRetrieve(w http.ResponseWriter, req *http.Request) {
	s.retrieveForm = req.Form
	data, err := json.Marshal(s.messages)
	if err != nil {
		panic("cannot marshal SMS messages inside the fake proxy: " + err.Error())
	}
	w.Write(data)
}

func (s *aqlServer) serveDelete(w http.ResponseWriter, req *http.Request) {
	for _, keyStr := range strings.Split(req.FormValue("keys"), ",") {
		key, err := strconv.Atoi(keyStr)
		if err != nil {
			panic("cannot convert deleted key to int in proxy: " + keyStr)
		}
		s.deletedKeys = append(s.deletedKeys, key)
		for i, sms := range s.messages {
			if sms.Key == key {
				copy(s.messages[i:], s.messages[i+1:])
				s.messages = s.messages[:len(s.messages)-1]
				break
			}
		}
	}
}
