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
