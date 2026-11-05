package advisor

import (
	"testing"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolve(t *testing.T) {
	s, err := Resolve(nil)
	require.NoError(t, err)
	assert.Equal(t, Settings{Enabled: true, Model: "opus", Effort: "low", Wait: time.Minute}, s)

	off := false
	s, err = Resolve(&config.AgentSettings{Enabled: &off, Model: "sonnet", Effort: "high", Account: "work", WaitSeconds: 30})
	require.NoError(t, err)
	assert.Equal(t, Settings{Enabled: false, Model: "sonnet", Effort: "high", Account: "work", Wait: 30 * time.Second}, s)

	bad := []config.AgentSettings{
		{Model: "two words"},
		{Effort: "turbo"},
		{WaitSeconds: 601},
		{WaitSeconds: -1},
	}
	for _, b := range bad {
		_, err := Resolve(&b)
		assert.Error(t, err, "%+v", b)
	}
}
