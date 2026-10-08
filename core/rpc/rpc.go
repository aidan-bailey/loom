// Package rpc carries core.Core over a connection (daemon stage 2):
// newline-delimited JSON frames, a Server serving a core.Loop, and a
// Client implementing core.Core.
//
// The client keeps a replica of the model's published state: it is sent
// the whole state when it connects (core.Model.Snapshot) and every change
// after (the state events Sync publishes), and answers every query from
// it, so only actions cross the wire. A request's reply follows the
// events the request produced, so a client that reads right after a
// request sees its effect. methods_gen.go, generated from core/iface.go,
// holds each method's wire types, the method table, the server's dispatch
// and the client's methods.
package rpc

//go:generate go run ./internal/gen/cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime/debug"

	"github.com/aidan-bailey/loom/core"
)

// Protocol is the wire's version. Both sides send it in their hello and
// refuse a different one (stage 3 adds the newer-side-wins handshake).
// Adding a method or an optional field is compatible and needs no bump: a
// peer that lacks the method answers CodeUnsupported, an ordinary error, a
// peer ignores a field it does not know, and an event it does not know is
// dropped. Removing or renaming a method, event or field, or changing what
// a field means or a frame's shape, is incompatible and bumps it.
const Protocol = 2

// pingMethod is the request that publishes and replies, and nothing else:
// a client's barrier (every frame the server wrote before the reply has
// been read when the reply arrives).
const pingMethod = "rpc.Ping"

// kind is how a client serves a method (core/iface.go's line comments).
type kind int

const (
	kindRequest kind = iota // a request, answered by a reply after its events
	kindLocal               // answered from the client's replica
	kindCast                // sent one way, with no reply
)

func (k kind) String() string {
	return [...]string{"request", "local", "cast"}[k]
}

// methodInfo describes one method the wire carries (methods).
type methodInfo struct {
	Name   string
	Kind   kind
	Params any
	Result any
}

// mismatchError is what a server answers a hello of another protocol with.
func mismatchError() *core.WireError {
	return &core.WireError{Code: core.CodeMismatch,
		Message: fmt.Sprintf("rpc: protocol mismatch: this server speaks %d", Protocol)}
}

// decodeParams decodes a request's parameters into p; none decode as
// zero values.
func decodeParams(params json.RawMessage, p any) error {
	if len(params) == 0 {
		return nil
	}
	if err := json.Unmarshal(params, p); err != nil {
		return &core.WireError{Code: core.CodeProtocol, Message: fmt.Sprintf("decode params: %v", err)}
	}
	return nil
}

// Frame is one line on the wire. Exactly one of its shapes is set:
//   - a hello: Hello;
//   - a request: Method, Params and a non-zero ID (a cast has no ID);
//   - a reply: ID, Result and Error;
//   - an event: Event (its Go type name) and Data;
//   - a fatal error: Fatal, which every later call re-raises.
type Frame struct {
	Hello  *Hello          `json:"hello,omitempty"`
	ID     uint64          `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *core.WireError `json:"error,omitempty"`
	Event  string          `json:"event,omitempty"`
	Data   json.RawMessage `json:"data,omitempty"`
	Fatal  *core.WireError `json:"fatal,omitempty"`
}

// Hello is each side's first frame.
type Hello struct {
	Protocol int    `json:"protocol"`
	Build    string `json:"build"`
}

// build names the binary: its module version and VCS revision, as Go
// stamps them.
// Build names this binary: its module version, VCS revision and whether
// the tree was modified. A daemon records it in its lock.
func Build() string { return build() }

func build() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	b := info.Main.Version
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			b += " " + s.Value
		case "vcs.modified":
			if s.Value == "true" {
				b += "+dirty"
			}
		}
	}
	return b
}

// eventTypes maps each event's wire name (its Go type name) to its type.
var eventTypes = func() map[string]reflect.Type {
	m := map[string]reflect.Type{}
	for _, ev := range core.EventTypes() {
		t := reflect.TypeOf(ev)
		m[t.Name()] = t
	}
	return m
}()

// encodeEvent is ev's frame.
func encodeEvent(ev core.Event) (Frame, error) {
	data, err := json.Marshal(ev)
	if err != nil {
		return Frame{}, err
	}
	return Frame{Event: reflect.TypeOf(ev).Name(), Data: data}, nil
}

// errUnknownEvent is decodeEvent's error for an event name this build does
// not know, which a newer peer may send.
var errUnknownEvent = errors.New("unknown event")

// decodeEvent is the event an event frame carries.
func decodeEvent(f Frame) (core.Event, error) {
	t, ok := eventTypes[f.Event]
	if !ok {
		return nil, fmt.Errorf("%w %q", errUnknownEvent, f.Event)
	}
	v := reflect.New(t)
	if err := json.Unmarshal(f.Data, v.Interface()); err != nil {
		return nil, fmt.Errorf("decode %s: %w", f.Event, err)
	}
	return v.Elem().Interface().(core.Event), nil
}
