# Dependent Jobs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A job can name one prerequisite (`loom job submit|run --after N`): it is held until that job ends, joins the queue at its place in line when it succeeds, and is cancelled down its chain when it doesn't, with one message per chain.

**Architecture:** `sched` gains the `held` state, `Record.After` and `Record.Ready`, a starvation clock that starts when a job joins the queue, simulated releases in `Forecast`, a prerequisite part in `Signature`, and a `beliefs.json` version set by content. `core` checks the prerequisite at submit, releases and cascades inside a job's settle (`settleChain`, in the new `core/queue_chain.go`), resolves held jobs at boot before adoption, and sends the chain's messages. The CLI, the work view and loom's Claude context show and teach it.

**Tech Stack:** Go (module `github.com/aidan-bailey/loom`), testify, cobra, Bubble Tea v2 / lipgloss v2.

**Spec:** [`docs/superpowers/specs/2026-11-05-dependent-jobs-design.md`](../specs/2026-11-05-dependent-jobs-design.md). Execution: inline (superpowers:executing-plans), no checkpoint pauses; one commit per package.

---

## Ground rules for every package

- Scratch files go in the session scratchpad, never the worktree. Commit by path: `git add <paths>`, then `git commit -m … -- <paths>`; never `-a`, `git add -A` or `git add .`.
- Quick checks run directly: `CGO_ENABLED=0 go build ./...`, `CGO_ENABLED=0 go vet ./<pkg>/...`, and one package's tests by name (`CGO_ENABLED=0 go test ./sched/ -run TestX`). `go test ./core/` and anything wider go through loom's work queue, with Bash's `run_in_background`:
  - `loom job run -d "dependent jobs: core" -- env CGO_ENABLED=0 go test ./core/`
  - `loom job run -d "dependent jobs: full suite" -- env CGO_ENABLED=0 go test ./...`
  - `loom job run -d "dependent jobs: race" -- env CC=clang CGO_ENABLED=1 go test -race ./...`
  - `loom job run -d "dependent jobs: e2e" -- env CGO_ENABLED=0 go test -tags e2e ./e2e/...`
- Format with `gofmt -w` on the files touched (never `gofmt -w .`); check with `gofmt -l sched core internal ui app`.
- CLAUDE.md budgets: `go run ./tools/claudemd -v`. `core/CLAUDE.md` is full (19971/20000 bytes), so the model's rules go in `docs/claude/the-work-queue.md`; `sched/CLAUDE.md` has room (12147/20000).
- golangci-lint: the dev shell's v2 rejects the config; run `CGO_ENABLED=0 go vet ./...` and say lint was skipped.
- **The subagent job results branch** (`aidanb/loom-doesnt-know-subagents`) also edits `core/queue_notice.go` (it adds `rec.Agent` to `buildNoticeJob`) and `sched/job.go` (`Record.Agent`). If it has merged to `main` by the time Package 2 starts, merge `main` into this branch first (`git merge main`), and in Step 2.5 pass `rec.Agent` through `sendNotice` to `buildNoticeJob` as that branch's `sendJobNotice` does.

## Spec amendments made while planning

Recorded in the spec in Step 1.1:

1. **§5.1, the refusal names how the prerequisite ended**, in the cascade's words (`endedHow`): `job 14 failed (exit 101), so a job after it would never run`, or `was killed by SIGSEGV`, `could not start`, `was cancelled`, `was lost`.
2. **§5.2, `MoveJobToFront` also moves the jobs held down the moved job's chain**: they take the same `Order`, so the view lists them where they will run. The release's lower-of rule still holds.
3. **§5.4, `cancelOwnerJobs` marks every job of the owners (no message, the reason) before it ends any**, so a cascade set off inside a pause keeps the pause's reason and sends nothing.
4. **§5.5, a job the cascade cancels whose news an earlier job's message reports is recorded `carried` too**, `its result goes with job 14's`.
5. **§5.7, the load check (`resolveHeld`) runs before `adoptJobs`, not after it**: `adoptJobs`' settles end in `afterSettle`, which runs an admission pass, so a check placed after it would come after a pass. `adoptJobs` releases and cascades through `settledJob`. The check cancels the held jobs that carry a cancel request first, since a release would otherwise take one back into the queue along with its siblings.
6. **§6, the follower's start line measures the wait from `Ready`**: unchanged for every job but a released one, whose line reads `job 15 started 2s after job 14 ended`. `list` has no `--json`, so the sentence about it goes.
7. **§4, `sched.State.Waiting`** (queued or held) is added, for the places that treat both alike.

## Files

| File | Package | Responsibility |
|---|---|---|
| `sched/job.go` | 1 | `Held`, `State.Waiting`, `NoticeCarried`, `Record.After`, `Record.Ready`, `Record.QueuedSince`, `Submit.After` and its bound |
| `sched/admit.go` | 1 | the starvation guard reads `QueuedSince` |
| `sched/forecast.go` | 1 | held jobs in the simulation, `releaseHeld` |
| `sched/signature.go` | 1 | `Signature.After`, `AfterNone`, `ID` |
| `sched/beliefs_store.go` | 1 | `BeliefsVersion` 2, `Beliefs.fileVersion` |
| `sched/size.go` | 1 | `joinAnd` exported as `JoinAnd` |
| `sched/beliefs_view.go`, `sched/belief_text.go` | 1 | `BeliefView.After`, `afterLine` |
| `sched/advisor/request.go`, `sched/advisor/prompt.md` | 1 | `advisor.After`, `Job.After`, the prompt's line |
| `sched/*_test.go` | 1 | the tests of Steps 1.2–1.8 |
| `sched/README.md`, `sched/CLAUDE.md` | 1 | facts and two rules |
| `core/queue_chain.go` (new) | 2 | the prerequisite check, release, cascade, the chain's messages, `resolveHeld`, `heldWaiting`, `advisorAfter` |
| `core/queue_chain_test.go` (new) | 2 | the model's tests |
| `core/queue_ops.go` | 2 | `submitJob`, `cancelJob`, `moveJobToFront`, `cancelOwnerJobs`, `settledJob` |
| `core/queue_notice.go` | 2 | `sendNotice`, `jobHead`, `noticeHead(rec, after)`, `afterText`, `overClaimLine` |
| `core/queue.go` | 2 | `resolveHeld` in `loadQueue` |
| `core/queue_beliefs.go`, `core/queue_advisor.go` | 2 | held jobs in live sizing and the asks; `readBeliefs`' `After`; the advisor's `after` |
| `core/queue_view.go` | 2 | `JobView.After`, `.Ready`, `.QueuedSince`; held jobs among the waiting |
| `core/queue_notice_test.go`, `core/queue_watch_test.go` | 2 | `noticeHead`'s new argument; `wqStop` ends held jobs |
| `docs/claude/the-work-queue.md` | 2 | rule 10 |
| `internal/cli/job.go`, `job_follow.go`, `job_sizing.go`, `job_list.go`, `job_beliefs.go`, `job_test.go` | 3 | `--after`, the held line, the start line, `list`, the beliefs command text, tests |
| `ui/work.go`, `ui/work_test.go` | 3 | held rows, section, tail and time |
| `app/work.go`, `app/work_badge.go`, `app/work_test.go`, `app/work_badge_test.go` | 3 | the cancel prompt, `t`, the badge |
| `session/loom_context_shared.md` | 3 | the context's sentence |
| `USAGE.md` | 3 | `--after` |

## Package 1: `sched`

### Step 1.1: Amend the spec

- [ ] **Edit `docs/superpowers/specs/2026-11-05-dependent-jobs-design.md`:**
  - §4, after the **Held** bullet, add: "**Waiting:** `sched.State.Waiting`, true for `queued` and `held`, for the places that treat both alike (cancel at once, move to the front, live sizing, the advisor's asks, the view's waiting jobs) (added while planning)."
  - §5.1, the third refusal bullet becomes: "job N ended other than `done`, named as the cascade names it (§5.4): `job 14 failed (exit 101), so a job after it would never run`, or `was killed by SIGSEGV`, `could not start`, `was cancelled`, `was lost` (amended while planning: the same words as the cascade's reasons)."
  - §5.2, the third bullet becomes: "`MoveJobToFront` accepts a held job as well as a queued one, and sets the `Order` one below the lowest `Order` of every queued and held job, for the job and every job held down its chain, so the view lists the chain where it will run and a job someone moves to the front stays ahead of a chain released after the move (amended while planning: the chain moves with it)."
  - §5.4, the `cancelOwnerJobs` bullet becomes: "`cancelOwnerJobs` (a pause, a kill or a reset someone requests) first marks every job of the owners (no message, the reason), then ends the queued and held ones at once, so a cascade one of them sets off finds the jobs after it marked, and they keep the pause's reason and send nothing (amended while planning)."
  - §5.5, the cascade's first sub-bullet gains: "Each job the cascade cancelled is then recorded `carried`, `its result goes with job 14's` (amended while planning)." Its second sub-bullet gains: "A job a message above it reports is recorded `carried` by that message's job."
  - §5.7, the second bullet becomes: "`loadQueue` checks the held jobs before `adoptJobs` (amended while planning: `adoptJobs`' settles end in `afterSettle`, which runs an admission pass, so a check after it would come after a pass), in ID order, so a chain resolves from its first job down, and the held jobs that carry a cancel request first (a release would otherwise take one back into the queue with its siblings); `adoptJobs` then settles through `settledJob`, so a prerequisite that ended while the daemon was down releases or cascades there. A held job is:" (the sub-bullets stay).
  - §6, the follower bullet's start-line sentence becomes: "Its start line measures the wait from `Ready` (unchanged for every job but a released one, whose line reads `job 15 started 2s after job 14 ended`)." The `list` bullet drops "`--json` carries `JobView.After` and `JobView.Ready`." (amended while planning: `list` has no `--json`).
  - §9: add "`sched.State.Waiting`; `sched.JoinAnd` (the exported `joinAnd`, for the cascade's and the cancel prompt's lists); `core.JobView.QueuedSince`."

### Step 1.2: The held state, `After` and `Ready`

**Files:** Modify `sched/job.go`, `sched/job_test.go`.

- [ ] **Write the tests** (append to `sched/job_test.go`; add `"time"` to its imports):

```go
func TestState_HeldWaitsAndHasNotEnded(t *testing.T) {
	assert.False(t, Held.Finished(), "a held job waits for its prerequisite")
	for state, want := range map[State]bool{
		Held: true, Queued: true, Running: false, Done: false, Failed: false, Cancelled: false, Lost: false,
	} {
		assert.Equal(t, want, state.Waiting(), string(state))
	}
}

func TestRecord_QueuedSince(t *testing.T) {
	t0 := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	assert.Equal(t, t0, Record{Submitted: t0}.QueuedSince(), "a record from an earlier build has no Ready")
	assert.Equal(t, t0.Add(time.Minute), Record{Submitted: t0, Ready: t0.Add(time.Minute)}.QueuedSince(),
		"a released job joined the queue at its release")
}

func TestSubmit_ValidateRefusesANegativeAfter(t *testing.T) {
	s := Submit{Argv: []string{"true"}, Cwd: "/work", Description: "x", Tail: 20, After: -2}
	assert.EqualError(t, s.Validate(), "--after is -2: it must be a job ID")
	s.After = 0
	assert.NoError(t, s.Validate(), "0 is none")
	s.After = 14
	assert.NoError(t, s.Validate())
}
```

- [ ] **Run them:** `CGO_ENABLED=0 go test ./sched/ -run 'TestState_HeldWaitsAndHasNotEnded|TestRecord_QueuedSince|TestSubmit_ValidateRefusesANegativeAfter'`. Expected: FAIL to compile (`undefined: Held`, `Waiting`, `QueuedSince`, `After`).

- [ ] **Implement** in `sched/job.go`:
  - The states block becomes:

```go
// The states of a job. Held is a job waiting for its prerequisite
// (Record.After) to end; Done is exit 0; Failed is a non-zero exit, a signal
// or a start that failed; Cancelled is a job cancelled by anyone, whatever
// its exit; Lost is a job whose shim vanished without writing its result.
const (
	Held      State = "held"
	Queued    State = "queued"
	Running   State = "running"
	Done      State = "done"
	Failed    State = "failed"
	Cancelled State = "cancelled"
	Lost      State = "lost"
)
```

  - After `State.Finished`, add:

```go
// Waiting reports whether a job in state s has yet to start: queued, or held
// after its prerequisite (dependent jobs spec §4).
func (s State) Waiting() bool { return s == Queued || s == Held }
```

  - The notice fates become:

```go
// The fates of a notice: none due or yet, sent, recorded as undelivered
// (with the reason in Record.NoticeWhy), or carried: another job's message
// reports it (dependent jobs spec §5.5), named in NoticeWhy.
const (
	NoticeNone        Notice = ""
	NoticeSent        Notice = "sent"
	NoticeUndelivered Notice = "undelivered"
	NoticeCarried     Notice = "carried"
)
```

  - In `Record`, after the `Adopted` field, add:

```go

	// Dependent jobs (spec 2026-11-05-dependent-jobs §4): the job this one
	// runs after (0 for none), and when it joined the queue: at its submit,
	// or at its release; zero while it is held, and on a record from an
	// earlier build (QueuedSince).
	After JobID     `json:"after,omitempty"`
	Ready time.Time `json:"ready,omitzero"`
```

  - After the `Record` type, add:

```go
// QueuedSince is when r joined the queue: Ready, or Submitted on a record
// with none (a held job, or one from a build before dependent jobs).
func (r Record) QueuedSince() time.Time {
	if r.Ready.IsZero() {
		return r.Submitted
	}
	return r.Ready
}
```

  - In `Submit`, after `Launch`, add `After JobID `json:"after,omitempty"` // the prerequisite (--after); 0 for none`.
  - In `Validate`'s comment, the last sentence before "It does not check" gains: "; the prerequisite is not negative (0 is none)". At the end of `Validate`, before `return nil`:

```go
	if s.After < 0 {
		return fmt.Errorf("--after is %d: it must be a job ID", s.After)
	}
```

- [ ] **Run the tests again.** Expected: PASS. Also `CGO_ENABLED=0 go test ./sched/ -run 'TestState|TestSubmit'` stays green.

### Step 1.3: The starvation clock starts when a job joins the queue

**Files:** Modify `sched/admit.go`, `sched/admit_test.go`.

- [ ] **Write the test** (append to `sched/admit_test.go`):

```go
// heldJob is a job held after job after, submitted waited ago.
func heldJob(id, after JobID, claim Claim, waited time.Duration) Record {
	return Record{ID: id, Order: int64(id), State: Held, After: after, Claim: claim, Submitted: admitNow.Add(-waited)}
}

// releasedJob is a queued job whose chain was submitted waited ago and that
// joined the queue ago.
func releasedJob(id JobID, claim Claim, waited, ago time.Duration) Record {
	r := queuedJob(id, claim, waited)
	r.Ready = admitNow.Add(-ago)
	return r
}

func TestAdmit_StarvationCountsFromReady(t *testing.T) {
	runAdmitCases(t, []admitCase{
		{
			name: "a job released a minute ago is no starving head, though its chain began half an hour ago",
			jobs: []Record{
				runningJob(1, Claim{CPUs: 6, MemoryGB: 6}),
				releasedJob(2, admitBig, 30*time.Minute, time.Minute),
				queuedJob(3, admitSmall, time.Minute),
			},
			start:   []JobID{3},
			waiting: map[JobID]string{2: "needs 10 cores (8 free)"},
		},
		{
			name: "a job released past the starvation time starves as any job does",
			jobs: []Record{
				runningJob(1, Claim{CPUs: 6, MemoryGB: 6}),
				releasedJob(2, admitBig, 30*time.Minute, 10*time.Minute),
				queuedJob(3, admitSmall, time.Minute),
			},
			waiting: map[JobID]string{
				2: "needs 10 cores (8 free)",
				3: "waits for job 2, which has waited past 10m",
			},
		},
		{
			name: "a held job is never admitted, never the head and never said to wait",
			jobs: []Record{
				heldJob(1, 9, admitSmall, time.Hour),
				queuedJob(2, admitSmall, time.Minute),
			},
			start: []JobID{2},
		},
	})
}
```

- [ ] **Run it:** `CGO_ENABLED=0 go test ./sched/ -run TestAdmit_StarvationCountsFromReady`. Expected: FAIL on the first case (job 3 "waits for job 2, which has waited past 10m": the guard counts from the submit).
- [ ] **Implement** in `sched/admit.go`: in `Admit`'s comment, item 2 becomes "Once it has waited p.StarveAfter since it joined the queue (Record.QueuedSince: its release, for a dependent), Admit starts it if it fits and starts nothing else, so smaller jobs cannot pass a large one forever.", and the closing paragraph gains "A held job is never seen: only Queued and Running records count." The guard's line becomes:

```go
	if head := line[0]; now.Sub(head.QueuedSince()) >= p.StarveAfter {
```

- [ ] **Run `CGO_ENABLED=0 go test ./sched/ -run TestAdmit`.** Expected: PASS (the existing tables have no `Ready`, so they count from the submit as before).

### Step 1.4: The forecast releases held jobs

**Files:** Modify `sched/forecast.go`, `sched/forecast_test.go`.

- [ ] **Write the test** (append to `sched/forecast_test.go`):

```go
func TestForecast_ReleasesHeldJobs(t *testing.T) {
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	p := Policy{Caps: Caps{CPUs: 8, MemoryGB: 16, IOJobs: 2}, DefaultClaim: Claim{CPUs: 4, MemoryGB: 6}, StarveAfter: time.Hour}
	four := Claim{CPUs: 4, MemoryGB: 4}
	jobs := []Record{
		{ID: 1, Order: 1, State: Running, Claim: four, Started: now.Add(-time.Minute)}, // usual 3m: ends at +2m
		{ID: 3, Order: 3, State: Running, Claim: four, Started: now},                   // usual 10m: ends at +10m
		{ID: 7, Order: 7, State: Queued, Claim: four, Submitted: now},                  // behind 9 and 10, which keep job 1's place: starts at +10m
		{ID: 9, Order: 1, State: Held, After: 1, Claim: four, Submitted: now},          // released at +2m and starts; usual 5m: ends at +7m
		{ID: 10, Order: 1, State: Held, After: 9, Claim: four, Submitted: now},         // released at +7m and starts
	}
	usual := map[JobID]time.Duration{1: 3 * time.Minute, 3: 10 * time.Minute, 7: 5 * time.Minute, 9: 5 * time.Minute, 10: 5 * time.Minute}
	got := Forecast(jobs, p, now, func(r Record) (time.Duration, bool) { d, ok := usual[r.ID]; return d, ok }, time.Hour)
	assert.Equal(t, map[JobID]time.Time{
		9:  now.Add(2 * time.Minute),
		10: now.Add(7 * time.Minute),
		7:  now.Add(10 * time.Minute),
	}, got)

	t.Run("a job held after one being sized gets no start", func(t *testing.T) {
		jobs := []Record{
			{ID: 1, Order: 1, State: Queued, Claim: four, Sizing: SizingAsking, Submitted: now},
			{ID: 2, Order: 1, State: Held, After: 1, Claim: four, Submitted: now},
		}
		assert.Empty(t, Forecast(jobs, p, now, func(Record) (time.Duration, bool) { return time.Minute, true }, time.Minute))
	})
}
```

- [ ] **Run it:** `CGO_ENABLED=0 go test ./sched/ -run TestForecast_ReleasesHeldJobs`. Expected: FAIL (got `{7: +2m}`: held jobs are left out).
- [ ] **Implement** in `sched/forecast.go`:
  - `Forecast`'s comment gains, after "Jobs being sized are left out.": "A held job joins the simulated queue when its prerequisite's expected end comes, at the lower of its order and its prerequisite's, as a release does (dependent jobs spec §5.3); one being sized is left out too, and one whose prerequisite never ends in the simulation gets no start."
  - The record loop's second case becomes:

```go
		case r.State.Waiting() && r.Sizing == "":
			sim = append(sim, r)
```

  - The loop's stop test becomes:

```go
		if !slices.ContainsFunc(sim, func(r Record) bool { return r.State.Waiting() }) || len(ends) == 0 {
			break
		}
```

  - The simulated end becomes:

```go
		sim[index(e.id)].State = Done
		releaseHeld(sim, e.id, clock)
```

  - Add after `Forecast`:

```go
// releaseHeld is a release in the simulation (dependent jobs spec §5.3):
// each held job after job id, which has just ended, joins the queue at at,
// at the lower of its order and id's.
func releaseHeld(sim []Record, id JobID, at time.Time) {
	i := slices.IndexFunc(sim, func(r Record) bool { return r.ID == id })
	if i < 0 {
		return
	}
	order := sim[i].Order
	for j := range sim {
		if sim[j].State == Held && sim[j].After == id {
			sim[j].State, sim[j].Ready, sim[j].Order = Queued, at, min(sim[j].Order, order)
		}
	}
}
```

- [ ] **Run `CGO_ENABLED=0 go test ./sched/ -run TestForecast`.** Expected: PASS.

### Step 1.5: A signature carries its prerequisite's

**Files:** Modify `sched/signature.go`, `sched/signature_test.go`.

- [ ] **Write the test** (append to `sched/signature_test.go`):

```go
func TestSignature_IDWithAPrerequisite(t *testing.T) {
	cold := NewSignature("/r/.git", ".", []string{"make"})
	assert.Equal(t, SignatureID("4e0b9c350d02"), cold.ID(), "a signature with no prerequisite keeps the ID it always had")

	warm := cold
	warm.After = "1a2b3c4d5e6f"
	assert.NotEqual(t, cold.ID(), warm.ID(), "a prerequisite makes it another signature")
	other := cold
	other.After = "0f0f0f0f0f0f"
	assert.NotEqual(t, warm.ID(), other.ID(), "and each prerequisite another again")
	none := cold
	none.After = AfterNone
	assert.NotEqual(t, cold.ID(), none.ID(), "a prerequisite with no signature still counts")
}
```

- [ ] **Run it:** `CGO_ENABLED=0 go test ./sched/ -run TestSignature_IDWithAPrerequisite`. Expected: FAIL to compile (`warm.After undefined`). Before the change, comment out every line after the first assertion once and run it: the pinned ID `4e0b9c350d02` must pass against today's code; restore the lines.
- [ ] **Implement** in `sched/signature.go`:
  - `SignatureID`'s comment becomes "SignatureID names a signature: the first 12 hex digits of the SHA-256 of the JSON array [repo, folder, command, environment pairs], and the prerequisite's signature after them when there is one."
  - `Signature`'s comment gains "; and for a dependent job, its prerequisite's signature (dependent jobs spec §5.6)", and its fields gain:

```go
	After   SignatureID `json:"after,omitempty"` // the prerequisite's signature, AfterNone for one with none; "" for no prerequisite
```

  - After the type, add:

```go
// AfterNone is Signature.After for a job whose prerequisite has no signature.
const AfterNone SignatureID = "none"
```

  - `ID` becomes:

```go
// ID is s's ID. No pairs and an empty list of pairs are one signature, and
// After is hashed only when it is set, so a signature with no prerequisite
// keeps the ID it always had.
func (s Signature) ID() SignatureID {
	env := s.Env
	if env == nil {
		env = []string{}
	}
	parts := []any{s.Repo, s.Folder, s.Command, env}
	if s.After != "" {
		parts = append(parts, s.After)
	}
	data, err := json.Marshal(parts)
	if err != nil {
		panic(err) // strings and slices of them: Marshal cannot fail
	}
	sum := sha256.Sum256(data)
	return SignatureID(hex.EncodeToString(sum[:])[:12])
}
```

- [ ] **Run `CGO_ENABLED=0 go test ./sched/ -run TestSignature`.** Expected: PASS.

### Step 1.6: `beliefs.json`'s version follows its content

**Files:** Modify `sched/beliefs_store.go`, `sched/belief_test.go`.

- [ ] **Write the test** (append to `sched/belief_test.go`; add `"encoding/json"` to its imports), and in `TestBeliefsStore_RoundTripAndRefusals` change the `"newer"` case's body to `{"version": 3, "beliefs": []}`:

```go
func TestBeliefsStore_VersionFollowsThePrerequisiteParts(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "jobs")
	version := func() int {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dir, BeliefsFile))
		require.NoError(t, err)
		var head struct {
			Version int `json:"version"`
		}
		require.NoError(t, json.Unmarshal(data, &head))
		return head.Version
	}
	var bs Beliefs
	build := bs.Ensure(NewSignature("/r/.git", ".", []string{"cargo", "test", "--no-run"}), bt0).ID
	require.NoError(t, SaveBeliefs(dir, bs))
	assert.Equal(t, 1, version(), "no prerequisite part: an older loom reads it")

	warm := NewSignature("/r/.git", ".", []string{"cargo", "test"})
	warm.After = build
	bs.Ensure(warm, bt0)
	require.NoError(t, SaveBeliefs(dir, bs))
	assert.Equal(t, 2, version(), "an older loom would compute this belief's ID without its prerequisite")
	got, err := LoadBeliefs(dir)
	require.NoError(t, err)
	assert.Equal(t, bs.Beliefs, got.Beliefs, "this build reads it back")

	bs.Forget(warm.ID())
	require.NoError(t, SaveBeliefs(dir, bs))
	assert.Equal(t, 1, version(), "back to 1 once no belief has one")
}
```

- [ ] **Run them:** `CGO_ENABLED=0 go test ./sched/ -run TestBeliefsStore`. Expected: FAIL (version 1 after the warm belief; and `LoadBeliefs` refuses the file at version 2 once `SaveBeliefs` writes it).
- [ ] **Implement** in `sched/beliefs_store.go`: the constants become

```go
// The registry's file, and the newest version of it this build reads. It
// writes version 2 only while a belief's signature has a prerequisite part
// (dependent jobs spec §5.6), and 1 otherwise (fileVersion), so an older
// loom keeps reading a registry with none: it would compute such a belief's
// ID without the part and refuse the whole file.
const (
	BeliefsFile    = "beliefs.json"
	BeliefsVersion = 2
)
```

  `SaveBeliefs`' comment ends "and stamps its version (fileVersion)", its `bs.Version = BeliefsVersion` becomes `bs.Version = bs.fileVersion()`, and after it add:

```go
// fileVersion is the version bs is written as: 2 while a belief's signature
// has a prerequisite part, else 1.
func (bs Beliefs) fileVersion() int {
	for _, b := range bs.Beliefs {
		if b.After != "" {
			return 2
		}
	}
	return 1
}
```

- [ ] **Run `CGO_ENABLED=0 go test ./sched/ -run TestBeliefsStore`.** Expected: PASS.

### Step 1.7: `JoinAnd`, and the belief text's `after` line

**Files:** Modify `sched/size.go`, `sched/beliefs_view.go`, `sched/belief_text.go`, `sched/beliefs_view_test.go`.

- [ ] **Rename** `joinAnd` to `JoinAnd` in `sched/size.go` (its definition and its one call, in the same file: `grep -n joinAnd sched/*.go` must then show none); its comment becomes "JoinAnd joins words as "a", "a and b", "a, b and c".".
- [ ] **Write the test** (append to `sched/beliefs_view_test.go`; add `"strings"` to its imports if it lacks it):

```go
func TestFormatBelief_NamesTheCommandItRunsAfter(t *testing.T) {
	sig := NewSignature("/r/.git", ".", []string{"cargo", "test"})
	text := func(after SignatureID, command []string) []string {
		s := sig
		s.After = after
		v := ViewBelief(Belief{ID: s.ID(), Signature: s}, Policy{})
		v.After = command
		return FormatBelief(v)
	}
	assert.Contains(t, text("1a2b3c4d5e6f", []string{"cargo", "test", "--no-run"}), "after     cargo test --no-run")
	assert.Contains(t, text(AfterNone, nil), "after     a job with no signature")
	assert.Contains(t, text("1a2b3c4d5e6f", nil), "after     belief 1a2b3c4d5e6f, since forgotten")
	for _, line := range text("", nil) {
		assert.False(t, strings.HasPrefix(line, "after"), "no prerequisite, no line: %q", line)
	}
	assert.Equal(t, "a, b and c", JoinAnd([]string{"a", "b", "c"}))
}
```

- [ ] **Run it:** `CGO_ENABLED=0 go test ./sched/ -run TestFormatBelief_NamesTheCommandItRunsAfter`. Expected: FAIL to compile (`v.After undefined`).
- [ ] **Implement:**
  - `sched/beliefs_view.go`: `BeliefView`'s comment gains "and the command it runs after", and its fields gain:

```go
	// After is the command of the belief this one's signature runs after
	// (dependent jobs spec §5.6). The caller sets it, since it holds the
	// registry; nil when the signature has no prerequisite part, the
	// prerequisite had no signature, or that belief is gone.
	After []string
```

  - `sched/belief_text.go`: in `FormatBelief`, after `lines = append(lines, "command   "+command)`, add

```go
	if line := afterLine(v); line != "" {
		lines = append(lines, line)
	}
```

  and after `FormatBelief` add:

```go
// afterLine is the belief text's line on the command its command runs after
// (dependent jobs spec §5.6), "" for one with no prerequisite part.
func afterLine(v BeliefView) string {
	switch after := v.Belief.After; {
	case after == "":
		return ""
	case after == AfterNone:
		return "after     a job with no signature"
	case v.After != nil:
		return "after     " + strings.Join(v.After, " ")
	}
	return fmt.Sprintf("after     belief %s, since forgotten", v.Belief.After)
}
```

  `FormatBelief`'s comment: "its signature, claim, …" becomes "its signature, the command it runs after, claim, …".
- [ ] **Run `CGO_ENABLED=0 go test ./sched/...`.** Expected: PASS.

### Step 1.8: The advisor is told what ran before

**Files:** Modify `sched/advisor/request.go`, `sched/advisor/prompt.md`.

- [ ] **Implement:** in `request.go`, `Job` gains

```go
	After       *After   `json:"after,omitempty"` // the job this one runs after (dependent jobs spec §5.6)
```

  and after `Job` add:

```go
// After is the job a job runs after, as the advisor is told of it: its
// command, as its belief normalizes it (its argv when it has none), and its
// description.
type After struct {
	Command     []string `json:"command"`
	Description string   `json:"description"`
}
```

  In `prompt.md`, after the "like:" and "note:" bullets of the prior task (before "Reason from the measurements…"), add the paragraph:

```
A job with "after" runs right after that command, in the same folder, and usually reuses what it made: estimate the run of what it built (a test run's memory, not its build's) unless its own command must build too.
```

- [ ] **Run `CGO_ENABLED=0 go test ./sched/advisor/`.** Expected: PASS (the field is new and optional; `TestRun` builds requests without it).

### Step 1.9: Docs, gates and commit

- [ ] **`sched/README.md`:**
  - Files: `job.go` gains `Held`, `State.Waiting`, `NoticeCarried`, `Record.QueuedSince`; `signature.go` gains `AfterNone`; `size.go` names `JoinAnd`.
  - Jobs: the first paragraph becomes "A job is `held` while it waits for its prerequisite (`Record.After`, dependent jobs spec), then `queued`, then `running`, then one of four finished states: … `State.Finished` is true for the last four, and `State.Waiting` for `held` and `queued`." Add after the `Order` paragraph: "`Ready` is when a job joined the queue: at its submit, or at its release for a dependent; `QueuedSince` falls back on `Submitted` for a held job and for a record from an earlier build."
  - Admission: "once the head, …, has waited `StarveAfter` since it was submitted" becomes "since it joined the queue (`QueuedSince`)", and add "A held job is never seen."
  - Signatures: the **Signature** bullet gains "A dependent's signature also carries its prerequisite's (`After`; `AfterNone` when that one had none), hashed only when set, so every other ID is what it always was."
  - The forecast: add "A held job joins the simulated queue at its prerequisite's expected end, at the lower of the two orders (`releaseHeld`)."
  - On disk, `beliefs.json`: "`{"version": 1, …}`" becomes "`{"version": N, …}`, N being 2 while a belief's signature has a prerequisite part and 1 otherwise, so an older loom still reads a registry with none".
- [ ] **`sched/CLAUDE.md`**, add after the `Size`/`Usual` rule:

```
- **Measure the starvation guard's wait from `Record.QueuedSince` (`Ready`, else `Submitted`), and never admit a held job.** A released dependent's chain may have begun long before the dependent joined the queue: counted from its submit it is a starving head at once, which starts alone or holds every other job back, and nothing says why. **Enforced** by `TestAdmit_StarvationCountsFromReady` (`sched/admit_test.go`).
- **Hash a signature's `After` only when it is set, and write `beliefs.json` as version 2 only while a belief has one (`Beliefs.fileVersion`).** An ID that changed for a signature with no prerequisite orphans every command's history; an older loom computes a dependent's ID without its part and refuses the whole file, while a version it doesn't read is left untouched with a reason. **Enforced** by `TestSignature_IDWithAPrerequisite` (its pinned ID) and `TestBeliefsStore_VersionFollowsThePrerequisiteParts`.
```

- [ ] **Gates:** `gofmt -l sched` (empty); `CGO_ENABLED=0 go build ./...`; `CGO_ENABLED=0 go vet ./sched/...`; `CGO_ENABLED=0 go test ./sched/...`; `go run ./tools/claudemd` (clean).
- [ ] **Commit:**

```bash
git add docs/superpowers/specs/2026-11-05-dependent-jobs-design.md sched/job.go sched/job_test.go sched/admit.go sched/admit_test.go sched/forecast.go sched/forecast_test.go sched/signature.go sched/signature_test.go sched/beliefs_store.go sched/belief_test.go sched/size.go sched/beliefs_view.go sched/beliefs_view_test.go sched/belief_text.go sched/advisor/request.go sched/advisor/prompt.md sched/README.md sched/CLAUDE.md
git commit -m "feat(sched): held jobs, a starvation clock from Ready, and signatures that know their prerequisite

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>" -- docs/superpowers/specs/2026-11-05-dependent-jobs-design.md sched
```

## Package 2: the model (`core`)

### Step 2.1: The chain's code

**Files:** Create `core/queue_chain.go`.

- [ ] **Write `core/queue_chain.go`:**

```go
package core

import (
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/sched/advisor"
)

// Dependent jobs (docs/superpowers/specs/2026-11-05-dependent-jobs-design.md):
// a job submitted --after another is held until that one ends. The end
// releases the jobs held after it when it is done and cancels them, down the
// chain, when it ended any other way; both happen inside its settle
// (settledJob, settleChain), before any admission pass.

// chainLink is one job a cascade cancelled, with the job it waited on.
type chainLink struct {
	id, after sched.JobID
}

// heldAfter is the IDs of the held jobs that wait on job id, in ID order.
func (q *queueState) heldAfter(id sched.JobID) []sched.JobID {
	var ids []sched.JobID
	for _, r := range q.store.Jobs {
		if r.State == sched.Held && r.After == id {
			ids = append(ids, r.ID)
		}
	}
	slices.Sort(ids)
	return ids
}

// heldChain is the IDs of every job held down job id's chain: those that
// wait on it, those that wait on them, and so on, in ID order.
func (q *queueState) heldChain(id sched.JobID) []sched.JobID {
	var out []sched.JobID
	for next := []sched.JobID{id}; len(next) > 0; {
		var more []sched.JobID
		for _, n := range next {
			more = append(more, q.heldAfter(n)...)
		}
		out = append(out, more...)
		next = more
	}
	slices.Sort(out)
	return out
}

// prerequisiteOf is the record a submission names with --after (spec §5.1),
// a copy, or nil for none (after 0). It refuses a job it has no record of, a
// job with another owner than the submitter's (owner 0, a plain shell, is
// an owner too), and a job that ended other than done.
func (q *queueState) prerequisiteOf(after sched.JobID, owner InstanceID) (*sched.Record, error) {
	if after == 0 {
		return nil, nil
	}
	i := q.index(after)
	if i < 0 {
		return nil, fmt.Errorf("there is no job %d", after)
	}
	p := q.store.Jobs[i]
	switch {
	case InstanceID(p.Owner) != owner:
		return nil, fmt.Errorf("job %d has another owner: --after takes a job with the same owner as the new one", after)
	case p.State.Finished() && p.State != sched.Done:
		return nil, fmt.Errorf("job %d %s, so a job after it would never run", after, endedHow(p))
	}
	return &p, nil
}

// afterPart is the prerequisite part of a dependent's signature (spec
// §5.6): its prerequisite's signature, or sched.AfterNone when it has none.
func afterPart(prereq sched.Record) sched.SignatureID {
	if prereq.Signature == "" {
		return sched.AfterNone
	}
	return prereq.Signature
}

// endedHow is how a job that did not succeed ended, as a refusal and a
// cascade's reason say it: "failed (exit 101)", "was killed by SIGSEGV",
// "could not start", "was cancelled" or "was lost".
func endedHow(r sched.Record) string {
	switch {
	case r.State == sched.Cancelled:
		return "was cancelled"
	case r.State == sched.Lost:
		return "was lost"
	case startFailed(r.State, r.Failure, r.Exit):
		return "could not start"
	case r.Exit != nil && r.Exit.Signal != "":
		return "was killed by " + r.Exit.Signal
	case r.Exit != nil:
		return fmt.Sprintf("failed (exit %d)", r.Exit.Code)
	}
	return "failed"
}

// cascadeReason is why root's end cancels the jobs held after it (spec
// §5.4): "because job 14 failed (exit 101)", and so on (endedHow).
func cascadeReason(root sched.Record) string {
	return fmt.Sprintf("because job %d %s", root.ID, endedHow(root))
}

// settleChain does what rec's end does to the jobs held after it, and sends
// rec's message (spec §5.3–§5.5). Done, it releases them, and its message
// goes with theirs (carried); any other end cancels them down the chain, and
// one message per branch says so. With none held after it, rec's message
// goes as it always did.
func (m *Model) settleChain(rec sched.Record) {
	now := time.Now()
	if rec.State == sched.Done {
		if released := m.releaseDependents(rec, now); len(released) > 0 {
			m.carry(rec.ID, released)
			return
		}
		m.sendJobNotice(rec)
		return
	}
	if links := m.cascade(rec, now); len(links) > 0 {
		m.sendCascadeNotices(rec, links)
		return
	}
	m.sendJobNotice(rec)
}

// releaseDependents releases the held jobs that wait on rec, which ended
// done (spec §5.3): each joins the queue now, at the lower of its order and
// rec's, so it keeps its chain's place in line. It returns their IDs in ID
// order.
func (m *Model) releaseDependents(rec sched.Record, now time.Time) []sched.JobID {
	q := m.queue
	ids := q.heldAfter(rec.ID)
	for _, id := range ids {
		d := &q.store.Jobs[q.index(id)]
		d.State, d.Ready, d.Order = sched.Queued, now, min(d.Order, rec.Order)
	}
	if len(ids) > 0 {
		q.moved()
	}
	return ids
}

// cascade cancels the held jobs that wait on root, which ended other than
// done (spec §5.4), and the jobs held after those, depth first in ID order.
// Each keeps a reason it already has (cancelOwnerJobs marks them first) and
// otherwise gets the one its prerequisite's end gives. It sends no message
// (sendCascadeNotices does) and returns what it cancelled, each with the job
// it waited on, in the order it cancelled them: a job always after the job
// it waited on.
func (m *Model) cascade(root sched.Record, now time.Time) []chainLink {
	q := m.queue
	var links []chainLink
	var walk func(parent sched.JobID, reason string)
	walk = func(parent sched.JobID, reason string) {
		for _, id := range q.heldAfter(parent) {
			d := &q.store.Jobs[q.index(id)]
			if d.CancelReason == "" {
				d.CancelReason = reason
			}
			endJob(d, sched.Cancelled, now)
			links = append(links, chainLink{id: id, after: parent})
			walk(id, fmt.Sprintf("because job %d was cancelled", id))
		}
	}
	walk(root.ID, cascadeReason(root))
	if len(links) > 0 {
		q.moved()
	}
	return links
}

// wantsNotice reports whether rec would send its owner a message now: it
// asked for one, has an owner, and its notice has no fate yet.
func wantsNotice(rec sched.Record) bool {
	return rec.Notify && rec.Owner != 0 && rec.Notice == sched.NoticeNone
}

// carry records that job id's message goes with the messages of the jobs by
// (spec §5.5), when it would send one. The settle's save writes it.
func (m *Model) carry(id sched.JobID, by []sched.JobID) {
	q := m.queue
	i := q.index(id)
	if i < 0 || !wantsNotice(q.store.Jobs[i]) {
		return
	}
	q.store.Jobs[i].Notice, q.store.Jobs[i].NoticeWhy = sched.NoticeCarried, "its result goes with "+jobsPossessive(by)
	q.moved()
}

// jobsPossessive is "job 15's", or "jobs 15 and 16's".
func jobsPossessive(ids []sched.JobID) string {
	words := make([]string, len(ids))
	for i, id := range ids {
		words[i] = strconv.FormatInt(int64(id), 10)
	}
	if len(words) == 1 {
		return "job " + words[0] + "'s"
	}
	return "jobs " + sched.JoinAnd(words) + "'s"
}

// sendCascadeNotices sends the messages of the cascade root's end set off
// (spec §5.5), one per branch, each carrying root's log. root sends its own
// when it notifies, with a line naming every job the cascade cancelled, each
// of which it then carries. Otherwise each job the cascade cancelled that
// notifies sends one, naming root's end and the jobs below it, unless a job
// above it already does, which then carries it.
func (m *Model) sendCascadeNotices(root sched.Record, links []chainLink) {
	q := m.queue
	parent := make(map[sched.JobID]sched.JobID, len(links))
	for _, l := range links {
		parent[l.id] = l.after
	}
	// below is the cascade's jobs under top, in ID order.
	below := func(top sched.JobID) []sched.JobID {
		var out []sched.JobID
		for _, l := range links {
			for p, ok := l.after, true; ok; p, ok = parent[p] {
				if p == top {
					out = append(out, l.id)
					break
				}
			}
		}
		slices.Sort(out)
		return out
	}
	reporter := map[sched.JobID]sched.JobID{}
	if wantsNotice(root) {
		reporter[root.ID] = root.ID
	}
	for _, l := range links {
		if rep := reporter[l.after]; rep != 0 {
			reporter[l.id] = rep
			m.carry(l.id, []sched.JobID{rep})
			continue
		}
		if wantsNotice(q.store.Jobs[q.index(l.id)]) {
			reporter[l.id] = l.id
		}
	}
	if reporter[root.ID] == root.ID {
		m.sendNotice(root, m.jobHead(root)+"\n"+m.cancelledLine(below(root.ID)), root.ID)
	}
	for _, l := range links {
		if reporter[l.id] != l.id {
			continue
		}
		rec := q.store.Jobs[q.index(l.id)]
		head := fmt.Sprintf("[loom] job %d %q was cancelled because job %d %q %s.", rec.ID, rec.Description, root.ID, root.Description, endedHow(root))
		if under := below(l.id); len(under) > 0 {
			head += "\n" + m.cancelledLine(under)
		}
		m.sendNotice(rec, head, root.ID)
	}
}

// cancelledLine names the jobs a cascade cancelled below a message's job
// (spec §5.5): `Job 15 "run tests", which waited on it, was cancelled.`, or
// `Jobs 15 "run tests" and 16 "bench", which waited on it, were cancelled.`
func (m *Model) cancelledLine(ids []sched.JobID) string {
	q := m.queue
	names := make([]string, len(ids))
	for i, id := range ids {
		names[i] = fmt.Sprintf("%d %q", id, q.store.Jobs[q.index(id)].Description)
	}
	if len(names) == 1 {
		return "Job " + names[0] + ", which waited on it, was cancelled."
	}
	return "Jobs " + sched.JoinAnd(names) + ", which waited on it, were cancelled."
}

// resolveHeld settles, at boot, every held job whose prerequisite a previous
// daemon left settled or never recorded (spec §5.7), in ID order, so a chain
// resolves from its first job down. First it cancels, down their chains, the
// held jobs a cancel was asked for (an older loom's pause can ask without
// ending one), so no release below takes one of them back into the queue.
// Then a held job is released when its prerequisite is done, and cancelled,
// down its chain, when that ended any other way or when it names no record
// (an older loom's save drops `after`). One whose prerequisite is queued,
// held or running stays held. loadQueue runs it before adoptJobs, whose
// settles release and cancel through settledJob, and whose afterSettle runs
// an admission pass.
func (m *Model) resolveHeld() {
	q := m.queue
	now := time.Now()
	changed := false
	// held is the held jobs match selects, in ID order.
	held := func(match func(sched.Record) bool) []sched.JobID {
		var ids []sched.JobID
		for _, r := range q.store.Jobs {
			if r.State == sched.Held && match(r) {
				ids = append(ids, r.ID)
			}
		}
		slices.Sort(ids)
		return ids
	}
	// end cancels held job id with reason (a reason it has stands), unless
	// a cascade above it has ended it.
	end := func(id sched.JobID, reason string) {
		i := q.index(id)
		if i < 0 || q.store.Jobs[i].State != sched.Held {
			return
		}
		if q.store.Jobs[i].CancelReason == "" {
			q.store.Jobs[i].CancelReason = reason
		}
		endJob(&q.store.Jobs[i], sched.Cancelled, now)
		m.settledJob(i)
		changed = true
	}
	for _, id := range held(func(r sched.Record) bool { return r.CancelReason != "" }) {
		end(id, "")
	}
	for _, id := range held(func(sched.Record) bool { return true }) {
		i := q.index(id)
		if q.store.Jobs[i].State != sched.Held {
			continue // released or cancelled above
		}
		p := -1
		if after := q.store.Jobs[i].After; after != 0 {
			p = q.index(after)
		}
		switch {
		case p < 0:
			end(id, "because its prerequisite is unknown")
		case q.store.Jobs[p].State == sched.Done:
			m.releaseDependents(q.store.Jobs[p], now)
			changed = true
		case q.store.Jobs[p].State.Finished():
			end(id, cascadeReason(q.store.Jobs[p]))
		}
	}
	if changed {
		m.saveQueueQuiet("boot")
	}
}

// heldWaiting is why a held job waits (spec §6): "after job 14 (running)",
// with the prerequisite's state, or "(gone)" when its record is.
func (q *queueState) heldWaiting(r sched.Record) string {
	state := "gone"
	if i := q.index(r.After); i >= 0 {
		state = string(q.store.Jobs[i].State)
	}
	return fmt.Sprintf("after job %d (%s)", r.After, state)
}

// advisorAfter is what the advisor is told of rec's prerequisite (spec
// §5.6): its command as its belief normalizes it (its argv when it has no
// belief) and its description; nil for a job with no prerequisite, or one
// whose prerequisite's record is gone.
func (m *Model) advisorAfter(rec sched.Record) *advisor.After {
	if rec.After == 0 {
		return nil
	}
	i := m.queue.index(rec.After)
	if i < 0 {
		return nil
	}
	p := m.queue.store.Jobs[i]
	command := slices.Clone(p.Argv)
	if b := m.queue.beliefOf(p.Signature); b != nil {
		command = slices.Clone(b.Command)
	}
	return &advisor.After{Command: command, Description: p.Description}
}
```

  It compiles once Step 2.4 adds `sendNotice` and `jobHead`; the package builds at the end of Step 2.4.

### Step 2.2: Submitting, cancelling, moving and pausing

**Files:** Modify `core/queue_ops.go`.

- [ ] **`submitJob`:** its comment gains, after "the caps,": "checks the prerequisite a --after names (prerequisiteOf; dependent jobs spec §5.1),". Replace the block from `policy, paused := m.refreshPolicy()` through `rec.ID, rec.Order, rec.Owner = id, int64(id), uint64(owner)` with:

```go
	policy, paused := m.refreshPolicy()
	if paused != "" {
		return 0, "", fmt.Errorf("admission paused: %s", paused)
	}
	owner, unknown := m.jobOwner(s.Launch)
	prereq, err := q.prerequisiteOf(s.After, owner)
	if err != nil {
		return 0, "", err
	}
	now := time.Now()
	sig, unsized := jobSignature(s.Argv, s.Cwd)
	rec := sched.Record{
		Description: strings.TrimSpace(s.Description), Argv: slices.Clone(s.Argv), Cwd: s.Cwd,
		Notify: s.Notify, Tail: s.Tail, State: sched.Queued, Submitted: now, Ready: now,
		Asked: s.Claim, Unsized: unsized,
	}
	if prereq != nil {
		// A dependent (spec §5.1): held while its prerequisite waits or runs,
		// queued at once after one already done; its signature carries its
		// prerequisite's (§5.6).
		rec.After = prereq.ID
		if !prereq.State.Finished() {
			rec.State, rec.Ready = sched.Held, time.Time{}
		}
		if sig != nil {
			sig.After = afterPart(*prereq)
		}
	}
	if sig != nil {
		rec.Signature = sig.ID()
	}
	q.sizeRecord(&rec, policy)
	if err := sched.CheckClaim(rec.Claim, policy); err != nil {
		return 0, "", err
	}
	if sig != nil && m.askForPrior(s.Claim, q.beliefOf(rec.Signature), now) {
		rec.Sizing = sched.SizingAsking
	}

	id = max(q.store.NextID, 1)
	rec.ID, rec.Order, rec.Owner = id, int64(id), uint64(owner)
	if rec.State == sched.Held {
		rec.Order = prereq.Order // its prerequisite's place in line (§5.2)
	}
```

- [ ] **`cancelJob`:** its comment's "a queued job is cancelled at once" becomes "a queued or held job is cancelled at once, and the jobs held after it with it (dependent jobs spec §5.4)". In its body, `queued := rec.State == sched.Queued` becomes `waiting := rec.State.Waiting()`, and the two uses of `queued` become `waiting`.
- [ ] **`moveJobToFront`:** replace the function with:

```go
// moveJobToFront puts queued or held job id ahead of every other waiting
// job: its order, and the order of every job held down its chain (dependent
// jobs spec §5.2), becomes one below the lowest waiting order. Anything but
// a waiting job is refused.
func (m *Model) moveJobToFront(id sched.JobID) error {
	q := m.queue
	if !q.ready() {
		return errQueueNotLoaded
	}
	i := q.index(id)
	switch {
	case i < 0:
		return fmt.Errorf("there is no job %d", id)
	case !q.store.Jobs[i].State.Waiting():
		return fmt.Errorf("job %d isn't waiting", id)
	}
	lowest := q.store.Jobs[i].Order
	for _, rec := range q.store.Jobs {
		if rec.State.Waiting() && rec.Order < lowest {
			lowest = rec.Order
		}
	}
	moved := append([]sched.JobID{id}, q.heldChain(id)...)
	prev := make(map[sched.JobID]int64, len(moved))
	for _, c := range moved {
		r := &q.store.Jobs[q.index(c)]
		prev[c], r.Order = r.Order, lowest-1
	}
	if err := m.saveQueue(); err != nil {
		for c, o := range prev {
			q.store.Jobs[q.index(c)].Order = o
		}
		return err
	}
	q.moved()
	m.admitJobs()
	return nil
}
```

- [ ] **`cancelOwnerJobs`:** its comment gains, after its first sentence: "Every job is marked first (no message, the reason) and the waiting ones (queued and held) are ended after, so a cascade one of them sets off finds the jobs after it marked: they keep this reason and send nothing (dependent jobs spec §5.4)." Replace its body after the guard with:

```go
	now := time.Now()
	found := false
	var waiting []sched.JobID
	for i := range q.store.Jobs {
		rec := &q.store.Jobs[i]
		if rec.Owner == 0 || !owners[InstanceID(rec.Owner)] || rec.State.Finished() {
			continue
		}
		found = true
		rec.Notify = false
		if rec.CancelReason == "" {
			rec.CancelReason = reason
		}
		if rec.State.Waiting() {
			waiting = append(waiting, rec.ID)
		}
	}
	if !found {
		return
	}
	for _, id := range waiting {
		i := q.index(id)
		if !q.store.Jobs[i].State.Waiting() {
			continue // the cascade of a job before it ended it
		}
		endJob(&q.store.Jobs[i], sched.Cancelled, now)
		m.settledJob(i)
	}
	q.moved()
	if len(waiting) > 0 {
		m.afterSettle()
	} else {
		m.saveQueueQuiet("cancel")
	}
	m.gate(gateQueue).request()
	m.maybeQueue()
```

- [ ] **`settledJob`:** its comment's "and its owner is sent the notice" becomes "and the jobs held after it are released or cancelled, with its message and theirs (settleChain)", and its last line `m.sendJobNotice(rec)` becomes `m.settleChain(rec)`.

### Step 2.3: Loading, live sizing, the advisor and the view

**Files:** Modify `core/queue.go`, `core/queue_beliefs.go`, `core/queue_advisor.go`, `core/queue_view.go`.

- [ ] **`core/queue.go`, `loadQueue`:** between `m.markPendingNotices()` and `m.adoptJobs()`, add `m.resolveHeld()`.
- [ ] **`core/queue_beliefs.go`:**
  - `resizeQueued`: its comment's "every queued job" becomes "every queued or held job", and its test `rec.State != sched.Queued` becomes `!rec.State.Waiting()`.
  - `forgetBelief`: `r.State == sched.Queued && r.Signature == id` becomes `r.State.Waiting() && r.Signature == id`.
  - `readBeliefs`: the loop body becomes:

```go
		if query.Matches(b) {
			v := sched.ViewBelief(b.Clone(), q.policy)
			if b.After != "" && b.After != sched.AfterNone {
				if p := q.beliefs.Find(b.After); p != nil {
					v.After = slices.Clone(p.Command)
				}
			}
			page.Beliefs = append(page.Beliefs, v)
		}
```

- [ ] **`core/queue_advisor.go`:**
  - `releaseAsking`, `releaseOverdueAsks`: `rec.State == sched.Queued && rec.Sizing != ""` becomes `rec.State.Waiting() && rec.Sizing != ""`.
  - `resumeAsks`: `rec.State != sched.Queued || rec.Sizing == ""` becomes `!rec.State.Waiting() || rec.Sizing == ""`.
  - `advisorInputFor`: the `advisor.Job{…}` literal gains `After: m.advisorAfter(rec),`.
- [ ] **`core/queue_view.go`:**
  - `QueueView.Jobs`' comment becomes "running (by start), waiting (queued and held, in queue order), then finished (newest end first)".
  - `JobView` gains, after `Ideal`:

```go
	// Dependent jobs (spec 2026-11-05-dependent-jobs §4): the job this one
	// runs after (0 for none), and when it joined the queue (zero while
	// held, and on a record from an earlier build).
	After sched.JobID
	Ready time.Time
```

  - After `StartFailed`, add:

```go
// QueuedSince is when the job joined the queue: Ready, or Submitted when it
// has none (a held job, or a record from an earlier build), as
// sched.Record.QueuedSince.
func (j JobView) QueuedSince() time.Time {
	if j.Ready.IsZero() {
		return j.Submitted
	}
	return j.Ready
}
```

  - `queueView`: `case sched.Queued:` becomes `case sched.Queued, sched.Held:` (the comment's "the queued ones in queue order" becomes "the waiting ones, queued and held, in queue order"), and `if r.State == sched.Queued {` (the `StartsAt` line) becomes `if r.State.Waiting() {`.
  - `queueJobView`: the literal gains `After: r.After, Ready: r.Ready,`, and its switch gains:

```go
	case sched.Held:
		jv.Waiting = q.heldWaiting(r)
```

### Step 2.4: Messages

**Files:** Modify `core/queue_notice.go`.

- [ ] **Replace `sendJobNotice`** with:

```go
// sendJobNotice tells rec's owner how the job ended (spec §5.8), through the
// loom mod's outbox with the origin OriginJob: its head (jobHead) and its
// log's tail.
func (m *Model) sendJobNotice(rec sched.Record) {
	m.sendNotice(rec, m.jobHead(rec), rec.ID)
}

// sendNotice sends head, then the tail of job logOf's log at rec's --tail,
// to rec's owner, and records the notice's fate on rec. Nothing is sent for
// a job that asked for no notice, has no owner, or already has a fate
// (wantsNotice). When the owner is gone or can't take it the notice is
// recorded undelivered at once; otherwise a job reads the log's tail off the
// loop (a log has no size cap) and deliverJobNotice queues the text. rec is
// a copy: the caller may be iterating the store.
func (m *Model) sendNotice(rec sched.Record, head string, logOf sched.JobID) {
	if !wantsNotice(rec) {
		return
	}
	owner := InstanceID(rec.Owner)
	if _, why := m.noticeTarget(owner); why != "" {
		m.noteNotice(rec.ID, sched.NoticeUndelivered, why)
		return
	}
	logPath := filepath.Join(m.queue.jobDir(logOf), sched.LogFile)
	m.spawnBackground(buildNoticeJob(rec.ID, owner, head, logPath, rec.Tail))
}

// jobHead is rec's message before its log: its first line, which names the
// job it was released after (afterText), and the over-claim line of a
// dependent that needs one (overClaimLine).
func (m *Model) jobHead(rec sched.Record) string {
	head := noticeHead(rec, m.afterText(rec))
	if line := overClaimLine(rec); line != "" {
		head += "\n" + line
	}
	return head
}

// afterText is what a released dependent's first line adds in its
// parenthesis (dependent jobs spec §5.5): `after job 14 "build tests", done
// in 3m10s`. It is "" for a job with no prerequisite, one queued at once
// after a job that had already ended (its Ready is its submit), and one
// whose prerequisite's record is gone or never ran.
func (m *Model) afterText(rec sched.Record) string {
	if rec.After == 0 || !rec.Ready.After(rec.Submitted) {
		return ""
	}
	i := m.queue.index(rec.After)
	if i < 0 {
		return ""
	}
	p := m.queue.store.Jobs[i]
	if p.State != sched.Done || p.Started.IsZero() {
		return ""
	}
	return fmt.Sprintf("after job %d %q, done in %s", p.ID, p.Description, roundedDur(p.Ended.Sub(p.Started)))
}

// overClaimLine is the line a dependent's message gets when it peaked at
// over twice its memory claim (dependent jobs spec §5.5): it may have rebuilt
// what its prerequisite built. The peak is the larger of the sampled one and
// the command's max RSS, as sched.RunOf takes it. "" for a job with no
// prerequisite, an exclusive claim, or a peak within twice the claim.
func overClaimLine(rec sched.Record) string {
	if rec.After == 0 || rec.Claim.Exclusive || rec.Claim.MemoryGB < 1 {
		return ""
	}
	peak := rec.PeakRSSKB
	if rec.Exit != nil {
		peak = max(peak, rec.Exit.MaxRSSKB)
	}
	if peak <= 2*(int64(rec.Claim.MemoryGB)<<20) {
		return ""
	}
	return fmt.Sprintf("It peaked at %.1f GB on a %d GB claim: if it rebuilt what job %d built, run job %d's output instead.",
		float64(peak)/(1<<20), rec.Claim.MemoryGB, rec.After, rec.After)
}
```

- [ ] **`noticeHead` takes the after text:** its comment becomes "noticeHead is the notice's first line (spec §5.8): the job, then its outcome and how long it ran and waited in the queue (from when it joined it, Record.QueuedSince), with after, unless it is "", added in the parenthesis (dependent jobs spec §5.5). A lost job or one that failed to start has no run to time; a job cancelled while it waited only waited." Its signature becomes `func noticeHead(rec sched.Record, after string) string`, and its final switch becomes:

```go
	switch {
	case rec.State == sched.Lost, startFailed(rec.State, rec.Failure, rec.Exit):
		return head + "."
	case !rec.Started.IsZero() && !rec.Ended.Before(rec.Started):
		wait := roundedDur(rec.Started.Sub(rec.QueuedSince())) + " in the queue"
		if after != "" {
			wait += ", " + after
		}
		return fmt.Sprintf("%s after %s (%s).", head, roundedDur(rec.Ended.Sub(rec.Started)), wait)
	default:
		return fmt.Sprintf("%s after %s in the queue.", head, roundedDur(rec.Ended.Sub(rec.QueuedSince())))
	}
```

- [ ] **Update the callers in tests:** in `core/queue_notice_test.go`, every `noticeHead(x)` becomes `noticeHead(x, "")` (`TestJobQueue_ANoticeNamesItsJobsOutcome` and `TestJobQueue_ANoticeReachesTheOwnersModAndIsRecordedSent`); `grep -rn 'noticeHead(' core` must show no one-argument call.
- [ ] **`core/queue_watch_test.go`, `wqStop`:** `case sched.Queued:` becomes `case sched.Queued, sched.Held:`, so a test's held jobs end with it.
- [ ] **Build:** `CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go vet ./core/`. Expected: clean. Then `loom job run -d "dependent jobs: core, before the new tests" -- env CGO_ENABLED=0 go test ./core/`. Expected: PASS (every existing test: no job has a prerequisite, so nothing it sees changed).

### Step 2.5: The model's tests

**Files:** Create `core/queue_chain_test.go`.

- [ ] **Write `core/queue_chain_test.go`:**

```go
package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wqHeld is a job held after job after, at order: one core, 1 GB, a real
// folder, a command that exits at once.
func wqHeld(t *testing.T, id, after sched.JobID, order int64) sched.Record {
	t.Helper()
	r := wqQueuedRecord(t, id)
	r.State, r.After, r.Order = sched.Held, after, order
	return r
}

// wqRunning is a running job's record with no shim behind it: a test ends
// it with wqEnd.
func wqRunning(t *testing.T, id sched.JobID) sched.Record {
	t.Helper()
	r := wqQueuedRecord(t, id)
	r.State, r.Started = sched.Running, time.Now()
	return r
}

// wqEnd ends job id in state with exit (nil for none) and settles it, as a
// pass does.
func wqEnd(t *testing.T, m *Model, id sched.JobID, state sched.State, exit *sched.Exit) {
	t.Helper()
	i := m.queue.index(id)
	require.GreaterOrEqual(t, i, 0, "job %d", id)
	m.queue.store.Jobs[i].Exit = exit
	m.finishJob(i, state, time.Now())
	m.afterSettle()
}

// wqSubmitAfter submits a job of argv after job after and returns its record.
func wqSubmitAfter(t *testing.T, m *Model, after sched.JobID, argv ...string) sched.Record {
	t.Helper()
	sub := wqJob(t, argv...)
	sub.After = after
	id, _, err := m.submitJob(sub)
	require.NoError(t, err)
	return wqRecord(t, m, id)
}

// wqPaused pauses admission (an invalid block), so nothing a test seeds
// starts, while settles, releases and cascades go on.
func wqPaused(t *testing.T, m *Model) {
	t.Helper()
	wqConfig(t, m, `{"cpus": -1}`)
	m.refreshPolicy()
	require.NotEmpty(t, m.queue.paused)
}

func TestJobQueue_AfterRefusesWhatCanNeverRun(t *testing.T) {
	m := wqModel(t)
	failed := wqEnded(5, 0)
	failed.State, failed.Exit = sched.Failed, &sched.Exit{Code: 101}
	cancelled := wqEnded(6, 0)
	cancelled.State, cancelled.Exit, cancelled.CancelReason = sched.Cancelled, nil, "with loom job cancel"
	lost := wqEnded(7, 0)
	lost.State, lost.Exit = sched.Lost, nil
	stranger := wqRunning(t, 8)
	stranger.Owner = 77
	for _, r := range []sched.Record{failed, cancelled, lost, stranger} {
		wqSeed(t, m, r)
	}
	next := m.queue.store.NextID

	for after, want := range map[sched.JobID]string{
		99: "there is no job 99",
		5:  "job 5 failed (exit 101), so a job after it would never run",
		6:  "job 6 was cancelled, so a job after it would never run",
		7:  "job 7 was lost, so a job after it would never run",
		8:  "job 8 has another owner: --after takes a job with the same owner as the new one",
	} {
		sub := wqJob(t, "true")
		sub.After = after
		_, _, err := m.submitJob(sub)
		assert.EqualError(t, err, want)
	}
	assert.Equal(t, next, m.queue.store.NextID, "nothing was created")
	assert.Len(t, m.queue.store.Jobs, 4)
}

func TestJobQueue_AJobAfterAWaitingOrRunningOneIsHeldAtItsPlace(t *testing.T) {
	m := wqModel(t)
	wqConfig(t, m, `{"cpus": 4}`) // one default job at a time
	running := wqSubmit(t, m, "sleep", "30")
	queued := wqSubmit(t, m, "true")
	m.Drain() // the running job's start is never run here

	b := wqSubmitAfter(t, m, running, "true")
	assert.Equal(t, sched.Held, b.State)
	assert.Equal(t, running, b.After)
	assert.Equal(t, int64(1), b.Order, "the running job's place in line")
	assert.True(t, b.Ready.IsZero(), "it hasn't joined the queue")
	c := wqSubmitAfter(t, m, queued, "true")
	assert.Equal(t, sched.Held, c.State)
	assert.Equal(t, int64(2), c.Order)
	d := wqSubmitAfter(t, m, b.ID, "true")
	assert.Equal(t, sched.Held, d.State, "a job after a held one is held too")
	assert.Equal(t, int64(1), d.Order, "a chain shares one place")

	saved, err := sched.LoadStore(m.queue.dir)
	require.NoError(t, err)
	assert.Equal(t, sched.Held, saved.Jobs[2].State, "written")
	assert.Equal(t, running, saved.Jobs[2].After)
}

func TestJobQueue_AJobAfterADoneOneQueuesAtOnceAtTheBack(t *testing.T) {
	m := wqModel(t)
	wqConfig(t, m, `{"cpus": 4}`)
	wqSeed(t, m, wqEnded(5, 0))
	wqSubmit(t, m, "sleep", "30") // job 6 fills the cores
	m.Drain()

	rec := wqSubmitAfter(t, m, 5, "true")

	assert.Equal(t, sched.Queued, rec.State)
	assert.Equal(t, int64(rec.ID), rec.Order, "its own place: an old finished job's is not borrowed")
	assert.Equal(t, rec.Submitted, rec.Ready)
	assert.Equal(t, sched.JobID(5), rec.After)
}

// The whole path, through real shims: the build ends, its dependent is
// released in the settle at the build's place, and the admission pass that
// follows starts it ahead of a job submitted between the two.
func TestJobQueue_ADoneJobReleasesItsDependentsAtItsPlace(t *testing.T) {
	m := wqLiveModel(t)
	wqConfig(t, m, `{"cpus": 4}`) // one default job at a time
	gate := filepath.Join(t.TempDir(), "go")
	build := wqSubmit(t, m, wqGated(gate, 0)...)
	wqPump(t, m) // its start: a real shim
	later := wqSubmit(t, m, "true")
	tests := wqSubmitAfter(t, m, build, "true").ID
	require.Equal(t, sched.Held, wqRecord(t, m, tests).State)

	require.NoError(t, os.WriteFile(gate, nil, 0o600))
	require.Equal(t, sched.Done, wqSettle(t, m, build).State)

	rec := wqRecord(t, m, tests)
	assert.Equal(t, sched.Running, rec.State, "released in the settle, and started by the pass after it")
	assert.True(t, rec.Ready.After(rec.Submitted), "it joined the queue at its release")
	assert.Equal(t, int64(build), rec.Order, "at its prerequisite's place")
	assert.Equal(t, sched.Queued, wqRecord(t, m, later).State, "the job submitted between them waits")
	assert.Equal(t, sched.Done, wqSettle(t, m, tests).State)
	assert.Equal(t, sched.Done, wqSettle(t, m, later).State)
}

func TestJobQueue_AJobThatDoesNotSucceedCancelsItsChain(t *testing.T) {
	cases := []struct {
		name    string
		state   sched.State
		exit    *sched.Exit
		failure string
		want    string
	}{
		{"an exit code", sched.Failed, &sched.Exit{Code: 101}, "", "because job 1 failed (exit 101)"},
		{"a signal", sched.Failed, &sched.Exit{Code: -1, Signal: "SIGSEGV"}, "", "because job 1 was killed by SIGSEGV"},
		{"a start that failed", sched.Failed, nil, "its folder is gone", "because job 1 could not start"},
		{"a cancel", sched.Cancelled, nil, "", "because job 1 was cancelled"},
		{"a lost runner", sched.Lost, nil, lostWhy, "because job 1 was lost"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := wqModel(t)
			wqPaused(t, m)
			wqSeed(t, m, wqRunning(t, 1))
			wqSeed(t, m, wqHeld(t, 2, 1, 1))
			wqSeed(t, m, wqHeld(t, 3, 2, 1))
			wqSeed(t, m, wqHeld(t, 4, 1, 1))
			wqSeed(t, m, wqQueuedRecord(t, 5))
			m.queue.store.Jobs[0].Failure = c.failure

			wqEnd(t, m, 1, c.state, c.exit)

			for id, reason := range map[sched.JobID]string{2: c.want, 3: "because job 2 was cancelled", 4: c.want} {
				rec := wqRecord(t, m, id)
				assert.Equal(t, sched.Cancelled, rec.State, "job %d", id)
				assert.Equal(t, reason, rec.CancelReason, "job %d", id)
				assert.False(t, rec.Ended.IsZero(), "job %d", id)
			}
			assert.Equal(t, sched.Queued, wqRecord(t, m, 5).State, "a job outside the chain is not touched")
		})
	}
}

func TestJobQueue_CancellingAHeldJobEndsItAndItsChainAtOnce(t *testing.T) {
	m := wqModel(t)
	wqPaused(t, m)
	wqSeed(t, m, wqRunning(t, 1))
	wqSeed(t, m, wqHeld(t, 2, 1, 1))
	wqSeed(t, m, wqHeld(t, 3, 2, 1))

	require.NoError(t, m.cancelJob(2, "with loom job cancel"))

	assert.Equal(t, sched.Cancelled, wqRecord(t, m, 2).State)
	assert.Equal(t, "with loom job cancel", wqRecord(t, m, 2).CancelReason)
	assert.Equal(t, sched.Cancelled, wqRecord(t, m, 3).State)
	assert.Equal(t, "because job 2 was cancelled", wqRecord(t, m, 3).CancelReason)
	assert.Equal(t, sched.Running, wqRecord(t, m, 1).State, "its prerequisite runs on")
	assert.EqualError(t, m.cancelJob(3, "x"), "job 3 has finished")
}

// A pause marks every job of its session before it ends any: the cascade a
// held job's end sets off keeps the pause's reason and sends nothing.
func TestJobQueue_APauseEndsHeldJobsWithItsReasonAndNoMessage(t *testing.T) {
	m := wqModel(t)
	owner := wqOwnerSession(t, m)
	wqPaused(t, m)
	own := func(r sched.Record) sched.Record {
		r.Owner, r.Notify = uint64(owner), true
		return r
	}
	wqSeed(t, m, own(wqRunning(t, 1)))
	wqSeed(t, m, own(wqHeld(t, 2, 1, 1)))
	wqSeed(t, m, own(wqHeld(t, 3, 2, 1)))

	m.Pause(owner, 0)
	m.Drain() // the session's own job and the queue's pass, which this test does not run

	for _, id := range []sched.JobID{2, 3} {
		rec := wqRecord(t, m, id)
		assert.Equal(t, sched.Cancelled, rec.State, "job %d: a held job ends at once", id)
		assert.Equal(t, "because the session was paused", rec.CancelReason, "job %d keeps the pause's reason", id)
		assert.False(t, rec.Notify, "job %d", id)
		assert.Equal(t, sched.NoticeNone, rec.Notice, "job %d: no message, not even an undelivered one", id)
	}
	root := wqRecord(t, m, 1)
	assert.Equal(t, sched.Running, root.State, "gateQueue ends a running job")
	assert.Equal(t, "because the session was paused", root.CancelReason)

	wqEnd(t, m, 1, sched.Cancelled, nil)
	assert.Equal(t, sched.NoticeNone, wqRecord(t, m, 1).Notice)
}

func TestJobQueue_MoveToFrontCarriesTheJobsHeldAfterIt(t *testing.T) {
	m := wqModel(t)
	wqConfig(t, m, `{"cpus": 4}`) // one default job at a time
	wqSubmit(t, m, "sleep", "30")  // job 1 runs
	m.Drain()
	x := wqSubmit(t, m, "true") // job 2, queued
	y := wqSubmit(t, m, "true") // job 3, queued
	b := wqSubmitAfter(t, m, y, "true").ID
	require.Equal(t, int64(3), wqRecord(t, m, b).Order)

	require.NoError(t, m.moveJobToFront(y))
	assert.Equal(t, int64(1), wqRecord(t, m, y).Order, "one below the lowest waiting order, job 2's")
	assert.Equal(t, int64(1), wqRecord(t, m, b).Order, "the job held after it moves with it")

	require.NoError(t, m.moveJobToFront(b))
	assert.Equal(t, int64(0), wqRecord(t, m, b).Order, "a held job moves to the front too")
	assert.Equal(t, int64(2), wqRecord(t, m, x).Order)
}

func TestJobQueue_ADependentsFirstLineNamesItsPrerequisite(t *testing.T) {
	r := wqEnded(15, 0)
	r.Description, r.After, r.Ready = "run tests", 14, r.Started.Add(-2*time.Second)
	assert.Equal(t, `[loom] job 15 "run tests" finished: exit 0 after 3m10s (2s in the queue, after job 14 "build tests", done in 1m0s).`,
		noticeHead(r, `after job 14 "build tests", done in 1m0s`))
}

func TestJobQueue_ADependentThatPeakedOverTwiceItsClaimIsToldSo(t *testing.T) {
	rec := sched.Record{After: 14, Claim: sched.Claim{CPUs: 2, MemoryGB: 2}, PeakRSSKB: 6_400_000}
	assert.Equal(t, "It peaked at 6.1 GB on a 2 GB claim: if it rebuilt what job 14 built, run job 14's output instead.", overClaimLine(rec))
	rec.PeakRSSKB, rec.Exit = 1_000_000, &sched.Exit{MaxRSSKB: 6_400_000}
	assert.NotEmpty(t, overClaimLine(rec), "the larger of the sampled peak and the max RSS")
	rec.Exit.MaxRSSKB = 4 << 20
	assert.Empty(t, overClaimLine(rec), "twice its claim is not over twice")
	rec.Exit.MaxRSSKB = 6_400_000
	rec.After = 0
	assert.Empty(t, overClaimLine(rec), "only a dependent")
	rec.After, rec.Claim = 14, sched.Claim{Exclusive: true}
	assert.Empty(t, overClaimLine(rec), "an exclusive claim has every GB")
}

// wqChainOwner is a session with a live mod on launch L1, for the chain's
// messages, and seed, which adds a record of that owner.
func wqChainOwner(t *testing.T, m *Model) (seed func(r sched.Record, desc string, notify bool)) {
	t.Helper()
	inst := modInst(t, m, "owner")
	owner := uint64(m.idOf(inst))
	require.NoError(t, m.ModHello("L1", ModHello{Mod: 1}))
	return func(r sched.Record, desc string, notify bool) {
		r.Owner, r.Notify, r.Description = owner, notify, desc
		wqSeed(t, m, r)
	}
}

func TestJobQueue_AChainThatSucceedsSendsOneMessage(t *testing.T) {
	withMod(t)
	m := wqModel(t)
	wqPaused(t, m)
	seed := wqChainOwner(t, m)
	build := wqRunning(t, 1)
	build.Started = time.Now().Add(-190 * time.Second)
	seed(build, "build tests", true)
	seed(wqHeld(t, 2, 1, 1), "run tests", true)

	wqEnd(t, m, 1, sched.Done, &sched.Exit{Code: 0})
	wqPump(t, m)

	got := wqRecord(t, m, 1)
	assert.Equal(t, sched.NoticeCarried, got.Notice, "its message goes with the dependent's")
	assert.Equal(t, "its result goes with job 2's", got.NoticeWhy)
	msgs, _, err := m.ModInbox("L1", "")
	require.NoError(t, err)
	assert.Empty(t, msgs, "no message for the build")

	// The dependent runs, and ends.
	i := m.queue.index(2)
	rec := &m.queue.store.Jobs[i]
	require.Equal(t, sched.Queued, rec.State, "released")
	rec.State, rec.Started = sched.Running, rec.Ready.Add(2*time.Second)
	rec.Ended = rec.Started.Add(9*time.Minute + 12*time.Second)
	wqEnd(t, m, 2, sched.Done, &sched.Exit{Code: 0, Ended: rec.Ended})
	wqPump(t, m)

	msgs, _, err = m.ModInbox("L1", "")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	head, _, _ := strings.Cut(msgs[0].Text, "\n")
	assert.Equal(t, `[loom] job 2 "run tests" finished: exit 0 after 9m12s (2s in the queue, after job 1 "build tests", done in 3m10s).`, head)
}

func TestJobQueue_AFailedJobsMessageNamesTheChainItCancelled(t *testing.T) {
	withMod(t)
	m := wqModel(t)
	wqPaused(t, m)
	seed := wqChainOwner(t, m)
	seed(wqRunning(t, 1), "build tests", true)
	seed(wqHeld(t, 2, 1, 1), "run tests", true)
	seed(wqHeld(t, 3, 2, 1), "bench", true)

	wqEnd(t, m, 1, sched.Failed, &sched.Exit{Code: 101})
	wqPump(t, m)

	msgs, _, err := m.ModInbox("L1", "")
	require.NoError(t, err)
	require.Len(t, msgs, 1, "one message for the chain")
	lines := strings.Split(msgs[0].Text, "\n")
	require.GreaterOrEqual(t, len(lines), 2)
	assert.True(t, strings.HasPrefix(lines[0], `[loom] job 1 "build tests" finished: exit 101 after `), lines[0])
	assert.Equal(t, `Jobs 2 "run tests" and 3 "bench", which waited on it, were cancelled.`, lines[1])
	for _, id := range []sched.JobID{2, 3} {
		rec := wqRecord(t, m, id)
		assert.Equal(t, sched.NoticeCarried, rec.Notice, "job %d", id)
		assert.Equal(t, "its result goes with job 1's", rec.NoticeWhy, "job %d", id)
	}
}

// A build followed with `run` sends no message of its own: the first job
// down each branch that asked for one reports the cascade, with the build's
// log.
func TestJobQueue_AQuietJobsCascadeIsReportedOncePerBranch(t *testing.T) {
	withMod(t)
	m := wqModel(t)
	wqPaused(t, m)
	seed := wqChainOwner(t, m)
	seed(wqRunning(t, 1), "build", false)
	seed(wqHeld(t, 2, 1, 1), "unit", true)
	seed(wqHeld(t, 3, 2, 1), "bench", true)
	seed(wqHeld(t, 4, 1, 1), "lint", false)
	seed(wqHeld(t, 5, 4, 1), "docs", true)
	logPath := filepath.Join(m.queue.jobDir(1), sched.LogFile)
	require.NoError(t, os.WriteFile(logPath, []byte("error[E0308]: mismatched types\n"), 0o600))

	wqEnd(t, m, 1, sched.Failed, &sched.Exit{Code: 101})
	wqPump(t, m)

	msgs, _, err := m.ModInbox("L1", "")
	require.NoError(t, err)
	require.Len(t, msgs, 2, "job 2's and job 5's")
	tail := "Log: " + logPath + " (1 line). Last 1 line:\n\nerror[E0308]: mismatched types"
	assert.Equal(t, `[loom] job 2 "unit" was cancelled because job 1 "build" failed (exit 101).`+"\n"+
		`Job 3 "bench", which waited on it, was cancelled.`+"\n"+tail, msgs[0].Text)
	assert.Equal(t, `[loom] job 5 "docs" was cancelled because job 1 "build" failed (exit 101).`+"\n"+tail, msgs[1].Text)
	assert.Equal(t, sched.NoticeCarried, wqRecord(t, m, 3).Notice, "job 2's message reports it")
	assert.Equal(t, "its result goes with job 2's", wqRecord(t, m, 3).NoticeWhy)
	assert.Equal(t, sched.NoticeNone, wqRecord(t, m, 1).Notice, "it asked for no message")
	assert.Equal(t, sched.NoticeNone, wqRecord(t, m, 4).Notice, "nor did it")
}

func TestJobQueue_ABootResolvesTheHeldJobsAnOlderDaemonLeft(t *testing.T) {
	dir := t.TempDir()
	// Admission paused, so nothing here starts and a release stays queued.
	require.NoError(t, os.WriteFile(filepath.Join(dir, config.ConfigFileName), []byte(`{"scheduler": {"cpus": -1}}`), 0o644))
	held := func(id, after sched.JobID) sched.Record { return wqHeld(t, id, after, int64(id)) }
	failed := wqEnded(3, 0)
	failed.State, failed.Exit = sched.Failed, &sched.Exit{Code: 2}
	paused := held(9, 1)
	paused.CancelReason = "because the session was paused"
	store := sched.Store{Version: sched.StoreVersion, NextID: 13, Jobs: []sched.Record{
		wqEnded(1, 0),
		held(2, 1),
		failed,
		held(4, 3),
		held(5, 4),
		held(6, 0),  // an older loom's save dropped its after
		held(7, 99), // names no record
		paused,
		held(11, 12),
		wqQueuedRecord(t, 12),
	}}
	require.NoError(t, sched.SaveStore(filepath.Join(dir, jobsDirName), store))

	m := wqModelAt(t, dir)

	released := wqRecord(t, m, 2)
	assert.Equal(t, sched.Queued, released.State, "its prerequisite is done")
	assert.False(t, released.Ready.IsZero())
	for id, reason := range map[sched.JobID]string{
		4: "because job 3 failed (exit 2)",
		5: "because job 4 was cancelled",
		6: "because its prerequisite is unknown",
		7: "because its prerequisite is unknown",
		9: "because the session was paused",
	} {
		rec := wqRecord(t, m, id)
		assert.Equal(t, sched.Cancelled, rec.State, "job %d", id)
		assert.Equal(t, reason, rec.CancelReason, "job %d", id)
	}
	assert.Equal(t, sched.Held, wqRecord(t, m, 11).State, "its prerequisite still waits")
	saved, err := sched.LoadStore(filepath.Join(dir, jobsDirName))
	require.NoError(t, err)
	assert.Equal(t, sched.Cancelled, saved.Jobs[3].State, "written")
}

func TestJobQueue_TheViewListsHeldJobsAmongTheWaiting(t *testing.T) {
	m := wqModel(t)
	wqConfig(t, m, `{"cpus": 4}`)
	build := wqSubmit(t, m, "sleep", "30") // job 1 runs
	m.Drain()
	later := wqSubmit(t, m, "true") // job 2 waits
	tests := wqSubmitAfter(t, m, build, "true").ID

	jobs := m.Queue().Jobs
	require.Len(t, jobs, 3)
	assert.Equal(t, []sched.JobID{build, tests, later}, []sched.JobID{jobs[0].ID, jobs[1].ID, jobs[2].ID},
		"running, then the waiting jobs in queue order: the held one at its chain's place")
	assert.Equal(t, sched.Held, jobs[1].State)
	assert.Equal(t, build, jobs[1].After)
	assert.Equal(t, "after job 1 (running)", jobs[1].Waiting)
	assert.True(t, jobs[1].Ready.IsZero())
	assert.False(t, jobs[1].StartsAt.IsZero(), "the forecast releases it at its prerequisite's expected end")
	assert.Equal(t, jobs[2].Submitted, jobs[2].Ready)
}

func TestJobQueue_ADependentLearnsApartFromTheColdCommand(t *testing.T) {
	m := wqModel(t)
	wqConfig(t, m, `{"cpus": 4}`)
	build := wqJob(t, "cargo", "test", "--no-run")
	buildID, _, err := m.submitJob(build)
	require.NoError(t, err)
	m.Drain()
	sub := wqJob(t, "cargo", "test")
	sub.Cwd, sub.After = build.Cwd, buildID
	id, _, err := m.submitJob(sub)
	require.NoError(t, err)

	warm := wqSigOf(t, sub.Argv, sub.Cwd)
	cold := warm.ID()
	warm.After = wqRecord(t, m, buildID).Signature
	rec := wqRecord(t, m, id)
	assert.Equal(t, warm.ID(), rec.Signature, "its own belief")
	assert.NotEqual(t, cold, rec.Signature, "apart from the cold command's")
	b := m.queue.beliefs.Find(rec.Signature)
	require.NotNil(t, b, "the submit made its belief")

	b.AddRun(sched.Run{Outcome: sched.OutcomeDone, PeakKB: 5 << 20, WallSeconds: 30})
	m.admitJobs()
	assert.Equal(t, 6, wqRecord(t, m, id).Claim.MemoryGB, "a held job's claim stays live: 5 GB plus its margin")

	page, err := m.readBeliefs(sched.BeliefQuery{ID: rec.Signature})
	require.NoError(t, err)
	require.Len(t, page.Beliefs, 1)
	assert.Equal(t, []string{"cargo", "test", "--no-run"}, page.Beliefs[0].After)
}

func TestAdvisor_ADependentsRequestNamesItsPrerequisite(t *testing.T) {
	m, calls := wqAdvisedModel(t, wqPriorAnswer, 0)
	build := wqJob(t, "sleep", "30")
	build.Description = "build tests"
	buildID, _, err := m.submitJob(build)
	require.NoError(t, err)
	sub := wqJob(t, "cargo", "test")
	sub.Cwd, sub.After = build.Cwd, buildID
	id, _, err := m.submitJob(sub)
	require.NoError(t, err)
	require.Equal(t, sched.Held, wqRecord(t, m, id).State)
	require.Equal(t, sched.SizingAsking, wqRecord(t, m, id).Sizing, "an unseen command asks while it is held")

	wqWait(t, "both priors", func() bool { wqPump(t, m); return len(wqCalls(t, calls)) >= 2 })
	assert.Contains(t, wqCalls(t, calls)[1], `"after":{"command":["sleep","30"],"description":"build tests"}`)
	assert.NotContains(t, wqCalls(t, calls)[0], `"after"`, "the build has none")
}

func TestJobQueue_EndedHowNamesEachEnd(t *testing.T) {
	r := wqEnded(14, 0)
	for _, c := range []struct {
		edit func(*sched.Record)
		want string
	}{
		{func(r *sched.Record) { r.State, r.Exit = sched.Failed, &sched.Exit{Code: 101} }, "failed (exit 101)"},
		{func(r *sched.Record) { r.State, r.Exit = sched.Failed, &sched.Exit{Code: -1, Signal: "SIGKILL"} }, "was killed by SIGKILL"},
		{func(r *sched.Record) { r.State, r.Exit, r.Failure = sched.Failed, nil, "its folder is gone" }, "could not start"},
		{func(r *sched.Record) { r.State = sched.Cancelled }, "was cancelled"},
		{func(r *sched.Record) { r.State, r.Exit = sched.Lost, nil }, "was lost"},
	} {
		x := r
		c.edit(&x)
		assert.Equal(t, c.want, endedHow(x), fmt.Sprint(c.want))
	}
}
```

- [ ] **Run them:** `loom job run -d "dependent jobs: the chain's tests" -- env CGO_ENABLED=0 go test ./core/ -run 'TestJobQueue_(After|AJobAfter|ADoneJob|AJobThatDoesNot|CancellingAHeld|APauseEnds|MoveToFrontCarries|ADependent|AChain|AFailedJobs|AQuietJobs|ABootResolves|TheViewListsHeld|EndedHow)|TestAdvisor_ADependents'`. Expected: PASS. If one fails, fix the code of Steps 2.1–2.4, never the test's expectations, unless the spec says otherwise.
- [ ] **Prove the tests go red**, one mutation at a time, restoring each:
  - comment out `m.resolveHeld()` in `loadQueue`: `TestJobQueue_ABootResolvesTheHeldJobsAnOlderDaemonLeft` must FAIL (jobs 6, 7 and 9 stay held);
  - in `resolveHeld`, drop the first loop (the jobs with a cancel request): the same test must FAIL (job 9 is released with job 2);
  - remove `rec.Order = prereq.Order` from `submitJob`: `TestJobQueue_ADoneJobReleasesItsDependentsAtItsPlace` must FAIL (job 2, submitted between them, starts first);
  - in `cancelOwnerJobs`, end each waiting job inside the marking loop instead of after it: `TestJobQueue_APauseEndsHeldJobsWithItsReasonAndNoMessage` must FAIL (job 3 gets "because job 2 was cancelled").

### Step 2.6: Docs, gates and commit

- [ ] **`docs/claude/the-work-queue.md`:** in "What it is", after "`core/queue_beliefs.go` (signatures, sizing, `beliefs.json`)", add "and `core/queue_chain.go` (dependent jobs: the prerequisite check, release, the cascade and its messages, the boot's `resolveHeld`)". Add rule 10:

```
10. **Release and cascade inside the settle, never in a pass.** `settledJob` ends with `settleChain`: a job that ended done releases the jobs held after it (`releaseDependents`: queued from now, at the lower of their order and its), and any other end cancels them down the chain (`cascade`), with one message per branch (`sendCascadeNotices`) and the others recorded `carried`. At boot, `resolveHeld` runs before `adoptJobs`, since `adoptJobs`' settles end in `afterSettle`, which runs an admission pass. A held job counts as waiting (`State.Waiting`) everywhere a queued one does except `Admit` and `Grant`, which never see it: cancel at once, pause, kill and reset, the move to the front, live sizing, the advisor's asks and the view. `cancelOwnerJobs` marks every job before it ends any, so a cascade inside a pause keeps the pause's reason and sends nothing. *Silent:* a release made in a pass starts a dependent a pass late, or behind jobs submitted after its chain; a cascade that skips the branch rules sends a message per job, or a pause's cancellations send messages.
```

- [ ] **Gates:** `gofmt -l core sched` (empty); `CGO_ENABLED=0 go vet ./core/ ./sched/...`; `go test ./core/rpc -run 'TestProtocolReference|TestGenerated_IsFresh'` (expected PASS: `Submit.After` is `omitempty`, so the reference's zero-valued params don't change; if it fails, regenerate with `go test ./core/rpc -run TestProtocolReference -update` and commit `docs/specs/protocol.md`); `loom job run -d "dependent jobs: core" -- env CGO_ENABLED=0 go test ./core/` (PASS).
- [ ] **Commit:**

```bash
git add core/queue_chain.go core/queue_chain_test.go core/queue_ops.go core/queue_notice.go core/queue.go core/queue_beliefs.go core/queue_advisor.go core/queue_view.go core/queue_notice_test.go core/queue_watch_test.go docs/claude/the-work-queue.md
git commit -m "feat(core): run a job after another: held, released at its place, cancelled down its chain

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>" -- core docs/claude/the-work-queue.md
```

## Package 3: the CLI, the work view, the context and docs

### Step 3.1: `--after` on `submit` and `run`, and what the CLI prints

**Files:** Modify `internal/cli/job.go`, `internal/cli/job_follow.go`, `internal/cli/job_sizing.go`, `internal/cli/job_list.go`, `internal/cli/job_beliefs.go`, `internal/cli/job_test.go`.

- [ ] **Write the tests** (append to `internal/cli/job_test.go`), and in `TestJobContext_NamesOnlyFlagsTheCommandsHave` change the expected flags to `[]string{"--cpus", "--mem", "--io", "--exclusive", "--after"}`:

```go
func TestJobSubmission_TheAfterFlag(t *testing.T) {
	f, c, args := parsedSubmit(t, "--after", "14", "--", "true")
	sub, err := f.submission(c, args, true, defaultTail)
	require.NoError(t, err)
	assert.Equal(t, sched.JobID(14), sub.After)

	f, c, args = parsedSubmit(t, "--", "true")
	sub, err = f.submission(c, args, true, defaultTail)
	require.NoError(t, err)
	assert.Zero(t, sub.After, "none")

	f, c, args = parsedSubmit(t, "--after", "0", "--", "true")
	_, err = f.submission(c, args, true, defaultTail)
	assert.EqualError(t, err, "--after is 0: it must be a job ID")
}

func TestJobStartedLine_AReleasedJobCountsFromItsPrerequisitesEnd(t *testing.T) {
	t0 := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	v := core.JobView{ID: 15, After: 14, Submitted: t0, Ready: t0.Add(3 * time.Minute), Started: t0.Add(3*time.Minute + 2*time.Second)}
	assert.Equal(t, "job 15 started 2s after job 14 ended", startedLine(v))
	v.Ready = t0 // queued at once after a job already done
	assert.Equal(t, "job 15 started after 3m2s", startedLine(v))
	v.After, v.Ready = 0, time.Time{} // a record from an earlier build
	assert.Equal(t, "job 15 started after 3m2s", startedLine(v))
}

func TestJobList_ShowsAHeldJobAndWhatItRunsAfter(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	q := core.QueueView{Jobs: []core.JobView{
		{ID: 4, State: sched.Running, Description: "build", Claim: sched.Claim{CPUs: 4, MemoryGB: 6}, Submitted: now.Add(-5 * time.Minute), Started: now.Add(-3 * time.Minute)},
		{ID: 5, State: sched.Held, Description: "tests", After: 4, Claim: sched.Claim{CPUs: 2, MemoryGB: 2}, Submitted: now.Add(-2 * time.Minute), Waiting: "after job 4 (running)"},
	}}
	var out bytes.Buffer
	require.NoError(t, writeJobList(&out, q, false, now))
	assert.Regexp(t, `(?m)^5 +held +tests +- +2c/2G +2m0s +- +after job 4 \(running\)$`, out.String())
}

func TestJobBeliefCommand_NamesWhatItRunsAfter(t *testing.T) {
	v := sched.BeliefView{Belief: sched.Belief{Signature: sched.Signature{Command: []string{"cargo", "test"}}}}
	assert.Equal(t, "cargo test", beliefCommand(v))
	v.After = []string{"cargo", "test", "--no-run"}
	assert.Equal(t, "cargo test (after cargo test --no-run)", beliefCommand(v))
}

// The whole path through a daemon: a job submitted --after a running one is
// held, and its follower says so, then counts its start from the end of the
// job it waited on.
func TestJobSubmit_AfterHoldsTheJobUntilItsPrerequisiteIsDone(t *testing.T) {
	isolateWorkspaces(t)
	t.Setenv(claudemod.EnvLaunch, "")
	d := serveDaemon(t, "true")
	_, _, err := runJob(t, "submit", "-d", "build", "--no-notify", "--", "sh", "-c", "echo up; sleep 3")
	require.NoError(t, err)

	out, _, err := runJob(t, "submit", "-d", "tests", "--after", "1", "--", "sh", "-c", "echo ran")
	require.NoError(t, err)
	assert.Regexp(t, `^job 2 held: runs after job 1 \((queued|running)\)\n`, out)

	out, errOut, err := runJob(t, "wait", "2")
	require.NoError(t, err)
	assert.Equal(t, "ran\n", out)
	assert.Regexp(t, `(?s)loom: job 2 held: runs after job 1 \(running\)\n.*loom: job 2 started \d+s after job 1 ended\n.*loom: job 2 done in \S+ \(exit 0\)\n`, errOut)
	d.waitFinished(t, 1)
}

func TestJobRun_AJobWhosePrerequisiteFailsExits125(t *testing.T) {
	isolateWorkspaces(t)
	t.Setenv(claudemod.EnvLaunch, "")
	serveDaemon(t, "true")
	_, _, err := runJob(t, "submit", "-d", "build", "--no-notify", "--", "sh", "-c", "sleep 1; exit 3")
	require.NoError(t, err)

	_, errOut, err := runJob(t, "run", "-d", "tests", "--after", "1", "--", "true")

	assert.Equal(t, 125, exitCode(t, err))
	assert.Contains(t, errOut, "loom: job 2 was cancelled because job 1 failed (exit 3)\n")
}
```

- [ ] **Run them:** `CGO_ENABLED=0 go test ./internal/cli/ -run 'TestJobSubmission_TheAfterFlag|TestJobStartedLine|TestJobList_ShowsAHeld|TestJobBeliefCommand|TestJobSubmit_AfterHolds|TestJobRun_AJobWhosePrerequisite|TestJobContext'`. Expected: FAIL to compile (`unknown flag`, `undefined: beliefCommand`).
- [ ] **Implement:**
  - `job.go`, `claimFlags`: add the field `after int64` (its comment: "what the job is, what it claims, and the job it runs after"); in `add`, add

```go
	fl.Int64Var(&f.after, "after", 0, "Run after job N ends done: held until then, and cancelled if it ends any other way")
```

  in `submission`, after the claim's check:

```go
	if c.Flags().Changed("after") && f.after < 1 {
		return sched.Submit{}, fmt.Errorf("--after is %d: it must be a job ID", f.after)
	}
```

  and the returned `sched.Submit` gains `After: sched.JobID(f.after),`.
  - `job.go`, `printPlacement`: its first case list gains, before `case v.State != sched.Queued:`,

```go
	case v.State == sched.Held:
		fmt.Fprintf(out, "job %d held: runs %s\n", v.ID, v.Waiting)
		return
```

  and its comment gains "or what a held job runs after".
  - `job_follow.go`, `report`: add before `case sched.Running:`

```go
	case sched.Held:
		f.queued = true // its start is reported
		if line := fmt.Sprintf("job %d held: runs %s", v.ID, v.Waiting); line != f.last {
			f.d.say("%s", line)
			f.last = line
		}
```

  and its comment becomes "report writes the held line when what a held job waits on changes, the queue line when the job's place changes, and the start line when a job seen waiting starts."
  - `job_sizing.go`, `startedLine`: its first line becomes

```go
	line := fmt.Sprintf("job %d started after %s", v.ID, rounded(v.Started.Sub(v.QueuedSince())))
	if v.After != 0 && v.Ready.After(v.Submitted) {
		// Released after its prerequisite (dependent jobs spec §6).
		line = fmt.Sprintf("job %d started %s after job %d ended", v.ID, rounded(v.Started.Sub(v.Ready)), v.After)
	}
```

  - `job_list.go`, `writeJobList`: both `j.State == sched.Queued` tests on `waits` become `j.State.Waiting()`; `elapsed`'s `case j.State == sched.Queued:` becomes `case j.State.Waiting():` returning `now.Sub(j.QueuedSince())`, and its comment says "(waiting: since it joined the queue, or since its submit while held)".
  - `job_beliefs.go`: add

```go
// beliefCommand is a belief's command as `loom job beliefs` lists it, with
// the command it runs after (dependent jobs spec §5.6).
func beliefCommand(v sched.BeliefView) string {
	command := strings.Join(v.Belief.Command, " ")
	if v.After != nil {
		command += " (after " + strings.Join(v.After, " ") + ")"
	}
	return command
}
```

  and in the list's row (`cutTo(oneLine(strings.Join(v.Belief.Command, " ")), 40)`) use `cutTo(oneLine(beliefCommand(v)), 40)`.
- [ ] **Run the tests again.** Expected: PASS. Then `CGO_ENABLED=0 go test ./internal/cli/ -run TestJob` (PASS).

### Step 3.2: The work view

**Files:** Modify `ui/work.go`, `ui/work_test.go`, `app/work.go`, `app/work_badge.go`, `app/work_test.go`, `app/work_badge_test.go`.

- [ ] **Write the tests.** Append to `ui/work_test.go`:

```go
func TestWorkRows_HeldJobsListAmongTheQueued(t *testing.T) {
	held := workQueued(32, "tests", "after job 20 (running)", time.Minute)
	held.State, held.After = sched.Held, 20
	var ids []sched.JobID
	for _, j := range WorkRows([]core.JobView{workRunning(20, "build", time.Minute), held, workQueued(30, "q30", "", time.Minute)}) {
		ids = append(ids, j.ID)
	}
	assert.Equal(t, []sched.JobID{20, 32, 30}, ids, "a held job sits among the queued, where the view's order puts it")
}

func TestRenderWork_AHeldJobSaysWhatItRunsAfter(t *testing.T) {
	held := workQueued(32, "run tests", "after job 14 (running)", time.Minute)
	held.State, held.After = sched.Held, 14
	q := workBusy()
	q.Jobs = append([]core.JobView{q.Jobs[0], q.Jobs[1], held}, q.Jobs[2:]...)

	out := ansi.Strip(RenderWork(148, 30, WorkData{Queue: q, Selected: 32, Now: workNow}))

	assert.Contains(t, lineWith(t, out, "run tests"), "after job 14 (running)")
	assert.Less(t, strings.Index(out, "QUEUED"), strings.Index(out, "run tests"), "listed under QUEUED")
	assert.Less(t, strings.Index(out, "run tests"), strings.Index(out, "RECENT"))
	assert.Contains(t, out, "(held: no output yet)")
}
```

  Append to `app/work_test.go`:

```go
// heldWorkJob is a job held after job after.
func heldWorkJob(id, after sched.JobID, desc string) core.JobView {
	j := queueJob(id, sched.Held, desc)
	j.Started, j.After = time.Time{}, after
	return j
}

func TestWorkCancelPrompt_NamesTheJobsHeldAfterIt(t *testing.T) {
	jobs := []core.JobView{
		queueJob(14, sched.Running, "build"),
		heldWorkJob(15, 14, "tests"),
		heldWorkJob(16, 15, "bench"),
		heldWorkJob(17, 99, "other"),
	}
	assert.Equal(t, `Cancel job 14 "build" and jobs 15 and 16 that wait on it?`, cancelPrompt(jobs[0], jobs))
	assert.Equal(t, `Cancel job 15 "tests" and job 16 that waits on it?`, cancelPrompt(jobs[1], jobs))
	assert.Equal(t, `Cancel job 16 "bench"?`, cancelPrompt(jobs[2], jobs))
}

func TestWorkFront_AHeldJobCanMoveToo(t *testing.T) {
	m := workHome(t, queueJob(14, sched.Running, "a"), heldWorkJob(16, 14, "b"))
	w := watchWork(t, m)
	pressWork(t, m, "j") // job 16, held
	assert.Nil(t, pressWork(t, m, "t"))
	assert.Equal(t, []sched.JobID{16}, w.fronts)
}
```

  Append to `app/work_badge_test.go`:

```go
func TestOwnerJobBadges_AHeldJobCountsAsQueued(t *testing.T) {
	q := core.QueueView{Jobs: []core.JobView{{ID: 5, State: sched.Held, Owner: 2}}}
	assert.Equal(t, map[core.InstanceID]string{2: "⚙ queued"}, ownerJobBadges(q))
}
```

- [ ] **Run them:** `CGO_ENABLED=0 go test ./ui/ -run 'TestWorkRows_HeldJobs|TestRenderWork_AHeldJob'` and `CGO_ENABLED=0 go test ./app/ -run 'TestWorkCancelPrompt|TestWorkFront_AHeldJob|TestOwnerJobBadges_AHeldJob'`. Expected: FAIL (held jobs fall in no section; `cancelPrompt` undefined).
- [ ] **Implement:**
  - `ui/work.go`: in `WorkRows`, `case j.State == sched.Queued:` becomes `case j.State.Waiting():` (its comment: "the queued ones" becomes "the waiting ones, queued and held"); in `workSection`, `case sched.Queued:` becomes `case sched.Queued, sched.Held:`; in `workTailBody`, `case j.State == sched.Queued:` becomes

```go
	case j.State.Waiting():
		return []string{workDimStyle.Render(fmt.Sprintf("(%s: no output yet)", j.State))}
```

  in `workRowFacts`, `case sched.Queued:` becomes `case sched.Queued, sched.Held:`, and its two `workWait(now.Sub(j.Submitted))` become `workWait(now.Sub(j.QueuedSince()))`.
  - `app/work.go`: in `askCancelWorkJob`, `m.confirmTask("Cancel "+ui.JobLabel(job)+"?", …)` becomes `m.confirmTask(cancelPrompt(job, m.queue.Jobs), …)`, and its comment gains "The prompt names the jobs held down its chain, which the cancel ends too." Add (with `slices` and `strconv` in the imports):

```go
// cancelPrompt is x's question for job (dependent jobs spec §7): it names
// the jobs held down its chain in jobs, which the cancel ends with it.
func cancelPrompt(job core.JobView, jobs []core.JobView) string {
	held := heldChain(jobs, job.ID)
	switch len(held) {
	case 0:
		return "Cancel " + ui.JobLabel(job) + "?"
	case 1:
		return fmt.Sprintf("Cancel %s and job %d that waits on it?", ui.JobLabel(job), held[0])
	}
	words := make([]string, len(held))
	for i, id := range held {
		words[i] = strconv.FormatInt(int64(id), 10)
	}
	return fmt.Sprintf("Cancel %s and jobs %s that wait on it?", ui.JobLabel(job), sched.JoinAnd(words))
}

// heldChain is the IDs of the jobs held down job id's chain in jobs: those
// that wait on it, those that wait on them, and so on, in ID order.
func heldChain(jobs []core.JobView, id sched.JobID) []sched.JobID {
	var out []sched.JobID
	for next := []sched.JobID{id}; len(next) > 0; {
		var more []sched.JobID
		for _, j := range jobs {
			if j.State == sched.Held && slices.Contains(next, j.After) {
				more = append(more, j.ID)
			}
		}
		out = append(out, more...)
		next = more
	}
	slices.Sort(out)
	return out
}
```

  In `frontWorkJob`, `if job.State != sched.Queued {` becomes `if !job.State.Waiting() {`, and its comment "Only a queued job can" becomes "Only a waiting job (queued or held) can".
  - `app/work_badge.go`: `case sched.Queued:` becomes `case sched.Queued, sched.Held:`, and the comment on the badge (if it says "only queued ones") gains "or held".
- [ ] **Run the tests again,** then `CGO_ENABLED=0 go test ./ui/ -run Work` and `CGO_ENABLED=0 go test ./app/ -run 'Work|OwnerJobBadges'`. Expected: PASS.

### Step 3.3: Loom's Claude context and USAGE

**Files:** Modify `session/loom_context_shared.md`, `USAGE.md`.

- [ ] **`session/loom_context_shared.md`:** in the work-queue bullet, after the sentence ending "use `--exclusive` for benchmarks.", insert:

```
When tests run for minutes after a heavy build, submit them as two jobs, the build and then the tests with `--after N`: the build's memory is freed while the tests run, the second job keeps the first's place in line, and one message reports both. Make the second job run what the first built rather than build it again (the test binaries, or an archive of them), since a rebuild there runs on a smaller claim.
```

- [ ] **`USAGE.md`, The Work Queue:** in the command block, after the `submit` line, add `loom job submit -d "tests" --after 14 -- cargo test   # held until job 14 ends done, then queued at its place`. After the **Order** bullet, add:

```
- **After another job.** `--after N` holds a job until job N ends. N must be the same session's (or, from a plain shell, a plain shell's) and not have failed. When N succeeds, the job joins the queue at N's place in line, ahead of anything submitted after N; when N fails, is cancelled or is lost, the job is cancelled, and so is every job held after it (`loom: job 15 was cancelled because job 14 failed (exit 101)`). Use it to free a build's memory while its tests run: submit the build, then, `--after` it, the step that runs what the build made (the test binaries, or an archive of them), since a step that rebuilds does so on its own, smaller claim, and its message says when it peaked at over twice that. A held job shows as `held` in `loom job list` and the work view, with what it waits on (`after job 14 (running)`), and the work view's `x` on its prerequisite names it. One message reports a chain: a build that succeeds sends none, and its dependent's message names it (`(2s in the queue, after job 14 "build tests", done in 3m10s)`); a build that fails sends its own, naming the jobs it cancelled. Each step is sized on its own: a command run after another learns apart from the same command run cold.
```

  In **Following**, after "`loom: job 14 started after 1m42s`", add "(`loom: job 15 held: runs after job 14 (running)` while a job waits on another, and `loom: job 15 started 2s after job 14 ended` once it is released)".
- [ ] **Run** `CGO_ENABLED=0 go test ./internal/cli/ -run 'TestLoomContext|TestJobContext'` and `CGO_ENABLED=0 go test ./session/ -run Context`. Expected: PASS.

### Step 3.4: Gates and commit

- [ ] `gofmt -l sched core internal ui app` (empty); `CGO_ENABLED=0 go build ./...`; `CGO_ENABLED=0 go vet ./...` (say golangci-lint was skipped); `go run ./tools/claudemd` (clean).
- [ ] `loom job run -d "dependent jobs: full suite" -- env CGO_ENABLED=0 go test ./...` (PASS).
- [ ] `loom job run -d "dependent jobs: race" -- env CC=clang CGO_ENABLED=1 go test -race ./...` (PASS).
- [ ] `loom job run -d "dependent jobs: e2e" -- env CGO_ENABLED=0 go test -tags e2e ./e2e/...` (PASS; the daemon's settle and the join changed).
- [ ] **Commit:**

```bash
git add internal/cli/job.go internal/cli/job_follow.go internal/cli/job_sizing.go internal/cli/job_list.go internal/cli/job_beliefs.go internal/cli/job_test.go ui/work.go ui/work_test.go app/work.go app/work_badge.go app/work_test.go app/work_badge_test.go session/loom_context_shared.md USAGE.md
git commit -m "feat(cli,app,ui): --after, held jobs in the list and the work view, and the context's sentence

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>" -- internal/cli ui app session/loom_context_shared.md USAGE.md
```

## Self-review against the spec

- §5.1 submit and refusals: Steps 2.1 (`prerequisiteOf`), 2.2 (`submitJob`), tests `TestJobQueue_AfterRefusesWhatCanNeverRun`, `…HeldAtItsPlace`, `…QueuesAtOnceAtTheBack`.
- §5.2 place in line: `submitJob`'s `Order`, `releaseDependents`' lower-of, `moveJobToFront`'s chain; tests `…ReleasesItsDependentsAtItsPlace`, `…MoveToFrontCarries…`.
- §5.3 release, `Ready`, the clock, the forecast, `Grant`: Steps 1.3, 1.4, 2.1; `Grant` needs no change (held jobs never reach `Admit`'s `Waiting`).
- §5.4 cascade and cancel: Steps 2.1, 2.2; tests `…DoesNotSucceedCancelsItsChain`, `…CancellingAHeldJob…`, `…APauseEndsHeldJobs…`.
- §5.5 messages: Steps 2.1, 2.4; tests `…AChainThatSucceeds…`, `…AFailedJobsMessage…`, `…AQuietJobsCascade…`, `…ADependentThatPeaked…`, `…ADependentsFirstLine…`.
- §5.6 sizing, signatures, beliefs version, advisor, display: Steps 1.5–1.8, 2.3; tests `…ADependentLearnsApart…`, `TestAdvisor_ADependentsRequest…`, `TestFormatBelief_NamesTheCommandItRunsAfter`, `TestJobBeliefCommand…`.
- §5.7 load: Steps 2.1 (`resolveHeld`), 2.3; test `…ABootResolves…`.
- §6 CLI, §7 work view, §8 context: Package 3.
- §9 wire: the fields of Steps 1.2, 1.5, 1.8, 2.3; the protocol check in Step 2.6.
