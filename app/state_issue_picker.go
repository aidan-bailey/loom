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

// issueExpandedMsg is the #n shorthand's result: on success the seeded
// prompt replaces the token; on error the literal prompt launches
// unchanged and unlinked.
type issueExpandedMsg struct {
	instance *session.Instance
	// repo is the workspace the prompt was submitted in. The instance is
	// already bound to its own worktree, so a late result cannot create
	// it in the wrong place — but openLaunchOptionsForNew would still
	// open the modal on whichever workspace is focused when this lands,
	// for an instance in another slot's list.
	repo           string
	number         int
	issue          github.Issue
	rest           string // user text after the #n token
	literal        string // original prompt, used when err != nil
	selectedBranch string
	err            error
}

// issueExpandCmd fetches issue n for the shorthand.
func issueExpandCmd(repo string, n int, inst *session.Instance, rest, literal, selectedBranch string) tea.Cmd {
	return func() tea.Msg {
		is, err := github.View(context.Background(), repo, n, internalexec.Default{})
		return issueExpandedMsg{instance: inst, repo: repo, number: n, issue: is, rest: rest, literal: literal, selectedBranch: selectedBranch, err: err}
	}
}

// issueExpandDropped explains a #n expansion that was dropped by a
// guard. The fetch error is folded into the same string because errBox
// shows one message at a time — reporting only the guard would assert
// an expansion that may in fact have failed. The instance is left
// unstarted with no path back into launch options (r/R only act on
// Paused/Recoverable status, and n/N always append a new instance), so
// the message says how to actually get rid of it.
func issueExpandDropped(msg issueExpandedMsg, title, why string) error {
	if msg.err != nil {
		return fmt.Errorf("issue #%d not expanded (%v) and %s; %q left unstarted — discard it with D", msg.number, msg.err, why, title)
	}
	return fmt.Errorf("issue #%d not expanded because %s; %q left unstarted — discard it with D", msg.number, why, title)
}

// handleIssueExpanded finishes the N flow after a #n expansion. The
// fetch is async and the user stays interactive during it (the prompt
// overlay is dismissed the moment the shorthand is spotted — see
// handleStatePromptKey), so this can land while another flow is on
// screen — whose overlay and pending launch-options closure
// openLaunchOptionsForNew would silently replace, stranding its
// instance unstarted. Matches the guard handleIssuePicked uses for the
// same reason. It can also land after the user switched workspace tabs:
// inst stays correctly bound to its own repo either way, but
// openLaunchOptionsForNew would open the modal on whichever workspace
// is now focused, for an instance living in another slot's list — so
// that is checked too, same as handleIssuePicked's repo guard.
//
// Both guards sit above the msg.err != nil branch below, unlike
// handleIssuePicked's error check which returns early. Here err!=nil
// is a degrade-and-continue branch — it still calls
// openLaunchOptionsForNew with the literal prompt — so if either guard
// ran after it, a failed fetch could still pop the modal on the wrong
// workspace or on top of another flow. Keep the guards first.
func (m *home) handleIssueExpanded(msg issueExpandedMsg) (tea.Model, tea.Cmd) {
	inst := msg.instance
	if inst == nil || inst.Started() {
		return m, nil
	}
	if msg.repo != m.repoPath() {
		return m, m.handleError(issueExpandDropped(msg, inst.Title, "its workspace is no longer focused"))
	}
	if m.state != stateDefault {
		return m, m.handleError(issueExpandDropped(msg, inst.Title, "another session was being created"))
	}
	var errCmd tea.Cmd
	if msg.err != nil {
		inst.Prompt = msg.literal
		errCmd = m.handleError(fmt.Errorf("issue #%d not expanded: %w", msg.number, msg.err))
	} else {
		inst.Prompt = github.SeedPrompt(msg.issue)
		if msg.rest != "" {
			inst.Prompt += "\n" + msg.rest + "\n"
		}
		inst.SetIssue(msg.issue.Number)
		m.lastGHQuery = time.Time{}
		m.applyGitHubState()
	}
	_, cmd := m.openLaunchOptionsForNew(inst, msg.selectedBranch)
	return m, tea.Batch(cmd, errCmd)
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
