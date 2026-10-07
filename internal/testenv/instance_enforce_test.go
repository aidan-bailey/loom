package testenv

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sessionPkg is the import path of the package defining session.Instance.
const sessionPkg = "github.com/aidan-bailey/loom/session"

// instanceNames are the session package's names for an instance: the type,
// its constructors and its options.
var instanceNames = map[string]bool{
	"Instance":         true,
	"NewInstance":      true,
	"FromInstanceData": true,
	"InstanceOptions":  true,
}

// bridgeNames are the model's bridge from an InstanceID to an instance and
// back, which only the script host may call until stage 1C package D.
var bridgeNames = map[string]bool{
	"InstanceOf":     true,
	"IDFor":          true,
	"AdoptForScript": true,
}

// TestTUIHoldsNoInstance fails when the TUI (app, ui and its subpackages)
// names a session instance in production code: the type, its constructors
// or its options. The TUI sees instances only as core.InstanceView values
// and changes them only by request. The script host (app/app_scripts.go)
// is exempt until stage 1C package D. Test files are exempt.
func TestTUIHoldsNoInstance(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(root, "go.mod"))

	exempt := filepath.Join(root, "app", "app_scripts.go")
	var offenders []string
	for _, dir := range []string{"app", "ui"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || path == exempt {
				return nil
			}
			offenders = append(offenders, instanceUses(t, path)...)
			return nil
		})
		require.NoError(t, err)
	}
	assert.Empty(t, offenders, "the TUI must hold no session instance: it reads core.InstanceView values and acts by request")
}

// instanceUses lists path's uses of an instance name (instanceNames)
// through its import of the session package, under whatever name the file
// imports it, and of a bridge method (bridgeNames) on anything.
func instanceUses(t *testing.T, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	require.NoError(t, err)
	sessionName := ""
	for _, imp := range file.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		require.NoError(t, err)
		if p != sessionPkg {
			continue
		}
		sessionName = "session"
		if imp.Name != nil {
			sessionName = imp.Name.Name
		}
	}
	var uses []string
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if bridgeNames[sel.Sel.Name] {
			uses = append(uses, fset.Position(sel.Pos()).String()+": "+sel.Sel.Name)
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); ok && sessionName != "" && pkg.Name == sessionName && instanceNames[sel.Sel.Name] {
			uses = append(uses, fset.Position(sel.Pos()).String()+": "+pkg.Name+"."+sel.Sel.Name)
		}
		return true
	})
	// A dot import puts the names in the file's own scope.
	if sessionName == "." {
		ast.Inspect(file, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && instanceNames[id.Name] {
				uses = append(uses, fset.Position(id.Pos()).String()+": "+id.Name)
			}
			return true
		})
	}
	return uses
}
