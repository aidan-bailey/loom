package github

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
)

// The watcher's own gh calls (docs/superpowers/specs/2026-11-13-issue-watcher-design.md
// §1, §7.1). They leave the existing poll's query, types and folds
// alone: the poll's PR carries only what the UI folds, the watcher's
// carries what ownership and nudges decide on.

// watchPRFields are the --json fields WatchPRs asks gh for.
const watchPRFields = "number,state,headRefName,baseRefName,headRefOid,baseRefOid,mergeable,author," +
	"isCrossRepository,closingIssuesReferences,statusCheckRollup,body,url"

// watchPRLimit is the --limit of WatchPRs' one call.
const watchPRLimit = "100"

// WatchPR is one pull request as the watcher reads it. State is OPEN,
// CLOSED or MERGED, and Mergeable is MERGEABLE, CONFLICTING or UNKNOWN,
// both as gh reports them. Closes holds the numbers of the issues the
// PR closes, Author the author's login.
type WatchPR struct {
	Number      int
	State       string
	HeadRefName string
	BaseRefName string
	HeadSHA     string
	BaseSHA     string
	Mergeable   string
	Author      string
	CrossRepo   bool
	Closes      []int
	Failing     []FailedCheck
	Body        string
	URL         string
}

// FailedCheck is one check of a PR's head commit that concluded
// FAILURE, TIMED_OUT or ERROR. JobID is the Actions job in URL, 0 when
// URL names none (a legacy check run, an external status).
type FailedCheck struct {
	Name  string
	URL   string
	JobID int64
}

type rawLogin struct {
	Login string `json:"login"`
}

type rawNumber struct {
	Number int `json:"number"`
}

// rawWatchCheck is one element of gh's statusCheckRollup, in both of
// its shapes: a CheckRun (name, conclusion, detailsUrl) and a
// StatusContext (context, state, targetUrl). Decoded loosely, so a field
// gh adds never breaks it.
type rawWatchCheck struct {
	Name       string `json:"name"`
	Context    string `json:"context"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
	DetailsURL string `json:"detailsUrl"`
	TargetURL  string `json:"targetUrl"`
}

type rawWatchPR struct {
	Number                  int             `json:"number"`
	State                   string          `json:"state"`
	HeadRefName             string          `json:"headRefName"`
	BaseRefName             string          `json:"baseRefName"`
	HeadRefOid              string          `json:"headRefOid"`
	BaseRefOid              string          `json:"baseRefOid"`
	Mergeable               string          `json:"mergeable"`
	Author                  rawLogin        `json:"author"`
	IsCrossRepository       bool            `json:"isCrossRepository"`
	ClosingIssuesReferences []rawNumber     `json:"closingIssuesReferences"`
	StatusCheckRollup       []rawWatchCheck `json:"statusCheckRollup"`
	Body                    string          `json:"body"`
	URL                     string          `json:"url"`
}

// actionsJobRE finds the job in an Actions check's URL
// (.../actions/runs/<run>/job/<job>, possibly with a query).
var actionsJobRE = regexp.MustCompile(`/actions/runs/\d+/job/(\d+)`)

// jobIDFromURL returns the Actions job ID in u, or 0 when u names none.
func jobIDFromURL(u string) int64 {
	m := actionsJobRE.FindStringSubmatch(u)
	if m == nil {
		return 0
	}
	id, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0
	}
	return id
}

// failed reports whether the check concluded FAILURE, TIMED_OUT or
// ERROR. A CheckRun's verdict is its conclusion, a StatusContext's its
// state; CANCELLED and an unfinished check (no verdict yet) do not
// count, since only a red check is a reason to nudge.
func (c rawWatchCheck) failed() (FailedCheck, bool) {
	verdict := c.Conclusion
	if verdict == "" {
		verdict = c.State
	}
	switch strings.ToUpper(verdict) {
	case "FAILURE", "TIMED_OUT", "ERROR":
		name, url := c.Name, c.DetailsURL
		if name == "" {
			name = c.Context
		}
		if url == "" {
			url = c.TargetURL
		}
		return FailedCheck{Name: name, URL: url, JobID: jobIDFromURL(url)}, true
	}
	return FailedCheck{}, false
}

func (rp rawWatchPR) pr() WatchPR {
	pr := WatchPR{
		Number:      rp.Number,
		State:       rp.State,
		HeadRefName: rp.HeadRefName,
		BaseRefName: rp.BaseRefName,
		HeadSHA:     rp.HeadRefOid,
		BaseSHA:     rp.BaseRefOid,
		Mergeable:   rp.Mergeable,
		Author:      rp.Author.Login,
		CrossRepo:   rp.IsCrossRepository,
		Body:        rp.Body,
		URL:         rp.URL,
	}
	for _, c := range rp.ClosingIssuesReferences {
		pr.Closes = append(pr.Closes, c.Number)
	}
	for _, c := range rp.StatusCheckRollup {
		if fc, ok := c.failed(); ok {
			pr.Failing = append(pr.Failing, fc)
		}
	}
	return pr
}

// WatchPRs lists the PRs the viewer authored, in every state (the
// newest 100). A gh or parse failure is an error, never an empty list:
// the caller makes no decisions from a failed read.
func WatchPRs(ctx context.Context, repoDir string, r internalexec.Executor) ([]WatchPR, error) {
	out, err := gh(ctx, r, repoDir, "pr", "list", "--author", "@me", "--state", "all",
		"--limit", watchPRLimit, "--json", watchPRFields)
	if err != nil {
		return nil, err
	}
	var raw []rawWatchPR
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parsing gh pr list: %w", err)
	}
	prs := make([]WatchPR, 0, len(raw))
	for _, rp := range raw {
		prs = append(prs, rp.pr())
	}
	return prs, nil
}

// WatchIssueLimit is how many labelled issues one tick considers (spec
// §7.1). WatchIssues returns one more than this at most, so that a 51st
// tells the caller more issues exist than it will consider.
const WatchIssueLimit = 50

// watchIssueListFields and watchIssueViewFields are the --json fields of
// WatchIssues and ViewWatchIssue. The list leaves out state: it lists
// open issues only.
const (
	watchIssueListFields = "number,title,body,url,labels,comments"
	watchIssueViewFields = "number,title,body,state,url,labels,comments"
)

// IssueComment is one comment on a watched issue. Association is gh's
// authorAssociation (OWNER, MEMBER, COLLABORATOR, NONE, ...), and ID its
// node ID, which is stable and so serves as a fingerprint.
type IssueComment struct {
	ID          string
	Author      string
	Association string
	Body        string
	CreatedAt   time.Time
}

// WatchIssue is an issue with the body and comments the watcher's
// prompts and its issue-changed nudge read.
type WatchIssue struct {
	Number   int
	Title    string
	Body     string
	URL      string
	Labels   []string
	Closed   bool
	Comments []IssueComment
}

type rawName struct {
	Name string `json:"name"`
}

type rawComment struct {
	ID                string    `json:"id"`
	Author            rawLogin  `json:"author"`
	AuthorAssociation string    `json:"authorAssociation"`
	Body              string    `json:"body"`
	CreatedAt         time.Time `json:"createdAt"`
}

type rawWatchIssue struct {
	Number   int          `json:"number"`
	Title    string       `json:"title"`
	Body     string       `json:"body"`
	State    string       `json:"state"`
	URL      string       `json:"url"`
	Labels   []rawName    `json:"labels"`
	Comments []rawComment `json:"comments"`
}

func (ri rawWatchIssue) issue() WatchIssue {
	is := WatchIssue{
		Number: ri.Number,
		Title:  ri.Title,
		Body:   ri.Body,
		URL:    ri.URL,
		Closed: ri.State == "CLOSED",
	}
	for _, l := range ri.Labels {
		is.Labels = append(is.Labels, l.Name)
	}
	for _, c := range ri.Comments {
		is.Comments = append(is.Comments, IssueComment{
			ID:          c.ID,
			Author:      c.Author.Login,
			Association: c.AuthorAssociation,
			Body:        c.Body,
			CreatedAt:   c.CreatedAt,
		})
	}
	return is
}

// WatchIssues lists the open issues carrying label, oldest first, with
// bodies and comments: gh answers newest first by default, so the search
// sort is what makes the limit keep the lowest-numbered issues. It
// returns at most WatchIssueLimit+1 issues sorted by number; the caller
// considers the first WatchIssueLimit. An empty label is an error (it
// would list every open issue), as is any gh or parse failure.
func WatchIssues(ctx context.Context, repoDir string, label string, r internalexec.Executor) ([]WatchIssue, error) {
	if label == "" {
		return nil, fmt.Errorf("listing watched issues: the label is empty")
	}
	out, err := gh(ctx, r, repoDir, "issue", "list", "--label", label, "--state", "open",
		"--search", "sort:created-asc", "--limit", strconv.Itoa(WatchIssueLimit+1),
		"--json", watchIssueListFields)
	if err != nil {
		return nil, err
	}
	var raw []rawWatchIssue
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parsing gh issue list: %w", err)
	}
	issues := make([]WatchIssue, 0, len(raw))
	for _, ri := range raw {
		issues = append(issues, ri.issue())
	}
	sort.Slice(issues, func(i, j int) bool { return issues[i].Number < issues[j].Number })
	if len(issues) > WatchIssueLimit+1 {
		issues = issues[:WatchIssueLimit+1]
	}
	return issues, nil
}

// ViewWatchIssue fetches one issue in any state with its comments, for
// the closed issues a watcher session or PR names.
func ViewWatchIssue(ctx context.Context, repoDir string, number int, r internalexec.Executor) (WatchIssue, error) {
	out, err := gh(ctx, r, repoDir, "issue", "view", strconv.Itoa(number), "--json", watchIssueViewFields)
	if err != nil {
		return WatchIssue{}, err
	}
	var ri rawWatchIssue
	if err := json.Unmarshal(out, &ri); err != nil {
		return WatchIssue{}, fmt.Errorf("parsing gh issue view: %w", err)
	}
	return ri.issue(), nil
}

// Viewer returns the login gh is authenticated as: the identity whose
// PRs the watcher treats as its own (spec §2). An empty answer is an
// error, never a login.
func Viewer(ctx context.Context, repoDir string, r internalexec.Executor) (string, error) {
	out, err := gh(ctx, r, repoDir, "api", "user", "--jq", ".login")
	if err != nil {
		return "", err
	}
	login := strings.TrimSpace(string(out))
	if login == "" {
		return "", fmt.Errorf("gh api user: the reply names no login")
	}
	return login, nil
}

// failedLogsTimeout bounds FailedJobLogs (spec §7.1): a failed job's
// log can be large, so it gets longer than ghTimeout.
const failedLogsTimeout = 30 * time.Second

// ghWithTimeout is gh with its own timeout in place of ghTimeout. gh
// itself cannot take a longer one: a parent context's deadline only
// ever shortens a child's.
func ghWithTimeout(ctx context.Context, r internalexec.Executor, dir string, timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	c := internalexec.GhCommand(ctx, args...)
	c.Dir = dir
	out, err := runner(r).Output(c)
	if err != nil {
		return nil, fmt.Errorf("gh %s: %w", label(args), err)
	}
	return out, nil
}

// FailedJobLogs returns the last lines lines of an Actions job's failed
// steps' log. It is called only when a ci nudge is sent. A job ID of 0
// (a check with no Actions job) or a non-positive lines is an error and
// reaches no gh call.
func FailedJobLogs(ctx context.Context, repoDir string, jobID int64, lines int, r internalexec.Executor) (string, error) {
	if jobID <= 0 {
		return "", fmt.Errorf("fetching a failed job's log: job %d is not an Actions job", jobID)
	}
	if lines <= 0 {
		return "", fmt.Errorf("fetching a failed job's log: %d lines asked for", lines)
	}
	out, err := ghWithTimeout(ctx, r, repoDir, failedLogsTimeout,
		"run", "view", "--job", strconv.FormatInt(jobID, 10), "--log-failed")
	if err != nil {
		return "", err
	}
	return tailLines(string(out), lines), nil
}

// tailLines returns the last n lines of s, without the newline that
// ends it.
func tailLines(s string, n int) string {
	s = strings.TrimRight(s, "\r\n")
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
