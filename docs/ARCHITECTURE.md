# Architecture

Facts, not rules. Nothing in CI verifies this file; re-check it with [`docs/claude/auditing-claude-md-currency.md`](claude/auditing-claude-md-currency.md).

Loom runs AI coding agents in parallel, each in its own git worktree and tmux session. One process per global config dir, the daemon (`loom serve`), owns every session and outlives every TUI; each `loom` TUI is its client. The rules for changing a package are in its `CLAUDE.md`, and its facts in its `README.md`; this file covers what spans packages.

## Packages

| Path | Role | Reference |
|---|---|---|
| `main.go`, `serve.go`, `daemon_client.go` | The Cobra CLI, `loom serve`, and the TUI's join to the daemon | [`internal/daemon/README.md`](../internal/daemon/README.md) |
| `app/` | The Bubble Tea TUI: views, key routing, overlays, the workbench, the Lua host | [`app/README.md`](../app/README.md) |
| `ui/`, `ui/overlay/`, `ui/review/` | View components: rail, cards, overview, panes, modal dialogs, the review pane | [`ui/README.md`](../ui/README.md) |
| `script/` | The Lua engine; `script/defaults.lua` is the built-in keymap | [`script/README.md`](../script/README.md), [`docs/specs/scripting.md`](specs/scripting.md) |
| `keys/` | Key names and lookup maps | the package itself |
| `core/` | The session model: workspaces, requests, jobs, completions, the health tick, the loop | [`core/README.md`](../core/README.md) |
| `core/rpc/` | The wire: `core.Core` as newline-delimited JSON, the server, the client's replica; the generator in `core/rpc/internal/gen` | [`core/rpc/README.md`](../core/rpc/README.md), [`docs/specs/protocol.md`](specs/protocol.md) |
| `internal/daemon/` | The lock, the socket, `Serve`, `Stop`, `Connect` | [`internal/daemon/README.md`](../internal/daemon/README.md) |
| `session/` and its sub-packages | `Instance`, storage, reconcile and orphans, worktrees (`session/git`), tmux (`session/tmux`), the emulator (`session/vt`), Claude status, hooks, accounts at launch | [`session/README.md`](../session/README.md), [`session/tmux/README.md`](../session/tmux/README.md) |
| `account/` | Extra Claude accounts: config dirs, links, auth, usage | [`account/README.md`](../account/README.md) |
| `config/` | `config.json`, `state.json`, the workspace registry, dir resolution | [`config/README.md`](../config/README.md), [`docs/specs/workspaces.md`](specs/workspaces.md) |
| `cmd/` | `loom workspace`, `loom account`, the executor seam | [`cmd/CLAUDE.md`](../cmd/CLAUDE.md) |
| `log/` | Structured and legacy logging | [`log/CLAUDE.md`](../log/CLAUDE.md) |
| `review/`, `review/gitdiff/` | Review comments vendored from crit, and changed-line maps | [`review/CLAUDE.md`](../review/CLAUDE.md) |
| `internal/exec/`, `internal/testenv/`, `internal/testpty/` | The git/gh subprocess seam; test isolation and repo-wide enforcement tests; fake PTYs | [`internal/exec/CLAUDE.md`](../internal/exec/CLAUDE.md), [`internal/testenv/CLAUDE.md`](../internal/testenv/CLAUDE.md) |
| `internal/devsandbox/`, `tools/`, `e2e/` | Dev sandboxes, `loomdev`, `fakeagent`, `claudemd`, and the end-to-end suite | [`internal/devsandbox/README.md`](../internal/devsandbox/README.md), [`tools/CLAUDE.md`](../tools/CLAUDE.md), [`e2e/CLAUDE.md`](../e2e/CLAUDE.md) |

## Core flow

`main.go` (Cobra CLI, joining the daemon: `daemon_client.go`) → `app/app.go` (Bubble Tea model, the view) → `core/rpc` (`rpc.Client`, the TUI's `core.Core`, over a unix socket) → `loom serve`, the daemon (`serve.go`, `internal/daemon`: an `rpc.Server` serving the loop) → `core/` (`core.Model` on its `core.Loop`) → `session/instance.go` instances.

**Keys.** The app follows Bubble Tea's Model-View-Update pattern: `app/app.go` owns the `home` model and its `Update`/`View`. `handleKeyPress` dispatches by `m.state` to a per-state handler in `app/state_*.go`; in the default state, keys flow through the Lua engine via `app/app_scripts.go:dispatchScript`, which consults `script.Engine.HasAction` and returns a `tea.Cmd` that drains the resulting `scriptDoneMsg`. The built-in keymap is `script/defaults.lua`, embedded at build time; user scripts in `~/.loom/scripts/*.lua` rebind or add keys.

**The daemon's start.** `daemon.Serve` takes the global dir's lock, builds the model (`core.New`) and boots it: `Model.Boot` (`core/load.go`) loads the account registry, detects the default account's remote-control auth, loads the global workspace and every registered one, and runs one orphan sweep over them all. Boot returns its notices, which the server keeps for the first client (`Server.Keep`), since none is connected yet. The model then runs on a `core.Loop` (`core/loop.go`), which the daemon begins (`Loop.Begin`: the first accounts refresh and usage probe, and the arming of the health tick), served by an `rpc.Server` on a socket the daemon listens on only once booted.

**A TUI's start.** `main.go` resolves the startup workspace (a directory argument or `--workspace`, else the global one) and joins (`joinDaemon`): `daemon.Connect` dials the socket the lock records, spawning `loom serve` detached when no daemon runs; `rpc.Dial` exchanges hellos and returns once the client's replica holds the snapshot; the newer build wins (`daemonLink.join`, `rpc.CompareBuilds`); and the TUI pins the daemon's tmux server (`tmux.UseServer`) before anything of its own touches tmux. `app.Run` takes the client, and `startHome` (`app/app_init.go`) builds the TUI over it: it rereads the registry (`ReloadRegistry`), records a named startup workspace as last used (`SetLastUsed`), shows the startup workspace (`home.startupName`, from the `wsCtx` `main.go` resolved; "" is the global one) in its classic slot, and opens it (`Core.Open`) unless it restores the tabs the last run left open (`restoreSavedWorkspaces`) or asks through the workspace picker. It drains after each model call and hands the resulting Cmds to `Init` (`home.initCmd`).

**Requests, queries and casts.** The client keeps a full replica of the model's published state, sent whole when it connects and every change after, so every query (`rpc:local` in `core/iface.go`) is answered locally and only actions cross the wire. A request waits for its reply, and the server sends a request's events ahead of its reply, so a read right after a request sees its effect. A cast (`SetSelected`, the pane triggers, the requests for a sooner poll or probe) goes one way, with no reply. The TUI sees instances only as `core.InstanceView` values and acts on one by a request naming its `core.InstanceID`.

**Jobs and wakes.** Blocking work is a `core.Job` (`func() any`) the model queues; the loop runs it on a goroutine of its own and delivers its result on the loop (`core.Model.Deliver`). After anything it did unprompted (a job's result, its tick) the loop signals `Loop.Wakes`, and the server publishes. The client signals `Client.Wakes` when events arrive, coalesced to one pending wake, which `Run`'s `forwardWakes` goroutine turns into a `coreWakeMsg`; nothing on that path blocks the client's reader or the loop, since `p.Send` waits for Update.

**Events.** The model reports what the view must do as `core.Event`s (`core/events.go`, all listed in `core.EventTypes()`). The state events carry the published state: `WorkspacesChanged`, `ModelChanged`, `AccountsChanged`, `GitHubChanged` and `ViewsChanged`. The others are `Reply`, `Notice`, `InstancesChanged`, `ClientsStale`, `SessionLaunched`, `Reactivated`, `Started`, `Recovered`, `StatusesChanged`, `Alive` and `HealthChecked`; [`docs/specs/protocol.md`](specs/protocol.md) has every field. `home.Update` runs the message switch, then `drainCore` (`app/core_glue.go`), which empties the client with `Sync` (`rpc:client`: it never leaves the client) and applies each event (`applyCoreEvent`), then `publishSelection`, which tells the model the selected row when it moved. `Sync` returns the state events first, coalesced to the newest of each kind and in the model's publish order (the workspaces, the model, account and GitHub views, then each changed workspace's views), then the other events in arrival order.

**Losing the daemon.** A panic on the loop's goroutine or in a job is a `core.LoopPanic`: the server turns it into a `panic` reply and a `Fatal` frame to every connection and closes `Server.Fatal()`, and the daemon exits without saving and without a bye. A graceful stop says bye first (below). The daemon's client (`rpc.Dial`) records the loss in `Client.Err` (matching `core.ErrUnavailable` when a bye came first, so a stop is told from a crash); it answers local reads from its last replica, refuses requests at once as `ErrUnavailable` and drops casts. An in-process client (`rpc.InProcess`, `core/rpc/inprocess.go`, which only tests use) re-raises the loss in the caller instead.

The TUI does not quit when it loses the daemon; it goes offline (`app/link.go`). `home.Update` checks the link after its drain (`checkLink`), so the replies that arrived before the connection closed are applied first. The link is *connected*; *stopping* (a bye arrived: requests refused, replies in flight still come); *waiting* (the connection closed after a bye): the TUI polls for a daemon once a second and starts none, and `ctrl+r` starts one; or *reconnecting* (closed with no bye, or the model failed): it redials on a backoff from 1s to 30s and starts a daemon when none runs, falling back to waiting after three that did not start. A banner names the state. Offline, panes keep rendering and attaching (they talk to tmux), the keys that need the model are refused with an info line (`offlineKeyAllowed`), a request made anyway is refused by the client, and every request still waiting for a `Reply` is failed with a synthetic unavailable one. UI prefs and the focused tab changed offline are kept and sent on rejoin. A rejoin (`Rejoin`, the startup join made quiet) follows the startup rule: the same build is rejoined, an older daemon is replaced once, and a newer one ends the TUI ("loom: the loom daemon was replaced by a newer loom (X); run loom again"). `resync` swaps the client in and brings the TUI up to date from its replica, keeping tabs, selection, drafts and overlays: instance and workspace IDs derive from the disk (a hash of the canonical config dir, the title and `created_at`), so every daemon over one disk names a record alike.

**Health tick.** The model's half (`core.Model.Tick`, `core/tick.go:tickInterval`: 3s, 500ms on the snapshot path) fires from the loop's own timer, probes every active instance and dispatches the gated background jobs; the loop re-arms it when the probe lands. The TUI's half (`tickUpdateMetadataMessage` in `app/app.go`) prunes pane clients and runs the TUI-side scans. With the emulator on, panes render on output events, not on a tick.

## View modes

The default state renders in one of three view modes (`m.viewMode`), orthogonal to `m.state`:

- **Focus**: the session rail beside the agent/terminal split. The classic layout.
- **Overview** (`ui/overview.go`, toggled with `tab`): a fleet-triage card grid. `enter`/`esc` return to focus; `n`/`N` drop to focus first, then run the create flow; `z` collapses a group; mouse input is dropped. It spans the open (tab-bar) workspaces only, each as its own card group (the focused one first, then alphabetical); registered workspaces that aren't open as tabs don't appear, though the model serves them all. Its global cursor `home.overviewCursor {slot,inst}` translates to render coordinates `ui.OverviewCursor {Group,Item}` in `overviewData`; `j/k` walk `fleetOrder` across all groups, and keys that commit the cursor route through `focusCursorSlot()` (`loadSlot`, then select), so `enter`, `D`, `r` and `n` reuse focus mode's intents on the right workspace. Focus mode's `]`/`[` (`jumpWaiting`) also cross open workspaces.
- **Workbench** (`enter` in focus): the agent pane on the left and a tabbed panel (markdown, diff, files, terminal, review) on the right. Never persisted: a restart lands in focus.

Focus or overview persists per workspace in `state.json`'s `ui` block, so switching workspaces applies the target workspace's mode.

## Session lifecycle

Statuses: `Ready` (initial), `Loading` (setup in progress), `Running` (agent active), `Paused` (worktree removed, branch preserved), and `Recoverable` (an orphaned worktree found on disk, surfaced inline for recover or discard; never persisted).

1. **New**: `n`/`N` opens an overlay that collects a title and an optional prompt into the TUI's draft row; no instance exists yet. Confirming Launch Options sends `Create`, which builds the instance (Ready) and starts it at once; a script's `ctx:new_instance` leaves it unstarted.
2. **Start**: creates the git worktree and tmux session and records the base commit: Loading, then Running.
3. **Running**: the agent works in its worktree; the TUI shows its live output and diff stats.
4. **Pause**: stashes uncommitted changes (`StashRef`), kills the tmux session, removes the worktree; the branch stays. Paused.
5. **Resume**: rebuilds the worktree from the branch only when it is absent or gutted (no `.git`; leftovers moved aside), restores the stash, and starts a new tmux session. An intact worktree is never rebuilt: the agent is relaunched in place with its recovery flag, and a live session is reattached (`session/resume_decision.go:decideResume`). Running.
6. **Kill**: cleans up the worktree, the tmux session and the branch, and removes the instance from storage.

The TUI starts each of these by a request naming the instance's `InstanceID` (`core/requests.go`: `Create` with `Start` for steps 1 and 2, `Pause`, `Resume` or `ResumeWith`, `Kill`, `Recover`); the model starts the job doing the blocking work (`core/ops.go`) and applies its result (`core/completions.go`).

**Workspace terminals** are instances with `IsWorkspaceTerminal: true` that run directly in the root repository, with no worktree. They can't be paused or resumed, and their diff shows the root repository's uncommitted changes. One starts at a client's first open of its workspace (`Core.Open`, `ensureTerminal`), not when the model loads it; until then it is dormant (not probed, any crash-restart deferred), and from then on it is relaunched on death under a restart breaker (`workspace_terminal.restart_circuit_tripped` when it trips). A terminal the breaker stopped relaunches at the workspace's next first open, after the daemon restarts. A dead or Paused terminal whose tmux name another session holds (`session.HeldElsewhere`) is never relaunched over it, and a check tmux leaves unanswered is retried by the health tick (`maybeSettleTerminals`).

## The daemon's lifetime and environment

- **One per global dir.** Two processes writing one workspace's sessions drop each other's records, so one process owns the model and writes `state.json`, holding the flock on `<globalDir>/loom.lock` for its whole life; any number of TUIs connect to it at once. Each TUI attaches its own pane clients, and tmux sizes a window to its most recently active client. The open list, last-used workspace and UI prefs are shared values: the last TUI to write one wins.
- **Lifetime.** The first `loom` that finds no daemon spawns one, detached; it runs until SIGTERM, SIGINT, SIGHUP or `loom serve stop`, and the first signal stops it gracefully, in this order: it says bye to every client (which then refuse new requests), stops accepting, waits for foreground jobs while their replies still reach the connections left open, saves every workspace, and only then publishes once more, flushes and closes the connections (`Server.Bye`, `Loop.Quiesce`, `SaveForQuit`, `Server.Close`). Sessions are tmux's and keep running through any stop; the next daemon reattaches them at its boot.
- **Saves are the daemon's**: on requests, after changes it makes on its own, and at its stop. A TUI's quit writes only its own state (split ratios, the open list), and offline it writes nothing (`quit.offline_unsaved`).
- **Upgrades.** The newer build wins at every join: a newer daemon refuses an older TUI, and a newer TUI stops the older daemon it dialed (`daemon.StopPID`, which signals only that pid; `daemon.Stop` is `loom serve stop`) and starts its own build. A TUI still open on the old daemon sees a graceful stop, waits, finds the newer daemon and exits saying so. `replaceGuard` refuses the replacement from inside loom.
- **Frozen environment.** The daemon keeps the environment of the `loom` that spawned it. That decides the tmux server it pins (kept from the last daemon while that server runs), the credential override and `$CLAUDE_CONFIG_DIR` its warnings read, Claude's temp root, `LOOM_PANE_RENDERER` for the model's tick, and the `update-environment` variables every agent gets at `new-session` (`SSH_AUTH_SOCK`, `DISPLAY`, …). The rest of an agent's environment (`ANTHROPIC_*`, the default account's `CLAUDE_CONFIG_DIR`, `PATH`) is the tmux server's global environment, the daemon's only when the daemon started that server. What a TUI starts itself keeps the TUI's environment: the terminal pane's shells, a relative or `~` path in `ctx:new_instance`, a login from the Accounts screen. To pick up a new environment, `loom serve stop`, then start loom from it.

## Persistent state

All under `~/.loom/` (the global dir) or a workspace's `<repo>/.loom/`.

| File | Holds | Details |
|---|---|---|
| `config.json` | User settings: default program, branch prefix, base branch, profiles, theme, Claude launch defaults; written only by the model | [`config/README.md`](../config/README.md), [`USAGE.md`](../USAGE.md) |
| `state.json` | App state and the `ui` prefs block (view mode, rail and terminal visibility, split ratios per session title), plus the `instances` array of serialized sessions | [`config/README.md`](../config/README.md), [`session/README.md`](../session/README.md) |
| `workspaces.json` (global dir) | Registered workspaces, last-used tracking and the open list | [`config/README.md`](../config/README.md) |
| `loom.lock` (global dir) | The daemon's lock and, on Unix, its record (`loom.lock.json` beside it on Windows); never removed | [`internal/daemon/README.md`](../internal/daemon/README.md) |
| the daemon's socket | `$XDG_RUNTIME_DIR/loom/<hash>.sock`, else `run/serve.sock` in the global dir, else the temp dir; named in the lock record | [`internal/daemon/README.md`](../internal/daemon/README.md) |
| `logs/loom.log`, `logs/serve.log`, `logs/serve-crash.log` | The TUI's log; the daemon's log and its runtime-crash output (global dir) | the next section |
| `accounts.json`, `accounts/<name>/` (global dir) | Extra Claude accounts and each one's `CLAUDE_CONFIG_DIR` | [`account/README.md`](../account/README.md) |
| `worktrees/` | Session worktrees, each with a `.loom-title` sidecar beside it | [`session/README.md`](../session/README.md) |
| `hooks/` | Per-instance Claude hook folders | [`session/README.md`](../session/README.md) |
| `archive/claude-tmp/` | Archived Claude temp dirs (or under `claude_tmp_archive_dir`) | [`session/README.md`](../session/README.md) |
| `scripts/` (global dir) | User Lua scripts, loaded at startup | [`docs/specs/scripting.md`](specs/scripting.md) |

## Logs and debugging

- The TUI writes `{configDir}/logs/loom.log`, rotated once to `.log.1` at startup when over 5 MB. The daemon writes `<globalDir>/logs/serve.log` (`log.Initialize(dir, true)`): never rotated at startup but rotated as it grows, its records tagged `component=daemon` and its legacy lines `[DAEMON]`; a runtime crash it can't log goes to `serve-crash.log` beside it (`debug.SetCrashOutput`).
- Each process logs to its own file. The daemon's file holds `subsystem=serve`, `core`, `account`, `github`, the server's `rpc` lines, and the `session` and `tmux` lines of the lifecycle it runs; the TUI's holds `subsystem=app`, the client's `rpc` lines and its pane clients' `tmux` lines. A TUI whose daemon fails to start quotes what the daemon added to both files.
- `loom debug` prints both paths, the daemon (pid, socket, build and tmux server; or "not running", or "starting"), the tmux server a daemon started now would pin, the nesting guard's answer on it, and the effective log level and format. Its Claude temp root comes from its own environment, not the daemon's.
- `LOOM_LOG_LEVEL=debug` or `--log-level=debug` turns on debug output, which goes only through the structured logger (`log.Debugf`, `log.DebugKV`). Code logs through `log.For("subsystem", ...)`, a pre-tagged `*slog.Logger`, or `log.InfoKV/WarnKV/ErrorKV/DebugKV`; records carry `subsystem=...`, so `grep subsystem=tmux loom.log` scopes the file to one component.
- The model logs under `subsystem=core` (`log.For("core")`), the account registry and the GitHub poller under `subsystem=account` and `subsystem=github`, and the TUI under `subsystem=app`. Debug records include `core.roster.query_failed` and `core.hook_scan.failed`; a job result the model doesn't recognize is logged as `deliver.unknown_result` and dropped.

## Environment variables

User-facing descriptions are in [`USAGE.md`](../USAGE.md). What developers need on top:

- `LOOM_HOME`: Overrides a process's config dir (default `~/.loom`; absolute, `~` expanded). A backward-compatible fallback: internal code threads `WorkspaceContext` explicitly.
- `LOOM_GLOBAL_DIR`: Relocates `GetGlobalConfigDir()` (the registry and global context), which ignores `LOOM_HOME` by design; also disables the legacy-home migration. It selects the daemon: one per global dir, so a loomdev sandbox has its own.
- `LOOM_TMUX_SOCKET`: Adds `-L <name>` to every tmux invocation through `tmux.Command`, overriding `$TMUX`. A starting daemon resolves its server from it (`tmux.ResolveServer`) unless the last daemon's server still runs, then pins it (`tmux.UseServer`); a TUI uses the server the daemon's hello names. It gets nothing past the nesting guard or `replaceGuard`.
- `LOOM_ALLOW_NESTED`: `1` bypasses the nesting guard of `loom serve` and `loom reset`; never `replaceGuard`.
- `LOOM_LOG_FORMAT`: `json` makes `log.InfoKV/WarnKV/ErrorKV` emit JSON lines; the legacy `InfoLog`/`WarningLog`/`ErrorLog` writers stay plain text.
- `LOOM_LOG_LEVEL`: `debug|info|warn|error` (default `info`); gates the structured logger and, at the writer layer, the legacy writers. The persistent `--log-level` flag wins over it.
- `LOOM_PANE_RENDERER`: `snapshot` disables the embedded VT emulator and falls back to `tmux capture-pane` snapshots (also the path on Windows). Unset, panes render from the emulator: mouse forwarding, event-driven updates with no render or status polling, the native hardware cursor, and title, bell and focus pass-through. Scroll-back is the emulator's (x/vt scrollback through `vt.Emulator.RenderWindow`, seeded once per attach from `tmux capture-pane -S - -E -1`). Each process reads its own: a TUI's picks its panes' renderer and its half of the tick, the daemon's the model's tick interval.
- `CLAUDE_SQUAD_HOME`, `CLAUDE_SQUAD_LOG_FORMAT`, `CLAUDE_SQUAD_LOG_LEVEL`: Legacy fallbacks, honoured with a one-time deprecation warning to stderr.

## Fork and claude-squad migration

Loom forked from [smtg-ai/claude-squad](https://github.com/smtg-ai/claude-squad) at v1.0.17 (April 2026); see [`NOTICE.md`](../NOTICE.md). On first launch it renames `~/.claude-squad/` to `~/.loom/` atomically (`config/migration.go:MigrateLegacyHome`), and live tmux sessions with the legacy prefix are renamed before reconcile runs (`tmux.RenameLegacySessions`), so running agents keep their panes: `tmux.TmuxPrefix = "loom_"`, `tmux.LegacyTmuxPrefix = "claudesquad_"`, and the orphan sweep recognizes both. Auto-commit tags changed from `[claudesquad]` to `[loom]` at the v0.1.0 cutover; older worktree commits keep the old tag.
