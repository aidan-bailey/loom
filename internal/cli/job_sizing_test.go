package cli

import (
	"testing"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
)

func srcs(c, m, io sched.Source) sched.ClaimSources {
	return sched.ClaimSources{CPUs: c, Memory: m, IO: io}
}

func TestSizingLine(t *testing.T) {
	L, A, D, E := sched.SourceLearned, sched.SourceAgent, sched.SourceDefault, sched.SourceExplicit
	cases := []struct {
		v    core.JobView
		want string
	}{
		{core.JobView{Claim: sched.Claim{CPUs: 8, MemoryGB: 16, IO: true}, Sources: srcs(L, L, L), Learned: 5}, "sized 8c/16G io (learned from 5 runs)"},
		{core.JobView{Claim: sched.Claim{CPUs: 4, MemoryGB: 6}, Sources: srcs(A, A, A)}, "sized 4c/6G (the agent's estimate)"},
		{core.JobView{Claim: sched.Claim{CPUs: 4, MemoryGB: 20}, Sources: srcs(L, E, L), Learned: 1}, "sized 4c/20G (cores and I/O learned from 1 run, memory yours)"},
		{core.JobView{Claim: sched.Claim{CPUs: 4, MemoryGB: 6}, Sources: srcs(D, D, D), AgentWhy: "the agent didn't answer in 60s"}, "sized 4c/6G (default: the agent didn't answer in 60s)"},
		{core.JobView{Claim: sched.Claim{CPUs: 4, MemoryGB: 6}, Sources: srcs(D, D, D)}, "sized 4c/6G (default)"},
		{core.JobView{Claim: sched.Claim{CPUs: 4, MemoryGB: 6}, Sources: srcs(D, D, D), Unsized: "read /x/.git: denied"}, "not sized: read /x/.git: denied; it claims 4c/6G (the default)"},
		{core.JobView{Claim: sched.Claim{Exclusive: true}, Sources: srcs(sched.SourceExclusive, sched.SourceExclusive, sched.SourceExclusive)}, ""},
		{core.JobView{Sizing: sched.SizingAsking}, "being sized: asking the agent"},
		{core.JobView{Claim: sched.Claim{CPUs: 4, MemoryGB: 6}}, ""},
		{core.JobView{Claim: sched.Claim{CPUs: 8, MemoryGB: 16, IO: true}, Sources: srcs(L, L, L), Learned: 5, Usual: 2 * time.Minute}, "sized 8c/16G io (learned from 5 runs); usually 2m"},
		{core.JobView{Claim: sched.Claim{CPUs: 4, MemoryGB: 6}, Sources: srcs(A, A, A), Usual: 5 * time.Minute, UsualFromPrior: true}, "sized 4c/6G (the agent's estimate); about 5m"},
		{core.JobView{Claim: sched.Claim{CPUs: 4, MemoryGB: 2}, Sources: srcs(L, L, D), Learned: 1, Usual: 34 * time.Second, Ideal: 6},
			"sized 4c/2G (cores and memory learned from 1 run, I/O default); usually 34s; up to 6 cores when idle"},
		{core.JobView{Claim: sched.Claim{CPUs: 8, MemoryGB: 4}, Spare: sched.Claim{CPUs: 4, MemoryGB: 2}, Sources: srcs(L, L, D), Learned: 1},
			"sized 4+4c/4G (cores and memory learned from 1 run, I/O default)"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, sizingLine(c.v))
	}
}

func TestAskingLine(t *testing.T) {
	assert.Equal(t, "being sized: asking the agent (up to 60s)", askingLine(time.Minute))
	assert.Equal(t, "being sized: asking the agent", askingLine(0))
}

func TestMemoryWarning(t *testing.T) {
	v := core.JobView{Claim: sched.Claim{CPUs: 4, MemoryGB: 12}, Sources: srcs(sched.SourceDefault, sched.SourceExplicit, sched.SourceDefault), BelowKB: 14_422_836}
	assert.Equal(t, "warning: --mem 12 is below what this command has used (13.8 GB)", memoryWarning(v))
	v.BelowKB = 0
	assert.Empty(t, memoryWarning(v))
}

func TestClaimText(t *testing.T) {
	assert.Equal(t, "agent", claimText(core.JobView{Kind: sched.KindAgent}), "an agent job claims nothing")
	assert.Equal(t, "8c/16G io learned·5", claimText(core.JobView{Claim: sched.Claim{CPUs: 8, MemoryGB: 16, IO: true}, Sources: srcs(sched.SourceLearned, sched.SourceLearned, sched.SourceLearned), Learned: 5}))
	assert.Equal(t, "4c/22G! learned·2", claimText(core.JobView{Claim: sched.Claim{CPUs: 4, MemoryGB: 22}, Sources: srcs(sched.SourceDefault, sched.SourceLearned, sched.SourceDefault), Learned: 2, AboveCap: true}))
	assert.Equal(t, "—", claimText(core.JobView{Sizing: sched.SizingAsking}))
	assert.Equal(t, "exclusive", claimText(core.JobView{Claim: sched.Claim{Exclusive: true}}))
	assert.Equal(t, "4c/6G", claimText(core.JobView{Claim: sched.Claim{CPUs: 4, MemoryGB: 6}}), "a job from before sizing")
	assert.Equal(t, "4+2c/3G learned·1", claimText(core.JobView{Claim: sched.Claim{CPUs: 6, MemoryGB: 3}, Spare: sched.Claim{CPUs: 2, MemoryGB: 1},
		Sources: srcs(sched.SourceLearned, sched.SourceLearned, sched.SourceDefault), Learned: 1}))
	held := core.JobView{State: sched.Running, Claim: sched.Claim{CPUs: 4, MemoryGB: 8}, Holds: sched.Need{CPUs: 1, MemoryGB: 2},
		Sources: srcs(sched.SourceLearned, sched.SourceLearned, sched.SourceDefault), Learned: 3}
	assert.Equal(t, "4c/8G learned·3 holds 1c/2G", claimText(held), "a running job holding less than its claim")
	held.State = sched.Queued
	assert.Equal(t, "4c/8G learned·3", claimText(held), "only a running job holds")
}

func TestStartedLine(t *testing.T) {
	t0 := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	v := core.JobView{ID: 21, Submitted: t0, Started: t0.Add(6 * time.Second), Claim: sched.Claim{CPUs: 4, MemoryGB: 2}}
	assert.Equal(t, "job 21 started after 6s", startedLine(v))
	v.Claim, v.Spare = sched.Claim{CPUs: 6, MemoryGB: 3}, sched.Claim{CPUs: 2, MemoryGB: 1}
	assert.Equal(t, "job 21 started after 6s with 2 spare cores (4+2c/3G)", startedLine(v))
	v.Claim, v.Spare = sched.Claim{CPUs: 5, MemoryGB: 3}, sched.Claim{CPUs: 1, MemoryGB: 1}
	assert.Equal(t, "job 21 started after 6s with 1 spare core (4+1c/3G)", startedLine(v))
	v.Claim, v.Spare, v.Borrower = sched.Claim{CPUs: 4, MemoryGB: 2}, sched.Claim{}, true
	assert.Equal(t, "job 21 started after 6s on borrowed memory", startedLine(v), "memory borrowing spec §8")
}
