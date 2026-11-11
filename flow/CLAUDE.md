# flow

The agent flow loom ships: the `flow` Claude plugin (`plugin/`: 12 pinned agents, the owner-started skills `/flow:init`, `/flow:draft`, `/flow:run` and `/flow:retro`, the preloaded `flow:habits`, and the dispatch guide) and the tools behind `loom flow` (`lib/`, Python and bash), embedded in the binary. `session` writes a copy into each Claude launch's hooks folder (`Write`). Full reference in [`README.md`](README.md).

## Rules when modifying this package

- **Change an agent's model, effort, maxTurns, tools or cache lifetime only on the owner's instruction, and record it with its date in `flow/PROVENANCE.md`.** The roster is tuned on run evidence by an owner-started retro; a pin changed on a hunch costs every run. **Convention** — a roster that drifts from its evidence.
- **Name the agents loom's git guard checks in `lib/guarded-agents.txt` only, and give no agent a `hooks:` block.** Claude ignores `hooks` in a plugin agent, so loom's PreToolUse hook (`loom flow guard`) is the only guard, and it reads this list. **Enforced** by `lib/check-agents.sh` (an editing agent missing from the list, a listed agent without a file, an agent with `hooks:`) through `.github/workflows/flow.yml`, and by `TestGuarded_TheShippedListParsesAndNamesRealAgents`.
- **Embed with `all:plugin`, and keep `lib` plain.** A plain `plugin` pattern skips `.claude-plugin`, and Claude then loads no plugin; `all:lib` would embed a `__pycache__` a local run left. **Enforced** by `TestWrite_WritesThePluginAndToolsButNoTests`.
- **Run `bash flow/lib/test.sh` after changing anything under `flow/`.** It runs the tools' unit tests and the agent lint; the Go suite runs neither. **Enforced** in CI by `.github/workflows/flow.yml`, on changes under `flow/**`.
- **Keep client repos' names out of `flow/`: a word that names one goes in the owner's private `~/.loom/flow/banned-tokens.txt`, which `lib/check-agents.sh` reads when it exists.** Loom's repo is public, and the flow serves every repo. **Convention** for what goes in the private list; **Enforced** for the shipped list's words by `lib/check-agents.sh`.
- **Write a copy only into a dir loom owns that doesn't exist yet (`Write` refuses one that does), and remove only its own `<dir>.partial`.** A copy written over another would mix two builds' prompts and scripts, and a removal outside its own sibling could delete what loom never wrote. **Enforced** by `TestWrite_RefusesADirThatExistsAndLeavesIt`.

## Pointers

- [`../session/CLAUDE.md`](../session/CLAUDE.md) — the launch that writes the copy (`prepareFlow`).
- [`../internal/cli/CLAUDE.md`](../internal/cli/CLAUDE.md) — `loom flow` and the guard.
- [`../docs/ARCHITECTURE.md`](../docs/ARCHITECTURE.md) — where this package sits.
