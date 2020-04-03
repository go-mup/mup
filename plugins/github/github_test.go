package github_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/mup.v0"
	_ "gopkg.in/mup.v0/plugins/launchpad"

	. "gopkg.in/check.v1"
)

func Test(t *testing.T) { TestingT(t) }

var _ = Suite(&S{})

type S struct{}

type ghTest struct {
	plugin   string
	send     []string
	recv     []string
	config   mup.Map
	targets  []mup.Target
	bugsText [][]int
	bugsForm url.Values
	status   int
	headers  map[string]mup.Map
}

var lpTests = []ghTest{
	{
		// Bug ids are numeric.
		plugin: "ghissuedata",
		send:   []string{"issue foo"},
		recv:   []string{"PRIVMSG nick :Oops: cannot parse issue or pull number from argument: foo"},
	}, {
		// The bug command reports errors.
		plugin: "ghissuedata",
		status: 500,
		send:   []string{"issue org/repo#123"},
		recv:   []string{"PRIVMSG nick :Oops: cannot perform GitHub request: 500 Internal Server Error"},
	}, {
		// Not found.
		plugin: "ghissuedata",
		status: 404,
		send:   []string{"issue org/repo#123"},
		recv:   []string{"PRIVMSG nick :Issue not found."},
	}, {
		// The trivial case.
		plugin: "ghissuedata",
		send:   []string{"issue org/repo#123"},
		recv:   []string{"PRIVMSG nick :Issue org/repo#123: Title of 123 <label1> <label2> <Created by joe> <https://github.com/org/repo/issue/123>"},
	}, {
		// Multiple issues in a single command. Repetitions are dropped.
		plugin: "ghissuedata",
		send:   []string{"issue org/repo#123 org/repo#123 org/repo#124"},
		recv: []string{
			"PRIVMSG nick :Issue org/repo#123: Title of 123 <label1> <label2> <Created by joe> <https://github.com/org/repo/issue/123>",
			"PRIVMSG nick :Issue org/repo#124: Title of 124 <Created by joe> <https://github.com/org/repo/issue/124>",
		},
	}, {
		// Project configured to org.
		plugin: "ghissuedata",
		config: mup.Map{"project": "org"},
		send:   []string{"issue repo#1", "issue other/repo#2"},
		recv: []string{
			"PRIVMSG nick :Issue repo#1: Title of 1 <Created by joe> <https://github.com/org/repo/issue/1>",
			"PRIVMSG nick :Issue other/repo#2: Title of 2 <Created by joe> <https://github.com/other/repo/issue/2>",
		},
	}, {
		// Project configured to repo.
		plugin: "ghissuedata",
		config: mup.Map{"project": "org/repo"},
		send:   []string{"issue #1", "issue other#2", "issue other/repo#3"},
		recv: []string{
			"PRIVMSG nick :Issue #1: Title of 1 <Created by joe> <https://github.com/org/repo/issue/1>",
			"PRIVMSG nick :Issue other#2: Title of 2 <Created by joe> <https://github.com/org/other/issue/2>",
			"PRIVMSG nick :Issue other/repo#3: Title of 3 <Created by joe> <https://github.com/other/repo/issue/3>",
		},
	}, {
		// Overhearing is disabled by default.
		plugin:  "ghissuedata",
		targets: []mup.Target{{Account: ""}},
		send:    []string{"[#chan] #111"},
		recv:    []string(nil),
	}, {
		// With overhearing enabled third-party messages are observed.
		plugin:  "ghissuedata",
		config:  mup.Map{"overhear": true},
		targets: []mup.Target{{Account: ""}},
		send:    []string{"[#chan] org/repo#1"},
		recv:    []string{"PRIVMSG #chan :Issue org/repo#1: Title of 1 <Created by joe> <https://github.com/org/repo/issue/1>"},
	}, {
		// When overhearing, do not report errors.
		plugin:  "ghissuedata",
		config:  mup.Map{"overhear": true},
		targets: []mup.Target{{Account: ""}},
		status:  500,
		send:    []string{"[#chan] org/repo#123"},
		recv:    []string(nil),
	}, {
		// Overhearing may be enabled on the target configuration.
		plugin: "ghissuedata",
		targets: []mup.Target{
			{Account: "", Config: `{"overhear": true}`},
		},
		send: []string{"[#chan] org/repo#1"},
		recv: []string{"PRIVMSG #chan :Issue org/repo#1: Title of 1 <Created by joe> <https://github.com/org/repo/issue/1>"},
	}, {
		// First matching target wins.
		plugin: "ghissuedata",
		targets: []mup.Target{
			{Channel: "#chan", Config: `{"overhear": false}`},
			{Account: "", Config: `{"overhear": true}`},
		},
		send: []string{"[#chan] org/repo#1"},
		recv: []string(nil),
	}, {
		// Overhearing with project configured to org.
		plugin:  "ghissuedata",
		config:  mup.Map{"overhear": true, "project": "org"},
		targets: []mup.Target{{Account: ""}},
		send:    []string{"[#chan] repo#1", "[#chan] other/repo#2"},
		recv: []string{
			"PRIVMSG #chan :Issue repo#1: Title of 1 <Created by joe> <https://github.com/org/repo/issue/1>",
			"PRIVMSG #chan :Issue other/repo#2: Title of 2 <Created by joe> <https://github.com/other/repo/issue/2>",
		},
	}, {
		// Overhearing with project configured to repo.
		plugin:  "ghissuedata",
		config:  mup.Map{"overhear": true, "project": "org/repo"},
		targets: []mup.Target{{Account: ""}},
		send:    []string{"[#chan] issue #1", "[#chan] issue other#2", "[#chan] issue other/repo#3"},
		recv: []string{
			"PRIVMSG #chan :Issue #1: Title of 1 <Created by joe> <https://github.com/org/repo/issue/1>",
			"PRIVMSG #chan :Issue other#2: Title of 2 <Created by joe> <https://github.com/org/other/issue/2>",
			"PRIVMSG #chan :Issue other/repo#3: Title of 3 <Created by joe> <https://github.com/other/repo/issue/3>",
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

func (s *S) TestGitHub(c *C) {
	for i, test := range lpTests {
		c.Logf("Testing message #%d: %s", i, test.send)
		server := ghServer{
			bugsText: test.bugsText,
			status:   test.status,
		}
		server.Start()
		if test.config == nil {
			test.config = mup.Map{}
		}
		test.config["endpoint"] = server.URL()
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

type ghServer struct {
	server *httptest.Server

	status int

	bugForm url.Values

	bugsForm url.Values
	bugsText [][]int
	bugsResp int

	mergesResp int

	headers map[string]http.Header
}

func (s *ghServer) Start() {
	s.server = httptest.NewServer(s)
	s.headers = make(map[string]http.Header)
}

func (s *ghServer) Stop() {
	s.server.Close()
}

func (s *ghServer) URL() string {
	return s.server.URL
}

func (s *ghServer) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	s.headers[req.URL.Path] = req.Header
	if s.status != 0 {
		w.WriteHeader(s.status)
		return
	}
	req.ParseForm()
	switch {
	case strings.HasPrefix(req.URL.Path, "/repos/") && strings.Contains(req.URL.Path, "/issues/"):
		s.serveIssue(w, req)
	default:
		panic("got unexpected request for " + req.URL.Path + " in test ghServer")
	}
}

func (s *ghServer) serveIssue(w http.ResponseWriter, req *http.Request) {
	path := strings.Split(strings.TrimPrefix(req.URL.Path, "/repos/"), "/")
	if len(path) != 4 || path[2] != "issues" {
		panic("invalid bug URL: " + req.URL.Path)
	}

	//org := path[1]
	//repo := path[2]

	id, err := strconv.Atoi(path[3])
	if err != nil {
		panic("invalid bug URL: " + req.URL.Path)
	}
	if id == 404 {
		w.WriteHeader(404)
		return
	}

	s.bugForm = req.Form

	var res string
	if id == 123 {
		res = fmt.Sprintf(`{
			"title": "Title of %d",
			"number": %d,
			"labels": [{"name": "label1"}, {"name": "label2"}],
			"user": {"login": "joe"}
		}`, id, id)
	} else {
		res = fmt.Sprintf(`{
			"title": "Title of %d",
			"number": %d,
			"user": {"login": "joe"}
		}`, id, id)
	}
	w.Write([]byte(res))
}
