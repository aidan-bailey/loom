package core

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/account"
	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/session/git"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/aidan-bailey/loom/session/launch"
	"github.com/aidan-bailey/loom/watch"
)

// watchStartPlan is a start's rendered result: the session's title and
// prompt, the existing branch a resume checks out (empty for a new session),
// the commit a new session's branch is cut from (empty on a resume), and what
// the issue said when the start was decided (memory's seen).
type watchStartPlan struct {
	title   string
	prompt  string
	branch  string
	baseRef string
	seen    string
}

// watchPrep is what the start job needs, copied off the loop.
type watchPrep struct {
	hook     *watch.HookConfig
	input    watch.HookInput
	tmpl     watch.TitleTemplate
	checkout string
	repoDir  string
	prefix   string
	base     string
	baseSHA  string
	prompt   string
	issue    github.WatchIssue
	pr       github.WatchPR
	resume   bool
	runner   internalexec.Executor
}

// watchIssueOf is the snapshot's issue n.
func watchIssueOf(snap *watch.Snapshot, n int) (github.WatchIssue, bool) {
	for _, is := range snap.Issues {
		if is.Number == n {
			return is, true
		}
	}
	return github.WatchIssue{}, false
}

// watchStartInput is the hook's input for a start (spec §5.2): the watcher's
// own sessions with their title variables, its open PRs, and for a resume the
// PR being resumed.
func watchStartInput(ws *Workspace, st *watchState, a watch.Action, is github.WatchIssue, pr github.WatchPR) watch.HookInput {
	snap := st.snap
	in := watch.HookInput{Workspace: ws.Name(), Repo: st.repo, Base: st.base, BaseSHA: st.baseSHA, Issue: is}
	for _, s := range snap.Sessions {
		if !watch.OwnSession(s, st.tmpl) {
			continue
		}
		_, vars, _ := st.tmpl.Parse(s.Title)
		in.Sessions = append(in.Sessions, watch.HookSession{Title: s.Title, Issue: s.Issue, Branch: s.Branch, Status: s.Status, Vars: vars})
	}
	for _, p := range snap.PRs {
		if p.State != "OPEN" {
			continue
		}
		for _, n := range p.Closes {
			if watch.OwnPR(p, n, snap.Viewer, st.tmpl.BranchPattern(snap.Prefix, n)) {
				in.PRs = append(in.PRs, watch.HookPR{Number: p.Number, Issue: n, Branch: p.HeadRefName, Body: p.Body})
				break
			}
		}
	}
	if a.Resume {
		in.PR = &watch.HookPR{Number: pr.Number, Issue: a.Issue, Branch: pr.HeadRefName, Body: pr.Body}
	}
	return in
}

// watchStart starts a session for issue a.Issue (a resume, with a.Resume, of
// the PR a.PR on a.Branch). The checks that need the snapshot come first, on
// the loop; the hook's start, the rendering and a resume's fetch run in a job
// (watchPrepareStart), and finishWatchStart creates the session when it
// returns.
func (m *Model) watchStart(ws *Workspace, st *watchState, a watch.Action) {
	is, found := watchIssueOf(st.snap, a.Issue)
	switch {
	case !st.startOK:
		setWatchActErr(st, a, fmt.Sprintf("no start this tick: the fetch of origin/%s or the watcher checkout refresh failed", st.base))
		return
	case !found:
		setWatchActErr(st, a, fmt.Sprintf("issue #%d is not in the tick's snapshot", a.Issue))
		return
	}
	var pr github.WatchPR
	if a.Resume {
		var ok bool
		if pr, ok = watchPROf(st.snap, a.PR); !ok || !watchOwnsPR(st, a.Issue, a.PR) || pr.HeadRefName != a.Branch {
			setWatchActErr(st, a, fmt.Sprintf("PR #%d is not the watcher's own open PR for #%d on branch %s: nothing was started", a.PR, a.Issue, a.Branch))
			return
		}
	}
	key := watchActKey(a)
	st.inFlight[key] = true
	limit := 90 * time.Second
	if st.cfg.Hook != nil {
		limit += st.cfg.Hook.Timeout
	}
	p := watchPrep{hook: st.cfg.Hook, input: watchStartInput(ws, st, a, is, pr), tmpl: st.tmpl, checkout: st.checkout, repoDir: ws.ctx.RepoPath, prefix: st.snap.Prefix}
	p.base, p.baseSHA, p.issue, p.pr, p.resume, p.runner = st.base, st.baseSHA, is, pr, a.Resume, m.executor()
	p.prompt = st.prompt
	if a.Resume {
		p.prompt = st.resumePrompt
	}
	seen := watch.IssueSeen(is)
	m.spawnBackground(func() any {
		ctx, cancel := context.WithTimeout(context.Background(), limit)
		defer cancel()
		res := watchActionResult{ws: ws, st: st, key: key, act: a}
		res.start, res.refused = watchPrepareStart(ctx, p)
		res.start.seen = seen
		return res
	})
}

// watchPrepareStart is a start's job: the hook's start (no hook always
// starts, with no variables), the title and the prompt rendered (the prompt
// with PromptFooter appended, refused over watchTextCap), and for a resume the
// check that the title makes the PR's own branch and the fetch of that branch.
// It returns a refusal's reason instead of a plan when anything is off; the
// issue then waits.
func watchPrepareStart(ctx context.Context, p watchPrep) (watchStartPlan, string) {
	hook := watch.HookConfig{}
	if p.hook != nil {
		hook = *p.hook
	}
	ans, err := watch.RunStart(ctx, hook, p.checkout, nil, p.input)
	switch {
	case err != nil:
		return watchStartPlan{}, "the hook's start failed: " + err.Error()
	case !ans.Start && ans.Reason == "":
		return watchStartPlan{}, "the hook declined the start"
	case !ans.Start:
		return watchStartPlan{}, "the hook declined the start: " + ans.Reason
	}
	title, err := p.tmpl.Render(p.issue.Number, ans.Vars)
	if err != nil {
		return watchStartPlan{}, "the title can't be rendered: " + err.Error()
	}
	vars := map[string]string{}
	maps.Copy(vars, ans.Vars)
	vars["issue"], vars["issue_title"], vars["issue_url"] = strconv.Itoa(p.issue.Number), p.issue.Title, p.issue.URL
	vars["pr"], vars["pr_url"], vars["branch"], vars["base"] = "", "", "", p.base
	if p.resume {
		vars["pr"], vars["pr_url"], vars["branch"] = strconv.Itoa(p.pr.Number), p.pr.URL, p.pr.HeadRefName
	}
	text, err := watch.RenderPrompt(p.prompt, vars)
	if err != nil {
		return watchStartPlan{}, "the prompt can't be rendered: " + err.Error()
	}
	text = strings.TrimRight(text, "\n") + watch.PromptFooter(p.issue.Number)
	if len(text) > watchTextCap {
		return watchStartPlan{}, fmt.Sprintf("the rendered prompt is %d bytes, over the %d-byte cap", len(text), watchTextCap)
	}
	plan := watchStartPlan{title: title, prompt: text}
	if !p.resume {
		plan.baseRef = p.baseSHA
		return plan, ""
	}
	if want := git.SanitizeBranchName(p.prefix + title); want != p.pr.HeadRefName {
		return watchStartPlan{}, fmt.Sprintf("resume refused: the title %q would make branch %q, but PR #%d's branch is %q", title, want, p.pr.Number, p.pr.HeadRefName)
	}
	if _, err := git.FetchBranch(p.repoDir, p.pr.HeadRefName, p.runner); err != nil {
		return watchStartPlan{}, fmt.Sprintf("the fetch of branch %s failed, so the resume waits: %v", p.pr.HeadRefName, err)
	}
	plan.branch = p.pr.HeadRefName
	return plan, ""
}

// finishWatchStart creates the session once the start job returned a plan, on
// the loop. The hook may have run for minutes, so the room, the title, the cap
// and the issue's sessions are checked again; a failure to create is returned
// for the issue's status. The launch is the workspace's settings with the
// config's model, effort, permission mode and account over them. A named
// account that is not registered refuses the start: the watcher never falls
// back to the default account, which would bill the wrong subscription.
func (m *Model) finishWatchStart(r watchActionResult) error {
	ws, st, a, plan := r.ws, r.st, r.act, r.start
	v := m.wsViewOf(ws)
	views := m.Views(v.ID)
	for _, check := range []error{CheckRoom(len(views)), CheckWritable(v), CheckNewTitle(plan.title, v)} {
		if check != nil {
			return check
		}
	}
	for _, iv := range views {
		if iv.Title == plan.title {
			return fmt.Errorf("%s already has a session titled %q", v.Label, plan.title)
		}
	}
	own := 0
	for _, inst := range ws.insts {
		if inst.IssueNumber() == a.Issue {
			return fmt.Errorf("a session for #%d (%s) appeared while the hook ran: nothing was started", a.Issue, inst.Title)
		}
		if n, _, ok := st.tmpl.Parse(inst.Title); ok && inst.IssueNumber() == n {
			own++
		}
	}
	if own >= st.cfg.Cap {
		return fmt.Errorf("the cap of %d watcher sessions is full: nothing was started", st.cfg.Cap)
	}
	program := v.Settings.GetProgram()
	m.ReloadAccounts()
	s := st.cfg.Session
	opts := launch.FromSettings(v.Settings)
	opts.Model = cmp.Or(s.Model, opts.Model)
	opts.Effort = cmp.Or(s.Effort, opts.Effort)
	opts.PermissionMode = cmp.Or(s.PermissionMode, opts.PermissionMode)
	opts.Account = s.Account
	if s.Account != "" && s.Account != account.DefaultName && m.AccountsLoaded() {
		if _, ok := m.Account(s.Account); !ok {
			return fmt.Errorf("account %q is not registered: nothing was started", s.Account)
		}
	}
	opts, _ = ChooseLaunchAccount(m, opts, program)
	if auth := m.RCAuthFor(opts.Account); launch.RemoteControlBlocked(auth, launch.EffectiveRemoteControl(opts), program) {
		return fmt.Errorf("remote control is unavailable: %s", auth.Reason)
	}
	spec := NewInstance{Title: plan.title, Path: v.RepoPath, Program: program, Prompt: plan.prompt, Branch: plan.branch, Issue: a.Issue, BaseRef: plan.baseRef, Launch: opts, Start: true}
	if _, made := m.createWS(ws, spec, 0); !made {
		return errors.New("the session was not created (see serve.log)")
	}
	st.mem.SetSeen(a.Issue, plan.seen)
	if err := watch.SaveMemory(st.memPath, st.mem); err != nil {
		err = fmt.Errorf("record what the issue said at the start: %w", err)
		m.notifyErr(err)
		return err
	}
	return nil
}
