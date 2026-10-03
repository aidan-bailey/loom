# Loom Daemon

**Date:** 2026-10-03
**Status:** Approved design, brainstormed with the user in sections.
**Origin:** the scrum workflow
([2026-10-03-scrum-workspaces-design.md](2026-10-03-scrum-workspaces-design.md))
needs sessions to be created, killed and nudged while no TUI is open. Every
alternative that kept the TUI as the owner of session lifecycle either
stalled the workflow when loom was closed or added a second writer of
`state.json`. So lifecycle moves into a daemon, and the TUI becomes a
client. This spec is that split, on its own: it changes nothing a user
sees who never closes loom.

## Problem

Today the TUI process owns everything: it loads the open workspaces,
reconciles `state.json` against tmux and disk, sweeps orphans, runs the
health tick that relaunches dead agents, polls the Claude roster, GitHub
and the accounts registry, and is the only writer of `state.json`. When
the TUI exits, all of that stops. Sessions survive, because they are tmux
sessions, but nothing watches them.

The scrum workflow asks an agent (the Scrum Master) to create and stand
down sessions and to be told when a pull request merges, with or without a
TUI. Letting the agent CLI do that directly means two processes writing
`state.json` and two processes deciding which tmux sessions are orphans,
which is the class of bug the orphan-sweep gotcha in CLAUDE.md records.

## Goals

1. Session lifecycle runs whether or not a TUI is open.
2. Exactly one process owns `state.json` and session lifecycle per global
   config dir.
3. The TUI's behaviour is unchanged for a user who never closes it.
4. Several clients (TUIs, the agent CLI, subcommands) can act on the same
   state at once, serialized.

Non-goals: remote access (the socket is local), a network transport,
authentication beyond filesystem permissions, and surviving a reboot
without a client starting the daemon.

## Decisions

| # | Decision |
|---|---|
| 1 | **Full split.** `loom serve` owns every workspace; the TUI, the agent CLI and the subcommands are clients. A daemon only for scrum workspaces was considered and rejected: it adds a second ownership model inside the TUI and still has to grow into this. |
| 2 | **On demand, like tmux's server.** The first client that finds no daemon spawns one. It keeps running after every client leaves and exits only on `loom serve stop` or a signal. |
| 3 | **Every registered workspace, always loaded,** plus the global context. Open tabs become a per-client view preference. |
| 4 | **The boundary is a Go interface,** `core.Core`, with an in-process and a socket-backed implementation. Clients never call `session` lifecycle methods. |
| 5 | **Transport:** newline-delimited JSON over a unix socket, request/reply with ids plus server-pushed events. No gRPC. |
| 6 | **Status comes from roster and hooks,** which need no pane. The TUI keeps its content-scrape ladder as a display-only fallback and sends the daemon nothing from the pane. |
| 7 | **Version mismatch: the newer side wins.** A newer client asks the daemon to exit and spawns its own build; a newer daemon tells an older client to upgrade. |
| 8 | **Strangler migration** in four shippable stages. The model loop moves out of the TUI first, in-process; the socket is the last step. |

Rejected:

- **The agent CLI creating sessions itself**, with the TUI adopting them
  by re-reading `state.json`. Two writers; the reconcile code is the most
  delicate in loom.
- **A lock-file hand-off** where the TUI acts when running and the CLI
  acts when it doesn't. Two code paths for one action, and no GitHub
  polling while the TUI is closed.
- **Agents polling GitHub themselves** with scheduled wake-ups. Works
  without a daemon, but every idle agent spends turns polling, and the
  cascade still needed a TUI to spawn sessions.
- **A daemon only for scrum workspaces.** See decision 1.
- **Explicit start only** (`loom serve` by hand or systemd). A reboot
  would silently stall a sprint. A systemd unit may come later as an
  add-on to on-demand start.

## Design

### 1. Processes

Three processes share one binary.

**`loom serve`, the daemon.** One per global config dir. It listens on a
unix socket under `$XDG_RUNTIME_DIR/loom/` (falling back to
`<globalDir>/run/`), named `<hash>.sock` where `<hash>` is a short hash of
the resolved global dir, so a `loomdev` sandbox with its own
`LOOM_GLOBAL_DIR` gets its own daemon, as `LOOM_TMUX_SOCKET` gives it its
own tmux server. Beside the socket it holds an exclusive `flock` on
`<hash>.lock` for its lifetime. The kernel releases the lock when the
process dies, so two daemons can never own one config dir, and a stale
socket is told from a live one by whether the lock is free.

At start it loads every registered workspace and the global context, and
for each runs what the TUI runs today on activation: `LoadAndReconcile`,
`reconcileOrphans`, the orphan tmux sweep and the hooks sweep. Then it
runs the health tick and every gated job: roster queries, hook scans, the
GitHub poll and parity, accounts reload and refresh, usage probes. It is
the only process that writes `state.json` and the only one that creates,
pauses, resumes, kills, recovers or relaunches a session. It daemonizes
(a new session via `setsid`, stdio to `/dev/null`, logs to
`logs/serve.log`) and exits only on `loom serve stop`, a mismatch request
(§6) or a signal.

**`loom`, the TUI, a client.** It dials the socket. If nothing answers
and the lock is free, it spawns `loom serve` detached and retries for a
bounded time. It renders, takes keys, runs the Lua engine and the
overlays, and attaches preview PTYs straight to tmux as today. Every
lifecycle key becomes a request. Its open tabs are a view preference the
daemon stores in the registry but does not act on.

**Subcommands, clients.** `loom work …` (the agent CLI), `loom reset`,
`loom workspace …` and `loom account …` send requests instead of loading
`state.json` or the registries themselves. `loom version` and
`loom debug` stay direct; they read files only.

### 2. The `Core` interface

`core.Core` is the one boundary. It has two implementations:

- **in-process:** the daemon's own model, also what tests and the first
  migration stage use;
- **socket-backed:** what a client holds.

Clients hold a `Core` and nothing else from the lifecycle layer. The
`session`, `session/git` and `session/tmux` packages are unchanged
underneath; they gain one caller instead of several.

**Requests.** A request is a JSON object with `id`, `method` and
`params`; it gets exactly one reply with the same `id` and either
`result` or `error`. Grouped:

- *Workspaces:* list, add, remove, rename, use, set mode and account
  (for the scrum spec), set the open tabs. The open list stays the one
  registry value it is today, written by the daemon on request; the
  daemon never acts on it.
- *Instances:* list (with runtime status, work state, diff stats, GitHub
  state, parity, subagent rows), new (title, prompt, issue, launch
  options), start, pause, resume (with options), recover, discard, kill,
  push, merge, set split ratio, send prompt.
- *Accounts:* list with auth and usage, add, remove, sync, and
  login-prepare, which returns the command the client runs itself,
  since `claude auth login` needs the terminal.
- *Work log:* append, read board, read entries since an offset (the
  scrum spec).
- *Daemon:* version, stop, reload registry.

A long operation (kill, resume, recover) replies when it finishes, as
the TUI's completion messages land today. The reply names the instance by
**title plus workspace path**, the identity `reopenedTwin` already
matches on, so the client applies the result to what it asked about, not
to its selection.

**Events** are frames with no `id`, pushed to every subscribed client:
instance added, removed and changed (status, diff stats, GitHub state,
parity, wait reason, work state), work log appended, registry changed,
accounts changed, and notice (the text `handleError` shows today, with a
severity). A client that reconnects replaces its lists wholesale from a
fresh `list`.

**`InstanceView`** is the value type the daemon publishes: title,
workspace path, worktree path, branch, tmux session name, runtime status
and its age, wait reason, diff stats, GitHub state, parity, issue,
account, program, and the flags the TUI gates keys on (workspace
terminal, started, paused, recoverable, busy). It carries no methods that
act. Panes attach by the tmux session name.

### 3. What moves, what stays

New packages: `core/` (the model) and `core/rpc/` (codec, server,
client).

**The model is one goroutine** owning a loop over an inbox of requests,
timer ticks and job results. Jobs run in goroutines and deliver results
as messages; nothing else touches the model. This is the Bubble Tea
Update rule moved out of the TUI, so every identity and stamping rule in
CLAUDE.md holds unchanged: only the sender changes, from
`tea.Program.Send` to the daemon's inbox.

**Moves into `core/`** (today in `app/`): the workspace slots and
`loadSlot`'s lifecycle half, storage and reconcile driving,
`reconcileOrphans`, the tmux and hooks sweeps, the health tick with
`applyLiveness`, crash restart and the workspace-terminal auto-restart,
`pollGate` and every gated job (roster, hook scan, GitHub poll and
parity, accounts reload and refresh, usage probe), the completion
handlers in `completions.go`, `killActionFor`, `pauseActionFor`,
`mergeActionFor`, `pushActionFor`, the issue-picker fetches, and (from
the scrum spec) the work log.

**Stays in `app/` and `ui/`:** the Bubble Tea model, `viewMode`, key
routing and the Lua engine (script intents call `Core`), overlays, the
preview and terminal panes with their PTY pumps and emulators,
`ScrollModel`, mouse, the workbench, review, the split-ratio throttle
(flushed to the daemon), help and settings screens. A TUI slot shrinks
to view state: list widget, panes, workbench, cursor. Its
`[]*session.Instance` becomes `[]core.InstanceView`.

**Behaviours that change hands:**

- A TUI quitting saves nothing and pauses nothing; the daemon has it.
  `s` still pauses, as a request.
- The nesting guard moves to `loom serve`: a daemon started inside a
  `loom_*` tmux session without its own socket refuses. A TUI inside one
  is fine; it no longer sweeps.
- `loom reset` becomes a request, so its sweep runs against loaded state.
- Status bar warnings and notices become `notice` events the TUI shows.
- The TUI's pane-driven status ladder (`statusDetectedMsg`,
  `maybeRedetect`) stays, but as a display overlay used only when the
  daemon's view has no opinion for that instance. It never reaches the
  daemon.

### 4. The protocol

Newline-delimited JSON over the socket, UTF-8, one frame per line.

```json
{"id":7,"method":"instance.kill","params":{"workspace":"/tb/Source/x","title":"api"}}
{"id":7,"result":{"notice":"stash forgotten: …"}}
{"event":"instance.changed","data":{…InstanceView…}}
```

- The **first frame** from each side is a `hello` carrying `protocol`
  (an integer, bumped on any incompatible change) and `build` (the
  binary's version and commit). The daemon answers `hello` or
  `mismatch` (§6).
- Errors carry a `code` (`not_found`, `refused`, `busy`, `storage`,
  `mismatch`, `internal`) and a message meant for the user, since the TUI
  shows it verbatim and the agent CLI prints it to stderr.
- A client **subscribes** once; the daemon then pushes events until the
  connection closes. Events are not acknowledged. A slow client's buffer
  is bounded; when it overflows the daemon drops that client's
  subscription and sends one `resync` event, and the client re-lists.
- The codec runs over any `io.ReadWriter`. Tests use `net.Pipe`.

### 5. Discovery and start

A client resolves the global dir (`LOOM_GLOBAL_DIR` or the default),
derives the socket and lock paths, and dials. On a dial failure it tries
the lock without blocking:

- **lock free:** no daemon. It removes a stale socket file, spawns
  `loom serve` from `os.Executable()` with the same environment, detached
  (new session, stdio closed), releases the lock, and redials with a
  backoff for up to 5s. A second failure is an error naming
  `serve.log`.
- **lock held:** a daemon is starting. It redials with the same backoff.

The daemon takes the lock before anything else, writes its pid into the
lock file for `loom debug`, binds the socket with mode `0600`, and only
then loads state.

### 6. Version handshake

Both `hello` frames carry `protocol` and `build`. The rule is **the
newer side wins**, so a `loomdev` TUI or a freshly installed loom never
runs against a stale daemon:

- Client newer: the daemon answers `mismatch` with its build. The client
  sends `daemon.stop`; the daemon finishes in-flight jobs, saves and
  exits; the client spawns its own build and reconnects. Sessions are
  tmux and survive.
- Daemon newer: the client reports "daemon is build X, this loom is
  build Y: upgrade or run `loom serve stop`" and exits.
- Same build: no check at all. "Newer" is by version (`main.go`), then
  by the commit time Go stamps into the binary (`vcs.time` from
  `debug.ReadBuildInfo`, no ldflags needed); two dev builds with the
  same version compare by commit time, and a `vcs.modified` build counts
  as newer than a clean one at the same commit.

### 7. Failure handling

| Situation | Behaviour |
|---|---|
| No daemon on the socket, lock free | The client spawns one (§5). The spawn is one attempt; a second failure is an error naming `serve.log`. |
| Socket present, lock free (daemon crashed) | Treated as no daemon: stale socket removed under the lock, new daemon started. Nothing is lost: `state.json` and the work log are on disk, and the new daemon reconciles as startup does today, relaunching dead sessions. |
| Daemon dies while a TUI is open | The connection drops. The TUI shows a banner, keeps rendering its panes (the PTYs are to tmux), disables lifecycle keys, redials on a backoff, and respawns if the lock is free. On reconnect it resubscribes and re-lists. |
| Daemon dies mid-operation | The operation's state is whatever `session` left on disk, as after a TUI crash today. The next start's reconcile classifies it (`Paused` with an intact tree, orphan, and so on). |
| Two clients act on one instance | Serialized by the model loop; the second gets a reply reflecting the first ("already killed"). |
| Version mismatch | §6. |
| A nested dev loom | `loomdev` sets `LOOM_GLOBAL_DIR`, so the sandbox's daemon owns its own socket, lock, registry and tmux server. The nesting guard refuses a daemon that would share a tmux server with its host. |
| A workspace fails to load (latched storage) | As today: marked failed, its titles unknown so the sweep skips it, writes refused. Reported as a `notice` and shown in the picker. |
| A request names a workspace the daemon hasn't loaded | `not_found`: the registry changed. Clients re-list on `registry changed`. |
| A slow client | Its subscription is dropped with one `resync` event (§4). |
| Reboot | No daemon until the first `loom` start or agent CLI call. A sprint stalls until then. A systemd user unit is a possible later add-on. |
| `loom serve stop` with sessions running | The daemon saves and exits. Sessions keep running; the next daemon reattaches them. |

Logs: the daemon writes `logs/serve.log`, the TUI `logs/loom.log`,
same rotation, same structured format, `subsystem=rpc` on protocol lines.

## Testing

- **`core`** inherits the `app` lifecycle tests that move with the code:
  completions by identity and owning slot, reconcile on activation,
  sweeps, the health tick, crash restart, gated jobs. They stay on the
  mock tmux session and the private tmux socket from `TestMain`.
- **`core/rpc`:** golden-frame tests for every method and event; round
  trips over `net.Pipe`; error codes; the slow-client drop and `resync`;
  the handshake in all three outcomes.
- **Daemon end-to-end** (`e2e`, on the private tmux socket and a
  throwaway global dir): spawn on demand, stale socket, daemon killed
  under an open TUI and reconnected, two TUIs on one daemon seeing each
  other's kills, mismatch with a fake newer build, `loom serve stop` with
  live sessions reattached by the next daemon.
- **The TUI** keeps its tests against an in-process `Core` over
  `net.Pipe`, so no test needs a daemon process.
- `loomdev` grows `serve` awareness: `up` starts the sandbox daemon,
  `down` stops it.
- `CC=clang CGO_ENABLED=1 go test -race ./...`.

`InstanceData` doesn't change; there is no schema bump. `state.json` and
the registries keep their formats, so a daemon and the current TUI read
the same files.

## Documentation

- **CLAUDE.md:** the Architecture section gains `core/` and `core/rpc/`,
  and the gotchas describing TUI ownership are rewritten: the focused
  slot, destructive-actions-by-identity, the orphan sweep and nesting
  guard, the no-model-mutation rule (now the model loop's rule), the
  pollGate. A new gotcha: the daemon is the only writer of `state.json`,
  and every subcommand goes through `Core`.
- **USAGE.md:** `loom serve`, `loom serve stop`, the socket and lock
  paths, `serve.log`, and what happens on a mismatch.
- **`docs/specs/`** gains a protocol reference generated from the
  golden frames.

## Rollout

A strangler, four stages, each shippable with the TUI working end to
end:

1. **Extract the model.** Create `core` with `Core`, `InstanceView` and
   the event types. Move the slots, storage, reconcile, sweeps, health
   tick, gated jobs and completions out of `home` into `core.Model`, a
   loop on its own goroutine inside the TUI process, fed by the same
   messages it gets today. `home` holds a `Core` backed by that loop over
   channels. No socket, no behaviour change.
2. **Codec and transport.** `core/rpc`. The TUI uses the socket client
   against an in-process server over `net.Pipe`.
3. **The daemon process.** `loom serve`, the lock, spawn on demand, the
   handshake, reconnect and respawn, `serve.log`, the nesting guard move.
   The subcommands become clients. `loomdev` runs a sandbox daemon.
4. **Cleanup.** Delete the TUI's dead lifecycle code, rewrite the
   CLAUDE.md gotchas, update USAGE.md, bump the version.

The scrum workflow starts after stage 3.

## Out of scope

- A network transport or remote clients.
- Authentication beyond the socket's file mode.
- A systemd unit (possible add-on).
- Moving pane rendering into the daemon. Panes attach to tmux directly.
- Windows: no unix sockets in this design, as there are no hooks or
  `flock` there already.

## Assumptions

### Verified against the code on 2026-10-03

- `app` lifecycle code already follows a message-passing rule (Cmd
  goroutines deliver results as messages; the Update goroutine mutates),
  so moving it behind a loop changes the sender, not the rules.
- Completions already act by identity (`reopenedTwin` matches on title
  and worktree path), so a reply naming title and workspace path is
  enough for a client to apply it.
- Preview panes attach to tmux by session name through `TmuxSession`,
  not through anything the TUI's lifecycle state owns, so they survive a
  daemon restart.
- `loomdev` already isolates a sandbox with `LOOM_GLOBAL_DIR` and
  `LOOM_TMUX_SOCKET`; a per-global-dir socket slots into that.

### Still to verify

| # | Assumption | How |
|---|---|---|
| 1 | A detached child started from a TUI process (new session, stdio closed) keeps running after the TUI exits and after its tmux pane closes. | Stage 3's end-to-end test. |
| 2 | `$XDG_RUNTIME_DIR` is set in the environments loom runs in (a tmux pane under a systemd user session; a plain ssh login may lack it). | Stage 3 falls back to `<globalDir>/run/`; the test covers both. |
