# Architecture Audit Remediation (2026-09-22)

Implements every recommendation from the 2026-09-22 architecture analysis
(the follow-up to `docs/audit-2026-06-18.md`). Branch: `aidanb/arch-audit`.

## Background — what the analysis found

The common thread: invariants enforced by a type or a test held through three
months of feature work (tmux mutex, `TransitionTo`, adapter registry,
`TestNoRawTmuxExec`); invariants enforced only by prose in CLAUDE.md are the
ones that leak. Confirmed leaks:

- **Data loss.** `MigrateAll` aborts the whole load on the first undecodable
  record (`session/storage_migrate.go`), and `enterGlobalMode` logs a load
  error and continues with an empty list; the next `SaveInstances` rewrites
  the global instance list wholesale. `unrecovered` only covers reconcile
  failures, not decode failures. `DeleteInstance`/`UpdateInstance` also go
  through `MigrateAll` and rewrite wholesale.
- **Script read race.** `scriptHost.SelectedInstance/Instances` read `s.m.list`
  and `SendTerminalKeys` reads `s.m.splitPane` from the Lua `tea.Cmd` goroutine;
  `ui.List` is unlocked and `loadSlot` reassigns both pointers.
- **Phantom drift guard.** `keys/keys.go` claims `migration_parity_test.go`
  keeps `GlobalkeyBindings` in sync with `defaults.lua`; no test references it.
- **Growth.** `home` 39 → 71 fields since June; the focused workspace slot
  still has two sources of truth (`saveCurrentSlot`/`loadSlot` copy 6–8
  fields). `session.Instance` 39 → 70 exported methods, ~27 pure tmux
  pass-throughs; five public fields are written from `app/`.
- Four hand-rolled `lastX`/`xInFlight` poll throttles; git stderr parsed in
  English with no `LC_ALL`; `findMainRepoRoot` and `session/files` bypass the
  injected runner; sandbox leaves `setfenv`/`getfenv`/`newproxy`.

## Global rules for every task

- Work in `/tb/Source/Personal/loom/.loom/worktrees/aidanb/arch-audit_18d7bddbe1ab93ed`.
  Stay on branch `aidanb/arch-audit`: no checkout/switch/rebase/new branches/worktrees.
  Never use bare `git stash`.
- Never touch `vendor/`. Never run `gofmt -w .` (it rewrites vendored files);
  format with `gofmt -w $(git ls-files '*.go' | grep -v '^vendor/')` or explicit paths.
- Never run `./loom` or `go run .` — the nesting guard/orphan sweep can kill
  real sessions. No live TUI runs are needed for this plan.
- Verification (all must pass before committing):
  ```bash
  CGO_ENABLED=0 go build ./...
  CGO_ENABLED=0 go vet ./...
  CGO_ENABLED=0 go test ./...
  CC=clang CGO_ENABLED=1 go test -race ./app/... ./session/... ./script/... ./ui/... ./config/...
  gofmt -l $(git ls-files '*.go' | grep -v '^vendor/')   # must print nothing
  ```
  `golangci-lint` cannot run locally (v2 binary vs v1 config) — do not try.
  Plain `go test` without `CGO_ENABLED=0` fails here (no gcc); keep the prefix.
- TDD where behavior changes: write the failing test first and observe it fail.
- Update CLAUDE.md (and package doc comments) for anything the task changes —
  docs ship in the same commit as the code they describe.
- One commit per task, conventional-commit subject, ending with the trailer
  `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

---

## Task 1 — Split `app/app.go` by concern (pure moves)

**Goal:** make later tasks' diffs land in focused files. No behavior change.

Move, byte-for-byte (no edits to bodies, comments, or signatures):

1. **`app/app_init.go`** — `Run`, `newHome`, `restoreSavedWorkspaces`, and any
   helpers used only by them.
2. **`app/overview.go`** — the overview/fleet cluster: `enterOverview`,
   `slotList`, `fleetOrder`, `normalizeOverviewCursor`, `seedOverviewCursor`,
   `focusCursorSlot`, `peerSectionFor`, `overviewGroupName`, `fleetSlotOrder`,
   `slotGroupName`, `overviewGroupFor`, `cursorFor`, `overviewData`,
   `moveCursor`, `jumpWaiting`, plus small helpers used only by them.
3. **`app/workspaces.go`** — workspace-slot management: `activateWorkspace`,
   `deactivateWorkspace`, `removeInstanceEverywhere`, `saveCurrentSlot`,
   `loadSlot`, `applyWorkspaceToggle`, `enterGlobalMode`, `saveOpenWorkspaces`,
   `persistFocusedWorkspace`, `slotNames`, `updateTabBarStatuses`,
   `refreshPeerSections`, `sessionToTabStatus`, and the `workspaceSlot` type.

Keep in `app.go`: the `home` struct, `Update`, `View`, key handling, and the rest.
If a function's placement is ambiguous, leave it in `app.go`.

**Verify the move is pure:** capture the sorted set of top-level declarations
before and after (e.g. `grep -hE '^(func|type|var|const) ' app/*.go | grep -v _test | sort`)
— identical; and `git diff -M --color-moved=zebra --stat` shows only moves.
Run the full verification. Commit: `refactor(app): split app.go into init, overview and workspace files`.

---

## Task 2 — Persistence fails closed (no data-loss path)

**Files:** `session/storage_migrate.go`, `session/storage.go`, `app/workspaces.go`
(`enterGlobalMode`), wherever `UnrecoveredTitles` is surfaced (recovery summary
in `app/`), tests in `session/` and `app/`.

**Design:**

1. `MigrateAll(raw []byte) (records []InstanceData, skipped []json.RawMessage, err error)`.
   `err` is returned only when the top-level array itself can't be decoded.
   Each element that fails `Migrate` (corrupt, or `schema_version` newer than
   `CurrentSchemaVersion`) is logged (`log.For("session").Warn("instance_undecodable", "index", i, "err", err)`)
   and appended to `skipped` as its original raw bytes.
2. `Storage` gains:
   - `undecodable []json.RawMessage` — records this binary cannot decode,
     replaced on every successful `loadInstanceDataLocked`.
   - `loadErr error` — set when the last load failed, cleared by a successful one.
   - `loaded bool` — whether any load has run.
3. **Every write path** (`SaveInstances` and `saveInstanceData`, which backs
   `DeleteInstance`/`UpdateInstance`):
   - If no load has run yet, run `loadInstanceDataLocked` first so undecodable
     records are known before anything is written.
   - If `loadErr != nil`, refuse: return an error wrapping a new exported
     sentinel `ErrStorageLoadFailed`, and write nothing.
   - Otherwise append every `undecodable` record **verbatim** after the live
     records (build the payload as `[]json.RawMessage`). No dedupe by title.
4. `DeleteAllInstances` is the explicit wipe (`loom reset`): allowed even when
   `loadErr != nil`; clears `undecodable`, `unrecovered`, and `loadErr`.
5. `UnrecoveredWorktreePaths` also includes each undecodable record's
   `worktree.worktree_path`, best-effort decoded (partial unmarshal into a tiny
   struct; skip on failure). Otherwise orphan discovery would offer the worktree
   as a Recoverable and the user could create a duplicate record.
6. Add `UndecodableCount() int`, and surface a message wherever
   `UnrecoveredTitles` is surfaced: "N session record(s) could not be read by
   this version of loom and were preserved unchanged".
7. `enterGlobalMode`: build the global storage and run `LoadAndReconcile`
   **before** deactivating any workspace slot. On error, return `m.handleError(...)`
   and leave workspace mode exactly as it was.
8. Check `cmd/workspace_migrate.go`: if it rewrites instance records in a way
   that would drop undecodable ones, report it as a concern (do not fix).

**Tests (write first, watch them fail):**
- `MigrateAll` with `[valid, {"schema_version":99,...}]` → 1 record, 1 skipped, nil err.
- Storage with backing state `[valid, v99]`: `LoadAndReconcile` returns 1
  instance; `SaveInstances(that one)` leaves the v99 record in the payload with
  identical JSON; `DeleteInstance(valid)` and `UpdateInstance` keep it too;
  `UnrecoveredWorktreePaths` includes its worktree path; `UndecodableCount()==1`.
- Top-level corrupt (`{"not":"an array"}`): `LoadAndReconcile` errors;
  `SaveInstances` returns `ErrStorageLoadFailed` and the backing state is byte-identical;
  `DeleteAllInstances` succeeds.
- `SaveInstances` on a never-loaded Storage whose backing state has a v99 record keeps it.
- `enterGlobalMode` fail-closed: global state with a corrupt instances payload
  (use `t.Setenv("LOOM_HOME", t.TempDir())` plus a pre-written state file, or
  the narrowest seam you can find) → error surfaced, slots untouched.

CLAUDE.md: update the `session/` bullet (storage) and the `SchemaVersion`
gotcha to describe skip-and-preserve plus the fail-closed save latch.
Commit: `fix(session): preserve undecodable instance records and refuse saves after a failed load`.

---

## Task 3 — Script host reads a main-goroutine snapshot

**Files:** `app/app_scripts.go`, `script/host.go` (docs), new race test in `app/`.

**Design:**
- `scriptHost` **loses its `m *home` field**, so no host method can reach the
  model from the Lua goroutine. A constructor `newScriptHost(m *home) *scriptHost`
  runs on the Update goroutine and captures: selected instance, a *copy* of
  the instance slice, the registry pointer, `configDir()`, `repoPath()`,
  `program`, `BranchPrefix` (via the locked accessor), and the current
  `*ui.SplitPane` (for `SendTerminalKeys` — confirm `SplitPane.terminal` is never
  reassigned after construction and that `TerminalPane.SendKeysToInstance` locks `t.mu`).
- `dispatchScript` and `handleScriptResume` (both run on Update) call
  `newScriptHost(m)`. `deferModelMutation` closures already take `*home` as a
  parameter: unchanged.
- Update the doc comments on `script.Host` and `deferModelMutation`: reads return
  values captured when the dispatch/resume began; they don't see changes made
  later in the same handler.

**Test (RED first):** a user script (loaded via `initScriptsIn` from a temp dir)
binds a key to a handler that loops ~200× calling `ctx:selected()`,
`ctx:instances()`, `ctx:config_dir()`. Get the Cmd from `m.dispatchScript(key)`
and run it in a goroutine while the test goroutine mutates the list
(`AddInstance`/`RemoveInstance`/`SetSelectedInstance`) and reassigns
`m.list`/`m.splitPane` like `loadSlot` does. Under
`CC=clang CGO_ENABLED=1 go test -race -run <Name> ./app/` it must report a race
before the fix and pass after. Record both outcomes in your report.

Commit: `fix(app): give script hosts a main-goroutine snapshot instead of the live model`.

---

## Task 4 — `pollGate`: make the throttle invariants structural

**Files:** new `app/pollgate.go` + `app/pollgate_test.go`; `app/app.go`
(home fields, Update cases for `rosterReadyMsg`, `subagentScanMsg`, `ghReadyMsg`,
`ghRefreshMsg`, `ratioSaveMsg`); `app/events.go`, `app/subagents.go`,
`app/github.go`; existing tests that poke the old fields; CLAUDE.md.

**Design:**
```go
type gateKind int // gateRoster, gateSubagent, gateGH, gateRatioSave

type pollGate struct {
    interval time.Duration
    last     time.Time
    inFlight bool
}

// dispatch calls build only when due (not in flight and interval elapsed).
// A nil Cmd from build arms nothing. Otherwise it arms the gate and returns a
// Cmd whose result comes back wrapped in gatedMsg{kind, msg}.
func (m *home) dispatchGated(kind gateKind, now time.Time, build func() tea.Cmd) tea.Cmd

// expedite makes the next dispatch due immediately (replaces zeroing lastGHQuery).
func (g *pollGate) expedite()

type gatedMsg struct { kind gateKind; msg tea.Msg }
```
- `Update` handles `gatedMsg` by disarming `m.gate(kind)` **first**, then
  processing the inner message through the normal handler. Handlers no longer
  touch in-flight state, so a new handler can't forget to disarm it.
- Use a `gateKind` enum resolved via `m.gate(kind)`, not a `*pollGate` pointer
  in the message (don't depend on `home` never being copied).
- Replace `lastRosterQuery/rosterInFlight`, `lastSubagentScan/subagentInFlight`,
  `lastGHQuery/ghInFlight`, and `ratioSaveArmed` with four gates. The ratio
  gate has interval 0; its build returns nil when `pendingRatioSaves` is empty
  and otherwise returns the existing `tea.Tick`.
- The GitHub `ghAvailable` backoff check stays a pre-check before
  `dispatchGated`. Every site that zeroed `lastGHQuery` calls `m.gh.expedite()`
  (or equivalent).
- Remove the "must disarm on every delivery" comments at each call site; the
  `pollGate` doc states the contract once.

**Tests:** unit tests for due/interval, nil-build-arms-nothing, expedite, and
disarm-on-delivery. An app-level test: deliver a `gatedMsg` wrapping a roster
*error* result and a GitHub result, then assert both gates are disarmed. Update
existing tests (e.g. `app/roster_status_test.go`, github/subagent tests) to the
new API — keep their behavioral intent.

CLAUDE.md: rewrite the roster, subagent and GitHub-poller gotcha passages that
describe `lastX/xInFlight` pairs to describe `pollGate`.
Commit: `refactor(app): replace hand-rolled poll throttles with pollGate`.

---

## Task 5 — git/gh subprocess constructors + enforcement

**Files:** new `internal/exec/command.go` + tests, new enforcement test; every
production site building `exec.Command[Context]("git"|"gh", …)`:
`session/git/*.go`, `session/orphan.go`, `session/files/ls_files.go`,
`review/gitdiff/diff.go`, `session/github/cli.go`, `internal/devsandbox/*.go`,
`tools/loomdev/*.go`, `tools/fakeagent/agent.go`.

**Design:**
```go
// GitCommand builds a git invocation. Every git subprocess loom runs is built
// here so it runs with LC_ALL=C: callers classify failures by matching git's
// English stderr (isBranchAbsentErr, isWorktreeAbsentErr, …).
func GitCommand(ctx context.Context, dir string, args ...string) *exec.Cmd // dir != "" → "-C dir"
// GhCommand builds a gh invocation with GH_PROMPT_DISABLED=1 and GH_NO_UPDATE_NOTIFIER=1.
func GhCommand(ctx context.Context, args ...string) *exec.Cmd
```
- Env: `append(os.Environ(), "LC_ALL=C")` — LC_ALL goes last because Go keeps
  the last duplicate. Callers with extra env (`runGitCommandEnvTimeout`) append
  theirs after it.
- Sites without a context use `context.Background()` (same behavior as `exec.Command`).
- Keep every existing runner injection. Also fix the two bypasses:
  `findMainRepoRoot` takes the `CommandRunner` its caller (`CleanupWorktrees`)
  already has; `session/files` gains an injected `internalexec.Executor` (a
  parameter if it has ≤3 production callers, otherwise a package-level var
  tests can swap — report which). For `session/orphan.go`, use a runner where
  one is already in scope; otherwise leave the direct call (LC_ALL still applies).
- **Enforcement test** (copy `session/tmux/command_enforce_test.go`'s approach):
  walk the repo (skip vendor/ and dot-dirs), parse non-`_test.go` files, and
  fail on `exec.Command`/`exec.CommandContext` whose program arg is the literal
  `"git"` or `"gh"` outside `internal/exec/command.go`. Prove it works: plant a
  violation, watch it fail, remove the plant.

**Tests:** `GitCommand` adds `-C dir`, and `LC_ALL=C` wins even with
`t.Setenv("LC_ALL", "de_DE.UTF-8")` (check the last `LC_ALL=` entry in `c.Env`);
extra env survives. Same for `GhCommand`'s two vars.

CLAUDE.md: add a gotcha paragraph next to "Every tmux exec goes through
`tmux.Command`" covering git/gh and the new test.
Commit: `fix(exec): build every git/gh subprocess through LC_ALL=C constructors, enforced by test`.

---

## Task 6 — Turn prose rules into tests (keymap, overlays, sandbox)

1. **Keymap parity test** (put it where both `keys` and `script` import cleanly,
   e.g. `app/keymap_parity_test.go`). Load the embedded defaults with
   `script.NewEngine(...)` + `LoadDefaults()`. For every `KeyName` in
   `keys.GlobalkeyBindings` except `KeySubmitName` (overlay-only `enter`) and
   the display aliases `KeyRecover`/`KeyDiscard`:
   - every key in `binding.Keys()` must be bound by defaults.lua;
   - when defaults.lua gives the primary key (`Keys()[0]`) a help string, it
     must equal `binding.Help().Desc`.
   Fix mismatches by editing `GlobalkeyBindings` (defaults.lua decides dispatch).
   Known: ctrl+a/ctrl+t read "attach agent/terminal" in Go but "interact
   agent/terminal" in Lua. Rewrite the false comment at `keys/keys.go` (the
   `GlobalkeyBindings` doc) to name the real test.
2. **Overlay compile-time checks.** In `ui/overlay/iface.go` add
   `var _ Overlay = (*X)(nil)` for every implementer (ConfirmationOverlay,
   FileExplorerOverlay, SessionLaunchOptions, MergePicker, WorkspacePicker,
   TextInputOverlay, IssuePicker, SettingsOverlay, TextOverlay — confirm the
   list with grep) and fix the doc comment that names only four.
3. **Sandbox.** In `script/sandbox.go` also nil out `setfenv`, `getfenv` and
   `newproxy`, and update the comment. Add a test asserting that after sandbox
   setup these are all nil: `dofile loadfile load loadstring require
   collectgarbage setfenv getfenv newproxy io os debug package` and
   `string.dump`. Update CLAUDE.md's `script/` bullet (the stripped list).

Run the full verification. Commit: `test: enforce keymap parity, overlay conformance and sandbox strip-list`.

---

## Task 7 — The workspace slot is the single source of truth

**Files:** `app/workspaces.go`, `app/app.go`, `app/app_init.go`, `app/overview.go`,
`app/events.go`, `app/github.go`, other app files touching the fields, app tests; CLAUDE.md.

**Design (embedding):**
- `m.slots` becomes `[]*workspaceSlot`. `home` **embeds `*workspaceSlot`** — the
  focused slot. Remove `home`'s own `list`, `splitPane`, `workbench`, `storage`,
  `appConfig`, `appState` fields (they'd shadow the promoted ones) and replace
  `activeCtx` with the slot's `wsCtx` (rename every `m.activeCtx` to `m.wsCtx`).
  Existing `m.list` etc. reads/writes then go straight to the focused slot.
- **Invariant:** `len(m.slots) > 0` ⇒ `m.workspaceSlot == m.slots[m.focusedSlot]`.
  `len(m.slots) == 0` ⇒ `m.workspaceSlot` is the classic/global slot (`wsCtx == nil`),
  not in `m.slots`. `m.workspaceSlot` is never nil after `newHome`.
- `loadSlot(idx)`: `cleanupWorkbench()` (still on the departing slot), set
  `focusedSlot`, `m.workspaceSlot = m.slots[idx]`, then the existing side effects
  (SetWorkspaceName, tab bar, interim resize, refreshPeerSections, applyUIPrefs).
- `saveCurrentSlot` → rename to `leaveFocusedSlot`; it keeps `cleanupWorkbench()`
  and `flushPendingRatioSaves()` and stops copying fields. Update every caller
  and every doc/comment that names it (CLAUDE.md's workbench gotcha).
- Every slot constructor (`activateWorkspace`, `newHome`, `enterGlobalMode`)
  must give the slot a non-nil `workbench` and `splitPane`. `enterGlobalMode`
  builds a fresh global slot and carries over the previously focused
  slot's `splitPane` and `workbench`, as happens implicitly today.
- Audit every `m.slots` mutation (append, removal in `deactivateWorkspace`,
  reorder) and restore the invariant at each: removing the focused slot while
  others remain must refocus via `loadSlot`; removing the last slot goes
  through the global-mode path.
- Delete the now-redundant `i == m.focusedSlot` special cases
  (`instanceForSession` in events.go, `jumpWaiting`, the metadata-tick
  instance collection, `updateTabBarStatuses`, `refreshPeerSections`, `slotList`)
  — `m.slots[i].list` is always current. Simplify `removeInstanceEverywhere`
  (classic mode still needs `m.list`).
- Add `func (m *home) checkSlotInvariant() error` (unexported) and call it in tests.

**Tests:** update test literals (`&home{list: l}` → `&home{workspaceSlot: &workspaceSlot{list: l}}`;
add a small test helper if it cuts churn). New tests: (a) after activate ×3,
switch, deactivate the focused slot, deactivate all → `checkSlotInvariant()`
holds at each step; (b) a mutation via `m.list` is visible through
`m.slots[m.focusedSlot].list` with no save step; (c) the classic slot is non-nil
and outside `m.slots`.

CLAUDE.md: rewrite the workbench gotcha's `saveCurrentSlot/loadSlot` wording;
add a short gotcha describing the embedded focused slot and its invariant; update
"Overview cursor and nav share one classic-vs-slots split" if the special cases changed.
Commit: `refactor(app): make the focused workspace slot the single owner of per-workspace state`.

---

## Task 8 — `Instance.Pane()`: move the tmux pass-throughs off `Instance`

**Files:** new `session/agent_pane.go`; `session/instance.go`; callers in `app/`,
`ui/`, `script/`; tests.

**Design:**
- `type AgentPane struct{ i *Instance }` and `func (i *Instance) Pane() AgentPane`.
- Move to `AgentPane` every exported `Instance` method whose body is a
  started/paused guard plus a forward to (or probe of) the tmux session, and
  which isn't lifecycle (`Start`, `Kill`, `Pause`, `Resume`, `Restart`,
  `CrashRestart`, `EnsureRunning`, `TransitionTo`), persistence
  (`Snapshot`/`ToInstanceData`), diff/git, or GitHub/subagent/bell/wait-reason
  state. Expected set, roughly: `Preview`, `EmulatorScreen`, `CaptureHistory`,
  `IsAlternateScreen`, `ForwardWheel`, `ForwardMouse`, `Paste`, `ForwardFocus`,
  `CursorState`, `PaneTitle`, `HasEmulator`, `SendKeys`, `SendKeysRaw`,
  `SendPrompt`, `TapEnter`, `GetContentHash`, `HasUpdated`,
  `CheckAndHandleTrustPrompt`, `CaptureAndProcessStatus`, `TmuxAlive`,
  `TmuxLiveness`, `PtmxAlive`, `RepairPtmx`, `TmuxSessionName`, plus any
  render-window/scrollback/size helpers matching the criterion. Report the
  final list, and anything you kept on `Instance`, with the reason.
- Bodies move verbatim (receiver becomes `p.i`) — no behavior change. Lua
  method names in `script/userdata_instance.go` stay the same; only their Go
  call sites change.
- If a `ui` interface is satisfied by `*session.Instance` because of these
  methods, repoint it at `AgentPane`.
- Callers: `inst.X(...)` → `inst.Pane().X(...)`.

Report exported `Instance` method counts before and after
(`grep -cE '^func \(i \*Instance\) [A-Z]' session/*.go`).
CLAUDE.md: mention `Instance.Pane()` in the `session/` bullet.
Commit: `refactor(session): move agent-pane I/O behind Instance.Pane()`.

---

## Task 9 — Encapsulate `Instance`'s externally mutated fields

**Files:** `session/instance.go` (+ constructor options), callers in `app/`,
`script/`, `ui/`, tests.

**Design:** unexport the five fields written from outside `session` and add
accessors that lock `i.mu`:

| Field → | Getter | Setter |
|---|---|---|
| `Program` → `program` | `Program()` | `SetProgram(string)` |
| `HeadroomProxy` → `headroomProxy` | `HeadroomProxy()` | via `SetLaunchOptions` |
| `CacheTTL1h` → `cacheTTL1h` | `CacheTTL1h()` | via `SetLaunchOptions` |
| `Prompt` → `prompt` | `Prompt()` | `SetPrompt(string)` |
| `CrashRecovered` → `crashRecovered` | `CrashRecovered()` | `SetCrashRecovered(bool)` |

- `SetLaunchOptions(program string, headroomProxy, cacheTTL1h bool)` sets all
  three under one lock (the launch-options overlay callbacks in
  `state_prompt.go`, `state_issue_picker.go`, `intents.go`).
- Add `Prompt` to `InstanceOptions`; `ctx:new_instance` passes it through the
  constructor instead of assigning afterwards.
- Internal `session` code keeps using the private fields directly. **Never call
  a locking getter from code that already holds `i.mu`** (RWMutex is not
  reentrant): audit every internal use.
- `Title`, `Path`, `Branch`, `Status`, `Height`, `Width`, `CreatedAt`,
  `UpdatedAt`, `ConfigDir`, `IsWorkspaceTerminal` stay exported (never written
  outside `session`).

Commit: `refactor(session): encapsulate Instance launch fields behind accessors`.

---

## Task 10 — Documentation sweep

- CLAUDE.md: Go version (`go.mod` says 1.25.8; drop the stale "1.23.0 (toolchain
  go1.24.1)"). Re-read every section Tasks 1–9 touched for consistency
  (function names such as `saveCurrentSlot`, field names such as
  `rosterInFlight`/`lastGHQuery`, file locations of moved functions).
- `cmd/doc.go`: remove the nonexistent `RealExecutor`, name the real type
  (`internal/exec.Default`, aliased `cmd.Exec`), and describe the `workspace`
  subcommands that make up most of the package.
- `log/log.go`/`log/doc.go`: correct the stale "~90"/"~117 legacy Printf call
  sites" claims (there are 15, all in `ui/split_pane.go`).
- Grep for other references to renamed or removed symbols across `*.md` and
  Go comments (`git grep -n 'saveCurrentSlot\|rosterInFlight\|lastGHQuery\|ghInFlight\|subagentInFlight\|ratioSaveArmed\|activeCtx'`).

Commit: `docs: sync CLAUDE.md and package docs with the audit remediation`.

---

## Final — cross-cutting review

One reviewer over the whole range (`<base>..HEAD`), chartered to hunt at the
**seams between tasks**: the snapshot host (T3) vs embedded slots (T7); the
poll gates (T4) vs slot switching (T7); the storage latch (T2) vs
`enterGlobalMode` after the slot refactor (T7); `AgentPane` (T8) vs script
userdata and the host snapshot; accessor locking (T9) vs the `i.mu` discipline.
Then the full verification, including `-race`.
