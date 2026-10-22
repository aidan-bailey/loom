package web

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/log"
)

const (
	// replyWait is how long a handler waits for its request's Reply before it
	// answers 202 and leaves the outcome to the device's stream.
	replyWait = 30 * time.Second
	// reqPoll is how often a handler that waits looks for a refusal the
	// client made itself, which no Reply follows.
	reqPoll = 100 * time.Millisecond
	// reqKeep is how long a request stays tracked, to catch the notices that
	// follow its Reply (a start's).
	reqKeep = 15 * time.Minute
	// maxReqs bounds the tracked requests. Answered ones go oldest first; a
	// request a handler still waits for never does, and the handlers bound
	// those.
	maxReqs = 256
)

// reqs tracks the web client's requests. It hands out their IDs (next),
// keeps the Reply and the notices the drain routes back for each (apply, as
// a sink) and lets the handler that made one wait for its answer (wait). A
// notice tied to no request is the stream's, which Story 7's sink takes.
//
// It is the only caller of rpc.Client.TakeRefused in this package: that list
// is drained by the caller, so a second caller would take the other's.
type reqs struct {
	l *Layer

	// timeout, poll, refused and lost are fields for tests to replace:
	// newReqs sets them from the layer's client.
	timeout time.Duration
	poll    time.Duration
	refused func() []core.ReqID
	lost    func() error

	mu      sync.Mutex
	last    core.ReqID
	entries map[core.ReqID]*reqEntry
	late    func(device string, n noticeJSON)
}

// reqEntry is one request's record.
type reqEntry struct {
	at      time.Time
	device  string
	done    chan struct{}
	seen    map[string]bool
	notices []noticeJSON
	reply   core.Reply
	replied bool
	// detached is set when the handler gave up waiting (the wait ran out, or
	// the phone went away) and answered when it took the Reply: from then on
	// the request's notices go to the device's stream.
	detached bool
	answered bool
}

// lateNotice is a notice for a device's stream, or for every stream when
// device is "".
type lateNotice struct {
	device string
	n      noticeJSON
}

func newReqs(l *Layer) *reqs {
	r := &reqs{
		l:       l,
		timeout: replyWait,
		poll:    reqPoll,
		entries: map[core.ReqID]*reqEntry{},
	}
	r.late = r.toHub
	if l.client != nil {
		r.refused = l.client.TakeRefused
		r.lost = l.client.Err
	}
	return r
}

// toHub puts n on device's streams, or on every stream for "".
func (r *reqs) toHub(device string, n noticeJSON) {
	if r.l.hub == nil {
		return
	}
	if device == "" {
		r.l.hub.publish("notice", n)
		return
	}
	r.l.hub.sendTo(device, "notice", n)
}

// setLate replaces where late notices go: a test seam.
func (r *reqs) setLate(f func(device string, n noticeJSON)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.late = f
}

// stream sends n where late notices go. It takes no lock but its own, for
// the instant it reads the function.
func (r *reqs) stream(device string, n noticeJSON) {
	r.mu.Lock()
	f := r.late
	r.mu.Unlock()
	f(device, n)
}

// next makes the ID of a new request and tracks it, so a Reply and notices
// that arrive before wait is called are kept.
func (r *reqs) next() core.ReqID {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.forget()
	r.last++
	r.entries[r.last] = &reqEntry{
		at:   r.l.now(),
		done: make(chan struct{}),
		seen: map[string]bool{},
	}
	return r.last
}

// forget drops the requests kept past reqKeep, then the oldest answered ones
// while there are too many. r.mu is held.
func (r *reqs) forget() {
	now := r.l.now()
	for req, e := range r.entries {
		if now.Sub(e.at) > reqKeep {
			delete(r.entries, req)
		}
	}
	for len(r.entries) >= maxReqs {
		var oldest core.ReqID
		for req, e := range r.entries {
			if e.settled() && (oldest == 0 || req < oldest) {
				oldest = req
			}
		}
		if oldest == 0 {
			return
		}
		delete(r.entries, oldest)
	}
}

// settled reports whether no handler waits for e any more.
func (e *reqEntry) settled() bool { return e.answered || e.detached }

// apply is the sink: it files the Replies and the notices that name the web
// client's requests. The notices it files for a request nobody waits for
// any more are sent on, after it has let go of its lock.
func (r *reqs) apply(evs []core.Event) {
	var out []lateNotice
	r.mu.Lock()
	for _, ev := range evs {
		switch e := ev.(type) {
		case core.Reply:
			out = r.fileReply(out, e)
		case core.Notice:
			if e.Req != 0 {
				out = r.fileNotice(out, e.Req, noticeOf(e, r.l.now()))
			}
		case core.Started:
			if text := e.PromptNotSentNotice(); e.Req != 0 && text != "" {
				out = r.fileNotice(out, e.Req, noticeOf(core.Notice{Info: text}, r.l.now()))
			}
		}
	}
	r.mu.Unlock()
	for _, n := range out {
		r.stream(n.device, n.n)
	}
}

// fileNotice puts n with request req: in its answer while a handler waits
// for it, on its device's stream after, and on every stream for a request
// no longer tracked. r.mu is held.
func (r *reqs) fileNotice(out []lateNotice, req core.ReqID, n noticeJSON) []lateNotice {
	e := r.entries[req]
	if e == nil {
		log.For("web").Warn("web.notice_unclaimed", "req", uint64(req), "text", n.Text)
		return append(out, lateNotice{n: n})
	}
	return e.file(out, n)
}

// file puts n with e, once per text: the model raises a success notice both
// as a Notice and in the Reply.
func (e *reqEntry) file(out []lateNotice, n noticeJSON) []lateNotice {
	if e.seen[n.Text] {
		return out
	}
	e.seen[n.Text] = true
	if e.detached || e.answered {
		return append(out, lateNotice{device: e.device, n: n})
	}
	e.notices = append(e.notices, n)
	return out
}

// fileReply keeps rep for the handler waiting for it. A Reply that comes
// after the handler gave up is the request's outcome, which goes to the
// device's stream when it is a failure. r.mu is held.
func (r *reqs) fileReply(out []lateNotice, rep core.Reply) []lateNotice {
	e := r.entries[rep.Req]
	if e == nil || e.replied {
		return out
	}
	e.reply, e.replied = rep, true
	if rep.Notice != nil {
		out = e.file(out, noticeOf(core.Notice{Err: rep.Notice}, r.l.now()))
	}
	if e.detached && rep.Err != nil {
		out = e.file(out, noticeOf(core.Notice{Err: rep.Err}, r.l.now()))
	}
	close(e.done)
	return out
}

// fail ends request req with err, unless its Reply came first. r.mu is held.
func (r *reqs) fail(req core.ReqID, err error) {
	e := r.entries[req]
	if e == nil || e.replied {
		return
	}
	e.reply, e.replied = core.Reply{Req: req, Err: err}, true
	close(e.done)
}

// sweep fails the requests the client refused itself (the daemon is
// stopping), which no Reply will answer, and every one still waiting once
// the client has lost the model.
func (r *reqs) sweep() {
	var gone []core.ReqID
	if r.refused != nil {
		gone = r.refused()
	}
	var lost error
	if r.lost != nil {
		lost = r.lost()
	}
	if len(gone) == 0 && lost == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, req := range gone {
		r.fail(req, core.ErrUnavailable)
	}
	if lost != nil {
		err := fmt.Errorf("%w: %v", core.ErrUnavailable, lost)
		for req := range r.entries {
			r.fail(req, err)
		}
	}
}

// wait returns request req's Reply and the notices that came before it, with
// true. It returns false when replyWait passes, or ctx ends (the phone went
// away), with no Reply: from then on the request's notices and its outcome
// go to device's stream. A request the client refused as unavailable is
// answered with that error.
func (r *reqs) wait(ctx context.Context, req core.ReqID, device string) (core.Reply, []noticeJSON, bool) {
	r.mu.Lock()
	e := r.entries[req]
	if e != nil {
		e.device = device
	}
	r.mu.Unlock()
	if e == nil {
		return core.Reply{Req: req, Err: fmt.Errorf("request %d is not tracked", req)}, nil, true
	}
	timer := time.NewTimer(r.timeout)
	defer timer.Stop()
	tick := time.NewTicker(r.poll)
	defer tick.Stop()
	for {
		r.sweep()
		select {
		case <-e.done:
			return r.take(e)
		case <-timer.C:
			return r.giveUp(e)
		case <-ctx.Done():
			return r.giveUp(e)
		case <-tick.C:
		}
	}
}

// take hands out e's Reply and notices; later notices go to the stream.
func (r *reqs) take(e *reqEntry) (core.Reply, []noticeJSON, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e.answered = true
	return e.reply, append([]noticeJSON{}, e.notices...), true
}

// giveUp ends the wait with no Reply: what was held goes to the stream now,
// and the rest as it comes. A Reply that arrived meanwhile wins.
func (r *reqs) giveUp(e *reqEntry) (core.Reply, []noticeJSON, bool) {
	r.mu.Lock()
	if e.replied {
		r.mu.Unlock()
		return r.take(e)
	}
	e.detached = true
	held, device := e.notices, e.device
	e.notices = nil
	r.mu.Unlock()
	for _, n := range held {
		r.stream(device, n)
	}
	return core.Reply{}, nil, false
}
