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
		"turns.json":           &turnsJSON{},
		"agent-turns.json":     &turnsJSON{},
		"diff.json":            &patchJSON{},
		"launch-defaults.json": &launchDefaultsJSON{},
		"job-log.json":         &jobLogJSON{},
		"job-log-after.json":   &jobLogJSON{},
		"flow.json":            &flowJSON{},
		"flows.json":           &flowsJSON{},
		"flow-doc.json":        &flowDocJSON{},
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(dir, name))
			require.NoError(t, err)
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.DisallowUnknownFields()
			require.NoError(t, dec.Decode(into))
		})
	}
	page, err := os.ReadFile(filepath.Join(dir, "mockup.html"))
	require.NoError(t, err)
	require.Contains(t, string(page), "window.brainstorm", "the mockup is a page the companion would serve (mockup.Fetch's marker)")
	canned, err := os.ReadFile(filepath.Join("..", "..", "tools", "webshot", "canned.go"))
	require.NoError(t, err)
	require.Contains(t, string(canned), `"`+mockupCSP+`"`, "webshot frames its mockup under the policy the layer serves it under")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 11, "a new fixture is checked here too")
}
