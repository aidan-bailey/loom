package app

import (
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
)

// paneSnapshot resolves each instance's pane on the Update goroutine, for
// use by a Cmd that must not read the model.
func (m *home) paneSnapshot(insts []*session.Instance) map[*session.Instance]ui.Pane {
	out := make(map[*session.Instance]ui.Pane, len(insts))
	for _, inst := range insts {
		out[inst] = m.panes.For(inst)
	}
	return out
}
