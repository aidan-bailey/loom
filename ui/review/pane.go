package reviewui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/aidan-bailey/loom/review"
)

// Pane embeds the vendored crit review model as a workbench panel.
// The app layer owns routing: SetSize before View, HandleKey for
// keystrokes (handled=false means the key is the workbench's),
// HandleMsg for LoadedMsg/SavedMsg deliveries.
type Pane struct {
	m AppModel
}

func (p *Pane) Title() string { return p.m.title }
func (p *Pane) Root() string  { return p.m.root }

// SetSize resizes the pane's viewports. Mirrors the vendored
// WindowSizeMsg handling: recalculateLayout + content rebuild.
func (p *Pane) SetSize(w, h int) {
	p.m.width, p.m.height = w, h
	p.m.recalculateLayout()
	if len(p.m.tabs) > 0 && p.m.tab().state != nil {
		p.m.rebuildContent()
	}
}

// LoadCmd reads all documents and review states off the Update
// goroutine. Deliver the result back via HandleMsg.
func (p *Pane) LoadCmd() tea.Cmd { return p.m.loadDocuments() }

// HandleKey routes a keystroke. While the pane is busy (comment modal,
// tab search, visual selection) it captures everything. While idle it
// claims only the keys it actually acts on (see claimsIdleKey) and
// returns handled=false for the rest, so the workbench keeps its panel
// tabs, session ops, attach and workspace-nav keys — esc included.
// exit=true → the user pressed q: persist Cmd returned, leave the tab.
func (p *Pane) HandleKey(msg tea.KeyPressMsg) (cmd tea.Cmd, handled, exit bool) {
	if !p.m.busy() && !p.m.claimsIdleKey(msg) {
		return nil, false, false
	}
	cmd = p.m.handleKeyPress(msg)
	exit = p.m.exitRequested
	p.m.exitRequested = false
	return cmd, true, exit
}

// HandleMsg applies async deliveries. The app layer routes only
// LoadedMsg and SavedMsg today; any other msg is forwarded to the
// focused viewport (or the modal textarea) should routing ever widen.
func (p *Pane) HandleMsg(msg tea.Msg) tea.Cmd { return p.m.update(msg) }

func (p *Pane) View() string { return p.m.view() }

// Busy reports whether the pane is in a capture-all state (comment
// modal, tab search, visual selection).
func (p *Pane) Busy() bool { return p.m.busy() }

// States exposes every tab's review state for the send bridge.
func (p *Pane) States() []*review.ReviewState {
	out := make([]*review.ReviewState, 0, len(p.m.tabs))
	for i := range p.m.tabs {
		if st := p.m.tabs[i].state; st != nil {
			out = append(out, st)
		}
	}
	return out
}

// CommentCount sums comments across tabs.
func (p *Pane) CommentCount() int {
	n := 0
	for _, s := range p.States() {
		n += len(s.Comments)
	}
	return n
}
