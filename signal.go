package mup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"gopkg.in/tomb.v2"
	"io"
	"os/exec"
	"sync"
)

type signalClient struct {
	accountName string

	dying   <-chan struct{}
	info    accountInfo
	tomb    tomb.Tomb
	signalR *signalReader
	signalW *signalWriter

	cliMutex sync.Mutex

	requests chan interface{}

	incoming chan *Message
	outgoing chan *Message
}

func (c *signalClient) AccountName() string     { return c.accountName }
func (c *signalClient) Dying() <-chan struct{}  { return c.dying }
func (c *signalClient) Outgoing() chan *Message { return c.outgoing }
func (c *signalClient) LastId() int64           { return c.info.LastId }

func startSignalClient(info *accountInfo, incoming chan *Message) accountClient {
	c := &signalClient{
		accountName: info.Name,

		info:     *info,
		requests: make(chan interface{}, 1),
		incoming: incoming,
		outgoing: make(chan *Message),
	}
	c.dying = c.tomb.Dying()
	c.tomb.Go(c.run)
	return c
}

func (c *signalClient) Alive() bool {
	return c.tomb.Alive()
}

func (c *signalClient) Stop() error {
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
func (c *signalClient) UpdateInfo(info *accountInfo) {
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

func (c *signalClient) die() {
	logf("[%s] Cleaning Signal connection resources", c.accountName)

	if c.signalW != nil {
		err := c.signalW.Stop()
		if err != nil {
			logf("[%s] Signal writer failure: %s", c.accountName, err)
		}
	}
	if c.signalR != nil {
		err := c.signalR.Stop()
		if err != nil {
			logf("[%s] Signal reader failure: %s", c.accountName, err)
		}
	}

	c.tomb.Kill(nil)
	logf("[%s] Signal client terminated (%v)", c.accountName, c.tomb.Err())
}

func (c *signalClient) run() error {
	defer c.die()

	if c.info.Identity == "" || c.info.Identity[0] != '+' {
		c.tomb.Killf("account identity is not a phone number: %q", c.info.Identity)
		return nil
	}

	c.signalR = startSignalReader(&c.cliMutex, c.accountName, c.info.Identity, c.info.Nick)
	c.signalW = startSignalWriter(&c.cliMutex, c.accountName, c.info.Identity, c.signalR)

	var inMsg, outMsg *Message
	var inRecv, outRecv <-chan *Message
	var inSend, outSend chan<- *Message

	inRecv = c.signalR.Incoming
	outRecv = c.outgoing

	quitting := false
	for {
		select {
		case inMsg = <-inRecv:
			inRecv = nil
			inSend = c.incoming

		case inSend <- inMsg:
			inMsg = nil
			inRecv = c.signalR.Incoming
			inSend = nil

		case outMsg = <-outRecv:
			if outMsg.Command == cmdQuit {
				quitting = true
			}
			outRecv = nil
			outSend = c.signalW.Outgoing

		case outSend <- outMsg:
			outMsg = nil
			outRecv = c.outgoing
			outSend = nil

		case req := <-c.requests:
			switch r := req.(type) {
			case ireqUpdateInfo:
				c.info = *r
			}

		case <-c.dying:
			return c.tomb.Err()
		case <-c.signalR.Dying:
			if quitting {
				return errStop
			}
			return c.signalR.Err()
		case <-c.signalW.Dying:
			if quitting {
				return errStop
			}
			return c.signalW.Err()
		}
	}
	panic("unreachable")
}

// ---------------------------------------------------------------------------
// signalWriter

// An signalWriter reads messages from the Outgoing channel and sends it to the server.
type signalWriter struct {
	cliMutex *sync.Mutex

	accountName string
	identity    string
	r           *signalReader
	tomb        tomb.Tomb

	Dying    <-chan struct{}
	Outgoing chan *Message
}

func startSignalWriter(cliMutex *sync.Mutex, accountName, identity string, r *signalReader) *signalWriter {
	w := &signalWriter{
		cliMutex:    cliMutex,
		accountName: accountName,
		identity:    identity,
		r:           r,
		Outgoing:    make(chan *Message, 1),
	}
	w.Dying = w.tomb.Dying()
	w.tomb.Go(w.loop)
	return w
}

func (w *signalWriter) Err() error {
	return w.tomb.Err()
}

func (w *signalWriter) Stop() error {
	debugf("[%s] Requesting writer to stop...", w.accountName)
	w.tomb.Kill(errStop)
	err := w.tomb.Wait()
	if err != errStop {
		return err
	}
	return nil
}

func (w *signalWriter) Send(msg *Message) error {
	select {
	case w.Outgoing <- msg:
	case <-w.Dying:
		return w.Err()
	}
	return nil
}

func (w *signalWriter) Sendf(format string, args ...interface{}) error {
	return w.Send(ParseOutgoing(w.accountName, fmt.Sprintf(format, args...)))
}

func (w *signalWriter) die() {
	debugf("[%s] Writer is dead (%v)", w.accountName, w.tomb.Err())
}

func outputErr(output []byte, err error) error {
	output = bytes.TrimSpace(output)
	if len(output) > 0 {
		if bytes.Contains(output, []byte{'\n'}) {
			err = fmt.Errorf("\n-----\n%s\n-----", output)
		} else {
			err = fmt.Errorf("%s", output)
		}
	}
	return err
}

func (w *signalWriter) loop() error {
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

		recipient := msg.Channel
		if recipient != "" && recipient[0] == '@' {
			recipient = recipient[1:]
		} else if recipient != "" && recipient[0] == '#' {
			recipient = recipient[1:]
		}

		var cmd *exec.Cmd
		if recipient[0] == '+' {
			cmd = exec.Command("signal-cli", "-u", w.identity, "send", recipient)
		} else {
			cmd = exec.Command("signal-cli", "-u", w.identity, "send", "-g", recipient)
		}
		cmd.Stdin = bytes.NewBufferString(msg.Text)

		// TODO Kill command if it hangs.
		w.cliMutex.Lock()
		output, err := cmd.CombinedOutput()
		w.cliMutex.Unlock()
		if err != nil {
			w.tomb.Killf("cannot run signal-cli command for sending: %v", outputErr(output, err))
			break
		}

		// Notify the account manager that the message was delivered.
		select {
		case w.r.Incoming <- ParseIncoming(w.accountName, "mup", "/", "PONG :sent:"+strconv.FormatInt(msg.Id, 16)):
		case <-w.Dying:
		case <-w.r.Dying:
			break
		}
	}

	return nil
}

type signalResultStatus struct {
	Ok          bool   `json:"ok"`
	ErrorCode   int    `json:"error_code"`
	Description string `json:"description"`
}

func (result *signalResultStatus) err() error {
	if result.Ok {
		return nil
	}
	if result.Description == "" {
		result.Description = "no error description"
	}
	return fmt.Errorf("error code %d - %s", result.ErrorCode, result.Description)
}

// ---------------------------------------------------------------------------
// signalReader

// An signalReader reads lines from the server and injects it in the Incoming channel.
type signalReader struct {
	cliMutex *sync.Mutex

	accountName string
	identity    string
	activeNick  string
	tomb        tomb.Tomb

	Dying    <-chan struct{}
	Incoming chan *Message
}

func startSignalReader(cliMutex *sync.Mutex, accountName, identity, nick string) *signalReader {
	r := &signalReader{
		cliMutex:    cliMutex,
		accountName: accountName,
		identity:    identity,
		activeNick:  nick,
		Incoming:    make(chan *Message, 1),
	}
	r.Dying = r.tomb.Dying()
	r.tomb.Go(r.loop)
	return r
}

func (r *signalReader) Err() error {
	return r.tomb.Err()
}

func (r *signalReader) Stop() error {
	debugf("[%s] Requesting Signal reader to stop...", r.accountName)
	r.tomb.Kill(errStop)
	err := r.tomb.Wait()
	if err != errStop {
		return err
	}
	return nil
}

func (r *signalReader) die() {
	debugf("[%s] Reader is dead (%v)", r.accountName, r.tomb.Err())
}

type signalUpdate struct {
	//signalResultStatus
	Envelope signalEnvelope `json:"envelope"`
}

type signalEnvelope struct {
	Source       string            `json:"source"`
	SourceDevice int               `json:"sourceDevice"`
	Timestamp    int64             `json:"timestamp"`
	IsReceipt    bool              `json:"isReceipt"`
	DataMessage  signalMessage     `json:"dataMessage"`
	SyncMessage  signalSyncMessage `json:"syncMessage"`
	// syncMessage
	// callMessage
	// receiptMessage
	// relay
}

type signalSyncMessage struct {
	SentMessage signalMessage `json:sentMessage`
	// blockedNumbers
	// readMessages
	// type
}

type signalMessage struct {
	Timestamp   int64           `json:"timestamp"`
	Message     string          `json:"message"`
	Destination string          `json:"destination"`
	GroupInfo   signalGroupInfo `json:"groupInfo"`
	// attachments
	// expiresInSeconds
}

type signalGroupInfo struct {
	GroupID string `json:"groupId"`
	// members
	// name
	// type
}

func (r *signalReader) loop() error {
	defer r.die()

	var err error
	var cmd *exec.Cmd
	var out io.ReadCloser
	cleanup := func() {
		if out != nil {
			out.Close()
			out = nil
		}
		if cmd != nil {
			if cmd.Process != nil && cmd.ProcessState == nil {
				cmd.Process.Kill()
				cmd.Wait()
			}
			cmd = nil
		}
	}
	defer cleanup()

	for r.tomb.Alive() {
		// TODO There should be a way to retry reading. Right now if a
		// crash happens between the signal-cli call and the messages
		// being stored in the mup database, these messages are lost.

		// This way we don't need to worry about cleanin up on every breakpoint.
		cleanup()

		cmd = exec.Command("signal-cli", "-u", r.identity, "receive", "--json", "--ignore-attachments")
		out, err = cmd.StdoutPipe()
		if err != nil {
			logf("[%s] Cannot open signal-cli output pipe: %v", r.accountName, err)
			continue
		}

		r.cliMutex.Lock()
		err := cmd.Start()
		if err != nil {
			r.cliMutex.Unlock()
			logf("[%s] Cannot start signal-cli command for receiving: %v", r.accountName, err)
			continue
		}
		decoder := json.NewDecoder(out)
		for {
			var update signalUpdate
			var data json.RawMessage
			err = decoder.Decode(&data)
			if err == io.EOF {
				break
			}
			if err == nil {
				err = json.Unmarshal(data, &update)
			}
			if err != nil {
				// Something unusual must be wrong.
				r.tomb.Killf("cannot decode signal-cli payload: %v", err)
				break
			}

			envelope := update.Envelope
			source := envelope.Source
			if source == "" {
				source = "system"
			}

			message := envelope.DataMessage
			if message.Timestamp == 0 && envelope.SyncMessage.SentMessage.Timestamp > 0 {
				message = envelope.SyncMessage.SentMessage
			}

			text := message.Message
			group := message.GroupInfo.GroupID

			var channel string
			if group != "" {
				channel = "#" + group
			} else {
				channel = "@" + source
			}

			var msgs []*Message

			line := fmt.Sprintf(":%s!~user@signal SIGNALDATA :%s", source, data)
			logf("[%s] Received: %s", r.accountName, line)
			msgs = append(msgs, ParseIncoming(r.accountName, r.activeNick, "/", line))

			if text != "" {
				line = fmt.Sprintf(":%s!~user@signal PRIVMSG %s :%s", source, channel, text)
				logf("[%s] Received: %s", r.accountName, line)
				msgs = append(msgs, ParseIncoming(r.accountName, r.activeNick, "/", line))
			}

			for _, msg := range msgs {
				timestamp := message.Timestamp
				if timestamp == 0 {
					timestamp = envelope.Timestamp
				}
				msg.Time = time.Unix(0, timestamp*1e6)

				select {
				case r.Incoming <- msg:
				case <-r.Dying:
				}
			}
		}
		r.cliMutex.Unlock()

		time.Sleep(100 * time.Millisecond)
	}
	return nil
}
