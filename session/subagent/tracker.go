package subagent

import (
	"cmp"
	"slices"
)

// kind distinguishes plain subagents, which end when they stop, from
// agent-team teammates, which go idle and can be re-tasked. kindUnknown
// covers the window before an agent's metadata sidecar is read.
type kind int

const (
	kindUnknown kind = iota
	kindPlain
	kindTeammate
)

// state is an agent's position in its lifecycle.
type state int

const (
	stateWorking state = iota
	stateIdle
	// stateStopping: a SubagentStop with no TeammateIdle after it yet.
	// Teammate shutdown produces exactly this, so reconciliation removes
	// Stopping teammates first. An agent of unknown kind also waits here
	// until its metadata says whether it has ended. Shown as idle.
	stateStopping
)

// statusRunning is the background_tasks status of a live task.
const statusRunning = "running"

// View is one agent as the UI renders it.
type View struct {
	Name        string
	Description string
	Idle        bool
}

type agent struct {
	id          string
	name        string // Meta.DisplayName once known, else the event's agent_type
	description string
	kind        kind
	state       state
	metaPath    string
	hasMeta     bool
	spawnSeq    int
	stoppedSeq  int
}

// Tracker derives the live agent set from hook events. It is not safe for
// concurrent use; session.Instance guards it with its own mutex.
type Tracker struct {
	agents map[string]*agent
	seq    int
}

// NewTracker returns an empty tracker.
func NewTracker() *Tracker { return &Tracker{agents: map[string]*agent{}} }

// Reset forgets every agent. Called at a new launch and before a replay
// rebuilds state from the full event log.
func (t *Tracker) Reset() { t.agents = map[string]*agent{} }

// Apply folds events, in order, into the tracker. meta holds sidecars read
// since the last call, keyed by agent ID. They are applied to known agents
// first, so an agent that started and stopped within one batch already
// has its kind when its SubagentStop is processed.
func (t *Tracker) Apply(events []Event, meta map[string]Meta) {
	for id, m := range meta {
		if a, ok := t.agents[id]; ok {
			t.applyMeta(a, m)
		}
	}
	for _, ev := range events {
		t.seq++
		switch ev.Name {
		case EventSubagentStart:
			t.start(ev, meta)
		case EventSubagentStop:
			t.stop(ev.AgentID)
		case EventTeammateIdle:
			t.idle(ev.TeammateName)
		case EventStop:
			// Only the parent's Stop reconciles: no foreground subagent can
			// be running when the parent's turn ends, while a SubagentStop
			// list may omit a parallel foreground agent.
			if ev.HasTasks {
				t.reconcile(ev.Tasks)
			}
		case EventSessionEnd:
			t.Reset()
		}
	}
}

// applyMeta records a sidecar. A plain agent whose SubagentStop arrived
// before its sidecar has ended, so it is removed here.
func (t *Tracker) applyMeta(a *agent, m Meta) {
	a.hasMeta = true
	if name := m.DisplayName(); name != "" {
		a.name = name
	}
	a.description = m.Description
	if m.IsTeammate() {
		a.kind = kindTeammate
	} else {
		a.kind = kindPlain
	}
	if a.kind == kindPlain && a.state == stateStopping {
		delete(t.agents, a.id)
	}
}

// start creates or re-tasks an agent. Rows are only ever created here, so
// Claude's internal helpers, which stop without starting, never appear.
func (t *Tracker) start(ev Event, meta map[string]Meta) {
	if ev.AgentID == "" {
		return
	}
	a, ok := t.agents[ev.AgentID]
	if !ok {
		a = &agent{
			id:       ev.AgentID,
			name:     ev.AgentType,
			metaPath: MetaPath(ev.TranscriptPath, ev.AgentID),
			spawnSeq: t.seq,
		}
		t.agents[ev.AgentID] = a
		if m, found := meta[ev.AgentID]; found {
			t.applyMeta(a, m)
		}
	}
	a.state = stateWorking
}

func (t *Tracker) stop(id string) {
	a, ok := t.agents[id]
	if !ok {
		return
	}
	if a.kind == kindPlain {
		delete(t.agents, id)
		return
	}
	a.state = stateStopping
	a.stoppedSeq = t.seq
}

// idle marks the named teammate idle. A TeammateIdle proves the agent is a
// teammate even before its sidecar is read.
func (t *Tracker) idle(name string) {
	if name == "" {
		return
	}
	for _, a := range t.agents {
		if a.name == name && a.kind != kindPlain {
			a.kind = kindTeammate
			a.state = stateIdle
		}
	}
}

// reconcile applies a parent Stop's list of running tasks. Plain subagents
// are matched by ID. Teammates are listed under task IDs that cannot be
// matched, so only their count is used: extras are removed, Stopping ones
// first, then the most recently stopped. Agents of unknown kind are left
// alone; they are hidden and only exist until their sidecar is read.
func (t *Tracker) reconcile(tasks []Task) {
	liveSubagents := map[string]bool{}
	liveTeammates := 0
	for _, task := range tasks {
		if task.Status != statusRunning {
			continue
		}
		switch task.Type {
		case "subagent":
			liveSubagents[task.ID] = true
		case "teammate":
			liveTeammates++
		}
	}

	var teammates []*agent
	for id, a := range t.agents {
		switch a.kind {
		case kindPlain:
			if !liveSubagents[id] {
				delete(t.agents, id)
			}
		case kindTeammate:
			teammates = append(teammates, a)
		}
	}

	if excess := len(teammates) - liveTeammates; excess > 0 {
		slices.SortFunc(teammates, func(x, y *agent) int {
			xs, ys := x.state == stateStopping, y.state == stateStopping
			if xs != ys {
				if xs {
					return -1
				}
				return 1
			}
			if c := cmp.Compare(y.stoppedSeq, x.stoppedSeq); c != 0 {
				return c
			}
			return cmp.Compare(x.spawnSeq, y.spawnSeq)
		})
		for _, a := range teammates[:excess] {
			delete(t.agents, a.id)
		}
	}

	for _, a := range t.agents {
		if a.kind == kindTeammate && a.state == stateStopping {
			a.state = stateIdle
		}
	}
}

// Visible returns the agents to render: those with a sidecar, working
// first, then idle, each group in spawn order. Never nil.
func (t *Tracker) Visible() []View {
	shown := make([]*agent, 0, len(t.agents))
	for _, a := range t.agents {
		if a.hasMeta {
			shown = append(shown, a)
		}
	}
	slices.SortFunc(shown, func(x, y *agent) int {
		xw, yw := x.state == stateWorking, y.state == stateWorking
		if xw != yw {
			if xw {
				return -1
			}
			return 1
		}
		return cmp.Compare(x.spawnSeq, y.spawnSeq)
	})
	out := make([]View, len(shown))
	for i, a := range shown {
		out[i] = View{Name: a.name, Description: a.description, Idle: a.state != stateWorking}
	}
	return out
}

// MissingMeta lists agents still waiting for their sidecar, sorted by ID,
// so the next scan can retry them.
func (t *Tracker) MissingMeta() []MetaRef {
	var refs []MetaRef
	for _, a := range t.agents {
		if !a.hasMeta && a.metaPath != "" {
			refs = append(refs, MetaRef{AgentID: a.id, Path: a.metaPath})
		}
	}
	slices.SortFunc(refs, func(x, y MetaRef) int { return cmp.Compare(x.AgentID, y.AgentID) })
	return refs
}
