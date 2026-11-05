package sched

import "context"

// ScopeSpec is the scope a job's shim creates for itself (spec.json's
// scope; cgroup spec §5.1).
type ScopeSpec struct {
	Unit      string `json:"unit"`
	Slice     string `json:"slice"`
	CPUWeight uint64 `json:"cpu_weight"`
}

// SliceProps are what the daemon sets on its jobs' slice (cgroup spec §5.2).
type SliceProps struct {
	CPUWeight      uint64
	MemoryMaxBytes uint64 // 0: no limit
}

// Confiner is the user's service manager as the queue drives it (cgroup
// spec §5). Every call waits on D-Bus: the model makes them from a spawned
// job, never on its loop.
type Confiner interface {
	// JoinScope creates s as a transient scope holding pid, and returns
	// once systemd has started it.
	JoinScope(ctx context.Context, s ScopeSpec, pid int) error
	// EnsureSlice sets name's properties, creating it when nothing has.
	EnsureSlice(ctx context.Context, name string, p SliceProps) error
	// SetMemoryHigh sets unit's MemoryHigh; 0 is no limit.
	SetMemoryHigh(ctx context.Context, unit string, bytes uint64) error
	// Freeze and Thaw pause and resume every process of unit.
	Freeze(ctx context.Context, unit string) error
	Thaw(ctx context.Context, unit string) error
	// Close drops the connection.
	Close()
}
