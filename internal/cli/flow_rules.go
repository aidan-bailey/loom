package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
	"github.com/aidan-bailey/loom/internal/flowrun"
)

// envRulesFromDirs makes Claude load each --add-dir directory's CLAUDE.md
// at startup (probed on Claude Code 2.1.323, 2026-11-14: a later Read
// under the directory loads it no second time).
const envRulesFromDirs = "CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD"

// agentRules is the package rules an agent run in cwd pre-loads for sc
// (package rules spec §2.1): every directory below cwd that holds a
// CLAUDE.md, on the way up from one of sc's files or the files
// sc.Base..sc.Head changed, sorted, and those files' total size. cwd's
// own CLAUDE.md loads anyway, and a path that leaves the tree adds
// nothing. A diff git can't give is an error: the step fails rather than
// run the agent without its rules.
func agentRules(cwd string, sc flowrun.Scope) ([]string, int, error) {
	files := slices.Clone(sc.Files)
	if sc.Base != "" {
		var out, errOut bytes.Buffer
		cmd := internalexec.GitCommand(context.Background(), cwd, "diff", "--name-only", "-z", sc.Base+".."+sc.Head)
		cmd.Stdout, cmd.Stderr = &out, &errOut
		if err := cmd.Run(); err != nil {
			return nil, 0, fmt.Errorf("git diff %s..%s: %v: %s", sc.Base, sc.Head, err, strings.TrimSpace(errOut.String()))
		}
		for _, f := range strings.Split(out.String(), "\x00") {
			if f != "" {
				files = append(files, f)
			}
		}
	}
	seen := map[string]bool{}
	var dirs []string
	size := 0
	for _, f := range files {
		rel := path.Clean(filepath.ToSlash(f))
		if path.IsAbs(rel) || filepath.IsAbs(f) || rel == ".." || strings.HasPrefix(rel, "../") {
			continue
		}
		for d := path.Dir(rel); d != "." && d != "/"; d = path.Dir(d) {
			if seen[d] {
				break // its parents were walked with it
			}
			seen[d] = true
			dir := filepath.Join(cwd, filepath.FromSlash(d))
			info, err := os.Stat(filepath.Join(dir, "CLAUDE.md"))
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			dirs = append(dirs, dir)
			size += int(info.Size())
		}
	}
	slices.Sort(dirs)
	return dirs, size, nil
}
