package help

import (
	"bytes"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
	"gopkg.in/mup.v0"
	"gopkg.in/mup.v0/schema"
	"strings"
)

var Plugin = mup.PluginSpec{
	Name:     "help",
	Help:     "Exposes the help system.",
	Start:    start,
	Commands: Commands,
}

var Commands = schema.Commands{{
	Name: "help",
	Help: "Displays available commands or details for a specific command.",
	Args: schema.Args{{
		Name: "cmdname",
	}},
}}

func init() {
	mup.RegisterPlugin(&Plugin)
}

type helpPlugin struct {
	plugger *mup.Plugger
}

func start(plugger *mup.Plugger) mup.Stopper {
	return &helpPlugin{plugger: plugger}
}

func (p *helpPlugin) Stop() error {
	return nil
}

func (p *helpPlugin) HandleCommand(cmd *mup.Command) {
	var args struct{ CmdName string }
	cmd.Args(&args)
	command, err := p.findSchema(args.CmdName)
	if err == mgo.ErrNotFound {
		p.plugger.Sendf(cmd, "Command %q not found.", args.CmdName)
		return
	}
	if err != nil {
		p.plugger.Logf("Cannot list available commands: %v", err)
		p.plugger.Sendf(cmd, "Cannot list available commands: %v", err)
		return
	}
	var buf bytes.Buffer
	buf.Grow(512)
	formatUsage(&buf, command)
	if buf.Len() > 50 {
		p.plugger.Sendf(cmd, "%s", buf.Bytes())
		buf.Reset()
	} else {
		buf.WriteString(" — ")
	}

	lines := helpLines(command.Help)
	summary := lines[0]
	if summary == "" {
		summary = "The author of this command is unhelpful."
	}
	buf.WriteString(summary)

	p.plugger.Sendf(cmd, "%s", buf.Bytes())
	for _, line := range lines[1:] {
		p.plugger.Sendf(cmd, "%s", line)
	}
}

func (p *helpPlugin) findSchema(name string) (*schema.Command, error) {
	session, c := p.plugger.Collection("dummy")
	defer session.Close()

	plugins := c.Database.C("plugins")
	var result struct{ Commands schema.Commands }
	err := plugins.Find(bson.D{{"commands.name", name}}).Select(bson.D{{"commands", 1}}).One(&result)
	if err != nil {
		return nil, err
	}
	for i := range result.Commands {
		command := &result.Commands[i]
		if command.Name == name {
			return command, nil
		}
	}
	panic("got a valid result which missed the command searched for")
}

func formatUsage(buf *bytes.Buffer, command *schema.Command) {
	buf.WriteString(command.Name)
	for _, arg := range command.Args {
		buf.WriteByte(' ')
		if arg.Flag&schema.Required == 0 {
			buf.WriteByte('[')
		}
		formatArg(buf, &arg)
		if arg.Flag&schema.Required == 0 {
			buf.WriteByte(']')
		}
	}
}

func formatArg(buf *bytes.Buffer, arg *schema.Arg) {
	if strings.HasPrefix(arg.Name, "-") {
		buf.WriteString(arg.Name)
		if t := valueType(arg); t != schema.Bool {
			buf.WriteString("=<")
			if arg.Hint != "" {
				buf.WriteString(arg.Hint)
			} else {
				buf.WriteString(string(t))
			}
			buf.WriteByte('>')
		}
	} else {
		buf.WriteByte('<')
		buf.WriteString(arg.Name)
		if arg.Flag&schema.Trailing != 0 {
			buf.WriteString(" ...")
		}
		buf.WriteByte('>')
	}
}

func valueType(arg *schema.Arg) schema.ValueType {
	if arg.Type != "" {
		return arg.Type
	}
	return schema.String
}

func helpLines(text string) []string {
	buf := []byte(strings.TrimSpace(text))
	first := true
	nl := 0
	j := 0
	for i := range buf {
		c := buf[i]
		switch {
		case c == '\n':
			if first {
				first = false
				nl = 1
			}
			if nl == 1 {
				buf[j] = '\n'
				j++
			}
			nl++
		case nl == 0:
			buf[j] = c
			j++
		case c != ' ' && c != '\t':
			if nl == 1 {
				buf[j] = ' '
				j++
			}
			nl = 0
			buf[j] = c
			j++
		}
	}
	return strings.Split(string(buf[:j]), "\n")
}
