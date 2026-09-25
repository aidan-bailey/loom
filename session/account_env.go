package session

import (
	"fmt"
	"os"
	"sync/atomic"

	"github.com/aidan-bailey/loom/account"
)

// accountDirsSnapshot is one atomic publication of the account registry's
// state: either its name → config dir map, or the error that kept it from
// loading. Bundled into one struct so a reader can never pair a dirs map
// from one publication with an error from another.
type accountDirsSnapshot struct {
	dirs map[string]string
	err  error
}

// accountDirsState holds the latest snapshot. app publishes it
// (SetAccountDirs) at startup and after every registry change; launches
// read it on lifecycle goroutines, hence the atomic pointer.
var accountDirsState atomic.Pointer[accountDirsSnapshot]

// SetAccountDirs publishes the account registry's name → config dir map
// together with registryErr, the error that kept the registry from
// loading (nil on success). Bundling them in one atomic store means a
// launch reading the published state can never observe dirs left over
// from before a load failure, or vice versa. It copies dirs, so the
// caller may keep mutating its own.
func SetAccountDirs(dirs map[string]string, registryErr error) {
	cp := make(map[string]string, len(dirs))
	for k, v := range dirs {
		cp[k] = v
	}
	accountDirsState.Store(&accountDirsSnapshot{dirs: cp, err: registryErr})
}

// MissingAccountError is a launch refused because the instance's account
// is not registered. A launch never falls back to the default account:
// that would bill the wrong subscription. The text names R because that
// is how the user actually recovers an existing session (relaunch with
// options, choosing a different account); it also fires on a brand-new
// Start, which removes the instance on failure instead — the wording
// stays accurate there too, since "pick another account" still applies
// and the parenthetical only claims R for an existing session.
type MissingAccountError struct{ Name string }

func (e *MissingAccountError) Error() string {
	return fmt.Sprintf("account %q is not registered any more — pick another account (R on an existing session)", e.Name)
}

// AccountDirMissingError is a launch refused because a registered
// account's config directory no longer exists on disk (deleted by hand,
// say). Distinct from MissingAccountError: the account is still
// registered, but relaunching Claude against a missing CLAUDE_CONFIG_DIR
// would have the CLI silently create a fresh, logged-out identity there
// instead of loom reporting the account's state gone.
type AccountDirMissingError struct {
	Name string
	Dir  string
}

func (e *AccountDirMissingError) Error() string {
	return fmt.Sprintf("account %q's config directory is missing (%s) — choose another account in Session Launch Options", e.Name, e.Dir)
}

// Unwrap lets callers use errors.Is(err, account.ErrAccountDirMissing)
// regardless of whether the missing-dir case was caught here (an
// instance's own resolution) or in the account package (a direct CLI
// subprocess call).
func (e *AccountDirMissingError) Unwrap() error { return account.ErrAccountDirMissing }

// RegistryLoadError is a launch refused because the account registry
// itself failed to load (a corrupt or unreadable accounts.json), so loom
// cannot tell whether the instance's account still exists. Distinct from
// MissingAccountError, whose "not registered" means the registry loaded
// fine and simply has no entry for this name — reporting that instead
// here would tell the user to pick another account when the real account
// may still be fine.
type RegistryLoadError struct {
	Name string
	Err  error
}

func (e *RegistryLoadError) Error() string {
	return fmt.Sprintf("account registry failed to load; can't confirm account %q still exists: %v", e.Name, e.Err)
}

func (e *RegistryLoadError) Unwrap() error { return e.Err }

// accountDir resolves an instance's account name to its config dir: ""
// for the default account (never consults the registry), a
// *RegistryLoadError when the registry itself failed to load, a
// *MissingAccountError for a name the registry does not have, and an
// *AccountDirMissingError when the account is registered but its
// directory no longer exists on disk — the CLI would otherwise treat that
// as a place to create a fresh, logged-out identity rather than an error.
func accountDir(name string) (string, error) {
	if name == "" || name == account.DefaultName {
		return "", nil
	}
	snap := accountDirsState.Load()
	if snap == nil {
		return "", &MissingAccountError{Name: name}
	}
	if snap.err != nil {
		return "", &RegistryLoadError{Name: name, Err: snap.err}
	}
	dir, ok := snap.dirs[name]
	if !ok {
		return "", &MissingAccountError{Name: name}
	}
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return "", &AccountDirMissingError{Name: name, Dir: dir}
		}
		return "", fmt.Errorf("account %q: stat %s: %w", name, dir, err)
	}
	return dir, nil
}

// bestEffortAccountDir is accountDir for a session object that is built
// but not launched (a restored Paused record): an unresolvable account
// yields no config dir, and the real launch fails closed later.
func bestEffortAccountDir(name string) string {
	dir, _ := accountDir(name)
	return dir
}
