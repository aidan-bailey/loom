# sched/advisor

The work scheduler's agent: a headless `claude -p` that gives a prior estimate for a command no run has measured and a note on a run that surprised its belief, with the filter that keeps secrets out of what it sees and the checks on what it answers. The model in `core` decides when to call it. Full reference in [`README.md`](README.md).

## Rules when modifying this package

- **Filter every environment value through `AllowedEnv` before it reaches a request.** A secret in a job's environment would otherwise leave the host inside the prompt, and nothing would say so. **Enforced** by `TestAllowedEnv`.
- **Keep the call's lockdown (`--tools Read,Glob,Grep`, `--restricted`, `--safe-mode`, `--strict-mcp-config`, `--no-session-persistence`, `--permission-mode dontAsk`) and run it in the job's folder.** A job's description, files and log are untrusted: without the lockdown a prompt-injected job could run code or write files with the user's account, and a working directory other than the job's folder would let Read reach other jobs' `spec.json`, which hold their environments. **Enforced** for the flags and the folder by `TestRun`.
- **Check every answer (`ParsePrior`, `ParseNote`) before it is stored, and treat a bad one as a failed call.** An answer outside the caps would admit work the host cannot hold. **Enforced** by `TestParsePrior` and `TestParseNote`.
- **Log calls, never requests, in `advisor.jsonl` (`CallRecord`).** A request holds the job's argv, description and filtered environment; the log is kept for a person to read and is not filtered like a prompt. **Convention** — a secret in a command line or description written to a long-lived file.
- **Never call the real CLI from a default test run; the contract test is behind `-tags advisor_live`.** Every `go test` would otherwise spend plan usage and depend on a login. **Convention** — a slow, flaky and billed suite.

## Pointers

- [`README.md`](README.md) — the call, the requests and the answers.
- [`../CLAUDE.md`](../CLAUDE.md) — the scheduler package this one serves.
- [`../../docs/claude/the-work-queue.md`](../../docs/claude/the-work-queue.md) — the model's side: when the advisor is called, and what it changes.
