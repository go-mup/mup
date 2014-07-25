package mup_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	. "gopkg.in/check.v1"

	"gopkg.in/mup.v0"
	_ "gopkg.in/mup.v0/plugins/wolframalpha"

	"gopkg.in/mgo.v2/bson"
	"gopkg.in/mup.v0/ldap"
	"net/url"
	"strings"
)

func Test(t *testing.T) { TestingT(t) }

var _ = Suite(&S{})

type S struct{}

type inferTest struct {
	target  string
	send    string
	recv    string
	sendAll []string
	recvAll []string
	result  string
	status  int
	config  bson.M
	targets []bson.M
	form    url.Values
	ldap    ldap.Conn
}

var inferTests = []inferTest{{
	// Basic result displaying.
	send:   "infer the query",
	recv:   "PRIVMSG nick :the result.",
	result: "<queryresult success='true'><pod><subpod><plaintext>the result</plaintext></subpod></pod></queryresult>",
	config: bson.M{
		"appid": "theid",
	},
	form: url.Values{
		"ip":            {"host"},
		"input":         {"the query"},
		"format":        {"plaintext"},
		"appid":         {"theid"},
		"parsetimeout":  {"3"},
		"scantimeout":   {"5"},
		"formattimeout": {"3"},
		"podtimeout":    {"2"},
	},
}, {
	// Ignore input and illustration pods.
	send: "infer the query",
	recv: "PRIVMSG nick :result.",
	result: `
		 <queryresult success='true'>
	         <pod id="Input"><subpod><plaintext>input</plaintext></subpod></pod>
	         <pod id="Illustration"><subpod><plaintext>illustration</plaintext></subpod></pod>
	         <pod><subpod><plaintext>result</plaintext></subpod></pod>
		 </queryresult>
	`,
}, {
	// Display only the primaries by default.
	send: "infer the query",
	recv: "PRIVMSG nick :primary one — primary two.",
	result: `
		 <queryresult success='true'>
	         <pod primary='true'><subpod><plaintext>primary one</plaintext></subpod></pod>
	         <pod primary='true'><subpod><plaintext>primary two</plaintext></subpod></pod>
	         <pod><subpod><plaintext>non-primary</plaintext></subpod></pod>
		 </queryresult>
	`,
}, {
	// Display only the first when there's no primary.
	send: "infer the query",
	recv: "PRIVMSG nick :first.",
	result: `
		 <queryresult success='true'>
	         <pod><subpod><plaintext>first</plaintext></subpod></pod>
	         <pod><subpod><plaintext>second</plaintext></subpod></pod>
		 </queryresult>
	`,
}, {
	// Display all when explicitly requested.
	send: "infer -all the query",
	recv: "PRIVMSG nick :primary — non-primary.",
	result: `
		 <queryresult success='true'>
	         <pod primary='true'><subpod><plaintext>primary</plaintext></subpod></pod>
	         <pod><subpod><plaintext>non-primary</plaintext></subpod></pod>
		 </queryresult>
	`,
}, {
	// Skip empty subpods, even if it's a primary.
	send: "infer the query",
	recv: "PRIVMSG nick :non-empty.",
	result: `
		 <queryresult success='true'>
	         <pod primary='true'><subpod><plaintext></plaintext></subpod></pod>
	         <pod><subpod><plaintext>non-empty</plaintext></subpod></pod>
		 </queryresult>
	`,
}, {
	// Display titles.
	send: "infer the query",
	recv: "PRIVMSG nick :Pod one: Sub one: one.one; Sub two: one.two — Pod two: Sub one: two.one; Sub two: two.two.",
	result: `
		 <queryresult success='true'>
	         <pod primary='true' title='Pod one'>
		   <subpod title='Sub one'><plaintext>one.one</plaintext></subpod>
		   <subpod title='Sub two'><plaintext>one.two</plaintext></subpod>
		 </pod>
	         <pod primary='true' title='Pod two'>
		   <subpod title='Sub one'><plaintext>two.one</plaintext></subpod>
		   <subpod title='Sub two'><plaintext>two.two</plaintext></subpod>
		 </pod>
		 </queryresult>
	`,
}, {
	// Ignore boring result titles.
	send: "infer the query",
	recv: "PRIVMSG nick :one — two.",
	result: `
		 <queryresult success='true'>
	         <pod primary='true' title='Result'><subpod><plaintext>one</plaintext></subpod></pod>
	         <pod primary='true' title='Results'><subpod><plaintext>two</plaintext></subpod></pod>
		 </queryresult>
	`,
}, {
	// Join multiple lines with commas.
	send:   "infer the query",
	recv:   "PRIVMSG nick :one, two, three.",
	result: "<queryresult success='true'><pod><subpod><plaintext> one,\ntwo \n three</plaintext></subpod></pod></queryresult>",
}, {
	// " | " is not a good way to say " => " out of math. Also drop repeated " | " garbage.
	send: "infer the query",
	recv: "PRIVMSG nick :foo bar — foo|bar, baz.",
	result: `
		 <queryresult success='true'>
	         <pod primary='true'><subpod><plaintext>foo | | bar</plaintext></subpod></pod>
	         <pod primary='true'><subpod><plaintext>| foo|bar |
		 baz
		 </plaintext></subpod></pod>
		 </queryresult>
	`,
}, {
	// Break down long pods, and skip extremely long ones.
	send: "infer the query",
	recvAll: []string{
		"PRIVMSG nick :before — " + lorem + " — middle.",
		"PRIVMSG nick : — " + lorem + " — after.",
	},
	result: `
		 <queryresult success='true'>
	         <pod primary='true'><subpod><plaintext>before</plaintext></subpod></pod>
	         <pod primary='true'><subpod><plaintext>` + lorem + `</plaintext></subpod></pod>
	         <pod primary='true'><subpod><plaintext>` + lorem + lorem + `</plaintext></subpod></pod>
	         <pod primary='true'><subpod><plaintext>middle</plaintext></subpod></pod>
	         <pod primary='true'><subpod><plaintext>` + lorem + `</plaintext></subpod></pod>
	         <pod primary='true'><subpod><plaintext>after</plaintext></subpod></pod>
		 </queryresult>
	`,
}, {
	// If it's just an extremely long pod, show nothing.
	send: "infer the query",
	recv: "PRIVMSG nick :Cannot infer much out of this. :-(",
	result: `
		 <queryresult success='true'>
	         <pod primary='true'><subpod><plaintext>` + lorem + lorem + `</plaintext></subpod></pod>
		 </queryresult>
	`,
}, {
	// No relevant meaning understood from the input.
	send:   "infer the query",
	recv:   "PRIVMSG nick :Cannot infer much out of this. :-(",
	result: "<queryresult success='false'></queryresult>",
}, {
	// Apparent success with no parseable result.
	send:   "infer the query",
	recv:   "PRIVMSG nick :Cannot parse WolframAlpha response.",
	result: "<queryresult success='true'><queryresult>",
}, {
	// Non-200 status code from endpoint.
	send:   "infer the query",
	recv:   "PRIVMSG nick :WolframAlpha request failed. Please try again soon.",
	status: 500,
}, {
	// Detailed error result from service.
	send:   "infer the query",
	recv:   "PRIVMSG nick :WolframAlpha reported an error: the error",
	result: `<queryresult error='true'><error><msg>the error</msg></error></queryresult>`,
}, {
	// Non-XML response.
	send:   "infer the query",
	recv:   "PRIVMSG nick :Cannot parse WolframAlpha response.",
	result: "bogus",
}, {
	// LDAP location querying.
	sendAll: []string{"infer query one", "infer query two"},
	recvAll: []string{"PRIVMSG nick :the result.", "PRIVMSG nick :the result."},
	result:  "<queryresult success='true'><pod><subpod><plaintext>the result</plaintext></subpod></pod></queryresult>",
	ldap:    ldapConnFor("nick", "c", "Country", "st", "State", "l", "City"),
	config: bson.M{
		"ldap": "test",
	},
	form: url.Values{
		"location":      {"City, State, Country"},
		"input":         {"query two"},
		"format":        {"plaintext"},
	},
}, {
	// LDAP location querying without proper attributes.
	send:   "infer the query",
	recv:   "PRIVMSG nick :the result.",
	result: "<queryresult success='true'><pod><subpod><plaintext>the result</plaintext></subpod></pod></queryresult>",
	ldap:   ldapConnFor("nick", "other", "irrelevant"),
	config: bson.M{
		"ldap": "test",
	},
	form: url.Values{
		"ip":            {"host"},
		"input":         {"the query"},
		"format":        {"plaintext"},
	},
}, {
	// Bad LDAP connection name
	send: "infer the query",
	recv: "PRIVMSG nick :Plugin configuration error: LDAP connection \"unknown\" not found.",
	config: bson.M{
		"ldap": "unknown",
	},
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
			status: test.status,
		}
		server.Start()
		if test.config == nil {
			test.config = bson.M{}
		}
		test.config["endpoint"] = server.URL()

		tester := mup.NewPluginTester("wolframalpha")
		tester.SetConfig(test.config)
		tester.SetTargets(test.targets)
		if test.ldap != nil {
			tester.SetLDAP("test", test.ldap)
		}
		tester.Start()
		if test.send != "" {
			tester.Sendf(test.target, "%s", test.send)
		}
		if test.sendAll != nil {
			tester.SendAll(test.target, test.sendAll)
		}

		c.Check(tester.Stop(), IsNil)

		if test.recv != "" {
			c.Check(tester.Recv(), Equals, test.recv)
		}
		if test.recvAll != nil {
			c.Check(tester.RecvAll(), DeepEquals, test.recvAll)
		}

		server.Stop()

		if test.form != nil {
			for _, k := range []string{"appid", "parsetimeout", "scantimeout", "formattimeout", "podtimeout"} {
				if test.form[k] == nil {
					delete(server.form, k)
				}
			}
			c.Check(server.form, DeepEquals, test.form)
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

type alphaServer struct {
	result string
	status int
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
	if s.status != 0 {
		w.WriteHeader(s.status)
	}
	s.form = req.Form
	w.Write([]byte(s.result))
}

var lorem = strings.Replace(`
Lorem ipsum dolor sit amet, consectetur adipisicing elit, sed do
eiusmod tempor incididunt ut labore et dolore magna aliqua. Ut enim ad
minim veniam, quis nostrud exercitation ullamco laboris nisi ut aliquip
ex ea commodo consequat
`, "\n", "", -1)
