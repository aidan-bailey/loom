package main

import (
	"bytes"
	"fmt"
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

// body returns exactly n bytes of lines, none longer than width bytes
// (newline included).
func body(n, width int) string {
	var b strings.Builder
	for ; n >= width; n -= width {
		b.WriteString(strings.Repeat("y", width-1) + "\n")
	}
	if n > 0 {
		b.WriteString(strings.Repeat("y", n-1) + "\n")
	}
	return b.String()
}

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

func TestBudget_AtTheLimit(t *testing.T) {
	root := "- **Skills:** none\n" + body(16000-19, 200)
	require.Len(t, root, 16000)
	assert.Empty(t, problems(t, with(minimal(), "CLAUDE.md", root), config{}), "exactly 16,000 bytes is within the root budget")
	pkg := "# pkg\n" + body(20000-6, 1000)
	require.Len(t, pkg, 20000)
	assert.Empty(t, problems(t, with(minimal(), "pkg/CLAUDE.md", pkg), config{}), "exactly 20,000 bytes is within a package budget")
	assert.Empty(t, problems(t, with(minimal(), "pkg/CLAUDE.md", "# pkg\n"+strings.Repeat("w", 1200)+"\n"), config{}), "a line of exactly 1,200 bytes is within the line budget")
}

func TestLineCount(t *testing.T) {
	for in, want := range map[string]int{"": 0, "\n": 1, "a": 1, "a\n": 1, "a\nb": 2, "a\nb\n": 2, "a\n\nb": 3} {
		assert.Equal(t, want, lineCount([]byte(in)), "%q", in)
	}
	assert.Empty(t, problems(t, with(minimal(), "pkg/CLAUDE.md", "# pkg\n"+lines(98)+"x"), config{}), "100 lines, the last unterminated")
	assert.Contains(t, problems(t, with(minimal(), "pkg/CLAUDE.md", "# pkg\n"+lines(99)+"x"), config{}),
		"pkg/CLAUDE.md: 101 lines, over the 100-line budget: move context into a docs/claude guide", "an unterminated last line counts")
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

// TestPhrases_EachPattern pins every pattern in deferring, and the cases that
// widened them: a [link] to the README, "described above" without "as", and
// a number after the phrase.
func TestPhrases_EachPattern(t *testing.T) {
	for _, c := range []struct{ doc, phrase string }{
		{"See README for the rest.", "see readme"},
		{"See the README.", "see the readme"},
		{"See [README](README.md) for the rest.", "see [readme"},
		{"It is described in README.", "described in readme"},
		{"Documented in the README.", "documented in the readme"},
		{"Explained in the [README](README.md).", "explained in the [readme"},
		{"Keep it as above.", "as above"},
		{"Do it as described below.", "as described below"},
		{"Do it as mentioned above.", "as mentioned above"},
		{"The helper described above does it.", "described above"},
		{"The flow mentioned below.", "mentioned below"},
		{"The rules noted above.", "noted above"},
		{"See above for the rest.", "see above"},
		{"Cf. below for the rest.", "cf. below"},
		{"Details described elsewhere.", "described elsewhere"},
		{"Details: see the pane-client gotcha.", "see the pane-client gotcha"},
		{"Details: see the core bullet.", "see the core bullet"},
		{"Details: see the foo and bar gotchas.", "see the foo and bar gotchas"},
		{"See gotchas.", "see gotchas"},
		{"See Testing Patterns.", "see testing patterns"},
		{"See key packages.", "see key packages"},
		{"The rule above says X.", "the rule above"},
		{"This section below says X.", "this section below"},
		{"The gotcha above says X.", "the gotcha above"},
		{"This bullet below says X.", "this bullet below"},
		{"The table above says X.", "the table above"},
		{"Facts: see `README.md`.", "see readme"},
		{"Details: see the `core/` bullet.", "see the core/ bullet"},
	} {
		ps := problems(t, with(minimal(), "pkg/CLAUDE.md", "# pkg\n\n"+c.doc+"\n"), config{})
		assert.Contains(t, ps, fmt.Sprintf("pkg/CLAUDE.md:3: self-deferring phrase %q: state the rule inline", c.phrase), c.doc)
	}
	for _, doc := range []string{
		"Keep this section below 100 lines.",
		"Keep the table above 20 rows.",
		"Quote `see README` or `see the core gotcha` as examples.",
		"Load is as above-average as usual.",
		"Output stays below-the-fold.",
	} {
		assert.Empty(t, problems(t, with(minimal(), "pkg/CLAUDE.md", "# pkg\n\n"+doc+"\n"), config{}), doc)
	}
	once := problems(t, with(minimal(), "pkg/CLAUDE.md", "# pkg\n\nSee README, and see README again.\n"), config{})
	assert.Equal(t, []string{`pkg/CLAUDE.md:3: self-deferring phrase "see readme": state the rule inline`}, once, "a phrase is reported once per line")
	both := problems(t, with(minimal(), "pkg/CLAUDE.md", "# pkg\n\nKeep this section below 5 lines and this section below.\n"), config{})
	assert.Equal(t, []string{`pkg/CLAUDE.md:3: self-deferring phrase "this section below": state the rule inline`}, both,
		"a number after one match doesn't hide the next")
}

func TestShape(t *testing.T) {
	assert.Contains(t, problems(t, with(minimal(), "pkg/CLAUDE.md", "# package\n"), config{}), `pkg/CLAUDE.md:1: first line must be "# pkg"`)
	doc := "# pkg\n\n## Rules when modifying this package\n\n- **Do X.** Why. **Convention** — nothing checks it.\n- **Do Y.** Why.\n\n## Pointers\n\n- elsewhere\n"
	ps := problems(t, with(minimal(), "pkg/CLAUDE.md", doc), config{})
	assert.Contains(t, ps, "pkg/CLAUDE.md:6: rule without **Enforced** or **Convention**: say what guards it")
	assert.Len(t, ps, 1, "only the Rules section's bullets are rules")
	bullets := "# pkg\n\n## Rules\n\n* **Star.** no marker\n+ **Plus.** no marker\n1. **One.** no marker\n10. **Ten.** no marker\n  - nested, not a rule\n- **Dash.** **Enforced** by x\n\n# Another title\n\n- not a rule\n"
	assert.ElementsMatch(t, []string{
		"pkg/CLAUDE.md:5: rule without **Enforced** or **Convention**: say what guards it",
		"pkg/CLAUDE.md:6: rule without **Enforced** or **Convention**: say what guards it",
		"pkg/CLAUDE.md:7: rule without **Enforced** or **Convention**: say what guards it",
		"pkg/CLAUDE.md:8: rule without **Enforced** or **Convention**: say what guards it",
	}, problems(t, with(minimal(), "pkg/CLAUDE.md", bullets), config{}), "*, + and numbered bullets are rules too; an indented one is not; a title ends the section")
}

func TestCoverage(t *testing.T) {
	assert.Contains(t, problems(t, with(minimal(), "other/other.go", "package other\n"), config{}),
		"other: Go package with no CLAUDE.md here or in a parent below the root")
	assert.Empty(t, problems(t, with(minimal(), "pkg/sub/sub.go", "package sub\n"), config{}), "a parent's CLAUDE.md covers its subtree")
	assert.Empty(t, problems(t, with(minimal(), "other/other.go", "package other\n"), config{exempt: map[string]string{"other": "why"}}))
	assert.Contains(t, problems(t, minimal(), config{exempt: map[string]string{"gone": "why"}}),
		"gone: exempt from coverage, but no Go package lives here any more: drop the exemption")
	// Each at the top level, where no CLAUDE.md covers it, so skipping one
	// can't hide behind the others.
	for _, dir := range []string{"vendor/v", "testdata/t", "_old", ".loom/w"} {
		assert.Empty(t, problems(t, with(minimal(), dir+"/x.go", "package x\n"), config{}), "%s holds no package of ours", dir)
	}
	assert.Contains(t, problems(t, without(minimal(), "pkg/pkg.go"), config{}),
		".: found no Go package dirs: coverage fails closed")
}

func TestCoverage_Exemptions(t *testing.T) {
	other := with(minimal(), "other/other.go", "package other\n")
	assert.Contains(t, problems(t, other, config{exempt: map[string]string{"other": " "}}),
		"other: exempt from coverage with no reason: say why")
	assert.Contains(t, problems(t, minimal(), config{exempt: map[string]string{"pkg": "why"}}),
		"pkg: exempt from coverage, but a CLAUDE.md here or in a parent already covers it: drop the exemption")
	sub := with(minimal(), "pkg/sub/sub.go", "package sub\n")
	assert.Contains(t, problems(t, sub, config{exempt: map[string]string{"pkg/sub": "why"}}),
		"pkg/sub: exempt from coverage, but a CLAUDE.md here or in a parent already covers it: drop the exemption")
	assert.Empty(t, problems(t, other, config{exempt: map[string]string{"other": "why"}}), "a reasoned exemption of an uncovered package is fine")
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

func TestLinks_Skips(t *testing.T) {
	doc := "# pkg\n\n[m](mailto:dev@example.com) [h](http://example.com/x.md) [s](#section)\n"
	assert.Empty(t, problems(t, with(minimal(), "pkg/CLAUDE.md", doc), config{}), "mailto and URL links are not files")
	targets, broken := links(tree(t, minimal()), "pkg/CLAUDE.md", []byte("[a](#top) [m](mailto:x@y.z) [e](https://e.com/x)\n"))
	assert.Empty(t, targets, "an anchor-only link points at the doc itself, which is no target")
	assert.Empty(t, broken)
}

func TestDocSet_Readmes(t *testing.T) {
	gone := "[gone](gone.md)\n"
	ps := problems(t, with(minimal(), "docs/README.md", gone, "README.md", gone), config{})
	assert.Contains(t, ps, `docs/README.md:1: broken link "gone.md"`, "a README is a doc wherever it lives")
	assert.NotContains(t, ps, `README.md:1: broken link "gone.md"`, "the root README is for users")
	assert.Contains(t, problems(t, with(minimal(), "docs/ARCHITECTURE.md", gone), config{}), `docs/ARCHITECTURE.md:1: broken link "gone.md"`)
	skipped := with(minimal(), "vendor/v/README.md", gone, "pkg/testdata/README.md", gone, "pkg/_old/README.md", gone, ".github/README.md", gone)
	assert.Empty(t, problems(t, skipped, config{}), "vendor/, testdata/, _dirs and dot-dirs hold no docs of ours")
	// A README in docs/claude is a guide as well; it is read once.
	both := with(minimal(),
		"CLAUDE.md", "# x\n\n- **Skills:** none\n\n[i](docs/claude/INDEX.md)\n",
		"docs/claude/INDEX.md", "[r](README.md)\n",
		"docs/claude/README.md", gone,
		"pkg/CLAUDE.md", "# pkg\n\n[r](../docs/claude/README.md)\n")
	n := 0
	for _, p := range problems(t, both, config{}) {
		if strings.Contains(p, `broken link "gone.md"`) {
			n++
		}
	}
	assert.Equal(t, 1, n)
}

func TestFences(t *testing.T) {
	doc := func(s string) map[string]string { return with(minimal(), "pkg/CLAUDE.md", "# pkg\n\n"+s) }
	const phrase = `: self-deferring phrase "see readme": state the rule inline`
	assert.Equal(t, []string{"pkg/CLAUDE.md:3: unclosed code fence: the checks skip the rest of the file"},
		problems(t, doc("```\nsee README\n"), config{}), "an unclosed fence is reported at its opening line")
	stray := "## Rules\n\n```\nnot closed\n\n- **Do X.** no marker; see README; [x](gone.md)\n"
	assert.Equal(t, []string{"pkg/CLAUDE.md:5: unclosed code fence: the checks skip the rest of the file"},
		problems(t, doc(stray), config{}), "what an unclosed fence hides is reported, not skipped silently")
	assert.Equal(t, []string{"pkg/CLAUDE.md:10" + phrase},
		problems(t, doc("````markdown\n```go\nsee README\n```\nsee README\n````\n\nafter: see README\n"), config{}),
		"a longer fence holds a shorter one")
	assert.Equal(t, []string{"pkg/CLAUDE.md:7" + phrase},
		problems(t, doc("~~~\nsee README\n~~~\n\nsee README\n"), config{}), "tilde fences")
	assert.Equal(t, []string{"pkg/CLAUDE.md:9" + phrase},
		problems(t, doc("```\nsee README\n~~~\nstill inside, see README\n```\n\nsee README\n"), config{}), "a fence closes on its own character")
	assert.Equal(t, []string{"pkg/CLAUDE.md:9" + phrase},
		problems(t, doc("```\nx\n``` y\nsee README\n```\n\nsee README\n"), config{}), "a closing fence has nothing after it")
	assert.Equal(t, []string{"pkg/CLAUDE.md:3" + phrase},
		problems(t, doc("```code``` see README\n"), config{}), "a backtick run with backticks after it is a code span, not a fence")
	assert.Equal(t, []string{"pkg/CLAUDE.md:7" + phrase},
		problems(t, doc("  ```\n  see README\n  ```\n\nsee README\n"), config{}), "an indented fence")
	assert.Equal(t, []string{"pkg/CLAUDE.md:3" + phrase},
		problems(t, doc("``a`` see README\n"), config{}), "two backticks are a code span, not a fence")
	assert.Equal(t, []string{"pkg/CLAUDE.md:4" + phrase},
		problems(t, doc("``\nsee README\n"), config{}), "a run of two is no fence, even with nothing after it")
	readme := with(minimal(), "pkg/README.md", "# pkg\n\n~~~\nopen\n")
	assert.Contains(t, problems(t, readme, config{}), "pkg/README.md:3: unclosed code fence: the checks skip the rest of the file", "every doc is checked, not only CLAUDE.md")
	assert.Equal(t, 0, unclosedFence([]byte("```\nx\n```\n")))
	assert.Equal(t, 2, unclosedFence([]byte("x\n````\n```\nx\n```\n")))
}

func TestAutoLoaded(t *testing.T) {
	const why = "loads in every session or bypasses the budgets; the conventions keep rules in <dir>/CLAUDE.md"
	assert.Contains(t, problems(t, with(minimal(), ".claude/CLAUDE.md", "x"), config{}), ".claude/CLAUDE.md: "+why)
	assert.Contains(t, problems(t, with(minimal(), ".claude/rules/go.md", "x"), config{}), ".claude/rules: "+why)
	assert.Contains(t, problems(t, with(minimal(), "CLAUDE.local.md", "x"), config{}), "CLAUDE.local.md: "+why)
	assert.Contains(t, problems(t, with(minimal(), "pkg/CLAUDE.local.md", "x"), config{}), "pkg/CLAUDE.local.md: "+why)
	fine := with(minimal(), ".claude/skills/dev/SKILL.md", "x", "CLAUDE.md", "- **Skills:** `dev`\n",
		"vendor/v/CLAUDE.local.md", "x", "pkg/testdata/CLAUDE.local.md", "x")
	assert.Empty(t, problems(t, fine, config{}), "skills load on demand; vendor/ and testdata/ are not ours")
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
	quoted := "# x\n\n- **Skills:** none\n\nThe line looks like:\n\n```markdown\n- **Skills:** `example`\n```\n"
	assert.Empty(t, problems(t, with(minimal(), "CLAUDE.md", quoted), config{}), "a quoted anchor inside a fence is not a second line")
	onlyQuoted := "# x\n\n```markdown\n- **Skills:** none\n```\n"
	assert.Contains(t, problems(t, with(minimal(), "CLAUDE.md", onlyQuoted), config{}), `CLAUDE.md: no line starting "- **Skills:**" to list .claude/skills`,
		"nor is it the list")
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
