package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"image/color"
	"image/png"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
)

func TestIcons_AreOpaqueDrawingsOfTheirSize(t *testing.T) {
	for _, size := range []int{180, 192, 512} {
		name := fmt.Sprintf("static/icons/icon-%d.png", size)
		f, err := os.Open(name)
		require.NoError(t, err, name)
		img, err := png.Decode(f)
		require.NoError(t, f.Close())
		require.NoError(t, err, name)

		b := img.Bounds()
		require.Equal(t, size, b.Dx(), name)
		require.Equal(t, size, b.Dy(), name)

		for _, p := range [][2]int{{0, 0}, {size - 1, 0}, {0, size - 1}, {size - 1, size - 1}} {
			_, _, _, a := img.At(p[0], p[1]).RGBA()
			require.Equal(t, uint32(0xffff), a, "%s corner %v is not opaque", name, p)
		}

		colours := map[color.Color]bool{}
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				colours[img.At(x, y)] = true
			}
		}
		require.GreaterOrEqual(t, len(colours), 3, "%s is a blank square", name)
	}
}

// The vendored module is htm 3.1.1's preact/standalone.module.js, byte for
// byte after a one-line header comment.
const (
	preactHTMURL    = "https://unpkg.com/htm@3.1.1/preact/standalone.module.js"
	preactHTMSize   = 13194
	preactHTMSHA256 = "72284e8e9079c87817145df1110f74e8a2aa040b2fc384922e18dfcb46fc1fd7"
)

func TestPreactHTM_IsThePinnedFile(t *testing.T) {
	raw, err := os.ReadFile("static/lib/preact-htm.mjs")
	require.NoError(t, err)

	header, body, ok := bytes.Cut(raw, []byte("\n"))
	require.True(t, ok, "the file is one line")
	require.True(t, bytes.HasPrefix(header, []byte("//")), "the first line is a comment")
	for _, want := range []string{"htm 3.1.1", preactHTMURL, preactHTMSHA256, "NOTICE.md"} {
		require.Contains(t, string(header), want)
	}

	require.Len(t, body, preactHTMSize)
	sum := sha256.Sum256(body)
	require.Equal(t, preactHTMSHA256, hex.EncodeToString(sum[:]))
}

func staticMux(t *testing.T, build string) *http.ServeMux {
	t.Helper()
	m := http.NewServeMux()
	(&Layer{build: build}).routeStatic(m)
	return m
}

func staticDo(h http.Handler, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

// staticAssets is the embedded app as routeStatic serves it.
func staticAssets(t *testing.T) map[string]staticAsset {
	t.Helper()
	sub, err := fs.Sub(staticFiles, "static")
	require.NoError(t, err)
	assets, err := buildStaticAssets(sub, "b-test")
	require.NoError(t, err)
	return assets
}

func TestStatic_ServesEachFileWithItsType(t *testing.T) {
	m := staticMux(t, "b-1")
	for _, tc := range []struct{ url, ctype string }{
		{"/", "text/html; charset=utf-8"},
		{"/index.html", "text/html; charset=utf-8"},
		{"/app.css", "text/css; charset=utf-8"},
		{"/sw.js", "text/javascript; charset=utf-8"},
		{"/manifest.webmanifest", "application/manifest+json"},
		{"/lib/preact-htm.mjs", "text/javascript; charset=utf-8"},
		{"/icons/icon-512.png", "image/png"},
	} {
		rec := staticDo(m, http.MethodGet, tc.url)
		require.Equal(t, http.StatusOK, rec.Code, tc.url)
		require.Equal(t, tc.ctype, rec.Header().Get("Content-Type"), tc.url)
		require.Equal(t, "no-cache", rec.Header().Get("Cache-Control"), tc.url)
		require.NotEmpty(t, rec.Body.Bytes(), tc.url)
	}
}

func TestStatic_RefusesWhatIsNotAFile(t *testing.T) {
	m := staticMux(t, "b-1")
	require.Equal(t, http.StatusMethodNotAllowed, staticDo(m, http.MethodPost, "/").Code)
	require.Equal(t, http.StatusMethodNotAllowed, staticDo(m, http.MethodPut, "/app.css").Code)
	for _, target := range []string{"/nope", "/static/index.html", "/js/", "/api/v1/anything"} {
		require.Equal(t, http.StatusNotFound, staticDo(m, http.MethodGet, target).Code, target)
	}
	// An unclean path is redirected by the mux, never served.
	require.NotEqual(t, http.StatusOK, staticDo(m, http.MethodGet, "/../static.go").Code)
}

func TestStatic_IndexCarriesTheBuild(t *testing.T) {
	build := `b-"1"<x>&'`
	m := staticMux(t, build)
	index := staticDo(m, http.MethodGet, "/").Body.String()

	require.NotContains(t, index, "__LOOM_")
	meta := regexp.MustCompile(`<meta name="loom-build" content="([^"]*)">`).FindStringSubmatch(index)
	require.NotNil(t, meta, "index.html lost its loom-build meta")
	require.Equal(t, build, html.UnescapeString(meta[1]))
	require.Equal(t, index, staticDo(m, http.MethodGet, "/index.html").Body.String())
}

func TestStatic_ServiceWorkerCarriesTheBuildAndTheShell(t *testing.T) {
	build := `b-"1"<x>&`
	sw := staticDo(staticMux(t, build), http.MethodGet, "/sw.js").Body.String()
	require.NotContains(t, sw, "__LOOM_")

	buildLine := regexp.MustCompile(`(?m)^const BUILD = (.*);$`).FindStringSubmatch(sw)
	require.NotNil(t, buildLine, "sw.js lost its BUILD line")
	var gotBuild string
	require.NoError(t, json.Unmarshal([]byte(buildLine[1]), &gotBuild))
	require.Equal(t, build, gotBuild)

	shellLine := regexp.MustCompile(`(?m)^const SHELL = (.*);$`).FindStringSubmatch(sw)
	require.NotNil(t, shellLine, "sw.js lost its SHELL line")
	var shell []string
	require.NoError(t, json.Unmarshal([]byte(shellLine[1]), &shell))

	want := []string{"/"}
	require.NoError(t, fs.WalkDir(staticFiles, "static", func(name string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			want = append(want, strings.TrimPrefix(name, "static"))
		}
		return err
	}))
	require.ElementsMatch(t, want, shell)
}

func TestStatic_BuildRefusesAnAppThatCannotBeServed(t *testing.T) {
	good := func() fstest.MapFS {
		return fstest.MapFS{
			"index.html": {Data: []byte(`<meta name="loom-build" content="__LOOM_BUILD__">`)},
			"sw.js":      {Data: []byte("const BUILD = \"__LOOM_BUILD__\";\nconst SHELL = [\"__LOOM_SHELL__\"];\n")},
		}
	}
	assets, err := buildStaticAssets(good(), "b-7")
	require.NoError(t, err)
	require.Contains(t, string(assets["/index.html"].body), `content="b-7"`)
	require.Contains(t, string(assets["/sw.js"].body), `const BUILD = "b-7";`)
	require.Contains(t, string(assets["/sw.js"].body), `const SHELL = ["/","/index.html","/sw.js"];`)

	for _, tc := range []struct {
		name string
		edit func(fstest.MapFS)
		want string
	}{
		{"no index", func(f fstest.MapFS) { delete(f, "index.html") }, "no /index.html"},
		{"index without its build", func(f fstest.MapFS) { f["index.html"] = &fstest.MapFile{Data: []byte("<html>")} }, "/index.html has no"},
		{"no service worker", func(f fstest.MapFS) { delete(f, "sw.js") }, "no /sw.js"},
		{"service worker without BUILD", func(f fstest.MapFS) {
			f["sw.js"] = &fstest.MapFile{Data: []byte("const SHELL = [\"__LOOM_SHELL__\"];\n")}
		}, "/sw.js has no"},
		{"service worker without SHELL", func(f fstest.MapFS) {
			f["sw.js"] = &fstest.MapFile{Data: []byte("const BUILD = \"__LOOM_BUILD__\";\n")}
		}, "/sw.js has no"},
		{"a file of an unknown type", func(f fstest.MapFS) { f["notes.txt"] = &fstest.MapFile{Data: []byte("x")} }, "no content type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := good()
			tc.edit(f)
			_, err := buildStaticAssets(f, "b-7")
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestStatic_ManifestInstallsAStandaloneApp(t *testing.T) {
	assets := staticAssets(t)
	var manifest struct {
		Name       string `json:"name"`
		StartURL   string `json:"start_url"`
		Scope      string `json:"scope"`
		Display    string `json:"display"`
		ThemeColor string `json:"theme_color"`
		Icons      []struct {
			Src   string `json:"src"`
			Sizes string `json:"sizes"`
			Type  string `json:"type"`
		} `json:"icons"`
	}
	require.NoError(t, json.Unmarshal(assets["/manifest.webmanifest"].body, &manifest))
	require.Equal(t, "Loom", manifest.Name)
	require.Equal(t, "/", manifest.StartURL)
	require.Equal(t, "/", manifest.Scope)
	require.Equal(t, "standalone", manifest.Display)
	require.NotEmpty(t, manifest.ThemeColor)

	sizes := map[string]bool{}
	for _, icon := range manifest.Icons {
		require.Contains(t, assets, icon.Src, "the manifest names an icon that is not embedded")
		require.Equal(t, "image/png", icon.Type)
		sizes[icon.Sizes] = true
	}
	require.Equal(t, map[string]bool{"180x180": true, "192x192": true, "512x512": true}, sizes)
}

// The skin is dark only (spec 2026-10-23, "Decisions"): the stylesheet has no
// light block, tells the browser so, and defines every token on :root; the
// shell names one theme colour.
func TestStatic_TheSkinIsDarkWithEveryToken(t *testing.T) {
	assets := staticAssets(t)
	css := string(assets["/app.css"].body)
	require.NotContains(t, css, "prefers-color-scheme", "no light block")
	start := strings.Index(css, ":root {")
	require.GreaterOrEqual(t, start, 0, "app.css has no :root block")
	root := css[start:]
	root = root[:strings.Index(root, "}")]
	require.Contains(t, root, "color-scheme: dark;")
	for _, token := range []string{"bg", "panel", "fg", "title", "dim", "faint", "border", "rule", "accent", "attention", "ok", "error", "workspace", "selection", "mono"} {
		require.Contains(t, root, "--"+token+":", ":root lacks --"+token)
	}
	index := string(assets["/index.html"].body)
	require.Equal(t, 1, strings.Count(index, `name="theme-color"`), "one theme colour")
	require.Contains(t, index, `<meta name="theme-color" content="#2e2e2e">`)
	require.Contains(t, index, `<meta name="color-scheme" content="dark">`)
}

// staticInlineMarkup lists what the Content-Security-Policy would block in
// markup: an inline script, a style attribute, an event-handler attribute.
func staticInlineMarkup(markup string) []string {
	var found []string
	for _, tag := range regexp.MustCompile(`(?is)<script\b[^>]*>`).FindAllString(markup, -1) {
		if !regexp.MustCompile(`(?i)\ssrc=`).MatchString(tag) {
			found = append(found, "inline script: "+tag)
		}
	}
	for _, attr := range regexp.MustCompile(`(?i)\s(style|on[a-z]+)\s*=`).FindAllString(markup, -1) {
		found = append(found, "attribute: "+strings.TrimSpace(attr))
	}
	return found
}

func TestStatic_InlineMarkupDetector(t *testing.T) {
	require.Empty(t, staticInlineMarkup(`<script type="module" src="/js/app.mjs"></script><p class="a">x</p>`))
	require.Len(t, staticInlineMarkup(`<script>alert(1)</script>`), 1)
	require.Len(t, staticInlineMarkup(`<p style="color: red">x</p>`), 1)
	require.Len(t, staticInlineMarkup(`<a onclick="go()">x</a>`), 1)
}

func TestStatic_IndexHasNoInlineScriptOrStyle(t *testing.T) {
	require.Empty(t, staticInlineMarkup(string(staticAssets(t)["/index.html"].body)))
}

// The layer's mux serves the app behind guard: the shell answers on "/" with
// the layer's build and the security headers, and a wrong Host is refused.
func TestMux_ServesTheAppBehindTheGuard(t *testing.T) {
	s := newWebStack(t)
	s.start(t)
	addr, _ := s.enable(t)

	resp := httpDo(t, addr, http.MethodGet, "/", "")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "text/html")
	require.Equal(t, contentSecurityPolicy, resp.Header.Get("Content-Security-Policy"), "routeStatic is registered behind guard")
	meta := regexp.MustCompile(`<meta name="loom-build" content="([^"]*)">`).FindStringSubmatch(bodyText(t, resp))
	require.NotNil(t, meta, "index.html lost its loom-build meta")
	require.Equal(t, "test build", html.UnescapeString(meta[1]), "the layer's build fills the shell")

	resp = httpDo(t, addr, http.MethodGet, "/app.css", "", func(r *http.Request) { r.Host = "evil.example" })
	require.Equal(t, http.StatusMisdirectedRequest, resp.StatusCode, "a wrong Host reaches no file")
}

func TestStatic_AnAppThatCannotBeServedAnswers500OnTheShell(t *testing.T) {
	m := http.NewServeMux()
	(&Layer{build: "b-7"}).routeStaticFrom(m, fstest.MapFS{"index.html": {Data: []byte("<html>")}})

	rec := staticDo(m, http.MethodGet, "/")
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Contains(t, rec.Body.String(), "missing from this build")
	require.Equal(t, http.StatusNotFound, staticDo(m, http.MethodGet, "/index.html").Code, "nothing else is served")
}

var (
	staticAttrRef   = regexp.MustCompile(`(?:src|href)="(/[^"]*)"`)
	staticImportRef = regexp.MustCompile(`(?:from|import)\s*\(?\s*"(\.{1,2}/[^"]+)"`)
	staticNamedRef  = regexp.MustCompile(`import\s*\{([^}]*)\}\s*from\s*"(\.{1,2}/[^"]+)"`)
	staticExportFn  = regexp.MustCompile(`export\s+(?:async\s+)?(?:function\*?|const|let|var|class)\s+([A-Za-z_$][\w$]*)`)
	staticExportSet = regexp.MustCompile(`export\s*\{([^}]*)\}`)
)

// staticModules lists the URLs of the app's modules, the vendored one included.
func staticModules(assets map[string]staticAsset) []string {
	var urls []string
	for url := range assets {
		if strings.HasSuffix(url, ".mjs") {
			urls = append(urls, url)
		}
	}
	return urls
}

// staticExports lists the names a module exports.
func staticExports(body []byte) map[string]bool {
	names := map[string]bool{}
	for _, m := range staticExportFn.FindAllSubmatch(body, -1) {
		names[string(m[1])] = true
	}
	for _, m := range staticExportSet.FindAllSubmatch(body, -1) {
		for _, item := range strings.Split(string(m[1]), ",") {
			parts := strings.Fields(item)
			if len(parts) > 0 {
				names[parts[len(parts)-1]] = true
			}
		}
	}
	return names
}

func TestStatic_EveryReferenceResolves(t *testing.T) {
	assets := staticAssets(t)

	for _, ref := range staticAttrRef.FindAllStringSubmatch(string(assets["/index.html"].body), -1) {
		require.Contains(t, assets, ref[1], "index.html references a file that is not embedded")
	}
	for _, url := range staticModules(assets) {
		for _, ref := range staticImportRef.FindAllStringSubmatch(string(assets[url].body), -1) {
			target := path.Join(path.Dir(url), ref[1])
			require.Contains(t, assets, target, "%s imports %s, which is not embedded", url, ref[1])
		}
	}
	require.Contains(t, assets, "/js/app.mjs")
}

func TestStatic_NamedImportsAreExported(t *testing.T) {
	assets := staticAssets(t)
	vendored := staticExports(assets["/lib/preact-htm.mjs"].body)
	require.True(t, vendored["useState"], "the export reader misses the vendored module's list")
	require.False(t, vendored["Fragment"])

	for _, url := range staticModules(assets) {
		for _, imp := range staticNamedRef.FindAllStringSubmatch(string(assets[url].body), -1) {
			target := path.Join(path.Dir(url), imp[2])
			exports := staticExports(assets[target].body)
			for _, item := range strings.Split(imp[1], ",") {
				parts := strings.Fields(item)
				if len(parts) == 0 {
					continue
				}
				require.True(t, exports[parts[0]], "%s imports %s, which %s does not export", url, parts[0], target)
			}
		}
	}
}

func TestStatic_ModulesSetNoStyleAttribute(t *testing.T) {
	assets := staticAssets(t)
	for _, url := range staticModules(assets) {
		if url == "/lib/preact-htm.mjs" {
			continue
		}
		require.Empty(t, regexp.MustCompile(`\sstyle=`).FindAllString(string(assets[url].body), -1),
			"%s sets a style attribute: the app uses classes", url)
	}
}

// A stray markdown fence copied into the stylesheet swallows the rule after
// it, and an unclosed brace swallows the rest.
func TestStatic_TheStylesheetHasNoFenceAndItsBracesBalance(t *testing.T) {
	raw, err := os.ReadFile("static/app.css")
	require.NoError(t, err)
	css := string(raw)

	require.NotContains(t, css, "```", "a markdown fence is in app.css")

	css = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(css, "")
	depth := 0
	for i, r := range css {
		switch r {
		case '{':
			depth++
		case '}':
			depth--
		}
		require.GreaterOrEqual(t, depth, 0, "app.css closes a brace it never opened, at byte %d", i)
	}
	require.Zero(t, depth, "app.css leaves a brace open")
}
