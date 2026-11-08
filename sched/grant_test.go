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
		return Grant(Admit(jobs, p, now, nil), jobs, p, nil, func(Record) *Belief { return b }, HostIdle{}).Spare
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
	assert.Nil(t, Grant(Decision{Start: []JobID{1}}, []Record{learned(1, 4, 2)}, Policy{}, nil, func(Record) *Belief { return wide }, HostIdle{}).Spare, "no caps")
}

func TestGrant_CountsARunningJobAtItsHold(t *testing.T) {
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	p := szPolicy // 14 cores and 22 GB; the reserve 4 cores and 6 GB
	p.StarveAfter = 10 * time.Minute
	wide := func(Record) *Belief { return belief(at(4, 3.9)) }
	job := Record{ID: 1, Order: 1, State: Queued, Submitted: now, Claim: Claim{CPUs: 4, MemoryGB: 2},
		Sources: ClaimSources{SourceLearned, SourceLearned, SourceDefault}}
	jobs := []Record{{ID: 9, State: Running, Claim: Claim{CPUs: 6, MemoryGB: 2}, Started: now}, job}
	assert.Nil(t, Grant(Admit(jobs, p, now, nil), jobs, p, nil, wide, HostIdle{}).Spare, "at its claim the reserve is all that is left")
	holds := map[JobID]Need{9: {CPUs: 2, MemoryGB: 1}}
	assert.Equal(t, map[JobID]Claim{1: {CPUs: 4, MemoryGB: 2}}, Grant(Admit(jobs, p, now, holds), jobs, p, holds, wide, HostIdle{}).Spare,
		"at its hold, 14 − 2 − 4 − the reserve's 4 leaves 4 cores")
}

func TestExploreWidth(t *testing.T) {
	narrow := belief(at(2, 0.97))
	job := Record{Claim: Claim{CPUs: 2, MemoryGB: 4}, Sources: ClaimSources{SourceLearned, SourceLearned, SourceDefault}}
	assert.Equal(t, 4, ExploreWidth(job, narrow, 14))
	for name, edit := range map[string]func(*Record){
		"explicit cores":  func(r *Record) { r.Sources.CPUs = SourceExplicit },
		"family cores":    func(r *Record) { r.Sources.CPUs = SourceFamily },
		"the agent's":     func(r *Record) { r.Sources.CPUs = SourceAgent },
		"the default":     func(r *Record) { r.Sources.CPUs = SourceDefault },
		"explicit memory": func(r *Record) { r.Sources.Memory = SourceExplicit },
		"exclusive":       func(r *Record) { r.Claim = Claim{Exclusive: true} },
		"held to the cap": func(r *Record) { r.AboveCap = true },
	} {
		r := job
		edit(&r)
		assert.Zero(t, ExploreWidth(r, narrow, 14), name)
	}
}

func TestGrant_Explores(t *testing.T) {
	now := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	p := szPolicy // 14 cores and 22 GB; the default claim, the reserve, 4 cores and 6 GB
	p.StarveAfter = 10 * time.Minute
	p.Explore = true
	narrow := belief(at(2, 0.97)) // one width, p reads 0: no ordinary grant widens it
	learned := func(id JobID, cores, mem int) Record {
		return Record{ID: id, Order: int64(id), State: Queued, Submitted: now, Claim: Claim{CPUs: cores, MemoryGB: mem},
			Sources: ClaimSources{SourceLearned, SourceLearned, SourceDefault}}
	}
	grant := func(b *Belief, idle HostIdle, jobs ...Record) Grants {
		return Grant(Admit(jobs, p, now, nil), jobs, p, nil, func(Record) *Belief { return b }, idle)
	}
	idle := func(cores float64) HostIdle { return HostIdle{Cores: cores, Known: true} }
	explored := func(spare map[JobID]Claim) Grants {
		g := Grants{Spare: spare, Explored: map[JobID]bool{}}
		for id := range spare {
			g.Explored[id] = true
		}
		return g
	}

	assert.Equal(t, explored(map[JobID]Claim{1: {CPUs: 2, MemoryGB: 4}}), grant(narrow, idle(12), learned(1, 2, 4)),
		"12 idle cores less the reserve's 4 leave 8: 2 → 4, memory in proportion")
	assert.Equal(t, explored(map[JobID]Claim{1: {CPUs: 1, MemoryGB: 2}}), grant(narrow, idle(5), learned(1, 2, 4)),
		"5 idle leave room for 1 more core: the smaller step, 3 = ⌈1.5 × 2⌉")
	assert.Equal(t, Grants{}, grant(narrow, idle(4), learned(1, 2, 4)), "no room for 3: no explore, and no ordinary grant either")
	wideOne := belief(at(4, 3.9)) // one width, p ≈ 0.99: an ordinary grant takes it to 8
	assert.Equal(t, Grants{Spare: map[JobID]Claim{1: {CPUs: 4, MemoryGB: 2}}}, grant(wideOne, idle(5), learned(1, 4, 2)),
		"room for 5 cores, under the explore's 6: the ordinary grant, which idle cores don't bound")

	assert.Equal(t, Grants{}, grant(narrow, HostIdle{}, learned(1, 2, 4)), "idle cores unknown")
	off := p
	off.Explore = false
	assert.Equal(t, Grants{}, Grant(Admit([]Record{learned(1, 2, 4)}, off, now, nil), []Record{learned(1, 2, 4)}, off, nil,
		func(Record) *Belief { return narrow }, idle(12)), "explore off")

	waiter := Record{ID: 2, Order: 2, State: Queued, Submitted: now, Claim: Claim{CPUs: 13, MemoryGB: 1}, Sources: all(SourceExplicit)}
	assert.Equal(t, Grants{}, grant(narrow, idle(12), learned(1, 2, 4), waiter), "a job left waiting for cores")
	busy := Record{ID: 9, State: Running, Claim: Claim{CPUs: 8, MemoryGB: 2}, Started: now}
	assert.Equal(t, Grants{}, grant(narrow, idle(12), busy, learned(1, 2, 4)), "14 − 8 − 2 leaves only the reserve")

	assert.Equal(t, explored(map[JobID]Claim{1: {CPUs: 1, MemoryGB: 5}}), grant(narrow, idle(12), learned(1, 2, 9)),
		"7 GB past the reserve: 4 cores would add 9, 3 cores add 5")
	assert.Equal(t, explored(map[JobID]Claim{1: {CPUs: 2, MemoryGB: 2}, 2: {CPUs: 1, MemoryGB: 1}}),
		grant(narrow, idle(7), learned(1, 2, 2), learned(2, 2, 2)), "two explorers share 3 idle cores: 2, then 1")

	floored := belief(at(2, 0.97), at(4, 2.0)) // p ≈ 1 from two widths: its base is 4
	assert.Equal(t, explored(map[JobID]Claim{1: {CPUs: 4, MemoryGB: 4}}), grant(floored, idle(12), learned(1, 4, 4)),
		"still gaining: 4 → 8")
}
