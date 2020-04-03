package webhook_test

import (
	"bytes"
	"net"
	"net/http"
	"testing"

	"gopkg.in/mup.v0"
	_ "gopkg.in/mup.v0/plugins/webhook"

	. "gopkg.in/check.v1"
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
	config  mup.Map
	targets []mup.Target
}

var webhookTests = []webhookTest{{
	// Missing target.
	payload: `{"token": "secret", "user_name": "nick", "text": "Hello"}`,
	config:  mup.Map{"tokens": []string{"secret"}},
	message: ``,
}, {
	// Bad secret.
	payload: `{"token": "bad", "user_name": "nick", "text": "Hello"}`,
	config:  mup.Map{"tokens": []string{"secret"}},
	targets: []mup.Target{{Account: "test"}},
	message: ``,
}, {
	// All good.
	payload: `{"token": "secret", "user_name": "nick", "text": "Hello"}`,
	message: `:nick!~user@webhook PRIVMSG mup :Hello`,
	config:  mup.Map{"tokens": []string{"secret"}},
	targets: []mup.Target{{Account: "test"}},
}, {
	// In a channel.
	payload: `{"token": "secret", "user_name": "nick", "channel_name": "#chan", "text": "Hello"}`,
	message: `:nick!~user@webhook PRIVMSG #chan :Hello`,
	config:  mup.Map{"tokens": []string{"secret"}},
	targets: []mup.Target{{Account: "test"}},
}, {
	// In a channel, without # in name.
	payload: `{"token": "secret", "user_name": "nick", "channel_name": "chan", "text": "Hello"}`,
	message: `:nick!~user@webhook PRIVMSG #chan :Hello`,
	config:  mup.Map{"tokens": []string{"secret"}},
	targets: []mup.Target{{Account: "test"}},
}, {
	// Different account.
	payload: `{"token": "secret", "user_name": "nick", "channel_name": "#chan", "text": "Hello"}`,
	message: `[@other] :nick!~user@webhook PRIVMSG #chan :Hello`,
	config:  mup.Map{"tokens": []string{"secret"}},
	targets: []mup.Target{{Account: "other"}},
}, {
	// From a bot.
	payload: `{"token": "secret", "user_name": "nick", "channel_name": "chan", "text": "Hello", "bot": true}`,
	message: ``,
	config:  mup.Map{"tokens": []string{"secret"}},
	targets: []mup.Target{{Account: "test"}},
}, {
	// From a bot with a document.
	payload: `{"token": "secret", "user_name": "nick", "channel_name": "chan", "text": "Hello", "bot": {"i": "foobar"}}`,
	message: ``,
	config:  mup.Map{"tokens": []string{"secret"}},
	targets: []mup.Target{{Account: "test"}},
}}

func (s *WebHookSuite) TestIn(c *C) {
	transport := &http.Transport{DisableKeepAlives: true}
	client := http.Client{Transport: transport}

	for i, test := range webhookTests {
		c.Logf("Testing payload #%d: %s", i, test.payload)
		tester := mup.NewPluginTester("webhook")
		if test.config == nil {
			test.config = mup.Map{}
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

		resp, err := client.Post("http://localhost:10645/", "application/json", bytes.NewBufferString(test.payload))
		c.Assert(err, IsNil)
		resp.Body.Close()

		tester.Stop()
		c.Assert(tester.RecvIncoming(), Equals, test.message)
	}
}
