package cli

import (
	"bytes"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session/buildsweep"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runSweep runs `loom sweep args…` on a fresh command tree (newSweepCmd).
func runSweep(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newSweepCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestSweep_WithoutAThresholdFails(t *testing.T) {
	isolateWorkspaces(t)
	registered(t, "ws-a", pausedRecords)
	serveDaemon(t, "true")

	_, err := runSweep(t, "--dry-run")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "build_sweep_after_hours")
}

func TestSweep_DryRunReportsEverySession(t *testing.T) {
	isolateWorkspaces(t)
	wt := t.TempDir()
	registered(t, "ws-a", `[{"title":"alpha","status":3,"program":"true","worktree":{"worktree_path":"`+wt+`"}}]`)
	serveDaemon(t, "true")

	out, err := runSweep(t, "--dry-run", "--after", "48")

	require.NoError(t, err)
	assert.Contains(t, out, "Dry run, nothing deleted: binaries unused for 48h")
	assert.Regexp(t, `ws-a\s+alpha\s+Paused\s+skipped \(no_target\)`, out)
	assert.Contains(t, out, "Total: 0 files, 0B")
}

func TestSweep_RefusesANegativeThreshold(t *testing.T) {
	_, err := runSweep(t, "--after", "-1")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "--after")
}

func TestWriteSweepReport(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	rep := buildsweep.Report{AfterHours: 12, Results: []buildsweep.Result{
		{
			Target: buildsweep.Target{Workspace: "poly", Title: "wasm", Status: "Ready"},
			Files:  []buildsweep.File{{Path: "target/debug/deps/health-0123456789abcdef", Size: 3 << 30, LastUsed: now.Add(-30 * time.Hour)}},
			Freed:  3 << 30,
		},
		{Target: buildsweep.Target{Workspace: "poly", Title: "busy", Status: "Paused", Skip: buildsweep.SkipBuilding, Detail: "target/debug/.cargo-build-lock is locked: cargo is building"}},
	}}
	var out bytes.Buffer

	writeSweepReport(&out, rep, true, now)

	s := out.String()
	assert.Contains(t, s, "Deleted binaries unused for 12h")
	assert.Regexp(t, `poly\s+wasm\s+Ready\s+1 file, 3\.0G`, s)
	assert.Contains(t, s, "target/debug/deps/health-0123456789abcdef  3.0G  unused 30h")
	assert.Regexp(t, `poly\s+busy\s+Paused\s+skipped \(building\): target/debug/\.cargo-build-lock is locked`, s)
	assert.Contains(t, s, "Total: 1 file, 3.0G")
}
