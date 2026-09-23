package app

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Drives n → title → N-style prompt overlay for an unstarted instance.
func promptOverlayForNewInstance(t *testing.T, m *home) {
	t.Helper()
	_, _ = runPromptNewInstance(m)
	inst := m.list.GetInstances()[m.list.NumInstances()-1]
	inst.Title = "shorthand"
	handleStateNewKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, statePrompt, m.state)
}

func TestPromptShorthand_DispatchesExpansion(t *testing.T) {
	m := newTestHomeWithWsCtx(t)
	m.ghAvailable = ghAvailability{checked: true, ok: true}
	promptOverlayForNewInstance(t, m)
	ti := m.textInput()
	require.NotNil(t, ti)
	ti.SetValue("#12 and tidy tests")
	// Initial focus is the textarea (index 0); shift+tab wraps backward
	// to the last stop (the Enter button) regardless of how many stops
	// the branch/profile pickers add — same idiom as
	// TestHandleStatePromptKeySubmitOpensLaunchOptionsInsteadOfStartingImmediately.
	handleStatePromptKey(m, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	_, cmd := handleStatePromptKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.NotNil(t, cmd)
	assert.Equal(t, stateDefault, m.state, "waits for the expansion before launch options")
}

func TestIssueExpandedMsg_SeedsPromptAndLinks(t *testing.T) {
	m := newTestHomeWithWsCtx(t)
	promptOverlayForNewInstance(t, m)
	inst := m.list.GetInstances()[m.list.NumInstances()-1]
	m.dismissOverlay()
	m.state = stateDefault
	m.Update(issueExpandedMsg{instance: inst, repo: m.repoPath(), rest: "and tidy tests",
		issue: github.Issue{Number: 12, Title: "Fix", URL: "https://x/12", Body: "b"}})
	assert.Equal(t, 12, inst.IssueNumber())
	assert.Contains(t, inst.Prompt(), "# Fix")
	assert.Contains(t, inst.Prompt(), "\n\nand tidy tests")
	assert.Equal(t, stateLaunchOptions, m.state)
}

func TestIssueExpandedMsg_FailureLaunchesLiteral(t *testing.T) {
	m := newTestHomeWithWsCtx(t)
	promptOverlayForNewInstance(t, m)
	inst := m.list.GetInstances()[m.list.NumInstances()-1]
	m.dismissOverlay()
	m.state = stateDefault
	_, cmd := m.Update(issueExpandedMsg{instance: inst, repo: m.repoPath(), number: 12, literal: "#12 and tidy tests", err: errors.New("nope")})
	assert.Equal(t, 0, inst.IssueNumber())
	assert.Equal(t, "#12 and tidy tests", inst.Prompt())
	assert.Equal(t, stateLaunchOptions, m.state, "a bad number never blocks the session")
	assert.NotNil(t, cmd, "the footer error still surfaces")
}

// handleIssueExpanded is reachable from any state, and openLaunchOptionsForNew
// would replace whatever overlay is already there. Matches the guard on the
// picker path.
func TestIssueExpandedMsg_DoesNotClobberAnotherFlow(t *testing.T) {
	m := newTestHomeWithWsCtx(t)
	promptOverlayForNewInstance(t, m)
	inst := m.list.GetInstances()[m.list.NumInstances()-1]
	m.dismissOverlay()
	m.state = stateLaunchOptions // another creation flow is on screen

	_, cmd := m.Update(issueExpandedMsg{instance: inst, repo: m.repoPath(), rest: "",
		issue: github.Issue{Number: 12, Title: "Fix", URL: "https://x/12", Body: "b"}})
	assert.Equal(t, stateLaunchOptions, m.state, "the in-progress flow's state is not replaced")
	assert.Equal(t, 0, inst.IssueNumber(), "and nothing is applied to the waiting instance")
	assert.NotNil(t, cmd, "but the user is told")
}

// The instance is bound to its own repo, so a late expansion cannot
// create it in the wrong place — but it would still open the launch
// options modal on whichever workspace is focused when it lands.
func TestIssueExpandedMsg_WrongRepoDoesNotOpenHere(t *testing.T) {
	m := newTestHomeWithWsCtx(t)
	promptOverlayForNewInstance(t, m)
	inst := m.list.GetInstances()[m.list.NumInstances()-1]
	m.dismissOverlay()
	m.state = stateDefault

	_, cmd := m.Update(issueExpandedMsg{instance: inst, repo: "/somewhere/else", number: 12,
		issue: github.Issue{Number: 12, Title: "Fix", URL: "https://x/12", Body: "b"}})
	assert.Equal(t, stateDefault, m.state, "no modal opens on the wrong workspace")
	assert.Equal(t, 0, inst.IssueNumber(), "and nothing is applied")
	assert.NotNil(t, cmd, "but the user is told")
}

// A guard that fires while the fetch also failed must not claim the
// expansion happened — errBox shows one message, so the fetch error has
// to be folded in rather than dropped.
func TestIssueExpandedMsg_DroppedResultReportsFetchError(t *testing.T) {
	m := newTestHomeWithWsCtx(t)
	promptOverlayForNewInstance(t, m)
	inst := m.list.GetInstances()[m.list.NumInstances()-1]
	m.dismissOverlay()
	m.state = stateDefault

	m.Update(issueExpandedMsg{instance: inst, repo: "/somewhere/else", number: 12,
		literal: "#12 and tidy tests", err: errors.New("gh exploded")})

	got := m.errBox.String()
	assert.Contains(t, got, "gh exploded", "the real failure must survive the guard")
	assert.Contains(t, got, "discard it with D", "and say how to recover")
	assert.NotContains(t, got, "expanded for another workspace", "and not claim an expansion that never happened")
}
