package travis_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	. "gopkg.in/check.v1"
	"gopkg.in/mgo.v2/bson"
	"gopkg.in/mup.v0"
)

func Test(t *testing.T) { TestingT(t) }

var _ = Suite(&travisSuite{})

type travisSuite struct {
	travisServer *travisServer
	tester       *mup.PluginTester
}

type travisServer struct {
	server         *httptest.Server
	responses      []string
	responsesIndex int
}

func (s *travisServer) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	totalResponses := len(s.responses)
	if s.responsesIndex < totalResponses {
		// http client errors are modeled as empty responses
		if s.responses[s.responsesIndex] == "" {
			// this redirect will cause httpClient.Do(req) to return a nil response and a non nil error
			http.Redirect(w, req, "http://unexisting.server", http.StatusMovedPermanently)
		} else {
			fmt.Fprintf(w, s.responses[s.responsesIndex])
		}
		s.responsesIndex++
	} else {
		fmt.Fprintf(w, s.responses[totalResponses-1])
	}
}

func (s *travisServer) URL() string {
	return s.server.URL
}

func (s *travisServer) Close() {
	s.server.Close()
}

func (s *travisServer) setup() {
	s.responses = []string{}
	s.responsesIndex = 0
}

func (s *travisServer) setResponses(r []string) {
	s.responses = r
}

func newTravisServer() *travisServer {
	ts := &travisServer{}
	ts.server = httptest.NewServer(ts)
	return ts
}

func (s *travisSuite) SetUpSuite(c *C) {
	s.travisServer = newTravisServer()
}

func (s *travisSuite) TearDownSuite(c *C) {
	s.travisServer.Close()
}

func (s *travisSuite) SetUpTest(c *C) {
	mup.SetLogger(c)
	mup.SetDebug(true)
}

func (s *travisSuite) TearDownTest(c *C) {
	mup.SetLogger(nil)
	mup.SetDebug(false)
}

func (s *travisSuite) setUpTester() {
	s.tester = mup.NewPluginTester("travisbuildwatch")
	config := bson.M{
		"endpoint":  s.travisServer.URL(),
		"project":   "testproject",
		"polldelay": "50ms",
		"allowed":   []string{"master", "allowed"}}
	s.tester.SetConfig(config)

	targets := []bson.M{{"account": "test", "channel": "test"}}
	s.tester.SetTargets(targets)
}

type travisTest struct {
	summary         string
	serverResponses []string
	recv            []string
}

var travisTests = []travisTest{
	{
		summary: "New finished build pass",
		serverResponses: []string{
			`[{"id":1,"number":"1","state":"finished","result":0,"branch":"master","message":"first changes"}]`,

			`[{"id":2,"number":"2","state":"finished","result":0,"branch":"master","message":"second changes"},
        {"id":1,"number":"1","state":"finished","result":0,"branch":"master","message":"first changes"}]`},
		recv: []string{"PRIVMSG test :Travis build passed: second changes <project: testproject> <branch: master> <https://travis-ci.org/testproject/builds/2>"},
	}, {
		summary: "New finished build fail",
		serverResponses: []string{
			`[{"id":1,"number":"1","state":"finished","result":0,"branch":"master","message":"first changes"}]`,

			`[{"id":2,"number":"2","state":"finished","result":1,"branch":"master","message":"second changes"},
        {"id":1,"number":"1","state":"finished","result":0,"branch":"master","message":"first changes"}]`},
		recv: []string{"PRIVMSG test :Travis build FAILED: second changes <project: testproject> <branch: master> <https://travis-ci.org/testproject/builds/2>"},
	}, {
		summary: "Long build",
		serverResponses: []string{
			`[{"id":1,"number":"1","state":"finished","result":0,"branch":"master","message":"first changes"}]`,

			`[{"id":2,"number":"2","state":"started","result":null,"branch":"master","message":"second changes"},
        {"id":1,"number":"1","state":"finished","result":0,"branch":"master","message":"first changes"}]`,

			`[{"id":2,"number":"2","state":"finished","result":0,"branch":"master","message":"second changes"},
        {"id":1,"number":"1","state":"finished","result":0,"branch":"master","message":"first changes"}]`},
		recv: []string{
			"PRIVMSG test :Travis build passed: second changes <project: testproject> <branch: master> <https://travis-ci.org/testproject/builds/2>"},
	}, {
		summary: "Long build plus quick build",
		serverResponses: []string{
			`[{"id":1,"number":"1","state":"finished","result":0,"branch":"master","message":"first changes"}]`,

			`[{"id":2,"number":"2","state":"started","result":null,"branch":"master","message":"second changes"},
        {"id":1,"number":"1","state":"finished","result":0,"branch":"master","message":"first changes"}]`,

			`[{"id":3,"number":"3","state":"finished","result":1,"branch":"master","message":"third changes"},
        {"id":2,"number":"2","state":"finished","result":0,"branch":"master","message":"second changes"},
        {"id":1,"number":"1","state":"finished","result":0,"branch":"master","message":"first changes"}]`},
		recv: []string{
			"PRIVMSG test :Travis build passed: second changes <project: testproject> <branch: master> <https://travis-ci.org/testproject/builds/2>",
			"PRIVMSG test :Travis build FAILED: third changes <project: testproject> <branch: master> <https://travis-ci.org/testproject/builds/3>"},
	}, {
		summary: "Long builds finishing in different order",
		serverResponses: []string{
			`[{"id":1,"number":"1","state":"started","result":null,"branch":"master","message":"first changes"}]`,

			`[{"id":2,"number":"2","state":"started","result":null,"branch":"master","message":"second changes"},
        {"id":1,"number":"1","state":"started","result":null,"branch":"master","message":"first changes"}]`,

			`[{"id":3,"number":"3","state":"started","result":null,"branch":"master","message":"third changes"},
        {"id":2,"number":"2","state":"started","result":null,"branch":"master","message":"second changes"},
        {"id":1,"number":"1","state":"started","result":null,"branch":"master","message":"first changes"}]`,

			`[{"id":3,"number":"3","state":"finished","result":1,"branch":"master","message":"third changes"},
        {"id":2,"number":"2","state":"started","result":null,"branch":"master","message":"second changes"},
        {"id":1,"number":"1","state":"started","result":null,"branch":"master","message":"first changes"}]`,

			`[{"id":3,"number":"3","state":"finished","result":1,"branch":"master","message":"third changes"},
        {"id":2,"number":"2","state":"finished","result":0,"branch":"master","message":"second changes"},
        {"id":1,"number":"1","state":"started","result":null,"branch":"master","message":"first changes"}]`,

			`[{"id":3,"number":"3","state":"finished","result":1,"branch":"master","message":"third changes"},
        {"id":2,"number":"2","state":"finished","result":0,"branch":"master","message":"second changes"},
        {"id":1,"number":"1","state":"finished","result":0,"branch":"master","message":"first changes"}]`},
		recv: []string{
			"PRIVMSG test :Travis build FAILED: third changes <project: testproject> <branch: master> <https://travis-ci.org/testproject/builds/3>",
			"PRIVMSG test :Travis build passed: second changes <project: testproject> <branch: master> <https://travis-ci.org/testproject/builds/2>",
			"PRIVMSG test :Travis build passed: first changes <project: testproject> <branch: master> <https://travis-ci.org/testproject/builds/1>"},
	}, {
		summary: "New build on paginated results",
		serverResponses: []string{
			`[{"id":28,"number":"28","state":"finished","result":1,"branch":"master","message":"28"},
        {"id":27,"number":"27","state":"finished","result":1,"branch":"master","message":"27"},
        {"id":26,"number":"26","state":"finished","result":1,"branch":"master","message":"26"},
        {"id":25,"number":"25","state":"finished","result":1,"branch":"master","message":"25"},
        {"id":24,"number":"24","state":"finished","result":1,"branch":"master","message":"24"},
        {"id":23,"number":"23","state":"finished","result":1,"branch":"master","message":"23"},
        {"id":22,"number":"22","state":"finished","result":1,"branch":"master","message":"22"},
        {"id":21,"number":"21","state":"finished","result":1,"branch":"master","message":"21"},
        {"id":20,"number":"20","state":"finished","result":1,"branch":"master","message":"20"},
        {"id":19,"number":"19","state":"finished","result":1,"branch":"master","message":"19"},
        {"id":18,"number":"18","state":"finished","result":1,"branch":"master","message":"18"},
        {"id":17,"number":"17","state":"finished","result":1,"branch":"master","message":"17"},
        {"id":16,"number":"16","state":"finished","result":1,"branch":"master","message":"16"},
        {"id":15,"number":"15","state":"finished","result":1,"branch":"master","message":"15"},
        {"id":14,"number":"14","state":"finished","result":1,"branch":"master","message":"14"},
        {"id":13,"number":"13","state":"finished","result":1,"branch":"master","message":"13"},
        {"id":12,"number":"12","state":"finished","result":1,"branch":"master","message":"12"},
        {"id":11,"number":"11","state":"finished","result":1,"branch":"master","message":"11"},
        {"id":10,"number":"10","state":"finished","result":1,"branch":"master","message":"10"},
        {"id":9,"number":"9","state":"finished","result":1,"branch":"master","message":"9"},
        {"id":8,"number":"8","state":"finished","result":1,"branch":"master","message":"8"},
        {"id":7,"number":"7","state":"finished","result":1,"branch":"master","message":"7"},
        {"id":6,"number":"6","state":"finished","result":1,"branch":"master","message":"6"},
        {"id":5,"number":"5","state":"finished","result":1,"branch":"master","message":"5"},
        {"id":4,"number":"4","state":"finished","result":1,"branch":"master","message":"4"}]`,
			`[{"id":3,"number":"3","state":"finished","result":1,"branch":"master","message":"3"},
        {"id":2,"number":"2","state":"finished","result":0,"branch":"master","message":"2"},
        {"id":1,"number":"1","state":"finished","result":0,"branch":"master","message":"1"}]`,

			`[{"id":29,"number":"29","state":"started","result":null,"branch":"master","message":"29"},
        {"id":28,"number":"28","state":"finished","result":1,"branch":"master","message":"28"},
        {"id":27,"number":"27","state":"finished","result":1,"branch":"master","message":"27"},
        {"id":26,"number":"26","state":"finished","result":1,"branch":"master","message":"26"},
        {"id":25,"number":"25","state":"finished","result":1,"branch":"master","message":"25"},
        {"id":24,"number":"24","state":"finished","result":1,"branch":"master","message":"24"},
        {"id":23,"number":"23","state":"finished","result":1,"branch":"master","message":"23"},
        {"id":22,"number":"22","state":"finished","result":1,"branch":"master","message":"22"},
        {"id":21,"number":"21","state":"finished","result":1,"branch":"master","message":"21"},
        {"id":20,"number":"20","state":"finished","result":1,"branch":"master","message":"20"},
        {"id":19,"number":"19","state":"finished","result":1,"branch":"master","message":"19"},
        {"id":18,"number":"18","state":"finished","result":1,"branch":"master","message":"18"},
        {"id":17,"number":"17","state":"finished","result":1,"branch":"master","message":"17"},
        {"id":16,"number":"16","state":"finished","result":1,"branch":"master","message":"16"},
        {"id":15,"number":"15","state":"finished","result":1,"branch":"master","message":"15"},
        {"id":14,"number":"14","state":"finished","result":1,"branch":"master","message":"14"},
        {"id":13,"number":"13","state":"finished","result":1,"branch":"master","message":"13"},
        {"id":12,"number":"12","state":"finished","result":1,"branch":"master","message":"12"},
        {"id":11,"number":"11","state":"finished","result":1,"branch":"master","message":"11"},
        {"id":10,"number":"10","state":"finished","result":1,"branch":"master","message":"10"},
        {"id":9,"number":"9","state":"finished","result":1,"branch":"master","message":"9"},
        {"id":8,"number":"8","state":"finished","result":1,"branch":"master","message":"8"},
        {"id":7,"number":"7","state":"finished","result":1,"branch":"master","message":"7"},
        {"id":6,"number":"6","state":"finished","result":1,"branch":"master","message":"6"},
        {"id":5,"number":"5","state":"finished","result":1,"branch":"master","message":"5"}]`,
			`[{"id":4,"number":"4","state":"finished","result":1,"branch":"master","message":"4"},
        {"id":3,"number":"3","state":"finished","result":1,"branch":"master","message":"3"},
        {"id":2,"number":"2","state":"finished","result":0,"branch":"master","message":"2"},
        {"id":1,"number":"1","state":"finished","result":0,"branch":"master","message":"1"}]`,

			`[{"id":29,"number":"29","state":"finished","result":0,"branch":"master","message":"29"},
        {"id":28,"number":"28","state":"finished","result":1,"branch":"master","message":"28"},
        {"id":27,"number":"27","state":"finished","result":1,"branch":"master","message":"27"},
        {"id":26,"number":"26","state":"finished","result":1,"branch":"master","message":"26"},
        {"id":25,"number":"25","state":"finished","result":1,"branch":"master","message":"25"},
        {"id":24,"number":"24","state":"finished","result":1,"branch":"master","message":"24"},
        {"id":23,"number":"23","state":"finished","result":1,"branch":"master","message":"23"},
        {"id":22,"number":"22","state":"finished","result":1,"branch":"master","message":"22"},
        {"id":21,"number":"21","state":"finished","result":1,"branch":"master","message":"21"},
        {"id":20,"number":"20","state":"finished","result":1,"branch":"master","message":"20"},
        {"id":19,"number":"19","state":"finished","result":1,"branch":"master","message":"19"},
        {"id":18,"number":"18","state":"finished","result":1,"branch":"master","message":"18"},
        {"id":17,"number":"17","state":"finished","result":1,"branch":"master","message":"17"},
        {"id":16,"number":"16","state":"finished","result":1,"branch":"master","message":"16"},
        {"id":15,"number":"15","state":"finished","result":1,"branch":"master","message":"15"},
        {"id":14,"number":"14","state":"finished","result":1,"branch":"master","message":"14"},
        {"id":13,"number":"13","state":"finished","result":1,"branch":"master","message":"13"},
        {"id":12,"number":"12","state":"finished","result":1,"branch":"master","message":"12"},
        {"id":11,"number":"11","state":"finished","result":1,"branch":"master","message":"11"},
        {"id":10,"number":"10","state":"finished","result":1,"branch":"master","message":"10"},
        {"id":9,"number":"9","state":"finished","result":1,"branch":"master","message":"9"},
        {"id":8,"number":"8","state":"finished","result":1,"branch":"master","message":"8"},
        {"id":7,"number":"7","state":"finished","result":1,"branch":"master","message":"7"},
        {"id":6,"number":"6","state":"finished","result":1,"branch":"master","message":"6"},
        {"id":5,"number":"5","state":"finished","result":1,"branch":"master","message":"5"}]`,
			`[{"id":4,"number":"4","state":"finished","result":1,"branch":"master","message":"4"},
        {"id":3,"number":"3","state":"finished","result":1,"branch":"master","message":"3"},
        {"id":2,"number":"2","state":"finished","result":0,"branch":"master","message":"2"},
        {"id":1,"number":"1","state":"finished","result":0,"branch":"master","message":"1"}]`},
		recv: []string{
			"PRIVMSG test :Travis build passed: 29 <project: testproject> <branch: master> <https://travis-ci.org/testproject/builds/29>"},
	}, {
		summary: "Invalid number is handled",
		serverResponses: []string{
			`[{"id":1,"number":"1","state":"finished","result":0,"branch":"master","message":"first changes"}]`,

			`[{"id":2,"number":"blabla","state":"finished","result":1,"branch":"master","message":"second changes"},
        {"id":1,"number":"1","state":"finished","result":0,"branch":"master","message":"first changes"}]`},
		recv: []string(nil),
	}, {
		summary: "Two builds finish on the same poll",
		serverResponses: []string{
			`[{"id":2,"number":"2","state":"started","result":null,"branch":"master","message":"2"},
        {"id":1,"number":"1","state":"started","result":null,"branch":"master","message":"1"}]`,

			`[{"id":2,"number":"2","state":"finished","result":1,"branch":"master","message":"2"},
        {"id":1,"number":"1","state":"finished","result":0,"branch":"master","message":"1"}]`},
		recv: []string{
			"PRIVMSG test :Travis build passed: 1 <project: testproject> <branch: master> <https://travis-ci.org/testproject/builds/1>",
			"PRIVMSG test :Travis build FAILED: 2 <project: testproject> <branch: master> <https://travis-ci.org/testproject/builds/2>"},
	}, {
		summary: "Server error is handled",
		serverResponses: []string{
			`[{"id":28,"number":"28","state":"finished","result":1,"branch":"master","message":"28"},
        {"id":27,"number":"27","state":"finished","result":1,"branch":"master","message":"27"},
        {"id":26,"number":"26","state":"finished","result":1,"branch":"master","message":"26"},
        {"id":25,"number":"25","state":"finished","result":1,"branch":"master","message":"25"},
        {"id":24,"number":"24","state":"finished","result":1,"branch":"master","message":"24"},
        {"id":23,"number":"23","state":"finished","result":1,"branch":"master","message":"23"},
        {"id":22,"number":"22","state":"finished","result":1,"branch":"master","message":"22"},
        {"id":21,"number":"21","state":"finished","result":1,"branch":"master","message":"21"},
        {"id":20,"number":"20","state":"finished","result":1,"branch":"master","message":"20"},
        {"id":19,"number":"19","state":"finished","result":1,"branch":"master","message":"19"},
        {"id":18,"number":"18","state":"finished","result":1,"branch":"master","message":"18"},
        {"id":17,"number":"17","state":"finished","result":1,"branch":"master","message":"17"},
        {"id":16,"number":"16","state":"finished","result":1,"branch":"master","message":"16"},
        {"id":15,"number":"15","state":"finished","result":1,"branch":"master","message":"15"},
        {"id":14,"number":"14","state":"finished","result":1,"branch":"master","message":"14"},
        {"id":13,"number":"13","state":"finished","result":1,"branch":"master","message":"13"},
        {"id":12,"number":"12","state":"finished","result":1,"branch":"master","message":"12"},
        {"id":11,"number":"11","state":"finished","result":1,"branch":"master","message":"11"},
        {"id":10,"number":"10","state":"finished","result":1,"branch":"master","message":"10"},
        {"id":9,"number":"9","state":"finished","result":1,"branch":"master","message":"9"},
        {"id":8,"number":"8","state":"finished","result":1,"branch":"master","message":"8"},
        {"id":7,"number":"7","state":"finished","result":1,"branch":"master","message":"7"},
        {"id":6,"number":"6","state":"finished","result":1,"branch":"master","message":"6"},
        {"id":5,"number":"5","state":"finished","result":1,"branch":"master","message":"5"},
        {"id":4,"number":"4","state":"finished","result":1,"branch":"master","message":"4"}]`,
			``,

			`[{"id":29,"number":"29","state":"finished","result":0,"branch":"master","message":"29"},
        {"id":28,"number":"28","state":"finished","result":1,"branch":"master","message":"28"},
        {"id":27,"number":"27","state":"finished","result":1,"branch":"master","message":"27"},
        {"id":26,"number":"26","state":"finished","result":1,"branch":"master","message":"26"},
        {"id":25,"number":"25","state":"finished","result":1,"branch":"master","message":"25"},
        {"id":24,"number":"24","state":"finished","result":1,"branch":"master","message":"24"},
        {"id":23,"number":"23","state":"finished","result":1,"branch":"master","message":"23"},
        {"id":22,"number":"22","state":"finished","result":1,"branch":"master","message":"22"},
        {"id":21,"number":"21","state":"finished","result":1,"branch":"master","message":"21"},
        {"id":20,"number":"20","state":"finished","result":1,"branch":"master","message":"20"},
        {"id":19,"number":"19","state":"finished","result":1,"branch":"master","message":"19"},
        {"id":18,"number":"18","state":"finished","result":1,"branch":"master","message":"18"},
        {"id":17,"number":"17","state":"finished","result":1,"branch":"master","message":"17"},
        {"id":16,"number":"16","state":"finished","result":1,"branch":"master","message":"16"},
        {"id":15,"number":"15","state":"finished","result":1,"branch":"master","message":"15"},
        {"id":14,"number":"14","state":"finished","result":1,"branch":"master","message":"14"},
        {"id":13,"number":"13","state":"finished","result":1,"branch":"master","message":"13"},
        {"id":12,"number":"12","state":"finished","result":1,"branch":"master","message":"12"},
        {"id":11,"number":"11","state":"finished","result":1,"branch":"master","message":"11"},
        {"id":10,"number":"10","state":"finished","result":1,"branch":"master","message":"10"},
        {"id":9,"number":"9","state":"finished","result":1,"branch":"master","message":"9"},
        {"id":8,"number":"8","state":"finished","result":1,"branch":"master","message":"8"},
        {"id":7,"number":"7","state":"finished","result":1,"branch":"master","message":"7"},
        {"id":6,"number":"6","state":"finished","result":1,"branch":"master","message":"6"},
        {"id":5,"number":"5","state":"finished","result":1,"branch":"master","message":"5"}]`,
			`[{"id":4,"number":"4","state":"finished","result":1,"branch":"master","message":"4"},
        {"id":3,"number":"3","state":"finished","result":1,"branch":"master","message":"3"},
        {"id":2,"number":"2","state":"finished","result":0,"branch":"master","message":"2"},
        {"id":1,"number":"1","state":"finished","result":0,"branch":"master","message":"1"}]`},
		recv: []string{
			"PRIVMSG test :Travis build passed: 29 <project: testproject> <branch: master> <https://travis-ci.org/testproject/builds/29>"},
	}, {
		summary: "Malformed response is handled",
		serverResponses: []string{
			`[{d":2,"number":"2","state":"started","result":null,"branch":"master","message":"2"}]`},
		recv: []string(nil),
	}, {
		summary: "Errored builds are reported",
		serverResponses: []string{
			`[{"id":1,"number":"1","state":"finished","result":0,"branch":"master","message":"first changes"}]`,

			`[{"id":4,"number":"4","state":"finished","result":null,"branch":"master","message":"fourth changes"},
        {"id":3,"number":"3","state":"finished","result":0,"branch":"master","message":"third changes"},
        {"id":2,"number":"2","state":"finished","result":null,"branch":"master","message":"second changes"},
        {"id":1,"number":"1","state":"finished","result":0,"branch":"master","message":"first changes"}]`},
		recv: []string{
			"PRIVMSG test :Travis build ERRORED: second changes <project: testproject> <branch: master> <https://travis-ci.org/testproject/builds/2>",
			"PRIVMSG test :Travis build passed: third changes <project: testproject> <branch: master> <https://travis-ci.org/testproject/builds/3>",
			"PRIVMSG test :Travis build ERRORED: fourth changes <project: testproject> <branch: master> <https://travis-ci.org/testproject/builds/4>",
		},
	}, {
		summary: "Do not take build as errored if result is present",
		serverResponses: []string{
			`[{"id":1,"number":"1","state":"finished","result":0,"branch":"master","message":"first changes"}]`,

			`[{"id":4,"number":"4","state":"finished","result":1,"branch":"master","message":"fourth changes"},
        {"id":3,"number":"3","state":"finished","result":0,"branch":"master","message":"third changes"},
        {"id":2,"number":"2","state":"finished","result":0,"branch":"master","message":"second changes"},
        {"id":1,"number":"1","state":"finished","result":0,"branch":"master","message":"first changes"}]`},
		recv: []string{
			"PRIVMSG test :Travis build passed: second changes <project: testproject> <branch: master> <https://travis-ci.org/testproject/builds/2>",
			"PRIVMSG test :Travis build passed: third changes <project: testproject> <branch: master> <https://travis-ci.org/testproject/builds/3>",
			"PRIVMSG test :Travis build FAILED: fourth changes <project: testproject> <branch: master> <https://travis-ci.org/testproject/builds/4>",
		},
	}, {
		summary: "Do not report builds with <skip notify> in the commit message",
		serverResponses: []string{
			`[{"id":1,"number":"1","state":"finished","result":0,"branch":"master","message":"first changes"}]`,

			`[{"id":9,"number":"9","state":"finished","result":0,"branch":"master","message":"<skip notify> ninth changes"},
        {"id":8,"number":"8","state":"finished","result":1,"branch":"master","message":"eighth changes <skip notify>"},
        {"id":7,"number":"7","state":"finished","result":0,"branch":"master","message":"seventh changes"},
        {"id":6,"number":"6","state":"finished","result":1,"branch":"master","message":"sexth <skip notify> changes"},
        {"id":5,"number":"5","state":"finished","result":0,"branch":"master","message":"fifth changes <skip notify>"},
        {"id":4,"number":"4","state":"finished","result":1,"branch":"master","message":"fourth changes"},
        {"id":3,"number":"3","state":"finished","result":0,"branch":"master","message":"third <skip notify> changes"},
        {"id":2,"number":"2","state":"finished","result":1,"branch":"master","message":"<skip notify> second changes"},
        {"id":1,"number":"1","state":"finished","result":0,"branch":"master","message":"first changes"}]`},
		recv: []string{
			"PRIVMSG test :Travis build FAILED: fourth changes <project: testproject> <branch: master> <https://travis-ci.org/testproject/builds/4>",
			"PRIVMSG test :Travis build passed: seventh changes <project: testproject> <branch: master> <https://travis-ci.org/testproject/builds/7>",
		},
	}, {
		summary: "Do not report builds from not included branches",
		serverResponses: []string{
			`[{"id":1,"number":"1","state":"finished","result":0,"branch":"master","message":"first changes"}]`,

			`[{"id":7,"number":"7","state":"finished","result":0,"branch":"allowed","message":"seventh changes"},
        {"id":6,"number":"6","state":"finished","result":1,"branch":"allowed","message":"sixth changes"},
        {"id":5,"number":"5","state":"finished","result":0,"branch":"master","message":"fifth changes"},
        {"id":4,"number":"4","state":"finished","result":1,"branch":"master","message":"fourth changes"},
        {"id":3,"number":"3","state":"finished","result":0,"branch":"not-allowed","message":"third changes"},
        {"id":2,"number":"2","state":"finished","result":1,"branch":"not-allowed","message":"second changes"},
        {"id":1,"number":"1","state":"finished","result":0,"branch":"master","message":"first changes"}]`},
		recv: []string{
			"PRIVMSG test :Travis build FAILED: fourth changes <project: testproject> <branch: master> <https://travis-ci.org/testproject/builds/4>",
			"PRIVMSG test :Travis build passed: fifth changes <project: testproject> <branch: master> <https://travis-ci.org/testproject/builds/5>",
			"PRIVMSG test :Travis build FAILED: sixth changes <project: testproject> <branch: allowed> <https://travis-ci.org/testproject/builds/6>",
			"PRIVMSG test :Travis build passed: seventh changes <project: testproject> <branch: allowed> <https://travis-ci.org/testproject/builds/7>",
		},
	},
}

func (s *travisSuite) TestBuilds(c *C) {
	for i, test := range travisTests {
		s.travisServer.setup()

		summary := test.summary
		c.Logf("Test #%d: %s", i, summary)
		s.testBuild(c, &test)
	}
}

func (s *travisSuite) testBuild(c *C, test *travisTest) {
	s.travisServer.setResponses(test.serverResponses)
	s.setUpTester()

	s.tester.Start()
	time.Sleep(time.Duration(len(test.serverResponses)*50+50) * time.Millisecond)
	c.Assert(s.tester.Stop(), IsNil)

	c.Assert(s.tester.RecvAll(), DeepEquals, test.recv)
}
