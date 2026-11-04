package buildsweep

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// crate is a one-binary crate with an integration test in a temp
// worktree, built by real cargo into the worktree's target/.
type crate struct {
	t      *testing.T
	cargo  string
	wt     string
	target string
	deps   string
}

// newCrate writes the crate, skipping the test when cargo is absent (as
// in the Nix sandbox).
func newCrate(t *testing.T) *crate {
	t.Helper()
	cargo, err := exec.LookPath("cargo")
	if err != nil {
		t.Skip("cargo not installed")
	}
	wt := t.TempDir()
	c := &crate{t: t, cargo: cargo, wt: wt, target: filepath.Join(wt, "target"), deps: filepath.Join(wt, "target", "debug", "deps")}
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
	out, err := cmd.CombinedOutput()
	require.NoError(c.t, err, string(out))
}

// executables are the sweep's candidates in debug/deps.
func (c *crate) executables() map[string]bool {
	c.t.Helper()
	out := map[string]bool{}
	entries, err := os.ReadDir(c.deps)
	require.NoError(c.t, err)
	for _, e := range entries {
		if fi, err := e.Info(); err == nil && candidateName.MatchString(e.Name()) && fi.Mode()&0o100 != 0 {
			out[e.Name()] = true
		}
	}
	return out
}

// age makes names in debug/deps last used three days ago.
func (c *crate) age(names map[string]bool) {
	c.t.Helper()
	old := time.Now().Add(-3 * day)
	for n := range names {
		require.NoError(c.t, os.Chtimes(filepath.Join(c.deps, n), old, old))
	}
}

// TestSweep_RealCargoBuild sweeps a target dir cargo itself laid out. A
// crate built with two feature sets leaves two of each executable; the
// first set, aged past the threshold, goes while the second stays, and
// cargo still tests both ways. Then the rest, aged too, go with the
// program's uplifted hard link, and cargo builds it again.
func TestSweep_RealCargoBuild(t *testing.T) {
	s := fakeSystem(t, "rw")
	s.Now = time.Now
	c := newCrate(t)

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
		assert.NoFileExists(t, filepath.Join(c.deps, n))
	}
	for n := range second {
		assert.FileExists(t, filepath.Join(c.deps, n))
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
// that held an agent's notes before cargo's first build. Cargo tags only a
// target dir it creates, so this one has no CACHEDIR.TAG; the stale
// executables still go, and the notes stay.
func TestSweep_RealCargoBuildIntoATargetDirThatExisted(t *testing.T) {
	s := fakeSystem(t, "rw")
	s.Now = time.Now
	c := newCrate(t)
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
		assert.NoFileExists(t, filepath.Join(c.deps, n))
	}
	assert.FileExists(t, filepath.Join(c.target, "notes.md"))
	c.run("test")
}
