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
