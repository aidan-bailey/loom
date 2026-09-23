package session

import "errors"

// Notice is the error an Instance operation returns when it did all it
// was asked but found something the user must hear about: a pending stash
// it forgot because it was no longer in `git stash list`, or a stash entry
// it could not drop. The operation's effects stand. A caller that gets
// exactly a Notice back (OnlyNotice) treats the operation as succeeded and
// shows the message — the app through its error bar, never just loom.log,
// since the message is often the only place a lost stash's SHA appears. A
// failure that also has notices to report joins them into its own error,
// where NoticeIn still finds them.
type Notice struct{ err error }

// Error implements error.
func (n *Notice) Error() string { return n.err.Error() }

// Unwrap exposes the notices for errors.Is/errors.As.
func (n *Notice) Unwrap() error { return n.err }

// NewNotice wraps the non-nil errs in a Notice, or returns nil if there
// are none.
func NewNotice(errs ...error) error {
	if err := errors.Join(errs...); err != nil {
		return &Notice{err: err}
	}
	return nil
}

// OnlyNotice returns err as a Notice when it is one and nothing more: the
// operation that returned it succeeded. A Notice wrapped into another
// error is part of a failure and does not count.
func OnlyNotice(err error) (*Notice, bool) {
	n, ok := err.(*Notice)
	return n, ok
}

// NoticeIn returns the Notice anywhere in err's chain, for a caller that
// handles a failure itself but must still show what else it carried.
func NoticeIn(err error) (*Notice, bool) {
	var n *Notice
	ok := errors.As(err, &n)
	return n, ok
}
