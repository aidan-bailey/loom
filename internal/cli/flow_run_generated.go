package cli

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/aidan-bailey/loom/internal/flowrun"
)

// generatedTrailer is the trailer of the engine's commit of generated
// files, naming the gate run that left them changed: a restarted engine
// knows the commit at HEAD as its own by it.
const generatedTrailer = "Loom-Flow-Gate: "

// CommitGenerated commits what gate run name left changed in the run's
// worktree when every change is to a file globs match, with hooks skipped
// (feedback thread 22: a test in the gates rewrote a stale bindings file).
// A commit of its own for name at HEAD, which a crash left unmemoized, is
// given again.
func (fx *runFX) CommitGenerated(name string, globs []string) (flowrun.Regenerated, error) {
	var none flowrun.Regenerated
	msg, err := fx.git("log", "-1", "--format=%B")
	if err != nil {
		return none, err
	}
	if slices.Contains(strings.Split(msg, "\n"), generatedTrailer+name) {
		head, err := fx.Head("")
		if err != nil {
			return none, err
		}
		files, err := fx.git("diff-tree", "--no-commit-id", "--name-only", "--no-renames", "-r", "HEAD")
		if err != nil {
			return none, err
		}
		return flowrun.Regenerated{Head: head, Paths: strings.Split(files, "\n")}, nil
	}
	paths, ok, err := fx.generatedChanges(globs)
	if err != nil || !ok {
		return none, err
	}
	if _, err := fx.git(append([]string{"add", "--"}, paths...)...); err != nil {
		return none, err
	}
	if _, err := fx.git(append([]string{"commit", "--no-verify", "-q", "-m", generatedMessage(name, paths), "--"}, paths...)...); err != nil {
		return none, err
	}
	head, err := fx.Head("")
	if err != nil {
		return none, err
	}
	fx.logf("%s left %s changed: committed as %s", name, strings.Join(paths, ", "), shortSHA(head))
	return flowrun.Regenerated{Head: head, Paths: paths}, nil
}

// generatedChanges is the paths the run's worktree has changed, and true,
// when every change is a modified, added or untracked file globs match;
// false for a clean tree or any other change (a deletion, a rename, a
// conflict, a file no glob matches), which the engine's tree check then
// parks on.
func (fx *runFX) generatedChanges(globs []string) ([]string, bool, error) {
	out, err := fx.gitRawIn(fx.cwd, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil || out == "" {
		return nil, false, err
	}
	var paths []string
	for _, entry := range strings.Split(strings.TrimSuffix(out, "\x00"), "\x00") {
		// "XY path"; a rename's or copy's second field never comes: R and C fail first.
		if len(entry) < 4 || entry[2] != ' ' || !generatedStatus(entry[:2]) || !globMatch(entry[3:], globs) {
			return nil, false, nil
		}
		paths = append(paths, entry[3:])
	}
	return paths, true, nil
}

// generatedStatus reports whether a porcelain status is a file the build
// could have written: untracked, or added or modified in the index and
// modified or not in the tree.
func generatedStatus(xy string) bool {
	return xy == "??" || strings.ContainsRune(" MA", rune(xy[0])) && strings.ContainsRune(" M", rune(xy[1]))
}

// generatedMessage is the engine's commit message for paths gate run name
// left changed, ending in its trailer.
func generatedMessage(name string, paths []string) string {
	bases := make([]string, len(paths))
	for i, p := range paths {
		bases[i] = path.Base(p)
	}
	return fmt.Sprintf("chore: regenerate %s\n\nThe gate run %s left these generated files changed, and loom flow run committed them:\n- %s\n\n%s%s\n",
		strings.Join(bases, ", "), name, strings.Join(paths, "\n- "), generatedTrailer, name)
}

// globMatch reports whether p matches one of globs as flow's glob_match
// does (flow/lib/flowconf.py): fnmatch on the path or on its base name, `*`
// crossing `/`. A glob that doesn't translate matches nothing.
func globMatch(p string, globs []string) bool {
	for _, g := range globs {
		re, err := fnmatchRegexp(g)
		if err == nil && (re.MatchString(p) || re.MatchString(path.Base(p))) {
			return true
		}
	}
	return false
}

// fnmatchRegexp is Python's fnmatch.translate for Go's regexp: `*` any run,
// `?` one character, `[…]` a set and `[!…]` its complement, a `[` with no
// `]` after it literal, and every other character literal.
func fnmatchRegexp(glob string) (*regexp.Regexp, error) {
	rs := []rune(glob)
	var b strings.Builder
	b.WriteString(`(?s)\A`)
	for i := 0; i < len(rs); {
		c := rs[i]
		i++
		switch c {
		case '*':
			b.WriteString(`.*`)
		case '?':
			b.WriteString(`.`)
		case '[':
			j := i
			if j < len(rs) && rs[j] == '!' {
				j++
			}
			if j < len(rs) && rs[j] == ']' {
				j++
			}
			for j < len(rs) && rs[j] != ']' {
				j++
			}
			if j >= len(rs) {
				b.WriteString(`\[`)
				continue
			}
			set := strings.ReplaceAll(string(rs[i:j]), `\`, `\\`) // never empty: a leading ] is the set's own
			i = j + 1
			switch set[0] {
			case '!':
				set = "^" + set[1:]
			case '^', '[':
				set = `\` + set
			}
			b.WriteString("[" + set + "]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString(`\z`)
	return regexp.Compile(b.String())
}
