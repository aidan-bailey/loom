package github

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

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
