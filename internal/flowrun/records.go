package flowrun

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/sched"
)

// The records of flow in loom part 3a's agent runs and part 2's gate runs:
// <state>/agents/<name>.json and <state>/gates/<name>.json. internal/cli
// writes them; internal/flowview reads them. One type, two users.

// AgentSchema and GateSchema are the records' versions.
const (
	AgentSchema = 1
	GateSchema  = 1
)

// An agent run's statuses beside a report's own (part 3a spec §5.5).
const (
	AgentRunning   = "running"
	AgentFailed    = "failed"
	AgentCancelled = "cancelled"
	// AgentUsageLimit is a run whose job stopped on the account's usage
	// limit (usage-limit hold spec §3): resumable, like TURN_CAP, once it
	// resets; AgentRecord.Limit says which and when.
	AgentUsageLimit = "USAGE_LIMIT"
)

// AgentRecord is one `loom flow agent run` and every job after it on its
// Claude session: <state>/agents/<id>.json.
type AgentRecord struct {
	Schema   int              `json:"schema"`
	ID       string           `json:"id"`
	Role     string           `json:"role"`
	Effort   string           `json:"effort,omitempty"`
	Cwd      string           `json:"cwd"`
	Brief    string           `json:"brief"`
	Session  string           `json:"session,omitempty"`
	Jobs     []AgentRecordJob `json:"jobs"`
	Status   string           `json:"status"`
	Question string           `json:"question,omitempty"`
	Report   string           `json:"report,omitempty"`
	CostUSD  float64          `json:"cost_usd,omitempty"`
	Turns    int              `json:"turns,omitempty"`
	Why      string           `json:"why,omitempty"`
	Limit    *Limit           `json:"limit,omitempty"` // set at AgentUsageLimit
}

// Limit is the usage limit an agent run stopped on (usage-limit hold spec
// §3): its window ("five_hour", "seven_day", …; "" unknown), when it resets
// (zero unknown), and when the run ended on it, which the engine measures
// the wait from, so a replay decides as the first pass did.
type Limit struct {
	Window string    `json:"window,omitempty"`
	Resets time.Time `json:"resets,omitzero"`
	At     time.Time `json:"at"`
}

// AgentRecordJob is one job of a run: its start, or an answer resuming it.
type AgentRecordJob struct {
	ID     sched.JobID `json:"id"`
	Answer string      `json:"answer,omitempty"`
	Read   bool        `json:"read,omitempty"` // its result is in the record
}

// Current is the run's latest job.
func (r *AgentRecord) Current() *AgentRecordJob { return &r.Jobs[len(r.Jobs)-1] }

// Ended reports whether the run has a report or ended without one.
func (r *AgentRecord) Ended() bool { return r.Status != AgentRunning }

// GateRecord is one `loom flow gates start` and what became of it: the run
// record, <state>/gates/<name>.json.
type GateRecord struct {
	Schema    int              `json:"schema"`
	Name      string           `json:"name"`
	Kind      string           `json:"kind"` // "story" or "final"
	Commits   []string         `json:"commits,omitempty"`
	Cwd       string           `json:"cwd"` // where its jobs run, queued-again ones too
	Steps     []GateRecordStep `json:"steps"`
	Cancelled bool             `json:"cancelled,omitempty"`
	// Nonce is this run's own, which keys each of its submits (gateKey);
	// "" in a record from before keys, whose submits carry none.
	Nonce string `json:"nonce,omitempty"`
}

// GateRecordStep is one line of the expansion and what became of it.
type GateRecordStep struct {
	Line    string        `json:"line"`              // as the expansion printed it, a LOCKED line as RUN <command>
	Run     bool          `json:"run,omitempty"`     // a RUN or LOCKED line, which a job runs
	Locks   []string      `json:"locks,omitempty"`   // a LOCKED line's locks, which each of its jobs takes
	Jobs    []sched.JobID `json:"jobs,omitempty"`    // its job in each generation, the latest last
	Verdict string        `json:"verdict,omitempty"` // "" until judged
	Why     string        `json:"why,omitempty"`
	Log     string        `json:"log,omitempty"` // its output, copied when its job ended
}

// gateFailing is the verdicts that fail a run, in the order a tally lists
// them after PASS: internal/cli's verdictFail, verdictKilled, verdictInfra
// and verdictVacuous (TestGateFailing_IsTheCLIsVerdicts).
var gateFailing = []string{"FAIL", "FAIL (killed)", "FAIL (infra)", "VACUOUS"}

// GateFailing is a copy of the verdicts that fail a gate run.
func GateFailing() []string { return slices.Clone(gateFailing) }

// Text is the line past its first word: a RUN line's command, or a SKIPPED
// or FAIL line's gate and why.
func (s GateRecordStep) Text() string {
	_, rest, _ := strings.Cut(s.Line, " ")
	return rest
}

// Job is the step's latest job, 0 for none.
func (s GateRecordStep) Job() sched.JobID {
	if len(s.Jobs) == 0 {
		return 0
	}
	return s.Jobs[len(s.Jobs)-1]
}

// Ended reports whether every step has a verdict, or the run was cancelled.
func (r *GateRecord) Ended() bool {
	return r.Cancelled || !slices.ContainsFunc(r.Steps, func(s GateRecordStep) bool { return s.Verdict == "" })
}

// Failed reports whether a step's verdict fails the run.
func (r *GateRecord) Failed() bool {
	return slices.ContainsFunc(r.Steps, func(s GateRecordStep) bool { return slices.Contains(gateFailing, s.Verdict) })
}

// GateOf is the table number of the step one of whose jobs is id, 0 for none.
func (r *GateRecord) GateOf(id sched.JobID) int {
	for i, s := range r.Steps {
		if id != 0 && slices.Contains(s.Jobs, id) {
			return i + 1
		}
	}
	return 0
}

// AgentPath is agent run name's record in dir.
func AgentPath(dir, name string) string { return filepath.Join(dir, name+".json") }

// GatePath is gate run name's record in dir.
func GatePath(dir, name string) string { return filepath.Join(dir, name+".json") }

// LoadAgent reads agent run name's record in dir, refusing one it can't
// decode, of another schema, or with no job.
func LoadAgent(dir, name string) (*AgentRecord, error) {
	return ReadAgent(AgentPath(dir, name))
}

// ReadAgent is LoadAgent by the record's path.
func ReadAgent(path string) (*AgentRecord, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r AgentRecord
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if r.Schema != AgentSchema {
		return nil, fmt.Errorf("%s: schema %d, and this loom reads %d", path, r.Schema, AgentSchema)
	}
	if len(r.Jobs) == 0 {
		return nil, fmt.Errorf("%s: a run with no job", path)
	}
	return &r, nil
}

// LoadGate reads gate run name's record in dir, refusing one it can't
// decode or of another schema.
func LoadGate(dir, name string) (*GateRecord, error) {
	return ReadGate(GatePath(dir, name))
}

// ReadGate is LoadGate by the record's path.
func ReadGate(path string) (*GateRecord, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r GateRecord
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if r.Schema != GateSchema {
		return nil, fmt.Errorf("%s: schema %d, and this loom reads %d", path, r.Schema, GateSchema)
	}
	return &r, nil
}

// SaveAgent writes r whole (a temp file renamed).
func SaveAgent(dir string, r *AgentRecord) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return config.AtomicWriteFile(AgentPath(dir, r.ID), append(b, '\n'), 0o600)
}

// SaveGate writes r whole (a temp file renamed).
func SaveGate(dir string, r *GateRecord) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return config.AtomicWriteFile(GatePath(dir, r.Name), append(b, '\n'), 0o600)
}
