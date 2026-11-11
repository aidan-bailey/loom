package core

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/internal/flowrun"
	"github.com/aidan-bailey/loom/internal/flowview"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/session"
)

// The flow visualisation (spec
// docs/superpowers/specs/2026-11-09-flow-visualisation-design.md). The
// model looks for a session's flows (flowview.Find) on a job: once at first
// sight, whenever a job of one of its runs starts or ends (flowJobKeys
// moves), and when a hook scan reads a Stop. It keeps the summaries per
// instance (Model.flows), and viewOf publishes them as InstanceView.Flows.
// ReadFlow, ReadFlows and ReadFlowDoc answer the phone's flow screens by
// Reply; ReadFlow prices the run with `loom flow costs run --json`.

// The reader and the pricer; a test swaps them.
var (
	findFlows    = flowview.Find
	detailFlow   = flowview.Detail
	readFlowDoc  = flowview.ReadDoc
	flowHistory  = flowview.History
	flowRepoKey  = flowview.RepoKey
	runFlowCosts = loomFlowCosts
	flowNow      = time.Now
)

// flowLooksOn says the model looks at sessions' flows on its own (the
// tick's and a Stop's looks). core's tests turn it off, as they do the
// confiner's probe: a look is a background job in every count they make;
// the flow tests turn it back on.
var flowLooksOn = true

// flowPriceEvery is how often ReadFlow prices a run again while one of its
// agents runs and nothing else moved.
const flowPriceEvery = 30 * time.Second

// flowCostsTimeout bounds one run of the pricer.
const flowCostsTimeout = 60 * time.Second

// flowState is what the model keeps of one instance's flows.
type flowState struct {
	sums    []flowview.Summary
	looking bool   // a look is in flight
	dirty   bool   // a trigger came during it
	looked  bool   // a look has been delivered
	jobsKey string // flowJobKeys' entry at the last look
	priced  map[string]pricedRun
	design  map[string]float64 // by spec, once its plan is committed
}

// pricedRun is a run's last pricing: the key it was priced at (each agent
// run with its status), when, and what the pricer said.
type pricedRun struct {
	key string
	at  time.Time
	p   *flowview.Priced
	err string
}

// flowsOf is id's flow state, made on first use.
func (m *Model) flowsOf(id InstanceID) *flowState {
	if m.flows == nil {
		m.flows = map[InstanceID]*flowState{}
	}
	fs, ok := m.flows[id]
	if !ok {
		fs = &flowState{priced: map[string]pricedRun{}, design: map[string]float64{}}
		m.flows[id] = fs
	}
	return fs
}

// flowRecordsDir is where the run records live: <global dir>/flow/records.
func flowRecordsDir() string {
	dir, err := config.GetGlobalConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "flow", "records")
}

// flowLookable reports whether inst may have flows to look for: a session
// with a worktree and a base, not a workspace terminal, not a placeholder
// or a row being started or torn down.
func flowLookable(inst *session.Instance) (worktree, base string, ok bool) {
	if inst.IsWorkspaceTerminal || !inst.Started() {
		return "", "", false
	}
	switch inst.GetStatus() {
	case session.Recoverable, session.Loading, session.Deleting:
		return "", "", false
	}
	worktree = inst.GetWorktreePath()
	gw, err := inst.GetGitWorktree()
	if worktree == "" || err != nil || gw == nil || gw.GetBaseCommitSHA() == "" {
		return "", "", false
	}
	return worktree, gw.GetBaseCommitSHA(), true
}

// flowLookResult is a look's result.
type flowLookResult struct {
	inst *session.Instance
	sums []flowview.Summary
	err  error
}

// wantFlowLook spawns a look at inst's flows, or marks one wanted when one
// is in flight (a background job: it serves no request).
func (m *Model) wantFlowLook(inst *session.Instance) {
	if !flowLooksOn {
		return
	}
	worktree, base, ok := flowLookable(inst)
	if !ok {
		return
	}
	fs := m.flowsOf(m.idOf(inst))
	if fs.looking {
		fs.dirty = true
		return
	}
	fs.looking = true
	records := flowRecordsDir()
	m.spawnBackground(func() any {
		sums, err := findFlows(worktree, base, records)
		return flowLookResult{inst: inst, sums: sums, err: err}
	})
}

// deliverFlowLook stores a look's summaries, keeping the last ones when it
// failed, and runs one more when a trigger came during it.
func (m *Model) deliverFlowLook(r flowLookResult) {
	id := m.idOf(r.inst)
	if id == 0 {
		return
	}
	fs := m.flowsOf(id)
	fs.looking, fs.looked = false, true
	if r.err != nil {
		log.DebugKV("core.flows.find_failed", "instance", r.inst.Title, "err", r.err.Error())
	} else {
		fs.sums = r.sums
	}
	if fs.dirty {
		fs.dirty = false
		m.wantFlowLook(r.inst)
	}
}

// flowJobKeys is, per owner, its run jobs and the jobs of runs with their
// states, in the queue's order ("12:running,13:done"): a change in an
// owner's entry is a step's start or end.
func (m *Model) flowJobKeys() map[InstanceID]string {
	keys := map[InstanceID]string{}
	q := m.queue
	if q == nil || !q.ready() {
		return keys
	}
	for _, rec := range q.store.Jobs {
		if rec.Owner == 0 || (rec.Kind != sched.KindRun && rec.Run == 0) {
			continue
		}
		owner := InstanceID(rec.Owner)
		if keys[owner] != "" {
			keys[owner] += ","
		}
		keys[owner] += strconv.FormatInt(int64(rec.ID), 10) + ":" + string(rec.State)
	}
	return keys
}

// maybeFlowLooks runs on the tick: a look for each instance never looked
// at, or whose run jobs moved since its last look.
func (m *Model) maybeFlowLooks() {
	keys := m.flowJobKeys()
	for _, inst := range m.allInstances() {
		if _, _, ok := flowLookable(inst); !ok {
			continue
		}
		id := m.idOf(inst)
		fs := m.flowsOf(id)
		if (!fs.looked && !fs.looking) || keys[id] != fs.jobsKey {
			fs.jobsKey = keys[id]
			m.wantFlowLook(inst)
		}
	}
}

// flowSummaries is id's flows for its view: a copy of the last look's,
// with what only the model knows (Lost, Spent).
func (m *Model) flowSummaries(id InstanceID) []FlowSummary {
	fs := m.flows[id]
	if fs == nil || len(fs.sums) == 0 {
		return nil
	}
	out := make([]FlowSummary, 0, len(fs.sums))
	for _, s := range fs.sums {
		f := FlowSummary{Summary: s, Lost: m.engineLost(s)}
		if pr, ok := fs.priced[s.Run]; ok && s.Run != "" && pr.p != nil && pr.err == "" {
			f.Spent, f.Priced = pr.p.Total.USD, true
		}
		out = append(out, f)
	}
	return out
}

// engineLost reports whether s's run says it runs while its engine job has
// ended or left the queue, as `loom flow run show` judges it. A queue that
// isn't loaded has no opinion.
func (m *Model) engineLost(s flowview.Summary) bool {
	if s.Status != flowrun.Running || s.Engine == 0 {
		return false
	}
	q := m.queue
	if q == nil || !q.ready() {
		return false
	}
	i := q.index(sched.JobID(s.Engine))
	return i < 0 || q.store.Jobs[i].State.Finished()
}

// flowJobFacts is what ReadFlow's job takes of the queue: each of the
// session's jobs' times and wait, and each running agent job's log.
func (m *Model) flowJobFacts(id InstanceID) (map[sched.JobID]JobTimes, map[sched.JobID]string) {
	times, logs := map[sched.JobID]JobTimes{}, map[sched.JobID]string{}
	q := m.queue
	if q == nil || !q.ready() {
		return times, logs
	}
	for _, rec := range q.store.Jobs {
		if InstanceID(rec.Owner) != id {
			continue
		}
		times[rec.ID] = JobTimes{Started: rec.Started, Ended: rec.Ended, Waiting: q.waitingOf(rec)}
		if rec.Kind == sched.KindAgent && rec.State == sched.Running {
			logs[rec.ID] = filepath.Join(q.jobDir(rec.ID), sched.LogFile)
		}
	}
	return times, logs
}

// flowReadResult is ReadFlow's job result.
type flowReadResult struct {
	id     InstanceID
	view   FlowView
	run    string
	priced *pricedRun // a new pricing to keep; nil when the cache answered
	spec   string
	design *float64 // a design figure to keep, nil for none
	err    error
}

// ReadFlow reads the board of the flow whose plan is plan in session id's
// worktree, with its costs, in a job, and answers with the Reply's Flow.
// The run is priced again only when its agents moved since the last
// pricing, or every flowPriceEvery while one runs.
func (m *Model) ReadFlow(id InstanceID, plan string, req ReqID) {
	inst, _, ok := m.admit(opReadFlow, id, req)
	if !ok {
		return
	}
	worktree, base, lookable := flowLookable(inst)
	if !lookable {
		m.refuse(req, id, fmt.Errorf("%s %s: the session has no worktree to read", opReadFlow, inst.Title))
		return
	}
	fs := m.flowsOf(id)
	env, envErr := m.accountEnv(inst.Account())
	times, logs := m.flowJobFacts(id)
	in := flowReadInput{
		worktree: worktree, base: base, records: flowRecordsDir(), plan: plan, env: env, envErr: envErr,
		priced: clonePriced(fs.priced), design: cloneDesign(fs.design), times: times, logs: logs, now: flowNow(),
	}
	m.spawn(m.track(req, id, func() any { return readFlowJob(id, in) }))
}

// flowReadInput is ReadFlow's job's inputs, captured on the loop.
type flowReadInput struct {
	worktree, base, records, plan string
	env                           []string
	envErr                        error
	priced                        map[string]pricedRun
	design                        map[string]float64
	times                         map[sched.JobID]JobTimes
	logs                          map[sched.JobID]string
	now                           time.Time
}

func clonePriced(in map[string]pricedRun) map[string]pricedRun {
	out := make(map[string]pricedRun, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneDesign(in map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// readFlowJob is ReadFlow's job: the disk, git and the pricer only.
func readFlowJob(id InstanceID, in flowReadInput) any {
	res := flowReadResult{id: id}
	d, err := detailFlow(in.worktree, in.base, in.records, in.plan)
	if err != nil {
		res.err = fmt.Errorf("%s: %w", opReadFlow, err)
		return res
	}
	key, _ := flowRepoKey(in.worktree)
	history, err := flowHistory(in.records)
	if err != nil {
		log.DebugKV("core.flows.history_failed", "err", err.Error())
	}
	var p *flowview.Priced
	perr := ""
	if d.Run != "" {
		pk, running := priceKey(d)
		cached, ok := in.priced[d.Run]
		if ok && cached.key == pk && !(running && in.now.Sub(cached.at) >= flowPriceEvery) {
			p, perr = cached.p, cached.err
		} else {
			p, perr = priceRun(in, d)
			res.run, res.priced = d.Run, &pricedRun{key: pk, at: in.now, p: p, err: perr}
		}
	}
	var design *float64
	if d.Spec != "" {
		if v, ok := in.design[d.Spec]; ok {
			design = &v
		} else if v, ok := priceDesign(in, d); ok {
			design = &v
			if designDone(d) {
				res.spec, res.design = d.Spec, &v
			}
		}
	}
	res.view = FlowView{Detail: d, Costs: flowview.CostsOf(d, p, perr, design, history, key), Times: stepTimes(d, in.times)}
	return res
}

// priceKey is the run's agent runs with their statuses, which a pricing is
// good for, and whether any of them runs.
func priceKey(d flowview.FlowDetail) (string, bool) {
	var parts []string
	running := false
	for _, s := range allSteps(d) {
		if s.Role == "" && s.Kind == flowrun.KindGates {
			continue
		}
		parts = append(parts, s.Name+"="+s.Status)
		if s.Status == flowrun.AgentRunning {
			running = true
		}
	}
	slices.Sort(parts)
	return strings.Join(parts, ","), running
}

// allSteps is every step of d's rows.
func allSteps(d flowview.FlowDetail) []flowview.StepView {
	var out []flowview.StepView
	if d.Premise != nil {
		out = append(out, d.Premise.Steps...)
	}
	for _, s := range d.Stories {
		out = append(out, s.Steps...)
	}
	if d.Final != nil {
		out = append(out, d.Final.Steps...)
	}
	return out
}

// stepTimes is the queue's times of d's steps' jobs.
func stepTimes(d flowview.FlowDetail, times map[sched.JobID]JobTimes) map[sched.JobID]JobTimes {
	out := map[sched.JobID]JobTimes{}
	for _, s := range allSteps(d) {
		for _, j := range s.Jobs {
			if t, ok := times[j]; ok {
				out[j] = t
			}
		}
	}
	return out
}

// priceRun runs `loom flow costs run <run> --json` in the worktree under
// the session's account, naming each running agent's session that its job
// log gives and its record doesn't yet; the error is its first stderr line.
func priceRun(in flowReadInput, d flowview.FlowDetail) (*flowview.Priced, string) {
	if in.envErr != nil {
		return nil, "the session's account: " + in.envErr.Error()
	}
	args := []string{"run", d.Run, "--json"}
	for _, s := range allSteps(d) {
		if s.Session != "" || s.Status != flowrun.AgentRunning || len(s.Jobs) == 0 {
			continue
		}
		if path, ok := in.logs[s.Jobs[len(s.Jobs)-1]]; ok {
			if sid := initSession(path); sid != "" {
				args = append(args, "--session", s.Name+"="+sid)
			}
		}
	}
	out, err := runFlowCosts(in.worktree, in.env, args...)
	if err != nil {
		return nil, err.Error()
	}
	var p flowview.Priced
	if err := json.Unmarshal(out, &p); err != nil {
		return nil, "the pricer's output: " + err.Error()
	}
	return &p, ""
}

// priceDesign runs `loom flow costs design <spec> --plan <plan> --json`.
func priceDesign(in flowReadInput, d flowview.FlowDetail) (float64, bool) {
	if in.envErr != nil {
		return 0, false
	}
	args := []string{"design", d.Spec, "--json"}
	if d.Plan != d.Spec {
		args = append(args, "--plan", d.Plan)
	}
	out, err := runFlowCosts(in.worktree, in.env, args...)
	if err != nil {
		log.DebugKV("core.flows.design_price_failed", "spec", d.Spec, "err", err.Error())
		return 0, false
	}
	var v struct {
		Sessions []json.RawMessage `json:"sessions"`
		Total    struct {
			USD float64 `json:"usd"`
		} `json:"total"`
	}
	if json.Unmarshal(out, &v) != nil || len(v.Sessions) == 0 {
		return 0, false
	}
	return v.Total.USD, true
}

// designDone reports whether d's design stage is done: its figure can't
// change after.
func designDone(d flowview.FlowDetail) bool {
	for _, s := range d.Stages {
		if s.Name == flowview.StageDesign {
			return s.State == flowview.StateDone
		}
	}
	return false
}

// initSession is the session id an agent job's output.log names in its
// system/init line, "" when it names none yet; it reads at most 64 KiB.
func initSession(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(io.LimitReader(f, 64<<10))
	sc.Buffer(make([]byte, 64<<10), 64<<10)
	for sc.Scan() {
		var ev struct {
			Type      string `json:"type"`
			Subtype   string `json:"subtype"`
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) == nil && ev.Type == "system" && ev.Subtype == "init" {
			return ev.SessionID
		}
	}
	return ""
}

// loomFlowCosts runs this binary's `flow costs <args>` in dir with env
// added to the daemon's environment, and returns its stdout; an error
// carries its first stderr line.
func loomFlowCosts(dir string, env []string, args ...string) ([]byte, error) {
	bin, err := os.Executable()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), flowCostsTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, append([]string{"flow", "costs"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		line, _, _ := strings.Cut(strings.TrimSpace(stderr.String()), "\n")
		if line == "" {
			line = err.Error()
		}
		return nil, errors.New(line)
	}
	return stdout.Bytes(), nil
}

// deliverFlowRead keeps what a ReadFlow priced; its Reply carries the
// board (deliverTracked).
func (m *Model) deliverFlowRead(r flowReadResult) {
	if r.priced == nil && r.design == nil {
		return
	}
	if _, ws := m.lookup(r.id); ws == nil {
		return
	}
	fs := m.flowsOf(r.id)
	if r.priced != nil {
		fs.priced[r.run] = *r.priced
	}
	if r.design != nil {
		fs.design[r.spec] = *r.design
	}
}

// flowsResult is ReadFlows' job result.
type flowsResult struct {
	view FlowsView
	err  error
}

// ReadFlows answers with every served session's flows and the host's past
// runs (the Reply's Flows), the records read in a job.
func (m *Model) ReadFlows(req ReqID) {
	var live []LiveFlow
	for _, inst := range m.allInstances() {
		id := m.idOf(inst)
		for _, f := range m.flowSummaries(id) {
			live = append(live, LiveFlow{Instance: id, Title: inst.Title, Flow: f})
		}
	}
	records := flowRecordsDir()
	m.spawn(m.track(req, 0, func() any {
		history, err := flowHistory(records)
		if err != nil {
			return flowsResult{err: fmt.Errorf("%s: %w", opReadFlows, err)}
		}
		return flowsResult{view: FlowsView{Live: live, History: history}}
	}))
}

// flowDocResult is ReadFlowDoc's job result.
type flowDocResult struct {
	doc flowview.Doc
	err error
}

// ReadFlowDoc reads one of the documents of the flow whose plan is plan in
// session id's worktree (flowview.ReadDoc builds its path; doc only names
// it) and answers with the Reply's FlowDoc.
func (m *Model) ReadFlowDoc(id InstanceID, plan string, doc string, req ReqID) {
	inst, _, ok := m.admit(opReadFlowDoc, id, req)
	if !ok {
		return
	}
	worktree, base, lookable := flowLookable(inst)
	if !lookable {
		m.refuse(req, id, fmt.Errorf("%s %s: the session has no worktree to read", opReadFlowDoc, inst.Title))
		return
	}
	records := flowRecordsDir()
	m.spawn(m.track(req, id, func() any {
		d, err := readFlowDoc(worktree, base, records, plan, doc)
		if err != nil {
			return flowDocResult{err: fmt.Errorf("%s: %w", opReadFlowDoc, err)}
		}
		return flowDocResult{doc: d}
	}))
}
