package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJobShimCmd_IsHiddenTakesOneArgumentAndIsRegistered(t *testing.T) {
	assert.True(t, jobShimCmd.Hidden, "only the daemon runs it, so --help does not list it")

	require.Error(t, jobShimCmd.Args(jobShimCmd, nil), "no job dir")
	require.NoError(t, jobShimCmd.Args(jobShimCmd, []string{"/jobs/j1"}))
	require.Error(t, jobShimCmd.Args(jobShimCmd, []string{"/jobs/j1", "extra"}), "one job dir and no more")

	// Runner.Start runs "<loom> job-shim <job dir>": rootCmd must resolve
	// that to this command.
	found, rest, err := rootCmd.Find([]string{"job-shim", "/jobs/j1"})
	require.NoError(t, err)
	assert.Same(t, jobShimCmd, found, "registered on rootCmd under the name the runner starts")
	assert.Equal(t, []string{"/jobs/j1"}, rest)
}
