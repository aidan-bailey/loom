# Agent jobs wait out a usage limit

Feedback thread 19 (`loom feedback show 19`). Design agreed with the owner on 2026-11-11.

## Problem

A flow run's review agent hit Claude's 5-hour usage limit. Its job exited 1 after 5 seconds with `api_error_status` 429, `api_error` `usage_limit_reached` and result "You've hit your 5-hour limit · resets 4:30pm". The engine treated it as a broken agent and parked the run as agent-failed, and the manager could only retry after the reset.

The session "feedback simple fixes" (branch `aidanb/feedback-simple-fixes`, its item 1) makes `readResult` (`internal/cli/flow_agent_exec.go`) carry the result text and the API status into the agent-failed question. This design builds on that.

## Facts

- Claude's stream-json output includes `rate_limit_event` lines, for example: `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed_warning","resetsAt":1794398400,"rateLimitType":"seven_day","utilization":0.88,…,"unifiedWindows":{"five_hour":{"utilization":0.11,"resetsAt":1794357000,"status":"allowed"},"seven_day":{…}}}}` (seen in `~/.loom/jobs/3505/output.log`). `resetsAt` is Unix seconds, so nothing needs to parse "resets 4:30pm".
- Only the statuses `allowed` and `allowed_warning` are on disk. The value for a refused request (probably `rejected`) is not, and job 3316's log is gone. The authoritative signal is the result event: `is_error`, API status 429, `usage_limit_reached`. The reset time comes from the latest `rate_limit_event` (the window `rateLimitType` names, or a window in `unifiedWindows` whose status is not allowed). Probe the refused shape if you can do so cheaply and safely; otherwise accept the 429 result with the latest event's reset, and say which shape you assumed.
- The scheduler already admits agent jobs per account: `Record.AccountKey` is the job's `CLAUDE_CONFIG_DIR` ("" for the default account), and `admitWith` (`sched/admit.go`) caps running agents per key at `Policy.AgentJobs`, with a reason for each job it leaves waiting.

## Decision

### 1. One parser

A small shared function reads a stream-json log and answers whether the agent stopped on a usage limit, and if so the window and its reset time. A known limit with no reset time is still a limit, with its reset unknown. `readResult` and the daemon use the same function. Put it where both `internal/cli` and `core` can import it without a cycle.

### 2. The daemon holds the account

When an agent job (`KindAgent`) settles with a failure, the daemon reads its log through the parser. Read only the tail, and off the model's loop: core must never block its loop on a file. A usage limit with a known reset records "account K limited until T" in memory. A restart forgets it, and the next agent to hit the limit records it again. While T is in the future, admission holds every queued agent job on that account with the reason "waits for the account's usage limit (<window>, resets <local time>)". Pass the holds to admission as an argument: `Admit` stays a pure function of its arguments (`sched/CLAUDE.md`). Build jobs and run jobs aren't affected. A hold ends at T, plus a small margin such as a minute.

The board and the PWA must show that reason for the waiting step. If the reason doesn't reach them through the job views they already read, carry it there.

### 3. The engine retries on its own

The agent run's report says "usage limit" (a status of its own, not agent-failed), with the window and the reset time. The engine then:

- **Reset within 6 hours** (covers any 5-hour window): retries the step without asking. Resume the agent's session when it has one, since a limit can hit mid-run (as the turn cap does today); otherwise start it again. The queue holds the new job until the reset. Note it in the run's log: "<step>: usage limit (<window>), waiting for the reset at <time>".
- **Reset more than 6 hours away, or unknown:** parks the run with a question of its own kind, naming the window and the reset ("7-day limit, resets Tue 14:00"), with the answers `wait` (resubmit, and the queue holds it) and `stop`. The account's hold still stops other runs' agents from burning attempts meanwhile.

Make 6 hours a named constant. Add the new question kind and its answers to `internal/flowrun/answers.go`, and make sure `loom flow run answer` and the PWA can give them.

## Tests

- Parser: a 429 `usage_limit_reached` result with a preceding event gives the window and reset; with no event, a limit with its reset unknown; an `allowed_warning` event on a successful run is no limit; a plain error result is no limit.
- Admission: a hold keeps an account's queued agents waiting with the reason; other accounts and build jobs are unaffected; the hold ends at its time.
- Daemon: a settled agent job whose log says limited records the hold; one that doesn't, doesn't; no log read on the loop.
- Engine: a reset within 6 hours retries with no question; a reset further away, or unknown, parks the new question; `wait` resubmits; `stop` stops; the retry resumes the session when the report has one; a resumed engine replays it all the same way.
