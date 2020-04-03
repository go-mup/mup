package github

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/mup.v0"
	"gopkg.in/mup.v0/schema"
	"gopkg.in/tomb.v2"
	"io/ioutil"
)

var Plugins = []mup.PluginSpec{{
	Name: "ghissuedata",
	Help: `Reports metadata about GitHub issues via a command or overhearing conversations.

	By default the plugin only provides bug metadata via the "issue" command. If the "overhear"
	configuration option is true for the whole plugin or for a specific plugin target, the
	bot will also search third-party conversations for text similar to "#123", or "repo#123",
	or "org/repo#123". The simpler syntax only works if the "project" configuration option is
	set to "<organization>" or "<organization>/<repository>".
	`,
	Start:    startIssueData,
	Commands: BugDataCommands,
}, {
	Name:  "ghissuewatch",
	Help:  "Shows status changes on issues and pull requests for a selected GitHub repository.",
	Start: startIssueWatch,
}}

var BugDataCommands = schema.Commands{{
	Name: "issue",
	Help: `Displays details of the provided GitHub issues.

	This command reports details about the provided issue numbers. The plugin it
	is part of (ghissuedata) can also overhear third-party conversations for issue
	patterns and report issues mentioned.
	`,
	Args: schema.Args{{
		Name: "issues",
		Flag: schema.Trailing,
	}},
}}

func init() {
	for i := range Plugins {
		mup.RegisterPlugin(&Plugins[i])
	}
}

var httpClient = http.Client{Timeout: mup.NetworkTimeout}

type pluginMode int

const (
	issueData pluginMode = iota + 1
	issueWatch
)

type ghPlugin struct {
	mode pluginMode

	mu       sync.Mutex
	tomb     tomb.Tomb
	plugger  *mup.Plugger
	messages chan *ghMessage
	config   struct {
		OAuthAccessToken string

		Endpoint string
		Project  string
		Overhear bool
		Options  string

		TrimProject string

		PrefixNewIssue string
		PrefixOldIssue string
		PrefixNewPull  string
		PrefixOldPull  string

		JustShownTimeout mup.DurationString
		PollDelay        mup.DurationString
	}

	overhear map[mup.Address]bool

	justShownList [30]justShownIssue
	justShownNext int

	rand *rand.Rand
}

type justShownIssue struct {
	org  string
	repo string
	num  int
	addr mup.Address
	when time.Time
}

const (
	defaultEndpoint         = "https://api.github.com/"
	defaultPollDelay        = 3 * time.Minute
	defaultJustShownTimeout = 1 * time.Minute
	defaultPrefixNewIssue   = "Issue %v opened"
	defaultPrefixOldIssue   = "Issue %v closed"
	defaultPrefixNewPull    = "PR %v opened"
	defaultPrefixOldPull    = "PR %v closed"
)

func startIssueData(plugger *mup.Plugger) mup.Stopper {
	return startPlugin(issueData, plugger)
}

func startIssueWatch(plugger *mup.Plugger) mup.Stopper {
	return startPlugin(issueWatch, plugger)
}

func startPlugin(mode pluginMode, plugger *mup.Plugger) mup.Stopper {
	if mode == 0 {
		panic("github plugin used under unknown mode: " + plugger.Name())
	}
	p := &ghPlugin{
		mode:     mode,
		plugger:  plugger,
		messages: make(chan *ghMessage, 10),
		overhear: make(map[mup.Address]bool),
		rand:     rand.New(rand.NewSource(time.Now().Unix())),
	}
	err := plugger.UnmarshalConfig(&p.config)
	if err != nil {
		plugger.Logf("%v", err)
	}
	if p.config.PollDelay.Duration == 0 {
		p.config.PollDelay.Duration = defaultPollDelay
	}
	if p.config.JustShownTimeout.Duration == 0 {
		p.config.JustShownTimeout.Duration = defaultJustShownTimeout
	}
	if p.config.Endpoint == "" {
		p.config.Endpoint = defaultEndpoint
	}
	if p.config.TrimProject == "" {
		p.config.TrimProject = p.config.Project
	}
	if p.config.PrefixNewIssue == "" {
		p.config.PrefixNewIssue = defaultPrefixNewIssue
	}
	if p.config.PrefixOldIssue == "" {
		p.config.PrefixOldIssue = defaultPrefixOldIssue
	}
	if p.config.PrefixNewPull == "" {
		p.config.PrefixNewPull = defaultPrefixNewPull
	}
	if p.config.PrefixOldPull == "" {
		p.config.PrefixOldPull = defaultPrefixOldPull
	}

	if p.mode == issueData {
		targets := plugger.Targets()
		for i := range targets {
			var tconfig struct{ Overhear bool }
			target := &targets[i]
			err := target.UnmarshalConfig(&tconfig)
			if err != nil {
				plugger.Logf("%v", err)
			}
			if p.config.Overhear || tconfig.Overhear {
				p.overhear[target.Address()] = true
			}
		}
	}

	switch p.mode {
	case issueData:
		p.tomb.Go(p.loop)
	case issueWatch:
		p.tomb.Go(p.pollIssues)
	default:
		panic("internal error: unknown github plugin mode")
	}
	return p
}

func (p *ghPlugin) Stop() error {
	close(p.messages)
	p.tomb.Kill(nil)
	return p.tomb.Wait()
}

type ghMessage struct {
	msg    *mup.Message
	cmd    *mup.Command
	issues []*ghIssue
}

func (p *ghPlugin) HandleMessage(msg *mup.Message) {
	if p.mode != issueData || msg.BotText != "" || !p.overhear[p.plugger.Target(msg).Address()] {
		return
	}
	issues := p.parseIssueChat(msg.Text)
	if len(issues) == 0 {
		return
	}
	p.handleMessage(&ghMessage{msg, nil, issues}, false)
}

func (p *ghPlugin) HandleCommand(cmd *mup.Command) {
	var issues []*ghIssue
	if p.mode == issueData {
		var args struct{ Issues string }
		var err error
		cmd.Args(&args)
		issues, err = p.parseIssueArgs(args.Issues)
		if err != nil {
			p.plugger.Sendf(cmd, "Oops: %v", err)
			return
		}
	}
	p.handleMessage(&ghMessage{cmd.Message, cmd, issues}, true)
}

func (p *ghPlugin) handleMessage(ghmsg *ghMessage, reportError bool) {
	select {
	case p.messages <- ghmsg:
	default:
		p.plugger.Logf("Message queue is full. Dropping message: %s", ghmsg.msg.String())
		if reportError {
			p.plugger.Sendf(ghmsg.msg, "The GitHub server seems a bit sluggish right now. Please try again soon.")
		}
	}
}

func (p *ghPlugin) loop() error {
	for {
		ghmsg, ok := <-p.messages
		if !ok {
			break
		}
		p.handle(ghmsg)
	}
	return nil
}

func (p *ghPlugin) handle(ghmsg *ghMessage) {
	if p.mode == issueData {
		overheard := ghmsg.msg.BotText == ""
		addr := ghmsg.msg.Address()
		for _, issue := range ghmsg.issues {
			if overheard && p.justShown(addr, issue) {
				continue
			}
			p.showIssue(ghmsg.msg, issue, "")
		}
	}
}

func (p *ghPlugin) justShown(addr mup.Address, issue *ghIssue) bool {
	oldest := time.Now().Add(-p.config.JustShownTimeout.Duration)
	for _, shown := range p.justShownList {
		if shown.org != issue.org || shown.repo != issue.repo || shown.num != issue.Number {
			continue
		}
		if shown.when.After(oldest) && shown.addr.Contains(addr) {
			return true
		}
	}
	return false
}

type ghIssue struct {
	org  string
	repo string

	Title     string    `json:"title"`
	Number    int       `json:"number"`
	RepoURL   string    `json:"repository_url"`
	State     string    `json:"state"`
	Assignees []ghUser  `json:"assignees"`
	Labels    []ghLabel `json:"labels"`
	User      ghUser    `json:"user"`
	ClosedBy  ghUser    `json:"closed_by"`
	Pull      ghPull    `json:"pull_request"`
}

type ghUser struct {
	Login string `json:"login"`
}

type ghLabel struct {
	URL   string `json:"url"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

type ghPull struct {
	Merged    bool
	Mergeable bool
	MergedBy  ghUser `json:"merged_by"`
	HTMLURL   string `json:"html_url"`
}

func (issue *ghIssue) isPull() bool {
	return issue.Pull.HTMLURL != ""
}

func (p *ghPlugin) showIssue(msg *mup.Message, issue *ghIssue, prefix string) {
	err := p.request("/repos/"+issue.org+"/"+issue.repo+"/issues/"+strconv.Itoa(issue.Number), &issue)
	if err != nil {
		if msg != nil && msg.BotText != "" {
			if err == errNotFound {
				p.plugger.Sendf(msg, "Issue not found.")
			} else {
				p.plugger.Sendf(msg, "Oops: %v", err)
			}
		}
		return
	}
	defaultPrefix := "Issue %v"
	what := "issue"
	if issue.isPull() {
		defaultPrefix = "PR %v"
		what = "pull"
		err := p.request("/repos/"+issue.org+"/"+issue.repo+"/pulls/"+strconv.Itoa(issue.Number), &issue.Pull)
		if err != nil {
			if msg != nil && msg.BotText != "" {
				p.plugger.Sendf(msg, "Oops: %v", err)
			}
			return
		}
	}
	if !strings.Contains(prefix, "%v") || strings.Count(prefix, "%") > 1 {
		prefix = defaultPrefix
	}
	issue.Title = strings.TrimRight(issue.Title, ".")
	format := prefix + ": %s%s <https://github.com/%s/%s/%s/%d>"
	args := []interface{}{p.issueKey(issue), issue.Title, p.formatNotes(issue), issue.org, issue.repo, what, issue.Number}
	switch {
	case msg == nil:
		p.plugger.Broadcastf(format, args...)
	case msg.BotText == "":
		p.plugger.SendChannelf(msg, format, args...)
		addr := msg.Address()
		if addr.Channel != "" {
			addr.Nick = ""
		}
		p.justShownList[p.justShownNext] = justShownIssue{issue.org, issue.repo, issue.Number, addr, time.Now()}
		p.justShownNext = (p.justShownNext + 1) % len(p.justShownList)
	default:
		p.plugger.Sendf(msg, format, args...)
	}
}

func (p *ghPlugin) issueKey(issue *ghIssue) string {
	if issue.org+"/"+issue.repo == p.config.TrimProject {
		return fmt.Sprintf("#%d", issue.Number)
	}
	if issue.org == p.config.TrimProject || strings.HasPrefix(p.config.TrimProject, issue.org+"/") {
		return fmt.Sprintf("%s#%d", issue.repo, issue.Number)
	}
	return fmt.Sprintf("%s/%s#%d", issue.org, issue.repo, issue.Number)
}

func (p *ghPlugin) formatNotes(issue *ghIssue) string {
	var buf bytes.Buffer
	buf.Grow(256)
	for _, label := range issue.Labels {
		buf.WriteString(" <")
		buf.WriteString(label.Name)
		buf.WriteString(">")
	}

	fmt.Fprintf(&buf, " <Created by %s>", issue.User.Login)
	if issue.State == "closed" && issue.Pull.Merged {
		fmt.Fprintf(&buf, " <Merged by %s>", issue.Pull.MergedBy.Login)
	} else if issue.State == "closed" {
		fmt.Fprintf(&buf, " <Closed by %s>", issue.ClosedBy.Login)
	}

	return buf.String()
}

var errNotFound = fmt.Errorf("resource not found")

func (p *ghPlugin) request(url string, result interface{}) error {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		endpoint := p.config.Endpoint
		url = strings.TrimRight(endpoint, "/") + "/" + strings.TrimLeft(url, "/")
	}
	if p.config.Options != "" {
		if strings.Contains(url, "?") {
			url += "&" + p.config.Options
		} else {
			url += "?" + p.config.Options
		}
	}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		p.plugger.Logf("Cannot perform GitHub request: %v", err)
		return fmt.Errorf("cannot perform GitHub request: %v", err)
	}
	if p.config.OAuthAccessToken != "" {
		req.Header.Add("Authorization", "token "+p.config.OAuthAccessToken)
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
		data, _ := ioutil.ReadAll(resp.Body)
		if len(data) > 0 {
			p.plugger.Logf("Cannot perform GitHub request: %v\nGitHub response: %s", err, data)
		} else {
			p.plugger.Logf("Cannot perform GitHub request: %v", err)
		}
		return fmt.Errorf("cannot perform GitHub request: %v", err)
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		p.plugger.Logf("Cannot read GitHub response: %v", err)
		return fmt.Errorf("cannot read GitHub response: %v", err)
	}
	err = json.Unmarshal(body, result)
	if err != nil {
		p.plugger.Logf("Cannot decode GitHub response: %v\n-----\n%s\n-----", err, body)
		return fmt.Errorf("cannot decode GitHub response: %v", err)
	}
	if !parseOrgRepo(result) {
		p.plugger.Logf("Cannot parse repository URL on GitHub issue at %s", url)
		return fmt.Errorf("cannot parse repository URL on GitHub issue at %s", url)
	}
	return nil
}

func parseOrgRepo(result interface{}) bool {
	issues, ok := result.(*[]*ghIssue)
	if !ok {
		issue, ok := result.(*ghIssue)
		if !ok {
			return true
		}
		issues = &[]*ghIssue{issue}
	}
	ok = true
	for _, issue := range *issues {
		start := strings.Index(issue.RepoURL, "/repos/")
		if start < 0 {
			ok = false
			continue
		}
		start += len("/repos/")
		slash := strings.Index(issue.RepoURL[start:], "/")
		if slash < 1 {
			ok = false
			continue
		}
		slash += start
		issue.org = issue.RepoURL[start:slash]
		issue.repo = issue.RepoURL[slash+1:]
	}
	return ok
}

var issueChat = regexp.MustCompile(`(?i)((?:issue|pr|pull|pull ?req(?:uest)?)s? )?((?:([a-zA-Z][-a-zA-Z0-9]*)/)?([a-zA-Z][-a-zA-Z0-9]*)(?:/pull/|/issue/|#)|#)?([0-9]+)`)
var issueArg = regexp.MustCompile(`^(?i)(?:(?:([a-zA-Z][-a-zA-Z0-9]*)/)?([a-zA-Z][-a-zA-Z0-9]*)(?:/pull/|/issue/|#)|#)?([0-9]+)$`)

func (p *ghPlugin) parseIssueChat(text string) []*ghIssue {
	var issues []*ghIssue
	for _, match := range issueChat.FindAllStringSubmatch(text, -1) {
		hasPrefix := match[1] != ""
		hasHash := match[2] != ""
		if !hasPrefix && !hasHash {
			continue
		}
		org, repo, ok := p.repository(match[3], match[4])
		if !ok {
			continue
		}
		num, err := strconv.Atoi(match[5])
		if err != nil {
			panic("bug id not an int, which must never happen (regexp is broken)")
		}
		issue := &ghIssue{org: org, repo: repo, Number: num}
		if !containsIssue(issues, issue) {
			issues = append(issues, issue)
		}
	}
	return issues
}

func (p *ghPlugin) parseIssueArgs(text string) ([]*ghIssue, error) {
	var issues []*ghIssue
	for _, s := range strings.Fields(text) {
		match := issueArg.FindStringSubmatch(s)
		if match == nil {
			return nil, fmt.Errorf("cannot parse issue or pull number from argument: %s", s)
		}
		org, repo, ok := p.repository(match[1], match[2])
		if !ok {
			return nil, fmt.Errorf("argument must be formatted as [<org>/][<repo>]#%s", match[3])
		}
		s := match[3]
		num, err := strconv.Atoi(s)
		if err != nil {
			panic("issue number not an int, which must never happen (regexp is broken)")
		}
		issue := &ghIssue{org: org, repo: repo, Number: num}
		if !containsIssue(issues, issue) {
			issues = append(issues, issue)
		}
	}
	return issues, nil
}

func (p *ghPlugin) repository(org, repo string) (neworg, newrepo string, ok bool) {
	if org == "" {
		org = p.config.Project
		if i := strings.Index(org, "/"); i >= 0 {
			org = org[:i]
		}
	}
	if repo == "" {
		if i := strings.Index(p.config.Project, "/"); i >= 0 {
			repo = p.config.Project[i+1:]
		}
	}
	return org, repo, org != "" && repo != ""
}

func containsIssue(issues []*ghIssue, issue *ghIssue) bool {
	for _, i := range issues {
		if i.org == issue.org && i.repo == issue.repo && i.Number == issue.Number {
			return true
		}
	}
	return false
}

func issueNums(issues []*ghIssue) []int {
	var nums []int
	for _, issue := range issues {
		nums = append(nums, issue.Number)
	}
	return nums
}

func (p *ghPlugin) pollIssues() error {
	var oldIssues []*ghIssue
	var first = true
	for {
		select {
		case <-time.After(p.config.PollDelay.Duration):
		case <-p.tomb.Dying():
			return nil
		}

		var newIssues []*ghIssue
		for page := 1; page <= 10; page++ {
			var pageIssues []*ghIssue
			err := p.request("/repos/"+p.config.Project+"/issues?direction=asc&per_page=100&page="+strconv.Itoa(page), &pageIssues)
			if err != nil {
				continue
			}
			// Cut out potential dups due to in-between activity.
			for len(newIssues) > 0 && len(pageIssues) > 0 && newIssues[len(newIssues)-1].Number >= pageIssues[0].Number {
				newIssues = newIssues[:len(newIssues)-1]
			}
			newIssues = append(newIssues, pageIssues...)
			if len(pageIssues) < 100 {
				break
			}
		}

		if first {
			first = false
			oldIssues = newIssues
			continue
		}

		var showNewIssues, showOldIssues []*ghIssue
		var showNewPulls, showOldPulls []*ghIssue
		var o, n int
		for o < len(oldIssues) || n < len(newIssues) {
			switch {
			case o == len(oldIssues) || n < len(newIssues) && newIssues[n].Number < oldIssues[o].Number:
				if newIssues[n].isPull() {
					showNewPulls = append(showNewPulls, newIssues[n])
				} else {
					showNewIssues = append(showNewIssues, newIssues[n])
				}
				n++
			case n == len(newIssues) || o < len(oldIssues) && oldIssues[o].Number < newIssues[n].Number:
				if oldIssues[o].isPull() {
					showOldPulls = append(showOldPulls, oldIssues[o])
				} else {
					showOldIssues = append(showOldIssues, oldIssues[o])
				}
				o++
			default:
				o++
				n++
				continue
			}
		}
		p.showIssues(showOldIssues, p.config.PrefixOldIssue)
		p.showIssues(showNewIssues, p.config.PrefixNewIssue)
		p.showIssues(showOldPulls, p.config.PrefixOldPull)
		p.showIssues(showNewPulls, p.config.PrefixNewPull)

		oldIssues = newIssues
	}
	return nil
}

func (p *ghPlugin) showIssues(issues []*ghIssue, prefix string) {
	if len(issues) > 3 {
		p.showIssueList(issues, prefix)
	} else {
		for _, issue := range issues {
			p.showIssue(nil, issue, prefix)
		}
	}
}

func (p *ghPlugin) showIssueList(issues []*ghIssue, prefix string) {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, prefix, "#")
	buf.WriteString(": ")
	for i, issue := range issues {
		if i > 0 {
			buf.WriteString(", ")
		}
		buf.WriteString(p.issueKey(issue))
	}
	p.plugger.Broadcast(&mup.Message{Text: buf.String()})
}
