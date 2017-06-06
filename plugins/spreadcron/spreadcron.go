package spreadcron

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"strings"

	mup "gopkg.in/mup.v0"
	"gopkg.in/mup.v0/schema"
)

var Plugin = mup.PluginSpec{
	Name:     "spreadcron",
	Help:     "Allows to manage spread-cron.",
	Start:    start,
	Commands: Commands,
}

var Commands = schema.Commands{{
	Name: "spreadcron",
	Help: "Describes the allowed commands.",
	Args: schema.Args{{
		Name: "text",
		Flag: schema.Trailing | schema.Required,
	}},
}}

func init() {
	mup.RegisterPlugin(&Plugin)
}

type spreadcronPlugin struct {
	plugger *mup.Plugger
	config  struct {
		Endpoint string
		Username string
		Token    string
		Allowed  []string
		Project  string
	}
}

type Branch struct {
	Name   string      `json:"name"`
	Commit interface{} `json:"commit"`
}

type Branches []Branch

type Content struct {
	Sha string `json:"sha"`
}

type Committer struct {
	Name  string
	Email string
}

type Payload struct {
	Message   string
	Content   string
	Committer *Committer
	Branch    string
	Sha       string
}

var httpClient = http.Client{Timeout: mup.NetworkTimeout}

const (
	defaultEndpoint = "https://api.github.com"
)

func start(plugger *mup.Plugger) mup.Stopper {
	p := &spreadcronPlugin{plugger: plugger}
	plugger.Config(&p.config)
	return p
}

func (p *spreadcronPlugin) Stop() error {
	return nil
}

func (p *spreadcronPlugin) HandleCommand(cmd *mup.Command) {
	var args struct{ Text string }
	cmd.Args(&args)

	items := strings.Fields(args.Text)

	var msg string
	var err error
	errMsg := "an error occurred, please try again later"
	switch items[0] {
	case "help":
		msg = `help: this help message
list: show available jobs
trigger: execute the given job`
	case "list":
		msg, err = p.getBranches()
		if err != nil {
			msg = errMsg
		}
	case "trigger":
		if len(items) == 1 {
			msg, err = p.getBranches()
			if err != nil {
				msg = errMsg
			} else {
				msg = "please select a job to trigger:\n" + msg
			}
		} else {
			if p.checkAllowed(cmd.Message.Nick) {
				msg, err = p.trigger(items[1])
				if err != nil {
					msg = errMsg
				}
			} else {
				msg = "I'm afraid I can't do that, Dave. Daiisyy, daisyyyyy"
			}
		}
	default:
		msg = "I'm afraid I can't do that, Dave. Daiisyy, daisyyyyy"
	}
	p.plugger.Sendf(cmd, "%s", msg)
}

func (p *spreadcronPlugin) getBranches() (string, error) {
	body, err := p.get("/repos/" + p.config.Project + "/branches")
	if err != nil {
		return "", err
	}
	results := make([]Branch, 0)
	err = json.Unmarshal(body, &results)
	if err != nil {
		p.plugger.Logf("Cannot decode GH response: %v\n-----\n%s\n-----", err, body)
		return "", fmt.Errorf("cannot decode GH response: %v", err)
	}
	var output []string
	for _, result := range results {
		output = append(output, result.Name)
	}
	return strings.Join(output, "\n"), nil
}

func (p *spreadcronPlugin) trigger(branch string) (string, error) {
	res, err := p.get("/repos/" + p.config.Project + "/contents/triggers?ref=" + branch)

	if err == nil || err.Error() == "resource not found" {
		var sha string
		if err == nil {
			// triggers found, update
			results := &Content{}
			err = json.Unmarshal(res, &results)
			if err != nil {
				return "", err
			}
			sha = results.Sha
		}
		commiter := &Committer{Name: "myname", Email: "myemail"}
		content := "build triggered by mup"
		body := &Payload{
			Message:   content,
			Content:   base64.StdEncoding.EncodeToString([]byte(content)),
			Committer: commiter,
			Branch:    branch,
			Sha:       sha,
		}
		b, err := json.Marshal(body)
		if err != nil {
			return "", err
		}
		_, err = p.put("/repos/"+p.config.Project+"/contents/triggers", string(b))
		if err != nil {
			return "", err
		}
		return branch + " was successfully triggered", nil
	}
	return "", nil
}

func (p *spreadcronPlugin) get(path string) ([]byte, error) {
	return p.do("GET", path, nil)
}

func (p *spreadcronPlugin) put(path, payload string) ([]byte, error) {
	return p.do("PUT", path, strings.NewReader(payload))
}

func (p *spreadcronPlugin) do(verb, path string, b io.Reader) ([]byte, error) {
	if p.config.Endpoint == "" {
		p.config.Endpoint = defaultEndpoint
	}
	url := p.config.Endpoint + path

	req, err := http.NewRequest(verb, url, b)
	if err != nil {
		p.plugger.Logf("Cannot perform GH request: %v", err)
		return nil, fmt.Errorf("cannot perform GH request: %v", err)
	}
	req.SetBasicAuth(p.config.Username, p.config.Token)

	resp, err := httpClient.Do(req)
	if err == nil && resp.StatusCode == 404 {
		resp.Body.Close()
		return nil, fmt.Errorf("resource not found")
	}
	if err == nil && resp.StatusCode/100 != 2 {
		resp.Body.Close()
		err = fmt.Errorf("%s", resp.Status)
	}
	if err != nil {
		if resp != nil {
			data, _ := ioutil.ReadAll(resp.Body)
			if len(data) > 0 {
				p.plugger.Logf("Cannot perform GH request: %v\nGH response: %s", err, data)
			} else {
				p.plugger.Logf("Cannot perform GH request: %v", err)
			}
		}
		return nil, fmt.Errorf("cannot perform GH request: %v", err)
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		p.plugger.Logf("Cannot read GH response: %v", err)
		return nil, fmt.Errorf("cannot read GH response: %v", err)
	}
	return body, nil
}

func (p *spreadcronPlugin) checkAllowed(nick string) bool {
	for _, allowed := range p.config.Allowed {
		if allowed == nick {
			return true
		}
	}
	return false
}
