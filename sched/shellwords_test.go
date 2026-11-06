package sched

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSplitScript(t *testing.T) {
	cases := []struct {
		script string
		want   [][]string
	}{
		{`a 'b c' "d $(e "f") g" h\ i`, [][]string{{"a", "b c", `d $(e "f") g`, "h i"}}},
		{"a && b || c; d | e & f\ng |& h", [][]string{{"a"}, {"b"}, {"c"}, {"d"}, {"e"}, {"f"}, {"g"}, {"h"}}},
		{"a 2>&1 >out <in &>all >>log b", [][]string{{"a", "b"}}},
		{"a 2 >x", [][]string{{"a", "2"}}},
		{"a # a comment && b\nc", [][]string{{"a"}, {"c"}}},
		{"a `b c` d", [][]string{{"a", "`b c`", "d"}}},
		{"X=${HOME}/y a", [][]string{{"X=${HOME}/y", "a"}}},
		{"a \\\n b", [][]string{{"a", "b"}}},
		{"a ''", [][]string{{"a", ""}}},
		{"a\r\n", [][]string{{"a"}}},
		{"", nil},
	}
	for _, c := range cases {
		got, err := splitScript(c.script)
		require.NoError(t, err, "%q", c.script)
		assert.Equal(t, c.want, got, "%q", c.script)
	}
	for _, script := range []string{
		`a 'b`, `a "b`, `a $(b`, "a `b", "a ${b",
		"(a)", "{ a; }", "a <<EOF\nx\nEOF", "a <<<x", "a >",
		"if a; then b; fi", "for x in y; do z; done", "FOO=1 while x; do y; done", "case x in y) z;; esac",
	} {
		_, err := splitScript(script)
		assert.ErrorIs(t, err, errScript, "%q", script)
	}
}
