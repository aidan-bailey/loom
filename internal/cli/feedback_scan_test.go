package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/core/rpc"
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

// scanToken is a GitHub-token-shaped string, built from parts.
var scanToken = "gh" + "p_" + strings.Repeat("a", 36)

const scanSID = "7f9c1e2a-3b4d-4e5f-8a6b-1c2d3e4f5a6b"

// isolateScan gives a scan test the host it expects: $USER is scanuser, git's
// global config holds email (when given) and the working directory is a
// scratch one outside every workspace. A test that wants a workspace's
// directory changes into it after serveDaemon.
func isolateScan(t *testing.T, email string) {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	body := ""
	if email != "" {
		body = "[user]\n\temail = " + email + "\n"
	}
	require.NoError(t, os.WriteFile(cfg, []byte(body), 0o600))
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("USER", "scanuser")
	t.Chdir(t.TempDir())
}

// runScanCmd runs `loom feedback scan args…` with stdin.
func runScanCmd(t *testing.T, stdin string, args ...string) (stdout string, code int, msg string) {
	t.Helper()
	root := newFeedbackCmd()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(append([]string{"scan"}, args...))
	err := root.Execute()
	assert.Empty(t, errOut.String(), "scan says what it has to on stdout and in its exit error")
	if err == nil {
		return out.String(), 0, ""
	}
	var exit *ExitError
	require.ErrorAs(t, err, &exit, "every failure is an ExitError: %v", err)
	return out.String(), exit.Code, exit.Msg
}

// draft writes text to a scratch file and returns its path.
func draft(t *testing.T, text string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "draft.md")
	require.NoError(t, os.WriteFile(p, []byte(text), 0o600))
	return p
}

func TestFeedbackScan_ExitsZeroWhenCleanAndOneWithAHit(t *testing.T) {
	isolateWorkspaces(t)
	isolateScan(t, "")
	serveDaemon(t, "true")

	out, code, msg := runScanCmd(t, "", draft(t, "Running `loom job run --after 3` in `<worktree>` said no such job.\nFeedback thread 3\n"))
	assert.Equal(t, 0, code)
	assert.Empty(t, out)
	assert.Empty(t, msg)

	out, code, msg = runScanCmd(t, "", draft(t, "token: "+scanToken+"\n"))
	assert.Equal(t, 1, code)
	assert.Equal(t, "1:8 token: "+scanToken+"\n", out)
	assert.Empty(t, msg, "the hits are the message")
}

func TestFeedbackScan_ReadsStandardInputForADash(t *testing.T) {
	isolateWorkspaces(t)
	isolateScan(t, "")
	registered(t, "ws-b", "")
	serveDaemon(t, "true")

	out, code, _ := runScanCmd(t, "see ws-b\n", "-")
	assert.Equal(t, 1, code)
	assert.Equal(t, "1:5 workspace: ws-b\n", out)
}

func TestFeedbackScan_EveryFailureExitsTwo(t *testing.T) {
	isolateWorkspaces(t)
	isolateScan(t, "")
	oversized := draft(t, strings.Repeat("x", scanMaxBytes+1))
	clean := draft(t, "clean\n")

	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"no file":             {nil, "accepts 1 arg"},
		"two files":           {[]string{clean, clean}, "accepts 1 arg"},
		"an unknown flag":     {[]string{clean, "--nope"}, "unknown flag: --nope"},
		"a missing file":      {[]string{filepath.Join(t.TempDir(), "gone.md")}, "no such file"},
		"an oversized draft":  {[]string{oversized}, "scan a shorter draft"},
		"a note for --thread": {[]string{clean, "--thread", "n4"}, "is a note no thread holds yet"},
		"a bad --thread":      {[]string{clean, "--thread", "x"}, "--thread"},
	} {
		t.Run(name, func(t *testing.T) {
			prev := Join
			t.Cleanup(func() { Join = prev })
			Join = func() (*rpc.Client, error) { t.Fatalf("joined the daemon before it had to"); return nil, nil }

			out, code, msg := runScanCmd(t, "", tc.args...)

			assert.Equal(t, 2, code)
			assert.Contains(t, msg, tc.want)
			assert.Empty(t, out)
		})
	}
}

func TestFeedbackScan_ADraftOfExactlyTheLimitIsScanned(t *testing.T) {
	isolateWorkspaces(t)
	isolateScan(t, "")
	serveDaemon(t, "true")

	out, code, msg := runScanCmd(t, "", draft(t, strings.Repeat("x", scanMaxBytes)))

	assert.Equal(t, 0, code, "scanMaxBytes bytes are within the bound: %s", msg)
	assert.Empty(t, out)
}

func TestFeedbackScan_GitThatCannotAnswerIsExitTwoAndPrintsNoHits(t *testing.T) {
	isolateWorkspaces(t)
	isolateScan(t, "")
	serveDaemon(t, "true")
	bad := filepath.Join(t.TempDir(), "gitconfig")
	require.NoError(t, os.WriteFile(bad, []byte("[user\n"), 0o600))
	t.Setenv("GIT_CONFIG_GLOBAL", bad)

	out, code, msg := runScanCmd(t, "", draft(t, "token: "+scanToken+"\n"))

	assert.Equal(t, 2, code, "git's own email is a term: without it the draft was not checked")
	assert.Contains(t, msg, "git config user.email")
	assert.Empty(t, out, "the token is not reported either")
}

func TestFeedbackScan_NoDaemonIsExitTwoAndPrintsNoHits(t *testing.T) {
	isolateWorkspaces(t)
	isolateScan(t, "")
	prev := Join
	Join = func() (*rpc.Client, error) { return nil, errors.New("no daemon for the test") }
	t.Cleanup(func() { Join = prev })

	out, code, msg := runScanCmd(t, "", draft(t, "token: "+scanToken+"\n"))

	assert.Equal(t, 2, code, "a draft that could not be checked is not a draft with hits")
	assert.Contains(t, msg, "no daemon for the test")
	assert.Empty(t, out, "the token is not reported: only the daemon knows the names, so nothing is said")
}

func TestFeedbackScan_AThreadTheDaemonDoesNotHoldIsExitTwo(t *testing.T) {
	isolateWorkspaces(t)
	isolateScan(t, "")
	serveDaemon(t, "true")

	_, code, msg := runScanCmd(t, "", draft(t, "clean\n"), "--thread", "9")

	assert.Equal(t, 2, code)
	assert.Contains(t, msg, "thread 9 doesn't exist")
}

func TestFeedbackScan_TheNamesTheDaemonKnowsAreHitsExceptTheOwnWorkspaces(t *testing.T) {
	isolateWorkspaces(t)
	isolateScan(t, "scan@example.org")
	repoA := gitRepoIn(t, filepath.Join(t.TempDir(), "repo-a"))
	repoB := gitRepoIn(t, filepath.Join(t.TempDir(), "repo-b"))
	registeredAt(t, "ws-a", repoA, pausedRecords)
	registeredAt(t, "ws-b", repoB, `[{"title":"other","status":3,"program":"true"}]`)
	serveDaemon(t, "true")
	t.Chdir(repoA)
	text := "ws-a and alpha are the own workspace's\n" +
		"ws-b runs other\n" +
		"scanuser mailed scan@example.org\n" +
		"b: " + repoB + "/core/x.go\n" +
		"a: " + repoA + "/core/x.go\n"

	out, code, _ := runScanCmd(t, "", draft(t, text))

	assert.Equal(t, 1, code)
	for _, want := range []string{
		"2:1 workspace: ws-b\n",
		"2:11 session: other\n",
		"3:1 identity: scanuser\n",
		"3:17 identity: scan@example.org\n",
		"4:4 path: " + repoB + "/core/x.go\n",
		"5:4 path: " + repoA + "/core/x.go\n",
	} {
		assert.Contains(t, out, want)
	}
	assert.NotRegexp(t, `(?m)^1:`, out, "the own workspace's name and its sessions' titles are public")
	assert.NotContains(t, out, "email:", "the git email is an identity hit, once")
}

func TestFeedbackScan_OutsideEveryWorkspaceEveryNameCounts(t *testing.T) {
	isolateWorkspaces(t)
	isolateScan(t, "")
	registered(t, "ws-a", pausedRecords)
	serveDaemon(t, "true")

	out, code, _ := runScanCmd(t, "", draft(t, "ws-a alpha\n"))

	assert.Equal(t, 1, code)
	assert.Equal(t, "1:1 workspace: ws-a\n1:6 session: alpha\n", out)
}

func TestFeedbackScan_ATitleOnlyTheStoragePreservesIsStillAHit(t *testing.T) {
	isolateWorkspaces(t)
	isolateScan(t, "")
	// The second record's schema version is newer than this build's: the
	// storage keeps it unread, so its title is in the view's PreservedTitles
	// and in no session's view.
	registered(t, "ws-b", `[{"title":"other","status":3,"program":"true"},{"schema_version":9999,"title":"future build"}]`)
	serveDaemon(t, "true")

	out, code, _ := runScanCmd(t, "", draft(t, "the future build failed\nand other too\n"))

	assert.Equal(t, 1, code)
	assert.Equal(t, "1:5 session: future build\n2:5 session: other\n", out)
}

// seedScanThread writes thread 1 before the daemon boots: n1 from the own
// workspace ws-a on main, n2 from a workspace and a session the daemon no
// longer serves.
func seedScanThread(t *testing.T) {
	t.Helper()
	at := time.Now().Add(-time.Hour).Round(0)
	in := feedback.Empty()
	n1 := in.Add(feedback.Note{Kind: feedback.KindBug, Title: "t1", Text: "x", At: at,
		From: feedback.Session{ID: 1, Title: "builder", Workspace: "ws-a", Branch: "main"}})
	th, err := in.OpenThread(n1, "a problem")
	require.NoError(t, err)
	n2 := in.Add(feedback.Note{Kind: feedback.KindBug, Title: "t2", Text: "y", At: at.Add(time.Minute),
		From: feedback.Session{ID: 2, Title: "deploy nightly", Workspace: "gone-ws", Branch: "me/secret-branch",
			ClaudeSession: scanSID, Transcript: "/home/u/.claude/projects/p/s.jsonl"}})
	require.NoError(t, in.Attach(n2, th))
	writeInbox(t, in)
}

func TestFeedbackScan_TheThreadFlagAddsTheThreadsNotes(t *testing.T) {
	isolateWorkspaces(t)
	isolateScan(t, "")
	repoA := gitRepoIn(t, filepath.Join(t.TempDir(), "repo-a"))
	registeredAt(t, "ws-a", repoA, "")
	seedScanThread(t)
	serveDaemon(t, "true")
	t.Chdir(repoA)
	file := draft(t, "builder fixed it\n"+
		"deploy nightly on gone-ws\n"+
		"branch me/secret-branch\n"+
		"id "+scanSID+" at /home/u/.claude/projects/p/s.jsonl\n"+
		"main is fine\n")

	out, code, _ := runScanCmd(t, "", file, "--thread", "1")
	assert.Equal(t, 1, code)
	assert.Equal(t, "2:1 thread: deploy nightly\n"+
		"2:19 thread: gone-ws\n"+
		"3:8 thread: me/secret-branch\n"+
		"4:4 thread: "+scanSID+"\n"+
		"4:44 thread: /home/u/.claude/projects/p/s.jsonl\n", out,
		"the own workspace's note and the default branch are public, and each span is one hit")

	out, code, _ = runScanCmd(t, "", file)
	assert.Equal(t, 1, code)
	assert.Equal(t, "4:4 uuid: "+scanSID+"\n4:44 path: /home/u/.claude/projects/p/s.jsonl\n", out,
		"without --thread the notes' names are not terms")
}

func TestFeedbackScan_AllowDropsTheWordAndSaysSo(t *testing.T) {
	isolateWorkspaces(t)
	isolateScan(t, "")
	registered(t, "ws-b", "")
	serveDaemon(t, "true")
	file := draft(t, "ws-b\nscanuser\n")

	out, code, _ := runScanCmd(t, "", file, "--allow", "ws-b")
	assert.Equal(t, 1, code)
	assert.Equal(t, "2:1 identity: scanuser\nallowed 1:1 workspace: ws-b\n", out)

	out, code, _ = runScanCmd(t, "", file, "--allow", "WS-B", "--allow", "scanuser")
	assert.Equal(t, 0, code, "nothing left but what the owner named")
	assert.Equal(t, "allowed 1:1 workspace: ws-b\nallowed 2:1 identity: scanuser\n", out)
}
