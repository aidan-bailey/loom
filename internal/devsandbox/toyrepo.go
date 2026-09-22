package devsandbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
)

type toyCommit struct {
	msg   string
	files map[string]string
}

var toyHistory = []toyCommit{
	{"chore: initial commit", map[string]string{
		".gitignore": ".loom/\n",
		"README.md":  "# toy\n\nThe loom dev sandbox's workspace repo.\n",
	}},
	{"feat: add hello program", map[string]string{
		"go.mod":  "module toy\n\ngo 1.23\n",
		"main.go": "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hello from toy\")\n}\n",
	}},
	{"docs: add notes", map[string]string{
		"docs/notes.md": "# Notes\n\n## Why\n\nMarkdown for the workbench's markdown and review tabs.\n\n## Todo\n\n- nothing yet\n",
	}},
}

// initToyRepo creates the sandbox workspace repo with a short history,
// pushed to a bare origin. A repoDir that already has a complete repo
// (resolves origin/HEAD) is left untouched; one that has a .git but never
// finished (a step after `git init` failed partway) is reported as an
// error rather than silently skipped — there is no self-healing, so
// `loomdev down` + `loomdev up` is required to recreate it.
func initToyRepo(repoDir, originDir string) error {
	if _, err := os.Stat(filepath.Join(repoDir, ".git")); err == nil {
		if err := git(repoDir, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/HEAD"); err != nil {
			return fmt.Errorf("toy repo %s is incomplete (no origin/HEAD); run `loomdev down` and `loomdev up` to recreate it", repoDir)
		}
		return nil
	}
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		return err
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.name", "loom dev sandbox"},
		{"config", "user.email", "loomdev@example.invalid"},
		{"config", "commit.gpgsign", "false"},
	} {
		if err := git(repoDir, args...); err != nil {
			return err
		}
	}
	for _, c := range toyHistory {
		for rel, body := range c.files {
			path := filepath.Join(repoDir, rel)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				return err
			}
		}
		if err := git(repoDir, "add", "-A"); err != nil {
			return err
		}
		if err := git(repoDir, "commit", "-q", "--no-verify", "-m", c.msg); err != nil {
			return err
		}
	}
	if err := git(filepath.Dir(originDir), "init", "-q", "--bare", originDir); err != nil {
		return err
	}
	for _, args := range [][]string{
		{"remote", "add", "origin", originDir},
		{"push", "-q", "--no-verify", "-u", "origin", "main"},
		{"remote", "set-head", "origin", "main"},
	} {
		if err := git(repoDir, args...); err != nil {
			return err
		}
	}
	return nil
}

func git(dir string, args ...string) error {
	out, err := internalexec.GitCommand(context.Background(), dir, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return nil
}
