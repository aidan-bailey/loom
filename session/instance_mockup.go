package session

import "time"

// TurnStart is when the instance's latest turn began: the last time it
// entered Running from any status but Prompting (TransitionTo), so a
// permission prompt or a question inside a turn doesn't split it. Zero when
// this process has seen none.
func (i *Instance) TurnStart() time.Time {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.turnStartedAt
}

// SetMockup records the brainstorm companion's screen the model found in
// the worktree and the screen's mtime; "" clears it.
func (i *Instance) SetMockup(screen string, changed time.Time) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.mockupScreen, i.mockupChanged = screen, changed
}

// Mockup is the screen SetMockup recorded and its mtime ("" for none).
func (i *Instance) Mockup() (string, time.Time) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.mockupScreen, i.mockupChanged
}
