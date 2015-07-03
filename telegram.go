package mup

import (
	"encoding/json"
	"fmt"
	"gopkg.in/mgo.v2/bson"
	"gopkg.in/tomb.v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const tgBotPrefix = "https://api.telegram.org/bot"

type tgClient struct {
	accountName string

	dying <-chan struct{}
	info  accountInfo
	tomb  tomb.Tomb
	tgR   *tgReader
	tgW   *tgWriter

	requests chan interface{}

	incoming chan *Message
	outgoing chan *Message
	lastId   bson.ObjectId
}

func (c *tgClient) AccountName() string     { return c.accountName }
func (c *tgClient) Dying() <-chan struct{}  { return c.dying }
func (c *tgClient) Outgoing() chan *Message { return c.outgoing }
func (c *tgClient) LastId() bson.ObjectId   { return c.lastId }

func startTgClient(info *accountInfo, incoming chan *Message) accountClient {
	c := &tgClient{
		accountName: info.Name,

		info:     *info,
		requests: make(chan interface{}, 1),
		incoming: incoming,
		outgoing: make(chan *Message),
	}
	c.lastId = c.info.LastId
	c.dying = c.tomb.Dying()
	c.tomb.Go(c.run)
	return c
}

func (c *tgClient) Alive() bool {
	return c.tomb.Alive()
}

func (c *tgClient) Stop() error {
	// Try to disconnect gracefully.
	timeout := time.After(NetworkTimeout)
	select {
	case c.outgoing <- &Message{Command: cmdQuit}:
		select {
		case <-c.dying:
		case <-timeout:
		}
	case <-c.dying:
	case <-timeout:
	}
	c.tomb.Kill(errStop)
	err := c.tomb.Wait()
	if err != errStop {
		return err
	}
	return nil
}

// UpdateInfo updates the account information. Everything but
// the account name may be updated.
func (c *tgClient) UpdateInfo(info *accountInfo) {
	if info.Name != c.accountName {
		panic("cannot change the account name")
	}
	// Make a copy as its use will continue after returning to the caller.
	infoCopy := *info
	select {
	case c.requests <- ireqUpdateInfo(&infoCopy):
	case <-c.dying:
	}
}

func (c *tgClient) die() {
	logf("[%s] Cleaning Telegram connection resources", c.accountName)

	if c.tgW != nil {
		err := c.tgW.Stop()
		if err != nil {
			logf("[%s] Telegram writer failure: %s", c.accountName, err)
		}
	}
	if c.tgR != nil {
		err := c.tgR.Stop()
		if err != nil {
			logf("[%s] Telegram reader failure: %s", c.accountName, err)
		}
	}

	c.tomb.Kill(nil)
	logf("[%s] Telegram client terminated (%v)", c.accountName, c.tomb.Err())
}

func (c *tgClient) run() error {
	defer c.die()

	apiPrefix := tgBotPrefix
	if c.info.Host != "" {
		apiPrefix = "http://" + c.info.Host + "/bot"
	}

	c.tgR = startTgReader(c.accountName, apiPrefix, c.info.Password)
	c.tgW = startTgWriter(c.accountName, apiPrefix, c.info.Password, c.tgR)

	var inMsg, outMsg *Message
	var inRecv, outRecv <-chan *Message
	var inSend, outSend chan<- *Message

	inRecv = c.tgR.Incoming
	outRecv = c.outgoing

	quitting := false
	for {
		select {
		case inMsg = <-inRecv:
			inRecv = nil
			inSend = c.incoming

		case inSend <- inMsg:
			inMsg = nil
			inRecv = c.tgR.Incoming
			inSend = nil

		case outMsg = <-outRecv:
			if outMsg.Command == cmdQuit {
				quitting = true
			}
			outRecv = nil
			outSend = c.tgW.Outgoing

		case outSend <- outMsg:
			outMsg = nil
			outRecv = c.outgoing
			outSend = nil

		case req := <-c.requests:
			switch r := req.(type) {
			case ireqUpdateInfo:
				// TODO Restart if API key changes.
				c.info = *r
			}

		case <-c.dying:
			return c.tomb.Err()
		case <-c.tgR.Dying:
			if quitting {
				return errStop
			}
			return c.tgR.Err()
		case <-c.tgW.Dying:
			if quitting {
				return errStop
			}
			return c.tgW.Err()
		}
	}
	panic("unreachable")
}

// ---------------------------------------------------------------------------
// tgWriter

// An tgWriter reads messages from the Outgoing channel and sends it to the server.
type tgWriter struct {
	accountName string
	apiPrefix   string
	apiKey      string
	r           *tgReader
	tomb        tomb.Tomb

	Dying    <-chan struct{}
	Outgoing chan *Message
}

func startTgWriter(accountName, apiPrefix, apiKey string, r *tgReader) *tgWriter {
	w := &tgWriter{
		accountName: accountName,
		apiPrefix:   apiPrefix,
		apiKey:      apiKey,
		r:           r,
		Outgoing:    make(chan *Message, 1),
	}
	w.Dying = w.tomb.Dying()
	w.tomb.Go(w.loop)
	return w
}

func (w *tgWriter) Err() error {
	return w.tomb.Err()
}

func (w *tgWriter) Stop() error {
	debugf("[%s] Requesting writer to stop...", w.accountName)
	w.tomb.Kill(errStop)
	err := w.tomb.Wait()
	if err != errStop {
		return err
	}
	return nil
}

func (w *tgWriter) Send(msg *Message) error {
	select {
	case w.Outgoing <- msg:
	case <-w.Dying:
		return w.Err()
	}
	return nil
}

func (w *tgWriter) Sendf(format string, args ...interface{}) error {
	return w.Send(ParseOutgoing(w.accountName, fmt.Sprintf(format, args...)))
}

func (w *tgWriter) die() {
	debugf("[%s] Writer is dead (%v)", w.accountName, w.tomb.Err())
}

func (w *tgWriter) loop() error {
	defer w.die()

loop:
	for {
		var msg *Message
		select {
		case msg = <-w.Outgoing:
		case <-w.Dying:
			break loop
		}
		switch msg.Command {
		case cmdQuit:
			break loop
		case "", cmdPrivMsg, cmdNotice:
			break
		default:
			continue
		}

		logf("[%s] Sending: %s", w.accountName, msg.String())

		var err error
		var chatId int64
		if len(msg.Channel) > 2 && (msg.Channel[0] == '#' || msg.Channel[0] == '@') {
			i := strings.LastIndex(msg.Channel, ":")
			if i > 0 {
				chatId, err = strconv.ParseInt(msg.Channel[i+1:], 10, 64)
			}
		}
		if chatId == 0 || err != nil {
			logf("[%s] Outgoing Telegram message with invalid channel: %q", w.accountName, msg.Channel)
			continue
		}

		params := url.Values{
			"chat_id": []string{strconv.FormatInt(chatId, 10)},
			"text":    []string{msg.Text},
		}
		resp, err := httpClient.PostForm(w.apiPrefix+w.apiKey+"/sendMessage", params)
		if err != nil {
			w.tomb.Kill(err)
			break
		}
		decoder := json.NewDecoder(resp.Body)

		var result tgResultStatus
		err = decoder.Decode(&result)
		resp.Body.Close()
		if err != nil {
			w.tomb.Kill(err)
			break
		}
		if err = result.err(); err != nil {
			w.tomb.Killf("on sendMessage: %v", err)
			break
		}

		// Notify the account manager that the message was delivered.
		select {
		case w.r.Incoming <- ParseIncoming(w.accountName, "mup", "/", "PONG :sent:"+msg.Id.Hex()):
		case <-w.Dying:
		case <-w.r.Dying:
			break
		}
	}

	return nil
}

type tgResultStatus struct {
	Ok          bool   `json:"ok"`
	ErrorCode   int    `json:"error_code"`
	Description string `json:"description"`
}

func (result *tgResultStatus) err() error {
	if result.Ok {
		return nil
	}
	if result.Description == "" {
		result.Description = "no error description"
	}
	return fmt.Errorf("error code %d - %s", result.ErrorCode, result.Description)
}

// ---------------------------------------------------------------------------
// tgReader

// An tgReader reads lines from the server and injects it in the Incoming channel.
type tgReader struct {
	accountName string
	apiPrefix   string
	apiKey      string
	activeNick  string
	tomb        tomb.Tomb

	Dying    <-chan struct{}
	Incoming chan *Message
}

func startTgReader(accountName, apiPrefix, apiKey string) *tgReader {
	r := &tgReader{
		accountName: accountName,
		apiPrefix:   apiPrefix,
		apiKey:      apiKey,
		Incoming:    make(chan *Message, 1),
	}
	r.Dying = r.tomb.Dying()
	r.tomb.Go(r.loop)
	return r
}

func (r *tgReader) Err() error {
	return r.tomb.Err()
}

func (r *tgReader) Stop() error {
	debugf("[%s] Requesting Telegram reader to stop...", r.accountName)
	r.tomb.Kill(errStop)
	err := r.tomb.Wait()
	if err != errStop {
		return err
	}
	return nil
}

func (r *tgReader) die() {
	debugf("[%s] Reader is dead (%v)", r.accountName, r.tomb.Err())
}

var httpClient = http.Client{Timeout: NetworkTimeout}

type tgUpdate struct {
	tgResultStatus
	Result []tgUpdateResult `json:"result"`
}

type tgUpdateResult struct {
	UpdateId int64           `json:"update_id"`
	Message  tgUpdateMessage `json:"message"`
}

type tgUpdateMessage struct {
	MessageId int64        `json:"message_id"`
	From      tgUpdateFrom `json:"from"`
	Chat      tgUpdateChat `json:"chat"`
	Date      uint64       `json:"date"`
	Text      string       `json:"text"`
}

type tgUpdateFrom struct {
	Id        int64  `json:"id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

type tgUpdateChat struct {
	Id int64 `json:"id"`

	// For group chats.
	Title        string `json:"title"`
	GroupCreated bool   `json:"group_chat_created"`
	GroupJoined  bool   `json:"new_chat_participant"`
	GroupParted  bool   `json:"left_chat_participant"`

	// For one-to-one chats.
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

type respStyle string

const (
	respStyleNoNick    respStyle = "x"
	respStyleNickColon respStyle = ":"
	respStyleNickComma respStyle = ","
	respStyleAtNick    respStyle = "@"
)

func (r *tgReader) updateNick() error {
	resp, err := httpClient.Get(r.apiPrefix + r.apiKey + "/getMe")
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(resp.Body)
	var result struct {
		tgResultStatus
		Result struct {
			Username string `json:"username"`
		} `json:"result"`
	}
	err = decoder.Decode(&result)
	resp.Body.Close()
	if err != nil {
		return err
	}
	if err = result.err(); err != nil {
		return err
	}
	r.activeNick = result.Result.Username
	logf("[%s] Using retrieved Telegram bot nick: %s", r.accountName, r.activeNick)
	return nil
}

func (r *tgReader) loop() error {
	defer r.die()

	err := r.updateNick()
	if err != nil {
		logf("[%s] Cannot retrieve Telegram bot information: %v", r.accountName, err)
		r.tomb.Killf("cannot retrieve bot information: %v", err)
		return nil
	}

	var lastUpdateId int64

	for r.tomb.Alive() {
		params := url.Values{
			"offset":  []string{strconv.FormatInt(lastUpdateId+1, 10)},
			"timeout": []string{"3"},
		}
		resp, err := httpClient.PostForm(r.apiPrefix+r.apiKey+"/getUpdates", params)
		if err != nil {
			r.tomb.Kill(err)
			break
		}

		decoder := json.NewDecoder(resp.Body)

		var update tgUpdate
		err = decoder.Decode(&update)
		resp.Body.Close()
		if err != nil {
			r.tomb.Kill(err)
			break
		}
		if err = update.err(); err != nil {
			r.tomb.Killf("on getUpdates: %v", err)
			break
		}

		for _, result := range update.Result {
			lastUpdateId = result.UpdateId
			from := result.Message.From
			chat := result.Message.Chat
			channelPrefix := '#'
			channelTitle := chat.Title
			if chat.Username != "" {
				channelPrefix = '@'
				channelTitle = chat.Username
			} else {
				buf := make([]byte, 0, len(channelTitle))
				for _, r := range chat.Title {
					if unicode.IsLetter(r) || unicode.IsNumber(r) {
						buf = append(buf, string(r)...)
					} else {
						buf = append(buf, '_')
					}
				}
				channelTitle = string(buf)
			}
			line := fmt.Sprintf(":%s!~user@telegram PRIVMSG %c%s:%d :%s", from.Username, channelPrefix, channelTitle, chat.Id, result.Message.Text)
			logf("[%s] Received: %s", r.accountName, line)
			msg := ParseIncoming(r.accountName, r.activeNick, "/", line)
			select {
			case r.Incoming <- msg:
			case <-r.Dying:
			}
		}
	}
	return nil
}
