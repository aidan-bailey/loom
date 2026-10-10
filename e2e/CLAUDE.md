# e2e

End-to-end tests that drive real `loom` builds in a loomdev sandbox (`internal/devsandbox`: its own tmux server and global dir, so its own daemon). `e2e/daemon_test.go` covers the daemon's lifecycle: spawn on demand (its working directory included), a stale socket, the daemon killed under an open TUI (which stays up and reconnects, keeping its selection), two TUIs on one daemon and two starting at once, a newer build replacing the daemon under an open older TUI (which exits saying so), an older build refusing, `loom serve stop` with live sessions (the open TUI waits and `ctrl+r` starts a daemon; they come back on the same agent process, not paused), a pause in flight across a stop, the daemon outliving its TUI's tmux session, a same-version rebuild replacing the sandbox daemon, and a stop during a start race. `e2e/e2e_test.go` covers session flows (restart survival, prompts, hook-driven status) and the sandbox leaving other tmux servers alone.

## Rules when modifying this package

- **Tag every file `//go:build e2e`, and skip when a tool is missing.** The suite needs tmux, git and go and builds loom itself, so it runs only under `go test -tags e2e ./e2e/...`; an untagged file would run in every `go test ./...` and fail where tmux is absent. **Convention** — a plain test run that breaks on machines without tmux.
- **Fail a test whose daemon outlives its sandbox.** `newSandbox`'s cleanup checks that `Down` left no daemon of the sandbox's builds running, because a leaked daemon keeps sessions and a socket alive after the test and hides a stop bug. **Enforced** by the suite's own cleanup (`e2e/e2e_test.go`).
- **Match an offline TUI's screen through the banner constants at the top of `e2e/daemon_test.go`, and keep them equal to `bannerText` in `app/link.go`.** The scenarios wait on the banner's words (stopped, reconnecting) and the info lines (`n needs it`, `reconnected to the loom daemon`), so a reworded banner fails every one with a timeout on a screen dump. **Convention** — an edit of one side that the other only reveals at the next e2e run, which no CI job makes.
- **Make a pause really be in flight before claiming one.** The fake agent pauses in milliseconds, so a stop sent after it would race past the pause. `TestE2E_Daemon_PauseAcrossAStop` dirties the worktree and holds `refs/stash` updates for seconds with a `reference-transaction` hook (`holdStashes`, `dirtyWorktree`; git 2.28 or later), then asserts `serve stop` waited. **Enforced** by that test's own elapsed-time assertion; a copy that drops the hook passes only by timing.
- **Changing the daemon, the join or a session's lifecycle?** Run `go test -tags e2e ./e2e/...`: unit tests serve the daemon in process, and only this suite runs real builds against each other across a real tmux server. **Convention** — a regression nothing else catches, since no CI workflow runs this suite.

## Pointers

- [`../internal/devsandbox/CLAUDE.md`](../internal/devsandbox/CLAUDE.md) — the sandbox the suite runs in.
- [`../docs/ARCHITECTURE.md`](../docs/ARCHITECTURE.md) — where the daemon sits.
