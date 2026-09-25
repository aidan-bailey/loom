package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/account"
)

// AccountStatus is the render-ready view of one Claude account, shared by
// the usage strip, the Launch Options Account row and the Accounts screen.
type AccountStatus struct {
	Name string
	// IsDefault marks the account new sessions preselect ("*").
	IsDefault bool
	// Usage is the last good probe; a zero At means never probed.
	Usage account.Usage
	// Failing reports that the latest probe failed (Usage is older).
	Failing bool
	// LoggedOut reports that `claude auth status` said so.
	LoggedOut bool
}

// UsageStaleAfter is when a usage sample renders dimmed with its age: two
// of app's 2-minute probe intervals.
const UsageStaleAfter = 4 * time.Minute

// AccountUsageText is the plain usage summary: "5h 12% · 7d 31%", plus
// " · 9m ago" once stale; "logged out"; "n/a" when plan limits don't apply
// (API-key auth); "—" before the first successful probe.
func AccountUsageText(s AccountStatus, now time.Time) string {
	return accountUsageText(s, now, true)
}

func accountUsageText(s AccountStatus, now time.Time, withWeek bool) string {
	switch {
	case s.LoggedOut:
		return "logged out"
	case s.Usage.At.IsZero():
		return "—"
	case !s.Usage.Available:
		return "n/a"
	}
	var parts []string
	if v := s.Usage.FiveHour.Text(now); v != "" {
		parts = append(parts, "5h "+v)
	}
	if v := s.Usage.SevenDay.Text(now); withWeek && v != "" {
		parts = append(parts, "7d "+v)
	}
	if len(parts) == 0 {
		parts = append(parts, "—")
	}
	if usageStale(s, now) {
		parts = append(parts, formatUsageAge(now.Sub(s.Usage.At))+" ago")
	}
	return strings.Join(parts, " · ")
}

func usageStale(s AccountStatus, now time.Time) bool {
	return !s.Usage.At.IsZero() && now.Sub(s.Usage.At) >= UsageStaleAfter
}

func formatUsageAge(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}

// usageSeverity ranks the fullest live window: 2 at ≥95%, 1 at ≥80%, else
// 0. A window past its reset counts as empty.
func usageSeverity(s AccountStatus, now time.Time) int {
	sev := 0
	for _, w := range []*account.Window{s.Usage.FiveHour, s.Usage.SevenDay} {
		if w == nil || (!w.ResetsAt.IsZero() && !now.Before(w.ResetsAt)) {
			continue
		}
		switch {
		case w.Pct >= 95:
			sev = 2
		case w.Pct >= 80 && sev < 1:
			sev = 1
		}
	}
	return sev
}
