package hooks

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var base = time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)

func prepared(t *testing.T) (dir, launchID string) {
	t.Helper()
	dir = filepath.Join(t.TempDir(), "hooks", "loom_x")
	id, err := Prepare(dir)
	require.NoError(t, err)
	return dir, id
}

func writeRaw(t *testing.T, dir, stem, payload string, mod time.Time) string {
	t.Helper()
	path := filepath.Join(EventsDir(dir), stem+".json")
	require.NoError(t, os.WriteFile(path, []byte(payload), 0o600))
	require.NoError(t, os.Chtimes(path, mod, mod))
	return path
}

func startPayload(id string) string {
	return fmt.Sprintf(`{"hook_event_name":"SubagentStart","agent_id":%q,"agent_type":"Explore","transcript_path":"/p/s.jsonl"}`, id)
}

func ids(events []Event) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.AgentID
	}
	return out
}

func names(entries []os.DirEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Name()
	}
	return out
}

func TestScan_NoFolder(t *testing.T) {
	_, err := Scan(Request{Dir: filepath.Join(t.TempDir(), "missing")}, base)
	assert.True(t, errors.Is(err, ErrNoHooks))
}

func TestScan_ConvertsNewEventsInOrder(t *testing.T) {
	dir, id := prepared(t)
	writeRaw(t, dir, "c", startPayload("third"), base.Add(2*time.Second))
	writeRaw(t, dir, "a", startPayload("first"), base)
	writeRaw(t, dir, "b", startPayload("second"), base.Add(time.Second))

	res, err := Scan(Request{Dir: dir}, base.Add(time.Minute))
	require.NoError(t, err)
	assert.Equal(t, id, res.LaunchID)
	assert.False(t, res.Replayed)
	assert.Equal(t, []string{"first", "second", "third"}, ids(res.Events))

	entries, err := os.ReadDir(EventsDir(dir))
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"a.ev", "b.ev", "c.ev"}, names(entries))
	info, err := os.Stat(filepath.Join(EventsDir(dir), "b.ev"))
	require.NoError(t, err)
	assert.True(t, info.ModTime().Equal(base.Add(time.Second)), "compact file keeps the event's time")
}

func TestScan_TiesBreakByStem(t *testing.T) {
	dir, _ := prepared(t)
	writeRaw(t, dir, "2-1", startPayload("later"), base)
	writeRaw(t, dir, "1-9", startPayload("earlier"), base)

	res, err := Scan(Request{Dir: dir}, base)
	require.NoError(t, err)
	assert.Equal(t, []string{"earlier", "later"}, ids(res.Events))
}

func TestScan_WarmIgnoresLogAndColdReplaysIt(t *testing.T) {
	dir, _ := prepared(t)
	writeRaw(t, dir, "a", startPayload("one"), base)
	_, err := Scan(Request{Dir: dir}, base)
	require.NoError(t, err)

	warm, err := Scan(Request{Dir: dir}, base)
	require.NoError(t, err)
	assert.Empty(t, warm.Events)

	cold, err := Scan(Request{Dir: dir, Cold: true}, base)
	require.NoError(t, err)
	assert.True(t, cold.Replayed)
	assert.Equal(t, []string{"one"}, ids(cold.Events))
}

func TestScan_ColdAppliesNewEventsOnce(t *testing.T) {
	dir, _ := prepared(t)
	writeRaw(t, dir, "a", startPayload("old"), base)
	_, err := Scan(Request{Dir: dir}, base)
	require.NoError(t, err)
	writeRaw(t, dir, "b", startPayload("new"), base.Add(time.Second))

	cold, err := Scan(Request{Dir: dir, Cold: true}, base)
	require.NoError(t, err)
	assert.Equal(t, []string{"old", "new"}, ids(cold.Events),
		"the .ev written for b in this scan must not be read again")
}

func TestScan_CapsNewEvents(t *testing.T) {
	dir, _ := prepared(t)
	for i := 0; i < maxNewPerScan+1; i++ {
		writeRaw(t, dir, fmt.Sprintf("%04d", i), startPayload(fmt.Sprintf("a%d", i)), base.Add(time.Duration(i)*time.Millisecond))
	}

	first, err := Scan(Request{Dir: dir}, base)
	require.NoError(t, err)
	assert.Len(t, first.Events, maxNewPerScan)

	second, err := Scan(Request{Dir: dir}, base)
	require.NoError(t, err)
	assert.Equal(t, []string{fmt.Sprintf("a%d", maxNewPerScan)}, ids(second.Events))
}

func TestScan_DiscardsBadFiles(t *testing.T) {
	dir, _ := prepared(t)
	huge := `{"hook_event_name":"Stop","pad":"` + strings.Repeat("x", maxEventBytes) + `"}`
	writeRaw(t, dir, "huge", huge, base)
	writeRaw(t, dir, "junk", "not json", base)
	writeRaw(t, dir, "other", `{"hook_event_name":"PreToolUse"}`, base)

	res, err := Scan(Request{Dir: dir}, base)
	require.NoError(t, err)
	assert.Empty(t, res.Events)
	entries, err := os.ReadDir(EventsDir(dir))
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestScan_StaleTmp(t *testing.T) {
	dir, _ := prepared(t)
	stale := filepath.Join(EventsDir(dir), "old.tmp")
	fresh := filepath.Join(EventsDir(dir), "new.tmp")
	for path, mod := range map[string]time.Time{stale: base.Add(-2 * time.Minute), fresh: base.Add(-10 * time.Second)} {
		require.NoError(t, os.WriteFile(path, []byte(`{"hook_event_name":"Stop"}`), 0o600))
		require.NoError(t, os.Chtimes(path, mod, mod))
	}

	res, err := Scan(Request{Dir: dir}, base)
	require.NoError(t, err)
	assert.Empty(t, res.Events)
	assert.NoFileExists(t, stale)
	assert.FileExists(t, fresh)
}

func TestScan_MalformedTasksReplayAsMissing(t *testing.T) {
	dir, _ := prepared(t)
	writeRaw(t, dir, "s", `{"hook_event_name":"Stop","background_tasks":{"x":1}}`, base)
	_, err := Scan(Request{Dir: dir}, base)
	require.NoError(t, err)

	cold, err := Scan(Request{Dir: dir, Cold: true}, base)
	require.NoError(t, err)
	require.Len(t, cold.Events, 1)
	assert.False(t, cold.Events[0].HasTasks)
}

func TestEventTime(t *testing.T) {
	mod := base
	assert.True(t, time.Unix(0, 1790179911178279565).Equal(eventTime("1790179911178279565-2237570", mod)))
	assert.True(t, mod.Equal(eventTime("1790179911N-2237570", mod)), "macOS date prints a literal N")
	assert.True(t, mod.Equal(eventTime("1790179911-2237570", mod)), "a seconds prefix is not nanoseconds")
	assert.True(t, mod.Equal(eventTime("a", mod)))
}

func TestScan_StampsEventsFromTheirNames(t *testing.T) {
	dir, _ := prepared(t)
	// The modification times disagree with the names on purpose: the name wins.
	writeRaw(t, dir, "1790179911000000002-1", startPayload("second"), base)
	writeRaw(t, dir, "1790179911000000001-1", startPayload("first"), base.Add(time.Second))

	res, err := Scan(Request{Dir: dir}, base)
	require.NoError(t, err)
	assert.Equal(t, []string{"first", "second"}, ids(res.Events))
	assert.True(t, res.Events[0].At.Equal(time.Unix(0, 1790179911000000001)))

	cold, err := Scan(Request{Dir: dir, Cold: true}, base)
	require.NoError(t, err)
	require.Len(t, cold.Events, 2)
	assert.True(t, cold.Events[0].At.Equal(time.Unix(0, 1790179911000000001)),
		"a replay recomputes At from the kept file's name")
}

func TestScan_StampsFromModTimeWithoutNanos(t *testing.T) {
	dir, _ := prepared(t)
	writeRaw(t, dir, "a", startPayload("one"), base)

	res, err := Scan(Request{Dir: dir}, base)
	require.NoError(t, err)
	require.Len(t, res.Events, 1)
	assert.True(t, res.Events[0].At.Equal(base))
}
