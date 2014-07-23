package mup

import (
	"bytes"
	"net"
	"os/exec"

	"gopkg.in/mgo.v2"
	"time"
)

const dbServerAddr = "127.0.0.1:50017"

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
	host, port, err := net.SplitHostPort(dbServerAddr)
	if err != nil {
		panic(err)
	}
	args := []string{
		"--dbpath", s.dbpath,
		"--bind_ip", host,
		"--port", port,
		"--nssize", "1",
		"--noprealloc",
		"--smallfiles",
		"--nojournal",
	}
	s.server = exec.Command("mongod", args...)
	s.server.Stdout = &s.output
	s.server.Stderr = &s.output
	err = s.server.Start()
	if err != nil {
		panic(err)
	}
}

// Stop stops the server process.
func (s *DBServerHelper) Stop() {
	if s.session != nil {
		s.session.Close()
		s.session = nil
	}
	if s.server != nil {
		s.server.Process.Kill()
		s.server.Process.Wait()
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
		s.session, err = mgo.Dial(dbServerAddr + "/mup")
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
