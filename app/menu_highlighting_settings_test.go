package app

import (
	"testing"

	"github.com/aidan-bailey/loom/ui/overlay"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resolveDeferredKeyReplays follows handleMenuHighlighting's deferred
// replay chain to completion, exactly as Bubble Tea's async Cmd runtime
// eventually would: each pending Cmd is a tea.Batch of a closure that
// re-emits the original KeyPressMsg plus a menu-highlight callback: run
// it, feed any KeyPressMsg it yields back into m.handleKeyPress, and
// keep following the chain (a replay can itself be re-deferred once more
// if nothing else raced it to consume m.keySent) until it stops
// producing a further Cmd or the bound is hit.
func resolveDeferredKeyReplays(t *testing.T, m *home, cmd tea.Cmd) {
	t.Helper()
	for i := 0; i < 5 && cmd != nil; i++ {
		msg := cmd()
		batch, ok := msg.(tea.BatchMsg)
		if !ok {
			t.Fatalf("expected tea.BatchMsg from the deferred replay, got %T", msg)
		}
		cmd = nil
		for _, sub := range batch {
			if sub == nil {
				continue
			}
			if replayed, ok := sub().(tea.KeyPressMsg); ok {
				_, next := m.handleKeyPress(replayed)
				if next != nil {
					cmd = next
				}
			}
		}
	}
}

// typeBurstThroughHandleKeyPress drives s through the real top-level
// dispatcher (m.handleKeyPress), which is what engages
// handleMenuHighlighting — unlike calling a state handler directly, as
// most settings tests do for setup. Each character is dispatched before
// any deferred replay from an earlier one is resolved, exactly as a fast
// burst (or a "type -l" driven paste, which writes the whole string in
// one shot) delivers them: the terminal-reader goroutine has the rest of
// the burst ready to enqueue immediately, while a deferred replay's
// trivial closure still has to be scheduled onto Bubble Tea's event
// channel.
func typeBurstThroughHandleKeyPress(t *testing.T, m *home, s string) {
	t.Helper()
	var pending []tea.Cmd
	for _, r := range s {
		_, cmd := m.handleKeyPress(tea.KeyPressMsg{Code: r, Text: string(r)})
		if cmd != nil {
			pending = append(pending, cmd)
		}
	}
	for _, cmd := range pending {
		resolveDeferredKeyReplays(t, m, cmd)
	}
}

// TestHandleKeyPress_SettingsProfilesNameBurstNotScrambled reproduces
// the typeahead-scramble bug for the Profiles "add name" field: "t" is a
// registered global keybinding (quick input bar → terminal), so typing
// it as the first character of a fast burst used to get pulled out of
// place by handleMenuHighlighting's deferred-replay mechanism (see
// handleMenuHighlighting's doc comment) and reinserted after whatever
// was typed in between — "toy" rendered as "oyt". Setup goes through the
// low-level handler directly (as the other settings tests do), since
// only the burst itself needs to exercise handleMenuHighlighting.
func TestHandleKeyPress_SettingsProfilesNameBurstNotScrambled(t *testing.T) {
	m := newTestHomeWithWsCtx(t)
	so := overlay.NewSettingsOverlay(m.appConfig, false, "")
	m.setOverlay(so, overlaySettings)
	m.state = stateSettings

	for i := 0; i < int(3); i++ { // Profiles is row 3
		handleStateSettingsKey(m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	handleStateSettingsKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})   // drill into Profiles
	handleStateSettingsKey(m, tea.KeyPressMsg{Code: 'n', Text: "n"}) // open add-name

	typeBurstThroughHandleKeyPress(t, m, "toy")

	rendered := so.Render()
	assert.Contains(t, rendered, "toy", "a fast burst must type in order")
	assert.NotContains(t, rendered, "oyt", "the first character must not be reordered to the end")
}

// TestHandleKeyPress_SettingsAccountsNameBurstNotScrambled is the same
// reproduction for the Accounts "add name" field ("a", the key that
// opens it, is itself a registered global binding too — quick input bar
// → agent), which is where the sandbox actually observed the bug.
func TestHandleKeyPress_SettingsAccountsNameBurstNotScrambled(t *testing.T) {
	m := newTestHomeWithWsCtx(t)
	so := overlay.NewSettingsOverlay(m.appConfig, false, "")
	so.SetAccountRows([]overlay.AccountRow{{Name: "default", IsDefault: true}})
	m.setOverlay(so, overlaySettings)
	m.state = stateSettings

	for i := 0; i < 20; i++ { // overshoot; the cursor clamps at the last row (Accounts)
		handleStateSettingsKey(m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	}
	handleStateSettingsKey(m, tea.KeyPressMsg{Code: tea.KeyEnter})   // drill into Accounts
	handleStateSettingsKey(m, tea.KeyPressMsg{Code: 'a', Text: "a"}) // open add-name

	typeBurstThroughHandleKeyPress(t, m, "toy")

	rendered := so.Render()
	assert.Contains(t, rendered, "toy", "a fast burst must type in order")
	assert.NotContains(t, rendered, "oyt", "the first character must not be reordered to the end")

	_, ok := so.TakeAccountRequest()
	require.False(t, ok, "typing must not itself request anything")
}
