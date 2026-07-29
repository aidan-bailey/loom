# Crit Absorption Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Absorb [kevindutra/crit](https://github.com/kevindutra/crit) into loom as a native workbench review tab with inline comments and a send-review-to-agent bridge.

**Architecture:** Vendor crit's review store, diff parser, and TUI (all `charm.land/*/v2`, same stack as loom) into `review/`, `review/gitdiff/`, and `ui/review/`, threading an explicit worktree root through every path (crit was cwd-relative). The TUI's `AppModel` becomes an embedded `reviewui.Pane` (sized by the workbench, keys via loom dispatch, themed via `ui.RegisterThemeHook`). A bridge composes stored comments into a prompt and delivers it via `Instance.SendPrompt`.

**Tech Stack:** Go 1.23, Bubble Tea v2 (`charm.land`), `go-gitdiff`, `chroma/v2`, `gofrs/flock`, `google/uuid`, `gopkg.in/yaml.v3` (already present).

**Spec:** `docs/superpowers/specs/2026-07-29-crit-absorption-design.md`

**Upstream pin:** commit `e9e5d1988407802bb11c2989f524a97f65c2fd96`. Every vendor step copies from a clone at this SHA:

```bash
git clone https://github.com/kevindutra/crit /tmp/crit-vendor
git -C /tmp/crit-vendor checkout e9e5d1988407802bb11c2989f524a97f65c2fd96
```

**Repo conventions that bind every task:**
- Build: `CGO_ENABLED=0 go build -o loom`. Tests: `CGO_ENABLED=0 go test ./<pkg>/...`. Race: `CC=clang CGO_ENABLED=1 go test -race ./<pkg>/...`.
- Lint: local golangci-lint is v2 and errors on this repo's v1 config — use `go vet ./...` + `gofmt -w .` instead.
- No model mutation from `tea.Cmd` goroutines. Cmds do I/O and return a msg; `Update` handlers apply it, gated on session title.
- Theme-derived `lipgloss.Style` package vars must be built inside a `ui.RegisterThemeHook` callback (`func init() { RegisterThemeHook(rebuild...) }`), never in var initializers. Color roles only (`ui.Accent`, `ui.Dim`, `ui.Text`, `ui.Rule`, `ui.OK`, `ui.Attention`, `ui.SelectionBg/Fg`...), no literal colors.
- Error wrapping `fmt.Errorf("context: %w", err)`. Module path `github.com/aidan-bailey/loom`.

**Intentional divergences from upstream (record once, in Task 2's NOTICE text):** root-threaded paths instead of cwd; legacy-JSON review migration dropped; `--detach` mode, Claude banner, alt-screen ownership, and the cobra CLI dropped; styles rebuilt on loom theme roles.

---

### Task 1: Dependencies

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add the four new deps**

```bash
go get github.com/bluekeyes/go-gitdiff@v0.8.1 github.com/alecthomas/chroma/v2@v2.23.1 github.com/gofrs/flock@v0.13.0 github.com/google/uuid@v1.6.0
go mod tidy
```

- [ ] **Step 2: Verify the build still passes**

Run: `CGO_ENABLED=0 go build -o /dev/null .`
Expected: exit 0. (`go mod tidy` may prune the unused new deps — that's fine; they re-resolve when the vendored code lands in Task 3. If pruned, re-run the `go get` line in Task 3 Step 5.)

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: add crit vendoring dependencies"
```

### Task 2: NOTICE.md attribution

**Files:**
- Modify: `NOTICE.md`

- [ ] **Step 1: Append the attribution section**

Add to the end of `NOTICE.md`:

```markdown

## Vendored code: crit

The review subsystem (`review/`, `review/gitdiff/`, `ui/review/`) is derived
from [kevindutra/crit](https://github.com/kevindutra/crit) at commit
`e9e5d19`, © kevindutra, distributed under the MIT License (declared in the
upstream README's License section). Substantial modifications were made
during absorption: paths are threaded through an explicit worktree root
instead of the process working directory, the CLI / detached tmux mode /
legacy JSON migration were dropped, and the TUI was re-styled onto loom's
theme system and embedded as a workbench panel.
```

- [ ] **Step 2: Commit**

```bash
git add NOTICE.md
git commit -m "docs: attribute vendored crit code in NOTICE.md"
```

### Task 3: `review/` package (store, types, document, paths, session)

Vendor crit's `internal/review` + `internal/document` as one root-threaded `review` package. Small enough to write fresh from the upstream sources rather than copy+sed. TDD: tests first.

**Files:**
- Create: `review/types.go`, `review/paths.go`, `review/document.go`, `review/store.go`, `review/session.go`
- Test: `review/store_test.go`, `review/paths_test.go`

- [ ] **Step 1: Write the failing tests**

`review/paths_test.go`:

```go
package review

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReviewPath_RootedAndStable(t *testing.T) {
	root := t.TempDir()
	p1 := ReviewPath(root, filepath.Join(root, "plan.md"))
	p2 := ReviewPath(root, filepath.Join(root, "plan.md"))
	assert.Equal(t, p1, p2, "same doc must hash to the same review file")
	assert.True(t, strings.HasPrefix(p1, filepath.Join(root, ".crit", "reviews")),
		"review file must live under <root>/.crit/reviews, got %s", p1)
	assert.True(t, strings.HasSuffix(p1, ".yaml"))

	other := ReviewPath(root, filepath.Join(root, "other.md"))
	assert.NotEqual(t, p1, other)
}

func TestEnsureDirs_WritesSelfIgnoringGitignore(t *testing.T) {
	root := t.TempDir()
	assert.NoError(t, EnsureDirs(root))
	data, err := readFile(t, filepath.Join(root, ".crit", ".gitignore"))
	assert.NoError(t, err)
	assert.Equal(t, "*\n", data)
}
```

`review/store_test.go`:

```go
package review

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func readFile(t *testing.T, path string) (string, error) {
	t.Helper()
	b, err := os.ReadFile(path)
	return string(b), err
}

func TestLoad_MissingReturnsEmptyState(t *testing.T) {
	root := t.TempDir()
	st, err := Load(root, filepath.Join(root, "plan.md"))
	assert.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "plan.md"), st.File)
	assert.Empty(t, st.Comments)
}

func TestSaveLoad_Roundtrip(t *testing.T) {
	root := t.TempDir()
	doc := filepath.Join(root, "plan.md")
	st := &ReviewState{File: doc}
	st.AddComment(Comment{
		ID: "abc12345", Line: 3, EndLine: 5,
		ContentSnippet: "## Rollout", Body: "phase this",
		CreatedAt: time.Now().UTC().Truncate(time.Second),
	})
	assert.NoError(t, Save(root, st))

	got, err := Load(root, doc)
	assert.NoError(t, err)
	assert.Len(t, got.Comments, 1)
	assert.Equal(t, st.Comments[0].Body, got.Comments[0].Body)
	assert.Equal(t, 5, got.Comments[0].EndLine)
}

func TestDeleteComment(t *testing.T) {
	st := &ReviewState{File: "x"}
	st.AddComment(Comment{ID: "a"})
	st.AddComment(Comment{ID: "b"})
	st.DeleteComment("a")
	assert.Len(t, st.Comments, 1)
	assert.Equal(t, "b", st.Comments[0].ID)
}

func TestSessionRoundtrip(t *testing.T) {
	root := t.TempDir()
	s := &CodeReviewSession{Files: []string{"a.go", "b.go"}, DiffBase: "HEAD", CreatedAt: time.Now()}
	assert.NoError(t, SaveSession(root, s))
	got, err := LoadSession(root)
	assert.NoError(t, err)
	assert.Equal(t, []string{"a.go", "b.go"}, got.Files)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `CGO_ENABLED=0 go test ./review/...`
Expected: FAIL — package does not exist / undefined symbols.

- [ ] **Step 3: Write the package**

`review/types.go` (verbatim from upstream `internal/review/types.go`, plus package doc):

```go
// Package review holds the comment model and on-disk store for the
// workbench review tab. Derived from kevindutra/crit (see NOTICE.md);
// unlike upstream, every path is rooted at an explicit worktree root
// rather than the process working directory.
package review

import "time"

type Comment struct {
	ID             string    `json:"id" yaml:"id"`
	Line           int       `json:"line" yaml:"line"`
	EndLine        int       `json:"end_line,omitempty" yaml:"end_line,omitempty"`
	ContentSnippet string    `json:"content_snippet" yaml:"content_snippet"`
	Body           string    `json:"body" yaml:"body"`
	CreatedAt      time.Time `json:"created_at" yaml:"created_at"`
}

type ReviewState struct {
	File     string    `json:"file" yaml:"file"`
	Comments []Comment `json:"comments" yaml:"comments"`
}

func (s *ReviewState) AddComment(c Comment) {
	s.Comments = append(s.Comments, c)
}

func (s *ReviewState) DeleteComment(id string) {
	for i, c := range s.Comments {
		if c.ID == id {
			s.Comments = append(s.Comments[:i], s.Comments[i+1:]...)
			return
		}
	}
}
```

`review/paths.go` (root-threaded rewrite of upstream `internal/document/paths.go`):

```go
package review

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

const (
	critDir    = ".crit"
	reviewsDir = "reviews"
)

// EnsureDirs creates <root>/.crit/reviews and a self-ignoring
// .gitignore so review state never shows up in git status or loom's
// diff stats.
func EnsureDirs(root string) error {
	dir := filepath.Join(root, critDir, reviewsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating directory %s: %w", dir, err)
	}
	gitignorePath := filepath.Join(root, critDir, ".gitignore")
	if _, err := os.Stat(gitignorePath); os.IsNotExist(err) {
		if err := os.WriteFile(gitignorePath, []byte("*\n"), 0o644); err != nil {
			return fmt.Errorf("creating .crit/.gitignore: %w", err)
		}
	}
	return nil
}

// ReviewPath maps a document path to its review file under root. The
// hash covers the absolute doc path so renaming the worktree parent
// (loom never does) is the only way to orphan a review.
func ReviewPath(root, docPath string) string {
	abs, err := filepath.Abs(docPath)
	if err != nil {
		abs = docPath
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(abs)))
	return filepath.Join(root, critDir, reviewsDir, hash+".yaml")
}
```

`review/document.go` (verbatim from upstream `internal/document/document.go`, package renamed):

```go
package review

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
)

type Document struct {
	Path    string
	Content string
	Lines   []string
	Hash    string
}

func LoadDocument(path string) (*Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading document: %w", err)
	}
	content := string(data)
	return &Document{
		Path:    path,
		Content: content,
		Lines:   strings.Split(content, "\n"),
		Hash:    fmt.Sprintf("%x", sha256.Sum256(data)),
	}, nil
}

func (d *Document) LineAt(n int) string {
	if n < 1 || n > len(d.Lines) {
		return ""
	}
	return d.Lines[n-1]
}

func (d *Document) LineCount() int {
	return len(d.Lines)
}
```

(Upstream's `Load` is renamed `LoadDocument` — it now shares a package with the review-state `Load`.)

`review/store.go` (root-threaded; legacy JSON migration dropped):

```go
package review

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/gofrs/flock"
	"gopkg.in/yaml.v3"
)

// Load reads the review state for docPath, returning an empty state
// when none exists yet.
func Load(root, docPath string) (*ReviewState, error) {
	data, err := os.ReadFile(ReviewPath(root, docPath))
	if err != nil {
		if os.IsNotExist(err) {
			return &ReviewState{File: docPath, Comments: []Comment{}}, nil
		}
		return nil, fmt.Errorf("reading review: %w", err)
	}
	var state ReviewState
	if err := yaml.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parsing review YAML: %w", err)
	}
	return &state, nil
}

// Save writes state atomically (tmp + rename) under a flock so a
// concurrently running crit CLI in the same worktree can't interleave.
func Save(root string, state *ReviewState) error {
	if err := EnsureDirs(root); err != nil {
		return err
	}
	reviewPath := ReviewPath(root, state.File)
	lockPath := reviewPath + ".lock"

	fileLock := flock.New(lockPath)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	locked, err := fileLock.TryLockContext(ctx, 100*time.Millisecond)
	if err != nil {
		return fmt.Errorf("acquiring review lock: %w", err)
	}
	if !locked {
		return fmt.Errorf("review file %s is locked by another process", lockPath)
	}
	defer func() {
		_ = fileLock.Unlock()
		_ = os.Remove(lockPath)
	}()

	data, err := yaml.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshaling review: %w", err)
	}
	tmpPath := reviewPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("writing temp review file: %w", err)
	}
	if err := os.Rename(tmpPath, reviewPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("renaming temp review file: %w", err)
	}
	return nil
}
```

`review/session.go` (root-threaded from upstream `internal/review/session.go`):

```go
package review

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// CodeReviewSession tracks which files belong to the current code
// review. Written for interop: an agent running the crit CLI in the
// worktree reads the same manifest.
type CodeReviewSession struct {
	Files     []string  `yaml:"files"`
	DiffBase  string    `yaml:"diff_base"`
	CreatedAt time.Time `yaml:"created_at"`
}

func sessionPath(root string) string {
	return filepath.Join(root, critDir, "code-review.yaml")
}

func SaveSession(root string, session *CodeReviewSession) error {
	if err := EnsureDirs(root); err != nil {
		return err
	}
	data, err := yaml.Marshal(session)
	if err != nil {
		return fmt.Errorf("marshaling session: %w", err)
	}
	if err := os.WriteFile(sessionPath(root), data, 0o644); err != nil {
		return fmt.Errorf("writing session file: %w", err)
	}
	return nil
}

func LoadSession(root string) (*CodeReviewSession, error) {
	data, err := os.ReadFile(sessionPath(root))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no active code review session")
		}
		return nil, fmt.Errorf("reading session: %w", err)
	}
	var session CodeReviewSession
	if err := yaml.Unmarshal(data, &session); err != nil {
		return nil, fmt.Errorf("parsing session: %w", err)
	}
	return &session, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=0 go test ./review/...`
Expected: PASS (all 6 tests).

- [ ] **Step 5: Verify git-cleanliness of the store (spec §3)**

Append to `review/store_test.go`:

```go
func TestStore_InvisibleToGitStatus(t *testing.T) {
	root := t.TempDir()
	run := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		assert.NoError(t, err, string(out))
		return string(out)
	}
	run("init", "-q")
	st := &ReviewState{File: filepath.Join(root, "plan.md")}
	st.AddComment(Comment{ID: "a", Line: 1, Body: "x", CreatedAt: time.Now()})
	assert.NoError(t, Save(root, st))
	assert.Empty(t, strings.TrimSpace(run("status", "--porcelain")),
		".crit must be fully self-ignored")
}
```

Add `"os/exec"` and `"strings"` to the test file's imports. Run: `CGO_ENABLED=0 go test ./review/ -run InvisibleToGit -v`
Expected: PASS. (This satisfies the spec's ignore requirement via the vendored self-ignoring `.gitignore` — no `.git/info/exclude` machinery needed. Note the simplification in the commit message.)

- [ ] **Step 6: Commit**

```bash
gofmt -w review/
git add review/
git commit -m "feat(review): vendor crit review store, root-threaded

.crit self-ignores via its own .gitignore, so no info/exclude step is
needed (simplifies spec §3)."
```

### Task 4: Prompt composition (`review.ComposePrompt`)

The bridge's pure core: comments → agent prompt. TDD.

**Files:**
- Create: `review/prompt.go`
- Test: `review/prompt_test.go`

- [ ] **Step 1: Write the failing test**

`review/prompt_test.go`:

```go
package review

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestComposePrompt_Empty(t *testing.T) {
	assert.Equal(t, "", ComposePrompt("/wt", nil))
	assert.Equal(t, "", ComposePrompt("/wt", []*ReviewState{{File: "/wt/a.md"}}))
}

func TestComposePrompt_FormatsCommentsRelativeToRoot(t *testing.T) {
	states := []*ReviewState{
		{File: "/wt/docs/plan.md", Comments: []Comment{
			{ID: "a", Line: 3, ContentSnippet: "## Rollout", Body: "split into two phases"},
			{ID: "b", Line: 10, EndLine: 14, Body: "this section contradicts the goals"},
		}},
		{File: "/wt/notes.md", Comments: []Comment{
			{ID: "c", Line: 1, Body: "wrong title"},
		}},
	}
	got := ComposePrompt("/wt", states)

	assert.True(t, strings.HasPrefix(got, "Please address the following review comments"))
	assert.Contains(t, got, "docs/plan.md:3\n> ## Rollout\nsplit into two phases")
	assert.Contains(t, got, "docs/plan.md:10-14\nthis section contradicts the goals")
	assert.Contains(t, got, "notes.md:1\nwrong title")
	// Single-line comment with no snippet has no quote line.
	assert.NotContains(t, got, "notes.md:1\n>")
}

func TestComposePrompt_FileOutsideRootKeptVerbatim(t *testing.T) {
	got := ComposePrompt("/wt", []*ReviewState{
		{File: "relative.md", Comments: []Comment{{ID: "a", Line: 2, Body: "x"}}},
	})
	assert.Contains(t, got, "relative.md:2")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=0 go test ./review/ -run ComposePrompt`
Expected: FAIL — `ComposePrompt` undefined.

- [ ] **Step 3: Implement**

`review/prompt.go`:

```go
package review

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ComposePrompt formats review comments as a single instruction prompt
// for the session's agent. File paths are shown relative to root (the
// agent's working directory). Returns "" when there are no comments.
func ComposePrompt(root string, states []*ReviewState) string {
	total := 0
	for _, s := range states {
		total += len(s.Comments)
	}
	if total == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("Please address the following review comments. Work through them one by one and confirm each as resolved.\n")
	for _, s := range states {
		file := s.File
		if rel, err := filepath.Rel(root, s.File); err == nil && !strings.HasPrefix(rel, "..") {
			file = rel
		}
		for _, c := range s.Comments {
			b.WriteString("\n")
			if c.EndLine != 0 && c.EndLine != c.Line {
				fmt.Fprintf(&b, "%s:%d-%d\n", file, c.Line, c.EndLine)
			} else {
				fmt.Fprintf(&b, "%s:%d\n", file, c.Line)
			}
			if c.ContentSnippet != "" {
				fmt.Fprintf(&b, "> %s\n", c.ContentSnippet)
			}
			fmt.Fprintf(&b, "%s\n", c.Body)
		}
	}
	return b.String()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=0 go test ./review/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w review/
git add review/prompt.go review/prompt_test.go
git commit -m "feat(review): compose review comments into an agent prompt"
```

### Task 5: `review/gitdiff/` package

Vendor `internal/git/diff.go` with a `dir string` threaded through every git invocation. This file is copied, then edited — it is ~235 lines of parsing logic that must stay byte-faithful except for the listed edits.

**Files:**
- Create: `review/gitdiff/diff.go` (copied), `review/gitdiff/diff_test.go` (new)

- [ ] **Step 1: Copy the file**

```bash
mkdir -p review/gitdiff
cp /tmp/crit-vendor/internal/git/diff.go review/gitdiff/diff.go
```

- [ ] **Step 2: Apply the mechanical edits**

In `review/gitdiff/diff.go`:

1. `package git` → `package gitdiff`.
2. Add `dir string` as the **first parameter** of every exported function and every unexported helper that shells out: `ChangedFiles(dir string)`, `ChangedFilesFrom(dir, ref string)`, `DiffFile(dir, path, ref string)`, `diffNameStatus(dir, ref string)`, `detectBinaryFiles(dir, ref string)`, `untrackedFiles(dir string)`, and any other helper containing `exec.Command`. Update all internal call sites to pass `dir` through.
3. Every `exec.Command("git", <args>...)` becomes `exec.Command("git", append([]string{"-C", dir}, <args>...)...)`. Where args are built as a slice already, prepend `"-C", dir` to that slice instead.
4. Keep everything else — status parsing, `DiffInfo`/`DeletedLine`, hunk walking — byte-identical to upstream.

Add at the top of the file, under the package clause:

```go
// Package gitdiff parses git diffs into per-file changed-line maps for
// the review tab. Derived from kevindutra/crit internal/git (see
// NOTICE.md); modified to run against an explicit repo dir via `git -C`
// instead of the process working directory. Kept separate from
// session/git, which shells out for worktree lifecycle operations.
```

- [ ] **Step 3: Write the test (temp-repo fixture)**

`review/gitdiff/diff_test.go`:

```go
package gitdiff

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// initRepo creates a repo with one committed file, then applies
// working-tree changes: modify a.txt, add new.txt.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	run("init", "-q")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\nthree\n"), 0o644))
	run("add", ".")
	run("commit", "-q", "-m", "init")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\nTWO\nthree\nfour\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "new.txt"), []byte("hi\n"), 0o644))
	return dir
}

func TestChangedFiles_RunsAgainstDirNotCwd(t *testing.T) {
	dir := initRepo(t)
	files, err := ChangedFiles(dir)
	assert.NoError(t, err)

	byPath := map[string]ChangeStatus{}
	for _, f := range files {
		byPath[f.Path] = f.Status
	}
	assert.Equal(t, StatusModified, byPath["a.txt"])
	assert.Equal(t, StatusUntracked, byPath["new.txt"])
}

func TestDiffFile_ChangedLines(t *testing.T) {
	dir := initRepo(t)
	info, err := DiffFile(dir, "a.txt", "HEAD")
	assert.NoError(t, err)
	require.NotNil(t, info)
	assert.True(t, info.ChangedLines[2], "line 2 (TWO) modified")
	assert.True(t, info.ChangedLines[4], "line 4 (four) added")
	assert.False(t, info.ChangedLines[1])
}
```

- [ ] **Step 4: Run tests, iterate until green**

Run: `CGO_ENABLED=0 go test ./review/gitdiff/ -v`
Expected: PASS. If `DiffFile`'s untracked-file handling differs (upstream may diff untracked via `/dev/null`), read the copied code and match the test to actual upstream semantics — the test's job is to prove dir-threading works from a different cwd, not to re-spec upstream parsing.

- [ ] **Step 5: Vet, format, commit**

```bash
gofmt -w review/gitdiff/ && go vet ./review/...
git add review/gitdiff/
git commit -m "feat(review): vendor crit git diff parser, dir-threaded"
```

### Task 6: `ui/review/` — vendor the TUI as an embeddable Pane

The heart of the absorption: crit's `AppModel` (2,008-line `app.go` + `filetab.go`, `styles.go`, `highlight.go`, `keys.go`, `messages.go`) becomes `reviewui.Pane`, an embedded component. Copy first, adapt in reviewable slices, keep the vendored logic byte-faithful wherever it isn't on the edit list.

**Files:**
- Create: `ui/review/app.go`, `ui/review/filetab.go`, `ui/review/styles.go`, `ui/review/highlight.go`, `ui/review/keys.go`, `ui/review/messages.go` (copied), `ui/review/pane.go` (new)
- Test: `ui/review/pane_test.go` (adapted from upstream `app_test.go` + new)

- [ ] **Step 1: Copy and re-package**

```bash
mkdir -p ui/review
for f in app.go filetab.go styles.go highlight.go keys.go messages.go app_test.go; do
  cp /tmp/crit-vendor/internal/tui/$f ui/review/
done
mv ui/review/app_test.go ui/review/pane_test.go
```

In all copied files: `package tui` → `package reviewui`; imports `github.com/kevindutra/crit/internal/review` → `github.com/aidan-bailey/loom/review`, `github.com/kevindutra/crit/internal/document` → same `review` package (merge the two import aliases; `document.Load` → `review.LoadDocument`, `document.Document` → `review.Document`), `gitpkg "github.com/kevindutra/crit/internal/git"` → `gitpkg "github.com/aidan-bailey/loom/review/gitdiff"`.

- [ ] **Step 2: Thread the root + title through the model**

In `ui/review/app.go`:

- Add fields to `AppModel`: `title string` (owning session, for msg gating) and `root string` (worktree root).
- `NewApp(filePath string)` → `NewDocPane(title, root, filePath string) *Pane` and `NewCodeReviewApp(files []gitpkg.FileChange, ref string)` → `NewCodePane(title, root string, files []gitpkg.FileChange, ref string) *Pane` (Pane defined in Step 4; both constructors set `title`/`root`). Inside `NewCodePane`, `gitpkg.DiffFile(f.Path, ref)` → `gitpkg.DiffFile(root, f.Path, ref)`.
- Every `review.Load(path)` → `review.Load(m.root, path)`; every `review.Save(state)` call **site is deleted** — persistence moves to Cmds (Step 5).
- `document.Load(tab.path)` → `review.LoadDocument(tab.path)`; tab paths for code mode are joined as `filepath.Join(root, f.Path)` at construction so all doc I/O is absolute.

- [ ] **Step 3: Replace the program-isms**

In `ui/review/app.go`:

- Delete `Init()`, the `tea.WindowSizeMsg` and `tea.BackgroundColorMsg` cases in `Update`, the `detached` field and `CRIT_DETACHED` read, and the `claudeStatusBar` banner branch in `View`.
- `View() tea.View` → `view() string`: return the composed `full` string directly (drop `tea.NewView`/`AltScreen`). The error and loading branches return their strings.
- The `keys.Quit` branch in `handleKeyPress` (which looped `review.Save` + returned `tea.Quit`): replace with setting a new field `exitRequested bool` and returning the persist Cmd for all dirty tabs (Step 5). Remove `ctrl+c` from the Quit binding in `keys.go` (loom reserves it): `key.WithKeys("q")`.
- `Update(msg tea.Msg) (tea.Model, tea.Cmd)` → `update(msg tea.Msg) tea.Cmd` on a **pointer receiver** (the model is owned by the Pane, not by a Program; value-receiver copying is what forces upstream's return-model dance). `handleKeyPress`, `handleTextModal`, `handleTabSearch` likewise return only `tea.Cmd`. Mechanical: every `return m, cmd` → `return cmd`.
- `loadDocuments()` currently only validates readability in the Cmd and does the real `document.Load`/`review.Load` I/O inside the `docRenderedMsg` handler — on the Update goroutine. Move the I/O into the Cmd (loom rule): the Cmd reads every tab's document and review state into a `LoadedMsg{Title string, Docs []LoadedDoc, Err error}` (`LoadedDoc{Path string, Doc *review.Document, State *review.ReviewState}`, defined in `messages.go`), and the msg handler only installs the results and calls `rebuildContent()`/`updateCommentSidebar()`. Delete `docRenderedMsg`.

- [ ] **Step 4: Write the Pane wrapper**

`ui/review/pane.go`:

```go
package reviewui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/aidan-bailey/loom/review"
)

// Pane embeds the vendored crit review model as a workbench panel.
// The app layer owns routing: SetSize before View, HandleKey for
// keystrokes (handled=false means the key is the workbench's),
// HandleMsg for LoadedMsg/SavedMsg deliveries.
type Pane struct {
	m AppModel
}

func (p *Pane) Title() string { return p.m.title }
func (p *Pane) Root() string  { return p.m.root }

// SetSize resizes the pane's viewports. Mirrors the vendored
// WindowSizeMsg handling: recalculateLayout + content rebuild.
func (p *Pane) SetSize(w, h int) {
	p.m.width, p.m.height = w, h
	p.m.recalculateLayout()
	if len(p.m.tabs) > 0 && p.m.tab().state != nil {
		p.m.rebuildContent()
	}
}

// LoadCmd reads all documents and review states off the Update
// goroutine. Deliver the result back via HandleMsg.
func (p *Pane) LoadCmd() tea.Cmd { return p.m.loadDocuments() }

// HandleKey routes a keystroke. handled=false → the workbench owns the
// key (only esc while idle falls through, so workbench-exit works).
// exit=true → the user pressed q: persist Cmd returned, leave the tab.
func (p *Pane) HandleKey(msg tea.KeyPressMsg) (cmd tea.Cmd, handled, exit bool) {
	if !p.m.busy() && msg.String() == "esc" {
		return nil, false, false
	}
	cmd = p.m.handleKeyPress(msg)
	exit = p.m.exitRequested
	p.m.exitRequested = false
	return cmd, true, exit
}

// HandleMsg applies async deliveries (LoadedMsg, textarea blink, ...).
func (p *Pane) HandleMsg(msg tea.Msg) tea.Cmd { return p.m.update(msg) }

func (p *Pane) View() string { return p.m.view() }

// Busy reports whether the pane is in a capture-all state (comment
// modal, tab search, visual selection).
func (p *Pane) Busy() bool { return p.m.busy() }

// States exposes every tab's review state for the send bridge.
func (p *Pane) States() []*review.ReviewState {
	out := make([]*review.ReviewState, 0, len(p.m.tabs))
	for i := range p.m.tabs {
		if st := p.m.tabs[i].state; st != nil {
			out = append(out, st)
		}
	}
	return out
}

// CommentCount sums comments across tabs.
func (p *Pane) CommentCount() int {
	n := 0
	for _, s := range p.States() {
		n += len(s.Comments)
	}
	return n
}
```

Add to `app.go`:

```go
// busy reports a capture-all input state.
func (m *AppModel) busy() bool {
	if m.modal != noModal || m.tabSearching {
		return true
	}
	return len(m.tabs) > 0 && m.tab().selecting
}
```

Constructors return `*Pane` wrapping the built `AppModel` (adapt the tails of the vendored `NewApp`/`NewCodeReviewApp` bodies: `return &Pane{m: AppModel{...}}`).

- [ ] **Step 5: Async persistence**

In `ui/review/messages.go` add:

```go
// SavedMsg reports a background review-state write.
type SavedMsg struct {
	Title string
	Err   error
}
```

In `app.go` add:

```go
// persistCmd snapshots state and writes it off the Update goroutine.
// The comments slice is copied — the model keeps mutating its own.
func (m *AppModel) persistCmd(state *review.ReviewState) tea.Cmd {
	snapshot := review.ReviewState{
		File:     state.File,
		Comments: append([]review.Comment(nil), state.Comments...),
	}
	root, title := m.root, m.title
	return func() tea.Msg {
		return SavedMsg{Title: title, Err: review.Save(root, &snapshot)}
	}
}
```

`modalSubmit()` and `modalDelete()` become `func (m *AppModel) modalSubmit() tea.Cmd` (and `modalDelete`), ending with `return m.persistCmd(t.state)` instead of `review.Save(t.state)`; their callers in `handleTextModal` return that Cmd. The exit path (`q`) returns `tea.Batch` of `persistCmd` for every tab with a non-nil state before setting `exitRequested = true`.

- [ ] **Step 6: Theme-hook the styles**

Rewrite `ui/review/styles.go`'s package-level style vars: keep every style **name and property** but move all constructions into a hook and swap the colors for loom roles:

```go
import "github.com/aidan-bailey/loom/ui"

var (
	subtle  = ui.Dim
	accent  = ui.Accent
	success = ui.OK
	warning = ui.Attention
	muted   = ui.Text
	// ... style vars declared (not initialized) ...
)

func init() { ui.RegisterThemeHook(rebuildReviewStyles) }

func rebuildReviewStyles() {
	subtle, accent, success, warning, muted = ui.Dim, ui.Accent, ui.OK, ui.Attention, ui.Text
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(ui.SelectionFg).Background(accent).Padding(0, 1)
	// ... every remaining style var from the vendored file, same
	// properties, colors mapped: subtle→ui.Dim, accent→ui.Accent,
	// success→ui.OK, warning→ui.Attention, muted→ui.Text,
	// BrightWhite→ui.SelectionFg ...
}
```

Delete `initAdaptiveStyles` and `claudeStatusBar`; move the styles it managed into `rebuildReviewStyles` using the dark-branch construction (loom themes are dark-first; the theme system owns light/dark now). Functions in the vendored code that referenced `initAdaptiveStyles` (the deleted `BackgroundColorMsg` case) are already gone from Step 3.

- [ ] **Step 7: Make it compile, adapt the vendored test**

Run: `CGO_ENABLED=0 go build ./ui/review/` and fix residual compile errors from the receiver/return-shape changes — mechanical only; do not redesign vendored logic.

Rewrite `ui/review/pane_test.go` around the Pane API (upstream `app_test.go` drove `AppModel` as a `tea.Model`; keep its scenarios). Minimum coverage:

```go
package reviewui

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aidan-bailey/loom/review"
)

func loadedDocPane(t *testing.T) *Pane {
	t.Helper()
	root := t.TempDir()
	doc := filepath.Join(root, "plan.md")
	require.NoError(t, os.WriteFile(doc, []byte("# Title\n\nbody line\n"), 0o644))
	p := NewDocPane("sess", root, doc)
	p.SetSize(100, 40)
	msg := p.LoadCmd()()          // run the load Cmd synchronously
	p.HandleMsg(msg)
	return p
}

func key(s string) tea.KeyPressMsg { return tea.KeyPressMsg{Text: s, Code: rune(s[0])} }

func TestPane_LoadsAndRenders(t *testing.T) {
	p := loadedDocPane(t)
	v := p.View()
	assert.Contains(t, v, "plan.md")
	assert.NotContains(t, v, "Loading")
}

func TestPane_EscFallsThroughWhenIdle(t *testing.T) {
	p := loadedDocPane(t)
	_, handled, _ := p.HandleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.False(t, handled, "idle esc belongs to the workbench")
}

func TestPane_CommentRoundtripPersists(t *testing.T) {
	p := loadedDocPane(t)
	_, handled, _ := p.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter}) // open comment modal
	require.True(t, handled)
	require.True(t, p.Busy())
	for _, r := range "looks wrong" {
		p.HandleKey(tea.KeyPressMsg{Text: string(r), Code: r})
	}
	cmd, _, _ := p.HandleKey(tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 's'}) // ctrl+s submits
	require.NotNil(t, cmd)
	saved := cmd() // run persist Cmd synchronously
	assert.IsType(t, SavedMsg{}, saved)
	assert.NoError(t, saved.(SavedMsg).Err)

	st, err := review.Load(p.Root(), p.States()[0].File)
	require.NoError(t, err)
	require.Len(t, st.Comments, 1)
	assert.Equal(t, "looks wrong", st.Comments[0].Body)
	assert.Equal(t, 1, st.Comments[0].Line)
}

func TestPane_QExitsWithPersist(t *testing.T) {
	p := loadedDocPane(t)
	cmd, handled, exit := p.HandleKey(key("q"))
	assert.True(t, handled)
	assert.True(t, exit)
	require.NotNil(t, cmd)
}

func TestPane_CommentCount(t *testing.T) {
	p := loadedDocPane(t)
	assert.Equal(t, 0, p.CommentCount())
}
```

Check the exact `tea.KeyPressMsg` construction against an existing loom test (`grep -r "KeyPressMsg{" app/ ui/ | head`) and match that idiom — the fields above follow bubbletea v2 but loom's tests are the authority.

- [ ] **Step 8: Run all touched packages' tests**

Run: `CGO_ENABLED=0 go test ./ui/review/ ./review/... -v`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
gofmt -w ui/review/ && go vet ./ui/review/
git add ui/review/
git commit -m "feat(ui/review): vendor crit TUI as embeddable review pane"
```

### Task 7: Workbench review tab (UI layer)

**Import-cycle constraint (why an interface):** `ui/review` imports `ui` for theme roles (`ui.RegisterThemeHook`, `ui.Dim`, ...), so package `ui` must NOT import `ui/review` — that would be a cycle. The workbench therefore holds a small render interface; the app layer (which imports both) owns the concrete `*reviewui.Pane`.

**Files:**
- Modify: `ui/workbench.go`
- Test: `ui/workbench_test.go`

- [ ] **Step 1: Write the failing test**

Append to `ui/workbench_test.go` (stub type, NOT the real pane — see the cycle note):

```go
// stubReviewPane satisfies ui.ReviewPane for workbench tests.
type stubReviewPane struct{ w, h int }

func (s *stubReviewPane) SetSize(w, h int) { s.w, s.h = w, h }
func (s *stubReviewPane) View() string     { return "REVIEW-PANE-CONTENT" }

func TestWorkbench_ReviewTab(t *testing.T) {
	w := newTestWorkbench()
	w.SetSize(100, 40)
	assert.Nil(t, w.Review())

	w.SetTab(WbTabReview)
	assert.Contains(t, w.String(), "no active review", "empty state before SetReview")

	p := &stubReviewPane{}
	w.SetReview(p)
	assert.Same(t, p, w.Review().(*stubReviewPane))
	assert.Contains(t, w.tabsRow(), "5 review")
	assert.Contains(t, w.String(), "REVIEW-PANE-CONTENT")
	assert.Greater(t, p.w, 0, "SetReview must size the pane")

	// SetSession to a different session clears the review pane.
	w.SetSession("other", t.TempDir())
	assert.Nil(t, w.Review())
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `CGO_ENABLED=0 go test ./ui/ -run Workbench_ReviewTab`
Expected: FAIL — `WbTabReview`, `Review`, `SetReview` undefined.

- [ ] **Step 3: Implement**

In `ui/workbench.go`:

1. Add the tab constant after `WbTabTerminal`: `WbTabReview` (value 4).
2. Add the interface and field (no `ui/review` import — cycle):

```go
// ReviewPane is the workbench's view of the review panel. The concrete
// *reviewui.Pane lives in the app layer: ui/review imports ui for
// theme roles, so package ui must not import it back.
type ReviewPane interface {
	SetSize(width, height int)
	View() string
}
```

Add field to `Workbench`: `reviewPane ReviewPane`.

3. Accessors:

```go
func (w *Workbench) Review() ReviewPane { return w.reviewPane }

// SetReview installs (or clears, with nil) the review pane and sizes it.
func (w *Workbench) SetReview(p ReviewPane) {
	w.reviewPane = p
	w.applySizes()
}
```

4. `SetSession`: append `w.reviewPane = nil` to the retarget branch (a review belongs to one session's snapshot).
5. `applySizes`: after the diff sizing line add

```go
	if w.reviewPane != nil {
		w.reviewPane.SetSize(iw, ih)
	}
```

6. `tabsRow` names: `[]string{"1 markdown", "2 diff", "3 files", "4 terminal", "5 review"}`.
7. `body()`: add before `default`:

```go
	case WbTabReview:
		if w.reviewPane == nil {
			return wbFileStyle.Render("no active review — press c on a markdown doc (or 5 again on the diff) to start one")
		}
		return w.reviewPane.View()
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `CGO_ENABLED=0 go test ./ui/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w ui/ && git add ui/workbench.go ui/workbench_test.go
git commit -m "feat(ui): workbench review tab hosting the review pane"
```

### Task 8: App wiring — entry, key routing, freeze/resume, teardown

**Files:**
- Modify: `app/workbench.go` (`handleWorkbenchKey`, `cleanupWorkbench`, new helpers), `app/app.go` (Update cases for `reviewui.LoadedMsg`/`reviewui.SavedMsg` — put them beside the existing `wbScanMsg`/`wbLoadMsg` cases; grep for `case wbScanMsg`)
- Test: `app/workbench_review_test.go` (new; model on `app/workbench_flow_test.go` and `app/workbench_mode_test.go` for the `home`-model test harness — reuse their setup helper)

- [ ] **Step 1: Write the failing tests**

`app/workbench_review_test.go` — reuse the existing workbench test constructor (read `app/workbench_flow_test.go` first and copy its `home` setup; the tests below assume a helper `newWorkbenchTestHome(t)` giving a home in workbench mode with a started fake instance whose worktree is a temp dir containing `plan.md`, loaded into the markdown pane):

```go
package app

// Scenarios (write with the harness found in workbench_flow_test.go):
//
// TestWorkbenchReview_CFreezesAndOpens
//   - markdown tab showing plan.md, follow mode on
//   - press "c" → workbench tab == ui.WbTabReview, Review() != nil,
//     Markdown.Following() == false, returned Cmd non-nil (LoadCmd)
//
// TestWorkbenchReview_CIgnoredWhileEditing
//   - StartEdit() first; press "c" → tab unchanged, Review() == nil
//
// TestWorkbenchReview_QReturnsToMarkdownAndResumesFollow
//   - enter review via "c", deliver LoadCmd msg, press "q"
//   - tab back to WbTabMarkdown, Review() == nil,
//     Markdown.Following() == true
//
// TestWorkbenchReview_EscExitsWorkbenchWhenIdle
//   - enter review, deliver load, press "esc" → viewMode == viewFocus
//     (pane reported handled=false; workbench exit ran; cleanup cleared
//     the review pane)
//
// TestWorkbenchReview_CleanupClearsPane
//   - enter review, then call m.cleanupWorkbench() directly
//   - Review() == nil and Markdown follow restored
```

Write these as real tests, not comments — the comment block above is the scenario list to implement.

- [ ] **Step 2: Run to verify failure**

Run: `CGO_ENABLED=0 go test ./app/ -run WorkbenchReview`
Expected: FAIL (behavior missing).

- [ ] **Step 3: Implement routing in `handleWorkbenchKey`**

First add the concrete-pane field to `home` (in `app/app.go`, near the other `wb*` fields — grep `wbPrevTerminalHidden`):

```go
	// wbReview is the concrete review pane; the workbench itself only
	// holds the ui.ReviewPane render interface (import-cycle rule).
	// Invariant: nil iff m.workbench.Review() is nil.
	wbReview *reviewui.Pane
```

At the **top** of `handleWorkbenchKey` (before the `md.Editing()` block), insert:

```go
	// Review tab owns its keys. The pane declines idle-esc
	// (handled=false) so workbench exit still works; q inside the pane
	// exits the review back to the markdown tab.
	if m.workbench.Tab() == ui.WbTabReview && m.wbReview != nil {
		rv := m.wbReview
		if msg.String() == "S" && !rv.Busy() {
			return m, m.sendReviewCmd(), true
		}
		if cmd, handled, exit := rv.HandleKey(msg); handled {
			if exit {
				return m, tea.Batch(cmd, m.closeReview()), true
			}
			return m, cmd, true
		}
		// fall through: idle esc → workbench exit below.
	}
```

Add the entry key in the main `switch msg.String()` (alongside `"e"`/`"f"`):

```go
	case "c":
		if m.workbench.Tab() == ui.WbTabMarkdown && !md.Editing() && md.Path() != "" {
			return m, m.openDocReview(md.Path()), true
		}
		return m, nil, true
	case "5":
		if m.wbReview != nil {
			m.workbench.SetTab(ui.WbTabReview)
		} else {
			m.errBox.SetInfo("no active review — press c on a markdown doc to start one")
		}
		return m, nil, true
```

- [ ] **Step 4: Implement the helpers in `app/workbench.go`**

```go
// openDocReview freezes the markdown pane (same contract as edit mode:
// follow-mode pauses so line anchors can't rot under a live agent) and
// opens the review tab on docPath.
func (m *home) openDocReview(docPath string) tea.Cmd {
	sel := m.list.GetSelectedInstance()
	if sel == nil || sel.Paused() {
		return nil
	}
	root := sel.GetWorktreePath()
	if root == "" {
		return nil
	}
	m.workbench.Markdown.SetFollowing(false)
	p := reviewui.NewDocPane(sel.Title, root, docPath)
	m.wbReview = p
	m.workbench.SetReview(p)
	m.workbench.SetTab(ui.WbTabReview)
	return p.LoadCmd()
}

// closeReview leaves the review tab back to markdown and resumes
// follow mode (the freeze counterpart of openDocReview).
func (m *home) closeReview() tea.Cmd {
	m.wbReview = nil
	m.workbench.SetReview(nil)
	m.workbench.SetTab(ui.WbTabMarkdown)
	m.workbench.Markdown.SetFollowing(true)
	return m.workbenchScanCmd()
}
```

In `cleanupWorkbench`, after `m.workbench.Markdown.CancelEdit()` add:

```go
		m.wbReview = nil
		m.workbench.SetReview(nil)
		m.workbench.Markdown.SetFollowing(true)
```

`Workbench.SetSession` (Task 7) also clears its interface field on retarget — the app must mirror that: in `enterWorkbench` (`app/workbench.go`), after `m.workbench.SetSession(...)`, add `m.wbReview = nil` (SetSession only clears on an actual title change, but a stale concrete pane for the same title is impossible — cleanup runs on every exit path — so unconditional nil-ing here is safe and keeps the invariant trivially true).

(Idempotent, and SetSession also clears — belt and suspenders, matching the choke-point comment above the function.)

- [ ] **Step 5: Route the async messages**

In `app/app.go`'s `Update`, next to the existing `case wbScanMsg:` block:

```go
	case reviewui.LoadedMsg:
		if t, ok := m.wbCurrentTitle(); ok && t == msg.Title && m.wbReview != nil {
			return m, m.wbReview.HandleMsg(msg)
		}
		return m, nil
	case reviewui.SavedMsg:
		if msg.Err != nil {
			return m, m.handleError(msg.Err)
		}
		return m, nil
```

(`SavedMsg` is deliberately not title-gated for success — it's fire-and-forget; errors surface regardless of navigation.)

- [ ] **Step 6: Run the tests**

Run: `CGO_ENABLED=0 go test ./app/ -run WorkbenchReview -v` then `CGO_ENABLED=0 go test ./app/`
Expected: PASS, no regressions in the existing workbench flow tests.

- [ ] **Step 7: Commit**

```bash
gofmt -w app/ && go vet ./app/
git add app/
git commit -m "feat(app): workbench review tab wiring — open, freeze, route, teardown"
```

### Task 9: The send bridge

**Files:**
- Modify: `app/workbench.go` (add `sendReviewCmd`)
- Test: `app/workbench_review_test.go`

- [ ] **Step 1: Write the failing tests**

Append scenarios (same harness):

```go
// TestWorkbenchReview_SendNoComments
//   - enter review, deliver load (zero comments), press "S"
//   - no confirmation overlay opened; errBox shows "no review comments"
//
// TestWorkbenchReview_SendOpensConfirm
//   - seed the store: review.Save(root, state-with-2-comments) BEFORE
//     entering review, then "c" + deliver load, press "S"
//   - m.state == stateConfirm (or the repo's confirm-overlay equivalent
//     — mirror how existing confirmTask flows are asserted; grep
//     "confirmTask" usages in app tests)
```

- [ ] **Step 2: Run to verify failure**

Run: `CGO_ENABLED=0 go test ./app/ -run WorkbenchReview_Send`
Expected: FAIL.

- [ ] **Step 3: Implement**

In `app/workbench.go`:

```go
// sendReviewCmd composes the review comments into a prompt and, after
// confirmation, sends it to the session's agent pane. The prompt is
// composed at press time — the confirm overlay's Sync step runs on the
// main goroutine (SendPrompt has its own locking, same precedent as
// the quick-input bar).
func (m *home) sendReviewCmd() tea.Cmd {
	sel := m.list.GetSelectedInstance()
	rv := m.wbReview
	if sel == nil || rv == nil {
		return nil
	}
	if sel.Paused() || !sel.TmuxAlive() {
		m.errBox.SetInfo("agent is not running — resume the session first")
		return nil
	}
	prompt := review.ComposePrompt(rv.Root(), rv.States())
	if prompt == "" {
		m.errBox.SetInfo("no review comments to send")
		return nil
	}
	title := sel.Title
	msg := fmt.Sprintf("Send %d review comment(s) to %s?", rv.CommentCount(), title)
	return m.confirmTask(msg, overlay.ConfirmationTask{
		Sync: func() {
			if err := sel.SendPrompt(prompt); err != nil {
				m.errBox.SetError(err)
			}
		},
	})
}
```

Imports: `"fmt"`, `"github.com/aidan-bailey/loom/review"`, (`overlay` and `ui` already imported). Check `overlay.ConfirmationTask`'s field set in `ui/overlay/iface.go` — if `Sync` is not the right hook for main-goroutine side effects, mirror whichever field `confirmDiscardEdit` uses (it uses `Sync`).

- [ ] **Step 4: Run tests**

Run: `CGO_ENABLED=0 go test ./app/ -run WorkbenchReview -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w app/ && git add app/
git commit -m "feat(app): send review comments to the agent with confirmation"
```

### Task 10: Phase 2 — code review mode + focus-mode entry

**Files:**
- Modify: `app/workbench.go` (`5` starts a code review when none active), `script/intent.go`, `script/api_actions.go`, `script/defaults.lua`, `app/app_scripts.go` (intent fulfillment — find the `switch` over `script.*Intent` types and mirror `RestartWithOptionsIntent`'s arm)
- Test: `app/workbench_review_test.go`, `script/api_actions_test.go` (mirror an existing intent-factory test)

- [ ] **Step 1: Write the failing tests**

```go
// app/workbench_review_test.go additions:
//
// TestWorkbenchReview_5StartsCodeReview
//   - worktree temp repo fixture with a committed base + a modified
//     file (reuse the initRepo pattern from review/gitdiff/diff_test.go)
//   - workbench mode, no active review; press "5"
//   - Review() != nil, returned Cmd non-nil; after delivering the load
//     msg, View() contains the changed filename
//   - a .crit/code-review.yaml session manifest exists in the worktree
//
// TestWorkbenchReview_5NoChangesNotifies
//   - clean worktree fixture; press "5" → Review() == nil, errBox info
```

`script/api_actions_test.go`: add a test that `cs.actions.open_review()` enqueues `script.OpenReviewIntent` (copy the shape of the existing `resume_selected`/`restart_with_options_selected` test).

- [ ] **Step 2: Run to verify failure**

Run: `CGO_ENABLED=0 go test ./app/ -run WorkbenchReview_5 ; CGO_ENABLED=0 go test ./script/ -run open_review`
Expected: FAIL.

- [ ] **Step 3: Implement code-review startup**

In `app/workbench.go`:

```go
// openCodeReview builds a multi-file review over the worktree's
// changes vs HEAD and writes the interop session manifest.
func (m *home) openCodeReview() tea.Cmd {
	sel := m.list.GetSelectedInstance()
	if sel == nil || sel.Paused() {
		return nil
	}
	root := sel.GetWorktreePath()
	if root == "" {
		return nil
	}
	files, err := gitdiff.ChangedFiles(root)
	if err != nil {
		return m.handleError(fmt.Errorf("listing changes: %w", err))
	}
	if len(files) == 0 {
		m.errBox.SetInfo("no changes to review in this worktree")
		return nil
	}
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.Path
	}
	title, sessionRoot := sel.Title, root
	p := reviewui.NewCodePane(title, root, files, "HEAD")
	m.wbReview = p
	m.workbench.SetReview(p)
	m.workbench.SetTab(ui.WbTabReview)
	manifest := func() tea.Msg {
		err := review.SaveSession(sessionRoot, &review.CodeReviewSession{
			Files: paths, DiffBase: "HEAD", CreatedAt: time.Now(),
		})
		return reviewui.SavedMsg{Title: title, Err: err}
	}
	return tea.Batch(p.LoadCmd(), manifest)
}
```

Change the `"5"` case from Task 8 to:

```go
	case "5":
		if m.wbReview != nil {
			m.workbench.SetTab(ui.WbTabReview)
			return m, nil, true
		}
		return m, m.openCodeReview(), true
```

Imports for `app/workbench.go`: `"time"`, `gitdiff "github.com/aidan-bailey/loom/review/gitdiff"`.

Note: `openCodeReview` runs `gitdiff.ChangedFiles` synchronously in the key handler (one `git diff --name-status` — same weight as diff-stat calls the app already makes inline). `NewCodePane` also calls `gitdiff.DiffFile` per file at construction (vendored behavior). Acceptable for v1; if it ever stutters on huge diffs, wrap construction in a Cmd delivering the built pane via a new msg.

- [ ] **Step 4: Focus-mode `c` via Lua intent**

`script/intent.go` — add beside `RestartWithOptionsIntent`:

```go
// OpenReviewIntent asks the app to open the workbench on the review
// tab for the selected instance (code review over the worktree diff).
type OpenReviewIntent struct{}

func (OpenReviewIntent) intent() {}
```

(Check whether existing intents declare `intent()` methods explicitly or via a shared embedding — mirror exactly.)

`script/api_actions.go` — register `open_review` the same way `restart_with_options_selected` is registered (an action factory that enqueues the intent and returns its await handle).

`script/defaults.lua` — in the session-ops block:

```lua
cs.bind("c", function() cs.actions.open_review() end, { help = "review" })
```

`app/app_scripts.go` — in the intent-fulfillment switch:

```go
	case script.OpenReviewIntent:
		cmds = append(cmds, m.enterWorkbenchReview())
```

(match the surrounding arms' exact append/return idiom), and in `app/workbench.go`:

```go
// enterWorkbenchReview is the focus-mode entry: workbench + code review
// in one hop.
func (m *home) enterWorkbenchReview() tea.Cmd {
	enter := m.enterWorkbench()
	if m.viewMode != viewWorkbench {
		return enter // nothing selected; enterWorkbench no-opped
	}
	return tea.Batch(enter, m.openCodeReview())
}
```

Do **not** add `"c"` to `overviewKeyAllowed` or `workbenchKeyAllowed` — overview stays inert (spec), and the workbench handles `c`/`5` itself before the whitelist gate.

- [ ] **Step 5: Run all tests**

Run: `CGO_ENABLED=0 go test ./app/ ./script/ ./ui/... ./review/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
gofmt -w app/ script/ && go vet ./app/ ./script/
git add app/ script/
git commit -m "feat(app): code review mode + focus-mode review entry (c / 5)"
```

### Task 11: Docs

**Files:**
- Modify: `CLAUDE.md` (keybindings table + workbench gotcha), `USAGE.md` (workbench section)

- [ ] **Step 1: Update CLAUDE.md**

Keybindings table — change the workbench tab row and add two rows:

```markdown
| `1`–`5` | (workbench) Select panel tab (markdown / diff / files / terminal / review) |
| `c` | (workbench, markdown tab) Review the shown doc with inline comments; (focus) open the workbench code review for the selected session |
| `S` | (workbench, review tab) Send review comments to the agent (with confirmation) |
```

Architecture → Key Packages: add after `session/files/`:

```markdown
- **`review/`** — Comment model, YAML store (`.crit/` in the worktree, self-gitignored), code-review session manifest, and agent-prompt composition. Vendored from kevindutra/crit (MIT, see NOTICE.md) with paths threaded through an explicit worktree root. `review/gitdiff/` parses per-file changed-line maps via go-gitdiff (`git -C <dir>`), separate from `session/git`'s worktree lifecycle ops.
```

`ui/` package bullet: mention `ui/review/` (embedded crit review pane, theme-hooked). Gotchas → workbench bullet: append a sentence:

```markdown
The review tab (`5`/`c`) freezes markdown follow-mode on entry and resumes it on exit (`q`)/teardown; review persistence runs as Cmds delivering `reviewui.SavedMsg`, and the pane declines idle-`esc` so workbench exit keeps working.
```

- [ ] **Step 2: Update USAGE.md**

Find the workbench section (`grep -n workbench USAGE.md`) and document: entering review (`c` on a doc, `5` for the code diff, focus-mode `c`), commenting keys (enter, `v` ranges, `[`/`]`, `s` sidebar — from the vendored keymap), `q` to finish, `S` to send to the agent, and that comments persist in the worktree's `.crit/` (git-ignored, crit-CLI compatible).

- [ ] **Step 3: Commit**

```bash
git add CLAUDE.md USAGE.md
git commit -m "docs: workbench review tab usage and keybindings"
```

### Task 12: Full verification

- [ ] **Step 1: Full test suite**

Run: `CGO_ENABLED=0 go test ./...`
Expected: PASS.

- [ ] **Step 2: Race detector on touched packages**

Run: `CC=clang CGO_ENABLED=1 go test -race ./review/... ./ui/... ./app/ ./script/`
Expected: PASS. Pay attention to the persistCmd snapshot (the one place vendored state crosses a goroutine).

- [ ] **Step 3: Vet + format check**

Run: `go vet ./... && gofmt -l .`
Expected: vet clean; gofmt lists nothing.

- [ ] **Step 4: Manual smoke test**

Build (`CGO_ENABLED=0 go build -o loom`) and run against a scratch repo (memory: isolate with a HOME override and `env -u TMUX`): create a session, have the agent write a markdown file, `enter` → workbench, `c` → comment on two lines (single + `v` range) → `q` → `S` → confirm → verify the prompt arrives in the agent pane and `.crit/` exists in the worktree but `git status` is clean.

- [ ] **Step 5: Final commit (if smoke fixes were needed) and cleanup**

```bash
rm -rf /tmp/crit-vendor
git log --oneline main..HEAD
```

Expected: ~10 commits telling the absorption story.
