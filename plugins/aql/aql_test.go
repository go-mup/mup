package mup_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/mup.v0"
	"gopkg.in/mup.v0/ldap"
	_ "gopkg.in/mup.v0/plugins/aql"

	. "gopkg.in/check.v1"
)

func Test(t *testing.T) { TestingT(t) }

var _ = Suite(&S{})

type S struct{}

type smsTest struct {
	send         []string
	recv         []string
	fail         bool
	config       mup.Map
	targets      []mup.Target
	messages     []aqlMessage
	endpointForm url.Values
	retrieveForm url.Values
	deletedKeys  []int
}

var smsTests = []smsTest{{
	send:   []string{"sms noldap Hey there"},
	recv:   []string{`PRIVMSG nick :Plugin configuration error: LDAP connection "unknown" not found.`},
	config: mup.Map{"ldap": "unknown"},
}, {
	send: []string{"sms notfound Hey there"},
	recv: []string{"PRIVMSG nick :Cannot find anyone with that IRC nick in the directory. :-("},
}, {
	send: []string{"sms tesla Fail please"},
	recv: []string{"PRIVMSG nick :SMS delivery failed: Error message from endpoint."},
	fail: true,
}, {
	send: []string{"[#chan] sms tesla Ignore me."},
}, {
	send: []string{"[#chan] mup: sms tesla Hey there"},
	recv: []string{"PRIVMSG #chan :nick: SMS is on the way!"},
	config: mup.Map{
		"aqluser": "myuser",
		"aqlpass": "mypass",
	},
	endpointForm: url.Values{
		"destination": {"+11223344"},
		"message":     {"#chan nick> Hey there"},
		"originator":  {"+447766404142"},
		"username":    {"myuser"},
		"password":    {"mypass"},
	},
}, {
	send: []string{"sms tésla Hey there"},
	recv: []string{"PRIVMSG nick :SMS is on the way!"},
	config: mup.Map{
		"aqluser": "myuser",
		"aqlpass": "mypass",
	},
	endpointForm: url.Values{
		"destination": {"+11223344"},
		"message":     {"nick> Hey there"},
		"originator":  {"+447766404142"},
		"username":    {"myuser"},
		"password":    {"mypass"},
	},
}, {
	recv: []string{
		"[@one] PRIVMSG nick :[SMS] <++99> A",
		"[@three] PRIVMSG nick :[SMS] <++99> A",
		"[@two] PRIVMSG #chan :[SMS] <tesla> B",
		"[@two] PRIVMSG #chan :Answer with: !sms tesla <your message>",
		"[@three] PRIVMSG #chan :[SMS] <tesla> B",
		"[@three] PRIVMSG #chan :Answer with: !sms tesla <your message>",
	},
	config: mup.Map{
		"aqlkeyword": "yo",
		"polldelay":  "100ms",
	},
	targets: []mup.Target{
		{Account: "one", Nick: "nick"},
		{Account: "two", Channel: "#chan"},
		{Account: "three"},
	},
	messages: []aqlMessage{
		{Key: 12, Message: "nick A", Sender: "+99"},
		{Key: 34, Message: "#chan B", Sender: "+55"},
	},
	retrieveForm: url.Values{
		"keyword": {"yo"},
	},
	deletedKeys: []int{12, 34},
}}

func (s *S) SetUpTest(c *C) {
	mup.SetLogger(c)
	mup.SetDebug(true)
}

func (s *S) TearDownTest(c *C) {
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

		if test.config == nil {
			test.config = mup.Map{}
		}
		if test.config["ldap"] == nil {
			test.config["ldap"] = "test"
		}
		test.config["aqlendpoint"] = server.URL() + "/endpoint"
		test.config["aqlproxy"] = server.URL() + "/proxy"

		tester := mup.NewPluginTester("aql")
		tester.SetConfig(test.config)
		tester.SetTargets(test.targets)
		tester.SetLDAP("test", ldapConn{})
		tester.Start()
		tester.SendAll(test.send)

		if test.messages != nil {
			time.Sleep(200 * time.Millisecond)
		}

		c.Check(tester.Stop(), IsNil)
		c.Check(tester.RecvAll(), DeepEquals, test.recv)

		server.Stop()

		if test.endpointForm != nil {
			c.Assert(server.endpointForm, DeepEquals, test.endpointForm)
		}
		if test.retrieveForm != nil {
			c.Assert(server.retrieveForm, DeepEquals, test.retrieveForm)
		}
		if test.deletedKeys != nil {
			c.Assert(server.deletedKeys, DeepEquals, test.deletedKeys)
		}

		if c.Failed() {
			c.FailNow()
		}
	}
}

type ldapConn struct{}

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
	`(mozillaNickname=tesla)`:      {nikolaTesla},
	`(mozillaNickname=t\c3\a9sla)`: {nikolaTesla},
	"(mobile=*5*5*)":               {nikolaTesla},
}

func (l ldapConn) Search(s *ldap.Search) ([]ldap.Result, error) {
	if strings.Contains(s.Filter, "=slow)") {
		time.Sleep(150 * time.Millisecond)
	}
	return ldapResults[s.Filter], nil
}

func (l ldapConn) Close() error { return nil }

type aqlServer struct {
	fail     bool
	messages []aqlMessage

	endpointForm url.Values
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
	case "/endpoint":
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
	s.endpointForm = req.Form
	if s.fail {
		w.Write([]byte("2:0 Error message from endpoint."))
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
