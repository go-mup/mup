package mup

import (
	"bytes"
	"net/http"
	"time"

	"gopkg.in/mgo.v2/bson"
	"gopkg.in/mup.v0"
	"gopkg.in/mup.v0/schema"
	"gopkg.in/tomb.v2"
	"gopkg.in/xmlpath.v2"
	"io/ioutil"
	"net/url"
	"strings"
)

var Plugin = mup.PluginSpec{
	Name:     "wolframalpha",
	Help:     "Exposes the infer command for querying the WolframAlpha engine.",
	Start:    start,
	Commands: Commands,
}

var Commands = schema.Commands{{
	Name: "infer",
	Help: "Queries the WolframAlpha engine.",
	Args: schema.Args{{
		Name: "query",
		Flag: schema.Required | schema.Trailing,
	}},
}}

func init() {
	mup.RegisterPlugin(&Plugin)
}

var defaultEndpoint = "http://api.wolframalpha.com/v2/query"

type alphaPlugin struct {
	tomb     tomb.Tomb
	plugger  *mup.Plugger
	commands chan *mup.Command
	config   struct {
		AppID    string
		Endpoint string

		HandleTimeout bson.DurationString
	}
}

const defaultHandleTimeout = 500 * time.Millisecond

func start(plugger *mup.Plugger) (mup.Stopper, error) {
	p := &alphaPlugin{
		plugger:  plugger,
		commands: make(chan *mup.Command),
	}
	plugger.Config(&p.config)
	if p.config.Endpoint == "" {
		p.config.Endpoint = defaultEndpoint
	}
	if p.config.HandleTimeout.Duration == 0 {
		p.config.HandleTimeout.Duration = defaultHandleTimeout
	}
	p.tomb.Go(p.loop)
	return p, nil
}

func (p *alphaPlugin) Stop() error {
	p.tomb.Kill(nil)
	return p.tomb.Wait()
}

func (p *alphaPlugin) HandleCommand(cmd *mup.Command) error {
	select {
	case p.commands <- cmd:
	case <-time.After(p.config.HandleTimeout.Duration):
		p.plugger.Sendf(cmd, "The WolframAlpha servers seem a bit sluggish right now. Please try again soon.")
	}
	return nil
}

func (p *alphaPlugin) loop() error {
	for {
		select {
		case cmd := <-p.commands:
			p.handle(cmd)
		case <-p.tomb.Dying():
			return nil
		}
	}
	return nil
}

var (
	httpClient = http.Client{Timeout: time.Duration(5 * time.Second)}
	textPath   = xmlpath.MustCompile("/queryresult[@success='true']/pod[@primary='true']/subpod/plaintext")
	failPath   = xmlpath.MustCompile("/queryresult[@success='false']")
	errorPath  = xmlpath.MustCompile("/queryresult[@error='true']/error/msg")
)

func (p *alphaPlugin) handle(cmd *mup.Command) {
	var args struct{ Query string }
	cmd.Args(&args)

	form := url.Values{
		"appid":  {p.config.AppID},
		"format": {"plaintext"},
		"input":  {args.Query},
	}

	req, err := http.NewRequest("GET", p.config.Endpoint, nil)
	if err != nil {
		panic(err)
	}
	req.URL.RawQuery = form.Encode()

	resp, err := httpClient.Do(req)
	if err != nil {
		p.plugger.Logf("Cannot query WolframAlpha: %v", err)
		p.plugger.Sendf(cmd, "Cannot query WolframAlpha: %v", err)
		return
	}
	defer resp.Body.Close()

	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		p.plugger.Logf("Cannot read WolframAlpha response: %v", err)
		p.plugger.Sendf(cmd, "Cannot read WolframAlpha response: %v", err)
		return
	}

	result, err := xmlpath.Parse(bytes.NewBuffer(data))
	if err != nil {
		p.plugger.Logf("Cannot parse WolframAlpha response: %v\nResponse:\n%s", err, data)
		p.plugger.Sendf(cmd, "Cannot parse WolframAlpha response.")
		return
	}

	if text, ok := textPath.String(result); ok {
		p.plugger.Debugf("WolframAlpha result:\n%s", data)
		p.plugger.Sendf(cmd, "%s", strip(text))
		return
	}

	if msg, ok := errorPath.String(result); ok {
		msg = strip(msg)
		p.plugger.Logf("WolframAlpha reported an error: %s", msg)
		p.plugger.Sendf(cmd, "WolframAlpha reported an error: %s", msg)
		return
	}

	if failPath.Exists(result) {
		p.plugger.Debugf("WolframAlpha result:\n%s", data)
		p.plugger.Sendf(cmd, "Cannot infer much out of this.")
		return
	}

	p.plugger.Logf("Cannot parse WolframAlpha response XML:\n%s", data)
	p.plugger.Sendf(cmd, "Cannot parse WolframAlpha response.")
}

func strip(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
