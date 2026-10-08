package core

import "github.com/aidan-bailey/loom/config"

// WorkspaceID names a workspace the model serves. The model assigns it the
// first time it reports the workspace and never reuses it; a workspace
// keeps its ID while the model serves it, which is until the model stops
// (a tab closed and reopened shows the same workspace). 0 means none.
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
