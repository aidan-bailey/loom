package core

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// maxModCommands bounds the slash commands a mod's hello may list; past it
// the rest are dropped.
const maxModCommands = 2000

// maxCommandOutput bounds, in characters, a command's output in the notice
// that reports it.
const maxCommandOutput = 2000

// commandName is a slash command's name as a reply and a mod's list give it:
// 1 to 128 letters, digits, '_', '-' and ':' (a plugin's commands are
// plugin:name). A path's second '/' fails it, so a reply that starts with a
// path stays a message.
var commandName = regexp.MustCompile(`^[A-Za-z0-9_:-]{1,128}$`)

// commandSources are the sources $.command.list() gives.
var commandSources = map[string]bool{"builtin": true, "plugin": true, "user": true, "mcp": true}

// slashCommand is a reply that runs a slash command: its name, without the
// slash, and its arguments.
type slashCommand struct {
	name, args string
}

// parseCommand reports whether text is a slash command: once trimmed, a
// first word of '/' and a name (commandName), the rest its arguments.
func parseCommand(text string) (slashCommand, bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return slashCommand{}, false
	}
	word, rest := text, ""
	if end := strings.IndexFunc(text, unicode.IsSpace); end >= 0 {
		word, rest = text[:end], text[end:]
	}
	if !commandName.MatchString(word[1:]) {
		return slashCommand{}, false
	}
	return slashCommand{name: word[1:], args: strings.TrimSpace(rest)}, true
}

// command is msg's slash command, when it is one: only a CLI or a web
// message runs one, and every other origin's text goes as a prompt.
func (msg Message) command() (slashCommand, bool) {
	if !msg.Origin.needsMod() {
		return slashCommand{}, false
	}
	return parseCommand(msg.Text)
}

// commandsOf is the list a mod's hello gives, by name: true for a name any
// of its entries gives as a built-in (a built-in and a user skill can share
// one, and which runs is Claude's to say). An entry whose name or source the
// model doesn't take is dropped, and every entry past maxModCommands. nil
// for a hello that listed none: an older mod, which can't run a command.
func commandsOf(list []ModCommand) map[string]bool {
	if list == nil {
		return nil
	}
	list = list[:min(len(list), maxModCommands)]
	out := make(map[string]bool, len(list))
	for _, c := range list {
		if commandName.MatchString(c.Name) && commandSources[c.Source] {
			out[c.Name] = out[c.Name] || c.Source == "builtin"
		}
	}
	return out
}

// commandRefusal is why cmd, sent as msg, can't run on the launch l links,
// or "" when it can. A command needs the mod's list, so an older mod, or one
// that has not said hello, is refused. compact, clear and model are the
// built-ins a reply may run, each finishing on its own (a bare /model opens
// a menu); any other built-in is refused, and so is a name the session's
// Claude doesn't list, an alias among them.
func commandRefusal(msg Message, cmd slashCommand, l *modLink) string {
	switch {
	case msg.Origin == OriginCLI && msg.From != "":
		return "a slash command takes no --from"
	case l.commands == nil && l.live == modLive:
		return "this session's loom mod predates slash commands: pause and resume the session to update it"
	case l.commands == nil:
		return "this session's loom mod has not said hello yet: send the command again in a moment"
	}
	switch cmd.name {
	case "compact":
		return ""
	case "clear":
		if cmd.args != "" {
			return "/clear takes no arguments"
		}
		return ""
	case "model":
		if cmd.args == "" {
			return "/model needs a model name: a bare /model opens a menu nobody can answer"
		}
		return ""
	}
	builtin, listed := l.commands[cmd.name]
	switch {
	case builtin:
		return fmt.Sprintf("/%s is a built-in loom doesn't run from a reply: it may open a menu or end the session", cmd.name)
	case !listed:
		return fmt.Sprintf("this session's Claude has no /%s command", cmd.name)
	}
	return ""
}

// commandResult is what a command's run tells its sender: its output after
// its name, cut at maxCommandOutput characters, or that it ran, when it
// printed nothing (as /compact, /model and a skill do).
func commandResult(name, args, output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return strings.TrimSpace("Ran /"+name+" "+args) + "."
	}
	if r := []rune(output); len(r) > maxCommandOutput {
		output = string(r[:maxCommandOutput]) + "…"
	}
	return "/" + name + ": " + output
}
