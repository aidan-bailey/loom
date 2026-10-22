package web

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteJSON_AnswersTheStatusAndTheBody(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusCreated, map[string]any{"id": 7, "notices": []string{}})
	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"id": 7, "notices": []}`, rec.Body.String())
}

func TestWriteJSON_AValueThatWillNotEncodeIsA500(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusOK, map[string]any{"c": make(chan int)})
	assert.Equal(t, http.StatusInternalServerError, rec.Code, "never a 200 with half a body")
	assert.JSONEq(t, `{"error": "the answer could not be encoded"}`, rec.Body.String())
}

func TestWriteError_IsAnErrorObject(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, http.StatusConflict, "the session is already paused")
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.JSONEq(t, `{"error": "the session is already paused"}`, rec.Body.String())
}

func TestReadJSON(t *testing.T) {
	type body struct {
		Code string `json:"code"`
	}
	for _, tc := range []struct {
		name  string
		body  string
		limit int64  // a cap on the body, as guard sets; 0 for none
		want  string // part of the error, or "" for none
	}{
		{"one object", `{"code": "K7QM"}`, 0, ""},
		{"white space after it", "{\"code\": \"K7QM\"}\n  \n", 0, ""},
		{"a field the route does not take", `{"code": "K7QM", "extra": 1}`, 0, `unknown field "extra"`},
		{"nothing", ``, 0, "the body is empty"},
		{"only white space", " \n", 0, "the body is empty"},
		{"two objects", `{"code": "A"} {"code": "B"}`, 0, "more than one JSON value"},
		{"junk after the object", `{"code": "A"} x`, 0, "more than one JSON value"},
		{"not JSON", `not json`, 0, "the body is not the JSON this route takes"},
		{"truncated", `{"code": `, 0, "the body is not the JSON this route takes"},
		{"the wrong type", `{"code": 7}`, 0, "the body is not the JSON this route takes"},
		{"over the cap", `{"code": "K7QM-2XPA-and-more"}`, 16, "the body is over 16 bytes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(tc.body))
			if tc.limit > 0 {
				req.Body = http.MaxBytesReader(httptest.NewRecorder(), req.Body, tc.limit)
			}
			var got body
			err := readJSON(req, &got)
			if tc.want == "" {
				require.NoError(t, err)
				assert.Equal(t, "K7QM", got.Code)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// guardOrigin is the origin the guard tests enforce.
const guardOrigin = "https://loom.example.com"

// guardedBy is guard around a handler that answers 200 with the number of
// body bytes it read, or 413 with the reader's error. The layer enforces
// origin, as a bound listener's does.
func guardedBy(t *testing.T, origin string) http.Handler {
	t.Helper()
	rule, err := parseOrigin(origin)
	require.NoError(t, err)
	l := &Layer{}
	l.rule.Store(&rule)
	return l.guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, err := io.Copy(io.Discard, r.Body)
		if err != nil {
			writeError(w, http.StatusRequestEntityTooLarge, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]int64{"read": n})
	}))
}

// sendGuarded runs one request through h with host as its Host header and
// origins as its Origin headers (none when nil).
func sendGuarded(h http.Handler, method, host string, origins []string, body io.Reader) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/api/v1/x", body)
	req.Host = host
	if origins != nil {
		req.Header["Origin"] = origins
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestParseOrigin(t *testing.T) {
	for origin, host := range map[string]string{
		"https://loom.example.com":      "loom.example.com",
		"https://LOOM.example.com:443":  "loom.example.com",
		"https://loom.example.com:8443": "loom.example.com:8443",
		"http://127.0.0.1:7420":         "127.0.0.1:7420",
		"http://localhost:80":           "localhost",
	} {
		rule, err := parseOrigin(origin)
		require.NoError(t, err, origin)
		assert.Equal(t, host, rule.host, origin)
	}
	for _, bad := range []string{
		"", "loom.example.com", "ftp://loom.example.com", "https://", "https://user@loom.example.com",
		"https://loom.example.com/path", "https://loom.example.com/", "https://loom.example.com?x=1",
		"https://loom.example.com?", "https://loom.example.com#f",
	} {
		_, err := parseOrigin(bad)
		assert.Error(t, err, bad)
	}
}

func TestGuard_TheHostMustBeTheOrigins(t *testing.T) {
	for _, tc := range []struct {
		name   string
		origin string
		host   string
		status int
	}{
		{"the origin's host", guardOrigin, "loom.example.com", http.StatusOK},
		{"in any case", guardOrigin, "LOOM.Example.COM", http.StatusOK},
		{"with the scheme's default port", guardOrigin, "loom.example.com:443", http.StatusOK},
		{"another port", guardOrigin, "loom.example.com:8443", http.StatusMisdirectedRequest},
		{"another host", guardOrigin, "evil.example", http.StatusMisdirectedRequest},
		{"the host as a prefix of another", guardOrigin, "loom.example.com.evil.example", http.StatusMisdirectedRequest},
		{"the host as a suffix of another", guardOrigin, "evil-loom.example.com", http.StatusMisdirectedRequest},
		{"no host", guardOrigin, "", http.StatusMisdirectedRequest},
		{"an origin with a port", "https://loom.example.com:8443", "loom.example.com:8443", http.StatusOK},
		{"an origin with a port, asked for by the bare host", "https://loom.example.com:8443", "loom.example.com", http.StatusMisdirectedRequest},
		{"an origin naming its default port", "https://loom.example.com:443", "loom.example.com", http.StatusOK},
		{"a development origin", "http://127.0.0.1:7420", "127.0.0.1:7420", http.StatusOK},
		{"a development origin on another port", "http://127.0.0.1:7420", "127.0.0.1:7421", http.StatusMisdirectedRequest},
		{"localhost is not 127.0.0.1", "http://127.0.0.1:7420", "localhost:7420", http.StatusMisdirectedRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := sendGuarded(guardedBy(t, tc.origin), http.MethodGet, tc.host, nil, nil)
			assert.Equal(t, tc.status, rec.Code)
		})
	}
}

func TestGuard_AnUnsafeMethodMustCarryTheOrigin(t *testing.T) {
	for _, tc := range []struct {
		name    string
		method  string
		origins []string
		status  int
	}{
		{"a POST from the origin", http.MethodPost, []string{guardOrigin}, http.StatusOK},
		{"a POST without an Origin", http.MethodPost, nil, http.StatusForbidden},
		{"a POST from another site", http.MethodPost, []string{"https://evil.example"}, http.StatusForbidden},
		{"a POST from the http scheme", http.MethodPost, []string{"http://loom.example.com"}, http.StatusForbidden},
		{"a POST from another port", http.MethodPost, []string{"https://loom.example.com:8443"}, http.StatusForbidden},
		{"a POST from a subdomain", http.MethodPost, []string{"https://app.loom.example.com"}, http.StatusForbidden},
		{"an opaque origin", http.MethodPost, []string{"null"}, http.StatusForbidden},
		{"an Origin with a path", http.MethodPost, []string{guardOrigin + "/x"}, http.StatusForbidden},
		{"two Origin headers", http.MethodPost, []string{guardOrigin, guardOrigin}, http.StatusForbidden},
		{"the origin in another case", http.MethodPost, []string{"HTTPS://LOOM.EXAMPLE.COM"}, http.StatusOK},
		{"the origin naming its default port", http.MethodPost, []string{"https://loom.example.com:443"}, http.StatusOK},
		{"a PUT without an Origin", http.MethodPut, nil, http.StatusForbidden},
		{"a DELETE from another site", http.MethodDelete, []string{"https://evil.example"}, http.StatusForbidden},
		{"a GET from another site is not checked", http.MethodGet, []string{"https://evil.example"}, http.StatusOK},
		{"a GET without one", http.MethodGet, nil, http.StatusOK},
		{"a HEAD from another site is not checked", http.MethodHead, []string{"https://evil.example"}, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := sendGuarded(guardedBy(t, guardOrigin), tc.method, "loom.example.com", tc.origins, nil)
			assert.Equal(t, tc.status, rec.Code)
		})
	}
}

func TestGuard_EveryAnswerCarriesTheSecurityHeaders(t *testing.T) {
	h := guardedBy(t, guardOrigin)
	for _, tc := range []struct {
		name   string
		rec    *httptest.ResponseRecorder
		status int
	}{
		{"served", sendGuarded(h, http.MethodGet, "loom.example.com", nil, nil), http.StatusOK},
		{"a wrong host", sendGuarded(h, http.MethodGet, "evil.example", nil, nil), http.StatusMisdirectedRequest},
		{"a wrong origin", sendGuarded(h, http.MethodPost, "loom.example.com", []string{"https://evil.example"}, nil), http.StatusForbidden},
	} {
		assert.Equal(t, tc.status, tc.rec.Code, tc.name)
		header := tc.rec.Header()
		assert.Equal(t, "default-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'",
			header.Get("Content-Security-Policy"), tc.name)
		assert.Equal(t, "nosniff", header.Get("X-Content-Type-Options"), tc.name)
		assert.Equal(t, "no-referrer", header.Get("Referrer-Policy"), tc.name)
	}
}

func TestGuard_NoRuleAnswersNothing(t *testing.T) {
	called := false
	l := &Layer{}
	h := l.guard(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	rec := sendGuarded(h, http.MethodGet, "loom.example.com", nil, nil)
	assert.Equal(t, http.StatusMisdirectedRequest, rec.Code, "a layer with no listener refuses every request")
	assert.False(t, called)
	assert.NotEmpty(t, rec.Header().Get("Content-Security-Policy"), "the refusal carries the headers too")
}

func TestGuard_TheBodyIsCapped(t *testing.T) {
	h := guardedBy(t, guardOrigin)

	rec := sendGuarded(h, http.MethodPost, "loom.example.com", []string{guardOrigin}, strings.NewReader(strings.Repeat("a", maxBody)))
	assert.Equal(t, http.StatusOK, rec.Code, "a body of exactly the cap")
	assert.JSONEq(t, `{"read": 1048576}`, rec.Body.String())

	rec = sendGuarded(h, http.MethodPost, "loom.example.com", []string{guardOrigin}, strings.NewReader(strings.Repeat("a", maxBody+1)))
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code, "one byte more")
	assert.Contains(t, rec.Body.String(), "too large")
}

// The stream (Story 7) flushes each event through http.ResponseController,
// which reaches the real Flusher only if guard hands the handler the
// ResponseWriter it was given, or one with an Unwrap() http.ResponseWriter.
// The handler stays open after its flush, so the client has the first event
// only if the flush went through to the connection.
func TestGuard_AHandlerBehindItCanFlushToTheClient(t *testing.T) {
	unblock := make(chan struct{})
	flushed := make(chan error, 1)
	l := &Layer{}
	srv := httptest.NewServer(l.guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: ping\n\n")
		flushed <- http.NewResponseController(w).Flush()
		<-unblock
	})))
	defer srv.Close()
	defer close(unblock) // runs first, so Close does not wait on the handler
	rule, err := parseOrigin(srv.URL)
	require.NoError(t, err)
	l.rule.Store(&rule)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(srv.URL)
	require.NoError(t, err, "the response head reached the client before the handler returned")
	defer func() { _ = resp.Body.Close() }()

	require.NoError(t, <-flushed, "Flush through the ResponseController failed behind guard")
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, "event: ping\n", line)
}
