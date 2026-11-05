package core

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wqSigOf is the signature a job of argv run in cwd gets.
func wqSigOf(t *testing.T, argv []string, cwd string) sched.Signature {
	t.Helper()
	repo, folder, err := sched.FindRepo(cwd)
	require.NoError(t, err)
	return sched.NewSignature(repo, folder, argv)
}

func TestJobQueue_ARunTeachesTheNextJobOfTheSameCommand(t *testing.T) {
	m := wqLiveModel(t)
	sub := wqJob(t, "sh", "-c", "exit 0")
	id1, _, err := m.submitJob(sub)
	require.NoError(t, err)
	first := wqRecord(t, m, id1)
	assert.Equal(t, sched.ClaimSources{CPUs: sched.SourceDefault, Memory: sched.SourceDefault, IO: sched.SourceDefault}, first.Sources)
	assert.NotEmpty(t, first.Signature)
	wqSettle(t, m, id1)

	b := m.queue.beliefs.Find(first.Signature)
	require.NotNil(t, b, "the submit made the belief")
	require.Len(t, b.Runs, 1, "and the settle recorded the run")
	assert.Equal(t, sched.OutcomeDone, b.Runs[0].Outcome)
	saved, err := sched.LoadBeliefs(m.queue.dir)
	require.NoError(t, err)
	require.Len(t, saved.Beliefs, 1, "beliefs.json is written")

	id2, _, err := m.submitJob(sub) // the same command in the same folder
	require.NoError(t, err)
	second := wqRecord(t, m, id2)
	assert.Equal(t, first.Signature, second.Signature)
	assert.Equal(t, sched.SourceLearned, second.Sources.Memory)
	assert.Equal(t, 1, second.Claim.MemoryGB, "a tiny peak plus its margin")
	assert.Equal(t, 1, second.Learned)
}

func TestJobQueue_AQueuedJobsClaimFollowsItsBelief(t *testing.T) {
	m := wqLiveModel(t)
	wqConfig(t, m, `{"cpus": 4}`) // one default job fills the cores
	blocker := wqSubmit(t, m, "sleep", "60")
	require.Equal(t, sched.Running, wqRecord(t, m, blocker).State)
	sub := wqJob(t, "cargo", "build")
	sig := wqSigOf(t, sub.Argv, sub.Cwd)
	b := m.queue.beliefs.Ensure(sig, time.Now())
	b.AddRun(sched.Run{Outcome: sched.OutcomeDone, PeakKB: 5 << 20, WallSeconds: 30})

	id, _, err := m.submitJob(sub)
	require.NoError(t, err)
	assert.Equal(t, sched.Queued, wqRecord(t, m, id).State)
	assert.Equal(t, 6, wqRecord(t, m, id).Claim.MemoryGB, "5 GB plus its margin")

	m.queue.beliefs.Find(sig.ID()).AddRun(sched.Run{Outcome: sched.OutcomeDone, PeakKB: 10 << 20, WallSeconds: 30})
	m.admitJobs()
	assert.Equal(t, 12, wqRecord(t, m, id).Claim.MemoryGB, "a claim stays live until the job starts")
}

func TestJobQueue_ASubmitRefusesAOneWordCommandWithSpaces(t *testing.T) {
	m := wqModel(t)
	_, _, err := m.submitJob(wqJob(t, "nixos-rebuild build --flake .#fresco"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the command is one word with spaces")
	assert.Empty(t, m.queue.store.Jobs, "nothing was queued")
}

func TestJobQueue_AJobOutsideARepoIsItsFoldersOwnAndAnUnreadableGitIsUnsized(t *testing.T) {
	m := wqModel(t)
	id := wqSubmit(t, m, "true")
	assert.NotEmpty(t, wqRecord(t, m, id).Signature)

	sub := wqJob(t, "true")
	require.NoError(t, os.WriteFile(filepath.Join(sub.Cwd, ".git"), []byte("garbage\n"), 0o644))
	id2, _, err := m.submitJob(sub)
	require.NoError(t, err)
	rec := wqRecord(t, m, id2)
	assert.Empty(t, rec.Signature)
	assert.Contains(t, rec.Unsized, "names no gitdir")
	assert.Equal(t, sched.Claim{CPUs: 4, MemoryGB: 6}, rec.Claim, "the default claim")
}

func TestJobQueue_AJobInAWorktreeSharesItsCheckoutsBelief(t *testing.T) {
	m := wqModel(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	gitdir := filepath.Join(root, "main", ".git", "worktrees", "wt")
	require.NoError(t, os.MkdirAll(gitdir, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "wt"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(gitdir, "commondir"), []byte("../..\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "wt", ".git"), []byte("gitdir: "+gitdir+"\n"), 0o644))

	inMain := wqJob(t, "true")
	inMain.Cwd = filepath.Join(root, "main")
	inWorktree := wqJob(t, "true")
	inWorktree.Cwd = filepath.Join(root, "wt")
	a, _, err := m.submitJob(inMain)
	require.NoError(t, err)
	b, _, err := m.submitJob(inWorktree)
	require.NoError(t, err)
	assert.Equal(t, wqRecord(t, m, a).Signature, wqRecord(t, m, b).Signature)
}

func TestJobQueue_BeliefsThatFailToLoadTurnLearningOff(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs", sched.BeliefsFile)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(`{nope`), 0o600))

	m := wqModelAt(t, dir)

	assert.Contains(t, m.queue.learningOff(), "beliefs.json could not be loaded")
	assert.Equal(t, m.queue.learningOff(), m.Queue().Learning)
	id := wqSubmit(t, m, "true")
	assert.Equal(t, sched.SourceDefault, wqRecord(t, m, id).Sources.Memory)
	m.saveBeliefsQuiet("test")
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, `{nope`, string(got), "never written")
}

func TestJobQueue_AdoptionMarksARunAdopted(t *testing.T) {
	m := wqLiveModel(t)
	id := wqLaunch(t, m, "sleep", "30")
	m.adoptJobs()
	assert.True(t, wqRecord(t, m, id).Adopted)
}
