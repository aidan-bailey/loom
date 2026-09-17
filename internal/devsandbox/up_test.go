package devsandbox

import (
	"bytes"
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
		assert.Equal(t, []string{"fake", "fake-claude", "fake-aider", "shell"}, profileList(cfg.Profiles), dir)
		assert.Equal(t, filepath.Join(sb.PersonaDir(), "fakeagent"), cfg.Profiles[0].Program)
		assert.Equal(t, filepath.Join(sb.PersonaDir(), "claude"), cfg.Profiles[1].Program)
		assert.Equal(t, filepath.Join(sb.PersonaDir(), "aider"), cfg.Profiles[2].Program)
	}
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
}
