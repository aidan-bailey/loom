package core

import (
	"bytes"
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
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/log"
)

// webFileVersion is the web.json version this build reads and writes. A
// file naming a newer one is left alone, as is one that can't be read or
// holds a value that fails validation: the web stays off, WebView.Error
// says why, a notice tells the next TUI, and nothing writes over the file.
const webFileVersion = 1

// errWebNoDir is why a change can't be kept when the model has no web.json
// path and the global config dir can't be found either.
var errWebNoDir = errors.New("web: the global config dir can't be found, so web.json has no place")

// webState is the model's web settings and devices: web.json as loaded and
// changed, and what the model keeps in memory only (the pairing codes and
// the count of wrong ones, and what the web layer reported through
// WebServing). The zero value is off, with no devices. Loop-goroutine only,
// like the rest of the model.
type webState struct {
	path           string           // <global dir>/web.json; "" on a model that never ran initWeb or when the global dir can't be found, and saveWeb then looks the dir up itself
	listen, origin string           // an empty listen is off; the origin is kept while off
	vapid          VAPIDKey         // zero until the first enable
	devices        []*webDevice     // sorted by name
	pending        []pendingPairing // oldest first, at most PairingMaxPending; memory only
	misses         int              // wrong codes since the last PairDevice, which resets it
	loadErr        string           // why web.json was left alone (unreadable, a newer version, or a value that fails validation); keeps the web off and saveWeb refusing
	rev            uint64           // ConfigureWeb's count, published as WebView.Rev; memory only, not in web.json
	serving        bool             // set by WebServing
	addr           string           // set by WebServing
	servingErr     string           // set by WebServing; WebView.Error is loadErr when set, else this
	now            func() time.Time // nil means time.Now; tests set it
}

// webDevice is a paired device. Its token is never kept, only the SHA-256
// of it.
type webDevice struct {
	name             string
	tokenHash        [32]byte
	paired, lastSeen time.Time
	notify           NotifySettings
	sub              PushSubscription // zero when none
	push             PushState
}

// pendingPairing is a code PairDevice made and nobody has redeemed.
type pendingPairing struct {
	code    string // in NormalizePairingCode's form
	name    string
	expires time.Time
}

// clock is the web state's time: w.now, or time.Now when nil.
func (w *webState) clock() time.Time {
	if w.now != nil {
		return w.now()
	}
	return time.Now()
}

// find is the device called name, nil when no device has the name.
func (w *webState) find(name string) *webDevice {
	for _, d := range w.devices {
		if d.name == name {
			return d
		}
	}
	return nil
}

// prune drops the pending codes that expired: one is good until its
// expiry, not at it.
func (w *webState) prune() {
	now := w.clock()
	w.pending = slices.DeleteFunc(w.pending, func(p pendingPairing) bool { return !now.Before(p.expires) })
}

// leftAlone is why a change is refused while web.json is left alone.
func (w *webState) leftAlone() error {
	return fmt.Errorf("web.json is left alone: %s", w.loadErr)
}

// view is the state as a client sees it: no token hash, no subscription.
func (w *webState) view() WebView {
	v := WebView{Listen: w.listen, Origin: w.origin, Serving: w.serving, Addr: w.addr, Error: w.servingErr, Rev: w.rev}
	if w.loadErr != "" {
		v.Error = w.loadErr
	}
	for _, d := range w.devices {
		v.Devices = append(v.Devices, WebDevice{Name: d.name, Paired: d.paired, LastSeen: d.lastSeen, Notify: d.notify, Push: d.push})
	}
	return v
}

// Web is the web settings and devices as a client sees them (Core.Web).
func (m *Model) Web() WebView { return m.web.view() }

// webFile is web.json, version 1.
type webFile struct {
	Version int             `json:"version"`
	Listen  string          `json:"listen"`
	Origin  string          `json:"origin"`
	VAPID   *webFileVAPID   `json:"vapid,omitempty"`
	Devices []webFileDevice `json:"devices"`
}

// webFileVAPID is the key pair, each half as unpadded base64url.
type webFileVAPID struct {
	Private string `json:"private"`
	Public  string `json:"public"`
}

// webFileDevice is one device of web.json. A pause is an RFC 3339 time, or
// empty when there is none.
type webFileDevice struct {
	Name        string        `json:"name"`
	TokenSHA256 string        `json:"token_sha256"`
	Paired      time.Time     `json:"paired"`
	LastSeen    time.Time     `json:"last_seen"`
	Notify      webFileNotify `json:"notify"`
	Push        *webFilePush  `json:"push,omitempty"`
	PushState   string        `json:"push_state,omitempty"`
}

type webFileNotify struct {
	Waiting     bool   `json:"waiting"`
	Finished    bool   `json:"finished"`
	PausedUntil string `json:"paused_until"`
}

type webFilePush struct {
	Endpoint string `json:"endpoint"`
	P256DH   string `json:"p256dh"`
	Auth     string `json:"auth"`
}

// initWeb loads web.json from the global config dir, as Boot does. A
// missing file is the web off with no devices. A file that can't be read,
// names a newer version or holds a value that fails validation is left
// exactly as it is: the web stays off, loadErr says why, and a notice
// tells the next TUI (Boot returns it).
func (m *Model) initWeb() {
	now := m.web.now
	m.web = webState{now: now}
	dir, err := config.GetGlobalConfigDir()
	if err != nil {
		log.For("web").Warn("web.no_global_dir", "err", err.Error())
		return
	}
	path := filepath.Join(dir, WebFile)
	w, err := loadWeb(path)
	if err != nil {
		log.For("web").Error("web.load_failed", "path", path, "err", err.Error())
		m.web = webState{path: path, loadErr: err.Error(), now: now}
		m.notifyErr(fmt.Errorf("web: %s; the web stays off and web.json is left as it is", err))
		return
	}
	w.now = now
	m.web = w
}

// loadWeb reads the web.json at path into a fresh state.
func loadWeb(path string) (webState, error) {
	w := webState{path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return w, nil
	}
	if err != nil {
		return webState{}, fmt.Errorf("read %s: %w", path, err)
	}
	var f webFile
	if err := json.Unmarshal(data, &f); err != nil {
		return webState{}, fmt.Errorf("parse %s: %w", path, err)
	}
	switch {
	case f.Version > webFileVersion:
		return webState{}, fmt.Errorf("%s is version %d, newer than the version %d this build reads", path, f.Version, webFileVersion)
	case f.Version < 1:
		return webState{}, fmt.Errorf("%s names version %d, which no build wrote", path, f.Version)
	}
	if err := w.adopt(f); err != nil {
		return webState{}, fmt.Errorf("validate %s: %w", path, err)
	}
	return w, nil
}

// adopt takes the settings, key and devices of f into w, refusing a value
// this build would never have written itself.
func (w *webState) adopt(f webFile) error {
	if f.Listen != "" {
		if err := ValidateWebListen(f.Listen); err != nil {
			return err
		}
		if f.Origin == "" {
			return errors.New("listen is set but origin is empty")
		}
	}
	if f.Origin != "" {
		if err := ValidateWebOrigin(f.Origin); err != nil {
			return err
		}
	}
	w.listen, w.origin = f.Listen, f.Origin
	if f.VAPID != nil {
		key, err := f.VAPID.key()
		if err != nil {
			return err
		}
		w.vapid = key
	}
	if len(f.Devices) > MaxWebDevices {
		return fmt.Errorf("%d devices; at most %d", len(f.Devices), MaxWebDevices)
	}
	tokens := map[[32]byte]bool{}
	for _, fd := range f.Devices {
		d, err := fd.device()
		if err != nil {
			return fmt.Errorf("device %q: %w", fd.Name, err)
		}
		if w.find(d.name) != nil {
			return fmt.Errorf("device %q is listed twice", d.name)
		}
		if tokens[d.tokenHash] {
			return fmt.Errorf("device %q shares a token with another device", d.name)
		}
		tokens[d.tokenHash] = true
		w.devices = append(w.devices, d)
	}
	slices.SortFunc(w.devices, func(a, b *webDevice) int { return strings.Compare(a.name, b.name) })
	return nil
}

// key decodes the key pair and checks that the public half is the private
// one's own.
func (v webFileVAPID) key() (VAPIDKey, error) {
	priv, err := base64.RawURLEncoding.DecodeString(v.Private)
	if err != nil {
		return VAPIDKey{}, fmt.Errorf("vapid private key: %w", err)
	}
	pub, err := base64.RawURLEncoding.DecodeString(v.Public)
	if err != nil {
		return VAPIDKey{}, fmt.Errorf("vapid public key: %w", err)
	}
	k, err := ecdh.P256().NewPrivateKey(priv)
	if err != nil {
		return VAPIDKey{}, fmt.Errorf("vapid private key: %w", err)
	}
	if !bytes.Equal(k.PublicKey().Bytes(), pub) {
		return VAPIDKey{}, errors.New("vapid public key is not the private key's")
	}
	return VAPIDKey{Private: priv, Public: pub}, nil
}

// device checks one stored device and builds it.
func (fd webFileDevice) device() (*webDevice, error) {
	name, err := ValidateDeviceName(fd.Name)
	if err != nil {
		return nil, err
	}
	if name != fd.Name {
		return nil, errors.New("the name has spaces around it")
	}
	raw, err := hex.DecodeString(fd.TokenSHA256)
	if err != nil || len(raw) != sha256.Size {
		return nil, errors.New("token_sha256 is not 64 hex digits")
	}
	if !validPushState(fd.PushState) {
		return nil, fmt.Errorf("push_state %q is not ok, expired or failing: <status>", fd.PushState)
	}
	d := &webDevice{
		name:     name,
		paired:   fd.Paired,
		lastSeen: fd.LastSeen,
		notify:   NotifySettings{Waiting: fd.Notify.Waiting, Finished: fd.Notify.Finished},
		push:     PushState(fd.PushState),
	}
	copy(d.tokenHash[:], raw)
	if fd.Notify.PausedUntil != "" {
		until, err := time.Parse(time.RFC3339, fd.Notify.PausedUntil)
		if err != nil {
			return nil, fmt.Errorf("paused_until: %w", err)
		}
		d.notify.PausedUntil = until
	}
	if fd.Push != nil {
		d.sub = PushSubscription{Endpoint: fd.Push.Endpoint, P256DH: fd.Push.P256DH, Auth: fd.Push.Auth}
		if err := ValidatePushSubscription(d.sub); err != nil {
			// The record stays and the device stays paired, but nothing is
			// posted to a subscription the check refuses (PushTargets
			// repeats it); the app's Turn on replaces it. Status 0: no push
			// service was asked.
			d.push = PushFailing(0)
			log.For("core").Warn("web.push_subscription_refused", "device", d.name, "err", err.Error())
		}
	}
	return d, nil
}

// validPushState reports whether s is a state this build writes: none, ok,
// expired, or "failing: <status>".
func validPushState(s string) bool {
	if s == "" || s == string(PushOK) || s == string(PushExpired) {
		return true
	}
	status, ok := strings.CutPrefix(s, "failing: ")
	if !ok {
		return false
	}
	n, err := strconv.Atoi(status)
	return err == nil && n >= 0 && strconv.Itoa(n) == status
}

// file is the device as web.json stores it.
func (d *webDevice) file() webFileDevice {
	f := webFileDevice{
		Name:        d.name,
		TokenSHA256: hex.EncodeToString(d.tokenHash[:]),
		Paired:      d.paired.UTC(),
		LastSeen:    d.lastSeen.UTC(),
		Notify:      webFileNotify{Waiting: d.notify.Waiting, Finished: d.notify.Finished},
		PushState:   string(d.push),
	}
	if !d.notify.PausedUntil.IsZero() {
		f.Notify.PausedUntil = d.notify.PausedUntil.UTC().Format(time.RFC3339Nano)
	}
	if d.sub != (PushSubscription{}) {
		f.Push = &webFilePush{Endpoint: d.sub.Endpoint, P256DH: d.sub.P256DH, Auth: d.sub.Auth}
	}
	return f
}

// saveWeb writes the state to web.json through config.AtomicWriteFile,
// mode 0600: it holds a token hash per device and the VAPID private key.
// It writes at the path the model loaded from, or, when it has none (a
// NewForTest model that never ran initWeb), under the global config dir,
// and fails with errWebNoDir only when neither is known. It refuses while
// the file is left alone (loadErr), so nothing overwrites what the model
// could not read. A caller changes the state first and puts it back when
// saveWeb fails.
func (m *Model) saveWeb() error {
	w := &m.web
	if w.loadErr != "" {
		return w.leftAlone()
	}
	path := w.path
	if path == "" {
		dir, err := config.GetGlobalConfigDir()
		if err != nil {
			return fmt.Errorf("%w: %s", errWebNoDir, err)
		}
		path = filepath.Join(dir, WebFile)
	}
	f := webFile{Version: webFileVersion, Listen: w.listen, Origin: w.origin, Devices: []webFileDevice{}}
	if len(w.vapid.Private) > 0 {
		f.VAPID = &webFileVAPID{
			Private: base64.RawURLEncoding.EncodeToString(w.vapid.Private),
			Public:  base64.RawURLEncoding.EncodeToString(w.vapid.Public),
		}
	}
	for _, d := range w.devices {
		f.Devices = append(f.Devices, d.file())
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if err := config.AtomicWriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// newVAPIDKey makes the P-256 key pair the first enable keeps for good:
// push subscriptions are bound to its public half.
func newVAPIDKey() (VAPIDKey, error) {
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return VAPIDKey{}, fmt.Errorf("make the VAPID key: %w", err)
	}
	return VAPIDKey{Private: k.Bytes(), Public: k.PublicKey().Bytes()}, nil
}

// newPairingCode makes a code of PairingCodeLen characters of
// PairingAlphabet from crypto/rand. A byte at or above the largest
// multiple of the alphabet's length is skipped, so no symbol is likelier.
func newPairingCode() (string, error) {
	const limit = 256 - 256%len(PairingAlphabet)
	code := make([]byte, 0, PairingCodeLen)
	buf := make([]byte, 2*PairingCodeLen)
	for len(code) < PairingCodeLen {
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("make a pairing code: %w", err)
		}
		for _, b := range buf {
			if int(b) < limit && len(code) < PairingCodeLen {
				code = append(code, PairingAlphabet[int(b)%len(PairingAlphabet)])
			}
		}
	}
	return string(code), nil
}
