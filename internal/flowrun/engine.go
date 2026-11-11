package flowrun

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/aidan-bailey/loom/sched"
)

// Report is an agent run's end, as 3a's record holds it.
type Report struct {
	Status   string `json:"status"` // DONE, DONE_WITH_CONCERNS, NEEDS_CONTEXT, BLOCKED, TURN_CAP, USAGE_LIMIT, failed, cancelled
	Text     string `json:"text,omitempty"`
	Question string `json:"question,omitempty"`
	Path     string `json:"path,omitempty"` // report.md
	Why      string `json:"why,omitempty"`
	Limit    *Limit `json:"limit,omitempty"` // at USAGE_LIMIT: which limit, and when it resets
}

func (r Report) done() bool { return r.Status == "DONE" || r.Status == "DONE_WITH_CONCERNS" }

// GateResult is a gate run's end.
type GateResult struct {
	Ended   string     `json:"ended"` // passed, failed or infra
	Failing []GateStep `json:"failing,omitempty"`
	Table   string     `json:"table,omitempty"`
	// Free is the free space, in bytes, on the worktree's filesystem when
	// the run ended; 0 when it couldn't be read. Only the record reads it.
	Free uint64 `json:"free,omitempty"`
}

// GateStep is a failing step of a gate run.
type GateStep struct {
	Line    string `json:"line"`
	Verdict string `json:"verdict"`
	Log     string `json:"log,omitempty"`
}

// Regenerated is the engine's commit of the generated files a gate run
// left changed: its head and their paths; empty when it made none.
type Regenerated struct {
	Head  string   `json:"head,omitempty"`
	Paths []string `json:"paths,omitempty"`
}

// Conformance is loom flow conformance's output for a story.
type Conformance struct {
	Path    string   `json:"path"`
	Markers []string `json:"markers,omitempty"`
}

// Effects is everything the engine does outside its state. Every wait is
// the effect's: Agent, Resume and Gates return once the run they start, or
// follow, has ended.
type Effects interface {
	AgentStarted(name string) (bool, error)
	// Agent starts agent run name in dir ("" for the session's worktree;
	// parallel pairs spec §5.3), or follows it.
	Agent(dir, name, role, effort, brief string) (Report, error)
	// Resume answers the ended agent run name with text, or follows the
	// answer already queued with that text.
	Resume(name, text string) (Report, error)
	GatesStarted(name string) (bool, error)
	Services(commands []string) error
	// Gates runs a story's gates over commits, or the final gates when
	// commits is nil.
	Gates(name string, commits []string) (GateResult, error)
	// CommitGenerated commits what gate run name left changed in the
	// session's worktree when every change is to a file one of globs
	// matches (flow's glob_match), and is that commit; nothing when the tree
	// is clean or holds any other change. Its commit at HEAD, found again
	// after a crash, is given again.
	CommitGenerated(name string, globs []string) (Regenerated, error)
	Head(dir string) (string, error)
	Clean(dir string) (bool, error)
	Commits(base, head string) ([]string, error)
	Conformance(story, base, head string) (Conformance, error)
	// Premises is plan_premises.py's output when it flagged anything, else "".
	Premises(rev string) (string, error)
	// DocsCheck is the paths base..head changed that aren't doc paths
	// (loom flow conf docs-check): none when it changed only docs.
	DocsCheck(base, head string) ([]string, error)
	// Lane is the lane a laned story's developer runs in (parallel pairs
	// spec §5.3): a worktree of the repository named name, detached at
	// base, made, or reused when it is already one.
	Lane(name, base string) (string, error)
	// Pick cherry-picks commits in order onto the session's worktree, whose
	// HEAD is onto, or onto with a prefix of commits already picked (a crash
	// mid-pick), and is the commits it made. A conflict aborts the pick as a
	// *PickConflict.
	Pick(commits []string, onto string) ([]string, error)
	// DropLane removes the lane at dir, refusing one that isn't clean.
	DropLane(dir string) error
	// OnlyPlanCommits reports whether head descends from base and every
	// commit in base..head touches only the run's spec and plan (spec §5.7).
	OnlyPlanCommits(base, head string) (bool, error)
	Save(st *State) error
}

// PickConflict is a pick that didn't apply: the lane commit it stopped at
// and the files it conflicted on. Pick has aborted it.
type PickConflict struct {
	Commit string
	Files  []string
}

func (c *PickConflict) Error() string {
	files := strings.Join(c.Files, ", ")
	if files == "" {
		files = "no file (the pick came out empty)"
	}
	return fmt.Sprintf("the lane commit %s conflicts on %s", short(c.Commit), files)
}

// LimitWaitWithin is how far off a usage limit's reset may be, from when
// the agent stopped on it, for the engine to retry the step on its own
// (usage-limit hold spec §3): any 5-hour window's. A reset further away, or
// an unknown one, asks the manager.
const LimitWaitWithin = 6 * time.Hour

var (
	errParked  = errors.New("the run parked")
	errStopped = errors.New("the manager stopped the run")
)

// finalStory is the pseudo-story the final round's fixes run under
// (planning amendment 14).
const finalStory = "final"

var notName = regexp.MustCompile(`[^A-Za-z0-9._-]`)

var errClosed = errors.New("the run closed while this track waited")

type engine struct {
	st      *State
	fx      Effects
	seq     map[string]int // attempts at each name prefix, this pass
	resumes map[string]int // resumes of each agent run, this pass
	limits  map[string]int // usage limits each agent run stopped on, this pass
	// lastReset is the reset of the last usage limit each agent run stopped
	// on, this pass.
	lastReset map[string]time.Time
	final     Story
	// accepted is each story's gate steps the manager accepted failing, by
	// step line, with the gate run they were accepted at. A replay rebuilds
	// it from the recalled gate results and answers (thread 20).
	accepted map[string]map[string]string

	// mu is held by whichever track runs engine code. A track lets go of it
	// only while it waits (wait), so another track can run meanwhile
	// (parallel pairs spec §5.3).
	mu      sync.Mutex
	closed  bool  // the run parked, stopped, ended or failed: no track touches the state again
	outcome error // what closed it: nil when the main track ended
	over    chan struct{}
	wg      sync.WaitGroup // the tracks' goroutines, which tests wait for
	main    *track
}

// track is one line of the engine's work (parallel pairs spec §5.3): the
// main track in the session's worktree, or a lane track running a laned
// story's developer in its lane.
type track struct {
	*engine
	head string        // the head last recorded
	cur  string        // the story in hand, "" outside one
	sent []SentFinding // the story in hand's findings sent to a fix round, for its doc pass
	dir  string        // the lane's path; "" for the session's worktree
	lane *laneRun      // the lane it runs, for a lane track; nil for the main track
}

// laneRun is a laned story's developer phase in its lane (spec §5.3).
type laneRun struct {
	s, partner *Story
	t          *track        // the lane track
	base       string        // the lane's base: its partner's base
	done       chan struct{} // closed once the developer phase has ended
	dev        Report        // its developer's report, when it ended without error
	notes      []string      // the lane track's notes, which the join appends
}

// laneCheck is the join's look at a lane, memoized (spec §5.5).
type laneCheck struct {
	Head    string   `json:"head"`
	Clean   bool     `json:"clean"`
	Commits []string `json:"commits,omitempty"` // base..head, when clean
}

// pickResult is a pick's end, memoized: the head it picked onto and the
// commits it made, or the conflict it stopped at.
type pickResult struct {
	Onto     string   `json:"onto"`
	Picked   []string `json:"picked,omitempty"`
	Conflict string   `json:"conflict,omitempty"`
}

// Run drives st until it parks or ends, saving it after every step. It
// returns nil when st has parked or ended, and otherwise the error it
// could not get past (a lost daemon, a file it can't write), with st left
// running for `loom flow run resume`. It returns at the run's first
// outcome: a track still waiting on a job finds the run closed when it
// wakes, and touches nothing.
func Run(st *State, fx Effects) error { return newEngine(st, fx).start() }

func newEngine(st *State, fx Effects) *engine {
	e := &engine{st: st, fx: fx, seq: map[string]int{}, resumes: map[string]int{}, limits: map[string]int{},
		lastReset: map[string]time.Time{}, final: Story{ID: finalStory}, accepted: map[string]map[string]string{},
		over: make(chan struct{})}
	e.main = &track{engine: e, head: st.Base}
	return e
}

func (e *engine) start() error {
	st := e.st
	st.Status, st.Question, st.Why = Running, nil, ""
	st.Phase, st.Notes, st.Kept = "", nil, nil
	for i := range st.Stories {
		st.Stories[i].Row = Row{}
	}
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.mu.Lock()
		defer e.mu.Unlock()
		e.finish(e.main.run())
	}()
	<-e.over
	e.mu.Lock()
	defer e.mu.Unlock()
	switch err := e.outcome; {
	case errors.Is(err, errParked):
		return nil
	case errors.Is(err, errStopped):
		return e.end(Stopped)
	case err != nil:
		return err
	}
	return e.end(Done)
}

// finish closes the run with err, its first outcome (nil: the main track
// ended); a later outcome changes nothing.
func (e *engine) finish(err error) {
	if e.closed {
		return
	}
	e.closed, e.outcome = true, err
	close(e.over)
}

// wait runs fn, which waits on a job or a track, without the engine's lock.
// It is errClosed when the run closed meanwhile; the caller then returns
// at once, touching nothing.
func (e *engine) wait(fn func() error) error {
	e.mu.Unlock()
	err := fn()
	e.mu.Lock()
	if e.closed {
		return errClosed
	}
	return err
}

func (e *engine) end(status string) error {
	e.st.Status, e.st.Question = status, nil
	e.st.End = time.Now().UTC().Format(time.RFC3339)
	e.st.Phase = "ended"
	return e.fx.Save(e.st)
}

func (e *track) run() error {
	if err := e.premises(); err != nil {
		return err
	}
	var lane *laneRun
	for i := range e.st.Stories {
		s := &e.st.Stories[i]
		base, dev, joined := e.head, Report{}, lane != nil
		if joined {
			var err error
			if base, dev, err = e.join(lane); err != nil {
				return err
			}
		}
		lane = nil
		if i+1 < len(e.st.Stories) && e.st.Stories[i+1].Lane {
			lane = e.startLane(&e.st.Stories[i+1], s)
		}
		if err := e.story(s, base, dev, joined); err != nil {
			return err
		}
	}
	return e.finalRound()
}

// note adds a line to the run's notes; a lane track's go to its lane, for
// the join to append, so their order doesn't depend on which track woke
// first.
func (e *track) note(format string, args ...any) {
	n := fmt.Sprintf(format, args...)
	if e.lane != nil {
		e.lane.notes = append(e.lane.notes, n)
		return
	}
	e.st.Notes = append(e.st.Notes, n)
}

// recall unmarshals key's memoized result into v.
func (e *engine) recall(key string, v any) bool {
	raw, ok := e.st.Memo[key]
	return ok && json.Unmarshal(raw, v) == nil
}

// remember memoizes v under key and saves the state.
func (e *engine) remember(key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if e.st.Memo == nil {
		e.st.Memo = map[string]json.RawMessage{}
	}
	e.st.Memo[key] = raw
	return e.fx.Save(e.st)
}

// next is the name of prefix's next attempt this pass: <run>-<prefix><k>.
func (e *engine) next(prefix string) string {
	e.seq[prefix]++
	return fmt.Sprintf("%s-%s%d", e.st.ID, prefix, e.seq[prefix])
}

// storyPrefix is story id as a name part: s<id>.
func storyPrefix(id string) string { return "s" + notName.ReplaceAllString(id, "-") }

// headAt is HEAD memoized under key, which becomes the head last recorded.
func (e *track) headAt(key string) (string, error) {
	var h string
	if !e.recall("head:"+key, &h) {
		var err error
		if h, err = e.fx.Head(e.dir); err != nil {
			return "", err
		}
		if err := e.remember("head:"+key, h); err != nil {
			return "", err
		}
	}
	e.head = h
	return h, nil
}

// commitsAt is base..HEAD's commits memoized under key.
func (e *track) commitsAt(key, base string) ([]string, error) {
	var cs []string
	if e.recall("commits:"+key, &cs) {
		return cs, nil
	}
	cs, err := e.fx.Commits(base, e.head)
	if err != nil {
		return nil, err
	}
	return cs, e.remember("commits:"+key, cs)
}

// ask is the answer to q, or parks the run on it.
func (e *track) ask(q Question) (string, error) {
	q.Story = e.cur
	if s := e.storyOf(e.cur); s != nil {
		s.Row.Escalated++
	}
	var a string
	if e.recall("q:"+q.Key, &a) {
		var head string
		if e.lane == nil && e.recall("took:"+q.Key, &head) {
			e.head = head // the manager's own commits, taken with its answer
		}
		return a, nil
	}
	q.At = e.head
	if e.lane != nil {
		q.At = e.main.head // answer compares it with HEAD in the session's worktree (spec §5.4)
	}
	e.st.Question, e.st.Status = &q, Parked
	if err := e.fx.Save(e.st); err != nil {
		return "", err
	}
	return "", errParked
}

func (e *engine) storyOf(id string) *Story {
	for i := range e.st.Stories {
		if e.st.Stories[i].ID == id {
			return &e.st.Stories[i]
		}
	}
	return nil
}

// checkTree parks as tree-moved when HEAD or the tree isn't what the
// engine recorded, before a fresh dispatch of name (planning amendment 12).
func (e *track) checkTree(name string) error {
	head, err := e.fx.Head(e.dir)
	if err != nil {
		return err
	}
	clean, err := e.fx.Clean(e.dir)
	if err != nil {
		return err
	}
	if head == e.head && clean {
		return nil
	}
	if clean {
		only, err := e.fx.OnlyPlanCommits(e.head, head)
		if err != nil {
			return err
		}
		if only {
			_, err := e.headAt("adopt:" + name)
			return err
		}
	}
	why := fmt.Sprintf("HEAD is %s, and the engine last recorded %s", head, e.head)
	if !clean {
		why = "the tree has uncommitted changes; " + why
	}
	a, err := e.ask(Question{Key: "tree:" + name, Kind: KindTreeMoved, Text: why + ", before it starts " + name})
	if err != nil {
		return err
	}
	if a == WordStop {
		return errStopped
	}
	_, err = e.headAt("tree:" + name)
	return err
}

// retake is the head the tree check before step name took, when it took
// one (a continue, or spec and plan commits it adopted), set again on a
// replay that skips the check, and the note an adoption leaves (spec §5.7).
func (e *track) retake(name string) {
	var h string
	if e.recall("head:tree:"+name, &h) {
		e.head = h
	}
	if e.recall("head:adopt:"+name, &h) {
		e.head = h
		e.note("took the spec and plan commits up to %s before %s", short(h), name)
	}
}

// agent is agent run name's report: memoized, followed when it exists, else
// started after the tree check.
func (e *track) agent(name, role, effort, brief string) (Report, error) {
	var r Report
	if e.recall("agent:"+name, &r) {
		return e.outlast(name, r)
	}
	started, err := e.fx.AgentStarted(name)
	if err != nil {
		return r, err
	}
	if !started {
		if err := e.checkTree(name); err != nil {
			return r, err
		}
	}
	e.retake(name)
	if !slices.Contains(e.st.Agents, name) {
		e.st.Agents = append(e.st.Agents, name)
		if err := e.fx.Save(e.st); err != nil {
			return r, err
		}
	}
	if err := e.wait(func() (err error) { r, err = e.fx.Agent(e.dir, name, role, effort, brief); return err }); err != nil {
		return r, err
	}
	if err := e.remember("agent:"+name, r); err != nil {
		return r, err
	}
	return e.outlast(name, r)
}

// resume is the report of name resumed with text (planning amendment 13),
// past any usage limit it stops on (outlast).
func (e *track) resume(name, text string) (Report, error) {
	r, err := e.resumeOnce(name, text)
	if err != nil {
		return r, err
	}
	return e.outlast(name, r)
}

// outlast retries name past each usage limit it stops on (usage-limit hold
// spec §3), and is its report once it ends otherwise. A reset within
// LimitWaitWithin of the stop is waited out with a note: the retry's job
// waits in the queue, which holds the account's agents until the reset. A
// reset further away, an unknown one, or the reset the run's last limit
// named (its retry ran unheld), asks the manager: wait retries the same
// way, stop stops. The retry resumes the run's session (the CLI
// starts the run again from its brief when it has none), with a text of
// its own for each limit, so a follow after a restart never takes one
// retry for another.
func (e *track) outlast(name string, r Report) (Report, error) {
	for r.Status == AgentUsageLimit {
		e.limits[name]++
		n := e.limits[name]
		l := Limit{}
		if r.Limit != nil {
			l = *r.Limit
		}
		window := sched.WindowLabel(l.Window)
		when := sched.ResetText(l.Resets, l.At)
		// The same reset twice means the retry ran before it: the queue did
		// not hold it (a daemon that keeps no hold), so a further retry would
		// only be refused again, every few seconds until the reset.
		again := !l.Resets.IsZero() && l.Resets.Equal(e.lastReset[name])
		e.lastReset[name] = l.Resets
		if !again && !l.Resets.IsZero() && l.Resets.Sub(l.At) <= LimitWaitWithin {
			e.note("%s: usage limit (%s), waiting for the reset at %s", name, window, when)
		} else {
			text := fmt.Sprintf("%s stopped on the account's usage limit (%s limit, resets %s)", name, window, when)
			if again {
				text += ", again before that reset: the queue did not hold its retry"
			}
			a, err := e.ask(Question{Key: fmt.Sprintf("limit:%s:%d", name, n), Kind: KindUsageLimit, Text: text})
			if err != nil {
				return r, err
			}
			if a == WordStop {
				return r, errStopped
			}
		}
		var err error
		text := fmt.Sprintf("The usage limit that stopped you has reset (retry %d). Continue from where you stopped.", n)
		if r, err = e.resumeOnce(name, text); err != nil {
			return r, err
		}
	}
	return r, nil
}

// resumeOnce is the report of name resumed with text, memoized.
func (e *track) resumeOnce(name, text string) (Report, error) {
	e.resumes[name]++
	key := fmt.Sprintf("resume:%s:%d", name, e.resumes[name])
	var r Report
	if e.recall(key, &r) {
		return r, nil
	}
	if err := e.wait(func() (err error) { r, err = e.fx.Resume(name, text); return err }); err != nil {
		return r, err
	}
	return r, e.remember(key, r)
}

// settle answers name's NEEDS_CONTEXT questions until it reports otherwise.
func (e *track) settle(name string, r Report) (Report, error) {
	for n := 1; r.Status == "NEEDS_CONTEXT"; n++ {
		a, err := e.ask(Question{Key: fmt.Sprintf("ctx:%s:%d", name, n), Kind: KindNeedsContext, Text: r.Question, Paths: paths(r.Path)})
		if err != nil {
			return r, err
		}
		if r, err = e.resume(name, a); err != nil {
			return r, err
		}
	}
	return r, nil
}

func paths(p ...string) []string {
	var out []string
	for _, s := range p {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// failed asks the manager about name's failure, and is the decision the
// retry takes: "" for a plain retry, else the manager's text, for the next
// attempt's brief (feedback thread 12).
func (e *track) failed(name string, r Report) (string, error) {
	why := r.Why
	if why == "" {
		why = "it reported " + r.Status
	}
	a, err := e.ask(Question{Key: "failed:" + name, Kind: KindAgentFailed, Text: name + " ended without a usable report: " + why, Paths: paths(r.Path)})
	switch {
	case err != nil:
		return "", err
	case a == WordStop:
		return "", errStopped
	case a == WordRetry:
		return "", nil
	}
	return a, nil
}

// review runs a reviewer at prefix until its report reads (spec §5.6).
func (e *track) review(prefix, role, brief string) (Review, Report, error) {
	next := brief
	for {
		name := e.next(prefix)
		r, err := e.agent(name, role, "", next)
		if err == nil {
			r, err = e.settle(name, r)
		}
		if err != nil {
			return Review{}, r, err
		}
		if r.done() {
			rev, perr := ParseReview(r.Text)
			if perr == nil {
				return rev, r, nil
			}
			r.Why = perr.Error()
		}
		decision, err := e.failed(name, r)
		if err != nil {
			return Review{}, r, err
		}
		next = withEarlier(brief, nil, decision)
	}
}

// developerEffort is a story developer's effort: xhigh for an
// intermediate-dev on a story with landmines.
func developerEffort(s *Story, role string) string {
	if role == "intermediate-dev" && len(s.Landmines) > 0 {
		return "xhigh"
	}
	return ""
}

// build runs developers at prefix until one reports done with commits,
// escalating as spec §5.3 step 2 says. fix is true for a fix round, whose
// developers stay at their pinned effort.
func (e *track) build(s *Story, prefix, role, brief string, fix bool) (Report, error) {
	before := e.head
	var earlier []string
	decision := ""
	effort := func() string {
		if fix {
			return ""
		}
		return developerEffort(s, role)
	}
	name := e.next(prefix)
	r, err := e.agent(name, role, effort(), brief)
	resumed := false
	for {
		if err == nil {
			r, err = e.settle(name, r)
		}
		if err != nil {
			return r, err
		}
		switch {
		case r.done():
			head, err := e.headAt(name)
			if err != nil {
				return r, err
			}
			if head == before && fix {
				// A fixer can find its findings fixed already (a kept Minor a
				// later fix took care of); the gates and the re-review after
				// every fix round judge that, so it's no failure.
				e.note("%s: %s reported done with no commits; the re-review checks its findings", storyName(s), name)
			}
			if head != before || fix {
				if !fix {
					s.Row.Developer = role // the story's own developer, never a fixer
				}
				s.Row.Deviations += Deviations(r.Text)
				return r, nil
			}
			d, err := e.failed(name, Report{Status: r.Status, Why: "it reported done with no commits", Path: r.Path})
			if err != nil {
				return r, err
			}
			if d != "" {
				decision = d
			}
		case r.Status == "TURN_CAP" && role == "intermediate-dev" && !resumed:
			s.Row.TurnCap, resumed = true, true
			r, err = e.resume(name, "Continue from where you stopped.")
			continue
		case (r.Status == "TURN_CAP" || r.Status == "BLOCKED") && role == "junior-dev":
			s.Row.TurnCap = s.Row.TurnCap || r.Status == "TURN_CAP"
			s.Row.UpATier++
			e.note("%s: %s reported %s; a fresh intermediate-dev took it", storyName(s), name, r.Status)
			earlier, role = append(earlier, r.Path), "intermediate-dev"
		case r.Status == "BLOCKED" && role == "intermediate-dev" && decision != "":
			s.Row.UpATier++
			e.note("%s: %s was BLOCKED with the manager's decision; senior-dev took it", storyName(s), name)
			earlier, role = append(earlier, r.Path), "senior-dev"
		case r.Status == "BLOCKED":
			a, err := e.ask(Question{Key: "blocked:" + name, Kind: KindBlocked, Text: name + " is BLOCKED: its report says on what", Paths: paths(r.Path)})
			if err != nil {
				return r, err
			}
			if a == WordStop {
				return r, errStopped
			}
			earlier, decision = append(earlier, r.Path), a
		default:
			s.Row.TurnCap = s.Row.TurnCap || r.Status == "TURN_CAP"
			d, err := e.failed(name, r)
			if err != nil {
				return r, err
			}
			if d != "" {
				decision = d
			}
		}
		name = e.next(prefix)
		r, err = e.agent(name, role, effort(), withEarlier(brief, earlier, decision))
		resumed = false
	}
}

// gates is gate run name's result: memoized, followed when it exists, else
// started after the profile's services.
func (e *track) gates(name string, commits []string) (GateResult, error) {
	var r GateResult
	if e.recall("gates:"+name, &r) {
		return r, nil
	}
	started, err := e.fx.GatesStarted(name)
	if err != nil {
		return r, err
	}
	if !slices.Contains(e.st.Gates, name) {
		e.st.Gates = append(e.st.Gates, name)
	}
	if !started {
		var serr error
		if err := e.wait(func() error { serr = e.fx.Services(e.st.Blocks.Services); return nil }); err != nil {
			return r, err
		}
		if serr != nil {
			r = GateResult{Ended: "infra", Table: "FAIL (infra): a service failed: " + serr.Error()}
			return r, e.remember("gates:"+name, r)
		}
	}
	if err := e.wait(func() (err error) { r, err = e.fx.Gates(name, commits); return err }); err != nil {
		return r, err
	}
	return r, e.remember("gates:"+name, r)
}

// regenerated is the engine's commit of the generated files gate run name
// left changed (CommitGenerated), memoized; its head becomes the head last
// recorded. A run whose state names no generated globs (a profile without
// them, or a run an older build started) commits nothing, as before.
func (e *track) regenerated(name string) (Regenerated, error) {
	var r Regenerated
	if len(e.st.Generated) == 0 {
		return r, nil
	}
	if !e.recall("regen:"+name, &r) {
		var err error
		if r, err = e.fx.CommitGenerated(name, e.st.Generated); err != nil {
			return r, err
		}
		if err := e.remember("regen:"+name, r); err != nil {
			return r, err
		}
	}
	if r.Head != "" {
		e.head = r.Head
	}
	return r, nil
}

func logsOf(r GateResult) []string {
	var out []string
	for _, f := range r.Failing {
		out = append(out, f.Log)
	}
	return paths(out...)
}

// gateLoop runs s's gates (final ones when s is the final pseudo-story)
// until they pass or the manager accepts them, with a fix after a failure.
func (e *track) gateLoop(s *Story, prefix, base string) error {
	s.Row.Status = "gating"
	final := s.ID == finalStory
	retried := false // the run once more after generated files were committed
	for failures := 0; ; {
		name := e.next(prefix + "-gates")
		var commits []string
		if !final {
			var err error
			if commits, err = e.commitsAt(name, base); err != nil {
				return err
			}
			s.Row.Commits = len(commits) // for show while the story gates; done counts again
		}
		res, err := e.gates(name, commits)
		if err != nil {
			return err
		}
		regen, err := e.regenerated(name)
		if err != nil {
			return err
		}
		// A failed gate that rewrote only generated files may have failed on
		// their stale copies alone (feedback thread 22): it runs once more,
		// with them committed, before a fixer.
		again := regen.Head != "" && res.Ended == "failed" && !retried
		if regen.Head != "" {
			n := fmt.Sprintf("%s: %s left %s changed, which the engine committed as %s", storyName(s), name, strings.Join(regen.Paths, ", "), short(regen.Head))
			if again {
				n += " and gated again"
			}
			e.note("%s", n)
		}
		if res.Ended == "passed" {
			return nil
		}
		if again {
			retried = true
			continue
		}
		failures++
		decision := ""
		// A failure only on steps the manager accepted is asked about, not
		// fixed: a fixer can't fix what the plan accepts (thread 20).
		at := e.acceptedAt(s, res)
		if res.Ended == "infra" || failures > 1 || at != nil {
			text := name + " failed:\n" + res.Table
			if at != nil {
				text = name + " failed only on steps the manager accepted at " + strings.Join(at, ", ") + ":\n" + res.Table
			}
			a, err := e.ask(Question{Key: "gates:" + name, Kind: KindGateFailure, Text: text, Paths: logsOf(res)})
			if err != nil {
				return err
			}
			switch a {
			case WordStop:
				return errStopped
			case WordAccept:
				e.note("%s: the manager accepted the failing gates of %s", storyName(s), name)
				e.accept(s, name, res)
				return nil
			case WordRetry:
				continue
			}
			decision = a
		}
		if _, err := e.build(s, prefix+"-fix", "intermediate-dev", FixGatesBrief(e.st, s, base, e.head, res, decision), true); err != nil {
			return err
		}
	}
}

// accept records res's failing steps as accepted for s at gate run name.
func (e *engine) accept(s *Story, name string, res GateResult) {
	steps := e.accepted[s.ID]
	if steps == nil {
		steps = map[string]string{}
		e.accepted[s.ID] = steps
	}
	for _, f := range res.Failing {
		steps[f.Line] = name
	}
}

// acceptedAt is the gate runs at which the manager accepted every step res
// fails on, in the order res lists them, or nil when res fails on a step
// not accepted for s (or on none, as a service's infra failure does).
func (e *engine) acceptedAt(s *Story, res GateResult) []string {
	if len(res.Failing) == 0 {
		return nil
	}
	var at []string
	for _, f := range res.Failing {
		name, ok := e.accepted[s.ID][f.Line]
		if !ok {
			return nil
		}
		if !slices.Contains(at, name) {
			at = append(at, name)
		}
	}
	return at
}

// premises sends the plan's flagged premises to premise-check and settles
// their verdicts (spec §5.3, before the first story).
func (e *track) premises() error {
	e.st.Phase = "premises"
	var flagged string
	if !e.recall("premises", &flagged) {
		out, err := e.fx.Premises(e.st.Base)
		if err != nil {
			return err
		}
		if err := e.remember("premises", out); err != nil {
			return err
		}
		flagged = out
	}
	var design []PremiseVerdict
	for _, g := range PremiseGroups(flagged, 3) {
		r, err := e.dispatchReport("premise", "premise-check", PremiseBrief(e.st, g))
		if err != nil {
			return err
		}
		for _, v := range PremiseVerdicts(r.Text) {
			switch {
			case v.Verdict == "HOLDS":
			case v.Class == "COSMETIC":
				if s := e.storyAtLine(v.Line); s != nil {
					s.Row.Corrections = append(s.Row.Corrections, v.Text)
				}
			default:
				design = append(design, v)
			}
		}
	}
	if len(design) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("Premises that need a decision before any story starts:\n")
	for _, v := range design {
		b.WriteString(v.Text + "\n")
	}
	a, err := e.ask(Question{Key: "premise-design", Kind: KindPremiseDesign, Text: b.String()})
	if err != nil {
		return err
	}
	if a == WordStop {
		return errStopped
	}
	for _, v := range design {
		if s := e.storyAtLine(v.Line); s != nil {
			s.Row.Corrections = append(s.Row.Corrections, v.Text+" — the manager: "+a)
		}
	}
	return nil
}

// dispatchReport runs a non-developer agent at prefix until it reports
// done; a BLOCKED is a failure (spec §5.3).
func (e *track) dispatchReport(prefix, role, brief string) (Report, error) {
	next := brief
	for {
		name := e.next(prefix)
		r, err := e.agent(name, role, "", next)
		if err == nil {
			r, err = e.settle(name, r)
		}
		if err != nil || r.done() {
			return r, err
		}
		decision, err := e.failed(name, r)
		if err != nil {
			return r, err
		}
		next = withEarlier(brief, nil, decision)
	}
}

// storyAtLine is the story whose range at the base holds line: premise
// flags are numbered by the plan at the base, whatever Restory moved since.
func (e *engine) storyAtLine(line int) *Story {
	for i := range e.st.Stories {
		s := &e.st.Stories[i]
		if l, end := s.baseRange(); l <= line && line <= end {
			return s
		}
	}
	return nil
}

// conformance is story s's conformance output, memoized under key.
func (e *track) conformance(key string, s *Story, base, head string) (Conformance, error) {
	var c Conformance
	if e.recall("conformance:"+key, &c) {
		return c, nil
	}
	c, err := e.fx.Conformance(s.ID, base, head)
	if err != nil {
		return c, err
	}
	return c, e.remember("conformance:"+key, c)
}

// story runs one story: developer, gates, review, findings, doc pass (spec
// §5.3). Its base is the head the engine last recorded, never a fresh read:
// a commit made outside the run is the tree check's to catch, not the
// story's to absorb. A joined story's developer ran in its lane (parallel
// pairs spec §5.5): dev is its report, and base the head before its pick.
func (e *track) story(s *Story, base string, dev Report, joined bool) error {
	e.cur, e.st.Phase, e.sent = s.ID, "story "+s.ID, nil
	defer func() { e.cur = "" }()
	p := storyPrefix(s.ID)
	s.Row.Base = base
	if !joined {
		s.Row.Status = "developing"
		var err error
		if dev, err = e.build(s, p+"-dev", s.Developer, DeveloperBrief(e.st, s), false); err != nil {
			return err
		}
	}
	if err := e.gateLoop(s, p, base); err != nil {
		return err
	}
	s.Row.Status = "reviewing"
	conf, err := e.conformance(p, s, base, e.head)
	if err != nil {
		return err
	}
	role := "story-reviewer"
	if s.Review != "vouched" || dev.Status == "DONE_WITH_CONCERNS" || len(conf.Markers) > 0 {
		role = "code-quality-reviewer"
	}
	rev, rep, err := e.review(p+"-review", role, ReviewBrief(e.st, s, base, e.head, conf.Path, dev.Path))
	if err != nil {
		return err
	}
	s.Row.Findings = rev.Counts()
	var plan planNotes
	plan.add(rev, rep)
	kept := len(e.st.Kept)
	if err := e.findings(s, p, base, rev, rep, &plan); err != nil {
		return err
	}
	if err := e.planLines(s, p, base, plan, len(e.st.Kept)-kept); err != nil {
		return err
	}
	if e.st.Docs {
		if err := e.docs(s, p, base, dev.Path); err != nil {
			return err
		}
	}
	commits, err := e.commitsAt(p+":done", base)
	if err != nil {
		return err
	}
	s.Row.Head, s.Row.Commits, s.Row.Status = e.head, len(commits), "done"
	return nil
}

// startLane starts s's developer phase in a lane at the main track's head,
// beside partner, on a goroutine of its own (parallel pairs spec §5.3).
func (e *track) startLane(s, partner *Story) *laneRun {
	l := &laneRun{s: s, partner: partner, base: e.head, done: make(chan struct{})}
	l.t = &track{engine: e.engine, head: e.head, cur: s.ID, lane: l}
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.mu.Lock()
		defer e.mu.Unlock()
		defer close(l.done)
		if e.closed {
			return
		}
		dev, err := l.t.laneDev()
		if err != nil {
			e.finish(err)
			return
		}
		l.dev = dev
	}()
	return l
}

// laneDev is a lane track's work: its lane, then its story's developer in it.
func (e *track) laneDev() (Report, error) {
	s := e.lane.s
	var dir string
	if !e.recall("lane:"+s.ID, &dir) {
		name := e.st.ID + "-" + storyPrefix(s.ID)
		if err := e.wait(func() (err error) { dir, err = e.fx.Lane(name, e.lane.base); return err }); err != nil {
			return Report{}, err
		}
		if err := e.remember("lane:"+s.ID, dir); err != nil {
			return Report{}, err
		}
	}
	e.dir = dir
	s.Row.Status, s.Row.Lane = "developing", dir
	return e.build(s, storyPrefix(s.ID)+"-dev", s.Developer, DeveloperBrief(e.st, s)+LaneNote(dir, e.lane.base), false)
}

// join waits for l's developer phase, then picks its lane's commits onto the
// session's worktree (parallel pairs spec §5.5). It is the laned story's
// base (the head before the pick) and its developer's report.
func (e *track) join(l *laneRun) (string, Report, error) {
	s := l.s
	e.cur, e.st.Phase = s.ID, "story "+s.ID
	if err := e.wait(func() error { <-l.done; return nil }); err != nil {
		return "", Report{}, err // a lane that failed has closed the run
	}
	e.st.Notes = append(e.st.Notes, l.notes...)
	for attempt := 1; ; attempt++ {
		key := fmt.Sprintf("%s:%d", s.ID, attempt)
		var c laneCheck
		if !e.recall("lane-check:"+key, &c) {
			var err error
			if c.Head, err = e.fx.Head(l.t.dir); err != nil {
				return "", Report{}, err
			}
			if c.Clean, err = e.fx.Clean(l.t.dir); err != nil {
				return "", Report{}, err
			}
			if c.Clean {
				if c.Commits, err = e.fx.Commits(l.base, c.Head); err != nil {
					return "", Report{}, err
				}
			}
			if err := e.remember("lane-check:"+key, c); err != nil {
				return "", Report{}, err
			}
		}
		problem := "has uncommitted changes"
		if c.Clean {
			r, err := e.pick(s, attempt, c.Commits)
			if err != nil {
				return "", Report{}, err
			}
			if r.Conflict == "" {
				return r.Onto, l.dev, e.landed(l, key, r)
			}
			problem = "didn't apply: " + r.Conflict
		}
		a, err := e.ask(Question{Key: "lane:" + key, Kind: KindLane,
			Text: fmt.Sprintf("The lane of %s at %s (head %s) %s", storyName(s), l.t.dir, short(c.Head), problem)})
		if err != nil {
			return "", Report{}, err
		}
		switch a {
		case WordStop:
			return "", Report{}, errStopped
		case WordRedo:
			e.note("%s: its lane at %s didn't land, and is kept; its developer runs again here", storyName(s), l.t.dir)
			base := e.head
			s.Row.Base, s.Row.Status = base, "developing"
			dev, err := e.build(s, storyPrefix(s.ID)+"-dev", s.Developer, withEarlier(DeveloperBrief(e.st, s), paths(l.dev.Path), RedoDecision(l.t.dir)), false)
			return base, dev, err
		}
		// retry: the manager fixed the lane by hand; look at it again.
	}
}

// pick picks commits onto the session's worktree after its tree check, as
// the join's attempt for s: memoized, and marked before it starts, so a
// crash mid-pick resumes it rather than reading its commits as the tree
// moving.
func (e *track) pick(s *Story, attempt int, commits []string) (pickResult, error) {
	key := fmt.Sprintf("%s:%d", s.ID, attempt)
	name := fmt.Sprintf("%s-%s-pick%d", e.st.ID, storyPrefix(s.ID), attempt)
	var r pickResult
	if e.recall("pick:"+key, &r) {
		e.retake(name)
		return r, nil
	}
	if e.recall("picking:"+key, &r.Onto) {
		e.retake(name)
	} else {
		if err := e.checkTree(name); err != nil {
			return r, err
		}
		e.retake(name)
		r.Onto = e.head
		if err := e.remember("picking:"+key, r.Onto); err != nil {
			return r, err
		}
	}
	picked, err := e.fx.Pick(commits, r.Onto)
	var conflict *PickConflict
	switch {
	case errors.As(err, &conflict):
		r.Conflict = conflict.Error()
	case err != nil:
		return r, err
	default:
		r.Picked = picked
	}
	return r, e.remember("pick:"+key, r)
}

// landed records a pick that applied (spec §5.5): the head after it, the
// lane removed, and the notes.
func (e *track) landed(l *laneRun, key string, r pickResult) error {
	if _, err := e.headAt("picked:" + key); err != nil {
		return err
	}
	var why string
	if !e.recall("dropped:"+l.s.ID, &why) {
		if err := e.fx.DropLane(l.t.dir); err != nil {
			why = err.Error()
		}
		if err := e.remember("dropped:"+l.s.ID, why); err != nil {
			return err
		}
	}
	commits := fmt.Sprintf("%d commits were", len(r.Picked))
	if len(r.Picked) == 1 {
		commits = "1 commit was"
	}
	e.note("%s: its developer ran in a lane beside %s; %s picked onto %s", storyName(l.s), storyName(l.partner), commits, short(r.Onto))
	if why != "" {
		e.note("%s: its lane at %s is left: %s", storyName(l.s), l.t.dir, why)
	}
	return nil
}

// planNotes is the PLAN lines a story's reviews found, and the reports of
// the reviews that found them, which the plan question names.
type planNotes struct {
	lines, reports []string
}

func (n *planNotes) add(rev Review, rep Report) {
	if len(rev.Plan) > 0 {
		n.lines = append(n.lines, rev.Plan...)
		n.reports = append(n.reports, paths(rep.Path)...)
	}
}

// findings routes a review's findings: Minors kept, a blocking review
// fixed and re-reviewed until clean or the manager decides (spec §5.3, 5).
func (e *track) findings(s *Story, p, base string, rev Review, rep Report, plan *planNotes) error {
	if !rev.Blocking() {
		e.st.Kept = append(e.st.Kept, rev.Minors()...)
		return nil
	}
	decision := ""
	if slices.ContainsFunc(blocking(rev.Findings), func(f Finding) bool { return f.Design }) {
		a, err := e.ask(Question{Key: "design:" + s.ID, Kind: KindDesignFinding, Text: "A review of " + storyName(s) + " found a design-level issue; its report has the FINDING lines", Paths: paths(rep.Path)})
		if err != nil {
			return err
		}
		switch a {
		case WordStop:
			return errStopped
		case WordAccept:
			e.note("%s: the manager left the design findings open", storyName(s))
		default:
			decision = a
		}
	}
	e.sendFindings(rev, rep)
	return e.fixRound(s, p, base, rev.Findings, nil, decision, plan)
}

// fixRound fixes findings and re-reviews them until the re-review is clean
// or the manager accepts what it found.
func (e *track) fixRound(s *Story, p, base string, findings []Finding, planFix []string, decision string, plan *planNotes) error {
	for round := 1; ; round++ {
		s.Row.Status = "fixing"
		fixBase := e.head
		if _, err := e.build(s, p+"-fix", "intermediate-dev", FixFindingsBrief(e.st, s, fixBase, e.head, findings, planFix, decision), true); err != nil {
			return err
		}
		if err := e.gateLoop(s, p, base); err != nil {
			return err
		}
		rev, rep, err := e.review(p+"-review", "story-reviewer", ReReviewBrief(e.st, s, fixBase, e.head, findings, planFix))
		if err != nil {
			return err
		}
		plan.add(rev, rep)
		if !rev.Blocking() {
			e.st.Kept = append(e.st.Kept, rev.Minors()...)
			return nil
		}
		a, err := e.ask(Question{Key: fmt.Sprintf("unresolved:%s:%d", p, round), Kind: KindUnresolved, Text: listed("After a fix round, a re-review of "+storyName(s)+" still finds Critical or Important issues:", blocking(rev.Findings)), Paths: paths(rep.Path)})
		if err != nil {
			return err
		}
		switch a {
		case WordStop:
			return errStopped
		case WordAccept:
			e.note("%s: the manager accepted %d findings left open", storyName(s), len(blocking(rev.Findings)))
			return nil
		}
		e.sendFindings(rev, rep)
		findings, planFix, decision = rev.Findings, nil, a
	}
}

// planLines asks the manager once about a story's PLAN lines (planning
// amendment 15), naming the reports they came from and the Minors the
// story's reviews kept, which the final round fixes.
func (e *track) planLines(s *Story, p, base string, notes planNotes, kept int) error {
	plan := notes.lines
	if len(plan) == 0 {
		return nil
	}
	text := "Reviews of " + storyName(s) + " found problems in the plan's own code:\n- " + strings.Join(plan, "\n- ")
	switch {
	case kept == 1:
		text += "\nThey also kept 1 Minor finding, which the final round fixes; their reports have the FINDING lines."
	case kept > 1:
		text += fmt.Sprintf("\nThey also kept %d Minor findings, which the final round fixes; their reports have the FINDING lines.", kept)
	}
	a, err := e.ask(Question{Key: "plan:" + s.ID, Kind: KindUnresolved, Text: text, Paths: notes.reports})
	if err != nil {
		return err
	}
	switch a {
	case WordStop:
		return errStopped
	case WordAccept:
		e.note("%s: the manager accepted the plan problems: %s", storyName(s), strings.Join(plan, "; "))
		return nil
	}
	var later planNotes
	if err := e.fixRound(s, p, base, nil, plan, a, &later); err != nil {
		return err
	}
	for _, l := range later.lines {
		e.note("%s: a re-review's plan problem, not asked again: %s", storyName(s), l)
	}
	return nil
}

// sendFindings keeps rev's Critical and Important findings, which go to a
// fix round, for the story's doc pass.
func (e *track) sendFindings(rev Review, rep Report) {
	for _, f := range blocking(rev.Findings) {
		e.sent = append(e.sent, SentFinding{Finding: f, Report: rep.Path})
	}
}

// docs runs story s's doc pass (the doc pass's spec, §5.3): a doc writer
// over the story as it landed, then docs-check and the story's gates when
// it committed. Its findings join the kept ones, for the final round.
func (e *track) docs(s *Story, p, base, devReport string) error {
	s.Row.Status = "documenting"
	before := e.head
	brief := DocsBrief(e.st, s, base, before, devReport, e.sent)
	next := brief
	var found []Finding
	var earlier []string // every attempt's report, for the next one's brief
	for {
		name := e.next(p + "-docs")
		r, d, err := e.docPass(name, next)
		if err != nil {
			return err
		}
		earlier = append(earlier, paths(r.Path)...)
		if d == nil {
			// Whatever a failed pass did, its commits are its own: take them,
			// so its retry goes on from them rather than parking as
			// tree-moved; docs-check spans every attempt from before.
			if _, err := e.headAt(name); err != nil {
				return err
			}
			decision, err := e.failed(name, r)
			if err != nil {
				return err
			}
			next = withEarlier(brief, earlier, decision)
			continue
		}
		s.Row.Docs, s.Row.Lessons = s.Row.Docs+d.Docs, s.Row.Lessons+d.Lessons
		for _, f := range d.Findings {
			if !slices.Contains(found, f) {
				found = append(found, f)
			}
		}
		head, err := e.headAt(name)
		if err != nil {
			return err
		}
		if head == before {
			e.note("%s: the doc pass changed no docs", storyName(s))
			break
		}
		strays, err := e.docsCheck(name, before, head)
		if err != nil {
			return err
		}
		if len(strays) > 0 {
			a, err := e.ask(Question{Key: "strays:" + name, Kind: KindDocsStrayed, Text: name + " changed paths that aren't doc paths:\n- " + strings.Join(strays, "\n- "), Paths: paths(r.Path)})
			if err != nil {
				return err
			}
			switch a {
			case WordStop:
				return errStopped
			case WordAccept:
				e.note("%s: the manager kept the doc pass's changes to %s", storyName(s), strings.Join(strays, ", "))
			default:
				next = withEarlier(brief, earlier, a) + undoStrays(strays)
				continue
			}
		}
		if err := e.gateLoop(s, p, base); err != nil {
			return err
		}
		break
	}
	if len(found) > 0 {
		e.st.Kept = append(e.st.Kept, found...)
		word := "findings"
		if len(found) == 1 {
			word = "finding"
		}
		e.note("%s: the doc pass kept %d %s for the final round", storyName(s), len(found), word)
	}
	return nil
}

// docPass is doc writer run name's report and its lines, or a nil DocPass
// when the run failed: it didn't report done, or its lines don't parse,
// which r.Why then says.
func (e *track) docPass(name, brief string) (Report, *DocPass, error) {
	r, err := e.agent(name, "doc-writer", "", brief)
	if err == nil {
		r, err = e.settle(name, r)
	}
	if err != nil || !r.done() {
		return r, nil, err
	}
	d, perr := ParseDocs(r.Text)
	if perr != nil {
		r.Why = perr.Error()
		return r, nil, nil
	}
	return r, &d, nil
}

// docsCheck is DocsCheck over base..head, memoized under name.
func (e *track) docsCheck(name, base, head string) ([]string, error) {
	var strays []string
	if e.recall("docs:"+name, &strays) {
		return strays, nil
	}
	strays, err := e.fx.DocsCheck(base, head)
	if err != nil {
		return nil, err
	}
	return strays, e.remember("docs:"+name, strays)
}

// finalRound runs the final gates, the anti-seam review and the final fix
// round (spec §5.3, after the last story).
func (e *track) finalRound() error {
	e.st.Phase = "final"
	s := &e.final
	if err := e.gateLoop(s, "final", e.st.Base); err != nil {
		return err
	}
	rev, rep, err := e.review("final-review", "code-quality-reviewer", AntiSeamBrief(e.st, e.head))
	if err != nil {
		return err
	}
	findings := append(slices.Clone(rev.Findings), e.st.Kept...)
	if len(findings) == 0 {
		return nil
	}
	decision := ""
	if slices.ContainsFunc(blocking(rev.Findings), func(f Finding) bool { return f.Design }) {
		a, err := e.ask(Question{Key: "design:final", Kind: KindDesignFinding, Text: "The whole-implementation review found a design-level issue; its report has the FINDING lines", Paths: paths(rep.Path)})
		if err != nil {
			return err
		}
		switch a {
		case WordStop:
			return errStopped
		case WordAccept:
			e.note("the whole implementation: the manager left the design findings open")
		default:
			decision = a
		}
	}
	for round := 1; ; round++ {
		roundBase := e.head
		for _, group := range fixBatches(findings, finalFixBatch) {
			if _, err := e.build(s, "final-fix", "intermediate-dev", FixFindingsBrief(e.st, s, e.st.Base, e.head, group, nil, decision), true); err != nil {
				return err
			}
		}
		if err := e.gateLoop(s, "final", e.st.Base); err != nil {
			return err
		}
		rev, rep, err = e.review("final-review", "story-reviewer", ReReviewBrief(e.st, s, roundBase, e.head, findings, nil))
		if err != nil {
			return err
		}
		if !rev.Blocking() {
			return nil
		}
		a, err := e.ask(Question{Key: fmt.Sprintf("unresolved:final:%d", round), Kind: KindUnresolved, Text: listed("After the final fix round, its re-review still finds Critical or Important issues:", blocking(rev.Findings)), Paths: paths(rep.Path)})
		if err != nil {
			return err
		}
		switch a {
		case WordStop:
			return errStopped
		case WordAccept:
			e.note("the whole implementation: the manager accepted %d findings left open", len(blocking(rev.Findings)))
			return nil
		}
		findings, decision = rev.Findings, a
	}
}

// listed is text with each of fs on a line of its own, as the question
// asking about them shows them.
func listed(text string, fs []Finding) string {
	var b strings.Builder
	b.WriteString(text)
	for _, f := range fs {
		fmt.Fprintf(&b, "\n- %s", f)
	}
	return b.String()
}

// finalFixBatch is the most findings one final-fix developer takes, unless
// one file holds more. A developer per file started 115 agents in ten runs,
// 28 of which changed nothing, and each start pays for its whole prompt (the
// 2026-11-11 retro).
const finalFixBatch = 8

// fixBatches groups findings for the final fix round's developers: whole
// files, in the order each first appears, as many as keep a batch within
// limit findings. A file with more than limit is a batch of its own.
func fixBatches(fs []Finding, limit int) [][]Finding {
	var out [][]Finding
	var batch []Finding
	for _, group := range byFile(fs) {
		if len(batch) > 0 && len(batch)+len(group) > limit {
			out, batch = append(out, batch), nil
		}
		batch = append(batch, group...)
	}
	if len(batch) > 0 {
		out = append(out, batch)
	}
	return out
}

// byFile groups findings by file, in the order each file first appears.
func byFile(fs []Finding) [][]Finding {
	var order []string
	groups := map[string][]Finding{}
	for _, f := range fs {
		if _, ok := groups[f.File]; !ok {
			order = append(order, f.File)
		}
		groups[f.File] = append(groups[f.File], f)
	}
	out := make([][]Finding, 0, len(order))
	for _, file := range order {
		out = append(out, groups[file])
	}
	return out
}
