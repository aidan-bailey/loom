package core

// The WebHost methods of Loop: each runs its namesake on the model, on the
// loop's goroutine. They are not Core methods: only the daemon's web layer
// calls them, in process, and nothing about them crosses the wire. A call
// that changed what a client sees (the Web view) or queued an event wakes the
// client (webCall), so the next Sync publishes it.

var _ WebHost = (*Loop)(nil)

// RedeemPairing runs Model.RedeemPairing on the loop. After Stop it answers
// ErrWebOff: the loop runs nothing, and a nil error would read as a device.
func (l *Loop) RedeemPairing(code string) (name, token string, err error) {
	err = ErrWebOff
	l.webCall(func(m *Model) bool {
		name, token, err = m.RedeemPairing(code)
		return err == nil
	})
	return name, token, err
}

// DeviceByToken runs Model.DeviceByToken on the loop. After Stop it answers
// ("", false), so a caller treats a stopped loop like no device; the daemon
// closes the web layer before the loop stops.
func (l *Loop) DeviceByToken(hash [32]byte) (name string, ok bool) {
	l.do(func(m *Model) { name, ok = m.DeviceByToken(hash) })
	return name, ok
}

// TouchDevice runs Model.TouchDevice on the loop; it wakes the client only
// when it wrote, since it runs on every authenticated request.
func (l *Loop) TouchDevice(name string) {
	l.webCall(func(m *Model) bool { return m.TouchDevice(name) })
}

// SetNotify runs Model.SetNotify on the loop. After Stop it answers ErrWebOff.
func (l *Loop) SetNotify(name string, c NotifyChange) (dev WebDevice, err error) {
	err = ErrWebOff
	l.webCall(func(m *Model) bool {
		dev, err = m.SetNotify(name, c)
		return err == nil
	})
	return dev, err
}

// SetPushSubscription runs Model.SetPushSubscription on the loop. After Stop
// it answers ErrWebOff.
func (l *Loop) SetPushSubscription(name string, sub PushSubscription) (dev WebDevice, err error) {
	err = ErrWebOff
	l.webCall(func(m *Model) bool {
		dev, err = m.SetPushSubscription(name, sub)
		return err == nil
	})
	return dev, err
}

// PushTargets runs Model.PushTargets on the loop.
func (l *Loop) PushTargets() []PushTarget { return get(l, (*Model).PushTargets) }

// PushResult runs Model.PushResult on the loop.
func (l *Loop) PushResult(device, endpoint string, state PushState) {
	l.webCall(func(m *Model) bool { return m.PushResult(device, endpoint, state) })
}

// VAPIDKey runs Model.VAPIDKey on the loop. After Stop it answers ErrWebOff.
func (l *Loop) VAPIDKey() (key VAPIDKey, err error) {
	err = ErrWebOff
	l.do(func(m *Model) { key, err = m.VAPIDKey() })
	return key, err
}

// WebServing runs Model.WebServing on the loop.
func (l *Loop) WebServing(addr string, err error) {
	l.webCall(func(m *Model) bool { return m.WebServing(addr, err) })
}

// webCall runs f on the loop and wakes the client when f says it changed the
// model's Web view, or when the model has events queued after it (a Notice).
// A wake has the server build and diff every view, so a call that changed
// nothing raises none. The wake is raised on the loop's goroutine, inside the
// call: Stop closes Wakes only once that goroutine has exited, so a signal
// from the caller's goroutine after the call could send on a closed channel.
func (l *Loop) webCall(f func(*Model) (changed bool)) {
	l.do(func(m *Model) {
		if f(m) || len(m.out.Events) > 0 {
			l.signal()
		}
	})
}
