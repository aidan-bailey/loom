package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/flow"
	"github.com/aidan-bailey/loom/session/claudemod"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeConf is a flowConf with the test's answers: its own state dir, the
// gate lines it is given, and a vacuous check that finds nothing unless the
// test says otherwise. It records each Gates call's arguments.
type fakeConf struct {
	state    string
	gates    []string
	gatesErr error
	vacuous  func(file string) (string, bool, error)
	gateArgs [][]string
}

func (f *fakeConf) State() (string, error) { return f.state, nil }

func (f *fakeConf) Gates(args ...string) ([]string, error) {
	f.gateArgs = append(f.gateArgs, args)
	return f.gates, f.gatesErr
}

func (f *fakeConf) Vacuous(file string) (string, bool, error) {
	if f.vacuous == nil {
		return "", false, nil
	}
	return f.vacuous(file)
}

// useFakeConf makes newFlowConf answer with a fakeConf for the test.
func useFakeConf(t *testing.T) *fakeConf {
	t.Helper()
	f := &fakeConf{state: t.TempDir()}
	old := newFlowConf
	newFlowConf = func() (flowConf, func(), error) { return f, func() {}, nil }
	t.Cleanup(func() { newFlowConf = old })
	return f
}

// flowJobsDaemon serves a test daemon for flow's job commands, with no
// launch and a fake flowconf whose state dir is the test's own.
func flowJobsDaemon(t *testing.T) (*testDaemon, *fakeConf) {
	t.Helper()
	isolateWorkspaces(t)
	t.Setenv(claudemod.EnvLaunch, "")
	d := serveDaemon(t, "true")
	return d, useFakeConf(t)
}

const fakeFlowconf = `import sys
cmd = sys.argv[1]
if cmd == "state":
    print("/the/state")
elif cmd == "gates" and sys.argv[2] == "--final":
    print("RUN echo a")
    print("FAIL cargo doc {packages} (cannot expand {packages}: no metadata)")
    sys.exit(1)
elif cmd == "gates":
    print("flowconf: no usable profile", file=sys.stderr)
    sys.exit(1)
elif cmd == "vacuous" and sys.argv[2] == "hollow":
    print("0 passed")
    print("0 passed again")
elif cmd == "vacuous" and sys.argv[2] == "fine":
    sys.exit(1)
else:
    print("flowconf: bad pattern", file=sys.stderr)
    sys.exit(2)
`

func TestScriptConf_ReadsFlowconfsAnswers(t *testing.T) {
	needPython(t)
	conf := scriptConf{root: fakeFlowRoot(t, map[string]string{"flowconf.py": fakeFlowconf})}

	state, err := conf.State()
	require.NoError(t, err)
	assert.Equal(t, "/the/state", state)

	lines, err := conf.Gates("--final")
	require.NoError(t, err, "exit 1 with a FAIL line is still an expansion")
	assert.Equal(t, []string{"RUN echo a", "FAIL cargo doc {packages} (cannot expand {packages}: no metadata)"}, lines)
	_, err = conf.Gates("--story", "abc")
	assert.ErrorContains(t, err, "no usable profile", "exit 1 with no FAIL line is an error")

	match, vacuous, err := conf.Vacuous("hollow")
	require.NoError(t, err)
	assert.True(t, vacuous)
	assert.Equal(t, "0 passed", match)
	_, vacuous, err = conf.Vacuous("fine")
	require.NoError(t, err)
	assert.False(t, vacuous)
	_, _, err = conf.Vacuous("broken")
	assert.ErrorContains(t, err, "bad pattern")
}

func TestFlowDaemon_ADaemonItCantReachExits125(t *testing.T) {
	useFakeConf(t)
	old := Join
	Join = nil
	t.Cleanup(func() { Join = old })
	_, _, err := runFlow(t, "wait", "start", "build", "--", "true")
	assert.Equal(t, exitNotRun, flowExit(err))
	assert.ErrorContains(t, err, "no way to reach the loom daemon")
}

// docs-check's exit 1 lists the strays, but a crash exits 1 too: one with
// no paths is an error, never a pass that changed only docs (the doc pass's
// spec, §5.4).
func TestScriptTools_DocsCheck(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "lib"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, flow.RosterFile), []byte("loom flow roster 0123456789ab\n"), 0o600))
	stub := func(code int, out string) {
		script := fmt.Sprintf("import sys\nsys.stdout.write(%q)\nsys.stderr.write('why it failed')\nsys.exit(%d)\n", out, code)
		require.NoError(t, os.WriteFile(filepath.Join(root, "lib", "flowconf.py"), []byte(script), 0o600))
	}
	tools := scriptTools{root: root}

	stub(0, "")
	strays, err := tools.DocsCheck("b", "h")
	require.NoError(t, err)
	assert.Empty(t, strays)

	stub(1, "main.go\ngo.sum\n")
	strays, err = tools.DocsCheck("b", "h")
	require.NoError(t, err)
	assert.Equal(t, []string{"main.go", "go.sum"}, strays)

	stub(1, "")
	_, err = tools.DocsCheck("b", "h")
	assert.ErrorContains(t, err, "exited 1 and listed no paths: why it failed")

	stub(2, "")
	_, err = tools.DocsCheck("b", "h")
	assert.ErrorContains(t, err, "exited 2: why it failed")

	stub(0, "main.go\n")
	_, err = tools.DocsCheck("b", "h")
	assert.Error(t, err, "exit 0 with a path is no answer")
}
