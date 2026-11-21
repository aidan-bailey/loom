package buildsweep

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// layout is a cargo build-dir layout: v1 (deps/ and .fingerprint/ in each
// profile dir, cargo's default up to 1.99) or v2 (a dir per build unit
// under build/, the default from cargo 1.100).
type layout string

const (
	layoutV1 layout = "v1"
	layoutV2 layout = "v2"
)

// layoutEnv asks cargo for l: cargo 1.100 builds v1 only under its
// temporary opt-out, and cargo 1.99 builds v2 only as an unstable option.
// Each cargo ignores the other's switch.
var layoutEnv = map[layout][]string{
	layoutV1: {"__CARGO_TEMPORARY_BUILD_DIR_NEW_LAYOUT_OPT_OUT=1"},
	layoutV2: {"RUSTC_BOOTSTRAP=1", "CARGO_UNSTABLE_BUILD_DIR_NEW_LAYOUT=true"},
}

// eachLayout runs f once per layout.
func eachLayout(t *testing.T, f func(t *testing.T, l layout)) {
	for _, l := range []layout{layoutV1, layoutV2} {
		t.Run("layout "+string(l), func(t *testing.T) { f(t, l) })
	}
}

// crate is a one-binary crate with an integration test in a temp
// worktree, built by real cargo into the worktree's target/ in layout.
type crate struct {
	t       *testing.T
	cargo   string
	layout  layout
	wt      string
	target  string
	profile string
}

// newCrate writes the crate, skipping the test when cargo is absent (as
// in the Nix sandbox).
func newCrate(t *testing.T, l layout) *crate {
	t.Helper()
	cargo, err := exec.LookPath("cargo")
	if err != nil {
		t.Skip("cargo not installed")
	}
	wt := t.TempDir()
	c := &crate{t: t, cargo: cargo, layout: l, wt: wt, target: filepath.Join(wt, "target"), profile: filepath.Join(wt, "target", "debug")}
	c.write("Cargo.toml", "[package]\nname = \"sweepme\"\nversion = \"0.1.0\"\nedition = \"2021\"\n\n[features]\nextra = []\n")
	c.write("src/main.rs", "fn main() { println!(\"one\"); }\n")
	c.write("tests/it.rs", "#[test]\nfn it() { assert_eq!(1, 1); }\n")
	return c
}

func (c *crate) write(rel, body string) {
	c.t.Helper()
	require.NoError(c.t, os.MkdirAll(filepath.Dir(filepath.Join(c.wt, rel)), 0o755))
	require.NoError(c.t, os.WriteFile(filepath.Join(c.wt, rel), []byte(body), 0o644))
}

func (c *crate) run(args ...string) {
	c.t.Helper()
	cmd := exec.Command(c.cargo, append(args, "--offline", "-q")...)
	cmd.Dir = c.wt
	// The user's cargo config may move the target or build dir.
	cmd.Env = append(os.Environ(), "CARGO_TARGET_DIR="+c.target, "CARGO_BUILD_BUILD_DIR="+c.target)
	cmd.Env = append(cmd.Env, layoutEnv[c.layout]...)
	out, err := cmd.CombinedOutput()
	require.NoError(c.t, err, string(out))
	v1 := isDir(filepath.Join(c.profile, "deps"))
	if v1 == (c.layout == layoutV2) {
		c.t.Skipf("this cargo can't build layout %s", c.layout)
	}
}

// executables are the crate's executables below debug/ (in deps/ for v1,
// in the build units' out/ for v2), uplifted links aside, relative to
// debug/.
func (c *crate) executables() map[string]bool {
	c.t.Helper()
	out := map[string]bool{}
	for _, dir := range []string{"deps", "build"} {
		err := filepath.WalkDir(filepath.Join(c.profile, dir), func(path string, d fs.DirEntry, err error) error {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			if err != nil || d.IsDir() || strings.Contains(d.Name(), ".") {
				return err
			}
			if fi, err := d.Info(); err == nil && fi.Mode().IsRegular() && fi.Mode()&0o100 != 0 {
				rel, _ := filepath.Rel(c.profile, path)
				out[rel] = true
			}
			return nil
		})
		require.NoError(c.t, err)
	}
	return out
}

// age makes executables (relative to debug/) last used three days ago.
func (c *crate) age(names map[string]bool) {
	c.t.Helper()
	old := time.Now().Add(-3 * day)
	for n := range names {
		require.NoError(c.t, os.Chtimes(filepath.Join(c.profile, n), old, old))
	}
}

// TestSweep_RealCargoBuild sweeps a target dir cargo itself laid out, in
// each layout. A crate built with two feature sets leaves two of each
// executable; the first set, aged past the threshold, goes while the
// second stays, and cargo still tests both ways. Then the rest, aged too,
// go with the program's uplifted hard link, and cargo builds it again.
func TestSweep_RealCargoBuild(t *testing.T) { eachLayout(t, realCargoBuild) }

func realCargoBuild(t *testing.T, l layout) {
	s := fakeSystem(t, "rw")
	s.Now = time.Now
	c := newCrate(t, l)

	c.run("test", "--no-run")
	first := c.executables()
	require.Len(t, first, 3, "the program, its unit tests and the integration test")
	c.age(first)
	c.run("test", "--no-run", "--features", "extra")
	second := c.executables()
	for n := range first {
		delete(second, n)
	}
	require.Len(t, second, 3, "each feature set gets its own hashes")

	r := sweepOne(s, c.wt, 2*day, false)

	require.Empty(t, r.Skip, r.Detail)
	assert.Len(t, r.Files, 3)
	for n := range first {
		assert.NoFileExists(t, filepath.Join(c.profile, n))
	}
	for n := range second {
		assert.FileExists(t, filepath.Join(c.profile, n))
	}
	c.run("test", "--features", "extra")
	c.run("test")

	t.Run("the uplifted program goes with its binary", func(t *testing.T) {
		uplift := filepath.Join(c.target, "debug", "sweepme")
		require.FileExists(t, uplift)
		all := c.executables()
		c.age(all)

		r := sweepOne(s, c.wt, 2*day, false)

		require.Empty(t, r.Skip, r.Detail)
		assert.Len(t, r.Files, len(all))
		assert.NoFileExists(t, uplift)
		c.run("build")
		assert.FileExists(t, uplift)
	})
}

// TestSweep_RealCargoBuildIntoATargetDirThatExisted sweeps a target dir
// that held an agent's notes before cargo's first build, in each layout.
// Cargo tags only a target dir it creates, so this one has no
// CACHEDIR.TAG; the stale executables still go, and the notes stay.
func TestSweep_RealCargoBuildIntoATargetDirThatExisted(t *testing.T) {
	eachLayout(t, realCargoBuildIntoATargetDirThatExisted)
}

func realCargoBuildIntoATargetDirThatExisted(t *testing.T, l layout) {
	s := fakeSystem(t, "rw")
	s.Now = time.Now
	c := newCrate(t, l)
	c.write("target/notes.md", "an agent's scratch file\n")

	c.run("test", "--no-run")
	require.NoFileExists(t, filepath.Join(c.target, "CACHEDIR.TAG"), "cargo tags only a target dir it creates")
	all := c.executables()
	require.Len(t, all, 3)
	c.age(all)

	r := sweepOne(s, c.wt, 2*day, false)

	require.Empty(t, r.Skip, r.Detail)
	assert.Len(t, r.Files, 3)
	for n := range all {
		assert.NoFileExists(t, filepath.Join(c.profile, n))
	}
	assert.FileExists(t, filepath.Join(c.target, "notes.md"))
	c.run("test")
}
