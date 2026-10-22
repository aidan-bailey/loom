package session

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
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
	// PID is the Claude process's pid, 0 when the roster named none.
	PID int
	// RemoteSession is the bridgeSessionId of the session's
	// <config dir>/sessions/<PID>.json: the ID in its
	// https://claude.ai/code/<id> Remote Control link. Empty unless that
	// file's sessionId equals SessionID (a reused pid names another
	// session), and on any doubt (see readBridgeID).
	RemoteSession string
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
// Field names match the CLI's observed output. The pid stays raw: one this
// build cannot read (parsePID) costs its entry the bridge ID, never the
// whole query, whose statuses drive every Claude session.
type claudeRosterEntry struct {
	Cwd        string          `json:"cwd"`
	Kind       string          `json:"kind"`
	SessionID  string          `json:"sessionId"`
	Name       string          `json:"name"`
	Status     string          `json:"status"`
	WaitingFor string          `json:"waitingFor"`
	PID        json.RawMessage `json:"pid"`
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
//
// Each entry also carries its pid and, while Remote Control is connected,
// its bridge ID, read from the sessions/<pid>.json beside the config dir
// the CLI ran under (rosterConfigDir). Nothing there can fail the query: a
// file that is missing, unreadable, oversized, malformed or another
// session's gives the entry no bridge ID.
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

	configDir := rosterConfigDir(env)
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
		pid := parsePID(e.PID)
		entries[cwd] = RosterEntry{
			SessionID:     e.SessionID,
			Name:          e.Name,
			Status:        parseRosterStatus(e.Status),
			WaitingFor:    e.WaitingFor,
			PID:           pid,
			RemoteSession: readBridgeID(configDir, pid, e.SessionID),
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

// parsePID reads a roster entry's pid: a positive JSON integer, else 0.
func parsePID(raw json.RawMessage) int {
	var pid int
	if err := json.Unmarshal(raw, &pid); err != nil || pid < 0 {
		return 0
	}
	return pid
}

// rosterConfigDir is the config dir `claude agents --json` ran under, whose
// sessions/ dir holds its sessions' files: the CLAUDE_CONFIG_DIR in env (an
// account's; the last value wins, as os/exec resolves a duplicated key),
// else the one loom itself inherited, else ~/.claude. It is "" when none can
// be found.
func rosterConfigDir(env []string) string {
	dir := ""
	for _, kv := range env {
		if v, found := strings.CutPrefix(kv, "CLAUDE_CONFIG_DIR="); found {
			dir = v
		}
	}
	if dir != "" {
		return dir
	}
	return account.MainDir(account.Identity{})
}

// maxSessionFileBytes is the largest sessions/<pid>.json readBridgeID reads.
// Claude's file is a few hundred bytes; the cap keeps a stray big file from
// being read on every roster query.
const maxSessionFileBytes = 64 << 10

// maxBridgeIDLen bounds a bridge ID, which becomes a path segment of the
// session's https://claude.ai/code/<id> link.
const maxBridgeIDLen = 128

// claudeSessionFile is the part of <config dir>/sessions/<pid>.json this
// package reads. Claude writes the file for each interactive session and
// adds bridgeSessionId while Remote Control is connected.
type claudeSessionFile struct {
	SessionID       string `json:"sessionId"`
	BridgeSessionID string `json:"bridgeSessionId"`
}

// readBridgeID returns the bridgeSessionId in dir/sessions/<pid>.json when
// that file's sessionId is sessionID, else "". It fails closed on every
// doubt: no config dir, pid or session ID; a file that is missing,
// unreadable, not a regular file, over maxSessionFileBytes or malformed; a
// file of another session (a reused pid); and an ID that could not be a
// path segment of the link built from it.
func readBridgeID(dir string, pid int, sessionID string) string {
	if dir == "" || pid <= 0 || sessionID == "" {
		return ""
	}
	path := filepath.Join(dir, "sessions", strconv.Itoa(pid)+".json")
	// Lstat first: opening a FIFO a stranger left here would block the query.
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() || info.Size() > maxSessionFileBytes {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxSessionFileBytes+1))
	if err != nil || len(data) > maxSessionFileBytes {
		return ""
	}
	var sf claudeSessionFile
	if err := json.Unmarshal(data, &sf); err != nil || sf.SessionID != sessionID || !validBridgeID(sf.BridgeSessionID) {
		return ""
	}
	return sf.BridgeSessionID
}

// validBridgeID reports whether id is 1 to maxBridgeIDLen letters, digits,
// underscores and hyphens.
func validBridgeID(id string) bool {
	if id == "" || len(id) > maxBridgeIDLen {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}
