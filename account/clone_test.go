package account

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUsageClone_SharesNothing: a clone's windows are its own.
func TestUsageClone_SharesNothing(t *testing.T) {
	u := Usage{FiveHour: &Window{Pct: 10}, SevenDay: &Window{Pct: 20}}
	c := u.Clone()
	c.FiveHour.Pct, c.SevenDay.Pct = 99, 99
	assert.InDelta(t, 10.0, u.FiveHour.Pct, 0)
	assert.InDelta(t, 20.0, u.SevenDay.Pct, 0)
	assert.Nil(t, Usage{}.Clone().FiveHour, "nil stays nil")
}

// TestSyncReportClone_SharesNothing: a clone's slices are its own.
func TestSyncReportClone_SharesNothing(t *testing.T) {
	r := SyncReport{Linked: []string{"a"}, Diverged: []string{"b"}, At: time.Unix(1, 0)}
	c := r.Clone()
	assert.Equal(t, r, c, "every field kept")
	c.Linked[0], c.Diverged[0] = "x", "y"
	assert.Equal(t, []string{"a"}, r.Linked)
	assert.Equal(t, []string{"b"}, r.Diverged)
}

// TestSync_StampsWhenItRan: a report says when its run started, so a
// client asking for a sync tells the report of its run from an older one
// (`loom account sync`).
func TestSync_StampsWhenItRan(t *testing.T) {
	main, acct := t.TempDir(), t.TempDir()
	before := time.Now()
	rep, err := Sync(acct, main)
	require.NoError(t, err)
	assert.False(t, rep.At.Before(before))
	assert.False(t, rep.At.After(time.Now()))
}
