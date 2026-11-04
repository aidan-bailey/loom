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
// server-info naming port (none when port is 0) changed at at, a
// server-stopped when stopped, and the screens, each a second newer than the
// one before.
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
	assert.True(t, c.Changed.Equal(at.Add(10*time.Minute+2*time.Second)), "the screen's mtime: %v", c.Changed)
}

func TestFind_NoMockup(t *testing.T) {
	at := time.Now().Add(-time.Hour)
	info := func(tree string) string {
		return filepath.Join(tree, ".superpowers", "brainstorm", "1", "state", "server-info")
	}
	for _, tc := range []struct {
		name  string
		build func(t *testing.T, tree string)
	}{
		{"no brainstorm dir", func(t *testing.T, tree string) {}},
		{"only a stopped companion", func(t *testing.T, tree string) { brainstormDir(t, tree, "1", 50001, true, at, "a.html") }},
		{"no server-info", func(t *testing.T, tree string) { brainstormDir(t, tree, "1", 0, false, at, "a.html") }},
		{"a garbled server-info", func(t *testing.T, tree string) {
			brainstormDir(t, tree, "1", 50001, false, at, "a.html")
			require.NoError(t, os.WriteFile(info(tree), []byte("{port"), 0o644))
		}},
		{"port 0", func(t *testing.T, tree string) {
			brainstormDir(t, tree, "1", 50001, false, at, "a.html")
			require.NoError(t, os.WriteFile(info(tree), []byte(`{"port":0}`), 0o644))
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

// TestFetch_NothingListeningIsAnError: a companion killed without its
// shutdown leaves a server-info naming a port nothing serves.
func TestFetch_NothingListeningIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	closed, err := strconv.Atoi(u.Port())
	require.NoError(t, err)
	srv.Close()
	_, err = Fetch(context.Background(), closed)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not answer")
}
