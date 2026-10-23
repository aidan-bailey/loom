package core

import (
	"fmt"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/git"
	"github.com/aidan-bailey/loom/session/transcript"
)

// turnsResult is ReadTurns' job result, and diffResult ReadDiff's.
type turnsResult struct {
	page transcript.Page
	err  error
}

type diffResult struct {
	patch git.Patch
	err   error
}

// ReadTurns reads a page of session id's conversation from Claude's
// transcript (transcript.Read) in a job, and answers with the Reply's
// Turns. The transcript is the one the session's launch named
// (Instance.ClaudeSession, read here on the loop); a session whose launch
// has not named one is refused.
func (m *Model) ReadTurns(id InstanceID, query transcript.Query, req ReqID) {
	inst, _, ok := m.admit(opReadTurns, id, req)
	if !ok {
		return
	}
	conversation, path := inst.ClaudeSession()
	if conversation == "" || path == "" {
		m.refuse(req, id, fmt.Errorf("%s %s: loom has not seen this session's transcript yet", opReadTurns, inst.Title))
		return
	}
	m.spawn(m.track(req, id, readTurnsJob(path, conversation, query)))
}

// readTurnsJob is ReadTurns' job: it reads only the file.
func readTurnsJob(path, conversation string, query transcript.Query) Job {
	return func() any {
		page, err := transcript.Read(path, conversation, query)
		return turnsResult{page: page, err: err}
	}
}

// ReadDiff reads session id's worktree diff against its base commit
// (Instance.ReadPatch) in a job, and answers with the Reply's Diff. Nothing
// is cached: the patch is the requester's alone.
func (m *Model) ReadDiff(id InstanceID, req ReqID) {
	inst, _, ok := m.admit(opReadDiff, id, req)
	if !ok {
		return
	}
	m.spawn(m.track(req, id, readDiffJob(inst)))
}

// readDiffJob is ReadDiff's job. It runs git off the loop through the
// instance's own locks, as the tick's probe does.
func readDiffJob(inst *session.Instance) Job {
	return func() any {
		patch, err := inst.ReadPatch()
		return diffResult{patch: patch, err: err}
	}
}
