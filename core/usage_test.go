package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUsageProbe_NotDispatchedWithoutAnExtraAccount(t *testing.T) {
	m := NewForTest(Options{Program: "claude"})
	withAccounts(t, m)

	assert.False(t, m.maybeUsageProbe())
	assert.False(t, m.gate(gateUsage).inFlight, "no Job, nothing armed")
}

func TestUsageProbe_NotDispatchedWithoutAClaudeProgram(t *testing.T) {
	m := NewForTest(Options{Program: "aider"})
	withAccounts(t, m, "max-2")

	assert.False(t, m.maybeUsageProbe())
}

func TestUsageProbe_DispatchesOnceAndThrottles(t *testing.T) {
	m := NewForTest(Options{Program: "claude"})
	withAccounts(t, m, "max-2")

	require.True(t, m.maybeUsageProbe())
	assert.True(t, m.gate(gateUsage).inFlight)
	assert.False(t, m.maybeUsageProbe(), "one probe in flight at a time")
}

func TestRequestUsageProbe_BringsTheNextProbeForward(t *testing.T) {
	m := NewForTest(Options{Program: "claude"})
	withAccounts(t, m, "max-2")
	require.True(t, m.maybeUsageProbe())
	m.Deliver(gatedResult{kind: gateUsage, result: usageResult{}})
	assert.False(t, m.maybeUsageProbe(), "throttled by usageInterval")

	m.Drain()
	m.RequestUsageProbe()
	assert.NotEmpty(t, m.Drain().Jobs)
}
