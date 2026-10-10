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
| 3 | **Every registered workspace, always loaded,** plus the global context. Open tabs become a per-client view preference. (Amended 2026-10-08 by stage 3A: this now holds, in process. The model loads the global workspace and every registered one when it boots and never drops one, and each client keeps its own tabs, the workspace it shows while none is open, and its selection.) |
| 4 | **The boundary is a Go interface,** `core.Core`, with an in-process and a socket-backed implementation. Clients never call `session` lifecycle methods. |
| 5 | **Transport:** newline-delimited JSON over a unix socket, request/reply with ids plus server-pushed events. No gRPC. |
| 6 | **Status comes from roster and hooks,** which need no pane. The TUI keeps its content-scrape ladder as a display-only fallback and sends the daemon nothing from the pane. |
| 7 | **Version mismatch: the newer side wins.** A newer client asks the daemon to exit and spawns its own build; a newer daemon tells an older client to upgrade. (Amended 2026-10-09 by stage 3B: the client stops the daemon with a signal, not a request, and compares builds itself; see §6.) |
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

(Amended 2026-10-09 by stage 3B, as built in `internal/daemon`: the lock
is `<globalDir>/loom.lock`, not beside the socket, since a lock under
`$XDG_RUNTIME_DIR` would let a client without that variable start a
second daemon; it is also the file the retired takeover lock used, so a
loom from before the daemon and a daemon exclude each other. The lock
file carries the holder's record (pid, tty, start time, socket, build,
host, tmux server; on Windows, whose lock is mandatory, in
`loom.lock.json` beside it), and clients dial the socket the record
names rather than derive it, since their environment may differ from
the daemon's. The socket is `$XDG_RUNTIME_DIR/loom/<hash>.sock`, else
`<globalDir>/run/serve.sock` when that path fits a socket address
(100 bytes), else `loom-<uid>/<hash>.sock` in the temp dir, each in a
0700 directory the user owns, the socket 0600. A daemon that finds its
socket file gone or replaced (a runtime dir removed at logout) listens
again wherever there is room and rewrites the record.)

At start it loads every registered workspace and the global context, and
for each runs what the TUI runs today on activation: `LoadAndReconcile`,
`reconcileOrphans`, the orphan tmux sweep and the hooks sweep. Then it
runs the health tick and every gated job: roster queries, hook scans, the
GitHub poll and parity, accounts reload and refresh, usage probes. It is
the only process that writes `state.json` and the only one that creates,
pauses, resumes, kills, recovers or relaunches a session. It daemonizes
(a new session via `setsid`, stdio to `/dev/null`, logs to
`logs/serve.log`) and exits only on `loom serve stop`, a mismatch request
(§6) or a signal. (Amended 2026-10-09 by stage 3B: it exits on SIGTERM,
SIGINT or SIGHUP, which is what `loom serve stop` and a newer client
send; the first signal stops it gracefully (§6); a Ctrl-C meanwhile exits
at once, and another SIGTERM is only logged, since two clients replacing
it together each send one. It also exits, without saving, when its model fails; the next
client starts a fresh one. A runtime crash it can't log goes to
`logs/serve-crash.log`. It starts in the global dir, never the spawning
client's directory.)

**`loom`, the TUI, a client.** It dials the socket. If nothing answers
and the lock is free, it spawns `loom serve` detached and retries for a
bounded time. It renders, takes keys, runs the Lua engine and the
overlays, and attaches preview PTYs straight to tmux as today. Every
lifecycle key becomes a request. Its open tabs are a view preference the
daemon stores in the registry but does not act on. (Amended 2026-10-09 by
stage 3B: it targets the daemon's tmux server, which the daemon names in
its hello, whatever its own environment would pick. When the daemon goes
away under it, it restores the terminal, says the daemon stopped and that
the sessions keep running, and exits: the user's decision, with live
reconnect left to a stage after 3B.) (Amended 2026-10-10 by stage 3R,
as built: it survives the daemon going away. A banner names the state,
it keeps rendering and attaching panes, refuses what needs the model,
and rejoins a daemon when one answers, starting one itself only after a
crash (a loss with no bye); a newer daemon found on rejoining makes it
exit. See the 3R entry under Rollout.)

**Subcommands, clients.** `loom work …` (the agent CLI), `loom reset`,
`loom workspace …` and `loom account …` send requests instead of loading
`state.json` or the registries themselves. `loom version` and
`loom debug` stay direct; they read files only. (Amended 2026-10-09 by
stage 3B: the subcommands become clients in 3C. Until then `loom reset`
and `loom workspace migrate`, which write `state.json` themselves,
refuse while any process holds the lock, and `loom debug` reports the
daemon from its lock record.)

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
`result` or `error`. A method's wire name is its Go name in `core.Core`
(`Kill`), and its `params` are an object whose fields are the Go
parameter names (`{"id":3,"req":7}`). A result is `{"value":…}`, plus
`"ok"` for a method that also returns a bool (`{}` for one that returns
nothing), and an error travels in the reply. Both are generated from `core/iface.go`, so a terse parameter
name was renamed to make a readable wire key (`a` became `auth`, `n`
became `number`). (Amended 2026-10-08 by stage 2: named parameters
generated from the interface were the user's choice, in place of the
dotted names this section's grouping suggested.) Grouped:

- *Workspaces:* list, add, remove, rename, use, set mode and account
  (for the scrum spec), set the open tabs. The open list stays the one
  registry value it is today, written by the daemon on request; the
  daemon never acts on it. (Amended 2026-10-08 by stage 3A, which built
  this part: `Workspaces` lists every served workspace, `Open` is a
  client showing one (it retries a failed load and, on the workspace's
  first open, starts its terminal), `Register` returns the new
  workspace's view, and `PersistOpenList` writes the open list a client
  hands it. The tab methods (`OpenTab`, `CloseTab`, `EnterGlobal`, …)
  left `Core`: which workspaces a client shows is the client's.)
- *Instances:* list (with runtime status, work state, diff stats, GitHub
  state, parity, subagent rows), new (title, prompt, issue, launch
  options), start, pause, resume (with options), recover, discard, kill,
  push, merge, set split ratio, send prompt.
- *Accounts:* list with auth and usage, add, remove, sync, and
  login-prepare, which returns the command the client runs itself,
  since `claude auth login` needs the terminal.
- *Work log:* append, read board, read entries since an offset (the
  scrum spec).
- *Daemon:* version, stop, reload registry. (As built in 3B, the version
  is the hello's and stop is a signal, §6; `ReloadRegistry` is a request.)

A long operation (kill, resume, recover) replies when it finishes, as
the TUI's completion messages land today. The reply names the instance by
its **`InstanceID`**, which the model assigns the first time it reports
the instance and never reuses within a daemon's life, so the client
applies the result to what it asked about, not to its selection, and a
stale ID is refused rather than reaching an instance that took its
place. A reconnecting client re-lists, since a restarted daemon assigns
new IDs. Title and repository path are in the view, for display. (Amended
2026-10-07 by stage 1C, which replaced the planned title-plus-workspace-
path identity.) (Amended 2026-10-10 by stage 3R, as built: IDs are stable
across daemons, which replaces "never reuses within a daemon's life" and
"a restarted daemon assigns new IDs" above. A workspace's is a hash of
its canonical config dir, an instance's of its workspace's config dir,
its title and its `created_at`, masked to 53 bits (exact in any JSON
client) and never 0 (the draft row's); a collision probes to the next
free value. Two daemons over one disk agree, so a rejoining client
re-lists nothing, and an ID names one record: an instance recreated under
a killed one's title has a new `created_at`, so a new ID, and a request
by the old one is refused as `not_found`. A record rebuilt as a new
instance (a retried load, a recovery) keeps its ID, and an orphan
placeholder's `created_at` is read from its worktree directory's name,
not the time it was found, so every daemon derives the same one.)

**Events** are frames with no `id`, pushed to every subscribed client:
instance added, removed and changed (status, diff stats, GitHub state,
parity, wait reason, work state), work log appended, registry changed,
accounts changed, and notice (the text `handleError` shows today, with a
severity). A client that reconnects replaces its lists wholesale from a
fresh `list`. (Amended 2026-10-08 by stage 2: a reconnecting client
re-dials, and the snapshot a server sends every new connection
(`Loop.SyncAndSnapshot`) rebuilds its replica wholesale, with no `list`
call. The events are the model's `core.Event`s, named by Go type: the
state events (`WorkspacesChanged`, `ModelChanged`, `AccountsChanged`,
`GitHubChanged`, `ViewsChanged`) carry the published state, and the
rest include `Notice` and `Reply`.) (Amended 2026-10-08 by stage 3A:
not every event goes to every client. `Started`, `Recovered` and
`Notice` carry `Req`, the request whose job they report on (the model
stamps them while it applies that request's result, `Model.cause`, and
the jobs it spawns meanwhile inherit it, so a `Create`'s `Started` still
names the `Create`). Each client numbers its requests from 1, below
2^32; the server puts the connection's number in the high 32 bits of
every request ID it hands the model and takes it out on the way back, so
it routes without a table: a `Reply`, and a request's `Notice`, go to the
client that made the request alone; `Started` and `Recovered` go to every
client, since each attaches the session's pane, naming the request only
to the one that made it, which alone selects the row and attaches
inline. Events naming no request go to every client. See the "Request
IDs" section of `docs/specs/protocol.md`.)

**`InstanceView`** is the value type the daemon publishes, as built in
stage 1C (`core/view.go`): `ID`; `Title`; `RepoPath` (the repository the
session works in); `WorktreePath` and `WorktreeRepoPath` (the repository
root git resolved for the worktree, which Lua's worktree handle is built
on); `Branch`; `TmuxSession` and `SessionProgram` (the session's name and
the program it was launched with, which picks a pane client's adapter);
`Program`; `Status` and `StatusSince`; `StatusReported` (whether Claude's
hooks or roster have an opinion on the status); `WaitReason`;
`LastMessage`/`HasLastMessage`; `Subagents`; `Diff`/`HasDiff`; `GitHub`;
`Ahead`, `Behind` and `ParityKnown`; `Issue`; `Account`; `HeadroomProxy`
and `CacheTTL1h`; `IsWorkspaceTerminal`; `Started`; and `Bell`, which only
the TUI fills (its bell overlay). Paused, busy and recoverable are read
from `Status` (`Paused()`, `Active()`). It carries no methods that act.
Panes attach by the tmux session name.

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
  is fine; it no longer sweeps. (Amended 2026-10-09 by stage 3B, as
  built: `loom reset` keeps the guard too, and a daemon spawned on demand
  logs its refusal to `serve.log`, which the client quotes. A TUI inside
  a loom session joins its global dir's daemon, but never replaces one
  that is older: a loom built in a worktree and run in an agent's pane
  would otherwise stop the user's daemon. That refusal has its own check
  (a `loom_*`/`claudesquad_*` session on the daemon's tmux server), which
  `LOOM_TMUX_SOCKET` and `LOOM_ALLOW_NESTED` do not bypass. The nesting
  guard itself is decided on the tmux server the daemon, or `reset`, is
  about to pin, the last daemon's while it runs, not on "its own socket":
  a private socket the pin ignores no longer waves a daemon through, and
  `reset` sweeps that server.)
- `loom reset` becomes a request, so its sweep runs against loaded state.
  (Stage 3C; until then it refuses while the daemon runs.)
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

As built in stage 2 (`core/rpc.Frame`), each frame has exactly one of
these shapes. The full reference, with every method's kind, parameters
and result and every event, is
[`docs/specs/protocol.md`](../../specs/protocol.md), generated by
`go test ./core/rpc -run TestProtocolReference -update`:

```json
{"hello":{"protocol":1,"build":"v0.x.y <commit>"}}
{"id":7,"method":"Kill","params":{"id":3,"req":12}}
{"id":7,"result":{}}
{"method":"PaneQuiet","params":{"id":3}}
{"event":"ViewsChanged","data":{"WS":1,"Views":[…InstanceView…]}}
{"fatal":{"code":"panic","message":"…"}}
```

- A **request** has a non-zero `id`; its **reply** carries the same `id`,
  a `result` and, on failure, an `error`. A **cast** has no `id` and gets
  no reply. An **event** is named by its Go type (`core.EventTypes()`).
  A **fatal** frame says the model is gone (it panicked, or an event
  would not encode), and the client re-raises it on every later call.
  (Amended 2026-10-09 by stage 3B: a daemon's client panics nowhere; it
  reports the loss, as it does a lost connection, and the TUI exits
  cleanly (§1). An in-process client still re-raises it. The daemon exits
  on a fatal error. A notice that reaches no client, raised while none is
  connected or a request's whose client has gone with no other connected,
  is kept for the next connection, the newest 50 at most.)
  (Amended 2026-10-10 by stage 3R, as built: a daemon stopping gracefully
  sends every connection a `bye` frame first, `{"bye":"stopping"}`, and
  from then on takes nothing new: a request is answered with a new error
  code, `unavailable` (a refusal, `core.ErrUnavailable`), without reaching
  the model, and a cast is dropped. The lifecycle jobs in flight finish
  and publish their replies to the connections still open, the workspaces
  are saved, and only then does the server publish once more, flush each
  connection and close it. A client that met a `bye` before the
  connection closed knows the loss was graceful (its error matches
  `core.ErrUnavailable`); one that did not, or that met a `fatal`, takes
  it for a crash. A model that failed gets no `bye`. `bye` is an optional
  field and `unavailable` a new code, so `Protocol` stays 2: an older
  client ignores the frame and sees only the error's message.)
- The server publishes after every request and before queueing its
  reply, so a request's events reach the client ahead of its reply.
  Casts publish nothing. A client is sent the whole published state
  (`Snapshot`) right after the hello, and every change after, so it keeps
  a replica and answers queries itself.
- Adding a method, an event or an optional field is compatible: a peer
  answers a method it lacks with `unsupported`, ignores a field it does
  not know and drops an event it does not know. Removing or renaming a
  method, event or field, or changing what a field means or a frame's
  shape, needs a `protocol` bump.

- The **first frame** from each side is a `hello` carrying `protocol`
  (an integer, bumped on any incompatible change) and `build` (the
  binary's version and commit). The daemon answers `hello` or
  `mismatch` (§6). (Amended 2026-10-09 by stage 3B: the hello also
  carries optional fields to compare builds by, `version`, `time`,
  `modified`, `exe` (the executable's hash) and `exe_time`, and, from a
  server, `tmux`, the tmux server its sessions run on. The server sends
  its hello first, even to a client of another protocol, before answering
  it `mismatch`, so every client learns the server's build. A peer that
  says no hello within 10s is dropped. `protocol` stays 2: every addition
  is optional.)
- Errors carry a `code` (`not_found`, `refused`, `busy`, `storage`,
  `mismatch`, `internal`) and a message meant for the user, since the TUI
  shows it verbatim and the agent CLI prints it to stderr. (As built in
  stage 2, `core.WireError`: `not_found`, `refused`, `storage`, `error`,
  `panic`, `protocol`, `mismatch` and `unsupported`. A busy session's
  refusal is `refused`, and `internal` is `error`. `unsupported` answers
  a method the server does not know, as an ordinary error, so a newer
  client can probe an older server; `panic` and `protocol` are fatal.)
- A client **subscribes** once; the daemon then pushes events until the
  connection closes. Events are not acknowledged. A slow client's buffer
  is bounded; when it overflows the daemon drops that client's
  subscription and sends one `resync` event, and the client re-lists.
  (Amended 2026-10-08 by stage 2, where the hello subscribes: each
  connection's outbound queue **coalesces** instead of dropping and
  resyncing. A state event (`WorkspacesChanged`, `ModelChanged`,
  `AccountsChanged`, `GitHubChanged`, and each workspace's
  `ViewsChanged`) replaces a queued one of the same kind in place, so a slow client queues at most one of
  each, and the client's `Sync` hands out the newest state once per kind.
  Replies and the other events are never dropped: a resync could rebuild
  the state, but not a lost `Reply`, and a script awaiting one would hang.
  The non-state queue is unbounded; replies are bounded by the requests
  in flight.)
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

(Amended 2026-10-09 by stage 3B, as built in `internal/daemon`. The
client derives nothing: it reads the lock's record (§1). A record with a
socket is a daemon that listens, and the client dials it. One with a
build but no socket is a daemon still booting, which the client waits
for, past its 20s timeout up to 3 minutes while that daemon lives, since
a boot relaunches every dead agent before it listens. A record of a loom
from before the daemon is refused while its pid lives. With no holder the
client spawns `loom serve` detached (a new session, stdio to
`/dev/null`, the global dir as its working directory, the client's
environment) and redials with a backoff; the spawner takes no lock, so
two clients starting at once may spawn two daemons, and the second
stands down at once on finding a live daemon's lock (it waits only for a
lock no live loom holds), and both clients dial the first. A daemon that
exits before it listens fails the connect at once, quoting what it wrote
to `serve.log` and `serve-crash.log`; one that stood down for a daemon
gone since is followed by another spawn, three at most. The daemon takes
the lock, finds the socket's place, boots, and only then listens and
writes the socket into its record: a socket that answers is a daemon
that is ready, and the stale socket file a killed daemon left is replaced
when the next one listens. After 750ms of waiting the client says it is
waiting for the daemon to load its workspaces.)

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

(Amended 2026-10-09 by stage 3B, as built. **Stopping is a signal**, not
a `daemon.stop` request: the client sends SIGTERM to the pid in the
lock's record, after checking the record's host and that the pid's
arguments say `serve`, so a newer client can stop a daemon of any
protocol. The daemon then stops accepting, waits up to 30s for the
lifecycle jobs in flight (a pause mid-stash), saves every workspace and
exits; the client waits for the process to go, then connects again,
which spawns its own build. The server's hello comes first, even on a
protocol mismatch, so the client always knows the daemon's build.
**Build identity** (`rpc.CompareBuilds`): the same executable (its
SHA-256, `exe`) is the same build; otherwise the higher version, then
the later commit time, then a modified tree over a clean one, then the
higher protocol (a daemon that sent no hello is older), then, only when
both name their commit time, the newer executable (`exe_time`), so a dev
build rebuilt at one commit replaces its sandbox's daemon. A field
either side lacks decides nothing, nor does an `exe_time` at or before
1970-01-01T00:00:01Z (a Nix store's), and a full tie is the same build:
two installs of one release keep whichever daemon runs. A Nix build has
no VCS stamp, so the flake stamps its commit time (`self.lastModified`)
at link time; a build that names no commit at all (a plain `go build`
from a tarball) keeps a daemon of its release, and `loom serve stop`
switches. A replacing client stops only the daemon it compared, by its
pid, never one another client started meanwhile. A client replaces a
daemon once per start, announcing it on stderr, and **never from inside
loom**: from a loom tmux session on the daemon's own tmux server it
refuses, and says to run the new loom outside loom or stop the daemon
first (§3). Windows has no graceful stop, and says so. A TUI of the
older build still open on a replaced daemon exits cleanly (§1).)

### 7. Failure handling

| Situation | Behaviour |
|---|---|
| No daemon on the socket, lock free | The client spawns one (§5). The spawn is one attempt; a second failure is an error naming `serve.log`. (Amended 2026-10-09 by stage 3B: a daemon that exits before listening fails the connect at once, quoting `serve.log` and `serve-crash.log`; one that stood down for a daemon gone since is followed by another spawn, three at most.) |
| Socket present, lock free (daemon crashed) | Treated as no daemon: stale socket removed under the lock, new daemon started. (As built in 3B, the new daemon replaces the stale file when it listens, holding the lock.) Nothing is lost: `state.json` and the work log are on disk, and the new daemon reconciles as startup does today, relaunching dead sessions. |
| Daemon dies while a TUI is open | The connection drops. The TUI shows a banner, keeps rendering its panes (the PTYs are to tmux), disables lifecycle keys, redials on a backoff, and respawns if the lock is free. On reconnect it resubscribes and re-lists. (Amended 2026-10-09 by stage 3B, the user's decision: for now the TUI restores the terminal, prints "loom: the daemon stopped (see <serve.log>); your sessions keep running. Run loom again." and exits, and the next `loom` starts a daemon. Live reconnect is a stage after 3B.) (Amended 2026-10-10 by stage 3R, as built: the TUI stays up under a banner. A daemon stopping gracefully says bye first and the TUI waits for a daemon, polling once a second and never spawning one on its own (`ctrl+r` starts one); a loss with no bye (a crash, a model failure) is redialled on a backoff from 1s to 30s, spawning a daemon when none runs, until three of them in a row exit before serving, after which it waits too. Meanwhile panes still render and attach, and what needs the model is refused at the key and, as a backstop, by the client. On rejoin the newer build wins as at startup: a newer daemon makes the TUI exit ("loom: the loom daemon was replaced by a newer loom (X); run loom again"), an older one is replaced once. See the 3R entry under Rollout.) |
| Daemon dies mid-operation | The operation's state is whatever `session` left on disk, as after a TUI crash today. The next start's reconcile classifies it (`Paused` with an intact tree, orphan, and so on). |
| Two clients act on one instance | Serialized by the model loop; the second gets a reply reflecting the first ("already killed"). |
| Version mismatch | §6. |
| A nested dev loom | `loomdev` sets `LOOM_GLOBAL_DIR`, so the sandbox's daemon owns its own socket, lock, registry and tmux server. The nesting guard refuses a daemon that would share a tmux server with its host. (Amended 2026-10-09 by stage 3B: and a loom inside a loom session never replaces the host's daemon, whatever its build (§6).) |
| A workspace fails to load (latched storage) | As today: marked failed, its titles unknown so the sweep skips it, writes refused. Reported as a `notice` and shown in the picker. (Amended 2026-10-08 by stage 3A: the workspace stays served, empty and latched, its error published (`WorkspaceView.LoadErr`); the sweep leaves only its roots out of the owned set and still sweeps every other workspace; every `Open` rereads it from disk. It is not reported as a notice: the boot only logs it (`workspace.load_failed`), and the TUI reads its own failed opens (`failedOpen`), not `LoadErr`, so a failed workspace that isn't in the open list shows nowhere until a client opens it.) |
| A request names a workspace the daemon hasn't loaded | `not_found`: the registry changed. Clients re-list on `registry changed`. |
| A slow client | Its queue coalesces state events, so it holds at most one of each kind; replies and other events are never dropped (§4, amended by stage 2). |
| Reboot | No daemon until the first `loom` start or agent CLI call. A sprint stalls until then. A systemd user unit is a possible later add-on. |
| `loom serve stop` with sessions running | The daemon saves and exits. Sessions keep running; the next daemon reattaches them. (As built in 3B, it first waits up to 30s for lifecycle jobs in flight. As built in 3R, it says bye first and keeps the connections open until those jobs' replies are sent and the workspaces saved; every open TUI then waits for a daemon, and `ctrl+r` starts one.) |
| The daemon is started from another environment (an SSH login, another tmux server) | (Added 2026-10-09 by stage 3B.) It keeps the last daemon's tmux server (named in the lock's record) while that server runs, so it finds the agents where they run rather than relaunch each on another server. Its environment, frozen when it was spawned, is what the model reads and what the agents' `update-environment` variables come from; a TUI started later from elsewhere changes none of it. |

Logs: the daemon writes `logs/serve.log`, the TUI `logs/loom.log`,
same rotation, same structured format, `subsystem=rpc` on protocol lines.
(Amended 2026-10-09 by stage 3B: `serve.log` is in the global dir and is
not rotated at startup, since a `loom serve` that loses the race for the
lock would rotate the live daemon's; it rotates as it grows.)

## Testing

- **`core`** inherits the `app` lifecycle tests that move with the code:
  completions by identity and owning slot, reconcile on activation,
  sweeps, the health tick, crash restart, gated jobs. They stay on the
  mock tmux session and the private tmux socket from `TestMain`.
- **`core/rpc`:** golden-frame tests for every method and event; round
  trips over `net.Pipe`; error codes; the slow-client coalescing (stage 2
  replaced the drop and `resync`); the handshake in all three outcomes.
  Stage 2 added parity between the client's replica and the model's own
  answers, and a round trip of every field of every event.
- **Daemon end-to-end** (`e2e`, on the private tmux socket and a
  throwaway global dir): spawn on demand, stale socket, daemon killed
  under an open TUI and reconnected, two TUIs on one daemon seeing each
  other's kills, mismatch with a fake newer build, `loom serve stop` with
  live sessions reattached by the next daemon. (Amended 2026-10-09 by
  stage 3B, as built in `e2e/daemon_test.go`: the TUI under a killed
  daemon exits cleanly rather than reconnect; the suite also covers two
  TUIs starting at once, a newer build replacing the daemon under an
  open older TUI, an older build refusing, the daemon outliving its TUI's
  tmux session (Assumption 1), a same-version rebuild replacing the
  sandbox daemon, and a stop during a start race. Sessions come back on
  the same agent process, not paused.)
  (Amended 2026-10-10 by stage 3R, as built in `e2e/daemon_test.go`: a TUI
  under a killed daemon reconnects, keeping its selection and the agent's
  process (`TestE2E_Daemon_KilledUnderAnOpenTUIReconnects`); after
  `loom serve stop` it waits, no daemon starts for three seconds, a key
  that needs the model is refused, and `ctrl+r` brings a daemon back
  (`TestE2E_Daemon_ServeStopWaitsAndCtrlRStartsOne`), or the next loom's
  daemon reattaches the sessions (`TestE2E_Daemon_ServeStopKeepsTheSessions`);
  under a newer build's replacement an older TUI exits saying so
  (`TestE2E_Daemon_NewerBuildReplacesIt`); a pause in flight across a stop,
  held there by a git hook, finishes and reaches the TUI before the daemon
  goes (`TestE2E_Daemon_PauseAcrossAStop`).)
- **The TUI** keeps its tests against an in-process `Core` over
  `net.Pipe`, so no test needs a daemon process.
- `loomdev` grows `serve` awareness: `up` starts the sandbox daemon,
  `down` stops it. (As built in 3B: the sandboxed loom starts it, as any
  loom does; `stop` stops it after the TUI unless `--keep-daemon`,
  `start --restart` stops both first, `down` stops it before removing the
  sandbox, and `ls` and `logs` show it.)
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
  golden frames: `docs/specs/protocol.md`, since stage 2, written by
  `TestProtocolReference -update`.

## Rollout

A strangler, four stages, each shippable with the TUI working end to
end:

1. **Extract the model.** Create `core` with `Core`, `InstanceView` and
   the event types. Move the slots, storage, reconcile, sweeps, health
   tick, gated jobs and completions out of `home` into `core.Model`, a
   loop on its own goroutine inside the TUI process, fed by the same
   messages it gets today. `home` holds a `Core` backed by that loop over
   channels. No socket, no behaviour change. Through stage 1 the model
   loads the TUI's open tabs as it does today; loading every registered
   workspace starts with stage 3 (3A).

   Planned on 2026-10-03 as four plans, each leaving the TUI working end
   to end:

   - **1A, pane split**
     ([plan](../plans/2026-10-03-daemon-stage1a-pane-split.md)).
     `session.Instance` holds a lifecycle-only `tmux.Session` and never
     attaches. The TUI attaches one client per live agent session, by
     name, through `ui.PaneClients`. Prompts and trust-prompt answers
     go through `send-keys`.
   - **1B, model extraction**
     ([plan](../plans/2026-10-04-daemon-stage1b-core-model.md)).
     `core.Model` owns the loaded workspaces and their instances, loading
     and saving, reconcile and the sweeps, the workspace transitions, the
     lifecycle operations and their completions, the health tick's model
     half and every gated job. It is still called on the Update
     goroutine: blocking work runs as jobs whose results come back
     through `Deliver`, and the model reports through events (carrying
     `*session.Instance`) that the TUI drains after every message.
     `ui.List` reads its rows from the workspace.
   - **1C, the instance boundary**
     ([plan](../plans/2026-10-07-daemon-stage1c-instance-boundary.md)).
     The TUI and Lua see instances only as `InstanceView` values named
     by a model-assigned `InstanceID` and change them only by request
     (answered by a `Reply`; Lua's lifecycle calls yield until it lands),
     with draft rows for creation flows, the pane ladder and bells as
     display overlays, and `home.core` typed as `core.Core`.
   - **1D, the workspace boundary**
     ([plan](../plans/2026-10-07-daemon-stage1d-workspace-boundary.md)).
     Workspaces are named by a model-assigned `WorkspaceID` and seen as
     `WorkspaceView` values (published as `WorkspacesChanged`); config
     crosses as `config.Settings` copies, and settings saves, UI prefs and
     help screens are requests; the registry and the account names cross
     as copies, and every query hands out copies. `core.Core` is
     value-typed (`TestCoreIsValueTyped`), apart from the job plumbing
     (`Sync`'s jobs, `Deliver`).
   - **1E, the model's own goroutine**
     ([plan](../plans/2026-10-08-daemon-stage1e-model-loop.md)).
     `core.Loop` runs the model on its own goroutine and runs its own
     jobs, so `Deliver` and the jobs leave the boundary (`Sync` returns
     events only); it wakes the TUI (a coalesced message) when it has
     events to drain, and forwards a panic to the next caller so the TUI
     still restores the terminal. Amended 2026-10-08: the model also
     fires its own health tick from the loop's timer (the user's choice;
     the TUI keeps a tick for its own half and names its selection with
     `SetSelected`), and calls are serialized one at a time, not per
     Update. The TUI's calls stay synchronous round trips over the loop,
     so the read-after-write sites (`syncViews`, `syncWorkspaces`, the
     drains after a transition) keep their meaning.
2. **Codec and transport.** `core/rpc`. The TUI uses the socket client
   against an in-process server over `net.Pipe`. Its calls must stay
   synchronous round trips, or the read-after-write sites 1E kept
   (`syncViews`, `syncWorkspaces`, the drains after a transition, the
   nested drain in `newLaunchOptionsOverlay`) must be revisited; the
   loop's wake becomes a pushed event.

   Done as one plan
   ([plan](../plans/2026-10-08-daemon-stage2-wire.md)): `core/rpc`
   carries `core.Core` as newline-delimited JSON. A `Server` serves the
   model's `core.Loop`, and the TUI holds a `Client` over an in-process
   `net.Pipe` (`rpc.InProcess`). Two decisions were the user's
   (2026-10-08):

   - **A full replica.** The client keeps a replica of everything the
     model publishes, so every query is answered locally and only
     actions cross the wire. The model now publishes all of its state:
     the workspace and instance views, plus `ModelView`, `AccountsView`
     and `GitHubView`, with a `Snapshot` for a client that connects. The
     alternatives were synchronous round trips everywhere, or a replica
     of the instance and workspace views only.
   - **Named, generated parameters.** `core/rpc/internal/gen` generates
     each method's wire types, the method table, the server's dispatch
     and the client's methods from `core/iface.go`, whose line comments
     say how a client serves each method (`rpc:local`, `rpc:cast`,
     `rpc:client`, or a request). See §2.

   The read-after-write sites keep their meaning without a round trip
   per read: the server publishes a request's events before its reply,
   and a cast publishes nothing, so a cast must change no published
   state. §4 is amended: a slow client's queue coalesces state events
   instead of being dropped and resynced, since a resync can't rebuild a
   lost `Reply`. The client's wakes are the loop's, pushed as events. A
   panic in the model reaches the client as a `panic` reply and a
   `Fatal` frame, and the client re-raises it, so the TUI still restores
   the terminal. Left for later: the non-state queue is unbounded, and a
   `ViewsChanged` re-sends a whole workspace's views on any change, the
   selected session's full diff included (per-instance deltas would trim
   it).
3. **The daemon process.** `loom serve`, the lock, spawn on demand, the
   handshake, reconnect and respawn, `serve.log`, the nesting guard move.
   The subcommands become clients. `loomdev` runs a sandbox daemon.
   Reconnecting is a re-dial: a new connection is sent the whole
   published state, so the snapshot rebuilds the client's replica,
   a restarted daemon's new IDs included. The newer-side-wins handshake
   (§6) replaces stage 2's refusal of another protocol, and several
   clients need a selection each (`SetSelected` is one model-wide value
   in stage 2; 3A merges each connection's into a set).

   Split on 2026-10-08 into three plans, each leaving the TUI working end
   to end:

   - **3A, the multi-client model**
     ([plan](../plans/2026-10-08-daemon-stage3a-multi-client-model.md)).
     Still in one process, the model serves every registered workspace
     and several clients at once. It boots before any client connects
     (`Model.Boot`: the account registry, the remote-control detection,
     then the global workspace and every registered one, and one orphan
     sweep) and never drops a workspace. A workspace's first open
     (`Core.Open`) starts its terminal and its GitHub polling. A failed
     load is kept, latched, left out of the sweep's owned roots (every
     other workspace is still swept), and reread from disk on open.
     `Core` loses the tab and startup methods (`LoadClassic`,
     `RestoreSaved`, `OpenTab`, `CloseTab`, `EnterGlobal`, `Tabs`,
     `Classic`, `Begin`, …) and gains `Open`, `Workspaces` and
     `PersistOpenList`: each client keeps its own tabs, classic workspace
     and failed opens (decision 3 now holds). The wire routes a request's
     answer to the client that made it, by the connection's number in the
     request ID's high 32 bits (§2), and merges each connection's
     selection into the model's set (`Backend.SetSelection`). Reconcile
     proves a live session is the record's (by its start directory)
     before restoring onto it or killing it, since another workspace's
     session can carry its name, and marks the record Paused when it
     can't; kill and discard leave such a session running, and resume
     refuses it; a workspace terminal's relaunch asks first who holds its
     name (`session.HeldElsewhere`, which fails closed and is retried by
     the tick when tmux leaves it unanswered). Workspaces are one per
     canonical config dir, so a directory registered twice is served
     once; the TUI opens the served twin in a twin's place at startup
     and refuses a twin or a rename made while loom runs in the picker.
     The session launch flags are kept per config dir. `Protocol` is 2.
   - **3B, the daemon process:** `loom serve`, the lock, spawn on demand,
     the handshake, reconnect and respawn, `serve.log`, the nesting
     guard's move, and the takeover lock's retirement. Left by 3A: a
     daemon's notices while no client is connected (`Boot`'s are handed
     to the first client by hand), the sticky fatal, the GitHub poll's
     start directory for the global workspace, and a guard for two
     global dirs that register the same repository.

     Done as one plan
     ([plan](../plans/2026-10-08-daemon-stage3b-serve.md)): `loom serve`
     (`internal/daemon`) holds `<globalDir>/loom.lock`, boots the model,
     and serves any number of TUIs on a private unix socket it records in
     the lock (§1, §5); `loom serve stop` and a newer client stop it with
     a signal, after which it waits for lifecycle jobs in flight and saves
     (§6). Every `loom` is a client: it spawns the daemon when none runs,
     the newer build wins the handshake, and it pins the daemon's tmux
     server. The takeover lock is retired, and the nesting guard moved to
     `loom serve` (§3). The user's decision (2026-10-08): **when the
     daemon goes away under an open TUI, the TUI restores the terminal,
     says so and exits; live reconnect is a stage of its own after 3B**
     (§7). The coordinator's, told to the user without objection: the open
     list, last-used workspace and UI prefs are shared values, the last
     writer winning; a TUI quitting saves nothing (the daemon saves on
     requests, after the changes it makes on its own, and on stop); a
     notice that reaches no client is kept for the next one, bounded and
     logged; in global mode GitHub polls the repositories the global
     sessions run in, plus those a client's issue picker asks for, not a
     start directory; and a model that fails makes the daemon exit, the
     next `loom` starting a fresh one (which retires the sticky fatal).
     The daemon's environment is frozen when it is spawned, so where the
     model reads its own environment (the credential override, running as
     an account) it publishes the answer and every client shows the
     daemon's. Still open: the guard for two global dirs registering one
     repository, and logging into the default account from a TUI, which
     uses the TUI's environment.
   - **3R, live reconnect** (designed 2026-10-09, built 2026-10-10; the
     user's decisions are marked). A TUI survives the daemon going away.

     - **States.** *Connected*; *stopping* (a `bye` arrived: lifecycle is
       refused, in-flight replies still arrive); *waiting* (the
       connection closed after a `bye`: the TUI polls once a second for
       a daemon to appear (a dial that never spawns) and starts none on
       its own; `ctrl+r`, reserved by the app while offline, starts one);
       *reconnecting* (closed with no `bye`, or a `fatal`: it redials on
       a backoff from 1s to 30s, spawning when none runs, and drops to
       *waiting* after three spawned daemons in a row exit before
       serving, so a daemon that fails at boot is not respawned forever). A banner names the state. **The user's
       decision: respawn only after a crash**, so `loom serve stop` stays
       a stop and an old build never races a new one to spawn while it
       replaces the daemon.
     - **Offline.** What talks to tmux directly keeps working: pane
       rendering and scroll-back, inline and full-screen attach, the
       terminal pane, navigation, the overview, the workbench, help and
       quit. Everything that needs the model is refused at the key by an
       offline whitelist (as overview's `overviewKeyAllowed`), and the
       client refuses requests locally (`core.ErrUnavailable`, which
       matches `ErrRefused`) as a backstop. Statuses stay as last
       published. Every request the TUI awaits a `Reply` for that nothing
       can answer any more (made after the `bye`, refused as unavailable
       around it, or all of them at the loss) is failed with a synthetic
       `Reply`, so Lua calls resume with an error, prompt-send holds
       release and flows end with a message. The link is checked after
       the drain, so a reply that arrived before the connection closed is
       applied, not failed. UI prefs and the focused tab changed offline
       apply locally and are sent on rejoin; quitting offline writes
       nothing, and logs what was lost (`quit.offline_unsaved`).
     - **Rejoin.** `main` hands `app.Run` a rejoin function: the
       startup join, quiet (its messages go to the banner), with a spawn
       flag. **The user's decision: the newer build wins as at startup.**
       The same build is rejoined. A newer daemon (a newer loom replaced
       it) makes the TUI exit: "the loom daemon was replaced by a newer
       loom (X); run loom again". An older one (an older TUI respawned it
       first after a crash) is replaced once, under `replaceGuard`.
     - **Resync.** The TUI swaps its client (`core`, wakes, the loss
       check, the stop function, and the goroutine forwarding wakes),
       rereads every slot's workspace view and instance views from the
       new replica, closes a tab whose workspace the new daemon does not
       serve (unregistered meanwhile; the classic slot falls back to
       global), `Open`s every workspace it shows again (the new daemon's
       first open: the terminal and GitHub polling start; a failed load
       shows its error), sends the prefs and tab changed offline,
       re-publishes its selection, and, when the daemon
       names another tmux server (the old one died), re-pins it and
       releases every pane client for the repair paths to re-attach.
       Drafts, overlays, the workbench, the review and scroll positions
       are kept.
     - **Stable IDs** (§2). **The user's decision:** a workspace's ID is
       derived from its canonical config dir and an instance's from its
       workspace's config dir, title and `created_at`, masked to 53 bits
       (safe in any JSON client), never 0 (the draft row's), with a
       collision probing the next value. A rejoining TUI's slots,
       selection, bells, ladder, workbench and open confirmations keep
       naming the same things, and one naming an instance gone meanwhile
       is refused (`not_found`). With per-daemon counters a new daemon
       would reassign 1, 2, 3, and a confirmation left open across a
       restart could reach another instance. A record rebuilt as a new
       instance (a load retried, a recovery) keeps its ID, and an orphan
       placeholder's comes from its worktree directory's name. These are the
       host-scoped stable IDs a hub would need (the cloud direction).
     - **Stop order.** bye (which refuses new requests), stop listening,
       `Quiesce` with the connections open, save, a last publish, flush
       and close, exit (it used to close first, so in-flight replies were
       lost). A stopping daemon can't be joined: a client that connects
       meanwhile hears the bye after its snapshot and `Dial` fails as
       unavailable. `daemon.ConnectNoSpawn` is the no-spawn mode for
       *waiting* (`ErrNoDaemon` when no process holds the lock).

   - **3C, the subcommands as clients,** plus the duties a daemon has
     with no TUI (trust prompts and hook scans, Assumption 4) and
     unloading a workspace removed or renamed in the registry (until
     then it stays served until restart, under its old name: a client
     can't open it by its new one).

   The user's decisions (2026-10-08):

   - **The split** into 3A, 3B and 3C above.
   - **A workspace's terminal starts on a client's first open of the
     workspace,** and is relaunched on death from then on; a live one is
     always kept (Assumption 5).
   - **Several TUIs may connect at once,** and the takeover lock is
     retired in 3B. Two TUIs attach pane clients to the same agent
     sessions, and tmux sizes each window to the most recently active
     client.
4. **Cleanup.** Delete the TUI's dead lifecycle code, rewrite the
   CLAUDE.md gotchas, update USAGE.md, bump the version.

The scrum workflow starts after stage 3.

## Out of scope

- A network transport or remote clients.
- Authentication beyond the socket's file mode.
- A systemd unit (possible add-on).
- Moving pane rendering into the daemon. Panes attach to tmux directly.
- Windows: no unix sockets in this design, as there are no hooks or
  `flock` there already. (Amended 2026-10-09 by stage 3B: the daemon
  builds for Windows, which releases ship, with its lock record beside
  the lock in `loom.lock.json`; it has no graceful stop there.)

## Assumptions

### Verified against the code on 2026-10-03

- `app` lifecycle code already follows a message-passing rule (Cmd
  goroutines deliver results as messages; the Update goroutine mutates),
  so moving it behind a loop changes the sender, not the rules.
- Completions already act by identity (`reopenedTwin` matches on title
  and worktree path), so a reply naming title and workspace path is
  enough for a client to apply it. **Superseded 2026-10-07:** replies
  name a model-assigned `InstanceID` instead (§2), which a reopened
  workspace's fresh copy of a record does not share.
- ~~Preview panes attach to tmux by session name through `TmuxSession`,
  not through anything the TUI's lifecycle state owns, so they survive a
  daemon restart.~~ **Corrected 2026-10-03:** not true. Every pane reads
  through the instance's own `TmuxSession`, which lifecycle code creates
  and attaches (`Start` → `Restore`, `EnsureRunning`, `Resume`,
  `CrashRestart`, `Restart`). All keys and prompts are written to that
  attach PTY. A daemon that attached its own clients would fight the
  TUI's over window size. Stage 1A makes the assumption true.
- `loomdev` already isolates a sandbox with `LOOM_GLOBAL_DIR` and
  `LOOM_TMUX_SOCKET`; a per-global-dir socket slots into that.

### Still to verify

| # | Assumption | How |
|---|---|---|
| 1 | A detached child started from a TUI process (new session, stdio closed) keeps running after the TUI exits and after its tmux pane closes. | Stage 3's end-to-end test. **Verified 2026-10-09 by stage 3B:** `TestE2E_Daemon_OutlivesItsTUI`. |
| 2 | `$XDG_RUNTIME_DIR` is set in the environments loom runs in (a tmux pane under a systemd user session; a plain ssh login may lack it). | Stage 3 falls back to `<globalDir>/run/`; the test covers both. **Resolved 2026-10-09 by stage 3B:** only the daemon picks the place, and clients dial the socket its lock record names, so a client without the variable still finds it; the daemon falls back to `<globalDir>/run/`, then the temp dir, and listens again elsewhere when its runtime dir is removed (`TestSocketPath`, `TestServe_ASocketWhoseDirIsGoneMovesElsewhere`). |
| 3 | The fallback socket path fits `sun_path`: 108 bytes on Linux, 104 on macOS. A deep `LOOM_GLOBAL_DIR`, such as a sandbox under a long temp dir, overflows it, and `connect` fails with "File name too long" (hit while probing for 1A). | Stage 3: hash into a short path, or bind relative to the dir, and test with a 100-byte global dir. **Resolved 2026-10-09 by stage 3B:** a candidate longer than 100 bytes is skipped, and the last is `loom-<uid>/<hash>.sock` in the temp dir, `<hash>` 16 hex digits (`TestSocketPath`). |
| 4 | With no pane events, the daemon still answers trust prompts and reads hook events promptly. 1A keeps trust detection in the TUI's status scrape and hook scans on pane events. Since 1E the model ticks on its own timer, so its hook backstop scan runs with no TUI, at the tick's cadence. | Stage 3 (3C, since the split): a launch watch (`capture-pane` for N seconds after each launch) and a faster hook-scan timer (~250ms; no file-watch library is vendored). |
| 5 | Loading every registered workspace (decision 3) does not start a Claude workspace terminal in each one. Today activating a workspace auto-creates its terminal. | Decide before stage 3. Suggested rule: create on a client's first open of the workspace, then relaunch on death as today. **Resolved 2026-10-08 by stage 3A (the user's decision): the suggested rule.** A workspace's first open since the model started (`Core.Open`) creates its terminal, or relaunches one that died while nobody had the workspace open or that its restart breaker stopped; a live one is kept. Until then the terminal is dormant: the health tick neither probes, relaunches nor pauses it. |
