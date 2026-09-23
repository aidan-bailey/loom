package subagent

import (
	"os"
	"slices"

	"github.com/aidan-bailey/loom/session/hooks"
)

// ReadMeta reads the metadata sidecars for the SubagentStart events in
// events and for the agents still missing one. An unreadable sidecar is
// skipped; the tracker keeps the agent hidden and the next scan retries
// it. It only touches the filesystem, so it is safe to run from a tea.Cmd.
func ReadMeta(events []hooks.Event, missing []MetaRef) map[string]Meta {
	refs := slices.Clone(missing)
	for _, ev := range events {
		if ev.Name != hooks.EventSubagentStart {
			continue
		}
		if p := MetaPath(ev.TranscriptPath, ev.AgentID); p != "" {
			refs = append(refs, MetaRef{AgentID: ev.AgentID, Path: p})
		}
	}
	meta := map[string]Meta{}
	for _, r := range refs {
		if _, done := meta[r.AgentID]; done {
			continue
		}
		data, err := os.ReadFile(r.Path)
		if err != nil {
			continue
		}
		m, err := ParseMeta(data)
		if err != nil {
			continue
		}
		meta[r.AgentID] = m
	}
	return meta
}
