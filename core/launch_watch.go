package core

import (
	"time"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
)

// Trust prompts are the daemon's (stage 3C+4). An agent may stop at one
// (Claude's trust prompt and its "new MCP server" one after it, aider's
// and gemini's) right after it launches, and a TUI's status scan used to
// answer it, so an agent launched with no TUI showing it (a crash restart
// at the daemon's boot, a workspace terminal relaunched by the health
// tick) sat at the prompt until someone looked. Now the model watches
// every real launch (watchLaunch) and answers each prompt it shows once,
// and no TUI answers it.

// launchWatchEvery is how often a launch watch captures the pane, and
// launchWatchFor how long it watches: a trust prompt shows within
// seconds of a launch or not at all. Vars so tests can shorten them; a
// watch reads them when it is built.
var (
	launchWatchEvery = 250 * time.Millisecond
	launchWatchFor   = 30 * time.Second
)

// trustMenuMoves is how many times an answer moves a trust prompt's menu
// (Down) looking for its option, and trustMenuWaits how many captures it
// waits for the agent to draw a move: past either, the prompt is left to
// the user.
const (
	trustMenuMoves = 4
	trustMenuWaits = 8
)

// launchWatch is one session's watch (Model.watching): the program of its
// latest launch, and whether the session launched again while the watch
// ran.
type launchWatch struct {
	program string
	again   bool
}

// launchWatched is a launch watch's end (launchWatchJob): it saw the
// session die or ran out of time, having answered a trust prompt or not.
// settled is the prompt the screen still showed at the end, answered or
// left to the user, which a watch of a relaunch (deliverLaunchWatched)
// does not answer again until it is gone: aider's and gemini's prompt
// lines linger on screen after their answer.
type launchWatched struct {
	session  string
	answered bool
	settled  string
}

// watchLaunch watches inst's session, which has just launched, for its
// agent's trust prompts, and answers each once (launchWatchJob). It is
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
	m.startWatch(name, program, "")
}

// launchSession is the lifecycle session a launch watch reaches name by:
// a tmux.Session with no client, on the model's executor.
func (m *Model) launchSession(name, program string) *tmux.Session {
	return tmux.NewSessionNamedWithDeps(name, program, tmux.MakePtyFactory(), m.executor())
}

// startWatch records a watch of name and queues its job, which leaves the
// prompt settled alone while the screen shows it.
func (m *Model) startWatch(name, program, settled string) {
	if m.watching == nil {
		m.watching = make(map[string]*launchWatch)
	}
	m.watching[name] = &launchWatch{program: program}
	m.spawnBackground(launchWatchJob(m.launchSession(name, program), launchWatchEvery, launchWatchFor, settled))
}

// launchWatchJob captures ts's pane every every, for up to within, and
// answers each trust prompt the screen shows (trustAnswer.see), leaving
// alone the prompt settled while the screen still shows it. It watches to
// the deadline after an answer, since one prompt can follow another
// (Claude's MCP prompt after its trust prompt), and returns early only
// when the session is gone: a capture that fails on a session tmux says
// is dead. It reads no model state: ts was built on the loop.
func launchWatchJob(ts *tmux.Session, every, within time.Duration, settled string) Job {
	name := ts.SessionName()
	return func() any {
		deadline := time.Now().Add(within)
		a := trustAnswer{ts: ts, prompt: settled, settled: settled != ""}
		answered := false
		for {
			screen, err := ts.CapturePaneContent()
			if err == nil && a.see(screen) {
				answered = true
			}
			if err != nil && ts.SessionLiveness() == tmux.LivenessDead {
				return launchWatched{session: name, answered: answered}
			}
			if !time.Now().Add(every).Before(deadline) {
				return launchWatched{session: name, answered: answered, settled: a.settledPrompt()}
			}
			time.Sleep(every)
		}
	}
}

// trustAnswer answers the trust prompts a launch watch's captures show,
// each once while the screen shows it, except one the agent leaves to the
// person (TrustPromptLeftToUser: Claude's MCP server dialogs, whose
// approval starts a process the repository names), which gets nothing. A
// prompt answered as shown gets
// the agent's keys (tmux.Session.AnswerTrustPrompt). A prompt with a
// choice (Claude's safety check, which selects "No, exit" first) gets
// Enter only once a capture shows its menu's cursor on the choice; until
// then each capture moves the menu one option down, after the agent drew
// the last move. A menu that never reaches the choice, or shows no single
// cursor, is left to the user: no key it sends can then confirm "No".
type trustAnswer struct {
	ts *tmux.Session
	// prompt is the trust prompt the last capture showed, "" for none;
	// settled reports that it was answered or left to the user.
	prompt  string
	settled bool
	// moves counts the Downs sent to prompt's menu, the last one from the
	// option from; waits counts the captures since that still show the
	// cursor on from.
	moves int
	from  string
	waits int
}

// see handles one capture of the pane, screen, and reports whether it
// answered a prompt.
func (a *trustAnswer) see(screen string) bool {
	shown := a.ts.TrustPromptShown(screen)
	if shown != a.prompt {
		*a = trustAnswer{ts: a.ts, prompt: shown}
	}
	if shown == "" || a.settled {
		return false
	}
	if a.ts.TrustPromptLeftToUser(shown) {
		a.leave("the person's to answer")
		return false
	}
	choice := a.ts.TrustPromptChoice(shown)
	if choice == "" {
		return a.answered(a.ts.AnswerTrustPrompt())
	}
	selected, ok := tmux.SelectedOption(screen)
	switch {
	case !ok:
		a.leave("no single menu cursor")
	case tmux.IsOption(selected, choice):
		return a.answered(a.ts.PressKeys("Enter"))
	case a.moves > 0 && selected == a.from:
		if a.waits++; a.waits > trustMenuWaits {
			a.leave("the menu did not move")
		}
	case a.moves >= trustMenuMoves:
		a.leave("the menu never reached " + choice)
	default:
		if err := a.ts.PressKeys("Down"); err != nil {
			a.leave(err.Error())
			break
		}
		a.moves, a.from, a.waits = a.moves+1, selected, 0
	}
	return false
}

// answered settles the prompt with the result of sending its answer, and
// reports whether it was sent.
func (a *trustAnswer) answered(err error) bool {
	a.settled = true
	if err != nil {
		log.For("core").Error("launch_watch.trust_prompt_failed", "session", a.ts.SessionName(), "prompt", a.prompt, "err", err)
		return false
	}
	log.For("core").Info("launch_watch.trust_prompt_answered", "session", a.ts.SessionName(), "prompt", a.prompt)
	return true
}

// leave settles the prompt unanswered, for the user, saying why.
func (a *trustAnswer) leave(why string) {
	a.settled = true
	log.For("core").Warn("launch_watch.trust_prompt_left", "session", a.ts.SessionName(), "prompt", a.prompt, "why", why)
}

// settledPrompt is the prompt the last capture showed, once settled.
func (a *trustAnswer) settledPrompt() string {
	if a.settled {
		return a.prompt
	}
	return ""
}

// deliverLaunchWatched ends a watch. A session that launched again while
// it ran is watched afresh, for the latest launch's program: the watch
// that ended may have run out of time, or seen the old session die, just
// before the new one showed its prompt. The prompt the ended watch
// settled is not answered again while the screen still shows it.
func (m *Model) deliverLaunchWatched(r launchWatched) {
	w := m.watching[r.session]
	delete(m.watching, r.session)
	if w != nil && w.again {
		m.startWatch(r.session, w.program, r.settled)
	}
}
