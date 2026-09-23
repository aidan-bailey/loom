package main

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// launchOptions are the Claude flags fakeagent honours. Every other flag
// is ignored, as before.
type launchOptions struct {
	settings string // --settings: loom's hooks settings file
	resume   string // --resume: the conversation to continue
	agents   bool   // `claude agents …`: loom's roster query
}

func parseArgs(args []string) launchOptions {
	var o launchOptions
	if len(args) > 0 && args[0] == "agents" {
		o.agents = true
		return o
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--settings":
			if i+1 < len(args) {
				o.settings = args[i+1]
				i++
			}
		case "--resume":
			if i+1 < len(args) {
				o.resume = args[i+1]
				i++
			}
		}
	}
	return o
}

// hookEmitter plays the hook side of Claude Code: it runs the commands the
// --settings file registers for each event, with a payload shaped like
// Claude's on stdin (see session/hooks/testdata/probe-2.1.280). Its
// methods are safe on a nil emitter, which a launch without --settings
// gets, and then do nothing.
type hookEmitter struct {
	commands   map[string][]string // event name → shell commands
	sessionID  string
	transcript string
	cwd        string
	resumed    bool
	// run executes one hook command; tests replace it.
	run func(command string, payload []byte) error
}

type settingsFile struct {
	Hooks map[string][]struct {
		Hooks []struct {
			Type    string `json:"type"`
			Command string `json:"command"`
		} `json:"hooks"`
	} `json:"hooks"`
}

// newHookEmitter loads the hook commands from o.settings and names the
// conversation: o.resume's ID, or a new one. It creates an empty
// transcript, because loom only resumes a conversation whose transcript
// exists. Returns nil, nil when o has no --settings.
func newHookEmitter(o launchOptions, cwd string) (*hookEmitter, error) {
	if o.settings == "" {
		return nil, nil
	}
	data, err := os.ReadFile(o.settings)
	if err != nil {
		return nil, fmt.Errorf("read --settings: %w", err)
	}
	var s settingsFile
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse --settings: %w", err)
	}
	e := &hookEmitter{commands: map[string][]string{}, cwd: cwd, resumed: o.resume != "", run: runHookCommand}
	for event, matchers := range s.Hooks {
		for _, m := range matchers {
			for _, h := range m.Hooks {
				if h.Type == "command" {
					e.commands[event] = append(e.commands[event], h.Command)
				}
			}
		}
	}
	e.sessionID = o.resume
	if e.sessionID == "" {
		if e.sessionID, err = newSessionID(); err != nil {
			return nil, err
		}
	}
	dir := filepath.Join(os.TempDir(), "fakeagent-transcripts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("transcript dir: %w", err)
	}
	e.transcript = filepath.Join(dir, e.sessionID+".jsonl")
	f, err := os.OpenFile(e.transcript, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("transcript: %w", err)
	}
	_ = f.Close()
	return e, nil
}

func runHookCommand(command string, payload []byte) error {
	cmd := exec.Command("sh", "-c", command)
	cmd.Stdin = bytes.NewReader(payload)
	return cmd.Run()
}

// newSessionID returns a random version-4 UUID, the shape of Claude's
// session IDs (loom only resumes IDs of that shape).
func newSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("session id: %w", err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// sessionStart fires SessionStart, as startup or resume.
func (e *hookEmitter) sessionStart() {
	if e == nil {
		return
	}
	source := "startup"
	if e.resumed {
		source = "resume"
	}
	e.emit("SessionStart", map[string]any{"source": source})
}

// emit runs event's hooks with the fields every Claude payload carries
// plus extra. Errors are ignored, as Claude ignores a failing hook.
func (e *hookEmitter) emit(event string, extra map[string]any) {
	if e == nil {
		return
	}
	payload := map[string]any{
		"hook_event_name": event,
		"session_id":      e.sessionID,
		"transcript_path": e.transcript,
		"cwd":             e.cwd,
	}
	for k, v := range extra {
		payload[k] = v
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	for _, c := range e.commands[event] {
		_ = e.run(c, data)
	}
}
