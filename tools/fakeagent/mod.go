package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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
// Claude it never merges messages into one turn.
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

// post sends body to path and returns the status, 0 when the request failed.
func (c *modClient) post(path string, body any) int {
	b, _ := json.Marshal(body)
	resp, err := c.http.Post("http://loom/v1"+path, "application/json", bytes.NewReader(b))
	if err != nil {
		return 0
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// report posts one event about a message; like the mod, it drops a failure.
func (c *modClient) report(ev map[string]any) {
	c.post("/events", map[string]any{"launch": c.launch, "events": []any{ev}})
}

// run polls until the process exits. Like the mod it says hello again after
// any failure, and reports a message the daemon offers again (its queued
// report was lost) queued again without handing it over twice.
func (c *modClient) run() {
	greeted := false
	seen := map[string]bool{}
	for {
		if !greeted {
			greeted = c.post("/hello", map[string]any{"launch": c.launch, "mod": claudemod.Version, "claude": "fakeagent", "session": "fake"}) == http.StatusNoContent
			if !greeted {
				time.Sleep(200 * time.Millisecond)
				continue
			}
		}
		resp, err := c.http.Get("http://loom/v1/inbox?launch=" + url.QueryEscape(c.launch))
		if err != nil {
			greeted = false
			time.Sleep(200 * time.Millisecond)
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
				c.report(map[string]any{"kind": "queued", "id": m.ID})
				if !seen[m.ID] {
					seen[m.ID] = true
					c.out <- m
				}
			}
		case http.StatusNoContent:
		default:
			greeted = false
			time.Sleep(200 * time.Millisecond)
		}
	}
}

// newTurnID is a turn ID, random like Claude's.
func newTurnID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
