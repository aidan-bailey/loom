package review

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestComposePrompt_Empty(t *testing.T) {
	assert.Equal(t, "", ComposePrompt("/wt", nil))
	assert.Equal(t, "", ComposePrompt("/wt", []*ReviewState{{File: "/wt/a.md"}}))
}

func TestComposePrompt_FormatsCommentsRelativeToRoot(t *testing.T) {
	states := []*ReviewState{
		{File: "/wt/docs/plan.md", Comments: []Comment{
			{ID: "a", Line: 3, ContentSnippet: "## Rollout", Body: "split into two phases"},
			{ID: "b", Line: 10, EndLine: 14, Body: "this section contradicts the goals"},
		}},
		{File: "/wt/notes.md", Comments: []Comment{
			{ID: "c", Line: 1, Body: "wrong title"},
		}},
	}
	got := ComposePrompt("/wt", states)

	assert.True(t, strings.HasPrefix(got, "Please address the following review comments"))
	assert.Contains(t, got, "docs/plan.md:3\n> ## Rollout\nsplit into two phases")
	assert.Contains(t, got, "docs/plan.md:10-14\nthis section contradicts the goals")
	assert.Contains(t, got, "notes.md:1\nwrong title")
	// Single-line comment with no snippet has no quote line.
	assert.NotContains(t, got, "notes.md:1\n>")
}

func TestComposePrompt_FileOutsideRootKeptVerbatim(t *testing.T) {
	got := ComposePrompt("/wt", []*ReviewState{
		{File: "relative.md", Comments: []Comment{{ID: "a", Line: 2, Body: "x"}}},
	})
	assert.Contains(t, got, "relative.md:2")
}
