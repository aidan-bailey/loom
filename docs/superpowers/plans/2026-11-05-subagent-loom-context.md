# Subagent Loom Context Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every subagent (but a fork) of a Claude session that got loom's context starts with a subagent's cut of it, through a `SubagentStart` hook's `additionalContext`.

**Architecture:** The shared tail of the loom-context prompt splits into "Loom itself" and "Other sessions"; a subagent cut is a new subagent head, the parent's head and "Loom itself". `hooks.Prepare` writes the cut as the hook output into the per-launch hooks folder and registers a second `SubagentStart` entry, matcher `^(?!fork$)`, that prints it. `launchProgram` passes the cut exactly when the parent gets loom's context flag.

**Tech Stack:** Go, testify, embedded markdown fragments, Claude Code command hooks.

Spec: [`../specs/2026-11-05-subagent-loom-context-design.md`](../specs/2026-11-05-subagent-loom-context-design.md). Execution: inline (the owner, 2026-11-05).

**Sequencing.** Packages A–D build on main. Package E merges `aidanb/loom-doesnt-know-subagents` once its Package D (display, context and docs) has landed, and rewords the work-queue paragraph; until E is done this branch must not merge, since the brief would tell subagents that a job's message reaches them.

**Gates for every package:** `CGO_ENABLED=0 go test` on the packages it touches; `gofmt -l` on the files it touches is empty. Full suite (Package D and E): `loom job submit -d "full test suite" -- env CGO_ENABLED=0 go test ./...`.

---

## File map

| File | Package | Change |
|---|---|---|
| `session/loom_context_shared.md` | A | keeps "## Loom itself" only |
| `session/loom_context_sessions.md` | A | new: "## Other sessions", moved verbatim |
| `session/loom_context_subagent.md` | A | new: the subagent head |
| `session/loom_context.go` | A, C | embeds; `loomContextFile` joins three; `subagentContextFile`; `loomContextPath`; `subagentBrief` |
| `session/loom_context_test.go` | A, C | fragment checks; the cut's tests; `subagentBrief`'s |
| `internal/cli/loom_context_test.go` | A | reads both fragments |
| `session/hooks/hooks.go` | B | brief file, command, matcher; `SettingsJSON` and `Prepare` take it |
| `session/hooks/hooks_test.go` | B | brief tests; callers |
| `core/hook_scan_test.go`, `core/mockup_test.go`, `tools/fakeagent/hooks_test.go`, `session/claude_hooks_realclaude_test.go`, `session/claude_memory_realclaude_test.go`, `session/subagent/realclaude_test.go` | B | `hooks.Prepare` gains a third argument |
| `session/subagent_hooks.go` | B, C | `prepareHooks` takes the brief; `launchProgram` passes it |
| `session/subagent_hooks_test.go` | C | the launch gate's test |
| `session/claude_subagent_brief_realclaude_test.go` | C | new: opt-in real-Claude test |
| `session/hooks/CLAUDE.md` | D | new: the hooks rules |
| `session/CLAUDE.md`, `internal/cli/CLAUDE.md`, `session/README.md` | D | fragment names, the cut, the brief |
| `docs/superpowers/specs/2026-11-05-subagent-loom-context-design.md` | D, E | `loomContextPath` in place of `(string, bool)`; status |

---

## Package A: the fragments and the subagent cut

**Files:** `session/loom_context_shared.md`, `session/loom_context_sessions.md` (new), `session/loom_context_subagent.md` (new), `session/loom_context.go`, `session/loom_context_test.go`, `internal/cli/loom_context_test.go`.

- [ ] **A1: Write the failing tests.** In `session/loom_context_test.go`, replace the fragment loop and the equality in `TestWriteLoomContextFiles`:

```go
	for _, frag := range [][]byte{loomContextWorktreeHead, loomContextMainHead, loomContextShared, loomContextSessions, loomContextSubagentHead} {
		assert.True(t, bytes.HasSuffix(frag, []byte("\n")) && !bytes.HasSuffix(frag, []byte("\n\n")),
			"a loom-context fragment must end in exactly one newline")
	}
	for path, head := range map[string][]byte{wt: loomContextWorktreeHead, ws: loomContextMainHead} {
		b, err := os.ReadFile(path)
		assert.NoError(t, err)
		assert.Contains(t, string(b), "Loom session context")
		assert.Equal(t, string(head)+"\n"+string(loomContextShared)+"\n"+string(loomContextSessions), string(b), path)
	}
```

(the comment above them becomes: "Each written file is its variant's head, the shared fragment, then the sessions fragment; every fragment ends in exactly one newline, so each seam is one blank line."). Append:

```go
// A subagent's cut is the subagent head, then its parent's head and the
// shared fragment. The sessions fragment is the parent's alone: messaging
// and starting sessions is its job.
func TestSubagentContext_IsTheParentsWithoutOtherSessions(t *testing.T) {
	for name, head := range map[string][]byte{"worktree": loomContextWorktreeHead, "workspace": loomContextMainHead} {
		parent := string(loomContextFile(head))
		cut := string(subagentContextFile(head))

		assert.Equal(t, string(loomContextSubagentHead)+"\n"+string(head)+"\n"+string(loomContextShared), cut, name)
		assert.Contains(t, cut, "**Heavy work goes through loom's work queue.**", name)
		assert.Contains(t, parent, "## Other sessions", name)
		assert.NotContains(t, cut, "## Other sessions", name)
		assert.NotContains(t, cut, "loom session send", name)
	}
}

// Claude passes a SubagentStart hook's additionalContext inline only up to
// 10,000 characters; past that it writes the text to a file and hands the
// subagent a 2,000-character preview. A byte count bounds the characters.
func TestSubagentContext_FitsClaudesInlineCap(t *testing.T) {
	for name, head := range map[string][]byte{"worktree": loomContextWorktreeHead, "workspace": loomContextMainHead} {
		assert.Less(t, len(subagentContextFile(head)), 10000, name)
	}
}
```

- [ ] **A2: Run them to see them fail.** `CGO_ENABLED=0 go test ./session -run 'TestWriteLoomContextFiles|TestSubagentContext'`. Expected: build failure (`loomContextShared`, `loomContextSessions`, `loomContextSubagentHead`, `subagentContextFile` undefined).

- [ ] **A3: Split the fragment and add the head.** "## Other sessions" starts at line 7 of `session/loom_context_shared.md` (line 6 is the blank line before it):

```sh
sed -n '7,$p' session/loom_context_shared.md > session/loom_context_sessions.md
sed -i '6,$d' session/loom_context_shared.md
```

Check that `session/loom_context_shared.md` now starts with `## Loom itself`, ends with the work-queue bullet and one newline, and that `session/loom_context_sessions.md` starts with `## Other sessions`. Create `session/loom_context_subagent.md`:

```markdown
# Loom context for a subagent

You are a subagent of a Claude session that loom runs. That session's loom context follows, and its rules bind you as they bind it. Leave other loom sessions to your parent: don't message, start, pause or kill them, and tell your parent when one should hear from it.
```

- [ ] **A4: Compose.** In `session/loom_context.go`, replace the embed block and `loomContextFile`:

```go
// The embedded fragments the prompt files are built from.
var (
	//go:embed loom_context_worktree.md
	loomContextWorktreeHead []byte
	//go:embed loom_context_main.md
	loomContextMainHead []byte
	//go:embed loom_context_shared.md
	loomContextShared []byte
	//go:embed loom_context_sessions.md
	loomContextSessions []byte
	//go:embed loom_context_subagent.md
	loomContextSubagentHead []byte
)

// loomContextFile is a prompt file's content: head, then the shared
// fragment ("Loom itself"), then the sessions fragment ("Other
// sessions"), with a blank line at each seam (every fragment ends in one
// newline). bytes.Join builds a slice of its own, so no append ever
// writes into an embedded one.
func loomContextFile(head []byte) []byte {
	return bytes.Join([][]byte{head, loomContextShared, loomContextSessions}, []byte("\n"))
}

// subagentContextFile is a subagent's cut of the prompt whose head is
// head: the subagent head, then head and the shared fragment. The
// sessions fragment stays the parent's, since messaging and starting
// sessions is its job. Loom's SubagentStart hook hands it to every
// subagent but a fork (subagentBrief, hooks.Prepare).
func subagentContextFile(head []byte) []byte {
	return bytes.Join([][]byte{loomContextSubagentHead, head, loomContextShared}, []byte("\n"))
}
```

In the file's top comment, "Each is its own head followed by the tail both share (loomContextFile)" becomes "Each is its own head followed by the fragments both share (loomContextFile)".

- [ ] **A5: Point the CLI test at both fragments.** In `internal/cli/loom_context_test.go`, replace the constant and the read:

```go
// loomContextFragments are the loom-context fragments that teach
// `loom job` ("Loom itself") and `loom session` ("Other sessions") to
// every Claude session loom launches (session package,
// WriteLoomContextFiles).
var loomContextFragments = []string{"../../session/loom_context_shared.md", "../../session/loom_context_sessions.md"}
```

and at the top of `TestLoomContext_NamesRealSessionFlags`:

```go
	var text strings.Builder
	for _, path := range loomContextFragments {
		b, err := os.ReadFile(path)
		require.NoError(t, err)
		text.Write(b)
	}
```

with the loop reading `text.String()` in place of `string(b)`. Any other use of `loomContextShared` in the file reads the same way (`grep -n loomContextShared internal/cli/*_test.go`).

- [ ] **A6: Run the tests.** `CGO_ENABLED=0 go test ./session -run 'TestWriteLoomContextFiles|TestSubagentContext|TestLoomContext' && CGO_ENABLED=0 go test ./internal/cli -run 'TestLoomContext|TestJobContext'`. Expected: PASS. Mutation check: drop `loomContextShared` from `subagentContextFile`'s join and see `TestSubagentContext_IsTheParentsWithoutOtherSessions` fail; restore.

- [ ] **A7: Commit.**

```sh
gofmt -l session/loom_context.go session/loom_context_test.go internal/cli/loom_context_test.go
git add -- session/loom_context_shared.md session/loom_context_sessions.md session/loom_context_subagent.md session/loom_context.go session/loom_context_test.go internal/cli/loom_context_test.go
git commit -m "feat(session): a subagent's cut of the loom-context prompt" -- session/loom_context_shared.md session/loom_context_sessions.md session/loom_context_subagent.md session/loom_context.go session/loom_context_test.go internal/cli/loom_context_test.go
```

---

## Package B: the hook that hands the brief over

**Files:** `session/hooks/hooks.go`, `session/hooks/hooks_test.go`, `session/subagent_hooks.go` and the `hooks.Prepare` callers in the file map.

- [ ] **B1: Write the failing tests.** In `session/hooks/hooks_test.go`, every `SettingsJSON(x, y)` becomes `SettingsJSON(x, y, "")` and every `Prepare(x, y)` becomes `Prepare(x, y, "")`. Append (`bytes`, `encoding/json`, `os`, `os/exec` and `path/filepath` are already imported):

```go
// A brief adds a second SubagentStart entry, for every agent type but a
// fork, and leaves the recorder's entry as it was; without one the
// settings are the recorder's alone (TestSettingsJSON_Golden).
func TestSettingsJSON_BriefsSubagentsButNotForks(t *testing.T) {
	got, err := SettingsJSON("/e", "", "/h/subagent-context.json")
	require.NoError(t, err)
	var doc settingsDoc
	require.NoError(t, json.Unmarshal(got, &doc))
	start := doc.Hooks[EventSubagentStart]
	require.Len(t, start, 2)
	assert.Equal(t, hookMatcher{Hooks: []hookCommand{{Type: "command", Command: HookCommand("/e")}}}, start[0],
		"the recorder still sees every agent, forks included")
	assert.Equal(t, hookMatcher{Matcher: "^(?!fork$)", Hooks: []hookCommand{{Type: "command", Command: briefCommand("/h/subagent-context.json")}}}, start[1])
	for _, name := range HookEvents {
		if name != EventSubagentStart {
			assert.Len(t, doc.Hooks[name], 1, name)
		}
	}

	got, err = SettingsJSON("/e", "", "")
	require.NoError(t, err)
	var bare settingsDoc
	require.NoError(t, json.Unmarshal(got, &bare))
	assert.Len(t, bare.Hooks[EventSubagentStart], 1)
	assert.NotContains(t, string(got), "matcher")
}

// The brief command prints the hook output loom wrote, and with the file
// gone prints nothing. Either way it reads its whole input and exits 0,
// as HookCommand does: Claude pipes the payload to it.
func TestBriefCommand_ReadsItsInputAndExitsZero(t *testing.T) {
	requireShell(t)
	dir := filepath.Join(t.TempDir(), "with space")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	present := filepath.Join(dir, "subagent-context.json")
	require.NoError(t, os.WriteFile(present, []byte(`{"hookSpecificOutput":{}}`), 0o600))

	for path, want := range map[string]string{present: `{"hookSpecificOutput":{}}`, filepath.Join(dir, "gone.json"): ""} {
		r, w, err := os.Pipe()
		require.NoError(t, err)
		var out bytes.Buffer
		cmd := exec.Command("sh", "-c", briefCommand(path))
		cmd.Stdin, cmd.Stdout = r, &out
		require.NoError(t, cmd.Start())
		require.NoError(t, r.Close())
		_, writeErr := w.Write(bytes.Repeat([]byte("x"), 1<<20)) // larger than a pipe buffer
		require.NoError(t, w.Close())

		assert.NoError(t, cmd.Wait(), path)
		assert.NoError(t, writeErr, "the command must consume its whole input")
		assert.Equal(t, want, out.String(), path)
	}
}

// Prepare writes a brief as the SubagentStart output the brief command
// prints, and registers the command; with no brief it writes neither.
func TestPrepare_WritesTheSubagentBrief(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "loom_x")
	brief := "Commit by path <always> & keep scratch files out."
	_, err := Prepare(dir, "", brief)
	require.NoError(t, err)

	b, err := os.ReadFile(BriefPath(dir))
	require.NoError(t, err)
	var out map[string]map[string]string
	require.NoError(t, json.Unmarshal(b, &out))
	assert.Equal(t, map[string]string{"hookEventName": "SubagentStart", "additionalContext": brief}, out["hookSpecificOutput"])
	settings, err := os.ReadFile(SettingsPath(dir))
	require.NoError(t, err)
	want, err := SettingsJSON(EventsDir(dir), "", BriefPath(dir))
	require.NoError(t, err)
	assert.Equal(t, string(want), string(settings))

	_, err = Prepare(dir, "", "")
	require.NoError(t, err)
	assert.NoFileExists(t, BriefPath(dir))
	settings, err = os.ReadFile(SettingsPath(dir))
	require.NoError(t, err)
	assert.NotContains(t, string(settings), "subagent-context.json")
}
```

- [ ] **B2: Run them to see them fail.** `CGO_ENABLED=0 go test ./session/hooks`. Expected: build failure (too many arguments; `briefCommand`, `BriefPath`, `hookMatcher.Matcher` undefined).

- [ ] **B3: Implement.** In `session/hooks/hooks.go`:

Add to the layout constants `briefFile    = "subagent-context.json"`, and after `EventsDir`:

```go
// BriefPath is the file briefCommand prints: the SubagentStart hook output
// that hands a subagent loom's context.
func BriefPath(dir string) string { return filepath.Join(dir, briefFile) }

// briefMatcher matches every agent type but a fork, which already holds
// its parent's whole prompt. Claude Code reads a matcher holding any
// character but letters, digits, _, -, spaces, "," and "|" as an
// unanchored JavaScript regex; Go's regexp has no lookahead, so only the
// opt-in TestRealClaude_SubagentBrief can pin what it matches.
const briefMatcher = "^(?!fork$)"

// briefCommand returns the shell command that hands a subagent the brief
// at briefPath, which holds the whole hook output. Like HookCommand it
// reads its whole input first and always exits 0, so Claude never fails
// writing the payload or reports a hook error; with the file gone it
// prints nothing, and the subagent starts unbriefed. briefPath must
// satisfy SafePath.
func briefCommand(briefPath string) string {
	return `cat >/dev/null; cat '` + briefPath + `' 2>/dev/null; exit 0`
}
```

`hookMatcher` gains a matcher, first so it encodes first:

```go
type hookMatcher struct {
	Matcher string        `json:"matcher,omitempty"`
	Hooks   []hookCommand `json:"hooks"`
}
```

`SettingsJSON` takes the brief's path:

```go
// SettingsJSON returns the settings file registering HookCommand for every
// event in HookEvents. Claude adds these hooks to the user's own. A
// non-empty autoMemoryDir also sets Claude's autoMemoryDirectory, which
// outranks the user's own settings for this launch. A non-empty briefPath
// adds a second SubagentStart entry, for every agent type but a fork
// (briefMatcher), running briefCommand on it.
func SettingsJSON(eventsDir, autoMemoryDir, briefPath string) ([]byte, error) {
	cmd := HookCommand(eventsDir)
	doc := settingsDoc{Hooks: make(map[string][]hookMatcher, len(HookEvents)), AutoMemoryDirectory: autoMemoryDir}
	for _, name := range HookEvents {
		doc.Hooks[name] = []hookMatcher{{Hooks: []hookCommand{{Type: "command", Command: cmd}}}}
	}
	if briefPath != "" {
		doc.Hooks[EventSubagentStart] = append(doc.Hooks[EventSubagentStart], hookMatcher{
			Matcher: briefMatcher,
			Hooks:   []hookCommand{{Type: "command", Command: briefCommand(briefPath)}},
		})
	}
	// … the encoder as before
```

`Prepare` takes the brief and writes it before the settings:

```go
// Prepare empties dir and writes a fresh settings.json (with
// autoMemoryDir, when set), events folder and launch-id for a new launch,
// returning the launch ID. A non-empty brief is what each subagent of the
// launch but a fork is told at its start: Prepare writes it as the hook
// output briefCommand prints (BriefPath) and registers that command.
// Events from an earlier launch describe agents that no longer exist, so
// nothing is kept. launch-id is written last, so a concurrent scan never
// sees a new ID without its settings.
func Prepare(dir, autoMemoryDir, brief string) (string, error) {
	// … SafePath, RemoveAll and MkdirAll as before
	briefPath := ""
	if brief != "" {
		briefPath = BriefPath(dir)
		if err := writeBrief(briefPath, brief); err != nil {
			return "", err
		}
	}
	settings, err := SettingsJSON(EventsDir(dir), autoMemoryDir, briefPath)
	// … the rest as before
}

// writeBrief writes path as the SubagentStart hook output whose
// additionalContext is brief.
func writeBrief(path, brief string) error {
	type output struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	}
	b, err := json.Marshal(struct {
		HookSpecificOutput output `json:"hookSpecificOutput"`
	}{output{EventSubagentStart, brief}})
	if err != nil {
		return fmt.Errorf("hooks: encode subagent brief: %w", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("hooks: write subagent brief: %w", err)
	}
	return nil
}
```

- [ ] **B4: Update the callers.** `session/subagent_hooks.go`'s `hooks.Prepare(dir, memoryDir)` becomes `hooks.Prepare(dir, memoryDir, "")` (Package C passes the brief). In every test caller:

```sh
perl -pi -e 's/hooks\.Prepare\(([^,()]+), ("[^"]*"|\w+)\)/hooks.Prepare($1, $2, "")/g' core/hook_scan_test.go core/mockup_test.go tools/fakeagent/hooks_test.go session/claude_hooks_realclaude_test.go session/claude_memory_realclaude_test.go session/subagent/realclaude_test.go
grep -rn 'hooks\.Prepare(' --include='*.go' . | grep -v ', "")\|brief)'
```

The grep prints only `session/subagent_hooks.go` (already done) or nothing.

- [ ] **B5: Run the tests.** `CGO_ENABLED=0 go test ./session/hooks ./session ./core ./tools/fakeagent && CGO_ENABLED=0 go vet ./session/... ./core ./tools/fakeagent`. Expected: PASS. Mutation checks: drop `cat >/dev/null; ` from `briefCommand` and see `TestBriefCommand_ReadsItsInputAndExitsZero` fail on `writeErr`; drop the `Matcher` and see `TestSettingsJSON_BriefsSubagentsButNotForks` fail; restore each.

- [ ] **B6: Commit.**

```sh
gofmt -l session/hooks/hooks.go session/hooks/hooks_test.go session/subagent_hooks.go core/hook_scan_test.go core/mockup_test.go tools/fakeagent/hooks_test.go session/claude_hooks_realclaude_test.go session/claude_memory_realclaude_test.go session/subagent/realclaude_test.go
git commit -m "feat(session): a SubagentStart hook that hands every subagent but a fork a brief" -- session/hooks/hooks.go session/hooks/hooks_test.go session/subagent_hooks.go core/hook_scan_test.go core/mockup_test.go tools/fakeagent/hooks_test.go session/claude_hooks_realclaude_test.go session/claude_memory_realclaude_test.go session/subagent/realclaude_test.go
```

---

## Package C: brief a launch's subagents when it gets loom's context

**Files:** `session/loom_context.go`, `session/loom_context_test.go`, `session/subagent_hooks.go`, `session/subagent_hooks_test.go`, `session/claude_subagent_brief_realclaude_test.go` (new).

- [ ] **C1: Write the failing tests.** Append to `session/loom_context_test.go`:

```go
// A launch's subagents get the cut of the prompt the launch itself gets,
// and none when it gets none: the feature off, its file missing, or a
// program other than Claude.
func TestSubagentBrief_FollowsTheParentsContext(t *testing.T) {
	dir := t.TempDir()
	assert.NoError(t, WriteLoomContextFiles(dir))
	SetLoomContextEnabled(dir, true)

	assert.Equal(t, string(subagentContextFile(loomContextWorktreeHead)), subagentBrief("claude", dir, false))
	assert.Equal(t, string(subagentContextFile(loomContextMainHead)), subagentBrief("claude", dir, true))
	assert.Empty(t, subagentBrief("aider", dir, false))

	missing := t.TempDir()
	SetLoomContextEnabled(missing, true)
	assert.Empty(t, subagentBrief("claude", missing, false), "no prompt file, no flag, no brief")

	SetLoomContextEnabled(dir, false)
	assert.Empty(t, subagentBrief("claude", dir, false))
}
```

Append to `session/subagent_hooks_test.go` (imports `encoding/json`, `errors`, `io/fs` as needed):

```go
// The launch hands hooks.Prepare the cut subagentBrief picks: the
// worktree cut, the workspace cut for a workspace terminal, none when
// loom's context is off.
func TestLaunchProgram_BriefsSubagentsOnlyWithTheLoomContext(t *testing.T) {
	cases := []struct {
		name          string
		on, workspace bool
		want          []byte // nil: no brief
	}{
		{"worktree", true, false, subagentContextFile(loomContextWorktreeHead)},
		{"workspace terminal", true, true, subagentContextFile(loomContextMainHead)},
		{"context off", false, false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inst := hooksInstance(t, "claude")
			inst.IsWorkspaceTerminal = tc.workspace
			require.NoError(t, WriteLoomContextFiles(inst.ConfigDir))
			SetLoomContextEnabled(inst.ConfigDir, tc.on)

			got := inst.launchProgram(LaunchEnv{Program: "claude"}, true)

			require.Contains(t, got, settingsFlag(inst))
			b, err := os.ReadFile(hooks.BriefPath(SubagentHooksDir(inst.ConfigDir, inst.Title)))
			if tc.want == nil {
				assert.True(t, errors.Is(err, fs.ErrNotExist), "no brief file: %v", err)
				return
			}
			require.NoError(t, err)
			var out map[string]map[string]string
			require.NoError(t, json.Unmarshal(b, &out))
			assert.Equal(t, string(tc.want), out["hookSpecificOutput"]["additionalContext"])
		})
	}
}
```

- [ ] **C2: Run them to see them fail.** `CGO_ENABLED=0 go test ./session -run 'TestSubagentBrief|TestLaunchProgram_BriefsSubagents'`. Expected: build failure (`subagentBrief` undefined).

- [ ] **C3: Implement the gate.** In `session/loom_context.go`, replace `loomContextProgram` with:

```go
// loomContextPath is the prompt file a launch in configDir points Claude
// at, and whether it gets one: the feature is on for configDir and the
// variant's file exists (fail-safe: never point Claude at a nonexistent
// file). Workspace terminals get the workspace variant, the rest the
// worktree variant.
func loomContextPath(configDir string, isWorkspaceTerminal bool) (string, bool) {
	if configDir == "" || !LoomContextEnabled(configDir) {
		return "", false
	}
	name := loomContextFileWorktree
	if isWorkspaceTerminal {
		name = loomContextFileWorkspace
	}
	path := filepath.Join(configDir, name)
	if _, err := os.Stat(path); err != nil {
		return "", false
	}
	return path, true
}

// loomContextProgram wraps program with the loom-context flag for launch,
// pointing at loomContextPath's file. Returns program unchanged when there
// is none. Non-Claude programs are a no-op via BuildLoomContextCommand.
func loomContextProgram(program, configDir string, isWorkspaceTerminal bool) string {
	path, ok := loomContextPath(configDir, isWorkspaceTerminal)
	if !ok {
		return program
	}
	return BuildLoomContextCommand(program, path)
}

// subagentBrief is what loom's SubagentStart hook tells each subagent of
// a program launched in configDir: the cut (subagentContextFile) of the
// prompt loomContextProgram gives the program itself, or "" when it gives
// none (the feature off, the file missing, a program other than Claude).
func subagentBrief(program, configDir string, isWorkspaceTerminal bool) string {
	if _, ok := loomContextPath(configDir, isWorkspaceTerminal); !ok || !IsClaudeProgram(program) {
		return ""
	}
	head := loomContextWorktreeHead
	if isWorkspaceTerminal {
		head = loomContextMainHead
	}
	return string(subagentContextFile(head))
}
```

In `session/subagent_hooks.go`, `launchProgram`'s hooks line becomes

```go
		program = i.prepareHooks(program, env.ClaudeConfigDir, subagentBrief(env.Program, i.ConfigDir, i.IsWorkspaceTerminal))
```

`prepareHooks` gains `brief string` (`func (i *Instance) prepareHooks(program, claudeConfigDir, brief string) string`), calls `hooks.Prepare(dir, memoryDir, brief)`, and its comment gains: "A non-empty brief is what each of the launch's subagents but a fork is told at its start (subagentBrief, hooks.Prepare)." `launchProgram`'s comment: "composes the command for env's new tmux session: loom's context flag and, when launching is true, loom's subagent hooks (with the subagents' cut of that context, subagentBrief, and on an account its memory dir: accountMemoryDir)."

- [ ] **C4: Run the tests.** `CGO_ENABLED=0 go test ./session`. Expected: PASS. Mutation checks, each restored: drop `!IsClaudeProgram(program)` and see `TestSubagentBrief_FollowsTheParentsContext` fail on aider; pass `""` in `launchProgram` and see `TestLaunchProgram_BriefsSubagentsOnlyWithTheLoomContext` fail; always pick `loomContextWorktreeHead` and see its workspace case fail.

- [ ] **C5: The opt-in real-Claude test.** Create `session/claude_subagent_brief_realclaude_test.go`:

```go
package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/session/hooks"
	"github.com/aidan-bailey/loom/session/subagent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRealClaude_SubagentBrief repeats the 2026-11-05 probe
// (docs/superpowers/specs/2026-11-05-subagent-loom-context-design.md §4)
// against real Claude: a general-purpose subagent of a launch prepared
// with a brief has it in its transcript, and a fork, which holds its
// parent's prompt already, doesn't. It pins the matcher Go's regexp can't
// evaluate. It spends a few cents of sonnet usage and leaves entries in
// ~/.claude/projects, so it only runs with LOOM_TEST_REAL_CLAUDE=1 and
// never in CI.
func TestRealClaude_SubagentBrief(t *testing.T) {
	if os.Getenv("LOOM_TEST_REAL_CLAUDE") != "1" {
		t.Skip("set LOOM_TEST_REAL_CLAUDE=1 to run against real Claude (costs money)")
	}
	for _, bin := range []string{"claude", "sh"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}

	const marker = "KUMQUAT-7"
	root := t.TempDir()
	hooksDir := filepath.Join(root, "hooks")
	_, err := hooks.Prepare(hooksDir, "", "Note for this test session: the probe codeword is "+marker+".")
	require.NoError(t, err)
	work := filepath.Join(root, "work")
	require.NoError(t, os.MkdirAll(work, 0o700))

	// Asking a subagent to repeat its context verbatim trips a safeguard
	// against prompt extraction; the transcript says what it was given.
	prompt := `We are testing Claude Code hooks. In ONE message, launch two subagents: one with subagent_type "fork" and one with subagent_type "general-purpose". Give both this task: "Do not use any tools. Reply with the word DONE." Wait for both, then reply DONE.`
	cmd := exec.Command("claude", "-p", "--model", "sonnet", "--settings", hooks.SettingsPath(hooksDir), "--allowedTools", "Agent", prompt)
	cmd.Dir = work
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))

	starts := map[string]hooks.Event{} // agent type -> its SubagentStart
	entries, err := os.ReadDir(hooks.EventsDir(hooksDir))
	require.NoError(t, err)
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(hooks.EventsDir(hooksDir), e.Name()))
		require.NoError(t, err)
		if ev, err := hooks.ParseEvent(data); err == nil && ev.Name == hooks.EventSubagentStart {
			starts[ev.AgentType] = ev
		}
	}
	transcript := func(typ string) string {
		ev, ok := starts[typ]
		require.True(t, ok, "the model launched no %s subagent:\n%s", typ, out)
		meta := subagent.MetaPath(ev.TranscriptPath, ev.AgentID)
		require.NotEmpty(t, meta)
		b, err := os.ReadFile(strings.TrimSuffix(meta, ".meta.json") + ".jsonl")
		require.NoError(t, err)
		return string(b)
	}
	assert.Contains(t, transcript("general-purpose"), marker)
	assert.NotContains(t, transcript("fork"), marker, "the brief's matcher skips a fork")
}
```

Run it once: `LOOM_TEST_REAL_CLAUDE=1 CGO_ENABLED=0 go test ./session -run TestRealClaude_SubagentBrief -v -timeout 10m`. Expected: PASS. If it fails, read the output and the transcripts before changing anything: a failure here is a fact about Claude Code, and the spec's §4 says what was observed.

- [ ] **C6: Commit.**

```sh
gofmt -l session/loom_context.go session/loom_context_test.go session/subagent_hooks.go session/subagent_hooks_test.go session/claude_subagent_brief_realclaude_test.go
git add -- session/claude_subagent_brief_realclaude_test.go
git commit -m "feat(session): brief a launch's subagents when it gets loom's context" -- session/loom_context.go session/loom_context_test.go session/subagent_hooks.go session/subagent_hooks_test.go session/claude_subagent_brief_realclaude_test.go
```

---

## Package D: rules, facts and the spec

**Files:** `session/hooks/CLAUDE.md` (new), `session/CLAUDE.md`, `internal/cli/CLAUDE.md`, `session/README.md`, the spec.

- [ ] **D1: `session/hooks/CLAUDE.md`.** Copy the shape of `session/claudemod/CLAUDE.md` (title, one-paragraph description, "## Rules when modifying this package", "## Pointers"). Description: loom's Claude Code hooks for one launch: the settings file `claude --settings` loads, the recorder command every event runs, the brief each subagent but a fork gets at its start, the launch ID, and the parsing and scanning of the recorder's events; `session` prepares a folder per launch and scans it. Rules:

  - **Give subagents loom's context only through the brief's own `SubagentStart` entry, with matcher `^(?!fork$)`, and keep the recorder's entries free of matchers.** A fork already holds its parent's whole prompt and would read loom's rules twice, while the subagent tracker needs every agent's start, forks included. **Enforced** by `TestSettingsJSON_BriefsSubagentsButNotForks`, and for what the matcher matches by the opt-in `TestRealClaude_SubagentBrief`.
  - **Make every hook command read its whole input and exit 0, whatever the folder holds.** Claude pipes the payload to the command: one that exits before reading it fails Claude's write, and a non-zero exit shows as a hook error. **Enforced** by `TestHookCommand_MissingFolderExitsZeroAndDrains` and `TestBriefCommand_ReadsItsInputAndExitsZero`.
  - **Single-quote every path a command names, and accept only a `SafePath` folder.** tmux and Claude run the commands through `sh`; a `'` in a path ends the quoting. **Enforced** by `TestPrepare_RejectsSingleQuote`.

  Pointers: `../CLAUDE.md` (the session rules, preparing hooks among them) and `../../docs/ARCHITECTURE.md`.

- [ ] **D2: `session/CLAUDE.md`.** It sits 8 bytes under its 20,000-byte budget (`go run ./tools/claudemd -v`). In the loom-context rule, name the fragments by pattern and add the cut and its gate:

  > - **Change the loom-context prompts (the `loom_context_*.md` fragments here) with what they describe: pause, merge, push, the diff probe's `git add -N .`, the shared tmux server, `loom session` and `loom job`.** Every Claude session loom launches reads them as its appended system prompt, and its subagents a cut of them exactly when it does (`subagentBrief`), so a stale line misleads every agent, silently. **Enforced** for the `loom session` subcommands and flags they name by `TestLoomContext_NamesRealSessionFlags`, and for the gate by `TestLaunchProgram_BriefsSubagentsOnlyWithTheLoomContext`; **Convention** for the rest.

  Add `[`hooks/CLAUDE.md`](hooks/CLAUDE.md)` to the Pointers line of sub-packages with rules of their own. Run `go run ./tools/claudemd -v`; if the file is over budget, shorten this rule's wording (never another rule's meaning) until it fits.

- [ ] **D3: `internal/cli/CLAUDE.md`.** In the rule "Renaming or removing a `loom session` or `loom job` subcommand or flag?", the file to update becomes "`session/loom_context_sessions.md` (`loom session`) or `session/loom_context_shared.md` (`loom job`), which teach them to every Claude session loom launches".

- [ ] **D4: `session/README.md`.** In the hooks paragraph (the one listing `settings.json`, `launch-id`, `events/`), add `subagent-context.json` and one sentence: with loom's context on, `settings.json` also registers a second `SubagentStart` entry, matcher `^(?!fork$)`, that prints `subagent-context.json`, the hook output whose `additionalContext` is the subagent cut (`subagentContextFile`: `loom_context_subagent.md`, the parent's head, `loom_context_shared.md`); Claude adds it once per subagent, keeps it across `SendMessage` resumes and the parent's `--resume`, and adds it again after compaction (Claude Code 2.1.318). In the paragraph on the loom-context prompts, if there is one (`grep -n loom_context session/README.md`), name the three-fragment composition.

- [ ] **D5: The spec.** In §2's last bullet, "a user's own `--append-system-prompt-file`" becomes "a missing prompt file" (loom adds its flag beside a user's own). §5.4 becomes: "`loomContextPath` answers whether a launch gets loom's prompt file; `loomContextProgram` adds the flag from it and `subagentBrief` the cut, so the two can't disagree. `launchProgram` passes the brief to `prepareHooks`. …" The §6 row for `session/loom_context.go` names `loomContextPath` and `subagentBrief` in place of "`loomContextProgram` reports whether it applied". §8's rule rationale: "a user who turned the context off has turned it off for the session's subagents too" (drop the prompt-file clause).

- [ ] **D6: Gates.** `go run ./tools/claudemd -v` (within budget, no errors), `CGO_ENABLED=0 go test ./tools/claudemd ./session/... ./internal/cli`, then the full suite: `loom job submit -d "full test suite" -- env CGO_ENABLED=0 go test ./...` and wait for its message. `CGO_ENABLED=0 go vet ./...`; golangci-lint is skipped locally (the dev shell's v2 rejects the repo's config).

- [ ] **D7: Commit.**

```sh
git add -- session/hooks/CLAUDE.md
git commit -m "docs(session,cli): rules and facts for the subagent brief" -- session/hooks/CLAUDE.md session/CLAUDE.md internal/cli/CLAUDE.md session/README.md docs/superpowers/specs/2026-11-05-subagent-loom-context-design.md
```

---

## Package E: build on the job routing (blocked until its Package D lands)

**Files:** `session/loom_context_shared.md`, `internal/cli/job_test.go` (if its assertions name the old sentence), the spec.

- [ ] **E1: Check the routing branch.** `git log --oneline main..aidanb/loom-doesnt-know-subagents` shows its Package D commit (display, context and docs). If not, stop: this package waits.

- [ ] **E2: Merge it.** `git merge --no-ff aidanb/loom-doesnt-know-subagents`. A conflict in `session/loom_context_shared.md` resolves to this branch's layout ("Loom itself" only) with the routing branch's work-queue paragraph; one in `session/loom_context_sessions.md` cannot happen unless the routing branch touched "Other sessions", in which case its change moves to `session/loom_context_sessions.md`.

- [ ] **E3: Reword the subagent sentence.** In the work-queue paragraph of `session/loom_context_shared.md`, the sentence the routing branch's spec §9 added is replaced by:

  > Submit from a foreground Bash call: a job's `[loom] job N` message goes to whoever submitted it, a subagent included, even after it has finished, and one loom can't place comes to the session, naming the subagent.

  Then `CGO_ENABLED=0 go test ./internal/cli -run 'TestJobContext|TestLoomContext' && CGO_ENABLED=0 go test ./session -run 'TestSubagentContext|TestWriteLoomContextFiles'`. If `TestJobContext_NamesOnlyFlagsTheCommandsHave` asserts words of the replaced sentence, change those assertions to `subagent` and `foreground Bash call`; it keeps asserting that the paragraph names subagents.

- [ ] **E4: Gates.** The full suite through `loom job submit` as in D6, `go run ./tools/claudemd -v`, `CGO_ENABLED=0 go vet ./...`.

- [ ] **E5: Commit.** Mark the spec's status "implemented 2026-MM-DD" in the same commit.

```sh
git commit -m "docs(session): a job's message reaches whoever submitted it, in words for parent and subagent" -- session/loom_context_shared.md internal/cli/job_test.go docs/superpowers/specs/2026-11-05-subagent-loom-context-design.md
```

---

## Spec coverage

| Spec | Package |
|---|---|
| §5.1 the text, the split, the subagent head, the 10,000-byte cap | A |
| §5.2 the work-queue paragraph | E |
| §5.3 delivery: the file, the command, the matcher, the recorder unchanged | B |
| §5.4 the gate | C |
| §7 failure handling: a failed write fails `Prepare`; a missing file prints nothing | B (`TestBriefCommand_…`; `writeBrief`'s errors) |
| §8 tests | A, B, C |
| §8 the package rule; `session/CLAUDE.md` by pattern | D |
| §9 known limits | spec only |
