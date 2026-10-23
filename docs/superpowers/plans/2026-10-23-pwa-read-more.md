# PWA conversation and diff Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the phone read a session's conversation (its turns from Claude's transcript: prompts, Claude's replies as markdown, tool calls as one-line steps) and its worktree's diff, file by file, on two new screens reached from the session screen.

**Architecture:** Two new `core.Core` requests answered by model jobs, each Reply going to the requester alone: `ReadTurns` reads a page of the transcript through a new pure reader, `session/transcript`, and `ReadDiff` reads the worktree's patch through a new `GitWorktree.Patch` (stdout only, the user's diff config pinned) split by `git.SplitPatch`, never touching the diff stats the views publish. The web layer adds two read routes that wait for the Reply and stop tracking it; the app adds a conversation screen and a changes screen, and links them from the session screen.

**Tech Stack:** Go 1.25 (`go.mod`), encoding/json, testify; Preact + htm (pinned file), plain ES modules (`.mjs`) and CSS.

**Spec:** `docs/superpowers/specs/2026-10-23-pwa-read-more-design.md`. **Mockups:** https://claude.ai/artifact/Dv1GwHHRw8cJefivtMGvyh (third row).

---

## Rules for every task

These come from the repo's `CLAUDE.md` files; they hold for every step.

- **Never run loom itself (`./loom`, `go run .`) or `loom serve stop` in a loom pane.** Try the app on the phone only through the `loom-dev` skill (`go run ./tools/loomdev`).
- **Keep scratch files out of the worktree**, and commit with explicit pathspecs: `git add -- <paths>` and `git commit -- <paths>`, never `-a`, `git add -A` or `.`. Loom runs `git add -N .` in this worktree on every diff probe, so a scratch file that lands here even briefly keeps an index entry.
- **Tests:** `CGO_ENABLED=0 go test ./...` (a plain `go test` fails where only clang is installed). Format with `gofmt -w` on the files you touched, never `gofmt -w .` (it rewrites `vendor/`).
- **Never edit generated files:** `core/rpc/methods_gen.go` (`go generate ./core/rpc`) and `docs/specs/protocol.md` (`go test ./core/rpc -run TestProtocolReference -update`).
- **The app:** modules are `.mjs`; no `style` attribute and no inline script anywhere (the CSP is `default-src 'self'`); the app's JSON only gains fields; never insert an HTML string into the page; Claude's text is drawn only from its tree.
- **Never log a token, a cookie, a pairing code, a push subscription, a turn's text or a patch's content.**
- **Writing files:** the security hook blocks a Write whose text holds the word exec followed by an open paren (a regexp's method included: use `String.prototype.match`), or React's raw-HTML prop name. Neither appears in this plan's code; keep it that way.
- **Commits** are conventional, scoped by package, and end with:

  ```
  Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_019icsHbrFq5LeZ4b4jFkSBe
  ```

## Settled while planning

These refine the spec, which is updated to match in the same commit as this plan:

- **A page's text bound.** A turn joins a page while the page holds fewer than 10 turns and the turn's text keeps the page within 256 KiB; the newest turn always joins. (The spec said reading "stops once it holds 256 KiB".)
- **A refresh's page has no `earlier`** (0, `null` in JSON): the app keeps the cursor its earlier pages gave.
- **`skipped` counts lines that are not JSON or are too long.** A JSON line of a shape the reader doesn't read, and an empty line, add nothing and are not counted.
- **A partial turn's ID is the first line read**, which is also the page's `earlier`, so the page before ends exactly where it begins.
- **The web layer stops tracking a read's request once its handler is done (`reqs.drop`).** A tracked request keeps its Reply for `reqKeep` (15 minutes, up to `maxReqs`), which for a diff is up to 1 MiB each, and a Reply that arrived after the wait gave up would reach the stream as a notice.
- **The reply box and the line under a session's title become shared** (`js/reply.mjs`'s `ReplyBox`, `js/session.mjs`'s `SubLine`), so the conversation screen ends in the same box.
- **Step summaries cut a path under the record's `cwd` to a relative one**, using the record's own `cwd` field.

## File map

| File | Change |
|---|---|
| `session/hooks/event.go` | `capSummary` exported as `CapSummary` |
| `session/transcript/transcript.go` (new) | `Query`, `Page`, `Turn`, `Item`, `Step` and their constants; `Read`; the file check; the pages |
| `session/transcript/scan.go` (new) | `lastLineEnd`, `scanBack`: lines read backward in chunks within a budget |
| `session/transcript/records.go` (new) | one line decoded into what it adds (`entry`); the summary table |
| `session/transcript/build.go` (new) | entries in file order folded into turns |
| `session/transcript/transcript_test.go`, `scan_test.go`, `realclaude_test.go` (new) | the reader's tests; the opt-in real-Claude test |
| `session/git/patch.go`, `patch_test.go` (new) | `Patch`, `FilePatch`, `Hunk`, `GitWorktree.Patch`, `SplitPatch` |
| `session/patch.go`, `patch_test.go` (new) | `Instance.ReadPatch` |
| `core/iface.go`, `core/loop_core.go` | `ReadTurns`, `ReadDiff` and their forwarders |
| `core/events.go` | `Reply.Turns`, `Reply.Diff` |
| `core/requests.go`, `core/model.go` | `opReadTurns`, `opReadDiff` in `precondition`; the results in `deliverTracked`, `outcome` and `Deliver` |
| `core/reads.go`, `reads_test.go` (new) | the two requests and their jobs |
| `core/requests_test.go` | the refusals |
| `core/rpc/methods_gen.go`, `docs/specs/protocol.md` | regenerated |
| `internal/modserver/realclaude_test.go` | `m1` sent `asUser`; the transcript read back |
| `internal/web/reqs.go` | `reqs.drop` |
| `internal/web/reads.go`, `reads_test.go` (new) | the read routes, their JSON and tests |
| `internal/web/api.go`, `fleet.go` | the routes registered; `sessionJSON.Claude` |
| `internal/web/static_test.go` | the `--add` and `--del` tokens |
| `internal/web/static/js/reply.mjs`, `turns.mjs`, `changes.mjs` (new) | the reply box; the conversation and changes screens |
| `internal/web/static/js/session.mjs`, `app.mjs`, `screens.mjs`, `skin.mjs` | the ways in, `SubLine`, `ReplyBox`; the routes; the screens; `ChevronIcon` |
| `internal/web/static/app.css` | the two tokens and the screens' rules |
| `session/README.md`, `session/CLAUDE.md`, `session/git/CLAUDE.md`, `core/README.md`, `internal/web/README.md`, `internal/web/CLAUDE.md`, `docs/claude/web-layer.md`, `USAGE.md` | docs |

Four tasks, one commit each: A (the transcript reader), B (the patch), C (the core requests), D (the web layer, the app and the docs). A and B are independent; C needs both; D needs C. Each leaves the tree building and every test passing.

---

### Task A: The transcript reader

**Files:**
- Modify: `session/hooks/event.go`
- Create: `session/transcript/transcript.go`, `scan.go`, `records.go`, `build.go`
- Create: `session/transcript/transcript_test.go`, `scan_test.go`, `realclaude_test.go`
- Modify: `session/README.md`, `session/CLAUDE.md`

- [ ] **Step 1: Export the summary cut**

In `session/hooks/event.go`, rename `capSummary` to `CapSummary` (its definition and its two calls, `ev.ToolSummary = capSummary(w.ToolSummary)` and `return capSummary(s)`), and start its doc comment with the new name:

```go
// CapSummary reduces s to its first line that has any text, trimmed, and
// cuts that to MaxToolSummaryRunes characters, ending in "…" when it cut.
// It is idempotent, which ParseEvent relies on when it reads a compact
// event's tool_summary back; a transcript's step summaries use it too.
func CapSummary(s string) string {
```

Run: `CGO_ENABLED=0 go test ./session/hooks/`
Expected: PASS.

- [ ] **Step 2: Write the failing reader tests**

Create `session/transcript/scan_test.go`:

```go
package transcript

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type scanned struct {
	off  int64
	data string
	long bool
}

func scanAll(t *testing.T, r *strings.Reader, from, to, budget int64) ([]scanned, bool) {
	t.Helper()
	var got []scanned
	reached, err := scanBack(r, from, to, budget, func(off int64, data []byte) bool {
		got = append(got, scanned{off, string(data), data == nil})
		return true
	})
	require.NoError(t, err)
	return got, reached
}

func TestScanBack_ReadsLinesAcrossChunks(t *testing.T) {
	lines := []string{strings.Repeat("a", 100<<10), "", "b", strings.Repeat("c", chunk-1), strings.Repeat("d", chunk), "e"}
	var b strings.Builder
	var offs []int64
	for _, l := range lines {
		offs = append(offs, int64(b.Len()))
		b.WriteString(l)
		b.WriteByte('\n')
	}
	r := strings.NewReader(b.String())
	end := int64(b.Len())

	got, reached := scanAll(t, r, 0, end, 1<<30)
	assert.True(t, reached)
	require.Len(t, got, len(lines))
	for i, s := range got {
		j := len(lines) - 1 - i
		assert.Equal(t, scanned{offs[j], lines[j], false}, s, "line %d, newest first", j)
	}

	got, reached = scanAll(t, r, offs[2], end, 1<<30)
	assert.True(t, reached)
	assert.Len(t, got, 4, "from a line's start: that line and the ones after it")

	got, reached = scanAll(t, r, 0, end, 3)
	assert.False(t, reached, "the budget ran out")
	assert.Equal(t, []scanned{{offs[5], "e", false}}, got)
}

func TestScanBack_ALineLongerThanTheLimitIsNil(t *testing.T) {
	old := lineLimit
	lineLimit = 1000
	t.Cleanup(func() { lineLimit = old })
	text := strings.Repeat("x", 5000) + "\nshort\n"
	got, reached := scanAll(t, strings.NewReader(text), 0, int64(len(text)), 1<<30)
	assert.True(t, reached)
	assert.Equal(t, []scanned{{5001, "short", false}, {0, "", true}}, got)
}

func TestLastLineEnd_IgnoresALineBeingWritten(t *testing.T) {
	text := "one\ntwo\nthr"
	end, err := lastLineEnd(strings.NewReader(text), int64(len(text)))
	require.NoError(t, err)
	assert.Equal(t, int64(8), end)
	end, err = lastLineEnd(strings.NewReader("no newline"), 10)
	require.NoError(t, err)
	assert.Zero(t, end)
}
```

Create `session/transcript/transcript_test.go`:

```go
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
```

- [ ] **Step 3: Run them and see them fail**

Run: `CGO_ENABLED=0 go test ./session/transcript/`
Expected: FAIL to build (`undefined: Read`, `undefined: scanBack`, …).

- [ ] **Step 4: Write the reader**

Create `session/transcript/transcript.go`:

```go
// Package transcript reads one page of a Claude Code conversation from the
// transcript Claude writes for it, <config>/projects/<encoded cwd>/<session
// ID>.jsonl: its turns (a prompt, then Claude's replies and its tool calls
// as one-line steps), newest first, in pages bounded in turns, text and
// bytes read. It only reads. Claude's format is its own and undocumented,
// so a record the reader does not recognise adds nothing to a page.
package transcript

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// A page's bounds. A turn joins a page while the page holds fewer than
// maxTurns turns and the turn's text keeps the page within maxText bytes;
// the newest turn always joins. A prompt keeps its first maxPrompt bytes, a
// text item its first maxItem, and a run of steps its newest maxSteps.
const (
	maxTurns  = 10
	maxText   = 256 << 10
	maxPrompt = 16 << 10
	maxItem   = 64 << 10
	maxSteps  = 200
)

// A read takes at most scanBudget bytes of the file, and skips a line
// longer than lineLimit. Tests shrink them.
var (
	scanBudget int64 = 16 << 20
	lineLimit  int64 = 4 << 20
)

// Query says which turns to read.
type Query struct {
	// Conversation is the conversation the caller holds turns of, "" for
	// none. Another than the transcript's is answered with the newest
	// page, marked Reset.
	Conversation string
	// Before > 0 reads the page before this offset (a page's Earlier).
	Before int64
	// After > 0 reads every turn from the one at this offset (a turn's ID)
	// to the end of the file.
	After int64
}

// Page is the turns a query read, oldest first.
type Page struct {
	// Conversation is the transcript's session ID.
	Conversation string
	Turns        []Turn
	// Earlier is the Before of the page before this one, 0 when this page
	// starts the file and on a refresh's page (Query.After), whose caller
	// keeps the cursor its earlier pages gave.
	Earlier int64
	// Reset is set when the page is the newest page in place of what the
	// query asked for: the caller held another conversation's turns, or
	// its cursor lies past the end of the file or out of a read's reach.
	Reset bool
	// Skipped counts the page's lines that could not be read: not JSON,
	// or longer than lineLimit.
	Skipped int
}

// Turn is one prompt and what Claude did with it.
type Turn struct {
	// ID is the byte offset of the turn's first record. Claude only
	// appends, so it names the turn for good.
	ID int64
	At time.Time
	// From says whose the prompt is: FromYou, FromPlugin, FromPeer or
	// FromTask; Name is the plugin's or the peer's name.
	From string
	Name string
	// Text is the prompt, trimmed, its first maxPrompt bytes; Cut says it
	// was cut.
	Text    string
	Cut     bool
	Command bool // a slash command, or a shell command ("! …")
	// Partial marks the part of a turn whose start lies before what was
	// read: no Text, its ID the first line read.
	Partial bool
	Items   []Item
}

// Who a turn's prompt came from.
const (
	FromYou    = "you"
	FromPlugin = "plugin"
	FromPeer   = "peer"
	FromTask   = "task"
)

// Item is one thing Claude's side of a turn holds.
type Item struct {
	Kind string // KindText, KindSteps, KindCompacted or KindNote
	// At is its record's timestamp; a steps item's is its first step's.
	At time.Time
	// Text is a text item's markdown (its first maxItem bytes; Cut says it
	// was cut), or a note's line.
	Text  string
	Cut   bool
	Error bool // a note that is an API error
	// Steps are a steps item's newest maxSteps steps; Total and Failed
	// count every step of the run.
	Steps  []Step
	Total  int
	Failed int
}

// The kinds of Item.
const (
	KindText      = "text"
	KindSteps     = "steps"
	KindCompacted = "compacted"
	KindNote      = "note"
)

// Step is one tool call.
type Step struct {
	ID      string // the tool_use block's id
	Tool    string
	Summary string
	At      time.Time
	State   string // StatePending, StateDone, StateFailed or StateInterrupted
}

// The states of a Step.
const (
	StatePending     = "pending"
	StateDone        = "done"
	StateFailed      = "failed"
	StateInterrupted = "interrupted"
)

// Read reads the page q asks for from the transcript at path, which belongs
// to conversation (its session ID). It refuses a path that is not
// absolute, not <conversation>.jsonl or not a regular file, so a bad hook
// payload can't point it at another file. It opens the file read-only and
// reads nothing past the last newline it finds: a line after that is one
// Claude is still writing.
func Read(path, conversation string, q Query) (Page, error) {
	if q.Before > 0 && q.After > 0 {
		return Page{}, errors.New("transcript: a query takes before or after, not both")
	}
	f, size, err := open(path, conversation)
	if err != nil {
		return Page{}, err
	}
	defer f.Close()
	page, err := read(f, size, conversation, q)
	if err != nil {
		return Page{}, fmt.Errorf("transcript %s: %w", path, err)
	}
	page.Conversation = conversation
	return page, nil
}

// open opens the transcript once its path passes the checks, and returns
// its size.
func open(path, conversation string) (*os.File, int64, error) {
	switch {
	case !filepath.IsAbs(path):
		return nil, 0, fmt.Errorf("transcript %s: not an absolute path", path)
	case conversation == "" || filepath.Base(path) != conversation+".jsonl":
		return nil, 0, fmt.Errorf("transcript %s: not the transcript of conversation %q", path, conversation)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("transcript: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, fmt.Errorf("transcript: %w", err)
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, 0, fmt.Errorf("transcript %s: not a regular file", path)
	}
	return f, info.Size(), nil
}

// read reads q's page from r, whose first size bytes are the transcript.
func read(r io.ReaderAt, size int64, conversation string, q Query) (Page, error) {
	end, err := lastLineEnd(r, size)
	if err != nil {
		return Page{}, err
	}
	other := q.Conversation != "" && q.Conversation != conversation
	if other || q.Before > end || (q.After > 0 && q.After >= end) {
		return pageBefore(r, end, true)
	}
	if q.After > 0 {
		page, ok, err := since(r, end, q.After)
		if err != nil || ok {
			return page, err
		}
		return pageBefore(r, end, true)
	}
	to := end
	if q.Before > 0 {
		to = q.Before
	}
	return pageBefore(r, to, false)
}

// pageBefore reads the newest page of the turns that end by to, marked
// reset when it stands in for another query.
func pageBefore(r io.ReaderAt, to int64, reset bool) (Page, error) {
	var newestFirst []entry
	joined, text, pending := 0, 0, 0
	first := -1   // newestFirst's index of the oldest joined turn's first record
	more := false // a turn older than the page exists
	reached, err := scanBack(r, 0, to, scanBudget, func(off int64, data []byte) bool {
		e := decode(off, data)
		newestFirst = append(newestFirst, e)
		pending += e.size
		// A /compact command record may repeat the turn before it (build
		// decides), so it starts no counted turn here.
		if !e.opens || e.compact {
			return true
		}
		if joined > 0 && (joined == maxTurns || text+pending > maxText) {
			more = true
			return false
		}
		joined, text, pending, first = joined+1, text+pending, 0, len(newestFirst)-1
		return true
	})
	if err != nil {
		return Page{}, err
	}
	window := newestFirst
	var earlier int64
	switch {
	case more || (!reached && first >= 0):
		window = newestFirst[:first+1]
		earlier = newestFirst[first].off
	case !reached && len(newestFirst) > 0:
		// The budget ran out inside one turn: what was read is its newest
		// part, and the page before ends where it begins.
		earlier = newestFirst[len(newestFirst)-1].off
	}
	entries := reverse(window)
	var start int64
	if len(entries) > 0 {
		start = entries[0].off
	}
	turns, skipped := build(entries, start)
	return Page{Turns: turns, Earlier: earlier, Reset: reset, Skipped: skipped}, nil
}

// since reads every turn from the one at after to end. It reports false
// when after lies out of a read's reach.
func since(r io.ReaderAt, end, after int64) (Page, bool, error) {
	var newestFirst []entry
	reached, err := scanBack(r, after, end, scanBudget, func(off int64, data []byte) bool {
		newestFirst = append(newestFirst, decode(off, data))
		return true
	})
	if err != nil || !reached {
		return Page{}, false, err
	}
	turns, skipped := build(reverse(newestFirst), after)
	return Page{Turns: turns, Skipped: skipped}, true, nil
}

// reverse is es in the opposite order.
func reverse(es []entry) []entry {
	out := make([]entry, len(es))
	for i, e := range es {
		out[len(es)-1-i] = e
	}
	return out
}
```

Create `session/transcript/scan.go`:

```go
package transcript

import (
	"bytes"
	"io"
)

// chunk is how much of the file a read takes at a time.
const chunk = 64 << 10

// lastLineEnd is the offset just past the last newline among r's first
// size bytes, looked for within scanBudget of the end; 0 when there is none.
func lastLineEnd(r io.ReaderAt, size int64) (int64, error) {
	buf := make([]byte, chunk)
	for pos := size; pos > 0 && size-pos < scanBudget; {
		n := min(int64(chunk), pos)
		b := buf[:n]
		if _, err := r.ReadAt(b, pos-n); err != nil && err != io.EOF {
			return 0, err
		}
		if i := bytes.LastIndexByte(b, '\n'); i >= 0 {
			return pos - n + int64(i) + 1, nil
		}
		pos -= n
	}
	return 0, nil
}

// scanBack calls yield with each line of r's [from, to), newest first: its
// offset, and its bytes without the newline (nil for a line longer than
// lineLimit). from is the start of a line, and to sits just past a newline.
// It stops when yield returns false, at from, or once budget bytes have
// been read, and reports whether every line of the range was yielded.
func scanBack(r io.ReaderAt, from, to, budget int64, yield func(off int64, data []byte) bool) (bool, error) {
	if to <= from {
		return true, nil
	}
	pos := to - 1      // the newline at to-1 ends the newest line
	var parts [][]byte // the line in progress, from the chunks read before this one, latest first
	size := int64(0)   // the length of parts, kept or not
	read := int64(0)
	buf := make([]byte, chunk)
	for pos > from {
		n := min(int64(chunk), pos-from, budget-read)
		if n <= 0 {
			return false, nil
		}
		b := buf[:n]
		if _, err := r.ReadAt(b, pos-n); err != nil && err != io.EOF {
			return false, err
		}
		read += n
		base := pos - n
		end := len(b)
		for i := len(b) - 1; i >= 0; i-- {
			if b[i] != '\n' {
				continue
			}
			if !yield(base+int64(i)+1, line(b[i+1:end], parts, size)) {
				return false, nil
			}
			parts, size, end = nil, 0, i
		}
		size += int64(end)
		if end > 0 && size <= lineLimit {
			parts = append(parts, append([]byte(nil), b[:end]...))
		}
		pos = base
	}
	yield(from, line(nil, parts, size))
	return true, nil
}

// line joins a line's first bytes (head, from the chunk being read) and the
// rest of it (parts, latest first, size bytes in all), or returns nil when
// the whole is longer than lineLimit.
func line(head []byte, parts [][]byte, size int64) []byte {
	if int64(len(head))+size > lineLimit {
		return nil
	}
	out := make([]byte, 0, len(head)+int(size))
	out = append(out, head...)
	for i := len(parts) - 1; i >= 0; i-- {
		out = append(out, parts[i]...)
	}
	return out
}
```

Create `session/transcript/records.go`:

```go
package transcript

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aidan-bailey/loom/session/hooks"
)

// entry is what one line adds to the conversation.
type entry struct {
	off  int64
	at   time.Time
	bad  bool // not JSON, or longer than lineLimit
	size int  // its text bytes, for a page's text bound
	// opens is set when the line starts a turn, turn. compact marks a
	// /compact command record, which may repeat the plain /compact Claude
	// wrote before a boundary.
	opens   bool
	compact bool
	turn    Turn
	// parts are what it adds to the turn it falls in, in order.
	parts []part
}

// part is one thing a line adds to a turn: an item (its Kind), a step
// (partStep) or a step's result (partResult, its step's ID and State).
type part struct {
	kind  string
	text  string
	cut   bool
	error bool
	step  Step
}

const (
	partStep   = "step"
	partResult = "result"
)

// The interruption markers Claude writes as the user's text.
const (
	interrupted        = "[Request interrupted by user]"
	interruptedForTool = "[Request interrupted by user for tool use]"
)

// record is the part of a line the reader looks at. Origin and Message
// stay raw: a line of another type may hold another shape there.
type record struct {
	Type                      string          `json:"type"`
	Subtype                   string          `json:"subtype"`
	Timestamp                 string          `json:"timestamp"`
	Cwd                       string          `json:"cwd"`
	IsSidechain               bool            `json:"isSidechain"`
	IsMeta                    bool            `json:"isMeta"`
	IsCompactSummary          bool            `json:"isCompactSummary"`
	IsVisibleInTranscriptOnly bool            `json:"isVisibleInTranscriptOnly"`
	IsAPIErrorMessage         bool            `json:"isApiErrorMessage"`
	Origin                    json.RawMessage `json:"origin"`
	Message                   json.RawMessage `json:"message"`
}

// origin is a user record's origin, where Claude writes one.
type origin struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Body   string `json:"body"`
	AsUser bool   `json:"asUser"`
}

// block is one content block of a message.
type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
}

// decode reads the line at off.
func decode(off int64, data []byte) entry {
	e := entry{off: off}
	if data == nil {
		e.bad = true
		return e
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return e
	}
	var r record
	if err := json.Unmarshal(data, &r); err != nil {
		// Valid JSON of another shape is no unreadable record.
		e.bad = !json.Valid(data)
		return e
	}
	e.at, _ = time.Parse(time.RFC3339Nano, r.Timestamp)
	if r.IsSidechain {
		return e
	}
	switch r.Type {
	case "system":
		if r.Subtype == "compact_boundary" {
			e.parts = []part{{kind: KindCompacted}}
		}
	case "assistant":
		e.assistant(r)
	case "user":
		e.user(r)
	}
	return e
}

// assistant reads Claude's blocks: text items and steps, or an API error's
// note.
func (e *entry) assistant(r record) {
	text, blocks := content(r.Message)
	if text != "" {
		blocks = []block{{Type: "text", Text: text}}
	}
	if r.IsAPIErrorMessage {
		var all []string
		for _, b := range blocks {
			if b.Type == "text" {
				all = append(all, b.Text)
			}
		}
		note := hooks.CapSummary(strings.Join(all, "\n"))
		if note == "" {
			note = "API error"
		}
		e.parts = []part{{kind: KindNote, text: note, error: true}}
		e.size = len(note)
		return
	}
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) == "" {
				continue
			}
			t, cut := capBytes(b.Text, maxItem)
			e.parts = append(e.parts, part{kind: KindText, text: t, cut: cut})
			e.size += len(t)
		case "tool_use":
			e.parts = append(e.parts, part{kind: partStep, step: Step{
				ID: b.ID, Tool: b.Name, Summary: summarize(b.Name, b.Input, r.Cwd), At: e.at, State: StatePending,
			}})
		}
	}
}

// user reads a user record: tool results, an interruption, or a prompt
// that starts a turn, labelled by where it came from.
func (e *entry) user(r record) {
	if r.IsCompactSummary || r.IsVisibleInTranscriptOnly {
		return
	}
	text, blocks := content(r.Message)
	var results []part
	var texts []string
	for _, b := range blocks {
		switch b.Type {
		case "tool_result":
			results = append(results, part{kind: partResult, step: Step{ID: b.ToolUseID, State: resultState(b)}})
		case "text":
			texts = append(texts, b.Text)
		case "image":
			texts = append(texts, "[image]")
		}
	}
	if len(results) > 0 {
		e.parts = results
		return
	}
	if len(texts) > 0 {
		text = strings.Join(texts, "\n")
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == interrupted || trimmed == interruptedForTool {
		e.parts = []part{{kind: KindNote, text: "interrupted"}}
		return
	}
	var o origin
	_ = json.Unmarshal(r.Origin, &o)
	switch {
	case o.Kind == "peer":
		body := o.Body
		if body == "" {
			body = text
		}
		e.open(FromPeer, o.Name, body)
	case o.Kind == "plugin" && o.AsUser:
		e.open(FromYou, "", text)
		e.turn.Command = isCommandLine(trimmed)
	case o.Kind == "plugin":
		e.open(FromPlugin, o.Name, text)
	case o.Kind == "task-notification" || strings.HasPrefix(trimmed, "<task-notification>"):
		e.open(FromTask, "", taskText(trimmed))
	case r.IsMeta:
	case strings.HasPrefix(trimmed, "<command-name>") || strings.HasPrefix(trimmed, "<command-message>"):
		name := tagText(trimmed, "command-name")
		if name == "" {
			return
		}
		if !strings.HasPrefix(name, "/") {
			name = "/" + name
		}
		cmd := name
		if args := tagText(trimmed, "command-args"); args != "" {
			cmd += " " + args
		}
		e.open(FromYou, "", cmd)
		e.turn.Command, e.compact = true, name == "/compact"
	case strings.HasPrefix(trimmed, "<bash-input>"):
		e.open(FromYou, "", "! "+tagText(trimmed, "bash-input"))
		e.turn.Command = true
	case hasAnyPrefix(trimmed, "<local-command-stdout>", "<local-command-stderr>", "<local-command-caveat>", "<bash-stdout>", "<bash-stderr>"):
	case strings.HasPrefix(trimmed, "Another Claude session sent a message:"):
		// Older Claude wrote a peer's message as plain text.
		e.open(FromPeer, "", text)
	case trimmed != "":
		e.open(FromYou, "", text)
		e.turn.Command = isCommandLine(trimmed)
	}
}

// open makes e start a turn from from (and name) with text as its prompt.
func (e *entry) open(from, name, text string) {
	t, cut := capBytes(strings.TrimSpace(text), maxPrompt)
	e.opens = true
	e.turn = Turn{ID: e.off, At: e.at, From: from, Name: name, Text: t, Cut: cut}
	e.size += len(t)
}

// content reads a message's content: a string, or its blocks.
func content(message json.RawMessage) (string, []block) {
	var m struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(message, &m) != nil || len(m.Content) == 0 {
		return "", nil
	}
	var s string
	if json.Unmarshal(m.Content, &s) == nil {
		return s, nil
	}
	var blocks []block
	if json.Unmarshal(m.Content, &blocks) != nil {
		return "", nil
	}
	return "", blocks
}

// resultState is the state a tool result leaves its step in. Only a short
// result can be the interruption marker, so a long one is never decoded.
func resultState(b block) string {
	if len(b.Content) < 128 {
		var s string
		if json.Unmarshal(b.Content, &s) == nil && strings.TrimSpace(s) == interruptedForTool {
			return StateInterrupted
		}
		var blocks []block
		if json.Unmarshal(b.Content, &blocks) == nil && len(blocks) == 1 && strings.TrimSpace(blocks[0].Text) == interruptedForTool {
			return StateInterrupted
		}
	}
	if b.IsError {
		return StateFailed
	}
	return StateDone
}

// taskText is a task notification's line: its status and its summary.
func taskText(s string) string {
	status, summary := tagText(s, "status"), tagText(s, "summary")
	switch {
	case status != "" && summary != "":
		return status + ": " + summary
	case summary != "":
		return summary
	case status != "":
		return status
	}
	return "background task"
}

// tagText is the trimmed text between s's first <tag> and the </tag> after
// it, "" when either is missing.
func tagText(s, tag string) string {
	open, end := "<"+tag+">", "</"+tag+">"
	i := strings.Index(s, open)
	if i < 0 {
		return ""
	}
	rest := s[i+len(open):]
	j := strings.Index(rest, end)
	if j < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:j])
}

func hasAnyPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// commandLine matches a prompt that is one slash command: "/name" and its
// arguments, the name holding no further slash ("/tmp/x is broken" is no
// command).
var commandLine = regexp.MustCompile(`^/[A-Za-z][A-Za-z0-9:_.-]*(\s|$)`)

func isCommandLine(s string) bool {
	return !strings.Contains(s, "\n") && commandLine.MatchString(s)
}

// capBytes is s's first n bytes, cut back to a rune boundary, and whether
// it cut.
func capBytes(s string, n int) (string, bool) {
	if len(s) <= n {
		return s, false
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n], true
}

// summaryKeys names, per tool, the input field its step shows. It is wider
// than the permission prompt's (session/hooks): a step is the user's own
// history to read, not a request to approve.
var summaryKeys = map[string]string{
	"Bash":         "command",
	"Read":         "file_path",
	"Edit":         "file_path",
	"Write":        "file_path",
	"MultiEdit":    "file_path",
	"NotebookEdit": "notebook_path",
	"Grep":         "pattern",
	"Glob":         "pattern",
	"WebFetch":     "url",
	"WebSearch":    "query",
	"ToolSearch":   "query",
	"Agent":        "description",
	"Task":         "description",
	"Skill":        "skill",
}

// summarize is a step's one line: its tool's field from summaryKeys, a path
// under cwd made relative to it, cut as a permission prompt's is
// (hooks.CapSummary). A tool not listed, or an input without the field as a
// string, has none.
func summarize(tool string, input json.RawMessage, cwd string) string {
	key, ok := summaryKeys[tool]
	if !ok {
		return ""
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(input, &fields) != nil {
		return ""
	}
	var s string
	if json.Unmarshal(fields[key], &s) != nil {
		return ""
	}
	if strings.HasSuffix(key, "_path") {
		s = under(cwd, s)
	}
	return hooks.CapSummary(s)
}

// under is p relative to dir when p lies inside it, else p.
func under(dir, p string) string {
	if dir == "" || !filepath.IsAbs(p) {
		return p
	}
	rel, err := filepath.Rel(dir, p)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return p
	}
	return rel
}
```

Create `session/transcript/build.go`:

```go
package transcript

// stepAt is where a step sits in the turns being built.
type stepAt struct{ turn, item, step int }

// builder folds entries, in file order, into turns.
type builder struct {
	start   int64 // the first line read: a partial turn's ID
	turns   []Turn
	steps   map[string]stepAt
	skipped int
}

// build folds entries (in file order, the first at start) into turns, and
// counts the lines it could not read.
func build(entries []entry, start int64) ([]Turn, int) {
	b := builder{start: start, steps: map[string]stepAt{}}
	for _, e := range entries {
		b.add(e)
	}
	b.count()
	return b.turns, b.skipped
}

func (b *builder) add(e entry) {
	if e.bad {
		b.skipped++
		return
	}
	if e.opens {
		if e.compact && b.repeatsCompact() {
			return
		}
		b.turns = append(b.turns, e.turn)
		return
	}
	for _, p := range e.parts {
		b.addPart(e, p)
	}
}

// repeatsCompact reports whether a /compact command record repeats the
// current turn: Claude writes a manual /compact as a plain /compact before
// its compact boundary, and again as a command record after the summary.
func (b *builder) repeatsCompact() bool {
	if len(b.turns) == 0 {
		return false
	}
	t := b.turns[len(b.turns)-1]
	if t.Partial || t.Text != "/compact" {
		return false
	}
	for _, it := range t.Items {
		if it.Kind == KindCompacted {
			return true
		}
	}
	return false
}

func (b *builder) addPart(e entry, p part) {
	if p.kind == partResult {
		if at, ok := b.steps[p.step.ID]; ok {
			b.turns[at.turn].Items[at.item].Steps[at.step].State = p.step.State
		}
		return
	}
	if len(b.turns) == 0 {
		b.turns = append(b.turns, Turn{ID: b.start, At: e.at, Partial: true})
	}
	ti := len(b.turns) - 1
	t := &b.turns[ti]
	if p.kind != partStep {
		t.Items = append(t.Items, Item{Kind: p.kind, At: e.at, Text: p.text, Cut: p.cut, Error: p.error})
		return
	}
	if n := len(t.Items); n == 0 || t.Items[n-1].Kind != KindSteps {
		t.Items = append(t.Items, Item{Kind: KindSteps, At: e.at})
	}
	ii := len(t.Items) - 1
	t.Items[ii].Steps = append(t.Items[ii].Steps, p.step)
	if p.step.ID != "" {
		b.steps[p.step.ID] = stepAt{ti, ii, len(t.Items[ii].Steps) - 1}
	}
}

// count totals each steps item, its failures included, and keeps its
// newest maxSteps steps.
func (b *builder) count() {
	for ti := range b.turns {
		for ii := range b.turns[ti].Items {
			it := &b.turns[ti].Items[ii]
			if it.Kind != KindSteps {
				continue
			}
			it.Total = len(it.Steps)
			for _, s := range it.Steps {
				if s.State == StateFailed {
					it.Failed++
				}
			}
			if len(it.Steps) > maxSteps {
				it.Steps = append([]Step(nil), it.Steps[len(it.Steps)-maxSteps:]...)
			}
		}
	}
}
```

- [ ] **Step 5: Run them and see them pass**

Run: `CGO_ENABLED=0 go test ./session/transcript/ ./session/hooks/`
Expected: PASS. A failing case is a bug in the reader, never in the census shapes: fix the reader.

- [ ] **Step 6: Write the opt-in real-Claude test**

Create `session/transcript/realclaude_test.go`:

```go
package transcript

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRealClaude_TranscriptReader runs one print-mode prompt that makes one
// Bash call and reads its transcript back: one turn of the prompt, one done
// Bash step whose summary is the command, and Claude's text. Claude's
// transcript format is its own and changes without notice, so this is the
// reader's guard. It spends a few cents of haiku and leaves the session in
// the developer's Claude config dir (projects/), so it runs only with
// LOOM_TEST_REAL_CLAUDE=1.
func TestRealClaude_TranscriptReader(t *testing.T) {
	if os.Getenv("LOOM_TEST_REAL_CLAUDE") != "1" {
		t.Skip("set LOOM_TEST_REAL_CLAUDE=1 to run against real Claude (costs money)")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not installed")
	}
	run := exec.Command("claude", "-p", "--model", "haiku", "--output-format", "json", "--allowedTools", "Bash(echo:*)",
		"Run the shell command `echo loom-transcript-probe` with the Bash tool, then reply with exactly the word DONE.")
	run.Dir = t.TempDir()
	out, err := run.Output()
	require.NoError(t, err, "claude -p: %s", out)
	var res struct {
		SessionID string `json:"session_id"`
	}
	require.NoError(t, json.Unmarshal(out, &res), "claude -p's JSON: %s", out)
	require.NotEmpty(t, res.SessionID)

	config := os.Getenv("CLAUDE_CONFIG_DIR")
	if config == "" {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		config = filepath.Join(home, ".claude")
	}
	paths, err := filepath.Glob(filepath.Join(config, "projects", "*", res.SessionID+".jsonl"))
	require.NoError(t, err)
	require.Len(t, paths, 1, "the transcript of session %s under %s", res.SessionID, config)

	p, err := Read(paths[0], res.SessionID, Query{})
	require.NoError(t, err)
	assert.Zero(t, p.Skipped)
	require.Len(t, p.Turns, 1)
	turn := p.Turns[0]
	assert.Equal(t, FromYou, turn.From)
	assert.Contains(t, turn.Text, "loom-transcript-probe")
	var steps []Step
	var said []string
	for _, it := range turn.Items {
		switch it.Kind {
		case KindSteps:
			steps = append(steps, it.Steps...)
		case KindText:
			said = append(said, it.Text)
		}
	}
	require.Len(t, steps, 1)
	assert.Equal(t, "Bash", steps[0].Tool)
	assert.True(t, strings.HasPrefix(steps[0].Summary, "echo"), "the summary is the command: %q", steps[0].Summary)
	assert.Contains(t, steps[0].Summary, "loom-transcript-probe")
	assert.Equal(t, StateDone, steps[0].State)
	assert.Contains(t, strings.Join(said, " "), "DONE")
}
```

Run: `CGO_ENABLED=0 go test ./session/transcript/ -run TestRealClaude -v`
Expected: SKIP (`set LOOM_TEST_REAL_CLAUDE=1 …`). The real run is the owner's to choose; it is not a gate.

- [ ] **Step 7: The docs**

In `session/README.md`, after the `### subagent` section, add:

```markdown
### transcript

Reads one page of a Claude conversation from its transcript, `<config>/projects/<encoded cwd>/<session ID>.jsonl`, for the phone's conversation screen (`core.Model.ReadTurns`). `Read(path, conversation, Query)` refuses a path that is not absolute, not `<conversation>.jsonl` or not a regular file, opens it read-only and reads backward from the last newline (a line after it is one Claude is still writing), in 64 KiB chunks, at most 16 MiB a read, skipping a line over 4 MiB. A turn starts at each prompt: a user record that is no tool result, compact summary or interruption, labelled by its `origin` (a peer's and a background task's, a plugin's framed or `asUser`), a slash command shown as `/name args`, a `<bash-input>` as `! cmd`; meta records start none, except a peer's. Claude's text blocks become text items (64 KiB each), each run of `tool_use` blocks a steps item (a one-line summary per step from the input field its tool names, a path under the record's `cwd` made relative, its state set by the result that names it; the newest 200 kept, all counted), a `compact_boundary` a compacted item. A manual `/compact` is written twice, plain before its boundary and as a command record after the summary; the repeat starts no turn. A page holds at most 10 turns and, past its newest, 256 KiB of text; `Earlier` is the next cursor, `After` reads from a turn to the end, and another conversation or a cursor out of reach answers the newest page marked `Reset`. Lines that are not JSON or too long are counted in `Skipped`. No tmux, UI or app dependency; `TestRealClaude_TranscriptReader` (opt-in) pins the format against real Claude.
```

In `session/CLAUDE.md`:
- In the opening paragraph, change "plus the sub-packages `agent`, `claudetmp`, `files`, `github`, `hooks`, `launch` and `subagent`" to "plus the sub-packages `agent`, `claudetmp`, `files`, `github`, `hooks`, `launch`, `subagent` and `transcript`".
- In the rule "**Keep `session/github`, `session/hooks`, `session/subagent` and `session/claudetmp` free of tmux, UI and app imports.**", name `session/transcript` too: "**Keep `session/github`, `session/hooks`, `session/subagent`, `session/claudetmp` and `session/transcript` free of tmux, UI and app imports.**"

Run: `CGO_ENABLED=0 go run ./tools/claudemd -v 2>&1 | grep session/CLAUDE`
Expected: within 100 lines and 20,000 bytes.

- [ ] **Step 8: Run the packages, the lint and the race detector**

Run: `CGO_ENABLED=0 go test ./session/... ./tools/claudemd/ && CC=clang CGO_ENABLED=1 go test -race ./session/transcript/ && gofmt -l session/transcript session/hooks && CGO_ENABLED=0 go vet ./session/transcript/ ./session/hooks/`
Expected: PASS, and `gofmt -l` prints nothing.

- [ ] **Step 9: Commit**

```bash
git add -- session/hooks/event.go session/transcript session/README.md session/CLAUDE.md
git commit -m "feat(transcript,hooks): read a page of a Claude conversation from its transcript

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_019icsHbrFq5LeZ4b4jFkSBe" -- session/hooks/event.go session/transcript session/README.md session/CLAUDE.md
```

---

### Task B: The patch

**Files:**
- Create: `session/git/patch.go`, `session/git/patch_test.go`
- Create: `session/patch.go`, `session/patch_test.go`
- Modify: `session/README.md`, `session/CLAUDE.md`, `session/git/CLAUDE.md`

- [ ] **Step 1: Write the failing patch tests**

Create `session/git/patch_test.go`:

```go
package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// patchRepo makes a repository whose one commit holds files, and returns it
// and the commit's SHA.
func patchRepo(t *testing.T, files map[string]string) (string, string) {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "t@example.com")
	runGit(t, repo, "config", "user.name", "T")
	for name, body := range files {
		require.NoError(t, os.WriteFile(filepath.Join(repo, name), []byte(body), 0o644))
	}
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-qm", "init")
	out, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	require.NoError(t, err)
	return repo, strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, repo, name, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(repo, name), []byte(body), 0o644))
}

// splitWorktree is the patch of repo's work tree against base, split.
func splitWorktree(t *testing.T, repo, base string) []FilePatch {
	t.Helper()
	text, err := NewGitWorktreeFromStorage(repo, repo, "t", "main", base, false, "").Patch()
	require.NoError(t, err)
	return SplitPatch(text)
}

func fileAt(t *testing.T, files []FilePatch, path string) FilePatch {
	t.Helper()
	var names []string
	for _, f := range files {
		if f.Path == path {
			return f
		}
		names = append(names, f.Path)
	}
	t.Fatalf("no file %q among %q", path, names)
	return FilePatch{}
}

func TestPatch_SplitsEachKindOfChange(t *testing.T) {
	repo, base := patchRepo(t, map[string]string{
		"src.txt":     "one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\n",
		"gone.txt":    "old\n",
		"mode.sh":     "echo\n",
		"before.txt":  "rename me\nline2\nline3\nline4\n",
		"nonl.txt":    "nonl",
		"bin.dat":     "a\x00b",
		"my file.txt": "keep\n",
		`q"uote.txt`:  "x\n",
		"café.txt":    "y\n",
		"blank.txt":   "one\n\nthree\n",
		"plus.txt":    "a\n",
		"dash.txt":    "-- y\nkeep\n",
	})
	runGit(t, repo, "config", "diff.renames", "copies")
	runGit(t, repo, "config", "diff.suppressBlankEmpty", "true")
	changed := "one\ntwo\nTHREE\nfour\nfive\nsix\nseven\neight\n"
	writeFile(t, repo, "src.txt", changed)
	writeFile(t, repo, "copy.txt", changed)
	require.NoError(t, os.Remove(filepath.Join(repo, "gone.txt")))
	require.NoError(t, os.Chmod(filepath.Join(repo, "mode.sh"), 0o755))
	runGit(t, repo, "mv", "before.txt", "after.txt")
	writeFile(t, repo, "after.txt", "rename me\nline2\nline3\nline4\nline5\n")
	writeFile(t, repo, "nonl.txt", "nonl2")
	writeFile(t, repo, "bin.dat", "a\x00c")
	writeFile(t, repo, "new.txt", "fresh\n")
	writeFile(t, repo, "empty.txt", "")
	writeFile(t, repo, "my file.txt", "keep\nmore\n")
	writeFile(t, repo, `q"uote.txt`, "x2\n")
	writeFile(t, repo, "café.txt", "y2\n")
	writeFile(t, repo, "blank.txt", "one\n\nTHREE\n")
	writeFile(t, repo, "plus.txt", "a\n++ x\n")
	writeFile(t, repo, "dash.txt", "keep\n")

	files := splitWorktree(t, repo, base)
	type shape struct {
		old, status    string
		binary         bool
		added, removed int
		hunks          int
	}
	for path, want := range map[string]shape{
		"src.txt":     {"", StatusModified, false, 1, 1, 1},
		"copy.txt":    {"src.txt", StatusCopied, false, 1, 1, 1},
		"gone.txt":    {"", StatusDeleted, false, 0, 1, 1},
		"mode.sh":     {"", StatusModified, false, 0, 0, 0},
		"after.txt":   {"before.txt", StatusRenamed, false, 1, 0, 1},
		"nonl.txt":    {"", StatusModified, false, 1, 1, 1},
		"bin.dat":     {"", StatusModified, true, 0, 0, 0},
		"new.txt":     {"", StatusAdded, false, 1, 0, 1},
		"empty.txt":   {"", StatusAdded, false, 0, 0, 0},
		"my file.txt": {"", StatusModified, false, 1, 0, 1},
		`q"uote.txt`:  {"", StatusModified, false, 1, 1, 1},
		"café.txt":    {"", StatusModified, false, 1, 1, 1},
		"blank.txt":   {"", StatusModified, false, 1, 1, 1},
		"plus.txt":    {"", StatusModified, false, 1, 0, 1},
		"dash.txt":    {"", StatusModified, false, 0, 1, 1},
	} {
		f := fileAt(t, files, path)
		assert.Equal(t, want, shape{f.OldPath, f.Status, f.Binary, f.Added, f.Removed, len(f.Hunks)}, path)
	}
	assert.Len(t, files, 15)
	assert.Equal(t, []string{" one", "", "-three", "+THREE"}, fileAt(t, files, "blank.txt").Hunks[0].Lines,
		"a blank context line, written empty")
	assert.Contains(t, fileAt(t, files, "plus.txt").Hunks[0].Lines, "+++ x", "an added line reading +++ x stays a line")
	assert.Contains(t, fileAt(t, files, "dash.txt").Hunks[0].Lines, "--- y", "a removed line reading --- y stays a line")
	nonl := fileAt(t, files, "nonl.txt").Hunks[0].Lines
	assert.Equal(t, `\ No newline at end of file`, nonl[len(nonl)-1])
}

func TestPatch_TheUsersDiffConfigChangesNothing(t *testing.T) {
	repo, base := patchRepo(t, map[string]string{"a.txt": "one\n"})
	for _, kv := range [][2]string{
		{"color.diff", "always"}, {"color.ui", "always"}, {"diff.noprefix", "true"},
		{"diff.mnemonicPrefix", "true"}, {"diff.external", "false"},
	} {
		runGit(t, repo, "config", kv[0], kv[1])
	}
	writeFile(t, repo, "a.txt", "one\ntwo\n")

	text, err := NewGitWorktreeFromStorage(repo, repo, "t", "main", base, false, "").Patch()
	require.NoError(t, err)
	assert.NotContains(t, text, "\x1b[", "no colour")
	files := SplitPatch(text)
	require.Len(t, files, 1)
	assert.Equal(t, "a.txt", files[0].Path)
	assert.Equal(t, 1, files[0].Added)
}

func TestPatch_NeedsABaseCommit(t *testing.T) {
	repo, _ := patchRepo(t, map[string]string{"a.txt": "one\n"})
	_, err := NewGitWorktreeFromStorage(repo, repo, "t", "main", "", false, "").Patch()
	assert.ErrorContains(t, err, "base commit SHA not set")
}

func TestSplitPatch_NamesAFileFromItsHeaderWhenNothingElseDoes(t *testing.T) {
	text := strings.Join([]string{
		"diff --git a/my file.bin b/my file.bin",
		"index 1111111..2222222 100644",
		"Binary files a/my file.bin and b/my file.bin differ",
		`diff --git "a/caf\303\251.bin" "b/caf\303\251.bin"`,
		"new file mode 100644",
		"index 0000000..3333333",
		`Binary files /dev/null and "b/caf\303\251.bin" differ`,
		"diff --git a/plain b/plain",
		"old mode 100644",
		"new mode 100755",
	}, "\n") + "\n"
	files := SplitPatch(text)
	require.Len(t, files, 3)
	assert.Equal(t, "my file.bin", files[0].Path)
	assert.True(t, files[0].Binary)
	assert.Equal(t, "café.bin", files[1].Path)
	assert.Equal(t, StatusAdded, files[1].Status)
	assert.True(t, files[1].Binary)
	assert.Equal(t, "plain", files[2].Path)
	assert.Empty(t, files[2].Hunks)
}

func TestSplitPatch_KeepsWithinItsBounds(t *testing.T) {
	var b strings.Builder
	file := func(name string, lines []string) {
		fmt.Fprintf(&b, "diff --git a/%s b/%s\n--- a/%s\n+++ b/%s\n@@ -0,0 +1,%d @@\n", name, name, name, name, len(lines))
		for _, l := range lines {
			b.WriteString("+" + l + "\n")
		}
	}
	file("long.txt", []string{strings.Repeat("x", maxPatchLineBytes-2) + "é" + "y", "short"})
	many := make([]string, maxFileLines+1)
	for i := range many {
		many[i] = "l"
	}
	file("many.txt", many)
	big := make([]string, 1100)
	for i := range big {
		big[i] = strings.Repeat("z", 1000)
	}
	file("big.txt", big)
	file("after.txt", []string{"tiny"})

	files := SplitPatch(b.String())
	require.Len(t, files, 4)
	h := files[0].Hunks[0]
	assert.Equal(t, []int{0}, h.Cut)
	assert.Equal(t, "+"+strings.Repeat("x", maxPatchLineBytes-2), h.Lines[0], "cut back to a rune boundary")
	assert.Equal(t, "+short", h.Lines[1])
	assert.Equal(t, OmittedLines, files[1].Omitted)
	assert.Empty(t, files[1].Hunks)
	assert.Equal(t, maxFileLines+1, files[1].Added, "the counts stay exact")
	assert.Equal(t, OmittedBudget, files[2].Omitted)
	assert.Equal(t, 1100, files[2].Added)
	assert.Equal(t, OmittedBudget, files[3].Omitted, "every file after the budget ran out")
	assert.Equal(t, 1, files[3].Added)
}
```

Create `session/patch_test.go`:

```go
package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/session/git"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReadPatch_LeavesTheCachedDiffAlone: a patch is read for one client,
// so the diff stats the published views carry to every client stay as the
// probe left them.
func TestReadPatch_LeavesTheCachedDiffAlone(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-q", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\n"), 0o644))
	runGit(t, repo, "add", "a.txt")
	runGit(t, repo, "commit", "-qm", "init")
	out, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	require.NoError(t, err)
	base := strings.TrimSpace(string(out))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\ntwo\n"), 0o644))

	inst, err := NewInstance(InstanceOptions{Title: "t", Path: repo, Program: "claude"})
	require.NoError(t, err)
	inst.setStarted(true)
	inst.setGitWorktree(git.NewGitWorktreeFromStorage(repo, repo, "t", "main", base, false, ""))
	cached := &git.DiffStats{Added: 9, Removed: 9}
	inst.setDiffStats(cached)

	p, err := inst.ReadPatch()
	require.NoError(t, err)
	assert.Equal(t, base, p.Base)
	require.Len(t, p.Files, 1)
	assert.Equal(t, "a.txt", p.Files[0].Path)
	assert.Equal(t, 1, p.Files[0].Added)
	assert.Same(t, cached, inst.GetDiffStats(), "the cached stats are left as they were")
}

func TestReadPatch_AWorkspaceTerminalHasNoWorktree(t *testing.T) {
	inst, err := NewInstance(InstanceOptions{Title: "term", Path: t.TempDir(), Program: "claude", IsWorkspaceTerminal: true})
	require.NoError(t, err)
	_, err = inst.ReadPatch()
	assert.EqualError(t, err, "read the diff of term: the session has no worktree")
}
```

- [ ] **Step 2: Run them and see them fail**

Run: `CGO_ENABLED=0 go test ./session/git/ ./session/ -run 'Patch|ReadPatch'`
Expected: FAIL to build (`undefined: SplitPatch`, `inst.ReadPatch undefined`, …).

- [ ] **Step 3: Write the patch**

Create `session/git/patch.go`:

```go
package git

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Patch is a worktree's diff against its base commit, file by file.
type Patch struct {
	// Base is the base commit's SHA.
	Base  string
	Files []FilePatch
}

// FilePatch is one file's part of a patch.
type FilePatch struct {
	// Path is the file's path in the worktree; OldPath is the path it was
	// renamed or copied from, "" otherwise.
	Path, OldPath string
	// Status is StatusAdded, StatusModified, StatusDeleted, StatusRenamed
	// or StatusCopied.
	Status string
	Binary bool
	// Added and Removed count the file's added and removed lines, kept
	// hunks or not.
	Added, Removed int
	Hunks          []Hunk
	// Omitted says why a file's hunks were dropped: OmittedLines or
	// OmittedBudget; "" when they are kept.
	Omitted string
}

// Hunk is one hunk of a file.
type Hunk struct {
	// Header is its @@ line.
	Header string
	// Lines are its lines with their prefixes (' ', '+', '-' or '\'); a
	// blank context line git wrote empty stays "".
	Lines []string
	// Cut lists the indexes of the lines cut at maxPatchLineBytes.
	Cut []int
}

// A file's status in a patch.
const (
	StatusAdded    = "A"
	StatusModified = "M"
	StatusDeleted  = "D"
	StatusRenamed  = "R"
	StatusCopied   = "C"
)

// Why a file's hunks were dropped.
const (
	OmittedLines  = "lines"
	OmittedBudget = "budget"
)

// A patch's bounds: the kept lines total at most maxPatchBytes, a file of
// more than maxFileLines lines keeps only its counts, and a line is cut at
// maxPatchLineBytes.
const (
	maxPatchBytes     = 1 << 20
	maxFileLines      = 5000
	maxPatchLineBytes = 2000
)

// Patch returns the worktree's diff against its base commit, as Diff runs
// it but for parsing: stdout alone, so a warning on stderr never lands in
// it, and with colour, external diff drivers and the a/ and b/ prefixes
// pinned, so the user's diff config can't change what SplitPatch reads.
func (g *GitWorktree) Patch() (string, error) {
	base := g.GetBaseCommitSHA()
	if base == "" {
		return "", fmt.Errorf("base commit SHA not set")
	}
	if err := g.ensureUntrackedStaged(time.Now); err != nil {
		return "", err
	}
	return g.runGitStdout(g.worktreePath, "--no-pager", "diff", "--no-color", "--no-ext-diff", "--src-prefix=a/", "--dst-prefix=b/", base)
}

// SplitPatch splits a patch Patch returned into its files, in git's order.
// Each file's counts are exact, and its hunks are kept within the patch's
// bounds: once the kept lines reach maxPatchBytes, every later file keeps
// only its counts.
func SplitPatch(text string) []FilePatch {
	lines := strings.Split(text, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	files := []FilePatch{}
	used, spent := 0, false
	for i := 0; i < len(lines); {
		if !strings.HasPrefix(lines[i], "diff --git ") {
			i++
			continue
		}
		f, size, next := splitFile(lines, i)
		switch {
		case len(f.Hunks) == 0:
		case lineCount(f.Hunks) > maxFileLines:
			f.Hunks, f.Omitted = nil, OmittedLines
		case spent || used+size > maxPatchBytes:
			f.Hunks, f.Omitted, spent = nil, OmittedBudget, true
		default:
			used += size
		}
		files = append(files, f)
		i = next
	}
	return files
}

// splitFile reads the file whose "diff --git" line is lines[i]: its header
// lines, then its hunks. It returns the file, the bytes its kept lines
// take, and the index of the line after it.
func splitFile(lines []string, i int) (FilePatch, int, int) {
	f := FilePatch{Status: StatusModified}
	header := lines[i]
	var oldName, newName string
	named := false
	for i++; i < len(lines) && !strings.HasPrefix(lines[i], "@@ ") && !strings.HasPrefix(lines[i], "diff --git "); i++ {
		l := lines[i]
		switch {
		case strings.HasPrefix(l, "new file mode "):
			f.Status = StatusAdded
		case strings.HasPrefix(l, "deleted file mode "):
			f.Status = StatusDeleted
		case strings.HasPrefix(l, "rename from "):
			f.Status, oldName, named = StatusRenamed, unquote(strings.TrimPrefix(l, "rename from ")), true
		case strings.HasPrefix(l, "rename to "):
			f.Status, newName, named = StatusRenamed, unquote(strings.TrimPrefix(l, "rename to ")), true
		case strings.HasPrefix(l, "copy from "):
			f.Status, oldName, named = StatusCopied, unquote(strings.TrimPrefix(l, "copy from ")), true
		case strings.HasPrefix(l, "copy to "):
			f.Status, newName, named = StatusCopied, unquote(strings.TrimPrefix(l, "copy to ")), true
		case strings.HasPrefix(l, "--- "):
			if p := side(l[4:], "a/"); p != "" {
				oldName, named = p, true
			}
		case strings.HasPrefix(l, "+++ "):
			if p := side(l[4:], "b/"); p != "" {
				newName, named = p, true
			}
		case strings.HasPrefix(l, "Binary files ") && strings.HasSuffix(l, " differ"):
			f.Binary = true
		}
	}
	if !named {
		oldName, newName = headerNames(header)
	}
	switch {
	case f.Status == StatusDeleted:
		f.Path = oldName
	case f.Status == StatusRenamed || f.Status == StatusCopied:
		f.Path, f.OldPath = newName, oldName
	case newName != "":
		f.Path = newName
	default:
		f.Path = oldName
	}
	size := 0
	for i < len(lines) && strings.HasPrefix(lines[i], "@@ ") {
		h, next, n, ok := splitHunk(lines, i, &f)
		if !ok {
			// A header git did not write as one: the rest of the file is
			// skipped up to the next.
			return f, size, i + 1
		}
		f.Hunks = append(f.Hunks, h)
		size += n
		i = next
	}
	return f, size, i
}

// hunkRange reads a hunk header's line counts: @@ -a[,b] +c[,d] @@.
var hunkRange = regexp.MustCompile(`^@@ -\d+(?:,(\d+))? \+\d+(?:,(\d+))? @@`)

// splitHunk reads the hunk whose header is lines[i], adding its changed
// lines to f's counts. Its lines are counted by the header's ranges, so an
// added line that reads "+++ x" stays a line. It returns the hunk, the
// index after it, the bytes its kept lines take, and false for a header it
// cannot read.
func splitHunk(lines []string, i int, f *FilePatch) (Hunk, int, int, bool) {
	m := hunkRange.FindStringSubmatch(lines[i])
	if m == nil {
		return Hunk{}, i, 0, false
	}
	oldLeft, newLeft := rangeCount(m[1]), rangeCount(m[2])
	h := Hunk{Header: lines[i]}
	size := len(lines[i])
	for i++; i < len(lines); i++ {
		l := lines[i]
		switch {
		case strings.HasPrefix(l, `\`):
		case oldLeft <= 0 && newLeft <= 0:
			return h, i, size, true
		case l == "" || l[0] == ' ':
			oldLeft, newLeft = oldLeft-1, newLeft-1
		case l[0] == '-':
			oldLeft--
			f.Removed++
		case l[0] == '+':
			newLeft--
			f.Added++
		default:
			return h, i, size, true
		}
		kept, cut := capLine(l)
		if cut {
			h.Cut = append(h.Cut, len(h.Lines))
		}
		h.Lines = append(h.Lines, kept)
		size += len(kept)
	}
	return h, i, size, true
}

// rangeCount is a hunk range's count, which git leaves out when it is 1.
func rangeCount(s string) int {
	if s == "" {
		return 1
	}
	n, _ := strconv.Atoi(s)
	return n
}

func lineCount(hunks []Hunk) int {
	n := 0
	for _, h := range hunks {
		n += len(h.Lines)
	}
	return n
}

// capLine cuts a line at maxPatchLineBytes, back to a rune boundary.
func capLine(l string) (string, bool) {
	if len(l) <= maxPatchLineBytes {
		return l, false
	}
	n := maxPatchLineBytes
	for n > 0 && !utf8.RuneStart(l[n]) {
		n--
	}
	return l[:n], true
}

// side is the path a ---/+++ line names, without its a/ or b/ prefix, or
// "" for /dev/null. Git ends the line with a tab when the path holds a
// space.
func side(p, prefix string) string {
	p = strings.TrimSuffix(p, "\t")
	if p == "/dev/null" {
		return ""
	}
	return strings.TrimPrefix(unquote(p), prefix)
}

// unquote undoes git's C-style quoting of a path it quoted.
func unquote(p string) string {
	if !strings.HasPrefix(p, `"`) {
		return p
	}
	if s, err := strconv.Unquote(p); err == nil {
		return s
	}
	return p
}

// headerNames reads the two paths of a "diff --git a/<p> b/<q>" line, for
// a file with no ---/+++, rename or copy line (binary, mode-only, empty).
// Its two paths are then the same, which is how an unquoted line whose path
// holds a space splits.
func headerNames(header string) (string, string) {
	rest := strings.TrimPrefix(header, "diff --git ")
	if strings.HasPrefix(rest, `"`) {
		a, after, ok := cutQuoted(rest)
		if !ok {
			return "", ""
		}
		b := strings.TrimPrefix(after, " ")
		if strings.HasPrefix(b, `"`) {
			b, _, _ = cutQuoted(b)
		}
		return strings.TrimPrefix(a, "a/"), strings.TrimPrefix(b, "b/")
	}
	if n := len(rest); n >= 5 && n%2 == 1 {
		half := n / 2
		a, b := rest[:half], rest[half+1:]
		if rest[half] == ' ' && strings.HasPrefix(a, "a/") && strings.HasPrefix(b, "b/") && a[2:] == b[2:] {
			return a[2:], b[2:]
		}
	}
	if i := strings.LastIndex(rest, " b/"); i >= 0 {
		return strings.TrimPrefix(rest[:i], "a/"), rest[i+3:]
	}
	return rest, rest
}

// cutQuoted splits s, which starts with a quoted path, after that path.
func cutQuoted(s string) (string, string, bool) {
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			name, err := strconv.Unquote(s[:i+1])
			return name, s[i+1:], err == nil
		}
	}
	return "", "", false
}
```

Create `session/patch.go`:

```go
package session

import (
	"fmt"

	"github.com/aidan-bailey/loom/session/git"
)

// ReadPatch returns the worktree's diff against its base commit, file by
// file (git.SplitPatch), with Base set. It leaves the cached diff stats
// alone: the published view carries those to every client, and a patch is
// read for one. A session with no worktree is an error.
func (i *Instance) ReadPatch() (git.Patch, error) {
	gw := i.getGitWorktree()
	if gw == nil || i.IsWorkspaceTerminal {
		return git.Patch{}, fmt.Errorf("read the diff of %s: the session has no worktree", i.Title)
	}
	base := gw.GetBaseCommitSHA()
	text, err := gw.Patch()
	if err != nil {
		return git.Patch{}, fmt.Errorf("read the diff of %s: %w", i.Title, err)
	}
	return git.Patch{Base: base, Files: git.SplitPatch(text)}, nil
}
```

- [ ] **Step 4: Run them and see them pass**

Run: `CGO_ENABLED=0 go test ./session/git/ ./session/ -run 'Patch|ReadPatch' -v 2>&1 | tail -20`
Expected: PASS for every `TestPatch_…`, `TestSplitPatch_…` and `TestReadPatch_…`.

- [ ] **Step 5: The docs**

In `session/README.md`'s `### git` section, after the Stashes bullet, add:

```markdown
- Patches (`git/patch.go`): `GitWorktree.Patch` runs the same `git diff <base>` as `Diff`, after `ensureUntrackedStaged`, but reads stdout alone and pins `--no-color --no-ext-diff --src-prefix=a/ --dst-prefix=b/`, so neither a warning nor the user's diff config (`color.diff`, `diff.noprefix`, `diff.external`) reaches the parser. `SplitPatch` splits it into `FilePatch`es in git's order: status (A, M, D, R, C), binary, exact added and removed counts, and hunks whose lines are counted by their headers' ranges (so an added `+++ x` stays a line; a blank context line written empty under `diff.suppressBlankEmpty` is kept as ""). Paths come from the `---`/`+++`, rename and copy lines, C-unquoted, and from the `diff --git` line only when a file has none. A line is cut at 2,000 bytes (`Hunk.Cut`), a file of more than 5,000 lines keeps only its counts (`OmittedLines`), and once the kept lines reach 1 MiB every later file does (`OmittedBudget`). `Instance.ReadPatch` (core's `ReadDiff`) returns it with `Base` and never touches the cached `DiffStats`.
```

In `session/git/CLAUDE.md`, add a section after `### Stashes`:

```markdown
### Patches

- **Parse only `Patch`'s output (`SplitPatch`), never `Diff`'s.** `Diff` folds stderr into its text and runs under the user's diff config, so a warning, `color.diff=always`, `diff.noprefix` or `diff.external` changes what a parser reads, silently; `Patch` reads stdout alone with colour, external drivers and the `a/` `b/` prefixes pinned. **Enforced** by `TestPatch_TheUsersDiffConfigChangesNothing`.
```

In `session/CLAUDE.md`, under `### Instance internals`, add:

```markdown
- **Read a patch for one client (`Instance.ReadPatch`) without touching the cached diff stats.** `InstanceView.Diff` carries what `setDiffStats` holds to every client on each views event, so caching a requester's full patch would send it to every TUI and stop the tick's own refresh from looking needed. **Enforced** by `TestReadPatch_LeavesTheCachedDiffAlone`.
```

- [ ] **Step 6: Run the packages, the lint and the race detector**

Run: `CGO_ENABLED=0 go test ./session/... ./tools/claudemd/ && CC=clang CGO_ENABLED=1 go test -race ./session/git/ -run 'Patch' && gofmt -l session/git session/patch.go session/patch_test.go && CGO_ENABLED=0 go vet ./session/ ./session/git/`
Expected: PASS, and `gofmt -l` prints nothing.

- [ ] **Step 7: Commit**

```bash
git add -- session/git/patch.go session/git/patch_test.go session/patch.go session/patch_test.go session/README.md session/CLAUDE.md session/git/CLAUDE.md
git commit -m "feat(git,session): a worktree's patch, file by file, for one client

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_019icsHbrFq5LeZ4b4jFkSBe" -- session/git/patch.go session/git/patch_test.go session/patch.go session/patch_test.go session/README.md session/CLAUDE.md session/git/CLAUDE.md
```

---

### Task C: The core requests

**Files:**
- Modify: `core/iface.go`, `core/loop_core.go`, `core/events.go`, `core/requests.go`, `core/model.go`
- Create: `core/reads.go`, `core/reads_test.go`
- Modify: `core/requests_test.go`, `core/README.md`
- Regenerate: `core/rpc/methods_gen.go`, `docs/specs/protocol.md`
- Modify: `internal/modserver/realclaude_test.go`

- [ ] **Step 1: Write the failing core tests**

Create `core/reads_test.go`:

```go
package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/transcript"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const readsConv = "6f1e0c2a-2a4b-4c55-9d1e-0b6c2f3a4d5e"

// transcriptInst is a Paused Claude session whose launch named the
// transcript at path, as a restored record carries it.
func transcriptInst(t *testing.T, title, path string) *session.Instance {
	t.Helper()
	inst, err := session.FromInstanceData(session.InstanceData{
		Title: title, Status: session.Paused, Program: "claude",
		ClaudeSessionID: readsConv, ClaudeTranscriptPath: path,
	}, t.TempDir())
	require.NoError(t, err)
	return inst
}

// worktreeInst is a Running session on a worktree of a new repository,
// branched at its one commit, whose file gained a line since.
func worktreeInst(t *testing.T, title string) (*session.Instance, string) {
	t.Helper()
	repo := gitRepo(t)
	out, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	require.NoError(t, err)
	base := strings.TrimSpace(string(out))
	tree := filepath.Join(t.TempDir(), title)
	runGit(t, repo, "worktree", "add", "-q", "-b", "b-"+title, tree)
	require.NoError(t, os.WriteFile(filepath.Join(tree, "f"), []byte("x\ntwo\n"), 0o644))
	inst, err := session.FromInstanceData(session.InstanceData{
		Title: title, Path: repo, Status: session.Paused, Program: "claude",
		Worktree: session.GitWorktreeData{RepoPath: repo, WorktreePath: tree, SessionName: title, BranchName: "b-" + title, BaseCommitSHA: base},
	}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, inst.TransitionTo(session.Running))
	return inst, base
}

func TestReadTurns_RepliesWithAPageOfTheTranscript(t *testing.T) {
	path := filepath.Join(t.TempDir(), readsConv+".jsonl")
	require.NoError(t, os.WriteFile(path, []byte(
		`{"type":"user","timestamp":"2026-10-23T12:00:00.000Z","promptSource":"typed","message":{"role":"user","content":"run it"}}`+"\n"+
			`{"type":"assistant","timestamp":"2026-10-23T12:00:05.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Done."}]}}`+"\n"), 0o600))
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	inst := transcriptInst(t, "paused", path)
	ws.add(inst)
	m.SetWorkspacesForTest(ws)

	m.ReadTurns(m.idOf(inst), transcript.Query{}, 7)
	rs := replies(run(m, m.Drain()))
	require.Len(t, rs, 1)
	require.NoError(t, rs[0].Err)
	assert.Equal(t, ReqID(7), rs[0].Req)
	assert.Equal(t, readsConv, rs[0].Turns.Conversation)
	require.Len(t, rs[0].Turns.Turns, 1)
	assert.Equal(t, "run it", rs[0].Turns.Turns[0].Text)
	assert.Equal(t, "Done.", rs[0].Turns.Turns[0].Items[0].Text)
}

func TestReadTurns_RefusesASessionWhoseTranscriptIsUnknown(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	inst := statusInst(t, "fresh", session.Running)
	ws.add(inst)
	m.SetWorkspacesForTest(ws)

	m.ReadTurns(m.idOf(inst), transcript.Query{}, 3)
	out := m.Drain()
	assert.Empty(t, out.Jobs, "nothing to read")
	rs := replies(out)
	require.Len(t, rs, 1)
	assert.EqualError(t, rs[0].Err, "read the conversation of fresh: loom has not seen this session's transcript yet")
	assert.ErrorIs(t, rs[0].Err, ErrRefused)
}

func TestReadDiff_RepliesWithTheWorktreesPatch(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	inst, base := worktreeInst(t, "live")
	ws.add(inst)
	m.SetWorkspacesForTest(ws)

	m.ReadDiff(m.idOf(inst), 4)
	rs := replies(run(m, m.Drain()))
	require.Len(t, rs, 1)
	require.NoError(t, rs[0].Err)
	assert.Equal(t, base, rs[0].Diff.Base)
	require.Len(t, rs[0].Diff.Files, 1)
	f := rs[0].Diff.Files[0]
	assert.Equal(t, "f", f.Path)
	assert.Equal(t, 2, f.Added)
	assert.Equal(t, 1, f.Removed)
	assert.Nil(t, inst.GetDiffStats(), "the read caches nothing the views publish")
}
```

In `core/requests_test.go`, add after `runningTerminal`:

```go
// programInst builds a started, Running session of program.
func programInst(t *testing.T, title, program string) *session.Instance {
	t.Helper()
	inst, err := session.FromInstanceData(session.InstanceData{Title: title, Status: session.Paused, Program: program}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, inst.TransitionTo(session.Running))
	return inst
}
```

In `TestRequests_RefuseWhatTheTUIRefuses`, add `"aider": programInst(t, "aider", "aider"),` to the `insts` map, and these cases to the table (the reads have no key in the TUI; their gates are the model's own):

```go
		{"read the conversation of a terminal", func(f fixture) { f.m.ReadTurns(f.id["term"], transcript.Query{}, 1) }, "read the conversation of term: not allowed on a workspace terminal"},
		{"read the conversation of another agent", func(f fixture) { f.m.ReadTurns(f.id["aider"], transcript.Query{}, 1) }, "read the conversation of aider: the session does not run Claude"},
		{"read the diff of a terminal", func(f fixture) { f.m.ReadDiff(f.id["term"], 1) }, "read the diff of term: not allowed on a workspace terminal"},
		{"read the diff of a loading session", func(f fixture) { f.m.ReadDiff(f.id["loading"], 1) }, "read the diff of loading: the session is busy (Loading)"},
		{"read the diff of a deleting session", func(f fixture) { f.m.ReadDiff(f.id["deleting"], 1) }, "read the diff of deleting: the session is busy (Deleting)"},
		{"read the diff of a paused session", func(f fixture) { f.m.ReadDiff(f.id["paused"], 1) }, "read the diff of paused: the session is not running (Paused)"},
		{"read the diff of a recoverable session", func(f fixture) { f.m.ReadDiff(f.id["orphan"], 1) }, "read the diff of orphan: the session is not running (Recoverable)"},
		{"read the diff of an unstarted session", func(f fixture) { f.m.ReadDiff(f.id["new"], 1) }, "read the diff of new: the session has not started"},
```

and `"github.com/aidan-bailey/loom/session/transcript"` to its imports.

- [ ] **Step 2: Run them and see them fail**

Run: `CGO_ENABLED=0 go test ./core/ -run 'ReadTurns|ReadDiff|RefuseWhatTheTUIRefuses'`
Expected: FAIL to build (`m.ReadTurns undefined`, `rs[0].Turns undefined`, …).

- [ ] **Step 3: Declare the requests**

In `core/iface.go`, add `"github.com/aidan-bailey/loom/session/transcript"` to the imports, and after `FetchIssue(repo string, number int, req ReqID)`:

```go
	// A session's conversation and its worktree's diff, read for the
	// client that asks: the Reply's Turns (transcript.Read) and Diff
	// (Instance.ReadPatch).
	ReadTurns(id InstanceID, query transcript.Query, req ReqID)
	ReadDiff(id InstanceID, req ReqID)
```

In `core/loop_core.go`, add the import and, after the `FetchIssue` forwarder:

```go
func (l *Loop) ReadTurns(id InstanceID, query transcript.Query, req ReqID) {
	l.do(func(m *Model) { m.ReadTurns(id, query, req) })
}
func (l *Loop) ReadDiff(id InstanceID, req ReqID) { l.do(func(m *Model) { m.ReadDiff(id, req) }) }
```

In `core/events.go`, add `"github.com/aidan-bailey/loom/session/git"` and `"github.com/aidan-bailey/loom/session/transcript"` to the imports (when not already there), change the Reply's doc comment's "Issue is FetchIssue's result." to "Issue is FetchIssue's result, Turns ReadTurns' page and Diff ReadDiff's patch.", and add the fields after `Issue`:

```go
	Issue  github.Issue
	Turns  transcript.Page
	Diff   git.Patch
```

- [ ] **Step 4: Write the requests**

Create `core/reads.go`:

```go
package core

import (
	"fmt"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/git"
	"github.com/aidan-bailey/loom/session/transcript"
)

// turnsResult is ReadTurns' job result, and diffResult ReadDiff's.
type turnsResult struct {
	page transcript.Page
	err  error
}

type diffResult struct {
	patch git.Patch
	err   error
}

// ReadTurns reads a page of session id's conversation from Claude's
// transcript (transcript.Read) in a job, and answers with the Reply's
// Turns. The transcript is the one the session's launch named
// (Instance.ClaudeSession, read here on the loop); a session whose launch
// has not named one is refused.
func (m *Model) ReadTurns(id InstanceID, query transcript.Query, req ReqID) {
	inst, _, ok := m.admit(opReadTurns, id, req)
	if !ok {
		return
	}
	conversation, path := inst.ClaudeSession()
	if conversation == "" || path == "" {
		m.refuse(req, id, fmt.Errorf("%s %s: loom has not seen this session's transcript yet", opReadTurns, inst.Title))
		return
	}
	m.spawn(m.track(req, id, readTurnsJob(path, conversation, query)))
}

// readTurnsJob is ReadTurns' job: it reads only the file.
func readTurnsJob(path, conversation string, query transcript.Query) Job {
	return func() any {
		page, err := transcript.Read(path, conversation, query)
		return turnsResult{page: page, err: err}
	}
}

// ReadDiff reads session id's worktree diff against its base commit
// (Instance.ReadPatch) in a job, and answers with the Reply's Diff. Nothing
// is cached: the patch is the requester's alone.
func (m *Model) ReadDiff(id InstanceID, req ReqID) {
	inst, _, ok := m.admit(opReadDiff, id, req)
	if !ok {
		return
	}
	m.spawn(m.track(req, id, readDiffJob(inst)))
}

// readDiffJob is ReadDiff's job. It runs git off the loop through the
// instance's own locks, as the tick's probe does.
func readDiffJob(inst *session.Instance) Job {
	return func() any {
		patch, err := inst.ReadPatch()
		return diffResult{patch: patch, err: err}
	}
}
```

In `core/requests.go`:

- add to the `requestOp` constants:

```go
	opReadTurns requestOp = "read the conversation of"
	opReadDiff  requestOp = "read the diff of"
```

- add to `precondition`'s doc comment, after the send bullet:

```go
//   - read the conversation and read the diff: no key reads them, so their
//     gates are the model's own. A conversation is read for a Claude session
//     that is not a workspace terminal; a diff for a started worktree session
//     neither busy nor Paused or Recoverable, whose tree may be gutted (an
//     older loom's pause removed it), where git answers for the enclosing
//     repository.
```

- add to `precondition`'s switch, before `default:`:

```go
	case opReadTurns:
		switch {
		case inst.IsWorkspaceTerminal:
			why = "not allowed on a workspace terminal"
		case !session.IsClaudeProgram(inst.Program()):
			why = "the session does not run Claude"
		}
	case opReadDiff:
		switch {
		case inst.IsWorkspaceTerminal:
			why = "not allowed on a workspace terminal"
		case st == session.Loading || st == session.Deleting:
			why = fmt.Sprintf("the session is busy (%s)", st)
		case st == session.Paused || st == session.Recoverable:
			why = fmt.Sprintf("the session is not running (%s)", st)
		case !inst.Started():
			why = "the session has not started"
		}
```

- in `deliverTracked`'s switch, after `case issueResult:`:

```go
	case turnsResult:
		r.Turns = res.page
	case diffResult:
		r.Diff = res.patch
```

- in `outcome`'s switch, after `case issueResult:`:

```go
	case turnsResult:
		return r.err, nil
	case diffResult:
		return r.err, nil
```

In `core/model.go`'s `Deliver`, change `case issueResult:` to `case issueResult, turnsResult, diffResult:` (its comment, "Only its Reply carries it (deliverTracked).", stays).

- [ ] **Step 5: Regenerate the wire and the protocol reference**

Run: `CGO_ENABLED=0 go generate ./core/rpc && CGO_ENABLED=0 go test ./core/rpc -run TestProtocolReference -update`
Expected: `core/rpc/methods_gen.go` gains `ReadTurns` and `ReadDiff` (with the `transcript` import), and `docs/specs/protocol.md` the two methods and the Reply's `Turns` and `Diff`. No `rpc.Protocol` bump: adding needs none.

- [ ] **Step 6: Run the core and wire tests and see them pass**

Run: `CGO_ENABLED=0 go test ./core/ ./core/rpc/`
Expected: PASS: the new tests, `TestRequests_RefuseWhatTheTUIRefuses`, `TestLoopForwardsEachMethodToItsNamesake`, `TestMethods_CoverCore`, `TestGenerated_IsFresh`, `TestCoreIsValueTyped`, `TestWire_EveryFieldSurvives` (its filler sets the new fields) and `TestProtocolReference`. Each Reply reaching only its requester is `TestServe_EachClientGetsTheRepliesToItsOwnRequests` (`core/rpc/route_test.go`), which covers every Reply.

- [ ] **Step 7: The mod's real-Claude test reads the transcript back**

In `internal/modserver/realclaude_test.go`:

- add `session string // the hello's Claude session ID` to `queueHost`, set `h.session = hello.Session` in `ModHello` beside `h.commands = hello.Commands`, and add:

```go
// sessionID is the Claude session the hello named.
func (h *queueHost) sessionID() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.session
}

// queueAsUser queues text as the person's own words, as the model hands
// the phone's replies to the mod.
func (h *queueHost) queueAsUser(id, text string) {
	h.add(core.ModMessage{ID: id, Text: text, AsUser: true})
}
```

- send `m1` as the phone does: `host.queueAsUser("m1", "Reply with exactly the word PONG.")` in place of `host.queue("m1", …)`;
- before the final `t.Log(host.said())`, add:

```go
	// The transcript reads both messages back under their origins: m1 as
	// the person's own words, as the phone's replies go, and m2 under the
	// plugin's name, as a CLI send goes. No census transcript held a
	// plugin's prompt, so this pins the shape the conversation screen
	// labels.
	config := claudeConfigDir
	if config == "" {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		config = filepath.Join(home, ".claude")
	}
	paths, err := filepath.Glob(filepath.Join(config, "projects", "*", host.sessionID()+".jsonl"))
	require.NoError(t, err)
	require.Len(t, paths, 1, "the transcript of session %s", host.sessionID())
	page, err := transcript.Read(paths[0], host.sessionID(), transcript.Query{})
	require.NoError(t, err)
	from := map[string]string{}
	for _, turn := range page.Turns {
		switch {
		case strings.Contains(turn.Text, "PONG"):
			from["m1"] = turn.From + ":" + turn.Name
		case strings.Contains(turn.Text, "QUEUED"):
			from["m2"] = turn.From + ":" + turn.Name
		}
	}
	require.Equal(t, map[string]string{"m1": transcript.FromYou + ":", "m2": transcript.FromPlugin + ":loom"}, from)
```

- add `"github.com/aidan-bailey/loom/session/transcript"` to the imports.

Run: `CGO_ENABLED=0 go test ./internal/modserver/ -run TestRealClaude -v 2>&1 | tail -5`
Expected: SKIP without `LOOM_TEST_REAL_CLAUDE=1`; the package builds. The real run is the owner's to choose.

- [ ] **Step 8: The docs**

In `core/README.md`, in the paragraph that lists the requests ("Every lifecycle action is a request …"), change "… `Create(ws, NewInstance, req)` (builds and adds an instance and, with `Start`, configures and starts it) and `FetchIssue`. A non-zero `ReqID` is answered by a `Reply` (`Req`, `ID`, `Err`, `Notice`, `Issue`)" to "… `Create(ws, NewInstance, req)` (builds and adds an instance and, with `Start`, configures and starts it), `FetchIssue`, and the reads `ReadTurns` (a page of the session's conversation, `transcript.Read` over the transcript its launch named) and `ReadDiff` (its worktree's patch, `Instance.ReadPatch`; refused for a session not started, busy, Paused or Recoverable), whose gates are the model's own since no key reads them. A non-zero `ReqID` is answered by a `Reply` (`Req`, `ID`, `Err`, `Notice`, `Issue`, `Turns`, `Diff`)".

- [ ] **Step 9: Run everything touched, the lint and the race detector**

Run: `CGO_ENABLED=0 go test ./core/... ./internal/... ./session/... ./tools/claudemd/ && CC=clang CGO_ENABLED=1 go test -race ./core/ -run 'ReadTurns|ReadDiff' && gofmt -l core internal/modserver && CGO_ENABLED=0 go vet ./core/... ./internal/modserver/`
Expected: PASS, and `gofmt -l` prints nothing.

- [ ] **Step 10: Commit**

```bash
git add -- core/iface.go core/loop_core.go core/events.go core/requests.go core/model.go core/reads.go core/reads_test.go core/requests_test.go core/README.md core/rpc/methods_gen.go docs/specs/protocol.md internal/modserver/realclaude_test.go
git commit -m "feat(core,rpc): ReadTurns and ReadDiff, answered to the client that asks

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_019icsHbrFq5LeZ4b4jFkSBe" -- core/iface.go core/loop_core.go core/events.go core/requests.go core/model.go core/reads.go core/reads_test.go core/requests_test.go core/README.md core/rpc/methods_gen.go docs/specs/protocol.md internal/modserver/realclaude_test.go
```

---

### Task D: The web layer, the app and the docs

**Files:**
- Modify: `internal/web/reqs.go`, `internal/web/api.go`, `internal/web/fleet.go`, `internal/web/static_test.go`
- Create: `internal/web/reads.go`, `internal/web/reads_test.go`
- Create: `internal/web/static/js/reply.mjs`, `turns.mjs`, `changes.mjs`
- Modify: `internal/web/static/js/session.mjs`, `app.mjs`, `screens.mjs`, `skin.mjs`, `internal/web/static/app.css`
- Modify: `internal/web/README.md`, `internal/web/CLAUDE.md`, `docs/claude/web-layer.md`, `USAGE.md`

- [ ] **Step 1: Write the failing web tests**

Create `internal/web/reads_test.go`:

```go
package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const webConv = "0c6f9a52-3b1d-4e7a-9f20-6d1b8e4c2a11"

// apiWithTranscript is a fixture Claude session whose launch named a
// transcript of lines, as a restored record carries it.
func apiWithTranscript(t *testing.T, title string, lines ...string) *session.Instance {
	t.Helper()
	path := filepath.Join(t.TempDir(), webConv+".jsonl")
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600))
	inst, err := session.FromInstanceData(session.InstanceData{
		Title: title, Status: session.Paused, Program: "claude",
		ClaudeSessionID: webConv, ClaudeTranscriptPath: path,
	}, t.TempDir())
	require.NoError(t, err)
	return inst
}

// apiWithWorktree is a fixture Running session on a new repository whose
// file gained a line since the base commit; it returns the base's SHA.
func apiWithWorktree(t *testing.T, title string) (*session.Instance, string) {
	t.Helper()
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", repo}, args...)...)
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := c.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "notes.md"), []byte("one\n"), 0o644))
	git("add", ".")
	git("commit", "-qm", "init")
	base := git("rev-parse", "HEAD")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "notes.md"), []byte("one\ntwo\n"), 0o644))
	inst, err := session.FromInstanceData(session.InstanceData{
		Title: title, Path: repo, Status: session.Paused, Program: "claude",
		Worktree: session.GitWorktreeData{RepoPath: repo, WorktreePath: repo, SessionName: title, BranchName: "main", BaseCommitSHA: base},
	}, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, inst.TransitionTo(session.Running))
	return inst, base
}

// readAnswer makes a read whose job the loop holds, runs the job as the
// loop would, and returns the handler's answer.
func (e *apiEnv) readAnswer(path string) apiAnswer {
	e.t.Helper()
	ch := e.async(http.MethodGet, path, nil)
	job := e.heldJob()
	e.finish(job())
	return e.answer(ch)
}

// tracked reports whether the layer still tracks request req.
func (e *apiEnv) tracked(req core.ReqID) bool {
	e.layer.reqs.mu.Lock()
	defer e.layer.reqs.mu.Unlock()
	_, ok := e.layer.reqs.entries[req]
	return ok
}

var talkLines = []string{
	`{"type":"user","timestamp":"2026-10-23T12:20:00.000Z","promptSource":"typed","message":{"role":"user","content":"open the PR"}}`,
	`{"type":"assistant","timestamp":"2026-10-23T12:20:05.000Z","cwd":"/w","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"gh pr create"}}]}}`,
	`{"type":"assistant","timestamp":"2026-10-23T12:20:09.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Opened **#318**."}]}}`,
}

func TestAPI_TurnsIsAPageOfTheConversation(t *testing.T) {
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{apiWithTranscript(t, "talk", talkLines...)}})

	got := env.readAnswer(fmt.Sprintf("/api/v1/sessions/%d/turns", env.ids["talk"]))
	require.Equal(t, http.StatusOK, got.status, "body: %s", got.body)
	assert.JSONEq(t, `{
		"conversation": "`+webConv+`",
		"turns": [{
			"id": 0, "at": "2026-10-23T12:20:00Z", "from": "you", "name": "", "text": "open the PR",
			"cut": false, "command": false, "partial": false,
			"items": [
				{"kind": "steps", "at": "2026-10-23T12:20:05Z", "total": 1,
				 "steps": [{"tool": "Bash", "summary": "gh pr create", "at": "2026-10-23T12:20:05Z", "state": "pending"}]},
				{"kind": "text", "at": "2026-10-23T12:20:09Z",
				 "blocks": [{"t": "p", "c": [{"t": "text", "v": "Opened "}, {"t": "strong", "c": [{"t": "text", "v": "#318"}]}, {"t": "text", "v": "."}]}]}
			]
		}],
		"earlier": null, "reset": false, "skipped": 0
	}`, string(got.body), "a text item crosses as its tree, never as its text")
	assert.Contains(t, env.logged(), loggedAction{device: "phone", name: "turns", session: env.ids["talk"], result: "200 ok"},
		"logged without its content")
	assert.False(t, env.tracked(env.lastReq()), "an answered read holds no Reply")
}

func TestAPI_TurnsTakesItsCursorsAndRefusesBadOnes(t *testing.T) {
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{apiWithTranscript(t, "talk", talkLines...)}})
	base := fmt.Sprintf("/api/v1/sessions/%d/turns", env.ids["talk"])

	for _, q := range []string{"?before=-1", "?before=x", "?after=1.5", "?before=10&after=20"} {
		status, out := env.do(http.MethodGet, base+q, nil)
		assert.Equal(t, http.StatusBadRequest, status, q)
		assert.NotEmpty(t, apiErrorOf(t, out), q)
	}
	assert.Empty(t, env.loop.JobsForTest(), "a refused query reaches no model")

	got := env.readAnswer(base + "?conversation=an-earlier-one")
	require.Equal(t, http.StatusOK, got.status, "body: %s", got.body)
	var page turnsJSON
	require.NoError(t, json.Unmarshal(got.body, &page))
	assert.True(t, page.Reset, "another conversation's turns are replaced")
	assert.Len(t, page.Turns, 1)
}

func TestAPI_DiffIsTheWorktreesPatch(t *testing.T) {
	inst, base := apiWithWorktree(t, "live")
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{inst}})

	got := env.readAnswer(fmt.Sprintf("/api/v1/sessions/%d/diff", env.ids["live"]))
	require.Equal(t, http.StatusOK, got.status, "body: %s", got.body)
	assert.JSONEq(t, `{"base": "`+base+`", "files": [{
		"path": "notes.md", "old_path": "", "status": "M", "binary": false, "added": 1, "removed": 0, "omitted": "",
		"hunks": [{"header": "@@ -1 +1,2 @@", "lines": [" one", "+two"], "cut": []}]
	}]}`, string(got.body))
	assert.Contains(t, env.logged(), loggedAction{device: "phone", name: "diff", session: env.ids["live"], result: "200 ok"})
}

func TestAPI_AReadTheModelRefusesIs409WithItsText(t *testing.T) {
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{apiInstance(t, "fresh", session.Ready), apiInstance(t, "resting", session.Paused)}})

	status, out := env.do(http.MethodGet, fmt.Sprintf("/api/v1/sessions/%d/turns", env.ids["fresh"]), nil)
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "read the conversation of fresh: loom has not seen this session's transcript yet", apiErrorOf(t, out))

	status, out = env.do(http.MethodGet, fmt.Sprintf("/api/v1/sessions/%d/diff", env.ids["resting"]), nil)
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "read the diff of resting: the session is not running (Paused)", apiErrorOf(t, out))
}

func TestAPI_ReadsOfAWorkspaceTerminalOrAnUnknownSessionAre404(t *testing.T) {
	term, err := session.NewInstance(session.InstanceOptions{Title: "term", Path: t.TempDir(), Program: "claude", IsWorkspaceTerminal: true})
	require.NoError(t, err)
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{term}})
	for _, path := range []string{
		fmt.Sprintf("/api/v1/sessions/%d/turns", env.ids["term"]),
		fmt.Sprintf("/api/v1/sessions/%d/diff", env.ids["term"]),
		"/api/v1/sessions/999999/turns",
		"/api/v1/sessions/999999/diff",
	} {
		status, out := env.do(http.MethodGet, path, nil)
		assert.Equal(t, http.StatusNotFound, status, path)
		assert.Equal(t, "no such session", apiErrorOf(t, out), path)
	}
}

func TestAPI_AReadIsNotTrackedOnceAnsweredAndATimedOutOneIs504(t *testing.T) {
	inst, _ := apiWithWorktree(t, "live")
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{inst}})
	path := fmt.Sprintf("/api/v1/sessions/%d/diff", env.ids["live"])

	got := env.readAnswer(path)
	require.Equal(t, http.StatusOK, got.status)
	assert.False(t, env.tracked(env.lastReq()), "an answered read holds no Reply")

	late := env.lateRecorder()
	env.layer.reqs.timeout = 100 * time.Millisecond
	ch := env.async(http.MethodGet, path, nil)
	job := env.heldJob()
	slow := env.answer(ch)
	assert.Equal(t, http.StatusGatewayTimeout, slow.status)
	assert.Equal(t, "loom did not answer in time", apiErrorOf(t, slow.body))
	assert.False(t, env.tracked(env.lastReq()))

	env.finish(job())
	select {
	case n := <-late:
		t.Fatalf("a read's late Reply reached the stream: %+v", n)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestSessionOf_ClaudeSaysTheSessionRunsClaude(t *testing.T) {
	assert.True(t, sessionOf(1, core.InstanceView{Program: "claude"}).Claude)
	assert.True(t, sessionOf(1, core.InstanceView{Program: "claude --model opus"}).Claude)
	assert.False(t, sessionOf(1, core.InstanceView{Program: "aider"}).Claude)
}
```

In `internal/web/static_test.go`'s `TestStatic_TheSkinIsDarkWithEveryToken`, add `"add", "del"` to the token list (after `"error"`).

- [ ] **Step 2: Run them and see them fail**

Run: `CGO_ENABLED=0 go test ./internal/web/ -run 'Turns|Diff|ARead|Reads|SessionOf_Claude|TheSkinIsDark'`
Expected: FAIL to build (`undefined: turnsJSON`, `….Claude undefined`).

- [ ] **Step 3: Write the read routes**

In `internal/web/reqs.go`, after `take`:

```go
// drop stops tracking request req once its handler is done with it: a
// read's, whose Reply carries its data and which no notice follows. Kept,
// the Reply would hold a page or a patch for reqKeep, and one arriving
// after the wait gave up would reach the device's stream as a notice.
func (r *reqs) drop(req core.ReqID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.entries, req)
}
```

Create `internal/web/reads.go`:

```go
package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session/git"
	"github.com/aidan-bailey/loom/session/transcript"
)

// The read routes: a session's conversation (GET …/turns) and its
// worktree's diff (GET …/diff). Each is a Core request whose Reply carries
// the data; none of the data is logged.

// turnsJSON is a page of a session's conversation (transcript.Page) as the
// app reads it. A text item crosses as its tree, never as its text.
type turnsJSON struct {
	Conversation string     `json:"conversation"`
	Turns        []turnJSON `json:"turns"`
	// Earlier is the before of the page before this one: null at the start
	// of the file and on a refresh's page.
	Earlier *int64 `json:"earlier"`
	Reset   bool   `json:"reset"`
	Skipped int    `json:"skipped"`
}

type turnJSON struct {
	ID      int64      `json:"id"`
	At      string     `json:"at"`
	From    string     `json:"from"`
	Name    string     `json:"name"`
	Text    string     `json:"text"`
	Cut     bool       `json:"cut"`
	Command bool       `json:"command"`
	Partial bool       `json:"partial"`
	Items   []itemJSON `json:"items"`
}

// itemJSON is one item of a turn. Its fields follow its kind: a text item
// has blocks and truncated, a steps item total, failed and steps, a note
// text and error; each has at.
type itemJSON struct {
	Kind      string     `json:"kind"`
	At        string     `json:"at"`
	Blocks    []*mdNode  `json:"blocks,omitempty"`
	Truncated bool       `json:"truncated,omitempty"`
	Text      string     `json:"text,omitempty"`
	Error     bool       `json:"error,omitempty"`
	Total     int        `json:"total,omitempty"`
	Failed    int        `json:"failed,omitempty"`
	Steps     []stepJSON `json:"steps,omitempty"`
}

type stepJSON struct {
	Tool    string `json:"tool"`
	Summary string `json:"summary"`
	At      string `json:"at"`
	State   string `json:"state"`
}

// patchJSON is a session's diff (git.Patch) as the app reads it.
type patchJSON struct {
	Base  string     `json:"base"`
	Files []fileJSON `json:"files"`
}

type fileJSON struct {
	Path    string     `json:"path"`
	OldPath string     `json:"old_path"`
	Status  string     `json:"status"`
	Binary  bool       `json:"binary"`
	Added   int        `json:"added"`
	Removed int        `json:"removed"`
	Hunks   []hunkJSON `json:"hunks"`
	Omitted string     `json:"omitted"`
}

type hunkJSON struct {
	Header string   `json:"header"`
	Lines  []string `json:"lines"`
	Cut    []int    `json:"cut"`
}

// stamp is t in RFC 3339, in UTC, and "" for the zero time.
func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// turnsOf is a page as the app reads it, each text item parsed into its
// tree (buildTree).
func turnsOf(p transcript.Page) turnsJSON {
	out := turnsJSON{Conversation: p.Conversation, Turns: make([]turnJSON, 0, len(p.Turns)), Reset: p.Reset, Skipped: p.Skipped}
	if p.Earlier > 0 {
		earlier := p.Earlier
		out.Earlier = &earlier
	}
	for _, t := range p.Turns {
		tj := turnJSON{
			ID: t.ID, At: stamp(t.At), From: t.From, Name: t.Name, Text: t.Text,
			Cut: t.Cut, Command: t.Command, Partial: t.Partial, Items: make([]itemJSON, 0, len(t.Items)),
		}
		for _, it := range t.Items {
			ij := itemJSON{Kind: it.Kind, At: stamp(it.At)}
			switch it.Kind {
			case transcript.KindText:
				var cut bool
				ij.Blocks, cut = buildTree(it.Text)
				ij.Truncated = cut || it.Cut
			case transcript.KindSteps:
				ij.Total, ij.Failed = it.Total, it.Failed
				ij.Steps = make([]stepJSON, 0, len(it.Steps))
				for _, s := range it.Steps {
					ij.Steps = append(ij.Steps, stepJSON{Tool: s.Tool, Summary: s.Summary, At: stamp(s.At), State: s.State})
				}
			case transcript.KindNote:
				ij.Text, ij.Error = it.Text, it.Error
			}
			tj.Items = append(tj.Items, ij)
		}
		out.Turns = append(out.Turns, tj)
	}
	return out
}

// patchOf is a patch as the app reads it, with every list non-null.
func patchOf(p git.Patch) patchJSON {
	out := patchJSON{Base: p.Base, Files: make([]fileJSON, 0, len(p.Files))}
	for _, f := range p.Files {
		fj := fileJSON{
			Path: f.Path, OldPath: f.OldPath, Status: f.Status, Binary: f.Binary,
			Added: f.Added, Removed: f.Removed, Omitted: f.Omitted, Hunks: make([]hunkJSON, 0, len(f.Hunks)),
		}
		for _, h := range f.Hunks {
			hj := hunkJSON{Header: h.Header, Lines: h.Lines, Cut: h.Cut}
			if hj.Lines == nil {
				hj.Lines = []string{}
			}
			if hj.Cut == nil {
				hj.Cut = []int{}
			}
			fj.Hunks = append(fj.Hunks, hj)
		}
		out.Files = append(out.Files, fj)
	}
	return out
}

// readSession is the session a read route names, answered 404 when the
// path names none or a workspace terminal, which is no session of the
// app's.
func (l *Layer) readSession(w http.ResponseWriter, r *http.Request, a *actionLog) (core.InstanceID, bool) {
	id, ok := l.sessionFromPath(w, r, *a)
	if !ok {
		return 0, false
	}
	a.sessionID = id
	if v, _ := l.client.View(id); v.IsWorkspaceTerminal {
		l.refuse(w, *a, http.StatusNotFound, "no such session")
		return 0, false
	}
	return id, true
}

// read makes the read request send names and waits for its Reply, then
// stops tracking the request (reqs.drop). It answers a failure itself (the
// model's refusal with its status, 504 when the model did not answer
// within replyWait) and otherwise returns the Reply with true.
func (l *Layer) read(w http.ResponseWriter, r *http.Request, a actionLog, send func(req core.ReqID)) (core.Reply, bool) {
	req := l.reqs.next()
	defer l.reqs.drop(req)
	send(req)
	reply, _, done := l.reqs.wait(r.Context(), req, a.device)
	switch {
	case !done:
		l.refuse(w, a, http.StatusGatewayTimeout, "loom did not answer in time")
	case reply.Err != nil:
		l.refuse(w, a, modelStatus(reply.Err), reply.Err.Error())
	default:
		return reply, true
	}
	return core.Reply{}, false
}

// readTurns answers GET /api/v1/sessions/{id}/turns: a page of the
// session's conversation, as the query's conversation, before and after
// ask.
func (l *Layer) readTurns(w http.ResponseWriter, r *http.Request, device string) {
	a := actionLog{device: device, name: "turns"}
	id, ok := l.readSession(w, r, &a)
	if !ok {
		return
	}
	q, err := turnsQuery(r)
	if err != nil {
		l.refuse(w, a, http.StatusBadRequest, err.Error())
		return
	}
	reply, ok := l.read(w, r, a, func(req core.ReqID) { l.client.ReadTurns(id, q, req) })
	if !ok {
		return
	}
	l.record(a, http.StatusOK, "ok")
	writeJSON(w, http.StatusOK, turnsOf(reply.Turns))
}

// turnsQuery reads the turns route's query: before and after are byte
// offsets, and at most one of them is set.
func turnsQuery(r *http.Request) (transcript.Query, error) {
	v := r.URL.Query()
	q := transcript.Query{Conversation: v.Get("conversation")}
	for _, c := range []struct {
		name string
		to   *int64
	}{{"before", &q.Before}, {"after", &q.After}} {
		s := v.Get(c.name)
		if s == "" {
			continue
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n < 0 {
			return transcript.Query{}, fmt.Errorf("%s %q: want a byte offset", c.name, s)
		}
		*c.to = n
	}
	if q.Before > 0 && q.After > 0 {
		return transcript.Query{}, errors.New("before and after: give at most one")
	}
	return q, nil
}

// readDiff answers GET /api/v1/sessions/{id}/diff: the session's worktree
// diff against its base commit, file by file.
func (l *Layer) readDiff(w http.ResponseWriter, r *http.Request, device string) {
	a := actionLog{device: device, name: "diff"}
	id, ok := l.readSession(w, r, &a)
	if !ok {
		return
	}
	reply, ok := l.read(w, r, a, func(req core.ReqID) { l.client.ReadDiff(id, req) })
	if !ok {
		return
	}
	l.record(a, http.StatusOK, "ok")
	writeJSON(w, http.StatusOK, patchOf(reply.Diff))
}
```

In `internal/web/api.go`'s `routeAPI`, after the message route:

```go
	m.Handle("GET /api/v1/sessions/{id}/turns", l.authed(l.readTurns))
	m.Handle("GET /api/v1/sessions/{id}/diff", l.authed(l.readDiff))
```

In `internal/web/fleet.go`, add to `sessionJSON` after `RemoteURL`:

```go
	// Claude says the session runs Claude, whose conversation the app can
	// read (GET …/turns).
	Claude bool `json:"claude"`
```

and to `sessionOf`'s literal: `Claude: session.IsClaudeProgram(v.Program),`.

- [ ] **Step 4: Run the web tests and see them pass**

Run: `CGO_ENABLED=0 go test ./internal/web/`
Expected: PASS but `TestStatic_TheSkinIsDarkWithEveryToken`, which waits for Step 7's tokens. If the text item's `blocks` differ from the expectation only in how `buildTree` splits its text nodes, the expectation follows `buildTree`: copy its actual output into the test.

- [ ] **Step 5: The shared reply box, the sub-line and the ways in**

In `internal/web/static/js/skin.mjs`, after `OutIcon`:

```js
// ChevronIcon marks a tile that opens another screen of the app.
export function ChevronIcon() {
  return html`<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M9 6l6 6-6 6"></path></svg>`;
}
```

Create `internal/web/static/js/reply.mjs`:

```js
// The reply box a session's screens end in: the quick replies while it is
// empty, a text box and Send. run is the screen's own runner, so the box
// and the screen's actions are off together while either is in flight.
import { html, useState } from "../lib/preact-htm.mjs";
import { utf8Len } from "./sheets.mjs";

export const MAX_REPLY = 64 * 1024;

export function ReplyBox({ snap, off, run }) {
  const [text, setText] = useState("");
  const reply = text.trim();
  const tooLong = utf8Len(reply) > MAX_REPLY;
  const replies = snap.quick_replies || [];

  async function send() {
    const r = await run("send", { text: reply });
    if (r.ok && r.status !== 202) setText("");
  }

  return html`<form class="sc-reply" onSubmit=${(e) => { e.preventDefault(); if (!off && reply && !tooLong) send(); }}>
    ${text === "" && replies.length > 0
      ? html`<div class="sc-quick" role="group" aria-label="Quick replies">
          ${replies.map(
            (q) => html`<button key=${q} class="sc-chip-btn" type="button" disabled=${off} onClick=${() => setText(q)}><span>${q}</span></button>`,
          )}
        </div>`
      : null}
    <div class="sc-reply-row">
      <textarea
        rows="2"
        placeholder="reply, or /command…"
        aria-label="Reply"
        value=${text}
        onInput=${(e) => setText(e.target.value)}
      ></textarea>
      <button class="sc-btn sc-primary" type="submit" disabled=${off || !reply || tooLong}>send</button>
    </div>
    ${tooLong ? html`<div class="sc-small sc-err">Too long: a reply is at most 64 KiB.</div>` : null}
  </form>`;
}
```

Replace `internal/web/static/js/session.mjs` with:

```js
// The session screen: why it waits, its tiles, the last message, its agents,
// the actions, and the reply box with the quick replies. The changes tile
// opens the changes screen, and the last message's rule the conversation.
import { html, useState, useEffect } from "../lib/preact-htm.mjs";
import { call } from "./api.mjs";
import { ConfirmSheet } from "./sheets.mjs";
import { allSessions, ago, markSeen } from "./fleet.mjs";
import { GLYPHS, glyphOf, OutIcon, ChevronIcon } from "./skin.mjs";
import { usageFor, shareText, isStale } from "./usage.mjs";
import { Markdown } from "./markdown.mjs";
import { ReplyBox } from "./reply.mjs";

// actionsFor says which buttons to show. It follows status only; the
// daemon's admit stays the authority and a refusal comes back as a toast.
// It shows only what admit accepts: every op but resume is refused while the
// session is Loading or Deleting, and resume is refused unless it is Paused
// (a Recoverable or Not started session has nothing to resume). diff is the
// changes screen's link, as send: ReadDiff refuses a busy session and one in
// the paused group.
export function actionsFor(s) {
  const busy = s.status === "Loading" || s.status === "Deleting";
  const paused = s.group === "paused";
  // Pause keeps the worktree, so push and merge work on a Paused session; a
  // Recoverable or Not started one may have no worktree to work on.
  const tree = !paused || s.status === "Paused";
  return {
    pause: !busy && !paused,
    resume: s.status === "Paused",
    push: !busy && tree,
    merge: !busy && tree,
    send: !busy && !paused,
    diff: !busy && !paused,
    kill: !busy,
  };
}

// mergeCandidates: the other sessions of the same workspace, whose branch
// can be merged into this one.
export function mergeCandidates(snap, s) {
  return allSessions(snap).filter((o) => o.workspace === s.workspace && o.id !== s.id);
}

// parityText is the TUI's parity token: "↑2 ↓0", "✓ even" at 0 and 0, and ""
// while parity is unknown.
export function parityText(s) {
  if (!s.parity_known) return "";
  if (!s.ahead && !s.behind) return "✓ even";
  return "↑" + s.ahead + " ↓" + s.behind;
}

// checksText is the PR tile's sub-line: an open PR's checks, else its state.
export function checksText(pr) {
  if (pr.state !== "open") return pr.state;
  return { passing: "✓ checks pass", pending: "checks pending", failing: "✗ checks fail", none: "no checks" }[pr.checks] || "";
}

// SubLine is the line under a session's title on its screens: its status
// and age, its workspace and its branch.
export function SubLine({ s, snap, now }) {
  const ws = (snap.workspaces || []).find((w) => w.id === s.workspace);
  return html`<div class="sc-subline">
    <span><span class=${"sc-glyph sc-glyph-" + s.group} aria-hidden="true">${glyphOf(s.group)}</span> ${s.label.toLowerCase()}${s.since ? " · " + ago(s.since, now) : ""}</span>
    ${ws ? html`<span class="sc-ws">${ws.label || ws.name}</span>` : null}
    ${s.branch ? html`<span>${s.branch}</span>` : null}
  </div>`;
}

// Tiles are the session's facts as boxes: its changes (a link to the changes
// screen when diff is set), its pull request (a link to it on GitHub when the
// daemon has its URL) and its account's usage. A tile whose data is missing
// is left out, and the row reflows.
function Tiles({ s, snap, now, diff }) {
  const tiles = [];
  const d = s.diff || {};
  if (d.added || d.removed || s.parity_known) {
    const parity = parityText(s);
    const inner = html`
      <span class="sc-legend">Changes</span>
      <span class="sc-tile-fig"><span class="sc-add">+${d.added || 0}</span> <span class="sc-del">−${d.removed || 0}</span>${diff ? html`<${ChevronIcon} />` : null}</span>
      ${parity ? html`<span class="sc-tile-sub">${parity}</span>` : null}
    `;
    tiles.push(
      diff
        ? html`<a key="changes" class="sc-box sc-tile" href=${"#s/" + s.id + "/diff"}>${inner}</a>`
        : html`<div key="changes" class="sc-box sc-tile">${inner}</div>`,
    );
  }
  if (s.pr) {
    const inner = html`
      <span class="sc-legend">PR</span>
      <span class="sc-tile-fig">#${s.pr.number}${s.pr.url ? html`<${OutIcon} />` : null}</span>
      <span class="sc-tile-sub">${checksText(s.pr)}</span>
    `;
    tiles.push(
      s.pr.url
        ? html`<a key="pr" class="sc-box sc-tile" href=${s.pr.url} target="_blank" rel="noopener noreferrer">${inner}</a>`
        : html`<div key="pr" class="sc-box sc-tile">${inner}</div>`,
    );
  }
  const u = usageFor(snap, s.account);
  const w = u && (u.five_hour || u.seven_day);
  if (w) {
    const old = isStale(u.at, snap.usage_stale_after_s, now);
    tiles.push(html`<div key="account" class=${"sc-box sc-tile" + (old ? " sc-stale" : "")}>
      <span class="sc-legend">Account</span>
      <span class="sc-tile-fig">${shareText(w, now)}</span>
      <span class="sc-tile-sub">${u.account} ${u.five_hour ? "5h" : "7d"}</span>
    </div>`);
  }
  return tiles.length > 0 ? html`<div class="sc-tiles">${tiles}</div>` : null;
}

// Message is the last message: its tree once fetched for this very text, the
// plain text before that and whenever the fetch fails. For a Claude session
// its rule ends in the way into the conversation, shown even before the
// first message, which is when a first turn is worth watching.
function Message({ s, tree }) {
  const ok = s.last_message && tree && tree.text === s.last_message;
  return html`
    <section class="sc-section">
      <h2 class="sc-rule">Last message${s.claude ? html`<a class="sc-rule-link" href=${"#s/" + s.id + "/turns"}>conversation ›</a>` : null}</h2>
      ${!s.last_message
        ? html`<p class="sc-pre sc-dim">No message yet.</p>`
        : ok
          ? html`<${Markdown} blocks=${tree.blocks} />`
          : html`<p class="sc-pre">${s.last_message}</p>`}
      ${ok && tree.truncated
        ? html`<p class="sc-dim sc-small">… continues in ${s.remote_url ? html`<a href=${s.remote_url} target="_blank" rel="noopener">Claude</a>` : "Claude"}</p>`
        : null}
    </section>
  `;
}

// Agents are the session's subagents by name, each running or idle, with its
// description under it.
function Agents({ agents }) {
  return html`
    <section class="sc-section">
      <h2 class="sc-rule">Agents ${agents.length}</h2>
      ${agents.map(
        (a, i) => html`<div key=${i} class="sc-agent">
          <div class="sc-row-top">
            <span class="sc-glyph" aria-hidden="true">${a.idle ? "○" : "◐"}</span>
            <span class="sc-row-title">${a.name}</span>
            <span class="sc-age">${a.idle ? "idle" : "running"}</span>
          </div>
          ${a.description ? html`<div class="sc-agent-desc">${a.description}</div>` : null}
        </div>`,
      )}
    </section>
  `;
}

export function Session({ snap, stale, route, go, act, toast, pending }) {
  const id = route.id;
  const [sheet, setSheet] = useState("");
  const [source, setSource] = useState("");
  const [busy, setBusy] = useState(false);
  const [tree, setTree] = useState(null);

  // The time this phone last opened the session feeds the fleet's NEW mark;
  // it is written on arrival and again on leaving, so a turn that ended while
  // the session was open does not light it.
  useEffect(() => {
    setSheet("");
    setTree(null);
    if (id === undefined) return undefined;
    markSeen(id);
    return () => markSeen(id);
  }, [id]);

  const s = snap && id !== undefined ? allSessions(snap).find((x) => x.id === id) : undefined;
  const msg = s ? s.last_message : "";

  // The tree is fetched for the message the stream holds, and again when the
  // stream brings another. The plain text shows until it arrives, and stays
  // when the fetch fails or the app is offline.
  useEffect(() => {
    if (id === undefined || !msg || stale || (tree && tree.text === msg)) return undefined;
    let live = true;
    call("GET", "/api/v1/sessions/" + id + "/message").then(({ status, data }) => {
      if (live && status === 200 && data && data.text === msg) setTree(data);
    });
    return () => {
      live = false;
    };
  }, [id, msg, stale]);

  if (!s) {
    return html`
      <section class="sc-screen">
        <nav class="sc-topbar"><a class="sc-back" href="#">‹ fleet</a></nav>
        <p class="sc-dim sc-empty">${snap && pending !== id ? "This session is gone." : "Loading…"}</p>
      </section>
    `;
  }

  const can = actionsFor(s);
  const off = stale || busy;
  const base = "/api/v1/sessions/" + s.id;
  const candidates = mergeCandidates(snap, s);
  const waiting = s.group === "needs_you";
  const now = Date.now();

  async function run(action, body) {
    setBusy(true);
    try {
      const r = await act("POST", base + "/" + action, body);
      if (r.status === 202) toast("Still running; the result will show here.", false);
      return r;
    } finally {
      setBusy(false);
    }
  }

  async function kill() {
    const r = await run("kill");
    setSheet("");
    if (r.ok && r.status !== 202) go("#");
  }

  async function pushBranch() {
    await run("push");
    setSheet("");
  }

  async function merge() {
    await run("merge", { source: Number(source) });
    setSheet("");
  }

  function openMerge() {
    if (candidates.length === 0) {
      toast("No other session in this workspace to merge in.", true);
      return;
    }
    setSource(String(candidates[0].id));
    setSheet("merge");
  }

  return html`
    <section class="sc-screen sc-session">
      <nav class="sc-topbar" aria-label="Session">
        <a class="sc-back" href="#">‹ fleet</a>
        <span class="sc-fill"></span>
        ${s.remote_url && !waiting
          ? html`<a class="sc-claude" href=${s.remote_url} target="_blank" rel="noopener">claude <${OutIcon} /></a>`
          : null}
      </nav>
      <h1 class="sc-h1 sc-wrap">${s.title}</h1>
      <${SubLine} s=${s} snap=${snap} now=${now} />
      ${waiting
        ? html`<div class="sc-box sc-attn-box">
            <span class="sc-legend">${GLYPHS.needs_you} Needs you</span>
            <div class="sc-attn">${(s.wait && s.wait.reason) || "Needs you"}</div>
            ${s.wait && s.wait.detail ? html`<pre class="sc-detail-box">${s.wait.detail}</pre>` : null}
            ${s.remote_url
              ? html`<a class="sc-btn sc-primary sc-block" href=${s.remote_url} target="_blank" rel="noopener">Open in Claude</a>`
              : html`<p class="sc-dim sc-small">Remote Control is off for this session.</p>`}
          </div>`
        : null}
      <${Tiles} s=${s} snap=${snap} now=${now} diff=${can.diff} />
      ${s.last_message || s.claude ? html`<${Message} s=${s} tree=${tree} />` : null}
      ${s.agents && s.agents.length > 0 ? html`<${Agents} agents=${s.agents} />` : null}

      <section class="sc-actions">
        ${can.pause ? html`<button class="sc-btn" type="button" disabled=${off} onClick=${() => run("pause")}>pause</button>` : null}
        ${can.resume ? html`<button class="sc-btn sc-primary" type="button" disabled=${off} onClick=${() => run("resume")}>resume</button>` : null}
        ${can.push ? html`<button class="sc-btn" type="button" disabled=${off} onClick=${() => setSheet("push")}>push</button>` : null}
        ${can.merge ? html`<button class="sc-btn" type="button" disabled=${off} onClick=${openMerge}>merge</button>` : null}
        ${can.kill ? html`<button class="sc-btn sc-danger-text" type="button" disabled=${off} onClick=${() => setSheet("kill")}>kill</button>` : null}
      </section>

      ${can.send
        ? html`<${ReplyBox} snap=${snap} off=${off} run=${run} />`
        : s.group === "paused"
          ? html`<p class="sc-dim sc-small">Resume the session to reply.</p>`
          : null}

      ${sheet === "kill"
        ? html`<${ConfirmSheet}
            title="Kill this session?"
            confirm="Kill"
            danger=${true}
            body=${html`<div>
              <p>${s.tmux ? html`It ends tmux session <code>${s.tmux}</code>.` : "It ends the session's tmux session."}</p>
              <p>${s.worktree ? html`It force-removes worktree <code>${s.worktree}</code>` : "It force-removes the session's worktree"}, discarding its uncommitted changes. Push first to keep them.</p>
              ${s.status === "Paused"
                ? html`<p>If an older loom paused it, its worktree is gone and its changes may be in a stash, which Kill drops.</p>`
                : null}
              <p>It deletes branch <code>${s.branch}</code> unless the session checked out an existing branch.</p>
            </div>`}
            onConfirm=${kill}
            onCancel=${() => setSheet("")}
          />`
        : null}
      ${sheet === "push"
        ? html`<${ConfirmSheet}
            title="Push this branch?"
            confirm="Commit and push"
            body=${html`<p>This commits the session's uncommitted changes first, then pushes <code>${s.branch}</code>.</p>`}
            onConfirm=${pushBranch}
            onCancel=${() => setSheet("")}
          />`
        : null}
      ${sheet === "merge"
        ? html`<${ConfirmSheet}
            title="Merge a branch in"
            confirm="Merge"
            body=${html`<p>Merge which session's branch into <code>${s.branch}</code>?</p>
              <select value=${source} onChange=${(e) => setSource(e.target.value)} aria-label="Session to merge in">
                ${candidates.map((o) => html`<option key=${o.id} value=${String(o.id)}>${o.title} (${o.branch})</option>`)}
              </select>`}
            onConfirm=${merge}
            onCancel=${() => setSheet("")}
          />`
        : null}
    </section>
  `;
}
```

- [ ] **Step 6: The conversation and changes screens, and their routes**

Create `internal/web/static/js/turns.mjs`:

```js
// The conversation screen: a session's turns from Claude's transcript
// (GET /api/v1/sessions/{id}/turns), oldest first and opening at the newest;
// earlier pages on request, and the newest turn refreshed while the session
// works. Claude's text is drawn from its tree, everything else as text.
import { html, useState, useEffect, useRef, useLayoutEffect } from "../lib/preact-htm.mjs";
import { call } from "./api.mjs";
import { allSessions, ago } from "./fleet.mjs";
import { Markdown } from "./markdown.mjs";
import { SubLine, actionsFor } from "./session.mjs";
import { ReplyBox } from "./reply.mjs";

// POLL is how often the screen refreshes the newest turn while the session
// is Running; PROMPT_LINES how many lines of a prompt show before "more".
const POLL = 5000;
const PROMPT_LINES = 12;

// mergeTurns folds a page into the turns held: the first page and a reset
// replace them, an earlier page goes before them, and a refresh replaces
// every held turn from its first turn on. skipped stays set once a page
// reported a record it could not read.
export function mergeTurns(held, page, kind) {
  const skipped = Boolean((held && held.skipped) || page.skipped > 0);
  const turns = page.turns || [];
  if (!held || page.reset || kind === "newest") {
    return { conversation: page.conversation, turns, earlier: page.earlier || 0, skipped };
  }
  if (kind === "earlier") {
    const first = held.turns.length > 0 ? held.turns[0].id : Infinity;
    return { ...held, turns: turns.filter((t) => t.id < first).concat(held.turns), earlier: page.earlier || 0, skipped };
  }
  if (turns.length === 0) return { ...held, skipped };
  const from = turns[0].id;
  return { ...held, turns: held.turns.filter((t) => t.id < from).concat(turns), skipped };
}

// turnLabel is the label in a turn's box: whose the prompt is.
export function turnLabel(t) {
  switch (t.from) {
    case "you":
      return "YOU";
    case "plugin":
      return (t.name || "plugin").toUpperCase();
    case "peer":
      return t.name ? "FROM " + t.name.toUpperCase() : "FROM ANOTHER SESSION";
    case "task":
      return "BACKGROUND TASK";
    default:
      return String(t.from || "").toUpperCase();
  }
}

// clock is a time as the phone's 24-hour hh:mm, "" for none.
export function clock(iso) {
  const t = Date.parse(iso || "");
  if (Number.isNaN(t)) return "";
  return new Date(t).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false });
}

function Prompt({ t }) {
  const [open, setOpen] = useState(false);
  const lines = (t.text || "").split("\n");
  const folded = lines.length > PROMPT_LINES && !open;
  const at = clock(t.at);
  return html`<div class="sc-box sc-prompt">
    <span class="sc-legend">${turnLabel(t)}${at ? " · " + at : ""}</span>
    <div class=${"sc-pre" + (t.command ? " sc-cmd" : "")}>${folded ? lines.slice(0, PROMPT_LINES).join("\n") : t.text}${t.cut && !folded ? "…" : ""}</div>
    ${folded ? html`<button class="sc-more" type="button" onClick=${() => setOpen(true)}>▸ ${lines.length - PROMPT_LINES} more lines</button>` : null}
  </div>`;
}

// Step is one row of a fold: its state's glyph, the tool, the summary, and
// for a pending step "running" (only in the newest turn while the session
// is Running) or "no result".
function Step({ st, live, now }) {
  const running = live && st.state === "pending";
  let glyph = "";
  let cls = "sc-dim";
  if (running) {
    glyph = "◐";
    cls = "sc-accent";
  } else if (st.state === "failed") {
    glyph = "×";
    cls = "sc-err";
  } else if (st.state === "interrupted") {
    glyph = "×";
  }
  const end = running ? "running " + ago(st.at, now) : st.state === "pending" ? "no result" : "";
  return html`
    <span class=${"sc-step-glyph " + cls} aria-hidden="true">${glyph}</span>
    <span class="sc-step-tool">${st.tool}</span>
    <span class="sc-step-sum">${st.summary}</span>
    <span class=${"sc-step-end " + (running ? "sc-accent" : "sc-dim")}>${end}</span>
  `;
}

function Steps({ it, live, open, now }) {
  const total = it.total || 0;
  const failed = it.failed || 0;
  const steps = it.steps || [];
  const hidden = total - steps.length;
  return html`<details class="sc-steps" open=${open}>
    <summary>${total} ${total === 1 ? "step" : "steps"}${failed ? html`<span class="sc-err"> · ${failed} failed</span>` : null}</summary>
    ${hidden > 0 ? html`<p class="sc-dim sc-small">${hidden} earlier steps not shown</p>` : null}
    <div class="sc-step-grid">${steps.map((st, i) => html`<${Step} key=${i} st=${st} live=${live} now=${now} />`)}</div>
  </details>`;
}

function Item({ it, live, open, now, remote }) {
  switch (it.kind) {
    case "text":
      return html`<div class="sc-said-text">
        <${Markdown} blocks=${it.blocks} />
        ${it.truncated
          ? html`<p class="sc-dim sc-small">… continues in ${remote ? html`<a href=${remote} target="_blank" rel="noopener">Claude</a>` : "Claude"}</p>`
          : null}
      </div>`;
    case "steps":
      return html`<${Steps} it=${it} live=${live} open=${open} now=${now} />`;
    case "compacted":
      return html`<div class="sc-compacted"><span>COMPACTED${it.at ? " " + clock(it.at) : ""}</span></div>`;
    case "note":
      return html`<p class=${"sc-note " + (it.error ? "sc-err" : "sc-dim")}>${it.error ? it.text : "× " + it.text}</p>`;
    default:
      return null;
  }
}

// Turn is one turn: its prompt's box, then Claude's side. live is set on the
// newest turn while the session is Running: its label says so, its last
// fold starts open, and its pending steps are running.
function Turn({ t, live, now, remote }) {
  const items = t.items || [];
  let lastFold = -1;
  items.forEach((it, i) => {
    if (it.kind === "steps") lastFold = i;
  });
  const last = items.length > 0 ? items[items.length - 1] : null;
  return html`<article class="sc-turn">
    ${t.partial ? html`<p class="sc-dim sc-small">Its start is earlier.</p>` : html`<${Prompt} t=${t} />`}
    ${last
      ? html`<div class="sc-said">
          <div class="sc-said-label">CLAUDE · ${live ? html`<span aria-hidden="true">◐</span> WORKING` : clock(last.at)}</div>
          ${items.map((it, i) => html`<${Item} key=${i} it=${it} live=${live} open=${live && i === lastFold} now=${now} remote=${remote} />`)}
        </div>`
      : null}
  </article>`;
}

export function Turns({ snap, stale, route, act, toast }) {
  const id = route.id;
  const s = snap && id !== undefined ? allSessions(snap).find((x) => x.id === id) : undefined;
  const [held, setHeld] = useState(null);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const heldRef = useRef(null);
  const loading = useRef(false);
  const again = useRef(false);
  const alive = useRef(true);
  // anchor says where the scroll goes after the next page lands: "bottom",
  // or {fromBottom} to keep the position an earlier page pushed down.
  const anchor = useRef(null);
  heldRef.current = held;
  const working = Boolean(s && s.status === "Running");

  // load fetches a page: "newest" (on open), "earlier" (the page before the
  // held turns) or "refresh" (from the newest turn held). One is in flight
  // at a time; a refresh asked for meanwhile runs once it lands.
  async function load(kind) {
    if (id === undefined) return;
    if (loading.current) {
      if (kind === "refresh") again.current = true;
      return;
    }
    const cur = heldRef.current;
    let ask = kind;
    if (ask === "refresh" && (!cur || cur.turns.length === 0)) ask = "newest";
    if (ask === "earlier" && (!cur || !cur.earlier)) return;
    const q = new URLSearchParams();
    if (cur && cur.conversation) q.set("conversation", cur.conversation);
    if (ask === "earlier") q.set("before", String(cur.earlier));
    if (ask === "refresh") q.set("after", String(cur.turns[cur.turns.length - 1].id));
    const doc = document.documentElement;
    const atBottom = window.innerHeight + window.scrollY >= doc.scrollHeight - 48;
    const fromBottom = doc.scrollHeight - window.scrollY;
    loading.current = true;
    let r;
    try {
      r = await call("GET", "/api/v1/sessions/" + id + "/turns" + (q.toString() ? "?" + q.toString() : ""));
    } finally {
      loading.current = false;
    }
    if (!alive.current) return;
    if (r.status !== 200 || !r.data) {
      setErr((r.data && r.data.error) || "Could not read the conversation.");
    } else {
      setErr("");
      const first = !cur || r.data.reset || ask === "newest";
      anchor.current = first ? "bottom" : ask === "earlier" ? { fromBottom } : atBottom ? "bottom" : null;
      setHeld(mergeTurns(cur, r.data, ask));
    }
    if (again.current) {
      again.current = false;
      load("refresh");
    }
  }

  useLayoutEffect(() => {
    const a = anchor.current;
    anchor.current = null;
    if (!a) return;
    const doc = document.documentElement;
    window.scrollTo(0, a === "bottom" ? doc.scrollHeight : doc.scrollHeight - a.fromBottom);
  }, [held]);

  useEffect(() => {
    alive.current = true;
    load("newest");
    return () => {
      alive.current = false;
    };
  }, [id]);

  // The stream changing the session's status or last message refreshes the
  // newest turn; so does the poll while the session is Running.
  const sig = s ? s.status + "\n" + (s.last_message || "") : "";
  useEffect(() => {
    if (heldRef.current) load("refresh");
  }, [sig]);
  useEffect(() => {
    if (!working) return undefined;
    const timer = setInterval(() => load("refresh"), POLL);
    return () => clearInterval(timer);
  }, [working, id]);

  if (!s) {
    return html`
      <section class="sc-screen">
        <nav class="sc-topbar"><a class="sc-back" href="#">‹ fleet</a></nav>
        <p class="sc-dim sc-empty">${snap ? "This session is gone." : "Loading…"}</p>
      </section>
    `;
  }

  const can = actionsFor(s);
  const off = stale || busy;
  const now = Date.now();
  const turns = held ? held.turns : [];

  async function run(action, body) {
    setBusy(true);
    try {
      const r = await act("POST", "/api/v1/sessions/" + s.id + "/" + action, body);
      if (r.status === 202) toast("Still running; the result will show here.", false);
      return r;
    } finally {
      setBusy(false);
    }
  }

  return html`
    <section class="sc-screen sc-turns">
      <nav class="sc-topbar" aria-label="Conversation"><a class="sc-back" href=${"#s/" + s.id}>‹ ${s.title}</a></nav>
      <h1 class="sc-h1">Conversation</h1>
      <${SubLine} s=${s} snap=${snap} now=${now} />
      ${held && held.skipped ? html`<p class="sc-dim sc-small">Some records could not be read.</p>` : null}
      ${held && held.earlier ? html`<button class="sc-earlier" type="button" onClick=${() => load("earlier")}>↑ earlier turns</button>` : null}
      ${!held
        ? html`<div class="sc-empty">
            <p class=${err ? "sc-err" : "sc-dim"}>${err || "Loading…"}</p>
            ${err ? html`<button class="sc-btn" type="button" onClick=${() => load("newest")}>retry</button>` : null}
          </div>`
        : null}
      ${held && turns.length === 0 ? html`<p class="sc-dim sc-empty">No turns yet.</p>` : null}
      <div class="sc-turn-list">
        ${turns.map((t, i) => html`<${Turn} key=${t.id} t=${t} live=${working && i === turns.length - 1} now=${now} remote=${s.remote_url} />`)}
      </div>
      ${held && err
        ? html`<p class="sc-dim sc-small">Could not refresh: ${err} · <button class="sc-inline-btn" type="button" onClick=${() => load("refresh")}>retry</button></p>`
        : null}
      ${can.send
        ? html`<${ReplyBox} snap=${snap} off=${off} run=${run} />`
        : s.group === "paused"
          ? html`<p class="sc-dim sc-small">Resume the session to reply.</p>`
          : null}
    </section>
  `;
}
```

Create `internal/web/static/js/changes.mjs`:

```js
// The changes screen: a session's worktree diff against its base commit,
// file by file (GET /api/v1/sessions/{id}/diff); a file opens to its hunks.
// Every line is drawn as text.
import { html, useState, useEffect, useRef } from "../lib/preact-htm.mjs";
import { call } from "./api.mjs";
import { allSessions } from "./fleet.mjs";
import { SubLine } from "./session.mjs";

// POLL is how often the screen refreshes while the session is Running.
const POLL = 10000;

// HUNK_HEAD splits a hunk header into its @@ ranges and the section after.
const HUNK_HEAD = /^(@@[^@]*@@)(.*)$/;

// lineClass is a diff line's class, by its prefix.
export function lineClass(l) {
  switch (l[0]) {
    case "+":
      return "sc-dl sc-dl-add";
    case "-":
      return "sc-dl sc-dl-del";
    case "\\":
      return "sc-dl sc-dim";
    default:
      return "sc-dl";
  }
}

// splitPath is a path's file name and its directory ("./" at the top).
export function splitPath(p) {
  const i = p.lastIndexOf("/");
  return i < 0 ? [p, "./"] : [p.slice(i + 1), p.slice(0, i + 1)];
}

function HunkView({ h }) {
  const cut = new Set(h.cut || []);
  const m = (h.header || "").match(HUNK_HEAD);
  return html`
    <div class="sc-dl sc-dl-hunk">${m ? html`<span class="sc-accent">${m[1]}</span><span class="sc-dim">${m[2]}</span>` : h.header}</div>
    ${(h.lines || []).map((l, i) => html`<div key=${i} class=${lineClass(l)}>${l}${cut.has(i) ? html`<span class="sc-dim">…</span>` : null}</div>`)}
  `;
}

// Body is an open file: its hunks, or why it has none.
function Body({ f }) {
  let why = "";
  if (f.omitted === "lines") why = "Too long to show here (" + (f.added + f.removed) + " changed lines).";
  else if (f.omitted === "budget") why = "Not shown: the diff is over 1 MiB.";
  else if (f.binary) why = "Binary file.";
  else if (!f.hunks || f.hunks.length === 0) why = "No text changes.";
  if (why) return html`<p class="sc-dim sc-small sc-file-why">${why}</p>`;
  return html`<div class="sc-hunks"><div class="sc-hunks-in">${f.hunks.map((h, i) => html`<${HunkView} key=${i} h=${h} />`)}</div></div>`;
}

function File({ f, open, onToggle }) {
  const [name, dir] = splitPath(f.path);
  const sub = f.old_path && (f.status === "R" || f.status === "C") ? "from " + f.old_path : dir;
  return html`<details class="sc-file" open=${open} onToggle=${(e) => onToggle(f.path, e.currentTarget.open)}>
    <summary class="sc-file-row">
      <span class=${"sc-file-st sc-st-" + f.status}>${f.status}</span>
      <span class="sc-file-name"><span class="sc-file-base">${name}</span><span class="sc-file-dir">${sub}</span></span>
      <span class="sc-file-n">${f.binary
        ? html`<span class="sc-dim">binary</span>`
        : html`${f.added ? html`<span class="sc-add">+${f.added}</span>` : null}${f.added && f.removed ? " " : null}${f.removed ? html`<span class="sc-del">−${f.removed}</span>` : null}`}</span>
    </summary>
    ${open ? html`<${Body} f=${f} />` : null}
  </details>`;
}

export function Changes({ snap, route }) {
  const id = route.id;
  const s = snap && id !== undefined ? allSessions(snap).find((x) => x.id === id) : undefined;
  const [patch, setPatch] = useState(null);
  const [err, setErr] = useState("");
  const [open, setOpen] = useState(() => new Set());
  const loading = useRef(false);
  const again = useRef(false);
  const alive = useRef(true);
  const seeded = useRef(false);
  const working = Boolean(s && s.status === "Running");

  // load fetches the diff; one is in flight at a time, and a load asked for
  // meanwhile runs once it lands. A lone file starts open, on the first
  // diff only, so a refresh never reopens one the user closed.
  async function load() {
    if (id === undefined) return;
    if (loading.current) {
      again.current = true;
      return;
    }
    loading.current = true;
    let r;
    try {
      r = await call("GET", "/api/v1/sessions/" + id + "/diff");
    } finally {
      loading.current = false;
    }
    if (!alive.current) return;
    if (r.status !== 200 || !r.data) {
      setErr((r.data && r.data.error) || "Could not read the diff.");
    } else {
      setErr("");
      setPatch(r.data);
      const files = r.data.files || [];
      if (!seeded.current) {
        seeded.current = true;
        if (files.length === 1) setOpen(new Set([files[0].path]));
      }
    }
    if (again.current) {
      again.current = false;
      load();
    }
  }

  function toggle(path, isOpen) {
    setOpen((o) => {
      if (o.has(path) === isOpen) return o;
      const n = new Set(o);
      if (isOpen) n.add(path);
      else n.delete(path);
      return n;
    });
  }

  useEffect(() => {
    alive.current = true;
    load();
    return () => {
      alive.current = false;
    };
  }, [id]);

  // The stream changing the session's counts or status refreshes the diff;
  // so does the poll while the session is Running.
  const d = (s && s.diff) || {};
  const sig = s ? s.status + ":" + (d.added || 0) + ":" + (d.removed || 0) : "";
  useEffect(() => {
    if (patch) load();
  }, [sig]);
  useEffect(() => {
    if (!working) return undefined;
    const timer = setInterval(load, POLL);
    return () => clearInterval(timer);
  }, [working, id]);

  if (!s) {
    return html`
      <section class="sc-screen">
        <nav class="sc-topbar"><a class="sc-back" href="#">‹ fleet</a></nav>
        <p class="sc-dim sc-empty">${snap ? "This session is gone." : "Loading…"}</p>
      </section>
    `;
  }

  const now = Date.now();
  const files = patch ? patch.files || [] : [];
  const added = files.reduce((n, f) => n + (f.added || 0), 0);
  const removed = files.reduce((n, f) => n + (f.removed || 0), 0);
  const since = patch && patch.base ? patch.base.slice(0, 7) : "";

  return html`
    <section class="sc-screen sc-changes">
      <nav class="sc-topbar" aria-label="Changes"><a class="sc-back" href=${"#s/" + s.id}>‹ ${s.title}</a></nav>
      <h1 class="sc-h1">Changes</h1>
      ${patch
        ? html`<div class="sc-subline">
            <span>${files.length} ${files.length === 1 ? "file" : "files"}</span>
            <span><span class="sc-add">+${added}</span> <span class="sc-del">−${removed}</span></span>
            ${since ? html`<span>since ${since}</span>` : null}
          </div>`
        : html`<${SubLine} s=${s} snap=${snap} now=${now} />`}
      ${!patch
        ? html`<div class="sc-empty">
            <p class=${err ? "sc-err" : "sc-dim"}>${err || "Loading…"}</p>
            ${err ? html`<button class="sc-btn" type="button" onClick=${() => load()}>retry</button>` : null}
          </div>`
        : null}
      ${patch && files.length === 0 ? html`<p class="sc-dim sc-empty">No changes${since ? " since " + since : ""}.</p>` : null}
      ${files.length > 0
        ? html`<div class="sc-files">${files.map((f, i) => html`<${File} key=${i + ":" + f.path} f=${f} open=${open.has(f.path)} onToggle=${toggle} />`)}</div>`
        : null}
      ${patch && err
        ? html`<p class="sc-dim sc-small">Could not refresh: ${err} · <button class="sc-inline-btn" type="button" onClick=${() => load()}>retry</button></p>`
        : null}
    </section>
  `;
}
```

In `internal/web/static/js/screens.mjs`, import `Turns` from `./turns.mjs` and `Changes` from `./changes.mjs`, and add `turns: Turns,` and `changes: Changes,` to `screens`.

In `internal/web/static/js/app.mjs`, replace `parseRoute` and its comment with:

```js
// parseRoute reads the location's hash: "" is the fleet, "#s/<id>" a session,
// "#s/<id>/turns" its conversation and "#s/<id>/diff" its changes, "#new",
// "#settings", "#pair"; anything else is the fleet. The session's id is a
// number, as in the JSON.
export function parseRoute(hash) {
  const h = (hash || "").replace(/^#\/?/, "");
  if (h === "new") return { name: "new" };
  if (h === "settings") return { name: "settings" };
  if (h === "pair") return { name: "pair" };
  const m = h.match(/^s\/(\d+)(?:\/(turns|diff))?$/);
  if (m) return { name: m[2] === "turns" ? "turns" : m[2] === "diff" ? "changes" : "session", id: Number(m[1]) };
  return { name: "fleet" };
}
```

- [ ] **Step 7: The stylesheet**

In `internal/web/static/app.css`:

- add to `:root`, after `--error-border`: `--add: #333a2b;` and `--del: #3d2f2b;`;
- change the rule `.sc-session { … }` (the full-height column) to apply to both screens that end in a reply box, `.sc-session,\n.sc-turns { … }`, and `.sc-session > .sc-h1 { flex: none; }` to `.sc-session > .sc-h1,\n.sc-turns > .sc-h1 { flex: none; }`;
- append:

```css
/* ---- the ways in ---- */
.sc-tile-fig svg { color: var(--accent); }
a.sc-tile { color: var(--fg); }
.sc-rule::after { order: 1; }
.sc-rule-link {
  order: 2;
  display: inline-flex;
  align-items: center;
  min-height: 44px;
  margin: -14px 0;
  font-size: 12px;
  font-weight: 400;
  letter-spacing: 0;
  text-transform: none;
  text-decoration: none;
  color: var(--accent);
}
/* ---- the conversation ---- */
.sc-accent { color: var(--accent); }
.sc-earlier { align-self: center; border: 0; font-size: 12px; color: var(--accent); }
.sc-inline-btn { min-height: 0; padding: 0; border: 0; font-size: inherit; color: var(--accent); text-decoration: underline; }
.sc-turn-list { display: flex; flex-direction: column; gap: 14px; margin-top: 14px; }
.sc-turn { display: flex; flex-direction: column; gap: 6px; }
.sc-prompt { margin-top: 6px; }
.sc-prompt .sc-pre { margin: 0; }
.sc-cmd { color: var(--accent); }
.sc-more { min-height: 44px; margin: 0 -10px -7px; padding: 0 10px; border: 0; font-size: 12px; color: var(--accent); }
.sc-said { display: flex; flex-direction: column; gap: 4px; }
.sc-said-label { font-size: 11px; font-weight: 700; letter-spacing: 0.06em; color: var(--dim); }
.sc-said .md { margin-top: 0; }
.sc-steps { font-size: 12px; }
.sc-steps > summary { display: flex; align-items: center; min-height: 44px; margin: -8px 0; color: var(--dim); cursor: pointer; list-style: none; }
.sc-steps > summary::-webkit-details-marker { display: none; }
.sc-steps > summary::before { content: "▸\00a0"; }
.sc-steps[open] > summary::before { content: "▾\00a0"; }
.sc-step-grid { display: grid; grid-template-columns: 12px auto minmax(0, 1fr) auto; column-gap: 6px; line-height: 20px; }
.sc-step-tool { max-width: 11ch; overflow: hidden; white-space: nowrap; text-overflow: ellipsis; }
.sc-step-sum { overflow: hidden; white-space: nowrap; text-overflow: ellipsis; color: var(--dim); }
.sc-step-end { white-space: nowrap; }
.sc-compacted { display: flex; align-items: center; gap: 8px; font-size: 11px; font-weight: 700; letter-spacing: 0.08em; color: var(--dim); }
.sc-compacted::before,
.sc-compacted::after { content: ""; flex: 1; height: 1px; background: var(--rule); }
.sc-note { margin: 0; font-size: 12px; }
/* ---- the changes ---- */
.sc-files { margin-top: 10px; border-bottom: 1px solid var(--rule); }
.sc-file { border-top: 1px solid var(--rule); }
.sc-file-row { display: grid; grid-template-columns: 14px minmax(0, 1fr) auto; column-gap: 8px; align-items: center; min-height: 44px; padding: 4px 0; cursor: pointer; list-style: none; }
.sc-file-row::-webkit-details-marker { display: none; }
.sc-file-st { font-weight: 700; color: var(--accent); }
.sc-st-A { color: var(--ok); }
.sc-st-D { color: var(--error); }
.sc-st-C { color: var(--dim); }
.sc-file-name { min-width: 0; }
.sc-file-base,
.sc-file-dir { display: block; overflow: hidden; white-space: nowrap; text-overflow: ellipsis; }
.sc-file-base { color: var(--title); }
.sc-file-dir { font-size: 11px; color: var(--dim); }
.sc-file-n { font-size: 12px; white-space: nowrap; }
.sc-file-why { margin: 0 0 10px; }
.sc-hunks { margin: 0 0 10px; overflow-x: auto; border: 1px solid var(--border); border-radius: var(--radius); font-size: 12px; line-height: 17px; }
.sc-hunks-in { display: inline-block; min-width: 100%; padding: 4px 0; }
.sc-dl { min-height: 17px; padding: 0 8px; white-space: pre; tab-size: 4; }
.sc-dl-hunk { background: var(--panel); }
.sc-dl-add { color: var(--ok); background: var(--add); }
.sc-dl-del { color: var(--error); background: var(--del); }
```

Run: `for f in internal/web/static/js/*.mjs; do node --check "$f" || echo "FAIL $f"; done && CGO_ENABLED=0 go test ./internal/web/`
Expected: no `FAIL` line, and PASS (the static tests check every import resolves and is exported, no `style` attribute, the tokens, the braces).

- [ ] **Step 8: See the screens**

Outside the worktree (the scratchpad), serve `internal/web/static/` from a throwaway server that answers `/api/v1/stream` with a canned `snapshot` (a Running Claude session with `claude: true`, `diff: {added: 212, removed: 40}`, quick replies), `GET /api/v1/sessions/<id>/turns` with a page holding: a `/compact` turn with a compacted item, a turn of 14 steps with one failed and a text item, a running turn whose last fold holds a pending Bash step, a peer turn and an interrupted note; and `GET /api/v1/sessions/<id>/diff` with files of each status, a binary one, an omitted one and a hunk with a cut line. Fill `index.html`'s `__LOOM_BUILD__` with the snapshot's `build`. Render `#s/<id>`, `#s/<id>/turns` and `#s/<id>/diff` with headless Chromium at 390×844 (`--screenshot`, `--virtual-time-budget=4000`, `--enable-logging=stderr`), as `docs/claude/web-layer.md` describes, and check each against the design canvas's third row: no console error; the chevron on the changes tile and `conversation ›` on the rule; the turn labels, the folds, `◐ running` only in the newest turn, the compaction rule; the file rows and an open file's hunks scrolling sideways, never the page.

- [ ] **Step 9: The docs**

In `internal/web/README.md`:

- in the Files table, add after the `api.go`, `reqs.go` row: `| \`reads.go\` | the read routes (\`GET …/turns\`, \`GET …/diff\`): \`read\`, which waits for the Reply and stops tracking it (\`reqs.drop\`); \`turnsJSON\` (each text item as its tree) and \`patchJSON\` |`;
- in the API table, after the message row:

```markdown
| `GET /api/v1/sessions/{id}/turns?conversation=&before=&after=` | a page of the session's conversation (`ReadTurns`): `{conversation, turns, earlier, reset, skipped}`. A turn is `{id, at, from, name, text, cut, command, partial, items}`, `from` one of `you`, `plugin`, `peer`, `task`; an item's fields follow its `kind`: `text` has `blocks` (its tree, `markdown.go`) and `truncated`, `steps` has `total`, `failed` and `steps` (`{tool, summary, at, state}`, `state` one of `pending`, `done`, `failed`, `interrupted`), `note` has `text` and `error`, `compacted` only `at`. `before` (the page before, from a page's `earlier`) and `after` (the turns from a turn's `id` to the end) are byte offsets, at most one set; `earlier` is null at the start of the file and on an `after` page; `reset` says the page replaces every turn held |
| `GET /api/v1/sessions/{id}/diff` | the session's worktree diff against its base commit (`ReadDiff`): `{base, files}`, a file `{path, old_path, status, binary, added, removed, hunks, omitted}`, `status` one of `A`, `M`, `D`, `R`, `C`, a hunk `{header, lines, cut}` (each line with its `+`, `-`, space or `\` prefix; `cut` the indexes of the lines cut at 2,000 bytes), `omitted` `lines` or `budget` when a file's hunks were dropped |
```

- in the paragraph after the API table, add 504 to the statuses ("… with 400, 401, 403, 404, 405, 409, 421, 429, 500, 503 or 504") and, after "A refusal by the model is 409 with its text; a stopping daemon is 503.", add: "A read (turns, diff) answers its data; one the model does not answer within `replyWait` is 504, and its request is no longer tracked once answered, since its Reply carries the data and no notice follows it.";
- in the session JSON sentence, add `claude` (the session runs Claude, so the app offers its conversation) after `remote_url`;
- in "The app", after "`#s/<id>` (a session)," add "`#s/<id>/turns` (its conversation, `js/turns.mjs`), `#s/<id>/diff` (its changes, `js/changes.mjs`),", and at the end of its second paragraph: "The conversation screen reads its turns on open and refreshes the newest turn (`after`) when the stream changes the session's status or last message and every 5 s while it is Running; the changes screen refreshes when the session's counts or status change and every 10 s while it is Running. Both session screens end in `js/reply.mjs`'s reply box.";
- in Limits, add: "A conversation page holds at most 10 turns and, past its newest, 256 KiB of text (a prompt 16 KiB, a text item 64 KiB, a fold its newest 200 steps), from at most 16 MiB of the transcript; a diff keeps at most 1 MiB of lines, 5,000 lines a file and 2,000 bytes a line."

In `internal/web/CLAUDE.md`:

- replace the rule that begins "**Draw a session's last message only from its tree" with:

```markdown
- **Draw Claude's text only from its tree (`buildTree` in `internal/web/markdown.go`): a last message (`GET /api/v1/sessions/{id}/message`) and each text item of a conversation (`GET /api/v1/sessions/{id}/turns`, which sends no item's text), as elements; draw prompts, step summaries and diff lines as text; and keep the link check (`safeHref`) in Go.** Claude's text can hold markup; an HTML string put into the page renders it, links and forms included, and the CSP stops only its scripts. **Enforced** for the tree by `TestBuildTree_KeepsNoMarkupAndFollowsNoUnsafeLink`, and for the turns route sending trees by `TestAPI_TurnsIsAPageOfTheConversation`; that no module puts an HTML string into the page is **Convention** — a message that rewrites the phone's screen.
```

- add, after the rule on refused actions:

```markdown
- **Answer a read route (a `Reply` that carries data: the conversation, the diff) through `l.read`, which stops tracking its request once its handler is done (`reqs.drop`) and answers 504 when `replyWait` runs out.** A tracked request keeps its `Reply` for `reqKeep` (up to `maxReqs`), so a phone polling a diff of up to 1 MiB would hold hundreds of them, and one arriving after the wait gave up would reach the stream as a notice. **Enforced** by `TestAPI_AReadIsNotTrackedOnceAnsweredAndATimedOutOneIs504`.
```

In `docs/claude/web-layer.md`'s "Adding an API route", append to step 2: "A read, whose `Reply` carries data, goes through `l.read` (`internal/web/reads.go`): 200 with the data, the model's refusal as an action's, 504 when `replyWait` runs out, and the request dropped once answered (`reqs.drop`)."

In `USAGE.md`'s "Use Loom from Your Phone", after the bullet that begins "**The fleet.**", add:

```markdown
- **The conversation and the changes.** On a Claude session, **conversation ›** beside its last message opens its recent turns: each prompt (yours, a background task's, or a subagent's report), Claude's replies as it formatted them, and its tool calls as one-line steps folded under a count (tap to open; the newest turn's run is open while Claude works, its running step marked). **↑ earlier turns** loads more. The **Changes** tile opens the worktree's diff against its base commit, file by file: tap a file for its hunks. Both refresh while the session works; neither shows tool output, and the changes are offered only while the session runs.
```

Run: `CGO_ENABLED=0 go run ./tools/claudemd -v 2>&1 | grep -E 'internal/web/CLAUDE|session/CLAUDE|session/git/CLAUDE'`
Expected: every file within its budget.

- [ ] **Step 10: Run every gate**

Run, in order:

```bash
CGO_ENABLED=0 go test ./...
CC=clang CGO_ENABLED=1 go test -race ./session/... ./core/... ./internal/web/...
gofmt -l $(git ls-files '*.go' | grep -v '^vendor/') session/transcript session/git/patch.go session/git/patch_test.go session/patch.go session/patch_test.go core/reads.go core/reads_test.go internal/web/reads.go internal/web/reads_test.go
CGO_ENABLED=0 go vet ./...
CGO_ENABLED=0 go run ./tools/claudemd
for f in internal/web/static/js/*.mjs; do node --check "$f" || echo "FAIL $f"; done
GOOS=windows CGO_ENABLED=0 go build ./...
```

Expected: every test passes, `gofmt -l` prints nothing, vet and the CLAUDE.md lint are clean, no `FAIL` line, and the Windows build succeeds. golangci-lint is skipped (the root `CLAUDE.md`: the dev shell's v2 rejects the repo's config); say so in the report.

- [ ] **Step 11: Commit**

```bash
git add -- internal/web/reqs.go internal/web/api.go internal/web/fleet.go internal/web/reads.go internal/web/reads_test.go internal/web/static_test.go internal/web/static/js/reply.mjs internal/web/static/js/turns.mjs internal/web/static/js/changes.mjs internal/web/static/js/session.mjs internal/web/static/js/app.mjs internal/web/static/js/screens.mjs internal/web/static/js/skin.mjs internal/web/static/app.css internal/web/README.md internal/web/CLAUDE.md docs/claude/web-layer.md USAGE.md
git commit -m "feat(web): the conversation and the changes on the phone

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_019icsHbrFq5LeZ4b4jFkSBe" -- internal/web/reqs.go internal/web/api.go internal/web/fleet.go internal/web/reads.go internal/web/reads_test.go internal/web/static_test.go internal/web/static/js/reply.mjs internal/web/static/js/turns.mjs internal/web/static/js/changes.mjs internal/web/static/js/session.mjs internal/web/static/js/app.mjs internal/web/static/js/screens.mjs internal/web/static/js/skin.mjs internal/web/static/app.css internal/web/README.md internal/web/CLAUDE.md docs/claude/web-layer.md USAGE.md
```

- [ ] **Step 12: The phone checklist (the owner's)**

Through the `loom-dev` skill (never `./loom` or `loom serve stop` in a loom pane), on the iPhone: open a working session's conversation and watch a step finish and the turn's label change when it ends; load earlier turns; send a reply from the conversation screen; `/clear` the session and see the screen start over; open the changes of a session with an untracked file and open a file; open a paused session (its conversation reads, its changes tile has no link). The opt-in real-Claude tests (`LOOM_TEST_REAL_CLAUDE=1`: `TestRealClaude_TranscriptReader`, `TestRealClaude_TheModCarriesAMessage`) spend a few cents and are the owner's to run.
