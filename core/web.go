package core

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// The web layer's settings and devices (internal/web). The model keeps them
// in web.json (web_store.go) and publishes them in ModelView.Web, so every
// client reads them from its replica (Core.Web); the requests that change
// them are ConfigureWeb, PairDevice and RevokeDevice.

// WebView is the web settings as a client sees them, and who is paired.
type WebView struct {
	Listen  string      // "127.0.0.1:7420"; "" when off
	Origin  string      // "https://loom.example.com"
	Serving bool        // the listener is up
	Addr    string      // what it listens on, while Serving
	Error   string      // why it is not serving, or why web.json was left alone
	Devices []WebDevice // sorted by Name; no token hashes
	Rev     uint64      // bumped by every ConfigureWeb; the layer rebinds when it changes
}

// WebDevice is a paired device as a client sees it: its token hash and its
// push subscription stay in the model.
type WebDevice struct {
	Name     string
	Paired   time.Time
	LastSeen time.Time
	Notify   NotifySettings
	Push     PushState
}

// NotifySettings are the kinds of push a device takes, and until when it
// takes none.
type NotifySettings struct {
	Waiting     bool
	Finished    bool
	PausedUntil time.Time // zero: not paused
}

// Pairing is the one-time code PairDevice makes, and when it stops working.
type Pairing struct {
	Code    string // PairingCodeLen characters of PairingAlphabet, no dash
	Expires time.Time
}

// PushSubscription is the browser's push subscription, as the app posts it.
type PushSubscription struct {
	Endpoint string
	P256DH   string // unpadded base64url of a 65-byte uncompressed P-256 point
	Auth     string // unpadded base64url of 16 bytes
}

// PushTarget is a device the web layer may push to, with what it takes.
type PushTarget struct {
	Device       string
	Notify       NotifySettings
	Subscription PushSubscription
}

// PushState is how a device's push subscription stands: none, ok, expired
// or failing (PushFailing).
type PushState string

// The states of a push subscription besides failing (PushFailing).
const (
	PushNone    PushState = ""
	PushOK      PushState = "ok"
	PushExpired PushState = "expired"
)

// PushFailing is the state of a subscription whose last push the push
// service answered with status: "failing: <status>".
func PushFailing(status int) PushState { return PushState("failing: " + strconv.Itoa(status)) }

// VAPIDKey is the P-256 key pair the web layer signs its pushes with, made
// when the web is first enabled.
type VAPIDKey struct {
	Private []byte // the 32-byte P-256 scalar
	Public  []byte // the 65-byte uncompressed point
}

// The pairing code's alphabet (no 0, 1, I, L or O) and length, how long a
// code lives, how many wait at once and how many wrong ones lock redeeming,
// the most devices that can be paired, and web.json's name in the global dir.
const (
	PairingAlphabet   = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"
	PairingCodeLen    = 8
	PairingTTL        = 10 * time.Minute
	PairingMaxPending = 4
	PairingMaxMisses  = 5
	MaxWebDevices     = 16
	WebFile           = "web.json" // in the global dir
)

const (
	maxWebOrigin     = 255 // bytes
	maxDeviceNameLen = 32  // characters
)

// The errors the web requests answer with; those that name a device wrap
// the sentinel (fmt.Errorf("%w: %q", ErrDeviceExists, name)).
var (
	ErrWebOff         = errors.New("web: not enabled; run loom web enable first")
	ErrPairingCode    = errors.New("pairing: the code is wrong, used or expired")
	ErrPairingLocked  = errors.New("pairing: too many wrong codes; run loom web pair again")
	ErrDeviceExists   = errors.New("pairing: a device has that name already")
	ErrTooManyDevices = errors.New("pairing: 16 devices are paired; revoke one first")
	ErrNoDevice       = errors.New("no device has that name")
)

// validPort reports whether port is a port number written plainly: digits
// only, 1 to 65535, no sign and no leading zero.
func validPort(port string) bool {
	n, err := strconv.ParseUint(port, 10, 16)
	return err == nil && n >= 1 && strconv.FormatUint(n, 10) == port
}

// ValidateWebListen checks a listen address: exactly 127.0.0.1 or ::1 and a
// port, written as net.JoinHostPort writes them (127.0.0.1:7420 or
// [::1]:7420). A host name such as localhost, any other address (another
// loopback one, 127.0.0.2, and the mapped ::ffff:127.0.0.1 too), a unix
// socket and a port outside 1 to 65535 are refused: only a process on this
// machine, cloudflared through the tunnel, may reach the listener.
func ValidateWebListen(listen string) error {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return fmt.Errorf("listen %q: want a loopback address and a port, like 127.0.0.1:7420 or [::1]:7420", listen)
	}
	addr, err := netip.ParseAddr(host)
	if err != nil || addr.String() != host || (addr != netip.AddrFrom4([4]byte{127, 0, 0, 1}) && addr != netip.IPv6Loopback()) {
		return fmt.Errorf("listen %q: the address must be the loopback IP literal 127.0.0.1 or ::1", listen)
	}
	if !validPort(port) {
		return fmt.Errorf("listen %q: the port must be a number from 1 to 65535", listen)
	}
	if want := net.JoinHostPort(host, port); listen != want {
		return fmt.Errorf("listen %q: write it as %s", listen, want)
	}
	return nil
}

// ValidateWebOrigin checks the origin the phone reaches loom at:
// https://<host>[:<port>] in lower case with no user, path, query or
// fragment, or for development http://127.0.0.1:<port> or
// http://localhost:<port>, at most 255 bytes. The web layer compares the
// Host and Origin headers with it, after dropping a default port from the
// header (a browser never sends one), so write the origin the way the
// address bar shows it. A default port written here (https://host:443) is
// not refused by this check.
func ValidateWebOrigin(origin string) error {
	if len(origin) > maxWebOrigin {
		return fmt.Errorf("origin is longer than %d bytes", maxWebOrigin)
	}
	bad := func(why string) error { return fmt.Errorf("origin %q: %s", origin, why) }
	u, err := url.Parse(origin)
	if err != nil || u.String() != origin {
		return bad("write it as https://<host>[:<port>], in lower case")
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return bad("the scheme must be https")
	}
	if u.Opaque != "" || u.User != nil || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" {
		return bad("it has no user, path, query or fragment")
	}
	host, port := u.Hostname(), u.Port()
	if host == "" || host != strings.ToLower(host) {
		return bad("the host must be named, in lower case")
	}
	if strings.HasSuffix(u.Host, ":") || (port != "" && !validPort(port)) {
		return bad("the port must be a number from 1 to 65535")
	}
	if u.Scheme == "http" && (port == "" || (host != "127.0.0.1" && host != "localhost")) {
		return bad("http is for development only: 127.0.0.1 or localhost, with a port")
	}
	return nil
}

// ValidateDeviceName checks a device name and returns it trimmed: 1 to 32
// characters, none of them a control character. Whether a device has it
// already is for the model to say (ErrDeviceExists).
func ValidateDeviceName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !utf8.ValidString(name) {
		return "", errors.New("device name: not valid UTF-8")
	}
	if n := utf8.RuneCountInString(name); n < 1 || n > maxDeviceNameLen {
		return "", fmt.Errorf("device name: want 1 to %d characters, got %d", maxDeviceNameLen, n)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", errors.New("device name: no control characters")
		}
	}
	return name, nil
}

// NormalizePairingCode puts a code as typed into the form the model keeps:
// upper case, with dashes and spaces dropped. It reports false unless what
// is left is PairingCodeLen characters of PairingAlphabet.
func NormalizePairingCode(code string) (string, bool) {
	out := make([]byte, 0, PairingCodeLen)
	for i := 0; i < len(code); i++ {
		c := code[i]
		if c == '-' || c == ' ' {
			continue
		}
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		if len(out) == PairingCodeLen || strings.IndexByte(PairingAlphabet, c) < 0 {
			return "", false
		}
		out = append(out, c)
	}
	if len(out) != PairingCodeLen {
		return "", false
	}
	return string(out), true
}

// ConfigureWeb sets where the web listens and the origin the phone reaches
// it at, and writes web.json (Core.ConfigureWeb). Both are validated. An
// empty listen turns the web off and keeps the origin (its argument is not
// read), the devices and the key, and voids every pending pairing code, so a
// later enable starts with none (the count of wrong codes is reset by the
// next PairDevice). The first enable makes the VAPID key, which every later
// enable keeps: push subscriptions are bound to it. Every
// call that succeeds, whatever it changed, bumps the revision (WebView.Rev)
// and clears what the web layer reported (WebServing): the layer rebinds
// whenever the revision changes, so running enable again retries a bind that
// failed, and a disable and an enable that one Sync coalesces still differ.
// Settings already in force, with the key made, write nothing. It is refused
// while web.json is left alone, and when the write fails (errWebNoDir when
// the model has no path and the global dir is unknown) nothing has changed,
// the revision included.
func (m *Model) ConfigureWeb(listen, origin string) error {
	w := &m.web
	if w.loadErr != "" {
		return w.leftAlone()
	}
	if listen == "" {
		origin = w.origin
	} else {
		if err := ValidateWebListen(listen); err != nil {
			return err
		}
		if err := ValidateWebOrigin(origin); err != nil {
			return err
		}
	}
	needKey := listen != "" && len(w.vapid.Private) == 0
	needWrite := listen != w.listen || origin != w.origin || needKey
	prev := *w
	w.rev++
	w.serving, w.addr, w.servingErr = false, "", ""
	w.listen, w.origin = listen, origin
	if needKey {
		key, err := newVAPIDKey()
		if err != nil {
			*w = prev
			return err
		}
		w.vapid = key
	}
	if needWrite {
		if err := m.saveWeb(); err != nil {
			*w = prev
			return err
		}
	}
	if listen == "" {
		w.pending = nil
	}
	return nil
}

// PairDevice makes the one-time code that pairs a device under name
// (Core.PairDevice): PairingCodeLen characters of PairingAlphabet, good for
// PairingTTL. The code lives in memory only, so a daemon restart voids it,
// and is not in the view. A second code for the same name replaces the
// first, and at most PairingMaxPending wait: one more voids the oldest.
// Starting a pairing also resets the count of wrong codes, which locks
// redeeming at PairingMaxMisses. The web must be on (ErrWebOff, which is
// also what a web.json left alone answers), the name valid and not a
// paired device's (ErrDeviceExists), and fewer than MaxWebDevices paired
// (ErrTooManyDevices): the codes waiting are not devices and don't count,
// and redeeming a code checks the limit again.
func (m *Model) PairDevice(name string) (Pairing, error) {
	w := &m.web
	if w.loadErr != "" || w.listen == "" {
		return Pairing{}, ErrWebOff
	}
	name, err := ValidateDeviceName(name)
	if err != nil {
		return Pairing{}, err
	}
	if w.find(name) != nil {
		return Pairing{}, fmt.Errorf("%w: %q", ErrDeviceExists, name)
	}
	if len(w.devices) >= MaxWebDevices {
		return Pairing{}, ErrTooManyDevices
	}
	code, err := newPairingCode()
	if err != nil {
		return Pairing{}, err
	}
	w.prune()
	p := pendingPairing{code: code, name: name, expires: w.clock().Add(PairingTTL)}
	w.pending = slices.DeleteFunc(w.pending, func(q pendingPairing) bool { return q.name == name })
	w.pending = append(w.pending, p)
	if n := len(w.pending) - PairingMaxPending; n > 0 {
		w.pending = slices.Delete(w.pending, 0, n)
	}
	w.misses = 0
	return Pairing{Code: p.code, Expires: p.expires}, nil
}

// RevokeDevice unpairs the device called name (Core.RevokeDevice): its
// record, token hash and push subscription go from web.json, and its
// cookie stops working. The next publish tells the web layer, which ends
// the device's streams. It works while the web is off. ErrNoDevice for a
// name no device has; refused while web.json is left alone; when the write
// fails the device stays paired.
func (m *Model) RevokeDevice(name string) error {
	w := &m.web
	if w.loadErr != "" {
		return w.leftAlone()
	}
	i := slices.IndexFunc(w.devices, func(d *webDevice) bool { return d.name == name })
	if i < 0 {
		return fmt.Errorf("%w: %q", ErrNoDevice, name)
	}
	prev := w.devices
	w.devices = slices.Delete(slices.Clone(prev), i, i+1)
	if err := m.saveWeb(); err != nil {
		w.devices = prev
		return err
	}
	return nil
}
