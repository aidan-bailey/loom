package account

import "os"

// CredentialOverrides are environment variables whose credential outranks
// every config dir's login: the Claude CLI checks these before ever
// reading a config dir's stored credentials, so with one set in loom's own
// environment, every account loom launches — regardless of which config
// dir CLAUDE_CONFIG_DIR points it at — silently runs (and bills) as that
// one credential instead of its own login. Order matters only in that it
// picks which name ActiveCredentialOverride reports when more than one is
// set; the CLI's own precedence among them is not loom's concern here.
var CredentialOverrides = []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN"}

// ActiveCredentialOverride returns the first of CredentialOverrides set to
// a non-blank value in loom's own environment, and whether one was found.
// Callers use this to warn before per-account selection would be a no-op:
// switching CLAUDE_CONFIG_DIR changes nothing while an override is active.
func ActiveCredentialOverride() (name string, ok bool) {
	for _, v := range CredentialOverrides {
		if os.Getenv(v) != "" {
			return v, true
		}
	}
	return "", false
}
