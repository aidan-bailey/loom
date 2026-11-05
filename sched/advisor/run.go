package advisor

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/account"
)

// DefaultTimeout bounds one call.
const DefaultTimeout = 90 * time.Second

// Call is one advisor call (spec §5.6).
type Call struct {
	Program string   // the Claude CLI (account.Binary takes its first word)
	Env     []string // the account's entries (account.Registry.Env); nil for the default account
	Dir     string   // the job's folder: the working directory, to which --restricted confines Read, Glob and Grep
	Model   string
	Effort  string
	Schema  string // PriorSchema or NoteSchema
	Message []byte // the request, as JSON, on stdin
	Timeout time.Duration
}

// args are the CLI's arguments for c: headless, the model and effort, the
// advisor's system prompt, read-only tools confined to the working
// directory, no settings, hooks, plugins, MCP servers or CLAUDE.md files,
// no transcript, no permission prompts, and a JSON result under c.Schema.
func (c Call) args() []string {
	return []string{
		"-p", "--model", c.Model, "--effort", c.Effort, "--system-prompt", SystemPrompt,
		"--tools", "Read,Glob,Grep", "--restricted", "--safe-mode", "--strict-mcp-config",
		"--no-session-persistence", "--permission-mode", "dontAsk",
		"--output-format", "json", "--json-schema", c.Schema,
	}
}

// Usage is what one call took (spec §16): its wall time by the daemon's
// clock, and what the CLI's JSON result reports (the model that answered,
// turns, tokens, cost in dollars and tool uses denied). Zero where the CLI
// printed no result.
type Usage struct {
	Model            string  `json:"model,omitempty"`
	DurationMS       int64   `json:"duration_ms"`
	Turns            int     `json:"turns,omitempty"`
	InputTokens      int64   `json:"input_tokens,omitempty"`
	OutputTokens     int64   `json:"output_tokens,omitempty"`
	CacheReadTokens  int64   `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int64   `json:"cache_write_tokens,omitempty"`
	CostUSD          float64 `json:"cost_usd,omitempty"`
	Denials          int     `json:"denials,omitempty"`
}

// Result is a call's structured answer, unchecked, and what it took.
type Result struct {
	Answer json.RawMessage
	Usage  Usage
}

// Run makes call c and returns its structured answer, unchecked (the caller
// parses it: ParsePrior, ParseNote), with what it took, which a failed call
// carries too. The command is built through account.Command, which refuses
// an account dir that is missing.
func Run(c Call) (Result, error) {
	timeout := cmp.Or(c.Timeout, DefaultTimeout)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd, err := account.Command(ctx, c.Program, c.Env, c.args()...)
	if err != nil {
		return Result{}, err
	}
	cmd.Dir = c.Dir
	cmd.Stdin = bytes.NewReader(c.Message)
	cmd.WaitDelay = time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	start := time.Now()
	out, err := cmd.Output()
	res := Result{Usage: usageOf(out)}
	res.Usage.DurationMS = max(1, time.Since(start).Milliseconds())
	if ctx.Err() != nil {
		return res, fmt.Errorf("the agent didn't answer in %s", timeout)
	}
	if err != nil {
		// Claude reports most failures (a logout, a refusal) in its JSON
		// result on stdout, not on stderr.
		detail := lastLine(stderr.String())
		if _, perr := structuredOutput(out); detail == "" && perr != nil && len(bytes.TrimSpace(out)) > 0 {
			detail = ": " + perr.Error()
		}
		return res, fmt.Errorf("the agent's claude failed: %w%s", err, detail)
	}
	res.Answer, err = structuredOutput(out)
	return res, err
}

// usageOf reads what a call took out of the CLI's JSON result; zero for
// output that isn't one.
func usageOf(out []byte) Usage {
	var r struct {
		NumTurns     int     `json:"num_turns"`
		TotalCostUSD float64 `json:"total_cost_usd"`
		Usage        struct {
			InputTokens              int64 `json:"input_tokens"`
			OutputTokens             int64 `json:"output_tokens"`
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
		} `json:"usage"`
		PermissionDenials []json.RawMessage          `json:"permission_denials"`
		ModelUsage        map[string]json.RawMessage `json:"modelUsage"`
	}
	if json.Unmarshal(bytes.TrimSpace(out), &r) != nil {
		return Usage{}
	}
	models := slices.Sorted(maps.Keys(r.ModelUsage))
	return Usage{
		Model: strings.Join(models, "+"), Turns: r.NumTurns, CostUSD: r.TotalCostUSD, Denials: len(r.PermissionDenials),
		InputTokens: r.Usage.InputTokens, OutputTokens: r.Usage.OutputTokens,
		CacheReadTokens: r.Usage.CacheReadInputTokens, CacheWriteTokens: r.Usage.CacheCreationInputTokens,
	}
}

// structuredOutput reads the answer out of the CLI's JSON result.
func structuredOutput(out []byte) (json.RawMessage, error) {
	var res struct {
		Subtype          string          `json:"subtype"`
		IsError          bool            `json:"is_error"`
		Result           string          `json:"result"`
		StructuredOutput json.RawMessage `json:"structured_output"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out), &res); err != nil {
		return nil, fmt.Errorf("the agent's claude printed no JSON result: %w", err)
	}
	if res.IsError || res.Subtype != "success" {
		return nil, fmt.Errorf("the agent's claude failed (%s): %s", res.Subtype, cut(res.Result, 200))
	}
	if len(res.StructuredOutput) == 0 || string(res.StructuredOutput) == "null" {
		return nil, errors.New("the agent gave no structured answer")
	}
	return res.StructuredOutput, nil
}

// lastLine is ": " and stderr's last line, cut to 200 bytes; "" for none.
func lastLine(stderr string) string {
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	if l := strings.TrimSpace(lines[len(lines)-1]); l != "" {
		return ": " + cut(l, 200)
	}
	return ""
}

func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "…"
}
