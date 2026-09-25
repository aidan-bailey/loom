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
	"daemon.log":        true,
	"session-env":       true,
	"ide":               true,
	"debug":             true,
	"cache":             true,
	"backups":           true,
	"shell-snapshots":   true,
	"statsig":           true,
	"jobs":              true,
	"stats-cache.json":  true,
	".last-cleanup":     true,
}

// shared reports whether main-dir entry name is linked into account dirs.
// Besides the deny-list, .claude.json's and .credentials.json's siblings
// (their backups and atomic-write temp files) stay per account too.
func shared(name string) bool {
	return !sharedDenyList[name] &&
		!strings.HasPrefix(name, ".claude.json") &&
		!strings.HasPrefix(name, ".credentials.json")
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

// within reports whether target is base itself or nested inside it, after
// cleaning both. Used to refuse a main dir that is really an account's own
// tree (a nested loom running as an account, pointed at its own accounts
// dir by mistake).
func within(base, target string) bool {
	base, target = filepath.Clean(base), filepath.Clean(target)
	return target == base || strings.HasPrefix(target, base+string(filepath.Separator))
}

// Create registers a new account called name: it makes the account's
// config dir under AccountsDir, links mainDir's shared entries into it,
// and records it. The dir must not exist yet. mainDir must be an absolute
// path outside AccountsDir. Logging in is the caller's next step.
func (r *Registry) Create(name, mainDir string) (Account, SyncReport, error) {
	if err := ValidName(name); err != nil {
		return Account{}, SyncReport{}, err
	}
	if r.loadErr != nil {
		return Account{}, SyncReport{}, fmt.Errorf("%w: %v", ErrRegistryLoadFailed, r.loadErr)
	}
	if !filepath.IsAbs(mainDir) {
		return Account{}, SyncReport{}, fmt.Errorf("main config dir %q must be an absolute path", mainDir)
	}
	if within(r.AccountsDir(), mainDir) {
		return Account{}, SyncReport{}, fmt.Errorf("main config dir %s is inside %s; refusing to link an account's own accounts tree", mainDir, r.AccountsDir())
	}
	if _, ok := r.Get(name); ok {
		return Account{}, SyncReport{}, fmt.Errorf("account %q already exists", name)
	}
	acct := Account{Name: name, Dir: filepath.Join(r.AccountsDir(), name)}
	if err := os.MkdirAll(r.AccountsDir(), 0o755); err != nil {
		return Account{}, SyncReport{}, fmt.Errorf("create %s: %w", r.AccountsDir(), err)
	}
	if err := os.Mkdir(acct.Dir, 0o700); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return Account{}, SyncReport{}, fmt.Errorf("%s already exists; remove it or pick another name", acct.Dir)
		}
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

// UnsharedError reports that Remove refused to delete an account because it
// holds real content that is not shared with the main config dir and so
// exists nowhere else: deleting it without --force would be the only copy
// lost.
type UnsharedError struct {
	Name    string
	Entries []string
}

func (e *UnsharedError) Error() string {
	return fmt.Sprintf("account %q holds files that are not shared with your main config: %s — move them, or remove with --force",
		e.Name, strings.Join(e.Entries, ", "))
}

// Unshared lists acctDir's top-level entries that Sync would link (their
// name passes shared()) but that are real files or directories rather than
// symlinks: an entry Sync's link was replaced with (a tool rewriting it
// atomically, say), or one created directly inside the account — by Claude
// running under it, typically — before the main dir ever had an entry of
// that name for Sync to link. Sorted (os.ReadDir's own order). A missing
// acctDir reports no entries, not an error.
func Unshared(acctDir string) ([]string, error) {
	entries, err := os.ReadDir(acctDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", acctDir, err)
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if !shared(name) {
			continue
		}
		fi, err := os.Lstat(filepath.Join(acctDir, name))
		if err != nil {
			return nil, fmt.Errorf("inspect %s: %w", name, err)
		}
		if fi.Mode()&fs.ModeSymlink == 0 {
			out = append(out, name)
		}
	}
	return out, nil
}

// Remove unregisters name. When it owns the config dir it names — the
// account is registered under exactly <AccountsDir>/name, recomputed here
// rather than trusted from the stored Dir field, which a hand-edited or
// corrupt registry could point anywhere — it also deletes that dir: the
// account's credentials and state plus links into the main dir. RemoveAll
// deletes the links themselves, never what they point at.
//
// A dir the account does not own (outside AccountsDir, or a stored Dir
// disagreeing with <AccountsDir>/name) is unregistered but never deleted;
// deleted reports which happened.
//
// Unless force, an owned dir holding real (non-symlink) entries Unshared
// would report is left registered and untouched, and Remove returns an
// *UnsharedError naming them: content that exists nowhere else should not
// vanish silently. force deletes regardless.
func (r *Registry) Remove(name string, force bool) (deleted bool, err error) {
	if name == DefaultName {
		return false, fmt.Errorf("the %q account cannot be removed", DefaultName)
	}
	acct, ok := r.Get(name)
	if !ok {
		return false, fmt.Errorf("account %q is not registered", name)
	}
	want := filepath.Join(r.AccountsDir(), name)
	owned := ValidName(name) == nil && filepath.Clean(acct.Dir) == want

	if owned && !force {
		unshared, uerr := Unshared(want)
		if uerr != nil {
			return false, fmt.Errorf("account %q: checking for unshared files: %w", name, uerr)
		}
		if len(unshared) > 0 {
			return false, &UnsharedError{Name: name, Entries: unshared}
		}
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
	if !owned {
		return false, nil
	}
	if err := os.RemoveAll(want); err != nil {
		return false, fmt.Errorf("account %q unregistered, but deleting %s failed: %w", name, want, err)
	}
	return true, nil
}
