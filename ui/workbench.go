package ui

import (
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
)

// WorkbenchTab enumerates the right panel's content tabs.
type WorkbenchTab int

const (
	WbTabMarkdown WorkbenchTab = iota
	WbTabDiff
	WbTabFiles
	WbTabTerminal
)

var (
	wbBorderStyle    lipgloss.Style
	wbTabActiveStyle lipgloss.Style
	wbTabIdleStyle   lipgloss.Style
	wbFileStyle      lipgloss.Style
	wbFileMDStyle    lipgloss.Style
	wbFileCurStyle   lipgloss.Style
)

func init() { RegisterThemeHook(rebuildWorkbenchStyles) }

func rebuildWorkbenchStyles() {
	wbBorderStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Rule)
	wbTabActiveStyle = lipgloss.NewStyle().Foreground(Accent).Bold(true)
	wbTabIdleStyle = lipgloss.NewStyle().Foreground(Dim)
	wbFileStyle = lipgloss.NewStyle().Foreground(Dim)
	wbFileMDStyle = lipgloss.NewStyle().Foreground(Text)
	wbFileCurStyle = lipgloss.NewStyle().Background(SelectionBg).Foreground(SelectionFg)
}

// Workbench renders the right panel of workbench mode: a tab strip
// over one of markdown / diff / files / terminal. It owns its
// MarkdownPane and DiffPane; the TerminalPane is the session's shared
// pane (see SplitPane.Terminal for the sizing-order contract).
type Workbench struct {
	width, height int

	tab      WorkbenchTab
	Markdown *MarkdownPane
	diff     *DiffPane
	terminal *TerminalPane

	sessionTitle string
	root         string // worktree root for the files tab + follow scan

	files      []string // repo-relative, from files.List
	fileCursor int
	fileOffset int
}

func NewWorkbench(diff *DiffPane, terminal *TerminalPane) *Workbench {
	return &Workbench{
		Markdown: NewMarkdownPane(),
		diff:     diff,
		terminal: terminal,
	}
}

func (w *Workbench) Tab() WorkbenchTab { return w.tab }
func (w *Workbench) SetTab(t WorkbenchTab) {
	w.tab = t
	w.applySizes()
}
func (w *Workbench) Root() string    { return w.root }
func (w *Workbench) Diff() *DiffPane { return w.diff }

// SessionTitle reports the session the panel is currently targeting
// ("" before the first SetSession).
func (w *Workbench) SessionTitle() string { return w.sessionTitle }

// SetSession retargets the panel to a different session. Clears the
// per-session content (document, file list) and resumes follow mode.
// No-op when the title is unchanged.
func (w *Workbench) SetSession(title, root string) {
	if title == w.sessionTitle {
		w.root = root
		return
	}
	w.sessionTitle = title
	w.root = root
	w.Markdown.Clear()
	w.Markdown.SetFollowing(true)
	w.Markdown.CancelEdit()
	w.files = nil
	w.fileCursor, w.fileOffset = 0, 0
}

func (w *Workbench) SetSize(width, height int) {
	w.width, w.height = width, height
	w.applySizes()
}

// applySizes pushes inner dimensions to the children. The terminal
// pane is only sized when its tab is active — it is shared with the
// (hidden) focus split, and resizing it re-sizes the underlying
// emulator, so we touch it only when it is actually displayed.
func (w *Workbench) applySizes() {
	iw, ih := max(w.width-2, 0), max(w.height-3, 0)
	w.Markdown.SetSize(iw, ih)
	w.diff.SetSize(iw, ih)
	if w.tab == WbTabTerminal {
		w.terminal.SetSize(iw, ih)
	}
}

func (w *Workbench) tabsRow() string {
	names := []string{"1 markdown", "2 diff", "3 files", "4 terminal"}
	parts := make([]string, len(names))
	for i, n := range names {
		if WorkbenchTab(i) == w.tab {
			parts[i] = wbTabActiveStyle.Render("[" + n + "]")
		} else {
			parts[i] = wbTabIdleStyle.Render(" " + n + " ")
		}
	}
	return strings.Join(parts, " ")
}

// SetFiles installs the files-tab listing (repo-relative paths).
func (w *Workbench) SetFiles(root string, paths []string) {
	w.root = root
	w.files = paths
	if w.fileCursor >= len(paths) {
		w.fileCursor = max(len(paths)-1, 0)
	}
}

func (w *Workbench) FilesUp() {
	if w.fileCursor > 0 {
		w.fileCursor--
	}
}

func (w *Workbench) FilesDown() {
	if w.fileCursor < len(w.files)-1 {
		w.fileCursor++
	}
}

// FilesTop/FilesBottom jump the files cursor to the first/last entry.
func (w *Workbench) FilesTop() { w.fileCursor = 0 }

func (w *Workbench) FilesBottom() {
	w.fileCursor = max(len(w.files)-1, 0)
}

// FileUnderCursor resolves the cursor to an absolute path.
func (w *Workbench) FileUnderCursor() (string, bool) {
	if len(w.files) == 0 || w.fileCursor >= len(w.files) {
		return "", false
	}
	return filepath.Join(w.root, w.files[w.fileCursor]), true
}

// IsMarkdownPath reports whether path has a markdown extension.
func IsMarkdownPath(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".md" || ext == ".markdown"
}

func (w *Workbench) filesView() string {
	if len(w.files) == 0 {
		return wbFileStyle.Render("no files (list loads on entry — check the worktree)")
	}
	bodyH := max(w.height-3, 1)
	if w.fileCursor < w.fileOffset {
		w.fileOffset = w.fileCursor
	}
	if w.fileCursor >= w.fileOffset+bodyH {
		w.fileOffset = w.fileCursor - bodyH + 1
	}
	end := min(w.fileOffset+bodyH, len(w.files))
	rows := make([]string, 0, end-w.fileOffset)
	for i := w.fileOffset; i < end; i++ {
		p := w.files[i]
		st := wbFileStyle
		if IsMarkdownPath(p) {
			st = wbFileMDStyle
		}
		if i == w.fileCursor {
			st = wbFileCurStyle
		}
		rows = append(rows, st.Render(p))
	}
	return strings.Join(rows, "\n")
}

func (w *Workbench) body() string {
	switch w.tab {
	case WbTabDiff:
		return w.diff.String()
	case WbTabFiles:
		return w.filesView()
	case WbTabTerminal:
		return w.terminal.String()
	default:
		return w.Markdown.View()
	}
}

// String renders the panel: tab strip as the first content row inside
// the border, body clipped to the remaining inner box. lipgloss
// Width/Height are total (border-inclusive) and Height is a minimum —
// MaxHeight does the capping (repo gotcha). MaxHeight's own truncation
// is not trusted here either: the body is explicitly clipped to its
// line budget before rendering, since Height/MaxHeight interaction
// with border styles has misbehaved in this repo before.
func (w *Workbench) String() string {
	if w.width <= 2 || w.height <= 3 {
		return ""
	}
	iw, ih := w.width-2, w.height-3
	body := clipLines(w.body(), ih)
	body = lipgloss.NewStyle().
		Width(iw).MaxWidth(iw).
		Height(ih).MaxHeight(ih).
		Render(body)
	content := w.tabsRow() + "\n" + body
	out := wbBorderStyle.
		Width(w.width).MaxWidth(w.width).
		MaxHeight(w.height).
		Render(content)
	return clipLines(out, w.height)
}

// clipLines truncates s to at most n lines, dropping the rest. A
// belt-and-suspenders guard alongside lipgloss's own MaxHeight: this
// repo has previously hit cases where Height (a minimum, not a cap)
// bled past MaxHeight, so the panel's own line budget is enforced
// directly rather than trusting lipgloss alone.
func clipLines(s string, n int) string {
	if n <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "\n")
}
