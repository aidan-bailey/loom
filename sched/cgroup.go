package sched

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// CgroupRoot is where the cgroup v2 hierarchy is mounted.
const CgroupRoot = "/sys/fs/cgroup"

// CgroupSample is one reading of a cgroup (cgroup spec §5.3).
type CgroupSample struct {
	WorkingKB   int64         // memory.current less memory.stat's inactive_file
	HighKB      int64         // memory.high; 0 for none ("max", or no file)
	CPU         time.Duration // cpu.stat's usage_usec: every process it ever held
	MemPressure float64       // memory.pressure's "some" avg10, a percentage
	CPUPressure float64       // cpu.pressure's "some" avg10
	OOMKills    int64         // memory.events' oom_kill, its subtree's included
	Frozen      bool          // cgroup.events' frozen
	Populated   bool          // cgroup.events' populated
}

// checkCgroupPath refuses a path that is not clean, absolute and below the
// root, as /proc/<pid>/cgroup names one.
func checkCgroupPath(path string) error {
	if path == "/" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("cgroup path %q is not a clean absolute path below the root", path)
	}
	return nil
}

// ReadCgroup reads cgroup path under root. memory.current, memory.stat,
// cpu.stat and cgroup.events must be there; memory.high, the pressure files
// and memory.events may be missing, and then read as 0.
func ReadCgroup(root, path string) (CgroupSample, error) {
	if err := checkCgroupPath(path); err != nil {
		return CgroupSample{}, err
	}
	dir := filepath.Join(root, path)
	read := func(name string) ([]byte, error) { return os.ReadFile(filepath.Join(dir, name)) }
	var s CgroupSample
	cur, err := read("memory.current")
	if err != nil {
		return CgroupSample{}, fmt.Errorf("read memory.current: %w", err)
	}
	current, err := strconv.ParseInt(strings.TrimSpace(string(cur)), 10, 64)
	if err != nil {
		return CgroupSample{}, fmt.Errorf("parse memory.current: %w", err)
	}
	stat, err := read("memory.stat")
	if err != nil {
		return CgroupSample{}, fmt.Errorf("read memory.stat: %w", err)
	}
	inactive, _ := keyedValue(stat, "inactive_file")
	s.WorkingKB = max(0, current-inactive) / 1024
	if data, err := read("memory.high"); err == nil {
		if v := strings.TrimSpace(string(data)); v != "max" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				s.HighKB = n / 1024
			}
		}
	}
	cpu, err := read("cpu.stat")
	if err != nil {
		return CgroupSample{}, fmt.Errorf("read cpu.stat: %w", err)
	}
	usec, ok := keyedValue(cpu, "usage_usec")
	if !ok {
		return CgroupSample{}, errors.New("cpu.stat has no usage_usec")
	}
	s.CPU = time.Duration(usec) * time.Microsecond
	if data, err := read("memory.pressure"); err == nil {
		s.MemPressure = someAvg10(data)
	}
	if data, err := read("cpu.pressure"); err == nil {
		s.CPUPressure = someAvg10(data)
	}
	if data, err := read("memory.events"); err == nil {
		s.OOMKills, _ = keyedValue(data, "oom_kill")
	}
	events, err := read("cgroup.events")
	if err != nil {
		return CgroupSample{}, fmt.Errorf("read cgroup.events: %w", err)
	}
	populated, _ := keyedValue(events, "populated")
	frozen, _ := keyedValue(events, "frozen")
	s.Populated, s.Frozen = populated == 1, frozen == 1
	return s, nil
}

// keyedValue is the integer after key in a flat-keyed file of "key value"
// lines, and false when the key is absent or its value is not one.
func keyedValue(data []byte, key string) (int64, bool) {
	for line := range bytes.SplitSeq(data, []byte{'\n'}) {
		k, v, ok := bytes.Cut(line, []byte{' '})
		if ok && string(k) == key {
			n, err := strconv.ParseInt(strings.TrimSpace(string(v)), 10, 64)
			return n, err == nil
		}
	}
	return 0, false
}

// someAvg10 is the avg10 of a pressure file's "some" line, 0 for none.
func someAvg10(data []byte) float64 {
	for line := range bytes.SplitSeq(data, []byte{'\n'}) {
		fields := strings.Fields(string(line))
		if len(fields) < 2 || fields[0] != "some" {
			continue
		}
		for _, f := range fields[1:] {
			if v, ok := strings.CutPrefix(f, "avg10="); ok {
				if x, err := strconv.ParseFloat(v, 64); err == nil {
					return x
				}
			}
		}
	}
	return 0
}

// CgroupKill writes 1 to path's cgroup.kill under root: SIGKILL to every
// process in it, whatever group or session it moved to. It refuses anything
// but a loom job's scope (a base name loom-…-job-….scope), since a kill
// reaches every process below the cgroup named.
func CgroupKill(root, path string) error {
	if err := checkCgroupPath(path); err != nil {
		return err
	}
	base := filepath.Base(path)
	if !strings.HasPrefix(base, "loom-") || !strings.Contains(base, "-job-") || !strings.HasSuffix(base, ".scope") {
		return fmt.Errorf("refusing to kill cgroup %s: not a loom job's scope", path)
	}
	f, err := os.OpenFile(filepath.Join(root, path, "cgroup.kill"), os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("open cgroup.kill: %w", err)
	}
	_, werr := f.Write([]byte("1"))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return fmt.Errorf("write cgroup.kill: %w", werr)
	}
	return nil
}

// SelfCgroup is this process's cgroup v2 path.
func SelfCgroup() (string, error) {
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return "", fmt.Errorf("read /proc/self/cgroup: %w", err)
	}
	return parseSelfCgroup(data)
}

// parseSelfCgroup is the path of /proc/<pid>/cgroup's "0::" line.
func parseSelfCgroup(data []byte) (string, error) {
	for line := range strings.SplitSeq(string(data), "\n") {
		if p, ok := strings.CutPrefix(line, "0::"); ok && checkCgroupPath(p) == nil {
			return p, nil
		}
	}
	return "", errors.New("no cgroup v2 entry in /proc/self/cgroup")
}

// parseMemAvailable is /proc/meminfo's MemAvailable, in KB.
func parseMemAvailable(data []byte) (int64, error) {
	for line := range strings.SplitSeq(string(data), "\n") {
		if v, ok := strings.CutPrefix(line, "MemAvailable:"); ok {
			if fields := strings.Fields(v); len(fields) >= 1 {
				return strconv.ParseInt(fields[0], 10, 64)
			}
		}
	}
	return 0, errors.New("no MemAvailable in /proc/meminfo")
}
