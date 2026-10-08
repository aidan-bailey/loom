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
