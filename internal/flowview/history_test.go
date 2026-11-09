package flowview

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// engineRecord is a record in the shape `loom flow run` writes
// (flow/plugin/docs/records.md), with two -final- rows.
const engineRecord = `# Run: 2026-11-06-p

- Plan: docs/superpowers/plans/2026-11-06-p.md (4 stories)
- Spec: docs/superpowers/specs/2026-11-06-p-design.md
- Commits: 537501935..8089d33f8 (24)
- Start: 2026-11-06T13:10:52Z, End: 2026-11-06T15:10:55Z
- Ended: stopped
- Engine: loom flow run (flow in loom part 3b)

## Cost

### Cost by agent

| Agent | Type | Model | Requests | Input | Output | Cache read | Write 5m | Write 1h | Over 100K | USD | Est. thinking | USD incl. est. |
|---|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| manager c4f58f5c | main | claude-opus-5-5 | 59 | 126 | 17,653 | 5,180,744 | 0 | 53,369 | 0 | $1.82 | 419 | $1.83 |
| r-s1-dev1 | intermediate-dev | claude-sonnet-5-5 | 15 | 30 | 5,791 | 858,214 | 0 | 77,222 | 0 | $0.5385 | 150 | $0.5400 |
| r-final-review1 | code-quality-reviewer | claude-opus-5-5 | 18 | 36 | 9,405 | 1,188,141 | 0 | 90,678 | 0 | $1.15 | 1,509 | $1.18 |
| r-final-fix1 | intermediate-dev | claude-sonnet-5-5 | 4 | 8 | 528 | 90,745 | 0 | 27,025 | 0 | $0.1600 | 0 | $0.1600 |

### Cost by type

| Type | Agents | Requests | Input | Output | Cache read | Write 5m | Write 1h | Over 100K | USD | Est. thinking | USD incl. est. |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| **Total** | 4 | 96 | 200 | 33,377 | 7,317,844 | 0 | 248,294 | 0 | $10.08 | 2,078 | $10.15 |

<!-- agent-costs {"run": "2026-11-06-p", "total": {"usd": 10.08}} -->
<!-- agent-costs-managers {"c4f58f5c": ["claude-opus-5-5"]} -->

## Stories

| Story | Developer |
|---|---|
| 1 | intermediate-dev |
`

// supervisedRecord is a supervised run's record: bold labels, no Ended
// line, two manager sessions' comments.
const supervisedRecord = `# Run

- **Plan:** ` + "`docs/superpowers/plans/2026-10-22-q.md`" + ` (9 stories)
- **Start:** 2026-10-22T09:00:00Z, **End:** 2026-10-22T18:00:00Z

## Cost

<!-- agent-costs {"total": {"usd": 20.5}} -->

<!-- agent-costs {"total": {"usd": 11.27}} -->
`

func TestHistory_ReadsEachRecord(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "-k/2026-11-06-p.md", engineRecord)
	writeFile(t, dir, "-k/2026-10-22-q.md", supervisedRecord)
	got, err := History(dir)
	require.NoError(t, err)
	require.Len(t, got, 2)
	p := got[0]
	assert.Equal(t, "-k", p.Key)
	assert.Equal(t, "2026-11-06-p.md", p.File)
	assert.Equal(t, "2026-11-06-p", p.Stem)
	assert.Equal(t, "docs/superpowers/plans/2026-11-06-p.md", p.Plan)
	assert.Equal(t, 4, p.Stories)
	assert.Equal(t, "stopped", p.Ended)
	assert.Equal(t, time.Date(2026, 11, 6, 15, 10, 55, 0, time.UTC), p.End)
	assert.InDelta(t, 10.08, p.USD, 1e-9)
	assert.True(t, p.HasUSD)
	assert.InDelta(t, 1.31, p.FinalUSD, 1e-9)
	assert.True(t, p.HasFinal)
	assert.Empty(t, p.Unreadable)

	q := got[1]
	assert.Equal(t, "docs/superpowers/plans/2026-10-22-q.md", q.Plan)
	assert.Equal(t, 9, q.Stories)
	assert.Equal(t, "supervised", q.Ended)
	assert.InDelta(t, 31.77, q.USD, 1e-9, "a run's manager sessions are summed")
	assert.False(t, q.HasFinal)
	assert.Equal(t, time.Date(2026, 10, 22, 18, 0, 0, 0, time.UTC), q.End)
}

func TestHistory_AnUndecodableRecordIsListedUnreadable(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "-k/2026-11-01-a.md", "# Run\n\nno header\n")
	writeFile(t, dir, "-k/2026-11-02-b.md", strings.Replace(engineRecord, `{"run": "2026-11-06-p", "total": {"usd": 10.08}}`, `{not json}`, 1))
	got, err := History(dir)
	require.NoError(t, err)
	require.Len(t, got, 2, "never skipped")
	byFile := map[string]PastRun{}
	for _, r := range got {
		byFile[r.File] = r
	}
	assert.Contains(t, byFile["2026-11-01-a.md"].Unreadable, "no Plan line")
	assert.Contains(t, byFile["2026-11-02-b.md"].Unreadable, "agent-costs")
	assert.False(t, byFile["2026-11-02-b.md"].HasUSD)
}

func TestHistory_LeavesRetrosOut(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "retros/retro-2026-11-01.md", "# Retro\n")
	writeFile(t, dir, "-k/2026-11-06-p.md", engineRecord)
	got, err := History(dir)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "-k", got[0].Key)

	none, err := History(t.TempDir() + "/missing")
	require.NoError(t, err)
	assert.Empty(t, none)
}
