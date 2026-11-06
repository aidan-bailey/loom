package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/flow"
	"github.com/spf13/cobra"
)

// FlowCmd is `loom flow`: the agent flow's tools (flow in loom spec §5.5).
// main attaches it.
var FlowCmd = newFlowCmd()

// flowTool is a `loom flow` subcommand that runs a script in a flow
// root's lib/, with args before the caller's.
type flowTool struct {
	name, script, short string
	args                []string
}

var flowTools = []flowTool{
	{name: "conf", script: "flowconf.py", short: "The repo's flow key, profile, role blocks, state, drafts and gates"},
	{name: "check-plan", script: "check_plan.py", short: "Check that a plan is ready for the supervised option"},
	{name: "packet", script: "plan_packet.py", short: "Write a plan's seams packet"},
	{name: "join", script: "plan_join.py", short: "Rebuild a plan's stories from their drafts"},
	{name: "premises", script: "plan_premises.py", short: "List a plan's premises the tree doesn't bear out"},
	{name: "conformance", script: "plan_conformance.py", short: "Show which of a story's plan code landed as written"},
	{name: "costs", script: "agent_costs.py", short: "Token usage and API-price cost of a session's agents, or of run records"},
	{name: "doctor", script: "flowconf.py", args: []string{"doctor"}, short: "Check that a supervised run can start here"},
	{name: "check-agents", script: "check-agents.sh", short: "Lint the flow plugin's agents and skills"},
}

func newFlowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "flow",
		Short: "The agent flow's tools: plans, profiles, gates, the git guard",
	}
	for _, tool := range flowTools {
		cmd.AddCommand(&cobra.Command{
			Use:                tool.name + " [ARGS…]",
			Short:              tool.short,
			DisableFlagParsing: true, // the script parses its own flags, --help among them
			RunE: func(c *cobra.Command, args []string) error {
				loomErrors(c)
				return runFlowTool(c, tool, args)
			},
		})
	}
	cmd.AddCommand(newFlowGuardCmd(), newFlowPathCmd(), newFlowImportCmd(), newFlowWaitCmd(), newFlowGatesCmd(), newFlowAgentCmd(), newFlowRunCmd())
	return cmd
}

// flowRoot is the flow root a tool runs from, and its cleanup: the
// launch's (flow.EnvRoot) inside a session, else this binary's copy,
// written to a temp dir for the call. A set EnvRoot that is not a flow
// root is refused, so a tool never runs another copy than its prompts'.
func flowRoot() (string, func(), error) {
	if dir := os.Getenv(flow.EnvRoot); dir != "" {
		if !flow.IsRoot(dir) {
			return "", nil, &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow: %s=%s is not a flow root", flow.EnvRoot, dir)}
		}
		return dir, func() {}, nil
	}
	tmp, err := os.MkdirTemp("", "loom-flow-")
	if err != nil {
		return "", nil, fmt.Errorf("loom flow: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }
	root := filepath.Join(tmp, "flow")
	if err := flow.Write(root); err != nil {
		cleanup()
		return "", nil, err
	}
	return root, cleanup, nil
}

// flowDataDir is where run records and retros live: <global dir>/flow.
func flowDataDir() (string, error) {
	dir, err := config.GetGlobalConfigDir()
	if err != nil {
		return "", fmt.Errorf("loom flow: the global dir: %w", err)
	}
	return filepath.Join(dir, "flow"), nil
}

// runFlowTool runs tool's script from a flow root, its stdio the
// command's, and ends with the script's exit code (an *ExitError for a
// non-zero one).
func runFlowTool(c *cobra.Command, tool flowTool, args []string) error {
	root, cleanup, err := flowRoot()
	if err != nil {
		return err
	}
	defer cleanup()
	code, err := flowScript(root, tool.script, append(slices.Clone(tool.args), args...), c.InOrStdin(), c.OutOrStdout(), c.ErrOrStderr())
	switch {
	case err != nil:
		return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow %s: %v", tool.name, err)}
	case code != 0:
		return &ExitError{Code: code}
	}
	return nil
}

func newFlowGuardCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "guard",
		Short: "Loom's PreToolUse hook: the git guard for flow's developers and drafter",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			loomErrors(c)
			if code := runFlowGuard(c.InOrStdin(), c.OutOrStdout(), c.ErrOrStderr(), os.Getenv(flow.EnvRoot), exec.LookPath); code != 0 {
				return &ExitError{Code: code}
			}
			return nil
		},
	}
}

// runFlowGuard is `loom flow guard` (spec §5.3). It reads the hook input,
// and for a call a guarded agent's mode covers it runs git_guard.py from
// the flow root on the same input and returns its verdict. A covered call
// it can't check is blocked (2), since Claude reads any other non-zero
// exit as allow; every other call passes (0), whatever the guard's state.
func runFlowGuard(in io.Reader, stdout, stderr io.Writer, root string, lookPath func(string) (string, error)) int {
	input, err := io.ReadAll(in)
	if err != nil {
		return 0 // no input names an agent, so nothing says the call is a guarded one
	}
	var call struct {
		AgentType string `json:"agent_type"`
		ToolName  string `json:"tool_name"`
	}
	if json.Unmarshal(input, &call) != nil {
		return 0
	}
	mode, ok := flow.GuardedMode(call.AgentType)
	if !ok || !mode.Guards(call.ToolName) {
		return 0
	}
	block := func(why string) int {
		fmt.Fprintf(stderr, "loom flow guard: %s's %s call is blocked: %s\n", call.AgentType, call.ToolName, why)
		return 2
	}
	if root == "" || !flow.IsRoot(root) {
		return block(flow.EnvRoot + " names no flow root, so the git guard can't run")
	}
	python, err := lookPath("python3")
	if err != nil {
		return block("python3 is not on PATH, so the git guard can't run")
	}
	args := []string{"-I", filepath.Join(root, "lib", "git_guard.py")}
	if mode == flow.Drafter {
		args = append(args, "--drafter")
	}
	runner := exec.Command(python, args...)
	runner.Stdin, runner.Stdout, runner.Stderr = bytes.NewReader(input), stdout, stderr
	err = runner.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit) && exit.ExitCode() == 2:
		return 2
	default:
		return block(fmt.Sprintf("the git guard failed: %v", err))
	}
}

func newFlowPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path FILE",
		Short: "Print the absolute path of a file in this session's flow plugin (docs/dispatch.md, …)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			loomErrors(c)
			root := os.Getenv(flow.EnvRoot)
			if root == "" {
				return fmt.Errorf("loom flow path: %s is unset; it names a launch's flow root, so run this inside a session loom launched", flow.EnvRoot)
			}
			if !flow.IsRoot(root) {
				return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow path: %s=%s is not a flow root", flow.EnvRoot, root)}
			}
			plugin := filepath.Join(root, "plugin")
			p := filepath.Join(plugin, filepath.FromSlash(args[0]))
			if rel, err := filepath.Rel(plugin, p); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return fmt.Errorf("loom flow path: %s is outside the flow plugin", args[0])
			}
			if _, err := os.Stat(p); err != nil {
				return fmt.Errorf("loom flow path: the flow plugin has no %s", args[0])
			}
			fmt.Fprintln(c.OutOrStdout(), p)
			return nil
		},
	}
}

func newFlowImportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "import CHECKOUT",
		Short: "Copy a claude-flow checkout's run records into loom, and say where its profiles go",
		Long: "Copies CHECKOUT's records/ into <global dir>/flow/records: a record already there with the same bytes is " +
			"skipped, one with other bytes is refused and listed. Then prints where each repos/<key>.toml belongs: " +
			"that repo's .claude/flow.toml, for you or that repo's session to commit. Exits 1 when it refused any record.",
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			loomErrors(c)
			data, err := flowDataDir()
			if err != nil {
				return err
			}
			refused, err := importRecords(args[0], data, c.OutOrStdout())
			if err != nil {
				return err
			}
			if refused > 0 {
				return &ExitError{Code: 1}
			}
			return nil
		},
	}
}

// importRecords copies checkout's records/ into data/records, never over a
// file that differs, then lists where each of checkout's profiles belongs.
// It returns how many records it refused.
func importRecords(checkout, data string, out io.Writer) (int, error) {
	var copied, skipped, refused int
	src := filepath.Join(checkout, "records")
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(checkout, p)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		target := filepath.Join(data, rel)
		switch have, err := os.ReadFile(target); {
		case err == nil && bytes.Equal(have, b):
			skipped++
		case err == nil:
			refused++
			fmt.Fprintf(out, "refused %s: %s differs\n", filepath.ToSlash(rel), target)
		case errors.Is(err, fs.ErrNotExist):
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			if err := config.AtomicWriteFile(target, b, 0o600); err != nil {
				return err
			}
			copied++
		default:
			return err
		}
		return nil
	})
	if err != nil {
		return refused, fmt.Errorf("loom flow import: %w", err)
	}
	profiles, err := filepath.Glob(filepath.Join(checkout, "repos", "*.toml"))
	if err != nil {
		return refused, fmt.Errorf("loom flow import: %w", err)
	}
	for _, p := range profiles {
		key := strings.TrimSuffix(filepath.Base(p), ".toml")
		if dir, ok := checkoutOf(key); ok {
			fmt.Fprintf(out, "repos/%s.toml: copy it to %s and commit it there\n", key, filepath.Join(dir, ".claude", "flow.toml"))
		} else {
			fmt.Fprintf(out, "repos/%s.toml: no checkout at the path its key names\n", key)
		}
	}
	fmt.Fprintf(out, "copied %d, skipped %d, refused %d\n", copied, skipped, refused)
	return refused, nil
}

var nonKey = regexp.MustCompile(`[^A-Za-z0-9]`)

// keyOf is flowconf's key of a path: every character outside [A-Za-z0-9]
// made "-".
func keyOf(path string) string { return nonKey.ReplaceAllString(path, "-") }

// checkoutOf is the directory whose resolved path key names, found by
// walking down from / one entry at a time, or false when none does.
func checkoutOf(key string) (string, bool) {
	return walkKey(string(filepath.Separator), strings.TrimPrefix(key, "-"))
}

func walkKey(dir, rest string) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		s, next := keyOf(e.Name()), filepath.Join(dir, e.Name())
		if rest == s {
			return next, true
		}
		if strings.HasPrefix(rest, s+"-") {
			if found, ok := walkKey(next, rest[len(s)+1:]); ok {
				return found, true
			}
		}
	}
	return "", false
}
