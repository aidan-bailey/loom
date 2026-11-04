# Work Scheduler Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Spec:** `docs/superpowers/specs/2026-11-04-work-scheduler-design.md`

**Goal:** A per-host work queue in the loom daemon. Sessions submit builds, tests and benchmarks with `loom job`. The daemon admits each one against per-host caps, runs it in a detached shim that survives daemon restarts, and messages the owning session's Claude when it ends. A TUI work view shows the schedule.

**Architecture:** A new package, `sched`, holds the scheduler's logic and its runtime: the types, caps resolution, first-fit admission, the `jobs.json` store, the per-job files, the `loom job-shim` process, and the lock probe, group signals and usage sampler. The model (`core`) owns the queue. It persists it under `<globalDir>/jobs/`, admits and starts jobs, watches them on a gated job, adopts them at boot, cancels a session's jobs when the session is paused or killed, and sends notices through the loom mod's outbox (`OriginJob`). It publishes `QueueView` with a `QueueChanged` state event. `internal/cli` adds `loom job`, the TUI adds a fourth view mode, and loom's Claude context tells agents to use it.

**Tech Stack:** Go 1.25 (`os/exec` with `Setsid`, `syscall.Flock`, `syscall.Setpriority`, `ioprio_set` on Linux), cobra, Bubble Tea and lipgloss, gopher-lua, testify.

---

## Execution

- **Where.** This worktree, on branch `aidanb/work-scheduler`, which loom manages: never switch branches. Every path is relative to the repo root. Commit by pathspec, `git add -- <files>` then `git commit -m "<message>" -- <files>`, never `-a`, `git add -A` or `.`. Use Conventional Commit types scoped by package (`sched`, `config`, `core`, `rpc`, `cli`, `app`, `ui`, `script`, `docs`; several joined with commas).
- **Order.** Stories 1 to 7, one at a time. The profile makes every Go build exclusive, and each story builds on the ones before it.
- **Gates.** The profile's story gates run after each story: gofmt, `go vet ./...` and `go test ./...`. The Windows cross-build is a final gate. Stories 2 and 3 add platform code and cross-build Windows and macOS in their Verify lines. Story 5 adds an e2e test and runs it in its Verify line; it needs tmux.
- **Generated files.** Story 4 regenerates `core/rpc/methods_gen.go` (`go generate ./core/rpc`) and `docs/specs/protocol.md` (`CGO_ENABLED=0 go test ./core/rpc -run TestProtocolReference -update`). Never edit either by hand. `rpc.Protocol` stays 2: adding methods and an event is compatible (`docs/claude/adding-a-core-method.md` step 9, `docs/claude/adding-a-core-event.md` step 8).
- **CLAUDE.md budgets.**
  - The headroom on 2026-11-04: `core/CLAUDE.md` 76 bytes free (19924/20000), `app/CLAUDE.md` 459, the root `CLAUDE.md` 499. No story adds a rule to those three.
  - Story 1 creates `sched/CLAUDE.md`, since the coverage lint fails a Go package without one. Stories 1 and 2 write the package's rules there, and Story 7 writes the guide `docs/claude/the-work-queue.md` for the model's side.
  - Check with `CGO_ENABLED=0 go run ./tools/claudemd -v`.
- **Tests.** No test touches the real `~/.loom` or the user's tmux server: every package whose tests reach `config` calls `testenv.IsolateLoomDirs` from its `TestMain`. Tests that start real shims (`sched`, `core`, `internal/cli`) run the test binary as the shim. Their `TestMain` calls `sched.MaybeRunTestShim()` first, and the runner is `sched.TestRunner()`. Real jobs run short commands (`sh -c`, `sleep`), and every test leaves no process group behind. Fixtures go through the production path: a booted model, a real shim, `jobs.json` written by `sched.SaveStore`. No seam injects queue records: a test seeds `<globalDir>/jobs/` with `sched.SaveStore` and `sched.WriteSpec` before the model boots. A package whose `TestMain` doesn't run the test shim (`core/rpc`, `app`, `ui`) uses finished records and queued ones that can't start (admission paused, or a claim over a cap), or builds `core.QueueView` values directly where no model is involved.
- **Running loom.** Never run loom itself (`./loom`, `go run .`) or `loom serve stop` in a loom pane. Manual checks use the `loom-dev` skill (`go run ./tools/loomdev`).

## File structure

| Path | Responsibility | Story |
|---|---|---|
| `config/settings.go`, `config/config.go`, `config/settings_test.go`, `config/README.md` | `SchedulerSettings` and `ClaimSettings`; `Settings.Scheduler`, its deep copy in `Clone`, and the getters and setter | 1 |
| `sched/job.go`, `sched/job_test.go` | `JobID`, `State`, `Claim`, `Caps`, `Need`, `Host`, `Policy`, `Record`, `Notice`, `Submit`; `Submit.Validate`, `CheckClaim` | 1 |
| `sched/policy.go`, `sched/policy_test.go` | `Resolve`: the host defaults, the bounds and their errors | 1 |
| `sched/admit.go`, `sched/admit_test.go` | `Admit`: first fit, the starvation guard, over-cap skipping, why each job waits | 1 |
| `sched/hints.go`, `sched/hints_test.go` | `Hints`: the parallelism variables and `LOOM_JOB` | 1 |
| `sched/store.go`, `sched/files.go`, `sched/store_test.go`, `sched/files_test.go` | `jobs.json` (`LoadStore`, `SaveStore`, `ErrNewerStore`); the per-job files and their codecs; `JobDir`, `RemoveJobDir` | 1 |
| `sched/testmain_test.go`, `sched/CLAUDE.md`, `sched/README.md`, `sched/doc.go` | The package's test isolation, rules, facts and doc | 1 (Story 2 adds to the first three) |
| `sched/shim.go`, `sched/shim_unix.go`, `sched/shim_windows.go`, `sched/shim_test.go` | `RunShim`, `Runner`, `NewRunner`, `Runner.Start`, `Probe`, `SignalGroup`, `ErrUnsupported` | 2 |
| `sched/prio_linux.go`, `sched/prio_other.go` | The shim's nice and Linux ioprio | 2 |
| `sched/sample.go`, `sched/sample_test.go` | `Sample`, `Snapshot`, `Proc`, `ParsePS`, `Snapshot.Group` | 2 |
| `sched/host.go`, `sched/host_linux.go`, `sched/host_darwin.go`, `sched/host_other.go`, `sched/host_test.go` | `DetectHost` | 2 |
| `sched/testshim.go` | `ShimTestEnv`, `MaybeRunTestShim`, `TestRunner` | 2 |
| `job_shim.go`, `job_shim_test.go`, `main.go` | The hidden `loom job-shim` command | 2 |
| `flake.nix` | `procps` in the Nix check inputs and the wrapper's `PATH`, for `ps` | 2 |
| `core/queue.go`, `core/queue_admit.go`, `core/queue_watch.go`, `core/queue_ops.go`, `core/queue_notice.go` | The queue's state, load and save, boot adoption, retention, admission passes, starting, watching and settling, cancel, the internal operations, notices | 3 |
| `core/mod.go`, `core/gate.go`, `core/tick.go`, `core/model.go`, `core/load.go`, `core/requests.go`, `core/seams.go` | `OriginJob`; `gateQueue`; the tick's call; the model's queue and `Deliver` cases; `Boot`; pause and kill cancelling the owner's jobs; `SetJobRunnerForTest` | 3 |
| `core/queue_test.go`, `core/queue_watch_test.go`, `core/queue_notice_test.go`, `core/gate_test.go`, `core/testmain_test.go` | The queue's tests, the gated-job case, the test shim | 3 |
| `core/settings.go`, `core/settings_test.go` | `SaveSettings` keeps the global `config.json`'s `scheduler` block as it is on disk | 3 |
| `core/iface.go`, `core/loop_core.go`, `core/queue_requests.go`, `core/queue_view.go`, `core/events.go`, `core/events_json.go`, `core/state_publish.go`, `core/model.go`, `core/rpc/server.go`, `core/rpc/replica.go`, `core/rpc/methods_gen.go`, `docs/specs/protocol.md`, `app/core_glue.go`, `app/app.go` | The `Core` methods, the requests' replies, `QueueView`, `QueueChanged`, `Reply.Job` and its JSON, the model's publication field, the replica, the wire, the TUI's applier | 4 |
| `core/queue_requests_test.go`, `core/queue_view_test.go`, `core/view_test.go`, `core/state_views_test.go`, `core/loop_test.go`, `core/rpc/client_test.go`, `core/rpc/wire_test.go`, `app/core_glue_test.go` | Their tests, and the event counts and kinds that gain `QueueChanged` | 4 |
| `internal/cli/job.go`, `internal/cli/job_unix.go`, `internal/cli/job_windows.go`, `internal/cli/job_test.go`, `internal/cli/daemon_test.go`, `internal/cli/testmain_test.go`, `internal/cli/README.md`, `internal/cli/CLAUDE.md`, `main.go` | `loom job`; the signal-number mapping off Windows; the test daemon's job runner | 5 |
| `session/claude-loom-context.md`, `session/claude-loom-context-workspace.md` | The context paragraph | 5 |
| `e2e/job_test.go`, `e2e/CLAUDE.md` | A daemon restarted while a job runs | 5 |
| `app/work.go`, `app/work_test.go`, `app/app.go`, `app/app_scripts.go`, `app/state_default.go`, `app/overview.go`, `app/requests.go`, `app/overlay_host.go` | The work view mode, its keys, the log tail and pager, the caps form's state, its pending request kind and overlay | 6 |
| `ui/work.go`, `ui/work_test.go`, `ui/overlay/workCapsOverlay.go`, `ui/overlay/workCapsOverlay_test.go` | Rendering the view; the caps form | 6 |
| `script/host.go`, `script/host_fake_test.go`, `script/api_actions.go`, `script/api_actions_test.go`, `script/defaults.lua` | `toggle_work_view` and `w` | 6 |
| `ui/card.go`, `ui/overview.go`, `ui/card_test.go`, `app/work_badge.go`, `app/work_badge_test.go`, `app/core_glue.go` | The job badges, and their update on `QueueChanged` | 7 |
| `USAGE.md`, `docs/ARCHITECTURE.md`, `docs/specs/scripting.md`, `docs/claude/the-work-queue.md`, `docs/claude/INDEX.md`, `sched/CLAUDE.md`, `core/README.md`, `app/README.md`, `ui/README.md`, `README.md`, `docs/claude/adding-a-core-event.md` | The docs, the guide's link, the package READMEs' rows for the new files and the work view, and `QueueChanged` in the lists of state events | 7 |

## Names contract

Every name one story defines and another uses. A story may add names of its own. A name another story needs that this section lacks goes back to the manager.

Texts a later story quotes or names, which every fix leaves unchanged: Story 1's `sched/testmain_test.go`, `sched/CLAUDE.md` and `sched/README.md`, as Stories 2 and 7 extend them; Story 3's `wq*` test helpers, and the `core` functions Story 7's `core/README.md` rows name; Story 4's `QueueChanged` case line (`m.queue = ev.View`) and `publishQueue`; Story 6's symbols that Story 7's `app/README.md` and `ui/README.md` name.

**config (Story 1)**, in `config/settings.go`:

```go
// SchedulerSettings is the global config.json's "scheduler" block. A 0 or
// absent field takes the host's default (sched.Resolve).
type SchedulerSettings struct {
	CPUs               int            `json:"cpus,omitempty"`
	MemoryGB           int            `json:"memory_gb,omitempty"`
	IOJobs             int            `json:"io_jobs,omitempty"`
	DefaultClaim       *ClaimSettings `json:"default_claim,omitempty"`
	StarveAfterMinutes int            `json:"starve_after_minutes,omitempty"`
	KeepFinished       int            `json:"keep_finished,omitempty"`
}

type ClaimSettings struct {
	CPUs     int `json:"cpus,omitempty"`
	MemoryGB int `json:"memory_gb,omitempty"`
}
```

- `Settings.Scheduler *SchedulerSettings` (`json:"scheduler,omitempty"`). `Settings.Clone` deep-copies it and its `DefaultClaim`.
- `func (s Settings) GetScheduler() SchedulerSettings`: a deep copy, or the zero value when nil.
- `func (c *Config) GetScheduler() SchedulerSettings` and `func (c *Config) SetScheduler(s SchedulerSettings)`: under the config lock. Both store or return a deep copy.

**sched: the logic (Story 1)**

```go
type JobID int64 // per host, from 1, never reused

type State string

const (
	Queued    State = "queued"
	Running   State = "running"
	Done      State = "done"
	Failed    State = "failed"
	Cancelled State = "cancelled"
	Lost      State = "lost"
)

func (s State) Finished() bool // Done, Failed, Cancelled or Lost

type Claim struct {
	CPUs      int  `json:"cpus,omitempty"`
	MemoryGB  int  `json:"memory_gb,omitempty"`
	IO        bool `json:"io,omitempty"`
	Exclusive bool `json:"exclusive,omitempty"` // every cap; CPUs, MemoryGB and IO stay zero
}

type Caps struct {
	CPUs     int `json:"cpus"`
	MemoryGB int `json:"memory_gb"`
	IOJobs   int `json:"io_jobs"`
}

// Need is what a claim takes from the caps. An exclusive claim takes them all,
// as they stand.
type Need struct {
	CPUs     int `json:"cpus"`
	MemoryGB int `json:"memory_gb"`
	IOSlots  int `json:"io_slots"`
}

func (c Claim) Need(caps Caps) Need

type Host struct {
	Name     string `json:"name"`
	Cores    int    `json:"cores"`
	RAMBytes uint64 `json:"ram_bytes"` // 0 when it could not be read
}

type Policy struct {
	Caps         Caps          `json:"caps"`
	DefaultClaim Claim         `json:"default_claim"` // CPUs and MemoryGB set
	StarveAfter  time.Duration `json:"starve_after"`
	KeepFinished int           `json:"keep_finished"`
}

func Resolve(s config.SchedulerSettings, h Host) (Policy, error)

type Notice string

const (
	NoticeNone        Notice = ""
	NoticeSent        Notice = "sent"
	NoticeUndelivered Notice = "undelivered"
)

type Record struct {
	ID           JobID     `json:"id"`
	Order        int64     `json:"order"`           // lower first; the ID at submit, the lowest queued Order − 1 on a move to the front
	Owner        uint64    `json:"owner,omitempty"` // a core.InstanceID; 0 for none
	Description  string    `json:"description"`
	Argv         []string  `json:"argv"`
	Cwd          string    `json:"cwd"`
	Claim        Claim     `json:"claim"` // the default claim already applied, except an exclusive one
	Notify       bool      `json:"notify,omitempty"`
	Tail         int       `json:"tail"`
	State        State     `json:"state"`
	Submitted    time.Time `json:"submitted"`
	Started      time.Time `json:"started,omitzero"`
	Ended        time.Time `json:"ended,omitzero"`
	PID          int       `json:"pid,omitempty"` // the shim's, from started.json
	Exit         *Exit     `json:"exit,omitempty"`
	Failure      string    `json:"failure,omitempty"`       // why it failed to start, or why it was lost
	PeakRSSKB    int64     `json:"peak_rss_kb,omitempty"`
	CancelReason string    `json:"cancel_reason,omitempty"` // set when a cancel is requested
	SignalledAt  time.Time `json:"signalled_at,omitzero"`   // when SIGTERM went to its group
	Notice       Notice    `json:"notice,omitempty"`
	NoticeWhy    string    `json:"notice_why,omitempty"`
}

type Submit struct {
	Argv        []string `json:"argv"`
	Cwd         string   `json:"cwd"`
	Env         []string `json:"env"`
	Description string   `json:"description"`
	Claim       Claim    `json:"claim"` // CPUs or MemoryGB 0: the default claim's
	Notify      bool     `json:"notify"`
	Tail        int      `json:"tail"`
	Launch      string   `json:"launch"` // $LOOM_LAUNCH; "" from a plain shell
}

func (s Submit) Validate() error              // the bounds of spec §6.1, except the cwd's existence
func CheckClaim(c Claim, p Policy) error       // a claim over a cap

type Decision struct {
	Start   []JobID          // in the order to start them
	Waiting map[JobID]string // why each queued job left waiting waits
}

func Admit(jobs []Record, p Policy, now time.Time) Decision

func Hints(env []string, cpus int, id JobID) []string // adds what env lacks; always sets LOOM_JOB

const StoreVersion = 1

type Store struct {
	Version int      `json:"version"`
	NextID  JobID    `json:"next_id"`
	Jobs    []Record `json:"jobs"`
}

var ErrNewerStore = errors.New("jobs.json is from a newer loom")

func LoadStore(dir string) (Store, error) // dir is <globalDir>/jobs; no file: {StoreVersion, NextID 1, no jobs}
func SaveStore(dir string, s Store) error // config.AtomicWriteFile, 0600; makes dir 0700

const (
	StoreFile   = "jobs.json"
	SpecFile    = "spec.json"
	LogFile     = "output.log"
	LockFile    = "run.lock"
	StartedFile = "started.json"
	ExitFile    = "exit.json"
)

func JobDir(dir string, id JobID) string    // filepath.Join(dir, the decimal ID)
func RemoveJobDir(dir string, id JobID) error // os.RemoveAll(JobDir(dir, id)): the only removal

type Spec struct {
	ID       JobID    `json:"id"`
	Argv     []string `json:"argv"`
	Cwd      string   `json:"cwd"`
	Env      []string `json:"env"`
	HintCPUs int      `json:"hint_cpus,omitempty"` // written when the job starts
}

func WriteSpec(jobDir string, s Spec) error // 0600; makes jobDir 0700
func ReadSpec(jobDir string) (Spec, error)

type Started struct {
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
}

type Exit struct {
	Code     int       `json:"code"`             // -1 for a signal
	Signal   string    `json:"signal,omitempty"` // "SIGKILL"
	Ended    time.Time `json:"ended"`
	MaxRSSKB int64     `json:"max_rss_kb,omitempty"`
	Error    string    `json:"error,omitempty"` // the exec failed; Code is then 127
}

func ReadStarted(jobDir string) (Started, error) // an error wrapping fs.ErrNotExist when absent
func ReadExit(jobDir string) (Exit, error)       // likewise
func writeStarted(jobDir string, s Started) error // atomic; the shim's (Story 2)
func writeExit(jobDir string, e Exit) error       // atomic; the shim's (Story 2)
```

Error and wait texts, which tests match:

- `Resolve`:
  - `scheduler.<key> is <v>: it must be <lo>–<hi>`, with `<key>` one of `cpus`, `memory_gb`, `io_jobs`, `starve_after_minutes`, `keep_finished`, `default_claim.cpus` or `default_claim.memory_gb`. A default claim's `<hi>` is its cap.
  - `this host's RAM could not be read: set scheduler.memory_gb`.
- `Submit.Validate`:
  - `the description is empty`
  - `the description is <n> characters: at most 200`
  - `no command to run`
  - `the command is <n> bytes: at most 65536`
  - `the environment is <n> bytes: at most 1048576`
  - `--tail is <n>: it must be 0–200`
  - `--cpus is <n>: it must be at least 1`
  - `--mem is <n>: it must be at least 1`
  - `--exclusive claims everything: drop --cpus, --mem and --io`
  - `the folder <cwd> is not an absolute path`
- `CheckClaim`:
  - `claims <n> cores; this host's cap is <cap>`
  - `claims <n> GB; this host's cap is <cap> GB`
- The model:
  - `the folder <cwd> does not exist`
  - `admission paused: <reason>`
- `Admit`'s `Waiting` texts, checked in this order (CPU, then memory, then I/O):
  - `needs <n> cores (<free> free)`
  - `needs <n> GB (<free> free)`
  - `needs an I/O slot` (a need of one slot)
  - `needs <n> I/O slots (<free> free)` (an exclusive claim)
  - `waits for job <id>, which has waited past <m>m`
  - `over this host's caps`

The hint variables, each set to N unless `env` already sets it: `CARGO_BUILD_JOBS=N`, `RUST_TEST_THREADS=N`, `MAKEFLAGS=-jN`, `NIX_BUILD_CORES=N`, `CMAKE_BUILD_PARALLEL_LEVEL=N`, `GOFLAGS=-p=N`. `LOOM_JOB=<id>` is always set. `Hints` returns a new slice: `env` without any `LOOM_JOB` it holds, then each hint variable `env` doesn't set, then `LOOM_JOB=<id>`. The shim runs the command with exactly that slice (`os/exec` keeps the last of a repeated key).

The files Stories 1 and 2 share. Story 1 writes them; Story 2 adds to them and changes none of Story 1's lines:

- `sched/testmain_test.go`, Story 1's whole file (`package sched`: the package's tests are internal):

```go
package sched

import (
	"os"
	"testing"

	"github.com/aidan-bailey/loom/internal/testenv"
	"github.com/aidan-bailey/loom/log"
)

func TestMain(m *testing.M) {
	cleanup := testenv.MustIsolateLoomDirs()
	_ = log.Initialize("", false)
	code := m.Run()
	log.Close()
	cleanup()
	os.Exit(code)
}
```

  Story 2 inserts `MaybeRunTestShim()` as `TestMain`'s first statement, and nothing else.
- `sched/CLAUDE.md`: a `# sched` heading, an orientation paragraph, `## Rules when modifying this package` with Story 1's rules, then `## Pointers`, the file's last section. Story 2 adds its rules as bullets at the end of the rules section, after Story 1's last, and changes no other line. Story 7 adds one bullet at the end of `## Pointers`, linking `../docs/claude/the-work-queue.md` (the lint fails a guide no CLAUDE.md links).
- `sched/README.md`: Story 2 adds one section, `## The runtime`, at the end of the file.

**sched: the runtime (Story 2)**

```go
func RunShim(jobDir string) int // the shim's whole life (spec §5.3); its exit code

type Runner struct {
	Shim []string // the argv before the job dir: {exe, "job-shim"}
	Env  []string // added to the shim process's own environment
}

func NewRunner() (Runner, error)                     // os.Executable() and "job-shim"
func (r Runner) Start(jobDir string) (pid int, err error) // a Setsid child; stdin /dev/null; stdout and stderr appended to output.log; a goroutine reaps it
func Probe(jobDir string) (alive bool, err error)    // a non-blocking flock test of run.lock, released at once
func SignalGroup(pgid int, sig syscall.Signal) error // syscall.Kill(-pgid, sig)

type Proc struct {
	PID   int
	PGID  int
	RSSKB int64
	CPU   time.Duration
}

type Snapshot struct {
	Taken time.Time
	Procs []Proc
}

func Sample(ctx context.Context) (Snapshot, error) // ps -A -o pid=,pgid=,rss=,time=; the caller gives it 5 s
func ParsePS(out []byte) ([]Proc, error)            // procps and BSD time formats
func (s Snapshot) Group(pgid int) (rssKB int64, cpu time.Duration, members int, leaderAlive bool)

func DetectHost() Host // os.Hostname, runtime.NumCPU, /proc/meminfo or sysctl hw.memsize (5 s)

const ShimTestEnv = "LOOM_SCHED_TEST_SHIM"

func MaybeRunTestShim() // first in a TestMain: when ShimTestEnv is "1", os.Exit(RunShim(the last argument))
func TestRunner() Runner // the test binary, with ShimTestEnv=1

var ErrUnsupported = errors.New("the work queue isn't available on Windows") // the Windows stubs' error
```

- `ErrUnsupported` is declared in `sched/shim.go`, built on every platform: core's Windows refusal and the CLI use it.
- `Exit.Signal` is `unix.SignalName`'s name for the signal (`golang.org/x/sys/unix`, vendored), and the follower maps it back with `unix.SignalNum`, so a job killed by any signal exits 128 + N (spec §6.3). A signal `unix.SignalName` can't name is recorded as `SIG<n>`, and the follower reads N from that name. Both calls sit in files built only off Windows.
- `Probe` reads a missing `run.lock` as free (`false, nil`).

The root package's `jobShimCmd` (`job_shim.go`, `Use: "job-shim <job dir>"`, `Hidden: true`, one argument) calls `os.Exit(sched.RunShim(args[0]))`. `main.go` adds `rootCmd.AddCommand(jobShimCmd)` on the line after `rootCmd.AddCommand(resetCmd)`. Story 5 adds `rootCmd.AddCommand(cli.JobCmd)` on the line after `rootCmd.AddCommand(cli.SweepCmd)`, and a `cli.ExitError` branch in `main()`; neither story changes a line the other's edit quotes.

**core: the queue's internals (Story 3)**

- `Model.queue *queueState`, built in `newModel`, loaded in `Boot`. Story 4 reads these fields, defined in `core/queue.go`:

```go
type queueState struct {
	dir      string                   // <globalDir>/jobs
	store    sched.Store              // every record
	loadErr  error                    // jobs.json failed to load: submissions refused, admission paused
	host     sched.Host               // detected once, at boot
	settings config.SchedulerSettings // the scheduler block as last read
	policy   sched.Policy             // in effect after the last pass; zero while paused
	paused   string                   // why admission is paused; "" when it isn't
	waiting  map[sched.JobID]string   // from the last pass
	usage    map[sched.JobID]jobUsage // running jobs, from the last sample
	runner   sched.Runner
	rev      uint64                   // bumped by every change to the queue, a job or its usage
}

type jobUsage struct {
	RSSKB int64
	CPU   float64 // cores
}
```

- The operations Story 4 wraps, each run on the loop. Each returns the refusal's error, with the texts of the spec and this contract:

```go
func (m *Model) submitJob(s sched.Submit) (id sched.JobID, warning string, err error) // warning: the unknown launch's text
func (m *Model) cancelJob(id sched.JobID, reason string) error
func (m *Model) moveJobToFront(id sched.JobID) error
func (m *Model) notifyJob(id sched.JobID) error
func (m *Model) setSchedulerCaps(s config.SchedulerSettings) error
func (m *Model) cancelOwnerJobs(owner InstanceID, reason string) // Pause and Kill call it
```

- The texts:
  - the unknown launch's warning: `this shell's loom session (launch <launch>) isn't one the daemon serves: job <id> has no owner, so no message will come`
  - cancel reasons, which follow "was cancelled" in the notice and the follower's line: `from the work view`, `with loom job cancel`, `because the session was paused`, `because the session was killed`
  - refusals: `job <id> has finished`, `job <id> isn't waiting`, `job <id> has no owner`, `there is no job <id>`
- On Windows (`runtime.GOOS == "windows"`), the queue is paused from `Boot` on, with `sched.ErrUnsupported`'s text as the reason, and `submitJob` refuses with `sched.ErrUnsupported` before any other check. So `loom job` and the view say the queue isn't available there (spec §10, §11).
- `OriginJob Origin = "job"` in `core/mod.go`. It needs the mod, has no "From" line and isn't the person's words. `withoutMod` records the notice as undelivered and never pastes.
- `gateQueue`, a `gateKind`, with interval `queueInterval = 3 * time.Second`, named `"queue"`.
- `func (m *Model) SetJobRunnerForTest(r sched.Runner)` in `core/seams.go`. It may be called between `core.New` and `Boot`; nothing after it replaces the runner.
- `submitJob` stores the description trimmed, and the claim with the policy's default claim filling a `CPUs` or `MemoryGB` of 0 (an exclusive claim stays as it is). It always sets `Submitted`.
- Story 4's `core` tests use Story 3's test helpers `wqModelAt`, `wqLaunch`, `wqPass`, `wqSettle`, `wqStop`, `wqOwnerSession`, `wqJob` and `wqHost`, and the private `queueState` fields `usage`, `waiting` and `store`, its method `index`, and the `*Model` method `admitJobs`.
- `Model.SaveSettings` (`core/settings.go`), saving the global workspace, never writes a `scheduler` block it was handed or holds in memory. It re-reads the block from the global `config.json`, as `refreshPolicy` does, writes that, and sets `ws.cfg`'s copy to it. A file that can't be read or parsed refuses the save with the error and changes nothing; a missing file reads as no block. So neither an S-overlay save nor a stale `ws.cfg` undoes a hand edit or another client's `SetSchedulerCaps`. Their names and signatures are Story 3's text; Story 4 uses them as Story 3 defines them, and a fix to one changes both stories.

**core: the queue on the wire (Story 4)**, in `core/queue_view.go`:

```go
type QueueView struct {
	Host      sched.Host
	Settings  config.SchedulerSettings // as configured; a 0 field shows the default
	Policy    sched.Policy             // in effect; zero while Paused != ""
	Paused    string                   // why admission is paused; "" when it isn't
	Claimed   sched.Need               // what the running jobs' claims take
	UsedCPU   float64                  // cores the running jobs use, last sampled
	UsedRSSKB int64                    // memory they use, last sampled
	Dir       string                   // <globalDir>/jobs
	Jobs      []JobView                // running (by start), queued (queue order), then finished (newest end first)
}

type JobView struct {
	ID               sched.JobID
	State            sched.State
	Description      string
	Argv             []string
	Cwd              string
	Owner            InstanceID  // 0: none
	OwnerTitle       string      // "" when the owner isn't served
	OwnerWorkspace   string      // the owner's workspace's WorkspaceView.Label; "" when the owner isn't served
	OwnerWorkspaceID WorkspaceID // the owner's workspace, which `o` opens (openTab); 0 when the owner isn't served
	Claim            sched.Claim
	Need             sched.Need // an exclusive claim's at the current caps
	Submitted        time.Time
	Started          time.Time
	Ended            time.Time
	Exit             *sched.Exit
	Failure          string
	CancelReason     string
	PeakRSSKB        int64
	RSSKB            int64
	CPU              float64
	LogPath          string // JobDir(Dir, ID)/output.log
	Waiting          string
	Notice           sched.Notice
	NoticeWhy        string
}
```

- `core.Core` gains, in `core/iface.go`:
  - `Queue() QueueView` (`rpc:local`);
  - the requests `SubmitJob(spec sched.Submit, req ReqID)`, `CancelJob(id sched.JobID, from CancelFrom, req ReqID)`, `MoveJobToFront(id sched.JobID, req ReqID)`, `NotifyJob(id sched.JobID, req ReqID)` and `SetSchedulerCaps(s config.SchedulerSettings, req ReqID)`.
- `type CancelFrom string`, with `CancelFromCLI CancelFrom = "cli"` and `CancelFromView CancelFrom = "view"`. `CancelJob` maps them to the reasons `with loom job cancel` and `from the work view`, and refuses any other value.
- `Reply.Job sched.JobID`: the new job's ID, on `SubmitJob`'s success. The unknown launch's warning travels in `Reply.Notice`.
- `QueueChanged{View QueueView}`, a state event with the coalescing key `"queue"`, published from `publishState` when `queueState.rev` moves: after `WorkspacesChanged` and the other model-wide state events, and before the instance views. The replica, `Model.Snapshot` and the wire test's order list follow the same order.
- The TUI keeps the newest view in `home.queue core.QueueView`, set by `applyCoreEvent`'s `QueueChanged` case (`app/core_glue.go`). That case holds the line `m.queue = ev.View`; Story 7 adds `m.syncJobBadges()` on the line after it, and changes nothing else in the case.

**cli (Story 5)**

- `cli.JobCmd *cobra.Command`, added to `rootCmd` in `main.go`, with `submit`, `run`, `wait`, `logs`, `list` and `cancel`.
- Flags: `-d`/`--desc`, `--cpus`, `--mem`, `--io`, `--exclusive`, `--tail` (submit only), `--no-notify` (submit only), `-f`/`--follow` (logs), `--tail` (logs), `--all` (list).
- `submit` prints `job <id> queued (<ordinal> in line, <n> running)` or `job <id> started`.
- The follower's lines go to stderr, prefixed `loom: `:
  - `job <id> queued (<ordinal> in line, <n> running)`
  - `job <id> started after <d>`
  - `job <id> done in <d> (exit 0)`
  - `job <id> failed in <d> (exit <n>)`
  - `job <id> was killed by <SIGNAME> after <d>`
  - `job <id> was cancelled <reason>`
  - `job <id> was lost: <failure>`
  - `job <id> could not start: <failure>`
  - when interrupted: `job <id> continues; a message will bring its result (loom job wait <id> follows it again)`, or `job <id> continues (loom job wait <id> follows it again)` for a job with no owner
- Ordinals: `1st`, `2nd`, `3rd`, `4th`, and so on.
- Durations: `time.Duration.Round(time.Second).String()`.
- The follower's exit codes are those of spec §6.3.

**TUI (Story 6)**

- `viewWork`, a `viewMode` after `viewWorkbench` in `app/app.go`.
- The Lua action `cs.actions.toggle_work_view()`, with the `script.Host` method `ToggleWorkView()`.
- `script/defaults.lua` binds `cs.bind("w", function() cs.actions.toggle_work_view() end, { help = "work queue" })`.
- `func (m *home) handleWorkKey(msg tea.KeyMsg) (tea.Model, tea.Cmd)` in `app/work.go`: the work view's keys.

## After the run

The owner, with a dev build from the `loom-dev` skill or a released build:

- [ ] `loom job run -d "sleep" -- sleep 5` from a plain shell queues, starts, follows and exits 0.
- [ ] From a Claude session, `loom job submit -d "tests" -- cargo test` returns at once, and a `[loom] job N` message arrives when it ends.
- [ ] Four default jobs submitted together run three at once on fresco. The fourth's wait reason names the cores.
- [ ] `w` shows the view; `x`, `t`, `o`, `enter` and `e` work; a caps edit applies at once.
- [ ] Stopping and restarting the daemon while a job runs leaves it running, and the new daemon settles it.
- [ ] Pausing a session cancels its jobs.

**Design stage:** /flow-draft session 8156f428-64d9-4732-b4d1-65fb9794ab5e, 2026-11-04T06:41:41Z to 2026-11-04T08:10:52Z UTC: $61.48 logged, $61.72 with estimated thinking; 19774 plan lines, $3.11 per 1,000 lines.

## Stories

- **Story 1** — `sched`'s logic and the `scheduler` config block.

  The pure half of the scheduler, with no processes: the job, claim, caps, policy and record types; `Resolve`, which applies the host defaults and the bounds; `Admit`, which is first fit with the starvation guard and says why each job waits; `Hints`; the `Submit` and claim validation; and the `jobs.json` store and per-job file codecs. Also config's `SchedulerSettings`, and the package's `CLAUDE.md`, which the coverage lint requires. The code holds the landmine marker `os.RemoveAll(`, in `RemoveJobDir` only. It removes `JobDir(dir, id)`, built from the ID, never a path read from a file.

  - **Developer:** intermediate-dev
  - **Files:** `config/settings.go`, `config/config.go`, `config/settings_test.go`, `config/README.md`, `sched/doc.go`, `sched/job.go`, `sched/job_test.go`, `sched/policy.go`, `sched/policy_test.go`, `sched/admit.go`, `sched/admit_test.go`, `sched/hints.go`, `sched/hints_test.go`, `sched/store.go`, `sched/store_test.go`, `sched/files.go`, `sched/files_test.go`, `sched/testmain_test.go`, `sched/CLAUDE.md`, `sched/README.md`
  - **Interfaces:** exposes the config and `sched` logic names of the names contract (Story 1); uses none
  - **Verify:** `CGO_ENABLED=0 go test ./sched/... ./config/... && CGO_ENABLED=0 go run ./tools/claudemd`
  - **Review:** deep
  - **Reading:** spec §4, §5.1, §5.2, §5.9, §6.1, §11, §12; `config/settings.go:1-200`, `config/config.go:180-210`, `config/config.go:315-360`, `config/atomic.go`, `config/CLAUDE.md`, `session/CLAUDE.md` (it holds `session/buildsweep`'s rules), `session/buildsweep/testmain_test.go`, `internal/testenv/CLAUDE.md`

- **Story 2** — The job runner: `loom job-shim`, starting, probing, signalling, sampling and the host.

  The process half of `sched`. `RunShim` lowers its priority (nice 10, and the Linux ioprio), takes `run.lock`, writes `started.json`, runs the argv with the spec's environment plus the hints, catches SIGTERM, SIGINT and SIGHUP so a signal sent to its process group reaches the command while it stays up to report, and writes `exit.json`. `Runner.Start` starts a shim as a `Setsid` child and reaps it. `Probe` tests the lock, `SignalGroup` signals a process group, `Sample` and `ParsePS` read `ps`, and `DetectHost` reads the host. The story also adds the test-shim hook, the Windows stubs, and the hidden `job-shim` command in the root package. The code holds the landmine markers `syscall.Kill(` (`SignalGroup`) and `os.Environ()` (`Runner.Start` builds the shim process's environment as `os.Environ()` plus `Runner.Env`; the job's environment comes only from `spec.json`). Its platform code lives only in `_unix.go`, `_windows.go`, `_linux.go`, `_darwin.go` and `_other.go` files.

  - **Developer:** intermediate-dev
  - **Files:** `sched/shim.go`, `sched/shim_unix.go`, `sched/shim_windows.go`, `sched/shim_test.go`, `sched/prio_linux.go`, `sched/prio_other.go`, `sched/sample.go`, `sched/sample_test.go`, `sched/host.go`, `sched/host_linux.go`, `sched/host_darwin.go`, `sched/host_other.go`, `sched/host_test.go`, `sched/testshim.go`, `sched/testmain_test.go`, `sched/CLAUDE.md`, `sched/README.md`, `job_shim.go`, `job_shim_test.go`, `main.go`, `flake.nix`
  - **Interfaces:** exposes the `sched` runtime names of the names contract (Story 2) and `jobShimCmd`; uses `Spec`, `Started`, `Exit`, `ReadSpec`, `writeStarted`, `writeExit`, `Hints`, `Host`, `JobDir` and the file-name constants from Story 1
  - **Verify:** `CGO_ENABLED=0 go test ./sched/... . && CGO_ENABLED=0 go build ./... && GOOS=windows CGO_ENABLED=0 go build ./... && GOOS=darwin CGO_ENABLED=0 go build ./... && nix eval --raw .#loom.drvPath >/dev/null`
  - **Review:** deep
  - **Reading:** spec §5.3, §5.4, §5.5, §5.6, §10, §11, §12; `session/buildsweep/lock_unix.go`, `session/buildsweep/lock_windows.go`, `session/buildsweep/proc.go`, `internal/daemon/proc_unix.go`, `internal/daemon/proc_windows.go`, `main.go:30-50`, `main.go:405-435`, `internal/cli/CLAUDE.md`

- **Story 3** — The queue in the model: admission, starting, watching, cancelling, adoption, notices and the session lifecycle.

  The model owns the queue. At boot it loads `jobs.json`, leaving a file it can't read untouched and pausing; it adopts and settles what ran while it was down, queued records included, and only then admits. Admission passes run on submit, on a job's end, on a caps change, on a move and on the tick while jobs wait, reading the global config each time. Starting a job is a model job that writes `spec.json`'s hint CPUs, starts the shim, and waits up to 5 s for `started.json`. `gateQueue` tests locks, samples usage, settles jobs, escalates a cancel to SIGKILL after 10 s, and kills a lost job's group only when the sample proves the group is the job's. `OriginJob` notices go through the outbox and are recorded as undelivered when there is no mod. Pause and kill cancel the owner's jobs, and retention drops finished jobs beyond `keep_finished`. The internal operations of the contract are what Story 4 wraps. The code calls `sched.SignalGroup` and `sched.RemoveJobDir`, so a signal goes only to a group whose lock test proves its shim alive, or which the lost-job rule of spec §5.4 allows. The scheduler's state is touched only on the loop, and jobs get snapshots.

  - **Developer:** intermediate-dev
  - **Files:** `core/queue.go`, `core/queue_admit.go`, `core/queue_watch.go`, `core/queue_ops.go`, `core/queue_notice.go`, `core/mod.go`, `core/gate.go`, `core/tick.go`, `core/model.go`, `core/load.go`, `core/requests.go`, `core/seams.go`, `core/queue_test.go`, `core/queue_watch_test.go`, `core/queue_notice_test.go`, `core/gate_test.go`, `core/testmain_test.go`, `core/settings.go`, `core/settings_test.go`
  - **Interfaces:** exposes `queueState`, `jobUsage`, the internal operations, `OriginJob`, `gateQueue` and `SetJobRunnerForTest` of the names contract (Story 3); uses Story 1's logic and Story 2's runtime
  - **Verify:** `CGO_ENABLED=0 go test ./core/... && CC=clang CGO_ENABLED=1 go test -race ./core/ -run 'Queue|Job'`
  - **Review:** deep
  - **Reading:** spec §5.2-§5.9, §6.2, §11, §12; `docs/claude/adding-a-gated-job.md`, `core/CLAUDE.md`, `core/build_sweep.go`, `core/gate.go`, `core/gate_test.go:195-260`, `core/tick.go:100-140`, `core/model.go:250-330`, `core/model.go:430-500`, `core/load.go:205-300`, `core/requests.go:280-320`, `core/mod.go:55-130`, `core/mod.go:385-450`, `core/persist.go`, `core/testmain_test.go`

- **Story 4** — The queue on the wire: the requests, `QueueView` and `QueueChanged`.

  This publishes the queue to every client. Story 3's operations become `Core` requests that reply with their refusals, and `SubmitJob` replies with the new ID in `Reply.Job` and the unknown launch's warning in `Reply.Notice`. `Queue()` is a local query on the replica. `QueueChanged` is a state event, with every step of the event guide: `EventTypes`, the coalescing key, the replica's state and its events in publish order, `Model.Snapshot`, and the TUI's applier into `home.queue`. The story regenerates `methods_gen.go` and `protocol.md`. The environment never appears in a published view.

  - **Developer:** intermediate-dev
  - **Files:** `core/iface.go`, `core/loop_core.go`, `core/queue_requests.go`, `core/queue_view.go`, `core/events.go`, `core/events_json.go`, `core/state_publish.go`, `core/model.go`, `core/rpc/server.go`, `core/rpc/replica.go`, `core/rpc/methods_gen.go`, `docs/specs/protocol.md`, `app/core_glue.go`, `app/app.go`, `core/queue_requests_test.go`, `core/queue_view_test.go`, `core/view_test.go`, `core/state_views_test.go`, `core/loop_test.go`, `core/rpc/client_test.go`, `core/rpc/wire_test.go`, `app/core_glue_test.go`
  - **Interfaces:** exposes `QueueView`, `JobView`, `CancelFrom`, the `Core` methods, `Reply.Job`, `QueueChanged` and `home.queue` of the names contract (Story 4); uses Story 3's `queueState` and internal operations
  - **Verify:** `CGO_ENABLED=0 go test ./core/... ./app/...`
  - **Review:** deep
  - **Reading:** spec §9, §6.2, §11; `docs/claude/adding-a-core-method.md`, `docs/claude/adding-a-core-event.md`, `core/iface.go`, `core/loop_core.go`, `core/events.go:193-237`, `core/state_publish.go`, `core/rpc/replica.go`, `core/rpc/server.go` (coalesceKey), `core/rpc/CLAUDE.md`, `app/core_glue.go`, `core/web.go` (a published view's shape)

- **Story 5** — `loom job`, the Claude context paragraph, and the restart e2e.

  The CLI is a daemon client joined through `cli.Join`. `submit` captures the argv, cwd, environment and `$LOOM_LAUNCH`, and prints the queue line. `run` and `wait` follow a job from the replica and `output.log`, exit with spec §6.3's codes, and send `NotifyJob` when interrupted. `logs`, `list` and `cancel` complete it. The story also adds the paragraph of spec §8 to both context files, and an e2e test that restarts the daemon while a job runs. The code holds the landmine marker `os.Environ()`: `submit` sends `os.Environ()` as the job's environment, and builds no git command from it.

  - **Developer:** intermediate-dev
  - **Files:** `internal/cli/job.go`, `internal/cli/job_unix.go`, `internal/cli/job_windows.go`, `internal/cli/job_test.go`, `internal/cli/daemon_test.go`, `internal/cli/testmain_test.go`, `internal/cli/README.md`, `internal/cli/CLAUDE.md`, `main.go`, `session/claude-loom-context.md`, `session/claude-loom-context-workspace.md`, `e2e/job_test.go`, `e2e/CLAUDE.md`
  - **Interfaces:** exposes `cli.JobCmd` and the CLI texts of the names contract (cli); uses Story 4's `Core` methods, `QueueView`, `Reply.Job` and `QueueChanged`, Story 3's `Model.SetJobRunnerForTest`, Story 2's `MaybeRunTestShim`, `TestRunner` and `ErrUnsupported`, and Story 1's `JobDir` and `ReadStarted`
  - **Verify:** `CGO_ENABLED=0 go test ./internal/cli/... ./session/... && CGO_ENABLED=0 go test -tags e2e ./e2e/... -run Job`
  - **Review:** deep
  - **Reading:** spec §6, §8, §12; `internal/cli/CLAUDE.md`, `internal/cli/session.go`, `internal/cli/sweep.go`, `internal/cli/join.go`, `internal/cli/testmain_test.go`, `main.go:405-435`, `e2e/CLAUDE.md`, `e2e/daemon_test.go`, `session/loom_context.go`

- **Story 6** — The work view: the view mode, `w`, the log tail and pager, and the caps form.

  The fourth view mode, over `home.queue`. It shows the header with the host, the capacity bars (or the paused reason in their place), the RUNNING, QUEUED and RECENT sections with why each queued job waits, and the selected job's log tail, read from disk. `enter` opens a scrolling pager, `x` cancels after a confirm, `t` moves a job to the front, `o` focuses the owner's session (opening its tab), and `e` opens the caps form, which sends `SetSchedulerCaps` and shows its refusal. `w` or `esc` returns to the mode the view came from. The view isn't persisted, and mouse input is dropped. The keys follow the TUI's rules: an action names the job's ID captured when its key passed, and the view re-reads `home.queue` after every drain.

  - **Developer:** intermediate-dev
  - **Files:** `app/work.go`, `app/work_test.go`, `app/app.go`, `app/app_scripts.go`, `app/state_default.go`, `app/overview.go`, `app/requests.go`, `app/overlay_host.go`, `ui/work.go`, `ui/work_test.go`, `ui/overlay/workCapsOverlay.go`, `ui/overlay/workCapsOverlay_test.go`, `script/host.go`, `script/host_fake_test.go`, `script/api_actions.go`, `script/api_actions_test.go`, `script/defaults.lua`
  - **Interfaces:** exposes `viewWork`, `toggle_work_view`, `ToggleWorkView` and `handleWorkKey` of the names contract (TUI); uses Story 4's `home.queue`, `QueueView`, `JobView` and the `CancelJob`, `MoveJobToFront` and `SetSchedulerCaps` requests
  - **Verify:** `CGO_ENABLED=0 go test ./app/... ./ui/... ./script/...`
  - **Review:** deep
  - **Reading:** spec §7.1, §7.2; `app/CLAUDE.md`, `ui/CLAUDE.md`, `script/CLAUDE.md`, `docs/claude/adding-a-modal-state.md`, `app/app.go:85-110`, `app/overview.go`, `app/app_scripts.go:370-390`, `script/host.go:100-120`, `script/api_actions.go:195-215`, `script/defaults.lua`, `ui/overview.go`, `ui/overlay/settingsOverlay.go`

- **Story 7** — Badges on session cards, and the docs.

  A session's rail card and overview card show `⚙ job <id>` (with `+N` for more running) or `⚙ queued`, computed from `home.queue`. The docs follow: `USAGE.md` gains `loom job`, the work view and its keys in the Keyboard Reference, the `scheduler` block, and `KillMode=process` in the systemd unit; `docs/ARCHITECTURE.md` gains the `sched` row, `jobs/` in persistent state, and the queue in the daemon's work with no TUI; `docs/specs/scripting.md` gains `toggle_work_view`; and the guide `docs/claude/the-work-queue.md`, with its `INDEX.md` entry, records the model's side, the test shim, and the lock-before-signal rule.

  - **Developer:** intermediate-dev
  - **Files:** `ui/card.go`, `ui/overview.go`, `ui/card_test.go`, `app/work_badge.go`, `app/work_badge_test.go`, `app/core_glue.go`, `USAGE.md`, `docs/ARCHITECTURE.md`, `docs/specs/scripting.md`, `docs/claude/the-work-queue.md`, `docs/claude/INDEX.md`, `sched/CLAUDE.md`, `core/README.md`, `app/README.md`, `ui/README.md`, `README.md`, `docs/claude/adding-a-core-event.md`
  - **Interfaces:** exposes none; uses Story 4's `home.queue` and `JobView`, and Story 6's `toggle_work_view`
  - **Verify:** `CGO_ENABLED=0 go test ./app/... ./ui/... ./tools/claudemd/... && CGO_ENABLED=0 go run ./tools/claudemd`
  - **Review:** deep
  - **Reading:** spec §7.3, §5.6, §10, §13; `ui/card.go`, `ui/overview.go`, `USAGE.md` (the Keyboard Reference, the systemd unit), `docs/ARCHITECTURE.md`, `docs/specs/scripting.md`, `docs/claude/INDEX.md`, `CLAUDE.md` (Conventions for CLAUDE.md files)

<!-- plan_join: the stories below are generated from the drafts; edit the drafts, then re-join -->

## Story 1: `sched`'s logic and the `scheduler` config block

- **Developer:** intermediate-dev
- **Files:** `config/settings.go`, `config/config.go`, `config/settings_test.go`, `config/README.md`, `sched/doc.go`, `sched/job.go`, `sched/job_test.go`, `sched/policy.go`, `sched/policy_test.go`, `sched/admit.go`, `sched/admit_test.go`, `sched/hints.go`, `sched/hints_test.go`, `sched/store.go`, `sched/store_test.go`, `sched/files.go`, `sched/files_test.go`, `sched/testmain_test.go`, `sched/CLAUDE.md`, `sched/README.md`
- **Interfaces:** exposes the config and `sched` logic names of the names contract (Story 1); uses none
- **Verify:** `CGO_ENABLED=0 go test ./sched/... ./config/... && CGO_ENABLED=0 go run ./tools/claudemd`
- **Review:** deep

The pure half of the scheduler, with no processes: config's `SchedulerSettings` (the `scheduler` block of the global `config.json`, deep-copied in `Clone`); then the `sched` package's job, claim, caps, policy and record types; `Submit.Validate` and `CheckClaim`, which hold every bound of a submission; `Resolve`, which applies the host defaults and the bounds and fails closed; `Admit`, which is first fit with the starvation guard and says why each job waits; `Hints`; and the `jobs.json` store and the per-job file codecs. It also creates `sched/CLAUDE.md`, which the coverage lint requires of any Go package, and writes Story 1's rules there task by task, each with the test that enforces it. The code holds the landmine marker `os.RemoveAll(`, in `RemoveJobDir` only (and in the strings of the test that scans for it): `RemoveJobDir` removes `JobDir(dir, id)`, built from the ID, never a path read from a file, and refuses an empty, relative or unclean `dir` and an ID below 1. The supervisor should dispatch this story's developer with `effort: xhigh`. Story 2 adds to three of this story's files (`sched/testmain_test.go`, `sched/CLAUDE.md`, `sched/README.md`) and changes none of Story 1's lines in them; keep `sched/testmain_test.go` exactly as the names contract gives it. `sched` offers no helper that applies the default claim or trims the description: the model's `submitJob` (Story 3) does both, since `Submit.Validate` checks the trimmed description and changes nothing.

### Task 1.1: The `scheduler` block in config

**Files:**
- Modify: `config/settings.go` (the `Settings` struct's last field, `Clone`, and after `GetBuildSweepAfterHours`)
- Modify: `config/config.go` (after `GetBuildSweepAfterHours`)
- Modify: `config/README.md:34`
- Test: `config/settings_test.go`

- [ ] **Step 1: Write the failing tests**

In `config/settings_test.go`, replace the import block

```go
import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)
```

with

```go
import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)
```

In `TestSettings_ConfigJSONIsUnchanged`, whose file sets every field, set the new one too: replace

```go
  "cache_ttl_1h": true,
  "claude_1m_context": true
}`)
```

with

```go
  "cache_ttl_1h": true,
  "claude_1m_context": true,
  "scheduler": {
    "cpus": 14,
    "memory_gb": 24,
    "io_jobs": 2,
    "default_claim": {"cpus": 4, "memory_gb": 6},
    "starve_after_minutes": 10,
    "keep_finished": 50
  }
}`)
```

and append at the end of the file, after `TestFromSettings_OwnsItsCopy`:

```go
// savedConfigKeys saves cfg into a fresh dir and returns the top-level keys
// of the config.json it wrote.
func savedConfigKeys(t *testing.T, cfg *Config) map[string]json.RawMessage {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, SaveConfigTo(cfg, dir))
	data, err := os.ReadFile(filepath.Join(dir, ConfigFileName))
	require.NoError(t, err)
	var keys map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &keys))
	return keys
}

// TestSettings_SchedulerBlockRoundTrips: the "scheduler" block loads from a
// config.json and saves back to the same JSON, and a config without one saves
// none, so a user who never set it gets no new key.
func TestSettings_SchedulerBlockRoundTrips(t *testing.T) {
	const block = `{
  "cpus": 14,
  "memory_gb": 24,
  "io_jobs": 2,
  "default_claim": {"cpus": 4, "memory_gb": 6},
  "starve_after_minutes": 10,
  "keep_finished": 50
}`
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ConfigFileName), []byte(`{"scheduler": `+block+`}`), 0o644))
	cfg := LoadConfigFrom(dir)
	assert.Equal(t, SchedulerSettings{
		CPUs:               14,
		MemoryGB:           24,
		IOJobs:             2,
		DefaultClaim:       &ClaimSettings{CPUs: 4, MemoryGB: 6},
		StarveAfterMinutes: 10,
		KeepFinished:       50,
	}, cfg.GetScheduler())
	assert.JSONEq(t, block, string(savedConfigKeys(t, cfg)["scheduler"]))

	absent := LoadConfigFrom(t.TempDir())
	assert.Equal(t, SchedulerSettings{}, absent.GetScheduler(), "an absent block is the zero value: every field takes the host's default")
	assert.NotContains(t, savedConfigKeys(t, absent), "scheduler")
}

// TestSettings_CloneCopiesTheSchedulerBlock: a clone's scheduler block and
// its default claim are its own; an absent block and an absent default claim
// stay absent.
func TestSettings_CloneCopiesTheSchedulerBlock(t *testing.T) {
	s := Settings{Scheduler: &SchedulerSettings{CPUs: 8, DefaultClaim: &ClaimSettings{CPUs: 2, MemoryGB: 3}}}
	c := s.Clone()
	c.Scheduler.CPUs = 1
	c.Scheduler.DefaultClaim.CPUs = 99
	c.Scheduler.DefaultClaim.MemoryGB = 99
	assert.Equal(t, 8, s.Scheduler.CPUs)
	assert.Equal(t, ClaimSettings{CPUs: 2, MemoryGB: 3}, *s.Scheduler.DefaultClaim)

	assert.Nil(t, Settings{}.Clone().Scheduler, "an absent block stays absent")
	noClaim := Settings{Scheduler: &SchedulerSettings{CPUs: 8}}.Clone()
	assert.Nil(t, noClaim.Scheduler.DefaultClaim, "an absent default claim stays absent")
}

// TestSettings_GetSchedulerReturnsACopy: the getter hands out a block the
// caller may change, and the zero value when there is none.
func TestSettings_GetSchedulerReturnsACopy(t *testing.T) {
	assert.Equal(t, SchedulerSettings{}, Settings{}.GetScheduler())

	s := Settings{Scheduler: &SchedulerSettings{KeepFinished: 5, DefaultClaim: &ClaimSettings{CPUs: 2}}}
	got := s.GetScheduler()
	got.KeepFinished = 99
	got.DefaultClaim.CPUs = 99
	assert.Equal(t, 5, s.Scheduler.KeepFinished)
	assert.Equal(t, 2, s.Scheduler.DefaultClaim.CPUs)
}

// TestConfig_SchedulerGetAndSetKeepCopies: SetScheduler keeps a copy of its
// argument, GetScheduler hands out a copy, and a snapshot's block is its own.
func TestConfig_SchedulerGetAndSetKeepCopies(t *testing.T) {
	cfg := FromSettings(Settings{})
	assert.Equal(t, SchedulerSettings{}, cfg.GetScheduler())

	in := SchedulerSettings{CPUs: 6, DefaultClaim: &ClaimSettings{CPUs: 2, MemoryGB: 4}}
	cfg.SetScheduler(in)
	in.CPUs = 99
	in.DefaultClaim.MemoryGB = 99
	want := SchedulerSettings{CPUs: 6, DefaultClaim: &ClaimSettings{CPUs: 2, MemoryGB: 4}}
	got := cfg.GetScheduler()
	assert.Equal(t, want, got, "SetScheduler keeps a copy")

	got.DefaultClaim.CPUs = 99
	assert.Equal(t, 2, cfg.GetScheduler().DefaultClaim.CPUs, "GetScheduler hands out a copy")

	snap := cfg.Snapshot()
	snap.Scheduler.DefaultClaim.CPUs = 77
	assert.Equal(t, want, cfg.GetScheduler(), "a snapshot's block is its own")
}

// TestConfig_SchedulerIsRaceSafe: SetScheduler and GetScheduler run under the
// config lock and share nothing. Only a -race run can fail it.
func TestConfig_SchedulerIsRaceSafe(t *testing.T) {
	cfg := FromSettings(Settings{})
	var wg sync.WaitGroup
	for i := 1; i <= 4; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 1; j <= 200; j++ {
				cfg.SetScheduler(SchedulerSettings{CPUs: i, DefaultClaim: &ClaimSettings{CPUs: 1, MemoryGB: j}})
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if got := cfg.GetScheduler(); got.DefaultClaim != nil {
					got.DefaultClaim.CPUs++ // a copy: this races only if it is shared
				}
			}
		}()
	}
	wg.Wait()
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `CGO_ENABLED=0 go test ./config -run Scheduler`

Expected: the build fails (line numbers differ):

```
# github.com/aidan-bailey/loom/config [github.com/aidan-bailey/loom/config.test]
config/settings_test.go:NN:NN: undefined: SchedulerSettings
config/settings_test.go:NN:NN: undefined: ClaimSettings
config/settings_test.go:NN:NN: cfg.GetScheduler undefined (type *Config has no field or method GetScheduler)
...
FAIL	github.com/aidan-bailey/loom/config [build failed]
```

- [ ] **Step 3: Implement**

In `config/settings.go`, replace the last field of `Settings` and the closing brace

```go
	Claude1MContext *bool `json:"claude_1m_context,omitempty"`
}
```

with

```go
	Claude1MContext *bool `json:"claude_1m_context,omitempty"`
	// Scheduler holds the work queue's caps and defaults (see
	// sched.Resolve). Read from the global config.json only, at every
	// admission pass, so a hand edit applies without a restart; a
	// workspace's config.json never holds it. nil is the block absent: every
	// field takes the host's default. Read it through GetScheduler.
	Scheduler *SchedulerSettings `json:"scheduler,omitempty"`
}

// SchedulerSettings is the global config.json's "scheduler" block. A 0 or
// absent field takes the host's default (sched.Resolve).
type SchedulerSettings struct {
	CPUs               int            `json:"cpus,omitempty"`
	MemoryGB           int            `json:"memory_gb,omitempty"`
	IOJobs             int            `json:"io_jobs,omitempty"`
	DefaultClaim       *ClaimSettings `json:"default_claim,omitempty"`
	StarveAfterMinutes int            `json:"starve_after_minutes,omitempty"`
	KeepFinished       int            `json:"keep_finished,omitempty"`
}

// ClaimSettings is the default claim of a job that names none: whole cores
// and whole GB. A 0 field takes the host's default.
type ClaimSettings struct {
	CPUs     int `json:"cpus,omitempty"`
	MemoryGB int `json:"memory_gb,omitempty"`
}

// clone deep-copies s: its default claim is its own.
func (s SchedulerSettings) clone() SchedulerSettings {
	out := s
	out.DefaultClaim = clonePtr(s.DefaultClaim)
	return out
}
```

In `Clone`, replace

```go
	out.Claude1MContext = clonePtr(s.Claude1MContext)
	return out
}
```

with

```go
	out.Claude1MContext = clonePtr(s.Claude1MContext)
	if s.Scheduler != nil {
		block := s.Scheduler.clone()
		out.Scheduler = &block
	}
	return out
}
```

After `GetBuildSweepAfterHours`, replace

```go
func (s Settings) GetBuildSweepAfterHours() int { return max(s.BuildSweepAfterHours, 0) }
```

with

```go
func (s Settings) GetBuildSweepAfterHours() int { return max(s.BuildSweepAfterHours, 0) }

// GetScheduler returns a deep copy of the scheduler block, or the zero value
// when there is none: every field then takes the host's default
// (sched.Resolve). Config's own GetScheduler reads it under the lock.
func (s Settings) GetScheduler() SchedulerSettings {
	if s.Scheduler == nil {
		return SchedulerSettings{}
	}
	return s.Scheduler.clone()
}
```

In `config/config.go`, replace

```go
func (c *Config) GetBuildSweepAfterHours() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Settings.GetBuildSweepAfterHours()
}
```

with

```go
func (c *Config) GetBuildSweepAfterHours() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Settings.GetBuildSweepAfterHours()
}

// GetScheduler returns a deep copy of the scheduler block under the config
// lock (Settings.GetScheduler): the model reads it at every admission pass
// while the work view's caps form writes it.
func (c *Config) GetScheduler() SchedulerSettings {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Settings.GetScheduler()
}

// SetScheduler stores a deep copy of s as the scheduler block, under the
// config lock. It does not save the file; the caller does (SaveConfigTo).
func (c *Config) SetScheduler(s SchedulerSettings) {
	c.mu.Lock()
	defer c.mu.Unlock()
	block := s.clone()
	c.Settings.Scheduler = &block
}
```

In `config/README.md`, after line 34 (the bullet that starts with ``- `ClaudeLoomContext` (`LoomContextEnabled`)``), add a bullet:

```markdown
- `Scheduler` (`scheduler`, a `*SchedulerSettings`), read from the global `config.json` only, at every admission pass: the work queue's caps (`cpus`, `memory_gb`, `io_jobs`), its default claim, the starvation wait (`starve_after_minutes`) and `keep_finished`. A 0 or absent field takes the host's default, which `sched.Resolve` applies together with the bounds; nil is the block absent. Read through `Config.GetScheduler()` and set through `Config.SetScheduler`, which copy under the lock.
```

- [ ] **Step 4: Run it to see it pass**

Run:

```
CGO_ENABLED=0 go test ./config/... -run Scheduler -v
CGO_ENABLED=0 go test ./config/...
CC=clang CGO_ENABLED=1 go test -race ./config -run 'Scheduler|Mutate'
gofmt -l config
CGO_ENABLED=0 go vet ./config/...
```

Expected: the first prints `--- PASS` for `TestSettings_SchedulerBlockRoundTrips`, `TestSettings_CloneCopiesTheSchedulerBlock`, `TestSettings_GetSchedulerReturnsACopy`, `TestConfig_SchedulerGetAndSetKeepCopies` and `TestConfig_SchedulerIsRaceSafe`; the second prints `ok  	github.com/aidan-bailey/loom/config	…`; the third prints `ok` with no race report (drop `CC=clang` where gcc exists); `gofmt -l` and `go vet` print nothing.

- [ ] **Step 5: Commit**

```
git commit -m "feat(config): the scheduler settings block" -- config/settings.go config/config.go config/settings_test.go config/README.md
```

### Task 1.2: The job types, the submission checks and the host policy

**Files:**
- Create: `sched/doc.go`
- Create: `sched/job.go`
- Create: `sched/policy.go`
- Create: `sched/testmain_test.go`
- Create: `sched/CLAUDE.md`
- Test: `sched/job_test.go`, `sched/policy_test.go`

- [ ] **Step 1: Write the failing tests**

Create `sched/testmain_test.go` (Story 1's whole file; Story 2 inserts `MaybeRunTestShim()` as `TestMain`'s first statement and changes nothing else):

```go
package sched

import (
	"os"
	"testing"

	"github.com/aidan-bailey/loom/internal/testenv"
	"github.com/aidan-bailey/loom/log"
)

func TestMain(m *testing.M) {
	cleanup := testenv.MustIsolateLoomDirs()
	_ = log.Initialize("", false)
	code := m.Run()
	log.Close()
	cleanup()
	os.Exit(code)
}
```

Create `sched/job_test.go`:

```go
package sched

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// submitFixture is a submission that passes Validate; a test breaks one field
// of it.
func submitFixture() Submit {
	return Submit{
		Argv:        []string{"cargo", "test"},
		Cwd:         "/work/tree",
		Description: "tests",
		Tail:        20,
	}
}

func TestState_Finished(t *testing.T) {
	finished := map[State]bool{
		Queued: false, Running: false, Done: true, Failed: true, Cancelled: true, Lost: true,
	}
	for state, want := range finished {
		assert.Equal(t, want, state.Finished(), string(state))
	}
	assert.False(t, State("").Finished(), "a state this build does not know has not ended")
}

func TestClaim_Need(t *testing.T) {
	caps := Caps{CPUs: 14, MemoryGB: 22, IOJobs: 2}
	tests := []struct {
		name  string
		claim Claim
		caps  Caps
		want  Need
	}{
		{"plain", Claim{CPUs: 4, MemoryGB: 6}, caps, Need{CPUs: 4, MemoryGB: 6}},
		{"io takes one slot", Claim{CPUs: 2, MemoryGB: 3, IO: true}, caps, Need{CPUs: 2, MemoryGB: 3, IOSlots: 1}},
		{"exclusive takes every cap", Claim{Exclusive: true}, caps, Need{CPUs: 14, MemoryGB: 22, IOSlots: 2}},
		{"exclusive follows a cap edit", Claim{Exclusive: true}, Caps{CPUs: 8, MemoryGB: 10, IOJobs: 1}, Need{CPUs: 8, MemoryGB: 10, IOSlots: 1}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.claim.Need(tc.caps))
		})
	}
}

func TestSubmit_Validate(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Submit)
		want string // the error text; "" for none
	}{
		{"a valid submission", func(s *Submit) {}, ""},
		{"the folder need not exist", func(s *Submit) { s.Cwd = "/no/such/folder" }, ""},
		{"a description is trimmed", func(s *Submit) { s.Description = "  x  " }, ""},
		{"an empty description", func(s *Submit) { s.Description = "" }, "the description is empty"},
		{"a blank description", func(s *Submit) { s.Description = " \t\n" }, "the description is empty"},
		{"200 characters", func(s *Submit) { s.Description = strings.Repeat("é", 200) }, ""},
		{"200 characters between blanks", func(s *Submit) { s.Description = "  " + strings.Repeat("x", 200) + "  " }, ""},
		{"201 characters", func(s *Submit) { s.Description = strings.Repeat("é", 201) }, "the description is 201 characters: at most 200"},
		{"no command", func(s *Submit) { s.Argv = nil }, "no command to run"},
		{"a command of 64 KiB", func(s *Submit) { s.Argv = []string{strings.Repeat("a", 65536)} }, ""},
		{"a command of 64 KiB and a byte", func(s *Submit) { s.Argv = []string{"a", strings.Repeat("a", 65536)} }, "the command is 65537 bytes: at most 65536"},
		{"an environment of 1 MiB", func(s *Submit) { s.Env = []string{strings.Repeat("a", 1<<20)} }, ""},
		{"an environment of 1 MiB and a byte", func(s *Submit) { s.Env = []string{"a", strings.Repeat("a", 1<<20)} }, "the environment is 1048577 bytes: at most 1048576"},
		{"tail 0", func(s *Submit) { s.Tail = 0 }, ""},
		{"tail 200", func(s *Submit) { s.Tail = 200 }, ""},
		{"tail 201", func(s *Submit) { s.Tail = 201 }, "--tail is 201: it must be 0–200"},
		{"tail -1", func(s *Submit) { s.Tail = -1 }, "--tail is -1: it must be 0–200"},
		{"cpus 0 takes the default claim's", func(s *Submit) { s.Claim.CPUs = 0 }, ""},
		{"cpus -1", func(s *Submit) { s.Claim.CPUs = -1 }, "--cpus is -1: it must be at least 1"},
		{"mem 0 takes the default claim's", func(s *Submit) { s.Claim.MemoryGB = 0 }, ""},
		{"mem -3", func(s *Submit) { s.Claim.MemoryGB = -3 }, "--mem is -3: it must be at least 1"},
		{"exclusive alone", func(s *Submit) { s.Claim = Claim{Exclusive: true} }, ""},
		{"exclusive with cpus", func(s *Submit) { s.Claim = Claim{Exclusive: true, CPUs: 2} }, "--exclusive claims everything: drop --cpus, --mem and --io"},
		{"exclusive with mem", func(s *Submit) { s.Claim = Claim{Exclusive: true, MemoryGB: 2} }, "--exclusive claims everything: drop --cpus, --mem and --io"},
		{"exclusive with io", func(s *Submit) { s.Claim = Claim{Exclusive: true, IO: true} }, "--exclusive claims everything: drop --cpus, --mem and --io"},
		{"a relative folder", func(s *Submit) { s.Cwd = "work/tree" }, "the folder work/tree is not an absolute path"},
		{"no folder", func(s *Submit) { s.Cwd = "" }, "the folder  is not an absolute path"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := submitFixture()
			tc.edit(&s)
			err := s.Validate()
			if tc.want == "" {
				assert.NoError(t, err)
				return
			}
			assert.EqualError(t, err, tc.want)
		})
	}
}

// TestSubmit_ValidateReportsTheFirstFault: a submission with every fault is
// refused for the first one in the order the texts are listed, and each fix
// brings up the next.
func TestSubmit_ValidateReportsTheFirstFault(t *testing.T) {
	s := Submit{Cwd: "rel", Tail: 500, Claim: Claim{CPUs: -1, MemoryGB: -1, IO: true, Exclusive: true}}
	wants := []string{
		"the description is empty",
		"no command to run",
		"--tail is 500: it must be 0–200",
		"--cpus is -1: it must be at least 1",
		"--mem is -1: it must be at least 1",
		"--exclusive claims everything: drop --cpus, --mem and --io",
		"the folder rel is not an absolute path",
	}
	fixes := []func(*Submit){
		func(s *Submit) { s.Description = "d" },
		func(s *Submit) { s.Argv = []string{"x"} },
		func(s *Submit) { s.Tail = 0 },
		func(s *Submit) { s.Claim.CPUs = 0 },
		func(s *Submit) { s.Claim.MemoryGB = 0 },
		func(s *Submit) { s.Claim.IO = false },
		func(s *Submit) { s.Cwd = "/ok" },
	}
	for i, want := range wants {
		require.EqualError(t, s.Validate(), want, "fault %d", i)
		fixes[i](&s)
	}
	assert.NoError(t, s.Validate())
}

func TestCheckClaim(t *testing.T) {
	p := Policy{Caps: Caps{CPUs: 14, MemoryGB: 22, IOJobs: 2}}
	tests := []struct {
		name  string
		claim Claim
		want  string // the error text; "" for none
	}{
		{"at the cores cap", Claim{CPUs: 14, MemoryGB: 1}, ""},
		{"over the cores cap", Claim{CPUs: 15, MemoryGB: 1}, "claims 15 cores; this host's cap is 14"},
		{"at the memory cap", Claim{CPUs: 1, MemoryGB: 22}, ""},
		{"over the memory cap", Claim{CPUs: 1, MemoryGB: 23}, "claims 23 GB; this host's cap is 22 GB"},
		{"over both reports the cores", Claim{CPUs: 32, MemoryGB: 64}, "claims 32 cores; this host's cap is 14"},
		{"an I/O job", Claim{CPUs: 2, MemoryGB: 2, IO: true}, ""},
		{"exclusive claims what the caps are", Claim{Exclusive: true}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckClaim(tc.claim, p)
			if tc.want == "" {
				assert.NoError(t, err)
				return
			}
			assert.EqualError(t, err, tc.want)
		})
	}
}
```

Create `sched/policy_test.go`:

```go
package sched

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aidan-bailey/loom/config"
)

// policyHost is a host with cores cores and ramBytes bytes of RAM.
func policyHost(cores int, ramBytes uint64) Host {
	return Host{Name: "test", Cores: cores, RAMBytes: ramBytes}
}

// policyFresco is the owner's host: 16 cores and 30.2 GB of RAM (a GB is
// 2^30 bytes).
var policyFresco = policyHost(16, 30<<30+(1<<30)/5)

// policyWant is the policy a host's defaults give: 2 I/O slots, a starvation
// wait of 10 minutes, 50 finished jobs kept, and the caps and default claim
// given.
func policyWant(cpus, memoryGB, claimCPUs, claimMemoryGB int) Policy {
	return Policy{
		Caps:         Caps{CPUs: cpus, MemoryGB: memoryGB, IOJobs: 2},
		DefaultClaim: Claim{CPUs: claimCPUs, MemoryGB: claimMemoryGB},
		StarveAfter:  10 * time.Minute,
		KeepFinished: 50,
	}
}

func TestResolve_Defaults(t *testing.T) {
	tests := []struct {
		name string
		host Host
		want Policy
	}{
		{"fresco: 14 cores, 22 GB, three default jobs at once", policyFresco, policyWant(14, 22, 4, 6)},
		{"a laptop", policyHost(8, 16<<30), policyWant(6, 12, 4, 6)},
		{"two cores and 1 GB: at least 1 of each, and a claim within them", policyHost(2, 1<<30), policyWant(1, 1, 1, 1)},
		{"one core", policyHost(1, 8<<30), policyWant(1, 6, 1, 6)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Resolve(config.SchedulerSettings{}, tc.host)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestResolve_Explicit(t *testing.T) {
	tests := []struct {
		name string
		in   config.SchedulerSettings
		want Policy
	}{
		{
			name: "the spec's example block",
			in: config.SchedulerSettings{
				CPUs: 14, MemoryGB: 24, IOJobs: 2,
				DefaultClaim:       &config.ClaimSettings{CPUs: 4, MemoryGB: 6},
				StarveAfterMinutes: 10, KeepFinished: 50,
			},
			want: policyWant(14, 24, 4, 6),
		},
		{
			name: "every field set",
			in: config.SchedulerSettings{
				CPUs: 4, MemoryGB: 8, IOJobs: 1,
				DefaultClaim:       &config.ClaimSettings{CPUs: 2, MemoryGB: 3},
				StarveAfterMinutes: 30, KeepFinished: 5,
			},
			want: Policy{
				Caps:         Caps{CPUs: 4, MemoryGB: 8, IOJobs: 1},
				DefaultClaim: Claim{CPUs: 2, MemoryGB: 3},
				StarveAfter:  30 * time.Minute,
				KeepFinished: 5,
			},
		},
		{
			name: "the largest values",
			in:   config.SchedulerSettings{CPUs: 1024, MemoryGB: 65536, IOJobs: 64, StarveAfterMinutes: 1440, KeepFinished: 10000},
			want: Policy{
				Caps:         Caps{CPUs: 1024, MemoryGB: 65536, IOJobs: 64},
				DefaultClaim: Claim{CPUs: 4, MemoryGB: 6},
				StarveAfter:  1440 * time.Minute,
				KeepFinished: 10000,
			},
		},
		{
			name: "the smallest values",
			in:   config.SchedulerSettings{CPUs: 1, MemoryGB: 1, IOJobs: 1, StarveAfterMinutes: 1, KeepFinished: 1},
			want: Policy{
				Caps:         Caps{CPUs: 1, MemoryGB: 1, IOJobs: 1},
				DefaultClaim: Claim{CPUs: 1, MemoryGB: 1},
				StarveAfter:  time.Minute,
				KeepFinished: 1,
			},
		},
		{
			name: "half a default claim: the other half takes its default",
			in:   config.SchedulerSettings{DefaultClaim: &config.ClaimSettings{CPUs: 8}},
			want: policyWant(14, 22, 8, 6),
		},
		{
			name: "a default claim that is all zeros is the default claim",
			in:   config.SchedulerSettings{DefaultClaim: &config.ClaimSettings{}},
			want: policyWant(14, 22, 4, 6),
		},
		{
			name: "the default claim's default follows a small cap",
			in:   config.SchedulerSettings{CPUs: 2, MemoryGB: 3},
			want: policyWant(2, 3, 2, 3),
		},
		{
			name: "a cap set above 75% of the RAM is the owner's call",
			in:   config.SchedulerSettings{MemoryGB: 30},
			want: policyWant(14, 30, 4, 6),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Resolve(tc.in, policyFresco)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestResolve_Bounds(t *testing.T) {
	tests := []struct {
		name string
		host Host // the zero Host is policyFresco
		in   config.SchedulerSettings
		want string
	}{
		{name: "cpus below 1", in: config.SchedulerSettings{CPUs: -1}, want: "scheduler.cpus is -1: it must be 1–1024"},
		{name: "cpus above 1024", in: config.SchedulerSettings{CPUs: 1025}, want: "scheduler.cpus is 1025: it must be 1–1024"},
		{name: "memory_gb below 1", in: config.SchedulerSettings{MemoryGB: -1}, want: "scheduler.memory_gb is -1: it must be 1–65536"},
		{name: "memory_gb above 65536", in: config.SchedulerSettings{MemoryGB: 65537}, want: "scheduler.memory_gb is 65537: it must be 1–65536"},
		{name: "io_jobs below 1", in: config.SchedulerSettings{IOJobs: -2}, want: "scheduler.io_jobs is -2: it must be 1–64"},
		{name: "io_jobs above 64", in: config.SchedulerSettings{IOJobs: 65}, want: "scheduler.io_jobs is 65: it must be 1–64"},
		{name: "starve_after_minutes below 1", in: config.SchedulerSettings{StarveAfterMinutes: -1}, want: "scheduler.starve_after_minutes is -1: it must be 1–1440"},
		{name: "starve_after_minutes above 1440", in: config.SchedulerSettings{StarveAfterMinutes: 1441}, want: "scheduler.starve_after_minutes is 1441: it must be 1–1440"},
		{name: "keep_finished below 1", in: config.SchedulerSettings{KeepFinished: -5}, want: "scheduler.keep_finished is -5: it must be 1–10000"},
		{name: "keep_finished above 10000", in: config.SchedulerSettings{KeepFinished: 10001}, want: "scheduler.keep_finished is 10001: it must be 1–10000"},
		{
			name: "a default claim above the default cap",
			in:   config.SchedulerSettings{DefaultClaim: &config.ClaimSettings{CPUs: 15}},
			want: "scheduler.default_claim.cpus is 15: it must be 1–14",
		},
		{
			name: "a default claim above the cap set",
			in:   config.SchedulerSettings{CPUs: 4, DefaultClaim: &config.ClaimSettings{CPUs: 5}},
			want: "scheduler.default_claim.cpus is 5: it must be 1–4",
		},
		{
			name: "a negative default claim",
			in:   config.SchedulerSettings{DefaultClaim: &config.ClaimSettings{CPUs: -2}},
			want: "scheduler.default_claim.cpus is -2: it must be 1–14",
		},
		{
			name: "a default claim's memory above the cap",
			in:   config.SchedulerSettings{DefaultClaim: &config.ClaimSettings{MemoryGB: 23}},
			want: "scheduler.default_claim.memory_gb is 23: it must be 1–22",
		},
		{
			name: "a negative default claim memory",
			in:   config.SchedulerSettings{DefaultClaim: &config.ClaimSettings{MemoryGB: -1}},
			want: "scheduler.default_claim.memory_gb is -1: it must be 1–22",
		},
		{
			name: "the first fault is the one reported",
			in:   config.SchedulerSettings{CPUs: -1, MemoryGB: -1},
			want: "scheduler.cpus is -1: it must be 1–1024",
		},
		{
			name: "a host whose default is out of bounds is invalid too: no cap is guessed",
			host: policyHost(2000, 64<<30),
			in:   config.SchedulerSettings{},
			want: "scheduler.cpus is 1998: it must be 1–1024",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			host := tc.host
			if host == (Host{}) {
				host = policyFresco
			}
			got, err := Resolve(tc.in, host)
			assert.EqualError(t, err, tc.want)
			assert.Equal(t, Policy{}, got, "an invalid block yields no policy")
		})
	}
}

func TestResolve_RAMUnreadable(t *testing.T) {
	host := policyHost(8, 0)

	got, err := Resolve(config.SchedulerSettings{}, host)
	assert.EqualError(t, err, "this host's RAM could not be read: set scheduler.memory_gb")
	assert.Equal(t, Policy{}, got)

	got, err = Resolve(config.SchedulerSettings{MemoryGB: 8}, host)
	require.NoError(t, err, "memory_gb set: the RAM is not needed")
	assert.Equal(t, policyWant(6, 8, 4, 6), got)
}

// TestResolve_LeavesTheSettingsAlone: Resolve reads the block it is given and
// changes nothing in it, the default claim included.
func TestResolve_LeavesTheSettingsAlone(t *testing.T) {
	s := config.SchedulerSettings{DefaultClaim: &config.ClaimSettings{}}
	_, err := Resolve(s, policyFresco)
	require.NoError(t, err)
	assert.Equal(t, config.SchedulerSettings{DefaultClaim: &config.ClaimSettings{}}, s)
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `CGO_ENABLED=0 go test ./sched/...`

Expected: the build fails (line numbers differ):

```
# github.com/aidan-bailey/loom/sched [github.com/aidan-bailey/loom/sched.test]
sched/job_test.go:NN:NN: undefined: Submit
sched/job_test.go:NN:NN: undefined: State
sched/job_test.go:NN:NN: undefined: Queued
...
FAIL	github.com/aidan-bailey/loom/sched [build failed]
```

- [ ] **Step 3: Implement**

Create `sched/doc.go`:

```go
// Package sched is the work scheduler's logic and its runtime: what a job, a
// claim and the host's caps are, first-fit admission with a starvation guard,
// the jobs.json store and the per-job files, and the processes that host a
// running job (the detached shim, the lock probe, group signals, the usage
// sampler and host detection).
//
// The model (package core) owns the queue. It persists it under
// <globalDir>/jobs/, calls [Admit] on its loop, and starts and watches jobs
// through this package. Nothing here keeps state of its own, so the logic
// takes the host, the policy and the clock as arguments.
package sched
```

Create `sched/job.go`:

```go
package sched

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// The bounds of a submission (spec §6.1), which Submit.Validate holds.
const (
	maxDescriptionChars = 200
	maxCommandBytes     = 64 << 10
	maxEnvBytes         = 1 << 20
	maxTail             = 200
)

// JobID names a job: per host, from 1, never reused.
type JobID int64

// State is where a job is in its life: queued, then running, then one of the
// four finished states.
type State string

// The states of a job. Done is exit 0; Failed is a non-zero exit, a signal or
// a start that failed; Cancelled is a job cancelled by anyone, whatever its
// exit; Lost is a job whose shim vanished without writing its result.
const (
	Queued    State = "queued"
	Running   State = "running"
	Done      State = "done"
	Failed    State = "failed"
	Cancelled State = "cancelled"
	Lost      State = "lost"
)

// Finished reports whether the job has ended: Done, Failed, Cancelled or Lost.
func (s State) Finished() bool {
	switch s {
	case Done, Failed, Cancelled, Lost:
		return true
	}
	return false
}

// Claim is what a job asks of the host: whole cores, whole GB, whether it is
// I/O-heavy, or everything.
type Claim struct {
	CPUs      int  `json:"cpus,omitempty"`
	MemoryGB  int  `json:"memory_gb,omitempty"`
	IO        bool `json:"io,omitempty"`
	Exclusive bool `json:"exclusive,omitempty"` // every cap; CPUs, MemoryGB and IO stay zero
}

// Caps are the host's totals that claims are admitted against.
type Caps struct {
	CPUs     int `json:"cpus"`
	MemoryGB int `json:"memory_gb"`
	IOJobs   int `json:"io_jobs"`
}

// Need is what a claim takes from the caps. An exclusive claim takes them all,
// as they stand.
type Need struct {
	CPUs     int `json:"cpus"`
	MemoryGB int `json:"memory_gb"`
	IOSlots  int `json:"io_slots"`
}

// Need returns what c takes from caps: the claimed cores and GB and one I/O
// slot for an I/O job, or every cap, as caps stand, for an exclusive claim.
func (c Claim) Need(caps Caps) Need {
	if c.Exclusive {
		return Need{CPUs: caps.CPUs, MemoryGB: caps.MemoryGB, IOSlots: caps.IOJobs}
	}
	n := Need{CPUs: c.CPUs, MemoryGB: c.MemoryGB}
	if c.IO {
		n.IOSlots = 1
	}
	return n
}

// Host is what the scheduler knows of the machine it runs on.
type Host struct {
	Name     string `json:"name"`
	Cores    int    `json:"cores"`
	RAMBytes uint64 `json:"ram_bytes"` // 0 when it could not be read
}

// Policy is the scheduler block resolved against the host: the caps and
// defaults admission runs with (Resolve).
type Policy struct {
	Caps         Caps          `json:"caps"`
	DefaultClaim Claim         `json:"default_claim"` // CPUs and MemoryGB set
	StarveAfter  time.Duration `json:"starve_after"`
	KeepFinished int           `json:"keep_finished"`
}

// Notice is the fate of a job's completion message to its owner's Claude.
type Notice string

// The fates of a notice: none due or yet, sent, or recorded as undelivered
// (with the reason in Record.NoticeWhy).
const (
	NoticeNone        Notice = ""
	NoticeSent        Notice = "sent"
	NoticeUndelivered Notice = "undelivered"
)

// Record is a job as jobs.json holds it: everything but its environment,
// which is in the job's spec.json.
type Record struct {
	ID           JobID     `json:"id"`
	Order        int64     `json:"order"`           // lower first; the ID at submit, the lowest queued Order − 1 on a move to the front
	Owner        uint64    `json:"owner,omitempty"` // a core.InstanceID; 0 for none
	Description  string    `json:"description"`
	Argv         []string  `json:"argv"`
	Cwd          string    `json:"cwd"`
	Claim        Claim     `json:"claim"` // the default claim already applied, except an exclusive one
	Notify       bool      `json:"notify,omitempty"`
	Tail         int       `json:"tail"`
	State        State     `json:"state"`
	Submitted    time.Time `json:"submitted"`
	Started      time.Time `json:"started,omitzero"`
	Ended        time.Time `json:"ended,omitzero"`
	PID          int       `json:"pid,omitempty"` // the shim's, from started.json
	Exit         *Exit     `json:"exit,omitempty"`
	Failure      string    `json:"failure,omitempty"` // why it failed to start, or why it was lost
	PeakRSSKB    int64     `json:"peak_rss_kb,omitempty"`
	CancelReason string    `json:"cancel_reason,omitempty"` // set when a cancel is requested
	SignalledAt  time.Time `json:"signalled_at,omitzero"`   // when SIGTERM went to its group
	Notice       Notice    `json:"notice,omitempty"`
	NoticeWhy    string    `json:"notice_why,omitempty"`
}

// Exit is exit.json: how the command ended, written by the shim when it
// does (its codec is in files.go; it is here because Record holds one).
type Exit struct {
	Code     int       `json:"code"`             // -1 for a signal
	Signal   string    `json:"signal,omitempty"` // "SIGKILL"
	Ended    time.Time `json:"ended"`
	MaxRSSKB int64     `json:"max_rss_kb,omitempty"`
	Error    string    `json:"error,omitempty"` // the exec failed; Code is then 127
}

// Submit is what a client sends to queue a job.
type Submit struct {
	Argv        []string `json:"argv"`
	Cwd         string   `json:"cwd"`
	Env         []string `json:"env"`
	Description string   `json:"description"`
	Claim       Claim    `json:"claim"` // CPUs or MemoryGB 0: the default claim's
	Notify      bool     `json:"notify"`
	Tail        int      `json:"tail"`
	Launch      string   `json:"launch"` // $LOOM_LAUNCH; "" from a plain shell
}

// totalBytes is the summed length of items.
func totalBytes(items []string) int {
	n := 0
	for _, s := range items {
		n += len(s)
	}
	return n
}

// Validate checks the bounds of spec §6.1, in this order, and returns the
// first fault: the trimmed description is 1–200 characters; the command is
// not empty and its arguments total at most 64 KiB; the environment totals at
// most 1 MiB; the tail is 0–200; the claim's cores and GB are not negative
// (0 takes the default claim's) and an exclusive claim names no other; the
// folder is absolute. It does not check that the folder exists, which the
// model does. It changes nothing: the caller stores the trimmed description.
func (s Submit) Validate() error {
	desc := strings.TrimSpace(s.Description)
	if desc == "" {
		return errors.New("the description is empty")
	}
	if n := utf8.RuneCountInString(desc); n > maxDescriptionChars {
		return fmt.Errorf("the description is %d characters: at most %d", n, maxDescriptionChars)
	}
	if len(s.Argv) == 0 {
		return errors.New("no command to run")
	}
	if n := totalBytes(s.Argv); n > maxCommandBytes {
		return fmt.Errorf("the command is %d bytes: at most %d", n, maxCommandBytes)
	}
	if n := totalBytes(s.Env); n > maxEnvBytes {
		return fmt.Errorf("the environment is %d bytes: at most %d", n, maxEnvBytes)
	}
	if s.Tail < 0 || s.Tail > maxTail {
		return fmt.Errorf("--tail is %d: it must be 0–%d", s.Tail, maxTail)
	}
	if s.Claim.CPUs < 0 {
		return fmt.Errorf("--cpus is %d: it must be at least 1", s.Claim.CPUs)
	}
	if s.Claim.MemoryGB < 0 {
		return fmt.Errorf("--mem is %d: it must be at least 1", s.Claim.MemoryGB)
	}
	if s.Claim.Exclusive && (s.Claim.CPUs != 0 || s.Claim.MemoryGB != 0 || s.Claim.IO) {
		return errors.New("--exclusive claims everything: drop --cpus, --mem and --io")
	}
	if !filepath.IsAbs(s.Cwd) {
		return fmt.Errorf("the folder %s is not an absolute path", s.Cwd)
	}
	return nil
}

// CheckClaim refuses a claim, its default already applied, that is over a cap
// of p: the cores first, then the GB. An exclusive claim is never over, and
// an I/O job takes one of at least one slot.
func CheckClaim(c Claim, p Policy) error {
	if c.Exclusive {
		return nil
	}
	if c.CPUs > p.Caps.CPUs {
		return fmt.Errorf("claims %d cores; this host's cap is %d", c.CPUs, p.Caps.CPUs)
	}
	if c.MemoryGB > p.Caps.MemoryGB {
		return fmt.Errorf("claims %d GB; this host's cap is %d GB", c.MemoryGB, p.Caps.MemoryGB)
	}
	return nil
}
```

Create `sched/policy.go`:

```go
package sched

import (
	"errors"
	"fmt"
	"time"

	"github.com/aidan-bailey/loom/config"
)

// resolveGB is a GB: 2^30 bytes throughout.
const resolveGB = 1 << 30

// The bounds of a scheduler block, once a 0 has taken its default (spec §5.1).
const (
	maxCapCPUs    = 1024
	maxCapMemory  = 65536
	maxIOJobs     = 64
	maxStarveMins = 1440
	maxKeep       = 10000
)

// resolveBounded returns v, or def when v is 0, and an error naming the
// scheduler key when the result is outside lo–hi.
func resolveBounded(key string, v, def, lo, hi int) (int, error) {
	if v == 0 {
		v = def
	}
	if v < lo || v > hi {
		return 0, fmt.Errorf("scheduler.%s is %d: it must be %d–%d", key, v, lo, hi)
	}
	return v, nil
}

// Resolve applies the host's defaults to the scheduler block and checks its
// bounds. A 0 or absent field takes its default: cpus is max(1, cores − 2);
// memory_gb is max(1, ⌊0.75 × RAM in GB⌋); io_jobs is 2; the default claim is
// {min(4, cpus), min(6, memory_gb)}, each half taking its own default;
// starve_after_minutes is 10; keep_finished is 50. It fails closed: a value
// outside its bounds (a negative one included), a default claim above a cap,
// or an unreadable RAM with memory_gb unset is an error and no Policy, since
// no cap is guessed. The first fault, in the order cpus, memory_gb, io_jobs,
// starve_after_minutes, keep_finished, default_claim.cpus,
// default_claim.memory_gb, is the one returned.
func Resolve(s config.SchedulerSettings, h Host) (Policy, error) {
	cpus, err := resolveBounded("cpus", s.CPUs, max(1, h.Cores-2), 1, maxCapCPUs)
	if err != nil {
		return Policy{}, err
	}
	memDefault := 0
	if s.MemoryGB == 0 {
		if h.RAMBytes == 0 {
			return Policy{}, errors.New("this host's RAM could not be read: set scheduler.memory_gb")
		}
		memDefault = max(1, int(h.RAMBytes*3/4/resolveGB))
	}
	mem, err := resolveBounded("memory_gb", s.MemoryGB, memDefault, 1, maxCapMemory)
	if err != nil {
		return Policy{}, err
	}
	ioJobs, err := resolveBounded("io_jobs", s.IOJobs, 2, 1, maxIOJobs)
	if err != nil {
		return Policy{}, err
	}
	starve, err := resolveBounded("starve_after_minutes", s.StarveAfterMinutes, 10, 1, maxStarveMins)
	if err != nil {
		return Policy{}, err
	}
	keep, err := resolveBounded("keep_finished", s.KeepFinished, 50, 1, maxKeep)
	if err != nil {
		return Policy{}, err
	}
	var claim config.ClaimSettings
	if s.DefaultClaim != nil {
		claim = *s.DefaultClaim
	}
	claimCPUs, err := resolveBounded("default_claim.cpus", claim.CPUs, min(4, cpus), 1, cpus)
	if err != nil {
		return Policy{}, err
	}
	claimMem, err := resolveBounded("default_claim.memory_gb", claim.MemoryGB, min(6, mem), 1, mem)
	if err != nil {
		return Policy{}, err
	}
	return Policy{
		Caps:         Caps{CPUs: cpus, MemoryGB: mem, IOJobs: ioJobs},
		DefaultClaim: Claim{CPUs: claimCPUs, MemoryGB: claimMem},
		StarveAfter:  time.Duration(starve) * time.Minute,
		KeepFinished: keep,
	}, nil
}
```

Create `sched/CLAUDE.md`:

```markdown
# sched

The work scheduler's package: the job, claim, caps and policy types, `Resolve` (the host's defaults and the bounds of the `scheduler` config block), `Admit` (first fit with a starvation guard, saying why each job waits), `Hints` (the parallelism variables), the submission checks, and the `jobs.json` store with the per-job files; and the runtime that hosts a running job (the detached `loom job-shim` process, the lock probe, group signals, the usage sampler and host detection). The model in `core` owns the queue and calls this package from its loop; the logic half runs no process, and nothing in the package keeps state of its own.

## Rules when modifying this package

- **Keep every bound of a submission in `Submit.Validate` and `CheckClaim`, with the texts written there.** The model runs them for every client, so the CLI, the work view and a script meet the same bounds; a client that checks its own lets them drift, and a reworded text silently stops matching the tests and the CLI output that quote it. **Enforced** for the bounds and texts by `TestSubmit_Validate`, `TestSubmit_ValidateReportsTheFirstFault` and `TestCheckClaim` (`sched/job_test.go`); **Convention** for other packages' matches — a test elsewhere that matches a stale text.
- **Make `Resolve` fail closed: a field outside its bounds, a default claim above a cap, and an unreadable RAM with `memory_gb` unset are errors, never a clamp or a guessed cap.** The model pauses admission on the error and the view says why; a clamped or guessed cap admits work the owner did not allow, or over-commits the host, and nothing says so. **Enforced** by `TestResolve_Bounds` and `TestResolve_RAMUnreadable` (`sched/policy_test.go`), and for the defaults by `TestResolve_Defaults`.

## Pointers

- [`../docs/ARCHITECTURE.md`](../docs/ARCHITECTURE.md) — where this package sits.
- [`../config/CLAUDE.md`](../config/CLAUDE.md) — the settings package that holds the `scheduler` block `Resolve` reads.
```

- [ ] **Step 4: Run it to see it pass**

Run:

```
CGO_ENABLED=0 go test ./sched/... ./config/... ./tools/claudemd/... ./internal/testenv/...
gofmt -l sched config
CGO_ENABLED=0 go vet ./sched/... ./config/...
```

Expected: `ok  	github.com/aidan-bailey/loom/sched	…`, and `ok` for `config`, `tools/claudemd` and `internal/testenv` (the last two prove `sched/CLAUDE.md` is well formed, covers the package, and that `sched`'s `TestMain` isolates the loom dirs); `gofmt -l` and `go vet` print nothing.

- [ ] **Step 5: Commit**

```
git add -N sched/doc.go sched/job.go sched/job_test.go sched/policy.go sched/policy_test.go sched/testmain_test.go sched/CLAUDE.md
git commit -m "feat(sched): job types, submission checks and the host policy" -- sched/doc.go sched/job.go sched/job_test.go sched/policy.go sched/policy_test.go sched/testmain_test.go sched/CLAUDE.md
```

### Task 1.3: First-fit admission and the parallelism hints

**Files:**
- Create: `sched/admit.go`
- Create: `sched/hints.go`
- Modify: `sched/CLAUDE.md` (three rules at the end of the rules section)
- Test: `sched/admit_test.go`, `sched/hints_test.go`

- [ ] **Step 1: Write the failing tests**

Create `sched/admit_test.go`:

```go
package sched

import (
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// admitNow is the clock of every admission test.
var admitNow = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

// Claims the admission tests share, against admitPolicy's 14 cores and 22 GB.
var (
	admitDefault = Claim{CPUs: 4, MemoryGB: 6}
	admitSmall   = Claim{CPUs: 2, MemoryGB: 2}
	admitBig     = Claim{CPUs: 10, MemoryGB: 12}
)

// admitPolicy is fresco's policy: 14 cores, 22 GB, 2 I/O slots, a default
// claim of 4 cores and 6 GB, and a starvation wait of 10 minutes.
func admitPolicy() Policy {
	return Policy{
		Caps:         Caps{CPUs: 14, MemoryGB: 22, IOJobs: 2},
		DefaultClaim: admitDefault,
		StarveAfter:  10 * time.Minute,
		KeepFinished: 50,
	}
}

// queuedJob is a queued job that was submitted waited ago. Its Order is its
// ID, as at submit.
func queuedJob(id JobID, claim Claim, waited time.Duration) Record {
	return Record{ID: id, Order: int64(id), State: Queued, Claim: claim, Submitted: admitNow.Add(-waited)}
}

// runningJob is a job that started a minute ago.
func runningJob(id JobID, claim Claim) Record {
	return Record{
		ID: id, Order: int64(id), State: Running, Claim: claim,
		Submitted: admitNow.Add(-time.Hour), Started: admitNow.Add(-time.Minute),
	}
}

// withOrder is r with the Order of a job moved to the front of the queue.
func withOrder(r Record, order int64) Record {
	r.Order = order
	return r
}

// admitCase is one row of an admission table.
type admitCase struct {
	name    string
	jobs    []Record
	edit    func(*Policy) // nil: admitPolicy as it is
	start   []JobID
	waiting map[JobID]string
}

func runAdmitCases(t *testing.T, cases []admitCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := admitPolicy()
			if tc.edit != nil {
				tc.edit(&p)
			}
			got := Admit(tc.jobs, p, admitNow)
			assert.Equal(t, tc.start, got.Start, "the jobs to start, in order")
			want := tc.waiting
			if want == nil {
				want = map[JobID]string{}
			}
			assert.Equal(t, want, got.Waiting, "why the others wait")
		})
	}
}

func TestAdmit_FirstFit(t *testing.T) {
	runAdmitCases(t, []admitCase{
		{name: "an empty host starts nothing and waits for nothing"},
		{
			name: "first fit starts every job that fits, in queue order",
			jobs: []Record{
				queuedJob(1, admitDefault, time.Minute),
				queuedJob(2, admitDefault, time.Minute),
				queuedJob(3, admitDefault, time.Minute),
				queuedJob(4, admitDefault, time.Minute),
			},
			start:   []JobID{1, 2, 3},
			waiting: map[JobID]string{4: "needs 4 cores (2 free)"},
		},
		{
			name: "running jobs count against the caps",
			jobs: []Record{
				runningJob(1, admitDefault),
				runningJob(2, admitDefault),
				queuedJob(3, admitDefault, time.Minute),
				queuedJob(4, admitDefault, time.Minute),
			},
			start:   []JobID{3},
			waiting: map[JobID]string{4: "needs 4 cores (2 free)"},
		},
		{
			name: "memory is checked after cores",
			jobs: []Record{
				runningJob(1, Claim{CPUs: 2, MemoryGB: 16}),
				queuedJob(2, Claim{CPUs: 2, MemoryGB: 8}, time.Minute),
			},
			waiting: map[JobID]string{2: "needs 8 GB (6 free)"},
		},
		{
			name: "an I/O job waits for a slot, and a job without I/O passes it",
			jobs: []Record{
				runningJob(1, Claim{CPUs: 2, MemoryGB: 2, IO: true}),
				runningJob(2, Claim{CPUs: 2, MemoryGB: 2, IO: true}),
				queuedJob(3, Claim{CPUs: 2, MemoryGB: 2, IO: true}, time.Minute),
				queuedJob(4, admitSmall, time.Minute),
			},
			start:   []JobID{4},
			waiting: map[JobID]string{3: "needs an I/O slot"},
		},
		{
			name: "a small job passes a large one that has not yet waited long enough to count",
			jobs: []Record{
				runningJob(1, Claim{CPUs: 6, MemoryGB: 6}),
				queuedJob(2, admitBig, 10*time.Minute-time.Second),
				queuedJob(3, admitSmall, time.Minute),
			},
			start:   []JobID{3},
			waiting: map[JobID]string{2: "needs 10 cores (8 free)"},
		},
	})
}

func TestAdmit_StarvationGuard(t *testing.T) {
	runAdmitCases(t, []admitCase{
		{
			name: "a head that has waited the starvation time and does not fit holds the rest back",
			jobs: []Record{
				runningJob(1, Claim{CPUs: 6, MemoryGB: 6}),
				queuedJob(2, admitBig, 10*time.Minute),
				queuedJob(3, admitSmall, time.Minute),
			},
			waiting: map[JobID]string{
				2: "needs 10 cores (8 free)",
				3: "waits for job 2, which has waited past 10m",
			},
		},
		{
			name: "a starved head that fits starts alone",
			jobs: []Record{
				queuedJob(1, admitBig, 11*time.Minute),
				queuedJob(2, admitSmall, time.Minute),
			},
			start:   []JobID{1},
			waiting: map[JobID]string{2: "waits for job 1, which has waited past 10m"},
		},
		{
			name: "the head is the first job that is not over a cap",
			jobs: []Record{
				runningJob(5, Claim{CPUs: 6, MemoryGB: 6}),
				queuedJob(1, Claim{CPUs: 32, MemoryGB: 2}, 2*time.Hour),
				queuedJob(2, admitBig, 11*time.Minute),
				queuedJob(3, admitSmall, time.Minute),
			},
			waiting: map[JobID]string{
				1: "over this host's caps",
				2: "needs 10 cores (8 free)",
				3: "waits for job 2, which has waited past 10m",
			},
		},
		{
			name: "the starvation time is the policy's: 20 minutes is not enough for 30",
			edit: func(p *Policy) { p.StarveAfter = 30 * time.Minute },
			jobs: []Record{
				runningJob(1, Claim{CPUs: 6, MemoryGB: 6}),
				queuedJob(2, admitBig, 20*time.Minute),
				queuedJob(3, admitSmall, time.Minute),
			},
			start:   []JobID{3},
			waiting: map[JobID]string{2: "needs 10 cores (8 free)"},
		},
		{
			name: "the starvation time is the policy's: 30 minutes is enough, and the text says 30m",
			edit: func(p *Policy) { p.StarveAfter = 30 * time.Minute },
			jobs: []Record{
				runningJob(1, Claim{CPUs: 6, MemoryGB: 6}),
				queuedJob(2, admitBig, 30*time.Minute),
				queuedJob(3, admitSmall, time.Minute),
			},
			waiting: map[JobID]string{
				2: "needs 10 cores (8 free)",
				3: "waits for job 2, which has waited past 30m",
			},
		},
	})
}

func TestAdmit_OverCapJobsAreSkipped(t *testing.T) {
	over := "over this host's caps"
	runAdmitCases(t, []admitCase{
		{
			name: "an over-cap job is skipped and never the head, however long it has waited",
			jobs: []Record{
				queuedJob(1, Claim{CPUs: 32, MemoryGB: 2}, 2*time.Hour),
				queuedJob(2, admitBig, time.Minute),
				queuedJob(3, admitSmall, time.Minute),
				queuedJob(4, Claim{CPUs: 2, MemoryGB: 23}, 2*time.Hour),
			},
			start:   []JobID{2, 3},
			waiting: map[JobID]string{1: over, 4: over},
		},
		{
			name:  "a claim at a cap is not over it",
			jobs:  []Record{queuedJob(1, Claim{CPUs: 14, MemoryGB: 22}, time.Minute)},
			start: []JobID{1},
		},
		{
			name: "a lowered cap makes a waiting job over it",
			edit: func(p *Policy) { p.Caps.MemoryGB = 4 },
			jobs: []Record{
				queuedJob(1, admitDefault, time.Minute),
				queuedJob(2, admitSmall, time.Minute),
			},
			start:   []JobID{2},
			waiting: map[JobID]string{1: over},
		},
		{
			name: "a zero policy admits nothing: even an exclusive job is over its caps",
			edit: func(p *Policy) { *p = Policy{} },
			jobs: []Record{
				queuedJob(1, admitDefault, time.Minute),
				queuedJob(2, Claim{Exclusive: true}, time.Minute),
			},
			waiting: map[JobID]string{1: over, 2: over},
		},
	})
}

func TestAdmit_ExclusiveClaims(t *testing.T) {
	runAdmitCases(t, []admitCase{
		{
			name: "an exclusive job waits for the host to empty, and first fit passes it",
			jobs: []Record{
				runningJob(1, admitDefault),
				queuedJob(2, Claim{Exclusive: true}, time.Minute),
				queuedJob(3, admitSmall, time.Minute),
			},
			start:   []JobID{3},
			waiting: map[JobID]string{2: "needs 14 cores (10 free)"},
		},
		{
			name: "an exclusive job starts on an empty host and takes every cap",
			jobs: []Record{
				queuedJob(1, Claim{Exclusive: true}, time.Minute),
				queuedJob(2, admitSmall, time.Minute),
			},
			start:   []JobID{1},
			waiting: map[JobID]string{2: "needs 2 cores (0 free)"},
		},
		{
			name: "a running exclusive job leaves nothing free",
			jobs: []Record{
				runningJob(1, Claim{Exclusive: true}),
				queuedJob(2, admitSmall, time.Minute),
			},
			waiting: map[JobID]string{2: "needs 2 cores (0 free)"},
		},
		{
			name: "an exclusive job that waits for I/O slots says how many",
			jobs: []Record{
				runningJob(1, Claim{IO: true}),
				queuedJob(2, Claim{Exclusive: true}, time.Minute),
			},
			waiting: map[JobID]string{2: "needs 2 I/O slots (1 free)"},
		},
		{
			name: "with one I/O slot an exclusive job needs an I/O slot",
			edit: func(p *Policy) { p.Caps.IOJobs = 1 },
			jobs: []Record{
				runningJob(1, Claim{IO: true}),
				queuedJob(2, Claim{Exclusive: true}, time.Minute),
			},
			waiting: map[JobID]string{2: "needs an I/O slot"},
		},
	})
}

func TestAdmit_CapEditsAndQueueOrder(t *testing.T) {
	runAdmitCases(t, []admitCase{
		{
			name: "a lowered cap leaves running jobs alone and nothing free",
			edit: func(p *Policy) { p.Caps.CPUs = 8 },
			jobs: []Record{
				runningJob(1, admitDefault),
				runningJob(2, admitDefault),
				runningJob(3, admitDefault),
				queuedJob(4, admitSmall, time.Minute),
			},
			waiting: map[JobID]string{4: "needs 2 cores (0 free)"},
		},
		{
			name: "a raised cap admits what was waiting",
			edit: func(p *Policy) { p.Caps.CPUs, p.Caps.MemoryGB = 16, 24 },
			jobs: []Record{
				runningJob(1, admitDefault),
				runningJob(2, admitDefault),
				runningJob(3, admitDefault),
				queuedJob(4, admitDefault, time.Minute),
			},
			start: []JobID{4},
		},
		{
			name: "a job moved to the front is first in line",
			edit: func(p *Policy) { p.Caps.CPUs = 4 },
			jobs: []Record{
				queuedJob(1, Claim{CPUs: 4, MemoryGB: 2}, time.Minute),
				withOrder(queuedJob(2, Claim{CPUs: 4, MemoryGB: 2}, time.Minute), 0),
			},
			start:   []JobID{2},
			waiting: map[JobID]string{1: "needs 4 cores (0 free)"},
		},
		{
			name: "finished jobs claim nothing",
			jobs: []Record{
				{ID: 1, State: Done, Claim: Claim{Exclusive: true}},
				{ID: 2, State: Failed, Claim: Claim{CPUs: 14, MemoryGB: 22}},
				{ID: 3, State: Cancelled, Claim: Claim{CPUs: 14, MemoryGB: 22}},
				{ID: 4, State: Lost, Claim: Claim{CPUs: 14, MemoryGB: 22}},
				queuedJob(5, admitDefault, time.Minute),
			},
			start: []JobID{5},
		},
	})
}

// TestAdmit_LeavesItsInputAlone: Admit sorts a copy of the queue, so a caller
// that holds the records in their own order sees them unchanged.
func TestAdmit_LeavesItsInputAlone(t *testing.T) {
	jobs := []Record{
		queuedJob(1, admitDefault, time.Minute),
		withOrder(queuedJob(2, admitDefault, time.Minute), 0),
		runningJob(3, admitSmall),
	}
	before := slices.Clone(jobs)
	_ = Admit(jobs, admitPolicy(), admitNow)
	assert.Equal(t, before, jobs)
}
```

Create `sched/hints_test.go`:

```go
package sched

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHints_AddsWhatTheEnvironmentLacks(t *testing.T) {
	got := Hints([]string{"PATH=/bin", "HOME=/home/me"}, 4, 14)
	assert.Equal(t, []string{
		"PATH=/bin",
		"HOME=/home/me",
		"CARGO_BUILD_JOBS=4",
		"RUST_TEST_THREADS=4",
		"MAKEFLAGS=-j4",
		"NIX_BUILD_CORES=4",
		"CMAKE_BUILD_PARALLEL_LEVEL=4",
		"GOFLAGS=-p=4",
		"LOOM_JOB=14",
	}, got)

	assert.Len(t, Hints(nil, 2, 1), 7, "an empty environment gets the six hints and LOOM_JOB")
}

// TestHints_KeepsWhatTheSubmitterSet: a variable the submitter's environment
// already sets keeps its value, an empty one included, and is not repeated; a
// name that only starts like a hint is no hint.
func TestHints_KeepsWhatTheSubmitterSet(t *testing.T) {
	env := []string{"CARGO_BUILD_JOBS=2", "GOFLAGS=-mod=vendor", "MAKEFLAGS=", "CARGO_BUILD_JOBS_EXTRA=1"}
	got := Hints(env, 8, 3)
	assert.Equal(t, []string{
		"CARGO_BUILD_JOBS=2",
		"GOFLAGS=-mod=vendor",
		"MAKEFLAGS=",
		"CARGO_BUILD_JOBS_EXTRA=1",
		"RUST_TEST_THREADS=8",
		"NIX_BUILD_CORES=8",
		"CMAKE_BUILD_PARALLEL_LEVEL=8",
		"LOOM_JOB=3",
	}, got)
}

// TestHints_ReplacesAnInheritedLoomJob: a job submitted from inside a job
// inherits its LOOM_JOB, and the new job's own replaces it, wherever it sat.
func TestHints_ReplacesAnInheritedLoomJob(t *testing.T) {
	got := Hints([]string{"A=1", "LOOM_JOB=9", "B=2"}, 2, 10)
	assert.Equal(t, []string{"A=1", "B=2"}, got[:2])
	assert.Equal(t, "LOOM_JOB=10", got[len(got)-1])
	loomJobs := 0
	for _, e := range got {
		if strings.HasPrefix(e, "LOOM_JOB=") {
			loomJobs++
		}
	}
	assert.Equal(t, 1, loomJobs, "exactly one LOOM_JOB")
}

// TestHints_LeavesItsInputAlone: the result is a new slice, and Hints writes
// nothing into the caller's spare capacity.
func TestHints_LeavesItsInputAlone(t *testing.T) {
	env := make([]string, 2, 16)
	env[0], env[1] = "A=1", "LOOM_JOB=9"
	before := slices.Clone(env)
	_ = Hints(env, 2, 3)
	assert.Equal(t, before, env)
	for _, spare := range env[len(env):cap(env)] {
		assert.Empty(t, spare, "Hints wrote into the caller's spare capacity")
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `CGO_ENABLED=0 go test ./sched/... -run 'Admit|Hints'`

Expected: the build fails (line numbers differ):

```
# github.com/aidan-bailey/loom/sched [github.com/aidan-bailey/loom/sched.test]
sched/admit_test.go:NN:NN: undefined: Admit
sched/hints_test.go:NN:NN: undefined: Hints
FAIL	github.com/aidan-bailey/loom/sched [build failed]
```

- [ ] **Step 3: Implement**

Create `sched/admit.go`:

```go
package sched

import (
	"cmp"
	"fmt"
	"slices"
	"time"
)

// waitOver is why a job whose claim exceeds a cap waits: it never will start
// at these caps.
const waitOver = "over this host's caps"

// Decision is the outcome of one admission pass.
type Decision struct {
	Start   []JobID          // in the order to start them; nil when none
	Waiting map[JobID]string // why each queued job left waiting waits; never nil
}

// plus is the sum of two needs.
func (n Need) plus(o Need) Need {
	return Need{CPUs: n.CPUs + o.CPUs, MemoryGB: n.MemoryGB + o.MemoryGB, IOSlots: n.IOSlots + o.IOSlots}
}

// exceeds reports whether c is over a cap of caps, so that it can never fit
// however empty the host is. An exclusive claim is never over.
func (c Claim) exceeds(caps Caps) bool {
	if c.Exclusive {
		return false
	}
	return c.CPUs > caps.CPUs || c.MemoryGB > caps.MemoryGB
}

// usable reports whether p has caps to admit against. The zero Policy, which
// the model holds while admission is paused, has none.
func (p Policy) usable() bool {
	return p.Caps.CPUs >= 1 && p.Caps.MemoryGB >= 1 && p.Caps.IOJobs >= 1
}

// shortfall says why need does not fit what used leaves of caps, checking
// the cores, then the GB, then the I/O slots, or returns "" when it fits.
// What is free is never shown below 0: lowering a cap leaves the running
// jobs over it.
func shortfall(used, need Need, caps Caps) string {
	switch {
	case used.CPUs+need.CPUs > caps.CPUs:
		return fmt.Sprintf("needs %d cores (%d free)", need.CPUs, max(0, caps.CPUs-used.CPUs))
	case used.MemoryGB+need.MemoryGB > caps.MemoryGB:
		return fmt.Sprintf("needs %d GB (%d free)", need.MemoryGB, max(0, caps.MemoryGB-used.MemoryGB))
	case used.IOSlots+need.IOSlots > caps.IOJobs:
		if need.IOSlots == 1 {
			return "needs an I/O slot"
		}
		return fmt.Sprintf("needs %d I/O slots (%d free)", need.IOSlots, max(0, caps.IOJobs-used.IOSlots))
	}
	return ""
}

// Admit decides which queued jobs start now (spec §5.2). It is a pure
// function of its arguments. It walks the queued jobs in queue order (Order,
// then ID):
//
//  1. A job whose claim exceeds a cap is skipped and waits "over this host's
//     caps"; it is never the head.
//  2. The head is the first job that is not skipped. Once it has waited
//     p.StarveAfter since it was submitted, Admit starts it if it fits and
//     starts nothing else, so smaller jobs cannot pass a large one forever.
//  3. Otherwise (first fit) it starts every job, in order, whose claim fits
//     what the running jobs and the jobs started before it leave.
//
// Each job left waiting gets its reason in Decision.Waiting. Only Queued and
// Running records count; a Running exclusive claim takes every cap as it
// stands. A policy without caps (the zero Policy) starts nothing. The jobs
// slice is left as it is.
func Admit(jobs []Record, p Policy, now time.Time) Decision {
	d := Decision{Waiting: map[JobID]string{}}
	var used Need
	var queued []Record
	for _, r := range jobs {
		switch r.State {
		case Running:
			used = used.plus(r.Claim.Need(p.Caps))
		case Queued:
			queued = append(queued, r)
		}
	}
	slices.SortFunc(queued, func(a, b Record) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), cmp.Compare(a.ID, b.ID))
	})

	var line []Record // the jobs that can ever run, in queue order
	for _, r := range queued {
		if !p.usable() || r.Claim.exceeds(p.Caps) {
			d.Waiting[r.ID] = waitOver
			continue
		}
		line = append(line, r)
	}
	if len(line) == 0 {
		return d
	}

	if head := line[0]; now.Sub(head.Submitted) >= p.StarveAfter {
		if why := shortfall(used, head.Claim.Need(p.Caps), p.Caps); why == "" {
			d.Start = append(d.Start, head.ID)
		} else {
			d.Waiting[head.ID] = why
		}
		for _, r := range line[1:] {
			d.Waiting[r.ID] = fmt.Sprintf("waits for job %d, which has waited past %dm", head.ID, int(p.StarveAfter/time.Minute))
		}
		return d
	}

	for _, r := range line {
		need := r.Claim.Need(p.Caps)
		if why := shortfall(used, need, p.Caps); why != "" {
			d.Waiting[r.ID] = why
			continue
		}
		used = used.plus(need)
		d.Start = append(d.Start, r.ID)
	}
	return d
}
```

Create `sched/hints.go`:

```go
package sched

import (
	"strconv"
	"strings"
)

// jobIDVar names the variable that tells a job its own ID.
const jobIDVar = "LOOM_JOB"

// Hints returns a new environment: env with the parallelism variables it
// lacks added, each set to cpus unless env already sets it (to anything, an
// empty value included), and LOOM_JOB set to id, replacing one a job
// submitted from inside another job inherited. The hints are
// CARGO_BUILD_JOBS=N, RUST_TEST_THREADS=N, MAKEFLAGS=-jN, NIX_BUILD_CORES=N,
// CMAKE_BUILD_PARALLEL_LEVEL=N and GOFLAGS=-p=N. env is left as it is.
func Hints(env []string, cpus int, id JobID) []string {
	n := strconv.Itoa(cpus)
	hints := []struct{ name, value string }{
		{"CARGO_BUILD_JOBS", n},
		{"RUST_TEST_THREADS", n},
		{"MAKEFLAGS", "-j" + n},
		{"NIX_BUILD_CORES", n},
		{"CMAKE_BUILD_PARALLEL_LEVEL", n},
		{"GOFLAGS", "-p=" + n},
	}
	out := make([]string, 0, len(env)+len(hints)+1)
	set := make(map[string]bool, len(env))
	for _, e := range env {
		name, _, _ := strings.Cut(e, "=")
		if name == jobIDVar {
			continue
		}
		set[name] = true
		out = append(out, e)
	}
	for _, h := range hints {
		if !set[h.name] {
			out = append(out, h.name+"="+h.value)
		}
	}
	return append(out, jobIDVar+"="+strconv.FormatInt(int64(id), 10))
}
```

In `sched/CLAUDE.md`, add these three bullets after the last bullet of `## Rules when modifying this package` (the `Resolve` rule), before the blank line and `## Pointers`:

```markdown
- **Keep `Admit` a pure function of its arguments, and never let it start a job that does not fit what the running jobs leave.** The model calls it on its loop with the records, the policy and the time, so a clock, file or process inside it blocks every client, and a pass that overshoots a cap is the memory exhaustion the queue exists to prevent. **Enforced** for the fit by `TestAdmit_FirstFit`, `TestAdmit_ExclusiveClaims` and `TestAdmit_CapEditsAndQueueOrder` (`sched/admit_test.go`); **Convention** for the purity — a pass that blocks the loop, or a table that passes on one machine only.
- **Keep the starvation guard and the over-cap skip: an over-cap job is never the head, and a head that has waited `StarveAfter` starts alone or holds the rest back.** Without the guard smaller jobs pass a large one forever; with an over-cap head the whole queue stalls behind a job that can never run, and the view shows a wait nobody can end. **Enforced** by `TestAdmit_StarvationGuard` and `TestAdmit_OverCapJobsAreSkipped`.
- **Make `Hints` add only what the submitter's environment lacks, and always set `LOOM_JOB`.** A job that set its own parallelism keeps it, and a `LOOM_JOB` inherited from an outer job would name the wrong job to `loom job`. **Enforced** by `TestHints_KeepsWhatTheSubmitterSet` and `TestHints_ReplacesAnInheritedLoomJob` (`sched/hints_test.go`).
```

- [ ] **Step 4: Run it to see it pass**

Run:

```
CGO_ENABLED=0 go test ./sched/... ./tools/claudemd/...
gofmt -l sched
CGO_ENABLED=0 go vet ./sched/...
```

Expected: `ok  	github.com/aidan-bailey/loom/sched	…` and `ok  	github.com/aidan-bailey/loom/tools/claudemd	…`; `gofmt -l` and `go vet` print nothing.

- [ ] **Step 5: Commit**

```
git add -N sched/admit.go sched/admit_test.go sched/hints.go sched/hints_test.go
git commit -m "feat(sched): first-fit admission and the parallelism hints" -- sched/admit.go sched/admit_test.go sched/hints.go sched/hints_test.go sched/CLAUDE.md
```

### Task 1.4: The `jobs.json` store and the per-job files

**Files:**
- Create: `sched/files.go`
- Create: `sched/store.go`
- Modify: `sched/CLAUDE.md` (three rules at the end of the rules section)
- Test: `sched/files_test.go`, `sched/store_test.go`

- [ ] **Step 1: Write the failing tests**

Create `sched/files_test.go`:

```go
package sched

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeFixtureFile creates path, and its parents, holding "x".
func writeFixtureFile(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o600))
}

// assertJobFileMode checks path's permission bits; Windows has none to check.
func assertJobFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, want, info.Mode().Perm(), path)
}

func TestJobDir_IsTheDecimalID(t *testing.T) {
	assert.Equal(t, filepath.Join("/x/jobs", "14"), JobDir("/x/jobs", 14))
	assert.Equal(t, filepath.Join("/x/jobs", "1000000"), JobDir("/x/jobs", 1000000))
}

func TestRemoveJobDir_RemovesOnlyThatJobsDir(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"14/spec.json", "14/output.log", "14/sub/deep.txt", "140/spec.json", "1/spec.json", "jobs.json"} {
		writeFixtureFile(t, filepath.Join(dir, p))
	}
	require.NoError(t, RemoveJobDir(dir, 14))
	assert.NoDirExists(t, filepath.Join(dir, "14"))
	for _, p := range []string{"140/spec.json", "1/spec.json", "jobs.json"} {
		assert.FileExists(t, filepath.Join(dir, p), "RemoveJobDir took %s with it", p)
	}
	assert.NoError(t, RemoveJobDir(dir, 14), "a job directory that is already gone is no error")
}

// TestRemoveJobDir_DoesNotFollowLinks: a link inside a job directory is
// removed, never what it points at.
func TestRemoveJobDir_DoesNotFollowLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating links needs privileges on Windows")
	}
	dir := t.TempDir()
	outsideFile := filepath.Join(t.TempDir(), "keep.txt")
	writeFixtureFile(t, outsideFile)
	outsideTree := filepath.Join(t.TempDir(), "tree")
	writeFixtureFile(t, filepath.Join(outsideTree, "keep.txt"))

	require.NoError(t, os.MkdirAll(JobDir(dir, 9), 0o700))
	require.NoError(t, os.Symlink(outsideFile, filepath.Join(JobDir(dir, 9), "output.log")))
	require.NoError(t, os.Symlink(outsideTree, filepath.Join(JobDir(dir, 9), "tree")))

	require.NoError(t, RemoveJobDir(dir, 9))
	assert.NoDirExists(t, JobDir(dir, 9))
	assert.FileExists(t, outsideFile)
	assert.FileExists(t, filepath.Join(outsideTree, "keep.txt"))
}

// TestRemoveJobDir_RefusesWhatItCannotProve: a dir that is empty, relative or
// not clean, and an ID below 1, are refused, and nothing is removed.
func TestRemoveJobDir_RefusesWhatItCannotProve(t *testing.T) {
	t.Run("an empty or relative dir would remove under the working directory", func(t *testing.T) {
		t.Chdir(t.TempDir())
		writeFixtureFile(t, "7/keep")
		writeFixtureFile(t, "jobs/7/keep")
		for _, dir := range []string{"", "jobs"} {
			assert.Error(t, RemoveJobDir(dir, 7), "dir %q", dir)
		}
		assert.FileExists(t, "7/keep")
		assert.FileExists(t, "jobs/7/keep")
	})

	t.Run("an ID below 1", func(t *testing.T) {
		dir := t.TempDir()
		writeFixtureFile(t, filepath.Join(dir, "0", "keep"))
		writeFixtureFile(t, filepath.Join(dir, "-3", "keep"))
		assert.Error(t, RemoveJobDir(dir, 0))
		assert.Error(t, RemoveJobDir(dir, -3))
		assert.FileExists(t, filepath.Join(dir, "0", "keep"))
		assert.FileExists(t, filepath.Join(dir, "-3", "keep"))
	})

	t.Run("a dir the kernel resolves differently from its text", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("creating links needs privileges on Windows")
		}
		root := t.TempDir()
		realDir := filepath.Join(root, "real")
		require.NoError(t, os.MkdirAll(filepath.Join(realDir, "sub"), 0o700))
		require.NoError(t, os.Symlink(filepath.Join(realDir, "sub"), filepath.Join(root, "lnk")))
		// Lexically root/lnk/../jobs is root/jobs; the kernel follows lnk
		// first and reaches real/jobs.
		lexical := filepath.Join(root, "jobs", "5", "keep")
		kernel := filepath.Join(realDir, "jobs", "5", "keep")
		writeFixtureFile(t, lexical)
		writeFixtureFile(t, kernel)

		assert.Error(t, RemoveJobDir(root+"/lnk/../jobs", 5))
		assert.FileExists(t, lexical)
		assert.FileExists(t, kernel)
	})
}

// removeAllCallers returns, for each use of os.RemoveAll in src (a call or a
// reference), the name of the function that holds it, or "" at package level.
// It resolves the file's own name for os, an alias or a dot import included.
func removeAllCallers(t *testing.T, name, src string) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), name, src, 0)
	require.NoError(t, err)
	osName := ""
	for _, imp := range f.Imports {
		if imp.Path.Value != `"os"` {
			continue
		}
		osName = "os"
		if imp.Name != nil {
			osName = imp.Name.Name
		}
	}
	if osName == "" || osName == "_" {
		return nil
	}
	var callers []string
	for _, decl := range f.Decls {
		enclosing := ""
		if fn, ok := decl.(*ast.FuncDecl); ok {
			enclosing = fn.Name.Name
		}
		ast.Inspect(decl, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.SelectorExpr:
				if id, ok := x.X.(*ast.Ident); ok && id.Name == osName && x.Sel.Name == "RemoveAll" {
					callers = append(callers, enclosing)
				}
			case *ast.Ident:
				if osName == "." && x.Name == "RemoveAll" {
					callers = append(callers, enclosing)
				}
			}
			return true
		})
	}
	return callers
}

// TestRemoveAllCallers_Detection pins removeAllCallers, so that the scan
// below cannot turn into a test that never fails.
func TestRemoveAllCallers_Detection(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{"a call", "package p\nimport \"os\"\nfunc RemoveJobDir() { os.RemoveAll(\"x\") }\n", []string{"RemoveJobDir"}},
		{"an aliased import", "package p\nimport o \"os\"\nfunc f() { o.RemoveAll(\"x\") }\n", []string{"f"}},
		{"a dot import", "package p\nimport . \"os\"\nfunc g() { RemoveAll(\"x\") }\n", []string{"g"}},
		{"a reference that is no call", "package p\nimport \"os\"\nvar rm = os.RemoveAll\n", []string{""}},
		{"inside a closure", "package p\nimport \"os\"\nfunc h() { go func() { os.RemoveAll(\"x\") }() }\n", []string{"h"}},
		{"another os function", "package p\nimport \"os\"\nfunc f() { os.Remove(\"x\") }\n", nil},
		{"no os import", "package p\nfunc f(os fs) { os.RemoveAll(\"x\") }\n", nil},
		{"a blank import", "package p\nimport _ \"os\"\nfunc f() {}\n", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.ElementsMatch(t, tc.want, removeAllCallers(t, "src.go", tc.src))
		})
	}
}

// TestSched_RemoveAllIsOnlyInRemoveJobDir: no non-test file of the package
// uses os.RemoveAll but RemoveJobDir, which builds its path from a job ID.
// The scan must find that one use, so a scan of nothing fails.
func TestSched_RemoveAllIsOnlyInRemoveJobDir(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	var found []string
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		require.NoError(t, err)
		for _, fn := range removeAllCallers(t, name, string(src)) {
			found = append(found, name+":"+fn)
		}
	}
	assert.Equal(t, []string{"files.go:RemoveJobDir"}, found)
}

func TestSpec_RoundTrip(t *testing.T) {
	jobDir := filepath.Join(t.TempDir(), "jobs", "14")
	in := Spec{ID: 14, Argv: []string{"sh", "-c", "echo hi"}, Cwd: "/work", Env: []string{"A=1", "B=two words"}}
	require.NoError(t, WriteSpec(jobDir, in))
	got, err := ReadSpec(jobDir)
	require.NoError(t, err)
	assert.Equal(t, in, got)
	assertJobFileMode(t, jobDir, 0o700)
	assertJobFileMode(t, filepath.Join(jobDir, SpecFile), 0o600)

	in.HintCPUs = 4 // the start adds the hint
	require.NoError(t, WriteSpec(jobDir, in))
	got, err = ReadSpec(jobDir)
	require.NoError(t, err)
	assert.Equal(t, in, got, "a rewrite replaces the file")
}

func TestShimFiles_RoundTrip(t *testing.T) {
	jobDir := t.TempDir()
	t0 := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)

	started := Started{PID: 4242, Started: t0}
	require.NoError(t, writeStarted(jobDir, started))
	gotStarted, err := ReadStarted(jobDir)
	require.NoError(t, err)
	assert.Equal(t, started, gotStarted)

	for _, exit := range []Exit{
		{Code: 0, Ended: t0.Add(time.Minute), MaxRSSKB: 2048},
		{Code: -1, Signal: "SIGKILL", Ended: t0.Add(time.Minute)},
		{Code: 127, Ended: t0, Error: `exec: "nope": executable file not found in $PATH`},
	} {
		require.NoError(t, writeExit(jobDir, exit))
		got, err := ReadExit(jobDir)
		require.NoError(t, err)
		assert.Equal(t, exit, got)
	}

	entries, err := os.ReadDir(jobDir)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	assert.ElementsMatch(t, []string{StartedFile, ExitFile}, names, "the atomic writes leave no temp files")
	assertJobFileMode(t, filepath.Join(jobDir, StartedFile), 0o600)
	assertJobFileMode(t, filepath.Join(jobDir, ExitFile), 0o600)
}

// TestJobFiles_AbsentAndCorrupt: an absent file's error wraps fs.ErrNotExist,
// which is how the model tells "not yet" from "unreadable"; a file that does
// not parse is an error of its own.
func TestJobFiles_AbsentAndCorrupt(t *testing.T) {
	jobDir := t.TempDir()
	reads := map[string]func() error{
		SpecFile:    func() error { _, err := ReadSpec(jobDir); return err },
		StartedFile: func() error { _, err := ReadStarted(jobDir); return err },
		ExitFile:    func() error { _, err := ReadExit(jobDir); return err },
	}
	for name, read := range reads {
		assert.ErrorIs(t, read(), fs.ErrNotExist, "%s absent", name)
	}
	for name, read := range reads {
		require.NoError(t, os.WriteFile(filepath.Join(jobDir, name), []byte(`{"pid": `), 0o600))
		err := read()
		assert.Error(t, err, "%s corrupt", name)
		assert.NotErrorIs(t, err, fs.ErrNotExist, "%s corrupt", name)
	}
}
```

Create `sched/store_test.go`:

```go
package sched

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// storeRecord is a finished job with every field of a Record set, so that a
// field the codec drops shows.
func storeRecord(id JobID) Record {
	t0 := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)
	return Record{
		ID:           id,
		Order:        int64(id),
		Owner:        7,
		Description:  "polyoxide tests",
		Argv:         []string{"cargo", "test"},
		Cwd:          "/work/tree",
		Claim:        Claim{CPUs: 4, MemoryGB: 6, IO: true},
		Notify:       true,
		Tail:         20,
		State:        Failed,
		Submitted:    t0,
		Started:      t0.Add(time.Minute),
		Ended:        t0.Add(5 * time.Minute),
		PID:          4242,
		Exit:         &Exit{Code: 101, Ended: t0.Add(5 * time.Minute), MaxRSSKB: 2048},
		Failure:      "the job runner did not start",
		PeakRSSKB:    4096,
		CancelReason: "from the work view",
		SignalledAt:  t0.Add(4 * time.Minute),
		Notice:       NoticeUndelivered,
		NoticeWhy:    "the session is not running",
	}
}

// storeKeys returns m's keys, sorted.
func storeKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}

// writeStoreFile writes content as jobs.json in a fresh dir and returns the
// dir.
func writeStoreFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, StoreFile), []byte(content), 0o600))
	return dir
}

func TestStore_RoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "jobs")
	in := Store{Version: StoreVersion, NextID: 4, Jobs: []Record{storeRecord(1), storeRecord(2), storeRecord(3)}}
	require.NoError(t, SaveStore(dir, in))
	got, err := LoadStore(dir)
	require.NoError(t, err)
	assert.Equal(t, in, got)

	next := Store{Version: StoreVersion, NextID: 5, Jobs: []Record{storeRecord(4)}}
	require.NoError(t, SaveStore(dir, next))
	got, err = LoadStore(dir)
	require.NoError(t, err)
	assert.Equal(t, next, got, "a save replaces the file")
}

// TestSaveStore_ModesAndFormat: the file is 0600 in a 0700 directory, made
// along with its parents, stamped with the version, and a queued record omits
// what it has not got yet.
func TestSaveStore_ModesAndFormat(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "global", "jobs")
	queued := Record{
		ID: 1, Order: 1, Description: "d", Argv: []string{"true"}, Cwd: "/w",
		Claim: Claim{CPUs: 1, MemoryGB: 1}, Tail: 20, State: Queued,
		Submitted: time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC),
	}
	require.NoError(t, SaveStore(dir, Store{NextID: 2, Jobs: []Record{queued}}))
	assertJobFileMode(t, dir, 0o700)
	assertJobFileMode(t, filepath.Join(dir, StoreFile), 0o600)

	raw, err := os.ReadFile(filepath.Join(dir, StoreFile))
	require.NoError(t, err)
	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &top))
	assert.Equal(t, []string{"jobs", "next_id", "version"}, storeKeys(top))
	assert.JSONEq(t, `1`, string(top["version"]), "SaveStore stamps StoreVersion")
	var jobs []map[string]any
	require.NoError(t, json.Unmarshal(top["jobs"], &jobs))
	require.Len(t, jobs, 1)
	assert.Equal(t,
		[]string{"argv", "claim", "cwd", "description", "id", "order", "state", "submitted", "tail"},
		storeKeys(jobs[0]), "a queued record omits what it has not got yet")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "the atomic write leaves no temp file")

	require.NoError(t, SaveStore(dir, Store{NextID: 1}))
	raw, err = os.ReadFile(filepath.Join(dir, StoreFile))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &top))
	assert.JSONEq(t, `[]`, string(top["jobs"]), "no jobs is written as an empty list")
}

func TestLoadStore_NoFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "jobs")
	got, err := LoadStore(dir)
	require.NoError(t, err)
	assert.Equal(t, Store{Version: StoreVersion, NextID: 1}, got)
	assert.NoDirExists(t, dir, "loading creates nothing")
}

// TestLoadStore_NewerVersionIsLeftUntouched: a jobs.json from a newer loom
// is reported as ErrNewerStore even when its shape is one this build cannot
// parse, and the file is not touched.
func TestLoadStore_NewerVersionIsLeftUntouched(t *testing.T) {
	content := `{"version": 2, "next_id": 9, "jobs": "a shape this loom does not know", "extra": true}`
	dir := writeStoreFile(t, content)

	got, err := LoadStore(dir)
	assert.ErrorIs(t, err, ErrNewerStore)
	assert.Equal(t, Store{}, got)

	after, readErr := os.ReadFile(filepath.Join(dir, StoreFile))
	require.NoError(t, readErr)
	assert.Equal(t, content, string(after), "the file is as it was")
	entries, readErr := os.ReadDir(dir)
	require.NoError(t, readErr)
	assert.Len(t, entries, 1, "nothing else was written beside it")
}

func TestLoadStore_InvalidFilesAreLeftUntouched(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string // a part of the error
	}{
		{"truncated", `{"version": 1, "next_id": 3, "jobs": [`, "parse"},
		{"not an object", `[]`, "parse"},
		{"no version", `{"next_id": 1, "jobs": []}`, "version 0"},
		{"a negative version", `{"version": -1}`, "version -1"},
		{"an ID below 1", `{"version": 1, "next_id": 3, "jobs": [{"id": 0}]}`, "job ID 0"},
		{"a repeated ID", `{"version": 1, "next_id": 3, "jobs": [{"id": 2}, {"id": 2}]}`, "job 2 appears twice"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeStoreFile(t, tc.content)
			got, err := LoadStore(dir)
			assert.ErrorContains(t, err, tc.want)
			assert.NotErrorIs(t, err, ErrNewerStore)
			assert.Equal(t, Store{}, got)

			after, readErr := os.ReadFile(filepath.Join(dir, StoreFile))
			require.NoError(t, readErr)
			assert.Equal(t, tc.content, string(after), "the file is as it was")
		})
	}
}

// TestLoadStore_RaisesNextIDPastEveryJob: a job directory is named by its ID,
// so a counter at or below an ID on disk would reuse it; the load raises it,
// and never lowers it.
func TestLoadStore_RaisesNextIDPastEveryJob(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    JobID
	}{
		{"a counter behind the jobs", `{"version": 1, "next_id": 2, "jobs": [{"id": 5}, {"id": 3}]}`, 6},
		{"a counter at the highest ID", `{"version": 1, "next_id": 5, "jobs": [{"id": 5}]}`, 6},
		{"a counter ahead of the jobs", `{"version": 1, "next_id": 9, "jobs": [{"id": 3}]}`, 9},
		{"no counter and no jobs", `{"version": 1, "jobs": []}`, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := LoadStore(writeStoreFile(t, tc.content))
			require.NoError(t, err)
			assert.Equal(t, tc.want, got.NextID)
		})
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `CGO_ENABLED=0 go test ./sched/... -run 'Store|JobDir|RemoveJobDir|RemoveAll|Spec|ShimFiles|JobFiles'`

Expected: the build fails (line numbers differ):

```
# github.com/aidan-bailey/loom/sched [github.com/aidan-bailey/loom/sched.test]
sched/files_test.go:NN:NN: undefined: JobDir
sched/files_test.go:NN:NN: undefined: RemoveJobDir
sched/files_test.go:NN:NN: undefined: Spec
sched/store_test.go:NN:NN: undefined: SaveStore
...
FAIL	github.com/aidan-bailey/loom/sched [build failed]
```

- [ ] **Step 3: Implement**

Create `sched/files.go`:

```go
package sched

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/aidan-bailey/loom/config"
)

// The files of the jobs directory: jobs.json beside the job directories, and
// in each job's directory (JobDir) the shim's inputs and outputs.
const (
	StoreFile   = "jobs.json"
	SpecFile    = "spec.json"
	LogFile     = "output.log"
	LockFile    = "run.lock"
	StartedFile = "started.json"
	ExitFile    = "exit.json"
)

// JobDir returns the directory of job id under dir, which is
// <globalDir>/jobs: dir joined with the decimal ID.
func JobDir(dir string, id JobID) string {
	return filepath.Join(dir, strconv.FormatInt(int64(id), 10))
}

// RemoveJobDir removes the directory of job id under dir: the only removal
// in this package. The path is JobDir(dir, id), built from the ID, never a
// path read from a file. It refuses a dir that is empty, relative or not
// clean, and an ID below 1: JobDir("", 7) is "7" in whatever directory the
// process runs in, and a dir such as "lnk/.." cleans lexically to another tree
// than the kernel reaches through the link. os.RemoveAll removes a link inside
// the directory, never what it points at. A directory already gone is no
// error.
func RemoveJobDir(dir string, id JobID) error {
	if dir == "" || !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || id < 1 {
		return fmt.Errorf("refusing to remove job %d's directory under %q: the path is not proved to be loom's", id, dir)
	}
	if err := os.RemoveAll(JobDir(dir, id)); err != nil {
		return fmt.Errorf("remove job %d's directory: %w", id, err)
	}
	return nil
}

// saveJSON writes v to path, indented, atomically, with mode 0600.
func saveJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", filepath.Base(path), err)
	}
	return config.AtomicWriteFile(path, append(data, '\n'), 0o600)
}

// loadJSON reads path into v. The error for a file that is absent wraps
// fs.ErrNotExist.
func loadJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

// Spec is spec.json: what the shim needs to run a job. It is written when the
// job is submitted, so a queued job survives a restart, and rewritten with
// HintCPUs when the job starts.
type Spec struct {
	ID       JobID    `json:"id"`
	Argv     []string `json:"argv"`
	Cwd      string   `json:"cwd"`
	Env      []string `json:"env"`
	HintCPUs int      `json:"hint_cpus,omitempty"` // written when the job starts
}

// WriteSpec writes jobDir's spec.json (mode 0600), making jobDir (mode 0700)
// when it is absent.
func WriteSpec(jobDir string, s Spec) error {
	if err := os.MkdirAll(jobDir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", jobDir, err)
	}
	return saveJSON(filepath.Join(jobDir, SpecFile), s)
}

// ReadSpec reads jobDir's spec.json. The error for an absent file wraps
// fs.ErrNotExist.
func ReadSpec(jobDir string) (Spec, error) {
	var s Spec
	if err := loadJSON(filepath.Join(jobDir, SpecFile), &s); err != nil {
		return Spec{}, err
	}
	return s, nil
}

// Started is started.json: the shim writes it once it holds the job's lock.
type Started struct {
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
}

// ReadStarted reads jobDir's started.json. The error for an absent file wraps
// fs.ErrNotExist.
func ReadStarted(jobDir string) (Started, error) {
	var s Started
	if err := loadJSON(filepath.Join(jobDir, StartedFile), &s); err != nil {
		return Started{}, err
	}
	return s, nil
}

// ReadExit reads jobDir's exit.json. The error for an absent file wraps
// fs.ErrNotExist.
func ReadExit(jobDir string) (Exit, error) {
	var e Exit
	if err := loadJSON(filepath.Join(jobDir, ExitFile), &e); err != nil {
		return Exit{}, err
	}
	return e, nil
}

// writeStarted writes jobDir's started.json atomically. The shim's.
func writeStarted(jobDir string, s Started) error {
	return saveJSON(filepath.Join(jobDir, StartedFile), s)
}

// writeExit writes jobDir's exit.json atomically. The shim's.
func writeExit(jobDir string, e Exit) error {
	return saveJSON(filepath.Join(jobDir, ExitFile), e)
}
```

Create `sched/store.go`:

```go
package sched

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// StoreVersion is the version of jobs.json this build reads and writes.
const StoreVersion = 1

// Store is jobs.json: every job's record, and the counter for the next ID.
type Store struct {
	Version int      `json:"version"`
	NextID  JobID    `json:"next_id"`
	Jobs    []Record `json:"jobs"`
}

// ErrNewerStore is LoadStore's error for a jobs.json written by a newer loom.
var ErrNewerStore = errors.New("jobs.json is from a newer loom")

// LoadStore reads dir's jobs.json, where dir is <globalDir>/jobs. With no
// file it returns an empty store: version StoreVersion, NextID 1, no jobs. It
// never writes, and a file it cannot use is left as it is: an unreadable or
// invalid one is an error, and one with a newer version is an error wrapping
// ErrNewerStore, found before the rest is parsed, since a newer shape may not
// parse here. A job ID that is below 1 or repeated is invalid. NextID is
// raised past the highest ID on disk, so an ID is never reused.
func LoadStore(dir string) (Store, error) {
	path := filepath.Join(dir, StoreFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Store{Version: StoreVersion, NextID: 1}, nil
	}
	if err != nil {
		return Store{}, fmt.Errorf("read %s: %w", path, err)
	}
	var head struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return Store{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if head.Version > StoreVersion {
		return Store{}, fmt.Errorf("%s has version %d, this loom reads up to %d: %w", path, head.Version, StoreVersion, ErrNewerStore)
	}
	if head.Version < 1 {
		return Store{}, fmt.Errorf("%s has version %d: it must be at least 1", path, head.Version)
	}
	var s Store
	if err := json.Unmarshal(data, &s); err != nil {
		return Store{}, fmt.Errorf("parse %s: %w", path, err)
	}
	seen := make(map[JobID]bool, len(s.Jobs))
	for _, r := range s.Jobs {
		if r.ID < 1 {
			return Store{}, fmt.Errorf("%s: job ID %d is below 1", path, r.ID)
		}
		if seen[r.ID] {
			return Store{}, fmt.Errorf("%s: job %d appears twice", path, r.ID)
		}
		seen[r.ID] = true
		s.NextID = max(s.NextID, r.ID+1)
	}
	s.NextID = max(s.NextID, 1)
	return s, nil
}

// SaveStore writes s as dir's jobs.json atomically (mode 0600), making dir
// (mode 0700) and its parents when they are absent. It stamps StoreVersion
// and writes no jobs as an empty list.
func SaveStore(dir string, s Store) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	s.Version = StoreVersion
	if s.Jobs == nil {
		s.Jobs = []Record{}
	}
	return saveJSON(filepath.Join(dir, StoreFile), s)
}
```

In `sched/CLAUDE.md`, add these three bullets after the last bullet of `## Rules when modifying this package` (the `Hints` rule), before the blank line and `## Pointers`:

```markdown
- **Make `RemoveJobDir` the only removal in this package, and build its path from the ID.** It removes `JobDir(dir, id)` with `os.RemoveAll`, so a path read from a file (a record, `spec.json`) must never reach it: such a path could name any tree, and a deleted tree is gone. It refuses an empty, relative or unclean `dir` (`JobDir("", 7)` is `7` in whatever directory the daemon runs in, and a `..` after a link cleans lexically to another tree than the kernel reaches) and an ID below 1, and it never follows a link. **Enforced** by `TestRemoveJobDir_RemovesOnlyThatJobsDir`, `TestRemoveJobDir_DoesNotFollowLinks`, `TestRemoveJobDir_RefusesWhatItCannotProve` and, for the only-removal, `TestSched_RemoveAllIsOnlyInRemoveJobDir` (`sched/files_test.go`), whose matcher `TestRemoveAllCallers_Detection` pins.
- **Read `jobs.json` only through `LoadStore`, and leave a file that fails to load untouched.** `LoadStore` never writes: an unreadable, invalid or newer-version file (`ErrNewerStore`, found before the rest is parsed) pauses admission and refuses submissions while the running shims carry on, and an overwrite with an empty store would drop every queued job and the running ones' records for good. **Enforced** by `TestLoadStore_NewerVersionIsLeftUntouched` and `TestLoadStore_InvalidFilesAreLeftUntouched` (`sched/store_test.go`).
- **Write `jobs.json` and the per-job files only through `config.AtomicWriteFile` (0600, in 0700 directories), and let `NextID` only go up.** A torn file loses the queue or a result; the job directory is named by the ID, so a reused ID hands a new job an old job's `output.log`, `exit.json` and lock. `SaveStore` stamps `StoreVersion`, and `LoadStore` raises `NextID` past the highest ID on disk. **Enforced** for the modes, the format and the counter by `TestSaveStore_ModesAndFormat`, `TestShimFiles_RoundTrip` and `TestLoadStore_RaisesNextIDPastEveryJob`; **Convention** for the routing through `AtomicWriteFile` — a torn file after a crash mid-write.
```

- [ ] **Step 4: Run it to see it pass**

Run:

```
CGO_ENABLED=0 go test ./sched/... ./tools/claudemd/... ./internal/testenv/...
gofmt -l sched
CGO_ENABLED=0 go vet ./sched/...
```

Expected: `ok` for `sched`, `tools/claudemd` and `internal/testenv`; `gofmt -l` and `go vet` print nothing. To see the removal scan bite, add a call `os.RemoveAll("x")` to any other non-test file of `sched` for a moment: `TestSched_RemoveAllIsOnlyInRemoveJobDir` then fails listing it. Take the call out again.

- [ ] **Step 5: Commit**

```
git add -N sched/files.go sched/files_test.go sched/store.go sched/store_test.go
git commit -m "feat(sched): the jobs.json store and the per-job files" -- sched/files.go sched/files_test.go sched/store.go sched/store_test.go sched/CLAUDE.md
```

### Task 1.5: The package README, and the story's gates

**Files:**
- Create: `sched/README.md`
- Modify: `sched/CLAUDE.md` (the Pointers section)

- [ ] **Step 1: Write the README**

Create `sched/README.md`. Story 2 adds one section, `## The runtime`, at the end of the file and changes nothing above it.

```markdown
# sched

The work scheduler's package. This file covers the logic half: the job types, the caps and policy, the admission pass, the parallelism hints, and the `jobs.json` store with the per-job files. The model (`core`) owns the queue, persists it under `<globalDir>/jobs/` and calls this package from its loop; nothing here keeps state of its own, so every function takes the host, the policy and the clock as arguments. The design is in [`docs/superpowers/specs/2026-11-04-work-scheduler-design.md`](../docs/superpowers/specs/2026-11-04-work-scheduler-design.md).

## Files

- `doc.go` — the package comment.
- `job.go` — `JobID`, `State`, `Claim`, `Caps`, `Need`, `Host`, `Policy`, `Notice`, `Record`, `Exit` and `Submit`, with `Claim.Need`, `Submit.Validate` and `CheckClaim`.
- `policy.go` — `Resolve`: a `config.SchedulerSettings` and a `Host` in, a `Policy` or an error out.
- `admit.go` — `Admit` and its `Decision`.
- `hints.go` — `Hints`.
- `store.go` — `Store`, `LoadStore`, `SaveStore` and `ErrNewerStore`.
- `files.go` — the file names, `JobDir`, `RemoveJobDir`, and the codecs of `Spec`, `Started` and `Exit`.
- `testmain_test.go` — isolates the loom dirs for the package's tests.

## Jobs

A job is `queued`, then `running`, then one of four finished states: `done` (exit 0), `failed` (a non-zero exit, a signal, or a start that failed), `cancelled` (cancelled by anyone, whatever its exit) or `lost` (its shim vanished without writing its result). `State.Finished` is true for the last four. A `JobID` is per host, from 1, and never reused.

A `Submit` is what a client sends; the model validates it (`Submit.Validate` for the bounds, `CheckClaim` for the caps), applies the default claim and stores a `Record`, which is everything about the job but its environment. A `Record`'s `Order` is its ID at submit and the lowest queued `Order` minus 1 after a move to the front, and the queue runs lowest first.

## Claims, caps and the policy

A `Claim` is whole cores and whole GB, whether the job is I/O-heavy (one of the `io_jobs` slots), or `Exclusive` (every cap, as the caps stand when it is admitted). `Claim.Need` turns a claim into what it takes from the `Caps`. `Resolve` turns the `scheduler` block of the global `config.json` and the `Host` into a `Policy`:

- a field that is 0 or absent takes the host's default: `cpus` is max(1, cores − 2); `memory_gb` is max(1, ⌊0.75 × RAM in GB⌋), with a GB being 2^30 bytes; `io_jobs` is 2; the default claim is {min(4, cpus), min(6, memory_gb)}; `starve_after_minutes` is 10; `keep_finished` is 50;
- the bounds apply once a default is taken: `cpus` 1–1024, `memory_gb` 1–65536, `io_jobs` 1–64, `starve_after_minutes` 1–1440, `keep_finished` 1–10000, each default claim value 1 up to its cap;
- a value outside its bounds, or an unreadable RAM with `memory_gb` unset, is an error and no `Policy`: the model pauses admission and says why.

## Admission

`Admit(jobs, policy, now)` returns the jobs to start and, for each queued job left waiting, why. It walks the queued jobs in queue order: a job over a cap is skipped (`over this host's caps`) and is never the head; once the head, the first job not skipped, has waited `StarveAfter` since it was submitted, it starts if it fits and nothing else starts (the others wait `waits for job <id>, which has waited past <m>m`); otherwise every job that fits what the running jobs and the earlier starts leave starts, first fit. A job that does not fit waits for cores (`needs <n> cores (<free> free)`), then GB (`needs <n> GB (<free> free)`), then I/O slots (`needs an I/O slot`, or `needs <n> I/O slots (<free> free)` for a need of more than one).

## On disk

Everything is under `<globalDir>/jobs/`, in directories of mode 0700 and files of mode 0600, written atomically.

| Path | Holds |
|---|---|
| `jobs.json` | `Store`: `{"version": 1, "next_id": N, "jobs": [...]}`. `LoadStore` never writes, leaves a file that fails to load alone, reports a newer version as `ErrNewerStore`, and raises `next_id` past every ID on disk |
| `<id>/spec.json` | `Spec`: argv, cwd, the submitter's environment, and the parallelism the start hints |
| `<id>/output.log` | the command's stdout and stderr |
| `<id>/run.lock` | the shim's lock, held while the job runs |
| `<id>/started.json` | `Started`: the shim's pid and start time |
| `<id>/exit.json` | `Exit`: the exit code or signal, the end time, the command's max RSS, and the start error if the exec failed |

`JobDir(dir, id)` is `<dir>/<id>`, and `RemoveJobDir` removes it when retention drops a finished job.
```

In `sched/CLAUDE.md`, replace the Pointers section's first line

```markdown
- [`../docs/ARCHITECTURE.md`](../docs/ARCHITECTURE.md) — where this package sits.
```

with

```markdown
- [`README.md`](README.md) — the package's facts: files, states, the policy, admission and the job directory.
- [`../docs/ARCHITECTURE.md`](../docs/ARCHITECTURE.md) — where this package sits.
```

- [ ] **Step 2: Run the story's Verify**

Run: `CGO_ENABLED=0 go test ./sched/... ./config/... && CGO_ENABLED=0 go run ./tools/claudemd`

Expected: `ok` for `sched` and `config`, and `claudemd` prints nothing and exits 0 (a broken link, a test name that does not exist, a rule without **Enforced** or **Convention** would print as `path:line: message`). `CGO_ENABLED=0 go run ./tools/claudemd -v` shows `sched/CLAUDE.md` well inside its budget (100 lines, 20000 bytes).

- [ ] **Step 3: Run the story gates**

Run, in this order:

```
bash -c 'out=$(gofmt -l $(git ls-files "*.go" | grep -v "^vendor/")) || exit 1; if [ -n "$out" ]; then echo "gofmt would reformat:"; echo "$out"; exit 1; fi'
CGO_ENABLED=0 go vet ./...
CGO_ENABLED=0 go test ./...
```

Expected: the first prints nothing, the second prints nothing, the third ends with every package `ok` or `no test files`.

- [ ] **Step 4: Commit**

```
git add -N sched/README.md
git commit -m "docs(sched): the package README" -- sched/README.md sched/CLAUDE.md
```

## Story 2: The job runner: `loom job-shim`, starting, probing, signalling, sampling and the host

- **Developer:** intermediate-dev
- **Files:** `sched/shim.go`, `sched/shim_unix.go`, `sched/shim_windows.go`, `sched/shim_test.go`, `sched/prio_linux.go`, `sched/prio_other.go`, `sched/sample.go`, `sched/sample_test.go`, `sched/host.go`, `sched/host_linux.go`, `sched/host_darwin.go`, `sched/host_other.go`, `sched/host_test.go`, `sched/testshim.go`, `sched/testmain_test.go`, `sched/CLAUDE.md`, `sched/README.md`, `job_shim.go`, `job_shim_test.go`, `main.go`, `flake.nix`
- **Interfaces:** exposes the `sched` runtime names of the names contract (Story 2) and `jobShimCmd`; uses `Spec`, `Started`, `Exit`, `ReadSpec`, `writeStarted`, `writeExit`, `Hints`, `Host`, `JobDir` and the file-name constants from Story 1
- **Verify:** `CGO_ENABLED=0 go test ./sched/... . && CGO_ENABLED=0 go build ./... && GOOS=windows CGO_ENABLED=0 go build ./... && GOOS=darwin CGO_ENABLED=0 go build ./... && nix eval --raw .#loom.drvPath >/dev/null`
- **Review:** deep

The process half of `sched`, in four tasks. Task 2.1 adds the `ps` sampler and its parser, and Task 2.2 the host detection; both are pure enough to test without a process of ours. Task 2.3 adds the runner: `RunShim`, `Runner.Start`, `Probe`, `SignalGroup`, the priorities, the Windows stubs and the test-shim hook, with tests that start real shims (the test binary re-executed as the shim). Task 2.4 adds the hidden `loom job-shim` command with its test, `procps` in `flake.nix` so that `ps` is there under Nix, and the package's rules and facts. The code holds the landmine markers `syscall.Kill(` (in `SignalGroup`, which refuses a pgid of 1 or below and its own group, and whose callers prove a group is the job's before they signal it) and `os.Environ()` (in `Runner.shimEnv`, which builds the shim process's environment as `os.Environ()` plus `Runner.Env`; the job's own environment comes only from `spec.json` and the hints), so the supervisor should dispatch this story's developer with `effort: xhigh`. Platform code lives only in `_unix.go`, `_windows.go`, `_linux.go`, `_darwin.go` and `_other.go` files. Where the contract and the spec leave a detail open, the code settles it as follows. A missing `run.lock` reads as free from `Probe`, since no shim has taken it. The shim catches SIGTERM, SIGINT and SIGHUP so a signal sent to its process group reaches the command while it stays up to report, and passes none on itself, since the group send that cancels a job already reaches the command (spec §5.3 step 5). `Exit.Signal` is `unix.SignalName`'s name for the signal (`golang.org/x/sys/unix`, vendored), or `SIG<n>` for a signal it has no name for. The command is found on the `PATH` of the job's environment, not the shim's. A job whose spec has no CPU hint (`HintCPUs` 0) gets 1.

### Task 2.1: The `ps` sampler

**Files:**
- Create: `sched/sample.go`
- Test: `sched/sample_test.go`

- [ ] **Step 1: Write the failing test**

Create `sched/sample_test.go`:

```go
//go:build unix

package sched

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requirePS skips the test where ps is not installed, as in the Nix build
// sandbox, whose check inputs hold no procps.
func requirePS(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ps"); err != nil {
		t.Skip("ps is not installed")
	}
}

func TestParsePS(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []Proc
	}{
		{
			name: "procps writes hh:mm:ss",
			in:   "    1     1  10240 00:00:03\n 4242  4242   2048 00:10:00\n",
			want: []Proc{
				{PID: 1, PGID: 1, RSSKB: 10240, CPU: 3 * time.Second},
				{PID: 4242, PGID: 4242, RSSKB: 2048, CPU: 10 * time.Minute},
			},
		},
		{
			name: "procps writes days before an hour",
			in:   "77 77 5 2-03:04:05\n",
			want: []Proc{{PID: 77, PGID: 77, RSSKB: 5, CPU: 48*time.Hour + 3*time.Hour + 4*time.Minute + 5*time.Second}},
		},
		{
			name: "BSD writes minutes and hundredths",
			in:   "  501   501   9216   0:03.45\n  502   501  12288  12:34.56\n",
			want: []Proc{
				{PID: 501, PGID: 501, RSSKB: 9216, CPU: 3*time.Second + 450*time.Millisecond},
				{PID: 502, PGID: 501, RSSKB: 12288, CPU: 12*time.Minute + 34*time.Second + 560*time.Millisecond},
			},
		},
		{
			name: "BSD writes hours when a process has run that long",
			in:   "503 503 1024 1:02:03.45\n",
			want: []Proc{{PID: 503, PGID: 503, RSSKB: 1024, CPU: time.Hour + 2*time.Minute + 3*time.Second + 450*time.Millisecond}},
		},
		{
			name: "a bare number of seconds",
			in:   "9 9 1 42\n",
			want: []Proc{{PID: 9, PGID: 9, RSSKB: 1, CPU: 42 * time.Second}},
		},
		{
			name: "blank lines and no trailing newline",
			in:   "\n9 9 1 0:00\n\n10 9 2 0:01",
			want: []Proc{
				{PID: 9, PGID: 9, RSSKB: 1},
				{PID: 10, PGID: 9, RSSKB: 2, CPU: time.Second},
			},
		},
		{name: "no output", in: "", want: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParsePS([]byte(tc.in))
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestParsePS_RefusesWhatItCannotRead(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"too few fields", "1 1 100\n", "ps line 1"},
		{"too many fields", "1 1 100 0:01 extra\n", "ps line 1"},
		{"a pid that is not a number", "x 1 100 0:01\n", "bad pid"},
		{"a negative group", "1 -1 100 0:01\n", "bad pgid"},
		{"a negative size", "1 1 -5 0:01\n", "bad rss"},
		{"a time with four parts", "1 1 100 1:2:3:4\n", "CPU time"},
		{"a time with letters", "1 1 100 ab:cd\n", "CPU time"},
		{"a time with an empty day count", "1 1 100 -01:00:00\n", "CPU time"},
		{"a time with an empty fraction", "1 1 100 0:01.\n", "CPU time"},
		{"a bad line after a good one", "1 1 100 0:01\n2 2 oops 0:01\n", "ps line 2"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParsePS([]byte(tc.in))
			require.Error(t, err)
			assert.ErrorContains(t, err, tc.want)
			assert.Nil(t, got, "a line it cannot read spoils the whole sample")
		})
	}
}

func TestSnapshot_Group(t *testing.T) {
	snap := Snapshot{Procs: []Proc{
		{PID: 100, PGID: 100, RSSKB: 1000, CPU: 2 * time.Second},
		{PID: 101, PGID: 100, RSSKB: 500, CPU: time.Second},
		{PID: 102, PGID: 100, RSSKB: 250, CPU: 500 * time.Millisecond},
		{PID: 200, PGID: 200, RSSKB: 9000, CPU: time.Hour},
		{PID: 300, PGID: 250, RSSKB: 40, CPU: time.Second},
		{PID: 450, PGID: 900, RSSKB: 7, CPU: time.Minute},
		{PID: 451, PGID: 450, RSSKB: 3, CPU: 2 * time.Minute},
	}}
	tests := []struct {
		name       string
		pgid       int
		wantRSS    int64
		wantCPU    time.Duration
		wantMember int
		wantLeader bool
	}{
		{"a whole group with its leader", 100, 1750, 3500 * time.Millisecond, 3, true},
		{"another group is left out", 200, 9000, time.Hour, 1, true},
		{"members whose leader is gone", 250, 40, time.Second, 1, false},
		{"a leader's pid taken by a process of another group", 450, 3, 2 * time.Minute, 1, true},
		{"no such group", 999, 0, 0, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rss, cpu, members, leaderAlive := snap.Group(tc.pgid)
			assert.Equal(t, tc.wantRSS, rss)
			assert.Equal(t, tc.wantCPU, cpu)
			assert.Equal(t, tc.wantMember, members)
			assert.Equal(t, tc.wantLeader, leaderAlive)
		})
	}
}

func TestSample_ListsThisProcess(t *testing.T) {
	requirePS(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	before := time.Now()

	snap, err := Sample(ctx)
	require.NoError(t, err)

	assert.False(t, snap.Taken.Before(before), "a snapshot is stamped when ps returned")
	var self *Proc
	for i := range snap.Procs {
		if snap.Procs[i].PID == os.Getpid() {
			self = &snap.Procs[i]
		}
	}
	require.NotNil(t, self, "ps -A lists this test binary")
	assert.Greater(t, self.RSSKB, int64(0))
	assert.Equal(t, syscall.Getpgrp(), self.PGID)
}

func TestSample_FailsOnADoneContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	snap, err := Sample(ctx)

	require.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, snap.Procs, "a failed sample carries nothing")
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `CGO_ENABLED=0 go test ./sched/ -run 'TestParsePS|TestSnapshot_Group|TestSample_'`

Expected: the package fails to build with `undefined: ParsePS`, `undefined: Proc`, `undefined: Snapshot` and `undefined: Sample`, and the run ends `FAIL github.com/aidan-bailey/loom/sched [build failed]`.

- [ ] **Step 3: Implement**

Create `sched/sample.go`:

```go
package sched

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Proc is one process in a Snapshot.
type Proc struct {
	PID   int
	PGID  int
	RSSKB int64
	CPU   time.Duration // CPU time used so far, user and system together
}

// Snapshot is one reading of the process table.
type Snapshot struct {
	Taken time.Time
	Procs []Proc
}

// Sample reads the process table with one "ps -A -o pid=,pgid=,rss=,time=",
// which procps (Linux) and BSD ps (macOS) both accept, though they write the
// time column differently (ParsePS reads both). ps runs for as long as ctx
// allows: the caller gives it 5 s. A ps that fails, runs out of time or lists
// nothing is an error, and the caller skips that sample.
func Sample(ctx context.Context) (Snapshot, error) {
	out, err := exec.CommandContext(ctx, "ps", "-A", "-o", "pid=,pgid=,rss=,time=").Output()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			err = ctxErr
		}
		return Snapshot{}, fmt.Errorf("ps: %w", err)
	}
	procs, err := ParsePS(out)
	if err != nil {
		return Snapshot{}, err
	}
	if len(procs) == 0 {
		return Snapshot{}, errors.New("ps listed no processes")
	}
	return Snapshot{Taken: time.Now(), Procs: procs}, nil
}

// ParsePS reads the output of "ps -A -o pid=,pgid=,rss=,time=": one process
// per line, four whitespace-separated fields, rss in kilobytes. The time is
// [d-][[h:]m:]s[.f]: procps writes [d-]hh:mm:ss, BSD ps m:ss.hh. A line it
// cannot read fails the whole parse, since a sample with a hole in it would
// understate a job's use.
func ParsePS(out []byte) ([]Proc, error) {
	var procs []Proc
	for n, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 4 {
			return nil, fmt.Errorf("ps line %d has %d fields, want 4: %q", n+1, len(fields), line)
		}
		p, err := parsePSLine(fields)
		if err != nil {
			return nil, fmt.Errorf("ps line %d: %w", n+1, err)
		}
		procs = append(procs, p)
	}
	return procs, nil
}

func parsePSLine(fields []string) (Proc, error) {
	pid, err := strconv.Atoi(fields[0])
	if err != nil || pid < 0 {
		return Proc{}, fmt.Errorf("bad pid %q", fields[0])
	}
	pgid, err := strconv.Atoi(fields[1])
	if err != nil || pgid < 0 {
		return Proc{}, fmt.Errorf("bad pgid %q", fields[1])
	}
	rss, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil || rss < 0 {
		return Proc{}, fmt.Errorf("bad rss %q", fields[2])
	}
	cpu, err := parseCPUTime(fields[3])
	if err != nil {
		return Proc{}, err
	}
	return Proc{PID: pid, PGID: pgid, RSSKB: rss, CPU: cpu}, nil
}

// parseCPUTime reads ps's time column: [d-][[h:]m:]s[.f].
func parseCPUTime(s string) (time.Duration, error) {
	bad := fmt.Errorf("CPU time %q is not [d-][[h:]m:]s[.f]", s)
	rest := s
	var days int64
	if d, r, found := strings.Cut(rest, "-"); found {
		n, ok := psDigits(d)
		if !ok {
			return 0, bad
		}
		days, rest = n, r
	}
	parts := strings.Split(rest, ":")
	if len(parts) > 3 {
		return 0, bad
	}
	last, frac, hasFrac := strings.Cut(parts[len(parts)-1], ".")
	parts[len(parts)-1] = last
	var clock int64
	for _, p := range parts {
		n, ok := psDigits(p)
		if !ok {
			return 0, bad
		}
		clock = clock*60 + n
	}
	var nanos int64
	if hasFrac {
		if _, ok := psDigits(frac); !ok {
			return 0, bad
		}
		if len(frac) > 9 {
			frac = frac[:9]
		}
		nanos, _ = psDigits(frac + strings.Repeat("0", 9-len(frac)))
	}
	return time.Duration(days*86400+clock)*time.Second + time.Duration(nanos), nil
}

// psDigits reads a non-empty run of ASCII digits.
func psDigits(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil
}

// Group sums the memory and CPU time of the processes in process group
// pgid, and counts them. leaderAlive says whether any process has pid pgid,
// whatever group it is in: a process that holds the shim's pid without
// belonging to its group is a reused pid, not the shim.
func (s Snapshot) Group(pgid int) (rssKB int64, cpu time.Duration, members int, leaderAlive bool) {
	for _, p := range s.Procs {
		if p.PID == pgid {
			leaderAlive = true
		}
		if p.PGID == pgid {
			rssKB += p.RSSKB
			cpu += p.CPU
			members++
		}
	}
	return rssKB, cpu, members, leaderAlive
}
```

- [ ] **Step 4: Run it to see it pass**

Run: `CGO_ENABLED=0 go test ./sched/ -run 'TestParsePS|TestSnapshot_Group|TestSample_' -v`

Expected: every `--- PASS` (`TestSample_ListsThisProcess` is a `--- SKIP` where `ps` is not installed), ending `ok  	github.com/aidan-bailey/loom/sched`. Then `gofmt -l sched` prints nothing and `CGO_ENABLED=0 go vet ./sched/` prints nothing.

- [ ] **Step 5: Commit**

```bash
git add -N sched/sample.go sched/sample_test.go
git commit -m "feat(sched): add the ps sampler and its parser" -- sched/sample.go sched/sample_test.go
```

### Task 2.2: Host detection

**Files:**
- Create: `sched/host.go`, `sched/host_linux.go`, `sched/host_darwin.go`, `sched/host_other.go`
- Test: `sched/host_test.go`

- [ ] **Step 1: Write the failing test**

Create `sched/host_test.go`:

```go
package sched

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseMemTotal(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want uint64
	}{
		{"a real /proc/meminfo head", "MemTotal:       31701924 kB\nMemFree:         1204448 kB\n", 31701924 * 1024},
		{"MemTotal after other lines", "MemFree: 5 kB\nMemTotal: 2048 kB\nBuffers: 1 kB\n", 2048 * 1024},
		{"another key that starts the same", "MemTotalFoo: 5 kB\nMemTotal: 7 kB\n", 7 * 1024},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseMemTotal([]byte(tc.in))
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestParseMemTotal_RefusesWhatItCannotRead(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"no MemTotal line", "MemFree: 5 kB\n"},
		{"empty", ""},
		{"no unit", "MemTotal: 2048\n"},
		{"another unit", "MemTotal: 2 MB\n"},
		{"not a number", "MemTotal: lots kB\n"},
		{"a negative number", "MemTotal: -5 kB\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseMemTotal([]byte(tc.in))
			assert.Error(t, err)
		})
	}
}

func TestParseMemsize(t *testing.T) {
	got, err := parseMemsize([]byte("34359738368\n"))
	require.NoError(t, err)
	assert.Equal(t, uint64(34359738368), got)

	got, err = parseMemsize([]byte("  8589934592  "))
	require.NoError(t, err)
	assert.Equal(t, uint64(8589934592), got, "surrounding space is trimmed")

	for _, in := range []string{"", "lots", "-5", "1.5e9"} {
		_, err := parseMemsize([]byte(in))
		assert.Error(t, err, "input %q", in)
	}
}

func TestDetectHost(t *testing.T) {
	h := DetectHost()

	assert.NotEmpty(t, h.Name)
	assert.Equal(t, runtime.NumCPU(), h.Cores)
	switch runtime.GOOS {
	case "linux", "darwin":
		assert.Greater(t, h.RAMBytes, uint64(0), "this platform can read its RAM")
	default:
		assert.Zero(t, h.RAMBytes, "no reader here, so the RAM stays unknown")
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `CGO_ENABLED=0 go test ./sched/ -run 'TestParseMemTotal|TestParseMemsize|TestDetectHost'`

Expected: the package fails to build with `undefined: parseMemTotal`, `undefined: parseMemsize` and `undefined: DetectHost`, and the run ends `FAIL github.com/aidan-bailey/loom/sched [build failed]`.

- [ ] **Step 3: Implement**

Create `sched/host.go`:

```go
package sched

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// DetectHost reads this host's name, cores and RAM. The model calls it once
// per daemon process and keeps the answer. A RAM it cannot read stays 0
// (Host.RAMBytes), never a guess: Resolve then asks for scheduler.memory_gb.
func DetectHost() Host {
	name, err := os.Hostname()
	if err != nil || name == "" {
		name = "localhost"
	}
	return Host{Name: name, Cores: runtime.NumCPU(), RAMBytes: readRAMBytes()}
}

// parseMemTotal reads MemTotal, in bytes, from the text of /proc/meminfo,
// where the kernel's "kB" is 1024 bytes.
func parseMemTotal(meminfo []byte) (uint64, error) {
	for _, line := range strings.Split(string(meminfo), "\n") {
		rest, ok := strings.CutPrefix(line, "MemTotal:")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) != 2 || fields[1] != "kB" {
			return 0, fmt.Errorf("unexpected MemTotal line %q", line)
		}
		kb, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("MemTotal %q: %w", fields[0], err)
		}
		return kb * 1024, nil
	}
	return 0, errors.New("no MemTotal line")
}

// parseMemsize reads the bytes "sysctl -n hw.memsize" prints.
func parseMemsize(out []byte) (uint64, error) {
	n, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("hw.memsize %q: %w", strings.TrimSpace(string(out)), err)
	}
	return n, nil
}
```

Create `sched/host_linux.go`:

```go
package sched

import "os"

// readRAMBytes reads MemTotal from /proc/meminfo; 0 when it cannot be read
// or understood.
func readRAMBytes() uint64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	n, err := parseMemTotal(data)
	if err != nil {
		return 0
	}
	return n
}
```

Create `sched/host_darwin.go`:

```go
package sched

import (
	"context"
	"os/exec"
	"time"
)

// sysctlTimeout bounds the one subprocess DetectHost runs.
const sysctlTimeout = 5 * time.Second

// readRAMBytes asks "sysctl -n hw.memsize"; 0 when it fails, runs out of
// time or prints something else.
func readRAMBytes() uint64 {
	ctx, cancel := context.WithTimeout(context.Background(), sysctlTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "sysctl", "-n", "hw.memsize").Output()
	if err != nil {
		return 0
	}
	n, err := parseMemsize(out)
	if err != nil {
		return 0
	}
	return n
}
```

Create `sched/host_other.go`:

```go
//go:build !linux && !darwin

package sched

// readRAMBytes has no source of RAM here; 0 is "could not be read".
func readRAMBytes() uint64 { return 0 }
```

- [ ] **Step 4: Run it to see it pass**

Run: `CGO_ENABLED=0 go test ./sched/ -run 'TestParseMemTotal|TestParseMemsize|TestDetectHost' -v`

Expected: every `--- PASS`, ending `ok  	github.com/aidan-bailey/loom/sched`. Then `gofmt -l sched` prints nothing, `CGO_ENABLED=0 go vet ./sched/` prints nothing, and `GOOS=darwin CGO_ENABLED=0 go build ./sched/` and `GOOS=windows CGO_ENABLED=0 go build ./sched/` print nothing.

- [ ] **Step 5: Commit**

```bash
git add -N sched/host.go sched/host_linux.go sched/host_darwin.go sched/host_other.go sched/host_test.go
git commit -m "feat(sched): detect the host's name, cores and RAM" -- sched/host.go sched/host_linux.go sched/host_darwin.go sched/host_other.go sched/host_test.go
```

### Task 2.3: The shim, the runner, the lock probe and group signals

**Files:**
- Create: `sched/shim.go`, `sched/shim_unix.go`, `sched/shim_windows.go`, `sched/prio_linux.go`, `sched/prio_other.go`, `sched/testshim.go`
- Modify: `sched/testmain_test.go`
- Test: `sched/shim_test.go`

- [ ] **Step 1: Write the failing test**

Create `sched/shim_test.go`. The tests start real shims: `TestRunner()` is the test binary, which `TestMain` (Step 3) turns into the shim when `ShimTestEnv` is `1`. Every test that starts one leaves no process group behind: `startShim` kills a group whose shim still holds its lock, or whose leader is gone but whose members remain.

```go
//go:build unix

package sched

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// shimLimit bounds every wait on a real process.
const shimLimit = 10 * time.Second

// shimWait polls cond until it holds, and fails the test after limit.
func shimWait(t *testing.T, what string, limit time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %v waiting for %s", limit, what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// shimSpec is a spec for argv, run from a fresh folder in the environment of
// a shell that has only PATH; later env entries override it. The CPU hint is
// 3 and the job's ID 1.
func shimSpec(t *testing.T, argv []string, env ...string) Spec {
	t.Helper()
	return Spec{
		ID:       1,
		Argv:     argv,
		Cwd:      t.TempDir(),
		Env:      append([]string{"PATH=" + os.Getenv("PATH")}, env...),
		HintCPUs: 3,
	}
}

// writeShimJob writes spec under a fresh jobs dir and returns the job's dir.
func writeShimJob(t *testing.T, spec Spec) string {
	t.Helper()
	dir := JobDir(t.TempDir(), spec.ID)
	require.NoError(t, WriteSpec(dir, spec))
	return dir
}

// shimJob writes the job shimSpec describes and returns its dir.
func shimJob(t *testing.T, argv []string, env ...string) string {
	t.Helper()
	return writeShimJob(t, shimSpec(t, argv, env...))
}

// startShim starts the job's shim the way the daemon does, and returns its
// pid, which is also the job's process group. When the test ends it kills
// what the job left behind, but only a group it can prove is the job's: the
// shim's lock held, or the shim gone with members left in the group and
// nothing holding the shim's pid.
func startShim(t *testing.T, dir string) int {
	t.Helper()
	pid, err := TestRunner().Start(dir)
	require.NoError(t, err)
	t.Cleanup(func() {
		if alive, err := Probe(dir); err == nil && alive {
			_ = SignalGroup(pid, syscall.SIGKILL)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		snap, err := Sample(ctx)
		if err != nil {
			return
		}
		if _, _, members, leaderAlive := snap.Group(pid); members > 0 && !leaderAlive {
			_ = SignalGroup(pid, syscall.SIGKILL)
		}
	})
	return pid
}

func shimFileExists(path string) func() bool {
	return func() bool {
		_, err := os.Stat(path)
		return err == nil
	}
}

// shimLockFree reports whether nothing holds the job's run.lock. It also holds
// before the shim has created the file.
func shimLockFree(dir string) func() bool {
	return func() bool {
		alive, err := Probe(dir)
		return err == nil && !alive
	}
}

// shimReadyThen is a command that creates the file ready, then runs script. A
// test that signals the job waits for ready first: the shim passes on no
// signal that arrives before it has started the command.
func shimReadyThen(ready, script string) []string {
	return []string{"sh", "-c", `: > "$1"; ` + script, "sh", ready}
}

// awaitShimExit waits for the shim to write exit.json and release its lock, and
// returns what it wrote.
func awaitShimExit(t *testing.T, dir string) Exit {
	t.Helper()
	var exit Exit
	shimWait(t, "exit.json", shimLimit, func() bool {
		var err error
		exit, err = ReadExit(dir)
		return err == nil
	})
	shimWait(t, "the shim to release run.lock", shimLimit, shimLockFree(dir))
	return exit
}

// shimLog is everything the shim and the command wrote to output.log.
func shimLog(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, LogFile))
	require.NoError(t, err)
	return string(data)
}

// shimOutput is the last line in output.log. A shim that could not lower its
// priority writes a warning before the command's lines.
func shimOutput(t *testing.T, dir string) string {
	t.Helper()
	lines := strings.Split(strings.TrimRight(shimLog(t, dir), "\n"), "\n")
	return lines[len(lines)-1]
}

func TestRunShim_ReportsTheCommandsExit(t *testing.T) {
	tests := []struct {
		name       string
		script     string
		wantCode   int
		wantSignal string
	}{
		{"success", "exit 0", 0, ""},
		{"a failing exit code", "exit 7", 7, ""},
		{"an exit code that looks like a signal's", "exit 137", 137, ""},
		{"killed by a signal", "kill -KILL $$", -1, "SIGKILL"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := time.Now()
			dir := shimJob(t, []string{"sh", "-c", tc.script})
			startShim(t, dir)

			exit := awaitShimExit(t, dir)

			assert.Equal(t, tc.wantCode, exit.Code)
			assert.Equal(t, tc.wantSignal, exit.Signal)
			assert.Empty(t, exit.Error)
			assert.False(t, exit.Ended.Before(before), "exit.json is stamped when the command ended")
			assert.Greater(t, exit.MaxRSSKB, int64(0), "the command's peak memory comes from rusage")
		})
	}
}

func TestRunShim_HoldsTheLockWhileTheCommandRuns(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	dir := shimJob(t, shimReadyThen(ready, "exec sleep 30"))
	pid := startShim(t, dir)
	shimWait(t, "the command to run", shimLimit, shimFileExists(ready))

	started, err := ReadStarted(dir)
	require.NoError(t, err)
	assert.Equal(t, pid, started.PID, "started.json names the shim, which leads the job's group")
	assert.False(t, started.Started.IsZero())
	alive, err := Probe(dir)
	require.NoError(t, err)
	assert.True(t, alive, "the shim holds run.lock while the command runs")
	_, err = ReadExit(dir)
	assert.ErrorIs(t, err, fs.ErrNotExist, "there is no result until the command ends")

	require.NoError(t, SignalGroup(pid, syscall.SIGTERM))
	exit := awaitShimExit(t, dir)

	assert.Equal(t, -1, exit.Code)
	assert.Equal(t, "SIGTERM", exit.Signal)
	alive, err = Probe(dir)
	require.NoError(t, err)
	assert.False(t, alive, "the lock is free once the shim has reported")
}

// grandchildScript starts a child that traps SIGTERM, noting it in marker,
// and waits for it. Its loop ends by itself, so a test that fails leaves no
// process behind for long.
const grandchildScript = `marker=$1
ready=$2
(
	trap 'echo term > "$marker"; exit 0' TERM
	: > "$ready"
	i=0
	while [ "$i" -lt 400 ]; do sleep 0.05; i=$((i + 1)); done
) &
wait
`

func TestRunShim_ACancelReachesTheCommandAndItsChildrenThroughTheGroup(t *testing.T) {
	scratch := t.TempDir()
	marker, ready := filepath.Join(scratch, "marker"), filepath.Join(scratch, "ready")
	dir := shimJob(t, []string{"sh", "-c", grandchildScript, "sh", marker, ready})
	pid := startShim(t, dir)
	shimWait(t, "the grandchild to run", shimLimit, shimFileExists(ready))

	require.NoError(t, SignalGroup(pid, syscall.SIGTERM))
	exit := awaitShimExit(t, dir)

	assert.Equal(t, -1, exit.Code, "the command died of the signal, and the shim stayed up to say so")
	assert.Equal(t, "SIGTERM", exit.Signal)
	shimWait(t, "the grandchild to see the signal", shimLimit, shimFileExists(marker))
}

func TestRunShim_AKilledGroupLeavesNoResultAndFreesTheLock(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	dir := shimJob(t, shimReadyThen(ready, "exec sleep 30"))
	pid := startShim(t, dir)
	shimWait(t, "the command to run", shimLimit, shimFileExists(ready))

	require.NoError(t, SignalGroup(pid, syscall.SIGKILL))
	shimWait(t, "the lock to be free", shimLimit, shimLockFree(dir))

	_, err := ReadExit(dir)
	assert.ErrorIs(t, err, fs.ErrNotExist, "a shim that was killed reports nothing")
}

func TestRunShim_ALeftoverChildDoesNotKeepTheLock(t *testing.T) {
	dir := shimJob(t, []string{"sh", "-c", "sleep 6 & exit 0"})
	startShim(t, dir)
	shimWait(t, "exit.json", shimLimit, func() bool {
		_, err := ReadExit(dir)
		return err == nil
	})

	// The command is gone and its child sleeps on. Had the child inherited
	// run.lock, the lock would stay held until the child ended.
	shimWait(t, "the lock to be free while the child sleeps", 3*time.Second, shimLockFree(dir))
}

func TestRunShim_SetsTheHintsOnlyWhereTheSubmitterDidNot(t *testing.T) {
	script := `echo "$CARGO_BUILD_JOBS $RUST_TEST_THREADS $MAKEFLAGS $LOOM_JOB"`
	dir := shimJob(t, []string{"sh", "-c", script}, "CARGO_BUILD_JOBS=99")
	startShim(t, dir)

	awaitShimExit(t, dir)

	assert.Equal(t, "99 3 -j3 1", shimOutput(t, dir), "the submitter's 99 stays; the others are the spec's 3 cores")
}

func TestRunShim_TheJobSeesOnlyTheSubmittersEnvironment(t *testing.T) {
	t.Setenv("LOOM_SCHED_TEST_LEAK", "from the daemon")
	shimEnv := TestRunner().shimEnv()
	require.Contains(t, shimEnv, "LOOM_SCHED_TEST_LEAK=from the daemon", "the shim itself has the daemon's environment")
	require.Contains(t, shimEnv, ShimTestEnv+"=1", "and the runner's additions")
	script := `echo "${LOOM_SCHED_TEST_LEAK-unset} ${LOOM_SCHED_TEST_SHIM-unset} ${FROM_SPEC-unset}"`
	dir := shimJob(t, []string{"sh", "-c", script}, "FROM_SPEC=yes")
	startShim(t, dir)

	awaitShimExit(t, dir)

	assert.Equal(t, "unset unset yes", shimOutput(t, dir))
}

func TestRunShim_FindsTheCommandOnTheSubmittersPath(t *testing.T) {
	bin := t.TempDir()
	tool := filepath.Join(bin, "loom-sched-test-tool")
	require.NoError(t, os.WriteFile(tool, []byte("#!/bin/sh\necho ran from the job PATH\n"), 0o700))
	_, err := exec.LookPath("loom-sched-test-tool")
	require.Error(t, err, "the shim's own PATH must not find it")
	dir := shimJob(t, []string{"loom-sched-test-tool"}, "PATH="+bin)
	startShim(t, dir)

	exit := awaitShimExit(t, dir)

	assert.Equal(t, 0, exit.Code, "exit.Error is %q", exit.Error)
	assert.Equal(t, "ran from the job PATH", shimOutput(t, dir))
}

func TestRunShim_ReportsACommandThatCannotRun(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		cwd  string
		want string
	}{
		{"the command is on no PATH", []string{"loom-sched-no-such-command"}, "", "not found"},
		{"the folder is gone", []string{"true"}, filepath.Join(t.TempDir(), "gone"), "no such file"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := shimSpec(t, tc.argv)
			if tc.cwd != "" {
				spec.Cwd = tc.cwd
			}
			dir := writeShimJob(t, spec)
			startShim(t, dir)

			exit := awaitShimExit(t, dir)

			assert.Equal(t, 127, exit.Code)
			assert.Empty(t, exit.Signal)
			assert.Contains(t, exit.Error, tc.want)
			_, err := ReadStarted(dir)
			assert.NoError(t, err, "the shim was up, so it says it started")
		})
	}
}

func TestRunShim_LowersTheCommandsPriority(t *testing.T) {
	requirePS(t)
	dir := shimJob(t, []string{"sh", "-c", "ps -o ni= -p $$"})
	startShim(t, dir)

	awaitShimExit(t, dir)

	nice, err := strconv.Atoi(strings.TrimSpace(shimOutput(t, dir)))
	require.NoError(t, err, "ps printed %q", shimOutput(t, dir))
	assert.GreaterOrEqual(t, nice, niceValue, "the command inherits the shim's nice value")
}

func TestRunShim_SetsTheCommandsIOClass(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ioprio_set is Linux's")
	}
	if _, err := exec.LookPath("ionice"); err != nil {
		t.Skip("ionice is not installed")
	}
	dir := shimJob(t, []string{"sh", "-c", "ionice -p $$"})
	startShim(t, dir)

	awaitShimExit(t, dir)

	assert.Contains(t, shimLog(t, dir), "best-effort: prio 7")
}

func TestRunShim_RefusesToRunWhenItLeadsNoGroup(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	dir := shimJob(t, []string{"sh", "-c", `: > "$1"`, "sh", marker})
	exe, err := os.Executable()
	require.NoError(t, err)

	// Started without Setsid, the shim belongs to this test's process group.
	cmd := exec.Command(exe, "job-shim", dir)
	cmd.Env = TestRunner().shimEnv()
	err = cmd.Run()

	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 1, exitErr.ExitCode())
	assert.NoFileExists(t, marker, "the command must not run")
	_, err = ReadStarted(dir)
	assert.ErrorIs(t, err, fs.ErrNotExist)
	exit, err := ReadExit(dir)
	require.NoError(t, err)
	assert.Equal(t, 127, exit.Code)
	assert.Contains(t, exit.Error, "process group")
}

func TestResolveCommand(t *testing.T) {
	cwd := t.TempDir()
	first, second, rel := filepath.Join(cwd, "first"), filepath.Join(cwd, "second"), filepath.Join(cwd, "rel")
	for _, d := range []string{first, second, rel, filepath.Join(first, "dirtool")} {
		require.NoError(t, os.MkdirAll(d, 0o755))
	}
	write := func(path string, mode os.FileMode) {
		require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"), 0o700))
		require.NoError(t, os.Chmod(path, mode))
	}
	write(filepath.Join(first, "tool"), 0o755)
	write(filepath.Join(second, "tool"), 0o755)
	write(filepath.Join(first, "nonexec"), 0o644)
	write(filepath.Join(second, "nonexec"), 0o755)
	write(filepath.Join(second, "dirtool"), 0o755)
	write(filepath.Join(rel, "reltool"), 0o755)
	write(filepath.Join(cwd, "cwdtool"), 0o755)
	both := "PATH=" + first + ":" + second

	tests := []struct {
		name  string
		env   []string
		argv0 string
		want  string // "" means not found
	}{
		{"the first directory with an executable wins", []string{both}, "tool", filepath.Join(first, "tool")},
		{"a file without an exec bit is skipped", []string{both}, "nonexec", filepath.Join(second, "nonexec")},
		{"a directory of that name is skipped", []string{both}, "dirtool", filepath.Join(second, "dirtool")},
		{"a relative PATH entry is the folder's", []string{"PATH=rel"}, "reltool", filepath.Join(rel, "reltool")},
		{"an empty PATH entry is the folder itself", []string{"PATH=:"}, "cwdtool", filepath.Join(cwd, "cwdtool")},
		{"the last PATH of the environment wins", []string{"PATH=" + first, "PATH=" + second}, "nonexec", filepath.Join(second, "nonexec")},
		{"a name with a slash is taken as it is", []string{"PATH="}, "./cwdtool", "./cwdtool"},
		{"a name on no PATH is not found", []string{both}, "nothing-like-it", ""},
		{"an environment without PATH finds nothing, even what the shim's PATH holds", nil, "sh", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveCommand(tc.argv0, tc.env, cwd)
			if tc.want == "" {
				require.ErrorIs(t, err, exec.ErrNotFound)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestProbe_ReadsTheLockAndLeavesItFree(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, LockFile)

	alive, err := Probe(dir)
	require.NoError(t, err)
	assert.False(t, alive, "no run.lock: no shim has taken it")

	held, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o600)
	require.NoError(t, err)
	t.Cleanup(func() { _ = held.Close() })
	require.NoError(t, syscall.Flock(int(held.Fd()), syscall.LOCK_EX))
	alive, err = Probe(dir)
	require.NoError(t, err)
	assert.True(t, alive, "a lock held elsewhere reads as a living shim")

	require.NoError(t, held.Close())
	alive, err = Probe(dir)
	require.NoError(t, err)
	assert.False(t, alive, "a released lock reads as a shim that is gone")

	again, err := os.Open(lockPath)
	require.NoError(t, err)
	defer again.Close()
	assert.NoError(t, syscall.Flock(int(again.Fd()), syscall.LOCK_EX|syscall.LOCK_NB), "Probe must not keep the lock it took")
}

func TestSignalGroup_RefusesAGroupItCouldNotMean(t *testing.T) {
	for _, pgid := range []int{-5, 0, 1, syscall.Getpgrp()} {
		err := SignalGroup(pgid, 0)
		assert.Error(t, err, "pgid %d", pgid)
	}
}

func TestSignalGroup_ReportsAGroupThatIsGone(t *testing.T) {
	dir := shimJob(t, []string{"true"})
	pid := startShim(t, dir)
	awaitShimExit(t, dir)

	// Until Start's goroutine has reaped the shim, its zombie still belongs
	// to the group.
	shimWait(t, "the group to be gone", shimLimit, func() bool {
		return errors.Is(SignalGroup(pid, 0), syscall.ESRCH)
	})
}

func TestNewRunner_UsesThisExecutable(t *testing.T) {
	exe, err := os.Executable()
	require.NoError(t, err)

	r, err := NewRunner()

	require.NoError(t, err)
	assert.Equal(t, []string{exe, "job-shim"}, r.Shim)
	assert.Empty(t, r.Env)
}

func TestRunnerStart_AppendsToTheLog(t *testing.T) {
	dir := shimJob(t, []string{"sh", "-c", "echo out; echo err >&2"})
	require.NoError(t, os.WriteFile(filepath.Join(dir, LogFile), []byte("earlier\n"), 0o600))
	startShim(t, dir)

	awaitShimExit(t, dir)

	log := shimLog(t, dir)
	assert.True(t, strings.HasPrefix(log, "earlier\n"), "the log is appended to, not truncated: %q", log)
	assert.True(t, strings.HasSuffix(log, "out\nerr\n"), "stdout and stderr both land in it: %q", log)
}

func TestRunnerStart_GivesTheShimNoStdinAndTheLogForOutput(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc/<pid>/fd")
	}
	ready := filepath.Join(t.TempDir(), "ready")
	dir := shimJob(t, shimReadyThen(ready, "exec sleep 30"))
	pid := startShim(t, dir)
	shimWait(t, "the command to run", shimLimit, shimFileExists(ready))
	logPath, err := filepath.EvalSymlinks(filepath.Join(dir, LogFile))
	require.NoError(t, err)

	for fd, want := range map[int]string{0: os.DevNull, 1: logPath, 2: logPath} {
		got, err := os.Readlink(fmt.Sprintf("/proc/%d/fd/%d", pid, fd))
		require.NoError(t, err)
		assert.Equal(t, want, got, "fd %d of the shim", fd)
	}

	require.NoError(t, SignalGroup(pid, syscall.SIGTERM))
	awaitShimExit(t, dir)
}

func TestRunnerStart_RefusesWhatItCannotStart(t *testing.T) {
	dir := shimJob(t, []string{"true"})
	tests := []struct {
		name   string
		runner Runner
		dir    string
	}{
		{"no shim command", Runner{}, dir},
		{"a shim that does not exist", Runner{Shim: []string{filepath.Join(t.TempDir(), "no-such-loom")}}, dir},
		{"a job dir that does not exist", TestRunner(), filepath.Join(t.TempDir(), "gone")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pid, err := tc.runner.Start(tc.dir)
			assert.Error(t, err)
			assert.Zero(t, pid)
		})
	}
}

func TestSignalName(t *testing.T) {
	tests := []struct {
		sig  syscall.Signal
		want string
	}{
		{syscall.SIGKILL, "SIGKILL"},
		{syscall.SIGTERM, "SIGTERM"},
		{syscall.SIGSEGV, "SIGSEGV"},
		{syscall.SIGHUP, "SIGHUP"},
		{syscall.Signal(63), "SIG63"},
	}
	for _, tc := range tests {
		got := signalName(tc.sig)
		assert.Equal(t, tc.want, got)
		if unix.SignalName(tc.sig) != "" {
			assert.Equal(t, tc.sig, unix.SignalNum(got), "the follower maps the name back to the signal")
		}
	}
}

func TestSample_SeesAJobsWholeGroup(t *testing.T) {
	requirePS(t)
	ready := filepath.Join(t.TempDir(), "ready")
	dir := shimJob(t, []string{"sh", "-c", `sleep 30 & : > "$1"; wait`, "sh", ready})
	pid := startShim(t, dir)
	shimWait(t, "the command to run", shimLimit, shimFileExists(ready))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	snap, err := Sample(ctx)
	require.NoError(t, err)

	rss, cpu, members, leaderAlive := snap.Group(pid)
	assert.GreaterOrEqual(t, members, 3, "the shim, the command's shell and its sleep")
	assert.True(t, leaderAlive, "the shim leads the group")
	assert.Greater(t, rss, int64(0))
	assert.GreaterOrEqual(t, cpu, time.Duration(0))

	require.NoError(t, SignalGroup(pid, syscall.SIGTERM))
	awaitShimExit(t, dir)
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `CGO_ENABLED=0 go test ./sched/ -run 'TestRunShim_|TestResolveCommand|TestProbe_|TestSignalGroup_|TestNewRunner_|TestRunnerStart_|TestSignalName|TestSample_SeesAJobsWholeGroup'`

Expected: the package fails to build. `go` prints the first ten errors (`undefined: Probe`, `undefined: SignalGroup`, `undefined: TestRunner`, `undefined: ShimTestEnv`, `undefined: resolveCommand`, `undefined: signalName`, `undefined: NewRunner`, `undefined: Runner`, `undefined: niceValue` and more) and `too many errors`, and the run ends `FAIL github.com/aidan-bailey/loom/sched [build failed]`.

- [ ] **Step 3: Implement**

Create `sched/shim.go`:

```go
package sched

import (
	"errors"
	"fmt"
	"os"
)

// ErrUnsupported is what the Windows stubs answer in place of running,
// probing or signalling a job: the runner leans on sessions, process groups
// and flock, which Windows has no counterpart for here.
var ErrUnsupported = errors.New("the work queue isn't available on Windows")

// Runner starts the shim processes that run jobs.
type Runner struct {
	// Shim is the command line before the job dir: {exe, "job-shim"}.
	Shim []string
	// Env is added to the shim process's own environment. It never reaches
	// the job, whose environment comes from its spec.json alone.
	Env []string
}

// NewRunner returns a Runner that starts this process's own executable as
// "job-shim".
func NewRunner() (Runner, error) {
	exe, err := os.Executable()
	if err != nil {
		return Runner{}, fmt.Errorf("finding loom's own executable: %w", err)
	}
	return Runner{Shim: []string{exe, "job-shim"}}, nil
}

// shimEnv is the shim process's environment: this process's own, then r.Env.
// Of two entries for one variable the later wins, as exec.Cmd.Env defines.
func (r Runner) shimEnv() []string {
	return append(os.Environ(), r.Env...)
}
```

Create `sched/shim_unix.go`:

```go
//go:build unix

package sched

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// niceValue is the nice value the shim, and everything it starts, runs at.
const niceValue = 10

// Start starts a shim for the job in jobDir: this program's "job-shim", as a
// child in a session of its own (so it leads a process group whose id is its
// pid, the job's), with stdin on /dev/null and stdout and stderr appended to
// output.log. A goroutine reaps it. The shim's environment is this
// process's plus r.Env. Start returns once the child exists: the shim says
// it is up by writing started.json.
func (r Runner) Start(jobDir string) (pid int, err error) {
	if len(r.Shim) == 0 {
		return 0, errors.New("no job-shim command to start")
	}
	logFile, err := os.OpenFile(filepath.Join(jobDir, LogFile), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return 0, fmt.Errorf("opening the job's log: %w", err)
	}
	defer logFile.Close()

	cmd := exec.Command(r.Shim[0], append(slices.Clone(r.Shim[1:]), jobDir)...)
	cmd.Env = r.shimEnv()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("starting the job runner: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	return cmd.Process.Pid, nil
}

// Probe tests whether the job's shim lives, by trying to take run.lock
// without waiting and letting go of it at once. The shim holds the lock from
// before it writes started.json until after it writes exit.json, and the
// kernel drops it if the shim dies, so a held lock means a living shim and a
// free one a shim that is gone. A run.lock that does not exist yet reads as
// free: no shim has taken it. Probe does not prove that a pid recorded for
// the job is still the shim's; the lock does, which is why callers signal a
// job's group only after finding it held.
func Probe(jobDir string) (alive bool, err error) {
	f, err := os.Open(filepath.Join(jobDir, LockFile))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("opening the job's lock: %w", err)
	}
	defer f.Close()
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	switch {
	case err == nil:
		return false, nil // closing f releases the lock again
	case errors.Is(err, syscall.EWOULDBLOCK):
		return true, nil
	default:
		return false, fmt.Errorf("testing the job's lock: %w", err)
	}
}

// SignalGroup sends sig to every process of process group pgid. It refuses a
// pgid of 1 or below and this process's own group, which kill(2) would read
// as "my group" (0) or "every process" (-1) or which is the daemon's own. An
// ESRCH error means the group is gone. SignalGroup cannot tell the job's
// group from one that a reused pid now leads: the caller proves the group is
// the job's first (see Probe and Snapshot.Group).
func SignalGroup(pgid int, sig syscall.Signal) error {
	if pgid <= 1 {
		return fmt.Errorf("refusing to signal process group %d: it is no job's", pgid)
	}
	if pgid == syscall.Getpgrp() {
		return fmt.Errorf("refusing to signal process group %d: it is this process's own", pgid)
	}
	return syscall.Kill(-pgid, sig)
}

// RunShim is the shim's whole life (spec §5.3). It runs the job in jobDir and
// writes how it ended, and returns the shim's own exit code: 0 once exit.json
// is written, 1 when it could not run the job or could not report. It prints
// to its stderr, which is output.log, and never to the job's.
//
// The shim must lead its own process group, which Runner.Start arranges: the
// daemon signals the group -pid as the job's. It holds an exclusive flock on
// run.lock from before it writes started.json to after it writes exit.json.
func RunShim(jobDir string) int {
	// Linux keeps the nice value and the I/O class per thread, and a forked
	// child inherits its forking thread's. Pin this goroutine to its thread,
	// set both on it, and start the command from it.
	runtime.LockOSThread()

	// Catching these keeps the shim up when a signal sent to the group
	// reaches it along with the command, which that same send signals; the
	// shim stays to report. A caught signal is back at its default in the
	// command, an ignored one would stay ignored across exec.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer signal.Stop(sigs)

	if syscall.Getpgrp() != os.Getpid() {
		return shimGiveUp(jobDir, errors.New("the shim must lead its own process group: the daemon starts it with Setsid"))
	}
	spec, err := ReadSpec(jobDir)
	if err != nil {
		return shimGiveUp(jobDir, fmt.Errorf("reading spec.json: %w", err))
	}
	if len(spec.Argv) == 0 {
		return shimGiveUp(jobDir, errors.New("spec.json has no command"))
	}
	if err := lowerPriority(); err != nil {
		// Not fatal: a job at the default priority is still a job.
		fmt.Fprintf(os.Stderr, "loom job-shim: lowering the priority: %v\n", err)
	}

	lock, err := os.OpenFile(filepath.Join(jobDir, LockFile), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return shimGiveUp(jobDir, fmt.Errorf("opening run.lock: %w", err))
	}
	// Go opens the file close-on-exec, so the command never holds the lock.
	// The deferred Close lets it go after exit.json is written, and keeps
	// the file reachable until then, since a finalizer would drop the lock.
	defer lock.Close()
	if err := lockExclusive(lock); err != nil {
		return shimGiveUp(jobDir, fmt.Errorf("locking run.lock: %w", err))
	}
	if err := writeStarted(jobDir, Started{PID: os.Getpid(), Started: time.Now()}); err != nil {
		return shimGiveUp(jobDir, fmt.Errorf("writing started.json: %w", err))
	}

	// The job's environment is the spec's and the hints: nothing of this
	// process's own, which is the daemon's. Hints returns a new slice of
	// spec.Env, the hint variables it lacks and LOOM_JOB, and the command
	// runs with exactly that.
	env := Hints(spec.Env, max(spec.HintCPUs, 1), spec.ID)
	cmd, err := startCommand(spec, env)
	if err != nil {
		return shimReport(jobDir, Exit{Code: 127, Ended: time.Now(), Error: err.Error()})
	}
	waitErr := cmd.Wait()
	return shimReport(jobDir, exitOf(cmd.ProcessState, waitErr))
}

// lockExclusive takes an exclusive flock on f, waiting for it: the daemon's
// Probe holds the lock for an instant, and the shim must outlast that.
func lockExclusive(f *os.File) error {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}

// shimReport writes the job's result and returns the shim's exit code.
func shimReport(jobDir string, e Exit) int {
	if err := writeExit(jobDir, e); err != nil {
		fmt.Fprintf(os.Stderr, "loom job-shim: writing exit.json: %v\n", err)
		return 1
	}
	return 0
}

// shimGiveUp reports a job the shim could not run, as the command that
// could not be executed (code 127) with cause as the reason, and returns 1.
func shimGiveUp(jobDir string, cause error) int {
	fmt.Fprintf(os.Stderr, "loom job-shim: %v\n", cause)
	_ = writeExit(jobDir, Exit{Code: 127, Ended: time.Now(), Error: cause.Error()})
	return 1
}

// startCommand starts spec's argv in its folder with env as its whole
// environment, no shell and stdin on /dev/null. The shim's stdout and
// stderr, which are output.log, are the command's.
func startCommand(spec Spec, env []string) (*exec.Cmd, error) {
	path, err := resolveCommand(spec.Argv[0], env, spec.Cwd)
	if err != nil {
		return nil, err
	}
	cmd := &exec.Cmd{
		Path:   path,
		Args:   spec.Argv,
		Dir:    spec.Cwd,
		Env:    env,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

// resolveCommand finds the executable argv0 names the way a shell holding
// env would. A name with a slash is taken as it is (a relative one is
// relative to the job's folder, which exec.Cmd does). Any other is looked for
// along env's PATH, the last PATH entry of env winning; an empty element is
// the folder and a relative one is the folder's. The shim's own PATH is never
// consulted: it is the daemon's, and the job must run the program its
// submitter's shell would have run. A name found nowhere is an exec.Error
// wrapping exec.ErrNotFound.
func resolveCommand(argv0 string, env []string, cwd string) (string, error) {
	if strings.Contains(argv0, "/") {
		return argv0, nil
	}
	path, _ := lastEnvValue(env, "PATH")
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			dir = "."
		}
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(cwd, dir)
		}
		candidate := filepath.Join(dir, argv0)
		if isExecutableFile(candidate) {
			return candidate, nil
		}
	}
	return "", &exec.Error{Name: argv0, Err: exec.ErrNotFound}
}

// lastEnvValue returns the value of key in env, the last entry for it winning.
func lastEnvValue(env []string, key string) (string, bool) {
	for i := len(env) - 1; i >= 0; i-- {
		if v, ok := strings.CutPrefix(env[i], key+"="); ok {
			return v, true
		}
	}
	return "", false
}

// isExecutableFile reports whether path is a regular file with an execute bit.
func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

// exitOf describes how the command ended. Code is the exit code, or -1 and
// the signal's name when a signal ended it; MaxRSSKB is the command's, and
// its waited-for descendants', peak memory from rusage.
func exitOf(ps *os.ProcessState, waitErr error) Exit {
	if ps == nil {
		return Exit{Code: 127, Ended: time.Now(), Error: fmt.Sprintf("waiting for the command: %v", waitErr)}
	}
	exit := Exit{Code: ps.ExitCode(), Ended: time.Now()} // -1 when a signal ended it
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		exit.Signal = signalName(ws.Signal())
	}
	if ru, ok := ps.SysUsage().(*syscall.Rusage); ok && ru != nil {
		exit.MaxRSSKB = maxRSSKB(ru)
	}
	var exitErr *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exitErr) {
		exit.Error = waitErr.Error()
	}
	return exit
}

// maxRSSKB is rusage's peak resident size in kilobytes: Linux reports
// kilobytes, macOS bytes.
func maxRSSKB(ru *syscall.Rusage) int64 {
	n := int64(ru.Maxrss)
	if runtime.GOOS == "darwin" {
		return n / 1024
	}
	return n
}

// signalName is unix.SignalName's name for sig ("SIGKILL"), or "SIG<n>" for
// one it has no name for, so that a signal's end is never recorded as none.
// syscall.Signal.String gives prose ("killed").
func signalName(sig syscall.Signal) string {
	if name := unix.SignalName(sig); name != "" {
		return name
	}
	return fmt.Sprintf("SIG%d", int(sig))
}
```

Create `sched/shim_windows.go`:

```go
package sched

import (
	"fmt"
	"os"
	"syscall"
)

// RunShim has no Windows counterpart: it says so and fails.
func RunShim(string) int {
	fmt.Fprintln(os.Stderr, "loom job-shim:", ErrUnsupported)
	return 1
}

// Start starts nothing on Windows.
func (Runner) Start(string) (pid int, err error) { return 0, ErrUnsupported }

// Probe tests nothing on Windows.
func Probe(string) (alive bool, err error) { return false, ErrUnsupported }

// SignalGroup signals nothing on Windows.
func SignalGroup(int, syscall.Signal) error { return ErrUnsupported }
```

Create `sched/prio_linux.go`:

```go
package sched

import (
	"errors"
	"fmt"
	"syscall"
)

// ioprioWhoProcess and ioprioBestEffort7 are ioprio_set's arguments: the
// calling thread (who 0), and (class best-effort = 2) << 13 | level 7.
const (
	ioprioWhoProcess  = 1
	ioprioBestEffort7 = 2<<13 | 7
)

// lowerPriority sets the calling thread's nice value to niceValue and its I/O
// class to best-effort level 7. Both are per thread on Linux and inherited
// by what the thread forks; RunShim calls it on the thread that starts the
// command. A failure of either is returned, and the other still applies.
func lowerPriority() error {
	var errs []error
	if err := syscall.Setpriority(syscall.PRIO_PROCESS, 0, niceValue); err != nil {
		errs = append(errs, fmt.Errorf("setpriority: %w", err))
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOPRIO_SET, ioprioWhoProcess, 0, ioprioBestEffort7); errno != 0 {
		errs = append(errs, fmt.Errorf("ioprio_set: %w", errno))
	}
	return errors.Join(errs...)
}
```

Create `sched/prio_other.go`:

```go
//go:build unix && !linux

package sched

import (
	"fmt"
	"syscall"
)

// lowerPriority sets the process's nice value to niceValue. These platforms
// have no I/O class to set.
func lowerPriority() error {
	if err := syscall.Setpriority(syscall.PRIO_PROCESS, 0, niceValue); err != nil {
		return fmt.Errorf("setpriority: %w", err)
	}
	return nil
}
```

Create `sched/testshim.go`:

```go
package sched

import (
	"fmt"
	"os"
)

// ShimTestEnv, set to "1" in a process's environment, makes MaybeRunTestShim
// turn that process into a job shim.
const ShimTestEnv = "LOOM_SCHED_TEST_SHIM"

// MaybeRunTestShim runs the shim and exits, when ShimTestEnv is "1": the last
// argument is the job dir, as Runner.Start passes it. It is the first
// statement of a TestMain whose package starts real shims, since a test
// binary has no job-shim command; a binary started as the shim without it
// would run the whole suite again, and start more shims.
func MaybeRunTestShim() {
	if os.Getenv(ShimTestEnv) != "1" {
		return
	}
	os.Exit(RunShim(os.Args[len(os.Args)-1]))
}

// TestRunner is a Runner that starts the running test binary as the shim,
// with ShimTestEnv set to "1".
func TestRunner() Runner {
	exe, err := os.Executable()
	if err != nil {
		panic(fmt.Sprintf("sched.TestRunner: finding the test binary: %v", err))
	}
	return Runner{Shim: []string{exe, "job-shim"}, Env: []string{ShimTestEnv + "=1"}}
}
```

Edit `sched/testmain_test.go` (Story 1's file; the contract pins its text). Insert `MaybeRunTestShim()` as `TestMain`'s first statement and change nothing else. Replace

```go
func TestMain(m *testing.M) {
	cleanup := testenv.MustIsolateLoomDirs()
```

with

```go
func TestMain(m *testing.M) {
	MaybeRunTestShim()
	cleanup := testenv.MustIsolateLoomDirs()
```

- [ ] **Step 4: Run it to see it pass**

Run: `CGO_ENABLED=0 go test ./sched/ -run 'TestRunShim_|TestResolveCommand|TestProbe_|TestSignalGroup_|TestNewRunner_|TestRunnerStart_|TestSignalName|TestSample_SeesAJobsWholeGroup' -v`

Expected: every `--- PASS` (`TestRunShim_SetsTheCommandsIOClass`, `TestRunShim_LowersTheCommandsPriority`, `TestSample_SeesAJobsWholeGroup` and `TestRunnerStart_GivesTheShimNoStdinAndTheLogForOutput` are a `--- SKIP` where `ionice`, `ps` or `/proc` is missing), ending `ok  	github.com/aidan-bailey/loom/sched`.

Then the package as a whole and its checks:
- `CGO_ENABLED=0 go test ./sched/...` ends `ok`.
- `CC=clang CGO_ENABLED=1 go test -race ./sched/ -run 'TestRunShim_|TestRunnerStart_|TestSample_'` ends `ok` (drop `CC=clang` where gcc exists).
- `gofmt -l sched` prints nothing, and `CGO_ENABLED=0 go vet ./sched/` prints nothing.
- `GOOS=windows CGO_ENABLED=0 go build ./sched/` and `GOOS=darwin CGO_ENABLED=0 go build ./sched/` print nothing.
- `pgrep -f 'sched.test'` finds no leftover shim or command.

- [ ] **Step 5: Commit**

```bash
git add -N sched/shim.go sched/shim_unix.go sched/shim_windows.go sched/prio_linux.go sched/prio_other.go sched/testshim.go sched/shim_test.go
git commit -m "feat(sched): add the job shim, its runner, lock probe and group signals" -- sched/shim.go sched/shim_unix.go sched/shim_windows.go sched/prio_linux.go sched/prio_other.go sched/testshim.go sched/shim_test.go sched/testmain_test.go
```

### Task 2.4: The `loom job-shim` command, `procps` for the flake, and the runtime's rules and facts

**Files:**
- Create: `job_shim.go`
- Modify: `main.go:416-418`
- Modify: `flake.nix:85-106`
- Modify: `sched/CLAUDE.md`
- Modify: `sched/README.md`
- Test: `job_shim_test.go`

The command is three lines over `sched.RunShim`, which Task 2.3 tests with the test binary as the shim, so its own test pins what the daemon relies on: the command exists on `rootCmd` under the name `Runner.Start` passes, takes the job dir and nothing else, and stays out of `--help`. Story 5's e2e test runs the real command, started by a real daemon. The root package's existing `TestMain` (`testmain_test.go`) isolates the loom dirs; this test adds no `TestMain` of its own. The other checks here are the builds for three operating systems, `go vet`, the CLAUDE.md lint and the flake's evaluation.

- [ ] **Step 1: Write the failing test**

Create `job_shim_test.go`:

```go
package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJobShimCmd_IsHiddenTakesOneArgumentAndIsRegistered(t *testing.T) {
	assert.True(t, jobShimCmd.Hidden, "only the daemon runs it, so --help does not list it")

	require.Error(t, jobShimCmd.Args(jobShimCmd, nil), "no job dir")
	require.NoError(t, jobShimCmd.Args(jobShimCmd, []string{"/jobs/j1"}))
	require.Error(t, jobShimCmd.Args(jobShimCmd, []string{"/jobs/j1", "extra"}), "one job dir and no more")

	// Runner.Start runs "<loom> job-shim <job dir>": rootCmd must resolve
	// that to this command.
	found, rest, err := rootCmd.Find([]string{"job-shim", "/jobs/j1"})
	require.NoError(t, err)
	assert.Same(t, jobShimCmd, found, "registered on rootCmd under the name the runner starts")
	assert.Equal(t, []string{"/jobs/j1"}, rest)
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `CGO_ENABLED=0 go test . -run TestJobShimCmd`

Expected: the package fails to build, `go` printing `./job_shim_test.go:…: undefined: jobShimCmd` for each use, and the run ends `FAIL github.com/aidan-bailey/loom [build failed]`.

- [ ] **Step 3: Write the command**

Create `job_shim.go`:

```go
package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/aidan-bailey/loom/sched"
)

// jobShimCmd is the process a scheduled job runs under (spec §5.3): the
// daemon starts it with sched.Runner.Start, and it lives until the job's
// command exits. Hidden, since only the daemon runs it.
var jobShimCmd = &cobra.Command{
	Use:    "job-shim <job dir>",
	Short:  "Run one scheduled job (started by the daemon)",
	Hidden: true,
	Args:   cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		os.Exit(sched.RunShim(args[0]))
	},
}
```

- [ ] **Step 4: Register it in `main.go`**

In `main.go`, in `init()`, replace

```go
	rootCmd.AddCommand(debugCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(resetCmd)
```

with

```go
	rootCmd.AddCommand(debugCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(resetCmd)
	rootCmd.AddCommand(jobShimCmd)
```

Change no import: `job_shim.go` holds the new ones.

- [ ] **Step 5: Put `ps` where the sampler and the daemon find it (`flake.nix`)**

`Sample` runs `ps`, and the sampler's tests skip without it; under Nix neither the check phase nor the wrapped daemon has one. Procps is Linux's `ps` (macOS has its own), so the additions are guarded to Linux. In `flake.nix`, replace

```nix
            nativeCheckInputs = [
              pkgs.gh
              pkgs.git
              pkgs.tmux
            ];
```

with

```nix
            nativeCheckInputs = [
              pkgs.gh
              pkgs.git
              pkgs.tmux
            ] ++ pkgs.lib.optionals pkgs.stdenv.isLinux [
              # ps, for the work queue's usage sampler (sched.Sample)
              pkgs.procps
            ];
```

and replace

```nix
                --prefix PATH : ${
                  pkgs.lib.makeBinPath [
                    pkgs.tmux
                    pkgs.git
                    pkgs.gh
                  ]
                }
```

with

```nix
                --prefix PATH : ${
                  pkgs.lib.makeBinPath (
                    [
                      pkgs.tmux
                      pkgs.git
                      pkgs.gh
                    ]
                    ++ pkgs.lib.optionals pkgs.stdenv.isLinux [ pkgs.procps ]
                  )
                }
```

Change no other line of `flake.nix`.

- [ ] **Step 6: Write the rules and the facts**

In `sched/CLAUDE.md`, add these bullets at the end of the `## Rules when modifying this package` section: directly after Story 1's last bullet, before the blank line that precedes `## Pointers`. Change no other line.

```markdown
- **Signal a job's process group only through `SignalGroup`, and only once the caller has proved the group is the job's.** `SignalGroup` refuses a pgid of 1 or below and this process's own group, since `kill(0)` signals the caller's group and `kill(-1)` every process; it cannot tell a group that is still the job's from one a reused pid now leads, so the proof is the caller's: the shim's `run.lock` held (`Probe`), or, for a lost job, a sample showing members in the group and no process with the shim's pid (`Snapshot.Group`). A signal to a group that is not the job's kills unrelated work, silently. **Enforced** for the refusals by `TestSignalGroup_RefusesAGroupItCouldNotMean`; **Convention** for the proof, which is the caller's — a SIGKILL to someone else's group.
- **Build the job's environment from its `spec.json` and the hints alone, and the shim process's from `os.Environ()` plus `Runner.Env`.** The shim runs in the daemon's environment (its tmux server, `CLAUDE_CONFIG_DIR`, `LOOM_*` variables); passing that on would make every build behave unlike the one the submitter's shell would have run. **Enforced** by `TestRunShim_TheJobSeesOnlyTheSubmittersEnvironment`.
- **Find the command on the `PATH` of the job's environment, never the shim's.** The shim's `PATH` is the daemon's, and the submitter's shell chose the `cargo` or `python` it meant, so a lookup in the daemon's finds another build or none. **Enforced** by `TestResolveCommand` and `TestRunShim_FindsTheCommandOnTheSubmittersPath`.
- **Catch the shim's signals with `signal.Notify`, never ignore them, and leave delivery to the command to the group send.** An ignored signal stays ignored across `exec`, so the command would shrug off its cancel and run until the daemon's SIGKILL; a forwarded copy on top of the group's would signal the command twice. **Enforced** by `TestRunShim_ACancelReachesTheCommandAndItsChildrenThroughTheGroup`.
- **Write `exit.json` before the shim lets go of `run.lock`, and keep the lock close-on-exec (Go's default for `os.OpenFile`).** A free lock is the daemon's cue that the shim is gone and `exit.json` is final; a lock released first settles a finished job `lost` and has its group killed, and a lock a leftover child inherited stays held after the shim exits, so the job never settles. **Enforced** for the child by `TestRunShim_ALeftoverChildDoesNotKeepTheLock` and for the held and released states by `TestRunShim_HoldsTheLockWhileTheCommandRuns`; **Convention** for the write coming first — a `lost` job that finished.
- **Run the shim as the leader of its own process group, and refuse to run as anything else.** The daemon signals `-pid` as the job's group and starts the shim with `Setsid`; a shim that led no group would leave every signal and sample to a group that is not the job's. **Enforced** by `TestRunShim_RefusesToRunWhenItLeadsNoGroup`.
- **Lower the nice value and I/O class on the thread that forks the command (`runtime.LockOSThread` at the top of `RunShim`).** Linux keeps both per thread and a child inherits its forking thread's, so a setting made on another thread leaves the command at the default priority, silently. **Enforced** for the settings by `TestRunShim_LowersTheCommandsPriority` and `TestRunShim_SetsTheCommandsIOClass`; **Convention** for the pinning — a command that runs at full priority now and then.
- **Keep syscalls and process control in `shim_unix.go`, `prio_linux.go`, `prio_other.go` and `host_*.go`, leave `shim_windows.go` as stubs that answer `ErrUnsupported`, and run `GOOS=windows CGO_ENABLED=0 go build ./...` and the same for `darwin` after touching them.** CI builds no Windows binary, but the release does. **Convention** — a release that fails to build.
- **Call `MaybeRunTestShim()` first in the `TestMain` of every package whose tests start real shims, and run their jobs through `TestRunner()`.** A test binary has no `job-shim` command; started as the shim without the hook it runs the whole suite again, and each run starts more shims. **Convention** — a runaway test run.
```

In `sched/README.md`, add one section at the end of the file, after a blank line, and change nothing above it:

```markdown
## The runtime

The process half of the package: it starts a job's runner, tests whether the runner lives, signals the job's process group, measures it and reads the host. It keeps no record of any job; `core` decides what each answer means.

| File | Holds |
|---|---|
| `shim.go` | `Runner`, `NewRunner`, `ErrUnsupported` |
| `shim_unix.go` | `RunShim`, `Runner.Start`, `Probe`, `SignalGroup` |
| `shim_windows.go` | the same four names, each answering `ErrUnsupported` |
| `prio_linux.go`, `prio_other.go` | the shim's nice value and, on Linux, its I/O class |
| `sample.go` | `Sample`, `ParsePS`, `Snapshot.Group` |
| `host.go`, `host_linux.go`, `host_darwin.go`, `host_other.go` | `DetectHost` and the RAM readers |
| `testshim.go` | `ShimTestEnv`, `MaybeRunTestShim`, `TestRunner` |

**The shim.** `loom job-shim <job dir>` (`job_shim.go` in the root package) calls `RunShim`. The daemon starts it with `Runner.Start` as `<loom> job-shim <job dir>`, a child in a session of its own (`Setsid`), so it leads a process group whose id is its pid. Its stdin is `/dev/null`, and its stdout and stderr are appended to `output.log`. It:
1. refuses to run unless it leads its group;
2. reads `spec.json` and sets nice 10 and, on Linux, the best-effort I/O class at level 7, on the thread that will start the command, so the command and its children inherit both;
3. takes an exclusive `flock` on `run.lock`, opened close-on-exec so nothing it starts holds the lock, and writes `started.json`;
4. starts the command with no shell, in the job's folder, with the environment of `spec.json` plus the parallelism hints (`Hints`), found on that environment's `PATH`;
5. catches SIGTERM, SIGINT and SIGHUP and passes none on, since a signal sent to the group reaches the command itself; one that arrives before the command starts is lost, and the daemon's SIGKILL escalation covers it;
6. writes `exit.json` (a signal ends a job with code -1 and the name `unix.SignalName` gives it, a command that cannot be executed with code 127 and the error) and returns, which releases the lock.

A shim that cannot run the job writes an `exit.json` with code 127 and the reason, and returns 1. It prints to its stderr, which is `output.log`.

**Asking about a job.** `Probe` is a non-blocking `flock` test of `run.lock`, released at once: held means the shim lives, free means it is gone, and no `run.lock` yet also reads free, since no shim has taken it. `SignalGroup(pgid, sig)` is `kill(-pgid, sig)`; it refuses a pgid of 1 or below and the caller's own group, and answers `ESRCH` for a group that is gone. Neither proves that a group is still the job's: the caller does, with the lock held, or with a `Sample` showing members in the group and no process holding the shim's pid.

**Usage.** `Sample` runs `ps -A -o pid=,pgid=,rss=,time=` once. `ParsePS` reads the procps time column (`[d-]hh:mm:ss`) and the BSD one (`[[h:]m:]s.cc`), and fails the whole sample on a line it cannot read. `Snapshot.Group(pgid)` sums a group's memory and CPU time, counts its members, and says whether any process has the group's id as its pid. `DetectHost` reads the host's name, `runtime.NumCPU()` and its RAM (`/proc/meminfo`, or `sysctl hw.memsize` with a 5 s limit); a RAM it cannot read stays 0.

**Windows.** The stubs answer `ErrUnsupported`: sessions, process groups and `flock` have no counterpart here.

**Tests.** A test binary has no `job-shim` command, so a package whose tests start real shims calls `MaybeRunTestShim()` first in its `TestMain` and builds its runner with `TestRunner()`: the test binary, run with `ShimTestEnv` set to `1`, becomes the shim.
```

- [ ] **Step 7: Run the checks**

- `CGO_ENABLED=0 go test . -run TestJobShimCmd -v` shows `--- PASS: TestJobShimCmd_IsHiddenTakesOneArgumentAndIsRegistered` and ends `ok  	github.com/aidan-bailey/loom`.
- `CGO_ENABLED=0 go build ./... && GOOS=windows CGO_ENABLED=0 go build ./... && GOOS=darwin CGO_ENABLED=0 go build ./...` print nothing.
- `gofmt -l job_shim.go job_shim_test.go main.go sched` prints nothing, and `CGO_ENABLED=0 go vet . ./sched/` prints nothing.
- `CGO_ENABLED=0 go run ./tools/claudemd` prints nothing and exits 0; `CGO_ENABLED=0 go run ./tools/claudemd -v` shows `sched/CLAUDE.md` inside its line and byte budgets.
- `CGO_ENABLED=0 go test ./sched/... .` ends `ok` for both packages.
- `nix eval --raw .#loom.drvPath >/dev/null` prints nothing and exits 0, so `flake.nix` still evaluates.

- [ ] **Step 8: Commit**

```bash
git add -N job_shim.go job_shim_test.go
git commit -m "feat(sched): add the hidden loom job-shim command and the runtime's rules" -- job_shim.go job_shim_test.go main.go flake.nix sched/CLAUDE.md sched/README.md
```

## Story 3: The queue in the model: admission, starting, watching, cancelling, adoption, notices and the session lifecycle

- **Developer:** intermediate-dev
- **Files:** `core/queue.go`, `core/queue_admit.go`, `core/queue_watch.go`, `core/queue_ops.go`, `core/queue_notice.go`, `core/mod.go`, `core/gate.go`, `core/tick.go`, `core/model.go`, `core/load.go`, `core/requests.go`, `core/seams.go`, `core/queue_test.go`, `core/queue_watch_test.go`, `core/queue_notice_test.go`, `core/gate_test.go`, `core/testmain_test.go`, `core/settings.go`, `core/settings_test.go`
- **Interfaces:** exposes `queueState`, `jobUsage`, the internal operations, `OriginJob`, `gateQueue` and `SetJobRunnerForTest` of the names contract (Story 3); uses Story 1's logic and Story 2's runtime
- **Verify:** `CGO_ENABLED=0 go test ./core/... && CC=clang CGO_ENABLED=1 go test -race ./core/ -run 'Queue|Job'`
- **Review:** deep

The model owns the work queue and touches it only on its loop; every job it spawns gets a snapshot. At boot it loads `jobs.json` (a file it can't read is left untouched, with admission paused and submissions refused), adopts and settles what ran while it was down, queued records included, and only then admits. An admission pass reads the global config each time and runs on submit, on a job's end, on a caps change, on a move to the front and on the tick while jobs wait. Starting a job is a model job that writes `spec.json`'s hint CPUs, starts the shim and waits up to 5 s for `started.json`; a shim that wrote nothing in time has its group killed if its lock proves it alive, and otherwise stays owed a SIGKILL (`queueState.silent`) that `gateQueue` sends once a later lock test finds it alive, and drops once no process has its pid, so a slowed shim never runs the command unaccounted behind a failed job. On Windows the queue is paused from `Boot` on with `sched.ErrUnsupported`'s text, and `submitJob` refuses with it before any other check. `gateQueue` (3 s) tests each running job's lock, samples usage with one `ps`, settles jobs from `exit.json`, sends a cancelled job SIGTERM and, after 10 s, SIGKILL, and kills a lost job's group only when the sample proves the group is the job's. `OriginJob` notices go through the loom mod's outbox and are recorded on the job as undelivered, never pasted, when there is no live mod. Pause and kill cancel the owner's jobs first (no notice), and retention drops finished jobs beyond `keep_finished`. The story holds none of the profile's landmine markers: its two risky acts go through `sched.SignalGroup`, which only `signalAlive` (the lock test proves the shim alive) and the lost-job rule (`orphanedGroup`: the sample shows members in the group and no process with the shim's pid, for a lost job and for a silent shim that is gone) reach, and through `sched.RemoveJobDir`, which retention reaches only for IDs already dropped from a written `jobs.json`. Both sides of each guard are pinned by tests with real shims. `Model.SaveSettings`, saving the global workspace, keeps the `scheduler` block of `config.json` as it is on disk (Task 3.7), so neither an S-overlay's save nor a stale in-memory config undoes a hand edit or another client's caps change. The story adds no rule to `core/CLAUDE.md` (76 bytes free), and it adds private fields to `queueState` beyond the contract's (`cfgDir`, `runnerErr`, `starting`, `silent`, `cpuMarks`, `doomed`, `lastPass`, `handshake`, `escalation`, `saveFailed`), and a package variable `queueGOOS` that tests set to stand on Windows, which no other story reads.

### Task 3.1: The queue's state, its load from `jobs.json`, the gate kind and the test seam

**Files:**
- Create: `core/queue.go`
- Create: `core/queue_test.go`
- Modify: `core/gate.go:11-65`
- Modify: `core/gate_test.go:21-28`, `core/gate_test.go:140-153`
- Modify: `core/model.go:95-98`, `core/model.go:254-264`
- Modify: `core/load.go:218-221`
- Modify: `core/seams.go:116-119`
- Modify: `core/testmain_test.go:1-17`

- [ ] **Step 1: Write the failing tests**

Create `core/queue_test.go`:

```go
package core

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wqHost is the host the queue's tests run on: 16 cores and 32 GB of RAM, so
// the default caps are 14 cores, 24 GB and 2 I/O jobs, and a default claim is
// 4 cores and 6 GB. Three default jobs fit; the fourth waits on cores.
var wqHost = sched.Host{Name: "testhost", Cores: 16, RAMBytes: 32 << 30}

// wqModel is a model whose queue is loaded over a throwaway global dir, its
// shims the test binary itself (sched.TestRunner; TestMain hands over to the
// shim first).
func wqModel(t *testing.T) *Model {
	t.Helper()
	return wqModelAt(t, t.TempDir())
}

// wqModelAt is wqModel over the global dir dir. A second model over the same
// dir is a restarted daemon.
func wqModelAt(t *testing.T, dir string) *Model {
	t.Helper()
	t.Setenv(config.EnvGlobalDir, dir)
	m := NewForTest(Options{})
	m.SetJobRunnerForTest(sched.TestRunner())
	m.loadQueue(dir, wqHost)
	return m
}

// wqEnded is a finished job's record: job id "polyoxide tests", submitted at
// 10:00:00, started 1m42s later and ended 3m10s after that, with exit 0.
func wqEnded(id sched.JobID, owner InstanceID) sched.Record {
	t0 := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	ended := t0.Add(292 * time.Second)
	return sched.Record{
		ID: id, Order: int64(id), Owner: uint64(owner), Description: "polyoxide tests",
		Argv: []string{"cargo", "test"}, Cwd: "/work", Notify: true, Tail: 2,
		State: sched.Done, Submitted: t0, Started: t0.Add(102 * time.Second), Ended: ended,
		Exit: &sched.Exit{Code: 0, Ended: ended},
	}
}

func TestJobQueue_AnEmptyQueueLoadsWithItsNextIDAtOne(t *testing.T) {
	m := wqModel(t)
	q := m.queue

	assert.True(t, q.ready())
	require.NoError(t, q.loadErr)
	assert.Empty(t, q.store.Jobs)
	assert.Equal(t, sched.JobID(1), q.store.NextID)
	assert.Equal(t, filepath.Join(q.cfgDir, "jobs"), q.dir)
	assert.Equal(t, wqHost, q.host)
	assert.Empty(t, q.paused)
	assert.Equal(t, sched.Caps{CPUs: 14, MemoryGB: 24, IOJobs: 2}, q.policy.Caps, "the host's defaults")
	assert.Equal(t, sched.Claim{CPUs: 4, MemoryGB: 6}, q.policy.DefaultClaim)
}

func TestJobQueue_ASavedQueueLoadsAsItWasSaved(t *testing.T) {
	dir := t.TempDir()
	saved := sched.Store{Version: sched.StoreVersion, NextID: 8, Jobs: []sched.Record{wqEnded(7, 0)}}
	require.NoError(t, sched.SaveStore(filepath.Join(dir, "jobs"), saved))

	m := wqModelAt(t, dir)

	assert.Equal(t, sched.JobID(8), m.queue.store.NextID)
	require.Len(t, m.queue.store.Jobs, 1)
	got := m.queue.store.Jobs[0]
	assert.Equal(t, sched.JobID(7), got.ID)
	assert.Equal(t, sched.Done, got.State)
	assert.Equal(t, "polyoxide tests", got.Description)
}

// A jobs.json that fails to load (a newer loom's, or one that is garbled) is
// left as it is: submissions are refused, admission pauses, the next TUI is
// told, and no save ever rewrites the file (spec §5.9).
func TestJobQueue_AJobsFileThatFailsToLoadIsLeftUntouched(t *testing.T) {
	cases := []struct {
		name, body, paused string
		newer              bool
	}{
		{"a newer loom's", `{"version": 99, "next_id": 5, "jobs": []}`, "jobs.json is from a newer loom", true},
		{"garbled", `{not json`, "jobs.json could not be loaded: ", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "jobs", sched.StoreFile)
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
			require.NoError(t, os.WriteFile(path, []byte(c.body), 0o600))

			m := wqModelAt(t, dir)

			require.Error(t, m.queue.loadErr)
			assert.Equal(t, c.newer, errors.Is(m.queue.loadErr, sched.ErrNewerStore))
			assert.Contains(t, m.queue.paused, c.paused)
			assert.Equal(t, sched.Policy{}, m.queue.policy, "admission is paused")
			require.Error(t, m.saveQueue(), "a queue that failed to load never writes")
			got, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, c.body, string(got))
			var notices []Notice
			for _, ev := range m.Drain().Events {
				if n, ok := ev.(Notice); ok {
					notices = append(notices, n)
				}
			}
			require.Len(t, notices, 1, "the next TUI is told")
			assert.Contains(t, notices[0].Err.Error(), "left as it is")
		})
	}
}

func TestJobQueue_AnInvalidSchedulerBlockPausesAdmissionAtLoad(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, config.ConfigFileName), []byte(`{"scheduler": {"cpus": 5000}}`), 0o644))

	m := wqModelAt(t, dir)

	assert.Equal(t, "scheduler.cpus is 5000: it must be 1–1024", m.queue.paused)
	assert.Equal(t, sched.Policy{}, m.queue.policy, "no cap is guessed")
	assert.Equal(t, 5000, m.queue.settings.CPUs, "the block as it was read")
}

func TestJobQueue_AdmissionPausesForTheMostBasicReasonFirst(t *testing.T) {
	resolve := errors.New("scheduler.cpus is 0: bad")
	q := newQueueState()

	assert.Equal(t, "", q.pauseReason(nil))
	assert.Equal(t, resolve.Error(), q.pauseReason(resolve))
	q.runnerErr = errors.New("no executable")
	assert.Equal(t, "no executable", q.pauseReason(resolve))
	q.loadErr = sched.ErrNewerStore
	assert.Equal(t, "jobs.json is from a newer loom", q.pauseReason(resolve))
	q.loadErr = errors.New("disk on fire")
	assert.Equal(t, "jobs.json could not be loaded: disk on fire", q.pauseReason(nil))
	q.runnerErr = sched.ErrUnsupported
	assert.Equal(t, sched.ErrUnsupported.Error(), q.pauseReason(resolve), "an unavailable queue hides every other reason")
}

// On Windows no shim runs: the queue is paused from Boot on with
// sched.ErrUnsupported's text (spec §10), whatever runner a test set.
func TestJobQueue_OnWindowsTheQueueIsPausedFromBootOn(t *testing.T) {
	queueGOOS = "windows"
	t.Cleanup(func() { queueGOOS = runtime.GOOS })

	m := wqModel(t)

	assert.ErrorIs(t, m.queue.runnerErr, sched.ErrUnsupported)
	assert.Equal(t, sched.ErrUnsupported.Error(), m.queue.paused)
	assert.Equal(t, sched.Policy{}, m.queue.policy, "no cap is guessed")
}

func TestJobQueue_ARunnerSetBeforeBootSurvivesIt(t *testing.T) {
	m := NewForTest(Options{})
	m.SetJobRunnerForTest(sched.Runner{Shim: []string{"true"}})
	m.queue.runnerErr = errors.New("left by NewRunner")
	m.SetJobRunnerForTest(sched.Runner{Shim: []string{"false"}})

	m.loadQueue(t.TempDir(), wqHost)

	assert.Equal(t, []string{"false"}, m.queue.runner.Shim, "loading the queue does not replace it")
	assert.NoError(t, m.queue.runnerErr, "setting it cleared the error NewRunner left")
}

func TestJobQueue_SaveQueueWritesTheStore(t *testing.T) {
	m := wqModel(t)
	m.queue.store.NextID = 9

	require.NoError(t, m.saveQueue())

	got, err := sched.LoadStore(m.queue.dir)
	require.NoError(t, err)
	assert.Equal(t, sched.JobID(9), got.NextID)
}

// Boot loads the queue from the global config dir, after the workspaces.
func TestJobQueue_BootLoadsTheQueueFromTheGlobalDir(t *testing.T) {
	m := bootModel(t)
	global, err := config.GetGlobalConfigDir()
	require.NoError(t, err)
	saved := sched.Store{Version: sched.StoreVersion, NextID: 4, Jobs: []sched.Record{wqEnded(3, 0)}}
	require.NoError(t, sched.SaveStore(filepath.Join(global, "jobs"), saved))

	m.Boot()

	assert.Equal(t, filepath.Join(global, "jobs"), m.queue.dir)
	require.Len(t, m.queue.store.Jobs, 1)
	assert.Equal(t, sched.JobID(3), m.queue.store.Jobs[0].ID)
}
```

In `core/gate_test.go`, `TestGateIntervalsUseEachJobsInterval` ends with these lines; add the queue's interval after the build sweep's:

```go
	assert.Equal(t, buildSweepInterval, gateIntervals[gateBuildSweep])
	assert.Zero(t, gateIntervals[gateAccountsRefresh], "account refreshes run on events, not a cadence")
```

becomes

```go
	assert.Equal(t, buildSweepInterval, gateIntervals[gateBuildSweep])
	assert.Equal(t, queueInterval, gateIntervals[gateQueue])
	assert.Zero(t, gateIntervals[gateAccountsRefresh], "account refreshes run on events, not a cadence")
```

and in `TestGatedDeliveryDisarmsFirst` replace

```go
	for _, kind := range []gateKind{gateRoster, gateHookScan, gateGH, gateUsage, gateAccountsRefresh, gateBuildSweep} {
```

with

```go
	for _, kind := range []gateKind{gateRoster, gateHookScan, gateGH, gateUsage, gateAccountsRefresh, gateBuildSweep, gateQueue} {
```

In `core/testmain_test.go` add the shim hand-over as `TestMain`'s first statement, and its import. Replace

```go
	"github.com/aidan-bailey/loom/internal/testenv"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session/tmux"
)

// TestMain runs before all tests to set up the test environment
func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}
```

with

```go
	"github.com/aidan-bailey/loom/internal/testenv"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/session/tmux"
)

// TestMain runs before all tests to set up the test environment
func TestMain(m *testing.M) {
	// A queue test starts real shims by re-running this binary
	// (sched.TestRunner): a process the runner marked hands over to the shim
	// before anything else runs.
	sched.MaybeRunTestShim()
	os.Exit(runTests(m))
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `CGO_ENABLED=0 go test ./core/ -run 'TestJobQueue_|TestGateIntervalsUseEachJobsInterval|TestGatedDeliveryDisarmsFirst'`

Expected: the package does not compile. The first errors name the missing pieces: `undefined: gateQueue`, `undefined: queueInterval`, `m.loadQueue undefined (type *Model has no field or method loadQueue)`, `m.SetJobRunnerForTest undefined`, `undefined: newQueueState`.

- [ ] **Step 3: Implement**

Create `core/queue.go`:

```go
package core

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/sched"
)

// queueInterval is gateQueue's cadence: how often the running jobs' locks are
// tested, their usage sampled and their ends settled (spec §5.4). It is also
// the least time between the admission passes the tick starts (tickQueue).
const queueInterval = 3 * time.Second

// jobsDirName is the queue's directory under the global config dir.
const jobsDirName = "jobs"

// silentLimit is how long a failed start's shim stays owed a SIGKILL: a shim
// that has not taken its lock by then is not coming (or its pid is a
// stranger's), and the entry is dropped.
const silentLimit = 10 * time.Minute

// queueGOOS is runtime.GOOS, a variable so a test can stand on Windows, where
// the work queue is paused from Boot on and refuses every submission.
var queueGOOS = runtime.GOOS

// silentShim is a shim whose job failed because it wrote no started.json in
// time, and whose lock never proved it alive, so its group has not been sent
// SIGKILL yet: it may still take its lock and run the command, unaccounted.
type silentShim struct {
	pid   int       // the shim's pid, which leads its group
	since time.Time // when the start failed
}

// jobUsage is what a running job's process group uses, as the last sample
// saw it.
type jobUsage struct {
	RSSKB int64
	CPU   float64 // cores
}

// cpuMark is a job's cumulative CPU time at a sample, which the next sample
// turns into cores.
type cpuMark struct {
	cpu time.Duration
	at  time.Time
}

// queueState is the work queue (spec §5): every job record, what admission
// last decided and what the last sample saw. The model owns it and touches
// it only on its loop; the jobs it spawns get snapshots of what they need.
type queueState struct {
	dir      string                   // <globalDir>/jobs
	store    sched.Store              // every record
	loadErr  error                    // jobs.json failed to load: submissions refused, admission paused
	host     sched.Host               // detected once, at boot
	settings config.SchedulerSettings // the scheduler block as last read
	policy   sched.Policy             // in effect after the last pass; zero while paused
	paused   string                   // why admission is paused; "" when it isn't
	waiting  map[sched.JobID]string   // from the last pass
	usage    map[sched.JobID]jobUsage // running jobs, from the last sample
	runner   sched.Runner
	rev      uint64 // bumped by every change to the queue, a job or its usage

	// What follows is private to the model's queue code; no other story reads
	// it.

	// cfgDir is the global config dir, where the scheduler block lives.
	cfgDir string
	// runnerErr is why no runner could be built (sched.NewRunner), or
	// sched.ErrUnsupported on Windows: admission pauses with its text, and
	// ErrUnsupported also refuses every submission.
	runnerErr error
	// starting holds the jobs whose start job is in flight: gateQueue leaves
	// them alone, since their shim may not hold its lock yet.
	starting map[sched.JobID]bool
	// silent holds the shims of jobs whose start failed on the 5 s wait with
	// no proof the shim was alive (a free lock), so no SIGKILL went to their
	// groups. gateQueue keeps testing each: it sends the SIGKILL once the lock
	// shows the shim alive, and drops the entry once the shim is gone (or after
	// silentLimit). In memory only.
	silent map[sched.JobID]silentShim
	// cpuMarks is each running job's CPU time at the last sample.
	cpuMarks map[sched.JobID]cpuMark
	// doomed holds the IDs retention dropped from the store, whose
	// directories go once a write of the shorter jobs.json succeeds.
	doomed []sched.JobID
	// lastPass is when the last admission pass ran, to throttle the tick's.
	lastPass time.Time
	// handshake and escalation override the start's 5 s wait for started.json
	// and the 10 s between a cancel's SIGTERM and SIGKILL; 0 keeps them. Tests
	// shorten them.
	handshake, escalation time.Duration
	// saveFailed is set while the last quiet save failed, so a run of failures
	// raises one notice.
	saveFailed bool
}

func newQueueState() *queueState {
	return &queueState{
		waiting:  map[sched.JobID]string{},
		usage:    map[sched.JobID]jobUsage{},
		starting: map[sched.JobID]bool{},
		silent:   map[sched.JobID]silentShim{},
		cpuMarks: map[sched.JobID]cpuMark{},
	}
}

// ready reports whether the queue has a directory: loadQueue ran.
func (q *queueState) ready() bool { return q != nil && q.dir != "" }

// moved records a change a client would see (QueueChanged follows rev).
func (q *queueState) moved() { q.rev++ }

// index is the position of job id in the store, -1 for none. Positions are
// valid until the store is next appended to or trimmed.
func (q *queueState) index(id sched.JobID) int {
	for i := range q.store.Jobs {
		if q.store.Jobs[i].ID == id {
			return i
		}
	}
	return -1
}

// jobDir is job id's directory.
func (q *queueState) jobDir(id sched.JobID) string { return sched.JobDir(q.dir, id) }

// pauseReason is why admission is paused, "" when it isn't: the queue did not
// load, no runner could be built, or the scheduler block is invalid
// (resolveErr, from sched.Resolve). A basic reason hides the ones above it,
// and the queue being unavailable (Windows) hides them all.
func (q *queueState) pauseReason(resolveErr error) string {
	switch {
	case errors.Is(q.runnerErr, sched.ErrUnsupported):
		return q.runnerErr.Error()
	case q.loadErr != nil:
		if errors.Is(q.loadErr, sched.ErrNewerStore) {
			return q.loadErr.Error()
		}
		return "jobs.json could not be loaded: " + q.loadErr.Error()
	case q.runnerErr != nil:
		return q.runnerErr.Error()
	case resolveErr != nil:
		return resolveErr.Error()
	}
	return ""
}

// bootQueue loads the work queue under the global config dir. Boot calls it
// once the workspaces are loaded, so a job's owner can be found.
func (m *Model) bootQueue() {
	dir, err := config.GetGlobalConfigDir()
	if err != nil {
		log.For("core").Error("queue.no_global_dir", "err", err)
		m.notifyErr(fmt.Errorf("work queue: the global config dir could not be resolved: %w", err))
		return
	}
	m.loadQueue(dir, sched.DetectHost())
}

// loadQueue loads jobs.json from cfgDir/jobs. A file that fails to load is
// left untouched: submissions are refused, admission pauses, and a notice
// tells the TUI the daemon's caller shows it to (Boot's return).
func (m *Model) loadQueue(cfgDir string, host sched.Host) {
	q := m.queue
	q.cfgDir, q.dir, q.host = cfgDir, filepath.Join(cfgDir, jobsDirName), host
	switch {
	case queueGOOS == "windows":
		// No shim runs there (spec §10): the queue is paused from boot on
		// with sched.ErrUnsupported's text, and submitJob refuses.
		q.runnerErr = sched.ErrUnsupported
	case len(q.runner.Shim) == 0:
		// SetJobRunnerForTest, called before Boot, keeps its runner.
		q.runner, q.runnerErr = sched.NewRunner()
	}
	store, err := sched.LoadStore(q.dir)
	if err != nil {
		q.loadErr = err
		m.refreshPolicy()
		log.For("core").Error("queue.load_failed", "dir", q.dir, "err", err)
		m.notifyErr(fmt.Errorf("work queue: %s; submissions are refused and admission is paused, and jobs.json is left as it is", q.paused))
		return
	}
	q.store = store
	q.moved()
	m.refreshPolicy()
}

// refreshPolicy reads the scheduler block from the global config.json, which
// a hand edit may have changed, and resolves it against the host. It records
// the block, the policy in effect and why admission is paused ("" when it
// isn't; the policy is then zero: no cap is guessed), and returns the last
// two.
func (m *Model) refreshPolicy() (sched.Policy, string) {
	q := m.queue
	settings := config.LoadConfigFrom(q.cfgDir).GetScheduler()
	policy, err := sched.Resolve(settings, q.host)
	reason := q.pauseReason(err)
	if reason != "" {
		policy = sched.Policy{}
	}
	if !reflect.DeepEqual(settings, q.settings) || policy != q.policy || reason != q.paused {
		q.moved()
	}
	q.settings, q.policy, q.paused = settings, policy, reason
	return policy, reason
}

// saveQueue writes jobs.json, for a request that a failed write refuses (it
// undoes its change) and as the quiet save's body. A queue that failed to
// load is never written: its file is kept as it is.
func (m *Model) saveQueue() error {
	q := m.queue
	if q.loadErr != nil {
		return fmt.Errorf("jobs.json was not loaded and is left as it is: %w", q.loadErr)
	}
	if err := sched.SaveStore(q.dir, q.store); err != nil {
		return fmt.Errorf("write jobs.json: %w", err)
	}
	q.saveFailed = false
	return nil
}

// saveQueueQuiet saves after a change the model made on its own (a start, a
// settle, a notice's fate): when the write fails the change stays in memory,
// a Notice says so once, and the next save writes it (spec §5.9). It reports
// whether jobs.json was written.
func (m *Model) saveQueueQuiet(after string) bool {
	q := m.queue
	if !q.ready() || q.loadErr != nil {
		return false
	}
	err := m.saveQueue()
	if err == nil {
		return true
	}
	log.For("core").Warn("queue.save_failed", "after", after, "err", err)
	if !q.saveFailed {
		q.saveFailed = true
		m.notifyErr(fmt.Errorf("work queue: %w; the change is kept in memory and written with the next save", err))
	}
	return false
}
```

In `core/gate.go` add the kind, its name and its interval. Replace

```go
		// gateBuildSweep throttles the build sweep (maybeBuildSweep).
		gateBuildSweep

		numGateKinds
```

with

```go
		// gateBuildSweep throttles the build sweep (maybeBuildSweep).
		gateBuildSweep
		// gateQueue throttles the work queue's watch of its running jobs
		// (maybeQueue).
		gateQueue

		numGateKinds
```

replace

```go
	case gateBuildSweep:
		return "build_sweep"
	}
	return "unknown"
```

with

```go
	case gateBuildSweep:
		return "build_sweep"
	case gateQueue:
		return "queue"
	}
	return "unknown"
```

and replace

```go
	gateBuildSweep: buildSweepInterval,
	// gateAccountsRefresh stays 0: refreshes run on events (an account
```

with

```go
	gateBuildSweep: buildSweepInterval,
	gateQueue:      queueInterval,
	// gateAccountsRefresh stays 0: refreshes run on events (an account
```

In `core/model.go` add the model's queue. After the `buildSweepWarned` field, replace

```go
	buildSweepWarned map[string]bool

	// dirtySessions records tmux session names that emitted output since the
```

with

```go
	buildSweepWarned map[string]bool

	// queue is the work queue (core/queue.go): the jobs sessions submitted
	// with `loom job`, their admission against the host's caps and their
	// persistence under the global config dir. Built by newModel, loaded by
	// Boot. Loop-goroutine only.
	queue *queueState

	// dirtySessions records tmux session names that emitted output since the
```

and in `newModel` replace

```go
		wsIDs:     make(map[*Workspace]WorkspaceID),
	}
}
```

with

```go
		wsIDs:     make(map[*Workspace]WorkspaceID),
		queue:     newQueueState(),
	}
}
```

In `core/load.go`, `Boot` loads the queue after the workspaces. Replace

```go
	m.boot()
	events := m.out.Events
	m.out.Events = nil
	return events
}
```

with

```go
	m.boot()
	m.bootQueue()
	events := m.out.Events
	m.out.Events = nil
	return events
}
```

and in the doc comment above `Boot` replace

```go
// serves (boot). The detection runs once, up front, so every launch
// decision after it is synchronous. It notes when it ran (modsSince), from
```

with

```go
// serves (boot), and then the work queue (bootQueue). The detection runs
// once, up front, so every launch decision after it is synchronous. It notes
// when it ran (modsSince), from
```

In `core/seams.go` add the runner seam and its import. Replace

```go
	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
)
```

with

```go
	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
)
```

and after

```go
func (m *Model) ApplyClaudeStatusForTest(inst *session.Instance) { m.applyClaudeStatus(inst) }
```

add

```go

// SetJobRunnerForTest replaces the runner that starts the work queue's shims:
// a test points it at its own binary (sched.TestRunner), which TestMain turns
// into a shim (sched.MaybeRunTestShim). Boot builds the real one from
// os.Executable unless one is set first, so call it between core.New and Boot;
// nothing after it replaces the runner. It also clears any runnerErr that
// sched.NewRunner left.
func (m *Model) SetJobRunnerForTest(r sched.Runner) {
	if m.queue == nil {
		m.queue = newQueueState()
	}
	m.queue.runner, m.queue.runnerErr = r, nil
}
```

- [ ] **Step 4: Run them to see them pass**

Run: `CGO_ENABLED=0 go test ./core/ -run 'TestJobQueue_|TestGateIntervalsUseEachJobsInterval|TestGatedDeliveryDisarmsFirst'`

Expected: `ok  	github.com/aidan-bailey/loom/core	<time>`. Then run the whole package, to see that no `Boot` test changed: `CGO_ENABLED=0 go test ./core/` prints `ok`.

- [ ] **Step 5: Commit**

```bash
git add -N core/queue.go core/queue_test.go
git commit -m "feat(core): the work queue's state, loaded from jobs.json at boot, and its gate kind" -- core/queue.go core/queue_test.go core/gate.go core/gate_test.go core/model.go core/load.go core/seams.go core/testmain_test.go
```

### Task 3.2: `OriginJob` and a job's notice

**Files:**
- Create: `core/queue_notice.go`
- Create: `core/queue_notice_test.go`
- Modify: `core/mod.go:1-15`, `core/mod.go:62-92`, `core/mod.go:136-146`, `core/mod.go:266-284`, `core/mod.go:425-446`, `core/mod.go:524-531`, `core/mod.go:738-747`
- Modify: `core/model.go:481-483`

- [ ] **Step 1: Write the failing tests**

Create `core/queue_notice_test.go`:

```go
package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wqSeed adds rec to the queue and makes its directory, as a submit does.
func wqSeed(t *testing.T, m *Model, rec sched.Record) {
	t.Helper()
	m.queue.store.Jobs = append(m.queue.store.Jobs, rec)
	if rec.ID >= m.queue.store.NextID {
		m.queue.store.NextID = rec.ID + 1
	}
	require.NoError(t, sched.WriteSpec(m.queue.jobDir(rec.ID), sched.Spec{ID: rec.ID, Argv: rec.Argv, Cwd: rec.Cwd}))
}

// wqRecord is a copy of job id's record.
func wqRecord(t *testing.T, m *Model, id sched.JobID) sched.Record {
	t.Helper()
	i := m.queue.index(id)
	require.GreaterOrEqual(t, i, 0, "job %d", id)
	return m.queue.store.Jobs[i]
}

// wqPump runs the jobs the model queued and delivers their results, until it
// queues no more, and returns the events it produced meanwhile.
func wqPump(t *testing.T, m *Model) []Event {
	t.Helper()
	var events []Event
	for range 50 {
		out := m.Drain()
		events = append(events, out.Events...)
		if len(out.Jobs) == 0 {
			return events
		}
		for _, j := range out.Jobs {
			m.Deliver(j())
		}
	}
	t.Fatal("the model kept queuing jobs")
	return events
}

func TestJobQueue_ANoticeNamesItsJobsOutcome(t *testing.T) {
	with := func(edit func(*sched.Record)) sched.Record {
		r := wqEnded(14, 0)
		edit(&r)
		return r
	}
	const prefix = `[loom] job 14 "polyoxide tests" `
	cases := []struct {
		name string
		rec  sched.Record
		want string
	}{
		{"exit 0", with(func(*sched.Record) {}),
			prefix + `finished: exit 0 after 3m10s (1m42s in the queue).`},
		{"a non-zero exit", with(func(r *sched.Record) { r.State = sched.Failed; r.Exit = &sched.Exit{Code: 2} }),
			prefix + `finished: exit 2 after 3m10s (1m42s in the queue).`},
		{"a signal", with(func(r *sched.Record) { r.State = sched.Failed; r.Exit = &sched.Exit{Code: -1, Signal: "SIGKILL"} }),
			prefix + `was killed by SIGKILL after 3m10s (1m42s in the queue).`},
		{"cancelled while running", with(func(r *sched.Record) {
			r.State, r.CancelReason, r.Exit = sched.Cancelled, "from the work view", nil
		}),
			prefix + `was cancelled from the work view after 3m10s (1m42s in the queue).`},
		{"cancelled while queued", with(func(r *sched.Record) {
			r.State, r.CancelReason, r.Exit = sched.Cancelled, "with loom job cancel", nil
			r.Started, r.Ended = time.Time{}, r.Submitted.Add(2*time.Minute)
		}),
			prefix + `was cancelled with loom job cancel after 2m0s in the queue.`},
		{"lost", with(func(r *sched.Record) { r.State, r.Exit = sched.Lost, nil }),
			prefix + `was lost: its runner died without a result (the host may have rebooted).`},
		{"could not start", with(func(r *sched.Record) {
			r.State, r.Exit, r.Started, r.Failure = sched.Failed, nil, time.Time{}, "its folder is gone"
		}),
			prefix + `could not start: its folder is gone.`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, noticeHead(c.rec))
		})
	}
}

func TestJobQueue_ANoticeCarriesTheLogsTail(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
		return p
	}
	var thirty strings.Builder
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&thirty, "line %d\n", i)
	}
	full := write("thirty.log", thirty.String())

	assert.Equal(t, "HEAD\nLog: "+full+" (30 lines). Last 5 lines:\n\nline 26\nline 27\nline 28\nline 29\nline 30",
		composeNotice("HEAD", full, 5))
	assert.Equal(t, "HEAD\nLog: "+full+" (30 lines).", composeNotice("HEAD", full, 0),
		"--tail 0 names the log and sends none of it")

	short := write("short.log", "a\nb")
	assert.Equal(t, "HEAD\nLog: "+short+" (2 lines). Last 2 lines:\n\na\nb", composeNotice("HEAD", short, 20),
		"a last line without its newline counts")
	one := write("one.log", "only\n")
	assert.Equal(t, "HEAD\nLog: "+one+" (1 line). Last 1 line:\n\nonly", composeNotice("HEAD", one, 20))
	empty := write("empty.log", "")
	assert.Equal(t, "HEAD\nLog: "+empty+" (0 lines).", composeNotice("HEAD", empty, 20))
	assert.Equal(t, "HEAD", composeNotice("HEAD", filepath.Join(dir, "never.log"), 20),
		"a job that never ran has no log to name")

	line := strings.Repeat("x", 99)
	wide := write("wide.log", strings.Repeat(line+"\n", 100))
	capped := composeNotice("HEAD", wide, 200)
	_, tail, found := strings.Cut(capped, ":\n\n")
	require.True(t, found)
	assert.LessOrEqual(t, len(tail), 4000, "at most the last 4,000 bytes")
	assert.True(t, strings.HasSuffix(tail, line), "and they end with the log's last line")
	assert.Contains(t, capped, "(100 lines). Last 39 lines:", "the window's first line may be cut, so it goes")
}

func TestJobQueue_ANoticeReachesTheOwnersModAndIsRecordedSent(t *testing.T) {
	withMod(t)
	m := wqModel(t)
	inst := modInst(t, m, "owner")
	rec := wqEnded(5, m.idOf(inst))
	wqSeed(t, m, rec)
	logPath := filepath.Join(m.queue.jobDir(5), sched.LogFile)
	require.NoError(t, os.WriteFile(logPath, []byte("one\ntwo\nthree\n"), 0o600))
	require.NoError(t, m.ModHello("L1", ModHello{Mod: 1}))

	m.sendJobNotice(rec)
	wqPump(t, m)

	msgs, _, err := m.ModInbox("L1", "")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.False(t, msgs[0].AsUser, "a job's notice is nobody's own words")
	assert.Equal(t, noticeHead(rec)+"\nLog: "+logPath+" (3 lines). Last 2 lines:\n\ntwo\nthree", msgs[0].Text)
	assert.Equal(t, sched.NoticeNone, wqRecord(t, m, 5).Notice, "not sent until the mod has queued it")

	require.NoError(t, m.ModEvents("L1", []ModEvent{{Kind: "queued", ID: msgs[0].ID}}))

	assert.Equal(t, sched.NoticeSent, wqRecord(t, m, 5).Notice)
	saved, err := sched.LoadStore(m.queue.dir)
	require.NoError(t, err)
	require.Len(t, saved.Jobs, 1)
	assert.Equal(t, sched.NoticeSent, saved.Jobs[0].Notice, "the fate is on disk")
}

// A launch with no live mod is never pasted into: a paste could answer an
// open dialog. The notice is recorded on the job with the reason (spec §5.8).
func TestJobQueue_ANoticeWithNoLiveModIsRecordedUndeliveredAndNeverPasted(t *testing.T) {
	cases := []struct{ name, program, why string }{
		{"no mod served", "claude", "this loom daemon serves no loom mod"},
		{"another program", "aider", "it runs aider, not Claude"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := wqModel(t)
			inst := startedInst(t, "owner", c.program)
			hold(m, inst)
			wqSeed(t, m, wqEnded(5, m.idOf(inst)))

			m.sendJobNotice(wqRecord(t, m, 5))
			wqPump(t, m)

			got := wqRecord(t, m, 5)
			assert.Equal(t, sched.NoticeUndelivered, got.Notice)
			assert.Equal(t, c.why, got.NoticeWhy)
			assert.Empty(t, m.Drain().Jobs, "nothing is pasted")
		})
	}
}

// A message the mod never took is settled as undelivered when the daemon
// stops, and one it took (an inbox poll handed it over) too, since it may
// have reached Claude; neither is pasted (core/CLAUDE.md, loom mod links).
func TestJobQueue_ANoticeTheModNeverQueuedIsUndeliveredWhenTheDaemonStops(t *testing.T) {
	for _, offered := range []bool{false, true} {
		t.Run(fmt.Sprintf("offered=%v", offered), func(t *testing.T) {
			withMod(t)
			m := wqModel(t)
			inst := modInst(t, m, "owner")
			wqSeed(t, m, wqEnded(5, m.idOf(inst)))
			m.sendJobNotice(wqRecord(t, m, 5))
			wqPump(t, m)
			if offered {
				require.NoError(t, m.ModHello("L1", ModHello{Mod: 1}))
				msgs, _, err := m.ModInbox("L1", "")
				require.NoError(t, err)
				require.Len(t, msgs, 1)
			}

			m.ModsStop()

			got := wqRecord(t, m, 5)
			assert.Equal(t, sched.NoticeUndelivered, got.Notice)
			if offered {
				assert.Contains(t, got.NoticeWhy, "may or may not have reached Claude")
			} else {
				assert.Equal(t, modsStoppedWhy, got.NoticeWhy)
			}
			assert.Empty(t, m.Drain().Jobs, "nothing is pasted")
		})
	}
}

func TestJobQueue_ANoticeNeedsAnOwnerThatCanTakeIt(t *testing.T) {
	withMod(t)
	m := wqModel(t)
	paused := pausedInst(t, "paused")
	hold(m, paused)
	cases := []struct {
		name string
		rec  sched.Record
		why  string
	}{
		{"not served", wqEnded(1, 4242), "the session that submitted it is no longer served"},
		{"paused", wqEnded(2, m.idOf(paused)), "the session is not running (Paused)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wqSeed(t, m, c.rec)

			m.sendJobNotice(c.rec)

			got := wqRecord(t, m, c.rec.ID)
			assert.Equal(t, sched.NoticeUndelivered, got.Notice)
			assert.Equal(t, c.why, got.NoticeWhy)
			assert.Empty(t, m.Drain().Jobs, "no notice is built for it")
		})
	}
}

func TestJobQueue_ANoticeIsSentOnlyForAJobThatAsksForOneAndHasNoFateYet(t *testing.T) {
	m := wqModel(t)
	ownerless := wqEnded(1, 0)
	quiet := wqEnded(2, 77)
	quiet.Notify = false
	decided := wqEnded(3, 77)
	decided.Notice = sched.NoticeSent
	for _, rec := range []sched.Record{ownerless, quiet, decided} {
		wqSeed(t, m, rec)

		m.sendJobNotice(rec)

		assert.Equal(t, rec.Notice, wqRecord(t, m, rec.ID).Notice, "job %d", rec.ID)
		assert.Empty(t, m.Drain().Jobs, "job %d", rec.ID)
	}
}

func TestJobQueue_AnOwnerGoneWhileTheNoticeWasBeingBuiltIsRecordedUndelivered(t *testing.T) {
	withMod(t)
	m := wqModel(t)
	inst := modInst(t, m, "owner")
	wqSeed(t, m, wqEnded(5, m.idOf(inst)))
	m.sendJobNotice(wqRecord(t, m, 5))
	jobs := m.Drain().Jobs
	require.Len(t, jobs, 1)
	built := jobs[0]()
	require.True(t, m.workspaces[0].RemoveForTest(inst), "the session is killed meanwhile")

	m.Deliver(built)

	got := wqRecord(t, m, 5)
	assert.Equal(t, sched.NoticeUndelivered, got.Notice)
	assert.Equal(t, "the session that submitted it is no longer served", got.NoticeWhy)
}

func TestJobQueue_NoClientCanSendAJobsOrigin(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")

	m.Send(m.idOf(inst), Message{Text: "hi", Origin: OriginJob}, 9)

	rs := replies(m.Drain())
	require.Len(t, rs, 1)
	require.Error(t, rs[0].Err)
	assert.Contains(t, rs[0].Err.Error(), "is no sender's origin")
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `CGO_ENABLED=0 go test ./core/ -run 'TestJobQueue_ANotice|TestJobQueue_NoClient|TestJobQueue_AnOwnerGone'`

Expected: the package does not compile: `undefined: noticeHead`, `undefined: composeNotice`, `m.sendJobNotice undefined`, `undefined: OriginJob`.

- [ ] **Step 3: Implement**

Create `core/queue_notice.go`:

```go
package core

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/session"
)

const (
	// lostWhy is why a job is lost: its runner vanished without writing its
	// result (spec §4).
	lostWhy = "its runner died without a result (the host may have rebooted)"
	// noticeTailBytes caps the log tail a notice carries (spec §5.8).
	noticeTailBytes = 4000
)

// jobNoticeBuilt is a job's notice, back from the job that read its log: the
// text for the owner's mod.
type jobNoticeBuilt struct {
	id    sched.JobID
	owner InstanceID
	text  string
}

// sendJobNotice tells rec's owner how the job ended (spec §5.8), through the
// loom mod's outbox with the origin OriginJob. Nothing is sent for a job
// that asked for no notice, has no owner, or already has a fate. When the
// owner is gone or can't take it the notice is recorded undelivered at once;
// otherwise a job reads the log's tail off the loop (a log has no size cap)
// and deliverJobNotice queues the text. rec is a copy: the caller may be
// iterating the store.
func (m *Model) sendJobNotice(rec sched.Record) {
	if !rec.Notify || rec.Owner == 0 || rec.Notice != sched.NoticeNone {
		return
	}
	owner := InstanceID(rec.Owner)
	if _, why := m.noticeTarget(owner); why != "" {
		m.noteNotice(rec.ID, sched.NoticeUndelivered, why)
		return
	}
	logPath := filepath.Join(m.queue.jobDir(rec.ID), sched.LogFile)
	m.spawnBackground(buildNoticeJob(rec.ID, owner, noticeHead(rec), logPath, rec.Tail))
}

// noticeTarget is the session a notice for owner would go to, and why it
// can't go now ("" when it can): the owner is no longer served, or its
// session doesn't run (the checks Send makes of a CLI message).
func (m *Model) noticeTarget(owner InstanceID) (*session.Instance, string) {
	inst := m.ownerInst(owner)
	if inst == nil {
		return nil, "the session that submitted it is no longer served"
	}
	return inst, jobOwnerWhy(inst)
}

// ownerInst is the served instance with ID id. It computes the ID of every
// instance when the cache (lookup) has not met it yet: at boot nothing has
// published the instances' IDs, and an ID is derived from the disk, so a job's
// owner is found again after a restart.
func (m *Model) ownerInst(id InstanceID) *session.Instance {
	if id == 0 {
		return nil
	}
	if inst, _ := m.lookup(id); inst != nil {
		return inst
	}
	for _, ws := range m.workspaces {
		for _, inst := range ws.insts {
			if m.idOf(inst) == id {
				return inst
			}
		}
	}
	return nil
}

// jobOwnerWhy is why a session can't take a job's notice now, "" when it can:
// it has not started, is not running, or is busy starting, resuming or
// pausing (its link would be the old launch's).
func jobOwnerWhy(inst *session.Instance) string {
	switch st := inst.GetStatus(); {
	case st == session.Loading || st == session.Deleting:
		return fmt.Sprintf("the session is busy (%s)", st)
	case st == session.Paused || st == session.Recoverable:
		return fmt.Sprintf("the session is not running (%s)", st)
	case !inst.Started():
		return "the session has not started"
	}
	return ""
}

// jobNoModReason says why inst's launch has no live mod, for a notice
// recorded undelivered: noModReason's causes in its order, in words that
// name no `loom` command.
func jobNoModReason(inst *session.Instance) string {
	launch, hooked := inst.HookLaunch()
	switch {
	case !session.IsClaudeProgram(inst.Program()):
		return fmt.Sprintf("it runs %s, not Claude", programName(inst.Program()))
	case session.ModSocket() == "":
		return "this loom daemon serves no loom mod"
	case inst.Launches() == 0:
		return "it was launched before this daemon started and its loom mod has not said hello: pause and resume it to relaunch it with the mod"
	case launch != "" && !hooked:
		return "it runs without loom's hooks, so it has no loom mod"
	default:
		return "its Claude runs without the loom mod (an older Claude, or the mod did not load)"
	}
}

// noteNotice records a notice's fate on job id and saves: sent (the mod
// queued it) or undelivered, with why. A fate once recorded stays.
func (m *Model) noteNotice(id sched.JobID, fate sched.Notice, why string) {
	q := m.queue
	i := q.index(id)
	if i < 0 || q.store.Jobs[i].Notice != sched.NoticeNone {
		return
	}
	q.store.Jobs[i].Notice, q.store.Jobs[i].NoticeWhy = fate, why
	q.moved()
	m.saveQueueQuiet("notice")
}

// buildNoticeJob reads the log's tail off the loop and returns the notice's
// text. It reads no model state: head, logPath and tail are copies.
func buildNoticeJob(id sched.JobID, owner InstanceID, head, logPath string, tail int) Job {
	return func() any {
		return jobNoticeBuilt{id: id, owner: owner, text: composeNotice(head, logPath, tail)}
	}
}

// deliverJobNotice hands a built notice to its owner's mod. The owner is
// looked up again: it may have been killed or paused while the log was read.
func (m *Model) deliverJobNotice(r jobNoticeBuilt) {
	inst, why := m.noticeTarget(r.owner)
	if why != "" {
		m.noteNotice(r.id, sched.NoticeUndelivered, why)
		return
	}
	m.enqueue(inst, &modMessage{text: r.text, origin: OriginJob, job: r.id})
}

// noticeHead is the notice's first line (spec §5.8): the job, then its outcome
// and how long it ran and waited. A lost job or one that failed to start has
// no run to time; a job cancelled while queued only waited.
func noticeHead(rec sched.Record) string {
	var what string
	switch {
	case rec.State == sched.Cancelled:
		what = "was cancelled " + rec.CancelReason
	case rec.State == sched.Lost:
		what = "was lost: " + lostWhy
	case rec.State == sched.Failed && rec.Failure != "":
		what = "could not start: " + rec.Failure
	case rec.Exit != nil && rec.Exit.Signal != "":
		what = "was killed by " + rec.Exit.Signal
	case rec.Exit != nil:
		what = fmt.Sprintf("finished: exit %d", rec.Exit.Code)
	default:
		what = "finished"
	}
	head := fmt.Sprintf("[loom] job %d %q %s", rec.ID, rec.Description, what)
	switch {
	case rec.State == sched.Lost, rec.State == sched.Failed && rec.Failure != "":
		return head + "."
	case !rec.Started.IsZero() && !rec.Ended.Before(rec.Started):
		return fmt.Sprintf("%s after %s (%s in the queue).", head, roundedDur(rec.Ended.Sub(rec.Started)), roundedDur(rec.Started.Sub(rec.Submitted)))
	default:
		return fmt.Sprintf("%s after %s in the queue.", head, roundedDur(rec.Ended.Sub(rec.Submitted)))
	}
}

// roundedDur is a duration to the second, as the CLI shows durations.
func roundedDur(d time.Duration) string { return d.Round(time.Second).String() }

// composeNotice is head, then the log's line: its path and line count, and the
// last tail lines within the last 4,000 bytes. A job that never ran has no log
// and gets the head alone.
func composeNotice(head, logPath string, tail int) string {
	total, lines, err := readLogTail(logPath, tail)
	var b strings.Builder
	b.WriteString(head)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		fmt.Fprintf(&b, "\nLog: %s (could not be read: %v)", logPath, err)
	default:
		fmt.Fprintf(&b, "\nLog: %s (%s).", logPath, plural(total, "line"))
		if len(lines) > 0 {
			fmt.Fprintf(&b, " Last %s:\n\n%s", plural(len(lines), "line"), strings.Join(lines, "\n"))
		}
	}
	return b.String()
}

// plural is "1 line" or "N lines".
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// readLogTail counts the lines of the file at path and returns the last want
// of them, within its last noticeTailBytes bytes. The window may open
// mid-line, so its first line is dropped when there is another.
func readLogTail(path string, want int) (total int, lines []string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, nil, err
	}
	defer f.Close()
	var size int64
	last := byte('\n')
	buf := make([]byte, 64<<10)
	for {
		n, rerr := f.Read(buf)
		total += bytes.Count(buf[:n], []byte{'\n'})
		if n > 0 {
			size += int64(n)
			last = buf[n-1]
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return 0, nil, rerr
		}
	}
	if size > 0 && last != '\n' {
		total++
	}
	if want <= 0 || size == 0 {
		return total, nil, nil
	}
	off := max(0, size-noticeTailBytes)
	window := make([]byte, size-off)
	if _, err := f.ReadAt(window, off); err != nil && !errors.Is(err, io.EOF) {
		return 0, nil, err
	}
	lines = strings.Split(strings.TrimRight(string(window), "\n"), "\n")
	if off > 0 && len(lines) > 1 {
		lines = lines[1:]
	}
	if len(lines) > want {
		lines = lines[len(lines)-want:]
	}
	for i := range lines {
		lines[i] = strings.ToValidUTF8(lines[i], "")
	}
	return total, lines, nil
}
```

In `core/mod.go` make the changes below, each replacing the quoted text.

The import block gains `sched`. Replace

```go
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
)
```

with

```go
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/session"
)
```

The origin. Replace

```go
	OriginWeb Origin = "web"
)
```

with

```go
	OriginWeb Origin = "web"
	// OriginJob is a work-queue job's end (core/queue_notice.go): no "From"
	// line and not the person's words. Like a CLI message it goes only
	// through a live mod, but only the model sends it (Send refuses it): where
	// a CLI message is refused, a job's notice is recorded on its job as
	// undelivered, and it is never pasted (withoutMod).
	OriginJob Origin = "job"
)
```

and replace

```go
// needsMod reports whether a message of origin o goes only through a live
// mod: a CLI message and a web one are refused where a script's is pasted.
func (o Origin) needsMod() bool { return o == OriginCLI || o == OriginWeb }
```

with

```go
// needsMod reports whether a message of origin o goes only through a live
// mod: a CLI message and a web one are refused where a script's is pasted, a
// job's notice recorded as undelivered.
func (o Origin) needsMod() bool { return o == OriginCLI || o == OriginWeb || o == OriginJob }
```

`Send` must not take a job's origin from a client. Replace

```go
	case !msg.Origin.needsMod() && msg.Origin != OriginScript:
		m.refuse(req, id, fmt.Errorf("send to %s: %q is no sender's origin", inst.Title, msg.Origin))
		return
```

with

```go
	case (!msg.Origin.needsMod() && msg.Origin != OriginScript) || msg.Origin == OriginJob:
		// OriginJob is the queue's own (sendJobNotice): no client sends one.
		m.refuse(req, id, fmt.Errorf("send to %s: %q is no sender's origin", inst.Title, msg.Origin))
		return
```

The message names its job. Replace

```go
	cause ReqID
	owner *Workspace
}
```

with

```go
	cause ReqID
	owner *Workspace
	// job is the queue job a job notice (OriginJob) reports on: its fate is
	// recorded there (noteNotice).
	job sched.JobID
}
```

A job's notice without a live mod is recorded, not pasted. In `withoutMod`, replace

```go
	case OriginFirstPrompt:
		m.causedBy(msg.cause, func() { m.spawn(m.sendInitialPrompt(inst, msg.owner, msg.text)) })
	}
}
```

with

```go
	case OriginFirstPrompt:
		m.causedBy(msg.cause, func() { m.spawn(m.sendInitialPrompt(inst, msg.owner, msg.text)) })
	case OriginJob:
		if why == "" {
			why = jobNoModReason(inst)
		}
		m.noteNotice(msg.job, sched.NoticeUndelivered, why)
	}
}
```

In `undelivered`, replace

```go
func (m *Model) undelivered(inst *session.Instance, msg *modMessage, why string) {
	if msg.origin == OriginFirstPrompt {
```

with

```go
func (m *Model) undelivered(inst *session.Instance, msg *modMessage, why string) {
	if msg.origin == OriginJob {
		m.noteNotice(msg.job, sched.NoticeUndelivered, why)
		return
	}
	if msg.origin == OriginFirstPrompt {
```

and in `modQueued`, replace

```go
	m.reply(msg.req, Reply{ID: m.idOf(inst)})
```

with

```go
	if msg.origin == OriginJob {
		m.noteNotice(msg.job, sched.NoticeSent, "")
		return
	}
	m.reply(msg.req, Reply{ID: m.idOf(inst)})
```

In `core/model.go`, `Deliver` routes the built notice. Replace

```go
	case buildSweepResult:
		m.deliverBuildSweep(msg)
	case tracked:
```

with

```go
	case buildSweepResult:
		m.deliverBuildSweep(msg)
	case jobNoticeBuilt:
		m.deliverJobNotice(msg)
	case tracked:
```

- [ ] **Step 4: Run them to see them pass**

Run: `CGO_ENABLED=0 go test ./core/ -run 'TestJobQueue_|TestMod_|TestSend'`

Expected: `ok  	github.com/aidan-bailey/loom/core	<time>`, the mod tests unchanged. Then `CGO_ENABLED=0 go test ./core/` prints `ok`.

- [ ] **Step 5: Commit**

```bash
git add -N core/queue_notice.go core/queue_notice_test.go
git commit -m "feat(core): a job's end reaches its owner's loom mod as an OriginJob notice, or is recorded undelivered" -- core/queue_notice.go core/queue_notice_test.go core/mod.go core/model.go
```

### Task 3.3: Watching: `gateQueue` tests locks, samples usage, settles jobs and carries out cancels

**Files:**
- Create: `core/queue_watch.go`
- Create: `core/queue_watch_test.go`
- Modify: `core/queue.go:1-11` (imports), `core/queue.go` (`saveQueue`, and the end of the file)
- Modify: `core/gate.go:173-188`
- Modify: `core/tick.go:122-130`
- Modify: `core/model.go:481-486`, `core/model.go:494-500`
- Modify: `core/gate_test.go:255-260`

A job's lock is the proof that its shim is alive, and a signal goes only where that proof (or the lost-job rule) holds: `signalAlive` is the one door to `sched.SignalGroup` for a running job, and it refuses pid 0 and 1 (`kill(-0)` is the caller's own group, `kill(-1)` everyone's). The pass runs in a gated job off the loop on a snapshot (`queuePass`) and returns what it saw (`queuePassResult`); `deliverQueuePass` applies it on the loop and ignores an observation of a record that moved on meanwhile.

- [ ] **Step 1: Write the failing tests**

Create `core/queue_watch_test.go`:

```go
package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wqBeatScript is a command that writes a rising number to its first argument
// every 50 ms until it dies: a process whose life a test can watch.
const wqBeatScript = `n=0; while :; do n=$((n+1)); echo $n > "$1"; sleep 0.05; done`

// wqWait polls cond on the test's goroutine until it holds.
func wqWait(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// wqConfig writes the scheduler block block into the global config.json the
// queue reads.
func wqConfig(t *testing.T, m *Model, block string) {
	t.Helper()
	body := `{"scheduler": ` + block + `}`
	require.NoError(t, os.WriteFile(filepath.Join(m.queue.cfgDir, config.ConfigFileName), []byte(body), 0o644))
}

// wqLiveModel is wqModel for a test that runs real shims: when the test ends
// every job still unfinished is cancelled and settled, so no process group
// outlives it.
func wqLiveModel(t *testing.T) *Model {
	t.Helper()
	m := wqModel(t)
	t.Cleanup(func() { wqStop(t, m) })
	return m
}

func wqStop(t *testing.T, m *Model) {
	t.Helper()
	q := m.queue
	for i := range q.store.Jobs {
		switch rec := &q.store.Jobs[i]; rec.State {
		case sched.Queued:
			rec.State = sched.Cancelled
		case sched.Running:
			if rec.CancelReason == "" {
				rec.CancelReason = "when the test ended"
			}
		}
	}
	q.escalation = 200 * time.Millisecond
	wqWait(t, "the jobs to stop", func() bool {
		wqPass(t, m)
		for _, rec := range q.store.Jobs {
			if rec.State == sched.Running {
				return false
			}
		}
		return true
	})
}

// wqPass runs one pass of gateQueue to completion, as the tick would: it
// makes the gate due, dispatches, runs every job the model queued and
// delivers the results.
func wqPass(t *testing.T, m *Model) []Event {
	t.Helper()
	m.gate(gateQueue).expedite()
	m.maybeQueue()
	return wqPump(t, m)
}

// wqSettle passes until job id has finished, and returns its record.
func wqSettle(t *testing.T, m *Model, id sched.JobID) sched.Record {
	t.Helper()
	wqWait(t, fmt.Sprintf("job %d to settle", id), func() bool {
		wqPass(t, m)
		return wqRecord(t, m, id).State.Finished()
	})
	return wqRecord(t, m, id)
}

// wqLaunch starts a real shim for a new running record, as the model's own
// start does (Task 3.4 drives that), and waits for its started.json.
func wqLaunch(t *testing.T, m *Model, argv ...string) sched.JobID {
	t.Helper()
	q := m.queue
	id := q.store.NextID
	q.store.NextID++
	dir := q.jobDir(id)
	cwd := t.TempDir()
	require.NoError(t, sched.WriteSpec(dir, sched.Spec{ID: id, Argv: argv, Cwd: cwd, Env: []string{"PATH=" + os.Getenv("PATH")}}))
	_, err := q.runner.Start(dir)
	require.NoError(t, err)
	var st sched.Started
	wqWait(t, "the shim's started.json", func() bool {
		var err error
		st, err = sched.ReadStarted(dir)
		return err == nil
	})
	q.store.Jobs = append(q.store.Jobs, sched.Record{
		ID: id, Order: int64(id), Description: "launched", Argv: argv, Cwd: cwd,
		Claim: sched.Claim{CPUs: 4, MemoryGB: 6}, Tail: 20,
		State: sched.Running, Submitted: st.Started, Started: st.Started, PID: st.PID,
	})
	return id
}

// wqBeats reports whether the number in file rises within a second.
func wqBeats(file string) bool {
	first := ""
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(file)
		if v := strings.TrimSpace(string(b)); err == nil && v != "" {
			if first == "" {
				first = v
			} else if v != first {
				return true
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// wqBystander starts a process of its own, in a group of its own: the stranger
// a reused pid names. It returns its pid and its heartbeat file, and is killed
// when the test ends.
func wqBystander(t *testing.T) (pid int, beat string) {
	t.Helper()
	beat = filepath.Join(t.TempDir(), "beat")
	r := sched.Runner{Shim: []string{"sh", "-c", wqBeatScript, "sh", beat}}
	pid, err := r.Start(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() {
		if p, err := os.FindProcess(pid); err == nil {
			_ = p.Kill()
		}
	})
	wqWait(t, "the bystander's first beat", func() bool { _, err := os.Stat(beat); return err == nil })
	return pid, beat
}

// wqBlockJobsJSON makes jobs.json unwritable: a directory takes its place, so
// the atomic rename fails. It returns the undo.
func wqBlockJobsJSON(t *testing.T, m *Model) (unblock func()) {
	t.Helper()
	path := filepath.Join(m.queue.dir, sched.StoreFile)
	if err := os.Remove(path); !os.IsNotExist(err) {
		require.NoError(t, err)
	}
	require.NoError(t, os.MkdirAll(filepath.Join(path, "x"), 0o700))
	return func() {
		require.NoError(t, os.Remove(filepath.Join(path, "x")))
		require.NoError(t, os.Remove(path))
	}
}

func TestJobQueue_AJobThatExitsZeroSettlesDone(t *testing.T) {
	m := wqLiveModel(t)
	id := wqLaunch(t, m, "sh", "-c", "echo hi")

	rec := wqSettle(t, m, id)

	assert.Equal(t, sched.Done, rec.State)
	require.NotNil(t, rec.Exit)
	assert.Equal(t, 0, rec.Exit.Code)
	assert.Equal(t, rec.Exit.Ended, rec.Ended)
	assert.Empty(t, rec.Failure)
	out, err := os.ReadFile(filepath.Join(m.queue.jobDir(id), sched.LogFile))
	require.NoError(t, err)
	assert.Equal(t, "hi\n", string(out))
}

func TestJobQueue_ANonZeroExitOrASignalSettlesFailed(t *testing.T) {
	t.Run("a non-zero exit", func(t *testing.T) {
		m := wqLiveModel(t)
		rec := wqSettle(t, m, wqLaunch(t, m, "sh", "-c", "exit 3"))

		assert.Equal(t, sched.Failed, rec.State)
		require.NotNil(t, rec.Exit)
		assert.Equal(t, 3, rec.Exit.Code)
		assert.Empty(t, rec.Failure, "it ran: nothing failed to start")
	})
	t.Run("a signal", func(t *testing.T) {
		m := wqLiveModel(t)
		rec := wqSettle(t, m, wqLaunch(t, m, "sh", "-c", "kill -9 $$"))

		assert.Equal(t, sched.Failed, rec.State)
		require.NotNil(t, rec.Exit)
		assert.Equal(t, "SIGKILL", rec.Exit.Signal)
	})
}

func TestJobQueue_ACommandThatCannotBeExecutedFailsWithItsError(t *testing.T) {
	m := wqLiveModel(t)

	rec := wqSettle(t, m, wqLaunch(t, m, "/no/such/binary"))

	assert.Equal(t, sched.Failed, rec.State)
	require.NotNil(t, rec.Exit)
	assert.NotEmpty(t, rec.Exit.Error)
	assert.Equal(t, rec.Exit.Error, rec.Failure, "the notice says it could not start")
}

func TestJobQueue_ACancelledRunningJobGetsSIGTERMToItsGroupAndSettlesCancelled(t *testing.T) {
	m := wqLiveModel(t)
	id := wqLaunch(t, m, "sh", "-c", "sleep 30")
	m.queue.store.Jobs[m.queue.index(id)].CancelReason = "from the work view"

	rec := wqSettle(t, m, id)

	assert.Equal(t, sched.Cancelled, rec.State, "whatever its exit")
	assert.Equal(t, "from the work view", rec.CancelReason)
	assert.False(t, rec.SignalledAt.IsZero(), "SIGTERM went to its group")
	require.NotNil(t, rec.Exit, "the shim stayed up to report")
	assert.Equal(t, "SIGTERM", rec.Exit.Signal)
}

func TestJobQueue_ACancelledJobThatIgnoresSIGTERMGetsSIGKILLAfterTheEscalation(t *testing.T) {
	m := wqLiveModel(t)
	m.queue.escalation = 300 * time.Millisecond
	ready := filepath.Join(t.TempDir(), "ready")
	id := wqLaunch(t, m, "sh", "-c", `trap "" TERM; echo ok > "$1"; while :; do sleep 0.1; done`, "sh", ready)
	wqWait(t, "the command to ignore SIGTERM", func() bool { _, err := os.Stat(ready); return err == nil })
	m.queue.store.Jobs[m.queue.index(id)].CancelReason = "with loom job cancel"

	wqPass(t, m)
	first := wqRecord(t, m, id)
	require.Equal(t, sched.Running, first.State, "SIGTERM alone does not end it")
	require.False(t, first.SignalledAt.IsZero())

	rec := wqSettle(t, m, id)

	assert.Equal(t, sched.Cancelled, rec.State)
	assert.Nil(t, rec.Exit, "SIGKILL took the shim with it, so nothing reported")
}

// A shim that vanished without writing its result leaves a lost job, and its
// group, which nothing now watches, is sent SIGKILL: the sample shows
// processes in the group and none with the shim's pid (spec §5.4).
func TestJobQueue_AShimThatVanishedLeavesALostJobAndItsOrphanedGroupIsKilled(t *testing.T) {
	m := wqLiveModel(t)
	beat := filepath.Join(t.TempDir(), "beat")
	id := wqLaunch(t, m, "sh", "-c", wqBeatScript, "sh", beat)
	wqWait(t, "the first beat", func() bool { _, err := os.Stat(beat); return err == nil })
	p, err := os.FindProcess(wqRecord(t, m, id).PID)
	require.NoError(t, err)
	require.NoError(t, p.Kill(), "the shim alone: its command and the command's group live on")
	dir := m.queue.jobDir(id)
	wqWait(t, "the shim's lock to be released", func() bool { alive, _ := sched.Probe(dir); return !alive })
	require.True(t, wqBeats(beat), "the orphaned command still runs")

	rec := wqSettle(t, m, id)

	assert.Equal(t, sched.Lost, rec.State)
	assert.Equal(t, lostWhy, rec.Failure)
	assert.Nil(t, rec.Exit)
	wqWait(t, "the orphaned group to die", func() bool { return !wqBeats(beat) })
}

// Only a lost job's group is killed: a job that wrote its result has nothing
// the queue lost, whatever it left running.
func TestJobQueue_OnlyALostJobsGroupIsKilledNotADoneJobsLeftovers(t *testing.T) {
	m := wqLiveModel(t)
	beat := filepath.Join(t.TempDir(), "beat")
	script := `sh -c '` + wqBeatScript + `' sh "$1" >/dev/null 2>&1 & exit 0`
	id := wqLaunch(t, m, "sh", "-c", script, "sh", beat)
	shim := wqRecord(t, m, id).PID
	// The leftover keeps the shim's pid in use as its group, so no other
	// process can have taken it by the time this signal is sent.
	t.Cleanup(func() { _ = sched.SignalGroup(shim, syscall.SIGKILL) })

	rec := wqSettle(t, m, id)

	assert.Equal(t, sched.Done, rec.State)
	assert.True(t, wqBeats(beat), "the job wrote its result: nothing is lost, so nothing is killed")
}

// A process that holds the shim's pid is a stranger (a reused pid) or the shim
// itself: its group is never signalled, lost job or not (spec §5.4).
func TestJobQueue_ALostJobsGroupIsLeftAloneWhenAProcessHoldsTheShimsPid(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancelled=%v", cancelled), func(t *testing.T) {
			m := wqLiveModel(t)
			stranger, beat := wqBystander(t)
			rec := wqEnded(1, 0)
			rec.State, rec.Exit, rec.Ended, rec.PID = sched.Running, nil, time.Time{}, stranger
			if cancelled {
				rec.CancelReason = "from the work view"
			}
			wqSeed(t, m, rec)

			got := wqSettle(t, m, 1)

			if cancelled {
				assert.Equal(t, sched.Cancelled, got.State)
			} else {
				assert.Equal(t, sched.Lost, got.State)
			}
			assert.True(t, wqBeats(beat), "the stranger carries on")
		})
	}
}

func TestJobQueue_AGroupIsOrphanedOnlyWhenItHasMembersAndNoProcessHasTheShimsPid(t *testing.T) {
	snap := func(procs ...sched.Proc) sched.Snapshot { return sched.Snapshot{Taken: time.Now(), Procs: procs} }
	cases := []struct {
		name string
		snap sched.Snapshot
		pgid int
		want bool
	}{
		{"members, no leader", snap(sched.Proc{PID: 20, PGID: 10}, sched.Proc{PID: 21, PGID: 10}), 10, true},
		{"the shim itself lives", snap(sched.Proc{PID: 10, PGID: 10}, sched.Proc{PID: 20, PGID: 10}), 10, false},
		{"a stranger holds the pid", snap(sched.Proc{PID: 10, PGID: 3}, sched.Proc{PID: 20, PGID: 10}), 10, false},
		{"nobody in the group", snap(sched.Proc{PID: 30, PGID: 30}), 10, false},
		{"pid 0 names the caller's own group", snap(sched.Proc{PID: 20, PGID: 0}), 0, false},
		{"pid 1 names everyone", snap(sched.Proc{PID: 20, PGID: 1}), 1, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, orphanedGroup(c.snap, c.pgid))
		})
	}
}

// A signal goes to a job's group only after its lock test proves the shim
// alive (spec §5.5): a stranger's group, a free lock and pid 0 are refused.
func TestJobQueue_ASignalGoesOnlyToAGroupWhoseShimHoldsItsLock(t *testing.T) {
	stranger, beat := wqBystander(t)

	t.Run("pid 0 names the caller's own group", func(t *testing.T) {
		sent, err := signalAlive(t.TempDir(), 0, syscall.Signal(0))
		assert.False(t, sent)
		assert.Error(t, err)
	})
	t.Run("a free lock", func(t *testing.T) {
		sent, err := signalAlive(t.TempDir(), stranger, syscall.SIGTERM)
		assert.NoError(t, err)
		assert.False(t, sent)
		assert.True(t, wqBeats(beat), "the stranger was not signalled")
	})
	t.Run("a held lock", func(t *testing.T) {
		m := wqLiveModel(t)
		id := wqLaunch(t, m, "sh", "-c", "sleep 30")

		sent, err := signalAlive(m.queue.jobDir(id), wqRecord(t, m, id).PID, syscall.SIGTERM)

		require.NoError(t, err)
		assert.True(t, sent)
		rec := wqSettle(t, m, id)
		assert.Equal(t, sched.Failed, rec.State, "nobody asked for a cancel")
		require.NotNil(t, rec.Exit)
		assert.Equal(t, "SIGTERM", rec.Exit.Signal)
	})
}

// wqSilent seeds what a start that timed out leaves: a failed record for a
// command that beats into beat, and an entry owing its shim a SIGKILL. start
// starts the shim (its dir exists, with the spec written) and returns its pid.
func wqSilent(t *testing.T, m *Model, beat string, start func(dir string) int) (id sched.JobID, pid int) {
	t.Helper()
	rec := wqEnded(1, 0)
	rec.State, rec.Exit, rec.Failure = sched.Failed, nil, errRunnerSilent.Error()
	rec.Argv, rec.Cwd = []string{"sh", "-c", wqBeatScript, "sh", beat}, t.TempDir()
	wqSeed(t, m, rec)
	dir := m.queue.jobDir(rec.ID)
	require.NoError(t, sched.WriteSpec(dir, sched.Spec{ID: rec.ID, Argv: rec.Argv, Cwd: rec.Cwd, Env: []string{"PATH=" + os.Getenv("PATH")}}))
	pid = start(dir)
	t.Cleanup(func() { _ = sched.SignalGroup(pid, syscall.SIGKILL) })
	m.queue.silent[rec.ID] = silentShim{pid: pid, since: time.Now()}
	return rec.ID, pid
}

// A shim slowed by memory pressure can take its lock after the 5 s wait, and
// would run the command while its job reads failed (spec §5.3 step 4). The
// entry a failed start leaves keeps the group due a SIGKILL: a free lock with
// the shim still there proves nothing, and the kill goes once a lock test
// finds the shim alive.
func TestJobQueue_AFailedStartsSlowShimIsKilledOnceItsLockProvesItAlive(t *testing.T) {
	m := wqLiveModel(t)
	beat := filepath.Join(t.TempDir(), "beat")
	slow := sched.TestRunner()
	slow.Shim = append([]string{"sh", "-c", `sleep 1.5; exec "$@"`, "sh"}, slow.Shim...)
	id, _ := wqSilent(t, m, beat, func(dir string) int {
		pid, err := slow.Start(dir)
		require.NoError(t, err)
		return pid
	})

	wqPass(t, m)
	require.Contains(t, m.queue.silent, id, "the shim is not up yet: a free lock proves nothing")

	wqWait(t, "the shim to take its lock and be killed", func() bool {
		wqPass(t, m)
		_, owed := m.queue.silent[id]
		return !owed
	})

	assert.Equal(t, sched.Failed, wqRecord(t, m, id).State, "the job stays failed")
	assert.False(t, wqBeats(beat), "the command does not run on, unaccounted")
}

// Once no process has the shim's pid it cannot take its lock any more: the
// entry goes, with no signal.
func TestJobQueue_AFailedStartsShimThatIsGoneIsDropped(t *testing.T) {
	m := wqLiveModel(t)
	id, _ := wqSilent(t, m, filepath.Join(t.TempDir(), "beat"), func(dir string) int {
		pid, err := sched.Runner{Shim: []string{"true"}}.Start(dir)
		require.NoError(t, err)
		return pid
	})

	wqWait(t, "the entry to go", func() bool {
		wqPass(t, m)
		_, owed := m.queue.silent[id]
		return !owed
	})
}

// A shim that took its lock, started the command and died leaves a group
// nothing watches: a gone shim's orphaned group is sent SIGKILL under the
// lost-job rule.
func TestJobQueue_AFailedStartsOrphanedGroupIsKilledOnceItsShimIsGone(t *testing.T) {
	m := wqLiveModel(t)
	beat := filepath.Join(t.TempDir(), "beat")
	var shim int
	id, _ := wqSilent(t, m, beat, func(dir string) int {
		pid, err := m.queue.runner.Start(dir)
		require.NoError(t, err)
		shim = pid
		return pid
	})
	wqWait(t, "the first beat", func() bool { _, err := os.Stat(beat); return err == nil })
	p, err := os.FindProcess(shim)
	require.NoError(t, err)
	require.NoError(t, p.Kill(), "the shim alone: its command lives on")
	dir := m.queue.jobDir(id)
	wqWait(t, "the shim's lock to be released", func() bool { alive, _ := sched.Probe(dir); return !alive })
	require.True(t, wqBeats(beat), "the orphaned command still runs")

	wqWait(t, "the entry to go", func() bool {
		wqPass(t, m)
		_, owed := m.queue.silent[id]
		return !owed
	})

	wqWait(t, "the orphaned group to die", func() bool { return !wqBeats(beat) })
}

// A process that has the shim's pid may be a stranger that reused it: its
// group is never signalled, and the entry waits until silentLimit gives it up.
func TestJobQueue_AFailedStartsEntryNeverSignalsAStrangerAndIsGivenUpAfterTheLimit(t *testing.T) {
	m := wqLiveModel(t)
	stranger, beat := wqBystander(t)
	id, _ := wqSilent(t, m, filepath.Join(t.TempDir(), "unused"), func(string) int { return stranger })

	wqPass(t, m)
	wqPass(t, m)

	assert.Contains(t, m.queue.silent, id, "the pid is in use: not gone, and nothing proves the shim alive")
	assert.True(t, wqBeats(beat), "the stranger was not signalled")

	m.queue.silent[id] = silentShim{pid: stranger, since: time.Now().Add(-silentLimit)}
	assert.Empty(t, m.queuePassFor(false).jobs, "an entry owed for silentLimit is given up")
	assert.NotContains(t, m.queue.silent, id)
	assert.True(t, wqBeats(beat), "and still nothing was signalled")
}

func TestJobQueue_AJobsUsageIsSampledFromItsProcessGroup(t *testing.T) {
	m := wqLiveModel(t)
	id := wqLaunch(t, m, "sh", "-c", "sleep 30")

	wqPass(t, m)
	wqPass(t, m)

	u, ok := m.queue.usage[id]
	require.True(t, ok)
	assert.Greater(t, u.RSSKB, int64(0))
	assert.GreaterOrEqual(t, wqRecord(t, m, id).PeakRSSKB, u.RSSKB)
	rev := m.queue.rev

	m.queue.store.Jobs[m.queue.index(id)].CancelReason = "when the test ends"
	wqSettle(t, m, id)

	assert.NotContains(t, m.queue.usage, id, "a settled job's usage is dropped")
	assert.NotContains(t, m.queue.cpuMarks, id)
	assert.Greater(t, m.queue.rev, rev, "a client is told")
}

// A job whose start job is in flight may not hold its lock yet: the watch
// must not take it for lost.
func TestJobQueue_AJobStillStartingIsLeftToItsStartJob(t *testing.T) {
	m := wqLiveModel(t)
	rec := wqEnded(1, 0)
	rec.State, rec.Exit, rec.Ended = sched.Running, nil, time.Time{}
	wqSeed(t, m, rec)
	m.queue.starting[1] = true

	assert.False(t, m.maybeQueue(), "nothing to watch yet")
	assert.Empty(t, m.Drain().Jobs)
	assert.Equal(t, sched.Running, wqRecord(t, m, 1).State)

	delete(m.queue.starting, 1)
	wqPass(t, m)
	assert.Equal(t, sched.Lost, wqRecord(t, m, 1).State, "once started, an absent shim is a lost job")
}

func TestJobQueue_AnObservationOfAJobThatMovedOnChangesNothing(t *testing.T) {
	m := wqModel(t)
	done := wqEnded(1, 0)
	done.Notify = false
	wqSeed(t, m, done)
	rev := m.queue.rev

	m.Deliver(queuePassResult{obs: []jobObs{{id: 1, gone: true}, {id: 99, gone: true}}})

	assert.Equal(t, sched.Done, wqRecord(t, m, 1).State, "a finished job is not settled again")
	assert.Equal(t, rev, m.queue.rev)
}

// A change the model makes on its own stays in memory when its write fails,
// raises one notice, and is written with the next save (spec §5.9).
func TestJobQueue_ASettleWhoseWriteFailsStaysInMemoryAndIsWrittenWithTheNextSave(t *testing.T) {
	m := wqLiveModel(t)
	id := wqLaunch(t, m, "sh", "-c", "exit 0")
	unblock := wqBlockJobsJSON(t, m)
	var events []Event
	wqWait(t, "the job to settle", func() bool {
		events = append(events, wqPass(t, m)...)
		return wqRecord(t, m, id).State.Finished()
	})

	assert.Equal(t, sched.Done, wqRecord(t, m, id).State, "the change stays in memory")
	m.saveQueueQuiet("another change")
	events = append(events, wqPump(t, m)...)
	var notices []string
	for _, ev := range events {
		if n, ok := ev.(Notice); ok && n.Err != nil {
			notices = append(notices, n.Err.Error())
		}
	}
	require.Len(t, notices, 1, "one notice for a run of failures")
	assert.Contains(t, notices[0], "kept in memory")

	unblock()
	require.True(t, m.saveQueueQuiet("the next save"))

	saved, err := sched.LoadStore(m.queue.dir)
	require.NoError(t, err)
	require.Len(t, saved.Jobs, 1)
	assert.Equal(t, sched.Done, saved.Jobs[0].State)
	assert.False(t, m.queue.saveFailed)
}

func TestJobQueue_AWatchRequestedMidFlightRunsAgainWhenItLands(t *testing.T) {
	m := wqLiveModel(t)
	wqLaunch(t, m, "sh", "-c", "sleep 30")
	require.True(t, m.maybeQueue())
	jobs := m.Drain().Jobs
	require.Len(t, jobs, 1)
	m.gate(gateQueue).request() // a cancel arrives while the pass runs
	result := jobs[0]()

	m.Deliver(result)

	again := m.Drain().Jobs
	require.Len(t, again, 1, "the pass runs again as soon as the first one lands")
	m.Deliver(again[0]())
	assert.False(t, m.gate(gateQueue).inFlight)
}

func TestJobQueue_TheTickWatchesRunningJobs(t *testing.T) {
	m := wqLiveModel(t)
	wqLaunch(t, m, "sh", "-c", "sleep 30")

	m.Tick()

	assert.True(t, m.gate(gateQueue).inFlight, "the health tick dispatches the watch")
}

func TestJobQueue_RetentionDropsTheOldestFinishedJobsAndTheirDirectories(t *testing.T) {
	m := wqModel(t)
	wqConfig(t, m, `{"keep_finished": 2}`)
	base := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	for id := sched.JobID(1); id <= 4; id++ {
		rec := wqEnded(id, 0)
		rec.Ended = base.Add(time.Duration(id) * time.Minute)
		wqSeed(t, m, rec)
	}
	running := wqEnded(5, 0)
	running.State, running.Exit, running.Ended = sched.Running, nil, time.Time{}
	wqSeed(t, m, running)
	m.queue.store.NextID = 9
	m.refreshPolicy()

	m.afterSettle()
	wqPump(t, m)

	var ids []sched.JobID
	for _, rec := range m.queue.store.Jobs {
		ids = append(ids, rec.ID)
	}
	assert.Equal(t, []sched.JobID{3, 4, 5}, ids, "the two newest finished jobs stay, and the running one is no finished job")
	assert.NoDirExists(t, m.queue.jobDir(1))
	assert.NoDirExists(t, m.queue.jobDir(2))
	for id := sched.JobID(3); id <= 5; id++ {
		assert.DirExists(t, m.queue.jobDir(id))
	}
	saved, err := sched.LoadStore(m.queue.dir)
	require.NoError(t, err)
	assert.Len(t, saved.Jobs, 3)
	assert.Equal(t, sched.JobID(9), saved.NextID, "the counter never goes back")
}

// A directory goes only once the shorter jobs.json that no longer lists its
// job is on disk.
func TestJobQueue_RetentionKeepsTheDirectoriesUntilTheShorterJobsFileIsWritten(t *testing.T) {
	m := wqModel(t)
	wqConfig(t, m, `{"keep_finished": 1}`)
	for id := sched.JobID(1); id <= 3; id++ {
		wqSeed(t, m, wqEnded(id, 0))
	}
	m.refreshPolicy()
	unblock := wqBlockJobsJSON(t, m)

	m.afterSettle()
	wqPump(t, m)

	assert.Len(t, m.queue.store.Jobs, 1)
	assert.DirExists(t, m.queue.jobDir(1), "jobs.json still lists job 1")
	assert.DirExists(t, m.queue.jobDir(2))

	unblock()
	require.True(t, m.saveQueueQuiet("the next save"))
	wqPump(t, m)

	assert.NoDirExists(t, m.queue.jobDir(1))
	assert.NoDirExists(t, m.queue.jobDir(2))
	assert.DirExists(t, m.queue.jobDir(3))
}

func TestJobQueue_RetentionDropsNothingWhileAdmissionIsPaused(t *testing.T) {
	m := wqModel(t)
	wqConfig(t, m, `{"cpus": 5000, "keep_finished": 1}`)
	for id := sched.JobID(1); id <= 3; id++ {
		wqSeed(t, m, wqEnded(id, 0))
	}
	m.refreshPolicy()
	require.NotEmpty(t, m.queue.paused)

	m.afterSettle()
	wqPump(t, m)

	assert.Len(t, m.queue.store.Jobs, 3, "the limit is unknown while the block is invalid")
}
```

In `core/gate_test.go`, `TestProductionGatedJobsYieldOneResult` gets the queue's case. Replace

```go
	t.Run("build_sweep", func(t *testing.T) {
		m := sweepModel(t, sweepInst(t, "idle", session.Paused, false))
		result := inner(t, m, m.maybeBuildSweep(), gateBuildSweep)
		assert.IsType(t, buildSweepResult{}, result)
	})
}
```

with

```go
	t.Run("build_sweep", func(t *testing.T) {
		m := sweepModel(t, sweepInst(t, "idle", session.Paused, false))
		result := inner(t, m, m.maybeBuildSweep(), gateBuildSweep)
		assert.IsType(t, buildSweepResult{}, result)
	})

	t.Run("queue", func(t *testing.T) {
		m := wqModel(t)
		rec := wqEnded(1, 0)
		rec.State, rec.Exit, rec.Ended = sched.Running, nil, time.Time{}
		wqSeed(t, m, rec)
		result := inner(t, m, m.maybeQueue(), gateQueue)
		assert.IsType(t, queuePassResult{}, result)
	})
}
```

and add `"github.com/aidan-bailey/loom/sched"` to that file's imports: replace

```go
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
```

with

```go
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
```

- [ ] **Step 2: Run them to see them fail**

Run: `CGO_ENABLED=0 go test ./core/ -run 'TestJobQueue_|TestProductionGatedJobsYieldOneResult'`

Expected: the package does not compile: `undefined: queuePassResult`, `undefined: jobObs`, `undefined: signalAlive`, `undefined: orphanedGroup`, `m.maybeQueue undefined`, `m.afterSettle undefined`.

- [ ] **Step 3: Implement**

Create `core/queue_watch.go`:

```go
package core

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"syscall"
	"time"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/sched"
)

const (
	// sampleTimeout bounds the one `ps` of a pass (spec §5.4).
	sampleTimeout = 5 * time.Second
	// defaultEscalation is how long after a cancel's SIGTERM a job whose shim
	// still holds its lock gets SIGKILL (spec §5.5).
	defaultEscalation = 10 * time.Second
)

// escalateAfter is the time between a cancel's SIGTERM and its SIGKILL.
func (q *queueState) escalateAfter() time.Duration {
	if q.escalation > 0 {
		return q.escalation
	}
	return defaultEscalation
}

// endJob records, on rec itself, that it ended in state at now: the change a
// job's end makes to its record, without its consequences (settledJob), so a
// request can undo it if its write fails.
func endJob(rec *sched.Record, state sched.State, now time.Time) {
	rec.State = state
	if rec.Ended.IsZero() {
		rec.Ended = now
	}
}

// settledJob does what a job's end brings with it: its usage and wait
// reasons go, a client is told (rev), and its owner is sent the notice. Job
// i of the store has already been ended (endJob).
func (m *Model) settledJob(i int) {
	q := m.queue
	rec := q.store.Jobs[i]
	delete(q.usage, rec.ID)
	delete(q.cpuMarks, rec.ID)
	delete(q.waiting, rec.ID)
	delete(q.starting, rec.ID)
	q.moved()
	m.sendJobNotice(rec)
}

// finishJob ends job i of the store in state and settles it.
func (m *Model) finishJob(i int, state sched.State, now time.Time) {
	endJob(&m.queue.store.Jobs[i], state, now)
	m.settledJob(i)
}

// afterSettle runs after one or more jobs ended: retention trims the finished
// jobs, and the write that follows is quiet (spec §5.9).
func (m *Model) afterSettle() {
	m.retainFinished()
	m.saveQueueQuiet("settle")
}

// lockHeld reports whether a shim holds jobDir's lock (sched.Probe). A job
// directory with no lock file has had no shim take it.
func lockHeld(jobDir string) (bool, error) {
	alive, err := sched.Probe(jobDir)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return alive, err
}

// signalAlive sends sig to the process group pgid (the shim's pid: the shim
// leads its group) only when jobDir's lock test proves its shim alive. A pid
// recorded in a file may have been reused by a stranger, and pid 0 and 1
// would name loom's own group and everyone's, so a signal goes only where the
// lock proves the pid is still the shim's. It reports whether it signalled.
func signalAlive(jobDir string, pgid int, sig syscall.Signal) (sent bool, err error) {
	if pgid <= 1 {
		return false, fmt.Errorf("no process group to signal (pid %d)", pgid)
	}
	alive, err := lockHeld(jobDir)
	if err != nil {
		return false, fmt.Errorf("test the job's lock: %w", err)
	}
	if !alive {
		return false, nil
	}
	if err := sched.SignalGroup(pgid, sig); err != nil {
		return false, fmt.Errorf("signal the job's group: %w", err)
	}
	return true, nil
}

// passJob is one record gateQueue looks at, copied from the store.
type passJob struct {
	id          sched.JobID
	pid         int  // 0: not recorded yet
	queued      bool // a queued record, probed at boot only
	cancelled   bool // a cancel was requested
	signalledAt time.Time
	silent      bool // not a record: a failed start's shim, still owed a SIGKILL (queueState.silent)
}

// queuePass is gateQueue's input: copies only, since its job runs off the loop.
type queuePass struct {
	dir      string
	now      time.Time
	escalate time.Duration
	jobs     []passJob
}

// jobObs is what a pass saw of one job.
type jobObs struct {
	id      sched.JobID
	queued  bool
	pid     int            // the shim's, from the record or started.json
	err     error          // the lock test failed: nothing is known
	gone    bool           // the lock is free: the shim is gone
	started *sched.Started // started.json, read when the record had no pid
	exit    *sched.Exit    // exit.json, read once the lock was free
	termAt  time.Time      // SIGTERM went to the group now
	killed  bool           // SIGKILL went to the group
	silent  bool           // a failed start's shim (passJob.silent)
	cleared bool           // silent only: its SIGKILL went, or the shim is gone, so nothing is owed
	// signalErr is why a signal failed.
	signalErr error
}

// queuePassResult is gateQueue's result: what each job's lock and result
// files said, and the one process sample (nil when it failed or timed out).
type queuePassResult struct {
	at        time.Time
	obs       []jobObs
	snap      *sched.Snapshot
	sampleErr error
}

// maybeQueue dispatches gateQueue's pass when it is due: nothing when no job
// runs (or is owed a SIGKILL as a failed start's shim), or its start job is
// still in flight.
func (m *Model) maybeQueue() bool {
	if !m.queue.ready() {
		return false
	}
	return m.dispatchGated(gateQueue, time.Now(), func() Job {
		pass := m.queuePassFor(false)
		if len(pass.jobs) == 0 {
			return nil
		}
		return func() any { return runQueuePass(pass) }
	})
}

// queuePassFor snapshots the records a pass looks at: the running ones whose
// start has landed and, at boot (withQueued), the queued ones too, since a
// start whose save failed leaves a running shim behind a queued record. It
// adds the shims of failed starts that are still owed a SIGKILL, and drops
// those owed for silentLimit.
func (m *Model) queuePassFor(withQueued bool) queuePass {
	q := m.queue
	p := queuePass{dir: q.dir, now: time.Now(), escalate: q.escalateAfter()}
	for id, s := range q.silent {
		if p.now.Sub(s.since) >= silentLimit {
			log.For("core").Warn("queue.silent_shim_given_up", "job", int64(id), "pid", s.pid)
			delete(q.silent, id)
			continue
		}
		p.jobs = append(p.jobs, passJob{id: id, pid: s.pid, silent: true})
	}
	for i := range q.store.Jobs {
		rec := &q.store.Jobs[i]
		switch {
		case rec.State == sched.Running && !q.starting[rec.ID]:
			p.jobs = append(p.jobs, passJob{id: rec.ID, pid: rec.PID, cancelled: rec.CancelReason != "", signalledAt: rec.SignalledAt})
		case withQueued && rec.State == sched.Queued:
			p.jobs = append(p.jobs, passJob{id: rec.ID, queued: true})
		}
	}
	return p
}

// runQueuePass is gateQueue's job (and Boot's adoption): for each job it tests
// the lock without blocking, reads the result files of one whose lock is free,
// and carries out a cancel (SIGTERM, then SIGKILL after the escalation) on one
// whose lock is held; then it takes one process sample and kills the group of
// a job whose shim vanished without a result, if the sample proves the group
// is the job's. A failed start's shim still owed a SIGKILL gets its lock
// tested too (watchSilent). It reads no model state.
func runQueuePass(p queuePass) queuePassResult {
	res := queuePassResult{at: p.now}
	running := false
	for _, j := range p.jobs {
		dir := sched.JobDir(p.dir, j.id)
		if j.silent {
			ob := watchSilent(dir, j)
			running = running || ob.gone
			res.obs = append(res.obs, ob)
			continue
		}
		ob := jobObs{id: j.id, queued: j.queued, pid: j.pid}
		alive, err := lockHeld(dir)
		switch {
		case err != nil:
			ob.err = err
		case alive:
			if ob.pid == 0 {
				ob.started, ob.pid = readStarted(dir)
			}
			if j.cancelled && !j.queued {
				tendCancel(&ob, dir, j, p)
			}
		default:
			ob.gone = true
			if exit, err := sched.ReadExit(dir); err == nil {
				ob.exit = &exit
			}
			if ob.pid == 0 {
				ob.started, ob.pid = readStarted(dir)
			}
		}
		if !j.queued && ob.err == nil {
			running = true
		}
		res.obs = append(res.obs, ob)
	}
	if running {
		res.snap, res.sampleErr = sample()
	}
	for i := range res.obs {
		ob := &res.obs[i]
		if ob.silent {
			clearSilent(ob, res.snap)
			continue
		}
		if !ob.gone || ob.exit != nil || ob.queued || res.snap == nil || !orphanedGroup(*res.snap, ob.pid) {
			continue
		}
		if err := sched.SignalGroup(ob.pid, syscall.SIGKILL); err != nil {
			ob.signalErr = err
			continue
		}
		ob.killed = true
	}
	return res
}

// watchSilent tests the lock of a failed start's shim (spec §5.3 step 4: the
// start timed out and the shim's group is to die). A held lock proves the shim
// alive: its group gets SIGKILL now, through signalAlive, and nothing is owed
// any more. A free lock proves nothing yet, since a shim slowed down may not
// have taken it: the entry stays, and clearSilent decides from the sample.
func watchSilent(dir string, j passJob) jobObs {
	ob := jobObs{id: j.id, pid: j.pid, silent: true}
	alive, err := lockHeld(dir)
	switch {
	case err != nil:
		ob.err = err
	case alive:
		ob.killed, ob.signalErr = signalAlive(dir, j.pid, syscall.SIGKILL)
		ob.cleared = ob.killed
	default:
		ob.gone = true
	}
	return ob
}

// clearSilent decides, from the pass's sample, whether a silent shim whose
// lock was free is gone: no process has its pid, so it cannot take the lock
// any more. Members left in its group (a command it started and died on) are
// sent SIGKILL under the lost-job rule (orphanedGroup); an empty group needs
// nothing. A process that still has the pid (the shim not yet up, or a
// stranger that reused it) keeps the entry, and so does a failed sample.
func clearSilent(ob *jobObs, snap *sched.Snapshot) {
	if !ob.gone || snap == nil {
		return
	}
	for _, proc := range snap.Procs {
		if proc.PID == ob.pid {
			return
		}
	}
	if orphanedGroup(*snap, ob.pid) {
		if err := sched.SignalGroup(ob.pid, syscall.SIGKILL); err != nil {
			ob.signalErr = err
			return
		}
		ob.killed = true
	}
	ob.cleared = true
}

// tendCancel carries out the cancel of a job whose lock is held: SIGTERM to
// its group once, and SIGKILL once the escalation has passed since. Each goes
// through signalAlive, which tests the lock again.
func tendCancel(ob *jobObs, dir string, j passJob, p queuePass) {
	switch {
	case j.signalledAt.IsZero():
		sent, err := signalAlive(dir, ob.pid, syscall.SIGTERM)
		ob.signalErr = err
		if sent {
			ob.termAt = p.now
		}
	case p.now.Sub(j.signalledAt) >= p.escalate:
		sent, err := signalAlive(dir, ob.pid, syscall.SIGKILL)
		ob.signalErr = err
		ob.killed = sent
	}
}

// readStarted reads a job's started.json: the record and the shim's pid, nil
// and 0 when there is none.
func readStarted(jobDir string) (*sched.Started, int) {
	st, err := sched.ReadStarted(jobDir)
	if err != nil {
		return nil, 0
	}
	return &st, st.PID
}

// sample takes the one process sample of a pass, within sampleTimeout.
func sample() (*sched.Snapshot, error) {
	ctx, cancel := context.WithTimeout(context.Background(), sampleTimeout)
	defer cancel()
	snap, err := sched.Sample(ctx)
	if err != nil {
		return nil, err
	}
	return &snap, nil
}

// orphanedGroup reports whether snap proves the process group pgid a dead
// shim's: it has members, and no process has the shim's pid. A live process
// with that pid is the shim itself or a stranger that reused the pid, and its
// group is never signalled; pid 0 and 1 name loom's own group and everyone's.
func orphanedGroup(snap sched.Snapshot, pgid int) bool {
	if pgid <= 1 {
		return false
	}
	for _, p := range snap.Procs {
		if p.PID == pgid {
			return false
		}
	}
	_, _, members, _ := snap.Group(pgid)
	return members > 0
}

// deliverQueuePass applies a pass on the loop: a job whose lock was free is
// settled, one whose lock is held records its pid and its SIGTERM, and usage
// is updated from the sample. An observation of a record that is no longer in
// the state the pass saw it in (a cancel, a settle, a drop since) is ignored.
func (m *Model) deliverQueuePass(r queuePassResult) {
	q := m.queue
	if !q.ready() {
		return
	}
	settled, changed := false, false
	for _, ob := range r.obs {
		if ob.silent {
			m.noteSilent(ob)
			continue
		}
		i := q.index(ob.id)
		if i < 0 {
			continue
		}
		want := sched.Running
		if ob.queued {
			want = sched.Queued
		}
		if q.store.Jobs[i].State != want {
			continue
		}
		if ob.signalErr != nil {
			log.For("core").Warn("queue.signal_failed", "job", int64(ob.id), "err", ob.signalErr)
		}
		switch {
		case ob.err != nil:
			log.For("core").Warn("queue.lock_test_failed", "job", int64(ob.id), "err", ob.err)
		case ob.gone && ob.queued && ob.exit == nil:
			// A queued record whose shim never ran: it stays queued.
		case ob.gone:
			m.settleGone(i, ob, r.at)
			settled = true
		default:
			if m.noteAlive(i, ob, r.at) {
				changed = true
			}
		}
	}
	m.applyUsage(r)
	switch {
	case settled:
		m.afterSettle()
	case changed:
		m.saveQueueQuiet("watch")
	}
}

// noteSilent applies what a pass saw of a failed start's shim: its entry goes
// once its SIGKILL went or the shim is gone, and stays after a lock test or a
// signal that failed.
func (m *Model) noteSilent(ob jobObs) {
	q := m.queue
	if _, ok := q.silent[ob.id]; !ok {
		return
	}
	if ob.signalErr != nil {
		log.For("core").Warn("queue.signal_failed", "job", int64(ob.id), "err", ob.signalErr)
	}
	if ob.err != nil {
		log.For("core").Warn("queue.lock_test_failed", "job", int64(ob.id), "err", ob.err)
		return
	}
	if ob.killed {
		log.For("core").Warn("queue.silent_shim_killed", "job", int64(ob.id), "pgid", ob.pid)
	}
	if ob.cleared {
		delete(q.silent, ob.id)
	}
}

// noteAlive records what a pass saw of a job whose lock is held: a shim behind
// a queued record (a start whose save failed) makes it running, so it is
// never started twice; a pid and start time it learned from started.json; and
// the moment its group got SIGTERM. It reports whether the record changed.
func (m *Model) noteAlive(i int, ob jobObs, now time.Time) bool {
	q := m.queue
	rec := &q.store.Jobs[i]
	changed := false
	if rec.State == sched.Queued {
		rec.State, rec.Started, changed = sched.Running, now, true
		delete(q.waiting, rec.ID)
	}
	if ob.started != nil && rec.PID == 0 {
		rec.PID, rec.Started, changed = ob.started.PID, ob.started.Started, true
	}
	if !ob.termAt.IsZero() && rec.SignalledAt.IsZero() {
		rec.SignalledAt, changed = ob.termAt, true
	}
	if changed {
		q.moved()
	}
	return changed
}

// settleGone settles job i, whose shim is gone, from what the pass read
// (spec §5.4). Cancelled when a cancel was requested, whatever the exit; else
// done for exit 0, failed for another code, a signal or a start error, and
// lost when exit.json is missing or unreadable.
func (m *Model) settleGone(i int, ob jobObs, now time.Time) {
	rec := &m.queue.store.Jobs[i]
	if ob.started != nil && rec.PID == 0 {
		rec.PID, rec.Started = ob.started.PID, ob.started.Started
	}
	cancelled := rec.CancelReason != ""
	state := sched.Lost
	if e := ob.exit; e != nil {
		x := *e
		rec.Exit, rec.Ended = &x, x.Ended
		rec.PeakRSSKB = max(rec.PeakRSSKB, x.MaxRSSKB)
		switch {
		case x.Error != "":
			rec.Failure, state = x.Error, sched.Failed
		case x.Code == 0 && x.Signal == "":
			state = sched.Done
		default:
			state = sched.Failed
		}
	} else if !cancelled {
		rec.Failure = lostWhy
	}
	if cancelled {
		state = sched.Cancelled
	}
	if ob.killed {
		log.For("core").Warn("queue.lost_group_killed", "job", int64(rec.ID), "pgid", ob.pid)
	}
	m.finishJob(i, state, now)
}

// applyUsage turns the pass's sample into each running job's usage: its
// group's memory now, the peak so far, and the cores used since the last
// sample. A pass whose sample failed leaves the last values.
func (m *Model) applyUsage(r queuePassResult) {
	q := m.queue
	if r.snap == nil {
		return
	}
	for _, ob := range r.obs {
		if ob.gone || ob.err != nil || ob.queued || ob.silent {
			continue
		}
		i := q.index(ob.id)
		if i < 0 {
			continue
		}
		rec := &q.store.Jobs[i]
		if rec.State != sched.Running || rec.PID <= 1 {
			continue
		}
		rss, cpu, _, _ := r.snap.Group(rec.PID)
		cores := 0.0
		if mark, ok := q.cpuMarks[ob.id]; ok {
			if dt := r.snap.Taken.Sub(mark.at); dt > 0 {
				cores = max(0, (cpu-mark.cpu).Seconds()) / dt.Seconds()
			}
		}
		q.cpuMarks[ob.id] = cpuMark{cpu: cpu, at: r.snap.Taken}
		if u := (jobUsage{RSSKB: rss, CPU: cores}); q.usage[ob.id] != u {
			q.usage[ob.id] = u
			q.moved()
		}
		if rss > rec.PeakRSSKB {
			rec.PeakRSSKB = rss
			q.moved()
		}
	}
}
```

In `core/queue.go`, retention and the removal of dropped jobs' directories. Add `slices` to the imports; replace

```go
	"path/filepath"
	"reflect"
	"runtime"
	"time"
```

with

```go
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"time"
```

replace the tail of `saveQueue`

```go
	q.saveFailed = false
	return nil
}
```

with

```go
	q.saveFailed = false
	if len(q.doomed) > 0 {
		// jobs.json no longer lists these jobs: now their directories may go.
		m.spawnBackground(removeJobDirsJob(q.dir, q.doomed))
		q.doomed = nil
	}
	return nil
}
```

and append to the end of the file

```go

// retainFinished drops the oldest finished jobs beyond keep_finished from the
// store, oldest end first (spec §5.9). Their directories are queued in doomed
// and removed once a write of the shorter jobs.json succeeds (saveQueue).
// While admission is paused the limit is unknown (the policy is zero) and
// nothing is dropped. The counter never goes back.
func (m *Model) retainFinished() {
	q := m.queue
	keep := q.policy.KeepFinished
	if keep <= 0 {
		return
	}
	var finished []int
	for i := range q.store.Jobs {
		if q.store.Jobs[i].State.Finished() {
			finished = append(finished, i)
		}
	}
	if len(finished) <= keep {
		return
	}
	slices.SortStableFunc(finished, func(a, b int) int {
		return q.store.Jobs[a].Ended.Compare(q.store.Jobs[b].Ended)
	})
	drop := map[sched.JobID]bool{}
	for _, i := range finished[:len(finished)-keep] {
		drop[q.store.Jobs[i].ID] = true
		q.doomed = append(q.doomed, q.store.Jobs[i].ID)
	}
	q.store.Jobs = slices.DeleteFunc(q.store.Jobs, func(r sched.Record) bool { return drop[r.ID] })
	q.moved()
}

// removeJobDirsJob removes the directories of dropped jobs off the loop, each
// through sched.RemoveJobDir, the one removal: it builds the path from the
// job's ID, never from a path read from a file.
func removeJobDirsJob(dir string, ids []sched.JobID) Job {
	ids = slices.Clone(ids)
	return func() any {
		for _, id := range ids {
			if err := sched.RemoveJobDir(dir, id); err != nil {
				log.For("core").Warn("queue.remove_job_dir_failed", "job", int64(id), "err", err)
			}
		}
		return nil
	}
}
```

In `core/gate.go`, a request for the watch re-dispatches it when the flight lands. Replace

```go
	case gateClaudeTmp:
		m.maybeClaudeTmpSweep()
	}
}
```

with

```go
	case gateClaudeTmp:
		m.maybeClaudeTmpSweep()
	case gateQueue:
		m.maybeQueue()
	}
}
```

In `core/tick.go`, `tickInst` watches the queue. Replace

```go
	m.maybeBuildSweep()

	// Workspace terminals whose relaunch waits on a name check tmux left
```

with

```go
	m.maybeBuildSweep()

	// The work queue's running jobs: their locks, usage and ends, on the
	// queue's own 3-second cadence (see maybeQueue). Nothing when no job
	// runs and no failed start's shim is owed a kill, or a pass is in flight.
	m.maybeQueue()

	// Workspace terminals whose relaunch waits on a name check tmux left
```

In `core/model.go`, `Deliver` applies a pass, and `Begin` looks at the jobs a restarted daemon adopted. Replace

```go
	case jobNoticeBuilt:
		m.deliverJobNotice(msg)
	case tracked:
```

with

```go
	case jobNoticeBuilt:
		m.deliverJobNotice(msg)
	case queuePassResult:
		m.deliverQueuePass(msg)
	case tracked:
```

and replace

```go
// Begin starts the model's first background jobs, each when due: an
// accounts refresh and a usage probe. Its loop runs it when it starts
// serving (Loop.Begin).
func (m *Model) Begin() {
	m.maybeAccountsRefresh()
	m.maybeUsageProbe()
}
```

with

```go
// Begin starts the model's first background jobs, each when due: an
// accounts refresh, a usage probe and a look at the work queue's running jobs
// (the shims a restarted daemon adopted). Its loop runs it when it starts
// serving (Loop.Begin).
func (m *Model) Begin() {
	m.maybeAccountsRefresh()
	m.maybeUsageProbe()
	m.maybeQueue()
}
```

- [ ] **Step 4: Run them to see them pass**

Run: `CGO_ENABLED=0 go test ./core/ -run 'TestJobQueue_|TestProductionGatedJobsYieldOneResult|TestGatedDeliveryDisarmsFirst'`

Expected: `ok  	github.com/aidan-bailey/loom/core	<time>` (the real-shim tests take a few seconds). Then `CGO_ENABLED=0 go test ./core/` prints `ok`.

- [ ] **Step 5: Commit**

```bash
git add -N core/queue_watch.go core/queue_watch_test.go
git commit -m "feat(core): gateQueue tests job locks, samples usage, settles jobs and carries out cancels" -- core/queue_watch.go core/queue_watch_test.go core/queue.go core/gate.go core/gate_test.go core/tick.go core/model.go
```

### Task 3.4: Admission, starting a job, and the operations: submit, cancel, move, notify, caps

**Files:**
- Create: `core/queue_admit.go`
- Create: `core/queue_ops.go`
- Modify: `core/queue_test.go` (imports, and append)
- Modify: `core/queue.go` (`loadQueue`)
- Modify: `core/queue_watch.go` (`afterSettle`)
- Modify: `core/tick.go:122-135`
- Modify: `core/model.go:481-490`

- [ ] **Step 1: Write the failing tests**

In `core/queue_test.go` add `slices` and `strings` to the imports. Replace

```go
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
```

with

```go
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
```

and append to the end of the file:

```go

// wqJob is a valid submission of argv from a fresh folder: the default claim,
// no owner, no notice.
func wqJob(t *testing.T, argv ...string) sched.Submit {
	t.Helper()
	return sched.Submit{
		Argv: argv, Cwd: t.TempDir(), Env: []string{"PATH=" + os.Getenv("PATH")},
		Description: "test job", Tail: 20,
	}
}

// wqSubmit submits argv and returns the job's ID.
func wqSubmit(t *testing.T, m *Model, argv ...string) sched.JobID {
	t.Helper()
	id, warning, err := m.submitJob(wqJob(t, argv...))
	require.NoError(t, err)
	require.Empty(t, warning)
	return id
}

func TestJobQueue_ASubmittedJobIsQueuedPersistedAndAdmitted(t *testing.T) {
	m := wqModel(t)
	sub := wqJob(t, "sh", "-c", "exit 0")
	sub.Env = []string{"SECRET=hunter2"}
	sub.Description = "  a build  "
	sub.Notify, sub.Tail = true, 7

	id, warning, err := m.submitJob(sub)

	require.NoError(t, err)
	assert.Empty(t, warning)
	assert.Equal(t, sched.JobID(1), id)
	rec := wqRecord(t, m, id)
	assert.Equal(t, "a build", rec.Description, "trimmed")
	assert.Equal(t, sched.Running, rec.State, "it fits, so the pass that follows the submit admits it")
	assert.Equal(t, sched.Claim{CPUs: 4, MemoryGB: 6}, rec.Claim, "the default claim, applied")
	assert.Equal(t, int64(1), rec.Order)
	assert.True(t, rec.Notify)
	assert.Equal(t, 7, rec.Tail)
	assert.Zero(t, rec.Owner)
	assert.False(t, rec.Submitted.IsZero())
	assert.True(t, m.queue.starting[id], "its start job is in flight")
	assert.Len(t, m.Drain().Jobs, 1, "and queued, to be run by the loop")

	spec, err := sched.ReadSpec(m.queue.jobDir(id))
	require.NoError(t, err)
	assert.Equal(t, sched.Spec{ID: id, Argv: sub.Argv, Cwd: sub.Cwd, Env: sub.Env}, spec,
		"the environment is in spec.json, and the CPU hint is written when the job starts")
	saved, err := sched.LoadStore(m.queue.dir)
	require.NoError(t, err)
	require.Len(t, saved.Jobs, 1)
	assert.Equal(t, sched.Running, saved.Jobs[0].State)
	assert.Equal(t, sched.JobID(2), saved.NextID)
	raw, err := os.ReadFile(filepath.Join(m.queue.dir, sched.StoreFile))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "hunter2", "jobs.json holds no environment")
}

func TestJobQueue_AClaimTakesTheDefaultsExceptWhereItIsExclusive(t *testing.T) {
	cases := []struct {
		name        string
		claim, want sched.Claim
	}{
		{"empty", sched.Claim{}, sched.Claim{CPUs: 4, MemoryGB: 6}},
		{"cores only", sched.Claim{CPUs: 2}, sched.Claim{CPUs: 2, MemoryGB: 6}},
		{"memory and io", sched.Claim{MemoryGB: 3, IO: true}, sched.Claim{CPUs: 4, MemoryGB: 3, IO: true}},
		{"exclusive", sched.Claim{Exclusive: true}, sched.Claim{Exclusive: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := wqModel(t)
			sub := wqJob(t, "true")
			sub.Claim = c.claim

			id, _, err := m.submitJob(sub)

			require.NoError(t, err)
			assert.Equal(t, c.want, wqRecord(t, m, id).Claim)
		})
	}
}

func TestJobQueue_ASubmitOutsideTheBoundsIsRefusedAndChangesNothing(t *testing.T) {
	cases := []struct {
		name string
		edit func(*sched.Submit)
		want string
	}{
		{"no description", func(s *sched.Submit) { s.Description = "  " }, "the description is empty"},
		{"no command", func(s *sched.Submit) { s.Argv = nil }, "no command to run"},
		{"a relative folder", func(s *sched.Submit) { s.Cwd = "work" }, "the folder work is not an absolute path"},
		{"a folder that is gone", func(s *sched.Submit) { s.Cwd = filepath.Join(s.Cwd, "gone") }, "the folder %CWD% does not exist"},
		{"a negative tail", func(s *sched.Submit) { s.Tail = -1 }, "--tail is -1: it must be 0–200"},
		{"too many cores", func(s *sched.Submit) { s.Claim.CPUs = 99 }, "claims 99 cores; this host's cap is 14"},
		{"too much memory", func(s *sched.Submit) { s.Claim.MemoryGB = 99 }, "claims 99 GB; this host's cap is 24 GB"},
		{"exclusive with cores", func(s *sched.Submit) { s.Claim = sched.Claim{Exclusive: true, CPUs: 2} },
			"--exclusive claims everything: drop --cpus, --mem and --io"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := wqModel(t)
			sub := wqJob(t, "true")
			c.edit(&sub)

			id, _, err := m.submitJob(sub)

			assert.EqualError(t, err, strings.ReplaceAll(c.want, "%CWD%", sub.Cwd))
			assert.Zero(t, id)
			assert.Empty(t, m.queue.store.Jobs)
			assert.Equal(t, sched.JobID(1), m.queue.store.NextID)
			assert.NoDirExists(t, m.queue.dir, "nothing was written")
			assert.Empty(t, m.Drain().Jobs)
		})
	}
}

func TestJobQueue_AJobsOwnerIsTheSessionHoldingItsLaunch(t *testing.T) {
	m := wqModel(t)
	inst := modInst(t, m, "owner") // launch L1
	cases := []struct {
		name, launch, warning string
		owner                 uint64
	}{
		{"a served launch", "L1", "", uint64(m.idOf(inst))},
		{"a plain shell", "", "", 0},
		{"a launch nobody holds", "L9",
			"this shell's loom session (launch L9) isn't one the daemon serves: job 3 has no owner, so no message will come", 0},
	}
	for i, c := range cases {
		sub := wqJob(t, "true")
		sub.Launch = c.launch

		id, warning, err := m.submitJob(sub)

		require.NoError(t, err, c.name)
		assert.Equal(t, sched.JobID(i+1), id)
		assert.Equal(t, c.warning, warning, c.name)
		assert.Equal(t, c.owner, wqRecord(t, m, id).Owner, c.name)
	}
}

func TestJobQueue_AdmissionFillsTheCapsAndTheRestWait(t *testing.T) {
	m := wqModel(t)
	var ids []sched.JobID
	for range 4 {
		ids = append(ids, wqSubmit(t, m, "sleep", "30"))
	}
	for _, id := range ids[:3] {
		assert.Equal(t, sched.Running, wqRecord(t, m, id).State)
	}
	assert.Equal(t, sched.Queued, wqRecord(t, m, ids[3]).State)
	assert.Equal(t, "needs 4 cores (2 free)", m.queue.waiting[ids[3]])
	assert.Len(t, m.Drain().Jobs, 3, "three starts, which this test never runs")

	// A job ends: the pass that follows starts the fourth.
	m.finishJob(m.queue.index(ids[0]), sched.Done, time.Now())
	m.afterSettle()

	assert.Equal(t, sched.Running, wqRecord(t, m, ids[3]).State)
	assert.NotContains(t, m.queue.waiting, ids[3])
	assert.Len(t, m.Drain().Jobs, 1)
}

// The tick runs an admission pass while jobs wait, which picks up a hand edit
// of the scheduler block, at most once a queue interval.
func TestJobQueue_TheTickRereadsTheCapsWhileJobsWait(t *testing.T) {
	m := wqModel(t)
	loaded := m.queue.lastPass
	m.queue.lastPass = loaded.Add(-time.Hour)
	m.tickQueue()
	assert.Equal(t, loaded.Add(-time.Hour), m.queue.lastPass, "nothing waits, so no pass")
	wqConfig(t, m, `{"cpus": 4}`) // one default job at a time
	wqSubmit(t, m, "true")
	waiting := wqSubmit(t, m, "true")
	require.Equal(t, sched.Queued, wqRecord(t, m, waiting).State)
	assert.Equal(t, "needs 4 cores (0 free)", m.queue.waiting[waiting])

	wqConfig(t, m, `{"cpus": 8}`) // edited by hand
	m.tickQueue()
	assert.Equal(t, sched.Queued, wqRecord(t, m, waiting).State, "a pass ran a moment ago")

	m.queue.lastPass = time.Now().Add(-queueInterval)
	m.tickQueue()
	assert.Equal(t, sched.Running, wqRecord(t, m, waiting).State)
}

func TestJobQueue_AnInvalidSchedulerBlockPausesAdmissionAndSaysWhy(t *testing.T) {
	m := wqModel(t)
	wqConfig(t, m, `{"cpus": 4}`)
	running := wqSubmit(t, m, "true")
	waiting := wqSubmit(t, m, "true")
	reason := "scheduler.cpus is 5000: it must be 1–1024"

	wqConfig(t, m, `{"cpus": 5000}`)
	m.admitJobs()

	assert.Equal(t, reason, m.queue.paused)
	assert.Equal(t, sched.Policy{}, m.queue.policy)
	assert.Equal(t, "admission paused: "+reason, m.queue.waiting[waiting])
	assert.Equal(t, sched.Running, wqRecord(t, m, running).State, "running jobs are left alone")
	_, _, err := m.submitJob(wqJob(t, "true"))
	assert.EqualError(t, err, "admission paused: "+reason)

	wqConfig(t, m, `{"cpus": 8}`)
	m.admitJobs()

	assert.Empty(t, m.queue.paused)
	assert.Equal(t, sched.Running, wqRecord(t, m, waiting).State, "the fixed block admits what waited")
}

// On Windows the refusal comes before every other check: a submission that is
// also invalid still hears that the queue isn't available, and so does one made
// before the queue loaded.
func TestJobQueue_AWindowsDaemonRefusesEverySubmission(t *testing.T) {
	was := queueGOOS
	queueGOOS = "windows"
	t.Cleanup(func() { queueGOOS = was })

	t.Run("a queue booted there", func(t *testing.T) {
		m := wqModel(t)

		_, _, err := m.submitJob(wqJob(t, "true"))
		assert.ErrorIs(t, err, sched.ErrUnsupported)
		assert.EqualError(t, err, "the work queue isn't available on Windows")

		_, _, err = m.submitJob(sched.Submit{})
		assert.ErrorIs(t, err, sched.ErrUnsupported, "before the submission is even checked")
		assert.Empty(t, m.queue.store.Jobs)
		assert.NoDirExists(t, m.queue.jobDir(1))
	})
	t.Run("a queue not loaded", func(t *testing.T) {
		m := NewForTest(Options{})

		_, _, err := m.submitJob(wqJob(t, "true"))

		assert.ErrorIs(t, err, sched.ErrUnsupported)
	})
	t.Run("an unsupported runner elsewhere refuses the same way", func(t *testing.T) {
		queueGOOS = was
		m := wqModel(t)
		m.queue.runnerErr = sched.ErrUnsupported

		_, _, err := m.submitJob(wqJob(t, "true"))

		assert.ErrorIs(t, err, sched.ErrUnsupported)
	})
	t.Run("another failure of NewRunner pauses admission and refuses with its reason", func(t *testing.T) {
		queueGOOS = was
		m := wqModel(t)
		m.queue.runnerErr = errors.New("no executable")

		_, _, err := m.submitJob(wqJob(t, "true"))

		assert.EqualError(t, err, "admission paused: no executable")
	})
}

// A failed write refuses a request and changes nothing (spec §5.9).
func TestJobQueue_ASubmitWhoseWriteFailsIsRefusedAndTakenBack(t *testing.T) {
	m := wqModel(t)
	unblock := wqBlockJobsJSON(t, m)

	_, _, err := m.submitJob(wqJob(t, "true"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "write jobs.json")
	assert.Empty(t, m.queue.store.Jobs)
	assert.Equal(t, sched.JobID(1), m.queue.store.NextID)
	assert.NoDirExists(t, m.queue.jobDir(1), "its spec was taken back")
	assert.Empty(t, m.Drain().Jobs, "and nothing was started")

	unblock()
	assert.Equal(t, sched.JobID(1), wqSubmit(t, m, "true"), "the ID was not spent")
}

func TestJobQueue_ARequestWhoseWriteFailsChangesNothing(t *testing.T) {
	m := wqModel(t)
	wqConfig(t, m, `{"cpus": 4}`) // one default job at a time
	running := wqSubmit(t, m, "true")
	waiting := wqSubmit(t, m, "true")
	last := wqSubmit(t, m, "true")
	m.queue.store.Jobs[m.queue.index(waiting)].Owner = 77
	m.Drain()
	unblock := wqBlockJobsJSON(t, m)
	defer unblock()
	before := slices.Clone(m.queue.store.Jobs)

	assert.Error(t, m.cancelJob(waiting, "from the work view"))
	assert.Error(t, m.cancelJob(running, "from the work view"))
	assert.Error(t, m.moveJobToFront(last))
	assert.Error(t, m.notifyJob(waiting))

	assert.Equal(t, before, m.queue.store.Jobs, "nothing changed")
}

func TestJobQueue_CancelEndsAQueuedJobAtOnceAndRecordsARunningJobsRequest(t *testing.T) {
	m := wqModel(t)
	var ids []sched.JobID
	for range 4 {
		ids = append(ids, wqSubmit(t, m, "sleep", "30"))
	}
	m.Drain() // the three start jobs are never run here
	running, queued := ids[0], ids[3]

	require.NoError(t, m.cancelJob(queued, "from the work view"))

	rec := wqRecord(t, m, queued)
	assert.Equal(t, sched.Cancelled, rec.State)
	assert.Equal(t, "from the work view", rec.CancelReason)
	assert.False(t, rec.Ended.IsZero())
	assert.NotContains(t, m.queue.waiting, queued)
	saved, err := sched.LoadStore(m.queue.dir)
	require.NoError(t, err)
	assert.Equal(t, sched.Cancelled, saved.Jobs[3].State, "written")

	require.NoError(t, m.cancelJob(running, "with loom job cancel"))
	assert.Equal(t, sched.Running, wqRecord(t, m, running).State, "gateQueue ends a running job")
	assert.Equal(t, "with loom job cancel", wqRecord(t, m, running).CancelReason)
	require.NoError(t, m.cancelJob(running, "from the work view"), "asked again")
	assert.Equal(t, "with loom job cancel", wqRecord(t, m, running).CancelReason, "the first request stands")

	assert.EqualError(t, m.cancelJob(queued, "x"), "job 4 has finished")
	assert.EqualError(t, m.cancelJob(99, "x"), "there is no job 99")
}

// The whole path: a running job is cancelled, its group gets SIGTERM, it
// settles, and the pass that follows starts the job that waited on its claim.
func TestJobQueue_CancellingARunningJobFreesItsClaimForTheNextWaitingOne(t *testing.T) {
	m := wqLiveModel(t)
	wqConfig(t, m, `{"cpus": 4}`) // one default job at a time
	long := wqSubmit(t, m, "sleep", "30")
	next := wqSubmit(t, m, "sh", "-c", "echo next")
	wqPump(t, m) // the first one's start: a real shim
	require.Equal(t, sched.Queued, wqRecord(t, m, next).State)
	assert.Equal(t, "needs 4 cores (0 free)", m.queue.waiting[next])

	require.NoError(t, m.cancelJob(long, "from the work view"))
	rec := wqSettle(t, m, long)

	assert.Equal(t, sched.Cancelled, rec.State)
	require.NotNil(t, rec.Exit)
	assert.Equal(t, "SIGTERM", rec.Exit.Signal)
	assert.Equal(t, sched.Done, wqSettle(t, m, next).State, "its claim freed, the waiting job ran")
}

func TestJobQueue_MoveToFrontPutsAJobAheadOfTheQueue(t *testing.T) {
	m := wqModel(t)
	wqConfig(t, m, `{"cpus": 4}`) // one default job at a time
	first := wqSubmit(t, m, "true")
	second := wqSubmit(t, m, "true")
	third := wqSubmit(t, m, "true")
	require.Equal(t, sched.Queued, wqRecord(t, m, third).State)

	require.NoError(t, m.moveJobToFront(third))

	assert.Equal(t, int64(1), wqRecord(t, m, third).Order, "one below the lowest queued order, the second job's 2")
	m.finishJob(m.queue.index(first), sched.Done, time.Now())
	m.afterSettle()
	assert.Equal(t, sched.Running, wqRecord(t, m, third).State, "the pass that follows starts the moved job first")
	assert.Equal(t, sched.Queued, wqRecord(t, m, second).State)

	assert.EqualError(t, m.moveJobToFront(third), "job 3 isn't waiting", "it runs now")
	assert.EqualError(t, m.moveJobToFront(first), "job 1 isn't waiting", "it has finished")
	assert.EqualError(t, m.moveJobToFront(99), "there is no job 99")
}

func TestJobQueue_NotifyJobSetsTheFlagWhereAMessageCanComeBack(t *testing.T) {
	m := wqModel(t)
	queued := func(id sched.JobID, owner InstanceID) sched.Record {
		r := wqEnded(id, owner)
		r.State, r.Exit, r.Ended, r.Started, r.Notify = sched.Queued, nil, time.Time{}, time.Time{}, false
		return r
	}
	wqSeed(t, m, queued(1, 77))
	wqSeed(t, m, queued(2, 0))
	wqSeed(t, m, wqEnded(3, 77))

	require.NoError(t, m.notifyJob(1))

	assert.True(t, wqRecord(t, m, 1).Notify)
	saved, err := sched.LoadStore(m.queue.dir)
	require.NoError(t, err)
	assert.True(t, saved.Jobs[0].Notify, "written")
	assert.EqualError(t, m.notifyJob(2), "job 2 has no owner")
	assert.EqualError(t, m.notifyJob(3), "job 3 has finished")
	assert.EqualError(t, m.notifyJob(99), "there is no job 99")
}

func TestJobQueue_SetSchedulerCapsWritesTheBlockAndAppliesItAtOnce(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, config.ConfigFileName)
	require.NoError(t, os.WriteFile(cfgPath, []byte(`{"default_program": "my-claude", "scheduler": {"cpus": 4}}`), 0o644))
	m := wqModelAt(t, dir)
	global := NewWorkspace(WorkspaceParts{Ctx: &config.WorkspaceContext{ConfigDir: dir}, Config: config.LoadConfigFrom(dir)})
	m.SetWorkspacesForTest(global)
	wqSubmit(t, m, "true")
	second := wqSubmit(t, m, "true")
	require.Equal(t, sched.Queued, wqRecord(t, m, second).State, "four cores hold one default job")

	require.NoError(t, m.setSchedulerCaps(config.SchedulerSettings{CPUs: 8, MemoryGB: 12}))

	assert.Equal(t, sched.Running, wqRecord(t, m, second).State, "a caps edit applies at once")
	written := config.LoadConfigFrom(dir)
	assert.Equal(t, config.SchedulerSettings{CPUs: 8, MemoryGB: 12}, written.GetScheduler())
	assert.Equal(t, "my-claude", written.DefaultProgram, "the rest of config.json is kept")
	assert.Equal(t, 8, global.cfg.GetScheduler().CPUs, "and so is the global workspace's own copy")

	before, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	err = m.setSchedulerCaps(config.SchedulerSettings{CPUs: 5000})
	assert.EqualError(t, err, "scheduler.cpus is 5000: it must be 1–1024")
	after, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "a refused edit writes nothing")
}

// The whole path for one job: submit, start (a real shim and its handshake),
// the CPU claim in spec.json, watch and settle.
func TestJobQueue_AStartedJobRecordsItsShimAndGetsItsCPUHint(t *testing.T) {
	cases := []struct {
		name  string
		claim sched.Claim
		hint  int
	}{
		{"the default claim", sched.Claim{}, 4},
		{"its own cores", sched.Claim{CPUs: 3}, 3},
		{"exclusive: the cpu cap", sched.Claim{Exclusive: true}, 14},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := wqLiveModel(t)
			sub := wqJob(t, "sh", "-c", "echo built")
			sub.Claim = c.claim
			id, _, err := m.submitJob(sub)
			require.NoError(t, err)

			wqPump(t, m)

			rec := wqRecord(t, m, id)
			assert.Greater(t, rec.PID, 1)
			assert.False(t, rec.Started.IsZero())
			assert.NotContains(t, m.queue.starting, id)
			spec, err := sched.ReadSpec(m.queue.jobDir(id))
			require.NoError(t, err)
			assert.Equal(t, c.hint, spec.HintCPUs)

			assert.Equal(t, sched.Done, wqSettle(t, m, id).State)
			out, err := os.ReadFile(filepath.Join(m.queue.jobDir(id), sched.LogFile))
			require.NoError(t, err)
			assert.Equal(t, "built\n", string(out))
		})
	}
}

func TestJobQueue_AStartThatCannotWorkFailsTheJob(t *testing.T) {
	t.Run("its folder is gone", func(t *testing.T) {
		m := wqLiveModel(t)
		sub := wqJob(t, "true")
		sub.Cwd = filepath.Join(t.TempDir(), "work")
		require.NoError(t, os.Mkdir(sub.Cwd, 0o755))
		id, _, err := m.submitJob(sub)
		require.NoError(t, err)
		require.NoError(t, os.Remove(sub.Cwd))

		wqPump(t, m)

		rec := wqRecord(t, m, id)
		assert.Equal(t, sched.Failed, rec.State)
		assert.Equal(t, "its folder is gone", rec.Failure)
		assert.False(t, rec.Ended.IsZero())
		assert.Zero(t, rec.PID, "nothing started")
	})
	t.Run("the runner writes no started.json", func(t *testing.T) {
		m := wqLiveModel(t)
		m.SetJobRunnerForTest(sched.Runner{Shim: []string{"true"}})
		m.queue.handshake = 200 * time.Millisecond
		id := wqSubmit(t, m, "true")

		wqPump(t, m)

		rec := wqRecord(t, m, id)
		assert.Equal(t, sched.Failed, rec.State)
		assert.Equal(t, "the job runner did not start", rec.Failure)
		assert.NotContains(t, m.queue.starting, id)
		assert.Contains(t, m.queue.silent, id, "a free lock does not prove the shim gone: its group stays owed a SIGKILL")
	})
	t.Run("a cancel asked for meanwhile wins", func(t *testing.T) {
		m := wqLiveModel(t)
		m.SetJobRunnerForTest(sched.Runner{Shim: []string{"true"}})
		m.queue.handshake = 200 * time.Millisecond
		id := wqSubmit(t, m, "true")
		require.NoError(t, m.cancelJob(id, "with loom job cancel"))

		wqPump(t, m)

		assert.Equal(t, sched.Cancelled, wqRecord(t, m, id).State)
	})
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `CGO_ENABLED=0 go test ./core/ -run 'TestJobQueue_'`

Expected: the package does not compile: `m.submitJob undefined`, `m.cancelJob undefined`, `m.moveJobToFront undefined`, `m.notifyJob undefined`, `m.setSchedulerCaps undefined`, `m.admitJobs undefined`, `m.tickQueue undefined`.

- [ ] **Step 3: Implement**

Create `core/queue_admit.go`:

```go
package core

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"syscall"
	"time"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/sched"
)

const (
	// defaultHandshake is how long a started shim has to write started.json
	// before the start counts as failed (spec §5.3).
	defaultHandshake = 5 * time.Second
	// startPoll is how often a start job looks for started.json.
	startPoll = 20 * time.Millisecond
)

var (
	// errFolderGone fails a job whose folder was removed while it waited.
	errFolderGone = errors.New("its folder is gone")
	// errRunnerSilent fails a job whose shim wrote no started.json in time.
	errRunnerSilent = errors.New("the job runner did not start")
)

// handshakeWait is the wait for started.json.
func (q *queueState) handshakeWait() time.Duration {
	if q.handshake > 0 {
		return q.handshake
	}
	return defaultHandshake
}

// hasQueued reports whether a job waits.
func (q *queueState) hasQueued() bool {
	for i := range q.store.Jobs {
		if q.store.Jobs[i].State == sched.Queued {
			return true
		}
	}
	return false
}

// admitJobs runs an admission pass (spec §5.2). It rereads the scheduler
// block, so a hand edit applies without a restart, asks sched.Admit which
// queued jobs to start and why each of the others waits, and starts the first
// kind. While admission is paused (an invalid block, a jobs.json that failed
// to load) it starts nothing and says why on every queued job.
func (m *Model) admitJobs() {
	q := m.queue
	if !q.ready() {
		return
	}
	now := time.Now()
	q.lastPass = now
	policy, paused := m.refreshPolicy()
	waiting := map[sched.JobID]string{}
	var start []sched.JobID
	if paused != "" {
		for i := range q.store.Jobs {
			if q.store.Jobs[i].State == sched.Queued {
				waiting[q.store.Jobs[i].ID] = "admission paused: " + paused
			}
		}
	} else {
		dec := sched.Admit(q.store.Jobs, policy, now)
		maps.Copy(waiting, dec.Waiting)
		start = dec.Start
	}
	if !maps.Equal(waiting, q.waiting) {
		q.moved()
	}
	q.waiting = waiting
	started := false
	for _, id := range start {
		if i := q.index(id); i >= 0 && q.store.Jobs[i].State == sched.Queued {
			m.startJob(i, policy, now)
			started = true
		}
	}
	if started {
		q.moved()
		m.saveQueueQuiet("start")
	}
}

// tickQueue runs an admission pass from the health tick while jobs wait, at
// most once a queueInterval: a hand edit of the scheduler block applies
// within seconds.
func (m *Model) tickQueue() {
	q := m.queue
	if !q.ready() || !q.hasQueued() || time.Since(q.lastPass) < queueInterval {
		return
	}
	m.admitJobs()
}

// jobStart is a start job's input: copies only.
type jobStart struct {
	dir       string
	id        sched.JobID
	cwd       string
	hint      int // the CPU claim, which becomes the job's parallelism
	runner    sched.Runner
	handshake time.Duration
}

// jobStarted is a start job's result: the shim's pid and start time, or why
// the job failed to start.
type jobStarted struct {
	id      sched.JobID
	pid     int
	started time.Time
	err     error
	// owed is the pid of a shim the start gave up on (errRunnerSilent) without
	// proof that it was alive, so its group got no SIGKILL: the model keeps it
	// owed one (queueState.silent). 0 when nothing is owed.
	owed int
}

// startJob admits job i of the store: it is running from now (its claim is
// taken by the next pass), and a model job starts its shim (spec §5.3).
func (m *Model) startJob(i int, policy sched.Policy, now time.Time) {
	q := m.queue
	rec := &q.store.Jobs[i]
	rec.State, rec.Started, rec.PID = sched.Running, now, 0
	q.starting[rec.ID] = true
	delete(q.waiting, rec.ID)
	m.spawnBackground(runJobStart(jobStart{
		dir: q.jobDir(rec.ID), id: rec.ID, cwd: rec.Cwd,
		hint:      rec.Claim.Need(policy.Caps).CPUs,
		runner:    q.runner,
		handshake: q.handshakeWait(),
	}))
}

// runJobStart is the start job: it fails a job whose folder is gone, writes
// the CPU hint into spec.json, starts the shim and waits for started.json. A
// shim that wrote nothing in time has its group killed, but only if its lock
// proves it alive (signalAlive). A free lock does not prove the shim gone, since
// a slowed shim may take it later and run the command while its job reads
// failed, so the result hands the shim's pid back (owed) for gateQueue to keep
// testing.
func runJobStart(s jobStart) Job {
	return func() any {
		res := jobStarted{id: s.id}
		if _, err := os.Stat(s.cwd); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				res.err = errFolderGone
			} else {
				res.err = fmt.Errorf("its folder can't be read: %w", err)
			}
			return res
		}
		spec, err := sched.ReadSpec(s.dir)
		if err != nil {
			res.err = fmt.Errorf("its spec could not be read: %w", err)
			return res
		}
		spec.HintCPUs = s.hint
		if err := sched.WriteSpec(s.dir, spec); err != nil {
			res.err = fmt.Errorf("its spec could not be written: %w", err)
			return res
		}
		pid, err := s.runner.Start(s.dir)
		if err != nil {
			res.err = fmt.Errorf("the job runner could not be started: %w", err)
			return res
		}
		deadline := time.Now().Add(s.handshake)
		for {
			if st, err := sched.ReadStarted(s.dir); err == nil {
				res.pid, res.started = st.PID, st.Started
				return res
			}
			if !time.Now().Before(deadline) {
				break
			}
			time.Sleep(startPoll)
		}
		sent, err := signalAlive(s.dir, pid, syscall.SIGKILL)
		if err != nil {
			log.For("core").Warn("queue.start_kill_failed", "job", int64(s.id), "err", err)
		}
		if !sent && pid > 1 {
			res.owed = pid
		}
		res.err = errRunnerSilent
		return res
	}
}

// deliverJobStarted records a start. A failed one ends the job: failed, or
// cancelled when a cancel was asked for meanwhile; the shim of one that timed
// out unproven stays owed a SIGKILL (queueState.silent), which the tick's
// next pass takes up. A good one records the
// shim's pid and start time, and a cancel asked for meanwhile is carried out
// by the pass this makes due.
func (m *Model) deliverJobStarted(r jobStarted) {
	q := m.queue
	delete(q.starting, r.id)
	if r.owed > 1 {
		// Whatever became of the record, the shim may still come up.
		q.silent[r.id] = silentShim{pid: r.owed, since: time.Now()}
	}
	i := q.index(r.id)
	if i < 0 || q.store.Jobs[i].State != sched.Running {
		return
	}
	rec := &q.store.Jobs[i]
	if r.err != nil {
		rec.Failure = r.err.Error()
		state := sched.Failed
		if rec.CancelReason != "" {
			state = sched.Cancelled
		}
		m.finishJob(i, state, time.Now())
		m.afterSettle()
		return
	}
	rec.PID, rec.Started = r.pid, r.started
	q.moved()
	m.saveQueueQuiet("start")
	if rec.CancelReason != "" {
		m.gate(gateQueue).request()
	}
	m.maybeQueue()
}
```

Create `core/queue_ops.go`:

```go
package core

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/sched"
)

// errQueueNotLoaded refuses a request made where the queue has no directory:
// before Boot, or where the global config dir could not be resolved.
var errQueueNotLoaded = errors.New("the work queue isn't loaded")

// jobOwner resolves a submitter's $LOOM_LAUNCH to the session whose current
// launch it is (spec §6.2): that session's ID, or 0 for a plain shell (no
// launch) and for a launch no served session holds, which is also reported
// (unknown) so the caller can warn.
func (m *Model) jobOwner(launch string) (owner InstanceID, unknown bool) {
	if launch == "" {
		return 0, false
	}
	inst := m.instByLaunch(launch)
	if inst == nil {
		return 0, true
	}
	return m.idOf(inst), false
}

// submitJob queues a job (spec §6.1, §5.9): it refuses first, on Windows, with
// sched.ErrUnsupported; then it checks the submission against the bounds and
// the caps, applies the default claim, writes spec.json and
// jobs.json, and runs an admission pass. It replies only once both files are
// written; a failed write refuses the request with the error and changes
// nothing. warning is the unknown launch's text.
func (m *Model) submitJob(s sched.Submit) (id sched.JobID, warning string, err error) {
	q := m.queue
	switch {
	case queueGOOS == "windows" || (q != nil && errors.Is(q.runnerErr, sched.ErrUnsupported)):
		// First of all: no shim runs there, so nothing else is worth saying.
		return 0, "", sched.ErrUnsupported
	case !q.ready():
		return 0, "", errQueueNotLoaded
	}
	if err := s.Validate(); err != nil {
		return 0, "", err
	}
	if _, err := os.Stat(s.Cwd); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, "", fmt.Errorf("the folder %s does not exist", s.Cwd)
		}
		return 0, "", fmt.Errorf("the folder %s can't be read: %w", s.Cwd, err)
	}
	policy, paused := m.refreshPolicy()
	if paused != "" {
		return 0, "", fmt.Errorf("admission paused: %s", paused)
	}
	claim := s.Claim
	if !claim.Exclusive {
		if claim.CPUs == 0 {
			claim.CPUs = policy.DefaultClaim.CPUs
		}
		if claim.MemoryGB == 0 {
			claim.MemoryGB = policy.DefaultClaim.MemoryGB
		}
	}
	if err := sched.CheckClaim(claim, policy); err != nil {
		return 0, "", err
	}

	owner, unknown := m.jobOwner(s.Launch)
	id = max(q.store.NextID, 1)
	rec := sched.Record{
		ID: id, Order: int64(id), Owner: uint64(owner), Description: strings.TrimSpace(s.Description),
		Argv: slices.Clone(s.Argv), Cwd: s.Cwd, Claim: claim, Notify: s.Notify, Tail: s.Tail,
		State: sched.Queued, Submitted: time.Now(),
	}
	prev := q.store
	if err := sched.WriteSpec(q.jobDir(id), sched.Spec{ID: id, Argv: rec.Argv, Cwd: s.Cwd, Env: slices.Clone(s.Env)}); err != nil {
		_ = sched.RemoveJobDir(q.dir, id)
		return 0, "", fmt.Errorf("write the job's spec: %w", err)
	}
	q.store.NextID = id + 1
	q.store.Jobs = append(q.store.Jobs, rec)
	if err := m.saveQueue(); err != nil {
		q.store = prev
		_ = sched.RemoveJobDir(q.dir, id)
		return 0, "", err
	}
	q.moved()
	if unknown {
		warning = fmt.Sprintf("this shell's loom session (launch %s) isn't one the daemon serves: job %d has no owner, so no message will come", s.Launch, id)
	}
	m.admitJobs()
	return id, warning, nil
}

// cancelJob cancels job id (spec §5.5) for reason, which the notice and the
// follower's line give after "was cancelled": a queued job is cancelled at
// once; a running job's request is recorded and gateQueue carries it out
// (SIGTERM to its group, SIGKILL if the lock is still held 10 s later). A
// finished job is refused, and a failed write refuses the request.
func (m *Model) cancelJob(id sched.JobID, reason string) error {
	q := m.queue
	if !q.ready() {
		return errQueueNotLoaded
	}
	i := q.index(id)
	switch {
	case i < 0:
		return fmt.Errorf("there is no job %d", id)
	case q.store.Jobs[i].State.Finished():
		return fmt.Errorf("job %d has finished", id)
	}
	rec := &q.store.Jobs[i]
	prev := *rec
	queued := rec.State == sched.Queued
	switch {
	case queued:
		rec.CancelReason = reason
		endJob(rec, sched.Cancelled, time.Now())
	case rec.CancelReason != "":
		return nil // asked for already: the first request stands
	default:
		rec.CancelReason = reason
	}
	if err := m.saveQueue(); err != nil {
		*rec = prev
		return err
	}
	q.moved()
	if queued {
		m.settledJob(i)
		m.afterSettle()
		return nil
	}
	m.gate(gateQueue).request()
	m.maybeQueue()
	return nil
}

// moveJobToFront puts queued job id ahead of every other queued job: its order
// becomes one below the lowest queued order. Anything but a queued job is
// refused.
func (m *Model) moveJobToFront(id sched.JobID) error {
	q := m.queue
	if !q.ready() {
		return errQueueNotLoaded
	}
	i := q.index(id)
	switch {
	case i < 0:
		return fmt.Errorf("there is no job %d", id)
	case q.store.Jobs[i].State != sched.Queued:
		return fmt.Errorf("job %d isn't waiting", id)
	}
	prev := q.store.Jobs[i].Order
	lowest := prev
	for _, rec := range q.store.Jobs {
		if rec.State == sched.Queued && rec.Order < lowest {
			lowest = rec.Order
		}
	}
	q.store.Jobs[i].Order = lowest - 1
	if err := m.saveQueue(); err != nil {
		q.store.Jobs[i].Order = prev
		return err
	}
	q.moved()
	m.admitJobs()
	return nil
}

// notifyJob sets the notify flag of job id, which has not ended and has an
// owner: a message will come when it ends (what an interrupted `loom job run`
// asks for).
func (m *Model) notifyJob(id sched.JobID) error {
	q := m.queue
	if !q.ready() {
		return errQueueNotLoaded
	}
	i := q.index(id)
	switch {
	case i < 0:
		return fmt.Errorf("there is no job %d", id)
	case q.store.Jobs[i].State.Finished():
		return fmt.Errorf("job %d has finished", id)
	case q.store.Jobs[i].Owner == 0:
		return fmt.Errorf("job %d has no owner", id)
	case q.store.Jobs[i].Notify:
		return nil
	}
	q.store.Jobs[i].Notify = true
	if err := m.saveQueue(); err != nil {
		q.store.Jobs[i].Notify = false
		return err
	}
	q.moved()
	return nil
}

// setSchedulerCaps replaces the scheduler block of the global config.json with
// s (a field left 0 takes the host's default), after resolving it against the
// host: an invalid block is refused, with sched.Resolve's text, and writes
// nothing. The rest of config.json is kept, and the global workspace's own
// copy follows. An admission pass applies it at once.
func (m *Model) setSchedulerCaps(s config.SchedulerSettings) error {
	q := m.queue
	if !q.ready() {
		return errQueueNotLoaded
	}
	if _, err := sched.Resolve(s, q.host); err != nil {
		return err
	}
	cfg := config.LoadConfigFrom(q.cfgDir)
	cfg.SetScheduler(s)
	if err := config.SaveConfigTo(cfg, q.cfgDir); err != nil {
		return fmt.Errorf("write the caps to config.json: %w", err)
	}
	if ws := m.wsByConfigDir(q.cfgDir); ws != nil && ws.cfg != nil {
		ws.cfg.SetScheduler(s)
	}
	m.admitJobs()
	return nil
}

// cancelOwnerJobs cancels every job of owner that has not ended, for reason
// ("because the session was paused" or "…killed"): the queued ones at once, the
// running ones by a request gateQueue carries out. Pause and Kill call it
// before they act, and neither waits for the jobs to exit. The owner is sent
// no notice for them (the flag is cleared), and the jobs of other owners and
// of none are not touched.
func (m *Model) cancelOwnerJobs(owner InstanceID, reason string) {
	q := m.queue
	if q == nil || !q.ready() || owner == 0 {
		return
	}
	now := time.Now()
	found, settled := false, false
	for i := range q.store.Jobs {
		rec := &q.store.Jobs[i]
		if rec.Owner != uint64(owner) || rec.State.Finished() {
			continue
		}
		found = true
		rec.Notify = false
		switch {
		case rec.State == sched.Queued:
			rec.CancelReason = reason
			endJob(rec, sched.Cancelled, now)
			m.settledJob(i)
			settled = true
		case rec.CancelReason == "":
			rec.CancelReason = reason
		}
	}
	if !found {
		return
	}
	q.moved()
	if settled {
		m.afterSettle()
	} else {
		m.saveQueueQuiet("cancel")
	}
	m.gate(gateQueue).request()
	m.maybeQueue()
}
```

In `core/queue.go`, `loadQueue` admits once the queue is loaded. Replace

```go
	q.store = store
	q.moved()
	m.refreshPolicy()
}
```

with

```go
	q.store = store
	q.moved()
	m.admitJobs()
}
```

In `core/queue_watch.go`, a job's end frees its claim, so the pass follows. Replace

```go
// afterSettle runs after one or more jobs ended: retention trims the finished
// jobs, and the write that follows is quiet (spec §5.9).
func (m *Model) afterSettle() {
	m.retainFinished()
	m.saveQueueQuiet("settle")
}
```

with

```go
// afterSettle runs after one or more jobs ended: their claims are free, so an
// admission pass follows; retention trims the finished jobs; and the write
// that follows is quiet (spec §5.9).
func (m *Model) afterSettle() {
	m.admitJobs()
	m.retainFinished()
	m.saveQueueQuiet("settle")
}
```

In `core/tick.go`, the tick runs the admission pass. Replace

```go
	m.maybeQueue()

	// Workspace terminals whose relaunch waits on a name check tmux left
```

with

```go
	m.maybeQueue()

	// An admission pass while jobs wait, at most every queue interval (see
	// tickQueue): it picks up a hand edit of the scheduler block.
	m.tickQueue()

	// Workspace terminals whose relaunch waits on a name check tmux left
```

In `core/model.go`, `Deliver` records a start. Replace

```go
	case queuePassResult:
		m.deliverQueuePass(msg)
	case tracked:
```

with

```go
	case queuePassResult:
		m.deliverQueuePass(msg)
	case jobStarted:
		m.deliverJobStarted(msg)
	case tracked:
```

- [ ] **Step 4: Run them to see them pass**

Run: `CGO_ENABLED=0 go test ./core/ -run 'TestJobQueue_'`

Expected: `ok  	github.com/aidan-bailey/loom/core	<time>`. Then `CGO_ENABLED=0 go test ./core/` prints `ok`.

- [ ] **Step 5: Commit**

```bash
git add -N core/queue_admit.go core/queue_ops.go
git commit -m "feat(core): admission passes, starting a job, and submit, cancel, move, notify and caps on the queue" -- core/queue_admit.go core/queue_ops.go core/queue_test.go core/queue.go core/queue_watch.go core/tick.go core/model.go
```

### Task 3.5: Boot adopts and settles what ran while the daemon was down

**Files:**
- Modify: `core/queue_test.go` (imports, and append)
- Modify: `core/queue.go` (`loadQueue`, and the end of the file)

- [ ] **Step 1: Write the failing tests**

In `core/queue_test.go` add `fmt` to the imports. Replace

```go
import (
	"errors"
	"os"
```

with

```go
import (
	"errors"
	"fmt"
	"os"
```

and append to the end of the file:

```go

// wqRestart is a daemon restarted over dir: a second model over the same
// global dir, which settles every job it still finds running when the test
// ends.
func wqRestart(t *testing.T, dir string) *Model {
	t.Helper()
	m := wqModelAt(t, dir)
	t.Cleanup(func() { wqStop(t, m) })
	return m
}

// wqShimDone waits until job id's shim has written its result and let go of
// its lock.
func wqShimDone(t *testing.T, m *Model, id sched.JobID) {
	t.Helper()
	dir := m.queue.jobDir(id)
	wqWait(t, "the shim to finish", func() bool {
		if _, err := sched.ReadExit(dir); err != nil {
			return false
		}
		alive, _ := sched.Probe(dir)
		return !alive
	})
}

// wqGated is a command that runs until the file gate exists, then exits with
// code: a job a test can keep running across a restart and end when it likes.
func wqGated(gate string, code int) []string {
	return []string{"sh", "-c", fmt.Sprintf(`while [ ! -e "$1" ]; do sleep 0.05; done; exit %d`, code), "sh", gate}
}

func TestJobQueue_ABootAdoptsARunningJobAndSettlesItLater(t *testing.T) {
	dir := t.TempDir()
	gate := filepath.Join(t.TempDir(), "go")
	old := wqModelAt(t, dir)
	id := wqLaunch(t, old, wqGated(gate, 7)...)
	old.queue.store.Jobs[0].PeakRSSKB = 1 << 40
	require.NoError(t, old.saveQueue())

	m := wqRestart(t, dir)

	rec := wqRecord(t, m, id)
	assert.Equal(t, sched.Running, rec.State)
	assert.Equal(t, wqRecord(t, old, id).PID, rec.PID)
	assert.Less(t, rec.PeakRSSKB, int64(1<<40), "the peak restarts from zero when a daemon adopts a job")
	assert.NotContains(t, m.queue.starting, id)

	require.NoError(t, os.WriteFile(gate, nil, 0o600))
	done := wqSettle(t, m, id)
	assert.Equal(t, sched.Failed, done.State)
	require.NotNil(t, done.Exit)
	assert.Equal(t, 7, done.Exit.Code)
}

func TestJobQueue_ABootSettlesWhatEndedWhileTheDaemonWasDown(t *testing.T) {
	dir := t.TempDir()
	old := wqModelAt(t, dir)
	id := wqLaunch(t, old, "sh", "-c", "exit 0")
	wqShimDone(t, old, id)
	require.NoError(t, old.saveQueue())

	m := wqRestart(t, dir)

	rec := wqRecord(t, m, id)
	assert.Equal(t, sched.Done, rec.State)
	require.NotNil(t, rec.Exit)
	saved, err := sched.LoadStore(m.queue.dir)
	require.NoError(t, err)
	assert.Equal(t, sched.Done, saved.Jobs[0].State, "and written")
}

func TestJobQueue_ABootCallsAShimThatVanishedLost(t *testing.T) {
	dir := t.TempDir()
	old := wqModelAt(t, dir)
	rec := wqEnded(1, 0)
	rec.State, rec.Exit, rec.Ended, rec.PID = sched.Running, nil, time.Time{}, 999999
	wqSeed(t, old, rec)
	require.NoError(t, old.saveQueue())

	m := wqRestart(t, dir)

	got := wqRecord(t, m, 1)
	assert.Equal(t, sched.Lost, got.State)
	assert.Equal(t, lostWhy, got.Failure)
}

// A start whose save failed leaves a running shim behind a queued record: it
// is adopted as running, and never started twice (spec §5.6).
func TestJobQueue_ABootAdoptsAShimBehindAQueuedRecord(t *testing.T) {
	dir := t.TempDir()
	gate := filepath.Join(t.TempDir(), "go")
	old := wqModelAt(t, dir)
	id := wqLaunch(t, old, wqGated(gate, 0)...)
	old.queue.store.Jobs[0].State, old.queue.store.Jobs[0].PID, old.queue.store.Jobs[0].Started = sched.Queued, 0, time.Time{}
	require.NoError(t, old.saveQueue())

	m := wqRestart(t, dir)

	got := wqRecord(t, m, id)
	assert.Equal(t, sched.Running, got.State, "adopted")
	assert.Greater(t, got.PID, 1)
	assert.Empty(t, m.queue.starting)
	assert.Empty(t, m.Drain().Jobs, "no start job: it is never started twice")
	require.NoError(t, os.WriteFile(gate, nil, 0o600))
	assert.Equal(t, sched.Done, wqSettle(t, m, id).State)
}

func TestJobQueue_ABootSettlesAQueuedRecordFromItsExit(t *testing.T) {
	dir := t.TempDir()
	old := wqModelAt(t, dir)
	id := wqLaunch(t, old, "sh", "-c", "exit 3")
	wqShimDone(t, old, id)
	old.queue.store.Jobs[0].State, old.queue.store.Jobs[0].PID, old.queue.store.Jobs[0].Started = sched.Queued, 0, time.Time{}
	require.NoError(t, old.saveQueue())

	m := wqRestart(t, dir)

	got := wqRecord(t, m, id)
	assert.Equal(t, sched.Failed, got.State)
	require.NotNil(t, got.Exit)
	assert.Equal(t, 3, got.Exit.Code)
	assert.Empty(t, m.Drain().Jobs, "it is never started twice")
}

// A queued record whose shim never ran stays queued through adoption, and the
// first admission pass, which comes after it, starts it.
func TestJobQueue_ABootLeavesAnUnstartedQueuedJobToTheFirstPass(t *testing.T) {
	dir := t.TempDir()
	old := wqModelAt(t, dir)
	queued := wqEnded(1, 0)
	queued.State, queued.Exit, queued.Started, queued.Ended = sched.Queued, nil, time.Time{}, time.Time{}
	wqSeed(t, old, queued)
	require.NoError(t, old.saveQueue())

	m := wqModelAt(t, dir)

	assert.Equal(t, sched.Running, wqRecord(t, m, 1).State, "admitted")
	assert.True(t, m.queue.starting[1])
	assert.Len(t, m.Drain().Jobs, 1, "with one start job")
}

// The first admission pass counts the jobs boot adopted: with three default
// jobs running, the fourth still waits for cores.
func TestJobQueue_ABootCountsAdoptedJobsBeforeItAdmits(t *testing.T) {
	dir := t.TempDir()
	old := wqModelAt(t, dir)
	t.Cleanup(func() { wqStop(t, old) })
	var ids []sched.JobID
	for range 3 {
		ids = append(ids, wqSubmit(t, old, "sleep", "30"))
	}
	wqPump(t, old)
	fourth := wqSubmit(t, old, "sleep", "30")
	require.Equal(t, sched.Queued, wqRecord(t, old, fourth).State)

	m := wqRestart(t, dir)

	for _, id := range ids {
		assert.Equal(t, sched.Running, wqRecord(t, m, id).State)
	}
	assert.Equal(t, sched.Queued, wqRecord(t, m, fourth).State, "adoption came before admission")
	assert.Equal(t, "needs 4 cores (2 free)", m.queue.waiting[fourth])
	assert.Empty(t, m.queue.starting)
}

func TestJobQueue_ABootMarksNoticesStillOnTheirWayUndelivered(t *testing.T) {
	dir := t.TempDir()
	old := wqModelAt(t, dir)
	pending := wqEnded(1, 77)
	sent := wqEnded(2, 77)
	sent.Notice = sched.NoticeSent
	quiet := wqEnded(3, 77)
	quiet.Notify = false
	ownerless := wqEnded(4, 0)
	for _, rec := range []sched.Record{pending, sent, quiet, ownerless} {
		wqSeed(t, old, rec)
	}
	require.NoError(t, old.saveQueue())

	m := wqRestart(t, dir)

	got := wqRecord(t, m, 1)
	assert.Equal(t, sched.NoticeUndelivered, got.Notice)
	assert.Equal(t, "the daemon stopped before the message was delivered", got.NoticeWhy)
	assert.Equal(t, sched.NoticeSent, wqRecord(t, m, 2).Notice)
	assert.Equal(t, sched.NoticeNone, wqRecord(t, m, 3).Notice)
	assert.Equal(t, sched.NoticeNone, wqRecord(t, m, 4).Notice)
	saved, err := sched.LoadStore(m.queue.dir)
	require.NoError(t, err)
	assert.Equal(t, sched.NoticeUndelivered, saved.Jobs[0].Notice, "and written")
}

// A job that ended while no daemon ran is settled at boot, and its owner, a
// session this daemon serves, is told: its notice is not taken for one that
// was lost with the old daemon.
func TestJobQueue_ABootSendsTheNoticeOfAJobThatEndedWhileTheDaemonWasDown(t *testing.T) {
	withMod(t)
	dir := t.TempDir()
	old := wqModelAt(t, dir)
	id := wqLaunch(t, old, "sh", "-c", "echo done")
	wqShimDone(t, old, id)
	m := NewForTest(Options{})
	m.SetJobRunnerForTest(sched.TestRunner())
	inst := modInst(t, m, "owner")
	old.queue.store.Jobs[0].Owner, old.queue.store.Jobs[0].Notify = uint64(m.idOf(inst)), true
	require.NoError(t, old.saveQueue())

	m.loadQueue(dir, wqHost)
	wqPump(t, m)

	assert.Equal(t, sched.Done, wqRecord(t, m, id).State)
	require.NoError(t, m.ModHello("L1", ModHello{Mod: 1}))
	msgs, _, err := m.ModInbox("L1", "")
	require.NoError(t, err)
	require.Len(t, msgs, 1, "the owner is told")
	assert.Contains(t, msgs[0].Text, "finished: exit 0")
	assert.Equal(t, sched.NoticeNone, wqRecord(t, m, id).Notice, "still on its way")
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `CGO_ENABLED=0 go test ./core/ -run 'TestJobQueue_ABoot'`

Expected: the package compiles, and the tests fail: the adopted job is still `queued`/`running` as the file left it (`Should be: "done"`, `Should be: "lost"`), the queued record with a running shim is started again (`Should be empty` on `m.Drain().Jobs`), and the pending notice is not marked (`Should be: "undelivered"`).

- [ ] **Step 3: Implement**

In `core/queue.go`, `loadQueue` marks the notices that were on their way, adopts, and only then admits. Replace

```go
	q.store = store
	q.moved()
	m.admitJobs()
}
```

with

```go
	q.store = store
	q.moved()
	m.markPendingNotices()
	m.adoptJobs()
	m.admitJobs()
}
```

and append to the end of the file

```go

// markPendingNotices records as undelivered every notice that was still on its
// way when the last daemon stopped: a finished job that asked for one, has an
// owner and has no fate. Its message may have reached the mod or not, and a
// message the mod may have submitted is never sent again. It runs before
// adoption, whose settled jobs send their own notices.
func (m *Model) markPendingNotices() {
	q := m.queue
	changed := false
	for i := range q.store.Jobs {
		rec := &q.store.Jobs[i]
		if rec.State.Finished() && rec.Notify && rec.Owner != 0 && rec.Notice == sched.NoticeNone {
			rec.Notice, rec.NoticeWhy = sched.NoticeUndelivered, "the daemon stopped before the message was delivered"
			changed = true
		}
	}
	if changed {
		q.moved()
		m.saveQueueQuiet("boot")
	}
}

// adoptJobs settles what ran while no daemon was up (spec §5.6): each running
// job through the lock test of a pass, which settles one whose lock is free
// from exit.json (lost when there is none), and each queued job too, since a
// start whose save failed leaves a running shim behind a queued record: one
// whose lock is held is adopted as running, one with an exit.json settles from
// it, and either way it is never started twice. The pass runs here, before the
// loop starts, so the first admission pass counts the jobs adopted. A job's
// peak memory restarts from zero.
func (m *Model) adoptJobs() {
	q := m.queue
	for i := range q.store.Jobs {
		if q.store.Jobs[i].State == sched.Running {
			q.store.Jobs[i].PeakRSSKB = 0
		}
	}
	pass := m.queuePassFor(true)
	if len(pass.jobs) == 0 {
		return
	}
	m.deliverQueuePass(runQueuePass(pass))
}
```

- [ ] **Step 4: Run them to see them pass**

Run: `CGO_ENABLED=0 go test ./core/ -run 'TestJobQueue_'`

Expected: `ok  	github.com/aidan-bailey/loom/core	<time>`. Then `CGO_ENABLED=0 go test ./core/` prints `ok`.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(core): boot adopts the jobs that ran while the daemon was down, queued records included, before it admits" -- core/queue.go core/queue_test.go
```

### Task 3.6: Pause and kill cancel the owner's jobs, and the jobs run beside the loop

**Files:**
- Modify: `core/requests.go:278-311`
- Modify: `core/queue_test.go` (append)
- Modify: `core/queue_watch_test.go` (append)

- [ ] **Step 1: Write the failing tests**

Append to `core/queue_test.go`:

```go

// wqOwnerSession serves a started Claude session on launch L1, in a workspace
// with storage (the pause and kill requests need one), and returns its ID. A
// job submitted with Launch "L1" is that session's.
func wqOwnerSession(t *testing.T, m *Model) InstanceID {
	t.Helper()
	inst := startedInst(t, "owner", "claude")
	ws := storedWorkspace(t, "owners")
	ws.add(inst)
	m.SetWorkspacesForTest(ws)
	adoptLaunch(t, m, inst, "L1", time.Now())
	m.Drain()
	return m.idOf(inst)
}

// Pausing or killing a job's owner cancels its queued and running jobs first,
// with no notice, and leaves the jobs of other owners and of none alone (spec
// §5.7).
func TestJobQueue_PauseOrKillCancelsTheOwnersJobsAndOnlyTheirs(t *testing.T) {
	cases := []struct {
		name   string
		act    func(*Model, InstanceID)
		reason string
	}{
		{"pause", func(m *Model, id InstanceID) { m.Pause(id, 0) }, "because the session was paused"},
		{"kill", func(m *Model, id InstanceID) { m.Kill(id, 0) }, "because the session was killed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := wqLiveModel(t)
			wqConfig(t, m, `{"cpus": 4}`) // one default job at a time
			owner := wqOwnerSession(t, m)
			submit := func(launch string, argv ...string) sched.JobID {
				sub := wqJob(t, argv...)
				sub.Launch, sub.Notify = launch, true
				id, _, err := m.submitJob(sub)
				require.NoError(t, err)
				return id
			}
			running := submit("L1", "sleep", "30")
			queued := submit("L1", "sleep", "30")
			ownerless := submit("", "sleep", "30")
			stranger := wqEnded(9, 77)
			stranger.State, stranger.Exit, stranger.Started, stranger.Ended = sched.Queued, nil, time.Time{}, time.Time{}
			stranger.Argv, stranger.Cwd, stranger.Claim = []string{"sleep", "30"}, t.TempDir(), sched.Claim{CPUs: 4, MemoryGB: 6}
			wqSeed(t, m, stranger)
			wqPump(t, m) // the running job's start: a real shim
			require.Equal(t, owner, InstanceID(wqRecord(t, m, running).Owner))

			c.act(m, owner)
			m.Drain() // the session's own job, which this test does not run, and the pass that went with it
			m.gate(gateQueue).inFlight = false

			got := wqRecord(t, m, queued)
			require.Equal(t, sched.Cancelled, got.State, "queued: cancelled at once")
			assert.Equal(t, c.reason, got.CancelReason)
			assert.False(t, got.Notify, "and no notice is sent for it")
			got = wqRecord(t, m, running)
			assert.Equal(t, sched.Running, got.State, "running: gateQueue ends it, and the request does not wait for that")
			assert.Equal(t, c.reason, got.CancelReason)
			assert.False(t, got.Notify)
			assert.Empty(t, wqRecord(t, m, ownerless).CancelReason, "a job with no owner is not touched")
			assert.Empty(t, wqRecord(t, m, 9).CancelReason, "nor is another owner's")

			rec := wqSettle(t, m, running)
			assert.Equal(t, sched.Cancelled, rec.State)
			require.NotNil(t, rec.Exit)
			assert.Equal(t, "SIGTERM", rec.Exit.Signal)
			assert.Equal(t, sched.NoticeNone, rec.Notice, "no notice, not even an undelivered one")
		})
	}
}

// The cancel comes after the request is admitted: a request the model refuses
// (the session is busy) changes nothing.
func TestJobQueue_ARefusedPauseOrKillCancelsNothing(t *testing.T) {
	m := wqModel(t)
	owner := wqOwnerSession(t, m)
	m.Pause(owner, 0) // admitted: it moves the session to Loading
	m.Drain()
	sub := wqJob(t, "sleep", "30")
	sub.Launch = "L1"
	later, _, err := m.submitJob(sub)
	require.NoError(t, err)
	m.Drain()

	m.Pause(owner, 0) // refused: the session is busy
	m.Kill(owner, 0)  // refused too

	rec := wqRecord(t, m, later)
	assert.Empty(t, rec.CancelReason)
	assert.NotEqual(t, sched.Cancelled, rec.State)
}
```

Append to `core/queue_watch_test.go`:

```go

// The jobs run off the loop on snapshots: while one runs, the loop edits the
// queue in every way a request can. Under -race a job that read model state
// shows up here.
func TestJobQueue_AGatePassOverlapsTheLoopsEdits(t *testing.T) {
	m := wqLiveModel(t)
	id := wqLaunch(t, m, "sh", "-c", "sleep 30")
	m.gate(gateQueue).expedite()
	require.True(t, m.maybeQueue())
	jobs := m.Drain().Jobs
	require.Len(t, jobs, 1)

	done := make(chan any, 1)
	go func() { done <- jobs[0]() }()
	for i := range 200 {
		extra := sched.JobID(100 + i)
		m.queue.store.Jobs[m.queue.index(id)].CancelReason = fmt.Sprintf("edit %d", i)
		m.queue.store.Jobs = append(m.queue.store.Jobs, wqEnded(extra, 0))
		m.queue.waiting[extra] = "edit"
		m.queue.usage[id] = jobUsage{RSSKB: int64(i)}
		m.queue.starting[extra] = true
		m.queue.cpuMarks[id] = cpuMark{cpu: time.Duration(i)}
		m.queue.rev++
	}
	m.Deliver(<-done)

	assert.False(t, m.gate(gateQueue).inFlight)
}

func TestJobQueue_AStartJobOverlapsTheLoopsEdits(t *testing.T) {
	m := wqLiveModel(t)
	id := wqSubmit(t, m, "sh", "-c", "exit 0")
	jobs := m.Drain().Jobs
	require.Len(t, jobs, 1)

	done := make(chan any, 1)
	go func() { done <- jobs[0]() }()
	for i := range 200 {
		rec := &m.queue.store.Jobs[m.queue.index(id)]
		rec.Cwd, rec.Description = fmt.Sprintf("/elsewhere/%d", i), "edit"
		m.queue.store.Jobs = append(m.queue.store.Jobs, wqEnded(sched.JobID(100+i), 0))
		m.queue.rev++
	}
	m.Deliver(<-done)

	assert.Greater(t, wqRecord(t, m, id).PID, 1, "it started on the snapshot it was given")
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `CGO_ENABLED=0 go test ./core/ -run 'TestJobQueue_PauseOrKill|TestJobQueue_ARefusedPause'`

Expected: the pause and kill subtests fail at once on the owner's queued job (`Not equal`: expected `"cancelled"`, actual `"queued"`), because `Pause` and `Kill` do not touch the queue yet. `TestJobQueue_ARefusedPauseOrKillCancelsNothing` and the two overlap tests (`TestJobQueue_AGatePassOverlapsTheLoopsEdits`, `TestJobQueue_AStartJobOverlapsTheLoopsEdits`) pass already; they pin what must stay true.

- [ ] **Step 3: Implement**

In `core/requests.go`, `Kill` and `Pause` cancel the owner's jobs once the request is admitted and before they act. Replace

```go
func (m *Model) Kill(id InstanceID, req ReqID) {
	inst, ws, ok := m.admit(opKill, id, req)
	if !ok {
		return
	}
	pre, job := m.killInst(ws, inst)
```

with

```go
func (m *Model) Kill(id InstanceID, req ReqID) {
	inst, ws, ok := m.admit(opKill, id, req)
	if !ok {
		return
	}
	// The session's work-queue jobs go first, without waiting for them to
	// exit: the kill removes the worktree they run in.
	m.cancelOwnerJobs(id, "because the session was killed")
	pre, job := m.killInst(ws, inst)
```

and replace

```go
func (m *Model) Pause(id InstanceID, req ReqID) {
	inst, ws, ok := m.admit(opPause, id, req)
	if !ok {
		return
	}
	job := m.pauseInst(ws, inst)
```

with

```go
func (m *Model) Pause(id InstanceID, req ReqID) {
	inst, ws, ok := m.admit(opPause, id, req)
	if !ok {
		return
	}
	// The session's work-queue jobs go first, without waiting for them to
	// exit: a paused agent can't take a result.
	m.cancelOwnerJobs(id, "because the session was paused")
	job := m.pauseInst(ws, inst)
```

- [ ] **Step 4: Run them to see them pass, then the story's gates**

Run: `CGO_ENABLED=0 go test ./core/ -run 'TestJobQueue_'`

Expected: `ok  	github.com/aidan-bailey/loom/core	<time>`.

Then the story's Verify, and the cross-builds (the story's code uses `syscall` only for the `Signal` constants and calls `sched`'s Windows stubs):

```bash
CGO_ENABLED=0 go test ./core/... && CC=clang CGO_ENABLED=1 go test -race ./core/ -run 'Queue|Job'
GOOS=windows CGO_ENABLED=0 go build ./... && GOOS=darwin CGO_ENABLED=0 go build ./...
CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go run ./tools/claudemd
```

Expected: each command prints `ok`/nothing and exits 0 (the race run needs `clang` where gcc is missing). `go run ./tools/claudemd` still passes, since the story adds no rule to any `CLAUDE.md`.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(core): pausing or killing a session cancels its work-queue jobs first" -- core/requests.go core/queue_test.go core/queue_watch_test.go
```

### Task 3.7: `SaveSettings` keeps the global `config.json`'s `scheduler` block as it is on disk

**Files:**
- Modify: `core/settings.go:1-40`
- Test: `core/settings_test.go` (append)

`Model.SaveSettings` writes the workspace's whole in-memory config. For the global workspace that includes a `scheduler` block that may be stale: a hand edit of the block takes effect through `refreshPolicy`, which re-reads the file, but the workspace's `ws.cfg` is never refreshed; and the S overlay saves a copy it took when it opened, so a `SetSchedulerCaps` from another TUI made meanwhile would be reverted. So, saving the global workspace, `SaveSettings` never writes the block it was handed or holds: it reads the block from the global `config.json` itself, writes that, and sets `ws.cfg`'s copy to it. A file that can't be read or parsed refuses the save with the error and changes nothing (the file is not quarantined or rewritten); a missing file reads as no block. A non-global workspace's save is unchanged.

- [ ] **Step 1: Write the failing tests**

Append to `core/settings_test.go`:

```go

// wqGlobalWorkspaceModel serves a stored global workspace over a model whose
// work queue is loaded in the same dir, so setSchedulerCaps (what another TUI's
// SetSchedulerCaps ends in) writes the config.json that SaveSettings saves.
func wqGlobalWorkspaceModel(t *testing.T) (*Model, *Workspace, string) {
	t.Helper()
	ws := storedWorkspace(t, "")
	dir := ws.ctx.ConfigDir
	m := wqModelAt(t, dir) // points LOOM_GLOBAL_DIR at dir
	m.SetWorkspacesForTest(ws)
	return m, ws, dir
}

// A hand edit of the scheduler block takes effect through refreshPolicy but
// never reaches the global workspace's ws.cfg: the next S-overlay save must not
// write ws.cfg's stale block over it.
func TestSaveSettings_AHandEditOfTheSchedulerBlockSurvivesAnOverlaySave(t *testing.T) {
	m, ws, dir := wqGlobalWorkspaceModel(t)
	require.NoError(t, m.setSchedulerCaps(config.SchedulerSettings{CPUs: 4}))
	require.Equal(t, config.SchedulerSettings{CPUs: 4}, ws.cfg.GetScheduler())

	hand := `{"scheduler": {"cpus": 9, "keep_finished": 5}}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, config.ConfigFileName), []byte(hand), 0o644))
	s := ws.cfg.Snapshot() // the overlay's copy still holds cpus 4
	s.DefaultProgram = "aider --yes"
	require.NoError(t, m.SaveSettings(m.wsIDOf(ws), s))

	want := config.SchedulerSettings{CPUs: 9, KeepFinished: 5}
	saved := config.LoadConfigFrom(dir)
	assert.Equal(t, want, saved.GetScheduler(), "the hand edit is on disk still")
	assert.Equal(t, "aider --yes", saved.GetProgram(), "the rest of the save is written")
	assert.Equal(t, want, ws.cfg.GetScheduler(), "ws.cfg now holds the block the file has")
	assert.Equal(t, "aider --yes", ws.cfg.GetProgram())
}

// The overlay edits a copy taken when it opened and saves it on every change: a
// SetSchedulerCaps from another TUI made after the copy was taken must survive
// the copy's save.
func TestSaveSettings_AnOverlaysStaleCopyDoesNotRevertNewCaps(t *testing.T) {
	m, ws, dir := wqGlobalWorkspaceModel(t)
	require.NoError(t, m.setSchedulerCaps(config.SchedulerSettings{CPUs: 4}))
	copyOfTheOverlay := ws.cfg.Snapshot() // taken as the overlay opens
	require.NotNil(t, copyOfTheOverlay.Scheduler)
	require.Equal(t, 4, copyOfTheOverlay.Scheduler.CPUs)

	require.NoError(t, m.setSchedulerCaps(config.SchedulerSettings{CPUs: 8, MemoryGB: 12})) // another TUI
	copyOfTheOverlay.DefaultProgram = "aider --yes"
	require.NoError(t, m.SaveSettings(m.wsIDOf(ws), copyOfTheOverlay))

	want := config.SchedulerSettings{CPUs: 8, MemoryGB: 12}
	assert.Equal(t, want, config.LoadConfigFrom(dir).GetScheduler(), "the newer caps are on disk still")
	assert.Equal(t, want, ws.cfg.GetScheduler())
	assert.Equal(t, 4, copyOfTheOverlay.Scheduler.CPUs, "the caller's copy is not changed")
}

// A file without the block, or no file at all, reads as no block: ws.cfg's
// stale block is not revived.
func TestSaveSettings_AMissingBlockOrFileReadsAsNoBlock(t *testing.T) {
	m, ws, dir := wqGlobalWorkspaceModel(t)
	path := filepath.Join(dir, config.ConfigFileName)
	ws.cfg.SetScheduler(config.SchedulerSettings{CPUs: 4}) // stale in memory
	require.NoError(t, os.WriteFile(path, []byte(`{}`), 0o644))

	require.NoError(t, m.SaveSettings(m.wsIDOf(ws), ws.cfg.Snapshot()))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.NotContains(t, string(data), `"scheduler"`, "a block the file lacks is not written back")
	assert.Equal(t, config.SchedulerSettings{}, ws.cfg.GetScheduler())

	ws.cfg.SetScheduler(config.SchedulerSettings{CPUs: 4})
	require.NoError(t, os.Remove(path))
	require.NoError(t, m.SaveSettings(m.wsIDOf(ws), ws.cfg.Snapshot()), "a missing file is no block, not an error")
	data, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.NotContains(t, string(data), `"scheduler"`)
	assert.Equal(t, config.SchedulerSettings{}, ws.cfg.GetScheduler())
}

// A global config.json that can't be parsed (or read) refuses the save with the
// error and changes nothing: not the file, not ws.cfg, not the model's program.
func TestSaveSettings_AnUnreadableGlobalConfigRefusesTheSave(t *testing.T) {
	refused := func(t *testing.T, m *Model, ws *Workspace) error {
		t.Helper()
		before, programBefore := ws.cfg.GetProgram(), m.program
		s := ws.cfg.Snapshot()
		s.DefaultProgram = "aider --yes"

		err := m.SaveSettings(m.wsIDOf(ws), s)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "save settings")
		assert.Equal(t, before, ws.cfg.GetProgram(), "ws.cfg is not replaced")
		assert.Equal(t, programBefore, m.program)
		return err
	}

	t.Run("unparseable", func(t *testing.T) {
		m, ws, dir := wqGlobalWorkspaceModel(t)
		path := filepath.Join(dir, config.ConfigFileName)
		broken := `{"scheduler": {"cpus": `
		require.NoError(t, os.WriteFile(path, []byte(broken), 0o644))

		refused(t, m, ws)

		data, err := os.ReadFile(path)
		require.NoError(t, err, "the file is not quarantined")
		assert.Equal(t, broken, string(data), "the file is left as it was")
	})
	t.Run("unreadable", func(t *testing.T) {
		m, ws, dir := wqGlobalWorkspaceModel(t)
		path := filepath.Join(dir, config.ConfigFileName)
		require.NoError(t, os.Mkdir(path, 0o755)) // reading a directory fails

		refused(t, m, ws)

		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.True(t, info.IsDir(), "the path is left as it was")
	})
}

// Only the global workspace's save is guarded: another workspace's config.json
// carries no scheduler block of the queue's, and its save writes what it holds.
// The global config.json is not read, let alone written.
func TestSaveSettings_ANonGlobalWorkspaceSavesWhatItHolds(t *testing.T) {
	globalDir := t.TempDir()
	m := wqModelAt(t, globalDir)
	hand := `{"scheduler": {"cpus": 9}}`
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, config.ConfigFileName), []byte(hand), 0o644))
	a := storedWorkspace(t, "a")
	m.SetWorkspacesForTest(a)
	a.cfg.SetScheduler(config.SchedulerSettings{CPUs: 3})

	s := a.cfg.Snapshot()
	s.DefaultProgram = "aider --yes"
	require.NoError(t, m.SaveSettings(m.wsIDOf(a), s))

	assert.Equal(t, config.SchedulerSettings{CPUs: 3}, config.LoadConfigFrom(a.ctx.ConfigDir).GetScheduler(), "it writes the block it was handed")
	assert.Equal(t, config.SchedulerSettings{CPUs: 3}, a.cfg.GetScheduler())
	data, err := os.ReadFile(filepath.Join(globalDir, config.ConfigFileName))
	require.NoError(t, err)
	assert.Equal(t, hand, string(data), "the global config.json is untouched")
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `CGO_ENABLED=0 go test ./core/ -run 'TestSaveSettings_'`

Expected: the package builds. `TestSaveSettings_AHandEditOfTheSchedulerBlockSurvivesAnOverlaySave` fails (`expected: config.SchedulerSettings{CPUs:9, ... KeepFinished:5}`, `actual: config.SchedulerSettings{CPUs:4, ...}`: the stale block overwrote the hand edit), `TestSaveSettings_AnOverlaysStaleCopyDoesNotRevertNewCaps` fails (`CPUs:8, MemoryGB:12` expected, `CPUs:4` on disk), `TestSaveSettings_AMissingBlockOrFileReadsAsNoBlock` fails (`"scheduler"` found in the file, ws.cfg still holds `CPUs:4`), and each `TestSaveSettings_AnUnreadableGlobalConfigRefusesTheSave` subtest fails (`An error is expected but got nil`). `TestSaveSettings_ANonGlobalWorkspaceSavesWhatItHolds` and the older `TestSaveSettings_*` tests pass already: they pin the side that must not change.

- [ ] **Step 3: Implement**

In `core/settings.go`, replace the import block

```go
import (
	"fmt"

	"github.com/aidan-bailey/loom/config"
)
```

with

```go
import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aidan-bailey/loom/config"
)
```

In `SaveSettings`'s comment, replace the line

```go
// client's drafts default to is the client's to follow. An unknown id is
// an error. Moved from app/state_settings.go, which did this to the
// model's own config.
```

with

```go
// client's drafts default to is the client's to follow. An unknown id is
// an error. Moved from app/state_settings.go, which did this to the
// model's own config.
//
// Saving the global workspace never writes a "scheduler" block it was
// handed or holds: the block is the work queue's, which a hand edit or
// another client's SetSchedulerCaps changes in the file, while ws.cfg and
// an overlay's copy of the settings go stale. So the block is re-read from
// the global config.json (as refreshPolicy does), written, and set as
// ws.cfg's. A file that can't be read or parsed refuses the save and
// changes nothing; a missing file reads as no block.
```

and replace

```go
	ws.cfg.ReplaceSettings(s)
	dir := ""
```

with

```go
	if isGlobalWS(ws) {
		block, err := diskSchedulerBlock(ws.configDir())
		if err != nil {
			return fmt.Errorf("save settings: %w", err)
		}
		s.Scheduler = block // s is this call's copy: the caller's is not changed
	}
	ws.cfg.ReplaceSettings(s)
	dir := ""
```

Then append to `core/settings.go`:

```go

// diskSchedulerBlock reads the "scheduler" block of dir's config.json as the
// file holds it: nil for a file without the block or a missing file. A file
// that can't be read or parsed is an error, and is left as it is
// (config.LoadConfigFrom would quarantine an unparseable one and fall back to
// defaults, which would make the save write a block nobody chose).
func diskSchedulerBlock(dir string) (*config.SchedulerSettings, error) {
	path := filepath.Join(dir, config.ConfigFileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the scheduler block from %s: %w", path, err)
	}
	var file struct {
		Scheduler *config.SchedulerSettings `json:"scheduler"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return file.Scheduler, nil
}
```

- [ ] **Step 4: Run them to see them pass, then the story's gates**

Run: `CGO_ENABLED=0 go test ./core/ -run 'TestSaveSettings_'`

Expected: `ok  	github.com/aidan-bailey/loom/core	<time>`.

Then the story's gates:

```bash
bash -c 'out=$(gofmt -l $(git ls-files "*.go" | grep -v "^vendor/")) || exit 1; if [ -n "$out" ]; then echo "gofmt would reformat:"; echo "$out"; exit 1; fi'
CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test ./...
CC=clang CGO_ENABLED=1 go test -race ./core/ -run 'Queue|Job|SaveSettings'
```

Expected: each command prints `ok`/nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git commit -m "fix(core): SaveSettings keeps the global config's scheduler block as it is on disk" -- core/settings.go core/settings_test.go
```

## Story 4: The queue on the wire: the requests, `QueueView` and `QueueChanged`

- **Developer:** intermediate-dev
- **Files:** `core/iface.go`, `core/loop_core.go`, `core/queue_requests.go`, `core/queue_view.go`, `core/events.go`, `core/events_json.go`, `core/state_publish.go`, `core/model.go`, `core/rpc/server.go`, `core/rpc/replica.go`, `core/rpc/methods_gen.go`, `docs/specs/protocol.md`, `app/core_glue.go`, `app/app.go`, `core/queue_requests_test.go`, `core/queue_view_test.go`, `core/view_test.go`, `core/state_views_test.go`, `core/loop_test.go`, `core/rpc/client_test.go`, `core/rpc/wire_test.go`, `app/core_glue_test.go`
- **Interfaces:** exposes `QueueView`, `JobView`, `CancelFrom`, the `Core` methods, `Reply.Job`, `QueueChanged` and `home.queue` of the names contract (Story 4); uses Story 3's `queueState` and internal operations
- **Verify:** `CGO_ENABLED=0 go test ./core/... ./app/...`
- **Review:** deep

This story publishes the work queue to every client. Task 4.1 builds `QueueView` and `JobView` from Story 3's `queueState`, publishes them as the state event `QueueChanged` (after `GitHubChanged` and before the instance views, in the model's `publishState` and in `Snapshot`), keeps them in the replica, and has the TUI's applier set `home.queue`; it ends by rewriting `docs/specs/protocol.md`. Task 4.2 turns Story 3's internal operations into `Core` requests (`SubmitJob`, `CancelJob`, `MoveJobToFront`, `NotifyJob`, `SetSchedulerCaps`) that answer at once with their refusal (matching `ErrRefused`), adds `Queue() QueueView` as an `rpc:local` query, gives `Reply` its `Job` field (the new job's ID; the unknown launch's warning rides in `Reply.Notice`), and regenerates `core/rpc/methods_gen.go` and `docs/specs/protocol.md` by command. `rpc.Protocol` stays 2: methods and an event are added, nothing is renamed or removed. No `QueueView` field holds a job's environment, and a test pins that. The code holds no landmine marker.

The tests build their queues through the production path, not through a seam: a global dir seeded with `sched.SaveStore`, `sched.WriteSpec` and a `config.json` scheduler block, loaded as a boot loads it (Story 3's `wqModelAt`), and a running job is a real shim (Story 3's `wqLaunch`, run by `sched.TestRunner()`; `core`'s `TestMain` hands over to the shim, which Story 3 adds). The packages whose `TestMain` runs no shim (`core/rpc`, `app`) use finished records and queued ones that cannot start (a claim over a cap, an invalid `scheduler` block), or build `core.QueueView` values directly.

### Task 4.1: Publish the queue: `QueueView`, `QueueChanged`, the replica and the TUI's applier

**Files:**
- Create: `core/queue_view.go`, `core/queue_view_test.go`
- Modify: `core/events.go`, `core/state_publish.go`, `core/model.go` (the `queuePub` field next to `usagePublishedGen`)
- Modify: `core/rpc/server.go` (`coalesceKey`), `core/rpc/replica.go`, `core/rpc/wire_test.go`
- Modify: `app/app.go`, `app/core_glue.go`, `app/core_glue_test.go`
- Modify: `core/view_test.go`, `core/state_views_test.go`, `core/loop_test.go` (their counts of the first Sync and of the snapshot, the snapshot's kinds, and the helper `instanceEvents`)
- Regenerated: `docs/specs/protocol.md`

The queue's view is built from `m.queue` (Story 3's `queueState`) by `queueView`, in the order the contract says: running jobs by start, queued in queue order, finished newest end first. `publishQueue` emits `QueueChanged` on the first Sync and whenever `queueState.rev` moves or a different `queueState` replaces the one published (a boot installs its own). Every point of `docs/claude/adding-a-core-event.md` for a state event is here: `EventTypes`, `coalesceKey`, the replica's flag with `apply` and `events` (in the model's publish order), `Model.Snapshot`, and the TUI's applier.

- [ ] **Step 1: Write the failing tests**

Create `core/queue_view_test.go`. Its queues come through the production path: a global dir seeded with `sched.SaveStore`, `sched.WriteSpec` and a `config.json` scheduler block, loaded as a boot loads it (`wqModelAt`, Story 3's helper in `core/queue_test.go`), and a running job is a real shim (`wqLaunch`, Story 3's helper in `core/queue_watch_test.go`, over the runner `sched.TestRunner()` that `wqModelAt` sets). The block `qvBlock` fixes the caps whatever host the tests run on, and the queued jobs are ones that cannot start (the running jobs hold every core, or admission is paused):

```go
package core

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var qvEpoch = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

// qvBlock is the scheduler block these tests configure: 8 cores, 16 GB, 2
// I/O jobs and a default claim of 2 cores and 4 GB. The policy it resolves
// to (qvPolicy) does not depend on the host the tests run on.
const qvBlock = `{"cpus": 8, "memory_gb": 16, "io_jobs": 2, "default_claim": {"cpus": 2, "memory_gb": 4}}`

// qvPolicy is the policy qvBlock resolves to.
var qvPolicy = sched.Policy{
	Caps:         sched.Caps{CPUs: 8, MemoryGB: 16, IOJobs: 2},
	DefaultClaim: sched.Claim{CPUs: 2, MemoryGB: 4},
	StarveAfter:  10 * time.Minute,
	KeepFinished: 50,
}

// qvRecord is a record of the given state, ordered by its ID, with the
// default claim of qvBlock. Its submission time is now, so a queued job has
// not yet waited long enough to starve the others.
func qvRecord(id sched.JobID, state sched.State) sched.Record {
	return sched.Record{
		ID:          id,
		Order:       int64(id),
		Description: fmt.Sprintf("job %d", id),
		Argv:        []string{"sleep", "1"},
		Cwd:         "/work",
		Claim:       sched.Claim{CPUs: 2, MemoryGB: 4},
		Tail:        20,
		State:       state,
		Submitted:   time.Now(),
	}
}

// qvFinished is a finished record that ended at ended: a done job exits 0, a
// failed one exits 2 after a peak of 512 KB, a cancelled one was cancelled
// from the work view.
func qvFinished(id sched.JobID, state sched.State, ended time.Time) sched.Record {
	r := qvRecord(id, state)
	r.Started, r.Ended = ended.Add(-time.Minute), ended
	switch state {
	case sched.Done:
		r.Exit = &sched.Exit{Code: 0, Ended: ended}
	case sched.Failed:
		r.Exit = &sched.Exit{Code: 2, Ended: ended, MaxRSSKB: 512}
		r.Failure = "exit 2"
	case sched.Cancelled:
		r.CancelReason = "from the work view"
	}
	return r
}

// qvSeed is a global dir that holds the scheduler block block (none when ""),
// and a jobs dir with a spec.json for each of jobs and a jobs.json listing
// them. The next ID is the greater of next and one past the highest ID.
func qvSeed(t *testing.T, block string, next sched.JobID, jobs ...sched.Record) string {
	t.Helper()
	dir := t.TempDir()
	if block != "" {
		require.NoError(t, os.WriteFile(filepath.Join(dir, config.ConfigFileName), []byte(`{"scheduler": `+block+`}`), 0o644))
	}
	jobsDir := filepath.Join(dir, "jobs")
	next = max(next, 1)
	for _, r := range jobs {
		require.NoError(t, sched.WriteSpec(sched.JobDir(jobsDir, r.ID), sched.Spec{ID: r.ID, Argv: r.Argv, Cwd: r.Cwd}))
		next = max(next, r.ID+1)
	}
	require.NoError(t, sched.SaveStore(jobsDir, sched.Store{Version: sched.StoreVersion, NextID: next, Jobs: jobs}))
	return dir
}

// qvLive is wqModelAt over dir for a test that runs real shims: when the test
// ends every job still unfinished is cancelled and settled, so no process
// group outlives it.
func qvLive(t *testing.T, dir string) *Model {
	t.Helper()
	m := wqModelAt(t, dir)
	t.Cleanup(func() { wqStop(t, m) })
	return m
}

func jobIDs(v QueueView) []sched.JobID {
	var ids []sched.JobID
	for _, j := range v.Jobs {
		ids = append(ids, j.ID)
	}
	return ids
}

func jobOf(t *testing.T, v QueueView, id sched.JobID) JobView {
	t.Helper()
	for _, j := range v.Jobs {
		if j.ID == id {
			return j
		}
	}
	require.Failf(t, "no such job", "job %d is not in the view", id)
	return JobView{}
}

// The view lists the running jobs by start, the queued ones in queue order
// and the finished ones newest end first; it totals what the running jobs
// claim and use, takes an exclusive claim's need at the caps in effect, and
// shows usage only for a running job and a wait only for a queued one. Two
// real shims run; the one launched first is made the later to start, so the
// order is by start and not by ID.
func TestQueueView_OrdersJobsAndTotals(t *testing.T) {
	queuedExcl := qvRecord(5, sched.Queued)
	queuedExcl.Claim = sched.Claim{Exclusive: true}
	queuedFront := qvRecord(6, sched.Queued)
	queuedFront.Order = 2 // moved to the front
	queuedFront.Claim = sched.Claim{CPUs: 6, MemoryGB: 4}
	dir := qvSeed(t, qvBlock, 0,
		qvFinished(4, sched.Failed, qvEpoch.Add(90*time.Second)), queuedExcl,
		qvFinished(2, sched.Done, qvEpoch.Add(30*time.Second)),
		qvFinished(8, sched.Cancelled, qvEpoch.Add(60*time.Second)), queuedFront)
	m := qvLive(t, dir)
	first := wqLaunch(t, m, "sh", "-c", "sleep 30")
	second := wqLaunch(t, m, "sh", "-c", "sleep 30")
	require.Equal(t, []sched.JobID{9, 10}, []sched.JobID{first, second}, "fixture: the IDs follow the seeded ones")
	m.queue.store.Jobs[m.queue.index(first)].Started = m.queue.store.Jobs[m.queue.index(second)].Started.Add(time.Second)
	wqPass(t, m)
	wqPass(t, m)
	m.queue.usage[2] = jobUsage{RSSKB: 999} // a finished job's stale usage
	m.admitJobs()
	m.queue.waiting[first] = "stale: it is running"

	v := m.Queue()

	assert.Equal(t, []sched.JobID{second, first, 6, 5, 4, 8, 2}, jobIDs(v), "running by start, queued in queue order, finished newest first")
	assert.Equal(t, wqHost, v.Host)
	assert.Equal(t, config.SchedulerSettings{CPUs: 8, MemoryGB: 16, IOJobs: 2, DefaultClaim: &config.ClaimSettings{CPUs: 2, MemoryGB: 4}}, v.Settings)
	assert.Equal(t, qvPolicy, v.Policy)
	assert.Equal(t, filepath.Join(dir, "jobs"), v.Dir)
	assert.Empty(t, v.Paused)
	assert.Equal(t, sched.Need{CPUs: 8, MemoryGB: 12}, v.Claimed, "what the two running jobs' claims take")

	run1, run2 := jobOf(t, v, first), jobOf(t, v, second)
	assert.Equal(t, sched.Need{CPUs: 4, MemoryGB: 6}, run1.Need)
	assert.Equal(t, filepath.Join(sched.JobDir(v.Dir, first), sched.LogFile), run1.LogPath)
	assert.Empty(t, run1.Waiting, "a running job waits for nothing")
	assert.Greater(t, run1.RSSKB, int64(0), "its sampled usage")
	assert.Equal(t, m.queue.usage[first].RSSKB, run1.RSSKB)
	assert.Equal(t, run1.RSSKB+run2.RSSKB, v.UsedRSSKB, "the finished job's stale usage is not counted")
	assert.InDelta(t, run1.CPU+run2.CPU, v.UsedCPU, 1e-9)

	excl := jobOf(t, v, 5)
	assert.Equal(t, sched.Need{CPUs: 8, MemoryGB: 16, IOSlots: 2}, excl.Need, "an exclusive claim takes the caps as they stand")
	assert.Equal(t, "needs 8 cores (0 free)", excl.Waiting)
	front := jobOf(t, v, 6)
	assert.Equal(t, sched.Need{CPUs: 6, MemoryGB: 4}, front.Need)
	assert.Equal(t, "needs 6 cores (0 free)", front.Waiting)

	done := jobOf(t, v, 2)
	assert.Zero(t, done.RSSKB, "a finished job shows no usage")
	require.NotNil(t, done.Exit)
	assert.Zero(t, done.Exit.Code)
	assert.True(t, done.Exit.Ended.Equal(qvEpoch.Add(30*time.Second)))
	failed := jobOf(t, v, 4)
	assert.Equal(t, "exit 2", failed.Failure)
	assert.Equal(t, int64(512), failed.Exit.MaxRSSKB)
	assert.Equal(t, "from the work view", jobOf(t, v, 8).CancelReason)
}

// While admission is paused the policy is zero, so an exclusive claim needs
// nothing, and a queued job the last pass said nothing about waits for the
// reason; one it did speak of keeps its own. The block's invalid cores pause
// admission, so neither job starts.
func TestQueueView_PausedQueueSaysWhy(t *testing.T) {
	const why = "scheduler.cpus is 5000: it must be 1–1024"
	exclusive := qvRecord(2, sched.Queued)
	exclusive.Claim = sched.Claim{Exclusive: true}
	m := wqModelAt(t, qvSeed(t, `{"cpus": 5000}`, 0, qvRecord(1, sched.Queued), exclusive))

	v := m.Queue()

	assert.Equal(t, why, v.Paused)
	assert.Equal(t, sched.Policy{}, v.Policy)
	assert.Equal(t, 5000, v.Settings.CPUs, "the block as it was read")
	assert.Equal(t, "admission paused: "+why, jobOf(t, v, 1).Waiting)
	assert.Equal(t, "admission paused: "+why, jobOf(t, v, 2).Waiting, "no pass has spoken of it")
	assert.Equal(t, sched.Need{}, jobOf(t, v, 2).Need)

	m.queue.waiting = map[sched.JobID]string{2: "over this host's caps"}
	v = m.Queue()
	assert.Equal(t, "admission paused: "+why, jobOf(t, v, 1).Waiting)
	assert.Equal(t, "over this host's caps", jobOf(t, v, 2).Waiting, "a reason a pass gave is kept")
}

// An owner resolves by identity to its title, its workspace's label and its
// workspace's ID; one no served workspace holds keeps its ID with none of
// them, and a job without one stays 0. The owner's job is a real one,
// submitted from the session's launch and run to its end.
func TestQueueView_ResolvesTheOwnerByIdentity(t *testing.T) {
	orphan := qvFinished(1, sched.Done, qvEpoch)
	orphan.Owner = 12345
	m := qvLive(t, qvSeed(t, qvBlock, 0, orphan, qvFinished(2, sched.Done, qvEpoch)))
	owner := wqOwnerSession(t, m)
	sub := wqJob(t, "true")
	sub.Launch = "L1"
	id, warning, err := m.submitJob(sub)
	require.NoError(t, err)
	require.Empty(t, warning)
	wqSettle(t, m, id)
	_, ws := m.lookup(owner)
	require.NotNil(t, ws, "fixture: the owner is served")

	v := m.Queue()

	owned := jobOf(t, v, id)
	assert.Equal(t, owner, owned.Owner)
	assert.Equal(t, "owner", owned.OwnerTitle)
	assert.Equal(t, "owners", owned.OwnerWorkspace)
	assert.Equal(t, ws.Label(), owned.OwnerWorkspace)
	assert.Equal(t, m.wsIDOf(ws), owned.OwnerWorkspaceID)
	assert.NotZero(t, owned.OwnerWorkspaceID)
	gone := jobOf(t, v, 1)
	assert.Equal(t, InstanceID(12345), gone.Owner)
	assert.Empty(t, gone.OwnerTitle, "no served session holds it")
	assert.Empty(t, gone.OwnerWorkspace)
	assert.Zero(t, gone.OwnerWorkspaceID)
	plain := jobOf(t, v, 2)
	assert.Zero(t, plain.Owner)
	assert.Zero(t, plain.OwnerWorkspaceID)
}

// Nothing a caller does to a returned view reaches the model's records, and
// Clone shares nothing with its source.
func TestQueueView_HandsOutCopies(t *testing.T) {
	m := wqModelAt(t, qvSeed(t, qvBlock, 0, qvRecord(1, sched.Queued), qvFinished(2, sched.Failed, qvEpoch)))

	v := m.Queue()
	jobOf(t, v, 1).Argv[0] = "edited"
	jobOf(t, v, 2).Exit.Code = 99
	v.Settings.DefaultClaim.CPUs = 99
	again := m.Queue()
	assert.Equal(t, "sleep", jobOf(t, again, 1).Argv[0], "argv")
	assert.Equal(t, 2, jobOf(t, again, 2).Exit.Code, "exit")
	assert.Equal(t, 2, again.Settings.DefaultClaim.CPUs, "default claim")

	c := again.Clone()
	jobOf(t, again, 1).Argv[0] = "edited"
	jobOf(t, again, 2).Exit.Code = 99
	again.Settings.DefaultClaim.CPUs = 99
	assert.Equal(t, "sleep", jobOf(t, c, 1).Argv[0], "clone argv")
	assert.Equal(t, 2, jobOf(t, c, 2).Exit.Code, "clone exit")
	assert.Equal(t, 2, c.Settings.DefaultClaim.CPUs, "clone default claim")
}

func queueEvents(events []Event) []QueueChanged {
	var out []QueueChanged
	for _, ev := range events {
		if q, ok := ev.(QueueChanged); ok {
			out = append(out, q)
		}
	}
	return out
}

// The queue publishes on the first Sync, then only when its revision moves
// or a different queue replaces it, and the snapshot a late client gets
// carries it without taking the next publish away.
func TestPublishQueue_OnceThenOnChange(t *testing.T) {
	m := wqModelAt(t, qvSeed(t, qvBlock, 0, qvFinished(1, sched.Done, qvEpoch), qvFinished(2, sched.Failed, qvEpoch.Add(time.Minute))))

	first := queueEvents(m.Sync().Events)
	require.Len(t, first, 1, "published the first time")
	assert.Equal(t, m.Queue(), first[0].View)
	assert.Empty(t, queueEvents(m.Sync().Events), "nothing changed")

	m.queue.store.Jobs[0].Description = "renamed"
	assert.Empty(t, queueEvents(m.Sync().Events), "a change that bumps no revision is not seen")

	m.queue.rev++
	moved := queueEvents(m.Sync().Events)
	require.Len(t, moved, 1, "a moved revision")
	assert.Equal(t, "renamed", jobOf(t, moved[0].View, m.queue.store.Jobs[0].ID).Description)

	m.queue = &queueState{dir: m.queue.dir, rev: m.queue.rev}
	replaced := queueEvents(m.Sync().Events)
	require.Len(t, replaced, 1, "a different queue at the same revision, as a boot installs one")
	assert.Empty(t, replaced[0].View.Jobs)

	m.queue.rev++
	snap := queueEvents(m.Snapshot())
	require.Len(t, snap, 1, "the snapshot holds the queue")
	assert.Equal(t, m.Queue(), snap[0].View, "as it is now, not as last published")
	assert.Len(t, queueEvents(m.Sync().Events), 1, "and is no publish: the change still goes out at the next Sync")
	assert.Empty(t, queueEvents(m.Sync().Events), "once")
}
```

In `core/view_test.go`, replace:

```go
	require.Len(t, out.Events, 6)
```

with:

```go
	require.Len(t, out.Events, 7)
```

In `core/view_test.go`, replace:

```go
	require.True(t, ok, "and the GitHub state, each published the first time")
	vc, ok := out.Events[4].(ViewsChanged)
	require.True(t, ok, "instance views next")
	assert.Equal(t, m.wsIDOf(ws), vc.WS)
	assert.Equal(t, Notice{Info: "hello"}, out.Events[5])
```

with:

```go
	require.True(t, ok, "and the GitHub state, each published the first time")
	_, ok = out.Events[4].(QueueChanged)
	require.True(t, ok, "then the work queue's")
	vc, ok := out.Events[5].(ViewsChanged)
	require.True(t, ok, "instance views next")
	assert.Equal(t, m.wsIDOf(ws), vc.WS)
	assert.Equal(t, Notice{Info: "hello"}, out.Events[6])
```

In `core/state_views_test.go`, replace:

```go
	require.Len(t, snap, 5)
```

with:

```go
	require.Len(t, snap, 6)
```

In `core/state_views_test.go`, replace:

```go
	assert.IsType(t, GitHubChanged{}, snap[3])
	vc := snap[4].(ViewsChanged)
```

with:

```go
	assert.IsType(t, GitHubChanged{}, snap[3])
	assert.IsType(t, QueueChanged{}, snap[4])
	vc := snap[5].(ViewsChanged)
```

In `core/view_test.go`, replace the helper's doc comment and case list:

```go
// instanceEvents drops the workspace and state events (WorkspacesChanged,
// ModelChanged, AccountsChanged, GitHubChanged) from events: the
// instance-view tests count only what they publish.
func instanceEvents(events []Event) []Event {
	var out []Event
	for _, ev := range events {
		switch ev.(type) {
		case WorkspacesChanged, ModelChanged, AccountsChanged, GitHubChanged:
```

with:

```go
// instanceEvents drops the workspace and state events (WorkspacesChanged,
// ModelChanged, AccountsChanged, GitHubChanged, QueueChanged) from events:
// the instance-view tests count only what they publish.
func instanceEvents(events []Event) []Event {
	var out []Event
	for _, ev := range events {
		switch ev.(type) {
		case WorkspacesChanged, ModelChanged, AccountsChanged, GitHubChanged, QueueChanged:
```

In `core/loop_test.go`, in `TestLoop_SyncAndSnapshotIsOneCall`, replace:

```go
	assert.Equal(t, []string{"WorkspacesChanged", "ModelChanged", "AccountsChanged", "GitHubChanged"}, kinds, "the whole state")
```

with:

```go
	assert.Equal(t, []string{"WorkspacesChanged", "ModelChanged", "AccountsChanged", "GitHubChanged", "QueueChanged"}, kinds, "the whole state")
```

In `core/rpc/wire_test.go`, replace:

```go
	order := []core.Event{core.WorkspacesChanged{}, core.ModelChanged{}, core.AccountsChanged{}, core.GitHubChanged{}, core.ViewsChanged{}}
```

with:

```go
	order := []core.Event{core.WorkspacesChanged{}, core.ModelChanged{}, core.AccountsChanged{}, core.GitHubChanged{}, core.QueueChanged{}, core.ViewsChanged{}}
```

In `core/rpc/wire_test.go`, replace:

```go
	for _, v := range []any{core.InstanceView{}, core.WorkspaceView{}, core.AccountsView{}, core.GitHubView{}} {
```

with:

```go
	for _, v := range []any{core.InstanceView{}, core.WorkspaceView{}, core.AccountsView{}, core.GitHubView{}, core.QueueView{}} {
```

In `app/core_glue_test.go` (imports), replace:

```go
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
```

with:

```go
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/session"
```

Append to the end of the file `app/core_glue_test.go`:

```go
// TestApplyCoreEvent_QueueChangedKeepsTheNewestQueue: the work view reads
// home.queue, which only the QueueChanged applier sets. The test applies the
// events as a drain does, with no syncViews to hide a missing applier. It
// builds the views directly: this package's tests run no job shim, so there
// is no model queue to change under it.
func TestApplyCoreEvent_QueueChangedKeepsTheNewestQueue(t *testing.T) {
	m := newTestHome(t)
	require.Empty(t, m.queue.Jobs, "nothing has been applied yet")

	_ = m.applyCoreEvent(core.QueueChanged{View: core.QueueView{
		Dir:    "/jobs",
		Host:   sched.Host{Name: "h", Cores: 8},
		Paused: "why",
		Jobs:   []core.JobView{{ID: 1, Description: "tests", Argv: []string{"cargo", "test"}, State: sched.Queued}},
	}})
	assert.Equal(t, "/jobs", m.queue.Dir)
	assert.Equal(t, "why", m.queue.Paused)
	require.Len(t, m.queue.Jobs, 1)
	assert.Equal(t, "tests", m.queue.Jobs[0].Description)

	_ = m.applyCoreEvent(core.QueueChanged{View: core.QueueView{Dir: "/jobs", Host: sched.Host{Name: "h", Cores: 8}}})
	assert.Empty(t, m.queue.Jobs, "the newest queue replaces the last")
	assert.Empty(t, m.queue.Paused)
}
```

- [ ] **Step 2: Run them to see them fail**

```
CGO_ENABLED=0 go test ./core/ -run 'TestQueueView_|TestPublishQueue_|TestSync_PublishesChangedWorkspacesFirst|TestSnapshot_IsTheWholeStateAndLeavesTheDiffAlone'
CGO_ENABLED=0 go test ./core/rpc -run 'TestWire_EveryFieldSurvives|TestProtocolReference|TestCoalesceKeys_'
CGO_ENABLED=0 go test ./app -run TestApplyCoreEvent_QueueChangedKeepsTheNewestQueue
```

Each prints `FAIL ... [build failed]`: the first with `m.Queue undefined` and `undefined: QueueChanged`, the second with `undefined: core.QueueChanged` and `undefined: core.QueueView`, the third with `undefined: core.QueueChanged`, `undefined: core.QueueView` and `m.queue undefined`.

- [ ] **Step 3: Implement**

Create `core/queue_view.go`:

```go
package core

import (
	"cmp"
	"path/filepath"
	"slices"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/sched"
)

// QueueView is the work queue as a client sees it (published as
// QueueChanged, and what Core.Queue answers from a replica). It holds no
// job's environment: that crosses the wire only inside SubmitJob.
type QueueView struct {
	Host      sched.Host
	Settings  config.SchedulerSettings // as configured; a 0 field shows the default
	Policy    sched.Policy             // in effect; zero while Paused != ""
	Paused    string                   // why admission is paused; "" when it isn't
	Claimed   sched.Need               // what the running jobs' claims take
	UsedCPU   float64                  // cores the running jobs use, last sampled
	UsedRSSKB int64                    // memory they use, last sampled
	Dir       string                   // <globalDir>/jobs
	Jobs      []JobView                // running (by start), queued (queue order), then finished (newest end first)
}

// JobView is one job of the queue as a client sees it.
type JobView struct {
	ID               sched.JobID
	State            sched.State
	Description      string
	Argv             []string
	Cwd              string
	Owner            InstanceID  // 0: none
	OwnerTitle       string      // "" when the owner isn't served
	OwnerWorkspace   string      // the owner's workspace's WorkspaceView.Label; "" when the owner isn't served
	OwnerWorkspaceID WorkspaceID // the owner's workspace, which `o` opens (openTab); 0 when the owner isn't served
	Claim            sched.Claim
	Need             sched.Need // an exclusive claim's at the current caps
	Submitted        time.Time
	Started          time.Time
	Ended            time.Time
	Exit             *sched.Exit
	Failure          string
	CancelReason     string
	PeakRSSKB        int64
	RSSKB            int64
	CPU              float64
	LogPath          string // JobDir(Dir, ID)/output.log
	Waiting          string
	Notice           sched.Notice
	NoticeWhy        string
}

// Clone is v with its own copy of everything a caller could edit in place:
// the scheduler block's default claim, and each job's argv and exit.
func (v QueueView) Clone() QueueView {
	v.Settings = cloneSchedulerSettings(v.Settings)
	v.Jobs = cloneJobViews(v.Jobs)
	return v
}

func cloneSchedulerSettings(s config.SchedulerSettings) config.SchedulerSettings {
	if s.DefaultClaim != nil {
		c := *s.DefaultClaim
		s.DefaultClaim = &c
	}
	return s
}

func cloneJobViews(jobs []JobView) []JobView {
	out := slices.Clone(jobs)
	for i := range out {
		out[i].Argv = slices.Clone(out[i].Argv)
		if e := out[i].Exit; e != nil {
			c := *e
			out[i].Exit = &c
		}
	}
	return out
}

// Queue is the work queue as it stands now (Core.Queue; a client's replica
// answers it from the last QueueChanged).
func (m *Model) Queue() QueueView { return m.queueView() }

// queueRev is the revision of the queue as last changed.
func (m *Model) queueRev() uint64 { return m.queue.rev }

// queueView builds the published view of m.queue: the running jobs by
// start, the queued ones in queue order, then the finished ones, newest end
// first. It copies everything it holds, and never the environment, which no
// record keeps.
func (m *Model) queueView() QueueView {
	q := m.queue
	v := QueueView{
		Host:     q.host,
		Settings: cloneSchedulerSettings(q.settings),
		Policy:   q.policy,
		Paused:   q.paused,
		Dir:      q.dir,
	}
	var running, queued, finished []sched.Record
	for _, r := range q.store.Jobs {
		switch r.State {
		case sched.Running:
			running = append(running, r)
		case sched.Queued:
			queued = append(queued, r)
		default:
			finished = append(finished, r)
		}
	}
	slices.SortStableFunc(running, func(a, b sched.Record) int {
		return cmp.Or(a.Started.Compare(b.Started), cmp.Compare(a.ID, b.ID))
	})
	slices.SortStableFunc(queued, func(a, b sched.Record) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), cmp.Compare(a.ID, b.ID))
	})
	slices.SortStableFunc(finished, func(a, b sched.Record) int {
		return cmp.Or(b.Ended.Compare(a.Ended), cmp.Compare(b.ID, a.ID))
	})
	for _, r := range slices.Concat(running, queued, finished) {
		jv := m.queueJobView(q, r)
		if r.State == sched.Running {
			v.Claimed.CPUs += jv.Need.CPUs
			v.Claimed.MemoryGB += jv.Need.MemoryGB
			v.Claimed.IOSlots += jv.Need.IOSlots
			v.UsedCPU += jv.CPU
			v.UsedRSSKB += jv.RSSKB
		}
		v.Jobs = append(v.Jobs, jv)
	}
	return v
}

// queueJobView is the view of record r. Only a running job has usage, and
// only a queued one has a reason to wait: a stale entry for a job that has
// moved on is not shown. A queued job the last pass said nothing about,
// while admission is paused, waits for that.
func (m *Model) queueJobView(q *queueState, r sched.Record) JobView {
	jv := JobView{
		ID:           r.ID,
		State:        r.State,
		Description:  r.Description,
		Argv:         slices.Clone(r.Argv),
		Cwd:          r.Cwd,
		Owner:        InstanceID(r.Owner),
		Claim:        r.Claim,
		Need:         r.Claim.Need(q.policy.Caps),
		Submitted:    r.Submitted,
		Started:      r.Started,
		Ended:        r.Ended,
		Failure:      r.Failure,
		CancelReason: r.CancelReason,
		PeakRSSKB:    r.PeakRSSKB,
		LogPath:      filepath.Join(sched.JobDir(q.dir, r.ID), sched.LogFile),
		Notice:       r.Notice,
		NoticeWhy:    r.NoticeWhy,
	}
	if r.Exit != nil {
		e := *r.Exit
		jv.Exit = &e
	}
	if inst, ws := m.lookup(jv.Owner); inst != nil {
		jv.OwnerTitle, jv.OwnerWorkspace, jv.OwnerWorkspaceID = inst.Title, ws.Label(), m.wsIDOf(ws)
	}
	switch r.State {
	case sched.Running:
		u := q.usage[r.ID]
		jv.RSSKB, jv.CPU = u.RSSKB, u.CPU
	case sched.Queued:
		jv.Waiting = q.waiting[r.ID]
		if jv.Waiting == "" && q.paused != "" {
			jv.Waiting = "admission paused: " + q.paused
		}
	}
	return jv
}
```
In `core/model.go`, replace:

```go
	// view unchanged (the same probe failing again).
	usageGen, usagePublishedGen uint64
```

with:

```go
	// view unchanged (the same probe failing again).
	usageGen, usagePublishedGen uint64
	// queuePub is the work queue as last published (publishQueue).
	queuePub queuePublication
```

In `core/events.go`, replace:

```go
func (GitHubChanged) coreEvent() {}
```

with:

```go
func (GitHubChanged) coreEvent() {}

// QueueChanged carries the work queue whenever it changed (a job submitted,
// started, settled or cancelled, the caps edited, the running jobs' usage
// sampled) and on the first Sync: the TUI keeps it for the work view
// (home.queue), and a replica answers Queue from it. The view holds no job's
// environment.
type QueueChanged struct {
	View QueueView
}

func (QueueChanged) coreEvent() {}
```

In `core/events.go`, replace:

```go
		HealthChecked{}, InstancesChanged{}, ModelChanged{}, Notice{},
		Reactivated{}, Recovered{}, Reply{}, SessionLaunched{}, Started{},
```

with:

```go
		HealthChecked{}, InstancesChanged{}, ModelChanged{}, Notice{},
		QueueChanged{}, Reactivated{}, Recovered{}, Reply{}, SessionLaunched{}, Started{},
```

In `core/state_publish.go`, replace:

```go
// publishState returns a ModelChanged, an AccountsChanged and a
// GitHubChanged for each of those views that changed since the last
// publish (always on the first). The model and account views are
```

with:

```go
// publishState returns a ModelChanged, an AccountsChanged, a GitHubChanged
// and a QueueChanged for each of those views that changed since the last
// publish (always on the first). The model and account views are
```

In `core/state_publish.go`, replace:

```go
		events = append(events, GitHubChanged{View: m.githubView()})
	}
	return events
}
```

with:

```go
		events = append(events, GitHubChanged{View: m.githubView()})
	}
	events = append(events, m.publishQueue()...)
	return events
}

// queuePublication is what publishQueue last published: the queue (by
// pointer, since a boot replaces the one the model was built with) and its
// revision.
type queuePublication struct {
	sent bool
	of   *queueState
	rev  uint64
}

// publishQueue returns a QueueChanged when the queue is not the one last
// published or its revision moved since, and on the first call always. The
// revision is queueState.rev, which every change to the queue, to a job or
// to the sampled usage bumps: a change that leaves it alone is not seen
// until something else moves it.
func (m *Model) publishQueue() []Event {
	p := queuePublication{sent: true, of: m.queue, rev: m.queueRev()}
	if m.queuePub == p {
		return nil
	}
	m.queuePub = p
	return []Event{QueueChanged{View: m.queueView()}}
}
```

In `core/state_publish.go`, replace:

```go
// Snapshot is the whole published state as events, in Sync's order: the
// workspace views, the model, account and GitHub views, and every served
```

with:

```go
// Snapshot is the whole published state as events, in Sync's order: the
// workspace views, the model, account, GitHub and queue views, and every served
```

In `core/state_publish.go`, replace:

```go
		GitHubChanged{View: m.githubView()},
	}
	for _, w := range m.workspaces {
```

with:

```go
		GitHubChanged{View: m.githubView()},
		QueueChanged{View: m.queueView()},
	}
	for _, w := range m.workspaces {
```

In `core/rpc/server.go`, replace:

```go
	case core.GitHubChanged:
		return "github"
```

with:

```go
	case core.GitHubChanged:
		return "github"
	case core.QueueChanged:
		return "queue"
```

In `core/rpc/replica.go`, replace:

```go
	github     core.GitHubView
	views      map[core.WorkspaceID][]core.InstanceView
```

with:

```go
	github     core.GitHubView
	queue      core.QueueView
	views      map[core.WorkspaceID][]core.InstanceView
```

In `core/rpc/replica.go`, replace:

```go
	workspaces, model, accounts, github bool
	views                               map[core.WorkspaceID]bool
```

with:

```go
	workspaces, model, accounts, github, queue bool
	views                                      map[core.WorkspaceID]bool
```

In `core/rpc/replica.go`, replace:

```go
	case core.GitHubChanged:
		r.github, changed.github = ev.View, true
```

with:

```go
	case core.GitHubChanged:
		r.github, changed.github = ev.View, true
	case core.QueueChanged:
		r.queue, changed.queue = ev.View, true
```

In `core/rpc/replica.go`, replace:

```go
// order the model publishes them: the workspaces, the model, account and
// GitHub state, then each changed workspace's instance views, in
// workspace order.
```

with:

```go
// order the model publishes them: the workspaces, the model, account,
// GitHub and queue state, then each changed workspace's instance views, in
// workspace order.
```

In `core/rpc/replica.go`, replace:

```go
		out = append(out, core.GitHubChanged{View: r.github.Clone()})
	}
```

with:

```go
		out = append(out, core.GitHubChanged{View: r.github.Clone()})
	}
	if changed.queue {
		out = append(out, core.QueueChanged{View: r.queue.Clone()})
	}
```

In `core/rpc/replica.go`, replace:

```go
func (r *replica) RCAuth() session.RemoteControlAuth { return r.model.RCAuth }
```

with:

```go
func (r *replica) RCAuth() session.RemoteControlAuth { return r.model.RCAuth }

// Queue is the work queue as last published.
func (r *replica) Queue() core.QueueView { return r.queue.Clone() }
```

In `app/app.go`, replace:

```go
	// (publishSelection).
	sentSelected core.InstanceID
```

with:

```go
	// (publishSelection).
	sentSelected core.InstanceID
	// queue is the work queue as last published (core.QueueChanged): what
	// the work view draws. The zero view until the first event.
	queue core.QueueView
```

In `app/core_glue.go`, replace:

```go
	case core.Reply:
		return m.handleReply(ev)
```

with:

```go
	case core.QueueChanged:
		m.queue = ev.View
	case core.Reply:
		return m.handleReply(ev)
```

- [ ] **Step 4: Run them to see them pass**

Rewrite the protocol reference first (its state-event list and events section gain `QueueChanged`), then run the tests of this task and the whole of the packages it touches:

```
CGO_ENABLED=0 go test ./core/rpc -run TestProtocolReference -update
CGO_ENABLED=0 go test ./core/ -run 'TestQueueView_|TestPublishQueue_|TestSync_PublishesChangedWorkspacesFirst|TestSnapshot_IsTheWholeStateAndLeavesTheDiffAlone|TestLoop_SyncAndSnapshotIsOneCall|TestSync_PublishedViewsDoNotAliasTheModel|TestSync_ForgetsClosedWorkspaces'
CGO_ENABLED=0 go test ./core/rpc -run 'TestWire_EveryFieldSurvives|TestProtocolReference|TestCoalesceKeys_'
CGO_ENABLED=0 go test ./app -run TestApplyCoreEvent_QueueChangedKeepsTheNewestQueue
CGO_ENABLED=0 go test ./core/... ./app/...
```

Each prints `ok`. The first run also leaves `docs/specs/protocol.md` changed: `git diff --stat docs/specs/protocol.md` shows it, and the diff holds only a `- QueueChanged` line in the state events (between `GitHubChanged` and `ViewsChanged`) and a `### QueueChanged` section. Two edits are easy to miss: `TestLoop_SyncAndSnapshotIsOneCall` expects the snapshot's kinds, which now end in `QueueChanged`, and the helper `instanceEvents` drops the state events, which now include `QueueChanged`, so that the tests calling it (`TestSync_PublishedViewsDoNotAliasTheModel`, `TestSync_ForgetsClosedWorkspaces`) keep their counts.

- [ ] **Step 5: Commit**

```
git add -N core/queue_view.go core/queue_view_test.go
git commit -m "feat(core,rpc,app): publish the work queue as QueueChanged" -- core/queue_view.go core/queue_view_test.go core/events.go core/state_publish.go core/model.go core/view_test.go core/state_views_test.go core/loop_test.go core/rpc/server.go core/rpc/replica.go core/rpc/wire_test.go app/app.go app/core_glue.go app/core_glue_test.go docs/specs/protocol.md
```

### Task 4.2: The requests: `Queue`, `SubmitJob`, `CancelJob`, `MoveJobToFront`, `NotifyJob`, `SetSchedulerCaps` and `Reply.Job`

**Files:**
- Create: `core/queue_requests.go`, `core/queue_requests_test.go`
- Modify: `core/iface.go`, `core/loop_core.go`, `core/events.go`, `core/events_json.go`
- Modify: `core/rpc/client_test.go`, `core/rpc/wire_test.go`
- Regenerated: `core/rpc/methods_gen.go`, `docs/specs/protocol.md`

Each request runs Story 3's operation (`submitJob`, `cancelJob`, `moveJobToFront`, `notifyJob`, `setSchedulerCaps`) on the loop and answers at once: `m.refuse` for an error (so the Reply's `Err` matches `ErrRefused`, and with `ReqID` 0 it is only logged), else `m.reply`. `SubmitJob` puts the new ID in `Reply.Job` and the operation's warning in `Reply.Notice`. `CancelJob` turns its `CancelFrom` into the reasons `with loom job cancel` and `from the work view` and refuses any other value before it looks at the job. None of these names an instance, so none runs `admit`, and none has a `TestRequests_RefuseWhatTheTUIRefuses` case. `Queue()` is `rpc:local`: the model builds the view, and the replica answers from the last `QueueChanged` (Task 4.1). `Reply.Job` crosses the wire only through `replyJSON`, so it is added there too (`docs/claude/adding-a-core-method.md`, traps). The core tests seed a global dir and hold one real running job that takes every core, so a submitted job waits and no other shim starts; the `core/rpc` tests boot a model whose loop keeps its jobs unrun, so no shim starts at all.

- [ ] **Step 1: Write the failing tests**

Create `core/queue_requests_test.go`. It builds on `core/queue_view_test.go` (`qvSeed`, `qvLive`, `qvRecord`, `qvFinished`, `jobOf`) and on Story 3's `wqModelAt` and `wqLaunch`. Its queue holds one real running job that takes every core, so a job submitted to it waits and none starts:

```go
package core

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// qvBlockedModel is a model over a seeded global dir whose caps are 4 cores
// and 8 GB (so a default claim is 4 cores and 6 GB), and the dir. It holds a
// running job (5, a real shim) that takes all 4 cores, two queued jobs (2 and
// 3) and a finished one (4). The next ID is 6.
func qvBlockedModel(t *testing.T) (*Model, string) {
	t.Helper()
	q2, q3 := qvRecord(2, sched.Queued), qvRecord(3, sched.Queued)
	q2.Claim, q3.Claim = sched.Claim{CPUs: 4, MemoryGB: 6}, sched.Claim{CPUs: 4, MemoryGB: 6}
	dir := qvSeed(t, `{"cpus": 4, "memory_gb": 8}`, 5, q2, q3, qvFinished(4, sched.Done, qvEpoch.Add(time.Minute)))
	m := qvLive(t, dir)
	require.Equal(t, sched.JobID(5), wqLaunch(t, m, "sh", "-c", "sleep 30"), "fixture: the blocker follows the seeded IDs")
	return m, dir
}

// qvSubmit is a submission the model accepts.
func qvSubmit(t *testing.T) sched.Submit {
	t.Helper()
	return sched.Submit{
		Argv:        []string{"cargo", "test"},
		Cwd:         t.TempDir(),
		Env:         []string{"PATH=/usr/bin"},
		Description: "tests",
		Notify:      true,
		Tail:        20,
	}
}

// requestReply is the one Reply to req the model produced.
func requestReply(t *testing.T, m *Model, req ReqID) Reply {
	t.Helper()
	r := replyTo(m.Drain().Events, req)
	require.NotNil(t, r, "a Reply to request %d", req)
	return *r
}

func queueOrder(m *Model) []sched.JobID {
	return jobIDs(m.Queue())
}

// SubmitJob replies with the new ID in Reply.Job and says nothing more for a
// submission from a plain shell; from a launch no served session holds it
// replies with the warning in Reply.Notice, and the job is queued either way.
func TestSubmitJob_RepliesWithTheIDAndTheOwnerWarning(t *testing.T) {
	m, _ := qvBlockedModel(t)

	m.SubmitJob(qvSubmit(t), 3)
	r := requestReply(t, m, 3)
	require.NoError(t, r.Err)
	assert.Equal(t, sched.JobID(6), r.Job)
	assert.NoError(t, r.Notice, "a plain shell has no owner and no warning")

	ghost := qvSubmit(t)
	ghost.Launch = "ghost"
	m.SubmitJob(ghost, 4)
	r = requestReply(t, m, 4)
	require.NoError(t, r.Err)
	assert.Equal(t, sched.JobID(7), r.Job)
	require.Error(t, r.Notice)
	assert.Equal(t, "this shell's loom session (launch ghost) isn't one the daemon serves: job 7 has no owner, so no message will come", r.Notice.Error())

	queued := jobOf(t, m.Queue(), 7)
	assert.Equal(t, sched.Queued, queued.State)
	assert.Zero(t, queued.Owner)
	assert.Equal(t, "needs 4 cores (0 free)", queued.Waiting, "it waits behind the job that takes every core")
}

// The environment is stored for the shim and never published: not in the
// view, so not in anything a client or the web layer is sent.
func TestSubmitJob_NeverPublishesTheEnvironment(t *testing.T) {
	m, _ := qvBlockedModel(t)
	spec := qvSubmit(t)
	spec.Env = append(spec.Env, "LOOM_SECRET_PROBE=hunter2")

	m.SubmitJob(spec, 3)
	r := requestReply(t, m, 3)
	require.NoError(t, r.Err)

	stored, err := sched.ReadSpec(sched.JobDir(m.Queue().Dir, r.Job))
	require.NoError(t, err)
	assert.Contains(t, stored.Env, "LOOM_SECRET_PROBE=hunter2", "the shim gets it")
	for name, v := range map[string]any{"Queue": m.Queue(), "QueueChanged": QueueChanged{View: m.Queue()}} {
		b, err := json.Marshal(v)
		require.NoError(t, err)
		assert.NotContains(t, string(b), "hunter2", name)
	}
}

// A refused submission replies with an error that matches ErrRefused and
// names its bound, adds nothing to the queue, and with ReqID 0 replies to
// no one.
func TestSubmitJob_RefusalsAreReplies(t *testing.T) {
	m, _ := qvBlockedModel(t)
	before := queueOrder(m)

	noDesc := qvSubmit(t)
	noDesc.Description = " "
	overCap := qvSubmit(t)
	overCap.Claim = sched.Claim{CPUs: 99}
	for i, tc := range []struct {
		name string
		spec sched.Submit
		want string
	}{
		{"an empty description", noDesc, "the description is empty"},
		{"a claim over a cap", overCap, "claims 99 cores; this host's cap is 4"},
	} {
		req := ReqID(10 + i)
		m.SubmitJob(tc.spec, req)
		r := requestReply(t, m, req)
		require.Error(t, r.Err, tc.name)
		assert.ErrorIs(t, r.Err, ErrRefused, tc.name)
		assert.Contains(t, r.Err.Error(), tc.want, tc.name)
		assert.Zero(t, r.Job, tc.name)
	}
	m.SubmitJob(noDesc, 0)
	assert.Empty(t, replies(m.Drain()), "no Reply was asked for")
	assert.Equal(t, before, queueOrder(m), "nothing was queued")
}

// While admission is paused, here by a scheduler block with an invalid
// number of cores, a submission is refused with the reason.
func TestSubmitJob_RefusedWhileAdmissionIsPaused(t *testing.T) {
	m := wqModelAt(t, qvSeed(t, `{"cpus": 5000}`, 0))

	m.SubmitJob(qvSubmit(t), 3)

	r := requestReply(t, m, 3)
	assert.ErrorIs(t, r.Err, ErrRefused)
	assert.EqualError(t, r.Err, "admission paused: scheduler.cpus is 5000: it must be 1–1024")
	assert.Empty(t, m.Queue().Jobs)
}

// CancelJob maps its source to the reason the notice words, cancels a
// queued job at once, and refuses an unknown source, a finished job and a
// job the queue doesn't hold.
func TestCancelJob_MapsItsSourceAndRefuses(t *testing.T) {
	m, _ := qvBlockedModel(t)

	m.CancelJob(2, CancelFromView, 3)
	require.NoError(t, requestReply(t, m, 3).Err)
	j := jobOf(t, m.Queue(), 2)
	assert.Equal(t, sched.Cancelled, j.State)
	assert.Equal(t, "from the work view", j.CancelReason)

	m.CancelJob(3, CancelFromCLI, 4)
	require.NoError(t, requestReply(t, m, 4).Err)
	j = jobOf(t, m.Queue(), 3)
	assert.Equal(t, sched.Cancelled, j.State)
	assert.Equal(t, "with loom job cancel", j.CancelReason)

	for _, tc := range []struct {
		name string
		id   sched.JobID
		from CancelFrom
		want string
	}{
		{"an unknown source", 5, CancelFrom("web"), `unknown source "web"`},
		{"an empty source", 5, CancelFrom(""), `unknown source ""`},
		{"a finished job", 4, CancelFromCLI, "job 4 has finished"},
		{"a job the queue doesn't hold", 99, CancelFromView, "there is no job 99"},
	} {
		m.CancelJob(tc.id, tc.from, 20)
		r := requestReply(t, m, 20)
		assert.ErrorIs(t, r.Err, ErrRefused, tc.name)
		require.Error(t, r.Err, tc.name)
		assert.Contains(t, r.Err.Error(), tc.want, tc.name)
	}
	running := jobOf(t, m.Queue(), 5)
	assert.Equal(t, sched.Running, running.State, "the refused cancels touched nothing")
	assert.Empty(t, running.CancelReason)
}

// MoveJobToFront puts a queued job first among the queued ones, and refuses
// a job that isn't waiting and one the queue doesn't hold.
func TestMoveJobToFront_MovesAndRefuses(t *testing.T) {
	m, _ := qvBlockedModel(t)
	require.Equal(t, []sched.JobID{5, 2, 3, 4}, queueOrder(m))

	m.MoveJobToFront(3, 3)
	require.NoError(t, requestReply(t, m, 3).Err)
	assert.Equal(t, []sched.JobID{5, 3, 2, 4}, queueOrder(m))

	m.MoveJobToFront(5, 4)
	r := requestReply(t, m, 4)
	assert.ErrorIs(t, r.Err, ErrRefused)
	assert.EqualError(t, r.Err, "job 5 isn't waiting")
	m.MoveJobToFront(99, 5)
	assert.EqualError(t, requestReply(t, m, 5).Err, "there is no job 99")
	assert.Equal(t, []sched.JobID{5, 3, 2, 4}, queueOrder(m))
}

// NotifyJob refuses a job with no owner and one that has ended, saying which.
func TestNotifyJob_RefusesWhatCannotBeNotified(t *testing.T) {
	m, _ := qvBlockedModel(t)

	for i, tc := range []struct {
		id   sched.JobID
		want string
	}{
		{2, "job 2 has no owner"},
		{4, "job 4 has finished"},
		{99, "there is no job 99"},
	} {
		req := ReqID(3 + i)
		m.NotifyJob(tc.id, req)
		r := requestReply(t, m, req)
		assert.ErrorIs(t, r.Err, ErrRefused)
		assert.EqualError(t, r.Err, tc.want)
	}
}

// SetSchedulerCaps applies a valid block at once (the view shows it and the
// caps in effect) and writes it to the global config; an invalid one is
// refused with the bound it broke and changes neither. The raised caps leave
// the queued jobs waiting, so no shim starts.
func TestSetSchedulerCaps_AppliesOrRefuses(t *testing.T) {
	m, dir := qvBlockedModel(t)

	m.SetSchedulerCaps(config.SchedulerSettings{CPUs: -1}, 3)
	r := requestReply(t, m, 3)
	assert.ErrorIs(t, r.Err, ErrRefused)
	assert.Contains(t, r.Err.Error(), "scheduler.cpus is -1")
	assert.Equal(t, 4, config.LoadConfigFrom(dir).GetScheduler().CPUs, "nothing was written")
	assert.Equal(t, 4, m.Queue().Settings.CPUs)

	m.SetSchedulerCaps(config.SchedulerSettings{CPUs: 6, MemoryGB: 8}, 4)
	require.NoError(t, requestReply(t, m, 4).Err)
	v := m.Queue()
	assert.Equal(t, 6, v.Settings.CPUs)
	assert.Equal(t, 6, v.Policy.Caps.CPUs, "the pass it ran applied it")
	assert.Equal(t, 8, v.Policy.Caps.MemoryGB)
	assert.Equal(t, 6, config.LoadConfigFrom(dir).GetScheduler().CPUs, "written to the global config")
	assert.Equal(t, sched.Queued, jobOf(t, v, 2).State, "a 4-core job still does not fit beside the running one")
}
```

In `core/rpc/wire_test.go`, replace:

```go
		core.Reply{Req: 2, ID: 3, Err: errors.New("kill x: worktree locked")},
		core.ViewsChanged{WS: 1, Views: []core.InstanceView{{
```

with:

```go
		core.Reply{Req: 2, ID: 3, Err: errors.New("kill x: worktree locked")},
		core.Reply{Req: 3, Job: 14, Notice: errors.New("job 14 has no owner")},
		core.ViewsChanged{WS: 1, Views: []core.InstanceView{{
```

In `core/rpc/client_test.go` (imports), replace:

```go
	"os"
	"path/filepath"
	"sync/atomic"
```

with:

```go
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
```

In `core/rpc/client_test.go` (imports), replace:

```go
	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
```

with:

```go
	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/session"
```

Append to the end of the file `core/rpc/client_test.go`. This package's `TestMain` does not run the test shim, and `core.StartForTest` keeps every job the model queues for the test to run, so no shim ever starts here: the fixture's queued job claims more cores than the cap, and a job the tests submit is admitted, its start job held and never run:

```go
// queueModel is a booted model whose global dir holds a scheduler block
// (8 cores, 16 GB, a default claim of 2 cores and 4 GB) and a queue written
// by sched.SaveStore: a queued job (2) whose claim is over the cap, so it
// never starts, and a finished one (3). The next ID is 4.
func queueModel(t *testing.T) *core.Model {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(config.EnvGlobalDir, dir)
	block := `{"scheduler": {"cpus": 8, "memory_gb": 16, "io_jobs": 2, "default_claim": {"cpus": 2, "memory_gb": 4}}}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, config.ConfigFileName), []byte(block), 0o644))
	t0 := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	job := func(id sched.JobID, state sched.State, claim sched.Claim) sched.Record {
		return sched.Record{
			ID: id, Order: int64(id), Description: fmt.Sprintf("job %d", id),
			Argv: []string{"sleep", "1"}, Cwd: "/work", Claim: claim, Tail: 20,
			State: state, Submitted: t0.Add(time.Duration(id) * time.Second),
		}
	}
	over := job(2, sched.Queued, sched.Claim{CPUs: 99, MemoryGB: 4})
	done := job(3, sched.Done, sched.Claim{CPUs: 2, MemoryGB: 4})
	done.Started, done.Ended = t0, t0.Add(time.Minute)
	done.Exit = &sched.Exit{Code: 0, Ended: done.Ended, MaxRSSKB: 2048}
	jobsDir := filepath.Join(dir, "jobs")
	for _, r := range []sched.Record{over, done} {
		require.NoError(t, sched.WriteSpec(sched.JobDir(jobsDir, r.ID), sched.Spec{ID: r.ID, Argv: r.Argv, Cwd: r.Cwd}))
	}
	require.NoError(t, sched.SaveStore(jobsDir, sched.Store{Version: sched.StoreVersion, NextID: 4, Jobs: []sched.Record{over, done}}))
	noTmux := cmd_test.MockCmdExec{
		RunFunc:    func(*exec.Cmd) error { return &exec.ExitError{} },
		OutputFunc: func(*exec.Cmd) ([]byte, error) { return nil, nil },
	}
	model := core.NewForTest(core.Options{CmdExec: noTmux})
	model.Boot()
	return model
}

// TestReplica_QueueAnswersAsTheModel: Queue is an rpc:local query, so the
// client answers it from the last QueueChanged, and gives what the model
// gives, the scheduler block's default claim and the jobs included.
func TestReplica_QueueAnswersAsTheModel(t *testing.T) {
	loop := core.StartForTest(queueModel(t))
	c := pair(t, loop)

	require.Len(t, loop.Queue().Jobs, 2, "fixture: the queue's jobs are in the view")
	require.NotNil(t, loop.Queue().Settings.DefaultClaim, "fixture: and the scheduler block's default claim")
	assert.Equal(t, normalize(loop.Queue()), normalize(c.Queue()))
}

// TestQueueRequests_ThroughTheClient: the work queue's requests over the
// wire. A submission's Reply carries the new ID and the owner warning, and
// its effect is in the replica when it returns, with no trace of the
// environment; each refusal matches ErrRefused with its text; a cancel's
// source reaches the job's reason.
func TestQueueRequests_ThroughTheClient(t *testing.T) {
	loop := core.StartForTest(queueModel(t))
	c := pair(t, loop)
	c.Sync() // the snapshot

	replyTo := func(req core.ReqID) core.Reply {
		t.Helper()
		require.NoError(t, c.FlushForTest())
		for _, ev := range c.Sync() {
			if r, ok := ev.(core.Reply); ok && r.Req == req {
				return r
			}
		}
		require.Failf(t, "no Reply", "request %d", req)
		return core.Reply{}
	}
	job := func(id sched.JobID) (core.JobView, bool) {
		for _, j := range c.Queue().Jobs {
			if j.ID == id {
				return j, true
			}
		}
		return core.JobView{}, false
	}

	c.SubmitJob(sched.Submit{
		Argv: []string{"cargo", "test"}, Cwd: t.TempDir(), Env: []string{"LOOM_WIRE_SECRET=hunter2"},
		Description: "tests", Notify: true, Tail: 20, Launch: "ghost",
	}, 11)
	r := replyTo(11)
	require.NoError(t, r.Err)
	assert.Equal(t, sched.JobID(4), r.Job, "the new job's ID crosses the wire")
	require.Error(t, r.Notice)
	assert.Equal(t, "this shell's loom session (launch ghost) isn't one the daemon serves: job 4 has no owner, so no message will come", r.Notice.Error())
	j, ok := job(4)
	require.True(t, ok, "the replica holds the job when the request returns")
	assert.Equal(t, sched.Running, j.State, "it fits, so the pass that follows the submit admits it; its start job is held")
	assert.Equal(t, "tests", j.Description)
	b, err := json.Marshal(c.Queue())
	require.NoError(t, err)
	assert.NotContains(t, string(b), "hunter2", "the environment is in no published view")

	c.CancelJob(4, core.CancelFromView, 12)
	require.NoError(t, replyTo(12).Err)
	j, _ = job(4)
	assert.Equal(t, "from the work view", j.CancelReason, "the cancel's source reached the job")

	for _, tc := range []struct {
		name string
		ask  func(req core.ReqID)
		want string
	}{
		{"a cancel of a finished job", func(req core.ReqID) { c.CancelJob(3, core.CancelFromCLI, req) }, "job 3 has finished"},
		{"a cancel from an unknown source", func(req core.ReqID) { c.CancelJob(2, core.CancelFrom("web"), req) }, `cancel job 2: unknown source "web"`},
		{"a move of a job that isn't waiting", func(req core.ReqID) { c.MoveJobToFront(4, req) }, "job 4 isn't waiting"},
		{"a move of a job that doesn't exist", func(req core.ReqID) { c.MoveJobToFront(99, req) }, "there is no job 99"},
		{"a notice for a job with no owner", func(req core.ReqID) { c.NotifyJob(2, req) }, "job 2 has no owner"},
		{"caps outside the bounds", func(req core.ReqID) { c.SetSchedulerCaps(config.SchedulerSettings{CPUs: -1}, req) }, "scheduler.cpus is -1"},
	} {
		tc.ask(20)
		r := replyTo(20)
		assert.ErrorIs(t, r.Err, core.ErrRefused, tc.name)
		require.Error(t, r.Err, tc.name)
		assert.Contains(t, r.Err.Error(), tc.want, tc.name)
	}
	j, _ = job(2)
	assert.Equal(t, sched.Queued, j.State, "the refused requests changed nothing")
}
```

- [ ] **Step 2: Run them to see them fail**

```
CGO_ENABLED=0 go test ./core/ -run 'TestSubmitJob_|TestCancelJob_|TestMoveJobToFront_|TestNotifyJob_|TestSetSchedulerCaps_'
CGO_ENABLED=0 go test ./core/rpc -run 'TestQueueRequests_ThroughTheClient|TestReplica_QueueAnswersAsTheModel|TestEvents_RoundTrip'
```

Each prints `FAIL ... [build failed]`: the first with `m.SubmitJob undefined`, `undefined: CancelFrom` and `unknown field Job in struct literal of type Reply`; the second with `unknown field Job in struct literal of type core.Reply`, `c.SubmitJob undefined` and `loop.Queue undefined`.

- [ ] **Step 3: Implement**

In `core/iface.go` (imports), replace:

```go
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
```

with:

```go
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/session"
```

In `core/iface.go`, replace:

```go
	RevokeDevice(name string) error
}
```

with:

```go
	RevokeDevice(name string) error

	// The work queue (package sched, owned by the model): its published
	// state, which a replica answers, and the requests that change it. Each
	// request is answered at once, with its refusal when it is refused.
	// SubmitJob's Reply carries the new job's ID (Job) and, in Notice, the
	// warning for a submission from a launch no served session holds.
	Queue() QueueView // rpc:local
	SubmitJob(spec sched.Submit, req ReqID)
	CancelJob(id sched.JobID, from CancelFrom, req ReqID)
	MoveJobToFront(id sched.JobID, req ReqID)
	NotifyJob(id sched.JobID, req ReqID)
	SetSchedulerCaps(s config.SchedulerSettings, req ReqID)
}
```

In `core/loop_core.go` (imports), replace:

```go
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
```

with:

```go
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/session"
```

Append to the end of the file `core/loop_core.go`:

```go
// Queue runs Model.Queue on the loop.
func (l *Loop) Queue() QueueView { return get(l, (*Model).Queue) }

// SubmitJob runs Model.SubmitJob on the loop.
func (l *Loop) SubmitJob(spec sched.Submit, req ReqID) {
	l.do(func(m *Model) { m.SubmitJob(spec, req) })
}

// CancelJob runs Model.CancelJob on the loop.
func (l *Loop) CancelJob(id sched.JobID, from CancelFrom, req ReqID) {
	l.do(func(m *Model) { m.CancelJob(id, from, req) })
}

// MoveJobToFront runs Model.MoveJobToFront on the loop.
func (l *Loop) MoveJobToFront(id sched.JobID, req ReqID) {
	l.do(func(m *Model) { m.MoveJobToFront(id, req) })
}

// NotifyJob runs Model.NotifyJob on the loop.
func (l *Loop) NotifyJob(id sched.JobID, req ReqID) {
	l.do(func(m *Model) { m.NotifyJob(id, req) })
}

// SetSchedulerCaps runs Model.SetSchedulerCaps on the loop.
func (l *Loop) SetSchedulerCaps(s config.SchedulerSettings, req ReqID) {
	l.do(func(m *Model) { m.SetSchedulerCaps(s, req) })
}
```

Create `core/queue_requests.go`:

```go
package core

import (
	"errors"
	"fmt"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/sched"
)

// CancelFrom says where a CancelJob came from, for the wording of the
// cancel in the job's notice and the follower's line.
type CancelFrom string

const (
	// CancelFromCLI is a cancel made with loom job cancel.
	CancelFromCLI CancelFrom = "cli"
	// CancelFromView is a cancel made in the TUI's work view.
	CancelFromView CancelFrom = "view"
)

// cancelReasons is what follows "was cancelled" for each source.
var cancelReasons = map[CancelFrom]string{
	CancelFromCLI:  "with loom job cancel",
	CancelFromView: "from the work view",
}

// The work queue's requests: each runs one of the queue's operations
// (queue_ops.go) on the loop and answers at once, with the refusal when the
// operation refused. Every refusal matches ErrRefused. A request with
// ReqID 0 asks for no Reply and a refusal is only logged.

// SubmitJob adds a job to the queue. The Reply's Job is the new job's ID,
// and its Notice the operation's warning, if any: the submitter's launch is
// one no served session holds, so the job has no owner.
func (m *Model) SubmitJob(spec sched.Submit, req ReqID) {
	id, warning, err := m.submitJob(spec)
	if err != nil {
		m.refuse(req, 0, err)
		return
	}
	r := Reply{Job: id}
	if warning != "" {
		r.Notice = errors.New(warning)
	}
	m.reply(req, r)
}

// CancelJob cancels a queued or running job. from names the source, which
// words the cancel for the notice (cancelReasons); any other value is
// refused before the job is looked at.
func (m *Model) CancelJob(id sched.JobID, from CancelFrom, req ReqID) {
	reason, ok := cancelReasons[from]
	if !ok {
		m.refuse(req, 0, fmt.Errorf("cancel job %d: unknown source %q", id, from))
		return
	}
	m.answerQueueRequest(req, m.cancelJob(id, reason))
}

// MoveJobToFront puts a queued job first in the queue.
func (m *Model) MoveJobToFront(id sched.JobID, req ReqID) {
	m.answerQueueRequest(req, m.moveJobToFront(id))
}

// NotifyJob asks for a message to the job's owner when it ends.
func (m *Model) NotifyJob(id sched.JobID, req ReqID) {
	m.answerQueueRequest(req, m.notifyJob(id))
}

// SetSchedulerCaps validates s, writes it as the global config's scheduler
// block and runs an admission pass.
func (m *Model) SetSchedulerCaps(s config.SchedulerSettings, req ReqID) {
	m.answerQueueRequest(req, m.setSchedulerCaps(s))
}

// answerQueueRequest replies to a request the model answers at once: with
// the refusal when err is set, else with a success.
func (m *Model) answerQueueRequest(req ReqID, err error) {
	if err != nil {
		m.refuse(req, 0, err)
		return
	}
	m.reply(req, Reply{})
}
```

In `core/events.go` (imports), replace:

```go
	"fmt"

	"github.com/aidan-bailey/loom/session/buildsweep"
```

with:

```go
	"fmt"

	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/session/buildsweep"
```

In `core/events.go`, replace:

```go
// ReadDiff's patch and Sweep SweepBuilds' report. The notices the TUI
```

with:

```go
// ReadDiff's patch and Sweep SweepBuilds' report. Job is SubmitJob's new job
// (0 for every other request), whose Notice is the owner warning. The notices the TUI
```

In `core/events.go`, replace:

```go
	Sweep  buildsweep.Report
}

func (Reply) coreEvent() {}
```

with:

```go
	Sweep  buildsweep.Report
	Job    sched.JobID
}

func (Reply) coreEvent() {}
```

In `core/events_json.go` (imports), replace:

```go
	"encoding/json"

	"github.com/aidan-bailey/loom/session/buildsweep"
```

with:

```go
	"encoding/json"

	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/session/buildsweep"
```

In `core/events_json.go`, replace:

```go
	Sweep  buildsweep.Report
}

// MarshalJSON encodes r with its errors as WireErrors.
```

with:

```go
	Sweep  buildsweep.Report
	Job    sched.JobID
}

// MarshalJSON encodes r with its errors as WireErrors.
```

In `core/events_json.go`, replace:

```go
Diff: r.Diff, Sweep: r.Sweep})
}
```

with:

```go
Diff: r.Diff, Sweep: r.Sweep, Job: r.Job})
}
```

In `core/events_json.go`, replace:

```go
Diff: j.Diff, Sweep: j.Sweep}
	return nil
```

with:

```go
Diff: j.Diff, Sweep: j.Sweep, Job: j.Job}
	return nil
```

Then regenerate the wire from `core/iface.go`. `go generate ./core/rpc` rewrites `core/rpc/methods_gen.go` (never edit it by hand): its table gains `Queue` (local) and the five requests, with their `Params` and `Result` types, and `*rpc.Client` gains the methods. `rpc.Protocol` stays 2.

```
CGO_ENABLED=0 go generate ./core/rpc
CGO_ENABLED=0 go test ./core/rpc -run TestProtocolReference -update
```

`git diff --stat core/rpc/methods_gen.go docs/specs/protocol.md` shows both rewritten; the protocol diff holds the six methods, and `Job` in the `Reply` example.

- [ ] **Step 4: Run them to see them pass**

```
CGO_ENABLED=0 go test ./core/ -run 'TestSubmitJob_|TestCancelJob_|TestMoveJobToFront_|TestNotifyJob_|TestSetSchedulerCaps_|TestLoopForwardsEachMethodToItsNamesake|TestCoreIsValueTyped'
CGO_ENABLED=0 go test ./core/rpc -run 'TestQueueRequests_ThroughTheClient|TestReplica_QueueAnswersAsTheModel|TestEvents_RoundTrip|TestWire_EveryFieldSurvives|TestGenerated_IsFresh|TestMethods_CoverCore|TestProtocolReference'
CGO_ENABLED=0 go test ./core/... ./app/...
```

Each prints `ok`.

- [ ] **Step 5: Commit**

```
git add -N core/queue_requests.go core/queue_requests_test.go
git commit -m "feat(core,rpc): the work queue's requests, Queue and Reply.Job" -- core/queue_requests.go core/queue_requests_test.go core/iface.go core/loop_core.go core/events.go core/events_json.go core/rpc/client_test.go core/rpc/wire_test.go core/rpc/methods_gen.go docs/specs/protocol.md
```

## Story 5: `loom job`, the Claude context paragraph, and the restart e2e

- **Developer:** intermediate-dev
- **Files:** `internal/cli/job.go`, `internal/cli/job_unix.go`, `internal/cli/job_windows.go`, `internal/cli/job_test.go`, `internal/cli/daemon_test.go`, `internal/cli/testmain_test.go`, `internal/cli/README.md`, `internal/cli/CLAUDE.md`, `main.go`, `session/claude-loom-context.md`, `session/claude-loom-context-workspace.md`, `e2e/job_test.go`, `e2e/CLAUDE.md`
- **Interfaces:** exposes `cli.JobCmd` and the CLI texts of the names contract (cli); uses Story 4's `Core` methods, `QueueView`, `Reply.Job` and `QueueChanged`, Story 3's `Model.SetJobRunnerForTest`, Story 2's `MaybeRunTestShim`, `TestRunner` and `ErrUnsupported`, and Story 1's `JobDir` and `ReadStarted`
- **Verify:** `CGO_ENABLED=0 go test ./internal/cli/... ./session/... && CGO_ENABLED=0 go test -tags e2e ./e2e/... -run Job`
- **Review:** deep

The CLI half of the work queue: `loom job` is a daemon client joined through `cli.Join`, like `loom session`. `submit` captures the argv, the working directory, the environment and `$LOOM_LAUNCH`, sends them with `SubmitJob`, and prints the queue line once the replica shows the job. `run` and `wait` follow a job from the replica and `output.log` (polled every 200 ms), write the contract's `loom: ` lines to stderr, exit with spec §6.3's codes, and send `NotifyJob` when interrupted. `logs`, `list` and `cancel` complete the set. The story also adds the paragraph of spec §8 to both context files, and an e2e test that restarts the daemon while a job runs. The code holds the landmine marker `os.Environ()`: `submission` sends `os.Environ()` as the job's environment, as data, and builds no git command or `exec.Cmd` from it, so the supervisor should dispatch its developer with `effort: xhigh`. Exit codes need one name of the story's own: `cli.ExitError`, which `main` turns into the process's exit code (so `main.go` gets two small edits, a registration line in Task 5.1 and the error handling in Task 5.2, each anchored on a line Story 2's `jobShimCmd` registration does not change). A job killed by signal N exits 128 + N (spec §6.3): the follower maps `Exit.Signal` back to N with `unix.SignalNum` in `job_unix.go`, built off Windows, and `job_windows.go` is its stub; on Windows every `loom job` command answers `sched.ErrUnsupported`. The tests start real shims by running the cli test binary as the shim (`sched.MaybeRunTestShim` first in `TestMain`, and `serveDaemonOn` calling `model.SetJobRunnerForTest(sched.TestRunner())` between `core.New` and `Boot`, which Task 5.1 adds to `daemon_test.go`), so each real job settles on the daemon's 3 s queue gate and the job tests take about half a minute together.

### Task 5.1: `loom job submit`, `list` and `cancel`

**Files:**
- Modify: `internal/cli/testmain_test.go:3-11`, `internal/cli/testmain_test.go:22-23`
- Modify: `internal/cli/daemon_test.go:3-19`, `internal/cli/daemon_test.go:87-88`
- Create: `internal/cli/job.go`
- Test: `internal/cli/job_test.go`
- Modify: `main.go:429` (Story 2's `jobShimCmd` line moves it down by one)

- [ ] **Step 1: Write the failing tests**

In `internal/cli/testmain_test.go`, the test binary must be able to act as a job's shim. Replace the import lines

```go
	"github.com/aidan-bailey/loom/internal/testenv"
	"github.com/aidan-bailey/loom/session/tmux"
```

with

```go
	"github.com/aidan-bailey/loom/internal/testenv"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/session/tmux"
```

and replace the first two lines of `TestMain`'s body

```go
func TestMain(m *testing.M) {
	cleanup := testenv.MustIsolateLoomDirs()
```

with

```go
func TestMain(m *testing.M) {
	// A job's runner here is this test binary (sched.TestRunner): when the
	// daemon starts it as a shim, hand over to the shim before anything else.
	sched.MaybeRunTestShim()
	cleanup := testenv.MustIsolateLoomDirs()
```

In `internal/cli/daemon_test.go`, the test daemon starts jobs through this test binary. Replace the import lines

```go
	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/session/tmux"
```

with

```go
	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/session/tmux"
```

and in `serveDaemonOn`, replace the lines

```go
	model := core.New(core.Options{Registry: reg, Program: program, CmdExec: ex})
	model.Boot()
```

with

```go
	model := core.New(core.Options{Registry: reg, Program: program, CmdExec: ex})
	// A job here is a real process group: its runner is this test binary
	// (sched.TestRunner), installed before Boot, which adopts and starts jobs.
	model.SetJobRunnerForTest(sched.TestRunner())
	model.Boot()
```

Create `internal/cli/job_test.go`:

```go
package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/session/claudemod"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runJob runs `loom job args…` on a fresh command tree (newJobCmd), so no
// flag carries over from another test.
func runJob(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := newJobCmd()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetIn(strings.NewReader(""))
	root.SetArgs(args)
	err = root.Execute()
	return out.String(), errOut.String(), err
}

// jobs is the daemon's jobs as the test's own client has them now.
func (d *testDaemon) jobs() []core.JobView {
	if d.client.FlushForTest() != nil {
		return nil
	}
	d.client.Sync()
	return d.client.Queue().Jobs
}

// job is the daemon's view of job id, or the zero view when it serves none.
func (d *testDaemon) job(id sched.JobID) core.JobView {
	for _, j := range d.jobs() {
		if j.ID == id {
			return j
		}
	}
	return core.JobView{}
}

// waitFinished waits until job id has ended, which the daemon notices when
// its queue gate next runs (every 3 s), and returns its view.
func (d *testDaemon) waitFinished(t *testing.T, id sched.JobID) core.JobView {
	t.Helper()
	var v core.JobView
	require.Eventually(t, func() bool {
		v = d.job(id)
		return v.State.Finished()
	}, 30*time.Second, 100*time.Millisecond, "job %d never ended", id)
	return v
}

// waitLog waits until job id's output holds text: the command ran, so its
// runner holds the lock a cancel needs.
func (d *testDaemon) waitLog(t *testing.T, id sched.JobID, text string) {
	t.Helper()
	require.Eventually(t, func() bool {
		path := d.job(id).LogPath
		if path == "" {
			return false
		}
		b, err := os.ReadFile(path)
		return err == nil && strings.Contains(string(b), text)
	}, 30*time.Second, 50*time.Millisecond, "job %d never printed %q", id, text)
}

// parsedSubmit parses a submit command line's flags the way cobra does and
// returns them with the command and the arguments left over.
func parsedSubmit(t *testing.T, line ...string) (*claimFlags, *cobra.Command, []string) {
	t.Helper()
	f := &claimFlags{}
	c := &cobra.Command{Use: "submit"}
	f.add(c)
	require.NoError(t, c.Flags().Parse(line))
	return f, c, c.Flags().Args()
}

func TestJobShellWord(t *testing.T) {
	for _, tc := range []struct {
		in, want string
	}{
		{"cargo", "cargo"},
		{"--release", "--release"},
		{"a/b-c.d=1,2:3@x%y+z_w", "a/b-c.d=1,2:3@x%y+z_w"},
		{"", "''"},
		{"two words", "'two words'"},
		{"it's", `'it'\''s'`},
		{"$HOME", "'$HOME'"},
		{"a;b", "'a;b'"},
		{"é", "'é'"},
	} {
		assert.Equal(t, tc.want, shellWord(tc.in), "%q", tc.in)
	}
}

func TestJobDefaultDescription(t *testing.T) {
	assert.Equal(t, "cargo test -p sched 'two words'", defaultDescription([]string{"cargo", "test", "-p", "sched", "two words"}))

	long := defaultDescription([]string{"echo", strings.Repeat("é", 100)})
	assert.Equal(t, 60, len([]rune(long)), "cut to 60 characters, not 60 bytes")
	assert.True(t, strings.HasPrefix(long, "echo 'éé"), "the cut keeps whole characters: %q", long)
}

func TestJobOrdinal(t *testing.T) {
	for n, want := range map[int]string{
		1: "1st", 2: "2nd", 3: "3rd", 4: "4th", 10: "10th", 11: "11th", 12: "12th", 13: "13th",
		21: "21st", 22: "22nd", 23: "23rd", 101: "101st", 111: "111th", 112: "112th",
	} {
		assert.Equal(t, want, ordinal(n), "%d", n)
	}
}

func TestJobParseID(t *testing.T) {
	id, err := parseJobID("14")
	require.NoError(t, err)
	assert.Equal(t, sched.JobID(14), id)

	for _, bad := range []string{"", "0", "-3", "x", "1.5", "12abc"} {
		_, err := parseJobID(bad)
		require.Error(t, err, "%q", bad)
		assert.Contains(t, err.Error(), "is not a job ID", "%q", bad)
	}
}

func TestJobCheckPlatform(t *testing.T) {
	err := checkPlatform("windows")
	require.ErrorIs(t, err, sched.ErrUnsupported)
	assert.Equal(t, "the work queue isn't available on Windows", err.Error())
	assert.NoError(t, checkPlatform("linux"))
	assert.NoError(t, checkPlatform("darwin"))
}

func TestJobQueuePlace(t *testing.T) {
	q := core.QueueView{Jobs: []core.JobView{
		{ID: 1, State: sched.Running},
		{ID: 2, State: sched.Running},
		{ID: 3, State: sched.Queued},
		{ID: 4, State: sched.Queued},
		{ID: 5, State: sched.Done},
	}}

	n, running := queuePlace(q, 4)
	assert.Equal(t, 2, n, "the second queued job")
	assert.Equal(t, 2, running)

	n, _ = queuePlace(q, 3)
	assert.Equal(t, 1, n)

	n, running = queuePlace(q, 1)
	assert.Zero(t, n, "a running job is in no line")
	assert.Equal(t, 2, running)
}

func TestJobSubmission_CapturesTheShellItRunsIn(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("LOOMJOB_TEST", "bar")
	t.Setenv(claudemod.EnvLaunch, "L1")
	f, c, args := parsedSubmit(t, "--cpus", "8", "--mem", "12", "--io", "--", "cargo", "test", "--release")

	sub, err := f.submission(c, args, true, 20)

	require.NoError(t, err)
	assert.Equal(t, []string{"cargo", "test", "--release"}, sub.Argv)
	wd, err := os.Getwd()
	require.NoError(t, err)
	assert.Equal(t, wd, sub.Cwd)
	assert.Contains(t, sub.Env, "LOOMJOB_TEST=bar", "the shell's environment travels with the job")
	assert.Equal(t, "L1", sub.Launch)
	assert.Equal(t, "cargo test --release", sub.Description, "the command line, when -d is not given")
	assert.Equal(t, sched.Claim{CPUs: 8, MemoryGB: 12, IO: true}, sub.Claim)
	assert.True(t, sub.Notify)
	assert.Equal(t, 20, sub.Tail)
}

func TestJobSubmission_TheDescription(t *testing.T) {
	f, c, args := parsedSubmit(t, "-d", "polyoxide tests", "--", "cargo", "test")
	sub, err := f.submission(c, args, true, 20)
	require.NoError(t, err)
	assert.Equal(t, "polyoxide tests", sub.Description)

	// An empty -d is sent as it is: the daemon refuses it, saying so.
	f, c, args = parsedSubmit(t, "-d", "", "--", "cargo", "test")
	sub, err = f.submission(c, args, true, 20)
	require.NoError(t, err)
	assert.Equal(t, "", sub.Description)
}

func TestJobSubmission_TheClaim(t *testing.T) {
	f, c, args := parsedSubmit(t, "--exclusive", "--", "bench")
	sub, err := f.submission(c, args, true, 20)
	require.NoError(t, err)
	assert.Equal(t, sched.Claim{Exclusive: true}, sub.Claim)

	f, c, args = parsedSubmit(t, "--", "true")
	sub, err = f.submission(c, args, false, 20)
	require.NoError(t, err)
	assert.Equal(t, sched.Claim{}, sub.Claim, "no flags: the daemon applies the default claim")
	assert.False(t, sub.Notify)

	// On the wire a 0 means the default claim, so an explicit 0 cannot be
	// sent: it is refused here, with the daemon's wording.
	for _, tc := range []struct {
		flag, value, want string
	}{
		{"--cpus", "0", "--cpus is 0: it must be at least 1"},
		{"--cpus", "-4", "--cpus is -4: it must be at least 1"},
		{"--mem", "0", "--mem is 0: it must be at least 1"},
	} {
		f, c, args = parsedSubmit(t, tc.flag, tc.value, "--", "true")
		_, err = f.submission(c, args, true, 20)
		require.Error(t, err, tc.flag+" "+tc.value)
		assert.Equal(t, tc.want, err.Error())
	}
}

func TestJobSubmission_TheCommandComesAfterTheDash(t *testing.T) {
	for _, line := range [][]string{
		{"cargo", "test"},
		{"-d", "x"},
		{"--"},
		{"cargo", "--", "test"},
	} {
		f, c, args := parsedSubmit(t, line...)
		_, err := f.submission(c, args, true, 20)
		require.Error(t, err, "%q", line)
		assert.Contains(t, err.Error(), "put the command after --", "%q", line)
	}
}

func TestJobList_ShowsEachJobsStateOwnerClaimAndWait(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	q := core.QueueView{Jobs: []core.JobView{
		{ID: 3, State: sched.Running, Description: "tests", Owner: 7, OwnerTitle: "alpha", Claim: sched.Claim{CPUs: 4, MemoryGB: 6}, Submitted: now.Add(-5 * time.Minute), Started: now.Add(-3 * time.Minute)},
		{ID: 4, State: sched.Queued, Description: "bench", Claim: sched.Claim{Exclusive: true}, Submitted: now.Add(-90 * time.Second), Waiting: "needs 14 cores (2 free)"},
		{ID: 2, State: sched.Failed, Description: "build", Owner: 9, Claim: sched.Claim{CPUs: 2, MemoryGB: 2, IO: true}, Submitted: now.Add(-time.Hour), Started: now.Add(-50 * time.Minute), Ended: now.Add(-40 * time.Minute), Exit: &sched.Exit{Code: 3}},
		{ID: 1, State: sched.Failed, Description: "link", Claim: sched.Claim{CPUs: 1, MemoryGB: 1}, Submitted: now.Add(-2 * time.Hour), Started: now.Add(-110 * time.Minute), Ended: now.Add(-100 * time.Minute), Exit: &sched.Exit{Code: -1, Signal: "SIGKILL"}},
	}}

	var all bytes.Buffer
	require.NoError(t, writeJobList(&all, q, true, now))

	assert.Regexp(t, `(?m)^ID +STATE +DESCRIPTION +OWNER +CLAIM +TIME +WAITS FOR$`, all.String())
	assert.Regexp(t, `(?m)^3 +running +tests +alpha +4c/6G +3m0s *$`, all.String())
	assert.Regexp(t, `(?m)^4 +queued +bench +- +exclusive +1m30s +needs 14 cores \(2 free\)$`, all.String())
	assert.Regexp(t, `(?m)^2 +failed \(exit 3\) +build +#9 +2c/2G io +10m0s *$`, all.String())
	assert.Regexp(t, `(?m)^1 +failed \(SIGKILL\) +link +- +1c/1G +10m0s *$`, all.String())

	var live bytes.Buffer
	require.NoError(t, writeJobList(&live, q, false, now))
	assert.Contains(t, live.String(), "tests")
	assert.Contains(t, live.String(), "bench")
	assert.NotContains(t, live.String(), "build", "finished jobs need --all")
	assert.NotContains(t, live.String(), "link", "finished jobs need --all")
}

func TestJobList_SaysWhenThereIsNothingToList(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	var out bytes.Buffer

	require.NoError(t, writeJobList(&out, core.QueueView{}, false, now))
	assert.Equal(t, "No running or queued jobs.\n", out.String())

	out.Reset()
	require.NoError(t, writeJobList(&out, core.QueueView{}, true, now))
	assert.Equal(t, "No jobs.\n", out.String())

	out.Reset()
	done := core.QueueView{Jobs: []core.JobView{{ID: 1, State: sched.Done, Started: now, Ended: now}}}
	require.NoError(t, writeJobList(&out, done, false, now))
	assert.Equal(t, "No running or queued jobs. 1 finished (loom job list --all shows them).\n", out.String())

	out.Reset()
	paused := core.QueueView{Paused: "scheduler.cpus is 5000: it must be 1–1024"}
	require.NoError(t, writeJobList(&out, paused, false, now))
	assert.Equal(t, "admission paused: scheduler.cpus is 5000: it must be 1–1024\nNo running or queued jobs.\n", out.String())
}

// A job claiming every cap holds the host: the next one queues behind it,
// `list` says why it waits, and a cancel ends each (a queued job at once, a
// running one when the daemon next settles it).
func TestJobSubmit_QueuesBehindAnExclusiveJobAndListShowsWhy(t *testing.T) {
	isolateWorkspaces(t)
	t.Setenv(claudemod.EnvLaunch, "")
	d := serveDaemon(t, "true")

	out, _, err := runJob(t, "submit", "-d", "hog", "--exclusive", "--", "sh", "-c", "echo up; exec sleep 60")
	require.NoError(t, err)
	assert.Equal(t, "job 1 started\nno session to notify: follow it with loom job wait 1\n", out)
	d.waitLog(t, 1, "up")

	t.Setenv(claudemod.EnvLaunch, "no-such-launch")
	out, errOut, err := runJob(t, "submit", "-d", "next", "--", "true")
	require.NoError(t, err)
	assert.Equal(t, "job 2 queued (1st in line, 1 running)\n", out)
	assert.Contains(t, errOut, "this shell's loom session (launch no-such-launch) isn't one the daemon serves: job 2 has no owner, so no message will come")

	out, _, err = runJob(t, "list")
	require.NoError(t, err)
	assert.Regexp(t, `(?m)^1 +running +hog +- +exclusive +\S+ *$`, out)
	assert.Regexp(t, `(?m)^2 +queued +next +- +\d+c/\d+G +\S+ +needs \d+ cores \(0 free\)$`, out)

	out, _, err = runJob(t, "cancel", "2")
	require.NoError(t, err)
	assert.Equal(t, "cancel requested for job 2\n", out)
	require.Eventually(t, func() bool { return d.job(2).State == sched.Cancelled }, 10*time.Second, 50*time.Millisecond,
		"a queued job is cancelled at once")
	assert.Equal(t, "with loom job cancel", d.job(2).CancelReason)

	out, _, err = runJob(t, "list", "--all")
	require.NoError(t, err)
	assert.Regexp(t, `(?m)^2 +cancelled +next `, out)

	_, _, err = runJob(t, "cancel", "2")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "job 2 has finished")
	_, _, err = runJob(t, "cancel", "99")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "there is no job 99")
	_, _, err = runJob(t, "cancel", "soon")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"soon" is not a job ID`)

	_, _, err = runJob(t, "cancel", "1")
	require.NoError(t, err)
	hog := d.waitFinished(t, 1)
	assert.Equal(t, sched.Cancelled, hog.State)
	assert.Equal(t, "with loom job cancel", hog.CancelReason)
}

// The daemon holds every bound, so a refused submission is the daemon's text,
// printed as the command's error, and queues nothing.
func TestJobSubmit_RefusalsArePrintedAndQueueNothing(t *testing.T) {
	isolateWorkspaces(t)
	t.Setenv(claudemod.EnvLaunch, "")
	d := serveDaemon(t, "true")

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"exclusive with cpus", []string{"submit", "-d", "x", "--exclusive", "--cpus", "2", "--", "true"}, "--exclusive claims everything: drop --cpus, --mem and --io"},
		{"zero cpus", []string{"submit", "-d", "x", "--cpus", "0", "--", "true"}, "--cpus is 0: it must be at least 1"},
		{"zero memory", []string{"submit", "-d", "x", "--mem", "0", "--", "true"}, "--mem is 0: it must be at least 1"},
		{"over the cpu cap", []string{"submit", "-d", "x", "--cpus", "5000", "--", "true"}, "claims 5000 cores; this host's cap is"},
		{"over the memory cap", []string{"submit", "-d", "x", "--mem", "500000", "--", "true"}, "claims 500000 GB; this host's cap is"},
		{"empty description", []string{"submit", "-d", "", "--", "true"}, "the description is empty"},
		{"tail out of range", []string{"submit", "-d", "x", "--tail", "201", "--", "true"}, "--tail is 201: it must be 0–200"},
		{"no command", []string{"submit", "-d", "x"}, "put the command after --"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, _, err := runJob(t, tc.args...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.Empty(t, out)
		})
	}
	assert.Empty(t, d.jobs(), "no refusal queued a job")
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `CGO_ENABLED=0 go test ./internal/cli/ -run 'TestJob' -count=1`

Expected: the package's tests fail to build, with errors of the form `internal/cli/job_test.go:NN:NN: undefined: newJobCmd` (also `claimFlags`, `shellWord`, `defaultDescription`, `ordinal`, `parseJobID`, `checkPlatform`, `queuePlace`, `writeJobList`), and `FAIL github.com/aidan-bailey/loom/internal/cli [build failed]`.

- [ ] **Step 3: Implement**

Create `internal/cli/job.go`:

```go
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/session/claudemod"
	"github.com/spf13/cobra"
)

// JobCmd is `loom job`: queue work (a build, a test run, a benchmark) with
// the daemon, follow it, read its output and cancel it. The daemon starts
// each job when its claim fits the host's caps and tells the submitting
// session's Claude when it ends. main attaches it.
var JobCmd = newJobCmd()

// defaultTail is how many lines of output.log a job's result message
// carries unless --tail says otherwise.
const defaultTail = 20

// descriptionLimit is how many characters of the command line a default
// description keeps.
const descriptionLimit = 60

// jobWait bounds the wait for the daemon to report a job it has just
// accepted: the Reply comes first, and the view that holds the job follows.
var jobWait = 5 * time.Second

// newJobCmd builds the `loom job` command tree, with flags of its own: each
// call is independent (tests build one per run).
func newJobCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "job",
		Short: "Queue builds, tests and benchmarks against this host's caps",
		Long: "Queue heavy work with the loom daemon, which starts each job when its claim fits what the host's caps leave " +
			"(cores, memory and I/O-heavy jobs) and keeps it running across daemon restarts.\n\n" +
			"Put the command after --. No shell is involved: a pipeline is `-- sh -c '…'`.",
	}
	cmd.AddCommand(newJobSubmitCmd(), newJobListCmd(), newJobCancelCmd())
	return cmd
}

// beginJob readies a `loom job` command's run: its errors are loom's, not
// usage mistakes, and the platform has a work queue.
func beginJob(c *cobra.Command) error {
	loomErrors(c)
	return checkPlatform(runtime.GOOS)
}

// checkPlatform refuses goos "windows", which has no scheduler, with the
// error the daemon's own refusal is too (sched.ErrUnsupported).
func checkPlatform(goos string) error {
	if goos == "windows" {
		return sched.ErrUnsupported
	}
	return nil
}

// claimFlags are the flags `submit` and `run` share: what the job is and what
// it claims.
type claimFlags struct {
	desc          string
	cpus, mem     int
	io, exclusive bool
}

// add registers the flags on c.
func (f *claimFlags) add(c *cobra.Command) {
	fl := c.Flags()
	fl.StringVarP(&f.desc, "desc", "d", "", "What the job is (default: the command line, cut to 60 characters)")
	fl.IntVar(&f.cpus, "cpus", 0, "Cores to claim, at least 1 (default: the host's default claim)")
	fl.IntVar(&f.mem, "mem", 0, "GB of memory to claim, at least 1 (default: the host's default claim)")
	fl.BoolVar(&f.io, "io", false, "I/O-heavy: takes one of the host's I/O slots")
	fl.BoolVar(&f.exclusive, "exclusive", false, "Claim every cap, for a benchmark (not with --cpus, --mem or --io)")
}

// claim is the claim the flags ask for. The daemon applies the default claim
// to a 0 and holds every other bound, so an explicit 0 is the one value the
// wire cannot carry: it is refused here, with the daemon's wording.
func (f *claimFlags) claim(c *cobra.Command) (sched.Claim, error) {
	for _, o := range []struct {
		flag string
		n    int
	}{
		{"cpus", f.cpus},
		{"mem", f.mem},
	} {
		if c.Flags().Changed(o.flag) && o.n < 1 {
			return sched.Claim{}, fmt.Errorf("--%s is %d: it must be at least 1", o.flag, o.n)
		}
	}
	return sched.Claim{CPUs: f.cpus, MemoryGB: f.mem, IO: f.io, Exclusive: f.exclusive}, nil
}

// submission is the job the command line asks for: args (everything after
// --) as the argv, with the shell it runs in: its working directory, its
// whole environment and the launch it belongs to ($LOOM_LAUNCH, which the
// daemon resolves to the owning session). The daemon checks every bound.
func (f *claimFlags) submission(c *cobra.Command, args []string, notify bool, tail int) (sched.Submit, error) {
	if len(args) == 0 || c.ArgsLenAtDash() != 0 {
		return sched.Submit{}, fmt.Errorf("put the command after --, as in: loom job %s -d \"<what>\" -- <cmd> [args…]", c.Name())
	}
	claim, err := f.claim(c)
	if err != nil {
		return sched.Submit{}, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return sched.Submit{}, fmt.Errorf("the working directory: %w", err)
	}
	desc := f.desc
	if !c.Flags().Changed("desc") {
		desc = defaultDescription(args)
	}
	return sched.Submit{
		Argv: slices.Clone(args), Cwd: cwd, Env: os.Environ(), Description: desc, Claim: claim,
		Notify: notify, Tail: tail, Launch: os.Getenv(claudemod.EnvLaunch),
	}, nil
}

// defaultDescription is the command line, each word shell-quoted, cut to
// descriptionLimit characters.
func defaultDescription(argv []string) string {
	words := make([]string, len(argv))
	for i, a := range argv {
		words[i] = shellWord(a)
	}
	r := []rune(strings.Join(words, " "))
	if len(r) > descriptionLimit {
		r = r[:descriptionLimit]
	}
	return string(r)
}

// shellWord quotes s for a POSIX shell: as it is when it holds only
// characters a shell never treats specially, else in single quotes.
func shellWord(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool { return !plainWordRune(r) }) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func plainWordRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	}
	return strings.ContainsRune("_@%+=:,./-", r)
}

func newJobSubmitCmd() *cobra.Command {
	var (
		f        claimFlags
		tail     int
		noNotify bool
	)
	cmd := &cobra.Command{
		Use:   "submit [flags] -- CMD [ARGS…]",
		Short: "Queue a command and return at once; a message brings the result",
		Long: "Queues the command with the daemon and returns as soon as it is queued or started. When it ends, " +
			"the daemon sends the result to the session this command runs in (unless --no-notify).",
		Args: cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			if err := beginJob(c); err != nil {
				return err
			}
			sub, err := f.submission(c, args, !noNotify, tail)
			if err != nil {
				return err
			}
			return withDaemon(c, func(d *client) error {
				id, err := d.submitJob(sub)
				if err != nil {
					return err
				}
				v, err := d.awaitJob(id)
				if err != nil {
					return fmt.Errorf("job %d was submitted, but %w", id, err)
				}
				out := c.OutOrStdout()
				d.printPlacement(out, v)
				if sub.Launch == "" {
					fmt.Fprintf(out, "no session to notify: follow it with loom job wait %d\n", id)
				}
				return nil
			})
		},
	}
	f.add(cmd)
	cmd.Flags().IntVar(&tail, "tail", defaultTail, "Lines of output the result message carries, 0–200")
	cmd.Flags().BoolVar(&noNotify, "no-notify", false, "Send no message when the job ends")
	return cmd
}

// submitJob makes the SubmitJob request and waits for its Reply: the new
// job's ID, or the daemon's refusal. The warning an unknown launch earns
// rides the Reply and goes to errOut.
func (d *client) submitJob(sub sched.Submit) (sched.JobID, error) {
	d.SubmitJob(sub, sessionReq)
	reply, err := d.awaitReply(sessionReq)
	switch {
	case err != nil:
		return 0, err
	case reply.Err != nil:
		return 0, reply.Err
	}
	if reply.Notice != nil {
		fmt.Fprintf(d.errOut, "loom: %v\n", reply.Notice)
	}
	return reply.Job, nil
}

// awaitJob waits, for up to jobWait, until the replica holds job id: the
// Reply to a submission can arrive before the view that shows the job.
func (d *client) awaitJob(id sched.JobID) (core.JobView, error) {
	var v core.JobView
	ok, err := d.await(jobWait, func([]core.Event) bool {
		var found bool
		v, found = jobOf(d.Queue(), id)
		return found
	})
	switch {
	case err != nil:
		return v, err
	case !ok:
		return v, errors.New("the loom daemon did not report it (loom job list shows where it is)")
	}
	return v, nil
}

// printPlacement writes the line `submit` ends with: where a queued job
// stands, or that it started.
func (d *client) printPlacement(out io.Writer, v core.JobView) {
	if v.State != sched.Queued {
		fmt.Fprintf(out, "job %d started\n", v.ID)
		return
	}
	n, running := queuePlace(d.Queue(), v.ID)
	fmt.Fprintf(out, "job %d queued (%s in line, %d running)\n", v.ID, ordinal(n), running)
}

// jobOf is job id among q's jobs.
func jobOf(q core.QueueView, id sched.JobID) (core.JobView, bool) {
	for _, j := range q.Jobs {
		if j.ID == id {
			return j, true
		}
	}
	return core.JobView{}, false
}

// queuePlace is job id's place among the queued jobs (1 for the head, 0 when
// it is not queued) and how many jobs run.
func queuePlace(q core.QueueView, id sched.JobID) (place, running int) {
	queued := 0
	for _, j := range q.Jobs {
		switch j.State {
		case sched.Running:
			running++
		case sched.Queued:
			queued++
			if j.ID == id {
				place = queued
			}
		}
	}
	return place, running
}

// ordinal is n as "1st", "2nd", "3rd", "4th", "11th", "21st"…
func ordinal(n int) string {
	suffix := "th"
	if n%100 < 11 || n%100 > 13 {
		switch n % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return strconv.Itoa(n) + suffix
}

// parseJobID is a job ID typed on the command line.
func parseJobID(s string) (sched.JobID, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%q is not a job ID", s)
	}
	return sched.JobID(n), nil
}

func newJobListCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the running and queued jobs, and why each queued one waits",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if err := beginJob(c); err != nil {
				return err
			}
			return withDaemon(c, func(d *client) error {
				return writeJobList(c.OutOrStdout(), d.Queue(), all, time.Now())
			})
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Include the finished jobs the daemon still keeps")
	return cmd
}

// writeJobList prints q's jobs as a table (the running, the queued, and with
// all the finished ones kept), led by the reason when admission is paused.
func writeJobList(w io.Writer, q core.QueueView, all bool, now time.Time) error {
	if q.Paused != "" {
		fmt.Fprintf(w, "admission paused: %s\n", q.Paused)
	}
	var rows []core.JobView
	finished := 0
	for _, j := range q.Jobs {
		switch {
		case !j.State.Finished() || all:
			rows = append(rows, j)
		default:
			finished++
		}
	}
	if len(rows) == 0 {
		msg := "No jobs."
		if !all {
			msg = "No running or queued jobs."
			if finished > 0 {
				msg += fmt.Sprintf(" %d finished (loom job list --all shows them).", finished)
			}
		}
		_, err := fmt.Fprintln(w, msg)
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATE\tDESCRIPTION\tOWNER\tCLAIM\tTIME\tWAITS FOR")
	for _, j := range rows {
		waits := ""
		if j.State == sched.Queued {
			waits = j.Waiting
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
			j.ID, stateText(j), j.Description, ownerText(j), claimText(j.Claim), rounded(elapsed(j, now)), waits)
	}
	return tw.Flush()
}

// stateText is a job's state, with how a failed one failed.
func stateText(j core.JobView) string {
	if j.State != sched.Failed {
		return string(j.State)
	}
	sig, code, ran := ranAs(j)
	switch {
	case !ran:
		return "failed (not started)"
	case sig != "":
		return "failed (" + sig + ")"
	}
	return fmt.Sprintf("failed (exit %d)", code)
}

// ranAs says how a failed job ran: ran is false when it never did (its start
// failed, or it has no exit record), else it was killed by the signal sig (a
// name) or exited with code.
func ranAs(j core.JobView) (sig string, code int, ran bool) {
	e := j.Exit
	switch {
	case e == nil || e.Error != "":
		return "", 0, false
	case e.Signal != "":
		return e.Signal, 0, true
	case e.Code > 0:
		return "", e.Code, true
	}
	return "", 0, false
}

func ownerText(j core.JobView) string {
	switch {
	case j.Owner == 0:
		return "-"
	case j.OwnerTitle == "":
		return fmt.Sprintf("#%d", j.Owner)
	}
	return j.OwnerTitle
}

func claimText(c sched.Claim) string {
	if c.Exclusive {
		return "exclusive"
	}
	s := fmt.Sprintf("%dc/%dG", c.CPUs, c.MemoryGB)
	if c.IO {
		s += " io"
	}
	return s
}

// elapsed is how long a job has waited (queued), has run (running), or ran
// (finished; a job that never started, how long it lived).
func elapsed(j core.JobView, now time.Time) time.Duration {
	switch {
	case j.State == sched.Queued:
		return now.Sub(j.Submitted)
	case j.State == sched.Running:
		return now.Sub(j.Started)
	case !j.Started.IsZero():
		return j.Ended.Sub(j.Started)
	}
	return j.Ended.Sub(j.Submitted)
}

// rounded is d to the second, as "3m10s".
func rounded(d time.Duration) string {
	return d.Round(time.Second).String()
}

func newJobCancelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cancel ID",
		Short: "Cancel a queued or running job",
		Long: "A queued job is cancelled at once. A running job's processes get SIGTERM, and SIGKILL ten seconds later " +
			"if they are still there; the job ends as cancelled either way.",
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if err := beginJob(c); err != nil {
				return err
			}
			id, err := parseJobID(args[0])
			if err != nil {
				return err
			}
			return withDaemon(c, func(d *client) error {
				if err := d.request(func(req core.ReqID) { d.CancelJob(id, core.CancelFromCLI, req) }); err != nil {
					return err
				}
				fmt.Fprintf(c.OutOrStdout(), "cancel requested for job %d\n", id)
				return nil
			})
		},
	}
}
```

In `main.go`, register the command. Replace the line

```go
	rootCmd.AddCommand(cli.SweepCmd)
```

with

```go
	rootCmd.AddCommand(cli.SweepCmd)
	rootCmd.AddCommand(cli.JobCmd)
```

- [ ] **Step 4: Run it to see it pass**

Run: `CGO_ENABLED=0 go test ./internal/cli/ -run 'TestJob' -count=1`

Expected: `ok  	github.com/aidan-bailey/loom/internal/cli` (about 10 s: the two real jobs settle on the 3 s gate).

Then the gates: `bash -c 'out=$(gofmt -l $(git ls-files "*.go" | grep -v "^vendor/")) || exit 1; if [ -n "$out" ]; then echo "gofmt would reformat:"; echo "$out"; exit 1; fi'` and `CGO_ENABLED=0 go vet ./...` print nothing.

- [ ] **Step 5: Commit**

```bash
git add -N internal/cli/job.go internal/cli/job_test.go
git commit -m "feat(cli): loom job submit, list and cancel" -- internal/cli/job.go internal/cli/job_test.go internal/cli/daemon_test.go internal/cli/testmain_test.go main.go
```

### Task 5.2: `loom job run`, `wait` and `logs`, and the follower's exit codes

**Files:**
- Modify: `internal/cli/job.go` (the import block, `newJobCmd`, then the end of the file)
- Create: `internal/cli/job_unix.go`, `internal/cli/job_windows.go`
- Test: `internal/cli/job_test.go` (the import block, then the end of the file)
- Modify: `main.go:442-445` (Story 2's `jobShimCmd` line moves them down by one)

- [ ] **Step 1: Write the failing tests**

In `internal/cli/job_test.go`, replace the import block

```go
import (
	"bytes"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/cmd"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/core/rpc"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/session/claudemod"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)
```

with

```go
import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/session/claudemod"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)
```

Append to the end of `internal/cli/job_test.go`:

```go
// exitCode is the exit code a command's error asks main to end with.
func exitCode(t *testing.T, err error) int {
	t.Helper()
	var exit *ExitError
	require.ErrorAs(t, err, &exit)
	return exit.Code
}

// interruptWhenAJobExists replaces the follower's signal source with one that
// delivers sig once d serves a job, and restores it when the test ends.
func interruptWhenAJobExists(t *testing.T, d *testDaemon, sig os.Signal) {
	t.Helper()
	sigs := make(chan os.Signal, 1)
	orig := notifySignals
	notifySignals = func() (<-chan os.Signal, func()) { return sigs, func() {} }
	t.Cleanup(func() { notifySignals = orig })
	go func() {
		for {
			if len(d.jobs()) > 0 {
				sigs <- sig
				return
			}
			select {
			case <-t.Context().Done():
				return
			case <-time.After(50 * time.Millisecond):
			}
		}
	}()
}

func TestJobOutcome(t *testing.T) {
	t0 := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)
	ran := func(state sched.State, exit *sched.Exit) core.JobView {
		return core.JobView{ID: 5, State: state, Submitted: t0.Add(-time.Minute), Started: t0, Ended: t0.Add(190 * time.Second), Exit: exit}
	}
	cancelled := ran(sched.Cancelled, &sched.Exit{Code: -1, Signal: "SIGTERM"})
	cancelled.CancelReason = "from the work view"
	cancelledBare := ran(sched.Cancelled, nil)
	lost := ran(sched.Lost, nil)
	lost.Failure = "its runner died without a result (the host may have rebooted)"
	noStart := ran(sched.Failed, nil)
	noStart.Failure = "its folder is gone"
	execFailed := ran(sched.Failed, &sched.Exit{Code: 127, Error: `exec: "nope": executable file not found in $PATH`})

	for _, tc := range []struct {
		name string
		job  core.JobView
		line string
		code int
	}{
		{"done", ran(sched.Done, &sched.Exit{Code: 0}), "job 5 done in 3m10s (exit 0)", 0},
		{"failed", ran(sched.Failed, &sched.Exit{Code: 3}), "job 5 failed in 3m10s (exit 3)", 3},
		{"killed", ran(sched.Failed, &sched.Exit{Code: -1, Signal: "SIGKILL"}), "job 5 was killed by SIGKILL after 3m10s", 137},
		{"terminated", ran(sched.Failed, &sched.Exit{Code: -1, Signal: "SIGTERM"}), "job 5 was killed by SIGTERM after 3m10s", 143},
		{"a signal beyond the usual few", ran(sched.Failed, &sched.Exit{Code: -1, Signal: "SIGXCPU"}), "job 5 was killed by SIGXCPU after 3m10s", 152},
		{"a real-time signal the platform has no name for", ran(sched.Failed, &sched.Exit{Code: -1, Signal: "SIG63"}), "job 5 was killed by SIG63 after 3m10s", 191},
		{"a name no signal has", ran(sched.Failed, &sched.Exit{Code: -1, Signal: "SIGBOGUS"}), "job 5 was killed by SIGBOGUS after 3m10s", exitNotRun},
		{"cancelled", cancelled, "job 5 was cancelled from the work view", exitNotRun},
		{"cancelled, no reason", cancelledBare, "job 5 was cancelled", exitNotRun},
		{"lost", lost, "job 5 was lost: its runner died without a result (the host may have rebooted)", exitNotRun},
		{"its start failed", noStart, "job 5 could not start: its folder is gone", exitNotRun},
		{"its exec failed", execFailed, `job 5 could not start: exec: "nope": executable file not found in $PATH`, exitNotRun},
	} {
		line, code := jobOutcome(tc.job)
		assert.Equal(t, tc.line, line, tc.name)
		assert.Equal(t, tc.code, code, tc.name)
	}
}

func TestJobInterruptCode(t *testing.T) {
	assert.Equal(t, 130, interruptCode(syscall.SIGINT))
	assert.Equal(t, 143, interruptCode(syscall.SIGTERM))
	assert.Equal(t, 129, interruptCode(syscall.SIGHUP))
	assert.Equal(t, 130, interruptCode(nil), "an interrupt that no signal brought (a cancelled context)")
}

func TestJobTailOffset(t *testing.T) {
	tail := func(content string, n int) string {
		off, err := tailOffset(strings.NewReader(content), int64(len(content)), n)
		require.NoError(t, err)
		return content[off:]
	}

	assert.Equal(t, "a\nb\nc\n", tail("a\nb\nc\n", -1), "a negative count is all of it")
	assert.Equal(t, "", tail("a\nb\nc\n", 0))
	assert.Equal(t, "c\n", tail("a\nb\nc\n", 1))
	assert.Equal(t, "b\nc\n", tail("a\nb\nc\n", 2))
	assert.Equal(t, "a\nb\nc\n", tail("a\nb\nc\n", 3))
	assert.Equal(t, "a\nb\nc\n", tail("a\nb\nc\n", 10))
	assert.Equal(t, "b", tail("a\nb", 1), "a last line with no newline is a line")
	assert.Equal(t, "", tail("", 5))

	var long strings.Builder
	for i := 1; i <= 5000; i++ {
		fmt.Fprintf(&long, "line %05d\n", i)
	}
	assert.Equal(t, "line 04998\nline 04999\nline 05000\n", tail(long.String(), 3), "found across several read blocks")
	assert.Equal(t, long.String(), tail(long.String(), 5000))
	assert.Equal(t, long.String()[len("line 00001\n"):], tail(long.String(), 4999))
}

func TestJobCopyLog_CopiesOnlyWhatIsNew(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.log")
	var out bytes.Buffer
	f := &jobFollower{out: &out}

	require.NoError(t, f.copyLog(path), "a job that has not started has no log yet")
	assert.Empty(t, out.String())

	require.NoError(t, os.WriteFile(path, []byte("ab"), 0o600))
	require.NoError(t, f.copyLog(path))
	assert.Equal(t, "ab", out.String())

	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = file.WriteString("cd")
	require.NoError(t, err)
	require.NoError(t, file.Close())
	require.NoError(t, f.copyLog(path))
	assert.Equal(t, "abcd", out.String(), "the second read starts where the first stopped")

	require.NoError(t, f.copyLog(path))
	assert.Equal(t, "abcd", out.String(), "nothing new, nothing copied")
}

// `run` follows the job to its end: the job's output goes to stdout, its
// progress to stderr, and the command exits with the job's exit code. `logs`
// and `wait` then read the finished job back.
func TestJobRun_FollowsAJobToItsExitCodeAndLogsReadItBack(t *testing.T) {
	isolateWorkspaces(t)
	t.Setenv(claudemod.EnvLaunch, "")
	serveDaemon(t, "true")

	out, errOut, err := runJob(t, "run", "-d", "fails", "--", "sh", "-c", "echo out; echo err >&2; exit 3")
	assert.Equal(t, 3, exitCode(t, err))
	assert.Equal(t, "out\nerr\n", out, "stdout and stderr of the job, in order")
	assert.Contains(t, errOut, "loom: job 1 started after ")
	assert.Regexp(t, `loom: job 1 failed in \S+ \(exit 3\)\n`, errOut)

	out, errOut, err = runJob(t, "run", "-d", "fine", "--", "sh", "-c", "echo fine")
	require.NoError(t, err)
	assert.Equal(t, "fine\n", out)
	assert.Regexp(t, `loom: job 2 done in \S+ \(exit 0\)\n`, errOut)

	out, _, err = runJob(t, "logs", "1")
	require.NoError(t, err)
	assert.Equal(t, "out\nerr\n", out)
	out, _, err = runJob(t, "logs", "1", "--tail", "1")
	require.NoError(t, err)
	assert.Equal(t, "err\n", out)
	out, _, err = runJob(t, "logs", "1", "--tail", "0")
	require.NoError(t, err)
	assert.Equal(t, "", out)
	out, _, err = runJob(t, "logs", "1", "-f")
	require.NoError(t, err, "following a finished job prints its output and returns")
	assert.Equal(t, "out\nerr\n", out)
	_, _, err = runJob(t, "logs", "99")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "there is no job 99")

	out, errOut, err = runJob(t, "wait", "1")
	assert.Equal(t, 3, exitCode(t, err), "wait follows a finished job too")
	assert.Equal(t, "out\nerr\n", out)
	assert.Regexp(t, `loom: job 1 failed in \S+ \(exit 3\)\n`, errOut)
}

// A job that queues behind another says where it stands, and when it starts
// how long it waited.
func TestJobRun_ShowsItsPlaceInLineThenItsStart(t *testing.T) {
	isolateWorkspaces(t)
	t.Setenv(claudemod.EnvLaunch, "")
	d := serveDaemon(t, "true")
	_, _, err := runJob(t, "submit", "-d", "hog", "--exclusive", "--no-notify", "--", "sh", "-c", "echo up; sleep 2")
	require.NoError(t, err)

	out, errOut, err := runJob(t, "run", "-d", "next", "--", "sh", "-c", "echo next")

	require.NoError(t, err)
	assert.Equal(t, "next\n", out)
	assert.Regexp(t, `(?s)loom: job 2 queued \(1st in line, 1 running\)\n.*loom: job 2 started after \d+s\n.*loom: job 2 done in \S+ \(exit 0\)\n`, errOut)
	d.waitFinished(t, 1)
}

// A job cancelled while waited on ends the wait with 125: it did not run to
// an exit.
func TestJobWait_ACancelledJobExits125(t *testing.T) {
	isolateWorkspaces(t)
	t.Setenv(claudemod.EnvLaunch, "")
	d := serveDaemon(t, "true")
	_, _, err := runJob(t, "submit", "-d", "nap", "--no-notify", "--", "sh", "-c", "echo up; exec sleep 60")
	require.NoError(t, err)
	d.waitLog(t, 1, "up")
	_, _, err = runJob(t, "cancel", "1")
	require.NoError(t, err)

	_, errOut, err := runJob(t, "wait", "1")

	assert.Equal(t, 125, exitCode(t, err))
	assert.Contains(t, errOut, "loom: job 1 was cancelled with loom job cancel\n")

	_, _, err = runJob(t, "wait", "99")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "there is no job 99")
}

// An interrupted follower of a job with no owner has no message to ask for;
// it says the job goes on and how to follow it again, and exits with the
// signal's code. The job is never cancelled by the follower's end.
func TestJobRun_AnInterruptWithNoOwnerSaysSo(t *testing.T) {
	isolateWorkspaces(t)
	t.Setenv(claudemod.EnvLaunch, "")
	d := serveDaemon(t, "true")
	interruptWhenAJobExists(t, d, syscall.SIGTERM)

	_, errOut, err := runJob(t, "run", "-d", "nap", "--", "sleep", "1")

	assert.Equal(t, 143, exitCode(t, err))
	assert.Contains(t, errOut, "loom: job 1 continues (loom job wait 1 follows it again)\n")
	assert.NotContains(t, errOut, "a message will bring")
	v := d.waitFinished(t, 1)
	assert.Equal(t, sched.Done, v.State, "the job ran on and finished")
}

// An interrupted follower of a session's job asks the daemon for a message
// (`run` submitted it without one): the job's result then reaches the
// session's Claude through the mod when it ends.
func TestJobRun_AnInterruptAsksForAMessageWhenTheJobHasAnOwner(t *testing.T) {
	isolateWorkspaces(t)
	withModSocket(t)
	agent := fakeAgent(t, "claude.ai")
	liveWorkspace(t, "ws-a", agent, nil)
	d := serveDaemonLive(t, agent)
	_, _, err := runSession(t, "", "new", "alpha", "-w", "ws-a")
	require.NoError(t, err)
	launch := launchOf(t, d, "ws-a", "alpha")
	require.NoError(t, d.loop.ModHello(launch, core.ModHello{Mod: 1}))
	carried := playMod(t, d.loop, launch, "", "")
	t.Setenv(claudemod.EnvLaunch, launch)
	interruptWhenAJobExists(t, d, syscall.SIGTERM)

	_, errOut, err := runJob(t, "run", "-d", "nap", "--", "sleep", "2")

	assert.Equal(t, 143, exitCode(t, err))
	assert.Contains(t, errOut, "loom: job 1 continues; a message will bring its result (loom job wait 1 follows it again)\n")
	select {
	case text := <-carried:
		assert.Contains(t, text, `[loom] job 1 "nap" finished: exit 0`, "the notice the interrupt asked for")
	case <-time.After(30 * time.Second):
		t.Fatal("the job's result never reached the session's Claude")
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `CGO_ENABLED=0 go test ./internal/cli/ -run 'TestJob' -count=1`

Expected: the package's tests fail to build, with errors of the form `internal/cli/job_test.go:NN:NN: undefined: ExitError` (also `notifySignals`, `jobOutcome`, `interruptCode`, `exitNotRun`, `tailOffset`, `jobFollower`), and `FAIL github.com/aidan-bailey/loom/internal/cli [build failed]`.

- [ ] **Step 3: Implement**

In `internal/cli/job.go`, replace the import block

```go
import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/session/claudemod"
	"github.com/spf13/cobra"
)
```

with

```go
import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/session/claudemod"
	"github.com/spf13/cobra"
)
```

Replace the line in `newJobCmd`

```go
	cmd.AddCommand(newJobSubmitCmd(), newJobListCmd(), newJobCancelCmd())
```

with

```go
	cmd.AddCommand(newJobSubmitCmd(), newJobRunCmd(), newJobWaitCmd(), newJobLogsCmd(), newJobListCmd(), newJobCancelCmd())
```

Append to the end of `internal/cli/job.go`:

```go
// ExitError ends a command with Code as the process's exit code. main prints
// Msg first when it has one: a follower says what happened on stderr as it
// goes, so most carry none.
type ExitError struct {
	Code int
	Msg  string
}

func (e *ExitError) Error() string { return e.Msg }

// exitNotRun is the exit code of a follower whose job was lost, could not
// start or was cancelled: it did not run to an exit of its own.
const exitNotRun = 125

// logPoll is how often a follower reads output.log for new output.
var logPoll = 200 * time.Millisecond

// notifyWait bounds an interrupted follower's wait for the daemon to answer
// its request for a message.
var notifyWait = 2 * time.Second

// notifySignals delivers the signals that interrupt a follower (SIGINT,
// SIGTERM and SIGHUP, as a Bash timeout sends them) on the channel it
// returns, until the stop function it returns is called. It registers with
// signal.Notify and releases with signal.Stop, never signal.Ignore: an
// ignored signal would survive into a process started later. Tests replace
// it with a channel of their own.
var notifySignals = func() (<-chan os.Signal, func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	return ch, func() { signal.Stop(ch) }
}

// interruptible returns a context that the first interrupt cancels, the
// function that names that signal, and the stop that releases the handler.
// The context ends the follower's waits (client.ctx); the daemon's work is
// never touched by it.
func interruptible(parent context.Context) (ctx context.Context, signalled func() os.Signal, stop func()) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	sigs, release := notifySignals()
	var (
		mu  sync.Mutex
		got os.Signal
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case s := <-sigs:
			mu.Lock()
			got = s
			mu.Unlock()
			cancel()
		case <-ctx.Done():
		}
	}()
	signalled = func() os.Signal {
		mu.Lock()
		defer mu.Unlock()
		return got
	}
	stop = func() {
		release()
		cancel()
		<-done
	}
	return ctx, signalled, stop
}

// interruptCode is the exit code of a command ended by sig, as a shell
// reports it: 128 plus the signal's number (130 for SIGINT, 143 for SIGTERM,
// 129 for SIGHUP). An interrupt no signal brought counts as SIGINT.
func interruptCode(sig os.Signal) int {
	if s, ok := sig.(syscall.Signal); ok {
		return 128 + int(s)
	}
	return 128 + int(syscall.SIGINT)
}

// jobOutcome is the line a follower writes when job v has ended, and the
// exit code that follows (spec §6.3): the job's own exit code, 128 plus the
// signal that killed it, or exitNotRun for a job that was lost, could not
// start or was cancelled.
func jobOutcome(v core.JobView) (line string, code int) {
	took := rounded(v.Ended.Sub(v.Started))
	switch v.State {
	case sched.Done:
		return fmt.Sprintf("job %d done in %s (exit 0)", v.ID, took), 0
	case sched.Cancelled:
		return strings.TrimSpace(fmt.Sprintf("job %d was cancelled %s", v.ID, v.CancelReason)), exitNotRun
	case sched.Lost:
		return fmt.Sprintf("job %d was lost: %s", v.ID, v.Failure), exitNotRun
	case sched.Failed:
		sig, exit, ran := ranAs(v)
		switch {
		case ran && sig != "":
			return fmt.Sprintf("job %d was killed by %s after %s", v.ID, sig, took), signalExit(sig)
		case ran:
			return fmt.Sprintf("job %d failed in %s (exit %d)", v.ID, took, exit), exit
		}
		return fmt.Sprintf("job %d could not start: %s", v.ID, startFailure(v)), exitNotRun
	}
	return fmt.Sprintf("job %d is %s", v.ID, v.State), 1
}

// startFailure is why a job that never ran failed: the daemon's reason, else
// the exec error the runner recorded.
func startFailure(v core.JobView) string {
	switch {
	case v.Failure != "":
		return v.Failure
	case v.Exit != nil && v.Exit.Error != "":
		return v.Exit.Error
	}
	return "no reason was recorded"
}

// say writes one of a command's progress lines to stderr.
func (d *client) say(format string, a ...any) {
	fmt.Fprintf(d.errOut, "loom: "+format+"\n", a...)
}

// jobFollower follows one job: it copies the job's output to out as the log
// grows and, unless quiet, says on stderr where the job stands in the queue
// and when it starts.
type jobFollower struct {
	d       *client
	id      sched.JobID
	out     io.Writer
	off     int64
	quiet   bool
	queued  bool
	started bool
	last    string
}

// run follows the job until it has ended (its last view, with the log copied
// whole) or the command's context ends (that context's error, with the last
// view). A job the daemon does not hold is an error, and so is a lost
// daemon: the job goes on without it.
func (f *jobFollower) run() (core.JobView, error) {
	tick := time.NewTicker(logPoll)
	defer tick.Stop()
	for {
		f.d.events()
		v, ok := jobOf(f.d.Queue(), f.id)
		if !ok {
			return v, fmt.Errorf("there is no job %d", f.id)
		}
		// The view comes first, so a job seen ended has a complete log.
		if err := f.copyLog(v.LogPath); err != nil {
			return v, err
		}
		f.report(v)
		if v.State.Finished() {
			return v, nil
		}
		if err := f.d.Err(); err != nil {
			return v, fmt.Errorf("lost the loom daemon: %w", err)
		}
		if err := f.d.ctx.Err(); err != nil {
			return v, err
		}
		select {
		case <-f.d.Wakes():
		case <-tick.C:
		case <-f.d.ctx.Done():
		}
	}
}

// report writes the queue line when the job's place changes, and the start
// line when a job seen waiting starts.
func (f *jobFollower) report(v core.JobView) {
	if f.quiet {
		return
	}
	switch v.State {
	case sched.Queued:
		f.queued = true
		n, running := queuePlace(f.d.Queue(), v.ID)
		if line := fmt.Sprintf("job %d queued (%s in line, %d running)", v.ID, ordinal(n), running); line != f.last {
			f.d.say("%s", line)
			f.last = line
		}
	case sched.Running:
		if f.queued && !f.started {
			f.d.say("job %d started after %s", v.ID, rounded(v.Started.Sub(v.Submitted)))
		}
		f.started = true
	}
}

// copyLog copies what the log at path has gained since the last call to out.
// A log that does not exist yet (a job still queued) has gained nothing.
func (f *jobFollower) copyLog(path string) error {
	if path == "" {
		return nil
	}
	file, err := os.Open(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("read the job's output: %w", err)
	}
	defer file.Close()
	if _, err := file.Seek(f.off, io.SeekStart); err != nil {
		return fmt.Errorf("read the job's output: %w", err)
	}
	n, err := io.Copy(f.out, file)
	f.off += n
	if err != nil {
		return fmt.Errorf("copy the job's output: %w", err)
	}
	return nil
}

// follow follows job id to its end, with its output on out, then writes the
// outcome and returns the exit code it asks for (an *ExitError, or nil for
// exit 0). An interrupt (signalled names it) leaves the job running and ends
// the command (interrupted). queuedHere is true for a job this command
// queued, so its start is reported even when it never waited.
func (d *client) follow(out io.Writer, id sched.JobID, signalled func() os.Signal, queuedHere bool) error {
	f := &jobFollower{d: d, id: id, out: out, queued: queuedHere}
	v, err := f.run()
	switch {
	case err == nil:
		line, code := jobOutcome(v)
		d.say("%s", line)
		if code == 0 {
			return nil
		}
		return &ExitError{Code: code}
	case errors.Is(err, context.Canceled):
		return d.interrupted(id, signalled())
	}
	return err
}

// interrupted ends a follower that sig interrupted. The job is never
// cancelled by it. A job that has an owner and has not ended gets a message
// asked for (the follower's job was submitted without one), and the line says
// so only when the daemon took the request; otherwise it says how to follow
// the job again. The exit code is the signal's.
func (d *client) interrupted(id sched.JobID, sig os.Signal) error {
	v, _ := jobOf(d.Queue(), id)
	switch {
	case v.State.Finished():
		line, _ := jobOutcome(v)
		d.say("%s", line)
	case v.Owner != 0 && d.askForMessage(id):
		d.say("job %d continues; a message will bring its result (loom job wait %d follows it again)", id, id)
	default:
		d.say("job %d continues (loom job wait %d follows it again)", id, id)
	}
	return &ExitError{Code: interruptCode(sig)}
}

// askForMessage sends NotifyJob on a copy of the client whose context is not
// the interrupted one (client.await would give up at once), and reports
// whether the daemon took it. A refusal or no answer is written to errOut.
func (d *client) askForMessage(id sched.JobID) bool {
	n := *d
	n.ctx = context.Background()
	n.NotifyJob(id, sessionReq)
	var reply core.Reply
	got, err := n.await(notifyWait, func(events []core.Event) bool {
		for _, ev := range events {
			if r, ok := ev.(core.Reply); ok && r.Req == sessionReq {
				reply = r
				return true
			}
		}
		return false
	})
	switch {
	case err != nil:
		d.say("no message was asked for: %v", err)
		return false
	case !got:
		d.say("no message was asked for: the daemon did not answer")
		return false
	case reply.Err != nil:
		d.say("no message was asked for: %v", reply.Err)
		return false
	}
	return true
}

func newJobRunCmd() *cobra.Command {
	var f claimFlags
	cmd := &cobra.Command{
		Use:   "run [flags] -- CMD [ARGS…]",
		Short: "Queue a command, follow it, and exit with its exit code",
		Long: "Queues the command and follows it: its output goes to stdout, where it stands in the queue to stderr, " +
			"and the command exits with the job's exit code (128 plus N for a job killed by signal N; 125 for a job that was lost, " +
			"could not start or was cancelled). No message is sent when it ends. An interrupt (Ctrl-C, a Bash timeout) leaves the job running: " +
			"when it belongs to a session, that session is sent a message with its result.",
		Args: cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			if err := beginJob(c); err != nil {
				return err
			}
			sub, err := f.submission(c, args, false, defaultTail)
			if err != nil {
				return err
			}
			ctx, signalled, stop := interruptible(c.Context())
			defer stop()
			c.SetContext(ctx)
			return withDaemon(c, func(d *client) error {
				id, err := d.submitJob(sub)
				switch {
				case errors.Is(err, context.Canceled):
					return &ExitError{
						Code: interruptCode(signalled()),
						Msg:  "interrupted before the daemon answered: the job may still be queued (loom job list shows it)",
					}
				case err != nil:
					return err
				}
				if _, err := d.awaitJob(id); err != nil {
					if errors.Is(err, context.Canceled) {
						return d.interrupted(id, signalled())
					}
					return fmt.Errorf("job %d was submitted, but %w", id, err)
				}
				return d.follow(c.OutOrStdout(), id, signalled, true)
			})
		},
	}
	f.add(cmd)
	return cmd
}

func newJobWaitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "wait ID",
		Short: "Follow a job to its end, and exit with its exit code",
		Long: "Follows a queued, running or finished job as `run` does: its output goes to stdout, its progress to stderr, " +
			"and the exit code is the job's. An interrupt leaves the job running.",
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if err := beginJob(c); err != nil {
				return err
			}
			id, err := parseJobID(args[0])
			if err != nil {
				return err
			}
			ctx, signalled, stop := interruptible(c.Context())
			defer stop()
			c.SetContext(ctx)
			return withDaemon(c, func(d *client) error {
				return d.follow(c.OutOrStdout(), id, signalled, false)
			})
		},
	}
}

func newJobLogsCmd() *cobra.Command {
	var (
		tail   uint
		follow bool
	)
	cmd := &cobra.Command{
		Use:   "logs ID",
		Short: "Print a job's output",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if err := beginJob(c); err != nil {
				return err
			}
			id, err := parseJobID(args[0])
			if err != nil {
				return err
			}
			lines := -1
			if c.Flags().Changed("tail") {
				lines = int(min(tail, 1<<31-1))
			}
			ctx, signalled, stop := interruptible(c.Context())
			defer stop()
			c.SetContext(ctx)
			return withDaemon(c, func(d *client) error {
				err := d.logs(c.OutOrStdout(), id, lines, follow)
				if errors.Is(err, context.Canceled) {
					return &ExitError{Code: interruptCode(signalled())}
				}
				return err
			})
		},
	}
	cmd.Flags().UintVar(&tail, "tail", 0, "Print only the last N lines")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Keep printing until the job ends")
	return cmd
}

// logs prints job id's output (all of it with tail below 0, else the last
// tail lines) and, with follow, goes on printing until the job ends.
func (d *client) logs(out io.Writer, id sched.JobID, tail int, follow bool) error {
	v, ok := jobOf(d.Queue(), id)
	if !ok {
		return fmt.Errorf("there is no job %d", id)
	}
	off, err := tailOffsetOf(v.LogPath, tail)
	if err != nil {
		return err
	}
	f := &jobFollower{d: d, id: id, out: out, off: off, quiet: true}
	if !follow {
		return f.copyLog(v.LogPath)
	}
	_, err = f.run()
	return err
}

// tailOffsetOf is where the last n lines of the file at path begin (see
// tailOffset); a log that does not exist yet begins at 0.
func tailOffsetOf(path string, n int) (int64, error) {
	if n < 0 || path == "" {
		return 0, nil
	}
	file, err := os.Open(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return 0, nil
	case err != nil:
		return 0, fmt.Errorf("read the job's output: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return 0, fmt.Errorf("read the job's output: %w", err)
	}
	return tailOffset(file, info.Size(), n)
}

// tailOffset is the offset in r (size bytes long) where its last n lines
// begin: 0 when it has no more than n lines, or when n is negative (all of
// it); size when n is 0. A final newline ends the last line rather than
// starting another. It reads backwards in blocks, so a large log costs only
// its tail.
func tailOffset(r io.ReaderAt, size int64, n int) (int64, error) {
	if n < 0 {
		return 0, nil
	}
	if n == 0 {
		return size, nil
	}
	end := size
	if size > 0 {
		last := make([]byte, 1)
		if _, err := r.ReadAt(last, size-1); err != nil && !errors.Is(err, io.EOF) {
			return 0, fmt.Errorf("read the job's output: %w", err)
		}
		if last[0] == '\n' {
			end--
		}
	}
	buf := make([]byte, 32<<10)
	seen := 0
	for end > 0 {
		step := min(int64(len(buf)), end)
		start := end - step
		if _, err := r.ReadAt(buf[:step], start); err != nil && !errors.Is(err, io.EOF) {
			return 0, fmt.Errorf("read the job's output: %w", err)
		}
		for i := step - 1; i >= 0; i-- {
			if buf[i] != '\n' {
				continue
			}
			seen++
			if seen == n {
				return start + i + 1, nil
			}
		}
		end = start
	}
	return 0, nil
}
```

The exit code of a job killed by a signal comes from the signal's number, which the platform names: create `internal/cli/job_unix.go`, built everywhere but Windows, where `golang.org/x/sys/unix` (vendored) holds `SignalNum`, the inverse of the `unix.SignalName` that the runner recorded `Exit.Signal` with. A signal `unix.SignalName` cannot name is recorded as `SIG<n>`, so `signalExit` reads N out of such a name when `SignalNum` gives 0:

```go
//go:build !windows

package cli

import (
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// signalExit is 128 plus the number of the signal named name ("SIGKILL"), as
// a shell reports a command killed by it. name is what the runner recorded:
// unix.SignalName's name, whose inverse unix.SignalNum is, or "SIG<n>" for a
// signal unix cannot name (a Linux real-time one), whose n is read from it.
// A name that is neither gives exitNotRun.
func signalExit(name string) int {
	if sig := unix.SignalNum(name); sig != 0 {
		return 128 + int(sig)
	}
	if digits, ok := strings.CutPrefix(name, "SIG"); ok {
		if n, err := strconv.Atoi(digits); err == nil && n > 0 {
			return 128 + n
		}
	}
	return exitNotRun
}
```

and `internal/cli/job_windows.go`, its stub: no job exists there (`checkPlatform` refuses every `loom job` command first), so no signal name ever reaches it.

```go
//go:build windows

package cli

// signalExit is the Windows stub: the work queue is unavailable there, so no
// job records a signal. Every name is one it cannot map.
func signalExit(string) int { return exitNotRun }
```

In `main.go`, a follower ends with the job's exit code. Replace

```go
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
```

with

```go
	if err := rootCmd.Execute(); err != nil {
		// A job follower ends with the job's own exit code, having said what
		// happened on stderr as it went (cli.ExitError).
		var exit *cli.ExitError
		if errors.As(err, &exit) {
			if exit.Msg != "" {
				fmt.Fprintln(os.Stderr, exit.Msg)
			}
			os.Exit(exit.Code)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
```

- [ ] **Step 4: Run it to see it pass**

Run: `CGO_ENABLED=0 go test ./internal/cli/ -run 'TestJob' -count=1`

Expected: `ok  	github.com/aidan-bailey/loom/internal/cli` (about 40 s: each real job settles on the daemon's 3 s gate). Then the race check on the follower and its signal handling: `CC=clang CGO_ENABLED=1 go test -race ./internal/cli/ -run 'TestJob' -count=1` prints the same `ok`.

Then the gates: the gofmt check from Task 5.1 Step 4, `CGO_ENABLED=0 go vet ./...` and `CGO_ENABLED=0 go test ./...` print no failure, and so do `GOOS=windows CGO_ENABLED=0 go build ./...` and `GOOS=darwin CGO_ENABLED=0 go build ./...` (the signal mapping sits in platform files).

- [ ] **Step 5: Commit**

```bash
git add -N internal/cli/job_unix.go internal/cli/job_windows.go
git commit -m "feat(cli): loom job run, wait and logs, with the follower's exit codes" -- internal/cli/job.go internal/cli/job_unix.go internal/cli/job_windows.go internal/cli/job_test.go main.go
```

### Task 5.3: The context paragraph

**Files:**
- Modify: `session/claude-loom-context.md:13`
- Modify: `session/claude-loom-context-workspace.md:11`
- Test: `internal/cli/job_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/cli/job_test.go`, replace the import lines

```go
	"path/filepath"
	"strings"
	"syscall"
```

with

```go
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
```

and the import line

```go
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/session/claudemod"
```

with

```go
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/claudemod"
```

Append to the end of `internal/cli/job_test.go`:

```go
// The paragraph both context files give an agent tells it how to submit work
// and which flags claim resources. A flag renamed on the commands but not
// there would send agents to a command that refuses it, so every flag the
// paragraph names must exist on both `submit` and `run`. The closing sentence
// no longer promises that builds and tests run directly, which the paragraph
// contradicts (spec §8).
func TestJobContext_NamesOnlyFlagsTheCommandsHave(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, session.WriteLoomContextFiles(dir))
	flagName := regexp.MustCompile(`--[a-z]+`)

	for name, closing := range map[string]string{
		"claude-loom-context.md":           "Everything else — editing files, committing, diffing, and viewing history within your branch — works exactly as normal.",
		"claude-loom-context-workspace.md": "Editing files, committing, and viewing diffs/history all work exactly as normal.",
	} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err)
		text := string(b)
		start := strings.Index(text, "**Heavy work goes through loom's work queue.**")
		require.GreaterOrEqual(t, start, 0, "%s carries the paragraph", name)
		para, _, _ := strings.Cut(text[start:], "\n")
		assert.True(t, strings.HasSuffix(strings.TrimSpace(text), closing), "%s ends with %q", name, closing)
		assert.NotContains(t, text, "running builds and tests", name)

		assert.Contains(t, para, `loom job submit -d "<what>" -- <cmd>`, name)
		assert.Contains(t, para, `loom job run -d "<what>" -- <cmd>`, name)
		flags := flagName.FindAllString(para, -1)
		assert.ElementsMatch(t, []string{"--cpus", "--mem", "--io", "--exclusive"}, flags, name)
		for _, flag := range flags {
			for _, command := range []*cobra.Command{newJobSubmitCmd(), newJobRunCmd()} {
				assert.NotNil(t, command.Flags().Lookup(strings.TrimPrefix(flag, "--")), "%s: loom job %s has no %s", name, command.Name(), flag)
			}
		}
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `CGO_ENABLED=0 go test ./internal/cli/ -run 'TestJobContext' -count=1`

Expected: `FAIL`, with `claude-loom-context.md carries the paragraph` (`Error: "-1" is not greater than or equal to "0"`) for whichever of the two files the loop reaches first.

- [ ] **Step 3: Implement**

In `session/claude-loom-context.md`, the paragraph goes between the list and the closing line, and the closing sentence drops "running builds and tests", which the paragraph contradicts (spec §8; no test in `session/` pins either text, `loom_context_test.go` checks only a heading). Replace the last line

```
Everything else — editing files, running builds and tests, committing, diffing, and viewing history within your branch — works exactly as normal.
```

with

```
**Heavy work goes through loom's work queue.** This machine is shared by many loom sessions. Run builds, full test suites and benchmarks with `loom job submit -d "<what>" -- <cmd>` and carry on: a `[loom] job N` message brings the result. Or use `loom job run -d "<what>" -- <cmd>` with Bash's `run_in_background` when you need the output to continue. A job gets 4 cores and 6 GB unless you claim more with `--cpus`/`--mem`, `--io` for full builds that write a lot, or `--exclusive` for benchmarks. Quick commands (`cargo check`, linters, git) run directly.

Everything else — editing files, committing, diffing, and viewing history within your branch — works exactly as normal.
```

In `session/claude-loom-context-workspace.md`, replace the last line

```
Editing files, running builds and tests, committing, and viewing diffs/history all work exactly as normal.
```

with

```
**Heavy work goes through loom's work queue.** This machine is shared by many loom sessions. Run builds, full test suites and benchmarks with `loom job submit -d "<what>" -- <cmd>` and carry on: a `[loom] job N` message brings the result. Or use `loom job run -d "<what>" -- <cmd>` with Bash's `run_in_background` when you need the output to continue. A job gets 4 cores and 6 GB unless you claim more with `--cpus`/`--mem`, `--io` for full builds that write a lot, or `--exclusive` for benchmarks. Quick commands (`cargo check`, linters, git) run directly.

Editing files, committing, and viewing diffs/history all work exactly as normal.
```

- [ ] **Step 4: Run it to see it pass**

Run: `CGO_ENABLED=0 go test ./internal/cli/ ./session/ -run 'TestJobContext|TestWriteLoomContextFiles|TestLoomContextProgram' -count=1`

Expected: `ok  	github.com/aidan-bailey/loom/internal/cli` and `ok  	github.com/aidan-bailey/loom/session`. Then the gates: the gofmt check from Task 5.1 Step 4 and `CGO_ENABLED=0 go vet ./...` print nothing.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(session): tell agents to queue heavy work with loom job" -- session/claude-loom-context.md session/claude-loom-context-workspace.md internal/cli/job_test.go
```

### Task 5.4: The restart e2e

**Files:**
- Create: `e2e/job_test.go`
- Modify: `e2e/CLAUDE.md:7`, `e2e/CLAUDE.md:17`

- [ ] **Step 1: Write the failing test**

Create `e2e/job_test.go`:

```go
//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A job outlives the daemon that started it: its runner is in a session of
// its own, so `loom serve stop` leaves it running, and the next daemon (the
// next command's join starts it) adopts it at boot and settles it, without
// starting it again. `loom job wait` returns the job's own exit code.
func TestE2E_Job_SurvivesADaemonRestart(t *testing.T) {
	sb := newSandbox(t, "")
	marks := t.TempDir()
	runs := filepath.Join(marks, "runs")
	// The job counts its starts, says it is up, outlasts the restart and
	// exits 7.
	script := `echo run >> "$1/runs"; echo up; sleep 8; echo finished; exit 7`
	job := func(args ...string) (string, error) {
		// An empty LOOM_LAUNCH makes the command a plain shell's, whatever
		// shell runs the suite.
		return runLoom(t, sb, append([]string{"env", "LOOM_LAUNCH=", sb.LoomBin(), "job"}, args...)...)
	}

	out, err := job("submit", "-d", "restart", "--no-notify", "--", "sh", "-c", script, "sh", marks)
	require.NoError(t, err, out)
	assert.Contains(t, out, "job 1 started")
	require.Eventually(t, func() bool {
		_, err := os.Stat(runs)
		return err == nil
	}, uiTimeout, 100*time.Millisecond, "the job never began to run")
	rec := servingDaemon(t, sb)
	started, err := sched.ReadStarted(sched.JobDir(filepath.Join(sb.GlobalDir(), "jobs"), 1))
	require.NoError(t, err)

	stopped, err := runLoom(t, sb, sb.LoomBin(), "serve", "stop")
	require.NoError(t, err, stopped)
	require.True(t, waitExit(rec.PID, uiTimeout))
	_, held := sb.Daemon()
	require.False(t, held, "no daemon runs")
	assert.True(t, alive(started.PID), "the job's runner outlives the daemon that started it")

	// This join starts the next daemon, which adopts the job.
	out, err = job("wait", "1")
	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit, out)
	assert.Equal(t, 7, exit.ExitCode(), out)
	assert.Contains(t, out, "finished", "the job's output reaches stdout")
	assert.Contains(t, out, "job 1 failed in")
	assert.Contains(t, out, "(exit 7)")
	servingDaemon(t, sb, rec.PID)
	got, err := os.ReadFile(runs)
	require.NoError(t, err)
	assert.Equal(t, "run\n", string(got), "the new daemon adopted the job and did not start it again")
}
```

- [ ] **Step 2: Run it to see it fail**

The test needs no new code: it drives the commands of Tasks 5.1 and 5.2 against real builds of the daemon (Stories 2 and 3), so there is no red state to see. Check that it is collected and compiles with `CGO_ENABLED=0 go test -tags e2e ./e2e/... -run Job -list .`, which prints `TestE2E_Job_SurvivesADaemonRestart`.

- [ ] **Step 3: Implement**

The test is the implementation. Only the suite's notes change: `e2e/CLAUDE.md` lists what each e2e file covers and the rules of writing one.

In `e2e/CLAUDE.md`, add the file's coverage after the `e2e/web_test.go` line. Replace the end of line 7

```
and `loom web revoke` ending the stream and the cookie.
```

with

```
and `loom web revoke` ending the stream and the cookie.
`e2e/job_test.go` covers `loom job` across a daemon restart: a job submitted through the CLI keeps running when `loom serve stop` ends the daemon, the next daemon (started by the next command's join) adopts and settles it without starting it again, and `loom job wait` prints its output and exits with its exit code.
```

and add a rule after the last one. Replace the end of line 17

```
a regression nothing else catches, since no CI workflow runs this suite.
```

with

```
a regression nothing else catches, since no CI workflow runs this suite.
- **Prove a restarted daemon adopted a job rather than started it again by counting the job's own starts.** The job appends a line to a marker file when it begins and `TestE2E_Job_SurvivesADaemonRestart` asserts exactly one line after `loom job wait` returns; a daemon that started the job a second time would still pass on its output and exit code. The job outlasts the restart by a sleep, so a slow machine can only make the restart land earlier in it. **Enforced** by that test's marker assertion.
```

- [ ] **Step 4: Run it to see it pass**

Run: `CGO_ENABLED=0 go test -tags e2e ./e2e/... -run Job -count=1`

Expected: `ok  	github.com/aidan-bailey/loom/e2e` (about 40 s: it builds loom, starts a sandbox daemon, stops it under the job and starts another). The test needs tmux, git and go; without them it skips. Then the gates: the gofmt check from Task 5.1 Step 4 and `CGO_ENABLED=0 go vet ./...` (the vet run without the tag does not see this file; also run `CGO_ENABLED=0 go vet -tags e2e ./e2e/...`) print nothing, and `CGO_ENABLED=0 go test ./tools/claudemd/ -run TestRepo_ClaudeMDStructure -count=1` prints `ok  	github.com/aidan-bailey/loom/tools/claudemd`.

- [ ] **Step 5: Commit**

```bash
git add -N e2e/job_test.go
git commit -m "test(e2e): a daemon restarted while a job runs" -- e2e/job_test.go e2e/CLAUDE.md
```

### Task 5.5: The package README and CLAUDE.md

**Files:**
- Modify: `internal/cli/README.md:3`, `internal/cli/README.md:14`, `internal/cli/README.md:43`, `internal/cli/README.md:49`
- Modify: `internal/cli/CLAUDE.md:3`, `internal/cli/CLAUDE.md:27-33`

- [ ] **Step 1: Write the failing test**

The README and the CLAUDE.md are prose, checked only by the CLAUDE.md lint (`TestRepo_ClaudeMDStructure` in `tools/claudemd`: its links, paths and test names) and `go run ./tools/claudemd -idents` (the symbols it names), so there is no test to write first.

- [ ] **Step 2: Run it to see it fail**

Nothing fails before the edit: the lint passes without the new text too. Go to Step 3.

- [ ] **Step 3: Implement**

In `internal/cli/README.md`, replace the phrase in line 3

```
`loom workspace`, `loom account`, `loom session` and `loom web` subcommands
```

with

```
`loom workspace`, `loom account`, `loom session`, `loom web` and `loom job` subcommands
```

and in the same line replace

```
`cli.SessionCmd`, `cli.WebCmd`)
```

with

```
`cli.SessionCmd`, `cli.WebCmd`, `cli.JobCmd`)
```

Replace the table row (line 14)

```
| `reset.go` | `Reset`, the client half of `loom reset` while a daemon runs |
```

with

```
| `job.go` | `JobCmd` (built by `newJobCmd`) and `submit`, `run`, `wait`, `logs`, `list`, `cancel`; `claimFlags` (`submission`: the argv, directory, environment and `$LOOM_LAUNCH` a job is sent with), `client.submitJob`, `client.awaitJob`, `jobFollower` (the follower of `run`, `wait` and `logs -f`), `jobOutcome` (a job's last line and exit code), `ExitError` (a command's exit code, which `main.go` honours), `interruptible` and `notifySignals` (the interrupt), `tailOffset` |
| `job_unix.go`, `job_windows.go` | `signalExit` (the name of the signal that killed a job to the exit code 128 + its number, through `unix.SignalNum`, or the n of a `SIG<n>` name; the Windows stub maps none) |
| `reset.go` | `Reset`, the client half of `loom reset` while a daemon runs |
```

Replace the table row (line 43)

```
| `web status` | `Web()` (read only) |
```

with

```
| `web status` | `Web()` (read only) |
| `job submit` | `SubmitJob` and a wait for its `Reply` (`submitJob`), then for the replica to hold the job (`awaitJob`); prints where it stands |
| `job run` | `SubmitJob` without a message, then follows the job (`follow`) and exits with its code |
| `job wait` | follows an existing job (`follow`); an interrupt asks for a message (`NotifyJob`) when the job has an owner and has not ended |
| `job logs` | `Queue()` for the log's path, then the file on disk; `-f` follows until the job ends |
| `job list` | `Queue()` (read only) |
| `job cancel` | `CancelJob` with `CancelFromCLI`, then a wait for its `Reply` (`request`) |
```

Replace the last sentence of the Tests section (line 49)

```
`e2e/clients_test.go` runs them against real builds.
```

with

```
`e2e/clients_test.go` runs them against real builds.

The job tests (`job_test.go`) use the same test daemon, with its jobs started through this test binary as their shim (`serveDaemon` installs `sched.TestRunner()` before the model boots, with `sched.MaybeRunTestShim()` first in `TestMain`), so a job is a real process group that the daemon settles on its 3 s queue gate; they wait for an end through the daemon's view (`waitFinished`) and deliver the follower's interrupt through `notifySignals`. `e2e/job_test.go` restarts a real daemon under a running job.
```

In `internal/cli/CLAUDE.md`, line 3 names the commands. Replace

```
The `loom workspace`, `loom account`, `loom session`, `loom web` and `loom sweep` subcommands (`WorkspaceCmd` in `workspace.go`, `AccountCmd` in `account.go`, `SessionCmd` in `session.go`, `WebCmd` in `web.go`, `SweepCmd` in `sweep.go`) and the daemon half
```

with

```
The `loom workspace`, `loom account`, `loom session`, `loom web`, `loom sweep` and `loom job` subcommands (`WorkspaceCmd` in `workspace.go`, `AccountCmd` in `account.go`, `SessionCmd` in `session.go`, `WebCmd` in `web.go`, `SweepCmd` in `sweep.go`, `JobCmd` in `job.go`) and the daemon half
```

Then add the job rules after the last rule. Replace

```
`TestWebEnable_AgainRetriesAFailedBind` and `TestWebEnable_WhenItAlreadyServesChangesNothing`.

## Pointers
```

with

```
`TestWebEnable_AgainRetriesAFailedBind` and `TestWebEnable_WhenItAlreadyServesChangesNothing`.
- **Follow a job in one loop (`jobFollower.run`): read its view first, then copy what `output.log` gained, and end only on a finished view, the daemon's loss or the command's interrupt.** A job seen finished has a complete log only when the view was read before the log, and the log grows with no event, so the loop also polls (`logPoll`). A wait on the next event alone stalls on a running job; a log read before the view can miss a finished job's last lines. **Enforced** by `TestJobRun_FollowsAJobToItsExitCodeAndLogsReadItBack` and `TestJobCopyLog_CopiesOnlyWhatIsNew`; **Convention** for the order — a missing last line.
- **Exit with the job's own code, through `*ExitError`, which `main.go` turns into the process's exit code: the job's exit code, 128 + N for a job killed by signal N (`signalExit`: `unix.SignalNum` of `Exit.Signal`, else the n of a `SIG<n>` name, in `job_unix.go`), `exitNotRun` (125) for a job that was lost, could not start, was cancelled, or was killed by a name the platform does not map.** A shell or a Bash tool reads the code as the command's own, and a follower that exits 1 for all of them hides whether the build failed or never ran. **Enforced** by `TestJobOutcome`, `TestJobInterruptCode` and `TestJobWait_ACancelledJobExits125`; **Convention** for the `ExitError` branch of `main` — every outcome exiting 1.
- **Let an interrupt end the follower, never the job (`notifySignals`: `signal.Notify` for SIGINT, SIGTERM and SIGHUP, released with `signal.Stop`), and exit 128 + the signal's number.** A Bash timeout sends SIGTERM to `loom job run`, and the build it queued is wanted; the follower asks for a message (`NotifyJob`) only for a job that has an owner and has not ended, and says so only when the daemon took the request. **Enforced** by `TestJobRun_AnInterruptWithNoOwnerSaysSo` and `TestJobRun_AnInterruptAsksForAMessageWhenTheJobHasAnOwner`.
- **Send `os.Environ()` as the job's environment, as data (`submission`), and refuse every `loom job` command on Windows with `sched.ErrUnsupported` (`beginJob`, `checkPlatform`).** The daemon starts the job in the submitter's environment, never its own; it has no scheduler on Windows, and the signal mapping sits in `job_unix.go` and `job_windows.go`. **Enforced** by `TestJobSubmission_CapturesTheShellItRunsIn` and `TestJobCheckPlatform`; **Convention** for the cross-build — `GOOS=windows CGO_ENABLED=0 go build ./...` after touching those files, since only the release builds Windows.
- **Start the test daemon's jobs through the test binary: `TestMain` calls `sched.MaybeRunTestShim()` first, and `serveDaemonOn` installs `sched.TestRunner()` with `SetJobRunnerForTest` before `Boot`.** A job in a test is a real process group of this binary; a runner installed after `Boot` would not be the one that adopts and starts jobs. **Convention** — every job test failing with a job that could not start.

## Pointers
```

and the pointer to the queue's own rules. Replace

```
- [`../../account/CLAUDE.md`](../../account/CLAUDE.md) — the registry and link rules the daemon applies for them.
```

with

```
- [`../../account/CLAUDE.md`](../../account/CLAUDE.md) — the registry and link rules the daemon applies for them.
- [`../../sched/CLAUDE.md`](../../sched/CLAUDE.md) — the work queue's runner and records that `loom job` drives.
```

- [ ] **Step 4: Run it to see it pass**

Run: `CGO_ENABLED=0 go test ./tools/claudemd/ -run TestRepo_ClaudeMDStructure -count=1 && CGO_ENABLED=0 go run ./tools/claudemd -v && CGO_ENABLED=0 go run ./tools/claudemd -idents`

Expected: `ok  	github.com/aidan-bailey/loom/tools/claudemd`; `-v` shows `internal/cli/CLAUDE.md` within 100 lines and 20,000 bytes (it ends near 40 lines and 16,000 bytes); and `-idents` lists no symbol of `internal/cli/README.md` or `internal/cli/CLAUDE.md` as missing.

- [ ] **Step 5: Commit**

```bash
git commit -m "docs(cli): loom job in the package README and CLAUDE.md" -- internal/cli/README.md internal/cli/CLAUDE.md
```

## Story 6: The work view: the view mode, `w`, the log tail and pager, and the caps form

- **Developer:** intermediate-dev
- **Files:** `app/work.go`, `app/work_test.go`, `app/app.go`, `app/app_scripts.go`, `app/state_default.go`, `app/overview.go`, `app/requests.go`, `app/overlay_host.go`, `ui/work.go`, `ui/work_test.go`, `ui/overlay/workCapsOverlay.go`, `ui/overlay/workCapsOverlay_test.go`, `script/host.go`, `script/host_fake_test.go`, `script/api_actions.go`, `script/api_actions_test.go`, `script/defaults.lua`
- **Interfaces:** exposes `viewWork`, `toggle_work_view`, `ToggleWorkView` and `handleWorkKey` of the names contract (TUI); uses Story 4's `home.queue`, `QueueView`, `JobView` and the `CancelJob`, `MoveJobToFront` and `SetSchedulerCaps` requests
- **Verify:** `CGO_ENABLED=0 go test ./app/... ./ui/... ./script/...`
- **Review:** deep

The work view is the TUI's fourth view mode, drawn from `home.queue` alone: a header with the host, capacity bars (or the reason admission is paused in their place), the RUNNING, QUEUED and RECENT sections with why each queued job waits, and the selected job's last log lines, read from `output.log` in a `tea.Cmd`. `enter` opens a scrolling pager over the log, `x` cancels after a confirm, `t` moves a queued job to the front, `o` focuses the owner's session (opening its workspace's tab first when none shows it), `e` opens the caps form, which sends `SetSchedulerCaps` and shows the model's refusal in the form, and `w` or `esc` returns to the mode the view came from. The view is not persisted and drops mouse input, as overview does. Seven tasks: the rendering in `ui` (6.1), the caps form in `ui/overlay` (6.2), the view mode with its selection, tail and pager in `app` (6.3), the caps form's state and reply routing (6.4), the actions `x`, `t` and `o` (6.5), `w` through the script engine (6.6), and keeping the view open when a workspace slot loads under it (6.7). Rules the tests pin: a slot load while the view is open (a rejoin or a `WorkspacesChanged` closing a tab) leaves it open and only changes the mode `w` or `esc` returns to; the selection is the job's ID, re-resolved from `home.queue` on every read (a job leaving the view moves it to the row that slid into its place); `x`, `t` and `o` act on the ID taken when the key passed, never on the selection a later drain may have moved; every request carries a `ReqID` (`newReq`), and a refusal reaches the user through it; a key that needs the model is refused offline; the caps form is a modal state (`stateWorkCaps`) in `handleMenuHighlighting`'s exclusion list and `View()`'s overlay list; everything an agent wrote (descriptions, titles, log lines) is cleaned of escapes and control characters before it is drawn. No code here holds one of the profile's landmine markers, so no `effort: xhigh` is needed. `USAGE.md`'s Keyboard Reference and `docs/specs/scripting.md` list `w` and `toggle_work_view`: Story 7 writes them, and `app/CLAUDE.md` gets no rule.

### Task 6.1: Render the work view and its pager (`ui/work.go`)

**Files:**
- Create: `ui/work.go`
- Test: `ui/work_test.go`

- [ ] **Step 1: Write the failing test**

Create `ui/work_test.go`:

```go
package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/sched"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var workNow = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

func workRunning(id sched.JobID, desc string, started time.Duration) core.JobView {
	j := core.JobView{ID: id, State: sched.Running, Description: desc, OwnerTitle: "kalshi", OwnerWorkspace: "polyoxide"}
	j.Owner = 7
	j.Claim = sched.Claim{CPUs: 4, MemoryGB: 6}
	j.Started = workNow.Add(-started)
	j.Submitted = j.Started.Add(-time.Minute)
	j.CPU = 3.2
	j.PeakRSSKB = 5_347_738
	return j
}

func workQueued(id sched.JobID, desc, waiting string, waited time.Duration) core.JobView {
	j := core.JobView{ID: id, State: sched.Queued, Description: desc, OwnerTitle: "wasm", OwnerWorkspace: "polyoxide", Waiting: waiting}
	j.Owner = 8
	j.Claim = sched.Claim{CPUs: 4, MemoryGB: 6}
	j.Submitted = workNow.Add(-waited)
	return j
}

func workFinished(id sched.JobID, desc string, state sched.State, code int, ago, took time.Duration) core.JobView {
	j := core.JobView{ID: id, State: state, Description: desc, OwnerTitle: "wasm", OwnerWorkspace: "polyoxide"}
	j.Owner = 8
	j.Claim = sched.Claim{CPUs: 4, MemoryGB: 6}
	j.Ended = workNow.Add(-ago)
	j.Started = j.Ended.Add(-took)
	j.Submitted = j.Started.Add(-time.Minute)
	j.Exit = &sched.Exit{Code: code, Ended: j.Ended}
	return j
}

// workBusy is the busy queue of the design's mock: two running jobs, two
// queued ones and two finished.
func workBusy() core.QueueView {
	q := core.QueueView{Dir: "/home/x/.loom/jobs"}
	q.Host = sched.Host{Name: "fresco", Cores: 16, RAMBytes: 30 << 30}
	q.Policy.Caps = sched.Caps{CPUs: 14, MemoryGB: 22, IOJobs: 2}
	q.Claimed = sched.Need{CPUs: 10, MemoryGB: 12, IOSlots: 1}
	q.UsedCPU, q.UsedRSSKB = 11.2, 10_276_045
	r15 := workRunning(15, "go test ./...", 42*time.Second)
	r15.OwnerTitle, r15.OwnerWorkspace = "work-scheduler", "loom"
	r15.Claim = sched.Claim{CPUs: 4, MemoryGB: 4}
	r15.PeakRSSKB = 1_258_291
	q16 := workQueued(16, "release build", "needs 8 cores (4 free)", 2*time.Minute)
	q16.Claim = sched.Claim{CPUs: 8, MemoryGB: 12, IO: true}
	q17 := workQueued(17, "bench orderbook", "waits for job 16, which has waited past 10m", time.Minute)
	q17.Claim = sched.Claim{Exclusive: true}
	q.Jobs = []core.JobView{
		workRunning(14, "polyoxide tests", 190*time.Second),
		r15,
		q16,
		q17,
		workFinished(13, "clippy", sched.Done, 0, 2*time.Minute, 62*time.Second),
		workFinished(12, "cargo test", sched.Failed, 101, 10*time.Minute, 273*time.Second),
	}
	return q
}

func lineWith(t *testing.T, out, sub string) string {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, sub) {
			return l
		}
	}
	t.Fatalf("no line holds %q in:\n%s", sub, out)
	return ""
}

func TestWorkRows_SectionsInOrderAndRecentCapped(t *testing.T) {
	var jobs []core.JobView
	for i := 1; i <= 12; i++ { // finished, newest first, as the queue view lists them
		jobs = append(jobs, workFinished(sched.JobID(100-i), "f", sched.Done, 0, time.Duration(i)*time.Minute, time.Second))
	}
	jobs = append(jobs, workQueued(30, "q30", "", time.Minute), workRunning(20, "r20", time.Minute), workQueued(31, "q31", "", time.Minute), workRunning(21, "r21", time.Minute))

	var ids []sched.JobID
	for _, j := range WorkRows(jobs) {
		ids = append(ids, j.ID)
	}
	assert.Equal(t, []sched.JobID{20, 21, 30, 31, 99, 98, 97, 96, 95, 94, 93, 92, 91, 90}, ids,
		"running, queued, then the newest ten finished, each section in the order the view gave")
}

func TestRenderWork_ShowsTheBarsTheSectionsAndTheTail(t *testing.T) {
	out := ansi.Strip(RenderWork(120, 30, WorkData{Queue: workBusy(), Selected: 14, Tail: WorkTail{ID: 14, Lines: []string{"test orderbook::depth ... ok"}}, Now: workNow}))
	for _, want := range []string{
		"Work queue · fresco", "w/esc back",
		"CPU ██████████░░░░ 10/14 claimed · 11.2 in use",
		"MEM ████████░░░░░░ 12/22 GB claimed · 9.8 GB in use",
		"I/O ██░░", "1/2",
		"polyoxide tests", "kalshi · polyoxide", "4c 6G", "3m10s", "peak 5.1G",
		"go test ./...", "work-scheduler · loom", "0m42s", "peak 1.2G",
		"release build", "8c 12G io", "needs 8 cores (4 free)",
		"bench orderbook", "exclusive", "waits for job 16",
		"clippy", "✓ 0", "1m02s", "2m ago",
		"cargo test", "✗ 101", "4m33s", "10m ago",
		"── job 14 · tail", "test orderbook::depth ... ok",
	} {
		assert.Contains(t, out, want)
	}
	rest := out
	for _, section := range []string{"RUNNING", "QUEUED", "RECENT"} {
		i := strings.Index(rest, section)
		require.GreaterOrEqual(t, i, 0, section)
		rest = rest[i+len(section):]
	}
}

func TestRenderWork_TheMarkerFollowsTheSelectedJobsID(t *testing.T) {
	for _, id := range []sched.JobID{14, 15, 16} {
		out := ansi.Strip(RenderWork(120, 30, WorkData{Queue: workBusy(), Selected: id, Now: workNow}))
		var marked []string
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, "▸") {
				marked = append(marked, line)
			}
		}
		require.Len(t, marked, 1, "job %d", id)
		assert.Contains(t, marked[0], fmt.Sprintf(" %d ", id))
	}
}

func TestRenderWork_AWindowKeepsTheSelectedJobInView(t *testing.T) {
	q := core.QueueView{Dir: "/d"}
	q.Host = sched.Host{Name: "h", Cores: 4}
	q.Policy.Caps = sched.Caps{CPUs: 2, MemoryGB: 2, IOJobs: 1}
	for i := 1; i <= 20; i++ {
		q.Jobs = append(q.Jobs, workRunning(sched.JobID(i), fmt.Sprintf("d%02d", i), time.Minute))
	}
	last := ansi.Strip(RenderWork(120, 14, WorkData{Queue: q, Selected: 20, Now: workNow}))
	assert.Contains(t, last, "d20")
	assert.NotContains(t, last, "d01")
	first := ansi.Strip(RenderWork(120, 14, WorkData{Queue: q, Selected: 1, Now: workNow}))
	assert.Contains(t, first, "d01")
	assert.NotContains(t, first, "d20")
}

func TestRenderWork_FillsExactlyItsBox(t *testing.T) {
	busy := WorkData{Queue: workBusy(), Selected: 14, Tail: WorkTail{ID: 14, Lines: []string{"x"}}, Now: workNow}
	silent := WorkData{Queue: core.QueueView{}, Now: workNow}
	for _, size := range [][2]int{{120, 30}, {100, 24}, {60, 12}, {40, 8}} {
		for _, d := range []WorkData{busy, silent} {
			out := RenderWork(size[0], size[1], d)
			assert.Equal(t, size[1], lipgloss.Height(out), "height at %v", size)
			assert.LessOrEqual(t, lipgloss.Width(out), size[0], "width at %v", size)
		}
	}
}

func TestRenderWork_PausedShowsTheReasonInPlaceOfTheBars(t *testing.T) {
	q := workBusy()
	q.Paused = "scheduler.cpus is 2000: it must be 1–1024"
	q.Policy = sched.Policy{}
	out := ansi.Strip(RenderWork(120, 30, WorkData{Queue: q, Selected: 14, Now: workNow}))
	assert.Contains(t, out, "Admission paused: scheduler.cpus is 2000: it must be 1–1024")
	assert.NotContains(t, out, "claimed")
	assert.Contains(t, out, "polyoxide tests", "running jobs stay listed")
}

func TestRenderWork_NothingToShow(t *testing.T) {
	q := workBusy()
	q.Jobs = nil
	assert.Contains(t, ansi.Strip(RenderWork(120, 30, WorkData{Queue: q, Now: workNow})), "No jobs yet")
	out := ansi.Strip(RenderWork(120, 30, WorkData{Queue: core.QueueView{}, Now: workNow}))
	assert.Contains(t, out, "has not reported its work queue yet")
	assert.NotContains(t, out, "No jobs yet", "a queue that never reported is not an empty one")
}

func TestRenderWork_TextAnAgentWroteCannotReachTheTerminal(t *testing.T) {
	q := workBusy()
	q.Jobs[0].Description = "x\x1b[2Jevil\nnewline\x07"
	q.Jobs[0].OwnerTitle = "t\x1b]52;c;AAAA\x07"
	q.Jobs[2].Waiting = "w\x1b[31mred"
	out := RenderWork(120, 30, WorkData{Queue: q, Selected: 14, Now: workNow})
	for _, injected := range []string{"\x1b[2J", "\x1b]52", "\x07", "\x1b[31mred"} {
		assert.NotContains(t, out, injected)
	}
	plain := ansi.Strip(out)
	assert.Contains(t, plain, "evil newline", "the text stays, its controls are spaces")
	assert.Equal(t, 30, lipgloss.Height(out), "a newline in a description cannot add a row")
}

func TestRenderWork_AnUndeliveredNoticeIsMarked(t *testing.T) {
	q := workBusy()
	a := workFinished(20, "alpha", sched.Done, 0, time.Minute, time.Second)
	a.Notice, a.NoticeWhy = sched.NoticeUndelivered, "no live loom mod"
	b := workFinished(21, "bravo", sched.Done, 0, time.Minute, time.Second)
	b.Notice = sched.NoticeSent
	q.Jobs = []core.JobView{a, b}
	out := ansi.Strip(RenderWork(120, 30, WorkData{Queue: q, Selected: 20, Now: workNow}))
	assert.Contains(t, lineWith(t, out, "alpha"), "not delivered")
	assert.NotContains(t, lineWith(t, out, "bravo"), "not delivered")
	assert.Contains(t, lineWith(t, out, "── job 20"), "message not delivered: no live loom mod")
}

func TestRenderWork_FailedAndCancelledJobsSayHow(t *testing.T) {
	q := workBusy()
	start := workFinished(30, "nostart", sched.Failed, 0, time.Minute, 0)
	start.Exit, start.Started, start.Failure = nil, time.Time{}, "its folder is gone"
	killed := workFinished(31, "killed", sched.Failed, -1, time.Minute, time.Second)
	killed.Exit.Signal = "SIGKILL"
	cancelled := workFinished(32, "stopped", sched.Cancelled, 0, time.Minute, time.Second)
	cancelled.CancelReason = "from the work view"
	lost := workFinished(33, "vanished", sched.Lost, 0, time.Minute, time.Second)
	lost.Exit = nil
	q.Jobs = []core.JobView{start, killed, cancelled, lost}
	out := ansi.Strip(RenderWork(140, 30, WorkData{Queue: q, Now: workNow}))
	assert.Contains(t, lineWith(t, out, "nostart"), "✗ start")
	assert.Contains(t, lineWith(t, out, "nostart"), "its folder is gone")
	assert.Contains(t, lineWith(t, out, "killed"), "✗ SIGKILL")
	assert.Contains(t, lineWith(t, out, "stopped"), "cancelled")
	assert.Contains(t, lineWith(t, out, "stopped"), "from the work view")
	assert.Contains(t, lineWith(t, out, "vanished"), "lost")
}

func TestRenderWork_ACancelInFlightSaysSo(t *testing.T) {
	q := workBusy()
	q.Jobs[0].CancelReason = "from the work view"
	out := ansi.Strip(RenderWork(120, 30, WorkData{Queue: q, Selected: 14, Now: workNow}))
	assert.Contains(t, lineWith(t, out, "polyoxide tests"), "cancelling")
	assert.NotContains(t, lineWith(t, out, "go test ./..."), "cancelling")
}

func TestRenderWork_TheTailSaysWhatItCannotShow(t *testing.T) {
	render := func(selected sched.JobID, tail WorkTail) string {
		return ansi.Strip(RenderWork(120, 30, WorkData{Queue: workBusy(), Selected: selected, Tail: tail, Now: workNow}))
	}
	assert.Contains(t, render(16, WorkTail{}), "(queued: no output yet)")
	assert.Contains(t, render(14, WorkTail{ID: 15, Lines: []string{"another job"}}), "(reading the log…)", "a tail read for another job is not shown")
	assert.NotContains(t, render(14, WorkTail{ID: 15, Lines: []string{"another job"}}), "another job")
	assert.Contains(t, render(14, WorkTail{ID: 14, Err: "permission denied"}), "cannot read the log: permission denied")
	assert.Contains(t, render(14, WorkTail{ID: 14}), "(no output yet)")

	var lines []string
	for i := 1; i <= 20; i++ {
		lines = append(lines, fmt.Sprintf("l%d", i))
	}
	long := render(14, WorkTail{ID: 14, Lines: lines})
	assert.Contains(t, long, "l20")
	assert.Contains(t, long, "l13")
	assert.NotContains(t, long, "l12", "the tail keeps the last lines that fit")
}

func TestWorkReported(t *testing.T) {
	assert.False(t, WorkReported(core.QueueView{}))
	assert.True(t, WorkReported(core.QueueView{Host: sched.Host{Name: "fresco"}}))
	assert.True(t, WorkReported(core.QueueView{Dir: "/d"}))
	assert.True(t, WorkReported(core.QueueView{Jobs: []core.JobView{{ID: 1}}}))
}

func TestRenderWorkPager_ShowsTheWindowAndClampsTheTop(t *testing.T) {
	var lines []string
	for i := 1; i <= 100; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	job := workRunning(14, "polyoxide tests", time.Minute)
	body := WorkPagerBody(12)
	require.Equal(t, 11, body)

	out := ansi.Strip(RenderWorkPager(80, 12, WorkPagerData{Job: job, Lines: lines, Top: 20}))
	assert.Equal(t, 12, lipgloss.Height(out))
	assert.Contains(t, out, "job 14 · polyoxide tests · lines 21–31 of 100")
	assert.Contains(t, out, "esc back")
	assert.Contains(t, out, "line 21")
	assert.Contains(t, out, "line 31")
	assert.NotContains(t, out, "line 20\n")
	assert.NotContains(t, out, "line 32")

	end := ansi.Strip(RenderWorkPager(80, 12, WorkPagerData{Job: job, Lines: lines, Top: 1000}))
	assert.Contains(t, end, "lines 90–100 of 100", "a top past the end shows the last page")
	assert.Contains(t, end, "line 100")

	omitted := ansi.Strip(RenderWorkPager(80, 12, WorkPagerData{Job: job, Lines: lines, Omitted: true}))
	assert.Contains(t, omitted, "earlier output not shown")
}

func TestRenderWorkPager_NothingToShow(t *testing.T) {
	job := workRunning(14, "polyoxide tests", time.Minute)
	assert.Contains(t, ansi.Strip(RenderWorkPager(80, 12, WorkPagerData{Job: job})), "(no output yet)")
	failed := ansi.Strip(RenderWorkPager(80, 12, WorkPagerData{Job: job, Err: "permission denied"}))
	assert.Contains(t, failed, "cannot read the log: permission denied")
	assert.Equal(t, 12, lipgloss.Height(failed))
	for _, size := range [][2]int{{80, 12}, {30, 5}, {20, 1}} {
		out := RenderWorkPager(size[0], size[1], WorkPagerData{Job: job, Lines: []string{strings.Repeat("x", 300)}})
		assert.Equal(t, size[1], lipgloss.Height(out), "height at %v", size)
		assert.LessOrEqual(t, lipgloss.Width(out), size[0], "a long line is cut to the width at %v", size)
	}
}

func TestLogLines(t *testing.T) {
	for _, tc := range []struct {
		name    string
		in      string
		partial bool
		want    []string
	}{
		{"lines", "a\nb\n", false, []string{"a", "b"}},
		{"no final newline", "a\nb", false, []string{"a", "b"}},
		{"crlf", "a\r\nb\r\n", false, []string{"a", "b"}},
		{"a cut head drops its first line", "half\nfull\n", true, []string{"full"}},
		{"a whole head keeps its first line", "whole\nfull\n", false, []string{"whole", "full"}},
		{"colors", "\x1b[31mred\x1b[0m\n", false, []string{"red"}},
		{"a progress bar keeps its last state", "10%\r50%\r100%\ndone\n", false, []string{"100%", "done"}},
		{"tabs", "a\tb\n", false, []string{"a    b"}},
		{"controls", "bell\x07\n", false, []string{"bell"}},
		{"a blank line stays", "a\n\nb\n", false, []string{"a", "", "b"}},
		{"nothing", "", false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, LogLines([]byte(tc.in), tc.partial))
		})
	}
}

func TestJobLabel(t *testing.T) {
	assert.Equal(t, `job 14 "polyoxide tests"`, JobLabel(core.JobView{ID: 14, Description: "polyoxide tests"}))
	assert.Equal(t, `job 3 "a b"`, JobLabel(core.JobView{ID: 3, Description: "a\x1b[2J\nb"}), "controls become spaces")
	long := JobLabel(core.JobView{ID: 3, Description: strings.Repeat("x", 100)})
	assert.Equal(t, `job 3 "`+strings.Repeat("x", 59)+`…"`, long, "a long description is cut to 60 columns")
}

func TestWorkFormatters(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		dur  string
		wait string
		ago  string
	}{
		{10 * time.Second, "0m10s", "<1m", "just now"},
		{42 * time.Second, "0m42s", "<1m", "just now"},
		{62 * time.Second, "1m02s", "1m", "1m ago"},
		{190 * time.Second, "3m10s", "3m", "3m ago"},
		{65 * time.Minute, "1h05m", "1h05m", "1h ago"},
		{72 * time.Hour, "72h00m", "72h00m", "3d ago"},
	} {
		assert.Equal(t, tc.dur, workDur(tc.d), "dur %v", tc.d)
		assert.Equal(t, tc.wait, workWait(tc.d), "wait %v", tc.d)
		assert.Equal(t, tc.ago, workAgo(tc.d), "ago %v", tc.d)
	}
	assert.Equal(t, "0m00s", workDur(-time.Second), "a clock that ran backwards shows zero")
	assert.Equal(t, "5.1G", workKB(5_347_738))
	assert.Equal(t, "640M", workKB(640<<10))
	assert.Equal(t, "12K", workKB(12))
	assert.Equal(t, "9.8 GB", workGB(10_276_045))
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `CGO_ENABLED=0 go test ./ui/ -run 'TestWork|TestRenderWork|TestLogLines|TestJobLabel'`

Expected: the package fails to build, printing `undefined: WorkRows`, `undefined: RenderWork`, `undefined: WorkData`, `undefined: WorkTail`, `undefined: WorkReported`, `undefined: RenderWorkPager`, `undefined: WorkPagerData`, `undefined: WorkPagerBody`, `undefined: LogLines`, `undefined: JobLabel`, `undefined: workDur`, `undefined: workWait`, `undefined: workAgo`, `undefined: workKB`, `undefined: workGB`, and then `FAIL github.com/aidan-bailey/loom/ui [build failed]`.

- [ ] **Step 3: Implement**

Create `ui/work.go`:

```go
package ui

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/sched"
	"github.com/charmbracelet/x/ansi"
)

// WorkRecentMax is how many finished jobs the work view's RECENT section
// lists: the newest ten.
const WorkRecentMax = 10

// workBarCells is the width of a capacity bar.
const workBarCells = 14

// The fixed columns of a job row.
const (
	workIDW    = 4
	workClaimW = 11
	workTimeW  = 6
)

// workFixed is what a row spends on the marker, the ID, the claim, the time
// and the gaps between the columns: the rest goes to the description, the
// owner and the last column.
const workFixed = 1 + 1 + 1 + workIDW + 2 + 2 + workClaimW + 2 + workTimeW + 2

var workTitleStyle, workSectionStyle, workDimStyle, workSelectedStyle lipgloss.Style

var workOKStyle, workErrStyle, workFillStyle, workEmptyStyle lipgloss.Style

func init() { RegisterThemeHook(rebuildWorkStyles) }

func rebuildWorkStyles() {
	workTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(Accent)
	workSectionStyle = lipgloss.NewStyle().Bold(true).Foreground(Dim)
	workDimStyle = lipgloss.NewStyle().Foreground(Dim)
	workSelectedStyle = lipgloss.NewStyle().Background(SelectionBg).Foreground(SelectionFg)
	workOKStyle = lipgloss.NewStyle().Foreground(OK)
	workErrStyle = lipgloss.NewStyle().Foreground(ErrorColor)
	workFillStyle = lipgloss.NewStyle().Foreground(Accent)
	workEmptyStyle = lipgloss.NewStyle().Foreground(Faint)
}

// WorkTail is the selected job's last log lines as the app read them, with
// the controls cleaned off (LogLines). ID names the job they belong to.
type WorkTail struct {
	ID    sched.JobID
	Lines []string
	Err   string
}

// WorkData is everything RenderWork draws, assembled by the app on the
// Update goroutine each frame.
type WorkData struct {
	Queue    core.QueueView
	Selected sched.JobID
	Tail     WorkTail
	Now      time.Time
}

// WorkPagerData is everything RenderWorkPager draws: the job, the log's
// lines (cleaned, LogLines), the first line shown, whether the log's start
// is cut off, and why the log could not be read.
type WorkPagerData struct {
	Job     core.JobView
	Lines   []string
	Top     int
	Omitted bool
	Err     string
}

// WorkReported reports whether the daemon has published its queue: a view
// with no host, no directory and no jobs is the zero value a TUI holds
// before the first QueueChanged.
func WorkReported(q core.QueueView) bool {
	return q.Host.Name != "" || q.Dir != "" || len(q.Jobs) > 0
}

// WorkRows is the jobs in the order the work view lists them, and so the
// order its selection walks: the running ones, the queued ones, then the
// newest WorkRecentMax finished, each section in the order jobs gives
// (running by start, queued in queue order, finished newest first).
func WorkRows(jobs []core.JobView) []core.JobView {
	var running, queued, finished []core.JobView
	for _, j := range jobs {
		switch {
		case j.State == sched.Running:
			running = append(running, j)
		case j.State == sched.Queued:
			queued = append(queued, j)
		case j.State.Finished():
			finished = append(finished, j)
		}
	}
	if len(finished) > WorkRecentMax {
		finished = finished[:WorkRecentMax]
	}
	rows := make([]core.JobView, 0, len(running)+len(queued)+len(finished))
	rows = append(rows, running...)
	rows = append(rows, queued...)
	return append(rows, finished...)
}

// CleanText makes text an agent wrote (a job's description, a session's
// title) safe to print: escape sequences are stripped and every control
// character, a newline included, becomes a space.
func CleanText(s string) string { return sanitizeCardText(s) }

// JobLabel names a job in a prompt, as `job 14 "polyoxide tests"`: the
// description cleaned and cut to 60 columns.
func JobLabel(j core.JobView) string {
	return fmt.Sprintf(`job %d "%s"`, j.ID, ansi.Truncate(CleanText(j.Description), 60, "…"))
}

// LogLines turns the bytes of a log's end into display lines: escape
// sequences stripped, tabs four spaces, other controls dropped, and a line
// a progress bar rewrote with carriage returns reduced to its last state.
// partial says the bytes start mid-file, so their first line is cut and is
// dropped. A final newline ends the last line rather than starting another.
func LogLines(data []byte, partial bool) []string {
	if len(data) == 0 {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if partial {
		lines = lines[1:]
	}
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	if len(lines) == 0 {
		return nil
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		last := ""
		for _, seg := range strings.Split(l, "\r") {
			if seg != "" {
				last = seg
			}
		}
		out[i] = cleanLogLine(last)
	}
	return out
}

// cleanLogLine strips a log line's escape sequences and controls.
func cleanLogLine(l string) string {
	l = strings.ReplaceAll(ansi.Strip(l), "\t", "    ")
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, l)
}

// RenderWork draws the work view in a width by height box: the header, the
// capacity bars (or the reason admission is paused), the job sections with
// the selected job marked, and the selected job's tail. Every line is cut
// to the width and the result is exactly height lines.
func RenderWork(width, height int, d WorkData) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	q := d.Queue
	reported := WorkReported(q)
	out := []string{workHeader(width, q)}
	switch {
	case !reported:
		out = append(out, " "+workDimStyle.Render("The daemon has not reported its work queue yet."))
	case q.Paused != "":
		out = append(out, " "+workErrStyle.Render("Admission paused: "+CleanText(q.Paused)))
	default:
		out = append(out, workBars(q)...)
	}

	rows := WorkRows(q.Jobs)
	var selected core.JobView
	haveSel := false
	for _, j := range rows {
		if j.ID == d.Selected {
			selected, haveSel = j, true
		}
	}

	// The list takes what the header leaves, less a tail of up to eight
	// lines under its own rule when there is room for both.
	rest := height - len(out)
	tailN, listH := 0, rest
	if rest >= 6 {
		tailN = min(8, max(1, (rest-1)/3))
		listH = rest - 1 - tailN
	}

	cols := workColumns(width)
	var lines []string
	selLine := 0
	section := ""
	for _, j := range rows {
		if s := workSection(j.State); s != section {
			lines = append(lines, " "+workSectionStyle.Render(s))
			section = s
		}
		if j.ID == d.Selected {
			selLine = len(lines)
		}
		lines = append(lines, workRow(j, cols, d.Now, j.ID == d.Selected))
	}
	if len(rows) == 0 && reported {
		lines = []string{" " + workDimStyle.Render(`No jobs yet. A session submits one with: loom job submit -d "<what>" -- <cmd>`)}
	}
	shown := workWindow(lines, selLine, listH)
	out = append(out, shown...)
	for i := len(shown); i < listH; i++ {
		out = append(out, "")
	}

	if tailN > 0 {
		out = append(out, workRule(width, workTailTitle(selected, haveSel)))
		body := workTailBody(selected, haveSel, d.Tail, tailN)
		for _, l := range body {
			out = append(out, " "+l)
		}
		for i := len(body); i < tailN; i++ {
			out = append(out, "")
		}
	}
	return workBox(out, width, height)
}

// WorkPagerBody is how many log lines the pager shows in a box height
// lines tall: all but its title line.
func WorkPagerBody(height int) int { return max(1, height-1) }

// RenderWorkPager draws the log pager in a width by height box: a title
// line naming the job and the lines shown, then the window of the log.
func RenderWorkPager(width, height int, d WorkPagerData) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	body := WorkPagerBody(height)
	top := max(0, min(d.Top, len(d.Lines)-body))
	end := min(len(d.Lines), top+body)

	title := fmt.Sprintf(" job %d · %s", d.Job.ID, CleanText(d.Job.Description))
	if d.Err == "" && len(d.Lines) > 0 {
		title += fmt.Sprintf(" · lines %d–%d of %d", top+1, end, len(d.Lines))
		if d.Omitted {
			title += " (earlier output not shown)"
		}
	}
	out := []string{workTitleStyle.Render(title) + workRightHint(width, title, "esc back ")}
	switch {
	case d.Err != "":
		out = append(out, " "+workErrStyle.Render("cannot read the log: "+CleanText(d.Err)))
	case len(d.Lines) == 0:
		out = append(out, " "+workDimStyle.Render("(no output yet)"))
	default:
		for _, l := range d.Lines[top:end] {
			out = append(out, " "+l)
		}
	}
	return workBox(out, width, height)
}

// workBox makes lines exactly height lines of at most width columns and
// joins them.
func workBox(lines []string, width, height int) string {
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, width, "")
	}
	return strings.Join(lines, "\n")
}

// workRightHint is the spaces and hint that put hint at the right edge
// after text, or nothing when they would not fit.
func workRightHint(width int, text, hint string) string {
	pad := width - ansi.StringWidth(text) - ansi.StringWidth(hint)
	if pad < 1 {
		return ""
	}
	return strings.Repeat(" ", pad) + workDimStyle.Render(hint)
}

func workHeader(width int, q core.QueueView) string {
	left := " Work queue"
	if q.Host.Name != "" {
		left += " · " + CleanText(q.Host.Name)
	}
	return workTitleStyle.Render(left) + workRightHint(width, left, "w/esc back ")
}

// workBars draws the claimed totals against the caps, and what the running
// jobs measure beside them.
func workBars(q core.QueueView) []string {
	caps := q.Policy.Caps
	cpu := fmt.Sprintf("%d/%d claimed · %.1f in use", q.Claimed.CPUs, caps.CPUs, q.UsedCPU)
	mem := fmt.Sprintf("%d/%d GB claimed · %s in use", q.Claimed.MemoryGB, caps.MemoryGB, workGB(q.UsedRSSKB))
	io := fmt.Sprintf("%d/%d", q.Claimed.IOSlots, caps.IOJobs)
	return []string{
		workBarLine("CPU", q.Claimed.CPUs, caps.CPUs, workBarCells, cpu),
		workBarLine("MEM", q.Claimed.MemoryGB, caps.MemoryGB, workBarCells, mem),
		workBarLine("I/O", q.Claimed.IOSlots, caps.IOJobs, min(workBarCells, caps.IOJobs*2), io),
	}
}

func workBarLine(label string, claimed, capacity, cells int, text string) string {
	cells = max(0, min(cells, workBarCells))
	fill := 0
	if capacity > 0 {
		fill = (claimed*cells + capacity/2) / capacity
	}
	fill = max(0, min(cells, fill))
	bar := workFillStyle.Render(strings.Repeat("█", fill)) + workEmptyStyle.Render(strings.Repeat("░", cells-fill))
	return " " + label + " " + bar + strings.Repeat(" ", workBarCells-cells) + " " + text
}

func workSection(s sched.State) string {
	switch s {
	case sched.Running:
		return "RUNNING"
	case sched.Queued:
		return "QUEUED"
	}
	return "RECENT"
}

// workWindow is the n lines of lines around line sel: all of them when
// they fit, else a window that keeps sel in view.
func workWindow(lines []string, sel, n int) []string {
	if n <= 0 {
		return nil
	}
	if len(lines) <= n {
		return lines
	}
	start := max(0, min(sel-n/2, len(lines)-n))
	return lines[start : start+n]
}

func workRule(width int, title string) string {
	head := ansi.Truncate("── "+title+" ", width, "")
	return workDimStyle.Render(head + strings.Repeat("─", max(0, width-ansi.StringWidth(head))))
}

func workTailTitle(j core.JobView, ok bool) string {
	if !ok {
		return "tail"
	}
	t := fmt.Sprintf("job %d · tail", j.ID)
	if j.Notice == sched.NoticeUndelivered {
		t += " · message not delivered"
		if j.NoticeWhy != "" {
			t += ": " + CleanText(j.NoticeWhy)
		}
	}
	return t
}

func workTailBody(j core.JobView, ok bool, t WorkTail, n int) []string {
	switch {
	case !ok:
		return nil
	case j.State == sched.Queued:
		return []string{workDimStyle.Render("(queued: no output yet)")}
	case t.ID != j.ID:
		return []string{workDimStyle.Render("(reading the log…)")}
	case t.Err != "":
		return []string{workErrStyle.Render("(cannot read the log: " + CleanText(t.Err) + ")")}
	case len(t.Lines) == 0:
		return []string{workDimStyle.Render("(no output yet)")}
	}
	lines := t.Lines
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// workCols are the widths of the description, owner and last columns of a
// job row. A terminal under 76 columns drops the owner.
type workCols struct{ desc, owner, extra int }

func workColumns(width int) workCols {
	if width < 76 {
		rest := width - workFixed
		extra := max(14, rest*40/100)
		return workCols{desc: max(0, rest-extra), extra: extra}
	}
	extra := max(16, width*30/100)
	rest := width - workFixed - 2 - extra
	desc := rest * 55 / 100
	return workCols{desc: desc, owner: rest - desc, extra: extra}
}

// workFit cuts s to w columns, with an ellipsis, or pads it to them.
func workFit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = ansi.Truncate(s, w, "…")
	if pad := w - ansi.StringWidth(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}

// workRow draws one job: marker, ID, description, owner, claim (or the
// outcome of a finished job), time and what the job is doing or why it
// waits. The selected row is one block of the selection colors.
func workRow(j core.JobView, c workCols, now time.Time, selected bool) string {
	marker := " "
	if selected {
		marker = "▸"
	}
	claim, claimStyle, timeText, extra := workRowFacts(j, now)

	var plain, styled strings.Builder
	add := func(text string, w int, st lipgloss.Style) {
		cell := workFit(text, w)
		plain.WriteString(cell)
		styled.WriteString(st.Render(cell))
	}
	gap := func() {
		plain.WriteString("  ")
		styled.WriteString("  ")
	}
	none := lipgloss.NewStyle()
	add(" "+marker+" ", 3, none)
	add(fmt.Sprintf("%d", j.ID), workIDW, none)
	gap()
	add(CleanText(j.Description), c.desc, none)
	if c.owner > 0 {
		gap()
		add(CleanText(workOwner(j)), c.owner, workDimStyle)
	}
	gap()
	add(claim, workClaimW, claimStyle)
	gap()
	add(timeText, workTimeW, workDimStyle)
	gap()
	add(CleanText(extra), c.extra, workDimStyle)
	if selected {
		return workSelectedStyle.Render(plain.String())
	}
	return styled.String()
}

func workOwner(j core.JobView) string {
	switch {
	case j.Owner == 0:
		return "no owner"
	case j.OwnerTitle == "":
		return "session gone"
	case j.OwnerWorkspace == "":
		return j.OwnerTitle
	}
	return j.OwnerTitle + " · " + j.OwnerWorkspace
}

func workClaim(j core.JobView) string {
	if j.Claim.Exclusive {
		return "exclusive"
	}
	s := fmt.Sprintf("%dc %dG", j.Claim.CPUs, j.Claim.MemoryGB)
	if j.Claim.IO {
		s += " io"
	}
	return s
}

// workRowFacts is what a row shows besides the job's name: the claim cell
// (the outcome, for a finished job) with its style, the time cell, and the
// last cell.
func workRowFacts(j core.JobView, now time.Time) (claim string, claimStyle lipgloss.Style, timeText, extra string) {
	switch j.State {
	case sched.Running:
		extra = fmt.Sprintf("%.1fc  peak %s", j.CPU, workKB(j.PeakRSSKB))
		if j.CancelReason != "" {
			extra = "cancelling"
		}
		return workClaim(j), lipgloss.NewStyle(), workDur(now.Sub(j.Started)), extra
	case sched.Queued:
		extra = j.Waiting
		if extra == "" {
			extra = "waiting"
		}
		return workClaim(j), lipgloss.NewStyle(), workWait(now.Sub(j.Submitted)), extra
	}
	claim, claimStyle = workResult(j)
	timeText = "-"
	if !j.Started.IsZero() && !j.Ended.IsZero() {
		timeText = workDur(j.Ended.Sub(j.Started))
	}
	var parts []string
	if !j.Ended.IsZero() {
		parts = append(parts, workAgo(now.Sub(j.Ended)))
	}
	if j.Failure != "" {
		parts = append(parts, j.Failure)
	}
	if j.State == sched.Cancelled && j.CancelReason != "" {
		parts = append(parts, j.CancelReason)
	}
	if j.Notice == sched.NoticeUndelivered {
		parts = append(parts, "not delivered")
	}
	return claim, claimStyle, timeText, strings.Join(parts, " · ")
}

// workResult is a finished job's outcome, and the style it is drawn in.
func workResult(j core.JobView) (string, lipgloss.Style) {
	switch j.State {
	case sched.Done:
		code := 0
		if j.Exit != nil {
			code = j.Exit.Code
		}
		return fmt.Sprintf("✓ %d", code), workOKStyle
	case sched.Failed:
		switch {
		case j.Exit == nil:
			return "✗ start", workErrStyle
		case j.Exit.Signal != "":
			return "✗ " + j.Exit.Signal, workErrStyle
		}
		return fmt.Sprintf("✗ %d", j.Exit.Code), workErrStyle
	case sched.Cancelled:
		return "cancelled", workDimStyle
	}
	return "lost", workErrStyle
}

// workDur is a running time: 42s as 0m42s, 190s as 3m10s, an hour and more
// as 1h05m.
func workDur(d time.Duration) string {
	d = max(0, d.Round(time.Second))
	if h := int(d / time.Hour); h > 0 {
		return fmt.Sprintf("%dh%02dm", h, int(d%time.Hour/time.Minute))
	}
	return fmt.Sprintf("%dm%02ds", int(d/time.Minute), int(d%time.Minute/time.Second))
}

// workWait is how long a queued job has waited, by the minute.
func workWait(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	}
	return fmt.Sprintf("%dh%02dm", int(d/time.Hour), int(d%time.Hour/time.Minute))
}

// workAgo is how long ago a job ended.
func workAgo(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	}
	return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
}

// workKB is a size in kilobytes as 5.1G, 640M or 12K.
func workKB(kb int64) string {
	switch {
	case kb >= 1<<20:
		return fmt.Sprintf("%.1fG", float64(kb)/(1<<20))
	case kb >= 1<<10:
		return fmt.Sprintf("%dM", kb>>10)
	}
	return fmt.Sprintf("%dK", kb)
}

// workGB is a size in kilobytes as 9.8 GB.
func workGB(kb int64) string { return fmt.Sprintf("%.1f GB", float64(kb)/(1<<20)) }
```

- [ ] **Step 4: Run it to see it pass**

Run: `CGO_ENABLED=0 go test ./ui/ -run 'TestWork|TestRenderWork|TestLogLines|TestJobLabel'`
Expected: `ok  	github.com/aidan-bailey/loom/ui	<time>`.

Then the package and the format gate for the task's files: `CGO_ENABLED=0 go test ./ui/... && gofmt -l ui/work.go ui/work_test.go`
Expected: `ok` lines for `ui` and `ui/overlay`, and `gofmt -l` printing nothing (if it lists a file, run `gofmt -w` on that file only).

- [ ] **Step 5: Commit**

```bash
git add -N ui/work.go ui/work_test.go
git commit -m "feat(ui): draw the work queue and its log pager" -- ui/work.go ui/work_test.go
```

### Task 6.2: The caps form (`ui/overlay/workCapsOverlay.go`)

**Files:**
- Create: `ui/overlay/workCapsOverlay.go`
- Test: `ui/overlay/workCapsOverlay_test.go`

The form edits seven numbers. It knows nothing of `config` or `sched`: the app hands it plain values (0 for a field on the host's default) and reads plain values back, and it reports the model's refusal in the form. A field takes digits only, so the form cannot make a negative or non-numeric value; the bounds are the model's (`sched.Resolve`), and its refusal text is shown as it comes.

- [ ] **Step 1: Write the failing test**

Create `ui/overlay/workCapsOverlay_test.go`:

```go
package overlay

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
)

func capsKey(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEsc}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "ctrl+u":
		return tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}
	}
	return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
}

func newCaps(set WorkCapsValues) *WorkCapsOverlay {
	host := WorkCapsHost{Name: "fresco", Cores: 16, RAMBytes: 30 << 30}
	defaults := WorkCapsValues{CPUs: 14, MemoryGB: 22, IOJobs: 2, ClaimCPUs: 4, ClaimMemoryGB: 6, StarveMinutes: 10, KeepFinished: 50}
	return NewWorkCapsOverlay(host, defaults, set, "")
}

func pressCaps(o *WorkCapsOverlay, keys ...string) {
	for _, k := range keys {
		o.HandleKeyPress(capsKey(k))
	}
}

func capsLine(t *testing.T, out, sub string) string {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, sub) {
			return l
		}
	}
	t.Fatalf("no line holds %q in:\n%s", sub, out)
	return ""
}

func TestWorkCapsOverlay_StartsFromTheConfiguredValues(t *testing.T) {
	set := WorkCapsValues{CPUs: 8, ClaimMemoryGB: 4}
	o := newCaps(set)
	assert.Equal(t, set, o.Values())

	out := ansi.Strip(o.Render())
	assert.Contains(t, out, "Work queue caps · fresco")
	assert.Contains(t, out, "this host: 16 cores, 30.0 GB RAM")
	cpus := capsLine(t, out, "CPUs")
	assert.Contains(t, cpus, "8")
	assert.Contains(t, cpus, "set · host default 14")
	mem := capsLine(t, out, "Memory (GB)")
	assert.Contains(t, mem, "22")
	assert.Contains(t, mem, "host default")
	assert.NotContains(t, mem, "set", "a field left alone is the host's default")
	claim := capsLine(t, out, "Default claim GB")
	assert.Contains(t, claim, "set · host default 6")
}

func TestWorkCapsOverlay_AHostThatCouldNotReadItsRAM(t *testing.T) {
	host := WorkCapsHost{Name: "odd", Cores: 8}
	o := NewWorkCapsOverlay(host, WorkCapsValues{CPUs: 6, IOJobs: 2}, WorkCapsValues{}, "")
	out := ansi.Strip(o.Render())
	assert.Contains(t, out, "this host: 8 cores, RAM could not be read")
	mem := capsLine(t, out, "Memory (GB)")
	assert.Contains(t, mem, "?")
	assert.Contains(t, mem, "no host default")
}

func TestWorkCapsOverlay_DigitsEditTheFieldUnderTheCursor(t *testing.T) {
	o := newCaps(WorkCapsValues{})
	pressCaps(o, "1", "2")
	assert.Equal(t, WorkCapsValues{CPUs: 12}, o.Values())
	pressCaps(o, "down", "3")
	assert.Equal(t, WorkCapsValues{CPUs: 12, MemoryGB: 3}, o.Values())
	pressCaps(o, "tab", "tab", "tab", "9")
	assert.Equal(t, WorkCapsValues{CPUs: 12, MemoryGB: 3, ClaimMemoryGB: 9}, o.Values())
	pressCaps(o, "shift+tab", "up", "up", "7") // claim CPUs, I/O jobs, memory: 7 follows the 3
	assert.Equal(t, WorkCapsValues{CPUs: 12, MemoryGB: 37, ClaimMemoryGB: 9}, o.Values())
}

func TestWorkCapsOverlay_TheCursorStopsAtBothEnds(t *testing.T) {
	o := newCaps(WorkCapsValues{})
	pressCaps(o, "up", "up")
	assert.Equal(t, workCapsCPUs, o.cursor)
	for i := 0; i < 20; i++ {
		pressCaps(o, "down")
	}
	assert.Equal(t, workCapsKeepFinished, o.cursor)
}

func TestWorkCapsOverlay_BackspaceAndCtrlUReturnAFieldToItsDefault(t *testing.T) {
	o := newCaps(WorkCapsValues{CPUs: 123})
	pressCaps(o, "backspace")
	assert.Equal(t, 12, o.Values().CPUs)
	pressCaps(o, "backspace", "backspace", "backspace")
	assert.Equal(t, 0, o.Values().CPUs, "an empty field is the host default")
	pressCaps(o, "5", "ctrl+u")
	assert.Equal(t, 0, o.Values().CPUs)
	assert.Contains(t, capsLine(t, ansi.Strip(o.Render()), "CPUs"), "host default")
}

func TestWorkCapsOverlay_OnlyDigitsAreTyped(t *testing.T) {
	o := newCaps(WorkCapsValues{})
	pressCaps(o, "a", "-", " ", ".", "x", "5")
	assert.Equal(t, WorkCapsValues{CPUs: 5}, o.Values())
}

func TestWorkCapsOverlay_LeadingZerosAndLength(t *testing.T) {
	o := newCaps(WorkCapsValues{})
	pressCaps(o, "0", "7")
	assert.Equal(t, 7, o.Values().CPUs, "a zero is replaced by the next digit")
	pressCaps(o, "ctrl+u", "0", "0")
	assert.Equal(t, "0", o.buf[workCapsCPUs], "zeros do not pile up")
	pressCaps(o, "ctrl+u", "1", "2", "3", "4", "5", "6", "7", "8", "9", "1", "1")
	assert.Equal(t, 123456789, o.Values().CPUs, "nine digits at most")
}

func TestWorkCapsOverlay_EnterSavesUnlessASaveIsInFlight(t *testing.T) {
	o := newCaps(WorkCapsValues{})
	closed, submit := o.HandleKeyPress(capsKey("enter"))
	assert.False(t, closed)
	assert.True(t, submit)

	o.SetSaving(true)
	assert.True(t, o.Saving())
	closed, submit = o.HandleKeyPress(capsKey("enter"))
	assert.False(t, closed)
	assert.False(t, submit, "a save in flight is not sent twice")
	pressCaps(o, "5")
	assert.Equal(t, WorkCapsValues{}, o.Values(), "the form is held while it saves")
	assert.Contains(t, ansi.Strip(o.Render()), "saving…")

	closed, submit = o.HandleKeyPress(capsKey("esc"))
	assert.True(t, closed, "esc leaves even while saving")
	assert.False(t, submit)
}

func TestWorkCapsOverlay_ShowsTheModelsRefusal(t *testing.T) {
	o := newCaps(WorkCapsValues{CPUs: 2000})
	o.SetSaving(true)
	o.SetError("scheduler.cpus is 2000: it must be 1–1024")
	assert.False(t, o.Saving(), "an answer ends the save")
	assert.Contains(t, ansi.Strip(o.Render()), "scheduler.cpus is 2000: it must be 1–1024")
	pressCaps(o, "backspace")
	assert.NotContains(t, ansi.Strip(o.Render()), "it must be", "editing clears the refusal")
	assert.Equal(t, 200, o.Values().CPUs)
}

func TestWorkCapsOverlay_NamesWhyAdmissionIsPaused(t *testing.T) {
	host := WorkCapsHost{Name: "fresco", Cores: 16, RAMBytes: 30 << 30}
	o := NewWorkCapsOverlay(host, WorkCapsValues{}, WorkCapsValues{CPUs: 2000}, "scheduler.cpus is 2000: it must be 1–1024")
	assert.Contains(t, ansi.Strip(o.Render()), "admission paused: scheduler.cpus is 2000: it must be 1–1024")
	assert.NotContains(t, ansi.Strip(newCaps(WorkCapsValues{}).Render()), "admission paused")
}

func TestWorkCapsOverlay_IsAnOverlay(t *testing.T) {
	var o Overlay = newCaps(WorkCapsValues{})
	o.SetSize(100, 40)
	closed, cmd := o.HandleKey(capsKey("esc"))
	assert.True(t, closed)
	assert.Nil(t, cmd)
	assert.Contains(t, ansi.Strip(o.View()), "Work queue caps")
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `CGO_ENABLED=0 go test ./ui/overlay/ -run WorkCaps`

Expected: the package fails to build, printing `undefined: WorkCapsValues`, `undefined: WorkCapsHost`, `undefined: NewWorkCapsOverlay`, `undefined: WorkCapsOverlay`, `undefined: workCapsCPUs` (and the other field names), and then `FAIL github.com/aidan-bailey/loom/ui/overlay [build failed]`.

- [ ] **Step 3: Implement**

Create `ui/overlay/workCapsOverlay.go`:

```go
package overlay

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// workCapsField indexes the caps form's rows, in display order.
type workCapsField int

const (
	workCapsCPUs workCapsField = iota
	workCapsMemoryGB
	workCapsIOJobs
	workCapsClaimCPUs
	workCapsClaimMemoryGB
	workCapsStarveMinutes
	workCapsKeepFinished
	workCapsFieldCount
)

// workCapsMaxDigits is how many digits a field takes: nine always fit an int.
const workCapsMaxDigits = 9

func (f workCapsField) label() string {
	switch f {
	case workCapsCPUs:
		return "CPUs"
	case workCapsMemoryGB:
		return "Memory (GB)"
	case workCapsIOJobs:
		return "I/O jobs"
	case workCapsClaimCPUs:
		return "Default claim CPUs"
	case workCapsClaimMemoryGB:
		return "Default claim GB"
	case workCapsStarveMinutes:
		return "Starve after (min)"
	case workCapsKeepFinished:
		return "Keep finished jobs"
	}
	return ""
}

// WorkCapsHost is the machine the form edits caps for: its name, cores and
// RAM in bytes (0 when the daemon could not read it).
type WorkCapsHost struct {
	Name     string
	Cores    int
	RAMBytes uint64
}

// WorkCapsValues holds one number per field of the form. As a form's
// content, 0 is a field left on the host's default; as defaults, 0 is a
// field the host has none for (the memory fields, when its RAM could not
// be read).
type WorkCapsValues struct {
	CPUs          int
	MemoryGB      int
	IOJobs        int
	ClaimCPUs     int
	ClaimMemoryGB int
	StarveMinutes int
	KeepFinished  int
}

func (v WorkCapsValues) get(f workCapsField) int {
	switch f {
	case workCapsCPUs:
		return v.CPUs
	case workCapsMemoryGB:
		return v.MemoryGB
	case workCapsIOJobs:
		return v.IOJobs
	case workCapsClaimCPUs:
		return v.ClaimCPUs
	case workCapsClaimMemoryGB:
		return v.ClaimMemoryGB
	case workCapsStarveMinutes:
		return v.StarveMinutes
	case workCapsKeepFinished:
		return v.KeepFinished
	}
	return 0
}

func (v *WorkCapsValues) set(f workCapsField, n int) {
	switch f {
	case workCapsCPUs:
		v.CPUs = n
	case workCapsMemoryGB:
		v.MemoryGB = n
	case workCapsIOJobs:
		v.IOJobs = n
	case workCapsClaimCPUs:
		v.ClaimCPUs = n
	case workCapsClaimMemoryGB:
		v.ClaimMemoryGB = n
	case workCapsStarveMinutes:
		v.StarveMinutes = n
	case workCapsKeepFinished:
		v.KeepFinished = n
	}
}

// WorkCapsOverlay is the work view's caps form: a field for each number of
// the scheduler block, each showing the value in effect and whether it is
// the host's default. It owns no persistence: HandleKeyPress reports an
// Enter, and the caller sends the values (Values) and reports the model's
// answer (SetError, or closing the form).
type WorkCapsOverlay struct {
	host     WorkCapsHost
	defaults WorkCapsValues
	paused   string
	buf      [workCapsFieldCount]string
	cursor   workCapsField
	width    int
	saving   bool
	err      string
}

var _ Overlay = (*WorkCapsOverlay)(nil)

// NewWorkCapsOverlay opens the form over the values set in the scheduler
// block (0 for a field it leaves on the default), with the host's defaults
// beside them and the reason admission is paused ("" when it is not).
func NewWorkCapsOverlay(host WorkCapsHost, defaults, set WorkCapsValues, paused string) *WorkCapsOverlay {
	o := &WorkCapsOverlay{host: host, defaults: defaults, paused: paused, width: 66}
	for f := workCapsField(0); f < workCapsFieldCount; f++ {
		if n := set.get(f); n != 0 {
			o.buf[f] = strconv.Itoa(n)
		}
	}
	return o
}

// Values is what the form holds: the number typed in each field, 0 for an
// empty one.
func (o *WorkCapsOverlay) Values() WorkCapsValues {
	var v WorkCapsValues
	for f := workCapsField(0); f < workCapsFieldCount; f++ {
		n, _ := strconv.Atoi(o.buf[f])
		v.set(f, n)
	}
	return v
}

// SetSaving marks a save as sent and unanswered: the form holds its fields
// and ignores Enter until the answer.
func (o *WorkCapsOverlay) SetSaving(saving bool) { o.saving = saving }

// Saving reports whether a save is sent and unanswered.
func (o *WorkCapsOverlay) Saving() bool { return o.saving }

// SetError shows the model's refusal in the form and ends the save. The
// next edit clears it.
func (o *WorkCapsOverlay) SetError(msg string) {
	o.err = msg
	o.saving = false
}

// HandleKeyPress processes one key press. closed reports that the form
// should be dismissed (Esc, which also works while a save is in flight);
// submit reports an Enter, which the caller answers with a save.
func (o *WorkCapsOverlay) HandleKeyPress(msg tea.KeyPressMsg) (closed, submit bool) {
	key := msg.String()
	if key == "esc" {
		return true, false
	}
	if o.saving {
		return false, false
	}
	switch key {
	case "enter":
		o.err = ""
		return false, true
	case "up", "shift+tab":
		o.cursor = workCapsField(max(0, int(o.cursor)-1))
	case "down", "tab":
		o.cursor = workCapsField(min(int(workCapsFieldCount)-1, int(o.cursor)+1))
	case "backspace":
		if b := o.buf[o.cursor]; b != "" {
			o.buf[o.cursor] = b[:len(b)-1]
		}
		o.err = ""
	case "ctrl+u":
		o.buf[o.cursor] = ""
		o.err = ""
	default:
		if len(key) == 1 && key[0] >= '0' && key[0] <= '9' {
			o.typeDigit(key[0])
		}
	}
	return false, false
}

// typeDigit appends d to the field under the cursor. A lone zero is
// replaced rather than followed, and a field takes workCapsMaxDigits.
func (o *WorkCapsOverlay) typeDigit(d byte) {
	o.err = ""
	b := o.buf[o.cursor]
	switch {
	case b == "0":
		b = string(d)
	case len(b) >= workCapsMaxDigits:
		return
	default:
		b += string(d)
	}
	o.buf[o.cursor] = b
}

// HandleKey satisfies the Overlay interface. The app calls HandleKeyPress,
// for the submit signal.
func (o *WorkCapsOverlay) HandleKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	closed, _ := o.HandleKeyPress(msg)
	return closed, nil
}

// SetSize satisfies the Overlay interface; the form uses the width.
func (o *WorkCapsOverlay) SetSize(width, _ int) { o.width = width }

// View satisfies the Overlay interface.
func (o *WorkCapsOverlay) View() string { return o.Render() }

var workCapsTitleStyle, workCapsSelectedStyle, workCapsNormalStyle, workCapsHintStyle, workCapsErrStyle lipgloss.Style

func init() { ui.RegisterThemeHook(rebuildWorkCapsStyles) }

func rebuildWorkCapsStyles() {
	workCapsTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(ui.Accent)
	workCapsSelectedStyle = lipgloss.NewStyle().Background(ui.SelectionBg).Foreground(ui.SelectionFg)
	workCapsNormalStyle = lipgloss.NewStyle().Foreground(ui.Text)
	workCapsHintStyle = lipgloss.NewStyle().Foreground(ui.Faint)
	workCapsErrStyle = lipgloss.NewStyle().Foreground(ui.ErrorColor)
}

// Render draws the form.
func (o *WorkCapsOverlay) Render() string {
	title := "Work queue caps"
	if o.host.Name != "" {
		title += " · " + ui.CleanText(o.host.Name)
	}
	var b strings.Builder
	b.WriteString(workCapsTitleStyle.Render(title) + "\n")
	b.WriteString(workCapsHintStyle.Render(o.hostLine()) + "\n")
	if o.paused != "" {
		b.WriteString(workCapsErrStyle.Render("admission paused: "+ui.CleanText(o.paused)) + "\n")
	}
	b.WriteString("\n")
	for f := workCapsField(0); f < workCapsFieldCount; f++ {
		b.WriteString(o.row(f) + "\n")
	}
	b.WriteString("\n")
	switch {
	case o.saving:
		b.WriteString(workCapsHintStyle.Render("saving…"))
	case o.err != "":
		b.WriteString(workCapsErrStyle.Render(ui.CleanText(o.err)))
	default:
		b.WriteString(workCapsHintStyle.Render("↑/↓ move · 0-9 edit · ^u default · enter save · esc cancel"))
	}
	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ui.Accent).
		Padding(1, 2).
		Width(max(o.width, 50))
	return border.Render(b.String())
}

// hostLine names the cores and RAM the defaults come from.
func (o *WorkCapsOverlay) hostLine() string {
	ram := "RAM could not be read"
	if o.host.RAMBytes > 0 {
		ram = fmt.Sprintf("%.1f GB RAM", float64(o.host.RAMBytes)/(1<<30))
	}
	return fmt.Sprintf("this host: %d cores, %s", o.host.Cores, ram)
}

// row draws one field: its value in effect, and whether that is the host's
// default or set.
func (o *WorkCapsOverlay) row(f workCapsField) string {
	cursor := "  "
	if f == o.cursor {
		cursor = "> "
	}
	def := o.defaults.get(f)
	value, note := o.buf[f], ""
	switch {
	case value == "" && def > 0:
		value, note = strconv.Itoa(def), "host default"
	case value == "":
		value, note = "?", "no host default"
	case def > 0:
		note = fmt.Sprintf("set · host default %d", def)
	default:
		note = "set"
	}
	if f == o.cursor && o.buf[f] != "" && !o.saving {
		value += "_"
	}
	line := fmt.Sprintf("%s%-20s %-11s %s", cursor, f.label(), value, note)
	if f == o.cursor {
		return workCapsSelectedStyle.Render(line)
	}
	return workCapsNormalStyle.Render(line)
}
```

- [ ] **Step 4: Run it to see it pass**

Run: `CGO_ENABLED=0 go test ./ui/overlay/ -run WorkCaps`
Expected: `ok  	github.com/aidan-bailey/loom/ui/overlay	<time>`.

Then the gate for the task's files: `CGO_ENABLED=0 go vet ./ui/... && gofmt -l ui/overlay/workCapsOverlay.go ui/overlay/workCapsOverlay_test.go`
Expected: no output (if `gofmt -l` lists a file, run `gofmt -w` on that file only).

- [ ] **Step 5: Commit**

```bash
git add -N ui/overlay/workCapsOverlay.go ui/overlay/workCapsOverlay_test.go
git commit -m "feat(ui): the work view's caps form" -- ui/overlay/workCapsOverlay.go ui/overlay/workCapsOverlay_test.go
```

### Task 6.3: The work view mode: selection, tail and pager (`app/work.go`)

**Files:**
- Create: `app/work.go`
- Modify: `app/app.go` (the `viewMode` constants, the `home` struct, `Update`, `update`, the health tick, `instanceChanged`, `View`, `attachCursor`)
- Modify: `app/state_default.go` (`handleStateDefaultKey`)
- Test: `app/work_test.go`

This task adds the view mode with everything that only reads: entering and leaving, the selection by ID, `j`/`k`, the tail and the pager (`enter`, scrolling, `esc`), drawing, and dropping the mouse. `w` does not reach it until Task 6.6, so the tests enter through `toggleWorkView`. Requires Story 4: `home.queue` and the `core.QueueChanged` applier.

- [ ] **Step 1: Write the failing test**

Create `app/work_test.go`:

```go
package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testQueue is the queue view of a host called fresco holding jobs.
func testQueue(jobs ...core.JobView) core.QueueView {
	q := core.QueueView{Dir: "/tmp/loom/jobs", Jobs: jobs}
	q.Host = sched.Host{Name: "fresco", Cores: 16, RAMBytes: 30 << 30}
	q.Policy.Caps = sched.Caps{CPUs: 14, MemoryGB: 22, IOJobs: 2}
	return q
}

// queueJob is a job in state with the default claim, submitted a minute ago.
func queueJob(id sched.JobID, state sched.State, desc string) core.JobView {
	j := core.JobView{ID: id, State: state, Description: desc}
	j.Claim = sched.Claim{CPUs: 4, MemoryGB: 6}
	j.Submitted = time.Now().Add(-time.Minute)
	if state != sched.Queued {
		j.Started = j.Submitted
	}
	if state.Finished() {
		j.Ended = time.Now()
	}
	return j
}

// setQueue publishes q to m as the model does: a QueueChanged event, which
// the TUI's applier puts in home.queue.
func setQueue(t *testing.T, m *home, q core.QueueView) {
	t.Helper()
	_ = m.applyCoreEvent(core.QueueChanged{View: q})
	require.Equal(t, q.Dir, m.queue.Dir, "fixture: the event reached home.queue")
	require.Len(t, m.queue.Jobs, len(q.Jobs))
}

// workHome is a home sized like a terminal and showing the work view over
// the jobs, entered from the focus layout.
func workHome(t *testing.T, jobs ...core.JobView) *home {
	t.Helper()
	m := newTestHome(t)
	m.errBox.SetSize(400, 1)
	m.updateHandleWindowSizeEvent(tea.WindowSizeMsg{Width: 120, Height: 40})
	setQueue(t, m, testQueue(jobs...))
	m.enterWorkView()
	require.Equal(t, viewWork, m.viewMode)
	return m
}

// pressWork sends the key the way a terminal does, to the default state.
func pressWork(t *testing.T, m *home, key string) tea.Cmd {
	t.Helper()
	_, cmd := handleStateDefaultKey(m, keyFor(t, key))
	return cmd
}

// selectedWork is the ID of the job the work view has selected.
func selectedWork(t *testing.T, m *home) sched.JobID {
	t.Helper()
	j, ok := m.workSelected()
	require.True(t, ok, "the work view has a job selected")
	return j.ID
}

// writeLog writes a log of n lines, "line 1" to "line n".
func writeLog(t *testing.T, path string, n int) {
	t.Helper()
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	require.NoError(t, os.WriteFile(path, []byte(b.String()), 0o600))
}

func TestWorkView_EnterAndLeaveReturnToTheModeItCameFrom(t *testing.T) {
	for name, from := range map[string]viewMode{"focus": viewFocus, "overview": viewOverview} {
		t.Run(name, func(t *testing.T) {
			m := newTestHome(t)
			m.viewMode = from
			m.toggleWorkView()
			require.Equal(t, viewWork, m.viewMode)
			m.toggleWorkView()
			assert.Equal(t, from, m.viewMode, "w returns to the mode it came from")
		})
	}

	t.Run("workbench", func(t *testing.T) {
		m := newTestHome(t)
		m.viewMode = viewWorkbench
		m.toggleWorkView()
		assert.Equal(t, viewWorkbench, m.viewMode, "the workbench has no way in")
	})
}

func TestWorkView_IsNotPersisted(t *testing.T) {
	m := newTestHome(t)
	require.NoError(t, m.appState().SetUIPrefs(config.UIPrefs{ViewMode: "overview"}))
	m.syncWorkspaces()
	m.viewMode = viewOverview
	m.toggleWorkView()
	assert.Equal(t, "overview", m.appState().GetUIPrefs().ViewMode, "entering the view writes nothing")
	m.toggleWorkView()
	assert.Equal(t, "overview", m.appState().GetUIPrefs().ViewMode, "nor does leaving it")
}

func TestWorkView_RefusedOnWindows(t *testing.T) {
	m := newTestHome(t)
	m.errBox.SetSize(400, 1)
	prev := workGOOS
	workGOOS = "windows"
	t.Cleanup(func() { workGOOS = prev })

	m.toggleWorkView()
	assert.Equal(t, viewFocus, m.viewMode)
	assert.Contains(t, m.errBox.String(), sched.ErrUnsupported.Error())

	workGOOS = "linux"
	m.toggleWorkView()
	assert.Equal(t, viewWork, m.viewMode, "anywhere else it opens")
}

func TestWorkView_SelectionIsTheJobsIDNotItsRow(t *testing.T) {
	m := workHome(t, queueJob(14, sched.Running, "a"), queueJob(15, sched.Running, "b"), queueJob(16, sched.Queued, "c"))
	assert.Equal(t, sched.JobID(14), selectedWork(t, m), "the first row is selected on entry")
	pressWork(t, m, "j")
	require.Equal(t, sched.JobID(15), selectedWork(t, m))

	// A job starts ahead of it: job 15's row moves down and it stays selected.
	setQueue(t, m, testQueue(queueJob(20, sched.Running, "n"), queueJob(14, sched.Running, "a"), queueJob(15, sched.Running, "b"), queueJob(16, sched.Queued, "c")))
	assert.Equal(t, sched.JobID(15), selectedWork(t, m))

	// It leaves the view: the row that slid into its place is selected.
	setQueue(t, m, testQueue(queueJob(20, sched.Running, "n"), queueJob(14, sched.Running, "a"), queueJob(16, sched.Queued, "c")))
	assert.Equal(t, sched.JobID(16), selectedWork(t, m))

	// Every job gone: nothing is selected, and a job that comes back is.
	setQueue(t, m, testQueue())
	_, ok := m.workSelected()
	assert.False(t, ok)
	setQueue(t, m, testQueue(queueJob(30, sched.Running, "z")))
	assert.Equal(t, sched.JobID(30), selectedWork(t, m))
}

func TestWorkView_JAndKWalkTheRowsAndStopAtTheEnds(t *testing.T) {
	m := workHome(t, queueJob(14, sched.Running, "a"), queueJob(16, sched.Queued, "b"), queueJob(12, sched.Done, "c"))
	var seen []sched.JobID
	for _, key := range []string{"j", "j", "j", "k", "down", "up", "up", "up"} {
		pressWork(t, m, key)
		seen = append(seen, selectedWork(t, m))
	}
	assert.Equal(t, []sched.JobID{16, 12, 12, 16, 12, 16, 14, 14}, seen)
}

func TestWorkView_EscLeavesForTheModeItCameFrom(t *testing.T) {
	t.Run("focus", func(t *testing.T) {
		m := workHome(t, queueJob(14, sched.Running, "a"))
		pressWork(t, m, "esc")
		assert.Equal(t, viewFocus, m.viewMode)
	})
	t.Run("overview", func(t *testing.T) {
		m := newTestHome(t)
		m.viewMode = viewOverview
		m.toggleWorkView()
		require.Equal(t, viewWork, m.viewMode)
		pressWork(t, m, "esc")
		assert.Equal(t, viewOverview, m.viewMode)
	})
}

func TestWorkView_KeysItDoesNotOwnDoNothing(t *testing.T) {
	m := workHome(t, queueJob(14, sched.Running, "a"))
	for _, key := range []string{"n", "N", "I", "D", "d", "tab", "i", "a", "g", "G", "W", "S", "f", "r", "p"} {
		cmd := pressWork(t, m, key)
		assert.Nil(t, cmd, "%q must not reach the script engine from the work view", key)
		assert.Equal(t, stateDefault, m.state, key)
		assert.Equal(t, viewWork, m.viewMode, key)
	}
	for _, key := range []string{"q", "?"} {
		assert.NotNil(t, pressWork(t, m, key), "%q is a script key of the work view", key)
	}
}

func TestWorkView_CtrlCStillQuits(t *testing.T) {
	m := workHome(t)
	cmd := pressWork(t, m, "ctrl+c")
	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, cmd())
}

func TestWorkView_MouseIsDropped(t *testing.T) {
	m := workHome(t)
	mustAddInstance(t, m, "a")

	_, cmd := m.Update(tea.MouseClickMsg{X: 60, Y: 5, Button: tea.MouseLeft})
	assert.Nil(t, cmd)
	assert.False(t, m.dragging, "a click must not begin a drag over the hidden layout")
	_, cmd = m.Update(tea.MouseMotionMsg{X: 65, Y: 6, Button: tea.MouseLeft})
	assert.Nil(t, cmd)
	m.dragging = true
	_, cmd = m.Update(tea.MouseReleaseMsg{X: 65, Y: 6, Button: tea.MouseLeft})
	assert.Nil(t, cmd)
	assert.False(t, m.dragging, "a release drops a drag that was in flight")
	_, cmd = m.Update(tea.MouseWheelMsg{X: 60, Y: 5, Button: tea.MouseWheelDown})
	assert.Nil(t, cmd)
}

func TestWorkView_TheRailsBellWaitsForFocus(t *testing.T) {
	m := workHome(t)
	a := mustAddInstance(t, m, "a")
	selectIn(m, m.list, a)
	ring(m, a)

	_ = m.instanceChanged()
	assert.True(t, bell(m, a), "the focus layout is off screen: the bell is not attended")
	m.leaveWorkView()
	_ = m.instanceChanged()
	assert.False(t, bell(m, a), "back in focus, the selected row's bell clears")
}

func TestWorkView_ViewDrawsTheQueueInPlaceOfTheLayout(t *testing.T) {
	m := workHome(t, queueJob(14, sched.Running, "polyoxide tests"), queueJob(16, sched.Queued, "release build"))
	mustAddInstance(t, m, "rail-session")

	v := m.View()
	screen := ansi.Strip(v.Content)
	assert.Contains(t, screen, "Work queue · fresco")
	assert.Contains(t, screen, "polyoxide tests")
	assert.Contains(t, screen, "release build")
	assert.Contains(t, screen, "enter log")
	assert.NotContains(t, screen, "rail-session", "the rail is not drawn")
	assert.Nil(t, v.Cursor, "no pane cursor floats over the queue")
}

func TestWorkView_TheTailIsReadWhenTheSelectedJobChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.log")
	writeLog(t, path, 30)
	job := queueJob(14, sched.Running, "polyoxide tests")
	job.LogPath = path
	other := queueJob(15, sched.Running, "go test")
	m := workHome(t, job, other)

	cmd := m.workDrained()
	require.NotNil(t, cmd, "the view's first frame reads the selected job's tail")
	msg, ok := cmd().(workLogMsg)
	require.True(t, ok)
	assert.Equal(t, sched.JobID(14), msg.id)
	assert.False(t, msg.full)
	require.Len(t, msg.lines, workTailLines)
	assert.Equal(t, "line 15", msg.lines[0])
	assert.Equal(t, "line 30", msg.lines[workTailLines-1])
	m.applyWorkLog(msg)
	assert.Contains(t, ansi.Strip(m.View().Content), "line 30")
	assert.Nil(t, m.workDrained(), "nothing changed: no second read")

	// The model samples the job's usage and publishes it: a read.
	moved := job
	moved.RSSKB = 123
	setQueue(t, m, testQueue(moved, other))
	assert.NotNil(t, m.workDrained())
	assert.Nil(t, m.workDrained())

	// Its state changes (it ends): a read for the last lines.
	done := moved
	done.State, done.Ended = sched.Done, time.Now()
	setQueue(t, m, testQueue(done, other))
	assert.NotNil(t, m.workDrained())

	// Another job is selected: a read for it, and the old tail is not shown under it.
	pressWork(t, m, "k")
	require.Equal(t, sched.JobID(15), selectedWork(t, m))
	cmd = m.workDrained()
	require.NotNil(t, cmd)
	assert.Equal(t, sched.JobID(15), cmd().(workLogMsg).id)
	assert.NotContains(t, ansi.Strip(m.View().Content), "line 30", "job 14's tail is not shown under job 15")
}

func TestWorkView_UpdateReadsAfterItsDrain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.log")
	writeLog(t, path, 3)
	job := queueJob(14, sched.Running, "polyoxide tests")
	job.LogPath = path
	m := workHome(t, job)

	_, cmd := m.Update(coreWakeMsg{})
	var read *workLogMsg
	for _, msg := range runCmds(t, cmd) {
		if r, ok := msg.(workLogMsg); ok {
			read = &r
		}
	}
	require.NotNil(t, read, "an Update in the work view reads the tail it has not read")
	_, _ = m.Update(*read)
	assert.Equal(t, []string{"line 1", "line 2", "line 3"}, m.work.tail.Lines, "the message reaches the view")

	_, cmd = m.Update(coreWakeMsg{})
	for _, msg := range runCmds(t, cmd) {
		_, isRead := msg.(workLogMsg)
		assert.False(t, isRead, "nothing changed: no second read")
	}
}

func TestWorkView_TheTickRereadsARunningJobOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.log")
	writeLog(t, path, 3)
	running := queueJob(14, sched.Running, "a")
	running.LogPath = path
	finished := queueJob(12, sched.Done, "b")
	finished.LogPath = path
	m := workHome(t, running, queueJob(16, sched.Queued, "c"), finished)

	prev := tickUpdateMetadataCmd
	tickUpdateMetadataCmd = func() tea.Msg { return tickRearmed{} }
	t.Cleanup(func() { tickUpdateMetadataCmd = prev })
	reads := func() int {
		_, cmd := m.Update(tickUpdateMetadataMessage{})
		n := 0
		for _, msg := range runCmds(t, cmd) {
			if _, ok := msg.(workLogMsg); ok {
				n++
			}
		}
		return n
	}

	_ = m.workDrained()
	assert.Equal(t, 1, reads(), "a running job's log may have grown")
	pressWork(t, m, "j")
	_ = m.workDrained()
	assert.Equal(t, 0, reads(), "a queued job has no log")
	pressWork(t, m, "j")
	_ = m.workDrained()
	assert.Equal(t, 0, reads(), "a finished job's log has stopped growing")
}

func TestWorkView_ATailReadForAnotherJobIsDropped(t *testing.T) {
	m := workHome(t, queueJob(14, sched.Running, "a"))
	m.applyWorkLog(workLogMsg{id: 99, lines: []string{"stale"}})
	assert.Equal(t, ui.WorkTail{}, m.work.tail)
	m.applyWorkLog(workLogMsg{id: 14, lines: []string{"fresh"}})
	assert.Equal(t, ui.WorkTail{ID: 14, Lines: []string{"fresh"}}, m.work.tail)
	m.applyWorkLog(workLogMsg{id: 14, err: "boom"})
	assert.Equal(t, ui.WorkTail{ID: 14, Err: "boom"}, m.work.tail)
}

func TestReadLogEnd(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.log")

	data, partial, err := readLogEnd(path, 100)
	require.NoError(t, err, "a job with no log yet")
	assert.Empty(t, data)
	assert.False(t, partial)

	require.NoError(t, os.WriteFile(path, []byte("0123456789"), 0o600))
	data, partial, err = readLogEnd(path, 100)
	require.NoError(t, err)
	assert.Equal(t, "0123456789", string(data))
	assert.False(t, partial)

	data, partial, err = readLogEnd(path, 4)
	require.NoError(t, err)
	assert.Equal(t, "6789", string(data), "the end of the log")
	assert.True(t, partial, "the head was cut")

	_, _, err = readLogEnd(filepath.Join(path, "below"), 4)
	assert.Error(t, err, "a path that cannot be a log is an error, not an empty log")
}

func TestWorkPager_OpensAtTheEndAndScrolls(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.log")
	writeLog(t, path, 100)
	job := queueJob(14, sched.Running, "polyoxide tests")
	job.LogPath = path
	m := workHome(t, job)
	body := ui.WorkPagerBody(m.workHeight())
	maxTop := 100 - body

	assert.Nil(t, pressWork(t, m, "enter"))
	require.NotNil(t, m.work.pager)
	cmd := m.workDrained()
	require.NotNil(t, cmd, "opening the pager reads the log")
	read := cmd().(workLogMsg)
	assert.True(t, read.full)
	m.applyWorkLog(read)
	assert.Len(t, m.work.pager.lines, 100)
	assert.Equal(t, maxTop, m.work.pager.top, "a log opens at its end")

	pressWork(t, m, "k")
	assert.Equal(t, maxTop-1, m.work.pager.top)
	pressWork(t, m, "pgup")
	assert.Equal(t, maxTop-1-body, m.work.pager.top)
	pressWork(t, m, "g")
	assert.Equal(t, 0, m.work.pager.top)
	pressWork(t, m, "k")
	assert.Equal(t, 0, m.work.pager.top, "stops at the top")
	pressWork(t, m, "pgdown")
	assert.Equal(t, body, m.work.pager.top)
	pressWork(t, m, "G")
	assert.Equal(t, maxTop, m.work.pager.top)
	pressWork(t, m, "j")
	assert.Equal(t, maxTop, m.work.pager.top, "stops at the end")

	screen := ansi.Strip(m.View().Content)
	assert.Contains(t, screen, "job 14 · polyoxide tests · lines")
	assert.Contains(t, screen, "line 100")
	assert.Contains(t, screen, "esc back · j/k scroll")
}

func TestWorkPager_FollowsALogOnlyWhileAtItsEnd(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.log")
	writeLog(t, path, 100)
	job := queueJob(14, sched.Running, "a")
	job.LogPath = path
	m := workHome(t, job)
	body := ui.WorkPagerBody(m.workHeight())
	pressWork(t, m, "enter")
	m.applyWorkLog(m.workDrained()().(workLogMsg))
	require.Equal(t, 100-body, m.work.pager.top)

	reread := func() { m.applyWorkLog(m.workTick()().(workLogMsg)) }
	writeLog(t, path, 120)
	reread()
	assert.Len(t, m.work.pager.lines, 120)
	assert.Equal(t, 120-body, m.work.pager.top, "at the end, the pager follows the log")

	pressWork(t, m, "g")
	writeLog(t, path, 130)
	reread()
	assert.Len(t, m.work.pager.lines, 130)
	assert.Equal(t, 0, m.work.pager.top, "scrolled away, it stays where it is")
}

func TestWorkPager_EscClosesItNotTheView(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.log")
	writeLog(t, path, 5)
	job := queueJob(14, sched.Running, "a")
	job.LogPath = path
	m := workHome(t, job)
	pressWork(t, m, "enter")
	m.applyWorkLog(m.workDrained()().(workLogMsg))

	assert.Nil(t, pressWork(t, m, "esc"))
	assert.Nil(t, m.work.pager)
	assert.Equal(t, viewWork, m.viewMode, "esc closes the pager, not the view")
	cmd := m.workDrained()
	require.NotNil(t, cmd, "the tail is read again")
	assert.False(t, cmd().(workLogMsg).full)
	pressWork(t, m, "esc")
	assert.Equal(t, viewFocus, m.viewMode, "a second esc leaves the view")
}

func TestWorkPager_ClosesWhenItsJobLeavesTheQueue(t *testing.T) {
	m := workHome(t, queueJob(14, sched.Running, "a"))
	pressWork(t, m, "enter")
	require.NotNil(t, m.work.pager)

	setQueue(t, m, testQueue())
	assert.Nil(t, m.workDrained())
	assert.Nil(t, m.work.pager)
	assert.Contains(t, m.errBox.String(), "job 14 is no longer listed")
}

func TestWorkPager_TakesOnlyItsOwnKeys(t *testing.T) {
	m := workHome(t, queueJob(14, sched.Running, "a"), queueJob(15, sched.Running, "b"))
	pressWork(t, m, "enter")
	for _, key := range []string{"q", "w", "x", "t", "o", "e", "n"} {
		assert.Nil(t, pressWork(t, m, key), "%q does nothing in the pager", key)
		require.NotNil(t, m.work.pager, key)
	}
	assert.Equal(t, sched.JobID(14), selectedWork(t, m), "scrolling is not selecting")
	pressWork(t, m, "j")
	assert.Equal(t, sched.JobID(14), selectedWork(t, m))
}

func TestWorkPager_AReadForAnotherJobIsDropped(t *testing.T) {
	m := workHome(t, queueJob(14, sched.Running, "a"))
	pressWork(t, m, "enter")
	m.applyWorkLog(workLogMsg{id: 99, full: true, lines: []string{"stale"}})
	assert.Empty(t, m.work.pager.lines)
	m.applyWorkLog(workLogMsg{id: 14, full: true, err: "permission denied"})
	assert.Equal(t, "permission denied", m.work.pager.err)
	assert.Contains(t, ansi.Strip(m.View().Content), "cannot read the log: permission denied")
}

func TestWorkPager_NeedsAJob(t *testing.T) {
	m := workHome(t)
	assert.Nil(t, pressWork(t, m, "enter"))
	assert.Nil(t, m.work.pager)
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `CGO_ENABLED=0 go test ./app/ -run 'TestWorkView|TestWorkPager|TestReadLogEnd'`

Expected: the package fails to build, printing `undefined: viewWork`, `undefined: workGOOS`, `m.enterWorkView undefined`, `m.toggleWorkView undefined`, `m.workSelected undefined`, `m.workDrained undefined`, `m.applyWorkLog undefined`, `undefined: workLogMsg`, `undefined: readLogEnd`, `m.work undefined`, and then `FAIL github.com/aidan-bailey/loom/app [build failed]`.

- [ ] **Step 3: Implement**

Create `app/work.go`:

```go
package app

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"runtime"
	"slices"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
)

// workGOOS is the OS the TUI runs on; a test sets it to see the Windows
// refusal.
var workGOOS = runtime.GOOS

// workTailLines is how many of a log's last lines the tail keeps.
const workTailLines = 16

// workTailBytes is how much of a log's end the tail reads.
const workTailBytes = 64 << 10

// workPagerBytes is how much of a log's end the pager reads: a longer log
// shows its last part, and says so. The queue keeps no cap on a log's size,
// and the TUI must not read a gigabyte into memory.
const workPagerBytes = 4 << 20

// workScriptKeys are the keys of the work view that go to the script engine:
// w leaves the view, q quits and ? shows the help. A user can rebind them.
var workScriptKeys = map[string]bool{"w": true, "q": true, "?": true}

// workState is the work view's own state. The jobs are home.queue's: the
// view re-reads them on every use, so a drain that changes the queue
// changes the view.
type workState struct {
	// from is the mode w and esc return to: viewFocus or viewOverview.
	from viewMode
	// sel is the selected job, by ID (0: none yet). selIdx is its row when
	// last seen, which heals the selection when the job leaves the view.
	sel    sched.JobID
	selIdx int
	// tail is the selected job's last log lines, as the last read returned
	// them.
	tail ui.WorkTail
	// pager is the open log pager, nil when closed.
	pager *workPager
	// sig is what the last log read was made for (workDrained).
	sig workSig
}

// workPager is the open log pager over one job's log.
type workPager struct {
	// id is the job whose log the pager shows.
	id sched.JobID
	// lines are the log's lines (its last workPagerBytes), and top is the
	// first one shown.
	lines []string
	top   int
	// loaded is set once a read landed; omitted says the log's start is not
	// in lines; err is why a read failed.
	loaded  bool
	omitted bool
	err     string
}

// workSig is what a log read was made for: the job shown, its state and
// its sampled usage (which the model publishes after each sample, so a
// change is a QueueChanged), and whether the pager or the tail was read.
type workSig struct {
	id    sched.JobID
	state sched.State
	rss   int64
	cpu   float64
	pager bool
}

// enterWorkView shows the work view over the mode it is entered from
// (focus or overview). The selection survives a visit; the tail, the pager
// and what the last read was for do not.
func (m *home) enterWorkView() {
	if workGOOS == "windows" {
		m.errBox.SetInfo(sched.ErrUnsupported.Error())
		return
	}
	m.work = workState{from: m.viewMode, sel: m.work.sel, selIdx: m.work.selIdx}
	m.viewMode = viewWork
}

// leaveWorkView returns to the mode the view was entered from. Entering
// overview again seeds its cursor, which heals one the queue's changes
// could not have moved.
func (m *home) leaveWorkView() {
	m.work.pager = nil
	if m.work.from == viewOverview {
		m.enterOverview()
		return
	}
	m.viewMode = viewFocus
}

// toggleWorkView is the w key: into the view from focus or overview, out of
// it to where it came from. The workbench has no way in.
func (m *home) toggleWorkView() {
	switch m.viewMode {
	case viewWork:
		m.leaveWorkView()
	case viewFocus, viewOverview:
		m.enterWorkView()
	}
}

// dropWorkMouse swallows mouse input while the work view is up, as overview
// does: the hit-tests describe the focus layout, which is not on screen. A
// release also drops a drag that was in flight when the view opened. It
// reports whether it took msg.
func (m *home) dropWorkMouse(msg tea.Msg) bool {
	if m.viewMode != viewWork {
		return false
	}
	switch msg.(type) {
	case tea.MouseWheelMsg, tea.MouseClickMsg, tea.MouseMotionMsg:
		return true
	case tea.MouseReleaseMsg:
		m.dragging = false
		m.splitPane.ClearSelections()
		return true
	}
	return false
}

// workSelected is the selected job, resolved from home.queue by its ID
// every time it is asked for. A selection whose job has left the view (a
// retention drop, a RECENT section that moved on) moves to the row that
// slid into its place, or the last row; false when the view lists no job.
func (m *home) workSelected() (core.JobView, bool) {
	rows := ui.WorkRows(m.queue.Jobs)
	i := slices.IndexFunc(rows, func(j core.JobView) bool { return j.ID == m.work.sel })
	if i < 0 {
		if len(rows) == 0 {
			m.work.sel, m.work.selIdx = 0, 0
			return core.JobView{}, false
		}
		i = min(m.work.selIdx, len(rows)-1)
		m.work.sel = rows[i].ID
	}
	m.work.selIdx = i
	return rows[i], true
}

// moveWorkSelection moves the selection dir rows (-1 or 1), stopping at the
// ends.
func (m *home) moveWorkSelection(dir int) {
	if _, ok := m.workSelected(); !ok {
		return
	}
	rows := ui.WorkRows(m.queue.Jobs)
	i := min(max(m.work.selIdx+dir, 0), len(rows)-1)
	m.work.sel, m.work.selIdx = rows[i].ID, i
}

// workShown is the job whose log the view reads: the pager's while it is
// open, else the selected one. A pager whose job is no longer in the queue
// closes, saying so.
func (m *home) workShown() (core.JobView, bool) {
	if p := m.work.pager; p != nil {
		if i := slices.IndexFunc(m.queue.Jobs, func(j core.JobView) bool { return j.ID == p.id }); i >= 0 {
			return m.queue.Jobs[i], true
		}
		m.work.pager = nil
		m.errBox.SetInfo(fmt.Sprintf("job %d is no longer listed", p.id))
	}
	return m.workSelected()
}

// workHeight is the rows the view has: the terminal less the top chrome,
// the status line and the error row.
func (m *home) workHeight() int { return max(1, m.lastHeight-m.topChromeHeight()-2) }

// workContent is the view's content for View: the pager over its job's log,
// or the queue with the selected job's tail.
func (m *home) workContent() string {
	job, ok := m.workShown()
	if p := m.work.pager; p != nil && ok {
		return ui.RenderWorkPager(m.lastWidth, m.workHeight(), ui.WorkPagerData{Job: job, Lines: p.lines, Top: p.top, Omitted: p.omitted, Err: p.err})
	}
	return ui.RenderWork(m.lastWidth, m.workHeight(), ui.WorkData{Queue: m.queue, Selected: job.ID, Tail: m.work.tail, Now: time.Now()})
}

// workHint is the status line under the view.
func (m *home) workHint() string {
	if m.work.pager != nil {
		return "esc back · j/k scroll · pgup/pgdn page · g/G top/bottom"
	}
	return "w/esc back · enter log · q quit"
}

// handleWorkKey is the work view's keys: j/k and the arrows move the
// selection, enter opens the pager on it, esc returns to the mode the view
// came from, and w, q and ? go to the script engine. Every other key does
// nothing, as in overview: it would act on the focus layout, which is not on
// screen.
func (m *home) handleWorkKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.work.pager != nil {
		m.handleWorkPagerKey(key)
		return m, nil
	}
	switch key {
	case "esc":
		m.leaveWorkView()
		return m, m.instanceChanged()
	case "j", "down":
		m.moveWorkSelection(1)
	case "k", "up":
		m.moveWorkSelection(-1)
	case "enter":
		m.openWorkPager()
	default:
		if workScriptKeys[key] {
			cmd, _ := m.dispatchScript(key)
			return m, cmd
		}
	}
	return m, nil
}

// openWorkPager opens the pager on the selected job. Its first read lands
// through workDrained.
func (m *home) openWorkPager() {
	job, ok := m.workSelected()
	if !ok {
		return
	}
	m.work.pager = &workPager{id: job.ID}
	m.work.sig = workSig{}
}

// handleWorkPagerKey scrolls the pager or closes it. Following is just
// being at the end: a read that finds the pager there keeps it there.
func (m *home) handleWorkPagerKey(key string) {
	p := m.work.pager
	body := ui.WorkPagerBody(m.workHeight())
	maxTop := max(0, len(p.lines)-body)
	switch key {
	case "esc":
		m.work.pager = nil
		m.work.sig = workSig{}
		return
	case "j", "down":
		p.top++
	case "k", "up":
		p.top--
	case "pgdown", "ctrl+d":
		p.top += body
	case "pgup", "ctrl+u":
		p.top -= body
	case "g", "home":
		p.top = 0
	case "G", "end":
		p.top = maxTop
	}
	p.top = min(max(p.top, 0), maxTop)
}

// workLogMsg is a log read's result: a job's tail, or all the pager can
// show of it.
type workLogMsg struct {
	id      sched.JobID
	full    bool
	lines   []string
	omitted bool
	err     string
}

// workLogCmd reads the end of the log at path for job id. Everything it
// reads is passed in: the Cmd runs off the Update goroutine.
func workLogCmd(id sched.JobID, path string, full bool) tea.Cmd {
	limit := int64(workTailBytes)
	if full {
		limit = workPagerBytes
	}
	return func() tea.Msg {
		msg := workLogMsg{id: id, full: full}
		data, partial, err := readLogEnd(path, limit)
		if err != nil {
			msg.err = err.Error()
			return msg
		}
		lines := ui.LogLines(data, partial)
		if !full && len(lines) > workTailLines {
			lines = lines[len(lines)-workTailLines:]
		}
		msg.lines, msg.omitted = lines, partial
		return msg
	}
}

// readLogEnd reads the last limit bytes of the file at path; partial says
// the file's start was left out. A file that does not exist is an empty log
// (a queued job has none yet), not an error.
func readLogEnd(path string, limit int64) (data []byte, partial bool, err error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	start := int64(0)
	if st.Size() > limit {
		start, partial = st.Size()-limit, true
	}
	data = make([]byte, st.Size()-start)
	n, err := f.ReadAt(data, start)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, false, err
	}
	return data[:n], partial, nil
}

// applyWorkLog takes a log read's result. A tail read lands only for the
// job selected now, and a pager read only for the job the pager shows, so
// a read that was overtaken by a key press shows under no other job. The
// pager keeps its place unless it was at the end, where it follows.
func (m *home) applyWorkLog(msg workLogMsg) {
	if !msg.full {
		sel, ok := m.workSelected()
		if m.work.pager != nil || !ok || sel.ID != msg.id {
			return
		}
		m.work.tail = ui.WorkTail{ID: msg.id, Lines: msg.lines, Err: msg.err}
		return
	}
	p := m.work.pager
	if p == nil || p.id != msg.id {
		return
	}
	p.err = msg.err
	if msg.err != "" {
		return
	}
	body := ui.WorkPagerBody(m.workHeight())
	atEnd := !p.loaded || p.top >= max(0, len(p.lines)-body)
	oldTop := p.top
	p.lines, p.omitted, p.loaded = msg.lines, msg.omitted, true
	maxTop := max(0, len(p.lines)-body)
	if atEnd {
		p.top = maxTop
	} else {
		p.top = min(oldTop, maxTop)
	}
}

// workDrained is Update's step after the model's events were applied
// (drainCore): it reads the log of the job the view shows when that job, its
// state or its sampled usage differ from what the last read was for. The
// queue changes only through QueueChanged, so this is the read on
// QueueChanged; a key that moves the selection or opens the pager changes
// the signature too.
func (m *home) workDrained() tea.Cmd {
	if m.viewMode != viewWork {
		return nil
	}
	job, ok := m.workShown()
	if !ok {
		m.work.sig = workSig{}
		return nil
	}
	sig := workSig{id: job.ID, state: job.State, rss: job.RSSKB, cpu: job.CPU, pager: m.work.pager != nil}
	if sig == m.work.sig {
		return nil
	}
	m.work.sig = sig
	return workLogCmd(job.ID, job.LogPath, sig.pager)
}

// workTick is the health tick's read: a running job's log grows between
// samples, so it is read again. A queued job has no log and a finished
// job's has stopped growing.
func (m *home) workTick() tea.Cmd {
	job, ok := m.workShown()
	if !ok || job.State != sched.Running {
		return nil
	}
	return workLogCmd(job.ID, job.LogPath, m.work.pager != nil)
}
```

In `app/app.go`, ten edits.

(1) Add the view mode. Replace

```go
	// persisted — quit/restart lands in focus.
	viewWorkbench
)
```

with

```go
	// persisted — quit/restart lands in focus.
	viewWorkbench
	// viewWork is the host-wide work queue: the jobs sessions submitted with
	// loom job, what they claim of the caps, and the selected job's log
	// (work.go). Entered from focus or overview and left for the one it came
	// from. Never persisted.
	viewWork
)
```

(2) Add the state to `home`. Replace

```go
	// from the render-space ui.OverviewCursor overviewData() translates to.
	overviewCursor overviewCursor
```

with

```go
	// from the render-space ui.OverviewCursor overviewData() translates to.
	overviewCursor overviewCursor

	// work is the work view's own state (work.go): the selection, the log
	// tail and the pager. The jobs it shows are home.queue's.
	work workState
```

(3) Read after the drain. In `Update`, replace

```go
	cmd = tea.Batch(cmd, m.drainCore(), m.checkLink())
```

with

```go
	cmd = tea.Batch(cmd, m.drainCore(), m.checkLink(), m.workDrained())
```

(4) Drop the mouse. In `update`, replace

```go
func (m *home) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case coreWakeMsg:
```

with

```go
func (m *home) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.dropWorkMouse(msg) {
		return m, nil
	}
	switch msg := msg.(type) {
	case coreWakeMsg:
```

(5) Route a read's result. In `update`, replace

```go
	case scriptDoneMsg:
		return m, m.handleScriptDone(msg)
```

with

```go
	case workLogMsg:
		m.applyWorkLog(msg)
		return m, nil
	case scriptDoneMsg:
		return m, m.handleScriptDone(msg)
```

(6) Read on the health tick. In the `tickUpdateMetadataMessage` case, replace

```go
		if m.viewMode == viewWorkbench {
			if scan := m.workbenchScanCmd(); scan != nil {
				cmds = append(cmds, scan)
			}
		}
		return m, tea.Batch(cmds...)
```

with

```go
		if m.viewMode == viewWorkbench {
			if scan := m.workbenchScanCmd(); scan != nil {
				cmds = append(cmds, scan)
			}
		}
		// The work view's log grows between the model's samples.
		if m.viewMode == viewWork {
			if read := m.workTick(); read != nil {
				cmds = append(cmds, read)
			}
		}
		return m, tea.Batch(cmds...)
```

(7) Leave the rail's bell alone while the focus layout is off screen. In `instanceChanged`, replace

```go
	if selected != nil && m.viewMode != viewOverview {
```

with

```go
	if selected != nil && m.viewMode != viewOverview && m.viewMode != viewWork {
```

(8) Draw the view. In `View`, replace

```go
		mainContent = m.overview.Render(m.overviewData())
	} else if m.viewMode == viewWorkbench && m.state != stateFileExplorer {
```

with

```go
		mainContent = m.overview.Render(m.overviewData())
	} else if m.viewMode == viewWork {
		mainContent = m.workContent()
	} else if m.viewMode == viewWorkbench && m.state != stateFileExplorer {
```

(9) The status line. In `View`, replace

```go
		} else if m.viewMode == viewWorkbench {
			hint = "esc focus · 1-5 panel · e edit · f follow · i attach · ] next waiting · q quit"
		}
```

with

```go
		} else if m.viewMode == viewWorkbench {
			hint = "esc focus · 1-5 panel · e edit · f follow · i attach · ] next waiting · q quit"
		} else if m.viewMode == viewWork {
			hint = m.workHint()
		}
```

(10) No pane cursor over the queue. In `attachCursor`, replace

```go
	// The overview grid has no pane content on screen — a hardware
	// cursor positioned from the hidden split layout would float over
	// arbitrary card cells.
	if m.viewMode == viewOverview {
		return
	}
```

with

```go
	// The overview grid and the work view have no pane content on screen — a
	// hardware cursor positioned from the hidden split layout would float
	// over arbitrary cells.
	if m.viewMode == viewOverview || m.viewMode == viewWork {
		return
	}
```

In `app/state_default.go`, route the keys. In `handleStateDefaultKey`, replace

```go
	if m.viewMode == viewOverview {
		switch msg.String() {
		case "enter":
```

with

```go
	if m.viewMode == viewWork {
		return m.handleWorkKey(msg)
	}
	if m.viewMode == viewOverview {
		switch msg.String() {
		case "enter":
```

- [ ] **Step 4: Run it to see it pass**

Run: `CGO_ENABLED=0 go test ./app/ -run 'TestWorkView|TestWorkPager|TestReadLogEnd'`
Expected: `ok  	github.com/aidan-bailey/loom/app	<time>`.

Then the story gates for this task: `gofmt -l app/work.go app/work_test.go app/app.go app/state_default.go && CGO_ENABLED=0 go vet ./app/ && CGO_ENABLED=0 go test ./app/...`
Expected: `gofmt -l` prints nothing (if it lists a file, run `gofmt -w` on that file only), `go vet` is silent, and the app tests print `ok`.

- [ ] **Step 5: Commit**

```bash
git add -N app/work.go app/work_test.go
git commit -m "feat(app): the work view mode, its tail and log pager" -- app/work.go app/work_test.go app/app.go app/state_default.go
```

### Task 6.4: The caps form in the work view (`e`)

**Files:**
- Modify: `app/work.go` (imports, `handleWorkKey`, `workHint`; new code appended)
- Modify: `app/app.go` (`stateWorkCaps`, `handleMenuHighlighting`, `handleKeyPress`, `View`)
- Modify: `app/overlay_host.go` (`overlayWorkCaps`, `workCapsOverlay`)
- Modify: `app/requests.go` (`pendingReq.work`, `handleReply`)
- Test: `app/work_test.go`

`e` opens the form (Task 6.2) over the view as a modal state, `stateWorkCaps`, per `docs/claude/adding-a-modal-state.md`: it is in `handleMenuHighlighting`'s exclusion list and `View()`'s overlay list, and its commit carries a `ReqID` whose Reply the form answers to (`pendingWork`). The form stays open while the save is in flight, shows the model's refusal in itself, and closes on success. An answer for a form that has since closed (Esc, or reopened) goes to the error line, never into another form. Offline, `e` is refused with the info line the offline gate uses: `e` is a TUI-only key in the focus layout, so the gate lets it through and the view refuses it itself.

- [ ] **Step 1: Write the failing test**

In `app/work_test.go`, add `"errors"` to the standard-library imports and `"github.com/aidan-bailey/loom/ui/overlay"` after `"github.com/aidan-bailey/loom/ui"`: replace

```go
import (
	"fmt"
	"os"
```

with

```go
import (
	"errors"
	"fmt"
	"os"
```

and replace

```go
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
```

with

```go
	"github.com/aidan-bailey/loom/ui"
	"github.com/aidan-bailey/loom/ui/overlay"

	tea "charm.land/bubbletea/v2"
```

Then append to `app/work_test.go`:

```go
// workWatch is a home's Core that records the work view's requests instead
// of sending them.
type workWatch struct {
	core.Core
	caps []config.SchedulerSettings
	reqs []core.ReqID
}

// watchWork puts a workWatch between m and its model.
func watchWork(t *testing.T, m *home) *workWatch {
	t.Helper()
	w := &workWatch{Core: m.core}
	testStacks.Store(core.Core(w), stackOf(m))
	t.Cleanup(func() { testStacks.Delete(core.Core(w)) })
	m.core = w
	return w
}

func (w *workWatch) SetSchedulerCaps(s config.SchedulerSettings, req core.ReqID) {
	w.caps = append(w.caps, s)
	w.reqs = append(w.reqs, req)
}

// capsSent opens the form over one running job, types 2000 into the CPUs
// field and saves: the form is waiting for the model's answer to
// w.reqs[0].
func capsSent(t *testing.T) (*home, *workWatch) {
	t.Helper()
	m := workHome(t, queueJob(14, sched.Running, "a"))
	w := watchWork(t, m)
	pressWork(t, m, "e")
	require.Equal(t, stateWorkCaps, m.state)
	for _, key := range []string{"2", "0", "0", "0", "enter"} {
		_, _ = handleStateWorkCapsKey(m, keyFor(t, key))
	}
	require.Len(t, w.caps, 1)
	return m, w
}

func TestWorkCaps_EOpensTheFormOverTheViewWithTheConfiguredValues(t *testing.T) {
	q := testQueue(queueJob(14, sched.Running, "a"))
	q.Settings = config.SchedulerSettings{CPUs: 8, DefaultClaim: &config.ClaimSettings{CPUs: 2}}
	m := workHome(t)
	setQueue(t, m, q)

	assert.Nil(t, pressWork(t, m, "e"))
	require.Equal(t, stateWorkCaps, m.state)
	o := m.workCapsOverlay()
	require.NotNil(t, o)
	assert.Equal(t, overlay.WorkCapsValues{CPUs: 8, ClaimCPUs: 2}, o.Values())
	assert.Contains(t, ansi.Strip(o.Render()), "set · host default 14", "the form shows the host's defaults beside the set values")

	screen := ansi.Strip(m.View().Content)
	assert.Contains(t, screen, "Work queue caps · fresco", "the form is drawn over the view")
	assert.Contains(t, screen, "Work queue · fresco", "the view stays under it")
}

func TestWorkCaps_NothingToEditBeforeTheDaemonReports(t *testing.T) {
	m := workHome(t)
	setQueue(t, m, core.QueueView{})
	pressWork(t, m, "e")
	assert.Equal(t, stateDefault, m.state)
	assert.Contains(t, m.errBox.String(), "has not reported its work queue yet")
}

func TestWorkCaps_OfflineRefusesTheKey(t *testing.T) {
	m := workHome(t, queueJob(14, sched.Running, "a"))
	m.link.state = linkWaiting
	cmd := pressWork(t, m, "e")
	assert.Nil(t, cmd)
	assert.Equal(t, stateDefault, m.state, "no form opens over a daemon that is gone")
	assert.Contains(t, m.errBox.String(), "the loom daemon is stopped: e needs it")
}

func TestWorkCaps_SavingSendsTheFormsSettingsOnce(t *testing.T) {
	m := workHome(t, queueJob(14, sched.Running, "a"))
	w := watchWork(t, m)
	pressWork(t, m, "e")
	require.Equal(t, stateWorkCaps, m.state)

	for _, key := range []string{"1", "2", "tab", "tab", "tab", "3", "enter", "enter"} {
		_, _ = handleStateWorkCapsKey(m, keyFor(t, key))
	}
	require.Len(t, w.caps, 1, "the second enter finds the save in flight")
	assert.Equal(t, config.SchedulerSettings{CPUs: 12, DefaultClaim: &config.ClaimSettings{CPUs: 3}}, w.caps[0])
	assert.NotZero(t, w.reqs[0], "the save carries a ReqID, so its answer finds the form")
	assert.True(t, m.workCapsOverlay().Saving())
	assert.Equal(t, stateWorkCaps, m.state, "the form stays until the model answers")
}

func TestWorkCaps_ARefusalIsShownInTheForm(t *testing.T) {
	m, w := capsSent(t)
	_ = m.applyCoreEvent(core.Reply{Req: w.reqs[0], Err: errors.New("scheduler.cpus is 2000: it must be 1–1024")})

	assert.Equal(t, stateWorkCaps, m.state, "the form stays open")
	o := m.workCapsOverlay()
	require.NotNil(t, o)
	assert.False(t, o.Saving())
	assert.Contains(t, ansi.Strip(o.Render()), "scheduler.cpus is 2000: it must be 1–1024")

	_, _ = handleStateWorkCapsKey(m, keyFor(t, "enter"))
	assert.Len(t, w.caps, 2, "the form can be sent again")
}

func TestWorkCaps_AnAcceptedSaveClosesTheForm(t *testing.T) {
	m, w := capsSent(t)
	_ = m.applyCoreEvent(core.Reply{Req: w.reqs[0]})
	assert.Equal(t, stateDefault, m.state)
	assert.Nil(t, m.workCapsOverlay())
	assert.Equal(t, viewWork, m.viewMode)
}

func TestWorkCaps_ARefusalAfterTheFormClosedIsAnError(t *testing.T) {
	m, w := capsSent(t)
	_, _ = handleStateWorkCapsKey(m, keyFor(t, "esc"))
	require.Equal(t, stateDefault, m.state)
	_ = m.applyCoreEvent(core.Reply{Req: w.reqs[0], Err: errors.New("scheduler.cpus is 2000: it must be 1–1024")})
	assert.Contains(t, m.errBox.String(), "scheduler.cpus is 2000")
}

func TestWorkCaps_AnOldSavesAnswerDoesNotTouchANewForm(t *testing.T) {
	m, w := capsSent(t)
	_, _ = handleStateWorkCapsKey(m, keyFor(t, "esc"))
	pressWork(t, m, "e")
	fresh := m.workCapsOverlay()
	require.NotNil(t, fresh)

	_ = m.applyCoreEvent(core.Reply{Req: w.reqs[0], Err: errors.New("scheduler.cpus is 2000: it must be 1–1024")})
	assert.Equal(t, stateWorkCaps, m.state)
	assert.NotContains(t, ansi.Strip(fresh.Render()), "scheduler.cpus is 2000")
	assert.Contains(t, m.errBox.String(), "scheduler.cpus is 2000", "the refusal reaches the user as an error instead")
}

func TestWorkCaps_ItsKeysAreNotDeferredByMenuHighlighting(t *testing.T) {
	m := workHome(t, queueJob(14, sched.Running, "a"))
	pressWork(t, m, "e")
	require.Equal(t, stateWorkCaps, m.state)

	_, cmd := m.handleKeyPress(keyFor(t, "t"))
	assert.Nil(t, cmd, "t is a built-in binding: a modal state outside the exclusion list defers it")
	assert.False(t, m.keySent)
	typeBurstThroughHandleKeyPress(t, m, "1234")
	assert.Equal(t, 1234, m.workCapsOverlay().Values().CPUs, "a burst of digits types in order")
}

func TestWorkDefaults(t *testing.T) {
	got := workDefaults(sched.Host{Name: "fresco", Cores: 16, RAMBytes: 30 << 30})
	assert.Equal(t, overlay.WorkCapsValues{CPUs: 14, MemoryGB: 22, IOJobs: 2, ClaimCPUs: 4, ClaimMemoryGB: 6, StarveMinutes: 10, KeepFinished: 50}, got)

	noRAM := workDefaults(sched.Host{Name: "odd", Cores: 8})
	assert.Equal(t, overlay.WorkCapsValues{CPUs: 6, IOJobs: 2, ClaimCPUs: 4, StarveMinutes: 10, KeepFinished: 50}, noRAM,
		"a host whose RAM could not be read has no memory default")
}

func TestSchedulerSettingsRoundTrip(t *testing.T) {
	for _, s := range []config.SchedulerSettings{
		{},
		{CPUs: 8, MemoryGB: 16, IOJobs: 3, StarveAfterMinutes: 5, KeepFinished: 20},
		{DefaultClaim: &config.ClaimSettings{CPUs: 2, MemoryGB: 3}},
		{DefaultClaim: &config.ClaimSettings{MemoryGB: 3}},
	} {
		assert.Equal(t, s, schedulerSettings(capsValues(s)))
	}
	assert.Nil(t, schedulerSettings(overlay.WorkCapsValues{}).DefaultClaim, "a form with no claim edited leaves no claim block in config.json")
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `CGO_ENABLED=0 go test ./app/ -run 'TestWorkCaps|TestWorkDefaults|TestSchedulerSettings'`

Expected: the package fails to build, printing `undefined: stateWorkCaps`, `m.workCapsOverlay undefined`, `undefined: handleStateWorkCapsKey`, `undefined: workDefaults`, `undefined: capsValues`, `undefined: schedulerSettings`, and then `FAIL github.com/aidan-bailey/loom/app [build failed]`.

- [ ] **Step 3: Implement**

In `app/overlay_host.go`, add the kind. Replace

```go
	overlayIssuePicker
)
```

with

```go
	overlayIssuePicker
	overlayWorkCaps
)
```

and add the accessor after `issuePicker`. Replace

```go
func (m *home) issuePicker() *overlay.IssuePicker {
	if o, ok := m.activeOverlay.(*overlay.IssuePicker); ok {
		return o
	}
	return nil
}
```

with

```go
func (m *home) issuePicker() *overlay.IssuePicker {
	if o, ok := m.activeOverlay.(*overlay.IssuePicker); ok {
		return o
	}
	return nil
}

// workCapsOverlay returns the active WorkCapsOverlay, or nil when a
// different overlay is active.
func (m *home) workCapsOverlay() *overlay.WorkCapsOverlay {
	if o, ok := m.activeOverlay.(*overlay.WorkCapsOverlay); ok {
		return o
	}
	return nil
}
```

In `app/requests.go`, add the pending kind. Replace

```go
	issue  *pendingIssue  // an issue fetch (state_issue_picker.go)
}
```

with

```go
	issue  *pendingIssue  // an issue fetch (state_issue_picker.go)
	work   *pendingWork   // a request of the work view (work.go)
}
```

and route its Reply. In `handleReply`, replace

```go
	case p.script != nil:
		return m.scriptReplied(p.script, r)
```

with

```go
	case p.script != nil:
		return m.scriptReplied(p.script, r)
	case p.work != nil:
		return m.workReplied(p.work, r)
```

In `app/app.go`, four edits.

(1) The state. Replace

```go
	// is displayed (opened by the 'I' key).
	stateIssuePicker
)
```

with

```go
	// is displayed (opened by the 'I' key).
	stateIssuePicker
	// stateWorkCaps is the state when the work view's caps form is displayed
	// (opened by the 'e' key in the work view).
	stateWorkCaps
)
```

(2) The menu-highlighting exclusion. In `handleMenuHighlighting`, the condition ends `... || m.state == stateIssuePicker || m.state == stateSettings {`. Replace that ending

```go
 || m.state == stateSettings {
```

with

```go
 || m.state == stateSettings || m.state == stateWorkCaps {
```

(3) The key router. In `handleKeyPress`, replace

```go
	case stateIssuePicker:
		return handleStateIssuePickerKey(m, msg)
```

with

```go
	case stateIssuePicker:
		return handleStateIssuePickerKey(m, msg)
	case stateWorkCaps:
		return handleStateWorkCapsKey(m, msg)
```

(4) The overlay placement. In `View`, replace

```go
		case statePrompt, stateHelp, stateConfirm, stateWorkspace, stateSettings, stateMergePicker, stateLaunchOptions, stateIssuePicker:
```

with

```go
		case statePrompt, stateHelp, stateConfirm, stateWorkspace, stateSettings, stateMergePicker, stateLaunchOptions, stateIssuePicker, stateWorkCaps:
```

In `app/work.go`, three small edits and the new code.

The imports. Replace

```go
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
)
```

with

```go
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/ui"
	"github.com/aidan-bailey/loom/ui/overlay"

	tea "charm.land/bubbletea/v2"
)
```

The hint. Replace

```go
	return "w/esc back · enter log · q quit"
```

with

```go
	return "w/esc back · enter log · e caps · q quit"
```

The key. In `handleWorkKey`, replace

```go
	if m.work.pager != nil {
		m.handleWorkPagerKey(key)
		return m, nil
	}
	switch key {
	case "esc":
		m.leaveWorkView()
		return m, m.instanceChanged()
```

with

```go
	if m.work.pager != nil {
		m.handleWorkPagerKey(key)
		return m, nil
	}
	if m.workOfflineRefused(key) {
		return m, nil
	}
	switch key {
	case "esc":
		m.leaveWorkView()
		return m, m.instanceChanged()
```

and replace

```go
	case "enter":
		m.openWorkPager()
	default:
```

with

```go
	case "enter":
		m.openWorkPager()
	case "e":
		m.openWorkCaps()
	default:
```

Append the form's code to `app/work.go`, after `workTick`. Replace

```go
func (m *home) workTick() tea.Cmd {
	job, ok := m.workShown()
	if !ok || job.State != sched.Running {
		return nil
	}
	return workLogCmd(job.ID, job.LogPath, m.work.pager != nil)
}
```

with

```go
func (m *home) workTick() tea.Cmd {
	job, ok := m.workShown()
	if !ok || job.State != sched.Running {
		return nil
	}
	return workLogCmd(job.ID, job.LogPath, m.work.pager != nil)
}

// workModelKeys are the work view's keys that need the model. Offline they
// are refused with the info line the offline gate uses: the gate lets t and
// e through, being TUI-only keys in the focus layout.
var workModelKeys = map[string]bool{"e": true}

// workOfflineRefused refuses a key that needs the model while the TUI is
// offline, saying so on the info line.
func (m *home) workOfflineRefused(key string) bool {
	if !workModelKeys[key] || !m.offline() {
		return false
	}
	m.errBox.SetInfo(fmt.Sprintf("the loom daemon is %s: %s needs it", m.link.state.word(), key))
	return true
}

// pendingWork is a request of the work view waiting for its Reply. form is
// the caps form that sent a SetSchedulerCaps, whose Reply it answers; nil
// for the other requests.
type pendingWork struct {
	form *overlay.WorkCapsOverlay
}

// workReplied routes a work view request's Reply. The model's answer to a
// caps save goes to the form that sent it while that form is still the one
// open: a refusal into the form, an acceptance closing it. An answer for a
// form that has since closed, or been replaced, and every other Reply's
// error goes to the error line.
func (m *home) workReplied(p *pendingWork, r core.Reply) tea.Cmd {
	if o := p.form; o != nil && o == m.workCapsOverlay() {
		if r.Err != nil {
			o.SetError(r.Err.Error())
			return nil
		}
		m.dismissOverlay()
		m.state = stateDefault
		return nil
	}
	if r.Err != nil {
		return m.handleError(r.Err)
	}
	return nil
}

// openWorkCaps opens the caps form over the view, on the scheduler block as
// the model last published it. Before the daemon has reported its queue
// there is no host to edit caps for.
func (m *home) openWorkCaps() {
	q := m.queue
	if !ui.WorkReported(q) {
		m.errBox.SetInfo("the daemon has not reported its work queue yet")
		return
	}
	host := overlay.WorkCapsHost{Name: q.Host.Name, Cores: q.Host.Cores, RAMBytes: q.Host.RAMBytes}
	o := overlay.NewWorkCapsOverlay(host, workDefaults(q.Host), capsValues(q.Settings), q.Paused)
	o.SetSize(int(float32(m.lastWidth)*ui.OverlayWidthPercent), int(float32(m.lastHeight)*ui.OverlayHeightPercent))
	m.setOverlay(o, overlayWorkCaps)
	m.state = stateWorkCaps
}

// handleStateWorkCapsKey drives the caps form. Esc closes it; Enter sends
// its values as SetSchedulerCaps and holds the form until the Reply
// (workReplied), which carries the model's refusal back into it.
func handleStateWorkCapsKey(m *home, msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	o := m.workCapsOverlay()
	if o == nil {
		return m, nil
	}
	closed, submit := o.HandleKeyPress(msg)
	switch {
	case closed:
		m.dismissOverlay()
		m.state = stateDefault
	case submit:
		o.SetSaving(true)
		m.core.SetSchedulerCaps(schedulerSettings(o.Values()), m.newReq(pendingReq{work: &pendingWork{form: o}}))
	}
	return m, nil
}

// workDefaults is the host's default for each field of the form, from the
// same function the model resolves its caps with. A host whose RAM could
// not be read has no memory default (the model refuses a block that does
// not set one), so those two fields stay 0 and the others keep theirs.
func workDefaults(h sched.Host) overlay.WorkCapsValues {
	p, err := sched.Resolve(config.SchedulerSettings{}, h)
	noMemory := err != nil
	if noMemory {
		p, err = sched.Resolve(config.SchedulerSettings{MemoryGB: 1}, h)
		if err != nil {
			return overlay.WorkCapsValues{}
		}
	}
	v := overlay.WorkCapsValues{
		CPUs:          p.Caps.CPUs,
		MemoryGB:      p.Caps.MemoryGB,
		IOJobs:        p.Caps.IOJobs,
		ClaimCPUs:     p.DefaultClaim.CPUs,
		ClaimMemoryGB: p.DefaultClaim.MemoryGB,
		StarveMinutes: int(p.StarveAfter / time.Minute),
		KeepFinished:  p.KeepFinished,
	}
	if noMemory {
		v.MemoryGB, v.ClaimMemoryGB = 0, 0
	}
	return v
}

// capsValues is the scheduler block as the form's numbers: 0 for a field the
// block leaves on the default.
func capsValues(s config.SchedulerSettings) overlay.WorkCapsValues {
	v := overlay.WorkCapsValues{CPUs: s.CPUs, MemoryGB: s.MemoryGB, IOJobs: s.IOJobs, StarveMinutes: s.StarveAfterMinutes, KeepFinished: s.KeepFinished}
	if s.DefaultClaim != nil {
		v.ClaimCPUs, v.ClaimMemoryGB = s.DefaultClaim.CPUs, s.DefaultClaim.MemoryGB
	}
	return v
}

// schedulerSettings is the scheduler block the form's numbers make. A form
// that edited no claim field leaves the block without a default claim.
func schedulerSettings(v overlay.WorkCapsValues) config.SchedulerSettings {
	s := config.SchedulerSettings{CPUs: v.CPUs, MemoryGB: v.MemoryGB, IOJobs: v.IOJobs, StarveAfterMinutes: v.StarveMinutes, KeepFinished: v.KeepFinished}
	if v.ClaimCPUs != 0 || v.ClaimMemoryGB != 0 {
		s.DefaultClaim = &config.ClaimSettings{CPUs: v.ClaimCPUs, MemoryGB: v.ClaimMemoryGB}
	}
	return s
}
```

- [ ] **Step 4: Run it to see it pass**

Run: `CGO_ENABLED=0 go test ./app/ -run 'TestWorkCaps|TestWorkDefaults|TestSchedulerSettings|TestWorkView|TestWorkPager'`
Expected: `ok  	github.com/aidan-bailey/loom/app	<time>`.

Then the gates for this task: `gofmt -l app/work.go app/work_test.go app/app.go app/overlay_host.go app/requests.go && CGO_ENABLED=0 go vet ./app/ && CGO_ENABLED=0 go test ./app/...`
Expected: `gofmt -l` prints nothing (if it lists a file, run `gofmt -w` on that file only), `go vet` is silent, and the app tests print `ok`.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(app): the work view's caps form" -- app/work.go app/work_test.go app/app.go app/overlay_host.go app/requests.go
```

### Task 6.5: Cancel, move to front, open the owner (`x`, `t`, `o`)

**Files:**
- Modify: `app/work.go` (`workModelKeys`, `workHint`, `handleWorkKey`; new code appended)
- Modify: `app/overview.go` (`listHas`, `slotShowing`, `focusSlotInstance`)
- Test: `app/work_test.go`

The three actions that act on a job. Each takes the job's ID from the selection at the moment its key passes the gate and uses only that ID afterwards: `x` names the job in a confirm and cancels the ID the confirm named, even if the queue changed under the dialog and the selection moved (the landmine shape the tests pin); `t` sends the ID of a queued job; `o` resolves the owner's `InstanceID` and finds its row by identity (`slotShowing`), opening the owner's workspace as a tab first when no slot shows it. Every request goes with a `ReqID` (`newReq`) so a refusal reaches the error line (`workReplied`). `x`, `t` and `o` need the model: offline the view refuses them with the offline gate's info line.

- [ ] **Step 1: Write the failing test**

In `app/work_test.go`, make the watcher record the two requests. Replace

```go
type workWatch struct {
	core.Core
	caps []config.SchedulerSettings
	reqs []core.ReqID
}
```

with

```go
type workWatch struct {
	core.Core
	cancels []workCancel
	fronts  []sched.JobID
	caps    []config.SchedulerSettings
	reqs    []core.ReqID
}

// workCancel is a CancelJob the work view sent.
type workCancel struct {
	id   sched.JobID
	from core.CancelFrom
}
```

and replace

```go
func (w *workWatch) SetSchedulerCaps(s config.SchedulerSettings, req core.ReqID) {
	w.caps = append(w.caps, s)
	w.reqs = append(w.reqs, req)
}
```

with

```go
func (w *workWatch) SetSchedulerCaps(s config.SchedulerSettings, req core.ReqID) {
	w.caps = append(w.caps, s)
	w.reqs = append(w.reqs, req)
}

func (w *workWatch) CancelJob(id sched.JobID, from core.CancelFrom, req core.ReqID) {
	w.cancels = append(w.cancels, workCancel{id: id, from: from})
	w.reqs = append(w.reqs, req)
}

func (w *workWatch) MoveJobToFront(id sched.JobID, req core.ReqID) {
	w.fronts = append(w.fronts, id)
	w.reqs = append(w.reqs, req)
}
```

Then append to `app/work_test.go`:

```go
func TestWorkCancel_ActsOnTheJobNamedWhenTheKeyPassed(t *testing.T) {
	m := workHome(t, queueJob(14, sched.Running, "polyoxide tests"), queueJob(15, sched.Running, "go test"))
	w := watchWork(t, m)
	require.Equal(t, sched.JobID(14), selectedWork(t, m))

	assert.Nil(t, pressWork(t, m, "x"))
	require.Equal(t, stateConfirm, m.state)
	assert.Contains(t, ansi.Strip(m.confirmation().Render()), `Cancel job 14 "polyoxide tests"?`)
	assert.Empty(t, w.cancels, "nothing is sent before the answer")

	// The queue changes under the dialog: job 14 leaves the view, and the
	// selection moves to the job that took its row.
	setQueue(t, m, testQueue(queueJob(15, sched.Running, "go test")))
	require.Equal(t, sched.JobID(15), selectedWork(t, m))

	_, _ = handleStateConfirmKey(m, keyFor(t, "y"))
	require.Len(t, w.cancels, 1)
	assert.Equal(t, sched.JobID(14), w.cancels[0].id, "the job the dialog named, not the one selected now")
	assert.Equal(t, core.CancelFromView, w.cancels[0].from)
	assert.NotZero(t, w.reqs[0], "the cancel carries a ReqID, so a refusal reaches the user")
	assert.Equal(t, stateDefault, m.state)
	assert.Equal(t, viewWork, m.viewMode)
}

func TestWorkCancel_ACancelledDialogSendsNothing(t *testing.T) {
	for _, answer := range []string{"n", "esc"} {
		t.Run(answer, func(t *testing.T) {
			m := workHome(t, queueJob(14, sched.Running, "a"))
			w := watchWork(t, m)
			pressWork(t, m, "x")
			require.Equal(t, stateConfirm, m.state)
			_, _ = handleStateConfirmKey(m, keyFor(t, answer))
			assert.Empty(t, w.cancels)
			assert.Equal(t, stateDefault, m.state)
		})
	}
}

func TestWorkCancel_QueuedAndRunningJobsCanBeCancelledFinishedOnesCannot(t *testing.T) {
	m := workHome(t, queueJob(14, sched.Running, "a"), queueJob(16, sched.Queued, "b"), queueJob(12, sched.Done, "c"))
	w := watchWork(t, m)

	pressWork(t, m, "j") // job 16, queued
	pressWork(t, m, "x")
	require.Equal(t, stateConfirm, m.state)
	_, _ = handleStateConfirmKey(m, keyFor(t, "y"))
	require.Len(t, w.cancels, 1)
	assert.Equal(t, sched.JobID(16), w.cancels[0].id)

	pressWork(t, m, "j") // job 12, done
	cmd := pressWork(t, m, "x")
	assert.Nil(t, cmd)
	assert.Equal(t, stateDefault, m.state, "a finished job has nothing to cancel: no dialog")
	assert.Contains(t, m.errBox.String(), "job 12 has finished")
	assert.Len(t, w.cancels, 1)
}

func TestWorkCancel_NothingSelectedNothingAsked(t *testing.T) {
	m := workHome(t)
	assert.Nil(t, pressWork(t, m, "x"))
	assert.Equal(t, stateDefault, m.state)
}

func TestWorkFront_SendsTheSelectedQueuedJobsID(t *testing.T) {
	m := workHome(t, queueJob(14, sched.Running, "a"), queueJob(16, sched.Queued, "b"), queueJob(17, sched.Queued, "c"))
	w := watchWork(t, m)

	pressWork(t, m, "j")
	pressWork(t, m, "j") // job 17
	assert.Nil(t, pressWork(t, m, "t"))
	assert.Equal(t, []sched.JobID{17}, w.fronts)
	assert.NotZero(t, w.reqs[0])

	pressWork(t, m, "k")
	pressWork(t, m, "k") // job 14, running
	pressWork(t, m, "t")
	assert.Equal(t, []sched.JobID{17}, w.fronts, "a running job is not sent")
	assert.Contains(t, m.errBox.String(), "job 14 isn't waiting")
}

func TestWorkFront_AModelRefusalReachesTheUser(t *testing.T) {
	m := workHome(t, queueJob(16, sched.Queued, "b"))
	w := watchWork(t, m)
	pressWork(t, m, "t")
	require.Len(t, w.fronts, 1)
	_ = m.applyCoreEvent(core.Reply{Req: w.reqs[0], Err: errors.New("job 16 isn't waiting")})
	assert.Contains(t, m.errBox.String(), "job 16 isn't waiting")
}

func TestWorkCancel_ARefusalReachesTheUser(t *testing.T) {
	m := workHome(t, queueJob(14, sched.Running, "a"))
	w := watchWork(t, m)
	pressWork(t, m, "x")
	_, _ = handleStateConfirmKey(m, keyFor(t, "y"))
	require.Len(t, w.cancels, 1)
	_ = m.applyCoreEvent(core.Reply{Req: w.reqs[0], Err: errors.New("job 14 has finished")})
	assert.Contains(t, m.errBox.String(), "job 14 has finished")
}

func TestWorkActions_OfflineRefusesTheKeysThatNeedTheModel(t *testing.T) {
	m := workHome(t, queueJob(14, sched.Running, "a"), queueJob(16, sched.Queued, "b"))
	w := watchWork(t, m)
	pressWork(t, m, "j") // job 16: t and x would both act
	m.link.state = linkWaiting
	for _, key := range []string{"x", "t", "o", "e"} {
		m.errBox.Clear()
		cmd := pressWork(t, m, key)
		assert.Nil(t, cmd, key)
		assert.Equal(t, stateDefault, m.state, "%q opens nothing offline", key)
		assert.Contains(t, m.errBox.String(), "the loom daemon is stopped: "+key+" needs it", key)
	}
	assert.Empty(t, w.cancels)
	assert.Empty(t, w.fronts)
	assert.Empty(t, w.caps)

	m.link.state = linkConnected
	m.errBox.Clear()
	pressWork(t, m, "t")
	assert.Equal(t, []sched.JobID{16}, w.fronts, "connected, the same key acts")
}

func TestWorkView_TheStatusLineListsTheKeys(t *testing.T) {
	m := workHome(t, queueJob(14, sched.Running, "a"))
	assert.Contains(t, ansi.Strip(m.View().Content), "w/esc back · enter log · x cancel · t to front · o open session · e caps · q quit")
}

func TestFocusSlotInstance_FocusesByIdentity(t *testing.T) {
	m := fleetHome(t)
	f2 := m.slots[0].list.GetInstanceByTitle("f2")
	b1 := m.slots[1].list.GetInstanceByTitle("b1")
	require.NotNil(t, f2)
	require.NotNil(t, b1)

	idx, ok := m.slotShowing(b1.ID)
	assert.True(t, ok)
	assert.Equal(t, 1, idx)
	_, ok = m.slotShowing(core.InstanceID(9999))
	assert.False(t, ok)

	assert.False(t, m.focusSlotInstance(0, b1.ID), "the row is not in that slot: nothing changes")
	assert.Equal(t, 0, m.focusedSlot)
	assert.True(t, m.focusSlotInstance(0, f2.ID))
	assert.Equal(t, f2.ID, selID(m.list))
	assert.True(t, m.focusSlotInstance(1, b1.ID))
	assert.Equal(t, 1, m.focusedSlot)
	assert.Equal(t, b1.ID, selID(m.list))
	require.NoError(t, m.checkSlotInvariant())
}

func TestFocusSlotInstance_ClassicModeHasOneSlot(t *testing.T) {
	m := newTestHome(t)
	a := mustAddInstance(t, m, "a")
	id := idOf(m, a)
	idx, ok := m.slotShowing(id)
	assert.True(t, ok)
	assert.Equal(t, 0, idx)
	assert.True(t, m.focusSlotInstance(0, id))
	assert.Equal(t, id, selID(m.list))
	assert.False(t, m.focusSlotInstance(0, core.InstanceID(9999)))
}

func TestWorkOpen_FocusesTheOwnersSessionInItsWorkspace(t *testing.T) {
	m := fleetHome(t)
	m.errBox = ui.NewErrBox()
	m.errBox.SetSize(400, 1)
	m.updateHandleWindowSizeEvent(tea.WindowSizeMsg{Width: 120, Height: 40})
	b1 := m.slots[1].list.GetInstanceByTitle("b1")
	require.NotNil(t, b1)
	job := queueJob(14, sched.Running, "tests")
	job.Owner, job.OwnerTitle, job.OwnerWorkspace = b1.ID, "b1", "bpeer"
	job.OwnerWorkspaceID = 2 // not opened: the row is found by its ID in the open slots
	setQueue(t, m, testQueue(job))
	m.enterWorkView()
	require.Equal(t, viewWork, m.viewMode)
	require.Equal(t, 0, m.focusedSlot)

	pressWork(t, m, "o")
	assert.Equal(t, viewFocus, m.viewMode, "o drops to the focus layout")
	assert.Equal(t, 1, m.focusedSlot, "the owner's workspace is focused")
	assert.Equal(t, b1.ID, selID(m.list), "and its session selected")
	require.NoError(t, m.checkSlotInvariant())
}

func TestWorkOpen_SaysWhyItCannot(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*core.JobView)
		want   string
	}{
		{"no owner", func(j *core.JobView) {}, "job 14 has no owner"},
		{"an owner that is not served (no workspace ID)", func(j *core.JobView) { j.Owner = 77 }, "job 14's session isn't served"},
		{"an owner whose workspace the model no longer serves", func(j *core.JobView) {
			j.Owner, j.OwnerTitle, j.OwnerWorkspace, j.OwnerWorkspaceID = 77, "g", "ghost", 77
		}, "serves no such workspace"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job := queueJob(14, sched.Running, "tests")
			tc.mutate(&job)
			m := workHome(t, job)
			pressWork(t, m, "o")
			assert.Equal(t, viewWork, m.viewMode, "the view stays")
			assert.Contains(t, m.errBox.String(), tc.want)
		})
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `CGO_ENABLED=0 go test ./app/ -run 'TestWorkCancel|TestWorkFront|TestWorkActions|TestWorkOpen|TestFocusSlotInstance|TestWorkView_TheStatusLine'`

Expected: the package fails to build, printing `m.slotShowing undefined (type *home has no field or method slotShowing)` and `m.focusSlotInstance undefined (type *home has no field or method focusSlotInstance)`, and then `FAIL github.com/aidan-bailey/loom/app [build failed]`.

- [ ] **Step 3: Implement**

In `app/overview.go`, add the two primitives beside `focusCursorSlot`. Replace

```go
// peerSectionFor summarizes one non-focused slot's instance statuses
```

with

```go
// listHas reports whether list shows the instance id.
func listHas(list *ui.List, id core.InstanceID) bool {
	for _, v := range list.GetInstances() {
		if v.ID == id {
			return true
		}
	}
	return false
}

// slotShowing finds the open slot whose rail shows the instance id: its
// index in m.slots, or 0 for the classic slot while no tab is open. false
// when no open slot shows it. A title is not unique across workspaces, so
// the instance is found by its ID.
func (m *home) slotShowing(id core.InstanceID) (int, bool) {
	if len(m.slots) == 0 {
		return 0, listHas(m.list, id)
	}
	for i, s := range m.slots {
		if listHas(s.list, id) {
			return i, true
		}
	}
	return 0, false
}

// focusSlotInstance focuses the slot at idx (loadSlot) and selects the row
// id in it, by identity, like focusCursorSlot does by position. It changes
// nothing and reports false when that slot's rail does not show id.
// Main-goroutine only.
func (m *home) focusSlotInstance(idx int, id core.InstanceID) bool {
	if len(m.slots) == 0 {
		if !listHas(m.list, id) {
			return false
		}
		m.list.SelectID(id)
		return true
	}
	if idx < 0 || idx >= len(m.slots) || !listHas(m.slots[idx].list, id) {
		return false
	}
	if idx != m.focusedSlot {
		m.loadSlot(idx)
	}
	m.list.SelectID(id)
	return true
}

// peerSectionFor summarizes one non-focused slot's instance statuses
```

In `app/work.go`, four edits and the new code.

The hint. Replace

```go
	return "w/esc back · enter log · e caps · q quit"
```

with

```go
	return "w/esc back · enter log · x cancel · t to front · o open session · e caps · q quit"
```

The keys that need the model. Replace

```go
var workModelKeys = map[string]bool{"e": true}
```

with

```go
var workModelKeys = map[string]bool{"x": true, "t": true, "o": true, "e": true}
```

The key handler. In `handleWorkKey`, replace

```go
	case "enter":
		m.openWorkPager()
	case "e":
		m.openWorkCaps()
	default:
```

with

```go
	case "enter":
		m.openWorkPager()
	case "x":
		return m, m.askCancelWorkJob()
	case "t":
		m.frontWorkJob()
	case "o":
		return m, m.focusWorkOwner()
	case "e":
		m.openWorkCaps()
	default:
```

Append the actions after `schedulerSettings`. Replace

```go
	if v.ClaimCPUs != 0 || v.ClaimMemoryGB != 0 {
		s.DefaultClaim = &config.ClaimSettings{CPUs: v.ClaimCPUs, MemoryGB: v.ClaimMemoryGB}
	}
	return s
}
```

with

```go
	if v.ClaimCPUs != 0 || v.ClaimMemoryGB != 0 {
		s.DefaultClaim = &config.ClaimSettings{CPUs: v.ClaimCPUs, MemoryGB: v.ClaimMemoryGB}
	}
	return s
}

// askCancelWorkJob is x: a confirm for cancelling the selected job. The job
// is taken now, named in the prompt by its ID, and the cancel goes to that
// ID whatever is selected when the answer comes: the queue may change under
// the dialog, and the model re-validates (a job that has finished by then
// is refused). A finished job has nothing to cancel, so it asks nothing.
func (m *home) askCancelWorkJob() tea.Cmd {
	job, ok := m.workSelected()
	if !ok {
		return nil
	}
	if job.State.Finished() {
		m.errBox.SetInfo(fmt.Sprintf("job %d has finished", job.ID))
		return nil
	}
	id := job.ID
	return m.confirmTask("Cancel "+ui.JobLabel(job)+"?", overlay.ConfirmationTask{Sync: func() {
		m.core.CancelJob(id, core.CancelFromView, m.newReq(pendingReq{work: &pendingWork{}}))
	}})
}

// frontWorkJob is t: the selected queued job goes to the front of the line.
// Only a queued job can; the model re-validates it.
func (m *home) frontWorkJob() {
	job, ok := m.workSelected()
	if !ok {
		return
	}
	if job.State != sched.Queued {
		m.errBox.SetInfo(fmt.Sprintf("job %d isn't waiting", job.ID))
		return
	}
	m.core.MoveJobToFront(job.ID, m.newReq(pendingReq{work: &pendingWork{}}))
}

// focusWorkOwner is o: it leaves the view for the focus layout on the
// selected job's owner. The row is found by the owner's InstanceID in the
// open slots; when none shows it, the owner's workspace is opened as a tab
// first, by the ID the job carries (openTab(job.OwnerWorkspaceID)), never
// by the workspace's label, which is for display only. An owner the model
// does not serve has no workspace ID (0), and the key says so.
func (m *home) focusWorkOwner() tea.Cmd {
	job, ok := m.workSelected()
	if !ok {
		return nil
	}
	switch {
	case job.Owner == 0:
		m.errBox.SetInfo(fmt.Sprintf("job %d has no owner", job.ID))
		return nil
	case job.OwnerWorkspaceID == 0:
		m.errBox.SetInfo(fmt.Sprintf("job %d's session isn't served", job.ID))
		return nil
	}
	var opened tea.Cmd
	idx, found := m.slotShowing(job.Owner)
	if !found {
		cmd, err := m.openTab(job.OwnerWorkspaceID)
		if err != nil {
			return m.handleError(err)
		}
		opened = cmd
		m.persistOpenList()
		idx, found = m.slotShowing(job.Owner)
	}
	if !found || !m.focusSlotInstance(idx, job.Owner) {
		m.errBox.SetInfo(fmt.Sprintf("job %d's session isn't in workspace %s", job.ID, job.OwnerWorkspace))
		return opened
	}
	m.work.pager = nil
	m.viewMode = viewFocus
	m.mutateUIPrefs(func(p *config.UIPrefs) { p.ViewMode = "" })
	m.persistFocusedWorkspace()
	return tea.Batch(opened, m.instanceChanged())
}
```

- [ ] **Step 4: Run it to see it pass**

Run: `CGO_ENABLED=0 go test ./app/ -run 'TestWorkCancel|TestWorkFront|TestWorkActions|TestWorkOpen|TestFocusSlotInstance|TestWorkView|TestWorkPager|TestWorkCaps'`
Expected: `ok  	github.com/aidan-bailey/loom/app	<time>`.

Then the gates for this task: `gofmt -l app/work.go app/work_test.go app/overview.go && CGO_ENABLED=0 go vet ./app/ && CGO_ENABLED=0 go test ./app/...`
Expected: `gofmt -l` prints nothing (if it lists a file, run `gofmt -w` on that file only), `go vet` is silent, and the app tests print `ok`.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(app): cancel, move to front and open the owner from the work view" -- app/work.go app/work_test.go app/overview.go
```

### Task 6.6: `w` through the script engine (`toggle_work_view`)

**Files:**
- Modify: `script/host.go` (the `Host` interface)
- Modify: `script/api_actions.go` (`installSyncActions`)
- Modify: `script/defaults.lua`
- Modify: `script/host_fake_test.go` (the fake host the script tests use)
- Modify: `app/app_scripts.go` (`scriptHost.ToggleWorkView`)
- Modify: `app/state_default.go` (`overviewKeyAllowed`, `offlineKeyAllowed`)
- Test: `script/api_actions_test.go`, `app/work_test.go`

`w` is bound in `script/defaults.lua` to the sync action `cs.actions.toggle_work_view()`, which calls `Host.ToggleWorkView()`; the app's `scriptHost` defers `toggleWorkView` as a focus mutation (applied only in `stateDefault`, as `ToggleOverview` is). `w` is let through where it must work: overview's whitelist (the view is entered from focus or overview) and the offline gate (it touches only the TUI), while the workbench still drops it. The work view sends `w` to the engine too (`workScriptKeys`), so one binding enters and leaves the view and a user's rebinding moves both. `USAGE.md`'s Keyboard Reference and `docs/specs/scripting.md` are Story 7's. The Host interface gains a method, so `script/host_fake_test.go` (which no other entry of this story lists) gains it as well, or the script tests stop compiling.

- [ ] **Step 1: Write the failing test**

In `script/host_fake_test.go`, add the fake's counter and method. Replace

```go
	toggleOverviewCalls     int
```

with

```go
	toggleOverviewCalls     int
	toggleWorkViewCalls     int
```

and replace

```go
func (f *fakeHost) ToggleOverview()     { f.toggleOverviewCalls++ }
```

with

```go
func (f *fakeHost) ToggleOverview()     { f.toggleOverviewCalls++ }
func (f *fakeHost) ToggleWorkView()     { f.toggleWorkViewCalls++ }
```

Append to `script/api_actions_test.go`:

```go
func TestSyncActions_ToggleWorkViewReachesTheHost(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	h := &fakeHost{}
	e.BeginLoad("t.lua")
	require.NoError(t, e.L.DoString(`cs.bind("Q", function() cs.actions.toggle_work_view() end)`))
	e.EndLoad()

	matched, err := e.Dispatch(context.Background(), "Q", h)
	require.NoError(t, err)
	assert.True(t, matched)
	assert.Equal(t, 1, h.toggleWorkViewCalls)
}

func TestDefaults_WTogglesTheWorkView(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	e.LoadDefaults()
	h := &fakeHost{}

	matched, err := e.Dispatch(context.Background(), "w", h)
	require.NoError(t, err)
	assert.True(t, matched, "script/defaults.lua binds w")
	assert.Equal(t, 1, h.toggleWorkViewCalls)
}
```

Append to `app/work_test.go`:

```go
func TestToggleWorkView_ScriptHostEntersAndLeaves(t *testing.T) {
	m := newTestHome(t)
	toggle := func() {
		host := newScriptHost(m)
		host.ToggleWorkView()
		_, _, actions := host.drain()
		require.Len(t, actions, 1, "ToggleWorkView must defer exactly one model mutation")
		_ = m.handleScriptDone(scriptDoneMsg{pendingActions: actions})
	}

	toggle()
	assert.Equal(t, viewWork, m.viewMode)
	toggle()
	assert.Equal(t, viewFocus, m.viewMode)

	m.state = stateSettings
	toggle()
	assert.Equal(t, viewFocus, m.viewMode, "a dialog that opened meanwhile keeps the layout under it")
}

func TestWKey_TogglesTheWorkViewThroughTheScriptEngine(t *testing.T) {
	m := newTestHome(t)
	press := func(key string) {
		t.Helper()
		_, cmd := handleStateDefaultKey(m, keyFor(t, key))
		require.NotNil(t, cmd, "%q dispatches to the script engine", key)
		msg, ok := cmd().(scriptDoneMsg)
		require.True(t, ok)
		require.NoError(t, msg.err)
		_ = m.handleScriptDone(msg)
	}

	press("w")
	assert.Equal(t, viewWork, m.viewMode, "w enters from focus")
	press("w")
	assert.Equal(t, viewFocus, m.viewMode, "and leaves")

	m.viewMode = viewOverview
	press("w")
	assert.Equal(t, viewWork, m.viewMode, "w enters from overview")
	press("w")
	assert.Equal(t, viewOverview, m.viewMode, "and returns to it")
}

func TestWKey_TheWorkbenchDropsIt(t *testing.T) {
	m := newTestHome(t)
	m.viewMode = viewWorkbench
	_, cmd := handleStateDefaultKey(m, keyFor(t, "w"))
	assert.Nil(t, cmd)
	assert.Equal(t, viewWorkbench, m.viewMode)
}

func TestWKey_WorksOffline(t *testing.T) {
	m := newTestHome(t)
	m.link.state = linkWaiting
	for _, want := range []viewMode{viewWork, viewFocus} {
		_, cmd := handleStateDefaultKey(m, keyFor(t, "w"))
		require.NotNil(t, cmd, "w only moves the TUI: the offline gate lets it through")
		msg, ok := cmd().(scriptDoneMsg)
		require.True(t, ok)
		_ = m.handleScriptDone(msg)
		assert.Equal(t, want, m.viewMode)
	}
}

func TestWorkView_TheQueueStaysReadableOffline(t *testing.T) {
	m := workHome(t, queueJob(14, sched.Running, "a"), queueJob(16, sched.Queued, "b"))
	m.link.state = linkWaiting
	pressWork(t, m, "j")
	assert.Equal(t, sched.JobID(16), selectedWork(t, m), "the last replica can still be walked")
	pressWork(t, m, "enter")
	assert.NotNil(t, m.work.pager, "and its logs opened")
	pressWork(t, m, "esc")
	pressWork(t, m, "esc")
	assert.Equal(t, viewFocus, m.viewMode, "esc leaves")
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `CGO_ENABLED=0 go test ./script/ -run 'TestSyncActions_ToggleWorkView|TestDefaults_W'`
Expected: `TestSyncActions_ToggleWorkViewReachesTheHost` fails at `require.NoError` with an error containing `attempt to call a nil value (field 'toggle_work_view')`, and `TestDefaults_WTogglesTheWorkView` fails with `Should be true` (`script/defaults.lua binds w`), then `FAIL github.com/aidan-bailey/loom/script`.

Run: `CGO_ENABLED=0 go test ./app/ -run 'TestToggleWorkView|TestWKey|TestWorkView_TheQueueStaysReadableOffline'`
Expected: the package fails to build, printing `host.ToggleWorkView undefined (type *scriptHost has no field or method ToggleWorkView)` and then `FAIL github.com/aidan-bailey/loom/app [build failed]`.

- [ ] **Step 3: Implement**

In `script/host.go`, add the method to the interface. Replace

```go
	ToggleOverview()
	ResizeSplitUp()
```

with

```go
	ToggleOverview()
	// ToggleWorkView flips between the work view (the jobs sessions
	// submitted with loom job, over the whole host) and the focus or
	// overview layout it was entered from. Not persisted.
	ToggleWorkView()
	ResizeSplitUp()
```

In `script/api_actions.go`, add the action before `resize_split_up`. Replace

```go
	actions.RawSetString("resize_split_up", L.NewFunction(func(L *lua.LState) int {
```

with

```go
	actions.RawSetString("toggle_work_view", L.NewFunction(func(L *lua.LState) int {
		if e.curHost != nil {
			e.curHost.ToggleWorkView()
		}
		return 0
	}))
	actions.RawSetString("resize_split_up", L.NewFunction(func(L *lua.LState) int {
```

In `script/defaults.lua`, bind the key in the Fleet block. Replace

```lua
cs.bind("tab", function() cs.actions.toggle_overview() end,     { help = "overview" })
```

with

```lua
cs.bind("tab", function() cs.actions.toggle_overview() end,     { help = "overview" })
cs.bind("w", function() cs.actions.toggle_work_view() end, { help = "work queue" })
```

In `app/app_scripts.go`, implement the host method. Replace

```go
// ResizeSplitUp implements script.Host (divider up = agent shrinks).
```

with

```go
// ToggleWorkView implements script.Host. Not persisted, and applied only in
// stateDefault like the other focus mutations, so a dialog that opened
// meanwhile keeps the layout under it.
func (s *scriptHost) ToggleWorkView() {
	s.deferFocusMutation(func(m *home) { m.toggleWorkView() })
}

// ResizeSplitUp implements script.Host (divider up = agent shrinks).
```

In `app/state_default.go`, let `w` through the two gates. In `overviewKeyAllowed`, replace

```go
	"q": true, "?": true, "W": true, "S": true,
```

with

```go
	"q": true, "?": true, "W": true, "S": true, "w": true,
```

and in `offlineKeyAllowed`, replace

```go
	"ctrl+left": true, "ctrl+right": true,
}
```

with

```go
	"ctrl+left": true, "ctrl+right": true, "w": true,
}
```

- [ ] **Step 4: Run it to see it pass**

Run: `CGO_ENABLED=0 go test ./script/... ./app/... ./ui/...`
Expected: `ok` for `script`, `app`, `ui` and `ui/overlay` (the app suite takes a while).

Then the gates for this task: `gofmt -l app/work.go app/work_test.go app/app.go app/app_scripts.go app/state_default.go app/overview.go app/overlay_host.go app/requests.go ui/work.go ui/work_test.go ui/overlay/workCapsOverlay.go ui/overlay/workCapsOverlay_test.go script/host.go script/api_actions.go script/api_actions_test.go script/host_fake_test.go && CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test ./...`
Expected: `gofmt -l` prints nothing (if it names a file, run `gofmt -w` on that file only), `go vet` is silent, and every package prints `ok` or `no test files`.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(app,script): w toggles the work view" -- script/host.go script/api_actions.go script/defaults.lua script/host_fake_test.go script/api_actions_test.go app/app_scripts.go app/state_default.go app/work_test.go
```

### Task 6.7: A slot load keeps the work view open (`applyUIPrefs`)

**Files:**
- Modify: `app/app.go` (`applyUIPrefs`)
- Test: `app/work_test.go`

`applyUIPrefs` runs after every `loadSlot` that changes the focused slot (and in `enterGlobalMode` and `startHome`), and it sets `m.viewMode` to the mode the slot's persisted prefs choose, whatever the mode is. Paths to it that run while the work view is open: a rejoin's `resync`, and the `closeUnservedMsg` that a `WorkspacesChanged` event schedules, both via `closeUnserved` (a closed focused tab is `deactivateWorkspace`, which calls `loadSlot`; no tab left is `enterGlobalMode`); a deferred script action (`WorkspaceNext`/`WorkspacePrev`, applied in `stateDefault`, which the work view is in) via `switchWorkspaceSlot`; and this story's own `focusWorkOwner` (`openTab`, `focusSlotInstance`), which sets `viewFocus` itself right after. Left alone, such a load would close the view under the user and leave `m.work` (its selection, tail and pager) describing a view that is not shown. The fix: while `m.viewMode == viewWork`, `applyUIPrefs` leaves the mode alone and records the mode the slot's prefs choose as `m.work.from`, the mode `w` or `esc` returns to (spec 7.1: the view is not persisted, so the prefs are only read). Everything else it does (rail, terminal, ratio, layout) still runs. Outside the work view it behaves as before. `m.work` holds only job IDs and the log reads, which no slot owns, so nothing in it goes stale. `loadSlot` calls `applyUIPrefs` only on a real slot change, so no other path needs the fix. This edits `applyUIPrefs`, a site of `app/app.go` that no other task of the story touches.

- [ ] **Step 1: Write the failing test**

In `app/work_test.go`, append:

```go
// twoTabsWorkPrefs opens tabs ws-a and ws-b with ws-a focused and ws-a's
// persisted view mode empty (focus), and gives ws-b the persisted view mode
// bPrefs, as a user who left that tab in overview did.
func twoTabsWorkPrefs(t *testing.T, bPrefs string) *home {
	t.Helper()
	m, _ := twoTabsWithPendingRatio(t)
	m.loadSlot(1)
	m.mutateUIPrefs(func(p *config.UIPrefs) { p.ViewMode = bPrefs })
	m.loadSlot(0)
	require.Equal(t, "ws-a", m.name())
	require.Equal(t, bPrefs, m.slots[1].uiPrefs().ViewMode, "fixture: ws-b's prefs hold the view mode")
	require.Equal(t, viewFocus, m.viewMode, "fixture: ws-a's prefs choose focus")
	return m
}

func TestWorkView_StaysOpenAcrossASlotLoad(t *testing.T) {
	cases := []struct {
		name     string
		bPrefs   string
		cameFrom viewMode
		want     viewMode
	}{
		{"prefs choose overview", "overview", viewFocus, viewOverview},
		{"prefs choose focus", "", viewOverview, viewFocus},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			load := map[string]func(m *home){
				"loadSlot":      func(m *home) { m.loadSlot(1) },
				"closing a tab": func(m *home) { _, err := m.deactivateWorkspace("ws-a"); require.NoError(t, err) },
			}
			for path, run := range load {
				t.Run(path, func(t *testing.T) {
					m := twoTabsWorkPrefs(t, tc.bPrefs)
					m.viewMode = tc.cameFrom
					m.enterWorkView()
					require.Equal(t, viewWork, m.viewMode)
					m.work.sel, m.work.selIdx = 14, 2
					m.work.pager = &workPager{id: 14}

					run(m)

					require.Equal(t, "ws-b", m.name(), "fixture: the other slot is focused")
					assert.Equal(t, viewWork, m.viewMode, "a slot load must not close the work view")
					assert.Equal(t, tc.want, m.work.from, "w and esc return to the mode the loaded slot's prefs choose")
					assert.Equal(t, sched.JobID(14), m.work.sel, "the selection is the job's, not the slot's")
					assert.Equal(t, 2, m.work.selIdx)
					require.NotNil(t, m.work.pager, "the pager stays open")
					assert.Equal(t, sched.JobID(14), m.work.pager.id)

					m.toggleWorkView()
					assert.Equal(t, tc.want, m.viewMode, "w returns to the mode the slot's prefs chose")
				})
			}
		})
	}
}

func TestApplyUIPrefs_OutsideTheWorkViewStillChoosesTheMode(t *testing.T) {
	for _, from := range []viewMode{viewFocus, viewOverview, viewWorkbench} {
		for prefs, want := range map[string]viewMode{"overview": viewOverview, "": viewFocus} {
			m := newTestHome(t)
			require.NoError(t, m.appState().SetUIPrefs(config.UIPrefs{ViewMode: prefs}))
			m.syncWorkspaces()
			m.viewMode = from
			m.applyUIPrefs()
			assert.Equal(t, want, m.viewMode, "from mode %d with prefs %q", from, prefs)
		}
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `CGO_ENABLED=0 go test ./app/ -run 'TestWorkView_StaysOpenAcrossASlotLoad|TestApplyUIPrefs_OutsideTheWorkViewStillChoosesTheMode'`
Expected: `TestWorkView_StaysOpenAcrossASlotLoad` fails in all four subtests at `a slot load must not close the work view` (`Not equal`: expected the `viewWork` value, actual the mode the prefs chose), and `FAIL github.com/aidan-bailey/loom/app`. `TestApplyUIPrefs_OutsideTheWorkViewStillChoosesTheMode` passes: it pins the behavior the fix must keep.

- [ ] **Step 3: Implement**

In `app/app.go`, replace

```go
// applyUIPrefs pushes persisted layout prefs onto the components.
// Called at the end of startHome (classic startup), after a slot is
// loaded/focused (loadSlot), and on entering global mode.
func (m *home) applyUIPrefs() {
	if m.id == 0 {
		// Bare test homes construct no app state; nothing to apply.
		return
	}
	p := m.uiPrefs()
	if p.ViewMode == "overview" {
		m.enterOverview()
	} else {
		m.viewMode = viewFocus
	}
```

with

```go
// applyUIPrefs pushes persisted layout prefs onto the components.
// Called at the end of startHome (classic startup), after a slot is
// loaded/focused (loadSlot), and on entering global mode.
//
// While the work view is up it keeps the view: a load that happens under
// it (a rejoin, a tab closed by the daemon) only changes the mode w and
// esc return to, to the one the slot's prefs choose. The view is not
// persisted, so nothing is written.
func (m *home) applyUIPrefs() {
	if m.id == 0 {
		// Bare test homes construct no app state; nothing to apply.
		return
	}
	p := m.uiPrefs()
	mode := viewFocus
	if p.ViewMode == "overview" {
		mode = viewOverview
	}
	switch {
	case m.viewMode == viewWork:
		m.work.from = mode
	case mode == viewOverview:
		m.enterOverview()
	default:
		m.viewMode = viewFocus
	}
```

- [ ] **Step 4: Run it to see it pass**

Run: `CGO_ENABLED=0 go test ./app/ -run 'TestWorkView_StaysOpenAcrossASlotLoad|TestApplyUIPrefs'`
Expected: `ok  	github.com/aidan-bailey/loom/app	<time>` (this also runs `TestApplyUIPrefs_RestoresViewMode`, which still passes).

Then the gates for the story: `gofmt -l app/work.go app/work_test.go app/app.go app/app_scripts.go app/state_default.go app/overview.go app/overlay_host.go app/requests.go ui/work.go ui/work_test.go ui/overlay/workCapsOverlay.go ui/overlay/workCapsOverlay_test.go script/host.go script/api_actions.go script/api_actions_test.go script/host_fake_test.go && CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test ./...`
Expected: `gofmt -l` prints nothing (if it names a file, run `gofmt -w` on that file only), `go vet` is silent, and every package prints `ok` or `no test files`. After this task the story's Verify line passes: `CGO_ENABLED=0 go test ./app/... ./ui/... ./script/...`.

- [ ] **Step 5: Commit**

```bash
git commit -m "fix(app): a slot load keeps the work view open" -- app/app.go app/work_test.go
```

## Story 7: Badges on session cards, and the docs

- **Developer:** intermediate-dev
- **Files:** `ui/card.go`, `ui/overview.go`, `ui/card_test.go`, `app/work_badge.go`, `app/work_badge_test.go`, `app/core_glue.go`, `USAGE.md`, `docs/ARCHITECTURE.md`, `docs/specs/scripting.md`, `docs/claude/the-work-queue.md`, `docs/claude/INDEX.md`, `sched/CLAUDE.md`, `core/README.md`, `app/README.md`, `ui/README.md`, `README.md`, `docs/claude/adding-a-core-event.md`
- **Interfaces:** exposes none; uses Story 4's `home.queue` and `JobView`, and Story 6's `toggle_work_view`
- **Verify:** `CGO_ENABLED=0 go test ./app/... ./ui/... ./tools/claudemd/... && CGO_ENABLED=0 go run ./tools/claudemd`
- **Review:** deep

A session's rail card and overview card show a badge for its jobs: `⚙ job 14` for the lowest running job ID (`⚙ job 14 +2` when two more run), or `⚙ queued` when it has only queued ones. The text is computed in `app` from `home.queue` (`ownerJobBadges`) and handed to `ui` through a package-level map, `ui.SetJobBadges`, which `BuildCardData` reads by `InstanceID`: this follows `ui.SetShowAccounts`, because the rail's `BuildCardData` call lives in `ui/list.go`, which this story does not touch. `app` republishes the map from `applyCoreEvent`'s `QueueChanged` case. The docs follow: `USAGE.md` (`loom job`, the work view and its keys, the `scheduler` block, `KillMode=process`), `docs/ARCHITECTURE.md` (the `sched` row, `jobs/`, the queue in the daemon's work with no TUI), `docs/specs/scripting.md` (`toggle_work_view`), and the guide `docs/claude/the-work-queue.md` with its `INDEX.md` entry. None of this code holds a landmine marker.

### Task 7.1: The badge on the rail and overview cards

**Files:**
- Modify: `ui/card.go:74-84` (a constant, the badge map and its token), `ui/card.go:157-160` (`CardData`), `ui/card.go:179-191` (`BuildCardData`), `ui/card.go:631-646` (`RenderCard`'s title line)
- Modify: `ui/overview.go:276-280` (`renderOverviewCard`'s meta tokens)
- Test: `ui/card_test.go` (append)

The badge rides the rail card's title line, left of the account badge, and the overview card's meta line, first. Both obey the layout rules the account badge already follows: the rail decides to show it from the card's width and the badge's cap alone (never from the title), and every overview card keeps `overviewCardHeight` lines.

- [ ] **Step 1: Write the failing test**

Append to `ui/card_test.go` (it already imports `strings`, `testing`, `core`, `session`, `ansi` and `assert`):

```go
// withJobBadges publishes badges for the test and clears them at cleanup.
func withJobBadges(t *testing.T, b map[core.InstanceID]string) {
	t.Helper()
	SetJobBadges(b)
	t.Cleanup(func() { SetJobBadges(nil) })
}

// railFirstLine is the rail card's title line, styling stripped.
func railFirstLine(d CardData, width int) string {
	return ansi.Strip(strings.Split(RenderCard(d, DensityRail, width), "\n")[0])
}

func TestBuildCardData_CarriesTheJobBadge(t *testing.T) {
	withJobBadges(t, map[core.InstanceID]string{7: "⚙ job 14 +1"})

	with := BuildCardData(core.InstanceView{ID: 7, Title: "t", Status: session.Ready}, Pane{}, false, "", 0)
	assert.Equal(t, "⚙ job 14 +1", with.JobBadge)

	without := BuildCardData(core.InstanceView{ID: 8, Title: "t", Status: session.Ready}, Pane{}, false, "", 0)
	assert.Equal(t, "", without.JobBadge, "a session the map does not name has no badge")

	SetJobBadges(nil)
	assert.Equal(t, "", JobBadge(7), "clearing the map clears every badge")
}

func TestRenderCard_RailShowsTheJobBadge(t *testing.T) {
	d := CardData{Title: "fix-login", Index: 1, Status: session.Running, JobBadge: "⚙ job 14 +1"}
	out := RenderCard(d, DensityRail, 40)
	first := railFirstLine(d, 40)
	assert.Contains(t, first, "1. fix-login")
	assert.Contains(t, first, "⚙ job 14 +1")
	for _, line := range strings.Split(out, "\n") {
		assert.LessOrEqual(t, ansi.StringWidth(line), 40)
	}

	d.JobBadge = ""
	assert.NotContains(t, railFirstLine(d, 40), "⚙", "no badge, no token")
}

// TestRenderCard_JobBadgeDecisionIsWidthOnly pins that, at a given card
// width, every badge text within the cap makes the same call: the decision
// reserves railJobBadgeMaxWidth, never the badge's own width.
func TestRenderCard_JobBadgeDecisionIsWidthOnly(t *testing.T) {
	badges := []string{"⚙ queued", "⚙ job 14", "⚙ job 14 +12"}
	wantShown := map[int]bool{20: false, 32: false, 33: true, 40: true}
	for w, want := range wantShown {
		for _, b := range badges {
			d := CardData{Title: "fix-login", Index: 1, Status: session.Running, JobBadge: b}
			first := railFirstLine(d, w)
			assert.Equal(t, want, strings.Contains(first, "⚙"), "width %d, badge %q", w, b)
		}
	}
}

// TestRenderCard_LongJobBadgeIsDroppedWhole pins the cap: a badge wider than
// railJobBadgeMaxWidth is dropped, never cut (a cut "+9" could read as "+").
func TestRenderCard_LongJobBadgeIsDroppedWhole(t *testing.T) {
	d := CardData{Title: "fix-login", Index: 1, Status: session.Running, JobBadge: "⚙ job 123456789012 +9"}
	first := railFirstLine(d, 60)
	assert.Contains(t, first, "1. fix-login")
	assert.NotContains(t, first, "⚙")
}

// TestRenderCard_JobBadgeTakesRoomFromTheAccountBadge pins the order of
// precedence when both want the title line's right edge: the job badge
// stays and the account badge goes first, and the account still shows on
// the same card at the same width once the job ends.
func TestRenderCard_JobBadgeTakesRoomFromTheAccountBadge(t *testing.T) {
	d := CardData{Title: "fix-login", Index: 1, Status: session.Running, Account: "max-2", JobBadge: "⚙ job 14"}

	tight := railFirstLine(d, 40)
	assert.Contains(t, tight, "⚙ job 14")
	assert.NotContains(t, tight, "@max-2", "no room for both at 40")

	roomy := railFirstLine(d, 44)
	assert.Contains(t, roomy, "⚙ job 14")
	assert.Contains(t, roomy, "@max-2")
	assert.Less(t, strings.Index(roomy, "⚙"), strings.Index(roomy, "@"), "the job badge sits left of the account's")

	d.JobBadge = ""
	assert.Contains(t, railFirstLine(d, 40), "@max-2", "without a job the account shows at 40")
}

func TestRenderCard_SelectedRailJobBadgeStaysInsideTheCard(t *testing.T) {
	d := CardData{Title: "fix-login", Index: 1, Status: session.Running, Selected: true,
		Account: "max-2", JobBadge: "⚙ job 14 +2"}
	for _, w := range []int{33, 44, 60} {
		for _, line := range strings.Split(RenderCard(d, DensityRail, w), "\n") {
			assert.LessOrEqual(t, ansi.StringWidth(line), w, "width %d", w)
		}
	}
}

func TestRenderOverviewCard_ShowsTheJobBadge(t *testing.T) {
	d := CardData{Title: "fix-login", Branch: "aidanb/fix", Status: session.Running, JobBadge: "⚙ queued"}
	assert.Contains(t, ansi.Strip(renderOverviewCard(d, 50)), "⚙ queued")

	d.JobBadge = ""
	assert.NotContains(t, ansi.Strip(renderOverviewCard(d, 50)), "⚙")
}

// TestRenderOverviewCard_JobBadgeOutlastsTheAccountOnTheMetaLine pins the
// ordering: the job badge is the meta line's first token, so the right-hand
// truncation drops the account (the last) before it.
func TestRenderOverviewCard_JobBadgeOutlastsTheAccountOnTheMetaLine(t *testing.T) {
	d := CardData{
		Title: "fix-login", Branch: "aidanb/fix-login-flow", Status: session.Running,
		Account: "max-2", JobBadge: "⚙ job 14",
		HasParity: true, Ahead: 2, Behind: 1,
		HasDiff: true, DiffAdded: 3, DiffRemoved: 1,
	}
	wide := ansi.Strip(renderOverviewCard(d, 40))
	assert.Contains(t, wide, "⚙ job 14")
	assert.Contains(t, wide, "@max-2")

	narrow := ansi.Strip(renderOverviewCard(d, 20))
	assert.Contains(t, narrow, "⚙ job 14", "the badge survives the cut")
	assert.NotContains(t, narrow, "@max-2", "the account is cut first")
}

func TestOverview_JobBadgeKeepsCardHeight(t *testing.T) {
	for _, width := range []int{18, 40, 60} {
		for _, badge := range []string{"⚙ queued", "⚙ job 14 +2", "⚙ job 123456789012 +99"} {
			d := CardData{Title: strings.Repeat("long-title-", 6), Status: session.Running,
				Branch: "aidanb/branch", Account: "max-2", JobBadge: badge,
				HasParity: true, Ahead: 100, Behind: 2000,
				HasDiff: true, DiffAdded: 1234, DiffRemoved: 5678}
			card := renderOverviewCard(d, width)
			assert.Equal(t, overviewCardHeight, len(strings.Split(card, "\n")),
				"width %d, badge %q", width, badge)
		}
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `CGO_ENABLED=0 go test ./ui/ -run 'JobBadge'`

Expected: the package does not build, so every test fails:

```
# github.com/aidan-bailey/loom/ui [github.com/aidan-bailey/loom/ui.test]
ui/card_test.go:NNN:2: undefined: SetJobBadges
ui/card_test.go:NNN:NN: unknown field JobBadge in struct literal of type CardData
...
FAIL	github.com/aidan-bailey/loom/ui [build failed]
```

- [ ] **Step 3: Implement**

In `ui/card.go`, add the cap beside the account badge's: insert this block right after the line `const railIndexPrefixReserve = 4` (the end of the constant block), with a blank line between:

```go
// railJobBadgeMaxWidth caps the title line's job badge ("⚙ job 14 +2"). Like
// railAccountBadgeMaxWidth it is what the show/hide decision reserves, never
// the badge's actual width, so every card on the rail decides the same way at
// a given width. A badge wider than the cap (a job ID past five digits with
// a "+N") is dropped whole, not cut: a cut "+9" could read as "+".
const railJobBadgeMaxWidth = 14
```

Still in `ui/card.go`, add the badge map: insert this block right after the line `func ShowAccounts() bool { return showAccounts }`, with a blank line between:

```go
// jobBadges maps a session to its work-queue badge text ("⚙ job 14",
// "⚙ job 14 +2", "⚙ queued"). app sets it (SetJobBadges) whenever the queue
// changes; a session the map does not name has no job. Main goroutine only.
var jobBadges map[core.InstanceID]string

// SetJobBadges replaces every session's job badge. A nil map clears them.
func SetJobBadges(b map[core.InstanceID]string) { jobBadges = b }

// JobBadge is session id's badge text, "" for none.
func JobBadge(id core.InstanceID) string { return jobBadges[id] }

// jobToken is the rail's dim job badge, "" without one or when the text is
// wider than railJobBadgeMaxWidth.
func jobToken(d CardData, solidBg bool) string {
	if d.JobBadge == "" || lipgloss.Width(d.JobBadge) > railJobBadgeMaxWidth {
		return ""
	}
	st := lipgloss.NewStyle().Foreground(Dim)
	if solidBg {
		st = st.Background(Panel)
	}
	return st.Render(d.JobBadge)
}
```

`CardData` gains the field. Quote:

```go
	// Account is the Claude account badge ("max-2", "default"); empty when
	// badges are off (no extra account) or the session isn't Claude.
	Account string
}
```

Replace it with:

```go
	// Account is the Claude account badge ("max-2", "default"); empty when
	// badges are off (no extra account) or the session isn't Claude.
	Account string
	// JobBadge is the work-queue badge ("⚙ job 14 +2", "⚙ queued"); empty
	// when the session has no queued or running job. See SetJobBadges.
	JobBadge string
}
```

`BuildCardData` fills it. Quote:

```go
		WaitReason:          sanitizeCardText(v.WaitReason),
		Account:             accountLabel(&v),
	}
```

Replace it with:

```go
		WaitReason:          sanitizeCardText(v.WaitReason),
		Account:             accountLabel(&v),
		JobBadge:            JobBadge(v.ID),
	}
```

`RenderCard` composes the title line's right edge. Quote (from `acct :=` to the end of `composeTitle`):

```go
	acct := accountToken(d, solidBg)
	if acct != "" && inner-railIndexPrefixReserve-railAccountBadgeMaxWidth-1 < railAccountBadgeMinTitle {
		acct = ""
	}
	titleW := inner
	if acct != "" {
		titleW = inner - lipgloss.Width(acct) - 1
	}
	title := truncate(prefix+d.Title, titleW)
	composeTitle := func(st lipgloss.Style) string {
		if acct == "" {
			return bar + sep + st.Render(title)
		}
		return bar + sep + spreadLineBg(st.Render(title), acct, inner, solidBg)
	}
```

Replace it with:

```go
	// The job badge rides the same edge, left of the account badge, under
	// the same rule: its decision reserves railJobBadgeMaxWidth. A shown job
	// badge takes its cap and a space from the account badge's room, so the
	// account goes first when both cannot fit.
	job := jobToken(d, solidBg)
	if job != "" && inner-railIndexPrefixReserve-railJobBadgeMaxWidth-1 < railAccountBadgeMinTitle {
		job = ""
	}
	jobReserve := 0
	if job != "" {
		jobReserve = railJobBadgeMaxWidth + 1
	}
	acct := accountToken(d, solidBg)
	if acct != "" && inner-railIndexPrefixReserve-jobReserve-railAccountBadgeMaxWidth-1 < railAccountBadgeMinTitle {
		acct = ""
	}
	right := acct
	if job != "" {
		right = job
		if acct != "" {
			right = job + sep + acct
		}
	}
	titleW := inner
	if right != "" {
		titleW = inner - lipgloss.Width(right) - 1
	}
	title := truncate(prefix+d.Title, titleW)
	composeTitle := func(st lipgloss.Style) string {
		if right == "" {
			return bar + sep + st.Render(title)
		}
		return bar + sep + spreadLineBg(st.Render(title), right, inner, solidBg)
	}
```

In `ui/overview.go`, `renderOverviewCard` puts the badge first on the meta line. Quote:

```go
	dim := lipgloss.NewStyle().Foreground(Dim)
	var right []string
	if p := parityToken(d); p != "" {
```

Replace it with:

```go
	dim := lipgloss.NewStyle().Foreground(Dim)
	var right []string
	// First: ansi.Truncate below trims the meta line from the right, so the
	// job badge outlasts every other token.
	if d.JobBadge != "" {
		right = append(right, dim.Render(d.JobBadge))
	}
	if p := parityToken(d); p != "" {
```

- [ ] **Step 4: Run it to see it pass**

Run: `CGO_ENABLED=0 go test ./ui/`

Expected: `ok  	github.com/aidan-bailey/loom/ui`. The account-badge tests (`TestRenderCard_AccountBadgeDecisionIsWidthOnly` and the rest of `ui/card_account_test.go`) pass unchanged: with no job badge `jobReserve` is 0 and `right` is `acct`. Also run `gofmt -l ui/` (no output).

- [ ] **Step 5: Commit**

```
git commit -m "feat(ui): job badges on the rail and overview cards" -- ui/card.go ui/overview.go ui/card_test.go
```

### Task 7.2: Computing the badges from the queue

**Files:**
- Create: `app/work_badge.go`
- Test: `app/work_badge_test.go`
- Modify: `app/core_glue.go` (the `core.QueueChanged` case of `applyCoreEvent`, which Story 4 adds)

`ownerJobBadges` turns a `core.QueueView` into the map Task 7.1 reads. `home.syncJobBadges` publishes it, and the `QueueChanged` arm calls that, so the badges follow every queue change. A job with no owner has no card to badge, and a finished job counts for nothing.

- [ ] **Step 1: Write the failing test**

Create `app/work_badge_test.go`:

```go
package app

import (
	"testing"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/ui"
	"github.com/stretchr/testify/assert"
)

func TestOwnerJobBadges(t *testing.T) {
	q := core.QueueView{Jobs: []core.JobView{
		// Owner 1: three running (listed out of order) and a queued one. The
		// lowest running ID names the badge, and the others count in "+N".
		{ID: 16, State: sched.Running, Owner: 1},
		{ID: 14, State: sched.Running, Owner: 1},
		{ID: 15, State: sched.Running, Owner: 1},
		{ID: 17, State: sched.Queued, Owner: 1},
		// Owner 2: queued only.
		{ID: 18, State: sched.Queued, Owner: 2},
		{ID: 19, State: sched.Queued, Owner: 2},
		// Owner 3: one running job.
		{ID: 20, State: sched.Running, Owner: 3},
		// Owner 4: only finished jobs of every kind: no badge.
		{ID: 21, State: sched.Done, Owner: 4},
		{ID: 22, State: sched.Failed, Owner: 4},
		{ID: 23, State: sched.Cancelled, Owner: 4},
		{ID: 24, State: sched.Lost, Owner: 4},
		// No owner: no card to badge.
		{ID: 25, State: sched.Running, Owner: 0},
		{ID: 26, State: sched.Queued, Owner: 0},
	}}
	assert.Equal(t, map[core.InstanceID]string{
		1: "⚙ job 14 +2",
		2: "⚙ queued",
		3: "⚙ job 20",
	}, ownerJobBadges(q))
}

func TestOwnerJobBadges_EmptyQueue(t *testing.T) {
	assert.Empty(t, ownerJobBadges(core.QueueView{}))
}

// TestQueueChanged_PublishesTheJobBadges runs the TUI's applier, not the
// helper: the event moves the badges to ui, and the next event, with the job
// done, takes them away again.
func TestQueueChanged_PublishesTheJobBadges(t *testing.T) {
	m := newTestHome(t)
	t.Cleanup(func() { ui.SetJobBadges(nil) })

	_ = m.applyCoreEvent(core.QueueChanged{View: core.QueueView{Jobs: []core.JobView{
		{ID: 14, State: sched.Running, Owner: 9},
		{ID: 15, State: sched.Queued, Owner: 10},
	}}})
	assert.Equal(t, "⚙ job 14", ui.JobBadge(9))
	assert.Equal(t, "⚙ queued", ui.JobBadge(10))
	assert.Equal(t, "", ui.JobBadge(11), "a session with no job has no badge")

	_ = m.applyCoreEvent(core.QueueChanged{View: core.QueueView{Jobs: []core.JobView{
		{ID: 14, State: sched.Done, Owner: 9},
		{ID: 15, State: sched.Running, Owner: 10},
	}}})
	assert.Equal(t, "", ui.JobBadge(9), "a finished job's badge is gone")
	assert.Equal(t, "⚙ job 15", ui.JobBadge(10), "a started job's badge changes")
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `CGO_ENABLED=0 go test ./app/ -run 'OwnerJobBadges|QueueChanged_PublishesTheJobBadges'`

Expected: the package does not build:

```
# github.com/aidan-bailey/loom/app [github.com/aidan-bailey/loom/app.test]
app/work_badge_test.go:NN:NN: undefined: ownerJobBadges
FAIL	github.com/aidan-bailey/loom/app [build failed]
```

- [ ] **Step 3: Implement**

Create `app/work_badge.go`:

```go
package app

import (
	"fmt"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/ui"
)

// ownerJobs is what one session's jobs add up to for its badge.
type ownerJobs struct {
	lowest  sched.JobID // the lowest running ID; meaningful when running > 0
	running int
	queued  bool
}

// ownerJobBadges is every session's work-queue badge, keyed by the owner's
// InstanceID: "⚙ job 14" for its lowest running job, with " +N" for N more
// running, or "⚙ queued" when it has only queued ones. A session with
// neither, a finished job, and a job with no owner get no entry. It reads
// the view only, so it is safe wherever the view is.
func ownerJobBadges(q core.QueueView) map[core.InstanceID]string {
	by := map[core.InstanceID]*ownerJobs{}
	for _, j := range q.Jobs {
		if j.Owner == 0 {
			continue
		}
		o := by[j.Owner]
		if o == nil {
			o = &ownerJobs{}
			by[j.Owner] = o
		}
		switch j.State {
		case sched.Running:
			if o.running == 0 || j.ID < o.lowest {
				o.lowest = j.ID
			}
			o.running++
		case sched.Queued:
			o.queued = true
		}
	}
	out := make(map[core.InstanceID]string, len(by))
	for id, o := range by {
		switch {
		case o.running > 0:
			label := fmt.Sprintf("⚙ job %d", o.lowest)
			if o.running > 1 {
				label += fmt.Sprintf(" +%d", o.running-1)
			}
			out[id] = label
		case o.queued:
			out[id] = "⚙ queued"
		}
	}
	return out
}

// syncJobBadges publishes the badges of home.queue to the cards. Call it
// wherever home.queue changes.
func (m *home) syncJobBadges() {
	ui.SetJobBadges(ownerJobBadges(m.queue))
}
```

In `app/core_glue.go`, find the `case core.QueueChanged:` arm of `applyCoreEvent` (Story 4 adds it; it assigns `m.queue = ev.View`). Add `m.syncJobBadges()` as the statement right after that assignment, so the arm reads:

```go
	case core.QueueChanged:
		m.queue = ev.View
		m.syncJobBadges()
```

If Story 4's arm holds more statements, keep them; the call goes directly after the assignment and before any `return`.

- [ ] **Step 4: Run it to see it pass**

Run: `CGO_ENABLED=0 go test ./app/ -run 'OwnerJobBadges|QueueChanged_PublishesTheJobBadges'`

Expected: `ok  	github.com/aidan-bailey/loom/app`. Then run `CGO_ENABLED=0 go test ./app/... ./ui/...` for the whole packages: `ok` for each.

- [ ] **Step 5: Commit**

```
git add -N app/work_badge.go app/work_badge_test.go
git commit -m "feat(app): badge a session's cards from the work queue" -- app/work_badge.go app/work_badge_test.go app/core_glue.go
```

### Task 7.3: USAGE.md

**Files:**
- Modify: `USAGE.md` (the view-mode list, Keyboard Reference, the systemd unit, the workflows, the CLI reference, the configuration)

No test covers `USAGE.md`; the check is a grep for each new heading and key row.

- [ ] **Step 1: Write the failing test**

Run:

```
for p in '^### Work View' '^### The Work Queue' '^### Job Subcommands' '^### Scheduler Caps' 'KillMode=process' '^\| `w` \|' '^\| `job` \|' '^\| `scheduler` \|'; do grep -qE -- "$p" USAGE.md && echo "ok   $p" || echo "MISSING $p"; done
```

- [ ] **Step 2: Run it to see it fail**

Expected: all eight lines print `MISSING …`.

- [ ] **Step 3: Implement**

Make these edits in `USAGE.md`.

**3a. Default State table.** Quote:

```
| `W` | Open workspace picker |
```

Replace it with:

```
| `W` | Open workspace picker |
| `w` | Open the work view: the host's job queue (see [The Work Queue](#the-work-queue)) |
```

**3b. Overview table.** Quote:

```
| `Tab` | Return to focus mode |
```

Replace it with:

```
| `Tab` | Return to focus mode |
| `w` | Open the work view; `w` or `Esc` there returns to the overview |
```

**3c. A Work View keyboard section.** Quote the heading it goes before:

```
### Name Entry Mode (after pressing `n` or `N`)
```

Replace it with (the new section, a blank line, then the same heading):

````markdown
### Work View (after pressing `w`)

The work view covers the whole host, across every workspace. Mouse input is dropped. Offline (daemon away), it shows the last state it had and its actions are refused like every other model key.

| Key | Action |
|-----|--------|
| `↑` / `k`, `↓` / `j` | Move the selection |
| `Enter` | Open the selected job's whole log in a scrolling pager (`Esc` back) |
| `x` | Cancel the selected job, after a confirmation |
| `t` | Move a queued job to the front of the queue |
| `o` | Focus the job's owner session in focus mode, opening its workspace's tab if it is not open |
| `e` | Open the caps form: the cores, memory and I/O caps, the default claim, the starvation wait and how many finished jobs to keep. It shows each value in effect, whether it is the host's default, and the host's detected cores and RAM; saving applies it at once, and a refused value is shown in the form |
| `w` / `Esc` | Return to the mode the view came from |

### Name Entry Mode (after pressing `n` or `N`)
````

**3d. The systemd unit.** Quote:

```
Restart=on-failure
RestartSec=5
```

Replace it with:

```
Restart=on-failure
RestartSec=5
# Leave the jobs the work queue runs alone when the unit stops or restarts:
# each runs in a session of its own and outlives the daemon.
KillMode=process
```

Quote the paragraph it goes after:

```
After installing a new loom build, restart the unit too. A `loom` you start by
hand that finds an older daemon replaces it with a detached daemon of its own,
outside the unit.
```

Replace it with (that paragraph, then):

```
After installing a new loom build, restart the unit too. A `loom` you start by
hand that finds an older daemon replaces it with a detached daemon of its own,
outside the unit.

**`KillMode=process` is what lets jobs survive a restart.** The work queue's
running jobs belong to the unit's cgroup. Under systemd's default
(`control-group`), `systemctl --user restart loom` kills every one of them with
the daemon; with `KillMode=process` only the daemon's own process is stopped,
and the restarted daemon adopts the jobs that are still running (see
[The Work Queue](#the-work-queue)).
```

**3e. A workflow section.** Quote the heading it goes before:

```
### Session Recovery (orphaned worktrees)
```

Replace it with (the new section, a blank line, then the same heading):

````markdown
### The Work Queue

Builds, test suites and benchmarks from several sessions at once can starve a host. The daemon keeps a per-host queue: a session (or you) submits a command with `loom job`, the daemon starts it when its claim fits what the host has left, runs it detached from the daemon, and tells the submitting session's Claude when it ends. Loom's Claude context tells agents to use it. Nothing rewrites an agent's commands, so work not submitted through `loom job` competes as before. The scheduler is not available on Windows.

```
loom job run -d "tests" -- cargo test       # queue it, follow it, exit with its exit code
loom job submit -d "tests" -- cargo test    # queue it and return; a message arrives when it ends
loom job wait 14                            # follow a job again
loom job logs 14 -f                         # its output, following
loom job list --all                         # running, queued and the finished ones kept
loom job cancel 14
```

- **No shell.** The command runs as given, in the folder you ran `loom job` from, with your environment. Write a pipeline as `-- sh -c '…'`.
- **Claims.** A job claims `--cpus N` cores and `--mem N` GB (each defaults to the default claim), plus `--io` if it is I/O-heavy, or `--exclusive` for the whole host (it cannot combine with the others). It starts when its claim fits within the caps beside the running jobs' claims; otherwise `loom job list` and the work view say why it waits ("needs 8 cores (4 free)"). The claim is only an estimate used for admission: nothing stops a job from using more.
- **Order.** First fit, so a small job can pass a big one that waits for room, until the big one has waited past `starve_after_minutes` ([Scheduler Caps](#scheduler-caps)); then nothing else starts before it.
- **Parallelism.** The CPU claim is the job's parallelism: loom sets `CARGO_BUILD_JOBS`, `RUST_TEST_THREADS`, `MAKEFLAGS=-jN`, `NIX_BUILD_CORES`, `CMAKE_BUILD_PARALLEL_LEVEL` and `GOFLAGS=-p=N` unless your environment already sets them, and `LOOM_JOB=<id>` always.
- **Owner and messages.** A job submitted from a loom session belongs to it (loom reads `$LOOM_LAUNCH`); one from a plain shell has no owner. `submit` messages the owner's Claude when the job ends, through the loom mod: `[loom] job 14 "tests" finished: exit 0 after 3m10s`, the log's path and its last lines (`--tail N`, default 20; `--no-notify` sends none). `run` sends none while it follows, but if it is interrupted (a Bash timeout, say) it asks for one and leaves the job running. A message the session cannot take (no live mod, or the session is not running) is never typed into its pane: the work view marks the job and says why.
- **Following.** `run` and `wait` print the log on stdout and status lines (`loom: job 14 started after 1m42s`) on stderr, and exit with the job's exit code, `128+N` for a job killed by signal N, or `125` for one that was lost, could not start, or was cancelled.
- **Cancelling.** A queued job is cancelled at once; a running job gets SIGTERM to its whole process group, and SIGKILL if it is still there 10 s later. Pausing or killing a session cancels its jobs first, with no message.
- **Daemon restarts.** Running jobs live in sessions of their own, so stopping or upgrading the daemon leaves them running, and the next daemon adopts and settles them before it admits anything new. Under systemd this needs `KillMode=process` in the unit ([Keep the daemon up](#keep-the-daemon-up-a-systemd-user-unit)). A job whose runner vanished without a result (a reboot) is marked `lost`.
- **The work view.** `w` shows the host's caps, what the running jobs claim and use, the running, queued and recent jobs, and the selected job's log tail (keys: [Work View](#work-view-after-pressing-w)). A session's card in the rail and the overview shows `⚙ job 14` (with `+N` for more running) or `⚙ queued`.
- **Files.** Everything is under `~/.loom/jobs/`: `jobs.json` and, per job, `<id>/output.log` (no size cap; `keep_finished` bounds how many finished jobs are kept). A `jobs.json` the daemon cannot read is left untouched and the queue refuses submissions, saying why.

### Session Recovery (orphaned worktrees)
````

**3f. The commands table.** Quote:

```
| `sweep` | Run the build sweep now:
```

Find that row (it begins so; it is one line) and add this row right after it:

```
| `job` | Submit, follow, list and cancel jobs in the host's work queue (see below, and [The Work Queue](#the-work-queue)); a client of the daemon too |
```

**3g. A subcommand table.** Quote the heading it goes before:

```
### Web Subcommands
```

Replace it with (the new section, a blank line, then the same heading):

````markdown
### Job Subcommands

A job's command follows `--`. The claim flags are `--cpus <n>`, `--mem <gb>`, `--io` and `--exclusive`; `-d`/`--desc` names the job (default: the command line, cut to 60 characters).

| Command | Description |
|---------|-------------|
| `job submit [-d <desc>] [claim flags] [--tail <n>] [--no-notify] -- <cmd…>` | Queue the command and return: `job 14 queued (3rd in line, 2 running)` or `job 14 started`. A refusal (a claim over a cap, admission paused, a bound exceeded) is printed with exit 1. The owner's Claude is messaged when the job ends; `--tail` (0–200, default 20) sets how many log lines the message carries |
| `job run [-d <desc>] [claim flags] -- <cmd…>` | Queue the command and follow it: the log on stdout, status lines on stderr, the job's exit code (see [The Work Queue](#the-work-queue)) |
| `job wait <id>` | Follow an existing job the same way |
| `job logs <id> [--tail <n>] [-f]` | Print the job's output, all of it or the last `n` lines; `-f` follows until the job ends |
| `job list [--all]` | The running and queued jobs, with why each waits; `--all` adds the finished ones kept |
| `job cancel <id>` | Cancel a queued or running job; refused for a finished one |

### Web Subcommands
````

**3h. The options table.** Quote the row:

```
| `build_sweep_after_hours` | int | `0` |
```

Find that row (one line) and add this row right after it:

```
| `scheduler` | object | host defaults | The work queue's caps and default claim (global `config.json` only). See [Scheduler Caps](#scheduler-caps) |
```

**3i. The configuration section.** Quote the heading it goes before:

```
### Claude Fullscreen Renderer
```

Replace it with (the new section, a blank line, then the same heading):

````markdown
### Scheduler Caps

The work queue admits jobs against caps on the host's cores, memory and I/O-heavy jobs. They live in a `scheduler` block of the global `~/.loom/config.json` (per host; a workspace's `config.json` never holds them, and the `S` settings overlay does not show them). Edit them in the work view (`w`, then `e`) or by hand; the daemon rereads the block at every admission pass, so a hand edit applies without a restart.

```json
{
  "scheduler": {
    "cpus": 14,
    "memory_gb": 24,
    "io_jobs": 2,
    "default_claim": { "cpus": 4, "memory_gb": 6 },
    "starve_after_minutes": 10,
    "keep_finished": 50
  }
}
```

An absent or `0` field takes the host's default:

| Field | Default | Bounds |
|-------|---------|--------|
| `cpus` | cores − 2 (at least 1) | 1–1024 |
| `memory_gb` | ¾ of the RAM in GB (at least 1; a GB is 2^30 bytes) | 1–65536 |
| `io_jobs` | `2` | 1–64 |
| `default_claim.cpus` | the lesser of `4` and `cpus` | 1 to `cpus` |
| `default_claim.memory_gb` | the lesser of `6` and `memory_gb` | 1 to `memory_gb` |
| `starve_after_minutes` | `10` | 1–1440 |
| `keep_finished` | `50` | 1–10000 |

A value outside its bounds, or a default claim larger than a cap, makes the block invalid: admission pauses, running jobs are left alone, `loom job submit` is refused, and the work view says why. No cap is guessed. If the RAM cannot be read, `memory_gb` has no default and must be set. Lowering a cap never touches running jobs; it changes only what starts next.

### Claude Fullscreen Renderer
````

**3j. The view-mode list (`USAGE.md:69`).** Quote:

```
Loom has three view modes:
```

Replace it with:

```
Loom has four view modes:
```

Then quote the list's last bullet:

```
- **Session workbench** — a single-session deep-dive: the agent pane on the left, a tabbed panel (markdown / diff / files / terminal / review) on the right. Press `enter` in focus mode to open it. See [Session Workbench](#session-workbench).
```

Replace it with (that bullet, then):

```
- **Session workbench** — a single-session deep-dive: the agent pane on the left, a tabbed panel (markdown / diff / files / terminal / review) on the right. Press `enter` in focus mode to open it. See [Session Workbench](#session-workbench).
- **Work view** — the host's job queue, across every workspace. Press `w` in focus or overview mode to open it; `w` or `Esc` returns to the mode it came from. It is not persisted: a restarted TUI opens in focus or overview as before. See [The Work Queue](#the-work-queue).
```

Leave the persistence line (the one that says focus and overview are what a restart restores) as it is.

- [ ] **Step 4: Run it to see it pass**

Rerun Step 1's command. Expected: eight lines, each `ok   …`.

- [ ] **Step 5: Commit**

```
git commit -m "docs: loom job, the work view and the scheduler block in USAGE.md" -- USAGE.md
```

### Task 7.4: The guide, its index entry and the pointer to it

**Files:**
- Create: `docs/claude/the-work-queue.md`
- Modify: `docs/claude/INDEX.md` (append a row)
- Modify: `sched/CLAUDE.md` (append a pointer to its `## Pointers` section)

The claudemd lint requires every guide to be listed in `INDEX.md` and linked from a `CLAUDE.md`. `sched/CLAUDE.md` is the file with room (the plan's Execution section: `core/CLAUDE.md`, `app/CLAUDE.md` and the root file are full).

- [ ] **Step 1: Write the failing test**

Add the index row and the pointer first, with no guide behind them. Append to `docs/claude/INDEX.md`, after the `web-layer.md` row (the file's last line):

```
| [`the-work-queue.md`](the-work-queue.md) | Changing the work queue's model side (`core/queue*.go`, `gateQueue`, boot adoption, notices) or `sched`'s runtime — settle through the lock before anything else, signal a group only after the lock test (or the lost-job rule), remove a job's files only through `RemoveJobDir`, leave an unreadable `jobs.json` alone, and test with the shim re-exec (`sched.MaybeRunTestShim()`, `sched.TestRunner()`); traps: a `TestMain` without the shim hook runs the whole suite as the "shim", a stale `ps` sample, a reused pid, a start whose save failed leaving a running shim behind a queued record, `KillMode=control-group` ending every job with the daemon |
```

Append to `sched/CLAUDE.md`, as the last bullet of its `## Pointers` section (the last section of the file; add it after the existing bullets, keeping their format):

```
- [`../docs/claude/the-work-queue.md`](../docs/claude/the-work-queue.md) — the model's side of the queue: boot adoption and settling, the lock-before-signal rule, retention, notices and the shim re-exec the tests use.
```

- [ ] **Step 2: Run it to see it fail**

Run: `CGO_ENABLED=0 go run ./tools/claudemd`

Expected, exit 1:

```
docs/claude/INDEX.md:28: broken link "the-work-queue.md"
sched/CLAUDE.md:NN: broken link "../docs/claude/the-work-queue.md"
```

- [ ] **Step 3: Implement**

Create `docs/claude/the-work-queue.md`. Every backticked path below resolves once Stories 1 to 6 are in; it names no test, because the lint requires a named test to exist.

````markdown
# The work queue (the model's side of `loom job`)

**Read this when** you change `core/queue*.go`, `gateQueue`, `adoptJobs`, a job's notice, or `sched`'s runtime (the shim, the lock probe, group signals, the usage sampler).

**What it is.** The daemon's model owns a per-host queue of jobs that sessions submit with `loom job`. It admits each against the caps in the global `config.json`'s `scheduler` block, starts it as a detached `loom job-shim` (a child in a session of its own, so it outlives the daemon), watches it through the shim's lock, and tells the owner's Claude when it ends. The pure half (types, caps, first-fit admission, the `jobs.json` store, the per-job file codecs) and the process half (the shim, the lock probe, group signals, the sampler) are the `sched` package; the queue's state and decisions are `core/queue.go` (state, load and save, boot adoption, retention), `core/queue_admit.go` (admission passes, starting), `core/queue_watch.go` (the gated job's lock tests, sampling, settling, a cancel's escalation), `core/queue_ops.go` (the operations the requests wrap) and `core/queue_notice.go` (notices). The gate is `gateQueue` in `core/gate.go`, 3 s; [`adding-a-gated-job.md`](adding-a-gated-job.md) covers the gate's own traps.

## Rules

1. **Test the lock before you signal a group.** A job's process group is its shim's pid. Send a signal (`sched.SignalGroup`) only to a group whose `sched.Probe` shows `run.lock` held, which proves the pid is still the shim. The exceptions are a lost job (the shim is gone and left no `exit.json`) and the shim of a failed start (`queueState.silent`) once it is gone: the group gets SIGKILL only when the latest `ps` sample shows processes in the group and none with the shim's pid. A live process with that pid and no lock is a reused pid, and its group is never signalled. *Silent:* a signal to an unrelated process group (your shell, an editor, another job). Convention only; a test that proves a signal is withheld needs a reused-pid fixture.
2. **Settle before you admit, at boot.** `Model.Boot` loads `jobs.json` (`loadQueue`), whose last steps are `adoptJobs` and then the first `admitJobs`. `adoptJobs` runs one lock pass over every running job (and every job the record calls queued, since a start whose save failed leaves a running shim behind a queued record), before the loop starts. `Model.Begin` then calls `maybeQueue`, which hands the adopted running jobs to `gateQueue`'s watch. The caps count the jobs adopted, and nothing runs twice: a held lock means running, an `exit.json` means settle from it, neither means `lost`. *Silent:* an admission pass before settling starts jobs on top of ones already running.
3. **The queue is touched only on the loop.** `queueState` is read and written by the model's goroutine; a job (the start, the sample) gets a snapshot (ids, paths, pids) and returns one result that `Deliver` applies. A result lands after the job it names may have been cancelled or removed: look the record up again before acting. *Silent:* a race that only `CC=clang CGO_ENABLED=1 go test -race` finds, and only when a test overlaps it.
4. **Remove a job's files only through `sched.RemoveJobDir`.** It removes `JobDir(dir, id)`, built from the ID; never a path read from `jobs.json`, `spec.json` or the wire. Retention drops finished jobs beyond `keep_finished`, oldest end first. *Silent:* a recursive delete of whatever a corrupt file names.
5. **Leave a `jobs.json` that fails to load alone.** An unreadable, invalid or newer-version file (`sched.ErrNewerStore`) sets `queueState.loadErr`: submissions are refused with the reason, admission pauses, a notice is raised, and the running shims carry on. Never write over it, and never start from an empty queue. Write the store only through `sched.SaveStore`. *Silent:* an overwritten file loses every queued job and the ID counter, and IDs are reused.
6. **A write that fails is not a lost change.** A request that cannot write `spec.json` or `jobs.json` is refused with the error and changes nothing. A change the model made on its own (a start, a settle, a usage peak) stays in memory, raises a notice and is written with the next save.
7. **Never paste a notice.** A job's end goes to the owner's Claude through the loom mod's outbox (`OriginJob`). With no live mod, or an owner that is not running, the notice is recorded on the job as undelivered, with the reason. A paste into the pane could answer an open dialog. Pausing or killing a session cancels its jobs first and sends no notice.
8. **Fail closed.** A cwd that is gone at start fails the job ("its folder is gone"); a shim that writes no `started.json` within 5 s fails the job, and its group is sent SIGKILL only once its lock proves the shim alive: until then `queueState.silent` keeps the pending kill, which `gateQueue` sends when a later lock test finds the shim alive and drops when no process has its pid any more, or after 10 minutes; a `ps` sample that fails or times out is skipped and the lock tests go on without it. Nothing is guessed.

## Testing with real shims

A test binary has no `job-shim` command, so a package whose tests start real shims (`sched`, `core`, `internal/cli`) re-executes its own test binary as the shim:

- Its `TestMain` calls `sched.MaybeRunTestShim()` as its first statement. When the environment variable `sched.ShimTestEnv` (`LOOM_SCHED_TEST_SHIM`) is `1`, the process runs `sched.RunShim` on its last argument and exits instead of running tests.
- The runner a test hands the model is `sched.TestRunner()` (the test binary, with that variable set), installed with `Model.SetJobRunnerForTest` in `core/seams.go`.
- Real jobs are short commands (`sh -c`, `sleep`), and every test leaves no process group behind: cancel or wait for each job it starts.
- Build fixtures through the production path: a booted model, a real shim, a `jobs.json` written with `sched.SaveStore`. A fake runner skips the lock, the `Setsid` and the `exit.json`, which is where the bugs are.

## Traps

- **A `TestMain` without the hook.** The shim child is the test binary with the variable set; without `MaybeRunTestShim` it runs the whole suite, whose tests start more shims. Add the hook to a new package's `TestMain` before its first real-shim test.
- **Environment.** The job's environment comes only from `spec.json`; the shim process itself gets the daemon's environment plus `Runner.Env`. Do not build a git or tmux command from a job's environment.
- **The sample is a hint, not proof.** Usage and the lost-job rule read one `ps -A` snapshot; procps and BSD format `time` differently and `ParsePS` reads both. Skip a failed sample, never reuse a stale one for the kill rule.
- **`KillMode`.** Under systemd's default `control-group`, restarting the unit kills every shim in its cgroup. `USAGE.md`'s unit sets `KillMode=process`; a report of "jobs die with the daemon" on a unit without it is not a model bug.
- **Windows.** The runtime's stubs return `sched.ErrUnsupported` and `SubmitJob` refuses with it. After touching `sched`'s platform files, run `GOOS=windows CGO_ENABLED=0 go build ./...` and the same for `darwin`.
- **A cap edit never touches running jobs.** It changes only what starts next; an `--exclusive` claim is stored as a flag, so it follows the caps it is admitted under.

## What the gates won't tell you

- Whether a new signal path tests the lock first (rule 1): no test fails when one forgets.
- Whether a new write of the queue's files goes through `sched.SaveStore` and the codecs.
- Whether a real-shim test leaves a process running after it ends.
````

- [ ] **Step 4: Run it to see it pass**

Run: `CGO_ENABLED=0 go test ./tools/claudemd/... && CGO_ENABLED=0 go run ./tools/claudemd -v`

Expected: the tests are `ok`; the lint prints nothing problematic (the `-v` run lists each `CLAUDE.md`'s headroom and exits 0), including no `broken link` or `not linked from any CLAUDE.md rule`. `sched/CLAUDE.md` stays under 100 lines and 20,000 bytes.

- [ ] **Step 5: Commit**

```
git add -N docs/claude/the-work-queue.md
git commit -m "docs: the work-queue guide, its index entry and a pointer from sched" -- docs/claude/the-work-queue.md docs/claude/INDEX.md sched/CLAUDE.md
```

### Task 7.5: ARCHITECTURE.md and the scripting reference

**Files:**
- Modify: `docs/ARCHITECTURE.md` (the packages table, the daemon's work with no TUI, the view modes, the persistent state, the events)
- Modify: `docs/specs/scripting.md` (the sync primitives table)

- [ ] **Step 1: Write the failing test**

Run:

```
grep -c 'sched/' docs/ARCHITECTURE.md; grep -c 'jobs/' docs/ARCHITECTURE.md; grep -c 'Work\*\*' docs/ARCHITECTURE.md; grep -c 'toggle_work_view' docs/specs/scripting.md; grep -c 'QueueChanged' docs/ARCHITECTURE.md; CGO_ENABLED=0 go run ./tools/claudemd
```

- [ ] **Step 2: Run it to see it fail**

Expected: the five counts print `0`; the lint is clean at this point (exit 0). The counts are the failing check.

- [ ] **Step 3: Implement**

**`docs/ARCHITECTURE.md`.**

The `config/` row, which the new row follows. Quote its start:

```
| `config/` | `config.json`, `state.json`, the workspace registry, dir resolution |
```

Add this row right after that row (the whole row is one line):

```
| `sched/` | The work queue's logic and runtime: claims, caps and first-fit admission, the `jobs.json` store and per-job files, the hidden `loom job-shim` that runs each job detached, the lock probe, group signals and the `ps` usage sampler | [`sched/README.md`](../sched/README.md), [`sched/CLAUDE.md`](../sched/CLAUDE.md) |
```

The `internal/cli/` row. Quote:

```
`loom workspace`, `loom account`, `loom session` and `loom web`, and the client half of `loom reset`
```

Replace with:

```
`loom workspace`, `loom account`, `loom session`, `loom web` and `loom job`, and the client half of `loom reset`
```

The daemon's work with no TUI. Quote the last sentence of that paragraph:

```
A workspace removed from the registry is unloaded in place (`Unregister`, or a registry reread) with its sessions left running, and one renamed keeps its ID.
```

Replace it with that sentence followed by:

```
 The work queue (`core/queue*.go`) runs with no TUI too. The model owns `<globalDir>/jobs/` and runs each admitted job as a detached `loom job-shim` in a session of its own, so a daemon stop or upgrade leaves running jobs running. `Model.Boot` loads `jobs.json` and settles what ran while the daemon was down through each job's `run.lock` before its first admission pass; `gateQueue` (every 3s while any job runs) tests the locks, samples usage with `ps` and settles ended jobs; pausing or killing a session cancels its jobs; and a job's end is sent to its owner's Claude through the loom mod's outbox as `OriginJob`, or recorded on the job as undelivered when there is no live mod. The guide is [`docs/claude/the-work-queue.md`](claude/the-work-queue.md).
```

The view modes. Quote:

```
The default state renders in one of three view modes (`m.viewMode`), orthogonal to `m.state`:
```

Replace with:

```
The default state renders in one of four view modes (`m.viewMode`), orthogonal to `m.state`:
```

Quote the start of the workbench bullet, and add a bullet right after the whole bullet (it is one line, ending `Never persisted: a restart lands in focus.`):

```
- **Workbench** (`enter` in focus):
```

New bullet after it:

```
- **Work** (`w` from focus or overview, `app/work.go`, `ui/work.go`): the host's job queue, read from `home.queue` (the replica's `core.QueueView`): capacity bars, the running, queued and recent jobs, and the selected job's log tail read from disk. `w` or `esc` returns to the mode it came from. Never persisted; mouse input is dropped.
```

The last sentence of the paragraph after the bullets ("Focus or overview persists per workspace…") stays as it is: work is not persisted.

Persistent state. Quote the `scripts/` row's start and add a row after the whole row:

```
| `scripts/` (global dir) | User Lua scripts, loaded at startup |
```

New row after it:

```
| `jobs/` (global dir) | The work queue: `jobs.json` (every job's record, versioned, written only by the model), and per job `<id>/` with `spec.json` (argv, folder, the submitter's environment), `output.log`, and the shim's `run.lock`, `started.json` and `exit.json`; directories 0700, files 0600; a `jobs.json` that fails to load is left untouched and admission pauses | [`sched/README.md`](../sched/README.md), [`docs/claude/the-work-queue.md`](claude/the-work-queue.md) |
```


The events. Quote the sentence naming the state events, in the **Events.** paragraph:

```
The state events carry the published state: `WorkspacesChanged`, `ModelChanged`, `AccountsChanged`, `GitHubChanged` and `ViewsChanged`.
```

Replace it with:

```
The state events carry the published state: `WorkspacesChanged`, `ModelChanged`, `AccountsChanged`, `GitHubChanged`, `QueueChanged` and `ViewsChanged`.
```

In the same paragraph, quote:

```
in the model's publish order (the workspaces, the model, account and GitHub views, then each changed workspace's views)
```

Replace it with:

```
in the model's publish order (the workspaces, the model, account, GitHub and queue views, then each changed workspace's views)
```

**`docs/specs/scripting.md`.** Quote:

```
| `cs.actions.toggle_overview()` | Switch between focus mode and the overview grid (persisted per workspace). |
```

Add this row right after it:

```
| `cs.actions.toggle_work_view()` | Open the work view (the host's job queue) from focus or overview, or return to the mode it came from. Not persisted. |
```

- [ ] **Step 4: Run it to see it pass**

Run:

```
grep -c 'sched/' docs/ARCHITECTURE.md; grep -c 'jobs/' docs/ARCHITECTURE.md; grep -c 'Work\*\*' docs/ARCHITECTURE.md; grep -c 'toggle_work_view' docs/specs/scripting.md; grep -c 'QueueChanged' docs/ARCHITECTURE.md; CGO_ENABLED=0 go run ./tools/claudemd
```

Expected: the five counts are at least `1` (`sched/` and `jobs/` several), and the lint prints nothing and exits 0 (every backticked path in the new text resolves: `core/queue*.go`, `app/work.go`, `ui/work.go`, `sched/README.md`, `sched/CLAUDE.md`, and the guide link `claude/the-work-queue.md`, which Task 7.4 created).


- [ ] **Step 5: Commit**

```
git commit -m "docs: the sched package, the work view and jobs/ in ARCHITECTURE.md; toggle_work_view in the scripting reference" -- docs/ARCHITECTURE.md docs/specs/scripting.md
```

### Task 7.6: The package READMEs, the root README and the event guide

**Files:**
- Modify: `core/README.md` (the file table, after the `build_sweep.go` row at line 27, and the `state_publish.go` row at line 33; the `SaveSettings` paragraph at line 98; the publish order at line 104 and the state events at lines 106-109)
- Modify: `app/README.md:10`, `app/README.md:84` and the end of its `## View modes` section
- Modify: `ui/README.md:10` and `ui/README.md:25`
- Modify: `docs/claude/adding-a-core-event.md:11`
- Modify: `README.md:16`

The READMEs are facts, checked only by the CLAUDE.md lint (`TestRepo_ClaudeMDStructure` in `tools/claudemd`: the links, paths and test names they name) and `go run ./tools/claudemd -idents` (the symbols). Every file and symbol below is created by Stories 3, 4, 6 and this one, so each resolves once those are in the tree.

- [ ] **Step 1: Write the failing test**

Run:

```
grep -c 'queue_view.go' core/README.md; grep -c 'work.go' app/README.md; grep -c 'work.go' ui/README.md; grep -c 'work_badge.go' app/README.md; grep -c 'QueueChanged' core/README.md app/README.md docs/claude/adding-a-core-event.md; grep -c 'loom job' README.md; CGO_ENABLED=0 go run ./tools/claudemd
```

- [ ] **Step 2: Run it to see it fail**

Expected: the first four counts print `0`; the three-file count prints `core/README.md:0`, `app/README.md:0` and `docs/claude/adding-a-core-event.md:0`; the `loom job` count prints `0`; the lint is clean at this point (exit 0). The counts are the failing check.

- [ ] **Step 3: Implement**

**`core/README.md`.** The file table's `build_sweep.go` row. Quote:

```
| `build_sweep.go` | `maybeBuildSweep`, `SweepBuilds`, `buildSweepTargets`, `buildSweepJob`, `deliverBuildSweep` |
```

Add these seven rows right after it (the settings row stays next). `retainFinished`, `markPendingNotices` and `adoptJobs` are in `queue.go`; `publishQueue` is in `state_publish.go`, whose existing row is edited below:

```
| `queue.go` | the work queue's state, boot, adoption and retention: `queueState`, `bootQueue`, `loadQueue`, `refreshPolicy`, `saveQueue`, `saveQueueQuiet`, `markPendingNotices`, `adoptJobs`, `retainFinished` |
| `queue_admit.go` | admission and starting: `admitJobs`, `tickQueue`, `startJob`, `runJobStart`, `deliverJobStarted` |
| `queue_watch.go` | the gated lock pass and settling: `maybeQueue`, `queuePassFor`, `runQueuePass`, `deliverQueuePass`, `settleGone`, `finishJob`, `applyUsage`, `lockHeld`, `signalAlive` |
| `queue_ops.go` | the queue's operations: `submitJob`, `cancelJob`, `moveJobToFront`, `notifyJob`, `setSchedulerCaps`, `cancelOwnerJobs` |
| `queue_notice.go` | a job's end sent to its owner's Claude: `sendJobNotice`, `noticeTarget`, `buildNoticeJob`, `deliverJobNotice`, `composeNotice`, `readLogTail` |
| `queue_requests.go` | the queue's requests: `SubmitJob`, `CancelJob` (`CancelFrom`), `MoveJobToFront`, `NotifyJob`, `SetSchedulerCaps` |
| `queue_view.go` | the published queue: `QueueView`, `JobView`, `Queue`, `queueView`, `queueJobView`, and the `QueueChanged` event (`publishQueue` in `state_publish.go` publishes it) |
```

The `state_publish.go` row (line 33). Quote:

```
| `state_views.go`, `state_publish.go` | `ModelView`, `AccountsView`, `GitHubView`, `WorkspacesView`; `publishState`, `Snapshot` |
```

Replace it with:

```
| `state_views.go`, `state_publish.go` | `ModelView`, `AccountsView`, `GitHubView`, `WorkspacesView`; `publishState`, `publishQueue`, `Snapshot` |
```

The publish order (line 104). Quote:

```
then `publishState`'s events (`state_publish.go`), then a `ViewsChanged`
```

Replace it with:

```
then `publishState`'s events (`ModelChanged`, `AccountsChanged`, `GitHubChanged`, then `QueueChanged`; `state_publish.go`), then a `ViewsChanged`
```

The state events (lines 106-109). Quote:

```
`publishState` emits three events, each carrying a state view of `state_views.go` (the fourth, `WorkspacesView`, is the workspace views):
```

Replace it with:

```
`publishState` emits four events, each carrying a published view (the first three are state views of `state_views.go`; `WorkspacesView`, the workspace views, is a fifth):
```

and quote the last bullet of that list:

```
- `GitHubChanged` (`GitHubView`: the snapshots, the poll errors, the aliases and gh's availability).
```

Add this bullet right after it:

```
- `QueueChanged` (`QueueView`, `queue_view.go`: the host, the caps as configured and in effect, why admission is paused, what the running jobs claim and use, and every job's `JobView`; `publishQueue` emits it on the first `Sync` and whenever the queue changed).
```

**`core/README.md`, the `SaveSettings` paragraph (line 98).** Quote:

```
`SaveSettings` (`settings.go`) replaces a workspace's settings, writes its `config.json` and applies the change at once: that workspace's launch toggles in `session`, and, for the global workspace only, the program the accounts' Claude commands run with (`Options.Program`, the global config's in the daemon, then the global workspace's last saved default program).
```

Replace it with:

```
`SaveSettings` (`settings.go`) replaces a workspace's settings, writes its `config.json` and applies the change at once: that workspace's launch toggles in `session`, and, for the global workspace only, the program the accounts' Claude commands run with (`Options.Program`, the global config's in the daemon, then the global workspace's last saved default program). Saving the global workspace keeps the `scheduler` block as it is on disk (a save never rewrites the caps; `SetSchedulerCaps` does), and a global `config.json` that cannot be read or parsed refuses the save.
```

**`app/README.md`.** Line 10. Quote:

```
- `Sync` never leaves the client. It returns the state events for what changed since the last `Sync` (`WorkspacesChanged`, `ModelChanged`, `AccountsChanged`, `GitHubChanged`, then each changed workspace's `ViewsChanged`, coalesced to the newest), then the other events in arrival order; `applyCoreEvent` applies each.
```

Replace the parenthesis with:

```
(`WorkspacesChanged`, `ModelChanged`, `AccountsChanged`, `GitHubChanged`, `QueueChanged`, then each changed workspace's `ViewsChanged`, coalesced to the newest)
```

Line 84. Quote:

```
- The view modes are `viewMode`s: `m.viewMode` (focus, overview, workbench) is orthogonal to `m.state`: overlays and per-state handlers work unchanged on top of it. `tab` toggles focus and overview; `enter`/`esc` return to focus; `n`/`N` drop to focus first.
```

Replace it with:

```
- The view modes are `viewMode`s: `m.viewMode` (focus, overview, workbench, work) is orthogonal to `m.state`: overlays and per-state handlers work unchanged on top of it. `tab` toggles focus and overview; `enter`/`esc` return to focus; `n`/`N` drop to focus first; `w` enters the work view from focus or overview and returns to the mode it came from.
```

Then add a bullet as the last bullet of the `## View modes` section, right after the bullet that starts `- The review tab (`5`/`c`)` and before the blank line that precedes `## Keys, modal states and mouse`:

```
- The work view (`app/work.go`, drawn by `RenderWork` in `ui/work.go`) is the host's job queue. `w` is the sync action `cs.actions.toggle_work_view`, which defers `toggleWorkView` like `ToggleOverview` (`enterWorkView`, `leaveWorkView`); the view reads `home.queue` alone, its selection is a job ID re-resolved on every read (`workSelected`), and the selected job's log tail arrives as a `workLogMsg` (`workLogCmd`). `enter` opens a pager, `x`, `t` and `o` act on the job ID taken when the key passed, and `e` opens the caps form (`openWorkCaps`, `stateWorkCaps`, drawn by `ui/overlay/workCapsOverlay.go`), whose save sends `SetSchedulerCaps` and shows the model's refusal in the form. The view is never persisted and drops mouse input (`dropWorkMouse`). `app/work_badge.go` turns `home.queue` into the session cards' job badges (`ownerJobBadges`, `syncJobBadges`), which `applyCoreEvent`'s `QueueChanged` case republishes through `ui.SetJobBadges`.
```

**`ui/README.md`.** Line 10. Quote:

```
- **Workbench** (`workbench.go`): the right half of workbench mode, with markdown (`markdown.go`; Glamour styles in `markdown_style.go`), diff, files, terminal and review tabs. It holds the review pane through the `ReviewPane` interface.
```

Add this bullet right after it:

```
- **Work** (`work.go`): the work view, the host's job queue, rendered from a `core.QueueView` (`RenderWork`, and `RenderWorkPager` for a job's log). Everything an agent wrote is cleaned before it is drawn (`CleanText`). The rail and overview cards show a job badge, set by `app` through `SetJobBadges` (`card.go`).
```

In line 25 (the `ui/overlay/` bullet), quote the phrase:

```
merge picker, Session Launch Options and issue picker. The settings overlay's
```

Replace it with:

```
merge picker, Session Launch Options, issue picker and the work view's caps form (`workCapsOverlay.go`). The settings overlay's
```

**`docs/claude/adding-a-core-event.md`.** Line 11. Quote:

```
- **A state event** carries a published view whole (`WorkspacesChanged`, `ModelChanged`, `AccountsChanged`, `GitHubChanged`, `ViewsChanged`).
```

Replace it with:

```
- **A state event** carries a published view whole (`WorkspacesChanged`, `ModelChanged`, `AccountsChanged`, `GitHubChanged`, `QueueChanged`, `ViewsChanged`).
```

**`README.md`.** Line 16, the feature bullet. Quote:

```
- Drive sessions from the shell or a script (`loom session new`, `list`, `send`, `pause`, `resume`, `kill`)
```

Replace it with:

```
- Drive sessions from the shell or a script (`loom session new`, `list`, `send`, `pause`, `resume`, `kill`), and queue their heavy work against the host's CPU and memory caps (`loom job`)
```

- [ ] **Step 4: Run it to see it pass**

Run:

```
grep -c 'queue_view.go' core/README.md; grep -c 'work.go' app/README.md; grep -c 'work.go' ui/README.md; grep -c 'work_badge.go' app/README.md; grep -c 'QueueChanged' core/README.md app/README.md docs/claude/adding-a-core-event.md; grep -c 'loom job' README.md; CGO_ENABLED=0 go run ./tools/claudemd; CGO_ENABLED=0 go run ./tools/claudemd -idents
```

Expected: the first four counts are at least `1`, every `QueueChanged` count is at least `1` and `loom job` counts at least `1`; the lint prints nothing and exits 0, and `-idents` lists none of the symbols named above as missing (`queueState`, `bootQueue`, `RenderWork`, `ownerJobBadges`, `SetJobBadges`, `stateWorkCaps` and the rest all exist once Stories 3, 4 and 6 and Task 7.2 are in the tree).

- [ ] **Step 5: Commit**

```
git commit -m "docs(core,app,ui): the work queue's files and QueueChanged in the READMEs and the event guide" -- core/README.md app/README.md ui/README.md docs/claude/adding-a-core-event.md README.md
```
