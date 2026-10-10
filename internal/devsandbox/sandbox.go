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
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/internal/daemon"
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
	// driver is the tmux session the driver methods act on: DriverSession
	// when empty (WithDriver).
	driver string
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
	// Daemon is the lock record of the process holding the sandbox's global
	// dir (its daemon, or one still booting: no socket yet); nil when none
	// holds it.
	Daemon *daemon.Record
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

// LogFiles lists the log files a sandboxed loom writes: its TUI's
// loom.log files, and its daemon's serve.log and the crash file beside it
// (the runtime's own report of a fatal error, which serve.log can't hold).
func (s *Sandbox) LogFiles() []string {
	return []string{
		filepath.Join(s.HomeDir(), "logs", "loom.log"),
		filepath.Join(s.WorkspaceConfigDir(), "logs", "loom.log"),
		daemon.LogPath(s.GlobalDir()),
		daemon.CrashLogPath(s.GlobalDir()),
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

// Cmd runs argv in the sandbox's environment from the toy repo, as a
// sandboxed loom would run: Cmd(sb.LoomBin(), "serve", "stop"), say.
func (s *Sandbox) Cmd(argv ...string) *exec.Cmd {
	c := exec.Command(argv[0], argv[1:]...)
	c.Dir = s.RepoDir()
	c.Env = s.Environ()
	return c
}

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

// daemonStopTimeout bounds the wait for the sandbox's daemon to stop: it
// waits for in-flight lifecycle jobs (up to 30s), then saves. A variable
// so tests can shorten it.
var daemonStopTimeout = 60 * time.Second

// Daemon reads the lock record of the sandbox's daemon, the `loom serve` a
// sandboxed loom started (with the sandbox's global dir). held reports
// whether a process holds the lock now: a daemon, or one still booting
// (no socket yet).
func (s *Sandbox) Daemon() (rec daemon.Record, held bool) {
	return daemon.ReadRecord(s.GlobalDir())
}

// StopDaemon stops the sandbox's daemon gracefully (it saves first) and
// waits for it to go; no daemon running is fine. The sessions keep
// running on the sandbox's tmux server, and the next sandboxed loom starts
// a fresh daemon, which reattaches them.
func (s *Sandbox) StopDaemon() error {
	if err := daemon.Stop(s.GlobalDir(), daemonStopTimeout); err != nil && !errors.Is(err, daemon.ErrNotRunning) {
		return fmt.Errorf("stop the sandbox's loom daemon: %w", err)
	}
	return nil
}

// ErrNoDaemon: nothing holds the sandbox's lock, so there is no daemon to
// kill.
var ErrNoDaemon = errors.New("no loom daemon runs in the sandbox")

// KillDaemon kills the sandbox's daemon with SIGKILL, as a crash does: no
// bye, no save, and the socket file stays behind. The sessions keep running
// on the sandbox's tmux server, and a TUI open on the daemon sees a crash,
// not a stop. It kills only a process proved a build in the sandbox's bin
// dir (killSandboxLoom), and returns the record of the daemon it killed;
// ErrNoDaemon when none runs.
func (s *Sandbox) KillDaemon() (daemon.Record, error) {
	rec, held := s.Daemon()
	if !held {
		return rec, ErrNoDaemon
	}
	return rec, s.killSandboxLoom()
}

// Down stops the sandbox's loom daemon (a sandboxed loom started it, with
// the sandbox's global dir), kills its private tmux server and deletes its
// directory, and the socket files both leave outside it. It refuses any Dir
// that is not <BaseDir>/<Name>. A daemon that won't stop, or a loom from
// before the daemon holding the sandbox's lock, stops it with nothing
// removed; ForceDown kills that process instead.
func (s *Sandbox) Down() error { return s.down(false) }

// ForceDown is Down, except that a process holding the sandbox's lock that
// won't stop (a daemon past its stop timeout, a loom from before the
// daemon) is killed with SIGKILL, once it is proved the sandbox's own loom
// (killSandboxLoom).
func (s *Sandbox) ForceDown() error { return s.down(true) }

func (s *Sandbox) down(force bool) error {
	base, err := BaseDir()
	if err != nil {
		return err
	}
	if !validName.MatchString(s.Name) || s.Dir != filepath.Join(base, s.Name) {
		return fmt.Errorf("refusing to remove %s: not the sandbox directory %s", s.Dir, filepath.Join(base, s.Name))
	}
	if err := s.StopDaemon(); err != nil {
		if !force {
			return fmt.Errorf("%w (`loomdev down --force` kills it)", err)
		}
		if kerr := s.killSandboxLoom(); kerr != nil {
			return fmt.Errorf("%w; --force: %w", err, kerr)
		}
	}
	// A daemon that stops removes its socket, but one killed outright
	// leaves it, in the runtime dir; the free lock proves no daemon uses it.
	if rec, held := s.Daemon(); !held {
		removeSocket(rec.Socket)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// tmux leaves its socket file behind on kill-server.
	path, _ := tmux.CommandOnSocket(ctx, s.Socket(), "display-message", "-p", "#{socket_path}").Output()
	_ = tmux.CommandOnSocket(ctx, s.Socket(), "kill-server").Run() // no server is fine
	removeSocket(strings.TrimSpace(string(path)))
	return os.RemoveAll(s.Dir)
}

// killSandboxLoom kills (SIGKILL) the process holding the sandbox's lock
// and waits for the lock to come free. It kills only a process proved the
// sandbox's own loom (runsSandboxLoom): the record's pid alone proves
// nothing, since a stale record can name a pid reused since.
func (s *Sandbox) killSandboxLoom() error {
	rec, held := s.Daemon()
	if !held {
		return nil
	}
	if rec.PID <= 0 {
		return errors.New("the sandbox's lock record names no process: not killed")
	}
	if !s.runsSandboxLoom(rec.PID) {
		return fmt.Errorf("pid %d holds the sandbox's lock but runs no build in %s: not killed", rec.PID, s.BinDir())
	}
	p, err := os.FindProcess(rec.PID)
	if err != nil {
		return err
	}
	if err := p.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("kill pid %d: %w", rec.PID, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if cur, held := s.Daemon(); !held || cur.PID != rec.PID {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("pid %d still holds the sandbox's lock after SIGKILL", rec.PID)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// runsSandboxLoom reports whether process pid runs an executable in the
// sandbox's bin dir (/proc/<pid>/exe, compared through symlinks), as the
// sandbox's builds of loom do; a build replaced since the process started
// reads "<path> (deleted)", still there. False where /proc can't say.
func (s *Sandbox) runsSandboxLoom(pid int) bool {
	exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return false
	}
	bin, err := filepath.EvalSymlinks(s.BinDir())
	if err != nil {
		return false
	}
	return filepath.Dir(strings.TrimSuffix(exe, " (deleted)")) == bin
}

// removeSocket deletes path when it is a socket.
func removeSocket(path string) {
	if info, err := os.Lstat(path); path != "" && err == nil && info.Mode()&os.ModeSocket != 0 {
		_ = os.Remove(path)
	}
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
		if rec, held := sb.Daemon(); held {
			info.Daemon = &rec
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
