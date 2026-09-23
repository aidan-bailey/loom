package app

import (
	"errors"
	"time"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/hooks"

	tea "charm.land/bubbletea/v2"
)

// hookScanInterval is the minimum time between hook scans. Scans are
// triggered by pane output and quiet as well as the health tick, so an
// event is read within about this long; a warm scan is one readdir per
// hooked instance.
const hookScanInterval = 250 * time.Millisecond

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

// handleHookScan applies a scan. An instance whose Claude status changed
// moves to it at once, and any change also asks the roster to confirm:
// its answer, stamped after the event, corrects a Stop that ended a turn
// but not the work (a lead about to pick up a teammate's reply).
func (m *home) handleHookScan(msg hookScanMsg) tea.Cmd {
	changed := false
	for _, r := range msg.results {
		if r.err != nil {
			if errors.Is(r.err, hooks.ErrNoHooks) {
				// The normal state of a session launched without
				// hooks. For a hooked launch the folder vanished
				// mid-run, and its rows would otherwise stay frozen.
				r.instance.ForgetSubagentsWithoutHooks()
			} else {
				log.DebugKV("app.hook_scan.failed", "instance", r.instance.Title, "err", r.err.Error())
			}
			continue
		}
		st0, why0, ok0 := r.instance.ClaudeStatus()
		r.instance.ApplyHookScan(r.result)
		st1, why1, ok1 := r.instance.ClaudeStatus()
		if st0 != st1 || why0 != why1 || ok0 != ok1 {
			changed = true
			m.applyClaudeStatus(r.instance)
		}
	}
	if !changed {
		return nil
	}
	m.updateTabBarStatuses()
	m.gate(gateRoster).request()
	return m.maybeRosterQuery(m.activeInstances())
}
