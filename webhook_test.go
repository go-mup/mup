package mup_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"

	. "gopkg.in/check.v1"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/dbtest"
	"gopkg.in/mup.v0"
)

type WebHookSuite struct {
	whserver webhookServer
	dbserver dbtest.DBServer
	session  *mgo.Session
	config   *mup.Config
	server   *mup.Server
	lserver  *LineServer
}

var _ = Suite(&WebHookSuite{})

func (s *WebHookSuite) SetUpSuite(c *C) {
	s.dbserver.SetPath(c.MkDir())
}

func (s *WebHookSuite) TearDownSuite(c *C) {
	s.dbserver.Stop()
}

func (s *WebHookSuite) SetUpTest(c *C) {
	s.whserver.Start()

	mup.SetDebug(true)
	mup.SetLogger(c)

	s.session = s.dbserver.Session()

	db := s.session.DB("")
	s.config = &mup.Config{
		Database: db,
		Refresh:  -1, // Manual refreshing for testing.
	}

	err := db.C("accounts").Insert(M{"_id": "one", "kind": "webhook", "endpoint": "http://" + s.whserver.Host() + "/some/endpoint"})
	c.Assert(err, IsNil)

	s.server, err = mup.Start(s.config)
	c.Assert(err, IsNil)
}

func (s *WebHookSuite) TearDownTest(c *C) {
	defer s.dbserver.Wipe()

	s.session.Close()

	mup.SetDebug(false)
	mup.SetLogger(nil)

	s.server.Stop()
	s.server = nil

	s.whserver.Stop()
}

func (s *WebHookSuite) TestQuit(c *C) {
	err := s.server.Stop()
	c.Assert(err, IsNil)
}

func (s *WebHookSuite) SendUpdates(c *C, update ...string) {
	err := s.whserver.SendUpdates(update...)
	c.Assert(err, IsNil)
}

func (s *WebHookSuite) RecvMessage(c *C, channel, text string) {
	msg, err := s.whserver.RecvMessage()
	c.Assert(err, IsNil)
	c.Assert(msg.Channel, Equals, channel)
	c.Assert(msg.Text, Equals, text)
	c.Assert(msg.Groupable, Equals, true)
}

func (s *WebHookSuite) TestOutgoing(c *C) {

	outgoing := s.session.DB("").C("outgoing")
	err := outgoing.Insert(
		&mup.Message{Account: "one", Nick: "nick", Text: "Implicit PRIVMSG."},
		&mup.Message{Account: "one", Nick: "nick", Text: "Explicit PRIVMSG.", Command: "PRIVMSG"},
		&mup.Message{Account: "one", Nick: "nick", Text: "Explicit NOTICE.", Command: "NOTICE"},
		&mup.Message{Account: "one", Channel: "#some_group", Nick: "nick", Text: "Group chat."},
	)
	c.Assert(err, IsNil)

	s.RecvMessage(c, "@nick", "Implicit PRIVMSG.")
	s.RecvMessage(c, "@nick", "Explicit PRIVMSG.")
	s.RecvMessage(c, "@nick", "Explicit NOTICE.")
	s.RecvMessage(c, "#some_group", "Group chat.")

	s.whserver.FailSend()

	err = outgoing.Insert(&mup.Message{
		Account: "one",
		Nick:    "nick",
		Text:    "Hello again!",
	})
	c.Assert(err, IsNil)

	// Delivered first time, when the server reported back an error to the client.
	s.RecvMessage(c, "@nick", "Hello again!")

	// Force an account refresh to bring the dead account back.
	time.Sleep(50 * time.Millisecond)
	s.server.RefreshAccounts()

	// Should be delivered again due to the missing confirmation.
	s.RecvMessage(c, "@nick", "Hello again!")
}

type webhookServer struct {
	server *httptest.Server

	updates  chan string
	messages chan webhookMessage
	failSend chan bool
}

type webhookMessage struct {
	Channel   string `json:"channel"`
	Text      string `json:"text"`
	Groupable bool   `json:"groupable"`
}

func (s *webhookServer) Start() {
	*s = webhookServer{
		server:   httptest.NewServer(s),
		updates:  make(chan string),
		messages: make(chan webhookMessage, 10),
		failSend: make(chan bool, 10),
	}
}

func (s *webhookServer) Stop() {
	s.server.Close()
}

func (s *webhookServer) Host() string {
	u, err := url.Parse(s.server.URL)
	if err != nil {
		panic(err)
	}
	return u.Host
}

func (s *webhookServer) SendUpdates(update ...string) error {
	json := fmt.Sprintf(`{"ok": true, "result": [` + strings.Join(update, ", ") + `]}`)
	select {
	case s.updates <- json:
		return nil
	case <-time.After(500 * time.Millisecond):
	}
	return fmt.Errorf("WebHook client did not attempt to receive updates")
}

func (s *webhookServer) RecvMessage() (webhookMessage, error) {
	select {
	case msg := <-s.messages:
		return msg, nil
	case <-time.After(1500 * time.Millisecond):
	}
	return webhookMessage{}, fmt.Errorf("WebHook client did not attempt to send messages")
}

func (s *webhookServer) FailSend() {
	select {
	case s.failSend <- true:
	default:
		panic("Trying to enqueue too many failures without the client receiving any of them.")
	}
}

func (s *webhookServer) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	req.ParseForm()

	if req.URL.Path != "/some/endpoint" {
		panic("Got unexpected request for " + req.URL.Path + " in test webhookServer")
	}

	select {
	case <-s.failSend:
		fmt.Fprintf(w, `{"success": false, "error": "error-something-wrong"}`)
	default:
	}
	var msg webhookMessage
	payload := req.Form.Get("payload")
	err := json.Unmarshal([]byte(payload), &msg)
	if err != nil {
		panic("Client sent invalid WebHook Chat JSON message: " + payload)
	}
	select {
	case s.messages <- msg:
		fmt.Fprintf(w, `{"success": true}`)
	case <-time.After(100 * time.Millisecond):
		panic("Client is sending messages much faster than test suite is trying to receive them")
	}
}
