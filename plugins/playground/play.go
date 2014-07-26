package mup

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"time"

	"gopkg.in/mup.v0"
	"gopkg.in/mup.v0/schema"
	"gopkg.in/tomb.v2"
)

var Plugin = mup.PluginSpec{
	Name:     "playground",
	Help:     "Exposes the run command for executing Go code.",
	Start:    start,
	Commands: Commands,
}

var Commands = schema.Commands{{
	Name: "run",
	Help: `Runs the provided Go language code.
	
	The code must be one or more statements separated by semicolons, or
	an expression to be given to fmt.Print if the -p option is provided.

	The output is presented in a single line, either visually
	separating non-empty lines, or as a single quoted string if
	the -q option is used.
	`,
	Args: schema.Args{{
		Name: "-p",
		Type: schema.Bool,
	}, {
		Name: "-q",
		Type: schema.Bool,
	}, {
		Name: "code",
		Flag: schema.Required | schema.Trailing,
	}},
}}

func init() {
	mup.RegisterPlugin(&Plugin)
}

var defaultEndpoint = "http://play.golang.org"

type playPlugin struct {
	tomb     tomb.Tomb
	plugger  *mup.Plugger
	commands chan *mup.Command
	config   struct {
		Endpoint string
	}
}

func start(plugger *mup.Plugger) mup.Stopper {
	p := &playPlugin{
		plugger:  plugger,
		commands: make(chan *mup.Command, 5),
	}
	plugger.Config(&p.config)
	if p.config.Endpoint == "" {
		p.config.Endpoint = defaultEndpoint
	}
	p.tomb.Go(p.loop)
	return p
}

func (p *playPlugin) Stop() error {
	close(p.commands)
	return p.tomb.Wait()
}

func (p *playPlugin) HandleCommand(cmd *mup.Command) {
	select {
	case p.commands <- cmd:
	default:
		p.plugger.Sendf(cmd, "The Go Playground servers seem a bit sluggish right now. Please try again soon.")
	}
}

func (p *playPlugin) loop() error {
	for {
		cmd, ok := <-p.commands
		if !ok {
			break
		}
		p.handle(cmd)
	}
	return nil
}

var httpClient = http.Client{Timeout: time.Duration(10 * time.Second)}

type compileResult struct {
	Errors string
	Events []compileEvent
}

type compileEvent struct {
	Message string
	Delay   time.Duration
}

type formatResult struct {
	Error string
	Body  string
}

func (p *playPlugin) handle(cmd *mup.Command) {
	var args struct {
		Code   string
		Print  bool "p"
		Quoted bool "q"
	}
	cmd.Args(&args)

	var body string
	if args.Print {
		body = "package main\nfunc main() { fmt.Print(" + args.Code + ") }"
	} else {
		body = "package main\nfunc main() { " + args.Code + " }"
	}

	var formatted formatResult
	var result compileResult
	if !p.play(cmd, format, body, &formatted) {
		return
	}
	if formatted.Error != "" {
		p.plugger.Sendf(cmd, "%s", formatText([]byte(formatted.Error)))
	}
	if !p.play(cmd, compile, formatted.Body, &result) {
		return
	}
	var reply []byte
	if result.Errors != "" {
		reply = formatText([]byte(result.Errors))
	} else if args.Quoted {
		reply = formatQuoted(joinEvents(result.Events))
	} else {
		reply = formatText(joinEvents(result.Events))
	}
	p.plugger.Sendf(cmd, "%s", reply)
}

type playAction int

const (
	format  playAction = 1
	compile playAction = 2
)

func (p *playPlugin) play(cmd *mup.Command, action playAction, body string, result interface{}) (ok bool) {
	var form url.Values
	var path string
	if action == format {
		path = "/fmt"
		form = url.Values{
			"imports": {"1"},
			"body":    {body},
		}
	} else {
		path = "/compile"
		form = url.Values{
			"version": {"2"},
			"body":    {body},
		}
	}

	endpoint := p.config.Endpoint + path
	resp, err := httpClient.Post(p.config.Endpoint+path, "application/x-www-form-urlencoded", bytes.NewBufferString(form.Encode()))
	if err == nil {
		defer resp.Body.Close()
	}
	if err != nil || resp.StatusCode != 200 {
		p.plugger.Logf("Error on request to Go Playground: %v", err)
		p.plugger.Sendf(cmd, "Go Playground request failed. Please try again soon.")
		return false
	}

	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		p.plugger.Logf("Cannot read Go Playground response: %v", err)
		p.plugger.Sendf(cmd, "Cannot read Go Playground response: %v", err)
		return false
	}

	p.plugger.Debugf("Response from %s: %s", endpoint, data)

	err = json.Unmarshal(data, result)
	if err != nil {
		p.plugger.Logf("Cannot parse Go Playground response: %v\nResponse:\n%s", err, data)
		p.plugger.Sendf(cmd, "Cannot parse Go Playground response.")
		return false
	}
	return true
}

const (
	split      = " — "
	more       = "[...]"
	nonZeroNew = "[non-zero exit status]"
)

var (
	nonZeroOld  = []byte(" [process exited with non-zero status]\n")
	noOutputOld = []byte("[no output]\n")
	noOutputNew = []byte("[no output]")
)

var maxTextLen = mup.MaxTextLen - len(split) - len(more) - len(split) - len(nonZeroNew)

func truncateMaxLen(buf *bytes.Buffer, spaceScan int) {
	i := bytes.LastIndex(buf.Bytes()[spaceScan:maxTextLen], []byte(" "))
	if j := spaceScan + i + 1; j > maxTextLen-50 {
		buf.Truncate(j)
	} else {
		buf.Truncate(maxTextLen)
	}
}

func joinEvents(events []compileEvent) []byte {
	var buf bytes.Buffer
	l := 0
	for _, event := range events {
		l += len(event.Message)
	}
	buf.Grow(l)
	for _, event := range events {
		buf.WriteString(event.Message)
	}
	return buf.Bytes()
}

func formatQuoted(text []byte) []byte {
	if bytes.Equal(text, noOutputOld) {
		return noOutputNew
	}
	nonZero := bytes.HasSuffix(text, nonZeroOld)
	if nonZero {
		text = text[:len(text)-len(nonZeroOld)]
	}

	var buf bytes.Buffer
	buf.Grow(512)
	fmt.Fprintf(&buf, "%q", text)
	if buf.Len() > maxTextLen {
		truncateMaxLen(&buf, 1)
		buf.Truncate(len(bytes.TrimRight(buf.Bytes(), "\\")))
		buf.WriteString(`" + `)
		buf.WriteString(more)
	}

	if nonZero {
		buf.WriteString(split)
		buf.WriteString(nonZeroNew)
	}
	return buf.Bytes()
}

func formatText(text []byte) []byte {
	nonZero := bytes.HasSuffix(text, nonZeroOld)
	if nonZero {
		text = text[:len(text)-len(nonZeroOld)]
	}

	var buf bytes.Buffer
	buf.Grow(512)
	s := bufio.NewScanner(bytes.NewBuffer(text))
	for s.Scan() {
		text := strings.TrimSpace(s.Text())
		if strings.HasPrefix(text, "prog.go:") {
			text = strings.TrimLeft(text[8:], "0123456789: \t")
		}
		if text == "" {
			continue
		}
		if buf.Len() > 0 {
			buf.WriteString(split)
		}
		mark := buf.Len()
		buf.WriteString(text)
		if buf.Len() > maxTextLen {
			truncateMaxLen(&buf, mark)
			buf.WriteString(more)
			break
		}
	}

	if nonZero {
		if buf.Len() > 0 {
			buf.WriteString(split)
		}
		buf.WriteString(nonZeroNew)
	}
	if len(text) == 0 {
		// This string is usually sent by the server when there's
		// no output. If it's also missing, inject it.
		return noOutputNew
	}
	return buf.Bytes()
}
