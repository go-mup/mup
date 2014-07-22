package ldap

import (
	"fmt"
	"sync"
	"time"

	"gopkg.in/tomb.v2"
)

type ManagedConn struct {
	tomb     tomb.Tomb
	config   Config
	searches chan *Search
	results  chan managedResults
	open     chan bool
	close    chan bool

	mu     sync.Mutex
	err    error
	closed bool
}

type managedResults struct {
	results []Result
	err     error
}

func DialManaged(config *Config) *ManagedConn {
	mconn := &ManagedConn{
		config:   *config,
		searches: make(chan *Search),
		results:  make(chan managedResults),
		open:     make(chan bool),
		close:    make(chan bool),
	}
	mconn.tomb.Go(mconn.loop)
	return mconn
}

func (mconn *ManagedConn) Close() error {
	mconn.mu.Lock()
	closed := mconn.closed
	mconn.closed = true
	mconn.mu.Unlock()
	if !closed {
		mconn.close <- true
	}
	return nil
}

func (mconn *ManagedConn) setError(err error) {
	mconn.mu.Lock()
	mconn.err = err
	mconn.mu.Unlock()
}

var pingSearch = Search{
	Filter: "(unknownAttr=this-query-is-just-a-ping)",
	Attrs:  []string{"unknownAttr"},
}

const managedTimeout = 5 * time.Second

func (mconn *ManagedConn) loop() error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	refs := 1
	for refs > 0 {
		conn, err := Dial(&mconn.config)
		if err != nil {
			mconn.setError(err)
			select {
			case <-time.After(managedTimeout):
			case <-mconn.open:
				refs++
			case <-mconn.close:
				refs--
			}
			continue
		}
		mconn.setError(nil)

		var results []Result
		for refs > 0 && err == nil {
			select {
			case s := <-mconn.searches:
				results, err = conn.Search(s)
				select {
				case mconn.results <- managedResults{results, err}:
				case <-time.After(500 * time.Millisecond):
				}
			case <-ticker.C:
				_, err = conn.Search(&pingSearch)
			case <-mconn.open:
				refs++
			case <-mconn.close:
				refs--
			}
		}
		mconn.setError(err)
		conn.Close()
	}
	return nil
}

func (mconn *ManagedConn) Conn() Conn {
	select {
	case mconn.open <- true:
	case <-mconn.tomb.Dead():
		panic("ManagedConn.Conn called after closing connection")
	}
	return &managedConn{mconn: mconn}
}

type managedConn struct {
	mu     sync.Mutex
	mconn  *ManagedConn
	closed bool
}

func (conn *managedConn) Close() error {
	conn.mu.Lock()
	closed := conn.closed
	conn.closed = true
	conn.mu.Unlock()
	if !closed {
		conn.mconn.close <- true
	}
	return nil
}

func (conn *managedConn) Search(s *Search) ([]Result, error) {
	conn.mu.Lock()
	closed := conn.closed
	conn.mu.Unlock()
	if closed {
		return nil, fmt.Errorf("LDAP connection already closed")
	}
	timeout := time.After(managedTimeout)
	select {
	case conn.mconn.searches <- s:
		select {
		case r := <-conn.mconn.results:
			return r.results, r.err
		case <-timeout:
		}
	case <-timeout:
	}
	conn.mconn.mu.Lock()
	err := conn.mconn.err
	conn.mconn.mu.Unlock()
	if err == nil {
		err = fmt.Errorf("LDAP server is a big sluggish right now. Please try again soon.")
	}
	return nil, err
}
