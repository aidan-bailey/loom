package web

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"math/rand"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// firstLoadBudget is the gzipped bytes a first load may cost (spec §15). The
// flow, job and mockup screens are lazy chunks that index.html names nowhere,
// so they do not count.
const firstLoadBudget = 250_000

var (
	firstLoadScriptTag = regexp.MustCompile(`(?is)<script\b[^>]*>`)
	firstLoadLinkTag   = regexp.MustCompile(`(?is)<link\b[^>]*>`)
)

// tagAttr reads one attribute of a whole tag, in any position, quoted or bare.
// It reports "" when the tag does not carry it.
func tagAttr(tag, name string) string {
	re := regexp.MustCompile(`(?is)\s` + regexp.QuoteMeta(name) + `\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+))`)
	m := re.FindStringSubmatch(tag)
	if m == nil {
		return ""
	}
	return m[1] + m[2] + m[3]
}

// firstLoadFiles lists the URL paths a cold load of index fetches as JavaScript,
// in document order and without repeats: the entry script first, then every
// modulepreload link.
func firstLoadFiles(index string) ([]string, error) {
	entry := ""
	for _, tag := range firstLoadScriptTag.FindAllString(index, -1) {
		if tagAttr(tag, "type") == "module" && tagAttr(tag, "src") != "" {
			entry = tagAttr(tag, "src")
			break
		}
	}
	if entry == "" {
		return nil, fmt.Errorf("index.html names no entry script")
	}

	files := []string{entry}
	for _, tag := range firstLoadLinkTag.FindAllString(index, -1) {
		href := tagAttr(tag, "href")
		if href == "" || !slices.Contains(strings.Fields(tagAttr(tag, "rel")), "modulepreload") {
			continue
		}
		if !slices.Contains(files, href) {
			files = append(files, href)
		}
	}
	return files, nil
}

// checkFirstLoad sums the gzipped size of every file a cold load of
// /index.html fetches as JavaScript and fails when the sum is over budget.
func checkFirstLoad(assets map[string]staticAsset, budget int) error {
	index, ok := assets["/index.html"]
	if !ok {
		return fmt.Errorf("the bundle has no /index.html")
	}
	files, err := firstLoadFiles(string(index.body))
	if err != nil {
		return err
	}

	total := 0
	var report strings.Builder
	for _, url := range files {
		asset, ok := assets[url]
		if !ok {
			return fmt.Errorf("index.html names %s, which the bundle lacks", url)
		}
		n := gzipLen(asset.body)
		total += n
		fmt.Fprintf(&report, "\n  %s: %d", url, n)
	}
	if total > budget {
		return fmt.Errorf("first load is %d bytes gzipped, over the %d budget:%s", total, budget, report.String())
	}
	return nil
}

// gzipLen is the size of body gzipped at the best compression.
func gzipLen(body []byte) int {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		panic(err)
	}
	if _, err := zw.Write(body); err != nil {
		panic(err)
	}
	if err := zw.Close(); err != nil {
		panic(err)
	}
	return buf.Len()
}

func TestDist_FirstLoadIsWithinBudget(t *testing.T) {
	assets := nextStaticAssets(t)
	require.Contains(t, assets, "/index.html")
	files, err := firstLoadFiles(string(assets["/index.html"].body))
	require.NoError(t, err)
	require.NotEmpty(t, files, "the committed index.html names no file for the first load")
	require.NoError(t, checkFirstLoad(assets, firstLoadBudget))
}

func TestFirstLoadFiles_NamesTheEntryAndEveryPreload(t *testing.T) {
	page := `<!doctype html>
<html>
<head>
<link rel="modulepreload" crossorigin href="/assets/vendor-b.js">
<link crossorigin rel="modulepreload" href="/assets/query-c.js">
<link rel="stylesheet" crossorigin href="/assets/index-a.css">
<link rel="modulepreload" crossorigin href="/assets/vendor-b.js">
<script>window.lazy = "/assets/lazy-d.js";</script>
<script type="module" crossorigin src="/assets/index-a.js"></script>
</head>
<body><div id="app"></div></body>
</html>`

	files, err := firstLoadFiles(page)
	require.NoError(t, err)
	require.Equal(t, []string{"/assets/index-a.js", "/assets/vendor-b.js", "/assets/query-c.js"}, files)
}

func TestFirstLoadFiles_NoEntryScriptIsAnError(t *testing.T) {
	page := `<head><link rel="modulepreload" crossorigin href="/assets/vendor-b.js"></head>`
	_, err := firstLoadFiles(page)
	require.ErrorContains(t, err, "no entry script")
}

// budgetRandom draws n incompressible bytes from r, so gzip cannot shrink them.
func budgetRandom(r *rand.Rand, n int) []byte {
	b := make([]byte, n)
	r.Read(b)
	return b
}

func TestCheckFirstLoad_FailsOverBudgetAndNamesTheFiles(t *testing.T) {
	const page = `<!doctype html>
<script type="module" crossorigin src="/assets/index-a.js"></script>
<link rel="modulepreload" crossorigin href="/assets/vendor-b.js">`
	r := rand.New(rand.NewSource(1))
	assets := map[string]staticAsset{
		"/index.html":         {body: []byte(page), ctype: "text/html; charset=utf-8"},
		"/assets/index-a.js":  {body: budgetRandom(r, 4000), ctype: "text/javascript; charset=utf-8"},
		"/assets/vendor-b.js": {body: budgetRandom(r, 4000), ctype: "text/javascript; charset=utf-8"},
	}

	err := checkFirstLoad(assets, 6000)
	require.ErrorContains(t, err, "over the 6000 budget")
	require.ErrorContains(t, err, "/assets/index-a.js")
	require.ErrorContains(t, err, "/assets/vendor-b.js")
	require.NoError(t, checkFirstLoad(assets, 100_000))

	// A lazy chunk that index.html never names is outside the sum either way.
	assets["/assets/lazy-d.js"] = staticAsset{body: budgetRandom(r, 500_000), ctype: "text/javascript; charset=utf-8"}
	err = checkFirstLoad(assets, 6000)
	require.ErrorContains(t, err, "over the 6000 budget")
	require.NotContains(t, err.Error(), "/assets/lazy-d.js")
	require.NoError(t, checkFirstLoad(assets, 100_000))
}

func TestCheckFirstLoad_RefusesAMissingFile(t *testing.T) {
	const page = `<script type="module" src="/assets/index-a.js"></script>
<link rel="modulepreload" href="/assets/gone.js">`
	assets := map[string]staticAsset{
		"/index.html":        {body: []byte(page), ctype: "text/html; charset=utf-8"},
		"/assets/index-a.js": {body: []byte("export {};"), ctype: "text/javascript; charset=utf-8"},
	}
	require.ErrorContains(t, checkFirstLoad(assets, firstLoadBudget), "/assets/gone.js")

	delete(assets, "/index.html")
	require.ErrorContains(t, checkFirstLoad(assets, firstLoadBudget), "/index.html")
}

// checkLazyViews is whether the job and mockup screens are chunks of their own that index.html
// does not load: one file per name, named by neither its URL nor its file name in the page, and
// not among the first load's files.
func checkLazyViews(assets map[string]staticAsset) error {
	index, ok := assets["/index.html"]
	if !ok {
		return fmt.Errorf("the bundle has no /index.html")
	}
	page := string(index.body)
	files, err := firstLoadFiles(page)
	if err != nil {
		return err
	}
	for _, name := range []string{"JobBody", "MockupBody"} {
		re := regexp.MustCompile(`^/assets/` + regexp.QuoteMeta(name) + `-[^/]+\.js$`)
		var chunks []string
		for url := range assets {
			if re.MatchString(url) {
				chunks = append(chunks, url)
			}
		}
		slices.Sort(chunks)
		switch {
		case len(chunks) == 0:
			return fmt.Errorf("the bundle has no lazy chunk for %s: its panel is imported statically, or the build renamed it", name)
		case len(chunks) > 1:
			return fmt.Errorf("the bundle has more than one lazy chunk for %s: %s", name, strings.Join(chunks, ", "))
		}
		url := chunks[0]
		if strings.Contains(page, url) || strings.Contains(page, strings.TrimPrefix(url, "/assets/")) || slices.Contains(files, url) {
			return fmt.Errorf("index.html names the lazy chunk %s: it would load on every launch", url)
		}
	}
	return nil
}

func TestDist_LazyViewsAreChunksOfTheirOwn(t *testing.T) {
	require.NoError(t, checkLazyViews(nextStaticAssets(t)))
}

func TestCheckLazyViews_RefusesAMissingChunkAndAPreloadedOne(t *testing.T) {
	const entry = `<!doctype html><script type="module" crossorigin src="/assets/index-a.js"></script>`
	build := func(page string, files ...string) map[string]staticAsset {
		assets := map[string]staticAsset{}
		if page != "" {
			assets["/index.html"] = staticAsset{body: []byte(page), ctype: "text/html; charset=utf-8"}
		}
		for _, f := range files {
			assets[f] = staticAsset{body: []byte("export {};"), ctype: "text/javascript; charset=utf-8"}
		}
		return assets
	}
	all := []string{"/assets/index-a.js", "/assets/JobBody-b.js", "/assets/MockupBody-c.js"}

	for _, tc := range []struct {
		name   string
		assets map[string]staticAsset
		want   string
	}{
		{"both chunks apart from the page", build(entry, all...), ""},
		{"no mockup chunk", build(entry, "/assets/index-a.js", "/assets/JobBody-b.js"), "MockupBody"},
		{"no job chunk", build(entry, "/assets/index-a.js", "/assets/MockupBody-c.js"), "JobBody"},
		{"a second job chunk", build(entry, append([]string{"/assets/JobBody-z.js"}, all...)...), "more than one"},
		{"a preloaded chunk", build(entry+`<link rel="modulepreload" crossorigin href="/assets/JobBody-b.js">`, all...), "/assets/JobBody-b.js"},
		{"a chunk named in the page's text", build(entry+`<script>load("MockupBody-c.js")</script>`, all...), "MockupBody"},
		{"no index.html", build("", all...), "/index.html"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkLazyViews(tc.assets)
			if tc.want == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.want)
		})
	}
	require.ErrorContains(t, checkLazyViews(build(entry+`<link rel="modulepreload" crossorigin href="/assets/JobBody-b.js">`, all...)), "launch")
}
