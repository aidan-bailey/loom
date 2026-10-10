package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session/agent"
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
