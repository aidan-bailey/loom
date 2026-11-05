package cli

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/flow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeFlowRoot is a flow root whose lib holds the given scripts.
func fakeFlowRoot(t *testing.T, scripts map[string]string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "flow")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "plugin", "docs"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "lib"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, flow.RosterFile), []byte("loom flow roster 0123456789ab\n"), 0o600))
	for name, body := range scripts {
		require.NoError(t, os.WriteFile(filepath.Join(root, "lib", name), []byte(body), 0o600))
	}
	return root
}

func needPython(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
}

func runFlow(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := newFlowCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

// flowExit is the exit code a flow command's error asks main to end with: an
// *ExitError's, 1 for another error, 0 for none.
func flowExit(err error) int {
	var exit *ExitError
	if errors.As(err, &exit) {
		return exit.Code
	}
	if err != nil {
		return 1
	}
	return 0
}

func TestFlowTool_PassesArgsEnvAndTheExitCodeThrough(t *testing.T) {
	needPython(t)
	root := fakeFlowRoot(t, map[string]string{"check_plan.py": "import os, sys\n" +
		"print(' '.join(sys.argv[1:]), os.environ['FLOW_ROSTER'], os.environ['FLOW_ROOT'] == os.environ['LOOM_FLOW_ROOT'])\n" +
		"sys.exit(7)\n"})
	t.Setenv(flow.EnvRoot, root)

	out, _, err := runFlow(t, "check-plan", "--x", "plan.md")

	assert.Equal(t, 7, flowExit(err))
	assert.Equal(t, "--x plan.md loom flow roster 0123456789ab True\n", out)
}

func TestFlowTool_DoctorRunsFlowconfDoctor(t *testing.T) {
	needPython(t)
	t.Setenv(flow.EnvRoot, fakeFlowRoot(t, map[string]string{"flowconf.py": "import sys\nprint(sys.argv[1:])\n"}))
	out, _, err := runFlow(t, "doctor")
	require.NoError(t, err)
	assert.Equal(t, "['doctor']\n", out)
}

func TestFlowTool_RefusesARootThatIsNotOne(t *testing.T) {
	t.Setenv(flow.EnvRoot, t.TempDir())
	_, _, err := runFlow(t, "conf", "status")
	assert.Equal(t, exitNotRun, flowExit(err))
	assert.ErrorContains(t, err, "is not a flow root")
}

func TestFlowTool_RefusesWithoutPython(t *testing.T) {
	t.Setenv(flow.EnvRoot, fakeFlowRoot(t, nil))
	t.Setenv("PATH", t.TempDir())
	_, _, err := runFlow(t, "conf", "status")
	assert.Equal(t, exitNotRun, flowExit(err))
	assert.ErrorContains(t, err, "python3 is not on PATH")
}

func TestFlowTool_OutsideASessionRunsThisBuildsCopy(t *testing.T) {
	needPython(t)
	t.Setenv(flow.EnvRoot, "")
	out, _, err := runFlow(t, "conf", "roster")
	require.NoError(t, err)
	assert.Equal(t, flow.RosterID()+"\n", out)
}

func TestFlowTool_EveryToolStartsUnderIsolatedPython(t *testing.T) {
	needPython(t)
	t.Setenv(flow.EnvRoot, "")
	for _, tool := range flowTools {
		if strings.HasSuffix(tool.script, ".sh") {
			continue
		}
		// A script without argparse may refuse --help; what this pins is that
		// every one imports its siblings under -I, which adds no script dir.
		_, errOut, _ := runFlow(t, tool.name, "--help")
		assert.NotContains(t, errOut, "ModuleNotFoundError", tool.name)
		assert.NotContains(t, errOut, "ImportError", tool.name)
	}
}

func TestFlowPath_PrintsAPluginFileOnlyInsideASession(t *testing.T) {
	root := fakeFlowRoot(t, nil)
	require.NoError(t, os.WriteFile(filepath.Join(root, "plugin", "docs", "dispatch.md"), []byte("d"), 0o600))
	t.Setenv(flow.EnvRoot, root)

	out, _, err := runFlow(t, "path", "docs/dispatch.md")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "plugin", "docs", "dispatch.md")+"\n", out)

	_, _, err = runFlow(t, "path", "docs/missing.md")
	assert.Equal(t, 1, flowExit(err))
	_, _, err = runFlow(t, "path", "../ROSTER")
	assert.Equal(t, 1, flowExit(err), "a path out of the plugin")

	t.Setenv(flow.EnvRoot, "")
	_, _, err = runFlow(t, "path", "docs/dispatch.md")
	assert.Equal(t, 1, flowExit(err))
	assert.ErrorContains(t, err, "inside a session")
}

// guardScript is a git_guard.py that prints its arguments and exits code.
func guardScript(code int) string {
	return "import sys\nsys.stdin.read()\nprint(sys.argv[1:])\nsys.exit(" + string(rune('0'+code)) + ")\n"
}

func guard(t *testing.T, root, input string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := runFlowGuard(strings.NewReader(input), &out, &errOut, root, exec.LookPath)
	return code, out.String(), errOut.String()
}

func TestFlowGuard_ChecksOnlyTheCallsAGuardedAgentsModeCovers(t *testing.T) {
	needPython(t)
	root := fakeFlowRoot(t, map[string]string{"git_guard.py": guardScript(2)})
	dev, drafter := `"agent_type":"flow:junior-dev"`, `"agent_type":"flow:drafter"`

	code, _, _ := guard(t, root, `{`+dev+`,"tool_name":"Bash"}`)
	assert.Equal(t, 2, code, "a developer's Bash call gets the guard's verdict")
	code, out, _ := guard(t, root, `{`+drafter+`,"tool_name":"Write"}`)
	assert.Equal(t, 2, code)
	assert.Contains(t, out, "--drafter")

	for _, input := range []string{
		`{` + dev + `,"tool_name":"Write"}`,                 // a developer's Write: not its mode's
		`{"agent_type":"flow:verifier","tool_name":"Bash"}`, // not guarded
		`{"tool_name":"Bash"}`,                              // the main thread
		`not json`,
	} {
		code, out, _ := guard(t, root, input)
		assert.Equal(t, 0, code, input)
		assert.Empty(t, out, "the guard did not run: %s", input)
	}
}

func TestFlowGuard_PassesWhatTheGuardAllows(t *testing.T) {
	needPython(t)
	code, _, _ := guard(t, fakeFlowRoot(t, map[string]string{"git_guard.py": guardScript(0)}), `{"agent_type":"flow:junior-dev","tool_name":"Bash"}`)
	assert.Equal(t, 0, code)
}

func TestFlowGuard_FailsClosedForAGuardedAgent(t *testing.T) {
	needPython(t)
	in := `{"agent_type":"flow:junior-dev","tool_name":"Bash"}`

	code, _, errOut := guard(t, "", in)
	assert.Equal(t, 2, code, "no root")
	assert.Contains(t, errOut, flow.EnvRoot)

	code, _, _ = guard(t, fakeFlowRoot(t, map[string]string{"git_guard.py": guardScript(1)}), in)
	assert.Equal(t, 2, code, "a guard that crashed blocks, since Claude reads exit 1 as allow")

	var out, errBuf bytes.Buffer
	noPython := func(string) (string, error) { return "", exec.ErrNotFound }
	assert.Equal(t, 2, runFlowGuard(strings.NewReader(in), &out, &errBuf, fakeFlowRoot(t, nil), noPython))
	assert.Contains(t, errBuf.String(), "python3")

	code, _, _ = guard(t, "", `{"agent_type":"flow:verifier","tool_name":"Bash"}`)
	assert.Equal(t, 0, code, "an unguarded agent passes whatever the guard's state")
}

func TestFlowImport_CopiesSkipsAndRefusesRecords(t *testing.T) {
	checkout, data := t.TempDir(), t.TempDir()
	write := func(base, rel, body string) {
		p := filepath.Join(base, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	}
	write(checkout, "records/-r/new.md", "new")
	write(checkout, "records/-r/same.md", "same")
	write(checkout, "records/-r/other.md", "theirs")
	write(checkout, "records/retros/retro-2026-11-01.md", "retro")
	write(checkout, "repos/-nowhere-at-all.toml", "schema = 1\n")
	write(data, "records/-r/same.md", "same")
	write(data, "records/-r/other.md", "mine")

	var out bytes.Buffer
	refused, err := importRecords(checkout, data, &out)
	require.NoError(t, err)

	assert.Equal(t, 1, refused)
	got, _ := os.ReadFile(filepath.Join(data, "records", "-r", "new.md"))
	assert.Equal(t, "new", string(got))
	got, _ = os.ReadFile(filepath.Join(data, "records", "-r", "other.md"))
	assert.Equal(t, "mine", string(got), "a differing record is never overwritten")
	assert.FileExists(t, filepath.Join(data, "records", "retros", "retro-2026-11-01.md"))
	assert.Contains(t, out.String(), "refused records/-r/other.md")
	assert.Contains(t, out.String(), "repos/-nowhere-at-all.toml: no checkout at the path its key names")
	assert.Contains(t, out.String(), "copied 2, skipped 1, refused 1")
}

func TestCheckoutOf_FindsTheDirectoryAKeyNames(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	dir := filepath.Join(base, "Dilettante Trading", "prader-rs.x")
	require.NoError(t, os.MkdirAll(dir, 0o700))

	got, ok := checkoutOf(keyOf(dir))
	assert.True(t, ok)
	assert.Equal(t, dir, got)
	_, ok = checkoutOf(keyOf(filepath.Join(base, "absent")))
	assert.False(t, ok)
}

// TestFlowGuard_TheShippedGuardBlocksADevelopersStash runs the guard on a
// copy of this build's flow tree, as a launch's hook does: the real
// git_guard.py, under isolated Python, sees the hook input Claude sends for
// a flow developer's Bash call (agent_type as TestRealClaude_FlowPluginContract
// saw it).
func TestFlowGuard_TheShippedGuardBlocksADevelopersStash(t *testing.T) {
	needPython(t)
	root := filepath.Join(t.TempDir(), "flow")
	require.NoError(t, flow.Write(root))
	call := func(agent, command string) (int, string) {
		in := `{"hook_event_name":"PreToolUse","agent_type":"` + agent + `","tool_name":"Bash","cwd":"/","tool_input":{"command":"` + command + `"}}`
		code, _, errOut := guard(t, root, in)
		return code, errOut
	}

	code, errOut := call("flow:junior-dev", "git stash")
	assert.Equal(t, 2, code)
	assert.Contains(t, errOut, "developer-git-guard: `git stash` is blocked")
	code, errOut = call("flow:junior-dev", "git status")
	assert.Equal(t, 0, code, errOut)
	code, _ = call("flow:verifier", "git stash")
	assert.Equal(t, 0, code, "the verifier is not guarded")
}
