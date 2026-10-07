package testenv

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

// TestNoProductionCallsOfTestSeams fails when production source (a .go
// file that is not a _test.go) calls a function or method whose name ends
// in ForTest, or takes one as a value (x.FooForTest). Those are test seams
// (core/seams.go, and the …ForTest methods in session/tmux and ui), and
// only their name keeps them out of the shipped binary's paths.
//
// Declaring a seam is allowed, and so is a seam's own body using another
// seam (ui's SplitPane.InjectTerminalSessionForTest delegates to
// TerminalPane.InjectSessionForTest): a seam's body only runs from tests.
func TestNoProductionCallsOfTestSeams(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(root, "go.mod"))

	isSeam := func(name string) bool { return strings.HasSuffix(name, "ForTest") }
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
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && isSeam(fn.Name.Name) {
				continue
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				var name string
				switch n := n.(type) {
				case *ast.SelectorExpr: // pkg.FooForTest or x.FooForTest, called or not
					name = n.Sel.Name
				case *ast.CallExpr: // FooForTest() within its own package
					if id, ok := n.Fun.(*ast.Ident); ok {
						name = id.Name
					}
				}
				if isSeam(name) {
					offenders = append(offenders, fset.Position(n.Pos()).String()+": "+name)
				}
				return true
			})
		}
		return nil
	})
	require.NoError(t, err)
	assert.Empty(t, offenders, "production code must not use a test seam (a …ForTest function or method)")
}
