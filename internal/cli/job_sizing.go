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
	if line := sizingLine(v); line != "" {
		fmt.Fprintln(out, line)
	}
	if w := memoryWarning(v); w != "" {
		fmt.Fprintln(out, w)
	}
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
	return fmt.Sprintf("sized %s (%s)", v.Claim, text)
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
// "—" while the job is being sized, a "!" after a memory held to the cap.
func claimText(v core.JobView) string {
	if v.Sizing != "" {
		return "—"
	}
	text := v.Claim.String()
	if v.AboveCap {
		text = strings.Replace(text, "G", "G!", 1)
	}
	if tag := v.Sources.Tag(v.Learned); tag != "" {
		text += " " + tag
	}
	return text
}
