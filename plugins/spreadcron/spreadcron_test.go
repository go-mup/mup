package spreadcron_test

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"gopkg.in/check.v1"
	"gopkg.in/mgo.v2/bson"
	"gopkg.in/mup.v0"
	"gopkg.in/mup.v0/plugins/spreadcron"
)

func Test(t *testing.T) { check.TestingT(t) }

var _ = check.Suite(&spreadcronSuite{})

type ghServer struct {
	server         *httptest.Server
	responses      []serverResponse
	responsesIndex int
}

func (s *ghServer) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	username, token, ok := req.BasicAuth()
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if username != "myusername" || token != "mytoken" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if req.Method == "PUT" {
		// check for required fields
		data, err := ioutil.ReadAll(req.Body)
		if err != nil {
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		// first check for the required fields in lower case from the data before unmarshaling
		// (this is what GH wants)
		sdata := string(data)
		for _, item := range []string{`"content":`, `"message":`, `"name":`, `"email":`} {
			if !strings.Contains(sdata, item) {
				w.WriteHeader(http.StatusUnprocessableEntity)
				return
			}
		}

		payload := &spreadcron.Payload{}
		err = json.Unmarshal(data, payload)
		if err != nil {
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		if payload.Content == "" || payload.Message == "" ||
			payload.Committer.Email == "" || payload.Committer.Name == "" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
	}

	totalResponses := len(s.responses)
	if s.responsesIndex < totalResponses {
		if s.responses[s.responsesIndex].status == 0 {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(s.responses[s.responsesIndex].status)
		}
		fmt.Fprintf(w, s.responses[s.responsesIndex].body)
		s.responsesIndex++
	} else {
		fmt.Fprintf(w, s.responses[totalResponses-1].body)
	}
}

func (s *ghServer) URL() string {
	return s.server.URL
}

func (s *ghServer) Close() {
	s.server.Close()
}

func (s *ghServer) reset() {
	s.responses = []serverResponse{}
	s.responsesIndex = 0
}

func (s *ghServer) setResponses(r []serverResponse) {
	s.responses = r
}

func newGhServer() *ghServer {
	ts := &ghServer{}
	ts.server = httptest.NewServer(ts)
	return ts
}

type spreadcronSuite struct {
	server                  *ghServer
	usernameBack, tokenBack string
}

func (s *spreadcronSuite) SetUpSuite(c *check.C) {
	s.server = newGhServer()
	s.usernameBack = os.Getenv("MUP_GH_USERNAME")
	s.tokenBack = os.Getenv("")
}

func (s *spreadcronSuite) TearDownSuite(c *check.C) {
	s.server.Close()
}

func (s *spreadcronSuite) SetUpTest(c *check.C) {
	mup.SetLogger(c)
	mup.SetDebug(true)
}

func (s *spreadcronSuite) TearDownTest(c *check.C) {
	mup.SetLogger(nil)
	mup.SetDebug(false)
}

type serverResponse struct {
	body   string
	status int
}

type spreadcronTest struct {
	desc            string
	send            string
	serverResponses []serverResponse
	recv            string
}

var spreadcronTests = []spreadcronTest{
	{
		desc:            "generic help shows all commands",
		send:            "[#chan] mup: spreadcron help",
		serverResponses: []serverResponse{},
		recv:            "PRIVMSG #chan :nick: help: this help message_list: show available jobs_trigger: execute the given job",
	}, {
		desc: "list shows branches",
		send: "[#chan] mup: spreadcron list",
		serverResponses: []serverResponse{serverResponse{body: `[
  {
    "name": "built-image-amd64-smoketest",
    "commit": {
      "sha": "0a37352f456fbbb30eccbdee3d55261cc9b65cc1",
      "url": "https://api.github.com/repos/snapcore/spread-cron/commits/0a37352f456fbbb30eccbdee3d55261cc9b65cc1"
    }
  },
  {
    "name": "core-amd64-refresh-to-beta",
    "commit": {
      "sha": "d5cbf4e3f07d84e2b49948ab5ac404d5fc02f735",
      "url": "https://api.github.com/repos/snapcore/spread-cron/commits/d5cbf4e3f07d84e2b49948ab5ac404d5fc02f735"
    }
  }
]`}},
		recv: "PRIVMSG #chan :nick: built-image-amd64-smoketest_core-amd64-refresh-to-beta",
	}, {
		desc: "empty trigger shows branches",
		send: "[#chan] mup: spreadcron trigger",
		serverResponses: []serverResponse{serverResponse{body: `[
  {
    "name": "built-image-amd64-smoketest",
    "commit": {
      "sha": "0a37352f456fbbb30eccbdee3d55261cc9b65cc1",
      "url": "https://api.github.com/repos/snapcore/spread-cron/commits/0a37352f456fbbb30eccbdee3d55261cc9b65cc1"
    }
  },
  {
    "name": "core-amd64-refresh-to-beta",
    "commit": {
      "sha": "d5cbf4e3f07d84e2b49948ab5ac404d5fc02f735",
      "url": "https://api.github.com/repos/snapcore/spread-cron/commits/d5cbf4e3f07d84e2b49948ab5ac404d5fc02f735"
    }
  }
]`}},
		recv: "PRIVMSG #chan :nick: please select a job to trigger:_built-image-amd64-smoketest_core-amd64-refresh-to-beta",
	}, {
		desc: "trigger creates commit, first time triggered",
		send: "[#chan] mup: spreadcron trigger myjob",
		serverResponses: []serverResponse{
			serverResponse{status: http.StatusNotFound},
			serverResponse{status: http.StatusCreated, body: `
{
    "content": {
        "sha": "mysha",
    },
}
`},
		},
		recv: "PRIVMSG #chan :nick: myjob was successfully triggered",
	}, {
		desc: "trigger creates commit, trigger file present",
		send: "[#chan] mup: spreadcron trigger myjob",
		serverResponses: []serverResponse{
			serverResponse{body: `
{
    "sha": "mysha"
}`},
			serverResponse{body: `
{
    "content": {
        "sha": "newsha",
    },
}
`},
		},
		recv: "PRIVMSG #chan :nick: myjob was successfully triggered",
	}, {
		desc:            "non-allowed requestors can't trigger job",
		send:            "[,raw] :other!~user@host#chan PRIVMSG mup : spreadcron trigger myjob",
		serverResponses: []serverResponse{},
		recv:            "PRIVMSG other :I'm afraid I can't do that, Dave. Daiisyy, daisyyyyy",
	}, {
		desc:            "wrong commands are responded",
		send:            "[#chan] mup: spreadcron blabla",
		serverResponses: []serverResponse{},
		recv:            "PRIVMSG #chan :nick: I'm afraid I can't do that, Dave. Daiisyy, daisyyyyy",
	}, {
		desc: "error conditions are shown: list 500 from server",
		send: "[#chan] mup: spreadcron list",
		serverResponses: []serverResponse{
			serverResponse{status: http.StatusServiceUnavailable},
		},
		recv: "PRIVMSG #chan :nick: an error occurred, please try again later",
	}, {
		desc: "error conditions are shown: empty trigger 500 from server",
		send: "[#chan] mup: spreadcron trigger",
		serverResponses: []serverResponse{
			serverResponse{status: http.StatusServiceUnavailable},
		},
		recv: "PRIVMSG #chan :nick: an error occurred, please try again later",
	}, {
		desc: "error conditions are shown: trigger 500 from server",
		send: "[#chan] mup: spreadcron trigger myjob",
		serverResponses: []serverResponse{
			serverResponse{body: `
{
    "sha": "mysha"
}`},
			serverResponse{status: http.StatusServiceUnavailable},
		},
		recv: "PRIVMSG #chan :nick: an error occurred, please try again later",
	},
}

func (s *spreadcronSuite) TestSpreadcron(c *check.C) {
	for i, test := range spreadcronTests {
		c.Logf("Test #%d: %s", i, test.desc)
		c.Logf("Testing message #%d: %s", i, test.send)

		tester := mup.NewPluginTester("spreadcron")

		config := bson.M{
			"endpoint": s.server.URL(),
			"username": "myusername",
			"token":    "mytoken",
			"allowed":  []string{"nick"},
			"project":  "snapcore/spread-cron",
		}
		tester.SetConfig(config)

		s.server.reset()
		s.server.responses = test.serverResponses

		tester.Start()
		tester.Sendf(test.send)
		tester.Stop()

		c.Assert(tester.Recv(), check.Equals, test.recv)
	}
}
