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
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return skip(SkipNoTarget, "no worktree") // paused before pause kept the tree
	case err != nil:
		return skip(SkipError, err.Error())
	}
	target := filepath.Join(wt, "target")
	if !isCargoTarget(target) {
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

// isCargoTarget reports whether target is a dir cargo builds into. Cargo
// tags a target dir it creates with CACHEDIR.TAG, but one that already
// existed (an agent's notes written there first) gets no tag, only the
// .rustc_info.json cargo writes into it on every build.
func isCargoTarget(target string) bool {
	return isDir(target) && (isRegular(filepath.Join(target, "CACHEDIR.TAG")) || isRegular(filepath.Join(target, ".rustc_info.json")))
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
