# Cgroup confinement and greedy admission Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every job runs in its own systemd scope, so the queue measures it exactly, shares CPU by weight below the agents, admits on forecast memory overlap within live headroom, and squeezes, pauses or requeues a job when the bet loses.

**Architecture:** `sched` gains the cgroup readers and unit names, a `Confiner` over the user's systemd (go-systemd on D-Bus), usage curves, predictions, the greedy fit inside a refactored `Admit`, and a pure ladder. The shim joins its scope before it starts the command. `core` probes the confiner off its loop, ensures the slice and writes the scope into `spec.json` at start, reads cgroups in its sampling pass, admits greedily when contained, and runs the ladder after each pass, making every D-Bus call from a spawned job.

**Tech Stack:** Go (module `github.com/aidan-bailey/loom`), `github.com/coreos/go-systemd/v22` v22.5.0 and `github.com/godbus/dbus/v5` v5.1.0 (vendored), testify, cobra, Bubble Tea v2 / lipgloss v2.

**Spec:** [`docs/superpowers/specs/2026-11-05-cgroup-greedy-scheduler-design.md`](../specs/2026-11-05-cgroup-greedy-scheduler-design.md). Execution: inline (superpowers:executing-plans), no checkpoint pauses; one commit per package.

---

## Ground rules for every package

- Scratch files go in the session scratchpad, never the worktree. Commit by path: `git add <paths>`, then `git commit -m … -- <paths>`; never `-a`, `git add -A` or `git add .`.
- Quick checks run directly: `gofmt -l`, `git`, reading files. Anything that compiles goes through loom's work queue: one package's tests with `loom job run -d "cgroups: …" -- env CGO_ENABLED=0 go test ./sched/ -run TestX`; the full suite, race and e2e with Bash's `run_in_background` (`… -- env CGO_ENABLED=0 go test ./...`, `… -- env CC=clang CGO_ENABLED=1 go test -race ./...`, `… -- env CGO_ENABLED=0 go test -tags e2e ./e2e/...`).
- Format with `gofmt -w` on the files touched; check `gofmt -l $(git ls-files '*.go' | grep -v '^vendor/')`.
- `core/CLAUDE.md` is full (19971/20000 bytes): the model's rules go in `docs/claude/the-work-queue.md`. `sched/CLAUDE.md` has room (14117/20000).
- golangci-lint: the dev shell's v2 rejects the config; run `CGO_ENABLED=0 go vet ./...` and say lint was skipped.
- After touching `sched`'s platform files: `GOOS=windows CGO_ENABLED=0 go build ./...` and `GOOS=darwin CGO_ENABLED=0 go build ./...` (through the queue).
- No test reaches the real user bus but the opt-in live test (`-tags systemd_live`); Step 1.2 makes that hold for every package.

## Spec amendments made while planning

Recorded in the spec (new §15) in Step 1.1:

1. **Unit names carry a tag of the jobs directory and the attempt:** the slice is `loom-<tag>.slice` (under `loom.slice`) and a scope `loom-<tag>-job-<id>-<attempt>.scope`, the tag being 8 hex digits of the SHA-256 of the jobs directory. A loomdev sandbox under the same user manager never shares a name with the real daemon, and a requeued attempt never waits for its predecessor's scope to go. (§4, §5.1, §5.2)
2. **`spec.json` carries the scope to create (`scope`),** written by the daemon at start when jobs are contained; without it the shim never touches the bus (fallback mode, and every test shim). (§5.1)
3. **The shim writes the cgroup it landed in (`cgroup`, from `/proc/self/cgroup`) into `started.json`;** the daemon never asks D-Bus for a path. (§5.3)
4. **Scopes set `CollectMode=inactive-or-failed`,** so a failed (OOM-killed) scope is collected at once. (§5.1)
5. **Cancels keep the group SIGTERM behind the lock proof; the SIGKILL escalation of a contained job also writes its scope's `cgroup.kill`,** which reaches processes that left the group. The rung-3 kill writes `cgroup.kill` too. `KillUnit` is not used, and the lost-job proof is unchanged. (§5.4, §8.2)
6. **The daemon ensures the slice before each contained start** (set its properties, or create it as a transient slice): an empty slice can be collected with its properties. (§5.2)
7. **Time alignment uses each profiled run's own wall times, not a rate.** A curve step is `[until, level, wall]`; a running job's time to a later step is that run's wall time from its current progress to the step, a candidate's from its start; the prediction is the most of every run's spans at each moment. (§6.1, §7.2 items 1–2)
8. **The curve test pins "never below the run",** as the staircase's does, not equality with the staircase: the two compress differently, and holds keep reading the stored staircase. (§6.2)
9. **`interactive_reserve_gb` is 1–1024** (0 takes the default, as every scheduler key does). (§10)
10. **The requeue message records no notice fate,** so the job's end message still comes. (§8.4)
11. **`Admit` reports the head the starvation guard started (`Decision.Starved`),** and the record keeps it (`starved`) for the ladder's victim order. (§8.2)
12. **A kernel OOM kill is read from the slice's `memory.events` `oom_kill`** (hierarchical) at each pass: a job that ends by a SIGKILL nobody asked for while that count rose is the OOM's victim. (§8.1, §12)
13. **The greedy forecast anchors a simulated start's prediction at its simulated start, and ignores the ladder's hold-off.** (§7.4)
14. **Every test binary points `DBUS_SESSION_BUS_ADDRESS` at a dead socket** (`testenv.IsolateLoomDirs`), keeping the real address in `LOOM_TEST_REAL_DBUS` for the live test alone. (§13)
15. **No e2e test reaches the real bus:** the live contract test covers D-Bus, and core tests cover the wiring with a fake confiner. (§13.3)
16. **A paused job is thawed before a cancel's SIGTERM,** since a frozen process can't act on it. (§8)
17. **The daemon reconciles each contained job's rung from its cgroup at every pass** (`cgroup.events` `frozen`, `memory.high`), which covers a restart. (§12)
18. **A unit systemd no longer knows (its user manager restarted) needs no state of its own:** its ladder calls fail, are logged, shown (`LadderErr`) and retried, and its rung follows its cgroup. (§12)

## Files

| File | Package | Responsibility |
|---|---|---|
| `go.mod`, `go.sum`, `vendor/` | 1 | go-systemd v22.5.0, godbus v5.1.0 |
| `internal/testenv/testenv.go` (+ test) | 1 | the dead session bus, `EnvDBus`, `EnvRealDBus` |
| `sched/units.go` (new, + test) | 1 | `SliceName`, `ScopeName`, `CPUWeight` |
| `sched/cgroup.go` (new, + test) | 1 | `CgroupRoot`, `CgroupSample`, `ReadCgroup`, `CgroupKill`, `SelfCgroup`, `parseMemAvailable` |
| `sched/meminfo_linux.go`, `sched/meminfo_other.go` (new) | 1 | `ReadMemAvailableKB` |
| `sched/confine.go` (new) | 1 | `ScopeSpec`, `SliceProps`, `Confiner` |
| `sched/confine_linux.go`, `sched/confine_other.go` (new) | 1 | `NewConfiner` |
| `sched/confine_live_test.go` (new) | 1 | the live contract test |
| `sched/files.go` | 1, 4 | `Spec.Scope`, `Started.Unit/Cgroup/Uncontained`; `ArchiveAttempt` (4) |
| `sched/shim_unix.go` | 1 | `joinScope` before `writeStarted` |
| `sched/job.go` | 1, 3, 4 | record fields; policy fields; `Submit.NoRequeue` |
| `config/settings.go` | 1, 3 | `contain`, `jobs_cpu_weight`; `interactive_reserve_gb`, `cpu_pressure_pct`, `cpu_overcommit` |
| `sched/policy.go` | 1, 3 | their defaults and bounds |
| `sched/profile.go` | 2 | `TracePoint.At` |
| `sched/curve.go` (new, + test) | 2 | `CurveStep`, `Curve`, `BuildCurve`, `Curve.Validate`, `CurvedRuns` |
| `sched/belief.go` | 2, 4 | `Run.Curve`, `AddRun`; `Run.Laddered` (4) |
| `sched/predict.go` (new, + test) | 3 | `Span`, `Prediction`, `Flat`, `Predict`, `PeakSum` |
| `sched/greedy.go` (new, + test) | 3 | `Greedy`, `Budget`, `greedyFit`, `AdmitGreedy` |
| `sched/admit.go` | 3 | `fitter`, `admitWith`, `claimFit`, `Decision.Starved` |
| `sched/forecast.go` | 3 | `forecastWith`, `ForecastGreedy` |
| `sched/ladder.go` (new, + test) | 4 | `Rung`, `LadderJob`, `LadderInput`, `LadderState`, `Action`, `Ladder` |
| `core/queue.go` | 1, 3, 4 | queue state fields |
| `core/queue_contain.go` (new, + test) | 1 | probe, `containWhy`, fallback |
| `core/queue_admit.go` | 1, 3 | the scope at start; the greedy pass |
| `core/queue_watch.go` | 1, 4 | cgroup reads in the pass, usage from cgroups, `cgroup.kill`; requeue at settle, OOM |
| `core/queue_greedy.go` (new, + test) | 3 | `greedyInputs`, predictions |
| `core/queue_ladder.go` (new, + test) | 4 | `stepLadder`, actions, `requeueJob`, the slice's `MemoryHigh` |
| `core/queue_beliefs.go` | 2 | curves at settle, checked at load |
| `core/queue_ops.go`, `core/queue_notice.go` | 4 | `NoRequeue` at submit, thaw before cancel, the requeue message |
| `core/queue_view.go` | 1, 3, 4 | view fields |
| `core/seams.go` | 1 | `SetConfinerForTest`, `SetCgroupRootForTest` |
| `ui/work.go` | 1, 3, 4 | header and job texts |
| `internal/cli/job.go`, `job_follow.go`, `job_sizing.go` | 4 | `--no-requeue`, requeue and rung lines, list text |
| Docs | each | `USAGE.md`, `sched/README.md`, `sched/CLAUDE.md`, `internal/testenv/CLAUDE.md`, `docs/claude/the-work-queue.md`, `docs/ARCHITECTURE.md`, the spec |

---

## Package 1: confinement and measurement

### Step 1.1: Amend the spec

- [ ] Append `## 15. Planning amendments` to the spec with the 18 amendments above, one line each, and fix the names in §4, §5.1, §5.2 and §5.4 to match amendments 1 and 5.

### Step 1.2: The dead session bus in every test binary

**Files:** `internal/testenv/testenv.go`, `internal/testenv/testenv_test.go`, `internal/testenv/CLAUDE.md`

- [ ] **Write the failing test** in `internal/testenv/testenv_test.go`:

```go
// TestIsolateLoomDirs_PointsTheSessionBusNowhere: a test binary reaches no
// real user bus, and the live test finds the real address where it was.
func TestIsolateLoomDirs_PointsTheSessionBusNowhere(t *testing.T) {
	t.Setenv(EnvDBus, "unix:path=/run/user/1000/bus")
	t.Setenv(EnvRealDBus, "")
	cleanup, err := IsolateLoomDirs()
	require.NoError(t, err)
	t.Cleanup(cleanup)
	assert.Equal(t, "unix:path=/run/user/1000/bus", os.Getenv(EnvRealDBus))
	addr := os.Getenv(EnvDBus)
	require.True(t, strings.HasPrefix(addr, "unix:path="), addr)
	_, statErr := os.Stat(strings.TrimPrefix(addr, "unix:path="))
	assert.ErrorIs(t, statErr, fs.ErrNotExist, "the bus socket must not exist")
}
```

- [ ] **Run it:** `loom job run -d "cgroups: testenv red" -- env CGO_ENABLED=0 go test ./internal/testenv/ -run TestIsolateLoomDirs_PointsTheSessionBusNowhere` → FAIL (`EnvDBus` undefined).
- [ ] **Implement** in `testenv.go`: constants `EnvDBus = "DBUS_SESSION_BUS_ADDRESS"` and `EnvRealDBus = "LOOM_TEST_REAL_DBUS"`; in `IsolateLoomDirs`, after the unsets:

```go
	if real := os.Getenv(EnvDBus); real != "" && os.Getenv(EnvRealDBus) == "" {
		if err := os.Setenv(EnvRealDBus, real); err != nil {
			_ = os.RemoveAll(dir)
			return nil, fmt.Errorf("keep %s: %w", EnvDBus, err)
		}
	}
	if err := os.Setenv(EnvDBus, "unix:path="+filepath.Join(dir, "no-bus")); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("set %s: %w", EnvDBus, err)
	}
```

and extend the doc comment: it points the session bus at a socket that doesn't exist, keeping the real address in `LOOM_TEST_REAL_DBUS`.
- [ ] **Run it again** → PASS.
- [ ] Add a rule to `internal/testenv/CLAUDE.md`: **Point the session bus nowhere in `IsolateLoomDirs` (`EnvDBus`), keeping the real address in `EnvRealDBus` for `-tags systemd_live` alone.** A test shim told to contain itself would otherwise create real scopes in the developer's systemd, and a core test with a real confiner could freeze their units. **Enforced** by `TestIsolateLoomDirs_PointsTheSessionBusNowhere`.

### Step 1.3: Vendor go-systemd and godbus

- [ ] Run through the queue (it compiles): `loom job run -d "cgroups: vendor go-systemd" -- sh -c 'go get github.com/coreos/go-systemd/v22@v22.5.0 github.com/godbus/dbus/v5@v5.1.0 && go mod tidy && go mod vendor'`.
- [ ] Check `go.mod` lists both as direct requirements once Step 1.5 imports them (`go mod tidy` drops an unused one: rerun it after Step 1.5), and that `vendor/modules.txt` has `github.com/coreos/go-systemd/v22/dbus` and `github.com/godbus/dbus/v5`.

### Step 1.4: Unit names and cgroup readers

**Files:** `sched/units.go`, `sched/units_test.go`, `sched/cgroup.go`, `sched/cgroup_test.go`, `sched/meminfo_linux.go`, `sched/meminfo_other.go`

- [ ] **Write the failing tests.** `sched/units_test.go`:

```go
package sched

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUnitNames(t *testing.T) {
	a, b := "/home/u/.loom/jobs", "/tmp/sandbox/jobs"
	assert.Regexp(t, `^loom-[0-9a-f]{8}\.slice$`, SliceName(a))
	assert.NotEqual(t, SliceName(a), SliceName(b), "two looms never share a slice")
	assert.Equal(t, SliceName(a), SliceName(a+"/"), "the path is cleaned")
	assert.Regexp(t, `^loom-[0-9a-f]{8}-job-7-1\.scope$`, ScopeName(a, 7, 0))
	assert.Regexp(t, `-job-7-3\.scope$`, ScopeName(a, 7, 2), "the attempt counts from 1")
	assert.Equal(t, strings.TrimSuffix(SliceName(a), ".slice"), strings.Split(ScopeName(a, 7, 0), "-job-")[0])
}

func TestCPUWeight(t *testing.T) {
	assert.Equal(t, uint64(1), CPUWeight(0))
	assert.Equal(t, uint64(400), CPUWeight(4))
	assert.Equal(t, uint64(10000), CPUWeight(200))
}
```

`sched/cgroup_test.go`:

```go
package sched

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeCgroup writes files into root/path, making the directories.
func writeCgroup(t *testing.T, root, path string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(root, path)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	for name, body := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
	}
}

func fullCgroup() map[string]string {
	return map[string]string{
		"memory.current":  "1073741824\n",
		"memory.stat":     "anon 1\nfile 2\ninactive_file 268435456\nactive_file 5\n",
		"memory.high":     "max\n",
		"cpu.stat":        "usage_usec 2500000\nuser_usec 1\nsystem_usec 1\n",
		"memory.pressure": "some avg10=12.50 avg60=1.00 avg300=0.00 total=1\nfull avg10=3.00 avg60=0.00 avg300=0.00 total=1\n",
		"cpu.pressure":    "some avg10=34.00 avg60=0.00 avg300=0.00 total=9\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n",
		"memory.events":   "low 0\nhigh 3\nmax 0\noom 1\noom_kill 1\n",
		"cgroup.events":   "populated 1\nfrozen 0\n",
	}
}

func TestReadCgroup(t *testing.T) {
	root := t.TempDir()
	path := "/user.slice/loom-ab-job-1-1.scope"
	writeCgroup(t, root, path, fullCgroup())
	s, err := ReadCgroup(root, path)
	require.NoError(t, err)
	assert.Equal(t, CgroupSample{
		WorkingKB: (1073741824 - 268435456) / 1024, CPU: 2500 * time.Millisecond,
		MemPressure: 12.5, CPUPressure: 34, OOMKills: 1, Populated: true,
	}, s)

	files := fullCgroup()
	files["memory.high"] = "536870912\n"
	files["cgroup.events"] = "populated 1\nfrozen 1\n"
	writeCgroup(t, root, path, files)
	s, err = ReadCgroup(root, path)
	require.NoError(t, err)
	assert.Equal(t, int64(512*1024), s.HighKB)
	assert.True(t, s.Frozen)
}

func TestReadCgroup_OptionalAndRequiredFiles(t *testing.T) {
	root := t.TempDir()
	files := fullCgroup()
	delete(files, "memory.pressure")
	delete(files, "cpu.pressure")
	delete(files, "memory.events")
	delete(files, "memory.high")
	writeCgroup(t, root, "/a.scope", files)
	s, err := ReadCgroup(root, "/a.scope")
	require.NoError(t, err, "pressure, events and memory.high are optional")
	assert.Zero(t, s.MemPressure)
	assert.Zero(t, s.HighKB)

	for _, need := range []string{"memory.current", "memory.stat", "cpu.stat", "cgroup.events"} {
		files := fullCgroup()
		delete(files, need)
		dir := "/" + need + ".scope"
		writeCgroup(t, root, dir, files)
		_, err := ReadCgroup(root, dir)
		assert.Error(t, err, need)
	}
}

func TestReadCgroup_RefusesAPathItCouldNotMean(t *testing.T) {
	for _, p := range []string{"", "/", "relative/x.scope", "/a/../b.scope", "/a//b.scope"} {
		_, err := ReadCgroup(t.TempDir(), p)
		assert.Error(t, err, p)
	}
}

func TestCgroupKill_RefusesWhatIsNotAJobScope(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"/user.slice", "/loom.slice/loom-ab.slice", "/x/other-job-1-1.scope", "/x/loom-ab-1.scope"} {
		writeCgroup(t, root, p, map[string]string{"cgroup.kill": ""})
		assert.Error(t, CgroupKill(root, p), p)
		data, _ := os.ReadFile(filepath.Join(root, p, "cgroup.kill"))
		assert.Empty(t, data, "nothing written for %s", p)
	}
	job := "/loom.slice/loom-ab.slice/loom-ab-job-4-2.scope"
	writeCgroup(t, root, job, map[string]string{"cgroup.kill": ""})
	require.NoError(t, CgroupKill(root, job))
	data, err := os.ReadFile(filepath.Join(root, job, "cgroup.kill"))
	require.NoError(t, err)
	assert.Equal(t, "1", string(data))
}

func TestParseSelfCgroup(t *testing.T) {
	p, err := parseSelfCgroup([]byte("0::/user.slice/loom-ab-job-1-1.scope\n"))
	require.NoError(t, err)
	assert.Equal(t, "/user.slice/loom-ab-job-1-1.scope", p)
	_, err = parseSelfCgroup([]byte("12:cpu:/x\n"))
	assert.Error(t, err, "cgroup v1 only")
}

func TestParseMemAvailable(t *testing.T) {
	kb, err := parseMemAvailable([]byte("MemTotal:       31696820 kB\nMemFree: 1 kB\nMemAvailable:   17896096 kB\n"))
	require.NoError(t, err)
	assert.Equal(t, int64(17896096), kb)
	_, err = parseMemAvailable([]byte("MemTotal: 1 kB\n"))
	assert.Error(t, err)
}
```

- [ ] **Run them:** `loom job run -d "cgroups: readers red" -- env CGO_ENABLED=0 go test ./sched/ -run 'TestUnitNames|TestCPUWeight|TestReadCgroup|TestCgroupKill|TestParseSelfCgroup|TestParseMemAvailable'` → FAIL (undefined).
- [ ] **Implement** `sched/units.go`:

```go
package sched

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
)

// unitTag is the 8 hex digits that keep two looms' units apart under one
// user manager (cgroup spec §15 amendment 1): the SHA-256 of the jobs
// directory, cleaned.
func unitTag(jobsDir string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(jobsDir)))
	return hex.EncodeToString(sum[:4])
}

// SliceName is the slice the jobs of the loom whose jobs directory is
// jobsDir run in: loom-<tag>.slice, which systemd places under loom.slice.
func SliceName(jobsDir string) string { return "loom-" + unitTag(jobsDir) + ".slice" }

// ScopeName is job id's scope for its attempt (0 for its first run):
// loom-<tag>-job-<id>-<attempt+1>.scope.
func ScopeName(jobsDir string, id JobID, attempt int) string {
	return fmt.Sprintf("loom-%s-job-%d-%d.scope", unitTag(jobsDir), id, attempt+1)
}

// CPUWeight is a job's scope weight: 100 per claimed core, 1–10000.
func CPUWeight(cores int) uint64 { return uint64(min(10000, max(1, 100*cores))) }
```

`sched/cgroup.go`:

```go
package sched

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// CgroupRoot is where the cgroup v2 hierarchy is mounted.
const CgroupRoot = "/sys/fs/cgroup"

// CgroupSample is one reading of a cgroup (cgroup spec §5.3).
type CgroupSample struct {
	WorkingKB   int64         // memory.current less memory.stat's inactive_file
	HighKB      int64         // memory.high; 0 for none ("max", or no file)
	CPU         time.Duration // cpu.stat's usage_usec: every process it ever held
	MemPressure float64       // memory.pressure's "some" avg10, a percentage
	CPUPressure float64       // cpu.pressure's "some" avg10
	OOMKills    int64         // memory.events' oom_kill, its subtree's included
	Frozen      bool          // cgroup.events' frozen
	Populated   bool          // cgroup.events' populated
}

// checkCgroupPath refuses a path that is not clean, absolute and below the
// root, as /proc/<pid>/cgroup names one.
func checkCgroupPath(path string) error {
	if path == "/" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("cgroup path %q is not a clean absolute path below the root", path)
	}
	return nil
}

// ReadCgroup reads cgroup path under root. memory.current, memory.stat,
// cpu.stat and cgroup.events must be there; memory.high, the pressure files
// and memory.events may be missing, and then read as 0.
func ReadCgroup(root, path string) (CgroupSample, error) {
	if err := checkCgroupPath(path); err != nil {
		return CgroupSample{}, err
	}
	dir := filepath.Join(root, path)
	read := func(name string) ([]byte, error) { return os.ReadFile(filepath.Join(dir, name)) }
	var s CgroupSample
	cur, err := read("memory.current")
	if err != nil {
		return CgroupSample{}, fmt.Errorf("read memory.current: %w", err)
	}
	current, err := strconv.ParseInt(strings.TrimSpace(string(cur)), 10, 64)
	if err != nil {
		return CgroupSample{}, fmt.Errorf("parse memory.current: %w", err)
	}
	stat, err := read("memory.stat")
	if err != nil {
		return CgroupSample{}, fmt.Errorf("read memory.stat: %w", err)
	}
	inactive, _ := keyedValue(stat, "inactive_file")
	s.WorkingKB = max(0, current-inactive) / 1024
	if data, err := read("memory.high"); err == nil {
		if v := strings.TrimSpace(string(data)); v != "max" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				s.HighKB = n / 1024
			}
		}
	}
	cpu, err := read("cpu.stat")
	if err != nil {
		return CgroupSample{}, fmt.Errorf("read cpu.stat: %w", err)
	}
	usec, ok := keyedValue(cpu, "usage_usec")
	if !ok {
		return CgroupSample{}, errors.New("cpu.stat has no usage_usec")
	}
	s.CPU = time.Duration(usec) * time.Microsecond
	if data, err := read("memory.pressure"); err == nil {
		s.MemPressure = someAvg10(data)
	}
	if data, err := read("cpu.pressure"); err == nil {
		s.CPUPressure = someAvg10(data)
	}
	if data, err := read("memory.events"); err == nil {
		s.OOMKills, _ = keyedValue(data, "oom_kill")
	}
	events, err := read("cgroup.events")
	if err != nil {
		return CgroupSample{}, fmt.Errorf("read cgroup.events: %w", err)
	}
	populated, _ := keyedValue(events, "populated")
	frozen, _ := keyedValue(events, "frozen")
	s.Populated, s.Frozen = populated == 1, frozen == 1
	return s, nil
}

// keyedValue is the integer after key in a flat-keyed file of "key value"
// lines, and false when the key is absent or its value is not one.
func keyedValue(data []byte, key string) (int64, bool) {
	for line := range bytes.SplitSeq(data, []byte{'\n'}) {
		k, v, ok := bytes.Cut(line, []byte{' '})
		if ok && string(k) == key {
			n, err := strconv.ParseInt(strings.TrimSpace(string(v)), 10, 64)
			return n, err == nil
		}
	}
	return 0, false
}

// someAvg10 is the avg10 of a pressure file's "some" line, 0 for none.
func someAvg10(data []byte) float64 {
	for line := range bytes.SplitSeq(data, []byte{'\n'}) {
		fields := strings.Fields(string(line))
		if len(fields) < 2 || fields[0] != "some" {
			continue
		}
		for _, f := range fields[1:] {
			if v, ok := strings.CutPrefix(f, "avg10="); ok {
				if x, err := strconv.ParseFloat(v, 64); err == nil {
					return x
				}
			}
		}
	}
	return 0
}

// CgroupKill writes 1 to path's cgroup.kill under root: SIGKILL to every
// process in it, whatever group or session it moved to. It refuses anything
// but a loom job's scope (a base name loom-…-job-….scope), since a kill
// reaches every process below the cgroup named.
func CgroupKill(root, path string) error {
	if err := checkCgroupPath(path); err != nil {
		return err
	}
	base := filepath.Base(path)
	if !strings.HasPrefix(base, "loom-") || !strings.Contains(base, "-job-") || !strings.HasSuffix(base, ".scope") {
		return fmt.Errorf("refusing to kill cgroup %s: not a loom job's scope", path)
	}
	f, err := os.OpenFile(filepath.Join(root, path, "cgroup.kill"), os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("open cgroup.kill: %w", err)
	}
	_, werr := f.Write([]byte("1"))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return fmt.Errorf("write cgroup.kill: %w", werr)
	}
	return nil
}

// SelfCgroup is this process's cgroup v2 path.
func SelfCgroup() (string, error) {
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return "", fmt.Errorf("read /proc/self/cgroup: %w", err)
	}
	return parseSelfCgroup(data)
}

// parseSelfCgroup is the path of /proc/<pid>/cgroup's "0::" line.
func parseSelfCgroup(data []byte) (string, error) {
	for line := range strings.SplitSeq(string(data), "\n") {
		if p, ok := strings.CutPrefix(line, "0::"); ok && checkCgroupPath(p) == nil {
			return p, nil
		}
	}
	return "", errors.New("no cgroup v2 entry in /proc/self/cgroup")
}

// parseMemAvailable is /proc/meminfo's MemAvailable, in KB.
func parseMemAvailable(data []byte) (int64, error) {
	for line := range strings.SplitSeq(string(data), "\n") {
		if v, ok := strings.CutPrefix(line, "MemAvailable:"); ok {
			fields := strings.Fields(v)
			if len(fields) >= 1 {
				return strconv.ParseInt(fields[0], 10, 64)
			}
		}
	}
	return 0, errors.New("no MemAvailable in /proc/meminfo")
}
```

`sched/meminfo_linux.go`:

```go
package sched

import (
	"fmt"
	"os"
)

// ReadMemAvailableKB is the host's MemAvailable, in KB.
func ReadMemAvailableKB() (int64, error) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, fmt.Errorf("read /proc/meminfo: %w", err)
	}
	return parseMemAvailable(data)
}
```

`sched/meminfo_other.go` (`//go:build !linux`): `func ReadMemAvailableKB() (int64, error) { return 0, ErrUnsupported }`.

- [ ] **Run them again** → PASS.

### Step 1.5: The confiner

**Files:** `sched/confine.go`, `sched/confine_linux.go`, `sched/confine_other.go`

- [ ] **Implement** `sched/confine.go`:

```go
package sched

import "context"

// ScopeSpec is the scope a job's shim creates for itself (spec.json's
// scope; cgroup spec §5.1).
type ScopeSpec struct {
	Unit      string `json:"unit"`
	Slice     string `json:"slice"`
	CPUWeight uint64 `json:"cpu_weight"`
}

// SliceProps are what the daemon sets on its jobs' slice (§5.2).
type SliceProps struct {
	CPUWeight      uint64
	MemoryMaxBytes uint64 // 0: no limit
}

// Confiner is the user's service manager as the queue drives it (cgroup
// spec §5). Every call waits on D-Bus: the model makes them from a spawned
// job, never on its loop.
type Confiner interface {
	// JoinScope creates s as a transient scope holding pid, and returns
	// once systemd has started it.
	JoinScope(ctx context.Context, s ScopeSpec, pid int) error
	// EnsureSlice sets name's properties, creating it when nothing has.
	EnsureSlice(ctx context.Context, name string, p SliceProps) error
	// SetMemoryHigh sets unit's MemoryHigh; 0 is no limit.
	SetMemoryHigh(ctx context.Context, unit string, bytes uint64) error
	Freeze(ctx context.Context, unit string) error
	Thaw(ctx context.Context, unit string) error
	Close()
}
```

`sched/confine_linux.go`:

```go
package sched

import (
	"context"
	"fmt"
	"math"

	sddbus "github.com/coreos/go-systemd/v22/dbus"
	godbus "github.com/godbus/dbus/v5"
)

// systemdConfiner is the Confiner of the user's systemd, on the session bus.
type systemdConfiner struct{ conn *sddbus.Conn }

// NewConfiner connects to the user's service manager.
func NewConfiner(ctx context.Context) (Confiner, error) {
	conn, err := sddbus.NewUserConnectionContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("connect to the user's systemd: %w", err)
	}
	return &systemdConfiner{conn: conn}, nil
}

func prop(name string, v any) sddbus.Property {
	return sddbus.Property{Name: name, Value: godbus.MakeVariant(v)}
}

// limit is bytes as a systemd memory limit: 0 is infinity.
func limit(bytes uint64) uint64 {
	if bytes == 0 {
		return math.MaxUint64
	}
	return bytes
}

// start creates unit as a transient unit with props and waits for its start
// job.
func (c *systemdConfiner) start(ctx context.Context, unit string, props []sddbus.Property) error {
	done := make(chan string, 1)
	if _, err := c.conn.StartTransientUnitContext(ctx, unit, "fail", props, done); err != nil {
		return fmt.Errorf("create %s: %w", unit, err)
	}
	select {
	case result := <-done:
		if result != "done" {
			return fmt.Errorf("create %s: its start job ended %q", unit, result)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("create %s: %w", unit, ctx.Err())
	}
}

func (c *systemdConfiner) JoinScope(ctx context.Context, s ScopeSpec, pid int) error {
	return c.start(ctx, s.Unit, []sddbus.Property{
		sddbus.PropDescription("loom job"),
		sddbus.PropSlice(s.Slice),
		sddbus.PropPids(uint32(pid)),
		prop("CPUWeight", s.CPUWeight),
		prop("CPUAccounting", true),
		prop("MemoryAccounting", true),
		prop("OOMPolicy", "kill"),
		prop("CollectMode", "inactive-or-failed"),
	})
}

func (c *systemdConfiner) EnsureSlice(ctx context.Context, name string, p SliceProps) error {
	props := []sddbus.Property{prop("CPUWeight", p.CPUWeight), prop("MemoryMax", limit(p.MemoryMaxBytes))}
	setErr := c.conn.SetUnitPropertiesContext(ctx, name, true, props...)
	if setErr == nil {
		return nil
	}
	if err := c.start(ctx, name, append(props, sddbus.PropDescription("loom jobs"))); err != nil {
		return fmt.Errorf("set %s: %v; %w", name, setErr, err)
	}
	return nil
}

func (c *systemdConfiner) SetMemoryHigh(ctx context.Context, unit string, bytes uint64) error {
	if err := c.conn.SetUnitPropertiesContext(ctx, unit, true, prop("MemoryHigh", limit(bytes))); err != nil {
		return fmt.Errorf("set %s's MemoryHigh: %w", unit, err)
	}
	return nil
}

func (c *systemdConfiner) Freeze(ctx context.Context, unit string) error {
	if err := c.conn.FreezeUnit(ctx, unit); err != nil {
		return fmt.Errorf("freeze %s: %w", unit, err)
	}
	return nil
}

func (c *systemdConfiner) Thaw(ctx context.Context, unit string) error {
	if err := c.conn.ThawUnit(ctx, unit); err != nil {
		return fmt.Errorf("thaw %s: %w", unit, err)
	}
	return nil
}

func (c *systemdConfiner) Close() { c.conn.Close() }
```

`sched/confine_other.go` (`//go:build !linux`):

```go
package sched

import "context"

// NewConfiner answers ErrUnsupported: cgroups are Linux only.
func NewConfiner(context.Context) (Confiner, error) { return nil, ErrUnsupported }
```

- [ ] `go mod tidy && go mod vendor` again through the queue, then `CGO_ENABLED=0 go build ./...`, and the Windows and darwin builds.

### Step 1.6: The shim joins its scope

**Files:** `sched/files.go`, `sched/shim_unix.go`, `sched/shim_test.go`

- [ ] **Write the failing test** in `sched/shim_test.go`, with the file's own helpers (`shimSpec`, `writeShimJob`, `startShim`, `awaitShimExit`). The test binary's session bus is dead (Step 1.2), so the join fails:

```go
// TestRunShim_RunsUncontainedWhenItCannotJoinItsScope: a scope the shim
// cannot create leaves the job running, with the reason in started.json.
func TestRunShim_RunsUncontainedWhenItCannotJoinItsScope(t *testing.T) {
	spec := shimSpec(t, []string{"true"})
	spec.Scope = &ScopeSpec{Unit: "loom-00000000-job-1-1.scope", Slice: "loom-00000000.slice", CPUWeight: 100}
	dir := writeShimJob(t, spec)
	startShim(t, dir)
	exit := awaitShimExit(t, dir)
	assert.Equal(t, 0, exit.Code, "the job ran")
	st, err := ReadStarted(dir)
	require.NoError(t, err)
	assert.Empty(t, st.Unit)
	assert.Empty(t, st.Cgroup)
	assert.NotEmpty(t, st.Uncontained, "why it runs uncontained")
}
```

- [ ] **Run it** → FAIL (`Scope` undefined).
- [ ] **Implement.** In `sched/files.go`: `Spec` gains `Scope *ScopeSpec \`json:"scope,omitempty"\`` ("the scope to create, written when the job starts contained; nil runs it uncontained"); `Started` gains

```go
	Unit        string `json:"unit,omitempty"`        // the scope it joined
	Cgroup      string `json:"cgroup,omitempty"`      // the cgroup it landed in
	Uncontained string `json:"uncontained,omitempty"` // why it asked for a scope and runs without one
```

In `sched/shim_unix.go`, replace the `writeStarted` call with:

```go
	st := Started{PID: os.Getpid(), Started: time.Now()}
	if spec.Scope != nil {
		st.Unit, st.Cgroup, st.Uncontained = joinScope(*spec.Scope)
	}
	if err := writeStarted(jobDir, st); err != nil {
		return shimGiveUp(jobDir, fmt.Errorf("writing started.json: %w", err))
	}
```

and add:

```go
// scopeTimeout bounds the shim's scope creation: the daemon waits 5 s for
// started.json.
const scopeTimeout = 2 * time.Second

// joinScope moves the shim into its scope before it starts the command, so
// everything the command forks is inside (cgroup spec §5.1). It returns the
// unit and the cgroup it landed in, or why it runs uncontained; the job runs
// either way.
func joinScope(s ScopeSpec) (unit, cgroup, why string) {
	ctx, cancel := context.WithTimeout(context.Background(), scopeTimeout)
	defer cancel()
	c, err := NewConfiner(ctx)
	if err != nil {
		return "", "", err.Error()
	}
	defer c.Close()
	if err := c.JoinScope(ctx, s, os.Getpid()); err != nil {
		return "", "", err.Error()
	}
	path, err := SelfCgroup()
	switch {
	case err != nil:
		return s.Unit, "", "its cgroup could not be read: " + err.Error()
	case filepath.Base(path) != s.Unit:
		return s.Unit, "", fmt.Sprintf("it is in %s, not its scope", path)
	}
	return s.Unit, path, ""
}
```

- [ ] **Run it again** → PASS, with the whole `sched` package (`go test ./sched/`).

### Step 1.7: Policy, config and record fields

**Files:** `config/settings.go`, `sched/job.go`, `sched/policy.go`, `sched/policy_test.go`

- [ ] **Write the failing tests** in `sched/policy_test.go`: `TestResolve_Defaults` expects `Contain: true, JobsCPUWeight: 50`; `TestResolve_Bounds` gains `{JobsCPUWeight: 10001}` → `scheduler.jobs_cpu_weight is 10001: it must be 1–10000` and `{JobsCPUWeight: -1}` → `… is -1 …`; a new case sets `Contain: ptr(false)` and expects `Contain: false`.
- [ ] **Run** → FAIL.
- [ ] **Implement.** `config.SchedulerSettings` gains `Contain *bool \`json:"contain,omitempty"\`` ("per-job scopes, greedy admission and the ladder; absent means true") and `JobsCPUWeight int \`json:"jobs_cpu_weight,omitempty"\``; `clone` copies `Contain` with `clonePtr`. `sched.Policy` gains `Contain bool \`json:"contain"\`` and `JobsCPUWeight int \`json:"jobs_cpu_weight"\``. `Resolve`, after the default claim: `weight, err := resolveBounded("jobs_cpu_weight", s.JobsCPUWeight, 50, 1, 10000)`, and sets `Contain: s.Contain == nil || *s.Contain`. Its doc comment names both defaults and adds `jobs_cpu_weight` last in the fault order.
- [ ] `sched.Record` gains, after `HoldBroke`:

```go
	// Confinement (cgroup spec §5): the scope it runs in and its cgroup, or
	// why it runs uncontained; Attempt counts its earlier runs (§8.4).
	Unit        string `json:"unit,omitempty"`
	Cgroup      string `json:"cgroup,omitempty"`
	Uncontained string `json:"uncontained,omitempty"`
	Attempt     int    `json:"attempt,omitempty"`
```

- [ ] Check `core.setSchedulerCaps` keeps `Contain` and `JobsCPUWeight` on a caps-form edit (it must copy every key the form doesn't show, as it does `ReleasePastPeaks`); add both to the test that pins `ReleasePastPeaks` there.
- [ ] **Run** `go test ./sched/ ./config/ ./core/ -run 'TestResolve|TestSetSchedulerCaps'` → PASS.

### Step 1.8: The model contains, measures and kills through cgroups

**Files:** `core/queue.go`, `core/queue_contain.go` (new), `core/queue_admit.go`, `core/queue_watch.go`, `core/queue_view.go`, `core/seams.go`, `core/model.go`, `core/queue_contain_test.go` (new)

- [ ] **Queue state** (`core/queue.go`), after `advisor`:

```go
	// Confinement (cgroup spec §5): the user's service manager once a probe
	// reached it, or why it couldn't; whether a probe is in flight and when
	// the last one ran. cgroupRoot is sched.CgroupRoot but in tests. availKB
	// is the host's MemAvailable and slice the jobs slice's reading, from
	// the last pass.
	confiner   sched.Confiner
	confineErr error
	probing    bool
	lastProbe  time.Time
	cgroupRoot string
	availKB    int64
	slice      *sched.CgroupSample
```

`newQueueState` sets `cgroupRoot: sched.CgroupRoot`.

- [ ] **`core/queue_contain.go`:**

```go
package core

import (
	"context"
	"time"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/sched"
)

const (
	probeEvery     = 60 * time.Second // a probe's retry while in fallback mode
	confineTimeout = 5 * time.Second  // one D-Bus call's bound
)

// newConfiner reaches the user's service manager; tests replace the
// confiner through SetConfinerForTest instead.
var newConfiner = sched.NewConfiner

// containWhy is why jobs started now run uncontained (fallback mode, cgroup
// spec §5.5), "" when they are contained.
func (q *queueState) containWhy(p sched.Policy) string {
	switch {
	case queueGOOS != "linux":
		return "cgroups are Linux only"
	case !p.Contain:
		return "scheduler.contain is off"
	case q.confiner != nil:
		return ""
	case q.confineErr != nil:
		return q.confineErr.Error()
	}
	return "not connected to the user's systemd yet"
}

// confinerProbed is a probe's result.
type confinerProbed struct {
	c   sched.Confiner
	err error
}

// maybeProbeConfiner reaches for the user's service manager from a job: at
// boot, and every probeEvery while jobs run uncontained with contain on.
func (m *Model) maybeProbeConfiner(p sched.Policy) {
	q := m.queue
	if queueGOOS != "linux" || !p.Contain || q.confiner != nil || q.probing ||
		(!q.lastProbe.IsZero() && time.Since(q.lastProbe) < probeEvery) {
		return
	}
	q.probing, q.lastProbe = true, time.Now()
	m.spawnBackground(func() any {
		ctx, cancel := context.WithTimeout(context.Background(), confineTimeout)
		defer cancel()
		c, err := newConfiner(ctx)
		return confinerProbed{c: c, err: err}
	})
}

// deliverConfinerProbed keeps a probe's confiner, or why there is none.
func (m *Model) deliverConfinerProbed(r confinerProbed) {
	q := m.queue
	q.probing = false
	if r.err != nil {
		m.confinerFailed(r.err)
		return
	}
	q.confiner, q.confineErr = r.c, nil
	log.For("core").Info("queue.contained")
	q.moved()
}

// confinerFailed drops the confiner after a D-Bus call failed: jobs start
// uncontained until the next probe reaches systemd again.
func (m *Model) confinerFailed(err error) {
	q := m.queue
	if q.confineErr == nil || q.confineErr.Error() != err.Error() {
		log.For("core").Warn("queue.uncontained", "err", err)
	}
	if q.confiner != nil {
		q.confiner.Close()
	}
	q.confiner, q.confineErr = nil, err
	q.moved()
}

```

Add `case confinerProbed: m.deliverConfinerProbed(r)` beside `case jobStarted:` in `core/model.go`'s switch. Call `m.maybeProbeConfiner(q.policy)` at the end of `loadQueue` and in `tickQueue` (both branches).

- [ ] **The start** (`core/queue_admit.go`): `jobStart` gains `scope *sched.ScopeSpec`, `confiner sched.Confiner` and `slice sched.SliceProps`; `jobStarted` gains `unit, cgroup, uncontained string` and `confineErr error`. In `startJob`, before the spawn:

```go
	hint := rec.Claim.Need(policy.Caps).CPUs
	var scope *sched.ScopeSpec
	rec.Unit, rec.Cgroup, rec.Uncontained = "", "", ""
	if why := q.containWhy(policy); why != "" {
		rec.Uncontained = why
	} else {
		scope = &sched.ScopeSpec{Unit: sched.ScopeName(q.dir, rec.ID, rec.Attempt), Slice: sched.SliceName(q.dir), CPUWeight: sched.CPUWeight(hint)}
	}
```

passing `hint`, `scope`, `confiner: q.confiner` and `slice: sched.SliceProps{CPUWeight: uint64(policy.JobsCPUWeight), MemoryMaxBytes: uint64(policy.Caps.MemoryGB) << 30}`. In `runJobStart`, before `spec.HintCPUs = s.hint`:

```go
		if s.scope != nil {
			ctx, cancel := context.WithTimeout(context.Background(), confineTimeout)
			err := s.confiner.EnsureSlice(ctx, s.scope.Slice, s.slice)
			cancel()
			if err != nil {
				res.confineErr = err
				res.uncontained = "its slice could not be set: " + err.Error()
				s.scope = nil
			}
		}
		spec.Scope = s.scope
```

and when `started.json` is read: `res.unit, res.cgroup = st.Unit, st.Cgroup; if st.Uncontained != "" { res.uncontained = st.Uncontained }`. In `deliverJobStarted`, before the `r.err` check: `if r.confineErr != nil { m.confinerFailed(r.confineErr) }`; on success: `rec.Unit, rec.Cgroup = r.unit, r.cgroup; if r.uncontained != "" { rec.Uncontained = r.uncontained }`. In `noteAlive` (an adopted or late record), copy `Unit`, `Cgroup` and `Uncontained` from `ob.started` when the record has no PID yet.

- [ ] **The pass** (`core/queue_watch.go`): `passJob` gains `cgroup string`; `queuePass` gains `cgroupRoot string`; `jobObs` gains `cg *sched.CgroupSample` and `cgErr error`; `queuePassResult` gains `availKB int64` and `slice *sched.CgroupSample`. `queuePassFor` fills `cgroup: rec.Cgroup` and `cgroupRoot: q.cgroupRoot`. In `runQueuePass`, for a job whose lock is held and that has a cgroup:

```go
			if j.cgroup != "" {
				if cg, err := sched.ReadCgroup(p.cgroupRoot, j.cgroup); err == nil {
					ob.cg = &cg
				} else {
					ob.cgErr = err
				}
			}
```

and after the loop:

```go
	if kb, err := readMemAvailable(); err == nil {
		res.availKB = kb
	}
	for _, j := range p.jobs {
		if j.cgroup == "" {
			continue
		}
		if s, err := sched.ReadCgroup(p.cgroupRoot, filepath.Dir(j.cgroup)); err == nil {
			res.slice = &s
		}
		break
	}
```

with `var readMemAvailable = sched.ReadMemAvailableKB` beside `sample`. In `tendCancel`, after a SIGKILL that went: `if ob.killed && j.cgroup != "" { if err := sched.CgroupKill(p.cgroupRoot, j.cgroup); err != nil { ob.signalErr = errors.Join(ob.signalErr, err) } }`. In `deliverQueuePass`, before `m.applyUsage(r)`: `q.availKB, q.slice = r.availKB, r.slice` (a zero `availKB` keeps the last value).
- [ ] **Usage** (`applyUsage`): drop the early `if r.snap == nil { return }`; per job:

```go
		var rss int64
		var cpu time.Duration
		switch {
		case ob.cg != nil:
			rss, cpu = ob.cg.WorkingKB, ob.cg.CPU
		case r.snap != nil && rec.PID > 1:
			rss, cpu, _ = r.snap.Session(rec.PID)
		default:
			continue
		}
```

(keep the `rec.PID <= 1` skip only in the session branch). A `cgErr` is logged once per job (reuse `lockLogged`'s pattern with a `cgroupLogged map[sched.JobID]string`, dropped in `settledJob`).
- [ ] **Views** (`core/queue_view.go`): `QueueView` gains `Uncontained string // why jobs start uncontained while contain is on (Linux); "" when contained or not expected`; `JobView` gains `Unit`, `Uncontained string` and `Attempt int`, copied from the record. `queueView` sets `Uncontained = q.containWhy(q.policy)` unless `queueGOOS != "linux" || !q.policy.Contain`.
- [ ] **Seams** (`core/seams.go`): `SetConfinerForTest(c sched.Confiner)` sets `q.confiner` and `q.lastProbe = time.Now()` (no probe replaces it); `SetCgroupRootForTest(root string)`. A fake confiner for the tests lives in `core/queue_contain_test.go`:

```go
// fakeConfiner records calls and fails those named in fail.
type fakeConfiner struct {
	mu    sync.Mutex
	calls []string
	fail  map[string]error // by method name
}

func (f *fakeConfiner) note(call string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
	return f.fail[strings.Fields(call)[0]]
}
func (f *fakeConfiner) JoinScope(_ context.Context, s sched.ScopeSpec, pid int) error {
	return f.note("JoinScope " + s.Unit)
}
func (f *fakeConfiner) EnsureSlice(_ context.Context, name string, p sched.SliceProps) error {
	return f.note(fmt.Sprintf("EnsureSlice %s %d %d", name, p.CPUWeight, p.MemoryMaxBytes))
}
func (f *fakeConfiner) SetMemoryHigh(_ context.Context, unit string, b uint64) error {
	return f.note(fmt.Sprintf("SetMemoryHigh %s %d", unit, b))
}
func (f *fakeConfiner) Freeze(_ context.Context, unit string) error { return f.note("Freeze " + unit) }
func (f *fakeConfiner) Thaw(_ context.Context, unit string) error   { return f.note("Thaw " + unit) }
func (f *fakeConfiner) Close()                                      {}
func (f *fakeConfiner) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}
```

- [ ] **Core tests** (`core/queue_contain_test.go`), built through the production path (`bootModel`, real shims through `sched.TestRunner`, as `core/queue_holds_test.go` does):
  1. `TestQueueContain_FallbackSaysWhy`: with no confiner, a started job's record says `uncontained`, its spec has no scope, and on Linux the view's `Uncontained` is set; with `contain: false` in the block, the view's `Uncontained` is `scheduler.contain is off`.
  2. `TestQueueContain_AStartEnsuresTheSliceAndWritesTheScope`: with a fake confiner, a submitted job's `spec.json` names `ScopeName(dir, id, 0)` in `SliceName(dir)` with weight `CPUWeight(cores)`, the fake saw `EnsureSlice <slice> 50 <memory cap bytes>`; the test shim can't join (dead bus), so the record ends `uncontained` with the shim's reason.
  3. `TestQueueContain_ASliceThatFailsStartsTheJobUncontained`: the fake fails `EnsureSlice`; the job runs, says why, and the queue drops the confiner (`containWhy` names the error).
  4. `TestQueueContain_UsageComesFromTheCgroup`: a running job (real shim) whose record is given `Cgroup: "/t/loom-ab-job-1-1.scope"` under a temp root (`SetCgroupRootForTest`) with written files; after a pass (`m.Deliver(queuePassResult…)` built by `runQueuePass(m.queuePassFor(false))`), its usage is the cgroup's working set, and `availKB` is set.
  5. `TestQueueContain_TheSIGKILLAlsoKillsTheCgroup`: a cancelled contained job past its escalation has its temp `cgroup.kill` written with `1`.
- [ ] **Run** `go test ./core/ -run TestQueueContain` → PASS, then `go test ./sched/ ./core/`.

### Step 1.9: The work view says why

**Files:** `ui/work.go`, its test

- [ ] The header gains ` · uncontained: <why>` (dim, truncated to the width like the other parts) while `QueueView.Uncontained` is set; a job's detail line gains `uncontained: <why>` when `JobView.Uncontained` is set and the host-wide reason is not the same text. Add a case to the work view's header test.

### Step 1.10: The live contract test

**Files:** `sched/confine_live_test.go`

- [ ] **Write** (`//go:build systemd_live && linux`):

```go
package sched

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/internal/testenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readKnob reads one file of a cgroup, trimmed.
func readKnob(t *testing.T, path, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(CgroupRoot, path, name))
	require.NoError(t, err)
	return strings.TrimSpace(string(data))
}

// TestConfiner_Live runs the confiner against the real user manager, on a
// test slice and scope of its own (cgroup spec §13.3).
func TestConfiner_Live(t *testing.T) {
	addr := os.Getenv(testenv.EnvRealDBus)
	if addr == "" {
		t.Skip("no user bus")
	}
	t.Setenv(testenv.EnvDBus, addr)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := NewConfiner(ctx)
	require.NoError(t, err)
	defer c.Close()

	slice := fmt.Sprintf("loom-test%d.slice", os.Getpid())
	unit := fmt.Sprintf("loom-test%d-job-1-1.scope", os.Getpid())
	require.NoError(t, c.EnsureSlice(ctx, slice, SliceProps{CPUWeight: 30, MemoryMaxBytes: 1 << 30}))

	cmd := exec.Command("sleep", "60")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	require.NoError(t, c.JoinScope(ctx, ScopeSpec{Unit: unit, Slice: slice, CPUWeight: 200}, cmd.Process.Pid))

	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", cmd.Process.Pid))
	require.NoError(t, err)
	path, err := parseSelfCgroup(data)
	require.NoError(t, err)
	require.Equal(t, unit, filepath.Base(path))
	require.Equal(t, slice, filepath.Base(filepath.Dir(path)))
	assert.Equal(t, "200", readKnob(t, path, "cpu.weight"))
	assert.Equal(t, "1", readKnob(t, path, "memory.oom.group"), "OOMPolicy=kill")
	assert.Equal(t, "30", readKnob(t, filepath.Dir(path), "cpu.weight"))
	assert.Equal(t, "1073741824", readKnob(t, filepath.Dir(path), "memory.max"))

	s, err := ReadCgroup(CgroupRoot, path)
	require.NoError(t, err)
	assert.True(t, s.Populated)

	require.NoError(t, c.SetMemoryHigh(ctx, unit, 512<<20))
	assert.Equal(t, "536870912", readKnob(t, path, "memory.high"))
	require.NoError(t, c.SetMemoryHigh(ctx, unit, 0))
	assert.Equal(t, "max", readKnob(t, path, "memory.high"))

	require.NoError(t, c.Freeze(ctx, unit))
	require.Eventually(t, func() bool { s, _ := ReadCgroup(CgroupRoot, path); return s.Frozen }, 5*time.Second, 50*time.Millisecond)
	require.NoError(t, c.Thaw(ctx, unit))
	require.Eventually(t, func() bool { s, _ := ReadCgroup(CgroupRoot, path); return !s.Frozen }, 5*time.Second, 50*time.Millisecond)

	require.NoError(t, CgroupKill(CgroupRoot, path))
	_, _ = cmd.Process.Wait()
	require.Eventually(t, func() bool {
		_, err := os.Stat(filepath.Join(CgroupRoot, path))
		return os.IsNotExist(err)
	}, 10*time.Second, 100*time.Millisecond, "the scope is collected")
}
```

- [ ] **Run it** once, on purpose, through the queue: `loom job run -d "cgroups: live contract" -- env CGO_ENABLED=0 go test -tags systemd_live -run TestConfiner_Live -v ./sched/`. If a property is refused (`OOMPolicy` on a scope, say), record it in the spec's §15 and adjust `JoinScope`: a refused property otherwise leaves every job uncontained.

### Step 1.11: Docs, gates, commit

- [ ] `USAGE.md`, the work queue section: jobs run in their own systemd scopes under a slice weighted below the agents (`jobs_cpu_weight`), measured exactly; `contain` and `jobs_cpu_weight` in [Scheduler Caps](#scheduler-caps); the header's `uncontained: <why>`.
- [ ] `sched/README.md`: the new files and types (units, cgroup readers, confiner, the shim's join, `started.json`'s new fields, `spec.json`'s `scope`).
- [ ] `sched/CLAUDE.md` rules: **Join the scope before the command starts (`joinScope` before `startCommand`), and run the command uncontained, with the reason in `started.json`, when the join fails.** A process moved after it forked leaves its children behind, and a job refused for want of a scope is work lost. **Convention** — a job half in its scope. **Write `cgroup.kill` only through `CgroupKill`, which refuses anything but a loom job's scope.** A kill of a parent cgroup reaches every process below it, the user's session included. **Enforced** by `TestCgroupKill_RefusesWhatIsNotAJobScope`.
- [ ] `docs/claude/the-work-queue.md`: a rule: **Make every D-Bus call from a spawned job, never on the loop (`maybeProbeConfiner`, `runJobStart`), and drop the confiner after a failed call (`confinerFailed`).** A call that blocks on a wedged bus freezes every client. *Silent:* a frozen daemon.
- [ ] `docs/ARCHITECTURE.md`: a line where the work queue is described: jobs run in `loom-<tag>.slice` scopes under the user's systemd.
- [ ] Gates through the queue: `go vet ./...`, the full suite, `-race`, e2e, the Windows and darwin builds, `go run ./tools/claudemd`; `gofmt -l`.
- [ ] Commit: `git add` the files of this package by path, then

```bash
git commit -m "feat(sched,core,config): run each job in its own systemd scope, measured from its cgroup

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>" -- <the paths>
```

---

## Package 2: usage curves

### Step 2.1: The trace keeps the time of each point

**Files:** `sched/profile.go`, `sched/profile_test.go`

- [ ] **Write the failing test** in `sched/profile_test.go`:

```go
func TestTrace_KeepsEachPointsTime(t *testing.T) {
	t0 := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	var tr Trace
	for i := range tracePoints + 2 {
		tr.Add(t0.Add(time.Duration(i)*3*time.Second), TracePoint{X: float64(i)})
	}
	require.Len(t, tr.Points, (tracePoints+2+1)/2)
	assert.Equal(t, t0.Add(3*time.Second), tr.Points[0].At, "a merged pair keeps the later time")
	assert.Equal(t, t0.Add(time.Duration(tracePoints+1)*3*time.Second), tr.Points[len(tr.Points)-1].At)
}
```

- [ ] **Run** → FAIL (`At` undefined).
- [ ] **Implement:** `TracePoint` gains `At time.Time // when it was sampled`; `Trace.Add` sets `p.At = at` before appending, and the merge writes `TracePoint{X: b.X, RSSKB: max(…), Cores: max(…), At: b.At}`. Update the doc comments.
- [ ] **Run** `go test ./sched/ -run 'TestTrace|TestBuildProfile'` → PASS.

### Step 2.2: The curve

**Files:** `sched/curve.go`, `sched/curve_test.go`, `sched/belief.go`

- [ ] **Write the failing tests** in `sched/curve_test.go`:

```go
package sched

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var curveStart = time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)

// traceOf is a complete trace with one point every 3 s at the given
// progresses and memory (KB), each point 2 cores busy.
func traceOf(xs []float64, kb []int64) *Trace {
	var tr Trace
	for i := range xs {
		tr.Add(curveStart.Add(time.Duration(i+1)*3*time.Second), TracePoint{X: xs[i], RSSKB: kb[i], Cores: 2})
	}
	return &tr
}

func TestBuildCurve_KeepsTheRise(t *testing.T) {
	tr := traceOf([]float64{10, 20, 30, 40, 50}, []int64{1 << 20, 1 << 20, 8 << 20, 8 << 20, 2 << 20})
	c, ok := BuildCurve(tr, curveStart)
	require.True(t, ok)
	assert.Equal(t, 50.0, c.End)
	assert.Equal(t, []CurveStep{
		{Until: 20, Level: 1 << 20, Wall: 6},
		{Until: 40, Level: 8 << 20, Wall: 12},
		{Until: 50, Level: 2 << 20, Wall: 15},
	}, c.Mem)
	assert.Equal(t, []CurveStep{{Until: 50, Level: 2000, Wall: 15}}, c.Cores)
	require.NoError(t, c.Validate())
}

func TestBuildCurve_NeverBelowTheRunAndCompressed(t *testing.T) {
	var xs []float64
	var kb []int64
	for i := range 200 {
		xs = append(xs, float64(i+1)*1.5)
		kb = append(kb, int64((i*7919)%97+1)<<14)
	}
	tr := traceOf(xs, kb)
	c, ok := BuildCurve(tr, curveStart)
	require.True(t, ok)
	require.NoError(t, c.Validate())
	assert.LessOrEqual(t, len(c.Mem), curveSteps)
	for _, p := range tr.Points {
		level := int64(-1)
		for _, st := range c.Mem {
			if p.X <= st.Until {
				level = st.Level
				break
			}
		}
		assert.GreaterOrEqual(t, level, (p.RSSKB+1023)/1024*1024, "at progress %.1f", p.X)
	}
}

func TestBuildCurve_Refuses(t *testing.T) {
	_, ok := BuildCurve(nil, curveStart)
	assert.False(t, ok)
	_, ok = BuildCurve(&Trace{}, curveStart)
	assert.False(t, ok)
	tr := traceOf([]float64{1}, []int64{1})
	tr.Incomplete = true
	_, ok = BuildCurve(tr, curveStart)
	assert.False(t, ok)
	_, ok = BuildCurve(traceOf([]float64{1}, []int64{1}), time.Time{})
	assert.False(t, ok, "no start, no wall times")
}

func TestCurveStep_JSON(t *testing.T) {
	data, err := json.Marshal(CurveStep{Until: 12.5, Level: 1048576, Wall: 3.5})
	require.NoError(t, err)
	assert.JSONEq(t, `[12.5, 1048576, 3.5]`, string(data))
	var st CurveStep
	require.NoError(t, json.Unmarshal(data, &st))
	assert.Equal(t, CurveStep{Until: 12.5, Level: 1048576, Wall: 3.5}, st)
}

func TestCurve_Validate(t *testing.T) {
	good := Curve{End: 20, Mem: []CurveStep{{10, 1, 1}, {20, 2, 2}}, Cores: []CurveStep{{20, 1, 2}}}
	require.NoError(t, good.Validate())
	for name, c := range map[string]Curve{
		"no steps":      {End: 20, Mem: nil, Cores: good.Cores},
		"not moving on": {End: 20, Mem: []CurveStep{{10, 1, 1}, {10, 2, 2}}, Cores: good.Cores},
		"wall back":     {End: 20, Mem: []CurveStep{{10, 1, 3}, {20, 2, 2}}, Cores: good.Cores},
		"negative":      {End: 20, Mem: []CurveStep{{10, -1, 1}, {20, 2, 2}}, Cores: good.Cores},
		"short of end":  {End: 30, Mem: good.Mem, Cores: good.Cores},
	} {
		assert.Error(t, c.Validate(), name)
	}
}

func TestBelief_CurvesOnTheNewestRunsOnly(t *testing.T) {
	var b Belief
	for i := range ProfiledRuns + 2 {
		b.AddRun(Run{Job: JobID(i + 1), Outcome: OutcomeDone, Curve: &Curve{End: 1, Mem: []CurveStep{{1, 1, 1}}, Cores: []CurveStep{{1, 1, 1}}}})
	}
	for i, r := range b.Runs {
		assert.Equal(t, i < ProfiledRuns, r.Curve != nil, "run %d", i)
	}
	b.Runs[1].Curve.End = 0 // invalid
	assert.Len(t, CurvedRuns(&b), ProfiledRuns-1)
	assert.Empty(t, CurvedRuns(nil))
}
```

- [ ] **Run** → FAIL (undefined).
- [ ] **Implement** `sched/curve.go`:

```go
package sched

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"
)

// curveSteps is a curve's most steps per resource (cgroup spec §6.1).
const curveSteps = 16

// CurveStep is one step of a curve: Level holds for progress from the step
// before's Until (0 for the first) up to Until, and the run had been going
// Wall seconds when its progress reached Until. It is written as
// [until, level, wall].
type CurveStep struct {
	Until float64
	Level int64
	Wall  float64
}

// MarshalJSON writes s as [until, level, wall].
func (s CurveStep) MarshalJSON() ([]byte, error) {
	return json.Marshal([3]float64{s.Until, float64(s.Level), s.Wall})
}

// UnmarshalJSON reads [until, level, wall].
func (s *CurveStep) UnmarshalJSON(b []byte) error {
	var v [3]float64
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	s.Until, s.Level, s.Wall = v[0], int64(v[1]), v[2]
	return nil
}

// Curve is a done run's usage over its progress, rising and falling
// (cgroup spec §6.1): memory in KB rounded up to MB, cores in milli-cores,
// and the progress at its end.
type Curve struct {
	End   float64     `json:"end"`
	Mem   []CurveStep `json:"mem"`
	Cores []CurveStep `json:"cores"`
}

// BuildCurve is t's curve for a run that started at started, and false for
// no trace, an empty or incomplete one, or no start.
func BuildCurve(t *Trace, started time.Time) (*Curve, bool) {
	if t == nil || len(t.Points) == 0 || t.Incomplete || started.IsZero() {
		return nil, false
	}
	return &Curve{
		End:   roundUpX(t.x()),
		Mem:   buildCurveSteps(t.Points, started, func(p TracePoint) int64 { return (p.RSSKB + 1023) / 1024 * 1024 }),
		Cores: buildCurveSteps(t.Points, started, func(p TracePoint) int64 { return milliCores(p.Cores) }),
	}, true
}

// buildCurveSteps is one step per point (the highest level of points at one
// progress, and one step for a run of equal levels), merged down to
// curveSteps by joining, each time, the adjacent pair whose merge adds the
// least area; a merge keeps the higher level and the later Until and Wall,
// so the curve is never below the run.
func buildCurveSteps(points []TracePoint, started time.Time, level func(TracePoint) int64) []CurveStep {
	var steps []CurveStep
	for _, p := range points {
		st := CurveStep{Until: roundUpX(p.X), Level: level(p), Wall: max(0, p.At.Sub(started).Seconds())}
		k := len(steps) - 1
		switch {
		case k >= 0 && steps[k].Until == st.Until:
			steps[k].Level, steps[k].Wall = max(steps[k].Level, st.Level), st.Wall
		case k >= 0 && steps[k].Level == st.Level:
			steps[k].Until, steps[k].Wall = st.Until, st.Wall
		default:
			steps = append(steps, st)
		}
	}
	from := func(i int) float64 {
		if i == 0 {
			return 0
		}
		return steps[i-1].Until
	}
	for len(steps) > curveSteps {
		best, cost := 0, math.Inf(1)
		for i := 0; i+1 < len(steps); i++ {
			top := max(steps[i].Level, steps[i+1].Level)
			c := float64(top-steps[i].Level)*(steps[i].Until-from(i)) + float64(top-steps[i+1].Level)*(steps[i+1].Until-steps[i].Until)
			if c < cost {
				best, cost = i, c
			}
		}
		next := steps[best+1]
		steps[best] = CurveStep{Until: next.Until, Level: max(steps[best].Level, next.Level), Wall: next.Wall}
		steps = slices.Delete(steps, best+1, best+2)
	}
	return steps
}

// Validate says why c can't be trusted, nil when it can: each resource has 1
// to curveSteps steps whose progress rises, whose wall times never fall,
// nothing negative, and a last step ending at End.
func (c *Curve) Validate() error {
	if err := validateCurve(c.Mem, c.End); err != nil {
		return fmt.Errorf("memory: %w", err)
	}
	if err := validateCurve(c.Cores, c.End); err != nil {
		return fmt.Errorf("cores: %w", err)
	}
	return nil
}

func validateCurve(steps []CurveStep, end float64) error {
	switch {
	case len(steps) == 0:
		return errors.New("no steps")
	case len(steps) > curveSteps:
		return fmt.Errorf("%d steps, over %d", len(steps), curveSteps)
	}
	for i, st := range steps {
		switch {
		case st.Level < 0 || !(st.Until >= 0) || !(st.Wall >= 0):
			return fmt.Errorf("step %d is negative", i+1)
		case i > 0 && st.Until <= steps[i-1].Until:
			return fmt.Errorf("step %d does not move on", i+1)
		case i > 0 && st.Wall < steps[i-1].Wall:
			return fmt.Errorf("step %d goes back in time", i+1)
		}
	}
	if steps[len(steps)-1].Until != end {
		return errors.New("its last step does not end at the run's end")
	}
	return nil
}

// CurvedRuns is the valid curves among b's newest ProfiledRuns runs.
func CurvedRuns(b *Belief) []*Curve {
	if b == nil {
		return nil
	}
	var out []*Curve
	for _, r := range b.Runs[:min(len(b.Runs), ProfiledRuns)] {
		if r.Curve != nil && r.Curve.Validate() == nil {
			out = append(out, r.Curve)
		}
	}
	return out
}
```

- [ ] `sched/belief.go`: `Run` gains `Curve *Curve \`json:"curve,omitempty"\` // its usage over its progress, rising and falling (cgroup spec §6); only the newest ProfiledRuns keep one`; `AddRun` clears `Curve` beside `Profile` past `ProfiledRuns`, its comment naming both.
- [ ] **Run** `go test ./sched/` → PASS.

### Step 2.3: Curves at settle, checked at load

**Files:** `core/queue_beliefs.go`, `core/queue_beliefs_test.go`

- [ ] **Write the failing test** `TestJobQueue_ADoneRunKeepsItsCurve` in `core/queue_beliefs_test.go`, after the test that pins a done run's profile (copy its setup: a real shim, synthetic samples delivered through the pass): the settled run in `beliefs.json` has a `Curve` whose `End` equals its profile's and whose memory steps carry wall times above 0.
- [ ] **Run** → FAIL.
- [ ] **Implement:** in `recordRun`, beside `BuildProfile`: `if c, ok := sched.BuildCurve(trace, rec.Started); ok { run.Curve = c }`. In `loadBeliefs`, beside the profile check, log an invalid curve once per signature (`queue.curve_invalid`, the signature and the error); `CurvedRuns` already leaves it out.
- [ ] **Run** `go test ./core/ -run 'TestJobQueue_ADoneRun|TestLoadBeliefs'` → PASS.

### Step 2.4: Docs, gates, commit

- [ ] `sched/README.md`: the curve (its steps, the 16-step merge, `[until, level, wall]`, `CurvedRuns`), and `beliefs.json`'s `curve` on the newest 5 runs.
- [ ] `sched/CLAUDE.md`: extend the rule on `Hold` and `BuildProfile` to `BuildCurve` (pure; never below the run), **Enforced** by `TestBuildCurve_NeverBelowTheRunAndCompressed`.
- [ ] Gates: vet, full, race (through the queue), `gofmt -l`, `go run ./tools/claudemd`.
- [ ] Commit: `feat(sched,core): keep each profiled run's usage curve, rise included`.

---

## Package 3: greedy admission

### Step 3.1: The new scheduler keys

**Files:** `config/settings.go`, `sched/job.go`, `sched/policy.go`, `sched/policy_test.go`

- [ ] **Write the failing tests:** `TestResolve_Defaults` expects `ReserveGB: 4, CPUPressurePct: 25, CPUOvercommit: 2`; `TestResolve_Bounds` gains `interactive_reserve_gb` 1025, `cpu_pressure_pct` 101 and `cpu_overcommit` 9 (and a negative of each), each with its `scheduler.<key> is N: it must be lo–hi` text.
- [ ] **Run** → FAIL.
- [ ] **Implement:** `config.SchedulerSettings` gains `InteractiveReserveGB int \`json:"interactive_reserve_gb,omitempty"\``, `CPUPressurePct int \`json:"cpu_pressure_pct,omitempty"\`` and `CPUOvercommit int \`json:"cpu_overcommit,omitempty"\``; `sched.Policy` gains `ReserveGB int \`json:"reserve_gb"\``, `CPUPressurePct int \`json:"cpu_pressure_pct"\`` and `CPUOvercommit int \`json:"cpu_overcommit"\``; `Resolve` resolves them after `jobs_cpu_weight` with defaults 4, 25, 2 and bounds 1–1024, 1–100, 1–8, its comment listing them. `setSchedulerCaps` keeps all three (extend its test).
- [ ] **Run** → PASS.

### Step 3.2: Predictions

**Files:** `sched/predict.go`, `sched/predict_test.go`

- [ ] **Write the failing tests** in `sched/predict_test.go`:

```go
package sched

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var t0 = time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)

func at(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }

func TestPrediction_EnvelopeAndAt(t *testing.T) {
	p := envelope([]Span{{at(0), at(60), 5}, {at(30), at(90), 8}, {at(120), at(150), 2}})
	assert.Equal(t, Prediction{{at(0), at(30), 5}, {at(30), at(90), 8}, {at(120), at(150), 2}}, p)
	assert.Equal(t, int64(5), p.At(at(0)))
	assert.Equal(t, int64(8), p.At(at(30)), "a span holds its start")
	assert.Equal(t, int64(0), p.At(at(90)), "and not its end")
	assert.Equal(t, int64(0), p.At(at(100)))
	assert.Equal(t, at(150), p.End())
	assert.Equal(t, Prediction{{at(0), at(60), 3}}, envelope([]Span{{at(0), at(30), 3}, {at(30), at(60), 3}}), "equal neighbours merge")
}

func TestFlat(t *testing.T) {
	assert.Equal(t, Prediction{{at(0), at(10), 7}}, Flat(7, at(0), at(10)))
	assert.Equal(t, Prediction{{at(0), at(60), 7}}, Flat(7, at(0), at(0)), "a minute when the end has passed")
}

// rising is a run whose memory is 1 GB for its first 100 CPU-seconds (50 s)
// and 8 GB to its end at 200 (100 s).
func rising() *Curve {
	return &Curve{End: 200, Mem: []CurveStep{{100, 1 << 20, 50}, {200, 8 << 20, 100}}, Cores: []CurveStep{{200, 2000, 100}}}
}

func TestPredict_FromTheStart(t *testing.T) {
	p, ok := Predict([]*Curve{rising(), rising(), rising()}, 0, t0)
	require.True(t, ok)
	// 1 GB × 1.15 from 0 to 50 s, widened to 80 s; 8 GB × 1.15 from 50 s,
	// widened back 30 s to 20 s, to 100 s, widened to 130 s.
	assert.Equal(t, Prediction{{at(0), at(20), 1205863}, {at(20), at(130), 9646900}}, p)
}

func TestPredict_FromItsProgress(t *testing.T) {
	// At progress 150 the run had been going 75 s; 25 s of its peak are left.
	p, ok := Predict([]*Curve{rising(), rising(), rising()}, 150, t0)
	require.True(t, ok)
	assert.Equal(t, Prediction{{at(0), at(55), 9646900}}, p)
}

func TestPredict_NeedsThreeRunsThatLastedPastIt(t *testing.T) {
	_, ok := Predict([]*Curve{rising(), rising()}, 0, t0)
	assert.False(t, ok, "two curves")
	short := &Curve{End: 120, Mem: []CurveStep{{120, 1 << 20, 60}}, Cores: []CurveStep{{120, 1, 60}}}
	_, ok = Predict([]*Curve{rising(), rising(), short}, 150, t0)
	assert.False(t, ok, "one of three ended before 150")
}

func TestPeakSum(t *testing.T) {
	a := Flat(9, at(0), at(60))
	b := Prediction{{at(0), at(60), 1}, {at(60), at(120), 9}}
	assert.Equal(t, int64(10), PeakSum([]Prediction{a, b}, at(0), at(120)), "the peaks don't meet")
	c := Prediction{{at(30), at(90), 9}}
	assert.Equal(t, int64(18), PeakSum([]Prediction{a, c}, at(0), at(120)), "they do")
	assert.Equal(t, int64(9), PeakSum([]Prediction{a, c}, at(0), at(20)), "not within the window")
}
```

- [ ] **Run** → FAIL.
- [ ] **Implement** `sched/predict.go`:

```go
package sched

import (
	"math"
	"slices"
	"sort"
	"time"
)

// The prediction's margins (cgroup spec §7.2).
const (
	widenMin   = 30 * time.Second // the least a predicted edge moves
	widenShare = 0.25             // of how far ahead of its anchor the edge is
)

// Span is a stretch of time in which a job is expected to use KB of memory.
type Span struct {
	From, Until time.Time
	KB          int64
}

// Prediction is a job's expected memory over time (cgroup spec §7.2): spans
// in time order that never overlap, nothing outside them.
type Prediction []Span

// At is what p expects at t: a span holds its From and not its Until.
func (p Prediction) At(t time.Time) int64 {
	i := sort.Search(len(p), func(i int) bool { return p[i].Until.After(t) })
	if i < len(p) && !t.Before(p[i].From) {
		return p[i].KB
	}
	return 0
}

// End is when p's last span ends, zero for none.
func (p Prediction) End() time.Time {
	if len(p) == 0 {
		return time.Time{}
	}
	return p[len(p)-1].Until
}

// Flat is kb from anchor to until, or for a minute when until has passed.
func Flat(kb int64, anchor, until time.Time) Prediction {
	if !until.After(anchor) {
		until = anchor.Add(time.Minute)
	}
	return Prediction{{From: anchor, Until: until, KB: kb}}
}

// WithMarginKB is kb with sizing's 15% margin, rounded up.
func WithMarginKB(kb int64) int64 { return int64(math.Ceil(float64(kb) * memoryMargin)) }

// envelope is the most of spans at each moment: between each pair of
// adjacent edges, the highest level of the spans holding that stretch;
// equal neighbours merge, and stretches at 0 drop.
func envelope(spans []Span) Prediction {
	var edges []time.Time
	for _, s := range spans {
		edges = append(edges, s.From, s.Until)
	}
	slices.SortFunc(edges, func(a, b time.Time) int { return a.Compare(b) })
	edges = slices.CompactFunc(edges, func(a, b time.Time) bool { return a.Equal(b) })
	var out Prediction
	for i := 0; i+1 < len(edges); i++ {
		from, until := edges[i], edges[i+1]
		var kb int64
		for _, s := range spans {
			if !s.From.After(from) && s.Until.After(from) {
				kb = max(kb, s.KB)
			}
		}
		switch n := len(out); {
		case kb == 0:
		case n > 0 && out[n-1].KB == kb && out[n-1].Until.Equal(from):
			out[n-1].Until = until
		default:
			out = append(out, Span{From: from, Until: until, KB: kb})
		}
	}
	return out
}

// widen is how far a predicted edge at t moves from anchor's view: 30 s, or
// a quarter of how far ahead it is, whichever is more.
func widen(anchor, t time.Time) time.Duration {
	return max(widenMin, time.Duration(widenShare*float64(t.Sub(anchor))))
}

// seconds is s seconds as a duration.
func seconds(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }

// wallAt is the wall time c's run had been going when its progress reached
// x, interpolated within the memory step holding x; past its end, its last.
func (c *Curve) wallAt(x float64) float64 {
	prevUntil, prevWall := 0.0, 0.0
	for _, st := range c.Mem {
		if x < st.Until {
			return prevWall + (st.Wall-prevWall)*(x-prevUntil)/(st.Until-prevUntil)
		}
		prevUntil, prevWall = st.Until, st.Wall
	}
	return prevWall
}

// runSpans is c's memory from progress x on, from anchor: each step ending
// past x lasts from the wall time the run reached its start (or x, for the
// step holding x) to the wall time it reached its end, less the run's wall
// time at x; each level carries the margin, and each edge is widened but
// never moved before anchor.
func runSpans(c *Curve, x float64, anchor time.Time) []Span {
	wallX := c.wallAt(x)
	var out []Span
	prevWall := 0.0
	for _, st := range c.Mem {
		if st.Until > x {
			from := anchor.Add(seconds(max(prevWall, wallX) - wallX))
			until := anchor.Add(seconds(st.Wall - wallX))
			from, until = from.Add(-widen(anchor, from)), until.Add(widen(anchor, until))
			if from.Before(anchor) {
				from = anchor
			}
			out = append(out, Span{From: from, Until: until, KB: WithMarginKB(st.Level)})
		}
		prevWall = st.Wall
	}
	return out
}

// Predict is a job's prediction from its belief's curves (cgroup spec
// §7.2): from progress x (0 for a job not started) and anchored at anchor
// (now, or the start), the most of the spans of every curve whose run lasted
// past x. It is false when fewer than holdRuns runs did.
func Predict(curves []*Curve, x float64, anchor time.Time) (Prediction, bool) {
	var spans []Span
	n := 0
	for _, c := range curves {
		if c.End > x {
			spans = append(spans, runSpans(c, x, anchor)...)
			n++
		}
	}
	if n < holdRuns {
		return nil, false
	}
	return envelope(spans), true
}

// PeakSum is the most preds use together in [from, to): their sum at from
// and at the start of every span within it, where a sum can only rise.
func PeakSum(preds []Prediction, from, to time.Time) int64 {
	points := []time.Time{from}
	for _, p := range preds {
		for _, s := range p {
			if s.From.After(from) && s.From.Before(to) {
				points = append(points, s.From)
			}
		}
	}
	var peak int64
	for _, t := range points {
		var sum int64
		for _, p := range preds {
			sum += p.At(t)
		}
		peak = max(peak, sum)
	}
	return peak
}
```

Add `sort` to the imports for `At`; `memoryMargin` (1.15) is sizing's, in `sched/size.go`. The test's `1205863` is ⌈1048576 × 1.15⌉ and `9646900` is ⌈8388608 × 1.15⌉.
- [ ] **Run** → PASS.

### Step 3.3: `Admit` through a fitter, and the greedy fit

**Files:** `sched/admit.go`, `sched/greedy.go`, `sched/greedy_test.go`, `sched/admit_test.go`

- [ ] **Write the failing tests** in `sched/greedy_test.go`:

```go
package sched

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func greedyPolicy() Policy {
	return Policy{Caps: Caps{CPUs: 14, MemoryGB: 14, IOJobs: 2}, DefaultClaim: Claim{CPUs: 4, MemoryGB: 6},
		StarveAfter: 10 * time.Minute, ReserveGB: 4, CPUPressurePct: 25, CPUOvercommit: 2}
}

func gb(n int64) int64 { return n * kbPerGB }

// flatPredict predicts each job flat at its claim for ten minutes, or at
// the shape in shapes.
func flatPredict(shapes map[JobID]func(start time.Time) Prediction) func(Record, time.Time) Prediction {
	return func(r Record, start time.Time) Prediction {
		if f, ok := shapes[r.ID]; ok {
			return f(start)
		}
		return Flat(gb(int64(r.Claim.MemoryGB)), start, start.Add(10*time.Minute))
	}
}

func running(id JobID, c Claim) Record {
	return Record{ID: id, Order: int64(id), State: Running, Claim: c, Started: t0.Add(-time.Minute)}
}

func queued(id JobID, c Claim) Record {
	return Record{ID: id, Order: int64(id), State: Queued, Claim: c, Submitted: t0, Ready: t0}
}

func TestBudget(t *testing.T) {
	p := greedyPolicy()
	assert.Equal(t, gb(10), Budget(gb(2), gb(12), p), "used + available − reserve")
	assert.Equal(t, gb(14), Budget(gb(10), gb(20), p), "held to the memory cap")
	assert.Equal(t, int64(0), Budget(0, gb(1), p), "never below 0")
}

func TestAdmitGreedy_PeaksThatDontMeetBothStart(t *testing.T) {
	g := Greedy{
		BudgetKB: gb(12),
		Running:  map[JobID]Prediction{1: {{t0, t0.Add(time.Minute), gb(9)}, {t0.Add(time.Minute), t0.Add(10 * time.Minute), gb(1)}}},
		Predict: flatPredict(map[JobID]func(time.Time) Prediction{2: func(s time.Time) Prediction {
			return Prediction{{s, s.Add(5 * time.Minute), gb(1)}, {s.Add(5 * time.Minute), s.Add(8 * time.Minute), gb(9)}}
		}}),
	}
	jobs := []Record{running(1, Claim{CPUs: 4, MemoryGB: 10}), queued(2, Claim{CPUs: 4, MemoryGB: 10})}
	assert.Equal(t, []JobID{2}, AdmitGreedy(jobs, greedyPolicy(), t0, g).Start)
	assert.Empty(t, Admit(jobs, greedyPolicy(), t0, nil).Start, "claims alone would hold it back")
}

func TestAdmitGreedy_PeaksThatMeetWait(t *testing.T) {
	g := Greedy{BudgetKB: gb(12), Running: map[JobID]Prediction{1: Flat(gb(9), t0, t0.Add(10*time.Minute))}, Predict: flatPredict(nil)}
	jobs := []Record{running(1, Claim{CPUs: 4, MemoryGB: 9}), queued(2, Claim{CPUs: 4, MemoryGB: 9})}
	d := AdmitGreedy(jobs, greedyPolicy(), t0, g)
	assert.Empty(t, d.Start)
	assert.Equal(t, "needs ~9 GB at its peak (forecast leaves 3)", d.Waiting[2])
}

func TestAdmitGreedy_JobsStartedInThePassCount(t *testing.T) {
	g := Greedy{BudgetKB: gb(12), Running: map[JobID]Prediction{}, Predict: flatPredict(nil)}
	jobs := []Record{queued(1, Claim{CPUs: 1, MemoryGB: 7}), queued(2, Claim{CPUs: 1, MemoryGB: 7})}
	d := AdmitGreedy(jobs, greedyPolicy(), t0, g)
	assert.Equal(t, []JobID{1}, d.Start)
	assert.Contains(t, d.Waiting[2], "needs ~7 GB")
}

func TestAdmitGreedy_CPU(t *testing.T) {
	p := greedyPolicy()
	g := Greedy{BudgetKB: gb(100), Running: map[JobID]Prediction{}, Predict: flatPredict(nil)}
	var jobs []Record
	for i := range 7 {
		jobs = append(jobs, running(JobID(i+1), Claim{CPUs: 4, MemoryGB: 1}))
	}
	for id := range JobID(7) {
		g.Running[id+1] = Flat(1, t0, t0.Add(time.Minute))
	}
	jobs = append(jobs, queued(8, Claim{CPUs: 4, MemoryGB: 1}))
	assert.Equal(t, "CPU overcommitted (32/28 claimed)", AdmitGreedy(jobs, p, t0, g).Waiting[8])

	jobs = []Record{running(1, Claim{CPUs: 4, MemoryGB: 1}), queued(2, Claim{CPUs: 4, MemoryGB: 1})}
	g.Running = map[JobID]Prediction{1: Flat(1, t0, t0.Add(time.Minute))}
	g.CPUPressure = 25
	assert.Equal(t, "CPU busy (pressure 25%)", AdmitGreedy(jobs, p, t0, g).Waiting[2])
	g.CPUPressure = 24.9
	assert.Equal(t, []JobID{2}, AdmitGreedy(jobs, p, t0, g).Start)
	g.CPUPressure = 90
	assert.Equal(t, []JobID{2}, AdmitGreedy([]Record{queued(2, Claim{CPUs: 4, MemoryGB: 1})}, p, t0, g).Start, "pressure counts only while a job runs")
}

func TestAdmitGreedy_ExclusiveIOAndHoldoff(t *testing.T) {
	p := greedyPolicy()
	g := Greedy{BudgetKB: gb(100), Running: map[JobID]Prediction{1: Flat(1, t0, t0.Add(time.Minute))}, Predict: flatPredict(nil)}
	ex := queued(2, Claim{Exclusive: true})
	assert.Equal(t, "waits for the host to empty", AdmitGreedy([]Record{running(1, Claim{CPUs: 1, MemoryGB: 1}), ex}, p, t0, g).Waiting[2])
	assert.Equal(t, []JobID{2}, AdmitGreedy([]Record{ex}, p, t0, g).Start)
	busy := running(1, Claim{Exclusive: true})
	assert.Equal(t, "waits for exclusive job 1", AdmitGreedy([]Record{busy, queued(2, Claim{CPUs: 1, MemoryGB: 1})}, p, t0, g).Waiting[2])

	io := []Record{running(1, Claim{CPUs: 1, MemoryGB: 1, IO: true}), running(3, Claim{CPUs: 1, MemoryGB: 1, IO: true}), queued(2, Claim{CPUs: 1, MemoryGB: 1, IO: true})}
	g.Running[3] = Flat(1, t0, t0.Add(time.Minute))
	assert.Equal(t, "needs an I/O slot", AdmitGreedy(io, p, t0, g).Waiting[2])

	g.Holdoff = "memory is short (job 1 paused)"
	assert.Equal(t, g.Holdoff, AdmitGreedy([]Record{queued(2, Claim{CPUs: 1, MemoryGB: 1})}, p, t0, g).Waiting[2])
}

func TestAdmitGreedy_KeepsTheGuards(t *testing.T) {
	p := greedyPolicy()
	g := Greedy{BudgetKB: gb(100), Running: map[JobID]Prediction{}, Predict: flatPredict(nil)}
	over := queued(1, Claim{CPUs: 4, MemoryGB: 15})
	assert.Equal(t, waitOver, AdmitGreedy([]Record{over}, p, t0, g).Waiting[1])
	head := queued(2, Claim{CPUs: 4, MemoryGB: 2})
	head.Ready = t0.Add(-11 * time.Minute)
	d := AdmitGreedy([]Record{head, queued(3, Claim{CPUs: 1, MemoryGB: 1})}, p, t0, g)
	assert.Equal(t, []JobID{2}, d.Start)
	assert.Equal(t, JobID(2), d.Starved)
	assert.Contains(t, d.Waiting[3], "waits for job 2")
}
```

and in `sched/admit_test.go`: `TestAdmit_ReportsTheStarvedHead` — the claim-based `Admit` sets `Decision.Starved` to a head started past `StarveAfter`, and leaves it 0 for a first-fit start.
- [ ] **Run** → FAIL.
- [ ] **Implement.** In `sched/admit.go`: `Decision` gains `Starved JobID // the head the starvation guard started; 0 for none`. Introduce:

```go
// fitter is how admission counts what runs and decides what fits: claims
// (claimFit, spec §5.2) or the greedy forecast (greedyFit, cgroup spec §7).
type fitter interface {
	count(r Record)       // a running job
	fits(r Record) string // why r doesn't fit what is counted; "" when it does
	take(r Record)        // r starts: count it
}

// claimFit counts running jobs at their holds or claims.
type claimFit struct {
	p     Policy
	holds map[JobID]Need
	used  Need
}

func (f *claimFit) count(r Record)       { f.used = f.used.plus(runningNeed(r, f.p, f.holds)) }
func (f *claimFit) fits(r Record) string { return shortfall(f.used, r.Claim.Need(f.p.Caps), f.p.Caps) }
func (f *claimFit) take(r Record)        { f.used = f.used.plus(r.Claim.Need(f.p.Caps)) }
```

`Admit` becomes `return admitWith(jobs, p, now, &claimFit{p: p, holds: holds})`, and `admitWith` is today's body with `case Running: f.count(r)`, the head's `shortfall(...)` replaced by `f.fits(head)` (and `d.Starved = head.ID` when it starts), and the first-fit loop's by `f.fits(r)` then `f.take(r)`. Its comment moves to `admitWith`; `Admit`'s keeps the claim rules. `sched/greedy.go`:

```go
package sched

import (
	"fmt"
	"slices"
	"time"
)

// Greedy is what greedy admission needs beside the records (cgroup spec §7).
type Greedy struct {
	BudgetKB int64
	// Running is each running job's prediction from now. A running job
	// without one (a start the forecast simulates) is predicted from its
	// start through Predict.
	Running map[JobID]Prediction
	// Predict is a job's prediction from start: a candidate's, or a
	// simulated start's.
	Predict     func(r Record, start time.Time) Prediction
	CPUPressure float64 // the jobs slice's cpu.pressure "some" avg10
	Holdoff     string  // set: nothing starts, and this is why (the ladder)
}

// Budget is the memory greedy admission packs into, in KB (cgroup spec
// §7.1): what the running jobs use now plus the host's available memory,
// less the reserve, held between 0 and the memory cap.
func Budget(usedKB, availKB int64, p Policy) int64 {
	b := usedKB + availKB - int64(p.ReserveGB)*kbPerGB
	return min(max(b, 0), int64(p.Caps.MemoryGB)*kbPerGB)
}

// AdmitGreedy is Admit with the greedy fit (cgroup spec §7.2–§7.3): the
// same queue walk, over-cap skip and starvation guard, a pure function of
// its arguments.
func AdmitGreedy(jobs []Record, p Policy, now time.Time, g Greedy) Decision {
	return admitWith(jobs, p, now, &greedyFit{p: p, g: g, now: now})
}

// greedyFit counts running jobs by their predictions and claimed cores.
type greedyFit struct {
	p         Policy
	g         Greedy
	now       time.Time
	preds     []Prediction
	cores     int
	slots     int
	exclusive JobID // a running or started exclusive job
	any       bool  // a job runs or has started
}

func (f *greedyFit) count(r Record) {
	pred, ok := f.g.Running[r.ID]
	if !ok {
		pred = f.g.Predict(r, r.Started)
	}
	f.add(r, pred)
}

func (f *greedyFit) take(r Record) { f.add(r, f.g.Predict(r, f.now)) }

func (f *greedyFit) add(r Record, pred Prediction) {
	f.any = true
	if r.Claim.Exclusive {
		f.exclusive = r.ID
		return
	}
	f.cores += r.Claim.CPUs
	if r.Claim.IO {
		f.slots++
	}
	f.preds = append(f.preds, pred)
}

func (f *greedyFit) fits(r Record) string {
	limit := f.p.CPUOvercommit * f.p.Caps.CPUs
	switch {
	case f.g.Holdoff != "":
		return f.g.Holdoff
	case f.exclusive != 0:
		return fmt.Sprintf("waits for exclusive job %d", f.exclusive)
	case r.Claim.Exclusive && f.any:
		return "waits for the host to empty"
	case r.Claim.Exclusive:
		return ""
	case f.cores+r.Claim.CPUs > limit:
		return fmt.Sprintf("CPU overcommitted (%d/%d claimed)", f.cores+r.Claim.CPUs, limit)
	case f.any && f.g.CPUPressure >= float64(f.p.CPUPressurePct):
		return fmt.Sprintf("CPU busy (pressure %.0f%%)", f.g.CPUPressure)
	case r.Claim.IO && f.slots >= f.p.Caps.IOJobs:
		return "needs an I/O slot"
	}
	cand := f.g.Predict(r, f.now)
	end := cand.End()
	if PeakSum(append(slices.Clone(f.preds), cand), f.now, end) <= f.g.BudgetKB {
		return ""
	}
	need := (PeakSum([]Prediction{cand}, f.now, end) + kbPerGB - 1) / kbPerGB
	left := max(0, f.g.BudgetKB-PeakSum(f.preds, f.now, end)) / kbPerGB
	return fmt.Sprintf("needs ~%d GB at its peak (forecast leaves %d)", need, left)
}
```

- [ ] **Run** `go test ./sched/` → PASS (every existing `TestAdmit_…` stays green: the refactor changes no claim decision).

### Step 3.4: The greedy forecast

**Files:** `sched/forecast.go`, `sched/forecast_test.go`

- [ ] **Write the failing test** `TestForecastGreedy` in `sched/forecast_test.go`: one running job (started 1 minute ago, usual 5 minutes) predicted flat at 9 GB in `Running`, budget 12 GB, a queued 9 GB job: its forecast start is the running job's expected end (`Started + 5m`); with `Holdoff` set the forecast still gives that start.
- [ ] **Run** → FAIL.
- [ ] **Implement:** rename `Forecast`'s body `forecastWith(jobs []Record, p Policy, now time.Time, usual func(Record) (time.Duration, bool), fallback time.Duration, admit func(sim []Record, clock time.Time) Decision)`, calling `admit(sim, clock)` where it called `Admit(sim, p, clock, holds)`. `Forecast` passes `func(sim []Record, clock time.Time) Decision { return Admit(sim, p, clock, holds) }`, and:

```go
// ForecastGreedy is Forecast with the greedy fit (cgroup spec §7.4): a
// simulated start is predicted from its simulated start, and the ladder's
// hold-off is left out, since it passes.
func ForecastGreedy(jobs []Record, p Policy, now time.Time, g Greedy, usual func(Record) (time.Duration, bool), fallback time.Duration) map[JobID]time.Time {
	g.Holdoff = ""
	return forecastWith(jobs, p, now, usual, fallback, func(sim []Record, clock time.Time) Decision {
		return AdmitGreedy(sim, p, clock, g)
	})
}
```

- [ ] **Run** `go test ./sched/` → PASS.

### Step 3.5: The model admits greedily

**Files:** `core/queue_greedy.go` (new), `core/queue_admit.go`, `core/queue_watch.go`, `core/queue_view.go`, `core/queue_greedy_test.go` (new)

- [ ] **Implement** `core/queue_greedy.go`:

```go
package core

import (
	"time"

	"github.com/aidan-bailey/loom/sched"
)

// kbPerGB is a GB in KB.
const kbPerGB = 1 << 20

// usualOf is a job's usual duration at its claimed cores, and whether it has
// one (the view's forecast and greedy admission share it).
func (m *Model) usualOf(r sched.Record) (time.Duration, bool) {
	u, _, n, fromPrior := sched.Usual(m.queue.beliefOf(r.Signature), r.Claim.CPUs)
	return u, n > 0 || fromPrior
}

// greedyInputs is what greedy admission needs now (cgroup spec §7), and
// false in fallback mode or before a pass has read the host's memory.
func (m *Model) greedyInputs(p sched.Policy, now time.Time) (sched.Greedy, bool) {
	q := m.queue
	if q.containWhy(p) != "" || q.availKB <= 0 {
		return sched.Greedy{}, false
	}
	fallback := m.hostUsual()
	expect := func(r sched.Record) time.Duration {
		if d, ok := m.usualOf(r); ok && d > 0 {
			return d
		}
		return fallback
	}
	g := sched.Greedy{Running: map[sched.JobID]sched.Prediction{}}
	var used int64
	for i := range q.store.Jobs {
		rec := q.store.Jobs[i]
		if rec.State != sched.Running {
			continue
		}
		used += q.usage[rec.ID].RSSKB
		g.Running[rec.ID] = m.predictRunning(rec, now, expect)
	}
	g.BudgetKB = sched.Budget(used, q.availKB, p)
	g.Predict = func(r sched.Record, start time.Time) sched.Prediction { return m.predictNew(r, start, expect) }
	if q.slice != nil {
		g.CPUPressure = q.slice.CPUPressure
	}
	return g, true
}

// predictRunning is a running job's prediction from now (cgroup spec §7.2):
// a contained job whose command has curves follows them from its progress;
// any other counts flat at its hold or claim until its expected end; and a
// job using more than predicted counts flat at its use plus the margin.
func (m *Model) predictRunning(rec sched.Record, now time.Time, expect func(sched.Record) time.Duration) sched.Prediction {
	q := m.queue
	end := rec.Started.Add(expect(rec))
	if !end.After(now) {
		end = now.Add(time.Minute)
	}
	var pred sched.Prediction
	ok := false
	if tr := q.traces[rec.ID]; rec.Cgroup != "" && !rec.Adopted && tr != nil && len(tr.Points) > 0 {
		pred, ok = sched.Predict(sched.CurvedRuns(q.beliefOf(rec.Signature)), tr.Points[len(tr.Points)-1].X, now)
	}
	if !ok {
		gb := rec.Claim.MemoryGB
		if h, held := q.holding[rec.ID]; held {
			gb = h.MemoryGB
		}
		pred = sched.Flat(int64(gb)*kbPerGB, now, end)
	}
	if use := sched.WithMarginKB(q.usage[rec.ID].RSSKB); use > pred.At(now) {
		pred = sched.Flat(use, now, max(end, pred.End()))
	}
	return pred
}

// predictNew is a job's prediction from start: its command's curves, or flat
// at its claim for its expected duration.
func (m *Model) predictNew(r sched.Record, start time.Time, expect func(sched.Record) time.Duration) sched.Prediction {
	if pred, ok := sched.Predict(sched.CurvedRuns(m.queue.beliefOf(r.Signature)), 0, start); ok {
		return pred
	}
	return sched.Flat(int64(r.Claim.MemoryGB)*kbPerGB, start, start.Add(expect(r)))
}
```

(`max` of two `time.Time` doesn't compile: write a `later(a, b time.Time) time.Time` helper. If `core` already defines `kbPerGB`, use it.) `queueView`'s `usualOf` closure becomes `m.usualOf`.
- [ ] **Admission** (`admitJobs`): replace `dec := sched.Admit(jobs, policy, now, q.holding)` with

```go
		var dec sched.Decision
		if g, ok := m.greedyInputs(policy, now); ok {
			dec = sched.AdmitGreedy(jobs, policy, now, g)
		} else {
			dec = sched.Admit(jobs, policy, now, q.holding)
		}
```

`Grant` keeps its arguments. In `deliverQueuePass`, after the holds refresh: `if q.hasQueued() && q.containWhy(q.policy) == "" { m.admitJobs() }`, so a greedy queue is admitted after every sample (cgroup spec §7.1).
- [ ] **View:** `QueueView` gains `Greedy bool`, `BudgetKB int64` and `PeakKB int64` (the running jobs' forecast peak from now); `queueView` computes `g, ok := m.greedyInputs(q.policy, now)` once, and when `ok` sets them (`PeakKB = sched.PeakSum(<g.Running's values>, now, <their latest End>)`) and takes `starts` from `sched.ForecastGreedy`, else from `sched.Forecast` as today.
- [ ] **Core tests** (`core/queue_greedy_test.go`), with a fake confiner and a temp cgroup root as in Package 1:
  1. `TestQueueGreedy_AdmitsWhatClaimsWouldHoldBack`: caps 14 GB; a running contained job claiming 10 GB whose belief has 3 curves (seeded into `beliefs.json` through `sched.SaveBeliefs` before boot) peaking early and long past; a queued 10 GB job of a command whose curves peak late: it starts once a pass has read `availKB` (inject `readMemAvailable` to return 20 GB).
  2. `TestQueueGreedy_WaitsWithTheForecastsReason`: the same with colliding peaks: the queued job's `Waiting` is `needs ~… GB at its peak (forecast leaves …)`.
  3. `TestQueueGreedy_FallbackAdmitsByClaims`: no confiner: the second job waits `needs 10 GB (4 free)`.
  4. `TestQueueGreedy_ARunningJobAboveItsPredictionCountsAtItsUse`: a running job's usage (fed through a pass) above its curve's level makes a queued job that fit before wait.
- [ ] **Run** `go test ./core/ -run 'TestQueueGreedy|TestJobQueue|TestQueueContain'` → PASS.

### Step 3.6: The view, docs, gates, commit

- [ ] `ui/work.go`: in greedy mode the header shows `budget 19 GB · forecast peak 16 GB` (from `BudgetKB`, `PeakKB`, rounded to GB) in place of the claimed-memory part; add a header test case.
- [ ] `USAGE.md`: greedy admission (the budget, the forecast, the CPU gate, the new wait reasons) and the three keys in [Scheduler Caps](#scheduler-caps).
- [ ] `sched/README.md`: predictions, `Budget`, the fitters, `AdmitGreedy`, `ForecastGreedy`.
- [ ] `sched/CLAUDE.md`: **Keep `AdmitGreedy`, `Predict` and `PeakSum` pure, and never start a job whose prediction, summed with the running jobs', passes the budget.** The model calls them at every sample; a start past the budget is the OOM the ladder then has to undo. **Enforced** by `TestAdmitGreedy_PeaksThatMeetWait` and `TestAdmitGreedy_JobsStartedInThePassCount`.
- [ ] `docs/claude/the-work-queue.md`: greedy mode runs only with a confiner (`greedyInputs`), and a pass admits after every sample while jobs wait.
- [ ] Gates: vet, full, race, e2e (through the queue), `gofmt -l`, `go run ./tools/claudemd`.
- [ ] Commit: `feat(sched,core,config,ui): admit greedily on forecast memory overlap within live headroom`.

---

## Package 4: the ladder

### Step 4.1: The ladder, pure

**Files:** `sched/ladder.go`, `sched/ladder_test.go`

- [ ] **Write the failing tests** in `sched/ladder_test.go`:

```go
package sched

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ladderIn(now time.Time, headroomGB int64, pressure float64, jobs ...LadderJob) LadderInput {
	return LadderInput{Now: now, HeadroomKB: gb(headroomGB), ReserveKB: gb(4), MemPressure: pressure, Jobs: jobs}
}

func ljob(id JobID, startedAgo time.Duration) LadderJob {
	return LadderJob{ID: id, Started: t0.Add(-startedAgo), WorkingKB: gb(3)}
}

func TestLadder_Short(t *testing.T) {
	assert.False(t, ladderIn(t0, 4, 9.99).Short(), "headroom at the reserve is not short")
	assert.True(t, ladderIn(t0, 3, 0).Short(), "below it is")
	assert.True(t, ladderIn(t0, 20, 10).Short(), "pressure at 10% is")
}

func TestLadder_SqueezesTheMostRecentlyStartedFirst(t *testing.T) {
	_, act := Ladder(LadderState{}, ladderIn(t0, 3, 0, ljob(1, time.Hour), ljob(2, time.Minute)))
	require.NotNil(t, act)
	assert.Equal(t, Action{Kind: ActSqueeze, Job: 2, MemoryHighKB: gb(3)}, *act)
}

func TestLadder_VictimOrder(t *testing.T) {
	starved := ljob(2, time.Minute)
	starved.Starved = true
	ex := ljob(3, time.Second)
	ex.Exclusive = true
	maxed := ljob(4, 2*time.Second)
	maxed.Requeues = MaxRequeues
	_, act := Ladder(LadderState{}, ladderIn(t0, 3, 0, ljob(1, time.Hour), starved, ex, maxed))
	require.NotNil(t, act)
	assert.Equal(t, JobID(1), act.Job, "a starved head goes last; exclusive and requeued-out jobs never")
	_, act = Ladder(LadderState{}, ladderIn(t0, 3, 0, starved, ex, maxed))
	require.NotNil(t, act)
	assert.Equal(t, JobID(2), act.Job)
	_, act = Ladder(LadderState{}, ladderIn(t0, 3, 0, ex, maxed))
	assert.Nil(t, act)
}

func TestLadder_SqueezeThenPauseThenRequeue(t *testing.T) {
	j := ljob(1, time.Minute)
	j.Rung, j.RungAt = RungSqueezed, t0.Add(-14*time.Second)
	_, act := Ladder(LadderState{}, ladderIn(t0, 3, 0, j))
	assert.Nil(t, act, "15 s after the squeeze")
	j.RungAt = t0.Add(-15 * time.Second)
	_, act = Ladder(LadderState{}, ladderIn(t0, 3, 0, j))
	require.NotNil(t, act)
	assert.Equal(t, ActPause, act.Kind)

	j.Rung, j.RungAt = RungPaused, t0.Add(-29*time.Second)
	_, act = Ladder(LadderState{}, ladderIn(t0, 3, 0, j))
	assert.Nil(t, act, "30 s after the pause")
	j.RungAt = t0.Add(-30 * time.Second)
	_, act = Ladder(LadderState{}, ladderIn(t0, 3, 0, j))
	require.NotNil(t, act)
	assert.Equal(t, ActRequeue, act.Kind)

	j.NoRequeue = true
	_, act = Ladder(LadderState{}, ladderIn(t0, 3, 0, j, ljob(2, time.Hour)))
	require.NotNil(t, act)
	assert.Equal(t, Action{Kind: ActSqueeze, Job: 2, MemoryHighKB: gb(3)}, *act, "a paused --no-requeue job has nothing left: the next one gives")
}

func TestLadder_SqueezeHasAFloor(t *testing.T) {
	j := ljob(1, time.Minute)
	j.WorkingKB = 0
	_, act := Ladder(LadderState{}, ladderIn(t0, 3, 0, j))
	require.NotNil(t, act)
	assert.Equal(t, int64(minHighKB), act.MemoryHighKB, "0 would mean no limit")
}

func TestLadder_Recovery(t *testing.T) {
	old, young := ljob(1, time.Hour), ljob(2, time.Minute)
	old.Rung, young.Rung = RungPaused, RungSqueezed
	st, act := Ladder(LadderState{}, ladderIn(t0, 6, 1.9, old, young))
	assert.Nil(t, act)
	assert.Equal(t, t0, st.CalmSince)
	st, act = Ladder(st, ladderIn(t0.Add(29*time.Second), 6, 1.9, old, young))
	assert.Nil(t, act, "calm for 30 s first")
	st, act = Ladder(st, ladderIn(t0.Add(30*time.Second), 6, 1.9, old, young))
	require.NotNil(t, act)
	assert.Equal(t, Action{Kind: ActRestore, Job: 1}, *act, "oldest first")
	old.Rung = ""
	_, act2 := Ladder(st, ladderIn(t0.Add(44*time.Second), 6, 1.9, old, young))
	assert.Nil(t, act2, "one every 15 s")
	_, act2 = Ladder(st, ladderIn(t0.Add(45*time.Second), 6, 1.9, old, young))
	require.NotNil(t, act2)
	assert.Equal(t, JobID(2), act2.Job)

	st, act = Ladder(LadderState{CalmSince: t0}, ladderIn(t0.Add(time.Minute), 5, 1.9, old, young))
	assert.Nil(t, act, "headroom under reserve + 2 GB is not calm")
	assert.True(t, st.CalmSince.IsZero())
	st, _ = Ladder(LadderState{CalmSince: t0}, ladderIn(t0.Add(time.Minute), 6, 2, old, young))
	assert.True(t, st.CalmSince.IsZero(), "pressure at 2% is not calm")
	st, _ = Ladder(LadderState{CalmSince: t0}, ladderIn(t0.Add(time.Minute), 3, 0, old, young))
	assert.True(t, st.CalmSince.IsZero(), "short resets calm")
}

func TestRung_Text(t *testing.T) {
	assert.Equal(t, "squeezed", RungSqueezed.Text())
	assert.Equal(t, "paused (memory)", RungPaused.Text())
	assert.Equal(t, "", Rung("").Text())
}
```

- [ ] **Run** → FAIL.
- [ ] **Implement** `sched/ladder.go`:

```go
package sched

import (
	"cmp"
	"slices"
	"time"
)

// Rung is where the ladder has a running job (cgroup spec §8.2): "" for
// none.
type Rung string

// The ladder's rungs on a running job; a requeue ends the attempt.
const (
	RungSqueezed Rung = "squeezed" // its MemoryHigh is its working set
	RungPaused   Rung = "paused"   // frozen
)

// Text is r as the work view and loom job list show it.
func (r Rung) Text() string {
	if r == RungPaused {
		return "paused (memory)"
	}
	return string(r)
}

// The ladder's thresholds and timings (cgroup spec §8).
const (
	shortPressure  = 10.0            // the slice's memory pressure (some avg10, %) that makes memory short
	calmPressure   = 2.0             // pressure under which, with headroom, memory is calm
	calmMarginKB   = 2 * kbPerGB     // headroom above the reserve that is calm
	minHighKB      = 64 << 10        // a squeeze's least MemoryHigh: 0 would be no limit
	squeezeToPause = 15 * time.Second
	pauseToRequeue = 30 * time.Second
	calmFor        = 30 * time.Second
	restoreEvery   = 15 * time.Second
	// MaxRequeues is how many times a job is requeued before it runs at its
	// full claim and is never laddered again.
	MaxRequeues = 2
)

// LadderJob is a running contained job as the ladder sees it.
type LadderJob struct {
	ID        JobID
	Started   time.Time
	WorkingKB int64
	Rung      Rung
	RungAt    time.Time
	NoRequeue bool
	Exclusive bool
	Starved   bool // the starvation guard started it
	Requeues  int
}

// LadderInput is one pass's reading (cgroup spec §8.1).
type LadderInput struct {
	Now         time.Time
	HeadroomKB  int64   // the host's MemAvailable
	ReserveKB   int64   // interactive_reserve_gb
	MemPressure float64 // the jobs slice's memory.pressure "some" avg10
	Jobs        []LadderJob
}

// LadderState is what the ladder keeps between passes.
type LadderState struct {
	CalmSince   time.Time // zero unless memory is calm
	LastRestore time.Time
}

// ActionKind is what an Action does.
type ActionKind string

// The ladder's actions.
const (
	ActSqueeze ActionKind = "squeeze"
	ActPause   ActionKind = "pause"
	ActRequeue ActionKind = "requeue"
	ActRestore ActionKind = "restore" // thaw if paused, MemoryHigh back to no limit
)

// Action is one step the ladder asks for.
type Action struct {
	Kind         ActionKind
	Job          JobID
	MemoryHighKB int64 // ActSqueeze's
}

// Short reports whether memory is short: headroom below the reserve, or the
// slice's memory pressure at 10% or more.
func (in LadderInput) Short() bool {
	return in.HeadroomKB < in.ReserveKB || in.MemPressure >= shortPressure
}

// calm reports whether memory is calm: headroom at least 2 GB above the
// reserve and pressure under 2%.
func (in LadderInput) calm() bool {
	return in.HeadroomKB >= in.ReserveKB+calmMarginKB && in.MemPressure < calmPressure
}

// Ladder is one pass of the ladder (cgroup spec §8.2–§8.3), a pure function:
// at most one action. While memory is short it climbs a rung on the victim;
// once it has been calm for calmFor it restores the oldest laddered job, one
// every restoreEvery; in between it holds.
func Ladder(st LadderState, in LadderInput) (LadderState, *Action) {
	switch {
	case in.Short():
		st.CalmSince = time.Time{}
		return st, climb(in)
	case in.calm():
		if st.CalmSince.IsZero() {
			st.CalmSince = in.Now
		}
		if in.Now.Sub(st.CalmSince) < calmFor || in.Now.Sub(st.LastRestore) < restoreEvery {
			return st, nil
		}
		if j := oldestLaddered(in.Jobs); j != nil {
			st.LastRestore = in.Now
			return st, &Action{Kind: ActRestore, Job: j.ID}
		}
		return st, nil
	}
	st.CalmSince = time.Time{}
	return st, nil
}

// climb is the next rung on the victim, nil while its rung's wait runs.
func climb(in LadderInput) *Action {
	v := victim(in.Jobs)
	switch {
	case v == nil:
		return nil
	case v.Rung == "":
		return &Action{Kind: ActSqueeze, Job: v.ID, MemoryHighKB: max(v.WorkingKB, minHighKB)}
	case v.Rung == RungSqueezed && in.Now.Sub(v.RungAt) >= squeezeToPause:
		return &Action{Kind: ActPause, Job: v.ID}
	case v.Rung == RungPaused && in.Now.Sub(v.RungAt) >= pauseToRequeue:
		return &Action{Kind: ActRequeue, Job: v.ID}
	}
	return nil
}

// victim is the job the ladder acts on: of the jobs with something left to
// give (not exclusive, not requeued MaxRequeues times, not paused with
// NoRequeue), one the starvation guard didn't start before one it did, then
// the most recently started; nil for none.
func victim(jobs []LadderJob) *LadderJob {
	var cands []*LadderJob
	for i := range jobs {
		j := &jobs[i]
		if j.Exclusive || j.Requeues >= MaxRequeues || (j.Rung == RungPaused && j.NoRequeue) {
			continue
		}
		cands = append(cands, j)
	}
	if len(cands) == 0 {
		return nil
	}
	return slices.MinFunc(cands, func(a, b *LadderJob) int {
		return cmp.Or(compareBool(a.Starved, b.Starved), b.Started.Compare(a.Started), cmp.Compare(b.ID, a.ID))
	})
}

// oldestLaddered is the laddered job that started first, nil for none.
func oldestLaddered(jobs []LadderJob) *LadderJob {
	var out *LadderJob
	for i := range jobs {
		j := &jobs[i]
		if j.Rung != "" && (out == nil || j.Started.Before(out.Started) || (j.Started.Equal(out.Started) && j.ID < out.ID)) {
			out = j
		}
	}
	return out
}

// compareBool orders false before true.
func compareBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case !a:
		return -1
	}
	return 1
}
```

- [ ] **Run** `go test ./sched/ -run 'TestLadder|TestRung'` → PASS.

### Step 4.2: Records, runs, submissions and the attempt directory

**Files:** `sched/job.go`, `sched/belief.go`, `sched/belief_test.go`, `sched/files.go`, `sched/files_test.go`

- [ ] **Write the failing tests:** in `sched/belief_test.go`, `TestRunOf_ALadderedRunTeachesFloorsOnly` (a done record with `Laddered` gives a run with `Laddered`, `FullPeak()` false, `TeachesDuration()` false and no cores taught; the same record without it teaches all three); in `sched/files_test.go`:

```go
func TestArchiveAttempt(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{LogFile, StartedFile, ExitFile, SpecFile} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600))
	}
	require.NoError(t, ArchiveAttempt(dir, 0))
	for _, name := range []string{LogFile, StartedFile, ExitFile} {
		assert.NoFileExists(t, filepath.Join(dir, name))
		data, err := os.ReadFile(filepath.Join(dir, "attempt-1", name))
		require.NoError(t, err)
		assert.Equal(t, name, string(data))
	}
	assert.FileExists(t, filepath.Join(dir, SpecFile), "the spec stays for the next attempt")
	info, err := os.Stat(filepath.Join(dir, "attempt-1"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())

	require.NoError(t, ArchiveAttempt(dir, 1), "missing files are skipped")
	assert.DirExists(t, filepath.Join(dir, "attempt-2"))
	assert.Error(t, ArchiveAttempt(dir, 1), "an attempt is archived once")
}
```

- [ ] **Run** → FAIL.
- [ ] **Implement.** `sched.Record` gains, after `Attempt`:

```go
	// The ladder (cgroup spec §8): the rung it is on and since when, whether
	// it was laddered in this attempt, how often it was requeued and why last,
	// a requeue asked for (the kill is out), whether it may be requeued at all
	// (--no-requeue), and whether the starvation guard started it.
	Rung        Rung      `json:"rung,omitempty"`
	RungAt      time.Time `json:"rung_at,omitzero"`
	Laddered    bool      `json:"laddered,omitempty"`
	Requeues    int       `json:"requeues,omitempty"`
	LastRequeue string    `json:"last_requeue,omitempty"`
	Requeue     string    `json:"requeue,omitempty"`
	NoRequeue   bool      `json:"no_requeue,omitempty"`
	Starved     bool      `json:"starved,omitempty"`
```

`Submit` gains `NoRequeue bool \`json:"no_requeue,omitempty"\``. `Run` gains `Laddered bool \`json:"laddered,omitempty"\` // squeezed or paused: its timing is distorted, so it teaches floors only`; `RunOf` copies it; `FullPeak` adds `&& !r.Laddered`, `teachesCores` and `TeachesDuration` add `&& !r.Laddered`. `sched/files.go`:

```go
// ArchiveAttempt moves an attempt's output.log, started.json and exit.json
// into jobDir/attempt-<attempt+1> (mode 0700), so the job's next attempt
// runs in a clean directory (cgroup spec §8.4); a file that isn't there is
// skipped. The directory must not exist yet.
func ArchiveAttempt(jobDir string, attempt int) error {
	dst := filepath.Join(jobDir, fmt.Sprintf("attempt-%d", attempt+1))
	if err := os.Mkdir(dst, 0o700); err != nil {
		return fmt.Errorf("make %s: %w", dst, err)
	}
	for _, name := range []string{LogFile, StartedFile, ExitFile} {
		err := os.Rename(filepath.Join(jobDir, name), filepath.Join(dst, name))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("move %s: %w", name, err)
		}
	}
	return nil
}
```

- [ ] **Run** `go test ./sched/` → PASS.

### Step 4.3: The model runs the ladder

**Files:** `core/queue.go`, `core/queue_ladder.go` (new), `core/queue_watch.go`, `core/queue_admit.go`, `core/queue_greedy.go`, `core/queue_ops.go`, `core/queue_notice.go`, `core/model.go`, `core/queue_ladder_test.go` (new)

- [ ] **Queue state:** `ladder sched.LadderState`, `laddering bool` (an action in flight), `ladderErr string` (the last failed call, shown in the view until a call succeeds), `sliceHighKB int64` and `sliceSetting bool` (the slice's `MemoryHigh` as last set, and a set in flight), `oomAt time.Time` (when the slice's `oom_kill` last rose).
- [ ] **`core/queue_ladder.go`:**

```go
package core

import (
	"context"
	"fmt"
	"time"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/sched"
)

// sliceHighStep is how far the budget moves before the slice's MemoryHigh
// follows it (cgroup spec §8.1).
const sliceHighStep = 256 << 10 // KB

// ladderDone is a ladder action's result.
type ladderDone struct {
	act sched.Action
	at  time.Time
	err error
}

// sliceHighSet is a set of the slice's MemoryHigh.
type sliceHighSet struct {
	kb  int64
	err error
}

// stepLadder runs one pass of the ladder after a sample (cgroup spec §8),
// in greedy mode only and one action at a time. A cancelled job that is
// paused is restored first, so it can act on its SIGTERM.
func (m *Model) stepLadder(now time.Time) {
	q := m.queue
	if q.containWhy(q.policy) != "" || q.availKB <= 0 || q.laddering {
		return
	}
	in := sched.LadderInput{Now: now, HeadroomKB: q.availKB, ReserveKB: int64(q.policy.ReserveGB) * kbPerGB}
	if q.slice != nil {
		in.MemPressure = q.slice.MemPressure
	}
	for i := range q.store.Jobs {
		rec := &q.store.Jobs[i]
		if rec.State != sched.Running || rec.Cgroup == "" || rec.Requeue != "" {
			continue
		}
		if rec.CancelReason != "" {
			if rec.Rung != "" {
				m.applyLadder(sched.Action{Kind: sched.ActRestore, Job: rec.ID}, now)
				return
			}
			continue
		}
		in.Jobs = append(in.Jobs, sched.LadderJob{
			ID: rec.ID, Started: rec.Started, WorkingKB: q.usage[rec.ID].RSSKB, Rung: rec.Rung, RungAt: rec.RungAt,
			NoRequeue: rec.NoRequeue, Exclusive: rec.Claim.Exclusive, Starved: rec.Starved, Requeues: rec.Requeues,
		})
	}
	var act *sched.Action
	q.ladder, act = sched.Ladder(q.ladder, in)
	if act != nil {
		m.applyLadder(*act, now)
		return
	}
	m.maybeSetSliceHigh()
}

// applyLadder carries out act from a job: a squeeze, pause or restore over
// D-Bus, a requeue by writing the scope's cgroup.kill once the record asks
// for it (settleGone then requeues the job). The rung changes only when the
// call returns (deliverLadderDone).
func (m *Model) applyLadder(act sched.Action, now time.Time) {
	q := m.queue
	i := q.index(act.Job)
	if i < 0 {
		return
	}
	rec := q.store.Jobs[i]
	c, root := q.confiner, q.cgroupRoot
	if act.Kind == sched.ActRequeue {
		q.store.Jobs[i].Requeue = fmt.Sprintf("memory ran short (%.1f GB free)", float64(q.availKB)/kbPerGB)
		q.moved()
		m.saveQueueQuiet("ladder")
	}
	q.laddering = true
	m.spawnBackground(func() any {
		ctx, cancel := context.WithTimeout(context.Background(), confineTimeout)
		defer cancel()
		var err error
		switch act.Kind {
		case sched.ActSqueeze:
			err = c.SetMemoryHigh(ctx, rec.Unit, uint64(act.MemoryHighKB)<<10)
		case sched.ActPause:
			err = c.Freeze(ctx, rec.Unit)
		case sched.ActRestore:
			if rec.Rung == sched.RungPaused {
				err = c.Thaw(ctx, rec.Unit)
			}
			if err == nil {
				err = c.SetMemoryHigh(ctx, rec.Unit, 0)
			}
		case sched.ActRequeue:
			err = sched.CgroupKill(root, rec.Cgroup)
		}
		return ladderDone{act: act, at: now, err: err}
	})
}

// deliverLadderDone records a ladder action that returned: the job's rung,
// or, for a failed call, nothing but the error, retried at the next pass.
func (m *Model) deliverLadderDone(r ladderDone) {
	q := m.queue
	q.laddering = false
	i := q.index(r.act.Job)
	if i < 0 || q.store.Jobs[i].State != sched.Running {
		return
	}
	rec := &q.store.Jobs[i]
	if r.err != nil {
		log.For("core").Warn("queue.ladder_failed", "job", int64(rec.ID), "action", string(r.act.Kind), "err", r.err)
		q.ladderErr = fmt.Sprintf("%s job %d: %v", r.act.Kind, rec.ID, r.err)
		if r.act.Kind == sched.ActRequeue {
			rec.Requeue = "" // the kill did not go out: the ladder tries again
		}
		q.moved()
		return
	}
	q.ladderErr = ""
	switch r.act.Kind {
	case sched.ActSqueeze:
		rec.Rung, rec.RungAt, rec.Laddered = sched.RungSqueezed, r.at, true
	case sched.ActPause:
		rec.Rung, rec.RungAt = sched.RungPaused, r.at
	case sched.ActRestore:
		rec.Rung, rec.RungAt = "", time.Time{}
	}
	log.For("core").Info("queue.ladder", "job", int64(rec.ID), "action", string(r.act.Kind),
		"headroom_kb", q.availKB, "working_kb", q.usage[rec.ID].RSSKB)
	q.moved()
	m.saveQueueQuiet("ladder")
}

// maybeSetSliceHigh keeps the slice's MemoryHigh at the budget (rung 0,
// cgroup spec §8.1), at least 1 GB, once it has moved by sliceHighStep.
func (m *Model) maybeSetSliceHigh() {
	q := m.queue
	if q.sliceSetting || q.confiner == nil {
		return
	}
	var used int64
	for i := range q.store.Jobs {
		if q.store.Jobs[i].State == sched.Running {
			used += q.usage[q.store.Jobs[i].ID].RSSKB
		}
	}
	kb := max(sched.Budget(used, q.availKB, q.policy), kbPerGB)
	if d := kb - q.sliceHighKB; d < sliceHighStep && -d < sliceHighStep {
		return
	}
	c, slice := q.confiner, sched.SliceName(q.dir)
	q.sliceSetting = true
	m.spawnBackground(func() any {
		ctx, cancel := context.WithTimeout(context.Background(), confineTimeout)
		defer cancel()
		return sliceHighSet{kb: kb, err: c.SetMemoryHigh(ctx, slice, uint64(kb)<<10)}
	})
}

// deliverSliceHighSet records the slice's MemoryHigh, or logs why it failed.
func (m *Model) deliverSliceHighSet(r sliceHighSet) {
	q := m.queue
	q.sliceSetting = false
	if r.err != nil {
		log.For("core").Warn("queue.slice_high_failed", "err", r.err)
		return
	}
	q.sliceHighKB = r.kb
}

// ladderHoldoff is why nothing is admitted while the ladder holds a job
// (cgroup spec §8.3), "" when it holds none: the latest laddered job.
func (m *Model) ladderHoldoff() string {
	q := m.queue
	var last *sched.Record
	for i := range q.store.Jobs {
		rec := &q.store.Jobs[i]
		if rec.State == sched.Running && rec.Rung != "" && (last == nil || rec.RungAt.After(last.RungAt)) {
			last = rec
		}
	}
	if last == nil {
		return ""
	}
	return fmt.Sprintf("memory is short (job %d %s)", last.ID, last.Rung)
}
```

Add `case ladderDone:` and `case sliceHighSet:` to `core/model.go`'s switch. Call `m.stepLadder(time.Now())` at the end of `deliverQueuePass`, after the greedy admission of Step 3.5. In `greedyInputs`, set `g.Holdoff = m.ladderHoldoff()`; in `predictNew` and `predictRunning`, a record with `Requeues >= sched.MaxRequeues` skips the curves and counts flat at its claim (§8.4); `predictRunning` also counts a laddered job (`Rung != ""`) flat at its claim. In `admitJobs`, `startJob` sets `rec.Starved = id == dec.Starved` (pass it in).
- [ ] **Reconcile from the cgroup** (amendment 17): in `applyUsage`, for a job with `ob.cg` while no ladder action is in flight (`!q.laddering`), the cgroup decides the rung (frozen is paused, a `memory.high` is squeezed, neither is none):

```go
		if cg := ob.cg; cg != nil && !q.laddering {
			next := sched.Rung("")
			switch {
			case cg.Frozen:
				next = sched.RungPaused
			case cg.HighKB > 0:
				next = sched.RungSqueezed
			}
			if next != rec.Rung {
				rec.Rung, rec.RungAt = next, time.Time{}
				if next != "" {
					rec.RungAt, rec.Laddered = r.at, true
				}
				q.moved()
			}
		}
```

- [ ] **The kernel's OOM kill:** in `deliverQueuePass`, before `q.slice = r.slice`: `if r.slice != nil && q.slice != nil && r.slice.OOMKills > q.slice.OOMKills { q.oomAt = r.at; log.For("core").Warn("queue.oom_kill", "count", r.slice.OOMKills) }`.
- [ ] **Requeue at settle** (`settleGone`): after the state is worked out and before `finishJob`:

```go
	if why := m.requeueWhy(*rec, now); why != "" {
		if !rec.NoRequeue && rec.Requeues < sched.MaxRequeues {
			m.requeueJob(i, why, now)
			return
		}
		if state == sched.Failed {
			rec.Failure = "killed: " + why
		}
	}
```

with

```go
// requeueWhy is why job rec, whose shim is gone, ran out of memory: the
// ladder's requeue, or a kernel OOM kill (a SIGKILL nobody asked for while
// the slice's oom_kill rose, within two passes); "" for neither.
func (m *Model) requeueWhy(rec sched.Record, now time.Time) string {
	switch {
	case rec.Requeue != "":
		return rec.Requeue
	case rec.CancelReason == "" && rec.Exit != nil && rec.Exit.Signal == "SIGKILL" && !m.queue.oomAt.IsZero() && now.Sub(m.queue.oomAt) <= 2*queueInterval:
		return "the kernel killed it: out of memory"
	}
	return ""
}

// requeueJob puts job i, killed for memory, back in the queue as its next
// attempt (cgroup spec §8.4): its run is a floor, its files go to
// attempt-<n>, and it keeps its ID, owner, order and place; its owner is told.
func (m *Model) requeueJob(i int, why string, now time.Time) {
	q := m.queue
	rec := &q.store.Jobs[i]
	if err := sched.ArchiveAttempt(q.jobDir(rec.ID), rec.Attempt); err != nil {
		log.For("core").Warn("queue.requeue_failed", "job", int64(rec.ID), "err", err)
		rec.Failure = "killed: " + why + "; its requeue failed: " + err.Error()
		m.finishJob(i, sched.Failed, now)
		return
	}
	floor := *rec
	floor.State, floor.Ended = sched.Cancelled, now
	m.forgetRunning(rec.ID)
	m.recordRun(floor, nil)
	rec.Claim.CPUs -= rec.Spare.CPUs
	rec.Claim.MemoryGB -= rec.Spare.MemoryGB
	attempt, requeues := rec.Attempt+1, rec.Requeues+1
	*rec = sched.Record{
		ID: rec.ID, Order: rec.Order, Owner: rec.Owner, Agent: rec.Agent, Description: rec.Description,
		Argv: rec.Argv, Cwd: rec.Cwd, Claim: rec.Claim, Notify: rec.Notify, Tail: rec.Tail, State: sched.Queued,
		Submitted: rec.Submitted, Asked: rec.Asked, Signature: rec.Signature, Checkout: rec.Checkout,
		Unsized: rec.Unsized, Sources: rec.Sources, Learned: rec.Learned, After: rec.After, Ready: rec.Ready,
		Attempt: attempt, Requeues: requeues, LastRequeue: why, NoRequeue: rec.NoRequeue, PeakRSSKB: rec.PeakRSSKB,
	}
	log.For("core").Warn("queue.requeued", "job", int64(rec.ID), "attempt", attempt+1, "why", why)
	q.moved()
	m.sendRequeueNotice(*rec, why)
}
```

Read `sched.Record`'s fields while writing the reset: every field that describes the submission or its sizing is kept, every field of a run is cleared. `forgetRunning` is `settledJob`'s map deletes, moved into a helper `settledJob` calls too. `settleGone`'s caller treats a requeue as settled (so `afterSettle` saves and admits). The floor run raises the next sizing through `Size`, which `resizeQueued` applies.
- [ ] **The requeue message** (`core/queue_notice.go`): `sendRequeueNotice(rec sched.Record, why string)` sends `[loom] job <id> "<description>" was stopped and requeued: <why>; it keeps its place in line` to the owner (or the subagent) through the same delivery `buildNoticeJob`/`deliverJobNotice` use, without the log's tail and without recording a fate (`noteNotice` is not called), so the end message still comes (amendment 10). Read `deliverJobNotice` first and add a flag to the job's result that skips `noteNotice`; a job with `Notify` off or no owner sends none.
- [ ] **Submissions** (`submitJob`): `rec.NoRequeue = s.NoRequeue`.
- [ ] **Views:** `JobView` gains `Rung sched.Rung`, `Requeues int`, `LastRequeue string`, `NoRequeue bool`; `QueueView` gains `LadderErr string`.
- [ ] **Core tests** (`core/queue_ladder_test.go`), with real shims, a fake confiner, a temp cgroup root and `readMemAvailable` injected; each test sets a record's `Cgroup` under the root and writes its files, delivers passes built by `runQueuePass(m.queuePassFor(false))`, and moves time by rewinding `RungAt`/`CalmSince` rather than sleeping:
  1. `TestQueueLadder_SqueezesTheMostRecentlyStartedWhenShort`: two contained jobs, MemAvailable under the reserve: the fake saw `SetMemoryHigh <younger unit> <its working set × 1024>`; once delivered its record says `squeezed`; a queued job waits `memory is short (job N squeezed)`.
  2. `TestQueueLadder_PausesThenRequeues`: rewind `RungAt` 16 s → `Freeze`; 31 s → the record's `Requeue` is set and the temp `cgroup.kill` holds `1`; kill the shim's group (`sched.SignalGroup(pid, SIGKILL)`, standing in for the kernel), deliver a pass: the record is `queued`, `Attempt` 1, `Requeues` 1, `attempt-1/output.log` exists, a floor run was recorded, and once memory is calm and no job is laddered it starts again with a spec naming `ScopeName(dir, id, 1)`.
  3. `TestQueueLadder_NoRequeueStopsAtPause`: a `--no-requeue` job paused 60 s is never killed.
  4. `TestQueueLadder_RecoveryRestoresOldestFirst`: two laddered jobs, calm for 30 s: `Thaw` and `SetMemoryHigh … 0` go to the older first.
  5. `TestQueueLadder_AKernelOOMKillRequeues`: the slice's `oom_kill` rises in one pass, then the job's shim dies by SIGKILL: requeued with `the kernel killed it: out of memory`; with `NoRequeue`, failed with `Failure` saying so.
  6. `TestQueueLadder_ReconcilesFromTheCgroup`: `frozen 1` in `cgroup.events` makes the record `paused`; `memory.high` back to `max` with no freeze clears it.
  7. `TestQueueLadder_ACancelThawsAPausedJob`: cancelling a paused job sends `Thaw` and `SetMemoryHigh … 0` before anything else.
  8. `TestQueueLadder_FailedCallsChangeNoRung`: the fake fails `Freeze`: the rung stays `squeezed`, `LadderErr` says why, and the next pass tries again.
  9. `TestQueueLadder_TheSliceFollowsTheBudget`: the fake saw `SetMemoryHigh <slice> <budget × 1024>`, and not again until the budget moves 256 MiB.
- [ ] **Run** `go test ./core/ -run 'TestQueueLadder|TestQueueGreedy|TestQueueContain|TestJobQueue'` → PASS.

### Step 4.4: Clients

**Files:** `internal/cli/job.go`, `internal/cli/job_follow.go`, `internal/cli/job_sizing.go`, `ui/work.go`, their tests

- [ ] **`--no-requeue`** on `submit` and `run` (`job.go`, beside `--no-notify`): "Never requeue the job when memory runs short; pause it instead". It sets `Submit.NoRequeue`.
- [ ] **The follower** (`job_follow.go`): `jobFollower` gains `attempt int`, `attemptSeen bool` and `rung sched.Rung`, and two pure steps, each tested on constructed views:

```go
// noteAttempt notices a requeue (cgroup spec §8.4): a view of a later
// attempt than the last resets the log's offset and the start report, and
// returns the line to say; "" otherwise.
func (f *jobFollower) noteAttempt(v core.JobView) string {
	if !f.attemptSeen {
		f.attempt, f.attemptSeen = v.Attempt, true
		return ""
	}
	if v.Attempt == f.attempt {
		return ""
	}
	f.attempt, f.off, f.started, f.queued, f.last = v.Attempt, 0, false, true, ""
	return fmt.Sprintf("job %d requeued: %s; following its next attempt", v.ID, v.LastRequeue)
}

// noteRung is the line for a running job's rung change, "" for none.
func (f *jobFollower) noteRung(v core.JobView) string {
	if v.State != sched.Running || v.Rung == f.rung {
		return ""
	}
	prev := f.rung
	f.rung = v.Rung
	switch {
	case v.Rung == sched.RungSqueezed && prev == "":
		return fmt.Sprintf("job %d squeezed: memory is short", v.ID)
	case v.Rung == sched.RungPaused:
		return fmt.Sprintf("job %d paused: memory is short", v.ID)
	case v.Rung == "":
		return fmt.Sprintf("job %d resumed", v.ID)
	}
	return ""
}
```

In `run`, after the view is read and before `copyLog`: say `noteAttempt`'s line (unless quiet); in `report`'s running case, say `noteRung`'s. Tests `TestJobFollower_NoteAttempt` (first view sets, same attempt says nothing, a later one resets `off` to 0 and says the line) and `TestJobFollower_NoteRung`.
- [ ] **`loom job list`** (`job_sizing.go`, after the hold text) and the **work view** (`ui/work.go`, after the hold text): a running job's `Rung.Text()`; the work view's header shows `LadderErr` dim while it is set. Extend each one's test.

### Step 4.5: Docs, gates, commit

- [ ] `USAGE.md`: the ladder (rung 0, squeeze, pause, requeue, recovery, the hold on admissions), `--no-requeue`, what `run`/`wait` print, the requeue message, `attempt-<n>/` in the job directory.
- [ ] `sched/README.md`: the ladder, `ArchiveAttempt`, the record's ladder fields, the job directory table's `attempt-<n>/`.
- [ ] `sched/CLAUDE.md`: **Keep `Ladder` pure, never let a squeeze set a MemoryHigh of 0 (`minHighKB`), and never ladder an exclusive job, one requeued `MaxRequeues` times, or a paused `--no-requeue` one further.** 0 is no limit to systemd, and a requeue of a job that asked for none loses its work. **Enforced** by `TestLadder_SqueezeHasAFloor`, `TestLadder_VictimOrder` and `TestLadder_SqueezeThenPauseThenRequeue`.
- [ ] `docs/claude/the-work-queue.md`, a rule: **Change a job's rung only when its D-Bus call returned (`deliverLadderDone`), admit nothing while any job is laddered (`ladderHoldoff`), and requeue only through `requeueJob`, which keeps the ID and archives the attempt.** *Silent:* a rung that says paused over a running job, a squeeze loop, or a requeued job's two logs mixed in one.
- [ ] `docs/ARCHITECTURE.md`: the ladder in one line beside the scopes.
- [ ] The spec: execution notes (§16) — what ran, the gates and their job numbers, anything that changed while building.
- [ ] Gates: vet, full, race, e2e, the Windows and darwin builds, the live contract test once more, `gofmt -l`, `go run ./tools/claudemd`.
- [ ] Commit: `feat(sched,core,cli,ui): squeeze, pause or requeue a job when memory runs short`.
