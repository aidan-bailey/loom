package app

import (
	"context"
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
	"github.com/aidan-bailey/loom/ui/overlay"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHandleStateOrphanRecoveryKey_AttributesOrphansToCorrectWorkspace
// is the regression guard for the bug where recovered orphans all landed
// in the focused workspace tab regardless of which workspace they
// actually came from. Root cause: the key handler cleared
// m.orphanCfgDirs to nil BEFORE calling applyOrphanRecovery, which then
// looked up an empty map and got "" for every cfgDir — and
// listForCfgDir("") returns m.list (the focused slot).
//
// This test sets up two slots, plants an orphan whose cfgDir belongs to
// the NON-focused slot, simulates the picker commit, and asserts the
// recovered instance lands in the right slot's list.
func TestHandleStateOrphanRecoveryKey_AttributesOrphansToCorrectWorkspace(t *testing.T) {
	s := spinner.New(spinner.WithSpinner(spinner.MiniDot))

	cfgDir1 := t.TempDir()
	cfgDir2 := t.TempDir()
	state1 := config.LoadStateFrom(cfgDir1)
	state2 := config.LoadStateFrom(cfgDir2)
	storage1, err := session.NewStorage(state1, cfgDir1)
	require.NoError(t, err)
	storage2, err := session.NewStorage(state2, cfgDir2)
	require.NoError(t, err)
	list1 := ui.NewList(&s, false)
	list2 := ui.NewList(&s, false)

	// The orphan worktree path that belongs to slot 2. Real dir so
	// ReconcileAndRestore's CheckWorktreeExists returns true and the
	// recovery proceeds (otherwise the wt-gone path would also work,
	// but using a real tempdir keeps the test setup explicit).
	worktreePath := t.TempDir()

	h := &home{
		ctx:         context.Background(),
		state:       stateOrphanRecovery,
		appConfig:   config.DefaultConfig(),
		list:        list1, // focused on slot 1
		menu:        ui.NewMenu(),
		splitPane:   ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		tabBar:      ui.NewWorkspaceTabBar(),
		errBox:      ui.NewErrBox(),
		storage:     storage1,
		appState:    state1,
		focusedSlot: 0,
		spinner:     s,
		slots: []workspaceSlot{
			{
				wsCtx:     &config.WorkspaceContext{Name: "ws1", ConfigDir: cfgDir1},
				storage:   storage1,
				appConfig: config.DefaultConfig(),
				appState:  state1,
				list:      list1,
				splitPane: ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
			},
			{
				wsCtx:     &config.WorkspaceContext{Name: "ws2", ConfigDir: cfgDir2},
				storage:   storage2,
				appConfig: config.DefaultConfig(),
				appState:  state2,
				list:      list2,
				splitPane: ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
			},
		},
	}

	cand := session.OrphanCandidate{
		WorktreePath:  worktreePath,
		BranchName:    "u/orphan",
		RepoPath:      t.TempDir(),
		BaseCommitSHA: "abc123",
		Title:         "orphan",
		HasLiveTmux:   true, // default-selected in the picker
	}
	h.pendingOrphans = []session.OrphanCandidate{cand}
	h.orphanCfgDirs = map[string]string{worktreePath: cfgDir2}

	h.setOverlay(overlay.NewOrphanRecoveryPicker(h.pendingOrphans), overlayOrphanRecovery)

	// `esc` commits the picker — HandleKeyPress returns closed=true and
	// Selected() reads the current recover[] (default-selected via
	// HasLiveTmux), so the candidate flows through applyOrphanRecovery.
	_, _ = handleStateOrphanRecoveryKey(h, tea.KeyMsg{Type: tea.KeyEsc})

	assert.Equal(t, 0, len(list1.GetInstances()),
		"focused workspace must NOT receive an orphan that belongs to a different cfgDir")
	assert.Equal(t, 1, len(list2.GetInstances()),
		"orphan must land in the workspace its WorktreePath belongs to")
}
