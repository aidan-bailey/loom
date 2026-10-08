# Daemon Stage 3A: the Multi-Client Model

> **For agentic workers:** this plan records an implementation the coordinator built and verified in the
> tree before writing it down (see "How this plan was made"). Its packages are committed one at a time, each
> reviewed; nothing here is to be re-typed from the text.

**Goal:** the session model serves every registered workspace and several clients at once, still in one
process: each client keeps its own tabs, classic workspace and selection, and a request's answer reaches
only the client that made it. Stage 3B then moves the model into `loom serve`.

**Architecture:** the model loads the global workspace and every registered one when it boots and never
drops one; a client's first open of a workspace starts its terminal and its GitHub polling. The wire
partitions request IDs by connection, so replies and the events a request causes are routed without a
table. The TUI owns its tabs, classic workspace and failed-open list, and the model only writes the open
list it is given.

**Tech stack:** Go 1.25, Bubble Tea v2, the `core/rpc` NDJSON wire of stage 2, tmux.

---

## Decisions

The user's (2026-10-08):

1. **Split stage 3 in three:** 3A the multi-client model, in process; 3B the daemon process; 3C the
   subcommands as clients plus the duties a daemon has with no TUI (trust prompts, hook scans).
2. **A workspace's terminal starts on a client's first open of the workspace,** and is relaunched on
   death from then on; a live one is always kept.
3. **Several TUIs may connect at once;** the takeover lock is retired (in 3B). Two TUIs attach pane
   clients to the same agent sessions, and tmux sizes each window to the most recently active client.

The coordinator's, from the code (inventory: an Opus mapping pass over `core` and `app`):

- **Routing without a table.** A client numbers its requests from 1, below 2^32. The server ORs its
  connection's number into the high 32 bits of every `core.ReqID` parameter (generated dispatch calls a
  `tag` hook per `ReqID` param) and masks it out on the way back: a `Reply` and a request's `Notice` go to
  the client that made it; `Started` and `Recovered` go to every client (each attaches the pane) with
  `Req` named only to the requester. A table would leak: a Create's `Reply` comes at once, its `Started`
  much later.
- **The request is ambient in the model** (`Model.cause`): while the model applies a request's job
  result, the `Started`, `Recovered` and `Notice` events it emits carry the request, and the jobs it
  spawns serve it too, so the end of a chain (Create → start → prompt send → `Started`) still names it.
  The model's own jobs (gated jobs, the tick's probe, dead checks) never inherit it.
- **Selection per client**, merged by the server into the model's set (`Backend.SetSelection`).
- **One `opened` flag per workspace gates the terminal, its probing and GitHub polling.** An unopened
  workspace's terminal is dormant (not probed, its crash-restart deferred); its first open relaunches a
  dead terminal, or one its restart breaker stopped (Paused for good before: a pre-existing bug).
- **A failed load is kept, latched,** and left out of the orphan sweep's owned roots; every other
  workspace is still swept (one broken workspace used to skip the sweep). Opening it rereads it from disk.
- **Reconcile proves a live session is the record's** before restoring onto it or killing it: tmux names
  are per server, so another workspace's same-titled session used to be adopted or killed.
- **Session launch flags per config dir** (loom context, subagent tracking): with every workspace loaded,
  the process-wide flags would follow whichever loaded last.
- **Protocol 2:** C removes event fields.

## Behaviour a user can notice

- Starting loom loads every registered workspace and the global one: crashed agents in workspaces not
  open are relaunched at start, dead ones are marked Paused by the tick, and the orphan sweep covers every
  served workspace's roots. Startup does more work the more workspaces are registered.
- `loom -p prog X` no longer launches X's workspace terminal with `prog`: a terminal uses its workspace's
  configured program.
- Closing a tab, or entering global mode, no longer saves or drops anything: the workspace stays served.
- A workspace terminal its breaker stopped relaunches on the workspace's next first open (after a start).
- A start or recovery another client asked for attaches its pane but neither moves the selection, attaches
  inline nor shows a notice.
- The remote-control auth detection at startup follows the global config's setting, not the startup
  workspace's (differs only for `loom --workspace X` when the two disagree).

## Packages

### A — safe to load everything

Changes nothing a client calls; makes the rest safe.

- `session/reconcile.go`: `startedElsewhere`. A non-Paused record whose tmux name is alive but whose
  session started outside the record's own worktree (its repository, for a workspace terminal) reads as
  dead for that record: no restore onto another workspace's session, no kill of it. An unreadable start
  directory keeps the old behaviour. Test `session/reconcile_foreign_test.go`.
- `session/loom_context.go`, `session/subagent_hooks.go`: the two launch flags per config dir
  (`dirFlags`); `core.syncSessionFlags`. A launch reads its own instance's dir; `SaveSettings` changes only
  its workspace's.
- `core/model.go`, `requests.go`, `gate.go`, `tick.go`: `Model.cause`, `caused`, `causedBy`, `spawn`
  inheriting the cause, `spawnBackground` for the model's own jobs; `Req` on `Notice`, `Started`,
  `Recovered` (and `Notice`'s JSON). Test `core/cause_test.go`.
- The selection as a set: `Model.SetSelection`, `Tick()` reads it, `selectedInstances`.
- RED-checked: the ownership guard, the stamping, the inheritance, the gate's exemption, Create's wrap.

### B — routing on the wire

- `core/rpc/internal/gen`: `dispatch` takes `tag func(*core.ReqID) error`, called for every `ReqID`
  parameter (`TestGenerate_TagsEveryRequestID`).
- `core/rpc/route.go`: `tagReq`, `local`, `routed`, `forConn`, and the per-connection selection
  (`connBackend`, `setSelected`, `dropSelection`). The server numbers connections and routes at publish.
  `Backend` gains `SetSelection`; `Loop.SetSelection`.
- `app/completions.go`: `applyStarted`/`applyRecovered` select and attach inline only for `Req != 0`.
  `core.CausedForTest` lets tests deliver a result as the TUI's own request.
- The protocol reference gains a "Request IDs" section.
- Tests `core/rpc/route_test.go` (every event naming a request is routed; two clients' colliding IDs;
  a request's Notice and Started; the merged selection), `TestInstanceStarted_AnotherClientsStartOnlyAttachesItsPane`.
  RED-checked five ways.

### C — the model serves every workspace

The TUI's API was kept for this package, so the model change was tested against an unchanged TUI.

- `core/load.go`: `boot` (startup workspace, global, every registered one; one sweep), `loadWS` (the one
  load path; the terminal's crash-restart deferred to its first open), `ensureLoaded`, `globalWS`,
  `retryLoad` (rereads the state from disk), `open`/`ensureTerminal` (create, crash-restart, or relaunch a
  tripped terminal, breaker reset), `sweepOrphans` (failed workspaces left out; terminal titles claimed).
- `core/workspaces.go`: `Loaded()` is every served workspace; an unopened workspace's terminal is not
  active; the tab transitions stop loading, saving and dropping.
- `core/github.go`: `openedRepos` (opened workspaces; the start directory for an opened workspace with no
  repository, as classic mode always polled).
- Dead code removed: the reopened-twin adoption, closed-owner notes, `Started`/`Recovered`
  `Loaded`/`ClosedNote`; `Protocol` 2.
- Tests `core/boot_test.go` (ten), `TestGHQuery_OnlyOpenedWorkspacesArePolled`, the app sweep test
  rewritten as `TestOrphanSweep_SparesWhatThisLoomCannotVouchFor`. RED-checked ten ways.

### D — the tabs, the classic workspace and startup are the client's

- `Core` loses `LoadClassic`, `RestoreSaved`, `StayGlobal`, `KeepRestoreFailed`, `OpenNames`,
  `RestoreFailed`, `Classic`, `Tabs`, `EnterGlobal`, `CloseTab`, `OpenTab`, `InitAccounts`, `SetRCAuth`,
  `Begin`, `Program`, `SetProgram` and `Save`; gains `Open(id)`, `Workspaces()` (local) and
  `PersistOpenList(names)`; `Register` returns the view. `WorkspaceView.LoadErr`,
  `RegistryView.LastUsed`; `WorkspacesChanged` loses `Classic`.
- `Model.Boot()` (not in `Core`): accounts, the remote-control detection (the global config's
  setting), `boot`; it returns its notices, since no client is connected yet. `rpc.InProcess` calls
  `Begin`.
- The TUI keeps `program`, `startupName` and `failedOpen`; startup, the restore, opening and closing
  tabs, global mode, the toggle, register, quit and the picker's open list are its own
  (`startHome`). `checkSlotInvariant`: every slot shows a served workspace.
- Tests for `Open` (retry, unknown ID, nothing tab-like published), `Workspaces` parity, the restore's
  `failedOpen`, `PersistOpenList`, Boot's notices, startup with a workspace that will not load.
  RED-checked eleven ways. Deleted: the model-side tab tests (pinned now in the TUI's invariant tests)
  and the pre-toggle save tests (no save remains).

### E — docs

CLAUDE.md (Core Flow, the `core/`, `core/rpc/` and `app/` bullets, the focused-slot, orphan-sweep and
no-model-mutation gotchas, Testing Patterns), `docs/specs/workspaces.md`, the daemon spec's rollout and
amendments, and this plan's outcome.

## Verification

Per package: gofmt, `go vet ./...`, `go test -count=1 ./...`, `-race` on core, core/rpc and app, e2e.
Then a sandbox smoke run (loomdev) of the startup paths, tab open/close, global mode, a broken workspace,
first-open terminals, and two clients' replies (in-process test only in 3A).

## How this plan was made

The coordinator built A–C in the tree (D by an Opus implementer from a brief), each green with RED checks,
then wrote this record. Stage 2's replay-from-plan step (implementers re-typing verified code) added nothing
but cost, so the packages are committed from the tree and reviewed.

## Follow-ups for 3B and 3C

- 3B: a daemon's notices while no client is connected (Boot's are handed to the first client by hand
  today); the sticky fatal (exit or respawn); the GitHub poll's start directory for the global workspace
  means nothing in a daemon; the takeover lock retires.
- 3C: unloading a workspace removed or renamed in the registry (today it stays served until restart);
  the headless duties (trust prompts, hook scans with no TUI).
- Two global dirs registering the same repository both own its sessions' roots, and each start's sweep
  could kill the other's orphans-by-its-lights. Rare; worth a guard in 3B.
- Startup time grows with the number of registered workspaces (each is reconciled). Measure in the smoke.


## Outcome and follow-ups

Executed 2026-10-08. The coordinator built A–C in the tree, and an Opus implementer built D from a brief
(`brief-D.md`). Each package was green with RED checks before it was committed. The user chose "commit,
then review": the packages were committed from snapshots, built in the index, and each commit builds, vets
and passes on its own. Then came:

- a reviewer per package (Opus), with fix waves as fixups that the filer re-checked;
- a sandbox smoke run against the 7b10467 baseline;
- the docs (E);
- a final cross-cutting review and its re-checks.

The fixups could not be squashed in place: some fixed a package's rule with tests whose context a later
package wrote. So the packages stay as committed (each builds, vets and passes on its own), the 33 code
fixups are one review-fix commit after D, and the docs commit comes last with its fixups folded in, so it
describes the final code. The tree is identical to the reviewed one. Assertions 8847 → 9262; race and e2e
green.

| Commit | What |
|---|---|
| 49e3e84 | the plan |
| b18dbc5 | A: ready the model to serve every workspace |
| f242250 | B: route a request's answer to the client that made it |
| 9521b4b | C: serve every registered workspace |
| 2f48353 | D: the tabs, the classic workspace and startup are the client's |
| a9aa403 | the review fixes, for all four packages |
| 96365ea | E: CLAUDE.md and the specs |

### What each mechanism found

- **The inventory** (an Opus map of `core` and `app` before the design) found:
  - replies colliding across TUIs;
  - every idle TUI inline-attaching on any client's create;
  - same-titled sessions across workspaces adopted or killed by reconcile;
  - per-workspace launch flags that were process-wide;
  - dormant terminals and the breaker-stopped terminal stuck for good;
  - the sweep skipped whenever one workspace failed.
- **The prototype's own tests** found that a failed workspace's retry re-read the payload its storage was built
  over (`retryLoad` now rereads disk).
- **The smoke run** (8 checks, no regressions):
  - a terminal created on first open was unsaved until quit;
  - startup cost is about +60 ms per registered workspace.
  The sweep still runs past a broken workspace, which the baseline skipped.
- **Package A's review:** ready. Its Important finding was older than 3A: the ownership guard covered only
  reconcile. That was partly fixed then (a held name goes straight to Paused) and fully fixed after the
  final review.
- **Package B's review:** ready. Its seven Minors included:
  - an untested recover that stops a crash on disconnect after a model panic;
  - no app test of the real routing path;
  - the generator's blind spot for nested `ReqID`s.
- **Package C's review:**
  - **Critical:** `ensureTerminal`'s relaunch of a Paused terminal killed a foreign same-named session by name,
    undoing A's guard. Fixed with the fail-closed `HeldElsewhere`.
  - **Important:** quit was blocked by an unreachable, never-opened workspace.
  - **Important:** a symlinked second registry entry was loaded twice, and the stale twin's save overwrote
    the other's changes.
  - **Re-check:** a terminal's "own" included its agents' worktrees and nested workspaces; it is now an exact
    repo match.
- **Package D's review:**
  - **Important:** `ModelView.Clone` dropped `RegistryView.LastUsed`, so the restore always focused the first
    tab and then overwrote LastUsed.
  - **Important:** nothing tested that production starts the health tick.
  - Seven restore and transition rules were unpinned.
  - **Re-checks:** a workspace renamed while loom runs, and symlinked twins at startup and restore (now refused
    in the picker, and resolved to the served twin at startup).
- **The final cross-cutting review:** ready. Its Important finding: Kill, discard and resume still acted on a
  name another workspace's session holds (proven against real tmux), which serving every workspace made far
  easier to reach. Fixed. Its Minors included:
  - a GitHub base branch overridden by the cwd stand-in;
  - a `$HOME` workspace folded into the global one;
  - three doc inaccuracies;
  - six unpinned rules.

  **Its re-check found a Critical that the fix wave introduced.** The new Kill guard read the record's home
  after Kill had cleared its worktree, so every D on a running agent would have left the agent running in a
  deleted worktree. No test had a positive control (the record's own session). One now does, for kill and
  discard, against real tmux and real worktrees.
- **One flaky test** (a tmux server exiting with its last session, raced by the next `new-session`) led to
  `isolateTmux` in core's real-tmux tests.

### Deviations from the plan

- The selection merge and the request partitioning were designed during prototyping, not in an earlier plan.
- `Save(id)` also left `Core`, since its only caller was the pre-toggle save.
- `RegistryView` gained `LastUsed`.
- `Model.Boot` returns its notices, since no client is connected yet.
- The unopened workspace's save at quit is best-effort, and is skipped when its config dir is missing.
- Duplicate open-list names give one tab, and the invariant refuses two tabs over one workspace.
- Twins are served once, and resolved at startup.

### Behaviour changes found after the plan

- An agent that exits while its tab is closed is marked Paused by the tick, as in an open tab. The baseline
  crash-restarted it when the tab reopened.
- Settings toggles (loom context, subagent tracking) affect only their own workspace.
- After `loom workspace migrate` a running agent reads as foreign and lands Paused; `r` reattaches it.
- GitHub polling continues for every workspace opened during the run, closed tabs included.
- The global `state.json` is rewritten at quit (timestamps only).

### Follow-ups

**3B:**
- **Routing and notices:**
  - A request's Notice whose requester has disconnected is dropped unlogged; broadcast or log it.
  - Untagged non-zero request IDs reach every client; drop and log an untagged Reply.
  - `Open`'s "remote control off" notice goes to every client.
- **Shared and model-wide state:**
  - `ws.opened` is model-wide and never clears, and the sticky quit counts closed tabs.
  - A client quitting should not run `SaveForQuit`; the daemon saves.
  - The registry's open list, last-used and UI prefs are shared, so two TUIs overwrite each other.
- **Process context:** `m.program` and `os.Getwd()` (the global workspace's GitHub stand-in) mean nothing in a
  daemon.
- **Boot:**
  - It runs every workspace's crash restarts synchronously.
  - Its per-record tmux checks could be one listing.
  - Its notices reach the first client by hand.
- **Other:**
  - Two global dirs registering one repository both own its roots.
  - The takeover lock retires.

**3C:**
- Unload and reload a workspace renamed or removed in the registry. Today it is refused by its new name until
  a restart.
- `loom reset` or `workspace migrate` run while a TUI is up is undone at quit, now for unopened workspaces
  too.
- Reopening a closed tab doesn't re-reconcile its orphans; they appear on the next start.
- The headless duties: trust prompts, and hook scans with no TUI.

**Known, accepted:**
- `HeldElsewhere` fails closed only where loom would kill or replace; reconcile's own check still fails open.
- KillAndPause still kills by name when a record has no dir of its own.

**Pre-existing:** the startup picker's `esc` commits the highlighted workspace, while its help line says
"esc global".
