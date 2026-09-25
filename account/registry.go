// Package account manages the extra Claude Code accounts loom can launch
// sessions under. Claude Code has no account flag: an account is whatever
// credentials live in a config dir, and CLAUDE_CONFIG_DIR picks the dir.
// loom keeps one dir per extra account under <globalDir>/accounts/<name>,
// links the main config dir's shared entries into it (link.go), and reads
// each account's identity and plan usage through the claude CLI (auth.go,
// usage.go). The implicit account "default" is Claude with no override;
// it is never stored.
//
// No app, ui or session imports. Every subprocess runs through an injected
// internalexec.Executor.
package account

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/aidan-bailey/loom/config"
)

// DefaultName names the implicit account: Claude with no CLAUDE_CONFIG_DIR
// override.
const DefaultName = "default"

const (
	registryFileName = "accounts.json"
	accountsDirName  = "accounts"
)

// Account is one registered extra account.
type Account struct {
	Name string `json:"name"`
	// Dir is the account's CLAUDE_CONFIG_DIR.
	Dir string `json:"dir"`
}

// Registry is accounts.json: the extra accounts and which account new
// sessions preselect. Every mutation reloads the file first and writes it
// back atomically, so a `loom account` run and a running TUI don't clobber
// each other (the same reload-before-save rule as config.WorkspaceRegistry).
type Registry struct {
	// DefaultAccount is the account new sessions preselect; empty means
	// DefaultName.
	DefaultAccount string    `json:"default,omitempty"`
	Accounts       []Account `json:"accounts"`

	path    string
	loadErr error
}

// ErrRegistryLoadFailed is returned by every write to a registry whose file
// failed to load, so a corrupt accounts.json is never overwritten.
var ErrRegistryLoadFailed = errors.New("accounts.json failed to load; refusing to overwrite it")

// LoadRegistry reads <globalDir>/accounts.json. A missing file is an empty
// registry. A file that cannot be read or parsed yields an empty registry
// whose LoadErr is set and whose writes all fail.
func LoadRegistry(globalDir string) *Registry {
	r := &Registry{path: filepath.Join(globalDir, registryFileName)}
	data, err := os.ReadFile(r.path)
	if err != nil {
		if !os.IsNotExist(err) {
			r.loadErr = fmt.Errorf("read %s: %w", r.path, err)
		}
		return r
	}
	if err := json.Unmarshal(data, r); err != nil {
		r.DefaultAccount, r.Accounts = "", nil
		r.loadErr = fmt.Errorf("parse %s: %w", r.path, err)
		return r
	}
	if err := validateAccounts(r.Accounts, r.AccountsDir()); err != nil {
		r.DefaultAccount, r.Accounts = "", nil
		r.loadErr = fmt.Errorf("validate %s: %w", r.path, err)
		return r
	}
	canonicalizeDirs(r.Accounts, r.AccountsDir())
	return r
}

// validateAccounts rejects a loaded accounts list this package would never
// have written itself: an invalid or reserved name, a name registered
// twice, a dir that isn't an absolute path, a dir that isn't already in
// lexically clean form, or a dir that doesn't resolve to
// <accountsDir>/<name>. Adopting an account whose dir loom did not itself
// create — a hand-edited "bring your own directory" entry — is out of
// scope: such a dir would still be handed to the claude CLI as
// CLAUDE_CONFIG_DIR on every launch, so refusing it only at delete time
// (Remove's own ownership check, which stays as a defence for a Registry
// built directly rather than loaded) is not enough. LoadRegistry treats a
// registry failing this the same as one that failed to parse — latched,
// not silently pruned, so a corrupt or hand-edited file is never
// overwritten with a partial view of it.
func validateAccounts(accounts []Account, accountsDir string) error {
	seen := make(map[string]bool, len(accounts))
	for _, a := range accounts {
		if err := ValidName(a.Name); err != nil {
			return fmt.Errorf("account %q: %w", a.Name, err)
		}
		if seen[a.Name] {
			return fmt.Errorf("account %q is registered twice", a.Name)
		}
		seen[a.Name] = true
		if a.Dir == "" || !filepath.IsAbs(a.Dir) {
			return fmt.Errorf("account %q: dir %q must be an absolute path", a.Name, a.Dir)
		}
		// Reject an unclean spelling outright, before it ever reaches
		// resolvedOrClean below. A stored "<accountsDir>/lnk/../name"
		// whose kernel target doesn't fully exist (not yet created, a
		// symlink loop, or a dangling link) makes resolvedOrClean's raw
		// EvalSymlinks attempt fail, falling back to a Clean-based climb
		// that would otherwise cancel the "lnk/.." pair lexically —
		// silently discarding the fact that "lnk" is a real symlink at
		// all, and accepting the disguise. A stored Dir this package
		// itself ever wrote (via Create, or canonicalized below on a
		// prior load) is always already clean, so this rejects nothing
		// legitimate.
		if a.Dir != filepath.Clean(a.Dir) {
			return fmt.Errorf("account %q: dir %q is not in canonical form", a.Name, a.Dir)
		}
		// Resolved, not a byte comparison: a.Dir was written under
		// whatever spelling of the global dir was in effect on some past
		// run, and accountsDir here reflects only this run's (a
		// respelled LOOM_GLOBAL_DIR, or $HOME going through a symlink
		// that isn't always resolved the same way before reaching here).
		// Two spellings of the same real directory must not latch the
		// registry. Safe from the disguise above now that a.Dir is
		// already known clean: resolvedOrClean's raw EvalSymlinks
		// attempt is the only path a clean, dot-free string can take.
		want := filepath.Join(accountsDir, a.Name)
		if resolvedOrClean(a.Dir) != resolvedOrClean(want) {
			return fmt.Errorf("account %q: dir %q is not %q", a.Name, a.Dir, want)
		}
	}
	return nil
}

// canonicalizeDirs replaces every account's Dir with the canonical
// <accountsDir>/<name>, in place — called only after validateAccounts has
// confirmed every stored Dir already resolves there, so this never turns
// a rejected entry into an accepted one. From here on, every consumer
// (Dirs, Env, Get, OwnedDir, a launch, a Sync, an auth or usage probe, the
// roster join, …) sees only the canonical spelling, never whatever string
// happened to be on disk. This closes the window a purely load-time check
// would leave open: a stored dir that was a genuine, accepted respelling
// of a symlinked prefix at load time could still be retargeted before a
// later operation that reuses the in-memory value without reloading (a
// plain resume, a crash restart) — canonicalizing means that operation
// was never going to depend on the symlink still resolving the same way
// in the first place.
func canonicalizeDirs(accounts []Account, accountsDir string) {
	for i := range accounts {
		accounts[i].Dir = filepath.Join(accountsDir, accounts[i].Name)
	}
}

// Unavailable is a registry that could not even be located (no global
// config dir). It holds no accounts and refuses every write. err is never
// nil in the returned registry's LoadErr, even when the caller passed nil.
func Unavailable(err error) *Registry {
	if err == nil {
		err = errors.New("account registry unavailable")
	}
	return &Registry{loadErr: err}
}

// LoadErr reports why the registry failed to load, or nil.
func (r *Registry) LoadErr() error { return r.loadErr }

// Path is the registry's accounts.json, which a long-lived holder can stat
// to tell whether a Reload is due; "" for an Unavailable registry.
func (r *Registry) Path() string { return r.path }

// Reload re-reads the registry's file into the receiver, replacing its
// accounts and default with whatever is on disk now. A long-lived holder
// (the TUI, which loads its registry once at workspace activation) should
// call this before acting on stale data, so it sees writes a concurrent
// `loom account` run or another loom process made since. On success it
// clears LoadErr; on failure it latches LoadErr exactly as LoadRegistry
// would and returns it, so a Reload that raced a corrupt write leaves the
// registry refusing further writes rather than silently keeping the old
// in-memory state. A registry with no file (Unavailable) has nothing to
// reload and keeps the error that made it unavailable.
func (r *Registry) Reload() error {
	if r.path == "" {
		if r.loadErr == nil {
			r.loadErr = errors.New("account registry has no file location to reload")
		}
		return r.loadErr
	}
	fresh := LoadRegistry(filepath.Dir(r.path))
	r.DefaultAccount, r.Accounts, r.loadErr = fresh.DefaultAccount, fresh.Accounts, fresh.loadErr
	return r.loadErr
}

// AccountsDir is where Create makes account dirs: <globalDir>/accounts.
func (r *Registry) AccountsDir() string {
	return filepath.Join(filepath.Dir(r.path), accountsDirName)
}

// HasExtra reports whether any account besides DefaultName is registered.
func (r *Registry) HasExtra() bool { return len(r.Accounts) > 0 }

// Get returns the registered account called name.
func (r *Registry) Get(name string) (Account, bool) {
	for _, a := range r.Accounts {
		if a.Name == name {
			return a, true
		}
	}
	return Account{}, false
}

// Default is the account new sessions preselect: DefaultAccount when it
// still names a registered account, else DefaultName.
func (r *Registry) Default() string {
	if _, ok := r.Get(r.DefaultAccount); ok {
		return r.DefaultAccount
	}
	return DefaultName
}

// Names lists every account: DefaultName first, then registration order.
func (r *Registry) Names() []string {
	names := []string{DefaultName}
	for _, a := range r.Accounts {
		names = append(names, a.Name)
	}
	return names
}

// Dirs maps each extra account's name to its config dir.
func (r *Registry) Dirs() map[string]string {
	dirs := make(map[string]string, len(r.Accounts))
	for _, a := range r.Accounts {
		dirs[a.Name] = a.Dir
	}
	return dirs
}

// EnvFor returns the environment entry that points Claude at config dir dir.
func EnvFor(dir string) []string { return []string{"CLAUDE_CONFIG_DIR=" + dir} }

// Env returns the environment entries that run Claude as name: nil for
// DefaultName (or ""), CLAUDE_CONFIG_DIR for a registered account, and an
// error for anything else.
func (r *Registry) Env(name string) ([]string, error) {
	if name == "" || name == DefaultName {
		return nil, nil
	}
	a, ok := r.Get(name)
	if !ok {
		return nil, fmt.Errorf("account %q is not registered", name)
	}
	return EnvFor(a.Dir), nil
}

// SetDefault makes name the account new sessions preselect.
func (r *Registry) SetDefault(name string) error {
	return r.update(func(fresh *Registry) error {
		if name == DefaultName {
			fresh.DefaultAccount = ""
			return nil
		}
		if _, ok := fresh.Get(name); !ok {
			return fmt.Errorf("account %q is not registered", name)
		}
		fresh.DefaultAccount = name
		return nil
	})
}

var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// maxNameLen bounds an account name: it becomes a directory name and
// appears in badges and the launch options row, where an unbounded name
// would overflow the layout.
const maxNameLen = 32

// ValidName checks an account name: lowercase letters, digits and dashes,
// not starting with a dash, at most maxNameLen characters, and not
// DefaultName. The name becomes a directory name and appears in badges.
func ValidName(name string) error {
	if name == DefaultName {
		return fmt.Errorf("%q is reserved for the account Claude uses without loom", DefaultName)
	}
	if len(name) > maxNameLen {
		return fmt.Errorf("invalid account name %q: longer than %d characters", name, maxNameLen)
	}
	if !validName.MatchString(name) {
		return fmt.Errorf("invalid account name %q: use lowercase letters, digits and dashes", name)
	}
	return nil
}

// update reloads the file, applies fn to the fresh copy, writes it back,
// and adopts the result. It refuses when this registry or the fresh load
// failed to load.
func (r *Registry) update(fn func(fresh *Registry) error) error {
	if r.loadErr != nil {
		return fmt.Errorf("%w: %v", ErrRegistryLoadFailed, r.loadErr)
	}
	fresh := LoadRegistry(filepath.Dir(r.path))
	if fresh.loadErr != nil {
		return fmt.Errorf("%w: %v", ErrRegistryLoadFailed, fresh.loadErr)
	}
	if err := fn(fresh); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(r.path), err)
	}
	data, err := json.MarshalIndent(fresh, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal accounts: %w", err)
	}
	if err := config.AtomicWriteFile(r.path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", r.path, err)
	}
	r.DefaultAccount, r.Accounts = fresh.DefaultAccount, fresh.Accounts
	return nil
}
