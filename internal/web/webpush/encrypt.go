// Package webpush builds Web Push requests: the aes128gcm body of RFC 8291,
// the VAPID authorization of RFC 8292 and the HTTP request of RFC 8030. It
// holds no state and does no network I/O, so whatever posts a request owns
// its timeout, its retries and the push service's answer.
package webpush

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/url"

	"github.com/aidan-bailey/loom/core"
)

const (
	saltLen    = 16
	authLen    = 16
	pointLen   = 65 // an uncompressed P-256 point
	tagLen     = 16 // AES-GCM's authentication tag
	recordSize = 4096
	// headerLen is the RFC 8188 header: the salt, the record size, the key
	// ID's length and the key ID, which RFC 8291 makes the sender's public key.
	headerLen = saltLen + 4 + 1 + pointLen
	// maxPayload is what one record of recordSize bytes holds beside the
	// header, the one padding delimiter and the tag: 3993 bytes.
	maxPayload   = recordSize - headerLen - 1 - tagLen
	paddingDelim = 0x02 // RFC 8188: the last record's delimiter
)

var errPayloadTooLarge = errors.New("webpush: payload does not fit one 4096-byte record")

// Subscription is a browser's push subscription, decoded.
type Subscription struct {
	Endpoint string
	P256DH   []byte // 65 bytes
	Auth     []byte // 16 bytes
}

// ParseSubscription decodes a stored subscription: an https endpoint, and the
// unpadded base64url key and secret, checked for their lengths and for the
// key being a point on P-256. Whether the endpoint's host is a push service
// is core.ValidatePushSubscription's check, made when the subscription is stored.
func ParseSubscription(s core.PushSubscription) (Subscription, error) {
	u, err := url.Parse(s.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return Subscription{}, errors.New("webpush: the endpoint is not an https URL")
	}
	p256dh, err := base64.RawURLEncoding.DecodeString(s.P256DH)
	if err != nil {
		return Subscription{}, fmt.Errorf("webpush: p256dh: %w", err)
	}
	auth, err := base64.RawURLEncoding.DecodeString(s.Auth)
	if err != nil {
		return Subscription{}, fmt.Errorf("webpush: auth: %w", err)
	}
	sub := Subscription{Endpoint: s.Endpoint, P256DH: p256dh, Auth: auth}
	if err := sub.check(); err != nil {
		return Subscription{}, err
	}
	return sub, nil
}

// check refuses a key or secret the encryption could not use.
func (s Subscription) check() error {
	if len(s.Auth) != authLen {
		return fmt.Errorf("webpush: auth is %d bytes, want %d", len(s.Auth), authLen)
	}
	if len(s.P256DH) != pointLen {
		return fmt.Errorf("webpush: p256dh is %d bytes, want %d", len(s.P256DH), pointLen)
	}
	if _, err := ecdh.P256().NewPublicKey(s.P256DH); err != nil {
		return fmt.Errorf("webpush: p256dh is not a P-256 point: %w", err)
	}
	return nil
}

// Encrypt encrypts payload for sub as one RFC 8291 aes128gcm record with a
// record size of 4096: a fresh sender key pair and salt for every message, so
// two messages never share a key. The result is the request body, 86 bytes of
// header then the record. A payload over 3993 bytes is refused, since a push
// service need not take a body over 4096.
func Encrypt(sub Subscription, payload []byte) ([]byte, error) {
	sender, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("webpush: sender key: %w", err)
	}
	salt := make([]byte, saltLen)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("webpush: salt: %w", err)
	}
	return encryptWith(sub, payload, sender, salt)
}

// encryptWith is Encrypt with its random inputs given, so the tests can feed
// it RFC 8291's example.
func encryptWith(sub Subscription, payload []byte, sender *ecdh.PrivateKey, salt []byte) ([]byte, error) {
	if len(payload) > maxPayload {
		return nil, fmt.Errorf("%w: %d bytes, at most %d", errPayloadTooLarge, len(payload), maxPayload)
	}
	if len(salt) != saltLen {
		return nil, fmt.Errorf("webpush: salt is %d bytes, want %d", len(salt), saltLen)
	}
	if err := sub.check(); err != nil {
		return nil, err
	}
	receiver, err := ecdh.P256().NewPublicKey(sub.P256DH)
	if err != nil {
		return nil, fmt.Errorf("webpush: p256dh: %w", err)
	}
	secret, err := sender.ECDH(receiver)
	if err != nil {
		return nil, fmt.Errorf("webpush: key agreement: %w", err)
	}
	senderPub := sender.PublicKey().Bytes()
	cek, nonce, err := contentKeys(secret, sub.Auth, sub.P256DH, senderPub, salt)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, fmt.Errorf("webpush: cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("webpush: cipher: %w", err)
	}

	body := make([]byte, 0, headerLen+len(payload)+1+tagLen)
	body = append(body, salt...)
	body = binary.BigEndian.AppendUint32(body, recordSize)
	body = append(body, byte(len(senderPub)))
	body = append(body, senderPub...)

	record := make([]byte, 0, len(payload)+1)
	record = append(record, payload...)
	record = append(record, paddingDelim)
	return gcm.Seal(body, nonce, record, nil), nil
}

// contentKeys derives the content encryption key and nonce: RFC 8291
// section 3.3 combines the ECDH secret with the authentication secret into
// the input keying material, then RFC 8188 expands that with the salt. The
// HKDF steps append the 0x01 counter byte themselves. The nonce is not
// XORed with a record number, since the one record is number zero.
func contentKeys(ecdhSecret, auth, uaPublic, asPublic, salt []byte) (cek, nonce []byte, err error) {
	keyInfo := make([]byte, 0, len("WebPush: info\x00")+len(uaPublic)+len(asPublic))
	keyInfo = append(keyInfo, "WebPush: info\x00"...)
	keyInfo = append(keyInfo, uaPublic...)
	keyInfo = append(keyInfo, asPublic...)
	ikm, err := hkdf.Key(sha256.New, ecdhSecret, auth, string(keyInfo), 32)
	if err != nil {
		return nil, nil, fmt.Errorf("webpush: input keying material: %w", err)
	}
	cek, err = hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, nil, fmt.Errorf("webpush: content encryption key: %w", err)
	}
	nonce, err = hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, nil, fmt.Errorf("webpush: nonce: %w", err)
	}
	return cek, nonce, nil
}
