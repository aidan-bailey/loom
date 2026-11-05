package modserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session/claudemod"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/aidan-bailey/loom/session/transcript"
	"github.com/stretchr/testify/require"
)

// claudeConfigDir is the developer's CLAUDE_CONFIG_DIR, read as the package
// initializes, before TestMain points it at an empty dir: the real Claude
// runs logged in as the developer.
var claudeConfigDir = os.Getenv("CLAUDE_CONFIG_DIR")

// queueHost is a ModHost for launch L1 whose messages the test queues.
type queueHost struct {
	mu       sync.Mutex
	hello    bool
	commands []core.ModCommand // the hello's list
	msgs     []core.ModMessage
	wake     chan struct{}
	events   []core.ModEvent
	session  string // the hello's Claude session ID
	// asks are the asks posted, answers what the test hands each by ask ID,
	// askWake ends the held answer polls when an answer arrives, and closes
	// the closes reported, "<ask> <how>".
	asks    []core.ModAsk
	answers map[string]json.RawMessage
	askWake chan struct{}
	closes  []string
	// calls are the Bash calls the mod reported starting.
	calls []core.ModCall
}

// ModCall records each call reported starting; an ended report is not kept.
func (h *queueHost) ModCall(launch, _ string, c core.ModCall, ended bool) error {
	if launch != "L1" {
		return core.ErrModUnknownLaunch
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if !ended {
		h.calls = append(h.calls, c)
	}
	return nil
}

// callWith is the first call reported starting whose command holds s.
func (h *queueHost) callWith(s string) (core.ModCall, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.calls {
		if strings.Contains(c.Command, s) {
			return c, true
		}
	}
	return core.ModCall{}, false
}

func (h *queueHost) ModAsk(launch, _ string, ask core.ModAsk) error {
	if launch != "L1" {
		return core.ErrModUnknownLaunch
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, a := range h.asks {
		if a.Ask == ask.Ask {
			return nil
		}
	}
	h.asks = append(h.asks, ask)
	return nil
}

func (h *queueHost) ModAnswer(launch, _, ask string) (json.RawMessage, <-chan struct{}, error) {
	if launch != "L1" {
		return nil, nil, core.ErrModUnknownLaunch
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if payload, ok := h.answers[ask]; ok {
		delete(h.answers, ask)
		return payload, nil, nil
	}
	if h.askWake == nil {
		h.askWake = make(chan struct{})
	}
	return nil, h.askWake, nil
}

func (h *queueHost) ModAskClosed(launch, _, ask, how string) error {
	if launch != "L1" {
		return core.ErrModUnknownLaunch
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closes = append(h.closes, ask+" "+how)
	return nil
}

// answer hands the mod payload as the answer to ask.
func (h *queueHost) answer(ask string, payload string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.answers == nil {
		h.answers = map[string]json.RawMessage{}
	}
	h.answers[ask] = json.RawMessage(payload)
	if h.askWake != nil {
		close(h.askWake)
		h.askWake = nil
	}
}

// askOf is the first posted ask of kind whose ID is not in skip.
func (h *queueHost) askOf(kind string, skip map[string]bool) (core.ModAsk, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, a := range h.asks {
		if a.Kind == kind && !skip[a.Ask] {
			return a, true
		}
	}
	return core.ModAsk{}, false
}

// closed reports whether a close "<ask> <how>" was reported.
func (h *queueHost) closed(ask, how string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.closes {
		if c == ask+" "+how {
			return true
		}
	}
	return false
}

func (h *queueHost) ModHello(launch string, hello core.ModHello) error {
	if launch != "L1" {
		return core.ErrModUnknownLaunch
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.hello = true
	h.commands = hello.Commands
	h.session = hello.Session
	return nil
}

// listed reports whether the hello listed the command name from source.
func (h *queueHost) listed(name, source string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.commands {
		if c.Name == name && c.Source == source {
			return true
		}
	}
	return false
}

func (h *queueHost) ModInbox(launch, _ string) ([]core.ModMessage, <-chan struct{}, error) {
	if launch != "L1" {
		return nil, nil, core.ErrModUnknownLaunch
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.wake != nil {
		close(h.wake)
	}
	h.wake = make(chan struct{})
	return append([]core.ModMessage(nil), h.msgs...), h.wake, nil
}

func (h *queueHost) ModEvents(launch string, events []core.ModEvent) error {
	if launch != "L1" {
		return core.ErrModUnknownLaunch
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ev := range events {
		h.events = append(h.events, ev)
		if ev.Kind != "queued" {
			continue
		}
		for i, m := range h.msgs {
			if m.ID == ev.ID {
				h.msgs = append(h.msgs[:i], h.msgs[i+1:]...)
				break
			}
		}
	}
	return nil
}

func (*queueHost) ModsServing() {}

func (h *queueHost) helloed() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.hello
}

func (h *queueHost) queue(id, text string) { h.add(core.ModMessage{ID: id, Text: text}) }

// queueAsUser queues text as the person's own words, as the model hands
// the phone's replies to the mod.
func (h *queueHost) queueAsUser(id, text string) {
	h.add(core.ModMessage{ID: id, Text: text, AsUser: true})
}

// sessionID is the Claude session the hello named.
func (h *queueHost) sessionID() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.session
}

// queueCommand queues the slash command name with args, as the model hands
// one to the mod.
func (h *queueHost) queueCommand(id, name, args string) {
	h.add(core.ModMessage{ID: id, Text: "/" + name + " " + args, Command: name, Args: args})
}

func (h *queueHost) add(m core.ModMessage) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.msgs = append(h.msgs, m)
	if h.wake != nil {
		close(h.wake)
		h.wake = nil
	}
}

// event is the first event of kind for message id.
func (h *queueHost) event(kind, id string) (core.ModEvent, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ev := range h.events {
		if ev.Kind == kind && ev.ID == id {
			return ev, true
		}
	}
	return core.ModEvent{}, false
}

func (h *queueHost) said() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return fmt.Sprintf("hello %v, events %+v", h.hello, h.events)
}

// probe is a real Claude (haiku, default permission mode) with the embedded
// mod, in a pane of a private tmux server, served by its queueHost: work is
// its folder, and the rest drive the pane.
type probe struct {
	work     string
	screen   func() string
	waitFor  func(what string, d time.Duration, ok func() bool)
	press    func(keys ...string)
	typeLine func(text string)
}

// startClaude starts the probe once its mod has said hello to host. It skips
// the test without LOOM_TEST_REAL_CLAUDE=1, tmux or claude.
func startClaude(t *testing.T, host *queueHost) probe {
	t.Helper()
	if os.Getenv("LOOM_TEST_REAL_CLAUDE") != "1" {
		t.Skip("set LOOM_TEST_REAL_CLAUDE=1 to run against real Claude (costs money)")
	}
	for _, bin := range []string{"tmux", "claude"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
	root := t.TempDir()
	mod := filepath.Join(root, "mod")
	require.NoError(t, claudemod.WriteTo(mod))
	work := filepath.Join(root, "work")
	require.NoError(t, os.MkdirAll(work, 0o700))
	socket := shortSocket(t)
	s, err := Listen(socket, host, 5*time.Second)
	require.NoError(t, err)
	t.Cleanup(s.Close)

	ctx := context.Background()
	sock := fmt.Sprintf("loommod-%d", os.Getpid())
	tm := func(args ...string) string {
		t.Helper()
		out, err := tmux.CommandOnSocket(ctx, sock, args...).CombinedOutput()
		require.NoError(t, err, string(out))
		return string(out)
	}
	var serverPath string // the server's socket, which kill-server leaves behind
	t.Cleanup(func() {
		_ = tmux.CommandOnSocket(ctx, sock, "kill-server").Run()
		if serverPath != "" {
			_ = os.Remove(serverPath)
		}
	})
	target := tmux.PaneTarget("probe")
	screen := func() string { return tm("capture-pane", "-p", "-t", target) }
	waitFor := func(what string, d time.Duration, ok func() bool) {
		t.Helper()
		for deadline := time.Now().Add(d); !ok(); time.Sleep(250 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s; %s; screen:\n%s", what, host.said(), screen())
			}
		}
	}
	// Text goes in as loom types it (TypeText: a paste buffer), keys by
	// name; both reach the private server through LOOM_TMUX_SOCKET.
	t.Setenv(tmux.EnvTmuxSocket, sock)
	// A server UseServer pinned in this binary would outrank it.
	cmdArgs := strings.Join(tmux.Command(ctx).Args, " ")
	require.Contains(t, cmdArgs, " -L "+sock, "the pane's helpers reach the private server")
	require.NotContains(t, cmdArgs, " -S ", "and no pinned one")
	pane := tmux.NewSessionNamed("probe", "")
	press := func(keys ...string) {
		t.Helper()
		require.NoError(t, pane.PressKeys(keys...))
	}
	typeLine := func(text string) {
		t.Helper()
		require.NoError(t, pane.TypeText(text))
		time.Sleep(300 * time.Millisecond)
		press("Enter")
	}

	// The server, and so the probe's shell, starts from a clean environment
	// (the 2026-10-14 probe recipe): nothing of this test binary's, whose
	// TestMain isolates Claude's dirs, or of a Claude the test runs inside,
	// which would hand the probe its session. TMUX_TMPDIR stays, or the
	// server's socket would land where tm's clients don't look.
	env := []string{"TERM=xterm-256color"}
	for _, k := range []string{"HOME", "PATH", "USER", "SHELL", "LANG", "LC_ALL", "LC_CTYPE", "XDG_RUNTIME_DIR", "TMUX_TMPDIR"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	if claudeConfigDir != "" {
		env = append(env, "CLAUDE_CONFIG_DIR="+claudeConfigDir)
	}
	start := tmux.CommandOnSocket(ctx, sock, "-f", "/dev/null", "new-session", "-d", "-s", "probe", "-x", "160", "-y", "45", "-c", work)
	start.Env = env
	out, err := start.CombinedOutput()
	require.NoError(t, err, string(out))
	serverPath = strings.TrimSpace(tm("display-message", "-p", "-t", target, "#{socket_path}"))
	typeLine(fmt.Sprintf("%s=%s %s=L1 claude --model haiku --permission-mode default --plugin-dir '%s'",
		claudemod.EnvSocket, socket, claudemod.EnvLaunch, mod))
	// Claude asks whether to trust a new folder, with "No, exit" selected
	// for one under a temp dir, and drops a key pressed as the dialog
	// appears: press Down until a capture shows the cursor on Yes, and Enter
	// only then, as loom's launch watch answers it.
	const trustYes = "Yes, I trust this folder"
	waitFor("the trust dialog or the mod's hello", 60*time.Second, func() bool {
		return host.helloed() || strings.Contains(screen(), trustYes)
	})
	if !host.helloed() {
		waitFor("the trust dialog's cursor on Yes", 30*time.Second, func() bool {
			if strings.Contains(screen(), "❯ "+trustYes) {
				return true
			}
			press("Down")
			time.Sleep(500 * time.Millisecond)
			return strings.Contains(screen(), "❯ "+trustYes)
		})
		press("Enter")
	}
	waitFor("the mod's hello", 60*time.Second, host.helloed)
	return probe{work: work, screen: screen, waitFor: waitFor, press: press, typeLine: typeLine}
}

// TestRealClaude_TheModCarriesAMessage launches a real Claude (haiku) with the
// embedded mod on a private tmux server and serves it from queueHost: hello;
// a message's queued, started and complete (complete carrying Claude's
// answer); and a message delivered while a permission dialog is open, which
// leaves the dialog alone and runs after it; then the hello's list of
// Claude's commands, a command message that runs (/model haiku) and one that
// fails by name. It spends a few cents and leaves entries in the developer's
// Claude config dir (projects/, the trust entry), so it runs only with
// LOOM_TEST_REAL_CLAUDE=1. Its waits add up to about 9.5 minutes at worst,
// inside go test's default 10-minute timeout.
func TestRealClaude_TheModCarriesAMessage(t *testing.T) {
	host := &queueHost{}
	p := startClaude(t, host)
	work, screen, waitFor, press, typeLine := p.work, p.screen, p.waitFor, p.press, p.typeLine

	host.queueAsUser("m1", "Reply with exactly the word PONG.")
	waitFor("m1's answer", 90*time.Second, func() bool {
		ev, ok := host.event("complete", "m1")
		return ok && strings.Contains(ev.Text, "PONG")
	})
	_, queued := host.event("queued", "m1")
	require.True(t, queued, "m1 was reported queued")
	started, ok := host.event("started", "m1")
	require.True(t, ok, "m1's turn was reported started")
	complete, _ := host.event("complete", "m1")
	require.NotEmpty(t, started.TurnID)
	require.Equal(t, started.TurnID, complete.TurnID, "m1 started and completed in one turn")

	typeLine("Run this exact bash command: mkdir made-by-test. Then reply DONE.")
	waitFor("the permission dialog", 90*time.Second, func() bool { return strings.Contains(screen(), "Do you want to proceed?") })
	host.queue("m2", "Reply with exactly the word QUEUED.")
	waitFor("m2 queued", 30*time.Second, func() bool { _, ok := host.event("queued", "m2"); return ok })
	time.Sleep(time.Second)
	still := screen()
	require.Contains(t, still, "Do you want to proceed?", "the queued message left the dialog open")
	require.Contains(t, still, "❯ 1. Yes", "with its choice where it was")
	_, early := host.event("started", "m2")
	require.False(t, early, "m2 waits for the dialog's turn to end")
	require.NoDirExists(t, filepath.Join(work, "made-by-test"), "nothing answered the dialog")
	press("Enter")
	waitFor("m2's answer", 90*time.Second, func() bool {
		ev, ok := host.event("complete", "m2")
		return ok && strings.Contains(ev.Text, "QUEUED")
	})
	require.DirExists(t, filepath.Join(work, "made-by-test"), "the dialog's own answer ran the command")

	// The hello listed Claude's commands, and a command message runs
	// through $.command.run, reporting its run, or failing by name.
	require.True(t, host.listed("clear", "builtin"), "the hello lists Claude's commands with their sources")
	host.queueCommand("c1", "model", "haiku")
	waitFor("c1's run", 60*time.Second, func() bool { _, ok := host.event("complete", "c1"); return ok })
	_, queued = host.event("queued", "c1")
	require.True(t, queued, "c1 was reported queued")
	host.queueCommand("c2", "nosuchcmd", "")
	waitFor("c2's failure", 30*time.Second, func() bool { _, ok := host.event("failed", "c2"); return ok })
	failed, _ := host.event("failed", "c2")
	require.Contains(t, failed.Error, "no command named /nosuchcmd")

	// The transcript reads both messages back under their origins: m1 as
	// the person's own words, as the phone's replies go, and m2 under the
	// plugin's name, as a CLI send goes. No census transcript held a
	// plugin's prompt, so this pins the shape the conversation screen
	// labels.
	config := claudeConfigDir
	if config == "" {
		home, err := os.UserHomeDir()
		require.NoError(t, err)
		config = filepath.Join(home, ".claude")
	}
	paths, err := filepath.Glob(filepath.Join(config, "projects", "*", host.sessionID()+".jsonl"))
	require.NoError(t, err)
	require.Len(t, paths, 1, "the transcript of session %s", host.sessionID())
	page, err := transcript.Read(paths[0], host.sessionID(), transcript.Query{})
	require.NoError(t, err)
	from := map[string]string{}
	for _, turn := range page.Turns {
		switch {
		case strings.Contains(turn.Text, "PONG"):
			from["m1"] = turn.From + ":" + turn.Name
		case strings.Contains(turn.Text, "QUEUED"):
			from["m2"] = turn.From + ":" + turn.Name
		}
	}
	require.Equal(t, map[string]string{"m1": transcript.FromYou + ":", "m2": transcript.FromPlugin + ":loom"}, from)

	t.Log(host.said())
}

// TestRealClaude_AnswersAsksThroughTheDaemon launches a real Claude (haiku)
// with the embedded mod and answers its dialogs from queueHost as the phone
// does through the daemon: a question answered from loom (the dialog closes
// and Claude reads the answer), a Bash permission allowed from loom (the
// command runs), then one of each answered in the terminal, which the mod
// reports closed (a permission when its call settles, a question when its
// dialog's answer comes back). It spends a few cents and leaves entries in
// the developer's Claude config dir, so it runs only with
// LOOM_TEST_REAL_CLAUDE=1; its waits stay inside go test's default
// 10-minute timeout.
func TestRealClaude_AnswersAsksThroughTheDaemon(t *testing.T) {
	host := &queueHost{}
	p := startClaude(t, host)
	seen := map[string]bool{}
	nextAsk := func(kind string) core.ModAsk {
		t.Helper()
		var a core.ModAsk
		p.waitFor("a "+kind+" ask", 90*time.Second, func() bool {
			var ok bool
			a, ok = host.askOf(kind, seen)
			return ok
		})
		seen[a.Ask] = true
		return a
	}
	lunch := "Use the AskUserQuestion tool once, with one question: header Lunch, what I want for lunch, options Soup and Salad, single select. Then reply with exactly the answer you were given."

	// A question answered from loom: the dialog closes, Claude reads it.
	p.typeLine(lunch)
	q := nextAsk(core.AskKindQuestion)
	var in struct {
		Questions []struct {
			Question string `json:"question"`
		} `json:"questions"`
	}
	require.NoError(t, json.Unmarshal(q.Input, &in))
	require.Len(t, in.Questions, 1)
	var raw struct {
		Questions json.RawMessage `json:"questions"`
	}
	require.NoError(t, json.Unmarshal(q.Input, &raw))
	host.answer(q.Ask, fmt.Sprintf(`{"result":{"questions":%s,"answers":{%q:"Salad"},"annotations":{}}}`, raw.Questions, in.Questions[0].Question))
	// The transcript shows a question's answer as "→ <answer>" once the
	// dialog closed with it ("Salad" alone is in the prompt too).
	p.waitFor("the dialog's answer", 90*time.Second, func() bool {
		sc := p.screen()
		return strings.Contains(sc, "→ Salad") && !strings.Contains(sc, "Chat about this")
	})

	// A permission allowed from loom: the command runs.
	p.typeLine("Run this exact bash command: mkdir made-by-loom. Then reply DONE.")
	perm := nextAsk(core.AskKindPermission)
	require.Equal(t, "Bash", perm.Tool)
	require.NotEmpty(t, perm.ToolUseID, "the ask found its call")
	host.answer(perm.Ask, `{"decision":{"behavior":"allow"}}`)
	p.waitFor("made-by-loom", 60*time.Second, func() bool {
		_, err := os.Stat(filepath.Join(p.work, "made-by-loom"))
		return err == nil
	})

	// A permission answered in the terminal: the mod closes it once its call
	// settles.
	p.typeLine("Run this exact bash command: mkdir made-at-desk. Then reply DONE.")
	desk := nextAsk(core.AskKindPermission)
	p.waitFor("the permission dialog", 30*time.Second, func() bool { return strings.Contains(p.screen(), "Do you want to proceed?") })
	p.press("Enter")
	p.waitFor("the permission's close", 60*time.Second, func() bool { return host.closed(desk.Ask, "settled") })
	require.DirExists(t, filepath.Join(p.work, "made-at-desk"))

	// A question answered in the terminal: the mod closes it at once.
	p.typeLine(lunch)
	q2 := nextAsk(core.AskKindQuestion)
	p.waitFor("the question dialog", 30*time.Second, func() bool { return strings.Contains(p.screen(), "Chat about this") })
	p.press("Enter")
	p.waitFor("the question's close", 60*time.Second, func() bool { return host.closed(q2.Ask, "elsewhere") })

	t.Log(host.said())
}
