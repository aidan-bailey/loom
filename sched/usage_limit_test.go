package sched

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// streamLog joins stream-json lines into a log.
func streamLog(lines ...string) []byte { return []byte(strings.Join(lines, "\n") + "\n") }

const (
	initLine    = `{"type":"system","subtype":"init","session_id":"s1"}`
	allowedLine = `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed_warning","resetsAt":1794398400,"rateLimitType":"seven_day","utilization":0.88,"unifiedWindows":{"five_hour":{"utilization":0.11,"resetsAt":1794357000,"status":"allowed"},"seven_day":{"utilization":0.88,"resetsAt":1794398400,"status":"allowed_warning"}}}}`
	limitResult = `{"type":"result","subtype":"success","is_error":true,"api_error_status":429,"api_error":"usage_limit_reached","result":"You've hit your 5-hour limit · resets 4:30pm","session_id":"s1"}`
	okResult    = `{"type":"result","subtype":"success","is_error":false,"result":"done\nSTATUS: DONE","session_id":"s1"}`
)

func TestUsageLimitOf(t *testing.T) {
	cases := []struct {
		name    string
		log     []byte
		limited bool
		want    UsageLimit
	}{
		{
			name: "a refused request's own rate_limit_info gives the window and reset",
			log: streamLog(initLine, allowedLine,
				`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"You've hit your 5-hour limit"}]},"is_api_error_message":true,"api_error_status":429,"api_error":"usage_limit_reached","api_error_params":{"rate_limit_info":{"status":"rejected","rateLimitType":"five_hour","resetsAt":1794360600}}}`,
				limitResult),
			limited: true,
			want:    UsageLimit{Window: "five_hour", Resets: time.Unix(1794360600, 0)},
		},
		{
			name: "a rejected rate_limit_event gives the window it names",
			log: streamLog(initLine,
				`{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","resetsAt":1794360600,"rateLimitType":"five_hour"}}`,
				limitResult),
			limited: true,
			want:    UsageLimit{Window: "five_hour", Resets: time.Unix(1794360600, 0)},
		},
		{
			name: "else a rejected window among the latest event's unified windows",
			log: streamLog(initLine,
				`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1794398400,"rateLimitType":"seven_day","unifiedWindows":{"five_hour":{"utilization":1.0,"resetsAt":1794360600,"status":"rejected"},"seven_day":{"utilization":0.5,"resetsAt":1794398400,"status":"allowed"}}}}`,
				limitResult),
			limited: true,
			want:    UsageLimit{Window: "five_hour", Resets: time.Unix(1794360600, 0)},
		},
		{
			name:    "with no event the limit's reset is unknown",
			log:     streamLog(initLine, limitResult),
			limited: true,
		},
		{
			name:    "an allowed event before the 429 says nothing of the refused window",
			log:     streamLog(initLine, allowedLine, limitResult),
			limited: true,
		},
		{
			name: "an allowed_warning event on a successful run is no limit",
			log:  streamLog(initLine, allowedLine, okResult),
		},
		{
			name: "a plain error result is no limit",
			log:  streamLog(initLine, allowedLine, `{"type":"result","subtype":"success","is_error":true,"api_error_status":500,"result":"API Error: 500","session_id":"s1"}`),
		},
		{
			name: "a throttle's 429 without usage_limit_reached is no limit",
			log:  streamLog(initLine, `{"type":"result","subtype":"success","is_error":true,"api_error_status":429,"result":"rate limited","session_id":"s1"}`),
		},
		{
			name: "a log with no result is no limit",
			log:  streamLog(initLine, allowedLine),
		},
		{
			name: "a tail cut mid-line still reads",
			log: append([]byte(`ted":0.5}}`+"\n"), streamLog(
				`{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","resetsAt":1794360600,"rateLimitType":"seven_day_opus"}}`,
				limitResult)...),
			limited: true,
			want:    UsageLimit{Window: "seven_day_opus", Resets: time.Unix(1794360600, 0)},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, limited := UsageLimitOf(tc.log)
			assert.Equal(t, tc.limited, limited)
			assert.Equal(t, tc.want.Window, got.Window)
			assert.True(t, tc.want.Resets.Equal(got.Resets), "resets %v, want %v", got.Resets, tc.want.Resets)
		})
	}
}

func TestWindowLabel(t *testing.T) {
	for w, want := range map[string]string{
		"five_hour":                  "5-hour",
		"seven_day":                  "7-day",
		"seven_day_opus":             "7-day Opus",
		"seven_day_sonnet":           "7-day Sonnet",
		"seven_day_overage_included": "7-day overage",
		"overage":                    "overage",
		"":                           "usage",
		"something_new":              "something_new",
	} {
		assert.Equal(t, want, WindowLabel(w), w)
	}
}
