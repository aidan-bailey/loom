# attention

The stop classifier ([spec](../docs/superpowers/specs/2026-11-07-stop-verdicts-design.md)): a headless `claude -p` the daemon runs for each Claude turn that ends with the session Ready, reading the turn's last message and saying whether the session **needs you**, is **done**, is **waiting** on its own background work, or has merely **stopped**. A `needs_you` verdict alerts the TUI and the phone as a permission prompt does. The model in `core` (`core/attention.go`) decides when to call and owns the verdicts; this package is pure logic over values.

## Files

- `doc.go` — the package comment.
- `verdict.go` — the verdicts (`NeedsYou`, `Done`, `Waiting`, `Stopped`), `MaxGist` (100 characters), `Schema` (passed to `--json-schema`), `Answer` and `ParseVerdict` (one of the four, a gist of one line of at most `MaxGist` characters).
- `request.go` — `Request`, the JSON on stdin (`title`, `prompt_from`, `last_prompt` cut to `MaxPrompt` 2 KiB, `last_message`, `jobs_in_flight`), and `NewRequest`.
- `settings.go` — `Settings` and `Resolve` (the `attention` block: `enabled` true, `model` `haiku`, `effort` `low`, `max_parallel` 3 (1–8), `wait_seconds` 30 (5–120); enabled, model and effort take `advisor.ResolveBlock`'s bounds).
- `prompt.go`, `prompt.md` — `SystemPrompt`.
- `calllog.go` — `CallRecord`, `LoadCalls`, `SaveCalls`: the call log.

## The verdicts

| Verdict | Meaning | Gist |
|---|---|---|
| `needs_you` | the agent can't go on without the person: a question, a choice, a go-ahead on a concrete next step ("Shall I push?"), an action for the person, a failure it can't get past | what it asks |
| `done` | the task is complete and nothing is awaited; a generic offer of more help doesn't change that | what it finished |
| `waiting` | it ended its turn to wait for work it started (a loom job, a subagent, another session) | what it waits on |
| `stopped` | anything else | empty |

Doubt between `needs_you` and another verdict is `needs_you`; between `done` and `stopped`, `stopped`. A reply to another session's prompt is `needs_you` only when it asks the person.

## The call

`core` builds it through `advisor.Run` (`account.Command`, which refuses a missing account dir), as the session's own account, in `<globalDir>/attention/run/` (empty), with the request on stdin and `wait_seconds` as its timeout:

```
claude -p --model haiku --effort low --system-prompt <prompt> --tools "" --restricted --safe-mode
  --strict-mcp-config --no-session-persistence --permission-mode dontAsk --output-format json --json-schema <schema>
```

`--tools ""` leaves it no tool at all. The answer is the CLI's `structured_output`, checked with `ParseVerdict`. A call takes about 2 s and $0.0003 (the live eval's average at Haiku low, 16 turns, 2026-11-07).

## The call log

`<globalDir>/attention/calls.jsonl` holds the last 500 calls, oldest first, one `CallRecord` per line: when it ended, the session's ID, title and account, the Stop it judged, the model and effort asked for, how long it waited for a free slot, its `advisor.Usage`, its outcome (`answered`; `dropped`, answered for a turn no longer current; `failed`; `skipped`, never run, with the reason), the verdict and gist, and the error. It never holds the message or the prompt. The model writes it whole after each call (`SaveCalls`, atomic); `LoadCalls` never writes, and fails on a line it can't read or from a newer version, which the model then leaves as it is. The model reads its answers back at boot, so a turn a restart replays takes its verdict without a call.

## Tests

`verdict_test.go`, `settings_test.go` and `calllog_test.go` pin the answer check, the bounds and the log. `live_test.go` (build tag `attention_live`) runs every labelled turn of `testdata/eval.json` against the installed `claude` with the user's `~/.claude` (or `ATTENTION_LIVE_CLAUDE_CONFIG_DIR`), logs accuracy and cost, and fails when a `needs_you` turn reads as `done` or `stopped`: `go test -tags attention_live ./attention -run TestLive -v`. The turns are synthetic, worded as real sessions word them, since the repository is public.
