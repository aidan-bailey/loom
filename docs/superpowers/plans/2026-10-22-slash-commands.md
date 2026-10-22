# Slash Commands Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A reply from the phone or `loom session send` that is an allowed slash command runs it in the session's Claude through the loom mod.

**Architecture:** The mod's hello lists Claude's commands (`$.command.list()`); the model (`core`) parses a CLI or web reply, applies the spec's rules against that list, and queues a command message; the mod runs it with `$.command.run` and reports `queued`, then `complete` with the output or `failed`; the model hands the result to the sender as a notice (or `Answered` to a waiting CLI). Policy stays in Go; the module only carries.

**Tech Stack:** Go (core, internal/modserver), TypeScript (the mod, `session/claudemod/mod/hooks/register.ts`), Preact (the phone's reply box).

**Spec:** `docs/superpowers/specs/2026-10-22-slash-commands-design.md`. **Execution:** inline in the session that wrote it, by the owner's choice; no checkpoint pauses.

---

### Task 1: core — parse, rules, protocol, results

**Files:**
- Create: `core/mod_command.go`, `core/mod_command_test.go`
- Modify: `core/mod.go` (`ModHello`, `ModMessage`, `modMessage`, `modLink`, `Send`, `ModHello()`, `ModInbox`, `modQueued`, `modComplete`, `modFailed`, `endTurns`)
- Modify: `internal/modserver/server_test.go` (the hello's `commands` decodes)

- [ ] **Step 1: Write the failing tests** in `core/mod_command_test.go`:

```go
package core

import (
	"fmt"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseCommand(t *testing.T) {
	for _, c := range []struct {
		text string
		want slashCommand
		ok   bool
	}{
		{"/compact", slashCommand{name: "compact"}, true},
		{"  /compact keep the tests  ", slashCommand{name: "compact", args: "keep the tests"}, true},
		{"/model\tsonnet", slashCommand{name: "model", args: "sonnet"}, true},
		{"/code-review:code-review the diff", slashCommand{name: "code-review:code-review", args: "the diff"}, true},
		{"/" + strings.Repeat("a", 128), slashCommand{name: strings.Repeat("a", 128)}, true},
		{"/" + strings.Repeat("a", 129), slashCommand{}, false},
		{"/tmp/x is full", slashCommand{}, false},
		{"/", slashCommand{}, false},
		{"/ compact", slashCommand{}, false},
		{"/foo.bar", slashCommand{}, false},
		{"compact", slashCommand{}, false},
		{"please /compact", slashCommand{}, false},
	} {
		got, ok := parseCommand(c.text)
		assert.Equal(t, c.ok, ok, c.text)
		assert.Equal(t, c.want, got, c.text)
	}
}

func TestCommandsOf(t *testing.T) {
	assert.Nil(t, commandsOf(nil), "an older mod lists none")
	assert.Equal(t, map[string]bool{}, commandsOf([]ModCommand{}))
	got := commandsOf([]ModCommand{
		{Name: "hello", Source: "user"},
		{Name: "exit", Source: "builtin"},
		{Name: "code-review", Source: "builtin"},
		{Name: "code-review", Source: "user"},
		{Name: "review", Source: "user"},
		{Name: "review", Source: "plugin"},
		{Name: "plugin:github:github:AssignCodingAgent (MCP)", Source: "mcp"},
		{Name: "odd", Source: "elsewhere"},
	})
	assert.Equal(t, map[string]bool{"hello": false, "exit": true, "code-review": true, "review": false}, got)

	long := make([]ModCommand, maxModCommands+1)
	for i := range long {
		long[i] = ModCommand{Name: fmt.Sprintf("c%d", i), Source: "user"}
	}
	got = commandsOf(long)
	assert.Len(t, got, maxModCommands)
	assert.NotContains(t, got, fmt.Sprintf("c%d", maxModCommands))
}

// sessionCommands is what a mod's hello lists in these tests: a user and a
// plugin command, the three built-ins a reply may run, two built-ins off that
// list, and a name a built-in and a user skill share.
var sessionCommands = []ModCommand{
	{Name: "hello", Source: "user"},
	{Name: "review:pr", Source: "plugin"},
	{Name: "compact", Source: "builtin"},
	{Name: "clear", Source: "builtin"},
	{Name: "model", Source: "builtin"},
	{Name: "exit", Source: "builtin"},
	{Name: "resume", Source: "builtin"},
	{Name: "code-review", Source: "builtin"},
	{Name: "code-review", Source: "user"},
}

// commandInst is modInst whose mod has said hello listing sessionCommands.
func commandInst(t *testing.T, m *Model) *session.Instance {
	t.Helper()
	inst := modInst(t, m, "x")
	require.NoError(t, m.ModHello("L1", ModHello{Mod: 1, Commands: sessionCommands}))
	m.Drain()
	return inst
}

// A reply that is an allowed command goes to the mod as a command, not text,
// and its run comes back to the sender as a notice.
func TestSend_ASlashCommandRunsThroughTheMod(t *testing.T) {
	for _, c := range []struct {
		origin        Origin
		text          string
		command, args string
	}{
		{OriginWeb, "/compact keep the tests", "compact", "keep the tests"},
		{OriginWeb, "/clear", "clear", ""},
		{OriginCLI, "/model haiku", "model", "haiku"},
		{OriginWeb, "/hello", "hello", ""},
		{OriginCLI, "/review:pr 12", "review:pr", "12"},
	} {
		t.Run(c.text, func(t *testing.T) {
			withMod(t)
			m := NewForTest(Options{})
			inst := commandInst(t, m)

			m.Send(m.idOf(inst), Message{Text: c.text, Origin: c.origin}, 5)

			msgs, _, err := m.ModInbox("L1", "")
			require.NoError(t, err)
			require.Len(t, msgs, 1)
			assert.Equal(t, c.command, msgs[0].Command)
			assert.Equal(t, c.args, msgs[0].Args)
			require.NoError(t, m.ModEvents("L1", []ModEvent{{Kind: "queued", ID: msgs[0].ID}, {Kind: "complete", ID: msgs[0].ID}}))
			out := m.Drain()
			assert.Equal(t, []Reply{{Req: 5, ID: m.idOf(inst)}}, replies(out))
			n, ok := noticeFor(out.Events, 5)
			require.True(t, ok, "the run comes back as a notice")
			assert.NoError(t, n.Err)
			assert.Equal(t, strings.TrimSpace("Ran /"+c.command+" "+c.args)+".", n.Info)
		})
	}
}

// Each rule refuses with its reason, and nothing reaches the mod or the pane.
func TestSend_ASlashCommandIsRefusedByTheRules(t *testing.T) {
	for _, c := range []struct {
		name, text, from, want string
		origin                 Origin
	}{
		{"--from", "/compact", "po", "a slash command takes no --from", OriginCLI},
		{"clear with arguments", "/clear all", "", "/clear takes no arguments", OriginWeb},
		{"a bare model", "/model", "", "/model needs a model name", OriginWeb},
		{"a built-in off the list", "/exit", "", "/exit is a built-in loom doesn't run from a reply", OriginWeb},
		{"a picker", "/resume", "", "/resume is a built-in", OriginCLI},
		{"a name a built-in shares", "/code-review", "", "/code-review is a built-in", OriginWeb},
		{"an unknown name", "/quit", "", "this session's Claude has no /quit command", OriginWeb},
	} {
		t.Run(c.name, func(t *testing.T) {
			withMod(t)
			m := NewForTest(Options{})
			inst := commandInst(t, m)

			m.Send(m.idOf(inst), Message{Text: c.text, From: c.from, Origin: c.origin}, 5)

			out := m.Drain()
			rs := replies(out)
			require.Len(t, rs, 1)
			require.ErrorIs(t, rs[0].Err, ErrRefused)
			assert.ErrorContains(t, rs[0].Err, c.want)
			assert.Empty(t, out.Jobs, "nothing is pasted")
			msgs, _, _ := m.ModInbox("L1", "")
			assert.Empty(t, msgs, "nothing reaches the mod")
		})
	}
}

// A command needs the mod's list: a mod whose hello listed none (an older one
// would submit the command as text) and one that has not said hello are
// refused, while a plain message to either still goes.
func TestSend_ASlashCommandNeedsTheModsList(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")

	err := refusalOf(t, m, inst, OriginWeb, "/compact")
	assert.ErrorContains(t, err, "this session's loom mod has not said hello yet")

	require.NoError(t, m.ModHello("L1", ModHello{Mod: 1}))
	m.Drain()
	err = refusalOf(t, m, inst, OriginWeb, "/compact")
	assert.ErrorContains(t, err, "this session's loom mod predates slash commands: pause and resume the session to update it")

	m.Send(m.idOf(inst), Message{Text: "hi", Origin: OriginWeb}, 6)
	msgs, _, _ := m.ModInbox("L1", "")
	require.Len(t, msgs, 1, "a message still goes")
	assert.Empty(t, msgs[0].Command)
}

// A path, a slash further in and a script's slash are messages, as before.
func TestSend_WhatIsNotACommandStaysAMessage(t *testing.T) {
	for _, c := range []struct {
		origin Origin
		text   string
	}{
		{OriginWeb, "/tmp/x is full, clear it"},
		{OriginWeb, "please /compact"},
		{OriginScript, "/compact"},
	} {
		t.Run(c.text, func(t *testing.T) {
			withMod(t)
			m := NewForTest(Options{})
			inst := commandInst(t, m)

			m.Send(m.idOf(inst), Message{Text: c.text, Origin: c.origin}, 5)

			msgs, _, _ := m.ModInbox("L1", "")
			require.Len(t, msgs, 1)
			assert.Empty(t, msgs[0].Command)
			assert.Equal(t, c.text, msgs[0].Text)
		})
	}
}

// A command's run comes back to its sender: as a notice, after its name and
// cut at maxCommandOutput characters; as Answered alone to a waiting CLI; and
// a run that fails after it was queued as an error notice, never pasted.
func TestMod_ACommandsRunComesBackToItsSender(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := commandInst(t, m)
	id := m.idOf(inst)
	m.Send(id, Message{Text: "/model haiku", Origin: OriginWeb}, 5)
	m.Send(id, Message{Text: "/hello", Origin: OriginWeb}, 6)
	m.Send(id, Message{Text: "/clear", Origin: OriginCLI, Wait: true}, 7)
	m.Send(id, Message{Text: "/compact", Origin: OriginWeb}, 8)
	msgs, _, _ := m.ModInbox("L1", "")
	require.Len(t, msgs, 4)
	long := strings.Repeat("é", maxCommandOutput+1)

	require.NoError(t, m.ModEvents("L1", []ModEvent{
		{Kind: "queued", ID: msgs[0].ID}, {Kind: "complete", ID: msgs[0].ID, Text: " Set model to haiku\n"},
		{Kind: "queued", ID: msgs[1].ID}, {Kind: "failed", ID: msgs[1].ID, Error: "boom"},
		{Kind: "queued", ID: msgs[2].ID}, {Kind: "complete", ID: msgs[2].ID},
		{Kind: "queued", ID: msgs[3].ID}, {Kind: "complete", ID: msgs[3].ID, Text: long},
	}))

	out := m.Drain()
	assert.Len(t, replies(out), 4)
	n, _ := noticeFor(out.Events, 5)
	assert.Equal(t, "/model: Set model to haiku", n.Info)
	n, _ = noticeFor(out.Events, 6)
	assert.EqualError(t, n.Err, "send to x: /hello failed: boom")
	assert.Equal(t, []Answered{{Req: 7, ID: id, Text: "Ran /clear."}}, answers(out))
	_, noticed := noticeFor(out.Events, 7)
	assert.False(t, noticed, "a waiting CLI hears its Answered alone")
	n, _ = noticeFor(out.Events, 8)
	assert.Equal(t, "/compact: "+strings.Repeat("é", maxCommandOutput)+"…", n.Info)
	assert.Empty(t, out.Jobs, "nothing is pasted")
}

// A link that ends before a queued command ran tells its sender so.
func TestMod_ACommandWhoseLinkEndsTellsItsSender(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := commandInst(t, m)
	m.Send(m.idOf(inst), Message{Text: "/compact", Origin: OriginWeb}, 5)
	msgs, _, _ := m.ModInbox("L1", "")
	require.NoError(t, m.ModEvents("L1", []ModEvent{{Kind: "queued", ID: msgs[0].ID}}))
	m.Drain()

	m.endLink(inst, m.mods[inst], "the session stopped")

	n, ok := noticeFor(m.Drain().Events, 5)
	require.True(t, ok)
	assert.EqualError(t, n.Err, "send to x: the session stopped before /compact ran")
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `CGO_ENABLED=0 go test ./core -run 'TestParseCommand|TestCommandsOf|SlashCommand|StaysAMessage|ACommand' -count=1`
Expected: FAIL to build: `undefined: slashCommand`, `undefined: parseCommand`, `undefined: ModCommand`.

- [ ] **Step 3: Write `core/mod_command.go`**

```go
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
```

- [ ] **Step 4: Wire it into `core/mod.go`**

`ModHello` gains, after `Loaded`:

```go
	// Commands are the slash commands the session's Claude lists
	// ($.command.list()), with their sources. nil from an older mod, which
	// can't run one, so the model hands it none (commandRefusal).
	Commands []ModCommand `json:"commands,omitempty"`
```

and beside it:

```go
// ModCommand is one slash command a mod's hello lists: its name, without the
// slash, and where Claude says it comes from (builtin, plugin, user or mcp).
type ModCommand struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}
```

`ModMessage` gains:

```go
	// Command, when set, is a slash command's name for the mod to run with
	// Args ($.command.run) in place of submitting Text.
	Command string `json:"command,omitempty"`
	Args    string `json:"args,omitempty"`
```

`modMessage` gains:

```go
	// command and args, for a slash command, are what the mod runs in place
	// of submitting text; its run comes back to the sender (modComplete).
	command, args string
```

`modLink` gains:

```go
	// commands are the slash commands the recorded hello listed, by name:
	// true for a built-in (commandsOf). nil before a hello, or after one
	// that listed none (an older mod).
	commands map[string]bool
```

In `Send`, after `out := &modMessage{…}`:

```go
	if cmd, ok := msg.command(); ok {
		// A launch with no mod is refused below as any message is.
		if l := m.linkFor(inst); l.live != modAbsent {
			if why := commandRefusal(msg, cmd, l); why != "" {
				m.refuse(req, id, fmt.Errorf("send to %s: %s", inst.Title, why))
				return
			}
		}
		out.text, out.command, out.args = msg.Text, cmd.name, cmd.args
	}
```

In `ModHello()`, with `l.poller, l.loaded = …`: `l.commands = commandsOf(h.Commands)`.
In `ModInbox`: `ModMessage{ID: msg.id, Text: msg.text, AsUser: msg.asUser, Command: msg.command, Args: msg.args}`.
In `modQueued`: keep a command for its result: `if msg.command != "" || (msg.wait && msg.req != 0) { l.turning[msg.id] = msg }`.
In `modComplete`, after `delete(l.turning, ev.ID)`:

```go
	if msg.command != "" {
		text := commandResult(msg.command, msg.args, ev.Text)
		if msg.wait {
			m.emit(Answered{Req: msg.req, ID: m.idOf(inst), Text: text})
			return
		}
		m.emit(Notice{Req: msg.req, Info: text})
		return
	}
```

In `modFailed`, for a message in `l.turning`, before its generic notice:

```go
		if msg.command != "" {
			m.emit(Notice{Req: msg.req, Err: fmt.Errorf("send to %s: /%s failed: %s", inst.Title, msg.command, ev.Error)})
			return
		}
```

In `endTurns`, a command's waiter reads `send to %s: %s before /%s ran`.

- [ ] **Step 5: The mod socket decodes the list.** In `internal/modserver/server_test.go`'s `TestHello`, post a hello with `"commands":[{"name":"clear","source":"builtin"}]` and assert the host received `[]core.ModCommand{{Name: "clear", Source: "builtin"}}`.

- [ ] **Step 6: Run the tests**

Run: `CGO_ENABLED=0 go test ./core ./internal/modserver -count=1`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add -- core/mod_command.go core/mod_command_test.go
git commit -m "feat(core): run a reply that is an allowed slash command through the mod" -- core/mod_command.go core/mod_command_test.go core/mod.go internal/modserver/server_test.go
```

### Task 2: the mod runs commands

**Files:**
- Modify: `session/claudemod/mod/hooks/register.ts`
- Modify: `session/claudemod/realclaude_test.go` (`allowedCalls`), `session/claudemod/CLAUDE.md` (the reach rule)
- Modify: `internal/modserver/realclaude_test.go` (commands against real Claude)

- [ ] **Step 1: `register.ts`.** `Message` gains `command?: string; args?: string`. The hello sends `commands: await commandList($)`:

```ts
// commandList is the slash commands this Claude lists, with their sources,
// for the daemon to judge a command message by; none when the list fails, so
// the daemon runs only the built-ins it allows.
async function commandList($: EngineInterface): Promise<{ name: string; source: string }[]> {
  try {
    return (await $.command.list()).map(c => ({ name: c.name, source: c.source }))
  } catch (err) {
    $.ui.log(`loom: list commands: ${String(err)}`, { to: 'debug' })
    return []
  }
}
```

A command message runs, not submits; the pump calls `m.command ? run($, m) : submit($, m)`:

```ts
// run hands a command message to Claude's command queue ($.command.run),
// which runs it once the session is idle: queued as it is handed over, then
// complete with the command's output, or failed. A command starts no turn of
// its own to claim (a skill's run resolves as its turn starts), so it
// reports no started.
async function run($: EngineInterface, m: Message) {
  if (submitted.includes(m.id)) {
    await report($, [{ kind: 'queued', id: m.id }])
    return
  }
  let done: Promise<CommandRunResult>
  try {
    done = $.command.run({ command: m.command ?? '', args: m.args ?? '' })
  } catch (err) {
    await report($, [{ kind: 'failed', id: m.id, error: String(err) }])
    return
  }
  submitted.push(m.id)
  if (submitted.length > KEEP_SUBMITTED) submitted.shift()
  const queued = reportInOrder($, [{ kind: 'queued', id: m.id }])
  done.then(
    r => void reportInOrder($, [{ kind: 'complete', id: m.id, text: r.text ?? '' }]),
    err => void reportInOrder($, [{ kind: 'failed', id: m.id, error: String(err) }]),
  )
  await queued
}
```

- [ ] **Step 2: Reach.** `allowedCalls` gains `"$.command.list"` and `"$.command.run"`; the rule in `session/claudemod/CLAUDE.md` names `$.command` with why (it runs only what the daemon hands it, and the daemon's rules refuse what needs a person).
- [ ] **Step 3: Real Claude.** `queueHost` keeps the hello's commands and queues a command message; `TestRealClaude_TheModCarriesAMessage` then checks the list holds `clear` as `builtin`, `/model haiku` completes, and `/nosuchcmd` fails naming it.
- [ ] **Step 4: Run.** `CGO_ENABLED=0 go test ./session/claudemod ./internal/modserver -count=1`, then `LOOM_TEST_REAL_CLAUDE=1 CGO_ENABLED=0 go test ./session/claudemod ./internal/modserver -run RealClaude -count=1 -v`. Expected: PASS.
- [ ] **Step 5: Commit** by pathspec: `feat(claudemod): list Claude's commands in the hello and run a command message`.

### Task 3: the app, the CLI, the docs

- [ ] The reply box's placeholder (`internal/web/static/js/session.mjs`): `Reply, or /compact, /clear, /model <name>, /<skill>…`.
- [ ] `loom session send`'s help names commands; `--wait`'s says a command prints its output.
- [ ] `USAGE.md`: the phone's reply bullet and the `session send` row; `internal/web/README.md`'s send row; `core/README.md`'s `OriginWeb` sentence; the spec's testing line (the web passes the reply on as is, `TestAPI_SendCarriesTheWebOrigin`, and answers a refusal 409, `TestAPI_ARefusalIs409WithTheModelsText`).
- [ ] Gates: gofmt, `CGO_ENABLED=0 go vet ./...`, `CGO_ENABLED=0 go test ./...`, `CC=clang CGO_ENABLED=1 go test -race ./core ./internal/...`, `go run ./tools/claudemd`, `GOOS=windows CGO_ENABLED=0 go build ./...`, `node --check` on the app's modules.
- [ ] Commit by pathspec: `feat(web,cli): slash commands in the reply box and loom session send; docs`.
