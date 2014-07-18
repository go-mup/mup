package mup_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	. "gopkg.in/check.v1"

	"gopkg.in/mup.v0"
	_ "gopkg.in/mup.v0/plugins/wolframalpha"

	"gopkg.in/mgo.v2/bson"
	"net/url"
)

func Test(t *testing.T) { TestingT(t) }

var _ = Suite(&S{})

type S struct{}

type inferTest struct {
	target  string
	send    string
	recv    string
	result  string
	config  bson.M
	targets []bson.M
	form    url.Values
}

var inferTests = []inferTest{{
	send:   "infer the query",
	recv:   "PRIVMSG nick :the result",
	result: successResult,
	config: bson.M{
		"appid": "theid",
	},
	form: url.Values{
		"input":  {"the query"},
		"format": {"plaintext"},
		"appid":  {"theid"},
	},
}, {
	send:   "infer the query",
	recv:   "PRIVMSG nick :WolframAlpha reported an error: the error",
	result: errorResult,
}, {
	send:   "infer the query",
	recv:   "PRIVMSG nick :Cannot infer much out of this.",
	result: failResult,
}, {
	send:   "infer the query",
	recv:   "PRIVMSG nick :Cannot parse WolframAlpha response.",
	result: "bogus",
}, {
	send:   "infer the query",
	recv:   "PRIVMSG nick :Cannot parse WolframAlpha response.",
	result: "<queryresult success='true'><queryresult>",
}}

func (s *S) SetUpTest(c *C) {
	mup.SetLogger(c)
	mup.SetDebug(true)
}

func (s *S) TearDownTest(c *C) {
	mup.SetLogger(nil)
	mup.SetDebug(false)
}

func (s *S) TestInfer(c *C) {
	for i, test := range inferTests {
		c.Logf("Running test %d with message: %v", i, test.send)

		server := &alphaServer{
			result: test.result,
		}
		server.Start()
		if test.config == nil {
			test.config = bson.M{}
		}
		test.config["endpoint"] = server.URL()

		tester := mup.NewPluginTester("wolframalpha")
		tester.SetConfig(test.config)
		tester.SetTargets(test.targets)
		tester.Start()
		tester.Sendf(test.target, "%s", test.send)

		c.Check(tester.Stop(), IsNil)
		c.Check(tester.Recv(), Equals, test.recv)

		server.Stop()

		if test.form != nil {
			c.Check(server.form, DeepEquals, test.form)
		}

		if c.Failed() {
			c.FailNow()
		}
	}
}

type alphaServer struct {
	result string
	form   url.Values

	server *httptest.Server
}

func (s *alphaServer) Start() {
	s.server = httptest.NewServer(s)
}

func (s *alphaServer) Stop() {
	s.server.Close()
}

func (s *alphaServer) URL() string {
	return s.server.URL
}

func (s *alphaServer) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	req.ParseForm()
	if req.URL.Path != "/" {
		panic("Got unexpected request for " + req.URL.Path + " in test alphaServer")
	}
	s.form = req.Form
	w.Write([]byte(s.result))
}

var successResult = `
<?xml version='1.0' encoding='UTF-8'?>
<queryresult success='true'>
  <pod><subpod><plaintext>Not this one.</plaintext></subpod></pod>
  <pod primary="true"><subpod>
  <plaintext>
  the
  result 
  </plaintext>
  </subpod></pod>
  <pod><subpod><plaintext>Not this one.</plaintext></subpod></pod>
</queryresult>
`

var errorResult = `
<?xml version='1.0' encoding='UTF-8'?>
<queryresult error='true'>
 <error>
  <code>1</code>
  <msg>
  the
  error
  </msg>
 </error>
</queryresult>
`

var failResult = `
<?xml version='1.0' encoding='UTF-8'?>
<queryresult success='false'></queryresult>
`
