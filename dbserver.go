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
func (h *DBServerHelper) SetPath(dbpath string) {
	h.dbpath = dbpath
}

func (h *DBServerHelper) start() {
	if h.server != nil {
		panic("DBServerHelper already started")
	}
	if h.dbpath == "" {
		panic("DBServerHelper.SetPath must be called before using the server")
	}
	mgo.SetStats(true)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic("unable to listen on a local address: " + err.Error())
	}
	addr := l.Addr().(*net.TCPAddr)
	l.Close()
	h.host = addr.String()

	args := []string{
		"--dbpath", h.dbpath,
		"--bind_ip", "127.0.0.1",
		"--port", strconv.Itoa(addr.Port),
		"--nssize", "1",
		"--noprealloc",
		"--smallfiles",
		"--nojournal",
	}
	h.tomb = tomb.Tomb{}
	h.server = exec.Command("mongod", args...)
	h.server.Stdout = &h.output
	h.server.Stderr = &h.output
	err = h.server.Start()
	if err != nil {
		panic(err)
	}
	h.tomb.Go(h.monitor)
	h.Wipe()
}

func (h *DBServerHelper) monitor() error {
	h.server.Process.Wait()
	if h.tomb.Alive() {
		// Present some debugging information.
		fmt.Fprintf(os.Stderr, "---- mongod process died unexpectedly:\n")
		fmt.Fprintf(os.Stderr, "%s", h.output.Bytes())
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
func (h *DBServerHelper) Stop() {
	if h.session != nil {
		h.checkSessions()
		h.session.Close()
		h.session = nil
	}
	if h.server != nil {
		h.tomb.Kill(nil)
		h.server.Process.Kill()
		select {
		case <-h.tomb.Dead():
		case <-time.After(5 * time.Second):
			panic("timeout waiting for mongod process to die")
		}
		h.server = nil
	}
}

// Session returns a new session to the server. The returned session
// must be closed after the test is done with it.
//
// The first Session obtained from a DBServerHelper will start it.
func (h *DBServerHelper) Session() *mgo.Session {
	if h.server == nil {
		h.start()
	}
	if h.session == nil {
		mgo.ResetStats()
		var err error
		h.session, err = mgo.Dial(h.host + "/test")
		if err != nil {
			panic(err)
		}
	}
	return h.session.Copy()
}

// checkSessions ensures all mgo sessions opened were properly closed.
// For slightly faster tests, it may be disabled setting the
// environmnet variable CHECK_SESSIONS to 0.
func (h *DBServerHelper) checkSessions() {
	if check := os.Getenv("CHECK_SESSIONS"); check == "0" || h.server == nil || h.session == nil {
		return
	}
	h.session.Close()
	h.session = nil
	for i := 0; i < 100; i++ {
		stats := mgo.GetStats()
		if stats.SocketsInUse == 0 && stats.SocketsAlive == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	panic("There are mgo sessions still alive.")
}

// Wipe drops all created databases.
func (h *DBServerHelper) Wipe() {
	if h.server == nil || h.session == nil {
		return
	}
	h.checkSessions()
	sessionUnset := h.session == nil
	session := h.Session()
	defer session.Close()
	if sessionUnset {
		h.session.Close()
		h.session = nil
	}
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
