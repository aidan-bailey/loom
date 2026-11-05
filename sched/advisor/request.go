package advisor

import (
	"math"
	"time"

	"github.com/aidan-bailey/loom/sched"
)

// Host is what the advisor is told of the host.
type Host struct {
	Cores        int         `json:"cores"`
	RAMGB        int         `json:"ram_gb"`
	Caps         sched.Caps  `json:"caps"`
	DefaultClaim sched.Claim `json:"default_claim"`
}

// Job is the job a call is about.
type Job struct {
	Argv        []string `json:"argv"`
	Command     []string `json:"command"`
	CommandEnv  []string `json:"command_env,omitempty"`
	Description string   `json:"description"`
	Repo        string   `json:"repo"`
	Folder      string   `json:"folder"`
	Environment []string `json:"environment,omitempty"` // AllowedEnv's
}

// Known is a belief as the advisor is told of it.
type Known struct {
	ID           sched.SignatureID  `json:"id"`
	Repo         string             `json:"repo"`
	Folder       string             `json:"folder"`
	Command      []string           `json:"command"`
	Env          []string           `json:"env,omitempty"` // its signature's pairs, through AllowedEnv
	Runs         int                `json:"runs"`
	Claim        sched.Claim        `json:"claim"`
	Sources      sched.ClaimSources `json:"sources"`
	UsualSeconds int                `json:"usual_seconds,omitempty"`
	P90Seconds   int                `json:"p90_seconds,omitempty"`
	Notes        []string           `json:"notes,omitempty"`
	Recent       []KnownRun         `json:"recent,omitempty"` // RecentRuns
}

// MaxRecentRuns is how many of a known belief's runs a request carries.
const MaxRecentRuns = 3

// KnownRun is one of a known belief's recent runs as the advisor is told of
// it (advisor run context spec §5.2).
type KnownRun struct {
	Outcome     sched.Outcome `json:"outcome"`
	MinutesAgo  int           `json:"minutes_ago"`
	Cores       int           `json:"cores,omitempty"` // the cores it ran with; 0 for an exclusive claim
	WallSeconds int           `json:"wall_seconds"`
	PeakGB      float64       `json:"peak_gb"`
	WroteMB     *int64        `json:"wrote_mb,omitempty"` // nil when it measured no I/O
	Here        *bool         `json:"here,omitempty"`     // whether it ran in the job's checkout; nil when either checkout is unknown
}

// RecentRuns is the newest MaxRecentRuns of runs (newest first, as a belief
// keeps them) as the advisor is told of them at now, for a job in checkout
// ("" for none): cores with spare ones, the peak in GiB to one decimal, MiB
// written, and here when both checkouts are known.
func RecentRuns(runs []sched.Run, checkout string, now time.Time) []KnownRun {
	var out []KnownRun
	for _, r := range runs[:min(len(runs), MaxRecentRuns)] {
		k := KnownRun{
			Outcome: r.Outcome, MinutesAgo: max(0, int(now.Sub(r.Ended)/time.Minute)), Cores: r.Claim.CPUs + r.Spare,
			WallSeconds: int(math.Round(r.WallSeconds)), PeakGB: math.Round(float64(r.PeakKB)/(1<<20)*10) / 10,
		}
		if r.WriteBytes != nil {
			mb := int64(math.Round(float64(*r.WriteBytes) / (1 << 20)))
			k.WroteMB = &mb
		}
		if checkout != "" && r.Checkout != "" {
			here := r.Checkout == checkout
			k.Here = &here
		}
		out = append(out, k)
	}
	return out
}

// PriorRequest asks for a prior.
type PriorRequest struct {
	Task    string  `json:"task"` // "prior"
	Job     Job     `json:"job"`
	Host    Host    `json:"host"`
	Beliefs []Known `json:"beliefs"`
}

// ReconcileRequest asks for a note on a surprise.
type ReconcileRequest struct {
	Task     string       `json:"task"` // "reconcile"
	Job      Job          `json:"job"`
	Host     Host         `json:"host"`
	Belief   Known        `json:"belief"`
	Runs     []sched.Run  `json:"runs"`
	Prior    *sched.Prior `json:"prior,omitempty"`
	Run      sched.Run    `json:"run"`
	Surprise string       `json:"surprise"`
	LogTail  []string     `json:"log_tail,omitempty"`
}
