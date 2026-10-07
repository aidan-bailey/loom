package account

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
	r := SyncReport{Linked: []string{"a"}, Diverged: []string{"b"}}
	c := r.Clone()
	c.Linked[0], c.Diverged[0] = "x", "y"
	assert.Equal(t, []string{"a"}, r.Linked)
	assert.Equal(t, []string{"b"}, r.Diverged)
}
