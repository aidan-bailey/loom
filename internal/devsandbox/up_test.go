package devsandbox

import (
	"bytes"
	"debug/buildinfo"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func upSandbox(t *testing.T, opts UpOptions) *Sandbox {
	t.Helper()
	requireGit(t)
	useTempBase(t)
	sb, err := Open("demo")
	require.NoError(t, err)
	if opts.SourceWorktree == "" {
		opts.SourceWorktree = "/src/loom"
	}
	require.NoError(t, sb.Up(opts))
	return sb
}

func readConfig(t *testing.T, dir string) *config.Config {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, config.ConfigFileName))
	require.NoError(t, err)
	var cfg config.Config
	require.NoError(t, json.Unmarshal(data, &cfg))
	return &cfg
}

func TestUp_CreatesLayoutAndMeta(t *testing.T) {
	sb := upSandbox(t, UpOptions{})
	for _, d := range []string{sb.GlobalDir(), sb.HomeDir(), sb.RepoDir(), sb.OriginDir(), sb.WorkspaceConfigDir()} {
		assert.DirExists(t, d)
	}
	for _, name := range []string{"fakeagent", "claude", "aider"} {
		target, err := os.Readlink(filepath.Join(sb.PersonaDir(), name))
		require.NoError(t, err, name)
		assert.Equal(t, filepath.Join("..", "fakeagent"), target)
	}
	meta, err := sb.LoadMeta()
	require.NoError(t, err)
	assert.Equal(t, "demo", meta.Name)
	assert.Equal(t, "loomdev-demo", meta.Socket)
	assert.Equal(t, "/src/loom", meta.SourceWorktree)
	assert.False(t, meta.RealClaude)
}

func TestUp_WritesSandboxConfigToBothConfigDirs(t *testing.T) {
	sb := upSandbox(t, UpOptions{})
	for _, dir := range []string{sb.GlobalDir(), sb.WorkspaceConfigDir()} {
		cfg := readConfig(t, dir)
		assert.Equal(t, "fake", cfg.DefaultProgram, dir)
		assert.Equal(t, "dev/", cfg.BranchPrefix, dir)
		assert.False(t, cfg.RemoteControlEnabled(), dir)
		assert.Equal(t, []string{"fake", "fake-claude", "fake-aider", "fake-claude-trust", "shell"}, profileList(cfg.Profiles), dir)
		assert.Equal(t, filepath.Join(sb.PersonaDir(), "fakeagent"), cfg.Profiles[0].Program)
		assert.Equal(t, filepath.Join(sb.PersonaDir(), "claude"), cfg.Profiles[1].Program)
		assert.Equal(t, filepath.Join(sb.PersonaDir(), "aider"), cfg.Profiles[2].Program)
		assert.Equal(t, filepath.Join(sb.PersonaDir(), "claude")+" --trust-first", cfg.Profiles[3].Program)
	}
}

func TestSeedWorkspace_GivesAWorkspaceTheToyWorkspacesConfigAndState(t *testing.T) {
	sb := upSandbox(t, UpOptions{DefaultProfile: "fake-aider"})
	repo := t.TempDir()

	require.NoError(t, sb.SeedWorkspace(repo))

	dir := filepath.Join(repo, ".loom")
	assert.Equal(t, readConfig(t, sb.WorkspaceConfigDir()), readConfig(t, dir))
	assert.Equal(t, "fake-aider", readConfig(t, dir).DefaultProgram)
	seeded, err := os.ReadFile(filepath.Join(dir, config.StateFileName))
	require.NoError(t, err)
	assert.Contains(t, string(seeded), "help_screens_seen", "no first-run overlay stops a headless driver")
}

func TestSeedWorkspace_NeedsAnUpSandbox(t *testing.T) {
	useTempBase(t)
	sb, err := Open("demo")
	require.NoError(t, err)
	assert.Error(t, sb.SeedWorkspace(t.TempDir()))
}

func TestUp_KeepsEditedConfigUnlessFlagsGiven(t *testing.T) {
	sb := upSandbox(t, UpOptions{})
	path := filepath.Join(sb.WorkspaceConfigDir(), config.ConfigFileName)
	require.NoError(t, os.WriteFile(path, []byte(`{"default_program":"shell"}`), 0o644))

	require.NoError(t, sb.Up(UpOptions{SourceWorktree: "/src/loom"}))
	assert.Equal(t, "shell", readConfig(t, sb.WorkspaceConfigDir()).DefaultProgram)

	require.NoError(t, sb.Up(UpOptions{SourceWorktree: "/src/loom", RealClaude: true, DefaultProfile: "fake-aider"}))
	cfg := readConfig(t, sb.WorkspaceConfigDir())
	assert.Equal(t, "fake-aider", cfg.DefaultProgram)
	assert.Contains(t, profileList(cfg.Profiles), "claude")

	require.NoError(t, sb.Up(UpOptions{SourceWorktree: "/src/loom"}))
	meta, err := sb.LoadMeta()
	require.NoError(t, err)
	assert.True(t, meta.RealClaude, "--real-claude is sticky")
}

func TestUp_RejectsUnknownDefaultProfileBeforeCreatingAnything(t *testing.T) {
	requireGit(t)
	useTempBase(t)
	sb, err := Open("demo")
	require.NoError(t, err)
	err = sb.Up(UpOptions{SourceWorktree: "/src/loom", DefaultProfile: "nope"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"nope"`)
	assert.NoDirExists(t, sb.Dir)
}

func TestUp_RejectsWhitespaceInPath(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "has space"))
	sb, err := Open("demo")
	require.NoError(t, err)
	err = sb.Up(UpOptions{SourceWorktree: "/src/loom"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "whitespace")
}

func TestUp_SeedsHelpScreensSeen(t *testing.T) {
	sb := upSandbox(t, UpOptions{})
	for _, dir := range []string{sb.GlobalDir(), sb.WorkspaceConfigDir()} {
		assert.Equal(t, uint32(math.MaxUint32), config.LoadStateFrom(dir).GetHelpScreensSeen(), dir)
	}
}

func TestUp_RegistersToyWorkspaceOnce(t *testing.T) {
	sb := upSandbox(t, UpOptions{})
	require.NoError(t, sb.Up(UpOptions{SourceWorktree: "/src/loom"}))

	t.Setenv(config.EnvGlobalDir, sb.GlobalDir())
	reg, err := config.LoadWorkspaceRegistry()
	require.NoError(t, err)
	require.Len(t, reg.Workspaces, 1)
	ws := reg.Get(WorkspaceName)
	require.NotNil(t, ws)
	assert.Equal(t, sb.RepoDir(), ws.Path)
	assert.Equal(t, sb.WorkspaceConfigDir(), config.WorkspaceConfigDir(ws))
	assert.Equal(t, WorkspaceName, reg.LastUsed)
}

func TestUp_WarnsWhenSourceWorktreeChanges(t *testing.T) {
	sb := upSandbox(t, UpOptions{SourceWorktree: "/src/a"})
	var warn bytes.Buffer
	require.NoError(t, sb.Up(UpOptions{SourceWorktree: "/src/b", Warn: &warn}))
	assert.Contains(t, warn.String(), "/src/a")
	meta, err := sb.LoadMeta()
	require.NoError(t, err)
	assert.Equal(t, "/src/b", meta.SourceWorktree)
}

// A dev build turns Go's VCS stamping off and stamps the commit itself, with
// the caller's ldflags after the stamp's.
func TestGoBuildArgs(t *testing.T) {
	assert.Equal(t, []string{"build", "-o", "/out/loom", "-buildvcs=false", "-ldflags", "-X a.b=1 -X main.version=99.0.0", "."},
		goBuildArgs("/out/loom", ".", "-X main.version=99.0.0", "-X a.b=1"))
	assert.Equal(t, []string{"build", "-o", "/out/loom", "-buildvcs=false", "-ldflags", "-X a.b=1", "."},
		goBuildArgs("/out/loom", ".", "", "-X a.b=1"))
	assert.Equal(t, []string{"build", "-o", "/out/loom", "-ldflags", "-X main.version=99.0.0", "."},
		goBuildArgs("/out/loom", ".", "-X main.version=99.0.0", ""), "no checkout: built as Go stamps it")
}

// vcsLdflags stamps the commit of the checkout it is given, read with git
// there: for a worktree nested in another checkout, the worktree's commit,
// where Go's own stamping would name the enclosing checkout's.
func TestVCSLdflags_StampsTheCheckoutsOwnCommit(t *testing.T) {
	requireGit(t)
	outer := t.TempDir()
	require.NoError(t, git(outer, "init", "-q", "-b", "main"))
	for _, args := range [][]string{
		{"config", "user.name", "t"}, {"config", "user.email", "t@example.invalid"}, {"config", "commit.gpgsign", "false"},
		{"commit", "-q", "--allow-empty", "--no-verify", "-m", "outer"},
	} {
		require.NoError(t, git(outer, args...))
	}
	inner := filepath.Join(outer, "worktrees", "feature")
	require.NoError(t, git(outer, "worktree", "add", "-q", "-b", "feature", inner))
	require.NoError(t, os.WriteFile(filepath.Join(inner, "f.txt"), []byte("x"), 0o644))
	require.NoError(t, git(inner, "add", "f.txt"))
	require.NoError(t, git(inner, "commit", "-q", "--no-verify", "-m", "inner"))
	head := gitOut(t, inner, "rev-parse", "HEAD")
	require.NotEqual(t, gitOut(t, outer, "rev-parse", "HEAD"), head)
	const rpc = "-X github.com/aidan-bailey/loom/core/rpc."

	flags, err := vcsLdflags(inner)

	require.NoError(t, err)
	assert.Contains(t, flags, rpc+"commitRevision="+head)
	assert.Contains(t, flags, rpc+"commitUnix="+gitOut(t, inner, "log", "-1", "--format=%ct"))
	assert.Contains(t, flags, rpc+"commitModified=false")

	require.NoError(t, os.WriteFile(filepath.Join(inner, "f.txt"), []byte("edited"), 0o644))
	flags, err = vcsLdflags(inner)
	require.NoError(t, err)
	assert.Contains(t, flags, rpc+"commitModified=true", "an edited tree")
	assert.Contains(t, flags, rpc+"commitRevision="+head)

	flags, err = vcsLdflags(t.TempDir())
	require.NoError(t, err)
	assert.Empty(t, flags, "no checkout, no stamp")
}

func TestBuild_RequiresUp(t *testing.T) {
	useTempBase(t)
	sb, err := Open("demo")
	require.NoError(t, err)
	assert.ErrorContains(t, sb.Build("/src/loom"), "not up")
}

func TestBuild_ProducesRunnableBinaries(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles loom; skipped with -short")
	}
	sb := upSandbox(t, UpOptions{})
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	require.NoError(t, sb.Build(root))

	cmd := exec.Command(sb.LoomBin(), "version")
	cmd.Env = sb.Environ() // LOOM_HOME set: legacy-home migration stays off
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	assert.Contains(t, string(out), "loom version")
	assert.FileExists(t, sb.FakeAgentBin())

	meta, err := sb.LoadMeta()
	require.NoError(t, err)
	assert.NotEmpty(t, meta.BuildSHA)

	// From a checkout (this one may be a worktree nested in another), the
	// build names the checkout's own commit, never the enclosing one's.
	if meta.BuildSHA == "unknown" {
		return
	}
	info, err := buildinfo.ReadFile(sb.LoomBin())
	require.NoError(t, err)
	settings := map[string]string{}
	for _, s := range info.Settings {
		settings[s.Key] = s.Value
	}
	assert.NotContains(t, settings, "vcs.revision", "Go's stamp would name the enclosing checkout")
	assert.Contains(t, settings["-ldflags"], "core/rpc.commitRevision="+gitOut(t, root, "rev-parse", "HEAD"))
}
