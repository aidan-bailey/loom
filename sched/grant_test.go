package sched

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestGrant(t *testing.T) {
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	p := szPolicy // 14 cores and 22 GB; the default claim, the reserve, 4 cores and 6 GB
	p.StarveAfter = 10 * time.Minute
	wide := belief(at(4, 3.9)) // p ≈ 0.99: efficient up to 8 cores, twice the 4 it had
	learned := func(id JobID, cores, mem int) Record {
		return Record{ID: id, Order: int64(id), State: Queued, Submitted: now, Claim: Claim{CPUs: cores, MemoryGB: mem},
			Sources: ClaimSources{SourceLearned, SourceLearned, SourceDefault}}
	}
	queued := func(id JobID, c Claim) Record {
		return Record{ID: id, Order: int64(id), State: Queued, Submitted: now, Claim: c, Sources: all(SourceExplicit)}
	}
	running := func(cores int) Record {
		return Record{ID: 9, State: Running, Claim: Claim{CPUs: cores, MemoryGB: 2}, Started: now}
	}
	grant := func(b *Belief, jobs ...Record) map[JobID]Claim {
		return Grant(Admit(jobs, p, now, nil), jobs, p, nil, func(Record) *Belief { return b })
	}

	assert.Equal(t, 8, SpareWidth(learned(1, 4, 2), wide))
	fromFamily := learned(1, 4, 2)
	fromFamily.Sources.CPUs = SourceFamily
	assert.Zero(t, SpareWidth(fromFamily, wide), "cores from the family are no grant's")
	assert.Equal(t, map[JobID]Claim{1: {CPUs: 4, MemoryGB: 2}}, grant(wide, learned(1, 4, 2)),
		"an idle host: 14 − 4 − the reserve's 4 leaves 6 cores; it takes 4, to its width of 8, and memory in proportion")
	assert.Equal(t, map[JobID]Claim{1: {CPUs: 2, MemoryGB: 1}}, grant(wide, running(4), learned(1, 4, 2)),
		"what a running job leaves")
	assert.Nil(t, grant(wide, running(6), learned(1, 4, 2)), "the reserve stays free")

	assert.Nil(t, grant(wide, learned(1, 4, 2), queued(2, Claim{CPUs: 11, MemoryGB: 1})), "a job left waiting for cores")
	assert.Equal(t, map[JobID]Claim{1: {CPUs: 4, MemoryGB: 2}}, grant(wide, learned(1, 4, 2), queued(2, Claim{CPUs: 20, MemoryGB: 1})),
		"a job over the caps waits for nothing a grant holds")
	sizing := queued(2, Claim{})
	sizing.Sizing = SizingAsking
	assert.Nil(t, grant(wide, learned(1, 4, 2), sizing), "a job being sized")
	head := learned(1, 4, 2)
	head.Submitted = now.Add(-time.Hour)
	assert.Nil(t, grant(wide, head, learned(2, 4, 2)), "a starved head starts alone")

	assert.Equal(t, map[JobID]Claim{1: {CPUs: 2, MemoryGB: 1}}, grant(wide, learned(1, 4, 2), learned(2, 4, 2)),
		"queue order: the first takes the 2 cores left, the second gets none")
	assert.Equal(t, map[JobID]Claim{1: {CPUs: 2, MemoryGB: 5}}, grant(wide, learned(1, 4, 10)),
		"6 GB free past the reserve: 8 cores would need 20 GB and 7 cores 18, so 6 cores and 15 GB")

	r10 := at(10, 9.8)
	r10.Spare, r10.PeakKB = 6, 2<<20
	assert.Equal(t, map[JobID]Claim{1: {CPUs: 6, MemoryGB: 1}}, grant(belief(at(4, 3.9), r10), learned(1, 4, 2)),
		"a run at 10 cores peaked at 2 GB: 3 GB with its margin, not 5 in proportion")

	explicitCores := learned(1, 4, 2)
	explicitCores.Sources.CPUs = SourceExplicit
	explicitMemory := learned(1, 4, 2)
	explicitMemory.Sources.Memory = SourceExplicit
	capped := learned(1, 4, 2)
	capped.AboveCap = true
	assert.Nil(t, grant(wide, explicitCores), "explicit cores")
	assert.Nil(t, grant(wide, explicitMemory), "explicit memory")
	assert.Nil(t, grant(wide, capped), "memory held to the cap")
	assert.Nil(t, grant(nil, learned(1, 4, 2)), "p unknown")
	assert.Nil(t, grant(belief(at(4, 7.81)), learned(1, 4, 2)), "an overrunner")
	assert.Nil(t, grant(belief(at(4, 2.17)), learned(1, 4, 2)), "a width of 4 is no wider")
	assert.Nil(t, Grant(Decision{Start: []JobID{1}}, []Record{learned(1, 4, 2)}, Policy{}, nil, func(Record) *Belief { return wide }), "no caps")
}

func TestGrant_CountsARunningJobAtItsHold(t *testing.T) {
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	p := szPolicy // 14 cores and 22 GB; the reserve 4 cores and 6 GB
	p.StarveAfter = 10 * time.Minute
	wide := func(Record) *Belief { return belief(at(4, 3.9)) }
	job := Record{ID: 1, Order: 1, State: Queued, Submitted: now, Claim: Claim{CPUs: 4, MemoryGB: 2},
		Sources: ClaimSources{SourceLearned, SourceLearned, SourceDefault}}
	jobs := []Record{{ID: 9, State: Running, Claim: Claim{CPUs: 6, MemoryGB: 2}, Started: now}, job}
	assert.Nil(t, Grant(Admit(jobs, p, now, nil), jobs, p, nil, wide), "at its claim the reserve is all that is left")
	holds := map[JobID]Need{9: {CPUs: 2, MemoryGB: 1}}
	assert.Equal(t, map[JobID]Claim{1: {CPUs: 4, MemoryGB: 2}}, Grant(Admit(jobs, p, now, holds), jobs, p, holds, wide),
		"at its hold, 14 − 2 − 4 − the reserve's 4 leaves 4 cores")
}
