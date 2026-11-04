package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// fixtureFiles is the canned loom's data, in the JSON the web layer serves.
// A string "now", "now-2m" or "now+1h40m" (a Go duration) is a time relative
// to the moment it is served, so ages and countdowns read the same on every
// run.
//
//go:embed fixture/*.json fixture/*.html
var fixtureFiles embed.FS

// cannedBuild is the build the canned loom reports and fills into
// index.html: the two must match, or the app reloads to upgrade its shell.
const cannedBuild = "webshot"

var staticTypes = map[string]string{
	".html":        "text/html; charset=utf-8",
	".css":         "text/css; charset=utf-8",
	".js":          "text/javascript; charset=utf-8",
	".mjs":         "text/javascript; charset=utf-8",
	".webmanifest": "application/manifest+json",
	".png":         "image/png",
}

// cannedLoom answers the phone app as the daemon's web layer would, from the
// app's files on disk (so an edit shows without a build) and the fixture.
// It holds no session state and every action is refused: the tool draws
// screens, it does not drive them.
type cannedLoom struct {
	static string
	now    func() time.Time

	mu     sync.Mutex
	misses []string // API routes the fixture has no answer for
}

func newCannedLoom(static string) (*cannedLoom, error) {
	if _, err := os.Stat(filepath.Join(static, "index.html")); err != nil {
		return nil, fmt.Errorf("the app's files: %w (run from the repo root, or pass -static)", err)
	}
	return &cannedLoom{static: static, now: time.Now}, nil
}

func (c *cannedLoom) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch {
	case p == "/api/v1/stream" && r.Method == http.MethodGet:
		c.stream(w, r)
	case strings.HasPrefix(p, "/api/"):
		c.api(w, r)
	default:
		c.file(w, p)
	}
}

// mockupCSP is the policy the web layer serves a mockup under
// (internal/web/mockup.go's mockupCSP, which TestWebshotFixture_IsTheJSONTheLayerServes
// holds this copy to), so a screenshot shows the frame a phone would.
const mockupCSP = "sandbox allow-scripts; default-src 'none'; script-src 'unsafe-inline' https:; style-src 'unsafe-inline' https:; img-src data: https:; font-src data: https:; connect-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'self'"

// apiRoutes maps each GET route the screens read on load to its fixture: a
// JSON one with its relative times filled, or the mockup's page as it is.
var apiRoutes = []struct {
	pattern *regexp.Regexp
	fixture string
}{
	{regexp.MustCompile(`^/api/v1/sessions/\d+/message$`), "message.json"},
	{regexp.MustCompile(`^/api/v1/sessions/\d+/turns$`), "turns.json"},
	{regexp.MustCompile(`^/api/v1/sessions/\d+/diff$`), "diff.json"},
	{regexp.MustCompile(`^/api/v1/sessions/\d+/mockup$`), "mockup.html"},
	{regexp.MustCompile(`^/api/v1/launch-defaults$`), "launch-defaults.json"},
}

func (c *cannedLoom) api(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		for _, rt := range apiRoutes {
			if rt.pattern.MatchString(r.URL.Path) {
				if strings.HasSuffix(rt.fixture, ".html") {
					page, err := fixtureFiles.ReadFile("fixture/" + rt.fixture)
					if err != nil {
						http.Error(w, err.Error(), http.StatusInternalServerError)
						return
					}
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					w.Header().Set("Content-Security-Policy", mockupCSP)
					_, _ = w.Write(page)
					return
				}
				body, err := c.fixture(rt.fixture)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(body)
				return
			}
		}
	}
	c.mu.Lock()
	c.misses = append(c.misses, r.Method+" "+r.URL.Path)
	c.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	_, _ = io.WriteString(w, `{"error":"the canned loom has no answer for this"}`)
}

// takeMisses returns the API routes asked for since the last call.
func (c *cannedLoom) takeMisses() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := c.misses
	c.misses = nil
	return m
}

// stream sends the snapshot, then a comment every few seconds until the app
// goes, as the hub keeps a stream open.
func (c *cannedLoom) stream(w http.ResponseWriter, r *http.Request) {
	snap, err := c.fixture("snapshot.json")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "no streaming", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", snap)
	fl.Flush()
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			_, _ = io.WriteString(w, ": ping\n\n")
			fl.Flush()
		}
	}
}

// fixture reads a fixture, fills its relative times in, and compacts it to
// one line (a server-sent event's data is one line).
func (c *cannedLoom) fixture(name string) ([]byte, error) {
	raw, err := fixtureFiles.ReadFile("fixture/" + name)
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("fixture %s: %w", name, err)
	}
	v, err = fillTimes(v, c.now())
	if err != nil {
		return nil, fmt.Errorf("fixture %s: %w", name, err)
	}
	out, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("fixture %s: %w", name, err)
	}
	return out, nil
}

var relTime = regexp.MustCompile(`^now(?:([+-])(\S+))?$`)

// fillTimes replaces every string of the form now[±duration] in v with the
// RFC 3339 time it names, counted from now.
func fillTimes(v any, now time.Time) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			y, err := fillTimes(x, now)
			if err != nil {
				return nil, err
			}
			t[k] = y
		}
	case []any:
		for i, x := range t {
			y, err := fillTimes(x, now)
			if err != nil {
				return nil, err
			}
			t[i] = y
		}
	case string:
		m := relTime.FindStringSubmatch(t)
		if m == nil {
			return t, nil
		}
		at := now
		if m[1] != "" {
			d, err := time.ParseDuration(m[2])
			if err != nil {
				return nil, fmt.Errorf("time %q: %w", t, err)
			}
			if m[1] == "-" {
				d = -d
			}
			at = now.Add(d)
		}
		return at.UTC().Format(time.RFC3339), nil
	}
	return v, nil
}

// file serves one of the app's files, with the build filled into
// index.html as the web layer does. The worker's placeholders stay: the
// browser runs with service workers bypassed.
func (c *cannedLoom) file(w http.ResponseWriter, p string) {
	if p == "/" {
		p = "/index.html"
	}
	clean := path.Clean(p)
	ctype, ok := staticTypes[path.Ext(clean)]
	if !ok || strings.Contains(clean, "..") {
		http.NotFound(w, nil)
		return
	}
	body, err := os.ReadFile(filepath.Join(c.static, filepath.FromSlash(strings.TrimPrefix(clean, "/"))))
	if err != nil {
		http.NotFound(w, nil)
		return
	}
	if clean == "/index.html" {
		body = bytes.ReplaceAll(body, []byte("__LOOM_BUILD__"), []byte(cannedBuild))
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(body)
}
