package ui

import (
	"cmp"
	"fmt"
	"image/color"
	"slices"
	"strings"
	"time"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

// CardDensity selects how much of an instance a card shows.
type CardDensity int

const (
	// DensityLine is a one-line row (height-starved fallback).
	DensityLine CardDensity = iota
	// DensityRail is the focus-mode rail mini-card: title line + one
	// tail/status line, with a left accent bar.
	DensityRail
	// DensityCard is the overview card: bordered box with metadata and
	// a multi-line tail (rendered by Overview in overview.go).
	DensityCard
)

// Rail layout constants (used by List height math).
const (
	// RailCardLines is lines per DensityRail card incl. trailing gap.
	RailCardLines = 3
	// RailHeaderLines is the section-label header at the top of the rail.
	RailHeaderLines = 2
)

// railStatusFloor is how many columns the rail's status phrase keeps
// before the GitHub token may claim any. The status says what the user
// must do (see the wait-reason note above); the token is context.
//
// Measured against the 20% rail ratio: for the common token (a linked
// issue plus an open PR) this floor is the binding constraint, and 6
// rather than 8 is what makes it appear on a ~95-column terminal
// instead of ~105. For a fully loaded token the token's own length
// dominates and no floor helps, which is why it is dropped whole.
const railStatusFloor = 6

// railAccountBadgeMaxWidth caps the title line's "@name" badge. The cap,
// not the badge's actual (name-dependent) width, is what the show/hide
// decision in RenderCard reserves room for — see railAccountBadgeMinTitle.
const railAccountBadgeMaxWidth = 10

// railAccountBadgeMinTitle is how many cells the title must keep, after
// its "N. " prefix, for the account badge to show at all. RenderCard
// checks this against railAccountBadgeMaxWidth rather than the badge's
// actual width, so the decision depends only on the card's width, never
// on which particular account name happens to be shorter or longer —
// every card on the rail must drop (or keep) its badge the same way at a
// given width.
const railAccountBadgeMinTitle = 12

// showAccounts turns account badges on. app sets it (SetShowAccounts)
// while an extra account is registered. Main goroutine only.
var showAccounts bool

// SetShowAccounts turns account badges on or off.
func SetShowAccounts(on bool) { showAccounts = on }

// ShowAccounts reports whether account badges are on.
func ShowAccounts() bool { return showAccounts }

// accountLabel is inst's account badge text: its account's name
// (account.DefaultName for the default account), or "" when badges are off
// or inst is not a Claude session.
func accountLabel(inst *session.Instance) string {
	if !showAccounts || inst == nil || !session.IsClaudeProgram(inst.Program()) {
		return ""
	}
	if name := inst.Account(); name != "" {
		return name
	}
	return account.DefaultName
}

// accountToken is the rail's dim "@name" badge, "" without an account.
// The raw label is capped to railAccountBadgeMaxWidth before styling, so
// a long account name never grows the badge past what RenderCard's
// show/hide decision reserved room for.
func accountToken(d CardData, solidBg bool) string {
	if d.Account == "" {
		return ""
	}
	st := lipgloss.NewStyle().Foreground(Dim)
	if solidBg {
		st = st.Background(Panel)
	}
	return st.Render(truncate("@"+d.Account, railAccountBadgeMaxWidth))
}

// PeerSection summarizes a non-focused workspace slot for the rail
// footer (live counts from that slot's list; selection stays scoped to
// the focused workspace until cross-workspace lands).
type PeerSection struct {
	Name      string
	Attention int // Prompting or bell-pending
	Running   int // Running/Loading
	Idle      int // everything else
}

// CardData is the render-ready view-model for one instance card. It is
// plain data so RenderCard stays a pure, table-testable function; build
// it from a live instance with BuildCardData.
type CardData struct {
	Title               string
	Index               int // 1-based display number (0 for workspace terminal)
	Status              session.Status
	IsWorkspaceTerminal bool
	BellPending         bool
	Selected            bool
	Branch              string
	DiffAdded           int
	DiffRemoved         int
	HasDiff             bool
	TailLines           []string
	StatusAge           time.Duration // 0 = unknown/not applicable
	Spinner             string        // current spinner frame for Running/Loading
	// WaitReason is Claude's own account of what a Prompting session is
	// blocked on ("permission: Bash", "sandbox request"), from its hooks
	// or the agent roster, via sanitizeCardText. Empty when unknown —
	// non-Claude agents, and any status the pane scraper decided.
	WaitReason string
	// Subagents lists the session's live subagents and teammates, working
	// first, with names and descriptions passed through sanitizeCardText.
	// Empty for most cards; see session.Instance.Subagents.
	Subagents []SubagentRow
	// GitHub is the poller's join for this session (issue, PR, checks).
	// Known=false renders nothing. See app/github.go.
	GitHub github.State
	// Ahead/Behind count commits relative to the base branch; HasParity
	// is false until a count succeeded.
	Ahead, Behind int
	HasParity     bool
	// Account is the Claude account badge ("max-2", "default"); empty when
	// badges are off (no extra account) or the session isn't Claude.
	Account string
}

// NeedsAttention reports whether this card should carry the Attention
// accent — the only loud signal in the UI. A Deleting instance never
// needs attention: a stale bell on a mid-kill card must not paint it
// gold or float it into the attention tier.
func (d CardData) NeedsAttention() bool {
	return d.Status != session.Deleting && (d.Status == session.Prompting || d.BellPending)
}

// BuildCardData snapshots inst into a CardData. spinnerFrame is the
// current spinner view (pass "" when unavailable). tailN caps the live
// tail; 0 skips the screen read entirely (DensityLine callers). The
// tail comes from AgentPane.EmulatorScreen — in-memory only, so calling
// this per visible card per frame forks no subprocesses; snapshot-path
// instances simply render their status label instead of a tail. When
// Claude's last message is current and the session is not working, the
// tail is the end of that message instead, on either path.
func BuildCardData(inst *session.Instance, selected bool, spinnerFrame string, tailN int) CardData {
	d := CardData{
		Title:               inst.Title,
		Status:              inst.GetStatus(),
		IsWorkspaceTerminal: inst.IsWorkspaceTerminal,
		BellPending:         inst.BellPending(),
		Selected:            selected,
		Branch:              inst.GetBranch(),
		StatusAge:           inst.StatusAge(),
		Spinner:             spinnerFrame,
		WaitReason:          sanitizeCardText(inst.WaitReason()),
		Account:             accountLabel(inst),
	}
	for _, v := range inst.Subagents() {
		d.Subagents = append(d.Subagents, SubagentRow{
			Name:        sanitizeCardText(v.Name),
			Description: sanitizeCardText(v.Description),
			Idle:        v.Idle,
		})
	}
	if stat := inst.GetDiffStats(); stat != nil && stat.Error == nil && !stat.IsEmpty() {
		d.HasDiff, d.DiffAdded, d.DiffRemoved = true, stat.Added, stat.Removed
	}
	d.GitHub = inst.GitHubState()
	d.GitHub.IssueTitle = sanitizeCardText(d.GitHub.IssueTitle)
	// Before the first poll the join is empty, but the link itself is
	// known from the instance — show "#12" immediately.
	if !d.GitHub.Known && inst.IssueNumber() != 0 {
		d.GitHub.IssueNumber = inst.IssueNumber()
	}
	d.Ahead, d.Behind, d.HasParity = inst.Parity()
	if tailN > 0 {
		if msg, current := inst.LastMessage(); current && msg != "" &&
			d.Status != session.Running && d.Status != session.Loading {
			// What Claude said it did, or is asking, says more than the
			// screen's tail once the session stops.
			d.TailLines = MessageTailLines(msg, tailN)
		} else if screen, ok := inst.Pane().EmulatorScreen(); ok {
			d.TailLines = ContentTailLines(screen, tailN)
		}
	}
	return d
}

// TailLines returns the last n non-blank-tail lines of screen with ANSI
// styling stripped (card chrome owns the styling; embedded SGR would
// bleed). C0 controls survive ansi.Strip, so tabs are normalized to a
// single space and carriage returns dropped — emulator output never
// contains them today, but the helper is exported and generic. Returns
// nil for an effectively empty screen.
func TailLines(screen string, n int) []string {
	if screen == "" {
		return nil
	}
	lines := strings.Split(screen, "\n")
	end := len(lines)
	for end > 0 && strings.TrimSpace(ansi.Strip(lines[end-1])) == "" {
		end--
	}
	if end == 0 {
		return nil
	}
	start := end - n
	if start < 0 {
		start = 0
	}
	out := make([]string, 0, end-start)
	for _, l := range lines[start:end] {
		out = append(out, sanitizeTailLine(l))
	}
	return out
}

// MessageTailLines returns the last n lines of a message Claude wrote, for
// a card's tail: blank and code-fence lines are dropped, a heading's
// leading #s trimmed, and every line sanitized, since the text is
// model-written. Returns nil when nothing is left or n < 1.
func MessageTailLines(msg string, n int) []string {
	if n < 1 {
		return nil
	}
	var lines []string
	for _, l := range strings.Split(msg, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "```") {
			continue
		}
		if h := strings.TrimLeft(t, "#"); h != t && (h == "" || h[0] == ' ') {
			t = strings.TrimSpace(h)
		}
		if t = strings.TrimSpace(sanitizeCardText(t)); t != "" {
			lines = append(lines, t)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// sanitizeTailLine strips ANSI styling and normalizes C0 controls the
// same way TailLines documents: tabs become a single space, carriage
// returns are dropped.
func sanitizeTailLine(l string) string {
	l = ansi.Strip(l)
	l = strings.ReplaceAll(l, "\t", " ")
	return strings.ReplaceAll(l, "\r", "")
}

// sanitizeCardText makes text the model or Claude wrote safe to print on
// a card: ANSI sequences are stripped, then every remaining control
// character (C0 including LF, CR and TAB; DEL; C1 including NEL) becomes
// a single space. Subagent names and descriptions come from metadata
// files the model writes, so unsanitized they could emit terminal escapes
// (an OSC 52 clipboard write, say) or break a card's fixed height with a
// newline.
func sanitizeCardText(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, ansi.Strip(s))
}

// Chrome scan bounds for ContentTailLines. Claude Code's footer below
// the input area is statusline, context meter, and mode line, plus an
// agents strip that grows one line per running subagent; legacy builds
// used one line. The input area grows with multiline typed input.
const (
	chromeFooterMax = 12 // non-delimiter lines allowed below the bottom delimiter
	chromeInputMax  = 8  // interior lines allowed between the delimiters
)

// chromeDelimiter reports whether a sanitized line is an input-area
// delimiter: a horizontal rule (the current Claude Code UI) or a
// box-border line (the legacy rounded-box UI) — nothing but horizontal
// box-drawing runes and corners, at least 4 of them.
func chromeDelimiter(s string) bool {
	s = strings.TrimSpace(s)
	runes := 0
	for _, r := range s {
		switch r {
		case '─', '━', '═', '╌', '╍', '┄', '┅', '┈', '┉',
			'╭', '╮', '╰', '╯', '┌', '┐', '└', '┘':
			runes++
		default:
			return false
		}
	}
	return runes >= 4
}

// tipHead reports whether a sanitized line opens one of the agent's
// transient hint blocks: "⎿  Tip: …" attached under the working spinner
// or "※ Tip: …" on idle screens. These are harness chrome, not
// conversation; ordinary "⎿" tool-result lines stay content.
func tipHead(s string) bool {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "⎿")
	s = strings.TrimPrefix(s, "※")
	return strings.HasPrefix(strings.TrimSpace(s), "Tip:")
}

// indentWidth counts a line's leading whitespace runes. A wrapped tip's
// continuation lines are indented deeper than their head's marker, which
// is how the whole block is recognized.
func indentWidth(s string) int {
	n := 0
	for _, r := range s {
		if !unicode.IsSpace(r) {
			break
		}
		n++
	}
	return n
}

// chromeInteriorText reduces an input-area interior line to its
// significant content: side borders (legacy box) and a leading prompt
// char are stripped. An empty result means the line is chrome (an idle
// prompt), not content.
func chromeInteriorText(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "│")
	s = strings.TrimSuffix(s, "│")
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "❯")
	s = strings.TrimPrefix(s, ">")
	return strings.TrimSpace(s)
}

// ContentTailLines is TailLines minus trailing agent chrome. Coding-agent
// TUIs (Claude Code and friends) pin an input area — delimited by
// horizontal rules or a box — plus footer lines to the bottom of the
// screen, so a bottom-anchored tail shows only that chrome and never the
// conversation. This scans the trailing lines for that shape and anchors
// the tail above it; a dialog rendered inside the input area (permission
// prompt) is real content, so its interior is returned instead. Any
// screen without the shape (shells, aider, half-drawn panes) falls back
// to the plain TailLines behavior.
func ContentTailLines(screen string, n int) []string {
	if screen == "" {
		return nil
	}
	raw := strings.Split(screen, "\n")
	lines := make([]string, len(raw))
	for i, l := range raw {
		lines[i] = sanitizeTailLine(l)
	}
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	if end == 0 {
		return nil
	}

	// Bottom delimiter: scan up through the footer (blank lines free,
	// non-delimiter lines bounded by chromeFooterMax).
	bottom := -1
	footer := 0
	for i := end - 1; i >= 0 && footer < chromeFooterMax; i-- {
		t := strings.TrimSpace(lines[i])
		if t == "" {
			continue
		}
		if chromeDelimiter(t) {
			bottom = i
			break
		}
		footer++
	}
	if bottom < 0 {
		return TailLines(screen, n)
	}

	// Top delimiter: within chromeInputMax interior lines above.
	top := -1
	for i := bottom - 1; i >= 0 && bottom-1-i < chromeInputMax; i-- {
		if chromeDelimiter(strings.TrimSpace(lines[i])) {
			top = i
			break
		}
	}
	if top < 0 {
		return TailLines(screen, n)
	}

	// A dialog inside the input area is the informative content.
	var dialog []string
	for _, l := range lines[top+1 : bottom] {
		if chromeInteriorText(l) != "" {
			dialog = append(dialog, l)
		}
	}
	if len(dialog) > 0 {
		if len(dialog) > n {
			dialog = dialog[len(dialog)-n:]
		}
		return dialog
	}

	// Idle input area: tail is the content above it. Blank lines carry
	// no signal on a 1-2 line card, and neither do the agent's transient
	// "Tip:" hints — both are skipped, a wrapped tip's indented
	// continuation lines going with their head. Take the last n of what
	// remains (unlike the plain-TailLines fallback, which preserves
	// layout).
	var out []string
	for i := 0; i < top; i++ {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		if tipHead(lines[i]) {
			ind := indentWidth(lines[i])
			for i+1 < top && strings.TrimSpace(lines[i+1]) != "" &&
				indentWidth(lines[i+1]) > ind && !tipHead(lines[i+1]) {
				i++
			}
			continue
		}
		out = append(out, lines[i])
	}
	if len(out) == 0 {
		return nil
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}

// accentColor returns the left-bar/border accent for a card:
// attention > selected > workspace-terminal > running (OK, the green
// border the approved mockups give active cards) > Rule.
func (d CardData) accentColor() color.Color {
	switch {
	case d.NeedsAttention():
		return Attention
	case d.Selected:
		return Accent
	case d.IsWorkspaceTerminal:
		return Workspace
	case d.Status == session.Running || d.Status == session.Loading:
		return OK
	default:
		return Rule
	}
}

// statusLabel is the human status phrase for the second line / card
// corner: "❯ awaiting input · 4m", "✻ working", "paused · 3d", …
func (d CardData) statusLabel() string {
	age := formatAge(d.StatusAge)
	switch d.Status {
	case session.Prompting:
		// Claude's own reason for the block ("sandbox request") replaces
		// the generic phrase rather than joining it: "awaiting input" only
		// restates what the attention accent already signals, while the
		// reason is the part that tells the user what to do.
		phrase := "awaiting input"
		if reason := strings.TrimSpace(d.WaitReason); reason != "" {
			phrase = reason
		}
		if age != "" {
			return "❯ " + phrase + " · " + age
		}
		return "❯ " + phrase
	case session.Running, session.Loading:
		return d.Spinner + " working"
	case session.Ready:
		if age != "" {
			return "✓ idle " + age
		}
		return "✓ idle"
	case session.Paused:
		if age != "" {
			return "paused · " + age
		}
		return "paused"
	case session.Recoverable:
		return "⟲ recoverable"
	case session.Deleting:
		return "✕ deleting"
	}
	return ""
}

// formatAge renders a duration as a compact age ("40s", "4m", "1h12m",
// "3d"); "" for zero/negative.
func formatAge(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		h := int(d.Hours())
		m := int(d.Minutes()) - h*60
		if m == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh%dm", h, m)
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// spreadLineBg is spreadLine, except the gap between l and r carries the
// Panel background when filled is true. A selected rail card paints a
// solid Panel background by having every styled run set it individually
// (see the solidBg note on RenderCard); spreadLine's gap is plain
// spaces, which without this would leave an unpainted seam between a
// right-aligned badge/token and the text to its left — a striped look.
func spreadLineBg(l, r string, width int, filled bool) string {
	if !filled {
		return spreadLine(l, r, width)
	}
	gap := width - lipgloss.Width(l) - lipgloss.Width(r)
	if gap < 0 {
		gap = 0
	}
	pad := ""
	if gap > 0 {
		pad = lipgloss.NewStyle().Background(Panel).Render(strings.Repeat(" ", gap))
	}
	return l + pad + r
}

// truncate cuts s to at most width cells, appending an ellipsis when
// content is dropped (runewidth.Truncate reserves the tail's width
// itself, so the full budget is passed through).
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if runewidth.StringWidth(s) <= width {
		return s
	}
	if width <= 1 {
		return runewidth.Truncate(s, width, "")
	}
	return runewidth.Truncate(s, width, "…")
}

// RenderCard renders d at the given density and total width. DensityCard
// is rendered by Overview (overview.go) which owns border/grid layout;
// RenderCard handles DensityLine and DensityRail.
func RenderCard(d CardData, density CardDensity, width int) string {
	if width < 4 {
		width = 4
	}
	// Selected rail cards paint a solid Panel background. It has to
	// ride the inner styles: wrapping the assembled line in a
	// Background style does not survive the embedded SGR resets
	// (lipgloss does not re-inject the outer background after a reset,
	// leaving the bg only on the bar and padding — a striped look).
	solidBg := d.Selected && density == DensityRail
	barStyle := lipgloss.NewStyle().Foreground(d.accentColor())
	sepStyle := lipgloss.NewStyle()
	titleFg := Text
	if d.NeedsAttention() {
		titleFg = Attention
	}
	if d.IsWorkspaceTerminal && !d.NeedsAttention() {
		titleFg = Workspace
	}
	titleStyleC := lipgloss.NewStyle().Foreground(titleFg)
	if d.Selected {
		titleStyleC = titleStyleC.Bold(true)
	}
	if solidBg {
		barStyle = barStyle.Background(Panel)
		sepStyle = sepStyle.Background(Panel)
		titleStyleC = titleStyleC.Background(Panel)
	}
	bar := barStyle.Render("▌")
	sep := sepStyle.Render(" ")

	prefix := fmt.Sprintf("%d. ", d.Index)
	inner := width - 2 // bar + space
	// The account badge rides the title line's right edge. The decision
	// to show it reserves railAccountBadgeMaxWidth — the badge's cap, not
	// its actual width — so a short account name can't sneak the badge
	// onto a card too narrow for a long one; see railAccountBadgeMinTitle.
	acct := accountToken(d, solidBg)
	if acct != "" && inner-lipgloss.Width(prefix)-railAccountBadgeMaxWidth-1 < railAccountBadgeMinTitle {
		acct = ""
	}
	titleW := inner
	if acct != "" {
		titleW = inner - lipgloss.Width(acct) - 1
	}
	title := truncate(prefix+d.Title, titleW)
	composeTitle := func(st lipgloss.Style) string {
		if acct == "" {
			return bar + sep + st.Render(title)
		}
		return bar + sep + spreadLineBg(st.Render(title), acct, inner, solidBg)
	}
	titleLine := composeTitle(titleStyleC)

	if density == DensityLine {
		return titleLine
	}

	// Second line: attention prompt, then live agents, then tail, then
	// status label. The agent count is a suffix, so end-truncation drops
	// it before the status.
	second := d.statusLabel()
	secondFg := Dim
	switch {
	case d.NeedsAttention():
		secondFg = Attention
		second += d.agentSummary()
	case len(d.Subagents) > 0:
		second += d.agentSummary()
	case len(d.TailLines) > 0:
		second = d.TailLines[len(d.TailLines)-1]
	}
	secondStyle := lipgloss.NewStyle().Foreground(secondFg)
	if solidBg {
		secondStyle = secondStyle.Background(Panel)
	}
	tok := railGitHubToken(d)
	// spreadLine zeroes its gap but does not shorten an over-wide right
	// side, so the token must be clamped here or the composed line
	// overflows the card. It is dropped whole rather than truncated: a
	// partial "PR ✓" could be the head of "PR ✓✗" (approved, checks
	// failing), which reverses the meaning rather than abbreviating it.
	if tok != "" && lipgloss.Width(tok) > inner-railStatusFloor-1 {
		tok = ""
	}
	body := second
	if tok != "" {
		body = truncate(second, inner-lipgloss.Width(tok)-1)
		secondLine := bar + sep + spreadLineBg(secondStyle.Render(body), tok, inner, solidBg)
		if d.finished() && !d.NeedsAttention() {
			titleLine = composeTitle(titleStyleC.Foreground(Dim))
		}
		if solidBg {
			pad := lipgloss.NewStyle().Background(Panel).Width(width)
			return pad.Render(titleLine) + "\n" + pad.Render(secondLine)
		}
		return titleLine + "\n" + secondLine
	}
	secondLine := bar + sep + secondStyle.Render(truncate(body, inner))

	if solidBg {
		// Outer style only right-pads to full width (padding spaces
		// carry the background); the visible runs above own their own.
		pad := lipgloss.NewStyle().Background(Panel).Width(width)
		return pad.Render(titleLine) + "\n" + pad.Render(secondLine)
	}
	return titleLine + "\n" + secondLine
}

// SortForOverview returns display order (indices into items) for the
// overview grid and overview cursor movement: workspace terminal pinned
// first, then attention > running/loading > ready > paused/recoverable,
// stable by title within a tier. Deleting sorts last.
func SortForOverview(items []*session.Instance) []int {
	// Tiers are computed once up front so the sort sees a consistent
	// snapshot (status/bell are read under the instance lock) and each
	// instance is read exactly once instead of O(n log n) times.
	tiers := make([]int, len(items))
	for i, inst := range items {
		tiers[i] = overviewTier(inst)
	}
	order := make([]int, len(items))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int {
		if c := cmp.Compare(tiers[a], tiers[b]); c != 0 {
			return c
		}
		return strings.Compare(items[a].Title, items[b].Title)
	})
	return order
}

// overviewTier maps an instance to its SortForOverview tier.
func overviewTier(inst *session.Instance) int {
	if inst.IsWorkspaceTerminal {
		return 0
	}
	st := inst.GetStatus()
	switch {
	case st == session.Deleting:
		// Checked before the bell: a stale bell on a mid-kill instance
		// must not float it into the attention tier.
		return 5
	case st == session.Prompting || inst.BellPending():
		return 1
	case st == session.Running || st == session.Loading:
		return 2
	case st == session.Ready:
		return 3
	case st == session.Paused || st == session.Recoverable:
		return 4
	default: // Deleting
		return 5
	}
}
