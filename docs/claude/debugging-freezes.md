# Debugging a frozen TUI or daemon

**Symptom:** "loom is unresponsive but the cursor still blinks" (the blink is the host terminal's, not loom's): keys do nothing and `q` doesn't quit. Or the TUI navigates, but the first action (`n`, `D`, `s`, `r`) hangs it.

**Cause:** almost always a circular wait in userspace. Bubble Tea's `Program.Send` blocks until the Update goroutine receives, so it behaves like a lock: a `Send` made from Update itself, or while holding a lock the Update goroutine takes, never returns. The daemon has the same shape around its loop: the loop goroutine runs every model call, so a model step that waits on a client, or calls the loop, stops everything.

**Rule:** never `Send` from the Update goroutine or while holding a lock Update takes; never let the loop's goroutine wait on a client or call the loop; do blocking work in a `Job`, not on the loop.

## 1. Is it frozen?

- **Detach first.** Loom enters inline attach on a session it just started, so keys go to the agent, and a `ctrl+c` there kills the agent: its output stops, which looks like a freeze. Press `ctrl+q` to detach, then a navigation key; only if nothing moves is it frozen.
- **A two-second stall is known, not a freeze.** The full-screen attach (`alt+a`, `alt+t`) releases the pane client on the Update goroutine and waits up to `pumpWaitTimeout` (`session/tmux/tmux.go`) for its output pump.
- **TUI or daemon?** A request waits for its reply with no timeout (`core/rpc/client.go:request`), and requests are made on the Update goroutine. If navigation works until the first action and then nothing moves, the daemon's loop is the stuck one: dump the daemon.

## 2. Fingerprint it without a debugger

```sh
for t in /proc/<pid>/task/*; do cat "$t/wchan"; echo; done | sort | uniq -c
```

A userspace deadlock (goroutines parked on a channel or a mutex) shows futex, epoll and child waits and no thread stuck in I/O (the bell deadlock's fingerprint); the counts vary:

```text
     12 __futex_wait
      1 do_epoll_wait
      1 do_wait
```

That says the goroutines are parked, not on what: the dump names it. The logs bound the moment: the last line before the silence (`loom debug` prints the TUI's and the daemon's log paths).

## 3. Get a goroutine dump

`dlv attach` fails on a process that isn't your descendant (Yama `ptrace_scope=1`), but a signal needs no ptrace: SIGQUIT makes the Go runtime print every goroutine, then exit.

- **The daemon.** First check what it is doing: `pgrep -a -P <pid>` lists its children. Jobs keep running while the loop is wedged, so a git child means a pause, kill or resume is mid-step, and a SIGQUIT now can leave a stash taken with the worktree still in place, or a killed `worktree remove` leaving a gutted tree (recovery: [`incident-triage.md`](incident-triage.md)); wait for git children to exit, and dump a sandbox's daemon rather than the user's whenever the freeze reproduces there. Then `kill -QUIT <pid>` on that one exact pid: `loom debug` prints the user's daemon's, `go run ./tools/loomdev ls` a sandbox daemon's. The daemon catches only SIGTERM, SIGINT and SIGHUP (`serve.go`) and routes the runtime's crash output to `logs/serve-crash.log` in the global dir (`debug.SetCrashOutput`), which receives a SIGQUIT dump too (checked 2026-10-09 with a minimal program). The daemon exits without its stop's save, which a wedged loop could not run anyway. Sessions keep running in tmux, every TUI quits with "the daemon stopped", and the next `loom` starts a new daemon.
- **The TUI.** It sets no crash output, and its stderr is the terminal, on the alt screen, where the dump is unreadable; a tmux pane's alt screen keeps no scrollback, so a `loomdev start` driver loses it too. Reproduce in a TUI whose stderr is a file: `go run ./tools/loomdev run 2>"$S/tui.stderr"`, where `$S` is a scratch dir (the sandbox's loom inherits loomdev's stderr). From another shell, find that TUI's exact pid: `pgrep -af 'loom-dev/.*/bin/loom'` also lists other sandboxes' TUIs, their daemons and `loomdev start` drivers, so pick the one whose parent is your `loomdev run` (`ps -o pid,ppid,args -p <pid>`), or take the newest with `pgrep -n`. `kill -QUIT` that pid alone, then `reset` the first terminal.

## 4. Read the dump: known signatures

| Stuck goroutine | What it means |
|---|---|
| The Update goroutine in `tea.(*Program).Send` | Update sends to itself. |
| The output pump in `xvtEmulator.Write`, inside an x/vt callback that calls out, while Update waits on the emulator's lock (`Render`, `Resize`, `Cursor`) | The 2026-07-13 bell deadlock: the Bell callback reached `Send` under the wrapper's write lock. Callbacks only assign wrapper fields; an event that must notify sets a pending flag that `Write`/`Resize` hand out after unlocking (`session/vt/xvt.go:takeBellLocked`, pinned by `TestBell_FiresOutsideWriteLock`). |
| The output pump in `io.(*pipe).Write` under `xvtEmulator.Write` | x/vt wrote a terminal-query reply nobody reads; `NewXVT` drains that pipe (`TestXVT_QueryReplyDoesNotBlock`). |
| The Update goroutine in `rpc.(*Client).request` | The TUI waits on the daemon: dump the daemon. |
| Daemon connection readers in `core.(*Loop).do` | Victims: each connection's reader handles its frames in order. Find the loop goroutine, `core.(*Loop).run`. |
| The loop goroutine itself in `core.(*Loop).do` | A call from inside a call: model code called the loop. |
| The loop goroutine in a subprocess, a file or a socket | Blocking work on the loop; it belongs in a `Job`. |
| Any goroutine holding `TmuxSession.stateMu` across PTY or subprocess I/O | Snapshot under the lock, release it, then do the I/O. |

## 5. Amplify the repro

An agent that streams output with bells fired the bell deadlock within a second of the session starting, no keys needed:

```sh
cat > "$S/bells.sh" <<'EOF'
#!/bin/sh
i=0
while :; do i=$((i+1)); printf 'line %d\a\n' "$i"; sleep 0.02; done
EOF
chmod +x "$S/bells.sh"
go run ./tools/loomdev run -- --program "$S/bells.sh" 2>"$S/tui.stderr"
```

## Traps

- SIGQUIT ends the process: one dump per repro, so redirect stderr before you start.
- A driver of your own instead of loomdev: Bubble Tea's first paint waits for terminal-capability replies a driver never sends, so poll for text (`loomdev wait --text`) rather than sleep; and a tmux socket under a deep scratch dir exceeds the unix socket path limit ("File name too long", then loom's "timed out waiting for tmux session"). loomdev's named sockets avoid both.
- Dump a sandbox, not the user's processes, unless the user's own daemon is the frozen one.

## What the gates won't tell you

- `-race` finds data races, not deadlocks: a deadlock needs its interleaving, which a test rarely hits.
- `TestBell_FiresOutsideWriteLock` pins the bell only; a new callback that notifies needs a test of its own.
- Nothing tests for a `Send` on the Update goroutine or a loop call made on the loop.
