package reviewui

import (
	"github.com/aidan-bailey/loom/review"
	gitpkg "github.com/aidan-bailey/loom/review/gitdiff"
)

// LoadedDoc carries one tab's document, per-file diff, and review state
// back from the load Cmd. Doc is nil for binary/deleted placeholder
// tabs. Diff is nil in doc mode, for placeholder tabs, and when the
// per-file diff failed — the tab then renders without change markers.
type LoadedDoc struct {
	Path  string
	Doc   *review.Document
	Diff  *gitpkg.DiffInfo
	State *review.ReviewState
}

// LoadedMsg reports the result of reading every tab's document and
// review state off the Update goroutine.
type LoadedMsg struct {
	Title string
	Docs  []LoadedDoc

	// Err is fatal and means the load had nothing left to show — every
	// file was unreadable and there were no binary/deleted placeholder
	// tabs to fall back on. A single unreadable file among several is
	// NOT an Err: that tab arrives with a nil Doc and renders as a
	// placeholder while the rest load normally.
	Err error
}

// SavedMsg reports a background review-state write. A non-nil Err is
// non-fatal — it surfaces in the footer, leaving the review usable, and
// is cleared by the next successful save.
type SavedMsg struct {
	Title string
	Err   error
}
