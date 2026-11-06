package sched

import (
	"context"
	"errors"
	"testing"
	"time"

	sddbus "github.com/coreos/go-systemd/v22/dbus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDialConfiner_TheConnectionOutlivesTheDialContext pins that ending the
// context a confiner was made with leaves its connection's context alive,
// and that only the confiner's stop ends it: godbus closes a connection once
// its dial context is done, and the daemon's probes cancel theirs on return,
// so every confiner died with its probe.
func TestDialConfiner_TheConnectionOutlivesTheDialContext(t *testing.T) {
	var life context.Context
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	c, err := dialConfiner(ctx, func(dctx context.Context) (*sddbus.Conn, error) {
		life = dctx
		return nil, nil
	})
	require.NoError(t, err)
	cancel()
	assert.NoError(t, life.Err(), "the dial's context ended the connection's")
	c.stop()
	assert.ErrorIs(t, life.Err(), context.Canceled, "the confiner's stop leaves its connection's context running")
}

// TestDialConfiner_AFailedDialEndsTheConnectionsContext pins the other side:
// a dial that fails, or outlasts its context, ends the connection's context,
// so a connection made late is closed rather than left open.
func TestDialConfiner_AFailedDialEndsTheConnectionsContext(t *testing.T) {
	t.Run("the dial fails", func(t *testing.T) {
		var life context.Context
		_, err := dialConfiner(context.Background(), func(dctx context.Context) (*sddbus.Conn, error) {
			life = dctx
			return nil, errors.New("no bus here")
		})
		require.ErrorContains(t, err, "connect to the user's systemd: no bus here")
		assert.ErrorIs(t, life.Err(), context.Canceled)
	})
	t.Run("the dial outlasts its context", func(t *testing.T) {
		lives := make(chan context.Context, 1)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		_, err := dialConfiner(ctx, func(dctx context.Context) (*sddbus.Conn, error) {
			lives <- dctx
			<-dctx.Done()
			return nil, dctx.Err()
		})
		require.ErrorIs(t, err, context.DeadlineExceeded)
		select {
		case <-(<-lives).Done():
		case <-time.After(time.Second):
			t.Fatal("the connection's context outlived a dial that timed out")
		}
	})
}
