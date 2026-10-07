package app

import (
	"fmt"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/aidan-bailey/loom/session/launch"

	tea "charm.land/bubbletea/v2"
)

// draft is a creation flow's session-to-be. It belongs to the TUI alone:
// the model hears of it only when the flow confirms (Create). Until then it
// is a row with ID 0 at the end of its slot's rail. Cancelling the flow
// discards it; there is no instance to kill. One draft at a time.
type draft struct {
	slot    *workspaceSlot
	path    string // the repo it will work in (m.repoPath() when the flow began)
	title   string
	prompt  string
	program string
	branch  string // the branch picker's choice; "" for a new branch
	issue   int
	created time.Time
}

// row is the draft as the rail shows it.
func (d *draft) row(m *home) core.InstanceView {
	v := core.InstanceView{
		Title:       d.title,
		RepoPath:    d.path,
		Program:     d.program,
		Status:      session.Ready,
		StatusSince: d.created,
		Issue:       d.issue,
	}
	if d.issue != 0 {
		snap, known := m.core.GitHubSnapshot(d.path)
		v.GitHub = github.StateFor(snap, known, "", d.issue)
	}
	return v
}

// draftRow returns s's draft row, when the open draft belongs to s.
func (m *home) draftRow(s *workspaceSlot) (core.InstanceView, bool) {
	if m.draft == nil || m.draft.slot != s {
		return core.InstanceView{}, false
	}
	return m.draft.row(m), true
}

// newDraft opens a draft in the focused slot, selects its row and returns it.
func (m *home) newDraft(title, prompt string, issue int) *draft {
	m.draft = &draft{
		slot:    m.workspaceSlot,
		path:    m.repoPath(),
		title:   title,
		prompt:  prompt,
		program: m.core.Program(),
		issue:   issue,
		created: time.Now(),
	}
	m.list.SetSelectedInstance(m.list.NumInstances() - 1)
	m.refreshSelection()
	return m.draft
}

// discardDraft drops the open draft, if any. Every creation-flow cancel path
// goes through it; nothing in the model needs undoing.
func (m *home) discardDraft() {
	m.draft = nil
	m.refreshSelection()
}

// pendingCreate is a confirmed draft waiting for its Create's Reply.
type pendingCreate struct {
	slot  *workspaceSlot
	title string
}

// confirmDraft sends d's Create, with opts from the Session Launch Options
// modal, and drops the draft. The new instance's row takes the draft's
// place (both are appended), and createReplied selects it.
func (m *home) confirmDraft(d *draft, opts launch.Options) {
	req := m.newReq(pendingReq{create: &pendingCreate{slot: d.slot, title: d.title}})
	if m.draft == d {
		m.draft = nil
	}
	m.core.Create(d.slot.id, core.NewInstance{
		Title:   d.title,
		Path:    d.path,
		Program: d.program,
		Prompt:  d.prompt,
		Branch:  d.branch,
		Issue:   d.issue,
		Launch:  opts,
		Start:   true,
	}, req)
}

// createReplied finishes a confirmed draft: a refusal is shown; otherwise
// the new row is selected where the draft's was, if the selection is
// still there. The start's own completion (core.Started) follows later and
// does the rest.
func (m *home) createReplied(p *pendingCreate, r core.Reply) tea.Cmd {
	if r.Err != nil {
		return m.handleError(fmt.Errorf("create %s: %w", p.title, r.Err))
	}
	if s := p.slot; s.list != nil {
		if sel := s.list.GetSelectedInstance(); sel == nil || sel.ID == 0 || sel.ID == r.ID {
			s.list.SelectID(r.ID)
		}
	}
	m.refreshSelection()
	return nil
}
