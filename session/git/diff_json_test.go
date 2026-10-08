package git

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDiffStats_RoundTripsJSON: diff stats keep their counts, content and
// error text through JSON (a client shows the text), and no error stays
// none.
func TestDiffStats_RoundTripsJSON(t *testing.T) {
	for _, d := range []DiffStats{
		{Content: "+a\n", Added: 1, Removed: 2},
		{Error: errors.New("base commit missing")},
	} {
		b, err := json.Marshal(d)
		require.NoError(t, err)
		var back DiffStats
		require.NoError(t, json.Unmarshal(b, &back))
		assert.Equal(t, d.Content, back.Content)
		assert.Equal(t, d.Added, back.Added)
		assert.Equal(t, d.Removed, back.Removed)
		if d.Error == nil {
			assert.Nil(t, back.Error)
		} else {
			assert.EqualError(t, back.Error, d.Error.Error())
		}
	}
}
