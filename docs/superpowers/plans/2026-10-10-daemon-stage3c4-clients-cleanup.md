# Daemon Stage 3C+4: Subcommands as Clients, Headless Duties, Cleanup

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans. The plan has four coarse packages (A–D). Each package gets one implementer, one review
> and one commit; the numbered steps inside it are checkpoints, not commits.

**Goal:** finish the daemon:

- the CLI's subcommands become clients of the daemon;
- the daemon does its own work with no TUI open;
- a workspace removed or renamed while the daemon runs is unloaded or renamed in place;
- the dead code, stale comments and docs are cleaned up;
- the version is bumped to 0.14.0.

**Spec:** `docs/superpowers/specs/2026-10-03-loom-daemon-design.md`, the "3C+4" entry under Rollout (bae7427), and its
amendments to §1, the `loom reset` bullet, and Assumption 4.

**Tech stack:** Go 1.25, `core/rpc`, Cobra, tmux.

---

## Decisions

The user's (2026-10-10):

1. **Always a client, `reset` excepted.**
   - `workspace` and `account` commands join the daemon as the TUI does: spawn it, the newer build wins,
     `replaceGuard` applies. They make a request and exit.
   - `reset` is a request while a daemon runs. With none running it works directly, holding the lock.
   - `workspace migrate` stays direct under the lock.
2. **Unload on remove, rename in place.**
   - `Unregister` saves the workspace and stops serving it. It refuses while any of its sessions is busy. Agents
     keep running, and re-registering serves them under the same ID.
   - `RenameWorkspace` keeps the ID. The TUI's "stop the daemon to open it as X" refusal goes.
3. **The daemon alone answers trust prompts.**
   - A launch watch after every real launch captures the pane every 250ms for up to 30s and answers once. The TUI
     stops answering.
   - A 250ms hook timer runs in the loop.
4. **0.14.0, not pushed:** `main.go` is bumped and `CHANGELOG.md` regenerated on local main.

The coordinator's, from facts found while planning:

- **The commands move out of `cmd`.** `core` imports `cmd` (for `cmd.Executor`), and `session/tmux`'s tests import
  `cmd`, so `cmd` can't import `core/rpc`.
  - The client-side `workspace` and `account` commands move to a new package, `internal/cli`.
  - `main` sets the join seam `cli.Join`.
  - `cmd` keeps the executor and `workspace migrate` (direct), which `main` attaches under `cli.WorkspaceCmd`.
- **One reset implementation serves both paths.** Its steps are:
  1. delete the records;
  2. sweep the workspace's owned sessions;
  3. remove the worktrees and branches.

  Today they live in `main.go:168-255`. They move to `core.ResetWorkspace(wsCtx, registry, exec)`, used by both the
  direct path and the model's `Reset`. In the model, `Reset` first drops the served workspace's instances (refusing
  while one is busy) and runs the steps as a job. Afterwards it gives an opened workspace a fresh terminal, as the next
  start used to.
- **`Core` changes.** All are compatible, so `Protocol` does not move: added methods and an added optional parameter.

  ```go
  Unregister(name string) error
  RenameWorkspace(from, to string) error
  Reset(ws WorkspaceID, req ReqID) error   // Reply when the job lands
  RemoveAccount(name string, force bool) error
  ```

  A newer client against an older daemon replaces it at the join, so an older server ignoring `force` never applies.
- **`account sync`** is `RequestAccountsRefresh(true)`, which re-syncs. The CLI waits up to 10s for the next
  `AccountsChanged`, then prints each account's `AccountSync` report.
- **`account list`** does the same, printing the published usage with its age if no newer round lands in time.
- **The trust watch's fake:** `tools/fakeagent` already shows trust prompts (`agent.go`), so the e2e can drive the
  watch.
- **Releasing:** `git-cliff` is installed. `docs/claude/releasing.md` is the procedure.

## Packages

Run them in order: **A → B → C → D**. B needs A's methods. C is independent of B but edits core near A, so it runs
after A, never in parallel in one tree.

The gate for every package, run in the foreground:

```bash
gofmt -l $(git ls-files '*.go' | grep -v '^vendor/')     # empty
CGO_ENABLED=0 go vet ./...
CGO_ENABLED=0 GOOS=windows go build ./...
CGO_ENABLED=0 go test ./...
CC=clang CGO_ENABLED=1 go test -race ./core/... ./app/... ./internal/... .
CGO_ENABLED=0 go test -tags e2e ./e2e/...                 # B, C, D
```

Run the pinned linter on the changed lines, as CI does:

- Download `golangci-lint` v1.60.1 into the scratchpad.
- Run `CGO_ENABLED=0 golangci-lint run --fast --new-from-rev=origin/main --max-issues-per-linter=0 --max-same-issues=0`.
- It is revive, mostly: document exported methods.

**RED checks:** mutations to the new code that make its new tests fail. Report each one.

---

### A — core: unregister, rename, reset, forced account removal

**Files:**

- `core/iface.go`: the four signatures above.
- `core/workspaces.go` / `core/workspace_requests.go`:
  - `Unregister`, `RenameWorkspace`.
  - `ReloadRegistry` applying removals and renames.
- `core/reset.go`, new: `ResetWorkspace` (the steps moved from `main.go:168-255`, which then calls it) and
  `Model.Reset`.
- `core/accounts.go:543-547`: `RemoveAccount` with `force` (`account.Registry.Remove`'s force).
- Regenerate the wire code and the reference:
  - `go generate ./core/rpc`;
  - `go test ./core/rpc -run TestProtocolReference -update`.
- Tests in `core/unregister_test.go`, `core/reset_test.go`, and extended accounts tests.
- `TestReplica_AnswersAsTheModel` and `TestLoopForwardsEachMethodToItsNamesake` need their new cases.

**A1. `Unregister(name)`.**

- Refuse:
  - the global workspace;
  - an unknown name (`ErrRefused`);
  - a workspace with a busy session (Loading or Deleting, `ErrRefused` naming it).
- Otherwise:
  1. `saveWS`;
  2. remove the registry entry (reload-before-save, as `config/workspace.go` does);
  3. drop the workspace from `m.workspaces`.
- What happens to the rest:
  - The pruning in `publishWorkspaces`/`publishViews` forgets its IDs.
  - Its tmux sessions are not touched.
  - Its pending jobs' completions find no owner and do nothing harmful. Check `deliver*` for a nil owner.

**A2. `RenameWorkspace(from, to)`.**

- It renames the registry entry with the registry's own rename, which also updates the open list and `LastUsed`.
- The served workspace takes a context copy with the new name, keeping its ID: the ID is keyed by the config dir.
- Refuse: an unknown `from`, a taken `to`, the global workspace.

**A3. `ReloadRegistry`.** After adding newly registered workspaces, it diffs the served set against the registry by
canonical config dir:

- A served non-global workspace no longer registered is unregistered, as A1. One with a busy session stays until the
  next reload, logged.
- One registered under another name is renamed, as A2.

**A4. `Reset(ws, req)`.**

- Refuse:
  - an unknown workspace;
  - a busy session;
  - a latched workspace (its storage refuses writes; reset clears the latch only on the direct path, as now).
- Otherwise, on the loop:
  1. Remove every instance from the served workspace.
  2. Spawn a job running `ResetWorkspace` with the workspace's context, registry and executor, tracked by `req`.
  3. On delivery, `ensureTerminal` when the workspace is opened.
- Its `Reply` carries the job's error.

**A5. Tests:**

- Unregister:
  - unregistering a served workspace drops it from `Workspaces()`;
  - its sessions survive;
  - re-registering serves them under the same IDs;
  - it refuses while busy and for the global workspace.
- Rename:
  - the same ID under the new name;
  - the registry's open list follows;
  - it refuses a taken name.
- `ReloadRegistry` after a hand edit of `workspaces.json`, both a removal and a rename.
- Reset:
  - it empties the served workspace, its records, worktrees and branches, sweeping only owned sessions;
  - it refuses while busy;
  - the `Reply` arrives;
  - an opened workspace gets a fresh terminal.
- The direct path's existing `main_test.go` reset tests still pass through `ResetWorkspace`.
- `RemoveAccount(name, true)` removes an account holding unshared files, which `force=false` refuses.

**RED checks:**

- Drop A3's removal diff: the hand-edit test fails.
- Drop the busy refusal: its test fails.
- Make `RenameWorkspace` give the workspace a new ID: the rename test fails.

---

### B — the CLI as clients

**Files:**

- `internal/cli/`, new:
  - `workspace.go` and `account.go`, moved from `cmd/workspace.go` and `cmd/account.go`;
  - `join.go`: `var Join func() (*rpc.Client, error)`, which `main` sets to the startup join;
  - `testmain_test.go`, with `IsolateLoomDirs`;
  - tests.

  Keep each command's flags, output and guards: `ValidateMainDir`, `guardAgainstAccountEnv`, the credential warning,
  and remove's confirmation order.
- `cmd/`: keeps `cmd.go` (the executor), `workspace_migrate.go` and their tests; `cmd/CLAUDE.md` follows.
- `main.go`:
  - It registers `cli.WorkspaceCmd` (with `cmd2.MigrateCmd` attached) and `cli.AccountCmd`, and sets `cli.Join`.
  - `resetCmd`:
    - When `daemon.ReadRecord` shows a holder, it joins and sends `Reset` for the resolved workspace, then waits
      for its `Reply`.
    - Otherwise it takes the lock and runs `core.ResetWorkspace` directly. The lock comes from a new
      `daemon.TakeLock(globalDir) (release func(), error)`: `ErrRunning` if held, never waiting.
  - `refuseWhileServed`/`cmd.StateWriteGuard` become the lock-holding guard for `migrate`.
- `daemon_client.go`: the join, reused; `say` writes to stderr for CLI commands.
- `app/workspaces.go:145-170`: delete the "renamed while the daemon runs" refusal, keeping the twin refusal.

**B1. A client command's shape.**

```go
c, err := Join()            // spawns or replaces the daemon as the TUI's join does
if err != nil { return err }
defer c.Close()
// one request (or a local read for list/status), its error returned as is
```

A request carrying a `ReqID` (`Reset`) waits for its `Reply`: loop on `c.Wakes()` and `c.Sync()` until a
`core.Reply` with that ID arrives, or the client's `Err()` reports a loss.

**B2. `workspace status`** reads `Workspaces()` and `Views(id)` from the replica, so its counts are the daemon's. Keep
its output format.

**B3. Tests** (`internal/cli`), against an in-process daemon:

- `cli.Join` returns an `rpc.InProcess` client over a booted model, using the `core/boot_test.go` patterns re-created
  locally, or `rpc.InProcessForTest`.
- One test per command. Assert the model's state after the request, not the files.
- `account login` still refuses a missing dir; with `--no-login` it runs nothing.
- `reset`:
  - with the lock held by a daemon it sends `Reset`;
  - with no daemon it works directly and holds the lock meanwhile: a `daemon.Serve` started during it stands down.

**RED checks:**

- Make `workspace add` write the registry file directly: the test asserting the daemon serves it at once fails.
- Drop `TakeLock` from the direct reset: the stands-down test fails.

---

### C — the daemon's work with no TUI

**Files:**

- `core/launch_watch.go`, new: `watchLaunch(inst)`, the job, and `launchWatched` delivery.
- `core/completions.go`, `core/load.go`, `core/tick.go`: call `watchLaunch` after each real launch:
  - a start's `Started`;
  - a resume that relaunched (not a reattach);
  - `crashRestart`;
  - a workspace terminal's `Restart`/`SessionLaunched`.
- `core/loop.go`: the 250ms hook timer, `hookInterval`, calling `Model.HookTick()`. It runs `maybeHookScan` when an
  active Claude instance exists. `Loop.HookTickForTest` is its seam; `StartForTest` loops never fire it on their own.
- `session/tmux/tmux.go:669`: `CaptureAndProcess` no longer dismisses trust prompts, and the `trustHandled` result
  goes.
- `session/tmux/attach.go`: `DetectStatus` follows.
- `app/events.go`: no trust handling remains.

**C1. The watch job.**

- It captures what it needs on the loop: the session name and the adapter, from the view's `SessionProgram`.
- It builds a lifecycle `tmux.Session` by name, with no client.
- Every 250ms for up to 30s it runs `capture-pane`. On a trust prompt it calls `DismissTrustPrompt(content)` once and
  returns.
- It also returns early when the session dies.
- The model keeps `watching map[string]bool`, so there is one watch per session name; the delivery clears it.
- It is a background job (`spawnBackground`): `Quiesce` doesn't wait for it.

**C2. Tests:**

- With a mock executor, a pane showing Claude's trust prompt on its third capture gets exactly one dismiss.
- A pane with no prompt stops after the deadline, using a seam for the 30s.
- Two launches of one session start one watch.
- Each launch kind calls `watchLaunch`.
- `HookTick` scans only with an active Claude instance.
- The TUI's status scan never sends keys: an app test asserts that.

**RED checks:**

- Drop the dedupe: the two-launches test fails.
- Restore dismissing in `CaptureAndProcess`: the app test fails.

---

### D — e2e, cleanup, docs, version

**D1. e2e** (`e2e/`):

- `workspace remove` of a workspace open as a tab closes the tab in the running TUI.
- `workspace rename` keeps the tab, now showing the new name.
- `loom reset --force` with the daemon and a TUI running empties the TUI's list. The killed agents are gone, and
  another workspace's agents survive.
- **Trust prompt with no TUI:**
  1. Kill an agent's tmux session with the TUI closed.
  2. Restart the daemon (`loomdev daemon --stop`, then any CLI command spawns one).
  3. Its crash restart shows the fakeagent's trust prompt, which the launch watch answers.
  4. A TUI opened afterwards sees the agent past it.
- The CLI subcommands spawn the daemon when none runs.

**D2. Cleanup:**

- Delete production code only tests use, from the map:
  - `ui.SplitPane.CleanupTerminal` (`ui/split_pane.go:590`);
  - `AgentPane.TmuxAlive` (`session/agent_pane.go:92`), and fix `app/views.go:256`'s comment;
  - `Instance.SetTmuxSession` (`session/instance.go:2023`);
  - `Instance.RestartFailureCount` (`:1201`);
  - `Storage.LoadInstances` / `UpdateInstance` / `LoadInstanceData` (`session/storage.go:208, :359, :436`).

  A test that needs one gets a `…ForTest` seam (`TestNoProductionCallsOfTestSeams` guards it). `rpc.InProcess` and
  `Model.Drain` stay.
- Drop the "Formerly X" comments naming code that no longer exists, about 25 of them in `app/` and `core/` (grep
  `Formerly`), and the "until … stage 3C" comments (`main.go:196-197`, `app/workspaces.go:145`).
- Run `LOOM_CLAUDEMD_STRICT=1 go run ./tools/claudemd -idents` and fix every stale symbol.

**D3. Docs**, written from a fact sheet of what was built:

- `cmd/CLAUDE.md`, plus the new `internal/cli` package's `CLAUDE.md`/`README.md` (follow the conventions the lint
  checks).
- `core/`, `core/rpc/`, `app/`, `internal/daemon/` and `session/tmux/` CLAUDE.md: trust prompts are the daemon's, and
  the hook timer.
- The root `CLAUDE.md` and USAGE.md: the subcommands are clients, `reset`'s two modes, unregister and rename while
  served.
- `docs/ARCHITECTURE.md`.
- The spec's 3C+4 amendments, turned from "planned" into "as built".

**D4. Version:**

- `main.go:25` `version = "0.14.0"`.
- `git cliff -o CHANGELOG.md --tag v0.14.0`, per `docs/claude/releasing.md`.
- Commit on the branch. Never push or tag.

**Acceptance:**

- e2e is green twice.
- `go test ./tools/claudemd/...` and the strict ident check are clean.
- The linter is clean on the changed lines.

---

## Execution

The flow is frugal, as in 3R:

- **Implementers:** one per package. A uses Opus; B is mostly moves, so Sonnet; C uses Opus; D uses Sonnet.
- **Reviews:** one combined spec-and-quality Opus review per package. Fixes go back to the same implementer.
- **D's review:** folded into a fresh final cross-cutting review.
- **Smoke run:** a sandbox run before the final review.
- **Rules** (memory `loom-subagent-review-lessons.md`):
  - Tests run in the foreground.
  - Never `git stash`, amend or rebase.
  - Commit with explicit `-- <paths>`.
  - Check that a file the plan calls new doesn't exist before writing it.
- **Afterwards:** merge into local main, as for 3R, and update memory `loom-scrum-daemon-direction.md`.

## Outcome and follow-ups

(Filled in after execution.)
