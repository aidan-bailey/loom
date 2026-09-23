package session

import "time"

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
	at     time.Time
	source obsSource
	valid  bool
}

// claudeState is what loom knows about a Claude session from its hooks and
// the agent roster. Instance guards it with mu. See
// docs/superpowers/specs/2026-09-23-claude-hook-events-design.md.
type claudeState struct {
	obs observation
	// sessionID and transcriptPath name the conversation a relaunch
	// resumes (persisted, schema v7). Only a parent SessionStart sets them.
	sessionID      string
	transcriptPath string
	// lastMessage is the parent's last_assistant_message from its latest
	// Stop. lastMsgValid turns false once a prompt or permission request
	// makes it stale.
	lastMessage  string
	lastMsgValid bool
}

// offer applies o if it is newer than the current observation, and reports
// whether the reported status or reason changed. Newest wins whichever
// source either came from: the roster publishes a change before Claude
// runs the hook, so a roster answer stamped after a hook event already
// reflects it, and one stamped before is older and dropped. The comparison
// uses at even when the current observation is no-opinion, so an older
// answer delivered late never revives a status a SessionEnd cleared.
//
// A roster observation with the same status as a current hook one keeps
// the hook's reason: the hook names the tool ("permission: Bash"), the
// roster only the kind of wait ("permission prompt").
func (s *claudeState) offer(o observation) bool {
	if !s.obs.at.IsZero() && !o.at.After(s.obs.at) {
		return false
	}
	if o.source == obsRoster && o.valid && s.obs.valid && s.obs.source == obsHook &&
		o.status == s.obs.status && s.obs.reason != "" {
		o.reason = s.obs.reason
	}
	changed := o.valid != s.obs.valid ||
		(o.valid && (o.status != s.obs.status || o.reason != s.obs.reason))
	s.obs = o
	return changed
}

// rosterSilent records that the roster had no opinion at at: no entry,
// an ambiguous cwd, an unknown status or a failed query. A roster
// observation older than that becomes no-opinion, as a failed query
// clears the roster: a roster-sourced status must not outlive the roster
// that produced it. A hook observation is left alone. Reports whether
// anything changed.
func (s *claudeState) rosterSilent(at time.Time) bool {
	if s.obs.source != obsRoster || !s.obs.valid || !at.After(s.obs.at) {
		return false
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
