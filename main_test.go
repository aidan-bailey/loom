package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRootCmd_VersionFlag exercises `loom --version`. The flag must
// print the same version string the `loom version` subcommand emits so
// users have a familiar one-shot affordance without remembering the
// subcommand spelling.
func TestRootCmd_VersionFlag(t *testing.T) {
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"--version"})

	require.NoError(t, rootCmd.Execute())

	out := buf.String()
	assert.Contains(t, out, "loom version "+version,
		"--version must print the canonical version line")
	assert.Contains(t, out, "https://github.com/aidan-bailey/loom/releases/tag/v"+version,
		"--version must include the releases URL like the `loom version` subcommand does")
	assert.True(t, strings.HasSuffix(strings.TrimSpace(out), "v"+version),
		"output must end with the version-tag URL, not stray content")
}

// isolateLoomEnv points every loom path and the tmux socket at throwaway
// locations so a missing guard cannot touch real state.
func isolateLoomEnv(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv(config.EnvHome, t.TempDir())
	t.Setenv(config.EnvGlobalDir, t.TempDir())
	t.Setenv(tmux.EnvTmuxSocket, fmt.Sprintf("loomtest-none-%d", time.Now().UnixNano()))
	rootCmd.SetOut(io.Discard)
	rootCmd.SetErr(io.Discard)
	// pflag latches a bool flag's value across Execute() calls on the same
	// FlagSet — Parse only visits flags present in the new argv, so a prior
	// "--version" run (TestRootCmd_VersionFlag shares this package's single
	// rootCmd) would otherwise make every later Execute() short-circuit to
	// the version template before RunE ever runs. Reset it defensively; the
	// flag may not exist yet if this runs before rootCmd's first Execute().
	_ = rootCmd.Flags().Set("version", "false")
}

func stubNesting(t *testing.T, err error) {
	t.Helper()
	orig := nestingCheck
	nestingCheck = func() error { return err }
	t.Cleanup(func() { nestingCheck = orig })
}

func TestResetCmd_RefusesWhenNested(t *testing.T) {
	isolateLoomEnv(t)
	stubNesting(t, &tmux.NestedError{Session: "loom_term_x"})
	t.Cleanup(func() { resetForceFlag = false })

	rootCmd.SetArgs([]string{"reset", "--force"})
	err := rootCmd.Execute()

	var nested *tmux.NestedError
	require.ErrorAs(t, err, &nested)
	assert.Equal(t, "loom_term_x", nested.Session)
}

func TestRootCmd_RefusesWhenNested(t *testing.T) {
	isolateLoomEnv(t)
	stubNesting(t, &tmux.NestedError{Session: "loom_agent"})
	t.Cleanup(func() { workspaceFlag = "" })

	rootCmd.SetArgs([]string{"--workspace", "__loomtest_missing__"})
	err := rootCmd.Execute()

	var nested *tmux.NestedError
	require.ErrorAs(t, err, &nested, "the guard must run before workspace resolution")
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	require.NoError(t, w.Close())
	os.Stdout = orig
	return <-done
}

func TestDebugCmd_ReportsIsolationKnobs(t *testing.T) {
	isolateLoomEnv(t)
	t.Setenv(tmux.EnvTmuxSocket, "loomdev-probe")
	stubNesting(t, nil)

	out := captureStdout(t, func() {
		rootCmd.SetArgs([]string{"debug"})
		require.NoError(t, rootCmd.Execute())
	})

	assert.Contains(t, out, "Tmux socket: loomdev-probe")
	assert.Contains(t, out, "Global dir: "+os.Getenv(config.EnvGlobalDir))
	assert.Contains(t, out, "Nesting guard: ok")
}
