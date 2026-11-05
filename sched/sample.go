package sched

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Proc is one process in a Snapshot.
type Proc struct {
	PID   int
	PGID  int
	SID   int // its session; its process group where the sample can't tell (ps)
	RSSKB int64
	CPU   time.Duration // CPU time so far, user and system together; on Linux its waited-for children's too
}

// Snapshot is one reading of the process table.
type Snapshot struct {
	Taken time.Time
	Procs []Proc
}

// ParsePS reads the output of "ps -A -o pid=,pgid=,rss=,time=": one process
// per line, four whitespace-separated fields, rss in kilobytes. The time is
// [d-][[h:]m:]s[.f]: procps writes [d-]hh:mm:ss, BSD ps m:ss.hh. SID is the
// process group: ps can't say the session on every platform. A line it
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
	return Proc{PID: pid, PGID: pgid, SID: pgid, RSSKB: rss, CPU: cpu}, nil
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

// userHZ is the unit of the CPU times in /proc/<pid>/stat: clock ticks, 100
// a second on every Linux (USER_HZ).
const userHZ = 100

// parseProcStat reads one /proc/<pid>/stat (sccache measurement spec §5.2):
// "pid (comm) state ppid pgrp session … utime stime cutime cstime … rss …",
// the times in ticks of 1/userHZ s and rss in pages of pageKB KB. comm is
// everything from the first "(" to the last ")", since a name may hold
// spaces and parentheses. CPU is utime + stime + cutime + cstime: the
// process's own time and its waited-for children's, so a session's sum
// keeps the processes reaped inside it.
func parseProcStat(raw []byte, pageKB int64) (Proc, error) {
	s := string(raw)
	open, end := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if open < 1 || end < open {
		return Proc{}, fmt.Errorf("stat %q has no (comm)", s)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(s[:open]))
	if err != nil {
		return Proc{}, fmt.Errorf("stat %q: pid: %w", s, err)
	}
	f := strings.Fields(s[end+1:]) // f[0] is field 3, the state: field n is f[n-3]
	if len(f) < 22 {
		return Proc{}, fmt.Errorf("stat %q has %d fields after comm, want 22 or more", s, len(f))
	}
	var n [7]int64 // pgrp, session, utime, stime, cutime, cstime, rss
	for i, idx := range []int{2, 3, 11, 12, 13, 14, 21} {
		if n[i], err = strconv.ParseInt(f[idx], 10, 64); err != nil {
			return Proc{}, fmt.Errorf("stat %q: field %d: %w", s, idx+3, err)
		}
	}
	ticks := n[2] + n[3] + n[4] + n[5]
	return Proc{
		PID: pid, PGID: int(n[0]), SID: int(n[1]), RSSKB: n[6] * pageKB,
		CPU: time.Duration(ticks) * time.Second / userHZ,
	}, nil
}

// Session sums the memory and CPU time of the processes in session sid, and
// counts them (sccache measurement spec §5.2): a job's usage, since the shim
// leads its session and a descendant leaves it only by calling setsid. Where
// the sample can't tell sessions (ps), a process's session is its group.
func (s Snapshot) Session(sid int) (rssKB int64, cpu time.Duration, members int) {
	for _, p := range s.Procs {
		if p.SID == sid {
			rssKB += p.RSSKB
			cpu += p.CPU
			members++
		}
	}
	return rssKB, cpu, members
}
