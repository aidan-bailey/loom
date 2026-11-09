package flowview

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/internal/flowrun"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadDoc_OnlyTheFlowsOwnDocuments(t *testing.T) {
	repo, base, records, state := boardRepo(t)
	stem := strings.TrimSuffix(filepath.Base(planP), ".md")
	writeFile(t, repo, ".flow/drafts/"+stem+"/STATE.md", "**Phase:** committed\n")
	key, err := RepoKey(repo)
	require.NoError(t, err)
	writeFile(t, records, key+"/2026-11-10-p.md", "# Run: p\n")
	st := runState("r", flowrun.Running, flowrun.Story{ID: "1"})
	st.Agents = []string{"r-s1-dev1"}
	writeRun(t, state, st)
	writeFile(t, state, "agents/r-s1-dev1/report.md", "STATUS: DONE\n")
	writeFile(t, state, "agents/r-other/report.md", "STATUS: DONE\n")

	for doc, want := range map[string]string{
		DocSpec:                 "# P design\n",
		DocPlan:                 joined(specP, "", "1", "2", "3"),
		DocRecord:               "# Run: p\n",
		DocDraftsState:          "**Phase:** committed\n",
		DocReport + "r-s1-dev1": "STATUS: DONE\n",
	} {
		d, err := ReadDoc(repo, base, records, planP, doc)
		require.NoError(t, err, doc)
		assert.Equal(t, want, d.Text, doc)
		assert.False(t, d.Cut, doc)
	}
	sk, err := ReadDoc(repo, base, records, planP, DocSkeleton)
	require.NoError(t, err)
	assert.NotContains(t, sk.Text, "## Story 1:", "the skeleton stops at the marker")
	assert.Contains(t, sk.Text, "## Stories")
	assert.Equal(t, planP, sk.Path)

	for _, doc := range []string{DocReport + "r-other", DocReport + "../r-s1-dev1", "../etc/passwd", "plan/../x", "", "notes"} {
		_, err := ReadDoc(repo, base, records, planP, doc)
		assert.Error(t, err, doc)
	}
}

func TestReadDoc_CutsAtTheLimit(t *testing.T) {
	repo, base, records, _ := boardRepo(t)
	writeFile(t, repo, specP, "# P design\n"+strings.Repeat("x", DocLimit))
	d, err := ReadDoc(repo, base, records, planP, DocSpec)
	require.NoError(t, err)
	assert.True(t, d.Cut)
	assert.Len(t, d.Text, DocLimit)
}
