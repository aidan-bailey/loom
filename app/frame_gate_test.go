package app

import (
	"fmt"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gateAt is a frameGate whose clock reads *now.
func gateAt(now *time.Time) *frameGate {
	return &frameGate{now: func() time.Time { return *now }}
}

// counting is a render func that counts its calls in *n and names the
// count in the frame it returns.
func counting(n *int) func() tea.View {
	return func() tea.View {
		*n++
		return tea.NewView(fmt.Sprintf("frame %d", *n))
	}
}

func TestFrameGate_RendersUnlessOutputAsksToReuse(t *testing.T) {
	now := time.Unix(1000, 0)
	g := gateAt(&now)
	n := 0
	r := counting(&n)

	assert.Equal(t, "frame 1", g.view(r).Content, "the first View renders")
	g.fresh()
	assert.Equal(t, "frame 2", g.view(r).Content, "an Update that asked for nothing renders")
	assert.Equal(t, "frame 3", g.view(r).Content, "a View with no Update before it renders")

	g.fresh()
	assert.Nil(t, g.output(false))
	assert.Equal(t, "frame 3", g.view(r).Content, "undrawn output reuses the frame")
	assert.Equal(t, "frame 4", g.view(r).Content, "a reuse covers only the View after its Update")

	g.fresh()
	assert.Nil(t, g.output(false))
	g.fresh()
	assert.Equal(t, "frame 5", g.view(r).Content, "fresh after output renders")
}

func TestFrameGate_NoFrameYetRenders(t *testing.T) {
	now := time.Unix(1000, 0)
	g := gateAt(&now)
	n := 0
	assert.Nil(t, g.output(true), "drawn output with no frame yet renders at once: no tick")
	assert.False(t, g.pending)
	g.fresh()
	assert.Nil(t, g.output(false))
	assert.Equal(t, "frame 1", g.view(counting(&n)).Content, "there is no frame to reuse")
	assert.Equal(t, "frame 2", g.view(counting(&n)).Content, "the ask ended with the View after it")
}

func TestFrameGate_DrawnOutputRendersAtOnceAfterAFrame(t *testing.T) {
	now := time.Unix(1000, 0)
	g := gateAt(&now)
	n := 0
	r := counting(&n)
	g.view(r)

	now = now.Add(frameInterval)
	g.fresh()
	assert.Nil(t, g.output(true), "a frame has passed: no tick")
	assert.Equal(t, "frame 2", g.view(r).Content, "it renders at once (a keystroke's echo)")
	assert.False(t, g.pending)
}

func TestFrameGate_ABurstGetsOneTrailingFrame(t *testing.T) {
	now := time.Unix(1000, 0)
	g := gateAt(&now)
	n := 0
	r := counting(&n)
	g.view(r)

	now = now.Add(5 * time.Millisecond)
	g.fresh()
	tick := g.output(true)
	require.NotNil(t, tick, "within a frame of the last render: a trailing tick")
	assert.Equal(t, "frame 1", g.view(r).Content, "the frame is reused until the tick")

	now = now.Add(5 * time.Millisecond)
	g.fresh()
	assert.Nil(t, g.output(true), "one tick pending at most")
	assert.Equal(t, "frame 1", g.view(r).Content)

	g.ticked()
	g.fresh()
	assert.Equal(t, "frame 2", g.view(r).Content, "the tick's Update renders")
	assert.False(t, g.pending)

	g.fresh()
	assert.NotNil(t, g.output(true), "after the tick a new burst arms again")
}

func TestFrameGate_TheTickIsAFrameTickMsgForTheRestOfTheFrame(t *testing.T) {
	now := time.Unix(1000, 0)
	g := gateAt(&now)
	n := 0
	g.view(counting(&n))
	now = now.Add(frameInterval - time.Millisecond)
	g.fresh()
	tick := g.output(true)
	require.NotNil(t, tick)
	start := time.Now()
	assert.Equal(t, frameTickMsg{}, tick())
	assert.Less(t, time.Since(start), frameInterval, "it waits the rest of the frame, not a whole one")
}

func TestFrameGate_ZeroValueUsesTheWallClock(t *testing.T) {
	var g frameGate
	n := 0
	g.view(counting(&n))
	g.fresh()
	assert.NotNil(t, g.output(true), "rendered just now: within a frame")
}
