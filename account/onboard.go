package account

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/aidan-bailey/loom/config"
)

// EnsureOnboarded marks a logged-in account's first-run onboarding as done
// in its <dir>/.claude.json, and reports whether it changed the file.
//
// `claude auth login` stores the credentials (and oauthAccount) but leaves
// hasCompletedOnboarding unset, and the interactive CLI runs its onboarding
// whenever that flag is missing. Onboarding includes the login-method
// screen, so the first session on an account logged in from the CLI asked
// to log in again (observed on 2.1.281). Loom marks it just before launching
// on the account.
//
// Only a logged-in account is marked (oauthAccount in .claude.json, or a
// .credentials.json): a logged-out one needs the onboarding to log in. A
// missing .claude.json is left alone, and one that doesn't parse as a JSON
// object is an error and is never overwritten. Every other key is kept.
func EnsureOnboarded(dir string) (bool, error) {
	path := filepath.Join(dir, ".claude.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal(data, &cfg); err != nil || cfg == nil {
		return false, fmt.Errorf("parse %s: not a JSON object", path)
	}
	if string(cfg["hasCompletedOnboarding"]) == "true" {
		return false, nil
	}
	if !loggedIn(dir, cfg) {
		return false, nil
	}
	cfg["hasCompletedOnboarding"] = json.RawMessage("true")
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return false, fmt.Errorf("marshal %s: %w", path, err)
	}
	if err := config.AtomicWriteFile(path, out, 0o600); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, nil
}

// loggedIn reports whether the account has completed a login: Claude
// records the account in oauthAccount, and on Linux keeps the tokens in
// .credentials.json (macOS keeps them in the Keychain, so oauthAccount is
// the portable signal).
func loggedIn(dir string, cfg map[string]json.RawMessage) bool {
	if raw, ok := cfg["oauthAccount"]; ok && string(raw) != "null" {
		return true
	}
	_, err := os.Stat(filepath.Join(dir, ".credentials.json"))
	return err == nil
}
