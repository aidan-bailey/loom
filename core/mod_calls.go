package core

import (
	"regexp"
	"slices"
	"strings"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session/claudemod"
)

// jobCall matches a `loom job submit` or `loom job run` in a Bash command
// (claudemod.JobCallPattern): each match is one submit a call may account
// for (subagent job results spec §5.2).
var jobCall = regexp.MustCompile(claudemod.JobCallPattern)

// maxModCalls caps the calls a link keeps in flight: a call whose ended
// report never came stays until the cap drops it, oldest first.
const maxModCalls = 64

// ModCall is a Bash call the loom mod reports while it runs (POST /v1/calls):
// its tool_use_id, the subagent it runs in (Claude's agentId; "" on the main
// loop) and its command. The mod reports only commands that match
// claudemod.JobCallPattern.
type ModCall struct {
	ID      string `json:"id"`
	Agent   string `json:"agent,omitempty"`
	Command string `json:"command"`
	// Model is the session's model as Claude's session info gives it
	// ($.session.model(): the main loop's, as /model shows it); "" from an
	// older mod or when the mod couldn't read it. A feedback note records it.
	Model string `json:"model,omitempty"`
}

// reportedCall is a call in flight on a link: uses is how many submits it
// may still account for, one per match of jobCall in its command, and
// unquoted is its command as the shell reads it (unquoted).
type reportedCall struct {
	ModCall
	uses     int
	unquoted string
}

// ModCall records a call launch's mod reports starting, or forgets it once
// ended. A call already recorded stays as it is (a retried report), and a
// command with no match is not kept. poller is as ModAsk's: another than the
// latest hello's is ErrModSuperseded.
func (m *Model) ModCall(launch, poller string, c ModCall, ended bool) error {
	inst, l, err := m.askLink(launch, poller)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(l.calls, func(x *reportedCall) bool { return x.ID == c.ID })
	switch {
	case ended:
		if i >= 0 {
			l.calls = slices.Delete(l.calls, i, i+1)
		}
		return nil
	case i >= 0:
		return nil
	}
	uses := len(jobCall.FindAllStringIndex(c.Command, -1))
	if uses == 0 {
		return nil
	}
	if len(l.calls) >= maxModCalls {
		log.For("core").Info("mod.call_dropped", "instance", inst.Title, "call", l.calls[0].ID)
		l.calls = l.calls[1:]
	}
	l.calls = append(l.calls, &reportedCall{ModCall: c, uses: uses, unquoted: unquoted(c.Command)})
	return nil
}

// attribution is the reported call a submit of argv from launch came from:
// the one call in flight on that launch's link whose command holds every
// element of argv, as written or unquoted, and has a use left. nil when none
// does, or more than one could have made it: the job then reports to the
// session (spec §5.2). The caller spends the use once the job is saved. A
// link for an earlier launch is never consulted.
func (m *Model) attribution(launch string, argv []string) *reportedCall {
	inst := m.instByLaunch(launch)
	if inst == nil {
		return nil
	}
	l := m.mods[inst]
	if l == nil || l.launch != launch {
		return nil
	}
	var found *reportedCall
	for _, c := range l.calls {
		if c.uses == 0 || !c.holdsAll(argv) {
			continue
		}
		if found != nil {
			return nil
		}
		found = c
	}
	return found
}

// holdsAll reports whether the call's command contains every element of
// argv, each as written or as the shell unquotes it: the shell removes the
// quotes before loom sees argv, so `sh -c 'a '"'"'b'` submits `a 'b`,
// which only the unquoted command holds. The command as written still
// counts, for what unquoted can't read ($'…').
func (c *reportedCall) holdsAll(argv []string) bool {
	for _, a := range argv {
		if !strings.Contains(c.Command, a) && !strings.Contains(c.unquoted, a) {
			return false
		}
	}
	return true
}

// hereDoc matches a here-document operator at its start, capturing the dash
// of <<- and the delimiter in whichever quotes it has.
var hereDoc = regexp.MustCompile(`^<<(-?)[ \t]*(?:'([^'\n]+)'|"([^"\n]+)"|\\?([^\s;&|<>()'"\\]+))`)

// unquoted is command with the shell's quote removal applied and nothing
// expanded: single quotes dropped with their content kept as is, double
// quotes dropped with a backslash before $ ` " \ or a newline, and outside
// quotes every backslash dropped (a backslash-newline whole). A comment, and
// the body of a here document, is copied as written, since the shell removes
// no quotes there: an apostrophe in one would otherwise open a quote that
// swallows the submit after it. It reads no $'…' quoting, and an unclosed
// quote runs to the end.
func unquoted(command string) string {
	var b strings.Builder
	var docs []hereDocOp // the here documents whose bodies start at the next newline
	inWord := false      // a # inside a word starts no comment
	for i := 0; i < len(command); {
		ch := command[i]
		switch {
		case ch == '\\' && i+1 < len(command):
			if command[i+1] != '\n' {
				b.WriteByte(command[i+1])
				inWord = true
			}
			i += 2
		case ch == '\'':
			end := strings.IndexByte(command[i+1:], '\'')
			if end < 0 {
				b.WriteString(command[i+1:])
				return b.String()
			}
			b.WriteString(command[i+1 : i+1+end])
			i += end + 2
			inWord = true
		case ch == '"':
			i = doubleQuoted(&b, command, i+1)
			inWord = true
		case ch == '#' && !inWord:
			end := strings.IndexByte(command[i:], '\n')
			if end < 0 {
				end = len(command) - i
			}
			b.WriteString(command[i : i+end])
			i += end
		case strings.HasPrefix(command[i:], "<<"):
			op := hereDoc.FindStringSubmatch(command[i:])
			if op == nil { // no delimiter, as in a here string (<<<): skip both, or the second reads as an operator
				b.WriteString("<<")
				i += 2
				break
			}
			docs = append(docs, hereDocOp{delim: op[2] + op[3] + op[4], tabs: op[1] == "-"})
			b.WriteString(op[0])
			i += len(op[0])
			inWord = false
		case ch == '\n' && len(docs) > 0:
			b.WriteByte('\n')
			i = hereBodies(&b, command, i+1, docs)
			docs = nil
			inWord = false
		default:
			b.WriteByte(ch)
			i++
			inWord = !strings.ContainsRune(" \t\n;&|()<>", rune(ch))
		}
	}
	return b.String()
}

// doubleQuoted writes the double-quoted text that starts at command[i],
// unquoted, and returns the index past its closing quote.
func doubleQuoted(b *strings.Builder, command string, i int) int {
	for i < len(command) {
		ch := command[i]
		switch {
		case ch == '\\' && i+1 < len(command) && strings.IndexByte("$`\"\\\n", command[i+1]) >= 0:
			if command[i+1] != '\n' {
				b.WriteByte(command[i+1])
			}
			i += 2
		case ch == '"':
			return i + 1
		default:
			b.WriteByte(ch)
			i++
		}
	}
	return i
}

// hereDocOp is a here-document operator: its delimiter, and whether it was
// <<-, whose delimiter line may start with tabs.
type hereDocOp struct {
	delim string
	tabs  bool
}

// hereBodies copies, as written, the bodies of docs in turn, each through its
// delimiter line, from command[i], the start of the line after their
// operators, and returns the index past the last.
func hereBodies(b *strings.Builder, command string, i int, docs []hereDocOp) int {
	for _, doc := range docs {
		for i < len(command) {
			end := strings.IndexByte(command[i:], '\n')
			if end < 0 {
				end = len(command) - i
			}
			line := command[i : i+end]
			b.WriteString(line)
			i += end
			if i < len(command) {
				b.WriteByte('\n')
				i++
			}
			if doc.tabs {
				line = strings.TrimLeft(line, "\t")
			}
			if line == doc.delim {
				break
			}
		}
	}
	return i
}
