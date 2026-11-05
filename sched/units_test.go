package sched

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUnitNames(t *testing.T) {
	a, b := "/home/u/.loom/jobs", "/tmp/sandbox/jobs"
	assert.Regexp(t, `^loom_[0-9a-f]{8}\.slice$`, SliceName(a), "no dash: a dash would nest it in loom.slice")
	assert.NotEqual(t, SliceName(a), SliceName(b), "two looms never share a slice")
	assert.Equal(t, SliceName(a), SliceName(a+"/"), "the path is cleaned")
	assert.Regexp(t, `^loom-[0-9a-f]{8}-job-7-1\.scope$`, ScopeName(a, 7, 0))
	assert.Regexp(t, `-job-7-3\.scope$`, ScopeName(a, 7, 2), "the attempt counts from 1")
	tag := strings.TrimSuffix(strings.TrimPrefix(SliceName(a), "loom_"), ".slice")
	assert.Equal(t, "loom-"+tag, strings.Split(ScopeName(a, 7, 0), "-job-")[0])
}

func TestCPUWeight(t *testing.T) {
	assert.Equal(t, uint64(1), CPUWeight(0))
	assert.Equal(t, uint64(400), CPUWeight(4))
	assert.Equal(t, uint64(10000), CPUWeight(200))
}
