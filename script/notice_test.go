package script

import (
	"errors"
	"testing"

	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNoticeAware: inst:kill() / inst:resume() used to raise on any error,
// so a kill or resume that succeeded but had something to report (a stash
// it forgot or could not drop) failed the script, and one reached from a
// dispatch-less context was never shown. A notice alone is a success that
// goes to the host's error/info bar; a failure still raises.
func TestNoticeAware(t *testing.T) {
	e := NewEngine(nil)
	defer e.Close()
	h := &fakeHost{}
	e.curHost = h
	L := e.L
	inst := &session.Instance{Title: "x"}

	call := func(op func(*session.Instance) error) error {
		L.Push(L.NewFunction(noticeAware(e, "kill", op)))
		L.Push(pushInstance(L, inst))
		return L.PCall(1, 0, nil)
	}

	require.NoError(t, call(func(*session.Instance) error {
		return session.NewNotice(errors.New("could not drop stash abc"))
	}))
	assert.Equal(t, []string{"kill x: could not drop stash abc"}, h.notices)

	err := call(func(*session.Instance) error { return errors.New("boom") })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kill: boom")

	require.NoError(t, call(func(*session.Instance) error { return nil }))
	assert.Len(t, h.notices, 1)
}
