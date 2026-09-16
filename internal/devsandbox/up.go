package devsandbox

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/config"
)

// registryFileName mirrors config's unexported workspacesFileName;
// TestUp_RegistersToyWorkspaceOnce reads it back via config to catch drift.
const registryFileName = "workspaces.json"

var personaNames = []string{"fakeagent", "claude", "aider"}

// UpOptions configures Up.
type UpOptions struct {
	// SourceWorktree is the loom checkout the sandbox builds from.
	SourceWorktree string
	// RealClaude adds a profile running the real claude CLI. Sticky.
	RealClaude bool
	// DefaultProfile selects config.json's default profile ("" keeps the
	// existing file, or "fake" for a new one). Non-empty rewrites config.json.
	DefaultProfile string
	// Warn, when non-nil, receives non-fatal warnings.
	Warn io.Writer
}

// Up creates the sandbox or tops up a partial one. Each step checks for its
// own artifact, so re-running is safe; config.json is only rewritten when
// it is missing or opts asks for a profile change.
func (s *Sandbox) Up(opts UpOptions) error {
	if strings.ContainsAny(s.Dir, " \t\n") {
		return fmt.Errorf("sandbox path %q contains whitespace; loom splits program strings on spaces (set XDG_STATE_HOME elsewhere)", s.Dir)
	}
	meta, err := s.LoadMeta()
	switch {
	case errors.Is(err, os.ErrNotExist):
		meta = &Meta{Name: s.Name, Socket: s.Socket(), CreatedAt: time.Now()}
	case err != nil:
		return err
	case meta.SourceWorktree != opts.SourceWorktree && opts.Warn != nil:
		fmt.Fprintf(opts.Warn, "loomdev: sandbox %q was last used from %s; now %s\n", s.Name, meta.SourceWorktree, opts.SourceWorktree)
	}
	meta.SourceWorktree = opts.SourceWorktree
	meta.RealClaude = meta.RealClaude || opts.RealClaude

	cfg, err := sandboxConfig(s.PersonaDir(), meta.RealClaude, opts.DefaultProfile)
	if err != nil {
		return err
	}
	for _, dir := range []string{s.GlobalDir(), s.HomeDir()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := s.ensurePersonas(); err != nil {
		return err
	}
	if err := initToyRepo(s.RepoDir(), s.OriginDir()); err != nil {
		return fmt.Errorf("toy repo: %w", err)
	}
	force := opts.RealClaude || opts.DefaultProfile != ""
	for _, dir := range []string{s.GlobalDir(), s.WorkspaceConfigDir()} {
		if err := writeConfig(dir, cfg, force); err != nil {
			return err
		}
		if err := seedState(dir); err != nil {
			return err
		}
	}
	if err := s.registerWorkspace(); err != nil {
		return err
	}
	return s.saveMeta(meta)
}

func sandboxConfig(personaDir string, realClaude bool, defaultProfile string) (map[string]any, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	profiles := []config.Profile{
		{Name: "fake", Program: filepath.Join(personaDir, "fakeagent")},
		{Name: "fake-claude", Program: filepath.Join(personaDir, "claude")},
		{Name: "fake-aider", Program: filepath.Join(personaDir, "aider")},
		{Name: "shell", Program: shell},
	}
	if realClaude {
		profiles = append(profiles, config.Profile{Name: "claude", Program: "claude"})
	}
	if defaultProfile == "" {
		defaultProfile = "fake"
	}
	known := false
	for _, p := range profiles {
		known = known || p.Name == defaultProfile
	}
	if !known {
		return nil, fmt.Errorf("unknown default profile %q (have %v)", defaultProfile, profileList(profiles))
	}
	// Keys mirror config.Config's json tags; TestUp_WritesSandboxConfigToBothConfigDirs
	// unmarshals the result into config.Config to catch drift.
	return map[string]any{
		"default_program":       defaultProfile,
		"branch_prefix":         "dev/",
		"profiles":              profiles,
		"claude_remote_control": false,
	}, nil
}

func profileList(ps []config.Profile) []string {
	names := make([]string, len(ps))
	for i, p := range ps {
		names[i] = p.Name
	}
	return names
}

func writeConfig(dir string, cfg map[string]any, force bool) error {
	path := filepath.Join(dir, config.ConfigFileName)
	if _, err := os.Stat(path); err == nil && !force {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return config.AtomicWriteFile(path, append(data, '\n'), 0o644)
}

// seedState pre-marks every help screen as seen so first-run overlays never
// block a headless driver. Existing state is left alone.
func seedState(dir string) error {
	path := filepath.Join(dir, config.StateFileName)
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return config.AtomicWriteFile(path, []byte("{\"help_screens_seen\": 4294967295, \"instances\": []}\n"), 0o644)
}

func (s *Sandbox) ensurePersonas() error {
	if err := os.MkdirAll(s.PersonaDir(), 0o755); err != nil {
		return err
	}
	for _, name := range personaNames {
		link := filepath.Join(s.PersonaDir(), name)
		if _, err := os.Lstat(link); err == nil {
			continue
		}
		if err := os.Symlink(filepath.Join("..", "fakeagent"), link); err != nil {
			return fmt.Errorf("persona %s: %w", name, err)
		}
	}
	return nil
}

func (s *Sandbox) registerWorkspace() error {
	path := filepath.Join(s.GlobalDir(), registryFileName)
	var reg config.WorkspaceRegistry
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(data, &reg); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		if reg.Get(WorkspaceName) != nil {
			return nil
		}
	case !errors.Is(err, os.ErrNotExist):
		return err
	}
	reg.Workspaces = append(reg.Workspaces, config.Workspace{Name: WorkspaceName, Path: s.RepoDir(), AddedAt: time.Now()})
	reg.LastUsed = WorkspaceName
	out, err := json.MarshalIndent(&reg, "", "  ")
	if err != nil {
		return err
	}
	return config.AtomicWriteFile(path, out, 0o644)
}

// Build compiles loom and fakeagent from the module rooted at srcDir into
// the sandbox and records the source revision. The sandbox must be Up.
func (s *Sandbox) Build(srcDir string) error {
	meta, err := s.LoadMeta()
	if err != nil {
		return fmt.Errorf("sandbox %q is not up (run `loomdev up`): %w", s.Name, err)
	}
	for _, t := range []struct{ out, pkg string }{
		{s.LoomBin(), "."},
		{s.FakeAgentBin(), "./tools/fakeagent"},
	} {
		cmd := exec.Command("go", "build", "-o", t.out, t.pkg)
		cmd.Dir = srcDir
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("go build %s: %w\n%s", t.pkg, err, out)
		}
	}
	meta.BuildSHA = sourceRevision(srcDir)
	return s.saveMeta(meta)
}

// sourceRevision describes srcDir as "<sha>" or "<sha>-dirty", or "unknown"
// when it is not a git checkout (e.g. a Nix build source).
func sourceRevision(srcDir string) string {
	head, err := exec.Command("git", "-C", srcDir, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "unknown"
	}
	rev := strings.TrimSpace(string(head))
	status, err := exec.Command("git", "-C", srcDir, "status", "--porcelain").Output()
	if err == nil && len(bytes.TrimSpace(status)) > 0 {
		rev += "-dirty"
	}
	return rev
}
