package tmux

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

// exactTargetAllowed lists the production functions that may pass a -t
// value not built by SessionTarget/PaneTarget, keyed "<file>:<func>"
// relative to the module root. Each entry must name a target that is
// exact by construction.
var exactTargetAllowed = map[string]string{
	// $TMUX_PANE is a pane id ("%12"): ids are unique and never
	// prefix-matched.
	"session/tmux/command.go:EnclosingSessionName": "pane id from $TMUX_PANE",
}

// TestTmuxTargetsAreExact fails on any tmux target in production code (not
// *_test.go) that could prefix-match another session. tmux resolves a bare
// `-t name` by exact match first and then by prefix, so a command aimed at
// a dead loom_api lands on a live loom_api-v2: Restore attached its
// preview PTY there, and every keystroke and prompt went to that agent.
// Every "-t" must be followed by SessionTarget(...) or PaneTarget(...), and
// no string may spell a target inline ("-t=" + name, fmt.Sprintf("-t=%s",
// …)). A "-t" for some other program would need an exactTargetAllowed
// entry; loom has none.
func TestTmuxTargetsAreExact(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(root, "go.mod"))

	var offenders []string
	used := map[string]bool{}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if path != root && (d.Name() == "vendor" || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		report := func(fn string, n ast.Node, why string) {
			key := rel + ":" + fn
			if _, ok := exactTargetAllowed[key]; ok {
				used[key] = true
				return
			}
			offenders = append(offenders, fset.Position(n.Pos()).String()+": "+why)
		}
		for _, decl := range file.Decls {
			fn := "<package level>"
			if fd, ok := decl.(*ast.FuncDecl); ok {
				fn = fd.Name.Name
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.BasicLit:
					if s, ok := stringLit(n); ok && strings.HasPrefix(s, "-t") && s != "-t" {
						report(fn, n, "target spelled inline in "+n.Value+"; pass \"-t\", SessionTarget(name) or PaneTarget(name)")
					}
				case *ast.CallExpr:
					checkTargetList(n.Args, func(e ast.Node, why string) { report(fn, e, why) })
				case *ast.CompositeLit:
					checkTargetList(n.Elts, func(e ast.Node, why string) { report(fn, e, why) })
				}
				return true
			})
		}
		return nil
	})
	require.NoError(t, err)
	assert.Empty(t, offenders, "build every tmux -t value with tmux.SessionTarget or tmux.PaneTarget (session/tmux/target.go)")
	for key := range exactTargetAllowed {
		assert.True(t, used[key], "exactTargetAllowed entry %s matches nothing; remove it", key)
	}
}

// checkTargetList reports every "-t" in list whose next element is not a
// SessionTarget/PaneTarget call.
func checkTargetList(list []ast.Expr, report func(ast.Node, string)) {
	for i, e := range list {
		lit, ok := e.(*ast.BasicLit)
		if !ok {
			continue
		}
		if s, ok := stringLit(lit); !ok || s != "-t" {
			continue
		}
		if i+1 >= len(list) || !isTargetHelperCall(list[i+1]) {
			report(lit, "\"-t\" not followed by SessionTarget(...) or PaneTarget(...)")
		}
	}
}

// isTargetHelperCall reports whether e calls SessionTarget or PaneTarget,
// qualified by tmux or (inside this package) not.
func isTargetHelperCall(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	var name string
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		name = fun.Name
	case *ast.SelectorExpr:
		if x, ok := fun.X.(*ast.Ident); !ok || x.Name != "tmux" {
			return false
		}
		name = fun.Sel.Name
	default:
		return false
	}
	return name == "SessionTarget" || name == "PaneTarget"
}

// stringLit returns the value of a string literal.
func stringLit(lit *ast.BasicLit) (string, bool) {
	if lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}

func TestTargetHelpers(t *testing.T) {
	assert.Equal(t, "=loom_api", SessionTarget("loom_api"))
	assert.Equal(t, "=loom_api:", PaneTarget("loom_api"))
	for name, want := range map[string]bool{
		"loom_api":      true,
		"loom_api-v2":   true,
		"loom_fix:0":    false,
		"loom_a.b":      false,
		"":              false,
		"claudesquad_x": true,
	} {
		assert.Equal(t, want, ExactlyTargetable(name), name)
	}
	for _, title := range []string{"fix: login", "v1.2", "a b:c.d"} {
		assert.True(t, ExactlyTargetable(ToLoomTmuxName(title)), title)
		assert.True(t, ExactlyTargetable(ToLegacyTmuxName(title)), title)
	}
}
