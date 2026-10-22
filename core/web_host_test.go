package core

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
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

// webHostSub is a push subscription the daemon accepts, for the push service
// at host: a real P-256 point and 16 bytes of auth secret.
func webHostSub(t *testing.T, host string) PushSubscription {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	require.NoError(t, err)
	auth := make([]byte, 16)
	_, err = rand.Read(auth)
	require.NoError(t, err)
	return PushSubscription{
		Endpoint: "https://" + host + "/push/abc123",
		P256DH:   base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()),
		Auth:     base64.RawURLEncoding.EncodeToString(auth),
	}
}

// The daemon posts to the endpoint a paired phone supplies, so only the push
// services' hosts are taken, over https, on their port, with no user.
func TestWebHost_ValidatePushSubscription_Endpoints(t *testing.T) {
	apple := "https://web.push.apple.com/"
	exactly2KiB := apple + strings.Repeat("a", webPushEndpointMax-len(apple))
	cases := []struct {
		name     string
		endpoint string
		want     string // "" accepts the endpoint; else part of the error
	}{
		{"Apple", "https://web.push.apple.com/wpush/v2/abc", ""},
		{"Google", "https://fcm.googleapis.com/fcm/send/abc", ""},
		{"Mozilla", "https://updates.push.services.mozilla.com/wpush/v2/abc", ""},
		{"Windows", "https://db5.notify.windows.com/?token=abc", ""},
		{"an explicit port 443", "https://web.push.apple.com:443/abc", ""},
		{"a host in capitals", "https://WEB.PUSH.APPLE.COM/abc", ""},
		{"exactly 2 KiB", exactly2KiB, ""},
		{"one byte over 2 KiB", exactly2KiB + "a", "longer than"},
		{"http", "http://web.push.apple.com/abc", "not https"},
		{"no scheme", "web.push.apple.com/abc", "not https"},
		{"empty", "", "not https"},
		{"a control character", "https://web.push.apple.com/a\x7fb", "no URL"},
		{"another host", "https://push.example.com/abc", "no push service"},
		{"a host that only ends like Apple's", "https://evil-web.push.apple.com/abc", "no push service"},
		{"Apple's host as a prefix", "https://web.push.apple.com.evil.example/abc", "no push service"},
		{"the bare Windows domain", "https://notify.windows.com/abc", "no push service"},
		{"a look-alike Windows domain", "https://evilnotify.windows.com/abc", "no push service"},
		{"a loopback address", "https://127.0.0.1/abc", "no push service"},
		{"a user naming the real host", "https://web.push.apple.com@evil.example/abc", "names a user"},
		{"a user before the real host", "https://evil.example@web.push.apple.com/abc", "names a user"},
		{"another port", "https://web.push.apple.com:8443/abc", "port other than 443"},
	}
	good := webHostSub(t, "web.push.apple.com")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sub := good
			sub.Endpoint = tc.endpoint

			err := ValidatePushSubscription(sub)

			if tc.want == "" {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, tc.want)
		})
	}
}

// The keys must decode to a P-256 point and 16 bytes, spelled as the browser
// writes them (unpadded base64url, nothing else in the text).
func TestWebHost_ValidatePushSubscription_Keys(t *testing.T) {
	good := webHostSub(t, "web.push.apple.com")
	require.NoError(t, ValidatePushSubscription(good))
	enc := base64.RawURLEncoding.EncodeToString
	offCurve := make([]byte, 65)
	offCurve[0] = 4
	for i := 1; i < len(offCurve); i++ {
		offCurve[i] = 1
	}
	cases := []struct {
		name   string
		mutate func(s *PushSubscription)
		want   string
	}{
		{"no p256dh", func(s *PushSubscription) { s.P256DH = "" }, "p256dh is not a 65-byte"},
		{"p256dh of 64 bytes", func(s *PushSubscription) { s.P256DH = enc(make([]byte, 64)) }, "p256dh is not a 65-byte"},
		{"p256dh of 66 bytes", func(s *PushSubscription) { s.P256DH = enc(make([]byte, 66)) }, "p256dh is not a 65-byte"},
		{"p256dh with padding", func(s *PushSubscription) { s.P256DH += "=" }, "p256dh is not a 65-byte"},
		{"p256dh with a newline in it", func(s *PushSubscription) { s.P256DH = s.P256DH[:10] + "\n" + s.P256DH[10:] }, "p256dh is not a 65-byte"},
		{"p256dh that is not base64", func(s *PushSubscription) { s.P256DH = "!!!!" }, "p256dh is not a 65-byte"},
		{"p256dh that is the point at infinity", func(s *PushSubscription) { s.P256DH = enc(make([]byte, 65)) }, "no point on P-256"},
		{"p256dh off the curve", func(s *PushSubscription) { s.P256DH = enc(offCurve) }, "no point on P-256"},
		{"no auth", func(s *PushSubscription) { s.Auth = "" }, "auth is not a 16-byte"},
		{"auth of 15 bytes", func(s *PushSubscription) { s.Auth = enc(make([]byte, 15)) }, "auth is not a 16-byte"},
		{"auth of 17 bytes", func(s *PushSubscription) { s.Auth = enc(make([]byte, 17)) }, "auth is not a 16-byte"},
		{"auth with padding", func(s *PushSubscription) { s.Auth += "=" }, "auth is not a 16-byte"},
		{"auth with a newline in it", func(s *PushSubscription) { s.Auth = s.Auth[:10] + "\n" + s.Auth[10:] }, "auth is not a 16-byte"},
		{"auth that is not base64", func(s *PushSubscription) { s.Auth = "!!!!" }, "auth is not a 16-byte"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sub := good
			tc.mutate(&sub)

			assert.ErrorContains(t, ValidatePushSubscription(sub), tc.want)
		})
	}
}

// webHostClock is the clock a web model reads (webState.now): a test moves it
// by hand.
type webHostClock struct{ at time.Time }

func (c *webHostClock) now() time.Time { return c.at }

// webHostModel is a model with the web enabled through the production path
// (ConfigureWeb), over an isolated global dir, reading a clock the test moves.
func webHostModel(t *testing.T) (*Model, *webHostClock) {
	t.Helper()
	t.Setenv(config.EnvGlobalDir, t.TempDir())
	m := NewForTest(Options{})
	clk := &webHostClock{at: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)}
	m.web.now = clk.now
	require.NoError(t, m.ConfigureWeb("127.0.0.1:7420", "https://loom.example.com"))
	return m, clk
}

// webHostPair pairs a device through the production path (PairDevice, then
// RedeemPairing) and returns its cookie token and the hash DeviceByToken takes.
func webHostPair(t *testing.T, m *Model, name string) (token string, hash [32]byte) {
	t.Helper()
	p, err := m.PairDevice(name)
	require.NoError(t, err)
	got, token, err := m.RedeemPairing(p.Code)
	require.NoError(t, err)
	require.Equal(t, name, got)
	raw, err := base64.RawURLEncoding.DecodeString(token)
	require.NoError(t, err)
	return token, sha256.Sum256(raw)
}

// webHostWrongCode is a well-formed code that is not right.
func webHostWrongCode(right string) string {
	if right == "22222222" {
		return "33333333"
	}
	return "22222222"
}

// webHostFile is the part of web.json the tests read back.
type webHostFile struct {
	Devices []struct {
		Name        string `json:"name"`
		TokenSHA256 string `json:"token_sha256"`
		LastSeen    string `json:"last_seen"`
		Notify      struct {
			Waiting     bool   `json:"waiting"`
			Finished    bool   `json:"finished"`
			PausedUntil string `json:"paused_until"`
		} `json:"notify"`
		Push struct {
			Endpoint string `json:"endpoint"`
		} `json:"push"`
		PushState string `json:"push_state"`
	} `json:"devices"`
}

// webHostFileOf reads web.json back from the isolated global dir, parsed and
// as text.
func webHostFileOf(t *testing.T) (webHostFile, string) {
	t.Helper()
	dir, err := config.GetGlobalConfigDir()
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(dir, WebFile))
	require.NoError(t, err)
	var f webHostFile
	require.NoError(t, json.Unmarshal(data, &f))
	return f, string(data)
}

// webHostBreakWrites makes web.json unwritable by putting a directory where
// the file goes: the atomic write's rename onto it fails, whoever runs the
// test. The returned func puts the file back.
func webHostBreakWrites(t *testing.T) (restore func()) {
	t.Helper()
	dir, err := config.GetGlobalConfigDir()
	require.NoError(t, err)
	path := filepath.Join(dir, WebFile)
	saved, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, os.Remove(path))
	require.NoError(t, os.Mkdir(path, 0o700))
	return func() {
		require.NoError(t, os.Remove(path))
		require.NoError(t, os.WriteFile(path, saved, 0o600))
	}
}

func TestWebHost_RedeemPairingTradesACodeForADevice(t *testing.T) {
	m, clk := webHostModel(t)
	p, err := m.PairDevice("iPhone")
	require.NoError(t, err)
	typed := strings.ToLower(p.Code[:4] + "-" + p.Code[4:]) // as typed: any case, with a dash
	clk.at = clk.at.Add(time.Minute)

	name, token, err := m.RedeemPairing(typed)

	require.NoError(t, err)
	assert.Equal(t, "iPhone", name, "the device takes the name the code was made for")
	raw, err := base64.RawURLEncoding.DecodeString(token)
	require.NoError(t, err, "the token is unpadded base64url")
	require.Len(t, raw, 32)
	found, ok := m.DeviceByToken(sha256.Sum256(raw))
	assert.True(t, ok)
	assert.Equal(t, "iPhone", found)

	require.Len(t, m.Web().Devices, 1)
	dev := m.Web().Devices[0]
	assert.Equal(t, "iPhone", dev.Name)
	assert.True(t, dev.Paired.Equal(clk.at))
	assert.True(t, dev.LastSeen.Equal(clk.at))
	assert.Equal(t, NotifySettings{Waiting: true, Finished: true}, dev.Notify)
	assert.Equal(t, PushNone, dev.Push)

	onDisk, text := webHostFileOf(t)
	require.Len(t, onDisk.Devices, 1)
	sum := sha256.Sum256(raw)
	assert.Equal(t, hex.EncodeToString(sum[:]), onDisk.Devices[0].TokenSHA256, "the file holds the token's hash")
	assert.NotContains(t, text, token, "and never the token")
}

func TestWebHost_RedeemPairingGivesACodeOneUse(t *testing.T) {
	m, _ := webHostModel(t)
	p, err := m.PairDevice("iPhone")
	require.NoError(t, err)
	_, _, err = m.RedeemPairing(p.Code)
	require.NoError(t, err)

	_, _, err = m.RedeemPairing(p.Code)

	assert.ErrorIs(t, err, ErrPairingCode)
	assert.Len(t, m.Web().Devices, 1)
}

func TestWebHost_RedeemPairingRefusesAnExpiredCode(t *testing.T) {
	for _, tc := range []struct {
		name  string
		after time.Duration
		want  error
	}{
		{"a second before it expires", PairingTTL - time.Second, nil},
		{"a second after it expires", PairingTTL + time.Second, ErrPairingCode},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, clk := webHostModel(t)
			p, err := m.PairDevice("iPhone")
			require.NoError(t, err)
			clk.at = clk.at.Add(tc.after)

			_, _, err = m.RedeemPairing(p.Code)

			if tc.want == nil {
				assert.NoError(t, err)
				return
			}
			assert.ErrorIs(t, err, tc.want)
			assert.Empty(t, m.Web().Devices)
		})
	}
}

// Five wrong codes since the last pair void every pending code, and the app is
// told to pair again: the right code is refused too, until the next PairDevice.
func TestWebHost_FiveWrongCodesLockPairingUntilTheNextPair(t *testing.T) {
	m, _ := webHostModel(t)
	p, err := m.PairDevice("iPhone")
	require.NoError(t, err)
	wrong := webHostWrongCode(p.Code)
	wrongs := []string{wrong, "abc", "", "0000-0000", wrong} // a malformed code is a wrong one
	require.Len(t, wrongs, PairingMaxMisses)

	for i, code := range wrongs[:PairingMaxMisses-1] {
		_, _, err = m.RedeemPairing(code)
		require.ErrorIs(t, err, ErrPairingCode, "wrong code %d", i+1)
	}
	_, _, err = m.RedeemPairing(wrongs[PairingMaxMisses-1])
	assert.ErrorIs(t, err, ErrPairingLocked, "the fifth voids every pending code")
	_, _, err = m.RedeemPairing(p.Code)
	assert.ErrorIs(t, err, ErrPairingLocked, "the right code is refused too")
	assert.Empty(t, m.Web().Devices)

	again, err := m.PairDevice("iPhone")
	require.NoError(t, err)
	name, _, err := m.RedeemPairing(again.Code)
	require.NoError(t, err, "the next pair lifts the lock")
	assert.Equal(t, "iPhone", name)
}

func TestWebHost_FourWrongCodesLockNothing(t *testing.T) {
	m, _ := webHostModel(t)
	p, err := m.PairDevice("iPhone")
	require.NoError(t, err)
	for i := 1; i < PairingMaxMisses; i++ {
		_, _, err = m.RedeemPairing(webHostWrongCode(p.Code))
		require.ErrorIs(t, err, ErrPairingCode, "wrong code %d", i)
	}

	name, _, err := m.RedeemPairing(p.Code)

	require.NoError(t, err)
	assert.Equal(t, "iPhone", name)
}

func TestWebHost_RedeemPairingWhileTheWebIsOff(t *testing.T) {
	t.Run("never enabled", func(t *testing.T) {
		t.Setenv(config.EnvGlobalDir, t.TempDir()) // a bare model's saveWeb would write under the global dir
		m := NewForTest(Options{})

		_, _, err := m.RedeemPairing("23456789")

		assert.ErrorIs(t, err, ErrWebOff)
	})
	t.Run("turned off with a code pending", func(t *testing.T) {
		m, _ := webHostModel(t)
		p, err := m.PairDevice("iPhone")
		require.NoError(t, err)
		require.NoError(t, m.ConfigureWeb("", "https://loom.example.com"))

		_, _, err = m.RedeemPairing(p.Code)

		assert.ErrorIs(t, err, ErrWebOff)
		assert.Empty(t, m.Web().Devices)
	})
}

// A code made while there was room can be redeemed after the room is gone,
// and a name taken in between is not given twice. No PairDevice rule reaches
// either state alone, so the fixture fills the device list directly.
func TestWebHost_RedeemPairingChecksTheDeviceListAgain(t *testing.T) {
	for _, tc := range []struct {
		name    string
		devices int
		want    error
	}{
		{"one place left", MaxWebDevices - 1, nil},
		{"none left", MaxWebDevices, ErrTooManyDevices},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := webHostModel(t)
			p, err := m.PairDevice("over")
			require.NoError(t, err)
			for i := 0; i < tc.devices; i++ {
				m.web.devices = append(m.web.devices, &webDevice{name: fmt.Sprintf("d%02d", i), tokenHash: [32]byte{byte(i + 1)}})
			}

			_, _, err = m.RedeemPairing(p.Code)

			if tc.want == nil {
				assert.NoError(t, err)
				assert.Len(t, m.web.devices, tc.devices+1)
				return
			}
			assert.ErrorIs(t, err, tc.want)
			assert.Len(t, m.web.devices, tc.devices)
		})
	}
	t.Run("a name taken in between", func(t *testing.T) {
		m, _ := webHostModel(t)
		p, err := m.PairDevice("iPad")
		require.NoError(t, err)
		taken := [32]byte{1}
		m.web.devices = append(m.web.devices, &webDevice{name: "iPad", tokenHash: taken})

		_, _, err = m.RedeemPairing(p.Code)

		assert.ErrorIs(t, err, ErrDeviceExists)
		assert.Len(t, m.web.devices, 1, "no second device")
		got, ok := m.DeviceByToken(taken)
		assert.True(t, ok, "the first device keeps its token")
		assert.Equal(t, "iPad", got)
	})
}

// A write that fails leaves no device behind and keeps the code, so the phone
// can try again once the disk answers.
func TestWebHost_RedeemPairingKeepsTheCodeWhenTheWriteFails(t *testing.T) {
	m, _ := webHostModel(t)
	p, err := m.PairDevice("iPhone")
	require.NoError(t, err)
	restore := webHostBreakWrites(t)

	_, _, err = m.RedeemPairing(p.Code)

	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrPairingCode, "the code was right")
	assert.Empty(t, m.Web().Devices, "no device without its record")

	restore()
	name, _, err := m.RedeemPairing(p.Code)
	require.NoError(t, err, "the code was kept")
	assert.Equal(t, "iPhone", name)
}

func TestWebHost_DeviceByToken(t *testing.T) {
	m, _ := webHostModel(t)
	phoneToken, phone := webHostPair(t, m, "iPhone")
	_, pad := webHostPair(t, m, "iPad")

	name, ok := m.DeviceByToken(phone)
	assert.True(t, ok)
	assert.Equal(t, "iPhone", name)
	name, ok = m.DeviceByToken(pad)
	assert.True(t, ok)
	assert.Equal(t, "iPad", name)
	_, ok = m.DeviceByToken(sha256.Sum256([]byte("nobody")))
	assert.False(t, ok)
	_, ok = m.DeviceByToken(sha256.Sum256([]byte(phoneToken)))
	assert.False(t, ok, "the hash is of the 32 raw bytes, not of the cookie's text")

	// A record with no hash (a hand-edited web.json) answers to no one's.
	m.web.devices = append(m.web.devices, &webDevice{name: "blank"})
	_, ok = m.DeviceByToken([32]byte{})
	assert.False(t, ok)

	require.NoError(t, m.RevokeDevice("iPhone"))
	_, ok = m.DeviceByToken(phone)
	assert.False(t, ok, "a revoked device is found no more")
}

func TestWebHost_TouchDeviceWritesAtMostOnceAMinute(t *testing.T) {
	m, clk := webHostModel(t)
	webHostPair(t, m, "iPhone")
	paired := clk.at
	lastSeen := func() time.Time { return m.Web().Devices[0].LastSeen }

	clk.at = paired.Add(time.Minute - time.Second)
	assert.False(t, m.TouchDevice("iPhone"))
	assert.True(t, lastSeen().Equal(paired), "within a minute of the last time nothing changes")

	clk.at = paired.Add(time.Minute)
	assert.True(t, m.TouchDevice("iPhone"))
	assert.True(t, lastSeen().Equal(clk.at))
	onDisk, _ := webHostFileOf(t)
	seen, err := time.Parse(time.RFC3339, onDisk.Devices[0].LastSeen)
	require.NoError(t, err)
	assert.True(t, seen.Equal(clk.at), "the file holds it")

	touched := clk.at
	clk.at = clk.at.Add(30 * time.Second)
	assert.False(t, m.TouchDevice("iPhone"), "a minute counts from the last write")
	assert.True(t, lastSeen().Equal(touched))
	assert.False(t, m.TouchDevice("nobody"), "an unknown name is no one")
}

func webHostPtr[T any](v T) *T { return &v }

func TestWebHost_SetNotifyAppliesOnlyTheFieldsSet(t *testing.T) {
	m, clk := webHostModel(t)
	webHostPair(t, m, "iPhone")
	until := clk.at.Add(time.Hour)

	dev, err := m.SetNotify("iPhone", NotifyChange{Waiting: webHostPtr(false), PausedUntil: &until})

	require.NoError(t, err)
	assert.False(t, dev.Notify.Waiting)
	assert.True(t, dev.Notify.Finished, "a nil field is left as it is")
	assert.True(t, dev.Notify.PausedUntil.Equal(until))
	assert.Equal(t, m.Web().Devices[0], dev, "the device as stored")
	onDisk, _ := webHostFileOf(t)
	assert.False(t, onDisk.Devices[0].Notify.Waiting)
	assert.True(t, onDisk.Devices[0].Notify.Finished)
	assert.NotEmpty(t, onDisk.Devices[0].Notify.PausedUntil)

	dev, err = m.SetNotify("iPhone", NotifyChange{Finished: webHostPtr(false)})
	require.NoError(t, err)
	assert.False(t, dev.Notify.Waiting, "a setting not named stands")
	assert.False(t, dev.Notify.Finished)
	assert.True(t, dev.Notify.PausedUntil.Equal(until), "so does a pause not named")

	dev, err = m.SetNotify("iPhone", NotifyChange{Waiting: webHostPtr(true), PausedUntil: &time.Time{}})
	require.NoError(t, err)
	assert.True(t, dev.Notify.Waiting)
	assert.True(t, dev.Notify.PausedUntil.IsZero(), "a zero time ends the pause")
	onDisk, _ = webHostFileOf(t)
	assert.Empty(t, onDisk.Devices[0].Notify.PausedUntil)
}

func TestWebHost_SetNotifyRefusesAnUnknownDeviceAndChangesNothingWhenTheWriteFails(t *testing.T) {
	m, _ := webHostModel(t)
	webHostPair(t, m, "iPhone")
	before := m.Web().Devices[0]

	_, err := m.SetNotify("nobody", NotifyChange{Waiting: webHostPtr(false)})
	assert.ErrorIs(t, err, ErrNoDevice)

	webHostBreakWrites(t)
	_, err = m.SetNotify("iPhone", NotifyChange{Waiting: webHostPtr(false)})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNoDevice)
	assert.Equal(t, before, m.Web().Devices[0], "the setting is put back")
}

func TestWebHost_SetPushSubscriptionStoresAValidOne(t *testing.T) {
	m, _ := webHostModel(t)
	webHostPair(t, m, "iPhone")
	sub := webHostSub(t, "web.push.apple.com")

	dev, err := m.SetPushSubscription("iPhone", sub)

	require.NoError(t, err)
	assert.Equal(t, PushOK, dev.Push)
	assert.Equal(t, m.Web().Devices[0], dev, "the device as stored")
	assert.Equal(t, []PushTarget{{Device: "iPhone", Notify: NotifySettings{Waiting: true, Finished: true}, Subscription: sub}}, m.PushTargets())
	onDisk, _ := webHostFileOf(t)
	assert.Equal(t, sub.Endpoint, onDisk.Devices[0].Push.Endpoint)
	assert.Equal(t, "ok", onDisk.Devices[0].PushState)
}

func TestWebHost_SetPushSubscriptionRefusesWhatItShouldNotKeep(t *testing.T) {
	m, _ := webHostModel(t)
	webHostPair(t, m, "iPhone")
	good := webHostSub(t, "web.push.apple.com")

	elsewhere := good
	elsewhere.Endpoint = "https://push.example.com/abc"
	_, err := m.SetPushSubscription("iPhone", elsewhere)
	assert.ErrorContains(t, err, "no push service")
	assert.Equal(t, PushNone, m.Web().Devices[0].Push, "nothing is stored")
	assert.Empty(t, m.PushTargets())

	_, err = m.SetPushSubscription("nobody", good)
	assert.ErrorIs(t, err, ErrNoDevice)

	webHostBreakWrites(t)
	_, err = m.SetPushSubscription("iPhone", good)
	require.Error(t, err)
	assert.Equal(t, PushNone, m.Web().Devices[0].Push, "a failed write puts it back")
	assert.Empty(t, m.PushTargets())
}

func TestWebHost_PushTargetsAreTheDevicesWithALiveSubscription(t *testing.T) {
	m, _ := webHostModel(t)
	for _, name := range []string{"a-ok", "b-none", "c-expired", "d-failing"} {
		webHostPair(t, m, name)
	}
	for _, name := range []string{"a-ok", "c-expired", "d-failing"} {
		_, err := m.SetPushSubscription(name, webHostSub(t, "fcm.googleapis.com"))
		require.NoError(t, err)
	}
	m.PushResult("c-expired", PushExpired)
	m.PushResult("d-failing", PushFailing(503))

	targets := m.PushTargets()

	require.Len(t, targets, 2, "no subscription, no target; an expired one is not tried")
	assert.Equal(t, "a-ok", targets[0].Device)
	assert.Equal(t, "d-failing", targets[1].Device, "a failing one is tried again")
}

func TestWebHost_PushResult(t *testing.T) {
	m, _ := webHostModel(t)
	webHostPair(t, m, "iPhone")
	_, err := m.SetPushSubscription("iPhone", webHostSub(t, "web.push.apple.com"))
	require.NoError(t, err)
	push := func() PushState { return m.Web().Devices[0].Push }

	assert.True(t, m.PushResult("iPhone", PushFailing(503)))
	assert.Equal(t, PushFailing(503), push())
	assert.Len(t, m.PushTargets(), 1, "a failing subscription is tried again")
	assert.False(t, m.PushResult("iPhone", PushFailing(503)), "the same result changes nothing")

	assert.True(t, m.PushResult("iPhone", PushOK))
	assert.Equal(t, PushOK, push())

	assert.True(t, m.PushResult("iPhone", PushExpired))
	assert.Equal(t, PushExpired, push())
	assert.Empty(t, m.PushTargets(), "an expired subscription is dropped")
	onDisk, _ := webHostFileOf(t)
	assert.Empty(t, onDisk.Devices[0].Push.Endpoint)
	assert.Equal(t, "expired", onDisk.Devices[0].PushState)

	assert.False(t, m.PushResult("iPhone", PushFailing(500)), "a late result of another push leaves the expiry standing")
	assert.Equal(t, PushExpired, push())
	assert.False(t, m.PushResult("nobody", PushOK), "a revoked device is no one")

	_, err = m.SetPushSubscription("iPhone", webHostSub(t, "web.push.apple.com"))
	require.NoError(t, err)
	assert.Equal(t, PushOK, push(), "a new subscription revives the device")
	assert.Len(t, m.PushTargets(), 1)
}

func TestWebHost_VAPIDKey(t *testing.T) {
	t.Run("before the first enable", func(t *testing.T) {
		t.Setenv(config.EnvGlobalDir, t.TempDir()) // a bare model's saveWeb would write under the global dir
		m := NewForTest(Options{})

		_, err := m.VAPIDKey()

		assert.ErrorIs(t, err, ErrWebOff)
	})
	t.Run("after it", func(t *testing.T) {
		m, _ := webHostModel(t)

		key, err := m.VAPIDKey()

		require.NoError(t, err)
		require.Len(t, key.Private, 32)
		require.Len(t, key.Public, 65)
		priv, err := ecdh.P256().NewPrivateKey(key.Private)
		require.NoError(t, err)
		assert.Equal(t, key.Public, priv.PublicKey().Bytes(), "the pair belongs together")

		want := append([]byte(nil), key.Private...)
		key.Private[0] ^= 0xff
		again, err := m.VAPIDKey()
		require.NoError(t, err)
		assert.Equal(t, want, again.Private, "a caller's edit does not reach the model")
	})
	t.Run("kept when the web is turned off", func(t *testing.T) {
		m, _ := webHostModel(t)
		before, err := m.VAPIDKey()
		require.NoError(t, err)
		require.NoError(t, m.ConfigureWeb("", "https://loom.example.com"))

		after, err := m.VAPIDKey()

		require.NoError(t, err)
		assert.Equal(t, before, after)
	})
}

func TestWebHost_WebServingSetsWhatTheViewShows(t *testing.T) {
	m, _ := webHostModel(t)
	m.Drain()
	rev := m.Web().Rev
	require.NotZero(t, rev, "fixture: the ConfigureWeb that enabled the web bumped it")

	assert.True(t, m.WebServing("127.0.0.1:7420", nil))
	v := m.Web()
	assert.True(t, v.Serving)
	assert.Equal(t, "127.0.0.1:7420", v.Addr)
	assert.Empty(t, v.Error)
	assert.Equal(t, rev, v.Rev, "the report is no new setting: Rev is the layer's cue to rebind, and only ConfigureWeb moves it")
	raised, _ := noticesAndStarts(m.Drain().Events)
	assert.Empty(t, raised, "listening is no news")

	assert.False(t, m.WebServing("127.0.0.1:7420", nil), "the same state changes nothing")

	assert.True(t, m.WebServing("", nil), "the listener closed")
	v = m.Web()
	assert.False(t, v.Serving)
	assert.Empty(t, v.Addr)
	assert.Equal(t, rev, v.Rev, "closing is a report too")
}

func TestWebHost_WebServingRaisesOneNoticePerDistinctError(t *testing.T) {
	m, _ := webHostModel(t)
	m.Drain()
	rev := m.Web().Rev
	require.NotZero(t, rev, "fixture: the ConfigureWeb that enabled the web bumped it")
	inUse := errors.New("listen tcp 127.0.0.1:7420: bind: address already in use")
	denied := errors.New("listen tcp 127.0.0.1:80: bind: permission denied")

	assert.True(t, m.WebServing("", inUse))
	v := m.Web()
	assert.False(t, v.Serving)
	assert.Empty(t, v.Addr)
	assert.Contains(t, v.Error, "address already in use")
	assert.Equal(t, rev, v.Rev, "a failed bind does not bump Rev, or the layer would retry it in a loop")
	raised, _ := noticesAndStarts(m.Drain().Events)
	require.Len(t, raised, 1)
	assert.ErrorContains(t, raised[0].Err, "address already in use")

	assert.False(t, m.WebServing("", inUse), "the same error again changes nothing")
	raised, _ = noticesAndStarts(m.Drain().Events)
	assert.Empty(t, raised, "and is told once")

	assert.True(t, m.WebServing("", denied))
	raised, _ = noticesAndStarts(m.Drain().Events)
	require.Len(t, raised, 1, "a different error is told")
	assert.ErrorContains(t, raised[0].Err, "permission denied")

	assert.True(t, m.WebServing("127.0.0.1:7420", nil))
	assert.Empty(t, m.Web().Error, "serving clears the reason")
	m.Drain()

	m.WebServing("", inUse)
	raised, _ = noticesAndStarts(m.Drain().Events)
	require.Len(t, raised, 1, "an error that comes back after serving is told again")
	assert.Equal(t, rev, m.Web().Rev, "none of the reports moved Rev")
}

// The model's web state reaches clients as ModelChanged on the next Sync, with
// no event of its own.
func TestWebHost_AChangeReachesClientsThroughTheModelView(t *testing.T) {
	m, _ := webHostModel(t)
	m.Sync() // the first publish

	m.WebServing("127.0.0.1:7420", nil)

	var view *ModelView
	for _, ev := range m.Sync().Events {
		if mc, ok := ev.(ModelChanged); ok {
			v := mc.View
			view = &v
		}
	}
	require.NotNil(t, view, "the Sync after the change carries the model view")
	assert.True(t, view.Web.Serving)
}

// webHostLoop runs a loop over a web model with one device, "iPhone", paired
// and published, and no wake waiting.
func webHostLoop(t *testing.T) (*Loop, *webHostClock, [32]byte) {
	t.Helper()
	m, clk := webHostModel(t)
	_, hash := webHostPair(t, m, "iPhone")
	l := StartForTest(m)
	t.Cleanup(l.Stop)
	l.Sync() // the first publish
	webHostDrainWakes(l)
	return l, clk, hash
}

func webHostDrainWakes(l *Loop) {
	for len(l.Wakes()) > 0 {
		<-l.Wakes()
	}
}

func webHostDevice(v WebView, name string) WebDevice {
	for _, d := range v.Devices {
		if d.Name == name {
			return d
		}
	}
	return WebDevice{}
}

// Each call that changes what a client sees wakes the client, and the change
// reaches it as ModelChanged on the Sync that follows.
func TestWebHostLoop_AChangeWakesTheClientAndReachesTheModelView(t *testing.T) {
	inUse := errors.New("listen tcp 127.0.0.1:7420: bind: address already in use")
	cases := []struct {
		name string
		prep func(t *testing.T, l *Loop, clk *webHostClock) (act func())
		want func(t *testing.T, v WebView, events []Event)
	}{
		{
			name: "WebServing",
			prep: func(t *testing.T, l *Loop, clk *webHostClock) func() {
				return func() { l.WebServing("127.0.0.1:7420", nil) }
			},
			want: func(t *testing.T, v WebView, _ []Event) { assert.True(t, v.Serving) },
		},
		{
			name: "WebServing with an error also queues its Notice",
			prep: func(t *testing.T, l *Loop, clk *webHostClock) func() {
				return func() { l.WebServing("", inUse) }
			},
			want: func(t *testing.T, v WebView, events []Event) {
				assert.Contains(t, v.Error, "address already in use")
				raised, _ := noticesAndStarts(events)
				require.Len(t, raised, 1)
				assert.ErrorContains(t, raised[0].Err, "address already in use")
			},
		},
		{
			name: "RedeemPairing",
			prep: func(t *testing.T, l *Loop, clk *webHostClock) func() {
				p, err := l.PairDevice("iPad")
				require.NoError(t, err)
				return func() {
					name, _, err := l.RedeemPairing(p.Code)
					require.NoError(t, err)
					assert.Equal(t, "iPad", name)
				}
			},
			want: func(t *testing.T, v WebView, _ []Event) { assert.Equal(t, "iPad", webHostDevice(v, "iPad").Name) },
		},
		{
			name: "TouchDevice a minute after the last time",
			prep: func(t *testing.T, l *Loop, clk *webHostClock) func() {
				return func() {
					clk.at = clk.at.Add(time.Minute)
					l.TouchDevice("iPhone")
				}
			},
			want: func(t *testing.T, v WebView, _ []Event) {
				assert.True(t, webHostDevice(v, "iPhone").LastSeen.After(webHostDevice(v, "iPhone").Paired))
			},
		},
		{
			name: "SetNotify",
			prep: func(t *testing.T, l *Loop, clk *webHostClock) func() {
				return func() {
					_, err := l.SetNotify("iPhone", NotifyChange{Waiting: webHostPtr(false)})
					require.NoError(t, err)
				}
			},
			want: func(t *testing.T, v WebView, _ []Event) { assert.False(t, webHostDevice(v, "iPhone").Notify.Waiting) },
		},
		{
			name: "SetPushSubscription",
			prep: func(t *testing.T, l *Loop, clk *webHostClock) func() {
				return func() {
					_, err := l.SetPushSubscription("iPhone", webHostSub(t, "web.push.apple.com"))
					require.NoError(t, err)
				}
			},
			want: func(t *testing.T, v WebView, _ []Event) { assert.Equal(t, PushOK, webHostDevice(v, "iPhone").Push) },
		},
		{
			name: "PushResult",
			prep: func(t *testing.T, l *Loop, clk *webHostClock) func() {
				_, err := l.SetPushSubscription("iPhone", webHostSub(t, "web.push.apple.com"))
				require.NoError(t, err)
				return func() { l.PushResult("iPhone", PushExpired) }
			},
			want: func(t *testing.T, v WebView, _ []Event) {
				assert.Equal(t, PushExpired, webHostDevice(v, "iPhone").Push)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l, clk, _ := webHostLoop(t)
			act := tc.prep(t, l, clk)
			l.Sync()
			webHostDrainWakes(l)

			act()

			select {
			case <-l.Wakes():
			case <-time.After(5 * time.Second):
				t.Fatal("no wake after the call")
			}
			events := l.Sync()
			var view *WebView
			for _, ev := range events {
				if mc, ok := ev.(ModelChanged); ok {
					v := mc.View.Web
					view = &v
				}
			}
			require.NotNil(t, view, "the Sync after the wake carries the model view")
			tc.want(t, *view, events)
		})
	}
}

// A call that changed nothing wakes no client: a wake has the server build and
// diff every view, and TouchDevice runs on every authenticated request.
func TestWebHostLoop_ACallThatChangesNothingWakesNoClient(t *testing.T) {
	l, _, hash := webHostLoop(t)

	name, ok := l.DeviceByToken(hash)
	assert.True(t, ok)
	assert.Equal(t, "iPhone", name)
	_, ok = l.DeviceByToken([32]byte{9})
	assert.False(t, ok)
	assert.Empty(t, l.PushTargets())
	_, err := l.VAPIDKey()
	require.NoError(t, err)
	l.TouchDevice("iPhone") // no minute has passed since the pairing
	l.TouchDevice("nobody")
	l.PushResult("nobody", PushExpired)
	l.PushResult("iPhone", PushExpired) // it has no subscription
	l.WebServing("", nil)               // it was not serving
	_, _, err = l.RedeemPairing("23456789")
	assert.ErrorIs(t, err, ErrPairingCode, "a refusal reaches the caller")
	_, err = l.SetNotify("nobody", NotifyChange{})
	assert.ErrorIs(t, err, ErrNoDevice)
	_, err = l.SetPushSubscription("nobody", webHostSub(t, "web.push.apple.com"))
	assert.ErrorIs(t, err, ErrNoDevice)

	assert.Empty(t, l.Wakes(), "no wake waits")
}

// A call after Stop runs nothing, so the ones that return an error must not
// answer with a success.
func TestWebHostLoop_AfterStopNoCallSucceeds(t *testing.T) {
	l, _, hash := webHostLoop(t)
	l.Stop()

	_, _, err := l.RedeemPairing("23456789")
	assert.ErrorIs(t, err, ErrWebOff, "a stopped loop pairs no one")
	_, err = l.SetNotify("iPhone", NotifyChange{})
	assert.Error(t, err)
	_, err = l.SetPushSubscription("iPhone", webHostSub(t, "web.push.apple.com"))
	assert.Error(t, err)
	_, err = l.VAPIDKey()
	assert.Error(t, err, "and hands out no key")
	_, ok := l.DeviceByToken(hash)
	assert.False(t, ok, "it knows no device")
}
