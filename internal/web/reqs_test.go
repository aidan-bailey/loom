package web

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reqsFixture is a tracker over a bare layer (no client, no hub) on a clock
// the test holds, with the notices it sends to the stream recorded.
type reqsFixture struct {
	r   *reqs
	now time.Time

	mu   sync.Mutex
	sent []lateNotice
}

func newReqsFixture(t *testing.T) *reqsFixture {
	t.Helper()
	f := &reqsFixture{now: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)}
	f.r = newReqs(&Layer{now: func() time.Time { return f.now }})
	f.r.timeout = time.Second
	f.r.poll = 5 * time.Millisecond
	f.r.setLate(func(device string, n noticeJSON) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.sent = append(f.sent, lateNotice{device: device, n: n})
	})
	return f
}

// late is what went to the stream so far.
func (f *reqsFixture) late() []lateNotice {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]lateNotice(nil), f.sent...)
}

// answered files a Reply for req and takes it, as a handler would.
func (f *reqsFixture) answered(t *testing.T, req core.ReqID, device string) {
	t.Helper()
	f.r.apply([]core.Event{core.Reply{Req: req}})
	_, _, ok := f.r.wait(context.Background(), req, device)
	require.True(t, ok)
}

func TestReqs_NextNumbersRequestsFromOne(t *testing.T) {
	f := newReqsFixture(t)
	assert.Equal(t, core.ReqID(1), f.r.next(), "0 asks for no Reply")
	assert.Equal(t, core.ReqID(2), f.r.next())
}

func TestReqs_WaitReturnsTheReplyAndTheNoticesBeforeIt(t *testing.T) {
	f := newReqsFixture(t)
	req, other := f.r.next(), f.r.next()
	f.r.apply([]core.Event{
		core.Notice{Req: req, Info: "stash kept"},
		core.Notice{Req: other, Info: "another request's"},
		core.Notice{Info: "tied to no request"},
		core.Reply{Req: req, ID: 7},
	})

	reply, notes, ok := f.r.wait(context.Background(), req, "phone")

	require.True(t, ok)
	require.NoError(t, reply.Err)
	assert.Equal(t, core.InstanceID(7), reply.ID)
	require.Len(t, notes, 1, "only this request's notice")
	assert.Equal(t, "stash kept", notes[0].Text)
	assert.False(t, notes[0].Error)
	assert.Equal(t, f.now, notes[0].At)
	assert.Empty(t, f.late(), "nothing went to the stream")
}

func TestReqs_ARefusalIsTheRepliesError(t *testing.T) {
	f := newReqsFixture(t)
	req := f.r.next()
	f.r.apply([]core.Event{core.Reply{Req: req, Err: errors.New("kill x: the session is busy (Loading)")}})

	reply, notes, ok := f.r.wait(context.Background(), req, "phone")

	require.True(t, ok)
	assert.EqualError(t, reply.Err, "kill x: the session is busy (Loading)")
	assert.NotNil(t, notes, "an answer's notices are a list, even an empty one")
	assert.Empty(t, notes)
}

func TestReqs_ARepliesNoticeRidesTheAnswerOnce(t *testing.T) {
	f := newReqsFixture(t)
	stash := errors.New("kept the stash abc123")

	// A kill raises its success notice twice: as a Notice and in the Reply.
	req := f.r.next()
	f.r.apply([]core.Event{core.Notice{Req: req, Err: stash}, core.Reply{Req: req, Notice: stash}})
	_, notes, ok := f.r.wait(context.Background(), req, "phone")
	require.True(t, ok)
	require.Len(t, notes, 1)
	assert.Equal(t, "kept the stash abc123", notes[0].Text)
	assert.True(t, notes[0].Error)

	// A Reply's notice that no Notice repeated still reaches the answer.
	req = f.r.next()
	f.r.apply([]core.Event{core.Reply{Req: req, Notice: stash}})
	_, notes, ok = f.r.wait(context.Background(), req, "phone")
	require.True(t, ok)
	require.Len(t, notes, 1)
	assert.Equal(t, "kept the stash abc123", notes[0].Text)
}

func TestReqs_TheTimeoutLeavesTheOutcomeToTheStream(t *testing.T) {
	f := newReqsFixture(t)
	f.r.timeout = 20 * time.Millisecond
	req := f.r.next()
	f.r.apply([]core.Event{core.Notice{Req: req, Info: "heard before giving up"}})

	_, notes, ok := f.r.wait(context.Background(), req, "phone")

	assert.False(t, ok, "no Reply within the wait")
	assert.Empty(t, notes)
	failure := errors.New("push x: rejected")
	f.r.apply([]core.Event{core.Notice{Req: req, Err: failure}, core.Reply{Req: req, Err: failure}})
	late := f.late()
	require.Len(t, late, 2, "what was held back, then the failure once")
	assert.Equal(t, "phone", late[0].device)
	assert.Equal(t, "heard before giving up", late[0].n.Text)
	assert.Equal(t, "phone", late[1].device)
	assert.Equal(t, "push x: rejected", late[1].n.Text)
	assert.True(t, late[1].n.Error)
}

func TestReqs_ASuccessThatCameAfterTheTimeoutSaysNothing(t *testing.T) {
	f := newReqsFixture(t)
	f.r.timeout = 20 * time.Millisecond
	req := f.r.next()
	_, _, ok := f.r.wait(context.Background(), req, "phone")
	require.False(t, ok)

	f.r.apply([]core.Event{core.Reply{Req: req}})

	assert.Empty(t, f.late(), "the session's own update shows it")
}

func TestReqs_ACancelledRequestGivesUpToo(t *testing.T) {
	f := newReqsFixture(t)
	req := f.r.next()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, ok := f.r.wait(ctx, req, "phone")

	assert.False(t, ok, "the phone went away")
	f.r.apply([]core.Event{core.Notice{Req: req, Info: "after it left"}})
	late := f.late()
	require.Len(t, late, 1)
	assert.Equal(t, "phone", late[0].device)
}

func TestReqs_NoticesAfterTheAnswerGoToTheStream(t *testing.T) {
	f := newReqsFixture(t)
	req := f.r.next()
	f.answered(t, req, "phone")

	f.r.apply([]core.Event{
		core.Started{Req: req, ID: 3, Title: "fresh"},
		core.Started{Req: req, ID: 3, Title: "fresh", PromptNotSent: "say hello"},
		core.Notice{Req: req, Err: errors.New("start fresh: no worktree")},
	})

	late := f.late()
	require.Len(t, late, 2, "a start that sent its prompt says nothing")
	assert.Equal(t, "phone", late[0].device)
	assert.Contains(t, late[0].n.Text, "say hello")
	assert.False(t, late[0].n.Error)
	assert.Equal(t, "start fresh: no worktree", late[1].n.Text)
	assert.True(t, late[1].n.Error)
}

func TestReqs_ANoticeForAnUntrackedRequestGoesToEveryStream(t *testing.T) {
	f := newReqsFixture(t)

	f.r.apply([]core.Event{core.Notice{Req: 99, Info: "nobody waits for this"}})

	late := f.late()
	require.Len(t, late, 1)
	assert.Equal(t, "", late[0].device, "no device: every stream")
	assert.Equal(t, "nobody waits for this", late[0].n.Text)
}

func TestReqs_ARefusedRequestIsUnavailable(t *testing.T) {
	f := newReqsFixture(t)
	req, answeredFirst := f.r.next(), f.r.next()
	f.r.apply([]core.Event{core.Reply{Req: answeredFirst}})
	once := true
	f.r.refused = func() []core.ReqID {
		if !once {
			return nil
		}
		once = false
		return []core.ReqID{req, answeredFirst}
	}

	reply, _, ok := f.r.wait(context.Background(), req, "phone")

	require.True(t, ok)
	assert.ErrorIs(t, reply.Err, core.ErrUnavailable, "the client refused it itself; no Reply will come")
	reply, _, ok = f.r.wait(context.Background(), answeredFirst, "phone")
	require.True(t, ok)
	assert.NoError(t, reply.Err, "a Reply that came first wins over the refusal list")
}

func TestReqs_ALostClientFailsEveryWaiter(t *testing.T) {
	f := newReqsFixture(t)
	first, second := f.r.next(), f.r.next()
	f.r.lost = func() error { return errors.New("connection to the model lost") }

	for _, req := range []core.ReqID{first, second} {
		reply, _, ok := f.r.wait(context.Background(), req, "phone")
		require.True(t, ok)
		assert.ErrorIs(t, reply.Err, core.ErrUnavailable)
		assert.ErrorContains(t, reply.Err, "connection to the model lost")
	}
}

func TestReqs_ForgetsAnAnsweredRequestAfterAWhile(t *testing.T) {
	f := newReqsFixture(t)
	req := f.r.next()
	f.answered(t, req, "phone")
	f.r.apply([]core.Event{core.Notice{Req: req, Info: "while kept"}})

	f.now = f.now.Add(reqKeep + time.Minute)
	f.r.next() // forgets what is older than reqKeep
	f.r.apply([]core.Event{core.Notice{Req: req, Info: "after forgotten"}})

	late := f.late()
	require.Len(t, late, 2)
	assert.Equal(t, "phone", late[0].device, "kept: its device's stream")
	assert.Equal(t, "", late[1].device, "forgotten: every stream")
}

func TestReqs_BoundsWhatItTracksAndKeepsWhatIsAwaited(t *testing.T) {
	f := newReqsFixture(t)
	first := f.r.next()
	f.answered(t, first, "phone")
	for i := 1; i < maxReqs; i++ {
		f.answered(t, f.r.next(), "phone")
	}
	pending := f.r.next() // over the bound: the oldest answered one goes

	f.r.apply([]core.Event{
		core.Notice{Req: first, Info: "the oldest, forgotten"},
		core.Notice{Req: first + 1, Info: "the next, kept"},
	})
	late := f.late()
	require.Len(t, late, 2)
	assert.Equal(t, "", late[0].device)
	assert.Equal(t, "phone", late[1].device)

	f.r.next() // over the bound again: another answered one goes, never the awaited
	f.r.apply([]core.Event{core.Notice{Req: pending, Info: "still awaited"}, core.Reply{Req: pending}})
	_, notes, ok := f.r.wait(context.Background(), pending, "phone")
	require.True(t, ok)
	require.Len(t, notes, 1)
	assert.Equal(t, "still awaited", notes[0].Text)
	assert.Len(t, f.late(), 2, "it went into the answer, not to the stream")
}
