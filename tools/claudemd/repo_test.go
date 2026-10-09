package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// repoRoot is the checkout this package sits in.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(root, "go.mod"))
	return root
}

// TestRepo_ClaudeMDStructure holds this repo's docs to the conventions:
// budgets, phrases, shape, coverage of every Go package, links, orphan
// guides, the skills list and the strong identifiers. A budget overrun is
// fixed by moving context into a docs/claude guide, not by raising it.
func TestRepo_ClaudeMDStructure(t *testing.T) {
	root := repoRoot(t)
	pkgs, err := packageDirs(root)
	require.NoError(t, err)
	found, err := findDocs(root)
	require.NoError(t, err)
	// Floors, not counts: they catch a walk that silently finds nothing.
	require.GreaterOrEqual(t, len(pkgs), 20, "Go package dirs found")
	require.GreaterOrEqual(t, len(found.claude), 10, "CLAUDE.md files found")
	ps, err := check(root, repoConfig)
	require.NoError(t, err)
	for _, p := range ps {
		t.Error(p)
	}
}

// TestRepo_ClaudeMDIdents logs the backticked symbols the docs name that no
// source file holds. Advisory: it fails only with LOOM_CLAUDEMD_STRICT=1.
func TestRepo_ClaudeMDIdents(t *testing.T) {
	ps, err := idents(repoRoot(t))
	require.NoError(t, err)
	strict := os.Getenv("LOOM_CLAUDEMD_STRICT") == "1"
	for _, p := range ps {
		if strict {
			t.Error(p)
		} else {
			t.Log("advisory:", p)
		}
	}
}
