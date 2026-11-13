package core

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOriginWatch_NoClientCanSendIt(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")

	m.Send(m.idOf(inst), Message{Text: "hi", Origin: OriginWatch}, 9)

	rs := replies(m.Drain())
	require.Len(t, rs, 1)
	require.Error(t, rs[0].Err)
	assert.Contains(t, rs[0].Err.Error(), "is no sender's origin")
	assert.Empty(t, m.mods, "nothing reaches the mod")
}

func TestOriginWatch_NeedsALiveMod(t *testing.T) {
	assert.True(t, OriginWatch.needsMod())
}

func TestOriginWatch_ANudgeGoesToALiveModAsItsOwnText(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")
	require.NoError(t, m.ModHello("L1", ModHello{Mod: 1}))

	m.enqueue(inst, &modMessage{text: "merge origin/main", origin: OriginWatch})

	msgs, _, err := m.ModInbox("L1", "")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, ModMessage{ID: msgs[0].ID, Text: "merge origin/main"}, msgs[0], "no From line, and not the person's words")
}

func TestOriginWatch_ANudgeTheModNeverTookIsReportedAndNotPasted(t *testing.T) {
	for _, offered := range []bool{false, true} {
		t.Run(fmt.Sprintf("offered=%v", offered), func(t *testing.T) {
			withMod(t)
			m := NewForTest(Options{})
			inst := modInst(t, m, "x")
			var why string
			m.enqueue(inst, &modMessage{text: "nudge", origin: OriginWatch, onUndelivered: func(w string) { why = w }})
			if offered {
				require.NoError(t, m.ModHello("L1", ModHello{Mod: 1}))
				msgs, _, err := m.ModInbox("L1", "")
				require.NoError(t, err)
				require.Len(t, msgs, 1)
			}

			m.ModsStop()

			if offered {
				assert.Contains(t, why, "may or may not have reached Claude")
			} else {
				assert.Equal(t, modsStoppedWhy, why)
			}
			assert.Empty(t, m.Drain().Jobs, "nothing is pasted")
		})
	}
}

func TestOriginWatch_AProgramWithoutAModIsReportedAtOnce(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := startedInst(t, "x", "aider")
	hold(m, inst)
	var why string

	m.enqueue(inst, &modMessage{text: "nudge", origin: OriginWatch, onUndelivered: func(w string) { why = w }})

	assert.Equal(t, "it runs aider, not Claude", why)
	assert.Empty(t, m.Drain().Jobs, "nothing is pasted")
}
