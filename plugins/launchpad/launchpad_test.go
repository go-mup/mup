package launchpad_test

import (
	"fmt"
	"testing"

	. "gopkg.in/check.v1"
	"gopkg.in/niemeyer/mup.v0"
	_ "gopkg.in/niemeyer/mup.v0/plugins/launchpad"
	"labix.org/v2/mgo/bson"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
)

func Test(t *testing.T) { TestingT(t) }

var _ = Suite(&S{})

type S struct{}

type lpTest struct {
	target   string
	send     string
	recv     string
	settings bson.M
}

var lpTests = []lpTest{
	{"mup", "bug #123", "PRIVMSG nick :Bug #123: Title of 123 <tag1> <tag2> <Some Project:New> <Other:Confirmed for joe> <https://launchpad.net/bugs/123>", nil},
}

func (s *S) SetUpTest(c *C) {
	mup.SetLogger(c)
	mup.SetDebug(true)
}

func (s *S) TearDownTest(c *C) {
	mup.SetLogger(nil)
	mup.SetDebug(false)
}

func (s *S) TestBugs(c *C) {
	for i, test := range lpTests {
		c.Logf("Testing message #%d: %s", i, test.send)
		server := lpServer{}
		server.Start()
		if test.settings == nil {
			test.settings = bson.M{}
		}
		test.settings["baseurl"] = server.URL()
		tester := mup.StartPluginTest("launchpad", test.settings)
		tester.Sendf(test.target, test.send)
		tester.Stop()
		server.Stop()
		c.Assert(tester.Recv(), Equals, test.recv)
	}
}

type lpServer struct {
	server *httptest.Server

	bugForm url.Values
}

func (s *lpServer) Start() {
	s.server = httptest.NewServer(s)
}

func (s *lpServer) Stop() {
	s.server.Close()
}

func (s *lpServer) URL() string {
	return s.server.URL
}

func (s *lpServer) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	req.ParseForm()
	switch {
	case strings.HasPrefix(req.URL.Path, "/bugs/"):
		s.serveBug(w, req)
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
	var res string
	if tasks {
		res = fmt.Sprintf(`{"entries": [
			{"status": "New", "bug_target_display_name": "Some Project"},
			{"status": "Confirmed", "bug_target_display_name": "Other", "assignee_link": "foo/~joe"}
		]}`)
	} else {
		res = fmt.Sprintf(`{
			"title": "Title of %d",
			"tags": ["tag1", "tag2"],
			"bug_tasks_collection_link": "%s/bugs/%d/bug_tasks"
		}`, id, s.URL(), id)
	}
	w.Write([]byte(res))
}
