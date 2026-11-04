package core

import (
	"context"
	"fmt"
	"time"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/hooks"
	"github.com/aidan-bailey/loom/session/mockup"
)

// The brainstorm companion's mockup (spec
// docs/superpowers/specs/2026-11-04-pwa-mockup-tab-design.md). The model
// looks for a session's companion when a hook scan reads an event that
// hands the turn back to the user, in the scan's own job so the screen
// lands in the same delivery as the status, and on each ReadMockup; it
// keeps the screen on the instance (Instance.SetMockup), and viewOf
// publishes it as InstanceView.Mockup.

// findMockup is mockup.Find; a test swaps it to count the looks.
var findMockup = mockup.Find

// mockupLook is one look for a session's companion: what Find said, any
// doubt being no companion.
type mockupLook struct {
	c  mockup.Companion
	ok bool
}

// lookForMockup runs findMockup, on a job's goroutine, and logs an I/O
// error at debug.
func lookForMockup(worktree string) *mockupLook {
	c, ok, err := findMockup(worktree)
	if err != nil {
		log.DebugKV("core.mockup.find_failed", "worktree", worktree, "err", err.Error())
	}
	return &mockupLook{c: c, ok: ok && err == nil}
}

// applyMockup records a look on inst: the screen found, or none.
func applyMockup(inst *session.Instance, look *mockupLook) {
	if look.ok {
		inst.SetMockup(look.c.Screen, look.c.Changed)
		return
	}
	inst.SetMockup("", time.Time{})
}

// handsBack reports whether a scan's events hand the turn back to the
// user, when a brainstorm's screen is worth looking for: the parent's Stop
// or Notification, or a PermissionRequest from any agent, as the status
// mapping counts them.
func handsBack(events []hooks.Event) bool {
	for _, e := range events {
		switch e.Name {
		case hooks.EventStop, hooks.EventNotification:
			if e.AgentID == "" {
				return true
			}
		case hooks.EventPermissionRequest:
			return true
		}
	}
	return false
}

// mockupResult is ReadMockup's job result.
type mockupResult struct {
	inst *session.Instance
	look *mockupLook
	page mockup.Page
	err  error
}

// ReadMockup reads the page session id's brainstorm companion serves now
// (mockup.Find, then mockup.Fetch) in a job, records what it found on the
// instance, and answers with the Reply's Mockup; with no companion, or a
// page Fetch refuses, the Reply's Err says why.
func (m *Model) ReadMockup(id InstanceID, req ReqID) {
	inst, _, ok := m.admit(opReadMockup, id, req)
	if !ok {
		return
	}
	m.spawn(m.track(req, id, readMockupJob(inst, inst.GetWorktreePath())))
}

// readMockupJob is ReadMockup's job: the worktree is read on the loop, and
// the job touches only the disk and the loopback port.
func readMockupJob(inst *session.Instance, worktree string) Job {
	title := inst.Title
	return func() any {
		look := lookForMockup(worktree)
		res := mockupResult{inst: inst, look: look}
		if !look.ok {
			res.err = fmt.Errorf("%s %s: no brainstorm companion is running in this session's worktree", opReadMockup, title)
			return res
		}
		html, err := mockup.Fetch(context.Background(), look.c.Port)
		if err != nil {
			res.err = fmt.Errorf("%s %s: %w", opReadMockup, title, err)
			return res
		}
		res.page = mockup.Page{Screen: look.c.Screen, Changed: look.c.Changed, HTML: html}
		return res
	}
}

// deliverMockup records what a ReadMockup found; its Reply carries the
// page (deliverTracked).
func (m *Model) deliverMockup(res mockupResult) {
	applyMockup(res.inst, res.look)
}
