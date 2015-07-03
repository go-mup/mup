package mup_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	. "gopkg.in/check.v1"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
	"gopkg.in/mgo.v2/dbtest"
	"gopkg.in/mup.v0"
)

type TelegramSuite struct {
	tgserver tgServer
	dbserver dbtest.DBServer
	session  *mgo.Session
	config   *mup.Config
	server   *mup.Server
	lserver  *LineServer
}

var _ = Suite(&TelegramSuite{})

func (s *TelegramSuite) SetUpSuite(c *C) {
	s.dbserver.SetPath(c.MkDir())
}

func (s *TelegramSuite) TearDownSuite(c *C) {
	s.dbserver.Stop()
}

func (s *TelegramSuite) SetUpTest(c *C) {
	s.tgserver.Start()

	mup.SetDebug(true)
	mup.SetLogger(c)

	s.session = s.dbserver.Session()

	db := s.session.DB("")
	s.config = &mup.Config{
		Database: db,
		Refresh:  -1, // Manual refreshing for testing.
	}

	err := db.C("accounts").Insert(M{"_id": "one", "kind": "telegram", "host": s.tgserver.Host(), "password": "<apikey>", "nick": "ignored"})
	c.Assert(err, IsNil)

	s.server, err = mup.Start(s.config)
	c.Assert(err, IsNil)
}

func (s *TelegramSuite) TearDownTest(c *C) {
	defer s.dbserver.Wipe()

	s.session.Close()

	mup.SetDebug(false)
	mup.SetLogger(nil)

	s.server.Stop()
	s.server = nil

	s.tgserver.Stop()
}

func (s *TelegramSuite) TestPasswordAsKey(c *C) {
	s.server.Stop()
	c.Assert(s.tgserver.LastAPIKey(), Equals, "<apikey>")
}

func (s *TelegramSuite) TestQuit(c *C) {
	err := s.server.Stop()
	c.Assert(err, IsNil)
}

func (s *TelegramSuite) SendUpdates(c *C, update ...string) {
	err := s.tgserver.SendUpdates(update...)
	c.Assert(err, IsNil)
}

func (s *TelegramSuite) RecvMessage(c *C, chat_id int, text string) {
	msg, err := s.tgserver.RecvMessage()
	c.Assert(err, IsNil)

	id, err := strconv.Atoi(msg.chat_id)
	if err != nil {
		c.Fatalf("sendMessage called with invalid chat_id: %q", msg.chat_id)
	}

	c.Assert(id, Equals, chat_id)
	c.Assert(msg.text, Equals, text)
}

var incomingTests = []struct {
	update  string
	message mup.Message
}{{
	`{
		"update_id": 12,
		"message": {
			"message_id": 34,
			"from": {"id": 56, "username": "bob"},
			"chat": {"id": 56, "username": "bob"},
			"text": "Hello mup!"
		}
	}`,
	mup.Message{
		Account: "one",
		Nick:    "bob",
		User:    "~user",
		Host:    "telegram",
		Command: "PRIVMSG",
		Channel: "@bob:56",
		Text:    "Hello mup!",
		BotText: "Hello mup!",
		Bang:    "/",
		AsNick:  "mupbot",
	},
}, {
	`{
		"update_id": 13,
		"message": {
			"message_id": 34,
			"from": {"id": 56, "username": "bob"},
			"chat": {"id": -78, "title": "Group Chat"},
			"text": "Hello there!"
		}
	}`,
	mup.Message{
		Account: "one",
		Nick:    "bob",
		User:    "~user",
		Host:    "telegram",
		Command: "PRIVMSG",
		Channel: "#Group_Chat:-78",
		Text:    "Hello there!",
		Bang:    "/",
		AsNick:  "mupbot",
	},
}}

func (s *TelegramSuite) TestIncoming(c *C) {
	incoming := s.session.DB("").C("incoming")

	var lastId bson.ObjectId
	for _, test := range incomingTests {
		before := time.Now().Add(-2 * time.Second)
		s.SendUpdates(c, test.update)

		var msg mup.Message
		var err error
		for i := 0; i < 10; i++ {
			err = incoming.Find(nil).Sort("-$natural").One(&msg)
			if err == nil && msg.Id != lastId {
				break
			}
		}
		if err == mgo.ErrNotFound || msg.Id == lastId {
			c.Fatalf("Telegram update not received as an incoming message: %s", test.update)
		}
		c.Assert(err, IsNil)

		lastId = msg.Id

		after := time.Now().Add(2 * time.Second)
		c.Logf("Message time: %s", msg.Time)
		c.Assert(msg.Time.After(before), Equals, true)
		c.Assert(msg.Time.Before(after), Equals, true)

		msg.Time = time.Time{}
		msg.Id = ""
		c.Assert(msg, DeepEquals, test.message)

		// Check that the client is providing the right offset to consume messages.
		var update struct {
			Id int `json:"update_id"`
		}
		err = json.Unmarshal([]byte(test.update), &update)
		c.Assert(err, IsNil)
		c.Assert(s.tgserver.LastUpdateOffset(), Equals, update.Id+1)
	}
}

func (s *TelegramSuite) TestOutgoing(c *C) {

	outgoing := s.session.DB("").C("outgoing")
	err := outgoing.Insert(
		&mup.Message{Account: "one", Channel: "@nick:56", Nick: "nick", Text: "Implicit PRIVMSG."},
		&mup.Message{Account: "one", Channel: "@nick:56", Nick: "nick", Text: "Explicit PRIVMSG.", Command: "PRIVMSG"},
		&mup.Message{Account: "one", Channel: "@nick:56", Nick: "nick", Text: "Explicit NOTICE.", Command: "NOTICE"},
		&mup.Message{Account: "one", Channel: "#some_group:56", Nick: "nick", Text: "Group chat."},
		&mup.Message{Account: "one", Channel: "@nick:-56", Nick: "nick", Text: "Negative chat id."},
		&mup.Message{Account: "one", Channel: "#some_group:-56", Nick: "nick", Text: "Negative group chat id."},
	)
	c.Assert(err, IsNil)

	s.RecvMessage(c, 56, "Implicit PRIVMSG.")
	s.RecvMessage(c, 56, "Explicit PRIVMSG.")
	s.RecvMessage(c, 56, "Explicit NOTICE.")
	s.RecvMessage(c, 56, "Group chat.")
	s.RecvMessage(c, -56, "Negative chat id.")
	s.RecvMessage(c, -56, "Negative group chat id.")

	s.tgserver.FailSend()

	err = outgoing.Insert(&mup.Message{
		Account: "one",
		Channel: "@nick:56",
		Nick:    "nick",
		Text:    "Hello again!",
	})
	c.Assert(err, IsNil)

	// Delivered first time, when the server reported back an error to the client.
	s.RecvMessage(c, 56, "Hello again!")

	// Force an account refresh to bring the dead account back.
	time.Sleep(50 * time.Millisecond)
	s.server.RefreshAccounts()

	// Should be delivered again due to the missing confirmation.
	s.RecvMessage(c, 56, "Hello again!")
}

type tgServer struct {
	server *httptest.Server

	updates  chan string
	messages chan tgMessage
	failSend chan bool

	mu               sync.Mutex
	lastAPIKey       string
	lastUpdateOffset int
}

type tgMessage struct {
	text, chat_id string
}

func (s *tgServer) Start() {
	*s = tgServer{
		server:   httptest.NewServer(s),
		updates:  make(chan string),
		messages: make(chan tgMessage, 10),
		failSend: make(chan bool, 10),
	}
}

func (s *tgServer) Stop() {
	s.server.Close()
}

func (s *tgServer) Host() string {
	u, err := url.Parse(s.server.URL)
	if err != nil {
		panic(err)
	}
	return u.Host
}

func (s *tgServer) SendUpdates(update ...string) error {
	json := fmt.Sprintf(`{"ok": true, "result": [` + strings.Join(update, ", ") + `]}`)
	select {
	case s.updates <- json:
		return nil
	case <-time.After(500 * time.Millisecond):
	}
	return fmt.Errorf("Telegram client did not attempt to receive updates")
}

func (s *tgServer) RecvMessage() (tgMessage, error) {
	select {
	case msg := <-s.messages:
		return msg, nil
	case <-time.After(1500 * time.Millisecond):
	}
	return tgMessage{}, fmt.Errorf("Telegram client did not attempt to send messages")
}

func (s *tgServer) FailSend() {
	select {
	case s.failSend <- true:
	default:
		panic("Trying to enqueue too many failures without the client receiving any of them.")
	}
}

func (s *tgServer) LastUpdateOffset() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastUpdateOffset
}

func (s *tgServer) LastAPIKey() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastAPIKey
}

func (s *tgServer) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	req.ParseForm()

	tokens := strings.Split(req.URL.Path, "/")
	if len(tokens) != 3 || tokens[0] != "" || !strings.HasPrefix(tokens[1], "bot") {
		panic("Got unexpected request for " + req.URL.Path + " in test tgServer")
	}

	s.mu.Lock()
	s.lastAPIKey = strings.TrimPrefix(tokens[1], "bot")
	s.mu.Unlock()

	switch method := tokens[2]; method {

	case "getUpdates":
		offset := req.Form.Get("offset")
		if offset != "" {
			n, err := strconv.Atoi(offset)
			if err != nil {
				panic("invalid getUpdates offset: " + offset)
			}
			s.mu.Lock()
			s.lastUpdateOffset = n
			s.mu.Unlock()
		}

		select {
		case json := <-s.updates:
			w.Write([]byte(json))
		case <-time.After(50 * time.Millisecond):
			fmt.Fprintf(w, `{"ok": true, "result": []}`)
		}

	case "sendMessage":
		select {
		case <-s.failSend:
			fmt.Fprintf(w, `{"ok": false, "description": "failure requested by test suite"}`)
		default:
		}
		select {
		case s.messages <- tgMessage{text: req.Form.Get("text"), chat_id: req.Form.Get("chat_id")}:
			fmt.Fprintf(w, `{"ok": true, "result": {}}`)
		case <-time.After(100 * time.Millisecond):
			panic("Client is sending messages much faster than test suite is trying to receive them")
		}

	case "getMe":
		fmt.Fprintf(w, `{"ok": true, "result": {"username": "mupbot"}}`)

	default:
		fmt.Fprintf(w, `{"ok": false, "error_code": 404, "description": "unexpected test request for %s method"}`, method)
	}
}
