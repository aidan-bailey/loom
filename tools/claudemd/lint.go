package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Budgets. A file over budget moves context into a docs/claude guide; the
// budget is not raised to fit.
const (
	rootMaxLines    = 150
	rootMaxBytes    = 16000
	packageMaxLines = 100
	packageMaxBytes = 20000
	maxLineBytes    = 1200 // one rule per line; a rule needing more links a guide
)

const (
	guidesDir      = "docs/claude"
	indexFile      = "docs/claude/INDEX.md"
	archFile       = "docs/ARCHITECTURE.md"
	skillsDir      = ".claude/skills"
	commandsDir    = ".claude/commands"
	skillsAnchor   = "- **Skills:**"
	commandsAnchor = "- **Commands:**"
	rulesHeading   = "## Rules"
)

// deferring matches phrases that send the reader elsewhere instead of
// stating the rule: to a file that doesn't auto-load, or to a passage that
// may now live in another file. Matched case-insensitively outside code, so
// a rule can still quote one.
var deferring = []*regexp.Regexp{
	regexp.MustCompile(`\bsee (the )?readme\b`),
	regexp.MustCompile(`\b(described|documented|explained) in (the )?readme\b`),
	regexp.MustCompile(`\bas (described |mentioned |noted )?(above|below)\b`),
	regexp.MustCompile(`\b(see|cf\.?) (above|below)\b`),
	regexp.MustCompile(`\bdescribed elsewhere\b`),
	regexp.MustCompile(`\bsee the (\S+ ){0,6}(gotcha|bullet)s?\b`),
	regexp.MustCompile(`\bsee (gotchas|testing patterns|key packages)\b`),
	regexp.MustCompile(`\b(the|this) (rule|section|gotcha|bullet|table) (above|below)\b`),
}

// config is what differs between the repo and a test's fixture tree.
type config struct {
	// exempt maps a Go package dir (slash path from the root) that needs no
	// CLAUDE.md to the reason. An entry whose dir holds no package is a
	// problem.
	exempt map[string]string
}

// repoConfig is loom's. Adding an exemption is a deliberate, reviewable act.
var repoConfig = config{exempt: map[string]string{
	"keys":             "key names and lookup maps; the rule about KeyForString lives in app/CLAUDE.md",
	"internal/testpty": "fake PTY pairs for tests; the rule about them lives in app/CLAUDE.md",
}}

var (
	codeSpan   = regexp.MustCompile("`[^`\n]*`")
	backticked = regexp.MustCompile("`([^`\n]+)`")
	mdLink     = regexp.MustCompile(`\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
)

// problem is one finding, printed as path:line: msg (path: msg for line 0).
type problem struct {
	path string // slash path from the repo root
	line int
	msg  string
}

func (p problem) String() string {
	if p.line > 0 {
		return fmt.Sprintf("%s:%d: %s", p.path, p.line, p.msg)
	}
	return fmt.Sprintf("%s: %s", p.path, p.msg)
}

// check runs every structural check over the repo at root.
func check(root string, cfg config) ([]problem, error) {
	claude, err := claudeFiles(root)
	if err != nil {
		return nil, err
	}
	docs, guides, err := docSet(root, claude)
	if err != nil {
		return nil, err
	}
	allow, ps, err := loadAllow(root)
	if err != nil {
		return nil, err
	}
	idx, err := buildIndex(root)
	if err != nil {
		return nil, err
	}
	linked := map[string][]string{} // doc -> the local files it links, slash paths from the root
	for _, f := range docs {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		if err != nil {
			return nil, err
		}
		if path.Base(f) == "CLAUDE.md" {
			ps = append(ps, budget(f, data)...)
			ps = append(ps, phrases(f, data)...)
			if f != "CLAUDE.md" {
				ps = append(ps, shape(f, data)...)
			}
		}
		targets, broken := links(root, f, data)
		linked[f] = targets
		ps = append(ps, broken...)
		ps = append(ps, identProblems(root, f, data, allow, idx, true)...)
	}
	cov, err := coverage(root, claude, cfg)
	if err != nil {
		return nil, err
	}
	ps = append(ps, cov...)
	ps = append(ps, orphans(claude, guides, linked)...)
	par, err := listParity(root)
	if err != nil {
		return nil, err
	}
	ps = append(ps, par...)
	sortProblems(ps)
	return ps, nil
}

// sortProblems orders problems by path, line and message.
func sortProblems(ps []problem) {
	sort.Slice(ps, func(i, j int) bool {
		if ps[i].path != ps[j].path {
			return ps[i].path < ps[j].path
		}
		if ps[i].line != ps[j].line {
			return ps[i].line < ps[j].line
		}
		return ps[i].msg < ps[j].msg
	})
}

// walk calls fn with the slash path of every file under root, skipping the
// dirs skip names. Root itself is never skipped.
func walk(root string, skip func(name string) bool, fn func(rel string) error) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && skip(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		return fn(filepath.ToSlash(rel))
	})
}

// ours skips what the go tool skips (testdata/, _ and dot dirs: .git,
// .claude, and .loom, whose worktrees are other checkouts of this repo) and
// vendor/ (third-party).
func ours(name string) bool {
	return name == "vendor" || name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

// claudeFiles returns every CLAUDE.md under root, the root's first.
func claudeFiles(root string) ([]string, error) {
	var out []string
	err := walk(root, ours, func(rel string) error {
		if path.Base(rel) == "CLAUDE.md" {
			out = append(out, rel)
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool {
		if out[i] == "CLAUDE.md" || out[j] == "CLAUDE.md" {
			return out[i] == "CLAUDE.md"
		}
		return out[i] < out[j]
	})
	return out, err
}

// guideFiles returns the Markdown files in docs/claude, INDEX.md included.
func guideFiles(root string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(guidesDir)))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			out = append(out, guidesDir+"/"+e.Name())
		}
	}
	return out, nil
}

// docSet returns the docs the checks read (every CLAUDE.md, the README.md
// beside each but the root's, which is for users, docs/ARCHITECTURE.md and
// the guides), and the guides alone.
func docSet(root string, claude []string) ([]string, []string, error) {
	docs := append([]string{}, claude...)
	for _, f := range claude {
		if d := path.Dir(f); d != "." && exists(root, d+"/README.md") {
			docs = append(docs, d+"/README.md")
		}
	}
	if exists(root, archFile) {
		docs = append(docs, archFile)
	}
	guides, err := guideFiles(root)
	if err != nil {
		return nil, nil, err
	}
	return append(docs, guides...), guides, nil
}

// limits returns f's line and byte budget.
func limits(f string) (int, int) {
	if f == "CLAUDE.md" {
		return rootMaxLines, rootMaxBytes
	}
	return packageMaxLines, packageMaxBytes
}

// lineCount counts lines, a final unterminated one included.
func lineCount(data []byte) int {
	n := bytes.Count(data, []byte("\n"))
	if len(data) > 0 && data[len(data)-1] != '\n' {
		n++
	}
	return n
}

// budget checks a CLAUDE.md against its line, byte and line-length budget.
func budget(f string, data []byte) []problem {
	maxLines, maxBytes := limits(f)
	var ps []problem
	if n := lineCount(data); n > maxLines {
		ps = append(ps, problem{f, 0, fmt.Sprintf("%d lines, over the %d-line budget: move context into a docs/claude guide", n, maxLines)})
	}
	if len(data) > maxBytes {
		ps = append(ps, problem{f, 0, fmt.Sprintf("%d bytes, over the %d-byte budget: move context into a docs/claude guide", len(data), maxBytes)})
	}
	for i, line := range strings.Split(string(data), "\n") {
		if len(line) > maxLineBytes {
			ps = append(ps, problem{f, i + 1, fmt.Sprintf("line of %d bytes, over the %d-byte line budget: move the rule's context into a docs/claude guide", len(line), maxLineBytes)})
		}
	}
	return ps
}

// headroom describes each CLAUDE.md's use of its budget, for -v.
func headroom(root string) ([]string, error) {
	claude, err := claudeFiles(root)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, f := range claude {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		if err != nil {
			return nil, err
		}
		maxLines, maxBytes := limits(f)
		out = append(out, fmt.Sprintf("%s: %d/%d lines, %d/%d bytes", f, lineCount(data), maxLines, len(data), maxBytes))
	}
	return out, nil
}

// mdLines calls fn with each line of a Markdown file outside fenced code
// blocks and its 1-based number.
func mdLines(data []byte, fn func(n int, line string)) {
	fenced := false
	for i, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			continue
		}
		if !fenced {
			fn(i+1, line)
		}
	}
}

// prose is mdLines with inline code spans blanked out.
func prose(data []byte, fn func(n int, line string)) {
	mdLines(data, func(n int, line string) { fn(n, codeSpan.ReplaceAllString(line, "``")) })
}

// phrases flags self-deferring phrases in a CLAUDE.md.
func phrases(f string, data []byte) []problem {
	var ps []problem
	prose(data, func(n int, line string) {
		lower := strings.ToLower(line)
		for _, re := range deferring {
			if m := re.FindString(lower); m != "" {
				ps = append(ps, problem{f, n, fmt.Sprintf("self-deferring phrase %q: state the rule inline", m)})
			}
		}
	})
	return ps
}

// shape checks a package CLAUDE.md's skeleton: its title names its dir, and
// every rule under its "## Rules" heading says what guards it.
func shape(f string, data []byte) []problem {
	var ps []problem
	want := "# " + path.Dir(f)
	if first, _, _ := strings.Cut(string(data), "\n"); strings.TrimSpace(first) != want {
		ps = append(ps, problem{f, 1, fmt.Sprintf("first line must be %q", want)})
	}
	inRules := false
	mdLines(data, func(n int, line string) {
		if strings.HasPrefix(line, "## ") {
			inRules = strings.HasPrefix(line, rulesHeading)
			return
		}
		if inRules && strings.HasPrefix(line, "- ") &&
			!strings.Contains(line, "**Enforced**") && !strings.Contains(line, "**Convention**") {
			ps = append(ps, problem{f, n, "rule without **Enforced** or **Convention**: say what guards it"})
		}
	})
	return ps
}

// links returns the local files a Markdown file links to (slash paths from
// the root) and a problem for each that doesn't exist. External links and
// pure anchors are skipped, an anchor on a local link is dropped, and a
// leading / means the repo root.
func links(root, f string, data []byte) ([]string, []problem) {
	var targets []string
	var ps []problem
	prose(data, func(n int, line string) {
		for _, m := range mdLink.FindAllStringSubmatch(line, -1) {
			t := m[1]
			if strings.Contains(t, "://") || strings.HasPrefix(t, "mailto:") || strings.HasPrefix(t, "#") {
				continue
			}
			if i := strings.Index(t, "#"); i >= 0 {
				t = t[:i]
			}
			rel := path.Clean(path.Join(path.Dir(f), t))
			if strings.HasPrefix(t, "/") {
				rel = path.Clean(strings.TrimPrefix(t, "/"))
			}
			if !exists(root, rel) {
				ps = append(ps, problem{f, n, fmt.Sprintf("broken link %q", m[1])})
				continue
			}
			targets = append(targets, rel)
		}
	})
	return targets, ps
}

// exists says whether rel, a slash path from root, exists.
func exists(root, rel string) bool {
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil
}

// packageDirs returns every dir below root holding a .go file, whatever its
// build tags (go list would drop e2e/, whose files are all tagged).
func packageDirs(root string) (map[string]bool, error) {
	pkgs := map[string]bool{}
	err := walk(root, ours, func(rel string) error {
		if strings.HasSuffix(rel, ".go") && path.Dir(rel) != "." {
			pkgs[path.Dir(rel)] = true
		}
		return nil
	})
	return pkgs, err
}

// coverage requires a CLAUDE.md in every Go package dir or in a parent of it
// below the root, since a nested CLAUDE.md applies to its whole subtree. The
// set of package dirs comes from the tree, so a new package without one fails
// by default; finding none at all fails closed.
func coverage(root string, claude []string, cfg config) ([]problem, error) {
	pkgs, err := packageDirs(root)
	if err != nil {
		return nil, err
	}
	if len(pkgs) == 0 {
		return []problem{{".", 0, "found no Go package dirs: coverage fails closed"}}, nil
	}
	have := map[string]bool{}
	for _, f := range claude {
		have[path.Dir(f)] = true
	}
	var ps []problem
	for dir := range cfg.exempt {
		if !pkgs[dir] {
			ps = append(ps, problem{dir, 0, "exempt from coverage, but no Go package lives here any more: drop the exemption"})
		}
	}
	for dir := range pkgs {
		if _, ok := cfg.exempt[dir]; ok {
			continue
		}
		covered := false
		for d := dir; d != "."; d = path.Dir(d) {
			if have[d] {
				covered = true
				break
			}
		}
		if !covered {
			ps = append(ps, problem{dir, 0, "Go package with no CLAUDE.md here or in a parent below the root"})
		}
	}
	return ps, nil
}

// orphans requires every guide but INDEX.md to be listed in INDEX.md and
// linked from at least one CLAUDE.md, and the root CLAUDE.md to link
// INDEX.md.
func orphans(claude, guides []string, linked map[string][]string) []problem {
	if len(guides) == 0 {
		return nil
	}
	hasIndex := false
	for _, g := range guides {
		hasIndex = hasIndex || g == indexFile
	}
	if !hasIndex {
		return []problem{{guidesDir, 0, "guides but no INDEX.md"}}
	}
	fromIndex, fromRules := map[string]bool{}, map[string]bool{}
	for _, t := range linked[indexFile] {
		fromIndex[t] = true
	}
	for _, f := range claude {
		for _, t := range linked[f] {
			fromRules[t] = true
		}
	}
	var ps []problem
	rootLinksIndex := false
	for _, t := range linked["CLAUDE.md"] {
		rootLinksIndex = rootLinksIndex || t == indexFile
	}
	if !rootLinksIndex {
		ps = append(ps, problem{"CLAUDE.md", 0, "doesn't link " + indexFile + ", so the guides can't be found from the file that always loads"})
	}
	for _, g := range guides {
		if g == indexFile {
			continue
		}
		if !fromIndex[g] {
			ps = append(ps, problem{g, 0, "not listed in " + indexFile})
		}
		if !fromRules[g] {
			ps = append(ps, problem{g, 0, "not linked from any CLAUDE.md rule"})
		}
	}
	return ps
}

// listParity compares the root CLAUDE.md's skills line with .claude/skills
// both ways, and its commands line with .claude/commands when that dir
// exists. Each comparison anchors on its one line, so a name mentioned
// elsewhere in the file doesn't count as listed. Descriptions on those lines
// must not use backticks: every backticked token there is a name.
func listParity(root string) ([]problem, error) {
	data, err := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	if err != nil {
		return nil, err
	}
	skills, _, err := dirNames(root, skillsDir, func(e fs.DirEntry) (string, bool) {
		return e.Name(), e.IsDir() && exists(root, skillsDir+"/"+e.Name()+"/SKILL.md")
	})
	if err != nil {
		return nil, err
	}
	ps := parity(data, skillsAnchor, skillsDir, skills)
	commands, ok, err := dirNames(root, commandsDir, func(e fs.DirEntry) (string, bool) {
		return strings.TrimSuffix(e.Name(), ".md"), !e.IsDir() && strings.HasSuffix(e.Name(), ".md")
	})
	if err != nil {
		return nil, err
	}
	if ok {
		ps = append(ps, parity(data, commandsAnchor, commandsDir, commands)...)
	}
	return ps, nil
}

// dirNames returns the names keep accepts among dir's entries, and whether
// dir exists.
func dirNames(root, dir string, keep func(fs.DirEntry) (string, bool)) (map[string]bool, bool, error) {
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir)))
	if os.IsNotExist(err) {
		return map[string]bool{}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	names := map[string]bool{}
	for _, e := range entries {
		if n, ok := keep(e); ok {
			names[n] = true
		}
	}
	return names, true, nil
}

// parity checks that exactly one line of the root CLAUDE.md starts with
// anchor and that the backticked names on it match want, both ways.
func parity(data []byte, anchor, dir string, want map[string]bool) []problem {
	var at []int
	listed := map[string]bool{}
	for i, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, anchor) {
			continue
		}
		at = append(at, i+1)
		for _, m := range backticked.FindAllStringSubmatch(line, -1) {
			listed[m[1]] = true
		}
	}
	switch {
	case len(at) == 0:
		return []problem{{"CLAUDE.md", 0, fmt.Sprintf("no line starting %q to list %s", anchor, dir)}}
	case len(at) > 1:
		return []problem{{"CLAUDE.md", at[1], fmt.Sprintf("a second line starting %q", anchor)}}
	}
	var ps []problem
	for n := range want {
		if !listed[n] {
			ps = append(ps, problem{"CLAUDE.md", at[0], fmt.Sprintf("%s/%s is missing from this list", dir, n)})
		}
	}
	for n := range listed {
		if !want[n] {
			ps = append(ps, problem{"CLAUDE.md", at[0], fmt.Sprintf("lists %q, which %s doesn't hold", n, dir)})
		}
	}
	return ps
}
