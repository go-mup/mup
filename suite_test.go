package mup

import (
	"bufio"
	"bytes"
	. "gopkg.in/check.v1"
	"labix.org/v2/mgo"
	"net"
	"os/exec"
	"sync"
	"testing"
	"time"
)

func Test(t *testing.T) {
	TestingT(t)
}

// Handy alias
type M map[string]interface{}

// ----------------------------------------------------------------------------
// The test suite for emulating a line-based protocol.

type LineServerSuite struct {
	Addr  *net.TCPAddr
	l     *net.TCPListener
	m     sync.Mutex
	conn  *net.TCPConn
	connr *bufio.Reader
	done  bool
	reset bool
}

func (tp *LineServerSuite) SetUpSuite(c *C) {
	addr, err := net.ResolveTCPAddr("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	tp.l, err = net.ListenTCP("tcp", addr)
	if err != nil {
		panic(err)
	}
	tp.Addr = tp.l.Addr().(*net.TCPAddr)
	go tp.serve()
}

func (tp *LineServerSuite) TearDownSuite(c *C) {
	tp.m.Lock()
	tp.done = true
	tp.m.Unlock()
	conn, err := net.DialTCP("tcp", nil, tp.Addr)
	if err != nil {
		conn.Close()
	}
}

func (tp *LineServerSuite) ResetLineServer(c *C) {
	tp.m.Lock()
	tp.reset = true
	tp.m.Unlock()
	conn, err := net.DialTCP("tcp", nil, tp.Addr)
	if err != nil {
		conn.Close()
	}
}

func (tp *LineServerSuite) TearDownTest(c *C) {
	tp.m.Lock()
	if tp.conn != nil {
		tp.conn.Close()
		tp.conn = nil
		tp.connr = nil
	}
	tp.m.Unlock()
}

func (tp *LineServerSuite) serve() {
	tp.m.Lock()
	defer tp.m.Unlock()
	for {
		tp.m.Unlock()
		conn, err := tp.l.Accept()
		tp.m.Lock()
		if tp.done || tp.reset {
			if err != nil {
				conn.Close()
			}
			if tp.conn != nil {
				tp.conn.Close()
				tp.conn = nil
				tp.connr = nil
			}
			if tp.reset {
				tp.reset = false
				continue
			}
			tp.l.Close()
			return
		}
		if tp.conn != nil {
			panic("got second connection with one in progress")
		}
		if err != nil {
			panic(err)
		}
		tp.conn = conn.(*net.TCPConn)
		tp.connr = bufio.NewReader(conn)
	}
}

func (tp *LineServerSuite) ReadLine() string {
	tp.m.Lock()
	defer tp.m.Unlock()
	tp.waitConn()
	l, prefix, err := tp.connr.ReadLine()
	if prefix {
		panic("line is too long")
	}
	if err != nil {
		panic(err)
	}
	return string(l)
}

func (tp *LineServerSuite) SendLine(line string) {
	tp.m.Lock()
	defer tp.m.Unlock()
	tp.waitConn()
	n, err := tp.conn.Write([]byte(line + "\r\n"))
	if err != nil {
		panic(err)
	}
	if n < len(line) {
		panic("short write")
	}
}

func (tp *LineServerSuite) waitConn() {
	for i := 0; i < 50 && tp.conn == nil; i++ {
		tp.m.Unlock()
		time.Sleep(1e8)
		tp.m.Lock()
	}
	if tp.conn == nil {
		panic("not connected")
	}
}

// ----------------------------------------------------------------------------
// The mgo test suite

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
