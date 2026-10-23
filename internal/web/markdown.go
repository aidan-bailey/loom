package web

import (
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

// The message tree: a session's last message parsed as markdown here, in Go,
// and sent as plain nodes the app draws as elements, so no HTML string ever
// reaches the page. Raw HTML stays text, an image is its alt text, and a link
// keeps its target only when it is an absolute http or https URL.

// The tree's bounds: the bytes of the message parsed, the nodes kept, and how
// deep they nest. Past any of them the rest is dropped and the tree says so.
const (
	maxMessageBytes = 64 << 10
	maxTreeNodes    = 5000
	maxTreeDepth    = 32
)

// mdNode is one node of the tree: T names its kind (p, h, ul, ol, li, quote,
// hr, code, table, tr, th, td; text, code_inline, em, strong, del, br, a), and
// the other fields are set only for the kinds that carry them. C holds the
// children.
type mdNode struct {
	T     string    `json:"t"`
	V     string    `json:"v,omitempty"`
	Level int       `json:"level,omitempty"`
	Start *int      `json:"start,omitempty"`
	Lang  string    `json:"lang,omitempty"`
	Href  string    `json:"href,omitempty"`
	Align []string  `json:"align,omitempty"`
	C     []*mdNode `json:"c,omitempty"`
}

// messageJSON is GET /api/v1/sessions/{id}/message: the tree of a session's
// last message, the text it was built from, and whether the tree is cut.
type messageJSON struct {
	Text      string    `json:"text"`
	Blocks    []*mdNode `json:"blocks"`
	Truncated bool      `json:"truncated"`
}

// buildTree parses msg's first maxMessageBytes, cut at a rune boundary, as
// GitHub-flavoured markdown (tables, strikethrough, bare links), and returns
// its blocks, never nil, and whether anything was dropped. A parser is made
// per call: goldmark does not promise that one is safe to share.
func buildTree(msg string) ([]*mdNode, bool) {
	src := []byte(msg)
	cut := false
	if len(src) > maxMessageBytes {
		n := maxMessageBytes
		for n > 0 && !utf8.RuneStart(src[n]) {
			n--
		}
		src, cut = src[:n], true
	}
	md := goldmark.New(goldmark.WithExtensions(extension.Table, extension.Strikethrough, extension.Linkify))
	doc := md.Parser().Parse(text.NewReader(src))
	b := &treeBuilder{src: src}
	blocks := b.children(doc, 1)
	if blocks == nil {
		blocks = []*mdNode{}
	}
	return blocks, cut || b.dropped
}

// treeBuilder turns goldmark's AST into the tree, counting what it keeps.
type treeBuilder struct {
	src     []byte
	nodes   int
	dropped bool
}

// room counts one node at depth, and reports false, marking the tree cut,
// once maxTreeNodes are kept or when the node would sit deeper than
// maxTreeDepth.
func (b *treeBuilder) room(depth int) bool {
	if b.nodes >= maxTreeNodes || depth > maxTreeDepth {
		b.dropped = true
		return false
	}
	b.nodes++
	return true
}

// leaf keeps nd at depth, or nothing past the bounds.
func (b *treeBuilder) leaf(depth int, nd *mdNode) []*mdNode {
	if !b.room(depth) {
		return nil
	}
	return []*mdNode{nd}
}

// parent keeps nd at depth with the children kids makes one level down, or
// nothing past the bounds.
func (b *treeBuilder) parent(depth int, nd *mdNode, kids func(depth int) []*mdNode) []*mdNode {
	if !b.room(depth) {
		return nil
	}
	nd.C = kids(depth + 1)
	return []*mdNode{nd}
}

// wrap keeps nd at depth with n's children under it.
func (b *treeBuilder) wrap(n gast.Node, depth int, nd *mdNode) []*mdNode {
	return b.parent(depth, nd, func(d int) []*mdNode { return b.children(n, d) })
}

// children converts n's children, each at depth.
func (b *treeBuilder) children(n gast.Node, depth int) []*mdNode {
	var out []*mdNode
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		out = append(out, b.node(c, depth)...)
	}
	return out
}

// node converts one goldmark node at depth: none past the bounds, several
// where goldmark nests what the tree keeps flat (a tight list's text block, a
// text followed by a line break), and for a node this build does not name,
// its content without its kind.
func (b *treeBuilder) node(n gast.Node, depth int) []*mdNode {
	switch n := n.(type) {
	case *gast.TextBlock:
		return b.children(n, depth)
	case *gast.Text:
		out := b.leaf(depth, &mdNode{T: "text", V: string(n.Value(b.src))})
		if n.SoftLineBreak() || n.HardLineBreak() {
			out = append(out, b.leaf(depth, &mdNode{T: "br"})...)
		}
		return out
	case *gast.String:
		return b.leaf(depth, &mdNode{T: "text", V: string(n.Value)})
	case *gast.RawHTML:
		return b.leaf(depth, &mdNode{T: "text", V: string(n.Segments.Value(b.src))})
	case *gast.CodeSpan:
		return b.leaf(depth, &mdNode{T: "code_inline", V: b.plain(n)})
	case *gast.Image:
		return b.leaf(depth, &mdNode{T: "text", V: b.plain(n)})
	case *gast.AutoLink:
		label := &mdNode{T: "text", V: string(n.Label(b.src))}
		href := safeHref(string(n.URL(b.src)))
		if href == "" {
			return b.leaf(depth, label)
		}
		return b.parent(depth, &mdNode{T: "a", Href: href}, func(d int) []*mdNode { return b.leaf(d, label) })
	case *gast.Link:
		if href := safeHref(string(n.Destination)); href != "" {
			return b.wrap(n, depth, &mdNode{T: "a", Href: href})
		}
		return b.children(n, depth)
	case *gast.Emphasis:
		if n.Level >= 2 {
			return b.wrap(n, depth, &mdNode{T: "strong"})
		}
		return b.wrap(n, depth, &mdNode{T: "em"})
	case *east.Strikethrough:
		return b.wrap(n, depth, &mdNode{T: "del"})
	case *gast.Paragraph:
		return b.wrap(n, depth, &mdNode{T: "p"})
	case *gast.Heading:
		return b.wrap(n, depth, &mdNode{T: "h", Level: n.Level})
	case *gast.List:
		if n.IsOrdered() {
			start := n.Start
			return b.wrap(n, depth, &mdNode{T: "ol", Start: &start})
		}
		return b.wrap(n, depth, &mdNode{T: "ul"})
	case *gast.ListItem:
		return b.wrap(n, depth, &mdNode{T: "li"})
	case *gast.Blockquote:
		return b.wrap(n, depth, &mdNode{T: "quote"})
	case *gast.ThematicBreak:
		return b.leaf(depth, &mdNode{T: "hr"})
	case *gast.FencedCodeBlock:
		return b.leaf(depth, &mdNode{T: "code", Lang: string(n.Language(b.src)), V: b.lines(n.Lines())})
	case *gast.CodeBlock:
		return b.leaf(depth, &mdNode{T: "code", V: b.lines(n.Lines())})
	case *gast.HTMLBlock:
		raw := string(n.Lines().Value(b.src))
		if n.HasClosure() {
			raw += string(n.ClosureLine.Value(b.src))
		}
		source := &mdNode{T: "text", V: strings.TrimSuffix(raw, "\n")}
		return b.parent(depth, &mdNode{T: "p"}, func(d int) []*mdNode { return b.leaf(d, source) })
	case *east.Table:
		align := make([]string, 0, len(n.Alignments))
		for _, a := range n.Alignments {
			align = append(align, alignOf(a))
		}
		return b.wrap(n, depth, &mdNode{T: "table", Align: align})
	case *east.TableHeader, *east.TableRow:
		return b.wrap(n, depth, &mdNode{T: "tr"})
	case *east.TableCell:
		if _, head := n.Parent().(*east.TableHeader); head {
			return b.wrap(n, depth, &mdNode{T: "th"})
		}
		return b.wrap(n, depth, &mdNode{T: "td"})
	}
	return b.children(n, depth)
}

// plain is the text under n without its markup: a code span's code, an
// image's alt text.
func (b *treeBuilder) plain(n gast.Node) string {
	var sb strings.Builder
	_ = gast.Walk(n, func(c gast.Node, entering bool) (gast.WalkStatus, error) {
		if !entering {
			return gast.WalkContinue, nil
		}
		switch c := c.(type) {
		case *gast.Text:
			sb.Write(c.Value(b.src))
			if c.SoftLineBreak() || c.HardLineBreak() {
				sb.WriteByte(' ')
			}
		case *gast.String:
			sb.Write(c.Value)
		}
		return gast.WalkContinue, nil
	})
	return sb.String()
}

// lines is a block's lines as one string, without its last newline.
func (b *treeBuilder) lines(segs *text.Segments) string {
	return strings.TrimSuffix(string(segs.Value(b.src)), "\n")
}

// alignOf is a column's alignment as the tree spells it: left, center, right,
// or "" for none.
func alignOf(a east.Alignment) string {
	switch a {
	case east.AlignLeft, east.AlignCenter, east.AlignRight:
		return a.String()
	}
	return ""
}

// safeHref is dest as the app may link to it: an absolute http or https URL
// with a host, in its parsed form; "" otherwise, so a javascript:, mailto: or
// relative link shows as its text.
func safeHref(dest string) string {
	u, err := url.Parse(dest)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	return u.String()
}
