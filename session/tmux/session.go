package tmux

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session/agent"
)

// Session is one tmux session on the server as session lifecycle sees it:
// launched, probed, typed into and killed through tmux commands. It never
// attaches a client (no PTY, output pump or emulator), so a process that
// holds only Sessions renders no pane and takes no part in the window-size
// negotiation between attached clients. TmuxSession embeds one and adds
// the attach client the TUI renders from.
type Session struct {
	// sanitizedName is the tmux session name: ToLoomTmuxName of a title.
	sanitizedName string
	// program is the command line the session runs.
	program string
	// adapter is the agent adapter resolved from program: the
	// trust-prompt and pending-prompt patterns that an attach client's
	// CaptureAndProcess (via DismissTrustPrompt) and HasUpdated use.
	adapter agent.Adapter
	// env holds "KEY=VALUE" entries applied to the session via
	// `new-session -e`, e.g. ANTHROPIC_BASE_URL under Headroom Proxy.
	// Scoped to this session; never touches program.
	env []string
	// ptyFactory runs `new-session`, which tmux is handed a terminal for.
	ptyFactory PtyFactory
	// cmdExec runs every other tmux command.
	cmdExec internalexec.Executor
}

// NewSession returns the Session for the instance titled name, wired to
// the production PTY factory and executor. Nothing runs until Start. env,
// if given, is applied to the session via `new-session -e`.
func NewSession(name, program string, env ...string) *Session {
	return newSanitizedSession(ToLoomTmuxName(name), program, MakePtyFactory(), internalexec.Default{}, env...)
}

// NewSessionWithDeps is NewSession with injected dependencies, so tests
// never spawn tmux or allocate a PTY.
func NewSessionWithDeps(name, program string, ptyFactory PtyFactory, cmdExec internalexec.Executor, env ...string) *Session {
	return newSanitizedSession(ToLoomTmuxName(name), program, ptyFactory, cmdExec, env...)
}

// newSanitizedSession is NewSessionWithDeps for a name that has already
// been through ToLoomTmuxName.
func newSanitizedSession(sanitizedName, program string, ptyFactory PtyFactory, cmdExec internalexec.Executor, env ...string) *Session {
	return &Session{
		sanitizedName: sanitizedName,
		program:       program,
		adapter:       adapterRegistry.Lookup(program),
		env:           env,
		ptyFactory:    ptyFactory,
		cmdExec:       cmdExec,
	}
}

// WithProgram is WithProgramEnv with s's own env carried over.
func (s *Session) WithProgram(program string) *Session {
	return s.WithProgramEnv(program, slices.Clone(s.env))
}

// WithProgramEnv returns a new, unstarted Session for the same tmux
// session that runs program with env in place of s's program and env. It
// keeps s's PTY factory and executor and resolves its adapter from
// program, as NewSession does. s itself is unchanged: the caller Closes s
// and Starts the result. Instance.Restart uses it to relaunch a dead
// session with a freshly composed command and a freshly resolved env,
// since the account's CLAUDE_CONFIG_DIR in particular may have changed
// since s was built.
func (s *Session) WithProgramEnv(program string, env []string) *Session {
	return newSanitizedSession(s.sanitizedName, program, s.ptyFactory, s.cmdExec, env...)
}

// SessionName returns the tmux session name. It is the identity pane
// events (Notifier callbacks) carry and the key the TUI's pane clients use.
func (s *Session) SessionName() string {
	return s.sanitizedName
}

// Start creates the tmux session, running program in workDir, and sets
// the options every client attached to it relies on: scroll-back history,
// mouse, no status line, and C-q to detach a full-screen attach. It
// attaches nothing; TmuxSession.Start does that for a caller that renders
// the session itself. If a session of the same name is already alive, Start
// refuses with ErrSessionExists before launching anything. Any other error
// may come after the program was launched, so the caller must not assume
// it is not running in workDir (see SessionLiveness).
func (s *Session) Start(workDir string) (err error) {
	t0 := time.Now()
	log.For("tmux").Debug("start.begin", "session", s.sanitizedName, "program", s.program, "workdir", workDir)
	defer func() {
		args := []any{"session", s.sanitizedName, "duration_ms", time.Since(t0).Milliseconds()}
		if err != nil {
			args = append(args, "err", err.Error())
		}
		log.For("tmux").Debug("start.end", args...)
	}()

	// Check if the session already exists
	if s.DoesSessionExist() {
		return fmt.Errorf("%w: %s", ErrSessionExists, s.sanitizedName)
	}

	// Create a new detached tmux session and start the program in it.
	// tmuxStartTimeout allows the agent process's initial exec before tmux
	// returns control; tmux itself is quick, but the wrapped program may not be.
	startCtx, startCancel := context.WithTimeout(context.Background(), tmuxStartTimeout)
	defer startCancel()
	args := []string{"new-session", "-d", "-s", s.sanitizedName, "-c", workDir}
	for _, e := range s.env {
		args = append(args, "-e", e)
	}
	args = append(args, s.program)
	cmd := Command(startCtx, args...)

	ptmx, err := s.ptyFactory.Start(cmd)
	if err != nil {
		// Cleanup any partially created session if any exists.
		if s.DoesSessionExist() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), tmuxTimeout)
			cleanupCmd := Command(cleanupCtx, "kill-session", "-t", SessionTarget(s.sanitizedName))
			if cleanupErr := s.cmdExec.Run(cleanupCmd); cleanupErr != nil {
				err = fmt.Errorf("%v (cleanup error: %v)", err, cleanupErr)
			}
			cleanupCancel()
		}
		return fmt.Errorf("error starting tmux session: %w", err)
	}

	// The new-session ptmx only exists to launch the command; close it on
	// every exit path.
	defer ptmx.Close()

	// Poll for session existence with exponential backoff
	timeout := time.After(2 * time.Second)
	sleepDuration := 5 * time.Millisecond
	for !s.DoesSessionExist() {
		select {
		case <-timeout:
			if cleanupErr := s.Close(); cleanupErr != nil {
				err = fmt.Errorf("%v (cleanup error: %v)", err, cleanupErr)
			}
			return fmt.Errorf("timed out waiting for tmux session %s: %v", s.sanitizedName, err)
		default:
			time.Sleep(sleepDuration)
			// Exponential backoff up to 50ms max
			if sleepDuration < 50*time.Millisecond {
				sleepDuration *= 2
			}
		}
	}

	// Set history limit to enable scrollback (default is 2000, we'll use 10000 for more history)
	histCtx, histCancel := context.WithTimeout(context.Background(), tmuxTimeout)
	historyCmd := Command(histCtx, "set-option", "-t", PaneTarget(s.sanitizedName), "history-limit", "10000")
	if err := s.cmdExec.Run(historyCmd); err != nil {
		log.For("tmux").Warn("history_limit_failed", "session", s.sanitizedName, "err", err)
	}
	histCancel()

	// Enable mouse scrolling for the session
	mouseCtx, mouseCancel := context.WithTimeout(context.Background(), tmuxTimeout)
	mouseCmd := Command(mouseCtx, "set-option", "-t", PaneTarget(s.sanitizedName), "mouse", "on")
	if err := s.cmdExec.Run(mouseCmd); err != nil {
		log.For("tmux").Warn("mouse_scroll_failed", "session", s.sanitizedName, "err", err)
	}
	mouseCancel()

	// Disable the tmux status bar. The detached attach stream the emulator
	// consumes includes the status line, but the pane preview must not — it
	// would consume a render row and shift content. tmux still owns the
	// session; only its chrome is hidden.
	statusCtx, statusCancel := context.WithTimeout(context.Background(), tmuxTimeout)
	statusCmd := Command(statusCtx, "set-option", "-t", PaneTarget(s.sanitizedName), "status", "off")
	if err := s.cmdExec.Run(statusCmd); err != nil {
		log.For("tmux").Warn("status_off_failed", "session", s.sanitizedName, "err", err)
	}
	statusCancel()

	// Rebind Ctrl-Q to detach-client for full-screen attach. The default tmux
	// prefix is Ctrl-B + d; our users expect Ctrl-Q because inline attach has
	// always used it. This binding is server-wide, but claude-squad has always
	// assumed ownership of Ctrl-Q as its detach key.
	bindCtx, bindCancel := context.WithTimeout(context.Background(), tmuxTimeout)
	bindCmd := Command(bindCtx, "bind-key", "-n", "C-q", "detach-client")
	if err := s.cmdExec.Run(bindCmd); err != nil {
		log.For("tmux").Warn("bind_cq_failed", "err", err)
	}
	bindCancel()

	return nil
}

// Close kills the tmux session by exact name (see SessionTarget). Close
// runs on sessions that may already be dead, so with a prefix match,
// closing "api" after its agent exited would kill a live "api-v2".
func (s *Session) Close() error {
	log.For("tmux").Debug("close", "session", s.sanitizedName)
	killCtx, killCancel := context.WithTimeout(context.Background(), tmuxTimeout)
	defer killCancel()
	cmd := Command(killCtx, "kill-session", "-t", SessionTarget(s.sanitizedName))
	if err := s.cmdExec.Run(cmd); err != nil {
		return fmt.Errorf("error killing tmux session: %w", err)
	}
	return nil
}

// TypeText types text into the session's active pane as literal keys
// (`send-keys -l`). tmux delivers exactly text's bytes to the program,
// the same bytes a write to an attach client's PTY delivers
// (TestSendKeysMatchesPTYWrite_RealTmux), so no client is needed. "--"
// ends tmux's option parsing, which means text that starts with '-' is
// typed rather than parsed. Empty text runs nothing.
func (s *Session) TypeText(text string) error {
	if text == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), tmuxTimeout)
	defer cancel()
	cmd := Command(ctx, "send-keys", "-l", "-t", PaneTarget(s.sanitizedName), "--", text)
	if err := s.cmdExec.Run(cmd); err != nil {
		return fmt.Errorf("type into tmux session %s: %w", s.sanitizedName, err)
	}
	return nil
}

// PressKeys presses keys in order, each given as a tmux key name ("Enter",
// "D", "C-c"). An empty list runs nothing.
func (s *Session) PressKeys(keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), tmuxTimeout)
	defer cancel()
	args := append([]string{"send-keys", "-t", PaneTarget(s.sanitizedName)}, keys...)
	if err := s.cmdExec.Run(Command(ctx, args...)); err != nil {
		return fmt.Errorf("press %v in tmux session %s: %w", keys, s.sanitizedName, err)
	}
	return nil
}

// promptEnterDelay separates a prompt's text from the Enter that submits
// it, so the agent reads that Enter as a submit, not as a newline inside
// the prompt.
const promptEnterDelay = 100 * time.Millisecond

// SendPrompt types prompt into the session and submits it with Enter.
func (s *Session) SendPrompt(prompt string) error {
	if err := s.TypeText(prompt); err != nil {
		return err
	}
	time.Sleep(promptEnterDelay)
	return s.PressKeys("Enter")
}

// DismissTrustPrompt scans content (the pane's screen) for the adapter's
// trust-prompt patterns. On a hit, it answers with the adapter's declared
// keys through send-keys. It reports whether a prompt was found; a failed
// answer is logged rather than returned.
func (s *Session) DismissTrustPrompt(content string) bool {
	normalized := normalizeForPatternMatch(content)
	for _, pattern := range s.adapter.TrustPromptPatterns() {
		if !strings.Contains(normalized, normalizeForPatternMatch(pattern)) {
			continue
		}
		var keys []string
		switch s.adapter.TrustPromptResponse() {
		case agent.TrustPromptTapEnter:
			keys = []string{"Enter"}
		case agent.TrustPromptTapDAndEnter:
			keys = []string{"D", "Enter"}
		default:
			return false
		}
		if err := s.PressKeys(keys...); err != nil {
			log.For("tmux").Error("trust_prompt.dismiss_failed", "agent", s.adapter.Name(), "err", err)
		}
		return true
	}
	return false
}

// ErrSessionExists is Start's refusal to create a session whose name is
// already taken. Nothing was launched: the caller's workdir was never
// handed to a program.
var ErrSessionExists = errors.New("tmux session already exists")

// pendingPrompt reports whether content shows the adapter's
// blocked-waiting-for-user pattern. Agents without one (the fallback
// adapter) never report a pending prompt.
func (s *Session) pendingPrompt(content string) bool {
	pattern := s.adapter.PendingPromptPattern()
	return pattern != "" &&
		strings.Contains(normalizeForPatternMatch(content), normalizeForPatternMatch(pattern))
}

// CloseRelatedSession best-effort kills another tmux session identified by
// its raw (pre-ToLoomTmuxName) name, reusing this session's cmdExec. It
// does not touch t's own PTY/emulator state.
//
// This exists so a resource whose lifecycle is tied to this session (e.g.
// the terminal pane's shell, which shares this instance's title but is
// otherwise untracked by *TmuxSession) can be torn down at the same point
// this session is — without the caller needing its own injected executor.
// The common case is "no such session", which is expected and harmless.
func (s *Session) CloseRelatedSession(rawName string) error {
	name := ToLoomTmuxName(rawName)
	ctx, cancel := context.WithTimeout(context.Background(), tmuxTimeout)
	defer cancel()
	cmd := Command(ctx, "kill-session", "-t", SessionTarget(name)) // exact, as in Close
	return s.cmdExec.Run(cmd)
}

// DoesSessionExist reports whether the backing tmux session is still
// alive on the tmux server. Used as a sanity check before attach and
// for orphan detection during reconcile.
// Liveness is the outcome of a tmux session liveness probe.
type Liveness int

const (
	// LivenessDead means tmux answered and the session is not there.
	LivenessDead Liveness = iota
	// LivenessAlive means tmux answered and the session exists.
	LivenessAlive
	// LivenessUnknown means the probe never got an answer, so the state
	// is simply not known. Callers must not treat this as death.
	LivenessUnknown
)

// livenessProbeTimeout bounds the has-session probe. A var, not the shared
// tmuxTimeout const, so tests can shorten it.
var livenessProbeTimeout = tmuxTimeout

// SetLivenessProbeTimeoutForTest shortens (or lengthens) the has-session
// probe's deadline, so a test can make a probe go unanswered without
// waiting out the real 5s, and returns a func restoring the previous one.
// Test-only: the name and doc comment are guardrails, nothing about the
// function enforces test-only use. Not safe to call while probes run.
func SetLivenessProbeTimeoutForTest(d time.Duration) (restore func()) {
	prev := livenessProbeTimeout
	livenessProbeTimeout = d
	return func() { livenessProbeTimeout = prev }
}

// SessionLiveness probes whether the tmux session exists, distinguishing
// "tmux said no" from "tmux never answered". The distinction matters: the
// probe is a subprocess, and under heavy load it can be killed at its
// deadline while the session is perfectly healthy. Collapsing that into a
// plain false is what let one loaded machine mark every running session
// Paused at once.
func (s *Session) SessionLiveness() Liveness {
	// Exact (see SessionTarget): a prefix match would report a dead
	// session alive while any sibling whose name it prefixes runs.
	ctx, cancel := context.WithTimeout(context.Background(), livenessProbeTimeout)
	defer cancel()
	existsCmd := Command(ctx, "has-session", "-t", SessionTarget(s.sanitizedName))
	if err := s.cmdExec.Run(existsCmd); err != nil {
		// Killed at the deadline: tmux never answered, so we learned
		// nothing. Reporting death here would be an assertion the probe
		// cannot support — and the load that starves the probe starves
		// every session's probe at once, so the mistake arrives for the
		// whole fleet simultaneously.
		if ctx.Err() == context.DeadlineExceeded {
			log.For("tmux").Warn("liveness.probe_timeout",
				"session", s.sanitizedName, "timeout_ms", livenessProbeTimeout.Milliseconds())
			return LivenessUnknown
		}
		return LivenessDead
	}
	return LivenessAlive
}

// DoesSessionExist reports whether the session is known to be alive. An
// inconclusive probe reads as false here, preserving the original
// semantics for callers that only gate reads on it; callers that act
// destructively on a negative should use SessionLiveness instead.
func (s *Session) DoesSessionExist() bool {
	return s.SessionLiveness() == LivenessAlive
}

// Env returns a clone of the tmux session environment this session was
// constructed with — the "KEY=VALUE" entries applied via `new-session -e`
// (see NewTmuxSession). Read-only: env is set once at construction (or by
// WithProgram/WithProgramEnv building a new session) and never mutated
// afterward; the clone means a caller mutating the result cannot corrupt
// it.
func (s *Session) Env() []string {
	return slices.Clone(s.env)
}

// CapturePaneContent captures the content of the tmux pane
func (s *Session) CapturePaneContent() (string, error) {
	// Add -e flag to preserve escape sequences (ANSI color codes).
	// Note: -J (join wrapped lines) is intentionally omitted so that tmux returns physical
	// screen rows (each bounded by the pane width). Using -J would join wrapped segments into
	// one long logical line; when lipgloss later renders those lines at the same width they
	// re-wrap and produce extra visual rows, causing the pane to overflow its height.
	ctx, cancel := context.WithTimeout(context.Background(), tmuxTimeout)
	defer cancel()
	cmd := Command(ctx, "capture-pane", "-p", "-e", "-t", PaneTarget(s.sanitizedName))
	output, err := s.cmdExec.Output(cmd)
	if err != nil {
		return "", fmt.Errorf("error capturing pane content: %v", err)
	}
	return string(output), nil
}

// CaptureHistory returns the full pane buffer — scrollback history plus the
// visible screen — as physical rows with ANSI escapes, via capture-pane -S -.
// Returns ("", false) on error. Only the no-emulator path (snapshot mode /
// Windows) windows this; the emulator path windows SeedHistory plus the
// emulator's own scrollback (ui.ScrollModel).
func (s *Session) CaptureHistory() (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), tmuxTimeout)
	defer cancel()
	cmd := Command(ctx, "capture-pane", "-p", "-e", "-S", "-", "-E", "-", "-t", PaneTarget(s.sanitizedName))
	output, err := s.cmdExec.Output(cmd)
	if err != nil {
		return "", false
	}
	return string(output), true
}

// captureHistoryRowsOnly captures the pane's HISTORY rows (excluding the
// visible screen) with ANSI styles: capture-pane -S - -E -1. Row -1 is the
// last history line in tmux's coordinate space (0 = first visible row).
// Returns (nil, true) when the pane simply has no history yet.
func (s *Session) captureHistoryRowsOnly() ([]string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), tmuxTimeout)
	defer cancel()
	cmd := Command(ctx, "capture-pane", "-p", "-e", "-S", "-", "-E", "-1", "-t", PaneTarget(s.sanitizedName))
	output, err := s.cmdExec.Output(cmd)
	if err != nil {
		return nil, false
	}
	trimmed := strings.TrimRight(string(output), "\n")
	if trimmed == "" {
		return nil, true
	}
	return strings.Split(trimmed, "\n"), true
}

// IsAlternateScreen reports whether the pane's foreground app is on the
// alternate screen (a full-screen TUI like Claude), which keeps NO tmux
// scrollback. Callers use this to decide whether scroll-back can be windowed
// from CaptureHistory or must instead be forwarded into the app itself.
func (s *Session) IsAlternateScreen() bool {
	ctx, cancel := context.WithTimeout(context.Background(), tmuxTimeout)
	defer cancel()
	// A missing session prints empty formats (exit 0), which reads as false.
	cmd := Command(ctx, "display-message", "-p", "-t", PaneTarget(s.sanitizedName), "#{alternate_on}")
	out, err := s.cmdExec.Output(cmd)
	if err != nil {
		return false
	}
	return len(out) > 0 && out[0] == '1'
}

// FullScreenAttachCmd returns a command that attaches to this tmux session in
// the foreground. It's intended to be handed to tea.ExecProcess, which
// releases and restores the terminal around the call so the child tmux
// owns the real tty for the duration of the attach. Detach is driven by
// the C-q key binding installed during Start (see bind-key call).
func (s *Session) FullScreenAttachCmd() *exec.Cmd {
	return Command(context.Background(), "attach-session", "-t", SessionTarget(s.sanitizedName))
}
