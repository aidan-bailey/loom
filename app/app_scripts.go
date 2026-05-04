package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/script"
	"github.com/aidan-bailey/loom/session"
	"path/filepath"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
)

// scriptDoneMsg is dispatched when a script action finishes (success
// or failure). pendingInstances carries any instances the script
// created via ctx:new_instance{} so Update can finalize them into
// h.list on the main goroutine. pendingIntents carries the Intents a
// handler enqueued (via cs.await(cs.actions.foo())) before yielding
// — handleScriptDone dispatches each one and the matching runXYZ
// schedules a scriptResumeMsg when done.
//
// trace is the correlation ID minted at key-dispatch time; propagating
// it through the message lets every downstream log record (intent
// dispatch, script.error, coroutine resume) share one grep-able
// identifier.
type scriptDoneMsg struct {
	err              error
	pendingInstances []*session.Instance
	notices          []string
	pendingIntents   []pendingIntent
	trace            string
	key              string
}

// scriptResumeMsg feeds a value back into a suspended handler
// coroutine keyed by id. Carries no Lua-specific state so the app
// layer stays independent of gopher-lua; the engine resumes with nil
// internally. trace is the originating dispatch's trace ID so the
// resumed work continues to correlate with the triggering key press.
type scriptResumeMsg struct {
	id    script.IntentID
	trace string
}

// scriptHost adapts *home to the script.Host interface. A fresh
// instance is allocated per dispatch so pending instances, notices,
// and references to *home don't leak across script invocations.
type scriptHost struct {
	m *home

	mu      sync.Mutex
	pending []*session.Instance
	notices []string
	intents []pendingIntent
}

// SelectedInstance implements script.Host.
func (s *scriptHost) SelectedInstance() *session.Instance {
	return s.m.list.GetSelectedInstance()
}

// Instances implements script.Host.
func (s *scriptHost) Instances() []*session.Instance {
	return s.m.list.GetInstances()
}

// Workspaces implements script.Host.
func (s *scriptHost) Workspaces() *config.WorkspaceRegistry {
	return s.m.registry
}

// ConfigDir implements script.Host.
func (s *scriptHost) ConfigDir() string {
	return s.m.configDir()
}

// RepoPath implements script.Host.
func (s *scriptHost) RepoPath() string {
	return s.m.repoPath()
}

// DefaultProgram implements script.Host.
func (s *scriptHost) DefaultProgram() string {
	return s.m.program
}

// BranchPrefix implements script.Host.
func (s *scriptHost) BranchPrefix() string {
	if s.m.appConfig != nil {
		return s.m.appConfig.BranchPrefix
	}
	return ""
}

// QueueInstance stages an instance for finalization on the main
// goroutine. We can't call h.list.AddInstance here because the
// returned finalizer must run on the main loop — the list isn't
// goroutine-safe.
func (s *scriptHost) QueueInstance(inst *session.Instance) {
	if inst == nil {
		return
	}
	s.mu.Lock()
	s.pending = append(s.pending, inst)
	s.mu.Unlock()
}

// Notify queues a message for the error/info bar. Routed through
// scriptDoneMsg rather than setting errBox directly so the UI only
// changes on the main goroutine.
func (s *scriptHost) Notify(msg string) {
	s.mu.Lock()
	s.notices = append(s.notices, msg)
	s.mu.Unlock()
}

// Enqueue stages an intent for main-loop processing. The actual
// handoff (and resume plumbing) is wired in Task 10 — for now this
// just appends so scriptHost still satisfies script.Host.
func (s *scriptHost) Enqueue(intent script.Intent) script.IntentID {
	id := script.NewIntentID()
	s.mu.Lock()
	s.intents = append(s.intents, pendingIntent{id: id, intent: intent})
	s.mu.Unlock()
	return id
}

// CursorUp, CursorDown, ToggleDiff, WorkspacePrev, and WorkspaceNext
// mirror the legacy runXYZ bodies in actions_nav.go and
// actions_workspace.go. They mutate list/splitPane/slot state directly
// because the engine holds its mutex during dispatch and we are still
// on the dispatch goroutine when these fire — Update has not had a
// chance to race with us. Anything that would produce a tea.Cmd is
// handled as a deferred Intent instead.

// CursorUp implements script.Host.
func (s *scriptHost) CursorUp() {
	s.m.list.Up()
}

// CursorDown implements script.Host.
func (s *scriptHost) CursorDown() {
	s.m.list.Down()
}

// ToggleDiff implements script.Host.
func (s *scriptHost) ToggleDiff() {
	s.m.splitPane.ToggleDiff()
}

// WorkspacePrev implements script.Host.
func (s *scriptHost) WorkspacePrev() {
	if len(s.m.slots) <= 1 {
		return
	}
	s.m.saveCurrentSlot()
	newIdx := (s.m.focusedSlot - 1 + len(s.m.slots)) % len(s.m.slots)
	s.m.loadSlot(newIdx)
	s.m.updateTabBarStatuses()
	s.m.persistFocusedWorkspace()
}

// WorkspaceNext implements script.Host.
func (s *scriptHost) WorkspaceNext() {
	if len(s.m.slots) <= 1 {
		return
	}
	s.m.saveCurrentSlot()
	newIdx := (s.m.focusedSlot + 1) % len(s.m.slots)
	s.m.loadSlot(newIdx)
	s.m.updateTabBarStatuses()
	s.m.persistFocusedWorkspace()
}

// ScrollLineUp through ScrollBottom and ScrollTerminalLineUp through
// ScrollTerminalPageDown are thin pass-throughs to SplitPane. The
// diff-visible > focused-pane routing is handled inside SplitPane
// itself; the agent-vs-terminal split exists so scripts can address
// each pane explicitly rather than relying on focus state.

// ScrollLineUp implements script.Host.
func (s *scriptHost) ScrollLineUp() { s.m.splitPane.ScrollUp() }

// ScrollLineDown implements script.Host.
func (s *scriptHost) ScrollLineDown() { s.m.splitPane.ScrollDown() }

// ScrollPageUp implements script.Host.
func (s *scriptHost) ScrollPageUp() { s.m.splitPane.PageUp() }

// ScrollPageDown implements script.Host.
func (s *scriptHost) ScrollPageDown() { s.m.splitPane.PageDown() }

// ScrollTop implements script.Host.
func (s *scriptHost) ScrollTop() { s.m.splitPane.GotoTop() }

// ScrollBottom implements script.Host.
func (s *scriptHost) ScrollBottom() { s.m.splitPane.GotoBottom() }

// ScrollTerminalLineUp implements script.Host.
func (s *scriptHost) ScrollTerminalLineUp() { s.m.splitPane.ScrollTerminalUp() }

// ScrollTerminalLineDown implements script.Host.
func (s *scriptHost) ScrollTerminalLineDown() { s.m.splitPane.ScrollTerminalDown() }

// ScrollTerminalPageUp implements script.Host.
func (s *scriptHost) ScrollTerminalPageUp() { s.m.splitPane.PageTerminalUp() }

// ScrollTerminalPageDown implements script.Host.
func (s *scriptHost) ScrollTerminalPageDown() { s.m.splitPane.PageTerminalDown() }

// ResetAgentScroll implements script.Host. ResetAgentToNormalMode is
// nil/Paused-instance safe (preview.go:325) and idempotent — no-op when
// the pane is not scrolled. Errors are surfaced through the same
// info-log path as the Esc handler in state_default.go.
func (s *scriptHost) ResetAgentScroll() {
	selected := s.m.list.GetSelectedInstance()
	if err := s.m.splitPane.ResetAgentToNormalMode(selected); err != nil {
		log.For("ui").Info("scripthost.reset_agent_scroll_failed", "err", err)
	}
}

// ResetTerminalScroll implements script.Host.
func (s *scriptHost) ResetTerminalScroll() { s.m.splitPane.ResetTerminalToNormalMode() }

// ListPageUp implements script.Host.
func (s *scriptHost) ListPageUp() { s.m.list.PageUp() }

// ListPageDown implements script.Host.
func (s *scriptHost) ListPageDown() { s.m.list.PageDown() }

// ListTop implements script.Host.
func (s *scriptHost) ListTop() { s.m.list.Top() }

// ListBottom implements script.Host.
func (s *scriptHost) ListBottom() { s.m.list.Bottom() }

// pendingIntent ties a caller-provided intent to the id the script
// awaits on. Task 10 drains this into scriptDoneMsg. trace records the
// dispatch that produced this intent so handleScriptIntent can log
// the routing decision under the same ID the earlier dispatch used.
type pendingIntent struct {
	id     script.IntentID
	intent script.Intent
	trace  string
}

// drain returns and clears the pending instances, notices, and
// intents. Called from dispatchScript after the Lua call returns and
// from scriptResumeMsg handling after each Resume — any call that
// wakes a coroutine may leave fresh Intents in the host buffer.
func (s *scriptHost) drain() ([]*session.Instance, []string, []pendingIntent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.pending
	n := s.notices
	in := s.intents
	s.pending = nil
	s.notices = nil
	s.intents = nil
	return p, n, in
}

// initScripts wires a fresh engine onto h and loads the global
// scripts directory. Never fails startup: a missing dir or a broken
// script is logged, not propagated.
func initScripts(h *home) {
	initScriptsIn(h, scriptsDir(), h.skipScripts)
}

// initScriptsIn is the testable core of initScripts. Callers (prod
// and tests) supply the user-scripts directory explicitly, plus a
// skipUser flag that lets --no-scripts load only the embedded
// defaults. Defaults always load because they ship in the binary
// and can't be corrupted by user state.
func initScriptsIn(h *home, dir string, skipUser bool) {
	if h.scripts != nil {
		return
	}
	h.scripts = script.NewEngine(buildReservedKeys())

	// Defaults ship embedded in the binary, so they always load —
	// even when --no-scripts is set or the user has no scripts
	// directory. User scripts run afterwards and can override any
	// default via cs.unbind + cs.bind (or just cs.bind, which
	// overwrites).
	h.scripts.LoadDefaults()

	if skipUser || dir == "" {
		return
	}
	h.scripts.Load(dir)
}

// buildReservedKeys is the hard-reserve list the engine uses to
// reject cs.bind / cs.unbind calls at load time. Reservation is a
// property of the binding API, not of dispatch — dispatch only runs
// in stateDefault via dispatchScript, so binding a key like ctrl+a
// (which the textinput widget uses for LineStart in stateQuickInteract)
// is safe and not reserved here.
//
// ctrl+c is the panic-exit backstop the app handles before any state
// routing, and must never reach the script engine. ctrl+q is the
// overlay-detach key handled by the attach overlays themselves; it
// never reaches stateDefault's dispatchScript, but we reserve it to
// keep the user model simple ("the detach key can't be overridden").
func buildReservedKeys() map[string]bool {
	return map[string]bool{
		"ctrl+c": true,
		"ctrl+q": true,
	}
}

// scriptsDir resolves the global scripts directory. Per plan
// docs/specs/scripting.md, scripts are always global — we read
// directly from config.GetConfigDir rather than any per-workspace
// config path.
func scriptsDir() string {
	dir, err := config.GetConfigDir()
	if err != nil {
		log.For("app").Warn("scripts_dir_resolve_failed", "err", err)
		return ""
	}
	return filepath.Join(dir, "scripts")
}

// dispatchScript looks the raw key up in the script engine and, if
// bound, returns a tea.Cmd that runs the action in a goroutine and
// produces a scriptDoneMsg. Returns (nil, false) if no script owns
// the key — the caller then falls through to its normal unhandled
// path.
func (m *home) dispatchScript(key string) (tea.Cmd, bool) {
	if m.scripts == nil {
		return nil, false
	}
	host := &scriptHost{m: m}

	// Peek: run a cheap lookup on the same goroutine to decide
	// whether a script owns this key. Dispatch itself holds the
	// engine mutex and actually runs the Lua code under it — we
	// defer that to the tea.Cmd so the main loop stays responsive.
	if !m.scripts.HasAction(key) {
		return nil, false
	}

	ctx, trace := log.WithTrace(context.Background())
	log.For("script").Debug("dispatch", "trace", trace, "key", key)

	return func() tea.Msg {
		_, err := m.scripts.Dispatch(ctx, key, host)
		pending, notices, intents := host.drain()
		// Stamp trace on every intent so handleScriptIntent can
		// log under the same ID. Engine.Dispatch already produced
		// traced handler.begin/end records; this carries the trace
		// across the async boundary into the intent-dispatch phase.
		for i := range intents {
			intents[i].trace = trace
		}
		return scriptDoneMsg{
			err:              err,
			pendingInstances: pending,
			notices:          notices,
			pendingIntents:   intents,
			trace:            trace,
			key:              key,
		}
	}, true
}

// handleScriptIntent routes a yielded Intent to its legacy runXYZ
// handler and batches a scriptResumeMsg so the awaiting Lua coroutine
// unblocks. Preconditions that used to live on ActionRegistry entries
// now run here — a blocked intent still resumes the coroutine (so
// scripts don't hang) but produces no side-effect Cmd. The resume
// fires as soon as the intent dispatches rather than after any
// confirmation overlay closes; scripts that need to observe user
// confirmation must arrange their own signal path. Quit routes
// through handleQuit to preserve the save-on-exit path the legacy
// "q" key used.
func (m *home) handleScriptIntent(p pendingIntent) tea.Cmd {
	log.For("script").Debug("intent", "trace", p.trace, "intent_id", int(p.id), "kind", fmt.Sprintf("%T", p.intent))
	var cmd tea.Cmd
	switch i := p.intent.(type) {
	case script.QuitIntent:
		// handleQuit returns tea.Quit on success. On SaveInstances
		// failure (any slot in multi-slot mode, or the root storage in
		// single-slot mode) it returns a non-terminal error Cmd so the
		// user can fix the underlying issue (disk full, read-only
		// mount) and retry rather than losing state silently. Falling
		// through to the tea.Batch below ensures the awaiting Lua
		// coroutine resumes in both outcomes.
		_, cmd = m.handleQuit()
	case script.PushSelectedIntent:
		if !selectedNotBusyNotWorkspace(m) {
			break
		}
		if i.Confirm {
			_, cmd = runSubmitSelected(m)
		} else {
			_, cmd = runSubmitSelectedNoConfirm(m)
		}
	case script.KillSelectedIntent:
		if !selectedNotBusyNotWorkspace(m) {
			break
		}
		if i.Confirm {
			_, cmd = runKillSelected(m)
		} else {
			_, cmd = runKillSelectedNoConfirm(m)
		}
	case script.CheckoutIntent:
		if !selectedNotBusyNotWorkspace(m) {
			break
		}
		_, cmd = runCheckoutSelectedOpts(m, i.Confirm, i.Help)
	case script.ResumeIntent:
		if !selectedPausedNotWorkspace(m) {
			break
		}
		_, cmd = runResumeSelected(m)
	case script.NewInstanceIntent:
		// Title pre-fill is not yet wired through the overlay; for now
		// the field is accepted but ignored. Follow-up can plumb a
		// pre-filled list entry through runNewInstance.
		if i.Prompt {
			_, cmd = runPromptNewInstance(m)
		} else {
			_, cmd = runNewInstance(m)
		}
	case script.ShowHelpIntent:
		_, cmd = runShowHelp(m)
	case script.WorkspacePickerIntent:
		_, cmd = runOpenWorkspacePicker(m)
	case script.InlineAttachIntent:
		if !selectedReadyForInput(m) {
			break
		}
		if i.Pane == script.AttachPaneTerminal {
			_, cmd = runInlineAttachTerminal(m)
		} else {
			_, cmd = runInlineAttachAgent(m)
		}
	case script.FullscreenAttachIntent:
		if !selectedReadyForInputNotWorkspace(m) {
			break
		}
		if i.Pane == script.AttachPaneTerminal {
			_, cmd = runFullScreenAttachTerminal(m)
		} else {
			_, cmd = runFullScreenAttachAgent(m)
		}
	case script.QuickInputIntent:
		if !selectedReadyForQuickInput(m) {
			break
		}
		if i.Pane == script.AttachPaneTerminal {
			_, cmd = runQuickInputTerminal(m)
		} else {
			_, cmd = runQuickInputAgent(m)
		}
	case script.ToggleFileExplorerIntent:
		_, cmd = runToggleFileExplorer(m)
	}
	resumeCmd := func() tea.Msg { return scriptResumeMsg{id: p.id, trace: p.trace} }
	if cmd == nil {
		return resumeCmd
	}
	return tea.Batch(cmd, resumeCmd)
}

// handleScriptResume wakes the suspended handler coroutine keyed by
// msg.id. A fresh scriptHost is allocated per resume so any intents
// the resumed coroutine enqueues on its way to the next yield are
// drained into a follow-up scriptDoneMsg. The engine resumes with
// nil internally (callers don't pass Lua values across the app
// boundary). Errors flow through handleError on the next tick.
func (m *home) handleScriptResume(msg scriptResumeMsg) tea.Cmd {
	if m.scripts == nil {
		return nil
	}
	host := &scriptHost{m: m}
	// Re-seed ctx with the same trace the originating dispatch used.
	// log.WithTrace reuses an existing ID when the ctx already carries
	// one, but here we're starting from Background, so seed directly
	// via WithValue-equivalent (WithTrace on an empty ctx + overwrite
	// would mint a fresh ID). The engine only reads TraceID(ctx), so
	// an empty-trace resume logs under an empty ID — acceptable and
	// unusual in practice.
	ctx := context.Background()
	if msg.trace != "" {
		ctx = log.WithValueForTrace(ctx, msg.trace)
	}
	return func() tea.Msg {
		err := m.scripts.ResumeWithHost(ctx, msg.id, host)
		pending, notices, intents := host.drain()
		for i := range intents {
			intents[i].trace = msg.trace
		}
		return scriptDoneMsg{
			err:              err,
			pendingInstances: pending,
			notices:          notices,
			pendingIntents:   intents,
			trace:            msg.trace,
		}
	}
}

// handleScriptDone processes a scriptDoneMsg: finalizes any pending
// instances into the list, routes a failure through handleError, and
// surfaces script notices via errBox so users see them inline. The
// ordering keeps instance adoption prior to error display so that,
// e.g., a script that creates an instance and then errors still
// leaves the new session visible. instanceChanged fires unconditionally
// on dispatch so sync primitives (CursorUp/Down/ToggleDiff) that used
// to trigger a refresh in the legacy runXYZ now still do.
func (m *home) handleScriptDone(msg scriptDoneMsg) tea.Cmd {
	for _, inst := range msg.pendingInstances {
		finalizer := m.list.AddInstance(inst)
		finalizer()
	}
	// Notices surface through the error bar so they auto-clear on
	// the same 3s schedule as real errors. ErrBox has no info-style
	// channel yet; adding one is deferred to a follow-up change.
	var cmds []tea.Cmd
	for _, n := range msg.notices {
		cmds = append(cmds, m.handleError(errors.New(n)))
	}
	if msg.err != nil {
		// Route the error to BOTH the TUI error bar and the log
		// file. Historically script errors only reached the error
		// bar (which auto-clears on a 3s schedule), so a failure
		// the user didn't happen to be watching vanished without
		// trace. The KV form lets a post-mortem grep by trace ID
		// to rebuild the full dispatch chain.
		log.For("script").Error("error", "trace", msg.trace, "key", msg.key, "err", msg.err)
		cmds = append(cmds, m.handleError(msg.err))
	}
	cmds = append(cmds, m.instanceChanged())
	// Dispatch any yielded intents. handleScriptIntent mutates state
	// synchronously (opens overlays, flips m.state, etc.) and returns a
	// Cmd that batches the intent's side-effect Cmd with a resume
	// message for the awaiting coroutine.
	for _, p := range msg.pendingIntents {
		if c := m.handleScriptIntent(p); c != nil {
			cmds = append(cmds, c)
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}
