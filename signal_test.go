package mup_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "gopkg.in/check.v1"
	"gopkg.in/mup.v0"
)

type SignalSuite struct {
	config  *mup.Config
	server  *mup.Server
	lserver *LineServer

	dbdir string
	db    *sql.DB

	bindir      string
	restorePath string
}

var _ = Suite(&SignalSuite{})

func (s *SignalSuite) SetUpSuite(c *C) {
	s.dbdir = c.MkDir()
	s.bindir = c.MkDir()

	s.restorePath = os.Getenv("PATH")
	os.Setenv("PATH", s.bindir+":"+s.restorePath)
}

func (s *SignalSuite) TearDownSuite(c *C) {
	os.Setenv("PATH", s.restorePath)
}

func (s *SignalSuite) SetUpTest(c *C) {
	s.FakeCLI(c, "")

	mup.SetDebug(true)
	mup.SetLogger(c)

	var err error
	s.db, err = mup.OpenDB(s.dbdir)
	c.Assert(err, IsNil)

	s.config = &mup.Config{
		DB:      s.db,
		Refresh: -1, // Manual refreshing for testing.
	}

	execSQL(c, s.db,
		`INSERT INTO account (name,kind,identity) VALUES ('one','signal','+55555')`,
	)

	s.server, err = mup.Start(s.config)
	c.Assert(err, IsNil)
}

func (s *SignalSuite) TearDownTest(c *C) {
	mup.SetDebug(false)
	mup.SetLogger(nil)

	s.server.Stop()
	s.server = nil

	s.db.Close()
	s.db = nil
	s.dbdir = c.MkDir()

	os.Remove(filepath.Join(s.bindir, "signal-cli"))
}

var outputOnceId int64

func (s *SignalSuite) FakeCLI(c *C, script string, outputOnce ...string) {
	outputOnceId++
	script = "#!/bin/bash\n{ echo -n $(cat)';'; echo -n $(basename $0); printf \";%s\" \"$@\"; echo; } >> $(dirname $0)/calls.txt\n" + script + "\n"
	if len(outputOnce) > 0 {
		script += fmt.Sprintf("once=$(dirname $0)/once.%d; if [ ! -f $once ]; then\ntouch $once\n", outputOnceId)
		for _, out := range outputOnce {
			script += "cat <<__OUTPUT_END__\n" + out + "\n__OUTPUT_END__\n"
		}
		script += "fi\n"
	}
	filename := filepath.Join(s.bindir, "signal-cli")
	err := ioutil.WriteFile(filename+".tmp", []byte(script), 0755)
	c.Assert(err, IsNil)
	err = os.Rename(filename+".tmp", filename)
	c.Assert(err, IsNil)
}

func (s *SignalSuite) CLI(c *C, subcmd string) [][]string {
	filename := filepath.Join(s.bindir, "calls.txt")
	data, err := ioutil.ReadFile(filename)
	if !os.IsNotExist(err) {
		c.Assert(err, IsNil)
	}
	data = bytes.TrimSuffix(data, []byte("\n"))
	var calls [][]string
	for _, line := range strings.Split(string(data), "\n") {
		call := strings.Split(line, ";")
		if subcmd == "" || len(call) > 4 && call[4] == subcmd {
			calls = append(calls, call)
		} else {
			c.Logf("Skipping line (%v %v %v): %q", subcmd == "", len(call) > 4, len(call) > 4 && call[4] == subcmd, line)
		}
	}
	return calls
}

func (s *SignalSuite) AssertCLI(c *C, subcmd string, calls [][]string) {
	filename := filepath.Join(s.bindir, "calls.txt")
	defer os.Remove(filename)
	var obtained [][]string
	for i := 0; i < 10; i++ {
		obtained = s.CLI(c, subcmd)
		if len(obtained) >= len(calls) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	c.Assert(obtained, DeepEquals, calls)
}

func (s *SignalSuite) TestFakeCLI(c *C) {
	s.server.Stop()
	os.Remove(filepath.Join(s.bindir, "calls.txt"))

	s.FakeCLI(c, "echo -n standard output:", "line1", "line2")

	cmd := exec.Command("signal-cli", "$arg0", "arg1a arg1b")
	cmd.Stdin = bytes.NewBufferString("standard input")
	output, err := cmd.CombinedOutput()
	c.Assert(err, IsNil)
	c.Assert(string(output), Equals, "standard output:line1\nline2\n")

	cmd = exec.Command("signal-cli", "again")
	cmd.Stdin = bytes.NewBufferString("standard again")
	output, err = cmd.CombinedOutput()
	c.Assert(err, IsNil)
	c.Assert(string(output), Equals, "standard output:")

	s.AssertCLI(c, "", [][]string{
		{"standard input", "signal-cli", "$arg0", "arg1a arg1b"},
		{"standard again", "signal-cli", "again"},
	})
}

func (s *SignalSuite) TestQuit(c *C) {
	err := s.server.Stop()
	c.Assert(err, IsNil)
}

var signalIncomingTests = []struct {
	update  string
	message mup.Message
}{{
	`{
		"envelope": {
			"source": "+12345",
			"sourceDevice": 1,
			"relay": null,
			"timestamp": 1586383094999,
			"isReceipt": false,
			"dataMessage": {
				"timestamp": 1586383094999,
				"message": "Hello mup!",
				"expiresInSeconds": 0,
				"attachments": [],
				"groupInfo": null
			},
			"syncMessage": null,
			"callMessage": null,
			"receiptMessage": null
		}
	}`,
	mup.Message{
		Account: "one",
		Lane:    1,
		Nick:    "+12345",
		User:    "~user",
		Host:    "signal",
		Command: "PRIVMSG",
		Channel: "@+12345",
		Text:    "Hello mup!",
		BotText: "Hello mup!",
		Bang:    "/",
		AsNick:  "mup",
		Time:    time.Date(2020, 4, 8, 21, 58, 14, 999e6, time.UTC),
	},
}, {
	`{
		"envelope": {
			"source": "+12345",
			"sourceDevice": 1,
			"relay": null,
			"timestamp": 1586383094999,
			"isReceipt": false,
			"dataMessage": {
				"timestamp": 1586383094999,
				"message": "Hello group!",
				"expiresInSeconds": 0,
				"attachments": [],
				"groupInfo": {
					"groupId": "AABBCCDD==",
					"members": null,
					"name": null,
					"type": "DELIVER"
				}
			},
			"syncMessage": null,
			"callMessage": null,
			"receiptMessage": null
		}
	}`,
	mup.Message{
		Account: "one",
		Lane:    1,
		Nick:    "+12345",
		User:    "~user",
		Host:    "signal",
		Command: "PRIVMSG",
		Channel: "#AABBCCDD==",
		Text:    "Hello group!",
		Bang:    "/",
		AsNick:  "mup",
		Time:    time.Date(2020, 4, 8, 21, 58, 14, 999e6, time.UTC),
	},
}, {
	`{
		"envelope": {
			"source": "+12345",
			"sourceDevice": 1,
			"relay": null,
			"timestamp": 1586383094999,
			"isReceipt": false,
			"dataMessage": null,
			"syncMessage": {
				"sentMessage": {
					"timestamp": 1586383094999,
					"message": "Hello sync!",
					"expiresInSeconds": 0,
					"attachments": [],
					"groupInfo": null,
					"destination": "+54321"
				},
				"blockedNumbers": null,
				"readMessages": null,
				"type": null
			},
			"callMessage": null,
			"receiptMessage": null
		}
	}`,
	mup.Message{
		Account: "one",
		Lane:    1,
		Nick:    "+12345",
		User:    "~user",
		Host:    "signal",
		Command: "PRIVMSG",
		Channel: "@+12345", // TODO Should that be the destination? Makes testing harder, might be more correct.
		Text:    "Hello sync!",
		BotText: "Hello sync!",
		Bang:    "/",
		AsNick:  "mup",
		Time:    time.Date(2020, 4, 8, 21, 58, 14, 999e6, time.UTC),
	},
}, {
	`{
		"error": {
			"message": null
		},
		"envelope": {
			"source": "",
			"timestamp": 1586383094999
		}
	}`,
	mup.Message{
		Account: "one",
		Lane:    1,
		Nick:    "system",
		User:    "~user",
		Host:    "signal",
		Command: "SIGNALDATA",
		Text:    `{"error":{"message":null},"envelope":{"source":"","timestamp":1586383094999}}`,
		Bang:    "/",
		AsNick:  "mup",
		Time:    time.Date(2020, 4, 8, 21, 58, 14, 999e6, time.UTC),
	},
}}

func (s *SignalSuite) TestIncoming(c *C) {
	for _, test := range signalIncomingTests {
		var buf bytes.Buffer
		err := json.Compact(&buf, []byte(test.update))
		c.Assert(err, IsNil)

		var lastId int64
		err = s.db.QueryRow("SELECT id FROM message ORDER BY id DESC").Scan(&lastId)
		if err != sql.ErrNoRows {
			c.Assert(err, IsNil)
		}

		s.FakeCLI(c, `test "$3" = receive || exit 0`, buf.String())

		var msg mup.Message
		for i := 0; i < 100; i++ {
			fields := "id,lane,account,nick,user,host,command,channel,text,bot_text,bang,as_nick,time"
			row := s.db.QueryRow("SELECT "+fields+" FROM message WHERE command=? ORDER BY id DESC", test.message.Command)
			err = row.Scan(
				&msg.Id, &msg.Lane, &msg.Account, &msg.Nick, &msg.User, &msg.Host, &msg.Command,
				&msg.Channel, &msg.Text, &msg.BotText, &msg.Bang, &msg.AsNick, &msg.Time,
			)
			if err == nil && msg.Id > lastId {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if err == sql.ErrNoRows || msg.Id == lastId {
			c.Fatalf("Signal update not received as an incoming message: %s", test.update)
		}
		c.Assert(err, IsNil)

		lastId = msg.Id

		c.Assert(msg.Time.UTC().String(), Equals, test.message.Time.String())
		msg.Time = time.Time{}
		test.message.Time = time.Time{}

		msg.Id = 0
		c.Assert(msg, DeepEquals, test.message)
	}
}

func (s *SignalSuite) TestIncomingSequence(c *C) {
	test0 := signalIncomingTests[0]
	test1 := signalIncomingTests[1]

	var update0, update1 bytes.Buffer
	err := json.Compact(&update0, []byte(test0.update))
	c.Assert(err, IsNil)
	err = json.Compact(&update1, []byte(test1.update))
	c.Assert(err, IsNil)

	s.FakeCLI(c, "", update0.String(), update1.String(), "", "")

	var gotCount, wantCount = 0, 4
	for i := 0; i < 100; i++ {
		err := s.db.QueryRow("SELECT COUNT(*) FROM message").Scan(&gotCount)
		c.Assert(err, IsNil)
		if gotCount == wantCount {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if gotCount != wantCount {
		c.Fatalf("Want %d signal incoming messages, got %d", wantCount, gotCount)
	}

	var rows *sql.Rows
	rows, err = s.db.Query("SELECT id,lane,account,nick,user,host,command,channel,text,bot_text,bang,as_nick,time FROM message ORDER BY id")
	c.Assert(err, IsNil)
	defer rows.Close()

	var msgs []mup.Message
	for rows.Next() {
		var msg mup.Message
		err = rows.Scan(&msg.Id, &msg.Lane, &msg.Account, &msg.Nick, &msg.User, &msg.Host, &msg.Command,
			&msg.Channel, &msg.Text, &msg.BotText, &msg.Bang, &msg.AsNick, &msg.Time)
		c.Assert(err, IsNil)
		msg.Id = 0
		msg.Time = time.Time{}
		msgs = append(msgs, msg)
	}

	test0.message.Time = time.Time{}
	test1.message.Time = time.Time{}

	c.Assert(len(msgs), Equals, wantCount)
	c.Assert(msgs[1], DeepEquals, test0.message)
	c.Assert(msgs[3], DeepEquals, test1.message)

	c.Assert(msgs[0].Command, Equals, "SIGNALDATA")
	c.Assert(msgs[0].Text, Matches, `^\{.*"message":"`+test0.message.Text+`".*\}$`)
	c.Assert(msgs[2].Command, Equals, "SIGNALDATA")
	c.Assert(msgs[2].Text, Matches, `^\{.*"message":"`+test1.message.Text+`".*\}$`)

	calls := s.CLI(c, "receive")
	c.Assert(calls[0], DeepEquals, []string{"", "signal-cli", "-u", "+55555", "receive", "--json", "--ignore-attachments"})
}

func (s *SignalSuite) TestOutgoing(c *C) {

	// Ensure messages are only inserted after plugin has been loaded.
	s.server.RefreshAccounts()

	execSQL(c, s.db,
		`INSERT INTO message (lane,account,channel,nick,text) VALUES (2,'one','@+12345','nick','Implicit PRIVMSG.')`,
		`INSERT INTO message (lane,account,channel,nick,text,command) VALUES (2,'one','@+12345','nick','Explicit PRIVMSG.','PRIVMSG')`,
		`INSERT INTO message (lane,account,channel,nick,text,command) VALUES (2,'one','@+12345','nick','Explicit NOTICE.','NOTICE')`,
		`INSERT INTO message (lane,account,channel,nick,text) VALUES (2,'one','#AABBCCDD==','nick','Group chat.')`,
	)

	s.AssertCLI(c, "send", [][]string{
		{"Implicit PRIVMSG.", "signal-cli", "-u", "+55555", "send", "+12345"},
		{"Explicit PRIVMSG.", "signal-cli", "-u", "+55555", "send", "+12345"},
		{"Explicit NOTICE.", "signal-cli", "-u", "+55555", "send", "+12345"},
		{"Group chat.", "signal-cli", "-u", "+55555", "send", "-g", "AABBCCDD=="},
	})

	// Send another one to test the loop further.
	execSQL(c, s.db,
		`INSERT INTO message (lane,account,channel,nick,text) VALUES (2,'one','@+12345','nick','Hello again!')`,
	)

	s.AssertCLI(c, "send", [][]string{
		{"Hello again!", "signal-cli", "-u", "+55555", "send", "+12345"},
	})
}
