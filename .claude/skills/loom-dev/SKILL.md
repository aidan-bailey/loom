---
name: loom-dev
description: Launch, drive, and screenshot a dev build of loom safely from inside loom. Use whenever you need to run loom itself — to see a change working in the real TUI or to reproduce a UI bug. Never run ./loom or `go run .` directly in a loom pane.
---

# Loom dev sandbox

Loom's startup orphan sweep kills every unclaimed `loom_*` tmux session on
the server it talks to. Inside a loom pane that is the host's server, so the
binary refuses to start there (nesting guard). Use the sandbox instead: a
private tmux socket, a private registry, and a toy workspace named `toy`.

Run everything from the repo root as `go run ./tools/loomdev <cmd>`. The
sandbox is named after the current branch's leaf; `--sandbox NAME` (`-s`)
picks another.

## Verify a change headlessly

1. `go run ./tools/loomdev up` — create or refresh the sandbox and build it (idempotent).
2. `go run ./tools/loomdev start --restart` — run the dev build in the driver session (160×48).
3. `go run ./tools/loomdev wait --text toy` — wait until the UI is up.
4. Drive it with tmux key names — `keys n`, `keys Enter`, `keys Escape`, `keys Up`, `keys C-c` — and type text with `keys -l some text`.
5. `go run ./tools/loomdev shot` prints the screen (`--ansi` keeps colors). Use `wait --text` instead of sleeping.
6. `go run ./tools/loomdev stop` when done; `down` deletes the sandbox.

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
- Restore path: `stop`, then `start`; sessions persist on the private tmux server.
- Interactive, for a human: `go run ./tools/loomdev run`. The host loom intercepts `ctrl+q` and double-`esc`, so test the dev loom's interact-exit from a plain OS terminal.
- End-to-end suite: `go test -tags e2e ./e2e/...`.

## Rules

- Never run `./loom`, `go run .`, or `loom reset` directly in a loom pane.
- Never run `clean.sh` / `clean_hard.sh` from inside loom (they refuse anyway).
- Fix the product, not the sandbox, when the UI misbehaves; fix the test's key sequence when the UI is right.
