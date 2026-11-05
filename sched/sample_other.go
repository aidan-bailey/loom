//go:build !linux

package sched

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

// Sample reads the process table with one "ps -A -o pid=,pgid=,rss=,time=",
// which procps (Linux) and BSD ps (macOS) both accept, though they write the
// time column differently (ParsePS reads both). ps runs for as long as ctx
// allows: the caller gives it 5 s. A ps that fails, runs out of time or lists
// nothing is an error, and the caller skips that sample.
func Sample(ctx context.Context) (Snapshot, error) {
	out, err := exec.CommandContext(ctx, "ps", "-A", "-o", "pid=,pgid=,rss=,time=").Output()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			err = ctxErr
		}
		return Snapshot{}, fmt.Errorf("ps: %w", err)
	}
	procs, err := ParsePS(out)
	if err != nil {
		return Snapshot{}, err
	}
	if len(procs) == 0 {
		return Snapshot{}, errors.New("ps listed no processes")
	}
	return Snapshot{Taken: time.Now(), Procs: procs}, nil
}
