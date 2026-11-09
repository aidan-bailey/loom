package flowview

import (
	"fmt"
	"path/filepath"

	"github.com/aidan-bailey/loom/internal/flowrun"
)

// Stage names, in a flow's order.
const (
	StageDesign = "design"
	StageSpec   = "spec"
	StageDraft  = "draft"
	StageRun    = "run"
	StageRecord = "record"
)

// A stage's states.
const (
	StateDone       = "done"
	StateNow        = "now"
	StateTodo       = "todo"
	StateUnreadable = "unreadable"
)

// A flow's statuses beside a run's own (flowrun.Running, Parked, Done,
// Stopped, Cancelled).
const (
	StatusDesigning  = "designing"
	StatusDesigned   = "designed"
	StatusDrafting   = "drafting"
	StatusDrafted    = "drafted"
	StatusSupervised = "supervised"
)

// The documents a flow opens (ReadFlowDoc's doc keys); an agent's report
// is DocReport + its name.
const (
	DocSpec        = "spec"
	DocSkeleton    = "skeleton"
	DocPlan        = "plan"
	DocRecord      = "record"
	DocDraftsState = "drafts-state"
	DocReport      = "report:"
)

// Stage is one step of a flow's strip: its name, its state, a few words
// about it and the document it opens ("" for none).
type Stage struct {
	Name   string
	State  string
	Detail string
	Doc    string
}

// stagesOf is f's strip, by the spec's stage table (§1): each stage read
// from what it leaves behind.
func stagesOf(f *flow) []Stage {
	return []Stage{f.designStage(), f.specStage(), f.draftStage(), f.runStage(), f.recordStage()}
}

func (f *flow) designStage() Stage {
	s := Stage{Name: StageDesign, State: StateNow, Detail: f.specTitle}
	if f.specExists {
		s.Doc = DocSpec
	}
	if !f.design && f.committed && (f.spec == "" || f.specCommitted) {
		s.State = StateDone
	}
	return s
}

func (f *flow) specStage() Stage {
	s := Stage{Name: StageSpec, State: StateTodo}
	switch {
	case f.design:
		// The spec is the design stage's own work until a plan names it.
		s.Detail, s.Doc = f.spec, DocSpec
	case f.spec == "":
		s.Detail = "the plan names no spec"
	case !f.specExists:
		s.State, s.Detail = StateUnreadable, fmt.Sprintf("the spec %s does not exist", f.spec)
	default:
		s.State, s.Detail, s.Doc = StateDone, f.spec, DocPlan
		if f.header.Marker {
			s.Doc = DocSkeleton
		}
	}
	return s
}

func (f *flow) draftStage() Stage {
	s := Stage{Name: StageDraft, State: StateTodo}
	switch {
	case f.design:
	case f.header.DesignStage != "":
		s.State, s.Detail, s.Doc = StateDone, "by /flow:draft", DocPlan
	case f.draftErr != nil:
		s.State, s.Detail = StateUnreadable, "unreadable: "+f.draftErr.Error()
	case f.draftsExist:
		s.State = StateNow
		s.Detail = fmt.Sprintf("%d of %d drafted", f.drafted, len(f.header.Entries))
		if f.draftPhase != "" {
			s.Detail += " · " + f.draftPhase
		}
		if f.draftState {
			s.Doc = DocDraftsState
		}
	case len(f.header.Stories) > 0:
		s.State, s.Detail, s.Doc = StateDone, "written by hand", DocPlan
	}
	return s
}

func (f *flow) runStage() Stage {
	s := Stage{Name: StageRun, State: StateTodo}
	if r := f.run(); r != nil {
		switch {
		case r.err != nil:
			s.State, s.Detail = StateUnreadable, "unreadable: "+r.err.Error()
		case flowrun.Ended(r.st.Status):
			s.State, s.Detail = StateDone, r.st.Status
		default:
			s.State, s.Detail = StateNow, r.st.Status
		}
		return s
	}
	if f.supervised != "" {
		s.State, s.Detail = StateNow, "supervised"
		if f.supErr != nil {
			s.State, s.Detail = StateUnreadable, "unreadable: "+f.supErr.Error()
		} else if f.supPhase != "" {
			s.Detail += " · " + f.supPhase
		}
		if f.record != "" && f.supErr == nil {
			s.State = StateDone
		}
	}
	return s
}

func (f *flow) recordStage() Stage {
	if f.record == "" {
		return Stage{Name: StageRecord, State: StateTodo}
	}
	return Stage{Name: StageRecord, State: StateDone, Detail: filepath.Base(f.record), Doc: DocRecord}
}

// furthest is the last stage begun.
func furthest(stages []Stage) string {
	name := StageDesign
	for _, s := range stages {
		if s.State != StateTodo {
			name = s.Name
		}
	}
	return name
}

// statusOf is a flow's status: its shown run's, else what its furthest
// stage says.
func statusOf(f *flow, stages []Stage) string {
	if r := f.run(); r != nil {
		if r.st != nil {
			return r.st.Status
		}
		return ""
	}
	byName := map[string]Stage{}
	for _, s := range stages {
		byName[s.Name] = s
	}
	switch {
	case f.supervised != "" && f.record != "":
		return flowrun.Done
	case f.supervised != "":
		return StatusSupervised
	case f.record != "":
		return flowrun.Done
	case byName[StageDraft].State == StateNow:
		return StatusDrafting
	case byName[StageDraft].State == StateDone:
		return StatusDrafted
	case byName[StageDesign].State == StateDone:
		return StatusDesigned
	}
	return StatusDesigning
}
