# session/claudemod

The loom mod for Claude Code: a function-hook plugin (`mod/`: the manifest, `hooks/hooks.json`, the TypeScript module `hooks/register.ts`) embedded in the binary, which `session` writes into each Claude launch's hooks folder (`WriteTo`) and passes with `--plugin-dir`. The module long-polls the daemon's mod socket (the daemon's mod server), submits each message with `$.prompt.submit`, and reports `queued`, `started`, `complete` or `failed`. Design: [`../../docs/superpowers/specs/2026-10-14-loom-claude-mod-design.md`](../../docs/superpowers/specs/2026-10-14-loom-claude-mod-design.md), as amended by its plan, [`../../docs/superpowers/plans/2026-10-14-loom-claude-mod.md`](../../docs/superpowers/plans/2026-10-14-loom-claude-mod.md), whose amendments win where they differ.

## Rules when modifying this package

- **Keep the module a carrier: compose text, decide policy and judge liveness in Go (the model, in `core`), never in TypeScript.** The module ships on Claude's early-access API and nothing in CI runs it; the Go side is where the tests are. **Convention** — logic only an opt-in real-Claude run exercises.
- **Keep the module's reach to `$.env`, `$.http`, `$.prompt`, `$.session`, `$.clock` and `$.ui.log`.** It runs inside every Claude loom launches; `$.process` or `$.fs` there would make every session a host for whatever reaches the socket. **Convention**, checked by the opt-in `TestRealClaude_ValidatesTheMod` (`LOOM_TEST_REAL_CLAUDE=1`; CI never runs it), which pins the validator's call list — a module reaching further, unnoticed until someone runs it.
- **Change `Version`, the manifest's major version, `MOD_VERSION` and the `/v<n>` path together, and only for a change an older daemon or module would misread.** A mod from an older build talks to a newer daemon after an upgrade. **Enforced** for the three agreeing by `TestModule_SpeaksThisProtocol` and `TestWriteTo_WritesTheWholeMod`; the need for a bump is **Convention** — a mod and daemon misreading each other silently.
- **Embed with `all:mod`.** A plain pattern skips `.claude-plugin`, and Claude then loads no plugin at all. **Enforced** by `TestWriteTo_WritesTheWholeMod`.
- **Write the mod per launch, into the hooks folder, never into a folder sessions share.** Claude writes `tsconfig.json` and `.claude-plugin/types/` into every `--plugin-dir` folder it loads, and saving a module file in a watched folder hot-reloads the module in every session watching it. **Convention** — sessions rewriting and reloading each other's mod.
- **Run the opt-in real-Claude tests (`LOOM_TEST_REAL_CLAUDE=1`, `-run RealClaude`, here and in the daemon's mod server) after changing the module.** CI has no `claude`, so nothing else type-checks the module or drives it against Claude. **Convention** — a module Claude refuses, and every session silently without it.

## Pointers

- [`../CLAUDE.md`](../CLAUDE.md) — the launch that writes the mod (`prepareMod`).
- [`../../docs/ARCHITECTURE.md`](../../docs/ARCHITECTURE.md) — where this package sits.
