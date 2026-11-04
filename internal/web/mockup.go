package web

import (
	"fmt"
	"html"
	"net/http"

	"github.com/aidan-bailey/loom/core"
)

// The mockup route: the page a session's brainstorm companion serves now
// (core.ReadMockup), for the app's iframe. It is the only route that
// replaces guard's Content-Security-Policy, and only with mockupCSP: the
// page is HTML a session wrote, so its scripts run in an opaque origin,
// reaching neither the app's DOM nor /api/v1 with the device cookie, and
// connect nowhere. Its failures are pages too (writePage), since nothing
// but a frame reads the route.

// mockupCSP sandboxes the companion's page: inline scripts and styles run,
// https: resources load, and nothing else.
const mockupCSP = "sandbox allow-scripts; default-src 'none'; script-src 'unsafe-inline' https:; style-src 'unsafe-inline' https:; img-src data: https:; font-src data: https:; connect-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'self'"

// mockupErrorHTML is the frame's page when a read fails, in the app's
// colours; %s is the escaped reason.
const mockupErrorHTML = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><style>body{margin:0;padding:24px 16px;background:#2e2e2e;color:#a0a0a0;font:14px/1.5 ui-monospace,Menlo,monospace}</style></head><body><p>%s</p></body></html>`

// mockupErrorPage is the page that says why a read failed.
func mockupErrorPage(msg string) string {
	return fmt.Sprintf(mockupErrorHTML, html.EscapeString(msg))
}

// writePage answers body as a sandboxed HTML page with status.
func writePage(w http.ResponseWriter, status int, body string) {
	h := w.Header()
	h.Set("Content-Security-Policy", mockupCSP)
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// refusePage records a failed read and answers the page that says why.
func (l *Layer) refusePage(w http.ResponseWriter, a actionLog, status int, msg string) {
	l.record(a, status, msg)
	writePage(w, status, mockupErrorPage(msg))
}

// readMockup answers GET /api/v1/sessions/{id}/mockup: the companion's page,
// or a page that says why there is none. The HTML is never logged.
func (l *Layer) readMockup(w http.ResponseWriter, r *http.Request, device string) {
	a := actionLog{device: device, name: "mockup"}
	id, ok := l.readSession(w, r, &a)
	if !ok {
		return
	}
	reply, ok := l.readAnswering(w, r, a, func(req core.ReqID) { l.client.ReadMockup(id, req) }, l.refusePage)
	if !ok {
		return
	}
	l.record(a, http.StatusOK, "ok")
	writePage(w, http.StatusOK, reply.Mockup.HTML)
}
