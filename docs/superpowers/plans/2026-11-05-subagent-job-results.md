# Subagent Job Results Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. The owner chose inline execution (superpowers:executing-plans) for this plan.

**Goal:** A job submitted by a Claude Code subagent reports back to that subagent, resuming it if it has finished. Anything loom can't route goes to the session, naming the subagent.

**Architecture:** The loom mod reports each Bash call that runs `loom job submit` or `loom job run` (`POST /v1/calls`), before the command runs. The daemon matches a submit to the one reported call whose command holds its argv, and records that call's agent on the job (`sched.Record.Agent`). A job notice for an agent goes to the mod with `ModMessage.Agent`, and the mod hands it to the subagent with `$.session.send`. On `isDelivered: false` the daemon gives it to the session, labelled. A mod whose hello lacks `agents: true` gets the session's labelled copy. No command is ever rewritten.

**Tech Stack:** Go (core, sched, internal/modserver, internal/cli, ui), the loom mod's TypeScript module on Claude Code's function-hook API, testify.

**Spec:** `docs/superpowers/specs/2026-11-05-subagent-job-results-design.md`. Section numbers below (§n) are the spec's.

**Shape:** four packages, each one unit of work leaving the build green and ending in one commit. Inside a package, the numbered steps are checkpoints, not commits.

**Rules that apply throughout** (from the repo's CLAUDE.md files):
- Commit by path (`git commit -- <paths>`), never `-a`, `git add -A` or `.`.
- After every edit, check `git status --short` for a stray ` D <file>.tmp.<pid>.<hash>` entry (the Edit tool's temp file, caught by loom's `git add -N .`) and clear it with `git rm --cached`.
- Run heavy suites through the queue: `loom job run -d "<what>" -- <cmd>`. Quick package tests run directly.
- Tests run with `CGO_ENABLED=0`.
- gofmt only the files you touched: `gofmt -l <files>`.

---

## File map

| File | Package | Responsibility |
|---|---|---|
| `sched/job.go` | A | `Record.Agent` |
| `session/claudemod/claudemod.go` | A | `JobCallPattern`, the one source of the call pattern |
| `core/mod_calls.go` (new) | A | `ModCall`, `reportedCall`, `Model.ModCall`, `attribution` |
| `core/mod_calls_test.go` (new) | A | attribution tests |
| `core/mod.go` | A, B | `ModHello.Agents`, `ModHost.ModCall`, `modLink.agents`/`calls`; B: `ModMessage.Agent`, `modMessage.agent`, the hand-out in `ModInbox`, `modFailed`'s fallback |
| `core/loop_mod.go` | A | `Loop.ModCall` |
| `core/queue_ops.go` | A | `submitJob` records the agent |
| `core/queue_view.go` | A | `JobView.Agent`, `ShortAgent` |
| `internal/modserver/server.go`, `server_test.go`, `realclaude_test.go` | A, C | the `/calls` route; the fakes' `ModCall`; C: the real-Claude test |
| `docs/specs/protocol.md` | A | regenerated |
| `core/queue_notice.go`, `core/queue_notice_test.go` | B | the agent on the notice; `withSubmitter` and its lines; delivery tests |
| `session/claudemod/mod/hooks/register.ts` | C | call reports, `agents: true`, `sendToAgent` |
| `session/claudemod/claudemod_test.go`, `realclaude_test.go` | C | the pattern check; the validator's allowlist and hooks |
| `internal/cli/job.go`, `job_follow.go`, `job_list.go`, `job_test.go` | D | `resultRoute`, `continuesLine`, `ownerText` |
| `ui/work.go`, `ui/work_test.go` | D | `workOwner` |
| `session/loom_context_shared.md` | D | the context sentence (§9) |
| `core/CLAUDE.md`, `session/claudemod/CLAUDE.md`, `internal/modserver/CLAUDE.md` | D | rules |

---

## Package A: attribution

The daemon learns which call made each submit and records it on the job. Nothing is delivered differently yet.

**Files:**
- Modify: `sched/job.go` (Record), `session/claudemod/claudemod.go`, `core/mod.go`, `core/loop_mod.go`, `core/queue_ops.go`, `core/queue_view.go`, `internal/modserver/server.go`, `internal/modserver/server_test.go`, `internal/modserver/realclaude_test.go`, `docs/specs/protocol.md` (generated)
- Create: `core/mod_calls.go`, `core/mod_calls_test.go`

- [ ] **A1. Add the record field and the pattern constant**

In `sched/job.go`, inside `type Record struct`, directly after the `Owner` line:

```go
	Agent        string    `json:"agent,omitempty"` // the subagent (its Claude agentId) whose Bash call submitted it; "" for the session's main conversation or unknown
```

In `session/claudemod/claudemod.go`, after the `const ( … EnvLaunch … )` block:

```go
// JobCallPattern finds `loom job submit` or `loom job run` in a Bash command.
// The module reports each call whose command matches it, and the daemon
// counts its matches (core, attribution); register.ts carries the same source
// as a JavaScript regex literal, which TestModule_SpeaksThisProtocol checks.
const JobCallPattern = `\bloom\s+job\s+(submit|run)\b`
```

- [ ] **A2. Write the failing attribution tests**

Create `core/mod_calls_test.go`:

```go
package core

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// callModel is a queue model holding a started Claude session on launch L1
// whose mod (poller "p") said hello and can send to subagents.
func callModel(t *testing.T) *Model {
	t.Helper()
	withMod(t)
	m := wqModel(t)
	modInst(t, m, "owner")
	require.NoError(t, m.ModHello("L1", ModHello{Mod: 1, Poller: "p", Agents: true}))
	return m
}

// reportCall has L1's mod report c starting.
func reportCall(t *testing.T, m *Model, c ModCall) {
	t.Helper()
	require.NoError(t, m.ModCall("L1", "p", c, false))
}

// submitFromL1 submits argv from launch L1 and returns the job's agent.
func submitFromL1(t *testing.T, m *Model, argv ...string) string {
	t.Helper()
	sub := wqJob(t, argv...)
	sub.Launch = "L1"
	id, _, err := m.submitJob(sub)
	require.NoError(t, err)
	return wqRecord(t, m, id).Agent
}

// A submit takes its agent from the one reported call that holds its whole
// argv, and from nothing else (spec §5.2): no call, two calls, a call missing
// an argument, and the main loop's own call all leave the job the session's.
func TestSubmit_TakesItsAgentFromTheOneCallThatMadeIt(t *testing.T) {
	exit0 := "loom job submit -d x -- sh -c 'exit 0'"
	cases := []struct {
		name  string
		calls []ModCall
		want  string
	}{
		{"one subagent call", []ModCall{{ID: "c1", Agent: "a1", Command: exit0}}, "a1"},
		{"the main loop's call", []ModCall{{ID: "c1", Command: exit0}}, ""},
		{"no call", nil, ""},
		{"two calls could have made it", []ModCall{{ID: "c1", Agent: "a1", Command: exit0}, {ID: "c2", Agent: "a2", Command: exit0}}, ""},
		{"only one holds the argv", []ModCall{{ID: "c1", Agent: "a1", Command: "loom job run -- cargo test"}, {ID: "c2", Agent: "a2", Command: exit0}}, "a2"},
		{"an argument missing", []ModCall{{ID: "c1", Agent: "a1", Command: "loom job submit -- sh -c 'exit 1'"}}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := callModel(t)
			for _, call := range c.calls {
				reportCall(t, m, call)
			}
			assert.Equal(t, c.want, submitFromL1(t, m, "sh", "-c", "exit 0"))
		})
	}
}

// A call accounts for as many submits as its command runs, and no more: a
// third submit of the same command came from somewhere loom can't see.
func TestSubmit_ACallAccountsForAsManySubmitsAsItRuns(t *testing.T) {
	m := callModel(t)
	reportCall(t, m, ModCall{ID: "c1", Agent: "a1", Command: "loom job submit -- true && loom job submit -- true"})
	got := []string{submitFromL1(t, m, "true"), submitFromL1(t, m, "true"), submitFromL1(t, m, "true")}
	assert.Equal(t, []string{"a1", "a1", ""}, got)
}

// The job view carries the agent the record holds.
func TestSubmit_TheJobViewCarriesTheAgent(t *testing.T) {
	m := callModel(t)
	reportCall(t, m, ModCall{ID: "c1", Agent: "a1b2c3d4e5f6", Command: "loom job submit -- true"})
	submitFromL1(t, m, "true")
	q := m.Queue()
	require.Len(t, q.Jobs, 1)
	assert.Equal(t, "a1b2c3d4e5f6", q.Jobs[0].Agent)
	assert.Equal(t, "a1b2c3d", ShortAgent(q.Jobs[0].Agent))
}

func TestModCall_AnEndedCallMatchesNothing(t *testing.T) {
	m := callModel(t)
	c := ModCall{ID: "c1", Agent: "a1", Command: "loom job submit -- true"}
	reportCall(t, m, c)
	require.NoError(t, m.ModCall("L1", "p", ModCall{ID: "c1"}, true))
	assert.Equal(t, "", submitFromL1(t, m, "true"))
}

// A command that runs no submit is not kept: `loom job list` submits nothing,
// so it can't take a submit that a script made.
func TestModCall_ACommandThatSubmitsNothingIsNotKept(t *testing.T) {
	m := callModel(t)
	reportCall(t, m, ModCall{ID: "c1", Agent: "a1", Command: "loom job list && true"})
	assert.Equal(t, "", submitFromL1(t, m, "true"))
}

func TestModCall_ARetriedReportChangesNothing(t *testing.T) {
	m := callModel(t)
	c := ModCall{ID: "c1", Agent: "a1", Command: "loom job submit -- true"}
	reportCall(t, m, c)
	assert.Equal(t, "a1", submitFromL1(t, m, "true"))
	reportCall(t, m, c) // the same report again: its use stays spent
	assert.Equal(t, "", submitFromL1(t, m, "true"))
}

func TestModCall_TheCapDropsTheOldest(t *testing.T) {
	m := callModel(t)
	for i := range maxModCalls + 1 {
		reportCall(t, m, ModCall{ID: fmt.Sprint(i), Agent: fmt.Sprintf("a%d", i), Command: fmt.Sprintf("loom job submit -- echo n%03d", i)})
	}
	assert.Equal(t, "", submitFromL1(t, m, "echo", "n000"), "the oldest was dropped")
	assert.Equal(t, fmt.Sprintf("a%d", maxModCalls), submitFromL1(t, m, "echo", fmt.Sprintf("n%03d", maxModCalls)))
}

func TestModCall_AnEndedLinkForgetsItsCalls(t *testing.T) {
	m := callModel(t)
	reportCall(t, m, ModCall{ID: "c1", Agent: "a1", Command: "loom job submit -- true"})
	inst := m.instByLaunch("L1")
	m.endLink(inst, m.mods[inst], "the session stopped")
	assert.Equal(t, "", submitFromL1(t, m, "true"))
}

func TestModCall_RefusesAnUnknownLaunchAndASupersededPoller(t *testing.T) {
	m := callModel(t)
	c := ModCall{ID: "c1", Agent: "a1", Command: "loom job submit -- true"}
	assert.ErrorIs(t, m.ModCall("L9", "p", c, false), ErrModUnknownLaunch)
	assert.ErrorIs(t, m.ModCall("L1", "old", c, false), ErrModSuperseded)
	assert.Equal(t, "", submitFromL1(t, m, "true"), "a refused report records nothing")
}
```

Run: `CGO_ENABLED=0 go test ./core -run 'TestSubmit_|TestModCall_' -count=1`
Expected: build failure (`undefined: ModCall`, `maxModCalls`, `ShortAgent`, unknown field `Agents`).

- [ ] **A3. Implement the model side**

Create `core/mod_calls.go`:

```go
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
}

// reportedCall is a call in flight on a link: uses is how many submits it
// may still account for, one per match of jobCall in its command.
type reportedCall struct {
	ModCall
	uses int
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
	l.calls = append(l.calls, &reportedCall{ModCall: c, uses: uses})
	return nil
}

// attribution is the reported call a submit of argv from launch came from:
// the one call in flight on that launch's link whose command holds every
// element of argv and has a use left. nil when none does, or more than one
// could have made it: the job then reports to the session (spec §5.2). The
// caller spends the use once the job is saved. A link for an earlier launch
// is never consulted.
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
		if c.uses == 0 || !holdsAll(c.Command, argv) {
			continue
		}
		if found != nil {
			return nil
		}
		found = c
	}
	return found
}

// holdsAll reports whether command contains every element of argv.
func holdsAll(command string, argv []string) bool {
	for _, a := range argv {
		if !strings.Contains(command, a) {
			return false
		}
	}
	return true
}
```

In `core/mod.go`:

1. In `type ModHello struct`, after `Commands`:

```go
	// Agents says the mod reports `loom job` Bash calls (ModCall) and can
	// send a message to a subagent (ModMessage.Agent). false from an older
	// mod, which gets a subagent's job notice as the session's (subagent job
	// results spec §5.3).
	Agents bool `json:"agents,omitempty"`
```

2. In `type ModHost interface`, after `ModAskClosed`:

```go
	// ModCall records a Bash call the mod reports starting, or forgets it
	// once ended; poller is as ModInbox's.
	ModCall(launch, poller string, c ModCall, ended bool) error
```

3. In `type modLink struct`, after `commands map[string]bool`:

```go
	// agents is the recorded hello's ModHello.Agents: the mod can send to a
	// subagent. calls are the `loom job` Bash calls it reports in flight,
	// oldest first (core/mod_calls.go).
	agents bool
	calls  []*reportedCall
```

4. In `ModHello`, after `l.commands = commandsOf(h.Commands)`:

```go
	l.agents = h.Agents
```

5. In `endLink`, change `l.outbox, l.turning = nil, map[string]*modMessage{}` to:

```go
	l.outbox, l.turning, l.calls = nil, map[string]*modMessage{}, nil
```

In `core/loop_mod.go`, after `ModAskClosed`:

```go
// ModCall runs Model.ModCall on the loop.
func (l *Loop) ModCall(launch, poller string, c ModCall, ended bool) error {
	var err error
	l.modCall(func(m *Model) { err = m.ModCall(launch, poller, c, ended) })
	return err
}
```

In `core/queue_ops.go`, `submitJob`:
- Right after `owner, unknown := m.jobOwner(s.Launch)`, add `call := m.attribution(s.Launch, s.Argv)`.
- After `rec.ID, rec.Order, rec.Owner = id, int64(id), uint64(owner)`, add:

```go
	if call != nil {
		rec.Agent = call.Agent
	}
```

- Right after the `q.moved()` that follows a successful `m.saveQueue()`, add:

```go
	if call != nil {
		call.uses-- // spent only by a job that was saved
	}
```

In `core/queue_view.go`, in `type JobView struct`, after `OwnerWorkspaceID`:

```go
	Agent            string      // the subagent that submitted it (sched.Record.Agent), full ID; "" for none
```

In `queueJobView`, add `Agent: r.Agent,` beside `Owner: InstanceID(r.Owner),`, and at the end of the file add:

```go
// ShortAgent is a subagent's ID as the job views show it: its first 7
// characters, enough to tell one session's subagents apart.
func ShortAgent(id string) string {
	if len(id) <= 7 {
		return id
	}
	return id[:7]
}
```

Run: `CGO_ENABLED=0 go test ./core -run 'TestSubmit_|TestModCall_' -count=1`
Expected: PASS. The build of `internal/modserver` still fails until A4 (the fakes lack `ModCall`).

- [ ] **A4. Serve `/calls`, and give the fakes `ModCall`**

In `internal/modserver/server.go`:
- Beside the other routes: `mux.HandleFunc(route("POST", "/calls"), s.calls)`.
- Beside `askClosedBody`:

```go
type callBody struct {
	Launch string       `json:"launch"`
	Poller string       `json:"poller"`
	Call   core.ModCall `json:"call"`
	Ended  bool         `json:"ended"`
}
```

- After `askClosed`:

```go
// calls takes a Bash call the mod reports starting or ended (subagent job
// results spec §6).
func (s *Server) calls(w http.ResponseWriter, r *http.Request) {
	var b callBody
	if decode(w, r, &b) {
		answer(w, s.host.ModCall(b.Launch, b.Poller, b.Call, b.Ended))
	}
}
```

In `internal/modserver/server_test.go`, add `calls []string` to `fakeHost`, and:

```go
func (h *fakeHost) ModCall(launch, poller string, c core.ModCall, ended bool) error {
	if err := askErr(launch, poller, ""); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if ended {
		h.calls = append(h.calls, c.ID+" ended")
	} else {
		h.calls = append(h.calls, c.ID+" "+c.Agent+" "+c.Command)
	}
	return nil
}

func TestCalls(t *testing.T) {
	h := &fakeHost{}
	_, c := listen(t, h, time.Second)
	assert.Equal(t, http.StatusNoContent, post(t, c, api("/calls"), `{"launch":"L1","poller":"p","call":{"id":"c1","agent":"a1","command":"loom job submit -- true"}}`))
	assert.Equal(t, http.StatusNoContent, post(t, c, api("/calls"), `{"launch":"L1","poller":"p","call":{"id":"c1"},"ended":true}`))
	assert.Equal(t, []string{"c1 a1 loom job submit -- true", "c1 ended"}, h.calls)
	assert.Equal(t, http.StatusNotFound, post(t, c, api("/calls"), `{"launch":"L9","call":{"id":"c1"}}`))
	assert.Equal(t, http.StatusConflict, post(t, c, api("/calls"), `{"launch":"L1","poller":"old","call":{"id":"c1"}}`))
}
```

In `internal/modserver/realclaude_test.go`, add `calls []core.ModCall` to `queueHost`, and:

```go
// ModCall records each call reported starting; an ended report is not kept.
func (h *queueHost) ModCall(launch, _ string, c core.ModCall, ended bool) error {
	if launch != "L1" {
		return core.ErrModUnknownLaunch
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if !ended {
		h.calls = append(h.calls, c)
	}
	return nil
}

// callWith is the first call reported starting whose command holds s.
func (h *queueHost) callWith(s string) (core.ModCall, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.calls {
		if strings.Contains(c.Command, s) {
			return c, true
		}
	}
	return core.ModCall{}, false
}
```

Run: `CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test ./internal/modserver ./core -count=1`
Expected: PASS. The real-Claude tests skip without `LOOM_TEST_REAL_CLAUDE=1`.

- [ ] **A5. Regenerate the wire reference and check the package**

Run:
```bash
go generate ./core/rpc
CGO_ENABLED=0 go test ./core/rpc -run TestProtocolReference -update -count=1
CGO_ENABLED=0 go test ./core/rpc ./sched ./core ./internal/modserver -count=1
gofmt -l sched/job.go session/claudemod/claudemod.go core/mod.go core/mod_calls.go core/mod_calls_test.go core/loop_mod.go core/queue_ops.go core/queue_view.go internal/modserver/server.go internal/modserver/server_test.go internal/modserver/realclaude_test.go
git diff --stat docs/specs/protocol.md core/rpc/methods_gen.go
```
Expected:
- the tests PASS and gofmt prints nothing;
- `protocol.md` changes only by `JobView`'s `Agent`;
- `methods_gen.go` is unchanged, since no `Core` method was added.

- [ ] **A6. Commit**

```bash
git add core/mod_calls.go core/mod_calls_test.go
git commit -m "feat(core,sched,modserver): attribute a submit to the subagent whose call made it

The loom mod's /v1/calls reports feed a per-link list of loom job Bash
calls in flight; submitJob records the agent of the one call whose
command holds the submitted argv (sched.Record.Agent, JobView.Agent).
Nothing is delivered differently yet.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>" -- sched/job.go session/claudemod/claudemod.go core/mod.go core/mod_calls.go core/mod_calls_test.go core/loop_mod.go core/queue_ops.go core/queue_view.go internal/modserver/server.go internal/modserver/server_test.go internal/modserver/realclaude_test.go docs/specs/protocol.md
git status --short
```

---

## Package B: delivery

A job notice for an agent goes to the mod addressed to the agent. An older mod gets the session's labelled copy. A failed send falls back to the session.

**Files:**
- Modify: `core/mod.go`, `core/queue_notice.go`, `core/queue_notice_test.go`

- [ ] **B1. Write the failing delivery tests**

Append to `core/queue_notice_test.go`:

```go
// agentNotice seeds job 5, ended and owned by inst, as submitted by subagent
// a1, with a three-line log, and returns its record and log path.
func agentNotice(t *testing.T, m *Model, inst InstanceID) (sched.Record, string) {
	t.Helper()
	rec := wqEnded(5, inst)
	rec.Agent = "a1"
	wqSeed(t, m, rec)
	logPath := filepath.Join(m.queue.jobDir(5), sched.LogFile)
	require.NoError(t, os.WriteFile(logPath, []byte("one\ntwo\nthree\n"), 0o600))
	return rec, logPath
}

// A subagent's notice goes to it through a mod that can send to subagents,
// as today's text, and a mod that can't gets the session's copy naming the
// subagent. Which one is decided as the inbox hands it over, so a notice
// queued before the hello follows the hello (spec §5.3).
func TestJobQueue_ASubagentsNoticeGoesToItWhenTheModCan(t *testing.T) {
	for _, agents := range []bool{true, false} {
		for _, helloFirst := range []bool{true, false} {
			t.Run(fmt.Sprintf("agents=%v helloFirst=%v", agents, helloFirst), func(t *testing.T) {
				withMod(t)
				m := wqModel(t)
				inst := modInst(t, m, "owner")
				rec, logPath := agentNotice(t, m, m.idOf(inst))
				hello := func() { require.NoError(t, m.ModHello("L1", ModHello{Mod: 1, Agents: agents})) }
				if helloFirst {
					hello()
				}
				m.sendJobNotice(rec)
				wqPump(t, m)
				if !helloFirst {
					hello()
				}

				msgs, _, err := m.ModInbox("L1", "")
				require.NoError(t, err)
				require.Len(t, msgs, 1)
				tail := "\nLog: " + logPath + " (3 lines). Last 2 lines:\n\ntwo\nthree"
				if agents {
					assert.Equal(t, "a1", msgs[0].Agent)
					assert.Equal(t, noticeHead(rec)+tail, msgs[0].Text, "the subagent reads today's notice")
				} else {
					assert.Empty(t, msgs[0].Agent, "the session's copy")
					assert.Equal(t, noticeHead(rec)+"\n"+oldModLine("a1")+tail, msgs[0].Text)
				}
				require.NoError(t, m.ModEvents("L1", []ModEvent{{Kind: "queued", ID: msgs[0].ID}}))
				assert.Equal(t, sched.NoticeSent, wqRecord(t, m, 5).Notice)
			})
		}
	}
}

// A subagent that can't take its notice leaves it to the session, which gets
// it with the subagent named and why; its fate is recorded when the session's
// copy is queued.
func TestJobQueue_ASubagentThatCantTakeItsNoticeLeavesItToTheSession(t *testing.T) {
	withMod(t)
	m := wqModel(t)
	inst := modInst(t, m, "owner")
	rec, logPath := agentNotice(t, m, m.idOf(inst))
	require.NoError(t, m.ModHello("L1", ModHello{Mod: 1, Agents: true}))
	m.sendJobNotice(rec)
	wqPump(t, m)
	first, _, err := m.ModInbox("L1", "")
	require.NoError(t, err)
	require.Len(t, first, 1)

	why := "Agent 'a1' could not be resumed: No transcript found for agent ID: a1."
	require.NoError(t, m.ModEvents("L1", []ModEvent{{Kind: "failed", ID: first[0].ID, Error: why}}))

	assert.Equal(t, sched.NoticeNone, wqRecord(t, m, 5).Notice, "not sent until the session's copy is queued")
	again, _, err := m.ModInbox("L1", "")
	require.NoError(t, err)
	require.Len(t, again, 1)
	assert.NotEqual(t, first[0].ID, again[0].ID)
	assert.Empty(t, again[0].Agent)
	assert.Equal(t, noticeHead(rec)+"\n"+unreachableLine("a1", why)+"\nLog: "+logPath+" (3 lines). Last 2 lines:\n\ntwo\nthree", again[0].Text)
	assert.Contains(t, again[0].Text, "(Submitted by your subagent a1; loom could not reach it: Agent 'a1' could not be resumed")
	require.NoError(t, m.ModEvents("L1", []ModEvent{{Kind: "queued", ID: again[0].ID}}))
	assert.Equal(t, sched.NoticeSent, wqRecord(t, m, 5).Notice)
}

// A failed report for any other message is unchanged: a CLI message the mod
// could not submit is refused, not handed to the session again.
func TestJobQueue_OnlyASubagentsNoticeFallsBackToTheSession(t *testing.T) {
	withMod(t)
	m := wqModel(t)
	inst := modInst(t, m, "owner")
	require.NoError(t, m.ModHello("L1", ModHello{Mod: 1, Agents: true}))
	m.enqueue(inst, cliMessage(7, "hi", false))
	msgs, _, err := m.ModInbox("L1", "")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.NoError(t, m.ModEvents("L1", []ModEvent{{Kind: "failed", ID: msgs[0].ID, Error: "boom"}}))
	again, _, err := m.ModInbox("L1", "")
	require.NoError(t, err)
	assert.Empty(t, again, "nothing is queued again")
}

// A subagent's notice a stopping daemon holds is undelivered, as any notice
// the mod took is: it is never rerouted to the session (core/CLAUDE.md, loom
// mod links).
func TestJobQueue_ASubagentsNoticeHeldAtTheStopIsUndelivered(t *testing.T) {
	withMod(t)
	m := wqModel(t)
	inst := modInst(t, m, "owner")
	rec, _ := agentNotice(t, m, m.idOf(inst))
	require.NoError(t, m.ModHello("L1", ModHello{Mod: 1, Agents: true}))
	m.sendJobNotice(rec)
	wqPump(t, m)
	_, _, err := m.ModInbox("L1", "")
	require.NoError(t, err)

	m.ModsStop()

	assert.Equal(t, sched.NoticeUndelivered, wqRecord(t, m, 5).Notice)
}

func TestWithSubmitter_PutsItsLineAfterTheHead(t *testing.T) {
	assert.Equal(t, "head\nline\nrest\nmore", withSubmitter("head\nrest\nmore", "line"))
	assert.Equal(t, "head\nline", withSubmitter("head", "line"), "a job that never ran has a head alone")
	assert.Equal(t, "(Submitted by your subagent a1; loom could not reach it: no reason was given.)", unreachableLine("a1", ""))
}
```

Run: `CGO_ENABLED=0 go test ./core -run 'TestJobQueue_ASubagent|TestJobQueue_OnlyASubagent|TestWithSubmitter' -count=1`
Expected: build failure (`undefined: oldModLine`, `unreachableLine`, `withSubmitter`, unknown field `Agent` in `ModMessage`).

- [ ] **B2. Carry the agent and choose the path at hand-out**

In `core/mod.go`:

1. In `type ModMessage struct`, after `Args`:

```go
	// Agent, when set, is the subagent (its agentId) the mod sends Text to
	// ($.session.send) in place of submitting it (subagent job results spec
	// §5.3).
	Agent string `json:"agent,omitempty"`
```

2. In `type modMessage struct`, after `job sched.JobID`:

```go
	// agent is the subagent a job notice goes to (sched.Record.Agent). The
	// inbox hands it to a mod that can send to subagents (modLink.agents),
	// and gives anything else the session's copy (ModInbox).
	agent string
```

3. In `ModInbox`, replace the hand-out loop with:

```go
	for _, msg := range l.outbox {
		msg.offered = true
		if msg.agent != "" && !l.agents {
			// A mod that can't send to a subagent gets the session's copy,
			// naming the subagent (spec §5.3). Decided here, not at enqueue:
			// the hello that says what the mod can do may come later.
			msg.text, msg.agent = withSubmitter(msg.text, oldModLine(msg.agent)), ""
		}
		out = append(out, ModMessage{ID: msg.id, Text: msg.text, AsUser: msg.asUser, Command: msg.command, Args: msg.args, Agent: msg.agent})
	}
```

4. In `modFailed`, replace the first `if` with:

```go
	if msg := l.take(ev.ID); msg != nil {
		if msg.origin == OriginJob && msg.agent != "" {
			// The subagent could not take it: the session gets it, naming
			// the subagent and why (spec §5.3). Its fate is recorded when
			// that copy is queued.
			log.For("core").Info("mod.agent_unreachable", "instance", inst.Title, "job", msg.job, "agent", msg.agent, "why", ev.Error)
			m.enqueue(inst, &modMessage{text: withSubmitter(msg.text, unreachableLine(msg.agent, ev.Error)), origin: OriginJob, job: msg.job})
			return
		}
		m.withoutMod(inst, msg, "the loom mod could not submit it: "+ev.Error)
		return
	}
```

In `core/queue_notice.go`:

1. Add `agent string` to `jobNoticeBuilt`, after `owner`.
2. `sendJobNotice`: `m.spawnBackground(buildNoticeJob(rec.ID, owner, rec.Agent, noticeHead(rec), logPath, rec.Tail))`.
3. `buildNoticeJob`:

```go
func buildNoticeJob(id sched.JobID, owner InstanceID, agent, head, logPath string, tail int) Job {
	return func() any {
		return jobNoticeBuilt{id: id, owner: owner, agent: agent, text: composeNotice(head, logPath, tail)}
	}
}
```

4. `deliverJobNotice`'s enqueue: `m.enqueue(inst, &modMessage{text: r.text, origin: OriginJob, job: r.id, agent: r.agent})`.
5. After `composeNotice`:

```go
// withSubmitter is a notice's text with line after its head line: the
// session's copy of a notice a subagent submitted (spec §5.4).
func withSubmitter(text, line string) string {
	head, rest, ok := strings.Cut(text, "\n")
	if !ok {
		return head + "\n" + line
	}
	return head + "\n" + line + "\n" + rest
}

// unreachableLine names the subagent that submitted a job, with why loom
// could not reach it, for the session's copy. The full ID: SendMessage takes
// it.
func unreachableLine(agent, why string) string {
	why = strings.TrimSuffix(strings.TrimSpace(why), ".")
	if why == "" {
		why = "no reason was given"
	}
	return fmt.Sprintf("(Submitted by your subagent %s; loom could not reach it: %s.)", agent, why)
}

// oldModLine names the subagent that submitted a job, for a session whose
// loom mod can't message it.
func oldModLine(agent string) string {
	return fmt.Sprintf("(Submitted by your subagent %s; this session's loom mod is too old to message it, so pass this on with SendMessage if it still needs it.)", agent)
}
```

Fix every other `buildNoticeJob` caller the compiler names. `grep -rn 'buildNoticeJob(' core` should list only `sendJobNotice` and the tests.

Run: `CGO_ENABLED=0 go test ./core -count=1`
Expected: PASS, the existing notice and mod tests included.

- [ ] **B3. Mutation-check the guards**

Each change below must turn at least one of B1's tests red. Revert each before the next.
- In `ModInbox`, drop `&& !l.agents`: the agents=true cases fail.
- In `ModInbox`, drop `msg.agent != "" &&`: the CLI test elsewhere (`TestMod_…`) or the agents=false case fails.
- In `modFailed`, drop `msg.origin == OriginJob &&`: `TestJobQueue_OnlyASubagentsNoticeFallsBackToTheSession` fails only if a CLI message carries an agent. It never does, so this term is defence. Note it, and keep the term.
- In `attribution`, drop `if found != nil { return nil }`: "two calls could have made it" fails.
- In `attribution`, drop `c.uses == 0 ||`: `TestSubmit_ACallAccountsForAsManySubmitsAsItRuns` fails.
- In `ModCall`, drop the `uses == 0` return: `TestModCall_ACommandThatSubmitsNothingIsNotKept` fails.

Run after each revert: `CGO_ENABLED=0 go test ./core -count=1`. Expected: PASS.

- [ ] **B4. Commit**

```bash
gofmt -l core/mod.go core/queue_notice.go core/queue_notice_test.go
git commit -m "feat(core): send a subagent's job notice to it, falling back to the session

A notice for a job with an agent goes to a mod that said it can send to
subagents, addressed to the agent; any other mod gets the session's copy
naming the subagent, decided as the inbox hands it over. A send the
subagent can't take comes back failed and the session gets the notice,
naming the subagent and why.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>" -- core/mod.go core/queue_notice.go core/queue_notice_test.go
git status --short
```

---

## Package C: the mod

The module reports `loom job` calls, says it can send to subagents, and sends a message with `Agent` through `$.session.send`.

**Files:**
- Modify: `session/claudemod/mod/hooks/register.ts`, `session/claudemod/claudemod_test.go`, `session/claudemod/realclaude_test.go`, `internal/modserver/realclaude_test.go`

- [ ] **C1. Write the failing pattern check**

In `session/claudemod/claudemod_test.go`, inside `TestModule_SpeaksThisProtocol`, after the `EnvSocket, EnvLaunch` loop:

```go
	assert.Contains(t, src, "/"+JobCallPattern+"/", "the module reports the calls the daemon counts (core, attribution)")
	assert.Contains(t, src, "agents: true", "the hello says the module can send to subagents")
```

Run: `CGO_ENABLED=0 go test ./session/claudemod -run TestModule_SpeaksThisProtocol -count=1`
Expected: FAIL (register.ts carries neither).

- [ ] **C2. Change the module**

In `session/claudemod/mod/hooks/register.ts`:

1. Extend the type import with `SessionSendResult`:

```ts
import type { CommandRunResult, EngineInterface, PromptSubmitResult, Register, SessionSendResult } from 'claude-code'
```

2. `type Message` gains `agent?: string`:

```ts
type Message = { id: string; text: string; asUser: boolean; command?: string; args?: string; agent?: string }
```

3. After `const KEEP_SUBMITTED = 200`:

```ts
// JOB_CALL finds `loom job submit` or `loom job run` in a Bash command: the
// module reports each call whose command matches it, from a subagent or the
// main loop, so the daemon can send a job's result back to the subagent that
// submitted it (claudemod.JobCallPattern carries the same source; spec
// docs/superpowers/specs/2026-11-05-subagent-job-results-design.md).
const JOB_CALL = /\bloom\s+job\s+(submit|run)\b/
// CALL_REPORT_MS bounds the wait for a call's report before its command runs.
const CALL_REPORT_MS = 2000
```

4. In `hello`, add `agents: true,` after `loaded: LOADED,`.

5. After `run`:

```ts
// sendToAgent hands a message for a subagent to it ($.session.send), which
// resumes one that has finished: queued once delivered, failed with the
// reason when it can't be, and the daemon then gives it to the session. It
// starts no main-loop turn, so it reports no started or complete.
async function sendToAgent($: EngineInterface, m: Message) {
  if (submitted.includes(m.id)) {
    await report($, [{ kind: 'queued', id: m.id }])
    return
  }
  let r: SessionSendResult
  try {
    r = await $.session.send({ to: { agentId: m.agent ?? '' }, text: m.text })
  } catch (err) {
    await report($, [{ kind: 'failed', id: m.id, error: String(err) }])
    return
  }
  if (!r.isDelivered) {
    await report($, [{ kind: 'failed', id: m.id, error: r.reason }])
    return
  }
  submitted.push(m.id)
  if (submitted.length > KEEP_SUBMITTED) submitted.shift()
  await reportInOrder($, [{ kind: 'queued', id: m.id }])
}

// reportCall posts a Bash call's start or end (/calls), giving up after
// CALL_REPORT_MS: a slow or absent daemon must not hold the command. A
// failure goes to the debug log, and the job then reports to the session.
async function reportCall($: EngineInterface, c: { id: string; agent?: string; command: string }, ended: boolean) {
  try {
    const r = await Promise.race([
      call($, 'POST', '/calls', { launch, poller: POLLER, call: c, ended }),
      $.clock.sleep(CALL_REPORT_MS).then(() => undefined),
    ])
    if (r === undefined) $.ui.log('loom: a call report timed out', { to: 'debug' })
  } catch (err) {
    $.ui.log(`loom: call report: ${String(err)}`, { to: 'debug' })
  }
}
```

6. In `pump`, replace the dispatch line with:

```ts
        for (const m of messages) await (m.command ? run($, m) : m.agent ? sendToAgent($, m) : submit($, m))
```

7. In `register`, after the pass-through `on('tool.call', …)` hook:

```ts
  // A Bash call that runs `loom job submit` or `loom job run` is reported
  // while it runs, so the daemon can match each submit to the call that
  // made it. The report is awaited, briefly, before the command runs. The
  // command is never changed: a rewrite would fail the person's allow
  // rules.
  on('tool.call', { tool: 'Bash' }, async ($, e, next) => {
    const command = typeof e.command === 'string' ? e.command : ''
    if (!JOB_CALL.test(command) || !(await ready($))) return next(e)
    const c = { id: e.tool_use_id ?? '', agent: e.agentId, command }
    await reportCall($, c, false)
    try {
      return await next(e)
    } finally {
      void reportCall($, c, true)
    }
  })
```

Run: `CGO_ENABLED=0 go test ./session/claudemod -count=1`
Expected: PASS.

- [ ] **C3. Update the validator test, add the real-Claude test, and run both**

In `session/claudemod/realclaude_test.go`, add `"$.session.send": true` to `allowedCalls`.

In `internal/modserver/realclaude_test.go`:

1. Rename `startClaude` to `startClaudeWith(t *testing.T, host *queueHost, prepare func(work string)) probe`. Right after `require.NoError(t, os.MkdirAll(work, 0o700))`, add:

```go
	if prepare != nil {
		prepare(work)
	}
```

Then add the old name back:

```go
// startClaude is startClaudeWith with nothing to prepare.
func startClaude(t *testing.T, host *queueHost) probe { return startClaudeWith(t, host, nil) }
```

2. Append:

```go
// TestRealClaude_ASubagentGetsItsJobsResult drives the route a subagent's
// job result takes (subagent job results spec §12). A subagent runs a
// `loom job submit` (an echo: no CLI is needed), and the mod reports the call
// with the subagent's agentId. A message queued for that agent then reaches
// the subagent after it has finished, and it runs the command the message
// gives it, which the mod reports under the same agentId.
func TestRealClaude_ASubagentGetsItsJobsResult(t *testing.T) {
	host := &queueHost{}
	p := startClaudeWith(t, host, func(work string) {
		require.NoError(t, os.MkdirAll(filepath.Join(work, ".claude"), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(work, ".claude", "settings.local.json"),
			[]byte(`{"permissions":{"allow":["Bash(echo:*)"]}}`), 0o600))
	})
	host.queue("m1", `Launch one general-purpose subagent with the Agent tool, in the foreground, with this prompt: "Run this Bash command exactly: echo loom job submit -- probe-first . Then reply DONE." When it returns, reply DONE.`)
	var first core.ModCall
	p.waitFor("the subagent's call report", 5*time.Minute, func() bool {
		c, ok := host.callWith("probe-first")
		first = c
		return ok && c.Agent != ""
	})
	p.waitFor("the main turn to end", 5*time.Minute, func() bool {
		_, ok := host.event("complete", "m1")
		return ok
	})
	host.add(core.ModMessage{ID: "m2", Agent: first.Agent,
		Text: `[loom] job 1 "probe" finished: exit 0. Run this Bash command exactly: echo loom job run -- probe-ack . Then reply DONE.`})
	p.waitFor("the mod to hand the message to the subagent", time.Minute, func() bool {
		_, ok := host.event("queued", "m2")
		return ok
	})
	p.waitFor("the resumed subagent's call", 5*time.Minute, func() bool {
		c, ok := host.callWith("probe-ack")
		return ok && c.Agent == first.Agent
	})
	_, started := host.event("started", "m2")
	assert.False(t, started, "a subagent's message starts no main-loop turn")
}
```

Run, each at a cost of real Claude calls:
```bash
LOOM_TEST_REAL_CLAUDE=1 CGO_ENABLED=0 go test ./session/claudemod -run TestRealClaude_ValidatesTheMod -count=1 -v
LOOM_TEST_REAL_CLAUDE=1 CGO_ENABLED=0 go test ./internal/modserver -run TestRealClaude_ASubagentGetsItsJobsResult -count=1 -v -timeout 20m
```
Expected:
- The validator test fails only on its pinned hooks line, since the module now also hooks `tool.call{tool=Bash}`. Set that `assert.Contains` to the line the validator prints, which must include `tool.call{tool=Bash}`, then rerun: PASS. Any other complaint means the module is wrong, so fix the module, not the test.
- The subagent test: PASS. If its first wait times out on a permission prompt, the screen in the failure shows it. The allow rule in `settings.local.json` should cover `echo`.

Then run the other real-Claude mod tests once, since the module changed:
`LOOM_TEST_REAL_CLAUDE=1 CGO_ENABLED=0 go test ./internal/modserver -run RealClaude -count=1 -timeout 30m`. Expected: PASS.

- [ ] **C4. Commit**

```bash
git commit -m "feat(claudemod): report loom job calls and send a subagent its message

The mod reports each Bash call that runs loom job submit or run, before
it runs and with its agentId, says agents: true in its hello, and sends
a message the daemon addresses to a subagent with \$.session.send,
reporting failed with the reason when it can't be delivered.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>" -- session/claudemod/mod/hooks/register.ts session/claudemod/claudemod_test.go session/claudemod/realclaude_test.go internal/modserver/realclaude_test.go
git status --short
```

---

## Package D: display, context and docs

**Files:**
- Modify: `internal/cli/job.go`, `internal/cli/job_follow.go`, `internal/cli/job_list.go`, `internal/cli/job_test.go`, `ui/work.go`, `ui/work_test.go`, `session/loom_context_shared.md`, `core/CLAUDE.md`, `session/claudemod/CLAUDE.md`, `internal/modserver/CLAUDE.md`

- [ ] **D1. Write the failing CLI and UI tests**

Append to `internal/cli/job_test.go`:

```go
func TestOwnerText_NamesTheSubagent(t *testing.T) {
	assert.Equal(t, "-", ownerText(core.JobView{}))
	assert.Equal(t, "lto build", ownerText(core.JobView{Owner: 3, OwnerTitle: "lto build"}))
	assert.Equal(t, "lto build › ab91bd4", ownerText(core.JobView{Owner: 3, OwnerTitle: "lto build", Agent: "ab91bd4b710780552"}))
	assert.Equal(t, "#3 › ab91bd4", ownerText(core.JobView{Owner: 3, Agent: "ab91bd4b710780552"}))
}

// submit says where a job's result will go, so a subagent knows whether it
// may end its turn (spec §5.5).
func TestResultRoute(t *testing.T) {
	served := sched.Submit{Launch: "L1", Notify: true}
	assert.Equal(t, "its result comes back to this subagent (ab91bd4)", resultRoute(served, core.JobView{ID: 1, Owner: 3, Agent: "ab91bd4b710780552"}))
	assert.Equal(t, "its result comes to this session", resultRoute(served, core.JobView{ID: 1, Owner: 3}))
	assert.Equal(t, "", resultRoute(served, core.JobView{ID: 1}), "an unknown launch: the daemon's warning says so")
	assert.Equal(t, "", resultRoute(sched.Submit{Launch: "L1"}, core.JobView{ID: 1, Owner: 3}), "--no-notify")
	assert.Equal(t, "no session to notify: follow it with loom job wait 1", resultRoute(sched.Submit{Notify: true}, core.JobView{ID: 1}))
	assert.Equal(t, "no session to notify: follow it with loom job wait 1", resultRoute(sched.Submit{}, core.JobView{ID: 1}), "a plain shell, as before")
}

func TestContinuesLine_NamesTheSubagent(t *testing.T) {
	assert.Equal(t, "job 4 continues; a message will bring its result (loom job wait 4 follows it again)", continuesLine(core.JobView{ID: 4}))
	assert.Equal(t, "job 4 continues; a message will bring its result to this subagent (ab91bd4) (loom job wait 4 follows it again)", continuesLine(core.JobView{ID: 4, Agent: "ab91bd4b710780552"}))
}
```

Append to `ui/work_test.go`:

```go
func TestWorkOwner_NamesTheSubagent(t *testing.T) {
	assert.Equal(t, "no owner", workOwner(core.JobView{}))
	assert.Equal(t, "lto build · loom", workOwner(core.JobView{Owner: 3, OwnerTitle: "lto build", OwnerWorkspace: "loom"}))
	assert.Equal(t, "lto build › ab91bd4 · loom", workOwner(core.JobView{Owner: 3, OwnerTitle: "lto build", OwnerWorkspace: "loom", Agent: "ab91bd4b710780552"}))
	assert.Equal(t, "lto build › ab91bd4", workOwner(core.JobView{Owner: 3, OwnerTitle: "lto build", Agent: "ab91bd4b710780552"}))
}
```

Run: `CGO_ENABLED=0 go test ./internal/cli ./ui -run 'TestOwnerText|TestResultRoute|TestContinuesLine|TestWorkOwner' -count=1`
Expected: build failure (`undefined: resultRoute`, `continuesLine`).

- [ ] **D2. Implement the owner texts and the two lines**

`internal/cli/job_list.go`, replace `ownerText`:

```go
func ownerText(j core.JobView) string {
	switch {
	case j.Owner == 0:
		return "-"
	case j.OwnerTitle == "":
		return fmt.Sprintf("#%d", j.Owner) + agentSuffix(j.Agent)
	}
	return j.OwnerTitle + agentSuffix(j.Agent)
}

// agentSuffix is " › <short id>" for a job a subagent submitted, "" for any
// other.
func agentSuffix(agent string) string {
	if agent == "" {
		return ""
	}
	return " › " + core.ShortAgent(agent)
}
```

`internal/cli/job.go`: in `newJobSubmitCmd`'s RunE, replace

```go
				if sub.Launch == "" {
					fmt.Fprintf(out, "no session to notify: follow it with loom job wait %d\n", id)
				}
```

with

```go
				if line := resultRoute(sub, v); line != "" {
					fmt.Fprintln(out, line)
				}
```

and add after `printPlacement`:

```go
// resultRoute says where a submitted job's result will go (subagent job
// results spec §5.5): to the subagent that submitted it, to the session, or,
// from a plain shell, nowhere. "" when the submit asked for no message, or
// its launch is unknown (the daemon's warning says so).
func resultRoute(sub sched.Submit, v core.JobView) string {
	switch {
	case sub.Launch == "":
		return fmt.Sprintf("no session to notify: follow it with loom job wait %d", v.ID)
	case !sub.Notify || v.Owner == 0:
		return ""
	case v.Agent != "":
		return fmt.Sprintf("its result comes back to this subagent (%s)", core.ShortAgent(v.Agent))
	}
	return "its result comes to this session"
}
```

`internal/cli/job_follow.go`: in `interrupted`, replace the `case v.Owner != 0 && d.askForMessage(id):` line's body with `d.say("%s", continuesLine(v))`, and add:

```go
// continuesLine is what an interrupted follower says of a job that keeps
// running and will send a message: to the subagent that submitted it when it
// has one.
func continuesLine(v core.JobView) string {
	to := ""
	if v.Agent != "" {
		to = fmt.Sprintf(" to this subagent (%s)", core.ShortAgent(v.Agent))
	}
	return fmt.Sprintf("job %d continues; a message will bring its result%s (loom job wait %d follows it again)", v.ID, to, v.ID)
}
```

`ui/work.go`, replace `workOwner`:

```go
func workOwner(j core.JobView) string {
	switch {
	case j.Owner == 0:
		return "no owner"
	case j.OwnerTitle == "":
		return "session gone"
	}
	owner := j.OwnerTitle
	if j.Agent != "" {
		owner += " › " + core.ShortAgent(j.Agent)
	}
	if j.OwnerWorkspace != "" {
		owner += " · " + j.OwnerWorkspace
	}
	return owner
}
```

Run: `CGO_ENABLED=0 go test ./internal/cli ./ui -count=1`
Expected: PASS, including `TestJobSubmit_QueuesBehindAnExclusiveJobAndListShowsWhy` and the interrupt test (`…a message will bring its result (loom job wait 1 follows it again)`), whose outputs are unchanged.

- [ ] **D3. The context sentence**

In `session/loom_context_shared.md`, replace the sentence that begins "A subagent runs in your session, so a job it submits reports to you, never to it:" with:

```
A job's message goes back to whoever submitted it: a subagent you brief to run `loom job submit` in a foreground Bash call gets its own result, even after it has finished, and can end its turn; a result loom can't place comes to you, naming the subagent.
```

In `internal/cli/job_test.go`, `TestJobContext_NamesOnlyFlagsTheCommandsHave`: drop `assert.Contains(t, para, "`loom job wait <N>`", name)`, keep the `"subagent"` assertion, and add `assert.Contains(t, para, "even after it has finished", name)`. Update the test's comment so it names the new sentence: "The paragraph also tells the agent that a job's message goes back to the subagent that submitted it."

Run: `CGO_ENABLED=0 go test ./internal/cli -run 'TestJobContext|TestLoomContext' -count=1 && CGO_ENABLED=0 go test ./session -run LoomContext -count=1`
Expected: PASS.

- [ ] **D4. Package rules**

Check headroom first: `go run ./tools/claudemd -v`. If a rule doesn't fit a file's budget, move its supporting context into a guide rather than raising the budget (root CLAUDE.md, "Size budgets").

`internal/modserver/CLAUDE.md`:
- In the header paragraph, after `POST /v<n>/asks/close`, add `, and for the work queue `POST /v<n>/calls` (a `loom job` Bash call starting or ended)`.
- In the 404/409 rule, add `TestCalls` to its Enforced list.

`session/claudemod/CLAUDE.md`, a new rule after the ask rule:

```markdown
- **Report a `loom job submit`/`run` Bash call before its command runs, bounded (`CALL_REPORT_MS`), and never change the command; send a message carrying `Agent` with `$.session.send`, reporting `failed` with its reason when undelivered.** A rewritten command fails the person's allow rules (probed live: every prefix form needs approval), an unbounded wait holds the command on a slow daemon, and a send the module judged itself would bypass the daemon's fallback to the session. `JOB_CALL` carries `JobCallPattern`'s source. **Enforced** for the pattern and the hello's `agents: true` by `TestModule_SpeaksThisProtocol`; **Convention** for the rest, checked by the opt-in `TestRealClaude_ASubagentGetsItsJobsResult` — a job's result lost to the parent, or a command that waits on the daemon.
```

`core/CLAUDE.md`, under "Loom mod links (`mod.go`)":

```markdown
- **Give a job a subagent only on an exact single match (`attribution`): the one reported call in flight on the submitting launch whose command holds the whole argv and has a use left; and choose a subagent notice's path as the inbox hands it over (`ModInbox`), falling back to the session on a failed send (`modFailed`).** A guess by timing or position sends a result to the wrong agent, silently; a choice made at enqueue misses a hello that comes later. **Enforced** by `TestSubmit_TakesItsAgentFromTheOneCallThatMadeIt`, `TestSubmit_ACallAccountsForAsManySubmitsAsItRuns`, `TestJobQueue_ASubagentsNoticeGoesToItWhenTheModCan` and `TestJobQueue_ASubagentThatCantTakeItsNoticeLeavesItToTheSession`.
```

Run: `CGO_ENABLED=0 go test . -run ClaudeMD -count=1 && go run ./tools/claudemd -v | tail -5`
Expected: PASS, and every file within budget.

- [ ] **D5. Full verification**

```bash
gofmt -l $(git ls-files '*.go' | grep -v '^vendor/')
CGO_ENABLED=0 go vet ./...
loom job run -d "loom full tests" -- env CGO_ENABLED=0 go test ./... -count=1
loom job run -d "loom race tests" -- env CC=clang CGO_ENABLED=1 go test -race ./... -count=1
```
Expected:
- gofmt prints nothing, and vet is clean;
- both suites PASS.

golangci-lint is skipped locally (CI pins v1; the dev shell's v2 rejects the config), and the report must say so. No Windows cross-build is needed, since no syscall, process or socket code changed. Run `go test -tags e2e ./e2e/...` only if a package-A change touched the join or a lifecycle; this plan touches neither.

- [ ] **D6. Commit**

```bash
git commit -m "feat(cli,ui,session): show the subagent behind a job and say where its result goes

loom job list and the work view name the submitting subagent; submit
says whether the result comes back to this subagent or the session; an
interrupted run names the subagent its message goes to. The context
prompt tells the agent a subagent gets its own job's result. Package
rules for attribution, the hand-out choice and the mod's call reports.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>" -- internal/cli/job.go internal/cli/job_follow.go internal/cli/job_list.go internal/cli/job_test.go ui/work.go ui/work_test.go session/loom_context_shared.md core/CLAUDE.md session/claudemod/CLAUDE.md internal/modserver/CLAUDE.md
git status --short
```

---

## Spec coverage

| Spec | Where |
|---|---|
| §5.1 call reports | C2 (module), A4 (route), A3 (`ModCall`) |
| §5.2 attribution, uses, exact-or-nothing | A2–A3, B3 |
| §5.3 delivery, hand-out choice, fallback, the stop | B1–B2, C2 |
| §5.4 texts | B2 (`withSubmitter`, `unreachableLine`, `oldModLine`) |
| §5.5 submit and run lines | D1–D2 |
| §6 protocol (`agents`, `/calls`, `Agent`) | A3–A4, B2, C2 |
| §7 records and wire | A1, A3, A5 |
| §8 display | D2 |
| §9 context | D3 |
| §10 rules | D4 |
| §11 failure handling | A2 (refusals, cap, ended), B1 (failed, stop), C2 (bounded report) |
| §12 tests | A2, A4, B1, B3, C1, C3, D1, D3 |
