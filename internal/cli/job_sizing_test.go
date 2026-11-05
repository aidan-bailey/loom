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
	assert.Equal(t, "8c/16G io learned·5", claimText(core.JobView{Claim: sched.Claim{CPUs: 8, MemoryGB: 16, IO: true}, Sources: srcs(sched.SourceLearned, sched.SourceLearned, sched.SourceLearned), Learned: 5}))
	assert.Equal(t, "4c/22G! learned·2", claimText(core.JobView{Claim: sched.Claim{CPUs: 4, MemoryGB: 22}, Sources: srcs(sched.SourceDefault, sched.SourceLearned, sched.SourceDefault), Learned: 2, AboveCap: true}))
	assert.Equal(t, "—", claimText(core.JobView{Sizing: sched.SizingAsking}))
	assert.Equal(t, "exclusive", claimText(core.JobView{Claim: sched.Claim{Exclusive: true}}))
	assert.Equal(t, "4c/6G", claimText(core.JobView{Claim: sched.Claim{CPUs: 4, MemoryGB: 6}}), "a job from before sizing")
}
