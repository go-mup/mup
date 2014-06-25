package mup

import (
	"bufio"
	"bytes"
	"fmt"
	. "gopkg.in/check.v1"
	"labix.org/v2/mgo"
	"gopkg.in/tomb.v2"
	"net"
	"os/exec"
	"sync"
	"testing"
	"time"
)

func Test(t *testing.T) { TestingT(t) }

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

type MgoSuite struct {
	Session *mgo.Session
	output  bytes.Buffer
	server  *exec.Cmd
}

func (s *MgoSuite) SetUpSuite(c *C) {
	//mgo.SetDebug(true)
	mgo.SetStats(true)
	dbdir := c.MkDir()
	args := []string{
		"--dbpath", dbdir,
		"--bind_ip", "127.0.0.1",
		"--port", "50017",
		"--nssize", "1",
		"--noprealloc",
		"--smallfiles",
		"--nojournal",
	}
	s.server = exec.Command("mongod", args...)
	s.server.Stdout = &s.output
	s.server.Stderr = &s.output
	err := s.server.Start()
	if err != nil {
		panic(err)
	}
}

func (s *MgoSuite) TearDownSuite(c *C) {
	s.server.Process.Kill()
	s.server.Process.Wait()
}

func (s *MgoSuite) SetUpTest(c *C) {
	err := DropAll("localhost:50017")
	if err != nil {
		panic(err)
	}
	//mgo.SetLogger(c)
	mgo.ResetStats()
	s.Session, err = mgo.Dial("127.0.0.1:50017/mup")
	if err != nil {
		panic(err)
	}
}

func (s *MgoSuite) TearDownTest(c *C) {
	s.Session.Close()
	for i := 0; ; i++ {
		stats := mgo.GetStats()
		if stats.SocketsInUse == 0 && stats.SocketsAlive == 0 {
			break
		}
		if i == 20 {
			c.Fatal("Test left sockets in a dirty state")
		}
		c.Logf("Waiting for sockets to die: %d in use, %d alive", stats.SocketsInUse, stats.SocketsAlive)
		time.Sleep(5e8)
	}
}

func DropAll(mongourl string) (err error) {
	session, err := mgo.Dial(mongourl)
	if err != nil {
		return err
	}
	defer session.Close()

	names, err := session.DatabaseNames()
	if err != nil {
		return err
	}
	for _, name := range names {
		switch name {
		case "admin", "local", "config":
		default:
			err = session.DB(name).DropDatabase()
			if err != nil {
				return err
			}
		}
	}
	return nil
}
