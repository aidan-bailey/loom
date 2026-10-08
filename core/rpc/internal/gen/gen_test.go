package gen

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// iface wraps one method line (and what precedes it) in a Core interface.
func iface(method string) []byte {
	return []byte("package core\n\ntype Core interface {\n" + method + "\n}\n")
}

func TestGenerate_AcceptsLineCommentDirectives(t *testing.T) {
	for _, method := range []string{
		"\tFoo() string",
		"\tFoo() string // rpc:local",
		"\tFoo(id int) // rpc:cast",
	} {
		out, err := Generate(iface(method))
		require.NoError(t, err, "method %q", method)
		assert.Contains(t, string(out), "func (c *Client) Foo(")
	}
}

// TestGenerate_NoRequestDiscardsItsError: a request whose Go signature has
// no error to return still reports a failure (to the log) instead of
// discarding it.
func TestGenerate_NoRequestDiscardsItsError(t *testing.T) {
	for _, method := range []string{"\tFoo()", "\tFoo() string", "\tFoo() (string, bool)"} {
		out, err := Generate(iface(method))
		require.NoError(t, err, "method %q", method)
		assert.Contains(t, string(out), `c.requestNoErr("Foo"`, "method %q", method)
		assert.NotContains(t, string(out), "_ = c.request(", "method %q", method)
	}
	out, err := Generate(iface("\tFoo() error"))
	require.NoError(t, err)
	assert.Contains(t, string(out), `return c.request("Foo"`, "an error result is returned")
}

// TestGenerate_TagsEveryRequestID: the server's dispatch hands every
// request ID a call carries to tag first, which names the connection in it,
// so a Reply reaches the client that made the request (rpc.tagReq).
func TestGenerate_TagsEveryRequestID(t *testing.T) {
	out, err := Generate(iface("\tFoo(id InstanceID, req ReqID, other ReqID)"))
	require.NoError(t, err)
	assert.Contains(t, string(out), "tag func(*core.ReqID) error")
	assert.Contains(t, string(out), "if err := tag(&p.Req); err != nil {")
	assert.Contains(t, string(out), "if err := tag(&p.Other); err != nil {")
	assert.NotContains(t, string(out), "tag(&p.ID)", "only request IDs")
}

// TestGenerate_RefusesARequestIDTheServerCannotTag: dispatch tags only a
// parameter of type ReqID with the connection's number, so one nested in a
// slice, a map, a pointer or a core struct's field, at any depth, would
// reach the model as the client numbered it. The generator refuses it.
func TestGenerate_RefusesARequestIDTheServerCannotTag(t *testing.T) {
	core := []byte("package core\n\n" +
		"type Spec struct {\n\tTitle string\n\tReq ReqID\n}\n" +
		"type Outer struct {\n\tIn Inner\n}\n" +
		"type Inner struct {\n\tReqs []*ReqID\n}\n" +
		"type Plain struct {\n\tTitle string\n\tSettings config.Settings\n}\n")
	for _, method := range []string{
		"\tFoo(reqs []ReqID)",
		"\tFoo(byName map[string]ReqID)",
		"\tFoo(req *ReqID)",
		"\tFoo(spec Spec)",
		"\tFoo(outer Outer)",
	} {
		_, err := Generate(iface(method), core)
		if assert.Error(t, err, "method %q", method) {
			assert.Contains(t, err.Error(), "request ID", "method %q", method)
		}
	}
	for _, method := range []string{"\tFoo(req ReqID)", "\tFoo(plain Plain, req ReqID)"} {
		_, err := Generate(iface(method), core)
		assert.NoError(t, err, "method %q", method)
	}
}

// TestGenerate_RefusesADirectiveInADocComment: a directive above the method
// is not read, so the method would silently become a request.
func TestGenerate_RefusesADirectiveInADocComment(t *testing.T) {
	for _, doc := range []string{
		"\t// rpc:local",
		"\t// Foo is read from the replica.\n\t// rpc:cast",
		"\t/* rpc:local */",
	} {
		_, err := Generate(iface(doc + "\n\tFoo() string"))
		if assert.Error(t, err, "doc %q", doc) {
			assert.Contains(t, err.Error(), "Foo")
			assert.Contains(t, err.Error(), "line comment")
		}
	}
}

func TestGenerate_RefusesAnUnknownLineComment(t *testing.T) {
	for _, comment := range []string{"// rpc:bogus", "// local", "// a note"} {
		_, err := Generate(iface("\tFoo() string " + comment))
		if assert.Error(t, err, "comment %q", comment) {
			assert.Contains(t, err.Error(), "unknown line comment")
		}
	}
}
