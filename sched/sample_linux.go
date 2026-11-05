//go:build linux

package sched

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

// Sample reads every process from /proc (sccache measurement spec §5.2):
// its group, its session, its resident memory and its CPU time with its
// waited-for children's (parseProcStat). A process that exits between the
// listing and its read is skipped; a stat it cannot parse spoils the whole
// sample, which would understate a job's use. The scan stops when ctx is
// done; a scan cut short or that finds no process is an error, and the
// caller skips that sample.
func Sample(ctx context.Context) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, fmt.Errorf("reading /proc: %w", err)
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return Snapshot{}, fmt.Errorf("reading /proc: %w", err)
	}
	pageKB := int64(os.Getpagesize() / 1024)
	var procs []Proc
	for i, e := range entries {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return Snapshot{}, fmt.Errorf("reading /proc: %w", err)
			}
		}
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		raw, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue // it exited since the listing
		}
		p, err := parseProcStat(raw, pageKB)
		if err != nil {
			return Snapshot{}, err
		}
		procs = append(procs, p)
	}
	if len(procs) == 0 {
		return Snapshot{}, errors.New("/proc listed no processes")
	}
	return Snapshot{Taken: time.Now(), Procs: procs}, nil
}
