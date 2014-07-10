package mup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/niemeyer/mup.v0"
	"gopkg.in/niemeyer/mup.v0/ldap"
	"gopkg.in/tomb.v2"
)

func init() {
	mup.RegisterPlugin("aql", startPlugin)
}

var httpClient = http.Client{Timeout: mup.NetworkTimeout}

type aqlPlugin struct {
	mu       sync.Mutex
	tomb     tomb.Tomb
	plugger  *mup.Plugger
	prefix   string
	messages chan *mup.Message
	smses    chan *smsMessage
	err      error
	settings struct {
		ldap.Settings `bson:",inline"`

		Command    string
		Account    string
		AQLProxy   string
		AQLUser    string
		AQLPass    string
		AQLKeyword string
		AQLGateway string

		HandleTimeout time.Duration
		PollDelay     time.Duration
	}
}

const (
	defaultCommand       = "sms"
	defaultHandleTimeout = 500 * time.Millisecond
	defaultPollDelay     = 3 * time.Second
)

func startPlugin(plugger *mup.Plugger) mup.Plugin {
	p := &aqlPlugin{
		plugger:  plugger,
		prefix:   defaultCommand,
		messages: make(chan *mup.Message),
		smses:    make(chan *smsMessage),
	}
	plugger.Settings(&p.settings)
	if p.settings.Command != "" {
		p.prefix = p.settings.Command
	}
	p.prefix += " "
	if p.settings.HandleTimeout == 0 {
		p.settings.HandleTimeout = defaultHandleTimeout
	} else {
		p.settings.HandleTimeout *= time.Millisecond
	}
	if p.settings.PollDelay == 0 {
		p.settings.HandleTimeout = defaultPollDelay
	} else {
		p.settings.PollDelay *= time.Millisecond
	}
	if p.settings.AQLGateway == "" {
		p.settings.AQLGateway = "https://gw.aql.com/sms/sms_gw.php"
	}
	p.tomb.Go(p.loop)
	p.tomb.Go(p.poll)
	return p
}

func (p *aqlPlugin) Stop() error {
	p.tomb.Kill(nil)
	return p.tomb.Wait()
}

func (p *aqlPlugin) Handle(msg *mup.Message) error {
	if !msg.ToMup || !strings.HasPrefix(msg.MupText, p.prefix) {
		return nil
	}
	select {
	case p.messages <- msg:
	case <-time.After(p.settings.HandleTimeout):
		reply := "The LDAP server seems a bit sluggish right now. Please try again soon."
		p.mu.Lock()
		err := p.err
		p.mu.Unlock()
		if err != nil {
			reply = err.Error()
		}
		p.plugger.Replyf(msg, "%s", reply)
	}
	return nil
}

func (p *aqlPlugin) loop() error {
	for {
		err := p.forward()
		if !p.tomb.Alive() {
			return nil
		}
		p.mu.Lock()
		p.err = err
		p.mu.Unlock()
		for i := 0; i < 10 && p.tomb.Alive(); i++ {
			time.Sleep(500 * time.Millisecond)
		}
	}
}

func (p *aqlPlugin) forward() error {
	conn, err := ldap.Dial(&p.settings.Settings)
	if err != nil {
		p.plugger.Logf("%v", err)
		return err
	}
	defer conn.Close()
	p.mu.Lock()
	p.err = nil
	p.mu.Unlock()
	for err == nil {
		select {
		case msg := <-p.messages:
			err = p.handle(conn, msg)
			if err != nil {
				p.plugger.Replyf(msg, "Error sending SMS: %v", err)
			}
		case sms := <-p.smses:
			err = p.receiveSMS(conn, sms)
		case <-time.After(mup.NetworkTimeout):
			err = conn.Ping()
		case <-p.tomb.Dying():
			err = tomb.ErrDying
		}
	}
	return err
}

func (p *aqlPlugin) handle(conn ldap.Conn, msg *mup.Message) error {
	query := strings.TrimSpace(msg.MupText[len(p.prefix):])
	fields := strings.SplitN(query, " ", 2)
	for i := range fields {
		fields[i] = strings.TrimSpace(fields[i])
	}
	if len(fields) != 2 || len(fields[0]) == 0 || len(fields[1]) == 0 {
		p.plugger.Replyf(msg, "Command looks like: sms <nick> <message>")
		return nil
	}
	nick := fields[0]
	text := fields[1]
	search := &ldap.Search{
		Filter: fmt.Sprintf("(mozillaNickname=%s)", ldap.EscapeFilter(nick)),
		Attrs:  []string{"mozillaNickname", "mobile"},
	}
	results, err := conn.Search(search)
	if err != nil {
		p.plugger.Replyf(msg, "Cannot search LDAP server right now: %v", err)
		return fmt.Errorf("cannot search LDAP server: %v", err)
	}
	if len(results) == 0 {
		p.plugger.Replyf(msg, "Cannot find anyone with that IRC nick in the directory. :-(")
		return nil
	}
	receiver := results[0]
	mobile := receiver.Value("mobile")
	if mobile == "" {
		p.plugger.Replyf(msg, "Person doesn't have a mobile phone in the directory.")
	} else if !strings.HasPrefix(mobile, "+") {
		p.plugger.Replyf(msg, "This person's mobile number is not in international format (+XX...): %s", mobile)
	} else {
		err := p.sendSMS(msg, nick, text, receiver)
		if err != nil {
			p.plugger.Replyf(msg, "Error sending SMS to %s (%s): %v", nick, mobile, err)
		}
	}
	return nil
}

func isChannel(name string) bool {
	return name != "" && (name[0] == '#' || name[0] == '&') && !strings.ContainsAny(name, " ,\x07")
}

func (p *aqlPlugin) sendSMS(msg *mup.Message, nick, text string, receiver ldap.Result) error {
	var content string
	if isChannel(msg.Target) {
		content = fmt.Sprintf("%s %s> %s", msg.Target, msg.Nick, text)
	} else {
		content = fmt.Sprintf("%s> %s", msg.Nick, text)
	}

	// This API is documented at http://aql.com/sms/integrated/sms-api
	mobile := trimPhone(receiver.Value("mobile"))
	form := url.Values{
		"username":    []string{p.settings.AQLUser},
		"password":    []string{p.settings.AQLPass},
		"destination": []string{mobile},
		"originator":  []string{"+447766404142"},
		"message":     []string{content},
	}
	resp, err := httpClient.PostForm(p.settings.AQLGateway, form)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	// Response format is "<status code>:<credits used> <description>".
	// For example: "2:0 Authentication error"
	i := bytes.IndexByte(data, ':')
	j := bytes.IndexByte(data, ' ')
	if i <= 0 || j <= i {
		return fmt.Errorf("AQL response not recognized.")
	}
	status := data[:i]
	credits := data[i+1 : j]
	info := data[j+1:]
	p.plugger.Logf("SMS delivery result: from=%s to=%s mobile=%s status=%s credits=%s info=%s", msg.Nick, nick, mobile, status, credits, info)
	if len(status) == 1 && (status[0] == '0' || status[0] == '1') {
		p.plugger.Replyf(msg, "SMS is on the way!")
	} else {
		p.plugger.Replyf(msg, "SMS delivery failed: %s", info)
	}
	return nil
}

func trimPhone(number string) string {
	buf := make([]byte, len(number)+1)
	buf[0] = '+'
	j := 1
	for _, c := range number {
		if c >= '0' && c <= '9' {
			buf[j] = byte(c)
			j++
		}
		if c == '/' {
			break
		}
	}
	buf = buf[:j]
	if bytes.HasPrefix(buf, []byte("+440")) {
		copy(buf[3:], buf[4:])
		buf = buf[:len(buf)-1]
	}
	return string(buf)
}

type smsMessage struct {
	Key     int    `json:"key"`
	Message string `json:"message"`
	Sender  string `json:"sender"`
	Time    string `json:"time"`
}

func (p *aqlPlugin) poll() error {
	form := url.Values{
		"keyword": []string{p.settings.AQLKeyword},
	}
	for {
		time.Sleep(p.settings.PollDelay)
		if !p.tomb.Alive() {
			return nil
		}
		resp, err := httpClient.Get(p.settings.AQLProxy + "/retrieve?" + form.Encode())
		if err != nil {
			p.plugger.Logf("Cannot retrieve SMSes from AQL proxy: %v", err)
			continue
		}
		defer resp.Body.Close()
		var smses []smsMessage
		err = json.NewDecoder(resp.Body).Decode(&smses)
		if err != nil {
			p.plugger.Logf("Cannot decode AQL proxy response: %v", err)
			continue
		}
		for i := range smses {
			smses[i].Sender = "+" + smses[i].Sender
			select {
			case p.smses <- &smses[i]:
			case <-p.tomb.Dying():
				return nil
			}
		}
	}
	return nil
}

func (p *aqlPlugin) receiveSMS(conn ldap.Conn, sms *smsMessage) error {
	query := strings.TrimSpace(sms.Message)
	fields := strings.SplitN(query, " ", 2)
	for i := range fields {
		fields[i] = strings.TrimSpace(fields[i])
	}
	if len(fields) != 2 || len(fields[0]) == 0 || len(fields[1]) == 0 {
		p.plugger.Logf("Received invalid SMS message text: %q", sms.Message)
		return nil
	}
	target := fields[0]
	text := fields[1]

	number := trimPhone(sms.Sender)[1:]
	numberQuery := make([]byte, len(number)*2+1)
	numberQuery[0] = '*'
	for i, c := range number {
		numberQuery[i*2+1] = byte(c)
		numberQuery[i*2+2] = '*'
	}
	search := &ldap.Search{
		Filter: fmt.Sprintf("(mobile=%s)", string(numberQuery)),
		Attrs:  []string{"mozillaNickname"},
	}
	sender := sms.Sender
	results, err := conn.Search(search)
	if err != nil {
		p.plugger.Logf("Cannot search LDAP server for SMS sender: %v", err)
	} else if len(results) > 0 {
		nick := results[0].Value("mozillaNickname")
		if nick != "" {
			sender = nick
		}
	}
	msg := mup.Message{
		Account: p.settings.Account,
		Target:  target,
		Text:    fmt.Sprintf("[SMS] <%s> %s", sender, text),
	}
	p.plugger.Logf("[%s] Delivering SMS from %s (%s) to %s: %s\n", p.settings.Account, sender, sms.Sender, target, text)
	err = p.plugger.Send(&msg)
	if err == nil {
		p.tomb.Go(func() error {
			_ = p.deleteSMS(sms)
			return nil
		})
	}
	if !strings.HasPrefix(sender, "+") {
		p.plugger.Sendf(p.settings.Account, target, "Answer with: !sms %s <your message>", sender)
	}
	return nil
}

func (p *aqlPlugin) deleteSMS(sms *smsMessage) error {
	form := url.Values{
		"keyword": []string{p.settings.AQLKeyword},
		"keys":    []string{strconv.Itoa(sms.Key)},
	}
	resp, err := httpClient.PostForm(p.settings.AQLProxy+"/delete", form)
	if err != nil {
		p.plugger.Logf("Cannot delete SMS message %s: %v", sms.Key, err)
		return err
	}
	p.plugger.Logf("Delete accepted for %v.", sms.Key)
	resp.Body.Close()
	return nil
}
