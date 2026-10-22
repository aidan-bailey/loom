package core

import (
	"crypto/ecdh"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	tokenA = strings.Repeat("ab", 32)
	tokenB = strings.Repeat("cd", 32)
)

// webJSON is an enabled web.json without a VAPID key, holding two devices
// listed out of name order: iPhone, which takes waiting pushes only, until
// a time, and iPad, whose push subscription expired.
var webJSON = `{"version":1,"listen":"127.0.0.1:7420","origin":"https://loom.example.com","devices":[` +
	`{"name":"iPhone","token_sha256":"` + tokenA + `","paired":"2026-10-01T09:00:00Z","last_seen":"2026-10-02T10:30:00Z",` +
	`"notify":{"waiting":true,"finished":false,"paused_until":"2026-10-03T08:00:00Z"},` +
	`"push":{"endpoint":"https://web.push.apple.com/abc","p256dh":"pk","auth":"au"},"push_state":"ok"},` +
	`{"name":"iPad","token_sha256":"` + tokenB + `","paired":"2026-10-04T09:00:00Z","last_seen":"0001-01-01T00:00:00Z",` +
	`"notify":{"waiting":false,"finished":true,"paused_until":""},"push_state":"expired"}]}`

// webDeviceJSON is one device of a web.json, with no push subscription.
func webDeviceJSON(name, token string) string {
	return `{"name":"` + name + `","token_sha256":"` + token + `","paired":"2026-10-01T09:00:00Z","last_seen":"2026-10-01T09:00:00Z",` +
		`"notify":{"waiting":true,"finished":true,"paused_until":""}}`
}

// webWith is an enabled web.json holding devices (webDeviceJSON).
func webWith(devices ...string) string {
	return `{"version":1,"listen":"127.0.0.1:7420","origin":"https://loom.example.com","devices":[` + strings.Join(devices, ",") + `]}`
}

// webDevices is n devices with distinct names and tokens, d0 to d<n-1>.
func webDevices(n int) []string {
	var out []string
	for i := 0; i < n; i++ {
		out = append(out, webDeviceJSON(fmt.Sprintf("d%d", i), fmt.Sprintf("%064x", i)))
	}
	return out
}

// loadWebInto points the global config dir at a fresh temp dir holding a
// web.json with body (none when body is empty) and loads it into m with
// initWeb, as Boot does.
func loadWebInto(t *testing.T, m *Model, body string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(config.EnvGlobalDir, dir)
	if body != "" {
		require.NoError(t, os.WriteFile(filepath.Join(dir, WebFile), []byte(body), 0o600))
	}
	m.initWeb()
}

// webModel is a model whose global dir holds a web.json with body (none
// when body is empty), loaded by initWeb.
func webModel(t *testing.T, body string) *Model {
	t.Helper()
	m := NewForTest(Options{})
	loadWebInto(t, m, body)
	return m
}

// webFileBytes is web.json's content in m's global dir.
func webFileBytes(t *testing.T, m *Model) []byte {
	t.Helper()
	data, err := os.ReadFile(m.web.path)
	require.NoError(t, err)
	return data
}

func TestInitWeb_NoFileIsOffAndEmpty(t *testing.T) {
	m := webModel(t, "")

	assert.Equal(t, WebView{}, m.Web())
	dir, err := config.GetGlobalConfigDir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, WebFile), m.web.path)
	assert.Empty(t, m.web.loadErr)
	assert.Empty(t, m.Drain().Events, "nothing to tell anyone")
	assert.NoFileExists(t, m.web.path, "loading writes nothing")
}

func TestInitWeb_LoadsTheFile(t *testing.T) {
	m := webModel(t, webJSON)

	v := m.Web()
	assert.Equal(t, "127.0.0.1:7420", v.Listen)
	assert.Equal(t, "https://loom.example.com", v.Origin)
	assert.False(t, v.Serving, "nothing listens until the layer says so")
	assert.Empty(t, v.Error)
	require.Len(t, v.Devices, 2)
	ipad, iphone := v.Devices[0], v.Devices[1]
	assert.Equal(t, "iPad", ipad.Name, "sorted by name")
	assert.Equal(t, PushExpired, ipad.Push)
	assert.True(t, ipad.LastSeen.IsZero(), "never seen")
	assert.True(t, ipad.Notify.Finished)
	assert.False(t, ipad.Notify.Waiting)
	assert.True(t, ipad.Notify.PausedUntil.IsZero(), "an empty paused_until is no pause")
	assert.Equal(t, "iPhone", iphone.Name)
	assert.Equal(t, PushOK, iphone.Push)
	assert.True(t, time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC).Equal(iphone.Paired))
	assert.True(t, time.Date(2026, 10, 2, 10, 30, 0, 0, time.UTC).Equal(iphone.LastSeen))
	assert.True(t, iphone.Notify.Waiting)
	assert.False(t, iphone.Notify.Finished)
	assert.True(t, time.Date(2026, 10, 3, 8, 0, 0, 0, time.UTC).Equal(iphone.Notify.PausedUntil))

	d := m.web.find("iPhone")
	require.NotNil(t, d)
	assert.Equal(t, tokenA, hex.EncodeToString(d.tokenHash[:]))
	assert.Equal(t, PushSubscription{Endpoint: "https://web.push.apple.com/abc", P256DH: "pk", Auth: "au"}, d.sub)
	assert.Nil(t, m.web.find("iphone"), "names match exactly")
	assert.Zero(t, m.web.vapid, "no key until the first enable")
	assert.Empty(t, m.Drain().Events)

	data, err := json.Marshal(v)
	require.NoError(t, err)
	assert.NotContains(t, string(data), tokenA, "the view carries no token hash")
	assert.NotContains(t, string(data), "web.push.apple.com", "and no push subscription")
}

func TestInitWeb_LeavesAFileItCannotUseAlone(t *testing.T) {
	for _, tc := range []struct{ name, body, why string }{
		{"not JSON", `{not json`, "parse"},
		{"a newer version", `{"version":2,"listen":"127.0.0.1:7420","origin":"https://loom.example.com","devices":[]}`, "newer"},
		{"no version", `{"listen":"127.0.0.1:7420","origin":"https://loom.example.com"}`, "version 0"},
		{"a listen address that is not loopback", `{"version":1,"listen":"0.0.0.0:7420","origin":"https://loom.example.com"}`, "listen"},
		{"a listen address without an origin", `{"version":1,"listen":"127.0.0.1:7420"}`, "origin is empty"},
		{"an unusable origin", `{"version":1,"listen":"127.0.0.1:7420","origin":"http://example.com"}`, "origin"},
		{"a device without a name", webWith(webDeviceJSON("", tokenA)), "device"},
		{"a name with spaces around it", webWith(webDeviceJSON(" a", tokenA)), "spaces"},
		{"a name listed twice", webWith(webDeviceJSON("a", tokenA), webDeviceJSON("a", tokenB)), "twice"},
		{"a short token hash", webWith(webDeviceJSON("a", "abcd")), "token_sha256"},
		{"a token hash that is not hex", webWith(webDeviceJSON("a", strings.Repeat("zz", 32))), "token_sha256"},
		{"a token shared by two devices", webWith(webDeviceJSON("a", tokenA), webDeviceJSON("b", tokenA)), "shares a token"},
		{"too many devices", webWith(webDevices(MaxWebDevices + 1)...), "devices"},
		{"an unknown push state", strings.Replace(webJSON, `"push_state":"expired"`, `"push_state":"bogus"`, 1), "push_state"},
		{"a pause that is no time", strings.Replace(webJSON, `"paused_until":"2026-10-03T08:00:00Z"`, `"paused_until":"tomorrow"`, 1), "paused_until"},
		{"a key that is not P-256", `{"version":1,"vapid":{"private":"AAAA","public":"AAAA"}}`, "vapid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := webModel(t, tc.body)

			assert.Contains(t, m.web.loadErr, tc.why)
			assert.Equal(t, WebView{Error: m.web.loadErr}, m.Web(), "off, with the reason shown and nothing of the file held")
			var notices []string
			for _, ev := range m.Drain().Events {
				if n, ok := ev.(Notice); ok && n.Err != nil {
					notices = append(notices, n.Err.Error())
				}
			}
			require.Len(t, notices, 1, "a notice for the next TUI")
			assert.Contains(t, notices[0], "web:")
			assert.Contains(t, notices[0], tc.why)
			assert.Error(t, m.saveWeb(), "and nothing writes over it")
			assert.Equal(t, tc.body, string(webFileBytes(t, m)), "the file is as it was")
		})
	}
}

func TestInitWeb_AnUnreadableFileIsLeftAlone(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.EnvGlobalDir, dir)
	require.NoError(t, os.Mkdir(filepath.Join(dir, WebFile), 0o755), "a directory where the file should be")
	m := NewForTest(Options{})

	m.initWeb()

	assert.Contains(t, m.web.loadErr, "read")
	assert.Error(t, m.saveWeb())
	info, err := os.Stat(filepath.Join(dir, WebFile))
	require.NoError(t, err)
	assert.True(t, info.IsDir(), "untouched")
}

func TestInitWeb_AKeyWhosePublicHalfIsNotItsOwnIsLeftAlone(t *testing.T) {
	a, err := newVAPIDKey()
	require.NoError(t, err)
	b, err := newVAPIDKey()
	require.NoError(t, err)
	enc := base64.RawURLEncoding.EncodeToString
	body := fmt.Sprintf(`{"version":1,"vapid":{"private":%q,"public":%q}}`, enc(a.Private), enc(b.Public))

	m := webModel(t, body)

	assert.Contains(t, m.web.loadErr, "vapid")
	assert.Equal(t, body, string(webFileBytes(t, m)))
}

func TestInitWeb_PushStates(t *testing.T) {
	device := func(state string) string {
		return strings.Replace(webDeviceJSON("a", tokenA), `"paused_until":""}}`, `"paused_until":""},"push_state":"`+state+`"}`, 1)
	}
	for state, want := range map[string]PushState{
		"": PushNone, "ok": PushOK, "expired": PushExpired, "failing: 503": PushFailing(503), "failing: 0": PushFailing(0),
	} {
		m := webModel(t, webWith(device(state)))
		require.Empty(t, m.web.loadErr, "%q", state)
		assert.Equal(t, want, m.Web().Devices[0].Push, "%q", state)
	}
	for _, state := range []string{"OK", "failing", "failing: ", "failing: -1", "failing: 05", "failing: x", "failed: 503"} {
		m := webModel(t, webWith(device(state)))
		assert.Contains(t, m.web.loadErr, "push_state", "%q", state)
	}
}

func TestSaveWeb_RoundTripsWhatWasLoaded(t *testing.T) {
	m := webModel(t, webJSON)
	key, err := newVAPIDKey()
	require.NoError(t, err)
	m.web.vapid = key

	require.NoError(t, m.saveWeb())

	info, err := os.Stat(m.web.path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "it holds a token hash and a key")
	m2 := NewForTest(Options{})
	m2.initWeb()
	require.Empty(t, m2.web.loadErr)
	assert.Equal(t, m.Web(), m2.Web())
	assert.Equal(t, m.web.devices, m2.web.devices, "tokens, subscriptions and states too")
	assert.Equal(t, key, m2.web.vapid)

	var f map[string]any
	require.NoError(t, json.Unmarshal(webFileBytes(t, m), &f))
	assert.EqualValues(t, 1, f["version"])
	assert.Equal(t, "127.0.0.1:7420", f["listen"])
	assert.Equal(t, "https://loom.example.com", f["origin"])
	assert.Equal(t, map[string]any{
		"private": base64.RawURLEncoding.EncodeToString(key.Private),
		"public":  base64.RawURLEncoding.EncodeToString(key.Public),
	}, f["vapid"], "unpadded base64url")
	devices := f["devices"].([]any)
	require.Len(t, devices, 2)
	ipad, iphone := devices[0].(map[string]any), devices[1].(map[string]any)
	assert.Equal(t, "iPad", ipad["name"], "sorted by name")
	assert.NotContains(t, ipad, "push", "no subscription, no key")
	assert.Equal(t, "", ipad["notify"].(map[string]any)["paused_until"])
	assert.Equal(t, "iPhone", iphone["name"])
	assert.Equal(t, tokenA, iphone["token_sha256"])
	assert.Equal(t, "2026-10-03T08:00:00Z", iphone["notify"].(map[string]any)["paused_until"])
	assert.Equal(t, map[string]any{"endpoint": "https://web.push.apple.com/abc", "p256dh": "pk", "auth": "au"}, iphone["push"])
	assert.Equal(t, "ok", iphone["push_state"])
}

func TestSaveWeb_ReportsAWriteThatFails(t *testing.T) {
	m := webModel(t, webJSON)
	blocker := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(blocker, nil, 0o600))
	m.web.path = filepath.Join(blocker, WebFile)

	assert.Error(t, m.saveWeb(), "a path below a regular file")
}

func TestSaveWeb_ALoadedModelWritesToItsPath(t *testing.T) {
	m := webModel(t, "")
	loaded := m.web.path
	require.NoFileExists(t, loaded, "fixture: loading wrote nothing")
	elsewhere := t.TempDir()
	t.Setenv(config.EnvGlobalDir, elsewhere)

	require.NoError(t, m.saveWeb())

	assert.FileExists(t, loaded, "the path it loaded from")
	assert.NoFileExists(t, filepath.Join(elsewhere, WebFile), "it does not look the global dir up again")
}

// TestSaveWeb_AModelWithNoPathWritesUnderTheGlobalDir: a NewForTest model
// that never ran initWeb (or Boot) has no path, and the tests of the web
// layer (Story 4) change such a model's web state and expect it kept. It is
// kept under the global dir TestMain isolates, so the test leaves no file
// behind.
func TestSaveWeb_AModelWithNoPathWritesUnderTheGlobalDir(t *testing.T) {
	require.NotEmpty(t, os.Getenv(config.EnvGlobalDir), "TestMain isolates the global dir: never write into the real one")
	dir, err := config.GetGlobalConfigDir()
	require.NoError(t, err)
	path := filepath.Join(dir, WebFile)
	_ = os.Remove(path)
	t.Cleanup(func() { _ = os.Remove(path) })
	m := NewForTest(Options{})
	require.Empty(t, m.web.path, "fixture: initWeb never ran")
	m.web.listen, m.web.origin = "127.0.0.1:7420", "https://loom.example.com"

	require.NoError(t, m.saveWeb())

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	m2 := NewForTest(Options{})
	m2.initWeb()
	require.Empty(t, m2.web.loadErr)
	assert.Equal(t, "127.0.0.1:7420", m2.Web().Listen, "what it wrote is what a boot reads")
	assert.Empty(t, m.web.path, "it looked the dir up and kept no path")
}

func TestSaveWeb_FailsOnlyWhenNeitherAPathNorAGlobalDirIsKnown(t *testing.T) {
	loaded := webModel(t, "")
	t.Setenv(config.EnvGlobalDir, "not/absolute")
	unbooted := NewForTest(Options{})

	err := unbooted.saveWeb()

	assert.ErrorIs(t, err, errWebNoDir)
	assert.ErrorContains(t, err, "must be an absolute path", "and says why the dir is unknown")
	assert.NoError(t, loaded.saveWeb(), "a model that has a path needs no dir")
}

func TestSaveWeb_StillRefusesWhileTheFileIsLeftAlone(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.EnvGlobalDir, dir)
	m := NewForTest(Options{})
	m.web.loadErr = "parse web.json: bad"

	err := m.saveWeb()

	assert.ErrorContains(t, err, "left alone")
	assert.NotErrorIs(t, err, errWebNoDir, "the load error comes first, before any dir is looked up")
	assert.NoFileExists(t, filepath.Join(dir, WebFile), "and nothing was written")
}

func TestWebView_ShowsTheLoadErrorBeforeTheServingError(t *testing.T) {
	var w webState
	w.servingErr = "listen tcp 127.0.0.1:7420: address already in use"
	assert.Equal(t, w.servingErr, w.view().Error)
	w.loadErr = "parse web.json: bad"
	assert.Equal(t, "parse web.json: bad", w.view().Error)
}

func TestWebView_CarriesTheRevisionAndKeepsItOutOfTheFile(t *testing.T) {
	m := webModel(t, webJSON)
	assert.Zero(t, m.Web().Rev, "a model that only loaded has seen no ConfigureWeb")
	m.web.rev = 5
	assert.EqualValues(t, 5, m.Web().Rev)

	require.NoError(t, m.saveWeb())

	assert.NotContains(t, strings.ToLower(string(webFileBytes(t, m))), `"rev`, "memory only: web.json does not hold it")
	m2 := NewForTest(Options{})
	m2.initWeb()
	assert.Zero(t, m2.Web().Rev, "a boot starts the count afresh")
}

func TestPrune_DropsTheCodesThatExpired(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	w := webState{now: func() time.Time { return now }}
	w.pending = []pendingPairing{
		{code: "AAAAAAAA", name: "old", expires: now.Add(-time.Second)},
		{code: "BBBBBBBB", name: "due", expires: now},
		{code: "CCCCCCCC", name: "live", expires: now.Add(time.Second)},
	}

	w.prune()

	require.Len(t, w.pending, 1, "a code expires at its time, not after it")
	assert.Equal(t, "live", w.pending[0].name)
}

func TestClock_IsTimeNowUnlessSet(t *testing.T) {
	var w webState
	assert.WithinDuration(t, time.Now(), w.clock(), time.Minute)
	set := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	w.now = func() time.Time { return set }
	assert.True(t, set.Equal(w.clock()))
}

func TestNewPairingCode_IsEightCharactersOfTheAlphabet(t *testing.T) {
	seen := map[string]bool{}
	symbols := map[byte]bool{}
	for i := 0; i < 500; i++ {
		code, err := newPairingCode()
		require.NoError(t, err)
		norm, ok := NormalizePairingCode(code)
		require.True(t, ok, code)
		require.Equal(t, code, norm, "already in the form the model keeps")
		seen[code] = true
		for j := 0; j < len(code); j++ {
			symbols[code[j]] = true
		}
	}
	assert.Greater(t, len(seen), 495, "random: no repeats to speak of")
	assert.Len(t, symbols, len(PairingAlphabet), "every symbol turns up in 4000 draws")
}

func TestNewVAPIDKey_IsAP256PairAndFresh(t *testing.T) {
	a, err := newVAPIDKey()
	require.NoError(t, err)
	assert.Len(t, a.Private, 32)
	assert.Len(t, a.Public, 65)
	assert.EqualValues(t, 4, a.Public[0], "an uncompressed point")
	priv, err := ecdh.P256().NewPrivateKey(a.Private)
	require.NoError(t, err)
	assert.Equal(t, a.Public, priv.PublicKey().Bytes(), "the public half belongs to the private")
	b, err := newVAPIDKey()
	require.NoError(t, err)
	assert.NotEqual(t, a.Private, b.Private)
}

// Boot loads web.json after the account registry, and the web is on from
// the moment it boots.
func TestBoot_LoadsTheWebFile(t *testing.T) {
	m := bootModel(t)
	global, err := config.GetGlobalConfigDir()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(global, WebFile), []byte(webJSON), 0o600))

	notices := m.Boot()

	assert.Empty(t, notices)
	assert.Equal(t, "127.0.0.1:7420", m.Web().Listen)
	assert.Len(t, m.Web().Devices, 2)
}

// A web.json Boot cannot use is left as it is, and Boot hands back the
// notice since no client is connected yet to be sent it; it leaves none
// behind for the first Sync.
func TestBoot_ReturnsTheNoticeOfAWebFileItLeavesAlone(t *testing.T) {
	m := bootModel(t)
	global, err := config.GetGlobalConfigDir()
	require.NoError(t, err)
	path := filepath.Join(global, WebFile)
	require.NoError(t, os.WriteFile(path, []byte(`{"version":2}`), 0o600))

	notices := m.Boot()

	require.Len(t, notices, 1)
	n, ok := notices[0].(Notice)
	require.True(t, ok)
	assert.Contains(t, n.Err.Error(), "web:")
	assert.Contains(t, n.Err.Error(), "newer")
	assert.Contains(t, m.Web().Error, "newer", "and the view says why the web is off")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, `{"version":2}`, string(data), "the file is untouched")
	for _, ev := range m.Sync().Events {
		assert.NotEqual(t, notices[0], ev, "nothing left for the first Sync")
	}
}
