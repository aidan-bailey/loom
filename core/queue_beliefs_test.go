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
	repo, folder, _, err := sched.FindRepo(cwd)
	require.NoError(t, err)
	return sched.NewSignature(repo, folder, argv)
}

func TestJobQueue_ARunTeachesTheNextJobOfTheSameCommand(t *testing.T) {
	m := wqLiveModel(t)
	sub := wqJob(t, "sh", "-c", "exit 0")
	id1, _, err := m.submitJob(sub)
	require.NoError(t, err)
	first := wqRecord(t, m, id1)
	checkout, err := filepath.EvalSymlinks(sub.Cwd)
	require.NoError(t, err)
	assert.Equal(t, checkout, first.Checkout, "a folder in no repository is its own checkout")
	assert.Equal(t, sched.ClaimSources{CPUs: sched.SourceDefault, Memory: sched.SourceDefault, IO: sched.SourceDefault}, first.Sources)
	assert.NotEmpty(t, first.Signature)
	wqSettle(t, m, id1)

	b := m.queue.beliefs.Find(first.Signature)
	require.NotNil(t, b, "the submit made the belief")
	require.Len(t, b.Runs, 1, "and the settle recorded the run")
	assert.Equal(t, checkout, b.Runs[0].Checkout, "the run keeps it")
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

func TestReadBeliefs_AnswersWhatTheQuerySelects(t *testing.T) {
	m := wqModel(t)
	a := m.queue.beliefs.Ensure(sched.NewSignature("/src/polyoxide/.git", ".", []string{"cargo", "build"}), time.Now())
	a.Prior = &sched.Prior{CPUs: 2, MemoryGB: 3, Score: sched.PriorScore{Memory: 1.1}}
	m.queue.beliefs.Ensure(sched.NewSignature("/src/loom/.git", ".", []string{"go", "test", "./..."}), time.Now().Add(time.Second))

	m.ReadBeliefs(sched.BeliefQuery{}, 7)
	r := requestReply(t, m, 7)
	require.NoError(t, r.Err)
	require.Len(t, r.Beliefs.Beliefs, 2)
	assert.Equal(t, "loom", r.Beliefs.Beliefs[0].RepoName, "most recently seen first")
	assert.Equal(t, 1, r.Beliefs.Track.Scored)

	m.ReadBeliefs(sched.BeliefQuery{Repo: "polyoxide"}, 8)
	assert.Len(t, requestReply(t, m, 8).Beliefs.Beliefs, 1)
	m.ReadBeliefs(sched.BeliefQuery{ID: "000000000000"}, 9)
	assert.ErrorContains(t, requestReply(t, m, 9).Err, "no belief 000000000000")
}

func TestForgetBelief_DropsItAndRefusesWhatItCannot(t *testing.T) {
	m := wqModel(t)
	b := m.queue.beliefs.Ensure(sched.NewSignature("/src/loom/.git", ".", []string{"go", "test"}), time.Now())
	id := b.ID
	m.ForgetBelief(id, 3)
	require.NoError(t, requestReply(t, m, 3).Err)
	assert.Nil(t, m.queue.beliefs.Find(id))
	saved, err := sched.LoadBeliefs(m.queue.dir)
	require.NoError(t, err)
	assert.Empty(t, saved.Beliefs)
	m.ForgetBelief(id, 4)
	assert.ErrorContains(t, requestReply(t, m, 4).Err, "no belief")
}

// wqWideBelief gives sub's signature a done run at 4 cores that kept 3.8
// busy (p ≈ 0.98): a job of it is sized 4c/2G and is efficient up to 8
// cores, twice the 4 it had.
func wqWideBelief(t *testing.T, m *Model, sub sched.Submit) sched.SignatureID {
	t.Helper()
	sig := wqSigOf(t, sub.Argv, sub.Cwd)
	m.queue.beliefs.Ensure(sig, time.Now()).AddRun(sched.Run{Outcome: sched.OutcomeDone, Claim: sched.Claim{CPUs: 4, MemoryGB: 2},
		PeakKB: 1 << 20, CPUSeconds: 380, WallSeconds: 100})
	return sig.ID()
}

func TestJobQueue_AnIdleHostLendsSpareCoresToAJobThatScales(t *testing.T) {
	m := wqLiveModel(t)
	sub := wqJob(t, "sleep", "1")
	sig := wqWideBelief(t, m, sub)
	id, _, err := m.submitJob(sub)
	require.NoError(t, err)
	wqPump(t, m)

	rec := wqRecord(t, m, id)
	require.Equal(t, sched.Running, rec.State)
	// Caps of 14 cores and 24 GB (wqHost), the reserve's 4c/6G and the base
	// 4c/2G leave 6 spare cores: it takes 4, to its width of 8.
	assert.Equal(t, sched.Claim{CPUs: 4, MemoryGB: 2}, rec.Spare)
	assert.Equal(t, sched.Claim{CPUs: 8, MemoryGB: 4}, rec.Claim)
	spec, err := sched.ReadSpec(m.queue.jobDir(id))
	require.NoError(t, err)
	assert.Equal(t, 8, spec.HintCPUs, "the hint variables carry the total")

	m.admitJobs()
	assert.Equal(t, 8, wqRecord(t, m, id).Claim.CPUs, "a pass never re-sizes a running claim")

	wqSettle(t, m, id)
	b := m.queue.beliefs.Find(sig)
	require.NotNil(t, b)
	assert.Equal(t, 4, b.Runs[0].Spare, "the run records its spare cores")
}

func TestJobQueue_NoSpareCoresWhileAnotherJobWaits(t *testing.T) {
	m := wqLiveModel(t)
	blocker := wqJob(t, "sleep", "1")
	blocker.Claim = sched.Claim{CPUs: 14, MemoryGB: 1}
	bid, _, err := m.submitJob(blocker)
	require.NoError(t, err)
	require.Equal(t, sched.Running, wqRecord(t, m, bid).State)

	sub := wqJob(t, "sleep", "30")
	wqWideBelief(t, m, sub)
	id, _, err := m.submitJob(sub)
	require.NoError(t, err)
	wide := wqJob(t, "sleep", "1")
	wide.Claim = sched.Claim{CPUs: 11, MemoryGB: 1}
	wid, _, err := m.submitJob(wide)
	require.NoError(t, err)

	wqWait(t, "the blocker to end and the next job to start", func() bool {
		if wqRecord(t, m, id).State == sched.Queued {
			wqPass(t, m)
		}
		return wqRecord(t, m, id).State != sched.Queued
	})
	rec := wqRecord(t, m, id)
	assert.Zero(t, rec.Spare, "job %d waits for 11 cores, so nothing is lent", wid)
	assert.Equal(t, 4, rec.Claim.CPUs)
	assert.Equal(t, sched.Queued, wqRecord(t, m, wid).State)
}
