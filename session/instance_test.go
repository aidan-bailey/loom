package session

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatusAge_StampsOnTransition(t *testing.T) {
	inst := &Instance{Title: "age-test", Status: Ready}
	assert.Equal(t, time.Duration(0), inst.StatusAge(), "zero before any transition")

	require.NoError(t, inst.TransitionTo(Loading))
	age := inst.StatusAge()
	assert.Greater(t, age, time.Duration(0))
	assert.Less(t, age, time.Second)

	// Self-transition must not restamp.
	time.Sleep(10 * time.Millisecond)
	before := inst.StatusAge()
	require.NoError(t, inst.TransitionTo(Loading))
	assert.GreaterOrEqual(t, inst.StatusAge(), before)
}

func TestInstance_ATurnStartsOnRunningButNotAfterAPrompt(t *testing.T) {
	inst := &Instance{Title: "turn", Status: Paused}
	assert.True(t, inst.TurnStart().IsZero(), "no turn seen in this process")

	require.NoError(t, inst.TransitionTo(Running))
	first := inst.TurnStart()
	require.False(t, first.IsZero())

	require.NoError(t, inst.TransitionTo(Prompting))
	require.NoError(t, inst.TransitionTo(Running))
	assert.Equal(t, first, inst.TurnStart(), "a prompt inside a turn doesn't split it")

	require.NoError(t, inst.TransitionTo(Ready))
	time.Sleep(time.Millisecond)
	require.NoError(t, inst.TransitionTo(Running))
	assert.True(t, inst.TurnStart().After(first), "a reply starts a new turn")
}

func TestInstance_KeepsTheMockupItIsGiven(t *testing.T) {
	inst := &Instance{Title: "mockup", Status: Running}
	at := time.Now()
	inst.SetMockup("layout.html", at)
	screen, changed := inst.Mockup()
	assert.Equal(t, "layout.html", screen)
	assert.Equal(t, at, changed)
	inst.SetMockup("", time.Time{})
	screen, _ = inst.Mockup()
	assert.Empty(t, screen)
}
