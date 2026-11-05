package advisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClaude writes a claude that saves its arguments, working directory
// and stdin beside itself, then runs body.
func fakeClaude(t *testing.T, body string) (program, dir string) {
	t.Helper()
	dir = t.TempDir()
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" > '" + filepath.Join(dir, "args") + "'\n" +
		"pwd > '" + filepath.Join(dir, "cwd") + "'\n" +
		"cat > '" + filepath.Join(dir, "stdin") + "'\n" + body + "\n"
	program = filepath.Join(dir, "claude")
	require.NoError(t, os.WriteFile(program, []byte(script), 0o755))
	return program, dir
}

func TestRun(t *testing.T) {
	work := t.TempDir()
	call := Call{Model: "opus", Effort: "low", Schema: NoteSchema, Message: []byte(`{"task":"reconcile"}`), Dir: work, Timeout: 5 * time.Second}

	t.Run("an answer", func(t *testing.T) {
		program, dir := fakeClaude(t, `printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"result":"x","num_turns":2,"total_cost_usd":0.0285,`+
			`"usage":{"input_tokens":2,"output_tokens":53,"cache_read_input_tokens":10,"cache_creation_input_tokens":3435},"permission_denials":[{"tool_name":"Bash"}],`+
			`"modelUsage":{"claude-opus-5-5":{}},"structured_output":{"note":"hi"}}'`)
		call.Program = program
		res, err := Run(call)
		require.NoError(t, err)
		assert.JSONEq(t, `{"note":"hi"}`, string(res.Answer))
		u := res.Usage
		assert.Equal(t, Usage{Model: "claude-opus-5-5", DurationMS: u.DurationMS, Turns: 2, InputTokens: 2, OutputTokens: 53,
			CacheReadTokens: 10, CacheWriteTokens: 3435, CostUSD: 0.0285, Denials: 1}, u)
		assert.Positive(t, u.DurationMS, "the call's wall time, by the daemon's clock")
		args, _ := os.ReadFile(filepath.Join(dir, "args"))
		for _, want := range []string{"-p", "--model\nopus", "--effort\nlow", "--tools\nRead,Glob,Grep", "--restricted", "--safe-mode",
			"--strict-mcp-config", "--no-session-persistence", "--permission-mode\ndontAsk", "--output-format\njson", "--json-schema", "--system-prompt"} {
			assert.Contains(t, string(args), want)
		}
		stdin, _ := os.ReadFile(filepath.Join(dir, "stdin"))
		assert.Equal(t, `{"task":"reconcile"}`, string(stdin))
		cwd, _ := os.ReadFile(filepath.Join(dir, "cwd"))
		real, _ := filepath.EvalSymlinks(work)
		assert.Equal(t, real, strings.TrimSpace(string(cwd)))
	})
	t.Run("the system prompt", func(t *testing.T) {
		body := `printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"result":"x","structured_output":{"note":"hi"}}'`
		program, dir := fakeClaude(t, body)
		own := call
		own.Program, own.SystemPrompt = program, "group these notes"
		_, err := Run(own)
		require.NoError(t, err)
		args, _ := os.ReadFile(filepath.Join(dir, "args"))
		assert.Contains(t, string(args), "--system-prompt\ngroup these notes\n", "a caller's own prompt")

		program, dir = fakeClaude(t, body)
		call.Program = program
		_, err = Run(call)
		require.NoError(t, err)
		args, _ = os.ReadFile(filepath.Join(dir, "args"))
		assert.Contains(t, string(args), "--system-prompt\n"+strings.SplitN(SystemPrompt, "\n", 2)[0], "none is the advisor's")
	})
	t.Run("an error result", func(t *testing.T) {
		program, _ := fakeClaude(t, `printf '%s\n' '{"type":"result","subtype":"error_max_turns","is_error":true,"result":"stopped","total_cost_usd":0.5}'`)
		call.Program = program
		res, err := Run(call)
		assert.ErrorContains(t, err, "error_max_turns")
		assert.Equal(t, 0.5, res.Usage.CostUSD, "what a failed call cost is kept")
	})
	t.Run("no structured output", func(t *testing.T) {
		program, _ := fakeClaude(t, `printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"result":"x"}'`)
		call.Program = program
		_, err := Run(call)
		assert.ErrorContains(t, err, "no structured answer")
	})
	t.Run("not JSON", func(t *testing.T) {
		program, _ := fakeClaude(t, `echo hello`)
		call.Program = program
		_, err := Run(call)
		assert.Error(t, err)
	})
	t.Run("a non-zero exit", func(t *testing.T) {
		program, _ := fakeClaude(t, `echo 'not logged in' >&2; exit 1`)
		call.Program = program
		_, err := Run(call)
		assert.ErrorContains(t, err, "not logged in")
	})
	t.Run("a hang", func(t *testing.T) {
		program, _ := fakeClaude(t, `sleep 5`)
		call.Program, call.Timeout = program, 300*time.Millisecond
		start := time.Now()
		_, err := Run(call)
		assert.ErrorContains(t, err, "didn't answer in")
		assert.Less(t, time.Since(start), 4*time.Second)
	})
}
