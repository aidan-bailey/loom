package reviewui

import (
	"bytes"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// highlightCode returns syntax-highlighted lines for a file using Chroma.
// Each line is pre-rendered with ANSI escape codes.
func highlightCode(filename string, content string) []string {
	lexer := lexers.Match(filename)
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)

	style := styles.Get("monokai")
	formatter := formatters.Get("terminal256")

	iterator, err := lexer.Tokenise(nil, content)
	if err != nil {
		// Fallback to raw lines
		return strings.Split(content, "\n")
	}

	var buf bytes.Buffer
	if err := formatter.Format(&buf, style, iterator); err != nil {
		return strings.Split(content, "\n")
	}

	// Split the ANSI output by newlines
	lines := strings.Split(buf.String(), "\n")

	// Chroma sometimes adds a trailing empty line from the final newline
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	return lines
}
