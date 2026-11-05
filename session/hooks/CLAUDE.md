# session/hooks

Loom's Claude Code hooks for one launch: the settings file `claude --settings` loads, the recorder command every registered event runs, the brief each subagent but a fork gets at its start (`subagent-context.json`, printed by a second `SubagentStart` entry), the launch ID, and the parsing and scanning of the events the recorder writes. `session` prepares a folder per launch (`Prepare`) and scans it. Full reference in [`../README.md`](../README.md).

## Rules when modifying this package

- **Give subagents loom's context only through the brief's own `SubagentStart` entry, with matcher `^(?!fork$)`, and keep the recorder's entries free of matchers.** A fork already holds its parent's whole prompt and would read loom's rules twice, while the subagent tracker needs every agent's start, forks included. Claude Code reads the matcher as a JavaScript regex, which Go's `regexp` can't evaluate. **Enforced** by `TestSettingsJSON_BriefsSubagentsButNotForks`, and for what the matcher matches by the opt-in `TestRealClaude_SubagentBrief` (`LOOM_TEST_REAL_CLAUDE=1`).
- **Make every hook command read its whole input and exit 0, whatever the folder holds — except the git guard (`SettingsJSON`'s guardCommand, `loom flow guard`), which exits 2 to block a guarded agent's call and still reads its whole input.** Claude pipes the payload to the command: one that exits before reading it fails Claude's write, and a non-zero exit shows as a hook error. **Enforced** by `TestHookCommand_MissingFolderExitsZeroAndDrains`, `TestBriefCommand_ReadsItsInputAndExitsZero` and, for the guard's entry, `TestSettingsJSON_GuardsFlowsAgentsOnlyWhenAsked`.
- **Single-quote every path a command names, and prepare only a `SafePath` folder.** Claude runs the commands through `sh`, and a `'` in a path ends the quoting. **Enforced** by `TestPrepare_RejectsSingleQuote`.

## Pointers

- [`../CLAUDE.md`](../CLAUDE.md) — the session rules, preparing hooks on real launches among them.
- [`../../docs/ARCHITECTURE.md`](../../docs/ARCHITECTURE.md) — where this package sits.
