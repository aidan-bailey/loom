package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func newTestSplit() *SplitPane {
	return NewSplitPane(NewPreviewPane(), NewDiffPane(), NewTerminalPane())
}

func TestSplitPane_DefaultRatioIs70(t *testing.T) {
	s := newTestSplit()
	s.SetSize(80, 44) // chrome 4 → available 40
	assert.Equal(t, 28, s.agent.height)
	assert.Equal(t, 12, s.terminal.height)
}

func TestSplitPane_AdjustRatioClampsAndRelayouts(t *testing.T) {
	s := newTestSplit()
	s.SetSize(80, 44)
	s.AdjustAgentRatio(0.10) // 0.8
	assert.Equal(t, 32, s.agent.height)
	for i := 0; i < 20; i++ {
		s.AdjustAgentRatio(0.10)
	}
	assert.InDelta(t, 0.9, s.AgentRatio(), 0.001, "clamped high")
	for i := 0; i < 40; i++ {
		s.AdjustAgentRatio(-0.10)
	}
	assert.InDelta(t, 0.2, s.AgentRatio(), 0.001, "clamped low")
}

func TestSplitPane_TerminalHiddenGivesAgentEverything(t *testing.T) {
	s := newTestSplit()
	s.SetTerminalHidden(true)
	s.SetSize(80, 44) // one pane chrome = 2 → available 42
	assert.Equal(t, 42, s.agent.height)
	assert.Equal(t, 0, s.terminal.height)

	// HitTest never resolves to the hidden terminal.
	_, _, _, ok := s.HitTest(5, s.agent.height+3)
	assert.False(t, ok)

	// AgentContentHeight matches for the app's mouse anchor.
	assert.Equal(t, 42, s.AgentContentHeight())
}
