package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// findings runs the advisory report over files and returns each finding as text.
func findings(t *testing.T, files map[string]string) []string {
	t.Helper()
	ps, err := idents(tree(t, files))
	require.NoError(t, err)
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.String()
	}
	return out
}

func TestStrong_Paths(t *testing.T) {
	base := func(doc string) map[string]string {
		return with(minimal(), "pkg/gate.go", "package pkg\n\nvar tickInterval = 1\n", "pkg/CLAUDE.md", "# pkg\n\n"+doc)
	}
	assert.Empty(t, problems(t, base("`gate.go` `pkg/gate.go` `pkg/gate.go:tickInterval` `pkg/gate.go:3` `./pkg/` `pkg/*.go` `*_test.go` `logs/serve.log` `worktrees/`\n"), config{}),
		"beside the doc, from the root, file:symbol, file:line, ./ trimmed, a matching glob; bare patterns and runtime paths skipped")
	assert.Contains(t, problems(t, base("`pkg/gone.go`\n"), config{}), "pkg/CLAUDE.md:3: pkg/gone.go: path not found")
	assert.Contains(t, problems(t, base("`pkg/gate.go:noSuch`\n"), config{}), "pkg/CLAUDE.md:3: pkg/gate.go:noSuch: noSuch is not in pkg/gate.go")
	assert.Contains(t, problems(t, base("`pkg/x_*.go`\n"), config{}), "pkg/CLAUDE.md:3: pkg/x_*.go: glob matches no file")
	assert.Contains(t, problems(t, with(base(""), "CLAUDE.md", "- **Skills:** none\n`gate.go`\n"), config{}),
		"CLAUDE.md:2: gate.go: no such file beside this doc or at the root: write the path from the repo root")
}

func TestStrong_TestNames(t *testing.T) {
	base := func(doc string) map[string]string {
		return with(minimal(), "pkg/pkg_test.go", "package pkg\n\nfunc TestKill_Exact(t *testing.T) {}\n", "pkg/CLAUDE.md", "# pkg\n\n"+doc)
	}
	assert.Empty(t, problems(t, base("Pinned by `TestKill_Exact` and the `TestKill_…` family.\n"), config{}))
	assert.Empty(t, problems(t, base("Other shapes: `Test…_…` and `TestKill_…_Exact`.\n"), config{}), "only a trailing … names a family")
	assert.Contains(t, problems(t, base("Pinned by `TestRenamedAway`.\n"), config{}), "pkg/CLAUDE.md:3: TestRenamedAway: no Go test function has this name")
	assert.Contains(t, problems(t, base("The `TestGone_…` family.\n"), config{}), "pkg/CLAUDE.md:3: TestGone_…: no Go test function starts with TestGone_")
}

func TestIdents_Symbols(t *testing.T) {
	base := func(kv ...string) map[string]string {
		m := with(minimal(),
			"pkg/thing.go", "package pkg\n\nfunc DoThing() {}\n\nconst LOOM_FLAG = \"x\"\n",
			"other/other.go", "package other\n\nfunc Elsewhere() {}\n",
			"other/CLAUDE.md", "# other\n")
		return with(m, kv...)
	}
	assert.Empty(t, findings(t, base("pkg/CLAUDE.md", "`DoThing()` `pkg.DoThing` `LOOM_FLAG` `Thing.DoThing` `vendor` `go test ./...` `<name>` `--resume` `ctrl+a` `v1.60.1`\n")))
	assert.Contains(t, findings(t, base("pkg/CLAUDE.md", "`GoneSymbol`\n")), "pkg/CLAUDE.md:1: GoneSymbol: not found in any source file (Markdown excluded)")
	readme := findings(t, base("pkg/README.md", "Mentions `OnlyInDocs` and `pkg.Elsewhere`.\n", "other/README.md", "`OnlyInDocs` again.\n"))
	assert.Contains(t, readme, "pkg/README.md:1: OnlyInDocs: not found in any source file (Markdown excluded)", "a doc can't confirm a symbol")
	assert.Contains(t, readme, "pkg/README.md:1: pkg.Elsewhere: found, but not in a pkg package dir: check where the rule says it lives")
	assert.Empty(t, findings(t, base("pkg/CLAUDE.md", "```\n`GoneSymbol`\n```\n")), "fenced blocks are not read")
}

// TestIdents_AllowList uses synthetic names, so a fixture can't read as a
// token a real doc cites (a removed method, say).
func TestIdents_AllowList(t *testing.T) {
	allow := "# header\n\nRemovedHelper  # negative claim: removed from core.Core\nTestRemoved  # negative claim: went with its feature\n"
	files := with(minimal(), "pkg/CLAUDE.md", "# pkg\n\n`RemovedHelper` and `TestRemoved` are gone.\n", "tools/claudemd/idents.allow", allow)
	assert.Empty(t, findings(t, files))
	assert.Empty(t, problems(t, files, config{}))
	noReason := with(files, "tools/claudemd/idents.allow", "RemovedHelper\nTestRemoved  # gone\n")
	assert.Contains(t, problems(t, noReason, config{}), "tools/claudemd/idents.allow:1: allowed token with no reason")
}

func TestStrong_NotPaths(t *testing.T) {
	base := func(doc string) map[string]string {
		return with(minimal(),
			"internal/exec/command.go", "package exec\n\nfunc Default() {}\n",
			"internal/exec/CLAUDE.md", "# internal/exec\n",
			"pkg/CLAUDE.md", "# pkg\n\n"+doc)
	}
	assert.Empty(t, problems(t, base("Suffixes `_test.go`, `_unix.go` and `_windows.go`.\n"), config{}),
		"a leading _ makes a file-name suffix, which describes files as a bare glob does")
	assert.Empty(t, problems(t, base("`internal/exec.Default` `internal/exec.Default()` `./internal/exec.Default` `golang.org/x/sys.Foo`\n"), config{}),
		"dir/pkg.Symbol names a symbol in a package dir, not a file; an import path outside the repo is skipped")
	assert.Contains(t, problems(t, base("`internal/gone.Default`\n"), config{}), "pkg/CLAUDE.md:3: internal/gone.Default: path not found")
	assert.Contains(t, problems(t, base("`gate.go`\n"), config{}),
		"pkg/CLAUDE.md:3: gate.go: no such file beside this doc or at the root: write the path from the repo root", "a bare file name is still a problem")
}

func TestStrong_PathForms(t *testing.T) {
	base := func(doc string) map[string]string {
		return with(minimal(),
			"pkg/gate.go", "package pkg\n",
			"pkg/sub/sub.go", "package sub\n\nfunc helper() {}\n",
			"assets/Logo.PNG", "png",
			"pkg/sub.json", "{}",
			".git", "gitdir: /elsewhere\n",
			"pkg/CLAUDE.md", "# pkg\n\n"+doc)
	}
	assert.Empty(t, problems(t, base("`pkg/gate.go:3-4` `pkg/gate.go:3:5` `pkg/gate.go#L3` `pkg/gate.go#L3-L5` `pkg/gate.go#L3-5` `gate.go:7-9`\n"), config{}),
		"a line, a range, line:column and a #L anchor all point into a file that exists")
	assert.Contains(t, problems(t, base("`pkg/gone.go:3-4`\n"), config{}), "pkg/CLAUDE.md:3: pkg/gone.go:3-4: path not found", "and into one that doesn't")
	assert.Empty(t, problems(t, base("`assets/Logo.PNG`\n"), config{}), "an existing path passes whatever its extension")
	assert.Empty(t, findings(t, base("`pkg/sub.json`\n")), "an existing file isn't taken for a symbol because a dir of that name exists")
	assert.Contains(t, problems(t, base("`assets/Gone.PNG`\n"), config{}), "pkg/CLAUDE.md:3: assets/Gone.PNG: path not found")
	assert.Empty(t, problems(t, base("`pkg/sub.helper` `pkg/sub.Exported`\n"), config{}), "dir/pkg.symbol, lower case too, is a symbol in a package dir")
	assert.Contains(t, problems(t, base("`pkg/sub.go`\n"), config{}), "pkg/CLAUDE.md:3: pkg/sub.go: path not found",
		"a missing file isn't taken for a symbol because a dir of that name exists")
	assert.Empty(t, problems(t, base("`.git/info/exclude` `.git`\n"), config{}), ".git is a file in a worktree, so its paths are runtime paths")
	assert.Empty(t, problems(t, base("`./logs/serve.log` `./worktrees/`\n"), config{}), "./ is trimmed first, or . would make every such path look like the repo's")
	assert.Contains(t, problems(t, base("`./pkg/gone.go`\n"), config{}), "pkg/CLAUDE.md:3: ./pkg/gone.go: path not found")
	assert.ElementsMatch(t, []string{
		"pkg/CLAUDE.md:3: pkg/sub.gone: not found in any source file (Markdown excluded)",
	}, findings(t, base("`pkg/sub.helper` `pkg/sub.gone`\n")), "a lower-case package symbol is still checked, as an advisory")
}

func TestIdents_PkgSymbols(t *testing.T) {
	files := with(minimal(),
		"internal/exec/command.go", "package exec\n\nfunc Default() {}\n",
		"internal/other/other.go", "package other\n\nfunc Elsewhere() {}\n",
		"pkg/CLAUDE.md", "# pkg\n\n`internal/exec.Default` `internal/exec.Gone` `internal/exec.Elsewhere` `internal/exec.Run`\n")
	assert.ElementsMatch(t, []string{
		"pkg/CLAUDE.md:3: internal/exec.Gone: not found in any source file (Markdown excluded)",
		"pkg/CLAUDE.md:3: internal/exec.Elsewhere: found, but not in internal/exec: check where the rule says it lives",
	}, findings(t, files), "the symbol must be in the named package dir (Default is); a name under four characters is left alone, as for any symbol")
}

// TestIndex_Declarations: a test name is a function declared in a _test.go
// file, not text that looks like one in a string, a comment or elsewhere.
func TestIndex_Declarations(t *testing.T) {
	files := with(minimal(),
		"tools/x/x_test.go", "package x\n\n"+
			"var raw = `\nfunc TestInRawString(t *testing.T) {}\n`\n\n"+
			"// func TestInComment(t *testing.T) {}\n\n"+
			"func TestDeclared(t *testing.T) {}\n\n"+
			"func (s *S) TestMethod(t *testing.T) {}\n",
		"tools/x/helper.go", "package x\n\nfunc TestInNonTestFile(t *testing.T) {}\n",
		"tools/x/CLAUDE.md", "# tools/x\n",
		"pkg/CLAUDE.md", "# pkg\n\n`TestDeclared` `TestInRawString` `TestInComment` `TestMethod` `TestInNonTestFile`\n")
	ps := problems(t, files, config{})
	for _, name := range []string{"TestInRawString", "TestInComment", "TestMethod", "TestInNonTestFile"} {
		assert.Contains(t, ps, "pkg/CLAUDE.md:3: "+name+": no Go test function has this name")
	}
	assert.Len(t, ps, 4, "TestDeclared is declared")
}

// TestIndex_Words: a Go file is read as Go. Identifiers and comments confirm a
// symbol everywhere, string literals only outside _test.go files, where they
// hold fixtures and lists of names that must not exist.
func TestIndex_Words(t *testing.T) {
	files := with(minimal(),
		"tools/x/x_test.go", "package x\n\n"+
			"var fixture = \"OnlyInTestString\"\n\n"+
			"var forbidden = []string{'a', \"AlsoOnlyInTestString\"}\n\n"+
			"// CommentInTest explains.\n\n"+
			"func HelperInTest() {}\n",
		"tools/x/x.go", "package x\n\n"+
			"var s = \"line\\nAfterEscape\"\n\n"+
			"var raw = `RawInCode`\n\n"+
			"// CommentInCode explains.\n",
		"tools/x/conf.yml", "key: FromYaml\n",
		"tools/x/CLAUDE.md", "# tools/x\n",
		"pkg/CLAUDE.md", "# pkg\n\n`OnlyInTestString` `AlsoOnlyInTestString` `CommentInTest` `HelperInTest` `AfterEscape` `RawInCode` `CommentInCode` `FromYaml`\n")
	assert.ElementsMatch(t, []string{
		"pkg/CLAUDE.md:3: OnlyInTestString: not found in any source file (Markdown excluded)",
		"pkg/CLAUDE.md:3: AlsoOnlyInTestString: not found in any source file (Markdown excluded)",
	}, findings(t, files))
}

// TestIndex_Dirs: the index reads .github, whose workflows name env vars and
// flags rules cite, but no other dot dir, and not vendor/.
func TestIndex_Dirs(t *testing.T) {
	files := with(minimal(),
		".github/workflows/ci.yml", "env:\n  LOOM_CI_FLAG: 1\n",
		".other/x.yml", "env:\n  LOOM_OTHER_FLAG: 1\n",
		"vendor/v/v.go", "package v\n\nfunc VendoredName() {}\n",
		"pkg/CLAUDE.md", "# pkg\n\n`LOOM_CI_FLAG` `LOOM_OTHER_FLAG` `VendoredName`\n")
	assert.ElementsMatch(t, []string{
		"pkg/CLAUDE.md:3: LOOM_OTHER_FLAG: not found in any source file (Markdown excluded)",
		"pkg/CLAUDE.md:3: VendoredName: not found in any source file (Markdown excluded)",
	}, findings(t, files))
}

func TestIdents_StaleAllow(t *testing.T) {
	allow := "# header\n\nRemovedHelper  # negative claim: gone\nNeverCited  # negative claim: gone\nDoThing  # negative claim: gone\nFenced  # negative claim: gone\nNeverCited  # again\n"
	files := with(minimal(),
		"pkg/thing.go", "package pkg\n\nfunc DoThing() {}\n",
		"pkg/CLAUDE.md", "# pkg\n\n`RemovedHelper` `DoThing`\n\n```\n`Fenced`\n```\n",
		"tools/claudemd/idents.allow", allow)
	assert.ElementsMatch(t, []string{
		"tools/claudemd/idents.allow:4: allowed token NeverCited hides no finding in any doc: drop the entry",
		"tools/claudemd/idents.allow:5: allowed token DoThing hides no finding in any doc: drop the entry",
		"tools/claudemd/idents.allow:6: allowed token Fenced hides no finding in any doc: drop the entry",
	}, problems(t, files, config{}), "an entry is stale when no doc token it matches would be reported: uncited, found after all, or only in a fence; a repeat names its first line")
}
