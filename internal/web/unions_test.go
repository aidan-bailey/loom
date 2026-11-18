package web

import (
	"reflect"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/attention"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/internal/flowrun"
	"github.com/aidan-bailey/loom/internal/flowview"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/git"
	"github.com/aidan-bailey/loom/session/transcript"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// unionRow is one typed string type with the name and values its union has.
type unionRow struct {
	typ    reflect.Type
	name   string
	values []string
}

func unionRows() []unionRow {
	return []unionRow{
		{reflect.TypeFor[sessionGroup](), "SessionGroup", []string{"needs_you", "your_turn", "working", "paused"}},
		{reflect.TypeFor[sessionStatus](), "SessionStatus", []string{"Running", "Ready", "Loading", "Paused", "Prompting", "Deleting", "Recoverable"}},
		{reflect.TypeFor[stopVerdict](), "StopVerdict", []string{"needs_you", "done", "waiting", "stopped"}},
		{reflect.TypeFor[jobState](), "JobState", []string{"held", "queued", "running", "done", "failed", "cancelled", "lost"}},
		{reflect.TypeFor[jobControlKind](), "JobControl", []string{"pause", "resume", ""}},
		{reflect.TypeFor[turnFrom](), "TurnFrom", []string{"you", "plugin", "peer", "task", "brief"}},
		{reflect.TypeFor[itemKind](), "ItemKind", []string{"text", "thinking", "steps", "compacted", "note"}},
		{reflect.TypeFor[stepState](), "StepState", []string{"pending", "done", "failed", "interrupted"}},
		{reflect.TypeFor[agentPageState](), "AgentPageState", []string{"running", "finished", "unknown"}},
		{reflect.TypeFor[fileStatus](), "FileStatus", []string{"A", "M", "D", "R", "C"}},
		{reflect.TypeFor[fileOmitted](), "FileOmitted", []string{"", "lines", "budget"}},
		{reflect.TypeFor[askKind](), "AskKind", []string{"question", "permission"}},
		{reflect.TypeFor[prState](), "PrState", []string{"draft", "open", "merged", "closed"}},
		{reflect.TypeFor[prChecks](), "PrChecks", []string{"none", "pending", "passing", "failing"}},
		{reflect.TypeFor[flowStageName](), "FlowStageName", []string{"design", "spec", "draft", "run", "record"}},
		{reflect.TypeFor[flowStageState](), "FlowStageState", []string{"done", "now", "todo", "unreadable"}},
		{reflect.TypeFor[flowStatus](), "FlowStatus", []string{"designing", "designed", "drafting", "drafted", "supervised", "running", "parked", "done", "stopped", "cancelled", "lost", ""}},
		{reflect.TypeFor[flowCell](), "FlowCell", []string{"todo", "running", "passed", "failed", "asks", "skipped"}},
		{reflect.TypeFor[flowStepKind](), "FlowStepKind", []string{"premise", "dev", "gates", "review", "fix", "docs"}},
		{reflect.TypeFor[flowQuestionKind](), "FlowQuestionKind", []string{"needs-context", "blocked-decision", "design-finding", "premise-design", "unresolved-findings", "gate-failure", "agent-failed", "tree-moved", "docs-strayed", "usage-limit", "lane", ""}},
	}
}

func TestUnions_EachTypeIsListedUnderItsName(t *testing.T) {
	rows := unionRows()
	require.Len(t, rows, 20)
	got := unions()
	assert.Len(t, got, 20)
	for _, r := range rows {
		u, ok := got[r.typ]
		if assert.True(t, ok, r.typ.String()) {
			assert.Equal(t, union{name: r.name, values: r.values}, u, r.typ.String())
		}
	}
}

func TestUnions_ValuesAreWhatTheCodeSends(t *testing.T) {
	var statuses []string
	for s := session.Status(0); !strings.HasPrefix(s.String(), "Status("); s++ {
		statuses = append(statuses, s.String())
	}
	want := map[reflect.Type][]string{
		reflect.TypeFor[sessionGroup]():  {string(groupNeedsYou), string(groupYourTurn), string(groupWorking), string(groupPaused)},
		reflect.TypeFor[sessionStatus](): statuses,
		reflect.TypeFor[stopVerdict]():   {attention.NeedsYou, attention.Done, attention.Waiting, attention.Stopped},
		reflect.TypeFor[jobState](): {
			string(sched.Held), string(sched.Queued), string(sched.Running), string(sched.Done),
			string(sched.Failed), string(sched.Cancelled), string(sched.Lost)},
		reflect.TypeFor[jobControlKind](): {string(sched.ControlPause), string(sched.ControlResume), ""},
		reflect.TypeFor[turnFrom](): {
			transcript.FromYou, transcript.FromPlugin, transcript.FromPeer, transcript.FromTask, transcript.FromBrief},
		reflect.TypeFor[itemKind](): {
			transcript.KindText, transcript.KindThinking, transcript.KindSteps, transcript.KindCompacted, transcript.KindNote},
		reflect.TypeFor[stepState](): {
			transcript.StatePending, transcript.StateDone, transcript.StateFailed, transcript.StateInterrupted},
		reflect.TypeFor[agentPageState](): {transcript.AgentRunning, transcript.AgentFinished, transcript.AgentUnknown},
		reflect.TypeFor[fileStatus](): {
			git.StatusAdded, git.StatusModified, git.StatusDeleted, git.StatusRenamed, git.StatusCopied},
		reflect.TypeFor[fileOmitted](): {"", git.OmittedLines, git.OmittedBudget},
		reflect.TypeFor[askKind]():     {core.AskKindQuestion, core.AskKindPermission},
		reflect.TypeFor[prState]():     {string(prDraft), string(prOpen), string(prMerged), string(prClosed)},
		reflect.TypeFor[prChecks]():    {string(checksNone), string(checksPending), string(checksPassing), string(checksFailing)},
		reflect.TypeFor[flowStageName](): {
			flowview.StageDesign, flowview.StageSpec, flowview.StageDraft, flowview.StageRun, flowview.StageRecord},
		reflect.TypeFor[flowStageState](): {
			flowview.StateDone, flowview.StateNow, flowview.StateTodo, flowview.StateUnreadable},
		reflect.TypeFor[flowStatus](): {
			flowview.StatusDesigning, flowview.StatusDesigned, flowview.StatusDrafting, flowview.StatusDrafted,
			flowview.StatusSupervised, flowrun.Running, flowrun.Parked, flowrun.Done, flowrun.Stopped,
			flowrun.Cancelled, string(flowLost), ""},
		reflect.TypeFor[flowCell](): {
			flowview.CellTodo, flowview.CellRunning, flowview.CellPassed, flowview.CellFailed, flowview.CellAsks,
			flowview.CellSkipped},
		reflect.TypeFor[flowStepKind](): {
			flowrun.KindPremise, flowrun.KindDev, flowrun.KindGates, flowrun.KindReview, flowrun.KindFix, flowrun.KindDocs},
		reflect.TypeFor[flowQuestionKind](): {
			flowrun.KindNeedsContext, flowrun.KindBlocked, flowrun.KindDesignFinding, flowrun.KindPremiseDesign,
			flowrun.KindUnresolved, flowrun.KindGateFailure, flowrun.KindAgentFailed, flowrun.KindTreeMoved,
			flowrun.KindDocsStrayed, flowrun.KindUsageLimit, flowrun.KindLane, ""},
	}
	got := unions()
	require.Len(t, want, len(got))
	for typ, values := range want {
		u, ok := got[typ]
		if assert.True(t, ok, typ.String()) {
			assert.Equal(t, values, u.values, u.name)
		}
	}
}

func TestUnions_EveryFixedValueFieldIsTyped(t *testing.T) {
	for _, r := range []struct {
		of    any
		field string
		want  reflect.Type
	}{
		{sessionJSON{}, "Group", reflect.TypeFor[sessionGroup]()},
		{sessionJSON{}, "Status", reflect.TypeFor[sessionStatus]()},
		{sessionJSON{}, "Verdict", reflect.TypeFor[stopVerdict]()},
		{prJSON{}, "State", reflect.TypeFor[prState]()},
		{prJSON{}, "Checks", reflect.TypeFor[prChecks]()},
		{agentPageJSON{}, "State", reflect.TypeFor[agentPageState]()},
		{turnJSON{}, "From", reflect.TypeFor[turnFrom]()},
		{itemJSON{}, "Kind", reflect.TypeFor[itemKind]()},
		{stepJSON{}, "State", reflect.TypeFor[stepState]()},
		{fileJSON{}, "Status", reflect.TypeFor[fileStatus]()},
		{fileJSON{}, "Omitted", reflect.TypeFor[fileOmitted]()},
		{jobJSON{}, "State", reflect.TypeFor[jobState]()},
		{jobJSON{}, "Control", reflect.TypeFor[jobControlKind]()},
		{askJSON{}, "Kind", reflect.TypeFor[askKind]()},
		{flowSummaryJSON{}, "Stage", reflect.TypeFor[flowStageName]()},
		{flowSummaryJSON{}, "Status", reflect.TypeFor[flowStatus]()},
		{flowSummaryJSON{}, "Asks", reflect.TypeFor[flowQuestionKind]()},
		{flowStageJSON{}, "Name", reflect.TypeFor[flowStageName]()},
		{flowStageJSON{}, "State", reflect.TypeFor[flowStageState]()},
		{flowStepJSON{}, "Kind", reflect.TypeFor[flowStepKind]()},
		{flowQuestionJSON{}, "Kind", reflect.TypeFor[flowQuestionKind]()},
		{flowStoryJSON{}, "Cells", reflect.TypeFor[map[string]flowCell]()},
	} {
		f, ok := reflect.TypeOf(r.of).FieldByName(r.field)
		if assert.True(t, ok, r.field) {
			assert.Equal(t, r.want, f.Type, "%T.%s", r.of, r.field)
		}
		typ := r.want
		if typ.Kind() == reflect.Map {
			typ = typ.Elem()
		}
		_, listed := unions()[typ]
		assert.True(t, listed, "%T.%s", r.of, r.field)
	}
}
