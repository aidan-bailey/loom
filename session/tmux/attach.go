package tmux

import internalexec "github.com/aidan-bailey/loom/internal/exec"

// NewAttachClient returns an unattached client for the existing session
// named sessionName, which the TUI then Restores. sessionName is already a
// tmux session name, as Session.SessionName returns, not a title. program
// only selects the agent adapter for the client's status scan
// (DetectStatus); a client launches nothing. Since session lifecycle never
// attaches, this is how the TUI gets into a session lifecycle launched.
func NewAttachClient(sessionName, program string) *TmuxSession {
	return newSanitizedTmuxSession(sessionName, program, MakePtyFactory(), internalexec.Default{})
}

// NewAttachClientWithDeps is NewAttachClient with injected dependencies,
// for tests.
func NewAttachClientWithDeps(sessionName, program string, ptyFactory PtyFactory, cmdExec internalexec.Executor) *TmuxSession {
	return newSanitizedTmuxSession(sessionName, program, ptyFactory, cmdExec)
}

// DetectStatus scans the pane's current screen once for the status ladder.
// It reports whether the screen changed since the last scan and whether it
// shows the agent's pending-input prompt, and it answers the agent's trust
// prompt through send-keys. An agent with no adapter patterns gets only
// the change check. err reports a failed capture on the snapshot path; it
// never means "no change".
func (t *TmuxSession) DetectStatus() (updated, hasPrompt bool, err error) {
	if t.adapter.Name() == "default" {
		updated, hasPrompt = t.HasUpdated()
		return updated, hasPrompt, nil
	}
	_, updated, hasPrompt, _, err = t.CaptureAndProcess()
	return updated, hasPrompt, err
}
