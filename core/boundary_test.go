package core

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCoreImportsNoUI fails when core, or any loom package it reaches,
// imports the TUI: app, ui and its subpackages, script, keys, or a
// charm.land library (Bubble Tea, Bubbles, Lip Gloss). The daemon runs
// core with no terminal; a UI import here is a seam someone has to cut
// again. Every file counts, whatever its build tags; tests do not.
func TestCoreImportsNoUI(t *testing.T) {
	const module = "github.com/aidan-bailey/loom"
	root, err := filepath.Abs("..")
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(root, "go.mod"))

	forbidden := func(ip string) bool {
		switch {
		case ip == module+"/app", ip == module+"/script", ip == module+"/keys":
			return true
		case ip == module+"/ui", strings.HasPrefix(ip, module+"/ui/"):
			return true
		case strings.HasPrefix(ip, "charm.land/"):
			return true
		}
		return false
	}
	seen := map[string]bool{}
	var walk func(ip string, via []string)
	walk = func(ip string, via []string) {
		if seen[ip] {
			return
		}
		seen[ip] = true
		dir := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(strings.TrimPrefix(ip, module), "/")))
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, name), nil, parser.ImportsOnly)
			require.NoError(t, err)
			for _, spec := range f.Imports {
				dep, err := strconv.Unquote(spec.Path.Value)
				require.NoError(t, err)
				chain := append(append([]string(nil), via...), ip)
				if forbidden(dep) {
					t.Errorf("%s imports %s (reached via %s)", ip, dep, strings.Join(chain, " → "))
				}
				if dep == module || strings.HasPrefix(dep, module+"/") {
					walk(dep, chain)
				}
			}
		}
	}
	walk(module+"/core", nil)
}
