package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestErrBox_InfoShownWhenNoError(t *testing.T) {
	b := NewErrBox()
	b.SetSize(80, 1)
	b.SetInfo("Recovery: cleaned 2 stale worktrees")
	assert.Contains(t, b.String(), "cleaned 2 stale worktrees")

	// An error takes precedence over info.
	b.SetError(errors.New("boom"))
	assert.Contains(t, b.String(), "boom")
	assert.False(t, strings.Contains(b.String(), "cleaned"))

	b.Clear()
	assert.NotContains(t, b.String(), "boom")
	assert.NotContains(t, b.String(), "cleaned")
}

// TestErrBox_InfoExpires pins the "Recovered session" lingering bug: SetInfo
// must arm a deadline on its own, since nothing else clears an info toast.
func TestErrBox_InfoExpires(t *testing.T) {
	b := NewErrBox()
	b.SetSize(80, 1)
	b.SetInfo("Recovered session 'foo'")
	assert.Contains(t, b.String(), "Recovered session")

	now := time.Now()
	b.ExpireIfDue(now)
	assert.Contains(t, b.String(), "Recovered session", "must not clear before its deadline")

	b.ExpireIfDue(now.Add(24 * time.Hour))
	assert.NotContains(t, b.String(), "Recovered session")
}

// TestErrBox_ErrorExpires mirrors TestErrBox_InfoExpires for the error
// path. app.handleError also schedules its own timer Cmd to hide errors
// promptly, but ExpireIfDue must independently be able to clear one too —
// it's the only mechanism SetInfo callers get.
func TestErrBox_ErrorExpires(t *testing.T) {
	b := NewErrBox()
	b.SetSize(80, 1)
	b.SetError(errors.New("boom"))
	assert.Contains(t, b.String(), "boom")

	b.ExpireIfDue(time.Now().Add(24 * time.Hour))
	assert.NotContains(t, b.String(), "boom")
}

// TestErrBox_NewMessageRearmsDeadline ensures a later message isn't cut
// short by an earlier message's now-expired deadline.
func TestErrBox_NewMessageRearmsDeadline(t *testing.T) {
	b := NewErrBox()
	b.SetSize(80, 1)
	b.SetInfo("first")
	b.SetInfo("second")
	b.ExpireIfDue(time.Now())
	assert.Contains(t, b.String(), "second", "fresh SetInfo call must reset the deadline")
}
