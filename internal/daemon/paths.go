package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// maxSocketPath bounds a socket's path: a unix socket address holds 108
// bytes on Linux and 104 on macOS, NUL included, and connect fails with
// "file name too long" past it.
const maxSocketPath = 100

// SocketPath is where globalDir's daemon listens, its directory made ready
// (0700, owned by this user): $XDG_RUNTIME_DIR/loom/<hash>.sock when the
// user has a runtime dir; else <globalDir>/run/serve.sock when that path
// fits a socket address; else /tmp/loom-<uid>/<hash>.sock, as tmux keeps its
// own. <hash> names the global dir, resolved, so each global dir (a loomdev
// sandbox's included) gets a daemon of its own. Only the daemon calls it:
// it records the path in its lock (Record.Socket), and clients dial what
// the record says, since their environment may differ from the daemon's.
func SocketPath(globalDir string) (string, error) {
	name := globalHash(globalDir) + ".sock"
	var candidates []string
	if rt := os.Getenv("XDG_RUNTIME_DIR"); rt != "" && filepath.IsAbs(rt) {
		candidates = append(candidates, filepath.Join(rt, "loom", name))
	}
	candidates = append(candidates,
		filepath.Join(globalDir, "run", "serve.sock"),
		filepath.Join(os.TempDir(), fmt.Sprintf("loom-%d", os.Getuid()), name))
	var lastErr error
	for _, p := range candidates {
		if len(p) > maxSocketPath {
			lastErr = fmt.Errorf("socket path %s is too long for a unix socket", p)
			continue
		}
		if err := privateDir(filepath.Dir(p)); err != nil {
			lastErr = err
			continue
		}
		return p, nil
	}
	return "", fmt.Errorf("no place for the loom daemon's socket: %w", lastErr)
}

// globalHash names globalDir in a socket's file name: 16 hex characters of
// the SHA-256 of its resolved path, so two spellings of one dir share it.
func globalHash(globalDir string) string {
	dir := filepath.Clean(globalDir)
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	sum := sha256.Sum256([]byte(dir))
	return hex.EncodeToString(sum[:])[:16]
}

// privateDir makes dir, or checks it: a directory, not a symlink, owned by
// this user, mode 0700, so no one else can reach the socket in it or put
// one there first.
func privateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	if ownedByAnother(info) {
		return fmt.Errorf("%s belongs to another user", dir)
	}
	if info.Mode().Perm() != 0o700 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("make %s private: %w", dir, err)
		}
	}
	return nil
}
