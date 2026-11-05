package sched

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeFixtureFile creates path, and its parents, holding "x".
func writeFixtureFile(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o600))
}

// assertJobFileMode checks path's permission bits; Windows has none to check.
func assertJobFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, want, info.Mode().Perm(), path)
}

func TestJobDir_IsTheDecimalID(t *testing.T) {
	assert.Equal(t, filepath.Join("/x/jobs", "14"), JobDir("/x/jobs", 14))
	assert.Equal(t, filepath.Join("/x/jobs", "1000000"), JobDir("/x/jobs", 1000000))
}

// TestCreateJobDir_IsExclusive: the directory is made (with its parents) only
// when it does not exist; one that does is an fs.ErrExist error and keeps its
// contents.
func TestCreateJobDir_IsExclusive(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "jobs")
	require.NoError(t, CreateJobDir(dir, 4), "the jobs dir is made too")
	info, err := os.Stat(JobDir(dir, 4))
	require.NoError(t, err)
	assert.True(t, info.IsDir())
	if runtime.GOOS != "windows" {
		assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	}

	writeFixtureFile(t, filepath.Join(JobDir(dir, 4), "output.log"))
	err = CreateJobDir(dir, 4)
	require.Error(t, err)
	assert.ErrorIs(t, err, fs.ErrExist)
	assert.FileExists(t, filepath.Join(JobDir(dir, 4), "output.log"))

	assert.Error(t, CreateJobDir(dir, 0), "an ID below 1 is refused")
	assert.NoDirExists(t, JobDir(dir, 0))
}

func TestRemoveJobDir_RemovesOnlyThatJobsDir(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"14/spec.json", "14/output.log", "14/sub/deep.txt", "140/spec.json", "1/spec.json", "jobs.json"} {
		writeFixtureFile(t, filepath.Join(dir, p))
	}
	require.NoError(t, RemoveJobDir(dir, 14))
	assert.NoDirExists(t, filepath.Join(dir, "14"))
	for _, p := range []string{"140/spec.json", "1/spec.json", "jobs.json"} {
		assert.FileExists(t, filepath.Join(dir, p), "RemoveJobDir took %s with it", p)
	}
	assert.NoError(t, RemoveJobDir(dir, 14), "a job directory that is already gone is no error")
}

// TestRemoveJobDir_DoesNotFollowLinks: a link inside a job directory is
// removed, never what it points at.
func TestRemoveJobDir_DoesNotFollowLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating links needs privileges on Windows")
	}
	dir := t.TempDir()
	outsideFile := filepath.Join(t.TempDir(), "keep.txt")
	writeFixtureFile(t, outsideFile)
	outsideTree := filepath.Join(t.TempDir(), "tree")
	writeFixtureFile(t, filepath.Join(outsideTree, "keep.txt"))

	require.NoError(t, os.MkdirAll(JobDir(dir, 9), 0o700))
	require.NoError(t, os.Symlink(outsideFile, filepath.Join(JobDir(dir, 9), "output.log")))
	require.NoError(t, os.Symlink(outsideTree, filepath.Join(JobDir(dir, 9), "tree")))

	require.NoError(t, RemoveJobDir(dir, 9))
	assert.NoDirExists(t, JobDir(dir, 9))
	assert.FileExists(t, outsideFile)
	assert.FileExists(t, filepath.Join(outsideTree, "keep.txt"))

	// The job's own directory is a link to an outside tree: the link goes,
	// the tree stays.
	require.NoError(t, os.Symlink(outsideTree, JobDir(dir, 10)))
	require.NoError(t, RemoveJobDir(dir, 10))
	_, err := os.Lstat(JobDir(dir, 10))
	assert.True(t, os.IsNotExist(err), "the link itself is removed")
	assert.FileExists(t, filepath.Join(outsideTree, "keep.txt"), "the tree the job directory linked to survives")
}

// TestRemoveJobDir_RefusesWhatItCannotProve: a dir that is empty, relative or
// not clean, and an ID below 1, are refused, and nothing is removed.
func TestRemoveJobDir_RefusesWhatItCannotProve(t *testing.T) {
	t.Run("an empty or relative dir would remove under the working directory", func(t *testing.T) {
		t.Chdir(t.TempDir())
		writeFixtureFile(t, "7/keep")
		writeFixtureFile(t, "jobs/7/keep")
		for _, dir := range []string{"", "jobs"} {
			assert.Error(t, RemoveJobDir(dir, 7), "dir %q", dir)
		}
		assert.FileExists(t, "7/keep")
		assert.FileExists(t, "jobs/7/keep")
	})

	t.Run("an ID below 1", func(t *testing.T) {
		dir := t.TempDir()
		writeFixtureFile(t, filepath.Join(dir, "0", "keep"))
		writeFixtureFile(t, filepath.Join(dir, "-3", "keep"))
		assert.Error(t, RemoveJobDir(dir, 0))
		assert.Error(t, RemoveJobDir(dir, -3))
		assert.FileExists(t, filepath.Join(dir, "0", "keep"))
		assert.FileExists(t, filepath.Join(dir, "-3", "keep"))
	})

	t.Run("a dir the kernel resolves differently from its text", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("creating links needs privileges on Windows")
		}
		root := t.TempDir()
		realDir := filepath.Join(root, "real")
		require.NoError(t, os.MkdirAll(filepath.Join(realDir, "sub"), 0o700))
		require.NoError(t, os.Symlink(filepath.Join(realDir, "sub"), filepath.Join(root, "lnk")))
		// Lexically root/lnk/../jobs is root/jobs; the kernel follows lnk
		// first and reaches real/jobs.
		lexical := filepath.Join(root, "jobs", "5", "keep")
		kernel := filepath.Join(realDir, "jobs", "5", "keep")
		writeFixtureFile(t, lexical)
		writeFixtureFile(t, kernel)

		assert.Error(t, RemoveJobDir(root+"/lnk/../jobs", 5))
		assert.FileExists(t, lexical)
		assert.FileExists(t, kernel)
	})
}

// removeAllCallers returns, for each use of os.RemoveAll in src (a call or a
// reference), the name of the function that holds it, or "" at package level.
// It resolves the file's own name for os, an alias or a dot import included.
func removeAllCallers(t *testing.T, name, src string) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), name, src, 0)
	require.NoError(t, err)
	osName := ""
	for _, imp := range f.Imports {
		if imp.Path.Value != `"os"` {
			continue
		}
		osName = "os"
		if imp.Name != nil {
			osName = imp.Name.Name
		}
	}
	if osName == "" || osName == "_" {
		return nil
	}
	var callers []string
	for _, decl := range f.Decls {
		enclosing := ""
		if fn, ok := decl.(*ast.FuncDecl); ok {
			enclosing = fn.Name.Name
		}
		ast.Inspect(decl, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.SelectorExpr:
				if id, ok := x.X.(*ast.Ident); ok && id.Name == osName && x.Sel.Name == "RemoveAll" {
					callers = append(callers, enclosing)
				}
			case *ast.Ident:
				if osName == "." && x.Name == "RemoveAll" {
					callers = append(callers, enclosing)
				}
			}
			return true
		})
	}
	return callers
}

// TestRemoveAllCallers_Detection pins removeAllCallers, so that the scan
// below cannot turn into a test that never fails.
func TestRemoveAllCallers_Detection(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{"a call", "package p\nimport \"os\"\nfunc RemoveJobDir() { os.RemoveAll(\"x\") }\n", []string{"RemoveJobDir"}},
		{"an aliased import", "package p\nimport o \"os\"\nfunc f() { o.RemoveAll(\"x\") }\n", []string{"f"}},
		{"a dot import", "package p\nimport . \"os\"\nfunc g() { RemoveAll(\"x\") }\n", []string{"g"}},
		{"a reference that is no call", "package p\nimport \"os\"\nvar rm = os.RemoveAll\n", []string{""}},
		{"inside a closure", "package p\nimport \"os\"\nfunc h() { go func() { os.RemoveAll(\"x\") }() }\n", []string{"h"}},
		{"another os function", "package p\nimport \"os\"\nfunc f() { os.Remove(\"x\") }\n", nil},
		{"no os import", "package p\nfunc f(os fs) { os.RemoveAll(\"x\") }\n", nil},
		{"a blank import", "package p\nimport _ \"os\"\nfunc f() {}\n", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.ElementsMatch(t, tc.want, removeAllCallers(t, "src.go", tc.src))
		})
	}
}

// TestSched_RemoveAllIsOnlyInRemoveJobDir: no non-test file of the package
// uses os.RemoveAll but RemoveJobDir, which builds its path from a job ID.
// The scan must find that one use, so a scan of nothing fails.
func TestSched_RemoveAllIsOnlyInRemoveJobDir(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	var found []string
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		require.NoError(t, err)
		for _, fn := range removeAllCallers(t, name, string(src)) {
			found = append(found, name+":"+fn)
		}
	}
	assert.Equal(t, []string{"files.go:RemoveJobDir"}, found)
}

func TestSpec_RoundTrip(t *testing.T) {
	jobDir := filepath.Join(t.TempDir(), "jobs", "14")
	in := Spec{ID: 14, Argv: []string{"sh", "-c", "echo hi"}, Cwd: "/work", Env: []string{"A=1", "B=two words"}}
	require.NoError(t, WriteSpec(jobDir, in))
	got, err := ReadSpec(jobDir)
	require.NoError(t, err)
	assert.Equal(t, in, got)
	assertJobFileMode(t, jobDir, 0o700)
	assertJobFileMode(t, filepath.Join(jobDir, SpecFile), 0o600)

	in.HintCPUs = 4 // the start adds the hint
	require.NoError(t, WriteSpec(jobDir, in))
	got, err = ReadSpec(jobDir)
	require.NoError(t, err)
	assert.Equal(t, in, got, "a rewrite replaces the file")
}

func TestShimFiles_RoundTrip(t *testing.T) {
	jobDir := t.TempDir()
	t0 := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)

	started := Started{PID: 4242, Started: t0}
	require.NoError(t, writeStarted(jobDir, started))
	gotStarted, err := ReadStarted(jobDir)
	require.NoError(t, err)
	assert.Equal(t, started, gotStarted)

	for _, exit := range []Exit{
		{Code: 0, Ended: t0.Add(time.Minute), MaxRSSKB: 2048},
		{Code: -1, Signal: "SIGKILL", Ended: t0.Add(time.Minute)},
		{Code: 127, Ended: t0, Error: `exec: "nope": executable file not found in $PATH`},
	} {
		require.NoError(t, writeExit(jobDir, exit))
		got, err := ReadExit(jobDir)
		require.NoError(t, err)
		assert.Equal(t, exit, got)
	}

	entries, err := os.ReadDir(jobDir)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	assert.ElementsMatch(t, []string{StartedFile, ExitFile}, names, "the atomic writes leave no temp files")
	assertJobFileMode(t, filepath.Join(jobDir, StartedFile), 0o600)
	assertJobFileMode(t, filepath.Join(jobDir, ExitFile), 0o600)
}

// TestJobFiles_AbsentAndCorrupt: an absent file's error wraps fs.ErrNotExist,
// which is how the model tells "not yet" from "unreadable"; a file that does
// not parse is an error of its own.
func TestJobFiles_AbsentAndCorrupt(t *testing.T) {
	jobDir := t.TempDir()
	reads := map[string]func() error{
		SpecFile:    func() error { _, err := ReadSpec(jobDir); return err },
		StartedFile: func() error { _, err := ReadStarted(jobDir); return err },
		ExitFile:    func() error { _, err := ReadExit(jobDir); return err },
	}
	for name, read := range reads {
		assert.ErrorIs(t, read(), fs.ErrNotExist, "%s absent", name)
	}
	for name, read := range reads {
		require.NoError(t, os.WriteFile(filepath.Join(jobDir, name), []byte(`{"pid": `), 0o600))
		err := read()
		assert.Error(t, err, "%s corrupt", name)
		assert.NotErrorIs(t, err, fs.ErrNotExist, "%s corrupt", name)
	}
}

func TestArchiveAttempt(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{LogFile, StartedFile, ExitFile, SpecFile} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600))
	}
	require.NoError(t, ArchiveAttempt(dir, 0))
	for _, name := range []string{LogFile, StartedFile, ExitFile} {
		assert.NoFileExists(t, filepath.Join(dir, name))
		data, err := os.ReadFile(filepath.Join(dir, "attempt-1", name))
		require.NoError(t, err)
		assert.Equal(t, name, string(data))
	}
	assert.FileExists(t, filepath.Join(dir, SpecFile), "the spec stays for the next attempt")
	info, err := os.Stat(filepath.Join(dir, "attempt-1"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())

	require.NoError(t, ArchiveAttempt(dir, 1), "missing files are skipped")
	assert.DirExists(t, filepath.Join(dir, "attempt-2"))
	assert.Error(t, ArchiveAttempt(dir, 1), "an attempt is archived once")
}
