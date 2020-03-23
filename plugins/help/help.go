package help

import (
	"bytes"
	"database/sql"
	"fmt"
	"math/rand"
	"strings"

	"gopkg.in/mup.v0"
	"gopkg.in/mup.v0/schema"
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
}, {
	Name: "start",
	Help: "Displays available commands.",
	Hide: true,
}}

func init() {
	mup.RegisterPlugin(&Plugin)
}

type helpPlugin struct {
	plugger *mup.Plugger
	rand    *rand.Rand
	config  struct {
		Boring bool
	}
}

func start(plugger *mup.Plugger) mup.Stopper {
	p := &helpPlugin{
		plugger: plugger,
		rand:    rand.New(rand.NewSource(42)),
	}
	plugger.Config(&p.config)
	return p
}

func (p *helpPlugin) Stop() error {
	return nil
}

func anyRunning(infos []pluginInfo) bool {
	for _, info := range infos {
		if info.Running {
			return true
		}
	}
	return false
}

func (p *helpPlugin) HandleMessage(msg *mup.Message) {
	if msg.BotText == "" || msg.Bang != "" && strings.HasPrefix(msg.Text, msg.Bang) {
		return
	}
	cmdname := schema.CommandName(msg.BotText)
	if cmdname != "" {
		infos, err := p.pluginsWith(cmdname)
		if err != nil {
			p.plugger.Logf("Cannot list available commands: %v", err)
			p.plugger.Sendf(msg, "Cannot list available commands: %v", err)
			return
		}
		if !anyRunning(infos) {
			if len(infos) == 0 {
				p.sendNotKnown(msg, cmdname)
			} else {
				p.sendNotUsable(msg, &infos[0], "running", "")
			}
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

func (p *helpPlugin) sendNotKnown(msg *mup.Message, cmdname string) {
	if p.config.Boring {
		p.plugger.Sendf(msg, "Command %q not found.", cmdname)
	} else {
		p.plugger.Sendf(msg, "%s", unknownReplies[p.rand.Intn(len(unknownReplies))])
	}
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
	if args.CmdName == "" {
		cmdnames, err := p.cmdList()
		if err != nil {
			p.plugger.Logf("Cannot list available commands: %v", err)
			p.plugger.Sendf(cmd, "Cannot list available commands: %v", err)
			return
		}
		if len(cmdnames) == 0 {
			p.plugger.Sendf(cmd, "No known commands available. Go load some plugins.")
			return
		}
		p.plugger.Sendf(cmd, `Run "help <cmdname>" for details on: %s`, strings.Join(cmdnames, ", "))
		return
	}

	infos, err := p.pluginsWith(args.CmdName)
	if err != nil {
		p.plugger.Logf("Cannot list available commands: %v", err)
		p.plugger.Sendf(cmd, "Cannot list available commands: %v", err)
		return
	}
	if len(infos) == 0 {
		p.plugger.Sendf(cmd, "Command %q not found.", args.CmdName)
		return
	}
	command := &infos[0].Command
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
	Name    string
	Running bool
	Command schema.Command
	Targets []mup.Address
}

func (p *helpPlugin) pluginsWith(cmdname string) ([]pluginInfo, error) {
	tx, err := p.plugger.DB().Begin()
	if err != nil {
		return nil, fmt.Errorf("cannot begin database transaction: %v", err)
	}
	defer tx.Rollback()

	var infos []pluginInfo
	crows, err := tx.Query("SELECT plugin,command,help,hide FROM command_schema WHERE command=?", cmdname)
	for err == nil && crows.Next() {
		var info pluginInfo

		// Scan the basic command data.
		err = crows.Scan(&info.Name, &info.Command.Name, &info.Command.Help, &info.Command.Hide)
		if err != nil {
			break
		}

		// Fetch the argument schema for the command.
		var arows *sql.Rows
		arows, err = tx.Query("SELECT argument,hint,type,flag FROM argument_schema WHERE plugin=? AND command=?", info.Name, cmdname)
		for err == nil && arows.Next() {
			var arg schema.Arg
			err = arows.Scan(&arg.Name, &arg.Hint, &arg.Type, &arg.Flag)
			if err != nil {
				break
			}
			info.Command.Args = append(info.Command.Args, arg)
		}
		if arows != nil {
			arows.Close()
		}
		if err != nil {
			break
		}

		// Check whether any of the plugins with such a command is running.
		row := tx.QueryRow("SELECT TRUE FROM plugin WHERE name=? OR name LIKE ? LIMIT 1", info.Name, info.Name+"/%")
		err = row.Scan(&info.Running)
		if err != nil && err != sql.ErrNoRows {
			break
		}

		// Fetch all targets that can see that command.
		var trows *sql.Rows
		trows, err = tx.Query("SELECT account,channel,nick FROM target WHERE plugin=? OR plugin LIKE ?", info.Name, info.Name+"/%")
		for err == nil && trows.Next() {
			var target mup.Address
			err = trows.Scan(&target.Account, &target.Channel, &target.Nick)
			if err != nil {
				break
			}
			info.Targets = append(info.Targets, target)
		}
		if trows != nil {
			trows.Close()
		}

		infos = append(infos, info)
	}
	if crows != nil {
		crows.Close()
	}
	if err != nil {
		return nil, fmt.Errorf("cannot retrieve command schema from database: %v", err)
	}

	return infos, nil
}

func (p *helpPlugin) cmdList() ([]string, error) {
	db := p.plugger.DB()

	var result []string
	rows, err := db.Query("SELECT DISTINCT(command) FROM command_schema WHERE hide=FALSE ORDER BY command")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var cmdname string
		err = rows.Scan(&cmdname)
		if err != nil {
			return nil, err
		}
		if cmdname == "help" {
			continue
		}
		result = append(result, cmdname)
	}
	return result, nil
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
