package launchpad

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"gopkg.in/niemeyer/mup.v0"
	"gopkg.in/tomb.v2"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

func init() {
	mup.RegisterPlugin("launchpad", startPlugin)
}

var httpClient = http.Client{Timeout: mup.NetworkTimeout}

type lpPlugin struct {
	mu       sync.Mutex
	tomb     tomb.Tomb
	plugger  *mup.Plugger
	messages chan *lpMessage
	settings struct {
		OAuthAccessToken string
		OAuthSecretToken string

		BaseURL       string
		HandleTimeout time.Duration
	}
}

const (
	defaultHandleTimeout = 500 * time.Millisecond
	defaultBaseURL       = "https://api.launchpad.net/1.0/"
)

func startPlugin(plugger *mup.Plugger) mup.Plugin {
	p := &lpPlugin{
		plugger:  plugger,
		messages: make(chan *lpMessage),
	}
	plugger.Settings(&p.settings)
	if p.settings.HandleTimeout == 0 {
		p.settings.HandleTimeout = defaultHandleTimeout
	} else {
		p.settings.HandleTimeout *= time.Millisecond
	}
	if p.settings.BaseURL == "" {
		p.settings.BaseURL = defaultBaseURL
	}
	p.tomb.Go(p.loop)
	return p
}

func (p *lpPlugin) Stop() error {
	p.tomb.Kill(nil)
	return p.tomb.Wait()
}

type lpMessage struct {
	msg  *mup.Message
	bugs []int
}

func (p *lpPlugin) Handle(msg *mup.Message) error {
	bmsg := &lpMessage{msg, parseBugs(msg.Text)}
	if len(bmsg.bugs) == 0 {
		return nil
	}
	select {
	case p.messages <- bmsg:
	case <-time.After(p.settings.HandleTimeout):
		p.plugger.Replyf(msg, "The Launchpad server seems a bit sluggish right now. Please try again soon.")
	}
	return nil
}

func (p *lpPlugin) loop() error {
	for {
		select {
		case bmsg := <-p.messages:
			err := p.handle(bmsg)
			if err != nil {
				mup.Logf("Error talking to Launchpad: %v")
			}
		case <-p.tomb.Dying():
			return nil
		}
	}
	return nil
}

func (p *lpPlugin) handle(bmsg *lpMessage) error {
	for _, id := range bmsg.bugs {
		_ = p.showBug(bmsg.msg, id)
	}
	return nil
}

type lpBug struct {
	Title     string   `json:"title"`
	Tags      []string `json:"tags"`
	TasksLink string   `json:"bug_tasks_collection_link"`
}

type lpBugTasks struct {
	Entries []lpBugEntry `json:"entries"`
}

type lpBugEntry struct {
	Target       string `json:"bug_target_display_name"`
	Status       string `json:"status"`
	AssigneeLink string `json:"assignee_link"`
}

func (p *lpPlugin) showBug(msg *mup.Message, bugId int) error {
	var bug lpBug
	var tasks lpBugTasks
	err := p.request("/bugs/"+strconv.Itoa(bugId), nil, &bug)
	if err != nil {
		return err
	}
	err = p.request(bug.TasksLink, nil, &tasks)
	if err != nil {
		return err
	}
	return p.plugger.Replyf(msg, "Bug #%d: %s%s <https://launchpad.net/bugs/%d>", bugId, bug.Title, p.formatTasks(&tasks), bugId)
}

func (p *lpPlugin) formatTasks(tasks *lpBugTasks) string {
	var buf bytes.Buffer
	buf.Grow(256)
	for _, entry := range tasks.Entries {
		buf.WriteString(" <")
		buf.WriteString(entry.Target)
		buf.WriteString(":")
		buf.WriteString(entry.Status)
		if i := strings.Index(entry.AssigneeLink, "~"); i > 0 {
			if entry.Status == "New" || entry.Status == "Confirmed" {
				buf.WriteString(" for ")
			} else {
				buf.WriteString(" by ")
			}
			buf.WriteString(entry.AssigneeLink[i:])
		}
		buf.WriteString(">")
	}
	return buf.String()
}

func (p *lpPlugin) request(url string, form url.Values, result interface{}) error {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = p.settings.BaseURL + url
	}
	resp, err := httpClient.Get(url + "?" + form.Encode())
	if err != nil {
		mup.Logf("Cannot perform Launchpad request: %v", err)
		return fmt.Errorf("cannot perform Launchpad request: %v", err)
	}
	defer resp.Body.Close()
	err = json.NewDecoder(resp.Body).Decode(result)
	if err != nil {
		mup.Logf("Cannot decode Launchpad response: %v", err)
		return fmt.Errorf("cannot decode Launchpad response: %v", err)
	}
	return nil
}

var bugre = regexp.MustCompile(`(?i)(?:bugs?[ /]#?([0-9]+)|(?:^|\W)#([0-9]{5,}))`)

func parseBugs(text string) []int {
	var bugs []int
	for _, match := range bugre.FindAllStringSubmatch(text, -1) {
		s := match[1]
		if s == "" {
			s = match[2]
		}
		id, err := strconv.Atoi(s)
		if err != nil {
			panic("bug id not an int, which must never happen; regexp is broken")
		}
		bugs = append(bugs, id)
	}
	return bugs
}
