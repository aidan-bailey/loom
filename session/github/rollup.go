package github

import "strings"

// rollupItem is one element of gh's statusCheckRollup. GitHub mixes two
// shapes in the array: CheckRun (status + conclusion) and StatusContext
// (state). Both are decoded loosely so a new field never breaks us.
type rollupItem struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

// foldChecks reduces a rollup to one Checks value. Priority: any
// failure → Failing; else any pending → Pending; else any item →
// Passing; empty → None. Unknown strings count as Pending so a state
// GitHub adds later degrades to "still waiting" rather than "green".
func foldChecks(items []rollupItem) Checks {
	if len(items) == 0 {
		return ChecksNone
	}
	failing, pending := false, false
	for _, it := range items {
		switch classifyRollup(it) {
		case ChecksFailing:
			failing = true
		case ChecksPending:
			pending = true
		}
	}
	switch {
	case failing:
		return ChecksFailing
	case pending:
		return ChecksPending
	default:
		return ChecksPassing
	}
}

func classifyRollup(it rollupItem) Checks {
	// CheckRun shape: status tells whether it finished.
	if it.Status != "" && !strings.EqualFold(it.Status, "COMPLETED") {
		return ChecksPending
	}
	verdict := it.Conclusion
	if verdict == "" {
		verdict = it.State
	}
	switch strings.ToUpper(verdict) {
	case "SUCCESS", "NEUTRAL", "SKIPPED":
		return ChecksPassing
	case "FAILURE", "ERROR", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STARTUP_FAILURE", "STALE":
		return ChecksFailing
	// EXPECTED is a StatusState meaning "a required check has not
	// reported yet" — it is waiting, not green.
	case "PENDING", "QUEUED", "IN_PROGRESS", "WAITING", "REQUESTED", "EXPECTED":
		return ChecksPending
	default:
		return ChecksPending
	}
}

// foldPRState maps gh's state string plus isDraft to PRState.
func foldPRState(state string, draft bool) PRState {
	switch strings.ToUpper(state) {
	case "MERGED":
		return PRMerged
	case "CLOSED":
		return PRClosed
	default:
		if draft {
			return PRDraft
		}
		return PROpen
	}
}

// foldReview maps gh's reviewDecision to Review. REVIEW_REQUIRED and ""
// both mean "no decision yet".
func foldReview(decision string) Review {
	switch strings.ToUpper(decision) {
	case "APPROVED":
		return ReviewApproved
	case "CHANGES_REQUESTED":
		return ReviewChangesRequested
	default:
		return ReviewNone
	}
}

// prRank orders states for the "one PR per branch" reduction: a live
// PR is what the user cares about, then a merged one, then closed.
func prRank(s PRState) int {
	switch s {
	case PROpen, PRDraft:
		return 3
	case PRMerged:
		return 2
	case PRClosed:
		return 1
	default:
		return 0
	}
}

// prOutranks reports whether a should replace b as the branch's PR.
func prOutranks(a, b PR) bool {
	if ra, rb := prRank(a.State), prRank(b.State); ra != rb {
		return ra > rb
	}
	return a.Number > b.Number
}
