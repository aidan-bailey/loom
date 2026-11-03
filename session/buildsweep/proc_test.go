package buildsweep

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeProc adds a process to a fake /proc at root: its comm, and its cwd
// as a symlink to cwd ("" for none).
func fakeProc(t *testing.T, root, pid, comm, cwd string) {
	t.Helper()
	dir := filepath.Join(root, pid)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "comm"), []byte(comm+"\n"), 0o644))
	if cwd != "" {
		require.NoError(t, os.Symlink(cwd, filepath.Join(dir, "cwd")))
	}
}

func TestCargoIn(t *testing.T) {
	if !supported {
		t.Skip("processes are read through /proc: Linux only")
	}
	const wt = "/work/wt"
	me := uint32(os.Getuid())
	proc := func(comm, cwd string) func(*testing.T, string) {
		return func(t *testing.T, root string) { fakeProc(t, root, "10", comm, cwd) }
	}
	for _, tc := range []struct {
		name string
		lay  func(*testing.T, string)
		uid  uint32
		busy bool
	}{
		{"cargo in the worktree", proc("cargo", "/work/wt/crates/a"), me, true},
		{"cargo at the worktree's root", proc("cargo", "/work/wt"), me, true},
		{"cargo-nextest in the worktree", proc("cargo-nextest", "/work/wt"), me, true},
		{"cargo in a sibling sharing the prefix", proc("cargo", "/work/wt2"), me, false},
		{"cargo elsewhere", proc("cargo", "/elsewhere"), me, false},
		{"another program in the worktree", proc("bash", "/work/wt"), me, false},
		{"another user's cargo", proc("cargo", "/work/wt"), me + 1, false},
		{"a pid gone mid-scan", func(t *testing.T, root string) {
			require.NoError(t, os.MkdirAll(filepath.Join(root, "10"), 0o755))
		}, me, false},
		{"cargo whose cwd can't be read", func(t *testing.T, root string) {
			fakeProc(t, root, "10", "cargo", "")
			require.NoError(t, os.WriteFile(filepath.Join(root, "10", "cwd"), nil, 0o644))
		}, me, true},
		{"not a pid", func(t *testing.T, root string) { fakeProc(t, root, "self", "cargo", "/work/wt") }, me, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			tc.lay(t, root)

			busy, detail := cargoIn(root, tc.uid, wt)

			assert.Equal(t, tc.busy, busy, detail)
			assert.Equal(t, tc.busy, detail != "", detail)
		})
	}
}
