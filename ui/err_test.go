package ui

import (
	"errors"
	"strings"
	"testing"

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
