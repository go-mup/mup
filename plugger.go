package mup

import (
	"fmt"
)

type Plugger struct {
	sendMessage  func(*Message) error
	loadSettings func(result interface{})
}

func newPlugger(sendMessage func(msg *Message) error, loadSettings func(result interface{})) *Plugger {
	return &Plugger{
		sendMessage:  sendMessage,
		loadSettings: loadSettings,
	}
}

func (p *Plugger) Settings(result interface{}) {
	p.loadSettings(result)
}

func (p *Plugger) Replyf(msg *Message, format string, args ...interface{}) error {
	text := fmt.Sprintf(format, args...)
	target := msg.Target
	if msg.Target == msg.MupNick {
		target = msg.Nick
	} else {
		text = msg.Nick + ": " + text
	}
	reply := &Message{Account: msg.Account, Target: target, Text: text}
	return p.Send(reply)
}

func (p *Plugger) Sendf(account, target, format string, args ...interface{}) error {
	reply := &Message{Account: account, Target: target, Text: fmt.Sprintf(format, args...)}
	return p.Send(reply)
}

func (p *Plugger) Send(msg *Message) error {
	err := p.sendMessage(msg)
	if err != nil {
		logf("Cannot put message in outgoing queue: %v", err)
		return fmt.Errorf("cannot put message in outgoing queue: %v", err)
	}
	return nil
}

