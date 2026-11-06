package flowrun

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Review is a review report's lines (spec §5.6).
type Review struct {
	Verdict  string
	Findings []Finding
	Plan     []string
}

// findingRE reads a FINDING line's value; `design` may come bracketed, as a
// reviewer copying the notation writes it (part 3b's probe, 2026-11-06).
var findingRE = regexp.MustCompile(`^(Critical|Important|Minor)\s+(\[?design\]?\s+)?(\S+?):(\d+)(?:-\d+)?\s+(.+)$`)

// tagLine is line trimmed of space and markdown's `*` and backticks, and its
// value after tag ("FINDING:"), if it starts with it.
func tagLine(line, tag string) (string, bool) {
	line = strings.Trim(strings.TrimSpace(line), "*`")
	v, ok := strings.CutPrefix(line, tag)
	return strings.Trim(strings.TrimSpace(v), "*`"), ok
}

// ParseReview reads a review's FINDING, PLAN and VERDICT lines. A review
// with no VERDICT line, or a FINDING line it can't read, is an error.
func ParseReview(text string) (Review, error) {
	var r Review
	for _, line := range strings.Split(text, "\n") {
		if v, ok := tagLine(line, "FINDING:"); ok {
			m := findingRE.FindStringSubmatch(v)
			if m == nil {
				return Review{}, fmt.Errorf("a FINDING line it can't read: %q", v)
			}
			n, _ := strconv.Atoi(m[4])
			r.Findings = append(r.Findings, Finding{Severity: m[1], Design: m[2] != "", File: m[3], Line: n, Fix: m[5]})
		} else if v, ok := tagLine(line, "PLAN:"); ok && v != "" {
			r.Plan = append(r.Plan, v)
		} else if v, ok := tagLine(line, "VERDICT:"); ok {
			r.Verdict = v
		}
	}
	if r.Verdict != "APPROVED" && r.Verdict != "CHANGES_REQUESTED" {
		return Review{}, errors.New("its report has no VERDICT line")
	}
	return r, nil
}

// Blocking reports whether r found a Critical or an Important.
func (r Review) Blocking() bool { return len(blocking(r.Findings)) > 0 }

func blocking(fs []Finding) []Finding {
	var out []Finding
	for _, f := range fs {
		if f.Severity != "Minor" {
			out = append(out, f)
		}
	}
	return out
}

// Counts is r's Critical, Important and Minor counts.
func (r Review) Counts() [3]int {
	var c [3]int
	for _, f := range r.Findings {
		switch f.Severity {
		case "Critical":
			c[0]++
		case "Important":
			c[1]++
		default:
			c[2]++
		}
	}
	return c
}

// Minors is r's Minor findings.
func (r Review) Minors() []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Severity == "Minor" {
			out = append(out, f)
		}
	}
	return out
}

// Deviations counts a developer report's DEVIATION lines.
func Deviations(text string) int {
	n := 0
	for _, line := range strings.Split(text, "\n") {
		if v, ok := tagLine(line, "DEVIATION:"); ok && v != "" {
			n++
		}
	}
	return n
}

// PremiseGroup is the flagged sections of up to three stories, for one
// premise-check.
type PremiseGroup struct {
	Stories []string
	Text    string
}

var premiseHeadRE = regexp.MustCompile(`^(Preamble|Story (\S+)) \(plan:\d+\): \d+ premises, (\d+) flagged$`)

// PremiseGroups splits plan_premises.py's output into groups of up to per
// flagged sections, keeping each section's header and flag lines.
func PremiseGroups(out string, per int) []PremiseGroup {
	var groups []PremiseGroup
	var cur *PremiseGroup
	keep := false
	for _, line := range strings.Split(out, "\n") {
		if m := premiseHeadRE.FindStringSubmatch(line); m != nil {
			keep = m[3] != "0"
			if !keep {
				continue
			}
			if cur == nil || len(cur.Stories) == per {
				groups = append(groups, PremiseGroup{})
				cur = &groups[len(groups)-1]
			}
			id := m[2]
			if id == "" {
				id = "preamble"
			}
			cur.Stories = append(cur.Stories, id)
			cur.Text += line + "\n"
			continue
		}
		if keep && strings.HasPrefix(line, "  plan:") {
			cur.Text += line + "\n"
		} else if !strings.HasPrefix(line, "  ") {
			keep = false
		}
	}
	return groups
}

// PremiseVerdict is one premise-check report line.
type PremiseVerdict struct {
	Line    int
	Verdict string // HOLDS, MOVED, RENAMED, STALE, CLASH or UNSURE
	Class   string // COSMETIC, DESIGN or "-"
	Fix     string
	Text    string // the line as reported
}

var verdictRE = regexp.MustCompile(`^plan:(\d+)\s*\|\s*([A-Z]+)\s*\|\s*([A-Z-]+)\s*\|([^|]*)\|?(.*)$`)

// PremiseVerdicts reads a premise-check report's verdict lines.
func PremiseVerdicts(text string) []PremiseVerdict {
	var out []PremiseVerdict
	for _, line := range strings.Split(text, "\n") {
		line = strings.Trim(strings.TrimSpace(line), "`")
		m := verdictRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		out = append(out, PremiseVerdict{Line: n, Verdict: m[2], Class: m[3], Fix: strings.TrimSpace(m[5]), Text: line})
	}
	return out
}
