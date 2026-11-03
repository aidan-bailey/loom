# Build Sweep Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task (the user chose inline execution). Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The daemon deletes the Rust executables in idle and paused sessions' cargo target dirs that have gone unused for `build_sweep_after_hours`, and `loom sweep [--dry-run]` runs the same sweep on demand.

**Architecture:** A filesystem-only package, `session/buildsweep`, sweeps one worktree at a time. It fails closed on the mount's access-time mode, cargo's `flock`s and any `cargo` process in the worktree. The model (`core`) chooses the sessions and runs the package as a gated background job every 30 minutes, and through a new `Core` request, `SweepBuilds`, whose `Reply` carries the report. The CLI command `loom sweep` sends that request and prints the report.

**Tech Stack:** Go (the version in `go.mod`), testify, cobra, `syscall.Flock`, `/proc/self/mountinfo`, `/proc/<pid>/{comm,cwd}`.

**Spec:** `docs/superpowers/specs/2026-11-03-build-sweep-design.md`. Its spike (§9) is done.

**Ground rules for every package:**
- Run Go with `CGO_ENABLED=0`.
- Format only the files you touched: `gofmt -w <files>`, never `gofmt -w .`.
- Commit with explicit pathspecs, never `-a`, `git add -A` or `.`.
- Keep scratch files out of the worktree.
- Never run loom itself in a pane.

---

## Work package 1: `session/buildsweep`

**Files:**
- Create: `session/buildsweep/report.go`, `session/buildsweep/mountinfo.go`, `session/buildsweep/proc.go`, `session/buildsweep/sweep.go`, `session/buildsweep/stat_linux.go`, `session/buildsweep/stat_other.go`, `session/buildsweep/lock_unix.go`, `session/buildsweep/lock_windows.go`
- Test: `session/buildsweep/testmain_test.go`, `session/buildsweep/mountinfo_test.go`, `session/buildsweep/proc_test.go`, `session/buildsweep/sweep_test.go`, `session/buildsweep/cargo_test.go`

- [ ] **Step 1: Write the report types** (`session/buildsweep/report.go`)

```go
// Package buildsweep deletes the Rust executables in session worktrees'
// target dirs that have gone unused for a while: cargo writes a new test
// binary under a new hash for each feature set and profile, and never
// deletes the old one. It reads only the filesystem, /proc and the mount
// table; the model (core) decides which sessions to give it.
package buildsweep

import "time"

// Options are a sweep request's settings. DryRun reports what would go
// and deletes nothing; AfterHours, when not 0, replaces the
// build_sweep_after_hours setting for this sweep.
type Options struct {
	DryRun     bool
	AfterHours int
}

// Skip names why a session's worktree was not swept; "" when it was.
type Skip string

// The reasons a worktree is skipped, as the report and the log name them.
const (
	// SkipStatus: the session's agent is not idle (the model's call).
	SkipStatus Skip = "status"
	// SkipNoTarget: the worktree has no cargo target dir.
	SkipNoTarget Skip = "no_target"
	// SkipAtimeOff: its filesystem records no access times, or too
	// coarsely for the threshold, or they can't be checked.
	SkipAtimeOff Skip = "atime_off"
	// SkipBuilding: cargo holds one of the target dir's locks.
	SkipBuilding Skip = "building"
	// SkipTesting: a cargo process runs in the worktree.
	SkipTesting Skip = "testing"
	// SkipError: the worktree or its target dir could not be read.
	SkipError Skip = "error"
)

// Target is one session's worktree, named as the report shows it. One
// whose Skip is set is reported as given and its disk never read.
type Target struct {
	Workspace string
	Title     string
	Status    string
	Worktree  string
	Skip      Skip
	Detail    string
}

// File is one executable the sweep deleted, or would in a dry run, with
// the time it was last used (its access or modification time, the later).
type File struct {
	Path     string // relative to the worktree
	Size     int64
	LastUsed time.Time
}

// Result is one session's sweep: its Target, with Skip and Detail set
// when it was skipped, and otherwise the files that went, the bytes they
// held and the deletes that failed.
type Result struct {
	Target
	Files  []File
	Freed  int64
	Errors []string
}

// Report is one sweep over every target it was given.
type Report struct {
	DryRun     bool
	AfterHours int
	Results    []Result
}
```

- [ ] **Step 2: Write the platform files**

`session/buildsweep/stat_linux.go`:

```go
package buildsweep

import (
	"fmt"
	"os"
	"syscall"
	"time"
)

// supported is whether this OS lets the sweep check access times and
// cargo's processes: Linux, through /proc.
const supported = true

// fileUse is fi's last use (its access or modification time, the later),
// inode and link count; ok is false when fi carries no stat data.
func fileUse(fi os.FileInfo) (lastUsed time.Time, ino, nlink uint64, ok bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return time.Time{}, 0, 0, false
	}
	lastUsed = time.Unix(st.Atim.Unix())
	if mt := fi.ModTime(); mt.After(lastUsed) {
		lastUsed = mt
	}
	return lastUsed, st.Ino, uint64(st.Nlink), true
}

// procOwner is the uid owning path, a /proc/<pid> dir: the process's.
func procOwner(path string) (uint32, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("%s: no owner", path)
	}
	return st.Uid, nil
}
```

`session/buildsweep/stat_other.go`:

```go
//go:build !linux

package buildsweep

import (
	"errors"
	"os"
	"time"
)

// supported is false: access times and cargo's processes are checked
// through /proc, which only Linux has, so every worktree is skipped.
const supported = false

func fileUse(os.FileInfo) (time.Time, uint64, uint64, bool) { return time.Time{}, 0, 0, false }

func procOwner(string) (uint32, error) { return 0, errors.New("no /proc on this OS") }
```

`session/buildsweep/lock_unix.go`:

```go
//go:build unix

package buildsweep

import (
	"errors"
	"os"
	"syscall"
)

// tryLock takes an exclusive flock on path without waiting, the kind of
// lock cargo takes (flock(2)); held is true when another holder has it.
// Closing the returned file releases the lock.
func tryLock(path string) (f *os.File, held bool, err error) {
	f, err = os.Open(path)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, true, nil
		}
		return nil, false, err
	}
	return f, false, nil
}
```

`session/buildsweep/lock_windows.go`:

```go
package buildsweep

import (
	"errors"
	"os"
)

// tryLock takes no lock on Windows, where the sweep never gets this far
// (supported is false).
func tryLock(string) (*os.File, bool, error) {
	return nil, false, errors.New("cargo's locks are not taken on windows")
}
```

- [ ] **Step 3: Write the failing mountinfo tests** (`session/buildsweep/testmain_test.go`, `session/buildsweep/mountinfo_test.go`)

```go
package buildsweep

import (
	"os"
	"testing"

	"github.com/aidan-bailey/loom/log"
)

func TestMain(m *testing.M) {
	_ = log.Initialize("", false)
	code := m.Run()
	log.Close()
	os.Exit(code)
}
```

```go
package buildsweep

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseMountInfo(t *testing.T) {
	data := `22 1 259:4 / / rw,noatime shared:1 - ext4 /dev/nvme1n1p2 rw,commit=60
84 22 259:1 / /tb rw,relatime shared:5 - ext4 /dev/nvme0n1p1 rw
44 22 0:28 / /run rw,nosuid,nodev shared:2 - tmpfs tmpfs rw,mode=755
91 22 0:50 / /mnt/My\040Disk rw,lazytime - ext4 /dev/sdb1 rw
`
	mounts, err := parseMountInfo(data)

	require.NoError(t, err)
	assert.Equal(t, []mount{
		{point: "/", atime: atimeOff},
		{point: "/tb", atime: atimeRelative},
		{point: "/run", atime: atimeStrict},
		{point: "/mnt/My Disk", atime: atimeStrict},
	}, mounts)

	_, err = parseMountInfo("22 1 259:4 /\n")
	assert.Error(t, err, "a short line")
}

func TestMountFor_TheDeepestMountWinsAndTheLaterOverTheSamePoint(t *testing.T) {
	mounts := []mount{
		{point: "/", atime: atimeOff},
		{point: "/tb", atime: atimeRelative},
		{point: "/tb", atime: atimeStrict}, // mounted over the one before
		{point: "/tbx", atime: atimeOff},
	}
	for path, want := range map[string]mount{
		"/home/me/wt":   {point: "/", atime: atimeOff},
		"/tb":           {point: "/tb", atime: atimeStrict},
		"/tb/Source/wt": {point: "/tb", atime: atimeStrict},
		"/tbx/wt":       {point: "/tbx", atime: atimeOff},
	} {
		got, ok := mountFor(mounts, path)
		require.True(t, ok, path)
		assert.Equal(t, want, got, path)
	}
	_, ok := mountFor(nil, "/tb")
	assert.False(t, ok)
}

func TestRecordsUse(t *testing.T) {
	for _, tc := range []struct {
		m     mount
		after time.Duration
		ok    bool
	}{
		{mount{point: "/", atime: atimeStrict}, time.Hour, true},
		{mount{point: "/", atime: atimeRelative}, 24 * time.Hour, true},
		{mount{point: "/", atime: atimeRelative}, 23 * time.Hour, false},
		{mount{point: "/", atime: atimeOff}, 1000 * time.Hour, false},
	} {
		ok, why := recordsUse(tc.m, tc.after)
		assert.Equal(t, tc.ok, ok, "%+v %v", tc.m, tc.after)
		assert.Equal(t, tc.ok, why == "", why)
	}
}
```

- [ ] **Step 4: Run them and see them fail**

Run: `CGO_ENABLED=0 go test ./session/buildsweep/ -run 'TestParseMountInfo|TestMountFor|TestRecordsUse'`
Expected: FAIL, build errors (`undefined: parseMountInfo`, `mount`, `mountFor`, `recordsUse`).

- [ ] **Step 5: Write `session/buildsweep/mountinfo.go`**

```go
package buildsweep

import (
	"fmt"
	"strings"
	"time"
)

// atimeMode is how a mount records access times.
type atimeMode int

const (
	// atimeStrict records every access: a mount whose options name no
	// atime flag (strictatime).
	atimeStrict atimeMode = iota
	// atimeRelative records an access at most once a day (relatime).
	atimeRelative
	// atimeOff records none (noatime).
	atimeOff
)

// mount is one line of the mount table: where it is mounted and how it
// records access times.
type mount struct {
	point string
	atime atimeMode
}

// parseMountInfo reads /proc/self/mountinfo (proc(5)): the mount point is
// each line's fifth field and the per-mount options its sixth, where the
// kernel reports noatime and relatime.
func parseMountInfo(data string) ([]mount, error) {
	var out []mount
	for _, line := range strings.Split(data, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if len(f) < 6 {
			return nil, fmt.Errorf("mountinfo: short line %q", line)
		}
		m := mount{point: unescapeMount(f[4])}
		for _, opt := range strings.Split(f[5], ",") {
			switch {
			case opt == "noatime":
				m.atime = atimeOff
			case opt == "relatime" && m.atime != atimeOff:
				m.atime = atimeRelative
			}
		}
		out = append(out, m)
	}
	return out, nil
}

// unescapeMount decodes the octal escapes (\040 for a space) the kernel
// writes in a mount point.
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) && isOctal(s[i+1]) && isOctal(s[i+2]) && isOctal(s[i+3]) {
			if v := int(s[i+1]-'0')<<6 | int(s[i+2]-'0')<<3 | int(s[i+3]-'0'); v <= 0xff {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isOctal(c byte) bool { return c >= '0' && c <= '7' }

// mountFor is the mount holding path: the one with the longest mount
// point at or above it, and of two at one point the later line, which
// was mounted over the other.
func mountFor(mounts []mount, path string) (mount, bool) {
	var best mount
	found := false
	for _, m := range mounts {
		if within(m.point, path) && (!found || len(m.point) >= len(best.point)) {
			best, found = m, true
		}
	}
	return best, found
}

// within reports whether p is dir or lies beneath it; both are clean,
// absolute paths.
func within(dir, p string) bool {
	if dir == "/" {
		return strings.HasPrefix(p, "/")
	}
	return p == dir || strings.HasPrefix(p, dir+"/")
}

// recordsUse reports whether m records access times finely enough to tell
// a file unused for after, and if not why, for the report.
func recordsUse(m mount, after time.Duration) (bool, string) {
	switch m.atime {
	case atimeOff:
		return false, m.point + " is mounted noatime, so it records no access times"
	case atimeRelative:
		if after < 24*time.Hour {
			return false, m.point + " is mounted relatime, which records an access at most once a day: too coarse for a threshold under 24h"
		}
	}
	return true, ""
}
```

- [ ] **Step 6: Run the mountinfo tests and see them pass**

Run: `CGO_ENABLED=0 go test ./session/buildsweep/ -run 'TestParseMountInfo|TestMountFor|TestRecordsUse'`
Expected: PASS.

- [ ] **Step 7: Write the failing process-scan test** (`session/buildsweep/proc_test.go`)

```go
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
```

- [ ] **Step 8: Run it and see it fail**

Run: `CGO_ENABLED=0 go test ./session/buildsweep/ -run TestCargoIn`
Expected: FAIL, `undefined: cargoIn`.

- [ ] **Step 9: Write `session/buildsweep/proc.go`**

```go
package buildsweep

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// cargoIn reports whether a process of uid named cargo or cargo-* (its
// /proc/<pid>/comm) has its working directory inside dir, reading
// procRoot as /proc; detail says which, or what could not be read. A pid
// that vanishes mid-scan is passed over; one of uid's own whose working
// directory can't be read counts as busy, since it might be a test run.
func cargoIn(procRoot string, uid uint32, dir string) (busy bool, detail string) {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return true, "can't list processes: " + err.Error()
	}
	for _, e := range entries {
		if !isPID(e.Name()) {
			continue
		}
		p := filepath.Join(procRoot, e.Name())
		if owner, err := procOwner(p); err != nil || owner != uid {
			continue // gone, or another user's
		}
		comm, err := os.ReadFile(filepath.Join(p, "comm"))
		if err != nil {
			continue // gone
		}
		name := strings.TrimSpace(string(comm))
		if name != "cargo" && !strings.HasPrefix(name, "cargo-") {
			continue
		}
		cwd, err := os.Readlink(filepath.Join(p, "cwd"))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return true, fmt.Sprintf("%s (pid %s): its working directory can't be read: %v", name, e.Name(), err)
		case within(dir, cwd):
			return true, fmt.Sprintf("%s (pid %s) runs in the worktree", name, e.Name())
		}
	}
	return false, ""
}

// isPID reports whether name, an entry of /proc, is a process's.
func isPID(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
```

- [ ] **Step 10: Run it and see it pass**

Run: `CGO_ENABLED=0 go test ./session/buildsweep/ -run TestCargoIn`
Expected: PASS.

- [ ] **Step 11: Write the failing sweep tests** (`session/buildsweep/sweep_test.go`)

```go
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

func TestSweep_NeedsCargosCachedirTag(t *testing.T) {
	s := fakeSystem(t, "rw")
	wt, debug := cargoTree(t)
	require.NoError(t, os.Remove(filepath.Join(wt, "target", "CACHEDIR.TAG")))
	bin := filepath.Join(debug, "deps/app-0123456789abcdef")
	put(t, bin, 0o755, now.Add(-3*day))

	r := sweepOne(s, wt, 2*day, false)

	assert.Equal(t, SkipNoTarget, r.Skip)
	assert.FileExists(t, bin)
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
```

- [ ] **Step 12: Run them and see them fail**

Run: `CGO_ENABLED=0 go test ./session/buildsweep/`
Expected: FAIL, build errors (`undefined: Sweeper`).

- [ ] **Step 13: Write `session/buildsweep/sweep.go`**

```go
package buildsweep

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sync"
	"time"

	"github.com/aidan-bailey/loom/log"
)

// maxDepth bounds the walk for profile dirs below a target dir: deep
// enough for target/<triple>/<profile> and the nested target dirs tools
// such as cargo-semver-checks make.
const maxDepth = 6

// lockNames are the lock files cargo flocks in a profile dir while it
// builds (cargo 1.99: the build and artifact locks exclusive, .cargo-lock
// shared). None is held while a `cargo test` runs its tests.
var lockNames = []string{".cargo-lock", ".cargo-build-lock", ".cargo-artifact-lock"}

// skipDirs are a profile dir's children the walk never enters.
var skipDirs = map[string]bool{"deps": true, "build": true, "incremental": true, ".fingerprint": true, "examples": true}

// candidateName is a cargo executable's name in deps/ or examples/: the
// crate or target name, a dash and a 16-digit hash, no extension.
var candidateName = regexp.MustCompile(`^[A-Za-z0-9_]+-[0-9a-f]{16}$`)

// sweepMu keeps one sweep at a time in the process: the daemon's periodic
// one and a `loom sweep` request.
var sweepMu sync.Mutex

// Sweeper sweeps worktrees. Default reads the live system; tests point
// MountInfo and ProcRoot at fakes and fix Now.
type Sweeper struct {
	MountInfo string
	ProcRoot  string
	UID       uint32
	Now       func() time.Time
}

// Default is the Sweeper of the running process.
func Default() Sweeper {
	return Sweeper{MountInfo: "/proc/self/mountinfo", ProcRoot: "/proc", UID: uint32(os.Getuid()), Now: time.Now}
}

// Run sweeps each target in turn and logs each result: it deletes the
// executables unused for after, or with dryRun only reports them.
func (s Sweeper) Run(targets []Target, after time.Duration, dryRun bool) Report {
	sweepMu.Lock()
	defer sweepMu.Unlock()
	rep := Report{DryRun: dryRun, AfterHours: int(after / time.Hour)}
	var mounts []mount
	var mountErr error
	if supported {
		var data []byte
		if data, mountErr = os.ReadFile(s.MountInfo); mountErr == nil {
			mounts, mountErr = parseMountInfo(string(data))
		}
	}
	for _, t := range targets {
		r := Result{Target: t}
		if t.Skip == "" {
			r = s.sweep(t, after, dryRun, mounts, mountErr)
		}
		logResult(r, dryRun)
		rep.Results = append(rep.Results, r)
	}
	return rep
}

// sweep sweeps one target, failing closed: any doubt about its target
// dir, its filesystem's access times, or a cargo at work in it skips it.
func (s Sweeper) sweep(t Target, after time.Duration, dryRun bool, mounts []mount, mountErr error) Result {
	r := Result{Target: t}
	skip := func(why Skip, detail string) Result {
		r.Skip, r.Detail = why, detail
		return r
	}
	wt, err := filepath.EvalSymlinks(t.Worktree)
	if err != nil {
		return skip(SkipError, err.Error())
	}
	target := filepath.Join(wt, "target")
	if !isDir(target) || !isRegular(filepath.Join(target, "CACHEDIR.TAG")) {
		return skip(SkipNoTarget, "no cargo target dir")
	}
	if !supported {
		return skip(SkipAtimeOff, "access times are not checked on "+runtime.GOOS)
	}
	if mountErr != nil {
		return skip(SkipAtimeOff, "the mount table can't be read: "+mountErr.Error())
	}
	m, ok := mountFor(mounts, target)
	if !ok {
		return skip(SkipAtimeOff, "no mount holds "+target)
	}
	if ok, why := recordsUse(m, after); !ok {
		return skip(SkipAtimeOff, why)
	}
	profiles, err := profileDirs(target)
	if err != nil {
		return skip(SkipError, err.Error())
	}
	locks, held, err := lockAll(profiles)
	defer unlockAll(locks)
	switch {
	case err != nil:
		return skip(SkipError, err.Error())
	case held != "":
		return skip(SkipBuilding, relTo(wt, held)+" is locked: cargo is building")
	}
	// Checked under the locks: a cargo that starts from here on waits on
	// them, and one already running its tests shows here.
	if busy, detail := cargoIn(s.ProcRoot, s.UID, wt); busy {
		return skip(SkipTesting, detail)
	}
	cutoff := s.Now().Add(-after)
	for _, p := range profiles {
		sweepProfile(&r, wt, p, cutoff, dryRun)
	}
	return r
}

// profileDirs are the dirs under target, up to maxDepth levels down, that
// hold both deps/ and .fingerprint/: cargo's profile dirs. The walk
// follows no symlink and stops at each profile dir.
func profileDirs(target string) ([]string, error) {
	var out []string
	var walk func(dir string, depth int) error
	walk = func(dir string, depth int) error {
		if isDir(filepath.Join(dir, "deps")) && isDir(filepath.Join(dir, ".fingerprint")) {
			out = append(out, dir)
			return nil
		}
		if depth == maxDepth {
			return nil
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if !e.IsDir() || skipDirs[e.Name()] {
				continue
			}
			if err := walk(filepath.Join(dir, e.Name()), depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	return out, walk(target, 0)
}

// lockAll takes every cargo lock file present in each profile dir; held
// names the first one another process holds, and the locks taken so far
// come back to be released either way.
func lockAll(profiles []string) (locks []*os.File, held string, err error) {
	for _, p := range profiles {
		for _, name := range lockNames {
			path := filepath.Join(p, name)
			if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
				continue
			}
			f, isHeld, err := tryLock(path)
			if err != nil {
				return locks, "", err
			}
			if isHeld {
				return locks, path, nil
			}
			locks = append(locks, f)
		}
	}
	return locks, "", nil
}

func unlockAll(locks []*os.File) {
	for _, f := range locks {
		_ = f.Close()
	}
}

// sweepProfile deletes, or with dryRun only lists, the executables in
// profile's deps/ and examples/ last used before cutoff, each with its
// other names in profile and examples/ (cargo's uplifted hard links). A
// file linked from anywhere else is kept: deleting it would free nothing.
func sweepProfile(r *Result, wt, profile string, cutoff time.Time, dryRun bool) {
	examples := filepath.Join(profile, "examples")
	names, err := namesByInode(profile, examples)
	if err != nil {
		r.Errors = append(r.Errors, err.Error())
		return
	}
	for _, dir := range []string{filepath.Join(profile, "deps"), examples} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				r.Errors = append(r.Errors, err.Error())
			}
			continue
		}
		for _, e := range entries {
			if !e.Type().IsRegular() || !candidateName.MatchString(e.Name()) {
				continue
			}
			fi, err := e.Info()
			if err != nil || fi.Mode().Perm()&0o100 == 0 {
				continue // gone, or not executable
			}
			lastUsed, ino, nlink, ok := fileUse(fi)
			if !ok || !lastUsed.Before(cutoff) {
				continue
			}
			path := filepath.Join(dir, e.Name())
			links := []string{path}
			for _, other := range names[ino] {
				if other != path {
					links = append(links, other)
				}
			}
			if uint64(len(links)) < nlink {
				continue
			}
			if !dryRun {
				if err := removeAll(links); err != nil {
					r.Errors = append(r.Errors, err.Error())
					continue
				}
			}
			r.Files = append(r.Files, File{Path: relTo(wt, path), Size: fi.Size(), LastUsed: lastUsed})
			r.Freed += fi.Size()
		}
	}
}

// namesByInode maps each regular file directly in dirs to its inode.
func namesByInode(dirs ...string) (map[uint64][]string, error) {
	out := map[uint64][]string{}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		for _, e := range entries {
			if !e.Type().IsRegular() {
				continue
			}
			fi, err := e.Info()
			if err != nil {
				continue
			}
			if _, ino, _, ok := fileUse(fi); ok {
				out[ino] = append(out[ino], filepath.Join(dir, e.Name()))
			}
		}
	}
	return out, nil
}

// removeAll removes every path, one already gone included, and joins the
// failures.
func removeAll(paths []string) error {
	var errs []error
	for _, p := range paths {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func isDir(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.IsDir()
}

func isRegular(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.Mode().IsRegular()
}

// relTo is p relative to wt, or p when it isn't beneath it.
func relTo(wt, p string) string {
	if rel, err := filepath.Rel(wt, p); err == nil {
		return rel
	}
	return p
}

// logResult logs one session's sweep. A session skipped for its status or
// for having no target dir is routine and logs at debug level.
func logResult(r Result, dryRun bool) {
	l := log.For("build_sweep")
	switch r.Skip {
	case "":
		l.Info("build_sweep.swept", "workspace", r.Workspace, "title", r.Title, "files", len(r.Files), "freed", r.Freed, "dry_run", dryRun)
	case SkipStatus, SkipNoTarget:
		l.Debug("build_sweep.skipped", "workspace", r.Workspace, "title", r.Title, "reason", string(r.Skip), "detail", r.Detail)
	default:
		l.Info("build_sweep.skipped", "workspace", r.Workspace, "title", r.Title, "reason", string(r.Skip), "detail", r.Detail)
	}
	for _, e := range r.Errors {
		l.Warn("build_sweep.error", "workspace", r.Workspace, "title", r.Title, "err", e)
	}
}
```

- [ ] **Step 14: Run the package's tests and see them pass**

Run: `CGO_ENABLED=0 go test ./session/buildsweep/`
Expected: PASS.

- [ ] **Step 15: Write the real-cargo test** (`session/buildsweep/cargo_test.go`)

```go
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
```

- [ ] **Step 16: Run it and see it pass**

Run: `CGO_ENABLED=0 go test ./session/buildsweep/ -run TestSweep_RealCargoBuild -v`
Expected: PASS, and not SKIP: cargo 1.99 is installed here. To mutation-check it, change `!lastUsed.Before(cutoff)` to `false` in `sweepProfile` and see it FAIL, because the second set goes too. Then restore the line.

- [ ] **Step 17: Cross-build, vet, format, commit**

```bash
GOOS=windows CGO_ENABLED=0 go build ./session/buildsweep/
GOOS=darwin CGO_ENABLED=0 go build ./session/buildsweep/
CGO_ENABLED=0 go vet ./session/buildsweep/
gofmt -l session/buildsweep/
git add -- session/buildsweep
git commit -m "feat(buildsweep): delete session worktrees' unused Rust executables

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>" -- session/buildsweep
```
Expected: the builds and vet succeed, and `gofmt -l` prints nothing.

---

## Work package 2: the setting, the model's gated job, and the `SweepBuilds` request

**Files:**
- Modify: `config/settings.go` (beside `ClaudeTmpArchiveDir`)
- Create: `core/build_sweep.go`
- Modify: `core/gate.go`, `core/tick.go:118-120`, `core/model.go:90-93` (fields) and `core/model.go:476` (`Deliver`), `core/requests.go` (`deliverTracked`, `outcome`), `core/iface.go:84-86`, `core/loop_core.go:98`, `core/events.go:199-207`, `core/events_json.go:36-57`
- Generated: `core/rpc/methods_gen.go`, `docs/specs/protocol.md`
- Test: `config/build_sweep_test.go`, `core/build_sweep_test.go`, `core/gate_test.go`

- [ ] **Step 1: Write the failing setting test** (`config/build_sweep_test.go`)

```go
package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSettings_BuildSweepAfterHoursKey(t *testing.T) {
	var s Settings
	require.NoError(t, json.Unmarshal([]byte(`{"build_sweep_after_hours": 12}`), &s))
	assert.Equal(t, 12, s.BuildSweepAfterHours)

	out, err := json.Marshal(Settings{})
	require.NoError(t, err)
	assert.NotContains(t, string(out), "build_sweep_after_hours", "off is left out")
}
```

Run: `CGO_ENABLED=0 go test ./config/ -run TestSettings_BuildSweepAfterHoursKey`
Expected: FAIL, `s.BuildSweepAfterHours undefined`.

- [ ] **Step 2: Add the setting** to `Settings` in `config/settings.go`, right after the `ClaudeTmpArchiveDir` field

```go
	// BuildSweepAfterHours turns the build sweep on (session/buildsweep):
	// every 30 minutes the daemon deletes the Rust executables in idle and
	// paused sessions' target dirs that have gone unused this many hours.
	// 0, the default, leaves it off. Read from the global config.json
	// only, at each sweep, so an edit takes effect without a restart.
	BuildSweepAfterHours int `json:"build_sweep_after_hours,omitempty"`
```

Run: `CGO_ENABLED=0 go test ./config/`
Expected: PASS.

- [ ] **Step 3: Write the failing model tests** (`core/build_sweep_test.go`)

```go
package core

import (
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/buildsweep"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sweepInst is a session record in status st whose worktree is a fresh
// temp dir, as a reconciled record comes back (FromInstanceData).
func sweepInst(t *testing.T, title string, st session.Status, terminal bool) *session.Instance {
	t.Helper()
	inst, err := session.FromInstanceData(session.InstanceData{
		Title: title, Status: st, Program: "claude", IsWorkspaceTerminal: terminal,
		Worktree: session.GitWorktreeData{WorktreePath: t.TempDir()},
	}, t.TempDir())
	require.NoError(t, err)
	return inst
}

// sweepModel serves one workspace holding insts.
func sweepModel(t *testing.T, insts ...*session.Instance) *Model {
	t.Helper()
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "ws")
	ws.insts = insts
	m.SetWorkspacesForTest(ws)
	return m
}

func skips(targets []buildsweep.Target) map[string]buildsweep.Skip {
	out := map[string]buildsweep.Skip{}
	for _, t := range targets {
		out[t.Title] = t.Skip
	}
	return out
}

// replyOf runs the one job a request queued, delivers its result, and
// returns the Reply the model emitted.
func replyOf(t *testing.T, m *Model) Reply {
	t.Helper()
	jobs := m.Drain().Jobs
	require.Len(t, jobs, 1)
	m.Deliver(jobs[0]())
	for _, ev := range m.Drain().Events {
		if r, ok := ev.(Reply); ok {
			return r
		}
	}
	require.Fail(t, "no Reply")
	return Reply{}
}

func TestBuildSweepTargets_OnlyIdleAndPausedSessions(t *testing.T) {
	m := sweepModel(t,
		sweepInst(t, "ready", session.Ready, false),
		sweepInst(t, "paused", session.Paused, false),
		sweepInst(t, "running", session.Running, false),
		sweepInst(t, "loading", session.Loading, false),
		sweepInst(t, "prompting", session.Prompting, false),
		sweepInst(t, "deleting", session.Deleting, false),
		sweepInst(t, "recoverable", session.Recoverable, false),
		sweepInst(t, "terminal", session.Ready, true),
	)

	assert.Equal(t, map[string]buildsweep.Skip{"ready": "", "paused": ""}, skips(m.buildSweepTargets(false)))
	assert.Equal(t, map[string]buildsweep.Skip{
		"ready": "", "paused": "",
		"running": buildsweep.SkipStatus, "loading": buildsweep.SkipStatus, "prompting": buildsweep.SkipStatus,
		"deleting": buildsweep.SkipStatus, "recoverable": buildsweep.SkipStatus,
	}, skips(m.buildSweepTargets(true)), "a request reports the rest; the terminal is never listed")
}

func TestMaybeBuildSweep_NoIdleSessionArmsNothing(t *testing.T) {
	m := sweepModel(t, sweepInst(t, "running", session.Running, false))

	assert.False(t, m.maybeBuildSweep())
	assert.False(t, m.gate(gateBuildSweep).inFlight)
	assert.Empty(t, m.Drain().Jobs)
}

func TestBuildSweepJob_ReadsTheThresholdFromTheGlobalConfig(t *testing.T) {
	targets := []buildsweep.Target{{Title: "s", Worktree: t.TempDir()}}
	job := func(opts buildsweep.Options) buildSweepResult {
		return buildSweepJob(buildsweep.Default(), targets, opts)().(buildSweepResult)
	}

	assert.ErrorIs(t, job(buildsweep.Options{}).err, errBuildSweepOff)

	dir, err := config.GetConfigDir()
	require.NoError(t, err)
	cfg := config.LoadConfigFrom(dir)
	cfg.BuildSweepAfterHours = 12
	require.NoError(t, config.SaveConfigTo(cfg, dir))
	t.Cleanup(func() {
		cfg.BuildSweepAfterHours = 0
		_ = config.SaveConfigTo(cfg, dir)
	})

	res := job(buildsweep.Options{DryRun: true})
	require.NoError(t, res.err)
	assert.Equal(t, 12, res.report.AfterHours)
	require.Len(t, res.report.Results, 1)
	assert.Equal(t, buildsweep.SkipNoTarget, res.report.Results[0].Skip)

	assert.Equal(t, 48, job(buildsweep.Options{DryRun: true, AfterHours: 48}).report.AfterHours, "a request's threshold wins")
}

func TestDeliverBuildSweep_NoticesAMountWithoutAccessTimesOnce(t *testing.T) {
	m := NewForTest(Options{})
	off := buildsweep.Result{Target: buildsweep.Target{Workspace: "ws", Title: "a", Skip: buildsweep.SkipAtimeOff, Detail: "/tb is mounted noatime, so it records no access times"}}
	other := off
	other.Title = "b"
	res := buildSweepResult{report: buildsweep.Report{Results: []buildsweep.Result{off, other}}}

	m.Deliver(res)
	m.Deliver(res)

	var notices []Notice
	for _, ev := range m.Drain().Events {
		if n, ok := ev.(Notice); ok {
			notices = append(notices, n)
		}
	}
	require.Len(t, notices, 1)
	assert.Contains(t, notices[0].Err.Error(), "/tb is mounted noatime")
}

func TestSweepBuilds_RepliesWithTheReport(t *testing.T) {
	m := sweepModel(t, sweepInst(t, "idle", session.Ready, false), sweepInst(t, "busy", session.Running, false))

	m.SweepBuilds(buildsweep.Options{DryRun: true, AfterHours: 48}, 7)
	reply := replyOf(t, m)

	assert.Equal(t, ReqID(7), reply.Req)
	require.NoError(t, reply.Err)
	assert.True(t, reply.Sweep.DryRun)
	got := map[string]buildsweep.Skip{}
	for _, r := range reply.Sweep.Results {
		got[r.Title] = r.Skip
	}
	assert.Equal(t, map[string]buildsweep.Skip{"idle": buildsweep.SkipNoTarget, "busy": buildsweep.SkipStatus}, got)
}

func TestSweepBuilds_WithoutAThresholdFails(t *testing.T) {
	m := sweepModel(t, sweepInst(t, "idle", session.Ready, false))

	m.SweepBuilds(buildsweep.Options{DryRun: true}, 7)

	assert.ErrorIs(t, replyOf(t, m).Err, errBuildSweepOff)
}
```

Add to `core/gate_test.go`:
- in `TestGateIntervalsUseEachJobsInterval`, the line `assert.Equal(t, buildSweepInterval, gateIntervals[gateBuildSweep])`;
- in `TestGatedDeliveryDisarmsFirst`, `gateBuildSweep` at the end of the kinds list;
- in `TestProductionGatedJobsYieldOneResult`, after the `accounts_refresh` subtest:

```go
	t.Run("build_sweep", func(t *testing.T) {
		m := sweepModel(t, sweepInst(t, "idle", session.Paused, false))
		result := inner(t, m, m.maybeBuildSweep(), gateBuildSweep)
		assert.IsType(t, buildSweepResult{}, result)
	})
```

Run: `CGO_ENABLED=0 go test ./core/ -run 'BuildSweep|SweepBuilds|Gate'`
Expected: FAIL, build errors (`undefined: gateBuildSweep`, `buildSweepTargets`, `Reply.Sweep`).

- [ ] **Step 4: Write `core/build_sweep.go`**

```go
package core

import (
	"errors"
	"fmt"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/buildsweep"
)

// buildSweepInterval is the build sweep's cadence: often enough to keep a
// threshold in hours to within half an hour, rarely enough that walking
// every Rust session's target dir costs nothing noticeable.
const buildSweepInterval = 30 * time.Minute

// errBuildSweepOff is a sweep's error when no threshold is set: the
// setting is absent or 0 and the request named none.
var errBuildSweepOff = errors.New("the build sweep is off: set build_sweep_after_hours in the global config.json, or pass --after")

// buildSweepResult is a build sweep's job result: its report, or why it
// didn't run.
type buildSweepResult struct {
	report buildsweep.Report
	err    error
}

// maybeBuildSweep dispatches the periodic build sweep when gateBuildSweep
// is due, over the sessions whose agent is idle or paused. The job reads
// the threshold, so a sweep turned off costs one config read an interval.
// Reports whether it dispatched; false when no session is eligible.
func (m *Model) maybeBuildSweep() bool {
	return m.dispatchGated(gateBuildSweep, time.Now(), func() Job {
		targets := m.buildSweepTargets(false)
		if len(targets) == 0 {
			return nil
		}
		return buildSweepJob(buildsweep.Default(), targets, buildsweep.Options{})
	})
}

// SweepBuilds sweeps every session's build dir now, as the periodic sweep
// would (or, with opts.DryRun, reports what would go), and answers with
// the Reply's Sweep. The sessions the periodic sweep passes over are
// reported as skipped, with their status.
func (m *Model) SweepBuilds(opts buildsweep.Options, req ReqID) {
	m.spawn(m.track(req, 0, buildSweepJob(buildsweep.Default(), m.buildSweepTargets(true), opts)))
}

// buildSweepTargets are the sessions a build sweep covers, in every
// workspace the model serves: those whose agent is Ready or Paused, and,
// with all, every other session with a worktree, marked skipped for its
// status. A workspace terminal runs in the main checkout and is never
// listed.
func (m *Model) buildSweepTargets(all bool) []buildsweep.Target {
	var out []buildsweep.Target
	for _, ws := range m.workspaces {
		for _, inst := range ws.insts {
			p := inst.GetWorktreePath()
			if inst.IsWorkspaceTerminal || p == "" {
				continue
			}
			st := inst.GetStatus()
			t := buildsweep.Target{Workspace: ws.Label(), Title: inst.Title, Status: st.String(), Worktree: p}
			if st != session.Ready && st != session.Paused {
				if !all {
					continue
				}
				t.Skip, t.Detail = buildsweep.SkipStatus, "the agent is "+st.String()
			}
			out = append(out, t)
		}
	}
	return out
}

// buildSweepJob runs a sweep off the loop: it resolves the threshold
// (opts.AfterHours, else the global config's build_sweep_after_hours,
// read now so an edit takes effect without a restart) and sweeps targets.
func buildSweepJob(s buildsweep.Sweeper, targets []buildsweep.Target, opts buildsweep.Options) Job {
	return func() any {
		hours := opts.AfterHours
		if hours == 0 {
			hours = config.LoadConfigFromGlobal().BuildSweepAfterHours
		}
		if hours <= 0 {
			return buildSweepResult{err: errBuildSweepOff}
		}
		return buildSweepResult{report: s.Run(targets, time.Duration(hours)*time.Hour, opts.DryRun)}
	}
}

// deliverBuildSweep raises a Notice for each mount the periodic sweep
// skipped for its access times, once per mount while the daemon runs: a
// sweep that can't work must say so, but not every half hour. A request's
// own sweep raises none, since its report says it.
func (m *Model) deliverBuildSweep(r buildSweepResult) {
	if m.cause != 0 {
		return
	}
	for _, res := range r.report.Results {
		if res.Skip != buildsweep.SkipAtimeOff || m.buildSweepWarned[res.Detail] {
			continue
		}
		if m.buildSweepWarned == nil {
			m.buildSweepWarned = map[string]bool{}
		}
		m.buildSweepWarned[res.Detail] = true
		m.emit(Notice{Err: fmt.Errorf("build sweep: %s; build dirs there are not swept", res.Detail)})
	}
}
```

- [ ] **Step 5: Wire it into the model**

`core/gate.go`: add `gateBuildSweep`, with its comment, before `numGateKinds`:

```go
	// gateBuildSweep throttles the build sweep (maybeBuildSweep).
	gateBuildSweep
```

Add a `String()` case:

```go
	case gateBuildSweep:
		return "build_sweep"
```

Add an entry to `gateIntervals`:

```go
	gateBuildSweep: buildSweepInterval,
```

`core/tick.go`: right after `m.maybeClaudeTmpSweep()` and its comment:

```go

	// The build sweep, on its own 30-minute cadence (see
	// maybeBuildSweep). Nothing when no session is idle or paused, or one
	// runs.
	m.maybeBuildSweep()
```

`core/model.go`, `Model` struct, after `claudeTmpPending`:

```go
	// buildSweepWarned holds the mounts the build sweep has raised a
	// Notice for, keyed by its skip's detail, so each is said once a
	// daemon run (deliverBuildSweep).
	buildSweepWarned map[string]bool
```

`core/model.go`, `Deliver`, before the `case tracked:` line:

```go
	case buildSweepResult:
		m.deliverBuildSweep(msg)
```

`core/requests.go`, in `deliverTracked`'s switch after `case diffResult:` / `r.Diff = res.patch`:

```go
	case buildSweepResult:
		r.Sweep = res.report
```

`core/requests.go`, in `outcome`'s switch after `case diffResult:` / `return r.err, nil`:

```go
	case buildSweepResult:
		return r.err, nil
```

`core/events.go`: add a `Sweep buildsweep.Report` field to `Reply`, after `Diff`, and the import `"github.com/aidan-bailey/loom/session/buildsweep"`.

`core/events_json.go`: add `Sweep buildsweep.Report` to `replyJSON` after `Diff`, and `Sweep: r.Sweep` in `MarshalJSON` and `Sweep: j.Sweep` in `UnmarshalJSON`, each after the `Diff` field, with the same import.

`core/iface.go`: after `ReadDiff(id InstanceID, req ReqID)`, with the import:

```go
	// Delete idle and paused sessions' unused Rust executables now, or
	// with DryRun list them: the Reply's Sweep (buildsweep.Report).
	SweepBuilds(opts buildsweep.Options, req ReqID)
```

`core/loop_core.go`: after the `ReadDiff` forwarder, with the import:

```go
func (l *Loop) SweepBuilds(opts buildsweep.Options, req ReqID) {
	l.do(func(m *Model) { m.SweepBuilds(opts, req) })
}
```

- [ ] **Step 6: Regenerate the wire and the protocol reference**

```bash
CGO_ENABLED=0 go generate ./core/rpc
CGO_ENABLED=0 go test ./core/rpc -run TestProtocolReference -update
```

- [ ] **Step 7: Run the model, wire and config tests and see them pass**

Run: `CGO_ENABLED=0 go test ./core/... ./config/`
Expected: PASS. Watch `TestWire_EveryFieldSurvives`, which fails if `replyJSON` drops `Sweep`; `TestCoreIsValueTyped`; `TestLoopForwardsEachMethodToItsNamesake`; `TestGenerated_IsFresh`; `TestProtocolReference`. To mutation-check, delete `Sweep: j.Sweep` from `UnmarshalJSON`, see `TestWire_EveryFieldSurvives/Reply` FAIL, and restore it.

- [ ] **Step 8: Race, format, commit**

```bash
CC=clang CGO_ENABLED=1 go test -race ./core/ -run 'BuildSweep|SweepBuilds|Gate'
gofmt -l config/settings.go config/build_sweep_test.go core/build_sweep.go core/build_sweep_test.go core/gate.go core/gate_test.go core/tick.go core/model.go core/requests.go core/events.go core/events_json.go core/iface.go core/loop_core.go core/rpc/methods_gen.go
git add -- config/settings.go config/build_sweep_test.go core/build_sweep.go core/build_sweep_test.go core/gate.go core/gate_test.go core/tick.go core/model.go core/requests.go core/events.go core/events_json.go core/iface.go core/loop_core.go core/rpc/methods_gen.go docs/specs/protocol.md
git commit -m "feat(core,rpc,config): the daemon sweeps idle sessions' unused Rust binaries; SweepBuilds runs it on request

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>" -- config/settings.go config/build_sweep_test.go core/build_sweep.go core/build_sweep_test.go core/gate.go core/gate_test.go core/tick.go core/model.go core/requests.go core/events.go core/events_json.go core/iface.go core/loop_core.go core/rpc/methods_gen.go docs/specs/protocol.md
```
Expected: race PASS; `gofmt -l` prints nothing. If `git status` shows other files that generation changed, add them to the commit by name.

---

## Work package 3: `loom sweep`

**Files:**
- Create: `internal/cli/sweep.go`
- Modify: `main.go:427` (registration)
- Test: `internal/cli/sweep_test.go`

- [ ] **Step 1: Write the failing CLI tests** (`internal/cli/sweep_test.go`)

```go
package cli

import (
	"bytes"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session/buildsweep"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runSweep runs `loom sweep args…` on a fresh command tree (newSweepCmd).
func runSweep(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newSweepCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestSweep_WithoutAThresholdFails(t *testing.T) {
	isolateWorkspaces(t)
	registered(t, "ws-a", pausedRecords)
	serveDaemon(t, "true")

	_, err := runSweep(t, "--dry-run")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "build_sweep_after_hours")
}

func TestSweep_DryRunReportsEverySession(t *testing.T) {
	isolateWorkspaces(t)
	wt := t.TempDir()
	registered(t, "ws-a", `[{"title":"alpha","status":3,"program":"true","worktree":{"worktree_path":"`+wt+`"}}]`)
	serveDaemon(t, "true")

	out, err := runSweep(t, "--dry-run", "--after", "48")

	require.NoError(t, err)
	assert.Contains(t, out, "Dry run, nothing deleted: binaries unused for 48h")
	assert.Regexp(t, `ws-a\s+alpha\s+Paused\s+skipped \(no_target\)`, out)
	assert.Contains(t, out, "Total: 0 files, 0B")
}

func TestSweep_RefusesANegativeThreshold(t *testing.T) {
	_, err := runSweep(t, "--after", "-1")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "--after")
}

func TestWriteSweepReport(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	rep := buildsweep.Report{AfterHours: 12, Results: []buildsweep.Result{
		{
			Target: buildsweep.Target{Workspace: "poly", Title: "wasm", Status: "Ready"},
			Files:  []buildsweep.File{{Path: "target/debug/deps/health-0123456789abcdef", Size: 3 << 30, LastUsed: now.Add(-30 * time.Hour)}},
			Freed:  3 << 30,
		},
		{Target: buildsweep.Target{Workspace: "poly", Title: "busy", Status: "Paused", Skip: buildsweep.SkipBuilding, Detail: "target/debug/.cargo-build-lock is locked: cargo is building"}},
	}}
	var out bytes.Buffer

	writeSweepReport(&out, rep, true, now)

	s := out.String()
	assert.Contains(t, s, "Deleted binaries unused for 12h")
	assert.Regexp(t, `poly\s+wasm\s+Ready\s+1 file, 3\.0G`, s)
	assert.Contains(t, s, "target/debug/deps/health-0123456789abcdef  3.0G  unused 30h")
	assert.Regexp(t, `poly\s+busy\s+Paused\s+skipped \(building\): target/debug/\.cargo-build-lock is locked`, s)
	assert.Contains(t, s, "Total: 1 file, 3.0G")
}
```

Run: `CGO_ENABLED=0 go test ./internal/cli/ -run 'TestSweep_|TestWriteSweepReport'`
Expected: FAIL, `undefined: newSweepCmd`, `writeSweepReport`.

- [ ] **Step 2: Write `internal/cli/sweep.go`**

```go
package cli

import (
	"errors"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/aidan-bailey/loom/session/buildsweep"
	"github.com/spf13/cobra"
)

// SweepCmd is `loom sweep`: the daemon's build sweep, run now.
var SweepCmd = newSweepCmd()

// newSweepCmd builds `loom sweep`, with flags of its own: each call is
// independent (tests build one per run).
func newSweepCmd() *cobra.Command {
	var (
		dryRun, verbose bool
		after           int
	)
	cmd := &cobra.Command{
		Use:   "sweep",
		Short: "Delete idle and paused sessions' unused Rust binaries",
		Long: "Asks the daemon to run its build sweep now: in every session whose agent is idle or paused, " +
			"it deletes the executables in the worktree's cargo target dir unused for build_sweep_after_hours " +
			"(or --after). --dry-run lists them and deletes nothing.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			loomErrors(c)
			if after < 0 {
				return errors.New("--after takes a number of hours above 0")
			}
			return withDaemon(c, func(d *client) error {
				d.SweepBuilds(buildsweep.Options{DryRun: dryRun, AfterHours: after}, sessionReq)
				reply, err := d.awaitReply(sessionReq)
				switch {
				case err != nil:
					return err
				case reply.Err != nil:
					return reply.Err
				}
				writeSweepReport(c.OutOrStdout(), reply.Sweep, verbose, time.Now())
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "List what would be deleted, and delete nothing")
	cmd.Flags().IntVar(&after, "after", 0, "Hours unused before a binary goes (default: build_sweep_after_hours)")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "List every file")
	return cmd
}

// writeSweepReport prints a sweep: a heading, one row per session (its
// files and bytes, or why it was skipped) with, verbose, each file under
// it, then the total.
func writeSweepReport(w io.Writer, rep buildsweep.Report, verbose bool, now time.Time) {
	if rep.DryRun {
		fmt.Fprintf(w, "Dry run, nothing deleted: binaries unused for %dh\n", rep.AfterHours)
	} else {
		fmt.Fprintf(w, "Deleted binaries unused for %dh\n", rep.AfterHours)
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	var files int
	var freed int64
	for _, r := range rep.Results {
		if r.Skip != "" {
			fmt.Fprintf(tw, "%s\t%s\t%s\tskipped (%s): %s\n", r.Workspace, r.Title, r.Status, r.Skip, r.Detail)
		} else {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s, %s\n", r.Workspace, r.Title, r.Status, fileCount(len(r.Files)), formatSize(r.Freed))
		}
		for _, e := range r.Errors {
			fmt.Fprintf(tw, "\t\t\terror: %s\n", e)
		}
		if verbose {
			for _, f := range r.Files {
				fmt.Fprintf(tw, "\t\t\t  %s  %s  unused %dh\n", f.Path, formatSize(f.Size), int(now.Sub(f.LastUsed).Hours()))
			}
		}
		files += len(r.Files)
		freed += r.Freed
	}
	_ = tw.Flush()
	fmt.Fprintf(w, "Total: %s, %s\n", fileCount(files), formatSize(freed))
}

func fileCount(n int) string {
	if n == 1 {
		return "1 file"
	}
	return fmt.Sprintf("%d files", n)
}

// formatSize is n bytes in binary units, to one decimal: 0B, 1.5K, 3.0G.
func formatSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%c", float64(n)/float64(div), "KMGTPE"[exp])
}
```

- [ ] **Step 3: Register it** in `main.go`, after `rootCmd.AddCommand(cli.WebCmd)`:

```go
	rootCmd.AddCommand(cli.SweepCmd)
```

- [ ] **Step 4: Run the CLI tests and see them pass**

Run: `CGO_ENABLED=0 go test ./internal/cli/ -run 'TestSweep_|TestWriteSweepReport'` then `CGO_ENABLED=0 go build ./...`
Expected: PASS, and the build succeeds. If `TestSweep_DryRunReportsEverySession` shows the record in another status than Paused (the boot's reconcile of a plain temp dir), point `worktree_path` at a dir as `liveWorkspace` makes, and change only the status in the regexp, leaving the `no_target` assertion as it is.

- [ ] **Step 5: Format and commit**

```bash
gofmt -l internal/cli/sweep.go internal/cli/sweep_test.go main.go
git add -- internal/cli/sweep.go internal/cli/sweep_test.go main.go
git commit -m "feat(cli): loom sweep runs the build sweep now, or lists it with --dry-run

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>" -- internal/cli/sweep.go internal/cli/sweep_test.go main.go
```

---

## Work package 4: docs and the whole-repo gates

**Files:**
- Modify: `USAGE.md` (options table, a new section, commands table), `session/README.md`, `session/CLAUDE.md`, `internal/cli/CLAUDE.md`, `core/README.md`, `docs/superpowers/specs/2026-11-03-build-sweep-design.md` (§10)

- [ ] **Step 1: `USAGE.md`**

In the Configuration options table, after the `claude_tmp_archive_dir` row:

```markdown
| `build_sweep_after_hours` | int | `0` | Turns the build sweep on: every 30 minutes the daemon deletes the Rust executables in idle and paused sessions' cargo target dirs that have gone unused this many hours (global `config.json` only; `0` is off). See [Build Sweep](#build-sweep) |
```

Before `### Claude Fullscreen Renderer`:

```markdown
### Build Sweep

Cargo builds into each session's own `<worktree>/target/` and never deletes a test binary it has replaced: a new feature set, profile or flag writes a new copy under a new hash. Over a few days of agents running `cargo test` that reaches tens of gigabytes per session. With `build_sweep_after_hours` set in the global `~/.loom/config.json`, the daemon deletes those copies once they have gone unused that long:

```json
{ "build_sweep_after_hours": 12 }
```

- **Which sessions:** only sessions whose agent is idle (Ready) or paused. The main checkout and workspace terminals are never swept.
- **What goes:** executables in a cargo profile dir's `deps/` and `examples/` (`<name>-<16 hex digits>`, no extension), with the hard links cargo makes to them under their plain names. Libraries, proc-macros, build scripts and the incremental cache stay. Cargo rebuilds anything deleted the next time it's needed.
- **"Unused" means the file's last access time,** so the disk must record access times. Linux only. A disk mounted `noatime` is skipped. One mounted `relatime` records an access at most once a day, so it is swept only with a threshold of 24 hours or more. For hour thresholds, mount it `strictatime,lazytime`: every access is recorded, and `lazytime` writes the timestamps out in batches. A skipped disk shows a notice once each time the daemon starts.
- **Never mid-build:** a session is skipped while cargo holds its target dir's locks, or while any of your `cargo` processes runs inside the worktree. That second check covers `cargo test`, which drops its locks before it runs the tests.
- **On demand:** `loom sweep` runs the sweep now. `loom sweep --dry-run --after 12 -v` lists what would go, file by file, and deletes nothing; `--after` works even while the setting is off. Each sweep is logged as `build_sweep.swept` and `build_sweep.skipped` in the daemon's log.
```

In the Commands table, after the `web` row:

```markdown
| `sweep` | Run the build sweep now: delete idle and paused sessions' unused Rust binaries (`--dry-run` lists them, `--after <hours>` sets the threshold, `-v` lists each file; see [Build Sweep](#build-sweep)); a client of the daemon too |
```

- [ ] **Step 2: `session/README.md`**

Add `session/buildsweep/` to the sub-packages list in the first paragraph, after `session/agent/`, and this section after `### agent` in `## Sub-packages`:

```markdown
### buildsweep

Deletes the Rust executables in a session worktree's cargo target dir that have gone unused for the threshold (`Sweeper.Run`). The model runs it every 30 minutes on idle and paused sessions, and on request (`core.SweepBuilds`, `loom sweep`).
- Candidates: names matching `<name>-<16 hex>` with the owner-execute bit, directly in a profile dir's `deps/` or `examples/`. A profile dir is any dir up to six levels under `target/` holding both `deps/` and `.fingerprint/`; no symlink is followed. A candidate's other names in the profile dir and `examples/` (cargo's uplifted hard links) go with it, and a file with a link outside them is kept.
- Last use is `max(atime, mtime)`, so the mount must record access times. `/proc/self/mountinfo`'s per-mount options decide: `noatime` is skipped, `relatime` is swept only for thresholds of 24h or more, and no flag means `strictatime`.
- A worktree is skipped while any of cargo's lock files (`.cargo-lock`, `.cargo-build-lock`, `.cargo-artifact-lock`) is `flock`ed. It is also skipped while a `cargo`/`cargo-*` process of the daemon's user has its working directory inside the worktree; that is checked under the locks, because cargo 1.99 holds no lock while `cargo test` runs the tests.
- Linux only (`stat_linux.go`); elsewhere every worktree is skipped as `atime_off`.
```

- [ ] **Step 3: `session/CLAUDE.md`**

In the first line's sub-package list, add `buildsweep` after `agent`. In the rule that lists the pure packages (`Keep session/github, session/hooks, …`), add `session/buildsweep`. Add this rule after that one:

```markdown
- **Let `session/buildsweep` delete only hashed executables in a profile dir's `deps/` and `examples/` (with their uplifted links) past the threshold, and skip a worktree on any doubt: no `CACHEDIR.TAG`, a mount that records no access times (or `relatime` under 24h), a cargo lock held, a `cargo` process inside it.** Libraries, proc-macros and build scripts are used rarely and look unused, and losing them rebuilds chains of crates; `cargo test` drops its locks before running the tests, so only the process check covers a binary cargo found fresh but hasn't run yet. A wrong delete fails a test run or costs minutes of rebuild, and the sweep's log doesn't say why. **Enforced** by `TestSweep_DeletesOnlyExecutablesUnusedPastTheThreshold`, `TestSweep_SweepsOnlyWhereAccessTimesAreRecorded`, `TestSweep_SkipsWhileCargoHoldsALock`, `TestSweep_SkipsWhileCargoRunsInTheWorktree` and `TestSweep_RealCargoBuild`.
```

- [ ] **Step 4: `internal/cli/CLAUDE.md` and `core/README.md`**

In `internal/cli/CLAUDE.md`'s first sentence, change "The `loom workspace`, `loom account`, `loom session` and `loom web` subcommands (`WorkspaceCmd` in `workspace.go`, `AccountCmd` in `account.go`, `SessionCmd` in `session.go`, `WebCmd` in `web.go`)" to "The `loom workspace`, `loom account`, `loom session`, `loom web` and `loom sweep` subcommands (`WorkspaceCmd` in `workspace.go`, `AccountCmd` in `account.go`, `SessionCmd` in `session.go`, `WebCmd` in `web.go`, `SweepCmd` in `sweep.go`)".

In `core/README.md`'s file table, after the `claude_tmp.go` row:

```markdown
| `build_sweep.go` | `maybeBuildSweep`, `SweepBuilds`, `buildSweepTargets`, `buildSweepJob`, `deliverBuildSweep` |
```

In "The health tick and gated jobs", change "(roster, hook scan, GitHub poll, usage probe, the Claude temp-dir sweeps workspace loads queued)" to "(roster, hook scan, GitHub poll, usage probe, the Claude temp-dir sweeps workspace loads queued, the build sweep)".

- [ ] **Step 5: The spec's known limits.** In §10 of `docs/superpowers/specs/2026-11-03-build-sweep-design.md`, replace the first bullet with:

```markdown
- Only the worktrees of sessions loom has a record of are swept. The daemon loads every registered workspace at boot, so a paused session in an unopened workspace is covered; an orphan worktree (`Recoverable`) is not.
```

- [ ] **Step 6: The whole-repo gates**

```bash
CGO_ENABLED=0 go test ./...
CC=clang CGO_ENABLED=1 go test -race ./session/buildsweep/ ./core/ ./internal/cli/
gofmt -l $(git ls-files '*.go' | grep -v '^vendor/')
CGO_ENABLED=0 go vet ./...
GOOS=windows CGO_ENABLED=0 go build ./...
go run ./tools/claudemd -v
```
Expected: every test passes, `gofmt -l` prints nothing, vet is clean, the Windows build succeeds, and the CLAUDE.md lint passes within its budgets. golangci-lint is skipped locally: the dev shell's v2 rejects the repo's config. Say so in the report.

- [ ] **Step 7: Commit**

```bash
git add -- USAGE.md session/README.md session/CLAUDE.md internal/cli/CLAUDE.md core/README.md docs/superpowers/specs/2026-11-03-build-sweep-design.md
git commit -m "docs: the build sweep, its setting and loom sweep

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>" -- USAGE.md session/README.md session/CLAUDE.md internal/cli/CLAUDE.md core/README.md docs/superpowers/specs/2026-11-03-build-sweep-design.md
```

---

## After the plan (the user's steps)

1. Change `/tb`'s mount options in `~/System` from `noatime` to `strictatime,lazytime`, keeping `commit=60`, and remount. Check with `findmnt -no OPTIONS -T /tb`: it must not show `noatime` or `relatime`.
2. Install the new loom build. The daemon has to restart onto it, and `loom serve stop` takes every open TUI offline until a new daemon starts.
3. `loom sweep --dry-run --after 12 -v` to see what would go, then set `"build_sweep_after_hours": 12` in `~/.loom/config.json`.
