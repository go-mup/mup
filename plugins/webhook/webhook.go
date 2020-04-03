package webhook

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"gopkg.in/mup.v0"
	"gopkg.in/tomb.v2"
	"io"
	"io/ioutil"
	"strings"
)

var Plugin = mup.PluginSpec{
	Name: "webhook",
	Help: `Starts an HTTP server to receive webhook-style payloads.
	
	The payload must be formatted according to Rocket Chat and Slack webhooks,
	with a "payload" HTTP parameter that contains a JSON object with "text",
	"channel_name", and "user_name" fields, and also a "token" field used for
	authentication that must match one of the strings in the "tokens" list
	in the plugin configuration. The message is only received by mup if the
	message origin matches one of the plugin targets.

	The address to listen on may be changed via the "addr" configuration
	option. If not provided the address 0.0.0.0:10456 is used.
	`,
	Start: start,
}

func init() {
	mup.RegisterPlugin(&Plugin)
}

type webhookPlugin struct {
	mu       sync.Mutex
	tomb     tomb.Tomb
	plugger  *mup.Plugger
	listener net.Listener
	config   struct {
		Tokens []string
		Nick   string
		Addr   string
	}
}

const defaultAddr = ":10456"

func start(plugger *mup.Plugger) mup.Stopper {
	p := &webhookPlugin{
		plugger: plugger,
	}
	err := p.plugger.UnmarshalConfig(&p.config)
	if err != nil {
		plugger.Logf("%v", err)
	}
	if p.config.Nick == "" {
		p.config.Nick = "mup"
	}
	if p.config.Addr == "" {
		p.config.Addr = defaultAddr
	}
	p.tomb.Go(p.loop)
	return p
}

func (p *webhookPlugin) Stop() error {
	p.tomb.Kill(nil)
	p.mu.Lock()
	if p.listener != nil {
		p.listener.Close()
	}
	p.mu.Unlock()
	p.plugger.Logf("Waiting.")
	return p.tomb.Wait()
}

func (p *webhookPlugin) loop() error {
	first := true
	for p.tomb.Alive() {
		l, err := net.Listen("tcp", p.config.Addr)
		if err != nil {
			if first {
				first = false
				p.plugger.Logf("Cannot listen on %s (%v). Will keep retrying.", p.config.Addr, err)
			}
			time.Sleep(500 * time.Millisecond)
			continue
		}
		p.plugger.Logf("Listening on %s.", p.config.Addr)

		p.mu.Lock()
		p.listener = l
		p.mu.Unlock()

		server := &http.Server{
			Addr:         p.config.Addr,
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
			Handler:      p,
		}

		err = server.Serve(l)
		if p.tomb.Alive() {
			p.tomb.Kill(err)
		}
		l.Close()
	}
	return nil
}

type payloadMessage struct {
	Token       string      `json:"token"`        // "jhkjK7Khwe7whekjhwe7lwlh"
	Timestamp   string      `json:"timestamp"`    // "2016-09-05T02:54:47.616Z"
	Text        string      `json:"text"`         // "Hello there"
	ChannelID   string      `json:"channel_id"`   // "3kjhkKQkjwekjwjwkJhkewjhqejKWM8Lvw"
	ChannelName string      `json:"channel_name"` // "#general" (not there for private)
	UserID      string      `json:"user_id"`      // "Kh41HKEqnjekqwekj"
	UserName    string      `json:"user_name"`    // "joe"
	Bot         interface{} `json:"bot"`          // false or {"i": "<id>"}
}

func (p *webhookPlugin) hasToken(token string) bool {
	for _, t := range p.config.Tokens {
		if token == t {
			return true
		}
	}
	return false
}

func (p *webhookPlugin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	contentType := r.Header.Get("Content-Type")
	payloadData, err := ioutil.ReadAll(&io.LimitedReader{R: r.Body, N: 16385})
	if len(payloadData) == 0 || r.Method != "POST" || contentType != "application/json" {
		p.plugger.Logf("Got request with empty payload (%d) or invalid method (%s)", len(payloadData), r.Method)
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"success:": false, "message": "message must be POSTed as JSON in request body with proper content-type"}`))
		return
	}

	pmsg := payloadMessage{}
	err = json.Unmarshal([]byte(payloadData), &pmsg)
	if err != nil {
		p.plugger.Logf("Cannot unmarshal provided JSON payload: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"success:": false, "message": "cannot unmarshal provided JSON payload"}`))
		return
	}

	if pmsg.Bot != nil && pmsg.Bot != false {
		// Ignore bot messages.
		return
	}

	if !p.hasToken(pmsg.Token) {
		p.plugger.Logf("Invalid token received: %s", pmsg.Token)
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"success:": false, "message": "invalid token"}`))
		return
	}

	if pmsg.UserName == "" || pmsg.Text == "" {
		p.plugger.Logf("Invalid payload received: %s", string(payloadData))
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"success:": false, "message": "must provide at least user_name and text"}`))
		return
	}
	if pmsg.UserID == "" {
		pmsg.UserID = "user"
	}
	if pmsg.ChannelName == "" {
		pmsg.ChannelName = p.config.Nick
	} else if !strings.HasPrefix(pmsg.ChannelName, "#") {
		pmsg.ChannelName = "#" + pmsg.ChannelName
	}

	line := fmt.Sprintf(":%s!~%s@webhook PRIVMSG %s :%s", pmsg.UserName, pmsg.UserID, pmsg.ChannelName, pmsg.Text)
	msg := mup.ParseIncoming("", p.config.Nick, "!", line)
	p.plugger.Logf("Received message: %s", msg)
	err = p.plugger.Handle(msg)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"success:": false, "message": "cannot enqueue message"}`))
		return
	}
}
