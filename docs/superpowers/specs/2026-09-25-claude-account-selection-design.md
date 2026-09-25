# Claude Account Selection

**Date:** 2026-09-25
**Status:** Approved design
**Verified against:** Claude Code 2.1.281 (Nix build), by local experiments on
2026-09-25: `claude auth status` and `claude agents --json` under an empty
`CLAUDE_CONFIG_DIR`, a headless `get_usage` control request against a logged-in
Max account, and a read of the CLI's embedded schemas. Auth-precedence and
`CLAUDE_CODE_OAUTH_TOKEN` facts come from code.claude.com/docs/en/authentication.

## Problem

A user with several Claude subscriptions (e.g. two Max plans) wants to spread
Loom's sessions across them so that no one account hits its 5-hour or weekly
limit while another sits idle. Today every Claude session Loom launches runs
under whatever account `~/.claude` is logged in to, and Loom shows nothing
about how much of that account's limit is used.

The goal: pick the account per session by hand, with each account's usage
visible while choosing and at a glance afterwards. Automatic assignment and
failover are out of scope (see Out of scope).

## Findings

### An account is a config dir

Claude Code has no account flag. The account is whatever credentials live in
the config dir, and `CLAUDE_CONFIG_DIR` moves the whole config dir:
`.credentials.json` (on macOS the Keychain item is keyed per config dir),
`.claude.json` (which also moves inside the dir), `settings.json`, `CLAUDE.md`,
skills, plugins, `projects/` (transcripts and auto-memory), `sessions/` and
`daemon/`. Run under an empty dir, `claude auth status` reports `loggedIn:
false` and a `configDirectory` pointing at it.

`CLAUDE_CODE_OAUTH_TOKEN` (from `claude setup-token`) is the other way to
switch accounts: it keeps one config dir and changes who is billed. It was
rejected because the token is inference-only, so `--remote-control` rejects it
(Loom enables remote control by default), and Loom would have to store a
secret.

### The roster is per config dir

`claude agents --json` returned `[]` under an empty `CLAUDE_CONFIG_DIR` while
the default dir listed a dozen sessions. Loom's roster join
(`session/claude_roster.go`, `app/events.go:rosterQueryCmd`) therefore sees
only sessions of the account it runs under.

### Transcripts are per config dir

`--resume <id>` and `--continue` look in the active config dir's `projects/`.
A conversation started on one account cannot be resumed on another unless
`projects/` is shared.

### `claude auth status` identifies an account

It prints JSON with `loggedIn`, `authMethod`, `email`, `orgName`,
`subscriptionType` and `configDirectory`. Loom already runs it for
remote-control detection (`session/remote_control_auth.go`), once, at
startup, for the default account.

### Usage can be probed headlessly, for free

The SDK control protocol has a `get_usage` request ("Requests the structured
/usage data … Experimental — the response shape may change"), with a
`skip_behaviors` flag described as being "for callers that need only the plan
rate limits, such as a usage meter". Measured:

```
$ printf '%s\n' '{"type":"control_request","request_id":"u1","request":{"subtype":"get_usage","skip_behaviors":true}}' \
  | claude -p --input-format stream-json --output-format stream-json \
           --verbose --no-session-persistence --setting-sources ""
{"type":"control_response","response":{"subtype":"success","request_id":"u1","response":{
  "session":{…},"subscription_type":"max","rate_limits_available":true,
  "rate_limits":{"five_hour":{"utilization":8,"resets_at":"2026-09-25T11:20:00.356471+00:00",…},
                 "seven_day":{"utilization":30,"resets_at":"2026-09-29T23:00:00.356491+00:00",…},
                 …}}}}
```

The run took about 1.4s and exited 0, printing exactly one line, with no model
call and no cost. Without
`--setting-sources ""` the user's plugin `SessionStart` hooks ran and the
output was swamped by hook events. The CLI answers from its own usage snapshot
when that is under 60s old ("Usage read answered from a snapshot … endpoint
not asked"), so polling does not hammer the usage endpoint.
`rate_limits_available` is false for API-key, Bedrock and Vertex auth, and
then `rate_limits` is null.

### Other usage sources considered

- The statusline input JSON carries `rate_limits.{five_hour,seven_day}.
  {used_percentage,resets_at}`, live, but only while a session on that account
  runs, and capturing it means overriding the user's `statusLine` via
  `--settings`.
- `<configDir>/.claude.json` caches `cachedUsageUtilization`, but the CLI
  writes it only when it fetches usage; it was 74 minutes old with sessions
  running.
- A `StopFailure` hook with matcher `rate_limit` fires when a turn hits a
  limit.

The user chose the active probe.

## Decisions

1. **Accounts are config dirs that Loom creates and links to the main dir.**
   Only credentials, `.claude.json` and runtime state are per account;
   everything else, `projects/` included, is shared by symlink, so the user's
   setup follows every account and `--resume` works across accounts.
2. **Manual assignment.** The account is chosen per session in Launch Options,
   preselecting a global default. No automatic choice.
3. **Usage comes from an active probe** of every registered account on a
   2-minute cadence, brought forward whenever the user is about to choose.
4. **Usage is display-only.** It never drives a status, a transition or a
   launch decision, so a failed probe keeps the last good sample, dimmed with
   its age, instead of blanking it.
5. **Fail closed on a removed account.** A launch whose account no longer
   exists is refused; it never falls back to `default`, which would bill the
   wrong subscription.
6. **Zero change for single-account users.** All account UI stays hidden and
   no probes run until at least one extra account exists.
7. **Two management paths, one implementation.** The `loom account` CLI and
   the TUI Accounts overlay both call only the `account` package.

## Design

### 1. Package `account/`

A new top-level package with no `app`, `ui` or `session` imports and an
injected `internalexec.Executor`, in the style of `session/github`.

- `Registry`: loads and saves `accounts.json` in `config.GetGlobalConfigDir()`
  (next to `workspaces.json`, so `LOOM_GLOBAL_DIR` isolates sandboxes):

  ```json
  { "default": "max-2",
    "accounts": [ { "name": "max-2", "dir": "/home/u/.loom/accounts/max-2" } ] }
  ```

  The implicit account `default` (Claude with no override) is never stored in
  `accounts`, cannot be removed, and is the default when `"default"` is empty.
  A registry that fails to load is treated as holding no extra accounts, its
  error is surfaced once, and it latches every write shut until a later load
  succeeds, as `session.Storage` does, so a corrupt file is never overwritten.
- `ValidName(name)`: `[a-z0-9-]+`, not `default`.
- `MainDir(id Identity)`: the main config dir, taken from `claude auth
  status`'s `configDirectory` for the default account, else
  `$CLAUDE_CONFIG_DIR` (so a config dir set in the user's own environment is
  respected), else `~/.claude`.
- `Create(name)`: makes `<globalDir>/accounts/<name>/` and runs `Sync`.
- `Sync(acct, mainDir) → SyncReport`: for every top-level entry of `mainDir`
  not on the deny-list, create a symlink in the account dir if nothing is
  there. Idempotent. Never replaces an existing real file or directory; one
  whose main counterpart exists is reported as diverged (`not shared:
  settings.json`), which covers a tool that rewrote a linked file atomically
  and so replaced the link. The deny-list: `.credentials.json`, `.claude.json`,
  `sessions`, `daemon`, `session-env`, `ide`, `debug`, `cache`, `backups`,
  `shell-snapshots`, `statsig`. A deny-list, not an allow-list, because user
  files such as an `RTK.md` pulled in by `CLAUDE.md`'s relative `@` import
  must follow too.
- `Remove(name, force)`: unregisters and `os.RemoveAll`s the account dir.
  `RemoveAll` removes symlinks without following them, so shared content is
  untouched; a test pins this. It deletes only `filepath.Join(AccountsDir,
  name)`, and only when the name is valid and the stored dir cleans to
  exactly that path; any other stored dir (a hand-edited registry) is
  unregistered, not deleted. Without `force` it refuses while the account
  dir holds real, unshared entries (`Unshared`: a diverged file, or a dir
  Claude created before the main dir had it), since those may hold the
  only copy of a change.
- `Reload()`: re-reads accounts.json. The TUI reloads before every action
  that reads the registry, since `loom account` may have changed it from
  another terminal. Loaded entries are validated; a bad one latches the
  registry.
- `Env(acct) []string`: `CLAUDE_CONFIG_DIR=<dir>`, or nil for `default`.
- `AuthStatus(program, acct, exec) (Identity, error)`: `claude auth status`
  under the account env; `Identity{LoggedIn, AuthMethod, Email, Plan,
  ConfigDir}`.
- `ProbeUsage(program, acct, exec) (Usage, error)`: see section 4.
- `LoginCmd(program, acct) *exec.Cmd`: `claude auth login` under the account
  env, for the CLI to run in the foreground and the TUI to hand to
  `tea.ExecProcess`.

The binary is the first field of the configured Claude program, as the roster
does, so an absolute or Nix store path resolves to the same CLI the agents
run.

### 2. Instance and launch

- `Instance` gains `account string` (the account name; empty means
  `default`), set through `SetLaunchOptions` and read through `Account()`
  under `i.mu` like the other launch fields. It is persisted in
  `InstanceData` as `account`: `CurrentSchemaVersion` 7 → 8, an upgrade step
  in `session/storage_migrate.go:Migrate` (absent means `default`), and the
  JSON fixture in `cmd/workspace_migrate_shape_test.go`.
- `InstanceEnv(program, headroomProxy, cacheTTL1h)` becomes
  `InstanceEnv(LaunchEnv{Program, HeadroomProxy, CacheTTL1h,
  ClaudeConfigDir})`. `CLAUDE_CONFIG_DIR` is added only for Claude programs
  and a non-empty dir. Its four call sites (`session/instance.go` twice,
  `session/reconcile.go`, `session/subagent_hooks.go`) pass the dir resolved
  for the instance's account. The variable reaches the agent through `tmux
  new-session -e`, like `ANTHROPIC_BASE_URL`.
- Every real launch (`Start`, `startFreshWithRecovery`, `CrashRestart`,
  `Restart`, relaunch with `R`) resolves name → dir through the registry at
  that moment. A missing account fails the launch with `account "max-2" no
  longer exists — press R to relaunch on another account`. `Restore`
  (reattach) resolves nothing: the env is already in the tmux session.
- The registry reaches `session` as a published name → dir map
  (`session.SetAccountDirs`, an atomic pointer), which `app` republishes
  after every registry change. Launches read it on lifecycle goroutines, so
  it cannot live on the Update-goroutine-only registry. `session` imports
  `account` (for `DefaultName` and `AuthStatus`), which is safe because
  `account` imports nothing from `session`.
- A logged-out account needs no check from Loom: Claude shows its own login
  prompt in the pane.

**Switching accounts.** `R` (relaunch with options) shows the Account row
preset to the session's account. Choosing another relaunches with the
recorded conversation (`BuildResumeCommand`: `--resume <id>`), which the new
account finds because `projects/` is shared. The transcript path recorded
under the old account's dir resolves through its `projects` link, so the
existence check in `BuildResumeCommand` still passes. `resetHookLaunch`
already runs on every real launch, so no tracker or status state carries
across.

### 3. Roster and remote control per account

- `rosterQueryCmd` groups active Claude instances by account and runs one
  `claude agents --json` per account in use, under that account's env, in
  parallel inside the one Cmd, so the gated single-message contract
  (`app/pollgate.go`) holds.
- `home.roster` becomes `map[account]map[cwd]RosterEntry`;
  `rosterStatusFor` looks up `roster[inst.Account()][inst.GetWorktreePath()]`.
  The ambiguous-cwd rule stays per account.
- A failed query clears only that account's entries (its instances get no
  roster opinion, and their roster-sourced observations become no opinion as
  today); other accounts' entries stand.
- `DetectClaudeRemoteControlAuth` takes the account env. `home.rcAuth`
  becomes `map[account]RemoteControlAuth`, filled at startup for every
  account and refreshed after a login. The global
  `ANTHROPIC_API_KEY`/`ANTHROPIC_AUTH_TOKEN` check still applies to all.
  Launch Options and the remote-control prompts
  (`app/remote_control.go`) read the entry for the selected account.

### 4. Usage probe

`ProbeUsage` runs

```
<claude> -p --input-format stream-json --output-format stream-json \
         --verbose --no-session-persistence --setting-sources ""
```

with the account env, cwd set to the account dir (the main dir for
`default`) so no project entry is recorded for an arbitrary directory, a
15-second deadline, and one line on stdin:

```json
{"type":"control_request","request_id":"loom-usage","request":{"subtype":"get_usage","skip_behaviors":true}}
```

It scans stdout for the `control_response` whose `request_id` matches and
decodes only `subscription_type`, `rate_limits_available` and
`rate_limits.five_hour` / `rate_limits.seven_day` → `{utilization,
resets_at}`, ignoring every other key.

```go
type Usage struct {
    Available bool    // false: API-key/console auth; rendered "n/a"
    Plan      string  // "max", "pro", …
    FiveHour  *Window // nil when the server did not report the window
    SevenDay  *Window
    At        time.Time // when the probe started
}
type Window struct {
    Pct      float64
    ResetsAt time.Time
}
```

**Cadence.** A new `gateUsage` kind in `app/pollgate.go` (`usageInterval` =
2 min in `gateIntervals`) rides the health tick. One dispatch probes every
registered account and `default` in parallel and returns one
`usageReadyMsg`. `expedite()` fires at startup, when Launch Options or the
Accounts overlay opens, and after an account is added or logged in. With no
extra accounts, nothing is dispatched.

**Failures.** `home.usage` is a `map[account]accountUsage{last Usage, err
error}`. A failed probe records the error and keeps `last`. A never-successful
account renders `—`; a not-logged-in one (from `AuthStatus` or a probe error
saying so) renders `logged out`. Errors are logged via
`log.For("account")`.

### 5. UI

**Usage strip** (`ui/account_strip.go`). One row at the very top of the TUI,
above the workspace tab bar, present only when an extra account exists:

```
 *default  5h 64% · 7d 40%    max-2  5h 12% · 7d 31%    max-3  logged out
```

`*` marks the registry default. Percentages use severity roles (≥80% warn,
≥95% error), with styles built in a `ui.RegisterThemeHook` callback. A sample
older than two intervals renders dimmed with its age (`12% · 9m ago`); a
window whose `ResetsAt` has passed renders `reset`. At narrow widths the `7d`
column goes first, then names truncate.

Ten sites in `app/` (`app.go`, `interact.go`, `workspaces.go`) offset content
height and mouse hit-tests by `m.tabBar.Height()`. They all switch to a new
`m.topChromeHeight()` (strip + tab bar), so drag-select and pane clicks stay
aligned with the strip shown or hidden.

**Launch Options.** A new Account row (`overlay.LaunchOptions.Account`),
present only when an extra account exists, cycles ◂ ▸ through `default` and
the registered accounts, preselects the registry default (or, from `R`, the
session's account), and shows usage inline: `Account ◂ max-2 ▸  5h 12% · 7d
31%`. Changing it recomputes the blocked remote-control note for that
account.

**Card badge.** When an extra account exists, Claude sessions show their
account name (including `default`) in the rail mini-card title line, the
overview card and the agent pane title, via `CardData.Account`.

**Accounts overlay** (`ui/overlay/accounts.go`), opened from a new Accounts
row in Settings (`S`). It lists name, email, plan, 5h/7d usage with age, and
warnings (`logged out`, `not shared: settings.json`). Keys:

- `a`: prompt for a name, `Create`, then `tea.ExecProcess(LoginCmd)`. On
  return, re-run `AuthStatus` and `rcAuth` for it and expedite a probe.
- `l`: log in again, the same way.
- `enter`: make it the default.
- `x`: remove, after confirmation. Refused while any session uses the account
  ("3 sessions use max-2"), counted by `accountUsers` (section 6). `default`
  cannot be removed.

### 6. CLI

`cmd/account.go`, registered as `loom account`, mirroring `loom workspace`:

```
loom account add <name> [--no-login]   # Create + Sync, then claude auth login in this terminal
loom account login <name>
loom account list                      # name, email, plan, 5h, 7d, default marker; probes live
loom account use <name>                # set the default
loom account sync                      # re-link every account; report diverged entries
loom account remove <name> [--force]   # --force skips the confirmation and the in-use and unshared-files refusals
```

`remove` refuses while any session uses the account. The count comes from a
helper, `accountUsers(name)`, which reads the stored instances of every
registered workspace and of the global dir read-only, as `loom workspace
status` does. The TUI adds its loaded slots' live instances on top. A session
whose record is not saved yet (a start in flight) is the one case it can
miss; that session's next relaunch fails closed (section 2).

Each TUI start runs `Sync` for every account, so entries added to the main
dir later are shared without a manual `sync`. `MainDir` comes from the
default account's `claude auth status`, which startup already runs for
remote-control detection; `DetectClaudeRemoteControlAuth` is extended to
return the parsed identity alongside the state so the one subprocess serves
both.

### 7. Failure handling

| Situation | Behaviour |
|---|---|
| Account removed, session relaunches | Launch refused with a clear error; `R` to pick another account |
| Account logged out | Claude's own login prompt in the pane; `logged out` in strip and overlay |
| Probe fails or times out | Last good sample kept, dimmed with its age; logged under `subsystem=account` |
| One account's roster query fails | Only its entries cleared; its sessions fall back to hooks and the scraper ladder |
| A linked entry became a real file | Left alone; reported as `not shared: <entry>` |
| No Claude program configured | No probes, no roster queries; account UI inert |
| `accounts.json` unreadable | Treated as no extra accounts, error shown once, writes latched until a load succeeds |
| API-key account (`rate_limits_available: false`) | Usage renders `n/a` |

## Testing

- `account/`: `Sync` on temp dirs (deny-list honoured, idempotent, a diverged
  real file reported and kept, new main entries picked up); `Remove` leaves
  symlink targets intact; registry round-trip and the load-failure latch;
  `ValidName`; `ProbeUsage` parsing against a fixture captured from the real
  response above, plus malformed JSON, no matching `request_id`, missing
  windows, `rate_limits_available: false` and a timeout; `AuthStatus` parsing.
- `session/`: `InstanceEnv` sets `CLAUDE_CONFIG_DIR` only for Claude and a
  non-default account; a launch with an unresolvable account fails with the
  documented error; the v7 → v8 migration; the `workspace migrate` shape
  fixture.
- `app/`: roster fan-out and join per account, including one account failing;
  `gateUsage` cadence and `expedite` triggers; `usageReadyMsg` keeping the
  last good sample on error; `R` switching account relaunches with
  `--resume`; remove refused while in use, including by a session stored in
  a workspace that is not open; `topChromeHeight` in mouse
  hit-tests with the strip shown and hidden.
- `ui/`: strip rendering (severity, stale, `reset`, `n/a`, narrow widths);
  Launch Options with and without the Account row (the index-navigating
  overlay tests shift when the row is present); card badge.
- `cmd/`: `loom account` subcommands against a temp `LOOM_GLOBAL_DIR` and a
  fake executor.
- Opt-in contract test: `LOOM_TEST_REAL_CLAUDE=1 go test ./account -run
  TestRealClaude_UsageProbe`, which probes the installed CLI's default account
  and asserts the response shape. Costs nothing but needs a logged-in
  account.

New packages need a `TestMain` calling `testenv.IsolateLoomDirs`
(`TestEveryConfigReachingPackageIsolatesLoomDirs` enforces it).

## Documentation

- CLAUDE.md: `account/` in Key Packages; a gotcha covering the per-account
  roster, the probe's reliance on an experimental control request, the link
  deny-list and why a diverged file is left alone, and that usage is
  display-only; schema v8's `account` field under Persistent State;
  `accounts.json` and `accounts/` under Persistent State.
- USAGE.md: the `loom account` commands, the Accounts overlay and the Launch
  Options row.

## Out of scope

- Automatic account choice at launch, and failover when a session hits a
  limit (the user chose manual assignment).
- `StopFailure`/`rate_limit` hook handling.
- Lua access to accounts (`ctx:new_instance{account=…}`, `inst:account()`).
- Accounts for non-Claude agents.
- Adopting an existing, user-managed config dir.
- Per-workspace default accounts.
- Known limitations, found in review and left for later:
  - The terminal pane runs on the default account.
  - `R` on a Paused session whose tmux session is still alive (a false-dead
    pause) reattaches to it on its old account, even if another was picked.
  - An adopted orphan comes back on the default account; its live session's
    `CLAUDE_CONFIG_DIR` could be read with `tmux show-environment`.
  - Wrapper programs that are not recognized as Claude get no Account row
    and run on the default account.

## Risks

- **`get_usage` is experimental.** Its shape may change. Loom decodes only the
  documented `rate_limits` windows and fails to `—` otherwise, and the opt-in
  contract test catches a change on upgrade.
- **Shared symlinks can be broken by atomic writes.** A tool that writes a
  linked file via rename replaces the link with a real file, silently
  un-sharing it. `Sync` reports it rather than repairing it, because the new
  file may hold the only copy of a change.
- **Concurrent writers to shared files.** Several accounts' sessions append
  to shared `history.jsonl` and write in `projects/`. That already happens
  across sessions of one account today, so no new failure mode is expected.
- **User-scope MCP servers live in `.claude.json`**, which is per account. An
  extra account does not see MCP servers added with `claude mcp add --scope
  user` to the main account. Documented, not solved.
