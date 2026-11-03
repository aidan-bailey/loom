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

// TestSweep_RealCargoBuild sweeps a target dir cargo itself laid out. A
// crate built with two feature sets leaves two of each executable; the
// first set, aged past the threshold, goes while the second stays, and
// cargo still tests both ways. Then the rest, aged too, go with the
// program's uplifted hard link, and cargo builds it again.
func TestSweep_RealCargoBuild(t *testing.T) {
	s := fakeSystem(t, "rw")
	s.Now = time.Now
	cargo, err := exec.LookPath("cargo")
	if err != nil {
		t.Skip("cargo not installed")
	}
	wt := t.TempDir()
	target := filepath.Join(wt, "target")
	write := func(rel, body string) {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(wt, rel)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(wt, rel), []byte(body), 0o644))
	}
	write("Cargo.toml", "[package]\nname = \"sweepme\"\nversion = \"0.1.0\"\nedition = \"2021\"\n\n[features]\nextra = []\n")
	write("src/main.rs", "fn main() { println!(\"one\"); }\n")
	write("tests/it.rs", "#[test]\nfn it() { assert_eq!(1, 1); }\n")
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(cargo, append(args, "--offline", "-q")...)
		cmd.Dir = wt
		// The user's cargo config may move the target or build dir.
		cmd.Env = append(os.Environ(), "CARGO_TARGET_DIR="+target, "CARGO_BUILD_BUILD_DIR="+target)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}
	deps := filepath.Join(target, "debug", "deps")
	executables := func() map[string]bool {
		out := map[string]bool{}
		entries, err := os.ReadDir(deps)
		require.NoError(t, err)
		for _, e := range entries {
			if fi, err := e.Info(); err == nil && candidateName.MatchString(e.Name()) && fi.Mode()&0o100 != 0 {
				out[e.Name()] = true
			}
		}
		return out
	}
	age := func(names map[string]bool) {
		old := time.Now().Add(-3 * day)
		for n := range names {
			require.NoError(t, os.Chtimes(filepath.Join(deps, n), old, old))
		}
	}

	run("test", "--no-run")
	first := executables()
	require.Len(t, first, 3, "the program, its unit tests and the integration test")
	age(first)
	run("test", "--no-run", "--features", "extra")
	second := executables()
	for n := range first {
		delete(second, n)
	}
	require.Len(t, second, 3, "each feature set gets its own hashes")

	r := sweepOne(s, wt, 2*day, false)

	require.Empty(t, r.Skip, r.Detail)
	assert.Len(t, r.Files, 3)
	for n := range first {
		assert.NoFileExists(t, filepath.Join(deps, n))
	}
	for n := range second {
		assert.FileExists(t, filepath.Join(deps, n))
	}
	run("test", "--features", "extra")
	run("test")

	t.Run("the uplifted program goes with its binary", func(t *testing.T) {
		uplift := filepath.Join(target, "debug", "sweepme")
		require.FileExists(t, uplift)
		all := executables()
		age(all)

		r := sweepOne(s, wt, 2*day, false)

		require.Empty(t, r.Skip, r.Detail)
		assert.Len(t, r.Files, len(all))
		assert.NoFileExists(t, uplift)
		run("build")
		assert.FileExists(t, uplift)
	})
}
