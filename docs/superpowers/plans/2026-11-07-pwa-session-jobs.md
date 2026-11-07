# PWA session jobs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. The owner chose inline execution for this plan.

**Goal:** The phone shows each session's work-queue jobs (fleet badge, a chip in the conversation's strip, a Jobs section on info) and a job screen with the job's log tail.

**Architecture:** The web layer projects `core.QueueChanged` onto each `sessionJSON` (`jobs`, `jobs_label`, `jobs_focus`), comparing only the fields it sends so sample ticks don't republish. Logs are read through a new `Core.ReadJobLog` request (admit, owner check, file read in a job), served by `GET /api/v1/sessions/{id}/jobs/{job}/log`, cleaned for display in Go. The app adds `job.mjs` and polls while the job runs.

**Tech Stack:** Go (core, rpc generator, net/http), Preact + htm ES modules, webshot (headless Chromium).

Spec: `docs/superpowers/specs/2026-11-07-pwa-session-jobs-design.md`.

Rules that bind every package: commit by path only (`git commit -- <paths>`); heavy commands through `loom job run`/`submit`; never run loom itself in a pane; never edit `core/rpc/methods_gen.go` or `docs/specs/protocol.md` by hand.

---

### Package 1: the model — `core.JobBadges`, `sched` log reads, `Core.ReadJobLog`

**Files:**
- Create: `sched/logread.go`, `sched/logread_test.go`
- Create: `core/job_badges.go`, `core/job_badges_test.go` (from `app/work_badge.go`, `app/work_badge_test.go`)
- Create: `core/job_log.go`, `core/job_log_test.go`
- Modify: `app/work_badge.go` (call `core.JobBadges`), `app/work_badge_test.go` (removed: its cases move), `app/work_log.go` (call `sched.ReadLogEnd`)
- Modify: `core/iface.go` (declare), `core/loop_core.go` (forward), `core/requests.go` (`opReadJobLog`, its precondition, `deliverTracked`/`outcome` cases), `core/model.go` (`Deliver` case), `core/events.go` (`Reply.JobLog`)
- Regenerate: `core/rpc/methods_gen.go`, `docs/specs/protocol.md`
- Test: `core/requests_test.go` (`TestRequests_RefuseWhatTheTUIRefuses` case)

- [ ] **Step 1: `sched/logread.go`.** Move `readLogEnd` from `app/work_log.go` as `ReadLogEnd(path string, limit int64) (data []byte, partial bool, err error)` (body unchanged; a missing file is no data, no error). Add:

```go
// LogRead is a read of a job's output.log: Data is the bytes from Start to
// End; Size the file's size when read. Cut says the read began past the
// file's start, moved forward to a line start; Reset says the offset asked
// for lay past the file's end, so this is a fresh tail.
type LogRead struct {
	Data       []byte
	Start, End int64
	Size       int64
	Cut, Reset bool
}

// ReadLog reads path's tail (after < 0: the last limit bytes, cut forward
// past the first newline when that is not the whole file) or the bytes from
// after (at most limit). An after past the file's size reads a fresh tail
// with Reset. A missing file is an empty read.
func ReadLog(path string, after, limit int64) (LogRead, error)
```

Implementation: open; `fs.ErrNotExist` → `LogRead{}`, nil; stat; if `after > size` → read tail and set `Reset`; tail: `start = max(0, size-limit)`, read `[start,size)`, when `start > 0` drop through the first `\n` (if none, keep all) and set `Cut`, `Start = start + dropped`; from offset: read `[after, min(size, after+limit))`. `End = Start + len(Data)`.

Tests (`sched/logread_test.go`, temp files): a missing file is empty; a small file's tail is whole, not cut; a long file's tail starts after a newline with `Cut` and `Start` at that line; `after` mid-file returns the rest; `after` with more than `limit` remaining returns `limit` bytes and `End` to continue from; `after > size` gives `Reset` and the tail; `ReadLogEnd` keeps its current behaviour (copy its existing test cases if any from `app/`).

- [ ] **Step 2: run** `loom job run -d "sched logread tests" -- env CGO_ENABLED=0 go test ./sched -run 'TestReadLog'` — fails until written, passes after.

- [ ] **Step 3: `core.JobBadges`.** Move `ownerJobs`/`ownerJobBadges` to `core/job_badges.go`:

```go
// JobBadge is one session's work-queue badge: Label "⚙ job 14" for its
// lowest running job, with " +N" for N more running, or "⚙ queued" when it
// has only queued or held ones; Focus the job the label names, 0 for none.
type JobBadge struct {
	Label string
	Focus sched.JobID
}

// JobBadges is every owning session's badge, keyed by InstanceID. A session
// with no running, queued or held job, and a job with no owner, get none.
func JobBadges(q QueueView) map[InstanceID]JobBadge
```

`app/work_badge.go` keeps `syncJobBadges`, building `map[core.InstanceID]string` from the labels for `ui.SetJobBadges`. Move the test to `core/job_badges_test.go` asserting `JobBadge{Label, Focus}` (Focus = lowest running ID; 0 for "⚙ queued").

- [ ] **Step 4: `Core.ReadJobLog`.** In `core/iface.go`, beside `ReadMockup`:

```go
	ReadJobLog(id InstanceID, job sched.JobID, after int64, req ReqID)
```

`core/loop_core.go`: `func (l *Loop) ReadJobLog(id InstanceID, job sched.JobID, after int64, req ReqID) { l.do(func(m *Model) { m.ReadJobLog(id, job, after, req) }) }`.

`core/requests.go`: `opReadJobLog requestOp = "read a job log of"`; precondition: refused only on a workspace terminal (as `opReadMockup`). `core/events.go`: `Reply.JobLog sched.LogRead` (doc comment's list updated).

`core/job_log.go`:

```go
// jobLogLimit is the most a ReadJobLog answers at once.
const jobLogLimit = 64 << 10

// jobLogResult is ReadJobLog's job result.
type jobLogResult struct {
	read sched.LogRead
	err  error
}

// ReadJobLog reads job's output.log for session id in a job (sched.ReadLog:
// the tail when after < 0, else from after) and answers with the Reply's
// JobLog. The job must be in the queue and owned by id; a job outlives its
// session's status, so only a workspace terminal is refused.
func (m *Model) ReadJobLog(id InstanceID, job sched.JobID, after int64, req ReqID) {
	inst, _, ok := m.admit(opReadJobLog, id, req)
	if !ok {
		return
	}
	q := m.queue
	i := -1
	if q.ready() {
		i = q.index(job)
	}
	if i < 0 {
		m.refuse(req, id, fmt.Errorf("%s %s: job %d is no longer in the queue", opReadJobLog, inst.Title, job))
		return
	}
	if InstanceID(q.store.Jobs[i].Owner) != id {
		m.refuse(req, id, fmt.Errorf("%s %s: job %d is not this session's", opReadJobLog, inst.Title, job))
		return
	}
	path := filepath.Join(q.jobDir(job), sched.LogFile)
	m.spawn(m.track(req, id, func() any {
		r, err := sched.ReadLog(path, after, jobLogLimit)
		return jobLogResult{read: r, err: err}
	}))
}
```

Add `jobLogResult` to `deliverTracked` (`r.JobLog = res.read`), `outcome` (`return r.err, nil`) and `Deliver`'s "only its Reply carries it" case.

- [ ] **Step 5: core tests** (`core/job_log_test.go`, on `wqModel` with records appended as the queue tests do through `wqEnded`, and instances from `modInst`): the owner's job is read (write `output.log` under `sched.JobDir`, run the held job, check `Reply.JobLog.Data`); another session's job is refused "is not this session's"; an unowned job likewise; an unknown job "is no longer in the queue"; a paused session's job is read. Add to `TestRequests_RefuseWhatTheTUIRefuses`: `{"read a job log of a terminal", func(f fixture) { f.m.ReadJobLog(f.id["term"], 1, -1, 1) }, "read a job log of term: not allowed on a workspace terminal"}`. Look at how `core/reads_test.go` runs a spawned job and reads its Reply, and copy that harness.

- [ ] **Step 6: wire.** `go generate ./core/rpc`, then `go test ./core/rpc -run TestProtocolReference -update`. No `rpc.Protocol` bump.

- [ ] **Step 7: run** `loom job run -d "core sched app rpc tests" -- env CGO_ENABLED=0 go test ./core/... ./sched ./app` — PASS.

- [ ] **Step 8: commit** `feat(core,sched,rpc): ReadJobLog reads a session's job log; JobBadges moves to core`, by path.

### Package 2: the web layer — projection and route

**Files:**
- Create: `internal/web/jobs.go`, `internal/web/jobs_test.go`
- Modify: `internal/web/fleet.go` (`sessionJSON` fields; `projection` holds `jobs map[core.InstanceID]sessionJobs`; `sessionOf` callers fill them), `internal/web/stream.go` (`hub.apply` case), `internal/web/api.go` (route), `internal/web/README.md` (route table, session fields, `jobs.go` row)

- [ ] **Step 1: `internal/web/jobs.go`.**

```go
// maxFinishedJobs is how many finished jobs a session carries.
const maxFinishedJobs = 10

// exitJSON is a finished job's exit.
type exitJSON struct {
	Code   int    `json:"code"`
	Signal string `json:"signal,omitempty"`
}

// jobJSON is one job of a session's, as the app reads it: no sampled usage,
// so a sample tick changes nothing here (spec §1).
type jobJSON struct {
	ID           sched.JobID `json:"id"`
	State        string      `json:"state"`
	Description  string      `json:"description"`
	Command      string      `json:"command"`
	Agent        string      `json:"agent"`
	Submitted    string      `json:"submitted"`
	Started      string      `json:"started"`
	Ended        string      `json:"ended"`
	Exit         *exitJSON   `json:"exit"`
	Failure      string      `json:"failure"`
	CancelReason string      `json:"cancel_reason"`
	Waiting      string      `json:"waiting"`
}

// sessionJobs is one session's jobs and badge.
type sessionJobs struct {
	jobs  []jobJSON
	label string
	focus sched.JobID
}

// jobsOf groups the queue's jobs by owner, in the view's order, keeping at
// most maxFinishedJobs finished ones each; jobs with no owner are left out.
func jobsOf(q core.QueueView) map[core.InstanceID]sessionJobs
```

`command` is `strings.Join(j.Argv, " ")`. A job is finished unless its state is `running`, `queued` or `held`. Badges from `core.JobBadges(q)`.

`projection.setQueue(q core.QueueView) bool`: `next := jobsOf(q)`; if `reflect.DeepEqual(next, p.jobs)` return false; store and return true. `sessionOf` (or the place the projection builds each `sessionJSON`) sets `Jobs` (an empty non-nil slice for none), `JobsLabel`, `JobsFocus` from `p.jobs[id]`. In `fleet.go`'s `sessionJSON` add:

```go
	// Jobs are the session's work-queue jobs (jobs.go): running, waiting,
	// then up to maxFinishedJobs finished; JobsLabel and JobsFocus its badge
	// (core.JobBadges), "" and 0 for none.
	Jobs      []jobJSON   `json:"jobs"`
	JobsLabel string      `json:"jobs_label"`
	JobsFocus sched.JobID `json:"jobs_focus"`
```

`hub.apply`: `case core.QueueChanged: if h.fleet.setQueue(ev.View) { moved = true }`.

- [ ] **Step 2: the route.** In `jobs.go`:

```go
// jobLogJSON is a read of a job's log as the app reads it.
type jobLogJSON struct {
	Text  string `json:"text"`
	Start int64  `json:"start"`
	End   int64  `json:"end"`
	Cut   bool   `json:"cut"`
	Reset bool   `json:"reset"`
}

// readJobLog answers GET /api/v1/sessions/{id}/jobs/{job}/log?after=: the
// job's output from after (the tail when absent), cleaned for display.
func (l *Layer) readJobLog(w http.ResponseWriter, r *http.Request, device string)
```

It uses `readSession`, parses `{job}` (`strconv.ParseUint`, > 0, else 400 `job "<s>": want a job id`) and `after` (absent → -1; else `strconv.ParseInt`, ≥ 0, else 400 `after "<s>": want a byte offset`), then `l.read(w, r, a, func(req core.ReqID) { l.client.ReadJobLog(id, job, after, req) })`, records ok and writes `jobLogOf(reply.JobLog)`. Register in `routeAPI` after the mockup route: `m.Handle("GET /api/v1/sessions/{id}/jobs/{job}/log", l.authed(l.readJobLog))`.

`cleanLog(b []byte) string`: drop `ESC [` … final byte `0x40–0x7E` (CSI), `ESC ]` … `BEL` or `ESC \` (OSC), and any other `ESC` with its next byte; `strings.ToValidUTF8(…, "�")`; then per line (split on `\n`, a trailing `\r` of a line removed first), keep what follows the line's last `\r`.

- [ ] **Step 3: tests** (`internal/web/jobs_test.go`): `jobsOf` — order kept, unowned left out, 11 finished cut to 10, label and focus from `JobBadges`; `setQueue` — a second view differing only in `CPU`/`RSSKB`/`PeakRSSKB`/`UsedCPU` returns false, a state change returns true, an owner change returns true; `cleanLog` — `"\x1b[31mred\x1b[0m"` → `"red"`, OSC title stripped, `"10%\r50%\r100%\n"` → `"100%\n"`, `"a\r\nb"` → `"a\nb"`, `"\xff"` → `"�"`; the route on `apiEnv` — a bad `job` and a bad `after` are 400, an unknown session 404, a job not in the queue 409 with the model's text (`is no longer in the queue`); `jobLogOf` maps every field.

- [ ] **Step 4: README.** Add `jobs.go` to the files table; the route row to the API table; `jobs`, `jobs_label`, `jobs_focus` where the session's fields are described.

- [ ] **Step 5: run** `loom job run -d "web tests" -- env CGO_ENABLED=0 go test ./internal/web/...` — PASS.

- [ ] **Step 6: commit** `feat(web): a session's jobs in the fleet and GET …/jobs/{job}/log`, by path.

### Package 3: the app and webshot

**Files:**
- Create: `internal/web/static/js/job.mjs`
- Modify: `internal/web/static/js/app.mjs` (`parseRoute`: `#s/<id>/job/<n>` → `{name: "job", id, job}`), `screens.mjs` (`job: { Screen: Job, Head: JobHead }`), `fleet.mjs` (`Row`: `s.jobs_label` in meta as `<span class="sc-job">`), `strip.mjs` (`FactStrip` gains a sibling job link; the strip becomes a `div` holding the info link and the job link, as `AgentStrip` holds two parts), `session.mjs` (`Jobs` section after `Agents`, exported helpers `jobGlyph`, `jobLine`), `internal/web/static/app.css` (`.sc-job`, `.sc-log`, strip layout)
- Modify: `tools/webshot/fixture/snapshot.json` (session 1 gets jobs), create `tools/webshot/fixture/job-log.json`, `tools/webshot/canned.go` (`apiRoutes` entry `^/api/v1/sessions/\d+/jobs/\d+/log$`), `tools/webshot/main.go` (`{"job", "#s/1/job/214"}` screen)

- [ ] **Step 1: helpers in `session.mjs`.**

```js
// jobGlyph is a job's mark by state and exit: ◐ running, ○ waiting, ✓ done
// with exit 0, ⊘ cancelled, ✗ anything else that ended.
export function jobGlyph(j) {
  if (j.state === "running") return "◐";
  if (j.state === "queued" || j.state === "held") return "○";
  if (j.state === "cancelled") return "⊘";
  if (j.state === "done" && j.exit && j.exit.code === 0) return "✓";
  return "✗";
}

// elapsed is "2m 10s" from iso to now (or to end).
export function elapsed(from, to = Date.now()) { … }

// jobLine is a job row's sub-line.
export function jobLine(j, now) {
  const head = "job " + j.id;
  if (j.state === "running") return head + " · " + elapsed(j.started, now);
  if (j.state === "queued" || j.state === "held") return [head, j.state, j.waiting].filter(Boolean).join(" · ");
  const why = j.failure || j.cancel_reason || (j.exit ? (j.exit.signal || "exit " + j.exit.code) : j.state);
  return [head, why, j.ended ? ago(j.ended, now) + " ago" : ""].filter(Boolean).join(" · ");
}
```

`Jobs({ jobs, sessionId, now })` draws `Jobs N` and rows linking to `#s/<id>/job/<n>`, in the Agents rows' classes. `Info` renders it after Agents when `s.jobs.length > 0`.

- [ ] **Step 2: `job.mjs`.** `JobHead`: `ContextRow` back `{href: "#s/<id>/info", label: "info"}`, title the job's description or command (`"job <n>"` when the session's list lacks it), `SessionTabs` with `current="info"`. `Job`: reads `GET /api/v1/sessions/<id>/jobs/<n>/log` (tail first, then `?after=<end>` every 2 s while the job's state is running/queued/held and `document.visibilityState === "visible"`; one more read when the state turns finished, then stop), appends `text` (replaces on `reset`), keeps `{text, end, cut}` per `id/job` in a module-level `Map` capped at 5 entries, draws facts (state, `elapsed`, exit/failure/cancel reason, command, agent) and `<pre class="sc-log">${text}</pre>` (a text child, never HTML), with "Earlier output not shown" when `cut`. It follows the bottom when the user was within 40 px of it before the append. A 409 shows the model's text (or "This job is no longer in the queue.") and stops; a 504 or status 0 waits for the next tick.

- [ ] **Step 3: strip and fleet.** `FactStrip` returns `<div class="sc-strip sc-strip-split">` with `<a class="sc-strip-main" href="#s/<id>/info">…facts…</a>` and, when `s.jobs_label`, `<a class="sc-strip-job" href=${s.jobs_focus ? "#s/" + s.id + "/job/" + s.jobs_focus : "#s/" + s.id + "/info"}>${s.jobs_label}</a>`. Keep the old single-link look when there is no label. `Row` pushes `html\`<span class="sc-job">${s.jobs_label}</span>\`` into meta.

- [ ] **Step 4: CSS.** `.sc-job, .sc-strip-job { color: var(--accent); }`, `.sc-strip-split { display: flex; gap: 8px; }`, `.sc-strip-main { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }`, `.sc-log { font-family: var(--mono); font-size: 12px; white-space: pre-wrap; word-break: break-all; background: var(--code); padding: 8px; border-radius: var(--radius); }`. No `style` attributes anywhere.

- [ ] **Step 5: webshot.** Session 1 in `snapshot.json` gets `"jobs"` (a running 214 started 2m ago with `waiting: ""`, a queued 215 `waiting: "after 214"`, a done 212 exit 0), `"jobs_label": "⚙ job 214"`, `"jobs_focus": 214`; other sessions `"jobs": [], "jobs_label": "", "jobs_focus": 0`. `job-log.json`: `{"text": "<~30 lines of go test output>", "start": 1024, "end": 4096, "cut": true, "reset": false}`. Add the route and the `job` screen.

- [ ] **Step 6: check.** `for f in internal/web/static/js/*.mjs; do node --check "$f"; done`; `loom job run -d "webshot" -- go run ./tools/webshot -out /tmp/claude-webshot-jobs -screens fleet,conversation,info,job` — exit 0, no console errors or misses; look at each PNG. Also `CGO_ENABLED=0 go test ./internal/web/ ./tools/webshot/` (static tests: no inline style, sw precache).

- [ ] **Step 7: commit** `feat(web): jobs on the fleet, the strip, info and a job screen`, by path.

### Package 4: gates and docs

- [ ] **Step 1:** `gofmt -l $(git ls-files '*.go' | grep -v '^vendor/')` — empty.
- [ ] **Step 2:** `loom job submit -d "full tests" -- env CGO_ENABLED=0 go test ./...` and `loom job submit -d "race" -- env CC=clang CGO_ENABLED=1 go test -race ./core/... ./internal/web/... ./sched ./app` and `loom job submit -d "vet+windows" -- sh -c 'CGO_ENABLED=0 go vet ./... && GOOS=windows CGO_ENABLED=0 go build ./...'` — all PASS. golangci-lint skipped (local v2 vs CI v1).
- [ ] **Step 3:** `go run ./tools/claudemd -v` — within budgets. Update `docs/claude/web-layer.md` only if something learned would have saved time.
- [ ] **Step 4:** commit any doc touch-ups by path.
