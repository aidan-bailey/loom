# Dev Sandbox for Developing Loom Inside Loom

**Date:** 2026-09-16
**Status:** Approved design

## Problem

Loom is developed from inside loom: agent sessions run in worktrees under
`<repo>/.loom/worktrees/`, and the natural way to try a change is to build the
binary and run it from the session's terminal pane. Today that is destructive.

### The orphan sweep kills the host

`session.CleanupOrphanedSessions` (`session/reconcile.go`) runs at startup
(`app/app.go`, both the classic and multi-tab restore paths) and kills every
`loom_*` / `claudesquad_*` tmux session that *this process* has not loaded as
an instance. Every tmux invocation in loom uses the default server selection,
and tmux resolves that as:

1. explicit `-L <name>` / `-S <path>`, else
2. the socket named in `$TMUX`, else
3. the default socket under `TMUX_TMPDIR`.

A terminal pane is itself a tmux pane, so `$TMUX` points at the host loom's
server. A dev build launched there talks to the host server, claims few or no
titles, and kills the host's agent sessions — including `loom_term_<title>`,
the pane it is running in. `TMUX_TMPDIR` alone does not help: tmux ignores it
while `$TMUX` is set.

### `LOOM_HOME` does not fully isolate

`config.GetGlobalConfigDir()` deliberately ignores `LOOM_HOME`, so a dev build
still reads and writes the host's `~/.loom/workspaces.json` (`last_used`,
`open_workspaces`). If the dev build opens the loom repo itself as a workspace
it shares `<repo>/.loom/` — the host's state, worktrees, and orphan-worktree
auto-clean.

### Cleanup scripts are footguns

`clean.sh` / `clean_hard.sh` run `tmux kill-server` and `rm -rf ~/.loom`.
From inside loom that wipes the environment doing the development.

## Goals

- One sandbox primitive with two front doors: an interactive launcher for the
  human, and a scriptable headless driver the agent uses to verify its own
  work before claiming it is done.
- Isolation enforced by the loom binary itself, so an ad-hoc `./loom` in a
  pane is safe (refused) rather than catastrophic.
- The user's real `HOME` is never swapped: Claude auth, `gh`, git identity,
  and shell rc keep working inside the sandbox.
- Deterministic, token-free agents by default; real `claude` on opt-in.
- Named, persistent sandboxes so quit → rebuild → relaunch exercises the
  restore/reconcile paths.

## Non-goals

- CI wiring for the e2e suite (follow-up).
- Checked-in state fixtures ("3 sessions, one paused, one orphaned").
- A `nix run .#loomdev` app.
- Hot reload (edit → auto-rebuild → auto-relaunch).
- Namespace/container isolation (bubblewrap, `unshare`).

## Design

### 1. Isolation layer (product code)

#### 1a. Single tmux command builder

Add to `session/tmux`:

```go
// Command builds a tmux invocation. When LOOM_TMUX_SOCKET is set, "-L <name>"
// is prepended so the call targets that private server regardless of $TMUX.
func Command(ctx context.Context, args ...string) *exec.Cmd
```

A context-free variant (or `context.Background()` at call sites) covers the
existing `exec.Command` uses. Every tmux exec in `session/tmux/tmux.go` (32
`"tmux"` literals) and `session/reconcile.go` (4) switches to the builder,
including the two `attach-session` PTY spawns. With the variable unset the
argv is byte-identical to today.

The socket name is read once per call via `os.Getenv` (no package-level
caching), so tests can set it with `t.Setenv`.

The builder lives in its own file, `session/tmux/command.go`, together with
`EnclosingSessionName()` — the one deliberately unsocketed call (see 1c).

**Enforcement test:** a unit test walks the non-test `.go` files in the module
(excluding `vendor/`) and fails on any `exec.Command`/`exec.CommandContext`
whose first program argument is the literal `"tmux"` anywhere except
`session/tmux/command.go`.

#### 1b. `LOOM_GLOBAL_DIR`

`config.GetGlobalConfigDir()` returns `$LOOM_GLOBAL_DIR` when set, applying
the same `~` expansion and absolute-path validation as `GetConfigDir`;
otherwise `~/.loom` as today. A separate variable (rather than widening
`LOOM_HOME`) keeps `LOOM_HOME`'s documented meaning unchanged for existing
users. `config.MigrateLegacyHome` also skips when `LOOM_GLOBAL_DIR` is set.

User Lua scripts already resolve through `GetConfigDir()` (`scriptsDir` in
`app/app_scripts.go`), so a sandbox `LOOM_HOME` already excludes the user's
`~/.loom/scripts`.

#### 1c. Nesting guard

The guard runs in exactly two places, before any tmux-touching work: the root
command's TUI entry (before `app.Run`, whose startup runs the orphan sweep)
and `loom reset` (which calls `tmux.CleanupSessions`). No other subcommand
touches tmux, so none other is guarded. It trips when:

1. `$TMUX` is non-empty, and
2. `LOOM_TMUX_SOCKET` is empty, and
3. `tmux.EnclosingSessionName()` — `tmux display-message -p '#S'`,
   deliberately *not* via the builder because the question is about the
   enclosing server — returns a name with prefix `tmux.TmuxPrefix` or
   `tmux.LegacyTmuxPrefix`.

If all hold, exit non-zero with:

```
loom: refusing to start inside a loom-managed tmux session (<name>) — its
orphan sweep would kill the host's sessions. Use `go run ./tools/loomdev run`
or set LOOM_TMUX_SOCKET.
```

`LOOM_ALLOW_NESTED=1` bypasses the guard. A failing
`display-message` (no server, not in tmux) means "not nested" — the guard
fails open only in the case where there is no enclosing loom server to harm.

The session-name lookup is injected (function field or `cmd.Executor`) so the
guard is unit-testable without tmux.

#### 1d. `loom debug`

Prints the effective tmux socket (`default` when unset), the global config
dir, and whether the nesting guard would trip.

### 2. Dev tool and sandbox

#### Form

- `internal/devsandbox` — all logic: `Sandbox`, `Up`, `Build`, `Start`,
  `Stop`, `SendKeys`, `Screen`, `WaitFor`, `Down`, `List`.
- `tools/loomdev` — thin CLI over `internal/devsandbox`
  (`go run ./tools/loomdev <cmd>`).
- `tools/fakeagent` — the fake agent binary.

`flake.nix`'s `buildGoModule` builds every `main` package when `subPackages`
is unset; set `subPackages = [ "." ]` so `loomdev` and `fakeagent` never land
in the installed package. Goreleaser has no `main:` key and already builds
only the root package — no change needed there.

#### Layout

Root: `${XDG_STATE_HOME:-$HOME/.local/state}/loom-dev/<name>/`. The default
`<name>` is the leaf of the current git branch (`aidanb/dev-env` → `dev-env`),
so parallel loom sessions get distinct sandboxes without coordination.

```
bin/loom                  dev build of the invoking worktree
bin/fakeagent
bin/personas/fakeagent    → ../fakeagent
bin/personas/claude       → ../fakeagent
bin/personas/aider        → ../fakeagent
global/                   LOOM_GLOBAL_DIR (workspaces.json)
home/                     LOOM_HOME (config.json, logs/)
repo/                     toy git repo, registered as workspace "toy"
                          (its .loom/ holds state + worktrees)
origin.git/               bare remote so push/merge have a target
sandbox.json              name, socket, source worktree, build SHA
```

Environment for every sandboxed loom process:

```
LOOM_TMUX_SOCKET=loomdev-<name>
LOOM_GLOBAL_DIR=<root>/global
LOOM_HOME=<root>/home
```

`HOME` is untouched.

#### Sandbox `config.json`

- `DefaultProgram`: `<root>/bin/personas/fakeagent`
- `Profiles`: `fake`, `fake-claude`, `fake-aider`, `shell` (`$SHELL`); plus
  `claude` (real, from `PATH`) only when `up --real-claude` is given.
- `ClaudeRemoteControl`: `false` (avoids remote-control title collisions with
  host sessions when real Claude is opted in).

#### Toy repo

`git init` with a local `user.name`/`user.email`, a few commits touching a
`README.md`, a small `.go` file, and a `docs/notes.md` (gives the workbench
markdown/review tabs content). `origin` points at `origin.git`.

#### Commands

| Command | Behavior |
|---|---|
| `up [name] [--real-claude]` | Idempotent create of the layout, toy repo, bare origin, and workspace registration (via the sandboxed `loom workspace add <root>/repo --name toy`). |
| `build [name]` | `CGO_ENABLED=0 go build -o <root>/bin/loom .` from the invoking module root, plus `fakeagent`. Records the SHA in `sandbox.json`. |
| `run [name]` | `build`, then exec the dev loom in the **current terminal** with the sandbox env, cwd = toy repo. |
| `start [name] [--size WxH]` | `build`, then create driver session `dev-driver` (no `loom_` prefix → never swept) on the private socket at a fixed size (default 160×48) running the dev loom. No-op if already running; `--restart` replaces it. |
| `stop [name]` | Send `q`, wait for the driver pane to exit (bounded), then kill the driver session. |
| `keys <name> <keys…>` | `send-keys` to the driver (tmux key names). |
| `shot <name> [--ansi]` | Print the driver pane (`capture-pane -p`, `-e` with `--ansi`). |
| `wait <name> --text S [--timeout D]` | Poll the driver screen until `S` appears (default 10s). On timeout: print the last screen and the tail of the sandbox `loom.log`, exit non-zero. |
| `logs [name] [-f]` | Tail the sandbox `loom.log` files. |
| `env [name]` | Print `export` lines for manual use. |
| `ls` | List sandboxes with private-server liveness. |
| `down [name]` | `tmux -L loomdev-<name> kill-server`, then `rm -rf` the root after asserting it is a direct child of the `loom-dev` root. |

`start`/`run` fail loudly with compiler output if the build fails.

#### Fake agent

Adapter selection is by basename of the program's first token
(`agent.basenameMatch`), so the persona symlinks make loom apply the real
Claude/Aider adapters — launch-flag composition, trust-prompt handling, and
pending-prompt patterns — to a deterministic process. `fakeagent` reads its
persona from `argv[0]`, accepts and ignores unknown flags (so Claude launch
flags like `--permission-mode` and `--append-system-prompt-file` are
harmless), prints a banner, and reads commands from stdin:

| Command | Effect |
|---|---|
| `work N` | Print a line every ~100ms for N seconds (drives Running). |
| `ask` | Print the persona's pending-prompt pattern and wait for a line. |
| `bell` | Emit BEL. |
| `title X` | Set the pane title via OSC 2. |
| `edit` | Append to a file in cwd (moves diff stats). |
| `commit` | `git add -A && git commit` in cwd. |
| `crash` | Exit 1 (drives crash recovery). |

The Claude roster stays inert for fake personas: `rosterQueryCmd` runs the
real `claude agents --json` from `PATH`, no real entry has a sandbox worktree
cwd, so `rosterStatusFor` expresses no opinion and the scraper ladder decides.

#### Known limitation

Run inline in a host pane, the dev loom never sees `ctrl+q` or double-`esc` —
the host intercepts them. Testing the dev loom's own interact-exit requires
`loomdev run` from a plain OS terminal.

### 3. Agent verification loop, tests, docs

#### Project skill

`.claude/skills/loom-dev/SKILL.md` documents the recipe
(`up` → `start` → `wait --text` → `keys` → `shot` → `stop`) and two rules:
never run `./loom` / `go run .` directly in a pane, never run `clean.sh` from
inside loom. The built-in `run` skill checks for a project launch skill before
its generic TUI fallback, so agents in this repo pick it up.

#### Tests

Unit:

- `tmux.Command` argv with and without `LOOM_TMUX_SOCKET`.
- The no-raw-`"tmux"` enforcement test.
- `GetGlobalConfigDir` with/without `LOOM_GLOBAL_DIR` (absolute, `~`,
  relative → error); `MigrateLegacyHome` skip.
- Nesting guard truth table via the injected lookup, including
  `LOOM_ALLOW_NESTED`, the legacy prefix, and a failing lookup.
- `internal/devsandbox`: layout creation, idempotent `up`, default-name
  derivation, `down`'s root-containment assertion.

Real-tmux (skipped when `tmux` is absent, like
`TestScrollbackAccumulation_RealTmux`):

- Two servers on distinct `-L` sockets; `CleanupOrphanedSessions` with
  `LOOM_TMUX_SOCKET` pointing at one leaves the other's `loom_*` session
  alive.

E2E (`//go:build e2e`, `go test -tags e2e ./e2e/...`, skipped without tmux):

1. **Nesting safety** — a decoy `loom_decoy` session on a second private
   socket survives sandbox `up` + `start`.
2. **Status path** — create a `fake-aider` session, send `ask`, wait for the
   prompting indicator.
3. **Restore path** — create a session, `stop`, `start`; the session is
   present and running.

#### Cleanup scripts

`clean.sh` and `clean_hard.sh` gain the same guard (refuse when the enclosing
tmux session has a loom prefix) and a comment pointing at `loomdev down`.

#### Docs

- `CLAUDE.md` — a "Dev sandbox" block under Build & Development Commands;
  Gotchas for the tmux builder rule and why the nesting guard exists.
- `CONTRIBUTING.md` — a short dev-loop pointer.
- `CLAUDE.md` Environment Variables and `USAGE.md` — `LOOM_TMUX_SOCKET`,
  `LOOM_GLOBAL_DIR`, `LOOM_ALLOW_NESTED`.

## Error handling summary

| Situation | Behavior |
|---|---|
| Bare `./loom` in a loom pane | Guard refuses with a pointer to `loomdev`. |
| `display-message` fails | Treated as not nested. |
| Invalid `LOOM_GLOBAL_DIR` | Startup error, same shape as invalid `LOOM_HOME`. |
| Build failure in `start`/`run` | Non-zero exit with compiler output; driver not started. |
| `wait` timeout | Last screen + log tail, non-zero exit. |
| `down` on a path outside the `loom-dev` root | Refuses; nothing removed. |
| Sandbox name reused from another worktree | Warn with the recorded source worktree; proceed. |

## Rollout order

1. `tmux.Command` builder + call-site migration + enforcement test (pure
   refactor, no behavior change when unset).
2. `LOOM_GLOBAL_DIR`.
3. Nesting guard + `loom debug` output + cleanup-script guards.
4. `tools/fakeagent`.
5. `internal/devsandbox` + `tools/loomdev` + `flake.nix` `subPackages`.
6. E2E suite.
7. Project skill + docs.
