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
	"strconv"

	"github.com/aidan-bailey/loom/log"
)

// staticFiles is the app: the shell, its modules, the stylesheet, the service
// worker, the manifest and the icons.
//
//go:embed static
var staticFiles embed.FS

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
// the embedded files.
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
		urls = append(urls, "/"+name)
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

// routeStatic serves the app shell and its files: public, with no data in
// them. When the embedded app cannot be served it logs why and answers 500 on
// "/", so a broken build is loud rather than a blank page.
func (l *Layer) routeStatic(m *http.ServeMux) {
	sub, err := fs.Sub(staticFiles, "static")
	var assets map[string]staticAsset
	if err == nil {
		assets, err = buildStaticAssets(sub, l.build)
	}
	if err != nil {
		log.For("web").Error("web.static_unavailable", "err", err)
		m.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "the app's files are missing from this build", http.StatusInternalServerError)
		})
		return
	}
	for url, a := range assets {
		pattern := "GET " + url
		if url == "/" {
			pattern = "GET /{$}"
		}
		m.HandleFunc(pattern, a.serve)
	}
}
