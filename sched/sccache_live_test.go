//go:build sccache_live

package sched

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sccacheTestPort is the private daemon's port, away from sccache's default
// (4226), so the test never talks to the host's shared daemon.
const sccacheTestPort = "42617"

// TestSccache_ClientSideCompilesInTheCallersTree pins what the hint relies on
// (sccache measurement spec §5.1): with SCCACHE_CLIENT_SIDE=1 a compile's CPU
// is in the tree of the process that ran cargo; without it, sccache's daemon
// runs the compiler and the caller's tree shows almost none. It runs a private
// daemon on a private cache, and skips where sccache or cargo is missing. Run
// with: go test -tags sccache_live -run TestSccache ./sched/
func TestSccache_ClientSideCompilesInTheCallersTree(t *testing.T) {
	sccache, err := exec.LookPath("sccache")
	if err != nil {
		t.Skip("sccache is not installed")
	}
	if _, err := exec.LookPath("cargo"); err != nil {
		t.Skip("cargo is not installed")
	}
	dir := t.TempDir()
	crate := filepath.Join(dir, "big")
	require.NoError(t, exec.Command("cargo", "new", "--lib", "--offline", crate).Run())
	var src strings.Builder
	for i := range 2000 {
		n := strconv.Itoa(i)
		src.WriteString("pub fn f" + n + "(x: u64) -> u64 { let mut a = x; for j in 0..7 { a = a.wrapping_mul(6364136223846793005).wrapping_add(j); } a ^ " + n + " }\n")
	}
	require.NoError(t, os.WriteFile(filepath.Join(crate, "src", "lib.rs"), []byte(src.String()), 0o644))

	// env is this process's environment without sccache's own settings, so
	// a SCCACHE_LOG or a server socket in the developer's shell can't change
	// the mode under test, with the private daemon's.
	env := func(extra ...string) []string {
		var out []string
		for _, e := range os.Environ() {
			if !strings.HasPrefix(e, "SCCACHE_") && !strings.HasPrefix(e, "RUSTC_WRAPPER=") {
				out = append(out, e)
			}
		}
		return append(out, append([]string{"RUSTC_WRAPPER=" + sccache, "SCCACHE_SERVER_PORT=" + sccacheTestPort,
			"SCCACHE_IDLE_TIMEOUT=60", "CARGO_INCREMENTAL=0"}, extra...)...)
	}
	t.Cleanup(func() {
		stop := exec.Command(sccache, "--stop-server")
		stop.Env = env()
		_ = stop.Run()
	})
	// treeCPU builds the crate cold in its own cache and target, and returns
	// the CPU time of the build's process tree, from the shell's times.
	treeCPU := func(mode string, extra ...string) time.Duration {
		cmd := exec.Command("sh", "-c", "cargo build -q --offline --release; times")
		cmd.Dir = crate
		cmd.Env = env(append(extra, "SCCACHE_DIR="+filepath.Join(dir, "cache-"+mode), "CARGO_TARGET_DIR="+filepath.Join(dir, "target-"+mode))...)
		out, err := cmd.Output()
		require.NoError(t, err, "%s", out)
		fields := strings.Fields(strings.TrimSpace(string(out)))
		require.GreaterOrEqual(t, len(fields), 2, "times printed nothing: %q", out)
		var total time.Duration
		for _, f := range fields[len(fields)-2:] { // the children's user and system time: 0m1.335s 0m0.417s
			m, s, ok := strings.Cut(strings.TrimSuffix(f, "s"), "m")
			require.True(t, ok, "a times field %q", f)
			mins, err := strconv.Atoi(m)
			require.NoError(t, err)
			secs, err := strconv.ParseFloat(s, 64)
			require.NoError(t, err)
			total += time.Duration(mins)*time.Minute + time.Duration(secs*float64(time.Second))
		}
		return total
	}

	server, client := treeCPU("server"), treeCPU("client", "SCCACHE_CLIENT_SIDE=1")
	assert.Less(t, server, 300*time.Millisecond, "server-side: the daemon ran the compiler")
	assert.Greater(t, client, server+200*time.Millisecond, "client-side: the compile is in the caller's tree")
}
