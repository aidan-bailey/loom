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
