package rpc

import (
	"testing"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUnsupported_ARequestNoReplyWillAnswer: a call carrying a request ID
// that the server answers CodeUnsupported (an older server without the
// method) is recorded, since its Reply never comes and its requester must
// fail; one carrying none, or failing otherwise, is not.
func TestUnsupported_ARequestNoReplyWillAnswer(t *testing.T) {
	answer := func(code string) func(Frame) Frame {
		return func(f Frame) Frame {
			return Frame{ID: f.ID, Error: &core.WireError{Code: code, Message: "rpc: unknown method " + f.Method}}
		}
	}

	t.Run("with a request ID", func(t *testing.T) {
		c, s := dialScripted(t)
		s.answerWith(answer(core.CodeUnsupported))
		c.SubmitJob(sched.Submit{}, 7)
		err := c.Unsupported()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown method SubmitJob")
		assert.Empty(t, c.TakeRefused(), "not refused as unavailable")
		c.SubmitJob(sched.Submit{}, 8)
		assert.Same(t, err, c.Unsupported(), "the first is kept")
	})
	t.Run("without one", func(t *testing.T) {
		c, s := dialScripted(t)
		s.answerWith(answer(core.CodeUnsupported))
		c.SubmitJob(sched.Submit{}, 0)
		assert.NoError(t, c.Unsupported())
	})
	t.Run("another error", func(t *testing.T) {
		c, s := dialScripted(t)
		s.answerWith(answer(core.CodeError))
		c.SubmitJob(sched.Submit{}, 7)
		assert.NoError(t, c.Unsupported())
	})
}
