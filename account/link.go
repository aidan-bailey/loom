package account

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// sharedDenyList names the main config dir's entries that stay per
// account: its credentials and account state, and the runtime state of the
// Claude processes running under it. Everything else is linked, which is
// why this is a deny-list: a user file such as an RTK.md that CLAUDE.md
// imports by relative path must follow too.
var sharedDenyList = map[string]bool{
	".credentials.json": true,
	".claude.json":      true,
	"sessions":          true,
	"daemon":            true,
	"session-env":       true,
	"ide":               true,
	"debug":             true,
	"cache":             true,
	"backups":           true,
	"shell-snapshots":   true,
	"statsig":           true,
}

// shared reports whether main-dir entry name is linked into account dirs.
// Besides the deny-list, .claude.json's siblings (its backups and
// atomic-write temp files) stay per account too.
func shared(name string) bool {
	return !sharedDenyList[name] && !strings.HasPrefix(name, ".claude.json")
}

// SyncReport is what one Sync did. Both lists are sorted.
type SyncReport struct {
	// Linked lists the entries this run linked.
	Linked []string
	// Diverged lists entries that are real files or directories in the
	// account dir although the main dir has them too: something replaced
	// the link (a tool rewriting the file atomically through it, say).
	// Sync leaves them alone, since the account's copy may hold the only
	// copy of a change.
	Diverged []string
}

// Sync links every shared entry of mainDir that acctDir does not have yet.
// Idempotent. Existing links are left as they are, wherever they point,
// and a real file or directory is never replaced.
func Sync(acctDir, mainDir string) (SyncReport, error) {
	var rep SyncReport
	entries, err := os.ReadDir(mainDir)
	if err != nil {
		return rep, fmt.Errorf("read main config dir %s: %w", mainDir, err)
	}
	for _, e := range entries {
		name := e.Name()
		if !shared(name) {
			continue
		}
		link := filepath.Join(acctDir, name)
		fi, err := os.Lstat(link)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if err := os.Symlink(filepath.Join(mainDir, name), link); err != nil {
				return rep, fmt.Errorf("link %s: %w", name, err)
			}
			rep.Linked = append(rep.Linked, name)
		case err != nil:
			return rep, fmt.Errorf("inspect %s: %w", link, err)
		case fi.Mode()&fs.ModeSymlink == 0:
			rep.Diverged = append(rep.Diverged, name)
		}
	}
	return rep, nil
}

// Create registers a new account called name: it makes the account's
// config dir under AccountsDir, links mainDir's shared entries into it,
// and records it. The dir must not exist yet. Logging in is the caller's
// next step.
func (r *Registry) Create(name, mainDir string) (Account, SyncReport, error) {
	if err := ValidName(name); err != nil {
		return Account{}, SyncReport{}, err
	}
	if r.loadErr != nil {
		return Account{}, SyncReport{}, fmt.Errorf("%w: %v", ErrRegistryLoadFailed, r.loadErr)
	}
	if _, ok := r.Get(name); ok {
		return Account{}, SyncReport{}, fmt.Errorf("account %q already exists", name)
	}
	acct := Account{Name: name, Dir: filepath.Join(r.AccountsDir(), name)}
	if _, err := os.Lstat(acct.Dir); err == nil {
		return Account{}, SyncReport{}, fmt.Errorf("%s already exists; remove it or pick another name", acct.Dir)
	}
	if err := os.MkdirAll(acct.Dir, 0o700); err != nil {
		return Account{}, SyncReport{}, fmt.Errorf("create %s: %w", acct.Dir, err)
	}
	rep, err := Sync(acct.Dir, mainDir)
	if err == nil {
		err = r.update(func(fresh *Registry) error {
			if _, ok := fresh.Get(name); ok {
				return fmt.Errorf("account %q already exists", name)
			}
			fresh.Accounts = append(fresh.Accounts, acct)
			return nil
		})
	}
	if err != nil {
		// Nothing but links lives there yet, and RemoveAll never follows them.
		_ = os.RemoveAll(acct.Dir)
		return Account{}, SyncReport{}, err
	}
	return acct, rep, nil
}

// Remove unregisters name and deletes its config dir, which holds that
// account's credentials and state plus links into the main dir. RemoveAll
// deletes the links themselves, never what they point at. A dir outside
// AccountsDir (a hand-edited registry) is unregistered but not deleted;
// deleted reports which happened.
func (r *Registry) Remove(name string) (deleted bool, err error) {
	if name == DefaultName {
		return false, fmt.Errorf("the %q account cannot be removed", DefaultName)
	}
	acct, ok := r.Get(name)
	if !ok {
		return false, fmt.Errorf("account %q is not registered", name)
	}
	if err := r.update(func(fresh *Registry) error {
		kept := fresh.Accounts[:0]
		for _, a := range fresh.Accounts {
			if a.Name != name {
				kept = append(kept, a)
			}
		}
		fresh.Accounts = kept
		if fresh.DefaultAccount == name {
			fresh.DefaultAccount = ""
		}
		return nil
	}); err != nil {
		return false, err
	}
	if !strings.HasPrefix(acct.Dir, r.AccountsDir()+string(filepath.Separator)) {
		return false, nil
	}
	if err := os.RemoveAll(acct.Dir); err != nil {
		return false, fmt.Errorf("account %q unregistered, but deleting %s failed: %w", name, acct.Dir, err)
	}
	return true, nil
}
