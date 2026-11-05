package core

import (
	"regexp"
	"slices"
	"strings"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session/claudemod"
)

// jobCall matches a `loom job submit` or `loom job run` in a Bash command
// (claudemod.JobCallPattern): each match is one submit a call may account
// for (subagent job results spec §5.2).
var jobCall = regexp.MustCompile(claudemod.JobCallPattern)

// maxModCalls caps the calls a link keeps in flight: a call whose ended
// report never came stays until the cap drops it, oldest first.
const maxModCalls = 64

// ModCall is a Bash call the loom mod reports while it runs (POST /v1/calls):
// its tool_use_id, the subagent it runs in (Claude's agentId; "" on the main
// loop) and its command. The mod reports only commands that match
// claudemod.JobCallPattern.
type ModCall struct {
	ID      string `json:"id"`
	Agent   string `json:"agent,omitempty"`
	Command string `json:"command"`
	// Model is the session's model as Claude's session info gives it
	// ($.session.model(): the main loop's, as /model shows it); "" from an
	// older mod or when the mod couldn't read it. A feedback note records it.
	Model string `json:"model,omitempty"`
}

// reportedCall is a call in flight on a link: uses is how many submits it
// may still account for, one per match of jobCall in its command.
type reportedCall struct {
	ModCall
	uses int
}

// ModCall records a call launch's mod reports starting, or forgets it once
// ended. A call already recorded stays as it is (a retried report), and a
// command with no match is not kept. poller is as ModAsk's: another than the
// latest hello's is ErrModSuperseded.
func (m *Model) ModCall(launch, poller string, c ModCall, ended bool) error {
	inst, l, err := m.askLink(launch, poller)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(l.calls, func(x *reportedCall) bool { return x.ID == c.ID })
	switch {
	case ended:
		if i >= 0 {
			l.calls = slices.Delete(l.calls, i, i+1)
		}
		return nil
	case i >= 0:
		return nil
	}
	uses := len(jobCall.FindAllStringIndex(c.Command, -1))
	if uses == 0 {
		return nil
	}
	if len(l.calls) >= maxModCalls {
		log.For("core").Info("mod.call_dropped", "instance", inst.Title, "call", l.calls[0].ID)
		l.calls = l.calls[1:]
	}
	l.calls = append(l.calls, &reportedCall{ModCall: c, uses: uses})
	return nil
}

// attribution is the reported call a submit of argv from launch came from:
// the one call in flight on that launch's link whose command holds every
// element of argv and has a use left. nil when none does, or more than one
// could have made it: the job then reports to the session (spec §5.2). The
// caller spends the use once the job is saved. A link for an earlier launch
// is never consulted.
func (m *Model) attribution(launch string, argv []string) *reportedCall {
	inst := m.instByLaunch(launch)
	if inst == nil {
		return nil
	}
	l := m.mods[inst]
	if l == nil || l.launch != launch {
		return nil
	}
	var found *reportedCall
	for _, c := range l.calls {
		if c.uses == 0 || !holdsAll(c.Command, argv) {
			continue
		}
		if found != nil {
			return nil
		}
		found = c
	}
	return found
}

// holdsAll reports whether command contains every element of argv.
func holdsAll(command string, argv []string) bool {
	for _, a := range argv {
		if !strings.Contains(command, a) {
			return false
		}
	}
	return true
}
