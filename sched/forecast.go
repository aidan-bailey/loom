package sched

import (
	"slices"
	"time"
)

// The forecast's bounds (smart scheduler spec §5.5).
const (
	forecastSteps   = 1000
	forecastHorizon = 24 * time.Hour
)

// Forecast is when each queued job is expected to start (spec §5.5), a pure
// function of its arguments: it runs Admit forward from now. A running job
// is expected to end at its start plus its usual duration (usual's answer,
// or fallback when it has none), or a minute from now when that has passed.
// At each expected end the job's claim is freed and Admit runs again with
// the clock at that time, so the starvation guard and the over-cap skip
// apply as they will. Jobs being sized are left out. A held job joins the
// simulated queue when its prerequisite's expected end comes, at the lower of
// its order and its prerequisite's, as a release does (dependent jobs spec
// §5.3); one whose prerequisite never ends in the simulation gets no start.
// It stops after
// forecastSteps steps or past forecastHorizon; a job it hasn't started by
// then has no entry.
func Forecast(jobs []Record, p Policy, now time.Time, usual func(Record) (time.Duration, bool), fallback time.Duration) map[JobID]time.Time {
	starts := map[JobID]time.Time{}
	if !p.usable() {
		return starts
	}
	expect := func(r Record) time.Duration {
		if d, ok := usual(r); ok && d > 0 {
			return d
		}
		return fallback
	}
	type ending struct {
		id JobID
		at time.Time
	}
	var sim []Record
	var ends []ending
	for _, r := range jobs {
		switch {
		case r.State == Running:
			at := r.Started.Add(expect(r))
			if !at.After(now) {
				at = now.Add(time.Minute)
			}
			ends = append(ends, ending{r.ID, at})
			sim = append(sim, r)
		case r.State.Waiting() && r.Sizing == "":
			sim = append(sim, r)
		}
	}
	index := func(id JobID) int { return slices.IndexFunc(sim, func(r Record) bool { return r.ID == id }) }
	clock := now
	for range forecastSteps {
		for _, id := range Admit(sim, p, clock).Start {
			i := index(id)
			sim[i].State, sim[i].Started = Running, clock
			starts[id] = clock
			ends = append(ends, ending{id, clock.Add(expect(sim[i]))})
		}
		if !slices.ContainsFunc(sim, func(r Record) bool { return r.State.Waiting() }) || len(ends) == 0 {
			break
		}
		slices.SortFunc(ends, func(a, b ending) int { return a.at.Compare(b.at) })
		e := ends[0]
		ends = ends[1:]
		if e.at.Sub(now) > forecastHorizon {
			break
		}
		if e.at.After(clock) {
			clock = e.at
		}
		sim[index(e.id)].State = Done
		releaseHeld(sim, e.id, clock)
	}
	return starts
}

// releaseHeld is a release in the simulation (dependent jobs spec §5.3):
// each held job after job id, which has just ended, joins the queue at at,
// at the lower of its order and id's.
func releaseHeld(sim []Record, id JobID, at time.Time) {
	i := slices.IndexFunc(sim, func(r Record) bool { return r.ID == id })
	if i < 0 {
		return
	}
	order := sim[i].Order
	for j := range sim {
		if sim[j].State == Held && sim[j].After == id {
			sim[j].State, sim[j].Ready, sim[j].Order = Queued, at, min(sim[j].Order, order)
		}
	}
}
