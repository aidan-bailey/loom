package reviewui

import (
	"sort"
	"strings"

	"github.com/aidan-bailey/loom/review"
	gitpkg "github.com/aidan-bailey/loom/review/gitdiff"
)

// FileTab holds per-file state for a single tab in the code review TUI.
type FileTab struct {
	path string // absolute path used for all document/review I/O
	// display is the label shown in the tab bar and header. In code
	// review mode it stays repo-relative even though path is absolute.
	display      string
	doc          *review.Document
	state        *review.ReviewState
	changedLines map[int]bool                 // line numbers that are added/modified
	deletedAfter map[int][]gitpkg.DeletedLine // deleted lines keyed by new-file line they appear after
	changeChunks []changeChunk                // contiguous groups of changed lines
	cursorLine   int                          // 1-based

	// Visual selection mode
	selecting    bool
	selectAnchor int

	// Sidebar annotation list and cursor
	sidebarItems  []sidebarItem
	sidebarCursor int

	// Annotation focus
	cursorOnAnnotation bool
	cursorAnnoIdx      int

	// Placeholder tabs
	isBinary  bool
	isDeleted bool

	// Cached rendering data (computed once per tab switch, not per keystroke)
	chromaLines      []string         // syntax-highlighted lines (nil for markdown)
	deletedLineCache map[int][]string // pre-highlighted deleted line content, keyed by afterLine
	isMarkdown       bool
}

// changeChunk represents a contiguous block of changed lines.
type changeChunk struct {
	startLine int
	endLine   int
}

// newFileTab creates a new FileTab from a DiffInfo. path is absolute
// (all doc I/O uses it); display is the label rendered in the tab bar.
func newFileTab(path, display string, diff *gitpkg.DiffInfo) FileTab {
	ft := FileTab{
		path:       path,
		display:    display,
		cursorLine: 1,
	}
	if diff != nil {
		ft.changedLines = diff.ChangedLines
		ft.deletedAfter = diff.DeletedAfter
		ft.changeChunks = computeChangeChunks(diff)
	}
	return ft
}

// ensureHighlightCache populates the Chroma highlight cache if needed.
// Call this once after loading a tab's document, not on every rebuild.
func (ft *FileTab) ensureHighlightCache() {
	if ft.doc == nil || ft.chromaLines != nil {
		return
	}
	ft.isMarkdown = strings.HasSuffix(strings.ToLower(ft.path), ".md")
	if ft.isMarkdown {
		return
	}
	ft.chromaLines = highlightCode(ft.path, ft.doc.Content)

	// Pre-highlight deleted lines
	ft.deletedLineCache = make(map[int][]string)
	for afterLine, dels := range ft.deletedAfter {
		highlighted := make([]string, len(dels))
		for i, del := range dels {
			hl := highlightCode(ft.path, del.Content)
			if len(hl) > 0 {
				highlighted[i] = hl[0]
			} else {
				highlighted[i] = del.Content
			}
		}
		ft.deletedLineCache[afterLine] = highlighted
	}
}

// computeChangeChunks groups contiguous changed/deleted lines into navigable chunks.
// Each chunk represents one visual change region that n/N should jump to.
func computeChangeChunks(diff *gitpkg.DiffInfo) []changeChunk {
	if diff == nil {
		return nil
	}

	// Collect all lines that are visually part of a change.
	// Added/modified lines are directly involved.
	// Deleted lines render before afterLine+1, so mark that line.
	involved := make(map[int]bool)
	for ln := range diff.ChangedLines {
		involved[ln] = true
	}
	for afterLine := range diff.DeletedAfter {
		// Deleted lines render just above afterLine+1, so that's
		// the line the cursor should land on.
		target := afterLine + 1
		if target < 1 {
			target = 1
		}
		involved[target] = true
	}

	if len(involved) == 0 {
		return nil
	}

	// Sort line numbers
	lines := make([]int, 0, len(involved))
	for ln := range involved {
		lines = append(lines, ln)
	}
	sort.Ints(lines)

	// Group strictly contiguous lines into chunks.
	var chunks []changeChunk
	start := lines[0]
	end := lines[0]
	for _, ln := range lines[1:] {
		if ln == end+1 {
			end = ln
		} else {
			chunks = append(chunks, changeChunk{startLine: start, endLine: end})
			start = ln
			end = ln
		}
	}
	chunks = append(chunks, changeChunk{startLine: start, endLine: end})

	return chunks
}
