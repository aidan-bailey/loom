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
	"github.com/aidan-bailey/loom/attention"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/sched/advisor"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/transcript"
)

// The stop classifier (docs/superpowers/specs/2026-11-07-stop-verdicts-design.md):
// a headless Claude reads each stopped turn's last message and says whether
// the session needs its person, is done, waits on its own work, or stopped.

// attentionDirName is the classifier's folder under the global config dir:
// its call log, and attentionRunDir, the empty folder its calls run in.
const (
	attentionDirName = "attention"
	attentionRunDir  = "run"
)

const (
	attentionSettingsEvery = time.Minute      // how often the block is reread on its own
	attentionPauseAfter    = 3                // failed calls in a row that pause the classifier
	attentionPauseFor      = 10 * time.Minute // how long a pause lasts
)

// stopVerdict is a verdict on the Stop at stop.
type stopVerdict struct {
	stop       time.Time
	kind, gist string
}

// verdictKey names a session's Stop in the call log's cache: the Stop's
// time in nanoseconds, since a time.Time read back from JSON is not equal,
// as a map key, to the one it was written from.
type verdictKey struct {
	id   InstanceID
	stop int64
}

// verdictCall is a Stop waiting for its call.
type verdictCall struct {
	id     InstanceID
	stop   time.Time
	queued time.Time
}

// attentionState is the classifier the model runs.
type attentionState struct {
	globalDir, dir string // dir is globalDir/attention; "" until boot resolved it
	settings       attention.Settings
	settingsErr    error
	readAt         time.Time // when config.json was last read

	verdicts map[InstanceID]stopVerdict // the latest per session; valid while its Stop is current
	pending  []verdictCall              // Stop order, at most one per session
	flight   map[InstanceID]time.Time   // the Stop each session's running call is for

	calls    []attention.CallRecord
	callsErr error                      // the log failed to load: calls are kept in memory only
	cache    map[verdictKey]stopVerdict // the log's answers, so a replayed Stop costs no call

	failures    int // failed calls in a row
	pausedUntil time.Time
	pauseWhy    string

	// program and timeout are the tests' fake claude and a shorter call; ""
	// and 0 keep the session's program and wait_seconds.
	program string
	timeout time.Duration
}

func newAttentionState() *attentionState {
	return &attentionState{verdicts: map[InstanceID]stopVerdict{}, flight: map[InstanceID]time.Time{}, cache: map[verdictKey]stopVerdict{}}
}

// verdictInput is what a call's job needs, captured on the loop.
type verdictInput struct {
	call             verdictCall
	waited           time.Duration
	title, message   string
	jobs             int
	conv, transcript string
	run              advisor.Call // everything but the Message, which the job builds
}

// verdictResult is a call's outcome, delivered on the loop.
type verdictResult struct {
	call          verdictCall
	waited        time.Duration
	model, effort string
	raw           json.RawMessage
	usage         advisor.Usage
	err           error
}

// bootAttention finds the classifier's folder under the global config dir,
// loads its call log and reads its settings. A log that fails to load is
// left as it is, calls are kept in memory only, and a Notice says so. Boot
// calls it after the feedback inbox.
func (m *Model) bootAttention() {
	a := m.attention
	dir, err := config.GetGlobalConfigDir()
	if err != nil {
		log.For("core").Error("attention.no_global_dir", "err", err)
		return
	}
	a.globalDir, a.dir = dir, filepath.Join(dir, attentionDirName)
	calls, err := attention.LoadCalls(a.dir)
	if err != nil {
		a.callsErr = err
		log.For("core").Error("attention.log_load_failed", "dir", a.dir, "err", err)
		m.notifyErr(fmt.Errorf("stop classifier: its call log could not be read: %w; it is left as it is, and calls are kept in memory only", err))
	}
	a.calls = calls
	a.cache = cacheOf(calls)
	m.refreshAttentionSettings(time.Now(), true)
}

// cacheOf is calls' answers by session and Stop: a dropped answer is a real
// answer for its turn too, which a replay of that turn takes back.
func cacheOf(calls []attention.CallRecord) map[verdictKey]stopVerdict {
	out := map[verdictKey]stopVerdict{}
	for _, c := range calls {
		if c.Outcome == attention.OutcomeAnswered || c.Outcome == attention.OutcomeDropped {
			out[verdictKey{InstanceID(c.Session), c.Stop.UnixNano()}] = stopVerdict{stop: c.Stop, kind: c.Verdict, gist: c.Gist}
		}
	}
	return out
}

// refreshAttentionSettings rereads the attention block from the global
// config.json when force is set or attentionSettingsEvery has passed. A
// block that fails to resolve turns the classifier off, with one Notice
// per new error.
func (m *Model) refreshAttentionSettings(now time.Time, force bool) {
	a := m.attention
	if a.globalDir == "" || (!force && now.Sub(a.readAt) < attentionSettingsEvery) {
		return
	}
	a.readAt = now
	block, err := diskConfigBlock[config.AttentionSettings](a.globalDir, "attention")
	if err != nil {
		err = fmt.Errorf("config.json could not be read: %w", err)
	}
	settings := attention.Settings{}
	if err == nil {
		settings, err = attention.Resolve(block)
	}
	if err != nil && errText(err) != errText(a.settingsErr) {
		log.For("core").Warn("attention.settings_failed", "err", err)
		m.causedBy(0, func() {
			m.notifyErr(fmt.Errorf("stop classifier: %w; it is off until the attention block is fixed", err))
		})
	}
	a.settings, a.settingsErr = settings, err
}

// attentionOff is why the classifier can't be called now, "" when it can.
func (m *Model) attentionOff(now time.Time) string {
	a := m.attention
	switch {
	case a.dir == "":
		return "the global config dir could not be resolved"
	case a.settingsErr != nil:
		return a.settingsErr.Error()
	case !a.settings.Enabled:
		return "turned off (attention.enabled)"
	case now.Before(a.pausedUntil):
		return fmt.Sprintf("paused until %s after %d failed calls in a row: %s", a.pausedUntil.Format("15:04"), attentionPauseAfter, a.pauseWhy)
	}
	return ""
}

// queueVerdict queues a call for inst's Stop at stop, replacing one its
// session has waiting, unless the classifier is off; a Stop the call log
// already judged takes that verdict without a call. Then it dispatches.
func (m *Model) queueVerdict(inst *session.Instance, stop time.Time) {
	a := m.attention
	now := time.Now()
	m.refreshAttentionSettings(now, false)
	if stop.IsZero() || m.attentionOff(now) != "" {
		return
	}
	id := m.idOf(inst)
	if v, ok := a.cache[verdictKey{id, stop.UnixNano()}]; ok {
		a.verdicts[id] = v
		return
	}
	a.pending = slices.DeleteFunc(a.pending, func(c verdictCall) bool { return c.id == id })
	a.pending = append(a.pending, verdictCall{id: id, stop: stop, queued: now})
	m.dispatchVerdicts(now)
}

// lastStopOf is inst's LastStop, nothing for a nil inst.
func lastStopOf(inst *session.Instance) (string, time.Time, bool) {
	if inst == nil {
		return "", time.Time{}, false
	}
	return inst.LastStop()
}

// dispatchVerdicts starts the waiting calls, oldest Stop first, while fewer
// than max_parallel run and the session has none running. A call whose
// Stop is no longer its session's current one is forgotten; one that waited
// longer than wait_seconds, or whose account can't be used now, is skipped
// and logged. Nothing waits while the classifier is off.
func (m *Model) dispatchVerdicts(now time.Time) {
	a := m.attention
	if m.attentionOff(now) != "" {
		a.pending = nil
		return
	}
	var keep []verdictCall
	for _, c := range a.pending {
		inst, _ := m.lookup(c.id)
		msg, stop, current := lastStopOf(inst)
		_, running := a.flight[c.id]
		switch {
		case !current || !stop.Equal(c.stop):
			continue
		case now.Sub(c.queued) > a.settings.Wait:
			m.logVerdict(m.verdictRecord(c, inst, now, attention.OutcomeSkipped, "waited longer than wait_seconds for a free call"))
			continue
		case len(a.flight) >= a.settings.MaxParallel || running:
			keep = append(keep, c)
			continue
		}
		in, why := m.verdictInput(inst, c, msg, now)
		if why != "" {
			m.logVerdict(m.verdictRecord(c, inst, now, attention.OutcomeSkipped, why))
			continue
		}
		a.flight[c.id] = c.stop
		m.spawnBackground(func() any { return runVerdict(in) })
	}
	a.pending = keep
}

// verdictInput is what call c's job needs, captured on the loop: the
// session's program and its account's environment, the request's parts and
// the transcript to read the last prompt from. why is set when the call
// can't run now: the account isn't registered, or its 5-hour use is at
// advisorUsageLimit or more.
func (m *Model) verdictInput(inst *session.Instance, c verdictCall, msg string, now time.Time) (verdictInput, string) {
	a := m.attention
	acct := inst.Account()
	env, err := m.accountEnv(acct)
	if err != nil {
		return verdictInput{}, err.Error()
	}
	name := cmp.Or(acct, account.DefaultName)
	if pct := m.usageHigh(name, now); pct > 0 {
		return verdictInput{}, fmt.Sprintf("account %s is at %.0f%% of its 5-hour window", name, pct)
	}
	conv, path := inst.ClaudeSession()
	return verdictInput{
		call: c, waited: now.Sub(c.queued), title: inst.Title, message: msg, jobs: m.jobsInFlight(c.id),
		conv: conv, transcript: path,
		run: advisor.Call{
			Program: cmp.Or(a.program, inst.Program()), Env: env, Dir: filepath.Join(a.dir, attentionRunDir),
			Model: a.settings.Model, Effort: a.settings.Effort, Schema: attention.Schema,
			SystemPrompt: attention.SystemPrompt, NoTools: true, Timeout: cmp.Or(a.timeout, a.settings.Wait),
		},
	}, ""
}

// jobsInFlight counts the work-queue jobs session id owns that have not
// finished.
func (m *Model) jobsInFlight(id InstanceID) int {
	n := 0
	for _, rec := range m.queue.store.Jobs {
		if rec.Owner == uint64(id) && !rec.State.Finished() {
			n++
		}
	}
	return n
}

// runVerdict is a call's job: it reads the last prompt from the transcript,
// makes the empty folder and runs the CLI.
func runVerdict(in verdictInput) verdictResult {
	res := verdictResult{call: in.call, waited: in.waited, model: in.run.Model, effort: in.run.Effort}
	from, prompt := lastPrompt(in.transcript, in.conv)
	msg, err := json.Marshal(attention.NewRequest(in.title, from, prompt, in.message, in.jobs))
	if err != nil {
		res.err = fmt.Errorf("encode the request: %w", err)
		return res
	}
	if err := os.MkdirAll(in.run.Dir, 0o700); err != nil {
		res.err = fmt.Errorf("make the classifier's folder: %w", err)
		return res
	}
	call := in.run
	call.Message = msg
	out, err := advisor.Run(call)
	res.raw, res.usage, res.err = out.Answer, out.Usage, err
	return res
}

// lastPrompt is who sent the conversation's last prompt and its text, from
// the newest page of its transcript; "" for both when it can't be read.
func lastPrompt(path, conv string) (from, text string) {
	if path == "" || conv == "" {
		return "", ""
	}
	page, err := transcript.Read(path, conv, transcript.Query{Conversation: conv})
	if err != nil || len(page.Turns) == 0 {
		return "", ""
	}
	t := page.Turns[len(page.Turns)-1]
	switch t.From {
	case transcript.FromYou:
		from = "owner"
	case transcript.FromPeer:
		from = "session " + t.Name
	case transcript.FromPlugin:
		from = "plugin " + t.Name
	case transcript.FromTask:
		from = "task notification"
	}
	return from, t.Text
}

// deliverVerdict applies a call's answer when its Stop is still its
// session's current one, else logs it dropped; a failure counts toward the
// pause, and the third in a row pauses the classifier with one Notice. It
// logs the call and dispatches the next.
func (m *Model) deliverVerdict(r verdictResult) {
	a := m.attention
	now := time.Now()
	c := r.call
	if f, ok := a.flight[c.id]; ok && f.Equal(c.stop) {
		delete(a.flight, c.id)
	}
	inst, _ := m.lookup(c.id)
	rec := m.verdictRecord(c, inst, now, attention.OutcomeAnswered, "")
	rec.Model, rec.Effort, rec.Usage, rec.WaitedMS = r.model, r.effort, r.usage, r.waited.Milliseconds()
	ans, err := attention.Answer{}, r.err
	if err == nil {
		ans, err = attention.ParseVerdict(r.raw)
	}
	if err != nil {
		rec.Outcome, rec.Error = attention.OutcomeFailed, err.Error()
		a.failures++
		log.For("core").Warn("attention.call_failed", "session", int64(c.id), "failures", a.failures, "err", err)
		if a.failures >= attentionPauseAfter {
			a.pausedUntil, a.pauseWhy, a.failures = now.Add(attentionPauseFor), err.Error(), 0
			m.causedBy(0, func() {
				m.notifyErr(fmt.Errorf("stop classifier: paused for %s after %d failed calls in a row: %w", attentionPauseFor, attentionPauseAfter, err))
			})
		}
	} else {
		a.failures = 0
		rec.Verdict, rec.Gist = ans.Verdict, ans.Gist
		v := stopVerdict{stop: c.stop, kind: ans.Verdict, gist: ans.Gist}
		a.cache[verdictKey{c.id, c.stop.UnixNano()}] = v
		if _, stop, ok := lastStopOf(inst); ok && stop.Equal(c.stop) {
			a.verdicts[c.id] = v
		} else {
			rec.Outcome = attention.OutcomeDropped
		}
	}
	m.logVerdict(rec)
	m.dispatchVerdicts(now)
}

// verdictRecord is the call log's line for call c, ending at now.
func (m *Model) verdictRecord(c verdictCall, inst *session.Instance, now time.Time, outcome, why string) attention.CallRecord {
	rec := attention.CallRecord{At: now, Session: uint64(c.id), Stop: c.stop, Outcome: outcome, Error: why,
		WaitedMS: now.Sub(c.queued).Milliseconds()}
	if inst != nil {
		rec.Title, rec.Account = inst.Title, inst.Account()
	}
	return rec
}

// logVerdict appends rec to the call log, keeping the last
// attention.MaxCalls, and writes calls.jsonl unless it failed to load.
func (m *Model) logVerdict(rec attention.CallRecord) {
	a := m.attention
	a.calls = append(a.calls, rec)
	if n := len(a.calls); n > attention.MaxCalls {
		a.calls = slices.Clone(a.calls[n-attention.MaxCalls:])
	}
	if len(a.cache) > 2*attention.MaxCalls {
		a.cache = cacheOf(a.calls)
	}
	if a.dir == "" || a.callsErr != nil {
		return
	}
	if err := attention.SaveCalls(a.dir, a.calls); err != nil {
		log.For("core").Warn("attention.log_save_failed", "err", err)
	}
}

// verdictOf is inst's verdict as its view shows it: the verdict on its
// current Stop, or pending while a call for that Stop waits or runs.
func (m *Model) verdictOf(inst *session.Instance) (kind, gist string, stop time.Time, pending bool) {
	a := m.attention
	_, stop, ok := inst.LastStop()
	if !ok || stop.IsZero() {
		return "", "", time.Time{}, false
	}
	id := m.idOf(inst)
	if v, ok := a.verdicts[id]; ok && v.stop.Equal(stop) {
		return v.kind, v.gist, stop, false
	}
	if f, ok := a.flight[id]; ok && f.Equal(stop) {
		return "", "", stop, true
	}
	for _, c := range a.pending {
		if c.id == id && c.stop.Equal(stop) {
			return "", "", stop, true
		}
	}
	return "", "", stop, false
}

// tickAttention rereads the attention block when it is due, forgets the
// verdicts of turns no longer current, and dispatches, so a call that
// waited too long for a free slot is skipped.
func (m *Model) tickAttention() {
	a := m.attention
	now := time.Now()
	m.refreshAttentionSettings(now, false)
	for id, v := range a.verdicts {
		inst, _ := m.lookup(id)
		if _, stop, ok := lastStopOf(inst); !ok || !stop.Equal(v.stop) {
			delete(a.verdicts, id)
		}
	}
	if len(a.pending) > 0 {
		m.dispatchVerdicts(now)
	}
}
