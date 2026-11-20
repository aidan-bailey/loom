package github

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
)

// issueReasonMax bounds a close reason, in bytes: GitHub's are a few words,
// and the feedback inbox stores the reason on one line.
const issueReasonMax = 40

// IssueState is an issue's state as the feedback poll reads it. Closed is
// gh's state being CLOSED, nothing else. Reason is gh's stateReason
// lowercased with each underscore a space ("completed", "not planned",
// "duplicate", …), or "" when gh gives none or one that is no plain word.
type IssueState struct {
	Closed bool
	Reason string
}

type rawIssueState struct {
	State       string `json:"state"`
	StateReason string `json:"stateReason"`
}

// IssueStateOf asks gh for issue number's state in repo ("owner/name"):
// `gh issue view <N> -R <repo> --json state,stateReason`, run in dir under
// ghTimeout. A closed issue is the answer's state alone; an unfamiliar
// stateReason is never an error. An answer with no state is one, so a
// malformed reply never reads as an open issue.
func IssueStateOf(ctx context.Context, dir string, repo string, number int, r internalexec.Executor) (IssueState, error) {
	if !strings.Contains(repo, "/") || strings.HasPrefix(repo, "-") || number <= 0 {
		return IssueState{}, fmt.Errorf("issue state: %q #%d does not name an issue", repo, number)
	}
	out, err := gh(ctx, r, dir, "issue", "view", strconv.Itoa(number), "-R", repo, "--json", "state,stateReason")
	if err != nil {
		return IssueState{}, err
	}
	var raw rawIssueState
	if err := json.Unmarshal(out, &raw); err != nil {
		return IssueState{}, fmt.Errorf("parsing gh issue view: %w", err)
	}
	if raw.State == "" {
		return IssueState{}, fmt.Errorf("gh issue view %d -R %s: the answer has no state", number, repo)
	}
	return IssueState{Closed: raw.State == "CLOSED", Reason: issueReason(raw.StateReason)}, nil
}

// issueReason is stateReason as a lowercase phrase, "" for none, one too
// long, or one with anything but letters and underscores in it.
func issueReason(raw string) string {
	if raw == "" || len(raw) > issueReasonMax {
		return ""
	}
	for _, c := range raw {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '_') {
			return ""
		}
	}
	return strings.ToLower(strings.ReplaceAll(raw, "_", " "))
}
