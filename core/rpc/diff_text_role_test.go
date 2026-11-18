package rpc

import (
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session/git"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// snapshotPlus is a loop whose snapshot carries extra events too.
type snapshotPlus struct {
	*core.Loop
	extra []core.Event
}

func (b snapshotPlus) SyncAndSnapshot() (published, snapshot []core.Event) {
	p, s := b.Loop.SyncAndSnapshot()
	return p, append(s, b.extra...)
}

// rawConn says hello to srv as role over a pipe and returns the frames it
// is sent.
func rawConn(t *testing.T, srv *Server, role string) (*json.Decoder, net.Conn) {
	t.Helper()
	a, b := net.Pipe()
	t.Cleanup(func() { _ = b.Close() })
	srv.Serve(a)
	require.NoError(t, json.NewEncoder(b).Encode(Frame{Hello: &Hello{Protocol: Protocol, Role: role}}))
	return json.NewDecoder(b), b
}

// nextViews reads frames until a ViewsChanged for workspace ws.
func nextViews(t *testing.T, dec *json.Decoder, conn net.Conn, ws core.WorkspaceID) core.ViewsChanged {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	for {
		var f Frame
		require.NoError(t, dec.Decode(&f))
		if f.Event != "ViewsChanged" {
			continue
		}
		ev, err := decodeEvent(f)
		require.NoError(t, err)
		if v := ev.(core.ViewsChanged); v.WS == ws {
			return v
		}
	}
}

// A workspace's views carry its selected sessions' whole diffs, and every
// publish re-sends them to every connection. Only a TUI reads the text (its
// diff pane, Lua's content); the daemon's web layer and a CLI command (each
// loom flow run among them) get the counts without it, at a join and on
// every publish after.
func TestServe_OnlyATUIIsSentTheDiffText(t *testing.T) {
	const ws = core.WorkspaceID(77)
	views := core.ViewsChanged{WS: ws, Views: []core.InstanceView{
		{ID: 1, Title: "a", HasDiff: true, Diff: git.DiffStats{Added: 2, Removed: 1, Content: "+x\n-y\n"}},
		{ID: 2, Title: "b"},
	}}
	loop := core.StartForTest(core.NewForTest(core.Options{}))
	t.Cleanup(loop.Stop)
	srv := NewServer(snapshotPlus{Loop: loop, extra: []core.Event{views}})
	t.Cleanup(srv.Close)

	for _, tc := range []struct {
		role     string
		wantText string
	}{
		{role: "", wantText: "+x\n-y\n"},
		{role: RoleWeb, wantText: ""},
		{role: RoleCLI, wantText: ""},
	} {
		t.Run("role "+tc.role, func(t *testing.T) {
			dec, conn := rawConn(t, srv, tc.role)
			joined := nextViews(t, dec, conn, ws)

			srv.mu.Lock()
			srv.sendLocked([]core.Event{views})
			srv.mu.Unlock()
			published := nextViews(t, dec, conn, ws)

			for name, got := range map[string]core.ViewsChanged{"at the join": joined, "on a publish": published} {
				require.Len(t, got.Views, 2, name)
				v := got.Views[0]
				assert.Equal(t, tc.wantText, v.Diff.Content, name)
				assert.True(t, v.HasDiff, name)
				assert.Equal(t, 2, v.Diff.Added, "%s: the counts stay", name)
				assert.Equal(t, 1, v.Diff.Removed, name)
			}
		})
	}
	assert.Equal(t, "+x\n-y\n", views.Views[0].Diff.Content, "the model's own views are left alone")
}
