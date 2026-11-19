package web

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/core"
	"github.com/stretchr/testify/require"
)

func readFixture(t *testing.T, name string, into any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "tools", "webshot", "fixture", name))
	require.NoError(t, err)
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	require.NoError(t, dec.Decode(into))
}

// TestWebshotFixture_TheAsksAreOfKindsTheLayerSends holds each canned ask to a
// kind the layer sends and to the fields that kind carries.
func TestWebshotFixture_TheAsksAreOfKindsTheLayerSends(t *testing.T) {
	var snap snapshotJSON
	readFixture(t, "snapshot.json", &snap)
	n := 0
	for _, list := range snap.Sessions {
		for _, s := range list {
			for _, a := range s.Asks {
				n++
				switch a.Kind {
				case askKind(core.AskKindQuestion):
					require.NotEmpty(t, a.Questions, a.ID)
					require.Empty(t, a.Tool, a.ID)
				case askKind(core.AskKindPermission):
					require.NotEmpty(t, a.Tool, a.ID)
					require.NotEmpty(t, a.Input, a.ID)
					require.Empty(t, a.Questions, a.ID)
				default:
					t.Fatalf("ask %s has kind %q, which the layer never sends", a.ID, a.Kind)
				}
			}
		}
	}
	require.NotZero(t, n, "the canned loom holds at least one ask")
}

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
	require.Contains(t, string(canned), `"`+contentSecurityPolicy+`"`, "webshot serves the app's files under the policy the layer serves them under")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 11, "a new fixture is checked here too")
}

const fixtureMarkdown = `# Findings

The cursor *jumps* when the header **redraws**; see [the notes](https://example.com/pane).

- first cause
- second cause

3. fix the draw order
4. draw the cursor last

> Keep the cursor off the header row.

` + "```go" + `
p.drawCursor(frame)
` + "```" + `

---

| file | lines |
| :--- | ---: |
| ui/pane.go | 12 |
`

// TestWebshotFixture_TheMarkdownTreeIsWhatBuildTreeMakes holds the canned
// conversation's markdown text item to the tree buildTree makes of its source.
func TestWebshotFixture_TheMarkdownTreeIsWhatBuildTreeMakes(t *testing.T) {
	var page turnsJSON
	readFixture(t, "turns.json", &page)
	var got []*mdNode
	var badHref func(ns []*mdNode) bool
	badHref = func(ns []*mdNode) bool {
		for _, n := range ns {
			if (n.T == "a" && n.Href == "") || badHref(n.C) {
				return true
			}
		}
		return false
	}
	for _, turn := range page.Turns {
		for _, it := range turn.Items {
			require.False(t, badHref(it.Blocks), "every link of the fixture keeps its href")
			if len(it.Blocks) > 0 && it.Blocks[0].T == "h" {
				got = it.Blocks
			}
		}
	}
	require.NotNil(t, got, "a text item whose first block is a heading")
	want, cut := buildTree(fixtureMarkdown)
	require.False(t, cut)
	wantJSON, err := json.Marshal(want)
	require.NoError(t, err)
	gotJSON, err := json.Marshal(got)
	require.NoError(t, err)
	require.JSONEq(t, string(wantJSON), string(gotJSON))
}

// TestWebshotFixture_TheDiffsFilesAreOfStatusesAndOmissionsTheLayerSends holds
// the canned diff's files to the statuses and omissions the layer sends.
func TestWebshotFixture_TheDiffsFilesAreOfStatusesAndOmissionsTheLayerSends(t *testing.T) {
	var patch patchJSON
	readFixture(t, "diff.json", &patch)
	deleted, lines := false, false
	for _, f := range patch.Files {
		require.Contains(t, []string{"A", "M", "D", "R", "C"}, string(f.Status), f.Path)
		require.Contains(t, []string{"", "lines", "budget"}, string(f.Omitted), f.Path)
		deleted = deleted || f.Status == "D"
		lines = lines || f.Omitted == "lines"
	}
	require.True(t, deleted, "a deleted file")
	require.True(t, lines, "a file omitted for its lines")
}
