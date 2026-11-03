package buildsweep

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseMountInfo(t *testing.T) {
	data := `22 1 259:4 / / rw,noatime shared:1 - ext4 /dev/nvme1n1p2 rw,commit=60
84 22 259:1 / /tb rw,relatime shared:5 - ext4 /dev/nvme0n1p1 rw
44 22 0:28 / /run rw,nosuid,nodev shared:2 - tmpfs tmpfs rw,mode=755
91 22 0:50 / /mnt/My\040Disk rw,lazytime - ext4 /dev/sdb1 rw
`
	mounts, err := parseMountInfo(data)

	require.NoError(t, err)
	assert.Equal(t, []mount{
		{point: "/", atime: atimeOff},
		{point: "/tb", atime: atimeRelative},
		{point: "/run", atime: atimeStrict},
		{point: "/mnt/My Disk", atime: atimeStrict},
	}, mounts)

	_, err = parseMountInfo("22 1 259:4 /\n")
	assert.Error(t, err, "a short line")
}

func TestMountFor_TheDeepestMountWinsAndTheLaterOverTheSamePoint(t *testing.T) {
	mounts := []mount{
		{point: "/", atime: atimeOff},
		{point: "/tb", atime: atimeRelative},
		{point: "/tb", atime: atimeStrict}, // mounted over the one before
		{point: "/tbx", atime: atimeOff},
	}
	for path, want := range map[string]mount{
		"/home/me/wt":   {point: "/", atime: atimeOff},
		"/tb":           {point: "/tb", atime: atimeStrict},
		"/tb/Source/wt": {point: "/tb", atime: atimeStrict},
		"/tbx/wt":       {point: "/tbx", atime: atimeOff},
	} {
		got, ok := mountFor(mounts, path)
		require.True(t, ok, path)
		assert.Equal(t, want, got, path)
	}
	_, ok := mountFor(nil, "/tb")
	assert.False(t, ok)
}

func TestRecordsUse(t *testing.T) {
	for _, tc := range []struct {
		m     mount
		after time.Duration
		ok    bool
	}{
		{mount{point: "/", atime: atimeStrict}, time.Hour, true},
		{mount{point: "/", atime: atimeRelative}, 24 * time.Hour, true},
		{mount{point: "/", atime: atimeRelative}, 23 * time.Hour, false},
		{mount{point: "/", atime: atimeOff}, 1000 * time.Hour, false},
	} {
		ok, why := recordsUse(tc.m, tc.after)
		assert.Equal(t, tc.ok, ok, "%+v %v", tc.m, tc.after)
		assert.Equal(t, tc.ok, why == "", why)
	}
}
