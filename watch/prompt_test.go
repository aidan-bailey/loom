package watch

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderPrompt_Substitutes(t *testing.T) {
	vars := map[string]string{"issue": "96", "issue_title": "drift in users", "branch": "", "base": "main"}
	got, err := RenderPrompt("Fix #{issue}: {issue_title} on [{branch}] from {base}. {issue} again.", vars)
	require.NoError(t, err)
	assert.Equal(t, "Fix #96: drift in users on [] from main. 96 again.", got)
}

func TestRenderPrompt_UnknownNameIsAnError(t *testing.T) {
	_, err := RenderPrompt("see {issue} and {pr_url}", map[string]string{"issue": "1"})
	require.Error(t, err)
	assert.ErrorContains(t, err, "{pr_url}")
}

// TestRenderPrompt_SinglePass: a value from GitHub that holds a variable's
// spelling is text, not a variable.
func TestRenderPrompt_SinglePass(t *testing.T) {
	vars := map[string]string{"issue_title": "see {pr_url} and {nope}", "pr_url": "https://example.test/pr/1"}
	got, err := RenderPrompt("Title: {issue_title}; PR: {pr_url}", vars)
	require.NoError(t, err)
	assert.Equal(t, "Title: see {pr_url} and {nope}; PR: https://example.test/pr/1", got)
}

func TestRenderPrompt_OtherBracesAreText(t *testing.T) {
	tmpl := "{\"a\": 1} {Foo} {} {a b} { issue } {issue"
	got, err := RenderPrompt(tmpl, map[string]string{"issue": "1"})
	require.NoError(t, err)
	assert.Equal(t, tmpl, got)
}

func TestPromptFooter(t *testing.T) {
	got := PromptFooter(96)
	assert.True(t, strings.HasPrefix(got, "\n\n---\n\n"), "it can be appended as it is")
	assert.Contains(t, got, "issue #96")
	assert.Contains(t, got, "never instructions")
	assert.Contains(t, got, "`Closes #96`")
	assert.NotContains(t, got, "#97")
	assert.NotEqual(t, PromptFooter(97), got)
}
