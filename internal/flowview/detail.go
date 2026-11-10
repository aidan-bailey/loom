package flowview

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/aidan-bailey/loom/internal/flowrun"
	"github.com/aidan-bailey/loom/sched"
)

// A cell's states on the board (spec §5).
const (
	CellTodo    = "todo"    // grey: no attempt of the kind yet
	CellRunning = "running" // blue: its latest attempt runs
	CellPassed  = "passed"  // green: its latest attempt passed, or reported done
	CellFailed  = "failed"  // red: its latest attempt failed, with no later pass
	CellAsks    = "asks"    // amber: the open question's step
	CellSkipped = "skipped" // —: the story went past without one
)

// stepStarting is a step whose name the run has but whose record isn't
// written yet: it is being started.
const stepStarting = "starting"

// rowKinds is a story's step kinds in their order; premiseKinds and
// finalKinds the premise and final rows'.
var (
	rowKinds     = []string{flowrun.KindDev, flowrun.KindGates, flowrun.KindReview, flowrun.KindFix, flowrun.KindDocs}
	premiseKinds = []string{flowrun.KindPremise}
	finalKinds   = []string{flowrun.KindGates, flowrun.KindReview, flowrun.KindFix}
)

// GateLine is one line of a gate run: its command, its verdict ("" until
// judged) and its latest job.
type GateLine struct {
	Text    string
	Verdict string
	Job     sched.JobID
}

// StepView is one agent or gate run of a story.
type StepView struct {
	Name string
	Kind string
	K    int
	Role string // an agent's role, "" for a gate run
	// Session is an agent run's Claude session as its record has it, ""
	// until its job has ended.
	Session string
	Status  string // a report's status (DONE, BLOCKED, …), running, starting, failed, cancelled; a gate run's passed, failed, infra or running
	Jobs    []sched.JobID
	Report  bool // agents/<name>/report.md exists
	Gates   []GateLine
	// Unreadable says why the step's record can't be read.
	Unreadable string
}

// StoryView is a story's row: the plan's facts, the run's row, its cells
// by kind and its steps in the order they started.
type StoryView struct {
	ID        string
	Developer string
	Tier      string
	Review    string
	Line, End int    // its plan range
	Reading   string // the skeleton entry's **Reading:** line
	Row       flowrun.Row
	Cells     map[string]string
	Steps     []StepView
}

// OtherRun is another run state of the plan.
type OtherRun struct {
	ID         string
	Status     string
	Start      string
	Unreadable string
}

// FlowDetail is one flow's board (spec §2).
type FlowDetail struct {
	Summary
	Stages   []Stage
	Base     string
	Start    string
	End      string
	Managers []string
	Premise  *StoryView // nil when the run has no premise step
	Stories  []StoryView
	Final    *StoryView // nil until a final step exists
	Question *flowrun.Question
	Kept     []flowrun.Finding
	Notes    []string
	Others   []OtherRun
	// Design is the plan's **Design stage:** line, "" for none.
	Design string
}

// Detail reads the flow whose plan (or spec in design) is plan, under top.
func Detail(top, base, recordsDir, plan string) (FlowDetail, error) {
	f, err := flowOf(top, base, recordsDir, plan)
	if err != nil {
		return FlowDetail{}, err
	}
	stages := stagesOf(f)
	d := FlowDetail{Summary: summaryOf(f, stages), Stages: stages, Design: f.header.DesignStage}
	for _, r := range f.runs[min(1, len(f.runs)):] {
		o := OtherRun{}
		if r.st != nil {
			o.ID, o.Status, o.Start = r.st.ID, r.st.Status, r.st.Start
		} else {
			o.ID = strings.TrimSuffix(filepath.Base(r.path), ".json")
		}
		if r.err != nil {
			o.Unreadable = r.err.Error()
		}
		d.Others = append(d.Others, o)
	}
	r := f.run()
	if r == nil || r.st == nil {
		for _, id := range f.header.storyIDs() {
			d.Stories = append(d.Stories, StoryView{ID: id, Reading: f.header.Readings[id], Cells: todoCells(rowKinds)})
		}
		return d, nil
	}
	st := r.st
	d.Base, d.Start, d.End, d.Managers = st.Base, st.Start, st.End, st.Managers
	d.Kept, d.Notes = st.Kept, st.Notes
	if st.Status == flowrun.Parked {
		d.Question = st.Question
	}
	board(&d, st, f.header, filepath.Join(f.stateDir, "agents"), filepath.Join(f.stateDir, "gates"))
	return d, nil
}

// flowOf gathers top's flows and picks plan's.
func flowOf(top, base, recordsDir, plan string) (*flow, error) {
	flows, err := gather(top, base, recordsDir)
	if err != nil {
		return nil, err
	}
	want := cleanRel(top, plan)
	for _, f := range flows {
		if f.plan == want {
			return f, nil
		}
	}
	return nil, fmt.Errorf("no flow %s in %s", plan, top)
}

// board fills d's rows from the run state and its records.
func board(d *FlowDetail, st *flowrun.State, h planHeader, agents, gates string) {
	byStory := map[string][]StepView{}
	var premise, final []StepView
	place := func(v StepView, s flowrun.Step) {
		switch {
		case s.Kind == flowrun.KindPremise:
			premise = append(premise, v)
		case s.Final:
			final = append(final, v)
		default:
			byStory[s.Story] = append(byStory[s.Story], v)
		}
	}
	for _, name := range st.Agents {
		s, ok := flowrun.StepOf(st.ID, name, st.Stories)
		if !ok {
			continue
		}
		place(agentStep(st, name, s, agents), s)
	}
	for _, name := range st.Gates {
		s, ok := flowrun.StepOf(st.ID, name, st.Stories)
		if !ok {
			continue
		}
		place(gateStep(st, name, s, gates), s)
	}
	asks, hasAsk := flowrun.Step{}, false
	if d.Question != nil {
		asks, hasAsk = flowrun.QuestionStep(st.ID, *d.Question, st.Stories)
	}
	for _, s := range st.Stories {
		v := StoryView{ID: s.ID, Developer: s.Developer, Tier: s.Tier, Review: s.Review, Line: s.Line, End: s.End,
			Reading: h.Readings[s.ID], Row: s.Row, Steps: ordered(byStory[s.ID])}
		v.Cells = cells(v.Steps, rowKinds, kindReached(s.Row.Status))
		if hasAsk && !asks.Final && asks.Kind != flowrun.KindPremise && asks.Story == s.ID {
			v.Cells[asks.Kind] = CellAsks
		}
		d.Stories = append(d.Stories, v)
	}
	if len(premise) > 0 || (hasAsk && asks.Kind == flowrun.KindPremise) {
		v := StoryView{ID: "P", Steps: ordered(premise)}
		v.Cells = cells(v.Steps, premiseKinds, len(premiseKinds))
		if hasAsk && asks.Kind == flowrun.KindPremise {
			v.Cells[flowrun.KindPremise] = CellAsks
		}
		d.Premise = &v
	}
	if len(final) > 0 {
		reached := 0
		if flowrun.Ended(st.Status) {
			reached = len(finalKinds)
		}
		v := StoryView{ID: "F", Steps: ordered(final)}
		v.Cells = cells(v.Steps, finalKinds, reached)
		if hasAsk && asks.Final {
			v.Cells[asks.Kind] = CellAsks
		}
		d.Final = &v
	}
}

// agentStep is agent run name's step: its status from the run's memo
// (the engine's own reading of its report), else from its record.
func agentStep(st *flowrun.State, name string, s flowrun.Step, dir string) StepView {
	v := StepView{Name: name, Kind: s.Kind, K: s.K, Status: stepStarting}
	rec, err := flowrun.LoadAgent(dir, name)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		v.Unreadable = err.Error()
	default:
		v.Role, v.Status, v.Session = rec.Role, rec.Status, rec.Session
		for _, j := range rec.Jobs {
			v.Jobs = append(v.Jobs, j.ID)
		}
	}
	// A resumed run is running again whatever its earlier report said.
	var r flowrun.Report
	if raw, ok := st.Memo["agent:"+name]; ok && v.Status != flowrun.AgentRunning && json.Unmarshal(raw, &r) == nil && r.Status != "" {
		v.Status = r.Status
	}
	if _, err := os.Stat(filepath.Join(dir, name, "report.md")); err == nil {
		v.Report = true
	}
	if v.Unreadable != "" && v.Status == stepStarting {
		v.Status = ""
	}
	return v
}

// gateStep is gate run name's step: its end from the run's memo, else from
// its record's verdicts.
func gateStep(st *flowrun.State, name string, s flowrun.Step, dir string) StepView {
	v := StepView{Name: name, Kind: s.Kind, K: s.K, Status: stepStarting}
	rec, err := flowrun.LoadGate(dir, name)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		v.Unreadable = err.Error()
	default:
		v.Status = "running"
		if rec.Ended() {
			v.Status = "passed"
			if rec.Failed() || rec.Cancelled {
				v.Status = "failed"
			}
		}
		for _, step := range rec.Steps {
			v.Gates = append(v.Gates, GateLine{Text: step.Text(), Verdict: step.Verdict, Job: step.Job()})
			v.Jobs = append(v.Jobs, step.Jobs...)
		}
	}
	var res flowrun.GateResult
	if raw, ok := st.Memo["gates:"+name]; ok && v.Status != "running" && json.Unmarshal(raw, &res) == nil && res.Ended != "" {
		v.Status = res.Ended
	}
	if v.Unreadable != "" && v.Status == stepStarting {
		v.Status = ""
	}
	return v
}

// ordered is steps in the order they started: by their first job, which
// the queue numbers in submission order; a step with no job yet goes last.
func ordered(steps []StepView) []StepView {
	out := slices.Clone(steps)
	first := func(v StepView) sched.JobID {
		if len(v.Jobs) == 0 {
			return 1<<62 - 1
		}
		return slices.Min(v.Jobs)
	}
	sort.SliceStable(out, func(i, j int) bool { return first(out[i]) < first(out[j]) })
	return out
}

// kindReached is how many of a story's kinds its row status has passed:
// a kind before it with no attempt went by without one.
func kindReached(status string) int {
	switch status {
	case "gating":
		return 1
	case "reviewing":
		return 2
	case "fixing":
		return 3
	case "documenting":
		return 4
	case "done":
		return len(rowKinds)
	}
	return 0
}

// cells is each kind's cell over steps: the latest attempt of the kind
// decides; a kind with none is skipped when reached passes it, or the
// story's doc pass has started, else todo.
func cells(steps []StepView, kinds []string, reached int) map[string]string {
	out := map[string]string{}
	// A doc pass comes after the rest of its story, whose gates then run
	// again and say gating: a kind before a docs attempt with none of its
	// own went by without one. Only docs: a gate's fixer is a fix attempt
	// before the review has run.
	if i := slices.Index(kinds, flowrun.KindDocs); i >= 0 && slices.ContainsFunc(steps, func(s StepView) bool { return s.Kind == flowrun.KindDocs }) {
		reached = max(reached, i)
	}
	for i, kind := range kinds {
		var latest *StepView
		for j := range steps {
			if steps[j].Kind == kind {
				latest = &steps[j]
			}
		}
		switch {
		case latest == nil && i < reached:
			out[kind] = CellSkipped
		case latest == nil:
			out[kind] = CellTodo
		default:
			out[kind] = cellOf(latest.Status)
		}
	}
	return out
}

// todoCells is every kind grey.
func todoCells(kinds []string) map[string]string {
	out := map[string]string{}
	for _, k := range kinds {
		out[k] = CellTodo
	}
	return out
}

// cellOf is an attempt's status as a cell.
func cellOf(status string) string {
	switch status {
	case flowrun.AgentRunning, stepStarting:
		return CellRunning
	case "DONE", "DONE_WITH_CONCERNS", "passed":
		return CellPassed
	case "":
		return CellTodo
	}
	return CellFailed
}
