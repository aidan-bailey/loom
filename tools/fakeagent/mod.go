package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
// like any typed line: one reading exit or crash ends the agent. Like the
// module it names a poller of its own and its load time in its hello, the
// poller in its polls, and stops for good when the daemon answers either 409
// (a mod loaded later holds the launch).
type modClient struct {
	launch string
	poller string
	loaded int64 // Unix milliseconds
	http   *http.Client
	out    chan modMsg
}

// newModClient is the mod for this launch, nil without its environment.
func newModClient(getenv func(string) string) *modClient {
	socket, launch := getenv(claudemod.EnvSocket), getenv(claudemod.EnvLaunch)
	if socket == "" || launch == "" {
		return nil
	}
	return &modClient{launch: launch, poller: newTurnID(), loaded: time.Now().UnixMilli(), out: make(chan modMsg, 64), http: &http.Client{Transport: &http.Transport{
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
	req, err := http.NewRequestWithContext(ctx, method, fmt.Sprintf("http://loom/v%d%s", claudemod.Version, path), r)
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

// The mod's retry schedule (backoff in register.ts): after a failure it
// waits 1 s, doubling to 5 s, until a call succeeds.
const (
	firstBackoff = time.Second
	maxBackoff   = 5 * time.Second
)

// backoff is the wait after a failure that followed a wait of d (0 after a
// success), as the mod's backoff computes it.
func backoff(d time.Duration) time.Duration {
	if d == 0 {
		return firstBackoff
	}
	return min(2*d, maxBackoff)
}

// run polls until ctx ends, which the agent does as it exits, or until the
// daemon answers a hello or a poll 409. Like the mod it says hello again after any other
// failure, retries on the mod's backoff, and reports a message the daemon
// offers again (its queued report was lost) queued again without handing it
// over twice.
func (c *modClient) run(ctx context.Context) {
	greeted := false
	seen := map[string]bool{}
	var delay time.Duration
	for ctx.Err() == nil {
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return
			}
		}
		if !greeted {
			status := c.post(ctx, "/hello", map[string]any{"launch": c.launch, "mod": claudemod.Version, "claude": "fakeagent", "session": "fake", "poller": c.poller, "loaded": c.loaded})
			if status == http.StatusConflict {
				return // superseded: a mod loaded later holds this launch
			}
			greeted = status == http.StatusNoContent
			if !greeted {
				delay = backoff(delay)
				continue
			}
		}
		resp, err := c.call(ctx, http.MethodGet, "/inbox?launch="+url.QueryEscape(c.launch)+"&poller="+url.QueryEscape(c.poller), nil)
		if err != nil {
			greeted = false
			delay = backoff(delay)
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
			delay = 0
		case http.StatusNoContent:
			delay = 0
		case http.StatusConflict:
			return // superseded: a newer mod polls for this launch
		default:
			greeted = false
			delay = backoff(delay)
		}
	}
}

// newTurnID is a turn ID, random like Claude's, and the mod's poller ID.
func newTurnID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
