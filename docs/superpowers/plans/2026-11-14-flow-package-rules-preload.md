# The Run Engine Pre-loads Package Rules: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every headless agent `/flow:run`'s engine dispatches starts with the package `CLAUDE.md` files that cover the files it works on already loaded.

**Architecture:**
- **The plan's files.** check-plan's `--json` gives each story its Files paths, and `flowrun.Story` keeps them.
- **The engine.** It hands each `Effects.Agent` call a `Scope`: files, plus a base and head, chosen per role from state it has memoized.
- **The CLI.** `runFX.Agent` lists the scope's files (the plan's, plus `git diff --name-only base..head`) and finds every directory above them that holds a `CLAUDE.md`. It records them on the agent's record and passes one `--add-dir` per directory, setting `CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD=1`.
- **The record.** The run's record gains a `- Package rules:` line.

**Tech Stack:** Go (testify), Python 3 (unittest), Claude Code 2.1.323 headless `--agent`.

**Spec:** `docs/superpowers/specs/2026-11-14-flow-package-rules-preload-design.md`.

**Rules that bind this work:**
- `internal/flowrun/CLAUDE.md`: every engine decision comes from state and memoized results only, so a `Scope` is built from `s.Files` and the heads the engine holds.
- `internal/cli/CLAUDE.md`: git runs only through `internalexec.GitCommand`.
- Root `CLAUDE.md`:
  - commit by path;
  - scratch files go outside the worktree;
  - heavy commands go through `loom job run`;
  - generated files are never edited by hand.
- `flow/CLAUDE.md`: run `bash flow/lib/test.sh` after any change under `flow/`.

---

### Task 1: Each story carries its Files paths

**Files:**
- Modify: `flow/lib/check_plan.py` (`story_json`, about line 633)
- Modify: `flow/lib/check_plan_test.py` (`JsonTest.test_gives_each_story_its_lines_and_fields`, about line 258)
- Modify: `internal/flowrun/state.go` (`Story`, line 81; `Restory`, line 262)
- Modify: `internal/flowrun/restory_test.go` (`TestRestory`, first subtest)

- [ ] **Step 1: Write the failing tests**

In `check_plan_test.py`, add this to the end of `test_gives_each_story_its_lines_and_fields`:

```python
        self.assertEqual(stories[0]["files"], ["a/store.rs", "a/tests/store.rs"], "the Files line's paths, sorted")
        self.assertEqual(stories[1]["files"], ["ui/panel.ts"])
```

In `restory_test.go`, in the subtest "an edit inside a story moves it and every later story":
- after the line that sets `fresh[2].Verify, fresh[2].Interfaces, fresh[2].Landmines`, add `fresh[2].Files = []string{"y/y.go"}`;
- after `assert.Equal(t, []string{"a landmine"}, s3.Landmines)`, add:

```go
		assert.Equal(t, []string{"y/y.go"}, s3.Files, "a plan edit moves the Files line too")
```

- [ ] **Step 2: Run them to see them fail**

Run: `cd flow/lib && PYTHONDONTWRITEBYTECODE=1 python3 -m unittest check_plan_test -k test_gives_each_story_its_lines_and_fields`
Expected: FAIL with `KeyError: 'files'`.

Run: `loom job run -d "flowrun restory test" -- sh -c 'CGO_ENABLED=0 go test ./internal/flowrun/ -run TestRestory'`
Expected: a compile error, `s3.Files undefined`.

- [ ] **Step 3: Implement**

In `check_plan.py`'s `story_json`, add `files` after `verify`:

```python
        "interfaces": story.fields.get("Interfaces", ""), "verify": story.fields.get("Verify", ""),
        "files": sorted(story.files),
```

In `state.go`'s `Story`, add after `Verify`:

```go
	// Files is the story's **Files** line's paths (check-plan's `files`):
	// what its developers' and reviewers' package rules cover (package
	// rules spec §2.1). A run an older build started has none.
	Files []string `json:"files,omitempty"`
```

In `Restory`, replace the line `s.Line, s.End, s.Verify, s.Interfaces, s.Landmines = f.Line, f.End, f.Verify, f.Interfaces, f.Landmines` with:

```go
		s.Line, s.End, s.Verify, s.Interfaces, s.Landmines, s.Files = f.Line, f.End, f.Verify, f.Interfaces, f.Landmines, f.Files
```

In `internal/flowrun/CLAUDE.md`'s Restory rule, change "(`State.Restory`: the ranges, `Verify`, `Interfaces`, `Landmines`)" to "(`State.Restory`: the ranges, `Verify`, `Interfaces`, `Landmines`, `Files`)".

- [ ] **Step 4: Run them to see them pass**

Run: `cd flow/lib && PYTHONDONTWRITEBYTECODE=1 python3 -m unittest -q check_plan_test`
Expected: OK.

Run: `loom job run -d "flowrun tests" -- sh -c 'CGO_ENABLED=0 go test ./internal/flowrun/'`
Expected: ok.

- [ ] **Step 5: Commit**

```bash
git add flow/lib/check_plan.py flow/lib/check_plan_test.py internal/flowrun/state.go internal/flowrun/restory_test.go internal/flowrun/CLAUDE.md
git commit -m "feat(flow): each story in check-plan's JSON carries its Files paths" -- flow/lib/check_plan.py flow/lib/check_plan_test.py internal/flowrun/state.go internal/flowrun/restory_test.go internal/flowrun/CLAUDE.md
```

---

### Task 2: The engine gives every dispatch its scope

**Files:**
- Modify: `internal/flowrun/engine.go`:
  - `Effects` (line 61);
  - `track.agent` (about line 461);
  - `review`, `build`, `dispatchReport`, `docs`, `docPass`, `finalRound`, `fixRound`, `gateLoop`, `laneDev` and the lane redo, and every call of them.
- Modify: `internal/flowrun/engine_test.go` (`fakeFX`, `newFake`, `fakeFX.Agent`, a new test)
- Modify: `internal/cli/flow_run_fx.go` (`runFX.Agent`'s signature only; Task 3 uses it)
- Modify: `internal/flowrun/CLAUDE.md`, `internal/flowrun/README.md`

- [ ] **Step 1: Write the failing test**

In `engine_test.go`:
- add `scopes map[string]Scope` to `fakeFX`'s fields, beside `briefs`;
- add `scopes: map[string]Scope{},` to `newFake`'s literal;
- in `fakeFX.Agent`, change the signature to `func (f *fakeFX) Agent(dir, name, role, effort, brief string, sc Scope) (Report, error)`, and change the line `f.started[name], f.roles[name], f.briefs[name], f.dirs[name] = true, role, brief, dir` to:

```go
	f.started[name], f.roles[name], f.briefs[name], f.dirs[name], f.scopes[name] = true, role, brief, dir, sc
```

Add the test after `TestRun_ARestartAtEverySavePointSubmitsNothingTwice`:

```go
// Each dispatch carries the scope its package rules cover (package rules
// spec §2.1): a first build the Files line, a fix or review the Files line
// and the story's commits so far, the doc pass its story's diff, the final
// round the run's whole diff.
func TestRun_EachAgentGetsItsScope(t *testing.T) {
	f := newFake()
	f.reports["r-s1-review1"] = Report{Status: "DONE", Text: "FINDING: Critical a.go:3 fix\nVERDICT: CHANGES_REQUESTED\nSTATUS: DONE"}
	f.reports["r-s1-docs1"] = Report{Status: "DONE", Text: "DOC: README.md the flag\nSTATUS: DONE"}
	f.reports["r-final-review1"] = Report{Status: "DONE", Text: "FINDING: Important b.go:2 fix\nVERDICT: CHANGES_REQUESTED\nSTATUS: DONE"}
	s := story("1", "intermediate-dev", "vouched")
	s.Files = []string{"pkg/a.go"}
	st := newState(s)
	st.Docs, st.Blocks.DocWriter = true, "Doc paths (change only files these match): `*.md`"
	drive(t, st, f, nil)
	require.Equal(t, Done, st.Status)
	files := []string{"pkg/a.go"}
	assert.Equal(t, map[string]Scope{
		"r-s1-dev1":       {Files: files},
		"r-s1-review1":    {Files: files, Base: "h0", Head: "h1"},
		"r-s1-fix1":       {Files: files, Base: "h0", Head: "h1"},
		"r-s1-review2":    {Files: files, Base: "h0", Head: "h2"},
		"r-s1-docs1":      {Base: "h0", Head: "h2"},
		"r-final-review1": {Base: "h0", Head: "h3"},
		"r-final-fix1":    {Base: "h0", Head: "h3"},
		"r-final-review2": {Base: "h0", Head: "h4"},
	}, f.scopes)
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `loom job run -d "flowrun scope test" -- sh -c 'CGO_ENABLED=0 go test ./internal/flowrun/ -run TestRun_EachAgentGetsItsScope'`
Expected: a compile error, `undefined: Scope`.

- [ ] **Step 3: Implement**

In `engine.go`, before `Effects`:

```go
// Scope is what an agent's package rules cover (package rules spec
// §2.1): the plan's paths for it, and the files Base..Head changed, which
// the CLI lists. No Base, no diff.
type Scope struct {
	Files []string `json:"files,omitempty"`
	Base  string   `json:"base,omitempty"`
	Head  string   `json:"head,omitempty"`
}
```

Change `Effects.Agent` and its comment to:

```go
	// Agent starts agent run name in dir ("" for the session's worktree;
	// parallel pairs spec §5.3), with the package rules sc covers, or
	// follows it.
	Agent(dir, name, role, effort, brief string, sc Scope) (Report, error)
```

Change `track.agent` to take the scope and pass it on:

```go
func (e *track) agent(name, role, effort, brief string, sc Scope) (Report, error) {
```

and inside it:

```go
	if err := e.wait(func() (err error) { r, err = e.fx.Agent(e.dir, name, role, effort, brief, sc); return err }); err != nil {
```

Add the helper after `track.agent`:

```go
// storyScope is a story's scope from base to the head last recorded: its
// Files line and what its commits changed (package rules spec §2.1).
func (e *track) storyScope(s *Story, base string) Scope {
	return Scope{Files: s.Files, Base: base, Head: e.head}
}
```

Thread a `sc Scope` parameter, last, through each of these, passing it to every `e.agent` call inside:
- `review(prefix, role, brief string, sc Scope)`;
- `build(s *Story, prefix, role, brief string, fix bool, sc Scope)`, which passes it to both of its `e.agent` calls;
- `dispatchReport(prefix, role, brief string, sc Scope)`;
- `docPass(name, brief string, sc Scope)`.

Set each call site's scope as follows:

| Call | Scope |
|---|---|
| `gateLoop`'s fix: `e.build(s, prefix+"-fix", …, FixGatesBrief(…), true, …)` | `e.storyScope(s, base)` |
| the premise checks' `e.dispatchReport("premise", …)` | `Scope{}` |
| `story`'s first build: `e.build(s, p+"-dev", s.Developer, DeveloperBrief(e.st, s), false, …)` | `Scope{Files: s.Files}` |
| `story`'s review: `e.review(p+"-review", role, ReviewBrief(…), …)` | `e.storyScope(s, base)` |
| `laneDev`'s build (`…+LaneNote(dir, e.lane.base), false, …`) | `Scope{Files: s.Files}` |
| the lane redo's build (`withEarlier(DeveloperBrief(e.st, s), paths(l.dev.Path), RedoDecision(l.t.dir)), false, …`) | `Scope{Files: s.Files}` |
| `fixRound`'s fix: `e.build(s, p+"-fix", "intermediate-dev", FixFindingsBrief(…), true, …)` | `e.storyScope(s, base)` |
| `fixRound`'s re-review: `e.review(p+"-review", "story-reviewer", ReReviewBrief(…), …)` | `e.storyScope(s, base)` |
| `docs`: `brief := DocsBrief(e.st, s, base, before, devReport, e.sent)` gains `sc := Scope{Base: base, Head: before}` below it, and `e.docPass(name, next)` becomes `e.docPass(name, next, sc)` | `Scope{Base: base, Head: before}` |
| `finalRound`'s review: `e.review("final-review", "code-quality-reviewer", AntiSeamBrief(e.st, e.head), …)` | `Scope{Base: e.st.Base, Head: e.head}` |
| `finalRound`'s fix: `e.build(s, "final-fix", …, true, …)` | `e.storyScope(s, e.st.Base)` |
| `finalRound`'s re-review: `e.review("final-review", "story-reviewer", ReReviewBrief(…), …)` | `Scope{Base: e.st.Base, Head: e.head}` |

In `internal/cli/flow_run_fx.go`, change `runFX.Agent`'s signature so the package builds:

```go
func (fx *runFX) Agent(dir, name, role, effort, brief string, sc flowrun.Scope) (flowrun.Report, error) {
```

Add this rule to `internal/flowrun/CLAUDE.md`, after the Restory rule:

```markdown
- **Give every agent dispatch its `Scope` from the story and the heads the engine recorded: a first build the story's `Files`, a fix or a review its `Files` and the story's base to the head last recorded, a doc pass its story's diff, the final round `State.Base` to the head, a premise check none.** The CLI pre-loads the package rules a scope covers, and a scope read from anything else changes on a replay or hands an agent another story's rules, silently. **Enforced** by `TestRun_EachAgentGetsItsScope`.
```

In `internal/flowrun/README.md`'s **Effects** bullet, add after its first sentence: "`Agent` takes a `Scope` (package rules spec, `docs/superpowers/specs/2026-11-14-flow-package-rules-preload-design.md`): the story's `Files` and a base and head, from which the CLI pre-loads the package `CLAUDE.md` files the agent's files sit under."

- [ ] **Step 4: Run the tests**

Run: `loom job run -d "flowrun and cli build" -- sh -c 'CGO_ENABLED=0 go test ./internal/flowrun/ && CGO_ENABLED=0 go vet ./internal/cli/'`
Expected: `ok` for flowrun, including `TestRun_EachAgentGetsItsScope` and `TestRun_ARestartAtEverySavePointSubmitsNothingTwice`; vet clean.

If `TestRun_EachAgentGetsItsScope` fails on a head only, read the engine's order of commits (a doc pass that commits moves the head before the final round) and correct the expected heads. Never change the scope rules to fit the test.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(flow): the run engine gives every agent dispatch its scope" -- internal/flowrun/engine.go internal/flowrun/engine_test.go internal/cli/flow_run_fx.go internal/flowrun/CLAUDE.md internal/flowrun/README.md
```

---

### Task 3: The CLI pre-loads the scope's package rules

**Files:**
- Create: `internal/cli/flow_rules.go`, `internal/cli/flow_rules_test.go`
- Modify: `internal/flowrun/records.go` (`AgentRecord`)
- Modify: `internal/cli/flow_run_fx.go` (`runFX.Agent`)
- Modify: `internal/cli/flow_agent_exec.go`:
  - `agentCommand` (line 83);
  - `agentEnv` (line 181);
  - `agentArgv` (line 380).
- Modify: `internal/cli/flow_agent.go`:
  - the `run` command's `agentArgv` call (line 90);
  - `answerArgv` (line 346);
  - `queueAgent` (line 155).
- Modify: `internal/cli/flow_agent_test.go`:
  - `agentArgv` calls (lines 512, 531, 533);
  - `agentEnv` calls (lines 430-433).
- Modify: `internal/cli/flow_run_test.go` (a new end-to-end test)

- [ ] **Step 1: Write the failing tests**

Create `internal/cli/flow_rules_test.go`:

```go
package cli

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/flow"
	"github.com/aidan-bailey/loom/internal/flowrun"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentRules(t *testing.T) {
	repo := t.TempDir()
	gitIn(t, repo, "init", "-q")
	write := func(rel, body string) {
		p := filepath.Join(repo, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	}
	write("CLAUDE.md", "root rules\n")
	write("pkg/CLAUDE.md", "pkg rules\n")     // 10 bytes
	write("pkg/sub/CLAUDE.md", "sub rules\n") // 10 bytes
	write("other/CLAUDE.md", "other rules\n") // 12 bytes
	write("pkg/sub/a.go", "package sub\n")
	write("plain/b.go", "package plain\n")
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "base")
	base := gitIn(t, repo, "rev-parse", "HEAD")
	write("other/c.go", "package other\n")
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "change")
	head := gitIn(t, repo, "rev-parse", "HEAD")
	at := func(rel ...string) []string {
		var out []string
		for _, r := range rel {
			out = append(out, filepath.Join(repo, filepath.FromSlash(r)))
		}
		return out
	}

	t.Run("every package above the files, the root's left out, docs counted", func(t *testing.T) {
		dirs, size, err := agentRules(repo, flowrun.Scope{Files: []string{"pkg/sub/a.go", "plain/b.go", "main.go", "pkg/README.md"}})
		require.NoError(t, err)
		assert.Equal(t, at("pkg", "pkg/sub"), dirs)
		assert.Equal(t, 20, size)
	})
	t.Run("a new file in a new directory gets the rules above it", func(t *testing.T) {
		dirs, size, err := agentRules(repo, flowrun.Scope{Files: []string{"pkg/new/dir/x.go"}})
		require.NoError(t, err)
		assert.Equal(t, at("pkg"), dirs)
		assert.Equal(t, 10, size)
	})
	t.Run("the diff's files join the plan's", func(t *testing.T) {
		dirs, size, err := agentRules(repo, flowrun.Scope{Files: []string{"pkg/sub/a.go"}, Base: base, Head: head})
		require.NoError(t, err)
		assert.Equal(t, at("other", "pkg", "pkg/sub"), dirs)
		assert.Equal(t, 32, size)
	})
	t.Run("a path that leaves the tree adds nothing", func(t *testing.T) {
		dirs, size, err := agentRules(repo, flowrun.Scope{Files: []string{"../x/y.go", "/etc/z.go", "pkg/../../w.go"}})
		require.NoError(t, err)
		assert.Empty(t, dirs)
		assert.Zero(t, size)
	})
	t.Run("no scope, no rules", func(t *testing.T) {
		dirs, size, err := agentRules(repo, flowrun.Scope{})
		require.NoError(t, err)
		assert.Empty(t, dirs)
		assert.Zero(t, size)
	})
	t.Run("a diff git can't give is an error", func(t *testing.T) {
		_, _, err := agentRules(repo, flowrun.Scope{Base: "nope", Head: head})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "git diff nope..")
	})
}

// A diff runFX.Agent can't read fails the step before anything starts: an
// agent never runs without the rules its scope covers.
func TestRunFX_AnUnreadableDiffFailsTheAgentStep(t *testing.T) {
	_, fx := generatedRepo(t)
	fx.agents = t.TempDir()
	r, err := fx.Agent("", "r-s1-review1", "story-reviewer", "", "Review.", flowrun.Scope{Base: "nope", Head: "HEAD"})
	require.NoError(t, err)
	assert.Equal(t, agentFailed, r.Status)
	assert.Contains(t, r.Why, "its package rules: git diff nope..HEAD")
	_, statErr := os.Stat(agentPath(fx.agents, "r-s1-review1"))
	assert.True(t, errors.Is(statErr, fs.ErrNotExist), "nothing started")
}

// A resumed run passes the rules its record holds.
func TestAnswerArgv_KeepsTheRunsRules(t *testing.T) {
	agentFixture(t, "story-reviewer")
	root := os.Getenv(flow.EnvRoot)
	run := &agentRun{ID: "r-s1-review1", Role: "story-reviewer", Cwd: t.TempDir(), Session: "sess-1", Status: "NEEDS_CONTEXT",
		Rules: []string{"/w/pkg", "/w/pkg/sub"}}
	argv, err := answerArgv(run, root, "Blue.")
	require.NoError(t, err)
	assert.Contains(t, strings.Join(argv, "\n"), "\n--add-dir\n/w/pkg\n--add-dir\n/w/pkg/sub\n")
	run.Rules = nil
	argv, err = answerArgv(run, root, "Blue.")
	require.NoError(t, err)
	assert.NotContains(t, argv, "--add-dir")
}
```

In `flow_agent_test.go`'s `TestAgentEnv_AFiveMinutePinSetsTheSwitchOnce` (about line 428), add `false` as the last argument to each `agentEnv` call, and add this after them:

```go
	assert.Equal(t, []string{"PATH=/bin", "LOOM_FLOW_AGENT=x", envRulesFromDirs + "=1"},
		agentEnv([]string{"PATH=/bin", envRulesFromDirs + "=0"}, "x", "", true), "package rules turn the variable on, once")
```

In the same file, add `nil` as the last argument to the three `agentArgv` calls (lines 512, 531, 533).

Add this to `flow_run_test.go`, after `TestFlowRun_EndToEnd`:

```go
// Each engine agent pre-loads the package rules its scope covers (package
// rules spec §2): Story 1's developer, fixer and reviewers get pkg's from
// its Files line; its doc writer and the final review, whose diffs (the
// fake's empty commits) name no file, get none.
func TestFlowRun_AgentsPreloadTheirPackageRules(t *testing.T) {
	s := runStory("1", "intermediate-dev")
	s.Files = []string{"pkg/a.go"}
	_, state, logDir := runFixture(t, s)
	repo, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "pkg"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "pkg", "CLAUDE.md"), []byte(strings.Repeat("r", 2047)+"\n"), 0o600))
	gitIn(t, repo, "add", "pkg")
	gitIn(t, repo, "commit", "-q", "-m", "pkg rules")
	id := "2026-11-06-the-plan"
	out, _, err := runRun(t, "", "start", "docs/2026-11-06-the-plan.md")
	require.NoError(t, err, out)
	out, _, err = runRun(t, "", "engine", id)
	require.NoError(t, err, out)
	out, _, err = runRun(t, "", "wait", id)
	require.NoError(t, err, out)
	require.Contains(t, out, "ended "+id+": done")

	read := func(name string) *agentRun {
		t.Helper()
		r, err := readAgentRun(agentPath(filepath.Join(state, "agents"), id+"-"+name))
		require.NoError(t, err)
		return r
	}
	dev := read("s1-dev1")
	pkg := filepath.Join(dev.Cwd, "pkg")
	assert.Equal(t, []string{pkg}, dev.Rules)
	assert.Equal(t, 2048, dev.RulesBytes)
	for _, name := range []string{"s1-review1", "s1-fix1", "s1-review2"} {
		assert.Equal(t, []string{pkg}, read(name).Rules, name)
	}
	assert.Empty(t, read("s1-docs1").Rules, "the doc writer's diff names no file")
	assert.Empty(t, read("final-review1").Rules, "nor does the run's")

	argFiles, err := filepath.Glob(filepath.Join(logDir, "*.args"))
	require.NoError(t, err)
	found := false
	for _, f := range argFiles {
		args, err := os.ReadFile(f)
		require.NoError(t, err)
		if !strings.Contains(string(args), "Build Story 1") {
			continue
		}
		found = true
		assert.Contains(t, string(args), "\n--add-dir\n"+pkg+"\n")
		env, err := os.ReadFile(strings.TrimSuffix(f, ".args") + ".env")
		require.NoError(t, err)
		assert.Contains(t, string(env), "\n"+envRulesFromDirs+"=1\n")
	}
	assert.True(t, found, "the developer's job ran")
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `loom job run -d "cli rules tests" -- sh -c 'CGO_ENABLED=0 go test ./internal/cli/ -run "TestAgentRules|TestRunFX_AnUnreadableDiffFailsTheAgentStep|TestAnswerArgv_KeepsTheRunsRules|TestFlowRun_AgentsPreloadTheirPackageRules|TestAgentEnv_AFiveMinutePinSetsTheSwitchOnce"'`
Expected: a compile error, `undefined: agentRules`.

- [ ] **Step 3: Implement**

In `internal/flowrun/records.go`'s `AgentRecord`, add after `Cwd`:

```go
	// Rules are the package rules directories the run pre-loads, and
	// RulesBytes their CLAUDE.md files' size (package rules spec §2.2): a
	// resume passes the same.
	Rules      []string `json:"rules,omitempty"`
	RulesBytes int      `json:"rules_bytes,omitempty"`
```

Create `internal/cli/flow_rules.go`:

```go
package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/internal/flowrun"
)

// envRulesFromDirs makes Claude load each --add-dir directory's CLAUDE.md
// at startup (probed on Claude Code 2.1.323, 2026-11-14: a later Read
// under the directory loads it no second time).
const envRulesFromDirs = "CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD"

// agentRules is the package rules an agent run in cwd pre-loads for sc
// (package rules spec §2.1): every directory below cwd that holds a
// CLAUDE.md, on the way up from one of sc's files or the files
// sc.Base..sc.Head changed, sorted, and those files' total size. cwd's
// own CLAUDE.md loads anyway, and a path that leaves the tree adds
// nothing. A diff git can't give is an error: the step fails rather than
// run the agent without its rules.
func agentRules(cwd string, sc flowrun.Scope) ([]string, int, error) {
	files := slices.Clone(sc.Files)
	if sc.Base != "" {
		var out, errOut bytes.Buffer
		cmd := internalexec.GitCommand(context.Background(), cwd, "diff", "--name-only", "-z", sc.Base+".."+sc.Head)
		cmd.Stdout, cmd.Stderr = &out, &errOut
		if err := cmd.Run(); err != nil {
			return nil, 0, fmt.Errorf("git diff %s..%s: %v: %s", sc.Base, sc.Head, err, strings.TrimSpace(errOut.String()))
		}
		for _, f := range strings.Split(out.String(), "\x00") {
			if f != "" {
				files = append(files, f)
			}
		}
	}
	seen := map[string]bool{}
	var dirs []string
	size := 0
	for _, f := range files {
		rel := path.Clean(filepath.ToSlash(f))
		if path.IsAbs(rel) || filepath.IsAbs(f) || rel == ".." || strings.HasPrefix(rel, "../") {
			continue
		}
		for d := path.Dir(rel); d != "." && d != "/"; d = path.Dir(d) {
			if seen[d] {
				break // its parents were walked with it
			}
			seen[d] = true
			dir := filepath.Join(cwd, filepath.FromSlash(d))
			info, err := os.Stat(filepath.Join(dir, "CLAUDE.md"))
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			dirs = append(dirs, dir)
			size += int(info.Size())
		}
	}
	slices.Sort(dirs)
	return dirs, size, nil
}
```

In `flow_agent_exec.go`, change `agentCommand`'s signature and comment, and add the flags after `--settings`:

```go
// agentCommand is the argv of an agent job (spec §5.4): a first run when
// session is "", else a resume of it with prompt as the answer. settings is
// agentSettings' JSON. maxTurns, when positive, is passed as --max-turns:
// `--agent` doesn't apply a definition's maxTurns (part 3b's probe,
// 2026-11-06), so each job carries it. rules are the package rules
// directories it pre-loads, one --add-dir each (package rules spec §2.2).
func agentCommand(program, role, effort string, maxTurns int, mode, root, settings, prompt, session string, rules []string) []string {
	argv := []string{program, "-p", prompt, "--agent", "flow:" + role,
		"--plugin-dir", filepath.Join(root, "plugin"), "--settings", settings}
	for _, d := range rules {
		argv = append(argv, "--add-dir", d)
	}
```

Change `agentEnv` to:

```go
// agentEnv is an agent job's environment: env without the scrubbed
// variables, with LOOM_FLOW_AGENT set to role, for a definition that pins
// `cacheTtl: 5m` the five-minute switch on, and with rules the switch that
// loads each --add-dir directory's CLAUDE.md. `--agent`'s main thread
// writes at the session's lifetime, one hour on a subscription, whatever
// the definition pins (the 2026-11-11 probe); any other ttl leaves the
// submitter's choice as it is.
func agentEnv(env []string, role, ttl string, rules bool) []string {
	out := make([]string, 0, len(env)+3)
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if scrubbedVar(name) || name == envAgentRole || (ttl == "5m" && name == envCache5m) || (rules && name == envRulesFromDirs) {
			continue
		}
		out = append(out, kv)
	}
	out = append(out, envAgentRole+"="+role)
	if ttl == "5m" {
		out = append(out, envCache5m+"=1")
	}
	if rules {
		out = append(out, envRulesFromDirs+"=1")
	}
	return out
}
```

Change `agentArgv`'s signature, its comment's first line ("…run in cwd from the flow root root, with the package rules directories rules (spec §5.4)…"), and its last call:

```go
func agentArgv(root, role, effort, cwd string, brief []byte, rules []string) ([]string, error) {
```

```go
	return agentCommand(agentProgram(), role, agentEffort(effort, definition), frontmatterMaxTurns(definition), agentPermissionMode(), root, settings,
		rolePreamble(role)+"\n\n"+skills+string(brief), "", rules), nil
```

In `flow_agent.go`:
- the `run` command (line 90) becomes `argv, err := agentArgv(root, role, effort, cwd, brief, nil)`;
- in `answerArgv`, the restart becomes `return agentArgv(root, run.Role, run.Effort, run.Cwd, brief, run.Rules)`, and its last line passes `run.Rules`:

```go
	return agentCommand(agentProgram(), run.Role, agentEffort(run.Effort, definition), frontmatterMaxTurns(definition), agentPermissionMode(), root, settings, answer, run.Session, run.Rules), nil
```

- in `queueAgent`:

```go
	sub.Env, sub.Kind = agentEnv(sub.Env, run.Role, frontmatterCacheTTL(definition), len(run.Rules) > 0), sched.KindAgent
```

In `flow_run_fx.go`'s `runFX.Agent`, resolve the rules first in the `!started` branch, and record them:

```go
	if !started {
		rules, size, err := agentRules(cwd, sc)
		if err != nil {
			return flowrun.Report{Status: agentFailed, Why: "its package rules: " + err.Error()}, nil
		}
		argv, err := agentArgv(fx.root, role, effort, cwd, []byte(brief), rules)
```

The log line becomes `fx.logf("start %s: flow:%s %s, %d package rules dirs", name, role, effort, len(rules))`. The record literal becomes:

```go
			return d.startAgent(io.Discard, fx.agents, fx.root, &agentRun{Schema: agentRunSchema, ID: name, Role: role, Effort: effort, Cwd: cwd, Rules: rules, RulesBytes: size}, argv, []byte(brief))
```

- [ ] **Step 4: Run the tests**

Run: `loom job run -d "cli flow tests" -- sh -c 'CGO_ENABLED=0 go test ./internal/cli/ -run "Flow|Agent|RunFX"'`
Expected: ok.

- [ ] **Step 5: Mutation-check the walk**

The root `CLAUDE.md` asks for each term of a compound guard to be checked separately. One at a time, edit `agentRules` to drop:
- `rel == ".." || strings.HasPrefix(rel, "../")`;
- `path.IsAbs(rel) || filepath.IsAbs(f)`;
- the `break`.

Re-run `TestAgentRules` after each edit. The first two must turn its "leaves the tree" subtest red. Restore each edit before the next.

The `break` is an optimisation, so its subtests stay green either way. Confirm that, then restore it.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/flow_rules.go internal/cli/flow_rules_test.go
git commit -m "feat(flow): engine agents pre-load the package rules their scope covers" -- internal/cli/flow_rules.go internal/cli/flow_rules_test.go internal/flowrun/records.go internal/cli/flow_run_fx.go internal/cli/flow_agent_exec.go internal/cli/flow_agent.go internal/cli/flow_agent_test.go internal/cli/flow_run_test.go
```

---

### Task 4: The record's Package rules line

**Files:**
- Modify: `internal/flowrun/record.go` (`RecordInputs`, `Record`)
- Modify: `internal/flowrun/record_test.go`
- Modify: `internal/cli/flow_run.go` (`runFX.record`, about line 491)
- Modify: `internal/cli/flow_run_test.go` (`TestFlowRun_AgentsPreloadTheirPackageRules`)
- Modify: `flow/plugin/docs/records.md`
- Modify: `docs/superpowers/specs/2026-11-14-flow-package-rules-preload-design.md` (§2.3, §3)

- [ ] **Step 1: Write the failing tests**

Append to `record_test.go`:

```go
// The Package rules line (package rules spec §2.3) follows the Engine
// line: absent when the record knows no agent run's rules.
func TestRecord_PackageRulesLine(t *testing.T) {
	st := newState(story("1", "intermediate-dev", "vouched"))
	st.Status = Done
	assert.NotContains(t, Record(st, RecordInputs{}), "Package rules")
	assert.Contains(t, Record(st, RecordInputs{Rules: []AgentRules{{Name: "r-s1-dev1", Role: "intermediate-dev"}}}),
		"\n- Package rules: none pre-loaded\n\n## Cost")
	got := Record(st, RecordInputs{Rules: []AgentRules{
		{Name: "r-s1-dev1", Role: "intermediate-dev", Bytes: 2048},
		{Name: "r-s1-review1", Role: "code-quality-reviewer", Bytes: 4096},
		{Name: "r-s1-docs1", Role: "doc-writer"},
	}})
	assert.Contains(t, got, "- Engine: loom flow run (flow in loom part 3b)\n"+
		"- Package rules: 2 of 3 agent runs pre-loaded rules, 3.0 KB a run, most 4.0 KB (r-s1-review1, code-quality-reviewer)\n\n## Cost")
}
```

and add `"github.com/stretchr/testify/assert"` to its imports.

Append to `TestFlowRun_AgentsPreloadTheirPackageRules`:

```go
	st, err := flowrun.Load(filepath.Join(state, "runs", id+".json"))
	require.NoError(t, err)
	record, err := os.ReadFile(st.Record)
	require.NoError(t, err)
	assert.Contains(t, string(record), "\n- Package rules: 4 of 6 agent runs pre-loaded rules, 2.0 KB a run, most 2.0 KB ("+id+"-s1-dev1, intermediate-dev)\n")
```

- [ ] **Step 2: Run them to see them fail**

Run: `loom job run -d "record tests" -- sh -c 'CGO_ENABLED=0 go test ./internal/flowrun/ -run TestRecord'`
Expected: a compile error, `undefined: AgentRules`.

- [ ] **Step 3: Implement**

In `record.go`, add a `Rules` field to `RecordInputs`, and the type:

```go
	// Rules is each agent run's package rules as its record holds them
	// (package rules spec §2.3); nil leaves the line out.
	Rules []AgentRules
```

```go
// AgentRules is one agent run's package rules for the record: its name,
// role and the bytes of CLAUDE.md it pre-loaded (0 for none).
type AgentRules struct {
	Name  string
	Role  string
	Bytes int
}
```

In `Record`, replace the line `fmt.Fprintf(&b, "- Ended: %s\n- Engine: loom flow run (flow in loom part 3b)\n\n", st.Status)` with:

```go
	fmt.Fprintf(&b, "- Ended: %s\n- Engine: loom flow run (flow in loom part 3b)\n", st.Status)
	if line := rulesLine(in.Rules); line != "" {
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")
```

and add:

```go
// rulesLine is the record's Package rules line (package rules spec §2.3),
// "" when the record knows no agent run's rules.
func rulesLine(rules []AgentRules) string {
	if len(rules) == 0 {
		return ""
	}
	n, total := 0, 0
	var most AgentRules
	for _, r := range rules {
		if r.Bytes == 0 {
			continue
		}
		n, total = n+1, total+r.Bytes
		if r.Bytes > most.Bytes {
			most = r
		}
	}
	if n == 0 {
		return "- Package rules: none pre-loaded"
	}
	kb := func(b float64) string { return strconv.FormatFloat(b/1024, 'f', 1, 64) }
	return fmt.Sprintf("- Package rules: %d of %d agent runs pre-loaded rules, %s KB a run, most %s KB (%s, %s)",
		n, len(rules), kb(float64(total)/float64(n)), kb(float64(most.Bytes)), most.Name, most.Role)
}
```

In `flow_run.go`'s `runFX.record`, after `in := flowrun.RecordInputs{…}`:

```go
	for _, name := range st.Agents {
		run, err := readAgentRun(agentPath(fx.agents, name))
		if err != nil {
			continue // a step that failed before it started has no record
		}
		in.Rules = append(in.Rules, flowrun.AgentRules{Name: name, Role: run.Role, Bytes: run.RulesBytes})
	}
```

In `flow/plugin/docs/records.md`, after the `- Ended:`/`- Engine:` item, add:

```markdown
   - `- Package rules:`, in a record the run engine wrote from 2026-11-14 on:
     how many of its agent runs pre-loaded the package rules their scope
     covers (an `--add-dir` for each directory holding a `CLAUDE.md` above
     the story's **Files** paths or the files its diff changed), their
     average size, and the largest with its agent run and role; `none
     pre-loaded` when none did. An older record has no such line.
```

In the spec:
- §2.3's line goes "after `- Engine:`", not after `- Ended:`, and ends `most <max> KB (<agent run>, <role>)`;
- §3's `flow/PROVENANCE.md` row reads "one dated entry under **All agents** naming the six engine agents; no pin changes";
- §3's `internal/cli/CLAUDE.md` row says the room comes from moving the two flow-agent rules' evidence into a new guide, `docs/claude/flow-agent-jobs.md`, which gets a row of its own.

- [ ] **Step 4: Run the tests**

Run: `loom job run -d "flowrun and cli tests" -- sh -c 'CGO_ENABLED=0 go test ./internal/flowrun/ && CGO_ENABLED=0 go test ./internal/cli/ -run "Flow|Agent|RunFX"'`
Expected: ok. `TestRecord`'s golden file is unchanged, since its inputs carry no rules.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(flow): the run's record says what package rules its agents pre-loaded" -- internal/flowrun/record.go internal/flowrun/record_test.go internal/cli/flow_run.go internal/cli/flow_run_test.go flow/plugin/docs/records.md docs/superpowers/specs/2026-11-14-flow-package-rules-preload-design.md
```

---

### Task 5: The contract test, the rules, the guide and provenance

**Files:**
- Create: `session/claude_flow_rules_realclaude_test.go`
- Create: `docs/claude/flow-agent-jobs.md`
- Modify: `docs/claude/INDEX.md`
- Modify: `internal/cli/CLAUDE.md` (the headless-agent rule, line 37; the pins rule, line 38)
- Modify: `flow/PROVENANCE.md` (**All agents**)

- [ ] **Step 1: Write the contract test**

Create `session/claude_flow_rules_realclaude_test.go`:

```go
package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rulesProbePlugin is the package-rules probe's plugin: one haiku agent
// that answers from its instructions and reads a file only when told to.
var rulesProbePlugin = map[string]string{
	".claude-plugin/plugin.json": `{"name": "rulesprobe", "version": "0.0.1", "description": "loom package-rules probe"}` + "\n",
	"agents/probe-rules.md": "---\nname: probe-rules\ndescription: Probe agent for loom's package-rules test. Use only when told to.\nmodel: haiku\neffort: low\ntools: Read\n---\n\n" +
		"Answer what you are asked, briefly. Use a tool only when the prompt tells you to.\n",
}

// TestRealClaude_FlowAgentPreloadsPackageRules checks the premise of the
// run engine's package rules
// (docs/superpowers/specs/2026-11-14-flow-package-rules-preload-design.md
// §1) against the claude on PATH, as the default account: --add-dir with
// CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD=1 loads the directory's
// CLAUDE.md into an --agent run at startup, without them it doesn't, and a
// later Read under the directory loads it no second time. It spends a few
// cents of haiku, so it only runs with LOOM_TEST_REAL_CLAUDE=1.
func TestRealClaude_FlowAgentPreloadsPackageRules(t *testing.T) {
	if os.Getenv("LOOM_TEST_REAL_CLAUDE") != "1" {
		t.Skip("set LOOM_TEST_REAL_CLAUDE=1 to run against real Claude (costs money)")
	}
	for _, bin := range []string{"claude", "git"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
	root := t.TempDir()
	plugin := filepath.Join(root, "rulesprobe")
	for rel, content := range rulesProbePlugin {
		p := filepath.Join(plugin, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	}
	work := filepath.Join(root, "work")
	pkg := filepath.Join(work, "pkg")
	require.NoError(t, os.MkdirAll(filepath.Join(pkg, "src"), 0o700))
	require.NoError(t, exec.Command("git", "-C", work, "init", "-q").Run())
	require.NoError(t, os.WriteFile(filepath.Join(pkg, "CLAUDE.md"), []byte("# pkg rules\n\nThe codeword is HERON-3.\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(pkg, "src", "a.txt"), []byte("x\n"), 0o600))

	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if name == "CLAUDE_CONFIG_DIR" || name == "CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD" || probeScrubbed(name) {
			continue
		}
		env = append(env, kv)
	}
	run := func(prompt string, preload bool) (string, string) {
		t.Helper()
		args := []string{"-p", prompt, "--agent", "rulesprobe:probe-rules", "--plugin-dir", plugin, "--output-format", "stream-json", "--verbose"}
		cmdEnv := slices.Clone(env)
		if preload {
			args = append(args, "--add-dir", pkg)
			cmdEnv = append(cmdEnv, "CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD=1")
		}
		cmd := exec.Command("claude", args...)
		cmd.Dir, cmd.Env = work, cmdEnv
		out, err := cmd.Output()
		require.NoError(t, err, string(out))
		_, result := agentProbeEvents(t, out)
		require.NotNil(t, result, string(out))
		text, _ := result["result"].(string)
		session, _ := result["session_id"].(string)
		return text, session
	}

	ask := "Without using any tool: what codeword do your instructions give? Answer with the codeword only, or NONE."
	got, _ := run(ask, true)
	assert.Contains(t, got, "HERON-3", "--add-dir with the variable loads the package's CLAUDE.md at startup")
	got, _ = run(ask, false)
	assert.NotContains(t, got, "HERON-3", "without them the package's CLAUDE.md waits for a touch")

	_, session := run("Read the file pkg/src/a.txt with the Read tool, then answer DONE.", true)
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	found, err := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", session+".jsonl"))
	require.NoError(t, err)
	require.Len(t, found, 1, "the read's transcript")
	b, err := os.ReadFile(found[0])
	require.NoError(t, err)
	assert.Contains(t, string(b), `"name":"Read"`, "the agent read under the package")
	for _, line := range strings.Split(string(b), "\n") {
		assert.False(t, strings.Contains(line, `"nested_memory"`) && strings.Contains(line, "pkg/CLAUDE.md"),
			"a Read under a pre-loaded directory loads its CLAUDE.md no second time")
	}
}
```

- [ ] **Step 2: Run it against real Claude, once**

Run: `loom job run -d "real-Claude package rules probe" -- sh -c 'LOOM_TEST_REAL_CLAUDE=1 CGO_ENABLED=0 go test ./session/ -run TestRealClaude_FlowAgentPreloadsPackageRules -v'`
Expected: PASS, for a few cents of haiku. Without the variable it SKIPs, and it compiles in the normal suite.

- [ ] **Step 3: Write the guide**

Create `docs/claude/flow-agent-jobs.md`:

```markdown
# Flow agent jobs: what `--agent` leaves out

`loom flow agent` and `/flow:run`'s engine run each flow agent as a headless `claude -p --agent flow:<role>` job (`agentCommand`, `internal/cli/flow_agent_exec.go`). `--agent` runs the definition's prompt as the main thread, which is not what a subagent gets, so the job adds back what it drops. Each item was probed against real Claude, and each probe is an opt-in test (`LOOM_TEST_REAL_CLAUDE=1`).

| What `--agent` does | What the job does | Probe |
|---|---|---|
| Doesn't preload the definition's `skills:` | inlines them into the brief (`preloadedSkills`) | 2026-11-06, `TestRealClaude_FlowAgentRun` |
| Ignores `--json-schema` | ends the report in status lines (`headlessPreamble`, `statusLines`) | 2026-11-06, `TestRealClaude_FlowAgentRun` |
| Ignores the definition's `maxTurns` | passes `--max-turns`; an `error_max_turns` result is `TURN_CAP`, which `loom flow agent answer` resumes | 2026-11-06, `TestRealClaude_FlowAgentRun` |
| Inherits the parent session's messaging socket and mod link, and so acts as its child | scrubs them from the environment (`scrubbedVar`) | 2026-11-06, `TestRealClaude_FlowAgentRun` |
| Runs the account's enabled plugins, whose hooks inject what no subagent gets | turns each one off in `--settings` (`pluginsOff`) | 2026-11-06, `TestRealClaude_FlowAgentRun` |
| Takes the account's effort (medium on Haiku), whatever the definition pins | passes `--effort` from `agentEffort`, on a resume too | 2026-11-11 retro: agents ran below their pin in 7 of 10 engine runs |
| Writes a one-hour prompt cache, whatever the definition pins | sets `FORCE_PROMPT_CACHING_5M=1` for `cacheTtl: 5m` (`agentEnv`) | 2026-11-11 |
| Loads a package's `CLAUDE.md` only when a tool touches a path under it | the engine passes an `--add-dir` for each directory above the agent's scope's files that holds a `CLAUDE.md`, with `CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD=1` (`agentRules`); a later Read there loads it no second time | 2026-11-14, `TestRealClaude_FlowAgentPreloadsPackageRules`: 42–54% of developer and reviewer runs had loaded none of their package rules |

The 2026-11-14 check also found that `--append-system-prompt-file` reaches an `--agent` run too, but its text isn't tracked as loaded, so a later read would load the same `CLAUDE.md` again. Prefer `--add-dir` for anything that is a `CLAUDE.md`.

A Claude Code upgrade can change any of these: re-run the probes after one (`LOOM_TEST_REAL_CLAUDE=1 CGO_ENABLED=0 go test ./session/ -run 'TestRealClaude_Flow'`).
```

In `docs/claude/INDEX.md`, add a row or line in the file's own format: "`flow-agent-jobs.md` — what `claude --agent` leaves out of a flow agent job, what the job adds back, and the probe for each."

- [ ] **Step 4: Rewrite the two flow-agent rules in `internal/cli/CLAUDE.md`**

The file is 34 bytes under its 20,000-byte budget, so the rules' evidence moves to the guide. Replace line 37 (the rule beginning `- **Run a flow agent headless`) with:

```markdown
- **Run a flow agent headless (`loom flow agent`) with its submitter's environment less the parent Claude session's variables (`scrubbedVar`), the account's and project's plugins off (`pluginsOff`, refusing a settings file it can't read), its definition's preloaded skills inlined (`preloadedSkills`) and its `maxTurns` as `--max-turns`; read its report only from its `output.log`'s result once its view says it ended, and fail a run whose report has no valid status (its last `STATUS` line, else a bare status word) or a NEEDS_CONTEXT with no `QUESTION` line.** `--agent` applies only part of a definition and inherits the parent's links, so each omission runs the agent with less than its definition, or as the parent's child, silently ([`../../docs/claude/flow-agent-jobs.md`](../../docs/claude/flow-agent-jobs.md)); a guessed report sends unverified work on. **Enforced** by `TestFlowAgent_RunWaitDone`, `TestFlowAgent_AJobWithoutAUsableReportFails`, `TestFlowAgent_ATurnCapIsResumable`, `TestAgentArgv_RolePreamblesAndMaxTurns`, `TestScrubbedVar` and `TestStatusLines`.
```

Replace line 38 (the rule beginning `- **Give every agent job its definition's pins`) with:

```markdown
- **Give every agent job its definition's pins and, from the engine, its package rules: `--effort` from `agentEffort` (the run's own, else the pin, on a resume too), `FORCE_PROMPT_CACHING_5M=1` for `cacheTtl: 5m` (`agentEnv`, `frontmatterCacheTTL`), and an `--add-dir` per directory `agentRules` finds above its scope's files, with `CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD=1`, kept for a resume; an unreadable diff fails the step.** Without them about half the engine's agents ran below their pin or blind to their package's rules. **Enforced** by `TestFlowAgent_ThePinnedEffortReachesEveryJob`, `TestFlowAgent_AFiveMinutePinReachesTheJob`, `TestAgentRules`, `TestAnswerArgv_KeepsTheRunsRules` and `TestFlowRun_AgentsPreloadTheirPackageRules`.
```

Run: `go run ./tools/claudemd -v 2>&1 | grep -E 'internal/cli/CLAUDE.md|internal/flowrun/CLAUDE.md|FAIL|error'`
Expected: `internal/cli/CLAUDE.md` at or under 20000 bytes, and no failure.

- [ ] **Step 5: Provenance**

Under `## All agents` in `flow/PROVENANCE.md`, append:

```markdown
- 2026-11-14, package rules under the run engine: each developer, fixer, story, code-quality and whole-implementation review and doc writer the engine dispatches now starts with the package `CLAUDE.md` files above its files loaded (an `--add-dir` per directory with `CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD=1`, `agentRules`, `internal/cli/flow_rules.go`); its files are the story's **Files** paths and its diff (`docs/superpowers/specs/2026-11-14-flow-package-rules-preload-design.md` §2.1); no pin changed. On the owner's instruction, from the 2026-11-14 check of 1,043 headless engine runs of 2026-11-06 to 2026-11-13: of the runs with a package `CLAUDE.md` over the files they changed or reviewed, intermediate-dev loaded none of them in 42%, story-reviewer in 54%, code-quality-reviewer in 48% and doc-writer in 31%; only 13–34% loaded all. Developers write through Bash (24 of 526 used Edit or Write) and reviewers read through `git diff`, which load no rules. **Wrong if:** the next retro's transcript scan finds fewer than 95% of engine agent runs with every covering package `CLAUDE.md` loaded, or `loom flow costs records` shows engine runs' cost per story up more than 20% over the runs before.
```

- [ ] **Step 6: Run every gate**

Run: `bash flow/lib/test.sh`
Expected: the suite OK, and the agent lint passes.

Run: `loom job run -d "go gates for package rules" -- sh -c 'CGO_ENABLED=0 go test ./internal/flowrun/ ./internal/cli/ ./flow/ ./tools/claudemd/ ./session/ && CGO_ENABLED=0 go vet ./internal/... ./session/ && gofmt -l internal/flowrun internal/cli session'`
Expected: every package ok, vet clean, and no file listed by `gofmt -l`.

Run: `loom job run -d "flowrun race" -- sh -c 'CC=clang CGO_ENABLED=1 go test -race ./internal/flowrun/'`
Expected: ok. The engine's tracks share the fake, and `scopes` is written under its lock.

- [ ] **Step 7: Commit**

```bash
git add session/claude_flow_rules_realclaude_test.go docs/claude/flow-agent-jobs.md
git commit -m "docs(flow): the package rules contract test, the flow-agent-jobs guide and provenance" -- session/claude_flow_rules_realclaude_test.go docs/claude/flow-agent-jobs.md docs/claude/INDEX.md internal/cli/CLAUDE.md flow/PROVENANCE.md
```
