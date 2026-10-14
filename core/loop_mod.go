package core

// The ModHost methods of Loop, and the daemon's ModsStop: each runs its
// namesake on the model, on the loop's goroutine. No client call follows
// one to drain what it produced (a Reply, Started, Answered, a refusal when
// a relaunch ends a link), so a call that queued an event wakes the client
// (modCall). They are not Core methods: only the daemon calls them (its mod
// server from its HTTP goroutines), in process, and nothing about them
// crosses the wire.

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

// ModsStop runs Model.ModsStop on the loop. The daemon calls it as it stops,
// once its mod server has closed and before it waits for the jobs in flight,
// so the pastes it starts are among them.
func (l *Loop) ModsStop() {
	l.modCall(func(m *Model) { m.ModsStop() })
}

// modCall runs f on the loop and wakes the client when the model has events
// queued after it, as the loop does for an inbox item. A poll or a report
// that changed nothing a client sees wakes none: a wake has the server build
// and diff every view. The wake is raised on the loop's goroutine, inside
// the call: Stop closes Wakes only once that goroutine has exited, so a
// signal from the caller's goroutine after the call could send on a closed
// channel.
func (l *Loop) modCall(f func(*Model)) {
	l.do(func(m *Model) {
		f(m)
		if len(m.out.Events) > 0 {
			l.signal()
		}
	})
}
