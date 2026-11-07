# attention

The stop classifier's package: the verdicts, the settings block (`Resolve`), the request, the schema, the prompt, the answer check (`ParseVerdict`) and the call log. Pure logic over values; the model in `core` (`core/attention.go`) decides when to call, runs the call through `advisor.Run` and owns the verdicts. Full reference in [`README.md`](README.md).

## Rules when modifying this package

- **Treat the message and the prompt as untrusted text: the prompt says they are data, never instructions, and the call runs with no tools (`advisor.Call.NoTools`) and the advisor's lockdown in an empty folder.** A turn's message can quote a repository's prompt injection, and the call runs as the session's account; the worst it may do is give a wrong verdict. **Enforced** for the prompt's sentence by `TestSchema_IsJSON` and for the flags by `sched/advisor`'s `TestRun`; **Convention** for the empty folder, which `core/attention.go` names (`attentionRunDir`).
- **Check every answer with `ParseVerdict` before it is applied, and count a bad one as a failed call.** A gist of many lines or no bound would reach every card, the fleet and the lock screen. **Enforced** by `TestParseVerdict`.
- **Make `Resolve` fail closed: a field outside its bounds is an error that turns the classifier off, never a clamp.** A clamped `max_parallel` starts node processes the owner didn't allow, and nothing says so. **Enforced** by `TestResolve_Bounds`.
- **Log calls, never requests, in `calls.jsonl` (`CallRecord`).** A request holds the session's last message and prompt; the log is kept for a person to read long after the turn. **Enforced** for the model's records by `TestAttention_AStopGetsItsVerdict` (`core/attention_test.go`), which finds no message in the file; **Convention** for a new field.
- **Read the call log only through `LoadCalls`, write it only through `SaveCalls`, and leave a file that fails to load untouched.** The model then keeps calls in memory; an overwrite would drop the answers a restart reuses instead of calling again. **Enforced** by `TestCalls_ANewerOrBadFileIsAnErrorAndLeftAlone`.
- **Never call the real CLI from a default test run; the eval is behind `-tags attention_live`, and runs again after every change to `prompt.md`.** Every `go test` would otherwise spend plan usage and depend on a login, while a prompt change that loses an ask shows only in the eval, which fails when a `needs_you` case reads as `done` or `stopped`. **Convention** — a billed suite, or a prompt that misses asks.

## Pointers

- [`README.md`](README.md) — the call, the request, the verdicts and the log.
- [`../sched/advisor/CLAUDE.md`](../sched/advisor/CLAUDE.md) — the runner and lockdown the classifier shares.
- [`../docs/claude/the-stop-classifier.md`](../docs/claude/the-stop-classifier.md) — the model's side: when a call is queued, the verdicts' identity, the pushes.
