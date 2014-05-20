package mup

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"launchpad.net/tomb"
	"net"
	"strings"
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
	info  serverInfo
	conn  net.Conn
	tomb  tomb.Tomb
	dying <-chan struct{}
	ircR  *ircReader
	ircW  *ircWriter

	nick     string
	channels []string

	requests chan interface{}

	R chan *Message
	W chan *Message
}

func startIrcServer(info *serverInfo, r chan *Message) *ircServer {
	s := &ircServer{
		info:     *info,
		W:        make(chan *Message),
		R:        r,
		requests: make(chan interface{}, 1),
	}
	s.dying = s.tomb.Dying()
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
	s.requests <- ireqUpdateInfo(info)
}

func (s *ircServer) loop() {
	for s.tomb.Err() == tomb.ErrStillAlive {
		s.cleanup()

		err := s.connect()
		if err != nil {
			logf("Failed to connect to IRC server: %s", err)
			continue
		}

		err = s.auth()
		if err != nil {
			logf("Failed to authenticate on IRC server: %s", err)
			continue
		}

		err = s.forward()
		if err != nil {
			logf("Error communicating with IRC server: %s", err)
		}
	}
	s.cleanup()
	logf("Server loop for %q terminated (%v).", s.info.Name, s.tomb.Err())
	s.tomb.Done()
}

func (s *ircServer) cleanup() {
	log("Cleaning IRC connection resources...")
	if s.ircW != nil {
		err := s.ircW.Stop()
		if err != nil {
			logf("IRC writer failure: %s", err)
		}
	}
	if s.conn != nil {
		err := s.conn.Close()
		if err != nil {
			logf("Failure closing IRC server connection: %s", err)
		}
		s.conn = nil
	}
	if s.ircR != nil {
		err := s.ircR.Stop()
		if err != nil {
			logf("IRC reader failure: %s", err)
		}
	}
}

func (s *ircServer) connect() (err error) {
	logf("Connecting with nick %q to IRC server %s (tls=%v)...", s.info.Nick, s.info.Host, s.info.TLS)
	if s.info.TLS {
		var config tls.Config
		if s.info.TLSInsecure {
			config.InsecureSkipVerify = true
		}
		s.conn, err = tls.Dial("tcp", s.info.Host, &config)
	} else {
		s.conn, err = net.Dial("tcp", s.info.Host)
	}
	if err != nil {
		s.conn = nil
		return err
	}
	logf("Connected to %s.", s.info.Host)

	s.ircR = startIrcReader(s.conn)
	s.ircW = startIrcWriter(s.conn)
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
		case <-s.dying:
			return s.Err()
		case <-s.ircR.dying:
			return s.ircR.Err()
		case <-s.ircW.dying:
			return s.ircW.Err()
		}

		if msg.Cmd == cmdNickInUse {
			logf("Nick %q is in use. Trying with %q.", nick, nick+"_")
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
			s.nick = msg.MupNick
			logf("Got welcome notice.")
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

	//if err := s.handleUpdateInfo(s.info); err != nil {
	//	return err
	//}

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
			rOut = s.R

		case rOut <- rMsg:
			rMsg = nil
			rOut = nil

		case wMsg = <-wIn:
			wOut = s.ircW.W

		case wOut <- wMsg:
			wMsg = nil
			wOut = nil

		case req := <-s.requests:
			switch r := req.(type) {
			case ireqUpdateInfo:
				err := s.handleUpdateInfo(r)
				if err != nil {
					return err
				}
			}

		case <-s.dying:
			return s.Err()
		case <-s.ircR.dying:
			return s.ircR.Err()
		case <-s.ircW.dying:
			return s.ircW.Err()
		}
	}
	panic("unreachable")
}

func (s *ircServer) handleMessage(msg *Message) (skip bool, err error) {
	switch msg.Cmd {
	case cmdNick:
		s.nick = msg.MupNick
	case cmdPing:
		err := s.ircW.Sendf("PONG :%s", msg.Text)
		if err != nil {
			return false, err
		}
		return true, nil
	case cmdJoin:
		if msg.Nick == s.nick && len(msg.Params) > 0 {
			name := strings.TrimLeft(msg.Params[0], ":")
			s.channels = append(s.channels, name)
			logf("Joined channel %q.", name)
		}
	case cmdPart:
		if msg.Nick == s.nick && len(msg.Params) > 0 {
			name := strings.TrimLeft(msg.Params[0], ":")
			for i, iname := range s.channels {
				if iname == name {
					copy(s.channels[i:], s.channels[i+1:])
					s.channels = s.channels[:len(s.channels)-1]
				}
			}
			logf("Left channel %q.", name)
		}
	}
	return false, nil
}

func (s *ircServer) handleUpdateInfo(info *serverInfo) error {
	var joins []string
	var parts []string
Outer1:
	for _, ci := range s.channels {
		for _, cj := range info.Channels {
			if ci == cj.Name {
				continue Outer1
			}
		}
		parts = append(parts, ci)
	}
Outer2:
	for _, ci := range info.Channels {
		for _, cj := range s.channels {
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
	W     chan *Message
	buf   *bufio.Writer
	tomb  tomb.Tomb
	dying <-chan struct{}
}

func startIrcWriter(conn net.Conn) *ircWriter {
	w := &ircWriter{
		W:   make(chan *Message, 1),
		buf: bufio.NewWriter(conn),
	}
	w.dying = w.tomb.Dying()
	go w.loop()
	return w
}

func (w *ircWriter) Err() error {
	return w.tomb.Err()
}

func (w *ircWriter) Stop() error {
	debugf("Requesting writer to stop...")
	w.tomb.Kill(nil)
	err := w.tomb.Wait()
	debugf("Writer is stopped (%v).", err)
	return err
}

func (w *ircWriter) Send(msg *Message) error {
	select {
	case w.W <- msg:
	case <-w.dying:
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
		case <-w.dying:
			break loop
		}
		line := msg.String()
		debugf("Sending: %s", line)
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
	debugf("Writer is dead (%v)", w.tomb.Err())
}

// ---------------------------------------------------------------------------
// ircReader

// An ircReader reads lines from the server and injects it in the R channel.
type ircReader struct {
	R     chan *Message
	nick  string
	buf   *bufio.Reader
	tomb  tomb.Tomb
	dying <-chan struct{}
}

func startIrcReader(conn net.Conn) *ircReader {
	r := &ircReader{
		R:   make(chan *Message, 1),
		buf: bufio.NewReader(conn),
	}
	r.dying = r.tomb.Dying()
	go r.loop()
	return r
}

func (r *ircReader) Err() error {
	return r.tomb.Err()
}

func (r *ircReader) Stop() error {
	debugf("Requesting reader to stop...")
	r.tomb.Kill(nil)
	err := r.tomb.Wait()
	debugf("Reader is stopped (%v).", err)
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
		debugf("Received: %s", line)
		msg := ParseMessage(r.nick, "!", string(line))
		switch msg.Cmd {
		case cmdNick:
			if r.nick == "" || r.nick == msg.Nick {
				r.nick = msg.Text
				msg.MupNick = r.nick
				logf("Nick %q accepted.", r.nick)
			}
		case cmdWelcome:
			if len(msg.Params) > 0 {
				r.nick = msg.Params[0]
				msg.MupNick = r.nick
				logf("Nick %q accepted.", r.nick)
			}
		}
		select {
		case r.R <- msg:
		case <-r.dying:
		}
	}
	r.tomb.Done()
	debugf("Reader is dead (%v)", r.tomb.Err())
}
