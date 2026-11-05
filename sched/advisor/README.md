# sched/advisor

The work scheduler's agent ([spec](../../docs/superpowers/specs/2026-11-05-smart-scheduler-design.md) §5.6): a headless `claude -p` the daemon runs, one call at a time, for two things. A **prior** estimates a command no run has measured (its cores, memory, I/O and duration, or the ID of a known command it is `like`). A **reconcile** explains, in a note of at most 300 characters, a run that surprised its belief. The model in `core` (`core/queue_advisor.go`) decides when to call, and a measured run always outweighs an answer.

## Files

- `doc.go` — the package comment.
- `settings.go` — `Settings`, `Resolve` (the `scheduler.agent` block: `enabled` true, `model` `opus`, `effort` `low`, `account` the default, `wait_seconds` 60) and its bounds.
- `env.go` — `AllowedEnv`: the build variables the advisor may see (`CARGO_*` build and profile settings, `RUSTFLAGS`, `GOFLAGS`, `CGO_ENABLED`, `MAKEFLAGS`, `CC`, the `*FLAGS`, …), less any whose name holds `TOKEN`, `SECRET`, `PASSW`, `KEY`, `CRED` or `AUTH`, each cut to 512 bytes.
- `request.go` — `PriorRequest` and `ReconcileRequest`, the JSON the call reads on stdin: the job (argv, normalized command, description, repo, folder, the allowed environment), the host (cores, RAM, caps, default claim), known beliefs, and for a reconcile the runs, the prior, the surprising run and the log's last lines.
- `prompt.go`, `prompt.md` — `SystemPrompt`.
- `schema.go` — `PriorSchema` and `NoteSchema`, passed to `--json-schema`.
- `answer.go` — `PriorAnswer`, `ParsePrior` (cores 1 to the default claim's, memory 1 to the cap, a duration of 1 s to a day, a `like` it was given) and `ParseNote`.
- `run.go` — `Call`, `Run`, and `Result` with its `Usage` (the call's wall time and the CLI's model, turns, tokens, cost and denied tool uses).
- `calllog.go` — `CallRecord`, `LoadCalls`, `SaveCalls`: the call log.

## The call

`Run` builds the command through `account.Command` (which refuses a missing account dir) and runs, in the job's folder, with the request on stdin and a 90 s timeout:

```
claude -p --model opus --effort low --system-prompt <prompt> --tools Read,Glob,Grep --restricted --safe-mode
  --strict-mcp-config --no-session-persistence --permission-mode dontAsk --output-format json --json-schema <schema>
```

`--tools` and `--restricted` leave read-only file tools confined to the working directory; `--safe-mode` turns off CLAUDE.md files, skills, plugins, hooks and custom agents; `--strict-mcp-config` starts no MCP server; `--no-session-persistence` writes no transcript. The CLI prints one JSON result; the answer is its `structured_output` (checked against Claude Code 2.1.318 on 2026-11-05). A result with `is_error`, a subtype other than `success`, no structured output, a non-zero exit or the timeout is an error.

## The call log

`<globalDir>/jobs/advisor.jsonl` ([spec](../../docs/superpowers/specs/2026-11-05-smart-scheduler-design.md) §16) holds the last 500 calls, oldest first, one `CallRecord` per line: when it ended, its kind, job, signature, repo and command, the model and effort asked for, how long it waited for its turn, its `Usage`, its outcome (`answered`, `failed` or `skipped`), the error, the answer as given, and a reconcile's surprise. The model writes it whole after each call (`SaveCalls`, atomic); `LoadCalls` never writes, and fails on a line it can't read or from a newer version, which the model then leaves as it is. No request is logged.

## Tests

`run_test.go` drives `Run` against fake `claude` scripts that save their arguments, folder and stdin. `live_test.go` (build tag `advisor_live`) makes one real call with the user's `~/.claude` (or `ADVISOR_LIVE_CLAUDE_CONFIG_DIR`): `go test -tags advisor_live ./sched/advisor -run TestLive`.
