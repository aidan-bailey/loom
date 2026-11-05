package feedback

import (
	"fmt"
	"strings"
	"unicode"
)

// FixTitleWidth is core.MaxTitleWidth (32), which this package can't
// import; TestFixTitle_FitsCoresLimit in core pins the two together.
const FixTitleWidth = 32

// FixTitle is thread t's fix session title: "fb-<t>-<slug>", the slug the
// title's ASCII letters and digits, lowercased, runs joined by "-", the
// whole cut to FixTitleWidth with no trailing "-".
func FixTitle(t ThreadID, title string) string {
	var b strings.Builder
	gap := false
	for _, r := range strings.ToLower(title) {
		if r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			if gap && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			gap = false
			continue
		}
		gap = true
	}
	s := fmt.Sprintf("fb-%d", t)
	if b.Len() > 0 {
		s += "-" + b.String()
	}
	if len(s) > FixTitleWidth {
		s = s[:FixTitleWidth]
	}
	return strings.TrimRight(s, "-")
}

// FixPrompt is the fix session's first prompt (spec §7): the thread, every
// note with who filed it, the build it ran and where its transcript is, and
// the instruction to check the base branch first.
func FixPrompt(in Inbox, t Thread) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Fix a problem loom's session agents reported (feedback thread %d, %s): %s\n\n", t.ID, t.Kind, t.Title)
	b.WriteString("Reporters may run an older loom than this branch. First check whether the base branch already fixes this. If it does, say so and stop. If not, reproduce it, then propose a fix to the user before building it.\n\n")
	fmt.Fprintf(&b, "The thread's notes (%d):\n", len(t.Notes))
	for _, id := range t.Notes {
		n, ok := in.Note(id)
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "\n## Note %d (%s), from %q in %s, %s\n", n.ID, n.Kind, n.From.Title, n.From.Workspace, n.At.Format("2006-01-02 15:04"))
		if n.Build != "" {
			fmt.Fprintf(&b, "Loom build: %s\n", n.Build)
		}
		if n.From.ClaudeSession != "" {
			fmt.Fprintf(&b, "Transcript: Claude session %s", n.From.ClaudeSession)
			if n.From.Transcript != "" {
				fmt.Fprintf(&b, " at %s", n.From.Transcript)
			}
			b.WriteString(" (once that session stops, loom's archive/chats/ holds a copy)\n")
		}
		fmt.Fprintf(&b, "\n%s\n\n%s\n", n.Title, n.Text)
	}
	return b.String()
}
