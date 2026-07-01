package app

import (
	tea "charm.land/bubbletea/v2"
)

// handleStateMergePickerKey drives the merge-picker overlay opened by
// runMergeSelected. On commit it either cancels (Esc — no git command
// runs) or hands the chosen source instance to mergeActionFor, which
// runs the actual git merge as a tea.Cmd. This is where the Lua
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

	target := m.list.GetSelectedInstance()
	row := mp.SelectedRow()
	m.dismissOverlay()
	m.state = stateDefault

	if canceled || row == nil || target == nil {
		return m, nil
	}
	source := instanceByDisplayIndex(m.list.GetInstances(), row.Index)
	if source == nil {
		return m, nil
	}
	return m, mergeActionFor(target, source)
}
