package sched

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/aidan-bailey/loom/config"
)

// The files of the jobs directory: jobs.json beside the job directories, and
// in each job's directory (JobDir) the shim's inputs and outputs.
const (
	StoreFile   = "jobs.json"
	SpecFile    = "spec.json"
	LogFile     = "output.log"
	LockFile    = "run.lock"
	StartedFile = "started.json"
	ExitFile    = "exit.json"
)

// JobDir returns the directory of job id under dir, which is
// <globalDir>/jobs: dir joined with the decimal ID.
func JobDir(dir string, id JobID) string {
	return filepath.Join(dir, strconv.FormatInt(int64(id), 10))
}

// RemoveJobDir removes the directory of job id under dir: the only removal
// in this package. The path is JobDir(dir, id), built from the ID, never a
// path read from a file. It refuses a dir that is empty, relative or not
// clean, and an ID below 1: JobDir("", 7) is "7" in whatever directory the
// process runs in, and a dir such as "lnk/.." cleans lexically to another tree
// than the kernel reaches through the link. os.RemoveAll removes a link inside
// the directory, never what it points at. A directory already gone is no
// error.
func RemoveJobDir(dir string, id JobID) error {
	if dir == "" || !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || id < 1 {
		return fmt.Errorf("refusing to remove job %d's directory under %q: the path is not proved to be loom's", id, dir)
	}
	if err := os.RemoveAll(JobDir(dir, id)); err != nil {
		return fmt.Errorf("remove job %d's directory: %w", id, err)
	}
	return nil
}

// saveJSON writes v to path, indented, atomically, with mode 0600.
func saveJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", filepath.Base(path), err)
	}
	return config.AtomicWriteFile(path, append(data, '\n'), 0o600)
}

// loadJSON reads path into v. The error for a file that is absent wraps
// fs.ErrNotExist.
func loadJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

// Spec is spec.json: what the shim needs to run a job. It is written when the
// job is submitted, so a queued job survives a restart, and rewritten with
// HintCPUs and Scope when the job starts.
type Spec struct {
	ID       JobID    `json:"id"`
	Argv     []string `json:"argv"`
	Cwd      string   `json:"cwd"`
	Env      []string `json:"env"`
	HintCPUs int      `json:"hint_cpus,omitempty"` // written when the job starts
	// Scope is the scope the shim creates for the job (cgroup spec §5.1),
	// written when it starts contained; nil runs it uncontained.
	Scope *ScopeSpec `json:"scope,omitempty"`
}

// CreateJobDir makes the directory of job id under dir (mode 0700), and dir
// and its parents (mode 0700) when they are absent, and only if the job's
// directory does not exist yet: one that does is an error wrapping
// fs.ErrExist and is left as it is, so a caller that gets nil owns the
// directory and no other job's files are in it. It refuses an ID below 1.
func CreateJobDir(dir string, id JobID) error {
	if id < 1 {
		return fmt.Errorf("refusing to create a directory for job %d: IDs start at 1", id)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	if err := os.Mkdir(JobDir(dir, id), 0o700); err != nil {
		return fmt.Errorf("create job %d's directory: %w", id, err)
	}
	return nil
}

// WriteSpec writes jobDir's spec.json (mode 0600), making jobDir (mode 0700)
// when it is absent.
func WriteSpec(jobDir string, s Spec) error {
	if err := os.MkdirAll(jobDir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", jobDir, err)
	}
	return saveJSON(filepath.Join(jobDir, SpecFile), s)
}

// ReadSpec reads jobDir's spec.json. The error for an absent file wraps
// fs.ErrNotExist.
func ReadSpec(jobDir string) (Spec, error) {
	var s Spec
	if err := loadJSON(filepath.Join(jobDir, SpecFile), &s); err != nil {
		return Spec{}, err
	}
	return s, nil
}

// Started is started.json: the shim writes it once it holds the job's lock.
type Started struct {
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
	// Confinement (cgroup spec §5.1, §5.3): the scope it joined and the
	// cgroup it landed in, or why it asked for a scope and runs without one.
	Unit        string `json:"unit,omitempty"`
	Cgroup      string `json:"cgroup,omitempty"`
	Uncontained string `json:"uncontained,omitempty"`
}

// ReadStarted reads jobDir's started.json. The error for an absent file wraps
// fs.ErrNotExist.
func ReadStarted(jobDir string) (Started, error) {
	var s Started
	if err := loadJSON(filepath.Join(jobDir, StartedFile), &s); err != nil {
		return Started{}, err
	}
	return s, nil
}

// ReadExit reads jobDir's exit.json. The error for an absent file wraps
// fs.ErrNotExist.
func ReadExit(jobDir string) (Exit, error) {
	var e Exit
	if err := loadJSON(filepath.Join(jobDir, ExitFile), &e); err != nil {
		return Exit{}, err
	}
	return e, nil
}

// writeStarted writes jobDir's started.json atomically. The shim's.
func writeStarted(jobDir string, s Started) error {
	return saveJSON(filepath.Join(jobDir, StartedFile), s)
}

// writeExit writes jobDir's exit.json atomically. The shim's.
func writeExit(jobDir string, e Exit) error {
	return saveJSON(filepath.Join(jobDir, ExitFile), e)
}
