package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// promptLimit is the largest prompt file the watcher reads. It equals core's
// watchFileLimit, which this package can't import (core imports it).
const promptLimit = 64 << 10

// repoFile returns the bytes of rel under the repository root. A test runs in
// its package directory, so the root is one level up; go.mod proves it.
func repoFile(t *testing.T, rel string) []byte {
	t.Helper()
	require.FileExists(t, filepath.Join("..", "go.mod"))
	data, err := os.ReadFile(filepath.Join("..", rel))
	require.NoError(t, err)
	return data
}

func TestRepoWatchConfig_Parses(t *testing.T) {
	cfg, err := ParseConfig(repoFile(t, ConfigFile))
	require.NoError(t, err)
	assert.Equal(t, "loom-feedback", cfg.Label)
	assert.Equal(t, "needs-human", cfg.EscalateLabel)
	assert.Equal(t, "issue {issue}", cfg.Title)
	assert.Equal(t, 2, cfg.Cap)
	assert.Equal(t, 2*time.Hour, cfg.Stuck)
	assert.Equal(t, 3, cfg.NudgeLimit)
	assert.Equal(t, time.Hour, cfg.NudgeGap)
	assert.True(t, cfg.IssueChanged)
	assert.Equal(t, 200, cfg.LogTail)
	assert.Nil(t, cfg.Hook)
	assert.Equal(t, SessionConfig{
		Model:          "opus",
		Effort:         "high",
		PermissionMode: "auto",
		Account:        "",
		Prompt:         ".loom-watch/start.md",
		ResumePrompt:   ".loom-watch/resume.md",
	}, cfg.Session)

	tmpl, err := ParseTitleTemplate(cfg.Title)
	require.NoError(t, err)
	title, err := tmpl.Render(57, nil)
	require.NoError(t, err)
	assert.Equal(t, "issue 57", title)
}
