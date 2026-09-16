package subagent

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
var ErrNoHooks = errors.New("subagent: no hooks folder")

// Request describes one instance's scan.
type Request struct {
	// Dir is the instance's hooks folder.
	Dir string
	// Cold replays the retained .ev log. True for the first scan after a
	// launch or a loom restart.
	Cold bool
	// MissingMeta lists agents still waiting for their sidecar.
	MissingMeta []MetaRef
}

// Result is what one scan found.
type Result struct {
	LaunchID string
	Events   []Event
	Meta     map[string]Meta
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
// returned ordered by modification time, then file stem, together with
// the metadata sidecars for new SubagentStart events and req.MissingMeta.
// Scan only touches the filesystem, so it is safe to run from a tea.Cmd.
func Scan(req Request, now time.Time) (Result, error) {
	idBytes, err := os.ReadFile(launchIDPath(req.Dir))
	if errors.Is(err, fs.ErrNotExist) {
		return Result{}, ErrNoHooks
	}
	if err != nil {
		return Result{}, fmt.Errorf("subagent: read launch-id: %w", err)
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
		return Result{}, fmt.Errorf("subagent: list events: %w", err)
	}

	var fresh, kept []eventFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue // removed since ReadDir
		}
		name := e.Name()
		f := eventFile{path: filepath.Join(dir, name), mod: info.ModTime(), size: info.Size()}
		switch {
		case strings.HasSuffix(name, ".tmp"):
			if now.Sub(f.mod) > staleTmpAge {
				_ = os.Remove(f.path)
			}
		case strings.HasSuffix(name, ".json"):
			f.stem = strings.TrimSuffix(name, ".json")
			fresh = append(fresh, f)
		case strings.HasSuffix(name, ".ev") && req.Cold:
			f.stem = strings.TrimSuffix(name, ".ev")
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
		Meta:     readMeta(events, req.MissingMeta),
		Replayed: req.Cold,
	}, nil
}

func discard(path, reason string) {
	log.DebugKV("subagent.scan.discarded", "file", path, "reason", reason)
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
		log.DebugKV("subagent.scan.not_retained", "file", f.path, "err", err.Error())
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

// readMeta reads the sidecars for SubagentStart events and for agents
// still missing one. An unreadable sidecar is skipped; the tracker keeps
// the agent hidden and it is retried on the next scan.
func readMeta(events []Event, missing []MetaRef) map[string]Meta {
	refs := slices.Clone(missing)
	for _, ev := range events {
		if ev.Name != EventSubagentStart {
			continue
		}
		if p := MetaPath(ev.TranscriptPath, ev.AgentID); p != "" {
			refs = append(refs, MetaRef{AgentID: ev.AgentID, Path: p})
		}
	}
	meta := map[string]Meta{}
	for _, r := range refs {
		if _, done := meta[r.AgentID]; done {
			continue
		}
		data, err := os.ReadFile(r.Path)
		if err != nil {
			continue
		}
		m, err := ParseMeta(data)
		if err != nil {
			continue
		}
		meta[r.AgentID] = m
	}
	return meta
}
