package cli

import (
	"errors"
	"testing"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/core/rpc"
	"github.com/stretchr/testify/assert"
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
