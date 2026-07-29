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
