package cli

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runWeb runs `loom web args…` on a fresh command tree (newWebCmd), so no
// flag carries over from another test.
func runWeb(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := newWebCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

// webDaemon serves a model in process over a global dir of the test's own,
// so the web.json it writes is the test's.
func webDaemon(t *testing.T) *testDaemon {
	t.Helper()
	isolateWorkspaces(t)
	return serveDaemon(t, "true")
}

// webView is the web state the daemon serves now, as the test's client has
// it.
func webView(t *testing.T, d *testDaemon) core.WebView {
	t.Helper()
	require.NoError(t, d.client.FlushForTest())
	d.client.Sync()
	return d.client.Web()
}

// webLayer plays the daemon's web layer for a test that has none: whenever
// WebView.Rev differs from the last one it acted on (unlike the real layer,
// which also acts on its first view, this fake treats the Rev it finds when
// it starts as already acted on), it reports addr and serveErr through
// core.WebHost.WebServing, as the layer does after it binds (or fails to). A
// ConfigureWeb of any values bumps Rev and clears what the layer reported, so
// only a command that really sends one makes it report; and WebServing does
// not bump Rev, so it reports once per ConfigureWeb. It reads Rev through a
// client of its own, and the test's cleanup waits for it.
func webLayer(t *testing.T, d *testDaemon, addr string, serveErr error) {
	t.Helper()
	c, err := Join()
	require.NoError(t, err)
	c.Sync()
	acted := c.Web().Rev
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		defer c.Close()
		for {
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Millisecond):
			}
			c.Sync()
			if w := c.Web(); w.Rev != acted {
				acted = w.Rev
				d.loop.WebServing(addr, serveErr)
			}
		}
	}()
	t.Cleanup(func() {
		close(stop)
		<-done
	})
}

// webDevice pairs a device called name, as a phone would: a code, then its
// redemption.
func webDevice(t *testing.T, d *testDaemon, name string) {
	t.Helper()
	p, err := d.loop.PairDevice(name)
	require.NoError(t, err)
	_, _, err = d.loop.RedeemPairing(p.Code)
	require.NoError(t, err)
}

func TestWebEnable_ReportsServing(t *testing.T) {
	d := webDaemon(t)
	webLayer(t, d, "127.0.0.1:7420", nil)

	out, _, err := runWeb(t, "enable", "--listen", "127.0.0.1:7420", "--origin", "https://loom.example.com")

	require.NoError(t, err)
	assert.Contains(t, out, "Serving on 127.0.0.1:7420")
	assert.Contains(t, out, "https://loom.example.com")
	w := webView(t, d)
	assert.Equal(t, "127.0.0.1:7420", w.Listen)
	assert.Equal(t, "https://loom.example.com", w.Origin)
}

func TestWebEnable_ReportsWhyItIsNotServing(t *testing.T) {
	d := webDaemon(t)
	webLayer(t, d, "", errors.New("listen tcp 127.0.0.1:7420: bind: address already in use"))

	_, _, err := runWeb(t, "enable", "--listen", "127.0.0.1:7420", "--origin", "https://loom.example.com")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not serving")
	assert.Contains(t, err.Error(), "address already in use")
	assert.Equal(t, "127.0.0.1:7420", webView(t, d).Listen, "the settings are kept, as the command says")
}

func TestWebEnable_SaysWhenTheDaemonDoesNotAnswer(t *testing.T) {
	old := webWait
	webWait = 150 * time.Millisecond
	t.Cleanup(func() { webWait = old })
	d := webDaemon(t) // no web layer reports

	_, _, err := runWeb(t, "enable", "--listen", "127.0.0.1:7420", "--origin", "https://loom.example.com")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "did not say")
	assert.Contains(t, err.Error(), "loom web status")
	assert.Equal(t, "127.0.0.1:7420", webView(t, d).Listen)
}

func TestWebEnable_AgainRetriesAFailedBind(t *testing.T) {
	d := webDaemon(t)
	require.NoError(t, d.loop.ConfigureWeb("127.0.0.1:7420", "https://loom.example.com"))
	d.loop.WebServing("", errors.New("listen tcp 127.0.0.1:7420: bind: address already in use"))
	// The layer reports when Rev changes, so it says "serving" only if enable
	// sends a ConfigureWeb of the stored values, which bumps Rev and clears
	// the stale Error; an enable that sent nothing would fail with it.
	webLayer(t, d, "127.0.0.1:7420", nil)
	before := webView(t, d)

	out, _, err := runWeb(t, "enable", "--listen", "127.0.0.1:7420", "--origin", "https://loom.example.com")

	require.NoError(t, err)
	assert.Contains(t, out, "Serving on 127.0.0.1:7420")
	w := webView(t, d)
	assert.Equal(t, before.Rev+1, w.Rev, "one ConfigureWeb, not a disable and an enable")
	assert.True(t, w.Serving)
	assert.Empty(t, w.Error, "the failed bind's error is gone")
	assert.Equal(t, "127.0.0.1:7420", w.Listen)
	assert.Equal(t, "https://loom.example.com", w.Origin)
}

func TestWebEnable_AgainRetryFailsAgainWithTheNewError(t *testing.T) {
	d := webDaemon(t)
	require.NoError(t, d.loop.ConfigureWeb("127.0.0.1:7420", "https://loom.example.com"))
	d.loop.WebServing("", errors.New("first: address already in use"))
	webLayer(t, d, "", errors.New("second: address already in use"))

	_, _, err := runWeb(t, "enable", "--listen", "127.0.0.1:7420", "--origin", "https://loom.example.com")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not serving")
	assert.Contains(t, err.Error(), "second: address already in use", "the retry's answer, not the stale one")
	assert.NotContains(t, err.Error(), "first")
}

func TestWebEnable_WhenItAlreadyServesChangesNothing(t *testing.T) {
	old := webWait
	webWait = 150 * time.Millisecond
	t.Cleanup(func() { webWait = old })
	d := webDaemon(t)
	require.NoError(t, d.loop.ConfigureWeb("127.0.0.1:7420", "https://loom.example.com"))
	d.loop.WebServing("127.0.0.1:7420", nil)
	// No layer reports: a ConfigureWeb would clear the model's serving state
	// and nothing would set it again, so the command would time out.
	before := webView(t, d)

	out, _, err := runWeb(t, "enable", "--listen", "127.0.0.1:7420", "--origin", "https://loom.example.com")

	require.NoError(t, err)
	assert.Contains(t, out, "Serving on 127.0.0.1:7420")
	w := webView(t, d)
	assert.Equal(t, before.Rev, w.Rev, "no ConfigureWeb was sent, so open streams stay")
	assert.True(t, w.Serving, "the listener was not taken down")
	assert.Equal(t, "127.0.0.1:7420", w.Addr)
}

func TestWebEnable_NewValuesAreAPlainEnable(t *testing.T) {
	d := webDaemon(t)
	require.NoError(t, d.loop.ConfigureWeb("127.0.0.1:7420", "https://loom.example.com"))
	d.loop.WebServing("", errors.New("listen tcp 127.0.0.1:7420: bind: address already in use"))
	webLayer(t, d, "127.0.0.1:7421", nil)

	out, _, err := runWeb(t, "enable", "--listen", "127.0.0.1:7421", "--origin", "https://loom.example.com")

	require.NoError(t, err)
	assert.Contains(t, out, "Serving on 127.0.0.1:7421")
	w := webView(t, d)
	assert.Equal(t, "127.0.0.1:7421", w.Listen)
	assert.Equal(t, "https://loom.example.com", w.Origin)
}

func TestServesAlready(t *testing.T) {
	const listen, origin = "127.0.0.1:7420", "https://loom.example.com"
	for _, tc := range []struct {
		name string
		w    core.WebView
		want bool
	}{
		{"stored values, serving", core.WebView{Listen: listen, Origin: origin, Serving: true, Addr: listen}, true},
		{"stored values, a failed bind", core.WebView{Listen: listen, Origin: origin, Error: "address already in use"}, false},
		{"stored values, nothing reported yet", core.WebView{Listen: listen, Origin: origin}, false},
		{"another listen, serving", core.WebView{Listen: "127.0.0.1:7421", Origin: origin, Serving: true, Addr: "127.0.0.1:7421"}, false},
		{"another origin, serving", core.WebView{Listen: listen, Origin: "https://other.example.com", Serving: true, Addr: listen}, false},
		{"off, the origin kept", core.WebView{Origin: origin}, false},
	} {
		assert.Equal(t, tc.want, servesAlready(tc.w, listen, origin), tc.name)
	}
}

func TestWebEnable_NeedsBothFlags(t *testing.T) {
	for _, args := range [][]string{
		{"enable"},
		{"enable", "--listen", "127.0.0.1:7420"},
		{"enable", "--origin", "https://loom.example.com"},
	} {
		_, _, err := runWeb(t, args...)
		require.Error(t, err, "%v", args)
		assert.Contains(t, err.Error(), "required flag(s)", "%v", args)
	}
}

func TestWebEnable_PassesTheDaemonsRefusalOn(t *testing.T) {
	for _, tc := range []struct{ name, listen, origin string }{
		{"a host name", "localhost:7420", "https://loom.example.com"},
		{"a public address", "0.0.0.0:7420", "https://loom.example.com"},
		{"port zero", "127.0.0.1:0", "https://loom.example.com"},
		{"a plain http origin", "127.0.0.1:7420", "http://loom.example.com"},
		{"an origin with a path", "127.0.0.1:7420", "https://loom.example.com/app"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := webDaemon(t)

			_, _, err := runWeb(t, "enable", "--listen", tc.listen, "--origin", tc.origin)

			require.Error(t, err)
			assert.Empty(t, webView(t, d).Listen, "a refused setting is not stored")
		})
	}
}

func TestWebSettled(t *testing.T) {
	const listen = "127.0.0.1:7420"
	const after = 5 // the Rev enable read before its ConfigureWeb
	for _, tc := range []struct {
		name string
		w    core.WebView
		want bool
	}{
		{"off", core.WebView{Rev: after + 1}, false},
		{"stored, nothing reported yet", core.WebView{Listen: listen, Rev: after + 1}, false},
		{"serving what was asked", core.WebView{Listen: listen, Serving: true, Addr: listen, Rev: after + 1}, true},
		{"serving what was asked, before the call", core.WebView{Listen: listen, Serving: true, Addr: listen, Rev: after}, false},
		{"serving the previous address", core.WebView{Listen: listen, Serving: true, Addr: "127.0.0.1:7000", Rev: after + 1}, false},
		{"another address is stored", core.WebView{Listen: "127.0.0.1:7000", Serving: true, Addr: "127.0.0.1:7000", Rev: after + 1}, false},
		{"it failed to listen", core.WebView{Listen: listen, Error: "address already in use", Rev: after + 1}, true},
		{"it failed to listen, before the call", core.WebView{Listen: listen, Error: "address already in use", Rev: after}, false},
		{"another address failed", core.WebView{Listen: "127.0.0.1:7000", Error: "address already in use", Rev: after + 1}, false},
	} {
		assert.Equal(t, tc.want, webSettled(tc.w, listen, after), tc.name)
	}
}

func TestSameAddr(t *testing.T) {
	assert.True(t, sameAddr("127.0.0.1:7420", "127.0.0.1:7420"))
	assert.True(t, sameAddr("[::1]:7420", "[0:0:0:0:0:0:0:1]:7420"), "one address, two spellings")
	assert.False(t, sameAddr("127.0.0.1:7420", "127.0.0.1:7421"))
	assert.False(t, sameAddr("127.0.0.1:7420", "[::1]:7420"))
	assert.True(t, sameAddr("not an address", "not an address"), "unparsable text compares as text")
	assert.False(t, sameAddr("not an address", "127.0.0.1:7420"))
}

func TestWebDisable_TurnsItOffAndKeepsTheOrigin(t *testing.T) {
	d := webDaemon(t)
	require.NoError(t, d.loop.ConfigureWeb("127.0.0.1:7420", "https://loom.example.com"))
	webDevice(t, d, "iPhone")

	out, _, err := runWeb(t, "disable")

	require.NoError(t, err)
	assert.Contains(t, out, "off")
	w := webView(t, d)
	assert.Empty(t, w.Listen)
	assert.Equal(t, "https://loom.example.com", w.Origin)
	assert.Len(t, w.Devices, 1, "paired devices are kept")
}

func TestWebDisable_WhenOffChangesNothing(t *testing.T) {
	d := webDaemon(t)

	out, _, err := runWeb(t, "disable")

	require.NoError(t, err)
	assert.Contains(t, out, "already off")
	w := webView(t, d)
	assert.Empty(t, w.Listen)
	assert.Empty(t, w.Origin)
}

func TestWebStatus_WhenOff(t *testing.T) {
	webDaemon(t)

	out, _, err := runWeb(t, "status")

	require.NoError(t, err)
	assert.Regexp(t, `Web:\s+off`, out)
	assert.Regexp(t, `Devices:\s+0`, out)
	assert.NotContains(t, out, "Serving")
}

func TestWebStatus_WhenServing(t *testing.T) {
	d := webDaemon(t)
	require.NoError(t, d.loop.ConfigureWeb("127.0.0.1:7420", "https://loom.example.com"))
	d.loop.WebServing("127.0.0.1:7420", nil)
	webDevice(t, d, "iPhone")

	out, _, err := runWeb(t, "status")

	require.NoError(t, err)
	assert.Regexp(t, `Web:\s+on`, out)
	assert.Regexp(t, `Listen:\s+127\.0\.0\.1:7420`, out)
	assert.Regexp(t, `Origin:\s+https://loom\.example\.com`, out)
	assert.Regexp(t, `Serving:\s+yes, on 127\.0\.0\.1:7420`, out)
	assert.Regexp(t, `Devices:\s+1`, out)
}

func TestWebStatus_SaysWhyItDoesNotServe(t *testing.T) {
	d := webDaemon(t)
	require.NoError(t, d.loop.ConfigureWeb("127.0.0.1:7420", "https://loom.example.com"))
	d.loop.WebServing("", errors.New("listen tcp 127.0.0.1:7420: bind: address already in use"))

	out, _, err := runWeb(t, "status")

	require.NoError(t, err)
	assert.Regexp(t, `Serving:\s+no: listen tcp 127\.0\.0\.1:7420: bind: address already in use`, out)
}
