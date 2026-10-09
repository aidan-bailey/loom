package app

import (
	"fmt"
	"sort"

	tea "charm.land/bubbletea/v2"

	"github.com/aidan-bailey/loom/session/github"
	"github.com/aidan-bailey/loom/session/launch"
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
	snap, ok := m.core.GitHubSnapshot(m.repoPath())
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
// state: "" once a snapshot exists, the last error when the repo's poll
// failed, "loading…" before the first result. The error case matters
// because a repo with no GitHub remote fails every poll while
// ghAvailable stays ok, so without it the picker waits forever on a
// result that will never come.
func (m *home) issuePickerStatus() string {
	repo := m.repoPath()
	if _, ok := m.core.GitHubSnapshot(repo); ok {
		return ""
	}
	if err := m.core.GitHubErr(repo); err != nil {
		return "gh unavailable: " + err.Error()
	}
	return "loading…"
}

// runNewFromIssue opens the issue picker. It asks the model to cover its
// repository on every open (WatchGitHub): in global mode that is the
// directory loom started in, which no poll covers unless a global session
// runs there, so the picker would otherwise wait on "loading…" for good,
// and the model stops polling a repository nobody has asked about for a
// while. When no snapshot exists yet the picker opens in the loading
// state, and the model polls at the next tick.
func runNewFromIssue(m *home) (tea.Model, tea.Cmd) {
	if m.list.NumInstances() >= GlobalInstanceLimit {
		return m, m.handleError(fmt.Errorf("you can't create more than %d instances", GlobalInstanceLimit))
	}
	if m.core.GitHubUnavailable() {
		return m, m.handleError(fmt.Errorf("gh unavailable: %s", m.core.GitHubUnavailableReason()))
	}
	p := overlay.NewIssuePicker(m.issueRows())
	p.SetStatus(m.issuePickerStatus())
	m.core.WatchGitHub(m.repoPath())
	m.setOverlay(p, overlayIssuePicker)
	m.state = stateIssuePicker
	return m, nil
}

// handleStateIssuePickerKey drives the picker. Enter fetches the full
// issue through the model (core.FetchIssue); its Reply reaches
// handleIssuePicked, where the draft is opened.
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
	m.core.FetchIssue(repo, row.Number, m.newReq(pendingReq{issue: &pendingIssue{picked: &issuePickedMsg{repo: repo}}}))
	return m, nil
}

// pendingIssue is an issue fetch waiting for its Reply: the picker's pick
// (picked), or a "#n" prompt's expansion (expand, filled in as
// issueExpandedMsg minus the issue and error).
type pendingIssue struct {
	picked *issuePickedMsg
	expand *issueExpandedMsg
}

// handleIssuePicked opens a draft titled by the issue slug, seeds its
// prompt, links the issue, and opens the launch options modal — the same
// path n/N take after title entry.
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
	//     its draft.
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
	if err := m.latchedStorageErr(); err != nil {
		return m, m.handleError(err)
	}
	title := github.SlugTitle(msg.issue)
	if err := m.preservedTitleErr(title); err != nil {
		return m, m.handleError(err)
	}
	d := m.newDraft(title, github.SeedPrompt(msg.issue), msg.issue.Number)
	m.core.ExpediteGitHub()
	return m.openLaunchOptionsForNew(d, "")
}

// issueExpandedMsg is the #n shorthand's result: on success the seeded
// prompt replaces the token; on error the literal prompt launches
// unchanged and unlinked.
type issueExpandedMsg struct {
	draft *draft
	// repo is the workspace the prompt was submitted in. The draft is
	// already bound to its own slot and repo, so a late result cannot
	// create it in the wrong place — but openLaunchOptionsForNew would
	// still open the modal on whichever workspace is focused when this
	// lands, for a draft in another slot's list.
	repo           string
	number         int
	issue          github.Issue
	rest           string // user text after the #n token
	literal        string // original prompt, used when err != nil
	selectedBranch string
	err            error
}

// issueExpandDropped explains a #n expansion that was dropped by a
// guard. The fetch error is folded into the same string because errBox
// shows one message at a time — reporting only the guard would assert
// an expansion that may in fact have failed. The draft has no path back
// into launch options (n/N/I always open a new draft), so it is
// discarded, and the message says so.
func issueExpandDropped(msg issueExpandedMsg, title, why string) error {
	if msg.err != nil {
		return fmt.Errorf("issue #%d not expanded (%v) and %s; %q discarded", msg.number, msg.err, why, title)
	}
	return fmt.Errorf("issue #%d not expanded because %s; %q discarded", msg.number, why, title)
}

// handleIssueExpanded finishes the N flow after a #n expansion. The
// fetch is async and the user stays interactive during it (the prompt
// overlay is dismissed the moment the shorthand is spotted — see
// handleStatePromptKey), so this can land after a newer creation flow
// replaced the draft, or while another flow is on screen — whose overlay
// and pending launch-options closure openLaunchOptionsForNew would
// silently replace. Matches the guard handleIssuePicked uses for the
// same reason. It can also land after the user switched workspace tabs:
// the draft stays bound to its own slot either way, but
// openLaunchOptionsForNew would open the modal on whichever workspace
// is now focused, for a draft living in another slot's list — so that
// is checked too, same as handleIssuePicked's repo guard. A guard that
// fires discards the draft (issueExpandDropped).
//
// The guards sit above the msg.err != nil branch below, unlike
// handleIssuePicked's error check which returns early. Here err!=nil
// is a degrade-and-continue branch — it still calls
// openLaunchOptionsForNew with the literal prompt — so if a guard ran
// after it, a failed fetch could still pop the modal on the wrong
// workspace or on top of another flow. Keep the guards first.
func (m *home) handleIssueExpanded(msg issueExpandedMsg) (tea.Model, tea.Cmd) {
	d := msg.draft
	if d == nil {
		return m, nil
	}
	if m.draft != d {
		// Replaced by a newer creation flow (or discarded) while the
		// fetch ran: reopening the flow would revive a draft no rail
		// shows.
		return m, m.handleError(issueExpandDropped(msg, d.title, "a newer session was being created"))
	}
	if msg.repo != m.repoPath() {
		m.discardDraft()
		return m, m.handleError(issueExpandDropped(msg, d.title, "its workspace is no longer focused"))
	}
	if m.state != stateDefault {
		m.discardDraft()
		return m, m.handleError(issueExpandDropped(msg, d.title, "another session was being created"))
	}
	var errCmd tea.Cmd
	if msg.err != nil {
		d.prompt = msg.literal
		errCmd = m.handleError(fmt.Errorf("issue #%d not expanded: %w", msg.number, msg.err))
	} else {
		prompt := github.SeedPrompt(msg.issue)
		if msg.rest != "" {
			prompt += "\n" + msg.rest + "\n"
		}
		d.prompt = prompt
		d.issue = msg.issue.Number
		// The draft's row shows its issue from its repository's poll, which
		// in global mode covers the directory only once a session runs
		// there: ask for it, as the picker does (WatchGitHub expedites).
		m.core.WatchGitHub(d.path)
	}
	_, cmd := m.openLaunchOptionsForNew(d, msg.selectedBranch)
	return m, tea.Batch(cmd, errCmd)
}

// openLaunchOptionsForNew shows the Session Launch Options modal for a
// draft. Confirming creates and starts its session with the chosen
// options (confirmDraft); cancelling discards it
// (discardPendingLaunchOptionsCancel). selectedBranch, when set, is the
// branch picker's choice, recorded on the draft (the prompt flow sets it
// there too, before any issue expansion).
func (m *home) openLaunchOptionsForNew(d *draft, selectedBranch string) (tea.Model, tea.Cmd) {
	if selectedBranch != "" {
		d.branch = selectedBranch
	}
	m.pendingLaunchOptions = func(opts overlay.LaunchOptions) (tea.Model, tea.Cmd) {
		startTask := overlay.ConfirmationTask{
			Sync: func() {
				m.promptAfterName = false
				m.state = stateDefault
				m.menu.SetState(ui.StateDefault)
				m.confirmDraft(d, opts)
			},
			Async: tea.RequestWindowSize,
		}
		if m.remoteControlBlockedOn(opts.Account, launch.EffectiveRemoteControl(opts), d.program) {
			return m, m.promptRemoteControlBlocked(startTask, m.core.RCAuthFor(opts.Account).Reason)
		}
		return m, tea.Batch(m.runTask(startTask), m.instanceChanged())
	}
	m.pendingLaunchOptionsCancel = m.discardPendingLaunchOptionsCancel
	m.state = stateLaunchOptions
	lo, reloaded := m.newLaunchOptionsOverlay(launch.FromSettings(m.settings()), d.program)
	m.setOverlay(lo, overlayLaunchOptions)
	m.menu.SetState(ui.StateNewInstance)
	m.core.RequestUsageProbe()
	return m, tea.Batch(tea.RequestWindowSize, reloaded)
}
