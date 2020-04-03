package launchpad

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/mup.v0"
	"gopkg.in/mup.v0/schema"
	"gopkg.in/tomb.v2"
	"io/ioutil"
	"math/rand"
)

var Plugins = []mup.PluginSpec{{
	Name: "lpbugdata",
	Help: `Reports metadata about Launchpad bugs via a command or overhearing conversations.

	By default the plugin only provides bug metadata via the "bug" command. If the "overhear"
	configuration option is true for the whole plugin or for a specific plugin target, the
	bot will also search third-party conversations for text similar to "#12345", "bug 12",
	or "/+bug/123". Entries such as "RT#123" or "#12" alone (no bug prefix and under 10000)
	are ignored.
	`,
	Start:    startBugData,
	Commands: BugDataCommands,
}, {
	Name:  "lpbugwatch",
	Help:  "Shows status changes on bugs for a selected Launchpad project.",
	Start: startBugWatch,
}, {
	Name:  "lpmergewatch",
	Help:  "Shows status changes on merges for a selected Launchpad project.",
	Start: startMergeWatch,
}, {
	Name:     "lpcontrib",
	Help:     "Offers a command for listing people that signed the contributor agreement.",
	Start:    startContribInfo,
	Commands: ContribCommands,
}}

var BugDataCommands = schema.Commands{{
	Name: "bug",
	Help: `Displays details of the provided Launchpad bugs.

	This command reports details about the provided bug numbers or URLs. The plugin it
	is part of (lpbugdata) can also overhear third-party conversations for bug patterns
	and report bugs mentioned.
	`,
	Args: schema.Args{{
		Name: "bugs",
		Flag: schema.Trailing,
	}},
}}

var ContribCommands = schema.Commands{{
	Name: "contrib",
	Help: `Searches for contributors that have signed the contributor agreement. `,
	Args: schema.Args{{
		Name: "text",
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
	bugData pluginMode = iota + 1
	bugWatch
	mergeWatch
	contribInfo
)

type lpPlugin struct {
	mode pluginMode

	mu       sync.Mutex
	tomb     tomb.Tomb
	plugger  *mup.Plugger
	messages chan *lpMessage
	config   struct {
		OAuthAccessToken string
		OAuthSecretToken string

		AuthCookie string

		Endpoint        string
		BugListEndpoint string
		Project         string
		Overhear        bool
		Options         string
		PrefixNew       string
		PrefixOld       string

		JustShownTimeout mup.DurationString
		PollDelay        mup.DurationString
	}

	overhear map[mup.Address]bool

	justShownList [30]justShownBug
	justShownNext int

	rand *rand.Rand
}

type justShownBug struct {
	id   int
	addr mup.Address
	when time.Time
}

const (
	defaultEndpoint         = "https://api.launchpad.net/1.0/"
	defaultBugListEndpoint  = "https://launchpad.net/"
	defaultPollDelay        = 3 * time.Minute
	defaultJustShownTimeout = 1 * time.Minute
	defaultPrefixNew        = "Bug #%v opened"
	defaultPrefixOld        = "Bug #%v changed"
)

func startBugData(plugger *mup.Plugger) mup.Stopper {
	return startPlugin(bugData, plugger)
}
func startBugWatch(plugger *mup.Plugger) mup.Stopper {
	return startPlugin(bugWatch, plugger)
}
func startMergeWatch(plugger *mup.Plugger) mup.Stopper {
	return startPlugin(mergeWatch, plugger)
}
func startContribInfo(plugger *mup.Plugger) mup.Stopper {
	return startPlugin(contribInfo, plugger)
}

func startPlugin(mode pluginMode, plugger *mup.Plugger) mup.Stopper {
	if mode == 0 {
		panic("launchpad plugin used under unknown mode: " + plugger.Name())
	}
	p := &lpPlugin{
		mode:     mode,
		plugger:  plugger,
		messages: make(chan *lpMessage, 10),
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
	if p.config.BugListEndpoint == "" {
		p.config.BugListEndpoint = defaultBugListEndpoint
	}
	if p.config.PrefixNew == "" {
		p.config.PrefixNew = defaultPrefixNew
	}
	if p.config.PrefixOld == "" {
		p.config.PrefixOld = defaultPrefixOld
	}

	if p.mode == bugData {
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
	case bugData, contribInfo:
		p.tomb.Go(p.loop)
	case bugWatch:
		p.tomb.Go(p.pollBugs)
	case mergeWatch:
		p.tomb.Go(p.pollMerges)
	default:
		panic("internal error: unknown launchpad plugin mode")
	}
	return p
}

func (p *lpPlugin) Stop() error {
	close(p.messages)
	p.tomb.Kill(nil)
	return p.tomb.Wait()
}

type lpMessage struct {
	msg  *mup.Message
	cmd  *mup.Command
	bugs []int
}

func (p *lpPlugin) HandleMessage(msg *mup.Message) {
	if p.mode != bugData || msg.BotText != "" || !p.overhear[p.plugger.Target(msg).Address()] {
		return
	}
	bugs := parseBugChat(msg.Text)
	if len(bugs) == 0 {
		return
	}
	p.handleMessage(&lpMessage{msg, nil, bugs}, false)
}

func (p *lpPlugin) HandleCommand(cmd *mup.Command) {
	var bugs []int
	if p.mode == bugData {
		var args struct{ Bugs string }
		var err error
		cmd.Args(&args)
		bugs, err = parseBugArgs(args.Bugs)
		if err != nil {
			p.plugger.Sendf(cmd, "Oops: %v", err)
			return
		}
	}
	p.handleMessage(&lpMessage{cmd.Message, cmd, bugs}, true)
}

func (p *lpPlugin) handleMessage(lpmsg *lpMessage, reportError bool) {
	select {
	case p.messages <- lpmsg:
	default:
		p.plugger.Logf("Message queue is full. Dropping message: %s", lpmsg.msg.String())
		if reportError {
			p.plugger.Sendf(lpmsg.msg, "The Launchpad server seems a bit sluggish right now. Please try again soon.")
		}
	}
}

func (p *lpPlugin) loop() error {
	for {
		lpmsg, ok := <-p.messages
		if !ok {
			break
		}
		p.handle(lpmsg)
	}
	return nil
}

func (p *lpPlugin) handle(lpmsg *lpMessage) {
	if p.mode == bugData {
		overheard := lpmsg.msg.BotText == ""
		addr := lpmsg.msg.Address()
		for _, id := range lpmsg.bugs {
			if overheard && p.justShown(addr, id) {
				continue
			}
			p.showBug(lpmsg.msg, id, "")
		}
	} else {
		var args struct{ Text string }
		lpmsg.cmd.Args(&args)
		p.showContrib(lpmsg.msg, args.Text)
	}
}

func (p *lpPlugin) justShown(addr mup.Address, bugId int) bool {
	oldest := time.Now().Add(-p.config.JustShownTimeout.Duration)
	for _, shown := range p.justShownList {
		if shown.id == bugId && shown.when.After(oldest) && shown.addr.Contains(addr) {
			return true
		}
	}
	return false
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

func (p *lpPlugin) showBug(msg *mup.Message, bugId int, prefix string) {
	var bug lpBug
	var tasks lpBugTasks
	err := p.request("/bugs/"+strconv.Itoa(bugId), &bug)
	if err != nil {
		if msg != nil && msg.BotText != "" {
			if err == errNotFound {
				p.plugger.Sendf(msg, "Bug not found.")
			} else {
				p.plugger.Sendf(msg, "Oops: %v", err)
			}
		}
		return
	}
	if bug.TasksLink != "" {
		err = p.request(bug.TasksLink, &tasks)
		if err != nil {
			if msg != nil && msg.BotText != "" {
				p.plugger.Sendf(msg, "Oops: %v", err)
			}
			return
		}
	}
	if !strings.Contains(prefix, "%v") || strings.Count(prefix, "%") > 1 {
		prefix = "Bug #%v"
	}
	format := prefix + ": %s%s <https://launchpad.net/bugs/%d>"
	args := []interface{}{bugId, bug.Title, p.formatNotes(&bug, &tasks), bugId}
	switch {
	case msg == nil:
		p.plugger.Broadcastf(format, args...)
	case msg.BotText == "":
		p.plugger.SendChannelf(msg, format, args...)
		addr := msg.Address()
		if addr.Channel != "" {
			addr.Nick = ""
		}
		p.justShownList[p.justShownNext] = justShownBug{bugId, addr, time.Now()}
		p.justShownNext = (p.justShownNext + 1) % len(p.justShownList)
	default:
		p.plugger.Sendf(msg, format, args...)
	}
}

func (p *lpPlugin) showManyBugs(bugIds []int, prefix string) {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, prefix, "")
	buf.WriteString(": ")
	for i, bugId := range bugIds {
		if i > 0 {
			buf.WriteString(", ")
		}
		buf.WriteString(strconv.Itoa(bugId))
	}
	p.plugger.Broadcast(&mup.Message{Text: buf.String()})
}

func (p *lpPlugin) formatNotes(bug *lpBug, tasks *lpBugTasks) string {
	var buf bytes.Buffer
	buf.Grow(256)
	for _, tag := range bug.Tags {
		buf.WriteString(" <")
		buf.WriteString(tag)
		buf.WriteString(">")
	}
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
			buf.WriteString(entry.AssigneeLink[i+1:])
		}
		buf.WriteString(">")
	}
	return buf.String()
}

func (p *lpPlugin) authHeader() string {
	nonce := p.rand.Int63()
	timestamp := time.Now().Unix()
	return fmt.Sprintf(``+
		`OAuth realm="https://api.launchpad.net",`+
		` oauth_consumer_key="mup",`+
		` oauth_signature_method="PLAINTEXT",`+
		` oauth_token=%q,`+
		` oauth_signature=%q,`+
		` oauth_nonce="%d",`+
		` oauth_timestamp="%d"`,
		p.config.OAuthAccessToken, "&"+p.config.OAuthSecretToken, nonce, timestamp)
}

var errNotFound = fmt.Errorf("resource not found")

func (p *lpPlugin) request(url string, result interface{}) error {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		endpoint := p.config.Endpoint
		if strings.Contains(url, "/+bugs-text") {
			endpoint = p.config.BugListEndpoint
		}
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
		p.plugger.Logf("Cannot perform Launchpad request: %v", err)
		return fmt.Errorf("cannot perform Launchpad request: %v", err)
	}
	if p.config.OAuthAccessToken != "" {
		req.Header.Add("Authorization", p.authHeader())
	}
	if p.config.AuthCookie != "" {
		req.Header.Add("Cookie", "lp="+p.config.AuthCookie)
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
		p.plugger.Logf("Cannot perform Launchpad request: %v", err)
		return fmt.Errorf("cannot perform Launchpad request: %v", err)
	}
	defer resp.Body.Close()
	if strings.Contains(url, "/+bugs-text") {
		data, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			p.plugger.Logf("Cannot read Launchpad response: %v", err)
			return fmt.Errorf("cannot read Launchpad response: %v", err)
		}
		list := parseBugList(string(data))
		*(result.(*[]int)) = list
		return nil
	}
	err = json.NewDecoder(resp.Body).Decode(result)
	if err != nil {
		p.plugger.Logf("Cannot decode Launchpad response: %v", err)
		return fmt.Errorf("cannot decode Launchpad response: %v", err)
	}
	return nil
}

var bugChat = regexp.MustCompile(`(?i)(?:bugs?[ /]#?([0-9]+)|(?:^|\W)#([0-9]{5,}))`)
var bugArg = regexp.MustCompile(`^(?i)(?:.*bugs?/)?#?([0-9]+)$`)

func parseBugChat(text string) []int {
	var bugs []int
	for _, match := range bugChat.FindAllStringSubmatch(text, -1) {
		s := match[1]
		if s == "" {
			s = match[2]
		}
		id, err := strconv.Atoi(s)
		if err != nil {
			panic("bug id not an int, which must never happen (regexp is broken)")
		}
		if !containsInt(bugs, id) {
			bugs = append(bugs, id)
		}
	}
	return bugs
}

func containsInt(ns []int, n int) bool {
	for _, i := range ns {
		if i == n {
			return true
		}
	}
	return false
}

func parseBugArgs(text string) ([]int, error) {
	var bugs []int
	for _, s := range strings.Fields(text) {
		match := bugArg.FindStringSubmatch(s)
		if match == nil {
			return nil, fmt.Errorf("cannot parse bug id from argument: %s", s)
		}
		s := match[1]
		id, err := strconv.Atoi(s)
		if err != nil {
			panic("bug id not an int, which must never happen (regexp is broken)")
		}
		if !containsInt(bugs, id) {
			bugs = append(bugs, id)
		}
	}
	return bugs, nil
}

func parseBugList(data string) []int {
	var bugs []int
	for _, s := range strings.Fields(data) {
		id, err := strconv.Atoi(s)
		if err != nil {
			continue
		}
		bugs = append(bugs, id)
	}
	sort.Ints(bugs)
	return bugs
}

func (p *lpPlugin) pollBugs() error {
	var oldBugs []int
	var first = true
	for {
		select {
		case <-time.After(p.config.PollDelay.Duration):
		case <-p.tomb.Dying():
			return nil
		}

		var newBugs []int
		err := p.request("/"+p.config.Project+"/+bugs-text", &newBugs)
		if err != nil {
			continue
		}

		if first {
			first = false
			oldBugs = newBugs
			continue
		}

		var showNewBugs, showOldBugs []int
		var o, n int
		for o < len(oldBugs) || n < len(newBugs) {
			switch {
			case o == len(oldBugs) || n < len(newBugs) && newBugs[n] < oldBugs[o]:
				showNewBugs = append(showNewBugs, newBugs[n])
				n++
			case n == len(newBugs) || o < len(oldBugs) && oldBugs[o] < newBugs[n]:
				showOldBugs = append(showOldBugs, oldBugs[o])
				o++
			default:
				o++
				n++
				continue
			}
		}
		if len(showOldBugs) > 3 {
			p.showManyBugs(showOldBugs, p.config.PrefixOld)
		} else {
			for _, bugId := range showOldBugs {
				p.showBug(nil, bugId, p.config.PrefixOld)
			}
		}
		if len(showNewBugs) > 3 {
			p.showManyBugs(showNewBugs, p.config.PrefixNew)
		} else {
			for _, bugId := range showNewBugs {
				p.showBug(nil, bugId, p.config.PrefixNew)
			}
		}
		oldBugs = newBugs
	}
	return nil
}

type lpMerges struct {
	Entries []lpMergeEntry
}

type lpMergeEntry struct {
	SelfLink    string `json:"self_link"`
	Status      string `json:"queue_status"`
	Description string `json:"description"`
}

func (e *lpMergeEntry) Id() (id int, ok bool) {
	i := strings.LastIndex(e.SelfLink, "/")
	if i < 0 {
		return 0, false
	}
	id, err := strconv.Atoi(e.SelfLink[i+1:])
	if err != nil {
		return 0, false
	}
	return id, true
}

func (e *lpMergeEntry) URL() (url string, ok bool) {
	i := strings.Index(e.SelfLink, "~")
	if i < 0 {
		return "", false
	}
	return "https://launchpad.net/" + e.SelfLink[i:], true
}

func (p *lpPlugin) pollMerges() error {
	oldMerges := make(map[int]string)
	first := true
	for {
		select {
		case <-time.After(p.config.PollDelay.Duration):
		case <-p.tomb.Dying():
			return nil
		}

		var newMerges lpMerges
		err := p.request("/"+p.config.Project+"?ws.op=getMergeProposals", &newMerges)
		if err != nil {
			continue
		}

		for _, merge := range newMerges.Entries {
			id, ok := merge.Id()
			if !ok || oldMerges[id] == merge.Status {
				continue
			}
			oldMerges[id] = merge.Status
			url, ok := merge.URL()
			if !ok || first {
				continue
			}
			p.plugger.Broadcastf("Merge proposal changed [%s]: %s <%s>", strings.ToLower(merge.Status), firstSentence(merge.Description), url)
		}
		first = false
	}
	return nil
}

func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i > 0 {
		return s[:i+1]
	}
	if i := strings.Index(s, "\n"); i > 0 {
		return s[:i]
	}
	if len(s) > 80 {
		if i := strings.LastIndex(s[:80], " "); i > 0 {
			return s[:i] + " (...)"
		}
		return s[:80] + "(...)"
	}
	return s
}

type lpPersonList struct {
	TotalSize int        `json:"total_size"`
	Start     int        `json:"start"`
	Entries   []lpPerson `json:"entries"`
}

type lpPerson struct {
	Username       string `json:"name"`
	Name           string `json:"display_name"`
	MembershipLink string `json:"memberships_details_collection_link"`
	EmailsLink     string `json:"confirmed_email_addresses_collection_link"`
	EmailLink      string `json:"preferred_email_address_link"`

	Emails []string
}

type lpMembershipList struct {
	TotalSize int            `json:"total_size"`
	Start     int            `json:"start"`
	Entries   []lpMembership `json:"entries"`
}

type lpMembership struct {
	Status   string `json:"status"`
	TeamLink string `json:"team_link"`
}

type lpEmailList struct {
	Entries []lpEmail `json:"entries"`
}

type lpEmail struct {
	Addr string `json:"email"`
}

type lpPersonSlice []lpPerson

func (s lpPersonSlice) Len() int           { return len(s) }
func (s lpPersonSlice) Less(i, j int) bool { return s[i].Name < s[j].Name }
func (s lpPersonSlice) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

func firstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func (p *lpPlugin) showContrib(to mup.Addressable, text string) {
	var people lpPersonList
	err := p.request("/people?ws.op=findPerson&text="+url.QueryEscape(text), &people)
	if err != nil {
		p.plugger.Sendf(to, "Oops: %v", err)
		return
	}
	if people.TotalSize == 0 {
		p.plugger.Sendf(to, "Cannot find anyone matching the search terms.")
		return
	}
	if people.TotalSize > len(people.Entries) || people.TotalSize > 10 {
		p.plugger.Sendf(to, "%d people match the search terms. Please be more specific.", people.TotalSize)
		return
	}
	ch := make(chan *lpPerson)
	for _, person := range people.Entries {
		person := person
		go func() {
			var mships lpMembershipList
			err = p.request(person.MembershipLink, &mships)
			if err != nil {
				p.plugger.Sendf(to, "Cannot retrieve membership information of ~%s from Launchpad: %v", person.Username, err)
				ch <- nil
				return
			}
			for _, mship := range mships.Entries {
				if mship.TeamLink == "https://api.launchpad.net/1.0/~contributor-agreement-canonical" && mship.Status == "Approved" {
					var email lpEmail
					var emails lpEmailList
					var errs = make(chan error)
					go func() { errs <- p.request(person.EmailLink, &email) }()
					go func() { errs <- p.request(person.EmailsLink, &emails) }()
					if err := firstErr(<-errs, <-errs); err != nil {
						p.plugger.Sendf(to, "Cannot retrieve email information of ~%s from Launchpad: %v", person.Username, err)
					}
					if email.Addr != "" {
						person.Emails = append(person.Emails, email.Addr)
					}
					for _, email := range emails.Entries {
						person.Emails = append(person.Emails, email.Addr)
					}
					ch <- &person
					return
				}
			}
			ch <- nil
		}()
	}

	contribs := make(lpPersonSlice, 0, len(people.Entries))
	for range people.Entries {
		if contrib := <-ch; contrib != nil {
			contribs = append(contribs, *contrib)
		}
	}
	sort.Sort(contribs)

	if len(contribs) == 0 {
		if people.TotalSize == 1 {
			p.plugger.Sendf(to, "One person matches the search terms, but the agreement was not signed.")
		} else {
			p.plugger.Sendf(to, "%d people match the search terms, but none of them have signed the agreement.", people.TotalSize)
		}
		return
	}
	if len(contribs) == 1 {
		p.plugger.Sendf(to, "One matching contributor signed the agreement:")
	} else {
		p.plugger.Sendf(to, "%d matching contributors signed the agreement:", len(contribs))
	}
	var buf bytes.Buffer
	buf.Grow(128)
	for _, contrib := range contribs {
		p.plugger.Sendf(to, p.formatContrib(&buf, contrib))
	}
}

func (p *lpPlugin) formatContrib(buf *bytes.Buffer, contrib lpPerson) string {
	buf.Truncate(0)
	fmt.Fprintf(buf, " — %s <https://launchpad.net/~%s>", contrib.Name, contrib.Username)
	for _, email := range contrib.Emails {
		buf.WriteString(" <")
		buf.WriteString(email)
		buf.WriteString(">")
	}
	return buf.String()
}
