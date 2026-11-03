package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSettings_BuildSweepAfterHoursKey(t *testing.T) {
	var s Settings
	require.NoError(t, json.Unmarshal([]byte(`{"build_sweep_after_hours": 12}`), &s))
	assert.Equal(t, 12, s.GetBuildSweepAfterHours())
	assert.Equal(t, 0, Settings{BuildSweepAfterHours: -3}.GetBuildSweepAfterHours(), "a negative value is off")

	out, err := json.Marshal(Settings{})
	require.NoError(t, err)
	assert.NotContains(t, string(out), "build_sweep_after_hours", "off is left out")
}
