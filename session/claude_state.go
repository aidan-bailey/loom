package session

import (
	"time"

	"github.com/aidan-bailey/loom/session/hooks"
)

// obsSource says which source produced an observation. The zero value is
// no source: nothing has been observed yet.
type obsSource int

const (
	obsHook obsSource = iota + 1
	obsRoster
)

// observation is one source's report of a Claude session's status,
// stamped with when it was observed rather than when loom received it.
// valid false is "no opinion": the status ladder decides.
type observation struct {
	status Status
	reason string // wait reason; empty unless status is Prompting
	detail string // the permission request's tool summary; empty unless status is Prompting
	// reasonFromHook is true when reason is one a hook gave, whether this
	// observation is the hook's or a roster answer that inherited it.
	reasonFromHook bool
	at             time.Time
	source         obsSource
	valid          bool
}

// claudeState is what loom knows about a Claude session from its hooks and
// the agent roster. Instance guards it with mu. See
// docs/superpowers/specs/2026-09-23-claude-hook-events-design.md.
type claudeState struct {
	obs observation
	// held is the status, reason and tool summary of a hook's wait that
	// rosterSilent voided: the labels a roster answer with the same status
	// takes back while obs is still the roster's no-opinion. Zero (valid
	// false) when nothing is held. A hook event, a roster answer, newLaunch
	// and folderGone each clear it.
	held observation
	// sessionID and transcriptPath name the conversation a relaunch
	// resumes (persisted, schema v7). Only a parent SessionStart sets them.
	sessionID      string
	transcriptPath string
	// lastMessage is the parent's last_assistant_message from its latest
	// Stop. lastMsgValid turns false once a prompt or permission request
	// makes it stale.
	lastMessage  string
	lastMsgValid bool
	// sessionStartedAt is when this launch's Claude first reported its
	// SessionStart (startup or resume): core's mod grace runs from it.
	// newLaunch clears it.
	sessionStartedAt time.Time
}

// offer applies o if it is newer than the current observation, and reports
// whether the reported status, reason or tool summary changed. Newest wins
// whichever source either came from: the roster publishes a change before
// Claude runs the hook, so a roster answer stamped after a hook event
// already reflects it, and one stamped before is older and dropped. The
// comparison uses at even when the current observation is no-opinion, so an
// older answer delivered late never revives a status a SessionEnd cleared.
//
// A roster observation with the same status as the current one keeps a
// reason a hook gave: the hook names the tool ("permission: Bash"), the
// roster only the kind of wait ("permission prompt"). The reason and the tool
// summary belong to the permission request, not to whoever reports the
// wait, so every roster answer that agrees on the status keeps both, the one
// after the hook's and the ones after that, until a hook event, a status
// change or the end of the wait replaces the observation: a relabel under
// an unchanged prompt would read as a new wait. The roster names no tool, so
// it never sets a summary, and a wait only the roster reports takes the
// roster's own reason.
//
// A roster answer arriving while the current observation is the roster's
// no-opinion (rosterSilent voided a hook's wait) and having the status the
// voided one had takes the reason and tool summary rosterSilent held: a failed
// query leaves the prompt itself unchanged, so its labels must not change
// either, or the phone reads the return as a new wait. Only the labels are
// carried; the status comes from the roster's own answer. A dropped (older)
// offer leaves the held labels alone; any accepted one ends the hold, a hook
// event because it is a fresh observation and a roster answer because it is
// either the return the labels waited for or a different wait.
func (s *claudeState) offer(o observation) bool {
	if !s.obs.at.IsZero() && !o.at.After(s.obs.at) {
		return false
	}
	held := s.held
	s.held = observation{}
	if o.source == obsRoster && o.valid && !s.obs.valid && s.obs.source == obsRoster &&
		held.valid && o.status == held.status {
		o.reason, o.detail, o.reasonFromHook = held.reason, held.detail, true
	}
	if o.source == obsHook && o.valid && o.reason != "" {
		o.reasonFromHook = true
	}
	if o.source == obsRoster && o.valid && s.obs.valid && s.obs.reasonFromHook &&
		o.status == s.obs.status && s.obs.reason != "" {
		o.reason, o.reasonFromHook = s.obs.reason, true
	}
	if o.source == obsRoster && o.valid && s.obs.valid && o.status == s.obs.status {
		o.detail = s.obs.detail
	}
	changed := o.valid != s.obs.valid ||
		(o.valid && (o.status != s.obs.status || o.reason != s.obs.reason || o.detail != s.obs.detail))
	s.obs = o
	return changed
}

// rosterSilent records that the roster had no opinion at at: no entry,
// an ambiguous cwd, an unknown status or a failed query. A roster
// observation older than that becomes no-opinion, as a failed query
// clears the roster: a roster-sourced status must not outlive the roster
// that produced it. A hook observation is left alone. Reports whether
// anything changed.
//
// When the voided observation carried a hook's reason, that reason, its tool
// summary and its status are kept in held: the prompt did not change because a
// query failed, and offer gives the labels back to the roster answer that
// confirms the same status.
func (s *claudeState) rosterSilent(at time.Time) bool {
	if s.obs.source != obsRoster || !s.obs.valid || !at.After(s.obs.at) {
		return false
	}
	if s.obs.reasonFromHook {
		s.held = s.obs
	}
	s.obs = observation{at: at, source: obsRoster}
	return true
}

// status returns the current observation, with ok false for no opinion.
func (s *claudeState) status() (Status, string, bool) {
	if !s.obs.valid {
		return Ready, "", false
	}
	return s.obs.status, s.obs.reason, true
}

// waitDetail returns the current observation's tool summary: "" unless it
// is a Prompting one a permission request gave.
func (s *claudeState) waitDetail() string {
	if !s.obs.valid || s.obs.status != Prompting {
		return ""
	}
	return s.obs.detail
}

// waitingNotifications are the Notification types that mean Claude is
// waiting on the user. They fire only after about six seconds without a
// keystroke, so PermissionRequest drives Prompting; these cover the
// prompts it misses (a sandboxed command's network request, elicitation
// dialogs).
var waitingNotifications = map[string]bool{
	"permission_prompt":      true,
	"elicitation_dialog":     true,
	"elicitation_url_dialog": true,
}

// applyEvent folds one hook event into s and reports whether the status
// or wait reason changed. Only parent events (no agent_id) count, except
// PermissionRequest: a subagent's prompt appears in the parent's UI.
func (s *claudeState) applyEvent(ev hooks.Event) bool {
	if ev.AgentID != "" && ev.Name != hooks.EventPermissionRequest {
		return false
	}
	switch ev.Name {
	case hooks.EventSessionStart:
		// Only SessionStart names the conversation: a failed --resume of an
		// unknown ID sends a SessionEnd carrying that ID.
		if ev.SessionID != "" {
			s.sessionID, s.transcriptPath = ev.SessionID, ev.TranscriptPath
		}
		if s.sessionStartedAt.IsZero() && (ev.Source == "startup" || ev.Source == "resume") {
			s.sessionStartedAt = ev.At
		}
		switch ev.Source {
		case "startup", "resume", "clear":
			return s.offer(observation{status: Ready, at: ev.At, source: obsHook, valid: true})
		}
		// compact, or a source this build does not know: compaction can run
		// mid-turn, so it says nothing about the status.
		return false
	case hooks.EventUserPromptSubmit:
		s.lastMsgValid = false
		return s.offer(observation{status: Running, at: ev.At, source: obsHook, valid: true})
	case hooks.EventPermissionRequest:
		s.lastMsgValid = false
		reason := "permission"
		if ev.ToolName != "" {
			reason = "permission: " + ev.ToolName
		}
		return s.offer(observation{status: Prompting, reason: reason, detail: ev.ToolSummary, at: ev.At, source: obsHook, valid: true})
	case hooks.EventNotification:
		if !waitingNotifications[ev.NotificationType] {
			return false
		}
		// It arrives about six seconds after the PermissionRequest for the
		// same prompt, with a generic message; keep the specific reason and
		// the tool summary that goes with it.
		reason, detail := ev.Message, ""
		if s.obs.valid && s.obs.status == Prompting && s.obs.reason != "" {
			reason, detail = s.obs.reason, s.obs.detail
		}
		return s.offer(observation{status: Prompting, reason: reason, detail: detail, at: ev.At, source: obsHook, valid: true})
	case hooks.EventStop:
		s.lastMessage, s.lastMsgValid = ev.LastAssistantMessage, true
		// Stop ends a turn, not the work: with a background subagent still
		// running, Claude resumes the parent when it reports back.
		status := Ready
		if runningSubagent(ev.Tasks) {
			status = Running
		}
		return s.offer(observation{status: status, at: ev.At, source: obsHook, valid: true})
	case hooks.EventSessionEnd:
		return s.offer(observation{at: ev.At, source: obsHook})
	}
	return false
}

// runningSubagent reports whether a Stop's background_tasks lists a
// running plain subagent. Teammate and shell tasks don't count: an idle
// teammate stays listed as running, and a background shell runs while
// Claude waits for input. A missing list reads as none: a wrong Ready is
// corrected by the roster query the event triggers, while a wrong Running
// would stay until the next event.
func runningSubagent(tasks []hooks.Task) bool {
	for _, t := range tasks {
		if t.Type == "subagent" && t.Status == "running" {
			return true
		}
	}
	return false
}

// newLaunch forgets what the previous process reported, keeping the
// conversation it names: the relaunch resumes it, and the new process's
// SessionStart replaces it. The no-opinion observation is stamped now, so
// a roster answer from before the relaunch cannot revive the old status.
func (s *claudeState) newLaunch(now time.Time) {
	s.obs = observation{at: now, source: obsHook}
	s.held = observation{}
	s.lastMessage, s.lastMsgValid = "", false
	s.sessionStartedAt = time.Time{}
}

// folderGone drops what the hooks reported once their folder vanished
// mid-run: nothing will refresh a hook observation, and the labels held from
// one go too. A roster observation stands, since the roster still answers for
// the session.
func (s *claudeState) folderGone(now time.Time) {
	s.held = observation{}
	if s.obs.source == obsHook && s.obs.valid {
		s.obs = observation{at: now, source: obsHook}
	}
	s.lastMessage, s.lastMsgValid = "", false
}

// ClaudeStatus returns the status Claude's hooks or the roster last
// reported for this session, with the wait reason when Prompting. ok is
// false when neither has an opinion, and the status ladder decides.
func (i *Instance) ClaudeStatus() (status Status, reason string, ok bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.claude.status()
}

// ClaudeWaitDetail returns the tool summary of the current observation: what
// the permission request that has this session Prompting asks to run or
// touch. It is "" unless the observation is a Prompting one a hook gave a
// summary (a wait only the roster reports, or a Notification alone, names no
// tool).
func (i *Instance) ClaudeWaitDetail() string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.claude.waitDetail()
}

// ObserveRoster offers the roster's answer for this session, observed at
// at (when the query started). ok false means the roster had no opinion.
// Reports whether the status or wait reason changed. Call it on the Update
// goroutine.
func (i *Instance) ObserveRoster(status Status, reason string, ok bool, at time.Time) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	if !ok {
		return i.claude.rosterSilent(at)
	}
	return i.claude.offer(observation{status: status, reason: reason, at: at, source: obsRoster, valid: true})
}

// LastMessage returns Claude's last message from its latest Stop, and
// whether it is still current (no prompt or permission request since).
func (i *Instance) LastMessage() (string, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.claude.lastMessage, i.claude.lastMsgValid
}

// ClaudeSession returns the conversation a relaunch should resume: the
// session ID and transcript path from the latest parent SessionStart.
func (i *Instance) ClaudeSession() (sessionID, transcriptPath string) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.claude.sessionID, i.claude.transcriptPath
}

// HooksLaunched reports whether this session's current launch registered
// loom's hooks, so a scan can find its events: a Claude program with a
// config dir whose launch did not fall back to noHooksLaunchID. A
// restored instance that has not adopted its folder's ID yet (empty ID)
// counts.
func (i *Instance) HooksLaunched() bool {
	if i.ConfigDir == "" || !IsClaudeProgram(i.Program()) {
		return false
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.hookLaunchID != noHooksLaunchID
}
