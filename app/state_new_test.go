package app

import (
	"encoding/json"
	"testing"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newPendingTitleEntryHome mirrors what runNewInstance (app/intents.go)
// does when 'n' is pressed: append a blank, unstarted instance and
// enter stateNew — without needing the full repoPath()/configDir()
// plumbing runNewInstance itself depends on.
func newPendingTitleEntryHome(t *testing.T) *home {
	t.Helper()
	m := newTestHome(t)
	instance, err := session.NewInstance(session.InstanceOptions{
		Title:     "",
		Path:      t.TempDir(),
		Program:   m.appConfig.DefaultProgram,
		ConfigDir: t.TempDir(),
	})
	require.NoError(t, err)
	m.list.AddInstance(instance)
	m.list.SetSelectedInstance(m.list.NumInstances() - 1)
	m.state = stateNew
	m.menu.SetState(ui.StateNewInstance)
	return m
}

func TestHandleStateNewKeyEnterOpensLaunchOptionsInsteadOfStartingImmediately(t *testing.T) {
	m := newPendingTitleEntryHome(t)

	for _, r := range "my-task" {
		handleStateNewKey(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	handleStateNewKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})

	assert.Equal(t, stateLaunchOptions, m.state)
	require.NotNil(t, m.pendingLaunchOptions)
	assert.NotNil(t, m.launchOptionsOverlay())
}

func TestNewInstanceFlowEndToEndComposesRealClosure(t *testing.T) {
	m := newPendingTitleEntryHome(t)

	for _, r := range "my-task" {
		handleStateNewKey(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	handleStateNewKey(m, tea.KeyPressMsg{Code: tea.KeyEnter}) // opens the real modal + stashes the real closure

	require.Equal(t, stateLaunchOptions, m.state)
	instance := m.list.GetInstances()[m.list.NumInstances()-1]

	// Move to Model row (row 2), cycle it, then confirm through the real
	// handleStateLaunchOptionsKey -> real pendingLaunchOptions closure
	// (not newPendingLaunchOptionsHome's substitute).
	handleStateLaunchOptionsKey(m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	handleStateLaunchOptionsKey(m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	handleStateLaunchOptionsKey(m, tea.KeyPressMsg{Code: ' ', Text: " "})
	handleStateLaunchOptionsKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})

	assert.Contains(t, instance.Program, "--model 'sonnet'")
	assert.Equal(t, stateDefault, m.state)
	assert.Nil(t, m.pendingLaunchOptions)
	assert.Equal(t, session.Loading, instance.GetStatus())
}

func TestNewInstanceFlowRemoteControlBlockedViaModalPromptsConfirm(t *testing.T) {
	m := newPendingTitleEntryHome(t)
	m.rcAuth = session.RemoteControlAuth{State: session.RemoteControlAuthBlocked, Reason: "not logged in"}

	for _, r := range "my-task" {
		handleStateNewKey(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	handleStateNewKey(m, tea.KeyPressMsg{Code: tea.KeyEnter}) // opens the real modal

	require.Equal(t, stateLaunchOptions, m.state)

	// Row 0 (Remote Control) defaults to enabled from DefaultConfig;
	// confirm without toggling it off, so remoteControlBlocked fires.
	handleStateLaunchOptionsKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})

	assert.Equal(t, stateConfirm, m.state)
	assert.NotNil(t, m.pendingConfirmation.Async)
}

// preservedTitleStorage returns a loaded Storage whose only record, titled
// title, was written by a newer loom: preserved on disk, absent from lists.
func preservedTitleStorage(t *testing.T, title string) *session.Storage {
	t.Helper()
	rec := &recordingInstanceStorage{lastData: json.RawMessage(
		`[{"schema_version":99,"title":"` + title + `"}]`)}
	storage, err := session.NewStorage(rec, t.TempDir())
	require.NoError(t, err)
	_, err = storage.LoadInstanceData()
	require.NoError(t, err)
	require.Equal(t, []string{title}, storage.PreservedTitles())
	return storage
}

// TestHandleStateNewKey_RejectsTitleOfPreservedRecord: storage never dedupes
// against records it can't load, so a new session under a preserved
// record's title would persist a second same-titled record (and share its
// tmux session name). The title must be refused while the user can still
// edit it.
func TestHandleStateNewKey_RejectsTitleOfPreservedRecord(t *testing.T) {
	m := newPendingTitleEntryHome(t)
	m.storage = preservedTitleStorage(t, "taken")
	m.errBox.SetSize(400, 1)

	for _, r := range "taken" {
		handleStateNewKey(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	handleStateNewKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})

	assert.Equal(t, stateNew, m.state, "stays on title entry so another title can be typed")
	assert.Nil(t, m.pendingLaunchOptions)
	assert.Contains(t, m.errBox.String(), `"taken"`)
}

// latchedStorage returns a Storage whose payload is not a JSON array, loaded
// so its write latch is engaged (every save refuses with ErrStorageLoadFailed).
func latchedStorage(t *testing.T) *session.Storage {
	t.Helper()
	rec := &recordingInstanceStorage{lastData: json.RawMessage(`{"not":"an array"}`)}
	storage, err := session.NewStorage(rec, t.TempDir())
	require.NoError(t, err)
	_, err = storage.LoadInstanceData()
	require.Error(t, err)
	require.True(t, storage.WritesRefused())
	return storage
}

// TestNewSession_RefusedWhileStorageLatched: a session created over a latched
// storage could never be persisted. Refusing up front keeps a latched list
// provably empty, which is what makes skipping its save lossless
// (applyWorkspaceToggle, handleQuit).
func TestNewSession_RefusedWhileStorageLatched(t *testing.T) {
	cases := map[string]func(m *home) tea.Cmd{
		"n": func(m *home) tea.Cmd { _, cmd := runNewInstance(m); return cmd },
		"N": func(m *home) tea.Cmd { _, cmd := runPromptNewInstance(m); return cmd },
		"issue picked": func(m *home) tea.Cmd {
			_, cmd := m.Update(issuePickedMsg{repo: m.repoPath(), issue: github.Issue{Number: 3, Title: "Bug"}})
			return cmd
		},
	}
	for name, create := range cases {
		t.Run(name, func(t *testing.T) {
			m := newTestHomeWithActiveCtx(t)
			m.storage = latchedStorage(t)
			m.errBox.SetSize(400, 1)
			before := m.list.NumInstances()

			cmd := create(m)

			assert.Equal(t, before, m.list.NumInstances(), "nothing may be created")
			assert.Equal(t, stateDefault, m.state)
			assert.NotNil(t, cmd)
			assert.Contains(t, m.errBox.String(), "can't be created")
		})
	}
}
