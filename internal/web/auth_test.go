package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptedHost is a core.WebHost scripted by a test. It embeds the
// interface, so a method it does not override panics: a test that reaches
// one has found a call it did not expect.
type scriptedHost struct {
	core.WebHost

	// RedeemPairing answers these, and remembers the codes it was asked about.
	name, token string
	err         error
	redeemed    []string

	// DeviceByToken looks a hash up in devices, which maps the SHA-256 of a
	// device's 32 token bytes to its name (hold fills it), and remembers
	// each hash it was asked.
	devices map[[32]byte]string
	asked   [][32]byte

	// TouchDevice remembers the devices it was told of.
	touched []string
}

// hold makes the host know the device called name by the token
// deviceToken(seed), as the model does: by the SHA-256 of the token's 32
// bytes, not of its text.
func (h *scriptedHost) hold(name string, seed byte) {
	if h.devices == nil {
		h.devices = map[[32]byte]string{}
	}
	h.devices[deviceTokenHash(seed)] = name
}

func (h *scriptedHost) RedeemPairing(code string) (string, string, error) {
	h.redeemed = append(h.redeemed, code)
	return h.name, h.token, h.err
}

func (h *scriptedHost) DeviceByToken(hash [32]byte) (string, bool) {
	h.asked = append(h.asked, hash)
	name, ok := h.devices[hash]
	return name, ok
}

func (h *scriptedHost) TouchDevice(name string) { h.touched = append(h.touched, name) }

// deviceToken is a device token as the model makes one: 32 bytes (all seed)
// as unpadded base64url, which is the text the cookie holds.
func deviceToken(seed byte) string {
	return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{seed}, tokenBytes))
}

// deviceTokenHash is the hash the model stores for deviceToken(seed): the
// SHA-256 of the 32 bytes the text decodes to, not of the text.
func deviceTokenHash(seed byte) [32]byte {
	return sha256.Sum256(bytes.Repeat([]byte{seed}, tokenBytes))
}

func TestTokenHash(t *testing.T) {
	good := deviceToken(7)
	hash, ok := tokenHash(good)
	require.True(t, ok)
	assert.Equal(t, deviceTokenHash(7), hash, "the hash of the 32 bytes the text decodes to, as the model stores it")
	assert.NotEqual(t, sha256.Sum256([]byte(good)), hash, "not the hash of the cookie's text")
	other, ok := tokenHash(deviceToken(8))
	require.True(t, ok)
	assert.NotEqual(t, hash, other, "another token, another hash")

	for name, bad := range map[string]string{
		"empty":                 "",
		"a word of 8 bytes":     "not-a-token",
		"31 bytes":              base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, tokenBytes-1)),
		"33 bytes":              base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, tokenBytes+1)),
		"64 bytes":              base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 2*tokenBytes)),
		"padded":                good + "=",
		"the standard alphabet": good[:42] + "+",
		"a space":               good[:42] + " ",
		"not base64 at all":     "!!!!",
	} {
		_, ok := tokenHash(bad)
		assert.False(t, ok, name)
	}
}

func TestDeviceCookie(t *testing.T) {
	rec := httptest.NewRecorder()
	setDeviceCookie(rec, deviceToken(1))
	cookies := rec.Result().Cookies()
	require.Len(t, cookies, 1)
	c := cookies[0]
	assert.Equal(t, "__Host-loom", c.Name)
	assert.Equal(t, deviceToken(1), c.Value)
	assert.Equal(t, "/", c.Path)
	assert.Empty(t, c.Domain, "a __Host- cookie names no domain")
	assert.True(t, c.Secure)
	assert.True(t, c.HttpOnly)
	assert.Equal(t, http.SameSiteStrictMode, c.SameSite)
	assert.Equal(t, 400*24*60*60, c.MaxAge, "400 days, in seconds")

	rec = httptest.NewRecorder()
	clearDeviceCookie(rec)
	cookies = rec.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, "__Host-loom", cookies[0].Name)
	assert.Empty(t, cookies[0].Value)
	assert.Negative(t, cookies[0].MaxAge, "the browser drops it")
	assert.Equal(t, "/", cookies[0].Path)
	assert.True(t, cookies[0].Secure, "a __Host- cookie is refused unless Secure, a clearing one too")
}

// serveAuthed runs one GET carrying cookies through authed on a layer whose
// host is host, and returns the answer and the devices the handler served.
func serveAuthed(host *scriptedHost, cookies ...*http.Cookie) (*httptest.ResponseRecorder, []string) {
	l := &Layer{host: host}
	var served []string
	h := l.authed(func(w http.ResponseWriter, r *http.Request, device string) {
		served = append(served, device)
		writeJSON(w, http.StatusOK, map[string]string{"device": device})
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec, served
}

// requireRefusedUnasked fails unless rec is a 401 with an error object, the
// handler never ran, and the host was neither asked about a token nor told of
// a device.
func requireRefusedUnasked(t *testing.T, rec *httptest.ResponseRecorder, served []string, host *scriptedHost) {
	t.Helper()
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), `"error"`)
	assert.Empty(t, served, "the handler never runs for an unpaired request")
	assert.Empty(t, host.asked, "a cookie that is not a token never reaches the host")
	assert.Empty(t, host.touched, "an unpaired request touches no device")
}

func TestAuthed_AGoodCookieNamesItsDevice(t *testing.T) {
	host := &scriptedHost{}
	host.hold("iPhone", 7)
	host.hold("iPad", 8)

	rec, served := serveAuthed(host, &http.Cookie{Name: cookieName, Value: deviceToken(7)})

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []string{"iPhone"}, served)
	require.Equal(t, [][32]byte{deviceTokenHash(7)}, host.asked, "asked once, by the SHA-256 of the token's 32 bytes")
	assert.NotEqual(t, sha256.Sum256([]byte(deviceToken(7))), host.asked[0], "not by the hash of the cookie's text")
	assert.Equal(t, []string{"iPhone"}, host.touched, "the device was seen")
}

func TestAuthed_TextThatDoesNotDecodeIsRefusedWithoutAskingTheHost(t *testing.T) {
	good := deviceToken(7)
	for name, text := range map[string]string{
		"the standard alphabet": good[:42] + "+",
		"padding":               good + "=",
		"a character in it":     good[:20] + "*" + good[21:],
		"not base64 at all":     "!!!!",
	} {
		t.Run(name, func(t *testing.T) {
			host := &scriptedHost{}
			host.hold("iPhone", 7)
			rec, served := serveAuthed(host, &http.Cookie{Name: cookieName, Value: text})
			requireRefusedUnasked(t, rec, served, host)
		})
	}
}

func TestAuthed_TextOfTheWrongLengthIsRefusedWithoutAskingTheHost(t *testing.T) {
	for name, n := range map[string]int{
		"empty":    0,
		"8 bytes":  8,
		"31 bytes": tokenBytes - 1,
		"33 bytes": tokenBytes + 1,
		"64 bytes": 2 * tokenBytes,
	} {
		t.Run(name, func(t *testing.T) {
			text := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, n))
			host := &scriptedHost{}
			host.hold("iPhone", 7)
			rec, served := serveAuthed(host, &http.Cookie{Name: cookieName, Value: text})
			requireRefusedUnasked(t, rec, served, host)
		})
	}
}

func TestAuthed_AWellFormedTokenNoDeviceHoldsIsRefused(t *testing.T) {
	host := &scriptedHost{}
	host.hold("iPhone", 7)

	rec, served := serveAuthed(host, &http.Cookie{Name: cookieName, Value: deviceToken(8)})

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), `"error"`)
	assert.Empty(t, served, "the handler never runs for an unpaired request")
	assert.Equal(t, [][32]byte{deviceTokenHash(8)}, host.asked, "a well-formed token is asked about, once")
	assert.Empty(t, host.touched, "an unpaired request touches no device")
}

func TestAuthed_NoDeviceCookieIsRefusedWithoutAskingTheHost(t *testing.T) {
	for name, cookies := range map[string][]*http.Cookie{
		"no cookie":           nil,
		"another cookie name": {{Name: "loom", Value: deviceToken(7)}},
	} {
		t.Run(name, func(t *testing.T) {
			host := &scriptedHost{}
			host.hold("iPhone", 7)
			rec, served := serveAuthed(host, cookies...)
			requireRefusedUnasked(t, rec, served, host)
		})
	}
}

func TestPair_AnswersWhatTheHostSays(t *testing.T) {
	good := deviceToken(7)
	const code = `{"code": "AAAA-AAAA"}`
	for _, tc := range []struct {
		name     string
		body     string
		err      error // what the host answers
		status   int
		redeems  []string // the codes the host was asked about
		sentinel error    // the refusal whose own text is the whole answer, or nil
		text     string   // part of the answer
	}{
		{"a code that pairs", `{"code": "k7qm-2xpa"}`, nil, http.StatusOK, []string{"k7qm-2xpa"}, nil, `"device":"iPhone"`},
		{"a wrong code is a 400, not a 401 (which the app reads as unpaired)", code, core.ErrPairingCode, http.StatusBadRequest, []string{"AAAA-AAAA"}, core.ErrPairingCode, "wrong, used or expired"},
		{"a wrapped wrong code is the same refusal", code, fmt.Errorf("redeem: %w", core.ErrPairingCode), http.StatusBadRequest, []string{"AAAA-AAAA"}, core.ErrPairingCode, "wrong, used or expired"},
		{"too many wrong codes", code, core.ErrPairingLocked, http.StatusTooManyRequests, []string{"AAAA-AAAA"}, core.ErrPairingLocked, "run loom web pair again"},
		{"a name taken", code, core.ErrDeviceExists, http.StatusConflict, []string{"AAAA-AAAA"}, core.ErrDeviceExists, "has that name already"},
		{"a name taken, wrapped with the name", code, fmt.Errorf("%w: %q", core.ErrDeviceExists, "iPhone"), http.StatusConflict, []string{"AAAA-AAAA"}, core.ErrDeviceExists, "has that name already"},
		{"16 devices paired", code, core.ErrTooManyDevices, http.StatusConflict, []string{"AAAA-AAAA"}, core.ErrTooManyDevices, "16 devices are paired"},
		{"the web off", code, core.ErrWebOff, http.StatusServiceUnavailable, []string{"AAAA-AAAA"}, core.ErrWebOff, "loom web enable"},
		{"a failure it does not name", code, errors.New("disk on fire"), http.StatusInternalServerError, []string{"AAAA-AAAA"}, nil, "pairing failed"},
		{"a field the route does not take", `{"code": "AAAA-AAAA", "name": "x"}`, nil, http.StatusBadRequest, nil, nil, "unknown field"},
		{"a code of the wrong type", `{"code": 7}`, nil, http.StatusBadRequest, nil, nil, "not the JSON"},
		{"no body", ``, nil, http.StatusBadRequest, nil, nil, "empty"},
		{"a code far too long", `{"code": "` + strings.Repeat("A", maxCodeLen+1) + `"}`, nil, http.StatusBadRequest, nil, nil, "too long"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host := &scriptedHost{name: "iPhone", token: good, err: tc.err}
			l := &Layer{host: host}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/pair", strings.NewReader(tc.body))
			rec := httptest.NewRecorder()
			l.mux().ServeHTTP(rec, req)

			assert.Equal(t, tc.status, rec.Code)
			assert.Contains(t, rec.Body.String(), tc.text)
			assert.Equal(t, tc.redeems, host.redeemed)
			if tc.sentinel != nil {
				want, err := json.Marshal(map[string]string{"error": tc.sentinel.Error()})
				require.NoError(t, err)
				assert.JSONEq(t, string(want), rec.Body.String(), "the sentinel's own text, nothing the host wrapped around it")
			}
			cookies := rec.Result().Cookies()
			if tc.status != http.StatusOK {
				assert.Empty(t, cookies, "no cookie unless the code paired")
				assert.NotContains(t, rec.Body.String(), "disk on fire", "an error the layer does not name is not echoed")
				return
			}
			require.Len(t, cookies, 1)
			assert.Equal(t, cookieName, cookies[0].Name)
			assert.Equal(t, good, cookies[0].Value, "the token the host made, unchanged")
		})
	}
}

func TestPair_TakesPostOnly(t *testing.T) {
	host := &scriptedHost{}
	l := &Layer{host: host}
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete} {
		rec := httptest.NewRecorder()
		l.mux().ServeHTTP(rec, httptest.NewRequest(method, "/api/v1/pair", nil))
		assert.Equal(t, http.StatusMethodNotAllowed, rec.Code, method)
	}
	assert.Empty(t, host.redeemed)
}

// A code the model made pairs a phone once, and the cookie it gets names the
// device to the layer through the model: the hash the layer computes from the
// 32 bytes the token decodes to is the one the model stored.
func TestPair_ACodeFromTheModelPairsAPhoneOnce(t *testing.T) {
	s := newWebStack(t)
	s.start(t)
	addr, _ := s.enable(t)
	pairing, err := s.tui.PairDevice("iPhone")
	require.NoError(t, err)
	typed := strings.ToLower(pairing.Code[:4] + "-" + pairing.Code[4:])
	payload := `{"code": "` + typed + `"}`

	resp := httpDo(t, addr, http.MethodPost, "/api/v1/pair", payload)
	require.Equal(t, http.StatusOK, resp.StatusCode, bodyText(t, resp))
	cookies := resp.Cookies()
	require.Len(t, cookies, 1)
	cookie := cookies[0]
	assert.Equal(t, cookieName, cookie.Name)
	raw, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	require.NoError(t, err, "the model's token is unpadded base64url")
	assert.Len(t, raw, tokenBytes, "of 32 bytes")

	again := httpDo(t, addr, http.MethodPost, "/api/v1/pair", payload)
	assert.Equal(t, http.StatusBadRequest, again.StatusCode, "a code works once; a used one is a wrong one, not an unpaired phone")
	assert.Empty(t, again.Cookies())

	status, device := s.whoAmI(cookie)
	require.Equal(t, http.StatusNoContent, status, "the hash of the token's 32 bytes, computed here, is the one the model stored")
	assert.Equal(t, "iPhone", device)
	status, device = s.whoAmI(&http.Cookie{Name: cookieName, Value: deviceToken(9)})
	assert.Equal(t, http.StatusUnauthorized, status, "a well-formed token the model never made")
	assert.Empty(t, device)
}

func TestAuthed_RevokingADeviceRefusesOnlyItsCookie(t *testing.T) {
	s := newWebStack(t)
	s.start(t)
	addr, _ := s.enable(t)
	phone := s.pair(t, addr, "iPhone")
	tablet := s.pair(t, addr, "iPad")

	status, device := s.whoAmI(phone)
	require.Equal(t, http.StatusNoContent, status)
	require.Equal(t, "iPhone", device)

	require.NoError(t, s.tui.RevokeDevice("iPhone"))
	status, device = s.whoAmI(phone)
	assert.Equal(t, http.StatusUnauthorized, status, "a revoked device's cookie")
	assert.Empty(t, device)
	status, device = s.whoAmI(tablet)
	assert.Equal(t, http.StatusNoContent, status, "another device's cookie still works")
	assert.Equal(t, "iPad", device)
}
