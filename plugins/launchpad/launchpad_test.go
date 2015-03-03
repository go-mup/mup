package launchpad_test

import (
	"fmt"
	"testing"

	. "gopkg.in/check.v1"
	"gopkg.in/mgo.v2/bson"
	"gopkg.in/mup.v0"
	_ "gopkg.in/mup.v0/plugins/launchpad"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func Test(t *testing.T) { TestingT(t) }

var _ = Suite(&S{})

type S struct{}

type lpTest struct {
	plugin   string
	send     []string
	recv     []string
	config   bson.M
	targets  []bson.M
	bugsText [][]int
	bugsForm url.Values
	status   int
	headers  map[string]bson.M
}

var lpTests = []lpTest{
	{
		// Bug ids are numeric.
		plugin: "lpbugdata",
		send:   []string{"bug foo"},
		recv:   []string{"PRIVMSG nick :Oops: cannot parse bug id from argument: foo"},
	}, {
		// The trivial case.
		plugin: "lpbugdata",
		send:   []string{"bug #123"},
		recv:   []string{"PRIVMSG nick :Bug #123: Title of 123 <tag1> <tag2> <Some Project:New> <Other:Confirmed for joe> <https://launchpad.net/bugs/123>"},
	}, {
		// The bug command reports errors.
		plugin: "lpbugdata",
		status: 500,
		send:   []string{"bug #123"},
		recv:   []string{"PRIVMSG nick :Oops: cannot perform Launchpad request: 500 Internal Server Error"},
	}, {
		plugin: "lpbugdata",
		status: 404,
		send:   []string{"bug #123"},
		recv:   []string{"PRIVMSG nick :Bug not found."},
	}, {
		// Multiple bugs in a single command. Repetitions are dropped.
		plugin: "lpbugdata",
		send:   []string{"bug 111 #111 +bug/222 bugs/333"},
		recv: []string{
			"PRIVMSG nick :Bug #111: Title of 111 <https://launchpad.net/bugs/111>",
			"PRIVMSG nick :Bug #222: Title of 222 <https://launchpad.net/bugs/222>",
			"PRIVMSG nick :Bug #333: Title of 333 <https://launchpad.net/bugs/333>",
		},
	}, {
		// Overhearing is disabled by default.
		plugin:  "lpbugdata",
		targets: []bson.M{{"account": ""}},
		send:    []string{"[#chan] foo bug #111"},
		recv:    []string(nil),
	}, {
		// With overhearing enabled third-party messages are observed.
		plugin:  "lpbugdata",
		config:  bson.M{"overhear": true},
		targets: []bson.M{{"account": ""}},
		send:    []string{"[#chan] foo bug #111"},
		recv:    []string{"PRIVMSG #chan :Bug #111: Title of 111 <https://launchpad.net/bugs/111>"},
	}, {
		// When overhearing, do not report errors.
		plugin:  "lpbugdata",
		config:  bson.M{"overhear": true},
		targets: []bson.M{{"account": ""}},
		status:  500,
		send:    []string{"[#chan] foo bug #111"},
		recv:    []string(nil),
	}, {
		// Overhearing may be enabled on the target configuration.
		plugin: "lpbugdata",
		targets: []bson.M{
			{"account": "", "config": bson.M{"overhear": true}},
		},
		send: []string{"[#chan] foo bug #111"},
		recv: []string{"PRIVMSG #chan :Bug #111: Title of 111 <https://launchpad.net/bugs/111>"},
	}, {
		// First matching target wins.
		plugin: "lpbugdata",
		targets: []bson.M{
			{"channel": "#chan", "config": bson.M{"overhear": false}},
			{"account": "", "config": bson.M{"overhear": true}},
		},
		send: []string{"[#chan] foo bug #111"},
		recv: []string(nil),
	}, {
		// Polling of bug changes.
		plugin: "lpbugwatch",
		config: bson.M{
			"project":   "some-project",
			"polldelay": "50ms",
			"prefixnew": "Bug #%d is new",
			"prefixold": "Bug #%d is old",
			"options":   "foo=bar",
		},
		targets: []bson.M{
			{"account": "test", "channel": "#chan"},
		},
		bugsText: [][]int{{111, 333, 404, 444, 555}, {111, 222, 444, 666}},
		bugsForm: url.Values{
			"foo": {"bar"},
		},
		recv: []string{
			"PRIVMSG #chan :Bug #222 is new: Title of 222 <https://launchpad.net/bugs/222>",
			"PRIVMSG #chan :Bug #333 is old: Title of 333 <https://launchpad.net/bugs/333>",
			"PRIVMSG #chan :Bug #555 is old: Title of 555 <https://launchpad.net/bugs/555>",
			"PRIVMSG #chan :Bug #666 is new: Title of 666 <https://launchpad.net/bugs/666>",
		},
	}, {
		// Polling of merge changes.
		plugin: "lpmergewatch",
		config: bson.M{
			"project":   "some-project",
			"polldelay": "50ms",
		},
		targets: []bson.M{
			{"account": "test", "channel": "#chan"},
		},
		recv: []string{
			"PRIVMSG #chan :Merge proposal changed [needs review]: Branch description. <https://launchpad.net/~user/+merge/111>",
			"PRIVMSG #chan :Merge proposal changed [merged]: Branch description. <https://launchpad.net/~user/+merge/333>",
			"PRIVMSG #chan :Merge proposal changed [approved]: Branch description. <https://launchpad.net/~user/+merge/111>",
			"PRIVMSG #chan :Merge proposal changed [rejected]: Branch description with a very long first line that never ends and continues (...) <https://launchpad.net/~user/+merge/444>",
		},
	}, {
		// OAuth authorization header.
		plugin: "lpbugdata",
		config: bson.M{
			"oauthaccesstoken": "atok",
			"oauthsecrettoken": "stok",
		},
		send: []string{"bug 111"},
		recv: []string{"PRIVMSG nick :Bug #111: Title of 111 <https://launchpad.net/bugs/111>"},
		headers: map[string]bson.M{
			"/bugs/111": {
				"Authorization": `` +
					`OAuth realm="https://api.launchpad.net",` +
					` oauth_consumer_key="mup",` +
					` oauth_signature_method="PLAINTEXT",` +
					` oauth_token="atok",` +
					` oauth_signature="&stok",` +
					` oauth_nonce="NNNNN",` +
					` oauth_timestamp="NNNNN"`,
			},
		},
	}, {
		// Auth cookie header.
		plugin: "lpbugwatch",
		config: bson.M{
			"project":   "some-project",
			"polldelay": "50ms",
			"prefixnew": "Bug #%d is new",

			"authcookie":       "lpcookie",
			"oauthaccesstoken": "atok",
			"oauthsecrettoken": "stok",
		},
		targets: []bson.M{
			{"account": "test", "channel": "#chan"},
		},
		bugsText: [][]int{{111}, {111, 222}},
		recv:     []string{"PRIVMSG #chan :Bug #222 is new: Title of 222 <https://launchpad.net/bugs/222>"},
		headers: map[string]bson.M{
			"/bugs/222": {
				"Cookie": "lp=lpcookie",
				"Authorization": `` +
					`OAuth realm="https://api.launchpad.net",` +
					` oauth_consumer_key="mup",` +
					` oauth_signature_method="PLAINTEXT",` +
					` oauth_token="atok",` +
					` oauth_signature="&stok",` +
					` oauth_nonce="NNNNN",` +
					` oauth_timestamp="NNNNN"`,
			},
			"/some-project/+bugs-text": {
				"Cookie": "lp=lpcookie",
				"Authorization": `` +
					`OAuth realm="https://api.launchpad.net",` +
					` oauth_consumer_key="mup",` +
					` oauth_signature_method="PLAINTEXT",` +
					` oauth_token="atok",` +
					` oauth_signature="&stok",` +
					` oauth_nonce="NNNNN",` +
					` oauth_timestamp="NNNNN"`,
			},
		},
	}, {
		// Basic contributor agreement query.
		plugin: "lpcontrib",
		send:   []string{"contrib some user"},
		recv: []string{
			"PRIVMSG nick :2 matching contributors signed the agreement:",
			"PRIVMSG nick : — Amuser Jeff <https://launchpad.net/~muser> <muser@prefemail.com> <muser@email.com> <muser@example.com>",
			"PRIVMSG nick : — Awesome Joe <https://launchpad.net/~wsome>",
		},
	},
}

func (s *S) SetUpTest(c *C) {
	mup.SetLogger(c)
	mup.SetDebug(true)
}

func (s *S) TearDownTest(c *C) {
	mup.SetLogger(nil)
	mup.SetDebug(false)
}

func (s *S) TestLaunchpad(c *C) {
	for i, test := range lpTests {
		c.Logf("Testing message #%d: %s", i, test.send)
		server := lpServer{
			bugsText: test.bugsText,
			status:   test.status,
		}
		server.Start()
		if test.config == nil {
			test.config = bson.M{}
		}
		test.config["endpoint"] = server.URL()
		test.config["buglistendpoint"] = server.URL()
		tester := mup.NewPluginTester(test.plugin)
		tester.SetConfig(test.config)
		tester.SetTargets(test.targets)
		tester.Start()
		tester.SendAll(test.send)
		if test.config["polldelay"] != "" {
			time.Sleep(250 * time.Millisecond)
		}
		tester.Stop()
		server.Stop()
		c.Assert(tester.RecvAll(), DeepEquals, test.recv)

		if test.bugsForm != nil {
			c.Assert(server.bugsForm, DeepEquals, test.bugsForm)
		}
		if len(test.headers) > 0 {
			for url, headers := range test.headers {
				for name, value := range headers {
					header := server.headers[url].Get(name)
					header = regexp.MustCompile("[0-9]{5,}").ReplaceAllString(header, "NNNNN")
					c.Assert(header, Equals, value)
				}
			}
		}
	}
}

func (s *S) TestJustShown(c *C) {
	server := lpServer{}
	server.Start()
	tester := mup.NewPluginTester("lpbugdata")
	tester.SetConfig(bson.M{
		"endpoint":         server.URL(),
		"overhear":         true,
		"justshowntimeout": "200ms",
	})
	tester.SetTargets([]bson.M{{"account": ""}})
	tester.Start()
	tester.Sendf("[#chan1] foo bug 111")
	tester.Sendf("[#chan2] foo bug 111")
	tester.Sendf("[#chan1] foo bug 222")
	tester.Sendf("[#chan1] foo bug 111")
	tester.Sendf("[#chan2] foo bug 111")
	tester.Sendf("[#chan1] foo bug 333")
	time.Sleep(250 * time.Millisecond)
	tester.Sendf("[#chan1] foo bug 111")
	tester.Sendf("[#chan2] foo bug 111")
	tester.Sendf("[#chan1] foo bug 444")
	tester.Stop()
	server.Stop()

	c.Assert(tester.RecvAll(), DeepEquals, []string{
		"PRIVMSG #chan1 :Bug #111: Title of 111 <https://launchpad.net/bugs/111>",
		"PRIVMSG #chan2 :Bug #111: Title of 111 <https://launchpad.net/bugs/111>",
		"PRIVMSG #chan1 :Bug #222: Title of 222 <https://launchpad.net/bugs/222>",
		"PRIVMSG #chan1 :Bug #333: Title of 333 <https://launchpad.net/bugs/333>",
		"PRIVMSG #chan1 :Bug #111: Title of 111 <https://launchpad.net/bugs/111>",
		"PRIVMSG #chan2 :Bug #111: Title of 111 <https://launchpad.net/bugs/111>",
		"PRIVMSG #chan1 :Bug #444: Title of 444 <https://launchpad.net/bugs/444>",
	})
}

type lpServer struct {
	server *httptest.Server

	status int

	bugForm url.Values

	bugsForm url.Values
	bugsText [][]int
	bugsResp int

	mergesResp int

	headers map[string]http.Header
}

func (s *lpServer) Start() {
	s.server = httptest.NewServer(s)
	s.headers = make(map[string]http.Header)
}

func (s *lpServer) Stop() {
	s.server.Close()
}

func (s *lpServer) URL() string {
	return s.server.URL
}

func (s *lpServer) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	s.headers[req.URL.Path] = req.Header
	if s.status != 0 {
		w.WriteHeader(s.status)
		return
	}
	req.ParseForm()
	switch {
	case strings.HasPrefix(req.URL.Path, "/bugs/"):
		s.serveBug(w, req)
	case strings.HasPrefix(req.URL.Path, "/some-project/+bugs-text"):
		s.serveBugsText(w, req)
	case strings.HasPrefix(req.URL.Path, "/some-project") && req.FormValue("ws.op") == "getMergeProposals":
		s.serveMerges(w, req)
	case strings.HasPrefix(req.URL.Path, "/people"):
		s.servePeople(w, req)
	default:
		panic("got unexpected request for " + req.URL.Path + " in test lpServer")
	}
}

func (s *lpServer) serveBug(w http.ResponseWriter, req *http.Request) {
	tasks := false
	path := strings.TrimPrefix(req.URL.Path, "/bugs/")
	if s := strings.TrimSuffix(path, "/bug_tasks"); s != path {
		tasks = true
		path = s
	}
	s.bugForm = req.Form
	id, err := strconv.Atoi(path)
	if err != nil {
		panic("invalid bug URL: " + req.URL.Path)
	}
	if id == 404 {
		w.WriteHeader(404)
		return
	}
	var res string
	if tasks {
		res = fmt.Sprintf(`{"entries": [
			{"status": "New", "bug_target_display_name": "Some Project"},
			{"status": "Confirmed", "bug_target_display_name": "Other", "assignee_link": "foo/~joe"}
		]}`)
	} else if id == 123 {
		res = fmt.Sprintf(`{
			"title": "Title of %d",
			"tags": ["tag1", "tag2"],
			"bug_tasks_collection_link": "%s/bugs/%d/bug_tasks"
		}`, id, s.URL(), id)
	} else {
		res = fmt.Sprintf(`{"title": "Title of %d"}`, id)
	}
	w.Write([]byte(res))
}

func (s *lpServer) serveBugsText(w http.ResponseWriter, req *http.Request) {
	s.bugsForm = req.Form
	for _, bugId := range s.bugsText[s.bugsResp] {
		w.Write([]byte(strconv.Itoa(bugId)))
		w.Write([]byte{'\n'})
	}
	if s.bugsResp+1 < len(s.bugsText) {
		s.bugsResp++
	}
}

// Merge proposal changed [needs review]: %s <%s>

func (s *lpServer) serveMerges(w http.ResponseWriter, req *http.Request) {
	e := []string{
		`{"queue_status": "Needs Review", "self_link": "http://foo/~user/+merge/999", "description": "Ignored."}`,
		`{"queue_status": "Needs Review", "self_link": "http://foo/~user/+merge/111", "description": "Branch description."}`,
		`{"queue_status": "Approved", "self_link": "http://foo/~user/+merge/111", "description": "Branch description. Foo."}`,
		`{"queue_status": "Merged", "self_link": "http://foo/~user/+merge/333", "description": "Branch description.\nFoo."}`,
		`{"queue_status": "Rejected", "self_link": "http://foo/~user/+merge/444",
		  "description": "Branch description with a very long first line that never ends and continues until being broken up."}`,
	}
	var entries []string
	switch s.mergesResp {
	case 0:
		entries = []string{e[0]}
		s.mergesResp++
	case 1:
		entries = []string{e[1]}
		s.mergesResp++
	case 2:
		entries = []string{e[1], e[3]}
		s.mergesResp++
	case 3:
		entries = []string{e[2], e[3], e[4]}
	}
	w.Write([]byte(`{"entries": [` + strings.Join(entries, ",") + `]}`))
}

func (s *lpServer) servePeople(w http.ResponseWriter, req *http.Request) {
	path := strings.TrimPrefix(req.URL.Path, "/people/")
	if user := strings.TrimSuffix(path, "/membership"); user != path {
		team := "contributor-agreement-canonical"
		status := "Approved"
		if user == "~other" {
			team = "other-team"
		}
		if user == "~another" {
			status = "Nope"
		}
		fmt.Fprintf(w, `{"entries": [{"team_link": "https://api.launchpad.net/1.0/~%s", "status": "%s"}]}`, team, status)
		return
	}

	if user := strings.TrimSuffix(path, "/emails"); user != path {
		if user == "~wsome" {
			w.Write([]byte("{}"))
			return
		}
		fmt.Fprintf(w, `{"entries": [{"email": "%s@email.com"}, {"email": "%s@example.com"}]}`, user[1:], user[1:])
		return
	}

	if user := strings.TrimSuffix(path, "/email"); user != path {
		if user != "~muser" {
			w.Write([]byte("{}"))
			return
		}
		fmt.Fprintf(w, `{"email": "%s@prefemail.com"}`, user[1:])
		return
	}

	entries := []string{
		`{"name": "wsome", "display_name": "Awesome Joe", "memberships_details_collection_link": "%s/people/~wsome/membership",
			"confirmed_email_addresses_collection_link": "%s/people/~wsome/emails",
			"preferred_email_address_link": "%s/people/~wsome/email"}`,
		`{"name": "other", "display_name": "Some Other", "memberships_details_collection_link": "%s/people/~other/membership",
			"confirmed_email_addresses_collection_link": "%s/people/~other/emails",
			"preferred_email_address_link": "%s/people/~other/email"}`,
		`{"name": "another", "display_name": "Some Another", "memberships_details_collection_link": "%s/people/~another/membership",
			"confirmed_email_addresses_collection_link": "%s/people/~another/emails",
			"preferred_email_address_link": "%s/people/~another/email"}`,
		`{"name": "muser", "display_name": "Amuser Jeff", "memberships_details_collection_link": "%s/people/~muser/membership",
			"confirmed_email_addresses_collection_link": "%s/people/~muser/emails",
			"preferred_email_address_link": "%s/people/~muser/email"}`,
	}
	for i := range entries {
		url := s.URL()
		entries[i] = fmt.Sprintf(entries[i], url, url, url)
	}
	fmt.Fprintf(w, `{"entries": [%s], "total_size": %d, "start": 0}`, strings.Join(entries, ","), len(entries))
}
