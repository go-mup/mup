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

type alphaPlugin struct {
	tomb     tomb.Tomb
	plugger  *mup.Plugger
	commands chan *mup.Command
	config   struct {
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
	bodyPath   = xmlpath.MustCompile("//body")
	failPath   = xmlpath.MustCompile("//queryresult[@success='false']")
	textPath   = xmlpath.MustCompile("/queryresult[@success='true']/pod[@primary='true']/subpod/plaintext")
)

func (p *alphaPlugin) handle(cmd *mup.Command) {
	var args struct{ Query string }
	cmd.Args(&args)

	form := url.Values{
		"i":         {args.Query},
		"plaintext": {"true"},
	}

	// TODO That's a hack to develop the plugin while they fix the web interface to request AppIDs.

	req, err := http.NewRequest("GET", "http://products.wolframalpha.com/alpha/styledapiresults.jsp", nil)
	if err != nil {
		panic(err)
	}
	req.URL.RawQuery = form.Encode()
	req.Header = http.Header{
		"User-Agent": {"Mozilla/5.0 (Windows NT 5.1; rv:10.0.2) Gecko/20100101 Firefox/10.0.2"},
		"Referer":    {"http://products.wolframalpha.com/api/explorer.html"},
	}

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

	root, err := xmlpath.ParseHTML(bytes.NewBuffer(data))
	if err != nil {
		p.plugger.Logf("Cannot parse WolframAlpha response: %v\nResponse:\n%s", err, data)
		p.plugger.Sendf(cmd, "Cannot parse WolframAlpha response.")
		return
	}

	body, ok := bodyPath.Bytes(root)
	if !ok {
		p.plugger.Logf("Cannot parse WolframAlpha response: no body\nResponse:\n%s", data)
		p.plugger.Sendf(cmd, "Cannot parse WolframAlpha response.")
		return
	}

	result, err := xmlpath.Parse(bytes.NewBuffer(body))
	if err != nil {
		p.plugger.Logf("Cannot parse WolframAlpha response body: %v\nResponse:\n%s", body)
		p.plugger.Sendf(cmd, "Cannot parse WolframAlpha response.")
		return
	}

	text, ok := textPath.String(result)
	if ok {
		p.plugger.Debugf("WolframAlpha result:\n%s", body)
		p.plugger.Sendf(cmd, "%s", strings.Join(strings.Fields(text), " "))
		return
	}

	if failPath.Exists(result) {
		p.plugger.Sendf(cmd, "Cannot infer any meaningful information out of this.")
		return
	}

	p.plugger.Logf("Cannot parse WolframAlpha response XML:\n%s", body)
	p.plugger.Sendf(cmd, "Cannot parse WolframAlpha response.")
}
