package session

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStatus_String(t *testing.T) {
	cases := map[Status]string{
		Running:   "Running",
		Ready:     "Ready",
		Loading:   "Loading",
		Paused:    "Paused",
		Prompting: "Prompting",
		Deleting:  "Deleting",
	}
	for s, want := range cases {
		assert.Equal(t, want, s.String())
	}
	assert.Contains(t, Status(99).String(), "Status(99)")
}

func TestIsAllowedTransition_SelfTransitionsAlwaysAllowed(t *testing.T) {
	for _, s := range []Status{Running, Ready, Loading, Paused, Prompting, Deleting} {
		assert.True(t, IsAllowedTransition(s, s), "self-transition %s→%s should be allowed", s, s)
	}
}

func TestIsAllowedTransition_PausedIsRestricted(t *testing.T) {
	// Paused has no live tmux session — only Loading/Running (via Resume)
	// and Deleting are valid exits.
	assert.True(t, IsAllowedTransition(Paused, Loading))
	assert.True(t, IsAllowedTransition(Paused, Running))
	assert.True(t, IsAllowedTransition(Paused, Deleting))
	assert.False(t, IsAllowedTransition(Paused, Prompting), "Paused→Prompting should be illegal")
	assert.False(t, IsAllowedTransition(Paused, Ready), "Paused→Ready should be illegal")
}

func TestIsAllowedTransition_AnyCanBecomeDeleting(t *testing.T) {
	for _, s := range []Status{Running, Ready, Loading, Paused, Prompting} {
		assert.True(t, IsAllowedTransition(s, Deleting), "%s→Deleting should be allowed", s)
	}
}

func TestIsAllowedTransition_DeletingCanRevert(t *testing.T) {
	for _, s := range []Status{Running, Ready, Loading, Paused, Prompting} {
		assert.True(t, IsAllowedTransition(Deleting, s), "Deleting→%s (revert) should be allowed", s)
	}
}

func TestRecoverable_StringAndTransitions(t *testing.T) {
	assert.Equal(t, "Recoverable", Recoverable.String())
	// Recover path: Recoverable -> Loading -> Running.
	assert.True(t, IsAllowedTransition(Recoverable, Loading))
	assert.True(t, IsAllowedTransition(Recoverable, Running))
	// Discard path: kill preAction does Recoverable -> Deleting.
	assert.True(t, IsAllowedTransition(Recoverable, Deleting))
	// Nothing transitions INTO Recoverable (set only at construction).
	assert.False(t, IsAllowedTransition(Paused, Recoverable))
	assert.False(t, IsAllowedTransition(Recoverable, Prompting))
}

func TestTransitionTo_UpdatesStatusOnSuccess(t *testing.T) {
	inst := &Instance{Title: "t", Status: Ready}
	assert.NoError(t, inst.TransitionTo(Loading))
	assert.Equal(t, Loading, inst.GetStatus())
	assert.NoError(t, inst.TransitionTo(Running))
	assert.Equal(t, Running, inst.GetStatus())
}

func TestTransitionTo_RejectsIllegal(t *testing.T) {
	inst := &Instance{Title: "t", Status: Paused}
	err := inst.TransitionTo(Prompting)
	assert.Error(t, err)
	assert.Equal(t, Paused, inst.GetStatus(), "status must not change on rejected transition")
}

func TestTransitionTo_SelfIsNoOp(t *testing.T) {
	inst := &Instance{Title: "t", Status: Running}
	assert.NoError(t, inst.TransitionTo(Running))
	assert.Equal(t, Running, inst.GetStatus())
}
