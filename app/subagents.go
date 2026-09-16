package app

import (
	"errors"
	"time"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/subagent"

	tea "charm.land/bubbletea/v2"
)

// subagentInterval is the hook-event scan cadence, matching rosterInterval:
// the snapshot-path health tick fires every 500ms, far more often than
// hook events need collecting.
const subagentInterval = 3 * time.Second

// subagentScanResult is one instance's scan outcome.
type subagentScanResult struct {
	instance *session.Instance
	result   subagent.Result
	err      error
}

// subagentScanMsg carries a scan of every tracked instance back to Update.
type subagentScanMsg struct {
	results []subagentScanResult
}

// subagentScanCmd builds one scan covering every instance that wants one.
// Requests are built here, on the Update goroutine, because they read the
// tracker; the returned Cmd only touches the filesystem. Returns nil when
// there is nothing to scan.
func subagentScanCmd(active []*session.Instance) tea.Cmd {
	type job struct {
		inst *session.Instance
		req  subagent.Request
	}
	var jobs []job
	for _, inst := range active {
		if req, ok := inst.SubagentScanRequest(); ok {
			jobs = append(jobs, job{inst: inst, req: req})
		}
	}
	if len(jobs) == 0 {
		return nil
	}
	return func() tea.Msg {
		now := time.Now()
		results := make([]subagentScanResult, 0, len(jobs))
		for _, j := range jobs {
			res, err := subagent.Scan(j.req, now)
			results = append(results, subagentScanResult{instance: j.inst, result: res, err: err})
		}
		return subagentScanMsg{results: results}
	}
}

// maybeSubagentScan returns a scan when one is due, following
// maybeRosterQuery: none in flight, and at least subagentInterval since
// the last dispatch. When nothing is dispatched neither field is armed,
// since no scan means no subagentScanMsg to clear them. Call on the Update
// goroutine.
func (m *home) maybeSubagentScan(active []*session.Instance) tea.Cmd {
	if m.subagentInFlight || time.Since(m.lastSubagentScan) < subagentInterval {
		return nil
	}
	cmd := subagentScanCmd(active)
	if cmd == nil {
		return nil
	}
	m.subagentInFlight = true
	m.lastSubagentScan = time.Now()
	return cmd
}

// handleSubagentScan applies a scan. It clears the in-flight flag first: a
// delivery that did not would stop scanning for the rest of the session.
func (m *home) handleSubagentScan(msg subagentScanMsg) {
	m.subagentInFlight = false
	for _, r := range msg.results {
		if r.err != nil {
			if errors.Is(r.err, subagent.ErrNoHooks) {
				// The normal state of a session launched without
				// tracking. For a tracked launch the folder vanished
				// mid-run, and its rows would otherwise stay frozen.
				r.instance.ForgetSubagentsWithoutHooks()
			} else {
				log.DebugKV("app.subagent.scan_failed", "instance", r.instance.Title, "err", r.err.Error())
			}
			continue
		}
		r.instance.ApplySubagentScan(r.result)
	}
}
