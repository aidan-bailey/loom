package sched

import (
	"bytes"
	"encoding/json"
	"time"
)

// UsageLimitTail is how much of an agent job's output.log the daemon reads
// for UsageLimitOf: the result and the events before it are the log's last
// lines.
const UsageLimitTail = 256 << 10

// usageLimitReached is the api_error of a request the account's own usage
// limit refused (Claude Code 2.1.322's result and assistant schemas).
const usageLimitReached = "usage_limit_reached"

// UsageLimit is a usage limit an agent stopped on (usage-limit hold spec
// §1): the window Claude named ("five_hour", "seven_day", …) and when it
// resets. Either is zero when the log doesn't say.
type UsageLimit struct {
	Window string
	Resets time.Time
}

// rateLimitInfo is the shape rate_limit_event carries, and a refused
// request's api_error_params.rate_limit_info.
type rateLimitInfo struct {
	Status         string `json:"status"`
	ResetsAt       int64  `json:"resetsAt"`
	RateLimitType  string `json:"rateLimitType"`
	UnifiedWindows map[string]struct {
		ResetsAt int64  `json:"resetsAt"`
		Status   string `json:"status"`
	} `json:"unifiedWindows"`
}

// rejected is the limit info names as refused: its own window when its
// status is rejected, else a unified window whose status is (the one that
// resets first, when several are), or false.
func (i *rateLimitInfo) rejected() (UsageLimit, bool) {
	if i == nil {
		return UsageLimit{}, false
	}
	if i.Status == "rejected" {
		return UsageLimit{Window: i.RateLimitType, Resets: unixOrZero(i.ResetsAt)}, true
	}
	var got UsageLimit
	found := false
	for name, w := range i.UnifiedWindows {
		if w.Status != "rejected" {
			continue
		}
		at := unixOrZero(w.ResetsAt)
		if !found || at.Before(got.Resets) || (at.Equal(got.Resets) && name < got.Window) {
			got, found = UsageLimit{Window: name, Resets: at}, true
		}
	}
	return got, found
}

func unixOrZero(s int64) time.Time {
	if s <= 0 {
		return time.Time{}
	}
	return time.Unix(s, 0)
}

// UsageLimitOf reads an agent's stream-json log (or its tail; a first line
// cut short is skipped) and reports whether the agent stopped on a usage
// limit: its last result is an error whose api_error is
// usage_limit_reached. The window and reset come from the refused request's
// own rate_limit_info (the assistant message that wraps the API error), else
// from the latest rate_limit_event that names a rejected window; a limit
// with neither has its window and reset unknown. An allowed or
// allowed_warning event alone says nothing of the refused window, since a
// refused request updates no event. It is a pure function of the log.
func UsageLimitOf(log []byte) (UsageLimit, bool) {
	type line struct {
		Type           string         `json:"type"`
		IsError        bool           `json:"is_error"`
		APIError       string         `json:"api_error"`
		RateLimitInfo  *rateLimitInfo `json:"rate_limit_info"`
		APIErrorParams struct {
			RateLimitInfo *rateLimitInfo `json:"rate_limit_info"`
		} `json:"api_error_params"`
	}
	var (
		limited, haveResult  bool
		fromError, fromEvent UsageLimit
		haveError, haveEvent bool
	)
	for _, raw := range bytes.Split(log, []byte("\n")) {
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 || raw[0] != '{' {
			continue
		}
		var l line
		if json.Unmarshal(raw, &l) != nil {
			continue
		}
		switch l.Type {
		case "result":
			haveResult = true
			limited = l.IsError && l.APIError == usageLimitReached
		case "rate_limit_event":
			if u, ok := l.RateLimitInfo.rejected(); ok {
				fromEvent, haveEvent = u, true
			}
		case "assistant":
			if l.APIError != usageLimitReached {
				continue
			}
			if u, ok := l.APIErrorParams.RateLimitInfo.rejected(); ok {
				fromError, haveError = u, true
			}
		}
	}
	switch {
	case !haveResult || !limited:
		return UsageLimit{}, false
	case haveError:
		return fromError, true
	case haveEvent:
		return fromEvent, true
	}
	return UsageLimit{}, true
}

// WindowLabel is how a person reads a rate-limit window: "5-hour" for
// five_hour, "7-day" for seven_day, "usage" when unknown, and the name
// itself for one this build doesn't know.
func WindowLabel(w string) string {
	switch w {
	case "":
		return "usage"
	case "five_hour":
		return "5-hour"
	case "seven_day":
		return "7-day"
	case "seven_day_opus":
		return "7-day Opus"
	case "seven_day_sonnet":
		return "7-day Sonnet"
	case "seven_day_overage_included":
		return "7-day overage"
	}
	return w
}

// LimitMargin is how long past its reset an account's usage limit still
// holds its agents (usage-limit hold spec §2), so the first agent after it
// is not refused by a clock a little behind the server's.
const LimitMargin = time.Minute

// AccountLimits is the usage limits the daemon knows, by account key
// (Record.AccountKey). Admission holds an account's queued agent jobs while
// its limit's reset, plus LimitMargin, is still ahead; a limit with no reset
// holds nothing.
type AccountLimits map[string]UsageLimit

// holding is why l holds account's queued agents at now, or "".
func (l AccountLimits) holding(account string, now time.Time) string {
	u, ok := l[account]
	if !ok || u.Resets.IsZero() || !now.Before(u.Resets.Add(LimitMargin)) {
		return ""
	}
	return "waits for the account's usage limit (" + WindowLabel(u.Window) + ", resets " + ResetText(u.Resets, now) + ")"
}

// ResetText is when a limit resets, in local time, as short as now allows:
// the time alone on now's day, with the weekday within the week, else with
// the date.
func ResetText(at, now time.Time) string {
	if at.IsZero() {
		return "an unknown time"
	}
	at, now = at.Local(), now.Local()
	y, m, d := at.Date()
	ny, nm, nd := now.Date()
	switch {
	case y == ny && m == nm && d == nd:
		return at.Format("15:04")
	case at.After(now) && at.Sub(now) < 6*24*time.Hour:
		return at.Format("Mon 15:04")
	}
	return at.Format("2 Jan 15:04")
}
