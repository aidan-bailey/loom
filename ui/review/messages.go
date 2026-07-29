package reviewui

import "github.com/aidan-bailey/loom/review"

// LoadedDoc carries one tab's document and review state back from the
// load Cmd. Doc is nil for binary/deleted placeholder tabs.
type LoadedDoc struct {
	Path  string
	Doc   *review.Document
	State *review.ReviewState
}

// LoadedMsg reports the result of reading every tab's document and
// review state off the Update goroutine.
type LoadedMsg struct {
	Title string
	Docs  []LoadedDoc
	Err   error
}

// SavedMsg reports a background review-state write.
type SavedMsg struct {
	Title string
	Err   error
}
