package travis

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	"io/ioutil"

	"gopkg.in/mup.v0"
	"gopkg.in/tomb.v2"
)

var Plugin = mup.PluginSpec{
	Name:  "travisbuildwatch",
	Help:  "Shows status and changes of Travis builds for a selected GitHub repository.",
	Start: startBuildWatch,
}

func init() {
	mup.RegisterPlugin(&Plugin)
}

var httpClient = http.Client{Timeout: mup.NetworkTimeout}

type travisPlugin struct {
	tomb    tomb.Tomb
	plugger *mup.Plugger

	config struct {
		Endpoint string
		Project  string

		PollDelay mup.DurationString
	}

	rand *rand.Rand
}

const (
	defaultEndpoint  = "https://api.travis-ci.org/"
	defaultPollDelay = 3 * time.Minute
)

func startBuildWatch(plugger *mup.Plugger) mup.Stopper {
	p := &travisPlugin{
		plugger: plugger,
		rand:    rand.New(rand.NewSource(time.Now().Unix())),
	}
	plugger.Config(&p.config)

	if p.config.PollDelay.Duration == 0 {
		p.config.PollDelay.Duration = defaultPollDelay
	}
	if p.config.Endpoint == "" {
		p.config.Endpoint = defaultEndpoint
	}

	p.tomb.Go(p.pollBuilds)

	return p
}

func (p *travisPlugin) Stop() error {
	p.tomb.Kill(nil)
	return p.tomb.Wait()
}

type travisBuild struct {
	Id      int    `json:"id"`
	State   string `json:"state"`
	Result  *int   `json:"result"`
	Branch  string `json:"branch"`
	Message string `json:"message"`
	Number  int    `json:"number,string"`
}

var errNotFound = fmt.Errorf("resource not found")

func (p *travisPlugin) request(url string, result interface{}) error {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		endpoint := p.config.Endpoint
		url = strings.TrimRight(endpoint, "/") + "/" + strings.TrimLeft(url, "/")
	}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		p.plugger.Logf("Cannot perform Travis request: %v", err)
		return fmt.Errorf("cannot perform Travis request: %v", err)
	}
	resp, err := httpClient.Do(req)
	if err == nil && resp.StatusCode == 404 {
		resp.Body.Close()
		return errNotFound
	}
	if err == nil && resp.StatusCode != 200 {
		resp.Body.Close()
		err = fmt.Errorf("%s", resp.Status)
	}
	if err != nil {
		if resp != nil {
			data, _ := ioutil.ReadAll(resp.Body)
			if len(data) > 0 {
				p.plugger.Logf("Cannot perform Travis request: %v\nTravis response: %s", err, data)
			} else {
				p.plugger.Logf("Cannot perform Travis request: %v", err)
			}
		}
		return fmt.Errorf("cannot perform Travis request: %v", err)
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		p.plugger.Logf("Cannot read Travis response: %v", err)
		return fmt.Errorf("cannot read Travis response: %v", err)
	}
	err = json.Unmarshal(body, result)
	if err != nil {
		p.plugger.Logf("Cannot decode Travis response: %v\n-----\n%s\n-----", err, body)
		return fmt.Errorf("cannot decode Travis response: %v", err)
	}
	return nil
}

func (p *travisPlugin) pollBuilds() error {
	var lastBuildNumber int
	var pendingBuilds []int

	first := true
	for {
		select {
		case <-time.After(p.config.PollDelay.Duration):
		case <-p.tomb.Dying():
			return nil
		}

		var newBuilds []*travisBuild
		var number int
		for page := 1; page <= 10; page++ {
			var pageBuilds []*travisBuild
			url := "/repos/" + p.config.Project + "/builds"
			if page > 1 && len(newBuilds) > 0 {
				number = newBuilds[len(newBuilds)-1].Number
				url += "?after_number=" + strconv.Itoa(number)
			}
			err := p.request(url, &pageBuilds)
			if err != nil {
				continue
			}
			newBuilds = append(newBuilds, pageBuilds...)

			if len(pageBuilds) < 25 || number == 1 {
				break
			}
		}
		if first && len(newBuilds) > 0 {
			// initialize last build number and pending list
			lastBuildNumber = newBuilds[0].Number

			for _, build := range newBuilds {
				if build.State != "finished" {
					pendingBuilds = append(pendingBuilds, build.Number)
				}
			}
			first = false
		}

		var currentNumber int
		for n := len(newBuilds) - 1; n >= 0; n-- {
			currentNumber = newBuilds[n].Number
			if currentNumber > lastBuildNumber {
				lastBuildNumber = currentNumber
				if newBuilds[n].State == "finished" {
					p.showBuild(newBuilds[n])
				} else {
					pendingBuilds = append([]int{currentNumber}, pendingBuilds...)
				}
			} else if newBuilds[n].State == "finished" {
				for index, pendingBuildNumber := range pendingBuilds {
					if pendingBuildNumber == currentNumber {
						pendingBuilds = append(pendingBuilds[:index], pendingBuilds[index+1:]...)
						p.showBuild(newBuilds[n])
						break
					}
				}
			}
		}
	}
}

func (p *travisPlugin) showBuild(build *travisBuild) {

	// check if the notification must be skipped
	if strings.Contains(build.Message, "<skip notify>") {
		return
	}

	var args []interface{}

	var result string
	if build.Result == nil {
		result = "ERRORED"
	} else if *build.Result == 0 {
		result = "passed"
	} else {
		result = "FAILED"
	}
	format := "Travis build %s: %s <project: %s> <branch: %s> <https://travis-ci.org/%s/builds/%d>"
	args = []interface{}{result, build.Message, p.config.Project, build.Branch, p.config.Project, build.Id}

	p.plugger.Broadcastf(format, args...)
}
