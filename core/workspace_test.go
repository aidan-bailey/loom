package core

import (
	"testing"

	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
)

func TestWorkspace_TerminalFirstThenAddOrder(t *testing.T) {
	w := NewWorkspace(WorkspaceParts{})
	a, b := newInst(t, "a"), newInst(t, "b")
	wt := newTerminal(t, "ws")
	w.add(a)
	w.add(wt)
	w.add(b)
	assert.Equal(t, []string{"ws", "a", "b"}, titles(w.instances()))
}

func TestWorkspace_EditsByIdentity(t *testing.T) {
	w := NewWorkspace(WorkspaceParts{})
	a, b, c := newInst(t, "a"), newInst(t, "b"), newInst(t, "c")
	twin := newInst(t, "b") // same title, different instance
	w.add(a)
	w.add(b)
	w.add(c)

	assert.False(t, w.remove(twin), "a same-titled instance is not the held one")
	assert.True(t, w.replace(b, twin))
	assert.Equal(t, []string{"a", "b", "c"}, titles(w.instances()))
	assert.Same(t, twin, w.instances()[1], "Replace keeps the row")
	assert.True(t, w.holds(twin))
	assert.False(t, w.holds(b))
	assert.True(t, w.remove(a))
	assert.Same(t, twin, w.byTitle("b"))
	assert.Nil(t, w.byTitle("a"))
}

func TestWorkspace_Label(t *testing.T) {
	assert.Equal(t, "global", NewWorkspace(WorkspaceParts{}).Label())
}

func titles(insts []*session.Instance) []string {
	out := make([]string, len(insts))
	for i, inst := range insts {
		out[i] = inst.Title
	}
	return out
}

func TestWorkspace_NameAndLabelAreNilSafe(t *testing.T) {
	var w *Workspace
	assert.Equal(t, "", w.Name())
	assert.Equal(t, "global", w.Label())
}

// TestModel_LookupWithNothingLoaded: with no workspace loaded, no ID
// resolves (lookup replaced InstanceForSession: the TUI resolves session
// names to IDs in its own view store).
func TestModel_LookupWithNothingLoaded(t *testing.T) {
	inst, _ := NewForTest(Options{}).lookup(1)
	assert.Nil(t, inst)
}
