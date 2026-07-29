package review

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReviewPath_RootedAndStable(t *testing.T) {
	root := t.TempDir()
	p1 := ReviewPath(root, filepath.Join(root, "plan.md"))
	p2 := ReviewPath(root, filepath.Join(root, "plan.md"))
	assert.Equal(t, p1, p2, "same doc must hash to the same review file")
	assert.True(t, strings.HasPrefix(p1, filepath.Join(root, ".crit", "reviews")),
		"review file must live under <root>/.crit/reviews, got %s", p1)
	assert.True(t, strings.HasSuffix(p1, ".yaml"))

	other := ReviewPath(root, filepath.Join(root, "other.md"))
	assert.NotEqual(t, p1, other)
}

func TestEnsureDirs_WritesSelfIgnoringGitignore(t *testing.T) {
	root := t.TempDir()
	assert.NoError(t, EnsureDirs(root))
	data, err := readFile(t, filepath.Join(root, ".crit", ".gitignore"))
	assert.NoError(t, err)
	assert.Equal(t, "*\n", data)
}
