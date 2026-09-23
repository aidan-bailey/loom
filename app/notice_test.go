package app

import (
	"errors"
	"testing"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResumeResult: a resume that returns only a session.Notice succeeded
// with something to report; anything else that is an error failed.
func TestResumeResult(t *testing.T) {
	inst := &session.Instance{Title: "r"}
	owner := &workspaceSlot{}

	done, ok := resumeResult(inst, "r", owner, nil).(resumeDoneMsg)
	require.True(t, ok)
	assert.Nil(t, done.notice)
	assert.Same(t, owner, done.slot)

	n := session.NewNotice(errors.New("forgot stash abc"))
	done, ok = resumeResult(inst, "r", owner, n).(resumeDoneMsg)
	require.True(t, ok, "a notice alone is a successful resume")
	assert.Equal(t, n, done.notice)

	for _, err := range []error{
		errors.New("boom"),
		errors.Join(errors.New("boom"), session.NewNotice(errors.New("forgot stash abc"))),
	} {
		failed, ok := resumeResult(inst, "r", owner, err).(transitionFailedMsg)
		require.True(t, ok, "%v is a failure", err)
		assert.Equal(t, err, failed.err, "the failure carries its notices to the error bar")
		assert.Equal(t, session.Paused, failed.previousStatus)
	}
}

// TestKillInstanceMsg_ShowsNotice: a kill that could not drop the paused
// session's stash reports it; the row still goes.
func TestKillInstanceMsg_ShowsNotice(t *testing.T) {
	m := fleetHome(t)
	m.errBox = ui.NewErrBox()
	m.errBox.SetSize(1000, 1)
	b1 := m.slots[1].list.GetInstanceByTitle("b1")
	require.NotNil(t, b1)
	require.NoError(t, b1.TransitionTo(session.Deleting))

	_, _ = m.Update(killInstanceMsg{inst: b1, title: "b1",
		notice: session.NewNotice(errors.New("could not drop stash abc"))})

	assert.Nil(t, m.slots[1].list.GetInstanceByTitle("b1"))
	assert.Contains(t, m.errBox.String(), "could not drop stash abc")
}

// TestResumeDone_ShowsNotice: a resume that forgot an unlisted stash says
// so: the notice holds the stash's SHA, which nothing else records.
func TestResumeDone_ShowsNotice(t *testing.T) {
	m := fleetHome(t)
	m.errBox = ui.NewErrBox()
	m.errBox.SetSize(1000, 1)
	f1 := m.list.GetInstanceByTitle("f1")
	require.NotNil(t, f1)

	_, _ = m.Update(resumeDoneMsg{instance: f1, slot: m.workspaceSlot,
		notice: session.NewNotice(errors.New("forgot stash abc"))})

	assert.Contains(t, m.errBox.String(), "forgot stash abc")
}
