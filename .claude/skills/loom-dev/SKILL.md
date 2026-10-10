---
name: loom-dev
description: Launch, drive, and screenshot a dev build of loom safely from inside loom. Use whenever you need to run loom itself — to see a change working in the real TUI or to reproduce a UI bug. Never run ./loom or `go run .` directly in a loom pane.
---

# Loom dev sandbox

A loom TUI in a loom pane is a client of the host's daemon (the user's
global dir), so a dev build run there never gets a daemon of its own: it
joins the user's daemon, or is refused. A loom inside loom never replaces
an older daemon (`replaceGuard`), and a daemon that would start there
refuses (its nesting guard): its boot runs an orphan sweep that kills
unclaimed `loom_*` tmux sessions on the tmux server it pins, which is the
host's (the last daemon's while it runs, whatever `LOOM_TMUX_SOCKET`
says). Use the sandbox instead: its own global dir, so its own daemon and
tmux server, a private registry, and a toy workspace named `toy`.

Run everything from the repo root as `go run ./tools/loomdev <cmd>`. The
sandbox is named after the current branch's leaf; `--sandbox NAME` (`-s`)
picks another.

## The sandbox daemon

A loom TUI is a client of `loom serve`, the daemon that owns the sessions.
The first sandboxed loom to start spawns the sandbox's own daemon (it runs
the sandbox's build, with the sandbox's global dir and tmux server), and the
daemon outlives that TUI:

- `stop` quits the TUI **and** stops the daemon, so the next `start` boots a
  fresh daemon that reattaches the sessions (the restore path).
  `stop --keep-daemon` quits only the TUI, as a real `q` does: the next
  `start` joins the same daemon.
- `daemon` prints the daemon's lock record (pid, socket, build, tmux server)
  and leaves every TUI open. `daemon --stop` stops it gracefully, as
  `loom serve stop` does, and `daemon --kill` sends SIGKILL, as a crash does
  (only to a process proved a build in the sandbox's `bin` dir). Use them to
  see how an open TUI takes losing its daemon (below).
- `start --restart` stops both first, like `stop` then `start`.
- `build` (and `up`, and `start` and `run` unless `--no-build`) replaces
  the binary only. The next loom to start replaces a running daemon only
  when the new build is newer, which takes a build that names its commit
  (one from a git checkout, as loomdev's are; a Nix build stamps it): a
  later commit, or a tree edited and rebuilt at the same one. An older
  build (an earlier commit, or a clean build after a dirty one at the same
  commit) is refused, and one that names no commit joins the running
  daemon as it is: `stop` the daemon first.
- `run` leaves the daemon running when its TUI quits, as a real loom does;
  `stop` ends it.
- `down` stops the daemon before it deletes the sandbox. A daemon that won't
  stop, or a loom from before the daemon holding the sandbox's lock, stops
  `down` with nothing deleted; `down --force` kills it (SIGKILL) first, once
  its executable is proved a build in the sandbox's `bin` dir.
- `ls` shows each sandbox's daemon (pid, socket); `logs` names it and
  includes its `serve.log` (and `serve-crash.log`, after a runtime crash).
- `loom serve stop` by hand, in a subshell so the sandbox's environment
  stays out of yours:
  `(eval "$(go run ./tools/loomdev env)"; "$LOOM_GLOBAL_DIR/../bin/loom" serve stop)`.

Losing the daemon does not end a TUI; it goes offline under a banner on its
top row, keeps showing and attaching panes, refuses the keys that need the
model ("the loom daemon is stopped: n needs it") and joins a daemon again
when one answers:

- A graceful stop (`daemon --stop`, `loom serve stop`, `stop` from another
  driver): "the loom daemon stopped: waiting for one to start (ctrl+r starts
  it)". The TUI polls for a daemon and starts none itself; `ctrl+r` starts one.
- A crash (`daemon --kill`): "lost the loom daemon: reconnecting (attempt N)".
  The TUI redials on a backoff (1s doubling to 30s) and starts a daemon when
  the lock is free, giving up after three starts that fail to serve.
- A newer build replacing the daemon makes an older TUI exit instead, saying
  the daemon "was replaced by a newer loom". An older build is replaced once.

Rejoined, the TUI keeps its tabs, selection and open flows, and shows
"reconnected to the loom daemon".

## Verify a change headlessly

1. `go run ./tools/loomdev up` — create or refresh the sandbox and build it (idempotent).
2. `go run ./tools/loomdev start --restart` — run the dev build in the driver session (160×48).
3. `go run ./tools/loomdev wait --text toy` — wait until the UI is up.
4. Drive it with tmux key names — `keys n`, `keys Enter`, `keys Escape`, `keys Up`, `keys C-c` — and type text with `keys -l some text`.
5. `go run ./tools/loomdev shot` prints the screen (`--ansi` keeps colors). Use `wait --text` instead of sleeping.
6. `go run ./tools/loomdev stop` when done (it stops the sandbox's daemon too); `down` deletes the sandbox.

On a `wait` timeout the tool prints the last screen and the sandbox log
tail. Read them before retrying. `logs -f` follows the logs.

## Recipes

`n` and `a` are dispatched through the Lua script engine asynchronously, so
typing right after them can race the state change — always `wait` for the
state's own prompt text before sending the next keys.

- New session: `keys n` → `wait --text "enter a name for the instance"` → `keys -l <title>` → `keys Enter` → `wait --text "Session Launch Options"` → `keys Enter`. A newly started session auto-attaches the agent pane (footer shows "CAPTURING INPUT"): anything typed now goes to the agent; `keys C-q` detaches back to loom's keymap.
- Send text to the selected agent from loom's keymap: `keys a` → `wait --text "Enter to send to agent"` → `keys -l <text>` → `keys Enter`.
- Fake agent commands (send them as agent text): `work N`, `ask`, `trust`, `bell`, `title X`, `edit`, `commit`, `crash`, `exit`.
- Profiles: `fake` (default), `fake-claude`, `fake-aider`, `shell`; `up --real-claude` adds the real `claude` (costs tokens). Change the default with `up --default-profile fake-aider`.
- Restore path: `stop`, then `start`; sessions persist on the private tmux server, and the fresh daemon reattaches them.
- Offline behaviour with the TUI open: `daemon --stop`, then `wait --text "waiting for one to start"`, try `keys n` (refused with an info line), wait three seconds to see that no daemon appears, then `keys C-r` and `wait --text "reconnected to the loom daemon"`. For a crash, `daemon --kill`, then `wait --text reconnecting` and `wait --text reconnected`.
- Two TUIs on one daemon: `--driver NAME` makes `start`, `stop`, `keys`, `shot` and `wait` drive another session on the sandbox's server, e.g. `start --driver two` then `shot --driver two`. Use `stop --keep-daemon --driver two` to quit it alone.
- Interactive, for a human: `go run ./tools/loomdev run`. The host loom intercepts `ctrl+q` and double-`esc`, so test the dev loom's interact-exit from a plain OS terminal.
- End-to-end suite: `go test -tags e2e ./e2e/...` (the daemon's lifecycle, losing and rejoining it included, is in `e2e/daemon_test.go`).

## Smoke runs

A live run in the sandbox finds bugs no unit test can: an earlier smoke run
found that fast typing into a modal's text field reordered characters
("toy" → "oyt"), so type at speed (`keys -l …`) into every text field you
touch.

- **Pin the binary.** `start` and `run` rebuild from the working tree unless
  `--no-build`, so fix commits landing in the same worktree change the binary
  mid-run. Run `up` once while the tree is clean at a SHA you name in your
  report, then use `start --no-build` (and `run --no-build`) for the rest of
  the run. When fix rounds are landing, wait for the SHA to test before
  rebuilding.
- **Build the base commit into a second sandbox** to tell a regression from
  an old bug: reproduce every oddity there before reporting it as new.
  loomdev builds from the git checkout it runs in, so extract the base
  without a worktree: `git archive <sha> | tar -x -C <scratch>/base`, then
  `git init` there (loomdev needs a git top level holding loom's `go.mod`)
  and build with `GOFLAGS=-buildvcs=false`. Compile loomdev once
  (`go build -o <scratch>/loomdev ./tools/loomdev`) and run that binary from
  `<scratch>/base` with its own sandbox name (`-s base…`), so neither
  sandbox's tool changes under you.
- **The clipboard is not isolated.** A drag-select in the sandboxed loom
  writes the host's clipboard (Wayland or OSC 52); don't leave secrets on it,
  and expect your own clipboard to change.
- **Accounts are not isolated either.** The sandbox overlays only
  `LOOM_TMUX_SOCKET`, `LOOM_GLOBAL_DIR` and `LOOM_HOME`; `CLAUDE_CONFIG_DIR`
  and `PATH` are yours. Before driving `loom account` or the Accounts screen,
  point `CLAUDE_CONFIG_DIR` at a throwaway main config dir and put a `claude`
  that resolves to the sandbox's fakeagent first on `PATH`, or adding an
  account links into your real `~/.claude`. Set both in the environment you
  run `loomdev start` from, before the sandbox's tmux server first starts (a
  fresh sandbox, or after `down`): the server keeps the environment it started
  with, the driver loom inherits it, and the sandbox's daemon inherits the
  driver's. For the CLI, run it through `loomdev env` in a subshell with the
  same two variables set.

## Rules

- Never run `./loom`, `go run .`, or `loom reset` directly in a loom pane.
- Never run a bare `loom serve stop` in a loom pane: with the pane's
  environment it stops the host's daemon, and every open loom TUI with it.
  Stop the sandbox's daemon with `loomdev stop` or `loomdev daemon --stop`
  (or the subshell recipe above).
- Never run `clean.sh` / `clean_hard.sh` from inside loom (they refuse anyway).
- Fix the product, not the sandbox, when the UI misbehaves; fix the test's key sequence when the UI is right.
- The nesting guard only recognizes loom-managed enclosing sessions (`loom_*`/`claudesquad_*`); a dev loom started from a plain (non-loom) tmux pane on a server that also hosts loom sessions is not refused, so still use `loomdev` there. Setting `LOOM_TMUX_SOCKET` is no way around either guard: a daemon keeps the last daemon's tmux server while it runs, and the global dir stays the user's.
