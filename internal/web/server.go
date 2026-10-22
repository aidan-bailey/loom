package web

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// maxBody caps a request's body, as the mod socket's own cap does.
const maxBody = 1 << 20

// contentSecurityPolicy lets the page load only its own files and images
// from itself or data: URLs, and forbids framing, a base tag and any form
// post elsewhere. The markup has no inline script and no style attribute,
// so nothing needs 'unsafe-inline'.
const contentSecurityPolicy = "default-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"

// originRule is the origin a listener serves, as guard enforces it: the
// scheme and the host with the scheme's default port dropped, in lower case,
// so https://loom.example.com and https://loom.example.com:443 are one.
type originRule struct {
	scheme string
	host   string
}

// parseOrigin reads the configured origin: scheme://host[:port] with no
// user, path, query or fragment. The model validates the same shape before it
// stores one; a value that fails here serves nothing rather than anything.
func parseOrigin(raw string) (originRule, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		u.User != nil || u.Opaque != "" || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return originRule{}, fmt.Errorf("the origin %q is not scheme://host[:port]", raw)
	}
	return originRule{scheme: u.Scheme, host: hostKey(u.Scheme, u.Host)}, nil
}

// hostKey is hostport in lower case with scheme's default port dropped.
func hostKey(scheme, hostport string) string {
	hostport = strings.ToLower(hostport)
	host, port, err := net.SplitHostPort(hostport)
	if err != nil || port != defaultPort(scheme) {
		return hostport
	}
	if strings.Contains(host, ":") {
		return "[" + host + "]"
	}
	return host
}

// defaultPort is the port a URL of scheme names when it names none.
func defaultPort(scheme string) string {
	switch scheme {
	case "http":
		return "80"
	case "https":
		return "443"
	}
	return ""
}

// allowsHost reports whether a request's Host header names the origin's
// host. The tunnel passes the Host the phone asked for, and a rebound DNS
// name arrives with its own.
func (o originRule) allowsHost(host string) bool {
	return host != "" && hostKey(o.scheme, host) == o.host
}

// allowsOrigin reports whether an Origin header is the origin itself:
// browsers send the scheme, host and port alone, so a path, a user or a
// query means it is not one.
func (o originRule) allowsOrigin(header string) bool {
	u, err := url.Parse(header)
	if err != nil || u.Scheme != o.scheme || u.Host == "" ||
		u.User != nil || u.Opaque != "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	return hostKey(u.Scheme, u.Host) == o.host
}

// guard is the door every route stands behind. It sets the security headers
// on every answer, refuses a Host that is not the origin's (421: DNS
// rebinding), refuses any request but a GET or HEAD without the origin's own
// Origin header (403: a cross-site post), and caps the body at maxBody. With
// no origin rule (no listener runs) it refuses everything. It hands next the
// ResponseWriter it was given, unwrapped, so a stream flushes through
// http.NewResponseController.
func (l *Layer) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		rule := l.rule.Load()
		if rule == nil || !rule.allowsHost(r.Host) {
			writeError(w, http.StatusMisdirectedRequest, "this is not the host loom serves")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if origins := r.Header.Values("Origin"); len(origins) != 1 || !rule.allowsOrigin(origins[0]) {
				writeError(w, http.StatusForbidden, "the request does not come from loom's origin")
				return
			}
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		next.ServeHTTP(w, r)
	})
}
