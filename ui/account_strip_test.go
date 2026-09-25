package ui

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/aidan-bailey/loom/account"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
)

var stripNow = time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

func probed(five, week float64) account.Usage {
	return account.Usage{
		Available: true, Plan: "max", At: stripNow.Add(-time.Minute),
		FiveHour: &account.Window{Pct: five, ResetsAt: stripNow.Add(time.Hour)},
		SevenDay: &account.Window{Pct: week, ResetsAt: stripNow.Add(72 * time.Hour)},
	}
}

func TestAccountUsageText(t *testing.T) {
	fresh := probed(12, 31)
	stale := probed(12, 31)
	stale.At = stripNow.Add(-9 * time.Minute)
	reset := probed(90, 31)
	reset.FiveHour.ResetsAt = stripNow.Add(-time.Minute)

	cases := map[string]struct {
		s    AccountStatus
		want string
	}{
		"fresh":        {AccountStatus{Usage: fresh}, "5h 12% · 7d 31%"},
		"stale":        {AccountStatus{Usage: stale}, "5h 12% · 7d 31% · 9m ago"},
		"window reset": {AccountStatus{Usage: reset}, "5h reset · 7d 31%"},
		"never probed": {AccountStatus{}, "—"},
		"no limits":    {AccountStatus{Usage: account.Usage{At: stripNow}}, "n/a"},
		"logged out":   {AccountStatus{Usage: fresh, LoggedOut: true}, "logged out"},
	}
	for name, tc := range cases {
		assert.Equal(t, tc.want, accountUsageText(tc.s, stripNow, true), name)
	}
	assert.Equal(t, "5h 12%", accountUsageText(AccountStatus{Usage: fresh}, stripNow, false), "compact drops 7d")
}

func TestAccountStrip_HiddenWithOnlyTheDefaultAccount(t *testing.T) {
	s := NewAccountStrip()
	s.SetWidth(120)
	s.SetAccounts([]AccountStatus{{Name: account.DefaultName, IsDefault: true, Usage: probed(1, 2)}})
	assert.Equal(t, 0, s.Height())
	assert.Equal(t, "", s.render(stripNow))
}

func TestAccountStrip_RendersEveryAccount(t *testing.T) {
	s := NewAccountStrip()
	s.SetWidth(120)
	s.SetAccounts([]AccountStatus{
		{Name: account.DefaultName, IsDefault: true, Usage: probed(64, 40)},
		{Name: "max-2", Usage: probed(12, 31)},
		{Name: "max-3", LoggedOut: true},
	})
	assert.Equal(t, 1, s.Height())
	out := ansi.Strip(s.render(stripNow))
	assert.Contains(t, out, "*default")
	assert.Contains(t, out, "5h 64% · 7d 40%")
	assert.Contains(t, out, "max-2")
	assert.Contains(t, out, "max-3  logged out")
}

func TestAccountStrip_NarrowDropsTheWeekThenTruncates(t *testing.T) {
	s := NewAccountStrip()
	s.SetAccounts([]AccountStatus{
		{Name: account.DefaultName, IsDefault: true, Usage: probed(64, 40)},
		{Name: "max-2", Usage: probed(12, 31)},
	})
	s.SetWidth(40)
	out := s.render(stripNow)
	assert.NotContains(t, ansi.Strip(out), "7d")
	assert.LessOrEqual(t, lipgloss.Width(out), 40)

	s.SetWidth(12)
	out = s.render(stripNow)
	assert.LessOrEqual(t, lipgloss.Width(out), 12)
	assert.False(t, strings.Contains(out, "\n"))
}
