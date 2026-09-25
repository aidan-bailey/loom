package account

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeClaudeJSON(t *testing.T, dir, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(body), 0o600))
}

func readClaudeJSON(t *testing.T, dir string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ".claude.json"))
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))
	return m
}

// A CLI `claude auth login` leaves exactly this shape: logged in, onboarding
// never marked. The first interactive launch then runs onboarding, whose
// login screen asks the user to log in again.
func TestEnsureOnboarded_MarksALoggedInAccount(t *testing.T) {
	dir := t.TempDir()
	writeClaudeJSON(t, dir, `{"oauthAccount":{"emailAddress":"a@b"},"firstStartTime":"x","projects":{"/p":{"allowedTools":[]}}}`)

	changed, err := EnsureOnboarded(dir)

	require.NoError(t, err)
	assert.True(t, changed)
	m := readClaudeJSON(t, dir)
	assert.Equal(t, true, m["hasCompletedOnboarding"])
	assert.Equal(t, "x", m["firstStartTime"], "every other key is kept")
	assert.Contains(t, m, "projects")
	assert.Contains(t, m, "oauthAccount")
	fi, err := os.Stat(filepath.Join(dir, ".claude.json"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
}

func TestEnsureOnboarded_CredentialsFileCountsAsLoggedIn(t *testing.T) {
	dir := t.TempDir()
	writeClaudeJSON(t, dir, `{"firstStartTime":"x"}`)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(`{}`), 0o600))

	changed, err := EnsureOnboarded(dir)

	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, true, readClaudeJSON(t, dir)["hasCompletedOnboarding"])
}

func TestEnsureOnboarded_LeavesALoggedOutAccountAlone(t *testing.T) {
	dir := t.TempDir()
	writeClaudeJSON(t, dir, `{"firstStartTime":"x"}`)

	changed, err := EnsureOnboarded(dir)

	require.NoError(t, err)
	assert.False(t, changed, "a logged-out account needs Claude's onboarding to log in")
	assert.NotContains(t, readClaudeJSON(t, dir), "hasCompletedOnboarding")
}

func TestEnsureOnboarded_NoOpWhenAlreadyMarked(t *testing.T) {
	dir := t.TempDir()
	body := `{"hasCompletedOnboarding":true,"oauthAccount":{}}`
	writeClaudeJSON(t, dir, body)

	changed, err := EnsureOnboarded(dir)

	require.NoError(t, err)
	assert.False(t, changed)
	data, _ := os.ReadFile(filepath.Join(dir, ".claude.json"))
	assert.Equal(t, body, string(data), "an onboarded file is not rewritten")
}

func TestEnsureOnboarded_MissingOrCorruptFile(t *testing.T) {
	changed, err := EnsureOnboarded(t.TempDir())
	assert.NoError(t, err, "no .claude.json: nothing to mark")
	assert.False(t, changed)

	dir := t.TempDir()
	writeClaudeJSON(t, dir, `not json`)
	_, err = EnsureOnboarded(dir)
	assert.Error(t, err)
	data, _ := os.ReadFile(filepath.Join(dir, ".claude.json"))
	assert.Equal(t, "not json", string(data), "a file loom can't parse is never overwritten")
}
