package tmux

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNoRawTmuxExec fails on any exec.Command/exec.CommandContext whose
// program is the literal "tmux" outside command.go. Raw execs would ignore
// LOOM_TMUX_SOCKET and follow $TMUX to the host server.
func TestNoRawTmuxExec(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(root, "go.mod"))
	allowed := filepath.Join(root, "session", "tmux", "command.go")

	var offenders []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			// vendor/ holds third-party code; dot-dirs include .git and
			// .loom (whose worktrees are other checkouts of this repo).
			if path != root && (d.Name() == "vendor" || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || path == allowed {
			return nil
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "exec" {
				return true
			}
			progArg := map[string]int{"Command": 0, "CommandContext": 1}
			idx, ok := progArg[sel.Sel.Name]
			if !ok || len(call.Args) <= idx {
				return true
			}
			if lit, ok := call.Args[idx].(*ast.BasicLit); ok && lit.Value == `"tmux"` {
				offenders = append(offenders, fset.Position(call.Pos()).String())
			}
			return true
		})
		return nil
	})
	require.NoError(t, err)
	assert.Empty(t, offenders, "invoke tmux via tmux.Command / tmux.CommandOnSocket (session/tmux/command.go)")
}
