package sched

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Proc is one process in a Snapshot.
type Proc struct {
	PID   int
	PGID  int
	RSSKB int64
	CPU   time.Duration // CPU time used so far, user and system together
}

// Snapshot is one reading of the process table.
type Snapshot struct {
	Taken time.Time
	Procs []Proc
}

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

// ParsePS reads the output of "ps -A -o pid=,pgid=,rss=,time=": one process
// per line, four whitespace-separated fields, rss in kilobytes. The time is
// [d-][[h:]m:]s[.f]: procps writes [d-]hh:mm:ss, BSD ps m:ss.hh. A line it
// cannot read fails the whole parse, since a sample with a hole in it would
// understate a job's use.
func ParsePS(out []byte) ([]Proc, error) {
	var procs []Proc
	for n, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 4 {
			return nil, fmt.Errorf("ps line %d has %d fields, want 4: %q", n+1, len(fields), line)
		}
		p, err := parsePSLine(fields)
		if err != nil {
			return nil, fmt.Errorf("ps line %d: %w", n+1, err)
		}
		procs = append(procs, p)
	}
	return procs, nil
}

func parsePSLine(fields []string) (Proc, error) {
	pid, err := strconv.Atoi(fields[0])
	if err != nil || pid < 0 {
		return Proc{}, fmt.Errorf("bad pid %q", fields[0])
	}
	pgid, err := strconv.Atoi(fields[1])
	if err != nil || pgid < 0 {
		return Proc{}, fmt.Errorf("bad pgid %q", fields[1])
	}
	rss, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil || rss < 0 {
		return Proc{}, fmt.Errorf("bad rss %q", fields[2])
	}
	cpu, err := parseCPUTime(fields[3])
	if err != nil {
		return Proc{}, err
	}
	return Proc{PID: pid, PGID: pgid, RSSKB: rss, CPU: cpu}, nil
}

// parseCPUTime reads ps's time column: [d-][[h:]m:]s[.f].
func parseCPUTime(s string) (time.Duration, error) {
	bad := fmt.Errorf("CPU time %q is not [d-][[h:]m:]s[.f]", s)
	rest := s
	var days int64
	if d, r, found := strings.Cut(rest, "-"); found {
		n, ok := psDigits(d)
		if !ok {
			return 0, bad
		}
		days, rest = n, r
	}
	parts := strings.Split(rest, ":")
	if len(parts) > 3 {
		return 0, bad
	}
	last, frac, hasFrac := strings.Cut(parts[len(parts)-1], ".")
	parts[len(parts)-1] = last
	var clock int64
	for _, p := range parts {
		n, ok := psDigits(p)
		if !ok {
			return 0, bad
		}
		clock = clock*60 + n
	}
	var nanos int64
	if hasFrac {
		if _, ok := psDigits(frac); !ok {
			return 0, bad
		}
		if len(frac) > 9 {
			frac = frac[:9]
		}
		nanos, _ = psDigits(frac + strings.Repeat("0", 9-len(frac)))
	}
	return time.Duration(days*86400+clock)*time.Second + time.Duration(nanos), nil
}

// psDigits reads a non-empty run of ASCII digits.
func psDigits(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil
}

// Group sums the memory and CPU time of the processes in process group
// pgid, and counts them. leaderAlive says whether any process has pid pgid,
// whatever group it is in: a process that holds the shim's pid without
// belonging to its group is a reused pid, not the shim.
func (s Snapshot) Group(pgid int) (rssKB int64, cpu time.Duration, members int, leaderAlive bool) {
	for _, p := range s.Procs {
		if p.PID == pgid {
			leaderAlive = true
		}
		if p.PGID == pgid {
			rssKB += p.RSSKB
			cpu += p.CPU
			members++
		}
	}
	return rssKB, cpu, members, leaderAlive
}
