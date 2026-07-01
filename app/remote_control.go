package app

import (
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
)

// remoteControlProgram returns program with Claude's --remote-control
// flag (named after title) applied when cfg enables it. It is a no-op
// when cfg is nil, when the toggle is disabled, or for non-Claude
// programs (see session.BuildRemoteControlCommand).
//
// Callers apply it to an instance's Program at first launch — once the
// title is known — so the rewritten command is persisted and later
// resume/crash restarts inherit the flag through BuildRecoveryCommand.
func remoteControlProgram(cfg *config.Config, program, title string) string {
	if cfg == nil || !cfg.RemoteControlEnabled() {
		return program
	}
	return session.BuildRemoteControlCommand(program, title)
}
