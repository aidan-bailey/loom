# Incident triage: sessions died together, a session won't resume, a worktree is gutted

**Symptoms:** "loom crashed": every agent shows Paused at once. A workspace terminal's restart fails with "already exists". `r` refuses with "loom cannot verify … as this session's worktree" or "git did not answer in time". A worktree directory still holds files but has no `.git`.

**Cause:** three distinct failures look alike from the TUI: something outside the daemon killed the sessions, the machine was too loaded for tmux or git to answer in time, or a worktree was half-removed under a live agent. Each needs a different recovery, and the wrong one destroys work.

**Rule:** decide which failure it is from the logs before touching anything, and never rebuild, move or delete a worktree an agent may still be running in.

## Where to look

- `loom debug` prints the TUI's log, the daemon's (`logs/serve.log` in the global dir), the daemon's pid and its tmux server.
- `serve.log` holds the lifecycle: `subsystem=core` (the health tick: `tick.tmux_gone_marking_paused`, `workspace_terminal.tmux_died_restarting`, `workspace_terminal.restart_failed`, `diff_stats_update_failed`), `subsystem=tmux` (`liveness.probe_timeout`), `subsystem=reconcile` (the orphan sweep: `orphan_tmux.kill_begin`, `orphan_tmux.sweep_done`), `subsystem=session` (boot reconcile: `reconcile.tmux_probe_inconclusive`) and `subsystem=serve` (start and stop).
- Restart markers: `serve.listening` ends a daemon's boot (its orphan sweep runs before it); `loader.loaded` marks a TUI starting. Compare them with `ps -o lstart= -p <pid>` to tell a restart from a quiet process.

## Which failure is it?

| Signature | Failure |
|---|---|
| Every session marked gone in one tick; afterwards `tmux ls` shows every session created at the same instant (the server restarted); a burst of `orphan_tmux.kill_begin` in some process's log | An external kill: another process's orphan sweep, or the tmux server itself died |
| Pauses or restarts scattered over minutes; `liveness.probe_timeout` warnings; `diff_stats_update_failed` git timeouts in the same window; a restart failing with "already exists" | Load: probes and git timing out. The tell is "already exists": a dead session can't already exist |
| One session marked gone, the rest fine | Its agent exited (`/exit`, a crash); its worktree is intact |

## An external kill

1. Find the burst: grep `orphan_tmux.kill_begin` in every `serve.log` and `loom.log` on the host: the user's global dir, each loomdev sandbox's (`~/.local/state/loom-dev/<name>/global/logs/serve.log`), and any dir a stray process ran with.
2. Name the process: `pgrep -fa loom`, then `tr '\0' '\n' < /proc/<pid>/environ | grep -E '^(TMUX|LOOM_|HOME)='` for each.
3. Know what still does it. The sweep kills only unclaimed sessions started under roots its process owns, and one daemon per global dir serves every registered workspace, so a second TUI only joins. It still happens with a loom build older than that ownership check, a dev daemon run inside a loom pane with `LOOM_ALLOW_NESTED=1` that serves the same workspaces, two global dirs that register one repository, or a tmux server that died on its own. A second loom's startup sweep killed all 20 sessions on 2026-09-23, before the ownership check.
4. Recover: the tick marked the dead sessions Paused without touching their worktrees, so `r` relaunches each in place with its recovery flag, resuming its conversation.

## Load: false dead

- A liveness probe killed at its deadline answers Unknown, not Dead (`session/tmux/session.go:SessionLiveness`), and the tick acts on no Unknown (`core/tick.go:applyLiveness`); boot reconcile retries a timed-out probe once and then assumes alive (`session/reconcile.go:CheckTmuxAlive`). A probe tmux answers with an error is still Dead, so a loaded server can still fake a death.
- Bias any new probe the same way. A wrong "alive" costs a failed restore; a wrong "dead" tears down a running agent or deletes its worktree.
- Recover: wait for the load to drop. A resume reattaches a session that is still alive, and refuses while the probe gets no answer.

## A session won't resume, pause or die

The refusal says what to do. Read it before running anything:

| Message | Meaning |
|---|---|
| "git did not answer in time while checking the worktree …" | Load: retry later |
| "loom cannot verify … as this session's worktree …" | `.git` is there but git won't vouch for it as this repository's linked worktree. If it holds nothing you need, run the quoted `mv` and resume: the worktree is rebuilt from the branch |
| "cannot tell whether this session's agent is still running …" | The liveness probe timed out: retry later |
| "cannot resume …: tmux session … was started in …" | Another workspace's session holds the name: loom neither reattaches to it nor relaunches over it |
| "worktree locked (reason: …)", on a pause or a kill | A user's lock, or one a `git worktree add` left: loom removes only an `initializing` lock older than its own add deadline plus a minute (`gitWorktreeAddTimeout`). The message names the `git worktree unlock` to run, and the session keeps its row until then |
| "failed to move leftover worktree directory … aside (a process may still be writing into it)" | Something still runs in a gutted tree |

Then check `git worktree list` for `prunable` or `locked` entries, and `readlink /proc/<pid>/cwd` of each agent process: an agent can outlive its worktree's removal and keep writing into an unlinked tree.

## A gutted worktree

A gutted worktree is a directory with no `.git`: a `git worktree remove` that failed half-way, because an agent kept writing into the tree (ENOTEMPTY on the final rmdir) or because the remove was killed at a deadline.

- **The danger.** git run inside it walks up to the enclosing repository, the main checkout: its log and reflog are main's, and a `git reset --hard` from an agent still there resets main. Check with `git -C <dir> rev-parse --show-toplevel`.
- **In the app.** Resume classifies the tree (`decideResume`): a gutted one is rebuilt from the branch after its leftovers are moved to `<path>.orphaned` (`git.worktree_leftover_preserved` in `serve.log` names it). Salvage uncommitted work from that copy with a diff.
- **By hand**, to repair the gutted directory in place (used on 2026-08-21; copy any known-dirty file to scratch first, and run nothing while an agent or a resume can touch the tree):
  ```sh
  git -C <repo> worktree prune
  git -C <repo> worktree add --no-checkout "$S/<same-basename>" <branch>
  mv "$S/<same-basename>/.git" <gutted>/.git
  git -C <repo> worktree repair <gutted>
  git -C <gutted> reset -q
  git -C <gutted> ls-files -d -z | xargs -0 git -C <gutted> checkout --
  ```
  `$S` is a scratch dir, and the scratch checkout takes the gutted directory's basename, which names git's admin entry for it. The `reset -q` rebuilds the index from HEAD and leaves the files alone; the last line restores only deleted tracked files, so modified ones keep their uncommitted work.
- **Then register it.** A raw `git worktree add` registers nothing with loom. If the session's record is gone from `state.json`, the next boot's reconcile treats the tree as an orphan: a clean one whose session is dead is removed (the branch stays), anything else shows as Recoverable, which `r` adopts.

## Reflog noise

`git worktree add` writes a blank reflog entry and a `reset: moving to HEAD` in the new worktree. Those are creation artifacts, not destructive resets.

## Traps

- An installed loom older than the repo may lack these guards: check `loom version` before trusting the behaviour this guide describes.
- Before telling the user to resume a session its agent's exit paused, run `git status` in its worktree: on such a build, a resume could rebuild an intact, dirty tree.

## What the gates won't tell you

- No test reproduces a loaded machine: the real-tmux tests run unloaded, and the probe-timeout tests fake the deadline.
- Nothing notices an agent writing into an unlinked tree.
