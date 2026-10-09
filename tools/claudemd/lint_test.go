package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// minimal is the smallest tree check passes on.
func minimal() map[string]string {
	return map[string]string{
		"go.mod":        "module example.com/x\n",
		"CLAUDE.md":     "# x\n\n- **Skills:** none\n",
		"pkg/pkg.go":    "package pkg\n",
		"pkg/CLAUDE.md": "# pkg\n",
	}
}

// with returns m with the key/value pairs kv set.
func with(m map[string]string, kv ...string) map[string]string {
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}

// without returns m with the keys removed.
func without(m map[string]string, keys ...string) map[string]string {
	for _, k := range keys {
		delete(m, k)
	}
	return m
}

// tree writes files (slash paths from the root) under a fresh temp dir.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
	return root
}

// problems runs check over files with cfg and returns each problem as text.
func problems(t *testing.T, files map[string]string, cfg config) []string {
	t.Helper()
	ps, err := check(tree(t, files), cfg)
	require.NoError(t, err)
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.String()
	}
	return out
}

// lines returns n short lines.
func lines(n int) string { return strings.Repeat("x\n", n) }

func TestCheck_MinimalTreePasses(t *testing.T) {
	assert.Empty(t, problems(t, minimal(), config{}))
}

func TestBudget(t *testing.T) {
	assert.Empty(t, problems(t, with(minimal(), "CLAUDE.md", "- **Skills:** none\n"+lines(149)), config{}), "150 lines is at the limit")
	assert.Contains(t, problems(t, with(minimal(), "CLAUDE.md", "- **Skills:** none\n"+lines(150)), config{}),
		"CLAUDE.md: 151 lines, over the 150-line budget: move context into a docs/claude guide")
	assert.Contains(t, problems(t, with(minimal(), "pkg/CLAUDE.md", "# pkg\n"+lines(100)), config{}),
		"pkg/CLAUDE.md: 101 lines, over the 100-line budget: move context into a docs/claude guide")
	long := "- **Skills:** none\n" + strings.Repeat(strings.Repeat("y", 199)+"\n", 81) // 19 + 81*200 = 16,219 bytes
	assert.Contains(t, problems(t, with(minimal(), "CLAUDE.md", long), config{}),
		"CLAUDE.md: 16219 bytes, over the 16000-byte budget: move context into a docs/claude guide")
	wide := "# pkg\n" + strings.Repeat(strings.Repeat("z", 1000)+"\n", 20) // 6 + 20*1001 = 20,026 bytes
	assert.Contains(t, problems(t, with(minimal(), "pkg/CLAUDE.md", wide), config{}),
		"pkg/CLAUDE.md: 20026 bytes, over the 20000-byte budget: move context into a docs/claude guide")
	assert.Contains(t, problems(t, with(minimal(), "pkg/CLAUDE.md", "# pkg\n"+strings.Repeat("w", 1201)+"\n"), config{}),
		"pkg/CLAUDE.md:2: line of 1201 bytes, over the 1200-byte line budget: move the rule's context into a docs/claude guide")
}

func TestPhrases(t *testing.T) {
	doc := "# pkg\n\nFor the API, See README.\nKeep it as above.\nDetails: see the pane-client gotcha.\n"
	ps := problems(t, with(minimal(), "pkg/CLAUDE.md", doc), config{})
	assert.Contains(t, ps, `pkg/CLAUDE.md:3: self-deferring phrase "see readme": state the rule inline`)
	assert.Contains(t, ps, `pkg/CLAUDE.md:4: self-deferring phrase "as above": state the rule inline`)
	assert.Contains(t, ps, `pkg/CLAUDE.md:5: self-deferring phrase "see the pane-client gotcha": state the rule inline`)
	quoted := "# pkg\n\nThe lint rejects `see README` and `as described elsewhere`.\n\n```\nsee readme\n```\n"
	assert.Empty(t, problems(t, with(minimal(), "pkg/CLAUDE.md", quoted), config{}), "code spans and fences are not prose")
}

func TestShape(t *testing.T) {
	assert.Contains(t, problems(t, with(minimal(), "pkg/CLAUDE.md", "# package\n"), config{}), `pkg/CLAUDE.md:1: first line must be "# pkg"`)
	doc := "# pkg\n\n## Rules when modifying this package\n\n- **Do X.** Why. **Convention** — nothing checks it.\n- **Do Y.** Why.\n\n## Pointers\n\n- elsewhere\n"
	ps := problems(t, with(minimal(), "pkg/CLAUDE.md", doc), config{})
	assert.Contains(t, ps, "pkg/CLAUDE.md:6: rule without **Enforced** or **Convention**: say what guards it")
	assert.Len(t, ps, 1, "only the Rules section's bullets are rules")
}

func TestCoverage(t *testing.T) {
	assert.Contains(t, problems(t, with(minimal(), "other/other.go", "package other\n"), config{}),
		"other: Go package with no CLAUDE.md here or in a parent below the root")
	assert.Empty(t, problems(t, with(minimal(), "pkg/sub/sub.go", "package sub\n"), config{}), "a parent's CLAUDE.md covers its subtree")
	assert.Empty(t, problems(t, with(minimal(), "other/other.go", "package other\n"), config{exempt: map[string]string{"other": "why"}}))
	assert.Contains(t, problems(t, minimal(), config{exempt: map[string]string{"gone": "why"}}),
		"gone: exempt from coverage, but no Go package lives here any more: drop the exemption")
	ignored := with(minimal(), "vendor/v/v.go", "package v\n", "pkg/testdata/t/t.go", "package t\n",
		"pkg/_old/o.go", "package o\n", ".loom/w/w.go", "package w\n")
	assert.Empty(t, problems(t, ignored, config{}), "vendor/, testdata/, _dirs and dot-dirs hold no packages of ours")
	assert.Contains(t, problems(t, without(minimal(), "pkg/pkg.go"), config{}),
		".: found no Go package dirs: coverage fails closed")
}

func TestLinks(t *testing.T) {
	ps := problems(t, with(minimal(), "pkg/CLAUDE.md", "# pkg\n\nFacts in [README](README.md).\n"), config{})
	assert.Contains(t, ps, `pkg/CLAUDE.md:3: broken link "README.md"`)
	ok := with(minimal(),
		"pkg/README.md", "# pkg\n\n[back](CLAUDE.md)\n",
		"pkg/CLAUDE.md", "# pkg\n\n[r](README.md#rules) [w](https://example.com) [a](#top) [root](/go.mod) `[x](nope.md)`\n")
	assert.Empty(t, problems(t, ok, config{}), "anchors dropped; external, anchor-only and code links skipped; / is the root")
	assert.Contains(t, problems(t, with(ok, "pkg/README.md", "[gone](gone.md)\n"), config{}),
		`pkg/README.md:1: broken link "gone.md"`, "a README beside a CLAUDE.md is in the doc set")
}

func TestOrphans(t *testing.T) {
	guide := "docs/claude/thing.md"
	base := func() map[string]string {
		return with(minimal(),
			"CLAUDE.md", "# x\n\n- **Skills:** none\n\nGuides: [index](docs/claude/INDEX.md)\n",
			guide, "# Thing\n",
			"docs/claude/INDEX.md", "| [`thing.md`](thing.md) | when |\n",
			"pkg/CLAUDE.md", "# pkg\n\nGuide: [`../docs/claude/thing.md`](../docs/claude/thing.md)\n")
	}
	assert.Empty(t, problems(t, base(), config{}))
	assert.Contains(t, problems(t, with(base(), "docs/claude/INDEX.md", "# Index\n"), config{}), guide+": not listed in docs/claude/INDEX.md")
	assert.Contains(t, problems(t, with(base(), "pkg/CLAUDE.md", "# pkg\n"), config{}), guide+": not linked from any CLAUDE.md rule")
	assert.Contains(t, problems(t, without(base(), "docs/claude/INDEX.md"), config{}), "docs/claude: guides but no INDEX.md")
	assert.Contains(t, problems(t, with(base(), "CLAUDE.md", "# x\n\n- **Skills:** none\n"), config{}),
		"CLAUDE.md: doesn't link docs/claude/INDEX.md, so the guides can't be found from the file that always loads")
	fromGuide := with(base(), "pkg/CLAUDE.md", "# pkg\n", "docs/claude/other.md", "[t](thing.md)\n",
		"docs/claude/INDEX.md", "[t](thing.md) [o](other.md)\n")
	assert.Contains(t, problems(t, fromGuide, config{}), guide+": not linked from any CLAUDE.md rule", "a guide's link is not a rule's")
}

func TestListParity(t *testing.T) {
	skill := ".claude/skills/dev/SKILL.md"
	assert.Empty(t, problems(t, with(minimal(), skill, "x", "CLAUDE.md", "- **Skills:** `dev`\n"), config{}))
	assert.Contains(t, problems(t, with(minimal(), skill, "x"), config{}), "CLAUDE.md:3: .claude/skills/dev is missing from this list")
	assert.Contains(t, problems(t, with(minimal(), "CLAUDE.md", "- **Skills:** `ghost`\n"), config{}), `CLAUDE.md:1: lists "ghost", which .claude/skills doesn't hold`)
	assert.Contains(t, problems(t, with(minimal(), "CLAUDE.md", "# x\n"), config{}), `CLAUDE.md: no line starting "- **Skills:**" to list .claude/skills`)
	assert.Contains(t, problems(t, with(minimal(), "CLAUDE.md", "- **Skills:** none\n- **Skills:** none\n"), config{}), `CLAUDE.md:2: a second line starting "- **Skills:**"`)
	assert.Empty(t, problems(t, with(minimal(), ".claude/skills/half/notes.md", "x"), config{}), "a dir without SKILL.md is no skill")
	assert.Contains(t, problems(t, with(minimal(), ".claude/commands/ship.md", "x"), config{}), `CLAUDE.md: no line starting "- **Commands:**" to list .claude/commands`)
	assert.Empty(t, problems(t, with(minimal(), ".claude/commands/ship.md", "x", "CLAUDE.md", "- **Skills:** none\n- **Commands:** `ship`\n"), config{}))
}

func TestRun_ExitCodes(t *testing.T) {
	var out, errOut bytes.Buffer
	assert.Equal(t, 0, run(tree(t, minimal()), config{}, options{}, &out, &errOut))
	assert.Equal(t, 1, run(tree(t, with(minimal(), "other/o.go", "package o\n")), config{}, options{}, &out, &errOut))
	assert.Equal(t, 2, run(t.TempDir(), config{}, options{}, &out, &errOut), "no go.mod")
	stale := with(minimal(), "pkg/CLAUDE.md", "# pkg\n\nUses `GoneSymbol`.\n")
	assert.Equal(t, 0, run(tree(t, stale), config{}, options{idents: true}, &out, &errOut), "symbol findings are advisory")
	assert.Equal(t, 1, run(tree(t, stale), config{}, options{idents: true, strict: true}, &out, &errOut), "unless strict")
	out.Reset()
	assert.Equal(t, 0, run(tree(t, minimal()), config{}, options{verbose: true}, &out, &errOut))
	assert.Contains(t, out.String(), "pkg/CLAUDE.md: 1/100 lines, 6/20000 bytes")
}
