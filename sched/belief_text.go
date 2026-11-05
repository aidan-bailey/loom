package sched

import (
	"fmt"
	"strings"
	"time"
)

// FormatBelief is a belief as `loom job beliefs show` and the work view's
// belief pager print it: its signature, claim, durations, prior, notes and
// runs, one line each, with no styling.
func FormatBelief(v BeliefView) []string {
	b := v.Belief
	lines := []string{fmt.Sprintf("belief %s · %s · %s", b.ID, v.RepoName, b.Folder)}
	command := strings.Join(b.Command, " ")
	if len(b.Env) > 0 {
		command += "  (env " + strings.Join(b.Env, " ") + ")"
	}
	lines = append(lines, "command   "+command)
	if v.Sized.Claim != (Claim{}) {
		lines = append(lines, fmt.Sprintf("claim     %s (%s)", v.Sized.Claim, v.Sized.Sources.Describe(v.Sized.Learned)))
	}
	switch {
	case v.DurationRuns > 0:
		lines = append(lines, fmt.Sprintf("usually   %s (p90 %s, %d runs)", RoughDuration(v.Usual), RoughDuration(v.P90), v.DurationRuns))
	case v.UsualFromPrior:
		lines = append(lines, fmt.Sprintf("about     %s (the agent's estimate)", RoughDuration(v.Usual)))
	}
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
