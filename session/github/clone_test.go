package github

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSnapshotClone_SharesNothing: a clone's maps and each issue's labels
// are its own.
func TestSnapshotClone_SharesNothing(t *testing.T) {
	s := Snapshot{
		PRs:    map[string]PR{"me/x": {Number: 1}},
		Issues: map[int]Issue{7: {Number: 7, Labels: []string{"bug"}}},
	}
	c := s.Clone()
	c.PRs["me/y"] = PR{Number: 2}
	c.Issues[7].Labels[0] = "changed"
	delete(c.Issues, 7)

	assert.Len(t, s.PRs, 1)
	assert.Equal(t, []string{"bug"}, s.Issues[7].Labels)
	assert.Nil(t, Snapshot{}.Clone().Issues, "nil stays nil")
}
