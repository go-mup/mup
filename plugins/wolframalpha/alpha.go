package mup

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"gopkg.in/mup.v0"
	"gopkg.in/mup.v0/ldap"
	"gopkg.in/mup.v0/schema"
	"gopkg.in/tomb.v2"
)

var Plugin = mup.PluginSpec{
	Name:     "wolframalpha",
	Help:     "Exposes the infer command for querying the WolframAlpha engine.",
	Start:    start,
	Commands: Commands,
}

var Commands = schema.Commands{{
	Name: "infer",
	Help: `Queries the WolframAlpha engine.

	If -all is provided, all known information about the query is displayed
	rather than just the primary results.
	`,
	Args: schema.Args{{
		Name: "-all",
		Type: schema.Bool,
	}, {
		Name: "query",
		Flag: schema.Required | schema.Trailing,
	}},
}}

func init() {
	mup.RegisterPlugin(&Plugin)
}

var defaultEndpoint = "http://api.wolframalpha.com/v2/query"

type locEntry struct {
	loc  string
	when time.Time
}

type alphaPlugin struct {
	tomb     tomb.Tomb
	plugger  *mup.Plugger
	commands chan *mup.Command
	newLoc   map[string]locEntry
	oldLoc   map[string]locEntry
	config   struct {
		AppID    string
		Endpoint string
		LDAP     string
	}
}

func start(plugger *mup.Plugger) mup.Stopper {
	p := &alphaPlugin{
		plugger:  plugger,
		commands: make(chan *mup.Command, 5),
		newLoc:   make(map[string]locEntry),
		oldLoc:   make(map[string]locEntry),
	}
	plugger.Config(&p.config)
	if p.config.Endpoint == "" {
		p.config.Endpoint = defaultEndpoint
	}
	p.tomb.Go(p.loop)
	return p
}

func (p *alphaPlugin) Stop() error {
	close(p.commands)
	return p.tomb.Wait()
}

func (p *alphaPlugin) HandleCommand(cmd *mup.Command) {
	select {
	case p.commands <- cmd:
	default:
		p.plugger.Sendf(cmd, "The WolframAlpha servers seem a bit sluggish right now. Please try again soon.")
	}
}

func (p *alphaPlugin) loop() error {
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

type xmlResult struct {
	Success bool     `xml:"success,attr"`
	Error   string   `xml:"error>msg"`
	Pods    []xmlPod `xml:"pod"`
}

type xmlPod struct {
	Id      string      `xml:"id,attr"`
	Title   string      `xml:"title,attr"`
	Primary bool        `xml:"primary,attr"`
	SubPods []xmlSubPod `xml:"subpod"`
}

type xmlSubPod struct {
	Title string `xml:"title,attr"`
	Text  string `xml:"plaintext"`
}

const locCacheLen = 100
const locCacheExpire = 24 * time.Hour

func (p *alphaPlugin) ldapLocation(cmd *mup.Command) string {
	if p.config.LDAP == "" {
		p.plugger.Debugf("No LDAP server configured.")
		return ""
	}

	// Two generations of locCacheLen expiring after locCacheExpire.
	now := time.Now()
	oldest := now.Add(-locCacheExpire)
	entry, ok := p.newLoc[cmd.Nick]
	if ok && entry.when.After(oldest) {
		p.plugger.Debugf("Obtained location for %q from the new cache generation: %q", cmd.Nick, entry.loc)
		return entry.loc
	}
	entry, ok = p.oldLoc[cmd.Nick]
	if ok && entry.when.After(oldest) {
		p.plugger.Debugf("Obtained location for %q from the old cache generation: %q", cmd.Nick, entry.loc)
		p.newLoc[cmd.Nick] = entry
		return entry.loc
	}

	// Not in the cache. Get a connection to look it up.
	conn, err := p.plugger.LDAP(p.config.LDAP)
	if err != nil {
		p.plugger.Logf("Plugin configuration error: %s.", err)
		p.plugger.Sendf(cmd, "Plugin configuration error: %s.", err)
		return ""
	}
	defer conn.Close()

	// Search for the nick in use, and take city, state, and country.
	search := &ldap.Search{
		Filter: fmt.Sprintf("(mozillaNickname=%s)", ldap.EscapeFilter(cmd.Nick)),
		Attrs:  []string{"c", "l", "st"},
	}
	loc := ""
	results, err := conn.Search(search)
	if err != nil {
		p.plugger.Logf("Cannot search LDAP server: %v", err)
		return ""
	}

	// Assemble the string as "city, state, country".
	if len(results) == 0 {
		p.plugger.Logf("Cannot find requested IRC nick in LDAP server: %q", cmd.Nick)
	} else {
		r := results[0]
		for _, name := range search.Attrs {
			if s := r.Value(name); s != "" {
				loc = s
				break
			}
		}
	}

	// Rotate the cache generations if the current one is at the limit.
	if len(p.newLoc) == locCacheLen {
		p.oldLoc = p.newLoc
		p.newLoc = make(map[string]locEntry)
	}

	// Cache successful positive and negative lookups.
	p.newLoc[cmd.Nick] = locEntry{loc, now}
	p.plugger.Debugf("Added location for %q to the cache: %q", cmd.Nick, loc)
	return loc
}

func (p *alphaPlugin) handle(cmd *mup.Command) {
	var args struct {
		Query string
		All   bool
	}
	cmd.Args(&args)

	form := url.Values{
		"appid":         {p.config.AppID},
		"input":         {args.Query},
		"parsetimeout":  {"3"},
		"scantimeout":   {"5"},
		"formattimeout": {"3"},
		"podtimeout":    {"2"},
		"format":        {"plaintext"},
	}
	if loc := p.ldapLocation(cmd); loc != "" {
		form["location"] = []string{loc}
	} else if cmd.Host != "" {
		form["ip"] = []string{cmd.Host}
	}

	req, err := http.NewRequest("GET", p.config.Endpoint, nil)
	if err != nil {
		panic(err)
	}
	req.URL.RawQuery = form.Encode()

	resp, err := httpClient.Do(req)
	if err == nil {
		defer resp.Body.Close()
	}
	if err != nil || resp.StatusCode != 200 {
		p.plugger.Logf("Error on request to WolframAlpha: %v", err)
		p.plugger.Sendf(cmd, "WolframAlpha request failed. Please try again soon.")
		return
	}

	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		p.plugger.Logf("Cannot read WolframAlpha response: %v", err)
		p.plugger.Sendf(cmd, "Cannot read WolframAlpha response: %v", err)
		return
	}

	var result xmlResult
	err = xml.Unmarshal(data, &result)
	if err != nil {
		p.plugger.Logf("Cannot parse WolframAlpha response: %v\nResponse:\n%s", err, data)
		p.plugger.Sendf(cmd, "Cannot parse WolframAlpha response.")
		return
	}

	if result.Error != "" {
		error := strip(result.Error)
		p.plugger.Logf("WolframAlpha reported an error: %s", error)
		p.plugger.Sendf(cmd, "WolframAlpha reported an error: %s", error)
		return
	}

	p.plugger.Debugf("WolframAlpha result:\n%s", data)

	var replied bool
	var buf bytes.Buffer
	if result.Success {
		buf.Grow(256)
	}
	for _, pod := range result.Pods {
		if pod.Id == "Input" || pod.Id == "Illustration" {
			continue
		}
		if !args.All && buf.Len() > 0 && !pod.Primary {
			break
		}
		mark := buf.Len()
		first := true
		for _, subpod := range pod.SubPods {
			text := strip(subpod.Text)
			if text == "" {
				continue
			}
			if first {
				first = false
				if buf.Len() > 0 {
					buf.WriteString(" — ")
				}
				if pod.Title != "" && pod.Title != "Result" && pod.Title != "Results" {
					buf.WriteString(pod.Title)
					buf.WriteString(": ")
				}
			} else {
				if buf.Len() > 0 {
					buf.WriteString("; ")
				}
			}
			if subpod.Title != "" {
				buf.WriteString(subpod.Title)
				buf.WriteString(": ")
			}
			buf.WriteString(text)
		}
		if buf.Len() > mup.MaxTextLen {
			if buf.Len()-mark > mup.MaxTextLen {
				// The pod is too big by itself. Skip it.
				buf.Truncate(mark)
			} else {
				p.send(cmd, string(buf.Next(mark)))
				replied = true
			}
		}
	}
	if buf.Len() > 0 {
		p.send(cmd, buf.String())
		replied = true
	}
	if !replied {
		if result.Success {
			p.plugger.Logf("Unrecognized WolframAlpha result:\n%s", data)
		}
		p.plugger.Sendf(cmd, "Cannot infer much out of this. :-(")
	}
}

var bars = regexp.MustCompile(` \|[| ]* `)
var newlines = regexp.MustCompile(`(?m),?\s*\n[\s\n,]*`)

func (p *alphaPlugin) send(cmd *mup.Command, text string) {
	if strings.Contains(text, " |") {
		text = strings.Replace(text, " |,", ",", -1)
		text = bars.ReplaceAllString(text, " ")
	}
	p.plugger.Sendf(cmd, "%s.", text)
}

func strip(text string) string {
	text = strings.TrimSpace(text)
	if strings.Contains(text, "\n") {
		text = newlines.ReplaceAllString(text, ", ")
	}
	return strings.TrimRight(strings.Join(strings.Fields(text), " "), ".")
}
