package core

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
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
