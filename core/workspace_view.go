package core

import (
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/watch"
)

// WorkspaceID names a workspace the model serves: a hash of its canonical
// config dir (wsIDOf), so every daemon over one disk gives it the same ID. A
// workspace keeps its ID while the model serves it, which is until the model
// stops (a tab closed and reopened shows the same workspace); a collision
// between two served workspaces probes to the next free value. 0 means none.
type WorkspaceID uint64

// WorkspaceView is a served workspace as its clients see it: a value the
// model publishes (WorkspacesChanged) and answers queries with. Every
// field is a copy.
type WorkspaceView struct {
	ID WorkspaceID
	// Name is the registered name, "" for the global context; Label names
	// it in notices (its name, or "global").
	Name, Label string
	// RepoPath and ConfigDir are the workspace context's ("" in a bare
	// context).
	RepoPath, ConfigDir string
	// Settings is a copy of the workspace's config.json.
	Settings config.Settings
	// UIPrefs and HelpScreensSeen are copies of its state.json.
	UIPrefs         config.UIPrefs
	HelpScreensSeen uint32
	// WritesRefused is set while the workspace's storage refuses writes
	// (its load failed: Storage.WritesRefused). PreservedTitles are the
	// titles of records its storage preserves but could not load.
	WritesRefused   bool
	PreservedTitles []string
	// Recovery is the summary of its last orphan reconcile.
	Recovery RecoverySummary
	// LoadErr is the text of the error its storage failed to load with,
	// "" once it loaded: what a client can show a failed workspace with.
	// Opening it retries the load (Core.Open), which reports the error
	// again if it still fails.
	LoadErr string
	// Watch is the issue watcher's summary for the workspace: whether it
	// is on, why its last tick decided nothing, and the newest
	// escalations, from which the PWA's push watch sends one push each.
	Watch WatchSummary
}

// WatchSummary is the part of the issue watcher's state every
// WorkspaceView carries: On is the workspace's switch (Settings.Watch),
// Loaded whether memory has been read from watch.json yet (false until
// the first poll loads it, so a client can tell escalations already in
// memory from new ones), Err why the last tick decided nothing ("" when
// it decided), and Escalations the newest at most 20 of memory's
// escalated issues, newest first by time, then by issue number. All four
// are zero while the watcher is off.
type WatchSummary struct {
	On          bool
	Loaded      bool
	Err         string
	Escalations []WatchEscalation
}

// WatchEscalation is one escalated issue: what the watcher wrote on it
// and when.
type WatchEscalation struct {
	Issue  int       `json:"issue"`
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

// WatchView is the issue watcher's state for one workspace as ReadWatch
// answers it (the Reply's Watch; `loom watch status --json` prints it).
// Err is why the last tick decided nothing (config, memory or a read; ""
// when it decided). Actions are the last tick's, or with a dry run the
// next tick's, none taken. Escalations are every escalated issue memory
// holds, newest first. Everything but On is empty while the watcher is
// off.
type WatchView struct {
	On           bool              `json:"on"`
	Err          string            `json:"err"`
	Viewer       string            `json:"viewer"`
	Considered   int               `json:"considered"`
	Unconsidered int               `json:"unconsidered"`
	Issues       []WatchIssueView  `json:"issues"`
	Actions      []watch.Action    `json:"actions"`
	Escalations  []WatchEscalation `json:"escalations"`
}

// WatchIssueView is one issue in a WatchView. State is "open" or "closed"
// from the last snapshot, "unknown" for an issue seen only through a
// session or a PR. Session is the watcher session's title ("" for none),
// PR the watcher's open PR (0 for none). Waiting is why the issue's
// action was a wait, else the hook's failure text, else "". Failed is the
// text of the issue's last failed or refused action (watchState.actErrs),
// "" for none. Nudges is memory's count per kind on that PR (nil for
// none).
type WatchIssueView struct {
	Number    int            `json:"number"`
	State     string         `json:"state"`
	Session   string         `json:"session"`
	PR        int            `json:"pr"`
	Waiting   string         `json:"waiting"`
	Failed    string         `json:"failed"`
	Nudges    map[string]int `json:"nudges"`
	Escalated bool           `json:"escalated"`
}

// RegistryView is a copy of the workspace registry: every registered
// workspace, the open tabs to restore (the open list resolved to its
// registered workspaces, in order, as GetOpenWorkspaces resolves it), and
// the workspace last focused, which a restore focuses.
type RegistryView struct {
	Workspaces []config.Workspace
	Open       []config.Workspace
	LastUsed   string
}

// AccountNames are the registered accounts, default first. Present is
// false until the account registry is set up (InitAccounts).
type AccountNames struct {
	Present bool
	Default string
	Names   []string
}
