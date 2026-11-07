package flowrun

import (
	"fmt"
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
	fmt.Fprintf(&b, "- Ended: %s\n- Engine: loom flow run (flow in loom part 3b)\n\n", st.Status)
	fmt.Fprintf(&b, "## Cost\n\n%s\n\n## Stories\n\n", strings.TrimSpace(in.Cost))
	b.WriteString("| Story | Developer | Tier | Review | Commits | Status | Deviations | BLOCKED, up a tier | Escalated to manager | Review findings | Turn cap hit |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, s := range st.Stories {
		r := s.Row
		dev := r.Developer
		if dev == "" {
			dev = s.Developer
		}
		status := r.Status
		if status == "" {
			status = "not started"
		}
		cap := "no"
		if r.TurnCap {
			cap = "yes"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %d | %s | %d | %d | %d | %d/%d/%d | %s |\n",
			s.ID, dev, s.Tier, s.Review, r.Commits, status, r.Deviations, r.UpATier, r.Escalated, r.Findings[0], r.Findings[1], r.Findings[2], cap)
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
	for _, g := range st.Gates {
		var r GateResult
		if raw, ok := st.Memo["gates:"+g]; ok && unmarshal(raw, &r) {
			fmt.Fprintf(&b, "- Gates %s: %s\n", g, r.Ended)
		}
	}
	if st.Why != "" {
		fmt.Fprintf(&b, "- %s\n", st.Why)
	}
	return b.String()
}

func short(sha string) string {
	if len(sha) > 9 {
		return sha[:9]
	}
	return sha
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
