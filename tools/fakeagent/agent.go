package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/session/agent"
)

const (
	genericPendingPrompt = "Proceed? (y/n)"
	genericTrustPrompt   = "Trust this folder? (y/n)"
	usage                = "commands: work N | ask | trust | bell | title TEXT | edit | commit | crash | exit"
	// clearScreen wipes the visible screen after a prompt is answered so
	// the pattern stops matching loom's scan, the way a real agent redraws.
	clearScreen = "\x1b[2J\x1b[H"
)

// persona is the agent fakeagent impersonates. It is resolved through
// loom's own adapter registry, so prompt texts cannot drift from what loom
// scans for.
type persona struct {
	name          string
	pendingPrompt string
	trustPrompt   string
}

func personaFor(argv0 string) persona {
	ad := agent.DefaultRegistry().Lookup(argv0)
	p := persona{name: ad.Name(), pendingPrompt: ad.PendingPromptPattern(), trustPrompt: genericTrustPrompt}
	if p.pendingPrompt == "" {
		p.pendingPrompt = genericPendingPrompt
	}
	if pats := ad.TrustPromptPatterns(); len(pats) > 0 {
		p.trustPrompt = pats[0]
	}
	return p
}

type fakeAgent struct {
	p     persona
	in    *bufio.Scanner
	out   io.Writer
	dir   string
	sleep func(time.Duration)
	git   func(dir string, args ...string) error
	edits int
	// hooks fires loom's hooks the way Claude does; nil without --settings.
	hooks *hookEmitter
}

func newFakeAgent(p persona, in io.Reader, out io.Writer, dir string) *fakeAgent {
	return &fakeAgent{p: p, in: bufio.NewScanner(in), out: out, dir: dir, sleep: time.Sleep, git: runGit}
}

func runGit(dir string, args ...string) error {
	cmd := internalexec.GitCommand(context.Background(), dir, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, out)
	}
	return nil
}

// run executes stdin commands until exit, crash, or EOF and returns the
// process exit code.
func (a *fakeAgent) run() int {
	a.hooks.sessionStart()
	fmt.Fprintf(a.out, "fakeagent: %s persona\n%s\n", a.p.name, usage)
	for {
		fmt.Fprint(a.out, "> ")
		if !a.in.Scan() {
			a.hooks.emit("SessionEnd", map[string]any{"reason": "other"})
			return 0
		}
		if code, done := a.exec(strings.TrimSpace(a.in.Text())); done {
			return code
		}
	}
}

func (a *fakeAgent) exec(line string) (code int, done bool) {
	cmd, arg, _ := strings.Cut(line, " ")
	switch cmd {
	case "", "crash":
	case "exit":
		a.hooks.emit("SessionEnd", map[string]any{"reason": "prompt_input_exit"})
	default:
		// Every other line is a prompt: Claude fires UserPromptSubmit when
		// it arrives and Stop when the turn ends. The Stop's message is
		// never printed, so a card showing it proves it came from a hook.
		a.hooks.emit("UserPromptSubmit", map[string]any{"prompt": line})
		defer a.hooks.emit("Stop", map[string]any{
			"last_assistant_message": "fakeagent finished: " + line,
			"background_tasks":       []any{},
			"stop_hook_active":       false,
		})
	}
	switch cmd {
	case "":
	case "work":
		a.work(arg)
	case "ask":
		a.hooks.emit("PermissionRequest", map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": "true"}})
		a.await(a.p.pendingPrompt, "answered")
	case "trust":
		a.await(a.p.trustPrompt, "trusted")
	case "bell":
		fmt.Fprint(a.out, "\a")
	case "title":
		fmt.Fprintf(a.out, "\x1b]2;%s\x07", arg)
	case "edit":
		a.edit()
	case "commit":
		a.commit()
	case "crash":
		fmt.Fprintln(a.out, "fakeagent: crashing")
		return 1, true
	case "exit":
		return 0, true
	default:
		fmt.Fprintf(a.out, "unknown command %q; %s\n", cmd, usage)
	}
	return 0, false
}

func (a *fakeAgent) work(arg string) {
	secs, err := strconv.Atoi(arg)
	if err != nil || secs < 0 {
		fmt.Fprintf(a.out, "work: want a non-negative number of seconds, got %q\n", arg)
		return
	}
	ticks := secs * 10
	for i := 1; i <= ticks; i++ {
		fmt.Fprintf(a.out, "working %d/%d\n", i, ticks)
		a.sleep(100 * time.Millisecond)
	}
	fmt.Fprintln(a.out, "work done")
}

func (a *fakeAgent) await(prompt, verb string) {
	fmt.Fprintln(a.out, prompt)
	if !a.in.Scan() {
		return
	}
	fmt.Fprintf(a.out, "%s%s: %q\n", clearScreen, verb, a.in.Text())
}

func (a *fakeAgent) edit() {
	a.edits++
	path := filepath.Join(a.dir, "fakeagent.txt")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintf(a.out, "edit: %v\n", err)
		return
	}
	defer func() { _ = f.Close() }()
	if _, err := fmt.Fprintf(f, "edit %d\n", a.edits); err != nil {
		fmt.Fprintf(a.out, "edit: %v\n", err)
		return
	}
	fmt.Fprintf(a.out, "edited %s\n", path)
}

func (a *fakeAgent) commit() {
	for _, args := range [][]string{{"add", "-A"}, {"commit", "--no-verify", "-m", "fakeagent: commit"}} {
		if err := a.git(a.dir, args...); err != nil {
			fmt.Fprintf(a.out, "commit: %v\n", err)
			return
		}
	}
	fmt.Fprintln(a.out, "committed")
}
