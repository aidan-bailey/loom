package watch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

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

// StartAnswer is the hook's answer to `start` (spec §5.2): whether to start,
// the variables for the title and the prompt, and on a refusal why. A
// refusal's Vars are always nil.
type StartAnswer struct {
	Start  bool
	Vars   map[string]string
	Reason string
}

// Bounds on what the hook returns (spec §7.1).
const (
	hookMaxVars      = 16
	hookMaxVarValue  = 64       // bytes
	hookMaxNudges    = 8        // per call
	hookMaxNudgeText = 16 << 10 // bytes
	hookMaxNudgeKey  = 256      // bytes
	hookMaxReason    = 1 << 10  // bytes
)

var (
	// hookVarName is what the name of a `start` variable matches.
	hookVarName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	// hookKind is what the kind of a hook nudge matches.
	hookKind = regexp.MustCompile(`^[a-z][a-z-]*$`)
	// hookPromptVars are the variables every prompt template has (spec §3.3).
	// A hook may not return one: merging it in would shadow the real value.
	hookPromptVars = map[string]bool{
		"issue": true, "issue_title": true, "issue_url": true,
		"pr": true, "pr_url": true, "branch": true, "base": true,
	}
	// hookLoomKinds are the kinds of loom's own nudges (spec §4.2), which a
	// hook's may not take.
	hookLoomKinds = map[string]bool{"conflict": true, "ci": true, "issue-changed": true}
)

type hookStartWire struct {
	Start  *bool             `json:"start"`
	Vars   map[string]string `json:"vars"`
	Reason string            `json:"reason"`
}

type hookNudgeWire struct {
	Kind string `json:"kind"`
	Key  string `json:"key"`
	Text string `json:"text"`
}

type hookNudgesWire struct {
	Nudges *[]hookNudgeWire `json:"nudges"`
}

// decodeHookObject decodes the one JSON object in out into v. A key v lacks,
// and any data after the object, fail it.
func decodeHookObject(out []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(out))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("stdout is not the expected JSON object: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("stdout has data after the JSON object")
	}
	return nil
}

// decodeHookStart checks a `start` answer against the shape of spec §5.2 and
// the bounds of §7.1. A refusal's variables are dropped without being checked.
func decodeHookStart(out []byte) (StartAnswer, error) {
	var w hookStartWire
	if err := decodeHookObject(out, &w); err != nil {
		return StartAnswer{}, err
	}
	if w.Start == nil {
		return StartAnswer{}, errors.New(`answer lacks "start"`)
	}
	if len(w.Reason) > hookMaxReason {
		return StartAnswer{}, fmt.Errorf("reason is %d bytes, at most %d", len(w.Reason), hookMaxReason)
	}
	if !*w.Start {
		return StartAnswer{Reason: w.Reason}, nil
	}
	if err := checkHookVars(w.Vars); err != nil {
		return StartAnswer{}, err
	}
	return StartAnswer{Start: true, Vars: w.Vars, Reason: w.Reason}, nil
}

// checkHookVars checks the variables of a `start` that starts: at most 16,
// each name `[a-z][a-z0-9_]*` and not a prompt builtin, each value at most
// 64 bytes. It reports the first fault in name order. The title-variable
// charset is TitleTemplate.Render's to check: the runner does not know the
// template.
func checkHookVars(vars map[string]string) error {
	if len(vars) > hookMaxVars {
		return fmt.Errorf("vars: %d entries, at most %d", len(vars), hookMaxVars)
	}
	for _, name := range slices.Sorted(maps.Keys(vars)) {
		switch {
		case !hookVarName.MatchString(name):
			return fmt.Errorf("vars: name %q is not [a-z][a-z0-9_]*", name)
		case hookPromptVars[name]:
			return fmt.Errorf("vars: name %q is a prompt variable", name)
		case len(vars[name]) > hookMaxVarValue:
			return fmt.Errorf("vars: value of %q is %d bytes, at most %d", name, len(vars[name]), hookMaxVarValue)
		}
	}
	return nil
}

// decodeHookNudges checks a `nudges` answer against the shape of spec §5.3
// and the bounds of §7.1: at most 8 nudges, each kind `[a-z][a-z-]*` and none
// of loom's own, each key and text non-empty and within their sizes. One bad
// nudge fails the answer.
func decodeHookNudges(out []byte) ([]Nudge, error) {
	var w hookNudgesWire
	if err := decodeHookObject(out, &w); err != nil {
		return nil, err
	}
	if w.Nudges == nil {
		return nil, errors.New(`answer lacks "nudges"`)
	}
	items := *w.Nudges
	if len(items) > hookMaxNudges {
		return nil, fmt.Errorf("%d nudges, at most %d", len(items), hookMaxNudges)
	}
	nudges := make([]Nudge, 0, len(items))
	for i, it := range items {
		switch {
		case !hookKind.MatchString(it.Kind):
			return nil, fmt.Errorf("nudges[%d]: kind %q is not [a-z][a-z-]*", i, it.Kind)
		case hookLoomKinds[it.Kind]:
			return nil, fmt.Errorf("nudges[%d]: kind %q is reserved for loom's own nudges", i, it.Kind)
		case it.Key == "":
			return nil, fmt.Errorf("nudges[%d]: key is empty", i)
		case len(it.Key) > hookMaxNudgeKey:
			return nil, fmt.Errorf("nudges[%d]: key is %d bytes, at most %d", i, len(it.Key), hookMaxNudgeKey)
		case it.Text == "":
			return nil, fmt.Errorf("nudges[%d]: text is empty", i)
		case len(it.Text) > hookMaxNudgeText:
			return nil, fmt.Errorf("nudges[%d]: text is %d bytes, at most %d", i, len(it.Text), hookMaxNudgeText)
		}
		nudges = append(nudges, Nudge{Kind: it.Kind, Key: it.Key, Text: it.Text})
	}
	return nudges, nil
}

const (
	hookMaxStdout  = 1 << 20 // bytes of stdout read; more fails the call
	hookStderrTail = 4 << 10 // bytes of stderr kept for status
	// hookWaitDelay is how long Wait lets the descendants of a killed hook
	// hold its pipes open before it closes them and fails the call.
	hookWaitDelay = 2 * time.Second
)

var errHookStdoutTooLarge = fmt.Errorf("stdout exceeds %d bytes", hookMaxStdout)

// hookStdout holds a hook's stdout up to hookMaxStdout bytes. The first Write
// that would pass the cap sets over and fails: exec's copy stops and closes
// the pipe, so a hook that floods stdout gets EPIPE and the daemon stops
// growing.
type hookStdout struct {
	buf  bytes.Buffer
	over bool
}

func (w *hookStdout) Write(p []byte) (int, error) {
	if w.buf.Len()+len(p) > hookMaxStdout {
		w.over = true
		return 0, errHookStdoutTooLarge
	}
	return w.buf.Write(p)
}

// hookStderr keeps the last hookStderrTail bytes written to it.
type hookStderr struct{ tail []byte }

func (w *hookStderr) Write(p []byte) (int, error) {
	w.tail = append(w.tail, p...)
	if extra := len(w.tail) - hookStderrTail; extra > 0 {
		w.tail = append(w.tail[:0], w.tail[extra:]...)
	}
	return len(p), nil
}

// hookEnv builds the hook's environment (spec §5.1): the daemon's, with PWD
// moved to dir (os/exec updates PWD only when Env is nil, so a script reading
// $PWD would see the daemon's directory), then extra, then the two variables
// the hook contract promises, last so that no extra can override them.
// os.Environ returns a copy, so appending to it is safe.
func hookEnv(dir string, extra []string, in HookInput) []string {
	env := os.Environ()
	if dir != "" {
		if abs, err := filepath.Abs(dir); err == nil {
			env = append(env, "PWD="+abs)
		}
	}
	env = append(env, extra...)
	return append(env, "LOOM_WATCH_WORKSPACE="+in.Workspace, "LOOM_WATCH_REPO="+in.Repo)
}

// runHook runs cfg.Command with in.Point as its last argument in dir, feeds
// it in as JSON on stdin, and returns its stdout and the tail of its stderr.
// It fails on a timeout (cfg.Timeout, which must be positive), on the
// caller's context ending, on a non-zero exit, on stdout over hookMaxStdout
// bytes, and on a start failure such as a missing dir; a failure returns no
// stdout. The caller has dealt with an empty cfg.Command (no hook).
func runHook(ctx context.Context, cfg HookConfig, dir string, env []string, in HookInput) ([]byte, string, error) {
	switch {
	case len(cfg.Command) == 0:
		return nil, "", errors.New("hook command is empty")
	case cfg.Command[0] == "":
		return nil, "", errors.New("hook command: the first string is empty")
	case cfg.Timeout <= 0:
		return nil, "", fmt.Errorf("hook timeout %s must be positive", cfg.Timeout)
	}
	payload, err := json.Marshal(in)
	if err != nil {
		return nil, "", fmt.Errorf("encode input: %w", err)
	}

	runCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	args := append(slices.Clone(cfg.Command[1:]), in.Point)
	cmd := exec.CommandContext(runCtx, cfg.Command[0], args...)
	cmd.Dir = dir
	cmd.Env = hookEnv(dir, env, in)
	cmd.Stdin = bytes.NewReader(payload)
	stdout, stderr := &hookStdout{}, &hookStderr{}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.WaitDelay = hookWaitDelay
	runErr := cmd.Run()

	tail := strings.ToValidUTF8(string(stderr.tail), "")
	switch {
	case stdout.over:
		return nil, tail, errHookStdoutTooLarge
	case ctx.Err() != nil:
		return nil, tail, fmt.Errorf("the caller's context ended: %w", ctx.Err())
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		return nil, tail, fmt.Errorf("timed out after %s: %w", cfg.Timeout, context.DeadlineExceeded)
	case runErr != nil:
		return nil, tail, fmt.Errorf("run %s: %w", cfg.Command[0], runErr)
	}
	return stdout.buf.Bytes(), tail, nil
}
