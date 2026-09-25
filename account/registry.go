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
	}
	return r
}

// Unavailable is a registry that could not even be located (no global
// config dir). It holds no accounts and refuses every write.
func Unavailable(err error) *Registry {
	return &Registry{loadErr: err}
}

// LoadErr reports why the registry failed to load, or nil.
func (r *Registry) LoadErr() error { return r.loadErr }

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

// ValidName checks an account name: lowercase letters, digits and dashes,
// not starting with a dash, and not DefaultName. The name becomes a
// directory name and appears in badges.
func ValidName(name string) error {
	if name == DefaultName {
		return fmt.Errorf("%q is reserved for the account Claude uses without loom", DefaultName)
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
