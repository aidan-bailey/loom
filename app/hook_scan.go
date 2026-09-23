package app

import (
	"errors"
	"time"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/hooks"

	tea "charm.land/bubbletea/v2"
)

// hookScanInterval is the hook-event scan cadence, matching rosterInterval:
// the snapshot-path health tick fires every 500ms, far more often than
// hook events need collecting.
const hookScanInterval = 3 * time.Second

// hookScanResult is one instance's scan outcome.
type hookScanResult struct {
	instance *session.Instance
	result   session.HookScanResult
	err      error
}

// hookScanMsg carries a scan of every tracked instance back to Update.
type hookScanMsg struct {
	results []hookScanResult
}

// hookScanCmd builds one scan covering every instance that wants one.
// Requests are built here, on the Update goroutine, because they read the
// tracker; the returned Cmd only touches the filesystem. Returns nil when
// there is nothing to scan.
func hookScanCmd(active []*session.Instance) tea.Cmd {
	type job struct {
		inst *session.Instance
		req  session.HookScanRequest
	}
	var jobs []job
	for _, inst := range active {
		if req, ok := inst.NextHookScan(); ok {
			jobs = append(jobs, job{inst: inst, req: req})
		}
	}
	if len(jobs) == 0 {
		return nil
	}
	return func() tea.Msg {
		now := time.Now()
		results := make([]hookScanResult, 0, len(jobs))
		for _, j := range jobs {
			res, err := session.ScanHooks(j.req, now)
			results = append(results, hookScanResult{instance: j.inst, result: res, err: err})
		}
		return hookScanMsg{results: results}
	}
}

// maybeHookScan returns a scan when gateHookScan is due, following
// maybeRosterQuery: none in flight, and at least hookScanInterval since
// the last dispatch. Returns nil when not due or nothing wants a scan.
// Call on the Update goroutine.
func (m *home) maybeHookScan(active []*session.Instance) tea.Cmd {
	return m.dispatchGated(gateHookScan, time.Now(), func() tea.Cmd {
		return hookScanCmd(active)
	})
}

// handleHookScan applies a scan.
func (m *home) handleHookScan(msg hookScanMsg) {
	for _, r := range msg.results {
		if r.err != nil {
			if errors.Is(r.err, hooks.ErrNoHooks) {
				// The normal state of a session launched without
				// tracking. For a tracked launch the folder vanished
				// mid-run, and its rows would otherwise stay frozen.
				r.instance.ForgetSubagentsWithoutHooks()
			} else {
				log.DebugKV("app.subagent.scan_failed", "instance", r.instance.Title, "err", r.err.Error())
			}
			continue
		}
		r.instance.ApplyHookScan(r.result)
	}
}
