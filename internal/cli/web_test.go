package cli

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
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

// pushSubscription is a subscription the model accepts: an Apple push
// endpoint, a real P-256 point and 16 bytes of auth.
func pushSubscription(t *testing.T) core.PushSubscription {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	require.NoError(t, err)
	auth := make([]byte, 16)
	_, err = rand.Read(auth)
	require.NoError(t, err)
	return core.PushSubscription{
		Endpoint: "https://web.push.apple.com/abc",
		P256DH:   base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()),
		Auth:     base64.RawURLEncoding.EncodeToString(auth),
	}
}

// boolPtr is a pointer to b, for a core.NotifyChange field.
func boolPtr(b bool) *bool { return &b }

// lineWith is the first line of out that contains s.
func lineWith(t *testing.T, out, s string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, s) {
			return line
		}
	}
	t.Fatalf("no line with %q in:\n%s", s, out)
	return ""
}

const codeAlphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

var pairingCodeLine = regexp.MustCompile(`Pairing code for (.+): ([` + codeAlphabet + `]{4}-[` + codeAlphabet + `]{4})`)

func TestWebPair_PrintsTheCodeTheDaemonRedeems(t *testing.T) {
	d := webDaemon(t)
	require.NoError(t, d.loop.ConfigureWeb("127.0.0.1:7420", "https://loom.example.com"))

	out, _, err := runWeb(t, "pair")

	require.NoError(t, err)
	m := pairingCodeLine.FindStringSubmatch(out)
	require.NotNil(t, m, out)
	assert.Equal(t, "iPhone", m[1], "the default device name")
	assert.Contains(t, out, "https://loom.example.com", "where to enter it")
	assert.Regexp(t, `expires at \d\d:\d\d:\d\d`, out)
	name, token, err := d.loop.RedeemPairing(m[2])
	require.NoError(t, err, "the printed code, dash and all, is the daemon's")
	assert.Equal(t, "iPhone", name)
	assert.NotEmpty(t, token)
	assert.Len(t, webView(t, d).Devices, 1)
}

func TestWebPair_NamesTheDevice(t *testing.T) {
	d := webDaemon(t)
	require.NoError(t, d.loop.ConfigureWeb("127.0.0.1:7420", "https://loom.example.com"))

	out, _, err := runWeb(t, "pair", "--name", "Work iPad")

	require.NoError(t, err)
	m := pairingCodeLine.FindStringSubmatch(out)
	require.NotNil(t, m, out)
	name, _, err := d.loop.RedeemPairing(m[2])
	require.NoError(t, err)
	assert.Equal(t, "Work iPad", name)
}

func TestWebPair_RefusedWhileTheWebIsOff(t *testing.T) {
	webDaemon(t)

	out, _, err := runWeb(t, "pair")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "run loom web enable first")
	assert.NotRegexp(t, pairingCodeLine, out)
}

func TestWebPair_RefusesAPairedNameAndABadOne(t *testing.T) {
	d := webDaemon(t)
	require.NoError(t, d.loop.ConfigureWeb("127.0.0.1:7420", "https://loom.example.com"))
	webDevice(t, d, "iPhone")

	_, _, err := runWeb(t, "pair")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a device has that name already")

	_, _, err = runWeb(t, "pair", "--name", "   ")
	require.Error(t, err, "a name of spaces is no name")
	assert.Len(t, webView(t, d).Devices, 1)
}

func TestWebDevices_ListsEachDevice(t *testing.T) {
	d := webDaemon(t)
	require.NoError(t, d.loop.ConfigureWeb("127.0.0.1:7420", "https://loom.example.com"))
	webDevice(t, d, "iPhone")
	webDevice(t, d, "iPad")
	_, err := d.loop.SetNotify("iPhone", core.NotifyChange{Waiting: boolPtr(true), Finished: boolPtr(false)})
	require.NoError(t, err)
	_, err = d.loop.SetPushSubscription("iPhone", pushSubscription(t))
	require.NoError(t, err)
	_, err = d.loop.SetPushSubscription("iPad", pushSubscription(t))
	require.NoError(t, err)
	d.loop.PushResult("iPad", core.PushFailing(500))

	out, _, err := runWeb(t, "devices")

	require.NoError(t, err)
	assert.Regexp(t, `NAME\s+PAIRED\s+LAST SEEN\s+NOTIFY\s+PUSH`, out)
	phone := lineWith(t, out, "iPhone")
	assert.Regexp(t, `\swaiting\s+ok\s*$`, phone)
	assert.NotContains(t, phone, "finished")
	assert.Contains(t, lineWith(t, out, "iPad"), "failing: 500")
}

func TestWebDevices_SaysWhenNoneArePaired(t *testing.T) {
	webDaemon(t)

	out, _, err := runWeb(t, "devices")

	require.NoError(t, err)
	assert.Contains(t, out, "No paired devices.")
	assert.NotContains(t, out, "NAME")
}

func TestWebDevices_JSON(t *testing.T) {
	d := webDaemon(t)
	require.NoError(t, d.loop.ConfigureWeb("127.0.0.1:7420", "https://loom.example.com"))
	webDevice(t, d, "iPhone")
	_, err := d.loop.SetNotify("iPhone", core.NotifyChange{Waiting: boolPtr(false), Finished: boolPtr(true)})
	require.NoError(t, err)
	_, err = d.loop.SetPushSubscription("iPhone", pushSubscription(t))
	require.NoError(t, err)

	out, _, err := runWeb(t, "devices", "--json")

	require.NoError(t, err)
	var rows []webDeviceJSON
	require.NoError(t, json.Unmarshal([]byte(out), &rows), out)
	require.Len(t, rows, 1)
	assert.Equal(t, "iPhone", rows[0].Name)
	assert.False(t, rows[0].Notify.Waiting)
	assert.True(t, rows[0].Notify.Finished)
	assert.Nil(t, rows[0].Notify.PausedUntil)
	assert.Equal(t, "ok", rows[0].Push)
	assert.False(t, rows[0].Paired.IsZero())
}

func TestWebDevices_JSONIsAnEmptyArrayWhenNonePaired(t *testing.T) {
	webDaemon(t)

	out, _, err := runWeb(t, "devices", "--json")

	require.NoError(t, err)
	assert.JSONEq(t, `[]`, out)
}

func TestWebNotifyText(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.Local)
	later := now.Add(time.Hour)
	for _, tc := range []struct {
		name string
		n    core.NotifySettings
		want string
	}{
		{"none", core.NotifySettings{}, "off"},
		{"waiting", core.NotifySettings{Waiting: true}, "waiting"},
		{"both", core.NotifySettings{Waiting: true, Finished: true}, "waiting,finished"},
		{"paused", core.NotifySettings{Finished: true, PausedUntil: later}, "finished (paused until " + later.Format("15:04") + ")"},
		{"a pause that ended", core.NotifySettings{Waiting: true, PausedUntil: now.Add(-time.Minute)}, "waiting"},
		{"paused with nothing on", core.NotifySettings{PausedUntil: later}, "off"},
	} {
		assert.Equal(t, tc.want, notifyText(tc.n, now), tc.name)
	}
}

func TestWebSeenText(t *testing.T) {
	assert.Equal(t, "never", seenText(time.Time{}))
	at := time.Date(2026, 10, 21, 9, 5, 0, 0, time.Local)
	assert.Equal(t, "2026-10-21 09:05", seenText(at))
}

func TestWebRevoke_RemovesTheNamedDevice(t *testing.T) {
	d := webDaemon(t)
	require.NoError(t, d.loop.ConfigureWeb("127.0.0.1:7420", "https://loom.example.com"))
	webDevice(t, d, "iPhone")
	webDevice(t, d, "iPad")

	out, _, err := runWeb(t, "revoke", "iPhone")

	require.NoError(t, err)
	assert.Contains(t, out, "Revoked iPhone")
	devs := webView(t, d).Devices
	require.Len(t, devs, 1)
	assert.Equal(t, "iPad", devs[0].Name)
}

func TestWebRevoke_AnUnknownNameIsAnError(t *testing.T) {
	d := webDaemon(t)
	require.NoError(t, d.loop.ConfigureWeb("127.0.0.1:7420", "https://loom.example.com"))
	webDevice(t, d, "iPhone")

	_, _, err := runWeb(t, "revoke", "iphone")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no device has that name")
	assert.Len(t, webView(t, d).Devices, 1, "a name matches exactly, not by case")
}

func TestWebRevoke_AllRemovesEveryDevice(t *testing.T) {
	d := webDaemon(t)
	require.NoError(t, d.loop.ConfigureWeb("127.0.0.1:7420", "https://loom.example.com"))
	webDevice(t, d, "iPhone")
	webDevice(t, d, "iPad")

	out, _, err := runWeb(t, "revoke", "--all")

	require.NoError(t, err)
	assert.Contains(t, out, "Revoked iPhone")
	assert.Contains(t, out, "Revoked iPad")
	assert.Empty(t, webView(t, d).Devices)

	out, _, err = runWeb(t, "revoke", "--all")
	require.NoError(t, err)
	assert.Contains(t, out, "No paired devices.")
}

func TestWebRevoke_NeedsOneNameOrAll(t *testing.T) {
	d := webDaemon(t)
	require.NoError(t, d.loop.ConfigureWeb("127.0.0.1:7420", "https://loom.example.com"))
	webDevice(t, d, "iPhone")

	_, _, err := runWeb(t, "revoke")
	require.Error(t, err, "neither a name nor --all")
	_, _, err = runWeb(t, "revoke", "iPhone", "--all")
	require.Error(t, err, "both a name and --all")
	_, _, err = runWeb(t, "revoke", "iPhone", "iPad")
	require.Error(t, err, "two names")

	assert.Len(t, webView(t, d).Devices, 1, "none of them revoked anything")
}
