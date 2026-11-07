package sched

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// FormatBelief is a belief as `loom job beliefs show` and the work view's
// belief pager print it: its signature, the command it runs after, claim,
// durations, how it scales, prior, notes and runs, one line each, with no
// styling.
func FormatBelief(v BeliefView) []string {
	b := v.Belief
	lines := []string{fmt.Sprintf("belief %s · %s · %s", b.ID, v.RepoName, b.Folder)}
	command := strings.Join(b.Command, " ")
	if len(b.Env) > 0 {
		command += "  (env " + strings.Join(b.Env, " ") + ")"
	}
	lines = append(lines, "command   "+command)
	if line := afterLine(v); line != "" {
		lines = append(lines, line)
	}
	if v.Family != "" {
		beliefs := "1 belief"
		if v.FamilySize != 1 {
			beliefs = fmt.Sprintf("%d beliefs", v.FamilySize)
		}
		lines = append(lines, fmt.Sprintf("family    %s (%s)", v.Family, beliefs))
	}
	if v.Sized.Claim != (Claim{}) {
		lines = append(lines, fmt.Sprintf("claim     %s (%s)", v.Sized.Claim, v.Sized.Sources.Describe(v.Sized.Learned, v.Sized.FamilyRuns, v.Family)))
	}
	switch {
	case v.DurationRuns > 0:
		lines = append(lines, fmt.Sprintf("usually   %s (p90 %s, %d runs)", RoughDuration(v.Usual), RoughDuration(v.P90), v.DurationRuns))
	case v.UsualFromFamily:
		lines = append(lines, fmt.Sprintf("about     %s (its family's runs)", RoughDuration(v.Usual)))
	case v.UsualFromPrior:
		lines = append(lines, fmt.Sprintf("about     %s (the agent's estimate)", RoughDuration(v.Usual)))
	}
	lines = append(lines, scalingLine(v))
	if p := b.Prior; p != nil {
		io := ""
		if p.IO {
			io = " io"
		}
		line := fmt.Sprintf("prior     the agent (%s, %s) %s: %dc/%dG%s, %s", p.Model, p.Effort, p.At.Local().Format("2006-01-02 15:04"),
			p.CPUs, p.MemoryGB, io, RoughDuration(time.Duration(p.DurationSeconds)*time.Second))
		var scores []string
		if p.Score.Memory > 0 {
			scores = append(scores, fmt.Sprintf("memory ×%.2f", p.Score.Memory))
		}
		if p.Score.Duration > 0 {
			scores = append(scores, fmt.Sprintf("duration ×%.2f", p.Score.Duration))
		}
		if len(scores) > 0 {
			line += " — measured " + strings.Join(scores, ", ")
		}
		lines = append(lines, line)
		if p.Note != "" {
			lines = append(lines, "          "+p.Note)
		}
	}
	for i, n := range b.Notes {
		head := "notes     "
		if i > 0 {
			head = "          "
		}
		lines = append(lines, fmt.Sprintf("%s%s job %d: %s", head, n.At.Local().Format("2006-01-02 15:04"), n.Job, n.Text))
	}
	if len(b.Runs) == 0 {
		return append(lines, "runs      none yet")
	}
	lines = append(lines, "runs      JOB   ENDED             OUTCOME    CLAIM       PEAK    BUSY  WALL      WRITTEN")
	for _, r := range b.Runs {
		peak := fmt.Sprintf("%.1fG", float64(r.PeakKB)/kbPerGB)
		if !r.FullPeak() {
			peak = "≥" + peak
		}
		busy, written := "-", "-"
		if r.CPUSeconds > 0 && r.WallSeconds > 0 {
			busy = fmt.Sprintf("%.1f", r.CPUSeconds/r.WallSeconds)
		}
		if r.WriteBytes != nil {
			written = fmt.Sprintf("%.1fG", float64(*r.WriteBytes)/(1<<30))
		}
		lines = append(lines, fmt.Sprintf("          %-5d %-17s %-10s %-11s %-7s %-5s %-9s %s",
			r.Job, r.Ended.Local().Format("2006-01-02 15:04"), r.Outcome, r.Claim, peak, busy, RoughDuration(seconds(r.WallSeconds)), written))
	}
	return lines
}

// afterLine is the belief text's line on the command its command runs after
// (dependent jobs spec §5.6), "" for one with no prerequisite part.
func afterLine(v BeliefView) string {
	switch after := v.Belief.After; {
	case after == "":
		return ""
	case after == AfterNone:
		return "after     a job with no signature"
	case v.After != nil:
		return "after     " + strings.Join(v.After, " ")
	}
	return fmt.Sprintf("after     belief %s, since forgotten", v.Belief.After)
}

// scalingLine is the belief text's line on how its command scales (job
// scaling spec §6, fit spec §5): an overrunner's busy cores and claim; or
// p, the runs or widths it came from, the ideal width and the usual
// duration at the claim's cores and at the ideal width; or that it is
// unknown.
func scalingLine(v BeliefView) string {
	s, base := v.Scaling, v.Sized.Claim.CPUs
	switch {
	case s.Overruns:
		line := fmt.Sprintf("scaling   ignores the hint variables · keeps %.1f cores busy", s.Busy)
		if base > 0 {
			line += fmt.Sprintf(", claims %d", base)
		}
		return line
	case !s.Known:
		return "scaling   unknown until a run with 2 cores or more"
	}
	from := "over 1 run"
	if s.Runs != 1 {
		from = fmt.Sprintf("over %d runs", s.Runs)
	}
	if s.fitted() {
		widths := make([]string, len(s.Widths))
		for i, w := range s.Widths {
			widths[i] = strconv.Itoa(w)
		}
		from = "from runs at " + JoinAnd(widths) + " cores"
	}
	line := fmt.Sprintf("scaling   %.0f%% parallel %s · efficient up to %d cores", 100*s.Parallel, from, s.Ideal)
	if v.DurationRuns == 0 || base < 1 {
		return line
	}
	at := func(c int) string {
		u, _, _, _ := Usual(&v.Belief, nil, c)
		return fmt.Sprintf("~%s at %d", RoughDuration(u), c)
	}
	if s.Ideal > base {
		return line + " · " + at(base) + ", " + at(s.Ideal)
	}
	return line + " · " + at(base)
}

// RoughDuration is a duration as an ETA says it: 40s, 3m, 1h05m.
func RoughDuration(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int((d+30*time.Second)/time.Minute))
	}
	return fmt.Sprintf("%dh%02dm", int(d/time.Hour), int(d%time.Hour/time.Minute))
}
