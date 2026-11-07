# PWA: a session's jobs

Date: 2026-11-07. Branch: `aidanb/session-jobs-in-pwm`.

## Goal

The phone shows the work-queue jobs a session submitted (`loom job submit` / `run` from its pane or its subagents), as the TUI's cards and work view do: which are running, queued or finished, and the tail of each one's output. Read-only: no cancel, no move to front.

Today the web layer ignores `core.QueueChanged`, so the phone sees no job at all. Every `QueueChanged` already carries the whole queue (`core.QueueView`), and each `core.JobView` names its owner session (`Owner`, an `InstanceID`, 0 for none).

## Decisions

- **Depth:** a list per session plus each job's log tail. No actions.
- **Placement (option C of the brainstorm):** a Jobs section on the session's info tab, a `⚙ job 214 +1` chip in the conversation's fact strip while jobs are live, and the same label as a badge on the fleet row. Each job opens a job screen with its log.
- **Log reads go through `Core`:** a new request, `ReadJobLog`, as the phone's other reads (`ReadTurns`, `ReadDiff`, `ReadMockup`) do. The model checks the job belongs to the session; the web layer never opens a job's file itself.

## 1. The projection: jobs on each session

`hub.apply` (`internal/web/stream.go`) takes `core.QueueChanged` and calls a new `projection.setQueue(view core.QueueView) bool` (`internal/web/fleet.go`), which wakes the flusher only when what the app reads changed.

`sessionJSON` gains three fields:

- `jobs []jobJSON`: the session's jobs, an empty list for none. Order: running (by start), then queued and held (in queue order), then finished (newest end first) — `QueueView.Jobs` is already in that order, so the projection keeps it. Finished jobs are capped at `maxFinishedJobs = 10` per session; the queue itself keeps `keep_finished` (default 50) host-wide.
- `jobs_label string`: `"⚙ job 214"` for the lowest-numbered running job, with `" +N"` for N more running, `"⚙ queued"` when it has only queued or held ones, `""` otherwise. The rule is the TUI's `ownerJobBadges` (`app/work_badge.go`), moved into `core` as `core.JobBadges(q QueueView) map[InstanceID]JobBadge` with its test, and called by both the TUI's `syncJobBadges` and the projection. The app maps nothing itself.
- `jobs_focus`: the job the label names (the lowest-numbered running one), 0 when the label names none. `core.JobBadges` returns it beside each label (`JobBadge{Label string; Focus sched.JobID}`), so the TUI and the projection agree on it.

`jobJSON`:

| Field | From | Notes |
|---|---|---|
| `id` | `JobView.ID` | |
| `state` | `JobView.State` | `held`, `queued`, `running`, `done`, `failed`, `cancelled`, `lost` |
| `description` | `JobView.Description` | `""` when none |
| `command` | `JobView.Argv` | joined with spaces, as the TUI's work view shows it (`ui/work.go`) |
| `agent` | `JobView.Agent` | the submitting subagent's id, `""` for none |
| `submitted`, `started`, `ended` | the `JobView` times | RFC 3339, `""` for a zero time |
| `exit` | `JobView.Exit` | `{code, signal}`, `null` until the job ends |
| `failure` | `JobView.Failure` | `""` for none |
| `cancel_reason` | `JobView.CancelReason` | `""` for none |
| `waiting` | `JobView.Waiting` | why a queued or held job has not started, `""` otherwise |

What the projection leaves out: the sampled `CPU`, `RSSKB` and `PeakRSSKB`, the claim and sizing, `Cwd`, `LogPath`. `QueueChanged` fires on every sample tick (the queue's revision moves with the sampled usage), so a field that moves with the samples would republish the fleet every `publishEvery`. `setQueue` builds the per-session lists and compares them with the last ones (`reflect.DeepEqual`); only a difference returns true. The phone works out a running job's elapsed time from `started`.

A job whose owner is 0, or is not a session the projection serves, is left out. A session the fleet does not show carries its jobs anyway, as it carries its other fields.

The snapshot carries the jobs on the sessions with no change of its own. All three fields are additions, so an older app reading `/api/v1` is unaffected.

## 2. The model: `Core.ReadJobLog`

`ReadJobLog(id InstanceID, job sched.JobID, after int64, req ReqID)` is a request with a read Reply, following `ReadDiff` in `core/reads.go`:

1. `admit(opReadJobLog, id, req)`; `opReadJobLog` (`"read a job log of"`) is refused only on a workspace terminal. A job outlives every status of its session, as a companion does (`opReadMockup`), so a paused or loading session's jobs stay readable.
2. The job must be in the queue with `Owner == id`; otherwise the request is refused with `fmt.Errorf("%s %s: job %d is not this session's", …)`, or `"job %d is no longer in the queue"` when retention dropped it. Both sides of the owner check are tested.
3. The read runs in a spawned job (`m.spawn(m.track(…))`), never on the loop, on the job's log path (`sched.JobDir(dir, id)/output.log`, as `JobView.LogPath` gives it).

What it reads, with `jobLogLimit = 64 KiB`:

- `after < 0` (the tail): the last `jobLogLimit` bytes; when that is not the whole file, the start moves forward past the first newline so the text starts on a line, and `cut` is true.
- `after ≥ 0`: the bytes from `after`, at most `jobLogLimit`; `cut` is false. A read that hits the limit leaves the rest for the next read (its `end` is where to continue).
- `after` past the file's size (the file was replaced): a fresh tail, with `reset` true.
- No file yet (a job that has not started): empty, `size` 0, no error.

The Reply carries `JobLog{Data []byte, Start, End, Size int64, Cut, Reset bool}`. The reader shares the file-tail logic with the TUI's `readLogEnd` (`app/work_log.go`), moved where both reach it (`sched`), with the line-start cut added for this caller only.

Wire: `go generate ./core/rpc` regenerates `core/rpc/methods_gen.go`, and `go test ./core/rpc -run TestProtocolReference -update` the protocol reference. Adding a method needs no `rpc.Protocol` bump (an older server answers `unsupported`). The procedure is `docs/claude/adding-a-core-method.md`.

## 3. The route

`GET /api/v1/sessions/{id}/jobs/{job}/log?after=<offset>` (`internal/web/reads.go`), through `l.read`:

- `id` and `job` are decimal; a malformed one is 400, an unknown session 404. `after` is optional (absent is the tail), a decimal `int64`; anything else is 400.
- 200 `{text, start, end, cut, reset}`. `text` is the bytes cleaned for display in Go (`cleanLog`): ANSI escape sequences (CSI, OSC, and lone ESC pairs) dropped, invalid UTF-8 replaced with U+FFFD, and on a line holding `\r` only what follows its last `\r` kept (a progress bar shows its last state). `\r` that ends a `\r\n` is a line end.
- The model's refusals are 409 with its text; a read past `replyWait` is 504.

The route is added to the README's API table and `apiRoutes` in `tools/webshot/canned.go` with a fixture.

## 4. The app

- **Fleet row** (`fleet.mjs`): `s.jobs_label`, when not empty, as a small badge on the row, in the accent colour.
- **Fact strip** (`strip.mjs`, on the conversation): a chip with `s.jobs_label` while it is not empty, linking to `#s/<id>/job/<s.jobs_focus>` when `jobs_focus` is set and to the info tab otherwise (`⚙ queued`).
- **Info tab** (`session.mjs`): a `Jobs N` section after Agents, in the Agents rows' style. Each row: a glyph (◐ running, ○ queued or held, ✓ done with exit 0, ✗ failed, lost or a non-zero exit, ⊘ cancelled), the description (the command when it has none), and a sub-line: `job 214 · 2m 10s` while running, `job 215 · queued · <waiting>` while waiting, `job 212 · exit 0 · 4m ago` when finished (`failure` or `cancel_reason` in place of the exit when set). Each row links to the job screen.
- **Job screen** (`#s/<id>/job/<n>`, a new `job.mjs`, its route in `screens.mjs` and `app.mjs`'s `parseRoute`):
  - The context row goes back to the info tab and is titled with the description or the command; under it the facts: state, elapsed or ended, exit, failure or cancel reason, the command, and the subagent when one submitted it.
  - The log in a `<pre>` as a text node, never HTML. It follows the bottom while the user is at the bottom, and stays put once they scroll up. A `cut` read starts with a dim line, "Earlier output not shown".
  - Polling: the first read is the tail; while the job is `running` (or `queued`/`held`, until its file appears) and the page is visible, it reads again every 2 s with `after=end`, appending; a `reset` replaces the text. When the job's state turns finished it reads once more and stops. A 504 waits for the next tick; a 409 shows the model's text in place of the log and stops.
  - The text held per job lives in a module-level map (kept to the most recent 5 jobs), so a trip to another tab returns to the same text and place, as `turns.mjs` keeps its turns.
  - A job missing from `s.jobs` (dropped from the queue, or past the 10 finished) still reads its log while the model has it; when the model refuses, the screen says "This job is no longer in the queue."

## 5. Testing

- `core`: `ReadJobLog` — the owner check both ways (a job of this session is read; one of another session and one with no owner are refused), the tail cut at a line start, `after` mid-file, the limit, `reset`, a missing file, and a job dropped by retention; the `opReadJobLog` precondition both ways (a paused session's job is read; a workspace terminal is refused). `core.JobBadges` keeps the moved test's cases.
- `internal/web`: `setQueue` — a `QueueChanged` that differs only in samples does not wake the flusher, a state or owner change does, the 10-finished cap, and a job of an unserved owner left out; the route — 200 with `text`, a 409 for another session's job, 400 for a bad `after`, 404 for an unknown session; `cleanLog` — CSI and OSC sequences, `\r` progress lines and `\r\n`, invalid UTF-8.
- The app: `node --check` on every module; `go run ./tools/webshot` with fixtures for the info tab's Jobs section, the fact-strip chip, the fleet badge and the job screen (running, with a cut tail).
- Gates: `CGO_ENABLED=0 go test ./...`, the race run, `go vet`, `gofmt -l`, and `GOOS=windows CGO_ENABLED=0 go build ./...` (the log reader touches files only, but `sched` builds for Windows).

## Out of scope

Cancel and move-to-front from the phone; a host-wide work view on the phone; job pushes (a job ending already reaches the session's Claude, whose turn ending is the existing finished push); usage figures (CPU, memory) on the phone.
