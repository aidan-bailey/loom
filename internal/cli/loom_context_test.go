package cli

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// loomContextFragments are the loom-context fragments that teach
// `loom job` ("Loom itself") and `loom session` ("Other sessions") to
// every Claude session loom launches (session package,
// WriteLoomContextFiles).
var loomContextFragments = []string{"../../session/loom_context_shared.md", "../../session/loom_context_sessions.md"}

// codeSpan is one inline code span: a run between single backticks.
var codeSpan = regexp.MustCompile("`([^`]+)`")

// Every `loom session <sub> …` or `loom job <sub> …` span in the
// loom-context fragments names a real subcommand and flags it has, and every span
// that is a bare flag one some subcommand of either has. An agent following
// a stale line fails on an unknown command or flag, and nothing else would
// notice.
func TestLoomContext_NamesRealSessionFlags(t *testing.T) {
	var text strings.Builder
	for _, path := range loomContextFragments {
		b, err := os.ReadFile(path)
		require.NoError(t, err)
		text.Write(b)
	}
	roots := map[string]*cobra.Command{"session": newSessionCmd(), "job": newJobCmd()}

	var commands, bare int
	for _, m := range codeSpan.FindAllStringSubmatch(text.String(), -1) {
		span := m[1]
		words := strings.Fields(span)
		switch {
		case len(words) >= 3 && words[0] == "loom" && roots[words[1]] != nil:
			commands++
			sub := subcommandNamed(roots[words[1]], words[2])
			if !assert.NotNil(t, sub, "`%s`: loom %s has no subcommand %q", span, words[1], words[2]) {
				continue
			}
			for _, w := range words[3:] {
				if f, ok := parseFlag(w); ok {
					assert.True(t, f.on(sub), "`%s`: loom %s %s has no flag %s", span, words[1], words[2], w)
				}
			}
		case strings.HasPrefix(span, "-"):
			bare++
			f, ok := parseFlag(words[0])
			assert.True(t, ok && (f.onAny(roots["session"]) || f.onAny(roots["job"])), "`%s`: no loom session or loom job subcommand has this flag", span)
		}
	}
	assert.Positive(t, commands, "no `loom session <subcommand>` or `loom job <subcommand>` span in %s", loomContextFragments)
	assert.Positive(t, bare, "no bare flag span in %s", loomContextFragments)
}

// subcommandNamed is root's subcommand called name, or nil.
func subcommandNamed(root *cobra.Command, name string) *cobra.Command {
	for _, c := range root.Commands() {
		if c.Name() == name {
			return c
		}
	}
	return nil
}

// flagRef is a flag a span names: a long name for "--x", a shorthand for
// "-x".
type flagRef struct {
	name  string
	short bool
}

// parseFlag is the flag word names, brackets and "=value" stripped
// ("[--wait]" is --wait), and whether it names one.
func parseFlag(word string) (flagRef, bool) {
	word = strings.Trim(word, "[]")
	switch {
	case strings.HasPrefix(word, "--") && len(word) > 2:
		name, _, _ := strings.Cut(word[2:], "=")
		return flagRef{name: name}, true
	case strings.HasPrefix(word, "-") && len(word) == 2 && word[1] != '-':
		return flagRef{name: word[1:], short: true}, true
	}
	return flagRef{}, false
}

// on reports whether cmd has f, as its own flag or one it inherits (the
// session command's persistent -w/--workspace).
func (f flagRef) on(cmd *cobra.Command) bool {
	if f.short {
		return cmd.Flags().ShorthandLookup(f.name) != nil || cmd.InheritedFlags().ShorthandLookup(f.name) != nil
	}
	return cmd.Flags().Lookup(f.name) != nil || cmd.InheritedFlags().Lookup(f.name) != nil
}

// onAny reports whether some subcommand of root has f.
func (f flagRef) onAny(root *cobra.Command) bool {
	for _, c := range root.Commands() {
		if f.on(c) {
			return true
		}
	}
	return false
}
