// Package mockup reads the brainstorming skill's visual companion from a
// session's worktree: Find says whether one runs there and which screen it
// shows, and Fetch reads the page it serves on 127.0.0.1. It touches only
// the filesystem and the loopback interface.
//
// The companion (superpowers' skills/brainstorming/scripts/server.cjs) keeps
// a session dir per run at <worktree>/.superpowers/brainstorm/<id>/, with
// its screens in content/ and, while it runs, state/server-info naming its
// port; on shutdown it deletes server-info and writes state/server-stopped.
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
// injects into each one, defines it.
const marker = "window.brainstorm"

// fetchTimeout bounds a Fetch; a var so a test can shorten it.
var fetchTimeout = 5 * time.Second

// Companion is a running companion found in a worktree.
type Companion struct {
	Port int
	// Screen is the newest content/*.html, a base name, and Changed its
	// mtime: the page the companion serves.
	Screen  string
	Changed time.Time
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
// (waiting*.html, "Continuing in terminal…"). err is set only for an I/O
// error other than not-exist.
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

// Fetch GETs http://127.0.0.1:<port>/ and returns the page. The host is
// fixed, so a server-info can name only a loopback port. It refuses a
// status but 200, a type but text/html, a body over 1 MiB and a body
// without the companion's marker (a stale server-info's port, taken by
// another process), with an error that names why and never carries the
// body.
func Fetch(ctx context.Context, port int) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(port)+"/", nil)
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
		return "", errors.New("the companion's page is over 1 MiB")
	}
	if !strings.Contains(string(body), marker) {
		return "", fmt.Errorf("port %d is not the brainstorm companion", port)
	}
	return string(body), nil
}
