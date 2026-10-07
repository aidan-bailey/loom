package app

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
)

// handleStateMergePickerKey drives the merge-picker overlay opened by
// runMergeSelected. On commit it either cancels (Esc — no git command
// runs) or requests core's Merge of the chosen source, whose job runs
// the actual git merge as a tea.Cmd. This is where the Lua
// coroutine's involvement ends for good — everything past
// runMergeSelected's yield-and-resume is plain Go state-handler code,
// the same as stateWorkspace/stateConfirm.
func handleStateMergePickerKey(m *home, msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	mp := m.mergePicker()
	if mp == nil {
		return m, nil
	}
	committed, canceled := mp.HandleKeyPress(msg)
	if !committed {
		return m, nil
	}

	target := m.pendingMergeTarget
	sourceItems := m.pendingMergeSourceItems
	row := mp.SelectedRow()
	m.dismissOverlay()
	m.state = stateDefault
	m.pendingMergeTarget = nil
	m.pendingMergeSourceItems = nil

	if canceled || row == nil || target == nil {
		return m, nil
	}
	source := instanceByDisplayIndex(sourceItems, row.Index)
	if source == nil {
		return m, nil
	}
	// Either session may have gone since the picker opened: re-resolve
	// both by ID before the request.
	targetRow, _ := m.viewByID(target.ID)
	sourceRow, _ := m.viewByID(source.ID)
	if targetRow == nil || sourceRow == nil {
		gone := target.Title
		if targetRow != nil {
			gone = source.Title
		}
		return m, m.handleError(fmt.Errorf("merge: session '%s' is gone", gone))
	}
	m.core.Merge(target.ID, source.ID, m.opReq("merge into", target.Title))
	return m, nil
}
