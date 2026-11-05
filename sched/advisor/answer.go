package advisor

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aidan-bailey/loom/sched"
)

// MaxNote is how many characters of a note are kept.
const MaxNote = 300

const maxDurationSeconds = 86400

// PriorAnswer is a prior as the advisor gives it.
type PriorAnswer struct {
	Like            sched.SignatureID `json:"like"`
	CPUs            int               `json:"cpus"`
	MemoryGB        int               `json:"memory_gb"`
	IO              bool              `json:"io"`
	DurationSeconds int               `json:"duration_seconds"`
	Note            string            `json:"note"`
}

// ParsePrior decodes a prior and checks it (spec §5.6): cores 1–maxCPUs
// (the default claim's), memory 1–maxMemoryGB (the cap), a duration of
// 1–86,400 s, and a like that is empty or a belief known says it was given.
// Any other answer is an error, and the call counts as failed. The note is
// trimmed and cut to MaxNote characters.
func ParsePrior(raw []byte, maxCPUs, maxMemoryGB int, known func(sched.SignatureID) bool) (PriorAnswer, error) {
	var a PriorAnswer
	if err := json.Unmarshal(raw, &a); err != nil {
		return PriorAnswer{}, fmt.Errorf("the agent's estimate is not the expected JSON: %w", err)
	}
	switch {
	case a.CPUs < 1 || a.CPUs > maxCPUs:
		return PriorAnswer{}, fmt.Errorf("the agent's estimate claims %d cores: it must be 1–%d", a.CPUs, maxCPUs)
	case a.MemoryGB < 1 || a.MemoryGB > maxMemoryGB:
		return PriorAnswer{}, fmt.Errorf("the agent's estimate claims %d GB: it must be 1–%d", a.MemoryGB, maxMemoryGB)
	case a.DurationSeconds < 1 || a.DurationSeconds > maxDurationSeconds:
		return PriorAnswer{}, fmt.Errorf("the agent's estimate lasts %d s: it must be 1–%d", a.DurationSeconds, maxDurationSeconds)
	case a.Like != "" && !known(a.Like):
		return PriorAnswer{}, fmt.Errorf("the agent's estimate is like belief %s, which it was not given", a.Like)
	}
	a.Note = cutNote(a.Note)
	return a, nil
}

// ParseNote decodes a reconcile's note, trimmed and cut to MaxNote
// characters; an empty one is an error.
func ParseNote(raw []byte) (string, error) {
	var a struct {
		Note string `json:"note"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("the agent's note is not the expected JSON: %w", err)
	}
	note := cutNote(a.Note)
	if note == "" {
		return "", errors.New("the agent wrote an empty note")
	}
	return note, nil
}

func cutNote(s string) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > MaxNote {
		r = r[:MaxNote]
	}
	return string(r)
}
