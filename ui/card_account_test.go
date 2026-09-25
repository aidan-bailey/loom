package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/aidan-bailey/loom/session"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withShowAccounts(t *testing.T, on bool) {
	t.Helper()
	SetShowAccounts(on)
	t.Cleanup(func() { SetShowAccounts(false) })
}

func claudeInstance(t *testing.T, program, acct string) *session.Instance {
	t.Helper()
	inst, err := session.NewInstance(session.InstanceOptions{Title: "badge", Path: t.TempDir(), Program: program})
	require.NoError(t, err)
	inst.SetAccount(acct)
	return inst
}

func TestAccountLabel(t *testing.T) {
	withShowAccounts(t, false)
	assert.Equal(t, "", accountLabel(claudeInstance(t, "claude", "max-2")), "badges off")

	withShowAccounts(t, true)
	assert.Equal(t, "max-2", accountLabel(claudeInstance(t, "claude", "max-2")))
	assert.Equal(t, "default", accountLabel(claudeInstance(t, "claude", "")))
	assert.Equal(t, "", accountLabel(claudeInstance(t, "aider", "max-2")), "only Claude sessions have an account")
	assert.Equal(t, "", accountLabel(nil))
}

func TestBuildCardData_CarriesTheAccount(t *testing.T) {
	withShowAccounts(t, true)
	d := BuildCardData(claudeInstance(t, "claude", "max-2"), false, "", 0)
	assert.Equal(t, "max-2", d.Account)
}

func TestRenderCard_RailShowsTheAccountBadge(t *testing.T) {
	d := CardData{Title: "fix-login", Index: 1, Status: session.Running, Account: "max-2"}
	out := RenderCard(d, DensityRail, 40)
	first := ansi.Strip(strings.Split(out, "\n")[0])
	assert.Contains(t, first, "1. fix-login")
	assert.Contains(t, first, "@max-2")
	for _, line := range strings.Split(out, "\n") {
		assert.LessOrEqual(t, lipgloss.Width(line), 40)
	}
}

func TestRenderCard_NarrowRailDropsTheBadge(t *testing.T) {
	d := CardData{Title: "fix-login", Index: 1, Status: session.Running, Account: "max-2"}
	out := ansi.Strip(RenderCard(d, DensityRail, 12))
	assert.NotContains(t, out, "@max-2")
}

func TestRenderOverviewCard_ShowsTheAccount(t *testing.T) {
	d := CardData{Title: "fix-login", Branch: "aidanb/fix", Status: session.Running, Account: "max-2"}
	assert.Contains(t, ansi.Strip(renderOverviewCard(d, 50)), "@max-2")
}
