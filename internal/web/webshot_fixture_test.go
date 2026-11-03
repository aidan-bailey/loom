package web

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWebshotFixture_IsTheJSONTheLayerServes holds tools/webshot's canned
// loom to the types this layer encodes: a field renamed or dropped here
// leaves a fixture field the decoder doesn't know, so the screenshots can't
// keep drawing an app fed JSON the daemon no longer sends.
func TestWebshotFixture_IsTheJSONTheLayerServes(t *testing.T) {
	dir := filepath.Join("..", "..", "tools", "webshot", "fixture")
	for name, into := range map[string]any{
		"snapshot.json":        &snapshotJSON{},
		"message.json":         &messageJSON{},
		"turns.json":           &turnsJSON{},
		"diff.json":            &patchJSON{},
		"launch-defaults.json": &launchDefaultsJSON{},
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(dir, name))
			require.NoError(t, err)
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.DisallowUnknownFields()
			require.NoError(t, dec.Decode(into))
		})
	}
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 5, "a new fixture is checked here too")
}
