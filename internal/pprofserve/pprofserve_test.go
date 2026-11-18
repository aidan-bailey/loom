package pprofserve

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// get fetches path from s with the Host header a local client sends.
func get(t *testing.T, s *Server, path string) *http.Response {
	t.Helper()
	resp, err := http.Get("http://" + s.Addr() + path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// body reads resp's body whole.
func body(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return b
}

// gzipMagic opens every profile runtime/pprof writes in proto form, which is
// what `go tool pprof` reads.
var gzipMagic = []byte{0x1f, 0x8b}

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		spec   string
		addr   string
		on     bool
		refuse bool
	}{
		{spec: ""},
		{spec: "0"},
		{spec: "off"},
		{spec: "false"},
		{spec: " 1 ", addr: "127.0.0.1:0", on: true},
		{spec: "1", addr: "127.0.0.1:0", on: true},
		{spec: "true", addr: "127.0.0.1:0", on: true},
		{spec: "on", addr: "127.0.0.1:0", on: true},
		{spec: "127.0.0.1:6060", addr: "127.0.0.1:6060", on: true},
		{spec: "127.0.0.2:6060", addr: "127.0.0.2:6060", on: true},
		{spec: "[::1]:6060", addr: "[::1]:6060", on: true},
		{spec: "localhost:6060", addr: "localhost:6060", on: true},
		// Off loopback, or no host at all (which binds every interface), is
		// refused rather than served somewhere the user didn't mean.
		{spec: ":6060", refuse: true},
		{spec: "0.0.0.0:6060", refuse: true},
		{spec: "[::]:6060", refuse: true},
		{spec: "192.0.2.1:6060", refuse: true},
		{spec: "example.com:6060", refuse: true},
		{spec: "6060", refuse: true},
		{spec: "127.0.0.1", refuse: true},
		{spec: "yes please", refuse: true},
	} {
		t.Run(tc.spec, func(t *testing.T) {
			addr, on, err := Parse(tc.spec)
			if tc.refuse {
				assert.Error(t, err)
				assert.False(t, on)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.on, on)
			assert.Equal(t, tc.addr, addr)
		})
	}
}

func TestStart_ServesTheHeapProfileOnLoopback(t *testing.T) {
	s, err := Start("127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	host, _, err := net.SplitHostPort(s.Addr())
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1", host)

	resp := get(t, s, "/debug/pprof/heap")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.True(t, bytes.HasPrefix(body(t, resp), gzipMagic), "a heap profile in proto form")
}

func TestStart_RefusesAnAddressOffLoopback(t *testing.T) {
	s, err := Start("0.0.0.0:0")
	assert.Error(t, err)
	assert.Nil(t, s)
}

func TestServer_GoroutineDumpAsText(t *testing.T) {
	s, err := Start("127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	resp := get(t, s, "/debug/pprof/goroutine?debug=2")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, string(body(t, resp)), "goroutine ")
}

func TestServer_CPUProfile(t *testing.T) {
	s, err := Start("127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	resp := get(t, s, "/debug/pprof/profile?seconds=1")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.True(t, bytes.HasPrefix(body(t, resp), gzipMagic), "a CPU profile in proto form")
}

func TestServer_RefusesABadCPUProfileLength(t *testing.T) {
	s, err := Start("127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	for _, q := range []string{"seconds=abc", "seconds=0", "seconds=-5", "seconds=301"} {
		resp := get(t, s, "/debug/pprof/profile?"+q)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, q)
	}
}

func TestServer_UnknownProfileIsNotFound(t *testing.T) {
	s, err := Start("127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	resp := get(t, s, "/debug/pprof/nonesuch")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestServer_IndexListsTheProfiles(t *testing.T) {
	s, err := Start("127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	resp := get(t, s, "/debug/pprof/")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	text := string(body(t, resp))
	for _, name := range []string{"heap", "allocs", "goroutine", "profile"} {
		assert.Contains(t, text, name)
	}
}

// A page the browser loaded from elsewhere can reach a loopback port through
// DNS rebinding, but its requests carry its own host name: only a loopback
// Host is served.
func TestServer_RefusesAForeignHost(t *testing.T) {
	s, err := Start("127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	_, port, err := net.SplitHostPort(s.Addr())
	require.NoError(t, err)
	for host, want := range map[string]int{
		"evil.example:" + port: http.StatusForbidden,
		"evil.example":         http.StatusForbidden,
		"127.0.0.1:" + port:    http.StatusOK,
		"localhost:" + port:    http.StatusOK,
		"[::1]:" + port:        http.StatusOK,
	} {
		req, err := http.NewRequest(http.MethodGet, "http://"+s.Addr()+"/debug/pprof/heap", nil)
		require.NoError(t, err)
		req.Host = host
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		assert.Equal(t, want, resp.StatusCode, host)
	}
}

func TestServer_CloseStopsServing(t *testing.T) {
	s, err := Start("127.0.0.1:0")
	require.NoError(t, err)
	addr := s.Addr()
	require.NoError(t, s.Close())

	_, err = http.Get("http://" + addr + "/debug/pprof/heap")
	assert.Error(t, err)
}

func TestFromEnv_OffStartsNothing(t *testing.T) {
	t.Setenv(EnvVar, "")
	s := FromEnv("test")
	assert.Nil(t, s)
}

func TestFromEnv_OnServes(t *testing.T) {
	t.Setenv(EnvVar, "1")
	s := FromEnv("test")
	require.NotNil(t, s)
	t.Cleanup(func() { _ = s.Close() })

	resp := get(t, s, "/debug/pprof/heap")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// A refused address is logged and serves nothing: it never falls back to
// another address.
func TestFromEnv_RefusedServesNothing(t *testing.T) {
	t.Setenv(EnvVar, "0.0.0.0:0")
	assert.Nil(t, FromEnv("test"))
}
