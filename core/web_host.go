package core

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/log"
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
	// PushTargets are the devices with a subscription that isn't expired and
	// passes ValidatePushSubscription.
	PushTargets() []PushTarget
	// PushEndpoint is the endpoint of the subscription the device holds, even
	// one that fails ValidatePushSubscription (PushTargets leaves it out), or
	// "" when it holds none, has an expired one or is no device. It is the
	// endpoint to name in a PushResult that drops the subscription.
	PushEndpoint(device string) string
	// PushResult records how a push to the endpoint of the device's
	// subscription went. PushExpired also drops the subscription. It changes
	// nothing when the device now holds another endpoint: a late result for
	// a subscription the device has replaced says nothing of the new one.
	PushResult(device, endpoint string, state PushState)
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

const (
	// webTokenBytes is the size of a device's random token before encoding.
	webTokenBytes = 32
	// webTouchInterval is the least time between two writes of a device's
	// last-seen time.
	webTouchInterval = time.Minute
)

// RedeemPairing trades a pairing code for a device (WebHost). A code matches
// ignoring case, dashes and spaces, once, and until it expires. Each wrong
// code counts in misses; the fifth voids every pending code, and every code
// then gets ErrPairingLocked until the next PairDevice resets the count. A
// match is consumed before the name and the device count are checked, so a
// refusal there costs the code; a failed write puts it back. Loop goroutine
// only.
func (m *Model) RedeemPairing(code string) (name, token string, err error) {
	w := &m.web
	if w.listen == "" {
		return "", "", ErrWebOff
	}
	if w.misses >= PairingMaxMisses {
		return "", "", ErrPairingLocked
	}
	now := w.clock()
	w.pending = slices.DeleteFunc(w.pending, func(p pendingPairing) bool { return !now.Before(p.expires) })
	at := -1
	if norm, ok := NormalizePairingCode(code); ok {
		for i, p := range w.pending {
			if subtle.ConstantTimeCompare([]byte(p.code), []byte(norm)) == 1 {
				at = i
			}
		}
	}
	if at < 0 {
		w.misses++
		if w.misses < PairingMaxMisses {
			return "", "", ErrPairingCode
		}
		w.pending = nil
		log.For("core").Warn("web.pairing_locked", "misses", w.misses)
		return "", "", ErrPairingLocked
	}
	p := w.pending[at]
	w.pending = slices.Delete(w.pending, at, at+1)
	if w.find(p.name) != nil {
		return "", "", fmt.Errorf("%w: %q", ErrDeviceExists, p.name)
	}
	if len(w.devices) >= MaxWebDevices {
		return "", "", ErrTooManyDevices
	}
	raw := make([]byte, webTokenBytes)
	_, _ = rand.Read(raw)
	d := &webDevice{
		name:      p.name,
		tokenHash: sha256.Sum256(raw),
		paired:    now,
		lastSeen:  now,
		notify:    NotifySettings{Waiting: true, Finished: true},
	}
	w.devices = append(w.devices, d)
	slices.SortFunc(w.devices, func(a, b *webDevice) int { return strings.Compare(a.name, b.name) })
	if err = m.saveWeb(); err != nil {
		w.devices = slices.DeleteFunc(w.devices, func(x *webDevice) bool { return x == d })
		w.pending = slices.Insert(w.pending, at, p)
		return "", "", fmt.Errorf("pairing: %w", err)
	}
	log.For("core").Info("web.paired", "device", p.name)
	return p.name, base64.RawURLEncoding.EncodeToString(raw), nil
}

// DeviceByToken finds the device whose token hashes to hash, the SHA-256 of
// the token's 32 raw bytes (WebHost). A record with no hash (a hand-edited
// web.json) answers to no hash. Loop goroutine only.
func (m *Model) DeviceByToken(hash [32]byte) (string, bool) {
	found := ""
	for _, d := range m.web.devices {
		if d.tokenHash != [32]byte{} && subtle.ConstantTimeCompare(d.tokenHash[:], hash[:]) == 1 {
			found = d.name
		}
	}
	return found, found != ""
}

// TouchDevice records that the device was seen, writing web.json at most once
// a minute per device (WebHost), and reports whether it did, so the loop
// wakes its client only then. A write that fails is logged and left to the
// next minute: the time stays in memory. Loop goroutine only.
func (m *Model) TouchDevice(name string) bool {
	d := m.web.find(name)
	if d == nil {
		return false
	}
	now := m.web.clock()
	if now.Sub(d.lastSeen) < webTouchInterval {
		return false
	}
	d.lastSeen = now
	if err := m.saveWeb(); err != nil {
		log.For("core").Warn("web.touch_unsaved", "device", name, "err", err.Error())
	}
	return true
}

// webHostDeviceView is d as a client sees it: the record without its hash or
// its subscription's keys.
func webHostDeviceView(d *webDevice) WebDevice {
	return WebDevice{Name: d.name, Paired: d.paired, LastSeen: d.lastSeen, Notify: d.notify, Push: d.push}
}

// SetNotify applies the fields c sets to the device's notification settings
// and returns the device as stored (WebHost). A write that fails puts the
// settings back. Loop goroutine only.
func (m *Model) SetNotify(name string, c NotifyChange) (WebDevice, error) {
	d := m.web.find(name)
	if d == nil {
		return WebDevice{}, noDeviceError(name)
	}
	prev := d.notify
	if c.Waiting != nil {
		d.notify.Waiting = *c.Waiting
	}
	if c.Finished != nil {
		d.notify.Finished = *c.Finished
	}
	if c.PausedUntil != nil {
		d.notify.PausedUntil = *c.PausedUntil
	}
	if err := m.saveWeb(); err != nil {
		d.notify = prev
		return WebDevice{}, fmt.Errorf("notify settings: %w", err)
	}
	return webHostDeviceView(d), nil
}

// SetPushSubscription validates sub, stores it as the device's and sets
// PushOK (WebHost). A write that fails puts the old subscription back. Loop
// goroutine only.
func (m *Model) SetPushSubscription(name string, sub PushSubscription) (WebDevice, error) {
	d := m.web.find(name)
	if d == nil {
		return WebDevice{}, noDeviceError(name)
	}
	if err := ValidatePushSubscription(sub); err != nil {
		return WebDevice{}, err
	}
	prevSub, prevPush := d.sub, d.push
	d.sub, d.push = sub, PushOK
	if err := m.saveWeb(); err != nil {
		d.sub, d.push = prevSub, prevPush
		return WebDevice{}, fmt.Errorf("push subscription: %w", err)
	}
	return webHostDeviceView(d), nil
}

// PushTargets are the devices with a subscription that isn't expired, in
// device order (WebHost). A subscription that failed is tried again, except
// one that fails ValidatePushSubscription (a stored one the check refused on
// load): the daemon posts to no endpoint it has not checked. Loop goroutine
// only.
func (m *Model) PushTargets() []PushTarget {
	var targets []PushTarget
	for _, d := range m.web.devices {
		if d.sub.Endpoint == "" || d.push == PushExpired || ValidatePushSubscription(d.sub) != nil {
			continue
		}
		targets = append(targets, PushTarget{Device: d.name, Notify: d.notify, Subscription: d.sub})
	}
	return targets
}

// PushEndpoint is the endpoint of the subscription the device holds, or ""
// (WebHost). Unlike PushTargets it does not check the subscription: a stored
// one the check refused on load is still the device's to drop. Loop goroutine
// only.
func (m *Model) PushEndpoint(device string) string {
	if d := m.web.find(device); d != nil {
		return d.sub.Endpoint
	}
	return ""
}

// PushResult records how a push to endpoint went; PushExpired also drops the
// subscription (WebHost). It reports whether a client would see a change. A
// result for a device with no subscription changes nothing: a push still in
// flight when another one's 410 dropped it would otherwise turn "expired"
// into "failing". Neither does a result for an endpoint the device no longer
// holds: a slow retry's 404 against the old endpoint must not drop the
// subscription the device posted since, nor its ok or failing overwrite the
// new one's state. A write that fails is logged and the state stays in
// memory. Loop goroutine only.
func (m *Model) PushResult(device, endpoint string, state PushState) bool {
	d := m.web.find(device)
	if d == nil || d.sub.Endpoint == "" || d.sub.Endpoint != endpoint {
		return false
	}
	prevSub, prevPush := d.sub, d.push
	d.push = state
	if state == PushExpired {
		d.sub = PushSubscription{}
	}
	if d.sub == prevSub && d.push == prevPush {
		return false
	}
	if err := m.saveWeb(); err != nil {
		log.For("core").Warn("web.push_result_unsaved", "device", device, "err", err.Error())
	}
	return true
}

// VAPIDKey is a copy of the push key pair; ErrWebOff before the first enable
// made one (WebHost). It outlives a disable. Loop goroutine only.
func (m *Model) VAPIDKey() (VAPIDKey, error) {
	k := m.web.vapid
	if len(k.Private) == 0 || len(k.Public) == 0 {
		return VAPIDKey{}, ErrWebOff
	}
	return VAPIDKey{Private: slices.Clone(k.Private), Public: slices.Clone(k.Public)}, nil
}

// WebServing records where the listener listens, or why it does not (WebHost):
// serving means no error and an address. An error also raises a Notice, once
// per distinct error text (the one already recorded is not told again), which
// the server keeps for the next TUI while only the web layer is connected. It
// reports whether the view changed. It leaves rev alone: only ConfigureWeb
// bumps it, and the layer rebinds when it moves, so a failed bind is not
// retried by its own report. Loop goroutine only.
func (m *Model) WebServing(addr string, err error) bool {
	w := &m.web
	serving := err == nil && addr != ""
	if !serving {
		addr = ""
	}
	errText := ""
	if err != nil {
		errText = err.Error()
	}
	told := errText != "" && errText != w.servingErr
	changed := serving != w.serving || addr != w.addr || errText != w.servingErr
	w.serving, w.addr, w.servingErr = serving, addr, errText
	if told {
		log.For("core").Warn("web.not_serving", "err", errText)
		m.notifyErr(fmt.Errorf("web: %w", err))
	}
	return changed
}
