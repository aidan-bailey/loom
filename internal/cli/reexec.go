package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aidan-bailey/loom/core/rpc"
)

// envReexec marks a process that re-executed into a newer daemon's build
// (reexecInto): its value is that build's hash (rpc.Hello.Exe). A process
// refused again by the build it carried on under fails fast rather than
// execute it in a loop; one refused by a later, different build may.
const envReexec = "LOOM_REEXEC"

// NewerDaemon is the join's refusal of a daemon newer than this loom
// (main's newerDaemonError), which rejoin reads with errors.As: the
// daemon's hello and its build's name for a message.
type NewerDaemon interface {
	error
	NewerDaemon() (peer rpc.Hello, name string)
}

// execProcess replaces this process with path, run with argv and env
// (syscall.Exec where the platform has it): it returns only on failure.
// Tests replace it.
var execProcess = execSelf

// reexecPlatform says whether this platform can replace a process in
// place (reexecSupported); tests turn it off.
var reexecPlatform = reexecSupported

// reexecInto replaces this process with the newer daemon's executable,
// with this process's arguments and envReexec naming the build, after
// saying so through say: a long-running command then carries on under
// the daemon that refused it, keeping its PID (a job's shim waits on it),
// its log and its cgroup. It does so only when the daemon names an
// absolute path (peer.ExePath) to a regular file this user can execute,
// whose SHA-256 is still the daemon's build (peer.Exe: not something
// installed over it since, which could be older), when this process has
// not already carried on under that build (envReexec), and when the
// platform can. Otherwise it returns why not, and executes nothing. A
// successful exec does not return; one that fails returns its error.
func reexecInto(peer rpc.Hello, name string, say func(string)) error {
	path := peer.ExePath
	switch {
	case path == "":
		return errors.New("the daemon names no executable (a build from before exe_path)")
	case !filepath.IsAbs(path):
		return fmt.Errorf("the daemon's executable %q is not absolute", path)
	case peer.Exe != "" && os.Getenv(envReexec) == peer.Exe:
		return fmt.Errorf("this process already carried on under this build (%s=%s) and was refused again", envReexec, peer.Exe)
	case !reexecPlatform:
		return errors.New("loom can't re-execute on this platform")
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("the daemon's executable: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("the daemon's executable %s is not a regular file", path)
	}
	if err := executable(path); err != nil {
		return fmt.Errorf("this user can't execute the daemon's executable %s: %w", path, err)
	}
	sum, err := fileSHA256(path)
	if err != nil {
		return fmt.Errorf("the daemon's executable: %w", err)
	}
	if peer.Exe == "" || sum != peer.Exe {
		return fmt.Errorf("%s is no longer the daemon's build: something was installed over it since", path)
	}
	say(fmt.Sprintf("the loom daemon was upgraded to %s; carrying on under it (%s)", name, path))
	argv := append([]string{path}, os.Args[1:]...)
	return execProcess(path, argv, withEnv(os.Environ(), envReexec, peer.Exe))
}

// withEnv is env with key set to value, replacing any entry it had.
func withEnv(env []string, key, value string) []string {
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if !strings.HasPrefix(e, key+"=") {
			out = append(out, e)
		}
	}
	return append(out, key+"="+value)
}

// fileSHA256 is the SHA-256 of the file at path, in hex.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}
