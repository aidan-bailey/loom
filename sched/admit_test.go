package sched

import (
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// admitNow is the clock of every admission test.
var admitNow = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

// Claims the admission tests share, against admitPolicy's 14 cores and 22 GB.
var (
	admitDefault = Claim{CPUs: 4, MemoryGB: 6}
	admitSmall   = Claim{CPUs: 2, MemoryGB: 2}
	admitBig     = Claim{CPUs: 10, MemoryGB: 12}
)

// admitPolicy is fresco's policy: 14 cores, 22 GB, 2 I/O slots, a default
// claim of 4 cores and 6 GB, and a starvation wait of 10 minutes.
func admitPolicy() Policy {
	return Policy{
		Caps:         Caps{CPUs: 14, MemoryGB: 22, IOJobs: 2},
		DefaultClaim: admitDefault,
		StarveAfter:  10 * time.Minute,
		KeepFinished: 50,
	}
}

// queuedJob is a queued job that was submitted waited ago. Its Order is its
// ID, as at submit.
func queuedJob(id JobID, claim Claim, waited time.Duration) Record {
	return Record{ID: id, Order: int64(id), State: Queued, Claim: claim, Submitted: admitNow.Add(-waited)}
}

// runningJob is a job that started a minute ago.
func runningJob(id JobID, claim Claim) Record {
	return Record{
		ID: id, Order: int64(id), State: Running, Claim: claim,
		Submitted: admitNow.Add(-time.Hour), Started: admitNow.Add(-time.Minute),
	}
}

// withOrder is r with the Order of a job moved to the front of the queue.
func withOrder(r Record, order int64) Record {
	r.Order = order
	return r
}

// admitCase is one row of an admission table.
type admitCase struct {
	name    string
	jobs    []Record
	edit    func(*Policy) // nil: admitPolicy as it is
	start   []JobID
	waiting map[JobID]string
}

func runAdmitCases(t *testing.T, cases []admitCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := admitPolicy()
			if tc.edit != nil {
				tc.edit(&p)
			}
			got := Admit(tc.jobs, p, admitNow, nil)
			assert.Equal(t, tc.start, got.Start, "the jobs to start, in order")
			want := tc.waiting
			if want == nil {
				want = map[JobID]string{}
			}
			assert.Equal(t, want, got.Waiting, "why the others wait")
		})
	}
}

func TestAdmit_FirstFit(t *testing.T) {
	runAdmitCases(t, []admitCase{
		{name: "an empty host starts nothing and waits for nothing"},
		{
			name: "first fit starts every job that fits, in queue order",
			jobs: []Record{
				queuedJob(1, admitDefault, time.Minute),
				queuedJob(2, admitDefault, time.Minute),
				queuedJob(3, admitDefault, time.Minute),
				queuedJob(4, admitDefault, time.Minute),
			},
			start:   []JobID{1, 2, 3},
			waiting: map[JobID]string{4: "needs 4 cores (2 free)"},
		},
		{
			name: "running jobs count against the caps",
			jobs: []Record{
				runningJob(1, admitDefault),
				runningJob(2, admitDefault),
				queuedJob(3, admitDefault, time.Minute),
				queuedJob(4, admitDefault, time.Minute),
			},
			start:   []JobID{3},
			waiting: map[JobID]string{4: "needs 4 cores (2 free)"},
		},
		{
			name: "memory is checked after cores",
			jobs: []Record{
				runningJob(1, Claim{CPUs: 2, MemoryGB: 16}),
				queuedJob(2, Claim{CPUs: 2, MemoryGB: 8}, time.Minute),
			},
			waiting: map[JobID]string{2: "needs 8 GB (6 free)"},
		},
		{
			name: "an I/O job waits for a slot, and a job without I/O passes it",
			jobs: []Record{
				runningJob(1, Claim{CPUs: 2, MemoryGB: 2, IO: true}),
				runningJob(2, Claim{CPUs: 2, MemoryGB: 2, IO: true}),
				queuedJob(3, Claim{CPUs: 2, MemoryGB: 2, IO: true}, time.Minute),
				queuedJob(4, admitSmall, time.Minute),
			},
			start:   []JobID{4},
			waiting: map[JobID]string{3: "needs an I/O slot"},
		},
		{
			name: "a small job passes a large one that has not yet waited long enough to count",
			jobs: []Record{
				runningJob(1, Claim{CPUs: 6, MemoryGB: 6}),
				queuedJob(2, admitBig, 10*time.Minute-time.Second),
				queuedJob(3, admitSmall, time.Minute),
			},
			start:   []JobID{3},
			waiting: map[JobID]string{2: "needs 10 cores (8 free)"},
		},
	})
}

func TestAdmit_StarvationGuard(t *testing.T) {
	runAdmitCases(t, []admitCase{
		{
			name: "a head that has waited the starvation time and does not fit holds the rest back",
			jobs: []Record{
				runningJob(1, Claim{CPUs: 6, MemoryGB: 6}),
				queuedJob(2, admitBig, 10*time.Minute),
				queuedJob(3, admitSmall, time.Minute),
			},
			waiting: map[JobID]string{
				2: "needs 10 cores (8 free)",
				3: "waits for job 2, which has waited past 10m",
			},
		},
		{
			name: "a starved head that fits starts alone",
			jobs: []Record{
				queuedJob(1, admitBig, 11*time.Minute),
				queuedJob(2, admitSmall, time.Minute),
			},
			start:   []JobID{1},
			waiting: map[JobID]string{2: "waits for job 1, which has waited past 10m"},
		},
		{
			name: "the head is the first job that is not over a cap",
			jobs: []Record{
				runningJob(5, Claim{CPUs: 6, MemoryGB: 6}),
				queuedJob(1, Claim{CPUs: 32, MemoryGB: 2}, 2*time.Hour),
				queuedJob(2, admitBig, 11*time.Minute),
				queuedJob(3, admitSmall, time.Minute),
			},
			waiting: map[JobID]string{
				1: "over this host's caps",
				2: "needs 10 cores (8 free)",
				3: "waits for job 2, which has waited past 10m",
			},
		},
		{
			name: "the starvation time is the policy's: 20 minutes is not enough for 30",
			edit: func(p *Policy) { p.StarveAfter = 30 * time.Minute },
			jobs: []Record{
				runningJob(1, Claim{CPUs: 6, MemoryGB: 6}),
				queuedJob(2, admitBig, 20*time.Minute),
				queuedJob(3, admitSmall, time.Minute),
			},
			start:   []JobID{3},
			waiting: map[JobID]string{2: "needs 10 cores (8 free)"},
		},
		{
			name: "the starvation time is the policy's: 30 minutes is enough, and the text says 30m",
			edit: func(p *Policy) { p.StarveAfter = 30 * time.Minute },
			jobs: []Record{
				runningJob(1, Claim{CPUs: 6, MemoryGB: 6}),
				queuedJob(2, admitBig, 30*time.Minute),
				queuedJob(3, admitSmall, time.Minute),
			},
			waiting: map[JobID]string{
				2: "needs 10 cores (8 free)",
				3: "waits for job 2, which has waited past 30m",
			},
		},
	})
}

func TestAdmit_OverCapJobsAreSkipped(t *testing.T) {
	over := "over this host's caps"
	runAdmitCases(t, []admitCase{
		{
			name: "an over-cap job is skipped and never the head, however long it has waited",
			jobs: []Record{
				queuedJob(1, Claim{CPUs: 32, MemoryGB: 2}, 2*time.Hour),
				queuedJob(2, admitBig, time.Minute),
				queuedJob(3, admitSmall, time.Minute),
				queuedJob(4, Claim{CPUs: 2, MemoryGB: 23}, 2*time.Hour),
			},
			start:   []JobID{2, 3},
			waiting: map[JobID]string{1: over, 4: over},
		},
		{
			name:  "a claim at a cap is not over it",
			jobs:  []Record{queuedJob(1, Claim{CPUs: 14, MemoryGB: 22}, time.Minute)},
			start: []JobID{1},
		},
		{
			name: "a lowered cap makes a waiting job over it",
			edit: func(p *Policy) { p.Caps.MemoryGB = 4 },
			jobs: []Record{
				queuedJob(1, admitDefault, time.Minute),
				queuedJob(2, admitSmall, time.Minute),
			},
			start:   []JobID{2},
			waiting: map[JobID]string{1: over},
		},
		{
			name: "a zero policy admits nothing: even an exclusive job is over its caps",
			edit: func(p *Policy) { *p = Policy{} },
			jobs: []Record{
				queuedJob(1, admitDefault, time.Minute),
				queuedJob(2, Claim{Exclusive: true}, time.Minute),
			},
			waiting: map[JobID]string{1: over, 2: over},
		},
	})
}

func TestAdmit_ExclusiveClaims(t *testing.T) {
	runAdmitCases(t, []admitCase{
		{
			name: "an exclusive job waits for the host to empty, and first fit passes it",
			jobs: []Record{
				runningJob(1, admitDefault),
				queuedJob(2, Claim{Exclusive: true}, time.Minute),
				queuedJob(3, admitSmall, time.Minute),
			},
			start:   []JobID{3},
			waiting: map[JobID]string{2: "needs 14 cores (10 free)"},
		},
		{
			name: "an exclusive job starts on an empty host and takes every cap",
			jobs: []Record{
				queuedJob(1, Claim{Exclusive: true}, time.Minute),
				queuedJob(2, admitSmall, time.Minute),
			},
			start:   []JobID{1},
			waiting: map[JobID]string{2: "needs 2 cores (0 free)"},
		},
		{
			name: "a running exclusive job leaves nothing free",
			jobs: []Record{
				runningJob(1, Claim{Exclusive: true}),
				queuedJob(2, admitSmall, time.Minute),
			},
			waiting: map[JobID]string{2: "needs 2 cores (0 free)"},
		},
		{
			name: "an exclusive job that waits for I/O slots says how many",
			jobs: []Record{
				runningJob(1, Claim{IO: true}),
				queuedJob(2, Claim{Exclusive: true}, time.Minute),
			},
			waiting: map[JobID]string{2: "needs 2 I/O slots (1 free)"},
		},
		{
			name: "with one I/O slot an exclusive job needs an I/O slot",
			edit: func(p *Policy) { p.Caps.IOJobs = 1 },
			jobs: []Record{
				runningJob(1, Claim{IO: true}),
				queuedJob(2, Claim{Exclusive: true}, time.Minute),
			},
			waiting: map[JobID]string{2: "needs an I/O slot"},
		},
	})
}

func TestAdmit_CapEditsAndQueueOrder(t *testing.T) {
	runAdmitCases(t, []admitCase{
		{
			name: "a lowered cap leaves running jobs alone and nothing free",
			edit: func(p *Policy) { p.Caps.CPUs = 8 },
			jobs: []Record{
				runningJob(1, admitDefault),
				runningJob(2, admitDefault),
				runningJob(3, admitDefault),
				queuedJob(4, admitSmall, time.Minute),
			},
			waiting: map[JobID]string{4: "needs 2 cores (0 free)"},
		},
		{
			name: "a raised cap admits what was waiting",
			edit: func(p *Policy) { p.Caps.CPUs, p.Caps.MemoryGB = 16, 24 },
			jobs: []Record{
				runningJob(1, admitDefault),
				runningJob(2, admitDefault),
				runningJob(3, admitDefault),
				queuedJob(4, admitDefault, time.Minute),
			},
			start: []JobID{4},
		},
		{
			name: "a job moved to the front is first in line",
			edit: func(p *Policy) { p.Caps.CPUs = 4 },
			jobs: []Record{
				queuedJob(1, Claim{CPUs: 4, MemoryGB: 2}, time.Minute),
				withOrder(queuedJob(2, Claim{CPUs: 4, MemoryGB: 2}, time.Minute), 0),
			},
			start:   []JobID{2},
			waiting: map[JobID]string{1: "needs 4 cores (0 free)"},
		},
		{
			name: "finished jobs claim nothing",
			jobs: []Record{
				{ID: 1, State: Done, Claim: Claim{Exclusive: true}},
				{ID: 2, State: Failed, Claim: Claim{CPUs: 14, MemoryGB: 22}},
				{ID: 3, State: Cancelled, Claim: Claim{CPUs: 14, MemoryGB: 22}},
				{ID: 4, State: Lost, Claim: Claim{CPUs: 14, MemoryGB: 22}},
				queuedJob(5, admitDefault, time.Minute),
			},
			start: []JobID{5},
		},
	})
}

// TestAdmit_LeavesItsInputAlone: Admit sorts a copy of the queue, so a caller
// that holds the records in their own order sees them unchanged.
func TestAdmit_LeavesItsInputAlone(t *testing.T) {
	jobs := []Record{
		queuedJob(1, admitDefault, time.Minute),
		withOrder(queuedJob(2, admitDefault, time.Minute), 0),
		runningJob(3, admitSmall),
	}
	before := slices.Clone(jobs)
	_ = Admit(jobs, admitPolicy(), admitNow, nil)
	assert.Equal(t, before, jobs)
}

func TestAdmit_AJobBeingSizedWaitsAndIsNeverTheHead(t *testing.T) {
	p := Policy{Caps: Caps{CPUs: 8, MemoryGB: 16, IOJobs: 2}, DefaultClaim: Claim{CPUs: 4, MemoryGB: 6}, StarveAfter: time.Minute}
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	jobs := []Record{
		{ID: 1, Order: 1, State: Queued, Claim: Claim{CPUs: 4, MemoryGB: 6}, Sizing: SizingAsking, Submitted: now.Add(-time.Hour)},
		{ID: 2, Order: 2, State: Queued, Claim: Claim{CPUs: 8, MemoryGB: 6}, Submitted: now},
	}
	d := Admit(jobs, p, now, nil)
	assert.Equal(t, []JobID{2}, d.Start, "the job being sized holds nothing back, starved or not")
	assert.Equal(t, "being sized (asking the agent)", d.Waiting[1])
}

// heldJob is a job held after job after, submitted waited ago.
func heldJob(id, after JobID, claim Claim, waited time.Duration) Record {
	return Record{ID: id, Order: int64(id), State: Held, After: after, Claim: claim, Submitted: admitNow.Add(-waited)}
}

// releasedJob is a queued job whose chain was submitted waited ago and that
// joined the queue ago.
func releasedJob(id JobID, claim Claim, waited, ago time.Duration) Record {
	r := queuedJob(id, claim, waited)
	r.Ready = admitNow.Add(-ago)
	return r
}

func TestAdmit_StarvationCountsFromReady(t *testing.T) {
	runAdmitCases(t, []admitCase{
		{
			name: "a job released a minute ago is no starving head, though its chain began half an hour ago",
			jobs: []Record{
				runningJob(1, Claim{CPUs: 6, MemoryGB: 6}),
				releasedJob(2, admitBig, 30*time.Minute, time.Minute),
				queuedJob(3, admitSmall, time.Minute),
			},
			start:   []JobID{3},
			waiting: map[JobID]string{2: "needs 10 cores (8 free)"},
		},
		{
			name: "a job released past the starvation time starves as any job does",
			jobs: []Record{
				runningJob(1, Claim{CPUs: 6, MemoryGB: 6}),
				releasedJob(2, admitBig, 30*time.Minute, 10*time.Minute),
				queuedJob(3, admitSmall, time.Minute),
			},
			waiting: map[JobID]string{
				2: "needs 10 cores (8 free)",
				3: "waits for job 2, which has waited past 10m",
			},
		},
		{
			name: "a held job is never admitted, never the head and never said to wait",
			jobs: []Record{
				heldJob(1, 9, admitSmall, time.Hour),
				queuedJob(2, admitSmall, time.Minute),
			},
			start: []JobID{2},
		},
	})
}

func TestAdmit_CountsARunningJobAtItsHold(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	p := Policy{Caps: Caps{CPUs: 4, MemoryGB: 8, IOJobs: 2}, StarveAfter: time.Hour}
	running := Record{ID: 1, Order: 1, State: Running, Claim: Claim{CPUs: 4, MemoryGB: 8}}
	queued := Record{ID: 2, Order: 2, State: Queued, Submitted: now, Claim: Claim{CPUs: 2, MemoryGB: 4}}
	jobs := []Record{running, queued}

	assert.Equal(t, "needs 2 cores (0 free)", Admit(jobs, p, now, nil).Waiting[2], "a job with no hold counts at its claim")
	assert.Equal(t, []JobID{2}, Admit(jobs, p, now, map[JobID]Need{1: {CPUs: 2, MemoryGB: 2}}).Start)
	assert.Equal(t, "needs 2 cores (1 free)", Admit(jobs, p, now, map[JobID]Need{1: {CPUs: 3, MemoryGB: 2}}).Waiting[2])
	assert.Equal(t, "needs 4 GB (0 free)", Admit(jobs, p, now, map[JobID]Need{1: {CPUs: 2, MemoryGB: 9}}).Waiting[2], "a hold above what is free starts nothing")

	running.Claim.Exclusive = true
	jobs[0] = running
	assert.Empty(t, Admit(jobs, p, now, map[JobID]Need{1: {CPUs: 1, MemoryGB: 1}}).Start, "an exclusive claim ignores a hold")
}
