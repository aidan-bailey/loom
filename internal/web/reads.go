package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session/git"
	"github.com/aidan-bailey/loom/session/subagent"
	"github.com/aidan-bailey/loom/session/transcript"
)

// The read routes: a session's conversation (GET …/turns) and its
// worktree's diff (GET …/diff). Each is a Core request whose Reply carries
// the data; none of the data is logged.

// turnsJSON is a page of a session's conversation (transcript.Page) as the
// app reads it. A text item crosses as its tree, never as its text.
type turnsJSON struct {
	Conversation string     `json:"conversation"`
	Turns        []turnJSON `json:"turns"`
	// Earlier is the before of the page before this one: null at the start
	// of the file and on a refresh's page.
	Earlier *int64 `json:"earlier"`
	Reset   bool   `json:"reset"`
	Skipped int    `json:"skipped"`
	// Agent describes the subagent whose turns the page holds; left out
	// unless the query named one (?agent=).
	Agent *agentPageJSON `json:"agent,omitempty"`
}

// agentPageJSON is a subagent page's agent (transcript.AgentInfo). State
// is one of running, finished or unknown; Missing says the agent's
// transcript file is not there, and Started is "" without a first record.
type agentPageJSON struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Teammate    bool   `json:"teammate"`
	State       string `json:"state"`
	Started     string `json:"started"`
	Missing     bool   `json:"missing"`
}

type turnJSON struct {
	ID      int64      `json:"id"`
	At      string     `json:"at"`
	From    string     `json:"from"`
	Name    string     `json:"name"`
	Text    string     `json:"text"`
	Cut     bool       `json:"cut"`
	Command bool       `json:"command"`
	Partial bool       `json:"partial"`
	Items   []itemJSON `json:"items"`
}

// itemJSON is one item of a turn. Its fields follow its kind: a text item
// has blocks and truncated, a thinking item text and truncated (plain text,
// never a tree), a steps item total, failed and steps, a note text and
// error; each has at.
type itemJSON struct {
	Kind      string     `json:"kind"`
	At        string     `json:"at"`
	Blocks    []*mdNode  `json:"blocks,omitempty"`
	Truncated bool       `json:"truncated,omitempty"`
	Text      string     `json:"text,omitempty"`
	Error     bool       `json:"error,omitempty"`
	Total     int        `json:"total,omitempty"`
	Failed    int        `json:"failed,omitempty"`
	Steps     []stepJSON `json:"steps,omitempty"`
}

// stepJSON is one tool step. Agent is the subagent an Agent/Task step
// launched, omitted when none was found; Finished says it has ended.
type stepJSON struct {
	Tool     string `json:"tool"`
	Summary  string `json:"summary"`
	At       string `json:"at"`
	State    string `json:"state"`
	Agent    string `json:"agent,omitempty"`
	Finished bool   `json:"finished"`
}

// patchJSON is a session's diff (git.Patch) as the app reads it.
type patchJSON struct {
	Base  string     `json:"base"`
	Files []fileJSON `json:"files"`
}

type fileJSON struct {
	Path    string     `json:"path"`
	OldPath string     `json:"old_path"`
	Status  string     `json:"status"`
	Binary  bool       `json:"binary"`
	Added   int        `json:"added"`
	Removed int        `json:"removed"`
	Hunks   []hunkJSON `json:"hunks"`
	Omitted string     `json:"omitted"`
}

type hunkJSON struct {
	Header string   `json:"header"`
	Lines  []string `json:"lines"`
	Cut    []int    `json:"cut"`
}

// stamp is t in RFC 3339, in UTC, and "" for the zero time.
func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// turnsOf is a page as the app reads it, each text item parsed into its
// tree (buildTree).
func turnsOf(p transcript.Page) turnsJSON {
	out := turnsJSON{Conversation: p.Conversation, Turns: make([]turnJSON, 0, len(p.Turns)), Reset: p.Reset, Skipped: p.Skipped}
	if p.Earlier > 0 {
		earlier := p.Earlier
		out.Earlier = &earlier
	}
	if a := p.Agent; a != nil {
		out.Agent = &agentPageJSON{
			ID: a.ID, Type: a.Type, Description: a.Description, Teammate: a.Teammate,
			State: a.State, Started: stamp(a.Started), Missing: a.Missing,
		}
	}
	for _, t := range p.Turns {
		tj := turnJSON{
			ID: t.ID, At: stamp(t.At), From: t.From, Name: t.Name, Text: t.Text,
			Cut: t.Cut, Command: t.Command, Partial: t.Partial, Items: make([]itemJSON, 0, len(t.Items)),
		}
		for _, it := range t.Items {
			ij := itemJSON{Kind: it.Kind, At: stamp(it.At)}
			switch it.Kind {
			case transcript.KindText:
				var cut bool
				ij.Blocks, cut = buildTree(it.Text)
				ij.Truncated = cut || it.Cut
			case transcript.KindSteps:
				ij.Total, ij.Failed = it.Total, it.Failed
				ij.Steps = make([]stepJSON, 0, len(it.Steps))
				for _, s := range it.Steps {
					ij.Steps = append(ij.Steps, stepJSON{
						Tool: s.Tool, Summary: s.Summary, At: stamp(s.At), State: s.State,
						Agent: s.Agent, Finished: s.Finished,
					})
				}
			case transcript.KindThinking:
				ij.Text, ij.Truncated = it.Text, it.Cut
			case transcript.KindNote:
				ij.Text, ij.Error = it.Text, it.Error
			}
			tj.Items = append(tj.Items, ij)
		}
		out.Turns = append(out.Turns, tj)
	}
	return out
}

// patchOf is a patch as the app reads it, with every list non-null.
func patchOf(p git.Patch) patchJSON {
	out := patchJSON{Base: p.Base, Files: make([]fileJSON, 0, len(p.Files))}
	for _, f := range p.Files {
		fj := fileJSON{
			Path: f.Path, OldPath: f.OldPath, Status: f.Status, Binary: f.Binary,
			Added: f.Added, Removed: f.Removed, Omitted: f.Omitted, Hunks: make([]hunkJSON, 0, len(f.Hunks)),
		}
		for _, h := range f.Hunks {
			hj := hunkJSON{Header: h.Header, Lines: h.Lines, Cut: h.Cut}
			if hj.Lines == nil {
				hj.Lines = []string{}
			}
			if hj.Cut == nil {
				hj.Cut = []int{}
			}
			fj.Hunks = append(fj.Hunks, hj)
		}
		out.Files = append(out.Files, fj)
	}
	return out
}

// isWorkspaceTerminal reports whether the session is a workspace terminal,
// which is no session of the app's.
func (l *Layer) isWorkspaceTerminal(id core.InstanceID) bool {
	v, _ := l.client.View(id)
	return v.IsWorkspaceTerminal
}

// readSession is the session a read route names, answered 404 when the
// path names none or a workspace terminal, which is no session of the
// app's.
func (l *Layer) readSession(w http.ResponseWriter, r *http.Request, a *actionLog) (core.InstanceID, bool) {
	id, ok := l.sessionFromPath(w, r, *a)
	if !ok {
		return 0, false
	}
	a.sessionID = id
	if l.isWorkspaceTerminal(id) {
		l.refuse(w, *a, http.StatusNotFound, "no such session")
		return 0, false
	}
	return id, true
}

// readAnswering makes a read request (readReply) and answers its failures with fail: the model's
// refusal with its status, 504 when the model did not answer within
// replyWait.
func (l *Layer) readAnswering(w http.ResponseWriter, r *http.Request, a actionLog, send func(req core.ReqID), fail func(http.ResponseWriter, actionLog, int, string)) (core.Reply, bool) {
	reply, err := l.readReply(r.Context(), a.device, send)
	if err != nil {
		status, msg := errStatus(err)
		fail(w, a, status, msg)
		return core.Reply{}, false
	}
	return reply, true
}

// readReply makes the read request send names, waits for its Reply and stops
// tracking the request (reqs.drop). Its error is the model's own, or a
// *statusError 504 when replyWait ran out.
func (l *Layer) readReply(ctx context.Context, device string, send func(req core.ReqID)) (core.Reply, error) {
	req := l.reqs.next()
	defer l.reqs.drop(req)
	send(req)
	reply, _, done := l.reqs.wait(ctx, req, device)
	switch {
	case !done:
		return core.Reply{}, &statusError{http.StatusGatewayTimeout, "loom did not answer in time"}
	case reply.Err != nil:
		return core.Reply{}, reply.Err
	}
	return reply, nil
}

// turnsParams is GET /api/v1/sessions/{id}/turns's query.
type turnsParams struct {
	Conversation string `query:"conversation"`
	Before       string `query:"before"`
	After        string `query:"after"`
	Agent        string `query:"agent"`
}

// readTurns answers GET /api/v1/sessions/{id}/turns: a page of the
// session's conversation, as the query's conversation, before and after
// ask.
func (l *Layer) readTurns(c *call, p turnsParams) (turnsJSON, error) {
	id, err := c.readSession()
	if err != nil {
		return turnsJSON{}, err
	}
	q, err := turnsQuery(p)
	if err != nil {
		return turnsJSON{}, &statusError{http.StatusBadRequest, err.Error()}
	}
	reply, err := c.read(func(req core.ReqID) { l.client.ReadTurns(id, q, req) })
	if err != nil {
		return turnsJSON{}, err
	}
	return turnsOf(reply.Turns), nil
}

// turnsQuery reads the turns route's query: before and after are byte
// offsets, and at most one of them is set; agent, when not empty, is a
// subagent's id (subagent.ValidAgentID), whose own turns are read in
// place of the conversation's.
func turnsQuery(p turnsParams) (transcript.Query, error) {
	q := transcript.Query{Conversation: p.Conversation, Agent: p.Agent}
	if q.Agent != "" && !subagent.ValidAgentID(q.Agent) {
		return transcript.Query{}, fmt.Errorf("agent %q: not an agent id", q.Agent)
	}
	for _, c := range []struct {
		name  string
		value string
		to    *int64
	}{{"before", p.Before, &q.Before}, {"after", p.After, &q.After}} {
		if c.value == "" {
			continue
		}
		n, err := strconv.ParseInt(c.value, 10, 64)
		if err != nil || n < 0 {
			return transcript.Query{}, fmt.Errorf("%s %q: want a byte offset", c.name, c.value)
		}
		*c.to = n
	}
	if q.Before > 0 && q.After > 0 {
		return transcript.Query{}, errors.New("before and after: give at most one")
	}
	return q, nil
}

// readDiff answers GET /api/v1/sessions/{id}/diff: the session's worktree
// diff against its base commit, file by file.
func (l *Layer) readDiff(c *call, _ struct{}) (patchJSON, error) {
	id, err := c.readSession()
	if err != nil {
		return patchJSON{}, err
	}
	reply, err := c.read(func(req core.ReqID) { l.client.ReadDiff(id, req) })
	if err != nil {
		return patchJSON{}, err
	}
	return patchOf(reply.Diff), nil
}
