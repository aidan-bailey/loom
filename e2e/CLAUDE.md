# e2e

End-to-end tests that drive real `loom` builds in a loomdev sandbox (`internal/devsandbox`: its own tmux server and global dir, so its own daemon). `e2e/daemon_test.go` covers the daemon's lifecycle: spawn on demand (its working directory included), a stale socket, the daemon killed under an open TUI, two TUIs on one daemon and two starting at once, a newer build replacing the daemon under an open older TUI, an older build refusing, `loom serve stop` with live sessions (they come back on the same agent process, not paused), the daemon outliving its TUI's tmux session, a same-version rebuild replacing the sandbox daemon, and a stop during a start race. `e2e/e2e_test.go` covers session flows (restart survival, prompts, hook-driven status) and the sandbox leaving other tmux servers alone.

## Rules when modifying this package

- **Tag every file `//go:build e2e`, and skip when a tool is missing.** The suite needs tmux, git and go and builds loom itself, so it runs only under `go test -tags e2e ./e2e/...`; an untagged file would run in every `go test ./...` and fail where tmux is absent. **Convention** — a plain test run that breaks on machines without tmux.
- **Fail a test whose daemon outlives its sandbox.** `newSandbox`'s cleanup checks that `Down` left no daemon of the sandbox's builds running, because a leaked daemon keeps sessions and a socket alive after the test and hides a stop bug. **Enforced** by the suite's own cleanup (`e2e/e2e_test.go`).
- **Changing the daemon, the join or a session's lifecycle?** Run `go test -tags e2e ./e2e/...`: unit tests serve the daemon in process, and only this suite runs real builds against each other across a real tmux server. **Convention** — a regression nothing else catches, since no CI workflow runs this suite.

## Pointers

- [`../internal/devsandbox/CLAUDE.md`](../internal/devsandbox/CLAUDE.md) — the sandbox the suite runs in.
- [`../docs/ARCHITECTURE.md`](../docs/ARCHITECTURE.md) — where the daemon sits.
