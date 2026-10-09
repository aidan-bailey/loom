package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
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
	fileSymbol = regexp.MustCompile(`^(.+\.[A-Za-z]+):([A-Za-z_][A-Za-z0-9_.]*)$`)
	// fileLine matches a file with the line or range a doc points at:
	// file.go:3, file.go:3-4, file.go:3:5 (line:column), file.go#L3, file.go#L3-L5.
	fileLine  = regexp.MustCompile(`^(.+\.[A-Za-z]+)(?::[0-9]+(?:[-:][0-9]+)?|#L[0-9]+(?:-L?[0-9]+)?)$`)
	pkgSymbol = regexp.MustCompile(`^(.*/[A-Za-z0-9_-]+)\.([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*)(?:\(\))?$`)
	word      = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
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
	tests  map[string]bool            // Go test functions declared in _test.go files
}

// buildIndex reads the repo's source files: testdata (payloads rules quote)
// and .github (workflows) included, vendor/ and every other dot-dir not.
//
// A Go file is read as Go, not as text, so a name that only sits in a fixture
// can't confirm a symbol. Its identifiers and comments count; its string and
// character literals count only outside _test.go files, since a test's
// literals hold fixtures and forbidden-name lists. Test functions are the
// declarations parser finds, never a "func TestX(" inside a string or comment.
// Other source files are read as plain words.
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
		add := func(w string) {
			if idx.words[w] == nil {
				idx.words[w] = map[string]bool{}
			}
			idx.words[w][dir] = true
		}
		addWords := func(text string) {
			for _, w := range word.FindAllString(text, -1) {
				add(w)
			}
		}
		if path.Ext(rel) != ".go" {
			addWords(string(data))
			return nil
		}
		idx.goDirs[path.Base(dir)] = true
		isTest := strings.HasSuffix(rel, "_test.go")
		if isTest {
			declaredTests(rel, data, idx.tests)
		}
		var sc scanner.Scanner
		fset := token.NewFileSet()
		sc.Init(fset.AddFile(rel, -1, len(data)), data, nil, scanner.ScanComments)
		for {
			_, tok, lit := sc.Scan()
			switch {
			case tok == token.EOF:
				return nil
			case tok == token.IDENT:
				add(lit)
			case tok == token.COMMENT:
				addWords(lit)
			case (tok == token.STRING || tok == token.CHAR) && !isTest:
				if s, err := strconv.Unquote(lit); err == nil {
					lit = s
				}
				addWords(lit)
			}
		}
	})
	return idx, err
}

// declaredTests adds to tests the top-level functions of a _test.go file
// named Test*. A file that doesn't parse still yields the declarations before
// its first error.
func declaredTests(rel string, data []byte, tests map[string]bool) {
	file, _ := parser.ParseFile(token.NewFileSet(), rel, data, parser.SkipObjectResolution)
	if file == nil {
		return
	}
	for _, d := range file.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil && strings.HasPrefix(fn.Name.Name, "Test") {
			tests[fn.Name.Name] = true
		}
	}
}

// idents reports, as advisories, each backticked symbol in the docs that no
// source file holds.
func idents(root string) ([]problem, error) {
	found, err := findDocs(root)
	if err != nil {
		return nil, err
	}
	docs, _, err := docSet(root, found)
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
		ps = append(ps, identProblems(root, f, data, allow, nil, idx, false)...)
	}
	sortProblems(ps)
	return ps, nil
}

// identProblems judges every code span outside fenced blocks in doc f and
// returns the strong findings (strong) or the advisory ones (!strong). An
// allow-listed token is not returned; when it would have been, of either
// kind, it is recorded in used (which may be nil) so a stale entry shows.
func identProblems(root, f string, data []byte, allow map[string]int, used map[string]bool, idx *index, strong bool) []problem {
	var ps []problem
	mdLines(data, func(n int, line string) {
		for _, m := range backticked.FindAllStringSubmatch(line, -1) {
			t := strings.TrimPrefix(m[1], "./")
			if skipToken(t) {
				continue
			}
			why, isStrong := judge(root, f, t, idx)
			if why == "" {
				continue
			}
			if _, ok := allow[m[1]]; ok {
				if used != nil {
					used[m[1]] = true
				}
				continue
			}
			if isStrong == strong {
				ps = append(ps, problem{f, n, m[1] + ": " + why})
			}
		}
	})
	return ps
}

// skipToken says a token is neither a repo path nor a symbol: too short;
// prose or a command (whitespace); a placeholder, key chord or quoted
// string; a flag, absolute path or heading; a URL or a Go package pattern;
// a path in .git, which is a file, not a dir, in a worktree.
func skipToken(t string) bool {
	return len(t) < 4 ||
		strings.ContainsAny(t, " \t<>{}[]$~=|'\",;!?@+%") ||
		strings.HasPrefix(t, "-") || strings.HasPrefix(t, "/") || strings.HasPrefix(t, "#") ||
		strings.Contains(t, "://") || strings.Contains(t, "...") ||
		strings.HasPrefix(t, ".git/")
}

// judge checks token t from doc f. It returns why t can't be found ("" when
// it can be, or when t is neither a repo path nor a symbol) and whether that
// is a strong (structural) finding or an advisory one.
func judge(root, f, t string, idx *index) (string, bool) {
	if m := fileLine.FindStringSubmatch(t); m != nil {
		t = m[1]
	}
	pathy := strings.Contains(t, "/") || pathExts[path.Ext(t)]
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
	case pathy && resolve(root, f, t) != "":
		return "", true // whatever its extension, a path that exists is not a symbol
	}
	if why, strong, ok := judgePkgSymbol(root, f, t, idx); ok {
		return why, strong
	}
	if pathy {
		return judgePath(root, f, t), true
	}
	return judgeSymbol(t, idx), false
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
// dir rather than a file path, when the part before the last dot is a dir.
// ok is false when t is not one (the dir doesn't exist, or what follows the
// dot is a file extension like .go), and the caller judges it as a path. The
// symbol, upper or lower case, must appear in a source file of that dir; that
// is advisory like any other symbol.
func judgePkgSymbol(root, f, t string, idx *index) (why string, strong, ok bool) {
	m := pkgSymbol.FindStringSubmatch(t)
	if m == nil || pathExts["."+m[2]] {
		return "", false, false
	}
	dir := resolveDir(root, f, m[1])
	if dir == "" {
		return "", false, false
	}
	sym := m[2][strings.LastIndex(m[2], ".")+1:]
	if len(sym) < 4 {
		return "", false, true
	}
	dirs := idx.words[sym]
	switch {
	case len(dirs) == 0:
		return "not found in any source file (Markdown excluded)", false, true
	case !dirs[dir]:
		return fmt.Sprintf("found, but not in %s: check where the rule says it lives", dir), false, true
	}
	return "", false, true
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

// resolveDir is resolve for a dir: it finds rel beside the doc f, then from
// the root, and returns its slash path from the root only if it is a dir.
func resolveDir(root, f, rel string) string {
	rel = strings.TrimSuffix(rel, "/")
	for _, p := range []string{path.Join(path.Dir(f), rel), path.Clean(rel)} {
		if fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err == nil && fi.IsDir() {
			return p
		}
	}
	return ""
}

// loadAllow reads allowFile into the line each token is allowed on; an entry
// without a reason is a problem.
func loadAllow(root string) (map[string]int, []problem, error) {
	allow := map[string]int{}
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
		token = strings.TrimSpace(token)
		if _, dup := allow[token]; !dup {
			allow[token] = i + 1
		}
	}
	return allow, ps, nil
}
