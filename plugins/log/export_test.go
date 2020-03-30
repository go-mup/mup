package log

import (
	"gopkg.in/mup.v0"
)

const MessageColumns = messageColumns
const MessagePlacers = messagePlacers

func MessageRefs(m *mup.Message) []interface{} {
	return messageRefs(m)
}
