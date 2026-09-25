package session

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/account"
	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/log"
)

// RosterStatus is a Claude session's self-reported state as published by
// `claude agents --json`. It is authoritative: Claude knows whether it is
// mid-turn or blocked on a dialog, where Loom's pane scraper can only
// infer it from screen text (see PendingPromptPattern and the redetect
// ladder in app/events.go).
type RosterStatus int

const (
	// RosterStatusUnknown means the roster reported a status string this
	// build does not recognize. Callers must treat it as "no opinion" and
	// fall back to pane-content detection rather than guessing.
	RosterStatusUnknown RosterStatus = iota
	// RosterStatusIdle means the session is waiting for the user's next
	// instruction — Loom's Ready.
	RosterStatusIdle
	// RosterStatusBusy means the session is mid-turn — Loom's Running.
	RosterStatusBusy
	// RosterStatusWaiting means the session is blocked on a prompt or
	// dialog — Loom's Prompting. WaitingFor carries Claude's reason.
	RosterStatusWaiting
)

// RosterEntry is one live Claude session as reported by the roster.
type RosterEntry struct {
	SessionID  string
	Name       string
	Status     RosterStatus
	WaitingFor string
}

// LoomStatus maps a roster status onto Loom's session lifecycle. The bool
// is false when the roster expressed no usable opinion, in which case the
// caller must leave the instance's status to the pane-content ladder.
func (e RosterEntry) LoomStatus() (Status, bool) {
	switch e.Status {
	case RosterStatusBusy:
		return Running, true
	case RosterStatusWaiting:
		return Prompting, true
	case RosterStatusIdle:
		return Ready, true
	default:
		return Ready, false
	}
}

// claudeRosterEntry is the subset of `claude agents --json` we rely on.
// Field names match the CLI's observed output.
type claudeRosterEntry struct {
	Cwd        string `json:"cwd"`
	Kind       string `json:"kind"`
	SessionID  string `json:"sessionId"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	WaitingFor string `json:"waitingFor"`
}

// rosterKindBackground is the `kind` value the CLI reports for `--bg`
// sessions. Those entries carry {id,state} where interactive ones carry
// {pid,status}, so they publish no `status` this package can read — and
// Loom instances are always interactive anyway.
const rosterKindBackground = "background"

// isInteractive reports whether this entry could back a Loom instance.
// Anything not explicitly labelled background counts: `kind` is absent
// from older CLI output, where every listed session was interactive, so
// defaulting the other way would drop every entry on those builds.
func (e claudeRosterEntry) isInteractive() bool {
	return !strings.EqualFold(strings.TrimSpace(e.Kind), rosterKindBackground)
}

// claudeRosterTimeout bounds the `claude agents --json` subprocess so a
// hung CLI cannot stall the health tick. Measured cost of a real call is
// ~100ms on Claude Code 2.1.280 (~380ms on older builds); this is
// generous headroom, not a target.
const claudeRosterTimeout = 5 * time.Second

// QueryClaudeRoster is QueryClaudeRosterEnv for the default account.
func QueryClaudeRoster(program string, runner internalexec.Executor) (map[string]RosterEntry, error) {
	return QueryClaudeRosterEnv(program, nil, runner)
}

// QueryClaudeRosterEnv runs `claude agents --json` with env appended to
// loom's own environment (nil: inherit unchanged) and returns its entries
// keyed by cwd. The roster lists only the sessions of the config dir the
// CLI runs under, so each account's sessions need a query run as that
// account (env = its CLAUDE_CONFIG_DIR).
//
// Returns an empty map and no error for non-Claude programs — callers can
// invoke it unconditionally. A missing subcommand, a hung CLI, an
// account.ErrAccountDirMissing config dir, or output this build cannot
// parse is an error: the caller logs it and keeps using pane-content
// detection.
func QueryClaudeRosterEnv(program string, env []string, runner internalexec.Executor) (map[string]RosterEntry, error) {
	if !IsClaudeProgram(program) {
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), claudeRosterTimeout)
	defer cancel()
	c, err := account.Command(ctx, program, env, "agents", "--json")
	if err != nil {
		return nil, err
	}
	out, err := runner.Output(c)
	if err != nil && len(out) == 0 {
		return nil, fmt.Errorf("claude agents --json: %w", err)
	}

	var raw []claudeRosterEntry
	if jsonErr := json.Unmarshal(out, &raw); jsonErr != nil {
		return nil, fmt.Errorf("parsing claude agents --json: %w", jsonErr)
	}

	// Group the interactive sessions by directory. Background sessions are
	// skipped outright rather than counted: a `claude --bg` started inside
	// a Loom worktree shares the cwd with Loom's own tmux-hosted session,
	// and treating that as a collision would blind the join for an
	// instance whose identity is not in doubt.
	byCwd := make(map[string][]claudeRosterEntry, len(raw))
	for _, e := range raw {
		if e.Cwd == "" || !e.isInteractive() {
			continue
		}
		byCwd[e.Cwd] = append(byCwd[e.Cwd], e)
	}

	entries := make(map[string]RosterEntry, len(byCwd))
	for cwd, group := range byCwd {
		// Two interactive sessions in one directory (the user ran claude
		// by hand inside a Loom worktree) make the join genuinely
		// ambiguous — neither can be attributed to the instance, so drop
		// the directory and let the caller fall back rather than driving
		// transitions off a coin flip.
		if len(group) > 1 {
			log.DebugKV("session.roster.ambiguous_cwd", "cwd", cwd, "sessions", len(group))
			continue
		}
		e := group[0]
		entries[cwd] = RosterEntry{
			SessionID:  e.SessionID,
			Name:       e.Name,
			Status:     parseRosterStatus(e.Status),
			WaitingFor: e.WaitingFor,
		}
	}
	return entries, nil
}

// parseRosterStatus maps Claude's status string onto RosterStatus,
// yielding Unknown for anything this build does not recognize so a future
// CLI status cannot silently drive a wrong transition.
func parseRosterStatus(s string) RosterStatus {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "idle":
		return RosterStatusIdle
	case "busy":
		return RosterStatusBusy
	case "waiting":
		return RosterStatusWaiting
	default:
		return RosterStatusUnknown
	}
}
