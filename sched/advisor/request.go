package advisor

import "github.com/aidan-bailey/loom/sched"

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
	After       *After   `json:"after,omitempty"`       // the job this one runs after (dependent jobs spec §5.6)
}

// After is the job a job runs after, as the advisor is told of it: its
// command, as its belief normalizes it (its argv when it has none), and its
// description.
type After struct {
	Command     []string `json:"command"`
	Description string   `json:"description"`
}

// Known is a belief as the advisor is told of it.
type Known struct {
	ID           sched.SignatureID  `json:"id"`
	Repo         string             `json:"repo"`
	Folder       string             `json:"folder"`
	Command      []string           `json:"command"`
	Runs         int                `json:"runs"`
	Claim        sched.Claim        `json:"claim"`
	Sources      sched.ClaimSources `json:"sources"`
	UsualSeconds int                `json:"usual_seconds,omitempty"`
	P90Seconds   int                `json:"p90_seconds,omitempty"`
	Notes        []string           `json:"notes,omitempty"`
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
