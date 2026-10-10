package core

import (
	"errors"
	"reflect"
	"slices"
	"time"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/hooks"
	"github.com/aidan-bailey/loom/session/subagent"
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

// hookScanResults carries a scan of every tracked instance back to the
// model.
type hookScanResults struct {
	results []hookScanResult
}

// hookScanJob builds one scan covering every instance that wants one.
// Requests are built here, on the model's goroutine, because they read the
// tracker; the returned Job only touches the filesystem. Returns nil when
// there is nothing to scan.
func hookScanJob(active []*session.Instance) Job {
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
	return func() any {
		now := time.Now()
		results := make([]hookScanResult, 0, len(jobs))
		for _, j := range jobs {
			res, err := session.ScanHooks(j.req, now)
			results = append(results, hookScanResult{instance: j.inst, result: res, err: err})
		}
		return hookScanResults{results: results}
	}
}

// maybeHookScan dispatches a scan when gateHookScan is due, following
// maybeRosterQuery: none in flight, and at least hookScanInterval since
// the last dispatch. Reports false when not due or nothing wants a scan.
// Call on the loop goroutine.
func (m *Model) maybeHookScan(active []*session.Instance) bool {
	return m.dispatchGated(gateHookScan, time.Now(), func() Job {
		return hookScanJob(active)
	})
}

// HookTick is the loop's hook timer (hookInterval): a hook scan when one is
// due (maybeHookScan: none in flight, hookScanInterval since the last
// dispatch), and only while an active Claude instance exists, since only
// a Claude session writes hook events. With none it does nothing: no job,
// no gate armed. Either way it changes nothing a client sees (noWake): the
// scan's result wakes the client if it moved anything. The loop's timer
// calls it; Loop.HookTickForTest runs it in tests.
func (m *Model) HookTick() {
	m.noWake = true
	active := m.activeInstances()
	if !slices.ContainsFunc(active, func(inst *session.Instance) bool { return session.IsClaudeProgram(inst.Program()) }) {
		return
	}
	m.maybeHookScan(active)
}

// hookFace is what a hook scan can change of an instance: what its view
// shows (its status and Claude's, the wait reason, the last message, the
// subagent rows) and the conversation the model saves.
type hookFace struct {
	status, claude   session.Status
	why, wait        string
	reported         bool
	conv, transcript string
	lastMsg          string
	hasLastMsg       bool
	subagents        []subagent.View
}

// hookFaceOf reads inst's hookFace.
func hookFaceOf(inst *session.Instance) hookFace {
	f := hookFace{status: inst.GetStatus(), wait: inst.WaitReason(), subagents: inst.Subagents()}
	f.claude, f.why, f.reported = inst.ClaudeStatus()
	f.conv, f.transcript = inst.ClaudeSession()
	f.lastMsg, f.hasLastMsg = inst.LastMessage()
	return f
}

// deliverHookScan applies a scan. An instance whose Claude status changed
// moves to it at once, and any change also asks the roster to confirm:
// its answer, stamped after the event, corrects a Stop that ended a turn
// but not the work (a lead about to pick up a teammate's reply). A scan
// that names another conversation for an instance (its SessionStart)
// saves the workspace holding it (saveUnprompted), since a relaunch
// resumes the recorded one. A scan that moved nothing of any instance's
// hookFace (an idle Claude's, every 250ms: Loop's hook timer) sets
// noWake, so the loop has no client publish for it.
func (m *Model) deliverHookScan(msg hookScanResults) {
	changed, moved := false, false
	var resumed []*Workspace
	for _, r := range msg.results {
		if r.err != nil && !errors.Is(r.err, hooks.ErrNoHooks) {
			log.DebugKV("core.hook_scan.failed", "instance", r.instance.Title, "err", r.err.Error())
			continue
		}
		before := hookFaceOf(r.instance)
		if r.err != nil {
			// ErrNoHooks: the normal state of a session launched
			// without hooks. For a hooked launch the folder vanished
			// mid-run, and its rows would otherwise stay frozen.
			r.instance.ForgetSubagentsWithoutHooks()
		} else {
			r.instance.ApplyHookScan(r.result)
		}
		after := hookFaceOf(r.instance)
		if after.conv != before.conv || after.transcript != before.transcript {
			if ws := m.holding(r.instance); ws != nil && !slices.Contains(resumed, ws) {
				resumed = append(resumed, ws)
			}
		}
		// Only a scan's events move the status: a vanished folder drops
		// its rows and Claude's observation, and adopts nothing.
		if r.err == nil && (after.claude != before.claude || after.why != before.why || after.reported != before.reported) {
			changed = true
			m.applyClaudeStatus(r.instance)
			after = hookFaceOf(r.instance)
		}
		if !reflect.DeepEqual(before, after) {
			moved = true
		}
	}
	for _, ws := range resumed {
		m.saveUnprompted(ws, "conversation")
	}
	if !moved {
		m.noWake = true
	}
	if !changed {
		return
	}
	m.emit(StatusesChanged{})
	m.gate(gateRoster).request()
	m.maybeRosterQuery(m.activeInstances())
}
