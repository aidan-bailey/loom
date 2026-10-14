# Claude Chat Archive Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan package-by-package. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep a permanent copy of every dead or paused loom session's Claude conversation, so Claude's 30-day transcript cleanup never takes the only copy, and put a paused session's deleted transcript back before Resume chooses `--resume`.

**Architecture:** `session/claudetmp` gains a copy mode (`Snapshot`: zip a dir without touching it, carrying forward entries of the previous zip whose source is gone) and a gap-filling restore (`Fill`). `session/claude_chat.go` wires them into Pause, Kill, Resume and a new `SweepClaudeChats`. `core` runs that sweep in the existing `gateClaudeTmp` job, after the temp-dir sweep.

**Tech Stack:** Go 1.25 (`archive/zip` incl. `zip.Writer.Copy`, `os.Root`), testify.

**Spec:** `docs/superpowers/specs/2026-10-14-claude-chat-archive-design.md`.

---

## Rules that bind every package

- Never run loom itself in a pane. Run dev builds through the `loom-dev` skill.
- Keep scratch files out of the worktree. Commit with explicit paths (`git commit -- <paths>`), never `-a`, `git add -A` or `.`.
- Tests: `CGO_ENABLED=0 go test ...` (the machine has clang only). Race: `CC=clang CGO_ENABLED=1 go test -race ...`.
- Format only the files you touch: `gofmt -w <files>`, never `gofmt -w .` (vendored files).
- `session/claudetmp` stays free of tmux, UI, app and config imports.
- Loom never moves, edits or deletes a file under Claude's `projects/`. Every chat operation copies.
- Each package is one implementer, one review and one commit. The numbered steps inside are checkpoints, not commits.

## File map

| File | Package | Change |
|---|---|---|
| `session/claudetmp/archive.go` | A | `Manifest.CarriedForward`, `Result.Carried`, `addTree` records the names it writes |
| `session/claudetmp/snapshot.go` | A | **new**: `ChatArchiveDir`, `Snapshot`, `Fill`, `modifiedSince`, `snapshotSlack` |
| `session/claudetmp/snapshot_test.go` | A | **new** |
| `session/claudetmp/locate.go` | A | package doc covers chat dirs |
| `session/claude_chat.go` | B | **new**: `ClaudeChatArchiveDir`, `ClaudeProjectsRoots`, `claudeProjectsDir`, `chatDir`, `archiveClaudeChat(Fn)`, `Instance.archiveChat`, `Instance.restoreChat`, `restoreClaudeChat`, `SweepClaudeChats` |
| `session/claude_chat_test.go` | B | **new** |
| `session/instance.go` | B | Pause, Kill and both Resume paths call the new functions |
| `session/claude_tmp_realclaude_test.go` | B | contract test also pins the chat dir, and hands the pane a logged-in config dir |
| `session/CLAUDE.md`, `session/README.md` | B | a rule; facts |
| `core/claude_tmp.go` | C | job carries the running set and config dirs and runs `SweepClaudeChats` |
| `core/claude_tmp_test.go` | C | two tests |
| `main.go` | C | `loom debug` prints the chat archive dir |
| `USAGE.md`, `docs/ARCHITECTURE.md`, `config/README.md` | C | user docs, persistent state, the setting's scope |

---

## Package A: `claudetmp` snapshot and fill

**Files:**
- Modify: `session/claudetmp/archive.go`
- Modify: `session/claudetmp/locate.go:1-13` (package doc)
- Create: `session/claudetmp/snapshot.go`
- Test: `session/claudetmp/snapshot_test.go`

### A1. Write the failing tests

- [ ] **Step 1: Create `session/claudetmp/snapshot_test.go`**

It reuses helpers already in the package's tests: `writeTree`, `requireNoPartials`, `zipEntries`, `readZipEntry`, `readZipManifest`, `failingFile` (`archive_test.go`), `backdate(t, name, age)` (`archive_test.go`), and `handZip`/`handEntry` (`restore_test.go`).

```go
package claudetmp

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// chatTree lays out a Claude project dir as a finished session leaves it:
// a transcript, a subagent's transcript and a tool result, all an hour old.
func chatTree(t *testing.T) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "-wt-u-chat-18be000000000001")
	writeTree(t, src, map[string]string{
		"s1.jsonl":                     "turn 1\n",
		"s1/subagents/agent-1.jsonl":   "sub\n",
		"s1/tool-results/result-1.txt": "out\n",
	})
	require.NoError(t, filepath.WalkDir(src, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		backdate(t, p, time.Hour)
		return nil
	}))
	return src
}

// snapshotted takes a first snapshot of src (reason "pause") and returns
// the zip's path.
func snapshotted(t *testing.T, src string) string {
	t.Helper()
	zipPath := filepath.Join(t.TempDir(), "chats", "u-chat.zip")
	_, wrote, err := Snapshot(src, zipPath, Manifest{Reason: "pause", Worktree: "/wt/u/chat_18be000000000001"})
	require.NoError(t, err)
	require.True(t, wrote)
	return zipPath
}

func TestSnapshot_CopiesAndLeavesTheSource(t *testing.T) {
	src := chatTree(t)
	zipPath := filepath.Join(t.TempDir(), "chats", "u-chat.zip")

	res, wrote, err := Snapshot(src, zipPath, Manifest{Reason: "kill", Worktree: "/wt/u/chat_18be000000000001"})

	require.NoError(t, err)
	assert.True(t, wrote)
	assert.Positive(t, res.BytesOut)
	entries := zipEntries(t, zipPath)
	assert.Equal(t, "turn 1\n", readZipEntry(t, entries["s1.jsonl"]))
	assert.Equal(t, "sub\n", readZipEntry(t, entries["s1/subagents/agent-1.jsonl"]))
	assert.Equal(t, "out\n", readZipEntry(t, entries["s1/tool-results/result-1.txt"]))
	m := readZipManifest(t, zipPath)
	assert.Equal(t, "kill", m.Reason)
	assert.Equal(t, filepath.Base(src), m.DirName)
	assert.Equal(t, "/wt/u/chat_18be000000000001", m.Worktree)
	assert.Empty(t, m.CarriedForward)
	data, err := os.ReadFile(filepath.Join(src, "s1.jsonl"))
	require.NoError(t, err)
	assert.Equal(t, "turn 1\n", string(data), "the source is never touched")
	fi, err := os.Stat(zipPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	requireNoPartials(t, filepath.Dir(zipPath))
}

func TestSnapshot_AnUnchangedSourceWritesNothing(t *testing.T) {
	src := chatTree(t)
	zipPath := snapshotted(t, src)
	before, err := os.Stat(zipPath)
	require.NoError(t, err)

	_, wrote, err := Snapshot(src, zipPath, Manifest{Reason: "kill"})

	require.NoError(t, err)
	assert.False(t, wrote)
	after, err := os.Stat(zipPath)
	require.NoError(t, err)
	assert.Equal(t, before.ModTime(), after.ModTime())
	assert.Equal(t, "pause", readZipManifest(t, zipPath).Reason, "the earlier snapshot stands")
}

func TestSnapshot_ANewerFileAnywhereTriggersAWrite(t *testing.T) {
	src := chatTree(t)
	zipPath := snapshotted(t, src)
	writeTree(t, src, map[string]string{"s1/subagents/agent-2.jsonl": "sub 2\n"})

	_, wrote, err := Snapshot(src, zipPath, Manifest{Reason: "kill"})

	require.NoError(t, err)
	assert.True(t, wrote)
	assert.Equal(t, "sub 2\n", readZipEntry(t, zipEntries(t, zipPath)["s1/subagents/agent-2.jsonl"]))
	assert.Equal(t, "kill", readZipManifest(t, zipPath).Reason)
}

// TestSnapshot_CarriesForwardWhatTheSourceLost: Claude's cleanup deletes a
// transcript between two snapshots. The second must not drop it: the zip
// may be its only copy.
func TestSnapshot_CarriesForwardWhatTheSourceLost(t *testing.T) {
	src := chatTree(t)
	zipPath := snapshotted(t, src)
	require.NoError(t, os.Remove(filepath.Join(src, "s1.jsonl")))
	writeTree(t, src, map[string]string{"s1/subagents/agent-1.jsonl": "sub, later\n"})

	res, wrote, err := Snapshot(src, zipPath, Manifest{Reason: "sweep"})

	require.NoError(t, err)
	assert.True(t, wrote)
	assert.Equal(t, 1, res.Carried)
	entries := zipEntries(t, zipPath)
	assert.Equal(t, "turn 1\n", readZipEntry(t, entries["s1.jsonl"]), "a file Claude deleted stays archived")
	assert.Equal(t, "sub, later\n", readZipEntry(t, entries["s1/subagents/agent-1.jsonl"]), "the file on disk wins")
	assert.Equal(t, []string{"s1.jsonl"}, readZipManifest(t, zipPath).CarriedForward)

	writeTree(t, src, map[string]string{"s2.jsonl": "next\n"})
	_, wrote, err = Snapshot(src, zipPath, Manifest{Reason: "sweep"})
	require.NoError(t, err)
	require.True(t, wrote)
	assert.Equal(t, "turn 1\n", readZipEntry(t, zipEntries(t, zipPath)["s1.jsonl"]), "carried again by every later snapshot")
}

func TestSnapshot_AWriteFailureKeepsThePreviousZip(t *testing.T) {
	src := chatTree(t)
	zipPath := snapshotted(t, src)
	before, err := os.ReadFile(zipPath)
	require.NoError(t, err)
	writeTree(t, src, map[string]string{"s1.jsonl": strings.Repeat("y", 1<<20)})
	orig := openArchiveFile
	openArchiveFile = func(dir, pattern string) (archiveFile, string, error) {
		f, name, err := orig(dir, pattern)
		if err != nil {
			return nil, "", err
		}
		return &failingFile{File: f.(*os.File), left: 100}, name, nil
	}
	t.Cleanup(func() { openArchiveFile = orig })

	_, wrote, err := Snapshot(src, zipPath, Manifest{})

	require.Error(t, err)
	assert.False(t, wrote)
	after, err := os.ReadFile(zipPath)
	require.NoError(t, err)
	assert.Equal(t, before, after, "the previous snapshot is untouched")
	requireNoPartials(t, filepath.Dir(zipPath))
}

// TestSnapshot_AnUnreadablePreviousZipIsMovedAsideAndKept: never replace
// what might be an archive loom cannot read.
func TestSnapshot_AnUnreadablePreviousZipIsMovedAsideAndKept(t *testing.T) {
	src := chatTree(t)
	dir := filepath.Join(t.TempDir(), "chats")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	zipPath := filepath.Join(dir, "u-chat.zip")
	require.NoError(t, os.WriteFile(zipPath, []byte("not a zip"), 0o600))
	backdate(t, zipPath, 2*time.Hour) // older than the source, which then reads as newer

	_, wrote, err := Snapshot(src, zipPath, Manifest{})

	require.NoError(t, err)
	assert.True(t, wrote)
	demoted, err := filepath.Glob(filepath.Join(dir, "u-chat.*.zip"))
	require.NoError(t, err)
	require.Len(t, demoted, 1)
	data, err := os.ReadFile(demoted[0])
	require.NoError(t, err)
	assert.Equal(t, "not a zip", string(data))
	assert.Contains(t, zipEntries(t, zipPath), "s1.jsonl")
}

func TestFill_RestoresOnlyWhatIsMissing(t *testing.T) {
	src := chatTree(t)
	zipPath := snapshotted(t, src)
	require.NoError(t, os.Remove(filepath.Join(src, "s1.jsonl")))
	writeTree(t, src, map[string]string{"s1/subagents/agent-1.jsonl": "local\n"})

	n, err := Fill(zipPath, src)

	require.NoError(t, err)
	assert.Equal(t, 1, n)
	data, err := os.ReadFile(filepath.Join(src, "s1.jsonl"))
	require.NoError(t, err)
	assert.Equal(t, "turn 1\n", string(data))
	fi, err := os.Stat(filepath.Join(src, "s1.jsonl"))
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now(), fi.ModTime(), time.Minute, "a restored transcript reads as fresh to Claude's cleanup")
	local, err := os.ReadFile(filepath.Join(src, "s1", "subagents", "agent-1.jsonl"))
	require.NoError(t, err)
	assert.Equal(t, "local\n", string(local), "Fill never overwrites")
	assert.FileExists(t, zipPath, "the archive stays")
}

func TestFill_CreatesAMissingDir(t *testing.T) {
	src := chatTree(t)
	zipPath := snapshotted(t, src)
	require.NoError(t, os.RemoveAll(src))

	n, err := Fill(zipPath, src)

	require.NoError(t, err)
	assert.Equal(t, 3, n, "files and links are counted, directories are not")
	assert.FileExists(t, filepath.Join(src, "s1.jsonl"))
	assert.FileExists(t, filepath.Join(src, "s1", "subagents", "agent-1.jsonl"))
	assert.FileExists(t, filepath.Join(src, "s1", "tool-results", "result-1.txt"))
}

func TestFill_RefusesAnotherDirsArchive(t *testing.T) {
	src := chatTree(t)
	zipPath := snapshotted(t, src)
	other := filepath.Join(t.TempDir(), "-wt-u-other-18be000000000002")

	_, err := Fill(zipPath, other)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "holds")
	assert.NoDirExists(t, other)
}

func TestFill_RejectsEscapes(t *testing.T) {
	outside := t.TempDir()
	for _, tc := range []struct {
		name    string
		entries []handEntry
	}{
		{"dot-dot entry", []handEntry{{name: "../x", body: "evil"}}},
		{"absolute entry", []handEntry{{name: "/x", body: "evil"}}},
		{"file beneath an outside symlink", []handEntry{
			{name: "link", body: outside, symlink: true},
			{name: "link/x", body: "evil"},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := t.TempDir()
			dir := filepath.Join(parent, "-wt-x")
			zipPath := handZip(t, "-wt-x", tc.entries)

			_, err := Fill(zipPath, dir)

			require.Error(t, err)
			assert.NoFileExists(t, filepath.Join(parent, "x"))
			assert.NoFileExists(t, filepath.Join(outside, "x"))
		})
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `CGO_ENABLED=0 go test ./session/claudetmp -run 'TestSnapshot|TestFill' 2>&1 | head -20`
Expected: build failure, `undefined: Snapshot`, `undefined: Fill`, `m.CarriedForward undefined`.

### A2. Implement

- [ ] **Step 3: Extend `Manifest`, `Result` and `addTree` in `session/claudetmp/archive.go`**

In `Manifest`, after `SkippedCaches`:

```go
	// CarriedForward names the entries a Snapshot copied from the previous
	// zip because their source was gone: kept, never dropped.
	CarriedForward []string `json:"carried_forward,omitempty"`
```

In `Result`, after `CacheBytes`:

```go
	Carried    int   // entries a Snapshot kept from the previous zip
```

Give `addTree` a `seen` map that, when non-nil, records every entry name it writes (directories with their trailing `/`). Replace the function with:

```go
// addTree adds src's content to zw, named by slash paths relative to src.
// WalkDir never follows symlinks. When seen is non-nil, every entry name
// written is recorded in it (a directory's with its trailing "/").
func addTree(zw *zip.Writer, src string, m *Manifest, res *Result, seen map[string]bool) error {
	note := func(name string) {
		if seen != nil {
			seen[name] = true
		}
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == src {
			return nil
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch mode := info.Mode(); {
		case mode&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			hdr := &zip.FileHeader{Name: name, Method: zip.Store, Modified: info.ModTime()}
			hdr.SetMode(mode)
			w, err := zw.CreateHeader(hdr)
			if err != nil {
				return err
			}
			note(name)
			_, err = io.WriteString(w, target)
			return err
		case mode.IsDir():
			if isCacheDir(p) {
				n := treeSize(p)
				m.SkippedCaches = append(m.SkippedCaches, SkippedCache{Path: name, Bytes: n})
				res.CacheBytes += n
				return fs.SkipDir
			}
			hdr := &zip.FileHeader{Name: name + "/", Modified: info.ModTime()}
			hdr.SetMode(mode)
			if _, err := zw.CreateHeader(hdr); err != nil {
				return err
			}
			note(name + "/")
			return nil
		case mode.IsRegular():
			if err := addFile(zw, p, name, info, res); err != nil {
				return err
			}
			note(name)
			return nil
		default: // sockets, FIFOs, devices
			return nil
		}
	})
}
```

and in `Archive`, change the call to `addTree(zw, src, &m, &res, nil)`.

- [ ] **Step 4: Create `session/claudetmp/snapshot.go`**

```go
package claudetmp

import (
	"archive/zip"
	"compress/flate"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/log"
)

// ChatArchiveDir is where configDir's chat archives live by default.
func ChatArchiveDir(configDir string) string {
	return filepath.Join(configDir, "archive", "chats")
}

// snapshotSlack is how long before its walk began a snapshot's zip is
// stamped. File times come from a coarse clock (a kernel tick, or whole
// seconds on some filesystems), so a write during the walk can carry a time
// slightly before the walk's start; stamping the zip earlier still makes
// every such write read as newer at the next Snapshot. The cost is at most
// one redundant snapshot of a file written just before a walk.
const snapshotSlack = 2 * time.Second

// Snapshot copies src, a Claude project dir holding a session's chats, into
// zipPath (mode 0600, its dir 0700), and never touches src. It reports
// whether it wrote a zip.
//
// When zipPath exists and nothing under src (src included) was modified
// after the zip's own mtime, it writes nothing. Otherwise it writes a fresh
// zip: src's content, walked as Archive walks it, then every entry of the
// previous zip that src no longer has (Claude's cleanup deleted the file),
// copied across without recompression and named in m.CarriedForward. A
// file on disk always replaces its archived copy, and nothing an earlier
// snapshot held is ever dropped. m.DirName, m.Source and m.ArchivedAt are
// filled in when empty.
//
// The zip is written to a partial file of its own and renamed into place
// once complete and synced, as Archive does; its mtime is then set to
// snapshotSlack before the walk began. A previous zip that cannot be read
// is moved aside to <name>.<UTC timestamp>.zip and kept, and the new zip
// starts fresh. On any error the partial file is removed and the previous
// zip is left as it was (or where it was moved).
func Snapshot(src, zipPath string, m Manifest) (Result, bool, error) {
	start := time.Now()
	if fi, err := os.Lstat(src); err != nil {
		return Result{}, false, fmt.Errorf("read %s: %w", src, err)
	} else if !fi.IsDir() {
		return Result{}, false, fmt.Errorf("%s is not a directory", src)
	}
	dir := filepath.Dir(zipPath)
	var prev *zip.ReadCloser
	if zi, err := os.Lstat(zipPath); err == nil {
		if !modifiedSince(src, zi.ModTime()) {
			return Result{}, false, nil
		}
		if prev, err = zip.OpenReader(zipPath); err != nil {
			prev = nil
			name := strings.TrimSuffix(filepath.Base(zipPath), ".zip")
			if err := os.Rename(zipPath, demotedPath(dir, name, zi.ModTime())); err != nil {
				return Result{}, false, fmt.Errorf("move the unreadable %s aside: %w", zipPath, err)
			}
		}
	}
	closePrev := func() {
		if prev != nil {
			_ = prev.Close()
			prev = nil
		}
	}
	defer closePrev()

	if m.DirName == "" {
		m.DirName = filepath.Base(src)
	}
	if m.Source == "" {
		m.Source = src
	}
	if m.ArchivedAt.IsZero() {
		m.ArchivedAt = start.UTC()
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Result{}, false, fmt.Errorf("create the archive dir: %w", err)
	}
	f, partial, err := openArchiveFile(dir, filepath.Base(zipPath)+".*.partial")
	if err != nil {
		return Result{}, false, fmt.Errorf("create a partial file for %s: %w", zipPath, err)
	}
	fail := func(err error) (Result, bool, error) {
		_ = f.Close()
		_ = os.Remove(partial)
		return Result{}, false, err
	}
	var res Result
	zw := zip.NewWriter(f)
	zw.RegisterCompressor(zip.Deflate, func(w io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(w, flate.BestSpeed)
	})
	seen := map[string]bool{}
	if err := addTree(zw, src, &m, &res, seen); err != nil {
		return fail(fmt.Errorf("archive %s: %w", src, err))
	}
	if prev != nil {
		for _, e := range prev.File {
			if e.Name == ManifestName || seen[e.Name] {
				continue
			}
			if err := zw.Copy(e); err != nil {
				return fail(fmt.Errorf("carry %q forward: %w", e.Name, err))
			}
			seen[e.Name] = true
			m.CarriedForward = append(m.CarriedForward, e.Name)
		}
	}
	res.Carried = len(m.CarriedForward)
	if err := addManifest(zw, m); err != nil {
		return fail(fmt.Errorf("write the manifest: %w", err))
	}
	if err := zw.Close(); err != nil {
		return fail(fmt.Errorf("finish %s: %w", partial, err))
	}
	if err := f.Sync(); err != nil {
		return fail(fmt.Errorf("sync %s: %w", partial, err))
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(partial)
		return Result{}, false, fmt.Errorf("close %s: %w", partial, err)
	}
	closePrev() // Windows renames over no open file
	if err := os.Rename(partial, zipPath); err != nil {
		_ = os.Remove(partial)
		return Result{}, false, fmt.Errorf("move %s into place: %w", zipPath, err)
	}
	syncDir(dir)
	stamp := start.Add(-snapshotSlack)
	if err := os.Chtimes(zipPath, stamp, stamp); err != nil {
		// The zip is complete. Its rename time stands in, so a write during
		// the walk may wait for the next change to be copied.
		log.For("claudetmp").Warn("claudetmp.snapshot_stamp_failed", "zip", zipPath, "err", err.Error())
	}
	if fi, err := os.Lstat(zipPath); err == nil {
		res.BytesOut = fi.Size()
	}
	return res, true, nil
}

// modifiedSince reports whether dir or anything under it was modified after
// t. A deletion counts: it changes its directory's mtime. It fails open: an
// entry it cannot read counts as modified, so the snapshot that follows
// reports the error.
func modifiedSince(dir string, t time.Time) bool {
	changed := false
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.ModTime().After(t) {
			changed = true
			return fs.SkipAll
		}
		return nil
	})
	return changed || err != nil
}

// Fill extracts into dir every entry of the archive at zipPath that dir
// lacks, and returns how many files and links it wrote. It never
// overwrites, never deletes the zip, and leaves what it writes with the
// current time as its mtime, so Claude's age-based cleanup does not take a
// restored transcript straight away. dir's base name must be the
// manifest's dir_name: the archive holds that dir's content and no other's.
// dir is created (0700) when missing.
//
// Entries are written through an os.Root, which rejects "..", absolute
// paths and escapes through symlinks. An error stops it part way, and what
// it already wrote stays: it only filled gaps.
//
// No error names zipPath: the caller does (see Restore).
func Fill(zipPath, dir string) (int, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		var pe *fs.PathError
		if errors.As(err, &pe) {
			err = pe.Err
		}
		return 0, fmt.Errorf("open the archive: %w", err)
	}
	defer zr.Close()
	manifest, err := manifestEntry(&zr.Reader)
	if err != nil {
		return 0, err
	}
	m, err := readManifest(manifest)
	if err != nil {
		return 0, err
	}
	if m.DirName != filepath.Base(dir) {
		return 0, fmt.Errorf("the archive holds %q, not %q", m.DirName, filepath.Base(dir))
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, fmt.Errorf("create %s: %w", dir, err)
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		return 0, err
	}
	defer r.Close()
	n := 0
	for _, f := range zr.File {
		if f == manifest {
			continue
		}
		name := strings.TrimSuffix(f.Name, "/")
		if !fs.ValidPath(name) || name == "." {
			return n, fmt.Errorf("archive entry %q is not a relative path inside the archive", f.Name)
		}
		if _, err := r.Lstat(name); err == nil {
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			return n, fmt.Errorf("restore %q: %w", f.Name, err)
		}
		if parent := path.Dir(name); parent != "." {
			if err := r.MkdirAll(parent, 0o700); err != nil {
				return n, fmt.Errorf("restore %q: %w", f.Name, err)
			}
		}
		mode := f.Mode()
		switch {
		case mode&fs.ModeSymlink != 0:
			target, err := readEntry(f, 4096)
			if err != nil {
				return n, fmt.Errorf("restore %q: %w", f.Name, err)
			}
			if err := r.Symlink(string(target), name); err != nil {
				return n, fmt.Errorf("restore %q: %w", f.Name, err)
			}
			n++
		case mode.IsDir():
			if err := r.MkdirAll(name, 0o700); err != nil {
				return n, fmt.Errorf("restore %q: %w", f.Name, err)
			}
		case mode.IsRegular():
			if err := writeEntry(r, f, name); err != nil {
				return n, fmt.Errorf("restore %q: %w", f.Name, err)
			}
			if err := r.Chmod(name, mode.Perm()); err != nil {
				return n, fmt.Errorf("restore the mode of %q: %w", f.Name, err)
			}
			n++
		}
	}
	return n, nil
}
```

- [ ] **Step 5: Update the package doc in `session/claudetmp/locate.go`**

Replace the first paragraph of the package comment (`// Package claudetmp finds, archives and restores ... from outliving it.`) with:

```go
// Package claudetmp finds, archives and restores the per-session
// directories Claude Code keeps outside the worktree: its temp dir,
// <root>/<encoded cwd>/<session id>/{scratchpad,tasks}/, which Archive
// moves into a zip and Restore puts back (loom removes a session's worktree
// on pause and kill; this keeps the temp dir, on tmpfs often, from
// outliving it), and its project dir of chats,
// <config>/projects/<encoded cwd>/, which Snapshot copies and Fill fills
// back in, never moving or changing Claude's files. Both names use the
// same encoding.
```

Leave the rest of the comment (layout source, contract test, "No tmux, UI, app or config dependency.") as it is.

- [ ] **Step 6: Run the package's tests**

Run: `CGO_ENABLED=0 go test ./session/claudetmp`
Expected: `ok`. Every pre-existing `TestArchive_…`/`TestRestore_…` test still passes, which checks the `addTree` change.

- [ ] **Step 7: Mutation-check the carry-forward and the slack**

Temporarily replace the `seen[e.Name]` skip with `e.Name == ManifestName` only (so on-disk files are written twice). `TestSnapshot_CarriesForwardWhatTheSourceLost` must fail on "the file on disk wins". Then temporarily change `stamp := start.Add(-snapshotSlack)` to `stamp := time.Now()`, and confirm `TestSnapshot_ANewerFileAnywhereTriggersAWrite` still passes (it should: the slack guards a sub-tick race the test can't force). Revert both.

- [ ] **Step 8: Format, vet, commit**

```bash
gofmt -w session/claudetmp/archive.go session/claudetmp/snapshot.go session/claudetmp/snapshot_test.go session/claudetmp/locate.go
CGO_ENABLED=0 go vet ./session/claudetmp
git add -- session/claudetmp/archive.go session/claudetmp/snapshot.go session/claudetmp/snapshot_test.go session/claudetmp/locate.go
git commit -m "feat(claudetmp): snapshot a dir without moving it, and fill back what it lost" -- session/claudetmp/archive.go session/claudetmp/snapshot.go session/claudetmp/snapshot_test.go session/claudetmp/locate.go
```

(End the message with the session's attribution lines.)

---

## Package B: `session` wiring

**Files:**
- Create: `session/claude_chat.go`
- Test: `session/claude_chat_test.go`
- Modify: `session/instance.go` (Pause ~1417, Kill ~1126, Resume ~1556 and ~1614, Resume's doc ~1440)
- Modify: `session/claude_tmp_realclaude_test.go`
- Modify: `session/CLAUDE.md`, `session/README.md`

Facts the code relies on:
- `Instance.ClaudeSession()` returns the transcript path from the latest parent `SessionStart` (persisted). Kill and Pause don't clear it.
- `bestEffortAccountDir(name)` (`session/account_env.go`) is an account's `CLAUDE_CONFIG_DIR`, or "" for the default account. `account.MainDir(account.Identity{})` is `$CLAUDE_CONFIG_DIR`, else `~/.claude`. `session` already imports `account`.
- Both Resume launch paths reach `recoveryLaunch` (`session/subagent_hooks.go`) through `finishResume` → `startFreshWithRecovery`. `recoveryLaunch` calls `BuildResumeCommand`, which picks `--resume <id>` only when the transcript file exists. So the restore must run before `finishResume`, which is where `restoreClaudeTemp` already runs.
- `TestMain` (`session/session_test.go`) isolates `CLAUDE_CONFIG_DIR` and `CLAUDE_CODE_TMPDIR`. Claude's temp root is absent there, so the scratchpad archive is a no-op unless a test makes one.
- Test helpers in the package that this package reuses: `newTestPausableInstance`, `newTestPausableInstanceWithExec`, `newTickPausedInstance`, `newTestStartedInstance`, `withConfigDir`, `manifestOf`, `zipsIn`, `setGlobalArchiveDir`, `sweepWorkspace`, `backdate(t, dir, skip)` (backdates a tree 48h), `fakeTmuxServer.launchArgs`, `newRecoverySession`, `OnlyNotice`.

### B1. Write the failing tests

- [ ] **Step 1: Create `session/claude_chat_test.go`**

```go
package session

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session/claudetmp"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// chatID is a well-formed Claude session ID: BuildResumeCommand resumes
// nothing else.
const chatID = "8c634184-0fe5-4b62-b437-8f364eeeefcc"

// chatProjects points Claude's main config dir at a fresh one for this test
// and returns its projects dir.
func chatProjects(t *testing.T) string {
	t.Helper()
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	projects := filepath.Join(cfg, "projects")
	require.NoError(t, os.Mkdir(projects, 0o700))
	return projects
}

// chatFor writes the project dir Claude keeps for a session whose cwd is
// wt, as a finished session leaves it (a transcript and a subagent's, all
// old), and returns the transcript's path.
func chatFor(t *testing.T, projects, wt string) string {
	t.Helper()
	dir := filepath.Join(projects, claudetmp.Names(wt)[0].Value)
	sub := filepath.Join(dir, chatID, "subagents")
	require.NoError(t, os.MkdirAll(sub, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "agent-1.jsonl"), []byte("sub\n"), 0o600))
	transcript := filepath.Join(dir, chatID+".jsonl")
	require.NoError(t, os.WriteFile(transcript, []byte("turn 1\n"), 0o600))
	backdate(t, dir, "")
	return transcript
}

// chatZips lists the chat archives in inst's own config folder.
func chatZips(t *testing.T, inst *Instance) []string {
	t.Helper()
	return zipsIn(t, claudetmp.ChatArchiveDir(inst.ConfigDir))
}

// zipNames lists zipPath's entry names.
func zipNames(t *testing.T, zipPath string) []string {
	t.Helper()
	zr, err := zip.OpenReader(zipPath)
	require.NoError(t, err)
	defer zr.Close()
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	return names
}

// withChat gives inst, a pausable fixture, a chat under projects and the
// transcript path its hooks would have reported. It returns that path.
func withChat(t *testing.T, inst *Instance, projects string) string {
	t.Helper()
	withConfigDir(inst)
	transcript := chatFor(t, projects, inst.getGitWorktree().GetWorktreePath())
	inst.claude = claudeState{sessionID: chatID, transcriptPath: transcript}
	return transcript
}

func TestPause_SnapshotsTheChatAndLeavesClaudesCopy(t *testing.T) {
	projects := chatProjects(t)
	inst := newTestPausableInstance(t)
	transcript := withChat(t, inst, projects)
	wt := inst.getGitWorktree().GetWorktreePath()

	require.NoError(t, inst.Pause(nil))

	assert.FileExists(t, transcript, "loom never moves Claude's files")
	zips := chatZips(t, inst)
	require.Len(t, zips, 1)
	m := manifestOf(t, zips[0])
	assert.Equal(t, "pause", m.Reason)
	assert.Equal(t, wt, m.Worktree)
	assert.Subset(t, zipNames(t, zips[0]), []string{chatID + ".jsonl", chatID + "/subagents/agent-1.jsonl"})
}

func TestKill_SnapshotsTheChat(t *testing.T) {
	projects := chatProjects(t)
	inst := newTestPausableInstance(t)
	transcript := withChat(t, inst, projects)

	require.NoError(t, inst.Kill())

	assert.FileExists(t, transcript)
	zips := chatZips(t, inst)
	require.Len(t, zips, 1)
	assert.Equal(t, "kill", manifestOf(t, zips[0]).Reason)
}

// TestKill_FindsTheChatWithoutATranscriptPath: no hook reported one (an
// untracked launch, or a session older than the hooks), so the dir is
// found by the worktree's encoding under the main config dir.
func TestKill_FindsTheChatWithoutATranscriptPath(t *testing.T) {
	projects := chatProjects(t)
	inst := newTestPausableInstance(t)
	withConfigDir(inst)
	chatFor(t, projects, inst.getGitWorktree().GetWorktreePath())

	require.NoError(t, inst.Kill())

	assert.Len(t, chatZips(t, inst), 1)
}

// TestKill_OfAPausedSessionWritesNoNewSnapshot: nothing changed since the
// pause's snapshot, so the kill costs a stat walk and no zip.
func TestKill_OfAPausedSessionWritesNoNewSnapshot(t *testing.T) {
	projects := chatProjects(t)
	inst := newTestPausableInstance(t)
	withChat(t, inst, projects)
	require.NoError(t, inst.Pause(nil))
	zips := chatZips(t, inst)
	require.Len(t, zips, 1)
	before, err := os.Stat(zips[0])
	require.NoError(t, err)

	require.NoError(t, inst.Kill())

	after, err := os.Stat(zips[0])
	require.NoError(t, err)
	assert.Equal(t, before.ModTime(), after.ModTime())
	assert.Equal(t, "pause", manifestOf(t, zips[0]).Reason)
}

// TestKill_SnapshotsNoWorkspaceTerminalChat: a workspace terminal runs in
// the repository root, whose chats the user's own sessions share.
func TestKill_SnapshotsNoWorkspaceTerminalChat(t *testing.T) {
	projects := chatProjects(t)
	inst := newTestStartedInstance(t)
	inst.Path = t.TempDir()
	inst.ConfigDir = t.TempDir()
	transcript := chatFor(t, projects, inst.Path)
	inst.claude = claudeState{sessionID: chatID, transcriptPath: transcript}

	require.NoError(t, inst.Kill())

	assert.Empty(t, chatZips(t, inst))
}

func TestChatArchiveFailure_FailsNeitherPauseNorKill(t *testing.T) {
	for _, op := range []struct {
		name string
		run  func(*Instance) error
	}{
		{"pause", func(i *Instance) error { return i.Pause(nil) }},
		{"kill", (*Instance).Kill},
	} {
		t.Run(op.name, func(t *testing.T) {
			projects := chatProjects(t)
			inst := newTestPausableInstance(t)
			transcript := withChat(t, inst, projects)
			// A file where the chat archive dir must go makes every snapshot fail.
			require.NoError(t, os.MkdirAll(filepath.Join(inst.ConfigDir, "archive"), 0o700))
			require.NoError(t, os.WriteFile(claudetmp.ChatArchiveDir(inst.ConfigDir), []byte("in the way"), 0o600))

			require.NoError(t, op.run(inst))
			assert.FileExists(t, transcript)
		})
	}
}

// TestResume_RestoresADeletedTranscriptAndResumesIt: Claude deleted the
// paused session's transcript (a month without a write). Resume puts it
// back before the launch command is built, so the relaunch resumes the
// conversation instead of starting over with --continue.
func TestResume_RestoresADeletedTranscriptAndResumesIt(t *testing.T) {
	t.Run("rebuild", func(t *testing.T) {
		projects := chatProjects(t)
		srv := &fakeTmuxServer{}
		inst := newTestPausableInstanceWithExec(t, srv.runner())
		inst.program = "claude"
		orig := newRecoverySession
		newRecoverySession = func(name, program string, env ...string) *tmux.Session {
			return tmux.NewSessionWithDeps(name, program, srv, srv.runner(), env...)
		}
		t.Cleanup(func() { newRecoverySession = orig })
		transcript := withChat(t, inst, projects)
		require.NoError(t, inst.Pause(nil))
		require.NoError(t, os.Remove(transcript)) // Claude's cleanup

		require.NoError(t, inst.Resume(nil))

		data, err := os.ReadFile(transcript)
		require.NoError(t, err)
		assert.Equal(t, "turn 1\n", string(data))
		require.Len(t, srv.launchArgs(), 1)
		assert.Contains(t, strings.Join(srv.launchArgs()[0], " "), "--resume "+chatID)
	})
	t.Run("relaunch in place", func(t *testing.T) {
		projects := chatProjects(t)
		inst, srv := newTickPausedInstance(t)
		transcript := withChat(t, inst, projects)
		inst.archiveChat(inst.getGitWorktree().GetWorktreePath(), "pause")
		require.NoError(t, os.Remove(transcript))

		require.NoError(t, inst.Resume(nil))

		assert.FileExists(t, transcript)
		require.Len(t, srv.launchArgs(), 1)
		assert.Contains(t, strings.Join(srv.launchArgs()[0], " "), "--resume "+chatID)
	})
}

// TestResume_WithNoChatArchiveStillContinues: the other side of the
// restore. No zip, no transcript: --continue, as before this feature.
func TestResume_WithNoChatArchiveStillContinues(t *testing.T) {
	projects := chatProjects(t)
	inst, srv := newTickPausedInstance(t)
	transcript := withChat(t, inst, projects)
	require.NoError(t, os.Remove(transcript))

	require.NoError(t, inst.Resume(nil))

	assert.NoFileExists(t, transcript)
	require.Len(t, srv.launchArgs(), 1)
	launch := strings.Join(srv.launchArgs()[0], " ")
	assert.NotContains(t, launch, "--resume")
	assert.Contains(t, launch, "--continue")
}

func TestResume_AChatRestoreFailureIsANoticeAndTheLaunchProceeds(t *testing.T) {
	projects := chatProjects(t)
	inst, srv := newTickPausedInstance(t)
	transcript := withChat(t, inst, projects)
	require.NoError(t, os.Remove(transcript))
	name := claudetmp.ArchiveName(filepath.Base(filepath.Dir(transcript)), claudetmp.WorktreePrefixes(inst.ConfigDir))
	adir := claudetmp.ChatArchiveDir(inst.ConfigDir)
	require.NoError(t, os.MkdirAll(adir, 0o700))
	zipPath := filepath.Join(adir, name+".zip")
	require.NoError(t, os.WriteFile(zipPath, []byte("not a zip"), 0o600))

	err := inst.Resume(nil)

	n, ok := OnlyNotice(err)
	require.True(t, ok, "a failed restore never fails the resume: %v", err)
	assert.Contains(t, n.Error(), "couldn't restore the conversation")
	assert.Contains(t, n.Error(), zipPath)
	assert.FileExists(t, zipPath, "the archive is kept")
	require.Len(t, srv.launchArgs(), 1, "the launch went ahead")
}

func TestClaudeChatArchiveDir(t *testing.T) {
	t.Setenv(config.EnvGlobalDir, t.TempDir()) // no config.json: unset
	cfg := filepath.Join(t.TempDir(), "repo", ".loom")
	assert.Equal(t, claudetmp.ChatArchiveDir(cfg), ClaudeChatArchiveDir(cfg))

	setGlobalArchiveDir(t, t.TempDir())
	assert.Equal(t, filepath.Join(ClaudeTmpArchiveDir(cfg), "chats"), ClaudeChatArchiveDir(cfg),
		"beside the workspace's temp-dir archives")
}

func TestPause_SnapshotsTheChatIntoTheConfiguredDir(t *testing.T) {
	setGlobalArchiveDir(t, t.TempDir())
	projects := chatProjects(t)
	inst := newTestPausableInstance(t)
	withChat(t, inst, projects)

	require.NoError(t, inst.Pause(nil))

	assert.Len(t, zipsIn(t, ClaudeChatArchiveDir(inst.ConfigDir)), 1)
	assert.Empty(t, chatZips(t, inst), "not in the workspace's own folder")
}

// TestResume_FindsAChatArchivedBeforeTheSettingChanged: a session paused
// under the default location and resumed after claude_tmp_archive_dir was
// set still gets its transcript back.
func TestResume_FindsAChatArchivedBeforeTheSettingChanged(t *testing.T) {
	projects := chatProjects(t)
	inst, _ := newTickPausedInstance(t)
	transcript := withChat(t, inst, projects)
	inst.archiveChat(inst.getGitWorktree().GetWorktreePath(), "pause")
	require.Len(t, chatZips(t, inst), 1)
	setGlobalArchiveDir(t, t.TempDir())
	require.NoError(t, os.Remove(transcript))

	require.NoError(t, inst.Resume(nil))

	assert.FileExists(t, transcript)
}

func TestClaudeProjectsRoots(t *testing.T) {
	mainDir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(mainDir, "projects"), 0o700))
	linked := t.TempDir() // projects/ is a link into the main dir, as account.Sync makes it
	require.NoError(t, os.Symlink(filepath.Join(mainDir, "projects"), filepath.Join(linked, "projects")))
	own := t.TempDir() // projects/ is a real dir of its own
	require.NoError(t, os.Mkdir(filepath.Join(own, "projects"), 0o700))
	none := t.TempDir() // Claude never ran here

	roots := ClaudeProjectsRoots(mainDir, map[string]string{"linked": linked, "own": own, "none": none})

	mainP, err := filepath.EvalSymlinks(filepath.Join(mainDir, "projects"))
	require.NoError(t, err)
	ownP, err := filepath.EvalSymlinks(filepath.Join(own, "projects"))
	require.NoError(t, err)
	assert.Equal(t, []string{mainP, ownP}, roots)
	assert.Empty(t, ClaudeProjectsRoots("", nil))
}

func TestSweepClaudeChats_SnapshotsDeadAndPausedChatsOnly(t *testing.T) {
	projects := t.TempDir()
	cfg, wtDir := sweepWorkspace(t, t.TempDir(), "repo")
	goneWT := filepath.Join(wtDir, "gone_18be000000000001")
	pausedWT := filepath.Join(wtDir, "paused_18be000000000002")
	liveWT := filepath.Join(wtDir, "live_18be000000000003")
	require.NoError(t, os.Mkdir(liveWT, 0o755))
	noStamp := filepath.Join(cfg, "worktrees-old")
	otherCfg, otherWT := sweepWorkspace(t, t.TempDir(), "other")
	otherGone := filepath.Join(otherWT, "gone_18be000000000005")
	for _, wt := range []string{goneWT, pausedWT, liveWT, noStamp, otherGone} {
		chatFor(t, projects, wt)
	}
	running := map[string]bool{liveWT: true}
	others := []string{cfg, otherCfg}

	n := SweepClaudeChats(cfg, []string{projects}, running, others)

	assert.Equal(t, 2, n)
	var names []string
	for _, z := range zipsIn(t, claudetmp.ChatArchiveDir(cfg)) {
		names = append(names, filepath.Base(z))
		assert.Equal(t, "sweep", manifestOf(t, z).Reason)
	}
	assert.ElementsMatch(t, []string{"u-gone-18be000000000001.zip", "u-paused-18be000000000002.zip"}, names)
	for _, wt := range []string{goneWT, pausedWT} {
		assert.DirExists(t, filepath.Join(projects, claudetmp.Names(wt)[0].Value), "the sweep copies, never moves")
	}
	assert.Empty(t, zipsIn(t, claudetmp.ChatArchiveDir(otherCfg)), "another workspace's chats are its own sweep's")
	assert.Zero(t, SweepClaudeChats(cfg, []string{projects}, running, others), "an unchanged chat is not written again")
}

// TestSweepClaudeChats_CarriesForwardAfterClaudesCleanup: the deletion is
// a change, and the next snapshot keeps the deleted transcript.
func TestSweepClaudeChats_CarriesForwardAfterClaudesCleanup(t *testing.T) {
	projects := t.TempDir()
	cfg, wtDir := sweepWorkspace(t, t.TempDir(), "repo")
	transcript := chatFor(t, projects, filepath.Join(wtDir, "gone_18be000000000001"))
	require.Equal(t, 1, SweepClaudeChats(cfg, []string{projects}, nil, []string{cfg}))
	require.NoError(t, os.Remove(transcript))

	assert.Equal(t, 1, SweepClaudeChats(cfg, []string{projects}, nil, []string{cfg}))

	zips := zipsIn(t, claudetmp.ChatArchiveDir(cfg))
	require.Len(t, zips, 1)
	assert.Contains(t, zipNames(t, zips[0]), chatID+".jsonl", "the transcript Claude deleted stays archived")
}

func TestSweepClaudeChats_NeverTakesATruncatedName(t *testing.T) {
	projects := t.TempDir()
	cfg, wtDir := sweepWorkspace(t, t.TempDir(), "repo")
	prefix, truncated := claudetmp.DirName(filepath.Join(wtDir, strings.Repeat("d", 180), "gone_18be000000000001"))
	require.True(t, truncated)
	dir := filepath.Join(projects, prefix+"18be000000000001") // a hash that even looks like a timestamp
	require.NoError(t, os.Mkdir(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, chatID+".jsonl"), []byte("x"), 0o600))

	assert.Zero(t, SweepClaudeChats(cfg, []string{projects}, nil, nil))
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `CGO_ENABLED=0 go test ./session -run 'Chat|ClaudeProjectsRoots|TranscriptPath|WorkspaceTerminalChat' 2>&1 | head -20`
Expected: build failure, with `undefined: SweepClaudeChats`, `ClaudeChatArchiveDir`, `ClaudeProjectsRoots`, and `inst.archiveChat undefined`.

### B2. Implement

- [ ] **Step 3: Create `session/claude_chat.go`**

```go
package session

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session/claudetmp"
)

// ClaudeChatArchiveDir is where configDir's chat archives go: a "chats"
// folder beside its Claude temp-dir archives when the global config.json
// sets claude_tmp_archive_dir (see ClaudeTmpArchiveDir), else
// claudetmp.ChatArchiveDir in the workspace's own config folder. Every
// snapshot, restore and sweep asks here.
func ClaudeChatArchiveDir(configDir string) string {
	root := configuredArchiveRoot()
	if root == "" {
		return claudetmp.ChatArchiveDir(configDir)
	}
	return filepath.Join(root, archiveSubdir(configDir), "chats")
}

// ClaudeProjectsRoots lists the dirs Claude writes chats into: mainDir's
// projects/, then each account dir's in name order, symlinks resolved,
// without duplicates and leaving out any that don't exist. An account's
// projects/ is usually a link into mainDir's (account.Sync), so it adds
// nothing; one that is a real dir holds chats nowhere else.
func ClaudeProjectsRoots(mainDir string, accountDirs map[string]string) []string {
	dirs := []string{mainDir}
	for _, name := range slices.Sorted(maps.Keys(accountDirs)) {
		dirs = append(dirs, accountDirs[name])
	}
	var out []string
	for _, d := range dirs {
		if d == "" {
			continue
		}
		p, err := filepath.EvalSymlinks(filepath.Join(d, "projects"))
		if err != nil || slices.Contains(out, p) {
			continue
		}
		if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
			continue
		}
		out = append(out, p)
	}
	return out
}

// claudeProjectsDir is the projects dir of the Claude config dir a session
// on accountName ("" for the default account) writes its chats into.
func claudeProjectsDir(accountName string) string {
	dir := bestEffortAccountDir(accountName)
	if dir == "" {
		dir = account.MainDir(account.Identity{})
	}
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "projects")
}

// chatDir returns the Claude project dir holding a session's chats: the
// dir of transcriptPath when Claude reported one and the dir is there,
// else the dir under projectsDir that worktreePath encodes to
// (claudetmp.Locate, which fails closed).
func chatDir(transcriptPath, projectsDir, worktreePath string) (string, bool) {
	if transcriptPath != "" {
		d := filepath.Dir(transcriptPath)
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			return d, true
		}
	}
	if projectsDir == "" || worktreePath == "" {
		return "", false
	}
	return claudetmp.Locate(projectsDir, worktreePath)
}

// archiveClaudeChatFn is how Pause and Kill reach archiveClaudeChat, so a
// test can watch it.
var archiveClaudeChatFn = archiveClaudeChat

// archiveClaudeChat snapshots src, a session's Claude project dir, as
// <chat archive dir>/<name>.zip (claudetmp.Snapshot), where name is the
// scratchpad zip's for the same dir, and logs the outcome. It reports
// whether it wrote a zip: an unchanged chat writes none.
func archiveClaudeChat(configDir, src, worktreePath, reason string) (bool, error) {
	dir := ClaudeChatArchiveDir(configDir)
	name := claudetmp.ArchiveName(filepath.Base(src), claudetmp.WorktreePrefixes(configDir))
	zipPath := filepath.Join(dir, name+".zip")
	res, wrote, err := claudetmp.Snapshot(src, zipPath, claudetmp.Manifest{Worktree: worktreePath, Reason: reason})
	lg := log.For("claudechat")
	if err != nil {
		lg.Warn("claudechat.archive_failed", "source", src, "zip", zipPath, "reason", reason, "err", err.Error())
		return false, err
	}
	if wrote {
		lg.Info("claudechat.archived", "source", src, "zip", zipPath, "reason", reason,
			"bytes_in", res.BytesIn, "bytes_out", res.BytesOut, "carried_forward", res.Carried,
			"archive_dir_bytes", claudetmp.DirSize(dir))
	}
	return wrote, nil
}

// archiveChat snapshots the chats of this session, whose worktree was
// worktreePath, for Pause and Kill. Best effort: a failure is logged and
// the next sweep retries. A no-op when no project dir is found, so
// non-Claude programs and sessions that never sent a message need no
// special case.
func (i *Instance) archiveChat(worktreePath, reason string) {
	if i.ConfigDir == "" || worktreePath == "" {
		return
	}
	_, transcript := i.ClaudeSession()
	src, ok := chatDir(transcript, claudeProjectsDir(i.Account()), worktreePath)
	if !ok {
		return
	}
	_, _ = archiveClaudeChatFn(i.ConfigDir, src, worktreePath, reason)
}

// restoreChat is restoreClaudeChat for this session's recorded transcript.
func (i *Instance) restoreChat() error {
	_, transcript := i.ClaudeSession()
	return restoreClaudeChat(i.ConfigDir, transcript)
}

// restoreClaudeChat puts back a transcript Claude deleted while its
// session was paused (cleanupPeriodDays counts from the last write), from
// the session's chat archive, before the agent relaunches: BuildResumeCommand
// resumes the conversation only when the file is there. It looks in the
// archive dir in effect, then in the workspace's own folder (a chat
// archived before claude_tmp_archive_dir was set). A no-op when no
// transcript is recorded, the file exists or can't be checked, or no
// archive is found. It fills in only what is missing (claudetmp.Fill) and
// keeps the zip. A failure returns what the user must hear; the launch
// goes ahead and starts over with --continue.
func restoreClaudeChat(configDir, transcriptPath string) error {
	if configDir == "" || transcriptPath == "" {
		return nil
	}
	if _, err := os.Lstat(transcriptPath); !errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	dir := filepath.Dir(transcriptPath)
	name := claudetmp.ArchiveName(filepath.Base(dir), claudetmp.WorktreePrefixes(configDir))
	dirs := []string{ClaudeChatArchiveDir(configDir)}
	if own := claudetmp.ChatArchiveDir(configDir); own != dirs[0] {
		dirs = append(dirs, own)
	}
	var zipPath string
	for _, d := range dirs {
		p := filepath.Join(d, name+".zip")
		if fi, err := os.Lstat(p); err == nil && fi.Mode().IsRegular() {
			zipPath = p
			break
		}
	}
	if zipPath == "" {
		return nil
	}
	lg := log.For("claudechat")
	n, err := claudetmp.Fill(zipPath, dir)
	if err != nil {
		lg.Warn("claudechat.restore_failed", "zip", zipPath, "transcript", transcriptPath, "err", err.Error())
		return fmt.Errorf("couldn't restore the conversation: %w; archive at %s", err, zipPath)
	}
	lg.Info("claudechat.restored", "zip", zipPath, "transcript", transcriptPath, "entries", n)
	return nil
}

// SweepClaudeChats snapshots (reason "sweep") the chats of configDir's
// sessions that are not running: dead ones (killed, orphaned, or gone
// before loom archived chats) and paused ones, so Claude's cleanup, which
// deletes a transcript a month after its last write, never takes the only
// copy. roots are Claude's projects dirs (ClaudeProjectsRoots). It returns
// how many zips it wrote, and first removes the ".partial" files older
// than sweepPartialAge that an interrupted snapshot left.
//
// A dir under a root is snapshotted when all of these hold:
//  1. Its name starts with configDir's encoded worktrees prefix and with no
//     other known config dir's (otherConfigDirs; SweepClaudeTemp's check 1).
//  2. Its last "-" segment is a plausible worktree timestamp and the name is
//     not truncated (SweepClaudeTemp's check 2).
//  3. No path in running encodes to it: a running session's chat changes
//     with every turn, and Pause or Kill snapshots it when it stops.
//  4. Something in it is newer than its archive (claudetmp.Snapshot).
//
// Nothing is deleted, so unlike SweepClaudeTemp it needs no on-disk recheck
// and no quiet period: snapshotting a dir a session is still writing costs
// a zip at worst, and the next snapshot replaces it.
func SweepClaudeChats(configDir string, roots []string, running map[string]bool, otherConfigDirs []string) int {
	if configDir == "" {
		return 0
	}
	lg := log.For("claudechat")
	if n := claudetmp.PurgePartials(ClaudeChatArchiveDir(configDir), sweepPartialAge); n > 0 {
		lg.Info("claudechat.partials_purged", "dir", ClaudeChatArchiveDir(configDir), "removed", n)
	}
	own := claudetmp.WorktreePrefixes(configDir)
	foreign := foreignWorktreePrefixes(configDir, otherConfigDirs)
	var runningNames []claudetmp.Name
	for p := range running {
		runningNames = append(runningNames, claudetmp.Names(p)...)
	}
	wrote := 0
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			lg.Debug("claudechat.sweep_read_failed", "root", root, "err", err.Error())
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if !e.IsDir() || !sweepableClaudeTemp(name, own, foreign) || matchesAnyName(runningNames, name) {
				continue
			}
			if ok, _ := archiveClaudeChat(configDir, filepath.Join(root, name), "", "sweep"); ok {
				wrote++
			}
		}
	}
	if wrote > 0 {
		lg.Info("claudechat.sweep_done", "config_dir", configDir, "archived", wrote)
	}
	return wrote
}
```

- [ ] **Step 4: Wire Pause, Kill and Resume in `session/instance.go`**

Pause: replace

```go
	archiveClaudeTempFn(i.ConfigDir, gw.GetWorktreePath(), "pause")

	// Checkpoint: mark as Paused immediately after cleanup succeeds.
```

with

```go
	archiveClaudeTempFn(i.ConfigDir, gw.GetWorktreePath(), "pause")
	// Claude deletes a transcript a month after its last write, and a paused
	// session writes none: keep a copy for Resume to put back. A copy deletes
	// nothing, so unlike the archive above it has no ordering to keep.
	i.archiveChat(gw.GetWorktreePath(), "pause")

	// Checkpoint: mark as Paused immediately after cleanup succeeds.
```

Kill: replace

```go
	if gitWT != nil && !isWorkspaceTerm {
		archiveClaudeTempFn(i.ConfigDir, gitWT.GetWorktreePath(), "kill")
	}
	return NewNotice(notices...)
```

with

```go
	if gitWT != nil && !isWorkspaceTerm {
		archiveClaudeTempFn(i.ConfigDir, gitWT.GetWorktreePath(), "kill")
		i.archiveChat(gitWT.GetWorktreePath(), "kill")
	}
	return NewNotice(notices...)
```

Resume, relaunch in place: replace

```go
		// Before finishResume launches the agent, so Claude finds its
		// scratchpad. (Evaluated first: Go evaluates call arguments in
		// order, so this must not move into withNotices' argument list
		// after finishResume.)
		tmpNote := restoreClaudeTemp(i.ConfigDir, gw.GetWorktreePath())
		return withNotices(i.finishResume(saveState, ts, gw), note, tmpNote)
```

with

```go
		// Before finishResume launches the agent, so Claude finds its
		// scratchpad and recoveryLaunch its transcript. (Evaluated first: Go
		// evaluates call arguments in order, so these must not move into
		// withNotices' argument list after finishResume.)
		tmpNote := restoreClaudeTemp(i.ConfigDir, gw.GetWorktreePath())
		chatNote := i.restoreChat()
		return withNotices(i.finishResume(saveState, ts, gw), note, tmpNote, chatNote)
```

Resume, rebuild: replace

```go
	// Before finishResume launches the agent, so Claude finds its scratchpad.
	notes = append(notes, restoreClaudeTemp(i.ConfigDir, gw.GetWorktreePath()))
```

with

```go
	// Before finishResume launches the agent, so Claude finds its scratchpad
	// and recoveryLaunch its transcript.
	notes = append(notes, restoreClaudeTemp(i.ConfigDir, gw.GetWorktreePath()), i.restoreChat())
```

In Resume's doc comment, replace `could not restore Claude's\n// archived scratchpad,` with `could not restore Claude's\n// archived scratchpad or transcript,`.

- [ ] **Step 5: Run the new tests**

Run: `CGO_ENABLED=0 go test ./session -run 'Chat|ClaudeProjectsRoots|TranscriptPath|WorkspaceTerminalChat'`
Expected: `ok`.

If `TestResume_RestoresADeletedTranscriptAndResumesIt` finds no `--resume`, print `srv.launchArgs()` and check how `ApplyResumeFlag` (`session/agent/claude.go`) spells the flag. `TestRecoveryLaunch_ResumesAcrossAnAccountSwitch` asserts the same `"--resume "+id` form.

- [ ] **Step 6: Mutation-check the ordering and the guard**

Temporarily move `i.restoreChat()` in the rebuild path to after `finishResume`, as `notes = append(notes, ...)` following a `res := i.finishResume(...)`. The rebuild subtest must fail on `--resume`. Then temporarily drop `|| matchesAnyName(runningNames, name)` from `SweepClaudeChats`. `TestSweepClaudeChats_SnapshotsDeadAndPausedChatsOnly` must fail with 3 zips. Revert both.

- [ ] **Step 7: Extend the contract test in `session/claude_tmp_realclaude_test.go`**

`TestMain` points `CLAUDE_CONFIG_DIR` at an empty dir (since c241a4e), so the pane's Claude would start logged out. Hand it the developer's config dir.

Below the existing `t.Setenv(claudetmp.EnvTmpDir, tmp)`, add:

```go
	// TestMain points CLAUDE_CONFIG_DIR at an empty dir, where Claude would
	// start logged out: hand the pane the developer's.
	claudeConfig := os.Getenv("LOOM_TEST_CLAUDE_CONFIG_DIR")
	if claudeConfig == "" {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		claudeConfig = filepath.Join(home, ".claude")
	}
```

Change the `new-session` line to also pass it:

```go
	tm("new-session", "-d", "-s", "probe", "-x", "160", "-y", "45", "-c", cwd,
		"-e", claudetmp.EnvTmpDir+"="+tmp, "-e", "CLAUDE_CONFIG_DIR="+claudeConfig)
```

At the end of the test, add:

```go
	// Its chats: <config>/projects/<the same encoding>/<session id>.jsonl,
	// written with the first message, so there by now.
	projects := filepath.Join(claudeConfig, "projects")
	chat, ok := claudetmp.Locate(projects, cwd)
	require.True(t, ok, "Locate finds the chat dir under %s", projects)
	t.Cleanup(func() {
		_ = tmux.CommandOnSocket(ctx, sock, "kill-server").Run() // before Claude can write again
		_ = os.RemoveAll(chat)
	})
	assert.Equal(t, want, filepath.Base(chat), "Claude names the chat dir as it names the temp dir")
	transcripts, err := filepath.Glob(filepath.Join(chat, "*.jsonl"))
	require.NoError(t, err)
	assert.Len(t, transcripts, 1, "one transcript per session, at <dir>/<session id>.jsonl")
```

Update the test's doc comment. Its first sentence should read: "pins the layouts session/claudetmp assumes (Claude's temp root, its encoding of the physical cwd, and the chat dir under `<config>/projects` that uses the same encoding)". Replace "leaves entries in ~/.claude/projects" with "leaves a trust entry in the config dir's .claude.json (it removes the chat dir it creates); set LOOM_TEST_CLAUDE_CONFIG_DIR to use a config dir other than ~/.claude".

Compile it: `CGO_ENABLED=0 go vet ./session`. The test itself is opt-in and costs a few cents. Run it only if the user agrees: `LOOM_TEST_REAL_CLAUDE=1 CGO_ENABLED=0 go test ./session -run TestRealClaude_TempDirLayout -v`.

- [ ] **Step 8: Docs in `session/`**

`session/CLAUDE.md`: under `### Claude temp dirs and hooks`, rename the heading to `### Claude temp dirs, chats and hooks` and add, after the "Archive Claude's temp dirs" bullet:

```markdown
- **Copy Claude's chats, never move or edit them, and ask `ClaudeChatArchiveDir` for every chat archive path.** Pause and Kill snapshot the session's project dir (`claudetmp.Snapshot`), carrying forward every entry Claude's cleanup has since deleted; Resume fills a deleted transcript back in (`claudetmp.Fill`, never overwriting) before `finishResume`, so `recoveryLaunch` still finds it for `--resume`; the sweep skips running sessions. Claude deletes a transcript a month after its last write, so a snapshot that dropped carried entries overwrites the only copy, silently. **Enforced** by `TestSnapshot_CarriesForwardWhatTheSourceLost`, `TestResume_RestoresADeletedTranscriptAndResumesIt` and the `TestSweepClaudeChats_…` tests.
```

`session/README.md`:
- In the first paragraph, change "temp-dir archiving" to "temp-dir and chat archiving".
- After the `## Claude temp dirs` section's last bullet (the one ending in `-run TestRealClaude_TempDirLayout`.), add:

```markdown
## Claude chats

Claude keeps each session's conversation in `<config>/projects/<encoded cwd>/` (`<session id>.jsonl`, plus `<session id>/subagents/` and `<session id>/tool-results/`), using the temp dir's encoding, and deletes a transcript once it has gone `cleanupPeriodDays` (30 by default) without a write. `session/claude_chat.go` copies it, never moving it:

- Pause and Kill snapshot the dir named by the instance's recorded transcript path (`ClaudeSession`), else the one `claudetmp.Locate` finds under the session's account's `projects/` (`claudeProjectsDir`). Workspace terminals are skipped. A failure is logged as `claudechat.archive_failed` and fails neither.
- Archives are `<configDir>/archive/chats/<name>.zip`, or `<claude_tmp_archive_dir>/<encoded config dir>/chats/` (`ClaudeChatArchiveDir`), with `<name>` the scratchpad zip's. `claudetmp.Snapshot` writes one only when something in the dir is newer than the zip, and carries forward every entry whose source is gone, so nothing archived is dropped. They are never pruned.
- Resume (both launch paths) calls `restoreClaudeChat` before `finishResume`. When the recorded transcript is missing and an archive exists (in the dir in effect, then the default one), `claudetmp.Fill` writes the missing entries back without overwriting anything, and `BuildResumeCommand` then picks `--resume`. A failure is a `Notice`, and the launch continues with `--continue`.
- `SweepClaudeChats` runs in core's temp-dir sweep job (`core/claude_tmp.go`) over `ClaudeProjectsRoots` (the main config dir's `projects/` and any account's that is not a link into it). It snapshots dirs with the config dir's worktrees prefix and no other's, a timestamp suffix and an untruncated name, skipping running sessions' dirs. Dead and paused ones qualify.
- The contract test `TestRealClaude_TempDirLayout` also pins the chat dir's name and layout.
```

- In the persistent-state list, after the `archive/claude-tmp/` bullet, add:

```markdown
- `archive/chats/` (or `<claude_tmp_archive_dir>/<encoded config dir>/chats/`): one zip per session of its Claude chats, a copy refreshed on pause, kill and sweep, keeping whatever Claude has since deleted.
```

- In `### claudetmp`, after `` `Restore` (through `os.Root`). ``, add: `` `Snapshot` (zip a dir without touching it, carrying forward entries of the previous zip whose source is gone, the zip stamped two seconds before the walk began); `Fill` (extract what a dir lacks, never overwriting); `ChatArchiveDir`. ``

- [ ] **Step 9: Full package run, race, lint, commit**

```bash
CGO_ENABLED=0 go test ./session/...
CC=clang CGO_ENABLED=1 go test -race ./session/ ./session/claudetmp
gofmt -w session/claude_chat.go session/claude_chat_test.go session/instance.go session/claude_tmp_realclaude_test.go
CGO_ENABLED=0 go vet ./session/...
go run ./tools/claudemd
git add -- session/claude_chat.go session/claude_chat_test.go session/instance.go session/claude_tmp_realclaude_test.go session/CLAUDE.md session/README.md
git commit -m "feat(session): archive Claude chats on pause, kill and sweep; restore a deleted transcript on resume" -- session/claude_chat.go session/claude_chat_test.go session/instance.go session/claude_tmp_realclaude_test.go session/CLAUDE.md session/README.md
```

Expected: all `ok`. `tools/claudemd` reports no errors (the rule's test names must exist).

---

## Package C: `core`, `loom debug` and user docs

**Files:**
- Modify: `core/claude_tmp.go`
- Test: `core/claude_tmp_test.go`
- Modify: `main.go` (the `loom debug` block, ~line 304)
- Modify: `USAGE.md`, `docs/ARCHITECTURE.md`, `config/README.md`

Facts: `Model.MainConfigDir()` (`core/accounts.go`) is `account.MainDir(m.rcAuth.Identity)`. In `NewForTest` the identity is zero, so it falls back to `$CLAUDE_CONFIG_DIR`. `Model.accountDirs()` returns a fresh map. A `Job` reads no model state, so capture both on the model's goroutine and resolve the roots inside the job, since resolving them does filesystem I/O.

### C1. Write the failing tests

- [ ] **Step 1: Add to `core/claude_tmp_test.go`**

Add `"github.com/aidan-bailey/loom/session/claudetmp"` to the imports.

```go
// TestClaudeTmpSweep_SnapshotsTheRunningSet: the chat sweep leaves running
// sessions to Pause and Kill; paused ones are its to copy.
func TestClaudeTmpSweep_SnapshotsTheRunningSet(t *testing.T) {
	cfg := t.TempDir()
	mk := func(title string, status session.Status, wt string) *session.Instance {
		t.Helper()
		inst, err := session.FromInstanceData(session.InstanceData{
			SchemaVersion: session.CurrentSchemaVersion,
			Title:         title,
			Path:          t.TempDir(),
			Branch:        "u/" + title,
			Status:        status,
			Worktree: session.GitWorktreeData{
				RepoPath:     t.TempDir(),
				WorktreePath: wt,
				BranchName:   "u/" + title,
			},
		}, cfg)
		require.NoError(t, err)
		return inst
	}
	ws := NewWorkspace(WorkspaceParts{})
	ws.add(mk("paused", session.Paused, "/wt/paused_18be000000000001"))
	ws.add(mk("live", session.Running, "/wt/live_18be000000000002"))
	m := NewForTest(Options{})

	m.requestClaudeTmpSweep(ws, cfg)

	job := m.claudeTmpPending[cfg]
	assert.Equal(t, map[string]bool{"/wt/live_18be000000000002": true}, job.running)
	assert.True(t, job.claimed["/wt/paused_18be000000000001"], "the temp sweep's claim set still holds paused sessions")
}

// TestClaudeTmpSweep_ArchivesDeadChats: the queued job runs the chat sweep
// after the temp sweep, over the main config dir's projects/.
func TestClaudeTmpSweep_ArchivesDeadChats(t *testing.T) {
	mainDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", mainDir)
	cfg := t.TempDir()
	gone := filepath.Join(cfg, "worktrees", "u", "gone_18be000000000001")
	chat := filepath.Join(mainDir, "projects", claudetmp.Names(gone)[0].Value)
	require.NoError(t, os.MkdirAll(chat, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(chat, "s.jsonl"), []byte("turn\n"), 0o600))
	m := NewForTest(Options{})
	m.requestClaudeTmpSweep(NewWorkspace(WorkspaceParts{}), cfg)
	require.True(t, m.maybeClaudeTmpSweep())
	jobs := m.Drain().Jobs
	require.Len(t, jobs, 1)

	jobs[0]()

	assert.FileExists(t, filepath.Join(claudetmp.ChatArchiveDir(cfg), "u-gone-18be000000000001.zip"))
	assert.FileExists(t, filepath.Join(chat, "s.jsonl"), "copied, not moved")
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `CGO_ENABLED=0 go test ./core -run 'TestClaudeTmpSweep_(SnapshotsTheRunningSet|ArchivesDeadChats)' 2>&1 | head`
Expected: build failure, `job.running undefined`.

### C2. Implement

- [ ] **Step 3: Update `core/claude_tmp.go`**

Replace the `claudeTmpJob` type with:

```go
// claudeTmpJob is one workspace's queued Claude temp-dir and chat sweep,
// with its inputs as they stood when it was queued: the claim set and the
// known config dirs for the temp sweep, and for the chat sweep the
// worktrees of the sessions that are not paused and the Claude config dirs
// whose projects/ the job resolves (session.ClaudeProjectsRoots) off the
// model's goroutine.
type claudeTmpJob struct {
	cfgDir      string
	claimed     map[string]bool
	others      []string
	running     map[string]bool
	mainDir     string
	accountDirs map[string]string
}
```

In `requestClaudeTmpSweep`, change the first line of its doc comment to `// requestClaudeTmpSweep queues a sweep of cfgDir's Claude temp dirs` / `// (session.SweepClaudeTemp) and chats (session.SweepClaudeChats), snapshotting` / `// ws's claim set and running set here, on the model's goroutine.`, keeping the rest. Replace the assignment with:

```go
	m.claudeTmpPending[cfgDir] = claudeTmpJob{
		cfgDir:      cfgDir,
		claimed:     claimedWorktreePaths(ws.insts, ws.storage),
		others:      m.knownConfigDirs(),
		running:     runningWorktreePaths(ws.insts),
		mainDir:     m.MainConfigDir(),
		accountDirs: m.accountDirs(),
	}
```

Add below `requestClaudeTmpSweep`:

```go
// runningWorktreePaths returns the worktree paths of insts that are not
// Paused or Recoverable: the sessions whose chats the chat sweep leaves to
// Pause and Kill, since each turn changes them. GetWorktreePath, not
// GetGitWorktree: a record not yet started (Loading, or Running before
// EnsureRunning) still names its worktree. A workspace terminal's path is
// its repository root, which no sweepable name encodes.
func runningWorktreePaths(insts []*session.Instance) map[string]bool {
	paths := map[string]bool{}
	for _, inst := range insts {
		if st := inst.GetStatus(); st == session.Paused || st == session.Recoverable {
			continue
		}
		if p := inst.GetWorktreePath(); p != "" {
			paths[p] = true
		}
	}
	return paths
}
```

Replace `claudeTmpSweepJob` with:

```go
// claudeTmpSweepJob runs the sweeps off the model's goroutine: each
// workspace's temp dirs, then its chats. Each logs what it archived and
// nothing comes back to apply, so the result is nil (deliverGated still
// disarms the gate).
func claudeTmpSweepJob(jobs []claudeTmpJob) Job {
	return func() any {
		for _, j := range jobs {
			session.SweepClaudeTemp(j.cfgDir, j.claimed, j.others)
			session.SweepClaudeChats(j.cfgDir, session.ClaudeProjectsRoots(j.mainDir, j.accountDirs), j.running, j.others)
		}
		return nil
	}
}
```

- [ ] **Step 4: Run the core tests**

Run: `CGO_ENABLED=0 go test ./core -run 'TestClaudeTmpSweep'`
Expected: `ok`. All four earlier `TestClaudeTmpSweep_…` tests still pass.

- [ ] **Step 5: `loom debug` in `main.go`**

After the `switch root, err := cfg.ClaudeTmpArchiveRoot(); { ... }` block, add:

```go
			fmt.Printf("Claude chat archives: %s (copies of sessions' conversations; claude_tmp_archive_dir moves them too)\n", session.ClaudeChatArchiveDir(wsCtx.ConfigDir))
```

- [ ] **Step 6: User and architecture docs**

`USAGE.md`, in the config table row for `claude_tmp_archive_dir`, replace `Where Claude temp-dir archives go, for every workspace` with `Where Claude temp-dir and chat archives go, for every workspace`, and replace `See [Claude Temp-Dir Archives](#claude-temp-dir-archives)` with `See [Claude Temp-Dir Archives](#claude-temp-dir-archives) and [Claude Chat Archives](#claude-chat-archives)`.

`USAGE.md`, after the `### Claude Temp-Dir Archives` section (before `### Claude Fullscreen Renderer`), add:

````markdown
### Claude Chat Archives

Claude Code keeps each session's conversation in `~/.claude/projects/<the session's directory, encoded>/` (under `$CLAUDE_CONFIG_DIR` when that is set; an extra account's `projects/` links to the main one), and deletes a transcript once it has gone unwritten for `cleanupPeriodDays` (30 by default). A loom session's directory is its worktree, which nothing reuses, so a session's conversation would vanish a month after it ended, or a month into a pause. Loom keeps a copy:

- **Pause** and **kill** copy the session's conversation (its transcripts, its subagents' and its tool results) into a zip. Claude's own files are never moved or changed.
- **Resume** puts a paused session's transcript back if Claude deleted it during the pause, so Claude picks the conversation up again (`--resume`) instead of starting over. If that fails, you see why and the session starts with `--continue`.
- **At every workspace load**, loom copies the conversations of its sessions that aren't running (paused, killed, or ended before loom archived chats) when they changed since their last copy.
- A new copy replaces the old zip but keeps everything in it that Claude has since deleted, so nothing once archived is lost.
- Archives live beside the temp-dir archives: `<repo>/.loom/archive/chats/` (or `~/.loom/archive/chats/`), or `<claude_tmp_archive_dir>/<workspace folder>/chats/` when that is set. `loom debug` prints the folder.
- Loom never deletes them. To read one:

  ```sh
  unzip -l <file>.zip                     # list the sessions in it
  unzip -p <file>.zip <session id>.jsonl  # print a transcript
  zipgrep 'some phrase' <file>.zip        # search every file in it
  ```

- Only loom's worktree sessions are covered, not Claude sessions run in a repository root or outside loom.
````

`docs/ARCHITECTURE.md`, in the persistent-state table, after the `archive/claude-tmp/` row, add:

```markdown
| `archive/chats/` | Copies of Claude's chats for paused and ended sessions (or under `claude_tmp_archive_dir`) | [`session/README.md`](../session/README.md) |
```

`config/README.md`, in the `ClaudeTmpArchiveDir` bullet, replace `(`session.ClaudeTmpArchiveDir`)` with `(`session.ClaudeTmpArchiveDir`), and its chat archives in a `chats` folder beside them (`session.ClaudeChatArchiveDir`)`.

- [ ] **Step 7: Full verification**

```bash
CGO_ENABLED=0 go test ./...
CC=clang CGO_ENABLED=1 go test -race ./core/... ./session/...
gofmt -l $(git ls-files '*.go' | grep -v '^vendor/')
CGO_ENABLED=0 go vet ./...
GOOS=windows CGO_ENABLED=0 go build ./...
go run ./tools/claudemd -v
```

Expected: every test `ok`; `gofmt -l` prints nothing; vet clean; the Windows build succeeds (`os.Chtimes`, `os.Root` and `zip.Writer.Copy` are portable); claudemd within budget. Say plainly that golangci-lint was skipped, unless you ran CI's pinned v1 binary.

- [ ] **Step 8: Commit**

```bash
gofmt -w core/claude_tmp.go core/claude_tmp_test.go main.go
git add -- core/claude_tmp.go core/claude_tmp_test.go main.go USAGE.md docs/ARCHITECTURE.md config/README.md
git commit -m "feat(core): sweep Claude chats with the temp dirs on workspace load" -- core/claude_tmp.go core/claude_tmp_test.go main.go USAGE.md docs/ARCHITECTURE.md config/README.md
```

---

## After the packages

- [ ] Smoke-test in a sandbox through the `loom-dev` skill. Give the sandbox a `CLAUDE_CONFIG_DIR` whose `projects/` holds a dir named for a dead worktree of the sandbox workspace, with an old `.jsonl` in it. Start the dev build, and check that the daemon's `serve.log` shows `claudechat.archived` with reason `sweep` and that `<sandbox repo>/.loom/archive/chats/` holds the zip. Never run the dev build against the user's real daemon or `~/.claude`.
- [ ] Update the memory file `loom-claude-chat-archive.md` with the commits.
