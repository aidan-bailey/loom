package sched

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CheckCommand refuses a command that is one word with a space or a tab in
// it and names no program (smart scheduler spec §5.7): not found on the PATH
// of env and, when it holds a slash, not a file relative to cwd. A command a
// shell would split runs as one program name and fails at start with exit
// 127. A missing command with no space passes: an earlier job may build it.
// The model runs it at submit, after the folder check.
func CheckCommand(argv, env []string, cwd string) error {
	if len(argv) != 1 || !strings.ContainsAny(argv[0], " \t") {
		return nil
	}
	word := argv[0]
	if strings.Contains(word, "/") {
		path := word
		if !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		if _, err := os.Stat(path); err == nil {
			return nil
		}
	} else if _, err := resolveCommand(word, env, cwd); err == nil {
		return nil
	}
	return fmt.Errorf("the command is one word with spaces (%q): pass its words as separate arguments (-- %s), or run it through a shell (-- sh -c '…')",
		word, strings.Join(strings.Fields(word), " "))
}

// resolveCommand finds the executable argv0 names the way a shell holding
// env would. A name with a slash is taken as it is (a relative one is
// relative to the job's folder, which exec.Cmd does). Any other is looked for
// along env's PATH, the last PATH entry of env winning; an empty element is
// the folder and a relative one is the folder's. The shim's own PATH is never
// consulted: it is the daemon's, and the job must run the program its
// submitter's shell would have run. A name found nowhere is an exec.Error
// wrapping exec.ErrNotFound.
func resolveCommand(argv0 string, env []string, cwd string) (string, error) {
	if strings.Contains(argv0, "/") {
		return argv0, nil
	}
	path, _ := lastEnvValue(env, "PATH")
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			dir = "."
		}
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(cwd, dir)
		}
		candidate := filepath.Join(dir, argv0)
		if isExecutableFile(candidate) {
			return candidate, nil
		}
	}
	return "", &exec.Error{Name: argv0, Err: exec.ErrNotFound}
}

// lastEnvValue returns the value of key in env, the last entry for it winning.
func lastEnvValue(env []string, key string) (string, bool) {
	for i := len(env) - 1; i >= 0; i-- {
		if v, ok := strings.CutPrefix(env[i], key+"="); ok {
			return v, true
		}
	}
	return "", false
}

// isExecutableFile reports whether path is a regular file with an execute bit.
func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}
