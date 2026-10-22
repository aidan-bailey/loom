package webpush

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

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
