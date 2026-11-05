# sccache measurement Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Queued cargo jobs run sccache client-side, so their compiles are measured in their own tree, and on Linux the 3 s sampler reads `/proc`, counts reaped children's CPU and sums each job's session.

**Architecture:** `sched.Hints` gains `SCCACHE_CLIENT_SIDE=1`. `sched.Sample` splits by platform: Linux scans `/proc/<pid>/stat` (`parseProcStat`), other platforms keep `ps`. `Proc` gains `SID`, `Snapshot` gains `Session`, and `core`'s `applyUsage` sums the job's session while the lost-job kill proof keeps `Snapshot.Group`.

**Tech Stack:** Go (module `github.com/aidan-bailey/loom`), testify, Linux procfs, sccache 0.18.

**Spec:** [`docs/superpowers/specs/2026-11-05-sccache-measurement-design.md`](../specs/2026-11-05-sccache-measurement-design.md). Execution: inline (superpowers:executing-plans), no checkpoint pauses; one commit.

---

## Ground rules

- Scratch files in the session scratchpad; commit by path with `--pathspec-from-file` (zsh doesn't split a variable into words).
- Quick checks directly (`CGO_ENABLED=0 go test ./sched/ -run X`); the full suite, the race run and e2e through `loom job run`, in the background.
- After touching the sampler files: `GOOS=windows CGO_ENABLED=0 go build ./...` and `GOOS=darwin CGO_ENABLED=0 go build ./...`.
- `gofmt -l sched core`; `CGO_ENABLED=0 go vet ./sched/... ./core/...`; golangci-lint skipped (the dev shell's v2 rejects the config).

## Files

| File | Responsibility |
|---|---|
| `sched/hints.go`, `sched/hints_test.go` | the hint |
| `sched/sample.go` | `Proc.SID`, `Snapshot.Session`, `userHZ`, `parseProcStat`, `ParsePS` setting `SID` |
| `sched/sample_linux.go` (new) | `Sample` from `/proc` |
| `sched/sample_other.go` (new) | `Sample` from `ps` (moved from `sample.go`) |
| `sched/sample_test.go` | `TestParsePS` with `SID`, `TestParseProcStat`, `TestSnapshot_Session`, `TestSample_ListsThisProcess` with `SID` |
| `sched/sample_linux_test.go` (new) | the live session tests |
| `sched/sccache_live_test.go` (new, `-tags sccache_live`) | the contract |
| `core/queue_watch.go`, `core/queue_watch_test.go` | `applyUsage` by session; `wqSnap` with `SID`; the session test |
| `sched/README.md`, `sched/CLAUDE.md`, `docs/claude/the-work-queue.md`, `USAGE.md` | docs and the rule |

## Package: the hint and the sampler

### Step 1: The hint

- [ ] **Test** (`sched/hints_test.go`): in `TestHints_AddsWhatTheEnvironmentLacks` add `"SCCACHE_CLIENT_SIDE=1",` after `"GOFLAGS=-p=4",`, and the length check becomes `assert.Len(t, Hints(nil, 2, 1), 8, "an empty environment gets the seven hints and LOOM_JOB")`. In `TestHints_KeepsWhatTheSubmitterSet` add `"SCCACHE_CLIENT_SIDE=0"` to `env` (after `"MAKEFLAGS=",`) and to the expected list in the same place, and no hint line for it.
- [ ] **Run:** `CGO_ENABLED=0 go test ./sched/ -run TestHints`. Expected: FAIL.
- [ ] **`sched/hints.go`:** add `{"SCCACHE_CLIENT_SIDE", "1"},` after the `GOFLAGS` hint, and the doc comment's list ends "…, CMAKE_BUILD_PARALLEL_LEVEL=N, GOFLAGS=-p=N and SCCACHE_CLIENT_SIDE=1, which has sccache run each compile in its client, inside the job's tree, rather than in its shared daemon (sccache measurement spec §5.1)."
- [ ] **Run:** Expected: PASS.

### Step 2: `Proc.SID`, `Snapshot.Session` and `parseProcStat`

- [ ] **Tests** (`sched/sample_test.go`):
  - In `TestParsePS`, every expected `Proc` gains `SID` equal to its `PGID` (`{PID: 1, PGID: 1, SID: 1, …}`).
  - Append:

```go
func TestParseProcStat(t *testing.T) {
	// pid (comm) state ppid pgrp session tty tpgid flags minflt cminflt majflt
	// cmajflt utime stime cutime cstime prio nice threads itreal start vsize rss …
	line := "4242 (cargo (build) x) S 4200 4100 4000 0 -1 4194304 10 0 0 0 150 50 300 25 20 0 4 0 1000 123456 2560 18446744073709551615"
	p, err := parseProcStat([]byte(line), 4)
	require.NoError(t, err)
	assert.Equal(t, Proc{PID: 4242, PGID: 4100, SID: 4000, RSSKB: 10240, CPU: 5250 * time.Millisecond}, p,
		"comm runs to the last ')', and CPU is the process's time and its reaped children's")

	zombie := "77 (rustc) Z 4242 4100 4000 0 -1 4227084 0 0 0 0 90 10 0 0 20 0 1 0 1001 0 0 18446744073709551615"
	p, err = parseProcStat([]byte(zombie), 4)
	require.NoError(t, err)
	assert.Equal(t, Proc{PID: 77, PGID: 4100, SID: 4000, CPU: time.Second}, p, "a zombie keeps its CPU time until it is reaped")

	for _, bad := range []string{"", "12 S 1 1 1", "x (a) S 1 1 1 0 -1 0 0 0 0 0 1 1 1 1 20 0 1 0 1 1 1", "12 (a) S 1 1"} {
		_, err := parseProcStat([]byte(bad), 4)
		assert.Error(t, err, "%q", bad)
	}
}

func TestSnapshot_Session(t *testing.T) {
	snap := Snapshot{Procs: []Proc{
		{PID: 100, PGID: 100, SID: 100, RSSKB: 1000, CPU: 2 * time.Second},
		{PID: 101, PGID: 101, SID: 100, RSSKB: 500, CPU: time.Second}, // timeout: a group of its own, the shim's session
		{PID: 200, PGID: 200, SID: 200, RSSKB: 9000, CPU: time.Hour},
	}}
	rss, cpu, members := snap.Session(100)
	assert.Equal(t, int64(1500), rss)
	assert.Equal(t, 3*time.Second, cpu)
	assert.Equal(t, 2, members)
	rss, _, members, _ = snap.Group(100)
	assert.Equal(t, int64(1000), rss, "the group leaves out the member that changed group")
	assert.Equal(t, 1, members)
	_, _, members = snap.Session(999)
	assert.Zero(t, members)
}
```

- [ ] **Run:** `CGO_ENABLED=0 go test ./sched/ -run 'TestParsePS|TestParseProcStat|TestSnapshot_Session'`. Expected: FAIL to compile.
- [ ] **`sched/sample.go`:** `Proc` becomes:

```go
// Proc is one process in a Snapshot.
type Proc struct {
	PID   int
	PGID  int
	SID   int // its session; its process group where the sample can't tell (ps)
	RSSKB int64
	CPU   time.Duration // CPU time so far, user and system together; on Linux its waited-for children's too
}
```

  `parsePSLine` sets `SID: pgid` beside `PGID: pgid`, and `ParsePS`'s comment adds "SID is the process group: ps can't say the session on every platform". Move `Sample` (with its `os/exec` import) to `sched/sample_other.go` under `//go:build !linux`, unchanged. Add to `sample.go`:

```go
// userHZ is the unit of the CPU times in /proc/<pid>/stat: clock ticks, 100
// a second on every Linux (USER_HZ).
const userHZ = 100

// parseProcStat reads one /proc/<pid>/stat (sccache measurement spec §5.2):
// "pid (comm) state ppid pgrp session … utime stime cutime cstime … rss …",
// the times in ticks of 1/userHZ s and rss in pages of pageKB KB. comm is
// everything from the first "(" to the last ")", since a name may hold
// spaces and parentheses. CPU is utime + stime + cutime + cstime: the
// process's own time and its waited-for children's, so a session's sum
// keeps the processes reaped inside it.
func parseProcStat(raw []byte, pageKB int64) (Proc, error) {
	s := string(raw)
	open, end := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if open < 1 || end < open {
		return Proc{}, fmt.Errorf("stat %q has no (comm)", s)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(s[:open]))
	if err != nil {
		return Proc{}, fmt.Errorf("stat %q: pid: %w", s, err)
	}
	f := strings.Fields(s[end+1:]) // f[0] is field 3, the state: field n is f[n-3]
	if len(f) < 22 {
		return Proc{}, fmt.Errorf("stat %q has %d fields after comm, want 22 or more", s, len(f))
	}
	var n [7]int64 // pgrp, session, utime, stime, cutime, cstime, rss
	for i, idx := range []int{2, 3, 11, 12, 13, 14, 21} {
		if n[i], err = strconv.ParseInt(f[idx], 10, 64); err != nil {
			return Proc{}, fmt.Errorf("stat %q: field %d: %w", s, idx+3, err)
		}
	}
	ticks := n[2] + n[3] + n[4] + n[5]
	return Proc{
		PID: pid, PGID: int(n[0]), SID: int(n[1]), RSSKB: n[6] * pageKB,
		CPU: time.Duration(ticks) * time.Second / userHZ,
	}, nil
}

// Session sums the memory and CPU time of the processes in session sid, and
// counts them (sccache measurement spec §5.2): a job's usage, since the shim
// leads its session and a descendant leaves it only by calling setsid. Where
// the sample can't tell sessions (ps), a process's session is its group.
func (s Snapshot) Session(sid int) (rssKB int64, cpu time.Duration, members int) {
	for _, p := range s.Procs {
		if p.SID == sid {
			rssKB += p.RSSKB
			cpu += p.CPU
			members++
		}
	}
	return rssKB, cpu, members
}
```

- [ ] **`sched/sample_linux.go`** (new):

```go
//go:build linux

package sched

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

// Sample reads every process from /proc (sccache measurement spec §5.2):
// its group, its session, its resident memory and its CPU time with its
// waited-for children's (parseProcStat). A process that exits between the
// listing and its read is skipped; a stat it cannot parse spoils the whole
// sample, which would understate a job's use. The scan stops when ctx is
// done; a scan cut short or that finds no process is an error, and the
// caller skips that sample.
func Sample(ctx context.Context) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, fmt.Errorf("reading /proc: %w", err)
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return Snapshot{}, fmt.Errorf("reading /proc: %w", err)
	}
	pageKB := int64(os.Getpagesize() / 1024)
	var procs []Proc
	for i, e := range entries {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return Snapshot{}, fmt.Errorf("reading /proc: %w", err)
			}
		}
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		raw, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue // it exited since the listing
		}
		p, err := parseProcStat(raw, pageKB)
		if err != nil {
			return Snapshot{}, err
		}
		procs = append(procs, p)
	}
	if len(procs) == 0 {
		return Snapshot{}, errors.New("/proc listed no processes")
	}
	return Snapshot{Taken: time.Now(), Procs: procs}, nil
}
```

- [ ] **Run:** `CGO_ENABLED=0 go test ./sched/ -run 'TestParsePS|TestParseProcStat|TestSnapshot_Session|TestSample'`, then `GOOS=windows CGO_ENABLED=0 go build ./... && GOOS=darwin CGO_ENABLED=0 go build ./...`. Expected: PASS and both builds clean. (`TestSample_FailsOnADoneContext` holds on Linux through the first `ctx.Err()` check; its `ErrorIs(context.Canceled)` passes since the error wraps it.)

### Step 3: The live session tests

- [ ] **`sched/sample_test.go`:** `TestSample_ListsThisProcess` calls `requirePS(t)` only when `runtime.GOOS != "linux"`, and adds:

```go
	sid, err := syscall.Getsid(0)
	require.NoError(t, err)
	if runtime.GOOS == "linux" {
		assert.Equal(t, sid, self.SID, "/proc gives the session")
	} else {
		assert.Equal(t, self.PGID, self.SID, "ps gives the group in its place")
	}
```

- [ ] **`sched/sample_linux_test.go`** (new):

```go
//go:build linux

package sched

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startSession starts sh -c script as the leader of a session of its own,
// as the daemon starts a shim, and kills what is left of it when the test
// ends: every process of the session, whatever its group.
func startSession(t *testing.T, script string) int {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	require.NoError(t, cmd.Start())
	sid := cmd.Process.Pid
	t.Cleanup(func() {
		if snap, err := Sample(context.Background()); err == nil {
			for _, p := range snap.Procs {
				if p.SID == sid && p.PID != sid {
					_ = syscall.Kill(p.PID, syscall.SIGKILL)
				}
			}
		}
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return sid
}

// TestSample_ASessionKeepsItsReapedChildrensCPU: children that ran and were
// reaped between two samples still count, in their parent's children's time.
func TestSample_ASessionKeepsItsReapedChildrensCPU(t *testing.T) {
	sid := startSession(t, `for k in 1 2 3; do sh -c 'i=0; while [ $i -lt 200000 ]; do i=$((i+1)); done'; done; exec sleep 30`)
	require.Eventually(t, func() bool {
		comm, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", sid))
		return err == nil && strings.TrimSpace(string(comm)) == "sleep"
	}, 60*time.Second, 20*time.Millisecond, "the busy children to finish")

	snap, err := Sample(context.Background())
	require.NoError(t, err)
	_, cpu, members := snap.Session(sid)
	assert.Equal(t, 1, members, "only the sleep is left")
	assert.GreaterOrEqual(t, cpu, 100*time.Millisecond, "the three reaped children's CPU time")
}

// TestSample_ASessionKeepsAChildThatChangedGroup: coreutils timeout moves
// itself and its command into a group of their own; the session keeps them.
func TestSample_ASessionKeepsAChildThatChangedGroup(t *testing.T) {
	if _, err := exec.LookPath("timeout"); err != nil {
		t.Skip("timeout is not installed")
	}
	sid := startSession(t, "timeout 30 sleep 30; true")
	var snap Snapshot
	require.Eventually(t, func() bool {
		s, err := Sample(context.Background())
		if err != nil {
			return false
		}
		snap = s
		_, _, members := s.Session(sid)
		return members == 3
	}, 10*time.Second, 20*time.Millisecond, "sh, timeout and sleep in the session")
	_, _, members, _ := snap.Group(sid)
	assert.Equal(t, 1, members, "only sh is left in the leader's group")
}
```

- [ ] **Run:** `CGO_ENABLED=0 go test ./sched/ -run 'TestSample' -count=3`. Expected: PASS, three times.

### Step 4: Usage by session in the model

- [ ] **Test** (`core/queue_watch_test.go`): `wqSnap` gives each process its session (`SID: pgid` for the shim and its child, `SID: 7` for the bystander), and append:

```go
// A job's usage sums its session, not its process group: a child that moved
// to a group of its own (coreutils timeout) still counts (sccache measurement
// spec §5.2).
func TestJobQueue_UsageSumsTheJobsSession(t *testing.T) {
	const pid = 4242
	m := wqModel(t)
	rec := wqEnded(1, 0)
	rec.State, rec.Exit, rec.Ended, rec.PID, rec.Notify = sched.Running, nil, time.Time{}, pid, false
	wqSeed(t, m, rec)
	snap := &sched.Snapshot{Taken: time.Now(), Procs: []sched.Proc{
		{PID: pid, PGID: pid, SID: pid, RSSKB: 100},
		{PID: pid + 1, PGID: pid + 1, SID: pid, RSSKB: 900},
		{PID: 7, PGID: 7, SID: 7, RSSKB: 1 << 30},
	}}
	m.Deliver(queuePassResult{at: snap.Taken, obs: []jobObs{{id: 1, pid: pid}}, snap: snap})
	assert.Equal(t, int64(1000), m.queue.usage[1].RSSKB)
	assert.Equal(t, int64(1000), wqRecord(t, m, 1).PeakRSSKB)
}
```

- [ ] **Run:** `CGO_ENABLED=0 go test ./core/ -run 'TestJobQueue_Usage'`. Expected: FAIL (100, the group alone).
- [ ] **`core/queue_watch.go`, `applyUsage`:** `rss, cpu, _ := r.snap.Session(rec.PID)`, and its comment's "its group's memory now" becomes "its session's memory now (the shim leads the job's session; sccache measurement spec §5.2)". `orphanedGroup` is unchanged.
- [ ] **Run:** `CGO_ENABLED=0 go test ./core/ -run 'TestJobQueue_Usage|Orphan|Lost'`. Expected: PASS.

### Step 5: The contract test

- [ ] **`sched/sccache_live_test.go`** (new):

```go
//go:build sccache_live

package sched

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSccache_ClientSideCompilesInTheCallersTree pins what the hint relies on
// (sccache measurement spec §5.1): with SCCACHE_CLIENT_SIDE=1 a compile's CPU
// is in the tree of the process that ran cargo; without it, sccache's daemon
// runs the compiler and the caller's tree shows almost none. It runs a
// private daemon on a private cache. Run with: go test -tags sccache_live -run
// TestSccache ./sched/
func TestSccache_ClientSideCompilesInTheCallersTree(t *testing.T) {
	sccache, err := exec.LookPath("sccache")
	if err != nil {
		t.Skip("sccache is not installed")
	}
	if _, err := exec.LookPath("cargo"); err != nil {
		t.Skip("cargo is not installed")
	}
	dir := t.TempDir()
	crate := filepath.Join(dir, "big")
	require.NoError(t, exec.Command("cargo", "new", "--lib", "--offline", crate).Run())
	var src strings.Builder
	for i := range 2000 {
		src.WriteString("pub fn f" + strconv.Itoa(i) + "(x: u64) -> u64 { let mut a = x; for j in 0..7 { a = a.wrapping_mul(6364136223846793005).wrapping_add(j); } a ^ " + strconv.Itoa(i) + " }\n")
	}
	require.NoError(t, os.WriteFile(filepath.Join(crate, "src", "lib.rs"), []byte(src.String()), 0o644))

	treeCPU := func(mode string) time.Duration {
		cmd := exec.Command("sh", "-c", "cargo build -q --offline --release; times")
		cmd.Dir = crate
		cmd.Env = append(os.Environ(), "RUSTC_WRAPPER="+sccache, "SCCACHE_DIR="+filepath.Join(dir, "cache-"+mode),
			"SCCACHE_SERVER_PORT=42617", "SCCACHE_IDLE_TIMEOUT=60", "CARGO_INCREMENTAL=0", "SCCACHE_LOG=",
			"CARGO_TARGET_DIR="+filepath.Join(dir, "target-"+mode), "SCCACHE_CLIENT_SIDE="+map[string]string{"client": "1"}[mode])
		out, err := cmd.Output()
		require.NoError(t, err, "%s", out)
		lines := strings.Fields(strings.TrimSpace(string(out)))
		var total time.Duration
		for _, f := range lines[len(lines)-2:] { // the children's user and system time: 0m1.335s 0m0.417s
			m, s, _ := strings.Cut(strings.TrimSuffix(f, "s"), "m")
			mins, _ := strconv.Atoi(m)
			secs, _ := strconv.ParseFloat(s, 64)
			total += time.Duration(mins)*time.Minute + time.Duration(secs*float64(time.Second))
		}
		return total
	}
	t.Cleanup(func() {
		stop := exec.Command(sccache, "--stop-server")
		stop.Env = append(os.Environ(), "SCCACHE_SERVER_PORT=42617")
		_ = stop.Run()
	})
	server, client := treeCPU("server"), treeCPU("client")
	assert.Less(t, server, 300*time.Millisecond, "server-side: the daemon ran the compiler")
	assert.Greater(t, client, server+200*time.Millisecond, "client-side: the compile is in the caller's tree")
}
```

- [ ] **Run:** `go test -tags sccache_live -run TestSccache ./sched/`. Expected: PASS on fresco (it skips where sccache or cargo is missing).

### Step 6: Docs

- [ ] **`sched/README.md`:** the `hints.go` entry and any list of the hint variables add `SCCACHE_CLIENT_SIDE=1`; the Files list gains "`sample_linux.go`, `sample_other.go` — `Sample` from `/proc` (Linux: each process's session and its CPU with its reaped children's) or from `ps`"; the runtime section's sampler sentence says a job's usage sums its session, and the lost-job proof its group.
- [ ] **`sched/CLAUDE.md`:** add the rule:
  - "**Sum a job's usage over its session (`Snapshot.Session`), but prove a lost job's group by its group (`Snapshot.Group`).** The shim leads both; a child that changes group (coreutils `timeout`) stays in the session, so usage by group misses it, while the group signal reaches only the group, so a proof by session would kill on evidence about processes the signal never reaches. **Enforced** by `TestSnapshot_Session` and `TestJobQueue_UsageSumsTheJobsSession`; the proof's tests (`orphanedGroup`) stand unchanged."
- [ ] **`docs/claude/the-work-queue.md`:** the sampling sentence (rule 8 / the runtime paragraph that names `ps`) says Linux reads `/proc` with reaped children's CPU, usage sums the session, and the kill proof keeps the group.
- [ ] **`USAGE.md`:** the **Parallelism** bullet adds "and `SCCACHE_CLIENT_SIDE=1`, so sccache runs each compile inside the job, where it is measured, rather than in its shared daemon (set it to `0` to keep a job server-side)"; where the work view's live cores are described, "it counts short-lived processes (on Linux)".
- [ ] **Run:** `go run ./tools/claudemd -v` and `CGO_ENABLED=0 go test . ./tools/claudemd`. Expected: PASS within budgets.

### Step 7: Verify and commit

- [ ] `gofmt -l sched core`; `CGO_ENABLED=0 go vet ./sched/... ./core/...`; both cross-builds.
- [ ] Mutation check: `Session` matching `PGID` instead of `SID`, `parseProcStat` dropping `cutime + cstime`, and `applyUsage` back on `Group` each turn a test red.
- [ ] `loom job run -d "sccache measurement: full suite" -- env CGO_ENABLED=0 go test ./...`, the race run, and `-tags e2e ./e2e/...` (the sampler feeds the daemon), in the background. Expected: all exit 0.
- [ ] Commit by path: the files of the table above, the spec untouched unless amended, and this plan.

## Self-review

- **Spec coverage:** §5.1 → Step 1 (and its opt-out in Step 1's test); §5.2 → Steps 2–4; §5.3 → Step 6's USAGE text needs no code; §6 → Step 6; §7 → nothing to do; §8 → the Files table; §9 → `Sample`'s errors (Step 2); §10 → Steps 1–5 and 7; §11 → one commit.
- **Names:** `Proc.SID`, `Snapshot.Session(sid) (rssKB, cpu, members)`, `parseProcStat(raw, pageKB)`, `userHZ`, `startSession` are used the same way throughout.

## Execution notes (2026-11-05)

- Step 3: Go's `syscall` has no `Getsid` on Linux; `TestSample_ListsThisProcess` calls `syscall.RawSyscall(syscall.SYS_GETSID, 0, 0, 0)`, which keeps `golang.org/x/sys` an indirect dependency.
- Step 5: the contract test drops every `SCCACHE_*` variable and `RUSTC_WRAPPER` from the inherited environment rather than setting `SCCACHE_LOG` empty, since a log setting in the developer's shell would turn client-side mode off; it passed against sccache 0.18.0 and cargo 1.95.
- Step 7: the four mutants (`Session` matching the group, `parseProcStat` without `cutime + cstime`, `applyUsage` on `Group`, the hint removed) each turned a test red. Full suite (job 118), race (job 117) and e2e (job 119) passed.
