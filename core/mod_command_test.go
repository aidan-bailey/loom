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
