package mup

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"launchpad.net/tomb"
	"net"
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
)

type serverInfo struct {
	Name        string
	Host        string
	TLS         bool
	TLSInsecure bool
	Nick        string
	Password    string
	Channels    []channelInfo
}

type channelInfo struct {
	Name string
	Key  string
}

type ircServer struct {
	info serverInfo
	conn net.Conn
	tomb tomb.Tomb
	ircR *ircReader
	ircW *ircWriter

	activeNick     string
	activeChannels []string

	requests chan interface{}

	Name  string
	Dying <-chan struct{}
	R     chan *Message
	W     chan *Message
}

func startIrcServer(info *serverInfo, r chan *Message) *ircServer {
	s := &ircServer{
		info:     *info,
		requests: make(chan interface{}, 1),
		Name:     info.Name,
		W:        make(chan *Message),
		R:        r,
	}
	s.Dying = s.tomb.Dying()
	go s.loop()
	return s
}

func (s *ircServer) Err() error {
	return s.tomb.Err()
}

func (s *ircServer) Stop() error {
	s.tomb.Kill(nil)
	return s.tomb.Wait()
}

type ireqUpdateInfo *serverInfo

func (s *ircServer) UpdateInfo(info *serverInfo) {
	infoCopy := *info
	s.requests <- ireqUpdateInfo(&infoCopy)
}

func (s *ircServer) loop() {
	for s.tomb.Err() == tomb.ErrStillAlive {
		s.cleanup()

		err := s.connect()
		if err != nil {
			logf("[%s] Failed to connect to IRC server: %s", s.Name, err)
			continue
		}

		err = s.auth()
		if err != nil {
			logf("[%s] Failed to authenticate on IRC server: %s", s.Name, err)
			continue
		}

		err = s.forward()
		if err != nil {
			logf("[%s] Error communicating with IRC server: %s", s.Name, err)
		}
	}
	s.cleanup()
	logf("[%s] Server loop terminated (%v)", s.Name, s.tomb.Err())
	s.tomb.Done()
}

func (s *ircServer) cleanup() {
	logf("[%s] Cleaning IRC connection resources", s.Name)
	if s.ircW != nil {
		err := s.ircW.Stop()
		if err != nil {
			logf("[%s] IRC writer failure: %s", s.Name, err)
		}
	}
	if s.conn != nil {
		err := s.conn.Close()
		if err != nil {
			logf("[%s] Failure closing IRC server connection: %s", s.Name, err)
		}
		s.conn = nil
	}
	if s.ircR != nil {
		err := s.ircR.Stop()
		if err != nil {
			logf("[%s] IRC reader failure: %s", s.Name, err)
		}
	}
}

func (s *ircServer) connect() (err error) {
	logf("[%s] Connecting with nick %q to IRC server %q (tls=%v)", s.Name, s.info.Nick, s.info.Host, s.info.TLS)
	if s.info.TLS {
		var config tls.Config
		if s.info.TLSInsecure {
			config.InsecureSkipVerify = true
		}
		s.conn, err = tls.Dial("tcp", s.info.Host, &config)
	} else {
		s.conn, err = net.DialTimeout("tcp", s.info.Host, 10 * time.Second)
	}
	if err != nil {
		s.conn = nil
		return err
	}
	logf("[%s] Connected to %q", s.Name, s.info.Host)

	s.ircR = startIrcReader(s.Name, s.conn)
	s.ircW = startIrcWriter(s.Name, s.conn)
	return nil
}

func (s *ircServer) auth() (err error) {
	if s.info.Password != "" {
		err = s.ircW.Sendf("PASS %s", s.info.Password)
		if err != nil {
			return err
		}
	}
	err = s.ircW.Sendf("NICK %s", s.info.Nick)
	if err != nil {
		return err
	}
	err = s.ircW.Sendf("USER mup 0 0 :Mup Pet")
	if err != nil {
		return err
	}
	nick := s.info.Nick
	for {
		var msg *Message
		select {
		case msg = <-s.ircR.R:
		case <-s.Dying:
			return s.Err()
		case <-s.ircR.Dying:
			return s.ircR.Err()
		case <-s.ircW.Dying:
			return s.ircW.Err()
		}

		if msg.Cmd == cmdNickInUse {
			logf("[%s] Nick %q is in use. Trying with %q.", s.Name, nick, nick+"_")
			nick += "_"
			err = s.ircW.Sendf("NICK %s", nick)
			if err != nil {
				return err
			}
			continue
		}
		if msg.Cmd == cmdPing {
			err = s.ircW.Sendf("PONG :%s", msg.Text)
			if err != nil {
				return err
			}
			continue
		}
		if msg.Cmd == cmdWelcome {
			s.activeNick = msg.MupNick
			logf("[%s] Got welcome notice.", s.Name)
			break
		}
	}
	return nil
}

func (s *ircServer) forward() error {
	var rMsg *Message
	var wMsg *Message
	var rIn, wIn <-chan *Message
	var rOut, wOut chan<- *Message

	rIn = s.ircR.R
	wIn = s.W

	if err := s.handleUpdateInfo(&s.info); err != nil {
		return err
	}

	//pinger := time.NewTicker(10 * time.Second)

	for {
		select {
		case rMsg = <-rIn:
			skip, err := s.handleMessage(rMsg)
			if err != nil {
				return err
			}
			if skip {
				rMsg = nil
				continue
			}
			rMsg.Server = s.Name
			rOut = s.R
			rIn = nil

		case rOut <- rMsg:
			rMsg = nil
			rOut = nil
			rIn = s.ircR.R

		case wMsg = <-wIn:
			logf("[%s] Got outgoing message for delivery: %s", s.Name, wMsg)
			wOut = s.ircW.W
			wIn = nil

		case wOut <- wMsg:
			logf("[%s] Delivered outgoing message: %s", s.Name, wMsg)
			wMsg = nil
			wOut = nil
			wIn = s.W

		case req := <-s.requests:
			switch r := req.(type) {
			case ireqUpdateInfo:
				err := s.handleUpdateInfo(r)
				if err != nil {
					return err
				}
			}

		case <-s.Dying:
			return s.Err()
		case <-s.ircR.Dying:
			return s.ircR.Err()
		case <-s.ircW.Dying:
			return s.ircW.Err()
		}
	}
	panic("unreachable")
}

func (s *ircServer) handleMessage(msg *Message) (skip bool, err error) {
	switch msg.Cmd {
	case cmdNick:
		s.activeNick = msg.MupNick
	case cmdPing:
		err := s.ircW.Sendf("PONG :%s", msg.Text)
		if err != nil {
			return false, err
		}
		return true, nil
	case cmdJoin:
		if msg.Nick == s.activeNick && len(msg.Params) > 0 {
			name := strings.TrimLeft(msg.Params[0], ":")
			s.activeChannels = append(s.activeChannels, name)
			logf("[%s] Joined channel %q.", s.Name, name)
		}
	case cmdPart:
		if msg.Nick == s.activeNick && len(msg.Params) > 0 {
			name := strings.TrimLeft(msg.Params[0], ":")
			for i, iname := range s.activeChannels {
				if iname == name {
					copy(s.activeChannels[i:], s.activeChannels[i+1:])
					s.activeChannels = s.activeChannels[:len(s.activeChannels)-1]
				}
			}
			logf("[%s] Left channel %q.", s.Name, name)
		}
	}
	return false, nil
}

func (s *ircServer) handleUpdateInfo(info *serverInfo) error {
	var joins []string
	var parts []string
Outer1:
	for _, ci := range s.activeChannels {
		for _, cj := range info.Channels {
			if ci == cj.Name {
				continue Outer1
			}
		}
		parts = append(parts, ci)
	}
Outer2:
	for _, ci := range info.Channels {
		for _, cj := range s.activeChannels {
			if ci.Name == cj {
				continue Outer2
			}
		}
		joins = append(joins, ci.Name)
	}
	s.info = *info
	if len(joins) > 0 {
		// TODO Handle channel keys.
		err := s.ircW.Sendf("JOIN %s", strings.Join(joins, ","))
		if err != nil {
			return err
		}
	}
	if len(parts) > 0 {
		err := s.ircW.Sendf("PART %s", strings.Join(parts, ","))
		if err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// ircWriter

// An ircWriter reads messages from the W channel and sends it to the server.
type ircWriter struct {
	name  string
	W     chan *Message
	buf   *bufio.Writer
	tomb  tomb.Tomb
	Dying <-chan struct{}
}

func startIrcWriter(name string, conn net.Conn) *ircWriter {
	w := &ircWriter{
		name: name,
		W:    make(chan *Message, 1),
		buf:  bufio.NewWriter(conn),
	}
	w.Dying = w.tomb.Dying()
	go w.loop()
	return w
}

func (w *ircWriter) Err() error {
	return w.tomb.Err()
}

func (w *ircWriter) Stop() error {
	debugf("[%s] Requesting writer to stop...", w.name)
	w.tomb.Kill(nil)
	err := w.tomb.Wait()
	debugf("[%s] Writer is stopped (%v).", w.name, err)
	return err
}

func (w *ircWriter) Send(msg *Message) error {
	select {
	case w.W <- msg:
	case <-w.Dying:
		return w.Err()
	}
	return nil
}

func (w *ircWriter) Sendf(format string, args ...interface{}) error {
	return w.Send(ParseMessage("", "", fmt.Sprintf(format, args...)))
}

func (w *ircWriter) loop() {
loop:
	for {
		var msg *Message
		select {
		case msg = <-w.W:
		case <-w.Dying:
			break loop
		}
		line := msg.String()
		debugf("[%s] Sending: %s", w.name, line)
		_, err := w.buf.WriteString(line)
		if err != nil {
			w.tomb.Kill(err)
			break
		}
		_, err = w.buf.WriteString("\r\n")
		if err != nil {
			w.tomb.Kill(err)
			break
		}
		err = w.buf.Flush()
		if err != nil {
			w.tomb.Kill(err)
			break
		}
	}
	w.tomb.Done()
	debugf("[%s] Writer is dead (%v)", w.name, w.tomb.Err())
}

// ---------------------------------------------------------------------------
// ircReader

// An ircReader reads lines from the server and injects it in the R channel.
type ircReader struct {
	name       string
	R          chan *Message
	activeNick string
	buf        *bufio.Reader
	tomb       tomb.Tomb
	Dying      <-chan struct{}
}

func startIrcReader(name string, conn net.Conn) *ircReader {
	r := &ircReader{
		name: name,
		R:    make(chan *Message, 1),
		buf:  bufio.NewReader(conn),
	}
	r.Dying = r.tomb.Dying()
	go r.loop()
	return r
}

func (r *ircReader) Err() error {
	return r.tomb.Err()
}

func (r *ircReader) Stop() error {
	debugf("[%s] Requesting reader to stop...", r.name)
	r.tomb.Kill(nil)
	err := r.tomb.Wait()
	debugf("[%s] Reader is stopped (%v).", r.name, err)
	return err
}

func (r *ircReader) loop() {
	for r.tomb.Err() == tomb.ErrStillAlive {
		line, prefix, err := r.buf.ReadLine()
		if err != nil {
			if e, ok := err.(net.Error); ok && e.Timeout() {
				if len(line) > 0 {
					panic("FIXME: timeout with line")
				}
				continue
			}
			r.tomb.Kill(err)
			break
		}
		if prefix {
			r.tomb.Killf("line is too long")
			break
		}
		debugf("[%s] Received: %s", r.name, line)
		msg := ParseMessage(r.activeNick, "!", string(line))
		switch msg.Cmd {
		case cmdNick:
			if r.activeNick == "" || r.activeNick == msg.Nick {
				r.activeNick = msg.Text
				msg.MupNick = r.activeNick
				logf("[%s] Nick %q accepted.", r.name, r.activeNick)
			}
		case cmdWelcome:
			if len(msg.Params) > 0 {
				r.activeNick = msg.Params[0]
				msg.MupNick = r.activeNick
				logf("[%s] Nick %q accepted.", r.name, r.activeNick)
			}
		}
		select {
		case r.R <- msg:
		case <-r.Dying:
		}
	}
	r.tomb.Done()
	debugf("[%s] Reader is dead (%#v)", r.name, r.tomb.Err())
}
