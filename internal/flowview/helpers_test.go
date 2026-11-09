package flowview

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/internal/flowrun"
	"github.com/stretchr/testify/require"
)

// gitRepo is a temp repository with one commit on main, its path resolved.
func gitRepo(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	git(t, dir, "init", "-q", "-b", "main")
	commitFile(t, dir, "README.md", "# repo\n")
	return dir
}

// git runs git in dir, failing the test on an error, and returns its
// output trimmed.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)
	cmd := internalexec.GitCommand(context.Background(), dir, full...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

// writeFile writes text at dir/path, making its parents.
func writeFile(t *testing.T, dir, path, text string) {
	t.Helper()
	p := filepath.Join(dir, path)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(text), 0o644))
}

// commitFile writes, adds and commits one file.
func commitFile(t *testing.T, dir, path, text string) {
	t.Helper()
	writeFile(t, dir, path, text)
	git(t, dir, "add", "--", path)
	git(t, dir, "commit", "-q", "-m", "add "+path, "--", path)
}

// head is dir's HEAD.
func head(t *testing.T, dir string) string { return git(t, dir, "rev-parse", "HEAD") }

// flowStateDir is dir's default state dir, <git-dir>/flow.
func flowStateDir(t *testing.T, dir string) string {
	t.Helper()
	d, err := stateDir(dir, "")
	require.NoError(t, err)
	return d
}

// writeRun saves st in state's runs dir.
func writeRun(t *testing.T, state string, st *flowrun.State) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(state, "runs"), 0o700))
	require.NoError(t, flowrun.Save(filepath.Join(state, "runs", st.ID+".json"), st))
}

// skeleton is a design session's skeleton naming spec, with stories ids.
func skeleton(spec string, ids ...string) string {
	var b strings.Builder
	b.WriteString("# Plan\n\n**Goal:** a thing.\n\n**Spec:** `" + spec + "`\n\n## Stories\n\n")
	for _, id := range ids {
		b.WriteString("- **Story " + id + "** does a part.\n  **Reading:** spec §" + id + "\n")
	}
	b.WriteString("\n" + joinMarker + "\n")
	return b.String()
}

// joined is a skeleton with its stories joined below the marker, and a
// Design stage line when stage is set.
func joined(spec, stage string, ids ...string) string {
	text := skeleton(spec, ids...)
	if stage != "" {
		text = strings.Replace(text, "## Stories", "**Design stage:** "+stage+"\n\n## Stories", 1)
	}
	for _, id := range ids {
		text += "\n## Story " + id + ": part " + id + "\n\n- **Developer:** intermediate-dev\n"
	}
	return text
}
