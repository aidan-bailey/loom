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
// h.list on the main goroutine.
type scriptDoneMsg struct {
	err              error
	pendingInstances []*session.Instance
	notices          []string
}

// scriptHost adapts *home to the script.Host interface. A fresh
// instance is allocated per dispatch so pending instances, notices,
// and references to *home don't leak across script invocations.
type scriptHost struct {
	m *home

	mu      sync.Mutex
	pending []*session.Instance
	notices []string
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

// drain returns and clears the pending instances and notices.
// Called from dispatchScript after the Lua call returns.
func (s *scriptHost) drain() ([]*session.Instance, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.pending
	n := s.notices
	s.pending = nil
	s.notices = nil
	return p, n
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
		pending, notices := host.drain()
		return scriptDoneMsg{
			err:              err,
			pendingInstances: pending,
			notices:          notices,
		}
	}, true
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
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}
