package core

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"

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
