package app

import (
	"context"
	"fmt"
	"sort"
	"time"

	tea "charm.land/bubbletea/v2"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/aidan-bailey/loom/ui"
	"github.com/aidan-bailey/loom/ui/overlay"
)

// issuePickedMsg carries the full issue (with body) after the user
// chose a row, or the View error. repo is the workspace the pick was
// made in: the fetch is async and the user stays fully interactive
// while it runs, so the result can land in a different world than the
// one that asked for it.
type issuePickedMsg struct {
	repo  string
	issue github.Issue
	err   error
}

// issueRows lists the focused repo's open issues, newest first.
func (m *home) issueRows() []overlay.IssueRow {
	snap, ok := m.ghState[m.repoPath()]
	if !ok {
		return nil
	}
	rows := make([]overlay.IssueRow, 0, len(snap.Issues))
	for _, is := range snap.Issues {
		if is.Closed {
			continue
		}
		rows = append(rows, overlay.IssueRow{Number: is.Number, Title: is.Title, Labels: is.Labels})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Number > rows[j].Number })
	return rows
}

// issuePickerStatus is the picker's status line for the current poll
// state: "" once a snapshot exists, "loading…" before.
func (m *home) issuePickerStatus() string {
	if _, ok := m.ghState[m.repoPath()]; ok {
		return ""
	}
	return "loading…"
}

// runNewFromIssue opens the issue picker. When no snapshot exists yet
// it opens in the loading state and forces the next tick to poll.
func runNewFromIssue(m *home) (tea.Model, tea.Cmd) {
	if m.list.NumInstances() >= GlobalInstanceLimit {
		return m, m.handleError(fmt.Errorf("you can't create more than %d instances", GlobalInstanceLimit))
	}
	if m.ghAvailable.checked && !m.ghAvailable.ok {
		return m, m.handleError(fmt.Errorf("gh unavailable: %s", m.ghAvailable.reason))
	}
	p := overlay.NewIssuePicker(m.issueRows())
	p.SetStatus(m.issuePickerStatus())
	if _, ok := m.ghState[m.repoPath()]; !ok {
		m.lastGHQuery = time.Time{}
	}
	m.setOverlay(p, overlayIssuePicker)
	m.state = stateIssuePicker
	return m, nil
}

// handleStateIssuePickerKey drives the picker. Enter fetches the full
// issue in a Cmd; the instance is created in handleIssuePicked.
func handleStateIssuePickerKey(m *home, msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := m.issuePicker()
	if p == nil {
		return m, nil
	}
	committed, canceled := p.HandleKeyPress(msg)
	if !committed {
		return m, nil
	}
	row := p.Selected()
	m.dismissOverlay()
	m.state = stateDefault
	if canceled || row == nil {
		return m, nil
	}
	repo := m.repoPath()
	n := row.Number
	return m, func() tea.Msg {
		is, err := github.View(context.Background(), repo, n, internalexec.Default{})
		return issuePickedMsg{repo: repo, issue: is, err: err}
	}
}

// handleIssuePicked creates the pre-started instance titled by the
// issue slug, seeds its prompt, links the issue, and opens the launch
// options modal — the same path n/N take after title entry.
func (m *home) handleIssuePicked(msg issuePickedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m, m.handleError(fmt.Errorf("fetch issue: %w", msg.err))
	}
	// The fetch is async and the user stays interactive during it, so
	// this result can arrive into a different world than the one that
	// asked for it. Two ways that matters:
	//
	//   - a different workspace is focused now, and creating here would
	//     seed an agent with issue text from another repository;
	//   - some other flow is on screen, whose overlay and pending
	//     launch-options closure this would silently replace, stranding
	//     its instance unstarted and unreachable.
	//
	// Both drop the result with an explanation rather than applying it.
	if msg.repo != m.repoPath() {
		return m, m.handleError(fmt.Errorf("issue #%d was picked in another workspace; not creating a session here", msg.issue.Number))
	}
	if m.state != stateDefault {
		return m, m.handleError(fmt.Errorf("issue #%d arrived while another session was being created; press I again", msg.issue.Number))
	}
	if m.list.NumInstances() >= GlobalInstanceLimit {
		return m, m.handleError(fmt.Errorf("you can't create more than %d instances", GlobalInstanceLimit))
	}
	instance, err := session.NewInstance(session.InstanceOptions{
		Title:     github.SlugTitle(msg.issue),
		Path:      m.repoPath(),
		Program:   m.program,
		ConfigDir: m.configDir(),
	})
	if err != nil {
		return m, m.handleError(err)
	}
	instance.Prompt = github.SeedPrompt(msg.issue)
	instance.SetIssue(msg.issue.Number)
	m.list.AddInstance(instance)
	m.list.SetSelectedInstance(m.list.NumInstances() - 1)
	m.lastGHQuery = time.Time{}
	m.applyGitHubState()
	return m.openLaunchOptionsForNew(instance, "")
}

// openLaunchOptionsForNew shows the Session Launch Options modal for an
// unstarted instance already in the list. Confirming composes the
// program from the chosen options and starts the instance; cancelling
// pops it (killPendingLaunchOptionsCancel). selectedBranch is threaded
// to instanceStartedMsg for the N flow's branch picker.
func (m *home) openLaunchOptionsForNew(instance *session.Instance, selectedBranch string) (tea.Model, tea.Cmd) {
	m.pendingLaunchOptions = func(opts overlay.LaunchOptions) (tea.Model, tea.Cmd) {
		startTask := overlay.ConfirmationTask{
			Sync: func() {
				instance.Program = applyLaunchOptions(opts, m.rcAuth, instance.Program, instance.Title)
				instance.HeadroomProxy = opts.HeadroomProxy
				instance.CacheTTL1h = opts.CacheTTL1h
				// Always recorded, edited or not, so branch composition has a
				// single source of truth instead of falling back to a re-read
				// of config.json inside the git package.
				instance.SetBranchPrefix(opts.BranchPrefix)
				_ = instance.TransitionTo(session.Loading)
				m.promptAfterName = false
				m.state = stateDefault
				m.menu.SetState(ui.StateDefault)
			},
			Async: tea.Batch(tea.RequestWindowSize, func() tea.Msg {
				err := instance.Start(true)
				return instanceStartedMsg{
					instance:        instance,
					err:             err,
					promptAfterName: false,
					selectedBranch:  selectedBranch,
				}
			}),
		}
		if m.remoteControlBlocked(effectiveRemoteControl(opts), instance.Program) {
			return m, m.promptRemoteControlBlocked(startTask)
		}
		return m, tea.Batch(startTask.Run(), m.instanceChanged())
	}
	m.pendingLaunchOptionsCancel = m.killPendingLaunchOptionsCancel
	m.state = stateLaunchOptions
	m.setOverlay(overlay.NewSessionLaunchOptions(launchOptionsFromConfig(m.appConfig), m.rcAuth.Blocked(), m.rcAuth.Reason), overlayLaunchOptions)
	m.menu.SetState(ui.StateNewInstance)
	return m, tea.RequestWindowSize
}
