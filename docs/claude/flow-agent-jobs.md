# Flow agent jobs: what `--agent` leaves out

`loom flow agent` and `/flow:run`'s engine run each flow agent as a headless `claude -p --agent flow:<role>` job (`agentCommand`, `internal/cli/flow_agent_exec.go`). `--agent` runs the definition's prompt as the main thread, which is not what a subagent gets, so the job adds back what it drops. Each item was probed against real Claude, and each probe is an opt-in test (`LOOM_TEST_REAL_CLAUDE=1`).

| What `--agent` does | What the job does | Probe |
|---|---|---|
| Doesn't preload the definition's `skills:` | inlines them into the brief (`preloadedSkills`) | 2026-11-06, `TestRealClaude_FlowAgentRun` |
| Ignores `--json-schema` | ends the report in status lines (`headlessPreamble`, `statusLines`) | 2026-11-06, `TestRealClaude_FlowAgentRun` |
| Ignores the definition's `maxTurns` | passes `--max-turns`; an `error_max_turns` result is `TURN_CAP`, which `loom flow agent answer` resumes | 2026-11-06, `TestRealClaude_FlowAgentRun` |
| Inherits the parent session's messaging socket and mod link, and so acts as its child | scrubs them from the environment (`scrubbedVar`) | 2026-11-06, `TestRealClaude_FlowAgentRun` |
| Runs the account's enabled plugins, whose hooks inject what no subagent gets | turns each one off in `--settings` (`pluginsOff`) | 2026-11-06, `TestRealClaude_FlowAgentRun` |
| Takes the account's effort (medium on Haiku), whatever the definition pins | passes `--effort` from `agentEffort`, on a resume too | the 2026-11-11 retro: agents ran below their pin in 7 of 10 engine runs |
| Writes a one-hour prompt cache, whatever the definition pins | sets `FORCE_PROMPT_CACHING_5M=1` for `cacheTtl: 5m` (`agentEnv`) | 2026-11-11 |
| Loads a package's `CLAUDE.md` only when a tool touches a path under it | the engine passes an `--add-dir` for each directory above the agent's scope's files that holds a `CLAUDE.md`, with `CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD=1` (`agentRules`); a later Read there loads it no second time | 2026-11-14, `TestRealClaude_FlowAgentPreloadsPackageRules`: 42–54% of developer and reviewer runs had loaded none of their package rules |

The 2026-11-14 check also found that `--append-system-prompt-file` reaches an `--agent` run too, but its text isn't tracked as loaded, so a later read would load the same `CLAUDE.md` again. Prefer `--add-dir` for anything that is a `CLAUDE.md`.

A Claude Code upgrade can change any of these: re-run the probes after one (`LOOM_TEST_REAL_CLAUDE=1 CGO_ENABLED=0 go test ./session/ -run 'TestRealClaude_Flow'`).
