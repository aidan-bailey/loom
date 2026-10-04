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
	w.Add(a)
	w.Add(wt)
	w.Add(b)
	assert.Equal(t, []string{"ws", "a", "b"}, titles(w.Instances()))
}

func TestWorkspace_EditsByIdentity(t *testing.T) {
	w := NewWorkspace(WorkspaceParts{})
	a, b, c := newInst(t, "a"), newInst(t, "b"), newInst(t, "c")
	twin := newInst(t, "b") // same title, different instance
	w.Add(a)
	w.Add(b)
	w.Add(c)

	assert.False(t, w.Remove(twin), "a same-titled instance is not the held one")
	assert.True(t, w.Replace(b, twin))
	assert.Equal(t, []string{"a", "b", "c"}, titles(w.Instances()))
	assert.Same(t, twin, w.Instances()[1], "Replace keeps the row")
	assert.True(t, w.Holds(twin))
	assert.False(t, w.Holds(b))
	assert.True(t, w.Remove(a))
	assert.Same(t, twin, w.ByTitle("b"))
	assert.Nil(t, w.ByTitle("a"))
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
