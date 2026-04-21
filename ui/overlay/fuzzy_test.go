package overlay

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFuzzyMatchEmptyPatternPreservesOrder(t *testing.T) {
	candidates := []string{"zeta.go", "alpha.go", "beta.go"}
	results := FuzzyMatch("", candidates)
	assert.Len(t, results, 3)
	assert.Equal(t, "zeta.go", results[0].Path)
	assert.Equal(t, "alpha.go", results[1].Path)
	assert.Equal(t, "beta.go", results[2].Path)
}

func TestFuzzyMatchContiguousBeatsSparse(t *testing.T) {
	results := FuzzyMatch("foo", []string{"foo.go", "f_o_o.go"})
	require := 2
	assert.Len(t, results, require)
	assert.Equal(t, "foo.go", results[0].Path, "contiguous match should rank first")
}

func TestFuzzyMatchFilenameBeatsDirectory(t *testing.T) {
	results := FuzzyMatch("util", []string{"util/main.go", "cmd/util.go"})
	assert.Equal(t, "cmd/util.go", results[0].Path,
		"match in filename should rank above match in directory segment")
}

func TestFuzzyMatchPrefixBonus(t *testing.T) {
	results := FuzzyMatch("ma", []string{"command.go", "main.go", "format.go"})
	assert.Equal(t, "main.go", results[0].Path,
		"filename prefix should get the largest bonus")
}

func TestFuzzyMatchCaseInsensitive(t *testing.T) {
	results := FuzzyMatch("README", []string{"docs/readme.md"})
	assert.Len(t, results, 1)
}

func TestFuzzyMatchFiltersNonMatches(t *testing.T) {
	results := FuzzyMatch("xyz", []string{"abc.go", "def.go"})
	assert.Empty(t, results)
}

func TestFuzzyMatchReportsMatchIndices(t *testing.T) {
	results := FuzzyMatch("ago", []string{"apple_grove.go"})
	assert.Len(t, results, 1)
	// "apple_grove.go" — 'a' at 0, 'g' at 6 (after underscore), 'o' at 8
	assert.Equal(t, []int{0, 6, 8}, results[0].MatchedIdx)
}

func TestFuzzyMatchTiebreakByShorterPath(t *testing.T) {
	// Both contain "abc" as a contiguous filename prefix so their
	// scores are equal; shorter path should win.
	results := FuzzyMatch("abc", []string{"abc_longer_name.go", "abc.go"})
	assert.Equal(t, "abc.go", results[0].Path)
}
