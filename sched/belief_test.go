package sched

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var bt0 = time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)

func int64p(n int64) *int64 { return &n }

func TestRunOf(t *testing.T) {
	base := Record{ID: 6, Claim: Claim{CPUs: 8, MemoryGB: 12, IO: true}, Started: bt0, Ended: bt0.Add(93 * time.Second), PeakRSSKB: 14_000_000,
		Checkout: "/src/wt"}
	exit := &Exit{Code: 0, MaxRSSKB: 14_422_836, CPUSeconds: 512, ReadBytes: int64p(5), WriteBytes: int64p(3 << 30)}
	cases := []struct {
		name string
		edit func(*Record)
		want Outcome
		ok   bool
	}{
		{"done", func(r *Record) { r.State, r.Exit = Done, exit }, OutcomeDone, true},
		{"an exit code", func(r *Record) { r.State, r.Exit = Failed, &Exit{Code: 101} }, OutcomeFailed, true},
		{"a signal", func(r *Record) { r.State, r.Exit = Failed, &Exit{Code: -1, Signal: "SIGKILL"} }, OutcomeSignal, true},
		{"cancelled", func(r *Record) { r.State, r.CancelReason = Cancelled, "x" }, OutcomeCancelled, true},
		{"cancelled while queued", func(r *Record) { r.State, r.Started = Cancelled, time.Time{} }, "", false},
		{"a start failure", func(r *Record) { r.State, r.Exit, r.Failure = Failed, &Exit{Code: 127, Error: "no"}, "no" }, "", false},
		{"its folder gone", func(r *Record) { r.State, r.Failure = Failed, "its folder is gone" }, "", false},
		{"lost", func(r *Record) { r.State = Lost }, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := base
			c.edit(&rec)
			run, ok := RunOf(rec)
			require.Equal(t, c.ok, ok)
			if !ok {
				return
			}
			assert.Equal(t, c.want, run.Outcome)
			assert.Equal(t, 93.0, run.WallSeconds)
			assert.Equal(t, rec.Claim, run.Claim)
			assert.Equal(t, "/src/wt", run.Checkout, "the run keeps its checkout")
		})
	}
	rec := base
	rec.State, rec.Exit, rec.Adopted = Done, exit, true
	run, _ := RunOf(rec)
	assert.Equal(t, int64(14_422_836), run.PeakKB, "the larger of the sampled peak and max_rss")
	assert.Equal(t, 512.0, run.CPUSeconds)
	assert.Equal(t, int64(3<<30), *run.WriteBytes)
	assert.True(t, run.Adopted)
	assert.False(t, run.FullPeak(), "an adopted run's peak is a floor")
}

func TestBeliefs_EnsureAddRunAndForget(t *testing.T) {
	var bs Beliefs
	sig := NewSignature("/r/.git", ".", []string{"cargo", "build"})
	b := bs.Ensure(sig, bt0)
	assert.Equal(t, sig.ID(), b.ID)
	assert.Equal(t, bt0, b.FirstSeen)
	for i := range MaxRuns + 3 {
		b.AddRun(Run{Job: JobID(i + 1)})
	}
	require.Len(t, b.Runs, MaxRuns)
	assert.Equal(t, JobID(MaxRuns+3), b.Runs[0].Job, "newest first")
	for i := range MaxNotes + 2 {
		b.AddNote(Note{Job: JobID(i + 1)})
	}
	require.Len(t, b.Notes, MaxNotes)
	assert.Equal(t, JobID(MaxNotes+2), b.Notes[0].Job)

	again := bs.Ensure(sig, bt0.Add(time.Hour))
	assert.Equal(t, bt0.Add(time.Hour), again.LastSeen, "seen again")
	assert.Len(t, bs.Beliefs, 1)
	assert.True(t, bs.Forget(sig.ID()))
	assert.False(t, bs.Forget(sig.ID()))
	assert.Nil(t, bs.Find(sig.ID()))
}

func TestBeliefs_EnsureDropsTheLeastRecentlySeenPastTheLimit(t *testing.T) {
	var bs Beliefs
	for i := range MaxBeliefs {
		bs.Ensure(NewSignature("/r/.git", ".", []string{"job", fmt.Sprint(i)}), bt0.Add(time.Duration(i+1)*time.Minute))
	}
	oldest := bs.Beliefs[0].ID
	newSig := NewSignature("/r/.git", ".", []string{"new"})
	bs.Ensure(newSig, bt0.Add(48*time.Hour))
	assert.Len(t, bs.Beliefs, MaxBeliefs)
	assert.Nil(t, bs.Find(oldest), "the least recently seen goes")
	assert.NotNil(t, bs.Find(newSig.ID()))
}

func TestBeliefsStore_RoundTripAndRefusals(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "jobs")
	empty, err := LoadBeliefs(dir)
	require.NoError(t, err, "no file is an empty registry")
	assert.Equal(t, BeliefsVersion, empty.Version)

	var bs Beliefs
	b := bs.Ensure(NewSignature("/r/.git", ".", []string{"go", "test", "./..."}), bt0)
	b.AddRun(Run{Job: 3, Outcome: OutcomeDone, PeakKB: 700_000, WallSeconds: 40, WriteBytes: int64p(1), Checkout: "/src/wt"})
	b.Prior = &Prior{CPUs: 4, MemoryGB: 2, DurationSeconds: 60, At: bt0}
	require.NoError(t, SaveBeliefs(dir, bs))
	info, err := os.Stat(filepath.Join(dir, BeliefsFile))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	got, err := LoadBeliefs(dir)
	require.NoError(t, err)
	assert.Equal(t, bs.Beliefs, got.Beliefs)

	cases := map[string]string{
		"newer":      `{"version": 3, "beliefs": []}`,
		"garbled":    `{nope`,
		"mismatched": `{"version": 1, "beliefs": [{"id": "000000000000", "repo": "/r/.git", "folder": ".", "command": ["x"]}]}`,
		"version 0":  `{"version": 0, "beliefs": []}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, BeliefsFile), []byte(body), 0o600))
			_, err := LoadBeliefs(dir)
			require.Error(t, err)
			assert.Equal(t, name == "newer", errors.Is(err, ErrNewerBeliefs))
			left, _ := os.ReadFile(filepath.Join(dir, BeliefsFile))
			assert.Equal(t, body, string(left), "left as it is")
		})
	}
}

func TestRunOf_KeepsItsSpareCores(t *testing.T) {
	t0 := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	rec := Record{ID: 3, State: Done, Started: t0, Ended: t0.Add(time.Minute), Claim: Claim{CPUs: 8, MemoryGB: 4},
		Spare: Claim{CPUs: 4, MemoryGB: 2}, Exit: &Exit{Ended: t0.Add(time.Minute)}}
	run, ok := RunOf(rec)
	require.True(t, ok)
	assert.Equal(t, 4, run.Spare)
	assert.Equal(t, Claim{CPUs: 8, MemoryGB: 4}, run.Claim, "the claim it ran with: the total")
}

func TestBeliefsStore_VersionFollowsThePrerequisiteParts(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "jobs")
	version := func() int {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dir, BeliefsFile))
		require.NoError(t, err)
		var head struct {
			Version int `json:"version"`
		}
		require.NoError(t, json.Unmarshal(data, &head))
		return head.Version
	}
	var bs Beliefs
	build := bs.Ensure(NewSignature("/r/.git", ".", []string{"cargo", "test", "--no-run"}), bt0).ID
	require.NoError(t, SaveBeliefs(dir, bs))
	assert.Equal(t, 1, version(), "no prerequisite part: an older loom reads it")

	warm := NewSignature("/r/.git", ".", []string{"cargo", "test"})
	warm.After = build
	bs.Ensure(warm, bt0)
	require.NoError(t, SaveBeliefs(dir, bs))
	assert.Equal(t, 2, version(), "an older loom would compute this belief's ID without its prerequisite")
	got, err := LoadBeliefs(dir)
	require.NoError(t, err)
	assert.Equal(t, bs.Beliefs, got.Beliefs, "this build reads it back")

	bs.Forget(warm.ID())
	require.NoError(t, SaveBeliefs(dir, bs))
	assert.Equal(t, 1, version(), "back to 1 once no belief has one")
}

func TestBelief_AddRunKeepsTheNewestProfiles(t *testing.T) {
	var b Belief
	for i := range ProfiledRuns + 2 {
		b.AddRun(Run{Job: JobID(i + 1), Profile: &Profile{End: float64(i)}})
	}
	for i, r := range b.Runs {
		assert.Equal(t, i < ProfiledRuns, r.Profile != nil, "run %d", r.Job)
	}
}

func TestRunOf_CopiesABrokenHold(t *testing.T) {
	rec := Record{ID: 1, State: Done, Started: time.Unix(10, 0), Ended: time.Unix(20, 0), HoldBroke: true}
	run, ok := RunOf(rec)
	require.True(t, ok)
	assert.True(t, run.HoldBroke)
}

func TestBelief_CloneCopiesProfiles(t *testing.T) {
	b := Belief{Runs: []Run{{Profile: &Profile{End: 1, Mem: Staircase{Steps: []Step{{Until: 1, Level: 2}}}}}}}
	c := b.Clone()
	c.Runs[0].Profile.Mem.Steps[0].Level = 9
	assert.Equal(t, int64(2), b.Runs[0].Profile.Mem.Steps[0].Level)
}

// TestRunOf_ALadderedRunTeachesFloorsOnly: a squeezed or paused run's
// timing is distorted (cgroup spec §8.5): its peak is a floor and it teaches
// neither cores nor a duration.
func TestRunOf_ALadderedRunTeachesFloorsOnly(t *testing.T) {
	start := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	rec := Record{ID: 1, State: Done, Started: start, Ended: start.Add(time.Minute), Claim: Claim{CPUs: 4, MemoryGB: 6},
		Exit: &Exit{Code: 0, Ended: start.Add(time.Minute), CPUSeconds: 120}, PeakRSSKB: 1 << 20}
	run, ok := RunOf(rec)
	require.True(t, ok)
	assert.True(t, run.FullPeak())
	assert.True(t, run.TeachesDuration())
	assert.True(t, run.teachesCores())

	rec.Laddered = true
	run, ok = RunOf(rec)
	require.True(t, ok)
	assert.True(t, run.Laddered)
	assert.False(t, run.FullPeak(), "its peak is a floor")
	assert.False(t, run.TeachesDuration())
	assert.False(t, run.teachesCores())
}
