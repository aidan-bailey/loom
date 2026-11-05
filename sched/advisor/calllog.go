package advisor

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/sched"
)

// The call log (smart scheduler spec §16): one JSON line per call, the
// last MaxCalls kept.
const (
	CallsFile   = "advisor.jsonl"
	MaxCalls    = 500
	CallVersion = 1
)

// The kinds of call.
const (
	KindPrior     = "prior"
	KindReconcile = "reconcile"
)

// The outcomes of a call.
const (
	OutcomeAnswered = "answered"
	OutcomeFailed   = "failed"
	OutcomeSkipped  = "skipped"
)

// CallRecord is one line of the call log: a call that ended, or one that
// was dropped before it ran. It holds no request: a request carries the
// job's argv, description and filtered environment.
type CallRecord struct {
	V         int               `json:"v"`
	At        time.Time         `json:"at"`   // when it ended, or was dropped
	Kind      string            `json:"kind"` // KindPrior or KindReconcile
	Job       sched.JobID       `json:"job"`
	Signature sched.SignatureID `json:"signature"`
	Repo      string            `json:"repo,omitempty"`
	Command   string            `json:"command,omitempty"`
	Model     string            `json:"model,omitempty"` // asked for; Usage.Model answered
	Effort    string            `json:"effort,omitempty"`
	WaitedMS  int64             `json:"waited_ms"` // from being queued to running
	Usage     Usage             `json:"usage"`
	Outcome   string            `json:"outcome"`
	Error     string            `json:"error,omitempty"`
	Answer    string            `json:"answer,omitempty"` // the structured answer as given
	Surprise  string            `json:"surprise,omitempty"`
}

// LoadCalls reads dir's call log, oldest first; none when there is no file.
// It never writes: a line that isn't a call record, or one from a newer
// version, is an error, and the caller leaves the file as it is.
func LoadCalls(dir string) ([]CallRecord, error) {
	path := filepath.Join(dir, CallsFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var calls []CallRecord
	for i, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var c CallRecord
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			return nil, fmt.Errorf("parse %s line %d: %w", path, i+1, err)
		}
		if c.V > CallVersion {
			return nil, fmt.Errorf("%s line %d has version %d, this loom reads up to %d", path, i+1, c.V, CallVersion)
		}
		calls = append(calls, c)
	}
	return calls, nil
}

// SaveCalls writes the last MaxCalls of calls as dir's call log, one JSON
// line each, atomically (mode 0600), stamping CallVersion.
func SaveCalls(dir string, calls []CallRecord) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	calls = calls[max(0, len(calls)-MaxCalls):]
	var buf bytes.Buffer
	for _, c := range calls {
		c.V = CallVersion
		line, err := json.Marshal(c)
		if err != nil {
			return fmt.Errorf("encode a call: %w", err)
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return config.AtomicWriteFile(filepath.Join(dir, CallsFile), buf.Bytes(), 0o600)
}
