# Session Workbench (focus view) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A third `viewMode` ("workbench") that deep-dives one session: the agent pane on the left, a tabbed content panel (markdown viewer/editor, diff, files, terminal) on the right.

**Architecture:** In workbench mode the *existing* `m.splitPane` renders the left half with its terminal pane force-hidden (agent gets the full column) — so inline attach, the hardware cursor, drag-select, and scroll routing keep working nearly unchanged. A new `ui.Workbench` component owns only the right panel: it has its own `MarkdownPane` (glamour-rendered viewer + bubbles-textarea editor) and `DiffPane`, and shares the session's `TerminalPane` via a new `SplitPane.Terminal()` accessor. Markdown auto-follows the most recently modified `.md` in the worktree, scanned from a `tea.Cmd` kicked off by the existing 3s health tick. All file I/O runs in Cmds; all model mutation happens in `Update` handlers.

**Tech Stack:** Go 1.25, Bubble Tea v2, lipgloss v2, `charm.land/glamour/v2` (new dep), `charm.land/bubbles/v2/textarea` (already in tree), testify.

**Spec:** `docs/superpowers/specs/2026-07-22-session-workbench-design.md`. Deliberate v1 deviations (updated in the spec by Task 11):
- No separate full-width session header — branch + diff stats already live in the agent pane's title border.
- Conflict dialog is binary (overwrite / cancel) — the existing `ConfirmationOverlay` is yes/no; "reload" = cancel, `esc`, follow-mode reload.
- Mouse wheel over the terminal tab no-ops; no hardware cursor while attached to the terminal tab (agent-pane cursor works).

**House rules that bind every task** (from CLAUDE.md — read the Gotchas section first):
- Never mutate model state from inside a `tea.Cmd` function body; Cmds return messages, `Update` applies them.
- Theme-derived `lipgloss.Style` package vars must be built inside `ui.RegisterThemeHook` callbacks, never in var initializers.
- lipgloss `.Width()`/`.Height()` are **total** (border-inclusive) and `.Height()` is a *minimum*, not a cap — use `MaxHeight`/truncation to cap.
- Run tests with `CGO_ENABLED=0 go test ./...` (CGO is off in this repo; plain `go test` may fail to link). Lint with `go vet ./...` — the locally installed golangci-lint is v2 and rejects the repo's v1 config.
- `gofmt -w .` before every commit (CI enforces).
- Commit messages: conventional commits (`feat(ui): …`, `test(app): …`), ending with:
  `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`

---

## File Structure

| File | Responsibility |
|---|---|
| `config/state.go` (modify) | `UIPrefs.WorkbenchRatios` — persisted per-session left/right split |
| `session/files/recent_md.go` (create) | `MostRecentMarkdown(root)` — follow-scan walk |
| `ui/markdown_style.go` (create) | Theme-role → glamour `ansi.StyleConfig`, theme hook, generation counter |
| `ui/markdown.go` (create) | `MarkdownPane`: rendered view, scroll, follow/pin, edit mode |
| `ui/workbench.go` (create) | `Workbench`: panel frame, tabs, files-tab state, child sizing |
| `ui/split_pane.go` (modify) | `Terminal()` accessor (share the TerminalPane with the workbench) |
| `app/app.go` (modify) | `viewWorkbench` enum value, View/sizing/cursor/mouse/tick integration |
| `app/state_default.go` (modify) | Workbench key branch + `workbenchKeyAllowed` whitelist |
| `app/workbench.go` (create) | App glue: enter/exit, key handler, scan/load/save Cmds + messages |
| `app/workbench_mode_test.go` (create) | Mode transitions, gating, data-flow handler tests |
| `CLAUDE.md`, `USAGE.md` (modify) | Keybindings, architecture notes, gotchas |

---

### Task 1: Add the glamour dependency

**Files:** Modify: `go.mod`, `go.sum`, `vendor/` (repo vendors dependencies)

- [ ] **Step 1: Fetch and vendor**

```bash
GOFLAGS=-mod=mod go get charm.land/glamour/v2@v2.0.1
go mod tidy
go mod vendor
```

- [ ] **Step 2: Verify the build and the API you'll use**

```bash
CGO_ENABLED=0 go build -o /dev/null .
go doc charm.land/glamour/v2.NewTermRenderer
go doc charm.land/glamour/v2/ansi.StyleConfig | head -30
```

Expected: build succeeds; `NewTermRenderer(options ...TermRendererOption)` exists with `WithStyles(ansi.StyleConfig)` and `WithWordWrap(int)`. (Verified against v2.0.1 during planning.)

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum vendor
git commit -m "build(deps): add charm.land/glamour/v2 for markdown rendering"
```

---

### Task 2: Persist workbench split ratios in UIPrefs

**Files:** Modify: `config/state.go:47-65` · Test: `config/state_test.go` (or a new `config/uiprefs_test.go` if no state_test exists)

- [ ] **Step 1: Write the failing test**

```go
func TestUIPrefsClone_WorkbenchRatiosIndependent(t *testing.T) {
	p := UIPrefs{WorkbenchRatios: map[string]float64{"sess": 0.6}}
	c := p.clone()
	c.WorkbenchRatios["sess"] = 0.9
	assert.Equal(t, 0.6, p.WorkbenchRatios["sess"], "clone must deep-copy WorkbenchRatios")
}
```

- [ ] **Step 2: Run it — expect FAIL** (`clone` doesn't copy the new field; it doesn't exist yet)

```bash
CGO_ENABLED=0 go test ./config -run TestUIPrefsClone_WorkbenchRatios -v
```

- [ ] **Step 3: Implement**

In the `UIPrefs` struct add:

```go
	// WorkbenchRatios maps session title → agent-pane width share in
	// workbench mode (sibling of SplitRatios, which is the focus-mode
	// vertical split).
	WorkbenchRatios map[string]float64 `json:"workbench_ratios,omitempty"`
```

In `clone()`, after the `SplitRatios` copy block:

```go
	if p.WorkbenchRatios != nil {
		out.WorkbenchRatios = make(map[string]float64, len(p.WorkbenchRatios))
		for k, v := range p.WorkbenchRatios {
			out.WorkbenchRatios[k] = v
		}
	}
```

- [ ] **Step 4: Run — expect PASS**, then commit

```bash
CGO_ENABLED=0 go test ./config -v
gofmt -w config && git add config && git commit -m "feat(config): persist per-session workbench split ratios"
```

---

### Task 3: `files.MostRecentMarkdown`

**Files:** Create: `session/files/recent_md.go` · Test: `session/files/recent_md_test.go`

- [ ] **Step 1: Write the failing tests**

```go
package files

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func touch(t *testing.T, path string, mtime time.Time) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("# x\n"), 0o644))
	require.NoError(t, os.Chtimes(path, mtime, mtime))
}

func TestMostRecentMarkdown_PicksNewest(t *testing.T) {
	root := t.TempDir()
	base := time.Now().Add(-time.Hour)
	touch(t, filepath.Join(root, "old.md"), base)
	touch(t, filepath.Join(root, "docs", "new.md"), base.Add(30*time.Minute))
	touch(t, filepath.Join(root, "not-md.txt"), base.Add(50*time.Minute))

	path, mtime, ok, err := MostRecentMarkdown(root)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, filepath.Join(root, "docs", "new.md"), path)
	assert.WithinDuration(t, base.Add(30*time.Minute), mtime, time.Second)
}

func TestMostRecentMarkdown_SkipsPrunedDirs(t *testing.T) {
	root := t.TempDir()
	base := time.Now().Add(-time.Hour)
	touch(t, filepath.Join(root, "real.md"), base)
	touch(t, filepath.Join(root, ".git", "hidden.md"), base.Add(time.Minute))
	touch(t, filepath.Join(root, "node_modules", "pkg", "README.md"), base.Add(2*time.Minute))
	touch(t, filepath.Join(root, "vendor", "dep.md"), base.Add(3*time.Minute))

	path, _, ok, err := MostRecentMarkdown(root)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, filepath.Join(root, "real.md"), path)
}

func TestMostRecentMarkdown_EmptyTree(t *testing.T) {
	_, _, ok, err := MostRecentMarkdown(t.TempDir())
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestMostRecentMarkdown_EmptyRootRejected(t *testing.T) {
	_, _, _, err := MostRecentMarkdown("")
	assert.Error(t, err)
}
```

- [ ] **Step 2: Run — expect FAIL** (`MostRecentMarkdown` undefined)

```bash
CGO_ENABLED=0 go test ./session/files -run TestMostRecentMarkdown -v
```

- [ ] **Step 3: Implement `session/files/recent_md.go`**

```go
package files

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"time"
)

// mdSkipDirs are directory names pruned from the markdown follow scan.
// Broader than listViaWalk's pruning because this walk runs every
// health tick: dependency and build trees are both noisy (their .md
// files are never the user's working documents) and large.
var mdSkipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	".loom":        true,
	"dist":         true,
	"target":       true,
}

// MostRecentMarkdown walks root and returns the most recently modified
// markdown file (.md/.markdown, case-insensitive). ok=false when the
// tree holds none. Unreadable entries and symlinks are skipped rather
// than failing the scan — a permission error on one subtree must not
// blank the follow view. Callers run this inside a tea.Cmd, so a slow
// filesystem stalls one background goroutine, not the UI loop.
func MostRecentMarkdown(root string) (path string, mtime time.Time, ok bool, err error) {
	if root == "" {
		return "", time.Time{}, false, errors.New("files.MostRecentMarkdown: root is empty")
	}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && mdSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if ext != ".md" && ext != ".markdown" {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		if !ok || info.ModTime().After(mtime) {
			path, mtime, ok = p, info.ModTime(), true
		}
		return nil
	})
	if err != nil {
		return "", time.Time{}, false, err
	}
	return path, mtime, ok, nil
}
```

- [ ] **Step 4: Run — expect PASS**, then commit

```bash
CGO_ENABLED=0 go test ./session/files -v
gofmt -w session/files && git add session/files && git commit -m "feat(files): MostRecentMarkdown follow-scan helper"
```

---

### Task 4: Glamour style from theme roles

**Files:** Create: `ui/markdown_style.go` · Test: `ui/markdown_style_test.go`

- [ ] **Step 1: Write the failing tests**

```go
package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMarkdownStyle_RebuildsOnApplyTheme(t *testing.T) {
	ApplyTheme("afterglow")
	genBefore := mdStyleGen
	afterglowText := *mdStyle.Document.StylePrimitive.Color

	ApplyTheme("legacy")
	t.Cleanup(func() { ApplyTheme("afterglow") })

	assert.Greater(t, mdStyleGen, genBefore, "ApplyTheme must bump the style generation")
	require.NotNil(t, mdStyle.Document.StylePrimitive.Color)
	assert.NotEqual(t, afterglowText, *mdStyle.Document.StylePrimitive.Color,
		"document text color must track the theme")
}

func TestMarkdownStyle_HeadingUsesAccent(t *testing.T) {
	ApplyTheme("afterglow")
	require.NotNil(t, mdStyle.Heading.StylePrimitive.Color)
	assert.Equal(t, *hexPtr(Accent), *mdStyle.Heading.StylePrimitive.Color)
}
```

- [ ] **Step 2: Run — expect FAIL** (symbols undefined)

```bash
CGO_ENABLED=0 go test ./ui -run TestMarkdownStyle -v
```

- [ ] **Step 3: Implement `ui/markdown_style.go`**

```go
package ui

import (
	"fmt"
	"image/color"

	"charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
)

// mdStyle is the glamour style config derived from the active theme
// roles. mdStyleGen increments on every rebuild so cached renders
// (MarkdownPane) can detect staleness without re-rendering per frame.
var (
	mdStyle    ansi.StyleConfig
	mdStyleGen int
)

func init() { RegisterThemeHook(rebuildMarkdownStyle) }

func rebuildMarkdownStyle() {
	mdStyle = buildMarkdownStyle()
	mdStyleGen++
}

// hexPtr renders any color.Color as the "#rrggbb" string glamour's
// style primitives expect. AdaptiveColor resolves via RGBA() against
// the detected background, which matches how lipgloss paints it.
func hexPtr(c color.Color) *string {
	r, g, b, _ := c.RGBA()
	s := fmt.Sprintf("#%02x%02x%02x", uint8(r>>8), uint8(g>>8), uint8(b>>8))
	return &s
}

func mdBoolPtr(b bool) *bool { return &b }
func mdUintPtr(u uint) *uint { return &u }

// buildMarkdownStyle starts from glamour's dark config (keeping its
// chroma code-block palette — syntax highlighting has its own color
// system) and overrides the prose roles with the active theme.
func buildMarkdownStyle() ansi.StyleConfig {
	s := styles.DarkStyleConfig
	s.Document.StylePrimitive.Color = hexPtr(Text)
	s.Document.Margin = mdUintPtr(0)
	s.Heading.StylePrimitive.Color = hexPtr(Accent)
	s.Heading.StylePrimitive.Bold = mdBoolPtr(true)
	s.H1.StylePrimitive.Color = hexPtr(SelectionFg)
	s.H1.StylePrimitive.BackgroundColor = hexPtr(Accent)
	s.BlockQuote.StylePrimitive.Color = hexPtr(Dim)
	s.Item.Color = hexPtr(Text)
	s.Enumeration.Color = hexPtr(Dim)
	s.Link.Color = hexPtr(Info)
	s.LinkText.Color = hexPtr(Info)
	s.Code.StylePrimitive.Color = hexPtr(Highlight)
	s.HorizontalRule.Color = hexPtr(Rule)
	return s
}
```

Note: if a field name doesn't compile (e.g. `s.Code` shape), check with `go doc charm.land/glamour/v2/ansi.StyleConfig` and adjust the *field path*, not the intent (inline code = Highlight, etc.).

- [ ] **Step 4: Run — expect PASS**, then commit

```bash
CGO_ENABLED=0 go test ./ui -run TestMarkdownStyle -v
gofmt -w ui && git add ui && git commit -m "feat(ui): theme-derived glamour style for markdown rendering"
```

---

### Task 5: `MarkdownPane` — viewer core

**Files:** Create: `ui/markdown.go` · Test: `ui/markdown_test.go`

- [ ] **Step 1: Write the failing tests**

```go
package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestMDPane() *MarkdownPane {
	p := NewMarkdownPane()
	p.SetSize(60, 20)
	return p
}

func TestMarkdownPane_StartsFollowingAndEmpty(t *testing.T) {
	p := newTestMDPane()
	assert.True(t, p.Following())
	assert.Contains(t, p.View(), "no markdown", "empty state must say so")
}

func TestMarkdownPane_RendersHeadingAndScrolls(t *testing.T) {
	p := newTestMDPane()
	var b strings.Builder
	b.WriteString("# Title\n\n")
	for i := 0; i < 100; i++ {
		b.WriteString("line\n\n")
	}
	p.SetDocument("/tmp/x/plan.md", b.String(), time.Now())

	top := p.View()
	assert.Contains(t, top, "Title")
	assert.Contains(t, top, "plan.md", "header shows the basename")

	p.ScrollBottom()
	assert.NotEqual(t, top, p.View(), "scrolling must change the window")
	p.ScrollTop()
	assert.Equal(t, top, p.View())
}

func TestMarkdownPane_PathChangeResetsScroll_SamePathPreservesIt(t *testing.T) {
	p := newTestMDPane()
	long := strings.Repeat("para\n\n", 200)
	p.SetDocument("/tmp/a.md", long, time.Now())
	p.ScrollBottom()
	require.NotEqual(t, 0, p.Scroll())

	p.SetDocument("/tmp/a.md", long+"tail\n", time.Now())
	assert.NotEqual(t, 0, p.Scroll(), "same-path reload keeps scroll")

	p.SetDocument("/tmp/b.md", long, time.Now())
	assert.Equal(t, 0, p.Scroll(), "new path resets scroll")
}

func TestMarkdownPane_TruncatesHugeDocs(t *testing.T) {
	p := newTestMDPane()
	p.SetDocument("/tmp/big.md", strings.Repeat("a", MarkdownMaxBytes+100), time.Now())
	p.ScrollBottom()
	assert.Contains(t, p.View(), "truncated")
}

func TestMarkdownPane_ReRendersOnThemeChange(t *testing.T) {
	ApplyTheme("afterglow")
	p := newTestMDPane()
	p.SetDocument("/tmp/x.md", "# Hi\n", time.Now())
	before := p.View()
	ApplyTheme("legacy")
	t.Cleanup(func() { ApplyTheme("afterglow") })
	assert.NotEqual(t, before, p.View(), "stale generation must trigger re-render")
}
```

- [ ] **Step 2: Run — expect FAIL**

```bash
CGO_ENABLED=0 go test ./ui -run TestMarkdownPane -v
```

- [ ] **Step 3: Implement `ui/markdown.go`** (viewer only; edit mode is Task 6)

```go
package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
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
		return m.editView() // Task 6
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
```

(`max`/`min` are Go 1.21 builtins.) Add a temporary `editView` stub so it compiles — Task 6 replaces it:

```go
func (m *MarkdownPane) editView() string { return "" }
```

- [ ] **Step 4: Run — expect PASS**, then commit

```bash
CGO_ENABLED=0 go test ./ui -run TestMarkdownPane -v
gofmt -w ui && git add ui && git commit -m "feat(ui): MarkdownPane glamour viewer with follow/pin and scroll"
```

---

### Task 6: `MarkdownPane` — edit mode

**Files:** Modify: `ui/markdown.go` · Test: `ui/markdown_test.go`

- [ ] **Step 1: Write the failing tests**

```go
func TestMarkdownPane_EditCycle(t *testing.T) {
	p := newTestMDPane()
	p.SetDocument("/tmp/a.md", "# One\n", time.Now())

	require.True(t, p.StartEdit())
	assert.True(t, p.Editing())
	assert.False(t, p.EditDirty())
	assert.Equal(t, "# One\n", p.EditValue())

	p.HandleEditKey(keyPress('!'))
	assert.True(t, p.EditDirty())

	p.CancelEdit()
	assert.False(t, p.Editing())
	assert.Contains(t, p.View(), "One", "view mode shows the untouched document")
}

func TestMarkdownPane_StartEditRequiresDocument(t *testing.T) {
	p := newTestMDPane()
	assert.False(t, p.StartEdit(), "no document → no edit mode")
}

func TestMarkdownPane_ApplySavedExitsEditAndRerenders(t *testing.T) {
	p := newTestMDPane()
	p.SetDocument("/tmp/a.md", "# One\n", time.Now())
	require.True(t, p.StartEdit())
	newMtime := time.Now().Add(time.Minute)
	p.ApplySaved("# Two\n", newMtime)
	assert.False(t, p.Editing())
	assert.Equal(t, newMtime, p.Mtime())
	assert.Contains(t, p.View(), "Two")
}

func TestMarkdownPane_EditingSuspendsFollowSwap(t *testing.T) {
	p := newTestMDPane()
	p.SetDocument("/tmp/a.md", "# One\n", time.Now())
	require.True(t, p.StartEdit())
	assert.True(t, p.Editing()) // app-side scan handler checks this and skips
}
```

Add the key helper at the top of `ui/markdown_test.go` (Bubble Tea v2 key press construction — mirror how existing ui tests build `tea.KeyPressMsg`; check `ui/overlay/settingsOverlay_test.go` for the in-repo idiom):

```go
func keyPress(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}
```

- [ ] **Step 2: Run — expect FAIL**

```bash
CGO_ENABLED=0 go test ./ui -run TestMarkdownPane_Edit -v
```

- [ ] **Step 3: Implement** — replace the `editView` stub and add:

```go
// StartEdit flips to the raw-text editor seeded with the loaded
// content. Returns false when there is nothing to edit.
func (m *MarkdownPane) StartEdit() bool {
	if m.path == "" {
		return false
	}
	m.ta.SetValue(m.raw)
	m.ta.Focus()
	m.editing = true
	return true
}

func (m *MarkdownPane) EditValue() string { return m.ta.Value() }
func (m *MarkdownPane) EditDirty() bool   { return m.editing && m.ta.Value() != m.raw }

func (m *MarkdownPane) CancelEdit() {
	m.ta.Blur()
	m.editing = false
}

// ApplySaved is called after a successful disk write: adopt the saved
// content as the new baseline and drop back to the rendered view.
func (m *MarkdownPane) ApplySaved(raw string, mtime time.Time) {
	m.raw, m.mtime = raw, mtime
	m.rendered = nil
	m.CancelEdit()
}

// HandleEditKey forwards a key to the textarea.
func (m *MarkdownPane) HandleEditKey(msg tea.KeyPressMsg) {
	m.ta, _ = m.ta.Update(msg)
}

func (m *MarkdownPane) editView() string {
	name := filepath.Base(m.path)
	dirty := ""
	if m.EditDirty() {
		dirty = mdPinStyle.Render(" [+]")
	}
	header := mdHeaderStyle.Render("✎ "+name) + dirty
	footer := mdFaintStyle.Render("ctrl+s save · esc cancel")
	return header + "\n" + m.ta.View() + "\n" + footer
}
```

- [ ] **Step 4: Run — expect PASS**, then commit

```bash
CGO_ENABLED=0 go test ./ui -run TestMarkdownPane -v
gofmt -w ui && git add ui && git commit -m "feat(ui): MarkdownPane raw-text edit mode"
```

---

### Task 7: `ui.Workbench` panel + `SplitPane.Terminal()` accessor

**Files:** Create: `ui/workbench.go` · Modify: `ui/split_pane.go` (after `NewSplitPane`, ~line 90) · Test: `ui/workbench_test.go`

- [ ] **Step 1: Write the failing tests**

```go
package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestWorkbench() *Workbench {
	w := NewWorkbench(NewDiffPane(), NewTerminalPane())
	w.SetSize(50, 20)
	return w
}

func TestWorkbench_DefaultsToMarkdownTab(t *testing.T) {
	w := newTestWorkbench()
	assert.Equal(t, WbTabMarkdown, w.Tab())
	out := w.String()
	assert.Contains(t, out, "markdown")
	assert.Contains(t, out, "diff")
	assert.Contains(t, out, "files")
	assert.Contains(t, out, "terminal")
}

func TestWorkbench_TabSwitchAndRender(t *testing.T) {
	w := newTestWorkbench()
	w.SetTab(WbTabFiles)
	assert.Equal(t, WbTabFiles, w.Tab())
	w.SetTab(WbTabMarkdown)
	w.Markdown.SetDocument("/tmp/p.md", "# Hello\n", time.Now())
	assert.Contains(t, w.String(), "Hello")
}

func TestWorkbench_StringNeverExceedsHeight(t *testing.T) {
	w := newTestWorkbench()
	long := strings.Repeat("x\n\n", 300)
	w.Markdown.SetDocument("/tmp/p.md", long, time.Now())
	lines := strings.Split(w.String(), "\n")
	assert.LessOrEqual(t, len(lines), 20, "panel must cap at its height")
}

func TestWorkbench_FilesCursorNavAndSelect(t *testing.T) {
	w := newTestWorkbench()
	w.SetFiles("/repo", []string{"README.md", "main.go", "docs/plan.md"})
	w.SetTab(WbTabFiles)

	// cursor 0 = README.md
	path, ok := w.FileUnderCursor()
	require.True(t, ok)
	assert.Equal(t, "/repo/README.md", path)
	assert.True(t, IsMarkdownPath(path))

	w.FilesDown() // main.go — resolvable, but not markdown
	path, ok = w.FileUnderCursor()
	require.True(t, ok)
	assert.False(t, IsMarkdownPath(path))

	w.FilesDown() // docs/plan.md
	path, _ = w.FileUnderCursor()
	assert.Equal(t, "/repo/docs/plan.md", path)
	w.FilesDown() // clamp at end
	path, _ = w.FileUnderCursor()
	assert.Equal(t, "/repo/docs/plan.md", path)
}

func TestWorkbench_SetInstanceTitleResetsMarkdownAndFiles(t *testing.T) {
	w := newTestWorkbench()
	w.Markdown.SetDocument("/tmp/p.md", "# A\n", time.Now())
	w.SetFiles("/repo", []string{"a.md"})
	w.SetSession("other-session", "/repo2")
	assert.Equal(t, "", w.Markdown.Path(), "session switch clears the document")
	assert.True(t, w.Markdown.Following())
	_, ok := w.FileUnderCursor()
	assert.False(t, ok, "session switch clears the file list")
	assert.Equal(t, "/repo2", w.Root())
}
```

- [ ] **Step 2: Run — expect FAIL**

```bash
CGO_ENABLED=0 go test ./ui -run TestWorkbench -v
```

- [ ] **Step 3: Add the `SplitPane` accessor** (`ui/split_pane.go`, after `NewSplitPane`)

```go
// Terminal exposes the terminal child pane so workbench mode can
// render/size the same TerminalPane instance in its right panel.
// Ordering contract: in workbench mode the app calls SplitPane.SetSize
// (which, with the terminal hidden, sizes it to zero) BEFORE
// Workbench.SetSize re-sizes it for the panel — keep that order.
func (s *SplitPane) Terminal() *TerminalPane { return s.terminal }
```

- [ ] **Step 4: Implement `ui/workbench.go`**

```go
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
func (w *Workbench) Root() string { return w.root }
func (w *Workbench) Diff() *DiffPane { return w.diff }

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
	iw, ih := max(w.width-2, 0), max(w.height-2, 0)
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
	bodyH := max(w.height-2, 1)
	// keep cursor in window
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

// String renders the panel: tab strip as the border title row, body
// clipped to the inner box. lipgloss Width/Height are total
// (border-inclusive) and Height is a minimum — MaxHeight does the
// capping (see the lipgloss border-sizing gotcha).
func (w *Workbench) String() string {
	if w.width <= 2 || w.height <= 2 {
		return ""
	}
	iw, ih := w.width-2, w.height-2
	body := lipgloss.NewStyle().
		Width(iw).MaxWidth(iw).
		Height(ih).MaxHeight(ih).
		Render(w.body())
	content := w.tabsRow() + "\n" + body
	return wbBorderStyle.
		Width(w.width).MaxWidth(w.width).
		MaxHeight(w.height).
		Render(content)
}
```

Layout note: the tab strip renders as the first *content* row inside the border (total = 1 border + 1 tabs + body + 1 border = `height`); therefore `body` gets `height-3`. If `TestWorkbench_StringNeverExceedsHeight` fails on an off-by-one, fix the inner heights (`applySizes` and `String` must agree: inner body height = `w.height - 3`, MarkdownPane already spends 2 of those on its own header/footer). Adjust both places together.

- [ ] **Step 5: Run — expect PASS** (iterate on the off-by-ones; the height-cap test is the guard), then commit

```bash
CGO_ENABLED=0 go test ./ui -v
gofmt -w ui && git add ui && git commit -m "feat(ui): Workbench right panel (tabs: markdown/diff/files/terminal)"
```

---

### Task 8: App wiring — mode, keys, view, sizing

**Files:** Modify: `app/app.go` (enum ~line 159, home fields ~line 250, constructors lines 426 & 2449, `updateHandleWindowSizeEvent` line 687, `View` line 3095, `attachCursor` line 3232, hint line ~3144), `app/state_default.go` · Create: `app/workbench.go`, `app/workbench_mode_test.go`

- [ ] **Step 1: Write the failing tests** (`app/workbench_mode_test.go`; copy the test-home construction pattern from `app/overview_mode_test.go` / `app/app_test.go:153` — bare homes built from struct literals with `ui.NewSplitPane(...)` etc.)

```go
package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aidan-bailey/loom/ui"
)

// newWorkbenchTestHome mirrors newOverviewTestHome (overview_mode_test.go)
// but attaches a workbench. Reuse the same instance/list scaffolding —
// one selected, started instance in m.list.
func newWorkbenchTestHome(t *testing.T) *home {
	m := newOverviewTestHome(t) // reuse: bare home + one selectable instance
	m.workbench = ui.NewWorkbench(ui.NewDiffPane(), m.splitPane.Terminal())
	return m
}

func key(s string) tea.KeyPressMsg // reuse the existing helper from overview_mode_test.go — do NOT redefine; delete this line if it already exists in the package.

func TestWorkbench_EnterFromFocus(t *testing.T) {
	m := newWorkbenchTestHome(t)
	require.Equal(t, viewFocus, m.viewMode)
	model, _ := handleStateDefaultKey(m, key("enter"))
	m = model.(*home)
	assert.Equal(t, viewWorkbench, m.viewMode)
	assert.True(t, m.splitPane.IsTerminalHidden(), "workbench forces the split terminal hidden")
}

func TestWorkbench_EscReturnsToFocus_RestoresTerminal(t *testing.T) {
	m := newWorkbenchTestHome(t)
	m.splitPane.SetTerminalHidden(false)
	handleStateDefaultKey(m, key("enter"))
	require.Equal(t, viewWorkbench, m.viewMode)
	model, _ := handleStateDefaultKey(m, key("esc"))
	m = model.(*home)
	assert.Equal(t, viewFocus, m.viewMode)
	assert.False(t, m.splitPane.IsTerminalHidden(), "terminal-hidden pref restored on exit")
}

func TestWorkbench_TabGoesToOverview(t *testing.T) {
	m := newWorkbenchTestHome(t)
	handleStateDefaultKey(m, key("enter"))
	model, _ := handleStateDefaultKey(m, key("tab"))
	m = model.(*home)
	assert.Equal(t, viewOverview, m.viewMode)
}

func TestWorkbench_NumberKeysSwitchTabs(t *testing.T) {
	m := newWorkbenchTestHome(t)
	handleStateDefaultKey(m, key("enter"))
	handleStateDefaultKey(m, key("2"))
	assert.Equal(t, ui.WbTabDiff, m.workbench.Tab())
	handleStateDefaultKey(m, key("4"))
	assert.Equal(t, ui.WbTabTerminal, m.workbench.Tab())
	handleStateDefaultKey(m, key("d"))
	assert.Equal(t, ui.WbTabDiff, m.workbench.Tab(), "d maps to the diff tab in workbench")
}

func TestWorkbench_NonWhitelistedKeysNoOp(t *testing.T) {
	m := newWorkbenchTestHome(t)
	handleStateDefaultKey(m, key("enter"))
	for _, k := range []string{"\\", "T", "K", "J"} {
		model, cmd := handleStateDefaultKey(m, key(k))
		m = model.(*home)
		assert.Equal(t, viewWorkbench, m.viewMode, "key %q must not leave workbench", k)
		assert.Nil(t, cmd, "key %q must no-op", k)
	}
}

func TestWorkbench_EnterWithNoInstanceNoOps(t *testing.T) {
	m := newWorkbenchTestHome(t)
	m.list.Kill() // or construct an empty-list home, matching the scaffolding available
	model, _ := handleStateDefaultKey(m, key("enter"))
	m = model.(*home)
	assert.Equal(t, viewFocus, m.viewMode)
}
```

Adapt the two scaffolding references (`newOverviewTestHome`, `key`) to whatever `app/overview_mode_test.go` actually names them — reuse, don't duplicate. If no empty-list helper exists for the last test, build the home without adding an instance.

- [ ] **Step 2: Run — expect FAIL** (`viewWorkbench`, `m.workbench` undefined)

```bash
CGO_ENABLED=0 go test ./app -run TestWorkbench_ -v
```

- [ ] **Step 3: Add the enum value + home fields** (`app/app.go`)

At the `viewMode` enum (~line 165):

```go
const (
	viewFocus viewMode = iota
	viewOverview
	// viewWorkbench is the single-session deep-dive: agent split on
	// the left (terminal force-hidden), tabbed content panel on the
	// right. Never persisted — quit/restart lands in focus.
	viewWorkbench
)
```

In the `home` struct (next to `overview *ui.Overview`):

```go
	// workbench renders the right content panel when viewMode is
	// viewWorkbench; the left half is m.splitPane with its terminal
	// hidden (wbPrevTerminalHidden restores the user's setting on exit).
	workbench            *ui.Workbench
	wbPrevTerminalHidden bool
	// wbLeftWidth is the workbench's agent-column width in screen
	// cells, cached for mouse-wheel routing (like listWidth).
	wbLeftWidth int
	// wbRatio is the in-memory agent share for the current session
	// (0 = default). Flushed to UIPrefs.WorkbenchRatios on exit/quit.
	wbRatio float64
```

At both home constructors (`app.go:426` and `app.go:2449`), after `splitPane` is constructed add (adapting to each site's shape — struct literal vs. local):

```go
	workbench: ui.NewWorkbench(ui.NewDiffPane(), <splitPane>.Terminal()),
```

For the struct-literal site, hoist the split pane into a local first:

```go
	sp := ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane())
	// … in the literal:
	splitPane: sp,
	workbench: ui.NewWorkbench(ui.NewDiffPane(), sp.Terminal()),
```

- [ ] **Step 4: Create `app/workbench.go`** — enter/exit + key handler

```go
package app

import (
	tea "charm.land/bubbletea/v2"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/ui"
)

// workbenchKeyAllowed whitelists script-dispatched keys in workbench
// mode (same shape and caveats as overviewKeyAllowed — the gate keys
// on raw key strings, not actions). Session ops (D/r/R/p/s/m), attach
// (i/ctrl+a/ctrl+t/alt+a/alt+t), quick input (a/t), waiting-jump
// (]/[), workspace nav, and app chrome pass through; layout keys that
// address the hidden focus chrome (\, T, K/J list paging) do not.
var workbenchKeyAllowed = map[string]bool{
	"q": true, "?": true, "W": true, "S": true,
	"{": true, "}": true, "l": true, ";": true,
	"]": true, "[": true,
	"D": true, "r": true, "R": true, "p": true, "s": true, "m": true,
	"i": true, "ctrl+a": true, "ctrl+t": true,
	"alt+a": true, "alt+t": true,
	"a": true, "t": true,
}

// enterWorkbench flips to workbench mode for the selected instance.
// Returns nil (no-op) when nothing is selected.
func (m *home) enterWorkbench() tea.Cmd {
	sel := m.list.GetSelectedInstance()
	if sel == nil {
		return nil
	}
	m.viewMode = viewWorkbench
	m.wbPrevTerminalHidden = m.splitPane.IsTerminalHidden()
	m.splitPane.SetTerminalHidden(true)
	m.workbench.SetSession(sel.Title, sel.GetWorktreePath())
	m.wbRatio = 0
	if m.appState != nil {
		if r, ok := m.appState.GetUIPrefs().WorkbenchRatios[sel.Title]; ok {
			m.wbRatio = r
		}
	}
	return tea.Batch(tea.RequestWindowSize, m.instanceChanged(), m.workbenchRefresh())
}

// exitWorkbench returns to `to` (viewFocus or viewOverview),
// restoring the split terminal and flushing the ratio.
func (m *home) exitWorkbench(to viewMode) tea.Cmd {
	m.flushWorkbenchRatio()
	m.workbench.Markdown.CancelEdit()
	m.splitPane.SetTerminalHidden(m.wbPrevTerminalHidden)
	m.viewMode = to
	if to == viewOverview {
		m.enterOverview()
	}
	return tea.Batch(tea.RequestWindowSize, m.instanceChanged())
}

// workbenchRatio is the effective agent share (default 0.5).
func (m *home) workbenchRatio() float64 {
	if m.wbRatio == 0 {
		return 0.5
	}
	return m.wbRatio
}

// flushWorkbenchRatio persists a non-default ratio for the current
// session title. Called on exit and from handleQuit.
func (m *home) flushWorkbenchRatio() {
	sel := m.list.GetSelectedInstance()
	if sel == nil || m.wbRatio == 0 {
		return
	}
	r := m.wbRatio
	m.mutateUIPrefs(func(p *config.UIPrefs) {
		if p.WorkbenchRatios == nil {
			p.WorkbenchRatios = map[string]float64{}
		}
		p.WorkbenchRatios[sel.Title] = r
	})
}

// handleWorkbenchKey processes workbench-local keys. Returns
// handled=false for keys that should fall through to the whitelist
// gate + script dispatch.
func handleWorkbenchKey(m *home, msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	md := m.workbench.Markdown

	// Edit mode captures everything except save/cancel.
	if md.Editing() {
		switch msg.String() {
		case "ctrl+s":
			return m, m.saveWorkbenchMarkdown(false), true
		case "esc":
			if md.EditDirty() {
				return m, m.confirmDiscardEdit(), true
			}
			md.CancelEdit()
			return m, nil, true
		default:
			md.HandleEditKey(msg)
			return m, nil, true
		}
	}

	switch msg.String() {
	case "esc":
		return m, m.exitWorkbench(viewFocus), true
	case "tab":
		return m, m.exitWorkbench(viewOverview), true
	case "1":
		m.workbench.SetTab(ui.WbTabMarkdown)
		return m, nil, true
	case "2", "d":
		m.workbench.SetTab(ui.WbTabDiff)
		return m, nil, true
	case "3":
		m.workbench.SetTab(ui.WbTabFiles)
		return m, m.workbenchFilesCmd(), true
	case "4":
		m.workbench.SetTab(ui.WbTabTerminal)
		m.workbench.SetSize(m.lastWidth-m.wbLeftWidth, m.lastHeight-m.tabBar.Height()-2)
		return m, nil, true
	case "e":
		if m.workbench.Tab() == ui.WbTabMarkdown {
			md.StartEdit()
		}
		return m, nil, true
	case "f":
		if !md.Following() {
			md.SetFollowing(true)
			return m, m.workbenchScanCmd(), true
		}
		return m, nil, true
	case "enter":
		if m.workbench.Tab() == ui.WbTabFiles {
			if path, ok := m.workbench.FileUnderCursor(); ok && ui.IsMarkdownPath(path) {
				md.SetFollowing(false)
				m.workbench.SetTab(ui.WbTabMarkdown)
				sel := m.list.GetSelectedInstance()
				if sel != nil {
					return m, loadMarkdownCmd(sel.Title, path, false), true
				}
			}
		}
		return m, nil, true
	case "j", "down":
		m.workbenchScrollDown()
		return m, nil, true
	case "k", "up":
		m.workbenchScrollUp()
		return m, nil, true
	case "g":
		md.ScrollTop()
		return m, nil, true
	case "G":
		md.ScrollBottom()
		return m, nil, true
	case "pgup":
		md.PageUp()
		return m, nil, true
	case "pgdown":
		md.PageDown()
		return m, nil, true
	case "ctrl+left", "ctrl+right":
		delta := 0.05
		if msg.String() == "ctrl+left" {
			delta = -0.05
		}
		r := m.workbenchRatio() + delta
		if r < 0.2 {
			r = 0.2
		}
		if r > 0.8 {
			r = 0.8
		}
		m.wbRatio = r
		return m, tea.RequestWindowSize, true
	case "n", "N":
		// Same rationale as overview: the create flow is a
		// focus-layout affordance — drop to focus, then dispatch.
		cmds := []tea.Cmd{m.exitWorkbench(viewFocus)}
		if cmd, handled := m.dispatchScript(msg.String()); handled {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...), true
	}
	return m, nil, false
}

// workbenchScrollUp/Down route j/k to the active tab.
func (m *home) workbenchScrollUp() {
	switch m.workbench.Tab() {
	case ui.WbTabDiff:
		m.workbench.Diff().ScrollUp()
	case ui.WbTabFiles:
		m.workbench.FilesUp()
	case ui.WbTabMarkdown:
		m.workbench.Markdown.ScrollUp()
	}
}

func (m *home) workbenchScrollDown() {
	switch m.workbench.Tab() {
	case ui.WbTabDiff:
		m.workbench.Diff().ScrollDown()
	case ui.WbTabFiles:
		m.workbench.FilesDown()
	case ui.WbTabMarkdown:
		m.workbench.Markdown.ScrollDown()
	}
}
```

`m.saveWorkbenchMarkdown`, `m.confirmDiscardEdit`, `m.workbenchRefresh`, `m.workbenchScanCmd`, `m.workbenchFilesCmd`, `loadMarkdownCmd` arrive in Tasks 9–10; for this task add compiling stubs in `app/workbench.go`:

```go
func (m *home) workbenchRefresh() tea.Cmd              { return nil }
func (m *home) workbenchScanCmd() tea.Cmd              { return nil }
func (m *home) workbenchFilesCmd() tea.Cmd             { return nil }
func (m *home) saveWorkbenchMarkdown(force bool) tea.Cmd { return nil }
func (m *home) confirmDiscardEdit() tea.Cmd            { return nil }
func loadMarkdownCmd(title, path string, follow bool) tea.Cmd { return nil }
```

- [ ] **Step 5: Route keys** (`app/state_default.go`) — insert *before* the `viewOverview` branch:

```go
	if m.viewMode == viewWorkbench {
		if model, cmd, handled := handleWorkbenchKey(m, msg); handled {
			return model, cmd
		}
		if !workbenchKeyAllowed[msg.String()] {
			return m, nil
		}
	}
```

And in the focus-mode path, intercept `enter` — add to the bottom of `handleStateDefaultKey`, just before the `dispatchScript` fallthrough (focus mode only: overview returned earlier from its own `enter` case):

```go
	if msg.String() == "enter" && m.viewMode == viewFocus {
		if cmd := m.enterWorkbench(); cmd != nil {
			return m, cmd
		}
		return m, nil
	}
```

- [ ] **Step 6: View, sizing, cursor** (`app/app.go`)

In `View()` (line 3111), extend the mode branch:

```go
	if m.viewMode == viewOverview && m.state != stateFileExplorer {
		mainContent = m.overview.Render(m.overviewData())
	} else if m.viewMode == viewWorkbench && m.state != stateFileExplorer {
		mainContent = lipgloss.JoinHorizontal(lipgloss.Top, m.splitPane.String(), m.workbench.String())
	} else {
		// … existing focus path unchanged
```

Hint line (line 3144 area):

```go
	hint := "tab overview · ] next waiting · \\ rail · ? help · q quit"
	if m.viewMode == viewOverview {
		hint = "enter focus · ] next waiting · z collapse · n new · tab/esc focus · q quit"
	} else if m.viewMode == viewWorkbench {
		hint = "esc focus · 1-4 panel · e edit · f follow · i attach · ] next waiting · q quit"
	}
```

In `updateHandleWindowSizeEvent` (line 687): replace the single `m.splitPane.SetSize(paneWidth, contentHeight)` call with:

```go
	if m.viewMode == viewWorkbench {
		// Workbench: no rail; splitPane (agent only) left, panel right.
		// Order matters — SplitPane.SetSize zeroes the hidden terminal,
		// Workbench.SetSize re-sizes it for the panel when its tab shows.
		listWidth = 0
		leftW := int(float64(msg.Width) * m.workbenchRatio())
		m.splitPane.SetSize(leftW, contentHeight)
		m.workbench.SetSize(msg.Width-leftW, contentHeight)
		m.wbLeftWidth = leftW
	} else {
		m.splitPane.SetSize(paneWidth, contentHeight)
	}
```

(The earlier `listWidth`/`paneWidth` computation stays; workbench just overrides `listWidth` to 0 before `m.listWidth = listWidth` is cached — move the override *above* that cache line if needed. Nil-guard `m.workbench` like `m.overview` is guarded, for bare test homes.)

In `attachCursor` (line 3232): keep the overview early-return, then:

```go
	xOff := m.listWidth
	if m.viewMode == viewWorkbench {
		if m.splitPane.GetFocusedPane() != ui.FocusAgent {
			return // terminal renders in the panel; split-local mapping is wrong there
		}
		xOff = 0
	}
```

…and use `xOff+lx` instead of `m.listWidth+lx` in the `tea.NewCursor` call.

In `handleQuit` (find it: `grep -n "func (m \*home) handleQuit" app/app.go`): add `m.flushWorkbenchRatio()` alongside the existing pref-flush calls, and ensure the persisted `ViewMode` for workbench saves as focus — check where `p.ViewMode` is written on quit/slot-save; if it snapshots `m.viewMode`, map `viewWorkbench → ""`.

- [ ] **Step 7: Run — expect PASS**, race check, commit

```bash
CGO_ENABLED=0 go test ./app -run TestWorkbench_ -v
CGO_ENABLED=0 go test ./app ./ui
gofmt -w app ui && git add app ui && git commit -m "feat(app): workbench view mode — enter/esc/tab, key gating, layout"
```

---

### Task 9: Markdown data flow — scan, load, files list

**Files:** Modify: `app/workbench.go` (replace stubs), `app/app.go` (`tickUpdateMetadataMessage` case ~line 1203, `instanceChanged` ~line 1891, new message cases in `Update`) · Test: `app/workbench_flow_test.go`

- [ ] **Step 1: Write the failing tests**

```go
package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWbScanMsg_NewFileTriggersLoad(t *testing.T) {
	m := newWorkbenchTestHome(t)
	handleStateDefaultKey(m, key("enter"))
	sel := m.list.GetSelectedInstance()
	require.NotNil(t, sel)

	_, cmd := m.Update(wbScanMsg{title: sel.Title, path: "/tmp/plan.md", mtime: time.Now()})
	assert.NotNil(t, cmd, "a new most-recent file must dispatch a load")
}

func TestWbScanMsg_IgnoredWhenPinnedEditingOrStale(t *testing.T) {
	m := newWorkbenchTestHome(t)
	handleStateDefaultKey(m, key("enter"))
	sel := m.list.GetSelectedInstance()

	// stale title
	_, cmd := m.Update(wbScanMsg{title: "other", path: "/tmp/a.md", mtime: time.Now()})
	assert.Nil(t, cmd)

	// pinned
	m.workbench.Markdown.SetFollowing(false)
	_, cmd = m.Update(wbScanMsg{title: sel.Title, path: "/tmp/a.md", mtime: time.Now()})
	assert.Nil(t, cmd)
	m.workbench.Markdown.SetFollowing(true)

	// editing
	m.workbench.Markdown.SetDocument("/tmp/a.md", "x", time.Now())
	require.True(t, m.workbench.Markdown.StartEdit())
	_, cmd = m.Update(wbScanMsg{title: sel.Title, path: "/tmp/b.md", mtime: time.Now()})
	assert.Nil(t, cmd)
}

func TestWbLoadMsg_AppliesDocument(t *testing.T) {
	m := newWorkbenchTestHome(t)
	handleStateDefaultKey(m, key("enter"))
	sel := m.list.GetSelectedInstance()

	now := time.Now()
	m.Update(wbLoadMsg{title: sel.Title, path: "/tmp/plan.md", raw: "# Plan\n", mtime: now, follow: true})
	assert.Equal(t, "/tmp/plan.md", m.workbench.Markdown.Path())
	assert.True(t, m.workbench.Markdown.Following())
}

func TestLoadMarkdownCmd_ReadsFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "doc.md")
	require.NoError(t, os.WriteFile(p, []byte("# Doc\n"), 0o644))

	msg := loadMarkdownCmd("sess", p, true)()
	lm, ok := msg.(wbLoadMsg)
	require.True(t, ok)
	assert.NoError(t, lm.err)
	assert.Equal(t, "# Doc\n", lm.raw)
	assert.True(t, lm.follow)
}
```

- [ ] **Step 2: Run — expect FAIL**

```bash
CGO_ENABLED=0 go test ./app -run 'TestWb|TestLoadMarkdown' -v
```

- [ ] **Step 3: Implement messages + Cmds** (`app/workbench.go`, replacing Task 8 stubs)

```go
import (
	"os"
	"time"

	"github.com/aidan-bailey/loom/session/files"
)

// wbScanMsg reports the follow scan's most-recent markdown file.
// title pins the result to the session it was scanned for, so a
// selection change between dispatch and delivery drops it stale.
type wbScanMsg struct {
	title string
	path  string
	mtime time.Time
	err   error
}

// wbLoadMsg delivers a loaded document (follow reloads and manual
// files-tab opens both land here; follow records which).
type wbLoadMsg struct {
	title  string
	path   string
	raw    string
	mtime  time.Time
	follow bool
	err    error
}

// wbFilesMsg delivers the files-tab listing.
type wbFilesMsg struct {
	title string
	root  string
	paths []string
	err   error
}

// workbenchScanCmd scans the selected instance's worktree for the
// most recent markdown file. All I/O in the Cmd goroutine.
func (m *home) workbenchScanCmd() tea.Cmd {
	sel := m.list.GetSelectedInstance()
	if sel == nil || !sel.Started() || sel.Paused() {
		return nil
	}
	title, root := sel.Title, sel.GetWorktreePath()
	if root == "" {
		return nil
	}
	return func() tea.Msg {
		path, mtime, ok, err := files.MostRecentMarkdown(root)
		if err != nil || !ok {
			return wbScanMsg{title: title, err: err}
		}
		return wbScanMsg{title: title, path: path, mtime: mtime}
	}
}

func loadMarkdownCmd(title, path string, follow bool) tea.Cmd {
	return func() tea.Msg {
		info, err := os.Stat(path)
		if err != nil {
			return wbLoadMsg{title: title, path: path, follow: follow, err: err}
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return wbLoadMsg{title: title, path: path, follow: follow, err: err}
		}
		return wbLoadMsg{title: title, path: path, raw: string(raw),
			mtime: info.ModTime(), follow: follow}
	}
}

func (m *home) workbenchFilesCmd() tea.Cmd {
	sel := m.list.GetSelectedInstance()
	if sel == nil {
		return nil
	}
	title, root := sel.Title, sel.GetWorktreePath()
	if root == "" {
		return nil
	}
	return func() tea.Msg {
		res, err := files.List(root)
		if err != nil {
			return wbFilesMsg{title: title, root: root, err: err}
		}
		return wbFilesMsg{title: title, root: root, paths: res.Paths}
	}
}

// workbenchRefresh kicks the initial scan + files load on entry.
func (m *home) workbenchRefresh() tea.Cmd {
	return tea.Batch(m.workbenchScanCmd(), m.workbenchFilesCmd())
}

// wbCurrentTitle guards stale message delivery.
func (m *home) wbCurrentTitle() (string, bool) {
	sel := m.list.GetSelectedInstance()
	if m.viewMode != viewWorkbench || sel == nil {
		return "", false
	}
	return sel.Title, true
}
```

- [ ] **Step 4: Add `Update` cases** (`app/app.go`, alongside the other message cases — e.g. after `metadataReadyMsg`)

```go
	case wbScanMsg:
		title, ok := m.wbCurrentTitle()
		if !ok || msg.title != title || msg.err != nil {
			return m, nil
		}
		md := m.workbench.Markdown
		if !md.Following() || md.Editing() {
			return m, nil
		}
		if msg.path == "" {
			md.Clear()
			return m, nil
		}
		if msg.path == md.Path() && !msg.mtime.After(md.Mtime()) {
			return m, nil
		}
		return m, loadMarkdownCmd(title, msg.path, true)
	case wbLoadMsg:
		title, ok := m.wbCurrentTitle()
		if !ok || msg.title != title {
			return m, nil
		}
		if msg.err != nil {
			// File vanished between scan and read (agent moved it):
			// clear and let the next tick's scan re-resolve.
			m.workbench.Markdown.Clear()
			return m, nil
		}
		if m.workbench.Markdown.Editing() {
			return m, nil // never clobber an open editor
		}
		m.workbench.Markdown.SetDocument(msg.path, msg.raw, msg.mtime)
		m.workbench.Markdown.SetFollowing(msg.follow)
		return m, nil
	case wbFilesMsg:
		title, ok := m.wbCurrentTitle()
		if !ok || msg.title != title || msg.err != nil {
			return m, nil
		}
		m.workbench.SetFiles(msg.root, msg.paths)
		return m, nil
```

- [ ] **Step 5: Hook the health tick** — in the `tickUpdateMetadataMessage` case (line ~1248), before `return m, tea.Batch(cmds...)`:

```go
		if m.viewMode == viewWorkbench {
			if scan := m.workbenchScanCmd(); scan != nil {
				cmds = append(cmds, scan)
			}
		}
```

- [ ] **Step 6: Retarget on selection change** — in `instanceChanged` (line 1891), where the selected instance is already resolved and `m.viewMode != viewOverview` work happens, add:

```go
	if m.viewMode == viewWorkbench && selected != nil {
		m.workbench.SetSession(selected.Title, selected.GetWorktreePath())
		m.workbench.Diff().SetDiff(selected)
	}
```

(`SetSession` no-ops on an unchanged title, so `]`/`[` jumps get a fresh panel and same-session refreshes stay cheap. Also verify `DiffPane.SetDiff` is the right refresh call by mirroring what `SplitPane.UpdateDiff` does at `ui/split_pane.go:301`.)

- [ ] **Step 7: Run — expect PASS**, commit

```bash
CGO_ENABLED=0 go test ./app -run 'TestWb|TestLoadMarkdown|TestWorkbench_' -v
gofmt -w app && git add app && git commit -m "feat(app): workbench markdown follow scan, load, and files listing"
```

---

### Task 10: Save, conflict guard, discard confirm

**Files:** Modify: `app/workbench.go` (replace remaining stubs) + `Update` cases in `app/app.go` · Test: `app/workbench_flow_test.go`

- [ ] **Step 1: Write the failing tests**

```go
func TestSaveMarkdownCmd_CleanSaveWrites(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "doc.md")
	require.NoError(t, os.WriteFile(p, []byte("old"), 0o644))
	info, _ := os.Stat(p)

	msg := saveMarkdownCmd("s", p, "new", info.ModTime(), false)()
	sm, ok := msg.(wbSaveMsg)
	require.True(t, ok)
	assert.NoError(t, sm.err)
	assert.False(t, sm.conflict)
	got, _ := os.ReadFile(p)
	assert.Equal(t, "new", string(got))
}

func TestSaveMarkdownCmd_ConflictDetected(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "doc.md")
	require.NoError(t, os.WriteFile(p, []byte("old"), 0o644))
	loaded := time.Now().Add(-time.Hour) // disk is newer than our load

	msg := saveMarkdownCmd("s", p, "new", loaded, false)()
	sm := msg.(wbSaveMsg)
	assert.True(t, sm.conflict)
	got, _ := os.ReadFile(p)
	assert.Equal(t, "old", string(got), "conflicted save must not write")
}

func TestSaveMarkdownCmd_ForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "doc.md")
	require.NoError(t, os.WriteFile(p, []byte("old"), 0o644))

	msg := saveMarkdownCmd("s", p, "new", time.Now().Add(-time.Hour), true)()
	sm := msg.(wbSaveMsg)
	assert.NoError(t, sm.err)
	assert.False(t, sm.conflict)
	got, _ := os.ReadFile(p)
	assert.Equal(t, "new", string(got))
}

func TestWbSaveMsg_SuccessExitsEditMode(t *testing.T) {
	m := newWorkbenchTestHome(t)
	handleStateDefaultKey(m, key("enter"))
	sel := m.list.GetSelectedInstance()
	md := m.workbench.Markdown
	md.SetDocument("/tmp/doc.md", "old", time.Now())
	require.True(t, md.StartEdit())

	m.Update(wbSaveMsg{title: sel.Title, path: "/tmp/doc.md", content: "new", mtime: time.Now()})
	assert.False(t, md.Editing(), "successful save drops back to the rendered view")
	assert.False(t, md.EditDirty(), "saved content becomes the new baseline")
}
```

- [ ] **Step 2: Run — expect FAIL**

```bash
CGO_ENABLED=0 go test ./app -run TestSaveMarkdown -v
```

- [ ] **Step 3: Implement** (`app/workbench.go`)

```go
import "github.com/aidan-bailey/loom/ui/overlay"

// wbSaveMsg reports a save attempt. conflict=true means the file
// changed on disk after load and force was false — nothing written.
type wbSaveMsg struct {
	title    string
	path     string
	content  string
	mtime    time.Time
	conflict bool
	err      error
}

func saveMarkdownCmd(title, path, content string, loadedMtime time.Time, force bool) tea.Cmd {
	return func() tea.Msg {
		if !force {
			if info, err := os.Stat(path); err == nil && info.ModTime().After(loadedMtime) {
				return wbSaveMsg{title: title, path: path, content: content, conflict: true}
			}
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return wbSaveMsg{title: title, path: path, err: err}
		}
		info, err := os.Stat(path)
		if err != nil {
			return wbSaveMsg{title: title, path: path, err: err}
		}
		return wbSaveMsg{title: title, path: path, content: content, mtime: info.ModTime()}
	}
}

// saveWorkbenchMarkdown dispatches a save of the open editor buffer.
func (m *home) saveWorkbenchMarkdown(force bool) tea.Cmd {
	md := m.workbench.Markdown
	sel := m.list.GetSelectedInstance()
	if sel == nil || !md.Editing() {
		return nil
	}
	return saveMarkdownCmd(sel.Title, md.Path(), md.EditValue(), md.Mtime(), force)
}

// confirmDiscardEdit asks before dropping unsaved editor changes.
// Mirror the confirmTask usage pattern of the kill flow (see
// app.go:2301 and an existing caller for ConfirmationTask's shape —
// Sync runs on confirm on the main goroutine).
func (m *home) confirmDiscardEdit() tea.Cmd {
	return m.confirmTask("Discard unsaved changes?", overlay.ConfirmationTask{
		Sync: func() { m.workbench.Markdown.CancelEdit() },
	})
}
```

And the `Update` case (`app/app.go`):

```go
	case wbSaveMsg:
		title, ok := m.wbCurrentTitle()
		if !ok || msg.title != title {
			return m, nil
		}
		if msg.err != nil {
			return m, m.handleError(msg.err)
		}
		if msg.conflict {
			path, content, loaded := msg.path, msg.content, m.workbench.Markdown.Mtime()
			_ = loaded
			return m, m.confirmTask(
				filepath.Base(msg.path)+" changed on disk — overwrite?",
				overlay.ConfirmationTask{
					Async: saveMarkdownCmd(title, path, content, time.Time{}, true),
				})
		}
		m.workbench.Markdown.ApplySaved(msg.content, msg.mtime)
		return m, nil
```

Adapt `ConfirmationTask`'s field usage (`Sync` func vs `Async` tea.Cmd) to the real struct — check `go doc ./ui/overlay ConfirmationTask` and an existing `confirmTask` caller; drop the unused `loaded` var. Add `"path/filepath"` and `"time"` imports to app.go if absent.

- [ ] **Step 4: Run — expect PASS**, commit

```bash
CGO_ENABLED=0 go test ./app -run 'TestSaveMarkdown|TestWbSaveMsg' -v
gofmt -w app && git add app && git commit -m "feat(app): workbench markdown save with mtime conflict guard"
```

---

### Task 11: Mouse wheel, docs, verification sweep

**Files:** Modify: `app/app.go` (MouseWheelMsg case, line ~1288), `CLAUDE.md`, `USAGE.md`, `docs/superpowers/specs/2026-07-22-session-workbench-design.md`

- [ ] **Step 1: Wheel routing** — at the top of the `tea.MouseWheelMsg` case, after the overview early-return:

```go
		if m.viewMode == viewWorkbench {
			mouse := msg.Mouse()
			if mouse.Button != tea.MouseWheelUp && mouse.Button != tea.MouseWheelDown {
				return m, nil
			}
			if mouse.X >= m.wbLeftWidth {
				// Right panel: scroll the active tab (terminal tab
				// no-ops in v1 — its scroll state is shared with the
				// hidden focus split).
				if m.workbench.Tab() != ui.WbTabTerminal {
					if mouse.Button == tea.MouseWheelUp {
						m.workbenchScrollUp()
					} else {
						m.workbenchScrollDown()
					}
				}
				return m, nil
			}
			// Left half: agent scroll via the existing split machinery.
			if mouse.Button == tea.MouseWheelUp {
				m.splitPane.ScrollAgentUp()
			} else {
				m.splitPane.ScrollAgentDown()
			}
			return m, nil
		}
```

Note: drag-select over the agent pane needs **no changes** — the click/motion handlers hit-test with `mouse.X - m.listWidth` (0 in workbench) against the splitPane, which is sized to the left half; coordinates beyond it fail `HitTest` and no-op. Verify manually in Step 3.

- [ ] **Step 2: Docs**

`CLAUDE.md`: add to the keybindings table —

```markdown
| `enter` | (focus) Open the workbench for the selected session |
| `esc` | (workbench) Return to focus mode |
| `1`–`4` | (workbench) Select panel tab (markdown / diff / files / terminal) |
| `e` / `f` | (workbench) Edit the shown markdown / resume follow mode |
| `ctrl+left` / `ctrl+right` | (workbench) Resize the agent/panel split (persisted per session) |
```

…and a Gotchas bullet:

```markdown
- **Workbench mode reuses the focus split for its left half.** `viewWorkbench` force-hides the split's terminal (restored from `wbPrevTerminalHidden` on exit) and shares its `TerminalPane` with the right panel via `SplitPane.Terminal()` — `SplitPane.SetSize` must run before `Workbench.SetSize` (the former zeroes the hidden terminal, the latter re-sizes it for the panel). The markdown follow scan rides the 3s health tick as a `tea.Cmd`; scan/load/save results are applied only in `Update` handlers, gated on session title to drop stale deliveries. Workbench is never persisted: quit saves `ViewMode` as focus. Glamour styles rebuild in a theme hook (`ui/markdown_style.go`); `MarkdownPane` re-renders lazily off the `mdStyleGen` counter.
```

`USAGE.md`: add a "Session workbench" section documenting entry (`enter`), the four tabs, follow/pin (`f`), edit (`e`, `ctrl+s`, `esc`), and the conflict prompt.

Spec: append the three v1 deviations from this plan's header to a "Deviations (v1)" section.

- [ ] **Step 3: Full verification sweep**

```bash
gofmt -w .
CGO_ENABLED=0 go test ./...
go vet ./...
CC=clang CGO_ENABLED=1 go test -race ./app ./ui ./session/files
CGO_ENABLED=0 go build -o loom
```

Expected: all pass, build succeeds. Then a manual smoke: run `./loom` in a scratch repo (see the smoke-test isolation memory: `HOME` override + `env -u TMUX`), enter a session, press `enter`, confirm the panel renders, `1`–`4` switch tabs, a `.md` written in the worktree appears within ~3s, `e`/`ctrl+s` round-trips an edit, `esc` returns to focus with the terminal pane restored.

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "feat(app): workbench mouse routing; docs for workbench mode"
```

---

## Self-review notes (already applied)

- **Spec coverage:** mode wiring → Task 8; layout/tabs → Tasks 7–8; markdown viewer/follow/pin/edit/conflict → Tasks 3–6, 9–10; diff/files/terminal panels → Task 7 (+ diff refresh in Task 9 Step 6); concurrency rules → Cmd/Update split throughout; ratio persistence → Tasks 2, 8; mouse → Task 11; testing → per-task; docs → Task 11.
- **Known intentional deviations** are listed in the header and written back to the spec in Task 11.
- **Types cross-check:** `wbScanMsg/wbLoadMsg/wbFilesMsg/wbSaveMsg` defined Task 9–10, consumed in the same tasks; `Workbench` API (`SetSession/SetTab/SetFiles/FileUnderCursor/FilesUp/FilesDown/Diff/Root/String`) defined Task 7, consumed Tasks 8–11; `MarkdownPane` API defined Tasks 5–6, consumed Tasks 8–10; `viewWorkbench`, `wbLeftWidth`, `wbRatio`, `wbPrevTerminalHidden` defined Task 8, consumed Tasks 8–11.
- Drafting artifacts in the Task 7/10 test listings were removed during self-review.
