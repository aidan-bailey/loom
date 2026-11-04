package sched

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// DetectHost reads this host's name, cores and RAM. The model calls it once
// per daemon process and keeps the answer. A RAM it cannot read stays 0
// (Host.RAMBytes), never a guess: Resolve then asks for scheduler.memory_gb.
func DetectHost() Host {
	name, err := os.Hostname()
	if err != nil || name == "" {
		name = "localhost"
	}
	return Host{Name: name, Cores: runtime.NumCPU(), RAMBytes: readRAMBytes()}
}

// parseMemTotal reads MemTotal, in bytes, from the text of /proc/meminfo,
// where the kernel's "kB" is 1024 bytes.
func parseMemTotal(meminfo []byte) (uint64, error) {
	for _, line := range strings.Split(string(meminfo), "\n") {
		rest, ok := strings.CutPrefix(line, "MemTotal:")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) != 2 || fields[1] != "kB" {
			return 0, fmt.Errorf("unexpected MemTotal line %q", line)
		}
		kb, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("MemTotal %q: %w", fields[0], err)
		}
		return kb * 1024, nil
	}
	return 0, errors.New("no MemTotal line")
}

// parseMemsize reads the bytes "sysctl -n hw.memsize" prints.
func parseMemsize(out []byte) (uint64, error) {
	n, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("hw.memsize %q: %w", strings.TrimSpace(string(out)), err)
	}
	return n, nil
}
