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

	"gopkg.in/mgo.v2/bson"
	"gopkg.in/mup.v0"
	"gopkg.in/mup.v0/ldap"
	"gopkg.in/mup.v0/schema"
	"gopkg.in/tomb.v2"
)

var Plugin = mup.PluginSpec{
	Name: "aql",
	Help: `Integrates the bot with AQL's SMS delivery gateway.

	The configured LDAP directory is queried for a person with the
	provided IRC nick (mozillaNickname) and a phone (mobile) in
	international format (+NN...). The message sender must also be
	registered in the LDAP directory with the IRC nick in use.

	The plugin also allows people to send SMS messages into IRC on
	one of the configured plugin targets. The message must be
	addressed to AQL's shared number in the UK (+447766404142) and
	have the format "<keyword> <nick or channel> <message>". The
	keyword must be reserved via AQL's interface and informed in
	the plugin configuration.

	Incoming SMS messages first go to custom HTTP server that acts
	as a proxy, receiving messages pushed from AQL via HTTP, and
	storing them until the plugin pulls the message and forwards
	it to the appropriate account. The role of that proxy is
	offering an increased availability to reduce the chances of
	AQL's HTTP requests ever getting lost.
	`,
	Start:    start,
	Commands: Commands,
}

var Commands = schema.Commands{{
	Name: "sms",
	Help: "Sends an SMS message.",
	Args: schema.Args{{
		Name: "nick",
		Help: "IRC nick of the person whose phone number should receive the SMS message.",
		Flag: schema.Required,
	}, {
		Name: "message",
		Help: "Message to be sent.",
		Flag: schema.Required | schema.Trailing,
	}},
}}

func init() {
	mup.RegisterPlugin(&Plugin)
}

var httpClient = http.Client{Timeout: mup.NetworkTimeout}

type aqlPlugin struct {
	mu       sync.Mutex
	tomb     tomb.Tomb
	plugger  *mup.Plugger
	commands chan *mup.Command
	smses    chan *smsMessage
	err      error
	config   struct {
		ldap.Config `bson:",inline"`

		AQLProxy   string
		AQLUser    string
		AQLPass    string
		AQLKeyword string
		AQLGateway string

		HandleTimeout bson.DurationString
		PollDelay     bson.DurationString
	}
}

const (
	defaultHandleTimeout = 500 * time.Millisecond
	defaultPollDelay     = 10 * time.Second
)

func start(plugger *mup.Plugger) (mup.Stopper, error) {
	p := &aqlPlugin{
		plugger:  plugger,
		commands: make(chan *mup.Command),
		smses:    make(chan *smsMessage),
	}
	plugger.Config(&p.config)
	if p.config.HandleTimeout.Duration == 0 {
		p.config.HandleTimeout.Duration = defaultHandleTimeout
	}
	if p.config.PollDelay.Duration == 0 {
		p.config.PollDelay.Duration = defaultPollDelay
	}
	if p.config.AQLGateway == "" {
		p.config.AQLGateway = "https://gw.aql.com/sms/sms_gw.php"
	}
	p.tomb.Go(p.loop)
	return p, nil
}

func (p *aqlPlugin) Stop() error {
	p.tomb.Kill(nil)
	return p.tomb.Wait()
}

func (p *aqlPlugin) HandleCommand(cmd *mup.Command) error {
	select {
	case p.commands <- cmd:
	case <-time.After(p.config.HandleTimeout.Duration):
		reply := "The LDAP server seems a bit sluggish right now. Please try again soon."
		p.mu.Lock()
		err := p.err
		p.mu.Unlock()
		if err != nil {
			reply = err.Error()
		}
		p.plugger.Sendf(cmd, "%s", reply)
	}
	return nil
}

func (p *aqlPlugin) loop() error {
	p.tomb.Go(p.poll)
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
	conn, err := ldap.Dial(&p.config.Config)
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
		case cmd := <-p.commands:
			p.handle(conn, cmd)
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

func (p *aqlPlugin) handle(conn ldap.Conn, cmd *mup.Command) {
	var args struct{ Nick, Message string }
	cmd.Args(&args)
	search := &ldap.Search{
		Filter: fmt.Sprintf("(mozillaNickname=%s)", ldap.EscapeFilter(args.Nick)),
		Attrs:  []string{"mozillaNickname", "mobile"},
	}
	results, err := conn.Search(search)
	if err != nil {
		p.plugger.Logf("Cannot search LDAP server: %v", err)
		p.plugger.Sendf(cmd, "Cannot search LDAP server right now: %v", err)
		return
	}
	if len(results) == 0 {
		p.plugger.Logf("Cannot find requested query in LDAP server: %q", args.Nick)
		p.plugger.Sendf(cmd, "Cannot find anyone with that IRC nick in the directory. :-(")
		return
	}
	receiver := results[0]
	mobile := receiver.Value("mobile")
	if mobile == "" {
		p.plugger.Sendf(cmd, "Person doesn't have a mobile phone in the directory.")
	} else if !strings.HasPrefix(mobile, "+") {
		p.plugger.Sendf(cmd, "This person's mobile number is not in international format (+NN...): %s", mobile)
	} else {
		err := p.sendSMS(cmd, args.Nick, args.Message, receiver)
		if err != nil {
			p.plugger.Logf("Error sending SMS to %s (%s): %v", args.Nick, mobile, err)
			p.plugger.Sendf(cmd, "Error sending SMS to %s (%s): %v", args.Nick, mobile, err)
		}
	}
}

func isChannel(name string) bool {
	return name != "" && (name[0] == '#' || name[0] == '&') && !strings.ContainsAny(name, " ,\x07")
}

func (p *aqlPlugin) sendSMS(cmd *mup.Command, nick, message string, receiver ldap.Result) error {
	var content string
	if cmd.Channel != "" {
		content = fmt.Sprintf("%s %s> %s", cmd.Channel, cmd.Nick, message)
	} else {
		content = fmt.Sprintf("%s> %s", cmd.Nick, message)
	}

	// This API is documented at http://aql.com/sms/integrated/sms-api
	mobile := trimPhone(receiver.Value("mobile"))
	form := url.Values{
		"username":    []string{p.config.AQLUser},
		"password":    []string{p.config.AQLPass},
		"destination": []string{mobile},
		"originator":  []string{"+447766404142"},
		"message":     []string{content},
	}
	resp, err := httpClient.PostForm(p.config.AQLGateway, form)
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
	p.plugger.Logf("SMS delivery result: from=%s to=%s mobile=%s status=%s credits=%s info=%s", cmd.Nick, nick, mobile, status, credits, info)
	if len(status) == 1 && (status[0] == '0' || status[0] == '1') {
		p.plugger.Sendf(cmd, "SMS is on the way!")
	} else {
		p.plugger.Sendf(cmd, "SMS delivery failed: %s", info)
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
		"keyword": []string{p.config.AQLKeyword},
	}
	for {
		select {
		case <-p.tomb.Dying():
			return nil
		case <-time.After(p.config.PollDelay.Duration):
		}
		resp, err := httpClient.Get(p.config.AQLProxy + "/retrieve?" + form.Encode())
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
	msg := &mup.Message{Text: fmt.Sprintf("[SMS] <%s> %s", sender, text)}
	isChan := isChannel(target)
	if isChan {
		msg.Channel = target
	} else {
		msg.Nick = target
	}
	for _, target := range p.plugger.Targets() {
		a := target.Address()
		if (a.Nick != "" || a.Channel != "") && (a.Nick != msg.Nick || a.Channel != msg.Channel) {
			continue
		}
		msg.Account = a.Account
		p.plugger.Logf("[%s] Delivering SMS from %s (%s) to %s: %s\n", msg.Account, sender, sms.Sender, target, text)
		err = p.plugger.Send(msg)
		if err == nil && !strings.HasPrefix(sender, "+") {
			p.plugger.Sendf(msg, "Answer with: !sms %s <your message>", sender)
		}
	}
	p.tomb.Go(func() error {
		_ = p.deleteSMS(sms)
		return nil
	})
	return nil
}

func (p *aqlPlugin) deleteSMS(sms *smsMessage) error {
	form := url.Values{
		"keyword": []string{p.config.AQLKeyword},
		"keys":    []string{strconv.Itoa(sms.Key)},
	}
	resp, err := httpClient.PostForm(p.config.AQLProxy+"/delete", form)
	if err != nil {
		p.plugger.Logf("Cannot delete SMS message %s: %v", sms.Key, err)
		return err
	}
	p.plugger.Logf("Delete accepted for %v.", sms.Key)
	resp.Body.Close()
	return nil
}
