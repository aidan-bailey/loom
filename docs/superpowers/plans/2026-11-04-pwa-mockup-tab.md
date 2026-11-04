# PWA mockup tab Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The phone app gets a "mockup" tab that shows the page a session's brainstorm companion is serving now, sandboxed, with the reply box below and a full-screen view.

**Architecture:** A pure reader, `session/mockup`, finds a live companion in a worktree and fetches its page from `127.0.0.1`. The model looks for it when a hook scan reads a hand-back event and on each `ReadMockup` request, keeps the screen on the instance and publishes `InstanceView.Mockup`. The web layer serves the page at `GET /api/v1/sessions/{id}/mockup` under a sandbox CSP and links pushes to the tab; the app draws it in an iframe.

**Tech Stack:** Go (net/http, the `core` request/Reply machinery, the generated rpc wire), Preact + htm modules, webshot for screenshots.

**Spec:** `docs/superpowers/specs/2026-11-04-pwa-mockup-tab-design.md`. This plan settles four of its details, written back into the spec in Task 0: a failed read answers a small HTML page (the frame shows why) rather than JSON; full screen is its own route, `#s/<id>/mockup/full`, so webshot can render it; the hook scan's `Find` is swapped in tests through an unexported package var, not a `…ForTest` seam; and the link to the tab applies to both push kinds, since a brainstorm's turn ends in a finished push (Running → Ready).

**Rules to keep (from the package CLAUDE.md files):** scratch files outside the worktree; never run loom in a pane (`go run ./tools/loomdev` only); commit with explicit paths; never edit `core/rpc/methods_gen.go` or `docs/specs/protocol.md` by hand; a `Job` reads no model state; no inline script or `style` attribute in the app.

**Gates (each task ends with the ones that touch it; Task 5 runs all):**
- `CGO_ENABLED=0 go test ./...`
- `CC=clang CGO_ENABLED=1 go test -race ./session/... ./core/... ./internal/web/...`
- `gofmt -l $(git ls-files '*.go' | grep -v '^vendor/')` prints nothing; `CGO_ENABLED=0 go vet ./...`
- `go run ./tools/claudemd`
- `GOOS=windows CGO_ENABLED=0 go build ./...`
- `for f in internal/web/static/js/*.mjs; do node --check "$f"; done`

---

## File structure

| File | Responsibility |
|---|---|
| `session/mockup/mockup.go` (new) | `Companion`, `Page`, `Find`, `Fetch` |
| `session/mockup/mockup_test.go` (new) | Find's and Fetch's tests |
| `session/mockup/testmain_test.go` (new) | the package's `TestMain` |
| `session/instance_mockup.go` (new) | the instance's in-memory mockup and turn start |
| `session/instance.go` | `TransitionTo` records the turn start; the fields |
| `core/mockup.go` (new) | `findMockup`, `handsBack`, `ReadMockup`, its job, `deliverMockup` |
| `core/hook_scan.go` | the scan's look for a hand-back; the face; the delivery |
| `core/view.go`, `core/views.go` | `MockupView`, `InstanceView.Mockup`, `viewOf` |
| `core/iface.go`, `core/loop_core.go` | the method and its forwarder |
| `core/events.go`, `core/events_json.go` | `Reply.Mockup` and its codec |
| `core/requests.go`, `core/model.go` | `opReadMockup`, its precondition, outcome, Reply copy, Deliver case |
| `core/mockup_test.go` (new) | the model's tests |
| `core/rpc/methods_gen.go`, `docs/specs/protocol.md` | regenerated |
| `internal/web/mockup.go` (new) | the route, the page writer, the CSP, the error page |
| `internal/web/mockup_test.go` (new) | the route's tests |
| `internal/web/reads.go` | `read` split so a route can answer its own failures |
| `internal/web/api.go` | the route's registration |
| `internal/web/fleet.go` | `mockupJSON` in `sessionJSON` |
| `internal/web/triggers.go` | `pushJob.Mockup`, the payload's URL |
| `internal/web/static/js/mockup.mjs` (new) | the screen |
| `internal/web/static/js/{app,screens,chrome,session,fleet}.mjs`, `internal/web/static/app.css` | the route, the tab, the link, the styles |
| `tools/webshot/{main,canned}.go`, `tools/webshot/fixture/{snapshot.json,mockup.html}` | the screens and the canned page |
| docs | `internal/web/CLAUDE.md`, `internal/web/README.md`, `session/CLAUDE.md`, `session/README.md`, `docs/claude/web-layer.md` |

---

### Task 0: Write the plan's settlements into the spec

**Files:** Modify `docs/superpowers/specs/2026-11-04-pwa-mockup-tab-design.md`

- [ ] **Step 1:** In "The web layer", replace "A refusal is 409 with the model's words; no answer within `replyWait` is 504." with: "A refusal is 409 and no answer within `replyWait` 504, each answered as a small HTML page under the same CSP that says why in the app's colours, since the route is only ever an iframe's `src`; an unknown session or a workspace terminal stays the JSON 404 every route answers."
- [ ] **Step 2:** In "The waiting push links the tab", make it "A push links the tab": both kinds (waiting and finished) take `/#s/<id>/mockup` under the same condition.
- [ ] **Step 3:** In "The phone", Full screen: "⤢ goes to `#s/<id>/mockup/full`, which renders no header and no reply box; ✕ done goes back to `#s/<id>/mockup`." Empty and failed: "With `s.mockup` null the tab's body says 'No mockup is showing; the companion may have stopped.'; a failed read shows its page in the frame, and ↻ retries."
- [ ] **Step 4:** In Testing, `TestHookScan_OnlyAHandBackLooks` swaps the unexported `findMockup` var (no `core/seams.go` seam).
- [ ] **Step 5:** Commit with the plan:

```bash
git add -- docs/superpowers/specs/2026-11-04-pwa-mockup-tab-design.md docs/superpowers/plans/2026-11-04-pwa-mockup-tab.md
git commit -m "docs(web): plan the PWA's mockup tab" -- docs/superpowers/specs/2026-11-04-pwa-mockup-tab-design.md docs/superpowers/plans/2026-11-04-pwa-mockup-tab.md
```

---

### Task 1: `session/mockup`, the companion reader

**Files:**
- Create: `session/mockup/mockup.go`, `session/mockup/mockup_test.go`, `session/mockup/testmain_test.go`
- Modify: `session/CLAUDE.md` (the sub-package list in its first paragraph, and "Keep `session/github`, … free of tmux, UI and app imports" gains `session/mockup`), `session/README.md` (the sub-package list)

- [ ] **Step 1: Write the failing tests** (`session/mockup/mockup_test.go`)

```go
package mockup

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// companionPage is what the companion serves: a page with its helper's marker.
const companionPage = `<!DOCTYPE html><html><body><h2>Which layout?</h2><script>window.brainstorm = {send: 0};</script></body></html>`

// brainstormDir makes <tree>/.superpowers/brainstorm/<name> with a
// server-info naming port (none when port is 0), a server-stopped when
// stopped, and the screens, each written with its mtime in order.
func brainstormDir(t *testing.T, tree, name string, port int, stopped bool, at time.Time, screens ...string) {
	t.Helper()
	dir := filepath.Join(tree, ".superpowers", "brainstorm", name)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "state"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "content"), 0o755))
	if port != 0 {
		info := filepath.Join(dir, "state", "server-info")
		require.NoError(t, os.WriteFile(info, []byte(fmt.Sprintf(`{"type":"server-started","port":%d,"host":"127.0.0.1"}`+"\n", port)), 0o644))
		require.NoError(t, os.Chtimes(info, at, at))
	}
	if stopped {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "state", "server-stopped"), []byte("{}\n"), 0o644))
	}
	for i, s := range screens {
		p := filepath.Join(dir, "content", s)
		require.NoError(t, os.WriteFile(p, []byte("<h2>"+s+"</h2>"), 0o644))
		mt := at.Add(time.Duration(i+1) * time.Second)
		require.NoError(t, os.Chtimes(p, mt, mt))
	}
}

func TestFind_PicksTheNewestLiveCompanion(t *testing.T) {
	tree := t.TempDir()
	at := time.Now().Add(-time.Hour).Truncate(time.Second)
	brainstormDir(t, tree, "1-old", 50001, false, at, "a.html")
	brainstormDir(t, tree, "2-stopped", 50002, true, at.Add(20*time.Minute), "b.html")
	brainstormDir(t, tree, "3-live", 50003, false, at.Add(10*time.Minute), "layout.html", "layout-v2.html")

	c, ok, err := Find(tree)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, 50003, c.Port)
	assert.Equal(t, "layout-v2.html", c.Screen)
	assert.Equal(t, at.Add(10*time.Minute+2*time.Second), c.Changed.Truncate(time.Second))
}

func TestFind_NoMockup(t *testing.T) {
	at := time.Now().Add(-time.Hour)
	for _, tc := range []struct {
		name  string
		build func(t *testing.T, tree string)
	}{
		{"no brainstorm dir", func(t *testing.T, tree string) {}},
		{"only a stopped companion", func(t *testing.T, tree string) { brainstormDir(t, tree, "1", 50001, true, at, "a.html") }},
		{"no server-info", func(t *testing.T, tree string) { brainstormDir(t, tree, "1", 0, false, at, "a.html") }},
		{"a garbled server-info", func(t *testing.T, tree string) {
			brainstormDir(t, tree, "1", 50001, false, at, "a.html")
			require.NoError(t, os.WriteFile(filepath.Join(tree, ".superpowers", "brainstorm", "1", "state", "server-info"), []byte("{port"), 0o644))
		}},
		{"port 0", func(t *testing.T, tree string) {
			brainstormDir(t, tree, "1", 50001, false, at, "a.html")
			require.NoError(t, os.WriteFile(filepath.Join(tree, ".superpowers", "brainstorm", "1", "state", "server-info"), []byte(`{"port":0}`), 0o644))
		}},
		{"port 65536", func(t *testing.T, tree string) { brainstormDir(t, tree, "1", 65536, false, at, "a.html") }},
		{"no screens", func(t *testing.T, tree string) { brainstormDir(t, tree, "1", 50001, false, at) }},
		{"the newest screen is a waiting screen", func(t *testing.T, tree string) {
			brainstormDir(t, tree, "1", 50001, false, at, "layout.html", "waiting-2.html")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tree := t.TempDir()
			tc.build(t, tree)
			_, ok, err := Find(tree)
			assert.NoError(t, err)
			assert.False(t, ok)
		})
	}
	_, ok, err := Find("")
	assert.NoError(t, err)
	assert.False(t, ok, "a session with no worktree has no companion")
}

// serve starts a loopback server answering h and returns its port.
func serve(t *testing.T, h http.HandlerFunc) int {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)
	return port
}

func TestFetch_ReturnsTheCompanionsPage(t *testing.T) {
	port := serve(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/", r.URL.Path)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(companionPage))
	})
	got, err := Fetch(context.Background(), port)
	require.NoError(t, err)
	assert.Equal(t, companionPage, got)
}

func TestFetch_RefusesWhatIsNotTheCompanion(t *testing.T) {
	old := fetchTimeout
	fetchTimeout = 200 * time.Millisecond
	t.Cleanup(func() { fetchTimeout = old })
	for _, tc := range []struct {
		name string
		h    http.HandlerFunc
		want string
	}{
		{"no marker", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html>another server</html>"))
		}, "is not the brainstorm companion"},
		{"too large", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(companionPage + strings.Repeat("x", maxPageBytes)))
		}, "over 1 MiB"},
		{"not HTML", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"window.brainstorm":1}`))
		}, "not HTML"},
		{"not 200", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "nope", http.StatusNotFound) }, "answered 404"},
		{"a redirect", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://example.com/", http.StatusFound)
		}, "answered 302"},
		{"too slow", func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(time.Second)
		}, "does not answer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			port := serve(t, tc.h)
			got, err := Fetch(context.Background(), port)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.Empty(t, got)
			assert.NotContains(t, err.Error(), "<html", "the error never carries the body")
		})
	}
}
```

`session/mockup/testmain_test.go`, as every package's:

```go
package mockup

import (
	"os"
	"testing"

	"github.com/aidan-bailey/loom/log"
)

func TestMain(m *testing.M) {
	log.Initialize(false)
	code := m.Run()
	log.Close()
	os.Exit(code)
}
```

(Check another `testmain_test.go` in `session/transcript` first and copy its exact shape if it differs.)

- [ ] **Step 2: Run them to see them fail**

Run: `CGO_ENABLED=0 go test ./session/mockup/`
Expected: FAIL, `undefined: Find`.

- [ ] **Step 3: Write `session/mockup/mockup.go`**

```go
// Package mockup reads the brainstorming skill's visual companion from a
// session's worktree: Find says whether one runs and which screen it shows,
// and Fetch reads the page it serves on 127.0.0.1. It touches only the
// filesystem and the loopback interface.
package mockup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// maxPageBytes caps a page, as a patch is capped (session/git).
const maxPageBytes = 1 << 20

// marker is in every page the companion serves: its helper script, which it
// injects into each, defines it.
const marker = "window.brainstorm"

// fetchTimeout bounds a Fetch; a var so a test can shorten it.
var fetchTimeout = 5 * time.Second

// Companion is a running companion found in a worktree.
type Companion struct {
	Port    int
	Screen  string    // the newest content/*.html, a base name
	Changed time.Time // its mtime
}

// Page is a companion's page as a client is sent it.
type Page struct {
	Screen  string
	Changed time.Time
	HTML    string
}

// Find looks in <worktree>/.superpowers/brainstorm/*/ for the session dir
// whose state/server-info is newest and has no state/server-stopped beside
// it, and returns its port and its newest screen. ok is false on any doubt:
// no such dir, a server-info that doesn't parse or names no port, no
// screen, or a newest screen that is the skill's waiting screen
// (waiting*.html). err is set only for an I/O error other than not-exist.
func Find(worktree string) (c Companion, ok bool, err error) {
	if worktree == "" {
		return Companion{}, false, nil
	}
	root := filepath.Join(worktree, ".superpowers", "brainstorm")
	dirs, err := os.ReadDir(root)
	if err != nil {
		return Companion{}, false, ignoreNotExist(err)
	}
	var best string
	var bestAt time.Time
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		state := filepath.Join(root, d.Name(), "state")
		info, err := os.Stat(filepath.Join(state, "server-info"))
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				return Companion{}, false, err
			}
			continue
		}
		if _, err := os.Stat(filepath.Join(state, "server-stopped")); err == nil {
			continue
		}
		if best == "" || info.ModTime().After(bestAt) {
			best, bestAt = d.Name(), info.ModTime()
		}
	}
	if best == "" {
		return Companion{}, false, nil
	}
	dir := filepath.Join(root, best)
	raw, err := os.ReadFile(filepath.Join(dir, "state", "server-info"))
	if err != nil {
		return Companion{}, false, ignoreNotExist(err)
	}
	var info struct {
		Port int `json:"port"`
	}
	if json.Unmarshal(raw, &info) != nil || info.Port < 1 || info.Port > 65535 {
		return Companion{}, false, nil
	}
	screen, changed, err := newestScreen(filepath.Join(dir, "content"))
	if err != nil || screen == "" || strings.HasPrefix(screen, "waiting") {
		return Companion{}, false, ignoreNotExist(err)
	}
	return Companion{Port: info.Port, Screen: screen, Changed: changed}, true, nil
}

// newestScreen is the .html file in dir with the latest mtime ("" for none).
func newestScreen(dir string) (string, time.Time, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", time.Time{}, err
	}
	var name string
	var at time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".html") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue // removed since the listing
		}
		if name == "" || info.ModTime().After(at) {
			name, at = e.Name(), info.ModTime()
		}
	}
	return name, at, nil
}

func ignoreNotExist(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// client fetches only what it is asked: no proxy, no redirect followed, no
// connection kept.
var client = &http.Client{
	Transport:     &http.Transport{Proxy: nil, DisableKeepAlives: true},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// Fetch GETs http://127.0.0.1:<port>/ and returns the page. It refuses a
// status but 200, a type but text/html, a body over 1 MiB and a body
// without the companion's marker, with an error that names why and never
// carries the body.
func Fetch(ctx context.Context, port int) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	addr := "127.0.0.1:" + strconv.Itoa(port)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/", nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("the companion on port %d does not answer: %w", port, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the companion on port %d answered %d", port, resp.StatusCode)
	}
	if mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err != nil || mt != "text/html" {
		return "", fmt.Errorf("the page on port %d is not HTML (%q)", port, resp.Header.Get("Content-Type"))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPageBytes+1))
	if err != nil {
		return "", fmt.Errorf("the companion on port %d does not answer: %w", port, err)
	}
	if len(body) > maxPageBytes {
		return "", fmt.Errorf("the companion's page is over 1 MiB")
	}
	if !strings.Contains(string(body), marker) {
		return "", fmt.Errorf("port %d is not the brainstorm companion", port)
	}
	return string(body), nil
}
```

- [ ] **Step 4: Run the tests**

Run: `CGO_ENABLED=0 go test ./session/mockup/ -v`
Expected: PASS. (`too slow` fails with a context deadline wrapped as "does not answer"; if the error is raised from `io.ReadAll` instead, the same wording covers it.)

- [ ] **Step 5: Docs.** In `session/CLAUDE.md`'s first paragraph add `mockup` to the sub-packages (alphabetical: after `launch`), and in the rule "Keep `session/github`, `session/hooks`, … free of tmux, UI and app imports" add `session/mockup`. Add `mockup` to `session/README.md`'s sub-package list with one line: "reads the brainstorm companion a worktree runs (`Find`) and its page (`Fetch`)". Run `go run ./tools/claudemd`.

- [ ] **Step 6: Commit**

```bash
git add -- session/mockup session/CLAUDE.md session/README.md
git commit -m "feat(session): read the brainstorm companion a worktree runs" -- session/mockup session/CLAUDE.md session/README.md
```

---

### Task 2: The model: the mockup on the view, the scan's look, `ReadMockup`

**Files:**
- Create: `session/instance_mockup.go`, `core/mockup.go`, `core/mockup_test.go`
- Modify: `session/instance.go` (fields; `TransitionTo`), a test in `session/instance_test.go`; `core/view.go`, `core/views.go`, `core/hook_scan.go`, `core/iface.go`, `core/loop_core.go`, `core/events.go`, `core/events_json.go`, `core/requests.go`, `core/model.go`, `core/requests_test.go`; regenerated `core/rpc/methods_gen.go`, `docs/specs/protocol.md`

- [ ] **Step 1: The instance's turn start and mockup — failing test** (append to `session/instance_test.go`)

```go
func TestInstance_ATurnStartsOnRunningButNotAfterAPrompt(t *testing.T) {
	inst, err := FromInstanceData(InstanceData{Title: "t", Status: Paused, Program: "claude"}, t.TempDir())
	require.NoError(t, err)
	assert.True(t, inst.TurnStart().IsZero(), "no turn seen in this process")

	require.NoError(t, inst.TransitionTo(Running))
	first := inst.TurnStart()
	require.False(t, first.IsZero())

	require.NoError(t, inst.TransitionTo(Prompting))
	require.NoError(t, inst.TransitionTo(Running))
	assert.Equal(t, first, inst.TurnStart(), "a prompt inside a turn doesn't split it")

	require.NoError(t, inst.TransitionTo(Ready))
	time.Sleep(time.Millisecond)
	require.NoError(t, inst.TransitionTo(Running))
	assert.True(t, inst.TurnStart().After(first), "a reply starts a new turn")
}

func TestInstance_KeepsTheMockupItIsGiven(t *testing.T) {
	inst, err := FromInstanceData(InstanceData{Title: "t", Status: Paused, Program: "claude"}, t.TempDir())
	require.NoError(t, err)
	at := time.Now()
	inst.SetMockup("layout.html", at)
	screen, changed := inst.Mockup()
	assert.Equal(t, "layout.html", screen)
	assert.Equal(t, at, changed)
	inst.SetMockup("", time.Time{})
	screen, _ = inst.Mockup()
	assert.Empty(t, screen)
}
```

Check `IsAllowedTransition` allows Paused→Running, Running→Prompting, Prompting→Running, Running→Ready and Ready→Running; if one is refused, route the fixture through the allowed path (Loading) instead.

- [ ] **Step 2: Run, see it fail** — `CGO_ENABLED=0 go test ./session/ -run 'TestInstance_ATurnStarts|TestInstance_KeepsTheMockup'`: `inst.TurnStart undefined`.

- [ ] **Step 3: Implement.** In `session/instance.go`, beside `statusChangedAt` (guarded by `mu`):

```go
	// turnStartedAt is when the instance last entered Running from any
	// status but Prompting: the start of the turn a brainstorm screen is
	// fresh in (in-memory only; zero until this process sees one).
	turnStartedAt time.Time
	// mockupScreen and mockupChanged are the brainstorm companion's screen
	// the model last found in the worktree (core's findMockup), in memory
	// only; "" for none.
	mockupScreen  string
	mockupChanged time.Time
```

In `TransitionTo`, after `i.Status = to`:

```go
	now := time.Now()
	i.statusChangedAt = now
	if to == Running && from != Prompting {
		i.turnStartedAt = now
	}
```

(replacing the existing `i.statusChangedAt = time.Now()`). Create `session/instance_mockup.go`:

```go
package session

import "time"

// TurnStart is when the instance's latest turn began (TransitionTo), or the
// zero time when this process has seen none.
func (i *Instance) TurnStart() time.Time {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.turnStartedAt
}

// SetMockup records the brainstorm companion's screen the model found in
// the worktree; "" clears it.
func (i *Instance) SetMockup(screen string, changed time.Time) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.mockupScreen, i.mockupChanged = screen, changed
}

// Mockup is the screen SetMockup recorded and its mtime.
func (i *Instance) Mockup() (string, time.Time) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.mockupScreen, i.mockupChanged
}
```

Run the two tests: PASS.

- [ ] **Step 4: The model's failing tests** (`core/mockup_test.go`)

```go
package core

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/hooks"
	"github.com/aidan-bailey/loom/session/mockup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const companionHTML = `<html><body>Which layout?<script>window.brainstorm = {};</script></body></html>`

// companionIn writes a live companion on port into tree, its one screen
// changed at at.
func companionIn(t *testing.T, tree string, port int, screen string, at time.Time) {
	t.Helper()
	dir := filepath.Join(tree, ".superpowers", "brainstorm", "1-1")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "state"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "content"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "state", "server-info"), []byte(fmt.Sprintf(`{"port":%d}`, port)), 0o644))
	p := filepath.Join(dir, "content", screen)
	require.NoError(t, os.WriteFile(p, []byte("<h2>x</h2>"), 0o644))
	require.NoError(t, os.Chtimes(p, at, at))
}

// companionServer serves companionHTML on loopback and returns its port.
func companionServer(t *testing.T) int {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(companionHTML))
	}))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)
	return port
}

// writeHook drops one hook event into inst's hooks folder.
func writeHook(t *testing.T, inst *session.Instance, payload string) {
	t.Helper()
	dir := session.SubagentHooksDir(inst.ConfigDir, inst.Title)
	_, err := hooks.Prepare(dir, "")
	require.NoError(t, err)
	name := fmt.Sprintf("%d-1.json", time.Now().UnixNano())
	require.NoError(t, os.WriteFile(filepath.Join(hooks.EventsDir(dir), name), []byte(payload), 0o600))
}

func TestHookScan_AStopFindsTheMockupInTheSameDelivery(t *testing.T) {
	m := NewForTest(Options{})
	inst := activeInst(t, m, "brainstorm")
	companionIn(t, inst.GetWorktreePath(), 50001, "layout.html", time.Now())
	writeHook(t, inst, `{"hook_event_name":"Stop"}`)

	require.True(t, m.maybeHookScan(m.activeInstances()))
	m.Deliver(m.Drain().Jobs[0]())

	v := m.viewOf(inst)
	assert.Equal(t, session.Ready, v.Status)
	assert.Equal(t, "layout.html", v.Mockup.Screen, "the status and the mockup land in one delivery")
	assert.True(t, v.Mockup.Fresh)
}

func TestHookScan_OnlyAHandBackLooks(t *testing.T) {
	m := NewForTest(Options{})
	inst := activeInst(t, m, "brainstorm")
	looks := 0
	old := findMockup
	findMockup = func(tree string) (mockup.Companion, bool, error) { looks++; return mockup.Find(tree) }
	t.Cleanup(func() { findMockup = old })

	writeHook(t, inst, `{"hook_event_name":"UserPromptSubmit"}`)
	require.True(t, m.maybeHookScan(m.activeInstances()))
	m.Deliver(m.Drain().Jobs[0]())
	assert.Zero(t, looks, "a scan that hands nothing back reads no more than before")

	m.gate(gateHookScan).last = time.Time{}
	writeHook(t, inst, `{"hook_event_name":"Stop"}`)
	require.True(t, m.maybeHookScan(m.activeInstances()))
	m.Deliver(m.Drain().Jobs[0]())
	assert.Equal(t, 1, looks)
}

func TestMockup_FreshUntilTheNextTurn(t *testing.T) {
	m := NewForTest(Options{})
	inst := activeInst(t, m, "brainstorm")
	start := inst.TurnStart()
	require.False(t, start.IsZero())

	inst.SetMockup("layout.html", start.Add(time.Second))
	assert.True(t, m.viewOf(inst).Mockup.Fresh, "changed in the latest turn")

	require.NoError(t, inst.TransitionTo(session.Prompting))
	require.NoError(t, inst.TransitionTo(session.Running))
	assert.True(t, m.viewOf(inst).Mockup.Fresh, "a prompt inside the turn keeps it fresh")

	require.NoError(t, inst.TransitionTo(session.Ready))
	time.Sleep(2 * time.Millisecond)
	require.NoError(t, inst.TransitionTo(session.Running))
	inst.SetMockup("layout.html", start.Add(time.Millisecond))
	assert.False(t, m.viewOf(inst).Mockup.Fresh, "a new turn leaves the old screen behind")

	stale, err := session.FromInstanceData(session.InstanceData{Title: "s", Status: session.Paused, Program: "claude"}, t.TempDir())
	require.NoError(t, err)
	stale.SetMockup("layout.html", time.Now())
	assert.False(t, m.viewOf(stale).Mockup.Fresh, "no turn seen: never fresh")
}

func TestReadMockup_AnswersThePageAndRefreshesTheView(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	inst, _ := worktreeInst(t, "brainstorm")
	ws.add(inst)
	m.SetWorkspacesForTest(ws)
	at := time.Now().Round(0)
	companionIn(t, inst.GetWorktreePath(), companionServer(t), "layout.html", at)

	m.ReadMockup(m.idOf(inst), 7)
	rs := replies(run(m, m.Drain()))
	require.Len(t, rs, 1)
	require.NoError(t, rs[0].Err)
	assert.Equal(t, "layout.html", rs[0].Mockup.Screen)
	assert.Equal(t, companionHTML, rs[0].Mockup.HTML)
	assert.Equal(t, "layout.html", m.viewOf(inst).Mockup.Screen)
}

func TestReadMockup_RefusesWithNoCompanionAndClearsTheView(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	inst, _ := worktreeInst(t, "brainstorm")
	ws.add(inst)
	m.SetWorkspacesForTest(ws)
	inst.SetMockup("old.html", time.Now())

	m.ReadMockup(m.idOf(inst), 7)
	rs := replies(run(m, m.Drain()))
	require.Len(t, rs, 1)
	require.Error(t, rs[0].Err)
	assert.ErrorIs(t, rs[0].Err, ErrRefused)
	assert.Equal(t, "read the mockup of brainstorm: no brainstorm companion is running in this session's worktree", rs[0].Err.Error())
	assert.Empty(t, m.viewOf(inst).Mockup.Screen)
}
```

`m.gate(gateHookScan).last` is the gate's last-dispatch field: check `core/gate.go` for its actual name and use it (the point is to let the second scan dispatch inside `hookScanInterval`). `replies`, `run`, `storedWorkspace` and `worktreeInst` are the existing helpers of `core/reads_test.go` and its neighbours.

Add to `TestRequests_RefuseWhatTheTUIRefuses`'s table (`core/requests_test.go`, beside the diff cases):

```go
		{"read the mockup of a terminal", func(f fixture) { f.m.ReadMockup(f.id["term"], 1) }, "read the mockup of term: not allowed on a workspace terminal"},
```

- [ ] **Step 5: Run, see them fail** — `CGO_ENABLED=0 go test ./core/ -run 'Mockup|HookScan_'`: undefined `findMockup`, `ReadMockup`, `Mockup`.

- [ ] **Step 6: The view.** In `core/view.go`, after `Asks`:

```go
	// Mockup is the session's brainstorm companion as the model last found
	// it (core/mockup.go); Screen "" means none.
	Mockup MockupView
```

and below `InstanceView`:

```go
// MockupView is the screen a session's brainstorm companion serves. Fresh:
// the screen changed at or after the start of the session's latest turn
// (Instance.TurnStart), which a new turn leaves behind.
type MockupView struct {
	Screen  string
	Changed time.Time
	Fresh   bool
}
```

In `viewOf` (`core/views.go`), after `v.LastMessage, …`:

```go
	if screen, changed := inst.Mockup(); screen != "" {
		start := inst.TurnStart()
		v.Mockup = MockupView{Screen: screen, Changed: changed, Fresh: !start.IsZero() && !changed.Before(start)}
	}
```

- [ ] **Step 7: The scan's look.** Create `core/mockup.go`:

```go
package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/hooks"
	"github.com/aidan-bailey/loom/session/mockup"
)

// findMockup is mockup.Find; a test swaps it to count the looks.
var findMockup = mockup.Find

// mockupLook is one look for a session's companion: what Find said.
type mockupLook struct {
	c  mockup.Companion
	ok bool
}

// lookForMockup runs findMockup on a job's goroutine and logs an I/O error
// at debug; any doubt is no companion.
func lookForMockup(worktree string) *mockupLook {
	c, ok, err := findMockup(worktree)
	if err != nil {
		log.DebugKV("core.mockup.find_failed", "worktree", worktree, "err", err.Error())
	}
	return &mockupLook{c: c, ok: ok && err == nil}
}

// applyMockup records a look on inst: the screen found, or none.
func applyMockup(inst *session.Instance, look *mockupLook) {
	if look.ok {
		inst.SetMockup(look.c.Screen, look.c.Changed)
		return
	}
	inst.SetMockup("", time.Time{})
}

// handsBack reports whether a scan's events hand the turn to the user: the
// parent's Stop or Notification, or a PermissionRequest from any agent (as
// the status mapping counts them), when a brainstorm's screen is worth
// looking for.
func handsBack(events []hooks.Event) bool {
	for _, e := range events {
		switch e.Name {
		case hooks.EventStop, hooks.EventNotification:
			if e.AgentID == "" {
				return true
			}
		case hooks.EventPermissionRequest:
			return true
		}
	}
	return false
}

// mockupResult is ReadMockup's job result.
type mockupResult struct {
	inst *session.Instance
	look *mockupLook
	page mockup.Page
	err  error
}

// ReadMockup reads the page session id's brainstorm companion serves now
// (mockup.Find, then mockup.Fetch) in a job, records what it found on the
// instance, and answers with the Reply's Mockup; with no companion, or a
// page Fetch refuses, it refuses.
func (m *Model) ReadMockup(id InstanceID, req ReqID) {
	inst, _, ok := m.admit(opReadMockup, id, req)
	if !ok {
		return
	}
	m.spawn(m.track(req, id, readMockupJob(inst, inst.GetWorktreePath())))
}

// readMockupJob is ReadMockup's job: the worktree is read on the loop, the
// job touches only the disk and the loopback port.
func readMockupJob(inst *session.Instance, worktree string) Job {
	title := inst.Title
	return func() any {
		look := lookForMockup(worktree)
		res := mockupResult{inst: inst, look: look}
		if !look.ok {
			res.err = fmt.Errorf("%s %s: no brainstorm companion is running in this session's worktree: %w", opReadMockup, title, ErrRefused)
			return res
		}
		html, err := mockup.Fetch(context.Background(), look.c.Port)
		if err != nil {
			res.err = fmt.Errorf("%s %s: %v: %w", opReadMockup, title, err, ErrRefused)
			return res
		}
		res.page = mockup.Page{Screen: look.c.Screen, Changed: look.c.Changed, HTML: html}
		return res
	}
}

// deliverMockup records what a ReadMockup found; its Reply carries the page
// (deliverTracked).
func (m *Model) deliverMockup(res mockupResult) {
	applyMockup(res.inst, res.look)
}
```

(add `"time"` to its imports; drop `errors` if unused.) The refusal's text must read exactly "read the mockup of brainstorm: no brainstorm companion is running in this session's worktree": check how `m.refuse` and `ErrRefused` compose a message elsewhere (`precondition`'s `fmt.Errorf("%s %s: %s: %w"…)`, or a wrapper type whose `Error()` drops the sentinel's text) and build the job's error the same way, so the test's exact string holds.

In `core/hook_scan.go`:
- `hookScanResult` gains `mockup *mockupLook`.
- `hookScanJob`'s `job` gains `worktree string`, set on the loop: `jobs = append(jobs, job{inst: inst, req: req, worktree: inst.GetWorktreePath()})`; in the closure, after `ScanHooks`:

```go
			r := hookScanResult{instance: j.inst, result: res, err: err}
			if err == nil && handsBack(res.Events) {
				r.mockup = lookForMockup(j.worktree)
			}
			results = append(results, r)
```

- `hookFace` gains `mockup string` and `mockupChanged time.Time`, read in `hookFaceOf` with `f.mockup, f.mockupChanged = inst.Mockup()`, so a found or cleared screen counts as moved.
- In `deliverHookScan`, inside the `else` branch after `r.instance.ApplyHookScan(r.result)`:

```go
			if r.mockup != nil {
				applyMockup(r.instance, r.mockup)
			}
```

- [ ] **Step 8: The request's wiring**, by `docs/claude/adding-a-core-method.md`:
- `core/iface.go`, after `ReadDiff`: `ReadMockup(id InstanceID, req ReqID)`, and extend the comment above `ReadTurns` to "A session's conversation, its worktree's diff and its brainstorm companion's page, read for the client that asks: the Reply's Turns (transcript.Read), Diff (Instance.ReadPatch) and Mockup (mockup.Fetch)."
- `core/loop_core.go`: `func (l *Loop) ReadMockup(id InstanceID, req ReqID) { l.do(func(m *Model) { m.ReadMockup(id, req) }) }`
- `core/events.go`: `Reply` gains `Mockup mockup.Page` after `Diff`, and the comment above it adds "Mockup ReadMockup's page".
- `core/events_json.go`: `replyJSON` gains `Mockup mockup.Page`; `MarshalJSON` and `UnmarshalJSON` copy `Mockup: r.Mockup` / `Mockup: j.Mockup`.
- `core/requests.go`: `opReadMockup requestOp = "read the mockup of"`; a `precondition` case:

```go
	case opReadMockup:
		if inst.IsWorkspaceTerminal {
			why = "not allowed on a workspace terminal"
		}
```

  in `deliverTracked`'s switch `case mockupResult: r.Mockup = res.page`; in `outcome` `case mockupResult: return r.err, nil`.
- `core/model.go`'s `Deliver`: `case mockupResult: m.deliverMockup(msg)`.
- `go generate ./core/rpc` and `go test ./core/rpc -run TestProtocolReference -update`.

- [ ] **Step 9: Run the model's tests**

Run: `CGO_ENABLED=0 go test ./session/ ./core/... ./internal/web/...`
Expected: PASS, including `TestWire_EveryFieldSurvives` (it fills `Reply.Mockup` and `InstanceView.Mockup`), `TestMethods_CoverCore`, `TestGenerated_IsFresh`, `TestLoopForwardsEachMethodToItsNamesake`, `TestCoreIsValueTyped`, `TestProtocolReference`. `internal/web` compiles against the new `Core` method through the rpc client.

- [ ] **Step 10: Race and commit**

Run: `CC=clang CGO_ENABLED=1 go test -race ./session/ ./core/`

```bash
git add -- session/instance.go session/instance_mockup.go session/instance_test.go core/mockup.go core/mockup_test.go core/view.go core/views.go core/hook_scan.go core/iface.go core/loop_core.go core/events.go core/events_json.go core/requests.go core/requests_test.go core/model.go core/rpc/methods_gen.go docs/specs/protocol.md
git commit -m "feat(core): find a session's brainstorm companion and read its page" -- <the same paths>
```

---

### Task 3: The web layer: the route, the JSON, the push link

**Files:**
- Create: `internal/web/mockup.go`, `internal/web/mockup_test.go`
- Modify: `internal/web/reads.go`, `internal/web/api.go`, `internal/web/fleet.go`, `internal/web/triggers.go`, `internal/web/api_test.go` (`apiAnswer` gains `header`), `internal/web/fleet_test.go`, `internal/web/triggers_test.go`, `internal/web/reads_test.go`, `internal/web/CLAUDE.md`, `internal/web/README.md`, `docs/claude/web-layer.md`

- [ ] **Step 1: Let a test read headers.** In `internal/web/api_test.go`, `apiAnswer` gains `header http.Header`, and `exchange` returns `apiAnswer{status: resp.StatusCode, header: resp.Header, body: data, err: err}`.

- [ ] **Step 2: Failing tests** (`internal/web/mockup_test.go`)

```go
package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const webCompanionHTML = `<html><body>Which layout?<script>window.brainstorm = {};</script></body></html>`

// withCompanion runs a companion on loopback and writes its live session
// dir into tree.
func withCompanion(t *testing.T, tree string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(webCompanionHTML))
	}))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)
	dir := filepath.Join(tree, ".superpowers", "brainstorm", "1-1")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "state"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "content"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "state", "server-info"), []byte(fmt.Sprintf(`{"port":%d}`, port)), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "content", "layout.html"), []byte("<h2>x</h2>"), 0o644))
}

func TestMockup_ServesThePageSandboxed(t *testing.T) {
	inst, _ := apiWithWorktree(t, "live")
	withCompanion(t, inst.GetWorktreePath())
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{inst}})

	got := env.readAnswer(fmt.Sprintf("/api/v1/sessions/%d/mockup?v=1", env.ids["live"]))
	require.Equal(t, http.StatusOK, got.status, "body: %s", got.body)
	assert.Equal(t, webCompanionHTML, string(got.body))
	assert.Equal(t, mockupCSP, got.header.Get("Content-Security-Policy"))
	assert.Equal(t, "sandbox allow-scripts; default-src 'none'; script-src 'unsafe-inline' https:; style-src 'unsafe-inline' https:; img-src data: https:; font-src data: https:; connect-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'self'", mockupCSP)
	assert.Equal(t, "text/html; charset=utf-8", got.header.Get("Content-Type"))
	assert.Equal(t, "no-store", got.header.Get("Cache-Control"))
	assert.Contains(t, env.logged(), loggedAction{device: "phone", name: "mockup", session: env.ids["live"], result: "200 ok"})
}

func TestMockup_ARefusalIsAPageThatSaysWhy(t *testing.T) {
	inst, _ := apiWithWorktree(t, "live")
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{inst}})

	got := env.readAnswer(fmt.Sprintf("/api/v1/sessions/%d/mockup", env.ids["live"]))
	assert.Equal(t, http.StatusConflict, got.status)
	assert.Equal(t, mockupCSP, got.header.Get("Content-Security-Policy"))
	assert.Equal(t, "text/html; charset=utf-8", got.header.Get("Content-Type"))
	assert.Contains(t, string(got.body), "read the mockup of live: no brainstorm companion is running in this session&#39;s worktree")
}

func TestMockupPage_EscapesTheMessage(t *testing.T) {
	assert.NotContains(t, mockupErrorPage(`<script>x</script>`), "<script>x")
}
```

In `TestAPI_ReadsOfAWorkspaceTerminalOrAnUnknownSessionAre404` (`internal/web/reads_test.go`) add the paths `fmt.Sprintf("/api/v1/sessions/%d/mockup", env.ids["term"])` and `"/api/v1/sessions/999999/mockup"`.

In `internal/web/fleet_test.go`, a case:

```go
func TestSessionOf_TheMockupIsItsScreenOrNull(t *testing.T) {
	at := time.Date(2026, 11, 4, 9, 30, 0, 0, time.UTC)
	got := sessionOf(1, core.InstanceView{Mockup: core.MockupView{Screen: "layout.html", Changed: at, Fresh: true}})
	require.NotNil(t, got.Mockup)
	assert.Equal(t, mockupJSON{Screen: "layout.html", Changed: "2026-11-04T09:30:00Z", Fresh: true}, *got.Mockup)
	assert.Nil(t, sessionOf(1, core.InstanceView{}).Mockup)
}
```

In `internal/web/triggers_test.go`, a case beside `TestPushWatch_FinishedNeedsAStartedSessionGoingFromRunningToReady` (read its fixture helpers first and build the views the same way):

```go
func TestPush_AFreshMockupLinksItsTab(t *testing.T) {
	fresh := core.MockupView{Screen: "layout.html", Fresh: true}
	for _, tc := range []struct {
		name string
		v    core.InstanceView
		url  string
	}{
		{"fresh", core.InstanceView{ID: 4, Mockup: fresh}, "/#s/4/mockup"},
		{"stale", core.InstanceView{ID: 4, Mockup: core.MockupView{Screen: "layout.html"}}, "/#s/4"},
		{"fresh but an ask is open", core.InstanceView{ID: 4, Mockup: fresh, Asks: []core.AskView{{}}}, "/#s/4"},
		{"none", core.InstanceView{ID: 4}, "/#s/4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := pushJob{Kind: pushFinished, Session: 4, Mockup: opensMockup(tc.v)}.payload()
			require.NoError(t, err)
			var p pushPayload
			require.NoError(t, json.Unmarshal(raw, &p))
			assert.Equal(t, tc.url, p.URL)
		})
	}
}
```

and in the observe tests one assertion that a Running → Ready transition of a view with a fresh mockup yields a job with `Mockup: true`.

- [ ] **Step 3: Run, see them fail** — `CGO_ENABLED=0 go test ./internal/web/ -run 'Mockup|SessionOf_TheMockup|Push_AFresh|ReadsOfAWorkspace'`.

- [ ] **Step 4: Implement.**

`internal/web/reads.go`: split `read`:

```go
// read makes the read request send names and waits for its Reply, then
// stops tracking the request (reqs.drop). It answers a failure as JSON
// (refuse) and otherwise returns the Reply with true.
func (l *Layer) read(w http.ResponseWriter, r *http.Request, a actionLog, send func(req core.ReqID)) (core.Reply, bool) {
	return l.readAnswering(w, r, a, send, l.refuse)
}

// readAnswering is read with the failures answered by fail: the model's
// refusal with its status, 504 when the model did not answer within
// replyWait.
func (l *Layer) readAnswering(w http.ResponseWriter, r *http.Request, a actionLog, send func(req core.ReqID), fail func(http.ResponseWriter, actionLog, int, string)) (core.Reply, bool) {
	req := l.reqs.next()
	defer l.reqs.drop(req)
	send(req)
	reply, _, done := l.reqs.wait(r.Context(), req, a.device)
	switch {
	case !done:
		fail(w, a, http.StatusGatewayTimeout, "loom did not answer in time")
	case reply.Err != nil:
		fail(w, a, modelStatus(reply.Err), reply.Err.Error())
	default:
		return reply, true
	}
	return core.Reply{}, false
}
```

`internal/web/mockup.go`:

```go
package web

import (
	"fmt"
	"html"
	"net/http"
)

// The mockup route: the page a session's brainstorm companion serves now
// (core.ReadMockup), for the app's iframe. It is the only route that
// replaces guard's Content-Security-Policy, and only with mockupCSP: the
// page's scripts run in an opaque origin, so they reach neither the app nor
// /api/v1, and connect nowhere.

// mockupCSP sandboxes the companion's page: inline scripts and styles run,
// https: resources load, and nothing else.
const mockupCSP = "sandbox allow-scripts; default-src 'none'; script-src 'unsafe-inline' https:; style-src 'unsafe-inline' https:; img-src data: https:; font-src data: https:; connect-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'self'"

// mockupErrorHTML is the frame's page when the read fails, in the app's
// colours; %s is the escaped reason.
const mockupErrorHTML = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><style>body{margin:0;padding:24px 16px;background:#2e2e2e;color:#a0a0a0;font:14px/1.5 ui-monospace,Menlo,monospace}</style></head><body><p>%s</p></body></html>`

// mockupErrorPage is the page that says why a read failed.
func mockupErrorPage(msg string) string {
	return fmt.Sprintf(mockupErrorHTML, html.EscapeString(msg))
}

// writePage answers body as a sandboxed HTML page.
func writePage(w http.ResponseWriter, status int, body string) {
	h := w.Header()
	h.Set("Content-Security-Policy", mockupCSP)
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// refusePage records a failed read and answers its page.
func (l *Layer) refusePage(w http.ResponseWriter, a actionLog, status int, msg string) {
	l.record(a, status, msg)
	writePage(w, status, mockupErrorPage(msg))
}

// readMockup answers GET /api/v1/sessions/{id}/mockup: the companion's page,
// or a page that says why there is none. The HTML is never logged.
func (l *Layer) readMockup(w http.ResponseWriter, r *http.Request, device string) {
	a := actionLog{device: device, name: "mockup"}
	id, ok := l.readSession(w, r, &a)
	if !ok {
		return
	}
	reply, ok := l.readAnswering(w, r, a, func(req core.ReqID) { l.client.ReadMockup(id, req) }, l.refusePage)
	if !ok {
		return
	}
	l.record(a, http.StatusOK, "ok")
	writePage(w, http.StatusOK, reply.Mockup.HTML)
}
```

(import `core`.) `internal/web/api.go`: `m.Handle("GET /api/v1/sessions/{id}/mockup", l.authed(l.readMockup))` after the diff route.

`internal/web/fleet.go`:

```go
// mockupJSON is the screen a session's brainstorm companion serves (GET
// …/mockup reads its page); Changed is RFC 3339.
type mockupJSON struct {
	Screen  string `json:"screen"`
	Changed string `json:"changed"`
	Fresh   bool   `json:"fresh"`
}
```

`sessionJSON` gains, after `Claude`:

```go
	// Mockup is the session's brainstorm companion's screen, null for none.
	Mockup *mockupJSON `json:"mockup"`
```

and `sessionOf`:

```go
	if v.Mockup.Screen != "" {
		s.Mockup = &mockupJSON{Screen: v.Mockup.Screen, Changed: stamp(v.Mockup.Changed), Fresh: v.Mockup.Fresh}
	}
```

`internal/web/triggers.go`: `pushJob` gains `Mockup bool // the push opens the session's mockup tab`; `payload`'s URL:

```go
	url := fmt.Sprintf("/#s/%d", j.Session)
	if j.Mockup {
		url += "/mockup"
	}
```

the two `pushJob{…}` literals in `observe` add `Mockup: opensMockup(v)`, and:

```go
// opensMockup reports whether a push for v opens its mockup tab: the
// companion's screen changed in the session's latest turn and no dialog
// waits on the session view (the app's fleet row follows the same rule).
func opensMockup(v core.InstanceView) bool {
	return v.Mockup.Screen != "" && v.Mockup.Fresh && len(v.Asks) == 0
}
```

Check `TestPushJob_PayloadIsTheContractsJSON` still passes (its job has no mockup).

- [ ] **Step 5: Run** — `CGO_ENABLED=0 go test ./internal/web/` then `CC=clang CGO_ENABLED=1 go test -race ./internal/web/`. Expected: PASS.

- [ ] **Step 6: Docs.**
- `internal/web/CLAUDE.md`, a rule after the `guard` rule: "**Replace `guard`'s Content-Security-Policy only on the mockup route (`internal/web/mockup.go`), only with `mockupCSP`, and answer that route's failures with `writePage`.** The companion's page is HTML a session wrote; without `sandbox` its scripts would run on the app's origin and call `/api/v1` with the device cookie, and a JSON error would show as raw text in the app's frame. **Enforced** by `TestMockup_ServesThePageSandboxed` and `TestMockup_ARefusalIsAPageThatSaysWhy`."
- `internal/web/README.md`: the route in the API table (`GET /api/v1/sessions/{id}/mockup` — the session's brainstorm companion's page, sandboxed; a failure is an HTML page), `mockup.go` in the Files table, and `mockup` in the session JSON's description.
- `docs/claude/web-layer.md` Traps: "**The mockup route is an iframe's page, not JSON.** It is the one route with its own CSP (`mockupCSP`) and HTML failures (`writePage`); the app learns that a companion stopped from `s.mockup` going null, not from the frame."
- Run `go run ./tools/claudemd`.

- [ ] **Step 7: Commit**

```bash
git add -- internal/web/mockup.go internal/web/mockup_test.go internal/web/reads.go internal/web/reads_test.go internal/web/api.go internal/web/api_test.go internal/web/fleet.go internal/web/fleet_test.go internal/web/triggers.go internal/web/triggers_test.go internal/web/CLAUDE.md internal/web/README.md docs/claude/web-layer.md
git commit -m "feat(web): serve a session's brainstorm mockup sandboxed and link pushes to it" -- <the same paths>
```

---

### Task 4: The app: the tab, the screen, full screen, the fleet link; webshot

**Files:**
- Create: `internal/web/static/js/mockup.mjs`, `tools/webshot/fixture/mockup.html`
- Modify: `internal/web/static/js/app.mjs`, `screens.mjs`, `chrome.mjs`, `session.mjs`, `fleet.mjs`, `internal/web/static/app.css`, `tools/webshot/main.go`, `tools/webshot/canned.go`, `tools/webshot/fixture/snapshot.json`, `tools/webshot/webshot_test.go`, `internal/web/webshot_fixture_test.go`

- [ ] **Step 1: The route.** `parseRoute` in `app.mjs`:

```js
// parseRoute reads the location's hash: "" is the fleet, "#s/<id>" a session,
// "#s/<id>/turns" its conversation, "#s/<id>/diff" its changes,
// "#s/<id>/mockup" its brainstorm mockup and "#s/<id>/mockup/full" that alone,
// "#new", "#settings", "#pair"; anything else is the fleet. The session's id
// is a number, as in the JSON.
export function parseRoute(hash) {
  const h = (hash || "").replace(/^#\/?/, "");
  if (h === "new") return { name: "new" };
  if (h === "settings") return { name: "settings" };
  if (h === "pair") return { name: "pair" };
  const m = h.match(/^s\/(\d+)(?:\/(turns|diff|mockup))?(\/full)?$/);
  if (m && (!m[3] || m[2] === "mockup")) {
    const name = { turns: "turns", diff: "changes", mockup: "mockup" }[m[2]] || "session";
    return m[3] ? { name, id: Number(m[1]), full: true } : { name, id: Number(m[1]) };
  }
  return { name: "fleet" };
}
```

and `const chromed = Boolean(snap) && route.name !== "pair" && !route.full;`. `screens.mjs`: `import { Mockup } from "./mockup.mjs";` and `mockup: { Screen: Mockup, Head: SessionHead },`.

- [ ] **Step 2: The tab.** `chrome.mjs`'s `SessionTabs({ s, current, conversation, changes })`, after the changes tab:

```js
  if (s.mockup || current === "mockup") {
    tabs.push({ key: "mockup", label: "mockup", href: base + "/mockup", fresh: Boolean(s.mockup && s.mockup.fresh) });
  }
```

and in the tab's markup, after the diff span: `${t.fresh ? html`<span class="sc-tab-dot" aria-label="new"></span>` : null}`. `session.mjs`'s `SessionHead` passes nothing new (the tab reads `s.mockup`); update its comment to "(session, conversation, changes, mockup)".

- [ ] **Step 3: The fleet link.** In `fleet.mjs`:

```js
// opensMockup reports whether the session's row opens its mockup tab: the
// companion's screen changed in the latest turn and no dialog waits on the
// session view (the daemon's push follows the same rule, opensMockup in
// internal/web/triggers.go).
export function opensMockup(s) {
  return Boolean(s.mockup && s.mockup.fresh && !(s.asks && s.asks.length));
}
```

and the row's `href=${"#s/" + s.id + (opensMockup(s) ? "/mockup" : "")}`.

- [ ] **Step 4: The screen** (`internal/web/static/js/mockup.mjs`)

```js
// The mockup screen: the page the session's brainstorm companion serves now
// (GET /api/v1/sessions/{id}/mockup), in a sandboxed frame that the route's
// own CSP also sandboxes. A new screen at a turn's end reloads it (its src
// carries the screen's time); ↻ reads it again. ⤢ shows it alone
// (#s/<id>/mockup/full: no header, no reply box). It ends in the reply box,
// as the session's other views do.
import { html, useState } from "../lib/preact-htm.mjs";
import { ago, sessionOf } from "./fleet.mjs";
import { actionsFor } from "./session.mjs";
import { ReplyBox, useRun } from "./reply.mjs";

export function Mockup({ snap, stale, route, act, toast }) {
  const id = route.id;
  const s = sessionOf(snap, id);
  const { busy, run } = useRun(act, toast, id);
  const [again, setAgain] = useState(0);

  if (!s) {
    return html`<section class="sc-screen"><p class="sc-dim sc-empty">${snap ? "This session is gone." : "Loading…"}</p></section>`;
  }
  const mk = s.mockup;
  const base = "#s/" + s.id + "/mockup";
  const src = mk ? "/api/v1/sessions/" + s.id + "/mockup?v=" + encodeURIComponent(mk.changed) + "&r=" + again : "";
  const frame = mk
    ? html`<iframe class="sc-mockup-frame" key=${src} src=${src} sandbox="allow-scripts" title=${"Mockup: " + mk.screen} referrerpolicy="no-referrer"></iframe>`
    : html`<p class="sc-dim sc-empty">No mockup is showing; the companion may have stopped.</p>`;

  if (route.full) {
    return html`<section class="sc-mockup-full">
      ${frame}
      <a class="sc-mockup-done" href=${base}>✕ done</a>
    </section>`;
  }

  const can = actionsFor(s);
  const off = stale || busy;
  return html`
    <section class="sc-screen sc-mockup">
      <div class="sc-mockup-strip">
        <span class="sc-mockup-name">${mk ? mk.screen + " · " + ago(mk.changed) : "no mockup"}</span>
        <button class="sc-inline-btn" type="button" aria-label="Read the mockup again" onClick=${() => setAgain((n) => n + 1)}>↻</button>
        ${mk ? html`<a class="sc-inline-btn" href=${base + "/full"} aria-label="Full screen">⤢</a>` : null}
      </div>
      ${frame}
      ${can.send
        ? html`<${ReplyBox} snap=${snap} off=${off} run=${run} id=${s.id} />`
        : s.group === "paused"
          ? html`<p class="sc-dim sc-small">Resume the session to reply.</p>`
          : null}
    </section>
  `;
}
```

Check that `ago` takes an RFC 3339 string (it does for `s.since`) and that `sc-inline-btn` exists in `app.css` (the changes screen uses it); if `ago` returns an empty string for a far time, the strip still shows the name.

- [ ] **Step 5: The styles** (`app.css`). Add `.sc-mockup` to the rule `.sc-session, .sc-turns, .sc-changes { flex: 1; display: flex; flex-direction: column; padding-bottom: 0; }`, and:

```css
/* The mockup: the companion's page fills the column between the strip and
   the reply box, edge to edge; full screen it fills the screen inside the
   safe areas, with "done" over its top right. */
.sc-mockup-strip { display: flex; align-items: center; gap: 12px; min-height: 36px; font-size: 12px; color: var(--dim); }
.sc-mockup-name { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.sc-mockup-frame { flex: 1; min-height: 240px; width: calc(100% + 32px); margin: 0 -16px; border: 0; border-top: 1px solid var(--rule); background: var(--bg); }
.sc-mockup-full { position: fixed; inset: 0; display: flex; flex-direction: column; padding: env(safe-area-inset-top) 0 env(safe-area-inset-bottom); background: var(--bg); }
.sc-mockup-full .sc-mockup-frame { width: 100%; margin: 0; border-top: 0; }
.sc-mockup-done { position: absolute; top: calc(env(safe-area-inset-top) + 6px); right: 8px; padding: 6px 10px; font-size: 12px; color: var(--fg); background: var(--panel); border: 1px solid var(--border); border-radius: var(--radius); text-decoration: none; }
.sc-tab-dot { width: 6px; height: 6px; border-radius: 50%; background: var(--accent); }
```

Check `.sc-screen`'s horizontal padding is 16px (the frame's negative margins assume it) and adjust both numbers to match.

- [ ] **Step 6: Syntax** — `for f in internal/web/static/js/*.mjs; do node --check "$f"; done` and `CGO_ENABLED=0 go test ./internal/web/ -run 'TestStatic_'` (no inline script, no `style` attribute, every token).

- [ ] **Step 7: Webshot.**
- `tools/webshot/fixture/mockup.html`: a page in the companion's frame look (a full document with its own `<style>`, a heading "Which layout works better?", two option cards A and B, and `<script>window.brainstorm = {};</script>`), so the screenshot shows a realistic page.
- `tools/webshot/fixture/snapshot.json`: session 1 gains `"mockup": {"screen": "layout.html", "changed": "now-2m", "fresh": true}`; every other session `"mockup": null`.
- `tools/webshot/canned.go`: widen the embed to `//go:embed fixture/*.json fixture/*.html`; `apiRoutes` entries gain a `contentType` (`application/json` for the JSON ones) and a `raw bool`; add `{regexp.MustCompile(`^/api/v1/sessions/\d+/mockup$`), "mockup.html", "text/html; charset=utf-8", true}`; `api` reads a raw fixture with `fixtureFiles.ReadFile("fixture/" + name)` instead of `c.fixture` and sets the route's type.
- `tools/webshot/main.go`: `{"mockup", "#s/1/mockup"}` and `{"mockup-full", "#s/1/mockup/full"}` after `diff`.
- `tools/webshot/webshot_test.go`: `TestCanned_AnswersTheRoutesTheScreensReadAndNamesTheRest` also gets `/api/v1/sessions/1/mockup` (200, contains `window.brainstorm`).
- `internal/web/webshot_fixture_test.go`: decode only the `.json` fixtures, require `mockup.html` to exist and contain `window.brainstorm`, and expect 6 entries.

Run: `CGO_ENABLED=0 go test ./tools/webshot/ ./internal/web/` — PASS. Then render: `go run ./tools/webshot -out /tmp/claude-1000/webshot-mockup -screens mockup,mockup-full,fleet,session` and look at each PNG: the tab with its dot, the strip, the page edge to edge above the reply box; full screen with "done" under the island; the fleet row (session 1) unchanged in look.

- [ ] **Step 8: Commit**

```bash
git add -- internal/web/static/js/mockup.mjs internal/web/static/js/app.mjs internal/web/static/js/screens.mjs internal/web/static/js/chrome.mjs internal/web/static/js/session.mjs internal/web/static/js/fleet.mjs internal/web/static/app.css tools/webshot internal/web/webshot_fixture_test.go
git commit -m "feat(web): a mockup tab on the phone, with full screen" -- <the same paths>
```

---

### Task 5: Gates, the real thing, records

- [ ] **Step 1: Every gate** listed at the top, in order; golangci-lint is skipped locally (say so).
- [ ] **Step 2: A real companion through a dev daemon.** With the `loom-dev` skill (`go run ./tools/loomdev`, never `./loom`): start a sandbox daemon with the web on, make a session whose worktree runs a real companion (`start-server.sh --project-dir <worktree>` and a screen written into its `content/`), drop a `Stop` hook event or let a real Claude end its turn, and check through the sandbox's web port with the paired cookie: the snapshot's `mockup`, `GET …/mockup` returning the page with `mockupCSP`, and the page in headless Chromium's frame (webshot's browser, pointed at the sandbox). If the sandbox can't host the web layer, record what was not run.
- [ ] **Step 3: docs/claude.** If the build taught something a later session needs (a trap of the iframe, the hook scan's look), add it to `docs/claude/web-layer.md` or a new guide listed in `docs/claude/INDEX.md`, and commit it with explicit paths.
- [ ] **Step 4: The iPhone checklist (the owner's):** open a session with a fresh mockup from its finished push; the tab and its dot; ⤢ and ✕ done; reply from the tab; a stopped companion's empty state; a permission prompt's push opening the session view.
