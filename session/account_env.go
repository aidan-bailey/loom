package session

import (
	"fmt"
	"sync/atomic"

	"github.com/aidan-bailey/loom/account"
)

// accountDirs maps each registered extra account to its config dir. app
// publishes it (SetAccountDirs) at startup and after every registry change;
// launches read it on lifecycle goroutines, hence the atomic pointer.
var accountDirs atomic.Pointer[map[string]string]

// SetAccountDirs publishes the account registry's name → config dir map.
// It copies dirs, so the caller may keep mutating its own.
func SetAccountDirs(dirs map[string]string) {
	cp := make(map[string]string, len(dirs))
	for k, v := range dirs {
		cp[k] = v
	}
	accountDirs.Store(&cp)
}

// MissingAccountError is a launch refused because the instance's account
// is no longer registered. A launch never falls back to the default
// account: that would bill the wrong subscription.
type MissingAccountError struct{ Name string }

func (e *MissingAccountError) Error() string {
	return fmt.Sprintf("account %q no longer exists — press R to relaunch on another account", e.Name)
}

// accountDir resolves an instance's account name to its config dir: "" for
// the default account, a *MissingAccountError for an unregistered one.
func accountDir(name string) (string, error) {
	if name == "" || name == account.DefaultName {
		return "", nil
	}
	if p := accountDirs.Load(); p != nil {
		if dir, ok := (*p)[name]; ok {
			return dir, nil
		}
	}
	return "", &MissingAccountError{Name: name}
}

// bestEffortAccountDir is accountDir for a session object that is built
// but not launched (a restored Paused record): an unresolvable account
// yields no config dir, and the real launch fails closed later.
func bestEffortAccountDir(name string) string {
	dir, _ := accountDir(name)
	return dir
}
