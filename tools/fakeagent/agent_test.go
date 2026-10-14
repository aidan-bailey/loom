package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session/agent"
	"github.com/aidan-bailey/loom/session/claudemod"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runScript(t *testing.T, argv0, script string) (string, int, *fakeAgent) {
	t.Helper()
	var out bytes.Buffer
	a := newFakeAgent(personaFor(argv0), strings.NewReader(script), &out, t.TempDir())
	a.sleep = func(time.Duration) {}
	code := a.run()
	return out.String(), code, a
}

func TestPersonaFor_MatchesLoomAdapters(t *testing.T) {
	reg := agent.DefaultRegistry()
	for _, argv0 := range []string{"/sbx/bin/personas/claude", "/sbx/bin/personas/aider"} {
		p := personaFor(argv0)
		ad := reg.Lookup(argv0)
		assert.Equal(t, ad.Name(), p.name, argv0)
		assert.Equal(t, ad.PendingPromptPattern(), p.pendingPrompt, argv0)
		assert.Equal(t, ad.TrustPromptPatterns()[0], p.trustPrompt, argv0)
	}
}

func TestPersonaFor_GenericFallback(t *testing.T) {
	p := personaFor("/sbx/bin/personas/fakeagent")
	assert.Equal(t, genericPendingPrompt, p.pendingPrompt)
	assert.Equal(t, genericTrustPrompt, p.trustPrompt)
}

func TestRun_AskPrintsPendingPatternThenClears(t *testing.T) {
	out, code, _ := runScript(t, "aider", "ask\ny\n")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "(Y)es/(N)o/(D)on't ask again")
	assert.Contains(t, out, clearScreen+`answered: "y"`)
}

func TestRun_TrustWaitsForDismissal(t *testing.T) {
	out, _, _ := runScript(t, "claude", "trust\n\n")
	assert.Contains(t, out, "Do you trust the files in this folder?")
	assert.Contains(t, out, clearScreen+`trusted: ""`)
}

// lockedBuffer is a bytes.Buffer a test can read while the agent writes.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// With --trust-first the agent stops at the trust prompt before its banner
// or its first hook, and goes on only once the prompt is answered.
func TestRun_TrustFirstWaitsBeforeTheBanner(t *testing.T) {
	out := &lockedBuffer{}
	in, answer := io.Pipe()
	a := newFakeAgent(personaFor("claude"), in, out, t.TempDir())
	a.trustFirst = true
	done := make(chan int)
	go func() { done <- a.run() }()

	require.Eventually(t, func() bool { return strings.Contains(out.String(), "Do you trust the files in this folder?") },
		5*time.Second, 5*time.Millisecond)
	assert.NotContains(t, out.String(), "commands: work N", "no banner while the prompt is unanswered")

	_, err := answer.Write([]byte("\nexit\n"))
	require.NoError(t, err)
	assert.Equal(t, 0, <-done)
	assert.Regexp(t, `Do you trust the files in this folder\?\n`+regexp.QuoteMeta(clearScreen)+`trusted: ""\nfakeagent: claude persona\ncommands: work N`, out.String())
}

func TestRun_WorkTicksTenTimesPerSecond(t *testing.T) {
	var sleeps int
	var out bytes.Buffer
	a := newFakeAgent(personaFor("fakeagent"), strings.NewReader("work 2\n"), &out, t.TempDir())
	a.sleep = func(time.Duration) { sleeps++ }
	a.run()
	assert.Equal(t, 20, sleeps)
	assert.Contains(t, out.String(), "working 20/20")
	assert.Contains(t, out.String(), "work done")
}

func TestRun_WorkRejectsBadArg(t *testing.T) {
	out, _, _ := runScript(t, "fakeagent", "work x\n")
	assert.Contains(t, out, `want a non-negative number of seconds, got "x"`)
}

func TestRun_BellAndTitleEmitControlSequences(t *testing.T) {
	out, _, _ := runScript(t, "fakeagent", "bell\ntitle hello world\n")
	assert.Contains(t, out, "\a")
	assert.Contains(t, out, "\x1b]2;hello world\x07")
}

func TestRun_EditAppendsLines(t *testing.T) {
	out, _, a := runScript(t, "fakeagent", "edit\nedit\n")
	data, err := os.ReadFile(filepath.Join(a.dir, "fakeagent.txt"))
	require.NoError(t, err)
	assert.Equal(t, "edit 1\nedit 2\n", string(data))
	assert.Contains(t, out, "edited ")
}

func TestRun_CommitStagesThenCommits(t *testing.T) {
	var calls [][]string
	var out bytes.Buffer
	a := newFakeAgent(personaFor("fakeagent"), strings.NewReader("commit\n"), &out, t.TempDir())
	a.git = func(_ string, args ...string) error {
		calls = append(calls, args)
		return nil
	}
	a.run()
	assert.Equal(t, [][]string{{"add", "-A"}, {"commit", "--no-verify", "-m", "fakeagent: commit"}}, calls)
	assert.Contains(t, out.String(), "committed")
}

func TestRun_ExitCodes(t *testing.T) {
	_, code, _ := runScript(t, "fakeagent", "crash\nexit\n")
	assert.Equal(t, 1, code, "crash exits 1 before reading further")
	_, code, _ = runScript(t, "fakeagent", "exit\n")
	assert.Equal(t, 0, code)
	_, code, _ = runScript(t, "fakeagent", "")
	assert.Equal(t, 0, code, "EOF exits cleanly")
}

func TestRun_UnknownCommand(t *testing.T) {
	out, _, _ := runScript(t, "fakeagent", "dance\n")
	assert.Contains(t, out, `unknown command "dance"`)
}

// A message the mod receives runs as a prompt once the agent is idle; one
// that arrives while the agent waits at a prompt neither answers it nor
// starts, as Claude queues a submitted prompt. A message offered again (its
// queued report lost) is reported queued again but never runs twice, and the
// mod stops polling when the agent exits.
func TestMod_AMessageRunsAsAPromptButNeverAnswersOne(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "fam")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "m.sock")
	ln, err := net.Listen("unix", sock)
	require.NoError(t, err)
	var (
		mu     sync.Mutex
		events []map[string]any
		left   atomic.Int32 // held polls the agent abandoned
	)
	offer := make(chan string) // an inbox body for the held poll
	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("POST /v%d/hello", claudemod.Version), func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc(fmt.Sprintf("GET /v%d/inbox", claudemod.Version), func(w http.ResponseWriter, r *http.Request) {
		select {
		case body := <-offer:
			w.Header().Set("content-type", "application/json")
			_, _ = io.WriteString(w, body)
		case <-r.Context().Done():
			left.Add(1)
		}
	})
	mux.HandleFunc(fmt.Sprintf("POST /v%d/events", claudemod.Version), func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Launch string           `json:"launch"`
			Events []map[string]any `json:"events"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		assert.Equal(t, "L1", body.Launch)
		mu.Lock()
		events = append(events, body.Events...)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	kinds := func() []string {
		mu.Lock()
		defer mu.Unlock()
		var k []string
		for _, e := range events {
			k = append(k, e["kind"].(string)+" "+e["id"].(string))
		}
		return k
	}
	eventually := func(what string, ok func() bool) {
		t.Helper()
		require.Eventually(t, ok, 5*time.Second, 10*time.Millisecond, "%s; events %v", what, kinds())
	}

	stdin, feed := io.Pipe()
	out := &lockedBuffer{}
	a := newFakeAgent(personaFor("claude"), stdin, out, t.TempDir())
	a.mod = newModClient(func(k string) string {
		return map[string]string{"LOOM_MOD_SOCKET": sock, "LOOM_LAUNCH": "L1"}[k]
	})
	require.NotNil(t, a.mod)
	done := make(chan int, 1)
	go func() { done <- a.run() }()

	_, _ = io.WriteString(feed, "ask\n")
	eventually("the prompt", func() bool { return strings.Contains(out.String(), a.p.pendingPrompt) })
	offer <- `{"messages":[{"id":"m1","text":"work 0"}]}`
	eventually("m1 queued", func() bool { return slices.Equal(kinds(), []string{"queued m1"}) })
	time.Sleep(200 * time.Millisecond)
	assert.NotContains(t, out.String(), "answered:", "a queued message never answers a prompt")
	assert.Equal(t, []string{"queued m1"}, kinds(), "nor does it start while the agent waits")

	_, _ = io.WriteString(feed, "y\n")
	eventually("m1 run", func() bool { return strings.Contains(out.String(), "work done") })
	assert.Contains(t, out.String(), `answered: "y"`)
	eventually("m1 complete", func() bool {
		return slices.Equal(kinds(), []string{"queued m1", "started m1", "complete m1"})
	})
	mu.Lock()
	assert.Equal(t, "fakeagent finished: work 0", events[2]["text"])
	assert.Equal(t, "answer", events[2]["reason"])
	assert.NotEmpty(t, events[1]["turnId"])
	assert.Equal(t, events[1]["turnId"], events[2]["turnId"], "started and complete name one turn")
	mu.Unlock()

	// Idle now: a message runs with no keystroke, and one offered again is
	// only reported queued again.
	offer <- `{"messages":[{"id":"m2","text":"bell"}]}`
	eventually("m2 complete", func() bool { return len(kinds()) == 6 })
	assert.Equal(t, []string{"queued m2", "started m2", "complete m2"}, kinds()[3:])
	offer <- `{"messages":[{"id":"m2","text":"bell"}]}`
	eventually("m2 queued again", func() bool { return len(kinds()) == 7 })
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, []string{"queued m2"}, kinds()[6:], "a message offered again never runs twice")

	require.Zero(t, left.Load())
	_, _ = io.WriteString(feed, "exit\n")
	assert.Equal(t, 0, <-done)
	eventually("the mod to stop with the agent", func() bool { return left.Load() == 1 })
}

// The mod retries on the module's schedule (register.ts's backoff): 1 s
// after a success, doubling to a 5 s cap.
func TestMod_BackoffIsTheModulesSchedule(t *testing.T) {
	var got []time.Duration
	for d, i := time.Duration(0), 0; i < 5; i++ {
		d = backoff(d)
		got = append(got, d)
	}
	assert.Equal(t, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second, 5 * time.Second}, got)
}

// The mod names its poller in its hello and every poll, and stops polling
// for good when the daemon answers 409: a newer mod polls for its launch.
func TestMod_StopsWhenANewerModSupersedesIt(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "fam")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "m.sock")
	ln, err := net.Listen("unix", sock)
	require.NoError(t, err)
	var (
		mu      sync.Mutex
		greeted string   // the hello's poller
		polled  []string // each poll's poller
	)
	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("POST /v%d/hello", claudemod.Version), func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Poller string `json:"poller"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		greeted = body.Poller
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc(fmt.Sprintf("GET /v%d/inbox", claudemod.Version), func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		polled = append(polled, r.URL.Query().Get("poller"))
		mu.Unlock()
		w.WriteHeader(http.StatusConflict)
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	c := newModClient(func(k string) string {
		return map[string]string{"LOOM_MOD_SOCKET": sock, "LOOM_LAUNCH": "L1"}[k]
	})
	require.NotNil(t, c)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})

	go func() { c.run(ctx); close(done) }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the mod kept polling after a 409")
	}
	mu.Lock()
	defer mu.Unlock()
	assert.NotEmpty(t, greeted, "its hello names its poller")
	assert.Equal(t, []string{greeted}, polled, "one poll, naming the same poller")
}

// The mod names its load time in its hello, and stops for good when the
// hello itself hears 409: a mod loaded later holds the launch.
func TestMod_StopsWhenItsHelloIsSuperseded(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "fam")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "m.sock")
	ln, err := net.Listen("unix", sock)
	require.NoError(t, err)
	var (
		mu     sync.Mutex
		loaded []int64 // each hello's load time
		polls  int
	)
	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("POST /v%d/hello", claudemod.Version), func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Loaded int64 `json:"loaded"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		loaded = append(loaded, body.Loaded)
		mu.Unlock()
		w.WriteHeader(http.StatusConflict)
	})
	mux.HandleFunc(fmt.Sprintf("GET /v%d/inbox", claudemod.Version), func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		polls++
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	c := newModClient(func(k string) string {
		return map[string]string{"LOOM_MOD_SOCKET": sock, "LOOM_LAUNCH": "L1"}[k]
	})
	require.NotNil(t, c)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})

	go func() { c.run(ctx); close(done) }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the mod kept going after its hello heard 409")
	}
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, loaded, 1, "one hello")
	assert.Positive(t, loaded[0], "naming its load time")
	assert.Zero(t, polls)
}
