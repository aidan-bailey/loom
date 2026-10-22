package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateWebListen(t *testing.T) {
	for _, listen := range []string{"127.0.0.1:7420", "[::1]:7420", "127.0.0.1:1", "127.0.0.1:65535", "127.0.0.2:8080"} {
		assert.NoError(t, ValidateWebListen(listen), listen)
	}
	for _, listen := range []string{
		"", "localhost:7420", "0.0.0.0:7420", ":7420", "192.168.1.5:7420", "[::]:7420", "[fe80::1]:7420",
		"127.0.0.1", "127.0.0.1:", "127.0.0.1:0", "127.0.0.1:65536", "127.0.0.1:-1", "127.0.0.1:+80",
		"127.0.0.1:07420", "127.0.0.1:http", "[127.0.0.1]:7420", "[0:0:0:0:0:0:0:1]:7420",
		"[::ffff:127.0.0.1]:7420", "[::1%lo]:7420", "::1:7420", " 127.0.0.1:7420", "127.0.0.1:7420 ",
		"127.0.0.1:7420/x", "unix:/tmp/loom.sock", "/tmp/loom.sock",
	} {
		assert.Error(t, ValidateWebListen(listen), "%q", listen)
	}
	assert.ErrorContains(t, ValidateWebListen("localhost:7420"), "loopback IP literal", "a host name is refused for what it is")
}

func TestValidateWebOrigin(t *testing.T) {
	long := "https://" + strings.Repeat("a", maxWebOrigin-len("https://"))
	for _, origin := range []string{
		"https://loom.example.com", "https://loom.example.com:8443", "https://x",
		"http://127.0.0.1:7420", "http://localhost:3000", long,
	} {
		assert.NoError(t, ValidateWebOrigin(origin), origin)
	}
	for _, origin := range []string{
		"", "loom.example.com", "//loom.example.com", "ftp://loom.example.com", "https://", "https:///x",
		"https://loom.example.com/", "https://loom.example.com/app", "https://loom.example.com?",
		"https://loom.example.com?a=1", "https://loom.example.com#", "https://loom.example.com#top",
		"https://u@loom.example.com", "https://u:p@loom.example.com", "HTTPS://loom.example.com",
		"https://Loom.Example.com", "https://loom.example.com:", "https://loom.example.com:0",
		"https://loom.example.com:65536", "https://loom.example.com:08443", "https://loom.example.com:+1",
		" https://loom.example.com", "https://loom.example.com ", "https://lo om.example.com",
		"http://loom.example.com:80", "http://localhost", "http://127.0.0.1", "http://[::1]:7420",
		"https:loom.example.com", long + "a",
	} {
		assert.Error(t, ValidateWebOrigin(origin), "%q", origin)
	}
}

func TestValidateDeviceName(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"iPhone", "iPhone"},
		{"  iPhone \t", "iPhone"},
		{"Aidan's iPhone 15", "Aidan's iPhone 15"},
		{"x", "x"},
		{strings.Repeat("x", 32), strings.Repeat("x", 32)},
		{strings.Repeat("é", 32), strings.Repeat("é", 32)},
	} {
		got, err := ValidateDeviceName(tc.in)
		require.NoError(t, err, "%q", tc.in)
		assert.Equal(t, tc.want, got)
	}
	for _, in := range []string{
		"", "   ", "\t\n", strings.Repeat("x", 33), strings.Repeat("é", 33),
		"a\tb", "a\nb", "bad\x00", "\x7f", "a\xffb",
	} {
		_, err := ValidateDeviceName(in)
		assert.Error(t, err, "%q", in)
	}
}

func TestNormalizePairingCode(t *testing.T) {
	for in, want := range map[string]string{
		"K7QM-2XPA":       "K7QM2XPA",
		"k7qm2xpa":        "K7QM2XPA",
		" k7qm 2xpa ":     "K7QM2XPA",
		"K-7-Q-M-2-X-P-A": "K7QM2XPA",
		"K7QM2XPA":        "K7QM2XPA",
	} {
		got, ok := NormalizePairingCode(in)
		assert.True(t, ok, "%q", in)
		assert.Equal(t, want, got, "%q", in)
	}
	for _, in := range []string{
		"", "----", "K7QM2XP", "K7QM2XPAA", "K7QM-2XPA-K7QM",
		"K7QM2XP0", "K7QM2XP1", "K7QMIXPA", "K7QM2XPO", "K7QM2XPL",
		"K7QM2XPÁ", "K7QM2XP\t", "ſ7QM2XPA",
	} {
		got, ok := NormalizePairingCode(in)
		assert.False(t, ok, "%q", in)
		assert.Empty(t, got, "%q", in)
	}
}

func TestPairingAlphabet_HasNoLookAlikes(t *testing.T) {
	assert.Len(t, PairingAlphabet, 31)
	seen := map[rune]bool{}
	for _, r := range PairingAlphabet {
		assert.False(t, seen[r], "%q twice", r)
		seen[r] = true
	}
	for _, r := range "01ILO" {
		assert.NotContains(t, PairingAlphabet, string(r))
	}
}

func TestPushFailing(t *testing.T) {
	assert.Equal(t, PushState("failing: 503"), PushFailing(503))
	assert.Equal(t, PushState("failing: 0"), PushFailing(0))
}

// webT0 is the instant an enabledWeb model's clock stands at.
var webT0 = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

// enabledWeb is a model loaded from an enabled web.json holding devices
// (webDeviceJSON), its clock fixed at webT0.
func enabledWeb(t *testing.T, devices ...string) *Model {
	t.Helper()
	m := webModel(t, webWith(devices...))
	require.Empty(t, m.web.loadErr)
	m.web.now = func() time.Time { return webT0 }
	return m
}

// blockWebPath moves m's web.json to a place that can't be written: below a
// regular file.
func blockWebPath(t *testing.T, m *Model) {
	t.Helper()
	blocker := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(blocker, nil, 0o600))
	m.web.path = filepath.Join(blocker, WebFile)
}

// pendingNames is the names the waiting codes are for, oldest first.
func pendingNames(m *Model) []string {
	var names []string
	for _, p := range m.web.pending {
		names = append(names, p.name)
	}
	return names
}

func TestConfigureWeb_EnablesWritesAndMakesTheKeyOnce(t *testing.T) {
	m := webModel(t, "")

	require.NoError(t, m.ConfigureWeb("127.0.0.1:7420", "https://loom.example.com"))

	v := m.Web()
	assert.Equal(t, "127.0.0.1:7420", v.Listen)
	assert.Equal(t, "https://loom.example.com", v.Origin)
	assert.Empty(t, v.Devices)
	assert.EqualValues(t, 1, v.Rev, "the layer rebinds on a new revision")
	v.Rev = 0 // memory only: a boot starts the count afresh
	info, err := os.Stat(m.web.path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	key := m.web.vapid
	require.Len(t, key.Private, 32, "the first enable makes the key")
	require.Len(t, key.Public, 65)
	m2 := NewForTest(Options{})
	m2.initWeb()
	require.Empty(t, m2.web.loadErr)
	assert.Equal(t, v, m2.Web(), "on disk")
	assert.Equal(t, key, m2.web.vapid, "the key too")

	require.NoError(t, m.ConfigureWeb("[::1]:7421", "https://loom.example.com"))
	assert.Equal(t, key, m.web.vapid, "a second enable keeps the key: push subscriptions are bound to it")
	require.NoError(t, m.ConfigureWeb("", ""))
	require.NoError(t, m.ConfigureWeb("127.0.0.1:7420", "https://loom.example.com"))
	assert.Equal(t, key, m.web.vapid, "so does an enable after a disable")
}

func TestConfigureWeb_OffKeepsTheOriginAndTheDevices(t *testing.T) {
	m := webModel(t, webJSON)

	require.NoError(t, m.ConfigureWeb("", "https://other.example.com"))

	v := m.Web()
	assert.Empty(t, v.Listen, "off")
	assert.Equal(t, "https://loom.example.com", v.Origin, "the origin is kept; the argument is not read")
	assert.Len(t, v.Devices, 2, "and the devices")
	assert.EqualValues(t, 1, v.Rev, "turning off bumps the revision too: the layer closes its listener on it")
	v.Rev = 0 // memory only: a boot starts the count afresh
	assert.Zero(t, m.web.vapid, "turning off makes no key")
	m2 := NewForTest(Options{})
	m2.initWeb()
	assert.Equal(t, v, m2.Web(), "on disk too")
}

func TestConfigureWeb_DisablingWhatWasNeverEnabledWritesNothing(t *testing.T) {
	m := webModel(t, "")

	require.NoError(t, m.ConfigureWeb("", ""))

	assert.NoFileExists(t, m.web.path)
}

func TestConfigureWeb_RefusesWhatItValidates(t *testing.T) {
	for _, tc := range []struct{ name, listen, origin, why string }{
		{"a host name", "localhost:7420", "https://loom.example.com", "loopback"},
		{"all interfaces", "0.0.0.0:7420", "https://loom.example.com", "loopback"},
		{"no port", "127.0.0.1", "https://loom.example.com", "listen"},
		{"no origin", "127.0.0.1:7420", "", "origin"},
		{"a plain http origin", "127.0.0.1:7420", "http://loom.example.com", "origin"},
		{"an origin with a path", "127.0.0.1:7420", "https://loom.example.com/app", "origin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := webModel(t, webJSON)
			m.web.rev = 3
			m.web.serving, m.web.addr = true, "127.0.0.1:7420"
			before := m.Web()

			err := m.ConfigureWeb(tc.listen, tc.origin)

			assert.ErrorContains(t, err, tc.why)
			assert.Equal(t, before, m.Web(), "nothing changed: the revision (3) and the layer's report too")
			assert.Equal(t, webJSON, string(webFileBytes(t, m)), "nothing was written")
		})
	}
}

// TestConfigureWeb_EverySuccessBumpsTheRevisionAndClearsWhatTheLayerReported:
// the layer rebinds when WebView.Rev changes, so every ConfigureWeb that
// succeeds bumps it, whatever it changed (even the stored values, which is how
// running loom web enable again retries a failed bind), and clears what the
// layer reported, so the next enable never reads a stale error.
func TestConfigureWeb_EverySuccessBumpsTheRevisionAndClearsWhatTheLayerReported(t *testing.T) {
	for _, tc := range []struct{ name, listen, origin string }{
		{"the stored settings", "127.0.0.1:7420", "https://loom.example.com"},
		{"another port", "127.0.0.1:7421", "https://loom.example.com"},
		{"another origin", "127.0.0.1:7420", "https://other.example.com"},
		{"off", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := webModel(t, webJSON)
			m.web.rev = 4
			m.web.serving, m.web.addr, m.web.servingErr = true, "127.0.0.1:7420", "stale"

			require.NoError(t, m.ConfigureWeb(tc.listen, tc.origin))

			assert.EqualValues(t, 5, m.Web().Rev, "one more than before")
			assert.False(t, m.web.serving)
			assert.Empty(t, m.web.addr)
			assert.Empty(t, m.web.servingErr, "so loom web enable never reads a stale error")
			assert.Empty(t, m.Web().Error)
			assert.False(t, m.Web().Serving)
		})
	}
}

// TestConfigureWeb_ADisableThenAnEnableLeavesTheRevisionChanged: a Sync
// coalesces the two ModelChanged events into the newest, whose Listen and
// Origin equal the first view's, so only Rev tells the layer to bind again.
func TestConfigureWeb_ADisableThenAnEnableLeavesTheRevisionChanged(t *testing.T) {
	m := webModel(t, webJSON)
	before := m.Web()

	require.NoError(t, m.ConfigureWeb("", ""))
	require.NoError(t, m.ConfigureWeb("127.0.0.1:7420", "https://loom.example.com"))

	after := m.Web()
	assert.Equal(t, before.Listen, after.Listen)
	assert.Equal(t, before.Origin, after.Origin)
	assert.EqualValues(t, before.Rev+2, after.Rev, "one per call")
}

// TestConfigureWeb_TheSameSettingsWriteNothing: the settings in force, with
// the key already made, leave web.json alone, and still bump the revision.
func TestConfigureWeb_TheSameSettingsWriteNothing(t *testing.T) {
	m := webModel(t, "")
	require.NoError(t, m.ConfigureWeb("127.0.0.1:7420", "https://loom.example.com"))
	require.NoError(t, os.Remove(m.web.path), "fixture: the file is gone, so a write would bring it back")
	rev := m.Web().Rev

	require.NoError(t, m.ConfigureWeb("127.0.0.1:7420", "https://loom.example.com"))

	assert.NoFileExists(t, m.web.path, "nothing to write")
	assert.EqualValues(t, rev+1, m.Web().Rev)
}

func TestConfigureWeb_RollsBackWhenTheWriteFails(t *testing.T) {
	m := webModel(t, webJSON)
	m.web.rev = 7
	m.web.serving, m.web.addr, m.web.servingErr = true, "127.0.0.1:7420", "stale"
	before := m.Web()
	blockWebPath(t, m)

	err := m.ConfigureWeb("127.0.0.1:7421", "https://other.example.com")

	require.Error(t, err)
	assert.Equal(t, before, m.Web(), "the settings are as they were, the revision (7) and the layer's report with them")
	assert.Zero(t, m.web.vapid, "and the key it made is gone")
}

func TestConfigureWeb_RefusesWhileTheFileIsLeftAlone(t *testing.T) {
	m := webModel(t, `{not json`)

	err := m.ConfigureWeb("127.0.0.1:7420", "https://loom.example.com")
	assert.ErrorContains(t, err, "left alone")
	assert.ErrorContains(t, err, "parse", "and says why")
	assert.Zero(t, m.Web().Rev, "a refusal bumps nothing")
	assert.ErrorContains(t, m.RevokeDevice("iPhone"), "left alone")
	_, err = m.PairDevice("Pixel")
	assert.ErrorIs(t, err, ErrWebOff)
	assert.Equal(t, `{not json`, string(webFileBytes(t, m)))
}

func TestConfigureWeb_NeedsAGlobalDirWhenTheModelHasNoPath(t *testing.T) {
	t.Setenv(config.EnvGlobalDir, "not/absolute")
	m := NewForTest(Options{}) // initWeb never ran, and the global dir is unknown: web.json has no place

	err := m.ConfigureWeb("127.0.0.1:7420", "https://loom.example.com")

	assert.ErrorIs(t, err, errWebNoDir)
	assert.Empty(t, m.Web().Listen, "nothing changed")
	assert.Zero(t, m.web.vapid, "and the key it made is gone")
}

func TestConfigureWeb_AModelThatNeverBootedWritesUnderTheGlobalDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.EnvGlobalDir, dir)
	m := NewForTest(Options{}) // initWeb never ran, as in a test of the web layer on a bare model

	require.NoError(t, m.ConfigureWeb("127.0.0.1:7420", "https://loom.example.com"))

	assert.FileExists(t, filepath.Join(dir, WebFile))
	assert.Len(t, m.web.vapid.Private, 32, "the first enable made the key")
	m2 := NewForTest(Options{})
	m2.initWeb()
	require.Empty(t, m2.web.loadErr)
	onDisk := m.Web()
	onDisk.Rev = 0 // memory only: a boot starts the count afresh
	assert.Equal(t, onDisk, m2.Web(), "on disk")
	assert.Equal(t, m.web.vapid, m2.web.vapid, "the key too")
}

func TestPairDevice_MakesACodeForTheName(t *testing.T) {
	m := enabledWeb(t)
	m.web.misses = 3
	before := m.Web()

	p, err := m.PairDevice("  Pixel  ")

	require.NoError(t, err)
	norm, ok := NormalizePairingCode(p.Code)
	assert.True(t, ok, "PairingCodeLen characters of the alphabet")
	assert.Equal(t, p.Code, norm, "without a dash")
	assert.Equal(t, webT0.Add(PairingTTL), p.Expires)
	assert.Equal(t, []pendingPairing{{code: p.Code, name: "Pixel", expires: webT0.Add(PairingTTL)}}, m.web.pending, "bound to the trimmed name")
	assert.Zero(t, m.web.misses, "a new pair starts the count of wrong codes afresh")
	assert.Equal(t, before, m.Web(), "a code is memory only: nothing for a client to see")
	assert.Equal(t, webWith(), string(webFileBytes(t, m)), "and never in web.json")
}

func TestPairDevice_RefusesWhatItShould(t *testing.T) {
	off := webModel(t, webJSON)
	require.NoError(t, off.ConfigureWeb("", ""))
	_, err := off.PairDevice("Pixel")
	assert.ErrorIs(t, err, ErrWebOff, "the web is off")
	assert.Empty(t, off.web.pending)

	m := enabledWeb(t, webDeviceJSON("iPhone", tokenA))
	for _, name := range []string{"", "   ", strings.Repeat("x", 33), "a\tb"} {
		_, err := m.PairDevice(name)
		assert.Error(t, err, "%q", name)
		assert.NotErrorIs(t, err, ErrDeviceExists, "%q", name)
	}
	_, err = m.PairDevice("iPhone")
	assert.ErrorIs(t, err, ErrDeviceExists)
	assert.ErrorContains(t, err, `"iPhone"`, "naming the device")
	_, err = m.PairDevice(" iPhone ")
	assert.ErrorIs(t, err, ErrDeviceExists, "the trimmed name is the one that counts")
	assert.Empty(t, m.web.pending, "a refusal leaves no code")
}

func TestPairDevice_AtMostSixteenDevices(t *testing.T) {
	m := enabledWeb(t, webDevices(MaxWebDevices-1)...)
	_, err := m.PairDevice("one more")
	require.NoError(t, err, "the sixteenth fits")

	m = enabledWeb(t, webDevices(MaxWebDevices)...)
	_, err = m.PairDevice("one too many")
	assert.ErrorIs(t, err, ErrTooManyDevices)
	assert.Empty(t, m.web.pending)
}

func TestPairDevice_WaitingCodesDoNotCountAgainstTheDeviceLimit(t *testing.T) {
	m := enabledWeb(t, webDevices(MaxWebDevices-1)...)

	for _, name := range []string{"a", "b", "c", "d"} {
		_, err := m.PairDevice(name)
		require.NoError(t, err, "%q: MaxWebDevices counts paired devices, not the codes waiting", name)
	}

	assert.Equal(t, []string{"a", "b", "c", "d"}, pendingNames(m), "fifteen paired and four waiting is past sixteen")
	assert.Len(t, m.web.devices, MaxWebDevices-1)
}

func TestPairDevice_ASecondCodeForANameReplacesTheFirst(t *testing.T) {
	m := enabledWeb(t)
	_, err := m.PairDevice("Pixel")
	require.NoError(t, err)

	second, err := m.PairDevice("Pixel")

	require.NoError(t, err)
	require.Len(t, m.web.pending, 1, "one code waits for the name")
	assert.Equal(t, second.Code, m.web.pending[0].code)
}

func TestPairDevice_AtMostFourCodesWait(t *testing.T) {
	m := enabledWeb(t)
	for _, name := range []string{"a", "b", "c", "d"} {
		_, err := m.PairDevice(name)
		require.NoError(t, err)
	}
	assert.Equal(t, []string{"a", "b", "c", "d"}, pendingNames(m))

	_, err := m.PairDevice("e")

	require.NoError(t, err)
	assert.Equal(t, []string{"b", "c", "d", "e"}, pendingNames(m), "the fifth voids the oldest")
}

func TestPairDevice_ExpiredCodesDoNotCount(t *testing.T) {
	m := enabledWeb(t)
	for _, name := range []string{"a", "b", "c", "d"} {
		_, err := m.PairDevice(name)
		require.NoError(t, err)
	}
	later := webT0.Add(PairingTTL)
	m.web.now = func() time.Time { return later }

	_, err := m.PairDevice("e")

	require.NoError(t, err)
	assert.Equal(t, []string{"e"}, pendingNames(m), "the four codes expired, so they are gone rather than the oldest voided")
}

func TestRevokeDevice_RemovesTheDeviceAndItsSubscription(t *testing.T) {
	m := webModel(t, webJSON)

	require.NoError(t, m.RevokeDevice("iPhone"))

	v := m.Web()
	require.Len(t, v.Devices, 1)
	assert.Equal(t, "iPad", v.Devices[0].Name)
	assert.Nil(t, m.web.find("iPhone"))
	data := string(webFileBytes(t, m))
	assert.NotContains(t, data, tokenA, "its token is gone, so its cookie is dead")
	assert.NotContains(t, data, "web.push.apple.com", "and its push subscription")
	m2 := NewForTest(Options{})
	m2.initWeb()
	assert.Equal(t, v, m2.Web(), "on disk too")
}

func TestRevokeDevice_AnUnknownNameIsRefused(t *testing.T) {
	m := webModel(t, webJSON)

	for _, name := range []string{"nobody", "iphone", " iPhone", ""} {
		assert.ErrorIs(t, m.RevokeDevice(name), ErrNoDevice, "%q", name)
	}

	assert.ErrorContains(t, m.RevokeDevice("nobody"), `"nobody"`, "naming it")
	assert.Len(t, m.Web().Devices, 2)
	assert.Equal(t, webJSON, string(webFileBytes(t, m)), "nothing was written")
}

func TestRevokeDevice_RollsBackWhenTheWriteFails(t *testing.T) {
	m := webModel(t, webJSON)
	before := m.Web()
	blockWebPath(t, m)

	require.Error(t, m.RevokeDevice("iPhone"))

	assert.Equal(t, before, m.Web(), "the device is still paired")
}

func TestRevokeDevice_WorksWhileTheWebIsOff(t *testing.T) {
	m := webModel(t, webJSON)
	require.NoError(t, m.ConfigureWeb("", ""))

	require.NoError(t, m.RevokeDevice("iPad"))

	assert.Len(t, m.Web().Devices, 1)
}
