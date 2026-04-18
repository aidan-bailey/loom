package script

import (
	"claude-squad/config"
	"claude-squad/session"
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
