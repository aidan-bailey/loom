package sched

import (
	"context"
	"os/exec"
	"time"
)

// sysctlTimeout bounds the one subprocess DetectHost runs.
const sysctlTimeout = 5 * time.Second

// readRAMBytes asks "sysctl -n hw.memsize"; 0 when it fails, runs out of
// time or prints something else.
func readRAMBytes() uint64 {
	ctx, cancel := context.WithTimeout(context.Background(), sysctlTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "sysctl", "-n", "hw.memsize").Output()
	if err != nil {
		return 0
	}
	n, err := parseMemsize(out)
	if err != nil {
		return 0
	}
	return n
}
