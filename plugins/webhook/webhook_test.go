package webhook_test

import (
	"testing"

	"net/http"
	"net/url"

	"gopkg.in/mgo.v2/bson"
	"gopkg.in/mup.v0"

	. "gopkg.in/check.v1"

	_ "gopkg.in/mup.v0/plugins/webhook"
	"net"
)

func Test(t *testing.T) { TestingT(t) }

var _ = Suite(&WebHookSuite{})

type WebHookSuite struct{}

func (s *WebHookSuite) SetUpSuite(c *C) {
	mup.SetLogger(c)
	mup.SetDebug(true)
}

func (s *WebHookSuite) TearDownSuite(c *C) {
	mup.SetLogger(nil)
	mup.SetDebug(false)
}

type webhookTest struct {
	payload string
	message string
	config  bson.M
	targets []bson.M
}

var webhookTests = []webhookTest{{
	// Missing target.
	payload: `{"token": "secret", "user_name": "nick", "text": "Hello"}`,
	config:  bson.M{"tokens": []string{"secret"}},
	message: ``,
}, {
	// Bad secret.
	payload: `{"token": "bad", "user_name": "nick", "text": "Hello"}`,
	config:  bson.M{"tokens": []string{"secret"}},
	targets: []bson.M{{"account": "test"}},
	message: ``,
}, {
	// All good.
	payload: `{"token": "secret", "user_name": "nick", "text": "Hello"}`,
	message: `:nick!~user@webhook PRIVMSG mup :Hello`,
	config:  bson.M{"tokens": []string{"secret"}},
	targets: []bson.M{{"account": "test"}},
}, {
	// In a channel.
	payload: `{"token": "secret", "user_name": "nick", "channel_name": "#chan", "text": "Hello"}`,
	message: `:nick!~user@webhook PRIVMSG #chan :Hello`,
	config:  bson.M{"tokens": []string{"secret"}},
	targets: []bson.M{{"account": "test"}},
}, {
	// Different account.
	payload: `{"token": "secret", "user_name": "nick", "channel_name": "#chan", "text": "Hello"}`,
	message: `[@other] :nick!~user@webhook PRIVMSG #chan :Hello`,
	config:  bson.M{"tokens": []string{"secret"}},
	targets: []bson.M{{"account": "other"}},
}}

func (s *WebHookSuite) TestIn(c *C) {
	transport := &http.Transport{DisableKeepAlives: true}
	client := http.Client{Transport: transport}

	for i, test := range webhookTests {
		c.Logf("Testing payload #%d: %s", i, test.payload)
		tester := mup.NewPluginTester("webhook")
		if test.config == nil {
			test.config = bson.M{}
		}
		test.config["addr"] = ":10645"
		tester.SetConfig(test.config)
		tester.SetTargets(test.targets)
		tester.Start()

		for i := 0; i < 100; i++ {
			conn, err := net.Dial("tcp", "localhost:10645")
			if err == nil {
				conn.Close()
				break
			}
		}

		resp, err := client.PostForm("http://localhost:10645/", url.Values{"payload": {test.payload}})
		c.Assert(err, IsNil)
		resp.Body.Close()

		tester.Stop()
		c.Assert(tester.RecvIncoming(), Equals, test.message)
	}
}
