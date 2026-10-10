package testenv

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	modulePath    = "github.com/aidan-bailey/loom"
	configPackage = modulePath + "/config"
)

// TestEveryConfigReachingPackageIsolatesLoomDirs fails when a package
// whose tests can reach the config package (directly or through any other
// package of this module) has no TestMain calling IsolateLoomDirs or
// MustIsolateLoomDirs. Such a package's tests resolve the developer's
// real ~/.loom the moment one of them builds a worktree with an empty
// ConfigDir or touches the workspace registry.
//
// config itself is exempt: its tests exercise directory resolution,
// defaults included, so its TestMain isolates HOME instead (see
// config/config_test.go).
func TestEveryConfigReachingPackageIsolatesLoomDirs(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(root, "go.mod"))

	type pkg struct {
		imports  map[string]bool // module-internal imports of every file, tests included
		hasTests bool
		isolates bool // a _test.go file calls testenv.(Must)IsolateLoomDirs
	}
	pkgs := map[string]*pkg{}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			// vendor/ is third-party; dot-dirs include .git and .loom
			// (whose worktrees are other checkouts of this repo).
			if p != root && (d.Name() == "vendor" || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, filepath.Dir(p))
		if err != nil {
			return err
		}
		importPath := modulePath
		if rel != "." {
			importPath = path.Join(modulePath, filepath.ToSlash(rel))
		}
		file, err := parser.ParseFile(token.NewFileSet(), p, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		pk := pkgs[importPath]
		if pk == nil {
			pk = &pkg{imports: map[string]bool{}}
			pkgs[importPath] = pk
		}
		for _, spec := range file.Imports {
			ip, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if ip == modulePath || strings.HasPrefix(ip, modulePath+"/") {
				pk.imports[ip] = true
			}
		}
		if strings.HasSuffix(p, "_test.go") {
			pk.hasTests = true
			if callsIsolate(file) {
				pk.isolates = true
			}
		}
		return nil
	})
	require.NoError(t, err)

	// reaches memoizes whether a package's files import config, directly
	// or through another package of this module.
	memo := map[string]bool{}
	visiting := map[string]bool{}
	var reaches func(string) bool
	reaches = func(ip string) bool {
		if ip == configPackage {
			return true
		}
		if v, ok := memo[ip]; ok {
			return v
		}
		if visiting[ip] {
			return false
		}
		visiting[ip] = true
		found := false
		if pk := pkgs[ip]; pk != nil {
			for dep := range pk.imports {
				if reaches(dep) {
					found = true
					break
				}
			}
		}
		memo[ip] = found
		return found
	}

	var offenders []string
	for ip, pk := range pkgs {
		if !pk.hasTests || ip == configPackage || !reaches(ip) {
			continue
		}
		if !pk.isolates {
			offenders = append(offenders, ip)
		}
	}
	sort.Strings(offenders)
	assert.Empty(t, offenders,
		"these packages' tests can reach config but never call testenv.IsolateLoomDirs/MustIsolateLoomDirs from a TestMain")
}

// callsIsolate reports whether file calls testenv.IsolateLoomDirs or
// testenv.MustIsolateLoomDirs.
func callsIsolate(file *ast.File) bool {
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return !found
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if x, ok := sel.X.(*ast.Ident); ok && x.Name == "testenv" &&
			(sel.Sel.Name == "IsolateLoomDirs" || sel.Sel.Name == "MustIsolateLoomDirs") {
			found = true
		}
		return !found
	})
	return found
}

func TestIsolateLoomDirs(t *testing.T) {
	t.Setenv(EnvHome, "before-home")
	t.Setenv(EnvGlobalDir, "before-global")
	t.Setenv(EnvClaudeTmpDir, "before-claude-tmp")
	t.Setenv(EnvClaudeConfigDir, "before-claude-config")
	cleanup, err := IsolateLoomDirs()
	require.NoError(t, err)

	home, global := os.Getenv(EnvHome), os.Getenv(EnvGlobalDir)
	assert.True(t, filepath.IsAbs(home))
	assert.True(t, filepath.IsAbs(global))
	assert.NotEqual(t, home, global)
	assert.Equal(t, filepath.Dir(home), filepath.Dir(global), "both live under one throwaway dir")
	assert.DirExists(t, filepath.Dir(home))
	claudeTmp := os.Getenv(EnvClaudeTmpDir)
	assert.Equal(t, filepath.Dir(home), filepath.Dir(claudeTmp), "Claude's temp root is isolated under the same dir")
	assert.DirExists(t, claudeTmp, "a real Claude launched by an opt-in test finds its root's parent")
	assert.NoDirExists(t, filepath.Join(claudeTmp, fmt.Sprintf("claude-%d", os.Getuid())), "but no root: every archive operation stays a no-op")
	claudeConfig := os.Getenv(EnvClaudeConfigDir)
	assert.Equal(t, filepath.Dir(home), filepath.Dir(claudeConfig), "the main Claude config dir is isolated under the same dir")
	assert.DirExists(t, claudeConfig, "an empty main config dir, as a real one would exist")

	cleanup()
	assert.NoDirExists(t, filepath.Dir(home), "cleanup removes the throwaway dir")
}

// TestIsolateLoomDirs_KeepsClaudesMainDirOffTheRealHome: once isolated, the
// main Claude config dir's override (CLAUDE_CONFIG_DIR, which
// account.MainDir reads before ~/.claude) is the throwaway one, never under
// the developer's home (~/.claude, or an account dir under ~/.loom the
// shell runs as). A test daemon that fell back to ~/.claude once linked a
// test account into it, and a test's write into the account dir overwrote
// the developer's settings.json through the link. account's own
// TestMainDir_IsTheIsolatedOneInTests checks MainDir reads this variable.
// It imports no loom package: one importing config here would make every
// package whose tests import this one reach config.
func TestIsolateLoomDirs_KeepsClaudesMainDirOffTheRealHome(t *testing.T) {
	realHome, err := os.UserHomeDir()
	require.NoError(t, err)
	for _, name := range []string{EnvHome, EnvGlobalDir, EnvClaudeTmpDir, EnvClaudeConfigDir} {
		t.Setenv(name, os.Getenv(name))
	}
	cleanup, err := IsolateLoomDirs()
	require.NoError(t, err)
	t.Cleanup(cleanup)

	main := os.Getenv(EnvClaudeConfigDir)

	require.NotEmpty(t, main)
	assert.Equal(t, filepath.Dir(os.Getenv(EnvHome)), filepath.Dir(main), "the throwaway dir")
	for _, real := range []string{filepath.Join(realHome, ".claude"), filepath.Join(realHome, ".loom")} {
		assert.False(t, within(real, main), "the main Claude config dir %s is under the real %s", main, real)
	}
}

// within reports whether p is dir or lies beneath it.
func within(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
