package flowview

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	internalexec "github.com/aidan-bailey/loom/internal/exec"
)

// profileFile is a worktree's flow profile, below its top level.
const profileFile = ".claude/flow.toml"

// defaultDrafts is where /flow:draft keeps a plan's drafts when the profile
// sets no paths.drafts (flowconf.DEFAULT_DRAFTS), below the top level.
const defaultDrafts = ".flow/drafts"

var (
	tomlTable = regexp.MustCompile(`^\s*\[\s*([A-Za-z0-9_.-]+)\s*\]\s*$`)
	tomlPath  = regexp.MustCompile(`^\s*(paths\.)?(state|drafts)\s*=\s*(.*?)\s*$`)
)

// profilePaths reads paths.state and paths.drafts from top's
// .claude/flow.toml without a TOML library: a [paths] table's or a dotted
// paths.<key>'s basic ("…", with no escape) or literal ('…') one-line
// string. Any other form of either key is refused by name, so a reader
// never guesses a directory. No profile is no paths.
func profilePaths(top string) (state, drafts string, err error) {
	path := filepath.Join(top, profileFile)
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	table := ""
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if m := tomlTable.FindStringSubmatch(line); m != nil {
			table = m[1]
			continue
		}
		m := tomlPath.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		dotted := m[1] != ""
		if (table == "" && !dotted) || (table == "paths" && dotted) || (table != "" && table != "paths") {
			continue
		}
		value, ok := tomlString(m[3])
		if !ok {
			return "", "", fmt.Errorf("%s: paths.%s: only a one-line string is read here", path, m[2])
		}
		if m[2] == "state" {
			state = value
		} else {
			drafts = value
		}
	}
	if err := sc.Err(); err != nil {
		return "", "", fmt.Errorf("%s: %w", path, err)
	}
	return state, drafts, nil
}

// tomlString is a basic ("…", no escape) or literal ('…') one-line
// string, with any trailing # comment after it.
func tomlString(s string) (string, bool) {
	if len(s) < 2 || strings.HasPrefix(s, `"""`) || strings.HasPrefix(s, `'''`) {
		return "", false
	}
	q := s[0]
	if q != '"' && q != '\'' {
		return "", false
	}
	end := strings.IndexByte(s[1:], q)
	if end < 0 {
		return "", false
	}
	value, rest := s[1:1+end], strings.TrimSpace(s[2+end:])
	if q == '"' && strings.Contains(value, `\`) {
		return "", false
	}
	if rest != "" && !strings.HasPrefix(rest, "#") {
		return "", false
	}
	return value, true
}

// stateDir is where the worktree's runs live: paths.state (made absolute
// against top, ~ expanded), else <git-dir>/flow (flowconf.state_dir).
func stateDir(top, state string) (string, error) {
	if state != "" {
		if rest, ok := strings.CutPrefix(state, "~"); ok && (rest == "" || rest[0] == '/') {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			state = home + rest
		}
		if !filepath.IsAbs(state) {
			state = filepath.Join(top, state)
		}
		return filepath.Clean(state), nil
	}
	dir, err := gitOut(top, "rev-parse", "--path-format=absolute", "--git-dir")
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "flow"), nil
}

// draftsDir is the plan stem's drafts dir: <top>/<paths.drafts, else
// .flow/drafts>/<stem> (flowconf.drafts_dir).
func draftsDir(top, drafts, stem string) string {
	if drafts == "" {
		drafts = defaultDrafts
	}
	return filepath.Join(top, drafts, stem)
}

// gitOut is git's stdout in dir, trimmed, or an error with its stderr.
func gitOut(dir string, args ...string) (string, error) {
	out, err := gitRaw(dir, args...)
	return strings.TrimSpace(out), err
}

// gitRaw is git's stdout in dir as it printed it, or an error with its
// stderr.
func gitRaw(dir string, args ...string) (string, error) {
	cmd := internalexec.GitCommand(context.Background(), dir, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s in %s: %v: %s", strings.Join(args, " "), dir, err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}
