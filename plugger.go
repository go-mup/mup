package mup

import (
	"fmt"
)

type Plugger struct {
	send     func(*Message) error
}

func newPlugger(send func(msg *Message) error) *Plugger {
	return &Plugger{
		send:     send,
	}
}

func (p *Plugger) Replyf(msg *Message, format string, args ...interface{}) error {
	text := fmt.Sprintf(format, args...)
	target := msg.Target
	if msg.Target == msg.MupNick {
		target = msg.Nick
	} else {
		text = msg.Nick + ": " + text
	}
	reply := &Message{Server: msg.Server, Target: target, Text: text}
	err := p.send(reply)
	if err != nil {
		logf("Cannot put message in outgoing queue: %v", err)
		return fmt.Errorf("cannot put message in outgoing queue: %v", err)
	}
	return nil
}
