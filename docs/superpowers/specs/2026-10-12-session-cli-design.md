# Session Subcommands

**Date:** 2026-10-12
**Status:** Approved design. Brainstormed with the user.

## Problem

The CLI cannot create or drive a session. Sessions are made in the TUI
(`n`, `N`) or by a Lua script (`ctx:new_instance`), and paused, resumed
and killed only there. The daemon already serves every lifecycle request
(`Create`, `Kill`, `Pause`, `Resume`) to any client, so the gap is the
client.

The scrum spec (`2026-10-03-scrum-workspaces-design.md` §5) plans
`loom work spawn` and `release`, but gated to scrum agents
(`LOOM_INSTANCE`, `LOOM_ROLE`, `LOOM_WORKSPACE`) and due in its rollout
stage 2. This spec builds the general commands first; `spawn` and
`release` will wrap them.

## Decisions

| # | Decision |
|---|---|
| 1 | Verbs: `new`, `list`, `kill`, `pause`, `resume`. `send` is dropped for now: a CLI can't reach Claude Code's cross-session inbox (no sender outside a Claude session; probe 2 of the supervised spec), and pasting into the prompt can answer an open permission or question menu. It is designed later, with a `--name` + `SendMessage` channel. |
| 2 | Registered workspaces only: `-w <name>`, else the registered workspace whose repo holds the cwd, else refuse. No global context. |
| 3 | No `--program`: `new` launches the workspace's default program (`Settings.GetProgram()`), in line with the Claude-only direction. |
| 4 | Launch defaults are resolved client side by a helper shared with the TUI's Launch Options modal (approach A). No wire or `state.json` change. Scrum's in-daemon spawn can move the helper into the model later. |

## Commands

```
loom session new <title> [-w <ws>] [--prompt <text> | --prompt-file <file>]
                 [--branch <existing>] [--issue <n>] [--account <name>]
                 [--model <m>] [--permission-mode <mode>] [--effort <e>]
                 [--no-remote-control]
loom session list   [-w <ws>] [--json]
loom session kill   <title> [-w <ws>] [--id <n>] [-y]
loom session pause  <title> [-w <ws>] [--id <n>]
loom session resume <title> [-w <ws>] [--id <n>]
```

Each subcommand defines its own `-w/--workspace`, as `reset` does; the
root's `-w` is not persistent.

### Naming a session

By exact title among the workspace's rows, never by prefix. The workspace
terminal is not a session here: `list` omits it and no command names it.
No match refuses. More than one match (the TUI allows a second record
under a title) refuses, naming the count and pointing at `--id`, the
`InstanceID` that `list --json` prints. With `--id` the title must still
match, so a stale ID can't land on another session.

### `new`

- **Title.** `core.CheckNewTitle` (below): not empty, at most 32 cells
  wide, not a title the workspace's storage preserves
  (`WorkspaceView.PreservedTitles`). The CLI also refuses a title any row
  of the workspace holds, since it names sessions by title. The TUI keeps
  allowing duplicates; that is out of scope.
- **Launch options.** What the modal pre-fills: `launch.FromSettings` of
  the workspace's settings, then `core.NewSessionLaunch` for the account.
  Flags override: `--account`, `--model`, `--permission-mode`, `--effort`.
  Remote control, 1M context, Headroom and the 1-hour cache TTL follow the
  settings and have no flags. A non-Claude default program clears the
  account; `--account` then warns on stderr and is ignored.
- **Remote control.** When the options enable it (`EffectiveRemoteControl`)
  and the account's auth blocks it (`launch.RemoteControlBlocked`), `new`
  refuses with the auth's reason; the TUI asks here and the CLI can't.
  `--no-remote-control` turns it off and starts.
- **`--branch`** checks out an existing branch (the branch picker's
  choice); without it a new branch is made from the prefix.
- **`--issue <n>`** records the issue link only. The TUI's issue flow also
  fetches the issue to build the title and prompt; the CLI does not.
- **`--prompt`/`--prompt-file`** is the creation prompt, sent once the
  agent is up, as `N` sends it. The two are exclusive.
- **Returns** once the session runs and its prompt is sent, and prints its
  title, branch and worktree.

### `list`

A table: title, status (with `WaitReason` when set), branch, account, and
the age of the status. Recoverable rows are listed with their status.
`--json` prints an array of objects with fixed field names, the contract
scrum's `board` builds on:

| Field | From `InstanceView` |
|---|---|
| `id` | `ID` |
| `title` | `Title` |
| `status` | `Status.String()` (`Running`, `Ready`, `Loading`, `Paused`, `Prompting`, `Deleting`, `Recoverable`) |
| `wait_reason` | `WaitReason` |
| `status_since` | `StatusSince`, RFC 3339 |
| `branch` | `Branch` |
| `worktree` | `WorktreePath` |
| `repo` | `RepoPath` |
| `program` | `Program` |
| `account` | `Account` |
| `issue` | `Issue` (0: none) |
| `pr` | `GitHub.PRNumber` when `GitHub.HasPR`, else 0 |

### `kill`

Asks `Kill session '<title>' in <ws>? [y/N]` on stdin, as `account remove`
asks; `-y/--yes` skips it. Anything but `y`/`yes`, an empty line or EOF
included, kills nothing and exits 1.

### Exit codes

0 on success. A refusal or failure goes to stderr with exit 1. A notice
riding a successful request's `Reply` (`Reply.Notice`: a Kill's
undroppable stash SHA, a Resume's recovery commands) goes to stderr with
exit 0.

## How a command runs

1. **Join and resolve.** `withDaemon` joins the daemon (starting one when
   none runs). `resolveWorkspace` runs `ReloadRegistry`, then `-w` or the
   cwd match against the registry, then `served`. It is extracted from
   `workspace status`, which uses it too.
2. **`new`.** `ReloadAccounts` (the modal reloads before reading
   accounts), the defaults, the flags, the title checks; then
   `Create(ws, NewInstance{…, Start: true}, req)`.
   - A refusing `Reply` exits 1.
   - Then it waits for `Started{Req: req}` (the daemon sends it after the
     initial prompt) or `Notice{Req: req, Err}` (a failed start; the
     daemon has already removed the instance and cleaned up). Today
     `client.events` drops a notice naming one of the command's requests,
     because its `Reply` reports it; it now hands such a notice to the
     waiter instead, and still writes no notice twice.
   - No timeout: a start may include a slow worktree setup. On SIGINT it
     exits 1, saying the daemon is still starting the session.
3. **`kill`, `pause`, `resume`.** The request with a `ReqID`, then
   `awaitReply`. Each `Reply` follows its job's delivery, so the command
   returns once the session is gone, paused or resumed. An `admit`
   refusal (pausing a paused session) is the Reply's `Err` and exits 1.
4. **The daemon lost** while waiting: `lost the loom daemon: …`, exit 1
   (`client.await`).

## Code

| Where | What |
|---|---|
| `internal/cli/session.go` (new) | `SessionCmd` and its subcommands, `findSession`, the JSON row. |
| `internal/cli/workspace.go` | `resolveWorkspace`, extracted from `workspace status`. |
| `internal/cli/join.go` | `client.events` hands a notice naming one of the command's requests to the waiter. |
| `main.go` | `rootCmd.AddCommand(cli.SessionCmd)`. |
| `core/new_session.go` (new) | `NewSessionLaunch(c Core, s config.Settings, program string) (launch.Options, string)`: the account switch from `app/accounts.go` (`newLaunchOptionsOverlay`) and its removed-account notice. `CheckNewTitle(title string, ws WorkspaceView) error`. |
| `app/accounts.go`, `app/state_new.go` | Call the two helpers. Which account rows the modal shows stays in `app`. |

`session/launch` can't hold `NewSessionLaunch`: it may import only
`config` and `session`, and the helper reads accounts through `Core`.

## Testing

`internal/cli/session_test.go`, against an in-process daemon. Today's
`serveDaemon` treats every tmux session as dead, so a `serveDaemonLive`
variant runs the daemon on a private real tmux server (short socket
name), with a temp git repo registered as a workspace and a stand-in
agent that stays up. Each guard is pinned on both sides:

- workspace: `-w` and a cwd inside a registered repo resolve; an
  unregistered repo refuses and creates nothing;
- title: taken, preserved, empty and too wide refuse and create nothing;
  a free title creates;
- defaults: `NewSessionLaunch` unit tests (default account, a removed
  account falls back with its notice, a non-Claude program clears it);
  the modal's tests pass on it unchanged; flags override;
- remote control blocked refuses with the reason; `--no-remote-control`
  starts;
- `new` returns after `Started`; a failed start (a `--branch` that does
  not exist) exits 1 with the daemon's error and leaves no instance;
- `kill`: `n` and EOF kill nothing, `-y` kills;
- `pause` then `resume`: Paused, then Running; a second `pause` exits 1
  with the refusal;
- an ambiguous title refuses and `--id` selects; an `--id` whose title
  doesn't match refuses;
- `list --json`'s field names.

End to end: `TestE2E_Clients_SessionLifecycle` runs `new`, `list --json`,
`pause`, `resume` and `kill` against real builds with no TUI open.

## Documentation

- `USAGE.md`: a Session Subcommands table and examples in the CLI
  Reference.
- `internal/cli/README.md`: the commands in its command-to-request table.
- `internal/cli/CLAUDE.md`: `new` waits for `Started` or its own request's
  notice; sessions are named by exact title or ID only; `kill` confirms.
- `core/README.md`: `NewSessionLaunch` and `CheckNewTitle` are the one
  source of a new session's defaults and title rules for the TUI and the
  CLI.
- `docs/specs/protocol.md` is unchanged: no wire change.

## Out of scope

- `send`, and any delivery into Claude Code's inbox.
- `--program` and `--profile`; the global context; a cross-workspace
  `list`.
- The issue flow's title and prompt expansion.
- Flags for remote control on, 1M context, Headroom and the cache TTL.
- `recover`, `merge`, `push`, `attach`.
- Refusing duplicate titles in the TUI.
- `loom work spawn`/`release`, which wrap these commands in scrum's
  stage 2.
