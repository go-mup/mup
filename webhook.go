package mup

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"gopkg.in/mgo.v2/bson"
	"gopkg.in/tomb.v2"
)

type webhookClient struct {
	accountName string

	dying    <-chan struct{}
	info     accountInfo
	tomb     tomb.Tomb
	webhookR *webhookReader
	webhookW *webhookWriter

	requests chan interface{}

	incoming chan *Message
	outgoing chan *Message
	lastId   bson.ObjectId
}

func (c *webhookClient) AccountName() string     { return c.accountName }
func (c *webhookClient) Dying() <-chan struct{}  { return c.dying }
func (c *webhookClient) Outgoing() chan *Message { return c.outgoing }
func (c *webhookClient) LastId() bson.ObjectId   { return c.lastId }

func startWebHookClient(info *accountInfo, incoming chan *Message) accountClient {
	c := &webhookClient{
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

func (c *webhookClient) Alive() bool {
	return c.tomb.Alive()
}

func (c *webhookClient) Stop() error {
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
func (c *webhookClient) UpdateInfo(info *accountInfo) {
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

func (c *webhookClient) die() {
	logf("[%s] Cleaning WebHook connection resources", c.accountName)

	if c.webhookW != nil {
		err := c.webhookW.Stop()
		if err != nil {
			logf("[%s] WebHook writer failure: %s", c.accountName, err)
		}
	}
	if c.webhookR != nil {
		err := c.webhookR.Stop()
		if err != nil {
			logf("[%s] WebHook reader failure: %s", c.accountName, err)
		}
	}

	c.tomb.Kill(nil)
	logf("[%s] WebHook client terminated (%v)", c.accountName, c.tomb.Err())
}

func (c *webhookClient) run() error {
	defer c.die()

	if strings.Contains(c.info.Host, "://") {
		return fmt.Errorf("host account setting must not contain ://, use endpoint instead")
	}
	if c.info.Endpoint != "" && (c.info.TLS || c.info.Host != "") {
		return fmt.Errorf("webhook accounts take either endpoint or host/tls settings, not both")
	}

	endpoint := c.info.Endpoint
	if endpoint == "" {
		scheme := "http://"
		if c.info.TLS {
			scheme = "https://"
		}
		endpoint = scheme + c.info.Host
	}

	c.webhookR = startWebHookReader(c.accountName, endpoint)
	c.webhookW = startWebHookWriter(c.accountName, endpoint, c.webhookR)

	var inMsg, outMsg *Message
	var inRecv, outRecv <-chan *Message
	var inSend, outSend chan<- *Message

	inRecv = c.webhookR.Incoming
	outRecv = c.outgoing

	quitting := false
	for {
		select {
		case inMsg = <-inRecv:
			inRecv = nil
			inSend = c.incoming

		case inSend <- inMsg:
			inMsg = nil
			inRecv = c.webhookR.Incoming
			inSend = nil

		case outMsg = <-outRecv:
			if outMsg.Command == cmdQuit {
				quitting = true
			}
			outRecv = nil
			outSend = c.webhookW.Outgoing

		case outSend <- outMsg:
			outMsg = nil
			outRecv = c.outgoing
			outSend = nil

		case req := <-c.requests:
			switch r := req.(type) {
			case ireqUpdateInfo:
				// TODO Restart if endpoint changes.
				c.info = *r
			}

		case <-c.dying:
			return c.tomb.Err()
		case <-c.webhookR.Dying:
			if quitting {
				return errStop
			}
			return c.webhookR.Err()
		case <-c.webhookW.Dying:
			if quitting {
				return errStop
			}
			return c.webhookW.Err()
		}
	}
	panic("unreachable")
}

// ---------------------------------------------------------------------------
// webhookWriter

// An webhookWriter reads messages from the Outgoing channel and sends it to the server.
type webhookWriter struct {
	accountName string
	apiEndpoint string
	r           *webhookReader
	tomb        tomb.Tomb

	Dying    <-chan struct{}
	Outgoing chan *Message
}

func startWebHookWriter(accountName, apiEndpoint string, r *webhookReader) *webhookWriter {
	w := &webhookWriter{
		accountName: accountName,
		apiEndpoint: apiEndpoint,
		r:           r,
		Outgoing:    make(chan *Message, 1),
	}
	w.Dying = w.tomb.Dying()
	w.tomb.Go(w.loop)
	return w
}

func (w *webhookWriter) Err() error {
	return w.tomb.Err()
}

func (w *webhookWriter) Stop() error {
	debugf("[%s] Requesting writer to stop...", w.accountName)
	w.tomb.Kill(errStop)
	err := w.tomb.Wait()
	if err != errStop {
		return err
	}
	return nil
}

func (w *webhookWriter) Send(msg *Message) error {
	select {
	case w.Outgoing <- msg:
	case <-w.Dying:
		return w.Err()
	}
	return nil
}

func (w *webhookWriter) Sendf(format string, args ...interface{}) error {
	return w.Send(ParseOutgoing(w.accountName, fmt.Sprintf(format, args...)))
}

func (w *webhookWriter) die() {
	debugf("[%s] Writer is dead (%v)", w.accountName, w.tomb.Err())
}

type webhookPayload struct {
	Channel string `json:"channel"`
	Text    string `json:"text"`
}

func (w *webhookWriter) loop() error {
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

		payload := webhookPayload{
			Channel: msg.Channel,
			Text:    msg.Text,
		}
		if payload.Channel == "" {
			payload.Channel = "@" + msg.Nick
		}

		data, err := json.Marshal(&payload)
		if err != nil {
			w.tomb.Killf("cannot marshal outgoing json payload: %v", err)
			break
		}

		params := url.Values{
			"payload": []string{string(data)},
		}
		resp, err := httpClient.PostForm(w.apiEndpoint, params)
		if err != nil {
			w.tomb.Kill(err)
			break
		}
		decoder := json.NewDecoder(resp.Body)

		var result webhookResultStatus
		err = decoder.Decode(&result)
		resp.Body.Close()
		if err != nil {
			w.tomb.Kill(err)
			break
		}
		if err = result.err(); err != nil {
			w.tomb.Kill(err)
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

type webhookResultStatus struct {
	Success   bool   `json:"success"`
	ErrorCode string `json:"error"`
}

func (result *webhookResultStatus) err() error {
	if result.Success {
		return nil
	}
	if result.ErrorCode == "" {
		return fmt.Errorf("server reported failure without error code")
	}
	return fmt.Errorf("server returned %s", result.ErrorCode)
}

// ---------------------------------------------------------------------------
// webhookReader

// An webhookReader reads lines from the server and injects it in the Incoming channel.
type webhookReader struct {
	accountName string
	apiEndpoint string
	tomb        tomb.Tomb

	Dying    <-chan struct{}
	Incoming chan *Message
}

func startWebHookReader(accountName, apiEndpoint string) *webhookReader {
	r := &webhookReader{
		accountName: accountName,
		apiEndpoint: apiEndpoint,
		Incoming:    make(chan *Message, 1),
	}
	r.Dying = r.tomb.Dying()
	r.tomb.Go(r.loop)
	return r
}

func (r *webhookReader) Err() error {
	return r.tomb.Err()
}

func (r *webhookReader) Stop() error {
	debugf("[%s] Requesting WebHook reader to stop...", r.accountName)
	r.tomb.Kill(errStop)
	err := r.tomb.Wait()
	if err != errStop {
		return err
	}
	return nil
}

func (r *webhookReader) die() {
	debugf("[%s] Reader is dead (%v)", r.accountName, r.tomb.Err())
}

func (r *webhookReader) loop() error {
	defer r.die()

	// Receiving messages from WebHook is done via the webhook plugin.
	// Many webhooks can receive into this same account.
	<-r.tomb.Dying()

	return nil
}
