package app

import (
	"errors"
	"testing"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/ui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestKillResult_ShowsNotice: a kill that could not drop the paused
// session's stash reports it; the row still goes.
func TestKillResult_ShowsNotice(t *testing.T) {
	m := fleetHome(t)
	m.errBox = ui.NewErrBox()
	m.errBox.SetSize(1000, 1)
	b1 := instByTitle(m, m.slots[1].list, "b1")
	require.NotNil(t, b1)
	require.NoError(t, b1.TransitionTo(session.Deleting))

	deliver(t, m, core.KillResult{Instance: b1, Title: "b1",
		Notice: session.NewNotice(errors.New("could not drop stash abc"))})

	assert.Nil(t, m.slots[1].list.GetInstanceByTitle("b1"))
	assert.Contains(t, m.errBox.String(), "could not drop stash abc")
}

// TestResumeDone_ShowsNotice: a resume that forgot an unlisted stash says
// so: the notice holds the stash's SHA, which nothing else records.
func TestResumeDone_ShowsNotice(t *testing.T) {
	m := fleetHome(t)
	m.errBox = ui.NewErrBox()
	m.errBox.SetSize(1000, 1)
	f1 := instByTitle(m, m.list, "f1")
	require.NotNil(t, f1)

	deliver(t, m, core.ResumeResult{Instance: f1, Owner: m.ws(),
		Notice: session.NewNotice(errors.New("forgot stash abc"))})

	assert.Contains(t, m.errBox.String(), "forgot stash abc")
}
