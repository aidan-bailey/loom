package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/feedback"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildScanTerms_PrivateHoldsEverySpellingOfEveryRepositoryAndTheHome(t *testing.T) {
	realDir := t.TempDir()
	link := filepath.Join(t.TempDir(), "lnk")
	require.NoError(t, os.Symlink(realDir, link))
	canon, err := filepath.EvalSymlinks(realDir)
	require.NoError(t, err)
	home := t.TempDir()

	terms := buildScanTerms(scanInputs{
		Home:       home,
		Cwd:        filepath.Join(realDir, "sub"),
		Workspaces: []scanWorkspace{{Name: "w", RepoPath: link}, {Name: "", RepoPath: ""}},
	})

	assert.Contains(t, terms.Private, link, "the raw spelling")
	assert.Contains(t, terms.Private, canon, "the canonical spelling")
	assert.Contains(t, terms.Private, home)
	assert.NotContains(t, terms.Private, "")
	assert.Empty(t, terms.Workspaces, "w is the own workspace: its name is public, its path is not")
}

func TestBuildScanTerms_TheOwnWorkspaceIsTheDeepestRepositoryHoldingTheDirectory(t *testing.T) {
	tmp := t.TempDir()
	outer := filepath.Join(tmp, "outer")
	inner := filepath.Join(outer, "inner")
	sibling := filepath.Join(tmp, "outer2")
	for _, d := range []string{filepath.Join(inner, "src"), filepath.Join(sibling, "x")} {
		require.NoError(t, os.MkdirAll(d, 0o755))
	}
	ws := []scanWorkspace{
		{Name: "outer-ws", RepoPath: outer, Titles: []string{"outer title"}},
		{Name: "inner-ws", RepoPath: inner, Titles: []string{"inner title"}},
		{Name: "sib-ws", RepoPath: sibling, Titles: []string{"sibling title"}},
	}

	terms := buildScanTerms(scanInputs{Cwd: filepath.Join(inner, "src"), Workspaces: ws})
	assert.Equal(t, []string{"outer-ws", "outer", "sib-ws", "outer2"}, terms.Workspaces, "inner is the own workspace, not outer")
	assert.Equal(t, []string{"outer title", "sibling title"}, terms.Sessions)

	terms = buildScanTerms(scanInputs{Cwd: outer, Workspaces: ws[:1:1]})
	assert.Empty(t, terms.Workspaces, "the repository's own directory is inside it")

	terms = buildScanTerms(scanInputs{Cwd: filepath.Join(sibling, "x"), Workspaces: []scanWorkspace{ws[0], ws[2]}})
	assert.Equal(t, []string{"outer-ws", "outer"}, terms.Workspaces, "sib-ws holds its own directory, and outer does not")

	terms = buildScanTerms(scanInputs{Cwd: filepath.Join(sibling, "x"), Workspaces: ws[:1:1]})
	assert.Equal(t, []string{"outer-ws", "outer"}, terms.Workspaces, "outer2/x is not inside outer: only a whole component holds a directory")
	assert.Equal(t, []string{"outer title"}, terms.Sessions, "no own workspace, so nothing is left out")

	terms = buildScanTerms(scanInputs{Cwd: tmp, Workspaces: ws})
	assert.Equal(t, []string{"outer-ws", "outer", "inner-ws", "inner", "sib-ws", "outer2"}, terms.Workspaces, "outside every workspace every name counts")
}

func TestBuildScanTerms_SessionTitlesAreFourCharactersOrMore(t *testing.T) {
	terms := buildScanTerms(scanInputs{Workspaces: []scanWorkspace{
		{Name: "w", RepoPath: "/srv/w", Titles: []string{"abc", "abcd", "  abc ", "日本語", "日本語です", "abcd"}},
	}})
	assert.Equal(t, []string{"abcd", "日本語です"}, terms.Sessions, "three characters are too few whatever their bytes, a title is listed once")
}

func TestBuildScanTerms_IdentityHoldsTheUserTheHostTheEmailsAndTheAccounts(t *testing.T) {
	terms := buildScanTerms(scanInputs{
		User: "alice", Host: "fresco.lan", Email: "a@x.org",
		Accounts: []scanAccount{{Name: "default", Email: "d@x.org"}, {Name: "work", Email: "w@x.org"}, {Name: "spare"}},
	})
	assert.Equal(t, []string{"alice", "fresco.lan", "fresco", "a@x.org", "d@x.org", "work", "w@x.org", "spare"}, terms.Identity,
		"the host's short name too; the implicit account's email but not its name")
}

func TestBuildScanTerms_ThreadTermsSkipTheDefaultBranchesAndTheOwnWorkspacesNames(t *testing.T) {
	notes := []feedback.Note{
		{ID: 1, From: feedback.Session{Title: "builder", Workspace: "ws-a", Branch: "main", ClaudeSession: "s1", Transcript: "/t/1"}},
		{ID: 2, From: feedback.Session{Title: "abc", Workspace: "gone", Branch: "fix/x", ClaudeSession: "s2"}},
		{ID: 3, From: feedback.Session{Title: "deploy", Workspace: "gone", Branch: "master", ClaudeSession: "s2"}},
		{ID: 4, From: feedback.Session{Title: "nightly job", Workspace: "global", Branch: "main"}},
	}
	own := []scanWorkspace{{Name: "ws-a", RepoPath: "/srv/a"}}
	cwd := "/srv/a/x"

	terms := buildScanTerms(scanInputs{Cwd: cwd, Workspaces: own, Notes: notes})
	assert.Equal(t, []string{"s1", "/t/1", "s2", "fix/x"}, terms.Thread, "no default branch, no repeat")
	assert.Equal(t, []string{"gone", "deploy", "nightly job"}, terms.ThreadNames,
		"ws-a is the own workspace; abc is three characters; a note from the global context has the title but no workspace name")

	terms = buildScanTerms(scanInputs{Cwd: "/elsewhere", Workspaces: own, Notes: notes})
	assert.Equal(t, []string{"builder", "ws-a", "gone", "deploy", "nightly job"}, terms.ThreadNames,
		"outside every workspace every note's names count, but `global` names no workspace")
}
