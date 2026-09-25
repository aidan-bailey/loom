package account

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
)

// Window is one plan rate-limit window.
type Window struct {
	// Pct is the share of the window used, 0-100.
	Pct float64
	// ResetsAt is when the window resets; zero when the server gave none.
	ResetsAt time.Time
}

// Text renders w for display: "64%", or "reset" once ResetsAt has passed
// (the percentage would describe a window that no longer exists). "" for a
// nil window.
func (w *Window) Text(now time.Time) string {
	if w == nil {
		return ""
	}
	if !w.ResetsAt.IsZero() && !now.Before(w.ResetsAt) {
		return "reset"
	}
	return fmt.Sprintf("%.0f%%", w.Pct)
}

// Usage is one account's plan usage, from ProbeUsage.
type Usage struct {
	// Available is false when plan limits do not apply (API key, Bedrock,
	// Vertex); the windows are then nil.
	Available bool
	// Plan is the subscription ("pro", "max", …); empty for API-key auth.
	Plan string
	// FiveHour and SevenDay are nil when the server did not report them.
	FiveHour, SevenDay *Window
	// At is when the probe started. Zero means never probed.
	At time.Time
}

// usageTimeout bounds one probe. It measured ~1.4s on 2.1.281, but the
// usage endpoint is a network call, so this is a network budget.
const usageTimeout = 15 * time.Second

const usageRequestID = "loom-usage"

// usageRequest is the SDK control request for the structured /usage data.
// skip_behaviors skips a scan of a week of local transcripts that only the
// /usage dialog needs; the CLI's own schema describes it as being "for
// callers that need only the plan rate limits, such as a usage meter".
// get_usage is marked experimental, so decodeUsage reads only the
// documented rate_limits windows.
const usageRequest = `{"type":"control_request","request_id":"` + usageRequestID +
	`","request":{"subtype":"get_usage","skip_behaviors":true}}` + "\n"

// ProbeUsage asks the account's CLI for its plan usage without starting a
// conversation: a headless `claude -p` over stream-json that answers the
// one control request on stdin and exits when stdin closes. No model call,
// no cost. The CLI answers from its own usage snapshot when that is under
// a minute old, so polling does not hammer the usage endpoint.
// --setting-sources "" keeps user and project settings, and the plugin
// hooks they enable, out of the probe (auth is read from the config dir
// regardless); --no-session-persistence writes no transcript. cwd is where
// the probe runs: pass the account's config dir so no project entry is
// recorded for an arbitrary directory.
func ProbeUsage(program string, env []string, cwd string, r internalexec.Executor) (Usage, error) {
	bin := Binary(program)
	if bin == "" {
		return Usage{}, errors.New("no claude program configured")
	}
	at := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), usageTimeout)
	defer cancel()
	c := withEnv(exec.CommandContext(ctx, bin,
		"-p", "--input-format", "stream-json", "--output-format", "stream-json",
		"--verbose", "--no-session-persistence", "--setting-sources", ""), env)
	c.Dir = cwd
	c.Stdin = strings.NewReader(usageRequest)
	out, err := runner(r).Output(c)
	u, derr := decodeUsage(out)
	if derr != nil {
		if err != nil {
			return Usage{}, fmt.Errorf("claude usage probe: %w (%v)", err, derr)
		}
		return Usage{}, derr
	}
	u.At = at
	return u, nil
}

// controlLine is one stream-json line, decoded far enough to find loom's
// control_response.
type controlLine struct {
	Type     string `json:"type"`
	Response struct {
		Subtype   string          `json:"subtype"`
		RequestID string          `json:"request_id"`
		Error     string          `json:"error"`
		Response  json.RawMessage `json:"response"`
	} `json:"response"`
}

type usagePayload struct {
	SubscriptionType    *string `json:"subscription_type"`
	RateLimitsAvailable bool    `json:"rate_limits_available"`
	RateLimits          *struct {
		FiveHour *rawWindow `json:"five_hour"`
		SevenDay *rawWindow `json:"seven_day"`
	} `json:"rate_limits"`
}

type rawWindow struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    *string  `json:"resets_at"`
}

var errNoUsageResponse = errors.New("claude usage probe: no get_usage response")

// decodeUsage finds loom's control_response among the stream-json lines
// (hook events and the like may precede it) and decodes its windows.
func decodeUsage(out []byte) (Usage, error) {
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var line controlLine
		if json.Unmarshal(sc.Bytes(), &line) != nil || line.Type != "control_response" ||
			line.Response.RequestID != usageRequestID {
			continue
		}
		if line.Response.Subtype != "success" {
			return Usage{}, fmt.Errorf("claude usage probe: get_usage failed: %s", line.Response.Error)
		}
		var p usagePayload
		if err := json.Unmarshal(line.Response.Response, &p); err != nil {
			return Usage{}, fmt.Errorf("claude usage probe: decode response: %w", err)
		}
		u := Usage{Available: p.RateLimitsAvailable}
		if p.SubscriptionType != nil {
			u.Plan = *p.SubscriptionType
		}
		if p.RateLimits != nil {
			u.FiveHour = p.RateLimits.FiveHour.window()
			u.SevenDay = p.RateLimits.SevenDay.window()
		}
		return u, nil
	}
	return Usage{}, errNoUsageResponse
}

// window converts a reported window: nil when absent or without a
// utilization. An unparseable reset time is dropped, not fatal.
func (w *rawWindow) window() *Window {
	if w == nil || w.Utilization == nil {
		return nil
	}
	out := &Window{Pct: *w.Utilization}
	if w.ResetsAt != nil {
		if t, err := time.Parse(time.RFC3339Nano, *w.ResetsAt); err == nil {
			out.ResetsAt = t
		}
	}
	return out
}
