// Package memguard watches the TUI's own memory and stops it before it can
// exhaust the host. Once the Go runtime holds half the limit it writes heap
// and goroutine profiles beside the TUI's log, so a runaway explains itself;
// at the limit it writes them again and hands the breach to its caller,
// which quits (Quit).
//
// A TUI holds no state the daemon lacks, so quitting one costs a restart.
// Letting one grow costs the host: on 2026-11-24 a TUI reached 24 GB of RSS
// and 6 GB of swap, and earlyoom killed about 48 agents and desktop
// services before the kernel killed the TUI.
package memguard

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/metrics"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aidan-bailey/loom/log"
)

// EnvVar names the environment variable that sets the limit: a size in
// bytes, with an optional K, M, G or T suffix (binary units, so 6G is 6 GiB;
// a trailing B or iB is allowed), or off, 0 or false to watch nothing.
const EnvVar = "LOOM_TUI_MEMORY_LIMIT"

// DefaultLimit is the limit when EnvVar is unset: profiles at 3 GiB, the
// quit at 6 GiB. A busy TUI holds well under 1 GiB; fresco's baseline of
// about 17 GiB on 24 GiB of RAM leaves a TUI of 8 GiB enough to fill the RAM
// and start earlyoom on the agents first.
const DefaultLimit uint64 = 6 << 30

// interval is how often the guard reads the runtime's memory. Reading
// runtime/metrics stops no goroutine.
const interval = 2 * time.Second

// ExitGrace is how long Quit lets the program's own shutdown run before it
// exits the process itself.
const ExitGrace = 5 * time.Second

// Reading is one sample of the process's memory, from the Go runtime's own
// accounting: it counts pages swapped out and goroutine stacks, which the
// process's RSS misses or hides.
type Reading struct {
	// Bytes is all the memory the runtime has mapped, less what it has
	// released to the OS: what the process holds, resident or swapped.
	Bytes uint64
	// HeapObjects is the bytes in heap objects, live or not yet swept.
	HeapObjects uint64
	// Stacks is the bytes in goroutine stacks.
	Stacks uint64
	// Goroutines is the number of live goroutines.
	Goroutines uint64
}

// Breach is the reading that reached the limit, and the profiles written
// for it.
type Breach struct {
	Reading
	Limit    uint64
	Profiles []string
}

// Error is the line the TUI prints as it quits.
func (b Breach) Error() string {
	msg := fmt.Sprintf("loom: quit at %s of memory, past its %s limit (%s), so it would not exhaust the host",
		Size(b.Bytes), Size(b.Limit), EnvVar)
	if len(b.Profiles) > 0 {
		msg += "; profiles: " + strings.Join(b.Profiles, ", ")
	}
	return msg
}

// ParseLimit reads an EnvVar value: the limit in bytes and whether to
// watch at all. An empty value is the default limit.
func ParseLimit(spec string) (uint64, bool, error) {
	s := strings.TrimSpace(spec)
	switch strings.ToLower(s) {
	case "":
		return DefaultLimit, true, nil
	case "off", "0", "false":
		return 0, false, nil
	}
	num := strings.ToUpper(s)
	num = strings.TrimSuffix(num, "IB")
	num = strings.TrimSuffix(num, "B")
	shift := 0
	if n := len(num); n > 0 {
		switch num[n-1] {
		case 'K':
			shift = 10
		case 'M':
			shift = 20
		case 'G':
			shift = 30
		case 'T':
			shift = 40
		}
		if shift > 0 {
			num = num[:n-1]
		}
	}
	v, err := strconv.ParseUint(strings.TrimSpace(num), 10, 64)
	if err != nil || v == 0 {
		return 0, false, fmt.Errorf("%s=%q: want a size such as 6G or 512M, or off", EnvVar, spec)
	}
	if shift > 0 && v > ^uint64(0)>>shift {
		return 0, false, fmt.Errorf("%s=%q: too large", EnvVar, spec)
	}
	return v << shift, true, nil
}

// Size formats n bytes for a person, in binary units.
func Size(n uint64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MiB", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// metricNames are the runtime/metrics a Reading is built from, in order.
var metricNames = []string{
	"/memory/classes/total:bytes",
	"/memory/classes/heap/released:bytes",
	"/memory/classes/heap/objects:bytes",
	"/memory/classes/heap/stacks:bytes",
	"/sched/goroutines:goroutines",
}

// Read samples the process's memory now.
func Read() Reading {
	s := make([]metrics.Sample, len(metricNames))
	for i, name := range metricNames {
		s[i].Name = name
	}
	metrics.Read(s)
	v := func(i int) uint64 {
		if s[i].Value.Kind() != metrics.KindUint64 {
			return 0
		}
		return s[i].Value.Uint64()
	}
	r := Reading{HeapObjects: v(2), Stacks: v(3), Goroutines: v(4)}
	if total, released := v(0), v(1); total > released {
		r.Bytes = total - released
	}
	return r
}

// Guard samples memory and acts at half the limit and at the limit, once
// each.
type Guard struct {
	limit   uint64
	dir     string
	read    func() Reading
	write   func(dir, stage string, at time.Time) ([]string, error)
	onLimit func(Breach)
	now     func() time.Time

	profiled bool
	breached bool

	stopOnce sync.Once
	stop     chan struct{}
	done     chan struct{}
}

// New builds a guard over limit that writes its profiles into dir and calls
// onLimit, from its own goroutine, when a reading reaches the limit. It
// does not run until Start.
func New(limit uint64, dir string, onLimit func(Breach)) *Guard {
	return &Guard{
		limit:   limit,
		dir:     dir,
		read:    Read,
		write:   WriteProfiles,
		onLimit: onLimit,
		now:     time.Now,
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
}

// FromEnv starts a guard for the TUI per EnvVar, writing profiles beside the
// TUI's log, and logs its limit. A value it cannot read keeps the default
// limit, since the guard is what protects the host. It returns nil when
// EnvVar turns the guard off.
func FromEnv(onLimit func(Breach)) *Guard {
	spec := os.Getenv(EnvVar)
	limit, on, err := ParseLimit(spec)
	if err != nil {
		log.For("memguard").Warn("memguard.bad_limit", "spec", spec, "err", err.Error(), "limit", Size(DefaultLimit))
		limit, on = DefaultLimit, true
	}
	if !on {
		log.For("memguard").Info("memguard.off", "spec", spec)
		return nil
	}
	g := New(limit, filepath.Dir(log.LogFilePath()), onLimit)
	g.Start()
	log.For("memguard").Info("memguard.watching", "limit", Size(limit), "profiles_at", Size(limit/2), "dir", g.dir)
	return g
}

// Start samples every interval on its own goroutine until Stop, or until a
// reading reaches the limit.
func (g *Guard) Start() {
	go func() {
		defer close(g.done)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			if g.check() {
				return
			}
			select {
			case <-g.stop:
				return
			case <-t.C:
			}
		}
	}()
}

// Stop ends the sampling and waits for it. A nil guard, or one never
// started, stops nothing.
func (g *Guard) Stop() {
	if g == nil {
		return
	}
	g.stopOnce.Do(func() { close(g.stop) })
	select {
	case <-g.done:
	case <-time.After(time.Second):
		// A check still writing profiles; the process is ending anyway.
	}
}

// check takes one reading and acts on it; it reports true once the limit
// was reached, after which there is nothing more to watch.
func (g *Guard) check() bool {
	if g.breached {
		return true
	}
	r := g.read()
	at := g.now()
	logger := log.For("memguard")
	if r.Bytes >= g.limit {
		g.breached, g.profiled = true, true
		paths, err := g.write(g.dir, "limit", at)
		logger.Error("memguard.limit", readingAttrs(r, g.limit, paths, err)...)
		g.onLimit(Breach{Reading: r, Limit: g.limit, Profiles: paths})
		return true
	}
	if !g.profiled && r.Bytes >= g.limit/2 {
		g.profiled = true
		paths, err := g.write(g.dir, "half", at)
		logger.Warn("memguard.profiled", readingAttrs(r, g.limit, paths, err)...)
	}
	return false
}

// readingAttrs are a log record's attributes for a reading.
func readingAttrs(r Reading, limit uint64, paths []string, err error) []any {
	attrs := []any{
		"bytes", r.Bytes, "size", Size(r.Bytes), "limit", Size(limit),
		"heap_objects", Size(r.HeapObjects), "stacks", Size(r.Stacks), "goroutines", r.Goroutines,
		"profiles", strings.Join(paths, ","),
	}
	if err != nil {
		attrs = append(attrs, "err", err.Error())
	}
	return attrs
}

// WriteProfiles writes the process's heap profile (gzipped protobuf, for
// go tool pprof) and goroutine profile (text, one entry per distinct stack)
// into dir, named for the time, the process and the stage, and returns the
// paths it wrote. It writes what it can: an error names what failed.
func WriteProfiles(dir, stage string, at time.Time) ([]string, error) {
	base := filepath.Join(dir, fmt.Sprintf("tui-memory-%s-%d-%s", at.Format("20060102-150405"), os.Getpid(), stage))
	var paths []string
	var errs []string
	for _, p := range []struct {
		name, suffix string
		debug        int
	}{
		{"heap", "-heap.pb.gz", 0},
		{"goroutine", "-goroutines.txt", 1},
	} {
		path := base + p.suffix
		if err := writeProfile(path, p.name, p.debug); err != nil {
			errs = append(errs, err.Error())
			continue
		}
		paths = append(paths, path)
	}
	if len(errs) > 0 {
		return paths, fmt.Errorf("memguard: %s", strings.Join(errs, "; "))
	}
	return paths, nil
}

// writeProfile writes the named runtime/pprof profile to path.
func writeProfile(path, name string, debug int) error {
	prof := pprof.Lookup(name)
	if prof == nil {
		return fmt.Errorf("no %s profile", name)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("%s profile: %w", name, err)
	}
	werr := prof.WriteTo(f, debug)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return fmt.Errorf("%s profile: %w", name, werr)
	}
	return nil
}

// Quit ends the process over a breach. It arms the exit before it asks the
// program to stop, since neither can be trusted to return: Update may be
// stalled (a terminal that stopped reading holds the renderer's write, and
// every Update waits on the renderer), and kill's restore of the terminal
// writes to that same terminal. So kill runs, and grace later the process
// exits with status 1 whether or not it did, after telling stderr why
// (a write it does not wait on for more than a second).
func Quit(b Breach, kill func(), grace time.Duration, stderr io.Writer, exit func(int)) {
	time.AfterFunc(grace, func() {
		said := make(chan struct{})
		go func() {
			defer close(said)
			_, _ = fmt.Fprintln(stderr, b.Error())
		}()
		select {
		case <-said:
		case <-time.After(time.Second):
		}
		exit(1)
	})
	kill()
}
