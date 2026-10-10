package core

import (
	"time"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
)

// Trust prompts are the daemon's (stage 3C+4). An agent may stop at one
// (Claude's "Do you trust the files in this folder?", aider's and
// gemini's) right after it launches, and a TUI's status scan used to
// answer it, so an agent launched with no TUI showing it (a crash restart
// at the daemon's boot, a workspace terminal relaunched by the health
// tick) sat at the prompt until someone looked. Now the model watches
// every real launch (watchLaunch) and answers the prompt once, and no TUI
// answers it.

// launchWatchEvery is how often a launch watch captures the pane, and
// launchWatchFor how long it watches: a trust prompt shows within
// seconds of a launch or not at all. Vars so tests can shorten them; a
// watch reads them when it is built.
var (
	launchWatchEvery = 250 * time.Millisecond
	launchWatchFor   = 30 * time.Second
)

// launchWatch is one session's watch (Model.watching): the program of its
// latest launch, and whether the session launched again while the watch
// ran.
type launchWatch struct {
	program string
	again   bool
}

// launchWatched is a launch watch's end (launchWatchJob): it answered the
// trust prompt, saw the session die, or ran out of time.
type launchWatched struct {
	session  string
	answered bool
}

// watchLaunch watches inst's session, which has just launched, for its
// agent's trust prompt, and answers it once (launchWatchJob). It is
// background work (spawnBackground): a request does not wait for it, and
// neither does Quiesce. One watch runs per tmux session name: a launch
// while one runs (a relaunch under the same name) is folded into it, and
// when it ends the session is watched afresh for the relaunch
// (deliverLaunchWatched). An agent with no trust prompt to answer
// (tmux.Session.AnswersTrustPrompt: a shell, an unknown program) is not
// watched. Call it on the loop's goroutine, after a launch that
// succeeded.
func (m *Model) watchLaunch(inst *session.Instance) {
	pane := inst.Pane()
	name, program := pane.TmuxSessionName(), pane.SessionProgram()
	if name == "" || !m.launchSession(name, program).AnswersTrustPrompt() {
		return
	}
	if w := m.watching[name]; w != nil {
		w.program, w.again = program, true
		return
	}
	m.startWatch(name, program)
}

// launchSession is the lifecycle session a launch watch reaches name by:
// a tmux.Session with no client, on the model's executor.
func (m *Model) launchSession(name, program string) *tmux.Session {
	return tmux.NewSessionNamedWithDeps(name, program, tmux.MakePtyFactory(), m.executor())
}

// startWatch records a watch of name and queues its job.
func (m *Model) startWatch(name, program string) {
	if m.watching == nil {
		m.watching = make(map[string]*launchWatch)
	}
	m.watching[name] = &launchWatch{program: program}
	m.spawnBackground(launchWatchJob(m.launchSession(name, program), launchWatchEvery, launchWatchFor))
}

// launchWatchJob captures ts's pane every every, for up to within, and
// answers the trust prompt the first time the screen shows one
// (DismissTrustPrompt). It returns early when the session is gone: a
// capture that fails on a session tmux says is dead. It reads no model
// state: ts was built on the loop.
func launchWatchJob(ts *tmux.Session, every, within time.Duration) Job {
	name := ts.SessionName()
	return func() any {
		deadline := time.Now().Add(within)
		for {
			screen, err := ts.CapturePaneContent()
			if err == nil && ts.DismissTrustPrompt(screen) {
				log.For("core").Info("launch_watch.trust_prompt_answered", "session", name)
				return launchWatched{session: name, answered: true}
			}
			if err != nil && ts.SessionLiveness() == tmux.LivenessDead {
				return launchWatched{session: name}
			}
			if !time.Now().Add(every).Before(deadline) {
				return launchWatched{session: name}
			}
			time.Sleep(every)
		}
	}
}

// deliverLaunchWatched ends a watch. A session that launched again while
// it ran is watched afresh, for the latest launch's program: the watch
// that ended may have run out of time, or seen the old session die, just
// before the new one showed its prompt.
func (m *Model) deliverLaunchWatched(r launchWatched) {
	w := m.watching[r.session]
	delete(m.watching, r.session)
	if w != nil && w.again {
		m.startWatch(r.session, w.program)
	}
}
