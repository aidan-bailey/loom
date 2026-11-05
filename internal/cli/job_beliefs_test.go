package cli

import (
	"bytes"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteBeliefs(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	b := sched.Belief{ID: "3f9a1c2b7d10", Signature: sched.Signature{Repo: "/src/polyoxide/.git", Folder: ".", Command: []string{"bash", "scripts/web-smoke.sh"}}, LastSeen: now.Add(-2 * time.Hour)}
	page := sched.BeliefsPage{
		Beliefs: []sched.BeliefView{{Belief: b, RepoName: "polyoxide", Usual: 93 * time.Second, DurationRuns: 5,
			Sized: sched.Sized{Claim: sched.Claim{CPUs: 8, MemoryGB: 16, IO: true}, Learned: 5,
				Sources: sched.ClaimSources{CPUs: sched.SourceLearned, Memory: sched.SourceLearned, IO: sched.SourceLearned}}}},
		Track: sched.Track{Priors: 12, Scored: 12, MemoryWithin: 9, MemoryUnder: 2, DurationScored: 10, DurationMedian: 1.4},
	}
	var out bytes.Buffer
	writeBeliefs(&out, page, now)
	text := out.String()
	assert.Contains(t, text, "agent priors: 12 scored · memory within 25%: 9 · under on memory: 2 · duration ×1.4")
	assert.Regexp(t, `(?m)^3f9a1c2b7d10 +polyoxide +\. +bash scripts/web-smoke\.sh +0 +8c/16G io learned·5 +- +~2m +2h00m ago$`, text)

	page.Beliefs[0].Scaling = sched.Scaling{Known: true, Parallel: 0.72, Runs: 1, Ideal: 4}
	out.Reset()
	writeBeliefs(&out, page, now)
	assert.Regexp(t, `learned·5 +72% +~2m`, out.String())
	page.Beliefs[0].Scaling = sched.Scaling{Overruns: true, Busy: 7.8}
	out.Reset()
	writeBeliefs(&out, page, now)
	assert.Regexp(t, `learned·5 +overruns +~2m`, out.String())

	out.Reset()
	writeBeliefs(&out, sched.BeliefsPage{}, now)
	assert.Contains(t, out.String(), "No beliefs yet.")
}

// beliefs lists what a run taught, shows it in full, and forgets it,
// through a daemon.
func TestJobBeliefs_ListShowAndForget(t *testing.T) {
	d := serveDaemon(t, "true")
	_, _, err := runJob(t, "run", "-d", "quick", "--", "true")
	require.NoError(t, err)
	require.Eventually(t, func() bool { return d.job(1).State == sched.Done }, 10*time.Second, 50*time.Millisecond)

	out, _, err := runJob(t, "beliefs")
	require.NoError(t, err)
	assert.Contains(t, out, "true")
	sig := d.job(1).Signature
	require.NotEmpty(t, sig)
	assert.Contains(t, out, string(sig))

	out, _, err = runJob(t, "beliefs", "show", string(sig))
	require.NoError(t, err)
	assert.Contains(t, out, "belief "+string(sig))
	assert.Contains(t, out, "runs      JOB")

	out, _, err = runJob(t, "beliefs", "forget", string(sig))
	require.NoError(t, err)
	assert.Contains(t, out, "forgot belief "+string(sig))
	assert.Contains(t, out, "1 run)")

	_, _, err = runJob(t, "beliefs", "show", string(sig))
	assert.ErrorContains(t, err, "no belief")
}
