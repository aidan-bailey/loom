package app

import (
	"testing"

	"github.com/aidan-bailey/loom/attention"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
	"github.com/stretchr/testify/assert"
)

// TestTabStatusOf_AStopThatAsksIsPrompting: the tab bar lights a stopped
// turn that asks for the person as it lights a dialog, and only that one.
func TestTabStatusOf_AStopThatAsksIsPrompting(t *testing.T) {
	assert.Equal(t, ui.TabStatusPrompting, tabStatusOf(core.InstanceView{Status: session.Ready, Verdict: attention.NeedsYou}))
	assert.Equal(t, ui.TabStatusReady, tabStatusOf(core.InstanceView{Status: session.Ready, Verdict: attention.Done}))
	assert.Equal(t, ui.TabStatusPrompting, tabStatusOf(core.InstanceView{Status: session.Prompting}))
	assert.Equal(t, ui.TabStatusRunning, tabStatusOf(core.InstanceView{Status: session.Running}))
}
