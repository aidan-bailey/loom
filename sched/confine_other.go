//go:build !linux

package sched

import "context"

// NewConfiner answers ErrUnsupported: cgroups are Linux only.
func NewConfiner(context.Context) (Confiner, error) { return nil, ErrUnsupported }
