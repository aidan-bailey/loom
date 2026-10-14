package core

// The ModHost methods of Loop: each runs its namesake on the model, on the
// loop's goroutine. No client call follows a mod's to drain what it
// produced (a Reply, Started, Answered, a refusal when a relaunch ends a
// link), so a call that queued an event wakes the client (modCall). They
// are not Core methods: only the daemon's mod server calls them, in
// process, from its HTTP goroutines, and nothing about them crosses the
// wire.

var _ ModHost = (*Loop)(nil)

// ModHello runs Model.ModHello on the loop.
func (l *Loop) ModHello(launch string, h ModHello) error {
	var err error
	l.modCall(func(m *Model) { err = m.ModHello(launch, h) })
	return err
}

// ModInbox runs Model.ModInbox on the loop. After Stop it returns a nil
// channel, which the server reads as the daemon stopping.
func (l *Loop) ModInbox(launch string) ([]ModMessage, <-chan struct{}, error) {
	var (
		msgs []ModMessage
		wake <-chan struct{}
		err  error
	)
	l.modCall(func(m *Model) { msgs, wake, err = m.ModInbox(launch) })
	return msgs, wake, err
}

// ModEvents runs Model.ModEvents on the loop.
func (l *Loop) ModEvents(launch string, events []ModEvent) error {
	var err error
	l.modCall(func(m *Model) { err = m.ModEvents(launch, events) })
	return err
}

// modCall runs f on the loop and wakes the client when the model has events
// queued after it, as the loop does for an inbox item. A poll or a report
// that changed nothing a client sees wakes none: a wake has the server build
// and diff every view.
func (l *Loop) modCall(f func(*Model)) {
	queued := false
	l.do(func(m *Model) {
		f(m)
		queued = len(m.out.Events) > 0
	})
	if queued {
		l.signal()
	}
}
