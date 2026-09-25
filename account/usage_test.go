package account

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	out, err := os.ReadFile(filepath.Join("testdata", "get_usage_2.1.281.jsonl"))
	require.NoError(t, err)
	return out
}

func TestDecodeUsage_RealResponse(t *testing.T) {
	u, err := decodeUsage(fixture(t))
	require.NoError(t, err)

	assert.True(t, u.Available)
	assert.Equal(t, "max", u.Plan)
	require.NotNil(t, u.FiveHour)
	require.NotNil(t, u.SevenDay)
	assert.Equal(t, 8.0, u.FiveHour.Pct)
	assert.Equal(t, 30.0, u.SevenDay.Pct)
	assert.True(t, u.FiveHour.ResetsAt.Equal(time.Date(2026, 9, 25, 11, 20, 0, 356471000, time.UTC)))
}

func TestDecodeUsage_SkipsNoiseAndOtherRequests(t *testing.T) {
	out := `{"type":"system","subtype":"hook_started"}
not json
{"type":"control_response","response":{"subtype":"success","request_id":"someone-else","response":{"rate_limits_available":true,"rate_limits":{"five_hour":{"utilization":99}}}}}
{"type":"control_response","response":{"subtype":"success","request_id":"loom-usage","response":{"subscription_type":"pro","rate_limits_available":true,"rate_limits":{"five_hour":{"utilization":12.4,"resets_at":null},"seven_day":null}}}}
`
	u, err := decodeUsage([]byte(out))
	require.NoError(t, err)
	assert.Equal(t, "pro", u.Plan)
	require.NotNil(t, u.FiveHour)
	assert.Equal(t, 12.4, u.FiveHour.Pct)
	assert.True(t, u.FiveHour.ResetsAt.IsZero())
	assert.Nil(t, u.SevenDay)
}

func TestDecodeUsage_NoPlanLimits(t *testing.T) {
	out := `{"type":"control_response","response":{"subtype":"success","request_id":"loom-usage","response":{"subscription_type":null,"rate_limits_available":false,"rate_limits":null}}}`
	u, err := decodeUsage([]byte(out))
	require.NoError(t, err)
	assert.False(t, u.Available)
	assert.Empty(t, u.Plan)
	assert.Nil(t, u.FiveHour)
	assert.Nil(t, u.SevenDay)
}

func TestDecodeUsage_ErrorResponse(t *testing.T) {
	out := `{"type":"control_response","response":{"subtype":"error","request_id":"loom-usage","error":"boom"}}`
	_, err := decodeUsage([]byte(out))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}

func TestDecodeUsage_NoResponse(t *testing.T) {
	_, err := decodeUsage([]byte(`{"type":"system"}`))
	assert.ErrorIs(t, err, errNoUsageResponse)
}

func TestProbeUsage_RunsAHeadlessControlRequest(t *testing.T) {
	f := &recordingExec{out: fixture(t)}
	before := time.Now()

	u, err := ProbeUsage("claude --model opus", EnvFor("/acct/max-2"), "/acct/max-2", f)

	require.NoError(t, err)
	assert.False(t, u.At.Before(before), "At is when the probe started")
	assert.Equal(t, []string{"claude", "-p", "--input-format", "stream-json", "--output-format", "stream-json",
		"--verbose", "--no-session-persistence", "--setting-sources", ""}, f.cmd.Args)
	assert.Equal(t, "/acct/max-2", f.cmd.Dir)
	assert.Equal(t, usageRequest, f.stdin)
	dir, _ := envValue(f.cmd.Env, "CLAUDE_CONFIG_DIR")
	assert.Equal(t, "/acct/max-2", dir)
}

func TestProbeUsage_ExitWithoutResponseFails(t *testing.T) {
	_, err := ProbeUsage("claude", nil, "", &recordingExec{err: errors.New("exit status 1")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exit status 1")
}

func TestWindowText(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	var none *Window
	assert.Equal(t, "", none.Text(now))
	assert.Equal(t, "64%", (&Window{Pct: 63.6, ResetsAt: now.Add(time.Hour)}).Text(now))
	assert.Equal(t, "reset", (&Window{Pct: 90, ResetsAt: now.Add(-time.Minute)}).Text(now))
	assert.Equal(t, "12%", (&Window{Pct: 12}).Text(now), "no reset time means no reset")
}
