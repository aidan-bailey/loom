package app

import (
	"claude-squad/config"
	"claude-squad/keys"
	"claude-squad/log"
	"claude-squad/script"
	"claude-squad/session"
	"errors"
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
type scriptDoneMsg struct {
	err              error
	pendingInstances []*session.Instance
	notices          []string
	pendingIntents   []pendingIntent
}

// scriptResumeMsg feeds a value back into a suspended handler
// coroutine keyed by id. Carries no Lua-specific state so the app
// layer stays independent of gopher-lua; the engine resumes with nil
// internally. Task 11 will emit these from handleScriptIntent after
// the matching runXYZ completes.
type scriptResumeMsg struct {
	id script.IntentID
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

// SelectedInstance: see script.Host.
func (s *scriptHost) SelectedInstance() *session.Instance {
	return s.m.list.GetSelectedInstance()
}

// Instances: see script.Host.
func (s *scriptHost) Instances() []*session.Instance {
	return s.m.list.GetInstances()
}

// Workspaces: see script.Host.
func (s *scriptHost) Workspaces() *config.WorkspaceRegistry {
	return s.m.registry
}

// ConfigDir: see script.Host.
func (s *scriptHost) ConfigDir() string {
	return s.m.configDir()
}

// RepoPath: see script.Host.
func (s *scriptHost) RepoPath() string {
	return s.m.repoPath()
}

// DefaultProgram: see script.Host.
func (s *scriptHost) DefaultProgram() string {
	return s.m.program
}

// BranchPrefix: see script.Host.
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

// CursorUp / CursorDown / ToggleDiff / WorkspacePrev / WorkspaceNext
// mirror the legacy runXYZ bodies in actions_nav.go and
// actions_workspace.go. They mutate list/splitPane/slot state directly
// because the engine holds its mutex during dispatch and we're still
// on the dispatch goroutine when these fire — Update hasn't had a
// chance to race with us. Anything that would produce a tea.Cmd is
// handled as a deferred Intent instead (Task 7+).

func (s *scriptHost) CursorUp() {
	s.m.list.Up()
}

func (s *scriptHost) CursorDown() {
	s.m.list.Down()
}

func (s *scriptHost) ToggleDiff() {
	s.m.splitPane.ToggleDiff()
}

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

// pendingIntent ties a caller-provided intent to the id the script
// awaits on. Task 10 drains this into scriptDoneMsg.
type pendingIntent struct {
	id     script.IntentID
	intent script.Intent
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
	if h.scripts != nil {
		return
	}
	reserved := make(map[string]bool, len(keys.GlobalKeyStringsMap)+1)
	for k := range keys.GlobalKeyStringsMap {
		reserved[k] = true
	}
	// ctrl+q is advertised in the help panel as the attach-mode detach
	// key but lives outside GlobalKeyStringsMap (it's handled in the
	// attach overlay). Reserve it so scripts don't silently steal a
	// key users expect to detach with.
	reserved["ctrl+q"] = true
	h.scripts = script.NewEngine(reserved)

	// Defaults ship embedded in the binary, so they always load —
	// even when the user has no scripts directory. User scripts run
	// afterwards and can override any default via cs.unbind + cs.bind
	// (or just cs.bind, which overwrites).
	h.scripts.LoadDefaults()

	dir := scriptsDir()
	if dir == "" {
		return
	}
	h.scripts.Load(dir)
}

// scriptsDir resolves the global scripts directory. Per plan
// docs/specs/scripting.md, scripts are always global — we read
// directly from config.GetConfigDir rather than any per-workspace
// config path.
func scriptsDir() string {
	dir, err := config.GetConfigDir()
	if err != nil {
		log.WarningLog.Printf("scripts: cannot resolve config dir: %v", err)
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

	return func() tea.Msg {
		_, err := m.scripts.Dispatch(key, host)
		pending, notices, intents := host.drain()
		return scriptDoneMsg{
			err:              err,
			pendingInstances: pending,
			notices:          notices,
			pendingIntents:   intents,
		}
	}, true
}

// handleScriptIntent routes a yielded Intent to its legacy runXYZ
// handler and batches a scriptResumeMsg so the awaiting Lua coroutine
// unblocks. The resume fires as soon as the intent dispatches rather
// than after any confirmation overlay closes — scripts that need to
// observe user confirmation must arrange their own signal path. Quit
// is exempt because tea.Quit ends the program before resume could
// matter.
func (m *home) handleScriptIntent(p pendingIntent) tea.Cmd {
	var cmd tea.Cmd
	switch i := p.intent.(type) {
	case script.QuitIntent:
		return tea.Quit
	case script.PushSelectedIntent:
		if i.Confirm {
			_, cmd = runSubmitSelected(m)
		} else {
			_, cmd = runSubmitSelectedNoConfirm(m)
		}
	case script.KillSelectedIntent:
		if i.Confirm {
			_, cmd = runKillSelected(m)
		} else {
			_, cmd = runKillSelectedNoConfirm(m)
		}
	case script.CheckoutIntent:
		_, cmd = runCheckoutSelectedOpts(m, i.Confirm, i.Help)
	case script.ResumeIntent:
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
		if i.Pane == script.AttachPaneTerminal {
			_, cmd = runInlineAttachTerminal(m)
		} else {
			_, cmd = runInlineAttachAgent(m)
		}
	case script.FullscreenAttachIntent:
		if i.Pane == script.AttachPaneTerminal {
			_, cmd = runFullScreenAttachTerminal(m)
		} else {
			_, cmd = runFullScreenAttachAgent(m)
		}
	case script.QuickInputIntent:
		if i.Pane == script.AttachPaneTerminal {
			_, cmd = runQuickInputTerminal(m)
		} else {
			_, cmd = runQuickInputAgent(m)
		}
	}
	resumeCmd := func() tea.Msg { return scriptResumeMsg{id: p.id} }
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
	return func() tea.Msg {
		err := m.scripts.ResumeWithHost(msg.id, host)
		pending, notices, intents := host.drain()
		return scriptDoneMsg{
			err:              err,
			pendingInstances: pending,
			notices:          notices,
			pendingIntents:   intents,
		}
	}
}

// handleScriptDone processes a scriptDoneMsg: finalizes any pending
// instances into the list, routes a failure through handleError, and
// surfaces script notices via errBox so users see them inline. The
// ordering keeps instance adoption prior to error display so that,
// e.g., a script that creates an instance and then errors still
// leaves the new session visible.
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
		cmds = append(cmds, m.handleError(msg.err))
	}
	if len(msg.pendingInstances) > 0 {
		cmds = append(cmds, m.instanceChanged())
	}
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
