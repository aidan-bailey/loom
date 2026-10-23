package git

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Patch is a worktree's diff against its base commit, file by file.
type Patch struct {
	// Base is the base commit's SHA.
	Base  string
	Files []FilePatch
}

// FilePatch is one file's part of a patch.
type FilePatch struct {
	// Path is the file's path in the worktree; OldPath is the path it was
	// renamed or copied from, "" otherwise.
	Path, OldPath string
	// Status is StatusAdded, StatusModified, StatusDeleted, StatusRenamed
	// or StatusCopied.
	Status string
	Binary bool
	// Added and Removed count the file's added and removed lines, kept
	// hunks or not.
	Added, Removed int
	Hunks          []Hunk
	// Omitted says why a file's hunks were dropped: OmittedLines or
	// OmittedBudget; "" when they are kept.
	Omitted string
}

// Hunk is one hunk of a file.
type Hunk struct {
	// Header is its @@ line.
	Header string
	// Lines are its lines with their prefixes (' ', '+', '-' or '\'); a
	// blank context line git wrote empty stays "".
	Lines []string
	// Cut lists the indexes of the lines cut at maxPatchLineBytes.
	Cut []int
}

// A file's status in a patch.
const (
	StatusAdded    = "A"
	StatusModified = "M"
	StatusDeleted  = "D"
	StatusRenamed  = "R"
	StatusCopied   = "C"
)

// Why a file's hunks were dropped.
const (
	OmittedLines  = "lines"
	OmittedBudget = "budget"
)

// A patch's bounds: the kept lines total at most maxPatchBytes, a file of
// more than maxFileLines lines keeps only its counts, and a line is cut at
// maxPatchLineBytes.
const (
	maxPatchBytes     = 1 << 20
	maxFileLines      = 5000
	maxPatchLineBytes = 2000
)

// Patch returns the worktree's diff against its base commit, as Diff runs
// it but for parsing: stdout alone, so a warning on stderr never lands in
// it, and with colour, external diff drivers and the a/ and b/ prefixes
// pinned, so the user's diff config can't change what SplitPatch reads.
func (g *GitWorktree) Patch() (string, error) {
	base := g.GetBaseCommitSHA()
	if base == "" {
		return "", fmt.Errorf("base commit SHA not set")
	}
	if err := g.ensureUntrackedStaged(time.Now); err != nil {
		return "", err
	}
	return g.runGitStdout(g.worktreePath, "--no-pager", "diff", "--no-color", "--no-ext-diff", "--src-prefix=a/", "--dst-prefix=b/", base)
}

// SplitPatch splits a patch Patch returned into its files, in git's order.
// Each file's counts are exact, and its hunks are kept within the patch's
// bounds: once the kept lines reach maxPatchBytes, every later file keeps
// only its counts.
func SplitPatch(text string) []FilePatch {
	lines := strings.Split(text, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	files := []FilePatch{}
	used, spent := 0, false
	for i := 0; i < len(lines); {
		if !strings.HasPrefix(lines[i], "diff --git ") {
			i++
			continue
		}
		f, size, next := splitFile(lines, i)
		switch {
		case len(f.Hunks) == 0:
		case lineCount(f.Hunks) > maxFileLines:
			f.Hunks, f.Omitted = nil, OmittedLines
		case spent || used+size > maxPatchBytes:
			f.Hunks, f.Omitted, spent = nil, OmittedBudget, true
		default:
			used += size
		}
		files = append(files, f)
		i = next
	}
	return files
}

// splitFile reads the file whose "diff --git" line is lines[i]: its header
// lines, then its hunks. It returns the file, the bytes its kept lines
// take, and the index of the line after it.
func splitFile(lines []string, i int) (FilePatch, int, int) {
	f := FilePatch{Status: StatusModified}
	header := lines[i]
	var oldName, newName string
	named := false
	for i++; i < len(lines) && !strings.HasPrefix(lines[i], "@@ ") && !strings.HasPrefix(lines[i], "diff --git "); i++ {
		l := lines[i]
		switch {
		case strings.HasPrefix(l, "new file mode "):
			f.Status = StatusAdded
		case strings.HasPrefix(l, "deleted file mode "):
			f.Status = StatusDeleted
		case strings.HasPrefix(l, "rename from "):
			f.Status, oldName, named = StatusRenamed, unquote(strings.TrimPrefix(l, "rename from ")), true
		case strings.HasPrefix(l, "rename to "):
			f.Status, newName, named = StatusRenamed, unquote(strings.TrimPrefix(l, "rename to ")), true
		case strings.HasPrefix(l, "copy from "):
			f.Status, oldName, named = StatusCopied, unquote(strings.TrimPrefix(l, "copy from ")), true
		case strings.HasPrefix(l, "copy to "):
			f.Status, newName, named = StatusCopied, unquote(strings.TrimPrefix(l, "copy to ")), true
		case strings.HasPrefix(l, "--- "):
			if p := side(l[4:], "a/"); p != "" {
				oldName, named = p, true
			}
		case strings.HasPrefix(l, "+++ "):
			if p := side(l[4:], "b/"); p != "" {
				newName, named = p, true
			}
		case strings.HasPrefix(l, "Binary files ") && strings.HasSuffix(l, " differ"):
			f.Binary = true
		}
	}
	if !named {
		oldName, newName = headerNames(header)
	}
	switch {
	case f.Status == StatusDeleted:
		f.Path = oldName
	case f.Status == StatusRenamed || f.Status == StatusCopied:
		f.Path, f.OldPath = newName, oldName
	case newName != "":
		f.Path = newName
	default:
		f.Path = oldName
	}
	size := 0
	for i < len(lines) && strings.HasPrefix(lines[i], "@@ ") {
		h, next, n, ok := splitHunk(lines, i, &f)
		if !ok {
			// A header git did not write as one: the rest of the file is
			// skipped up to the next.
			return f, size, i + 1
		}
		f.Hunks = append(f.Hunks, h)
		size += n
		i = next
	}
	return f, size, i
}

// hunkRange reads a hunk header's line counts: @@ -a[,b] +c[,d] @@.
var hunkRange = regexp.MustCompile(`^@@ -\d+(?:,(\d+))? \+\d+(?:,(\d+))? @@`)

// splitHunk reads the hunk whose header is lines[i], adding its changed
// lines to f's counts. Its lines are counted by the header's ranges, so an
// added line that reads "+++ x" stays a line. It returns the hunk, the
// index after it, the bytes its kept lines take, and false for a header it
// cannot read.
func splitHunk(lines []string, i int, f *FilePatch) (Hunk, int, int, bool) {
	m := hunkRange.FindStringSubmatch(lines[i])
	if m == nil {
		return Hunk{}, i, 0, false
	}
	oldLeft, newLeft := rangeCount(m[1]), rangeCount(m[2])
	h := Hunk{Header: lines[i]}
	size := len(lines[i])
	for i++; i < len(lines); i++ {
		l := lines[i]
		switch {
		case strings.HasPrefix(l, `\`):
		case oldLeft <= 0 && newLeft <= 0:
			return h, i, size, true
		case l == "" || l[0] == ' ':
			oldLeft, newLeft = oldLeft-1, newLeft-1
		case l[0] == '-':
			oldLeft--
			f.Removed++
		case l[0] == '+':
			newLeft--
			f.Added++
		default:
			return h, i, size, true
		}
		kept, cut := capLine(l)
		if cut {
			h.Cut = append(h.Cut, len(h.Lines))
		}
		h.Lines = append(h.Lines, kept)
		size += len(kept)
	}
	return h, i, size, true
}

// rangeCount is a hunk range's count, which git leaves out when it is 1.
func rangeCount(s string) int {
	if s == "" {
		return 1
	}
	n, _ := strconv.Atoi(s)
	return n
}

func lineCount(hunks []Hunk) int {
	n := 0
	for _, h := range hunks {
		n += len(h.Lines)
	}
	return n
}

// capLine cuts a line at maxPatchLineBytes, back to a rune boundary.
func capLine(l string) (string, bool) {
	if len(l) <= maxPatchLineBytes {
		return l, false
	}
	n := maxPatchLineBytes
	for n > 0 && !utf8.RuneStart(l[n]) {
		n--
	}
	return l[:n], true
}

// side is the path a ---/+++ line names, without its a/ or b/ prefix, or
// "" for /dev/null. Git ends the line with a tab when the path holds a
// space.
func side(p, prefix string) string {
	p = strings.TrimSuffix(p, "\t")
	if p == "/dev/null" {
		return ""
	}
	return strings.TrimPrefix(unquote(p), prefix)
}

// unquote undoes git's C-style quoting of a path it quoted.
func unquote(p string) string {
	if !strings.HasPrefix(p, `"`) {
		return p
	}
	if s, err := strconv.Unquote(p); err == nil {
		return s
	}
	return p
}

// headerNames reads the two paths of a "diff --git a/<p> b/<q>" line, for
// a file with no ---/+++, rename or copy line (binary, mode-only, empty).
// Its two paths are then the same, which is how an unquoted line whose path
// holds a space splits.
func headerNames(header string) (string, string) {
	rest := strings.TrimPrefix(header, "diff --git ")
	if strings.HasPrefix(rest, `"`) {
		a, after, ok := cutQuoted(rest)
		if !ok {
			return "", ""
		}
		b := strings.TrimPrefix(after, " ")
		if strings.HasPrefix(b, `"`) {
			b, _, _ = cutQuoted(b)
		}
		return strings.TrimPrefix(a, "a/"), strings.TrimPrefix(b, "b/")
	}
	if n := len(rest); n >= 5 && n%2 == 1 {
		half := n / 2
		a, b := rest[:half], rest[half+1:]
		if rest[half] == ' ' && strings.HasPrefix(a, "a/") && strings.HasPrefix(b, "b/") && a[2:] == b[2:] {
			return a[2:], b[2:]
		}
	}
	if i := strings.LastIndex(rest, " b/"); i >= 0 {
		return strings.TrimPrefix(rest[:i], "a/"), rest[i+3:]
	}
	return rest, rest
}

// cutQuoted splits s, which starts with a quoted path, after that path.
func cutQuoted(s string) (string, string, bool) {
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			name, err := strconv.Unquote(s[:i+1])
			return name, s[i+1:], err == nil
		}
	}
	return "", "", false
}
