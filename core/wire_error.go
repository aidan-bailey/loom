package core

import (
	"errors"

	"github.com/aidan-bailey/loom/session"
)

// Wire error codes: what a client can still tell about an error once it
// has crossed a process boundary as text (see WireError).
const (
	// CodeNotFound: the request named a session no loaded workspace holds
	// (ErrNoSession).
	CodeNotFound = "not_found"
	// CodeRefused: the model refused the request (ErrRefused).
	CodeRefused = "refused"
	// CodeStorage: the workspace's storage refuses writes
	// (session.ErrStorageLoadFailed).
	CodeStorage = "storage"
	// CodeError: any other failure; only its message survives.
	CodeError = "error"
	// CodePanic: the model panicked (LoopPanic); the client re-raises it.
	CodePanic = "panic"
	// CodeProtocol: a frame the other side could not use.
	CodeProtocol = "protocol"
	// CodeMismatch: the two sides speak different protocol versions.
	CodeMismatch = "mismatch"
)

// WireError is an error as it crosses a process boundary: its message,
// and a code that keeps the identities clients test with errors.Is
// (ErrNoSession, ErrRefused, session.ErrStorageLoadFailed). Every other
// error arrives as text only, which is all the TUI does with one.
type WireError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *WireError) Error() string { return e.Message }

// Is keeps the sentinel identities a client checks: a not_found error is
// ErrNoSession (and so ErrRefused, as ErrNoSession is a refusal), a
// refused one ErrRefused, a storage one session.ErrStorageLoadFailed.
func (e *WireError) Is(target error) bool {
	switch e.Code {
	case CodeNotFound:
		return target == ErrNoSession || target == ErrRefused
	case CodeRefused:
		return target == ErrRefused
	case CodeStorage:
		return target == session.ErrStorageLoadFailed
	}
	return false
}

// ToWire encodes err for the wire: nil for nil, a WireError unchanged, an
// error wrapping one (decode params: %w) under that one's code with the
// whole message, anything else its message under the code of the first
// sentinel it matches (CodeError for none).
func ToWire(err error) *WireError {
	if err == nil {
		return nil
	}
	if w, ok := err.(*WireError); ok {
		return w
	}
	var w *WireError
	if errors.As(err, &w) && w != nil {
		return &WireError{Code: w.Code, Message: err.Error()}
	}
	code := CodeError
	switch {
	case errors.Is(err, ErrNoSession):
		code = CodeNotFound
	case errors.Is(err, ErrRefused):
		code = CodeRefused
	case errors.Is(err, session.ErrStorageLoadFailed):
		code = CodeStorage
	}
	return &WireError{Code: code, Message: err.Error()}
}

// FromWire decodes a wire error: nil (a nil interface, never a typed nil
// pointer) for nil.
func FromWire(w *WireError) error {
	if w == nil {
		return nil
	}
	return w
}
