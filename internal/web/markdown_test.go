package web

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// treeJSON is the tree of msg as the app reads it, and whether it was cut.
func treeJSON(t *testing.T, msg string) (string, bool) {
	t.Helper()
	blocks, truncated := buildTree(msg)
	b, err := json.Marshal(blocks)
	require.NoError(t, err)
	return string(b), truncated
}

func TestBuildTree_EachNodeType(t *testing.T) {
	for _, tc := range []struct{ name, md, want string }{
		{"a paragraph keeps its line break", "one\ntwo", `[{"t":"p","c":[{"t":"text","v":"one"},{"t":"br"},{"t":"text","v":"two"}]}]`},
		{"headings", "# One\n### Three", `[{"t":"h","level":1,"c":[{"t":"text","v":"One"}]},{"t":"h","level":3,"c":[{"t":"text","v":"Three"}]}]`},
		{"emphasis, strong, strikethrough and code", "*a* **b** ~~c~~ `d`", `[{"t":"p","c":[{"t":"em","c":[{"t":"text","v":"a"}]},{"t":"text","v":" "},{"t":"strong","c":[{"t":"text","v":"b"}]},{"t":"text","v":" "},{"t":"del","c":[{"t":"text","v":"c"}]},{"t":"text","v":" "},{"t":"code_inline","v":"d"}]}]`},
		{"a tight list", "- a\n- b", `[{"t":"ul","c":[{"t":"li","c":[{"t":"text","v":"a"}]},{"t":"li","c":[{"t":"text","v":"b"}]}]}]`},
		{"an ordered list keeps its start", "3. c\n4. d", `[{"t":"ol","start":3,"c":[{"t":"li","c":[{"t":"text","v":"c"}]},{"t":"li","c":[{"t":"text","v":"d"}]}]}]`},
		{"a quote", "> said", `[{"t":"quote","c":[{"t":"p","c":[{"t":"text","v":"said"}]}]}]`},
		{"a rule", "a\n\n---\n\nb", `[{"t":"p","c":[{"t":"text","v":"a"}]},{"t":"hr"},{"t":"p","c":[{"t":"text","v":"b"}]}]`},
		{"a fenced block", "```go\nx := 1\n```", `[{"t":"code","lang":"go","v":"x := 1"}]`},
		{"an indented block", "    x := 1", `[{"t":"code","v":"x := 1"}]`},
		{"a link", "[docs](https://x.example/a)", `[{"t":"p","c":[{"t":"a","href":"https://x.example/a","c":[{"t":"text","v":"docs"}]}]}]`},
		{"a bare link", "see https://x.example/a", `[{"t":"p","c":[{"t":"text","v":"see "},{"t":"a","href":"https://x.example/a","c":[{"t":"text","v":"https://x.example/a"}]}]}]`},
		{"a table", "| a | b |\n|:--|--:|\n| 1 | 2 |", `[{"t":"table","align":["left","right"],"c":[{"t":"tr","c":[{"t":"th","c":[{"t":"text","v":"a"}]},{"t":"th","c":[{"t":"text","v":"b"}]}]},{"t":"tr","c":[{"t":"td","c":[{"t":"text","v":"1"}]},{"t":"td","c":[{"t":"text","v":"2"}]}]}]}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, truncated := treeJSON(t, tc.md)
			assert.JSONEq(t, tc.want, got)
			assert.False(t, truncated)
		})
	}
}

func TestBuildTree_KeepsNoMarkupAndFollowsNoUnsafeLink(t *testing.T) {
	for _, tc := range []struct{ name, md, want string }{
		{"inline HTML is its source", "Run <b>this</b> now", `[{"t":"p","c":[{"t":"text","v":"Run "},{"t":"text","v":"<b>"},{"t":"text","v":"this"},{"t":"text","v":"</b>"},{"t":"text","v":" now"}]}]`},
		{"an HTML block is its source", "<div>\nhi\n</div>", `[{"t":"p","c":[{"t":"text","v":"<div>\nhi\n</div>"}]}]`},
		{"an image is its alt text", "![a cat](https://x.example/cat.png)", `[{"t":"p","c":[{"t":"text","v":"a cat"}]}]`},
		{"a javascript link is its text", "[go](javascript:void(0))", `[{"t":"p","c":[{"t":"text","v":"go"}]}]`},
		{"a mailto link is its text", "[mail](mailto:me@x.example)", `[{"t":"p","c":[{"t":"text","v":"mail"}]}]`},
		{"a relative link is its text", "[file](docs/a.md)", `[{"t":"p","c":[{"t":"text","v":"file"}]}]`},
		{"a bare email is its text", "me@x.example", `[{"t":"p","c":[{"t":"text","v":"me@x.example"}]}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := treeJSON(t, tc.md)
			assert.JSONEq(t, tc.want, got)
		})
	}
}

func TestBuildTree_Limits(t *testing.T) {
	blocks, truncated := buildTree("")
	assert.NotNil(t, blocks, "no message: an empty list, never null")
	assert.Empty(t, blocks)
	assert.False(t, truncated)

	_, truncated = buildTree(strings.Repeat("a", maxMessageBytes))
	assert.False(t, truncated, "64 KiB is parsed whole")

	_, truncated = buildTree(strings.Repeat("a", maxMessageBytes) + "tail")
	assert.True(t, truncated, "past 64 KiB the rest is dropped")

	// "é" is two bytes, so a cut at 64 KiB would split one.
	blocks, truncated = buildTree("a" + strings.Repeat("é", maxMessageBytes/2))
	require.True(t, truncated)
	require.NotEmpty(t, blocks)
	last := blocks[0].C[len(blocks[0].C)-1].V
	assert.True(t, utf8.ValidString(last), "the cut falls between runes")
	assert.True(t, strings.HasSuffix(last, "é"))

	_, truncated = buildTree(strings.Repeat("- a\n", maxTreeNodes))
	assert.True(t, truncated, "past 5,000 nodes the rest is dropped")

	_, truncated = buildTree(strings.Repeat(">", maxTreeDepth+2) + " deep")
	assert.True(t, truncated, "past 32 levels the rest is dropped")
}
