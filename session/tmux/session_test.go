package tmux

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"sync"
	"testing"

	"github.com/aidan-bailey/loom/cmd/cmd_test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// argvRecorder is a tmux executor that records the argv and stdin of every
// command it runs. has-session answers "no such session" the first time it
// is asked and "alive" after that, which is the sequence Start's pre-launch
// check and post-launch poll expect. No tmux server is contacted.
type argvRecorder struct {
	mu     sync.Mutex
	probed bool
	fail   error // when set, every Run and CombinedOutput returns it
	// failOn makes a `tmux <sub> …` run through CombinedOutput exit 1
	// after printing failOn[sub], as tmux prints its error message.
	failOn map[string]string
	runs   [][]string
	stdins []string // stdins[i] is what runs[i] read on stdin, "" for none
}

// record appends c's argv and drains its stdin. Callers hold r.mu.
func (r *argvRecorder) record(c *exec.Cmd) {
	var in []byte
	if c.Stdin != nil {
		in, _ = io.ReadAll(c.Stdin)
	}
	r.runs = append(r.runs, slices.Clone(c.Args))
	r.stdins = append(r.stdins, string(in))
}

func (r *argvRecorder) runner() cmd_test.MockCmdExec {
	return cmd_test.MockCmdExec{
		RunFunc: func(c *exec.Cmd) error {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.record(c)
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
			r.record(c)
			return []byte{}, nil
		},
		CombinedOutputFunc: func(c *exec.Cmd) ([]byte, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.record(c)
			if r.fail != nil {
				return nil, r.fail
			}
			if msg, ok := r.failOn[cmd_test.TmuxSubcommand(c.Args)]; ok {
				return []byte(msg + "\n"), errors.New("exit status 1")
			}
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
		if cmd_test.TmuxSubcommand(argv) == sub {
			out = append(out, argv)
		}
	}
	return out
}

// stdin returns what every recorded `tmux <sub> …` command read on stdin,
// in order.
func (r *argvRecorder) stdin(sub string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for i, argv := range r.runs {
		if cmd_test.TmuxSubcommand(argv) == sub {
			out = append(out, r.stdins[i])
		}
	}
	return out
}

// subcommands returns the tmux subcommand of every recorded command, in
// order.
func (r *argvRecorder) subcommands() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, argv := range r.runs {
		if s := cmd_test.TmuxSubcommand(argv); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func TestSession_TypeTextPastesThroughABuffer(t *testing.T) {
	rec := &argvRecorder{}
	s := NewSessionWithDeps("typed", "claude", NewMockPtyFactory(t), rec.runner())
	text := "-v fix it;"

	require.NoError(t, s.TypeText(text))

	require.Equal(t, []string{"load-buffer", "paste-buffer"}, rec.subcommands())
	load, paste := rec.ran("load-buffer")[0], rec.ran("paste-buffer")[0]
	buffer := load[4]
	assert.Regexp(t, fmt.Sprintf(`^loom-loom_typed-%d-\d+$`, os.Getpid()), buffer,
		"named for the session, this process and the call, so no other process's paste can collide")
	assert.Equal(t, []string{"tmux", "-u", "load-buffer", "-b", buffer, "-"}, load)
	assert.Equal(t, []string{text}, rec.stdin("load-buffer"),
		"the text goes in on stdin, where tmux neither parses nor size-caps it")
	assert.Equal(t, []string{"tmux", "-u", "paste-buffer", "-d", "-r", "-b", buffer, "-t", "=loom_typed:"}, paste,
		"the same buffer, deleted once pasted, LF kept as LF, exactly targeted")
}

func TestSession_TypeTextUsesAFreshBufferPerCall(t *testing.T) {
	rec := &argvRecorder{}
	s := NewSessionWithDeps("typed", "claude", NewMockPtyFactory(t), rec.runner())

	require.NoError(t, s.TypeText("one"))
	require.NoError(t, s.TypeText("two"))

	loads := rec.ran("load-buffer")
	require.Len(t, loads, 2)
	assert.NotEqual(t, loads[0][4], loads[1][4])
}

func TestSession_TypeTextEmptyRunsNothing(t *testing.T) {
	rec := &argvRecorder{}
	s := NewSessionWithDeps("typed", "claude", NewMockPtyFactory(t), rec.runner())

	require.NoError(t, s.TypeText(""))

	assert.Empty(t, rec.runs)
}

func TestSession_PressKeysNamesKeysInOrder(t *testing.T) {
	rec := &argvRecorder{}
	s := NewSessionWithDeps("keys", "aider", NewMockPtyFactory(t), rec.runner())

	require.NoError(t, s.PressKeys("D", "Enter"))

	assert.Equal(t, [][]string{{"tmux", "-u", "send-keys", "-t", "=loom_keys:", "D", "Enter"}}, rec.ran("send-keys"))
}

func TestSession_SendPromptTypesThenPressesEnter(t *testing.T) {
	rec := &argvRecorder{}
	s := NewSessionWithDeps("prompt", "claude", NewMockPtyFactory(t), rec.runner())

	require.NoError(t, s.SendPrompt("fix the login bug"))

	assert.Equal(t, []string{"load-buffer", "paste-buffer", "send-keys"}, rec.subcommands())
	assert.Equal(t, []string{"fix the login bug"}, rec.stdin("load-buffer"))
	assert.Equal(t, [][]string{{"tmux", "-u", "send-keys", "-t", "=loom_prompt:", "Enter"}}, rec.ran("send-keys"))
}

func TestSession_TypeTextReportsTmuxFailure(t *testing.T) {
	rec := &argvRecorder{fail: errors.New("no server running")}
	s := NewSessionWithDeps("gone", "claude", NewMockPtyFactory(t), rec.runner())

	err := s.TypeText("x")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "loom_gone")
	assert.Empty(t, rec.ran("paste-buffer"), "nothing to paste when the load failed")
}

func TestSession_TypeTextDeletesTheBufferWhenThePasteFails(t *testing.T) {
	rec := &argvRecorder{failOn: map[string]string{"paste-buffer": "can't find pane: =loom_gone:"}}
	s := NewSessionWithDeps("gone", "claude", NewMockPtyFactory(t), rec.runner())

	err := s.TypeText("x")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "can't find pane", "tmux's own message, not just its exit status")
	buffer := rec.ran("load-buffer")[0][4]
	assert.Equal(t, [][]string{{"tmux", "-u", "delete-buffer", "-b", buffer}}, rec.ran("delete-buffer"),
		"-d deletes only after a paste, so a failed one would leave the buffer on the server")
}

func TestSession_PressKeysReportsTmuxMessage(t *testing.T) {
	rec := &argvRecorder{failOn: map[string]string{"send-keys": "can't find pane: =loom_keys:"}}
	s := NewSessionWithDeps("keys", "claude", NewMockPtyFactory(t), rec.runner())

	err := s.PressKeys("Enter")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "can't find pane")
	assert.Contains(t, err.Error(), "loom_keys")
}

func TestSession_StartLaunchesWithoutAttaching(t *testing.T) {
	ptyFactory := NewMockPtyFactory(t)
	rec := &argvRecorder{}
	s := NewSessionWithDeps("launch", "claude", ptyFactory, rec.runner())
	workdir := t.TempDir()

	require.NoError(t, s.Start(workdir))

	require.Len(t, ptyFactory.cmds, 1, "new-session only: no attach client")
	assert.Equal(t, []string{"tmux", "-u", "new-session", "-d", "-s", "loom_launch", "-c", workdir, "claude"}, ptyFactory.cmds[0].Args)
	assert.Len(t, rec.ran("set-option"), 4, "history-limit, mouse, status and detach-on-destroy")
	assert.Contains(t, rec.ran("set-option"), []string{"tmux", "-u", "set-option", "-t", "=loom_launch:", "detach-on-destroy", "on"})
	assert.Len(t, rec.ran("bind-key"), 1)
	assert.Empty(t, rec.ran("capture-pane"), "no seed capture: that belongs to an attach client")
}

func TestSession_CloseOnlyKills(t *testing.T) {
	ptyFactory := NewMockPtyFactory(t)
	rec := &argvRecorder{}
	s := NewSessionWithDeps("closing", "claude", ptyFactory, rec.runner())

	require.NoError(t, s.Close())

	assert.Equal(t, [][]string{{"tmux", "-u", "kill-session", "-t", "=loom_closing"}}, rec.runs)
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
			want := append([]string{"tmux", "-u", "send-keys", "-t", "=loom_trust:"}, tc.keys...)
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
