package web

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io/fs"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/aidan-bailey/loom/log"
)

// staticFiles is the classic app: the shell, its modules, the stylesheet, the
// service worker, the manifest and the icons.
//
//go:embed static
var staticFiles embed.FS

// nextFiles is the new app: `pnpm run build` in internal/web/ui writes dist/,
// which is committed, and `pnpm run dist:check` fails when it is stale. `all:`
// keeps a file whose name starts with a dot or an underscore.
//
//go:embed all:dist
var nextFiles embed.FS

const (
	staticBuildMark = "__LOOM_BUILD__"
	staticShellMark = "__LOOM_SHELL__"
)

// staticContentTypes is every extension the app's files may have. A file of
// another extension fails the build of the assets, so a new kind of file is a
// decision here, never a guessed type.
var staticContentTypes = map[string]string{
	".html":        "text/html; charset=utf-8",
	".css":         "text/css; charset=utf-8",
	".js":          "text/javascript; charset=utf-8",
	".mjs":         "text/javascript; charset=utf-8",
	".webmanifest": "application/manifest+json",
	".png":         "image/png",
	".svg":         "image/svg+xml",
	".woff2":       "font/woff2",
	".txt":         "text/plain; charset=utf-8",
}

// staticAsset is one served file.
type staticAsset struct {
	body  []byte
	ctype string
}

func (a staticAsset) serve(w http.ResponseWriter, _ *http.Request) {
	h := w.Header()
	h.Set("Content-Type", a.ctype)
	h.Set("Cache-Control", "no-cache")
	h.Set("Content-Length", strconv.Itoa(len(a.body)))
	if _, err := w.Write(a.body); err != nil {
		log.For("web").Debug("web.static_not_delivered", "err", err)
	}
}

// buildStaticAssets reads every file of fsys into a map keyed by URL path. It
// fills the build ID into index.html and sw.js and the list of every file's
// URL (and "/") into sw.js, and serves index.html at "/" too. It fails closed:
// a missing placeholder, a missing index.html or sw.js, or a file of an
// unknown type is an error, so the worker's precache list cannot drift from
// the embedded files. Every file is served, but the list leaves out terminalOnly's.
func buildStaticAssets(fsys fs.FS, build string) (map[string]staticAsset, error) {
	assets := make(map[string]staticAsset)
	var urls []string
	err := fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ctype, ok := staticContentTypes[path.Ext(name)]
		if !ok {
			return fmt.Errorf("%s: no content type for %q", name, path.Ext(name))
		}
		body, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		if !terminalOnly("/" + name) {
			urls = append(urls, "/"+name)
		}
		assets["/"+name] = staticAsset{body: body, ctype: ctype}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read the app's files: %w", err)
	}

	shell, err := json.Marshal(append([]string{"/"}, urls...))
	if err != nil {
		return nil, fmt.Errorf("encode the shell list: %w", err)
	}
	buildJSON, err := json.Marshal(build)
	if err != nil {
		return nil, fmt.Errorf("encode the build: %w", err)
	}
	if err := fillStatic(assets, "/index.html", staticBuildMark, html.EscapeString(build)); err != nil {
		return nil, err
	}
	if err := fillStatic(assets, "/sw.js", `"`+staticBuildMark+`"`, string(buildJSON)); err != nil {
		return nil, err
	}
	if err := fillStatic(assets, "/sw.js", `["`+staticShellMark+`"]`, string(shell)); err != nil {
		return nil, err
	}
	assets["/"] = assets["/index.html"]
	return assets, nil
}

// terminalOnly reports whether url is a file only the terminal's document loads: the document
// itself and its own entry script and styles (built as assets/terminal-<hash>.*, with xterm.js
// in the script). The worker never precaches them, so an install does not download xterm.js for a
// page most devices never open; the page's files come from the network when it is opened.
func terminalOnly(url string) bool {
	return url == terminalPage || strings.HasPrefix(url, "/assets/terminal-")
}

// fillStatic replaces every old in the asset at url with repl, and errors when
// the asset or the placeholder is not there.
func fillStatic(assets map[string]staticAsset, url, old, repl string) error {
	a, ok := assets[url]
	if !ok {
		return errors.New("the app has no " + url)
	}
	if !bytes.Contains(a.body, []byte(old)) {
		return fmt.Errorf("%s has no %s to fill", url, old)
	}
	a.body = bytes.ReplaceAll(a.body, []byte(old), []byte(repl))
	assets[url] = a
	return nil
}

// appCookie is the device's choice of app: "next" asks for the new one, any
// other value or none for the classic one. The app sets it (it is not
// HttpOnly) and it holds no data.
const appCookie = "__Host-loom-ui"

// staticTree is one of the two embedded apps.
type staticTree int

const (
	treeClassic staticTree = iota
	treeNext
)

// shellFiles are the four URLs whose answer is the app's shell, which the
// request's tree picks.
var shellFiles = []string{"/", "/index.html", "/sw.js", "/manifest.webmanifest"}

// isShellFile reports whether url is one of shellFiles.
func isShellFile(url string) bool {
	return slices.Contains(shellFiles, url)
}

// treeOf is the tree a request is answered from. A shell file with a ui
// parameter takes it (next for the new app, any other value for the classic
// one), so a worker's own requests choose their tree whether or not they carry
// the cookie; otherwise the cookie decides, next for the new app and anything
// else, or none, for the classic one.
func treeOf(r *http.Request) staticTree {
	if isShellFile(r.URL.Path) {
		if q := r.URL.Query(); q.Has("ui") {
			if q.Get("ui") == "next" {
				return treeNext
			}
			return treeClassic
		}
	}
	if c, err := r.Cookie(appCookie); err == nil && c.Value == "next" {
		return treeNext
	}
	return treeClassic
}

// routeStatic serves the classic app and the new one: public, with no data in
// them. An embedded app that cannot be served is logged (once, by
// routeStaticFrom) and fails closed for its own requests only: the shell
// requests treeOf gives it answer 500, so a broken build is loud rather than a
// blank page, while the other app is served as normal.
func (l *Layer) routeStatic(m *http.ServeMux) {
	l.routeStaticFrom(m, subOrBroken(staticFiles, "static"), subOrBroken(nextFiles, "dist"))
}

// subOrBroken is the subtree of fsys at dir, or when fs.Sub fails an FS whose
// every open returns that error, so routeStaticFrom logs the cause once with
// the app's other build errors.
func subOrBroken(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		return brokenFS{err}
	}
	return sub
}

// brokenFS is an FS that cannot be opened.
type brokenFS struct{ err error }

func (b brokenFS) Open(string) (fs.File, error) { return nil, b.err }

// staticTreeBuild is one embedded app as routeStaticFrom built it: its assets,
// or why it cannot be served.
type staticTreeBuild struct {
	assets map[string]staticAsset
	err    error
}

// routeStaticFrom serves the two apps held in classic and next (the embedded
// ones, in production; a nil one is an app that could not be opened). Each URL
// a working tree holds is registered once and answered by serveTrees. A tree
// that cannot be built is logged and fails closed for its own requests only:
// the four shell URLs are registered so that a request treeOf gives it answers
// 500, the other tree serves as normal, and no fallback reaches the broken
// tree's files, because it has none.
func (l *Layer) routeStaticFrom(m *http.ServeMux, classic, next fs.FS) {
	var trees [2]staticTreeBuild
	broken := false
	for _, app := range []struct {
		tree staticTree
		name string
		fsys fs.FS
	}{{treeClassic, "classic", classic}, {treeNext, "new", next}} {
		var err error
		if app.fsys == nil {
			err = errors.New("its files cannot be opened")
		} else {
			trees[app.tree].assets, err = buildStaticAssets(app.fsys, l.build)
		}
		if err != nil {
			log.For("web").Error("web.static_unavailable", "app", app.name, "err", err)
			trees[app.tree] = staticTreeBuild{err: err}
			broken = true
		}
	}
	urls := make(map[string]bool)
	for _, tree := range trees {
		for url := range tree.assets {
			urls[url] = true
		}
	}
	if broken {
		for _, url := range shellFiles {
			urls[url] = true
		}
	}
	for url := range urls {
		pattern := "GET " + url
		if url == "/" {
			pattern = "GET /{$}"
		}
		m.HandleFunc(pattern, serveTrees(trees, url))
	}
}

// terminalPage is the one URL answered under terminalCSP: the terminal's document
// (internal/web/ui/terminal.html), whose script runs xterm.js.
const terminalPage = "/terminal.html"

// terminalCSP is the policy of terminalPage, and of no other path: the app's own, which is
// default-src 'self' with strict framing, except that style-src allows inline styles, because
// xterm.js appends <style> elements and sets style attributes at run time and the app's policy
// drops them. Scripts and connections stay under default-src 'self'. frame-ancestors is 'self' so
// the app's panel can frame the page (part 2), and no other origin can.
const terminalCSP = "default-src 'self'; style-src 'self' 'unsafe-inline'; frame-ancestors 'self'; base-uri 'none'; form-action 'none'"

// serveTrees answers url from the tree treeOf picks for the request, else from
// the other tree, so a page loaded just before a switch still gets its assets.
// A shell answer depends on the request's cookie, so it says so, the 500 of a
// broken tree included. A shell request whose tree is broken answers 500 and
// is never handed to the other tree: the device chose that app and is told it
// is down. A tree that failed to build holds no assets, so no other request
// reaches it. The answer for terminalPage replaces guard's policy with
// terminalCSP; every other URL keeps guard's.
func serveTrees(trees [2]staticTreeBuild, url string) http.HandlerFunc {
	shell := isShellFile(url)
	policy := ""
	if url == terminalPage {
		policy = terminalCSP
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if shell {
			w.Header().Add("Vary", "Cookie")
		}
		chosen := treeOf(r)
		if shell && trees[chosen].err != nil {
			http.Error(w, "the app's files are missing from this build", http.StatusInternalServerError)
			return
		}
		a, ok := trees[chosen].assets[url]
		if !ok {
			a, ok = trees[treeNext-chosen].assets[url]
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		if policy != "" {
			w.Header().Set("Content-Security-Policy", policy)
		}
		a.serve(w, r)
	}
}
