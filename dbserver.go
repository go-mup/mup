package mup

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"time"

	"gopkg.in/mgo.v2"
	"gopkg.in/tomb.v2"
	"strconv"
)

// DBServerHelper is a simple utility to be used strictly within
// test suites for controlling the MongoDB server process.
// Its design encourages the test suite to only start the server
// if running a test that depends on the database, and then keeping
// the database running for all tests to use it.
type DBServerHelper struct {
	session *mgo.Session
	output  bytes.Buffer
	server  *exec.Cmd
	dbpath  string
	host    string
	tomb    tomb.Tomb
}

// SetPath sets the path to the directory where the server files
// will be held if it is started.
func (s *DBServerHelper) SetPath(dbpath string) {
	s.dbpath = dbpath
}

func (s *DBServerHelper) start() {
	if s.server != nil {
		panic("DBServerHelper already started")
	}
	if s.dbpath == "" {
		panic("DBServerHelper.SetPath must be called before using the server")
	}
	mgo.SetStats(true)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic("unable to listen on a local address: " + err.Error())
	}
	addr := l.Addr().(*net.TCPAddr)
	l.Close()
	s.host = addr.String()

	args := []string{
		"--dbpath", s.dbpath,
		"--bind_ip", "127.0.0.1",
		"--port", strconv.Itoa(addr.Port),
		"--nssize", "1",
		"--noprealloc",
		"--smallfiles",
		"--nojournal",
	}
	s.tomb = tomb.Tomb{}
	s.server = exec.Command("mongod", args...)
	s.server.Stdout = &s.output
	s.server.Stderr = &s.output
	err = s.server.Start()
	if err != nil {
		panic(err)
	}
	s.tomb.Go(s.monitor)
	s.Reset()
}

func (s *DBServerHelper) monitor() error {
	s.server.Process.Wait()
	if s.tomb.Alive() {
		// Present some debugging information.
		fmt.Fprintf(os.Stderr, "---- mongod process died unexpectedly:\n")
		fmt.Fprintf(os.Stderr, "%s", s.output.Bytes())
		fmt.Fprintf(os.Stderr, "---- mongod processes running right now:\n")
		cmd := exec.Command("/bin/sh", "-c", "ps auxw | grep mongod")
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		cmd.Run()
		fmt.Fprintf(os.Stderr, "----------------------------------------\n")

		panic("mongod process died unexpectedly")
	}
	return nil
}

// Stop stops the server process.
func (s *DBServerHelper) Stop() {
	if s.session != nil {
		s.session.Close()
		s.session = nil
	}
	if s.server != nil {
		s.tomb.Kill(nil)
		s.server.Process.Kill()
		select {
		case <-s.tomb.Dead():
		case <-time.After(5 * time.Second):
			panic("timeout waiting for mongod process to die")
		}
		s.server = nil
	}
}

// Session returns a new session to the server. The returned session
// must be closed after the test is done with it.
//
// The first Session obtained from a DBServerHelper will start it.
func (s *DBServerHelper) Session() *mgo.Session {
	if s.server == nil {
		s.start()
	}
	if s.session == nil {
		mgo.ResetStats()
		var err error
		s.session, err = mgo.Dial(s.host + "/test")
		if err != nil {
			panic(err)
		}
	}
	return s.session.Copy()
}

// AssertClosed ensures all mgo sessions opened were properly closed.
func (s *DBServerHelper) AssertClosed() {
	if s.server == nil {
		return
	}
	if s.session != nil {
		s.session.Close()
		s.session = nil
	}
	for i := 0; i < 100; i++ {
		stats := mgo.GetStats()
		if stats.SocketsInUse == 0 && stats.SocketsAlive == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	panic("There are mgo sessions still alive.")
}

// Reset resets the server state, dropping any created databases.
func (s *DBServerHelper) Reset() {
	if s.server == nil {
		return
	}
	session := s.Session()
	defer session.Close()
	names, err := session.DatabaseNames()
	if err != nil {
		panic(err)
	}
	for _, name := range names {
		switch name {
		case "admin", "local", "config":
		default:
			err = session.DB(name).DropDatabase()
			if err != nil {
				panic(err)
			}
		}
	}
}
