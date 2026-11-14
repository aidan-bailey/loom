package flowrun

import (
	"fmt"
	"strconv"
	"strings"
)

// RecordInputs is what the record needs beside the state: the cost section
// as loom flow costs run prints it, each manager session's models, and the
// roster line at the end.
type RecordInputs struct {
	Cost      string
	Models    map[string][]string
	RosterEnd string
	Commits   int
	Head      string
	// Rules is each agent run's package rules as its record holds them
	// (package rules spec §2.3); nil leaves the line out.
	Rules []AgentRules
}

// AgentRules is one agent run's package rules for the record: its name,
// role and the bytes of CLAUDE.md it pre-loaded (0 for none).
type AgentRules struct {
	Name  string
	Role  string
	Bytes int
}

// Record is the run's record (spec §5.7, flow/plugin/docs/records.md).
func Record(st *State, in RecordInputs) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Run: %s\n\n", st.ID)
	fmt.Fprintf(&b, "- Plan: %s (%d stories)\n- Spec: %s\n", st.Plan, len(st.Stories), st.Spec)
	fmt.Fprintf(&b, "- Commits: %s..%s (%d)\n", short(st.Base), short(in.Head), in.Commits)
	var managers []string
	for _, id := range st.Managers {
		if ms := in.Models[id]; len(ms) > 0 {
			managers = append(managers, strings.Join(ms, ", ")+" "+id)
		} else {
			managers = append(managers, id)
		}
	}
	fmt.Fprintf(&b, "- Manager: %s\n", strings.Join(managers, "; "))
	fmt.Fprintf(&b, "- Start: %s, End: %s\n", st.Start, st.End)
	roster := st.Roster
	if in.RosterEnd != "" && in.RosterEnd != st.Roster {
		roster += " → " + in.RosterEnd
	}
	fmt.Fprintf(&b, "- Definitions: %s\n- Profile: %s\n", roster, st.Profile)
	if st.DesignStage != "" {
		fmt.Fprintf(&b, "- Design stage: %s\n", st.DesignStage)
	}
	if st.Part != "" {
		fmt.Fprintf(&b, "- Part: %s of %s\n", st.Part, st.Spec)
	}
	fmt.Fprintf(&b, "- Ended: %s\n- Engine: loom flow run (flow in loom part 3b)\n", st.Status)
	if line := rulesLine(in.Rules); line != "" {
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "## Cost\n\n%s\n\n## Stories\n\n", strings.TrimSpace(in.Cost))
	b.WriteString("| Story | Developer | Tier | Review | Commits | Status | Deviations | BLOCKED, up a tier | Escalated to manager | Review findings | Turn cap hit | Docs | Lessons |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, s := range st.Stories {
		r := s.Row
		dev := r.Developer
		if dev == "" {
			dev = s.Developer
		}
		if r.Lane != "" {
			dev += " (lane)"
		}
		status := r.Status
		if status == "" {
			status = "not started"
		}
		cap := "no"
		if r.TurnCap {
			cap = "yes"
		}
		docs, lessons := "—", "—"
		if st.Docs {
			docs, lessons = strconv.Itoa(r.Docs), strconv.Itoa(r.Lessons)
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %d | %s | %d | %d | %d | %d/%d/%d | %s | %s | %s |\n",
			s.ID, dev, s.Tier, s.Review, r.Commits, status, r.Deviations, r.UpATier, r.Escalated, r.Findings[0], r.Findings[1], r.Findings[2], cap, docs, lessons)
	}
	b.WriteString("\n## Notes\n\n")
	if len(st.Answers) > 0 {
		spec := 0
		for _, a := range st.Answers {
			if a.From == FromSpec {
				spec++
			}
		}
		fmt.Fprintf(&b, "- Questions: %d; answered from the spec: %d; by the owner: %d.\n", len(st.Answers), spec, len(st.Answers)-spec)
	}
	for _, a := range st.Answers {
		where := ""
		if a.Story != "" {
			where = ", Story " + a.Story
		}
		by := "by the owner"
		if a.From == FromSpec {
			by = "from the spec"
		}
		took := ""
		if a.Head != "" {
			took = " (the run took HEAD " + a.Head + " with it)"
		}
		fmt.Fprintf(&b, "- Asked (%s%s): %s\n  Answered %s: %s%s\n", a.Kind, where, oneLine(a.Text), by, oneLine(a.Answer), took)
	}
	for _, n := range st.Notes {
		fmt.Fprintf(&b, "- %s\n", n)
	}
	var low, last uint64
	lowAt := ""
	for _, g := range st.Gates {
		var r GateResult
		if raw, ok := st.Memo["gates:"+g]; ok && unmarshal(raw, &r) {
			fmt.Fprintf(&b, "- Gates %s: %s%s\n", g, r.Ended, failingSteps(r))
			if r.Free > 0 {
				if last = r.Free; low == 0 || r.Free < low {
					low, lowAt = r.Free, g
				}
			}
		}
	}
	if low > 0 {
		fmt.Fprintf(&b, "- Disk: free space at a gate run's end was lowest at %s (%s), and %s at the last.\n", gigabytes(low), lowAt, gigabytes(last))
	}
	if st.Why != "" {
		fmt.Fprintf(&b, "- %s\n", st.Why)
	}
	return b.String()
}

// failingSteps names a gate run's failing steps, each with its verdict, for
// the record's Gates line: a retro attributes a failed run by them.
func failingSteps(r GateResult) string {
	if len(r.Failing) == 0 {
		return ""
	}
	steps := make([]string, 0, len(r.Failing))
	for _, f := range r.Failing {
		line := oneLine(f.Line)
		if runes := []rune(line); len(runes) > gateLineMax {
			line = string(runes[:gateLineMax]) + "…"
		}
		steps = append(steps, f.Verdict+": "+line)
	}
	return " (" + strings.Join(steps, "; ") + ")"
}

// gateLineMax bounds a failing step's command on a Gates line: a gate can
// be a whole shell script.
const gateLineMax = 100

// gigabytes is n bytes as whole or tenths of a gigabyte.
func gigabytes(n uint64) string {
	gb := float64(n) / (1 << 30)
	if gb >= 10 {
		return fmt.Sprintf("%.0f GB", gb)
	}
	return fmt.Sprintf("%.1f GB", gb)
}

func short(sha string) string {
	if len(sha) > 9 {
		return sha[:9]
	}
	return sha
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// rulesLine is the record's Package rules line (package rules spec §2.3),
// "" when the record knows no agent run's rules.
func rulesLine(rules []AgentRules) string {
	if len(rules) == 0 {
		return ""
	}
	n, total := 0, 0
	var most AgentRules
	for _, r := range rules {
		if r.Bytes == 0 {
			continue
		}
		n, total = n+1, total+r.Bytes
		if r.Bytes > most.Bytes {
			most = r
		}
	}
	if n == 0 {
		return "- Package rules: none pre-loaded"
	}
	kb := func(b float64) string { return strconv.FormatFloat(b/1024, 'f', 1, 64) }
	return fmt.Sprintf("- Package rules: %d of %d agent runs pre-loaded rules, %s KB a run, most %s KB (%s, %s)",
		n, len(rules), kb(float64(total)/float64(n)), kb(float64(most.Bytes)), most.Name, most.Role)
}
