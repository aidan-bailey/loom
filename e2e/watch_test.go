//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/internal/devsandbox"
	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The issue watcher against real processes: a daemon on its own tmux
// server, a stub gh on PATH, and the toy repository's bare origin. The
// watcher's tick is 60 s, so each wait allows watchWait.
const watchWait = 150 * time.Second

// watchConfig is loom-watch.json as it is committed to origin/main. The
// branch prefix of the sandbox's workspace is "dev/", so the title
// "heal 7" gets the branch "dev/heal-7".
const watchConfig = `{
  "version": 1,
  "label": "watch-me",
  "escalate_label": "needs-human",
  "title": "heal {issue}",
  "cap": 3,
  "session": { "prompt": ".loom-watch/start.md" }
}
`

const watchPrompt = "Fix issue {issue}: {issue_title}\n"

// watchRow is the part of a `loom session list --json` row the test reads.
type watchRow struct {
	Title    string `json:"title"`
	Status   string `json:"status"`
	Branch   string `json:"branch"`
	Worktree string `json:"worktree"`
	Issue    int    `json:"issue"`
}

// watchLoom runs `loom <args>` in the sandbox and returns its combined
// output. Unlike runLoom it never fails the test, so it can run inside
// require.Eventually's condition.
func watchLoom(sb *devsandbox.Sandbox, args ...string) (string, error) {
	c := sb.Cmd(append([]string{sb.LoomBin()}, args...)...)
	var out bytes.Buffer
	c.Stdout, c.Stderr = &out, &out
	if err := c.Start(); err != nil {
		return "", err
	}
	timer := time.AfterFunc(uiTimeout, func() { _ = c.Process.Kill() })
	defer timer.Stop()
	err := c.Wait()
	return out.String(), err
}

// watchSessions lists the workspace's sessions through the CLI.
func watchSessions(sb *devsandbox.Sandbox) ([]watchRow, error) {
	out, err := watchLoom(sb, "session", "list", "-w", devsandbox.WorkspaceName, "--json")
	if err != nil {
		return nil, fmt.Errorf("session list: %w: %s", err, out)
	}
	var rows []watchRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		return nil, fmt.Errorf("session list json: %w: %s", err, out)
	}
	return rows, nil
}

// watchSession is the session titled title, and whether the list has it.
// A list that cannot be read reports the session as absent.
func watchSession(sb *devsandbox.Sandbox, title string) (watchRow, bool) {
	rows, err := watchSessions(sb)
	if err != nil {
		return watchRow{}, false
	}
	for _, r := range rows {
		if r.Title == title {
			return r, true
		}
	}
	return watchRow{}, false
}

// publish writes v as JSON to the stub's file name, by rename, so a stub
// reading it never sees half a file.
func publish(t *testing.T, dir, name string, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	tmp := filepath.Join(dir, name+".tmp")
	require.NoError(t, os.WriteFile(tmp, data, 0o644))
	require.NoError(t, os.Rename(tmp, filepath.Join(dir, name)))
}

// watchIssue is an open issue carrying the watcher's label, in gh's
// `issue list --json` shape.
func watchIssue(n int) map[string]any {
	return map[string]any{
		"number":   n,
		"title":    fmt.Sprintf("Schema drift %d", n),
		"body":     "The schema drifted.",
		"url":      fmt.Sprintf("https://github.com/e2e/watch/issues/%d", n),
		"labels":   []map[string]any{{"name": "watch-me"}},
		"comments": []any{},
	}
}

// watchPR is a pull request for issue 7 on the branch the watcher's
// session for 7 gets, in gh's `pr list --json` shape.
func watchPR(number int, state, author string) map[string]any {
	return map[string]any{
		"number":                  number,
		"state":                   state,
		"headRefName":             "dev/heal-7",
		"headRefOid":              strings.Repeat("a", 40),
		"baseRefOid":              strings.Repeat("b", 40),
		"baseRefName":             "main",
		"mergeable":               "UNKNOWN",
		"author":                  map[string]any{"login": author},
		"isCrossRepository":       false,
		"closingIssuesReferences": []map[string]any{{"number": 7, "repository": map[string]any{"name": "watch", "owner": map[string]any{"login": "e2e"}}}},
		"statusCheckRollup":       []any{},
		"body":                    "Closes #7",
		"url":                     fmt.Sprintf("https://github.com/e2e/watch/pull/%d", number),
	}
}

// A labelled issue starts a session cut from origin/main (local main is
// ahead of it); a merged PR another login opened is not the watcher's, so
// it leaves the session alone; the watcher's own merged PR kills the
// session and deletes its branch, and only that issue's session.
func TestE2E_Watch_StartsAndKillsByIssue(t *testing.T) {
	sb := newSandbox(t, "")
	repo := sb.RepoDir()

	// The config and its prompt are on origin/main; a later commit is local only.
	require.NoError(t, os.MkdirAll(filepath.Join(repo, ".loom-watch"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "loom-watch.json"), []byte(watchConfig), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, ".loom-watch", "start.md"), []byte(watchPrompt), 0o644))
	gitOut(t, repo, "add", "loom-watch.json", ".loom-watch/start.md")
	gitOut(t, repo, "commit", "-q", "--no-verify", "-m", "chore: watcher config")
	gitOut(t, repo, "push", "-q", "--no-verify", "origin", "main")
	originSHA := strings.TrimSpace(gitOut(t, repo, "rev-parse", "origin/main"))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "local-only.txt"), []byte("not pushed\n"), 0o644))
	gitOut(t, repo, "add", "local-only.txt")
	gitOut(t, repo, "commit", "-q", "--no-verify", "-m", "chore: a commit nobody pushed")
	require.NotEqual(t, originSHA, strings.TrimSpace(gitOut(t, repo, "rev-parse", "main")), "fixture: local main is ahead of origin/main")

	// The stub gh goes first on PATH before the first loom command: the
	// daemon inherits the environment of the command that starts it.
	stubDir := filepath.Join(sb.Dir, "ghstub")
	require.NoError(t, os.MkdirAll(filepath.Join(stubDir, "bin"), 0o755))
	script, err := os.ReadFile(filepath.Join("testdata", "gh-watch-stub.sh"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(stubDir, "bin", "gh"), script, 0o755))
	t.Setenv("PATH", filepath.Join(stubDir, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("WATCH_STUB_DIR", stubDir)
	publish(t, stubDir, "issues.json", []any{watchIssue(7)})
	publish(t, stubDir, "prs.json", []any{})
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		calls, _ := os.ReadFile(filepath.Join(stubDir, "calls.log"))
		t.Logf("gh calls:\n%s", calls)
		status, _ := watchLoom(sb, "watch", "status", "-w", devsandbox.WorkspaceName)
		t.Logf("loom watch status:\n%s", status)
	})

	out, err := watchLoom(sb, "watch", "on", "-w", devsandbox.WorkspaceName)
	require.NoError(t, err, out)
	assert.Contains(t, out, "watch on: "+devsandbox.WorkspaceName)

	// A labelled issue starts a session on a branch cut from origin/main.
	var seven watchRow
	require.Eventually(t, func() bool {
		var ok bool
		seven, ok = watchSession(sb, "heal 7")
		return ok
	}, watchWait, 3*time.Second, "the watcher started a session for issue 7")
	assert.Equal(t, 7, seven.Issue)
	assert.Equal(t, "dev/heal-7", seven.Branch)
	// The session is listed before its tmux session exists.
	require.Eventually(t, func() bool { return hasSession(sb, "heal 7") }, 30*time.Second, time.Second, "its agent runs in tmux")
	assert.Equal(t, originSHA, strings.TrimSpace(gitOut(t, repo, "rev-parse", "refs/heads/"+seven.Branch)), "the branch is origin/main's commit, not the local main's")
	assert.NoFileExists(t, filepath.Join(seven.Worktree, "local-only.txt"))
	assert.FileExists(t, filepath.Join(seven.Worktree, "loom-watch.json"))

	// A merged PR another login opened is not the watcher's. The second
	// issue, which appears with it, is started by the tick that read the
	// PR, so once its session exists the PR has been seen and judged.
	publish(t, stubDir, "issues.json", []any{watchIssue(7), watchIssue(8)})
	publish(t, stubDir, "prs.json", []any{watchPR(100, "MERGED", "someone-else")})
	require.Eventually(t, func() bool {
		_, ok := watchSession(sb, "heal 8")
		return ok
	}, watchWait, 3*time.Second, "the watcher started a session for issue 8")
	require.Never(t, func() bool {
		rows, err := watchSessions(sb)
		if err != nil {
			return false
		}
		for _, r := range rows {
			if r.Title == "heal 7" {
				return false
			}
		}
		return true
	}, 8*time.Second, 2*time.Second, "a merged PR another login opened must not kill issue 7's session")
	assert.True(t, hasSession(sb, "heal 7"))

	// The watcher's own merged PR kills issue 7's session and its branch.
	publish(t, stubDir, "prs.json", []any{watchPR(100, "MERGED", "someone-else"), watchPR(101, "MERGED", "watcher-bot")})
	require.Eventually(t, func() bool {
		_, ok := watchSession(sb, "heal 7")
		return !ok && !hasSession(sb, "heal 7")
	}, watchWait, 3*time.Second, "the watcher killed issue 7's session after its PR merged")
	require.Eventually(t, func() bool {
		out, err := internalexec.GitCommand(context.Background(), repo, "for-each-ref", "refs/heads/dev/heal-7").Output()
		return err == nil && strings.TrimSpace(string(out)) == ""
	}, 30*time.Second, time.Second, "the kill deleted the branch loom created")
	_, ok := watchSession(sb, "heal 8")
	assert.True(t, ok, "issue 8's session is not the merged PR's")
	require.Eventually(t, func() bool { return hasSession(sb, "heal 8") }, 30*time.Second, time.Second, "issue 8's agent runs in tmux")

	// The watcher is on, healthy, and reached the stub rather than a real gh.
	statusOut, err := watchLoom(sb, "watch", "status", "-w", devsandbox.WorkspaceName, "--json")
	require.NoError(t, err, statusOut)
	var view struct {
		On          bool   `json:"on"`
		Err         string `json:"err"`
		Escalations []any  `json:"escalations"`
	}
	require.NoError(t, json.Unmarshal([]byte(statusOut), &view), statusOut)
	assert.True(t, view.On)
	assert.Empty(t, view.Err)
	assert.Empty(t, view.Escalations)
	calls, err := os.ReadFile(filepath.Join(stubDir, "calls.log"))
	require.NoError(t, err)
	assert.Contains(t, string(calls), "pr list --author @me", "the watcher's PR read went to the stub")
}
