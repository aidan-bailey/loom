// Package flow is the agent flow loom ships: the `flow` Claude plugin
// (plugin/: the pinned agents, the owner-started skills and the dispatch
// guide) and the tools behind `loom flow` (lib/). Each Claude launch gets
// its own copy in its hooks folder (Write), passed with --plugin-dir and
// named in the launch's environment (EnvRoot). Design:
// docs/superpowers/specs/2026-11-05-flow-in-loom-distribution-design.md.
package flow

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// files is the flow tree. all: keeps plugin/.claude-plugin, which a plain
// pattern skips; plain lib leaves out a __pycache__ a local test run left.
//
//go:embed all:plugin lib
var files embed.FS

// EnvRoot names a launch's flow root in its environment.
const EnvRoot = "LOOM_FLOW_ROOT"

// RosterFile is the file in a flow root that holds its roster ID.
const RosterFile = "ROSTER"

// probedPrefix is what the hook input's agent_type puts before a flow
// agent's name, as TestRealClaude_FlowPluginContract saw it (P1).
const probedPrefix = "flow:"

// Write writes the flow tree into dir: plugin/, lib/ without its tests, and
// RosterFile. It refuses a dir that exists, and writes a sibling first and
// renames it into place, so dir never holds part of a copy.
func Write(dir string) error {
	if _, err := os.Lstat(dir); err == nil {
		return fmt.Errorf("flow: write %s: it exists", dir)
	}
	partial := dir + ".partial"
	if err := os.RemoveAll(partial); err != nil {
		return fmt.Errorf("flow: clear %s: %w", partial, err)
	}
	if err := writeTree(partial); err != nil {
		_ = os.RemoveAll(partial)
		return fmt.Errorf("flow: write %s: %w", dir, err)
	}
	if err := os.Rename(partial, dir); err != nil {
		_ = os.RemoveAll(partial)
		return fmt.Errorf("flow: write %s: %w", dir, err)
	}
	return nil
}

func writeTree(dst string) error {
	err := fs.WalkDir(files, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), "_test.py") {
			return nil
		}
		target := filepath.Join(dst, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		b, err := files.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o600)
	})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dst, RosterFile), []byte(RosterID()+"\n"), 0o600)
}

// IsRoot reports whether dir is a flow root Write made.
func IsRoot(dir string) bool {
	for _, part := range []string{"plugin", "lib", RosterFile} {
		if _, err := os.Stat(filepath.Join(dir, part)); err != nil {
			return false
		}
	}
	return true
}

// RosterID names the roster this build ships, as run records carry it:
// "loom flow roster " and the first 12 hex digits of the SHA-256 over
// plugin/'s files, each as its path, its length and its bytes, in path
// order. lib/ is left out: a tool's fix doesn't change the roster.
func RosterID() string { return rosterOf(files) }

func rosterOf(fsys fs.FS) string {
	h := sha256.New()
	// An embedded or in-memory tree can't fail to read.
	_ = fs.WalkDir(fsys, "plugin", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", p, len(b))
		h.Write(b)
		return nil
	})
	return "loom flow roster " + hex.EncodeToString(h.Sum(nil))[:12]
}

// Mode is how loom's git guard checks a guarded agent's calls.
type Mode string

const (
	// Developer checks every Bash call.
	Developer Mode = "developer"
	// Drafter checks every Bash, Write and Edit call, with
	// git_guard.py --drafter.
	Drafter Mode = "drafter"
)

// Guards reports whether m checks a call to tool.
func (m Mode) Guards(tool string) bool {
	switch m {
	case Developer:
		return tool == "Bash"
	case Drafter:
		return tool == "Bash" || tool == "Write" || tool == "Edit"
	}
	return false
}

// Guarded is lib/guarded-agents.txt: each agent loom's git guard checks,
// by its name in the plugin, and its mode.
func Guarded() (map[string]Mode, error) { return guarded() }

var guarded = sync.OnceValues(func() (map[string]Mode, error) { return parseGuarded(files) })

func parseGuarded(fsys fs.FS) (map[string]Mode, error) {
	b, err := fs.ReadFile(fsys, "lib/guarded-agents.txt")
	if err != nil {
		return nil, fmt.Errorf("flow: %w", err)
	}
	out := map[string]Mode{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 2 || (Mode(f[1]) != Developer && Mode(f[1]) != Drafter) {
			return nil, fmt.Errorf("flow: guarded-agents.txt:%d: want `<agent> developer|drafter`, got %q", n, line)
		}
		out[f[0]] = Mode(f[1])
	}
	return out, sc.Err()
}

// GuardedMode is the mode the hook input's agent_type gets, and whether
// the guard checks it at all. A type without probedPrefix (the main
// thread's "", another plugin's agent, an agent of the home install) is
// not flow's and passes.
func GuardedMode(agentType string) (Mode, bool) {
	name, ok := strings.CutPrefix(agentType, probedPrefix)
	if !ok || name == "" {
		return "", false
	}
	g, err := Guarded()
	if err != nil {
		return Drafter, true // a list that can't be read guards every flow agent: fail closed
	}
	mode, ok := g[name]
	return mode, ok
}
