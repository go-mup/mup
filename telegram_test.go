package mup_test

import (
	"database/sql"
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
	"gopkg.in/mup.v0"
)

type TelegramSuite struct {
	tgserver tgServer
	config   *mup.Config
	server   *mup.Server
	lserver  *LineServer

	dbdir string
	db    *sql.DB
}

var _ = Suite(&TelegramSuite{})

func (s *TelegramSuite) SetUpSuite(c *C) {
	s.dbdir = c.MkDir()
}

func (s *TelegramSuite) SetUpTest(c *C) {
	s.tgserver.Start()

	mup.SetDebug(true)
	mup.SetLogger(c)

	var err error
	s.db, err = mup.OpenDB(s.dbdir)
	c.Assert(err, IsNil)

	s.config = &mup.Config{
		DB:      s.db,
		Refresh: -1, // Manual refreshing for testing.
	}

	execSQL(c, s.db,
		`INSERT INTO account (name,kind,host,password,nick) VALUES ('one','telegram','`+s.tgserver.Host()+`','<apikey>','ignored')`,
	)

	s.server, err = mup.Start(s.config)
	c.Assert(err, IsNil)
}

func (s *TelegramSuite) TearDownTest(c *C) {
	mup.SetDebug(false)
	mup.SetLogger(nil)

	s.server.Stop()
	s.server = nil

	s.db.Close()
	s.db = nil
	//c.Assert(mup.WipeDB(s.dbdir), IsNil)
	s.dbdir = c.MkDir()

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

var telegramIncomingTests = []struct {
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
		Lane:    1,
		Nick:    "bob",
		User:    "~user",
		Host:    "telegram",
		Command: "PRIVMSG",
		Channel: "@bob:56",
		Text:    "Hello mup!",
		BotText: "Hello mup!",
		Bang:    "/",
		AsNick:  "joe",
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
		Lane:    1,
		Nick:    "bob",
		User:    "~user",
		Host:    "telegram",
		Command: "PRIVMSG",
		Channel: "#Group_Chat:-78",
		Text:    "Hello there!",
		Bang:    "/",
		AsNick:  "joe",
	},
}}

func (s *TelegramSuite) TestIncoming(c *C) {
	var lastId int64
	for _, test := range telegramIncomingTests {
		before := time.Now().Add(-2 * time.Second)
		s.SendUpdates(c, test.update)

		var msg mup.Message
		var err error
		for i := 0; i < 10; i++ {
			row := s.db.QueryRow("SELECT id,lane,account,nick,user,host,command,channel,text,bottext,bang,asnick,time FROM message ORDER BY id DESC")
			err = row.Scan(&msg.Id, &msg.Lane, &msg.Account, &msg.Nick, &msg.User, &msg.Host, &msg.Command,
				&msg.Channel, &msg.Text, &msg.BotText, &msg.Bang, &msg.AsNick, &msg.Time)
			if err == nil && msg.Id != lastId {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if err == sql.ErrNoRows || msg.Id == lastId {
			c.Fatalf("Telegram update not received as an incoming message: %s", test.update)
		}
		c.Assert(err, IsNil)

		lastId = msg.Id

		after := time.Now().Add(2 * time.Second)
		c.Logf("Message time: %s", msg.Time)
		c.Assert(msg.Time.After(before), Equals, true)
		c.Assert(msg.Time.Before(after), Equals, true)

		msg.Time = time.Time{}
		msg.Id = 0
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

	// Ensure messages are only inserted after plugin has been loaded.
	s.server.RefreshAccounts()

	execSQL(c, s.db,
		`INSERT INTO message (lane,account,channel,nick,text) VALUES (2,'one','@nick:56','nick','Implicit PRIVMSG.')`,
		`INSERT INTO message (lane,account,channel,nick,text,command) VALUES (2,'one','@nick:56','nick','Explicit PRIVMSG.','PRIVMSG')`,
		`INSERT INTO message (lane,account,channel,nick,text,command) VALUES (2,'one','@nick:56','nick','Explicit NOTICE.','NOTICE')`,
		`INSERT INTO message (lane,account,channel,nick,text) VALUES (2,'one','#some_group:56','nick','Group chat.')`,
		`INSERT INTO message (lane,account,channel,nick,text) VALUES (2,'one','@nick:-56','nick','Negative chat id.')`,
		`INSERT INTO message (lane,account,channel,nick,text) VALUES (2,'one','#some_group:-56','nick','Negative group chat id.')`,
	)

	s.RecvMessage(c, 56, "Implicit PRIVMSG.")
	s.RecvMessage(c, 56, "Explicit PRIVMSG.")
	s.RecvMessage(c, 56, "Explicit NOTICE.")
	s.RecvMessage(c, 56, "Group chat.")
	s.RecvMessage(c, -56, "Negative chat id.")
	s.RecvMessage(c, -56, "Negative group chat id.")

	s.tgserver.FailSend()

	execSQL(c, s.db,
		`INSERT INTO message (lane,account,channel,nick,text) VALUES (2,'one','@nick:56','nick','Hello again!')`,
	)

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
	text, chat_id  string
	disablePreview bool
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
		msg := tgMessage{
			text:           req.Form.Get("text"),
			chat_id:        req.Form.Get("chat_id"),
			disablePreview: req.Form.Get("disable_web_page_preview") == "true",
		}
		select {
		case s.messages <- msg:
			fmt.Fprintf(w, `{"ok": true, "result": {}}`)
		case <-time.After(100 * time.Millisecond):
			panic("Client is sending messages much faster than test suite is trying to receive them")
		}

	case "getMe":
		fmt.Fprintf(w, `{"ok": true, "result": {"username": "joebot"}}`)

	default:
		fmt.Fprintf(w, `{"ok": false, "error_code": 404, "description": "unexpected test request for %s method"}`, method)
	}
}
