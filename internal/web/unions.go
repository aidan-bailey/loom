package web

import "reflect"

// The fields of the JSON the app reads that hold one of a fixed set of
// words are typed strings here, and unions lists each type with its TS
// union's name and its values, so the generator (contract.go) can write the
// union. encoding/json writes a typed string as the plain string: the wire
// is unchanged.

// union is one fixed set of words: the name of its TS union and its values,
// in the order the app lists them.
type union struct {
	name   string
	values []string
}

// sessionGroup types sessionJSON.Group.
type sessionGroup string

// sessionStatus types sessionJSON.Status. A status this build does not know
// is sent as Status.String() writes it, outside the union.
type sessionStatus string

// stopVerdict types sessionJSON.Verdict.
type stopVerdict string

// jobState types jobJSON.State.
type jobState string

// jobControlKind types jobJSON.Control.
type jobControlKind string

// turnFrom types turnJSON.From.
type turnFrom string

// itemKind types itemJSON.Kind.
type itemKind string

// stepState types stepJSON.State.
type stepState string

// agentPageState types agentPageJSON.State.
type agentPageState string

// fileStatus types fileJSON.Status.
type fileStatus string

// fileOmitted types fileJSON.Omitted.
type fileOmitted string

// askKind types askJSON.Kind.
type askKind string

// prState types prJSON.State.
type prState string

// prChecks types prJSON.Checks.
type prChecks string

// flowStageName types flowSummaryJSON.Stage and flowStageJSON.Name.
type flowStageName string

// flowStageState types flowStageJSON.State.
type flowStageState string

// flowStatus types flowSummaryJSON.Status.
type flowStatus string

// flowCell types the values of flowStoryJSON.Cells.
type flowCell string

// flowStepKind types flowStepJSON.Kind.
type flowStepKind string

// flowQuestionKind types flowQuestionJSON.Kind and flowSummaryJSON.Asks.
type flowQuestionKind string

// The words of prJSON the web layer writes itself (prOf).
const (
	prDraft  prState = "draft"
	prOpen   prState = "open"
	prMerged prState = "merged"
	prClosed prState = "closed"

	checksNone    prChecks = "none"
	checksPending prChecks = "pending"
	checksPassing prChecks = "passing"
	checksFailing prChecks = "failing"
)

// flowLost is the status flowSummaryOf writes for a run that says it runs
// and whose engine has ended.
const flowLost flowStatus = "lost"

// unions lists each typed string type of the JSON with its union.
func unions() map[reflect.Type]union {
	return map[reflect.Type]union{
		reflect.TypeFor[sessionGroup]():   {"SessionGroup", []string{"needs_you", "your_turn", "working", "paused"}},
		reflect.TypeFor[sessionStatus]():  {"SessionStatus", []string{"Running", "Ready", "Loading", "Paused", "Prompting", "Deleting", "Recoverable"}},
		reflect.TypeFor[stopVerdict]():    {"StopVerdict", []string{"needs_you", "done", "waiting", "stopped"}},
		reflect.TypeFor[jobState]():       {"JobState", []string{"held", "queued", "running", "done", "failed", "cancelled", "lost"}},
		reflect.TypeFor[jobControlKind](): {"JobControl", []string{"pause", "resume", ""}},
		reflect.TypeFor[turnFrom]():       {"TurnFrom", []string{"you", "plugin", "peer", "task", "brief"}},
		reflect.TypeFor[itemKind]():       {"ItemKind", []string{"text", "thinking", "steps", "compacted", "note"}},
		reflect.TypeFor[stepState]():      {"StepState", []string{"pending", "done", "failed", "interrupted"}},
		reflect.TypeFor[agentPageState](): {"AgentPageState", []string{"running", "finished", "unknown"}},
		reflect.TypeFor[fileStatus]():     {"FileStatus", []string{"A", "M", "D", "R", "C"}},
		reflect.TypeFor[fileOmitted]():    {"FileOmitted", []string{"", "lines", "budget"}},
		reflect.TypeFor[askKind]():        {"AskKind", []string{"question", "permission"}},
		reflect.TypeFor[prState]():        {"PrState", []string{"draft", "open", "merged", "closed"}},
		reflect.TypeFor[prChecks]():       {"PrChecks", []string{"none", "pending", "passing", "failing"}},
		reflect.TypeFor[flowStageName]():  {"FlowStageName", []string{"design", "spec", "draft", "run", "record"}},
		reflect.TypeFor[flowStageState](): {"FlowStageState", []string{"done", "now", "todo", "unreadable"}},
		reflect.TypeFor[flowStatus](): {"FlowStatus", []string{
			"designing", "designed", "drafting", "drafted", "supervised", "running", "parked", "done", "stopped", "cancelled", "lost", ""}},
		reflect.TypeFor[flowCell]():     {"FlowCell", []string{"todo", "running", "passed", "failed", "asks", "skipped"}},
		reflect.TypeFor[flowStepKind](): {"FlowStepKind", []string{"premise", "dev", "gates", "review", "fix", "docs"}},
		reflect.TypeFor[flowQuestionKind](): {"FlowQuestionKind", []string{
			"needs-context", "blocked-decision", "design-finding", "premise-design", "unresolved-findings",
			"gate-failure", "agent-failed", "tree-moved", "docs-strayed", "usage-limit", "lane", ""}},
	}
}
