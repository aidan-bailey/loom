package flow

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWrite_WritesThePluginAndToolsButNoTests(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "flow")
	require.NoError(t, Write(dir))

	assert.True(t, IsRoot(dir))
	assert.FileExists(t, filepath.Join(dir, "plugin", ".claude-plugin", "plugin.json"), "all: keeps the dot-directory")
	assert.FileExists(t, filepath.Join(dir, "plugin", "agents", "verifier.md"))
	assert.FileExists(t, filepath.Join(dir, "plugin", "skills", "supervise", "SKILL.md"))
	assert.FileExists(t, filepath.Join(dir, "lib", "flowconf.py"))
	assert.FileExists(t, filepath.Join(dir, "lib", "guarded-agents.txt"))
	assert.NoFileExists(t, filepath.Join(dir, "lib", "flowconf_test.py"))
	roster, err := os.ReadFile(filepath.Join(dir, RosterFile))
	require.NoError(t, err)
	assert.Equal(t, RosterID()+"\n", string(roster))
	assert.NoDirExists(t, dir+".partial")
}

func TestWrite_RefusesADirThatExistsAndLeavesIt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "flow")
	require.NoError(t, Write(dir))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "mine"), []byte("x"), 0o600))

	assert.Error(t, Write(dir))
	assert.FileExists(t, filepath.Join(dir, "mine"), "a refused write changes nothing")
	assert.NoDirExists(t, dir+".partial")
}

func TestRosterID_ChangesWithAPluginFileAndNotWithLib(t *testing.T) {
	base := fstest.MapFS{
		"plugin/agents/a.md": {Data: []byte("one")},
		"lib/x.py":           {Data: []byte("x")},
	}
	edited := fstest.MapFS{
		"plugin/agents/a.md": {Data: []byte("two")},
		"lib/x.py":           {Data: []byte("x")},
	}
	libOnly := fstest.MapFS{
		"plugin/agents/a.md": {Data: []byte("one")},
		"lib/x.py":           {Data: []byte("y")},
	}
	assert.Regexp(t, `^loom flow roster [0-9a-f]{12}$`, rosterOf(base))
	assert.NotEqual(t, rosterOf(base), rosterOf(edited))
	assert.Equal(t, rosterOf(base), rosterOf(libOnly))
	assert.Equal(t, rosterOf(files), RosterID())
}

func TestGuarded_TheShippedListParsesAndNamesRealAgents(t *testing.T) {
	g, err := Guarded()
	require.NoError(t, err)
	assert.Equal(t, Developer, g["intermediate-dev"])
	assert.Equal(t, Drafter, g["drafter"])
	for name := range g {
		_, err := fs.Stat(files, "plugin/agents/"+name+".md")
		assert.NoError(t, err, name)
	}
}

func TestParseGuarded_RefusesABadLine(t *testing.T) {
	for _, line := range []string{"dev", "dev reviewer", "dev developer extra"} {
		_, err := parseGuarded(fstest.MapFS{"lib/guarded-agents.txt": {Data: []byte("# c\n" + line + "\n")}})
		assert.ErrorContains(t, err, "guarded-agents.txt:2", line)
	}
}

func TestMode_Guards(t *testing.T) {
	assert.True(t, Developer.Guards("Bash"))
	assert.False(t, Developer.Guards("Write"))
	for _, tool := range []string{"Bash", "Write", "Edit"} {
		assert.True(t, Drafter.Guards(tool), tool)
	}
	assert.False(t, Drafter.Guards("Read"))
}

func TestGuardedMode_MatchesTheAgentTypeTheProbeSaw(t *testing.T) {
	mode, ok := GuardedMode(probedPrefix + "junior-dev")
	assert.True(t, ok)
	assert.Equal(t, Developer, mode)
	_, ok = GuardedMode(probedPrefix + "verifier")
	assert.False(t, ok, "an unguarded flow agent")
	_, ok = GuardedMode("")
	assert.False(t, ok, "the main thread")
	if probedPrefix != "" {
		_, ok = GuardedMode("junior-dev")
		assert.False(t, ok, "a bare name is another plugin's or the home install's agent")
	}
	assert.True(t, strings.HasSuffix(probedPrefix, ":") || probedPrefix == "")
}
