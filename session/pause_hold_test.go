package session

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestHoldPauseForE2E(t *testing.T) {
	orig := e2ePauseHold
	t.Cleanup(func() { e2ePauseHold = orig })

	marker := filepath.Join(t.TempDir(), "held")
	e2ePauseHold = marker + "|50ms"
	began := time.Now()
	holdPauseForE2E()
	assert.FileExists(t, marker, "the marker says the pause is in flight")
	assert.GreaterOrEqual(t, time.Since(began), 50*time.Millisecond)

	for _, v := range []string{"", "no-duration", "|1s", marker + "|soon"} {
		e2ePauseHold = v
		began = time.Now()
		holdPauseForE2E()
		assert.Less(t, time.Since(began), 50*time.Millisecond, "%q holds nothing", v)
	}
}
