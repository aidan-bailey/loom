package core

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	cmd2 "github.com/aidan-bailey/loom/cmd"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/git"
)

// ResetReport says how far a reset got (ResetWorkspace), step by step.
type ResetReport struct {
	// Records: the workspace's stored instances were deleted.
	Records bool
	// Sweep is what the sweep of its tmux sessions did, once it ran; Swept
	// that it ran and every kill landed.
	Sweep session.SweepResult
	Swept bool
	// Worktrees: its worktrees and their branches were removed.
	Worktrees bool
}

// ResetWorkspace resets the workspace of ctx from its records on disk, as
// `loom reset` does with no daemon running (the model's Reset runs the
// same steps over the records it serves). In order, it
//  1. deletes the workspace's stored instances, and the records storage
//     preserves beside them (undecodable or unrecovered);
//  2. kills the loom tmux sessions started in its repository or worktrees
//     dir, and none of another workspace's (registry tells them apart; nil
//     when it can't be read), with exec;
//  3. removes its worktrees and their branches: unpushed commits on them
//     are lost.
//
// A sweep that fails stops it before step 3: some of the workspace's
// agents may still be running in those worktrees. The report says how far
// it got.
func ResetWorkspace(ctx *config.WorkspaceContext, registry *config.WorkspaceRegistry, exec cmd2.Executor) (ResetReport, error) {
	storage, err := session.NewStorage(config.LoadStateFrom(ctx.ConfigDir), ctx.ConfigDir)
	if err != nil {
		return ResetReport{}, fmt.Errorf("failed to initialize storage: %w", err)
	}
	return resetWorkspace(ctx, storage, registry, exec)
}

// resetWorkspace runs ResetWorkspace's steps, deleting the records through
// storage: the served workspace's own in the model, so its in-memory
// payload follows.
func resetWorkspace(ctx *config.WorkspaceContext, storage *session.Storage, registry *config.WorkspaceRegistry, exec cmd2.Executor) (ResetReport, error) {
	var rep ResetReport
	if err := storage.DeleteAllInstances(); err != nil {
		return rep, fmt.Errorf("failed to reset storage: %w", err)
	}
	rep.Records = true

	// The records are gone, so nothing is claimed: every session started in
	// the workspace's roots goes, and only those (the tmux server is shared
	// by every workspace).
	scope := session.NewSweepScope([]*config.WorkspaceContext{ctx}, registry)
	swept, err := session.CleanupOrphanedSessions(nil, scope, exec)
	rep.Sweep = swept
	if err != nil {
		return rep, fmt.Errorf("tmux cleanup failed (%d killed, %d kills failed); worktrees and branches were NOT removed, since an agent may still be running in one — re-run once tmux answers: %w",
			swept.Killed, swept.Failed, err)
	}
	rep.Swept = true

	// A workspace that never had a session has no worktrees dir: nothing to
	// remove.
	if _, err := os.Stat(filepath.Join(ctx.ConfigDir, "worktrees")); errors.Is(err, fs.ErrNotExist) {
		rep.Worktrees = true
		return rep, nil
	}
	if err := git.CleanupWorktrees(ctx.ConfigDir, nil); err != nil {
		return rep, fmt.Errorf("failed to cleanup worktrees: %w", err)
	}
	rep.Worktrees = true
	return rep, nil
}

// resetResult is a Reset job's result.
type resetResult struct {
	ws     *Workspace
	report ResetReport
	err    error
}

// Reset resets the served workspace id as `loom reset` does
// (ResetWorkspace). Its sessions leave the workspace at once, and a job
// deletes their records, kills the tmux sessions started in its roots
// and removes its worktrees and their branches. Meanwhile the workspace
// takes no session (Create) and no terminal (ensureTerminal). The job's
// outcome is the Reply to req, and a failure is shown as a Notice too;
// once it lands, an opened workspace gets a fresh terminal, as the next
// start used to give it.
//
// A refusal (ErrRefused) is returned, with no Reply: an unknown
// workspace, a busy session or a reset already running (busy), and a
// workspace whose load failed. Its storage refuses writes, since loom
// can't read its records; the direct path clears that (`loom serve stop`,
// then `loom reset`).
func (m *Model) Reset(id WorkspaceID, req ReqID) error {
	ws := m.wsLookup(id)
	if ws == nil || ws.ctx == nil || ws.storage == nil {
		return refusedError{errors.New("reset: the model serves no such workspace")}
	}
	if why := busy(ws); why != "" {
		return refusedError{fmt.Errorf("reset %s: %s", ws.Label(), why)}
	}
	if ws.loadErr != nil || ws.storage.WritesRefused() {
		return refusedError{fmt.Errorf("reset %s: its sessions failed to load, so loom can't tell what the reset would delete: stop the daemon (`loom serve stop`), then run `loom reset`", ws.Label())}
	}
	ws.insts = nil
	ws.resetting, ws.terminalUnsettled = true, false
	ctx, storage, registry, exec, label := *ws.ctx, ws.storage, m.registryCopy(), m.executor(), ws.Label()
	m.spawn(m.track(req, 0, func() any {
		rep, err := resetWorkspace(&ctx, storage, registry, exec)
		if err != nil {
			err = fmt.Errorf("reset %s: %w", label, err)
		}
		return resetResult{ws: ws, report: rep, err: err}
	}))
	m.emit(InstancesChanged{})
	m.emit(ClientsStale{})
	return nil
}

// deliverReset finishes a reset. One that deleted nothing serves the
// workspace's records again, read afresh from disk; an opened workspace
// gets its terminal back (ensureTerminal).
func (m *Model) deliverReset(r resetResult) {
	ws := r.ws
	ws.resetting = false
	log.For("core").Info("workspace.reset", "workspace", ws.Label(), "records", r.report.Records,
		"killed", r.report.Sweep.Killed, "unowned", r.report.Sweep.Unowned, "worktrees", r.report.Worktrees, "err", r.err)
	m.notifyErr(r.err)
	if !m.isLoadedWS(ws) {
		return
	}
	if !r.report.Records {
		if err := m.reloadWS(ws); err != nil {
			m.notifyErr(err)
		}
	}
	if ws.opened {
		m.ensureTerminal(ws)
	}
	m.emit(InstancesChanged{})
}

// registryCopy is a copy of the registry's workspaces for a job (the
// registry has no lock), nil without a registry.
func (m *Model) registryCopy() *config.WorkspaceRegistry {
	if m.registry == nil {
		return nil
	}
	return &config.WorkspaceRegistry{Workspaces: slices.Clone(m.registry.Workspaces)}
}
