# Daemon Stage 3B: `loom serve`

> **For agentic workers:** this plan records an implementation built and verified package by package; each
> package is committed when green, then reviewed. Nothing here is to be re-typed from the text.

**Goal:** the session model runs in its own process, `loom serve`, one per global config dir, started on demand
and outliving its TUIs; every TUI is a client of it over a unix socket.

**Architecture:** the daemon holds the global dir's lock, boots the model (stage 3A), and serves it with
`rpc.Server` on a private socket it records in the lock. A TUI dials the socket (spawning the daemon when none
runs), settles versions (the newer side wins), pins its tmux server to the daemon's, and exits cleanly if the
daemon goes away. Live reconnect is a later stage.

**Tech stack:** Go 1.25, `core/rpc` over unix sockets, gofrs/flock, tmux.

---

## Decisions

The user's (2026-10-08):

1. **When the daemon goes away under an open TUI** (a crash, `loom serve stop`, a newer loom replacing it), the
   TUI restores the terminal, says so, and exits; the next `loom` starts a daemon. **Live reconnect is its own
   stage after 3B.**

The coordinator's, told to the user without objection:

- The open list, last-used workspace and UI prefs stay shared registry and state values: the last writer wins.
- A TUI quitting saves nothing: the daemon saves, as it always did on changes, and on stop.
- A notice that reaches no client (raised while none is connected, or a request's whose client has gone) is
  kept for the next client, bounded, and logged.
- In global mode GitHub polls the repositories the global sessions run in, not a start directory.
- A model that fails makes the daemon exit; the next `loom` starts a fresh one.

From the inventory (an Opus map of everything that still assumed the model in the TUI's process):

- **The lock stays at `<globalDir>/loom.lock`,** the takeover lock's path, not beside the socket: a lock beside
  the socket would depend on `$XDG_RUNTIME_DIR`, so a client without it could start a second daemon. A loom from
  before the daemon and a daemon exclude each other through it.
- **The socket** goes in `$XDG_RUNTIME_DIR/loom/<hash>.sock`, else `<globalDir>/run/serve.sock` when that fits a
  socket address, else `/tmp/loom-<uid>/<hash>.sock` (a 0700 dir this user owns); the daemon records it in the
  lock and clients dial what the record says. *(Amends spec §1/§5's `<globalDir>/run/` fallback, which overflows
  `sun_path` for deep global dirs.)*
- **Stopping is a signal** (SIGTERM to the pid in the lock record), so a newer loom can stop a daemon of any
  protocol. *(Amends spec §6's stop request.)* A stop waits for in-flight lifecycle jobs (a pause mid-stash)
  before it saves.
- **The tmux server is the daemon's:** it resolves an absolute socket path once and publishes it, and every TUI
  targets it, whatever its own environment says.
- **Build identity includes the executable's hash:** two edited builds at one commit otherwise compare equal.
- **A loom inside a loom pane never replaces the daemon.**

## Packages

### A — the daemon process

`internal/daemon` (the lock and its record, the socket path, `Serve`, `Stop`), `loom serve` and `loom serve stop`
(`serve.go`), `serve.log` and a crash file, the loop's foreground-job count and `Quiesce`, and the server's fatal
signal, hello deadline and notice backlog.

### B — connecting

Spawn on demand and dial, the hello's build identity and the daemon's tmux socket, the newer-side-wins policy,
the TUI as a client of the daemon (a clean exit when it goes), the takeover lock retired, `loom debug`.

### C — the environment

What the daemon's environment decides that the TUI showed from its own: the credential override, an account's
config dir, the global workspace's GitHub repos, relative paths from Lua.

### D — loomdev and e2e

A sandbox daemon (`down` stops it, a rebuild replaces it), and end-to-end daemon scenarios.

### E — docs

CLAUDE.md, USAGE.md (`loom serve`), the spec's amendments, and this plan's outcome.

## Outcome and follow-ups

Executed 2026-10-08 and 2026-10-09. Implementers built A–D from briefs, each package committed when green with RED
checks, then reviewed (the user chose "commit, then review"). Then came:

- a reviewer per package, and fix waves that the filers re-checked;
- the docs (E), written from a map of what was built (`facts-E.md`);
- a sandbox smoke run against the `ffb9db2` baseline;
- a final cross-cutting review, its fix wave, and its re-check.

The history is consolidated as 3A's was. The packages stay as committed, each building, vetting and passing on its
own: B's fixup is folded into B, and A's review fixes into one commit with the Windows build fix, both before C,
which builds on them. The fixes after D are one review-fix commit, and the docs come last, so they describe the final
code. The tree is identical to the reviewed one. Assertions 9262 → 9885; race and e2e green.

| Commit | What |
|---|---|
| c6caa4e | the plan |
| ad199a3 | A: the daemon process |
| 55659ef | B: loom is a client of the daemon |
| 1da2129 | A's review fixes, and the Windows build |
| 7cd1eb4 | C: the daemon's environment decides |
| 079f2e1 | D: loomdev and the e2e daemon scenarios |
| b2ad821 | the review fixes for B, C, D and the final review |
| 3276c2b | E: CLAUDE.md, USAGE.md and the specs |

### What each mechanism found

- **The inventory** (an Opus map of everything that still assumed the model in the TUI's process) settled the lock
  in the global dir, the socket's fallbacks, stopping by signal, the daemon's tmux server, the executable hash in the
  build identity, and that a loom inside loom never replaces the daemon.
- **Package A's review:** the socket watch (2s, and relisten wherever `SocketPath` finds room), a failed `Accept`
  waited out, the socket placed before the boot, an unwritable record fatal, torn records reread, `Stop` limited to
  this host's `serve` pid, a stop meeting a failed model, NewModel's notices kept, a gone client's notice broadcast,
  the daemon's log not rotated by a losing `serve`.
- **The Windows cross-build** (GoReleaser builds Windows; CI does not) found `Setsid`, `syscall.Stat_t` and
  `syscall.Kill` in shared code: split into `proc_unix.go`/`proc_windows.go`, and the record moved beside the
  mandatory lock (`loom.lock.json`).
- **Package B's review:**
  - **Critical:** a loom run in an agent's pane with `LOOM_TMUX_SOCKET` set stopped the user's daemon (the
    replacement decision was the nesting guard's, which a private socket waved through). `replaceGuard` now decides
    it on the daemon's own tmux server, failing closed.
  - A full build tie counted as "client newer" both ways (two installs replaced each other's daemon every launch):
    the hello gained `exe_time`, and a full tie is the same build.
  - A daemon started while another stopped took over afterwards (`acquire`/`liveHolder` stand down at once);
    `Stop` returned before the process had gone; a daemon started from another environment found every agent dead
    and relaunched each on its own tmux server (the record now carries the server, `TmuxServer` keeps it); a long
    boot outlasted the client's timeout; the daemon kept the spawning client's cwd for life.
  - The daemon saved an agent's pause on a dead session, and a conversation a hook scan adopted, only at quit
    (`saveUnprompted`); stale "restart loom" advice; untested wiring (the tmux pin, the handshake bound).
- **Package C/D's review:**
  - **Important:** the global-mode issue picker loaded forever (the poll no longer covered the cwd): `WatchGitHub`.
  - GitHub state keyed by the first spelling of a path (a symlinked or subdirectory session got no state): keyed by
    the repository's top level (`git.RepoRoot`, `ghRepoKey`, `GitHubView.Aliases`).
  - The running-as-an-account warning reached the first client only: published (`RunningAsAccount`).
  - e2e: a sleep-synchronised race test, a leak check blind to non-holders, unresolved `/proc` paths; loomdev had no
    way past a daemon that won't stop (`down --force`) and two untested paths.
- **The smoke run** (9 scenarios, no regressions): quit and restart keep every session (same pane pids); two TUIs
  see each other's changes and keep their own selections; a SIGKILLed daemon ends each TUI cleanly and the next
  `loom` restores everything; an agent that exited just before the kill comes back Paused. Startup: cold (spawning
  the daemon) ~0.83s, warm ~0.33s, baseline ~0.53s. One 3B oddity: a theme changed in one TUI did not repaint the
  other (fixed with the final wave).
- **The final cross-cutting review:**
  - **Important:** `loom serve` decided nesting on its own environment, but pinned the last daemon's tmux server, so
    a dev loom with `LOOM_TMUX_SOCKET` set, started while the user's daemon was down, became the user's daemon on
    the user's server. Nesting is now decided on the server it pins, for `serve` and `reset`, failing closed.
  - **Important:** nothing made the global dir before the spawn (a first launch with a new `LOOM_GLOBAL_DIR`
    failed).
  - A replacing TUI could stop a daemon another had just started (`StopPID`); Nix builds, which name no commit and
    whose mtimes are all 1970, never replaced each other (the flake now stamps `rpc.commitUnix`, and a 1970
    `exe_time` decides nothing); `loom debug` and `loom reset` ignored the record's tmux server; GitHub watches
    never expired (`ghWatchTTL`).
  - The fix wave's own report found two more, fixed by the coordinator: the nesting guard failed open on a lookup
    timeout, and a second SIGTERM (two looms replacing the daemon together) force-exited a daemon mid-save.
  - **Its re-check** confirmed every fix (and `CompareBuilds` antisymmetric over 2.25M hello pairs) and found two
    Minor leftovers, both fixed: `StopPID` failed instead of answering "not running" for a dead daemon whose record
    outlived it under a probe, and an issue picker left open past `ghWatchTTL` emptied (it now renews its watch on
    every poll).

### Deviations from the plan

- The hello carries `exe_time` (and, for Nix, a stamped commit time) beside the executable's hash.
- `Core` gained `WatchGitHub` (a cast) and `RunningAsAccount` (a local query): 23 local, 9 casts, 23 requests.
- The nesting guard moved from the TUI to `loom serve` and `reset`, decided on the server they pin.
- `daemon.StopPID` beside `Stop`; `loomdev down --force`.
- A stopping daemon is force-exited only by Ctrl-C.

### Follow-ups

- **Live reconnect** (the user's choice: its own stage after 3B).
- **Windows in CI:** GoReleaser builds it and the matrix does not; 3B broke the Windows build once. Windows runtime
  is untested (it has no graceful stop).
- A build that names no commit (a plain `go build` from a tarball) at the same version keeps the running daemon:
  `loom serve stop` switches. The Nix stamp is unverified by a Nix build here.
- An open settings overlay keeps its copy when another TUI's change arrives.
- The nesting guard now fails closed: a tmux lookup timing out under load refuses to start the daemon (the error
  names `LOOM_ALLOW_NESTED=1`).
- "gh unavailable: gh pr list: exit status 1" for a repository with no GitHub remote (as before 3B).
- A TUI's quit waits ~2s per live pane client (`pump.wait_timeout`), as before.
- `loom debug` prints the TUI's Claude temp root, not the daemon's.
- Two global dirs registering one repository can each sweep the other's sessions there (from 3A).
- Agents inherit `ANTHROPIC_*` from the tmux server, which is the daemon's only when it started the server: the
  credential warning reflects the daemon's environment.
