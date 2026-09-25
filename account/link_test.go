package account

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mainDirWith builds a main config dir holding shared entries and the
// per-account ones the deny-list keeps out.
func mainDirWith(t *testing.T) string {
	t.Helper()
	main := t.TempDir()
	for _, f := range []string{"CLAUDE.md", "RTK.md", "settings.json", ".credentials.json", ".claude.json", ".claude.json.backup"} {
		require.NoError(t, os.WriteFile(filepath.Join(main, f), []byte(f), 0o600))
	}
	for _, d := range []string{"projects", "skills", "sessions", "daemon"} {
		require.NoError(t, os.Mkdir(filepath.Join(main, d), 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(main, "projects", "keep.jsonl"), []byte("x"), 0o600))
	return main
}

func notExist(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Lstat(path)
	return errors.Is(err, fs.ErrNotExist)
}

func TestSync_LinksSharedEntriesOnly(t *testing.T) {
	main, acct := mainDirWith(t), t.TempDir()

	rep, err := Sync(acct, main)
	require.NoError(t, err)

	assert.Equal(t, []string{"CLAUDE.md", "RTK.md", "projects", "settings.json", "skills"}, rep.Linked)
	assert.Empty(t, rep.Diverged)
	target, err := os.Readlink(filepath.Join(acct, "projects"))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(main, "projects"), target)
	for _, private := range []string{".credentials.json", ".claude.json", ".claude.json.backup", "sessions", "daemon"} {
		assert.True(t, notExist(t, filepath.Join(acct, private)), "%s must stay per account", private)
	}
}

func TestSync_IsIdempotent(t *testing.T) {
	main, acct := mainDirWith(t), t.TempDir()
	_, err := Sync(acct, main)
	require.NoError(t, err)

	rep, err := Sync(acct, main)
	require.NoError(t, err)
	assert.Empty(t, rep.Linked)
	assert.Empty(t, rep.Diverged)
}

func TestSync_ReportsADivergedRealFileAndKeepsIt(t *testing.T) {
	main, acct := mainDirWith(t), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(acct, "settings.json"), []byte("mine"), 0o600))

	rep, err := Sync(acct, main)
	require.NoError(t, err)

	assert.Equal(t, []string{"settings.json"}, rep.Diverged)
	data, err := os.ReadFile(filepath.Join(acct, "settings.json"))
	require.NoError(t, err)
	assert.Equal(t, "mine", string(data), "a diverged file may hold the only copy of a change")
}

func TestSync_PicksUpNewMainEntries(t *testing.T) {
	main, acct := mainDirWith(t), t.TempDir()
	_, err := Sync(acct, main)
	require.NoError(t, err)
	require.NoError(t, os.Mkdir(filepath.Join(main, "agents"), 0o755))

	rep, err := Sync(acct, main)
	require.NoError(t, err)
	assert.Equal(t, []string{"agents"}, rep.Linked)
}

func TestSync_MissingMainDirFails(t *testing.T) {
	_, err := Sync(t.TempDir(), filepath.Join(t.TempDir(), "nope"))
	assert.Error(t, err)
}

func TestCreate_MakesALinkedDirAndRegistersIt(t *testing.T) {
	global, main := t.TempDir(), mainDirWith(t)
	r := LoadRegistry(global)

	acct, rep, err := r.Create("max-2", main)
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(global, "accounts", "max-2"), acct.Dir)
	assert.Contains(t, rep.Linked, "projects")
	fi, err := os.Stat(acct.Dir)
	require.NoError(t, err)
	assert.Equal(t, fs.FileMode(0o700), fi.Mode().Perm(), "the dir will hold credentials")
	got, ok := LoadRegistry(global).Get("max-2")
	require.True(t, ok)
	assert.Equal(t, acct, got)
}

func TestCreate_RejectsInvalidAndDuplicateNames(t *testing.T) {
	r, main := LoadRegistry(t.TempDir()), mainDirWith(t)
	_, _, err := r.Create("Bad Name", main)
	assert.Error(t, err)
	_, _, err = r.Create(DefaultName, main)
	assert.Error(t, err)
	_, _, err = r.Create("max-2", main)
	require.NoError(t, err)
	_, _, err = r.Create("max-2", main)
	assert.Error(t, err)
}

func TestCreate_FailureLeavesNothingBehind(t *testing.T) {
	global := t.TempDir()
	r := LoadRegistry(global)

	_, _, err := r.Create("max-2", filepath.Join(global, "no-such-main"))

	require.Error(t, err)
	assert.True(t, notExist(t, filepath.Join(global, "accounts", "max-2")))
	assert.False(t, LoadRegistry(global).HasExtra())
}

func TestCreate_UpdateFailureCleansUpTheDir(t *testing.T) {
	global, main := t.TempDir(), mainDirWith(t)
	r := LoadRegistry(global)
	// Corrupt the on-disk registry after r's own load, so Sync succeeds
	// but the update step (which reloads from disk before writing) fails.
	require.NoError(t, os.MkdirAll(global, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(global, "accounts.json"), []byte("not json"), 0o644))

	_, _, err := r.Create("max-2", main)

	require.Error(t, err)
	assert.True(t, notExist(t, filepath.Join(global, "accounts", "max-2")), "the dir Sync populated must not survive a failed registration")
}

func TestCreate_RejectsARelativeMainDir(t *testing.T) {
	r := LoadRegistry(t.TempDir())
	_, _, err := r.Create("max-2", "relative/path")
	assert.Error(t, err)
}

func TestCreate_RejectsAMainDirInsideAccountsDir(t *testing.T) {
	global := t.TempDir()
	r := LoadRegistry(global)
	require.NoError(t, os.MkdirAll(r.AccountsDir(), 0o755))

	_, _, err := r.Create("max-2", r.AccountsDir())
	assert.Error(t, err)

	nested := filepath.Join(r.AccountsDir(), "other-account")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	_, _, err = r.Create("max-2", nested)
	assert.Error(t, err)
}

// TestCreate_RejectsAMainDirContainingAccountsDir covers the reverse
// direction: a mainDir that is AccountsDir's own parent (or an ancestor
// further up) would have Sync link "accounts" itself — the dir Create is
// about to populate — into every account it makes.
func TestCreate_RejectsAMainDirContainingAccountsDir(t *testing.T) {
	global := t.TempDir()
	r := LoadRegistry(global)

	_, _, err := r.Create("max-2", global)
	assert.Error(t, err, "mainDir == globalDir, AccountsDir's own parent")

	_, _, err = r.Create("max-2", filepath.Dir(global))
	assert.Error(t, err, "mainDir an ancestor further up")
}

func TestWithin_ResolvesASymlinkDisguisingContainment(t *testing.T) {
	global := t.TempDir()
	accountsDir := filepath.Join(global, "accounts")
	require.NoError(t, os.MkdirAll(filepath.Join(accountsDir, "max-2"), 0o755))

	// A path elsewhere that merely symlinks into accountsDir: its raw
	// string shares no prefix with accountsDir, but it resolves to a path
	// inside it.
	elsewhere := t.TempDir()
	disguised := filepath.Join(elsewhere, "looks-unrelated")
	require.NoError(t, os.Symlink(accountsDir, disguised))

	assert.True(t, within(accountsDir, disguised))
}

func TestWithin_UnrelatedPathsAreNotWithin(t *testing.T) {
	base := t.TempDir()
	other := t.TempDir()
	assert.False(t, within(base, other))
}

func TestWithin_FallsBackToCleanForAPathThatDoesNotExistYet(t *testing.T) {
	base := t.TempDir()
	notYetCreated := filepath.Join(base, "accounts", "max-2")
	assert.True(t, within(filepath.Join(base, "accounts"), notYetCreated))
	assert.False(t, within(filepath.Join(base, "accounts"), filepath.Join(base, "other")))
}

// TestWithin_ResolvesASymlinkedAncestorOfANotYetExistingPath covers a gap
// in the not-yet-existing fallback: falling straight back to Clean skips
// symlink resolution entirely, so a symlinked ancestor earlier in the path
// (this machine's own $HOME, say — /home/aidanb/Source is itself a
// symlink here) was never followed for a path that doesn't exist in full.
// within must instead resolve the nearest existing ancestor and re-append
// the missing tail.
func TestWithin_ResolvesASymlinkedAncestorOfANotYetExistingPath(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	require.NoError(t, os.MkdirAll(real, 0o755))
	link := filepath.Join(root, "link")
	require.NoError(t, os.Symlink(real, link))

	// "link/deep/accounts" doesn't exist at all (not even "deep" under
	// link), but link itself resolves to real, so real contains it.
	notYetCreated := filepath.Join(link, "deep", "accounts")
	assert.True(t, within(real, notYetCreated))
	assert.False(t, within(real+"-other", notYetCreated))
}

// TestCreate_RejectsAMainDirContainingAccountsDirThroughASymlinkedAncestor
// is the end-to-end version: AccountsDir's own parent (the global dir) is
// reached only through a symlink and AccountsDir itself does not exist
// yet on the first Create, exactly the case a plain Clean fallback missed.
func TestCreate_RejectsAMainDirContainingAccountsDirThroughASymlinkedAncestor(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	require.NoError(t, os.MkdirAll(real, 0o755))
	global := filepath.Join(root, "link")
	require.NoError(t, os.Symlink(real, global))
	r := LoadRegistry(global)

	// mainDir is the resolved target global's symlink points to, so it
	// contains AccountsDir even though AccountsDir's own parent path is a
	// symlink and AccountsDir does not exist on disk yet.
	_, _, err := r.Create("max-2", real)

	assert.Error(t, err)
}

func TestRemove_DeletesLinksButNeverTheirTargets(t *testing.T) {
	global, main := t.TempDir(), mainDirWith(t)
	r := LoadRegistry(global)
	acct, _, err := r.Create("max-2", main)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(acct.Dir, ".credentials.json"), []byte("secret"), 0o600))

	deleted, err := r.Remove("max-2", false)

	require.NoError(t, err)
	assert.True(t, deleted)
	assert.True(t, notExist(t, acct.Dir))
	data, err := os.ReadFile(filepath.Join(main, "projects", "keep.jsonl"))
	require.NoError(t, err, "shared content behind the links must survive")
	assert.Equal(t, "x", string(data))
	assert.False(t, LoadRegistry(global).HasExtra())
}

func TestRemove_ClearsTheDefaultAndRefusesDefaultAccount(t *testing.T) {
	global, main := t.TempDir(), mainDirWith(t)
	r := LoadRegistry(global)
	_, _, err := r.Create("max-2", main)
	require.NoError(t, err)
	require.NoError(t, r.SetDefault("max-2"))

	_, err = r.Remove(DefaultName, false)
	assert.Error(t, err)
	_, err = r.Remove("max-2", false)
	require.NoError(t, err)
	assert.Equal(t, DefaultName, LoadRegistry(global).Default())
}

// TestRemove_OutsideAccountsDirOnlyUnregisters keeps Remove's own
// ownership defence tested for a Registry built directly in memory,
// bypassing LoadRegistry's validation. Adopting a foreign dir through the
// loader itself is refused outright — see
// TestLoadRegistry_RejectsInvalidStoredEntries's "foreign dir" case — so
// this can no longer happen via a Registry a real `loom account` run or
// the TUI would ever hold; the in-memory defence is what is left to test.
func TestRemove_OutsideAccountsDirOnlyUnregisters(t *testing.T) {
	global, outside := t.TempDir(), t.TempDir()
	r := &Registry{path: filepath.Join(global, "accounts.json"), Accounts: []Account{{Name: "byo", Dir: outside}}}

	deleted, err := r.Remove("byo", false)

	require.NoError(t, err)
	assert.False(t, deleted)
	_, err = os.Stat(outside)
	assert.NoError(t, err, "loom only deletes dirs it created")
}

// TestRemove_NeverDeletesOutsideItsOwnAccountDir reproduces three ways a
// hand-edited or corrupt registry's stored Dir could point Remove at
// something other than the account's own <AccountsDir>/<name>: a parent
// escape, a bare-prefix collision from a missing trailing separator, and a
// path reaching through the account's own symlinks into the main dir's
// real content. Remove must recompute the trusted path from name and
// AccountsDir(), never delete based on the stored Dir, and refuse
// (unregister only) whenever they disagree.
func TestRemove_NeverDeletesOutsideItsOwnAccountDir(t *testing.T) {
	global := t.TempDir()
	accountsDir := filepath.Join(global, "accounts")
	require.NoError(t, os.MkdirAll(accountsDir, 0o755))
	sentinel := filepath.Join(global, "sentinel.txt")
	require.NoError(t, os.WriteFile(sentinel, []byte("keep"), 0o600))

	cases := map[string]string{
		// Raw string concatenation, not filepath.Join: a stored Dir field
		// is whatever string was in the JSON, with no Go-side cleaning. A
		// naive prefix check on the uncleaned string ("<accountsDir>/..")
		// still matches the "<accountsDir>/" prefix even though it
		// resolves to accountsDir's own parent.
		"parent-escape":  accountsDir + string(filepath.Separator) + "..",
		"trailing-slash": accountsDir + string(filepath.Separator),
		"through-a-link": filepath.Join(accountsDir, "max-2", "projects", "myproj"),
	}
	for label, dir := range cases {
		t.Run(label, func(t *testing.T) {
			r := &Registry{path: filepath.Join(global, "accounts.json"), Accounts: []Account{{Name: "max-2", Dir: dir}}}

			deleted, err := r.Remove("max-2", true)

			require.NoError(t, err)
			assert.False(t, deleted, "must not claim to have deleted an untrusted dir")
			_, statErr := os.Stat(sentinel)
			assert.NoError(t, statErr, "must never touch anything outside the account's own dir")
			_, statErr = os.Stat(accountsDir)
			assert.NoError(t, statErr, "the accounts dir itself must survive")
		})
	}

	// A fifth way the stored Dir can disguise itself as owned, and the
	// one that actually got through: filepath.Clean simplifies
	// "lnk/../max-2" lexically to "max-2", so filepath.Clean(acct.Dir) ==
	// want passes even though the *kernel* resolves "lnk" to its real
	// symlink target first and only then applies "..", landing somewhere
	// else entirely. OwnedDir must hand Remove the canonical path itself
	// once it decides "owned" — never the stored string, even when the
	// stored string looks identical to the canonical one after Clean.
	t.Run("lnk-dotdot", func(t *testing.T) {
		victim := t.TempDir()
		sub := filepath.Join(victim, "sub")
		require.NoError(t, os.MkdirAll(sub, 0o755))
		require.NoError(t, os.MkdirAll(filepath.Join(victim, "max-2"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(victim, "max-2", "precious"), []byte("keep"), 0o600))
		require.NoError(t, os.Symlink(sub, filepath.Join(accountsDir, "lnk")))
		// Raw concatenation, not filepath.Join, which would clean away
		// the "lnk/.." itself before it's even stored — the same reason
		// "parent-escape" above avoids it.
		dir := filepath.Join(accountsDir, "lnk") + string(filepath.Separator) + ".." + string(filepath.Separator) + "max-2"
		require.Equal(t, filepath.Join(accountsDir, "max-2"), filepath.Clean(dir), "sanity: Clean must make this look owned")

		r := &Registry{path: filepath.Join(global, "accounts.json"), Accounts: []Account{{Name: "max-2", Dir: dir}}}

		_, err := r.Remove("max-2", true)

		require.NoError(t, err)
		_, statErr := os.Stat(filepath.Join(victim, "max-2", "precious"))
		assert.NoError(t, statErr, "must never delete through a symlink+.. that only *looks* like the canonical path after Clean")
	})
}

// TestRemove_GuardsAgainstAnInvalidNameEvenIfConstructedDirectly checks the
// ValidName(name) half of the ownership guard directly: LoadRegistry now
// refuses to load an entry named "..", but Remove must not rely on that —
// a Registry can still be built in-process without going through the
// loader.
func TestRemove_GuardsAgainstAnInvalidNameEvenIfConstructedDirectly(t *testing.T) {
	global := t.TempDir()
	r := &Registry{path: filepath.Join(global, "accounts.json"), Accounts: []Account{{Name: "..", Dir: filepath.Join(global, "accounts", "..")}}}

	deleted, err := r.Remove("..", true)

	require.NoError(t, err)
	assert.False(t, deleted)
}

func TestRemove_RefusesWhenAccountHoldsUnsharedFiles(t *testing.T) {
	global, main := t.TempDir(), mainDirWith(t)
	r := LoadRegistry(global)
	acct, _, err := r.Create("max-2", main)
	require.NoError(t, err)
	// Replace the settings.json link with a real file: diverged content
	// that exists nowhere but this account.
	require.NoError(t, os.Remove(filepath.Join(acct.Dir, "settings.json")))
	require.NoError(t, os.WriteFile(filepath.Join(acct.Dir, "settings.json"), []byte("mine"), 0o600))

	deleted, err := r.Remove("max-2", false)

	var uerr *UnsharedError
	require.ErrorAs(t, err, &uerr)
	assert.Equal(t, "max-2", uerr.Name)
	assert.Contains(t, uerr.Entries, "settings.json")
	assert.Contains(t, err.Error(), "settings.json")
	assert.Contains(t, err.Error(), "--force")
	assert.False(t, deleted)
	_, statErr := os.Stat(acct.Dir)
	assert.NoError(t, statErr, "a refused removal must leave the account intact")
	_, ok := LoadRegistry(global).Get("max-2")
	assert.True(t, ok, "a refused removal must leave it registered")
}

func TestRemove_ForceDeletesDespiteUnsharedFiles(t *testing.T) {
	global, main := t.TempDir(), mainDirWith(t)
	r := LoadRegistry(global)
	acct, _, err := r.Create("max-2", main)
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(acct.Dir, "settings.json")))
	require.NoError(t, os.WriteFile(filepath.Join(acct.Dir, "settings.json"), []byte("mine"), 0o600))

	deleted, err := r.Remove("max-2", true)

	require.NoError(t, err)
	assert.True(t, deleted)
	assert.True(t, notExist(t, acct.Dir))
	assert.False(t, LoadRegistry(global).HasExtra())
}

func TestUnshared_EmptyRightAfterCreate(t *testing.T) {
	main, acct := mainDirWith(t), t.TempDir()
	_, err := Sync(acct, main)
	require.NoError(t, err)

	list, err := Unshared(acct)

	require.NoError(t, err)
	assert.Empty(t, list)
}

func TestUnshared_ListsDivergedAndAccountOnlyRealEntries(t *testing.T) {
	main, acct := mainDirWith(t), t.TempDir()
	_, err := Sync(acct, main)
	require.NoError(t, err)
	// A diverged file: real content replacing the link.
	require.NoError(t, os.Remove(filepath.Join(acct, "settings.json")))
	require.NoError(t, os.WriteFile(filepath.Join(acct, "settings.json"), []byte("mine"), 0o600))
	// A dir Claude created in the account before main ever had one of that
	// name, so Sync never linked it.
	require.NoError(t, os.Mkdir(filepath.Join(acct, "agents"), 0o755))
	// Per-account state (deny-listed) must never be reported as unshared.
	require.NoError(t, os.WriteFile(filepath.Join(acct, ".credentials.json"), []byte("secret"), 0o600))

	list, err := Unshared(acct)

	require.NoError(t, err)
	assert.Equal(t, []string{"agents", "settings.json"}, list)
}

func TestUnshared_MissingDirIsEmpty(t *testing.T) {
	list, err := Unshared(filepath.Join(t.TempDir(), "gone"))
	require.NoError(t, err)
	assert.Empty(t, list)
}

func TestSync_ExcludesRuntimeLogAndJobState(t *testing.T) {
	main, acct := t.TempDir(), t.TempDir()
	for _, f := range []string{"daemon.log", "stats-cache.json", ".last-cleanup"} {
		require.NoError(t, os.WriteFile(filepath.Join(main, f), []byte("x"), 0o600))
	}
	require.NoError(t, os.Mkdir(filepath.Join(main, "jobs"), 0o755))

	rep, err := Sync(acct, main)

	require.NoError(t, err)
	assert.Empty(t, rep.Linked)
	for _, name := range []string{"daemon.log", "stats-cache.json", ".last-cleanup", "jobs"} {
		assert.True(t, notExist(t, filepath.Join(acct, name)), "%s must stay per account", name)
	}
}

func TestSync_ExcludesCredentialsAndClaudeJsonVariants(t *testing.T) {
	main, acct := t.TempDir(), t.TempDir()
	for _, f := range []string{".credentials.json", ".credentials.json.bak", ".claude.json", ".claude.json.backup"} {
		require.NoError(t, os.WriteFile(filepath.Join(main, f), []byte("x"), 0o600))
	}

	rep, err := Sync(acct, main)

	require.NoError(t, err)
	assert.Empty(t, rep.Linked)
}

// TestSync_ExcludesPerSessionSecurityWarningsState covers a false positive
// found in practice: Claude writes a security_warnings_state_<uuid>.json
// per session, so the main dir accumulates several. Linking them would
// make Sync try to relink a growing, unstable set on every run, and
// Unshared would report an account's own copy (written directly under it,
// never through a link) as content that would be lost on removal, refusing
// every account that ever ran a session.
func TestSync_ExcludesPerSessionSecurityWarningsState(t *testing.T) {
	main, acct := t.TempDir(), t.TempDir()
	for _, id := range []string{"11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"} {
		require.NoError(t, os.WriteFile(filepath.Join(main, "security_warnings_state_"+id+".json"), []byte("x"), 0o600))
	}

	rep, err := Sync(acct, main)

	require.NoError(t, err)
	assert.Empty(t, rep.Linked)
	for _, id := range []string{"11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"} {
		assert.True(t, notExist(t, filepath.Join(acct, "security_warnings_state_"+id+".json")))
	}
}

func TestUnshared_IgnoresAnAccountsOwnSecurityWarningsState(t *testing.T) {
	acct := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(acct, "security_warnings_state_33333333-3333-3333-3333-333333333333.json"), []byte("x"), 0o600))

	list, err := Unshared(acct)

	require.NoError(t, err)
	assert.Empty(t, list)
}

// TestValidateMainDir_MatchesWhatCreateRejects exercises the extracted
// guard directly with the same cases Create's own tests cover (relative,
// inside AccountsDir, containing AccountsDir), plus the accepting case, so
// a caller resolving mainDir itself (the CLI, before Create ever runs) can
// rely on identical checks.
func TestValidateMainDir_MatchesWhatCreateRejects(t *testing.T) {
	global := t.TempDir()
	r := LoadRegistry(global)
	require.NoError(t, os.MkdirAll(r.AccountsDir(), 0o755))

	assert.Error(t, ValidateMainDir("relative/path", r.AccountsDir()))
	assert.Error(t, ValidateMainDir(r.AccountsDir(), r.AccountsDir()), "inside AccountsDir")
	assert.Error(t, ValidateMainDir(global, r.AccountsDir()), "AccountsDir's own parent")
	assert.NoError(t, ValidateMainDir(mainDirWith(t), r.AccountsDir()))
}

func TestOwnedDir_TrueForACreatedAccount(t *testing.T) {
	global, main := t.TempDir(), mainDirWith(t)
	r := LoadRegistry(global)
	acct, _, err := r.Create("max-2", main)
	require.NoError(t, err)

	dir, owned := r.OwnedDir("max-2")

	assert.True(t, owned)
	assert.Equal(t, acct.Dir, dir)
}

func TestOwnedDir_FalseForADirOutsideAccountsDir(t *testing.T) {
	global, outside := t.TempDir(), t.TempDir()
	r := LoadRegistry(global)
	require.NoError(t, r.update(func(f *Registry) error {
		f.Accounts = append(f.Accounts, Account{Name: "byo", Dir: outside})
		return nil
	}))

	dir, owned := r.OwnedDir("byo")

	assert.False(t, owned)
	assert.Equal(t, outside, dir, "still reports the account's real dir, just not as one Remove would delete")
}

func TestOwnedDir_FalseWhenNotRegistered(t *testing.T) {
	r := LoadRegistry(t.TempDir())

	dir, owned := r.OwnedDir("nope")

	assert.False(t, owned)
	assert.Empty(t, dir)
}
