// Package devsandbox creates and drives isolated loom dev environments: a
// named directory holding a dev build, a toy workspace repo, seeded config,
// and a private tmux server, so a dev loom can run from inside a loom pane
// without touching the host's sessions or state. See
// docs/superpowers/specs/2026-09-16-dev-sandbox-design.md.
package devsandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session/tmux"
)

// WorkspaceName is the name the toy repo is registered under.
const WorkspaceName = "toy"

const (
	socketPrefix = "loomdev-"
	metaFileName = "sandbox.json"
)

var (
	validName  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)
	invalidRun = regexp.MustCompile(`[^a-z0-9._-]+`)
)

// Sandbox is one named, persistent dev environment.
type Sandbox struct {
	Name string
	Dir  string
}

// Meta is the sandbox's sandbox.json.
type Meta struct {
	Name           string    `json:"name"`
	Socket         string    `json:"socket"`
	SourceWorktree string    `json:"source_worktree"`
	BuildSHA       string    `json:"build_sha,omitempty"`
	RealClaude     bool      `json:"real_claude"`
	CreatedAt      time.Time `json:"created_at"`
}

// Info summarizes one sandbox for List.
type Info struct {
	Name        string
	Dir         string
	ServerAlive bool
	Meta        *Meta // nil when sandbox.json is missing or unreadable
}

// BaseDir returns the directory holding every sandbox:
// ${XDG_STATE_HOME:-~/.local/state}/loom-dev.
func BaseDir() (string, error) {
	if x := os.Getenv("XDG_STATE_HOME"); x != "" && filepath.IsAbs(x) {
		return filepath.Join(x, "loom-dev"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".local", "state", "loom-dev"), nil
}

// Open resolves the sandbox called name without touching disk.
func Open(name string) (*Sandbox, error) {
	if !validName.MatchString(name) {
		return nil, fmt.Errorf("invalid sandbox name %q (want %s)", name, validName)
	}
	base, err := BaseDir()
	if err != nil {
		return nil, err
	}
	return &Sandbox{Name: name, Dir: filepath.Join(base, name)}, nil
}

// DefaultName derives a sandbox name from a git branch: the leaf after the
// last "/", lowercased, with runs of other characters collapsed to "-".
func DefaultName(branch string) string {
	leaf := branch
	if i := strings.LastIndex(branch, "/"); i >= 0 {
		leaf = branch[i+1:]
	}
	leaf = invalidRun.ReplaceAllString(strings.ToLower(leaf), "-")
	leaf = strings.Trim(leaf, "-._")
	if len(leaf) > 63 {
		leaf = strings.TrimRight(leaf[:63], "-._")
	}
	if leaf == "" {
		return "default"
	}
	return leaf
}

// Socket is the sandbox's private tmux socket name (tmux -L).
func (s *Sandbox) Socket() string { return socketPrefix + s.Name }

// BinDir holds the sandbox's built binaries.
func (s *Sandbox) BinDir() string { return filepath.Join(s.Dir, "bin") }

// LoomBin is the dev build of loom.
func (s *Sandbox) LoomBin() string { return filepath.Join(s.BinDir(), "loom") }

// FakeAgentBin is the built fakeagent.
func (s *Sandbox) FakeAgentBin() string { return filepath.Join(s.BinDir(), "fakeagent") }

// PersonaDir holds the fakeagent persona symlinks.
func (s *Sandbox) PersonaDir() string { return filepath.Join(s.BinDir(), "personas") }

// GlobalDir is LOOM_GLOBAL_DIR: workspaces.json plus the global context's config and state.
func (s *Sandbox) GlobalDir() string { return filepath.Join(s.Dir, "global") }

// HomeDir is LOOM_HOME: startup logs.
func (s *Sandbox) HomeDir() string { return filepath.Join(s.Dir, "home") }

// RepoDir is the toy workspace repo.
func (s *Sandbox) RepoDir() string { return filepath.Join(s.Dir, "repo") }

// OriginDir is the toy repo's bare remote.
func (s *Sandbox) OriginDir() string { return filepath.Join(s.Dir, "origin.git") }

// WorkspaceConfigDir is the toy workspace's .loom directory.
func (s *Sandbox) WorkspaceConfigDir() string { return filepath.Join(s.RepoDir(), ".loom") }

// LogFiles lists the loom.log files a sandboxed loom writes.
func (s *Sandbox) LogFiles() []string {
	return []string{
		filepath.Join(s.HomeDir(), "logs", "loom.log"),
		filepath.Join(s.WorkspaceConfigDir(), "logs", "loom.log"),
	}
}

// Env is the environment overlay every sandboxed loom process runs with.
func (s *Sandbox) Env() []string {
	return []string{
		tmux.EnvTmuxSocket + "=" + s.Socket(),
		config.EnvGlobalDir + "=" + s.GlobalDir(),
		config.EnvHome + "=" + s.HomeDir(),
	}
}

// Environ is os.Environ() with Env() appended; os/exec uses the last value
// of a duplicated key, so the overlay wins.
func (s *Sandbox) Environ() []string { return append(os.Environ(), s.Env()...) }

func (s *Sandbox) metaPath() string { return filepath.Join(s.Dir, metaFileName) }

// LoadMeta reads sandbox.json; the error wraps os.ErrNotExist when the
// sandbox has never been brought up.
func (s *Sandbox) LoadMeta() (*Meta, error) {
	data, err := os.ReadFile(s.metaPath())
	if err != nil {
		return nil, err
	}
	var m Meta
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.metaPath(), err)
	}
	return &m, nil
}

func (s *Sandbox) saveMeta(m *Meta) error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return config.AtomicWriteFile(s.metaPath(), append(data, '\n'), 0o644)
}

func (s *Sandbox) serverAlive() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return tmux.CommandOnSocket(ctx, s.Socket(), "list-sessions").Run() == nil
}

// Down kills the sandbox's private tmux server and deletes its directory.
// It refuses any Dir that is not <BaseDir>/<Name>.
func (s *Sandbox) Down() error {
	base, err := BaseDir()
	if err != nil {
		return err
	}
	if !validName.MatchString(s.Name) || s.Dir != filepath.Join(base, s.Name) {
		return fmt.Errorf("refusing to remove %s: not the sandbox directory %s", s.Dir, filepath.Join(base, s.Name))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tmux.CommandOnSocket(ctx, s.Socket(), "kill-server").Run() // no server is fine
	return os.RemoveAll(s.Dir)
}

// List returns every sandbox under BaseDir, sorted by name.
func List() ([]Info, error) {
	base, err := BaseDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(base)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var infos []Info
	for _, e := range entries {
		if !e.IsDir() || !validName.MatchString(e.Name()) {
			continue
		}
		sb := &Sandbox{Name: e.Name(), Dir: filepath.Join(base, e.Name())}
		info := Info{Name: sb.Name, Dir: sb.Dir, ServerAlive: sb.serverAlive()}
		if m, err := sb.LoadMeta(); err == nil {
			info.Meta = m
		}
		infos = append(infos, info)
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Name < infos[j].Name })
	return infos, nil
}

// TailLogs returns the last n lines of each existing sandbox log, each
// under a tail(1)-style "==> path <==" header.
func (s *Sandbox) TailLogs(n int) string {
	var b strings.Builder
	for _, path := range s.LogFiles() {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
		if len(lines) > n {
			lines = lines[len(lines)-n:]
		}
		fmt.Fprintf(&b, "==> %s <==\n%s\n", path, strings.Join(lines, "\n"))
	}
	return b.String()
}
