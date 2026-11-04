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

// TestMockup_ServesThePageSandboxed: the page is HTML a session wrote, so it
// runs under the sandbox policy (an opaque origin, no connections) and is
// never cached.
func TestMockup_ServesThePageSandboxed(t *testing.T) {
	inst, _ := apiWithWorktree(t, "live")
	withCompanion(t, inst.GetWorktreePath())
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{inst}})

	got := env.readAnswer(fmt.Sprintf("/api/v1/sessions/%d/mockup?v=1", env.ids["live"]))
	require.NoError(t, got.err)
	require.Equal(t, http.StatusOK, got.status, "body: %s", got.body)
	assert.Equal(t, webCompanionHTML, string(got.body))
	assert.Equal(t, "sandbox allow-scripts; default-src 'none'; script-src 'unsafe-inline' https:; style-src 'unsafe-inline' https:; img-src data: https:; font-src data: https:; connect-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'self'", got.header.Get("Content-Security-Policy"))
	assert.Equal(t, "text/html; charset=utf-8", got.header.Get("Content-Type"))
	assert.Equal(t, "no-store", got.header.Get("Cache-Control"))
	assert.Equal(t, "nosniff", got.header.Get("X-Content-Type-Options"), "guard's other headers stay")
	assert.Contains(t, env.logged(), loggedAction{device: "phone", name: "mockup", session: env.ids["live"], result: "200 ok"})
}

// TestMockup_ARefusalIsAPageThatSaysWhy: the route is only an iframe's src,
// so a failure is a page in the frame, sandboxed like the mockup.
func TestMockup_ARefusalIsAPageThatSaysWhy(t *testing.T) {
	inst, _ := apiWithWorktree(t, "live")
	env := newAPIEnv(t, apiFixture{insts: []*session.Instance{inst}})

	got := env.readAnswer(fmt.Sprintf("/api/v1/sessions/%d/mockup", env.ids["live"]))
	require.NoError(t, got.err)
	assert.Equal(t, http.StatusConflict, got.status)
	assert.Equal(t, mockupCSP, got.header.Get("Content-Security-Policy"))
	assert.Equal(t, "text/html; charset=utf-8", got.header.Get("Content-Type"))
	assert.Contains(t, string(got.body), "read the mockup of live: no brainstorm companion is running in this session&#39;s worktree")
	assert.Contains(t, env.logged(), loggedAction{device: "phone", name: "mockup", session: env.ids["live"],
		result: "409 read the mockup of live: no brainstorm companion is running in this session's worktree"})
}

func TestMockupErrorPage_EscapesTheMessage(t *testing.T) {
	page := mockupErrorPage(`<script>alert(1)</script>`)
	assert.NotContains(t, page, "<script>alert")
	assert.Contains(t, page, "&lt;script&gt;alert(1)&lt;/script&gt;")
}
