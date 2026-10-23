package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session/git"
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
// has blocks and truncated, a steps item total, failed and steps, a note
// text and error; each has at.
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

type stepJSON struct {
	Tool    string `json:"tool"`
	Summary string `json:"summary"`
	At      string `json:"at"`
	State   string `json:"state"`
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
					ij.Steps = append(ij.Steps, stepJSON{Tool: s.Tool, Summary: s.Summary, At: stamp(s.At), State: s.State})
				}
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

// readSession is the session a read route names, answered 404 when the
// path names none or a workspace terminal, which is no session of the
// app's.
func (l *Layer) readSession(w http.ResponseWriter, r *http.Request, a *actionLog) (core.InstanceID, bool) {
	id, ok := l.sessionFromPath(w, r, *a)
	if !ok {
		return 0, false
	}
	a.sessionID = id
	if v, _ := l.client.View(id); v.IsWorkspaceTerminal {
		l.refuse(w, *a, http.StatusNotFound, "no such session")
		return 0, false
	}
	return id, true
}

// read makes the read request send names and waits for its Reply, then
// stops tracking the request (reqs.drop). It answers a failure itself (the
// model's refusal with its status, 504 when the model did not answer
// within replyWait) and otherwise returns the Reply with true.
func (l *Layer) read(w http.ResponseWriter, r *http.Request, a actionLog, send func(req core.ReqID)) (core.Reply, bool) {
	req := l.reqs.next()
	defer l.reqs.drop(req)
	send(req)
	reply, _, done := l.reqs.wait(r.Context(), req, a.device)
	switch {
	case !done:
		l.refuse(w, a, http.StatusGatewayTimeout, "loom did not answer in time")
	case reply.Err != nil:
		l.refuse(w, a, modelStatus(reply.Err), reply.Err.Error())
	default:
		return reply, true
	}
	return core.Reply{}, false
}

// readTurns answers GET /api/v1/sessions/{id}/turns: a page of the
// session's conversation, as the query's conversation, before and after
// ask.
func (l *Layer) readTurns(w http.ResponseWriter, r *http.Request, device string) {
	a := actionLog{device: device, name: "turns"}
	id, ok := l.readSession(w, r, &a)
	if !ok {
		return
	}
	q, err := turnsQuery(r)
	if err != nil {
		l.refuse(w, a, http.StatusBadRequest, err.Error())
		return
	}
	reply, ok := l.read(w, r, a, func(req core.ReqID) { l.client.ReadTurns(id, q, req) })
	if !ok {
		return
	}
	l.record(a, http.StatusOK, "ok")
	writeJSON(w, http.StatusOK, turnsOf(reply.Turns))
}

// turnsQuery reads the turns route's query: before and after are byte
// offsets, and at most one of them is set.
func turnsQuery(r *http.Request) (transcript.Query, error) {
	v := r.URL.Query()
	q := transcript.Query{Conversation: v.Get("conversation")}
	for _, c := range []struct {
		name string
		to   *int64
	}{{"before", &q.Before}, {"after", &q.After}} {
		s := v.Get(c.name)
		if s == "" {
			continue
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n < 0 {
			return transcript.Query{}, fmt.Errorf("%s %q: want a byte offset", c.name, s)
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
func (l *Layer) readDiff(w http.ResponseWriter, r *http.Request, device string) {
	a := actionLog{device: device, name: "diff"}
	id, ok := l.readSession(w, r, &a)
	if !ok {
		return
	}
	reply, ok := l.read(w, r, a, func(req core.ReqID) { l.client.ReadDiff(id, req) })
	if !ok {
		return
	}
	l.record(a, http.StatusOK, "ok")
	writeJSON(w, http.StatusOK, patchOf(reply.Diff))
}
