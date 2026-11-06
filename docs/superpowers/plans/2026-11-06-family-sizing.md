# Family Sizing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a job with no runs of its own take its claim and duration from its near relatives' runs (its family) before the advisor's prior or the default.

**Architecture:** A pure `sched.FamilyOf` reduces a signature's command to a sorted set of steps (tool, subcommand, targets), splitting `sh -c` scripts with a small lexer. A `sched.Families` memo, held by the model's queue state, finds a signature's family pool: the newest runs of every other belief of its family. `Size`, `NeedsPrior` and `Usual` take that pool as a fallback after the job's own runs; the views carry a `family` source. Spec: `docs/superpowers/specs/2026-11-06-family-sizing-design.md`.

**Tech Stack:** Go (module `github.com/aidan-bailey/loom`), testify. Packages `sched`, `core`, `internal/cli`, `ui`.

**Execution:** inline in this session (owner's choice), one commit per work package, no pauses between them.

**Running checks:** every build and test goes through loom's queue: `loom job run -d "<what>" -- sh -c 'CGO_ENABLED=0 go test ./sched/ -run <Name>'` (run in the background, or `loom job submit` and wait for the `[loom] job N` message). `gofmt` runs directly.

---

## Work package 1: the family key (`sched/shellwords.go`, `sched/family.go`)

Pure code with no callers yet.

**Files:**
- Create: `sched/shellwords.go`, `sched/shellwords_test.go`
- Create: `sched/family.go`, `sched/family_test.go`

- [ ] **Step 1: Write the lexer's failing test** (`sched/shellwords_test.go`)

```go
package sched

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSplitScript(t *testing.T) {
	cases := []struct {
		script string
		want   [][]string
	}{
		{`a 'b c' "d $(e "f") g" h\ i`, [][]string{{"a", "b c", `d $(e "f") g`, "h i"}}},
		{"a && b || c; d | e & f\ng |& h", [][]string{{"a"}, {"b"}, {"c"}, {"d"}, {"e"}, {"f"}, {"g"}, {"h"}}},
		{"a 2>&1 >out <in &>all >>log b", [][]string{{"a", "b"}}},
		{"a 2 >x", [][]string{{"a", "2"}}},
		{"a # a comment && b\nc", [][]string{{"a"}, {"c"}}},
		{"a `b c` d", [][]string{{"a", "`b c`", "d"}}},
		{"X=${HOME}/y a", [][]string{{"X=${HOME}/y", "a"}}},
		{"a \\\n b", [][]string{{"a", "b"}}},
		{"a ''", [][]string{{"a", ""}}},
		{"a\r\n", [][]string{{"a"}}},
		{"", nil},
	}
	for _, c := range cases {
		got, err := splitScript(c.script)
		require.NoError(t, err, "%q", c.script)
		assert.Equal(t, c.want, got, "%q", c.script)
	}
	for _, script := range []string{
		`a 'b`, `a "b`, `a $(b`, "a `b", "a ${b",
		"(a)", "{ a; }", "a <<EOF\nx\nEOF", "a <<<x", "a >",
		"if a; then b; fi", "for x in y; do z; done", "FOO=1 while x; do y; done", "case x in y) z;; esac",
	} {
		_, err := splitScript(script)
		assert.ErrorIs(t, err, errScript, "%q", script)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `loom job run -d "sched: split script test" -- sh -c 'CGO_ENABLED=0 go test ./sched/ -run TestSplitScript'`
Expected: FAIL, `undefined: splitScript`.

- [ ] **Step 3: Write the lexer** (`sched/shellwords.go`)

```go
package sched

import (
	"errors"
	"strings"
)

// errScript is why a script has no family (family sizing spec §5.1): it
// holds something the lexer doesn't read.
var errScript = errors.New("a script the family lexer doesn't read")

// scriptKeywords are the words that make a script one the lexer doesn't
// read when they stand in command position: control flow.
var scriptKeywords = map[string]bool{
	"if": true, "then": true, "else": true, "elif": true, "fi": true, "for": true, "while": true,
	"until": true, "do": true, "done": true, "case": true, "esac": true, "function": true,
}

// splitScript is script's simple commands, each as its words (family
// sizing spec §5.1): split on unquoted &&, ||, ;, |, |&, & and newlines;
// quotes and backslashes honoured and removed; $(…), ${…} and backquotes
// kept in their word as written; redirections dropped with their target;
// comments dropped. It fails with errScript on an unbalanced quote, $(…),
// ${…} or backquote, a subshell or brace group, a here-document or
// here-string, a redirection with no target, and control flow.
func splitScript(script string) ([][]string, error) {
	l := lexer{src: script}
	if err := l.run(); err != nil {
		return nil, err
	}
	for _, words := range l.cmds {
		i := 0
		for i < len(words) && isAssignment(words[i]) {
			i++
		}
		if i < len(words) && scriptKeywords[words[i]] {
			return nil, errScript
		}
	}
	return l.cmds, nil
}

// lexer is splitScript's state.
type lexer struct {
	src    string
	i      int
	cur    strings.Builder
	inWord bool // a word has begun, an empty quoted one too
	quoted bool // some of the word was quoted or escaped
	skip   bool // the next word is a redirection's target
	words  []string
	cmds   [][]string
}

func (l *lexer) run() error {
	for l.i < len(l.src) {
		c := l.src[l.i]
		switch {
		case c == ' ' || c == '\t' || c == '\r':
			l.endWord()
			l.i++
		case c == '\n':
			if err := l.endCommand(); err != nil {
				return err
			}
			l.i++
		case c == '#' && !l.inWord:
			for l.i < len(l.src) && l.src[l.i] != '\n' {
				l.i++
			}
		case c == '\'':
			end := strings.IndexByte(l.src[l.i+1:], '\'')
			if end < 0 {
				return errScript
			}
			l.cur.WriteString(l.src[l.i+1 : l.i+1+end])
			l.inWord, l.quoted = true, true
			l.i += end + 2
		case c == '"':
			if err := l.double(); err != nil {
				return err
			}
		case c == '\\':
			if l.i+1 < len(l.src) && l.src[l.i+1] != '\n' { // a backslash-newline joins the lines
				l.cur.WriteByte(l.src[l.i+1])
				l.inWord, l.quoted = true, true
			}
			l.i += 2
		case c == '$' && (l.peek(1) == '(' || l.peek(1) == '{'):
			if err := l.raw(); err != nil {
				return err
			}
		case c == '`':
			if err := l.backquote(); err != nil {
				return err
			}
		case c == '(' || c == ')' || c == '{' || c == '}':
			return errScript
		case c == '<' && l.peek(1) == '<':
			return errScript
		case c == '<' || c == '>' || (c == '&' && l.peek(1) == '>'):
			l.redirect()
		case c == '&' || c == '|' || c == ';':
			if err := l.endCommand(); err != nil {
				return err
			}
			l.i++
			if n := l.peek(0); n == c || (c == '|' && n == '&') {
				l.i++
			}
		default:
			l.cur.WriteByte(c)
			l.inWord = true
			l.i++
		}
	}
	return l.endCommand()
}

// peek is the byte k past l.i, 0 past the end.
func (l *lexer) peek(k int) byte {
	if l.i+k < len(l.src) {
		return l.src[l.i+k]
	}
	return 0
}

// double reads the double-quoted part at l.i: a backslash escapes $, `,
// ", \ and a newline; $(…), ${…} and backquotes stay as written.
func (l *lexer) double() error {
	l.inWord, l.quoted = true, true
	l.i++
	for l.i < len(l.src) {
		switch c := l.src[l.i]; {
		case c == '"':
			l.i++
			return nil
		case c == '\\' && strings.IndexByte("$`\"\\\n", l.peek(1)) >= 0 && l.peek(1) != 0:
			if l.peek(1) != '\n' {
				l.cur.WriteByte(l.peek(1))
			}
			l.i += 2
		case c == '$' && (l.peek(1) == '(' || l.peek(1) == '{'):
			if err := l.raw(); err != nil {
				return err
			}
		case c == '`':
			if err := l.backquote(); err != nil {
				return err
			}
		default:
			l.cur.WriteByte(c)
			l.i++
		}
	}
	return errScript
}

// raw copies the $(…) or ${…} at l.i into the word as written, counting
// nested pairs and skipping quoted parts inside it.
func (l *lexer) raw() error {
	open := l.src[l.i+1]
	closing := byte(')')
	if open == '{' {
		closing = '}'
	}
	depth := 0
	for j := l.i + 1; j < len(l.src); j++ {
		switch l.src[j] {
		case '\\':
			j++
		case '\'', '"':
			end := strings.IndexByte(l.src[j+1:], l.src[j])
			if end < 0 {
				return errScript
			}
			j += end + 1
		case open:
			depth++
		case closing:
			if depth--; depth == 0 {
				l.cur.WriteString(l.src[l.i : j+1])
				l.inWord = true
				l.i = j + 1
				return nil
			}
		}
	}
	return errScript
}

// backquote copies the backquoted part at l.i into the word as written.
func (l *lexer) backquote() error {
	end := strings.IndexByte(l.src[l.i+1:], '`')
	if end < 0 {
		return errScript
	}
	l.cur.WriteString(l.src[l.i : l.i+2+end])
	l.inWord = true
	l.i += end + 2
	return nil
}

// redirect drops the redirection at l.i: an unquoted word of digits right
// before it is its file descriptor, and the word after it its target. The
// operators: <, >, >>, <>, <&, >&, >|, &>, &>>.
func (l *lexer) redirect() {
	if l.inWord && !l.quoted && isDigits(l.cur.String()) {
		l.cur.Reset()
		l.inWord = false
	} else {
		l.endWord()
	}
	if l.src[l.i] == '&' {
		l.i++
	}
	l.i++ // < or >
	if n := l.peek(0); n == '>' || n == '&' || n == '|' {
		l.i++
	}
	l.skip = true
}

func (l *lexer) endWord() {
	if !l.inWord {
		return
	}
	if l.skip {
		l.skip = false
	} else {
		l.words = append(l.words, l.cur.String())
	}
	l.cur.Reset()
	l.inWord, l.quoted = false, false
}

func (l *lexer) endCommand() error {
	l.endWord()
	if l.skip {
		return errScript // a redirection with no target
	}
	if len(l.words) > 0 {
		l.cmds = append(l.cmds, l.words)
		l.words = nil
	}
	return nil
}

// isDigits reports whether s is a non-empty run of decimal digits.
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
```

- [ ] **Step 4: Run the lexer test until it passes**

Run: `loom job run -d "sched: split script test" -- sh -c 'CGO_ENABLED=0 go test ./sched/ -run TestSplitScript'`
Expected: PASS.

- [ ] **Step 5: Write the family's failing tests** (`sched/family_test.go`)

```go
package sched

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// famSig is the signature of argv in the folder "." of a repo.
func famSig(argv ...string) Signature { return NewSignature("/src/loom/.git", ".", argv) }

// famLabel is argv's family's label, "" for none.
func famLabel(argv ...string) string {
	f, ok := FamilyOf(famSig(argv...))
	if !ok {
		return ""
	}
	return f.Label()
}

func TestFamilyOf(t *testing.T) {
	cases := []struct {
		want string
		argv []string
	}{
		// Merged: filters, trims, redirections, assignments, wrappers, flag order, paths.
		{"go test ./sched", []string{"go", "test", "./sched"}},
		{"go test ./sched", []string{"sh", "-c", "go test ./sched -run TestA 2>&1 | tail -40"}},
		{"go test ./sched", []string{"go", "test", "./sched/", "-count=1", "-v"}},
		{"go test ./sched", []string{"env", "CGO_ENABLED=0", "go", "test", "./sched"}},
		{"go test ./sched", []string{"sh", "-c", "CGO_ENABLED=0 go test ./sched && echo ok"}},
		{"go test ./sched", []string{"bash", "-ec", "export X=1; nice -n 5 go test ./sched | grep -v ^ok"}},
		{"go test ./sched", []string{"/usr/local/bin/go", "test", "./sched"}},
		{"go test ./sched", []string{"sh", "-c", "go test ./sched; go test ./sched -run TestB"}},
		{"go test -race ./...", []string{"go", "test", "-race", "./...", "-count=1"}},
		{"go test -race ./...", []string{"go", "test", "-count=1", "--race", "./..."}},
		{"go test -tags e2e ./e2e/...", []string{"go", "test", "-tags", "e2e", "./e2e/..."}},
		{"go test -tags e2e ./e2e/...", []string{"go", "test", "-tags=e2e", "./e2e/...", "-v"}},
		{"go test ./core ./sched", []string{"go", "test", "./sched", "./core"}},
		{"cargo nextest run -p a -p b", []string{"cargo", "nextest", "run", "-p", "b", "-p", "a", "some_filter"}},
		{"cargo nextest run -p a -p b", []string{"cargo", "nextest", "run", "--package=a", "-pb", "--lib", "--", "--exact"}},
		{"cargo test --workspace --doc", []string{"cargo", "test", "--all", "--doc", "--all-features"}},
		{"cargo test --workspace --doc", []string{"cargo", "test", "--doc", "--workspace"}},
		{"cargo clippy -p a && cargo fmt", []string{"sh", "-c", "cargo fmt --check && cargo clippy -p a -- -D warnings"}},
		{"cargo clippy -p a && cargo fmt", []string{"sh", "-c", "cargo clippy -p a && cargo fmt --all --check"}},
		// Kept apart.
		{"cd a && pnpm test", []string{"sh", "-c", "cd a && pnpm test"}},
		{"cd b && pnpm test", []string{"sh", "-c", "cd b && pnpm test"}},
		{"cargo build --release", []string{"cargo", "build", "--release"}},
		{"cargo build", []string{"cargo", "build"}},
		{"go test -tags systemd_live ./sched", []string{"go", "test", "-tags", "systemd_live", "./sched"}},
		{"go vet ./sched", []string{"go", "vet", "./sched"}},
		// Other programs: the program and its first two words that aren't flags.
		{"pnpm exec playwright", []string{"pnpm", "exec", "playwright", "test", "x.spec.ts", "--workers", "8"}},
		{"python3 scripts/gen.py", []string{"python3", "scripts/gen.py", "--check"}},
		{"bash scripts/verify-affected.sh", []string{"bash", "scripts/verify-affected.sh"}},
		{"sleep 0", []string{"sh", "-c", "sleep 0 2>&1 | tail -3"}},
		// No family.
		{"", []string{"sh", "-c", "echo 'unbalanced"}},
		{"", []string{"sh", "-c", "(cd x && make)"}},
		{"", []string{"sh", "-c", "cat <<EOF\nx\nEOF"}},
		{"", []string{"sh", "-c", "for f in a b; do go test $f; done"}},
		{"", []string{"sh", "-c", "echo hi | tail -1"}},
		{"", []string{"sh", "-c", "cd x"}},
		{"", []string{"true"}},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, famLabel(c.argv...), "%q", c.argv)
	}
}

func TestFamilyOf_KeyHoldsTheRepoAndFolderButNotTheEnvOrPrerequisite(t *testing.T) {
	key := func(s Signature) string {
		f, ok := FamilyOf(s)
		require.True(t, ok)
		return f.Key()
	}
	base := famSig("go", "test", "./sched")
	withEnv := famSig("env", "CGO_ENABLED=0", "go", "test", "./sched")
	after := base
	after.After = "0123456789ab"
	assert.Equal(t, key(base), key(withEnv), "the environment is not part of it")
	assert.Equal(t, key(base), key(after), "nor the prerequisite")
	assert.NotEqual(t, key(base), key(NewSignature("/src/loom/.git", "core", []string{"go", "test", "./sched"})), "another folder")
	assert.NotEqual(t, key(base), key(NewSignature("/src/other/.git", ".", []string{"go", "test", "./sched"})), "another repo")
}

func TestFamilies_Pool(t *testing.T) {
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	var bs Beliefs
	self := famSig("sh", "-c", "go test ./sched -run TestA")
	sister := famSig("go", "test", "./sched")
	cousin := famSig("go", "test", "./sched", "-run", "TestB")
	stranger := famSig("go", "test", "./core")
	run := func(job JobID, age time.Duration) Run {
		return Run{Job: job, Ended: now.Add(-age), Outcome: OutcomeDone, PeakKB: 1 << 20, WallSeconds: 1}
	}
	bs.Ensure(self, now).AddRun(run(1, 0))
	for i := range 8 {
		bs.Ensure(sister, now).AddRun(run(JobID(10+i), time.Duration(i+1)*time.Minute))
	}
	for i := range 4 {
		bs.Ensure(cousin, now).AddRun(run(JobID(20+i), time.Duration(i)*time.Minute+30*time.Second))
	}
	bs.Ensure(stranger, now).AddRun(run(30, 0))

	var fs Families
	pool := fs.Pool(bs.Beliefs, self)
	require.NotNil(t, pool)
	require.Len(t, pool.Runs, window, "cut to the window")
	var jobs []JobID
	for _, r := range pool.Runs {
		jobs = append(jobs, r.Job)
	}
	assert.Equal(t, []JobID{20, 10, 21, 11, 22, 12, 23, 13, 14, 15}, jobs,
		"its relatives' runs newest first; its own run and the stranger's are left out")
	assert.Nil(t, fs.Pool(bs.Beliefs, stranger), "a family with no other belief")
	assert.Nil(t, fs.Pool(bs.Beliefs, famSig("sh", "-c", "(x)")), "no family")
	assert.Equal(t, 3, fs.Members(bs.Beliefs, self), "self, sister and cousin")
	assert.Zero(t, fs.Members(bs.Beliefs, famSig("true")))
}

func TestFamilies_Kin(t *testing.T) {
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	var bs Beliefs
	for _, argv := range [][]string{
		{"sh", "-c", "cd a && cargo nextest run -p x && cargo clippy -p x"},
		{"sh", "-c", "cd a && cargo clippy -p x && cargo nextest run -p x filter"},
		{"cargo", "clippy", "-p", "x"},
		{"sh", "-c", "cd a && cargo build"},
		{"sh", "-c", "(x)"},
	} {
		bs.Ensure(famSig(argv...), now)
	}
	b := func(i int) *Belief { return &bs.Beliefs[i] }
	var fs Families
	same, shared := fs.Kin(b(0), b(1))
	assert.True(t, same)
	assert.Equal(t, 2, shared, "the cd step doesn't count")
	same, shared = fs.Kin(b(0), b(2))
	assert.False(t, same)
	assert.Equal(t, 1, shared)
	_, shared = fs.Kin(b(0), b(3))
	assert.Zero(t, shared, "a shared cd step alone")
	_, shared = fs.Kin(b(0), b(4))
	assert.Zero(t, shared, "a belief with no family")
}
```

- [ ] **Step 6: Run them to see them fail**

Run: `loom job run -d "sched: family tests" -- sh -c 'CGO_ENABLED=0 go test ./sched/ -run "TestFamil"'`
Expected: FAIL, `undefined: FamilyOf`.

- [ ] **Step 7: Write `sched/family.go`**

```go
package sched

import (
	"cmp"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
)

// Family is what a job shares with its near relatives (family sizing spec
// §4): its repo, its folder and the sorted set of its steps, each a command
// reduced to what decides its size.
type Family struct {
	Repo   string
	Folder string
	Steps  []string
}

// Key is f as one comparable string: the JSON array [repo, folder, steps].
func (f Family) Key() string {
	data, err := json.Marshal([]any{f.Repo, f.Folder, f.Steps})
	if err != nil {
		panic(err) // strings and a slice of them: Marshal cannot fail
	}
	return string(data)
}

// Label is f's steps joined by " && ", as the display names a family.
func (f Family) Label() string { return strings.Join(f.Steps, " && ") }

// outputOnly are the programs whose step a family drops: they shape a
// command's output and size nothing.
var outputOnly = map[string]bool{
	"tail": true, "head": true, "grep": true, "sed": true, "wc": true, "sort": true,
	"cat": true, "echo": true, "true": true, "export": true, "set": true,
}

// FamilyOf is sig's family (family sizing spec §5.1), a pure function of
// its repo, folder and command: the environment and the prerequisite are
// not part of it. It is false when sig has none: a shell script the lexer
// doesn't read, or no step left but cd.
func FamilyOf(sig Signature) (Family, bool) {
	cmds, ok := commandsOf(sig.Command)
	if !ok {
		return Family{}, false
	}
	var steps []string
	real := false
	for _, words := range cmds {
		if step, ok := stepOf(words); ok {
			steps = append(steps, step)
			real = real || !isCdStep(step)
		}
	}
	if !real {
		return Family{}, false
	}
	slices.Sort(steps)
	return Family{Repo: sig.Repo, Folder: sig.Folder, Steps: slices.Compact(steps)}, true
}

// commandsOf is command's simple commands: a shell's -c script split by
// splitScript, or command itself.
func commandsOf(command []string) ([][]string, bool) {
	if len(command) >= 3 && isShell(command[0]) && isDashC(command[1]) {
		cmds, err := splitScript(command[2])
		return cmds, err == nil
	}
	return [][]string{command}, len(command) > 0
}

func isShell(w string) bool {
	switch filepath.Base(w) {
	case "sh", "bash", "dash", "zsh":
		return true
	}
	return false
}

// isDashC reports whether a is -c or a one-dash cluster of letters ending
// in c (-ec, -lc, -euc).
func isDashC(a string) bool {
	if len(a) < 2 || a[0] != '-' || a[len(a)-1] != 'c' {
		return false
	}
	for _, r := range a[1:] {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

func isCdStep(step string) bool { return step == "cd" || strings.HasPrefix(step, "cd ") }

// stepOf is the step of one simple command (spec §5.1), false for a step
// the family drops.
func stepOf(words []string) (string, bool) {
	i := 0
	for i < len(words) && isAssignment(words[i]) {
		i++
	}
	command, _ := Normalize(words[i:])
	if len(command) == 0 || outputOnly[command[0]] {
		return "", false
	}
	switch prog, args := command[0], command[1:]; prog {
	case "cd":
		if len(args) == 0 {
			return "cd", true
		}
		return "cd " + args[0], true
	case "cargo":
		return cargoStep(args), true
	case "go":
		return goStep(args), true
	default:
		return otherStep(prog, args), true
	}
}

// cargoStep is a cargo command's step: its subcommand (nextest with the
// word after it), --workspace, --doc and --release, and its crates.
func cargoStep(args []string) string {
	sub, rest := "", []string(nil)
	for i, a := range args {
		if a == "--" {
			break
		}
		if !strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "+") {
			sub, rest = a, args[i+1:]
			break
		}
	}
	if sub == "nextest" && len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		sub, rest = sub+" "+rest[0], rest[1:]
	}
	var workspace, doc, release bool
	var crates []string
	for i := 0; i < len(rest); i++ {
		switch a := rest[i]; {
		case a == "--":
			i = len(rest)
		case a == "--workspace" || a == "--all":
			workspace = true
		case a == "--doc":
			doc = true
		case a == "--release":
			release = true
		case (a == "-p" || a == "--package") && i+1 < len(rest):
			crates = append(crates, rest[i+1])
			i++
		case strings.HasPrefix(a, "--package="):
			crates = append(crates, strings.TrimPrefix(a, "--package="))
		case strings.HasPrefix(a, "-p") && len(a) > 2:
			crates = append(crates, a[2:])
		}
	}
	parts := []string{"cargo"}
	if sub != "" {
		parts = append(parts, sub)
	}
	for _, f := range []struct {
		on   bool
		flag string
	}{{workspace, "--workspace"}, {doc, "--doc"}, {release, "--release"}} {
		if f.on {
			parts = append(parts, f.flag)
		}
	}
	slices.Sort(crates)
	for _, c := range slices.Compact(crates) {
		parts = append(parts, "-p", c)
	}
	return strings.Join(parts, " ")
}

// goStep is a go command's step: its subcommand, -race, -tags and its
// package patterns, up to -args.
func goStep(args []string) string {
	sub, rest := "", []string(nil)
	for i, a := range args {
		if !strings.HasPrefix(a, "-") {
			sub, rest = a, args[i+1:]
			break
		}
	}
	race, tags := false, ""
	var pkgs []string
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		flag := strings.TrimLeft(a, "-")
		dashed := flag != a
		switch {
		case dashed && flag == "args":
			i = len(rest)
		case dashed && flag == "race":
			race = true
		case dashed && flag == "tags" && i+1 < len(rest):
			tags = rest[i+1]
			i++
		case dashed && strings.HasPrefix(flag, "tags="):
			tags = strings.TrimPrefix(flag, "tags=")
		case a == "." || strings.HasPrefix(a, "./") || strings.HasPrefix(a, "../"):
			if len(a) > 2 {
				a = strings.TrimSuffix(a, "/")
			}
			pkgs = append(pkgs, a)
		}
	}
	parts := []string{"go"}
	if sub != "" {
		parts = append(parts, sub)
	}
	if race {
		parts = append(parts, "-race")
	}
	if tags != "" {
		parts = append(parts, "-tags", tags)
	}
	slices.Sort(pkgs)
	return strings.Join(append(parts, slices.Compact(pkgs)...), " ")
}

// otherStep is any other program's step: it and its first two words that
// aren't flags.
func otherStep(prog string, args []string) string {
	parts := []string{prog}
	for _, a := range args {
		if len(parts) == 3 {
			break
		}
		if !strings.HasPrefix(a, "-") {
			parts = append(parts, a)
		}
	}
	return strings.Join(parts, " ")
}

// Families memoizes FamilyOf by signature ID (family sizing spec §5.2): an
// ID names one signature, so an entry never goes stale, and the memo
// starts over past maxFamilyMemo entries, since beliefs come and go. The
// zero value is ready; it is not safe for concurrent use (the model's loop
// holds the one it uses).
type Families struct {
	byID map[SignatureID]familyMemo
}

// familyMemo is one signature's family: key is "" when it has none.
type familyMemo struct {
	family Family
	key    string
}

const maxFamilyMemo = 2 * MaxBeliefs

func (fs *Families) memo(id SignatureID, sig Signature) familyMemo {
	if m, ok := fs.byID[id]; ok {
		return m
	}
	if fs.byID == nil || len(fs.byID) >= maxFamilyMemo {
		fs.byID = make(map[SignatureID]familyMemo)
	}
	var m familyMemo
	if f, ok := FamilyOf(sig); ok {
		m = familyMemo{family: f, key: f.Key()}
	}
	fs.byID[id] = m
	return m
}

// memoOf is b's family memo; a belief built with no ID (a test's) is
// looked up by its signature's.
func (fs *Families) memoOf(b *Belief) familyMemo {
	id := b.ID
	if id == "" {
		id = b.Signature.ID()
	}
	return fs.memo(id, b.Signature)
}

// Of is sig's family, false for none.
func (fs *Families) Of(sig Signature) (Family, bool) {
	m := fs.memo(sig.ID(), sig)
	f := m.family
	f.Steps = slices.Clone(f.Steps)
	return f, m.key != ""
}

// Pool is sig's family pool among bs (spec §5.2): the runs of every belief
// of sig's family but sig's own, newest first (by Ended, then by job),
// cut to the window, as a belief with only its runs (their profiles and
// curves dropped: nothing reads them from a pool). It is nil when sig has
// no family or no other belief of its family has a run.
func (fs *Families) Pool(bs []Belief, sig Signature) *Belief {
	id := sig.ID()
	self := fs.memo(id, sig)
	if self.key == "" {
		return nil
	}
	var runs []Run
	for i := range bs {
		if b := &bs[i]; b.ID != id && fs.memoOf(b).key == self.key {
			runs = append(runs, b.Runs...)
		}
	}
	if len(runs) == 0 {
		return nil
	}
	slices.SortStableFunc(runs, func(x, y Run) int {
		if c := y.Ended.Compare(x.Ended); c != 0 {
			return c
		}
		return cmp.Compare(y.Job, x.Job)
	})
	runs = runs[:min(len(runs), window)]
	for i := range runs {
		runs[i].Profile, runs[i].Curve = nil, nil
	}
	return &Belief{Runs: runs}
}

// Members is how many beliefs of bs are of sig's family, sig's own
// included; 0 when sig has none.
func (fs *Families) Members(bs []Belief, sig Signature) int {
	self := fs.memo(sig.ID(), sig)
	if self.key == "" {
		return 0
	}
	n := 0
	for i := range bs {
		if fs.memoOf(&bs[i]).key == self.key {
			n++
		}
	}
	return n
}

// Kin is how a and b are related (spec §5.5): whether they are of one
// family, and how many steps but cd steps their families share; false and
// 0 when either has none.
func (fs *Families) Kin(a, b *Belief) (same bool, shared int) {
	ma, mb := fs.memoOf(a), fs.memoOf(b)
	if ma.key == "" || mb.key == "" {
		return false, 0
	}
	for _, s := range ma.family.Steps {
		if !isCdStep(s) && slices.Contains(mb.family.Steps, s) {
			shared++
		}
	}
	return ma.key == mb.key, shared
}
```

- [ ] **Step 8: Run the package's tests until they pass**

Run: `loom job run -d "sched: family tests" -- sh -c 'CGO_ENABLED=0 go test ./sched/ -run "TestFamil|TestSplitScript"'`
Expected: PASS. In `TestFamilies_Pool`, the expected order interleaves cousin (Ended at 0, 1.5, 2.5, 3.5 minutes ago… i.e. `i` minutes plus 30 s) and sister (`i+1` minutes): recompute it from the fixture if it disagrees, never loosen it to a set.

- [ ] **Step 9: Format and commit**

```bash
gofmt -l sched/
git add sched/shellwords.go sched/shellwords_test.go sched/family.go sched/family_test.go
git commit -m "feat(sched): a job's family: its steps, the pool of its relatives' runs" -- sched/shellwords.go sched/shellwords_test.go sched/family.go sched/family_test.go
```

## Work package 2: sizing and durations from the family (`sched`)

Signature changes; every caller passes `nil` for the pool until work package 3 wires real ones, so the build stays green.

**Files:**
- Modify: `sched/size.go` (`SourceFamily`, `Size`, `Sized.FamilyRuns`, `Tag`, `Describe`, `fullPeaks`)
- Modify: `sched/surprise.go` (`NeedsPrior`, `Surprise`)
- Modify: `sched/durations.go` (`DurationSource`, `Usual`, `OverdueAfter`)
- Modify: `sched/job.go` (`Record.FamilyRuns`)
- Modify: `sched/beliefs_view.go` (`BeliefView` fields, `ViewBelief`)
- Modify: `sched/belief_text.go` (claim, duration and family lines)
- Modify callers: `core/queue_beliefs.go`, `core/queue_advisor.go`, `core/queue_greedy.go`, `core/queue_view.go`, `internal/cli/job_sizing.go`, `internal/cli/job_beliefs.go`, `ui/work.go`
- Tests: `sched/size_test.go`, `sched/surprise_test.go`, `sched/grant_test.go`, the belief-text test, `internal/cli/job_sizing_test.go`

- [ ] **Step 1: Write the failing sizing tests** — append to `sched/size_test.go`:

```go
func TestSize_FromTheFamily(t *testing.T) {
	const peak138 = 14_422_836 // 13.8 GB
	F, L, D, A, E := SourceFamily, SourceLearned, SourceDefault, SourceAgent, SourceExplicit
	fam := belief(done(5<<20, 20, 10)) // 2 busy cores, 5 GB
	failedBig := Run{Outcome: OutcomeFailed, PeakKB: peak138, WallSeconds: 30}
	adopted := Run{Outcome: OutcomeDone, Adopted: true, PeakKB: 1 << 20, CPUSeconds: 10, WallSeconds: 10}
	io3 := done(1<<20, 10, 10)
	io3.WriteBytes = int64p(3 << 30)
	capped := Policy{Caps: Caps{CPUs: 14, MemoryGB: 14, IOJobs: 2}, DefaultClaim: Claim{CPUs: 4, MemoryGB: 6}}
	cases := []struct {
		name       string
		asked      Claim
		b, family  *Belief
		policy     Policy
		want       Claim
		sources    ClaimSources
		learned    int
		familyRuns int
		above      bool
	}{
		{"the family fills what the job lacks", Claim{}, nil, fam, szPolicy, Claim{CPUs: 3, MemoryGB: 6}, ClaimSources{F, F, D}, 0, 1, false},
		{"the job's own runs win", Claim{}, belief(done(1<<20, 10, 10)), fam, szPolicy, Claim{CPUs: 2, MemoryGB: 2}, ClaimSources{L, L, D}, 1, 0, false},
		{"parts mix: own cores, family memory", Claim{}, belief(adopted), fam, szPolicy, Claim{CPUs: 2, MemoryGB: 6}, ClaimSources{L, F, D}, 1, 1, false},
		{"a job's own failed peak raises a family claim", Claim{}, belief(failedBig), fam, szPolicy, Claim{CPUs: 3, MemoryGB: 16}, ClaimSources{F, F, D}, 0, 1, false},
		{"a family member's failed peak neither sizes", Claim{}, nil, belief(failedBig), szPolicy, Claim{CPUs: 4, MemoryGB: 6}, all(D), 0, 0, false},
		{"nor raises", Claim{}, nil, belief(done(1<<20, 10, 10), failedBig), szPolicy, Claim{CPUs: 2, MemoryGB: 2}, ClaimSources{F, F, D}, 0, 2, false},
		{"a family never makes an overrunner", Claim{}, nil, belief(at(4, 7.81)), szPolicy, Claim{CPUs: 4, MemoryGB: 2}, ClaimSources{F, F, D}, 0, 1, false},
		{"explicit wins over the family", Claim{MemoryGB: 3}, nil, fam, szPolicy, Claim{CPUs: 3, MemoryGB: 3}, ClaimSources{F, E, D}, 0, 1, false},
		{"the family wins over the prior", Claim{}, &Belief{Prior: &Prior{CPUs: 6, MemoryGB: 12, IO: true}}, fam, szPolicy,
			Claim{CPUs: 3, MemoryGB: 6, IO: true}, ClaimSources{F, F, A}, 0, 1, false},
		{"a family claim above the cap", Claim{}, nil, belief(done(peak138, 0, 93)), capped, Claim{CPUs: 4, MemoryGB: 14}, ClaimSources{D, F, D}, 0, 1, true},
		{"the family's I/O", Claim{}, nil, belief(io3), szPolicy, Claim{CPUs: 2, MemoryGB: 2, IO: true}, all(F), 0, 1, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := Size(c.asked, c.b, c.family, c.policy)
			assert.Equal(t, c.want, s.Claim)
			assert.Equal(t, c.sources, s.Sources)
			assert.Equal(t, c.learned, s.Learned)
			assert.Equal(t, c.familyRuns, s.FamilyRuns)
			assert.Equal(t, c.above, s.AboveCap)
		})
	}
}

func TestUsual_FromTheFamily(t *testing.T) {
	sec := func(s float64) Run { return Run{Outcome: OutcomeDone, WallSeconds: s} }
	fam := belief(sec(60), sec(120), sec(90))
	usual, p90, n, src := Usual(nil, fam, 4)
	assert.Equal(t, 90*time.Second, usual)
	assert.Equal(t, 120*time.Second, p90)
	assert.Equal(t, 3, n)
	assert.Equal(t, DurationFamily, src)

	usual, _, n, src = Usual(belief(sec(30)), fam, 0)
	assert.Equal(t, 30*time.Second, usual, "the job's own runs win")
	assert.Equal(t, 1, n)
	assert.Equal(t, DurationLearned, src)

	prior := &Belief{Prior: &Prior{DurationSeconds: 300}}
	usual, _, _, src = Usual(prior, fam, 0)
	assert.Equal(t, 90*time.Second, usual, "the family before the prior")
	assert.Equal(t, DurationFamily, src)

	scaling := belief(at(3, 2.74), at(3, 2.74), at(3, 2.74)) // 100 s at 3 cores, 78 s at 4 as its own
	usual, _, _, _ = Usual(nil, scaling, 4)
	assert.Equal(t, 100*time.Second, usual, "a family's durations are unscaled")
	assert.Zero(t, OverdueAfter(&Belief{}, 0), "overdue reads only the job's own runs")
}
```

Edit the existing tests for the new signatures:
- `sched/size_test.go`: `Size(c.asked, c.b, c.policy)` → `Size(c.asked, c.b, nil, c.policy)`, and the three `Size(Claim{}, belief(…), szPolicy)` calls likewise; `Describe(n)` → `Describe(n, 0, "")`; `Tag(n)` → `Tag(n, 0)`. In `TestUsualAndOverdueAfter`: `Usual(x, n)` → `Usual(x, nil, n)`, the `fromPrior` variable → `src`, `assert.False(t, fromPrior)` → `assert.Equal(t, DurationLearned, src)` for the first and `assert.Equal(t, DurationNone, src)` for `Usual(nil, nil, 0)`, `assert.True(t, fromPrior)` → `assert.Equal(t, DurationPrior, src)`.
- Add to `TestDescribe…`/`TestTag…` (the functions holding lines 100–114):

```go
	F := SourceFamily
	assert.Equal(t, "from 6 runs of its family (go test ./sched)", ClaimSources{F, F, F}.Describe(0, 6, "go test ./sched"))
	assert.Equal(t, "cores and I/O learned from 3 runs, memory from 1 run of its family (sleep 0)", ClaimSources{L, F, L}.Describe(3, 1, "sleep 0"))
	assert.Equal(t, "family·6", ClaimSources{Memory: SourceFamily}.Tag(0, 6))
```

- `sched/surprise_test.go`: every `NeedsPrior(c, b)` → `NeedsPrior(c, b, nil)`, then add:

```go
	assert.False(t, NeedsPrior(Claim{}, nil, learned), "a family covering every part")
	assert.False(t, NeedsPrior(Claim{}, &Belief{}, learned), "a belief with no runs, a family covering every part")
	assert.True(t, NeedsPrior(Claim{}, nil, belief(Run{Outcome: OutcomeFailed, PeakKB: 1 << 20, WallSeconds: 5})), "a family with no full peak")
```

and to the surprise test:

```go
	assert.Equal(t, "it peaked at 3.0 GB, above its 2 GB claim", Surprise(prev, big, SourceFamily), "a family-sourced claim")
```

- `sched/grant_test.go`, in `TestGrant` after the first `SpareWidth` assertion:

```go
	fromFamily := learned(1, 4, 2)
	fromFamily.Sources.CPUs = SourceFamily
	assert.Zero(t, SpareWidth(fromFamily, wide), "cores from the family are no grant's")
```

- [ ] **Step 2: Run the sched tests to see them fail to build**

Run: `loom job run -d "sched: sizing tests" -- sh -c 'CGO_ENABLED=0 go test ./sched/'`
Expected: build failure: too many arguments in calls to `Size`, `Usual`, `NeedsPrior`; `undefined: SourceFamily`.

- [ ] **Step 3: Change `sched/size.go`**

Add the source, after `SourceExclusive`:

```go
	SourceFamily    Source = "family" // the family pool's runs (family sizing spec §5.3)
```

`Tag` takes the family's runs and gains a case:

```go
// Tag is the short source shown beside a claim: its memory's (learned·N,
// family·N, agent, yours, default); "" for an exclusive claim, and for a
// record from before sizing.
func (s ClaimSources) Tag(learned, familyRuns int) string {
	switch s.Memory {
	case SourceLearned:
		return fmt.Sprintf("learned·%d", learned)
	case SourceFamily:
		return fmt.Sprintf("family·%d", familyRuns)
```

`Describe` and `describe` take the family's runs and label:

```go
func (s ClaimSources) Describe(learned, familyRuns int, familyLabel string) string {
	…
	if len(groups) == 1 {
		return groups[0].src.describe(learned, familyRuns, familyLabel)
	}
	out := make([]string, len(groups))
	for i, g := range groups {
		out[i] = JoinAnd(g.parts) + " " + g.src.describe(learned, familyRuns, familyLabel)
	}
	return strings.Join(out, ", ")
}

func (src Source) describe(learned, familyRuns int, familyLabel string) string {
	switch src {
	case SourceLearned:
		if learned == 1 {
			return "learned from 1 run"
		}
		return fmt.Sprintf("learned from %d runs", learned)
	case SourceFamily:
		runs := "1 run"
		if familyRuns != 1 {
			runs = fmt.Sprintf("%d runs", familyRuns)
		}
		if familyLabel == "" {
			return "from " + runs + " of its family"
		}
		return fmt.Sprintf("from %s of its family (%s)", runs, familyLabel)
```

and update `Describe`'s doc comment to name the family source. `Sized` gains, after `Learned`:

```go
	FamilyRuns int // the family pool's runs a family part came from; 0 when no part is the family's
```

`Size` takes the pool (doc comment: "…the flag, the belief's last window runs, its family pool's runs, the belief's prior, the default claim…; a family never makes an overrunner and only its full peaks size memory"):

```go
func Size(asked Claim, b, family *Belief, p Policy) Sized {
	if asked.Exclusive {
		return Sized{Claim: Claim{Exclusive: true}, Sources: ClaimSources{SourceExclusive, SourceExclusive, SourceExclusive}}
	}
	runs, pool := b.recent(), family.recent()
	var prior *Prior
	if b != nil {
		prior = b.Prior
	}
	var s Sized
	learned, fromFamily := false, false

	sc := ScalingOf(b)
	busy, haveBusy := medianBusy(runs)
	famBusy, haveFamBusy := medianBusy(pool)
	switch {
	case asked.CPUs > 0:
		s.Claim.CPUs, s.Sources.CPUs = asked.CPUs, SourceExplicit
	case sc.Overruns:
		// The hint variables don't reach it: count the cores it takes, up
		// to the cap (job scaling spec §5.2).
		s.Claim.CPUs, s.Sources.CPUs = clampInt(int(math.Ceil(coreHeadroom*sc.Busy)), 1, p.Caps.CPUs), SourceLearned
		learned = true
	case haveBusy:
		s.Claim.CPUs, s.Sources.CPUs = clampInt(int(math.Ceil(coreHeadroom*busy)), 1, p.DefaultClaim.CPUs), SourceLearned
		learned = true
	case haveFamBusy:
		s.Claim.CPUs, s.Sources.CPUs = clampInt(int(math.Ceil(coreHeadroom*famBusy)), 1, p.DefaultClaim.CPUs), SourceFamily
		fromFamily = true
	case prior != nil:
		s.Claim.CPUs, s.Sources.CPUs = clampInt(prior.CPUs, 1, p.DefaultClaim.CPUs), SourceAgent
	default:
		s.Claim.CPUs, s.Sources.CPUs = p.DefaultClaim.CPUs, SourceDefault
	}

	peakKB, full := largestPeak(runs, s.Claim.CPUs)
	famKB, famFull := largestPeak(fullPeaks(pool), s.Claim.CPUs)
	evidence := peakKB // the peak a claim above the cap was measured from
	switch {
	case asked.MemoryGB > 0:
		s.Claim.MemoryGB, s.Sources.Memory = asked.MemoryGB, SourceExplicit
		if peakKB > int64(asked.MemoryGB)*kbPerGB {
			s.BelowKB = peakKB
		}
	case full:
		s.Claim.MemoryGB, s.Sources.Memory = withMargin(peakKB), SourceLearned
		learned = true
	case famFull:
		s.Claim.MemoryGB, s.Sources.Memory = withMargin(famKB), SourceFamily
		fromFamily = true
		evidence = max(peakKB, famKB)
		if peakKB > 0 {
			s.Claim.MemoryGB = max(s.Claim.MemoryGB, withMargin(peakKB))
		}
	default:
		s.Claim.MemoryGB, s.Sources.Memory = p.DefaultClaim.MemoryGB, SourceDefault
		if prior != nil {
			s.Claim.MemoryGB, s.Sources.Memory = prior.MemoryGB, SourceAgent
		}
		if peakKB > 0 {
			s.Claim.MemoryGB = max(s.Claim.MemoryGB, withMargin(peakKB))
		}
	}
	if s.Sources.Memory != SourceExplicit && s.Claim.MemoryGB > p.Caps.MemoryGB {
		s.AboveCap = evidence > 0 && withMargin(evidence) > p.Caps.MemoryGB
		s.Claim.MemoryGB = p.Caps.MemoryGB
	}
	s.Claim.MemoryGB = max(s.Claim.MemoryGB, 1)

	writes, haveIO := medianWrites(runs)
	famWrites, haveFamIO := medianWrites(pool)
	switch {
	case asked.IO || (asked.CPUs > 0 && asked.MemoryGB > 0):
		s.Claim.IO, s.Sources.IO = asked.IO, SourceExplicit
	case haveIO:
		s.Claim.IO, s.Sources.IO = writes >= ioThreshold, SourceLearned
		learned = true
	case haveFamIO:
		s.Claim.IO, s.Sources.IO = famWrites >= ioThreshold, SourceFamily
		fromFamily = true
	case prior != nil:
		s.Claim.IO, s.Sources.IO = prior.IO, SourceAgent
	default:
		s.Claim.IO, s.Sources.IO = false, SourceDefault
	}
	if learned {
		s.Learned = len(runs)
	}
	if fromFamily {
		s.FamilyRuns = len(pool)
	}
	return s
}

// fullPeaks is the runs among runs whose peak is full: a family member's
// floor neither sizes nor raises another job's claim (family sizing spec
// §5.3).
func fullPeaks(runs []Run) []Run {
	return slices.DeleteFunc(slices.Clone(runs), func(r Run) bool { return !r.FullPeak() })
}
```

- [ ] **Step 4: Change `sched/surprise.go`**

```go
// NeedsPrior reports whether a job that asked for asked, of belief b (nil
// for a signature with none yet) and family pool family (nil for none),
// would take some part it didn't claim from the default where an
// advisor's prior could give it (smart scheduler spec §5.4, family sizing
// spec §5.3): b has no prior, and a part not explicit has no learned value
// and none from the family.
func NeedsPrior(asked Claim, b, family *Belief) bool {
	if asked.Exclusive || (b != nil && b.Prior != nil) {
		return false
	}
	runs, pool := b.recent(), family.recent()
	_, busy := medianBusy(runs)
	_, famBusy := medianBusy(pool)
	_, full := largestPeak(runs, 0) // whether a peak is full doesn't depend on the cores
	_, famFull := largestPeak(fullPeaks(pool), 0)
	_, io := medianWrites(runs)
	_, famIO := medianWrites(pool)
	explicitIO := asked.IO || (asked.CPUs > 0 && asked.MemoryGB > 0)
	return (asked.CPUs == 0 && !busy && !famBusy) || (asked.MemoryGB == 0 && !full && !famFull) || (!explicitIO && !io && !famIO)
}
```

In `Surprise`, the memory-source condition becomes `(memory == SourceLearned || memory == SourceFamily || memory == SourceAgent || memory == SourceDefault)`, and its doc comment says "learned, the family's, the agent's or the default".

- [ ] **Step 5: Change `sched/durations.go`**

```go
// DurationSource is where a usual duration came from (family sizing spec
// §5.4).
type DurationSource string

// The sources of a usual duration.
const (
	DurationNone    DurationSource = ""
	DurationLearned DurationSource = "learned" // the belief's own runs
	DurationFamily  DurationSource = "family"  // its family pool's runs, unscaled
	DurationPrior   DurationSource = "agent"   // the advisor's prior
)

// Usual is a belief's usual duration and its P90 at cores (smart scheduler
// spec §5.5, job scaling spec §5.4, family sizing spec §5.4): the median
// and the 90th percentile (nearest rank) of the wall times of its duration
// runs among its last window, each scaled from the cores it had to cores
// (Scaling.scale: as measured when cores is 0, b's parallel fraction is
// unknown or it overruns); n is how many there were. With none, its family
// pool's duration runs stand in, unscaled (a pool mixes members whose
// scaling differs); with none of those either, the prior's duration stands
// in for both, unscaled, with n 0; with no prior, all are zero. src says
// which.
func Usual(b, family *Belief, cores int) (usual, p90 time.Duration, n int, src DurationSource) {
	s := ScalingOf(b)
	var walls []float64
	for _, r := range b.recent() {
		if r.TeachesDuration() {
			walls = append(walls, s.scale(r.WallSeconds, r.Claim.CPUs, cores))
		}
	}
	src = DurationLearned
	if len(walls) == 0 {
		for _, r := range family.recent() {
			if r.TeachesDuration() {
				walls = append(walls, r.WallSeconds)
			}
		}
		src = DurationFamily
	}
	if len(walls) == 0 {
		if b != nil && b.Prior != nil && b.Prior.DurationSeconds > 0 {
			d := time.Duration(b.Prior.DurationSeconds) * time.Second
			return d, d, 0, DurationPrior
		}
		return 0, 0, 0, DurationNone
	}
	m, _ := median(walls)
	slices.Sort(walls)
	rank := max(0, int(math.Ceil(0.9*float64(len(walls))))-1)
	return seconds(m), seconds(walls[rank]), len(walls), src
}
```

`OverdueAfter` (doc: "…of Usual over b's own runs at cores…: a family's runs never make a job overdue") calls `_, p90, n, _ := Usual(b, nil, cores)`.

- [ ] **Step 6: `Record`, `BeliefView`, `ViewBelief`, `FormatBelief`**

`sched/job.go`, after `Learned`:

```go
	FamilyRuns int `json:"family_runs,omitempty"` // the family pool's runs its family parts came from (family sizing spec §5.3)
```

`sched/beliefs_view.go`: `BeliefView` gains, after `UsualFromPrior`:

```go
	UsualFromFamily bool // Usual and P90 are its family pool's (family sizing spec §6)
	// Family is the label of its family and FamilySize how many beliefs
	// share it, its own included (family sizing spec §6). The caller sets
	// them, since it holds the registry; "" and 0 for a belief with none.
	Family     string
	FamilySize int
```

and `ViewBelief` takes the pool:

```go
// ViewBelief is b's view under p, with family its family pool (nil for
// none); with no caps (admission paused) the claim is left zero.
func ViewBelief(b Belief, family *Belief, p Policy) BeliefView {
	v := BeliefView{Belief: b, RepoName: RepoName(b.Repo), Scaling: ScalingOf(&b)}
	if p.usable() {
		v.Sized = Size(Claim{}, &b, family, p)
	}
	usual, p90, n, src := Usual(&b, family, v.Sized.Claim.CPUs)
	v.Usual, v.P90 = usual, p90
	switch src {
	case DurationLearned:
		v.DurationRuns = n
	case DurationFamily:
		v.UsualFromFamily = true
	case DurationPrior:
		v.UsualFromPrior = true
	}
	return v
}
```

`sched/belief_text.go`, in `FormatBelief`: the claim line passes `v.Sized.Sources.Describe(v.Sized.Learned, v.Sized.FamilyRuns, v.Family)`; after the `afterLine` block:

```go
	if v.Family != "" {
		lines = append(lines, fmt.Sprintf("family    %s (%s)", v.Family, plural(v.FamilySize, "belief", "beliefs")))
	}
```

(`plural` if the package has a helper of that shape; otherwise write it inline as the `runs := "1 run"` pattern in `scalingLine` does); the durations switch gains, before the prior's case:

```go
	case v.UsualFromFamily:
		lines = append(lines, fmt.Sprintf("about     %s (its family's runs)", RoughDuration(v.Usual)))
```

and `scalingLine`'s `at` calls `Usual(&v.Belief, nil, c)`. Add a test case to the existing `FormatBelief` test with `Family: "go test ./sched", FamilySize: 3, UsualFromFamily: true, Usual: 2 * time.Minute` asserting the lines `family    go test ./sched (3 beliefs)` and `about     2m (its family's runs)`.

- [ ] **Step 7: Callers pass `nil` and the new arguments**

- `core/queue_beliefs.go:122`: `sched.Size(rec.Asked, q.beliefOf(rec.Signature), nil, p)` (work package 3 passes the pool).
- `core/queue_advisor.go`: `askForPrior`'s `sched.NeedsPrior(asked, b, nil)`; `known`'s `sched.Size(sched.Claim{}, &b, nil, q.policy)` and `sched.Usual(&b, nil, 0)`; `priorOf`'s `sched.Size(sched.Claim{}, like, nil, q.policy)` and `sched.Usual(like, nil, 0)`. `known` and `priorOf` keep `nil` for good: a candidate and a `like` are described by their own runs.
- `core/queue_greedy.go` `usualOf`: `u, _, n, src := sched.Usual(m.queue.beliefOf(r.Signature), nil, r.Claim.CPUs)`; `return u, n > 0 || src == sched.DurationPrior`.
- `core/queue_view.go` `hostUsual`: `sched.Usual(&m.queue.beliefs.Beliefs[i], nil, 0)` (for good: the host's median reads each belief's own runs). `queueJobView`: `var src sched.DurationSource; jv.Usual, jv.P90, _, src = sched.Usual(b, nil, r.Claim.CPUs); jv.UsualFromPrior = src == sched.DurationPrior`.
- `core/queue_beliefs.go` `readBeliefs`: `sched.ViewBelief(b.Clone(), nil, q.policy)`.
- `internal/cli/job_sizing.go`: `v.Sources.Describe(v.Learned, v.FamilyRuns, v.Family)`, `v.Sources.Tag(v.Learned, v.FamilyRuns)`; `internal/cli/job_beliefs.go:130` adds `FamilyRuns: v.Sized.FamilyRuns` to the `JobView` it builds; `ui/work.go:576,588`: `j.Sources.Tag(j.Learned, j.FamilyRuns)`.
- `core/queue_view.go` `JobView` gains, after `Learned`:

```go
	FamilyRuns int    // the family pool's runs its family parts came from (family sizing spec §6)
	Family     string // its family's label; "" for none
```

and after `UsualFromPrior`: `UsualFromFamily bool // Usual and P90 are its family pool's`. The `queueJobView` literal sets `FamilyRuns: r.FamilyRuns`.

- [ ] **Step 8: Build everything and run the sched tests**

Run: `loom job run -d "sched: sizing tests" -- sh -c 'CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go vet ./sched/ ./core/ ./internal/cli/ ./ui/ && CGO_ENABLED=0 go test ./sched/'`
Expected: PASS. A `TestSize` row that changed means a non-family behaviour moved: fix the code, not the row.

- [ ] **Step 9: Format and commit**

```bash
gofmt -l sched core internal/cli ui
git commit -m "feat(sched): size claims and durations from a job's family pool" -- sched/size.go sched/size_test.go sched/surprise.go sched/surprise_test.go sched/durations.go sched/job.go sched/beliefs_view.go sched/belief_text.go sched/grant_test.go <the belief-text test> core/queue_beliefs.go core/queue_advisor.go core/queue_greedy.go core/queue_view.go internal/cli/job_sizing.go internal/cli/job_beliefs.go ui/work.go
```

## Work package 3: the model uses the family (`core`, `internal/cli`)

**Files:**
- Modify: `core/queue.go` (`queueState.families`)
- Modify: `core/queue_beliefs.go` (`familyPool`, `poolOf`, `sizeRecord`, `resizeQueued`, `readBeliefs`)
- Modify: `core/queue_ops.go` (`submitJob`)
- Modify: `core/queue_advisor.go` (`askForPrior`, `knownBeliefs`)
- Modify: `core/queue_greedy.go` (`usualOf`), `core/queue_view.go` (`queueJobView`), `core/queue_ladder.go` (requeue copy)
- Modify: `internal/cli/job_sizing.go` (`sizingLine`'s "about")
- Tests: `core/queue_family_test.go` (new), `internal/cli/job_sizing_test.go`

- [ ] **Step 1: Write the failing model tests** (`core/queue_family_test.go`)

```go
package core

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/sched"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wqRelative seeds a done run of argv in sub's folder: a relative of the
// jobs the tests submit there.
func wqRelative(t *testing.T, m *Model, sub Submission, peakKB int64, argv ...string) sched.SignatureID {
	t.Helper()
	sig := wqSigOf(t, argv, sub.Cwd)
	written := int64(0)
	m.queue.beliefs.Ensure(sig, time.Now()).AddRun(sched.Run{Job: 1, Ended: time.Now(), Outcome: sched.OutcomeDone,
		PeakKB: peakKB, CPUSeconds: 20, WallSeconds: 10, WriteBytes: &written})
	return sig.ID()
}

func TestJobQueue_AJobLearnsFromItsFamily(t *testing.T) {
	m := wqLiveModel(t)
	sub := wqJob(t, "sh", "-c", "sleep 0 2>&1 | tail -3")
	relative := wqRelative(t, m, sub, 5<<20, "sleep", "0")

	id, _, err := m.submitJob(sub)
	require.NoError(t, err)
	rec := wqRecord(t, m, id)
	assert.NotEqual(t, relative, rec.Signature, "a signature of its own")
	assert.Equal(t, sched.ClaimSources{CPUs: sched.SourceFamily, Memory: sched.SourceFamily, IO: sched.SourceFamily}, rec.Sources)
	assert.Equal(t, sched.Claim{CPUs: 3, MemoryGB: 6}, rec.Claim, "2 busy cores and 5 GB, with their margins")
	assert.Equal(t, 1, rec.FamilyRuns)
	assert.Zero(t, rec.Learned)
	for _, jv := range m.Queue().Jobs {
		if jv.ID == id {
			assert.Equal(t, "sleep 0", jv.Family)
			assert.Equal(t, 1, jv.FamilyRuns)
			assert.True(t, jv.UsualFromFamily)
		}
	}
	wqSettle(t, m, id)
}

func TestJobQueue_AWaitingJobFollowsItsFamily(t *testing.T) {
	m := wqLiveModel(t)
	wqConfig(t, m, `{"cpus": 4}`) // one default job fills the cores
	blocker := wqSubmit(t, m, "sleep", "60")
	require.Equal(t, sched.Running, wqRecord(t, m, blocker).State)
	sub := wqJob(t, "sh", "-c", "cargo build -p a | tail -1")
	relative := wqRelative(t, m, sub, 5<<20, "cargo", "build", "-p", "a")

	id, _, err := m.submitJob(sub)
	require.NoError(t, err)
	require.Equal(t, sched.Queued, wqRecord(t, m, id).State)
	assert.Equal(t, 6, wqRecord(t, m, id).Claim.MemoryGB)

	m.queue.beliefs.Find(relative).AddRun(sched.Run{Job: 2, Ended: time.Now(), Outcome: sched.OutcomeDone, PeakKB: 10 << 20, WallSeconds: 30})
	m.admitJobs()
	assert.Equal(t, 12, wqRecord(t, m, id).Claim.MemoryGB, "re-sized at the next pass")
}

func TestJobQueue_NoFamilyWhileLearningIsOff(t *testing.T) {
	m := wqLiveModel(t)
	sub := wqJob(t, "sh", "-c", "sleep 0 | tail -1")
	wqRelative(t, m, sub, 5<<20, "sleep", "0")
	m.queue.beliefsErr = errors.New("beliefs.json is unreadable")

	id, _, err := m.submitJob(sub)
	require.NoError(t, err)
	assert.Equal(t, sched.SourceDefault, wqRecord(t, m, id).Sources.Memory)
	assert.Zero(t, wqRecord(t, m, id).FamilyRuns)
	wqSettle(t, m, id)
}

func TestAdvisor_AFamilyWithHistoryAsksNothing(t *testing.T) {
	m, calls := wqAdvisedModel(t, wqPriorAnswer, 0)
	sub := wqJob(t, "sh", "-c", "sleep 0 | tail -1")
	wqRelative(t, m, sub, 1<<20, "sleep", "0")
	id, _, err := m.submitJob(sub)
	require.NoError(t, err)
	assert.Empty(t, wqRecord(t, m, id).Sizing, "every part from the family")
	wqPump(t, m)
	assert.Empty(t, wqCalls(t, calls), "no call")

	t.Run("a family that lacks a part still asks", func(t *testing.T) {
		m, _ := wqAdvisedModel(t, wqPriorAnswer, 0)
		sub := wqJob(t, "sh", "-c", "sleep 1 | tail -1")
		sig := wqSigOf(t, []string{"sleep", "1"}, sub.Cwd)
		m.queue.beliefs.Ensure(sig, time.Now()).AddRun(sched.Run{Job: 1, Ended: time.Now(), Outcome: sched.OutcomeDone,
			PeakKB: 1 << 20, CPUSeconds: 1, WallSeconds: 1}) // no I/O measured
		id, _, err := m.submitJob(sub)
		require.NoError(t, err)
		assert.Equal(t, sched.SizingAsking, wqRecord(t, m, id).Sizing)
	})
}

func TestAdvisor_KnownBeliefsPutTheFamilyFirst(t *testing.T) {
	m := wqLiveModel(t)
	cwd := t.TempDir()
	now := time.Now()
	ensure := func(age time.Duration, argv ...string) sched.SignatureID {
		sig := wqSigOf(t, argv, cwd)
		m.queue.beliefs.Ensure(sig, now.Add(-age))
		return sig.ID()
	}
	ensure(1*time.Minute, "cargo", "build")                                            // unrelated, the newest
	ensure(2*time.Minute, "cargo", "clippy", "-p", "a")                                // shares a step
	ensure(3*time.Minute, "sh", "-c", "cargo clippy -p a && cargo nextest run -p a y") // the same family, the oldest
	self := ensure(0, "sh", "-c", "cargo nextest run -p a x && cargo clippy -p a")

	var got []string
	for _, k := range m.knownBeliefs(m.queue.beliefs.Find(self), "", now) {
		got = append(got, strings.Join(k.Command, " "))
	}
	assert.Equal(t, []string{"sh -c cargo clippy -p a && cargo nextest run -p a y", "cargo clippy -p a", "cargo build"}, got)
}
```

(The `Submission` type name, `wqSubmit`, `wqConfig`, `wqPump`, `wqPriorAnswer` and `wqSettle` are the helpers the existing `core/queue_*_test.go` files use; check their exact names and signatures before running, and adapt `wqRelative`'s parameter type to the type `wqJob` returns.)

Add to `internal/cli/job_sizing_test.go`'s table:

```go
		{core.JobView{Claim: sched.Claim{CPUs: 3, MemoryGB: 6}, Sources: srcs(sched.SourceFamily, sched.SourceFamily, sched.SourceDefault),
			FamilyRuns: 2, Family: "sleep 0", Usual: 2 * time.Minute, UsualFromFamily: true},
			"sized 3c/6G (cores and memory from 2 runs of its family (sleep 0), I/O default); about 2m"},
```

- [ ] **Step 2: Run them to see them fail**

Run: `loom job run -d "core: family tests" -- sh -c 'CGO_ENABLED=0 go test ./core/ -run "Family|KnownBeliefs" && CGO_ENABLED=0 go test ./internal/cli/ -run TestSizingLine'`
Expected: FAIL: sources default, `jv.Family` empty, the advisor asked, the known order by recency, the sizing line saying "usually".

- [ ] **Step 3: Wire the pools**

`core/queue.go`, beside `beliefs`:

```go
	// families memoizes the beliefs' families (family sizing spec §5.2);
	// only the loop reads it.
	families sched.Families
```

`core/queue_beliefs.go`:

```go
// familyPool is sig's family pool among the beliefs (family sizing spec
// §5.2): nil while learning is off, for a signature with no family, and
// for a family with no other runs.
func (q *queueState) familyPool(sig sched.Signature) *sched.Belief {
	if q.beliefsErr != nil {
		return nil
	}
	return q.families.Pool(q.beliefs.Beliefs, sig)
}

// poolOf is the family pool of signature id, through its belief's
// signature: nil when it has no belief.
func (q *queueState) poolOf(id sched.SignatureID) *sched.Belief {
	if b := q.beliefOf(id); b != nil {
		return q.familyPool(b.Signature)
	}
	return nil
}

// familyLabel is the label of b's family, "" for none.
func (q *queueState) familyLabel(b *sched.Belief) string {
	if b == nil {
		return ""
	}
	if f, ok := q.families.Of(b.Signature); ok {
		return f.Label()
	}
	return ""
}

// sizeRecord sets rec's claim from what it asked for, its belief, its
// family pool and p (spec §5.4, family sizing spec §5.3), and reports
// whether anything changed. sig is the submit's signature, whose belief
// may not exist yet; nil from a re-size, which finds it through the
// belief.
func (q *queueState) sizeRecord(rec *sched.Record, p sched.Policy, sig *sched.Signature) bool {
	family := q.poolOf(rec.Signature)
	if sig != nil {
		family = q.familyPool(*sig)
	}
	s := sched.Size(rec.Asked, q.beliefOf(rec.Signature), family, p)
	if rec.Claim == s.Claim && rec.Sources == s.Sources && rec.Learned == s.Learned && rec.FamilyRuns == s.FamilyRuns &&
		rec.AboveCap == s.AboveCap && rec.BelowKB == s.BelowKB {
		return false
	}
	rec.Claim, rec.Sources, rec.Learned, rec.FamilyRuns, rec.AboveCap, rec.BelowKB = s.Claim, s.Sources, s.Learned, s.FamilyRuns, s.AboveCap, s.BelowKB
	return true
}
```

`resizeQueued`: `q.sizeRecord(rec, p, nil)`. `readBeliefs`:

```go
			v := sched.ViewBelief(b.Clone(), q.familyPool(b.Signature), q.policy)
			if label := q.familyLabel(&b); label != "" {
				v.Family, v.FamilySize = label, q.families.Members(q.beliefs.Beliefs, b.Signature)
			}
```

`core/queue_ops.go` `submitJob`:

```go
	if !agentJob {
		q.sizeRecord(&rec, policy, sig)
		…
	}
	if sig != nil && m.askForPrior(s.Claim, q.beliefOf(rec.Signature), q.familyPool(*sig), now) {
```

`core/queue_advisor.go`:

```go
// askForPrior reports whether a job asking for asked, of belief b (nil for
// none yet) and family pool family (nil for none), waits for a prior: the
// advisor is available, a part needs one, and the signature's last prior
// didn't fail within advisorRetryAfter.
func (m *Model) askForPrior(asked sched.Claim, b, family *sched.Belief, now time.Time) bool {
	if m.advisorOff(now) != "" || !sched.NeedsPrior(asked, b, family) {
```

and in `knownBeliefs` (doc: "…up to knownSameRepo of its repo, its family first, then those sharing more of its steps, then the most recently seen, and knownOtherRepos of others running the same program, most recently seen first"):

```go
	fs := &m.queue.families
	kin := func(x, y sched.Belief) int {
		xs, xn := fs.Kin(self, &x)
		ys, yn := fs.Kin(self, &y)
		switch {
		case xs != ys && xs:
			return -1
		case xs != ys:
			return 1
		case xn != yn:
			return cmp.Compare(yn, xn)
		}
		return y.LastSeen.Compare(x.LastSeen)
	}
	slices.SortFunc(same, kin)
	slices.SortFunc(other, recent)
```

(import `cmp`). `core/queue_greedy.go` `usualOf`:

```go
	q := m.queue
	u, _, n, src := sched.Usual(q.beliefOf(r.Signature), q.poolOf(r.Signature), r.Claim.CPUs)
	return u, n > 0 || src == sched.DurationPrior
```

`core/queue_view.go` `queueJobView`:

```go
	b := q.beliefOf(r.Signature)
	var family *sched.Belief
	if b != nil {
		family = q.familyPool(b.Signature)
	}
	var src sched.DurationSource
	jv.Usual, jv.P90, _, src = sched.Usual(b, family, r.Claim.CPUs)
	jv.UsualFromPrior, jv.UsualFromFamily = src == sched.DurationPrior, src == sched.DurationFamily
	jv.Family = q.familyLabel(b)
	jv.OverdueAfter = sched.OverdueAfter(b, r.Claim.CPUs)
```

`core/queue_ladder.go:237`: add `FamilyRuns: rec.FamilyRuns,` after `Learned: rec.Learned,`.

`internal/cli/job_sizing.go` `sizingLine`: `case v.Usual > 0 && (v.UsualFromPrior || v.UsualFromFamily):`.

- [ ] **Step 4: Run the core and CLI suites**

Run: `loom job run -d "core+cli: family" -- sh -c 'CGO_ENABLED=0 go test ./core/ ./internal/cli/ ./ui/ ./sched/'`
Expected: PASS.

- [ ] **Step 5: Format and commit**

```bash
gofmt -l core internal/cli
git commit -m "feat(core,cli): size jobs from their family and rank the advisor's candidates by it" -- core/queue.go core/queue_beliefs.go core/queue_ops.go core/queue_advisor.go core/queue_greedy.go core/queue_view.go core/queue_ladder.go core/queue_family_test.go internal/cli/job_sizing.go internal/cli/job_sizing_test.go
```

## Work package 4: wire reference, rules, docs, gates

**Files:**
- Regenerate: `docs/specs/protocol.md`
- Modify: `sched/CLAUDE.md`, `sched/README.md`, `USAGE.md` (where it names the claim tags), and any doc `git grep -n "learned·"` finds

- [ ] **Step 1: Regenerate the protocol reference**

Run: `loom job run -d "rpc: protocol reference" -- sh -c 'CGO_ENABLED=0 go test ./core/rpc -run TestProtocolReference -update && CGO_ENABLED=0 go test ./core/rpc'`
Expected: PASS; `git diff docs/specs/protocol.md` shows only the new optional fields.

- [ ] **Step 2: Add the `sched/CLAUDE.md` rules** (after the signature rule), then check the budget with `go run ./tools/claudemd -v` (as a job):

```markdown
- **Keep `FamilyOf` a pure function of the signature's repo, folder and command, and give a job no family on any doubt (`splitScript`'s `errScript`, no step but `cd`).** A family lends its runs' claims to a job that has none; a guess that merges two commands lends one's memory to the other, and nothing says so until the host swaps. **Enforced** by `TestFamilyOf` (its no-family rows) and `TestSplitScript` (its failures).
- **Keep the family out of `SignatureID` and out of `recordRun`: a run is recorded only in its own belief, and a family is only a fallback after a job's own runs.** A family in the ID would orphan every belief and split, not merge; a run recorded in its relatives would teach them a command they don't run. **Enforced** by `TestSignature_IDWithAPrerequisite` (its pinned ID) and `TestSize_FromTheFamily` ("the job's own runs win").
- **Read only a job's own runs in `OverdueAfter`, `Predict`/`CurvedRuns`, `Hold`, `ScalingOf` and `SpareWidth`/`Grant`.** A family mixes filtered and full runs: its curves would release memory a job still needs, its durations flag a full suite overdue, and its cores earn no grant. **Enforced** for overdue by `TestUsual_FromTheFamily` and for grants by `TestGrant` (its family case); **Convention** for curves and holds, whose callers pass a belief, never a pool.
```

- [ ] **Step 3: Document the facts**

`sched/README.md`: a "Families" paragraph (what a family is, the pool, the `family` source and its place in the order, what stays exact), linking the spec. `USAGE.md` and any other doc naming `learned·N`: add `family·N` ("from N runs of commands of the same family: the same tools, crates and packages with other filters or trims"). Check `git grep -n "learned·\|learned from" -- '*.md' ':!docs/superpowers'` and `session/loom_context_shared.md` for text agents read.

- [ ] **Step 4: Run every gate**

Run, as one job chain:
`loom job run -d "gates: full suite" -- sh -c 'CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test ./... && GOOS=windows CGO_ENABLED=0 go build ./... && GOOS=darwin CGO_ENABLED=0 go build ./...'`
then `loom job run -d "gates: race" -- sh -c 'CC=clang CGO_ENABLED=1 go test -race ./sched/ ./core/ ./internal/cli/'`, and `gofmt -l $(git ls-files '*.go' | grep -v '^vendor/')` directly.
Expected: all PASS, gofmt prints nothing. golangci-lint is skipped (the dev shell's v2 rejects the repo's config): say so in the report.

- [ ] **Step 5: Commit**

```bash
git commit -m "docs(sched,rpc): family sizing rules, facts and wire reference" -- docs/specs/protocol.md sched/CLAUDE.md sched/README.md USAGE.md <other docs touched>
```

- [ ] **Step 6: The replay check**

Re-run the prototype's replay against the shipped rules: a throwaway Go program in the scratchpad (not the worktree) that loads `~/.loom/jobs/beliefs.json` read-only with `sched.LoadBeliefs`, replays the runs in `Ended` order through `Families.Pool` and `Size`, and prints the share of runs sized from their own runs, their family's, or neither, and the family claims' claim ÷ peak (median, p90, share below 1). Report the numbers beside the spec's §1 table; don't commit the program.
