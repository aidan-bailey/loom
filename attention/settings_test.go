package attention

import (
	"testing"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolve_Defaults(t *testing.T) {
	want := Settings{Enabled: true, Model: "haiku", Effort: "low", MaxParallel: 3, Wait: 30 * time.Second}
	s, err := Resolve(nil)
	require.NoError(t, err)
	assert.Equal(t, want, s)
	s, err = Resolve(&config.AttentionSettings{})
	require.NoError(t, err)
	assert.Equal(t, want, s)
}

func TestResolve_Bounds(t *testing.T) {
	off := false
	s, err := Resolve(&config.AttentionSettings{Enabled: &off, Model: "sonnet", Effort: "medium", MaxParallel: 8, WaitSeconds: 5})
	require.NoError(t, err)
	assert.Equal(t, Settings{Model: "sonnet", Effort: "medium", MaxParallel: 8, Wait: 5 * time.Second}, s)
	s, err = Resolve(&config.AttentionSettings{MaxParallel: 1, WaitSeconds: 120})
	require.NoError(t, err)
	assert.Equal(t, 1, s.MaxParallel)
	assert.Equal(t, 120*time.Second, s.Wait)

	for _, bad := range []config.AttentionSettings{
		{MaxParallel: 9}, {MaxParallel: -1}, {WaitSeconds: 4}, {WaitSeconds: 121},
		{Model: "two words"}, {Effort: "huge"},
	} {
		_, err := Resolve(&bad)
		assert.ErrorContains(t, err, "attention.", "%+v", bad)
	}
}
