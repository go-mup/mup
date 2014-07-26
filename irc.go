package mup

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"gopkg.in/tomb.v2"
	"gopkg.in/mgo.v2/bson"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	cmdWelcome   = "001"
	cmdNickInUse = "433"
	cmdPrivMsg   = "PRIVMSG"
	cmdNotice    = "NOTICE"
	cmdNick      = "NICK"
	cmdPing      = "PING"
	cmdPong      = "PONG"
	cmdJoin      = "JOIN"
	cmdPart      = "PART"
	cmdQuit      = "QUIT"
)

type accountInfo struct {
	Name        string `bson:"_id"`
	Host        string
	TLS         bool
	TLSInsecure bool
	Nick        string
	Password    string
	Channels    []channelInfo
	LastId      bson.ObjectId
}

type channelInfo struct {
	Name string
	Key  string
}

// NetworkTimeout's value is used as a timeout in a number of network-related activities.
// Plugins are encouraged to use that same value internally for consistent behavior.
var NetworkTimeout = 15 * time.Second

const nickChangeDelay = 30 * time.Second

type ircClient struct {
	info accountInfo
	conn net.Conn
	tomb tomb.Tomb
	ircR *ircReader
	ircW *ircWriter

	activeChannels []string
	activeNick     string
	nextNickChange time.Time

	requests chan interface{}
	stopAuth chan bool

	Account  string
	Dying    <-chan struct{}
	Incoming chan *Message
	Outgoing chan *Message
	LastId   bson.ObjectId
}

func startIrcClient(info *accountInfo, incoming chan *Message) *ircClient {
	c := &ircClient{
		info:     *info,
		requests: make(chan interface{}, 1),
		stopAuth: make(chan bool),
		Account:  info.Name,
		Incoming: incoming,
		Outgoing: make(chan *Message),
	}
	c.LastId = c.info.LastId
	c.Dying = c.tomb.Dying()
	c.tomb.Go(c.loop)
	return c
}

func (c *ircClient) Err() error {
	return c.tomb.Err()
}

func (c *ircClient) Stop() error {
	// Try to disconnect gracefully.
	timeout := time.After(NetworkTimeout)
	select {
	case c.Outgoing <- &Message{Command: cmdQuit, Params: []string{":brb"}}:
		select {
		case <-c.tomb.Dying():
		case <-timeout:
		}
	case c.stopAuth <- true:
	case <-c.tomb.Dying():
	case <-timeout:
	}
	c.tomb.Kill(errStop)
	err := c.tomb.Wait()
	if err != errStop {
		return err
	}
	return nil
}

type ireqUpdateInfo *accountInfo

// UpdateInfo updates the account information. Everything but
// the account name may be updated.
func (c *ircClient) UpdateInfo(info *accountInfo) {
	if info.Name != c.Account {
		panic("cannot change the account name")
	}
	// Make a copy as its use will continue after returning to the caller.
	infoCopy := *info
	select {
	case c.requests <- ireqUpdateInfo(&infoCopy):
	case <-c.tomb.Dying():
	}
}

func (c *ircClient) loop() error {
	defer c.die()

	err := c.connect()
	if err != nil {
		logf("[%s] While connecting to IRC server: %v", c.Account, err)
		c.tomb.Killf("%s: cannot connect to IRC server: %v", c.Account, err)
		return nil
	}

	err = c.auth()
	if err != nil {
		logf("[%s] While authenticating on IRC server: %v", c.Account, err)
		c.tomb.Killf("%s: cannot authenticate on IRC server: %v", c.Account, err)
		return nil
	}

	err = c.forward()
	if err != nil {
		logf("[%s] While talking to IRC server: %v", c.Account, err)
		c.tomb.Killf("%s: while talking to IRC server: %v", c.Account, err)
		return nil
	}

	return nil
}

func (c *ircClient) die() {
	logf("[%s] Cleaning IRC connection resources", c.Account)

	// Stop the writer before closing the connection, so that
	// in progress writes are politely finished.
	if c.ircW != nil {
		err := c.ircW.Stop()
		if err != nil {
			logf("[%s] IRC writer failure: %s", c.Account, err)
		}
	}
	// Close the connection before stopping the reader, as the
	// reader is likely blocked attempting to get more data.
	if c.conn != nil {
		debugf("[%s] Closing connection", c.Account)
		err := c.conn.Close()
		if err != nil {
			logf("[%s] Failure closing IRC server connection: %s", c.Account, err)
		}
		c.conn = nil
	}
	// Finally, stop the reader.
	if c.ircR != nil {
		err := c.ircR.Stop()
		if err != nil {
			logf("[%s] IRC reader failure: %s", c.Account, err)
		}
	}

	logf("[%s] Client loop terminated (%v)", c.Account, c.tomb.Err())
}

func (c *ircClient) connect() (err error) {
	logf("[%s] Connecting with nick %q to IRC server %q (tls=%v)", c.Account, c.info.Nick, c.info.Host, c.info.TLS)
	dialer := &net.Dialer{Timeout: NetworkTimeout}
	if c.info.TLS {
		var config tls.Config
		if c.info.TLSInsecure {
			config.InsecureSkipVerify = true
		}
		c.conn, err = tls.DialWithDialer(dialer, "tcp", c.info.Host, &config)
	} else {
		c.conn, err = dialer.Dial("tcp", c.info.Host)
	}
	if err != nil {
		c.conn = nil
		return err
	}
	logf("[%s] Connected to %q", c.Account, c.info.Host)

	c.ircR = startIrcReader(c.Account, c.conn)
	c.ircW = startIrcWriter(c.Account, c.conn)
	return nil
}

func (c *ircClient) auth() (err error) {
	if c.info.Password != "" {
		err = c.ircW.Sendf("PASS %s", c.info.Password)
		if err != nil {
			return err
		}
	}
	err = c.ircW.Sendf("NICK %s", c.info.Nick)
	if err != nil {
		return err
	}
	err = c.ircW.Sendf("USER mup 0 0 :Mup Pet")
	if err != nil {
		return err
	}
	nick := c.info.Nick
	for {
		var msg *Message
		select {
		case msg = <-c.ircR.Incoming:
		case <-c.Dying:
			return c.Err()
		case <-c.ircR.Dying:
			return c.ircR.Err()
		case <-c.ircW.Dying:
			return c.ircW.Err()
		case <-c.stopAuth:
			return errStop
		}

		if msg.Command == cmdNickInUse {
			logf("[%s] Nick %q is in use. Trying with %q.", c.Account, nick, nick+"_")
			nick += "_"
			err = c.ircW.Sendf("NICK %s", nick)
			if err != nil {
				return err
			}
			continue
		}
		if msg.Command == cmdPing {
			err = c.ircW.Sendf("PONG :%s", msg.Text)
			if err != nil {
				return err
			}
			continue
		}
		if msg.Command == cmdWelcome {
			c.activeNick = msg.AsNick
			logf("[%s] Got welcome notice.", c.Account)
			break
		}
	}
	return nil
}

func (c *ircClient) forward() error {
	// Join initial channels before forwarding any outgoing messages.
	if err := c.handleUpdateInfo(&c.info); err != nil {
		return err
	}

	var inMsg, outMsg *Message
	var inRecv, outRecv <-chan *Message
	var inSend, outSend chan<- *Message

	inRecv = c.ircR.Incoming
	outRecv = c.Outgoing

	quitting := false
	for {
		select {
		case inMsg = <-inRecv:
			skip, err := c.handleMessage(inMsg)
			if err != nil {
				return err
			}
			if skip {
				inMsg = nil
				continue
			}
			inRecv = nil
			inSend = c.Incoming

		case inSend <- inMsg:
			inMsg = nil
			inRecv = c.ircR.Incoming
			inSend = nil

		case outMsg = <-outRecv:
			if outMsg.Command == cmdQuit {
				quitting = true
			}
			outRecv = nil
			outSend = c.ircW.Outgoing

		case outSend <- outMsg:
			outMsg = nil
			outRecv = c.Outgoing
			outSend = nil

		case req := <-c.requests:
			switch r := req.(type) {
			case ireqUpdateInfo:
				err := c.handleUpdateInfo(r)
				if err != nil {
					return err
				}
			}

		case <-c.Dying:
			return c.Err()
		case <-c.ircR.Dying:
			if quitting {
				return errStop
			}
			return c.ircR.Err()
		case <-c.ircW.Dying:
			if quitting {
				return errStop
			}
			return c.ircW.Err()
		}
	}
	panic("unreachable")
}

func changedChannel(msg *Message) string {
	if len(msg.Params) > 0 {
		return msg.Params[0]
	}
	if len(msg.Text) > 0 {
		return msg.Text
	}
	return ""
}

func (c *ircClient) handleMessage(msg *Message) (skip bool, err error) {
	switch msg.Command {
	case cmdNick:
		c.activeNick = msg.AsNick
	case cmdPing:
		err = c.ircW.Sendf("PONG :%s", msg.Text)
		if err != nil {
			return false, err
		}
		return true, nil
	case cmdJoin, cmdPart:
		if msg.Nick != c.activeNick {
			break
		}
		channel := changedChannel(msg)
		if channel == "" {
			break
		}
		if msg.Command == cmdJoin {
			c.activeChannels = append(c.activeChannels, channel)
			logf("[%s] Joined channel %q.", c.Account, channel)
		} else {
			for i, ichannel := range c.activeChannels {
				if ichannel == channel {
					copy(c.activeChannels[i:], c.activeChannels[i+1:])
					c.activeChannels = c.activeChannels[:len(c.activeChannels)-1]
				}
			}
			logf("[%s] Left channel %q.", c.Account, channel)
		}
	}
	return false, nil
}

func (c *ircClient) handleUpdateInfo(info *accountInfo) error {
	var joins []string
	var parts []string
Outer1:
	for _, ci := range c.activeChannels {
		for _, cj := range info.Channels {
			if ci == cj.Name {
				continue Outer1
			}
		}
		parts = append(parts, ci)
	}
Outer2:
	for _, ci := range info.Channels {
		for _, cj := range c.activeChannels {
			if ci.Name == cj {
				continue Outer2
			}
		}
		joins = append(joins, ci.Name)
	}
	c.info = *info
	if len(joins) > 0 {
		// TODO Handle channel keys.
		err := c.ircW.Sendf("JOIN %s", strings.Join(joins, ","))
		if err != nil {
			return err
		}
	}
	if len(parts) > 0 {
		err := c.ircW.Sendf("PART %s", strings.Join(parts, ","))
		if err != nil {
			return err
		}
	}
	if c.activeNick != c.info.Nick {
		now := time.Now()
		if c.nextNickChange.Before(now) {
			c.nextNickChange = now.Add(nickChangeDelay)
			err := c.ircW.Sendf("NICK %s", c.info.Nick)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// ircWriter

// An ircWriter reads messages from the Outgoing channel and sends it to the server.
type ircWriter struct {
	account string
	conn    net.Conn
	buf     *bufio.Writer
	tomb    tomb.Tomb

	Dying    <-chan struct{}
	Outgoing chan *Message
}

func startIrcWriter(name string, conn net.Conn) *ircWriter {
	w := &ircWriter{
		account:  name,
		conn:     conn,
		buf:      bufio.NewWriter(conn),
		Outgoing: make(chan *Message, 1),
	}
	w.Dying = w.tomb.Dying()
	w.tomb.Go(w.loop)
	return w
}

func (w *ircWriter) Err() error {
	return w.tomb.Err()
}

func (w *ircWriter) Stop() error {
	debugf("[%s] Requesting writer to stop...", w.account)
	w.tomb.Kill(errStop)
	err := w.tomb.Wait()
	if err != errStop {
		return err
	}
	return nil
}

func (w *ircWriter) Send(msg *Message) error {
	select {
	case w.Outgoing <- msg:
	case <-w.Dying:
		return w.Err()
	}
	return nil
}

func (w *ircWriter) Sendf(format string, args ...interface{}) error {
	return w.Send(ParseOutgoing(w.account, fmt.Sprintf(format, args...)))
}

func (w *ircWriter) die() {
	debugf("[%s] Writer is dead (%v)", w.account, w.tomb.Err())
}

func (w *ircWriter) loop() error {
	defer w.die()

	pingDelay := NetworkTimeout / 3
	pinger := time.NewTicker(pingDelay)
	defer pinger.Stop()
	lastPing := time.Now()
loop:
	for {
		var send []string
		select {
		case msg := <-w.Outgoing:
			line := msg.String()
			if msg.Command != cmdPong {
				logf("[%s] Sending: %s", w.account, line)
			}
			if (msg.Command == cmdPrivMsg || msg.Command == "") && msg.Id != "" {
				send = []string{line, "\r\nPING :sent:", msg.Id.Hex(), "\r\n"}
				lastPing = time.Now()
			} else {
				send = []string{line, "\r\n"}
			}
		case t := <-pinger.C:
			if t.Before(lastPing.Add(pingDelay)) {
				continue
			}
			lastPing = t
			send = []string{"PING :", strconv.FormatInt(t.Unix(), 10), "\r\n"}
		case <-w.Dying:
			break loop
		}
		for _, s := range send {
			_, err := w.buf.WriteString(s)
			if err != nil {
				w.tomb.Kill(err)
				break
			}
		}
		err := w.buf.Flush()
		if err != nil {
			w.tomb.Kill(err)
			break
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// ircReader

// An ircReader reads lines from the server and injects it in the Incoming channel.
type ircReader struct {
	account    string
	conn       net.Conn
	activeNick string
	buf        *bufio.Reader
	tomb       tomb.Tomb

	Dying    <-chan struct{}
	Incoming chan *Message
}

func startIrcReader(name string, conn net.Conn) *ircReader {
	r := &ircReader{
		account:  name,
		conn:     conn,
		buf:      bufio.NewReader(conn),
		Incoming: make(chan *Message, 1),
	}
	r.Dying = r.tomb.Dying()
	r.tomb.Go(r.loop)
	return r
}

func (r *ircReader) Err() error {
	return r.tomb.Err()
}

var errStop = fmt.Errorf("stop requested")

func (r *ircReader) Stop() error {
	debugf("[%s] Requesting reader to stop...", r.account)
	r.tomb.Kill(errStop)
	err := r.tomb.Wait()
	if err != errStop {
		return err
	}
	return nil
}

func (r *ircReader) die() {
	debugf("[%s] Reader is dead (%v)", r.account, r.tomb.Err())
}

func (r *ircReader) loop() error {
	defer r.die()

	for r.tomb.Alive() {
		r.conn.SetReadDeadline(time.Now().Add(NetworkTimeout))
		line, prefix, err := r.buf.ReadLine()
		if err != nil {
			r.tomb.Kill(err)
			break
		}
		if prefix {
			r.tomb.Killf("line is too long")
			break
		}
		msg := ParseIncoming(r.account, r.activeNick, "!", string(line))
		if msg.Command != cmdPong && msg.Command != cmdPing {
			logf("[%s] Received: %s", r.account, line)
		}
		switch msg.Command {
		case cmdNick:
			if r.activeNick == "" || r.activeNick == msg.Nick {
				r.activeNick = msg.Text
				msg.AsNick = r.activeNick
				logf("[%s] Nick %q accepted.", r.account, r.activeNick)
			}
		case cmdWelcome:
			if len(msg.Params) > 0 {
				r.activeNick = msg.Params[0]
				msg.AsNick = r.activeNick
				logf("[%s] Nick %q accepted.", r.account, r.activeNick)
			}
		}
		select {
		case r.Incoming <- msg:
		case <-r.Dying:
		}
	}
	return nil
}
