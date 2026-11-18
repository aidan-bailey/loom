package app

import (
	"testing"

	"charm.land/bubbles/v2/spinner"

	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// noopMsg is a message Update handles by doing nothing.
type noopMsg struct{}

// spinnerHome is a test home whose rail shows one row in status.
func spinnerHome(t *testing.T, status session.Status) (*home, *session.Instance) {
	t.Helper()
	h := newTestHome(t)
	h.spinner = spinner.New(spinner.WithSpinner(spinner.MiniDot)) // as newHome builds it
	inst, err := session.NewInstance(session.InstanceOptions{Title: "a", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	h.ws().AddForTest(inst)
	require.NoError(t, inst.TransitionTo(status))
	h.syncViews()
	h.list.SetSize(40, 40)
	return h, inst
}

// The spinner's tick redraws the whole TUI twelve times a second: it stops
// once no card on screen shows the spinner.
func TestSpinner_StopsWhenNoDrawnCardSpins(t *testing.T) {
	h, _ := spinnerHome(t, session.Ready)
	h.spinnerOn = true
	before := h.spinner.View()

	h.Update(h.spinner.Tick())

	assert.False(t, h.spinnerOn, "the tick is not re-armed")
	assert.Equal(t, before, h.spinner.View(), "a spinner nobody sees doesn't advance")
}

func TestSpinner_KeepsTickingWhileADrawnCardSpins(t *testing.T) {
	h, _ := spinnerHome(t, session.Running)
	h.spinnerOn = true
	before := h.spinner.View()

	h.Update(h.spinner.Tick())

	assert.True(t, h.spinnerOn)
	assert.NotEqual(t, before, h.spinner.View(), "the spinner advanced a frame")
}

func TestSpinner_StartsWhenACardStartsSpinning(t *testing.T) {
	h, inst := spinnerHome(t, session.Ready)
	h.Update(noopMsg{})
	require.False(t, h.spinnerOn, "fixture: nothing spins")

	require.NoError(t, inst.TransitionTo(session.Running))
	h.syncViews()
	h.Update(noopMsg{})

	assert.True(t, h.spinnerOn, "the first Update that sees a spinning card starts the tick")
}
