package web

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/log"
)

const (
	// cookieName is the device cookie. Browsers accept a __Host- cookie only
	// when it is Secure, has Path=/ and names no Domain, so no sibling host
	// can plant one.
	cookieName = "__Host-loom"
	// cookieMaxAge is 400 days in seconds, the longest a browser keeps one.
	cookieMaxAge = 400 * 24 * 60 * 60
	// tokenBytes is the size of a device token before it is encoded.
	tokenBytes = 32
	// maxCodeLen bounds the pairing code a request may carry. A code is eight
	// characters and the model drops dashes and spaces, so this is ample.
	maxCodeLen = 64
)

// deviceHandler serves a request from the paired device it names.
type deviceHandler func(w http.ResponseWriter, r *http.Request, device string)

// setDeviceCookie gives the browser the device token, as the one cookie the
// layer trusts.
func setDeviceCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, deviceCookie(token, cookieMaxAge))
}

// clearDeviceCookie makes the browser drop the device cookie.
func clearDeviceCookie(w http.ResponseWriter) {
	http.SetCookie(w, deviceCookie("", -1))
}

// deviceCookie is the cookie with the attributes that make it safe to hold a
// credential: Secure and Path=/ for the __Host- prefix, HttpOnly so no
// script reads it, SameSite=Strict so no cross-site request carries it.
func deviceCookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     cookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	}
}

// tokenHash is the hash the host knows a device by: the SHA-256 of the 32
// bytes the cookie's unpadded base64url text decodes to, which is what the
// model stored when it made the token. Hashing the text instead would match
// no device. A value that does not decode to exactly 32 bytes is no token,
// and has no hash.
func tokenHash(value string) ([32]byte, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) != tokenBytes {
		return [32]byte{}, false
	}
	return sha256.Sum256(raw), true
}

// authed is h for a paired device only: a request whose cookie names no
// device is a 401, on which the app clears its snapshot and shows pairing.
// A cookie that is not a device token is refused without asking the host.
// The device is touched (last seen) before h runs.
func (l *Layer) authed(h deviceHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		device, ok := l.deviceByCookie(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "this phone is not paired")
			return
		}
		l.host.TouchDevice(device)
		h(w, r, device)
	})
}

// deviceByCookie is the device r's cookie names, asked of the host by the
// hash of the token's 32 bytes (tokenHash). A cookie that is not a token is
// never looked up.
func (l *Layer) deviceByCookie(r *http.Request) (string, bool) {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return "", false
	}
	hash, ok := tokenHash(c.Value)
	if !ok {
		return "", false
	}
	return l.host.DeviceByToken(hash)
}

// routePair registers the one route a phone can call before it is paired.
func (l *Layer) routePair(m *http.ServeMux) {
	m.Handle("POST /api/v1/pair", http.HandlerFunc(l.pair))
}

// pairRefusals are the host's refusals of a pairing code, with the status
// the app keys on, tried in order with errors.Is. A wrong code is a 400 and
// never a 401: the app reads every 401 as "this phone is not paired" and
// clears what it holds.
var pairRefusals = []struct {
	err    error
	status int
}{
	{core.ErrPairingLocked, http.StatusTooManyRequests},
	{core.ErrPairingCode, http.StatusBadRequest},
	{core.ErrDeviceExists, http.StatusConflict},
	{core.ErrTooManyDevices, http.StatusConflict},
	{core.ErrWebOff, http.StatusServiceUnavailable},
}

// pair trades a pairing code for the device cookie. The model decides
// whether the code is right (it consumes it, counts the misses and makes the
// device); this maps its answer: a refusal in pairRefusals answers
// {"error": <the sentinel's own text>}, never the text of what the host
// wrapped around it, and any other error is a fixed 500, since an error can
// carry paths or names.
func (l *Layer) pair(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(body.Code) > maxCodeLen {
		writeError(w, http.StatusBadRequest, "the code is too long")
		return
	}
	name, token, err := l.host.RedeemPairing(body.Code)
	if err == nil {
		setDeviceCookie(w, token)
		log.For("web").Info("web.paired", "device", name)
		writeJSON(w, http.StatusOK, map[string]string{"device": name})
		return
	}
	for _, refusal := range pairRefusals {
		if errors.Is(err, refusal.err) {
			log.For("web").Warn("web.pair_refused", "reason", refusal.err.Error())
			writeError(w, refusal.status, refusal.err.Error())
			return
		}
	}
	log.For("web").Error("web.pair_failed", "err", err)
	writeError(w, http.StatusInternalServerError, "pairing failed; the daemon's log says why")
}
