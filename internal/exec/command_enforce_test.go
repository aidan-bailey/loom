package exec

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

// TestNoRawGitGhExec fails on any os/exec Command/CommandContext call whose
// program is the literal "git" or "gh" outside command.go, in non-test (*.go,
// not *_test.go) production source. A raw git exec skips LC_ALL=C, so under
// a non-English locale the stderr its caller matches on (isBranchAbsentErr,
// isWorktreeAbsentErr, …) is translated and the classification silently
// flips; a raw gh exec skips the prompt/update-notifier guards. Mirrors
// session/tmux's TestNoRawTmuxExec, with the same deliberate exemption for
// _test.go files, whose raw git calls only build and inspect fixtures.
//
// The os/exec import is resolved per file, so an aliased import
// (osexec "os/exec") or a dot import is still caught. Only a literal
// program name is detected; a name routed through a variable or constant
// is not.
func TestNoRawGitGhExec(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(root, "go.mod"))
	allowed := filepath.Join(root, "internal", "exec", "command.go")

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
		for _, pos := range rawGitGhExecs(file) {
			offenders = append(offenders, fset.Position(pos).String())
		}
		return nil
	})
	require.NoError(t, err)
	assert.Empty(t, offenders, "build git/gh subprocesses with internalexec.GitCommand / GhCommand (internal/exec/command.go)")
}

// rawGitGhExecs returns the positions of every os/exec Command or
// CommandContext call in file whose program argument is the literal "git"
// or "gh".
func rawGitGhExecs(file *ast.File) []token.Pos {
	// The local name os/exec is bound to in this file: "exec" by default,
	// the alias when renamed, "." for a dot import. A file that does not
	// import os/exec cannot call it.
	local := ""
	for _, imp := range file.Imports {
		if p, err := strconv.Unquote(imp.Path.Value); err != nil || p != "os/exec" {
			continue
		}
		local = "exec"
		if imp.Name != nil {
			local = imp.Name.Name
		}
	}
	if local == "" || local == "_" {
		return nil
	}

	progArg := map[string]int{"Command": 0, "CommandContext": 1}
	var hits []token.Pos
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		var fn string
		switch f := call.Fun.(type) {
		case *ast.SelectorExpr:
			if pkg, ok := f.X.(*ast.Ident); !ok || pkg.Name != local {
				return true
			}
			fn = f.Sel.Name
		case *ast.Ident:
			if local != "." {
				return true
			}
			fn = f.Name
		default:
			return true
		}
		idx, ok := progArg[fn]
		if !ok || len(call.Args) <= idx {
			return true
		}
		if lit, ok := call.Args[idx].(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if prog, err := strconv.Unquote(lit.Value); err == nil && (prog == "git" || prog == "gh") {
				hits = append(hits, call.Pos())
			}
		}
		return true
	})
	return hits
}

// TestRawGitGhExecs_Detection pins the matcher itself, so a refactor that
// quietly stops matching (a wrong alias lookup, say) fails here instead of
// turning TestNoRawGitGhExec into a test that can never fail.
func TestRawGitGhExecs_Detection(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"plain Command git", `import "os/exec"; func f() { exec.Command("git", "status") }`, 1},
		{"CommandContext gh", `import "os/exec"; func f() { exec.CommandContext(ctx, "gh", "auth") }`, 1},
		{"raw string literal", "import \"os/exec\"; func f() { exec.Command(`git`) }", 1},
		{"aliased import", `import osexec "os/exec"; func f() { osexec.Command("git") }`, 1},
		{"dot import", `import . "os/exec"; func f() { CommandContext(ctx, "gh") }`, 1},
		{"other program", `import "os/exec"; func f() { exec.Command("tmux", "ls") }`, 0},
		{"git as a later arg", `import "os/exec"; func f() { exec.Command("sh", "-c", "git status") }`, 0},
		{"exec is not os/exec", `import exec "example.com/other"; func f() { exec.Command("git") }`, 0},
		{"aliased import, default name unbound", `import osexec "os/exec"; func f() { exec.Command("git") }`, 0},
		{"no os/exec import", `func f() { exec.Command("git") }`, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "x.go", "package x; "+tc.src, 0)
			require.NoError(t, err)
			assert.Len(t, rawGitGhExecs(file), tc.want)
		})
	}
}
