// Command claudemd holds the repo's docs to the conventions the root
// CLAUDE.md sets out. It fails on structure: line, byte and line-length
// budgets; self-deferring phrases; package files titled by their dir with
// every rule saying what guards it; a CLAUDE.md covering every Go package;
// local links that resolve; no orphan docs/claude guides; a skills list
// matching .claude/skills; and the identifiers it can check without false
// positives (repo paths, globs, file.go:Symbol, Go test names). With -idents
// it also reports other backticked symbols it can't find in source, which is
// advisory unless -strict.
//
// It checks structure, not truth: a rule naming a symbol that moved can still
// pass. docs/claude/auditing-claude-md-currency.md is the audit for that.
//
//	go run ./tools/claudemd            # structure; exit 1 on a problem
//	go run ./tools/claudemd -v         # plus each CLAUDE.md's budget headroom
//	go run ./tools/claudemd -idents    # plus the advisory symbol report
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func main() {
	root := flag.String("root", ".", "the repo root (the directory holding go.mod)")
	withIdents := flag.Bool("idents", false, "also report backticked symbols not found in source (advisory)")
	strict := flag.Bool("strict", false, "with -idents, exit 1 on any finding")
	verbose := flag.Bool("v", false, "print each CLAUDE.md's budget headroom")
	flag.Parse()
	opt := options{idents: *withIdents, strict: *strict, verbose: *verbose}
	os.Exit(run(*root, repoConfig, opt, os.Stdout, os.Stderr))
}

// options are the CLI's switches.
type options struct{ idents, strict, verbose bool }

// run checks the repo at root with cfg and returns the exit code: 0 clean,
// 1 a problem (or, with strict, an advisory finding), 2 a usage or I/O
// error.
func run(root string, cfg config, opt options, stdout, stderr io.Writer) int {
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		fmt.Fprintf(stderr, "claudemd: no go.mod in %s: run it from the repo root or pass -root\n", root)
		return 2
	}
	ps, err := check(root, cfg)
	if err != nil {
		fmt.Fprintln(stderr, "claudemd:", err)
		return 2
	}
	code := 0
	for _, p := range ps {
		fmt.Fprintln(stdout, p)
		code = 1
	}
	if opt.verbose {
		rows, err := headroom(root)
		if err != nil {
			fmt.Fprintln(stderr, "claudemd:", err)
			return 2
		}
		for _, r := range rows {
			fmt.Fprintln(stdout, r)
		}
	}
	if opt.idents {
		findings, err := idents(root)
		if err != nil {
			fmt.Fprintln(stderr, "claudemd:", err)
			return 2
		}
		for _, f := range findings {
			fmt.Fprintln(stdout, "advisory:", f)
			if opt.strict {
				code = 1
			}
		}
	}
	return code
}
