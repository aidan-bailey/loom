package core

import (
	"crypto/ecdh"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
)

// WebHost is the model as the web layer (internal/web) reaches it, beside the
// Core it dials over the wire: the device records and push state a client may
// not set for itself. *Loop implements it (loop_web.go), in process, and
// nothing about it crosses the wire. Each call runs on the loop's goroutine.
type WebHost interface {
	// RedeemPairing trades a pairing code for a device named as the code was
	// made for. The token is the unpadded base64url of 32 random bytes, which
	// the model keeps only as its SHA-256 and returns here once. The errors
	// are ErrWebOff, ErrPairingCode, ErrPairingLocked, ErrDeviceExists and
	// ErrTooManyDevices.
	RedeemPairing(code string) (name, token string, err error)
	// DeviceByToken finds the device whose token hashes to hash: the SHA-256
	// of the 32 raw bytes the cookie's token decodes to, not of its text.
	DeviceByToken(hash [32]byte) (name string, ok bool)
	// TouchDevice records that the device was seen, written at most once a
	// minute.
	TouchDevice(name string)
	// SetNotify applies the fields c sets to the device's notification
	// settings and returns the device as stored; ErrNoDevice for an unknown
	// name.
	SetNotify(name string, c NotifyChange) (WebDevice, error)
	// SetPushSubscription validates sub (ValidatePushSubscription), stores it
	// as the device's and sets PushOK; ErrNoDevice for an unknown name.
	SetPushSubscription(name string, sub PushSubscription) (WebDevice, error)
	// PushTargets are the devices with a subscription that isn't expired.
	PushTargets() []PushTarget
	// PushResult records how a push to the device went. PushExpired also
	// drops the subscription.
	PushResult(device string, state PushState)
	// VAPIDKey is the push key pair; ErrWebOff before the first enable.
	VAPIDKey() (VAPIDKey, error)
	// WebServing records that the listener listens on addr, or why it does
	// not: WebView.Serving, Addr and Error. A non-nil err also raises a
	// Notice, once per distinct error, which waits for the next TUI.
	WebServing(addr string, err error)
}

// NotifyChange changes a device's notification settings; a nil field is left
// as it is.
type NotifyChange struct {
	Waiting     *bool
	Finished    *bool
	PausedUntil *time.Time // a zero time ends a pause
}

// webPushEndpointMax is the longest push endpoint a device may store.
const webPushEndpointMax = 2048

// webPushHosts are the push services whose endpoints the daemon posts to;
// Windows' end in webPushWindowsSuffix instead.
var webPushHosts = []string{"web.push.apple.com", "fcm.googleapis.com", "updates.push.services.mozilla.com"}

const webPushWindowsSuffix = ".notify.windows.com"

// webPushHostAllowed reports whether host is a push service the daemon posts
// to.
func webPushHostAllowed(host string) bool {
	h := strings.ToLower(host)
	if slices.Contains(webPushHosts, h) {
		return true
	}
	return strings.HasSuffix(h, webPushWindowsSuffix) && len(h) > len(webPushWindowsSuffix)
}

// webPushKey decodes s, an unpadded base64url value of exactly n bytes, and
// accepts only its canonical spelling: the decoder skips newlines and stray
// bits, and the daemon encrypts to the decoded bytes.
func webPushKey(s string, n int) ([]byte, bool) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(b) != n || base64.RawURLEncoding.EncodeToString(b) != s {
		return nil, false
	}
	return b, true
}

// ValidatePushSubscription checks a push subscription before the model keeps
// it. The daemon posts to its endpoint, so only the push services' hosts are
// taken: an https endpoint of at most 2 KiB on web.push.apple.com,
// fcm.googleapis.com, updates.push.services.mozilla.com or a host ending in
// .notify.windows.com, with no user and no port but 443 (stricter than the
// spec's limits, which name the host only). p256dh is a 65-byte uncompressed
// P-256 point and auth 16 bytes, both unpadded base64url. The errors never
// quote the endpoint: it is a capability.
func ValidatePushSubscription(sub PushSubscription) error {
	if len(sub.Endpoint) > webPushEndpointMax {
		return fmt.Errorf("push subscription: the endpoint is longer than %d bytes", webPushEndpointMax)
	}
	u, err := url.Parse(sub.Endpoint)
	switch {
	case err != nil:
		return errors.New("push subscription: the endpoint is no URL")
	case u.Scheme != "https":
		return errors.New("push subscription: the endpoint is not https")
	case u.User != nil:
		return errors.New("push subscription: the endpoint names a user")
	case u.Port() != "" && u.Port() != "443":
		return errors.New("push subscription: the endpoint names a port other than 443")
	case !webPushHostAllowed(u.Hostname()):
		return fmt.Errorf("push subscription: %q is no push service the daemon posts to", u.Hostname())
	}
	p256dh, ok := webPushKey(sub.P256DH, 65)
	if !ok {
		return errors.New("push subscription: p256dh is not a 65-byte unpadded base64url value")
	}
	if _, err := ecdh.P256().NewPublicKey(p256dh); err != nil {
		return errors.New("push subscription: p256dh is no point on P-256")
	}
	if _, ok := webPushKey(sub.Auth, 16); !ok {
		return errors.New("push subscription: auth is not a 16-byte unpadded base64url value")
	}
	return nil
}
