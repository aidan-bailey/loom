package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/aidan-bailey/loom/session/claudemod"
)

// modMsg is a message the loom daemon has for the agent.
type modMsg struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// modClient plays the loom mod (session/claudemod) for e2e tests: with
// LOOM_MOD_SOCKET and LOOM_LAUNCH set it says hello, long-polls the inbox,
// reports each message queued as it arrives, and hands it to the agent, which
// runs it as a prompt once idle, as Claude runs a submitted prompt. Unlike
// Claude it never merges messages into one turn, and a message is a command
// like any typed line: one reading exit or crash ends the agent.
type modClient struct {
	launch string
	http   *http.Client
	out    chan modMsg
}

// newModClient is the mod for this launch, nil without its environment.
func newModClient(getenv func(string) string) *modClient {
	socket, launch := getenv(claudemod.EnvSocket), getenv(claudemod.EnvLaunch)
	if socket == "" || launch == "" {
		return nil
	}
	return &modClient{launch: launch, out: make(chan modMsg, 64), http: &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
	}}}
}

// call makes one request to the mod API, bound to ctx.
func (c *modClient) call(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://loom/v1"+path, r)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("content-type", "application/json")
	}
	return c.http.Do(req)
}

// post sends body to path and returns the status, 0 when the request failed.
func (c *modClient) post(ctx context.Context, path string, body any) int {
	resp, err := c.call(ctx, http.MethodPost, path, body)
	if err != nil {
		return 0
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// report posts one event about a message; like the mod, it drops a failure.
func (c *modClient) report(ctx context.Context, ev map[string]any) {
	c.post(ctx, "/events", map[string]any{"launch": c.launch, "events": []any{ev}})
}

// run polls until ctx ends, which the agent does as it exits. Like the mod
// it says hello again after any failure, and reports a message the daemon
// offers again (its queued report was lost) queued again without handing it
// over twice.
func (c *modClient) run(ctx context.Context) {
	greeted := false
	seen := map[string]bool{}
	pause := func() {
		select {
		case <-time.After(200 * time.Millisecond):
		case <-ctx.Done():
		}
	}
	for ctx.Err() == nil {
		if !greeted {
			greeted = c.post(ctx, "/hello", map[string]any{"launch": c.launch, "mod": claudemod.Version, "claude": "fakeagent", "session": "fake"}) == http.StatusNoContent
			if !greeted {
				pause()
				continue
			}
		}
		resp, err := c.call(ctx, http.MethodGet, "/inbox?launch="+url.QueryEscape(c.launch), nil)
		if err != nil {
			greeted = false
			pause()
			continue
		}
		var body struct {
			Messages []modMsg `json:"messages"`
		}
		status := resp.StatusCode
		if status == http.StatusOK {
			_ = json.NewDecoder(resp.Body).Decode(&body)
		}
		_ = resp.Body.Close()
		switch status {
		case http.StatusOK:
			// Reported before the handover, so an idle agent's started
			// never overtakes it.
			for _, m := range body.Messages {
				c.report(ctx, map[string]any{"kind": "queued", "id": m.ID})
				if seen[m.ID] {
					continue
				}
				seen[m.ID] = true
				select {
				case c.out <- m:
				case <-ctx.Done():
					return
				}
			}
		case http.StatusNoContent:
		default:
			greeted = false
			pause()
		}
	}
}

// newTurnID is a turn ID, random like Claude's.
func newTurnID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
