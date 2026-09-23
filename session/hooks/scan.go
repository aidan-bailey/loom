package hooks

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/log"
)

// Scan limits.
const (
	maxNewPerScan = 500
	maxEventBytes = 1 << 20
	staleTmpAge   = time.Minute
)

// ErrNoHooks means the folder or its launch-id is missing: the session was
// launched without tracking.
var ErrNoHooks = errors.New("hooks: no hooks folder")

// Request describes one instance's scan.
type Request struct {
	// Dir is the instance's hooks folder.
	Dir string
	// Cold replays the retained .ev log. True for the first scan after a
	// launch or a loom restart.
	Cold bool
}

// Result is what one scan found.
type Result struct {
	LaunchID string
	Events   []Event
	// Replayed is true when Events is the full history (Request.Cold).
	Replayed bool
}

type eventFile struct {
	path string
	stem string
	mod  time.Time
	size int64
}

type stampedEvent struct {
	eventFile
	ev Event
}

func byTimeThenStem(a, b eventFile) int {
	if c := a.mod.Compare(b.mod); c != 0 {
		return c
	}
	return strings.Compare(a.stem, b.stem)
}

// Scan collects hook events for one instance. New .json files (at most
// maxNewPerScan, oldest first) are parsed and replaced by compact .ev
// files that keep their original modification time. A cold scan also
// replays every .ev file that existed before this scan. Events are
// returned ordered by modification time, then file stem.
// Scan only touches the filesystem, so it is safe to run from a tea.Cmd.
func Scan(req Request, now time.Time) (Result, error) {
	idBytes, err := os.ReadFile(launchIDPath(req.Dir))
	if errors.Is(err, fs.ErrNotExist) {
		return Result{}, ErrNoHooks
	}
	if err != nil {
		return Result{}, fmt.Errorf("hooks: read launch-id: %w", err)
	}
	launchID := strings.TrimSpace(string(idBytes))
	if launchID == "" {
		return Result{}, ErrNoHooks
	}

	dir := EventsDir(req.Dir)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return Result{}, ErrNoHooks
	}
	if err != nil {
		return Result{}, fmt.Errorf("hooks: list events: %w", err)
	}

	var fresh, kept []eventFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		kind, stem := classifyEntry(name, req.Cold)
		if kind == entrySkip {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue // removed since ReadDir
		}
		f := eventFile{path: filepath.Join(dir, name), stem: stem, mod: info.ModTime(), size: info.Size()}
		switch kind {
		case entryTmp:
			if now.Sub(f.mod) > staleTmpAge {
				_ = os.Remove(f.path)
			}
		case entryFresh:
			fresh = append(fresh, f)
		case entryKept:
			kept = append(kept, f)
		}
	}
	slices.SortFunc(fresh, byTimeThenStem)
	if len(fresh) > maxNewPerScan {
		fresh = fresh[:maxNewPerScan]
	}

	var stamped []stampedEvent
	for _, f := range kept {
		if ev, ok := readKept(f); ok {
			stamped = append(stamped, stampedEvent{f, ev})
		}
	}
	for _, f := range fresh {
		if ev, ok := convert(f); ok {
			stamped = append(stamped, stampedEvent{f, ev})
		}
	}
	slices.SortFunc(stamped, func(a, b stampedEvent) int { return byTimeThenStem(a.eventFile, b.eventFile) })

	events := make([]Event, len(stamped))
	for i, s := range stamped {
		events[i] = s.ev
	}
	return Result{
		LaunchID: launchID,
		Events:   events,
		Replayed: req.Cold,
	}, nil
}

// entryKind is what Scan does with one entry of the events folder.
type entryKind int

const (
	entrySkip  entryKind = iota // an unrelated name, or a retained .ev on a warm scan
	entryTmp                    // a write in progress; removed once stale
	entryFresh                  // a new .json event
	entryKept                   // a retained .ev event, replayed by a cold scan
)

// classifyEntry decides an entry's kind, and its file stem, from the name
// alone, so Scan only lstats the entries it uses. A warm scan skips every
// retained .ev file, which would otherwise cost one lstat per file on
// every scan for the rest of a long, teammate-heavy session.
func classifyEntry(name string, cold bool) (entryKind, string) {
	switch {
	case strings.HasSuffix(name, ".tmp"):
		return entryTmp, ""
	case strings.HasSuffix(name, ".json"):
		return entryFresh, strings.TrimSuffix(name, ".json")
	case strings.HasSuffix(name, ".ev") && cold:
		return entryKept, strings.TrimSuffix(name, ".ev")
	}
	return entrySkip, ""
}

func discard(path, reason string) {
	log.DebugKV("hooks.scan.discarded", "file", path, "reason", reason)
	_ = os.Remove(path)
}

func readKept(f eventFile) (Event, bool) {
	data, err := os.ReadFile(f.path)
	if err != nil {
		return Event{}, false
	}
	ev, err := ParseEvent(data)
	if err != nil {
		discard(f.path, err.Error())
		return Event{}, false
	}
	return ev, true
}

// convert parses one new event file and replaces it with its compact .ev
// form. The .json never survives a scan. If the .ev cannot be written the
// event is still returned; it just won't be replayed after a restart.
func convert(f eventFile) (Event, bool) {
	if f.size > maxEventBytes {
		discard(f.path, "too large")
		return Event{}, false
	}
	data, err := os.ReadFile(f.path)
	if err != nil {
		discard(f.path, err.Error())
		return Event{}, false
	}
	ev, err := ParseEvent(data)
	if err != nil {
		discard(f.path, err.Error())
		return Event{}, false
	}
	if err := writeCompact(f, ev); err != nil {
		log.DebugKV("hooks.scan.not_retained", "file", f.path, "err", err.Error())
	}
	_ = os.Remove(f.path)
	return ev, true
}

func writeCompact(f eventFile, ev Event) error {
	data, err := ev.Compact()
	if err != nil {
		return err
	}
	dst := filepath.Join(filepath.Dir(f.path), f.stem+".ev")
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Chtimes(tmp, f.mod, f.mod); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
