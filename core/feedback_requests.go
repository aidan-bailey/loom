package core

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/feedback"
	"github.com/aidan-bailey/loom/session/launch"
)

// feedbackCall is the words a `loom feedback add` Bash call holds, which
// FileFeedback matches the mod's reported calls by (attribution).
var feedbackCall = []string{"feedback", "add"}

// FileFeedback files a note from the session note.Launch resolves to (spec
// §3): it checks the note (feedback.ValidateNote), refuses a launch no
// served session holds, stamps who filed it from the model's own state (the
// session, the subagent and the model from the mod's report of the call,
// the build), saves, and asks the grouper. The Reply carries the note's ID;
// a failed save refuses it and keeps nothing.
func (m *Model) FileFeedback(note FeedbackNote, req ReqID) {
	f := m.feedback
	switch {
	case f.dir == "" || f.loadErr != nil:
		m.refuse(req, 0, errors.New("the feedback inbox is not loaded, so notes are refused"))
		return
	case note.Launch == "":
		m.refuse(req, 0, errors.New("no launch: loom feedback add runs inside a session loom launched ($LOOM_LAUNCH)"))
		return
	}
	if err := feedback.ValidateNote(note.Kind, note.Title, note.Text); err != nil {
		m.refuse(req, 0, err)
		return
	}
	inst := m.instByLaunch(note.Launch)
	if inst == nil {
		m.refuse(req, 0, errors.New("this launch is no session the loom daemon serves, so the note can't say who filed it"))
		return
	}
	from := feedback.Session{ID: uint64(m.idOf(inst)), Title: inst.Title, Branch: inst.GetBranch()}
	if ws := m.holding(inst); ws != nil {
		from.Workspace = ws.Label()
	}
	from.ClaudeSession, from.Transcript = inst.ClaudeSession()
	if call := m.attribution(note.Launch, feedbackCall); call != nil {
		from.Agent, from.Model = call.Agent, call.Model
	}
	if note.Effort != "default" && slices.Contains(config.ClaudeEfforts, note.Effort) {
		from.Effort = note.Effort
	}
	prev := f.inbox.Clone()
	id := f.inbox.Add(feedback.Note{Kind: note.Kind, Title: strings.TrimSpace(note.Title),
		Text: strings.TrimSpace(note.Text), From: from, Build: m.build, At: time.Now()})
	if err := m.saveFeedback(); err != nil {
		f.inbox = prev
		m.refuse(req, 0, err)
		return
	}
	f.rev++
	m.reply(req, Reply{Note: id})
	m.refreshFeedbackSettings(time.Now(), true)
	m.maybeGroupFeedback()
}

// CloseThread closes thread t. An unknown or closed thread, or a failed
// save, is refused and changes nothing.
func (m *Model) CloseThread(t feedback.ThreadID, req ReqID) {
	f := m.feedback
	if f.dir == "" || f.loadErr != nil {
		m.refuse(req, 0, errors.New("the feedback inbox is not loaded"))
		return
	}
	prev := f.inbox.Clone()
	if err := f.inbox.Close(t); err != nil {
		m.refuse(req, 0, err)
		return
	}
	if err := m.saveFeedback(); err != nil {
		f.inbox = prev
		m.refuse(req, 0, err)
		return
	}
	f.rev++
	m.reply(req, Reply{})
}

// FixThread starts thread t's fix session (spec §7) in workspace (else
// feedback.workspace): a Create the model composes as `loom session new`
// does, titled feedback.FixTitle and prompted with feedback.FixPrompt. The
// thread is marked fixing with the new instance as the Create is made; the
// Reply and Started are the Create's. Refused: an unknown or closed thread,
// one whose fix session is still served, no workspace, one the model
// doesn't serve, the checks a new session meets (CheckRoom, CheckWritable,
// CheckNewTitle), a title the workspace holds, and blocked remote control.
func (m *Model) FixThread(t feedback.ThreadID, workspace string, req ReqID) {
	f := m.feedback
	th, ok := f.inbox.Thread(t)
	switch {
	case f.dir == "" || f.loadErr != nil:
		m.refuse(req, 0, errors.New("the feedback inbox is not loaded"))
		return
	case !ok:
		m.refuse(req, 0, fmt.Errorf("thread %d doesn't exist", t))
		return
	case th.Status == feedback.Closed:
		m.refuse(req, 0, fmt.Errorf("thread %d is closed", t))
		return
	case th.Fix.ID != 0:
		if v, live := m.View(InstanceID(th.Fix.ID)); live {
			m.refuse(req, 0, fmt.Errorf("thread %d's fix session %q is still there", t, v.Title))
			return
		}
	}
	m.refreshFeedbackSettings(time.Now(), true)
	name := cmp.Or(workspace, f.workspace)
	if name == "" {
		m.refuse(req, 0, errors.New("no workspace to fix in: pass -w <workspace>, or set feedback.workspace in the global config.json"))
		return
	}
	served := m.Workspaces()
	i := slices.IndexFunc(served, func(v WorkspaceView) bool { return v.Name == name })
	if i < 0 {
		m.refuse(req, 0, fmt.Errorf("workspace %q is not served by the loom daemon", name))
		return
	}
	v := served[i]
	title := feedback.FixTitle(t, th.Title)
	views := m.Views(v.ID)
	for _, check := range []error{CheckRoom(len(views)), CheckWritable(v), CheckNewTitle(title, v)} {
		if check != nil {
			m.refuse(req, 0, check)
			return
		}
	}
	if slices.ContainsFunc(views, func(iv InstanceView) bool { return iv.Title == title }) {
		m.refuse(req, 0, fmt.Errorf("%s already has a session titled %q", v.Label, title))
		return
	}
	program := v.Settings.GetProgram()
	m.ReloadAccounts()
	opts, _ := ChooseLaunchAccount(m, launch.FromSettings(v.Settings), program)
	if auth := m.RCAuthFor(opts.Account); launch.RemoteControlBlocked(auth, launch.EffectiveRemoteControl(opts), program) {
		m.refuse(req, 0, fmt.Errorf("remote control is unavailable: %s", auth.Reason))
		return
	}
	id, made := m.createWS(m.wsLookup(v.ID), NewInstance{Title: title, Path: v.RepoPath, Program: program,
		Prompt: feedback.FixPrompt(f.inbox, th), Launch: opts, Start: true}, req)
	if !made {
		return
	}
	if err := f.inbox.SetFix(t, feedback.Fix{ID: uint64(id), Title: title, Workspace: v.Label, At: time.Now()}); err != nil {
		return
	}
	if err := m.saveFeedback(); err != nil {
		m.notifyErr(fmt.Errorf("feedback inbox: %w; thread %d's fix session is kept in memory", err, t))
	}
	f.rev++
}
