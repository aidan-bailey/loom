package rpc

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/core"
	"github.com/stretchr/testify/require"
)

// The server writes frames by hand (appendFrame) because encoding/json
// validates and compacts every json.RawMessage it encodes, the event's
// already-encoded data included, once per connection: most of a busy
// daemon's CPU. A peer must not see the difference: the bytes are those
// json.Encoder wrote.
func TestAppendFrame_WritesWhatJSONEncoderWrites(t *testing.T) {
	event, err := encodeEvent(core.Notice{Info: `a <b> & "c" ` + "  é"})
	require.NoError(t, err)
	result, err := json.Marshal(map[string]any{"n": 1, "s": "<x>"})
	require.NoError(t, err)
	frames := map[string]Frame{
		"event":        event,
		"reply":        {ID: 7, Result: result},
		"empty reply":  {ID: 9},
		"error reply":  {ID: 8, Error: &core.WireError{Code: core.CodeProtocol, Message: "x < y & z"}},
		"hello":        {Hello: &Hello{Protocol: Protocol, Build: "b", Version: "1.2.3", Role: RoleWeb}},
		"fatal":        {Fatal: &core.WireError{Code: core.CodeProtocol, Message: "gone"}},
		"bye":          {Bye: "stopping"},
		"request":      {ID: 3, Method: "Kill", Params: json.RawMessage(`{"id":4}`)},
		"zero":         {},
		"large event":  {Event: "ViewsChanged", Data: mustMarshal(t, map[string]string{"diff": strings.Repeat("+line <tag>\n", 2000)})},
		"event and id": {ID: 5, Event: "Started", Data: json.RawMessage(`{"req":5}`)},
	}
	for name, f := range frames {
		t.Run(name, func(t *testing.T) {
			var want bytes.Buffer
			require.NoError(t, json.NewEncoder(&want).Encode(f))
			got, err := appendFrame(nil, f)
			require.NoError(t, err)
			require.Equal(t, want.String(), string(got))
		})
	}
}

// mustMarshal is json.Marshal that fails the test on an error.
func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

// BenchmarkWriteFrame compares encoding a 2 MB event frame with
// json.Encoder, as writeLoop did, against appendFrame.
func BenchmarkWriteFrame(b *testing.B) {
	data, _ := json.Marshal(map[string]string{"diff": strings.Repeat("+a line of a large diff <x>\n", 70000)})
	f := Frame{Event: "ViewsChanged", Data: data}
	b.Run("json.Encoder", func(b *testing.B) {
		enc := json.NewEncoder(&bytes.Buffer{})
		for b.Loop() {
			_ = enc.Encode(f)
		}
	})
	b.Run("appendFrame", func(b *testing.B) {
		var buf []byte
		for b.Loop() {
			buf, _ = appendFrame(buf[:0], f)
		}
	})
}
