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

// Finished reports whether the job has ended: Done, Failed, Cancelled or Lost.
func (s State) Finished() bool {
	switch s {
	case Done, Failed, Cancelled, Lost:
		return true
	}
	return false
}

// Waiting reports whether a job in state s has yet to start: queued, or held
// after its prerequisite (dependent jobs spec §4).
func (s State) Waiting() bool { return s == Queued || s == Held }

// Sizing is where a queued job's sizing stands: "" once sized, or
// SizingAsking while it waits for the advisor's prior (smart scheduler
// spec §5.4). Admission skips a job being sized.
type Sizing string

// SizingAsking is a job waiting for the advisor's prior.
const SizingAsking Sizing = "asking"

// Wait is why admission leaves a job being sized waiting.
func (Sizing) Wait() string { return "being sized (asking the agent)" }

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

// HoldText is n as the hold a running job counts at (past-peak holds spec
// §7): "holds 1c/2G".
func (n Need) HoldText() string { return fmt.Sprintf("holds %dc/%dG", n.CPUs, n.MemoryGB) }

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
	// ReleasePastPeaks lets a running job's hold fall below its claim once
	// it is past its peaks (past-peak holds spec §8).
	ReleasePastPeaks bool `json:"release_past_peaks"`
	// Contain runs each job in its own scope, and JobsCPUWeight is the
	// jobs slice's CPUWeight (cgroup spec §10).
	Contain       bool `json:"contain"`
	JobsCPUWeight int  `json:"jobs_cpu_weight"`
	// Greedy admission (cgroup spec §7, §10): the headroom kept outside the
	// queue (GB), the CPU pressure (%) past which no job starts, and the
	// summed core claims allowed as a multiple of the cores cap.
	ReserveGB      int `json:"reserve_gb"`
	CPUPressurePct int `json:"cpu_pressure_pct"`
	CPUOvercommit  int `json:"cpu_overcommit"`
	// Borrow lets a job the firm fit holds back start as a borrower, which
	// gives its memory back when memory is tight (memory borrowing spec §9).
	Borrow bool `json:"borrow"`
	// AgentJobs is how many agent jobs of one account run at once (flow in
	// loom part 3a, spec §5.2).
	AgentJobs int `json:"agent_jobs"`
}

// Notice is the fate of a job's completion message to its owner's Claude.
type Notice string

// The fates of a notice: none due or yet, sent, recorded as undelivered
// (with the reason in Record.NoticeWhy), or carried: another job's message
// reports it (dependent jobs spec §5.5), named in NoticeWhy.
const (
	NoticeNone        Notice = ""
	NoticeSent        Notice = "sent"
	NoticeUndelivered Notice = "undelivered"
	NoticeCarried     Notice = "carried"
)

// Record is a job as jobs.json holds it: everything but its environment,
// which is in the job's spec.json.
type Record struct {
	ID           JobID     `json:"id"`
	Order        int64     `json:"order"`           // lower first; the ID at submit, the lowest queued Order − 1 on a move to the front
	Owner        uint64    `json:"owner,omitempty"` // a core.InstanceID; 0 for none
	Agent        string    `json:"agent,omitempty"` // the subagent (its Claude agentId) whose Bash call submitted it; "" for the session's main conversation or unknown
	Description  string    `json:"description"`
	Argv         []string  `json:"argv"`
	Cwd          string    `json:"cwd"`
	Claim        Claim     `json:"claim"` // the claim it is admitted with: sized while queued (smart scheduler spec §5.4), fixed once it starts; an exclusive one stays exclusive
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

	// The smart scheduler's sizing (spec 2026-11-05 §5.4). Asked is the
	// submitter's claim as given (a 0 part was not claimed); Sources says
	// where each part of Claim came from, and is empty on a record queued by
	// a build before sizing, whose claim is left as it was.
	Asked     Claim        `json:"asked,omitzero"`
	Signature SignatureID  `json:"signature,omitempty"`
	Checkout  string       `json:"checkout,omitempty"` // the working tree's top it runs in (FindRepo); "" without a signature (advisor run context spec §5.1)
	Unsized   string       `json:"unsized,omitempty"`  // why the job has no signature
	Sizing    Sizing       `json:"sizing,omitempty"`
	Sources   ClaimSources `json:"sources,omitzero"`
	Learned   int          `json:"learned,omitempty"` // the runs its learned parts came from
	// FamilyRuns is the family pool's runs its family parts came from
	// (family sizing spec §5.3).
	FamilyRuns int    `json:"family_runs,omitempty"`
	AboveCap   bool   `json:"above_cap,omitempty"` // its memory from measured peaks was held to the cap
	BelowKB    int64  `json:"below_kb,omitempty"`  // the recent peak an explicit memory claim is below
	AgentWhy   string `json:"agent_why,omitempty"` // why it was sized without the advisor's prior after asking
	Adopted    bool   `json:"adopted,omitempty"`   // a restarted daemon adopted it: its sampled peak restarted

	// Spare is what a grant added to its base claim when it started (job
	// scaling spec §5.3); Claim is then the total.
	Spare Claim `json:"spare,omitzero"`

	// Dependent jobs (spec 2026-11-05-dependent-jobs §4): the job this one
	// runs after (0 for none), and when it joined the queue: at its submit,
	// or at its release; zero while it is held, and on a record from an
	// earlier build (QueuedSince).
	After JobID     `json:"after,omitempty"`
	Ready time.Time `json:"ready,omitzero"`

	// Past-peak holds (spec 2026-11-05-past-peak-holds §5.4): its usage
	// passed its lowered hold, so it held its claim from then on.
	HoldBroke bool `json:"hold_broke,omitempty"`

	// Confinement (cgroup spec §5): the scope it runs in and its cgroup, or
	// why it runs uncontained; Attempt counts its earlier runs (§8.4).
	Unit        string `json:"unit,omitempty"`
	Cgroup      string `json:"cgroup,omitempty"`
	Uncontained string `json:"uncontained,omitempty"`
	Attempt     int    `json:"attempt,omitempty"`

	// The ladder (cgroup spec §8): the rung it is on and since when, whether
	// this attempt was laddered, how often it was requeued and why last, a
	// requeue asked for (the kill is out), whether it may be requeued at all
	// (--no-requeue), and whether the starvation guard started it.
	Rung        Rung      `json:"rung,omitempty"`
	RungAt      time.Time `json:"rung_at,omitzero"`
	Laddered    bool      `json:"laddered,omitempty"`
	Requeues    int       `json:"requeues,omitempty"`
	LastRequeue string    `json:"last_requeue,omitempty"`
	Requeue     string    `json:"requeue,omitempty"`
	NoRequeue   bool      `json:"no_requeue,omitempty"`
	Starved     bool      `json:"starved,omitempty"`

	// Memory borrowing (spec 2026-11-06-memory-borrowing §7.3): whether it
	// runs as a borrower, when it was lent (zero unless lent) and the
	// seconds it was lent in this attempt, its give-back requeues across
	// attempts, a requeue asked for as a give-back, and whether this attempt
	// runs firm only (the one after a give-back requeue).
	Borrower   bool      `json:"borrower,omitempty"`
	LentAt     time.Time `json:"lent_at,omitzero"`
	LentSecs   int64     `json:"lent_secs,omitempty"`
	GaveBack   int       `json:"gave_back,omitempty"`
	GivingBack bool      `json:"giving_back,omitempty"`
	NoBorrow   bool      `json:"no_borrow,omitempty"`

	// Agent runs (flow in loom part 3a, spec §5.2): the job's kind ("" for
	// a build, KindAgent), and an agent job's account: its environment's
	// CLAUDE_CONFIG_DIR ("" for the default account).
	Kind       string `json:"kind,omitempty"`
	AccountKey string `json:"account_key,omitempty"`
	// Run is the run job (KindRun) this job was submitted under, from its
	// environment's LOOM_FLOW_RUN (flow in loom part 3b, planning amendment
	// 1): a pause spares it. 0 for any other job.
	Run JobID `json:"run,omitempty"`

	// Named locks (--lock): while it runs no other job naming one of them
	// starts, so jobs that bind one port or share one fixture take turns.
	Locks []string `json:"locks,omitempty"`
}

// QueuedSince is when r joined the queue: Ready, or Submitted on a record
// with none (a held job, or one from a build before dependent jobs).
func (r Record) QueuedSince() time.Time {
	if r.Ready.IsZero() {
		return r.Submitted
	}
	return r.Ready
}

// LentFor is how long r has been lent in this attempt as of now: its
// LentSecs, plus the current lend's length while it is lent.
func (r Record) LentFor(now time.Time) time.Duration {
	d := time.Duration(r.LentSecs) * time.Second
	if !r.LentAt.IsZero() {
		d += now.Sub(r.LentAt)
	}
	return d
}

// Exit is exit.json: how the command ended, written by the shim when it
// does (its codec is in files.go; it is here because Record holds one).
type Exit struct {
	Code     int       `json:"code"`             // -1 for a signal
	Signal   string    `json:"signal,omitempty"` // "SIGKILL"
	Ended    time.Time `json:"ended"`
	MaxRSSKB int64     `json:"max_rss_kb,omitempty"`
	Error    string    `json:"error,omitempty"` // the exec failed; Code is then 127

	CPUSeconds float64 `json:"cpu_seconds,omitempty"` // user and system time of the command and the descendants it waited for
	ReadBytes  *int64  `json:"read_bytes,omitempty"`  // Linux: what it read from storage; nil when not measured
	WriteBytes *int64  `json:"write_bytes,omitempty"` // Linux: what it wrote to storage, net of cancelled writes
}

// KindAgent is an agent job's kind (flow in loom part 3a, spec §5.2): a
// headless Claude process, admitted by a per-account limit, never by the
// caps, and never sized. A build job's kind is "".
const KindAgent = "agent"

// KindRun is a flow run engine's kind (flow in loom part 3b, spec §5.2): a
// process that drives a plan's agents and gates, started at once, outside
// the caps, the advisor and the agent limit, and never sized.
const KindRun = "run"

// Unclaimed reports whether a job of kind claims nothing: an agent or a run
// job. Such jobs are never sized, granted or contained, and get no
// parallelism hints.
func Unclaimed(kind string) bool { return kind == KindAgent || kind == KindRun }

// Submit is what a client sends to queue a job.
type Submit struct {
	Argv        []string `json:"argv"`
	Cwd         string   `json:"cwd"`
	Env         []string `json:"env"`
	Description string   `json:"description"`
	Claim       Claim    `json:"claim"` // CPUs or MemoryGB 0: the default claim's
	Notify      bool     `json:"notify"`
	Tail        int      `json:"tail"`
	Launch      string   `json:"launch"`               // $LOOM_LAUNCH; "" from a plain shell
	After       JobID    `json:"after,omitempty"`      // the prerequisite (--after); 0 for none
	NoRequeue   bool     `json:"no_requeue,omitempty"` // never requeued when memory runs short: paused instead (cgroup spec §8.2)
	Kind        string   `json:"kind,omitempty"`       // "" for a build, KindAgent
	Locks       []string `json:"locks,omitempty"`      // named locks it holds while it runs (--lock): a job naming one waits while another holds it
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
// folder is absolute; the prerequisite is not negative (0 is none); an agent
// or run job claims nothing and runs after nothing, and no other kind is
// known; at most maxLocks locks, each a lock name (ValidLockName) given once,
// and none on an agent or run job. It does
// not check that the folder exists, or that the prerequisite does, which the
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
	if s.After < 0 {
		return fmt.Errorf("--after is %d: it must be a job ID", s.After)
	}
	switch {
	case s.Kind != "" && s.Kind != KindAgent && s.Kind != KindRun:
		return fmt.Errorf("unknown job kind %q", s.Kind)
	case s.Kind == KindAgent && (s.Claim != Claim{} || s.After != 0):
		return errors.New("an agent job claims nothing and runs after nothing")
	case s.Kind == KindRun && (s.Claim != Claim{} || s.After != 0):
		return errors.New("a run job claims nothing and runs after nothing")
	}
	return checkLocks(s.Locks, s.Kind)
}

// maxLocks is how many locks one job may name, and maxLockChars how long a
// lock's name may be.
const (
	maxLocks     = 8
	maxLockChars = 64
)

// checkLocks is Validate's check of a submission's locks: at most maxLocks,
// each a valid name given once, and none on an agent or run job.
func checkLocks(locks []string, kind string) error {
	if len(locks) == 0 {
		return nil
	}
	switch kind {
	case KindAgent:
		return errors.New("an agent job takes no lock")
	case KindRun:
		return errors.New("a run job takes no lock")
	}
	if len(locks) > maxLocks {
		return fmt.Errorf("%d locks: at most %d", len(locks), maxLocks)
	}
	seen := make(map[string]bool, len(locks))
	for _, l := range locks {
		if !ValidLockName(l) {
			return fmt.Errorf("--lock %q: a name is 1–%d of A–Z, a–z, 0–9 and . _ : / -", l, maxLockChars)
		}
		if seen[l] {
			return fmt.Errorf("--lock %s is given twice", l)
		}
		seen[l] = true
	}
	return nil
}

// ValidLockName reports whether name is a lock's name: 1–maxLockChars
// characters, each a letter, a digit or one of . _ : / -, as in port:4300.
func ValidLockName(name string) bool {
	if name == "" || len(name) > maxLockChars {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', strings.ContainsRune("._:/-", r):
		default:
			return false
		}
	}
	return true
}

// CheckClaim refuses a claim, its default already applied, that is over a cap
// of p: the cores first, then the GB. An exclusive claim is never over, and
// an I/O job takes one of at least one slot.
func CheckClaim(c Claim, p Policy) error {
	if !c.exceeds(p.Caps) {
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

// String is the claim as one short word or "4c/6G io": the cores and GB, and
// io when it takes an I/O slot; "exclusive" for a claim on the whole host.
func (c Claim) String() string {
	if c.Exclusive {
		return "exclusive"
	}
	s := fmt.Sprintf("%dc/%dG", c.CPUs, c.MemoryGB)
	if c.IO {
		s += " io"
	}
	return s
}

// Spared is c as a claim with spare cores reads (job scaling spec §6): the
// base cores, then spare's, "4+2c/3G"; c.String() when spare has no cores.
func (c Claim) Spared(spare Claim) string {
	if spare.CPUs == 0 || c.Exclusive {
		return c.String()
	}
	s := fmt.Sprintf("%d+%dc/%dG", c.CPUs-spare.CPUs, spare.CPUs, c.MemoryGB)
	if c.IO {
		s += " io"
	}
	return s
}
