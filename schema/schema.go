package schema

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

type Commands []Command

type Command struct {
	Name string
	Help string
	Args Args
}

type Args []Arg

type Arg struct {
	Name string
	Hint string
	Help string
	Type ValueType
	Flag int
}

const (
	Required = 1 << iota
	Trailing
)

type ValueType string

var (
	String ValueType = "string"
	Bool   ValueType = "bool"
	Int    ValueType = "int"
)

func valueType(arg *Arg) ValueType {
	if arg.Type != "" {
		return arg.Type
	}
	return String
}

func parseValue(t ValueType, s string) (interface{}, error) {
	switch t {
	case String:
		return s, nil
	case Bool:
		b, err := strconv.ParseBool(s)
		return b, err
	case Int:
		s, err := strconv.Atoi(s)
		return s, err
	}
	panic("internal error: unknown value type: " + string(t))
}

func parseArg(arg *Arg, s string) (interface{}, error) {
	value, err := parseValue(valueType(arg), s)
	if err != nil {
		return nil, fmt.Errorf("cannot parse value as %s: %q", valueType(arg), s)
	}
	return value, err
}

var errInvalid = errors.New("invalid command")

// CommandName returns the command name used in the provided text,
// or the empty string if no command name could be parsed out of it.
func CommandName(text string) string {
	p := parser{text, 0}
	p.skipSpaces()
	mark := p.i
	if !p.skipAlphas() {
		return ""
	}
	return text[mark:p.i]
}

// Command returns the command with the given name, or nil if one is not found.
func (cs Commands) Command(name string) *Command {
	var c *Command
	for i := range cs {
		if cs[i].Name == name {
			c = &cs[i]
			break
		}
	}
	return c
}

func (c *Command) Parse(text string) (interface{}, error) {
	p := parser{text, 0}

	p.skipSpaces()
	mark := p.i
	if !p.skipAlphas() {
		return nil, fmt.Errorf("invalid text for command %q: %q", c.Name, text)
	}
	name := text[mark:p.i]
	if name != c.Name {
		return nil, fmt.Errorf("cannot parse with command %q text meant to %q: %s", c.Name, name, text)
	}

	// TODO Must require the space here.
	p.skipSpaces()

	var opts map[string]interface{}

	for p.peekByte('-') {
		mark := p.i
		p.skipArgRunes()
		name := text[mark:p.i]
		var arg *Arg
		for i := range c.Args {
			if c.Args[i].Name == name {
				arg = &c.Args[i]
				break
			}
		}
		if arg == nil {
			return nil, fmt.Errorf("unknown argument: %s", text[mark:p.i])
		}
		if len(opts) == 0 {
			opts = make(map[string]interface{})
		}
		var value interface{}
		var err error
		if p.skipByte('=') {
			mark := p.i
			p.skipNonSpaces()
			value, err = parseArg(arg, text[mark:p.i])
			if err != nil {
				return nil, err
			}
		} else if arg.Type == "" || arg.Type == Bool {
			value = true
		} else {
			return nil, fmt.Errorf("missing value for argument: %s=%s", arg.Name, arg.Type)
		}
		opts[arg.Name[1:]] = value
		p.skipSpaces()
	}

	var missing []string
	for i := range c.Args {
		arg := &c.Args[i]
		if strings.HasPrefix(arg.Name, "-") {
			if arg.Flag&Required != 0 && opts[arg.Name[1:]] == nil {
				missing = append(missing, arg.Name)
			}
			continue
		}
		mark := p.i
		if p.skipNonSpaces() {
			if len(opts) == 0 {
				opts = make(map[string]interface{})
			}
			var s string
			if arg.Flag&Trailing == 0 {
				s = text[mark:p.i]
			} else {
				s = strings.TrimSpace(text[mark:])
				p.i = len(text)
			}
			var err error
			opts[arg.Name], err = parseArg(arg, s)
			if err != nil {
				return nil, err
			}
		} else if arg.Flag&Required != 0 {
			missing = append(missing, arg.Name)
		}
		p.skipSpaces()
	}

	if len(missing) > 0 {
		return nil, fmt.Errorf("missing input for argument%s: %s", plural(len(missing), "", "s"), strings.Join(missing, ", "))
	}

	if p.i < len(text) {
		return nil, fmt.Errorf("unexpected input: %s", text[p.i:])
	}
	return opts, nil
}

func plural(n int, singular, plural string) string {
	if n > 1 {
		return plural
	}
	return singular
}

type parser struct {
	text string
	i    int
}

func (p *parser) skipSpaces() bool {
	return p.skipFunc(unicode.IsSpace, true)
}

func (p *parser) skipNonSpaces() bool {
	return p.skipFunc(unicode.IsSpace, false)
}

func (p *parser) skipArgRunes() bool {
	return p.skipFunc(isArgRune, true)
}

func (p *parser) skipAlphas() bool {
	return p.skipFunc(isAlpha, true)
}

func (p *parser) skipFunc(f func(rune) bool, when bool) bool {
	for i, c := range p.text[p.i:] {
		if f(c) != when {
			p.i += i
			return true
		}
	}
	if p.i < len(p.text) {
		p.i = len(p.text)
		return true
	}
	return false
}

func (p *parser) skipByte(b byte) bool {
	if p.peekByte(b) {
		p.i++
		return true
	}
	return false
}

func (p *parser) peekByte(b byte) bool {
	return p.i < len(p.text) && p.text[p.i] == b
}

func isAlpha(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

func isArgRune(r rune) bool {
	return r == '-' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
