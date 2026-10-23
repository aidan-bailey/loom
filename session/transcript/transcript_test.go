package transcript

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const conv = "2fba5511-2348-4614-a16b-fd369153f8ba"

// tfile builds a transcript in the shapes Claude writes (the census in the
// spec's Findings), never a copied one.
type tfile struct {
	t     *testing.T
	b     strings.Builder
	clock time.Time
}

func newTFile(t *testing.T) *tfile {
	return &tfile{t: t, clock: time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)}
}

// add writes rec as one line, stamped a second after the last, and returns
// its offset.
func (f *tfile) add(rec map[string]any) int64 {
	f.t.Helper()
	f.clock = f.clock.Add(time.Second)
	if _, ok := rec["timestamp"]; !ok {
		rec["timestamp"] = f.clock.Format("2006-01-02T15:04:05.000Z")
	}
	data, err := json.Marshal(rec)
	require.NoError(f.t, err)
	off := int64(f.b.Len())
	f.b.Write(data)
	f.b.WriteByte('\n')
	return off
}

// raw writes s as it is.
func (f *tfile) raw(s string) int64 {
	off := int64(f.b.Len())
	f.b.WriteString(s)
	return off
}

// path writes the transcript as <dir>/<conv>.jsonl.
func (f *tfile) path() string {
	f.t.Helper()
	p := filepath.Join(f.t.TempDir(), conv+".jsonl")
	require.NoError(f.t, os.WriteFile(p, []byte(f.b.String()), 0o600))
	return p
}

func (f *tfile) read(q Query) Page {
	f.t.Helper()
	p, err := Read(f.path(), conv, q)
	require.NoError(f.t, err)
	return p
}

func user(content any, extra map[string]any) map[string]any {
	rec := map[string]any{"type": "user", "cwd": "/w", "isSidechain": false,
		"message": map[string]any{"role": "user", "content": content}}
	for k, v := range extra {
		rec[k] = v
	}
	return rec
}

func typed(text string) map[string]any {
	return user(text, map[string]any{"promptSource": "typed", "origin": map[string]any{"kind": "human"}})
}

func assistant(blocks ...map[string]any) map[string]any {
	content := make([]any, len(blocks))
	for i, b := range blocks {
		content[i] = b
	}
	return map[string]any{"type": "assistant", "cwd": "/w", "isSidechain": false,
		"message": map[string]any{"id": "msg_1", "role": "assistant", "content": content}}
}

func said(text string) map[string]any {
	return assistant(map[string]any{"type": "text", "text": text})
}

func tool(id, name string, input map[string]any) map[string]any {
	return assistant(map[string]any{"type": "tool_use", "id": id, "name": name, "input": input})
}

func bash(id, command string) map[string]any {
	return tool(id, "Bash", map[string]any{"command": command, "description": "run it"})
}

func result(id string, isError bool, content any) map[string]any {
	return user([]any{map[string]any{"type": "tool_result", "tool_use_id": id, "is_error": isError, "content": content}},
		map[string]any{"toolUseResult": map[string]any{"stdout": "…"}})
}

func boundary() map[string]any {
	return map[string]any{"type": "system", "subtype": "compact_boundary", "content": "Conversation compacted"}
}

func command(name, args string) map[string]any {
	return user("<command-name>"+name+"</command-name>\n            <command-message>"+strings.TrimPrefix(name, "/")+
		"</command-message>\n            <command-args>"+args+"</command-args>", nil)
}

func texts(turns []Turn) []string {
	out := []string{}
	for _, t := range turns {
		out = append(out, t.Text)
	}
	return out
}

func kinds(items []Item) []string {
	out := []string{}
	for _, it := range items {
		out = append(out, it.Kind)
	}
	return out
}

func states(steps []Step) []string {
	out := []string{}
	for _, s := range steps {
		out = append(out, s.State)
	}
	return out
}

func summaries(steps []Step) []string {
	out := []string{}
	for _, s := range steps {
		out = append(out, s.Summary)
	}
	return out
}

func TestRead_ATurnIsItsPromptRepliesAndSteps(t *testing.T) {
	f := newTFile(t)
	f.add(map[string]any{"type": "permission-mode", "permissionMode": "default"})
	at := f.add(typed("  run the race detector  "))
	f.add(assistant(map[string]any{"type": "thinking", "thinking": "", "signature": "s"}))
	f.add(bash("t1", "go test -race ./core/...\nmore"))
	f.add(result("t1", false, "ok"))
	f.add(tool("t2", "Read", map[string]any{"file_path": "/w/core/hub.go"}))
	f.add(result("t2", true, "<tool_use_error>File does not exist.</tool_use_error>"))
	f.add(said("Fixed the race in `hub.flush`."))
	f.add(bash("t3", "gh pr checks 318 --watch"))

	p := f.read(Query{})
	assert.Equal(t, conv, p.Conversation)
	assert.Zero(t, p.Earlier, "the page starts the file")
	assert.False(t, p.Reset)
	assert.Zero(t, p.Skipped)
	require.Len(t, p.Turns, 1)
	turn := p.Turns[0]
	assert.Equal(t, at, turn.ID)
	assert.False(t, turn.At.IsZero())
	assert.Equal(t, FromYou, turn.From)
	assert.Equal(t, "run the race detector", turn.Text, "trimmed")
	assert.False(t, turn.Command)
	assert.False(t, turn.Partial)
	require.Equal(t, []string{KindSteps, KindText, KindSteps}, kinds(turn.Items))
	first := turn.Items[0]
	assert.Equal(t, 2, first.Total)
	assert.Equal(t, 1, first.Failed)
	assert.Equal(t, []string{"go test -race ./core/...", "core/hub.go"}, summaries(first.Steps),
		"a command's first line, and a path under the record's cwd made relative")
	assert.Equal(t, []string{StateDone, StateFailed}, states(first.Steps))
	assert.Equal(t, "Fixed the race in `hub.flush`.", turn.Items[1].Text)
	assert.Equal(t, []string{StatePending}, states(turn.Items[2].Steps), "no result yet")
}

func TestRead_TheOriginLabelsATurn(t *testing.T) {
	f := newTFile(t)
	f.add(user("Another Claude session sent a message: <teammate-message>story 3 is drafted</teammate-message>",
		map[string]any{"isMeta": true, "promptSource": "system",
			"origin": map[string]any{"kind": "peer", "name": "drafter", "body": "story 3 is drafted", "from": "a1"}}))
	f.add(user("yes, merge it", map[string]any{"origin": map[string]any{"kind": "plugin", "name": "loom", "asUser": true}}))
	f.add(user("status?", map[string]any{"origin": map[string]any{"kind": "plugin", "name": "loom"}}))
	f.add(user("<task-notification>\n<task-id>b1</task-id>\n<status>completed</status>\n<summary>Tests passed</summary>\n</task-notification>",
		map[string]any{"promptSource": "system", "origin": map[string]any{"kind": "task-notification"}}))
	f.add(user("Another Claude session sent a message: done", nil))
	f.add(user([]any{map[string]any{"type": "text", "text": "Base directory for this skill: /skills/x"}}, map[string]any{"isMeta": true}))
	f.add(user("plain", map[string]any{"promptSource": "queued", "origin": map[string]any{"kind": "human"}}))

	type label struct{ from, name, text string }
	var got []label
	for _, turn := range f.read(Query{}).Turns {
		got = append(got, label{turn.From, turn.Name, turn.Text})
	}
	assert.Equal(t, []label{
		{FromPeer, "drafter", "story 3 is drafted"},
		{FromYou, "", "yes, merge it"},
		{FromPlugin, "loom", "status?"},
		{FromTask, "", "completed: Tests passed"},
		{FromPeer, "", "Another Claude session sent a message: done"},
		{FromYou, "", "plain"},
	}, got, "a meta record starts a turn only as a peer's")
}

func TestRead_CommandsAndTheCompactRepeat(t *testing.T) {
	f := newTFile(t)
	compact := f.add(user("/compact", nil))
	f.add(boundary())
	f.add(user("This session is being continued from a previous conversation.",
		map[string]any{"isCompactSummary": true, "isVisibleInTranscriptOnly": true}))
	f.add(user("<local-command-caveat>Caveat: the messages below were generated by the user.</local-command-caveat>", map[string]any{"isMeta": true}))
	f.add(command("/compact", ""))
	f.add(user("<local-command-stdout>Compacted</local-command-stdout>", nil))
	docs := f.add(typed("now the docs"))
	f.add(boundary())
	older := f.add(command("/compact", ""))
	skill := f.add(user("<command-message>flow-supervise is running…</command-message>\n<command-name>/flow-supervise</command-name>\n<command-args>docs/plan.md</command-args>",
		map[string]any{"origin": map[string]any{"kind": "human"}}))
	shell := f.add(user("<bash-input>ls -la</bash-input>", nil))
	f.add(user("<bash-stdout>total 0</bash-stdout><bash-stderr></bash-stderr>", nil))
	path := f.add(typed("/tmp/x is broken"))

	type turnOf struct {
		id      int64
		text    string
		command bool
		items   []string
	}
	var got []turnOf
	for _, turn := range f.read(Query{}).Turns {
		got = append(got, turnOf{turn.ID, turn.Text, turn.Command, kinds(turn.Items)})
	}
	assert.Equal(t, []turnOf{
		{compact, "/compact", true, []string{KindCompacted}},
		{docs, "now the docs", false, []string{KindCompacted}},
		{older, "/compact", true, []string{}},
		{skill, "/flow-supervise docs/plan.md", true, []string{}},
		{shell, "! ls -la", true, []string{}},
		{path, "/tmp/x is broken", false, []string{}},
	}, got, "the command record after a /compact's boundary repeats it; one after an idle compaction does not")
}

func TestRead_ACompactRepeatTakesNoTurnsPlaceInAPage(t *testing.T) {
	f := newTFile(t)
	for i := 1; i <= 9; i++ {
		f.add(typed(fmt.Sprintf("p%d", i)))
		f.add(said("ok"))
	}
	f.add(user("/compact", nil))
	f.add(boundary())
	f.add(user("This session is being continued from a previous conversation.",
		map[string]any{"isCompactSummary": true, "isVisibleInTranscriptOnly": true}))
	f.add(command("/compact", ""))
	f.add(typed("last"))

	p := f.read(Query{})
	assert.Len(t, p.Turns, maxTurns, "the repeated /compact counts as no turn of the page")
	assert.Equal(t, "p2", p.Turns[0].Text)
}

func TestRead_LeavesOutWhatIsNotTheConversation(t *testing.T) {
	f := newTFile(t)
	f.add(map[string]any{"type": "mode", "mode": "normal"})
	f.add(typed("q"))
	f.add(map[string]any{"type": "attachment", "attachment": map[string]any{"type": "hook_additional_context"}})
	side := said("a subagent's words")
	side["isSidechain"] = true
	f.add(side)
	f.add(map[string]any{"type": "system", "subtype": "turn_duration", "durationMs": 10})
	f.add(map[string]any{"type": "system", "subtype": "local_command", "content": "<command-name>/remote-control</command-name>"})
	f.add(said("   "))
	f.add(map[string]any{"type": "api-request-blob", "message": "a string where a record holds an object"})
	f.add(user("<local-command-stdout>Set model to haiku</local-command-stdout>", nil))
	f.raw("\n")
	failed := said("API Error: 529 overloaded\nretrying")
	failed["isApiErrorMessage"] = true
	f.add(failed)

	p := f.read(Query{})
	assert.Zero(t, p.Skipped, "a JSON line of another shape, and an empty line, are no unreadable records")
	require.Len(t, p.Turns, 1)
	require.Len(t, p.Turns[0].Items, 1)
	note := p.Turns[0].Items[0]
	assert.Equal(t, KindNote, note.Kind)
	assert.True(t, note.Error)
	assert.Equal(t, "API Error: 529 overloaded", note.Text)
}

func TestRead_StepStatesAndTheNewestTwoHundred(t *testing.T) {
	f := newTFile(t)
	f.add(typed("go"))
	f.add(bash("a", "first"))
	f.add(result("a", true, "boom"))
	for i := 0; i < 203; i++ {
		id := fmt.Sprintf("s%d", i)
		f.add(bash(id, "step"))
		f.add(result(id, false, "ok"))
	}
	f.add(bash("b", "sleep 99"))
	f.add(result("b", true, "[Request interrupted by user for tool use]"))
	f.add(bash("c", "sleep 99"))
	f.add(result("c", true, []any{map[string]any{"type": "text", "text": "[Request interrupted by user for tool use]"}}))
	f.add(user([]any{map[string]any{"type": "text", "text": "[Request interrupted by user]"}}, nil))

	items := f.read(Query{}).Turns[0].Items
	require.Equal(t, []string{KindSteps, KindNote}, kinds(items))
	steps := items[0]
	assert.Equal(t, 206, steps.Total)
	assert.Equal(t, 1, steps.Failed, "an interruption is no failure, and a failure past the newest 200 still counts")
	require.Len(t, steps.Steps, maxSteps)
	assert.Equal(t, "c", steps.Steps[maxSteps-1].ID)
	assert.Equal(t, []string{StateInterrupted, StateInterrupted}, states(steps.Steps[maxSteps-2:]))
	assert.Equal(t, "interrupted", items[1].Text)
	assert.False(t, items[1].Error)
}

func TestSummarize(t *testing.T) {
	long := strings.Repeat("x", 300)
	for _, tc := range []struct {
		tool  string
		input map[string]any
		want  string
	}{
		{"Bash", map[string]any{"command": "make\nmake install"}, "make"},
		{"Read", map[string]any{"file_path": "/w/a/b.go"}, "a/b.go"},
		{"Edit", map[string]any{"file_path": "/elsewhere/b.go"}, "/elsewhere/b.go"},
		{"Write", map[string]any{"file_path": "/w"}, "/w"},
		{"MultiEdit", map[string]any{"file_path": "/w/m.go"}, "m.go"},
		{"NotebookEdit", map[string]any{"notebook_path": "/w/n.ipynb"}, "n.ipynb"},
		{"Grep", map[string]any{"pattern": "func Read"}, "func Read"},
		{"Glob", map[string]any{"pattern": "**/*.go"}, "**/*.go"},
		{"WebFetch", map[string]any{"url": "https://go.dev"}, "https://go.dev"},
		{"WebSearch", map[string]any{"query": "goldmark"}, "goldmark"},
		{"ToolSearch", map[string]any{"query": "select:Read"}, "select:Read"},
		{"Agent", map[string]any{"description": "Review the change"}, "Review the change"},
		{"Task", map[string]any{"description": "Its older name"}, "Its older name"},
		{"Skill", map[string]any{"skill": "loom-dev"}, "loom-dev"},
		{"SendMessage", map[string]any{"to": "x"}, ""},
		{"Bash", map[string]any{"command": 3}, ""},
		{"Bash", map[string]any{"command": long}, strings.Repeat("x", 199) + "…"},
	} {
		input, err := json.Marshal(tc.input)
		require.NoError(t, err)
		assert.Equal(t, tc.want, summarize(tc.tool, input, "/w"), "%s %v", tc.tool, tc.input)
	}
}

func TestRead_CutsAtARuneBoundary(t *testing.T) {
	f := newTFile(t)
	f.add(typed(strings.Repeat("a", maxPrompt-1) + "é" + "tail"))
	f.add(said(strings.Repeat("b", maxItem-1) + "é" + "tail"))
	turn := f.read(Query{}).Turns[0]
	assert.True(t, turn.Cut)
	assert.Equal(t, strings.Repeat("a", maxPrompt-1), turn.Text)
	assert.True(t, turn.Items[0].Cut)
	assert.Equal(t, strings.Repeat("b", maxItem-1), turn.Items[0].Text)
}

func TestRead_PagesAreNewestFirst(t *testing.T) {
	f := newTFile(t)
	f.add(user("<local-command-caveat>Caveat</local-command-caveat>", map[string]any{"isMeta": true}))
	var ids []int64
	for i := 1; i <= 12; i++ {
		ids = append(ids, f.add(typed(fmt.Sprintf("p%d", i))))
		f.add(said(fmt.Sprintf("a%d", i)))
	}
	path := f.path()

	newest, err := Read(path, conv, Query{})
	require.NoError(t, err)
	require.Len(t, newest.Turns, maxTurns)
	assert.Equal(t, "p3", newest.Turns[0].Text)
	assert.Equal(t, "p12", newest.Turns[maxTurns-1].Text)
	assert.Equal(t, ids[2], newest.Earlier)

	before, err := Read(path, conv, Query{Before: newest.Earlier})
	require.NoError(t, err)
	assert.Equal(t, []string{"p1", "p2"}, texts(before.Turns))
	assert.Zero(t, before.Earlier, "the caveat before the first turn makes no earlier page")
}

func TestRead_APageKeepsWithinItsTextButHoldsItsNewestTurn(t *testing.T) {
	big := strings.Repeat("w", 60<<10)
	f := newTFile(t)
	var ids []int64
	for i := 1; i <= 5; i++ {
		ids = append(ids, f.add(typed(fmt.Sprintf("p%d", i))))
		f.add(said(big))
		f.add(said(big))
	}
	p := f.read(Query{})
	assert.Equal(t, []string{"p4", "p5"}, texts(p.Turns), "a third turn would take the page past 256 KiB")
	assert.Equal(t, ids[3], p.Earlier)

	g := newTFile(t)
	g.add(typed("one"))
	for i := 0; i < 5; i++ {
		g.add(said(big))
	}
	q := g.read(Query{})
	assert.Equal(t, []string{"one"}, texts(q.Turns), "the newest turn joins however large")
	assert.Len(t, q.Turns[0].Items, 5)
}

func TestRead_AfterReadsFromATurnToTheEnd(t *testing.T) {
	f := newTFile(t)
	var ids []int64
	for i := 1; i <= 12; i++ {
		ids = append(ids, f.add(typed(fmt.Sprintf("p%d", i))))
		f.add(said("ok"))
	}
	mid := f.add(bash("t1", "make"))
	f.add(result("t1", false, "ok"))
	path := f.path()

	p, err := Read(path, conv, Query{Conversation: conv, After: ids[1]})
	require.NoError(t, err)
	assert.Len(t, p.Turns, 11, "every turn from the one at after, past a page's 10")
	assert.Equal(t, "p2", p.Turns[0].Text)
	assert.Zero(t, p.Earlier, "a refresh's page has no earlier")
	assert.False(t, p.Reset)

	q, err := Read(path, conv, Query{After: mid})
	require.NoError(t, err)
	require.Len(t, q.Turns, 1)
	assert.True(t, q.Turns[0].Partial, "records before a turn starts there make a partial turn")
	assert.Equal(t, mid, q.Turns[0].ID)
	assert.Equal(t, []string{StateDone}, states(q.Turns[0].Items[0].Steps))
}

func TestRead_ATurnPastTheBudgetIsPartial(t *testing.T) {
	f := newTFile(t)
	f.add(typed("one"))
	f.add(said("first answer"))
	two := f.add(typed("two"))
	for i := 0; i < 40; i++ {
		id := fmt.Sprintf("t%d", i)
		f.add(bash(id, "step"))
		f.add(result(id, false, "ok"))
	}
	path := f.path()
	old := scanBudget
	scanBudget = 4 << 10
	t.Cleanup(func() { scanBudget = old })

	p, err := Read(path, conv, Query{})
	require.NoError(t, err)
	require.Len(t, p.Turns, 1)
	turn := p.Turns[0]
	assert.True(t, turn.Partial)
	assert.Empty(t, turn.Text)
	assert.Positive(t, p.Earlier)
	assert.Equal(t, turn.ID, p.Earlier, "the page before ends where this one begins")

	r, err := Read(path, conv, Query{After: two})
	require.NoError(t, err)
	assert.True(t, r.Reset, "a turn out of a read's reach is a reset")
}

func TestRead_UnreadableLinesAreCountedAndALineBeingWrittenIsNot(t *testing.T) {
	old := lineLimit
	lineLimit = 400
	t.Cleanup(func() { lineLimit = old })
	f := newTFile(t)
	f.add(typed("q"))
	f.raw("not json at all\n")
	f.add(said(strings.Repeat("z", 600)))
	f.add(said("short"))
	f.raw(`{"type":"assistant","message":{"content":[{"type":"text","text":"half`)

	p := f.read(Query{})
	assert.Equal(t, 2, p.Skipped)
	require.Len(t, p.Turns, 1)
	assert.Equal(t, []string{KindText}, kinds(p.Turns[0].Items))
	assert.Equal(t, "short", p.Turns[0].Items[0].Text)
}

func TestRead_AQueryItCannotServeIsTheNewestPageReset(t *testing.T) {
	f := newTFile(t)
	f.add(typed("p1"))
	f.add(said("a1"))
	path := f.path()
	for name, q := range map[string]Query{
		"another conversation": {Conversation: "an-earlier-conversation"},
		"before past the end":  {Before: 1 << 30},
		"after past the end":   {After: 1 << 30},
	} {
		p, err := Read(path, conv, q)
		require.NoError(t, err, name)
		assert.True(t, p.Reset, name)
		assert.Equal(t, []string{"p1"}, texts(p.Turns), name)
	}
	_, err := Read(path, conv, Query{Before: 5, After: 5})
	assert.ErrorContains(t, err, "before or after")
}

func TestRead_RefusesAPathThatIsNotTheTranscript(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, conv+".jsonl")
	require.NoError(t, os.WriteFile(good, nil, 0o600))
	sub := filepath.Join(dir, "d.jsonl")
	require.NoError(t, os.Mkdir(sub, 0o700))

	p, err := Read(good, conv, Query{})
	require.NoError(t, err, "an empty transcript is no error")
	assert.Empty(t, p.Turns)

	for name, tc := range map[string]struct{ path, conversation, want string }{
		"relative":        {conv + ".jsonl", conv, "not an absolute path"},
		"another name":    {good, "other", "not the transcript of conversation"},
		"no conversation": {good, "", "not the transcript of conversation"},
		"a directory":     {sub, "d", "not a regular file"},
		"missing":         {filepath.Join(dir, "gone.jsonl"), "gone", "no such file"},
	} {
		_, err := Read(tc.path, tc.conversation, Query{})
		assert.ErrorContains(t, err, tc.want, name)
	}
}
