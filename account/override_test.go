package account

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// clearCredentialOverrides blanks every CredentialOverrides var for the
// duration of the test, restored automatically by t.Setenv, so a
// developer's own shell environment (an ANTHROPIC_API_KEY set for other
// tools, say) can't make this test flaky.
func clearCredentialOverrides(t *testing.T) {
	t.Helper()
	for _, v := range CredentialOverrides {
		t.Setenv(v, "")
	}
}

func TestActiveCredentialOverride_NoneSet(t *testing.T) {
	clearCredentialOverrides(t)

	_, ok := ActiveCredentialOverride()

	assert.False(t, ok)
}

func TestActiveCredentialOverride_ReportsWhicheverIsSet(t *testing.T) {
	clearCredentialOverrides(t)
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "tok")

	name, ok := ActiveCredentialOverride()

	assert.True(t, ok)
	assert.Equal(t, "ANTHROPIC_AUTH_TOKEN", name)
}

func TestActiveCredentialOverride_FirstListedWinsWhenSeveralAreSet(t *testing.T) {
	clearCredentialOverrides(t)
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "oauth")
	t.Setenv("ANTHROPIC_API_KEY", "key")

	name, ok := ActiveCredentialOverride()

	assert.True(t, ok)
	assert.Equal(t, "ANTHROPIC_API_KEY", name, "CredentialOverrides' own order decides, not env-map iteration order")
}

func TestActiveCredentialOverride_BlankValueDoesNotCount(t *testing.T) {
	clearCredentialOverrides(t)
	t.Setenv("ANTHROPIC_API_KEY", "")

	_, ok := ActiveCredentialOverride()

	assert.False(t, ok, "an env var present but empty is not a credential")
}

func TestActiveCredentialOverride_WhitespaceOnlyValueDoesNotCount(t *testing.T) {
	clearCredentialOverrides(t)
	t.Setenv("ANTHROPIC_API_KEY", "   \t\n  ")

	_, ok := ActiveCredentialOverride()

	assert.False(t, ok, "matches the trimming the remote-control check already does")
}
