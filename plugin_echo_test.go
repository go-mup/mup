package mup

import (
	. "gopkg.in/check.v1"
	"labix.org/v2/mgo/bson"
)

var _ = Suite(&EchoSuite{})

type EchoSuite struct{}

var echoTests = []struct {
	msg, reply string
	settings   interface{}
}{
	{":nick!~user@host PRIVMSG mup :echo repeat", "PRIVMSG nick :repeat", nil},
	{":nick!~user@host PRIVMSG #channel :mup: echo repeat", "PRIVMSG #channel :nick: repeat", nil},
	{":nick!~user@host PRIVMSG #channel :echo repeat", "", nil},
	{":nick!~user@host PRIVMSG mup :notecho repeat", "", nil},
	{":nick!~user@host PRIVMSG mup :echonospace", "", nil},
	{":nick!~user@host PRIVMSG mup :myecho hi", "PRIVMSG nick :hi", M{"command": "myecho"}},
}

func (s *EchoSuite) TestEcho(c *C) {
	for i, test := range echoTests {
		var replies []string
		settings := func(result interface{}) {
			if test.settings == nil {
				return
			}
			data, err := bson.Marshal(test.settings)
			c.Assert(err, IsNil)
			err = bson.Unmarshal(data, result)
			c.Assert(err, IsNil)
		}
		plugger := newTestPlugger(&replies, settings)
		plugin := registeredPlugins["echo"](plugger)

		c.Logf("Feeding message #%d: %s", i, test.msg)
		err := plugin.Handle(parse(test.msg))
		c.Assert(err, IsNil)
		if test.reply == "" {
			c.Assert(replies, IsNil)
		} else {
			c.Assert(replies, DeepEquals, []string{test.reply})
		}
	}
}
