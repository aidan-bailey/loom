// Package claudemod is the loom mod for Claude Code: a function-hook plugin
// (mod/) that carries the daemon's messages into Claude's prompt queue. It is
// embedded in the loom binary, and session writes it into each Claude
// launch's hooks folder (WriteTo), which the launch passes with --plugin-dir.
// The module reaches the daemon over HTTP on the socket EnvSocket names, as
// the launch EnvLaunch names (internal/modserver serves it).
package claudemod

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// The embedded mod. all: keeps .claude-plugin, which a plain pattern skips
// as a dot-directory.
//
//go:embed all:mod
var files embed.FS

// Version is the mod protocol version: the module's hello says it, and its
// API is /v<Version>. The manifest's major version matches it.
const Version = 1

// The environment a launch gives the mod.
const (
	// EnvSocket names the daemon's mod socket.
	EnvSocket = "LOOM_MOD_SOCKET"
	// EnvLaunch names the launch: its hooks folder's launch ID.
	EnvLaunch = "LOOM_LAUNCH"
)

// WriteTo writes the mod into dir, creating it. Each launch's hooks folder is
// emptied before it (hooks.Prepare), so nothing older is left beside it.
// Claude itself writes tsconfig.json and .claude-plugin/types/ into the
// folder when it loads the mod.
func WriteTo(dir string) error {
	err := fs.WalkDir(files, "mod", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(p, "mod")))
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		b, err := files.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o600)
	})
	if err != nil {
		return fmt.Errorf("claudemod: write the mod: %w", err)
	}
	return nil
}
