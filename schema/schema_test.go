package schema_test

import (
	"fmt"
	"strings"
	"testing"

	"gopkg.in/niemeyer/mup.v0/schema"

	. "gopkg.in/check.v1"
)

func Test(t *testing.T) { TestingT(t) }

type S struct{}

var _ = Suite(&S{})

var commands = schema.Commands{{
	Name: "cmd0",
	Help: help("cmd0"),
}, {
	Name: "cmd1",
	Help: help("cmd1"),
	Args: schema.Args{{
		Name: "arg0",
		Help: help("arg0"),
		Flag: schema.Required,
	}, {
		Name: "arg1",
		Help: help("arg1"),
		Flag: schema.Required,
	}, {
		Name: "arg2",
		Help: help("arg2"),
	}},
}, {
	Name: "cmd2",
	Help: help("cmd2"),
	Args: schema.Args{{
		Name: "arg0",
		Help: help("arg0"),
		Flag: schema.Required,
	}, {
		Name: "arg1",
		Help: help("arg1"),
		Flag: schema.Required | schema.Trailing,
	}},
}, {
	Name: "cmd3",
	Help: help("cmd3"),
	Args: schema.Args{{
		Name: "arg0",
		Help: help("arg0"),
		Flag: schema.Required,
	}, {
		Name: "arg1",
		Help: help("arg1"),
	}, {
		Name: "-arg2",
		Help: help("arg2"),
		Flag: schema.Required,
	}, {
		Name: "-arg3",
		Help: help("arg3"),
	}},
}, {
	Name: "cmd4",
	Help: help("cmd4"),
	Args: schema.Args{{
		Name: "arg0",
		Help: help("arg0"),
		Type: schema.String,
	}, {
		Name: "-arg1",
		Help: help("arg1"),
		Type: schema.String,
	}},
}, {
	Name: "cmd5",
	Help: help("cmd6"),
	Args: schema.Args{{
		Name: "stringA",
		Help: help("stringA"),
		Type: schema.String,
	}, {
		Name: "intA",
		Help: help("intA"),
		Type: schema.Int,
	}, {
		Name: "boolA",
		Help: help("boolA"),
		Type: schema.Bool,
	}, {
		Name: "-stringB",
		Help: help("stringB"),
		Type: schema.String,
	}, {
		Name: "-intB",
		Help: help("intB"),
		Type: schema.Int,
	}, {
		Name: "-boolB",
		Help: help("boolB"),
		Type: schema.Bool,
	}},
}}

func help(name string) string {
	return fmt.Sprintf("help for %s", name)
}

var parseTests = []struct {
	text  string
	opts  map[string]interface{}
	error string
}{

	// Basic errors.
	{
		text:  "'",
		error: "invalid command",
	},
	{
		text:  "bad foo",
		error: "unknown command: bad",
	},

	// Simple positional argument handling.
	{
		text: "cmd0",
	}, {
		text:  "cmd0 val0 val1",
		error: "unexpected input: val0 val1",
	}, {
		text:  "cmd1",
		error: "missing input for arguments: arg0, arg1",
	}, {
		text:  "cmd1 val0",
		error: "missing input for arguments: arg1",
	}, {
		text: " cmd1  val0  val1 ",
		opts: map[string]interface{}{"arg0": "val0", "arg1": "val1"},
	}, {
		text: "cmd1 val0 val1 val2",
		opts: map[string]interface{}{"arg0": "val0", "arg1": "val1", "arg2": "val2"},
	}, {
		text:  "cmd1 val0 val1 val2  val3  val4",
		error: "unexpected input: val3  val4",
	},

	// Trailing argument handling.
	{
		text:  "cmd2",
		error: "missing input for arguments: arg0, arg1",
	}, {
		text:  "cmd2 val0",
		error: "missing input for arguments: arg1",
	}, {
		text: "cmd2 val0 val1",
		opts: map[string]interface{}{"arg0": "val0", "arg1": "val1"},
	}, {
		text: "cmd2 val0 val1  val2  ",
		opts: map[string]interface{}{"arg0": "val0", "arg1": "val1  val2"},
	},

	// Dash argument handling.
	{
		text:  "cmd0 -arg0 -arg1",
		error: "unknown argument: -arg0",
	}, {
		text:  "cmd3 -arg2",
		error: "missing input for arguments: arg0",
	}, {
		text:  "cmd3 -arg4",
		error: "unknown argument: -arg4",
	}, {
		text:  "cmd3 arg0",
		error: "missing input for arguments: -arg2",
	}, {
		text: "cmd3 -arg2 val0",
		opts: map[string]interface{}{"arg0": "val0", "arg2": true},
	}, {
		text: "cmd3 -arg3 -arg2 val0 val1",
		opts: map[string]interface{}{"arg0": "val0", "arg1": "val1", "arg2": true, "arg3": true},
	},

	// Dash argument with value.
	{
		text:  "cmd4 -arg1",
		error: "missing value for argument: -arg1=string",
	}, {
		text: "cmd4 -arg1=val1",
		opts: map[string]interface{}{"arg1": "val1"},
	}, {
		text: "cmd4 -arg1=val1 val0",
		opts: map[string]interface{}{"arg0": "val0", "arg1": "val1"},
	},

	// Type handling.
	{
		text:  "cmd5 -boolB=foo",
		error: `cannot parse value as bool: "foo"`,
	}, {
		text:  "cmd5 -boolB= foo",
		error: `cannot parse value as bool: ""`,
	}, {
		text: "cmd5 -stringB=string -intB=42 -boolB string 42 true",
		opts: map[string]interface{}{
			"stringA": "string",
			"stringB": "string",
			"intA":    42,
			"intB":    42,
			"boolA":   true,
			"boolB":   true,
		},
	}, {
		text: "cmd5 -boolB=true",
		opts: map[string]interface{}{"boolB": true},
	},
}

func (s *S) TestCommandParse(c *C) {
	for _, test := range parseTests {
		c.Logf("Processing command line: %q", test.text)
		cmd, opts, err := commands.Parse(test.text)
		if test.error != "" {
			c.Assert(err, ErrorMatches, test.error)
		} else {
			c.Assert(err, IsNil)
			c.Assert(cmd, NotNil)
			c.Assert(cmd.Name, Equals, strings.Fields(test.text)[0])
			c.Assert(opts, DeepEquals, test.opts)
		}
	}
}
