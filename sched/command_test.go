package sched

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckCommand(t *testing.T) {
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "my tool"), []byte("#!/bin/sh\n"), 0o755))
	cwd := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "run me.sh"), []byte("#!/bin/sh\n"), 0o755))
	env := []string{"PATH=" + bin}
	cases := []struct {
		name    string
		argv    []string
		refused bool
	}{
		{"a command line as one word", []string{"nixos-rebuild build --flake .#fresco"}, true},
		{"a tab", []string{"cargo\tbuild"}, true},
		{"separate words", []string{"nixos-rebuild", "build"}, false},
		{"a program with a space on PATH", []string{"my tool"}, false},
		{"a file with a space in the job's folder", []string{"./run me.sh"}, false},
		{"a missing path with a space", []string{"./no such.sh"}, true},
		{"a missing command with no space", []string{"not-built-yet"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := CheckCommand(c.argv, env, cwd)
			assert.Equal(t, c.refused, err != nil, "%v", err)
		})
	}
	err := CheckCommand([]string{"nixos-rebuild build --flake .#fresco"}, env, cwd)
	require.Error(t, err)
	assert.Equal(t, `the command is one word with spaces ("nixos-rebuild build --flake .#fresco"): pass its words as separate arguments (-- nixos-rebuild build --flake .#fresco), or run it through a shell (-- sh -c '…')`, err.Error())
}
