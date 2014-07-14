package schema

import (
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
	if strings.HasPrefix(arg.Name, "-") {
		return Bool
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

func (cs Commands) Parse(text string) (*Command, interface{}, error) {
	p := parser{text, 0}
	p.skipSpaces()
	mark := p.i
	if !p.skipAlphas() {
		return nil, nil, fmt.Errorf("invalid command")
	}
	name := text[mark:p.i]
	var c *Command
	for i := range cs {
		if cs[i].Name == name {
			c = &cs[i]
			break
		}
	}
	if c == nil {
		return nil, nil, fmt.Errorf("unknown command: %s", name)
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
			return nil, nil, fmt.Errorf("unknown argument: %s", text[mark:p.i])
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
				return nil, nil, err
			}
		} else if arg.Type == "" || arg.Type == Bool {
			value = true
		} else {
			return nil, nil, fmt.Errorf("missing value for argument: %s=%s", arg.Name, arg.Type)
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
				return nil, nil, err
			}
		} else if arg.Flag&Required != 0 {
			missing = append(missing, arg.Name)
		}
		p.skipSpaces()
	}

	if len(missing) > 0 {
		return nil, nil, fmt.Errorf("missing input for arguments: %s", strings.Join(missing, ", "))
	}

	if p.i < len(text) {
		return nil, nil, fmt.Errorf("unexpected input: %s", text[p.i:])
	}
	return c, opts, nil
}

func (cs Commands) Command(name string) (*Command, error) {
	var c *Command
	for i := range cs {
		if cs[i].Name == name {
			c = &cs[i]
			break
		}
	}
	if c == nil {
		return nil, fmt.Errorf("unknown command: %s", name)
	}
	return c, nil
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
	i := p.i
	for _, c := range p.text[p.i:] {
		if f(c) != when {
			break
		}
		i++
	}
	skipped := i != p.i
	p.i = i
	return skipped
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
