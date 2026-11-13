package watch

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aidan-bailey/loom/session/github"
)

// The hook's points: the value of HookInput.Point and the last argument of
// the hook command.
const (
	hookPointStart  = "start"
	hookPointNudges = "nudges"
)

// HookInput is what a hook reads on stdin (spec §5.2, §5.3). RunStart and
// RunNudges set Point, so callers leave it empty. PR is set for a resume and
// for every nudges call; Session and HeadSHA only for nudges.
type HookInput struct {
	Point     string
	Workspace string
	Repo      string
	Base      string
	BaseSHA   string
	Issue     github.WatchIssue
	PR        *HookPR
	Sessions  []HookSession
	PRs       []HookPR
	Session   *HookSession
	HeadSHA   string
}

// HookPR is an open watcher PR as the hook sees it.
type HookPR struct {
	Number int
	Issue  int
	Branch string
	Body   string
}

// HookSession is a watcher session as the hook sees it, with the variables
// its title parses to.
type HookSession struct {
	Title  string
	Issue  int
	Branch string
	Status string
	Vars   map[string]string
}

type hookIssueWire struct {
	Number int      `json:"number"`
	Title  string   `json:"title"`
	Labels []string `json:"labels"`
	Body   string   `json:"body"`
	URL    string   `json:"url"`
}

type hookPRWire struct {
	Number int    `json:"number"`
	Issue  int    `json:"issue"`
	Branch string `json:"branch"`
	Body   string `json:"body"`
}

type hookSessionWire struct {
	Title  string            `json:"title"`
	Issue  int               `json:"issue"`
	Branch string            `json:"branch"`
	Status string            `json:"status"`
	Vars   map[string]string `json:"vars"`
}

type hookInputWire struct {
	Point     string            `json:"point"`
	Workspace string            `json:"workspace"`
	Repo      string            `json:"repo"`
	Base      string            `json:"base"`
	BaseSHA   string            `json:"base_sha"`
	Issue     hookIssueWire     `json:"issue"`
	PR        *hookPRWire       `json:"pr"`
	Sessions  []hookSessionWire `json:"sessions"`
	PRs       []hookPRWire      `json:"prs"`
	Session   *hookSessionWire  `json:"session,omitempty"`
	HeadSHA   string            `json:"head_sha,omitempty"`
}

// MarshalJSON writes the input in the shape of spec §5.2. Its own wire types
// fix the keys, so github.WatchIssue's closed flag and comments are not sent;
// `pr` is always present (null when unset); and a nil slice or map is sent
// empty, so a hook can iterate it without a null check.
func (in HookInput) MarshalJSON() ([]byte, error) {
	w := hookInputWire{
		Point:     in.Point,
		Workspace: in.Workspace,
		Repo:      in.Repo,
		Base:      in.Base,
		BaseSHA:   in.BaseSHA,
		Issue: hookIssueWire{
			Number: in.Issue.Number,
			Title:  in.Issue.Title,
			Labels: append([]string{}, in.Issue.Labels...),
			Body:   in.Issue.Body,
			URL:    in.Issue.URL,
		},
		Sessions: make([]hookSessionWire, 0, len(in.Sessions)),
		PRs:      make([]hookPRWire, 0, len(in.PRs)),
		HeadSHA:  in.HeadSHA,
	}
	if in.PR != nil {
		pr := wireHookPR(*in.PR)
		w.PR = &pr
	}
	for _, s := range in.Sessions {
		w.Sessions = append(w.Sessions, wireHookSession(s))
	}
	for _, pr := range in.PRs {
		w.PRs = append(w.PRs, wireHookPR(pr))
	}
	if in.Session != nil {
		s := wireHookSession(*in.Session)
		w.Session = &s
	}
	return json.Marshal(w)
}

func wireHookPR(pr HookPR) hookPRWire {
	return hookPRWire{Number: pr.Number, Issue: pr.Issue, Branch: pr.Branch, Body: pr.Body}
}

func wireHookSession(s HookSession) hookSessionWire {
	vars := s.Vars
	if vars == nil {
		vars = map[string]string{}
	}
	return hookSessionWire{Title: s.Title, Issue: s.Issue, Branch: s.Branch, Status: s.Status, Vars: vars}
}

// HookError is the failure of one hook call: the point, the cause and the
// last 4 KiB of the hook's stderr. The call changed nothing, so the caller
// leaves the issue as it was and reports the error in status.
type HookError struct {
	Point  string
	Err    error
	Stderr string
}

// Error reads `hook <point>: <cause>`, plus `; stderr: <last non-blank line>`
// when the hook wrote to stderr, so a caller can store it as the reason.
func (e *HookError) Error() string {
	msg := fmt.Sprintf("hook %s: %v", e.Point, e.Err)
	if line := hookLastLine(e.Stderr); line != "" {
		msg += "; stderr: " + line
	}
	return msg
}

// Unwrap returns the cause, so errors.Is sees a timeout's
// context.DeadlineExceeded.
func (e *HookError) Unwrap() error { return e.Err }

// hookLastLine returns the last non-blank line of s, trimmed.
func hookLastLine(s string) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}
