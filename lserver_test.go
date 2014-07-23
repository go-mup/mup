package mup_test

import (
	"bufio"
	"fmt"
	"net"
	"sync"
	"time"

	. "gopkg.in/check.v1"
	"gopkg.in/tomb.v2"
)

type M map[string]interface{}

type LineServerSuite struct {
	Addr    *net.TCPAddr
	tomb    tomb.Tomb
	l       *net.TCPListener
	m       sync.Mutex
	active  bool
	servers []*LineServer
}

func (lsuite *LineServerSuite) SetUpSuite(c *C) {
	addr, err := net.ResolveTCPAddr("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	lsuite.l, err = net.ListenTCP("tcp", addr)
	if err != nil {
		panic(err)
	}
	lsuite.Addr = lsuite.l.Addr().(*net.TCPAddr)
	lsuite.tomb.Go(lsuite.loop)
}

func (lsuite *LineServerSuite) TearDownSuite(c *C) {
	lsuite.tomb.Kill(nil)
	lsuite.l.Close()
}

func (lsuite *LineServerSuite) SetUpTest(c *C) {
	c.Assert(lsuite.tomb.Err(), Equals, tomb.ErrStillAlive)
	lsuite.m.Lock()
	lsuite.active = true
	lsuite.m.Unlock()
}

func (lsuite *LineServerSuite) TearDownTest(c *C) {
	lsuite.m.Lock()
	lsuite.active = false
	for _, server := range lsuite.servers {
		server.Close()
	}
	lsuite.servers = nil
	lsuite.m.Unlock()
	c.Assert(lsuite.tomb.Err(), Equals, tomb.ErrStillAlive)
}

func (lsuite *LineServerSuite) loop() error {
	for lsuite.tomb.Alive() {
		conn, err := lsuite.l.Accept()
		if err != nil {
			return err
		}
		lsuite.m.Lock()
		if !lsuite.active {
			panic("LineServerSuite got connection without active tests")
		}
		lsuite.servers = append(lsuite.servers, NewLineServer(conn))
		lsuite.m.Unlock()
	}
	return nil
}

func (lsuite *LineServerSuite) CloseLineServers() {
	lsuite.m.Lock()
	for _, server := range lsuite.servers {
		server.Close()
	}
	lsuite.m.Unlock()
}

func (lsuite *LineServerSuite) NextLineServer() int {
	lsuite.m.Lock()
	n := len(lsuite.servers)
	lsuite.m.Unlock()
	return n
}

func (lsuite *LineServerSuite) LineServer(connIndex int) *LineServer {
	var server *LineServer
	for i := 0; i < 500; i++ {
		lsuite.m.Lock()
		if len(lsuite.servers) > connIndex {
			server = lsuite.servers[connIndex]
		}
		lsuite.m.Unlock()
		if server != nil {
			return server
		}
		time.Sleep(10 * time.Millisecond)
	}
	panic(fmt.Sprintf("timeout waiting for connection %d to be established", connIndex))
}

type LineServer struct {
	conn net.Conn
	tomb tomb.Tomb
	lbuf chan string
}

func NewLineServer(conn net.Conn) *LineServer {
	lserver := &LineServer{
		conn: conn,
		lbuf: make(chan string, 64),
	}
	lserver.tomb.Go(lserver.loop)
	return lserver
}

func (lserver *LineServer) loop() error {
	scanner := bufio.NewScanner(lserver.conn)
	for scanner.Scan() && lserver.tomb.Alive() {
		select {
		case lserver.lbuf <- scanner.Text():
		default:
			panic("too many lines received without being processed by test")
		}
	}
	return scanner.Err()
}

func (lserver *LineServer) Close() error {
	lserver.tomb.Kill(nil)
	lserver.conn.Close()
	return lserver.tomb.Wait()
}

func (lserver *LineServer) Err() error {
	return lserver.tomb.Err()
}

func (lserver *LineServer) ReadLine() string {
	select {
	case line := <-lserver.lbuf:
		return line
	case <-lserver.tomb.Dead():
		select {
		case line := <-lserver.lbuf:
			return line
		default:
		}
		return fmt.Sprintf("<LineServer closed: %v>", lserver.tomb.Err())
	}
}

func (lserver *LineServer) SendLine(line string) {
	n, err := lserver.conn.Write([]byte(line + "\r\n"))
	if err != nil {
		panic(fmt.Sprintf("LineServer cannot SendLine: %v", err))
	}
	if n < len(line) {
		panic("short write")
	}
}
