//go:build !windows

package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebOpen_OpenerCommandStartsInItsOwnSession(t *testing.T) {
	cmd := openerCommand("http://loom.localhost:7420/?ui=next")

	require.NotNil(t, cmd.SysProcAttr, "the opener must not share the CLI's process group")
	assert.True(t, cmd.SysProcAttr.Setsid, "a Ctrl-C during the wait must not reach the opener or its browser")
}
