package help

import (
	"bytes"
	"gopkg.in/mgo.v2/bson"
	"gopkg.in/mup.v0"
	"gopkg.in/mup.v0/schema"
	"math/rand"
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
	rand    *rand.Rand
}

func start(plugger *mup.Plugger) mup.Stopper {
	return &helpPlugin{
		plugger: plugger,
		rand:    rand.New(rand.NewSource(42)),
	}
}

func (p *helpPlugin) Stop() error {
	return nil
}

func (p *helpPlugin) HandleMessage(msg *mup.Message) {
	if msg.BotText == "" {
		return
	}
	cmdname := schema.CommandName(msg.BotText)
	if cmdname != "" {
		infos, err := p.pluginsWith(cmdname, false)
		if err == nil && len(infos) == 0 {
			infos, err = p.pluginsWith(cmdname, true)
			if len(infos) > 0 {
				p.sendNotUsable(msg, &infos[0], "running", "")
				return
			}
		}
		if err != nil {
			p.plugger.Logf("Cannot list available commands: %v", err)
			p.plugger.Sendf(msg, "Cannot list available commands: %v", err)
			return
		}
		if len(infos) == 0 {
			p.sendNotKnown(msg)
			return
		}
		addr := msg.Address()
		for _, info := range infos {
			for _, target := range info.Targets {
				if target.Contains(addr) {
					return
				}
			}
		}
		p.sendNotUsable(msg, &infos[0], "enabled", "here")
	}
}

var unknownReplies = []string{
	"Nope.. I don't understand it.",
	"Unknown commands are unknown.",
	"Strictly speaking, I'm not intelligent enough to understand that.",
	"Sorry, but I don't understand.",
	"Excuse moi, parlez vous anglais?",
	"I apologize, but I'm pretty strict about only responding to known commands.",
	"I apologize. I'm a program with a limited vocabulary.",
	"I really wish I understood what you're trying to do.",
	"Roses are red, violets are blue, and I don't understand what you just said.",
	"Can't grasp that.",
	"That's incomprehensible to my small and non-existent mind.",
	"In-com-pre-hen-si-ble-ness.",
}

func (p *helpPlugin) sendNotKnown(msg *mup.Message) {
	// Don't use Intn, so it remains stable when adding entries.
	p.plugger.Sendf(msg, "%s", unknownReplies[p.rand.Intn(len(unknownReplies))])
}

func (p *helpPlugin) sendNotUsable(msg *mup.Message, info *pluginInfo, what, where string) {
	pluginName := info.Name
	if i := strings.Index(pluginName, "/"); i > 0 {
		pluginName = pluginName[:i]
	}
	p.plugger.Logf("Plugin %q not %s for account=%q, channel=%q, nick=%q: %s",
		pluginName, what, msg.Account, msg.Channel, msg.Nick, msg.BotText)
	if where != "" {
		what += " " + where
	}
	p.plugger.Sendf(msg, "Plugin %q is not %s.", pluginName, what)
}

func (p *helpPlugin) HandleCommand(cmd *mup.Command) {
	var args struct{ CmdName string }
	cmd.Args(&args)
	infos, err := p.pluginsWith(args.CmdName, false)
	if err == nil && len(infos) == 0 {
		infos, err = p.pluginsWith(args.CmdName, true)
	}
	if err != nil {
		p.plugger.Logf("Cannot list available commands: %v", err)
		p.plugger.Sendf(cmd, "Cannot list available commands: %v", err)
		return
	}
	if len(infos) == 0 {
		p.plugger.Sendf(cmd, "Command %q not found.", args.CmdName)
		return
	}
	command := infos[0].Command
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

type pluginInfo struct {
	Name    string          `bson:"_id"`
	Command *schema.Command `bson:"commands"`
	Targets []mup.Address
}

func (p *helpPlugin) pluginsWith(cmdname string, known bool) ([]pluginInfo, error) {
	session, c := p.plugger.Collection("dummy")
	defer session.Close()

	var pipeline = []bson.M{
		{"$match": bson.M{"commands.name": cmdname}},
		{"$project": bson.M{"id": 1, "commands": 1, "targets": 1}},
		{"$match": bson.M{"commands.name": cmdname}},
		{"$unwind": "$commands"},
	}

	cname := "plugins"
	if known {
		cname = "plugins.known"
	}
	plugins := c.Database.C(cname)

	var infos []pluginInfo
	err := plugins.Pipe(pipeline).All(&infos)
	if err != nil {
		return nil, err
	}
	return infos, nil
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
