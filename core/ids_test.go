package core

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordInst builds a Paused instance as a saved record comes back: the
// title and creation time are what an ID derives from.
func recordInst(t *testing.T, title string, created time.Time) *session.Instance {
	t.Helper()
	inst, err := session.FromInstanceData(session.InstanceData{Title: title, Status: session.Paused, Program: "claude", CreatedAt: created}, t.TempDir())
	require.NoError(t, err)
	return inst
}

// constantHash makes every ID collide on want until the test ends.
func constantHash(t *testing.T, want uint64) {
	t.Helper()
	old := idHash
	idHash = func(...string) uint64 { return want }
	t.Cleanup(func() { idHash = old })
}

func TestStableID_IsWithin53BitsAndNeverZero(t *testing.T) {
	seen := make(map[uint64]bool)
	for i := range 20000 {
		id := stableID("instance", fmt.Sprint(i), "title")
		require.NotZero(t, id)
		require.LessOrEqual(t, id, uint64(idMask), "within a float64's mantissa")
		seen[id] = true
	}
	assert.Len(t, seen, 20000, "no collision among 20000 inputs")

	assert.Equal(t, stableID("a", "b"), stableID("a", "b"), "a function of its parts")
	assert.NotEqual(t, stableID("a", "b"), stableID("ab", ""), "parts are separated, not concatenated")
	assert.NotEqual(t, stableID("a", "b"), stableID("a", "b", ""), "an empty part still counts")
}

func TestProbe_TakesTheNextFreeValue(t *testing.T) {
	assert.Equal(t, uint64(7), probe(7, func(uint64) bool { return false }), "a free value is kept")
	taken := map[uint64]bool{7: true, 8: true, 9: true}
	assert.Equal(t, uint64(10), probe(7, func(v uint64) bool { return taken[v] }))

	wrapped := probe(idMask, func(v uint64) bool { return v == idMask })
	assert.Equal(t, uint64(1), wrapped, "wraps within idMask, skipping 0")
	assert.Equal(t, uint64(2), probe(idMask, func(v uint64) bool { return v == idMask || v == 1 }))
}

// Two daemons over one disk name the same workspaces and records by the
// same IDs, whatever else each serves and whatever order it asked in: that
// is what lets a client that rejoins another daemon keep naming things.
func TestIDs_TwoModelsOverOneDiskAgree(t *testing.T) {
	records := func(first string) string {
		return `[{"title":"` + first + `","status":3,"program":"claude","created_at":"2026-01-02T03:04:05.123456789Z"},` +
			`{"title":"y","status":3,"program":"claude","created_at":"2026-01-03T00:00:00Z"}]`
	}
	a := workspaceDef(t, "a", records("x"), "true")
	b := workspaceDef(t, "b", records("x"), "true") // same titles as a's, in another workspace
	extra := workspaceDef(t, "extra", records("z"), "true")

	first := bootModel(t, a, b)
	globalDir := os.Getenv(config.EnvGlobalDir)
	first.boot()
	second := bootModel(t, extra, a, b) // one more workspace, served before the shared ones
	t.Setenv(config.EnvGlobalDir, globalDir)
	second.boot()

	idsOf := func(m *Model) (ws map[string]WorkspaceID, insts map[string]InstanceID) {
		ws, insts = make(map[string]WorkspaceID), make(map[string]InstanceID)
		for _, w := range m.Loaded() { // serve order, so a counter would disagree
			dir := canonicalDir(w.ctx.ConfigDir)
			ws[dir] = m.wsIDOf(w)
			for _, inst := range w.instances() {
				insts[dir+"|"+inst.Title] = m.idOf(inst)
			}
		}
		return ws, insts
	}
	ws1, insts1 := idsOf(first)
	ws2, insts2 := idsOf(second)

	require.Contains(t, ws1, canonical(t, config.WorkspaceConfigDir(&a)))
	require.Contains(t, ws2, canonical(t, config.WorkspaceConfigDir(&extra)))
	require.NotContains(t, ws1, canonical(t, config.WorkspaceConfigDir(&extra)))
	for dir, id := range ws1 {
		assert.Equal(t, id, ws2[dir], "workspace %s", dir)
	}
	require.Len(t, insts1, 4, "x and y of a and of b")
	for key, id := range insts1 {
		assert.Equal(t, id, insts2[key], "instance %s", key)
	}

	distinct := make(map[InstanceID]bool)
	for _, id := range insts2 {
		distinct[id] = true
	}
	assert.Len(t, distinct, len(insts2), "every record has its own ID, same-titled ones in two workspaces included")
}

// The workspace is part of an instance's ID: two workspaces holding a
// record with one title and creation time still name them apart, and the
// same way on every daemon, whichever it asks about first.
func TestIDs_SameTitleAndCreatedAtInTwoWorkspacesDiffer(t *testing.T) {
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	wsA, wsB := storedWorkspace(t, "a"), storedWorkspace(t, "b")
	instA, instB := recordInst(t, "x", created), recordInst(t, "x", created)
	wsA.add(instA)
	wsB.add(instB)

	first, second := NewForTest(Options{}), NewForTest(Options{})
	first.SetWorkspacesForTest(wsA, wsB)
	second.SetWorkspacesForTest(wsB, wsA)

	a1, b1 := first.idOf(instA), first.idOf(instB)
	b2, a2 := second.idOf(instB), second.idOf(instA) // the other way round
	assert.NotEqual(t, a1, b1)
	assert.Equal(t, a1, a2)
	assert.Equal(t, b1, b2)
}

// A record killed and made again under its title is a new record, with a
// new creation time, so it gets a new ID: a request naming the old one is
// refused rather than landing on the new.
func TestIDs_ARecordRecreatedUnderAKilledTitleGetsANewID(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	old := recordInst(t, "x", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	ws.add(old)
	m.SetWorkspacesForTest(ws)
	oldID := m.idOf(old)
	assert.Equal(t, oldID, m.idOf(old), "stable while it lives")

	ws.remove(old)
	m.Sync() // forgotten
	_, ok := m.View(oldID)
	assert.False(t, ok)
	assert.NotContains(t, m.idHolders, oldID, "the publish pruned the holder too")

	again := recordInst(t, "x", time.Date(2026, 1, 2, 4, 0, 0, 0, time.UTC))
	ws.add(again)
	assert.NotEqual(t, oldID, m.idOf(again))
	_, ok = m.View(oldID)
	assert.False(t, ok, "the old ID reaches nothing")
}

// A record that comes back as the same record (same config dir, title and
// creation time) is the same ID, as it is on another daemon.
func TestIDs_ARecordReloadedFromDiskKeepsItsID(t *testing.T) {
	created := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	before := recordInst(t, "x", created)
	ws.add(before)
	m.SetWorkspacesForTest(ws)
	id := m.idOf(before)

	ws.remove(before)
	m.Sync()
	after := recordInst(t, "x", created)
	ws.add(after)
	assert.Equal(t, id, m.idOf(after))
}

// A workspace whose load failed is empty; once it loads on a retry (Open),
// its workspace and its records have the IDs a daemon whose load never
// failed gives them.
func TestIDs_ARetriedLoadKeepsItsInstancesIDs(t *testing.T) {
	broken := workspaceDef(t, "flaky", `{"not":"an array"}`, "true")
	m := bootModel(t, broken)
	globalDir := os.Getenv(config.EnvGlobalDir)
	m.boot()
	require.Error(t, served(m, broken).loadErr)
	wsID := m.wsIDOf(served(m, broken))
	assert.Empty(t, served(m, broken).instances())

	require.NoError(t, os.WriteFile(filepath.Join(config.WorkspaceConfigDir(&broken), config.StateFileName),
		[]byte(`{"instances":[{"title":"x","status":3,"program":"claude","created_at":"2026-01-02T03:04:05Z"}]}`), 0o644))
	_, err := m.Open(wsID)
	require.NoError(t, err)
	inst := served(m, broken).byTitle("x")
	require.NotNil(t, inst)

	fresh := bootModel(t, broken) // a daemon whose load never failed
	t.Setenv(config.EnvGlobalDir, globalDir)
	fresh.boot()
	require.NoError(t, served(fresh, broken).loadErr)
	assert.Equal(t, fresh.wsIDOf(served(fresh, broken)), m.wsIDOf(served(m, broken)), "the workspace keeps its ID")
	assert.Equal(t, wsID, m.wsIDOf(served(m, broken)))
	assert.Equal(t, fresh.idOf(served(fresh, broken).byTitle("x")), m.idOf(inst))
}

// A Recoverable placeholder is replaced by the instance adopting its
// worktree, rebuilt from the placeholder's own record (recoverInst), so the
// row keeps the ID the clients select it by.
func TestIDs_ARecoveredOrphanKeepsItsPlaceholdersID(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	placeholder := statusInst(t, "orphan", session.Recoverable)
	ws.add(placeholder)
	m.SetWorkspacesForTest(ws)
	pid := m.idOf(placeholder)

	data := placeholder.ToInstanceData() // what recoverInst hands ReconcileAndRestore
	data.Status = session.Paused
	adopted, err := session.FromInstanceData(data, t.TempDir())
	require.NoError(t, err)
	require.NotSame(t, placeholder, adopted)

	m.spawn(m.track(4, pid, func() any {
		return RecoverResult{Placeholder: placeholder, Owner: ws, OldTitle: "orphan", Recovered: adopted}
	}))
	out := run(m, m.Drain())

	rs := replies(out)
	require.Len(t, rs, 1)
	assert.Equal(t, pid, rs[0].ID, "the Reply names the placeholder's ID")
	var rec []Recovered
	for _, ev := range out.Events {
		if r, ok := ev.(Recovered); ok {
			rec = append(rec, r)
		}
	}
	require.Len(t, rec, 1)
	assert.Equal(t, pid, rec[0].ID)
	assert.Same(t, adopted, ws.instances()[0])
	assert.Equal(t, pid, m.idOf(adopted))
	v, ok := m.View(pid)
	require.True(t, ok, "the placeholder's ID now reaches the adopted instance")
	assert.Equal(t, "orphan", v.Title)
}

// Two live instances whose hashes collide get neighbouring IDs; once the
// first is gone its ID is free again, and the next instance to want it
// takes it.
func TestIDs_ACollisionProbesPastALiveHolder(t *testing.T) {
	const want = 1000
	constantHash(t, want)
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	a, b := pausedInst(t, "a"), pausedInst(t, "b")
	ws.add(a)
	ws.add(b)
	m.SetWorkspacesForTest(ws)

	assert.Equal(t, InstanceID(want), m.idOf(a))
	assert.Equal(t, InstanceID(want+1), m.idOf(b), "the live holder keeps its ID")
	assert.Equal(t, InstanceID(want), m.idOf(a), "stable")

	ws.remove(a) // no publish in between: the holder's entry is still there
	c := pausedInst(t, "c")
	ws.add(c)
	assert.Equal(t, InstanceID(want), m.idOf(c), "a removed holder gives its ID up")
	assert.Equal(t, InstanceID(want+1), m.idOf(b), "and the live one is untouched")
	assert.Same(t, c, m.InstanceForTest(want))
	assert.Same(t, b, m.InstanceForTest(want+1))

	m.Sync()
	assert.Equal(t, InstanceID(want), m.idOf(c), "a publish prunes only what is gone")
	assert.Equal(t, InstanceID(want+1), m.idOf(b))
	assert.Len(t, m.idHolders, 2)
	assert.Len(t, m.ids, 2)
}

func TestIDs_AWorkspaceCollisionProbesPastAServedHolder(t *testing.T) {
	const want = 2000
	constantHash(t, want)
	m := NewForTest(Options{})
	a, b := storedWorkspace(t, "a"), storedWorkspace(t, "b")
	m.SetWorkspacesForTest(a, b)

	assert.Equal(t, WorkspaceID(want), m.wsIDOf(a))
	assert.Equal(t, WorkspaceID(want+1), m.wsIDOf(b))

	c := storedWorkspace(t, "c")
	m.SetWorkspacesForTest(b, c) // a is no longer served; no publish yet
	assert.Equal(t, WorkspaceID(want), m.wsIDOf(c), "a workspace no longer served gives its ID up")
	assert.Equal(t, WorkspaceID(want+1), m.wsIDOf(b))

	m.Sync()
	assert.Equal(t, WorkspaceID(want), m.wsIDOf(c))
	assert.Len(t, m.wsHolders, 2)
	assert.Len(t, m.wsIDs, 2)
}

// An instance no served workspace holds has no place in the maps: it gets
// the ID its workspace-less hash gives, uncached, and holds nothing.
func TestIDs_AnInstanceNoWorkspaceHoldsCachesNothing(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	m.SetWorkspacesForTest(ws)
	stray := pausedInst(t, "stray")

	id := m.idOf(stray)
	assert.NotZero(t, id)
	assert.Empty(t, m.ids)
	assert.Empty(t, m.idHolders)
	_, ok := m.View(id)
	assert.False(t, ok, "nothing serves it")
}
