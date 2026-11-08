package sched

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadHostCPU_ReadsThisHost(t *testing.T) {
	got, err := ReadHostCPU()
	require.NoError(t, err)
	assert.Positive(t, got.CPUs)
	assert.Positive(t, got.Total)
	assert.LessOrEqual(t, got.Idle, got.Total)
}
