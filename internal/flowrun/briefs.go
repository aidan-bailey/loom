package flowrun

import (
	"fmt"
	"strings"
)

// developerBlock is the [developer] block, without its landmine notes for a
// light story (planning amendment 7).
func developerBlock(st *State, s *Story) string {
	if s == nil || s.Tier != "light" {
		return st.Blocks.Developer
	}
	var out []string
	notes := false
	for _, line := range strings.Split(st.Blocks.Developer, "\n") {
		switch {
		case strings.HasPrefix(line, "Landmine notes"):
			notes = true
			out = append(out, line+" none (a light story)")
		case notes && strings.HasPrefix(line, "    "):
		default:
			notes = false
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// DeveloperBrief is a story's developer brief (spec §5.5).
func DeveloperBrief(st *State, s *Story) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Build Story %s of the plan %s: its section is plan lines %d-%d. Read those lines; the tasks are there, not here.\n", s.ID, st.Plan, s.Line, s.End)
	fmt.Fprintf(&b, "The spec is %s.\n", st.Spec)
	if len(s.Row.Corrections) > 0 {
		b.WriteString("\nCorrections to the plan's premises (checked against the base commit):\n")
		for _, c := range s.Row.Corrections {
			fmt.Fprintf(&b, "- %s\n", c)
		}
	}
	fmt.Fprintf(&b, "\nCommit once per task. Run the story's Verify command (%s) before you report DONE.\n", s.Verify)
	fmt.Fprintf(&b, "\n[developer]\n%s\n", developerBlock(st, s))
	return b.String()
}

// LaneNote tells a laned story's developer where it works (parallel pairs
// spec §5.3).
func LaneNote(dir, base string) string {
	return fmt.Sprintf("\nYou work in a lane: a worktree of its own at %s, detached at %s, beside the story before yours, which runs in the session's worktree. Commit there as the plan says; the run picks your commits onto its branch once that story is done.\n", dir, short(base))
}

// RedoDecision is the decision a laned story's developer runs with when its
// lane didn't land and it builds the story again in the session's worktree.
func RedoDecision(dir string) string {
	return fmt.Sprintf("An earlier attempt ran in the lane %s, and its commits are not on this branch: they didn't apply, or the lane was left dirty. Build the story here, from this branch's HEAD; `git -C %s log` shows what that attempt did.", dir, dir)
}

// withEarlier is brief with the reports of the developers before and the
// manager's decision, for an escalated or decided developer.
func withEarlier(brief string, earlier []string, decision string) string {
	if len(earlier) == 0 && decision == "" {
		return brief
	}
	var b strings.Builder
	b.WriteString(brief)
	if len(earlier) > 0 {
		b.WriteString("\nThis story was attempted before; read each earlier report first and go on from the commits it made:\n")
		for _, p := range earlier {
			fmt.Fprintf(&b, "- %s\n", p)
		}
	}
	if decision != "" {
		fmt.Fprintf(&b, "\nThe manager's decision on what was left open:\n%s\n", decision)
	}
	return b.String()
}

// FixGatesBrief is a fix developer's brief for failing gates.
func FixGatesBrief(st *State, s *Story, base, head string, res GateResult, decision string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Fix the gates %s fails. The story's commits are %s..%s; its section is plan lines %d-%d of %s.\n\n", storyName(s), base, head, s.Line, s.End, st.Plan)
	b.WriteString("Failing steps:\n")
	for _, f := range res.Failing {
		fmt.Fprintf(&b, "- %s: %s (log: %s)\n", f.Verdict, f.Line, f.Log)
	}
	if decision != "" {
		fmt.Fprintf(&b, "\nThe manager's decision:\n%s\n", decision)
	}
	fmt.Fprintf(&b, "\nCommit each fix. Run the failing commands again before you report DONE.\n\n[developer]\n%s\n", developerBlock(st, s))
	return b.String()
}

// FixFindingsBrief is a fix developer's brief for review findings.
func FixFindingsBrief(st *State, s *Story, base, head string, findings []Finding, plan []string, decision string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Fix these review findings in %s. Its commits are %s..%s; its section is plan lines %d-%d of %s.\n\n", storyName(s), base, head, s.Line, s.End, st.Plan)
	for _, f := range findings {
		fmt.Fprintf(&b, "- %s\n", f)
	}
	for _, p := range plan {
		fmt.Fprintf(&b, "- Plan: %s\n", p)
	}
	if decision != "" {
		fmt.Fprintf(&b, "\nThe manager's decision:\n%s\n", decision)
	}
	fmt.Fprintf(&b, "\nCommit each fix. Run the story's Verify command (%s) before you report DONE.\n\n[developer]\n%s\n", s.Verify, developerBlock(st, s))
	return b.String()
}

// ReviewBrief is a story's review brief.
func ReviewBrief(st *State, s *Story, base, head, conformance, devReport string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Review Story %s of the plan %s: its section is plan lines %d-%d, and its commits are %s..%s.\n", s.ID, st.Plan, s.Line, s.End, base, head)
	fmt.Fprintf(&b, "The output of loom flow conformance for it is in %s.\n", conformance)
	fmt.Fprintf(&b, "The developer's report, with its deviations, is %s.\n", devReport)
	fmt.Fprintf(&b, "Its Interfaces line: %s\nThe Interfaces lines of the earlier stories it uses are in the plan.\n", s.Interfaces)
	fmt.Fprintf(&b, "\n[reviewer]\n%s\n", st.Blocks.Reviewer)
	return b.String()
}

// ReReviewBrief is the brief of a re-review of a fix round.
func ReReviewBrief(st *State, s *Story, base, head string, findings []Finding, plan []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Re-review a fix round of %s in the plan %s: the fix's commits are %s..%s. Check that each finding below is fixed, and review the fix itself.\n\n", storyName(s), st.Plan, base, head)
	for _, f := range findings {
		fmt.Fprintf(&b, "- %s\n", f)
	}
	for _, p := range plan {
		fmt.Fprintf(&b, "- Plan: %s\n", p)
	}
	fmt.Fprintf(&b, "\n[reviewer]\n%s\n", st.Blocks.Reviewer)
	return b.String()
}

// AntiSeamBrief is the whole-implementation review's brief.
func AntiSeamBrief(st *State, head string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Review the whole implementation of the plan %s, %s..%s, for its seams: contracts between stories, values one story produces that no other reads, units or limits that disagree across a boundary. The spec is %s.\n", st.Plan, st.Base, head, st.Spec)
	b.WriteString("Docs the run changed that contradict the code are findings too, each at the doc's file:line.\n")
	fmt.Fprintf(&b, "\n[reviewer]\n%s\n", st.Blocks.Reviewer)
	return b.String()
}

// SentFinding is a Critical or Important finding a review sent to a fix
// round, with the report that found it: what a doc pass reads for lessons.
type SentFinding struct {
	Finding
	Report string
}

// DocsBrief is a story's doc pass brief (the doc pass's spec, §5.2): the
// story as it landed, base..head, its developer's report, the findings
// its fix rounds took, and the [doc-writer] block.
func DocsBrief(st *State, s *Story, base, head, devReport string, sent []SentFinding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Update the docs for Story %s of the plan %s: its section is plan lines %d-%d. The spec is %s.\n", s.ID, st.Plan, s.Line, s.End, st.Spec)
	fmt.Fprintf(&b, "The story landed as %s..%s: that diff is what the docs follow.\n", base, head)
	b.WriteString("The plan's header and the later stories' **Files** lines say which story owns which doc edit: leave a doc a later story's **Files** line names to that story.\n")
	fmt.Fprintf(&b, "Its developer's report, whose DEVIATION lines say where the code left the plan, and so where the plan's doc edits may have gone stale: %s\n", devReport)
	if len(sent) == 0 {
		b.WriteString("Its reviews sent no Critical or Important finding to a fix round.\n")
	} else {
		b.WriteString("The Critical and Important findings its reviews sent to a fix round, for lessons, each with the report that found it:\n")
		for _, f := range sent {
			fmt.Fprintf(&b, "- %s (%s)\n", f.Finding, f.Report)
		}
	}
	fmt.Fprintf(&b, "\n[doc-writer]\n%s\n", st.Blocks.DocWriter)
	return b.String()
}

// undoStrays is what a doc pass's retry adds after the manager's decision
// on its strays: the paths to put back.
func undoStrays(strays []string) string {
	var b strings.Builder
	b.WriteString("\nThe earlier pass changed these paths, which aren't doc paths; unless the manager's decision says otherwise, undo its changes to them with inverse edits, and commit that:\n")
	for _, p := range strays {
		fmt.Fprintf(&b, "- %s\n", p)
	}
	return b.String()
}

// PremiseBrief is a premise-check's brief.
func PremiseBrief(st *State, g PremiseGroup) string {
	return fmt.Sprintf("Settle these flags of loom flow premises on the plan %s, against the base commit %s:\n\n%s", st.Plan, st.Base, g.Text)
}

func storyName(s *Story) string {
	if s.ID == finalStory {
		return "the whole implementation"
	}
	return "Story " + s.ID
}
