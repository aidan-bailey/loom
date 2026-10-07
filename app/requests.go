package app

import (
	"errors"
	"fmt"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/script"
	"github.com/aidan-bailey/loom/session"

	tea "charm.land/bubbletea/v2"
)

// pendingReq is what the TUI asked for with a ReqID, so its Reply can find
// the flow waiting on it. Exactly one field is set.
type pendingReq struct {
	create *pendingCreate // a creation flow's Create (drafts.go)
	send   *pendingSend   // a prompt send (sendPrompt)
	op     *pendingOp     // a lifecycle request the user made (opReq)
	script *pendingScript // a Lua call waiting to resume (app_scripts.go)
	issue  *pendingIssue  // an issue fetch (state_issue_picker.go)
}

// newReq records p and returns the ReqID to send with its request.
func (m *home) newReq(p pendingReq) core.ReqID {
	if m.pending == nil {
		m.pending = make(map[core.ReqID]pendingReq)
	}
	m.nextReq++
	m.pending[m.nextReq] = p
	return m.nextReq
}

// handleReply routes a Reply to the flow that asked for it.
func (m *home) handleReply(r core.Reply) tea.Cmd {
	p, ok := m.pending[r.Req]
	if !ok {
		log.For("app").Warn("reply.unexpected", "req", uint64(r.Req))
		return nil
	}
	delete(m.pending, r.Req)
	switch {
	case p.create != nil:
		return m.createReplied(p.create, r)
	case p.send != nil:
		return m.sendReplied(p.send, r)
	case p.op != nil:
		return m.refusal(p.op.op+" "+p.op.title, r)
	case p.script != nil:
		return m.scriptReplied(p.script, r)
	case p.issue != nil && p.issue.picked != nil:
		msg := *p.issue.picked
		msg.issue, msg.err = r.Issue, r.Err
		_, cmd := m.handleIssuePicked(msg)
		return cmd
	case p.issue != nil && p.issue.expand != nil:
		msg := *p.issue.expand
		msg.issue, msg.err = r.Issue, r.Err
		_, cmd := m.handleIssueExpanded(msg)
		return cmd
	}
	return nil
}

// pendingOp is a lifecycle request the user made (a kill, a pause, a
// resume, …), waiting for its Reply only to show a refusal: time passes
// between the key's gate and the request whenever a dialog is open (a
// confirmation, the merge picker, Launch Options), and a request the model
// refuses meanwhile is reported nowhere else.
type pendingOp struct {
	op    string // as the model names it in its refusals: "kill", "merge into", …
	title string
}

// opReq returns the ReqID for a lifecycle request op on the session
// titled title, so a refusal reaches the user (refusal).
func (m *home) opReq(op, title string) core.ReqID {
	return m.newReq(pendingReq{op: &pendingOp{op: op, title: title}})
}

// refusal shows r's error when the model refused the request
// (core.ErrRefused), and nothing otherwise: a failure of the request's job
// is the model's to show, in its own notice, never twice. A gone session's
// refusal names no request, so what (e.g. "kill x") goes in front of it.
func (m *home) refusal(what string, r core.Reply) tea.Cmd {
	if !errors.Is(r.Err, core.ErrRefused) {
		return nil
	}
	if errors.Is(r.Err, core.ErrNoSession) {
		return m.handleError(fmt.Errorf("%s: %w", what, r.Err))
	}
	return m.handleError(r.Err)
}

// pendingSend is a prompt send waiting for its Reply. While it is in flight
// the TUI holds input to its instance (decision 12): keys typed into inline
// attach, or a second send, would land between the paste and its Enter.
type pendingSend struct {
	id    core.InstanceID
	title string
}

// sendPrompt sends text to v's agent through the model, holding further
// input to v until the send's Reply.
func (m *home) sendPrompt(v *core.InstanceView, text string) {
	m.holdInput(v.ID)
	m.core.SendPrompt(v.ID, text, m.newReq(pendingReq{send: &pendingSend{id: v.ID, title: v.Title}}))
}

// holdInput holds input to the instance id while a prompt send to it is
// in flight (sendingTo); the send's Reply releases it.
func (m *home) holdInput(id core.InstanceID) {
	if m.sending == nil {
		m.sending = make(map[core.InstanceID]bool)
	}
	m.sending[id] = true
}

// sendReplied releases the hold. The model has already shown a failed send
// as a notice ("prompt not sent to …"); a send it refused (the session
// gone, or paused since the text was typed) is shown here (refusal).
func (m *home) sendReplied(p *pendingSend, r core.Reply) tea.Cmd {
	delete(m.sending, p.id)
	return m.refusal("prompt not sent to "+p.title, r)
}

// sendingTo reports whether a prompt send to v is in flight; when it is,
// it says so on the info line.
func (m *home) sendingTo(v *core.InstanceView) bool {
	if v == nil || !m.sending[v.ID] {
		return false
	}
	m.errBox.SetInfo(fmt.Sprintf("still sending the last prompt to %s", v.Title))
	return true
}

// pendingScript is a Lua call waiting on the model: its coroutine resumes
// when the Reply lands. held is the instance a send_prompt holds input to
// until then (holdInput), 0 for the other calls. killed is a kill's
// session as it was when the kill was asked for, nil for the other calls
// (scriptReplied).
type pendingScript struct {
	intent script.IntentID
	trace  string
	op     string
	held   core.InstanceID
	killed *core.InstanceView
}

// scriptReplied resumes the Lua call a Reply answers. A failure resumes it
// with the error the method raises (scriptError). A success resumes it
// with the session's row as the model left it: new_instance returns it,
// and a lifecycle call's instance takes it as its view (the method
// returns nothing), so inst:status() after inst:pause() reads Paused, as
// it did when the method changed the instance itself. A kill leaves no
// row: its instance keeps the view it had when the kill was asked for,
// marked Deleting, the last status the session had. A Reply's Notice
// changes none of this: the model has shown it already. A send_prompt's
// hold is released first.
func (m *home) scriptReplied(p *pendingScript, r core.Reply) tea.Cmd {
	if p.held != 0 {
		delete(m.sending, p.held)
	}
	var v script.ResumeValue
	switch {
	case r.Err != nil:
		v.Err = scriptError(p.op, r.Err)
	default:
		if row, _ := m.viewByID(r.ID); row != nil {
			v.Instance = row
		} else if cv, ok := m.core.View(r.ID); ok {
			v.Instance = &cv
		} else if p.killed != nil {
			gone := *p.killed
			gone.Status = session.Deleting
			v.Instance = &gone
		}
	}
	return func() tea.Msg { return scriptResumeMsg{id: p.intent, trace: p.trace, value: v} }
}

// scriptError is the message a failed Lua call raises. A refusal raises
// its own, which names the request and the session ("kill x: not allowed
// on a workspace terminal"); anything else is "<op>: <err>", as the
// methods raised before the model ran them: a failed job's error, a gone
// session's (ErrNoSession names neither), and every ctx:new_instance
// error ("new_instance: …").
func scriptError(op string, err error) string {
	if op != "new_instance" && errors.Is(err, core.ErrRefused) && !errors.Is(err, core.ErrNoSession) {
		return err.Error()
	}
	return fmt.Sprintf("%s: %s", op, err)
}
