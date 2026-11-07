package flowrun

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Report is an agent run's end, as 3a's record holds it.
type Report struct {
	Status   string `json:"status"` // DONE, DONE_WITH_CONCERNS, NEEDS_CONTEXT, BLOCKED, TURN_CAP, failed, cancelled
	Text     string `json:"text,omitempty"`
	Question string `json:"question,omitempty"`
	Path     string `json:"path,omitempty"` // report.md
	Why      string `json:"why,omitempty"`
}

func (r Report) done() bool { return r.Status == "DONE" || r.Status == "DONE_WITH_CONCERNS" }

// GateResult is a gate run's end.
type GateResult struct {
	Ended   string     `json:"ended"` // passed, failed or infra
	Failing []GateStep `json:"failing,omitempty"`
	Table   string     `json:"table,omitempty"`
}

// GateStep is a failing step of a gate run.
type GateStep struct {
	Line    string `json:"line"`
	Verdict string `json:"verdict"`
	Log     string `json:"log,omitempty"`
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
	Agent(name, role, effort, brief string) (Report, error)
	// Resume answers the ended agent run name with text, or follows the
	// answer already queued with that text.
	Resume(name, text string) (Report, error)
	GatesStarted(name string) (bool, error)
	Services(commands []string) error
	// Gates runs a story's gates over commits, or the final gates when
	// commits is nil.
	Gates(name string, commits []string) (GateResult, error)
	Head() (string, error)
	Clean() (bool, error)
	Commits(base, head string) ([]string, error)
	Conformance(story, base, head string) (Conformance, error)
	// Premises is plan_premises.py's output when it flagged anything, else "".
	Premises(rev string) (string, error)
	Save(st *State) error
}

var (
	errParked  = errors.New("the run parked")
	errStopped = errors.New("the manager stopped the run")
)

// finalStory is the pseudo-story the final round's fixes run under
// (planning amendment 14).
const finalStory = "final"

var notName = regexp.MustCompile(`[^A-Za-z0-9._-]`)

type engine struct {
	st      *State
	fx      Effects
	head    string         // the head last recorded
	cur     string         // the story in hand, "" outside one
	seq     map[string]int // attempts at each name prefix, this pass
	resumes map[string]int // resumes of each agent run, this pass
	final   Story
}

// Run drives st until it parks or ends, saving it after every step. It
// returns nil when st has parked or ended, and otherwise the error it
// could not get past (a lost daemon, a file it can't write), with st left
// running for `loom flow run resume`.
func Run(st *State, fx Effects) error {
	e := &engine{st: st, fx: fx, head: st.Base, seq: map[string]int{}, resumes: map[string]int{}, final: Story{ID: finalStory}}
	st.Status, st.Question, st.Why = Running, nil, ""
	st.Phase, st.Notes, st.Kept = "", nil, nil
	for i := range st.Stories {
		st.Stories[i].Row = Row{}
	}
	err := e.run()
	switch {
	case errors.Is(err, errParked):
		return nil
	case errors.Is(err, errStopped):
		return e.end(Stopped)
	case err != nil:
		return err
	}
	return e.end(Done)
}

func (e *engine) end(status string) error {
	e.st.Status, e.st.Question = status, nil
	e.st.End = time.Now().UTC().Format(time.RFC3339)
	e.st.Phase = "ended"
	return e.fx.Save(e.st)
}

func (e *engine) run() error {
	if err := e.premises(); err != nil {
		return err
	}
	for i := range e.st.Stories {
		if err := e.story(&e.st.Stories[i]); err != nil {
			return err
		}
	}
	return e.finalRound()
}

func (e *engine) note(format string, args ...any) {
	e.st.Notes = append(e.st.Notes, fmt.Sprintf(format, args...))
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
func (e *engine) headAt(key string) (string, error) {
	var h string
	if !e.recall("head:"+key, &h) {
		var err error
		if h, err = e.fx.Head(); err != nil {
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
func (e *engine) commitsAt(key, base string) ([]string, error) {
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
func (e *engine) ask(q Question) (string, error) {
	q.Story = e.cur
	if s := e.storyOf(e.cur); s != nil {
		s.Row.Escalated++
	}
	var a string
	if e.recall("q:"+q.Key, &a) {
		var head string
		if e.recall("took:"+q.Key, &head) {
			e.head = head // the manager's own commits, taken with its answer
		}
		return a, nil
	}
	q.At = e.head
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
func (e *engine) checkTree(name string) error {
	head, err := e.fx.Head()
	if err != nil {
		return err
	}
	clean, err := e.fx.Clean()
	if err != nil {
		return err
	}
	if head == e.head && clean {
		return nil
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

// agent is agent run name's report: memoized, followed when it exists, else
// started after the tree check.
func (e *engine) agent(name, role, effort, brief string) (Report, error) {
	var r Report
	if e.recall("agent:"+name, &r) {
		return r, nil
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
	if !slices.Contains(e.st.Agents, name) {
		e.st.Agents = append(e.st.Agents, name)
		if err := e.fx.Save(e.st); err != nil {
			return r, err
		}
	}
	if r, err = e.fx.Agent(name, role, effort, brief); err != nil {
		return r, err
	}
	return r, e.remember("agent:"+name, r)
}

// resume is the report of name resumed with text (planning amendment 13).
func (e *engine) resume(name, text string) (Report, error) {
	e.resumes[name]++
	key := fmt.Sprintf("resume:%s:%d", name, e.resumes[name])
	var r Report
	if e.recall(key, &r) {
		return r, nil
	}
	r, err := e.fx.Resume(name, text)
	if err != nil {
		return r, err
	}
	return r, e.remember(key, r)
}

// settle answers name's NEEDS_CONTEXT questions until it reports otherwise.
func (e *engine) settle(name string, r Report) (Report, error) {
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
func (e *engine) failed(name string, r Report) (string, error) {
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
func (e *engine) review(prefix, role, brief string) (Review, Report, error) {
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
func (e *engine) build(s *Story, prefix, role, brief string, fix bool) (Report, error) {
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
func (e *engine) gates(name string, commits []string) (GateResult, error) {
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
		if err := e.fx.Services(e.st.Blocks.Services); err != nil {
			r = GateResult{Ended: "infra", Table: "FAIL (infra): a service failed: " + err.Error()}
			return r, e.remember("gates:"+name, r)
		}
	}
	if r, err = e.fx.Gates(name, commits); err != nil {
		return r, err
	}
	return r, e.remember("gates:"+name, r)
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
func (e *engine) gateLoop(s *Story, prefix, base string) error {
	s.Row.Status = "gating"
	final := s.ID == finalStory
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
		if res.Ended == "passed" {
			return nil
		}
		failures++
		decision := ""
		if res.Ended == "infra" || failures > 1 {
			a, err := e.ask(Question{Key: "gates:" + name, Kind: KindGateFailure, Text: name + " failed:\n" + res.Table, Paths: logsOf(res)})
			if err != nil {
				return err
			}
			switch a {
			case WordStop:
				return errStopped
			case WordAccept:
				e.note("%s: the manager accepted the failing gates of %s", storyName(s), name)
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

// premises sends the plan's flagged premises to premise-check and settles
// their verdicts (spec §5.3, before the first story).
func (e *engine) premises() error {
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
func (e *engine) dispatchReport(prefix, role, brief string) (Report, error) {
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
func (e *engine) conformance(key string, s *Story, base, head string) (Conformance, error) {
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

// story runs one story: developer, gates, review, findings (spec §5.3).
func (e *engine) story(s *Story) error {
	e.cur, e.st.Phase = s.ID, "story "+s.ID
	defer func() { e.cur = "" }()
	p := storyPrefix(s.ID)
	// The story's base is the head the engine last recorded, never a fresh
	// read: a commit made outside the run is the tree check's to catch, not
	// the story's to absorb.
	base := e.head
	s.Row.Base, s.Row.Status = base, "developing"
	dev, err := e.build(s, p+"-dev", s.Developer, DeveloperBrief(e.st, s), false)
	if err != nil {
		return err
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
	plan := rev.Plan
	if err := e.findings(s, p, base, rev, rep, &plan); err != nil {
		return err
	}
	if err := e.planLines(s, p, base, plan); err != nil {
		return err
	}
	commits, err := e.commitsAt(p+":done", base)
	if err != nil {
		return err
	}
	s.Row.Head, s.Row.Commits, s.Row.Status = e.head, len(commits), "done"
	return nil
}

// findings routes a review's findings: Minors kept, a blocking review
// fixed and re-reviewed until clean or the manager decides (spec §5.3, 5).
func (e *engine) findings(s *Story, p, base string, rev Review, rep Report, plan *[]string) error {
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
	return e.fixRound(s, p, base, rev.Findings, nil, decision, plan)
}

// fixRound fixes findings and re-reviews them until the re-review is clean
// or the manager accepts what it found.
func (e *engine) fixRound(s *Story, p, base string, findings []Finding, planFix []string, decision string, plan *[]string) error {
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
		if plan != nil {
			*plan = append(*plan, rev.Plan...)
		}
		if !rev.Blocking() {
			e.st.Kept = append(e.st.Kept, rev.Minors()...)
			return nil
		}
		a, err := e.ask(Question{Key: fmt.Sprintf("unresolved:%s:%d", p, round), Kind: KindUnresolved, Text: "After a fix round, a re-review of " + storyName(s) + " still finds Critical or Important issues", Paths: paths(rep.Path)})
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
		findings, planFix, decision = rev.Findings, nil, a
	}
}

// planLines asks the manager once about a story's PLAN lines (planning
// amendment 15).
func (e *engine) planLines(s *Story, p, base string, plan []string) error {
	if len(plan) == 0 {
		return nil
	}
	a, err := e.ask(Question{Key: "plan:" + s.ID, Kind: KindUnresolved, Text: "Reviews of " + storyName(s) + " found problems in the plan's own code:\n- " + strings.Join(plan, "\n- ")})
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
	var later []string
	if err := e.fixRound(s, p, base, nil, plan, a, &later); err != nil {
		return err
	}
	for _, l := range later {
		e.note("%s: a re-review's plan problem, not asked again: %s", storyName(s), l)
	}
	return nil
}

// finalRound runs the final gates, the anti-seam review and the final fix
// round (spec §5.3, after the last story).
func (e *engine) finalRound() error {
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
		for _, group := range byFile(findings) {
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
		a, err := e.ask(Question{Key: fmt.Sprintf("unresolved:final:%d", round), Kind: KindUnresolved, Text: "After the final fix round, its re-review still finds Critical or Important issues", Paths: paths(rep.Path)})
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
