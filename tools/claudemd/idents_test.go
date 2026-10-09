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

// TestIdents_AllowList uses synthetic names: the lint indexes this file's
// words like any source, so a name a real doc cites (a removed method, say)
// would count as found in the repo and hide that doc's finding.
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

func TestIdents_PkgSymbols(t *testing.T) {
	files := with(minimal(),
		"internal/exec/command.go", "package exec\n\nfunc Default() {}\n",
		"internal/other/other.go", "package other\n\nfunc Elsewhere() {}\n",
		"pkg/CLAUDE.md", "# pkg\n\n`internal/exec.Default` `internal/exec.Gone` `internal/exec.Elsewhere`\n")
	assert.ElementsMatch(t, []string{
		"pkg/CLAUDE.md:3: internal/exec.Gone: not found in any source file (Markdown excluded)",
		"pkg/CLAUDE.md:3: internal/exec.Elsewhere: found, but not in internal/exec: check where the rule says it lives",
	}, findings(t, files), "the symbol must be in the named package dir; Default is")
}
