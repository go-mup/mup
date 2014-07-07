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
	"time"
)

func Test(t *testing.T) { TestingT(t) }

var _ = Suite(&S{})

type S struct{}

type lpTest struct {
	plugin   string
	target   string
	send     []string
	recv     []string
	settings bson.M
	bugsText [][]int
	bugsForm url.Values
}

var lpTests = []lpTest{
	{
		plugin: "lpshowbugs",
		send:   []string{"bug #123"},
		recv:   []string{"PRIVMSG nick :Bug #123: Title of 123 <tag1> <tag2> <Some Project:New> <Other:Confirmed for joe> <https://launchpad.net/bugs/123>"},
	}, {
		plugin: "lptrackbugs",
		settings: bson.M{
			"project":   "some-project",
			"polldelay": "50ms",
			"prefixnew": "Bug #%d is new",
			"prefixold": "Bug #%d is old",
			"options":   "foo=bar",
		},
		bugsText: [][]int{{111, 333, 444, 555}, {111, 222, 444, 666}},
		bugsForm: url.Values{
			"foo": {"bar"},
		},
		recv:   []string{
			"PRIVMSG #mup-test :Bug #222 is new: Title of 222 <https://launchpad.net/bugs/222>",
			"PRIVMSG #mup-test :Bug #333 is old: Title of 333 <https://launchpad.net/bugs/333>",
			"PRIVMSG #mup-test :Bug #555 is old: Title of 555 <https://launchpad.net/bugs/555>",
			"PRIVMSG #mup-test :Bug #666 is new: Title of 666 <https://launchpad.net/bugs/666>",
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
		}
		server.Start()
		if test.settings == nil {
			test.settings = bson.M{}
		}
		test.settings["baseurl"] = server.URL()
		tester := mup.StartPluginTest(test.plugin, test.settings)
		tester.SendAll(test.target, test.send)
		if test.settings["polldelay"] != "" {
			time.Sleep(250 * time.Millisecond)
		}
		tester.Stop()
		server.Stop()
		c.Assert(tester.RecvAll(), DeepEquals, test.recv)

		if test.bugsForm != nil {
			c.Assert(server.bugsForm, DeepEquals, test.bugsForm)
		}
	}
}

type lpServer struct {
	server *httptest.Server

	bugForm url.Values

	bugsText [][]int
	bugsForm url.Values

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
	case strings.HasPrefix(req.URL.Path, "/some-project/+bugs-text"):
		s.serveBugsText(w, req)
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
	for i, bugs := range s.bugsText {
		if bugs == nil {
			continue
		}
		if i < len(s.bugsText)-1 {
			s.bugsText[i] = nil
		}
		for _, bugId := range bugs {
			w.Write([]byte(strconv.Itoa(bugId)))
			w.Write([]byte{'\n'})
		}
		break
	}
}
