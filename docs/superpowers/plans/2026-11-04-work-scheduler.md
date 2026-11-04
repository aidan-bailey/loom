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
- **Tests.** No test touches the real `~/.loom` or the user's tmux server: every package whose tests reach `config` calls `testenv.IsolateLoomDirs` from its `TestMain`. Tests that start real shims (`sched`, `core`, `internal/cli`) run the test binary as the shim. Their `TestMain` calls `sched.MaybeRunTestShim()` first, and the runner is `sched.TestRunner()`. Real jobs run short commands (`sh -c`, `sleep`), and every test leaves no process group behind. Fixtures go through the production path: a booted model, a real shim, `jobs.json` written by `sched.SaveStore`.
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
| `job_shim.go`, `main.go` | The hidden `loom job-shim` command | 2 |
| `core/queue.go`, `core/queue_admit.go`, `core/queue_watch.go`, `core/queue_ops.go`, `core/queue_notice.go` | The queue's state, load and save, boot adoption, retention, admission passes, starting, watching and settling, cancel, the internal operations, notices | 3 |
| `core/mod.go`, `core/gate.go`, `core/tick.go`, `core/model.go`, `core/load.go`, `core/requests.go`, `core/seams.go` | `OriginJob`; `gateQueue`; the tick's call; the model's queue and `Deliver` cases; `Boot`; pause and kill cancelling the owner's jobs; `SetJobRunnerForTest` | 3 |
| `core/queue_test.go`, `core/queue_watch_test.go`, `core/queue_notice_test.go`, `core/gate_test.go`, `core/testmain_test.go` | The queue's tests, the gated-job case, the test shim | 3 |
| `core/iface.go`, `core/loop_core.go`, `core/queue_requests.go`, `core/queue_view.go`, `core/events.go`, `core/state_publish.go`, `core/rpc/server.go`, `core/rpc/replica.go`, `core/rpc/methods_gen.go`, `docs/specs/protocol.md`, `app/core_glue.go`, `app/app.go` | The `Core` methods, the requests' replies, `QueueView`, `QueueChanged`, `Reply.Job`, the replica, the wire, the TUI's applier | 4 |
| `core/queue_requests_test.go`, `core/queue_view_test.go`, `core/rpc/client_test.go`, `core/rpc/wire_test.go` | Their tests | 4 |
| `internal/cli/job.go`, `internal/cli/job_test.go`, `internal/cli/testmain_test.go`, `internal/cli/README.md`, `main.go` | `loom job` | 5 |
| `session/claude-loom-context.md`, `session/claude-loom-context-workspace.md` | The context paragraph | 5 |
| `e2e/job_test.go` | A daemon restarted while a job runs | 5 |
| `app/work.go`, `app/work_test.go`, `app/app.go`, `app/app_scripts.go`, `app/state_default.go`, `app/overview.go` | The work view mode, its keys, the log tail and pager, the caps form's state | 6 |
| `ui/work.go`, `ui/work_test.go`, `ui/overlay/workCapsOverlay.go`, `ui/overlay/workCapsOverlay_test.go` | Rendering the view; the caps form | 6 |
| `script/host.go`, `script/api_actions.go`, `script/api_actions_test.go`, `script/defaults.lua` | `toggle_work_view` and `w` | 6 |
| `ui/card.go`, `ui/overview.go`, `ui/card_test.go`, `app/work_badge.go`, `app/work_badge_test.go` | The job badges | 7 |
| `USAGE.md`, `docs/ARCHITECTURE.md`, `docs/specs/scripting.md`, `docs/claude/the-work-queue.md`, `docs/claude/INDEX.md` | The docs | 7 |

## Names contract

Every name one story defines and another uses. A story may add names of its own. A name another story needs that this section lacks goes back to the manager.

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

The hint variables, each set to N unless `env` already sets it: `CARGO_BUILD_JOBS=N`, `RUST_TEST_THREADS=N`, `MAKEFLAGS=-jN`, `NIX_BUILD_CORES=N`, `CMAKE_BUILD_PARALLEL_LEVEL=N`, `GOFLAGS=-p=N`. `LOOM_JOB=<id>` is always set.

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

The root package's `jobShimCmd` (`job_shim.go`, `Use: "job-shim <job dir>"`, `Hidden: true`, one argument) calls `os.Exit(sched.RunShim(args[0]))`. `main.go` adds it to `rootCmd`.

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
- `OriginJob Origin = "job"` in `core/mod.go`. It needs the mod, has no "From" line and isn't the person's words. `withoutMod` records the notice as undelivered and never pastes.
- `gateQueue`, a `gateKind`, with interval `queueInterval = 3 * time.Second`, named `"queue"`.
- `func (m *Model) SetJobRunnerForTest(r sched.Runner)` in `core/seams.go`.

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
	ID             sched.JobID
	State          sched.State
	Description    string
	Argv           []string
	Cwd            string
	Owner          InstanceID // 0: none
	OwnerTitle     string     // "" when the owner isn't served
	OwnerWorkspace string
	Claim          sched.Claim
	Need           sched.Need // an exclusive claim's at the current caps
	Submitted      time.Time
	Started        time.Time
	Ended          time.Time
	Exit           *sched.Exit
	Failure        string
	CancelReason   string
	PeakRSSKB      int64
	RSSKB          int64
	CPU            float64
	LogPath        string // JobDir(Dir, ID)/output.log
	Waiting        string
	Notice         sched.Notice
	NoticeWhy      string
}
```

- `core.Core` gains, in `core/iface.go`:
  - `Queue() QueueView` (`rpc:local`);
  - the requests `SubmitJob(spec sched.Submit, req ReqID)`, `CancelJob(id sched.JobID, from CancelFrom, req ReqID)`, `MoveJobToFront(id sched.JobID, req ReqID)`, `NotifyJob(id sched.JobID, req ReqID)` and `SetSchedulerCaps(s config.SchedulerSettings, req ReqID)`.
- `type CancelFrom string`, with `CancelFromCLI CancelFrom = "cli"` and `CancelFromView CancelFrom = "view"`. `CancelJob` maps them to the reasons `with loom job cancel` and `from the work view`, and refuses any other value.
- `Reply.Job sched.JobID`: the new job's ID, on `SubmitJob`'s success. The unknown launch's warning travels in `Reply.Notice`.
- `QueueChanged{View QueueView}`, a state event with the coalescing key `"queue"`, published when `queueState.rev` moves, after the published workspaces' views.
- The TUI keeps the newest view in `home.queue core.QueueView`, set by `applyCoreEvent`'s `QueueChanged` case.

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

## Stories

- **Story 1** — `sched`'s logic and the `scheduler` config block.

  The pure half of the scheduler, with no processes: the job, claim, caps, policy and record types; `Resolve`, which applies the host defaults and the bounds; `Admit`, which is first fit with the starvation guard and says why each job waits; `Hints`; the `Submit` and claim validation; and the `jobs.json` store and per-job file codecs. Also config's `SchedulerSettings`, and the package's `CLAUDE.md`, which the coverage lint requires. The code holds the landmine marker `os.RemoveAll(`, in `RemoveJobDir` only. It removes `JobDir(dir, id)`, built from the ID, never a path read from a file.

  - **Developer:** intermediate-dev
  - **Files:** `config/settings.go`, `config/config.go`, `config/settings_test.go`, `config/README.md`, `sched/doc.go`, `sched/job.go`, `sched/job_test.go`, `sched/policy.go`, `sched/policy_test.go`, `sched/admit.go`, `sched/admit_test.go`, `sched/hints.go`, `sched/hints_test.go`, `sched/store.go`, `sched/store_test.go`, `sched/files.go`, `sched/files_test.go`, `sched/testmain_test.go`, `sched/CLAUDE.md`, `sched/README.md`
  - **Interfaces:** exposes the config and `sched` logic names of the names contract (Story 1); uses none
  - **Verify:** `CGO_ENABLED=0 go test ./sched/... ./config/... && CGO_ENABLED=0 go run ./tools/claudemd`
  - **Review:** deep
  - **Reading:** spec §4, §5.1, §5.2, §5.9, §6.1, §11, §12; `config/settings.go:1-200`, `config/config.go:180-210`, `config/config.go:315-360`, `config/atomic.go`, `config/CLAUDE.md`, `session/buildsweep/CLAUDE.md`, `session/buildsweep/testmain_test.go`, `internal/testenv/CLAUDE.md`

- **Story 2** — The job runner: `loom job-shim`, starting, probing, signalling, sampling and the host.

  The process half of `sched`. `RunShim` lowers its priority (nice 10, and the Linux ioprio), takes `run.lock`, writes `started.json`, runs the argv with the spec's environment plus the hints, passes signals to the command, and writes `exit.json`. `Runner.Start` starts a shim as a `Setsid` child and reaps it. `Probe` tests the lock, `SignalGroup` signals a process group, `Sample` and `ParsePS` read `ps`, and `DetectHost` reads the host. The story also adds the test-shim hook, the Windows stubs, and the hidden `job-shim` command in the root package. The code holds the landmine markers `syscall.Kill(` (`SignalGroup`) and `os.Environ()` (`Runner.Start` builds the shim process's environment as `os.Environ()` plus `Runner.Env`; the job's environment comes only from `spec.json`). Its platform code lives only in `_unix.go`, `_windows.go`, `_linux.go`, `_darwin.go` and `_other.go` files.

  - **Developer:** intermediate-dev
  - **Files:** `sched/shim.go`, `sched/shim_unix.go`, `sched/shim_windows.go`, `sched/shim_test.go`, `sched/prio_linux.go`, `sched/prio_other.go`, `sched/sample.go`, `sched/sample_test.go`, `sched/host.go`, `sched/host_linux.go`, `sched/host_darwin.go`, `sched/host_other.go`, `sched/host_test.go`, `sched/testshim.go`, `sched/testmain_test.go`, `sched/CLAUDE.md`, `sched/README.md`, `job_shim.go`, `main.go`
  - **Interfaces:** exposes the `sched` runtime names of the names contract (Story 2) and `jobShimCmd`; uses `Spec`, `Started`, `Exit`, `ReadSpec`, `writeStarted`, `writeExit`, `Hints`, `Host`, `JobDir` and the file-name constants from Story 1
  - **Verify:** `CGO_ENABLED=0 go test ./sched/... && CGO_ENABLED=0 go build ./... && GOOS=windows CGO_ENABLED=0 go build ./... && GOOS=darwin CGO_ENABLED=0 go build ./...`
  - **Review:** deep
  - **Reading:** spec §5.3, §5.4, §5.5, §5.6, §10, §11, §12; `session/buildsweep/lock_unix.go`, `session/buildsweep/lock_windows.go`, `session/buildsweep/proc.go`, `internal/daemon/proc_unix.go`, `internal/daemon/proc_windows.go`, `main.go:30-50`, `main.go:405-435`, `internal/cli/CLAUDE.md`

- **Story 3** — The queue in the model: admission, starting, watching, cancelling, adoption, notices and the session lifecycle.

  The model owns the queue. At boot it loads `jobs.json`, leaving a file it can't read untouched and pausing; it adopts and settles what ran while it was down, queued records included, and only then admits. Admission passes run on submit, on a job's end, on a caps change, on a move and on the tick while jobs wait, reading the global config each time. Starting a job is a model job that writes `spec.json`'s hint CPUs, starts the shim, and waits up to 5 s for `started.json`. `gateQueue` tests locks, samples usage, settles jobs, escalates a cancel to SIGKILL after 10 s, and kills a lost job's group only when the sample proves the group is the job's. `OriginJob` notices go through the outbox and are recorded as undelivered when there is no mod. Pause and kill cancel the owner's jobs, and retention drops finished jobs beyond `keep_finished`. The internal operations of the contract are what Story 4 wraps. The code calls `sched.SignalGroup` and `sched.RemoveJobDir`, so a signal goes only to a group whose lock test proves its shim alive, or which the lost-job rule of spec §5.4 allows. The scheduler's state is touched only on the loop, and jobs get snapshots.

  - **Developer:** intermediate-dev
  - **Files:** `core/queue.go`, `core/queue_admit.go`, `core/queue_watch.go`, `core/queue_ops.go`, `core/queue_notice.go`, `core/mod.go`, `core/gate.go`, `core/tick.go`, `core/model.go`, `core/load.go`, `core/requests.go`, `core/seams.go`, `core/queue_test.go`, `core/queue_watch_test.go`, `core/queue_notice_test.go`, `core/gate_test.go`, `core/testmain_test.go`
  - **Interfaces:** exposes `queueState`, `jobUsage`, the internal operations, `OriginJob`, `gateQueue` and `SetJobRunnerForTest` of the names contract (Story 3); uses Story 1's logic and Story 2's runtime
  - **Verify:** `CGO_ENABLED=0 go test ./core/... && CC=clang CGO_ENABLED=1 go test -race ./core/ -run 'Queue|Job'`
  - **Review:** deep
  - **Reading:** spec §5.2-§5.9, §6.2, §11, §12; `docs/claude/adding-a-gated-job.md`, `core/CLAUDE.md`, `core/build_sweep.go`, `core/gate.go`, `core/gate_test.go:195-260`, `core/tick.go:100-140`, `core/model.go:250-330`, `core/model.go:430-500`, `core/load.go:205-300`, `core/requests.go:280-320`, `core/mod.go:55-130`, `core/mod.go:385-450`, `core/persist.go`, `core/testmain_test.go`

- **Story 4** — The queue on the wire: the requests, `QueueView` and `QueueChanged`.

  This publishes the queue to every client. Story 3's operations become `Core` requests that reply with their refusals, and `SubmitJob` replies with the new ID in `Reply.Job` and the unknown launch's warning in `Reply.Notice`. `Queue()` is a local query on the replica. `QueueChanged` is a state event, with every step of the event guide: `EventTypes`, the coalescing key, the replica's state and its events in publish order, `Model.Snapshot`, and the TUI's applier into `home.queue`. The story regenerates `methods_gen.go` and `protocol.md`. The environment never appears in a published view.

  - **Developer:** intermediate-dev
  - **Files:** `core/iface.go`, `core/loop_core.go`, `core/queue_requests.go`, `core/queue_view.go`, `core/events.go`, `core/state_publish.go`, `core/rpc/server.go`, `core/rpc/replica.go`, `core/rpc/methods_gen.go`, `docs/specs/protocol.md`, `app/core_glue.go`, `app/app.go`, `core/queue_requests_test.go`, `core/queue_view_test.go`, `core/rpc/client_test.go`, `core/rpc/wire_test.go`
  - **Interfaces:** exposes `QueueView`, `JobView`, `CancelFrom`, the `Core` methods, `Reply.Job`, `QueueChanged` and `home.queue` of the names contract (Story 4); uses Story 3's `queueState` and internal operations
  - **Verify:** `CGO_ENABLED=0 go test ./core/... ./app/...`
  - **Review:** deep
  - **Reading:** spec §9, §6.2, §11; `docs/claude/adding-a-core-method.md`, `docs/claude/adding-a-core-event.md`, `core/iface.go`, `core/loop_core.go`, `core/events.go:195-260`, `core/state_publish.go`, `core/rpc/replica.go`, `core/rpc/server.go` (coalesceKey), `core/rpc/CLAUDE.md`, `app/core_glue.go`, `core/web.go` (a published view's shape)

- **Story 5** — `loom job`, the Claude context paragraph, and the restart e2e.

  The CLI is a daemon client joined through `cli.Join`. `submit` captures the argv, cwd, environment and `$LOOM_LAUNCH`, and prints the queue line. `run` and `wait` follow a job from the replica and `output.log`, exit with spec §6.3's codes, and send `NotifyJob` when interrupted. `logs`, `list` and `cancel` complete it. The story also adds the paragraph of spec §8 to both context files, and an e2e test that restarts the daemon while a job runs. The code holds the landmine marker `os.Environ()`: `submit` sends `os.Environ()` as the job's environment, and builds no git command from it.

  - **Developer:** intermediate-dev
  - **Files:** `internal/cli/job.go`, `internal/cli/job_test.go`, `internal/cli/testmain_test.go`, `internal/cli/README.md`, `main.go`, `session/claude-loom-context.md`, `session/claude-loom-context-workspace.md`, `e2e/job_test.go`
  - **Interfaces:** exposes `cli.JobCmd` and the CLI texts of the names contract (cli); uses Story 4's `Core` methods, `QueueView`, `Reply.Job` and `QueueChanged`, and Story 2's `MaybeRunTestShim`/`TestRunner`
  - **Verify:** `CGO_ENABLED=0 go test ./internal/cli/... ./session/... && CGO_ENABLED=0 go test -tags e2e ./e2e/... -run Job`
  - **Review:** deep
  - **Reading:** spec §6, §8, §12; `internal/cli/CLAUDE.md`, `internal/cli/session.go`, `internal/cli/sweep.go`, `internal/cli/join.go`, `internal/cli/testmain_test.go`, `main.go:405-435`, `e2e/CLAUDE.md`, `e2e/daemon_test.go`, `session/loom_context.go`

- **Story 6** — The work view: the view mode, `w`, the log tail and pager, and the caps form.

  The fourth view mode, over `home.queue`. It shows the header with the host, the capacity bars (or the paused reason in their place), the RUNNING, QUEUED and RECENT sections with why each queued job waits, and the selected job's log tail, read from disk. `enter` opens a scrolling pager, `x` cancels after a confirm, `t` moves a job to the front, `o` focuses the owner's session (opening its tab), and `e` opens the caps form, which sends `SetSchedulerCaps` and shows its refusal. `w` or `esc` returns to the mode the view came from. The view isn't persisted, and mouse input is dropped. The keys follow the TUI's rules: an action names the job's ID captured when its key passed, and the view re-reads `home.queue` after every drain.

  - **Developer:** intermediate-dev
  - **Files:** `app/work.go`, `app/work_test.go`, `app/app.go`, `app/app_scripts.go`, `app/state_default.go`, `app/overview.go`, `ui/work.go`, `ui/work_test.go`, `ui/overlay/workCapsOverlay.go`, `ui/overlay/workCapsOverlay_test.go`, `script/host.go`, `script/api_actions.go`, `script/api_actions_test.go`, `script/defaults.lua`
  - **Interfaces:** exposes `viewWork`, `toggle_work_view`, `ToggleWorkView` and `handleWorkKey` of the names contract (TUI); uses Story 4's `home.queue`, `QueueView`, `JobView` and the `CancelJob`, `MoveJobToFront` and `SetSchedulerCaps` requests
  - **Verify:** `CGO_ENABLED=0 go test ./app/... ./ui/... ./script/...`
  - **Review:** deep
  - **Reading:** spec §7.1, §7.2; `app/CLAUDE.md`, `ui/CLAUDE.md`, `script/CLAUDE.md`, `docs/claude/adding-a-modal-state.md`, `app/app.go:85-110`, `app/overview.go`, `app/app_scripts.go:370-390`, `script/host.go:100-120`, `script/api_actions.go:195-215`, `script/defaults.lua`, `ui/overview.go`, `ui/overlay/settingsOverlay.go`

- **Story 7** — Badges on session cards, and the docs.

  A session's rail card and overview card show `⚙ job <id>` (with `+N` for more running) or `⚙ queued`, computed from `home.queue`. The docs follow: `USAGE.md` gains `loom job`, the work view and its keys in the Keyboard Reference, the `scheduler` block, and `KillMode=process` in the systemd unit; `docs/ARCHITECTURE.md` gains the `sched` row, `jobs/` in persistent state, and the queue in the daemon's work with no TUI; `docs/specs/scripting.md` gains `toggle_work_view`; and the guide `docs/claude/the-work-queue.md`, with its `INDEX.md` entry, records the model's side, the test shim, and the lock-before-signal rule.

  - **Developer:** intermediate-dev
  - **Files:** `ui/card.go`, `ui/overview.go`, `ui/card_test.go`, `app/work_badge.go`, `app/work_badge_test.go`, `USAGE.md`, `docs/ARCHITECTURE.md`, `docs/specs/scripting.md`, `docs/claude/the-work-queue.md`, `docs/claude/INDEX.md`
  - **Interfaces:** exposes none; uses Story 4's `home.queue` and `JobView`, and Story 6's `toggle_work_view`
  - **Verify:** `CGO_ENABLED=0 go test ./app/... ./ui/... ./tools/claudemd/... && CGO_ENABLED=0 go run ./tools/claudemd`
  - **Review:** deep
  - **Reading:** spec §7.3, §5.6, §10, §13; `ui/card.go`, `ui/overview.go`, `USAGE.md` (the Keyboard Reference, the systemd unit), `docs/ARCHITECTURE.md`, `docs/specs/scripting.md`, `docs/claude/INDEX.md`, `CLAUDE.md` (Conventions for CLAUDE.md files)

<!-- plan_join: the stories below are generated from the drafts; edit the drafts, then re-join -->
