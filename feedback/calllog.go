package feedback

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/aidan-bailey/loom/sched/advisor"
)

// CallLogFile is the grouper's call log under <globalDir>/feedback: one
// JSON line per call, never the request (the notes are in feedback.json).
const CallLogFile = "grouper.jsonl"

// CallRecord is one grouper call.
type CallRecord struct {
	At      time.Time     `json:"at"`
	Note    NoteID        `json:"note"`
	Offered []ThreadID    `json:"offered,omitempty"`
	Model   string        `json:"model"`
	Effort  string        `json:"effort"`
	Usage   advisor.Usage `json:"usage"`
	Answer  string        `json:"answer,omitempty"`
	Thread  ThreadID      `json:"thread,omitempty"` // where the note went
	Err     string        `json:"err,omitempty"`
}

// AppendCall appends rec to dir's call log (mode 0600, created on first
// use).
func AppendCall(dir string, rec CallRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("encode a grouper call: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, CallLogFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open %s: %w", CallLogFile, err)
	}
	_, werr := f.Write(append(data, '\n'))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return fmt.Errorf("write %s: %w", CallLogFile, werr)
	}
	return nil
}
