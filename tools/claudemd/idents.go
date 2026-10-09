package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// allowFile lists tokens neither identifier check may flag, one per line as
// "token  # reason". Only negative claims belong there (a rule saying
// something must not exist, or no longer does): anything else in it is
// hiding a stale rule.
const allowFile = "tools/claudemd/idents.allow"

var (
	symbolLike = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*(\(\))?$`)
	testName   = regexp.MustCompile(`^Test[A-Z_][A-Za-z0-9_]*$`)
	testFunc   = regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]*)\(`)
	fileSymbol = regexp.MustCompile(`^(.+\.[A-Za-z]+):([A-Za-z_][A-Za-z0-9_.]*)$`)
	fileLine   = regexp.MustCompile(`^(.+\.[A-Za-z]+):[0-9]+$`)
	pkgSymbol  = regexp.MustCompile(`^(.*/[A-Za-z0-9_-]+)\.([A-Z][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*)(?:\(\))?$`)
	word       = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
)

// sourceExts are the files a symbol may be found in. Markdown is never one,
// so a doc cannot confirm a symbol from another doc.
var sourceExts = map[string]bool{
	".go": true, ".lua": true, ".nix": true, ".yml": true, ".yaml": true,
	".toml": true, ".sh": true, ".json": true, ".mod": true,
}

// pathExts make a token without a slash a file path.
var pathExts = map[string]bool{
	".go": true, ".lua": true, ".md": true, ".nix": true, ".sh": true,
	".toml": true, ".yml": true, ".yaml": true, ".mod": true,
}

// index is what the docs' tokens are checked against.
type index struct {
	words  map[string]map[string]bool // word -> dirs of the source files holding it
	goDirs map[string]bool            // base names of the dirs holding Go files
	tests  map[string]bool            // Go test functions declared anywhere
}

// buildIndex reads the repo's source files: testdata (payloads rules quote)
// and .github (workflows) included, vendor/ and every other dot-dir not.
func buildIndex(root string) (*index, error) {
	idx := &index{words: map[string]map[string]bool{}, goDirs: map[string]bool{}, tests: map[string]bool{}}
	skip := func(name string) bool {
		return name == "vendor" || (strings.HasPrefix(name, ".") && name != ".github")
	}
	err := walk(root, skip, func(rel string) error {
		if !sourceExts[path.Ext(rel)] {
			return nil
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return err
		}
		dir := path.Dir(rel)
		if path.Ext(rel) == ".go" {
			idx.goDirs[path.Base(dir)] = true
		}
		if strings.HasSuffix(rel, "_test.go") {
			for _, m := range testFunc.FindAllStringSubmatch(string(data), -1) {
				idx.tests[m[1]] = true
			}
		}
		for _, w := range word.FindAllString(string(data), -1) {
			if idx.words[w] == nil {
				idx.words[w] = map[string]bool{}
			}
			idx.words[w][dir] = true
		}
		return nil
	})
	return idx, err
}

// idents reports, as advisories, each backticked symbol in the docs that no
// source file holds.
func idents(root string) ([]problem, error) {
	claude, err := claudeFiles(root)
	if err != nil {
		return nil, err
	}
	docs, _, err := docSet(root, claude)
	if err != nil {
		return nil, err
	}
	allow, _, err := loadAllow(root)
	if err != nil {
		return nil, err
	}
	idx, err := buildIndex(root)
	if err != nil {
		return nil, err
	}
	var ps []problem
	for _, f := range docs {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		if err != nil {
			return nil, err
		}
		ps = append(ps, identProblems(root, f, data, allow, idx, false)...)
	}
	sortProblems(ps)
	return ps, nil
}

// identProblems judges every code span outside fenced blocks in doc f and
// returns the strong findings (strong) or the advisory ones (!strong).
func identProblems(root, f string, data []byte, allow map[string]bool, idx *index, strong bool) []problem {
	var ps []problem
	mdLines(data, func(n int, line string) {
		for _, m := range backticked.FindAllStringSubmatch(line, -1) {
			t := strings.TrimPrefix(m[1], "./")
			if allow[m[1]] || skipToken(t) {
				continue
			}
			if why, isStrong := judge(root, f, t, idx); why != "" && isStrong == strong {
				ps = append(ps, problem{f, n, m[1] + ": " + why})
			}
		}
	})
	return ps
}

// skipToken says a token is neither a repo path nor a symbol: too short;
// prose or a command (whitespace); a placeholder, key chord or quoted
// string; a flag, absolute path or heading; a URL or a Go package pattern.
func skipToken(t string) bool {
	return len(t) < 4 ||
		strings.ContainsAny(t, " \t<>{}[]$~=|'\",;!?@+%") ||
		strings.HasPrefix(t, "-") || strings.HasPrefix(t, "/") || strings.HasPrefix(t, "#") ||
		strings.Contains(t, "://") || strings.Contains(t, "...")
}

// judge checks token t from doc f. It returns why t can't be found ("" when
// it can be, or when t is neither a repo path nor a symbol) and whether that
// is a strong (structural) finding or an advisory one.
func judge(root, f, t string, idx *index) (string, bool) {
	if m := fileLine.FindStringSubmatch(t); m != nil {
		t = m[1]
	}
	switch {
	case strings.Contains(t, "…"):
		return judgeTestFamily(t, idx), true
	case testName.MatchString(t):
		if !idx.tests[t] {
			return "no Go test function has this name", true
		}
		return "", true
	case fileSymbol.MatchString(t):
		return judgeFileSymbol(root, f, t), true
	case strings.Contains(t, "*"):
		return judgeGlob(root, f, t), true
	case strings.HasPrefix(t, "_") && !strings.Contains(t, "/") && pathExts[path.Ext(t)]:
		return "", true // a file-name suffix (_test.go, _unix.go) describes files rather than naming one
	case pkgSymbol.MatchString(t):
		return judgePkgSymbol(root, f, t, idx)
	case strings.Contains(t, "/") || pathExts[path.Ext(t)]:
		return judgePath(root, f, t), true
	default:
		return judgeSymbol(t, idx), false
	}
}

// judgeTestFamily checks "TestFoo_…", which names a family of tests: one
// must exist. Other tokens holding "…" are left alone.
func judgeTestFamily(t string, idx *index) string {
	prefix := strings.TrimSuffix(t, "…")
	if !strings.HasPrefix(t, "Test") || prefix == t || strings.Contains(prefix, "…") {
		return ""
	}
	for name := range idx.tests {
		if strings.HasPrefix(name, prefix) {
			return ""
		}
	}
	return "no Go test function starts with " + prefix
}

// judgeFileSymbol checks "file.go:Symbol": the file resolves and holds the
// symbol's last segment as a whole word.
func judgeFileSymbol(root, f, t string) string {
	m := fileSymbol.FindStringSubmatch(t)
	p := resolve(root, f, m[1])
	if p == "" {
		return "path not found"
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
	if err != nil {
		return "path not found"
	}
	sym := m[2][strings.LastIndex(m[2], ".")+1:]
	if !regexp.MustCompile(`\b` + regexp.QuoteMeta(sym) + `\b`).Match(data) {
		return fmt.Sprintf("%s is not in %s", sym, p)
	}
	return ""
}

// judgeGlob checks a path glob ("app/state_*.go") matches a file. A bare
// pattern ("*_test.go") describes files rather than naming them.
func judgeGlob(root, f, t string) string {
	if !strings.Contains(t, "/") {
		return ""
	}
	for _, base := range []string{path.Dir(f), "."} {
		if ms, _ := filepath.Glob(filepath.Join(root, filepath.FromSlash(path.Join(base, t)))); len(ms) > 0 {
			return ""
		}
	}
	if repoAnchored(root, f, t) {
		return "glob matches no file"
	}
	return ""
}

// judgePath checks a repo path resolves beside the doc or from the root.
func judgePath(root, f, t string) string {
	if resolve(root, f, t) != "" {
		return ""
	}
	if !strings.Contains(t, "/") {
		return "no such file beside this doc or at the root: write the path from the repo root"
	}
	if repoAnchored(root, f, t) {
		return "path not found"
	}
	return "" // a runtime path (logs/serve.log, worktrees/), not the repo's
}

// judgePkgSymbol checks "dir/pkg.Symbol", a symbol qualified by its package
// dir rather than a file path. The dir must exist, which is a claim about the
// repo and so strong; the symbol must appear in a source file of that dir,
// which is advisory like any other symbol.
func judgePkgSymbol(root, f, t string, idx *index) (string, bool) {
	m := pkgSymbol.FindStringSubmatch(t)
	dir := resolve(root, f, m[1])
	if dir == "" {
		if repoAnchored(root, f, m[1]) {
			return "path not found", true
		}
		return "", true // an import path or runtime path, not the repo's
	}
	sym := m[2][strings.LastIndex(m[2], ".")+1:]
	if len(sym) < 4 {
		return "", false
	}
	dirs := idx.words[sym]
	switch {
	case len(dirs) == 0:
		return "not found in any source file (Markdown excluded)", false
	case !dirs[dir]:
		return fmt.Sprintf("found, but not in %s: check where the rule says it lives", dir), false
	}
	return "", false
}

// repoAnchored says whether a slashed path's first segment exists beside
// the doc or at the root, which makes it a claim about the repo rather than
// a runtime path.
func repoAnchored(root, f, t string) bool {
	first := strings.SplitN(strings.TrimSuffix(t, "/"), "/", 2)[0]
	return exists(root, first) || exists(root, path.Join(path.Dir(f), first))
}

// judgeSymbol checks a symbol's last segment (four characters or more)
// appears as a whole word in some source file and, when its first segment
// is the name of a Go package dir, in such a dir.
func judgeSymbol(t string, idx *index) string {
	if !symbolLike.MatchString(t) || !looksLikeSymbol(t) {
		return ""
	}
	parts := strings.Split(strings.TrimSuffix(t, "()"), ".")
	last := parts[len(parts)-1]
	if len(last) < 4 {
		return ""
	}
	dirs := idx.words[last]
	if len(dirs) == 0 {
		return "not found in any source file (Markdown excluded)"
	}
	if len(parts) > 1 && idx.goDirs[parts[0]] {
		for d := range dirs {
			if path.Base(d) == parts[0] {
				return ""
			}
		}
		return fmt.Sprintf("found, but not in a %s package dir: check where the rule says it lives", parts[0])
	}
	return ""
}

// looksLikeSymbol rules out plain lower-case words (vendor, serve), which
// say nothing checkable.
func looksLikeSymbol(t string) bool {
	return strings.ContainsAny(t, "._") || strings.HasSuffix(t, "()") || strings.ToLower(t) != t
}

// resolve finds rel beside the doc f, then from the root, and returns its
// slash path from the root, or "".
func resolve(root, f, rel string) string {
	rel = strings.TrimSuffix(rel, "/")
	for _, p := range []string{path.Join(path.Dir(f), rel), path.Clean(rel)} {
		if exists(root, p) {
			return p
		}
	}
	return ""
}

// loadAllow reads allowFile; an entry without a reason is a problem.
func loadAllow(root string) (map[string]bool, []problem, error) {
	allow := map[string]bool{}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(allowFile)))
	if os.IsNotExist(err) {
		return allow, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var ps []problem
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		token, reason, _ := strings.Cut(line, "#")
		if strings.TrimSpace(reason) == "" {
			ps = append(ps, problem{allowFile, i + 1, "allowed token with no reason"})
		}
		allow[strings.TrimSpace(token)] = true
	}
	return allow, ps, nil
}
