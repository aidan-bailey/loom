package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/sched"
)

// sizeWait is how long `submit` waits for a job asking the agent to be
// sized before it says the job is still being sized.
const sizeWait = 3 * time.Second

// printSizing writes what `submit` says of a job's claim (smart scheduler
// spec §7): the sized line, once the job is sized or sizeWait has passed,
// and the warning for an explicit memory claim below what the command has
// used. A daemon lost meanwhile says nothing more: the placement is out.
func (d *client) printSizing(out io.Writer, id sched.JobID) {
	var v core.JobView
	found := false
	_, _ = d.await(sizeWait, func([]core.Event) bool {
		v, found = jobOf(d.Queue(), id)
		return !found || v.Sizing == ""
	})
	if !found {
		return
	}
	line := sizingLine(v)
	if v.Sizing != "" {
		line = askingLine(d.Queue().Agent.Wait)
	}
	if line != "" {
		fmt.Fprintln(out, line)
	}
	if w := memoryWarning(v); w != "" {
		fmt.Fprintln(out, w)
	}
}

// askingLine is `submit`'s line for a job still waiting for the agent's
// prior, with how long it waits at most.
func askingLine(wait time.Duration) string {
	if wait <= 0 {
		return "being sized: asking the agent"
	}
	return fmt.Sprintf("being sized: asking the agent (up to %ds)", int(wait.Seconds()))
}

// sizingLine says how a job was sized: "" for an exclusive job and one
// queued before sizing.
func sizingLine(v core.JobView) string {
	switch {
	case v.Sizing != "":
		return "being sized: asking the agent"
	case v.Claim.Exclusive || v.Sources == (sched.ClaimSources{}):
		return ""
	case v.Unsized != "":
		return fmt.Sprintf("not sized: %s; it claims %s (the default)", v.Unsized, v.Claim)
	}
	text := v.Sources.Describe(v.Learned)
	if text == "default" && v.AgentWhy != "" {
		text += ": " + v.AgentWhy
	}
	line := fmt.Sprintf("sized %s (%s)", v.Claim.Spared(v.Spare), text)
	switch {
	case v.Usual > 0 && v.UsualFromPrior:
		line += "; about " + sched.RoughDuration(v.Usual)
	case v.Usual > 0:
		line += "; usually " + sched.RoughDuration(v.Usual)
	}
	if v.Ideal > 0 {
		line += fmt.Sprintf("; up to %d cores when idle", v.Ideal)
	}
	return line
}

// startedLine is the follower's line when a job starts (job scaling spec
// §6): how long it waited, and the spare cores it was lent.
func startedLine(v core.JobView) string {
	line := fmt.Sprintf("job %d started after %s", v.ID, rounded(v.Started.Sub(v.QueuedSince())))
	if v.After != 0 && v.Ready.After(v.Submitted) {
		// Released after its prerequisite (dependent jobs spec §6).
		line = fmt.Sprintf("job %d started %s after job %d ended", v.ID, rounded(v.Started.Sub(v.Ready)), v.After)
	}
	switch n := v.Spare.CPUs; {
	case n == 1:
		line += fmt.Sprintf(" with 1 spare core (%s)", v.Claim.Spared(v.Spare))
	case n > 1:
		line += fmt.Sprintf(" with %d spare cores (%s)", n, v.Claim.Spared(v.Spare))
	}
	return line
}

// memoryWarning warns of an explicit memory claim below the largest peak
// among the command's last runs.
func memoryWarning(v core.JobView) string {
	if v.BelowKB <= 0 || v.Sources.Memory != sched.SourceExplicit {
		return ""
	}
	return fmt.Sprintf("warning: --mem %d is below what this command has used (%.1f GB)", v.Claim.MemoryGB, float64(v.BelowKB)/(1<<20))
}

// claimText is a claim with its memory source's tag, as `list` shows it:
// "—" while the job is being sized, a "!" after a memory held to the cap,
// and a running job's hold after it when that is below its claim
// (past-peak holds spec §7).
func claimText(v core.JobView) string {
	if v.Sizing != "" {
		return "—"
	}
	text := v.Claim.Spared(v.Spare)
	if v.AboveCap {
		text = strings.Replace(text, "G", "G!", 1)
	}
	if tag := v.Sources.Tag(v.Learned); tag != "" {
		text += " " + tag
	}
	if v.State == sched.Running && v.Holds != (sched.Need{}) {
		text += " " + v.Holds.HoldText()
	}
	return text
}
