package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/session/hooks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// memoryFixture is an account linked to its main config dir the way
// account.Create links it (projects/ and settings.json are symlinks into
// mainDir), and a repository reached through a symlinked parent, as a
// workspace under ~/Source -> /tb/Source is.
type memoryFixture struct {
	mainDir, acctDir string
	repo, physRepo   string
}

func newMemoryFixture(t *testing.T) memoryFixture {
	t.Helper()
	mainDir := filepath.Join(t.TempDir(), "main")
	require.NoError(t, os.MkdirAll(filepath.Join(mainDir, "projects"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(mainDir, "settings.json"), []byte(`{"model":"opus"}`), 0o600))
	acctDir := filepath.Join(t.TempDir(), "accounts", "max-2")
	require.NoError(t, os.MkdirAll(acctDir, 0o700))
	_, err := account.Sync(acctDir, mainDir)
	require.NoError(t, err)

	real := filepath.Join(t.TempDir(), "real")
	require.NoError(t, os.MkdirAll(filepath.Join(real, "repo", ".git"), 0o700))
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(real, link))
	physRepo, err := filepath.EvalSymlinks(filepath.Join(real, "repo"))
	require.NoError(t, err)
	return memoryFixture{mainDir: mainDir, acctDir: acctDir, repo: filepath.Join(link, "repo"), physRepo: physRepo}
}

// wantDir is where Claude keeps the repository's memory once projects/ is
// resolved: the main dir's projects/, keyed by the physical repo root.
func (f memoryFixture) wantDir(t *testing.T) string {
	t.Helper()
	projects, err := filepath.EvalSymlinks(filepath.Join(f.mainDir, "projects"))
	require.NoError(t, err)
	return filepath.Join(projects, strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, f.physRepo), "memory")
}

// The account's projects/ is a link into the main dir, so Claude would
// check every memory write under both spellings and prompt. The resolved
// dir gives it one spelling, inside its own memory carve-out.
func TestAccountMemoryDir_ResolvesTheLinkedProjectsDir(t *testing.T) {
	f := newMemoryFixture(t)

	got, why := accountMemoryDir(f.acctDir, f.repo)

	assert.Equal(t, f.wantDir(t), got, why)
}

func TestAccountMemoryDir_LeavesClaudesOwnPathAlone(t *testing.T) {
	f := newMemoryFixture(t)
	realProjects := filepath.Join(t.TempDir(), "acct-with-own-projects")
	require.NoError(t, os.MkdirAll(filepath.Join(realProjects, "projects"), 0o700))

	cases := map[string]string{
		"default account":     "",
		"unlinked projects/":  realProjects,
		"no projects/ at all": t.TempDir(),
	}
	for name, dir := range cases {
		t.Run(name, func(t *testing.T) {
			got, why := accountMemoryDir(dir, f.repo)
			assert.Empty(t, got)
			assert.Empty(t, why, "Claude's own dir works, so there is nothing to report")
		})
	}
}

// A dir the user chose in their own settings must win: loom's flag
// settings outrank userSettings, so setting one would move their memory.
// A settings file loom can't read might be choosing one too.
func TestAccountMemoryDir_UserSettingsMayChooseTheirOwn(t *testing.T) {
	for name, body := range map[string]string{
		"chosen":     `{"autoMemoryDirectory":"~/mem"}`,
		"unreadable": `{not json`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newMemoryFixture(t)
			require.NoError(t, os.WriteFile(filepath.Join(f.mainDir, "settings.json"), []byte(body), 0o600))

			got, why := accountMemoryDir(f.acctDir, f.repo)

			assert.Empty(t, got)
			assert.NotEmpty(t, why)
		})
	}
}

// Claude keys a linked worktree's memory on the main repository it reads
// from the .git file, and gives up on any layout it can't follow. Loom
// computes the key only for a repository with a .git directory, where
// both agree.
func TestAccountMemoryDir_OnlyForARepositoryWithAGitDir(t *testing.T) {
	f := newMemoryFixture(t)
	wt := filepath.Join(t.TempDir(), "wt")
	require.NoError(t, os.MkdirAll(wt, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: /elsewhere/.git/worktrees/wt\n"), 0o600))

	for name, repo := range map[string]string{
		"linked worktree": wt,
		"no repository":   t.TempDir(),
		"missing":         filepath.Join(t.TempDir(), "gone"),
	} {
		t.Run(name, func(t *testing.T) {
			got, why := accountMemoryDir(f.acctDir, repo)
			assert.Empty(t, got)
			assert.NotEmpty(t, why)
		})
	}
}

// Past 200 characters Claude appends a hash loom can't compute; a guessed
// key would point the session at an empty memory dir.
func TestAccountMemoryDir_RefusesAKeyClaudeWouldHash(t *testing.T) {
	f := newMemoryFixture(t)
	long := filepath.Join(t.TempDir(), strings.Repeat("d", 210))
	require.NoError(t, os.MkdirAll(filepath.Join(long, ".git"), 0o700))

	got, why := accountMemoryDir(f.acctDir, long)

	assert.Empty(t, got)
	assert.Contains(t, why, "long")
}

func TestAccountMemoryDir_DanglingProjectsLinkGetsNone(t *testing.T) {
	f := newMemoryFixture(t)
	require.NoError(t, os.RemoveAll(filepath.Join(f.mainDir, "projects")))

	got, why := accountMemoryDir(f.acctDir, f.repo)

	assert.Empty(t, got)
	assert.NotEmpty(t, why)
}

func hooksSettings(t *testing.T, inst *Instance) map[string]any {
	t.Helper()
	data, err := os.ReadFile(hooks.SettingsPath(SubagentHooksDir(inst.ConfigDir, inst.Title)))
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(data, &doc))
	return doc
}

func TestLaunchProgram_AccountSessionGetsItsResolvedMemoryDir(t *testing.T) {
	f := newMemoryFixture(t)
	inst := hooksInstance(t, "claude")
	inst.Path = f.repo

	got := inst.launchProgram(LaunchEnv{Program: "claude", ClaudeConfigDir: f.acctDir}, true)

	assert.Contains(t, got, settingsFlag(inst))
	assert.Equal(t, f.wantDir(t), hooksSettings(t, inst)["autoMemoryDirectory"])
}

func TestLaunchProgram_DefaultAccountSetsNoMemoryDir(t *testing.T) {
	f := newMemoryFixture(t)
	inst := hooksInstance(t, "claude")
	inst.Path = f.repo

	inst.launchProgram(LaunchEnv{Program: "claude"}, true)

	assert.NotContains(t, hooksSettings(t, inst), "autoMemoryDirectory")
}
