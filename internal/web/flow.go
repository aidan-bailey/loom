package web

import (
	"net/http"
	"slices"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/internal/flowview"
	"github.com/aidan-bailey/loom/sched"
)

// The flow routes (spec docs/superpowers/specs/2026-11-09-flow-visualisation-design.md
// §5): a session's flow board (GET …/flow?plan=), every session's flows and
// the host's past runs (GET /api/v1/flows), and one of a flow's documents
// as its tree (GET …/flow/doc?plan=&doc=). Each is a Core read answered by
// Reply; the screens draw what they answer and act on nothing.

// The bounds of a flow request's query, and of a document's tree.
const (
	maxPlanQuery = 4096
	maxDocQuery  = 256
	docTreeNodes = 40000
)

// flowSummaryJSON is a flow as the fleet chip and the Flows screen read it
// (core.FlowSummary). Status is "lost" when the run says it runs and its
// engine has ended; Spent is null until the run has been priced.
type flowSummaryJSON struct {
	Plan       string   `json:"plan"`
	Spec       string   `json:"spec"`
	Title      string   `json:"title"`
	Stage      string   `json:"stage"`
	Status     string   `json:"status"`
	Run        string   `json:"run"`
	Done       int      `json:"done"`
	Total      int      `json:"total"`
	Drafted    int      `json:"drafted"`
	Asks       string   `json:"asks"`
	Phase      string   `json:"phase"`
	Updated    string   `json:"updated"`
	Unreadable string   `json:"unreadable"`
	Spent      *float64 `json:"spent"`
}

func flowSummaryOf(f core.FlowSummary) flowSummaryJSON {
	s := flowSummaryJSON{
		Plan: f.Plan, Spec: f.Spec, Title: f.Title, Stage: f.Stage, Status: f.Status, Run: f.Run,
		Done: f.Done, Total: f.Total, Drafted: f.Drafted, Asks: f.Asks, Phase: f.Phase,
		Updated: stamp(f.Updated), Unreadable: f.Unreadable,
	}
	if f.Lost {
		s.Status = "lost"
	}
	if f.Priced {
		v := f.Spent
		s.Spent = &v
	}
	return s
}

// flowsOfView is a session's flows for its fleet row, an empty list for none.
func flowsOfView(v core.InstanceView) []flowSummaryJSON {
	out := make([]flowSummaryJSON, 0, len(v.Flows))
	for _, f := range v.Flows {
		out = append(out, flowSummaryOf(f))
	}
	return out
}

// flowStageJSON is one step of the strip (flowview.Stage).
type flowStageJSON struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Detail string `json:"detail"`
	Doc    string `json:"doc"`
}

// flowGateJSON is a gate run's line.
type flowGateJSON struct {
	Text    string      `json:"text"`
	Verdict string      `json:"verdict"`
	Job     sched.JobID `json:"job"`
}

// flowStepJSON is an agent or gate run of a row, with its first job's
// start and its latest job's end as the queue still knows them ("" when it
// doesn't).
type flowStepJSON struct {
	Name       string         `json:"name"`
	Kind       string         `json:"kind"`
	K          int            `json:"k"`
	Role       string         `json:"role"`
	Status     string         `json:"status"`
	Jobs       []sched.JobID  `json:"jobs"`
	Report     bool           `json:"report"`
	Gates      []flowGateJSON `json:"gates"`
	Started    string         `json:"started"`
	Ended      string         `json:"ended"`
	Unreadable string         `json:"unreadable"`
}

// flowStoryJSON is a row of the board: a story, or the premise (P) or final
// (F) row.
type flowStoryJSON struct {
	ID         string            `json:"id"`
	Developer  string            `json:"developer"`
	Tier       string            `json:"tier"`
	Review     string            `json:"review"`
	Line       int               `json:"line"`
	End        int               `json:"end"`
	Reading    string            `json:"reading"`
	Status     string            `json:"status"`
	Commits    int               `json:"commits"`
	Deviations int               `json:"deviations"`
	Findings   [3]int            `json:"findings"`
	Cells      map[string]string `json:"cells"`
	Steps      []flowStepJSON    `json:"steps"`
}

// flowQuestionJSON is a parked run's open question.
type flowQuestionJSON struct {
	Key   string `json:"key"`
	Kind  string `json:"kind"`
	Story string `json:"story"`
	Text  string `json:"text"`
}

// flowOtherJSON is another run of the plan.
type flowOtherJSON struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	Start      string `json:"start"`
	Unreadable string `json:"unreadable"`
}

// flowCostsJSON is the cost box (flowview.Costs), at API list prices.
type flowCostsJSON struct {
	Spent      float64            `json:"spent"`
	ByStory    map[string]float64 `json:"by_story"`
	ByAgent    map[string]float64 `json:"by_agent"`
	ByRole     map[string]float64 `json:"by_role"`
	Unpriced   []string           `json:"unpriced"`
	Design     *float64           `json:"design"`
	Draft      *float64           `json:"draft"`
	Flow       float64            `json:"flow"`
	FlowParts  int                `json:"flow_parts"`
	Projection *float64           `json:"projection"`
	Why        string             `json:"why"`
	Past       []float64          `json:"past"`
	Median     *float64           `json:"median"`
	This       *float64           `json:"this"`
	Error      string             `json:"error"`
}

// flowJSON is GET …/flow: the board.
type flowJSON struct {
	Summary  flowSummaryJSON   `json:"summary"`
	Stages   []flowStageJSON   `json:"stages"`
	Base     string            `json:"base"`
	Start    string            `json:"start"`
	End      string            `json:"end"`
	Premise  *flowStoryJSON    `json:"premise"`
	Stories  []flowStoryJSON   `json:"stories"`
	Final    *flowStoryJSON    `json:"final"`
	Question *flowQuestionJSON `json:"question"`
	Kept     []string          `json:"kept"`
	Others   []flowOtherJSON   `json:"others"`
	Costs    flowCostsJSON     `json:"costs"`
}

func flowOf(v core.FlowView, lost bool) flowJSON {
	d := v.Detail
	f := flowJSON{
		Summary: flowSummaryOf(core.FlowSummary{Summary: d.Summary, Lost: lost, Spent: v.Costs.Spent, Priced: v.Costs.Error == "" && d.Run != ""}),
		Stages:  make([]flowStageJSON, 0, len(d.Stages)), Base: d.Base, Start: d.Start, End: d.End,
		Stories: make([]flowStoryJSON, 0, len(d.Stories)), Kept: make([]string, 0, len(d.Kept)), Others: make([]flowOtherJSON, 0, len(d.Others)),
		Costs: costsOf(v.Costs),
	}
	for _, s := range d.Stages {
		f.Stages = append(f.Stages, flowStageJSON{Name: s.Name, State: s.State, Detail: s.Detail, Doc: s.Doc})
	}
	for _, s := range d.Stories {
		f.Stories = append(f.Stories, storyOf(s, v.Times))
	}
	if d.Premise != nil {
		p := storyOf(*d.Premise, v.Times)
		f.Premise = &p
	}
	if d.Final != nil {
		p := storyOf(*d.Final, v.Times)
		f.Final = &p
	}
	if q := d.Question; q != nil {
		f.Question = &flowQuestionJSON{Key: q.Key, Kind: q.Kind, Story: q.Story, Text: q.Text}
	}
	for _, k := range d.Kept {
		f.Kept = append(f.Kept, k.String())
	}
	for _, o := range d.Others {
		f.Others = append(f.Others, flowOtherJSON{ID: o.ID, Status: o.Status, Start: o.Start, Unreadable: o.Unreadable})
	}
	return f
}

func storyOf(s flowview.StoryView, times map[sched.JobID]core.JobTimes) flowStoryJSON {
	out := flowStoryJSON{
		ID: s.ID, Developer: s.Developer, Tier: s.Tier, Review: s.Review, Line: s.Line, End: s.End, Reading: s.Reading,
		Status: s.Row.Status, Commits: s.Row.Commits, Deviations: s.Row.Deviations, Findings: s.Row.Findings,
		Cells: s.Cells, Steps: make([]flowStepJSON, 0, len(s.Steps)),
	}
	if out.Cells == nil {
		out.Cells = map[string]string{}
	}
	if out.Developer == "" {
		out.Developer = s.Row.Developer
	}
	for _, st := range s.Steps {
		step := flowStepJSON{Name: st.Name, Kind: st.Kind, K: st.K, Role: st.Role, Status: st.Status, Report: st.Report,
			Jobs: slices.Clone(st.Jobs), Gates: make([]flowGateJSON, 0, len(st.Gates)), Unreadable: st.Unreadable}
		if step.Jobs == nil {
			step.Jobs = []sched.JobID{}
		}
		for _, g := range st.Gates {
			step.Gates = append(step.Gates, flowGateJSON{Text: g.Text, Verdict: g.Verdict, Job: g.Job})
		}
		var started, ended time.Time
		for _, j := range st.Jobs {
			t := times[j]
			if !t.Started.IsZero() && (started.IsZero() || t.Started.Before(started)) {
				started = t.Started
			}
			if t.Ended.After(ended) {
				ended = t.Ended
			}
		}
		if len(st.Jobs) > 0 && times[st.Jobs[len(st.Jobs)-1]].Ended.IsZero() {
			ended = time.Time{} // its latest job runs
		}
		step.Started, step.Ended = stamp(started), stamp(ended)
		out.Steps = append(out.Steps, step)
	}
	return out
}

func costsOf(c flowview.Costs) flowCostsJSON {
	out := flowCostsJSON{
		Spent: c.Spent, ByStory: c.ByStory, ByAgent: c.ByAgent, ByRole: c.ByRole, Unpriced: c.Unpriced,
		Design: c.Design, Draft: c.Draft, Flow: c.Flow, FlowParts: c.FlowParts, Projection: c.Projection, Why: c.Why,
		Past: c.Compare.Past, Median: c.Compare.Median, This: c.Compare.This, Error: c.Error,
	}
	if out.Unpriced == nil {
		out.Unpriced = []string{}
	}
	if out.Past == nil {
		out.Past = []float64{}
	}
	return out
}

// liveFlowJSON is a session's flow on the Flows screen.
type liveFlowJSON struct {
	Session core.InstanceID `json:"session"`
	Title   string          `json:"title"`
	Flow    flowSummaryJSON `json:"flow"`
}

// pastRunJSON is a run record (flowview.PastRun); usd is null for a record
// without its cost.
type pastRunJSON struct {
	Key        string   `json:"key"`
	File       string   `json:"file"`
	Stem       string   `json:"stem"`
	Plan       string   `json:"plan"`
	Stories    int      `json:"stories"`
	Ended      string   `json:"ended"`
	End        string   `json:"end"`
	USD        *float64 `json:"usd"`
	Unreadable string   `json:"unreadable"`
}

// flowsJSON is GET /api/v1/flows.
type flowsJSON struct {
	Live []liveFlowJSON `json:"live"`
	Past []pastRunJSON  `json:"past"`
}

func flowsOf(v core.FlowsView) flowsJSON {
	out := flowsJSON{Live: make([]liveFlowJSON, 0, len(v.Live)), Past: make([]pastRunJSON, 0, len(v.History))}
	for _, l := range v.Live {
		out.Live = append(out.Live, liveFlowJSON{Session: l.Instance, Title: l.Title, Flow: flowSummaryOf(l.Flow)})
	}
	for _, r := range v.History {
		p := pastRunJSON{Key: r.Key, File: r.File, Stem: r.Stem, Plan: r.Plan, Stories: r.Stories, Ended: r.Ended, End: stamp(r.End), Unreadable: r.Unreadable}
		if r.HasUSD {
			v := r.USD
			p.USD = &v
		}
		out.Past = append(out.Past, p)
	}
	return out
}

// flowDocJSON is GET …/flow/doc: a document's path, its tree, and whether
// the document or its tree was cut.
type flowDocJSON struct {
	Path   string    `json:"path"`
	Blocks []*mdNode `json:"blocks"`
	Cut    bool      `json:"cut"`
}

// flowQuery is a flow route's plan, refused when empty or too long.
func (l *Layer) flowQuery(w http.ResponseWriter, r *http.Request, a actionLog) (string, bool) {
	plan := r.URL.Query().Get("plan")
	if plan == "" || len(plan) > maxPlanQuery {
		l.refuse(w, a, http.StatusBadRequest, "bad request: a flow route takes its plan (?plan=)")
		return "", false
	}
	return plan, true
}

// readFlow answers GET /api/v1/sessions/{id}/flow?plan=: the board.
func (l *Layer) readFlow(w http.ResponseWriter, r *http.Request, device string) {
	a := actionLog{device: device, name: "flow"}
	id, ok := l.readSession(w, r, &a)
	if !ok {
		return
	}
	plan, ok := l.flowQuery(w, r, a)
	if !ok {
		return
	}
	reply, ok := l.read(w, r, a, func(req core.ReqID) { l.client.ReadFlow(id, plan, req) })
	if !ok {
		return
	}
	lost := false
	if v, ok := l.client.View(id); ok {
		for _, f := range v.Flows {
			if f.Plan == reply.Flow.Detail.Plan {
				lost = f.Lost
			}
		}
	}
	l.record(a, http.StatusOK, "ok")
	writeJSON(w, http.StatusOK, flowOf(reply.Flow, lost))
}

// readFlows answers GET /api/v1/flows: every session's flows and the
// host's past runs.
func (l *Layer) readFlows(w http.ResponseWriter, r *http.Request, device string) {
	a := actionLog{device: device, name: "flows"}
	reply, ok := l.read(w, r, a, func(req core.ReqID) { l.client.ReadFlows(req) })
	if !ok {
		return
	}
	l.record(a, http.StatusOK, "ok")
	writeJSON(w, http.StatusOK, flowsOf(reply.Flows))
}

// readFlowDoc answers GET /api/v1/sessions/{id}/flow/doc?plan=&doc=: the
// document as its tree, each heading with its line.
func (l *Layer) readFlowDoc(w http.ResponseWriter, r *http.Request, device string) {
	a := actionLog{device: device, name: "flow-doc"}
	id, ok := l.readSession(w, r, &a)
	if !ok {
		return
	}
	plan, ok := l.flowQuery(w, r, a)
	if !ok {
		return
	}
	doc := r.URL.Query().Get("doc")
	if doc == "" || len(doc) > maxDocQuery {
		l.refuse(w, a, http.StatusBadRequest, "bad request: the document route takes its doc (?doc=)")
		return
	}
	reply, ok := l.read(w, r, a, func(req core.ReqID) { l.client.ReadFlowDoc(id, plan, doc, req) })
	if !ok {
		return
	}
	blocks, cut := buildTreeLimit(reply.FlowDoc.Text, flowview.DocLimit, docTreeNodes, true)
	l.record(a, http.StatusOK, "ok")
	writeJSON(w, http.StatusOK, flowDocJSON{Path: reply.FlowDoc.Path, Blocks: blocks, Cut: cut || reply.FlowDoc.Cut})
}
