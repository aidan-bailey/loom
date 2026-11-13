package watch_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session/git"
	"github.com/aidan-bailey/loom/watch"
)

const specTemplate = "heal {spec} {issue} {row}"

func mustTemplate(t *testing.T, tmpl string) watch.TitleTemplate {
	t.Helper()
	tt, err := watch.ParseTitleTemplate(tmpl)
	require.NoError(t, err)
	return tt
}

func TestParseTitleTemplate_Valid(t *testing.T) {
	tests := []struct {
		tmpl string
		vars []string
	}{
		{"heal {issue}", nil},
		{specTemplate, []string{"spec", "row"}},
		{"heal {issue} tail", nil},
		{"heal {row_1}-{issue}", []string{"row_1"}},
		{"3rd {issue}", nil},
	}
	for _, tc := range tests {
		t.Run(tc.tmpl, func(t *testing.T) {
			tt := mustTemplate(t, tc.tmpl)
			assert.Equal(t, tc.vars, tt.Vars())
		})
	}
}

func TestParseTitleTemplate_Invalid(t *testing.T) {
	tests := []struct {
		name string
		tmpl string
		want string
	}{
		{"empty", "", "is empty"},
		{"starts with a variable", "{issue} heal", "literal word"},
		{"starts with a space", " heal {issue}", "literal word"},
		{"starts with a symbol", "#heal {issue}", "literal word"},
		{"no issue", "heal {spec}", "must contain {issue}"},
		{"issue twice", "heal {issue} {issue}", "more than once"},
		{"variable twice", "heal {spec} {spec} {issue}", "more than once"},
		{"two variables side by side", "heal {spec}{issue}", "literal between"},
		{"capitalised name", "heal {Spec} {issue}", "variable name"},
		{"empty braces", "heal {} {issue}", "variable name"},
		{"unclosed brace", "heal {issue", "unclosed"},
		{"stray closing brace", "heal } {issue}", "stray"},
		{"issue then a separator the sanitizer removes", "heal {issue}#{spec}", "empty once branch-sanitized"},
		{"separator the sanitizer removes before the issue", "heal {spec}#{issue}", "empty once branch-sanitized"},
		{"colon before the issue", "heal {spec}:{issue}", "empty once branch-sanitized"},
		{"digit before the issue", "heal5{issue}", "end in a digit"},
		{"digit after the issue", "heal {issue}5 {spec}", "start with a digit"},
		{"can never fit", strings.Repeat("a", 40) + " {issue}", "cells"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := watch.ParseTitleTemplate(tc.tmpl)
			require.Error(t, err)
			assert.ErrorContains(t, err, tc.want)
		})
	}
}

func TestTitleTemplate_Render(t *testing.T) {
	tt := mustTemplate(t, specTemplate)

	got, err := tt.Render(96, map[string]string{"spec": "Schema.Drift", "row": "R_1", "unused": "ignored"})
	require.NoError(t, err)
	assert.Equal(t, "heal Schema.Drift 96 R_1", got)

	bad := []struct {
		name  string
		issue int
		vars  map[string]string
		want  string
	}{
		{"missing variable", 96, map[string]string{"spec": "a"}, "{row}"},
		{"empty value", 96, map[string]string{"spec": "", "row": "r"}, "{spec}"},
		{"space in value", 96, map[string]string{"spec": "a b", "row": "r"}, "{spec}"},
		{"slash in value", 96, map[string]string{"spec": "a/b", "row": "r"}, "{spec}"},
		{"only dashes", 96, map[string]string{"spec": "--", "row": "r"}, "{spec}"},
		{"only dots", 96, map[string]string{"spec": ".", "row": "r"}, "{spec}"},
		{"newline in value", 96, map[string]string{"spec": "a\n", "row": "r"}, "{spec}"},
		{"issue zero", 0, map[string]string{"spec": "a", "row": "r"}, "issue 0"},
		{"negative issue", -3, map[string]string{"spec": "a", "row": "r"}, "issue -3"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tt.Render(tc.issue, tc.vars)
			require.Error(t, err)
			assert.ErrorContains(t, err, tc.want)
		})
	}
}

// TestMaxTitleWidth_MatchesCore pins watch's private maxTitleWidth to
// core.MaxTitleWidth: a title of exactly that many cells renders, one more
// does not.
func TestMaxTitleWidth_MatchesCore(t *testing.T) {
	tt := mustTemplate(t, "t {issue} {v}")
	fill := core.MaxTitleWidth - len("t 1 ")

	got, err := tt.Render(1, map[string]string{"v": strings.Repeat("a", fill)})
	require.NoError(t, err)
	assert.Equal(t, core.MaxTitleWidth, len(got))

	_, err = tt.Render(1, map[string]string{"v": strings.Repeat("a", fill+1)})
	require.Error(t, err)
	assert.ErrorContains(t, err, "cells")
}

func TestTitleTemplate_Render_CountsCellsNotBytes(t *testing.T) {
	tt := mustTemplate(t, "heal "+strings.Repeat("修", 12)+" {issue}")

	got, err := tt.Render(11, nil)
	require.NoError(t, err)
	assert.Equal(t, 44, len(got), "32 cells wide, 44 bytes long")

	_, err = tt.Render(111, nil)
	require.Error(t, err)
	assert.ErrorContains(t, err, "cells")
}

func TestTitleTemplate_Parse(t *testing.T) {
	tt := mustTemplate(t, specTemplate)

	issue, vars, ok := tt.Parse("heal drift.v2 96 r-1")
	require.True(t, ok)
	assert.Equal(t, 96, issue)
	assert.Equal(t, map[string]string{"spec": "drift.v2", "row": "r-1"}, vars)

	for _, title := range []string{
		"",
		"fix drift 96 r1",
		" heal drift 96 r1",
		"heal drift 007 r1",
		"heal drift 0 r1",
		"heal drift 96 r1 extra",
		"heal drift 96",
		"heal drift 99999999999999999999 r1",
		"heal drift 96 r1\n",
	} {
		t.Run(title, func(t *testing.T) {
			_, _, ok := tt.Parse(title)
			assert.False(t, ok)
		})
	}
}

func TestTitleTemplate_ZeroValue(t *testing.T) {
	var tt watch.TitleTemplate

	_, _, ok := tt.Parse("heal 1")
	assert.False(t, ok)
	_, err := tt.Render(1, nil)
	assert.Error(t, err)
	assert.False(t, tt.BranchPattern("u/", 1).MatchString("u/heal-1"))
	assert.Empty(t, tt.Vars())
}

// TestTitleTemplate_RoundTrip renders titles, parses them back, and checks
// the branch the real sanitizer makes from prefix+title against the pattern.
func TestTitleTemplate_RoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		tmpl   string
		prefix string
		issue  int
		vars   map[string]string
	}{
		{"spec's template", specTemplate, "aidanb/", 96, map[string]string{"spec": "Schema.Drift", "row": "R_1"}},
		{"no variables, no prefix", "heal {issue}", "", 7, nil},
		{"a dot in the literal", "fix.v2 {issue}", "u/", 7, nil},
		{"prefix the sanitizer rewrites", "heal {issue} {spec}", `DOMAIN\user/`, 5, map[string]string{"spec": "a.b"}},
		{"variable first, then the issue", "heal {spec}-{issue}", "u/", 12, map[string]string{"spec": "x"}},
		{"issue last", "heal {spec} {issue}", "u/", 3, map[string]string{"spec": "A-B"}},
		{"issue then a variable", "heal {issue}-{spec}", "u/", 5, map[string]string{"spec": "x"}},
		{"separator with a character the sanitizer removes", "heal {spec}!-{issue}", "u/", 5, map[string]string{"spec": "x"}},
		{"separator with a character the sanitizer removes, issue first", "heal {issue}!.{spec}", "u/", 5, map[string]string{"spec": "x"}},
		{"underscore separator", "heal {spec}_{issue}", "u/", 5, map[string]string{"spec": "x1"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tt := mustTemplate(t, tc.tmpl)
			title, err := tt.Render(tc.issue, tc.vars)
			require.NoError(t, err)

			issue, vars, ok := tt.Parse(title)
			require.True(t, ok, title)
			assert.Equal(t, tc.issue, issue)
			want := tc.vars
			if want == nil {
				want = map[string]string{}
			}
			assert.Equal(t, want, vars)

			branch := git.SanitizeBranchName(tc.prefix + title)
			assert.True(t, tt.BranchPattern(tc.prefix, tc.issue).MatchString(branch), "pattern for %q misses %q", tc.prefix, branch)
			pattern := tt.BranchPattern(tc.prefix, tc.issue)
			// No other issue's branch (same variables) may match this issue's
			// pattern: not the next number, one with a digit appended, or one
			// with a digit prepended.
			for _, other := range []int{tc.issue + 1, tc.issue*10 + 1, 10*tc.issue + 9, 1_000_000 + tc.issue, 1*pow10(len(strconv.Itoa(tc.issue))) + tc.issue, 9*pow10(len(strconv.Itoa(tc.issue))) + tc.issue} {
				otherTitle, err := tt.Render(other, tc.vars)
				require.NoError(t, err)
				otherBranch := git.SanitizeBranchName(tc.prefix + otherTitle)
				assert.False(t, pattern.MatchString(otherBranch), "issue %d's pattern takes issue %d's branch %q", tc.issue, other, otherBranch)
			}
			assert.False(t, tt.BranchPattern(tc.prefix+"x/", tc.issue).MatchString(branch), "another prefix's pattern takes %q", branch)
		})
	}
}

func pow10(n int) int {
	p := 1
	for ; n > 0; n-- {
		p *= 10
	}
	return p
}

func TestBranchPattern_IssueNumberIsWholeAndLiteralsAreQuoted(t *testing.T) {
	tt := mustTemplate(t, "heal {issue}")
	re := tt.BranchPattern("u/", 96)
	assert.True(t, re.MatchString("u/heal-96"))
	assert.False(t, re.MatchString("u/heal-960"), "96 is not a prefix of 960")
	assert.False(t, re.MatchString("u/heal-196"), "96 is not a suffix of 196")
	assert.False(t, re.MatchString("heal-96"), "the prefix is part of the pattern")

	dotted := mustTemplate(t, "fix.v2 {issue}").BranchPattern("u/", 7)
	assert.True(t, dotted.MatchString("u/fix.v2-7"))
	assert.False(t, dotted.MatchString("u/fixxv2-7"), "the dot in a literal is a dot, not any character")
}

func TestBranchPattern_NoFreeMarkerLetterMatchesNothing(t *testing.T) {
	tt := mustTemplate(t, "heal {issue}")
	prefix := "abcdefghijklmnopqrstuvwxyz/"
	re := tt.BranchPattern(prefix, 1)
	assert.False(t, re.MatchString(git.SanitizeBranchName(prefix+"heal 1")))
}
