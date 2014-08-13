package mup_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	. "gopkg.in/check.v1"
	"gopkg.in/mgo.v2/bson"
	"gopkg.in/mup.v0"
	"gopkg.in/mup.v0/ldap"
	_ "gopkg.in/mup.v0/plugins/playground"
	"strings"
)

func Test(t *testing.T) { TestingT(t) }

var _ = Suite(&S{})

type S struct{}

type runTest struct {
	config      bson.M
	send        string
	recv        string
	output      string
	compile     string
	format      string
	compileForm url.Values
	formatForm  url.Values
	status      int
}

var runTests = []runTest{{
	// Basic processing.
	send:    "run one; two",
	recv:    "PRIVMSG nick :result",
	format:  `{"Body": "formatted"}`,
	compile: `{"Events": [{"Message": "result"}]}`,
	formatForm: url.Values{
		"imports": {"1"},
		"body":    {"package main\nfunc main() { one; two }"},
	},
	compileForm: url.Values{
		"body":    {"formatted"},
		"version": {"2"},
	},
}, {
	// Print option handling.
	send: "run -p expr",
	recv: "PRIVMSG nick :result",
	formatForm: url.Values{
		"imports": {"1"},
		"body":    {"package main\nfunc main() { fmt.Print(expr) }"},
	},
}, {
	// Join all events into a single output.
	send:    "run code",
	recv:    "PRIVMSG nick :a — b",
	compile: `{"Events": [{"Message": "a\n"}, {"Message": "\nb\n"}]}`,
}, {
	// Handling of empty output.
	send:    "run code",
	recv:    "PRIVMSG nick :[no output]",
	compile: `{"Events": [{"Message": ""}]}`,
}, {
	send:    "run code",
	recv:    "PRIVMSG nick :[no output]",
	compile: `{"Events": [{"Message": "[no output]\n"}]}`,
}, {
	send:    "run -q code",
	recv:    "PRIVMSG nick :[no output]",
	compile: `{"Events": [{"Message": "[no output]\n"}]}`,
}, {
	// Multi-line displaying and space trimming.
	send:   "run code",
	recv:   "PRIVMSG nick :a — b — c",
	output: "  a  \nb\n\nc\n ",
}, {
	// Quoting.
	send:   "run -q code",
	recv:   `PRIVMSG nick :"  a  \nb\n\nc\n "`,
	output: "  a  \nb\n\nc\n ",
}, {
	// Exit message status handling
	send:   "run code",
	recv:   "PRIVMSG nick :a — b — [non-zero exit status]",
	output: "a\nb [process exited with non-zero status]\n",
}, {
	send:   "run -q code",
	recv:   `PRIVMSG nick :"a\nb" — [non-zero exit status]`,
	output: "a\nb [process exited with non-zero status]\n",
}, {
	// Truncate long output.
	send:   "run code",
	recv:   "PRIVMSG nick :a — " + lorem + " — b — Lorem ipsum dolor [...]",
	output: "\na\n" + lorem + "\nb\n" + lorem + "\nc\n",
}, {
	send:   "run -q code",
	recv:   `PRIVMSG nick :"\na\n` + lorem + `\nb\nLorem ipsum dolor sit " + [...]`,
	output: "\na\n" + lorem + "\nb\n" + lorem + "\nc\n",
}, {
	send:   "run code",
	recv:   "PRIVMSG nick :" + strings.Repeat("X", 263) + "[...]",
	output: strings.Repeat("X", 300),
}, {
	send:   "run -q code",
	recv:   `PRIVMSG nick :"` + strings.Repeat("X", 262) + `" + [...]`,
	output: strings.Repeat("X", 300),
}, {
	// Must have enough room for the exit status message at the end.
	send:   "run -q code",
	recv:   `PRIVMSG nick :"\na\n` + lorem + `\nbcd\nLorem ipsum dolor sit " + [...] — [non-zero exit status]`,
	output: "\na\n" + lorem + "\nbcd\n" + lorem + "\ne\n [process exited with non-zero status]\n",
}, {
	send:   "run -q code",
	recv:   `PRIVMSG nick :"` + strings.Repeat("X", 262) + `" + [...] — [non-zero exit status]`,
	output: strings.Repeat("X", 300) + " [process exited with non-zero status]\n",
}}

func (s *S) SetUpTest(c *C) {
	mup.SetLogger(c)
	mup.SetDebug(true)
}

func (s *S) TearDownTest(c *C) {
	mup.SetLogger(nil)
	mup.SetDebug(false)
}

func (s *S) TestRun(c *C) {
	for i, test := range runTests {
		c.Logf("Running test %d with message: %v", i, test.send)

		server := &playServer{
			compile: test.compile,
			format:  test.format,
			status:  test.status,
		}
		if server.format == "" {
			server.format = "{}"
		}
		if server.compile == "" {
			output := test.output
			if output == "" {
				output = "result"
			}
			m := bson.M{"Events": []bson.M{{"Message": output}}}
			data, err := json.Marshal(m)
			if err != nil {
				c.Fatal(err)
			}
			server.compile = string(data)
		}
		server.Start()
		if test.config == nil {
			test.config = bson.M{}
		}
		test.config["endpoint"] = server.URL()

		tester := mup.NewPluginTester("playground")
		tester.SetConfig(test.config)
		tester.Start()
		tester.Sendf("%s", test.send)
		c.Check(tester.Stop(), IsNil)
		c.Check(tester.Recv(), Equals, test.recv)
		c.Check(tester.Recv(), Equals, "")

		server.Stop()

		if test.compileForm != nil {
			c.Check(server.compileForm, DeepEquals, test.compileForm)
		}
		if test.formatForm != nil {
			c.Check(server.formatForm, DeepEquals, test.formatForm)
		}

		if c.Failed() {
			c.FailNow()
		}
	}
}

type ldapConn struct {
	nick   string
	result ldap.Result
}

func ldapConnFor(nick string, attrs ...string) ldap.Conn {
	res := ldap.Result{Attrs: []ldap.Attr{
		{Name: "mozillaNickname", Values: []string{nick}},
	}}
	for i := 0; i < len(attrs); i += 2 {
		res.Attrs = append(res.Attrs, ldap.Attr{Name: attrs[i], Values: []string{attrs[i+1]}})
	}
	return ldapConn{nick, res}
}

func (l ldapConn) Search(s *ldap.Search) ([]ldap.Result, error) {
	if s.Filter == "(mozillaNickname="+l.nick+")" {
		return []ldap.Result{l.result}, nil
	}
	return nil, nil
}

func (l ldapConn) Close() error { return nil }

type playServer struct {
	format      string
	compile     string
	formatForm  url.Values
	compileForm url.Values
	status      int

	server *httptest.Server
}

func (s *playServer) Start() {
	s.server = httptest.NewServer(s)
}

func (s *playServer) Stop() {
	s.server.Close()
}

func (s *playServer) URL() string {
	return s.server.URL
}

func (s *playServer) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	req.ParseForm()
	result := ""
	switch req.URL.Path {
	case "/compile":
		s.compileForm = req.Form
		result = s.compile
	case "/fmt":
		s.formatForm = req.Form
		result = s.format
	default:
		panic("Got unexpected request for " + req.URL.Path + " in test playServer")
	}
	if s.status != 0 {
		w.WriteHeader(s.status)
	}
	w.Write([]byte(result))
}

var lorem = strings.Replace(`
Lorem ipsum dolor sit amet, consectetur adipisicing elit, sed do
eiusmod tempor incididunt ut labore et dolore magna aliqua. Ut enim ad
minim veniam, quis nostrud exercitation ullamco laboris nisi ut aliquip
ex ea commodo consequat
`, "\n", "", -1)
