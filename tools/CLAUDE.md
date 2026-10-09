# tools

Developer tools, excluded from the Nix package (`excludedPackages` in `flake.nix`) but built and tested by `go test ./...`: `tools/loomdev` (the dev-sandbox CLI over `internal/devsandbox`), `tools/fakeagent` (a deterministic, token-free stand-in for an agent) and `tools/claudemd` (the lint that holds the CLAUDE.md, README and guide files to their conventions; its package doc in `tools/claudemd/main.go` lists the checks).

## Rules when modifying this package

- **Run loom from a loom pane only through `loomdev`.** A dev build run there joins, or is refused by, the user's own daemon, and a bare `loom serve stop` stops it and quits every open TUI; the sandbox has its own global dir, daemon and tmux server. **Convention** — a dev loom acting on the user's real sessions. Skill: [`../.claude/skills/loom-dev/SKILL.md`](../.claude/skills/loom-dev/SKILL.md)
- **Keep `loomdev`'s driver names out of loom's namespace.** `--driver` refuses a `loom_` or `claudesquad_` name, which the sandbox daemon's orphan sweep would take for one of its own sessions. **Enforced** by `TestDriverFlag_RejectsLoomSessionNames` (`tools/loomdev/main_test.go`).
- **Take `fakeagent`'s persona from `argv[0]` through loom's adapter registry, never from hard-coded prompt text.** The sandbox installs it under persona names (`claude`, `aider`), and `agent.DefaultRegistry().Lookup` supplies the real pending and trust prompts, so what it prints can't drift from what loom scans for. **Enforced** by `TestPersonaFor_MatchesLoomAdapters` (`tools/fakeagent/agent_test.go`).
- **Make `fakeagent` fire loom's hooks the way Claude does.** It honours `--settings` and `--resume` and answers `fakeagent agents …` with no sessions, so a sandbox's Claude status is hook-driven; a hook it fires differently tests a Claude that doesn't exist. **Enforced** by `TestRun_EmitsHooksLikeClaude` and `TestHookEmitter_ThroughLoomsHooks` (`tools/fakeagent/hooks_test.go`).
- **Derive what `claudemd` requires from the tree, and fail closed.** Package dirs, guides and skills come from walking the repo, so a new package without a CLAUDE.md fails by default, and finding no package dir at all is a problem, not a pass. **Enforced** by `TestCoverage` (`tools/claudemd/lint_test.go`).
- **Keep `claudemd`'s exemptions and allow-list deliberate.** An exemption in `repoConfig` carries its reason and becomes a problem once its package is gone; a `tools/claudemd/idents.allow` entry carries a reason and holds only a negative claim (a rule saying something must not exist), since any other entry hides a stale rule. **Enforced** by `TestCoverage` and `TestIdents_AllowList` (`tools/claudemd/idents_test.go`).
- **Change a `claudemd` check or heuristic only with a test on a temp-dir fixture tree.** Fixtures live only in `t.TempDir()` at runtime: the repo's enforcement tests parse every committed `.go` file, `testdata/` included, so a committed fixture could trip them. **Convention** — a check that drifts from what its tests describe, or a fixture failing another package's test.
- **Keep `claudemd` stdlib-only, with every identifier unexported and no loom import.** revive's `exported` rule (`.golangci.yml`) covers `tools/`, and importing loom would pull in config and need a `TestMain` isolating the loom dirs. **Convention** — lint failures in CI, or a lint that breaks with the code it checks.

## Pointers

- [`../internal/devsandbox/CLAUDE.md`](../internal/devsandbox/CLAUDE.md) — the sandbox `loomdev` drives.
- [`../docs/ARCHITECTURE.md`](../docs/ARCHITECTURE.md) — where these tools sit.
