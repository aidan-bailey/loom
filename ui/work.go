package ui

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/sched"
	"github.com/charmbracelet/x/ansi"
)

// WorkRecentMax is how many finished jobs the work view's RECENT section
// lists: the newest ten.
const WorkRecentMax = 10

// workBarCells is the width of a capacity bar.
const workBarCells = 14

// The fixed columns of a job row.
const (
	workIDW    = 4
	workClaimW = 11
	workSrcW   = 10 // the claim's source: learned·N, agent, yours, default
	workTimeW  = 6
)

// workFixed is what a row spends on the marker, the ID, the claim, the time
// and the gaps between the columns: the rest goes to the description, the
// owner and the last column.
const workFixed = 1 + 1 + 1 + workIDW + 2 + 2 + workClaimW + 2 + workSrcW + 2 + workTimeW + 2

var workTitleStyle, workSectionStyle, workDimStyle, workSelectedStyle lipgloss.Style

var workOKStyle, workErrStyle, workFillStyle, workEmptyStyle lipgloss.Style

func init() { RegisterThemeHook(rebuildWorkStyles) }

func rebuildWorkStyles() {
	workTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(Accent)
	workSectionStyle = lipgloss.NewStyle().Bold(true).Foreground(Dim)
	workDimStyle = lipgloss.NewStyle().Foreground(Dim)
	workSelectedStyle = lipgloss.NewStyle().Background(SelectionBg).Foreground(SelectionFg)
	workOKStyle = lipgloss.NewStyle().Foreground(OK)
	workErrStyle = lipgloss.NewStyle().Foreground(ErrorColor)
	workFillStyle = lipgloss.NewStyle().Foreground(Accent)
	workEmptyStyle = lipgloss.NewStyle().Foreground(Faint)
}

// WorkTail is the selected job's last log lines as the app read them, with
// the controls cleaned off (LogLines). ID names the job they belong to.
type WorkTail struct {
	ID    sched.JobID
	Lines []string
	Err   string
}

// WorkData is everything RenderWork draws, assembled by the app on the
// Update goroutine each frame.
type WorkData struct {
	Queue    core.QueueView
	Selected sched.JobID
	Tail     WorkTail
	Now      time.Time
}

// WorkPagerData is everything RenderWorkPager draws: the job, the log's
// lines (cleaned, LogLines), the first line shown, whether the log's start
// is cut off, and why the log could not be read.
type WorkPagerData struct {
	Job     core.JobView
	Lines   []string
	Top     int
	Omitted bool
	Err     string
}

// WorkReported reports whether the daemon has published its queue: a view
// with no host, no directory and no jobs is the zero value a TUI holds
// before the first QueueChanged.
func WorkReported(q core.QueueView) bool {
	return q.Host.Name != "" || q.Dir != "" || len(q.Jobs) > 0
}

// WorkRows is the jobs in the order the work view lists them, and so the
// order its selection walks: the running ones, the queued ones, then the
// newest WorkRecentMax finished, each section in the order jobs gives
// (running by start, queued in queue order, finished newest first).
func WorkRows(jobs []core.JobView) []core.JobView {
	var running, queued, finished []core.JobView
	for _, j := range jobs {
		switch {
		case j.State == sched.Running:
			running = append(running, j)
		case j.State == sched.Queued:
			queued = append(queued, j)
		case j.State.Finished():
			finished = append(finished, j)
		}
	}
	if len(finished) > WorkRecentMax {
		finished = finished[:WorkRecentMax]
	}
	rows := make([]core.JobView, 0, len(running)+len(queued)+len(finished))
	rows = append(rows, running...)
	rows = append(rows, queued...)
	return append(rows, finished...)
}

// CleanText makes text an agent wrote (a job's description, a session's
// title) safe to print: escape sequences are stripped and every control
// character, a newline included, becomes a space.
func CleanText(s string) string { return sanitizeCardText(s) }

// JobLabel names a job in a prompt, as `job 14 "polyoxide tests"`: the
// description cleaned and cut to 60 columns.
func JobLabel(j core.JobView) string {
	return fmt.Sprintf(`job %d "%s"`, j.ID, ansi.Truncate(CleanText(j.Description), 60, "…"))
}

// LogLines turns the bytes of a log's end into display lines: escape
// sequences stripped, tabs four spaces, other controls dropped, and a line
// a progress bar rewrote with carriage returns reduced to its last state.
// partial says the bytes start mid-file, so their first line is cut and is
// dropped, unless it is the only line: a log whose whole window is one
// line (a progress bar redrawn with carriage returns) shows that line. A
// final newline ends the last line rather than starting another.
func LogLines(data []byte, partial bool) []string {
	if len(data) == 0 {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if n := len(lines); lines[n-1] == "" {
		lines = lines[:n-1]
	}
	if partial && len(lines) > 1 {
		lines = lines[1:]
	}
	if len(lines) == 0 {
		return nil
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		last := ""
		for _, seg := range strings.Split(l, "\r") {
			if seg != "" {
				last = seg
			}
		}
		out[i] = cleanLogLine(last)
	}
	return out
}

// cleanLogLine strips a log line's escape sequences and controls.
func cleanLogLine(l string) string {
	l = strings.ReplaceAll(ansi.Strip(l), "\t", "    ")
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, l)
}

// RenderWork draws the work view in a width by height box: the header, the
// capacity bars (or the reason admission is paused), the job sections with
// the selected job marked, and the selected job's tail. Every line is cut
// to the width and the result is exactly height lines.
func RenderWork(width, height int, d WorkData) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	q := d.Queue
	reported := WorkReported(q)
	out := []string{workHeader(width, q)}
	switch {
	case !reported:
		out = append(out, " "+workDimStyle.Render("The daemon has not reported its work queue yet."))
	case q.Paused != "":
		out = append(out, " "+workErrStyle.Render("Admission paused: "+CleanText(q.Paused)))
	default:
		out = append(out, workBars(q)...)
	}
	switch {
	case reported && q.Learning != "":
		out = append(out, " "+workErrStyle.Render("learning off: "+CleanText(q.Learning)))
	case reported && q.Agent.Why != "":
		out = append(out, " "+workDimStyle.Render("agent off: "+CleanText(q.Agent.Why)))
	}

	rows := WorkRows(q.Jobs)
	var selected core.JobView
	haveSel := false
	for _, j := range rows {
		if j.ID == d.Selected {
			selected, haveSel = j, true
		}
	}

	// The list takes what the header leaves, less a tail of up to eight
	// lines under its own rule when there is room for both.
	rest := height - len(out)
	tailN, listH := 0, rest
	if rest >= 6 {
		tailN = min(8, max(1, (rest-1)/3))
		listH = rest - 1 - tailN
	}

	cols := workColumns(width)
	var lines []string
	selLine := 0
	section := ""
	for _, j := range rows {
		if s := workSection(j.State); s != section {
			lines = append(lines, " "+workSectionStyle.Render(s))
			section = s
		}
		if j.ID == d.Selected {
			selLine = len(lines)
		}
		lines = append(lines, workRow(j, cols, d.Now, j.ID == d.Selected))
	}
	if len(rows) == 0 && reported {
		lines = []string{" " + workDimStyle.Render(`No jobs yet. A session submits one with: loom job submit -d "<what>" -- <cmd>`)}
	}
	shown := workWindow(lines, selLine, listH)
	out = append(out, shown...)
	for i := len(shown); i < listH; i++ {
		out = append(out, "")
	}

	if tailN > 0 {
		out = append(out, workRule(width, workTailTitle(selected, haveSel)))
		body := workTailBody(selected, haveSel, d.Tail, tailN)
		for _, l := range body {
			out = append(out, " "+l)
		}
		for i := len(body); i < tailN; i++ {
			out = append(out, "")
		}
	}
	return workBox(out, width, height)
}

// WorkPagerBody is how many log lines the pager shows in a box height
// lines tall: all but its title line.
func WorkPagerBody(height int) int { return max(1, height-1) }

// workPagerHint is the pager title's right-hand hint.
const workPagerHint = "esc back "

// workPagerCommand is the job's command and folder as the tail of the pager's
// title, cleaned of escapes and control characters like every string an agent
// wrote, and cut to the room columns the rest of the title leaves; nothing
// when the job carries neither or the room is under a few columns.
func workPagerCommand(room int, j core.JobView) string {
	var parts []string
	if cmd := CleanText(strings.Join(j.Argv, " ")); strings.TrimSpace(cmd) != "" {
		parts = append(parts, cmd)
	}
	if cwd := CleanText(j.Cwd); strings.TrimSpace(cwd) != "" {
		parts = append(parts, "in "+cwd)
	}
	if len(parts) == 0 || room < 8 {
		return ""
	}
	return ansi.Truncate(" · "+strings.Join(parts, " · "), room, "…")
}

// RenderWorkPager draws the log pager in a width by height box: a title
// line naming the job, the lines shown and the job's command and folder,
// then the window of the log.
func RenderWorkPager(width, height int, d WorkPagerData) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	body := WorkPagerBody(height)
	top := max(0, min(d.Top, len(d.Lines)-body))
	end := min(len(d.Lines), top+body)

	title := fmt.Sprintf(" job %d · %s", d.Job.ID, CleanText(d.Job.Description))
	if d.Err == "" && len(d.Lines) > 0 {
		title += fmt.Sprintf(" · lines %d–%d of %d", top+1, end, len(d.Lines))
		if d.Omitted {
			title += " (earlier output not shown)"
		}
	}
	title += workPagerCommand(width-ansi.StringWidth(title)-ansi.StringWidth(workPagerHint)-1, d.Job)
	out := []string{workTitleStyle.Render(title) + workRightHint(width, title, workPagerHint)}
	switch {
	case d.Err != "":
		out = append(out, " "+workErrStyle.Render("cannot read the log: "+CleanText(d.Err)))
	case len(d.Lines) == 0:
		out = append(out, " "+workDimStyle.Render("(no output yet)"))
	default:
		for _, l := range d.Lines[top:end] {
			out = append(out, " "+l)
		}
	}
	return workBox(out, width, height)
}

// workBox makes lines exactly height lines of at most width columns and
// joins them.
func workBox(lines []string, width, height int) string {
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, width, "")
	}
	return strings.Join(lines, "\n")
}

// workRightHint is the spaces and hint that put hint at the right edge
// after text, or nothing when they would not fit.
func workRightHint(width int, text, hint string) string {
	pad := width - ansi.StringWidth(text) - ansi.StringWidth(hint)
	if pad < 1 {
		return ""
	}
	return strings.Repeat(" ", pad) + workDimStyle.Render(hint)
}

func workHeader(width int, q core.QueueView) string {
	left := " Work queue"
	if q.Host.Name != "" {
		left += " · " + CleanText(q.Host.Name)
	}
	return workTitleStyle.Render(left) + workRightHint(width, left, "w/esc back ")
}

// workBars draws the claimed totals against the caps, and what the running
// jobs measure beside them.
func workBars(q core.QueueView) []string {
	caps := q.Policy.Caps
	cpu := fmt.Sprintf("%d/%d claimed · %.1f in use", q.Claimed.CPUs, caps.CPUs, q.UsedCPU)
	mem := fmt.Sprintf("%d/%d GB claimed · %s in use", q.Claimed.MemoryGB, caps.MemoryGB, workGB(q.UsedRSSKB))
	io := fmt.Sprintf("%d/%d", q.Claimed.IOSlots, caps.IOJobs)
	return []string{
		workBarLine("CPU", q.Claimed.CPUs, caps.CPUs, workBarCells, cpu),
		workBarLine("MEM", q.Claimed.MemoryGB, caps.MemoryGB, workBarCells, mem),
		workBarLine("I/O", q.Claimed.IOSlots, caps.IOJobs, min(workBarCells, caps.IOJobs*2), io),
	}
}

func workBarLine(label string, claimed, capacity, cells int, text string) string {
	cells = max(0, min(cells, workBarCells))
	fill := 0
	if capacity > 0 {
		fill = (claimed*cells + capacity/2) / capacity
	}
	fill = max(0, min(cells, fill))
	bar := workFillStyle.Render(strings.Repeat("█", fill)) + workEmptyStyle.Render(strings.Repeat("░", cells-fill))
	return " " + label + " " + bar + strings.Repeat(" ", workBarCells-cells) + " " + text
}

func workSection(s sched.State) string {
	switch s {
	case sched.Running:
		return "RUNNING"
	case sched.Queued:
		return "QUEUED"
	}
	return "RECENT"
}

// workWindow is the n lines of lines around line sel: all of them when
// they fit, else a window that keeps sel in view.
func workWindow(lines []string, sel, n int) []string {
	if n <= 0 {
		return nil
	}
	if len(lines) <= n {
		return lines
	}
	start := max(0, min(sel-n/2, len(lines)-n))
	return lines[start : start+n]
}

func workRule(width int, title string) string {
	head := ansi.Truncate("── "+title+" ", width, "")
	return workDimStyle.Render(head + strings.Repeat("─", max(0, width-ansi.StringWidth(head))))
}

func workTailTitle(j core.JobView, ok bool) string {
	if !ok {
		return "tail"
	}
	t := fmt.Sprintf("job %d · tail", j.ID)
	if j.Notice == sched.NoticeUndelivered {
		t += " · message not delivered"
		if j.NoticeWhy != "" {
			t += ": " + CleanText(j.NoticeWhy)
		}
	}
	return t
}

func workTailBody(j core.JobView, ok bool, t WorkTail, n int) []string {
	switch {
	case !ok:
		return nil
	case j.State == sched.Queued:
		return []string{workDimStyle.Render("(queued: no output yet)")}
	case t.ID != j.ID:
		return []string{workDimStyle.Render("(reading the log…)")}
	case t.Err != "":
		return []string{workErrStyle.Render("(cannot read the log: " + CleanText(t.Err) + ")")}
	case len(t.Lines) == 0:
		return []string{workDimStyle.Render("(no output yet)")}
	}
	lines := t.Lines
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// workCols are the widths of the description, owner and last columns of a
// job row. A terminal under 76 columns drops the owner.
type workCols struct{ desc, owner, extra int }

func workColumns(width int) workCols {
	if width < 76 {
		rest := width - workFixed
		extra := max(14, rest*40/100)
		return workCols{desc: max(0, rest-extra), extra: extra}
	}
	extra := max(16, width*30/100)
	rest := width - workFixed - 2 - extra
	desc := rest * 55 / 100
	return workCols{desc: desc, owner: rest - desc, extra: extra}
}

// workFit cuts s to w columns, with an ellipsis, or pads it to them.
func workFit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = ansi.Truncate(s, w, "…")
	if pad := w - ansi.StringWidth(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}

// workRow draws one job: marker, ID, description, owner, claim (or the
// outcome of a finished job), time and what the job is doing or why it
// waits. The selected row is one block of the selection colors.
func workRow(j core.JobView, c workCols, now time.Time, selected bool) string {
	marker := " "
	if selected {
		marker = "▸"
	}
	claim, claimStyle, src, timeText, extra := workRowFacts(j, now)

	var plain, styled strings.Builder
	add := func(text string, w int, st lipgloss.Style) {
		cell := workFit(text, w)
		plain.WriteString(cell)
		styled.WriteString(st.Render(cell))
	}
	gap := func() {
		plain.WriteString("  ")
		styled.WriteString("  ")
	}
	none := lipgloss.NewStyle()
	add(" "+marker+" ", 3, none)
	add(fmt.Sprintf("%d", j.ID), workIDW, none)
	gap()
	add(CleanText(j.Description), c.desc, none)
	if c.owner > 0 {
		gap()
		add(CleanText(workOwner(j)), c.owner, workDimStyle)
	}
	gap()
	add(claim, workClaimW, claimStyle)
	gap()
	add(src, workSrcW, workDimStyle)
	gap()
	add(timeText, workTimeW, workDimStyle)
	gap()
	add(CleanText(extra), c.extra, workDimStyle)
	if selected {
		return workSelectedStyle.Render(plain.String())
	}
	return styled.String()
}

func workOwner(j core.JobView) string {
	switch {
	case j.Owner == 0:
		return "no owner"
	case j.OwnerTitle == "":
		return "session gone"
	case j.OwnerWorkspace == "":
		return j.OwnerTitle
	}
	return j.OwnerTitle + " · " + j.OwnerWorkspace
}

// workRowFacts is what a row shows besides the job's name: the claim cell
// (the outcome, for a finished job) with its style, the claim's source
// (smart scheduler spec §8), the time cell, and the last cell.
func workRowFacts(j core.JobView, now time.Time) (claim string, claimStyle lipgloss.Style, src, timeText, extra string) {
	switch j.State {
	case sched.Running:
		extra = fmt.Sprintf("%.1fc  peak %s", j.CPU, workKB(j.PeakRSSKB))
		if j.CancelReason != "" {
			extra = "cancelling"
		}
		return workClaim(j), lipgloss.NewStyle(), j.Sources.Tag(j.Learned), workDur(now.Sub(j.Started)), extra
	case sched.Queued:
		extra = j.Waiting
		if extra == "" {
			extra = "waiting"
		}
		if j.Sizing != "" {
			return "—", lipgloss.NewStyle(), "sizing", workWait(now.Sub(j.Submitted)), extra
		}
		return workClaim(j), lipgloss.NewStyle(), j.Sources.Tag(j.Learned), workWait(now.Sub(j.Submitted)), extra
	}
	claim, claimStyle = workResult(j)
	timeText = "-"
	if !j.Started.IsZero() && !j.Ended.IsZero() {
		timeText = workDur(j.Ended.Sub(j.Started))
	}
	var parts []string
	if !j.Ended.IsZero() {
		parts = append(parts, workAgo(now.Sub(j.Ended)))
	}
	if j.Failure != "" {
		parts = append(parts, j.Failure)
	}
	if j.State == sched.Cancelled && j.CancelReason != "" {
		parts = append(parts, j.CancelReason)
	}
	if j.Notice == sched.NoticeUndelivered {
		parts = append(parts, "not delivered")
	}
	return claim, claimStyle, "", timeText, strings.Join(parts, " · ")
}

// workClaim is a running or queued job's claim, with a "!" after a memory
// held to the cap.
func workClaim(j core.JobView) string {
	if j.AboveCap {
		return strings.Replace(j.Claim.String(), "G", "G!", 1)
	}
	return j.Claim.String()
}

// workResult is a finished job's outcome, and the style it is drawn in.
func workResult(j core.JobView) (string, lipgloss.Style) {
	switch j.State {
	case sched.Done:
		code := 0
		if j.Exit != nil {
			code = j.Exit.Code
		}
		return fmt.Sprintf("✓ %d", code), workOKStyle
	case sched.Failed:
		switch {
		case j.StartFailed():
			return "✗ start", workErrStyle
		case j.Exit.Signal != "":
			return "✗ " + j.Exit.Signal, workErrStyle
		}
		return fmt.Sprintf("✗ %d", j.Exit.Code), workErrStyle
	case sched.Cancelled:
		return "cancelled", workDimStyle
	}
	return "lost", workErrStyle
}

// workDur is a running time: 42s as 0m42s, 190s as 3m10s, an hour and more
// as 1h05m.
func workDur(d time.Duration) string {
	d = max(0, d.Round(time.Second))
	if h := int(d / time.Hour); h > 0 {
		return fmt.Sprintf("%dh%02dm", h, int(d%time.Hour/time.Minute))
	}
	return fmt.Sprintf("%dm%02ds", int(d/time.Minute), int(d%time.Minute/time.Second))
}

// workWait is how long a queued job has waited, by the minute.
func workWait(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	}
	return fmt.Sprintf("%dh%02dm", int(d/time.Hour), int(d%time.Hour/time.Minute))
}

// workAgo is how long ago a job ended.
func workAgo(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	}
	return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
}

// workKB is a size in kilobytes as 5.1G, 640M or 12K.
func workKB(kb int64) string {
	switch {
	case kb >= 1<<20:
		return fmt.Sprintf("%.1fG", float64(kb)/(1<<20))
	case kb >= 1<<10:
		return fmt.Sprintf("%dM", kb>>10)
	}
	return fmt.Sprintf("%dK", kb)
}

// workGB is a size in kilobytes as 9.8 GB.
func workGB(kb int64) string { return fmt.Sprintf("%.1f GB", float64(kb)/(1<<20)) }
