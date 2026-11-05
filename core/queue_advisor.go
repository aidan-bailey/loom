package core

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/sched/advisor"
)

// The advisor's limits (smart scheduler spec §5.6).
const (
	advisorRetryAfter   = 10 * time.Minute // a signature whose prior failed isn't asked again before
	advisorPauseAfter   = 3                // failed calls in a row that pause the advisor
	advisorPauseFor     = 30 * time.Minute
	reconcileEvery      = 6 * time.Hour // at most one reconcile per belief
	reconcileStaleAfter = time.Hour     // a reconcile waiting longer is dropped
	advisorUsageLimit   = 90.0          // percent of the 5-hour window
	knownSameRepo       = 20
	knownOtherRepos     = 10
	reconcileTail       = 20
)

// advisorKind is what a call asks for.
type advisorKind int

const (
	advisePrior advisorKind = iota
	adviseReconcile
)

func (k advisorKind) String() string {
	if k == advisePrior {
		return "prior"
	}
	return "reconcile"
}

// advisorCall is a call waiting, or in flight: a prior for signature sig,
// asked by job, or a reconcile of job's surprising run.
type advisorCall struct {
	kind     advisorKind
	sig      sched.SignatureID
	job      sched.JobID
	queued   time.Time
	surprise string
	run      sched.Run
}

// advisorState is the work queue's advisor: its settings as last read, the
// calls waiting, the one in flight, and its pause.
type advisorState struct {
	settings    advisor.Settings
	settingsErr error
	pending     []advisorCall
	flight      *advisorCall
	failures    int // in a row
	pausedUntil time.Time
	pauseWhy    string
	// program and timeout are SetAdvisorForTest's: a fake claude, and a
	// shorter call. "" and 0 keep Model.ClaudeProgram and the default.
	program string
	timeout time.Duration
}

// advisorInput is what a call's job needs: copies only.
type advisorInput struct {
	call        advisorCall
	settings    advisor.Settings
	program     string
	accountEnv  []string
	timeout     time.Duration
	jobDir, cwd string
	job         advisor.Job
	host        advisor.Host
	known       []advisor.Known
	belief      advisor.Known
	runs        []sched.Run
	prior       *sched.Prior
}

// advisorResult is a call's outcome, delivered on the loop.
type advisorResult struct {
	call          advisorCall
	model, effort string
	raw           json.RawMessage
	err           error
}

// advisorProgram is the Claude CLI the advisor runs.
func (m *Model) advisorProgram() string {
	if p := m.queue.advisor.program; p != "" {
		return p
	}
	return m.ClaudeProgram()
}

// advisorEnv is the environment entries of the account the advisor runs
// as: none for the default account.
func (m *Model) advisorEnv() ([]string, error) {
	name := m.queue.advisor.settings.Account
	if name == "" || name == account.DefaultName {
		return nil, nil
	}
	if m.accounts == nil {
		return nil, fmt.Errorf("account %q isn't registered", name)
	}
	env, err := m.accounts.Env(name)
	if err != nil {
		return nil, fmt.Errorf("account %q isn't registered", name)
	}
	return env, nil
}

// advisorOff is why the advisor can't be called now, "" when it can.
func (m *Model) advisorOff(now time.Time) string {
	q := m.queue
	a := &q.advisor
	switch {
	case q.beliefsErr != nil:
		return "learning is off"
	case a.settingsErr != nil:
		return a.settingsErr.Error()
	case !a.settings.Enabled:
		return "turned off (scheduler.agent.enabled)"
	case m.advisorProgram() == "":
		return "no Claude program is configured"
	}
	if _, err := m.advisorEnv(); err != nil {
		return err.Error()
	}
	if now.Before(a.pausedUntil) {
		return fmt.Sprintf("paused until %s after %d failed calls in a row: %s", a.pausedUntil.Format("15:04"), advisorPauseAfter, a.pauseWhy)
	}
	return ""
}

// advisorUsageHigh is the advisor's account's use of its 5-hour window when
// a fresh sample puts it at advisorUsageLimit or more, else 0.
func (m *Model) advisorUsageHigh(now time.Time) float64 {
	name := cmp.Or(m.queue.advisor.settings.Account, account.DefaultName)
	u := m.usage[name].last
	if u.At.IsZero() || now.Sub(u.At) > UsageStaleAfter || u.FiveHour == nil || u.FiveHour.Pct < advisorUsageLimit {
		return 0
	}
	return u.FiveHour.Pct
}

// askForPrior reports whether a job asking for asked, of belief b (nil for
// none yet), waits for a prior: the advisor is available, a part needs one,
// and the signature's last prior didn't fail within advisorRetryAfter.
func (m *Model) askForPrior(asked sched.Claim, b *sched.Belief, now time.Time) bool {
	if m.advisorOff(now) != "" || !sched.NeedsPrior(asked, b) {
		return false
	}
	return b == nil || b.Advisor.FailedAt.IsZero() || now.Sub(b.Advisor.FailedAt) >= advisorRetryAfter
}

// queueAdvice queues call c, unless a prior for the same signature is
// already waiting or in flight, and dispatches.
func (m *Model) queueAdvice(c advisorCall) {
	a := &m.queue.advisor
	if c.kind == advisePrior {
		same := func(p advisorCall) bool { return p.kind == advisePrior && p.sig == c.sig }
		if slices.ContainsFunc(a.pending, same) || (a.flight != nil && same(*a.flight)) {
			return
		}
	}
	a.pending = append(a.pending, c)
	m.dispatchAdvisor()
}

// dispatchAdvisor starts the next waiting call when none is in flight:
// priors first, each kind in the order queued. A call the advisor can't
// make now (off, paused, its account near its limit, its job or belief
// gone) is dropped, and the jobs waiting on a dropped prior are sized
// without it.
func (m *Model) dispatchAdvisor() {
	a := &m.queue.advisor
	now := time.Now()
	for a.flight == nil && len(a.pending) > 0 {
		i := max(0, slices.IndexFunc(a.pending, func(c advisorCall) bool { return c.kind == advisePrior }))
		c := a.pending[i]
		a.pending = slices.Delete(a.pending, i, i+1)
		if c.kind == adviseReconcile && now.Sub(c.queued) >= reconcileStaleAfter {
			continue
		}
		why := m.advisorOff(now)
		if pct := m.advisorUsageHigh(now); why == "" && pct > 0 {
			why = fmt.Sprintf("the agent's account is at %.0f%% of its 5-hour limit", pct)
		}
		var in advisorInput
		if why == "" {
			var ok bool
			if in, ok = m.advisorInputFor(c); !ok {
				why = "its job or belief is gone"
			}
		}
		if why != "" {
			log.For("core").Info("queue.advisor_skipped", "kind", c.kind.String(), "sig", string(c.sig), "why", why)
			if c.kind == advisePrior {
				m.releaseAsking(c.sig, why)
			}
			continue
		}
		flight := c
		a.flight = &flight
		m.spawnBackground(runAdvisor(in))
	}
}

// advisorInputFor captures what call c's job needs.
func (m *Model) advisorInputFor(c advisorCall) (advisorInput, bool) {
	q := m.queue
	b := q.beliefOf(c.sig)
	i := q.index(c.job)
	if b == nil || i < 0 {
		return advisorInput{}, false
	}
	rec := q.store.Jobs[i]
	env, _ := m.advisorEnv()
	in := advisorInput{
		call: c, settings: q.advisor.settings, program: m.advisorProgram(), accountEnv: env, timeout: q.advisor.timeout,
		jobDir: q.jobDir(rec.ID), cwd: rec.Cwd,
		job: advisor.Job{
			Argv: slices.Clone(rec.Argv), Command: slices.Clone(b.Command), CommandEnv: slices.Clone(b.Env),
			Description: rec.Description, Repo: sched.RepoName(b.Repo), Folder: b.Folder,
		},
		host: advisor.Host{Cores: q.host.Cores, RAMGB: int(q.host.RAMBytes >> 30), Caps: q.policy.Caps, DefaultClaim: q.policy.DefaultClaim},
	}
	switch c.kind {
	case advisePrior:
		in.known = m.knownBeliefs(b)
	case adviseReconcile:
		in.belief, in.runs = m.known(*b), slices.Clone(b.Runs)
		if b.Prior != nil {
			p := *b.Prior
			in.prior = &p
		}
	}
	return in, true
}

// knownBeliefs are the beliefs a prior for self is told of: up to
// knownSameRepo of its repo and knownOtherRepos of others running the same
// program, most recently seen first.
func (m *Model) knownBeliefs(self *sched.Belief) []advisor.Known {
	var same, other []sched.Belief
	for _, b := range m.queue.beliefs.Beliefs {
		switch {
		case b.ID == self.ID:
		case b.Repo == self.Repo:
			same = append(same, b)
		case len(b.Command) > 0 && len(self.Command) > 0 && b.Command[0] == self.Command[0]:
			other = append(other, b)
		}
	}
	recent := func(x, y sched.Belief) int { return y.LastSeen.Compare(x.LastSeen) }
	slices.SortFunc(same, recent)
	slices.SortFunc(other, recent)
	var out []advisor.Known
	for _, b := range same[:min(len(same), knownSameRepo)] {
		out = append(out, m.known(b))
	}
	for _, b := range other[:min(len(other), knownOtherRepos)] {
		out = append(out, m.known(b))
	}
	return out
}

// known is b as the advisor is told of it.
func (m *Model) known(b sched.Belief) advisor.Known {
	q := m.queue
	k := advisor.Known{ID: b.ID, Repo: sched.RepoName(b.Repo), Folder: b.Folder, Command: slices.Clone(b.Command), Runs: len(b.Runs)}
	if q.policy.Caps.CPUs > 0 {
		s := sched.Size(sched.Claim{}, &b, q.policy)
		k.Claim, k.Sources = s.Claim, s.Sources
	}
	if u, p90, n, _ := sched.Usual(&b); n > 0 {
		k.UsualSeconds, k.P90Seconds = int(u.Seconds()), int(p90.Seconds())
	}
	for _, n := range b.Notes {
		k.Notes = append(k.Notes, n.Text)
	}
	return k
}

// runAdvisor is a call's job: it builds the request (reading the job's
// spec.json for its environment, filtered, and a reconcile's log tail) and
// runs the CLI in the job's folder.
func runAdvisor(in advisorInput) Job {
	return func() any {
		res := advisorResult{call: in.call, model: in.settings.Model, effort: in.settings.Effort}
		msg, schema, err := in.message()
		if err != nil {
			res.err = fmt.Errorf("build the request: %w", err)
			return res
		}
		if _, err := os.Stat(in.cwd); err != nil {
			res.err = fmt.Errorf("the job's folder can't be read: %w", err)
			return res
		}
		res.raw, res.err = advisor.Run(advisor.Call{
			Program: in.program, Env: in.accountEnv, Dir: in.cwd, Model: in.settings.Model, Effort: in.settings.Effort,
			Schema: schema, Message: msg, Timeout: in.timeout,
		})
		return res
	}
}

func (in advisorInput) message() ([]byte, string, error) {
	job := in.job
	if spec, err := sched.ReadSpec(in.jobDir); err == nil {
		job.Environment = advisor.AllowedEnv(spec.Env)
	}
	if in.call.kind == advisePrior {
		data, err := json.Marshal(advisor.PriorRequest{Task: "prior", Job: job, Host: in.host, Beliefs: in.known})
		return data, advisor.PriorSchema, err
	}
	_, tail, _ := readLogTail(filepath.Join(in.jobDir, sched.LogFile), reconcileTail)
	data, err := json.Marshal(advisor.ReconcileRequest{
		Task: "reconcile", Job: job, Host: in.host, Belief: in.belief, Runs: in.runs, Prior: in.prior,
		Run: in.call.run, Surprise: in.call.surprise, LogTail: tail,
	})
	return data, advisor.NoteSchema, err
}

// deliverAdvisor stores a call's answer: a prior on its belief, or a note.
// A failed call is logged, recorded on a prior's belief (no retry for
// advisorRetryAfter), and counted: advisorPauseAfter in a row pause the
// advisor. The jobs waiting on a prior are sized, with or without it, and
// the next call starts.
func (m *Model) deliverAdvisor(r advisorResult) {
	q := m.queue
	a := &q.advisor
	a.flight = nil
	now := time.Now()
	b := q.beliefOf(r.call.sig)
	if b == nil {
		if r.call.kind == advisePrior {
			m.releaseAsking(r.call.sig, "its belief was forgotten")
		}
		m.afterAdvice()
		return
	}
	err := r.err
	if err == nil {
		switch r.call.kind {
		case advisePrior:
			var ans advisor.PriorAnswer
			known := func(id sched.SignatureID) bool { return id != b.ID && q.beliefOf(id) != nil }
			if ans, err = advisor.ParsePrior(r.raw, q.policy.DefaultClaim.CPUs, q.policy.Caps.MemoryGB, known); err == nil {
				b.Prior = m.priorOf(ans, r, now)
				b.Advisor.FailedAt, b.Advisor.FailedWhy = time.Time{}, ""
			}
		case adviseReconcile:
			var note string
			if note, err = advisor.ParseNote(r.raw); err == nil {
				b.AddNote(sched.Note{At: now, Job: r.call.job, Text: note})
			}
		}
	}
	why := ""
	if err != nil {
		log.For("core").Warn("queue.advisor_failed", "kind", r.call.kind.String(), "sig", string(r.call.sig), "err", err)
		why = "the agent failed: " + err.Error()
		if r.call.kind == advisePrior {
			b.Advisor.FailedAt, b.Advisor.FailedWhy = now, err.Error()
		}
		if a.failures++; a.failures >= advisorPauseAfter {
			a.pausedUntil, a.pauseWhy, a.failures = now.Add(advisorPauseFor), err.Error(), 0
		}
	} else {
		a.failures = 0
	}
	m.saveBeliefsQuiet("advisor")
	if r.call.kind == advisePrior {
		m.releaseAsking(r.call.sig, why)
	}
	m.afterAdvice()
}

// afterAdvice runs an admission pass, which sizes the jobs a call released,
// and starts the next call.
func (m *Model) afterAdvice() {
	m.queue.moved()
	m.admitJobs()
	m.dispatchAdvisor()
}

// priorOf is the prior an answer gives: with like, that belief's learned
// parts and usual duration in place of the answer's.
func (m *Model) priorOf(ans advisor.PriorAnswer, r advisorResult, now time.Time) *sched.Prior {
	q := m.queue
	p := &sched.Prior{
		CPUs: ans.CPUs, MemoryGB: ans.MemoryGB, IO: ans.IO, DurationSeconds: ans.DurationSeconds,
		Like: ans.Like, Note: ans.Note, Model: r.model, Effort: r.effort, At: now,
	}
	if like := q.beliefOf(ans.Like); like != nil && q.policy.Caps.CPUs > 0 {
		s := sched.Size(sched.Claim{}, like, q.policy)
		if s.Sources.CPUs == sched.SourceLearned {
			p.CPUs = s.Claim.CPUs
		}
		if s.Sources.Memory == sched.SourceLearned {
			p.MemoryGB = s.Claim.MemoryGB
		}
		if s.Sources.IO == sched.SourceLearned {
			p.IO = s.Claim.IO
		}
		if u, _, n, _ := sched.Usual(like); n > 0 {
			p.DurationSeconds = max(1, int(u.Seconds()))
		}
	}
	return p
}

// releaseAsking ends the wait of every queued job of signature sig asking
// for a prior; why says why it was sized without one ("" when the prior
// came).
func (m *Model) releaseAsking(sig sched.SignatureID, why string) {
	q := m.queue
	for i := range q.store.Jobs {
		rec := &q.store.Jobs[i]
		if rec.State == sched.Queued && rec.Sizing != "" && rec.Signature == sig {
			rec.Sizing, rec.AgentWhy = "", why
			q.moved()
		}
	}
}

// releaseOverdueAsks sizes without its prior every job that has waited
// wait_seconds since its submit (spec §5.4).
func (m *Model) releaseOverdueAsks(now time.Time) {
	q := m.queue
	wait := cmp.Or(q.advisor.settings.Wait, advisor.DefaultWait)
	for i := range q.store.Jobs {
		rec := &q.store.Jobs[i]
		if rec.State == sched.Queued && rec.Sizing != "" && now.Sub(rec.Submitted) >= wait {
			rec.Sizing, rec.AgentWhy = "", fmt.Sprintf("the agent didn't answer in %ds", int(wait.Seconds()))
			q.moved()
		}
	}
}

// resumeAsks asks again for the jobs a previous daemon left asking, or
// sizes them without a prior when the advisor is unavailable. Boot runs it
// once the queue is loaded.
func (m *Model) resumeAsks() {
	q := m.queue
	now := time.Now()
	for i := range q.store.Jobs {
		rec := &q.store.Jobs[i]
		if rec.State != sched.Queued || rec.Sizing == "" {
			continue
		}
		if why := m.advisorOff(now); why != "" {
			rec.Sizing, rec.AgentWhy = "", why
			q.moved()
			continue
		}
		m.queueAdvice(advisorCall{kind: advisePrior, sig: rec.Signature, job: rec.ID, queued: now})
	}
}

// agentView is the advisor as a client sees it.
func (m *Model) agentView(now time.Time) AgentView {
	a := m.queue.advisor
	why := m.advisorOff(now)
	return AgentView{On: why == "", Model: a.settings.Model, Effort: a.settings.Effort, Wait: a.settings.Wait, Why: why}
}
