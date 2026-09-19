package github

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSlugTitle(t *testing.T) {
	assert.Equal(t, "gh-12-fix-the-flaky-reconcile-test", SlugTitle(Issue{Number: 12, Title: "Fix the flaky reconcile test!"}))
	assert.Equal(t, "gh-7-a-b", SlugTitle(Issue{Number: 7, Title: "  A -- b  "}))
	assert.Equal(t, "gh-3", SlugTitle(Issue{Number: 3, Title: "###"}), "no usable words: number only")
	assert.Equal(t, "gh-5", SlugTitle(Issue{Number: 5, Title: "修正テスト"}), "non-ASCII is dropped entirely, same path as no usable words")
	long := SlugTitle(Issue{Number: 1, Title: strings.Repeat("word ", 30)})
	assert.LessOrEqual(t, len(long), len("gh-1-")+slugMax)
	assert.False(t, strings.HasSuffix(long, "-"))
}

func TestSeedPrompt(t *testing.T) {
	got := SeedPrompt(Issue{Number: 12, Title: "Fix it", Body: "Steps:\n1. x", URL: "https://x/12"})
	assert.Equal(t, "You are working on GitHub issue #12 (https://x/12).\n\n# Fix it\n\nSteps:\n1. x\n", got)
}

func TestSeedPrompt_EmptyBody(t *testing.T) {
	got := SeedPrompt(Issue{Number: 12, Title: "Fix it", URL: "https://x/12"})
	assert.Equal(t, "You are working on GitHub issue #12 (https://x/12).\n\n# Fix it\n", got)
}

func TestParseShorthand(t *testing.T) {
	n, rest, ok := ParseShorthand("#123")
	assert.True(t, ok)
	assert.Equal(t, 123, n)
	assert.Equal(t, "", rest)

	n, rest, ok = ParseShorthand("  #45 and also refactor the tests")
	assert.True(t, ok)
	assert.Equal(t, 45, n)
	assert.Equal(t, "and also refactor the tests", rest)

	_, _, ok = ParseShorthand("fix #45")
	assert.False(t, ok, "only a leading token counts")
	_, _, ok = ParseShorthand("#abc")
	assert.False(t, ok)
	_, _, ok = ParseShorthand("#0")
	assert.False(t, ok)
	_, _, ok = ParseShorthand("")
	assert.False(t, ok)
}
