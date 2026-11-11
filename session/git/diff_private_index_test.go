package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// execRecorder runs every command for real and records each one's
// arguments and environment, so a test can count the probe's subprocesses
// and check what each ran with.
type execRecorder struct {
	mu   sync.Mutex
	cmds []*exec.Cmd
}

func (r *execRecorder) record(c *exec.Cmd) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cmds = append(r.cmds, c)
}

func (r *execRecorder) Run(c *exec.Cmd) error { r.record(c); return internalexec.Default{}.Run(c) }
func (r *execRecorder) Output(c *exec.Cmd) ([]byte, error) {
	r.record(c)
	return internalexec.Default{}.Output(c)
}
func (r *execRecorder) CombinedOutput(c *exec.Cmd) ([]byte, error) {
	r.record(c)
	return internalexec.Default{}.CombinedOutput(c)
}

// take returns the commands recorded since the last take, and forgets them.
func (r *execRecorder) take() []*exec.Cmd {
	r.mu.Lock()
	defer r.mu.Unlock()
	cmds := r.cmds
	r.cmds = nil
	return cmds
}

func hasEnv(c *exec.Cmd, kv string) bool {
	for _, e := range c.Env {
		if e == kv {
			return true
		}
	}
	return false
}

// probeFixture is a linked worktree of a fresh repository, with the
// GitWorktree a session would hold for it.
type probeFixture struct {
	repo, tree, gitDir, base string
	runner                   *execRecorder
	g                        *GitWorktree
}

func newProbeFixture(t *testing.T) *probeFixture {
	t.Helper()
	_, repo, tree, branch := setupTestRepoWithWorktree(t)
	writeFile(t, tree, ".gitignore", "*.log\n")
	writeFile(t, tree, "tracked.txt", "one\n")
	runGit(t, tree, "add", ".gitignore", "tracked.txt")
	runGit(t, tree, "commit", "-qm", "base")
	base, err := exec.Command("git", "-C", tree, "rev-parse", "HEAD").Output()
	require.NoError(t, err)
	gitDir, ok := readGitFile(tree)
	require.True(t, ok, "a linked worktree's .git names its admin dir")
	r := &execRecorder{}
	g := NewGitWorktreeFromStorageWithRunner(repo, tree, "s", branch, strings.TrimSpace(string(base)), false, "", r)
	return &probeFixture{repo: repo, tree: tree, gitDir: gitDir, base: strings.TrimSpace(string(base)), runner: r, g: g}
}

// expireUntrackedCache makes the next probe look for untracked files again.
func (f *probeFixture) expireUntrackedCache() {
	f.g.untrackedCacheMu.Lock()
	f.g.untrackedCheckedAt = time.Now().Add(-2 * untrackedCacheTTL)
	f.g.untrackedCacheMu.Unlock()
}

// probeAll runs every probe and returns what each shows: Diff's content,
// DiffShortStat's added count, and the paths Patch's files name.
func (f *probeFixture) probeAll(t *testing.T) (string, int, []string) {
	t.Helper()
	d := f.g.Diff()
	require.NoError(t, d.Error)
	s := f.g.DiffShortStat()
	require.NoError(t, s.Error)
	text, err := f.g.Patch()
	require.NoError(t, err)
	var paths []string
	for _, fp := range SplitPatch(text) {
		paths = append(paths, fp.Path)
	}
	return d.Content, s.Added, paths
}

// TestDiffProbe_LeavesTheRealIndexAlone: the probe works on a private copy,
// so the worktree's own index stays byte for byte what the agent left, and
// no index.lock is ever taken (feedback thread 17).
func TestDiffProbe_LeavesTheRealIndexAlone(t *testing.T) {
	f := newProbeFixture(t)
	writeFile(t, f.tree, "new.txt", "fresh\n")
	writeFile(t, f.tree, "tracked.txt", "two\n")
	realIndex := filepath.Join(f.gitDir, "index")
	before, err := os.ReadFile(realIndex)
	require.NoError(t, err)
	beforeInfo, err := os.Stat(realIndex)
	require.NoError(t, err)

	content, added, paths := f.probeAll(t)
	f.expireUntrackedCache()
	f.probeAll(t)

	assert.Contains(t, content, "+++ b/new.txt", "the untracked file shows in the diff")
	assert.Equal(t, 2, added, "the new file's line and tracked.txt's changed line")
	assert.ElementsMatch(t, []string{"new.txt", "tracked.txt"}, paths)

	after, err := os.ReadFile(realIndex)
	require.NoError(t, err)
	assert.Equal(t, before, after, "the real index is unchanged")
	afterInfo, err := os.Stat(realIndex)
	require.NoError(t, err)
	assert.True(t, os.SameFile(beforeInfo, afterInfo), "the real index was never replaced")
	assert.NoFileExists(t, realIndex+".lock")

	for _, c := range f.runner.take() {
		assert.True(t, hasEnv(c, "GIT_OPTIONAL_LOCKS=0"), "%q runs without optional locks", c.Args)
		assert.True(t, hasEnv(c, "GIT_INDEX_FILE="+filepath.Join(f.gitDir, privateIndexName)), "%q runs on the private index", c.Args)
	}
}

// TestDiffProbe_WorksWhileAnAgentHoldsTheIndexLock: an agent's merge holds
// index.lock; the probe neither fails on it nor touches it.
func TestDiffProbe_WorksWhileAnAgentHoldsTheIndexLock(t *testing.T) {
	f := newProbeFixture(t)
	writeFile(t, f.tree, "new.txt", "fresh\n")
	lock := filepath.Join(f.gitDir, "index.lock")
	require.NoError(t, os.WriteFile(lock, []byte("merge in progress"), 0o644))

	content, added, paths := f.probeAll(t)

	assert.Contains(t, content, "+++ b/new.txt")
	assert.Equal(t, 1, added)
	assert.Equal(t, []string{"new.txt"}, paths)
	held, err := os.ReadFile(lock)
	require.NoError(t, err, "the agent's lock is left in place")
	assert.Equal(t, "merge in progress", string(held))
}

// TestDiffProbe_UntrackedIgnoredAndStagedFiles: untracked files come and
// go, an ignored file never shows, and a file the real index stages shows
// as a new file, as before.
func TestDiffProbe_UntrackedIgnoredAndStagedFiles(t *testing.T) {
	f := newProbeFixture(t)
	writeFile(t, f.tree, "scratch.txt", "temp\n")
	writeFile(t, f.tree, "build.log", "noise\n")
	writeFile(t, f.tree, "staged.txt", "kept\n")
	runGit(t, f.tree, "add", "staged.txt")

	_, _, paths := f.probeAll(t)
	assert.ElementsMatch(t, []string{"scratch.txt", "staged.txt"}, paths, "untracked and staged files show; the ignored one doesn't")
	files := splitWorktree(t, f.tree, f.base)
	assert.Equal(t, StatusAdded, fileAt(t, files, "staged.txt").Status)
	assert.Equal(t, StatusAdded, fileAt(t, files, "scratch.txt").Status)

	require.NoError(t, os.Remove(filepath.Join(f.tree, "scratch.txt")))
	f.expireUntrackedCache()
	_, _, paths = f.probeAll(t)
	assert.Equal(t, []string{"staged.txt"}, paths, "a deleted untracked file leaves the diff")
}

// TestDiffProbe_FollowsTheRealIndex: the private copy is refreshed when the
// agent changes its index, so what the index tracks shows on the next probe.
func TestDiffProbe_FollowsTheRealIndex(t *testing.T) {
	f := newProbeFixture(t)
	writeFile(t, f.tree, "keep.log", "wanted\n")
	_, _, paths := f.probeAll(t)
	assert.Empty(t, paths, "an ignored file stays out")

	runGit(t, f.tree, "add", "-f", "keep.log")
	_, _, paths = f.probeAll(t)
	assert.Equal(t, []string{"keep.log"}, paths, "a file the agent force-adds shows")

	runGit(t, f.tree, "commit", "-qm", "keep")
	_, _, paths = f.probeAll(t)
	assert.Equal(t, []string{"keep.log"}, paths, "a committed file shows against the base")

	runGit(t, f.tree, "rm", "-q", "--cached", "keep.log")
	runGit(t, f.tree, "commit", "-qm", "unkeep")
	_, _, paths = f.probeAll(t)
	assert.Empty(t, paths, "once the index drops it, the ignored file leaves the diff")
}

// TestDiffProbe_WorksBeforeTheWorktreeHasAnIndex: with no real index yet,
// the probe starts from an empty private one.
func TestDiffProbe_WorksBeforeTheWorktreeHasAnIndex(t *testing.T) {
	f := newProbeFixture(t)
	require.NoError(t, os.Remove(filepath.Join(f.gitDir, "index")))
	writeFile(t, f.tree, "new.txt", "fresh\n")

	_, _, paths := f.probeAll(t)

	assert.Equal(t, []string{"new.txt"}, paths, "files the base holds unchanged show nothing; the new one shows")
	assert.NoFileExists(t, filepath.Join(f.gitDir, "index"), "the probe never creates the real index")
}

// TestDiffProbe_SteadyStateCost: once the private index is current and holds
// every untracked file, a probe runs one subprocess, the diff, and copies
// nothing.
func TestDiffProbe_SteadyStateCost(t *testing.T) {
	f := newProbeFixture(t)
	writeFile(t, f.tree, "new.txt", "fresh\n")
	private := filepath.Join(f.gitDir, privateIndexName)

	// Warm up: the first probe copies the index, lists the untracked file
	// and adds it; a list that found files is never cached, so the second
	// lists again, and finds nothing new.
	require.NoError(t, f.g.DiffShortStat().Error)
	assert.Len(t, f.runner.take(), 3, "ls-files, add -N and the diff")
	require.NoError(t, f.g.DiffShortStat().Error)
	assert.Len(t, f.runner.take(), 2, "ls-files again, and the diff")
	warm, err := os.Stat(private)
	require.NoError(t, err)

	require.NoError(t, f.g.DiffShortStat().Error)
	cmds := f.runner.take()
	require.Len(t, cmds, 1, "a steady-state probe runs the diff alone")
	assert.Contains(t, strings.Join(cmds[0].Args, " "), "diff --shortstat")
	now, err := os.Stat(private)
	require.NoError(t, err)
	assert.True(t, os.SameFile(warm, now) && warm.ModTime().Equal(now.ModTime()), "a steady-state probe copies nothing")

	// After the untracked cache expires, one ls-files joins the diff.
	f.expireUntrackedCache()
	require.NoError(t, f.g.DiffShortStat().Error)
	assert.Len(t, f.runner.take(), 2, "ls-files and the diff")
}

// TestWorkspaceTerminalDiffs_TakeNoOptionalLocks: the workspace terminal's
// diffs run in the user's own checkout and must not take its index.lock
// to refresh stat info.
func TestWorkspaceTerminalDiffs_TakeNoOptionalLocks(t *testing.T) {
	repo, _ := patchRepo(t, map[string]string{"a.txt": "one\n"})
	writeFile(t, repo, "a.txt", "two\n")
	r := &execRecorder{}

	d := DiffUncommitted(repo, r)
	require.NoError(t, d.Error)
	s := DiffUncommittedShortStat(repo, r)
	require.NoError(t, s.Error)

	assert.Equal(t, 1, s.Added)
	cmds := r.take()
	require.Len(t, cmds, 2)
	for _, c := range cmds {
		assert.True(t, hasEnv(c, "GIT_OPTIONAL_LOCKS=0"), "%q runs without optional locks", c.Args)
	}
}

// TestNoAddIntentToAddOutsideTheProbe: `add -N` writes whichever index it
// runs on, so in this package it runs only through runProbe, which points
// it at the private index (feedback thread 17).
func TestNoAddIntentToAddOutsideTheProbe(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	addN := regexp.MustCompile(`"add",\s*"-N"`)
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		require.NoError(t, err)
		for i, line := range strings.Split(string(src), "\n") {
			if addN.MatchString(line) && !strings.Contains(line, "runProbe(") {
				t.Errorf("%s:%d runs add -N outside runProbe: %s", name, i+1, strings.TrimSpace(line))
			}
		}
	}
}

// TestDiffProbe_RecopiesAPrivateIndexThatWentMissing: with the real index
// unchanged, a private index someone deleted is copied again rather than
// read as empty, which would drop what the real index tracks.
func TestDiffProbe_RecopiesAPrivateIndexThatWentMissing(t *testing.T) {
	f := newProbeFixture(t)
	writeFile(t, f.tree, "keep.log", "wanted\n")
	runGit(t, f.tree, "add", "-f", "keep.log")
	_, _, paths := f.probeAll(t)
	require.Equal(t, []string{"keep.log"}, paths)

	require.NoError(t, os.Remove(filepath.Join(f.gitDir, privateIndexName)))
	_, _, paths = f.probeAll(t)
	assert.Equal(t, []string{"keep.log"}, paths, "the force-added file still shows")
}
