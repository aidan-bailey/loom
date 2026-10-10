# internal/devsandbox

Dev sandboxes for developing loom inside loom: a named dir holding a dev build, a toy workspace, seeded config, its own global dir (so its own daemon) and a private tmux server, plus the headless driver `tools/loomdev` and the e2e suite run loom through. Full reference in [`README.md`](README.md).

## Rules when modifying this package

- **Kill a lock holder only once its executable is proved a build in the sandbox's bin dir** (`ForceDown` → `killSandboxLoom`, `runsSandboxLoom` through `/proc/<pid>/exe`). A stale lock record can name a pid reused since by an unrelated process, or by the user's own loom. **Enforced** by `TestForceDown_KillsNoProcessButTheSandboxsLoom` (`internal/devsandbox/daemon_test.go`).
- **Remove nothing when `Down` can't stop the lock holder, and nothing outside `<BaseDir>/<Name>`.** A daemon past its stop timeout, or a loom from before the daemon, makes `Down` fail with its error naming `loomdev down --force`; deleting the dir under a live daemon would strand its sessions and socket. **Enforced** by `TestDown_AProcessThatWontStopNeedsForce` and `TestDown_RefusesOutsideBase`.
- **Keep the sandbox's isolation in `Env` (`LOOM_TMUX_SOCKET`, `LOOM_GLOBAL_DIR`, `LOOM_HOME`), and know what it leaves shared.** The global dir is what keeps a sandboxed loom off the host's daemon and sessions; `CLAUDE_CONFIG_DIR`, `PATH` and the clipboard are the host's, so an account flow driven in a sandbox needs a throwaway `CLAUDE_CONFIG_DIR` and a `claude` on `PATH` that resolves to fakeagent, or `loom account add` links into the real `~/.claude`, and a drag-select writes the host's clipboard. **Enforced** for the overlay by `TestOpen_PathsAndEnv` (`internal/devsandbox/sandbox_test.go`); **Convention** for the rest — a smoke run that changes the developer's real Claude config.
- **Name drivers so loom's orphan sweep never takes them** (`DriverSession`, `WithDriver`): no `loom_`/`claudesquad_` prefix, and no `:` or `.`, which tmux targets can't name exactly. **Enforced** by `TestWithDriver_ValidatesTheName`.
- **Make every `Up` step check for its own artifact.** `Up` runs on every `loomdev up`, `start` and `run`, so re-running must top up a partial sandbox and rewrite `config.json` only when it is missing or a profile change is asked for. **Enforced** by `TestUp_KeepsEditedConfigUnlessFlagsGiven` and `TestUp_RegistersToyWorkspaceOnce` (`internal/devsandbox/up_test.go`).

## Pointers

- [`../../tools/CLAUDE.md`](../../tools/CLAUDE.md) — the `loomdev` CLI and `fakeagent`.
- [`../../.claude/skills/loom-dev/SKILL.md`](../../.claude/skills/loom-dev/SKILL.md) — how to drive a sandbox.
- [`../../docs/ARCHITECTURE.md`](../../docs/ARCHITECTURE.md) — where this package sits.
