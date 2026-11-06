package cli

import (
	"encoding/json"
	"errors"
	"net"
	"testing"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/core/rpc"
	"github.com/aidan-bailey/loom/session/claudemod"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUnknownToDaemon: a method an older daemon does not know fails with
// its build named and how it is upgraded; any other error, or the same
// error from a daemon that is not older, is left as it is.
func TestUnknownToDaemon(t *testing.T) {
	orig := OlderDaemon
	t.Cleanup(func() { OlderDaemon = orig })
	unknown := &core.WireError{Code: core.CodeUnsupported, Message: `rpc: unknown method "Core.NewThing"`}
	other := &core.WireError{Code: core.CodeError, Message: "boom"}
	older := func(*rpc.Client) string {
		return "the loom daemon is 0.14.0 [commit A], older than this loom (0.14.0 [commit B])"
	}
	notOlder := func(*rpc.Client) string { return "" }

	OlderDaemon = older
	err := unknownToDaemon(nil, unknown)
	assert.Equal(t, `the loom daemon is 0.14.0 [commit A], older than this loom (0.14.0 [commit B]), which does not know this command `+
		`(rpc: unknown method "Core.NewThing"); it is upgraded when it is restarted from outside loom`, err.Error())
	var w *core.WireError
	assert.True(t, errors.As(err, &w), "still the daemon's error underneath")

	assert.Same(t, other, unknownToDaemon(nil, other), "another error")
	assert.NoError(t, unknownToDaemon(nil, nil))

	OlderDaemon = notOlder
	assert.Same(t, unknown, unknownToDaemon(nil, unknown), "a daemon that is not older")

	OlderDaemon = nil
	assert.Same(t, unknown, unknownToDaemon(nil, unknown), "no describer set")
}

// dialUnknowing connects to a daemon of this protocol that knows no method
// but the barrier (rpc.Ping): it answers every other request as an older
// daemon answers a method it lacks (CodeUnsupported).
func dialUnknowing(t *testing.T) (*rpc.Client, error) {
	t.Helper()
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close() })
	go func() {
		dec, enc := json.NewDecoder(a), json.NewEncoder(a)
		var hello rpc.Frame
		if dec.Decode(&hello) != nil {
			return
		}
		older := rpc.Self()
		older.Version = "0.0.1"
		_ = enc.Encode(rpc.Frame{Hello: &older})
		for {
			var f rpc.Frame
			if dec.Decode(&f) != nil {
				return
			}
			if f.ID == 0 {
				continue
			}
			reply := rpc.Frame{ID: f.ID, Result: json.RawMessage("{}")}
			if f.Method != "rpc.Ping" {
				reply = rpc.Frame{ID: f.ID, Error: &core.WireError{Code: core.CodeUnsupported, Message: "rpc: unknown method " + f.Method}}
			}
			_ = enc.Encode(reply)
		}
	}()
	return rpc.Dial(b)
}

// A real command against an older daemon that lacks its method fails
// through withDaemon with the daemon's build named, and `run` still reports
// it as nothing submitted (125).
func TestJobRun_AMethodAnOlderDaemonLacksSaysTheDaemonIsOlder(t *testing.T) {
	t.Setenv(claudemod.EnvLaunch, "")
	origJoin, origOlder := Join, OlderDaemon
	t.Cleanup(func() { Join, OlderDaemon = origJoin, origOlder })
	Join = func() (*rpc.Client, error) { return dialUnknowing(t) }
	OlderDaemon = func(c *rpc.Client) string {
		require.Equal(t, "0.0.1", c.Peer().Version, "described from the joined client's peer")
		return "the loom daemon is 0.0.1, older than this loom (9.9.9)"
	}

	_, _, err := runJob(t, "run", "-d", "x", "--", "true")
	exit := exitOf(t, err)
	assert.Equal(t, 125, exit.Code)
	assert.Contains(t, exit.Msg, "loom: nothing was submitted: the loom daemon is 0.0.1, older than this loom (9.9.9), "+
		"which does not know this command (rpc: unknown method ")
	assert.Contains(t, exit.Msg, "it is upgraded when it is restarted from outside loom")
}
