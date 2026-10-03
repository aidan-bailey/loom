package tmux

import (
	"errors"
	"os/exec"
	"slices"
	"sync"
	"testing"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// argvRecorder is a tmux executor that records the argv of every command
// it runs. has-session answers "no such session" the first time it is
// asked and "alive" after that, which is the sequence Start's pre-launch
// check and post-launch poll expect. No tmux server is contacted.
type argvRecorder struct {
	mu     sync.Mutex
	probed bool
	fail   error // when set, every Run returns it
	runs   [][]string
}

func (r *argvRecorder) runner() cmd_test.MockCmdExec {
	return cmd_test.MockCmdExec{
		RunFunc: func(c *exec.Cmd) error {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.runs = append(r.runs, slices.Clone(c.Args))
			if r.fail != nil {
				return r.fail
			}
			if slices.Contains(c.Args, "has-session") && !r.probed {
				r.probed = true
				return errors.New("can't find session")
			}
			return nil
		},
		OutputFunc: func(c *exec.Cmd) ([]byte, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.runs = append(r.runs, slices.Clone(c.Args))
			return []byte{}, nil
		},
	}
}

// ran returns the argv of every recorded `tmux <sub> …` command, in order.
func (r *argvRecorder) ran(sub string) [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out [][]string
	for _, argv := range r.runs {
		if len(argv) > 1 && argv[1] == sub {
			out = append(out, argv)
		}
	}
	return out
}

func TestSession_TypeTextSendsLiteralKeys(t *testing.T) {
	rec := &argvRecorder{}
	s := NewSessionWithDeps("typed", "claude", NewMockPtyFactory(t), rec.runner())

	require.NoError(t, s.TypeText("-v fix it"))

	assert.Equal(t, [][]string{{"tmux", "send-keys", "-l", "-t", "=loom_typed:", "--", "-v fix it"}}, rec.ran("send-keys"),
		"literal, exactly targeted, and a leading dash is typed rather than parsed as a flag")
}

func TestSession_TypeTextEmptyRunsNothing(t *testing.T) {
	rec := &argvRecorder{}
	s := NewSessionWithDeps("typed", "claude", NewMockPtyFactory(t), rec.runner())

	require.NoError(t, s.TypeText(""))

	assert.Empty(t, rec.ran("send-keys"))
}

func TestSession_PressKeysNamesKeysInOrder(t *testing.T) {
	rec := &argvRecorder{}
	s := NewSessionWithDeps("keys", "aider", NewMockPtyFactory(t), rec.runner())

	require.NoError(t, s.PressKeys("D", "Enter"))

	assert.Equal(t, [][]string{{"tmux", "send-keys", "-t", "=loom_keys:", "D", "Enter"}}, rec.ran("send-keys"))
}

func TestSession_SendPromptTypesThenPressesEnter(t *testing.T) {
	rec := &argvRecorder{}
	s := NewSessionWithDeps("prompt", "claude", NewMockPtyFactory(t), rec.runner())

	require.NoError(t, s.SendPrompt("fix the login bug"))

	assert.Equal(t, [][]string{
		{"tmux", "send-keys", "-l", "-t", "=loom_prompt:", "--", "fix the login bug"},
		{"tmux", "send-keys", "-t", "=loom_prompt:", "Enter"},
	}, rec.ran("send-keys"))
}

func TestSession_TypeTextReportsTmuxFailure(t *testing.T) {
	rec := &argvRecorder{fail: errors.New("no server running")}
	s := NewSessionWithDeps("gone", "claude", NewMockPtyFactory(t), rec.runner())

	err := s.TypeText("x")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "loom_gone")
}

func TestSession_StartLaunchesWithoutAttaching(t *testing.T) {
	ptyFactory := NewMockPtyFactory(t)
	rec := &argvRecorder{}
	s := NewSessionWithDeps("launch", "claude", ptyFactory, rec.runner())
	workdir := t.TempDir()

	require.NoError(t, s.Start(workdir))

	require.Len(t, ptyFactory.cmds, 1, "new-session only: no attach client")
	assert.Equal(t, []string{"tmux", "new-session", "-d", "-s", "loom_launch", "-c", workdir, "claude"}, ptyFactory.cmds[0].Args)
	assert.Len(t, rec.ran("set-option"), 3, "history-limit, mouse and status, as before")
	assert.Len(t, rec.ran("bind-key"), 1)
	assert.Empty(t, rec.ran("capture-pane"), "no seed capture: that belongs to an attach client")
}

func TestSession_CloseOnlyKills(t *testing.T) {
	ptyFactory := NewMockPtyFactory(t)
	rec := &argvRecorder{}
	s := NewSessionWithDeps("closing", "claude", ptyFactory, rec.runner())

	require.NoError(t, s.Close())

	assert.Equal(t, [][]string{{"tmux", "kill-session", "-t", "=loom_closing"}}, rec.runs)
	assert.Empty(t, ptyFactory.cmds)
}

func TestSession_DismissTrustPrompt(t *testing.T) {
	for _, tc := range []struct {
		name, program, screen string
		found                 bool
		keys                  []string
	}{
		{"claude taps enter", "claude", "Do you trust the files in this folder?\n❯ 1. Yes, proceed", true, []string{"Enter"}},
		{"aider answers D", "aider", "Open documentation url for more info? (Y)es/(N)o/(D)on't ask again", true, []string{"D", "Enter"}},
		{"no prompt on screen", "claude", "$ ls\nREADME.md", false, nil},
		{"an agent with no patterns", "bash", "Do you trust the files in this folder?", false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &argvRecorder{}
			s := NewSessionWithDeps("trust", tc.program, NewMockPtyFactory(t), rec.runner())

			assert.Equal(t, tc.found, s.DismissTrustPrompt(tc.screen))

			if tc.keys == nil {
				assert.Empty(t, rec.ran("send-keys"))
				return
			}
			want := append([]string{"tmux", "send-keys", "-t", "=loom_trust:"}, tc.keys...)
			assert.Equal(t, [][]string{want}, rec.ran("send-keys"))
		})
	}
}

func TestSession_WithProgramEnv(t *testing.T) {
	ptyFactory := NewMockPtyFactory(t)
	cmdExec := cmd_test.MockCmdExec{}
	old := NewSessionWithDeps("with program env", "aider", ptyFactory, cmdExec, "A=1")

	got := old.WithProgramEnv("claude --model opus", []string{"CLAUDE_CONFIG_DIR=/acct/max-2"})

	require.NotSame(t, old, got)
	require.Equal(t, old.sanitizedName, got.sanitizedName)
	require.Equal(t, "claude --model opus", got.program)
	require.Equal(t, "claude", got.adapter.Name())
	require.Equal(t, "aider", old.adapter.Name(), "the original is untouched")
	require.Equal(t, []string{"A=1"}, old.env)
	require.Equal(t, []string{"CLAUDE_CONFIG_DIR=/acct/max-2"}, got.env)
	require.Same(t, ptyFactory, got.ptyFactory.(*MockPtyFactory))
	require.Equal(t, cmdExec, got.cmdExec)
	require.Equal(t, []string{"A=1"}, old.WithProgram("claude").env, "WithProgram carries the env over")
}
