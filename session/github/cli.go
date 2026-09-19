package github

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/log"
)

// ghTimeout bounds every gh subprocess. gh talks to the network, so
// this is a network budget, not a tick budget.
const ghTimeout = 20 * time.Second

// listLimit caps pr/issue list sizes. Large enough for any repo loom
// is plausibly pointed at; small enough that the JSON stays cheap.
const listLimit = "200"

func runner(r internalexec.Executor) internalexec.Executor {
	if r == nil {
		return internalexec.Default{}
	}
	return r
}

// CheckCLI reports whether gh is installed and authenticated. The
// messages are user-facing (they surface in the picker and the push
// error path).
func CheckCLI(r internalexec.Executor) error {
	if _, err := exec.LookPath("gh"); err != nil {
		return fmt.Errorf("GitHub CLI (gh) is not installed. Please install it first")
	}
	ctx, cancel := context.WithTimeout(context.Background(), ghTimeout)
	defer cancel()
	if err := runner(r).Run(exec.CommandContext(ctx, "gh", "auth", "status")); err != nil {
		// The returned message stays clean because it is user-facing.
		// The cause goes to the log so an expired token and a broken gh
		// install are distinguishable; Run captures no stderr, so
		// wrapping with %w would only add "exit status 1".
		log.For("github").Debug("auth.check_failed", "err", err.Error())
		return fmt.Errorf("GitHub CLI is not configured. Please run 'gh auth login' first")
	}
	return nil
}

func gh(ctx context.Context, r internalexec.Executor, dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, ghTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, "gh", args...)
	c.Dir = dir
	out, err := runner(r).Output(c)
	if err != nil {
		return nil, fmt.Errorf("gh %s: %w", label(args), err)
	}
	return out, nil
}

// label names a gh invocation for error messages by its leading
// subcommand words ("pr list"), without indexing past a short arg list.
func label(args []string) string {
	if len(args) > 2 {
		args = args[:2]
	}
	return strings.Join(args, " ")
}

type rawPR struct {
	Number         int          `json:"number"`
	HeadRefName    string       `json:"headRefName"`
	State          string       `json:"state"`
	IsDraft        bool         `json:"isDraft"`
	ReviewDecision string       `json:"reviewDecision"`
	Rollup         []rollupItem `json:"statusCheckRollup"`
}

type rawIssue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	State  string `json:"state"`
	URL    string `json:"url"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

func (ri rawIssue) issue() Issue {
	is := Issue{Number: ri.Number, Title: ri.Title, Body: ri.Body, URL: ri.URL, Closed: ri.State == "CLOSED"}
	for _, l := range ri.Labels {
		is.Labels = append(is.Labels, l.Name)
	}
	return is
}

// Query fetches repoDir's PRs (all states, reduced to one per head
// branch) and open issues. linked lists issue numbers loom sessions
// reference; any of them absent from the open list (i.e. closed) is
// backfilled with one `gh issue view` each — a backfill failure only
// omits that issue. A pr/issue list failure is fatal: the caller must
// drop its cached snapshot rather than keep a stale one.
func Query(ctx context.Context, repoDir string, linked []int, r internalexec.Executor) (Snapshot, error) {
	out, err := gh(ctx, r, repoDir, "pr", "list", "--state", "all", "--limit", listLimit,
		"--json", "number,headRefName,state,isDraft,reviewDecision,statusCheckRollup")
	if err != nil {
		return Snapshot{}, err
	}
	var prs []rawPR
	if err := json.Unmarshal(out, &prs); err != nil {
		return Snapshot{}, fmt.Errorf("parsing gh pr list: %w", err)
	}

	out, err = gh(ctx, r, repoDir, "issue", "list", "--state", "open", "--limit", listLimit,
		"--json", "number,title,state,url,labels")
	if err != nil {
		return Snapshot{}, err
	}
	var issues []rawIssue
	if err := json.Unmarshal(out, &issues); err != nil {
		return Snapshot{}, fmt.Errorf("parsing gh issue list: %w", err)
	}

	snap := Snapshot{PRs: map[string]PR{}, Issues: map[int]Issue{}, FetchedAt: time.Now()}
	for _, p := range prs {
		pr := PR{Number: p.Number, State: foldPRState(p.State, p.IsDraft), Review: foldReview(p.ReviewDecision), Checks: foldChecks(p.Rollup)}
		if cur, ok := snap.PRs[p.HeadRefName]; !ok || prOutranks(pr, cur) {
			snap.PRs[p.HeadRefName] = pr
		}
	}
	for _, i := range issues {
		snap.Issues[i.Number] = i.issue()
	}
	for _, n := range linked {
		if n == 0 {
			continue
		}
		if _, ok := snap.Issues[n]; ok {
			continue
		}
		is, err := View(ctx, repoDir, n, r)
		if err != nil {
			log.For("github").Debug("issue.backfill_failed", "issue", n, "err", err.Error())
			continue
		}
		snap.Issues[n] = is
	}
	return snap, nil
}

// View fetches one issue with its body, for the picker and the #123
// prompt shorthand.
func View(ctx context.Context, repoDir string, number int, r internalexec.Executor) (Issue, error) {
	out, err := gh(ctx, r, repoDir, "issue", "view", strconv.Itoa(number),
		"--json", "number,title,body,state,url,labels")
	if err != nil {
		return Issue{}, err
	}
	var ri rawIssue
	if err := json.Unmarshal(out, &ri); err != nil {
		return Issue{}, fmt.Errorf("parsing gh issue view: %w", err)
	}
	return ri.issue(), nil
}
