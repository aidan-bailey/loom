package buildsweep

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var now = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

const day = 24 * time.Hour

// fakeSystem is a Sweeper over a mount table of one line, the root mount
// with opts as its per-mount options, and an empty /proc.
func fakeSystem(t *testing.T, opts string) Sweeper {
	t.Helper()
	if !supported {
		t.Skip("the sweep checks access times through /proc: Linux only")
	}
	mi := filepath.Join(t.TempDir(), "mountinfo")
	require.NoError(t, os.WriteFile(mi, []byte("22 1 259:4 / / "+opts+" shared:1 - ext4 /dev/nvme1n1p2 rw\n"), 0o644))
	return Sweeper{MountInfo: mi, ProcRoot: t.TempDir(), UID: uint32(os.Getuid()), Now: func() time.Time { return now }}
}

// cargoTree is a worktree whose target/ is laid out as cargo leaves it:
// CACHEDIR.TAG, and debug/ holding deps/, .fingerprint/, build/,
// incremental/ and examples/. It returns the worktree and debug/.
func cargoTree(t *testing.T) (wt, debug string) {
	t.Helper()
	wt = t.TempDir()
	debug = filepath.Join(wt, "target", "debug")
	for _, d := range []string{"deps", ".fingerprint", "build", "incremental", "examples"} {
		require.NoError(t, os.MkdirAll(filepath.Join(debug, d), 0o755))
	}
	put(t, filepath.Join(wt, "target", "CACHEDIR.TAG"), 0o644, now)
	return wt, debug
}

// put writes a 4-byte file at path with mode, last accessed and modified
// at used.
func put(t *testing.T, path string, mode os.FileMode, used time.Time) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("elf!"), mode))
	require.NoError(t, os.Chmod(path, mode)) // WriteFile's mode passes through the umask
	require.NoError(t, os.Chtimes(path, used, used))
}

// sweepOne runs s over the one worktree wt.
func sweepOne(s Sweeper, wt string, after time.Duration, dryRun bool) Result {
	return s.Run([]Target{{Workspace: "ws", Title: "s", Status: "Ready", Worktree: wt}}, after, dryRun).Results[0]
}

func paths(r Result) []string {
	var out []string
	for _, f := range r.Files {
		out = append(out, f.Path)
	}
	return out
}

func TestSweep_DeletesOnlyExecutablesUnusedPastTheThreshold(t *testing.T) {
	s := fakeSystem(t, "rw")
	wt, debug := cargoTree(t)
	old := now.Add(-3 * day)
	for _, f := range []struct {
		rel  string
		mode os.FileMode
		used time.Time
		gone bool
	}{
		{"deps/health-0123456789abcdef", 0o755, old, true},                   // a stale test binary
		{"examples/demo-0123456789abcdef", 0o755, old, true},                 // a stale example
		{"deps/live-0123456789abcdef", 0o755, now.Add(-time.Hour), false},    // used an hour ago
		{"deps/libderive-0123456789abcdef.so", 0o755, old, false},            // a proc-macro: it has an extension
		{"deps/libcore-0123456789abcdef.rlib", 0o644, old, false},            // a library
		{"deps/plain-0123456789abcdef", 0o644, old, false},                   // no exec bit
		{"deps/health", 0o755, old, false},                                   // no hash
		{"build/foo-0123456789abcdef/build-script-build", 0o755, old, false}, // a build script
		{"incremental/foo-0123456789abcdef/s-abc", 0o755, old, false},        // the incremental cache
	} {
		put(t, filepath.Join(debug, f.rel), f.mode, f.used)
		defer func() {
			if f.gone {
				assert.NoFileExists(t, filepath.Join(debug, f.rel))
			} else {
				assert.FileExists(t, filepath.Join(debug, f.rel))
			}
		}()
	}

	r := sweepOne(s, wt, 2*day, false)

	require.Empty(t, r.Skip, r.Detail)
	assert.ElementsMatch(t, []string{"target/debug/deps/health-0123456789abcdef", "target/debug/examples/demo-0123456789abcdef"}, paths(r))
	assert.Equal(t, int64(8), r.Freed)
	assert.Empty(t, r.Errors)
}

func TestSweep_TheThresholdIsExclusive(t *testing.T) {
	s := fakeSystem(t, "rw")
	wt, debug := cargoTree(t)
	put(t, filepath.Join(debug, "deps/edge-0123456789abcdef"), 0o755, now.Add(-2*day))
	put(t, filepath.Join(debug, "deps/past-0123456789abcdef"), 0o755, now.Add(-2*day-time.Second))

	r := sweepOne(s, wt, 2*day, false)

	assert.Equal(t, []string{"target/debug/deps/past-0123456789abcdef"}, paths(r))
}

func TestSweep_AnUpliftedNameGoesWithItsBinary(t *testing.T) {
	s := fakeSystem(t, "rw")
	wt, debug := cargoTree(t)
	bin := filepath.Join(debug, "deps/app-0123456789abcdef")
	put(t, bin, 0o755, now.Add(-3*day))
	require.NoError(t, os.Link(bin, filepath.Join(debug, "app")))

	r := sweepOne(s, wt, 2*day, false)

	assert.Equal(t, []string{"target/debug/deps/app-0123456789abcdef"}, paths(r))
	assert.Equal(t, int64(4), r.Freed, "one inode's bytes, once")
	assert.NoFileExists(t, bin)
	assert.NoFileExists(t, filepath.Join(debug, "app"))
}

func TestSweep_ALinkOutsideTheProfileDirKeepsTheFile(t *testing.T) {
	s := fakeSystem(t, "rw")
	wt, debug := cargoTree(t)
	bin := filepath.Join(debug, "deps/app-0123456789abcdef")
	put(t, bin, 0o755, now.Add(-3*day))
	require.NoError(t, os.Link(bin, filepath.Join(wt, "app-copy")))

	r := sweepOne(s, wt, 2*day, false)

	assert.Empty(t, r.Files)
	assert.FileExists(t, bin)
}

func TestSweep_FindsEveryProfileDirAndFollowsNoSymlink(t *testing.T) {
	s := fakeSystem(t, "rw")
	wt, _ := cargoTree(t)
	target := filepath.Join(wt, "target")
	profile := func(dir string) string {
		for _, d := range []string{"deps", ".fingerprint"} {
			require.NoError(t, os.MkdirAll(filepath.Join(dir, d), 0o755))
		}
		bin := filepath.Join(dir, "deps/t-0123456789abcdef")
		put(t, bin, 0o755, now.Add(-3*day))
		return bin
	}
	triple := profile(filepath.Join(target, "x86_64-unknown-linux-gnu", "debug"))
	nested := profile(filepath.Join(target, "semver-checks", "git-v0_40_0", "target", "debug"))
	outsideDir := filepath.Join(t.TempDir(), "debug")
	outside := profile(outsideDir)
	require.NoError(t, os.Symlink(outsideDir, filepath.Join(target, "linked")))

	sweepOne(s, wt, 2*day, false)

	assert.NoFileExists(t, triple)
	assert.NoFileExists(t, nested)
	assert.FileExists(t, outside, "reached only through a symlink")
}

// TestSweep_NeedsCargosMarkOnTheTargetDir: cargo tags a target dir it
// creates with CACHEDIR.TAG, and one that already existed (an agent's
// notes written there first) only gets its .rustc_info.json.
func TestSweep_NeedsCargosMarkOnTheTargetDir(t *testing.T) {
	for _, tc := range []struct {
		name  string
		marks []string
		swept bool
	}{
		{"CACHEDIR.TAG", []string{"CACHEDIR.TAG"}, true},
		{".rustc_info.json", []string{".rustc_info.json"}, true},
		{"neither", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := fakeSystem(t, "rw")
			wt, debug := cargoTree(t)
			target := filepath.Join(wt, "target")
			require.NoError(t, os.Remove(filepath.Join(target, "CACHEDIR.TAG")))
			for _, m := range tc.marks {
				put(t, filepath.Join(target, m), 0o644, now)
			}
			bin := filepath.Join(debug, "deps/app-0123456789abcdef")
			put(t, bin, 0o755, now.Add(-3*day))

			r := sweepOne(s, wt, 2*day, false)

			if tc.swept {
				require.Empty(t, r.Skip, r.Detail)
				assert.NoFileExists(t, bin)
			} else {
				assert.Equal(t, SkipNoTarget, r.Skip)
				assert.FileExists(t, bin)
			}
		})
	}
}

func TestSweep_AMarkThatIsADirOrALinkIsNoMark(t *testing.T) {
	for _, mark := range []string{"CACHEDIR.TAG", ".rustc_info.json"} {
		t.Run(mark, func(t *testing.T) {
			s := fakeSystem(t, "rw")
			wt, debug := cargoTree(t)
			target := filepath.Join(wt, "target")
			require.NoError(t, os.Remove(filepath.Join(target, "CACHEDIR.TAG")))
			real := filepath.Join(t.TempDir(), mark)
			put(t, real, 0o644, now)
			require.NoError(t, os.Symlink(real, filepath.Join(target, mark)))
			bin := filepath.Join(debug, "deps/app-0123456789abcdef")
			put(t, bin, 0o755, now.Add(-3*day))

			r := sweepOne(s, wt, 2*day, false)

			assert.Equal(t, SkipNoTarget, r.Skip)
			assert.FileExists(t, bin)
		})
	}
}

// TestSweep_AWorktreeThatIsGoneHasNoTarget: a session paused before pause
// kept its worktree has none on disk, which is nothing to sweep, not an
// error. A worktree path that can't be resolved for another reason still
// is one.
func TestSweep_AWorktreeThatIsGoneHasNoTarget(t *testing.T) {
	s := fakeSystem(t, "rw")
	dir := t.TempDir()

	gone := sweepOne(s, filepath.Join(dir, "gone"), 2*day, false)

	assert.Equal(t, SkipNoTarget, gone.Skip)
	assert.Equal(t, "no worktree", gone.Detail)

	loop := filepath.Join(dir, "loop")
	require.NoError(t, os.Symlink(loop, loop))

	unreadable := sweepOne(s, loop, 2*day, false)

	assert.Equal(t, SkipError, unreadable.Skip)
}

func TestSweep_ADryRunDeletesNothing(t *testing.T) {
	s := fakeSystem(t, "rw")
	wt, debug := cargoTree(t)
	bin := filepath.Join(debug, "deps/app-0123456789abcdef")
	put(t, bin, 0o755, now.Add(-3*day))

	rep := s.Run([]Target{{Title: "s", Worktree: wt}}, 2*day, true)

	assert.True(t, rep.DryRun)
	assert.Equal(t, 48, rep.AfterHours)
	assert.Equal(t, []string{"target/debug/deps/app-0123456789abcdef"}, paths(rep.Results[0]))
	assert.Equal(t, int64(4), rep.Results[0].Freed)
	assert.FileExists(t, bin)
}

func TestSweep_SweepsOnlyWhereAccessTimesAreRecorded(t *testing.T) {
	for _, tc := range []struct {
		name  string
		opts  string
		after time.Duration
		swept bool
	}{
		{"strictatime", "rw,lazytime", 2 * time.Hour, true},
		{"noatime", "rw,noatime", 2 * day, false},
		{"relatime under a day", "rw,relatime", 12 * time.Hour, false},
		{"relatime at a day", "rw,relatime", day, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := fakeSystem(t, tc.opts)
			wt, debug := cargoTree(t)
			bin := filepath.Join(debug, "deps/app-0123456789abcdef")
			put(t, bin, 0o755, now.Add(-3*day))

			r := sweepOne(s, wt, tc.after, false)

			if tc.swept {
				assert.Empty(t, r.Skip, r.Detail)
				assert.NoFileExists(t, bin)
				return
			}
			assert.Equal(t, SkipAtimeOff, r.Skip)
			assert.Contains(t, r.Detail, "/ is mounted")
			assert.FileExists(t, bin)
		})
	}
}

func TestSweep_LooksUpTheMountOfTheResolvedWorktree(t *testing.T) {
	s := fakeSystem(t, "rw")
	wt, debug := cargoTree(t)
	bin := filepath.Join(debug, "deps/app-0123456789abcdef")
	put(t, bin, 0o755, now.Add(-3*day))
	links := t.TempDir()
	alias := filepath.Join(links, "wt")
	require.NoError(t, os.Symlink(wt, alias))
	// The alias's own dir is mounted noatime; the worktree it names is not.
	f, err := os.OpenFile(s.MountInfo, os.O_APPEND|os.O_WRONLY, 0)
	require.NoError(t, err)
	_, err = f.WriteString("90 22 0:40 / " + links + " rw,noatime shared:2 - tmpfs tmpfs rw\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	r := sweepOne(s, alias, 2*day, false)

	assert.Empty(t, r.Skip, r.Detail)
	assert.NoFileExists(t, bin)
}

func TestSweep_SkipsWhileCargoHoldsALock(t *testing.T) {
	s := fakeSystem(t, "rw")
	wt, debug := cargoTree(t)
	bin := filepath.Join(debug, "deps/app-0123456789abcdef")
	put(t, bin, 0o755, now.Add(-3*day))
	put(t, filepath.Join(debug, ".cargo-build-lock"), 0o644, now)
	cargo, held, err := tryLock(filepath.Join(debug, ".cargo-build-lock"))
	require.NoError(t, err)
	require.False(t, held)

	r := sweepOne(s, wt, 2*day, false)

	assert.Equal(t, SkipBuilding, r.Skip)
	assert.Contains(t, r.Detail, "target/debug/.cargo-build-lock")
	assert.FileExists(t, bin)

	require.NoError(t, cargo.Close())
	r = sweepOne(s, wt, 2*day, false)
	assert.Empty(t, r.Skip, r.Detail)
	assert.NoFileExists(t, bin)
}

func TestSweep_SkipsWhileCargoRunsInTheWorktree(t *testing.T) {
	s := fakeSystem(t, "rw")
	wt, debug := cargoTree(t)
	bin := filepath.Join(debug, "deps/app-0123456789abcdef")
	put(t, bin, 0o755, now.Add(-3*day))
	resolved, err := filepath.EvalSymlinks(wt)
	require.NoError(t, err)
	fakeProc(t, s.ProcRoot, "4242", "cargo", filepath.Join(resolved, "crates", "a"))

	r := sweepOne(s, wt, 2*day, false)

	assert.Equal(t, SkipTesting, r.Skip)
	assert.Contains(t, r.Detail, "pid 4242")
	assert.FileExists(t, bin)
}

func TestRun_ReportsASkippedTargetWithoutReadingIt(t *testing.T) {
	s := fakeSystem(t, "rw")

	rep := s.Run([]Target{{Title: "busy", Status: "Running", Worktree: "/nonexistent/wt", Skip: SkipStatus, Detail: "the agent is Running"}}, day, false)

	require.Len(t, rep.Results, 1)
	assert.Equal(t, SkipStatus, rep.Results[0].Skip)
	assert.Equal(t, "the agent is Running", rep.Results[0].Detail)
}
