package session

import (
	"time"

	"github.com/aidan-bailey/loom/session/hooks"
	"github.com/aidan-bailey/loom/session/subagent"
)

// HookScanRequest describes one instance's hook scan: the folder to scan,
// whether to replay its history, and the subagents still waiting for a
// metadata sidecar. Build it with Instance.NextHookScan on the Update
// goroutine; ScanHooks runs it off that goroutine.
type HookScanRequest struct {
	Dir         string
	Cold        bool
	MissingMeta []subagent.MetaRef
}

// HookScanResult is one instance's scan: the events found plus the
// sidecars read for them. Apply it with Instance.ApplyHookScan.
type HookScanResult struct {
	LaunchID string
	Events   []hooks.Event
	Meta     map[string]subagent.Meta
	Replayed bool
}

// ScanHooks runs one instance's scan: hooks.Scan, then the subagent
// sidecars for its SubagentStart events and req.MissingMeta. It only
// touches the filesystem, so it is safe to run from a tea.Cmd.
func ScanHooks(req HookScanRequest, now time.Time) (HookScanResult, error) {
	res, err := hooks.Scan(hooks.Request{Dir: req.Dir, Cold: req.Cold}, now)
	if err != nil {
		return HookScanResult{}, err
	}
	return HookScanResult{
		LaunchID: res.LaunchID,
		Events:   res.Events,
		Meta:     subagent.ReadMeta(res.Events, req.MissingMeta),
		Replayed: res.Replayed,
	}, nil
}
