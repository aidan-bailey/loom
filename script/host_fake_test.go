package script

import (
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
)

// fakeHost is a minimal Host implementation for tests that never
// touches real TUI state. Fields are exposed so individual tests can
// set expectations directly.
type fakeHost struct {
	instances       []*session.Instance
	selected        *session.Instance
	registry        *config.WorkspaceRegistry
	configDir       string
	repoPath        string
	defaultProgram  string
	branchPrefix    string
	queuedInstances []*session.Instance
	notices         []string
	enqueued        []Intent
	enqueuedIDs     []IntentID
	terminalKeys    []terminalKeysCall
}

func (f *fakeHost) SelectedInstance() *session.Instance   { return f.selected }
func (f *fakeHost) Instances() []*session.Instance        { return f.instances }
func (f *fakeHost) Workspaces() *config.WorkspaceRegistry { return f.registry }
func (f *fakeHost) ConfigDir() string                     { return f.configDir }
func (f *fakeHost) RepoPath() string                      { return f.repoPath }
func (f *fakeHost) DefaultProgram() string                { return f.defaultProgram }
func (f *fakeHost) BranchPrefix() string                  { return f.branchPrefix }

func (f *fakeHost) QueueInstance(inst *session.Instance) {
	f.queuedInstances = append(f.queuedInstances, inst)
}

func (f *fakeHost) Notify(msg string) {
	f.notices = append(f.notices, msg)
}

// Enqueue stashes the intent and returns a fresh id so tests can assert
// against h.enqueued / h.enqueuedIDs without wiring up a real app.
func (f *fakeHost) Enqueue(intent Intent) IntentID {
	id := newIntentID()
	f.enqueued = append(f.enqueued, intent)
	f.enqueuedIDs = append(f.enqueuedIDs, id)
	return id
}

// Sync primitives — fakeHost's defaults are no-ops. recordingHost in
// api_actions_test.go overrides these to capture call ordering.
func (f *fakeHost) CursorUp()      {}
func (f *fakeHost) CursorDown()    {}
func (f *fakeHost) ToggleDiff()    {}
func (f *fakeHost) WorkspacePrev() {}
func (f *fakeHost) WorkspaceNext() {}

func (f *fakeHost) ScrollLineUp()           {}
func (f *fakeHost) ScrollLineDown()         {}
func (f *fakeHost) ScrollPageUp()           {}
func (f *fakeHost) ScrollPageDown()         {}
func (f *fakeHost) ScrollTop()              {}
func (f *fakeHost) ScrollBottom()           {}
func (f *fakeHost) ScrollTerminalLineUp()   {}
func (f *fakeHost) ScrollTerminalLineDown() {}
func (f *fakeHost) ScrollTerminalPageUp()   {}
func (f *fakeHost) ScrollTerminalPageDown() {}
func (f *fakeHost) ResetAgentScroll()       {}
func (f *fakeHost) ResetTerminalScroll()    {}
func (f *fakeHost) ListPageUp()             {}
func (f *fakeHost) ListPageDown()           {}
func (f *fakeHost) ListTop()                {}
func (f *fakeHost) ListBottom()             {}

// terminalKeysCall records a SendTerminalKeys invocation so tests can
// assert the right instance and text reached the host without standing
// up a real terminal pane.
type terminalKeysCall struct {
	inst *session.Instance
	text string
}

func (f *fakeHost) SendTerminalKeys(inst *session.Instance, text string) error {
	f.terminalKeys = append(f.terminalKeys, terminalKeysCall{inst: inst, text: text})
	return nil
}
