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
// (Down) looking for its option: past it, the prompt is left to the user.
const trustMenuMoves = 4

// trustMenuSettle is how long a watch waits, after it first sees a trust
// prompt, before its first key, and again after a Down the menu has not
// drawn before it presses Down once more. Claude 2.1.299 refuses every key
// for a moment after a dialog opens (its bundle's input guard is 2,000 ms
// from mounting; a probe saw a Down at once refused and one 1.5 s later
// taken), so a key sent at first sight is lost, and a refused Enter would
// leave a prompt the watch thinks it answered.
const trustMenuSettle = 2500 * time.Millisecond

// trustMenuLost is how many settles a Down may go undrawn, with the menu
// unchanged, before an answer counts it as refused rather than on its way:
// until then no Enter goes, and no new move, since a Down still to land
// would move the cursor first. A Down that draws later than that (10 s) is
// taken for refused: the one lag an answer does not survive.
const trustMenuLost = 4

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
	m.spawnBackground(launchWatchJob(m.launchSession(name, program), launchWatchEvery, launchWatchFor, trustMenuSettle, settled))
}

// launchWatchJob captures ts's pane every every, for up to within, and
// answers each trust prompt the screen shows (trustAnswer.see), leaving
// alone the prompt settled while the screen still shows it. It watches to
// the deadline after an answer, since one prompt can follow another
// (Claude's MCP prompt after its trust prompt), and returns early only
// when the session is gone: a capture that fails on a session tmux says
// is dead. It reads no model state: ts was built on the loop.
func launchWatchJob(ts *tmux.Session, every, within, settle time.Duration, settled string) Job {
	name := ts.SessionName()
	return func() any {
		deadline := time.Now().Add(within)
		a := trustAnswer{ts: ts, settle: settle, prompt: settled, settled: settled != ""}
		answered := false
		for {
			screen, err := ts.CapturePaneContent()
			if err == nil && a.see(screen, time.Now()) {
				answered = true
			}
			if err != nil && ts.SessionLiveness() == tmux.LivenessDead {
				return launchWatched{session: name, answered: answered}
			}
			if !time.Now().Add(every).Before(deadline) {
				a.end()
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
// prompt answered as shown gets the agent's keys
// (tmux.Session.AnswerTrustPrompt). A prompt with a choice (Claude's safety
// check, which selects "No, exit" first) gets Enter only once a capture
// shows its menu's cursor on the choice; until then the menu is moved one
// option down at a time, after the agent drew the last move. No key goes
// before settle has passed since the prompt first showed, and a Down the
// menu has not drawn is pressed again once settle has passed since it: the
// agent refuses keys just after a dialog opens, until the menu drew one.
// Enter waits until every Down sent was seen drawn, since one that lands
// late would move the cursor off the choice first. A menu that never reaches
// the choice, shows no single cursor, or has not reached it when the watch
// ends is left to the user: no key it sends can then confirm "No".
type trustAnswer struct {
	ts     *tmux.Session
	settle time.Duration
	// prompt is the trust prompt the last capture showed, "" for none;
	// settled reports that it was answered or left to the user. seenAt is
	// when a capture first showed it.
	prompt  string
	settled bool
	seenAt  time.Time
	// moves counts the moves sent to prompt's menu toward its choice, and
	// sent every Down, a move pressed again included. drawn counts the
	// changes of the cursor the captures saw, last the option it was on at
	// the one before, and activeAt is when a Down went or a change was seen.
	moves    int
	sent     int
	drawn    int
	last     string
	activeAt time.Time
}

// see handles one capture of the pane, screen, taken at now, and reports
// whether it answered a prompt.
func (a *trustAnswer) see(screen string, now time.Time) bool {
	shown := a.ts.TrustPromptShown(screen)
	if shown != a.prompt {
		*a = trustAnswer{ts: a.ts, settle: a.settle, prompt: shown, seenAt: now}
	}
	if shown == "" || a.settled {
		return false
	}
	if a.ts.TrustPromptLeftToUser(shown) {
		a.leave("the person's to answer")
		return false
	}
	if now.Sub(a.seenAt) < a.settle {
		return false
	}
	choice := a.ts.TrustPromptChoice(shown)
	if choice == "" {
		return a.answered(a.ts.AnswerTrustPrompt())
	}
	selected, ok := tmux.SelectedOption(screen)
	if !ok {
		a.leave("no single menu cursor")
		return false
	}
	if a.last != "" && selected != a.last {
		a.drawn, a.activeAt = a.drawn+1, now
	}
	a.last = selected
	// pending is how many Downs may still be on their way: each change a
	// capture saw drew one at least, so it is never fewer than are. A cursor
	// move the watch did not cause (a person pressing keys in the pane
	// meanwhile) raises drawn too, and may hide one on its way: acceptable,
	// since the person is at the pane. Downs that drew nothing for
	// trustMenuLost settles since the menu last changed were refused, and
	// count as landed, wherever the cursor is.
	pending, quiet := a.sent-a.drawn, now.Sub(a.activeAt)
	if pending > 0 && quiet >= trustMenuLost*a.settle {
		a.drawn, pending = a.sent, 0
	}
	switch {
	case tmux.IsOption(selected, choice):
		// Enter only with no Down in flight: one still to land would move
		// the cursor first, and a menu of two wraps onto "No, exit".
		if pending > 0 {
			return false
		}
		return a.answered(a.ts.PressKeys("Enter"))
	case pending > 0:
		// A Down is not drawn: refused, or still to come. A menu that drew
		// none yet may still be refusing keys, so it gets Down again once
		// settle has passed; one that drew a Down takes keys, and the rest
		// are on their way: another would only follow them.
		if a.drawn == 0 && quiet >= a.settle {
			a.down(now)
		}
	case a.moves >= trustMenuMoves:
		a.leave("the menu never reached " + choice)
	default:
		a.moves++
		a.down(now)
	}
	return false
}

// down presses Down on the menu at now.
func (a *trustAnswer) down(now time.Time) {
	if err := a.ts.PressKeys("Down"); err != nil {
		a.leave(err.Error())
		return
	}
	a.sent, a.activeAt = a.sent+1, now
}

// end leaves to the user a menu the watch was still moving when it ran out
// of time. A prompt it sent no key yet stays unsettled, for a relaunch's
// watch to answer.
func (a *trustAnswer) end() {
	if a.prompt != "" && !a.settled && a.moves > 0 {
		a.leave("the watch ended before the menu reached its option")
	}
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
