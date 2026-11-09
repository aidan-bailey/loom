package flowview

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// joinMarker is the line that ends a skeleton and starts the joined
// stories (flow/lib/plan_join.py's MARKER).
const joinMarker = "<!-- plan_join: the stories below are generated from the drafts; edit the drafts, then re-join -->"

// storiesHeading opens a skeleton's story list (plan_join.STORIES_HEADING).
const storiesHeading = "## Stories"

// maxPlanBytes bounds what a look reads of one plan or spec.
const maxPlanBytes = 8 << 20

var (
	fenceRE   = regexp.MustCompile("^\\s*(```|~~~)")                                // check_plan.FENCE_RE
	h1RE      = regexp.MustCompile(`^#\s+(.*\S)\s*$`)                               // a document's title
	h2RE      = regexp.MustCompile(`^##\s`)                                         // check_plan.H2_RE
	storyRE   = regexp.MustCompile(`^##\s+Story\s+([^\s:.]+)[:.]?\s*(.*)$`)         // check_plan.STORY_RE
	entryRE   = regexp.MustCompile(`^-\s+\*\*Story\s+([^\s*:.]+)\*\*(?:\s|$)`)      // plan_join.ENTRY_RE
	specRE    = regexp.MustCompile(`^\*\*(?:Spec|Design):\*\*\s*(.*)$`)             // check_plan.SPEC_RE
	codeRE    = regexp.MustCompile("`([^`]+)`")                                     // check_plan.CODE_RE
	stageRE   = regexp.MustCompile(`^\*\*Design stage:\*\*\s*(.*)$`)                // /flow:draft's line
	readingRE = regexp.MustCompile(`^\s*[-*]?\s*\*\*Reading:\*\*\s*(.*)$`)          // a skeleton entry's Reading line
	phaseRE   = regexp.MustCompile(`^\s*[-*]?\s*\*\*Phase(?::\*\*|\*\*:)\s*(.*\S)`) // a state file's **Phase** line
)

// planHeader is what a look reads of a plan.
type planHeader struct {
	Title       string
	Spec        string   // the first `backticked` path of the **Spec:** line before the first story, "" for none
	HasSpecLine bool     // a **Spec:** (or **Design:**) line before the first story
	DesignStage string   // the **Design stage:** line's text, "" for none
	Marker      bool     // the plan_join marker line, outside a fence
	Entries     []string // the ## Stories section's story ids, in order
	Stories     []planStory
	Readings    map[string]string // each entry's **Reading:** line, by story id
}

// planStory is a joined story's heading: its id and its 1-based line.
type planStory struct {
	ID   string
	Line int
}

// isFlowPlan reports whether h is a flow's plan: a skeleton or a joined
// plan (the marker), or a plan that names its spec as check_plan reads it.
func (h planHeader) isFlowPlan() bool { return h.Marker || h.HasSpecLine }

// storyIDs is the plan's stories: the skeleton's list, else the joined
// headings.
func (h planHeader) storyIDs() []string {
	if len(h.Entries) > 0 {
		return h.Entries
	}
	ids := make([]string, len(h.Stories))
	for i, s := range h.Stories {
		ids[i] = s.ID
	}
	return ids
}

// readPlan reads path's header; a file past maxPlanBytes is read to there.
func readPlan(path string) (planHeader, error) {
	text, err := readCapped(path, maxPlanBytes)
	if err != nil {
		return planHeader{}, err
	}
	return parsePlan(text), nil
}

// parsePlan is readPlan's parse, on text.
func parsePlan(text string) planHeader {
	h := planHeader{Readings: map[string]string{}}
	fenced, inStories := false, false
	entry := ""
	for i, line := range strings.Split(text, "\n") {
		if fenceRE.MatchString(line) {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		if strings.TrimRight(line, " \t\r") == joinMarker {
			h.Marker = true
			inStories = false
			continue
		}
		if h.Title == "" {
			if m := h1RE.FindStringSubmatch(line); m != nil {
				h.Title = m[1]
			}
		}
		if m := storyRE.FindStringSubmatch(line); m != nil {
			h.Stories = append(h.Stories, planStory{ID: m[1], Line: i + 1})
			inStories = false
			continue
		}
		if strings.TrimRight(line, " \t\r") == storiesHeading {
			inStories = true
			continue
		}
		if h2RE.MatchString(line) {
			inStories = false
			continue
		}
		if len(h.Stories) == 0 {
			if m := specRE.FindStringSubmatch(line); m != nil && !h.HasSpecLine {
				h.HasSpecLine = true
				if paths := codeRE.FindStringSubmatch(m[1]); paths != nil {
					h.Spec = paths[1]
				}
			}
			if m := stageRE.FindStringSubmatch(line); m != nil && h.DesignStage == "" {
				h.DesignStage = strings.TrimSpace(m[1])
			}
		}
		if inStories {
			if m := entryRE.FindStringSubmatch(line); m != nil {
				entry = m[1]
				h.Entries = append(h.Entries, entry)
			} else if m := readingRE.FindStringSubmatch(line); m != nil && entry != "" {
				h.Readings[entry] = strings.TrimSpace(m[1])
			}
		}
	}
	return h
}

// skeletonOf is text above the marker: the plan as the design session
// wrote it. A plan with no marker is all skeleton.
func skeletonOf(text string) string {
	fenced := false
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if fenceRE.MatchString(line) {
			fenced = !fenced
			continue
		}
		if !fenced && strings.TrimRight(line, " \t\r") == joinMarker {
			return strings.Join(lines[:i], "\n")
		}
	}
	return text
}

// phaseOf is a state file's **Phase** line (a supervisor's, a draft's
// STATE.md), "" for none.
func phaseOf(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if m := phaseRE.FindStringSubmatch(line); m != nil {
			return strings.TrimSpace(m[1])
		}
	}
	return ""
}

// readCapped reads at most limit bytes of path.
func readCapped(path string, limit int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit))
	if err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	return string(b), nil
}
