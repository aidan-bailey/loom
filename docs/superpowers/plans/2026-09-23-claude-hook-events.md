# Claude Hook Events Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Drive Claude sessions' status from Claude Code's own hook events and the agent roster (newest observation wins), resume crashed sessions with `--resume <session_id>`, and show Claude's last message on stopped sessions' cards.

**Architecture:** Hook transport (folder, settings, scan, event parsing) moves out of `session/subagent` into a new `session/hooks` package that both the subagent tracker and a new per-instance `claudeState` consume. `claudeState` folds hook events and roster answers into one timestamped observation; the app applies it through a single choke point (`adoptClaudeStatus`), triggered by pane output, pane quiet and roster results. `pollGate` gains trailing requests so an event never waits for the 3s health tick.

**Tech Stack:** Go 1.25, Bubble Tea v2, testify. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-09-23-claude-hook-events-design.md` (read it first; its Findings and Probe results sections explain every rule below).

---

## Conventions for every task

- You are in a loom-managed git worktree on branch `aidanb/claudetheist`. Never switch branches, create branches or rebase. Commit at the end of every task.
- Build and test with CGO off: `CGO_ENABLED=0 go test ./session/...`. The race detector needs `CC=clang CGO_ENABLED=1 go test -race ./...`.
- Format only tracked non-vendor files: `gofmt -w $(git ls-files '*.go' | grep -v '^vendor/')`. Never `gofmt -w .` (it rewrites `vendor/`).
- Lint with `CGO_ENABLED=0 go vet ./...`. The local golangci-lint is v2 and does not accept the repo's v1 config, so do not run it.
- End every commit message with these two lines:

```
Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_017Dc9sKTFBhQzMRk4jUoj4p
```

- zsh expands a leading `=word` into a command path. Quote tmux targets such as `'=name'` in any shell command you type.

## File structure

| File | Responsibility |
|---|---|
| `session/hooks/` (new package, moved from `session/subagent`) | `event.go` parses hook payloads into `Event`; `hooks.go` writes the per-launch folder and settings; `scan.go` collects events and stamps `At` |
| `session/hooks/testdata/probe-2.1.280/` (exists) | 34 real payloads from the 2026-09-23 probe, used as fixtures |
| `session/subagent/readmeta.go` (new) | `ReadMeta`: reads subagent sidecars for scanned events (was `scan.go`'s `readMeta`) |
| `session/hook_scan.go` (new) | `HookScanRequest`/`HookScanResult`/`ScanHooks`: one instance's scan, hooks plus sidecars |
| `session/claude_state.go` (new) | `claudeState`: observation merge rule, event mapping, and the `Instance` accessors |
| `session/subagent_hooks.go` | Launch-time hook preparation and scan application (renamed methods, status wiring) |
| `session/agent_restart.go`, `session/agent/*.go` | `BuildResumeCommand` and the adapter's `ApplyResumeFlag` |
| `session/storage.go`, `session/storage_migrate.go`, `session/instance.go`, `cmd/workspace_migrate.go` | Schema v7: persisted session ID and transcript path |
| `app/pollgate.go` | `request()` and trailing re-dispatch |
| `app/hook_scan.go` (renamed from `app/subagents.go`) | Hook scan dispatch and result handling |
| `app/events.go`, `app/app.go` | Roster observations, `adoptClaudeStatus`/`applyClaudeStatus`, dirty and quiet triggers |
| `ui/card.go` | `MessageTailLines`; cards show the last message when stopped |
| `tools/fakeagent/` | Fake claude persona fires loom's hooks; answers the roster query |
| `e2e/e2e_test.go` | Hook-driven status end to end |
| `session/claude_hooks_realclaude_test.go` (new) | Opt-in real-Claude contract test for the probe results |

Task order matters: each task leaves the tree building and every test passing.

---

### Task 1: Extract `session/hooks` (no behaviour change)

Moves the hook transport into its own package and renames the scan API that will soon carry more than subagents. Nothing behaves differently after this task.

**Files:**
- Move: `session/subagent/{event.go,event_test.go,hooks.go,hooks_test.go,scan.go,scan_test.go}` → `session/hooks/`
- Move: `session/subagent/testdata/{session_end,stop_teammate_running,subagent_start,subagent_stop,teammate_idle}.json` → `session/hooks/testdata/`
- Create: `session/subagent/readmeta.go`, `session/hook_scan.go`
- Modify: `session/subagent/{tracker.go,tracker_test.go,meta_test.go,realclaude_test.go}`
- Modify: `session/subagent_hooks.go`, `session/subagent_hooks_test.go`, `session/instance_lifecycle_test.go`
- Move + modify: `app/subagents.go` → `app/hook_scan.go`, `app/subagents_test.go` → `app/hook_scan_test.go`
- Modify: `app/app.go`, `app/pollgate.go`, `app/pollgate_test.go`, `ui/card_agents_test.go`

- [ ] **Step 1: Move the transport files and their fixtures**

```bash
mkdir -p session/hooks/testdata
for f in event.go event_test.go hooks.go hooks_test.go scan.go scan_test.go; do
  git mv session/subagent/$f session/hooks/$f
done
for f in session_end stop_teammate_running subagent_start subagent_stop teammate_idle; do
  git mv session/subagent/testdata/$f.json session/hooks/testdata/$f.json
done
git mv app/subagents.go app/hook_scan.go
git mv app/subagents_test.go app/hook_scan_test.go
sed -i 's/^package subagent$/package hooks/' session/hooks/*.go
sed -i 's/"subagent: /"hooks: /g; s/"subagent\.scan\./"hooks.scan./g' session/hooks/*.go
```

- [ ] **Step 2: Replace the package doc at the top of `session/hooks/event.go`**

Replace:

```go
// Package subagent tracks the subagents and agent-team teammates a Claude
// session has spawned, from the Claude Code hook events loom registers at
// launch. See docs/superpowers/specs/2026-09-16-subagent-nesting-design.md.
//
// It has no dependency on tmux, the UI or the app, so every piece can be
// tested against payloads captured from a real Claude session.
package hooks
```

with:

```go
// Package hooks is loom's side of the Claude Code hooks it registers at
// launch: the per-instance folder layout, the settings file and hook
// command, and the scan that turns the files the hooks write into Events.
// It does not interpret them: session/subagent tracks subagents from them,
// and session derives the session's status, ID and last message. See
// docs/superpowers/specs/2026-09-16-subagent-nesting-design.md and
// docs/superpowers/specs/2026-09-23-claude-hook-events-design.md.
//
// It has no dependency on tmux, the UI or the app, so every piece can be
// tested against payloads captured from a real Claude session.
package hooks
```

- [ ] **Step 3: Drop the sidecar read from `session/hooks/scan.go`**

Replace the `Request` and `Result` types:

```go
// Request describes one instance's scan.
type Request struct {
	// Dir is the instance's hooks folder.
	Dir string
	// Cold replays the retained .ev log. True for the first scan after a
	// launch or a loom restart.
	Cold bool
	// MissingMeta lists agents still waiting for their sidecar.
	MissingMeta []MetaRef
}

// Result is what one scan found.
type Result struct {
	LaunchID string
	Events   []Event
	Meta     map[string]Meta
	// Replayed is true when Events is the full history (Request.Cold).
	Replayed bool
}
```

with:

```go
// Request describes one instance's scan.
type Request struct {
	// Dir is the instance's hooks folder.
	Dir string
	// Cold replays the retained .ev log. True for the first scan after a
	// launch or a loom restart.
	Cold bool
}

// Result is what one scan found.
type Result struct {
	LaunchID string
	Events   []Event
	// Replayed is true when Events is the full history (Request.Cold).
	Replayed bool
}
```

In the doc comment above `func Scan`, replace:

```go
// returned ordered by modification time, then file stem, together with
// the metadata sidecars for new SubagentStart events and req.MissingMeta.
```

with:

```go
// returned ordered by modification time, then file stem.
```

Replace the `return` at the end of `Scan`:

```go
	return Result{
		LaunchID: launchID,
		Events:   events,
		Meta:     readMeta(events, req.MissingMeta),
		Replayed: req.Cold,
	}, nil
```

with:

```go
	return Result{
		LaunchID: launchID,
		Events:   events,
		Replayed: req.Cold,
	}, nil
```

Delete the whole `readMeta` function and its doc comment (the last function in the file, starting `// readMeta reads the sidecars for SubagentStart events`).

- [ ] **Step 4: Move the sidecar test out of `session/hooks/scan_test.go`**

Delete the function `TestScan_ReadsMetaForStartsAndMissing` from `session/hooks/scan_test.go`.

- [ ] **Step 5: Create `session/subagent/readmeta.go`**

```go
package subagent

import (
	"os"
	"slices"

	"github.com/aidan-bailey/loom/session/hooks"
)

// ReadMeta reads the metadata sidecars for the SubagentStart events in
// events and for the agents still missing one. An unreadable sidecar is
// skipped; the tracker keeps the agent hidden and the next scan retries
// it. It only touches the filesystem, so it is safe to run from a tea.Cmd.
func ReadMeta(events []hooks.Event, missing []MetaRef) map[string]Meta {
	refs := slices.Clone(missing)
	for _, ev := range events {
		if ev.Name != hooks.EventSubagentStart {
			continue
		}
		if p := MetaPath(ev.TranscriptPath, ev.AgentID); p != "" {
			refs = append(refs, MetaRef{AgentID: ev.AgentID, Path: p})
		}
	}
	meta := map[string]Meta{}
	for _, r := range refs {
		if _, done := meta[r.AgentID]; done {
			continue
		}
		data, err := os.ReadFile(r.Path)
		if err != nil {
			continue
		}
		m, err := ParseMeta(data)
		if err != nil {
			continue
		}
		meta[r.AgentID] = m
	}
	return meta
}
```

- [ ] **Step 6: Give `session/subagent/meta_test.go` its own fixture helper and the moved sidecar test**

Replace its import block with:

```go
import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/session/hooks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return data
}
```

Append:

```go
func TestReadMeta_StartsAndMissing(t *testing.T) {
	root := t.TempDir()
	transcriptPath := filepath.Join(root, "sess.jsonl")
	subagents := filepath.Join(root, "sess", "subagents")
	require.NoError(t, os.MkdirAll(subagents, 0o700))
	writeMeta := func(id, desc string) {
		require.NoError(t, os.WriteFile(filepath.Join(subagents, "agent-"+id+".meta.json"),
			[]byte(fmt.Sprintf(`{"agentType":"Explore","description":%q}`, desc)), 0o600))
	}
	writeMeta("a1", "from start")
	writeMeta("a2", "from retry")

	events := []hooks.Event{{Name: hooks.EventSubagentStart, AgentID: "a1", AgentType: "Explore", TranscriptPath: transcriptPath}}
	got := ReadMeta(events, []MetaRef{
		{AgentID: "a2", Path: filepath.Join(subagents, "agent-a2.meta.json")},
		{AgentID: "a3", Path: filepath.Join(subagents, "agent-a3.meta.json")},
	})

	assert.Equal(t, map[string]Meta{
		"a1": {AgentType: "Explore", Description: "from start"},
		"a2": {AgentType: "Explore", Description: "from retry"},
	}, got)
}
```

- [ ] **Step 7: Point the tracker at `hooks.Event`**

Qualify the moved types everywhere they appear unqualified in the three files that stay (no comment in these files mentions them, so the rewrite is safe):

```bash
perl -pi -e 's/(?<![\w.])(Event(?:SubagentStart|SubagentStop|TeammateIdle|Stop|SessionEnd)?|Task)\b/hooks.$1/g' \
  session/subagent/tracker.go session/subagent/tracker_test.go session/subagent/realclaude_test.go
```

Replace the head of `session/subagent/tracker.go` (from `package subagent` through the closing `)` of its import block) with:

```go
// Package subagent tracks the subagents and agent-team teammates a Claude
// session has spawned, from the hook events session/hooks collects. See
// docs/superpowers/specs/2026-09-16-subagent-nesting-design.md.
//
// It has no dependency on tmux, the UI or the app, so every piece can be
// tested against payloads captured from a real Claude session.
package subagent

import (
	"cmp"
	"slices"

	"github.com/aidan-bailey/loom/session/hooks"
)
```

In `session/subagent/tracker_test.go`, replace the import block with:

```go
import (
	"testing"

	"github.com/aidan-bailey/loom/session/hooks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)
```

- [ ] **Step 8: Update `session/subagent/realclaude_test.go`**

Its local variable `hooks` would shadow the package. Replace:

```go
	root := t.TempDir()
	hooks := filepath.Join(root, "hooks")
	launchID, err := Prepare(hooks)
```

with:

```go
	root := t.TempDir()
	hooksDir := filepath.Join(root, "hooks")
	launchID, err := hooks.Prepare(hooksDir)
```

Replace `SettingsPath(hooks)` with `hooks.SettingsPath(hooksDir)`.

Replace:

```go
		res, err := Scan(Request{Dir: hooks, Cold: cold, MissingMeta: tracker.MissingMeta()}, time.Now())
```

with:

```go
		res, err := hooks.Scan(hooks.Request{Dir: hooksDir, Cold: cold}, time.Now())
```

and replace `tracker.Apply(res.Events, res.Meta)` with `tracker.Apply(res.Events, ReadMeta(res.Events, tracker.MissingMeta()))`.

Add `"github.com/aidan-bailey/loom/session/hooks"` to its import block.

- [ ] **Step 9: Create `session/hook_scan.go`**

```go
package session

import (
	"time"

	"github.com/aidan-bailey/loom/session/hooks"
	"github.com/aidan-bailey/loom/session/subagent"
)

// HookScanRequest describes one instance's hook scan: the folder to scan,
// whether to replay its history, and the subagents still waiting for a
// metadata sidecar. Build it with Instance.NextHookScan on the Update
// goroutine; ScanHooks runs it off that goroutine.
type HookScanRequest struct {
	Dir         string
	Cold        bool
	MissingMeta []subagent.MetaRef
}

// HookScanResult is one instance's scan: the events found plus the
// sidecars read for them. Apply it with Instance.ApplyHookScan.
type HookScanResult struct {
	LaunchID string
	Events   []hooks.Event
	Meta     map[string]subagent.Meta
	Replayed bool
}

// ScanHooks runs one instance's scan: hooks.Scan, then the subagent
// sidecars for its SubagentStart events and req.MissingMeta. It only
// touches the filesystem, so it is safe to run from a tea.Cmd.
func ScanHooks(req HookScanRequest, now time.Time) (HookScanResult, error) {
	res, err := hooks.Scan(hooks.Request{Dir: req.Dir, Cold: req.Cold}, now)
	if err != nil {
		return HookScanResult{}, err
	}
	return HookScanResult{
		LaunchID: res.LaunchID,
		Events:   res.Events,
		Meta:     subagent.ReadMeta(res.Events, req.MissingMeta),
		Replayed: res.Replayed,
	}, nil
}
```

- [ ] **Step 10: Rename the session scan methods and update their callers**

```bash
perl -pi -e 's/\bApplySubagentScan\b/ApplyHookScan/g; s/\bSubagentScanRequest\b/NextHookScan/g; s/\bresetSubagentLaunch\b/resetHookLaunch/g; s/\bprepareSubagentHooks\b/prepareHooks/g' \
  $(git grep -lE 'ApplySubagentScan|SubagentScanRequest|resetSubagentLaunch|prepareSubagentHooks' -- '*.go')
perl -pi -e 's/\bsubagent\.(SafePath|Prepare|SettingsPath|ErrNoHooks)\b/hooks.$1/g; s/\bsubagent\.Request\b/HookScanRequest/g; s/\bsubagent\.Result\b/HookScanResult/g' \
  session/subagent_hooks.go
perl -pi -e 's/\bsubagent\.Result\{/HookScanResult{/g; s/\bsubagent\.Scan\(/ScanHooks(/g; s/\bsubagent\.(Event\w*|Prepare|SettingsPath|EventsDir|ErrNoHooks)\b/hooks.$1/g' \
  session/subagent_hooks_test.go session/instance_lifecycle_test.go
```

Add `"github.com/aidan-bailey/loom/session/hooks"` to the import blocks of `session/subagent_hooks.go`, `session/subagent_hooks_test.go` and `session/instance_lifecycle_test.go`. Each still uses `subagent` (the tracker, `View`, `Meta`, `MetaRef`), so keep that import.

- [ ] **Step 11: Rename the app scan identifiers and update the app and UI callers**

```bash
perl -pi -e 's/\bgateSubagent\b/gateHookScan/g; s/\bsubagentInterval\b/hookScanInterval/g; s/\bmaybeSubagentScan\b/maybeHookScan/g; s/\bsubagentScanCmd\b/hookScanCmd/g; s/\bsubagentScanMsg\b/hookScanMsg/g; s/\bsubagentScanResult\b/hookScanResult/g; s/\bhandleSubagentScan\b/handleHookScan/g' app/*.go
perl -pi -e 's/\bsubagent\.Result\b/session.HookScanResult/g; s/\bsubagent\.Request\b/session.HookScanRequest/g; s/\bsubagent\.Scan\(/session.ScanHooks(/g; s/\bsubagent\.(Event\w*|Prepare|EventsDir|ErrNoHooks)\b/hooks.$1/g' \
  app/hook_scan.go app/hook_scan_test.go ui/card_agents_test.go
```

Import changes:
- `app/hook_scan.go`: replace `"github.com/aidan-bailey/loom/session/subagent"` with `"github.com/aidan-bailey/loom/session/hooks"`.
- `app/hook_scan_test.go` and `ui/card_agents_test.go`: add `"github.com/aidan-bailey/loom/session/hooks"` and keep `subagent` (`View`, `Meta`).

In `app/pollgate.go`, in `func (k gateKind) String()`, replace:

```go
	case gateHookScan:
		return "subagent"
```

with:

```go
	case gateHookScan:
		return "hook_scan"
```

- [ ] **Step 12: Build, vet and look for leftovers**

Run:

```bash
CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go vet ./session/... ./app/... ./ui/... ./tools/...
git grep -nE 'subagent\.(Scan|Request|Result|Event|Prepare|SettingsPath|EventsDir|ErrNoHooks|SafePath|HookEvents|ParseEvent|Task)\b' -- '*.go'
```

Expected: build and vet print nothing; the `git grep` prints nothing. If vet reports an unused or missing import, fix that import block and re-run.

- [ ] **Step 13: Run the affected suites**

Run: `CGO_ENABLED=0 go test ./session/... ./app/... ./ui/... ./tools/...`
Expected: every package `ok`.

- [ ] **Step 14: Format and commit**

```bash
gofmt -w $(git ls-files '*.go' | grep -v '^vendor/')
git add -A session app ui
git commit -m "refactor(session): move the hook transport into session/hooks

The folder layout, settings, scan and event parsing now live in their
own package, so the subagent tracker and the coming status tracking
share one event path. ReadMeta stays in session/subagent, since the
sidecars are a subagent concept; session.ScanHooks runs both. Scan
methods and app identifiers are renamed from subagent to hook.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_017Dc9sKTFBhQzMRk4jUoj4p"
```

---

### Task 2: Status, resume and message fields on hook events

Registers the four new events and parses the fields loom will read. Each event keeps only its own fields, so compact files stay small and a subagent's reply is never mistaken for the parent's.

**Files:**
- Modify: `session/hooks/event.go`
- Test: `session/hooks/event_test.go`, `session/hooks/hooks_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `session/hooks/event_test.go`, and add `"strings"` and `"unicode/utf8"` to its imports:

```go
// probeFixture reads one payload from the 2026-09-23 probe by its
// <unix-nanos> file-name prefix.
func probeFixture(t *testing.T, stem string) []byte {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join("testdata", "probe-2.1.280", stem+"-*.json"))
	require.NoError(t, err)
	require.Len(t, matches, 1, "fixture %s", stem)
	data, err := os.ReadFile(matches[0])
	require.NoError(t, err)
	return data
}

func TestParseEvent_ProbeSessionStart(t *testing.T) {
	ev, err := ParseEvent(probeFixture(t, "1790179911178279565"))
	require.NoError(t, err)
	assert.Equal(t, EventSessionStart, ev.Name)
	assert.Equal(t, "8c634184-0fe5-4b62-b437-8f364eeeefcc", ev.SessionID)
	assert.Equal(t, "startup", ev.Source)
	assert.Equal(t, "/home/user/.claude/projects/-probe-work/8c634184-0fe5-4b62-b437-8f364eeeefcc.jsonl", ev.TranscriptPath)
	assert.Empty(t, ev.AgentID)

	cleared, err := ParseEvent(probeFixture(t, "1790180077137029376"))
	require.NoError(t, err)
	assert.Equal(t, "clear", cleared.Source)
	assert.Equal(t, "487f460e-49ff-4961-a883-6b2c290c9aec", cleared.SessionID)
}

func TestParseEvent_ProbePermissionRequest(t *testing.T) {
	parent, err := ParseEvent(probeFixture(t, "1790179972989150631"))
	require.NoError(t, err)
	assert.Equal(t, EventPermissionRequest, parent.Name)
	assert.Equal(t, "Bash", parent.ToolName)
	assert.Empty(t, parent.AgentID)

	sub, err := ParseEvent(probeFixture(t, "1790180017571376478"))
	require.NoError(t, err)
	assert.Equal(t, "Bash", sub.ToolName)
	assert.Equal(t, "a4775447930305717", sub.AgentID)
}

func TestParseEvent_ProbeNotification(t *testing.T) {
	ev, err := ParseEvent(probeFixture(t, "1790179978985750130"))
	require.NoError(t, err)
	assert.Equal(t, EventNotification, ev.Name)
	assert.Equal(t, "permission_prompt", ev.NotificationType)
	assert.Equal(t, "Claude needs your permission", ev.Message)
}

func TestParseEvent_ProbeStops(t *testing.T) {
	done, err := ParseEvent(probeFixture(t, "1790179927419880486"))
	require.NoError(t, err)
	assert.Equal(t, "PONG", done.LastAssistantMessage)
	require.True(t, done.HasTasks)
	assert.Empty(t, done.Tasks)

	mid, err := ParseEvent(probeFixture(t, "1790180017490508216"))
	require.NoError(t, err)
	assert.Equal(t, "Agent launched. Waiting for completion...", mid.LastAssistantMessage)
	assert.Equal(t, []Task{{ID: "a4775447930305717", Type: "subagent", Status: "running"}}, mid.Tasks)
}

// Each event keeps only the fields loom reads from it: a SubagentStop's
// last_assistant_message is the subagent's reply, not the parent's.
func TestParseEvent_KeepsFieldsOnlyForTheirEvent(t *testing.T) {
	ev, err := ParseEvent(fixture(t, "subagent_stop.json"))
	require.NoError(t, err)
	assert.Empty(t, ev.LastAssistantMessage)
	assert.Empty(t, ev.SessionID)

	ev, err = ParseEvent([]byte(`{"hook_event_name":"Stop","session_id":"s","source":"x","tool_name":"Bash","message":"m","notification_type":"n"}`))
	require.NoError(t, err)
	assert.Equal(t, Event{Name: EventStop}, ev)
}

func TestParseEvent_CapsMessagesKeepingTheEnd(t *testing.T) {
	long := strings.Repeat("é", MaxMessageBytes) + "Should I push?"
	ev, err := ParseEvent([]byte(`{"hook_event_name":"Stop","last_assistant_message":"` + long + `"}`))
	require.NoError(t, err)
	assert.LessOrEqual(t, len(ev.LastAssistantMessage), MaxMessageBytes)
	assert.True(t, utf8.ValidString(ev.LastAssistantMessage), "the cap never splits a character")
	assert.True(t, strings.HasSuffix(ev.LastAssistantMessage, "Should I push?"),
		"cards show a message's last lines, where Claude puts its summary or question")
}

func TestEventCompact_ProbeFixturesRoundTrip(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "probe-2.1.280", "*.json"))
	require.NoError(t, err)
	require.Len(t, files, 34)
	for _, f := range files {
		raw, err := os.ReadFile(f)
		require.NoError(t, err)
		ev, err := ParseEvent(raw)
		require.NoError(t, err, f)
		compact, err := ev.Compact()
		require.NoError(t, err)
		again, err := ParseEvent(compact)
		require.NoError(t, err)
		assert.Equal(t, ev, again, f)
	}
}
```

In `TestEventCompact_RoundTrips`, a `Stop` fixture now keeps its message. Replace:

```go
			assert.NotContains(t, string(compact), "last_assistant_message")
```

with:

```go
			if ev.Name != EventStop {
				assert.NotContains(t, string(compact), "last_assistant_message")
			}
```

In `session/hooks/hooks_test.go`'s `TestSettingsJSON_Golden` (JSON encodes map keys sorted), replace:

```go
			entry("SessionEnd"), entry("Stop"), entry("SubagentStart"),
			entry("SubagentStop"), entry("TeammateIdle"),
```

with:

```go
			entry("Notification"), entry("PermissionRequest"), entry("SessionEnd"),
			entry("SessionStart"), entry("Stop"), entry("SubagentStart"),
			entry("SubagentStop"), entry("TeammateIdle"), entry("UserPromptSubmit"),
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `CGO_ENABLED=0 go test ./session/hooks/`
Expected: compile errors: `undefined: EventSessionStart`, `ev.SessionID undefined`, `undefined: MaxMessageBytes`.

- [ ] **Step 3: Implement the fields in `session/hooks/event.go`**

Replace the event-name constants and `HookEvents` with:

```go
// Hook event names loom registers.
const (
	EventSubagentStart     = "SubagentStart"
	EventSubagentStop      = "SubagentStop"
	EventTeammateIdle      = "TeammateIdle"
	EventStop              = "Stop"
	EventSessionEnd        = "SessionEnd"
	EventSessionStart      = "SessionStart"
	EventUserPromptSubmit  = "UserPromptSubmit"
	EventPermissionRequest = "PermissionRequest"
	EventNotification      = "Notification"
)

// HookEvents lists the registered events.
var HookEvents = []string{
	EventSubagentStart, EventSubagentStop, EventTeammateIdle, EventStop, EventSessionEnd,
	EventSessionStart, EventUserPromptSubmit, EventPermissionRequest, EventNotification,
}

// MaxMessageBytes caps LastAssistantMessage and Message. Payloads can be
// up to maxEventBytes, and the text is kept in memory and in every .ev
// file; a card only shows a message's last few lines.
const MaxMessageBytes = 4 << 10
```

Add these fields at the end of the `Event` struct (after `HasTasks bool`):

```go
	// SessionID and Source are kept for SessionStart only: the ID names
	// the conversation a relaunch resumes, and Source says why the
	// session started (startup, resume, clear, compact).
	SessionID string
	Source    string
	// ToolName is kept for PermissionRequest only.
	ToolName string
	// NotificationType and Message are kept for Notification only.
	NotificationType string
	Message          string
	// LastAssistantMessage is kept for Stop only.
	LastAssistantMessage string
```

Add these fields at the end of the `wireEvent` struct:

```go
	SessionID            string `json:"session_id,omitempty"`
	Source               string `json:"source,omitempty"`
	ToolName             string `json:"tool_name,omitempty"`
	NotificationType     string `json:"notification_type,omitempty"`
	Message              string `json:"message,omitempty"`
	LastAssistantMessage string `json:"last_assistant_message,omitempty"`
```

In `ParseEvent`, directly after the `ev := Event{...}` literal, add:

```go
	switch w.Name {
	case EventSessionStart:
		ev.SessionID, ev.Source = w.SessionID, w.Source
	case EventPermissionRequest:
		ev.ToolName = w.ToolName
	case EventNotification:
		ev.NotificationType, ev.Message = w.NotificationType, capText(w.Message)
	case EventStop:
		ev.LastAssistantMessage = capText(w.LastAssistantMessage)
	}
```

In `Compact`, extend the `w := wireEvent{...}` literal with:

```go
		SessionID:            e.SessionID,
		Source:               e.Source,
		ToolName:             e.ToolName,
		NotificationType:     e.NotificationType,
		Message:              e.Message,
		LastAssistantMessage: e.LastAssistantMessage,
```

Add at the end of the file, and add `"unicode/utf8"` to the imports:

```go
// capText keeps the last MaxMessageBytes of s, starting on a character
// boundary: cards show a message's last lines, where Claude puts its
// summary or question.
func capText(s string) string {
	if len(s) <= MaxMessageBytes {
		return s
	}
	s = s[len(s)-MaxMessageBytes:]
	for len(s) > 0 && !utf8.RuneStart(s[0]) {
		s = s[1:]
	}
	return s
}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `CGO_ENABLED=0 go test ./session/hooks/ ./session/subagent/`
Expected: `ok` for both.

- [ ] **Step 5: Commit**

```bash
gofmt -w $(git ls-files '*.go' | grep -v '^vendor/')
git add session/hooks
git commit -m "feat(hooks): register status events and parse their fields

SessionStart (session ID, source), UserPromptSubmit, PermissionRequest
(tool name) and Notification (type, message) join the registered hooks,
and Stop now keeps last_assistant_message. Each event keeps only its own
fields, and messages are capped at 4 KB keeping the end, which is what
a card shows.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_017Dc9sKTFBhQzMRk4jUoj4p"
```

---

### Task 3: Stamp each event with when its hook ran

`At` is the hook's own timestamp, from the `<unix-nanos>` file-name prefix, falling back to the file's modification time on macOS, where `date +%s%N` prints a literal `N`. The merge rule in Task 4 compares it with roster query times.

**Files:**
- Modify: `session/hooks/event.go`, `session/hooks/scan.go`
- Test: `session/hooks/scan_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `session/hooks/scan_test.go`:

```go
func TestEventTime(t *testing.T) {
	mod := base
	assert.True(t, time.Unix(0, 1790179911178279565).Equal(eventTime("1790179911178279565-2237570", mod)))
	assert.True(t, mod.Equal(eventTime("1790179911N-2237570", mod)), "macOS date prints a literal N")
	assert.True(t, mod.Equal(eventTime("1790179911-2237570", mod)), "a seconds prefix is not nanoseconds")
	assert.True(t, mod.Equal(eventTime("a", mod)))
}

func TestScan_StampsEventsFromTheirNames(t *testing.T) {
	dir, _ := prepared(t)
	// The modification times disagree with the names on purpose: the name wins.
	writeRaw(t, dir, "1790179911000000002-1", startPayload("second"), base)
	writeRaw(t, dir, "1790179911000000001-1", startPayload("first"), base.Add(time.Second))

	res, err := Scan(Request{Dir: dir}, base)
	require.NoError(t, err)
	assert.Equal(t, []string{"first", "second"}, ids(res.Events))
	assert.True(t, res.Events[0].At.Equal(time.Unix(0, 1790179911000000001)))

	cold, err := Scan(Request{Dir: dir, Cold: true}, base)
	require.NoError(t, err)
	require.Len(t, cold.Events, 2)
	assert.True(t, cold.Events[0].At.Equal(time.Unix(0, 1790179911000000001)),
		"a replay recomputes At from the kept file's name")
}

func TestScan_StampsFromModTimeWithoutNanos(t *testing.T) {
	dir, _ := prepared(t)
	writeRaw(t, dir, "a", startPayload("one"), base)

	res, err := Scan(Request{Dir: dir}, base)
	require.NoError(t, err)
	require.Len(t, res.Events, 1)
	assert.True(t, res.Events[0].At.Equal(base))
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `CGO_ENABLED=0 go test ./session/hooks/ -run 'TestEventTime|TestScan_Stamps'`
Expected: compile errors `undefined: eventTime` and `res.Events[0].At undefined`.

- [ ] **Step 3: Implement**

In `session/hooks/event.go`, add `"time"` to the imports and this field at the end of `Event`:

```go
	// At is when the hook ran. Scan sets it from the file's name or
	// modification time; ParseEvent and Compact leave it alone, since it
	// is never part of a payload.
	At time.Time
```

In `session/hooks/scan.go`, add `"strconv"` to the imports and an `at` field to `eventFile`:

```go
type eventFile struct {
	path string
	stem string
	mod  time.Time
	at   time.Time
	size int64
}
```

Order by it: replace `byTimeThenStem` with:

```go
func byTimeThenStem(a, b eventFile) int {
	if c := a.at.Compare(b.at); c != 0 {
		return c
	}
	return strings.Compare(a.stem, b.stem)
}
```

In `Scan`, replace:

```go
		f := eventFile{path: filepath.Join(dir, name), stem: stem, mod: info.ModTime(), size: info.Size()}
```

with:

```go
		f := eventFile{path: filepath.Join(dir, name), stem: stem, mod: info.ModTime(), size: info.Size()}
		f.at = eventTime(f.stem, f.mod)
```

and replace:

```go
	events := make([]Event, len(stamped))
	for i, s := range stamped {
		events[i] = s.ev
	}
```

with:

```go
	events := make([]Event, len(stamped))
	for i, s := range stamped {
		ev := s.ev
		ev.At = s.at
		events[i] = ev
	}
```

Add at the end of `scan.go`:

```go
// minPlausibleNanos is 2001-09-09 in Unix nanoseconds. A smaller name
// prefix is not a `date +%s%N` timestamp: macOS date prints a literal N,
// so its prefix does not parse, and anything else is seconds or junk.
const minPlausibleNanos = 1_000_000_000_000_000_000

// eventTime is when the hook that wrote a file ran: the <unix-nanos>
// prefix of its <unix-nanos>-<pid> stem, or mod when the prefix is not a
// plausible nanosecond timestamp. A kept .ev file keeps both its stem and
// its original modification time, so a replay gets the same answer.
func eventTime(stem string, mod time.Time) time.Time {
	prefix, _, _ := strings.Cut(stem, "-")
	if n, err := strconv.ParseInt(prefix, 10, 64); err == nil && n >= minPlausibleNanos {
		return time.Unix(0, n)
	}
	return mod
}
```

Update the doc comment above `func Scan`: replace `returned ordered by modification time, then file stem.` with `returned ordered by when their hooks ran (see eventTime), then file stem, each with At set.`

- [ ] **Step 4: Run the whole package**

Run: `CGO_ENABLED=0 go test ./session/hooks/ ./session/...`
Expected: all `ok`. The existing scan tests use stems such as `a` and `2-1`, whose prefixes are not plausible nanoseconds, so their order still comes from the modification time.

- [ ] **Step 5: Commit**

```bash
gofmt -w $(git ls-files '*.go' | grep -v '^vendor/')
git add session/hooks
git commit -m "feat(hooks): stamp each event with when its hook ran

At comes from the <unix-nanos> prefix the hook command writes, or the
file's modification time where date has no %N, and events are ordered
by it. The status merge compares it with roster query times.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_017Dc9sKTFBhQzMRk4jUoj4p"
```

---
### Task 4: `claudeState` and the newest-wins merge rule

The pure merge logic, with no events or instances yet. An observation carries the time it was observed; the newer one wins whichever source it came from. The spec's Findings explain why no grace window is needed: the roster publishes a change before Claude runs the hook.

**Files:**
- Create: `session/claude_state.go`
- Test: `session/claude_state_test.go`

- [ ] **Step 1: Write the failing tests**

Create `session/claude_state_test.go`:

```go
package session

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var t0 = time.Date(2026, 9, 23, 18, 0, 0, 0, time.UTC)

func hookObs(st Status, reason string, dt time.Duration) observation {
	return observation{status: st, reason: reason, at: t0.Add(dt), source: obsHook, valid: true}
}

func rosterObs(st Status, reason string, dt time.Duration) observation {
	return observation{status: st, reason: reason, at: t0.Add(dt), source: obsRoster, valid: true}
}

// describe renders a state's status for table assertions: "-" for no
// opinion, else the status and any wait reason.
func describe(s claudeState) string {
	st, reason, ok := s.status()
	if !ok {
		return "-"
	}
	if reason != "" {
		return st.String() + ": " + reason
	}
	return st.String()
}

func TestClaudeState_NewestWinsAcrossSources(t *testing.T) {
	var s claudeState
	assert.True(t, s.offer(hookObs(Ready, "", 0)))
	assert.True(t, s.offer(rosterObs(Running, "", time.Second)), "a newer roster answer replaces a hook observation")
	assert.True(t, s.offer(hookObs(Ready, "", 2*time.Second)), "a newer hook event replaces a roster answer")
	assert.False(t, s.offer(rosterObs(Running, "", 1500*time.Millisecond)), "an older roster answer delivered late is dropped")
	assert.Equal(t, "Ready", describe(s))
}

func TestClaudeState_EqualTimeIsNotNewer(t *testing.T) {
	var s claudeState
	s.offer(hookObs(Ready, "", 0))
	assert.False(t, s.offer(rosterObs(Running, "", 0)))
	assert.Equal(t, "Ready", describe(s))
}

func TestClaudeState_SameStatusIsNotAChange(t *testing.T) {
	var s claudeState
	s.offer(hookObs(Running, "", 0))
	assert.False(t, s.offer(rosterObs(Running, "", time.Second)))
	assert.Equal(t, obsRoster, s.obs.source, "the newer observation is still stored")
}

func TestClaudeState_RosterKeepsHookReason(t *testing.T) {
	var s claudeState
	s.offer(hookObs(Prompting, "permission: Bash", 0))
	assert.False(t, s.offer(rosterObs(Prompting, "permission prompt", time.Second)))
	assert.Equal(t, "Prompting: permission: Bash", describe(s), "the hook names the tool, the roster only the kind of wait")

	s.offer(rosterObs(Running, "", 2*time.Second))
	s.offer(rosterObs(Prompting, "sandbox request", 3*time.Second))
	assert.Equal(t, "Prompting: sandbox request", describe(s), "with no hook observation the roster's reason stands")
}

func TestClaudeState_NoOpinionStillOrdersByTime(t *testing.T) {
	var s claudeState
	s.offer(hookObs(Ready, "", 0))
	assert.True(t, s.offer(observation{at: t0.Add(time.Second), source: obsHook}), "SessionEnd: no opinion")
	assert.Equal(t, "-", describe(s))
	assert.False(t, s.offer(rosterObs(Running, "", 500*time.Millisecond)),
		"an answer from before the SessionEnd must not revive a status")
}

func TestClaudeState_RosterSilence(t *testing.T) {
	var s claudeState
	s.offer(rosterObs(Running, "", 0))
	assert.False(t, s.rosterSilent(t0.Add(-time.Second)), "an older silence changes nothing")
	assert.True(t, s.rosterSilent(t0.Add(time.Second)))
	assert.Equal(t, "-", describe(s), "a roster status must not outlive the roster")

	s.offer(hookObs(Ready, "", 2*time.Second))
	assert.False(t, s.rosterSilent(t0.Add(3*time.Second)), "silence never voids a hook observation")
	assert.Equal(t, "Ready", describe(s))
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `CGO_ENABLED=0 go test ./session/ -run TestClaudeState`
Expected: compile errors `undefined: observation`, `undefined: claudeState`.

- [ ] **Step 3: Implement `session/claude_state.go`**

```go
package session

import "time"

// obsSource says which source produced an observation.
type obsSource int

const (
	obsNone obsSource = iota
	obsHook
	obsRoster
)

// observation is one source's report of a Claude session's status,
// stamped with when it was observed rather than when loom received it.
// valid false is "no opinion": the status ladder decides.
type observation struct {
	status Status
	reason string // wait reason; empty unless status is Prompting
	at     time.Time
	source obsSource
	valid  bool
}

// claudeState is what loom knows about a Claude session from its hooks and
// the agent roster. Instance guards it with mu. See
// docs/superpowers/specs/2026-09-23-claude-hook-events-design.md.
type claudeState struct {
	obs observation
	// sessionID and transcriptPath name the conversation a relaunch
	// resumes (persisted, schema v7). Only a parent SessionStart sets them.
	sessionID      string
	transcriptPath string
	// lastMessage is the parent's last_assistant_message from its latest
	// Stop. lastMsgValid turns false once a prompt or permission request
	// makes it stale.
	lastMessage  string
	lastMsgValid bool
}

// offer applies o if it is newer than the current observation, and reports
// whether the reported status or reason changed. Newest wins whichever
// source either came from: the roster publishes a change before Claude
// runs the hook, so a roster answer stamped after a hook event already
// reflects it, and one stamped before is older and dropped. The comparison
// uses at even when the current observation is no-opinion, so an older
// answer delivered late never revives a status a SessionEnd cleared.
//
// A roster observation with the same status as a current hook one keeps
// the hook's reason: the hook names the tool ("permission: Bash"), the
// roster only the kind of wait ("permission prompt").
func (s *claudeState) offer(o observation) bool {
	if !s.obs.at.IsZero() && !o.at.After(s.obs.at) {
		return false
	}
	if o.source == obsRoster && o.valid && s.obs.valid && s.obs.source == obsHook &&
		o.status == s.obs.status && s.obs.reason != "" {
		o.reason = s.obs.reason
	}
	changed := o.valid != s.obs.valid ||
		(o.valid && (o.status != s.obs.status || o.reason != s.obs.reason))
	s.obs = o
	return changed
}

// rosterSilent records that the roster had no opinion at at: no entry,
// an ambiguous cwd, an unknown status or a failed query. A roster
// observation older than that becomes no-opinion, as a failed query
// clears the roster: a roster-sourced status must not outlive the roster
// that produced it. A hook observation is left alone. Reports whether
// anything changed.
func (s *claudeState) rosterSilent(at time.Time) bool {
	if s.obs.source != obsRoster || !s.obs.valid || !at.After(s.obs.at) {
		return false
	}
	s.obs = observation{at: at, source: obsRoster}
	return true
}

// status returns the current observation, with ok false for no opinion.
func (s *claudeState) status() (Status, string, bool) {
	if !s.obs.valid {
		return Ready, "", false
	}
	return s.obs.status, s.obs.reason, true
}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `CGO_ENABLED=0 go test ./session/ -run TestClaudeState`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
gofmt -w $(git ls-files '*.go' | grep -v '^vendor/')
git add session/claude_state.go session/claude_state_test.go
git commit -m "feat(session): merge Claude status observations, newest first

An observation carries when it was observed; the newer one wins whether
it came from a hook or the roster, with no grace window, because the
roster publishes a change before Claude runs the hook. A roster answer
with no opinion voids only a roster-sourced status.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_017Dc9sKTFBhQzMRk4jUoj4p"
```

---

### Task 5: Hook events → observations

Maps each event to an observation, records the session ID and the last message, and replays the whole probe to pin the result after every one of its 34 events.

**Files:**
- Modify: `session/claude_state.go`
- Test: `session/claude_state_test.go`

- [ ] **Step 1: Write the failing tests**

Replace the import block of `session/claude_state_test.go` with:

```go
import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session/hooks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)
```

Append:

```go
func TestClaudeState_EventMapping(t *testing.T) {
	subagent := hooks.Task{ID: "a1", Type: "subagent", Status: "running"}
	cases := []struct {
		name string
		ev   hooks.Event
		want string
	}{
		{"startup", hooks.Event{Name: hooks.EventSessionStart, Source: "startup"}, "Ready"},
		{"resume", hooks.Event{Name: hooks.EventSessionStart, Source: "resume"}, "Ready"},
		{"clear", hooks.Event{Name: hooks.EventSessionStart, Source: "clear"}, "Ready"},
		{"compact runs mid-turn", hooks.Event{Name: hooks.EventSessionStart, Source: "compact"}, "-"},
		{"unknown source", hooks.Event{Name: hooks.EventSessionStart, Source: "teleport"}, "-"},
		{"prompt", hooks.Event{Name: hooks.EventUserPromptSubmit}, "Running"},
		{"subagent prompt ignored", hooks.Event{Name: hooks.EventUserPromptSubmit, AgentID: "a1"}, "-"},
		{"permission", hooks.Event{Name: hooks.EventPermissionRequest, ToolName: "Edit"}, "Prompting: permission: Edit"},
		{"subagent permission shows in the parent", hooks.Event{Name: hooks.EventPermissionRequest, ToolName: "Bash", AgentID: "a1"}, "Prompting: permission: Bash"},
		{"permission without a tool", hooks.Event{Name: hooks.EventPermissionRequest}, "Prompting: permission"},
		{"elicitation", hooks.Event{Name: hooks.EventNotification, NotificationType: "elicitation_dialog", Message: "Claude needs your input"}, "Prompting: Claude needs your input"},
		{"idle notification", hooks.Event{Name: hooks.EventNotification, NotificationType: "idle_prompt"}, "-"},
		{"stop", hooks.Event{Name: hooks.EventStop, HasTasks: true}, "Ready"},
		{"stop without a task list", hooks.Event{Name: hooks.EventStop}, "Ready"},
		{"stop while a subagent runs", hooks.Event{Name: hooks.EventStop, HasTasks: true, Tasks: []hooks.Task{subagent}}, "Running"},
		{"stop after a subagent finished", hooks.Event{Name: hooks.EventStop, HasTasks: true, Tasks: []hooks.Task{{ID: "a1", Type: "subagent", Status: "completed"}}}, "Ready"},
		{"idle teammates stay listed as running", hooks.Event{Name: hooks.EventStop, HasTasks: true, Tasks: []hooks.Task{{ID: "tk", Type: "teammate", Status: "running"}}}, "Ready"},
		{"a background shell runs while Claude waits", hooks.Event{Name: hooks.EventStop, HasTasks: true, Tasks: []hooks.Task{{ID: "b1", Type: "shell", Status: "running"}}}, "Ready"},
		{"subagent stop", hooks.Event{Name: hooks.EventSubagentStop, AgentID: "a1"}, "-"},
		{"session end", hooks.Event{Name: hooks.EventSessionEnd}, "-"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var s claudeState
			tc.ev.At = t0
			s.applyEvent(tc.ev)
			assert.Equal(t, tc.want, describe(s))
		})
	}
}

// The Notification for a prompt arrives about six seconds after its
// PermissionRequest, with a generic message; the specific reason stays.
func TestClaudeState_NotificationKeepsPermissionReason(t *testing.T) {
	var s claudeState
	s.applyEvent(hooks.Event{Name: hooks.EventPermissionRequest, ToolName: "Bash", At: t0})
	s.applyEvent(hooks.Event{Name: hooks.EventNotification, NotificationType: "permission_prompt",
		Message: "Claude needs your permission", At: t0.Add(6 * time.Second)})
	assert.Equal(t, "Prompting: permission: Bash", describe(s))
}

func TestClaudeState_SessionIDOnlyFromParentSessionStart(t *testing.T) {
	var s claudeState
	s.applyEvent(hooks.Event{Name: hooks.EventSessionStart, Source: "startup", SessionID: "one", TranscriptPath: "/t/one.jsonl", At: t0})
	s.applyEvent(hooks.Event{Name: hooks.EventSessionStart, Source: "startup", SessionID: "sub", AgentID: "a1", At: t0.Add(time.Second)})
	s.applyEvent(hooks.Event{Name: hooks.EventSessionEnd, At: t0.Add(2 * time.Second)})
	assert.Equal(t, "one", s.sessionID)

	s.applyEvent(hooks.Event{Name: hooks.EventSessionStart, Source: "compact", SessionID: "two", TranscriptPath: "/t/two.jsonl", At: t0.Add(3 * time.Second)})
	assert.Equal(t, "two", s.sessionID, "compact records the ID even though it sets no status")
	assert.Equal(t, "/t/two.jsonl", s.transcriptPath)
}

func TestClaudeState_LastMessageWindow(t *testing.T) {
	var s claudeState
	s.applyEvent(hooks.Event{Name: hooks.EventStop, LastAssistantMessage: "done", At: t0})
	assert.Equal(t, "done", s.lastMessage)
	assert.True(t, s.lastMsgValid)

	s.applyEvent(hooks.Event{Name: hooks.EventPermissionRequest, ToolName: "Bash", At: t0.Add(time.Second)})
	assert.False(t, s.lastMsgValid, "a permission request is mid-turn: the message belongs to the previous turn")

	s.applyEvent(hooks.Event{Name: hooks.EventStop, LastAssistantMessage: "again", At: t0.Add(2 * time.Second)})
	assert.True(t, s.lastMsgValid)
	s.applyEvent(hooks.Event{Name: hooks.EventUserPromptSubmit, At: t0.Add(3 * time.Second)})
	assert.False(t, s.lastMsgValid)
	assert.Equal(t, "again", s.lastMessage)
}

// probeEvents reads the 2026-09-23 probe's payloads in the order their
// hooks ran, stamped the way Scan stamps them.
func probeEvents(t *testing.T) []hooks.Event {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("hooks", "testdata", "probe-2.1.280", "*.json"))
	require.NoError(t, err)
	require.Len(t, files, 34)
	sort.Strings(files)
	events := make([]hooks.Event, 0, len(files))
	for _, f := range files {
		data, err := os.ReadFile(f)
		require.NoError(t, err)
		ev, err := hooks.ParseEvent(data)
		require.NoError(t, err, f)
		prefix, _, _ := strings.Cut(filepath.Base(f), "-")
		n, err := strconv.ParseInt(prefix, 10, 64)
		require.NoError(t, err)
		ev.At = time.Unix(0, n)
		events = append(events, ev)
	}
	return events
}

// TestClaudeState_ProbeReplay pins what hooks alone conclude after each
// event of the live probe (see testdata/probe-2.1.280/README.md). Two
// entries are known to be wrong without a roster, and say so.
func TestClaudeState_ProbeReplay(t *testing.T) {
	want := []string{
		"Ready",            // SessionStart startup
		"Running", "Ready", // plain turn
		"Running", "Prompting: permission: Bash", "Prompting: permission: Bash", "Ready", // Bash prompt, its Notification, approved
		"Ready",            // an internal helper's SubagentStop
		"Running", "Running", // prompt, SubagentStart
		"Running",          // parent Stop while the background subagent runs
		"Prompting: permission: Bash", "Prompting: permission: Bash", // the subagent's prompt and its Notification
		"Prompting: permission: Bash", "Prompting: permission: Bash", // SubagentStops: hooks cannot see the approval; the roster can
		"Running", "Ready", // the <task-notification> prompt resumes the parent; final Stop
		"Ready",            // helper SubagentStop
		"-", "Ready",       // /clear: SessionEnd, SessionStart
		"Running", "Ready", "-", // a turn, then /exit
		"Ready",            // --resume
		"Running", "Running", "Running", "Running", // teammate: prompt, start, stop, idle
		"Ready", "Ready",   // the lead's intermediate Stop (the roster corrects it) and its final Stop
		"Ready", "Ready",   // helper SubagentStops
		"-", "-",           // /exit, then a failed --resume
	}
	events := probeEvents(t)
	require.Len(t, want, len(events))

	var s claudeState
	for i, ev := range events {
		s.applyEvent(ev)
		assert.Equal(t, want[i], describe(s), "after event %d (%s)", i, ev.Name)
	}
	assert.Equal(t, "487f460e-49ff-4961-a883-6b2c290c9aec", s.sessionID,
		"the failed resume's SessionEnd carries another ID and must not replace this one")
	assert.Equal(t, "/home/user/.claude/projects/-probe-work/487f460e-49ff-4961-a883-6b2c290c9aec.jsonl", s.transcriptPath)
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `CGO_ENABLED=0 go test ./session/ -run TestClaudeState`
Expected: compile error `s.applyEvent undefined`.

- [ ] **Step 3: Implement the mapping**

In `session/claude_state.go`, replace `import "time"` with:

```go
import (
	"time"

	"github.com/aidan-bailey/loom/session/hooks"
)
```

Append:

```go
// waitingNotifications are the Notification types that mean Claude is
// waiting on the user. They fire only after about six seconds without a
// keystroke, so PermissionRequest drives Prompting; these cover the
// prompts it misses (a sandboxed command's network request, elicitation
// dialogs).
var waitingNotifications = map[string]bool{
	"permission_prompt":      true,
	"elicitation_dialog":     true,
	"elicitation_url_dialog": true,
}

// applyEvent folds one hook event into s and reports whether the status
// or wait reason changed. Only parent events (no agent_id) count, except
// PermissionRequest: a subagent's prompt appears in the parent's UI.
func (s *claudeState) applyEvent(ev hooks.Event) bool {
	if ev.AgentID != "" && ev.Name != hooks.EventPermissionRequest {
		return false
	}
	switch ev.Name {
	case hooks.EventSessionStart:
		// Only SessionStart names the conversation: a failed --resume of an
		// unknown ID sends a SessionEnd carrying that ID.
		if ev.SessionID != "" {
			s.sessionID, s.transcriptPath = ev.SessionID, ev.TranscriptPath
		}
		switch ev.Source {
		case "startup", "resume", "clear":
			return s.offer(observation{status: Ready, at: ev.At, source: obsHook, valid: true})
		}
		// compact, or a source this build does not know: compaction can run
		// mid-turn, so it says nothing about the status.
		return false
	case hooks.EventUserPromptSubmit:
		s.lastMsgValid = false
		return s.offer(observation{status: Running, at: ev.At, source: obsHook, valid: true})
	case hooks.EventPermissionRequest:
		s.lastMsgValid = false
		reason := "permission"
		if ev.ToolName != "" {
			reason = "permission: " + ev.ToolName
		}
		return s.offer(observation{status: Prompting, reason: reason, at: ev.At, source: obsHook, valid: true})
	case hooks.EventNotification:
		if !waitingNotifications[ev.NotificationType] {
			return false
		}
		// It arrives about six seconds after the PermissionRequest for the
		// same prompt, with a generic message; keep the specific reason.
		reason := ev.Message
		if s.obs.valid && s.obs.status == Prompting && s.obs.reason != "" {
			reason = s.obs.reason
		}
		return s.offer(observation{status: Prompting, reason: reason, at: ev.At, source: obsHook, valid: true})
	case hooks.EventStop:
		s.lastMessage, s.lastMsgValid = ev.LastAssistantMessage, true
		// Stop ends a turn, not the work: with a background subagent still
		// running, Claude resumes the parent when it reports back.
		status := Ready
		if runningSubagent(ev.Tasks) {
			status = Running
		}
		return s.offer(observation{status: status, at: ev.At, source: obsHook, valid: true})
	case hooks.EventSessionEnd:
		return s.offer(observation{at: ev.At, source: obsHook})
	}
	return false
}

// runningSubagent reports whether a Stop's background_tasks lists a
// running plain subagent. Teammate and shell tasks don't count: an idle
// teammate stays listed as running, and a background shell runs while
// Claude waits for input. A missing list reads as none: a wrong Ready is
// corrected by the roster query the event triggers, while a wrong Running
// would stay until the next event.
func runningSubagent(tasks []hooks.Task) bool {
	for _, t := range tasks {
		if t.Type == "subagent" && t.Status == "running" {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `CGO_ENABLED=0 go test ./session/ -run TestClaudeState -v 2>&1 | tail -20`
Expected: every `TestClaudeState_*` passes. If `TestClaudeState_ProbeReplay` fails, the assertion message names the event index; compare it with the table in `session/hooks/testdata/probe-2.1.280/README.md` before changing either the mapping or the expectation.

- [ ] **Step 5: Commit**

```bash
gofmt -w $(git ls-files '*.go' | grep -v '^vendor/')
git add session/claude_state.go session/claude_state_test.go
git commit -m "feat(session): map Claude hook events to status observations

PermissionRequest drives Prompting (a subagent's too, since its prompt
shows in the parent), a Stop with a running subagent stays Running,
and only a parent SessionStart names the conversation. A replay of the
live probe pins the result after every event.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_017Dc9sKTFBhQzMRk4jUoj4p"
```

---

### Task 6: Wire `claudeState` into `Instance`

Scans now update the instance's status observation, session ID and last message. New launches and vanished folders clear the right parts.

**Files:**
- Modify: `session/instance.go` (field), `session/subagent_hooks.go`, `session/claude_state.go`
- Test: `session/claude_state_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `session/claude_state_test.go`:

```go
func TestApplyHookScan_DrivesClaudeState(t *testing.T) {
	inst := hooksInstance(t, "claude")
	require.True(t, inst.ApplyHookScan(HookScanResult{LaunchID: "L", Replayed: true, Events: []hooks.Event{
		{Name: hooks.EventSessionStart, Source: "startup", SessionID: "s1", TranscriptPath: "/t/s1.jsonl", At: t0},
		{Name: hooks.EventUserPromptSubmit, At: t0.Add(time.Second)},
		{Name: hooks.EventStop, HasTasks: true, LastAssistantMessage: "done", At: t0.Add(2 * time.Second)},
	}}))

	st, _, ok := inst.ClaudeStatus()
	require.True(t, ok)
	assert.Equal(t, Ready, st)
	msg, valid := inst.LastMessage()
	assert.Equal(t, "done", msg)
	assert.True(t, valid)
	id, transcript := inst.ClaudeSession()
	assert.Equal(t, "s1", id)
	assert.Equal(t, "/t/s1.jsonl", transcript)

	assert.True(t, inst.ObserveRoster(Running, "", true, t0.Add(3*time.Second)))
	st, _, _ = inst.ClaudeStatus()
	assert.Equal(t, Running, st)
	assert.True(t, inst.ObserveRoster(Ready, "", false, t0.Add(4*time.Second)), "silence voids the roster's own status")
	_, _, ok = inst.ClaudeStatus()
	assert.False(t, ok)
}

func TestResetHookLaunch_KeepsConversationClearsStatus(t *testing.T) {
	inst := hooksInstance(t, "claude")
	require.True(t, inst.ApplyHookScan(HookScanResult{LaunchID: "L", Replayed: true, Events: []hooks.Event{
		{Name: hooks.EventSessionStart, Source: "startup", SessionID: "s1", TranscriptPath: "/t/s1.jsonl", At: t0},
		{Name: hooks.EventStop, LastAssistantMessage: "done", At: t0.Add(time.Second)},
	}}))

	inst.resetHookLaunch()

	_, _, ok := inst.ClaudeStatus()
	assert.False(t, ok)
	_, valid := inst.LastMessage()
	assert.False(t, valid)
	id, _ := inst.ClaudeSession()
	assert.Equal(t, "s1", id, "the relaunch resumes this conversation, so the reset keeps it")
	assert.False(t, inst.ObserveRoster(Running, "", true, time.Now().Add(-time.Minute)),
		"a roster answer from before the relaunch must not revive the old process's status")
}

func TestForgetSubagentsWithoutHooks_ClearsHookState(t *testing.T) {
	inst := hooksInstance(t, "claude")
	require.True(t, inst.ApplyHookScan(HookScanResult{LaunchID: "0123456789abcdef", Replayed: true, Events: []hooks.Event{
		{Name: hooks.EventStop, LastAssistantMessage: "done", At: time.Now().Add(-time.Minute)},
	}}))

	inst.ForgetSubagentsWithoutHooks()

	_, _, ok := inst.ClaudeStatus()
	assert.False(t, ok, "nothing refreshes a hook observation once its folder is gone")
	_, valid := inst.LastMessage()
	assert.False(t, valid)
}

func TestHooksLaunched(t *testing.T) {
	assert.False(t, hooksInstance(t, "aider").HooksLaunched())
	inst := hooksInstance(t, "claude")
	assert.True(t, inst.HooksLaunched(), "a restored instance that has not adopted an ID yet counts")
	inst.hookLaunchID = noHooksLaunchID
	assert.False(t, inst.HooksLaunched())
	inst.hookLaunchID = "0123456789abcdef"
	assert.True(t, inst.HooksLaunched())
	inst.ConfigDir = ""
	assert.False(t, inst.HooksLaunched())
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `CGO_ENABLED=0 go test ./session/ -run 'TestApplyHookScan_DrivesClaudeState|TestResetHookLaunch|TestForgetSubagentsWithoutHooks_ClearsHookState|TestHooksLaunched'`
Expected: compile errors `inst.ClaudeStatus undefined`, `inst.LastMessage undefined`.

- [ ] **Step 3: Add the field to `Instance`**

In `session/instance.go`, directly after the line `subagentWarm bool` in the `Instance` struct, add:

```go

	// claude is what Claude's hooks and the agent roster report about this
	// session: its status, the conversation to resume and its last
	// message (see claude_state.go). Guarded by mu. sessionID and
	// transcriptPath are persisted (InstanceData v7); the rest is not.
	claude claudeState
```

- [ ] **Step 4: Add the lifecycle helpers and accessors to `session/claude_state.go`**

Append:

```go
// newLaunch forgets what the previous process reported, keeping the
// conversation it names: the relaunch resumes it, and the new process's
// SessionStart replaces it. The no-opinion observation is stamped now, so
// a roster answer from before the relaunch cannot revive the old status.
func (s *claudeState) newLaunch(now time.Time) {
	s.obs = observation{at: now, source: obsHook}
	s.lastMessage, s.lastMsgValid = "", false
}

// folderGone drops what the hooks reported once their folder vanished
// mid-run: nothing will refresh a hook observation. A roster observation
// stands, since the roster still answers for the session.
func (s *claudeState) folderGone(now time.Time) {
	if s.obs.source == obsHook && s.obs.valid {
		s.obs = observation{at: now, source: obsHook}
	}
	s.lastMessage, s.lastMsgValid = "", false
}

// ClaudeStatus returns the status Claude's hooks or the roster last
// reported for this session, with the wait reason when Prompting. ok is
// false when neither has an opinion, and the status ladder decides.
func (i *Instance) ClaudeStatus() (status Status, reason string, ok bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.claude.status()
}

// ObserveRoster offers the roster's answer for this session, observed at
// at (when the query started). ok false means the roster had no opinion.
// Reports whether the status or wait reason changed. Call it on the Update
// goroutine.
func (i *Instance) ObserveRoster(status Status, reason string, ok bool, at time.Time) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	if !ok {
		return i.claude.rosterSilent(at)
	}
	return i.claude.offer(observation{status: status, reason: reason, at: at, source: obsRoster, valid: true})
}

// LastMessage returns Claude's last message from its latest Stop, and
// whether it is still current (no prompt or permission request since).
func (i *Instance) LastMessage() (string, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.claude.lastMessage, i.claude.lastMsgValid
}

// ClaudeSession returns the conversation a relaunch should resume: the
// session ID and transcript path from the latest parent SessionStart.
func (i *Instance) ClaudeSession() (sessionID, transcriptPath string) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.claude.sessionID, i.claude.transcriptPath
}

// HooksLaunched reports whether this session's current launch registered
// loom's hooks, so a scan can find its events: a Claude program with a
// config dir whose launch did not fall back to noHooksLaunchID. A
// restored instance that has not adopted its folder's ID yet (empty ID)
// counts.
func (i *Instance) HooksLaunched() bool {
	if i.ConfigDir == "" || !IsClaudeProgram(i.Program()) {
		return false
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.hookLaunchID != noHooksLaunchID
}
```

- [ ] **Step 5: Feed scans into it in `session/subagent_hooks.go`**

In `resetHookLaunch`, replace:

```go
	i.subagentTrackerLocked().Reset()
	i.mu.Unlock()
	i.removeSubagentHooks()
```

with:

```go
	i.subagentTrackerLocked().Reset()
	i.claude.newLaunch(time.Now())
	i.mu.Unlock()
	i.removeSubagentHooks()
```

Update its doc comment's first sentence to: `// resetHookLaunch clears the previous launch's hook state before a new process starts: the tracker and warm flag, the Claude status and last message (the conversation ID is kept for the relaunch to resume), hookLaunchID (set to the sentinel, so no stale scan result can match until prepareHooks maybe sets a real one), and the old hooks folder on disk.`

Replace the body of `ApplyHookScan` from `tracker := i.subagentTrackerLocked()` to the end with:

```go
	tracker := i.subagentTrackerLocked()
	if res.Replayed {
		tracker.Reset()
		i.claude.lastMessage, i.claude.lastMsgValid = "", false
	} else if !i.subagentWarm {
		return false
	}
	tracker.Apply(res.Events, res.Meta)
	for _, ev := range res.Events {
		i.claude.applyEvent(ev)
	}
	i.subagentWarm = true
	return true
}
```

and replace its doc comment with:

```go
// ApplyHookScan applies one scan result to the subagent tracker and the
// Claude state, and reports whether it was applied. A result for another
// launch is dropped. An instance restored after a loom restart has no
// launch ID yet and adopts the result's. A replayed result rebuilds the
// tracker and the last message from scratch; the status observation needs
// no reset, since replayed events are never newer than it. An incremental
// result is only applied once the tracker is warm.
```

In `ForgetSubagentsWithoutHooks`, replace:

```go
	wasWarm := i.subagentWarm
	i.subagentWarm = false
	i.subagentTrackerLocked().Reset()
	i.mu.Unlock()
```

with:

```go
	wasWarm := i.subagentWarm
	i.subagentWarm = false
	i.subagentTrackerLocked().Reset()
	i.claude.folderGone(time.Now())
	i.mu.Unlock()
```

and add this sentence to the end of its doc comment: `The Claude status the hooks reported and the last message go with them; a roster-sourced status stays.`

Add `"time"` to the imports of `session/subagent_hooks.go`.

- [ ] **Step 6: Run the session suite**

Run: `CGO_ENABLED=0 go test ./session/`
Expected: `ok`.

- [ ] **Step 7: Commit**

```bash
gofmt -w $(git ls-files '*.go' | grep -v '^vendor/')
git add session
git commit -m "feat(session): track Claude status, conversation and last message per instance

Hook scans now update the instance's merged status observation, the
session ID and transcript to resume, and Claude's last message. A new
launch clears the status and message but keeps the conversation, and a
vanished hooks folder drops what its hooks reported.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_017Dc9sKTFBhQzMRk4jUoj4p"
```

---

### Task 7: Hooks on every Claude launch

Hooks now carry status, the session ID and the last message, so `claude_subagent_tracking` only hides or shows subagent rows. A trial run before this plan was written showed exactly two tests depend on the old gate; both are updated below.

**Files:**
- Modify: `session/subagent_hooks.go`, `config/config.go` (comments only)
- Test: `session/subagent_hooks_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `session/subagent_hooks_test.go`:

```go
func TestLaunchProgram_HooksInstalledWithTrackingOff(t *testing.T) {
	withTracking(t, false)
	inst := hooksInstance(t, "claude")

	got := inst.launchProgram("claude", true)

	assert.Contains(t, got, settingsFlag(inst), "hooks carry status and the session ID, not only subagent rows")
}

func TestSubagents_HiddenWhenTrackingOff(t *testing.T) {
	withTracking(t, true)
	inst := hooksInstance(t, "claude")
	require.True(t, inst.ApplyHookScan(HookScanResult{LaunchID: "L", Replayed: true,
		Events: []hooks.Event{{Name: hooks.EventSubagentStart, AgentID: "a1", TranscriptPath: "/p/s.jsonl"}},
		Meta:   map[string]subagent.Meta{"a1": {AgentType: "Explore"}}}))
	require.Len(t, inst.Subagents(), 1)

	withTracking(t, false)
	assert.Nil(t, inst.Subagents())
	withTracking(t, true)
	assert.Len(t, inst.Subagents(), 1, "the tracker kept running, so the rows return at once")
}
```

In `TestLaunchProgram_SkipsHooks`, delete the case line:

```go
		{"disabled", false, "claude", false},
```

In `TestLaunchProgram_UntrackedRelaunchClearsState`, the relaunch must still skip hooks without the toggle. Replace:

```go
	withTracking(t, false)
	inst.recoveryLaunch()
```

with:

```go
	// A user-supplied --settings is one of the launches that skip loom's hooks.
	inst.SetProgram("claude --settings /mine.json")
	inst.recoveryLaunch()
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `CGO_ENABLED=0 go test ./session/ -run 'TestLaunchProgram|TestSubagents_HiddenWhenTrackingOff'`
Expected: `TestLaunchProgram_HooksInstalledWithTrackingOff` fails (no `--settings`); `TestSubagents_HiddenWhenTrackingOff` fails (rows shown with tracking off).

- [ ] **Step 3: Implement**

In `session/subagent_hooks.go`, in `prepareHooks`, replace:

```go
	if !subagentTrackingEnabled.Load() || i.ConfigDir == "" ||
		runtime.GOOS == "windows" || !IsClaudeProgram(program) {
```

with:

```go
	if i.ConfigDir == "" || runtime.GOOS == "windows" || !IsClaudeProgram(program) {
```

In `Subagents`, replace:

```go
	if i.subagents == nil || !subagentLive(i.Status) {
```

with:

```go
	if i.subagents == nil || !subagentLive(i.Status) || !subagentTrackingEnabled.Load() {
```

and add to its doc comment: `, or when subagent tracking is turned off`.

Replace the doc comment on `var subagentTrackingEnabled` with:

```go
// subagentTrackingEnabled mirrors config.SubagentTrackingEnabled(). The
// app sets it at the same points as SetLoomContextEnabled. It only decides
// whether Subagents returns the tracked rows: every Claude launch gets
// hooks, because they also carry the session's status, ID and last
// message, and the tracker keeps running with the setting off, so turning
// it back on shows the current agents at once.
```

In `config/config.go`, replace the `ClaudeSubagentTracking` field comment:

```go
	// ClaudeSubagentTracking controls whether new Claude sessions launch
	// with hooks that report subagent and teammate state, shown as a count
	// on rail cards and as rows on overview cards (see session/subagent).
	// nil is treated as enabled (read via SubagentTrackingEnabled),
	// matching ClaudeLoomContext. Takes effect at the next launch or
	// resume.
```

with:

```go
	// ClaudeSubagentTracking controls whether the subagents and teammates
	// loom's hooks track are shown, as a count on rail cards and as rows
	// on overview cards (see session/subagent). Every Claude launch gets
	// the hooks regardless, since they also carry status, the session ID
	// and the last message. nil is treated as enabled (read via
	// SubagentTrackingEnabled), matching ClaudeLoomContext. Takes effect
	// at once.
```

and in the comment above `func (c *Config) SubagentTrackingEnabled()`, replace `with loom's subagent hooks` with `showing the subagents loom's hooks track`.

- [ ] **Step 4: Run the affected suites**

Run: `CGO_ENABLED=0 go test ./session/... ./app/... ./ui/... ./config/...`
Expected: all `ok`.

- [ ] **Step 5: Commit**

```bash
gofmt -w $(git ls-files '*.go' | grep -v '^vendor/')
git add session config
git commit -m "feat(session): install loom's hooks on every Claude launch

Hooks now report status, the conversation ID and the last message, so
the subagent-tracking setting only hides or shows the subagent rows,
at once rather than at the next launch.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_017Dc9sKTFBhQzMRk4jUoj4p"
```

---
### Task 8: Trailing requests on `pollGate`

`expedite()` only resets the interval, so a trigger that lands while a job is in flight waits for the next health tick (up to 3s). That in-flight job usually started *before* the event, and the merge rule discards its answer. `request()` adds one follow-up dispatch as soon as the flight lands.

**Files:**
- Modify: `app/pollgate.go`, `app/app.go` (extract `activeInstances`), `app/github.go` (host for the helper, next to `allInstances`)
- Test: `app/pollgate_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `app/pollgate_test.go`:

```go
func TestPollGateRequest(t *testing.T) {
	now := time.Now()
	g := pollGate{last: now}
	g.request()
	assert.True(t, g.due(now, time.Hour), "a request makes an idle gate due at once")
	assert.False(t, g.pending, "nothing in flight: the caller's own dispatch is the follow-up")

	g.inFlight = true
	g.request()
	g.request()
	assert.True(t, g.pending, "requests during a flight collapse into one follow-up")
}

func TestDeliverGatedRedispatchesPendingOnce(t *testing.T) {
	inst := startedInstanceWithProgram(t, "gate-redispatch", "claude", "x")
	m := homeWithAppState(t)
	m.list.AddInstance(inst)
	require.NotNil(t, m.maybeRosterQuery(m.activeInstances()))
	m.gate(gateRoster).request()

	_, cmd := m.Update(gatedMsg{kind: gateRoster, msg: rosterReadyMsg{}})

	require.NotNil(t, cmd, "the pending request dispatches again as soon as the flight lands")
	assert.True(t, m.gate(gateRoster).inFlight)
	assert.False(t, m.gate(gateRoster).pending)

	_, cmd = m.Update(gatedMsg{kind: gateRoster, msg: rosterReadyMsg{}})
	assert.Nil(t, cmd, "no request, no follow-up")
	assert.False(t, m.gate(gateRoster).inFlight)
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `CGO_ENABLED=0 go test ./app/ -run 'TestPollGateRequest|TestDeliverGatedRedispatchesPendingOnce'`
Expected: compile errors `g.request undefined`, `g.pending undefined`, `m.activeInstances undefined`.

- [ ] **Step 3: Implement `request` and the re-dispatch in `app/pollgate.go`**

Replace the `pollGate` struct:

```go
type pollGate struct {
	last     time.Time
	inFlight bool
}
```

with:

```go
type pollGate struct {
	last     time.Time
	inFlight bool
	// pending records a request() made while a dispatch was in flight;
	// deliverGated dispatches once more when that flight lands.
	pending bool
}
```

Add after `expedite`:

```go
// request asks for the job to run as soon as possible: it is due at once,
// and a request made while a dispatch is in flight is kept, so
// deliverGated dispatches once more after that flight lands. The caller
// still makes its own maybe… call, which dispatches at once when nothing
// is in flight. Requests during one flight collapse into one follow-up.
// For triggers that must not wait for the next health tick: a pane going
// quiet, or a status change the roster should confirm.
func (g *pollGate) request() {
	g.expedite()
	if g.inFlight {
		g.pending = true
	}
}
```

Replace `deliverGated` with:

```go
// deliverGated disarms msg's gate before anything else, then routes the
// inner message back through Update so it reaches the same handler it
// would have reached unwrapped. A nil inner message still disarms. A
// request() made during the flight dispatches the job once more, after
// the inner message is handled.
func (m *home) deliverGated(msg gatedMsg) (tea.Model, tea.Cmd) {
	g := m.gate(msg.kind)
	g.inFlight = false
	again := g.pending
	g.pending = false
	var cmd tea.Cmd
	switch inner := msg.msg.(type) {
	case nil:
	case tea.BatchMsg:
		// A builder broke dispatchGated's single-message rule. Update has
		// no case for a BatchMsg, so its Cmds would silently never run.
		log.For("app").Error("gated_batch_msg", "kind", msg.kind.String(), "cmds", len(inner))
	default:
		_, cmd = m.Update(msg.msg)
	}
	if again {
		cmd = tea.Batch(cmd, m.redispatch(msg.kind))
	}
	return m, cmd
}

// redispatch runs kind's job again for a request() made mid-flight. Only
// the jobs that request() have an entry.
func (m *home) redispatch(kind gateKind) tea.Cmd {
	switch kind {
	case gateRoster:
		return m.maybeRosterQuery(m.activeInstances())
	case gateHookScan:
		return m.maybeHookScan(m.activeInstances())
	}
	return nil
}
```

- [ ] **Step 4: Extract `activeInstances`**

In `app/github.go`, add after `allInstances`:

```go
// activeInstances returns the loaded instances the background jobs may
// touch: started and not paused. Recoverable placeholders are ephemeral
// orphan-review rows: they report Started() (so recover/discard can reach
// their handles) but must never be driven by a background job, since
// RepairPtmx would attach a PTY and TransitionTo(Running) would promote a
// never-confirmed orphan past the explicit recover flow. Loading rows are
// likewise owned by an in-flight Start/Resume/Recover: probing them
// mid-setup reads a dead tmux session and force-flips them to Paused
// under the op. Deleting rows are being torn down.
func (m *home) activeInstances() []*session.Instance {
	var active []*session.Instance
	for _, inst := range m.allInstances() {
		st := inst.GetStatus()
		if inst.Started() && !inst.Paused() && st != session.Deleting && st != session.Recoverable && st != session.Loading {
			active = append(active, inst)
		}
	}
	return active
}
```

In `app/app.go`, in the `tickUpdateMetadataMessage` case, replace:

```go
		// Collect instances from every loaded workspace slot.
		allInstances := m.allInstances()

		// Filter to active instances.
		selected := m.list.GetSelectedInstance()
		var active []*session.Instance
		for _, inst := range allInstances {
			status := inst.GetStatus()
			// Recoverable placeholders are ephemeral orphan-review rows:
			// they report Started() (so recover/discard can reach their
			// handles) but must never be driven by the tick — RepairPtmx
			// would attach a PTY and TransitionTo(Running) would promote a
			// never-confirmed orphan past the explicit recover flow.
			// Loading rows are likewise owned by an in-flight
			// Start/Resume/Recover: probing them mid-setup reads a dead
			// tmux session and force-flips them to Paused under the op.
			if inst.Started() && !inst.Paused() && status != session.Deleting && status != session.Recoverable && status != session.Loading {
				active = append(active, inst)
			}
		}
```

with:

```go
		// Active instances from every loaded workspace slot (see
		// activeInstances for what is skipped and why).
		selected := m.list.GetSelectedInstance()
		active := m.activeInstances()
```

- [ ] **Step 5: Run the app suite**

Run: `CGO_ENABLED=0 go test ./app/`
Expected: `ok`. If the compiler reports `allInstances declared and not used`, a later line in that case still used it; replace that use with `m.allInstances()`.

- [ ] **Step 6: Commit**

```bash
gofmt -w $(git ls-files '*.go' | grep -v '^vendor/')
git add app
git commit -m "feat(app): let a gated job be requested mid-flight

request() makes a job due at once and, when it is already in flight,
dispatches it once more as soon as that flight lands, so an event never
waits for the next health tick. The tick's active-instance filter moves
into activeInstances for the re-dispatch to share.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_017Dc9sKTFBhQzMRk4jUoj4p"
```

---

### Task 9: Roster answers as observations; one status choke point

The roster's answer is offered to each instance as it lands, stamped with the time the query started, and merges with hook observations. `adoptRosterStatus` becomes `adoptClaudeStatus`, which reads the instance's merged observation; both status paths keep calling it. Output no longer promotes a session whose status Claude reported, and output on a Prompting session asks the roster, because answering a prompt fires no hook.

**Files:**
- Modify: `app/events.go`, `app/app.go`
- Create test: `app/claude_status_test.go`
- Modify test: `app/roster_status_test.go`

- [ ] **Step 1: Write the failing tests**

Create `app/claude_status_test.go`:

```go
package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/hooks"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// deliverRoster lands a roster answer the way a finished query does,
// stamped now.
func deliverRoster(m *home, entries map[string]session.RosterEntry) {
	m.Update(rosterReadyMsg{entries: entries, at: time.Now()})
}

// failRoster lands a failed roster query.
func failRoster(m *home) {
	m.Update(rosterReadyMsg{err: errAssertRoster, at: time.Now()})
}

// applyHookEvents feeds events to inst as its first scan after launch
// would, under the launch ID its hooks folder holds.
func applyHookEvents(t *testing.T, inst *session.Instance, events ...hooks.Event) {
	t.Helper()
	id, err := os.ReadFile(filepath.Join(session.SubagentHooksDir(inst.ConfigDir, inst.Title), "launch-id"))
	require.NoError(t, err, "the instance must have launched with hooks")
	require.True(t, inst.ApplyHookScan(session.HookScanResult{LaunchID: string(id), Replayed: true, Events: events}))
}

// A query that started before a Stop and landed after it must not undo
// the Stop: its busy is older than the hook's Ready.
func TestRosterAnswerOlderThanHookEventIsDropped(t *testing.T) {
	inst := startedInstanceWithProgram(t, "stale-roster", "claude", "x")
	m := homeWithAppState(t)
	m.list.AddInstance(inst)
	queryStarted := time.Now()
	applyHookEvents(t, inst, hooks.Event{Name: hooks.EventStop, HasTasks: true, At: queryStarted.Add(100 * time.Millisecond)})
	m.applyClaudeStatus(inst)
	require.Equal(t, session.Ready, inst.GetStatus())

	m.Update(rosterReadyMsg{entries: rosterFor(inst, session.RosterStatusBusy), at: queryStarted})

	assert.Equal(t, session.Ready, inst.GetStatus())
}

// A lead that stops while waiting for a teammate's reply, then picks it
// up with no hook event, is corrected by the next roster answer.
func TestNewerRosterAnswerCorrectsIntermediateStop(t *testing.T) {
	inst := startedInstanceWithProgram(t, "mid-stop", "claude", "x")
	m := homeWithAppState(t)
	m.list.AddInstance(inst)
	applyHookEvents(t, inst, hooks.Event{Name: hooks.EventStop, HasTasks: true, At: time.Now().Add(-time.Second)})
	m.applyClaudeStatus(inst)
	require.Equal(t, session.Ready, inst.GetStatus())

	deliverRoster(m, rosterFor(inst, session.RosterStatusBusy))

	assert.Equal(t, session.Running, inst.GetStatus())
}

func TestFailedRosterKeepsHookStatus(t *testing.T) {
	inst := startedInstanceWithProgram(t, "hook-survives", "claude", "x")
	m := homeWithAppState(t)
	m.list.AddInstance(inst)
	applyHookEvents(t, inst, hooks.Event{Name: hooks.EventPermissionRequest, ToolName: "Bash", At: time.Now().Add(-time.Second)})
	m.applyClaudeStatus(inst)

	failRoster(m)

	assert.Equal(t, session.Prompting, inst.GetStatus())
	assert.Equal(t, "permission: Bash", inst.WaitReason())
}

// Claude's report owns the status: output alone (a repaint on a focus
// change) must not promote a reported Ready session to Running.
func TestReportedReadyNotPromotedByOutput(t *testing.T) {
	inst := startedInstanceWithProgram(t, "reported-ready", "claude", "x")
	m := homeWithAppState(t)
	m.list.AddInstance(inst)
	m.splitPane.SetSize(100, 40)
	m.splitPane.SetInstance(inst)
	applyHookEvents(t, inst, hooks.Event{Name: hooks.EventStop, HasTasks: true, At: time.Now()})
	m.applyClaudeStatus(inst)
	require.Equal(t, session.Ready, inst.GetStatus())

	m.Update(paneDirtyMsg{session: inst.Pane().TmuxSessionName()})

	assert.Equal(t, session.Ready, inst.GetStatus())
}

// Answering a prompt makes output but fires no hook, so output on a
// Prompting session asks the roster, no more often than
// promptingRosterSpacing.
func TestPromptingOutputQueriesRosterSpaced(t *testing.T) {
	inst := startedInstanceWithProgram(t, "prompt-roster", "claude", "x")
	m := homeWithAppState(t)
	m.list.AddInstance(inst)
	m.splitPane.SetSize(100, 40)
	m.splitPane.SetInstance(inst)
	require.NoError(t, inst.TransitionTo(session.Prompting))

	m.Update(paneDirtyMsg{session: inst.Pane().TmuxSessionName()})
	require.True(t, m.gate(gateRoster).inFlight, "output on a Prompting session queries the roster")

	m.gate(gateRoster).inFlight = false
	m.Update(paneDirtyMsg{session: inst.Pane().TmuxSessionName()})
	assert.False(t, m.gate(gateRoster).inFlight, "a second query inside promptingRosterSpacing is not dispatched")

	m.gate(gateRoster).last = time.Now().Add(-promptingRosterSpacing)
	m.Update(paneDirtyMsg{session: inst.Pane().TmuxSessionName()})
	assert.True(t, m.gate(gateRoster).inFlight)
}

// A reported status also retires the re-detection chain: the ladder
// re-samples only because one content hash cannot tell "still working"
// from "just finished", and Claude's report says which it is.
func TestReportedStatusSuppressesRedetect(t *testing.T) {
	inst := startedInstanceWithProgram(t, "hook-redetect", "claude", "working...")
	t.Setenv("LOOM_PANE_RENDERER", "")
	m := homeWithAppState(t)
	m.list.AddInstance(inst)
	applyHookEvents(t, inst, hooks.Event{Name: hooks.EventPermissionRequest, ToolName: "Bash", At: time.Now()})

	_, follow := m.Update(statusDetectedMsg{instance: inst, updated: true})

	assert.Equal(t, session.Prompting, inst.GetStatus())
	assert.Equal(t, "permission: Bash", inst.WaitReason())
	assert.Nil(t, follow)
}
```

In `app/roster_status_test.go`, the tests set `m.roster` by hand and expect the next status event to read it. Roster answers now reach instances when they land, so deliver them instead:

```bash
perl -pi -e 's/^(\t)m\.roster = (rosterFor|rosterWithReason)\((.*)\)$/$1deliverRoster(m, $2($3))/' app/roster_status_test.go
```

Then, by hand:
- In `TestRosterAbsentFallsBackToScraper`, delete the line `m.roster = nil`.
- In `TestRosterWaitReasonClearedWhenRosterGoesAway`, replace the line `m.roster = nil` with `failRoster(m)`.
- Leave `TestRosterReadyMsgStoresEntries` and `TestRosterReadyMsgErrorClearsEntries` unchanged: `m.roster` still holds the latest answer.

- [ ] **Step 2: Run the tests to see them fail**

Run: `CGO_ENABLED=0 go test ./app/ -run 'Roster|Reported|Prompting|FailedRoster'`
Expected: compile errors `unknown field at in struct literal of type rosterReadyMsg`, `m.applyClaudeStatus undefined`, `undefined: promptingRosterSpacing`.

- [ ] **Step 3: Stamp roster answers in `app/events.go`**

Replace the `rosterReadyMsg` type:

```go
type rosterReadyMsg struct {
	entries map[string]session.RosterEntry
	err     error
}
```

with:

```go
type rosterReadyMsg struct {
	entries map[string]session.RosterEntry
	err     error
	// at is when the query started, which is when its answer was true.
	// Each instance's observation is stamped with it (see
	// session.Instance.ObserveRoster), so an answer from before a hook
	// event loses to that event however late it lands.
	at time.Time
}
```

In `rosterQueryCmd`, replace:

```go
	return func() tea.Msg {
		entries, err := session.QueryClaudeRoster(program, internalexec.Default{})
		return rosterReadyMsg{entries: entries, err: err}
	}
```

with:

```go
	return func() tea.Msg {
		at := time.Now()
		entries, err := session.QueryClaudeRoster(program, internalexec.Default{})
		return rosterReadyMsg{entries: entries, err: err, at: at}
	}
```

- [ ] **Step 4: Replace `adoptRosterStatus` with `adoptClaudeStatus` and add the helpers**

In `app/events.go`, replace the whole `adoptRosterStatus` function and its doc comment with:

```go
// adoptClaudeStatus is the single place a Claude session's reported status
// is applied to an instance. It returns the instance's merged hook and
// roster observation (see session.Instance.ClaudeStatus) and, as a side
// effect, records Claude's reason for blocking so the card can render it.
//
// The reason lives exactly as long as the reported wait: any other
// outcome clears it. Both status paths (statusDetectedMsg and
// metadataReadyMsg) must go through here, and so must applyClaudeStatus;
// duplicating the set/clear at each call site is how they drift apart,
// which is the lockstep hazard called out in CLAUDE.md.
func (m *home) adoptClaudeStatus(inst *session.Instance) (session.Status, bool) {
	if inst == nil {
		return session.Ready, false
	}
	status, reason, ok := inst.ClaudeStatus()
	if ok && status == session.Prompting {
		inst.SetWaitReason(reason)
	} else {
		inst.SetWaitReason("")
	}
	return status, ok
}

// applyClaudeStatus moves inst to the status its hooks or the roster last
// reported, for a change that arrived outside the two status paths: a hook
// scan or a roster answer. It does nothing for an instance the status
// pipelines may not drive (statusEligible), and clears a stale wait reason
// when neither source has an opinion.
func (m *home) applyClaudeStatus(inst *session.Instance) {
	if !statusEligible(inst) {
		return
	}
	target, ok := m.adoptClaudeStatus(inst)
	if !ok {
		return
	}
	if err := inst.TransitionTo(target); err != nil {
		log.For("app").Warn("claude_status.transition_failed", "instance", inst.Title, "to", target.String(), "err", err.Error())
	}
}

// observeRoster offers the roster in m.roster to every active Claude
// instance, stamped with at, and moves any whose status changed. An
// instance the roster has no opinion on (a failed query leaves m.roster
// nil) loses a roster-sourced status but keeps a hook-sourced one.
func (m *home) observeRoster(at time.Time) {
	changed := false
	for _, inst := range m.activeInstances() {
		if !session.IsClaudeProgram(inst.Program()) {
			continue
		}
		status, reason, ok := m.rosterStatusFor(inst)
		if inst.ObserveRoster(status, reason, ok, at) {
			changed = true
			m.applyClaudeStatus(inst)
		}
	}
	if changed {
		m.updateTabBarStatuses()
	}
}

// promptingRosterSpacing bounds how often a Prompting session's output
// can trigger a roster query (see paneDirtyMsg). Answering a prompt makes
// output but fires no hook, and the roster reports busy at once.
const promptingRosterSpacing = 500 * time.Millisecond

// maybeRosterQuerySoon dispatches a roster query ahead of the roster's own
// cadence, but at most once per promptingRosterSpacing and never while one
// is in flight. A request() would not do: a Prompting session the roster
// has no opinion on would then query back to back for as long as it
// produces output.
func (m *home) maybeRosterQuerySoon() tea.Cmd {
	g := m.gate(gateRoster)
	if !g.due(time.Now(), promptingRosterSpacing) {
		return nil
	}
	g.expedite()
	return m.maybeRosterQuery(m.activeInstances())
}
```

Update the doc comment of `rosterStatusFor`: replace its first sentence (`// rosterStatusFor returns Claude's authoritative status for inst, if it`) through `// published one, along with its stated reason for blocking (empty unless` with:

```go
// rosterStatusFor returns the roster's answer for inst from m.roster, if
// it has one, along with Claude's stated reason for blocking (empty unless
```

and replace `// The bool is false whenever Loom must fall back to the pane-content` / `// ladder:` with `// The bool is false when the roster has no opinion:`.

- [ ] **Step 5: Use them in `app/app.go`**

Rename the two call sites:

```bash
perl -pi -e 's/\bm\.adoptRosterStatus\(/m.adoptClaudeStatus(/g' app/app.go
```

In the `statusDetectedMsg` case, replace the comment block above `target, authoritative := m.adoptClaudeStatus(msg.instance)`:

```go
		// Claude publishes its own status, so prefer it over the pane
		// ladder below, which can only infer one from screen text. An
		// authoritative answer also retires the re-detection chain: the
		// ladder re-samples because one content hash cannot distinguish
		// "still working" from "just finished", but the roster says which
		// it is, and the next health tick refreshes it.
```

with:

```go
		// Claude reports its own status, through its hooks and the roster,
		// so prefer it over the pane ladder below, which can only infer one
		// from screen text. A reported status also retires the
		// re-detection chain: the ladder re-samples because one content
		// hash cannot distinguish "still working" from "just finished",
		// but the report says which it is.
```

In the `metadataReadyMsg` case, replace the comment block above `if target, authoritative := m.adoptClaudeStatus(r.instance); authoritative {`:

```go
			// The roster applies on BOTH paths. The exclusion below is
			// specifically about r.updated/r.hasPrompt, which are zero for
			// emulator instances (no capture ran) and would fight the event
			// pipeline; the roster is a real freshly-queried value, so it is
			// safe here — and it is the only thing that corrects a session
			// that changes state while emitting no output at all (a long
			// silent tool call fires no quiet event to sample). It may be up
			// to one tick stale: rosterQueryCmd is dispatched in the same
			// batch as gatherMetadataCmd, so this reads the previous tick's
			// answer. TransitionTo still validates, so an illegal transition
			// is rejected rather than forced.
```

with:

```go
			// Claude's reported status applies on BOTH paths. The exclusion
			// below is specifically about r.updated/r.hasPrompt, which are
			// zero for emulator instances (no capture ran) and would fight
			// the event pipeline. Hook scans and roster answers already
			// moved the instance when they landed (applyClaudeStatus);
			// applying the report here again covers a move TransitionTo
			// refused then, and keeps the snapshot path in lockstep with
			// the event path. TransitionTo still validates, so an illegal
			// transition is rejected rather than forced.
```

Replace the `rosterReadyMsg` case:

```go
	case rosterReadyMsg:
		if msg.err != nil {
			// Debug, not warn: a missing daemon or an older CLI without
			// `agents --json` is a supported configuration, not a fault —
			// detection simply falls back to pane content. Dropping the
			// previous roster is deliberate; a stale snapshot would keep
			// driving transitions long after it stopped being true.
			log.DebugKV("app.roster.query_failed", "err", msg.err.Error())
			m.roster = nil
			return m, nil
		}
		m.roster = msg.entries
		return m, nil
```

with:

```go
	case rosterReadyMsg:
		if msg.err != nil {
			// Debug, not warn: a missing daemon or an older CLI without
			// `agents --json` is a supported configuration, not a fault —
			// detection simply falls back to hooks and pane content.
			// Dropping the previous roster is deliberate; a stale snapshot
			// would keep driving transitions long after it stopped being
			// true, which is also why observeRoster voids every
			// roster-sourced status below.
			log.DebugKV("app.roster.query_failed", "err", msg.err.Error())
			m.roster = nil
		} else {
			m.roster = msg.entries
		}
		m.observeRoster(msg.at)
		return m, nil
```

In the `paneDirtyMsg` case, replace:

```go
			st := inst.GetStatus()
			if st == session.Ready {
				if err := inst.TransitionTo(session.Running); err != nil {
					log.For("app").Warn("event.transition_failed", "instance", inst.Title, "to", "Running", "err", err.Error())
				}
				m.updateTabBarStatuses()
			}
			if selected != nil && inst == selected {
				if err := m.splitPane.UpdateAgent(selected); err != nil {
					return m, m.handleError(err)
				}
			}
			return m, nil
```

with:

```go
			// A Claude session whose hooks or roster reported a status is
			// exempt too: the report owns the status, and output alone
			// says nothing new.
			st := inst.GetStatus()
			if _, _, reported := inst.ClaudeStatus(); st == session.Ready && !reported {
				if err := inst.TransitionTo(session.Running); err != nil {
					log.For("app").Warn("event.transition_failed", "instance", inst.Title, "to", "Running", "err", err.Error())
				}
				m.updateTabBarStatuses()
			}
			var cmds []tea.Cmd
			// Answering a permission prompt makes output (the dialog goes
			// away) but fires no hook; the roster reports busy at once.
			if st == session.Prompting && session.IsClaudeProgram(inst.Program()) {
				cmds = append(cmds, m.maybeRosterQuerySoon())
			}
			if selected != nil && inst == selected {
				if err := m.splitPane.UpdateAgent(selected); err != nil {
					return m, m.handleError(err)
				}
			}
			return m, tea.Batch(cmds...)
```

- [ ] **Step 6: Run the app suite**

Run: `CGO_ENABLED=0 go test ./app/`
Expected: `ok`.

- [ ] **Step 7: Commit**

```bash
gofmt -w $(git ls-files '*.go' | grep -v '^vendor/')
git add app
git commit -m "feat(app): merge roster answers with hook events per instance

A roster answer reaches each Claude instance when it lands, stamped
with the query's start, and adoptClaudeStatus reads the merged result
on both status paths. Output no longer promotes a session whose status
Claude reported, and output on a Prompting session asks the roster,
since answering a prompt fires no hook.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_017Dc9sKTFBhQzMRk4jUoj4p"
```

---

### Task 10: Hook scans on output and quiet; status applied as scans land

Scans run within about 250ms of output and right after a pane goes quiet, which is when `Stop` and `PermissionRequest` arrive. A scan that changes a status moves the instance at once and asks the roster to confirm, which corrects a `Stop` that ended a turn but not the work.

**Files:**
- Modify: `app/hook_scan.go`, `app/app.go`
- Test: `app/hook_scan_test.go`, `app/status_redetect_test.go`, `app/events_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `app/hook_scan_test.go`:

```go
func TestHookScanOnOutputHonoursInterval(t *testing.T) {
	inst := startedInstanceWithProgram(t, "scan-dirty", "claude", "x")
	m := homeWithAppState(t)
	m.list.AddInstance(inst)
	m.splitPane.SetSize(100, 40)
	m.splitPane.SetInstance(inst)
	require.True(t, inst.HooksLaunched())

	m.Update(paneDirtyMsg{session: inst.Pane().TmuxSessionName()})
	require.True(t, m.gate(gateHookScan).inFlight, "output on a hooked session scans")

	m.gate(gateHookScan).inFlight = false
	m.Update(paneDirtyMsg{session: inst.Pane().TmuxSessionName()})
	assert.False(t, m.gate(gateHookScan).inFlight, "a second scan inside hookScanInterval is not dispatched")
}

func TestHookScanOnQuietIgnoresInterval(t *testing.T) {
	inst := startedInstanceWithProgram(t, "scan-quiet", "claude", "x")
	m := homeWithAppState(t)
	m.list.AddInstance(inst)
	require.NotNil(t, m.maybeHookScan(m.activeInstances()))

	m.Update(paneQuietMsg{session: inst.Pane().TmuxSessionName()})
	assert.True(t, m.gate(gateHookScan).pending, "a quiet during a scan asks for one more")

	m.gate(gateHookScan).inFlight, m.gate(gateHookScan).pending = false, false
	m.Update(paneQuietMsg{session: inst.Pane().TmuxSessionName()})
	assert.True(t, m.gate(gateHookScan).inFlight, "a quiet scans even inside hookScanInterval")
}

func TestHookScanStatusChangeMovesInstanceAndAsksRoster(t *testing.T) {
	inst := startedInstanceWithProgram(t, "scan-status", "claude", "x")
	m := homeWithAppState(t)
	m.list.AddInstance(inst)
	dir := session.SubagentHooksDir(inst.ConfigDir, inst.Title)
	name := fmt.Sprintf("%d-1.json", time.Now().UnixNano())
	require.NoError(t, os.WriteFile(filepath.Join(hooks.EventsDir(dir), name),
		[]byte(`{"hook_event_name":"PermissionRequest","tool_name":"Bash"}`), 0o600))

	cmd := m.maybeHookScan(m.activeInstances())
	require.NotNil(t, cmd)
	_, follow := m.Update(cmd())

	assert.Equal(t, session.Prompting, inst.GetStatus())
	assert.Equal(t, "permission: Bash", inst.WaitReason())
	require.NotNil(t, follow, "a status change asks the roster to confirm")
	assert.True(t, m.gate(gateRoster).inFlight)
}
```

Add `"github.com/stretchr/testify/assert"` to that file's imports if it is not there.

The quiet handler now also schedules a scan for a hooked Claude session, so its command can be a batch. Add this helper to `app/status_redetect_test.go`, below `runDetection`:

```go
// detectionFrom runs a quiet or redetect handler's command the way the
// runtime would, expanding a batch, and returns the status detection it
// scheduled. For a hooked Claude session the quiet handler also schedules
// a hook scan, so its command can be a batch.
func detectionFrom(t *testing.T, cmd tea.Cmd) statusDetectedMsg {
	t.Helper()
	require.NotNil(t, cmd)
	pending := []tea.Cmd{cmd}
	for len(pending) > 0 {
		c := pending[0]
		pending = pending[1:]
		if c == nil {
			continue
		}
		switch msg := c().(type) {
		case tea.BatchMsg:
			pending = append(pending, msg...)
		case statusDetectedMsg:
			return msg
		}
	}
	t.Fatal("command scheduled no status detection")
	return statusDetectedMsg{}
}
```

In `runDetection`, replace:

```go
	msg := cmd()
	detected, ok := msg.(statusDetectedMsg)
	require.True(t, ok, "detection cmd must return statusDetectedMsg, got %T", msg)
	require.NoError(t, detected.err)
```

with:

```go
	detected := detectionFrom(t, cmd)
	require.NoError(t, detected.err)
```

In `TestStatusDetectionConvergesToReadyAfterSettle` and `TestStatusDetectionSurfacesPromptAfterSettle`, replace each:

```go
	require.NotNil(t, cmd)
	msg := cmd()
	detected, ok := msg.(statusDetectedMsg)
	require.True(t, ok, "expected statusDetectedMsg, got %T", msg)
```

with:

```go
	detected := detectionFrom(t, cmd)
```

In `app/events_test.go`'s `TestPaneQuietRunsStatusDetection`, replace:

```go
	msg := cmd()
	detected, ok := msg.(statusDetectedMsg)
	require.True(t, ok, "detection cmd must return statusDetectedMsg, got %T", msg)
```

with:

```go
	detected := detectionFrom(t, cmd)
```

`TestQuietDuringLoadingSchedulesRedetect` uses a bash session, which has no hooks, so it stays as it is.

- [ ] **Step 2: Run the tests to see them fail**

Run: `CGO_ENABLED=0 go test ./app/ -run 'TestHookScanOn|TestHookScanStatusChange'`
Expected: `TestHookScanOnOutputHonoursInterval`, `TestHookScanOnQuietIgnoresInterval` and `TestHookScanStatusChangeMovesInstanceAndAsksRoster` fail (no scan dispatched; status not applied).

- [ ] **Step 3: Implement in `app/hook_scan.go`**

Replace:

```go
// hookScanInterval is the hook-event scan cadence, matching rosterInterval:
// the snapshot-path health tick fires every 500ms, far more often than
// hook events need collecting.
const hookScanInterval = 3 * time.Second
```

with:

```go
// hookScanInterval is the minimum time between hook scans. Scans are
// triggered by pane output and quiet as well as the health tick, so an
// event is read within about this long; a warm scan is one readdir per
// hooked instance.
const hookScanInterval = 250 * time.Millisecond
```

Replace `handleHookScan` with:

```go
// handleHookScan applies a scan. An instance whose Claude status changed
// moves to it at once, and any change also asks the roster to confirm:
// its answer, stamped after the event, corrects a Stop that ended a turn
// but not the work (a lead about to pick up a teammate's reply).
func (m *home) handleHookScan(msg hookScanMsg) tea.Cmd {
	changed := false
	for _, r := range msg.results {
		if r.err != nil {
			if errors.Is(r.err, hooks.ErrNoHooks) {
				// The normal state of a session launched without
				// hooks. For a hooked launch the folder vanished
				// mid-run, and its rows would otherwise stay frozen.
				r.instance.ForgetSubagentsWithoutHooks()
			} else {
				log.DebugKV("app.hook_scan.failed", "instance", r.instance.Title, "err", r.err.Error())
			}
			continue
		}
		st0, why0, ok0 := r.instance.ClaudeStatus()
		r.instance.ApplyHookScan(r.result)
		st1, why1, ok1 := r.instance.ClaudeStatus()
		if st0 != st1 || why0 != why1 || ok0 != ok1 {
			changed = true
			m.applyClaudeStatus(r.instance)
		}
	}
	if !changed {
		return nil
	}
	m.updateTabBarStatuses()
	m.gate(gateRoster).request()
	return m.maybeRosterQuery(m.activeInstances())
}
```

- [ ] **Step 4: Trigger scans in `app/app.go`**

Replace the `hookScanMsg` case:

```go
	case hookScanMsg:
		m.handleHookScan(msg)
		return m, nil
```

with:

```go
	case hookScanMsg:
		return m, m.handleHookScan(msg)
```

In the `paneDirtyMsg` case, directly after the Prompting roster block added in Task 9 (the `if st == session.Prompting && …` block), add:

```go
			// While Claude works, its spinner keeps output flowing, so this
			// reads a UserPromptSubmit within hookScanInterval.
			if inst.HooksLaunched() {
				cmds = append(cmds, m.maybeHookScan(m.activeInstances()))
			}
```

Replace the `paneQuietMsg` case:

```go
	case paneQuietMsg:
		inst := m.instanceForSession(msg.session)
		if !statusEligible(inst) {
			// A quiet that lands mid-Start (Loading) is this burst's only
			// settle signal — quiet never re-fires without new output, so
			// dropping it would leave the unconditional Running set by
			// Start/Resume uncorrected. Re-check after the start resolves.
			if inst != nil && inst.GetStatus() == session.Loading {
				return m, m.maybeRedetect(msg.session)
			}
			return m, nil
		}
		return m, statusDetectCmd(inst)
```

with:

```go
	case paneQuietMsg:
		inst := m.instanceForSession(msg.session)
		var scan tea.Cmd
		if inst != nil && inst.HooksLaunched() {
			// Stop and PermissionRequest arrive as output settles. This is
			// often a burst's last output, so it must scan even inside
			// hookScanInterval or while a scan is in flight: request().
			m.gate(gateHookScan).request()
			scan = m.maybeHookScan(m.activeInstances())
		}
		if !statusEligible(inst) {
			// A quiet that lands mid-Start (Loading) is this burst's only
			// settle signal — quiet never re-fires without new output, so
			// dropping it would leave the unconditional Running set by
			// Start/Resume uncorrected. Re-check after the start resolves.
			if inst != nil && inst.GetStatus() == session.Loading {
				return m, tea.Batch(scan, m.maybeRedetect(msg.session))
			}
			return m, scan
		}
		return m, tea.Batch(scan, statusDetectCmd(inst))
```

Update the comment above the health tick's `maybeHookScan` call. Replace:

```go
		// Subagent hook events, throttled like the roster (see
		// maybeHookScan). nil when not due, in flight, or no Claude
		// agent is live.
```

with:

```go
		// Hook events: the backstop behind the output and quiet triggers
		// (see maybeHookScan). nil when not due, in flight, or no Claude
		// agent is live.
```

- [ ] **Step 5: Run the app suite**

Run: `CGO_ENABLED=0 go test ./app/`
Expected: `ok`.

- [ ] **Step 6: Commit**

```bash
gofmt -w $(git ls-files '*.go' | grep -v '^vendor/')
git add app
git commit -m "feat(app): read hook events within a second and apply their status

Hook scans now run on pane output (every 250ms at most) and whenever a
pane goes quiet, which is when Stop and PermissionRequest arrive, with
the health tick as the backstop. A scan that changes a status moves the
instance at once and asks the roster to confirm.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_017Dc9sKTFBhQzMRk4jUoj4p"
```

---
### Task 11: Persist the conversation to resume (schema v7)

Saves the session ID and transcript path so a relaunch after a loom restart can still resume the right conversation.

**Files:**
- Modify: `session/storage.go`, `session/storage_migrate.go`, `session/instance.go`, `cmd/workspace_migrate.go`
- Test: `session/storage_migrate_test.go`, `session/claude_state_test.go`, `cmd/workspace_migrate_shape_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `session/storage_migrate_test.go`:

```go
// TestMigrate_V6UpgradesAddsClaudeSession verifies a v6 record migrates to
// v7 with no conversation recorded: the empty strings mean "resume with
// --continue", which is what a v6 loom did.
func TestMigrate_V6UpgradesAddsClaudeSession(t *testing.T) {
	raw := []byte(`{"schema_version":6,"title":"t","program":"claude","issue":3}`)

	data, err := Migrate(raw)
	require.NoError(t, err)
	assert.Equal(t, 7, data.SchemaVersion)
	assert.Empty(t, data.ClaudeSessionID)
	assert.Empty(t, data.ClaudeTranscriptPath)
	assert.Equal(t, 3, data.Issue)
}
```

Append to `session/claude_state_test.go`:

```go
func TestClaudeSession_PersistsThroughInstanceData(t *testing.T) {
	data := InstanceData{
		SchemaVersion:        CurrentSchemaVersion,
		Title:                "persisted",
		Program:              "claude",
		Status:               Paused,
		IsWorkspaceTerminal:  true,
		ClaudeSessionID:      "8c634184-0fe5-4b62-b437-8f364eeeefcc",
		ClaudeTranscriptPath: "/t/8c634184-0fe5-4b62-b437-8f364eeeefcc.jsonl",
	}
	inst, err := FromInstanceData(data, t.TempDir())
	require.NoError(t, err)

	id, transcript := inst.ClaudeSession()
	assert.Equal(t, data.ClaudeSessionID, id)
	assert.Equal(t, data.ClaudeTranscriptPath, transcript)
	again := inst.Snapshot()
	assert.Equal(t, data.ClaudeSessionID, again.ClaudeSessionID)
	assert.Equal(t, data.ClaudeTranscriptPath, again.ClaudeTranscriptPath)
}
```

In `cmd/workspace_migrate_shape_test.go`'s `TestMigrationInstance_MirrorsInstanceData_JSON`, replace the fixture's last line:

```go
		"issue": 42
	}`
```

with:

```go
		"issue": 42,
		"claude_session_id": "8c634184-0fe5-4b62-b437-8f364eeeefcc",
		"claude_transcript_path": "/home/u/.claude/projects/-wt/8c634184-0fe5-4b62-b437-8f364eeeefcc.jsonl"
	}`
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `CGO_ENABLED=0 go test ./session/ ./cmd/ -run 'TestMigrate_V6|TestClaudeSession_Persists|TestMigrationInstance'`
Expected: compile errors `unknown field ClaudeSessionID in struct literal of type InstanceData`; once those compile, the cmd tests fail because the mirror drops the new keys.

- [ ] **Step 3: Implement**

In `session/storage.go`, change `const CurrentSchemaVersion = 6` to `const CurrentSchemaVersion = 7`, and add after the `Issue` field of `InstanceData`:

```go
	// ClaudeSessionID and ClaudeTranscriptPath name the Claude
	// conversation a crash relaunch resumes with --resume: the session ID
	// and transcript of the latest parent SessionStart hook. Empty until
	// one is seen, which means --continue. Added in schema v7.
	ClaudeSessionID      string `json:"claude_session_id,omitempty"`
	ClaudeTranscriptPath string `json:"claude_transcript_path,omitempty"`
```

In `session/storage_migrate.go`, add after the `case 5:` block:

```go
		case 6:
			// v6 → v7: ClaudeSessionID and ClaudeTranscriptPath added.
			// Empty (no conversation recorded, resume with --continue) is
			// the correct default for pre-existing records — version stamp
			// only.
			data.SchemaVersion = 7
```

and in `Migrate`'s doc comment, replace `CacheTTL1h; v5→v6 adds Issue — all default correctly on their own` with `CacheTTL1h; v5→v6 adds Issue; v6→v7 adds ClaudeSessionID and ClaudeTranscriptPath — all default correctly on their own`.

In `session/instance.go`'s `Snapshot`, add after `Issue:               i.issue,`:

```go
		ClaudeSessionID:      i.claude.sessionID,
		ClaudeTranscriptPath: i.claude.transcriptPath,
```

In `FromInstanceData`, add after `issue:               data.Issue,`:

```go
		claude:              claudeState{sessionID: data.ClaudeSessionID, transcriptPath: data.ClaudeTranscriptPath},
```

In `cmd/workspace_migrate.go`, add after the `Issue` field of `migrationInstance`:

```go
	ClaudeSessionID      string                `json:"claude_session_id,omitempty"`
	ClaudeTranscriptPath string                `json:"claude_transcript_path,omitempty"`
```

- [ ] **Step 4: Run the suites**

Run: `CGO_ENABLED=0 go test ./session/... ./cmd/... ./app/...`
Expected: all `ok`.

- [ ] **Step 5: Commit**

```bash
gofmt -w $(git ls-files '*.go' | grep -v '^vendor/')
git add session cmd
git commit -m "feat(session): persist the Claude conversation to resume (schema v7)

InstanceData gains claude_session_id and claude_transcript_path, from
the latest parent SessionStart hook, so a relaunch after a loom restart
still resumes the right conversation. The workspace migrate mirror
carries them too.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_017Dc9sKTFBhQzMRk4jUoj4p"
```

---

### Task 12: Relaunch with `--resume <session_id>`

Crash recovery resumes the conversation loom was showing, instead of whatever `--continue` picks as the directory's most recent. It falls back to `--continue` when the ID is not a well-formed UUID (it goes into a shell command) or its transcript is gone (Claude deletes transcripts after `cleanupPeriodDays`, and `--resume` on a missing one exits at once).

**Files:**
- Modify: `session/agent/adapter.go`, `session/agent/claude.go`, `session/agent/aider.go`, `session/agent/gemini.go`, `session/agent/default.go`
- Modify: `session/agent_restart.go`, `session/subagent_hooks.go` (`recoveryLaunch`), `session/instance.go` (comments)
- Test: `session/agent/adapter_test.go`, `session/agent_restart_test.go`, `session/subagent_hooks_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `session/agent/adapter_test.go`:

```go
func TestApplyResumeFlag(t *testing.T) {
	const id = "8c634184-0fe5-4b62-b437-8f364eeeefcc"
	c := Claude()
	assert.Equal(t, "claude --resume "+id+" --model sonnet", c.ApplyResumeFlag("claude --model sonnet", id))
	assert.Equal(t, "/usr/bin/claude --resume "+id, c.ApplyResumeFlag("/usr/bin/claude", id))
	assert.Equal(t, "claude --continue", c.ApplyResumeFlag("claude --continue", id), "the user's own recovery flag wins")
	assert.Equal(t, "claude --resume other", c.ApplyResumeFlag("claude --resume other", id))
	assert.Equal(t, "claude", c.ApplyResumeFlag("claude", ""))
	for _, ad := range []Adapter{Aider(), Gemini(), Default()} {
		assert.Equal(t, "x --flag", ad.ApplyResumeFlag("x --flag", id), ad.Name())
	}
}
```

Append to `session/agent_restart_test.go`, adding `"os"`, `"path/filepath"` and `"github.com/stretchr/testify/require"` to its imports if missing:

```go
func TestBuildResumeCommand(t *testing.T) {
	const id = "8c634184-0fe5-4b62-b437-8f364eeeefcc"
	transcript := filepath.Join(t.TempDir(), id+".jsonl")
	require.NoError(t, os.WriteFile(transcript, []byte("{}\n"), 0o600))
	missing := filepath.Join(t.TempDir(), "gone.jsonl")

	assert.Equal(t, "claude --resume "+id, BuildResumeCommand("claude", id, transcript))
	assert.Equal(t, "claude --continue", BuildResumeCommand("claude", id, missing),
		"Claude deletes old transcripts, and --resume on a missing one exits at once")
	assert.Equal(t, "claude --continue", BuildResumeCommand("claude", "", transcript))
	assert.Equal(t, "claude --continue", BuildResumeCommand("claude", id, ""))
	assert.Equal(t, "claude --continue", BuildResumeCommand("claude", "x; rm -rf ~", transcript),
		"the ID goes into a shell command: only a UUID is accepted")
	assert.Equal(t, "claude --continue --model sonnet", BuildResumeCommand("claude --continue --model sonnet", id, transcript))
	assert.Equal(t, "aider", BuildResumeCommand("aider", id, transcript))
}
```

Append to `session/subagent_hooks_test.go`:

```go
func TestRecoveryLaunch_ResumesRecordedConversation(t *testing.T) {
	inst := hooksInstance(t, "claude")
	const id = "8c634184-0fe5-4b62-b437-8f364eeeefcc"
	transcript := filepath.Join(t.TempDir(), id+".jsonl")
	require.NoError(t, os.WriteFile(transcript, nil, 0o600))
	require.True(t, inst.ApplyHookScan(HookScanResult{LaunchID: "L", Replayed: true, Events: []hooks.Event{
		{Name: hooks.EventSessionStart, Source: "startup", SessionID: id, TranscriptPath: transcript, At: time.Now()},
	}}))

	launch, env := inst.recoveryLaunch()

	assert.Contains(t, launch, "--resume "+id)
	assert.NotContains(t, launch, "--continue")
	assert.Equal(t, InstanceEnv("claude --resume "+id, false, false), env)
	got, _ := inst.ClaudeSession()
	assert.Equal(t, id, got, "the relaunch's reset keeps the ID until its own SessionStart replaces it")
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `CGO_ENABLED=0 go test ./session/agent/ ./session/ -run 'TestApplyResumeFlag|TestBuildResumeCommand|TestRecoveryLaunch_Resumes'`
Expected: compile errors `c.ApplyResumeFlag undefined`, `undefined: BuildResumeCommand`.

- [ ] **Step 3: Add `ApplyResumeFlag` to the adapters**

In `session/agent/adapter.go`, add to the `Adapter` interface directly after `ApplyRecoveryFlag`:

```go
	// ApplyResumeFlag returns the program string with the agent's flag for
	// resuming the named conversation (e.g. "claude --resume <id>").
	// Returns the input unchanged when it already carries --continue or
	// --resume, when sessionID is empty, and for agents that cannot resume
	// a named conversation. sessionID must already be validated: it is
	// inserted into a shell command.
	ApplyResumeFlag(program, sessionID string) string
```

In `session/agent/claude.go`, add after `ApplyRecoveryFlag`:

```go
// ApplyResumeFlag inserts "--resume <sessionID>" after "claude". Returns
// program unchanged if --continue or --resume is already present, or if
// program or sessionID is empty.
func (claudeAdapter) ApplyResumeFlag(program, sessionID string) string {
	parts := strings.Fields(program)
	if len(parts) == 0 || sessionID == "" {
		return program
	}
	for _, p := range parts[1:] {
		if p == "--continue" || p == "--resume" || strings.HasPrefix(p, "--resume=") {
			return program
		}
	}
	return insertAfterCommand(program, "--resume "+sessionID)
}
```

In `session/agent/aider.go`, add after `ApplyRecoveryFlag`:

```go
// ApplyResumeFlag is a no-op for aider — it cannot resume a named
// conversation.
func (aiderAdapter) ApplyResumeFlag(program, _ string) string { return program }
```

In `session/agent/gemini.go`, add after its `ApplyRecoveryFlag`:

```go
// ApplyResumeFlag is a no-op for gemini — it cannot resume a named
// conversation.
func (geminiAdapter) ApplyResumeFlag(program, _ string) string { return program }
```

In `session/agent/default.go`, add after its `ApplyRecoveryFlag`:

```go
// ApplyResumeFlag implements Adapter. The fallback adapter never modifies
// the program.
func (defaultAdapter) ApplyResumeFlag(program, _ string) string { return program }
```

- [ ] **Step 4: Add `BuildResumeCommand` to `session/agent_restart.go`**

Replace its import block with:

```go
import (
	"os"
	"regexp"

	"github.com/aidan-bailey/loom/session/agent"
)
```

Add after `BuildRecoveryCommand`:

```go
// claudeSessionIDRe matches the UUIDs Claude uses as session IDs. The ID
// is inserted into a shell command, so anything else is refused.
var claudeSessionIDRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// BuildResumeCommand rewrites program to resume the recorded conversation:
// --resume <sessionID> when the ID is a well-formed Claude session ID and
// its transcript still exists, else BuildRecoveryCommand's --continue.
// The transcript check matters because Claude deletes transcripts after
// cleanupPeriodDays (30 by default), and --resume on a missing one exits
// at once, which the health tick would read as the agent dying again.
func BuildResumeCommand(program, sessionID, transcriptPath string) string {
	if claudeSessionIDRe.MatchString(sessionID) && transcriptPath != "" {
		if _, err := os.Stat(transcriptPath); err == nil {
			return defaultRegistry.Lookup(program).ApplyResumeFlag(program, sessionID)
		}
	}
	return BuildRecoveryCommand(program)
}
```

- [ ] **Step 5: Use it for every recovery launch**

In `session/subagent_hooks.go`, replace `recoveryLaunch`:

```go
func (i *Instance) recoveryLaunch() (launch string, env []string) {
	program, headroomProxy, cacheTTL1h := i.launchSpec()
	program = BuildRecoveryCommand(program)
	return i.launchProgram(program, true), InstanceEnv(program, headroomProxy, cacheTTL1h)
}
```

with:

```go
func (i *Instance) recoveryLaunch() (launch string, env []string) {
	program, headroomProxy, cacheTTL1h := i.launchSpec()
	sessionID, transcriptPath := i.ClaudeSession()
	program = BuildResumeCommand(program, sessionID, transcriptPath)
	return i.launchProgram(program, true), InstanceEnv(program, headroomProxy, cacheTTL1h)
}
```

and in its doc comment replace `(the bare program rewritten by BuildRecoveryCommand)` with `(the bare program rewritten by BuildResumeCommand: --resume <id> for the recorded conversation, else --continue)`.

In `session/instance.go`, update two comments that name the old flag:
- In the doc of `startFreshWithRecovery`, replace `The program is rewritten via BuildRecoveryCommand so supported agents resume` / `// their prior conversation (e.g. `claude --continue`).` with `The program is rewritten via BuildResumeCommand so Claude resumes` / `// its prior conversation (`claude --resume <id>`, or `--continue` when none is recorded).`
- In the doc of `CrashRestart`, replace `The program is modified with --continue for` / `// supported agents.` with `The program is modified with --resume <id>` / `// (or --continue) for Claude, via BuildResumeCommand.`

- [ ] **Step 6: Run the suites**

Run: `CGO_ENABLED=0 go test ./session/... ./app/...`
Expected: all `ok`.

- [ ] **Step 7: Commit**

```bash
gofmt -w $(git ls-files '*.go' | grep -v '^vendor/')
git add session
git commit -m "feat(session): resume the recorded conversation after a crash

Recovery relaunches now pass --resume <session_id> for the conversation
loom was showing, instead of --continue's most recent one in the
directory. They fall back to --continue when the ID is not a UUID or
its transcript is gone, since --resume on a deleted transcript exits
at once.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_017Dc9sKTFBhQzMRk4jUoj4p"
```

---

### Task 13: Cards show Claude's last message when stopped

A stopped session's card shows the end of what Claude said (its summary or question) instead of the screen's tail. The message is valid from its `Stop` until the next prompt or permission request; a permission dialog therefore still shows the live tail.

**Files:**
- Modify: `ui/card.go`
- Create test: `ui/card_message_test.go`

- [ ] **Step 1: Write the failing tests**

Create `ui/card_message_test.go`:

```go
package ui

import (
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/hooks"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMessageTailLines(t *testing.T) {
	msg := "# Summary\n\nDone:\n```go\nx := 1\n```\n- fixed #12\n## Next?\nShould I push?\n"
	assert.Equal(t, []string{"- fixed #12", "Next?", "Should I push?"}, MessageTailLines(msg, 3))
	assert.Equal(t, []string{"Should I push?"}, MessageTailLines(msg, 1))
	assert.Equal(t, []string{"#12 is fixed"}, MessageTailLines("#12 is fixed", 1), "an issue reference is not a heading")
	assert.Nil(t, MessageTailLines("```\n\n```", 2))
	assert.Nil(t, MessageTailLines("text", 0))

	got := MessageTailLines("\x1b]52;c;aGk=\x07pwn", 1)
	require.Len(t, got, 1)
	assert.NotContains(t, got[0], "\x1b", "model-written text must not reach the terminal as escapes")
}

func messageInstance(t *testing.T, events ...hooks.Event) *session.Instance {
	t.Helper()
	inst, err := session.NewInstance(session.InstanceOptions{Title: "lm", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	require.True(t, inst.ApplyHookScan(session.HookScanResult{LaunchID: "L", Replayed: true, Events: events}))
	return inst
}

func TestBuildCardData_LastMessageReplacesTailWhenStopped(t *testing.T) {
	inst := messageInstance(t, hooks.Event{Name: hooks.EventStop, HasTasks: true,
		LastAssistantMessage: "All tests pass.\nShould I push?", At: time.Now()})
	require.NoError(t, inst.TransitionTo(session.Ready))

	d := BuildCardData(inst, false, "", 2)
	assert.Equal(t, []string{"All tests pass.", "Should I push?"}, d.TailLines)

	require.NoError(t, inst.TransitionTo(session.Running))
	d = BuildCardData(inst, false, "", 2)
	assert.Empty(t, d.TailLines, "a working session shows the live tail (none here: no emulator)")
}

func TestBuildCardData_PermissionRequestShowsLiveTail(t *testing.T) {
	now := time.Now()
	inst := messageInstance(t,
		hooks.Event{Name: hooks.EventStop, HasTasks: true, LastAssistantMessage: "Done!", At: now},
		hooks.Event{Name: hooks.EventPermissionRequest, ToolName: "Bash", At: now.Add(time.Second)})
	require.NoError(t, inst.TransitionTo(session.Prompting))

	d := BuildCardData(inst, false, "", 2)
	assert.Empty(t, d.TailLines, "the stored message belongs to the previous turn; the dialog is on screen")
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `CGO_ENABLED=0 go test ./ui/ -run 'TestMessageTailLines|TestBuildCardData_LastMessage|TestBuildCardData_PermissionRequest'`
Expected: compile error `undefined: MessageTailLines`.

- [ ] **Step 3: Implement in `ui/card.go`**

In `BuildCardData`, replace:

```go
	if tailN > 0 {
		if screen, ok := inst.Pane().EmulatorScreen(); ok {
			d.TailLines = ContentTailLines(screen, tailN)
		}
	}
```

with:

```go
	if tailN > 0 {
		if msg, current := inst.LastMessage(); current && msg != "" &&
			d.Status != session.Running && d.Status != session.Loading {
			// What Claude said it did, or is asking, says more than the
			// screen's tail once the session stops.
			d.TailLines = MessageTailLines(msg, tailN)
		} else if screen, ok := inst.Pane().EmulatorScreen(); ok {
			d.TailLines = ContentTailLines(screen, tailN)
		}
	}
```

In the doc comment of `BuildCardData`, replace `snapshot-path instances simply render their status label instead of a tail.` with `snapshot-path instances simply render their status label instead of a tail. When Claude's last message is current and the session is not working, the tail is the end of that message instead, on either path.`

Add after `TailLines`:

```go
// MessageTailLines returns the last n lines of a message Claude wrote, for
// a card's tail: blank and code-fence lines are dropped, a heading's
// leading #s trimmed, and every line sanitized, since the text is
// model-written. Returns nil when nothing is left or n < 1.
func MessageTailLines(msg string, n int) []string {
	if n < 1 {
		return nil
	}
	var lines []string
	for _, l := range strings.Split(msg, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "```") {
			continue
		}
		if h := strings.TrimLeft(t, "#"); h != t && (h == "" || h[0] == ' ') {
			t = strings.TrimSpace(h)
		}
		if t = strings.TrimSpace(sanitizeCardText(t)); t != "" {
			lines = append(lines, t)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}
```

- [ ] **Step 4: Run the UI suite**

Run: `CGO_ENABLED=0 go test ./ui/...`
Expected: all `ok`.

- [ ] **Step 5: Commit**

```bash
gofmt -w $(git ls-files '*.go' | grep -v '^vendor/')
git add ui
git commit -m "feat(ui): show Claude's last message on a stopped session's card

Ready and Prompting-by-question cards now show the end of Claude's last
message, from its Stop hook, instead of the screen's tail. A permission
request makes it stale, so the dialog still shows, and working sessions
keep the live tail. It also gives snapshot-path cards a tail.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_017Dc9sKTFBhQzMRk4jUoj4p"
```

---

### Task 14: The fake claude fires loom's hooks; end-to-end test

`tools/fakeagent`'s claude persona now reads `--settings` and runs the registered hook commands with Claude-shaped payloads, and answers loom's roster query (`claude agents --json`) with an empty list. A sandboxed loom's status for it is therefore entirely hook-driven, and the e2e test proves it with two things only hooks can produce: the wait reason `permission: Bash`, and a last message the pane never shows.

**Files:**
- Create: `tools/fakeagent/hooks.go`, `tools/fakeagent/hooks_test.go`
- Modify: `tools/fakeagent/agent.go`, `tools/fakeagent/main.go`
- Modify: `e2e/e2e_test.go`

- [ ] **Step 1: Write the failing tests**

Create `tools/fakeagent/hooks_test.go`:

```go
package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session/hooks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseArgs(t *testing.T) {
	o := parseArgs([]string{"--append-system-prompt-file", "/c.md", "--settings", "/h/settings.json", "--resume", "abc"})
	assert.Equal(t, launchOptions{settings: "/h/settings.json", resume: "abc"}, o)
	assert.True(t, parseArgs([]string{"agents", "--json"}).agents)
	assert.Equal(t, launchOptions{}, parseArgs([]string{"--settings"}), "a flag with no value is ignored")
}

type recordedHook struct {
	command string
	payload map[string]any
}

func recordingEmitter(t *testing.T, got *[]recordedHook) *hookEmitter {
	t.Helper()
	cmds := map[string][]string{}
	for _, ev := range []string{"SessionStart", "UserPromptSubmit", "PermissionRequest", "Stop", "SessionEnd"} {
		cmds[ev] = []string{"hook-" + ev}
	}
	return &hookEmitter{
		commands:   cmds,
		sessionID:  "8c634184-0fe5-4b62-b437-8f364eeeefcc",
		transcript: "/t/8c.jsonl",
		cwd:        "/w",
		run: func(command string, payload []byte) error {
			var m map[string]any
			require.NoError(t, json.Unmarshal(payload, &m))
			*got = append(*got, recordedHook{command: command, payload: m})
			return nil
		},
	}
}

func TestRun_EmitsHooksLikeClaude(t *testing.T) {
	var got []recordedHook
	var out bytes.Buffer
	a := newFakeAgent(personaFor("claude"), strings.NewReader("work 0\nask\ny\nexit\n"), &out, t.TempDir())
	a.sleep = func(time.Duration) {}
	a.hooks = recordingEmitter(t, &got)

	a.run()

	var names []string
	for _, h := range got {
		names = append(names, h.payload["hook_event_name"].(string))
		assert.Equal(t, "8c634184-0fe5-4b62-b437-8f364eeeefcc", h.payload["session_id"])
		assert.Equal(t, "hook-"+h.payload["hook_event_name"].(string), h.command)
	}
	assert.Equal(t, []string{"SessionStart", "UserPromptSubmit", "Stop", "UserPromptSubmit", "PermissionRequest", "Stop", "SessionEnd"}, names)
	assert.Equal(t, "startup", got[0].payload["source"])
	assert.Equal(t, "fakeagent finished: work 0", got[2].payload["last_assistant_message"])
	assert.Equal(t, "Bash", got[4].payload["tool_name"])
	assert.Equal(t, "prompt_input_exit", got[6].payload["reason"])
}

func TestRun_NoHooksWithoutSettings(t *testing.T) {
	_, code, _ := runScript(t, "claude", "work 0\nexit\n")
	assert.Equal(t, 0, code, "a nil emitter fires nothing and breaks nothing")
}

// The emitter's payloads, run through loom's real hook command, come back
// out of loom's scan as the events loom maps to status.
func TestHookEmitter_ThroughLoomsHooks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hook commands need sh")
	}
	t.Setenv("TMPDIR", t.TempDir()) // transcripts go under os.TempDir()
	dir := filepath.Join(t.TempDir(), "hooks", "loom_x")
	_, err := hooks.Prepare(dir)
	require.NoError(t, err)
	e, err := newHookEmitter(launchOptions{settings: hooks.SettingsPath(dir)}, "/w")
	require.NoError(t, err)

	e.sessionStart()
	e.emit("Stop", map[string]any{"last_assistant_message": "done", "background_tasks": []any{}})

	res, err := hooks.Scan(hooks.Request{Dir: dir}, time.Now())
	require.NoError(t, err)
	require.Len(t, res.Events, 2)
	assert.Equal(t, hooks.EventSessionStart, res.Events[0].Name)
	assert.Equal(t, e.sessionID, res.Events[0].SessionID)
	assert.Equal(t, "startup", res.Events[0].Source)
	assert.Equal(t, "done", res.Events[1].LastAssistantMessage)
	assert.FileExists(t, e.transcript)
}

func TestHookEmitter_ResumeKeepsTheID(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	dir := filepath.Join(t.TempDir(), "hooks", "loom_x")
	_, err := hooks.Prepare(dir)
	require.NoError(t, err)
	const id = "8c634184-0fe5-4b62-b437-8f364eeeefcc"

	e, err := newHookEmitter(launchOptions{settings: hooks.SettingsPath(dir), resume: id}, "/w")
	require.NoError(t, err)

	assert.Equal(t, id, e.sessionID)
	assert.True(t, e.resumed)
}
```

Add to `e2e/e2e_test.go`:

```go
// sendToAgent types text into the focused session through the quick input
// bar and sends it.
func sendToAgent(t *testing.T, sb *devsandbox.Sandbox, text string) {
	t.Helper()
	require.NoError(t, sb.SendKeys("a"))
	// "a" is Lua-dispatched (see createSession); wait for the quick input
	// bar's own footer before typing into it.
	require.NoError(t, sb.WaitFor("Enter to send to agent", uiTimeout))
	require.NoError(t, sb.SendText(text))
	require.NoError(t, sb.SendKeys("Enter"))
}

// The fake claude persona fires loom's hooks the way Claude does, and
// answers the roster query with no sessions, so this status is entirely
// hook-driven: a wait reason only a PermissionRequest supplies, then
// Claude's last message on the stopped card, which the pane never shows.
func TestE2E_FakeClaudeHooksDriveStatus(t *testing.T) {
	sb := newSandbox(t, "fake-claude")
	startLoom(t, sb)
	createSession(t, sb, "hooked")

	sendToAgent(t, sb, "ask")
	require.NoError(t, sb.WaitFor("permission: Bash", uiTimeout))

	sendToAgent(t, sb, "y")
	require.NoError(t, sb.WaitFor("fakeagent finished: ask", uiTimeout))
}
```

- [ ] **Step 2: Run the unit tests to see them fail**

Run: `CGO_ENABLED=0 go test ./tools/fakeagent/`
Expected: compile errors `undefined: parseArgs`, `undefined: hookEmitter`, `a.hooks undefined`.

- [ ] **Step 3: Create `tools/fakeagent/hooks.go`**

```go
package main

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// launchOptions are the Claude flags fakeagent honours. Every other flag
// is ignored, as before.
type launchOptions struct {
	settings string // --settings: loom's hooks settings file
	resume   string // --resume: the conversation to continue
	agents   bool   // `claude agents …`: loom's roster query
}

func parseArgs(args []string) launchOptions {
	var o launchOptions
	if len(args) > 0 && args[0] == "agents" {
		o.agents = true
		return o
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--settings":
			if i+1 < len(args) {
				o.settings = args[i+1]
				i++
			}
		case "--resume":
			if i+1 < len(args) {
				o.resume = args[i+1]
				i++
			}
		}
	}
	return o
}

// hookEmitter plays the hook side of Claude Code: it runs the commands the
// --settings file registers for each event, with a payload shaped like
// Claude's on stdin (see session/hooks/testdata/probe-2.1.280). Its
// methods are safe on a nil emitter, which a launch without --settings
// gets, and then do nothing.
type hookEmitter struct {
	commands   map[string][]string // event name → shell commands
	sessionID  string
	transcript string
	cwd        string
	resumed    bool
	// run executes one hook command; tests replace it.
	run func(command string, payload []byte) error
}

type settingsFile struct {
	Hooks map[string][]struct {
		Hooks []struct {
			Type    string `json:"type"`
			Command string `json:"command"`
		} `json:"hooks"`
	} `json:"hooks"`
}

// newHookEmitter loads the hook commands from o.settings and names the
// conversation: o.resume's ID, or a new one. It creates an empty
// transcript, because loom only resumes a conversation whose transcript
// exists. Returns nil, nil when o has no --settings.
func newHookEmitter(o launchOptions, cwd string) (*hookEmitter, error) {
	if o.settings == "" {
		return nil, nil
	}
	data, err := os.ReadFile(o.settings)
	if err != nil {
		return nil, fmt.Errorf("read --settings: %w", err)
	}
	var s settingsFile
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse --settings: %w", err)
	}
	e := &hookEmitter{commands: map[string][]string{}, cwd: cwd, resumed: o.resume != "", run: runHookCommand}
	for event, matchers := range s.Hooks {
		for _, m := range matchers {
			for _, h := range m.Hooks {
				if h.Type == "command" {
					e.commands[event] = append(e.commands[event], h.Command)
				}
			}
		}
	}
	e.sessionID = o.resume
	if e.sessionID == "" {
		if e.sessionID, err = newSessionID(); err != nil {
			return nil, err
		}
	}
	dir := filepath.Join(os.TempDir(), "fakeagent-transcripts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("transcript dir: %w", err)
	}
	e.transcript = filepath.Join(dir, e.sessionID+".jsonl")
	f, err := os.OpenFile(e.transcript, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("transcript: %w", err)
	}
	_ = f.Close()
	return e, nil
}

func runHookCommand(command string, payload []byte) error {
	cmd := exec.Command("sh", "-c", command)
	cmd.Stdin = bytes.NewReader(payload)
	return cmd.Run()
}

// newSessionID returns a random version-4 UUID, the shape of Claude's
// session IDs (loom only resumes IDs of that shape).
func newSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("session id: %w", err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// sessionStart fires SessionStart, as startup or resume.
func (e *hookEmitter) sessionStart() {
	if e == nil {
		return
	}
	source := "startup"
	if e.resumed {
		source = "resume"
	}
	e.emit("SessionStart", map[string]any{"source": source})
}

// emit runs event's hooks with the fields every Claude payload carries
// plus extra. Errors are ignored, as Claude ignores a failing hook.
func (e *hookEmitter) emit(event string, extra map[string]any) {
	if e == nil {
		return
	}
	payload := map[string]any{
		"hook_event_name": event,
		"session_id":      e.sessionID,
		"transcript_path": e.transcript,
		"cwd":             e.cwd,
	}
	for k, v := range extra {
		payload[k] = v
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	for _, c := range e.commands[event] {
		_ = e.run(c, data)
	}
}
```

- [ ] **Step 4: Fire the hooks from `tools/fakeagent/agent.go`**

Add a field to `fakeAgent`, after `edits int`:

```go
	// hooks fires loom's hooks the way Claude does; nil without --settings.
	hooks *hookEmitter
```

In `run`, replace:

```go
func (a *fakeAgent) run() int {
	fmt.Fprintf(a.out, "fakeagent: %s persona\n%s\n", a.p.name, usage)
	for {
		fmt.Fprint(a.out, "> ")
		if !a.in.Scan() {
			return 0
		}
```

with:

```go
func (a *fakeAgent) run() int {
	a.hooks.sessionStart()
	fmt.Fprintf(a.out, "fakeagent: %s persona\n%s\n", a.p.name, usage)
	for {
		fmt.Fprint(a.out, "> ")
		if !a.in.Scan() {
			a.hooks.emit("SessionEnd", map[string]any{"reason": "other"})
			return 0
		}
```

In `exec`, replace:

```go
func (a *fakeAgent) exec(line string) (code int, done bool) {
	cmd, arg, _ := strings.Cut(line, " ")
	switch cmd {
	case "":
	case "work":
		a.work(arg)
	case "ask":
		a.await(a.p.pendingPrompt, "answered")
```

with:

```go
func (a *fakeAgent) exec(line string) (code int, done bool) {
	cmd, arg, _ := strings.Cut(line, " ")
	switch cmd {
	case "", "crash":
	case "exit":
		a.hooks.emit("SessionEnd", map[string]any{"reason": "prompt_input_exit"})
	default:
		// Every other line is a prompt: Claude fires UserPromptSubmit when
		// it arrives and Stop when the turn ends. The Stop's message is
		// never printed, so a card showing it proves it came from a hook.
		a.hooks.emit("UserPromptSubmit", map[string]any{"prompt": line})
		defer a.hooks.emit("Stop", map[string]any{
			"last_assistant_message": "fakeagent finished: " + line,
			"background_tasks":       []any{},
			"stop_hook_active":       false,
		})
	}
	switch cmd {
	case "":
	case "work":
		a.work(arg)
	case "ask":
		a.hooks.emit("PermissionRequest", map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": "true"}})
		a.await(a.p.pendingPrompt, "answered")
```

- [ ] **Step 5: Wire it up in `tools/fakeagent/main.go`**

Replace the file with:

```go
// Command fakeagent is a deterministic, token-free stand-in for an AI
// coding agent, used by loom's dev sandbox (tools/loomdev). The sandbox
// installs it under persona names (claude, aider); loom's adapter registry
// matches on the program's basename and applies the real adapter to it.
// Command-line flags are ignored except --settings, whose hooks it fires
// the way Claude does, and --resume. `fakeagent agents …` answers loom's
// roster query with no sessions, so the sandbox's status is hook-driven.
package main

import (
	"fmt"
	"os"
)

func main() {
	dir, err := os.Getwd()
	if err != nil {
		dir = "."
	}
	opts := parseArgs(os.Args[1:])
	if opts.agents {
		fmt.Println("[]")
		return
	}
	a := newFakeAgent(personaFor(os.Args[0]), os.Stdin, os.Stdout, dir)
	emitter, err := newHookEmitter(opts, dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fakeagent: hooks disabled: %v\n", err)
	}
	a.hooks = emitter
	os.Exit(a.run())
}
```

- [ ] **Step 6: Run the unit tests**

Run: `CGO_ENABLED=0 go test ./tools/fakeagent/ ./internal/testenv/`
Expected: `ok` for both. The `testenv` package checks that every package reaching `config` isolates loom's dirs; `session/hooks` does not reach `config`, so `tools/fakeagent` needs no `TestMain`.

- [ ] **Step 7: Run the e2e suite**

Run: `CGO_ENABLED=0 go test -tags e2e ./e2e/... -run 'TestE2E_FakeClaudeHooksDriveStatus|TestE2E_FakeAiderPromptSurfacesAsAwaitingInput' -v 2>&1 | tail -20`
Expected: both `PASS`. It needs `tmux`, `git` and `go` on `PATH`, and builds a sandboxed loom on a private tmux server, so it never touches your own sessions. On failure the test log prints the sandbox's last screen and log tail.

- [ ] **Step 8: Commit**

```bash
gofmt -w $(git ls-files '*.go' | grep -v '^vendor/')
git add tools e2e
git commit -m "test(fakeagent): fire loom's hooks like Claude; hook-driven e2e test

The fake claude persona runs the hook commands --settings registers,
with Claude-shaped payloads, honours --resume, and answers the roster
query with no sessions. An e2e test checks that a sandboxed loom shows
a wait reason and a last message only the hooks can supply.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_017Dc9sKTFBhQzMRk4jUoj4p"
```

---

### Task 15: Opt-in real-Claude contract test

Re-runs the probe's core against real Claude, so a CLI change that breaks an assumption the design rests on fails loudly: the payload fields, and the roster moving before a hook is stamped.

**Files:**
- Create: `session/claude_hooks_realclaude_test.go`

- [ ] **Step 1: Write the test**

```go
package session

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/session/hooks"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/require"
)

// TestRealClaude_HookStatusContract repeats the core of the 2026-09-23
// probe (docs/superpowers/specs/2026-09-23-claude-hook-events-design.md)
// against a real interactive Claude session on a private tmux server. It
// pins the payload fields the status mapping reads, and the ordering the
// newest-wins merge relies on: the roster has already moved when a hook
// is stamped. It spends a few cents of haiku usage and leaves entries in
// ~/.claude/projects, so it only runs with LOOM_TEST_REAL_CLAUDE=1 and
// never in CI.
func TestRealClaude_HookStatusContract(t *testing.T) {
	if os.Getenv("LOOM_TEST_REAL_CLAUDE") != "1" {
		t.Skip("set LOOM_TEST_REAL_CLAUDE=1 to run against real Claude (costs money)")
	}
	for _, bin := range []string{"tmux", "claude", "sh"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}

	root := t.TempDir()
	hooksDir := filepath.Join(root, "hooks")
	launchID, err := hooks.Prepare(hooksDir)
	require.NoError(t, err)
	work := filepath.Join(root, "work")
	require.NoError(t, os.MkdirAll(work, 0o700))
	cwd, err := filepath.EvalSymlinks(work) // the roster reports a resolved cwd
	require.NoError(t, err)

	ctx := context.Background()
	sock := fmt.Sprintf("loomhooks-%d", os.Getpid())
	tm := func(args ...string) string {
		t.Helper()
		out, err := tmux.CommandOnSocket(ctx, sock, args...).CombinedOutput()
		require.NoError(t, err, string(out))
		return string(out)
	}
	t.Cleanup(func() { _ = tmux.CommandOnSocket(ctx, sock, "kill-server").Run() })
	target := tmux.PaneTarget("probe")
	screen := func() string { return tm("capture-pane", "-p", "-t", target) }
	send := func(text string) {
		tm("send-keys", "-t", target, "-l", text)
		time.Sleep(300 * time.Millisecond)
		tm("send-keys", "-t", target, "Enter")
	}

	tm("new-session", "-d", "-s", "probe", "-x", "160", "-y", "45", "-c", work)
	tm("send-keys", "-t", target, fmt.Sprintf("claude --model haiku --settings '%s'", hooks.SettingsPath(hooksDir)), "Enter")
	const trustDialog = "trust this folder"
	waitForRealClaude(t, 60*time.Second, func() bool {
		s := screen()
		return strings.Contains(s, trustDialog) || strings.Contains(s, "Claude Code")
	})
	if s := screen(); strings.Contains(s, trustDialog) {
		// "No, exit" is the default for folders under a temp dir.
		if strings.Contains(s, "❯ No, exit") {
			tm("send-keys", "-t", target, "Down")
		}
		tm("send-keys", "-t", target, "Enter")
	}
	waitForRealClaude(t, 60*time.Second, func() bool {
		s := screen()
		return strings.Contains(s, "Claude Code") && !strings.Contains(s, trustDialog)
	})
	time.Sleep(2 * time.Second) // let the input box take focus

	// Sample the roster continuously, stamping each sample with its start.
	type rosterSample struct {
		at     time.Time
		status RosterStatus
		listed bool
	}
	var (
		mu      sync.Mutex
		samples []rosterSample
		wg      sync.WaitGroup
	)
	quit := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-quit:
				return
			default:
			}
			at := time.Now()
			entries, err := QueryClaudeRoster("claude", internalexec.Default{})
			e, ok := entries[cwd]
			mu.Lock()
			samples = append(samples, rosterSample{at: at, status: e.Status, listed: err == nil && ok})
			mu.Unlock()
		}
	}()
	stopPoller := sync.OnceFunc(func() { close(quit); wg.Wait() })
	t.Cleanup(stopPoller)

	var seen []hooks.Event
	cold := true
	collect := func() {
		res, err := hooks.Scan(hooks.Request{Dir: hooksDir, Cold: cold}, time.Now())
		require.NoError(t, err)
		require.Equal(t, launchID, res.LaunchID)
		if res.Replayed {
			seen = nil
		}
		cold = false
		seen = append(seen, res.Events...)
	}
	parent := func(name string) []hooks.Event {
		var out []hooks.Event
		for _, e := range seen {
			if e.Name == name && e.AgentID == "" {
				out = append(out, e)
			}
		}
		return out
	}

	// Startup names the conversation.
	waitForRealClaude(t, 30*time.Second, func() bool { collect(); return len(parent(hooks.EventSessionStart)) == 1 })
	start := parent(hooks.EventSessionStart)[0]
	require.Equal(t, "startup", start.Source)
	require.NotEmpty(t, start.SessionID)
	require.FileExists(t, start.TranscriptPath)

	// A plain turn: UserPromptSubmit, then a Stop carrying the reply.
	send("Automated test. Reply with exactly the word PONG and nothing else. Use no tools.")
	waitForRealClaude(t, 2*time.Minute, func() bool { collect(); return len(parent(hooks.EventStop)) == 1 })
	firstStop := parent(hooks.EventStop)[0]
	require.Contains(t, firstStop.LastAssistantMessage, "PONG")
	require.Len(t, parent(hooks.EventUserPromptSubmit), 1)
	time.Sleep(time.Second) // let the roster poller sample the idle session

	// A permission prompt, answered.
	secondPrompt := time.Now()
	send("Automated test. Use the Bash tool exactly once to run: true  Then reply DONE.")
	waitForRealClaude(t, 2*time.Minute, func() bool { collect(); return len(parent(hooks.EventPermissionRequest)) == 1 })
	perm := parent(hooks.EventPermissionRequest)[0]
	require.Equal(t, "Bash", perm.ToolName)
	time.Sleep(time.Second) // let the roster poller sample the wait
	approved := time.Now()
	tm("send-keys", "-t", target, "Enter")
	waitForRealClaude(t, 2*time.Minute, func() bool { collect(); return len(parent(hooks.EventStop)) == 2 })

	// /clear ends the conversation and starts a new one.
	send("/clear")
	waitForRealClaude(t, 30*time.Second, func() bool { collect(); return len(parent(hooks.EventSessionStart)) == 2 })
	cleared := parent(hooks.EventSessionStart)[1]
	require.Equal(t, "clear", cleared.Source)
	require.NotEqual(t, start.SessionID, cleared.SessionID)

	// The status mapping over everything seen ends Ready, naming the new
	// conversation.
	var s claudeState
	for _, ev := range seen {
		s.applyEvent(ev)
	}
	require.Equal(t, "Ready", describe(s))
	require.Equal(t, cleared.SessionID, s.sessionID)

	// The roster leads the hooks: a query that started after a hook was
	// stamped already reports what the hook reported.
	stopPoller()
	mu.Lock()
	defer mu.Unlock()
	requireRoster := func(from, to time.Time, want RosterStatus, what string) {
		t.Helper()
		n := 0
		for _, sm := range samples {
			if !sm.listed || !sm.at.After(from) || !sm.at.Before(to) {
				continue
			}
			n++
			require.Equal(t, want, sm.status,
				"%s: a roster query that started %s after the hook reported %v; the newest-wins merge assumes the roster has already moved",
				what, sm.at.Sub(from), sm.status)
		}
		require.NotZero(t, n, "%s: no roster sample in the window", what)
	}
	requireRoster(firstStop.At, secondPrompt, RosterStatusIdle, "after Stop")
	requireRoster(perm.At, approved, RosterStatusWaiting, "after PermissionRequest")
}

func waitForRealClaude(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}
```

- [ ] **Step 2: Confirm it is skipped by default and compiles**

Run: `CGO_ENABLED=0 go test ./session/ -run TestRealClaude_HookStatusContract -v 2>&1 | tail -3`
Expected: `--- SKIP: TestRealClaude_HookStatusContract` with the "costs money" message.

- [ ] **Step 3: Run it for real, if you have Claude and accept the cost**

Run: `LOOM_TEST_REAL_CLAUDE=1 CGO_ENABLED=0 go test ./session/ -run TestRealClaude_HookStatusContract -timeout 15m -v 2>&1 | tail -5`
Expected: `--- PASS`. A failure in `requireRoster` means the roster no longer leads the hooks, and the no-grace-window merge rule (Task 4) must be revisited before merging. Report it rather than loosening the test.

- [ ] **Step 4: Commit**

```bash
gofmt -w $(git ls-files '*.go' | grep -v '^vendor/')
git add session/claude_hooks_realclaude_test.go
git commit -m "test(session): opt-in contract test for Claude's status hooks

Repeats the core of the live probe against real Claude: the payload
fields the status mapping reads, /clear's new session ID, and the
roster reporting each status before the hook's timestamp, which the
no-grace-window merge relies on. Runs only with LOOM_TEST_REAL_CLAUDE=1.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_017Dc9sKTFBhQzMRk4jUoj4p"
```

---

### Task 16: Documentation

**Files:**
- Modify: `CLAUDE.md`, `USAGE.md`, `session/claude_roster.go`, `app/events.go`, `app/app.go`, `session/instance.go`, `ui/card.go` (comments only)

- [ ] **Step 1: Correct the roster cost and the wait-reason comments in code**

In `session/instance.go`, replace the `waitReason` field comment:

```go
	// waitReason is Claude's own account of what this session is blocked
	// on ("sandbox request", "dialog open"), taken from the agent
	// roster's waitingFor. Only ever set while the roster is the one
	// driving a Prompting status, and cleared the moment it is not, so a
	// dismissed dialog cannot leave a label behind. Empty for non-Claude
	// agents and whenever the scraper is deciding. Ephemeral: never
	// serialized (absent from InstanceData).
```

with:

```go
	// waitReason is Claude's own account of what this session is blocked
	// on ("permission: Bash" from a PermissionRequest hook, "sandbox
	// request" from the roster's waitingFor). Only ever set while Claude's
	// report drives a Prompting status (adoptClaudeStatus), and cleared
	// the moment it does not, so a dismissed dialog cannot leave a label
	// behind. Empty for non-Claude agents and whenever the scraper is
	// deciding. Ephemeral: never serialized (absent from InstanceData).
```

In `ui/card.go`, replace the `WaitReason` field comment:

```go
	// WaitReason is Claude's own account of what a Prompting session is
	// blocked on ("sandbox request", "dialog open"), from the agent roster
	// via sanitizeCardText. Empty when unknown — non-Claude agents, and any
	// status the pane scraper rather than the roster decided.
```

with:

```go
	// WaitReason is Claude's own account of what a Prompting session is
	// blocked on ("permission: Bash", "sandbox request"), from its hooks
	// or the agent roster, via sanitizeCardText. Empty when unknown —
	// non-Claude agents, and any status the pane scraper decided.
```

In `session/claude_roster.go`, replace:

```go
// hung CLI cannot stall the health tick. Measured cost of a real call is
// ~380ms; this is generous headroom, not a target.
```

with:

```go
// hung CLI cannot stall the health tick. Measured cost of a real call is
// ~100ms on Claude Code 2.1.280 (~380ms on older builds); this is
// generous headroom, not a target.
```

In `app/events.go`, in the doc comment of `rosterInterval`, replace `and a ~380ms subprocess every 500ms keeps a claude process alive ~76% of` / `// the time purely to poll status` with `and a subprocess every 500ms would keep a claude process alive much of` / `// the time purely to poll status`, and append to that comment: `// Events that need a sooner answer ask for one (handleHookScan, maybeRosterQuerySoon).`

In `app/app.go`, in the health tick, replace `// One `claude agents --json` for the whole fleet (~380ms, off the` with `// One `claude agents --json` for the whole fleet (~100ms, off the`.

- [ ] **Step 2: Update `CLAUDE.md`**

Replace the `session/subagent/` bullet:

```markdown
- **`session/subagent/`** — Tracks the subagents and agent-team teammates a Claude session spawns, from Claude Code hook events. `hooks.go` writes the per-launch hooks folder (`settings.json`, `launch-id`, `events/`); `scan.go` turns new event files into compact `.ev` files and replays them when needed; `tracker.go` is the state machine; `event.go`/`meta.go` parse payloads and the `agent-<id>.meta.json` sidecars. No tmux, UI or app dependency. `session/subagent_hooks.go` wires it into `Instance`.
```

with:

```markdown
- **`session/hooks/`** — Loom's side of the Claude Code hooks it registers on every Claude launch. `hooks.go` writes the per-launch hooks folder (`settings.json`, `launch-id`, `events/`); `scan.go` turns new event files into compact `.ev` files, replays them when needed, and stamps each event's `At` from its file name; `event.go` parses payloads, keeping only the fields loom reads from each event. `testdata/probe-2.1.280/` holds the payloads of a live probe (see its README). No tmux, UI or app dependency.
- **`session/subagent/`** — Tracks the subagents and agent-team teammates a Claude session spawns, from `session/hooks` events. `tracker.go` is the state machine; `meta.go`/`readmeta.go` parse and read the `agent-<id>.meta.json` sidecars. No tmux, UI or app dependency. `session/subagent_hooks.go` and `session/hook_scan.go` wire both into `Instance`; `session/claude_state.go` derives the session's status, conversation and last message from the same events (see the Claude status gotcha).
```

In the "Claude's roster outranks the pane scraper" gotcha, make these replacements:
- `(`rosterQueryCmd`, ~380ms, off the Update goroutine)` → `(`rosterQueryCmd`, ~100ms on 2.1.280, off the Update goroutine)`
- `an unthrottled ~380ms subprocess there would keep a `claude` process alive ~76% of the time` → `an unthrottled subprocess there would keep a `claude` process alive much of the time`
- `Every throttled job (roster query, subagent scan, GitHub poll, and the split-ratio flush tick) is gated this way` → `Every throttled job (roster query, hook scan, GitHub poll, and the split-ratio flush tick) is gated this way`
- Replace the sentence `Both status paths (`statusDetectedMsg` on the event path, `metadataReadyMsg` on the snapshot path) consult it **before** the content ladder and must stay in lockstep — they do so by both calling **`adoptRosterStatus`**, the single choke point that applies the status *and* records `Instance.WaitReason`; never re-implement that pairing at a call site.` with `A roster answer is offered to each active Claude instance as it lands (`observeRoster`), stamped with the query's start time, and merges with hook observations under the newest-wins rule (next gotcha). Both status paths (`statusDetectedMsg` on the event path, `metadataReadyMsg` on the snapshot path) consult the instance's merged observation **before** the content ladder and must stay in lockstep — they do so by both calling **`adoptClaudeStatus`**, the single choke point that applies the status *and* records `Instance.WaitReason` (`applyClaudeStatus` uses it too); never re-implement that pairing at a call site.`
- `A failed query **clears** the roster rather than retaining it — stale statuses are worse than none.` → `A failed query **clears** the roster rather than retaining it — stale statuses are worse than none — and turns every roster-sourced observation into no opinion (`claudeState.rosterSilent`); a hook-sourced one stands.`
- `rides through `adoptRosterStatus` onto` → `rides through `adoptClaudeStatus` onto`

Directly after that gotcha (before `- **Subagent rows come from hooks, not transcripts.**`), insert:

```markdown
- **Claude status: hooks and roster, newest observation wins.** Each Claude instance holds one merged observation (`session/claude_state.go`, `claudeState`, guarded by `Instance.mu`): hook events (`SessionStart`, `UserPromptSubmit`, `PermissionRequest`, `Notification`, `Stop`, `SessionEnd`) and roster answers are both offered to it, each stamped with when it was *observed* — a hook event's `At` from its file name (`hooks.eventTime`), a roster answer's `rosterReadyMsg.at` taken before the subprocess starts — and the newer one wins whichever source it came from. There is deliberately **no grace window**: a live probe (spec `docs/superpowers/specs/2026-09-23-claude-hook-events-design.md`, payloads in `session/hooks/testdata/probe-2.1.280/`) showed the roster moving 0–90ms *before* the hook's timestamp, so an answer stamped after an event already reflects it; the opt-in `TestRealClaude_HookStatusContract` asserts that ordering. Rules that are easy to break: only parent events (no `agent_id`) count, except `PermissionRequest`, whose prompt shows in the parent; `Stop` ends a *turn*, not the work — a `Stop` listing a running `subagent` task is Running, and teammate and `shell` tasks never count (idle teammates stay listed as running); only `SessionStart` sets the session ID (a failed `--resume` sends a `SessionEnd` carrying the unknown ID); a roster answer with no opinion voids a roster-sourced status but never a hook-sourced one; a same-status roster answer keeps the hook's more specific reason (`permission: Bash`). Delivery: `maybeHookScan` (`gateHookScan`, 250ms) runs on pane output and, through `pollGate.request()` (which dispatches once more after an in-flight job lands), on pane quiet, when `Stop` and `PermissionRequest` arrive; a scan that changes a status applies it at once (`applyClaudeStatus`) and `request()`s a roster query to confirm it, which is what corrects a lead that resumes after a teammate reply with no event. Answering a prompt fires no hook, so output on a Prompting session queries the roster (`maybeRosterQuerySoon`, at most every `promptingRosterSpacing`). A reported status also exempts the session from `paneDirtyMsg`'s Ready→Running promotion. Without a working roster two gaps remain: an answered prompt stays Prompting until the next hook, and a lead resuming after a teammate reply shows Ready. A relaunch resumes the recorded conversation (`BuildResumeCommand`: `--resume <id>` when the ID is a UUID and its transcript exists, else `--continue`), and a stopped session's card shows the end of Claude's last message (`ui.MessageTailLines`), valid from its `Stop` until the next prompt or permission request. Tests: `session/claude_state_test.go` (including a replay of the whole probe), `app/claude_status_test.go`, `app/hook_scan_test.go`, `ui/card_message_test.go`.
```

In the "Subagent rows come from hooks, not transcripts." gotcha:
- `registering `SubagentStart`/`SubagentStop`/`TeammateIdle`/`Stop`/`SessionEnd`` → `registering `SubagentStart`/`SubagentStop`/`TeammateIdle`/`Stop`/`SessionEnd` (plus the status events of the previous gotcha)`
- ``maybeSubagentScan` (gated like `maybeRosterQuery`, via `gateSubagent`) collects them into `Instance`'s `subagent.Tracker`` → ``maybeHookScan` (via `gateHookScan`) collects them into `Instance`'s `subagent.Tracker` and `claudeState``
- `` `resetSubagentLaunch` (`session/subagent_hooks.go`) clears the tracker and warm flag`` → `` `resetHookLaunch` (`session/subagent_hooks.go`) clears the tracker, warm flag, Claude status and last message (keeping the conversation ID the relaunch resumes)``
- `` `claude_subagent_tracking` only decides whether a launch gets hooks; sessions launched with hooks keep being scanned only until their next launch.`` → `` `claude_subagent_tracking` only decides whether `Instance.Subagents` returns rows: every Claude launch that can take hooks gets them, because they also carry status, the conversation ID and the last message.``

In the "Pane updates are event-driven, not polled." gotcha, replace `see the Claude roster gotcha below` with `see the Claude roster and Claude status gotchas below`.

In "Persistent State":
- In the `config.json` bullet, replace `` `ClaudeSubagentTracking` (`*bool`, default on — launches Claude sessions with loom's subagent hooks;`` with `` `ClaudeSubagentTracking` (`*bool`, default on — shows the subagents and teammates loom's hooks track; every Claude launch gets the hooks regardless;``
- In the `instances` bullet, replace `(`issue`, since schema v6, links a session to the GitHub issue number it was created from)` with `(`issue`, since schema v6, links a session to the GitHub issue number it was created from; `claude_session_id`/`claude_transcript_path`, since v7, name the conversation a crash relaunch resumes with `--resume`)`
- In the `hooks/` bullet, replace `per-instance Claude hook folders for subagent tracking (see `session/subagent`)` with `per-instance Claude hook folders for status, resume and subagent tracking (see `session/hooks`)`

- [ ] **Step 3: Update `USAGE.md`**

In the rail-card paragraph, after `When Claude says why it is waiting, the reason replaces the generic phrase (`❯ sandbox request · 4m`).` add: ` When a Claude session stops, the tail shows the end of Claude's last message instead of the screen.`

In "### Subagent Tracking", replace:

```markdown
Loom shows the subagents and agent-team teammates a Claude session has spawned: a count on its rail card and rows on its overview card. It works by launching Claude with an extra `--settings` file that registers hooks for subagent events; your own hooks keep running alongside them.

- Toggle it with **Track Subagents** under `S` → Claude Preferences. It is on by default, and a change applies the next time a session launches or resumes.
- Turning it off doesn't clear the rows of sessions already launched with hooks: they keep being tracked until their next launch or resume.
```

with:

```markdown
Loom shows the subagents and agent-team teammates a Claude session has spawned: a count on its rail card and rows on its overview card. It works through an extra `--settings` file loom launches every Claude session with, which registers hooks; your own hooks keep running alongside them. The same hooks report the session's status, the conversation a crash relaunch resumes, and Claude's last message.

- Toggle the rows with **Track Subagents** under `S` → Claude Preferences. It is on by default and applies at once; the hooks stay installed either way, so turning it back on shows the current agents.
```

and replace:

```markdown
- Sessions launched before you enabled it aren't tracked until they are resumed. Sessions whose program already passes `--settings`, and sessions on Windows, are never tracked.
```

with:

```markdown
- Sessions whose program already passes `--settings`, and sessions on Windows, get no hooks: they show no subagent rows, and their status comes from Claude's session list and the screen.
```

- [ ] **Step 4: Check nothing still names the old identifiers**

Run: `git grep -nE 'adoptRosterStatus|gateSubagent|maybeSubagentScan|SubagentScanRequest|ApplySubagentScan|resetSubagentLaunch|prepareSubagentHooks' -- ':!docs/superpowers/specs' ':!docs/superpowers/plans'`
Expected: no output. Any hit is a comment the renames missed: reword it to the new name (`adoptClaudeStatus`, `gateHookScan`, `maybeHookScan`, `NextHookScan`, `ApplyHookScan`, `resetHookLaunch`, `prepareHooks`) and re-run.

- [ ] **Step 5: Commit**

```bash
git add CLAUDE.md USAGE.md session/claude_roster.go session/instance.go ui/card.go app/events.go app/app.go
git commit -m "docs: describe hook-driven Claude status, resume and last message

CLAUDE.md gains the Claude status gotcha (newest observation wins, no
grace window, Stop ends a turn) and the session/hooks package; USAGE.md
explains that the hooks are always installed and the setting only hides
subagent rows. The roster's cost is corrected to the measured ~100ms.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_017Dc9sKTFBhQzMRk4jUoj4p"
```

---

### Task 17: Final verification

- [ ] **Step 1: Formatting and vet**

Run: `test -z "$(gofmt -l $(git ls-files '*.go' | grep -v '^vendor/'))" && CGO_ENABLED=0 go vet ./...`
Expected: no output, exit 0.

- [ ] **Step 2: The whole suite**

Run: `CGO_ENABLED=0 go test ./... 2>&1 | grep -v '^ok' | grep -v 'no test files'`
Expected: no output (every package `ok`).

- [ ] **Step 3: The race detector**

Run: `CC=clang CGO_ENABLED=1 go test -race ./session/... ./app/... ./ui/... 2>&1 | grep -v '^ok' | grep -v 'no test files'`
Expected: no output. `claudeState` is only touched under `Instance.mu`, and scans and roster queries apply their results on the Update goroutine; a race report here means one of those rules was broken.

- [ ] **Step 4: End to end**

Run: `CGO_ENABLED=0 go test -tags e2e ./e2e/... 2>&1 | tail -5`
Expected: `ok`.

- [ ] **Step 5: See it in the real TUI (optional, recommended)**

Use the `loom-dev` skill (never run `./loom` in a loom pane): `go run ./tools/loomdev up`, then `go run ./tools/loomdev run`, create a session with the `fake-claude` profile, send it `ask` with `a`, and watch the rail show `❯ permission: Bash`. Answer `y` and the card shows `fakeagent finished: ask`. `go run ./tools/loomdev down` when done.

- [ ] **Step 6: Confirm the tree is clean**

Run: `git status --short`
Expected: no output; every task committed its work.
