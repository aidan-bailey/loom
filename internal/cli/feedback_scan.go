package cli

import (
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/feedback"
)

// scanMinTitle is the fewest characters a session title needs to be a term:
// a shorter one matches ordinary words.
const scanMinTitle = 4

// scanGlobalLabel is the workspace a note from the global context names
// (core's Workspace.Label): it names no workspace, so it is no term.
const scanGlobalLabel = "global"

// scanInputs is what `loom feedback scan` found out about the host: the
// pure input of buildScanTerms.
type scanInputs struct {
	Home, User, Host, Email string
	// Cwd is the command's working directory.
	Cwd string
	// Workspaces are the workspaces the daemon serves.
	Workspaces []scanWorkspace
	// Accounts are the registered accounts, the implicit default included.
	Accounts []scanAccount
	// Notes are the notes of the thread `--thread` names, none without it.
	Notes []feedback.Note
}

// scanWorkspace is a served workspace and the titles of its sessions.
type scanWorkspace struct {
	Name, RepoPath string
	Titles         []string
}

// scanAccount is an account's name and the email it is logged in as, ""
// when unknown.
type scanAccount struct{ Name, Email string }

// buildScanTerms is the terms feedback.Scan looks for. The own workspace
// (ownWorkspace) is public: its name, its repository's base name, its
// sessions' titles and its notes' names stay out of every list but Private,
// which holds every repository's path, since a path inside the own
// repository carries the home directory (spec §3.1 writes it repo-relative).
func buildScanTerms(in scanInputs) feedback.ScanTerms {
	var t feedback.ScanTerms
	t.Private = appendUnique(t.Private, in.Home, config.CanonicalPath(in.Home))
	own := ownWorkspace(in)
	for i, w := range in.Workspaces {
		if w.RepoPath != "" {
			t.Private = appendUnique(t.Private, w.RepoPath, config.CanonicalPath(w.RepoPath))
		}
		if i == own {
			continue
		}
		t.Workspaces = appendUnique(t.Workspaces, w.Name)
		if b := filepath.Base(w.RepoPath); w.RepoPath != "" && b != "." && b != string(filepath.Separator) {
			t.Workspaces = appendUnique(t.Workspaces, b)
		}
		for _, title := range w.Titles {
			if longEnough(title) {
				t.Sessions = appendUnique(t.Sessions, strings.TrimSpace(title))
			}
		}
	}

	t.Identity = appendUnique(t.Identity, in.User, in.Host)
	if short, _, ok := strings.Cut(in.Host, "."); ok {
		t.Identity = appendUnique(t.Identity, short)
	}
	t.Identity = appendUnique(t.Identity, in.Email)
	for _, a := range in.Accounts {
		if a.Name != account.DefaultName {
			t.Identity = appendUnique(t.Identity, a.Name)
		}
		t.Identity = appendUnique(t.Identity, a.Email)
	}

	ownName := ""
	if own >= 0 {
		ownName = in.Workspaces[own].Name
	}
	for _, n := range in.Notes {
		t.Thread = appendUnique(t.Thread, n.From.ClaudeSession, n.From.Transcript)
		if n.From.Branch != "main" && n.From.Branch != "master" {
			t.Thread = appendUnique(t.Thread, n.From.Branch)
		}
		if own >= 0 && n.From.Workspace == ownName {
			continue
		}
		if longEnough(n.From.Title) {
			t.ThreadNames = appendUnique(t.ThreadNames, strings.TrimSpace(n.From.Title))
		}
		if n.From.Workspace != scanGlobalLabel {
			t.ThreadNames = appendUnique(t.ThreadNames, n.From.Workspace)
		}
	}
	return t
}

// ownWorkspace is the index of the served workspace whose repository holds
// the working directory, the deepest when repositories nest, and -1 when
// none does. The search is config.WorkspaceRegistry.FindByPath's (canonical
// paths, whole components, longest root) over a registry built from the
// served workspaces; the workspace it finds is mapped back to its index.
func ownWorkspace(in scanInputs) int {
	if in.Cwd == "" {
		return -1
	}
	var reg config.WorkspaceRegistry
	var index []int
	for i, w := range in.Workspaces {
		if w.RepoPath != "" {
			reg.Workspaces = append(reg.Workspaces, config.Workspace{Name: w.Name, Path: w.RepoPath})
			index = append(index, i)
		}
	}
	found := reg.FindByPath(in.Cwd)
	for j := range reg.Workspaces {
		if found == &reg.Workspaces[j] {
			return index[j]
		}
	}
	return -1
}

// longEnough reports whether a title has scanMinTitle characters once
// trimmed.
func longEnough(title string) bool {
	return utf8.RuneCountInString(strings.TrimSpace(title)) >= scanMinTitle
}

// appendUnique adds each non-empty s of add that list lacks.
func appendUnique(list []string, add ...string) []string {
	for _, s := range add {
		if s != "" && !slices.Contains(list, s) {
			list = append(list, s)
		}
	}
	return list
}
