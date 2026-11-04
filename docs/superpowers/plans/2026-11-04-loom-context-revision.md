# Loom-context Revision Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace loom's two appended Claude prompts with accurate ones that add the diff-probe, push, tmux and daemon guardrails and teach `loom session list/new/send`, built from one shared tail.

**Architecture:** Three embedded markdown fragments in package `session` (a worktree head, a main-session head, a shared tail); `WriteLoomContextFiles` writes head, blank line and tail under the two existing file names, so the launch path is untouched. A test in `internal/cli` pins every `loom session` command and flag the tail names against the real cobra tree.

**Tech Stack:** Go (`embed`, `bytes`), cobra, testify.

**Spec:** `docs/superpowers/specs/2026-11-04-loom-context-revision-design.md`

**Execution:** inline, by the user's choice. Scratch files go in the session scratchpad, never the worktree (loom's `git add -N .` would index them). Commit with explicit paths only.

---

### Task 1: Compose the prompts from a shared tail, with the new prose

**Files:**
- Rename: `session/claude-loom-context.md` → `session/loom_context_worktree.md` (then rewrite)
- Rename: `session/claude-loom-context-workspace.md` → `session/loom_context_main.md` (then rewrite)
- Create: `session/loom_context_shared.md`
- Modify: `session/loom_context.go:14-27` (embeds), `session/loom_context.go:57-82` (`WriteLoomContextFiles`)
- Test: `session/loom_context_test.go:11-21` (`TestWriteLoomContextFiles`)

- [ ] **Step 1: Write the failing test**

In `session/loom_context_test.go`, replace the first block of `TestWriteLoomContextFiles` (from `dir := t.TempDir()` through the `for` loop that asserts "Loom session context") with:

```go
	dir := t.TempDir()
	assert.NoError(t, WriteLoomContextFiles(dir))

	wt := filepath.Join(dir, "claude-loom-context.md")
	ws := filepath.Join(dir, "claude-loom-context-workspace.md")
	// Each written file is its variant's head, a blank line, then the tail
	// both variants share; every fragment ends in exactly one newline, so
	// the seam is one blank line.
	for _, frag := range [][]byte{loomContextWorktreeHead, loomContextMainHead, loomContextSharedTail} {
		assert.True(t, bytes.HasSuffix(frag, []byte("\n")) && !bytes.HasSuffix(frag, []byte("\n\n")),
			"a loom-context fragment must end in exactly one newline")
	}
	for path, head := range map[string][]byte{wt: loomContextWorktreeHead, ws: loomContextMainHead} {
		b, err := os.ReadFile(path)
		assert.NoError(t, err)
		assert.Contains(t, string(b), "Loom session context")
		assert.Equal(t, string(head)+"\n"+string(loomContextSharedTail), string(b), path)
	}
```

and add `"bytes"` to the file's imports. Leave the rest of the test (skip-if-current, stale rewrite, empty dir) as it is.

- [ ] **Step 2: Run it to verify it fails**

Run: `CGO_ENABLED=0 go test ./session -run TestWriteLoomContextFiles`
Expected: FAIL to build: `undefined: loomContextWorktreeHead` (and the other two).

- [ ] **Step 3: Rename the sources and write the prose**

```bash
git mv session/claude-loom-context.md session/loom_context_worktree.md
git mv session/claude-loom-context-workspace.md session/loom_context_main.md
```

Write `session/loom_context_worktree.md` (whole file, ending in one newline):

```markdown
# Loom session context

You are running inside **loom**, which runs many coding agents side by side, each in its own git worktree and tmux session, under one loom daemon. Your working directory is the worktree loom made for this session, on the session's own branch; other agents are at work in sibling worktrees of this repository.

## Your worktree and branch

- **Stay on your branch.** Don't switch branches, create or delete branches, add or remove worktrees, or rebase onto another branch: loom tracks this session by its branch and worktree, and its resume, merge and push act on them. Merging another branch into yours is fine when the task needs it.
- **Stage and commit by path (`git add <paths>`, `git commit -- <paths>`), never with `git commit -a`, `git add -A` or `git add .`.** Loom computes this session's diff by running `git add -N .` here, so every new file in the worktree has an intent-to-add entry, and `git commit -a` commits it.
- **Keep scratch files out of the worktree**, in your scratchpad or a temp directory. A file that sat here even briefly keeps its index entry after you delete it (`git status` shows ` D`): clear it with `git rm --cached <path>`.
- **Only commits travel.** Merging this session into another takes its commits alone, and when the user pushes it from loom, loom first commits everything still uncommitted (`git add .`, as `[loom] update from '<title>' …`, skipping hooks).
- **A pause keeps your work.** It stops you and leaves the worktree as it is, uncommitted and ignored files included; a resume relaunches you in it.
```

Write `session/loom_context_main.md` (whole file, ending in one newline):

```markdown
# Loom session context

You are running inside **loom**, which runs many coding agents side by side, each in its own git worktree and tmux session, under one loom daemon. This is the workspace's **main session**: you work in the repository's main checkout, not a worktree, while other agents work in loom's worktrees of it.

## The main checkout

- **For parallel work, offer to start a loom session (below) rather than creating a branch or worktree yourself.** Loom gives each session its own branch and worktree; one you make yourself is one loom doesn't track.
- **Leave the sessions' branches and worktrees alone.** Each session's branch is checked out in its own worktree under `.loom/worktrees/` and holds another agent's live work: don't rebase, reset or delete it, and don't edit anything under `.loom/`. Merge a session's branch into this checkout only when the user asks.
- **Your changes land in the real checkout.** Loom can't pause or resume the main session, and your uncommitted changes are the working state the user and their tools see.
- **`.loom/` is gitignored, but tools that don't read `.gitignore` (`grep -r`, `find`) walk every worktree under it**: exclude it.
```

Create `session/loom_context_shared.md` (whole file, ending in one newline):

```markdown
## Loom itself

- **The tmux server is shared.** Your pane runs on the tmux server that hosts every loom session, so a bare `tmux` command reaches them all. Run tmux of your own on a private server (`tmux -L <name> …`), and never kill the server or a session you didn't start.
- **Leave the daemon alone.** Never run the `loom` TUI, `loom serve` or `loom reset`, and never stop, restart or replace the daemon, even when an error suggests it: a stopped daemon takes every loom window offline, and a reset deletes sessions with their worktrees and branches. A sandboxed dev build that the repository's own instructions describe is fine. If `loom` reports that the daemon is a different build, tell the user.

## Other sessions

`loom session` acts on the workspace whose repository holds your working directory (`-w <workspace>` names another) and names a session by its exact title:

- `loom session list [--json]`: the worktree sessions, with each one's title, status, branch and account; a worktree session finds its own row by its branch. `--json` adds each one's worktree, issue and PR.
- `loom session send <title> --from <you> --text "…" [--wait]` (or `--file <path>`): queue a message for that session's Claude, behind whatever it is doing. Always sign with `--from`: your session's title, or `main` from the main session, which isn't listed and can't be sent messages. `--wait` blocks until the turn carrying the message ends and prints the reply; a long turn can outlast your tool's timeout, so run it in the background.
- `loom session new <title> --prompt "…"` (or `--prompt-file <path>`): start a session on a new branch cut from the workspace's base branch, so it sees none of your unmerged work; `--branch <existing>` checks out an existing branch instead. It returns once the session runs, printing its branch and worktree.

Message other sessions whenever coordinating helps, and make each message stand on its own: the other agent has none of your context. Start a session only when the user asks for one or agrees to your proposal, since each costs the user plan usage and attention. Kill, pause or resume a session only when the user asks.

A prompt that begins `From <name>:` came from another session or a script through `loom session send`, not from the user. Treat it as a request from a colleague working for the same user: help within what the user has asked of you, and check with the user before anything they wouldn't expect. Answer it in your reply, which a sender that used `--wait` receives when your turn ends; to write back separately, `send` without `--wait`, since the sender may be blocked waiting on you.
```

- [ ] **Step 4: Compose the files in `session/loom_context.go`**

Replace the comment, constants and embeds at the top (lines 14-27) with:

```go
// Loom-context prompt files, written to the config dir by
// WriteLoomContextFiles and appended to every Claude launch's system
// prompt. The worktree variant orients an isolated-worktree session, the
// workspace variant the root-repo main session (IsWorkspaceTerminal). Each
// is its own head followed by the tail both share (loomContextFile).
const (
	loomContextFileWorktree  = "claude-loom-context.md"
	loomContextFileWorkspace = "claude-loom-context-workspace.md"
)

// The embedded fragments the prompt files are built from.
var (
	//go:embed loom_context_worktree.md
	loomContextWorktreeHead []byte
	//go:embed loom_context_main.md
	loomContextMainHead []byte
	//go:embed loom_context_shared.md
	loomContextSharedTail []byte
)

// loomContextFile is a prompt file's content: head, a blank line (head
// ends in a newline), then the shared tail. bytes.Join builds a slice of
// its own, so no append ever writes into an embedded one.
func loomContextFile(head []byte) []byte {
	return bytes.Join([][]byte{head, loomContextSharedTail}, []byte("\n"))
}
```

In `WriteLoomContextFiles`, change its doc's first sentence to "WriteLoomContextFiles writes both prompt files (loomContextFile) into configDir," and its entries to:

```go
		{loomContextFileWorktree, loomContextFile(loomContextWorktreeHead)},
		{loomContextFileWorkspace, loomContextFile(loomContextMainHead)},
```

- [ ] **Step 5: Run the session tests to verify they pass**

Run: `CGO_ENABLED=0 go test ./session -run 'TestWriteLoomContextFiles|TestLoomContextProgram'`
Expected: `ok  github.com/aidan-bailey/loom/session`

Run: `CGO_ENABLED=0 go test ./session ./session/agent ./core`
Expected: all `ok` (the launch check in `session/instance_lifecycle_test.go` uses the unchanged file name).

- [ ] **Step 6: Read the composed worktree file as Claude will**

Run: `cat session/loom_context_worktree.md; echo; cat session/loom_context_shared.md`
Expected: one blank line between the head's last bullet and `## Loom itself`, no stray blank lines elsewhere.

- [ ] **Step 7: Commit**

```bash
gofmt -l session/loom_context.go session/loom_context_test.go
git add -- session/loom_context.go session/loom_context_test.go session/loom_context_worktree.md session/loom_context_main.md session/loom_context_shared.md
git commit -m "feat(session): revise the loom-context prompts around a shared tail" -- session/loom_context.go session/loom_context_test.go session/claude-loom-context.md session/claude-loom-context-workspace.md session/loom_context_worktree.md session/loom_context_main.md session/loom_context_shared.md
```

(`gofmt -l` prints nothing. The commit pathspec names the old paths too, so the renames land whole.)

### Task 2: Pin the `loom session` commands and flags the tail names

**Files:**
- Create: `internal/cli/loom_context_test.go`

- [ ] **Step 1: Write the test**

```go
package cli

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// loomContextShared is the tail of the loom-context prompts, which teaches
// `loom session` to every Claude session loom launches (session package,
// WriteLoomContextFiles).
const loomContextShared = "../../session/loom_context_shared.md"

// codeSpan is one inline code span: a run between single backticks.
var codeSpan = regexp.MustCompile("`([^`]+)`")

// Every `loom session <sub> …` span in the loom-context tail names a real
// subcommand and flags it has, and every span that is a bare flag one some
// subcommand has. An agent following a stale line fails on an unknown
// command or flag, and nothing else would notice.
func TestLoomContext_NamesRealSessionFlags(t *testing.T) {
	b, err := os.ReadFile(loomContextShared)
	require.NoError(t, err)
	root := newSessionCmd()

	var commands, bare int
	for _, m := range codeSpan.FindAllStringSubmatch(string(b), -1) {
		span := m[1]
		words := strings.Fields(span)
		switch {
		case len(words) >= 3 && words[0] == "loom" && words[1] == "session":
			commands++
			sub := subcommandNamed(root, words[2])
			if !assert.NotNil(t, sub, "`%s`: loom session has no subcommand %q", span, words[2]) {
				continue
			}
			for _, w := range words[3:] {
				if f, ok := parseFlag(w); ok {
					assert.True(t, f.on(sub), "`%s`: loom session %s has no flag %s", span, words[2], w)
				}
			}
		case strings.HasPrefix(span, "-"):
			bare++
			f, ok := parseFlag(words[0])
			assert.True(t, ok && f.onAny(root), "`%s`: no loom session subcommand has this flag", span)
		}
	}
	assert.Positive(t, commands, "no `loom session <subcommand>` span in %s", loomContextShared)
	assert.Positive(t, bare, "no bare flag span in %s", loomContextShared)
}

// subcommandNamed is root's subcommand called name, or nil.
func subcommandNamed(root *cobra.Command, name string) *cobra.Command {
	for _, c := range root.Commands() {
		if c.Name() == name {
			return c
		}
	}
	return nil
}

// flagRef is a flag a span names: a long name for "--x", a shorthand for
// "-x".
type flagRef struct {
	name  string
	short bool
}

// parseFlag is the flag word names, brackets and "=value" stripped
// ("[--wait]" is --wait), and whether it names one.
func parseFlag(word string) (flagRef, bool) {
	word = strings.Trim(word, "[]")
	switch {
	case strings.HasPrefix(word, "--") && len(word) > 2:
		name, _, _ := strings.Cut(word[2:], "=")
		return flagRef{name: name}, true
	case strings.HasPrefix(word, "-") && len(word) == 2 && word[1] != '-':
		return flagRef{name: word[1:], short: true}, true
	}
	return flagRef{}, false
}

// on reports whether cmd has f, as its own flag or one it inherits (the
// session command's persistent -w/--workspace).
func (f flagRef) on(cmd *cobra.Command) bool {
	if f.short {
		return cmd.Flags().ShorthandLookup(f.name) != nil || cmd.InheritedFlags().ShorthandLookup(f.name) != nil
	}
	return cmd.Flags().Lookup(f.name) != nil || cmd.InheritedFlags().Lookup(f.name) != nil
}

// onAny reports whether some subcommand of root has f.
func (f flagRef) onAny(root *cobra.Command) bool {
	for _, c := range root.Commands() {
		if f.on(c) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run it to verify it passes against the real tail**

Run: `CGO_ENABLED=0 go test ./internal/cli -run TestLoomContext_NamesRealSessionFlags -v`
Expected: `--- PASS: TestLoomContext_NamesRealSessionFlags`

- [ ] **Step 3: Prove each check goes red**

```bash
S=/tmp/claude-1000/-tb-Source-Personal-loom--loom-worktrees-aidanb-loom-append-prompt-18e4c9817654d2a2/bc338b6c-02d4-40d5-883d-a9b7309e996c/scratchpad
F=session/loom_context_shared.md
cp "$F" "$S/shared.bak"
for m in 's/\[--wait\]/[--wiat]/' 's/loom session list \[--json\]/loom session ls [--json]/' 's/`--prompt-file <path>`/`--promptfile <path>`/'; do
  sed -i "$m" "$F"
  CGO_ENABLED=0 go test ./internal/cli -run TestLoomContext_NamesRealSessionFlags 2>&1 | grep -E '^(--- FAIL|ok)'
  cp "$S/shared.bak" "$F"
done
git diff --exit-code -- "$F" && echo restored
```

Expected: three `--- FAIL: TestLoomContext_NamesRealSessionFlags` lines (no `ok`), then `restored`.

- [ ] **Step 4: Commit**

```bash
gofmt -l internal/cli/loom_context_test.go
git add -- internal/cli/loom_context_test.go
git commit -m "test(cli): pin the loom session commands the loom-context tail teaches" -- internal/cli/loom_context_test.go
```

### Task 3: Record the rules, then run every gate

**Files:**
- Modify: `session/CLAUDE.md` (section "Accounts and launching", after the `CLAUDE_CODE_NO_FLICKER=1` rule)
- Modify: `internal/cli/CLAUDE.md` (after the `findSession` rule)

- [ ] **Step 1: Add the session rule**

Insert after the bullet that starts `- **Launch every Claude session with \`CLAUDE_CODE_NO_FLICKER=1\``:

```markdown
- **Change the loom-context prompts (`session/loom_context_worktree.md`, `session/loom_context_main.md`, `session/loom_context_shared.md`) with what they describe: pause, merge, push, the diff probe's `git add -N .`, the shared tmux server and `loom session`.** Every Claude session loom launches reads them as its appended system prompt, so a stale line misleads every agent, silently. **Enforced** for the `loom session` subcommands and flags they name by `TestLoomContext_NamesRealSessionFlags`; **Convention** for the rest.
```

- [ ] **Step 2: Add the cli rule**

Insert after the bullet that starts `- **Name a session by exact title among the workspace's rows`:

```markdown
- **Renaming or removing a `loom session` subcommand or flag? Update `session/loom_context_shared.md`, which teaches them to every Claude session loom launches.** An agent following the stale line fails on an unknown command or flag. **Enforced** by `TestLoomContext_NamesRealSessionFlags`.
```

- [ ] **Step 3: Check the docs lint and its budgets**

Run: `CGO_ENABLED=0 go test ./tools/claudemd -run TestRepo_ClaudeMDStructure && CGO_ENABLED=0 go run ./tools/claudemd -v 2>&1 | grep -E '^(session|internal/cli)/CLAUDE.md'`
Expected: `ok`, then `session/CLAUDE.md` under 100 lines and 20000 bytes, `internal/cli/CLAUDE.md` likewise.

- [ ] **Step 4: Run every gate**

```bash
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go vet ./...
gofmt -l $(git ls-files '*.go' | grep -v '^vendor/')
```

Expected: every package `ok`; `go vet` silent; `gofmt -l` prints nothing. golangci-lint is skipped (the local v2 can't read the repo's v1 config); say so in the report.

- [ ] **Step 5: Commit**

```bash
git add -- session/CLAUDE.md internal/cli/CLAUDE.md
git commit -m "docs(session,cli): keep the loom-context prompts in step with what they describe" -- session/CLAUDE.md internal/cli/CLAUDE.md
```
