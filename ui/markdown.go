package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	glamour "charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
)

// MarkdownMaxBytes caps how much of a document is fed to glamour; the
// tail beyond it is dropped with a truncation notice. Glamour renders
// synchronously on the Update goroutine (lazily, once per
// content/width/theme change), so the cap bounds that stall.
const MarkdownMaxBytes = 256 * 1024

var (
	mdHeaderStyle lipgloss.Style
	mdFollowStyle lipgloss.Style
	mdPinStyle    lipgloss.Style
	mdFaintStyle  lipgloss.Style
)

func init() { RegisterThemeHook(rebuildMarkdownPaneStyles) }

func rebuildMarkdownPaneStyles() {
	mdHeaderStyle = lipgloss.NewStyle().Foreground(Dim)
	mdFollowStyle = lipgloss.NewStyle().Foreground(OK)
	mdPinStyle = lipgloss.NewStyle().Foreground(Attention)
	mdFaintStyle = lipgloss.NewStyle().Foreground(Faint)
}

// MarkdownPane is the workbench's markdown viewer/editor: a
// glamour-rendered scrollable view with follow/pin semantics and a
// raw-text textarea edit mode. Not goroutine-safe; Update-goroutine
// only (renders lazily inside View, same discipline as
// ScrollModel.AdvanceAndRender).
type MarkdownPane struct {
	width, height int

	path   string    // absolute path shown ("" = empty state)
	raw    string    // raw content as loaded from disk
	mtime  time.Time // mtime at load — the save-conflict guard
	follow bool

	rendered      []string
	renderedGen   int // mdStyleGen the cache was built against
	renderedWidth int
	truncated     bool
	scroll        int

	editing bool
	ta      textarea.Model
}

func NewMarkdownPane() *MarkdownPane {
	ta := textarea.New()
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	ta.CharLimit = 0
	ta.MaxHeight = 0
	return &MarkdownPane{follow: true, ta: ta}
}

func (m *MarkdownPane) SetSize(width, height int) {
	if width != m.renderedWidth {
		m.rendered = nil // force re-wrap at the new width
	}
	m.width, m.height = width, height
	m.ta.SetWidth(max(width, 1))
	m.ta.SetHeight(max(height-2, 1)) // header + footer rows
}

func (m *MarkdownPane) Path() string        { return m.path }
func (m *MarkdownPane) Mtime() time.Time    { return m.mtime }
func (m *MarkdownPane) Following() bool     { return m.follow }
func (m *MarkdownPane) SetFollowing(f bool) { m.follow = f }
func (m *MarkdownPane) Editing() bool       { return m.editing }
func (m *MarkdownPane) Scroll() int         { return m.scroll }

// SetDocument replaces the shown document. A path change resets
// scroll; a same-path reload (follow refresh) preserves it, clamped.
func (m *MarkdownPane) SetDocument(path, raw string, mtime time.Time) {
	if path != m.path {
		m.scroll = 0
	}
	m.path, m.raw, m.mtime = path, raw, mtime
	m.rendered = nil
}

// Clear empties the pane (file deleted / no markdown in tree).
func (m *MarkdownPane) Clear() {
	m.path, m.raw, m.rendered, m.scroll = "", "", nil, 0
}

func (m *MarkdownPane) bodyHeight() int { return max(m.height-2, 0) }

func (m *MarkdownPane) maxScroll() int {
	return max(len(m.rendered)-m.bodyHeight(), 0)
}

func (m *MarkdownPane) clampScroll() {
	if m.scroll > m.maxScroll() {
		m.scroll = m.maxScroll()
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
}

func (m *MarkdownPane) ScrollUp()     { m.ensureRendered(); m.scroll--; m.clampScroll() }
func (m *MarkdownPane) ScrollDown()   { m.ensureRendered(); m.scroll++; m.clampScroll() }
func (m *MarkdownPane) PageUp()       { m.ensureRendered(); m.scroll -= m.bodyHeight(); m.clampScroll() }
func (m *MarkdownPane) PageDown()     { m.ensureRendered(); m.scroll += m.bodyHeight(); m.clampScroll() }
func (m *MarkdownPane) ScrollTop()    { m.scroll = 0 }
func (m *MarkdownPane) ScrollBottom() { m.ensureRendered(); m.scroll = m.maxScroll() }

// ensureRendered re-runs glamour when content, width, or theme
// generation changed. Mutation-in-render is deliberate and bounded
// (once per change), mirroring ScrollModel.AdvanceAndRender.
func (m *MarkdownPane) ensureRendered() {
	if m.path == "" || m.width <= 0 {
		m.rendered = nil
		return
	}
	if m.rendered != nil && m.renderedGen == mdStyleGen && m.renderedWidth == m.width {
		return
	}
	src := m.raw
	m.truncated = false
	if len(src) > MarkdownMaxBytes {
		src, m.truncated = src[:MarkdownMaxBytes], true
	}
	var out string
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(mdStyle),
		glamour.WithWordWrap(m.width),
	)
	if err == nil {
		out, err = r.Render(src)
	}
	if err != nil {
		m.rendered = []string{mdFaintStyle.Render("render error: " + err.Error())}
	} else {
		m.rendered = strings.Split(strings.TrimRight(out, "\n"), "\n")
		if m.truncated {
			m.rendered = append(m.rendered, mdFaintStyle.Render(
				fmt.Sprintf("… truncated at %d KB", MarkdownMaxBytes/1024)))
		}
	}
	m.renderedGen, m.renderedWidth = mdStyleGen, m.width
	m.clampScroll()
}

func (m *MarkdownPane) headerLine() string {
	badge := mdFollowStyle.Render("(following)")
	if !m.follow {
		badge = mdPinStyle.Render("(pinned · f to follow)")
	}
	name := "—"
	if m.path != "" {
		name = filepath.Base(m.path)
	}
	pos := ""
	if ms := m.maxScroll(); ms > 0 {
		pos = fmt.Sprintf(" %d%%", m.scroll*100/ms)
	}
	return mdHeaderStyle.Render("▸ "+name+pos) + " " + badge
}

func (m *MarkdownPane) View() string {
	if m.editing {
		return m.editView() // implemented in a later task
	}
	if m.path == "" {
		return mdHeaderStyle.Render("▸ —") + "\n" +
			mdFaintStyle.Render("no markdown file in this worktree yet — the agent's first saved .md will appear here")
	}
	m.ensureRendered()
	body := m.rendered
	lo := min(m.scroll, len(body))
	hi := min(lo+m.bodyHeight(), len(body))
	footer := mdFaintStyle.Render("j/k scroll · e edit · f follow")
	return m.headerLine() + "\n" + strings.Join(body[lo:hi], "\n") + "\n" + footer
}

func (m *MarkdownPane) editView() string { return "" }
