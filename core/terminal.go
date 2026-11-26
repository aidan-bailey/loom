package core

import (
	"errors"
	"fmt"
	"os"

	cmd2 "github.com/aidan-bailey/loom/cmd"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
)

// terminalResult is EnsureTerminal's job result.
type terminalResult struct {
	// title and home are the instance's, taken when the request was
	// admitted: a close job on delivery names the shell by them.
	title string
	home  session.SessionHome
	// name is the shell's tmux name, set when the shell is the instance's.
	name string
	// started: the job made the shell. possible: the launch failed after
	// tmux may have made it (err says how). Neither: the shell existed.
	started  bool
	possible bool
	// err is the refusal (a refusedError) or the failure.
	err error
}

// terminalShell is the program of a worktree shell: the daemon's $SHELL,
// else /bin/sh, as the TUI's terminal pane picks it.
func terminalShell() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/sh"
}

// EnsureTerminal makes sure session id's worktree shell runs and answers
// with its tmux name in the Reply's Terminal: the tmux session
// tmux.TerminalSessionName(title), the one the TUI's terminal pane shares.
// Refused (precondition) unless the session is started and active with a
// worktree. The job starts the shell when no session has its name, through
// a tmux.Session, which attaches no client; a session of that name that
// started in the worktree is the instance's own and is left as it is, and
// one that did not is refused (see ensureTerminalJob).
func (m *Model) EnsureTerminal(id InstanceID, req ReqID) {
	inst, _, ok := m.admit(opTerminal, id, req)
	if !ok {
		return
	}
	title := inst.Title
	ts := tmux.NewSessionNamedWithDeps(shellSessionName(title), terminalShell(), tmux.MakePtyFactory(), m.executor())
	m.spawn(m.track(req, id, ensureTerminalJob(ts, title, inst.GetWorktreePath(), inst.SessionHome(), m.executor())))
}

// shellSessionName is the tmux name of the worktree shell of the instance
// titled title.
func shellSessionName(title string) string {
	return tmux.ToLoomTmuxName(tmux.TerminalSessionName(title))
}

// ensureTerminalJob is EnsureTerminal's job. It reads no model state: the
// session, the worktree and the home were captured when it was built.
//
//   - tmux that does not answer the has-session probe: a refusal, nothing
//     started.
//   - a session of the name that runs: the instance's own only when it
//     started in home (ownShell: fail-closed, so a listing that cannot be
//     read is a refusal too, and a session no longer listed is never
//     named). Nothing is started or closed.
//   - none: start the shell in worktree. tmux.ErrSessionExists means
//     another client made it first: the session now under the name gets
//     ownShell's judgement as one that existed, and nothing was started.
//     Any other error may come after the launch (the probe says), so the
//     result is possible, for the delivery to close if its row went.
func ensureTerminalJob(ts *tmux.Session, title, worktree string, home session.SessionHome, exec cmd2.Executor) Job {
	name := ts.SessionName()
	return func() any {
		res := terminalResult{title: title, home: home}
		switch ts.SessionLiveness() {
		case tmux.LivenessUnknown:
			res.err = refusedError{fmt.Errorf("open a terminal of %s: tmux did not answer whether its shell is running: try again", title)}
		case tmux.LivenessAlive:
			res.name, res.err = ownShell(title, name, home, exec)
		default:
			err := ts.Start(worktree)
			switch {
			case err == nil:
				res.name, res.started = name, true
			case errors.Is(err, tmux.ErrSessionExists):
				res.name, res.err = ownShell(title, name, home, exec)
			default:
				res.possible = ts.SessionLiveness() != tmux.LivenessDead
				res.err = fmt.Errorf("open a terminal of %s: %w", title, err)
				log.For("core").Warn("terminal.start_failed", "title", title, "possibly_started", res.possible, "err", err)
			}
		}
		return res
	}
}

// ownShell judges the session that holds the shell's name (name, the tmux
// name of title's shell) from one listing, and answers its name when it is
// the instance's own: started in home. It answers a refusal when another
// session holds the name (another workspace's same-titled session, or an
// agent whose title begins term_), when tmux cannot say whose it is, and
// when no session of the name is listed any more (the shell ended between
// the probe and the listing): a name that no longer runs is never handed
// out, and the caller asks again.
func ownShell(title, name string, home session.SessionHome, exec cmd2.Executor) (string, error) {
	held, listed, err := session.HeldElsewhereListed(tmux.TerminalSessionName(title), home, exec)
	switch {
	case err != nil:
		return "", refusedError{fmt.Errorf("open a terminal of %s: tmux did not answer whose the session %s is: %w", title, name, err)}
	case held:
		return "", refusedError{fmt.Errorf("open a terminal of %s: tmux session %s, which has its shell's name, was not started in its worktree: it is another session's", title, name)}
	case !listed:
		return "", refusedError{fmt.Errorf("open a terminal of %s: its shell ended while loom looked at it: try again", title)}
	}
	return name, nil
}
