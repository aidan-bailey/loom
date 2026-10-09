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

	"github.com/aidan-bailey/loom/core"
)

// Protocol is the wire's version. Both sides send it in their hello and
// refuse a different one; the hello's build tells which side is newer.
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

// mismatchError is what a server answers a hello of another protocol with,
// after its own hello.
func mismatchError() *core.WireError {
	return &core.WireError{Code: core.CodeMismatch,
		Message: fmt.Sprintf("rpc: protocol mismatch: this server speaks %d", Protocol)}
}

// MismatchError is Dial's error when the server speaks another protocol.
// Peer is the server's hello, nil when it sent none (a server from before
// servers said hello first), so a client can still tell which build is
// newer and replace an older server.
type MismatchError struct {
	Peer *Hello
}

func (e *MismatchError) Error() string {
	if e.Peer == nil {
		return fmt.Sprintf("rpc: protocol mismatch: the server does not speak %d", Protocol)
	}
	return fmt.Sprintf("rpc: protocol mismatch: the server speaks %d, this client %d", e.Peer.Protocol, Protocol)
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

// Hello is each side's first frame: its protocol and its build, which a
// client compares with the server's (CompareBuilds: the newer side wins).
// A server sends its own even to a peer of another protocol, ahead of the
// mismatch error, so every client learns the server's build. The fields
// after Build are optional: a peer that lacks them leaves them empty.
type Hello struct {
	Protocol int    `json:"protocol"`
	Build    string `json:"build"`
	// Version is the release (main's version, SetVersion).
	Version string `json:"version,omitempty"`
	// Time is when the VCS revision was committed (RFC 3339): vcs.time,
	// else the commit time the build stamped (commitUnix, as Nix does).
	Time string `json:"time,omitempty"`
	// Modified says the tree was modified when built (vcs.modified).
	Modified bool `json:"modified,omitempty"`
	// Exe is the SHA-256 of the running executable, in hex: two builds of
	// one commit with different edits agree on everything else.
	Exe string `json:"exe,omitempty"`
	// ExeTime is when the running executable was last modified (UTC, RFC
	// 3339), which breaks a tie nothing else decides when both sides name
	// their commit (Time): a rebuild at one commit is newer. One at or
	// before the epoch's first second (a Nix store's) tells nothing.
	ExeTime string `json:"exe_time,omitempty"`
	// Tmux is the tmux server the server's sessions run on (its socket's
	// path), which its clients use too (Server.SetTmux). Servers only.
	Tmux string `json:"tmux,omitempty"`
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
