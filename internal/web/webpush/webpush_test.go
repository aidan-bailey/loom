package webpush

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/asn1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/internal/testenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain points LOOM_HOME and LOOM_GLOBAL_DIR at throwaway directories:
// this package imports core, so its tests can reach config.
func TestMain(m *testing.M) {
	cleanup := testenv.MustIsolateLoomDirs()
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// RFC 8291, Appendix A ("Intermediate Values for Encryption") and section 5
// ("Push Message Encryption Example"), base64url without padding.
const (
	rfcPlaintext  = "V2hlbiBJIGdyb3cgdXAsIEkgd2FudCB0byBiZSBhIHdhdGVybWVsb24"
	rfcAuthSecret = "BTBZMqHH6r4Tts7J_aSIgg"
	rfcUAPrivate  = "q1dXpw3UpT5VOmu_cf_v6ih07Aems3njxI-JWgLcM94"
	rfcUAPublic   = "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4"
	rfcASPrivate  = "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"
	rfcASPublic   = "BP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A8"
	rfcSalt       = "DGv6ra1nlYgDCS1FRnbzlw"
	rfcECDHSecret = "kyrL1jIIOHEzg3sM2ZWRHDRB62YACZhhSlknJ672kSs"
	rfcCEK        = "oIhVW04MRdy2XN9CiKLxTg"
	rfcNonce      = "4h_95klXJ5E_qnoN"
	rfcBody       = "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN"
)

// b64 decodes unpadded base64url, dropping the whitespace the RFCs wrap it with.
func b64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(strings.Join(strings.Fields(s), ""))
	require.NoError(t, err)
	return b
}

func b64s(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// newSub makes a subscription the way a browser does, and returns the
// receiver's private key so a test can open what Encrypt made.
func newSub(t *testing.T) (Subscription, *ecdh.PrivateKey) {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	require.NoError(t, err)
	auth := make([]byte, authLen)
	_, err = io.ReadFull(rand.Reader, auth)
	require.NoError(t, err)
	return Subscription{
		Endpoint: "https://web.push.apple.com/wpush/v2/abc",
		P256DH:   key.PublicKey().Bytes(),
		Auth:     auth,
	}, key
}

// open is the user agent's side of RFC 8291: it parses the header, derives
// the keys from the receiver's private key and decrypts the one record.
func open(receiver *ecdh.PrivateKey, auth, body []byte) ([]byte, error) {
	if len(body) < headerLen+1+tagLen {
		return nil, errors.New("body shorter than a header and an empty record")
	}
	salt := body[:saltLen]
	if rs := binary.BigEndian.Uint32(body[saltLen : saltLen+4]); rs != recordSize {
		return nil, fmt.Errorf("record size %d", rs)
	}
	if body[saltLen+4] != pointLen {
		return nil, errors.New("key ID is not a P-256 point")
	}
	keyID := body[saltLen+5 : headerLen]
	sender, err := ecdh.P256().NewPublicKey(keyID)
	if err != nil {
		return nil, err
	}
	secret, err := receiver.ECDH(sender)
	if err != nil {
		return nil, err
	}
	cek, nonce, err := contentKeys(secret, auth, receiver.PublicKey().Bytes(), keyID, salt)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, nonce, body[headerLen:], nil)
	if err != nil {
		return nil, err
	}
	if len(plain) == 0 || plain[len(plain)-1] != paddingDelim {
		return nil, errors.New("padding delimiter missing")
	}
	return plain[:len(plain)-1], nil
}

func TestEncryptWith_RFC8291Example(t *testing.T) {
	receiver, err := ecdh.P256().NewPrivateKey(b64(t, rfcUAPrivate))
	require.NoError(t, err)
	require.Equal(t, b64(t, rfcUAPublic), receiver.PublicKey().Bytes(), "the RFC's receiver key pair is consistent")
	sender, err := ecdh.P256().NewPrivateKey(b64(t, rfcASPrivate))
	require.NoError(t, err)
	require.Equal(t, b64(t, rfcASPublic), sender.PublicKey().Bytes(), "the RFC's sender key pair is consistent")
	plaintext := b64(t, rfcPlaintext)
	require.Equal(t, "When I grow up, I want to be a watermelon", string(plaintext))

	secret, err := sender.ECDH(receiver.PublicKey())
	require.NoError(t, err)
	assert.Equal(t, b64(t, rfcECDHSecret), secret, "the ECDH secret")

	cek, nonce, err := contentKeys(secret, b64(t, rfcAuthSecret), receiver.PublicKey().Bytes(), sender.PublicKey().Bytes(), b64(t, rfcSalt))
	require.NoError(t, err)
	assert.Equal(t, b64(t, rfcCEK), cek, "the content encryption key")
	assert.Equal(t, b64(t, rfcNonce), nonce, "the nonce")

	sub := Subscription{
		Endpoint: "https://push.example.net/push/JzLQ3raZJfFBR0aqvOMsLrt54w4rJUsV",
		P256DH:   b64(t, rfcUAPublic),
		Auth:     b64(t, rfcAuthSecret),
	}
	body, err := encryptWith(sub, plaintext, sender, b64(t, rfcSalt))
	require.NoError(t, err)
	assert.Equal(t, b64(t, rfcBody), body, "the whole push message body")

	got, err := open(receiver, sub.Auth, b64(t, rfcBody))
	require.NoError(t, err)
	assert.Equal(t, plaintext, got, "the RFC's own body opens with the receiver's key")
}

func TestEncryptWith_DependsOnEveryInput(t *testing.T) {
	receiver, err := ecdh.P256().NewPrivateKey(b64(t, rfcUAPrivate))
	require.NoError(t, err)
	sender, err := ecdh.P256().NewPrivateKey(b64(t, rfcASPrivate))
	require.NoError(t, err)
	other, err := ecdh.P256().GenerateKey(rand.Reader)
	require.NoError(t, err)
	plaintext := b64(t, rfcPlaintext)
	sub := Subscription{Endpoint: "https://push.example.net/p", P256DH: receiver.PublicKey().Bytes(), Auth: b64(t, rfcAuthSecret)}
	want := b64(t, rfcBody)

	t.Run("auth secret", func(t *testing.T) {
		s := sub
		s.Auth = bytes.Clone(sub.Auth)
		s.Auth[0] ^= 1
		got, err := encryptWith(s, plaintext, sender, b64(t, rfcSalt))
		require.NoError(t, err)
		assert.NotEqual(t, want, got)
	})
	t.Run("salt", func(t *testing.T) {
		salt := bytes.Clone(b64(t, rfcSalt))
		salt[0] ^= 1
		got, err := encryptWith(sub, plaintext, sender, salt)
		require.NoError(t, err)
		assert.NotEqual(t, want, got)
	})
	t.Run("sender key", func(t *testing.T) {
		got, err := encryptWith(sub, plaintext, other, b64(t, rfcSalt))
		require.NoError(t, err)
		assert.NotEqual(t, want, got)
	})
	t.Run("payload", func(t *testing.T) {
		got, err := encryptWith(sub, append(bytes.Clone(plaintext), '!'), sender, b64(t, rfcSalt))
		require.NoError(t, err)
		assert.NotEqual(t, want, got)
	})
	t.Run("salt of the wrong length", func(t *testing.T) {
		_, err := encryptWith(sub, plaintext, sender, b64(t, rfcSalt)[:15])
		assert.Error(t, err)
	})
}

func TestEncrypt_RoundTrip(t *testing.T) {
	sub, receiver := newSub(t)
	payload := []byte(`{"kind":"waiting","session":7}`)

	body, err := Encrypt(sub, payload)
	require.NoError(t, err)
	assert.Equal(t, headerLen+len(payload)+1+tagLen, len(body), "one record: header, payload, delimiter, tag")
	assert.Equal(t, []byte{0x00, 0x00, 0x10, 0x00}, body[saltLen:saltLen+4], "record size 4096")
	assert.Equal(t, byte(65), body[saltLen+4], "key ID length")
	assert.Equal(t, byte(4), body[saltLen+5], "the key ID is an uncompressed point")
	got, err := open(receiver, sub.Auth, body)
	require.NoError(t, err)
	assert.Equal(t, payload, got)

	again, err := Encrypt(sub, payload)
	require.NoError(t, err)
	assert.NotEqual(t, body[:saltLen], again[:saltLen], "a fresh salt each time")
	assert.NotEqual(t, body[saltLen+5:headerLen], again[saltLen+5:headerLen], "a fresh sender key each time")
	got, err = open(receiver, sub.Auth, again)
	require.NoError(t, err)
	assert.Equal(t, payload, got)
}

func TestEncrypt_OnlyTheReceiverCanOpenIt(t *testing.T) {
	sub, receiver := newSub(t)
	_, stranger := newSub(t)
	body, err := Encrypt(sub, []byte("secret"))
	require.NoError(t, err)

	_, err = open(receiver, sub.Auth, body)
	require.NoError(t, err, "control: the receiver opens it")

	_, err = open(stranger, sub.Auth, body)
	assert.Error(t, err, "another key pair")
	wrongAuth := bytes.Clone(sub.Auth)
	wrongAuth[0] ^= 1
	_, err = open(receiver, wrongAuth, body)
	assert.Error(t, err, "another authentication secret")
	for _, i := range []int{0, saltLen + 4, saltLen + 20, headerLen + 2, len(body) - 1} {
		bad := bytes.Clone(body)
		bad[i] ^= 1
		_, err = open(receiver, sub.Auth, bad)
		assert.Error(t, err, "byte %d flipped", i)
	}
}

func TestEncrypt_PayloadLimit(t *testing.T) {
	sub, receiver := newSub(t)

	full := bytes.Repeat([]byte("x"), maxPayload)
	body, err := Encrypt(sub, full)
	require.NoError(t, err)
	assert.Equal(t, recordSize, len(body), "3993 bytes of payload fill the 4096-byte record exactly")
	got, err := open(receiver, sub.Auth, body)
	require.NoError(t, err)
	assert.Equal(t, full, got)

	_, err = Encrypt(sub, bytes.Repeat([]byte("x"), maxPayload+1))
	assert.ErrorIs(t, err, errPayloadTooLarge)

	empty, err := Encrypt(sub, nil)
	require.NoError(t, err, "an empty payload is one delimiter and a tag")
	got, err = open(receiver, sub.Auth, empty)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestEncrypt_RefusesABadSubscription(t *testing.T) {
	good, _ := newSub(t)
	offCurve := bytes.Clone(good.P256DH)
	offCurve[len(offCurve)-1] ^= 0xff
	compressed := bytes.Clone(good.P256DH)
	compressed[0] = 0x02

	for name, sub := range map[string]Subscription{
		"no key":           {Endpoint: good.Endpoint, Auth: good.Auth},
		"short key":        {Endpoint: good.Endpoint, P256DH: good.P256DH[:64], Auth: good.Auth},
		"long key":         {Endpoint: good.Endpoint, P256DH: append(bytes.Clone(good.P256DH), 0), Auth: good.Auth},
		"point off curve":  {Endpoint: good.Endpoint, P256DH: offCurve, Auth: good.Auth},
		"not uncompressed": {Endpoint: good.Endpoint, P256DH: compressed, Auth: good.Auth},
		"no auth":          {Endpoint: good.Endpoint, P256DH: good.P256DH},
		"short auth":       {Endpoint: good.Endpoint, P256DH: good.P256DH, Auth: good.Auth[:15]},
		"long auth":        {Endpoint: good.Endpoint, P256DH: good.P256DH, Auth: append(bytes.Clone(good.Auth), 0)},
	} {
		t.Run(name, func(t *testing.T) {
			body, err := Encrypt(sub, []byte("x"))
			assert.Error(t, err)
			assert.Nil(t, body)
		})
	}

	_, err := Encrypt(good, []byte("x"))
	assert.NoError(t, err, "control: the unmodified subscription encrypts")
}

func TestParseSubscription(t *testing.T) {
	good, _ := newSub(t)
	valid := core.PushSubscription{
		Endpoint: good.Endpoint,
		P256DH:   b64s(good.P256DH),
		Auth:     b64s(good.Auth),
	}

	got, err := ParseSubscription(valid)
	require.NoError(t, err)
	assert.Equal(t, good, got)

	offCurve := bytes.Clone(good.P256DH)
	offCurve[len(offCurve)-1] ^= 0xff
	for name, mod := range map[string]func(s *core.PushSubscription){
		"empty endpoint":        func(s *core.PushSubscription) { s.Endpoint = "" },
		"http endpoint":         func(s *core.PushSubscription) { s.Endpoint = "http://web.push.apple.com/wpush/v2/abc" },
		"endpoint without host": func(s *core.PushSubscription) { s.Endpoint = "https:///wpush/v2/abc" },
		"endpoint not a URL":    func(s *core.PushSubscription) { s.Endpoint = "://nope" },
		"padded p256dh":         func(s *core.PushSubscription) { s.P256DH = base64.URLEncoding.EncodeToString(good.P256DH) },
		"p256dh not base64":     func(s *core.PushSubscription) { s.P256DH = "!!!" },
		"short p256dh":          func(s *core.PushSubscription) { s.P256DH = b64s(good.P256DH[:64]) },
		"p256dh off curve":      func(s *core.PushSubscription) { s.P256DH = b64s(offCurve) },
		"padded auth":           func(s *core.PushSubscription) { s.Auth = base64.URLEncoding.EncodeToString(good.Auth) },
		"auth not base64":       func(s *core.PushSubscription) { s.Auth = "!!!" },
		"short auth":            func(s *core.PushSubscription) { s.Auth = b64s(good.Auth[:15]) },
		"long auth":             func(s *core.PushSubscription) { s.Auth = b64s(append(bytes.Clone(good.Auth), 0)) },
	} {
		t.Run(name, func(t *testing.T) {
			s := valid
			mod(&s)
			got, err := ParseSubscription(s)
			assert.Error(t, err)
			assert.Equal(t, Subscription{}, got)
		})
	}
}

// RFC 8292, section 2.4: the JWT's first two segments for these claims, and
// the key that signed the whole token.
const (
	rfc8292Header = "eyJ0eXAiOiJKV1QiLCJhbGciOiJFUzI1NiJ9"
	rfc8292Claims = "eyJhdWQiOiJodHRwczovL3B1c2guZXhhbXBsZS5uZXQiLCJleHAiOjE0NTM1MjM3NjgsInN1YiI6Im1haWx0bzpwdXNoQGV4YW1wbGUuY29tIn0"
)

// newVAPIDKey makes a VAPID key the way the model does: the 32-byte scalar
// and the 65-byte uncompressed point.
func newVAPIDKey(t *testing.T) core.VAPIDKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	priv, err := k.Bytes()
	require.NoError(t, err)
	pub, err := k.PublicKey.Bytes()
	require.NoError(t, err)
	return core.VAPIDKey{Private: priv, Public: pub}
}

// splitAuth cuts an Authorization value "vapid t=<JWT>, k=<key>" into its parts.
func splitAuth(t *testing.T, header string) (jwt, k string) {
	t.Helper()
	rest, ok := strings.CutPrefix(header, "vapid t=")
	require.True(t, ok, header)
	jwt, k, ok = strings.Cut(rest, ", k=")
	require.True(t, ok, header)
	return jwt, k
}

// verifyJWT checks a compact ES256 JWT against a public key, as a push
// service does, and returns its claims.
func verifyJWT(t *testing.T, jwt string, pub []byte) (vapidClaims, bool) {
	t.Helper()
	parts := strings.Split(jwt, ".")
	require.Len(t, parts, 3, jwt)
	sig := b64(t, parts[2])
	require.Len(t, sig, 64, "ES256 signatures are r then s, 32 bytes each")
	pk, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), pub)
	require.NoError(t, err)
	der, err := asn1.Marshal(struct{ R, S *big.Int }{new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])})
	require.NoError(t, err)
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	var claims vapidClaims
	require.NoError(t, json.Unmarshal(b64(t, parts[1]), &claims))
	return claims, ecdsa.VerifyASN1(pk, digest[:], der)
}

func TestVAPIDSigningInput_MatchesRFC8292(t *testing.T) {
	got, err := vapidSigningInput("https://push.example.net", "mailto:push@example.com", 1453523768)
	require.NoError(t, err)
	assert.Equal(t, rfc8292Header+"."+rfc8292Claims, got)
}

func TestVAPIDAuth_SignsClaimsThatVerify(t *testing.T) {
	key := newVAPIDKey(t)
	now := time.Unix(1_700_000_000, 0)
	for name, tc := range map[string]struct{ endpoint, aud string }{
		"path and query dropped":                {"https://web.push.apple.com/wpush/v2/abc?x=1", "https://web.push.apple.com"},
		"port kept":                             {"https://fcm.googleapis.com:8443/fcm/send/abc", "https://fcm.googleapis.com:8443"},
		"wildcard host":                         {"https://eu.notify.windows.com/w/?token=abc", "https://eu.notify.windows.com"},
		"default port dropped":                  {"https://web.push.apple.com:443/wpush/v2/abc", "https://web.push.apple.com"},
		"upper-case host":                       {"https://Web.Push.Apple.COM/wpush/v2/abc", "https://web.push.apple.com"},
		"upper-case host, port":                 {"https://FCM.googleapis.com:8443/fcm/send/abc", "https://fcm.googleapis.com:8443"},
		"IPv6 host stays bracketed":             {"https://[2001:DB8::1]/push", "https://[2001:db8::1]"},
		"IPv6 host with a port stays bracketed": {"https://[2001:db8::1]:8443/push", "https://[2001:db8::1]:8443"},
	} {
		t.Run(name, func(t *testing.T) {
			header, err := VAPIDAuth(key, tc.endpoint, "https://loom.example.com", now)
			require.NoError(t, err)
			jwt, k := splitAuth(t, header)
			assert.Equal(t, b64s(key.Public), k, "k is the public key, base64url")
			assert.True(t, strings.HasPrefix(jwt, rfc8292Header+"."), "the header is typ JWT, alg ES256")

			claims, ok := verifyJWT(t, jwt, key.Public)
			assert.True(t, ok, "the signature verifies with the public key")
			assert.Equal(t, vapidClaims{
				Aud: tc.aud,
				Exp: 1_700_000_000 + 12*3600,
				Sub: "https://loom.example.com",
			}, claims)
		})
	}
}

// A leading zero byte of r or s must stay: dropping it (append(r.Bytes(),
// s.Bytes()...)) shortens the signature in about 1 of 128 tokens, which a
// push service rejects.
func TestJWSSignature_PadsEachIntegerTo32Bytes(t *testing.T) {
	got := jwsSignature(big.NewInt(1), big.NewInt(2))
	want := append(append(bytes.Repeat([]byte{0}, 31), 1), append(bytes.Repeat([]byte{0}, 31), 2)...)
	require.Len(t, got, 64)
	assert.Equal(t, want, got)
}

func TestVAPIDAuth_SignatureBindsTheClaimsAndTheKey(t *testing.T) {
	key, other := newVAPIDKey(t), newVAPIDKey(t)
	header, err := VAPIDAuth(key, "https://web.push.apple.com/wpush/v2/abc", "https://loom.example.com", time.Unix(1_700_000_000, 0))
	require.NoError(t, err)
	jwt, _ := splitAuth(t, header)
	parts := strings.Split(jwt, ".")

	_, ok := verifyJWT(t, jwt, key.Public)
	require.True(t, ok, "control: the token verifies with its own key")

	_, ok = verifyJWT(t, jwt, other.Public)
	assert.False(t, ok, "another key")

	forged, err := json.Marshal(vapidClaims{Aud: "https://web.push.apple.com", Exp: 1_700_000_000 + 24*3600, Sub: "https://loom.example.com"})
	require.NoError(t, err)
	_, ok = verifyJWT(t, parts[0]+"."+b64s(forged)+"."+parts[2], key.Public)
	assert.False(t, ok, "claims changed after signing")
}

func TestVAPIDAuth_RefusesWhatItCannotSign(t *testing.T) {
	key, other := newVAPIDKey(t), newVAPIDKey(t)
	const endpoint = "https://web.push.apple.com/wpush/v2/abc"
	const subject = "https://loom.example.com"
	now := time.Unix(1_700_000_000, 0)

	_, err := VAPIDAuth(key, endpoint, subject, now)
	require.NoError(t, err, "control: the unmodified inputs sign")
	_, err = VAPIDAuth(key, endpoint, "mailto:me@example.com", now)
	require.NoError(t, err, "control: a mailto subject is fine")

	for name, tc := range map[string]struct {
		key               core.VAPIDKey
		endpoint, subject string
	}{
		"no private key":      {core.VAPIDKey{Public: key.Public}, endpoint, subject},
		"short private key":   {core.VAPIDKey{Private: key.Private[:31], Public: key.Public}, endpoint, subject},
		"no public key":       {core.VAPIDKey{Private: key.Private}, endpoint, subject},
		"public key of other": {core.VAPIDKey{Private: key.Private, Public: other.Public}, endpoint, subject},
		"empty subject":       {key, endpoint, ""},
		"subject not a URI":   {key, endpoint, "loom.example.com"},
		"subject over http":   {key, endpoint, "http://loom.example.com"},
		"subject over ftp":    {key, endpoint, "ftp://loom.example.com"},
		"empty endpoint":      {key, "", subject},
		"relative endpoint":   {key, "/wpush/v2/abc", subject},
		"endpoint over ftp":   {key, "ftp://web.push.apple.com/x", subject},
		"endpoint over http":  {key, "http://web.push.apple.com/x", subject},
		"endpoint unparsable": {key, "://nope", subject},
		"endpoint no host":    {key, "https:///x", subject},
	} {
		t.Run(name, func(t *testing.T) {
			header, err := VAPIDAuth(tc.key, tc.endpoint, tc.subject, now)
			assert.Error(t, err)
			assert.Empty(t, header)
		})
	}
}

type ctxKey struct{}

func TestNewRequest(t *testing.T) {
	sub, receiver := newSub(t)
	sub.Endpoint = "https://web.push.apple.com/wpush/v2/abc?x=1"
	key := newVAPIDKey(t)
	now := time.Unix(1_700_000_000, 0)
	ctx := context.WithValue(context.Background(), ctxKey{}, "marker")

	req, err := NewRequest(ctx, sub, key, "https://loom.example.com", Message{
		Payload: []byte(`{"kind":"waiting"}`),
		TTL:     time.Hour,
		Urgency: "high",
		Topic:   "AAAAAAAAACo",
	}, now)
	require.NoError(t, err)

	assert.Equal(t, http.MethodPost, req.Method)
	assert.Equal(t, sub.Endpoint, req.URL.String())
	assert.Equal(t, "marker", req.Context().Value(ctxKey{}), "the caller's context rides the request")
	assert.Equal(t, "aes128gcm", req.Header.Get("Content-Encoding"))
	assert.Equal(t, "application/octet-stream", req.Header.Get("Content-Type"))
	assert.Equal(t, "3600", req.Header.Get("TTL"))
	assert.Equal(t, "high", req.Header.Get("Urgency"))
	assert.Equal(t, "AAAAAAAAACo", req.Header.Get("Topic"))

	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	assert.EqualValues(t, len(body), req.ContentLength)
	got, err := open(receiver, sub.Auth, body)
	require.NoError(t, err)
	assert.Equal(t, `{"kind":"waiting"}`, string(got), "the body is the payload, encrypted for the subscription")

	jwt, k := splitAuth(t, req.Header.Get("Authorization"))
	assert.Equal(t, b64s(key.Public), k)
	claims, ok := verifyJWT(t, jwt, key.Public)
	assert.True(t, ok)
	assert.Equal(t, "https://web.push.apple.com", claims.Aud, "aud is the endpoint's origin")
	assert.Equal(t, now.Add(12*time.Hour).Unix(), claims.Exp)
}

func TestNewRequest_OmitsWhatIsNotSet(t *testing.T) {
	sub, _ := newSub(t)
	req, err := NewRequest(context.Background(), sub, newVAPIDKey(t), "https://loom.example.com",
		Message{Payload: []byte("x")}, time.Unix(1_700_000_000, 0))
	require.NoError(t, err)

	assert.Equal(t, "0", req.Header.Get("TTL"), "a TTL of zero is sent: the service delivers now or never")
	_, hasUrgency := req.Header["Urgency"]
	assert.False(t, hasUrgency)
	_, hasTopic := req.Header["Topic"]
	assert.False(t, hasTopic)
}

func TestNewRequest_AcceptsEveryUrgencyOfRFC8030(t *testing.T) {
	sub, _ := newSub(t)
	for _, urgency := range []string{"very-low", "low", "normal", "high"} {
		req, err := NewRequest(context.Background(), sub, newVAPIDKey(t), "https://loom.example.com",
			Message{Payload: []byte("x"), Urgency: urgency}, time.Unix(1_700_000_000, 0))
		require.NoError(t, err, urgency)
		assert.Equal(t, urgency, req.Header.Get("Urgency"))
	}
}

func TestNewRequest_RefusesWhatItCannotSend(t *testing.T) {
	sub, _ := newSub(t)
	key := newVAPIDKey(t)
	now := time.Unix(1_700_000_000, 0)
	ok := Message{Payload: []byte("x"), TTL: time.Hour, Urgency: "normal", Topic: "AAAAAAAAACo"}

	_, err := NewRequest(context.Background(), sub, key, "https://loom.example.com", ok, now)
	require.NoError(t, err, "control: the unmodified message sends")
	_, err = NewRequest(context.Background(), sub, key, "https://loom.example.com",
		Message{Payload: []byte("x"), Topic: strings.Repeat("a", 32)}, now)
	require.NoError(t, err, "control: a 32-character topic is the longest allowed")

	badSub := sub
	badSub.Auth = nil
	badEndpoint := sub
	badEndpoint.Endpoint = "ftp://web.push.apple.com/x"
	for name, tc := range map[string]struct {
		sub     Subscription
		key     core.VAPIDKey
		subject string
		msg     Message
	}{
		"unknown urgency":  {sub, key, "https://loom.example.com", Message{Payload: ok.Payload, Urgency: "urgent"}},
		"topic too long":   {sub, key, "https://loom.example.com", Message{Payload: ok.Payload, Topic: strings.Repeat("a", 34)}},
		"topic of 5 chars": {sub, key, "https://loom.example.com", Message{Payload: ok.Payload, Topic: "abcde"}},
		"topic with slash": {sub, key, "https://loom.example.com", Message{Payload: ok.Payload, Topic: "a/b"}},
		"topic with space": {sub, key, "https://loom.example.com", Message{Payload: ok.Payload, Topic: "a b"}},
		"negative TTL":     {sub, key, "https://loom.example.com", Message{Payload: ok.Payload, TTL: -time.Second}},
		"payload too big":  {sub, key, "https://loom.example.com", Message{Payload: bytes.Repeat([]byte("x"), maxPayload+1)}},
		"bad subscription": {badSub, key, "https://loom.example.com", ok},
		"bad endpoint":     {badEndpoint, key, "https://loom.example.com", ok},
		"bad subject":      {sub, key, "loom.example.com", ok},
		"bad VAPID key":    {sub, core.VAPIDKey{}, "https://loom.example.com", ok},
	} {
		t.Run(name, func(t *testing.T) {
			req, err := NewRequest(context.Background(), tc.sub, tc.key, tc.subject, tc.msg, now)
			assert.Error(t, err)
			assert.Nil(t, req)
		})
	}
}

// Apple answers a Topic whose length is 1 mod 4 with 400 BadWebPushTopic,
// though RFC 8030 allows it: unpadded base64url never has such a length (the
// spike, 2026-10-22). Every other length up to maxTopic is sent as it is.
func TestNewRequest_TopicLengths(t *testing.T) {
	sub, _ := newSub(t)
	key := newVAPIDKey(t)
	now := time.Unix(1_700_000_000, 0)
	for n := 1; n <= maxTopic; n++ {
		topic := strings.Repeat("a", n)
		req, err := NewRequest(context.Background(), sub, key, "https://loom.example.com",
			Message{Payload: []byte("x"), Topic: topic}, now)
		if n%4 == 1 {
			assert.Error(t, err, "a topic of %d characters", n)
			assert.Nil(t, req)
			continue
		}
		require.NoError(t, err, "a topic of %d characters", n)
		assert.Equal(t, topic, req.Header.Get("Topic"))
	}
}
