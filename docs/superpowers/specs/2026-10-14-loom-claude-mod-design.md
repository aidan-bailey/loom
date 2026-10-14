# The loom Claude Code mod: foundation and send

**Date:** 2026-10-14
**Status:** Approved design. Brainstormed with the user.

## Problem

Loom cannot hand a message to a running Claude session safely. Every send
today (`Core.SendPrompt`: the prompt overlay, the quick bar, the workbench
review, Lua `inst:send_prompt`, the `N` flow's first prompt) pastes into the
pane with `load-buffer` + `paste-buffer` and presses Enter. If a permission
or AskUserQuestion dialog is open, the paste answers it. That hazard is why
`loom session send` was dropped from the session CLI
(`2026-10-12-session-cli-design.md`, decision 1), and scrum's SM needs a
send it can trust and a reply it can read.

Claude Code 2.1.299 ships an early-access mod API: a plugin folder whose
`hooks/hooks.json` names a TypeScript module that hooks the engine's events
and calls it through `$` (`$.prompt.submit`, `$.http.fetch`,
`$.session.*`, ...). A mod loom loads into every Claude it launches can take
messages from the daemon and queue them as prompts that never touch a
dialog.

This spec builds that foundation and the send on it. Guards, live context
and the rest come later, on the same foundation (Out of scope).

## Findings

Probed live on 2026-10-14 with a throwaway mod and real `claude` (Haiku) on
a private tmux server. The probe recipe is in
`docs/claude/testing-pitfalls.md` (Claude and tmux premises).

1. **Loading.** `--plugin-dir <dir>` and `CLAUDE_CODE_PLUGIN_DIRS` both
   load a mod with no prompt, including under a loom account's
   `CLAUDE_CONFIG_DIR`. Hooks modules wait for workspace trust
   (`hooks modules not loaded until workspace trust is accepted` in the
   debug log); loom's launch watch answers the trust dialog, and
   `session.start` fires right after. In a trusted directory, and on
   `--resume`, the mod loads at startup with the resumed session ID.
2. **A child for the session's life.** A `$.process.spawn` loop started
   un-awaited in `session.start` survived turns, dialogs, `/clear` and 8
   idle minutes; `/exit` and a killed tmux server ended it with no process
   left behind.
3. **Hot reload.** Saving a file in a `--plugin-dir` folder reloads the
   module in the running session about 2 seconds later: the old module's
   children are killed, `session.start` runs again with the same session
   ID, and module variables start over (`$.state` survives).
4. **`$.prompt.submit`.** While idle it starts a turn and leaves a
   half-typed draft in the prompt box alone. With an AskUserQuestion or a
   Bash permission dialog open it waits: the dialog stays the person's,
   and the prompt runs as a turn of its own once the open turn ends.
   Submitting the text `4` did not pick menu item 4. The model sees the
   prompt framed (`The <plugin> plugin sent a message: ... it starts this
   turn in the user's place`); `asUser: true` drops the frame.
5. **`/clear`** raises `session.end` with reason `clear` and no
   `session.start` after it; the process and the mod run on.
6. **`$.http.fetch`** takes a `socketPath` (a Unix socket of at most about
   100 bytes) and resolves only once the whole body is read: no streaming.
   No timeout is documented.
7. **The validator** (`claude plugin validate`) refuses a module that
   passes `$` to anything but a function declared at the top of the file.

Two premises are unproven and gate the plan (Spike gate).

## Decisions

| # | Decision |
|---|---|
| 1 | First spec: the foundation (ship and load the mod, a per-launch channel both ways, liveness) and send through it. |
| 2 | Mod-routed sends: `loom session send`, Lua `inst:send_prompt` and the `N` flow's first prompt. The prompt overlay, the quick bar and the workbench review keep pasting: the person is watching the pane, and a paste mid-turn steers the running turn, where the mod's prompt waits for the next. |
| 3 | A launch with no live mod: `loom session send` refuses and says why; script sends and the first prompt fall back to today's paste. |
| 4 | Framing by origin: the first prompt and script sends go `asUser` (they stand for what the person typed); `loom session send` is framed, with an optional `--from <name>` line. |
| 5 | `loom session send` returns once the message is queued; `--wait` blocks until the turn it opened ends and prints Claude's reply. |
| 6 | Transport: an HTTP endpoint the daemon serves on a second Unix socket, reached by the mod's `$.http.fetch`: `hello` and events posted, messages long-polled. (Files in the hooks folder, and a connected `loom` bridge child, were weighed and not chosen.) |

## Design

### Architecture

```
loom daemon (one per global dir)
 ├─ NDJSON socket   …/<hash>.sock       TUI, CLI (existing)
 └─ mod socket      …/<hash>.mod.sock   HTTP, new (beside it)
        ▲  POST /v1/hello, POST /v1/events
        │  GET  /v1/inbox?launch=L&after=N   (long poll)
        │
claude (each Claude launch loom makes)
 └─ the loom mod (--plugin-dir <globalDir>/mod/<content-hash>)
      env: LOOM_MOD_SOCKET, LOOM_LAUNCH
```

A send: a sender calls `Core.Deliver`; the model puts the message in the
instance's outbox; the mod's open long poll returns it; the mod calls
`$.prompt.submit` and posts `queued`, and the model replies to the sender.
When the turn starts the mod posts `started` with its turn ID, and when it
ends, `complete` with Claude's answer, which `--wait` prints.

Four parts, each with one job:

| Part | Package | Job |
|---|---|---|
| The mod | `session/claudemod` (new) | The TypeScript module and manifest, embedded; written once per content hash. |
| The mod server | `internal/modserver` (new) | The HTTP listener and handlers. Holds no state: it calls the model through `core.ModHost`. |
| Mod state | `core` | Per instance: the launch's liveness, the outbox, the waiters. |
| Senders | `core`, `internal/cli`, `app` | `Core.Deliver`, `loom session send`, Lua `send_prompt`, the first prompt. |

### The mod

**Files**, under `session/claudemod/mod/`, embedded with `go:embed`:

- `.claude-plugin/plugin.json`: name `loom`, `version` the mod protocol
  version (`1`), `types` naming the contract.
- `hooks/hooks.json`: `{ "modules": ["./register.ts"] }`.
- `hooks/register.ts`: the module.
- `types/index.d.ts`: its `$.state` contract (`submitted`, `lastSeq`).

**Placement.** At startup the daemon writes the embedded files to
`<globalDir>/mod/<first 16 hex of the files' SHA-256>/`: into a temporary
sibling, then renamed into place. A folder that already exists is never
written again, so no running session ever hot-reloads (Finding 3): a
session keeps the mod it launched with until it relaunches. Old folders are
kept (a few KiB each). A failed write is logged, and launches go without
the mod.

**Loading.** For a Claude program on a Unix host, `Instance.prepareHooks`,
which already adds `--settings`, also adds `--plugin-dir <folder>`, and
`InstanceEnv` adds `LOOM_MOD_SOCKET` (the path the daemon listens on) and
`LOOM_LAUNCH` (the launch ID `hooks.Prepare` minted). A launch that skips
the hooks (not Claude, Windows, no config dir, a single quote in the path,
`--settings` already given) gets no mod either and is absent from the
start.

**Behaviour.** The module is thin: composition and policy live in Go.

1. `session.start` reads `LOOM_MOD_SOCKET` and `LOOM_LAUNCH` through
   `$.env.get`. Without both it does nothing more. Otherwise it starts the
   poll loop un-awaited and returns `next(e)`.
2. The poll loop posts `hello` (launch, mod version, `$.session.version()`,
   `$.session.id()`), then repeats `GET /v1/inbox?launch=L&after=<lastSeq>`.
   A 204 polls again. A failed fetch (the daemon restarting) waits 1 s,
   doubling to at most 5 s, and says `hello` again once a fetch succeeds. A
   404 (the launch is unknown or superseded) ends the loop for good.
3. For each message, in `seq` order: one whose ID is in `$.state`'s
   `submitted` is skipped (a reload never submits twice). Otherwise it
   calls `$.prompt.submit({ text, asUser })`, records the ID and `lastSeq`,
   and posts `queued`. A submit that rejects posts `failed{id, error}`.
4. Turn correlation: the mod keeps a FIFO of submitted messages awaiting
   their turn. The next main-loop `turn.start` (no `agentId`) whose `text`
   matches the head's submitted text (equal to it, or containing it if
   Claude wraps it; the spike settles which) claims it and posts `started{id, turnId}`; a main-loop
   `turn.complete` of a claimed turn posts `complete{id, turnId, text,
   reason}`. Subagent turns and the person's own turns post nothing. If the
   spike shows `turn.start.text` does not identify the message, the rule is
   order alone: `$.prompt.submit` resolves as its turn starts, so the first
   main-loop `turn.start` after it resolves is that message's.
5. `/clear` changes nothing (Finding 5). Exit unloads the module, which
   ends the loop.

It calls only `$.env`, `$.http`, `$.prompt`, `$.session` and `$.state`:
no `$.process`, no `$.fs`, no UI. It gates nothing (no `tool.call` or
`prompt.submit` hook), so a hook that fails is skipped and nothing else.

### The mod server

`internal/daemon.SocketPath` gains a sibling, the mod socket: the main
socket's path with its `.sock` suffix replaced by `.mod.sock`
(`<hash>.mod.sock`, or `run/serve.mod.sock`), in the same private (0700)
directory, under the same 100-byte check. Only the daemon computes it, and
launches learn it from `LOOM_MOD_SOCKET`. The path is settled before the boot, since the boot relaunches
agents whose environment names it, and the server listens after the boot;
a mod that dials first retries. A path that doesn't fit is not fatal, as
the main socket's is: the daemon logs it and every launch goes without the
mod.

All requests are `http://loom/v1/...` over the socket, JSON bodies capped at
1 MiB:

| Request | Answer |
|---|---|
| `POST /v1/hello {launch, mod, claude, session}` | 204; 404 for a launch no instance holds now. |
| `GET /v1/inbox?launch=L&after=N` | 200 `{messages: [{seq, id, text, asUser}]}` with every pending message after `N`; with none, held up to the hold time (25 s unless the spike says otherwise), then 204. A relaunch ends a held poll with 404; a daemon stop, with 503. For one launch the newest poll wins: an older one still held gets 204. |
| `POST /v1/events {launch, events: [{kind, id, turnId?, text?, reason?, error?}]}` | 204; 404 for an unknown launch. `kind` is `queued`, `started`, `complete` or `failed`. |

The server reaches the model through `core.ModHost`, which `*core.Loop`
implements: `ModHello(launch, info) error`, `ModInbox(launch, after)
([]ModMessage, <-chan struct{}, error)` (the messages, or a channel closed
when there is something new or the launch changes) and `ModEvents(launch,
events) error`. These are calls inside the daemon, not `Core` wire methods:
nothing is generated for them. On a stop the mod server closes with the
main socket, after the bye and before the quiesce.

### Mod state in the model

Per instance, for its current launch:

- **Identity.** The launch the model trusts is the instance's
  `hookLaunchID` (a new accessor). A request naming any other launch gets
  404. A restored instance adopts the launch ID its hooks scan reads, so a
  mod that reconnects after a daemon restart maps straight back.
- **Liveness.** `pending` from the launch until `hello`; `live` after it;
  `absent` when the launch got no mod, or when the classic `SessionStart`
  event of this launch has been seen and no `hello` came within the grace
  (10 s after the later of that event and the daemon's boot). A `hello`
  always makes a launch `live`, even an absent one: a mod's reconnect can
  come after the grace when the daemon restarted.
- **Outbox.** Messages with a per-instance `seq` and an ID. `queued`
  removes a message and sends its `Reply`; `complete` sends a waiting
  sender's `Answered`; `failed` refuses the message, or pastes it for a
  script or first-prompt origin.
- **Leaving.** When the instance stops running (pause, kill, crash) or
  relaunches, its pending messages are refused, its waiters get a `Notice`
  with the error, and its liveness starts over. When a launch turns
  `absent`, CLI messages are refused and script and first-prompt messages
  are pasted.

Nothing here is saved. A daemon restart drops pending messages; their CLI
senders see "lost the loom daemon", so none is lost silently.

### Senders

**`Core.Deliver(id InstanceID, d Delivery, req ReqID)`**, a request.
`Delivery` holds `Text`, `From`, `Wait` and `Origin` (`cli`, `script`, or
`first-prompt`, which only the daemon sets). The origin decides the policy,
in one place:

| Origin | Text the mod submits | `asUser` | When the launch is absent |
|---|---|---|---|
| `cli` | `From <from>:` and a blank line before the text when `From` is set, else the text | no | refused |
| `script` | the text | yes | pasted (`SendPrompt`'s path) |
| `first-prompt` | the text | yes | pasted |

It admits as `SendPrompt` does (refused for a session not started or
Paused). The `Reply` comes on `queued`, or with `Err` for a refusal. With
`Wait`, a new event `Answered{Req, Text, Reason}` goes to the requesting
client alone (the `forConn` route); a session that stops mid-turn gets the
requester a `Notice` with `Err` instead. Adding a method doesn't bump
`rpc.Protocol`: an older daemon answers `CodeUnsupported`, which the CLI
reports as "the loom daemon is too old for send: upgrade it".

**`loom session send <title> [-w <ws>] [--text <t> | --file <f>] [--from <name>] [--wait]`**
resolves the session as the other session verbs do (`internal/cli`) and
reads stdin when neither `--text` nor `--file` is given. Exit 0 once queued;
with `--wait`, once the turn ends, with Claude's reply on stdout. Exit 1
with the reason on stderr for a refusal, an absent mod, a session that
stopped, or a lost daemon. Ctrl-C under `--wait` exits 1, saying the
message was delivered and its reply will not be printed.

**Lua `inst:send_prompt(text)`** calls `Deliver` with origin `script`: it
now resumes once the message is queued, not once a paste lands. The TUI
holds input to the session until the `Reply`, as today: normally about a
second, longer for a session still starting.

**The first prompt.** `sendInitialPrompt` delivers through the mod when the
launch offered it, and the start completion publishes `Started` after
`queued`, or after the fallback paste once the launch turns absent. So
`loom session new --prompt` still returns once the prompt is in, and the
prompt can no longer race the trust dialog: the mod loads after trust.

**Unchanged:** the prompt overlay, the quick bar and the workbench review
call `SendPrompt`, which pastes.

## Failure handling

- **The mod** backs off on any failed fetch (1 s doubling to 5 s, so a
  re-`hello` lands inside the grace), stops on 404, and reports a submit it
  could not make as `failed`. It logs only to Claude's debug log
  (`$.ui.log` with `to: "debug"`).
- **A Claude update that breaks the mod.** If the module no longer loads,
  no `hello` comes, the launch turns absent, the CLI refuses and scripts
  and first prompts paste: today's behaviour, reached by failing closed.
  If it loads but cannot submit, `failed` covers it.
- **The mod server** logs through `log.For("modserver")`; malformed or
  oversized bodies get 400.

## Compatibility

- `state.json`: unchanged.
- The wire: `Deliver` and `Answered`, generated (`go generate ./core/rpc`,
  `go test ./core/rpc -run TestProtocolReference -update`); no protocol
  bump.
- The mod's HTTP API is versioned (`/v1`); both sides ignore unknown
  fields, so a mod from an older build works with a newer daemon.
- The flag leaves a user's own `CLAUDE_CODE_PLUGIN_DIRS` alone.
- Windows: no mod. The server lives in `_unix.go` files with a Windows stub;
  cross-build with `GOOS=windows CGO_ENABLED=0 go build ./...`.

## Security

The mod socket is 0600 in the daemon's private 0700 directory: the same
same-user boundary as the main socket, which already grants full control.
The launch ID binds a request to one instance's outbox; it is not a secret
from the same user. The mod's calls are limited as listed above, and a test
pins that list.

## Spike gate

The plan's first step probes, with the throwaway-mod recipe:

1. **Long poll.** How long a `$.http.fetch` over a Unix socket can be held
   from the un-awaited loop, and what the host's timeout is. Sets the hold
   time.
2. **Turn text.** What `turn.start.text` holds for a plugin-submitted
   prompt (bare or framed). Sets the correlation rule.
3. **Back to back.** Whether two prompts submitted during one turn become
   two turns, in order.

A premise that fails sends the design back to the user before any code.

## Testing

- **Go, `internal/modserver`:** each handler against a fake `ModHost`, over
  a real socket at a short temporary path; caps, 404s, the newest poll
  winning, a held poll ended by a relaunch and by a stop.
- **Go, `core`:** liveness transitions (pending to live, to absent at the
  grace, absent back to live on a late `hello`), the outbox (seq, `after`,
  acknowledgement, refusal on pause, kill, crash and relaunch), each
  origin's policy and fallback, `Answered` reaching only the requester, the
  first prompt's `Started` after `queued` or the fallback.
- **Go, `session`:** launch composition (`--plugin-dir` and both variables
  for a Claude launch; none for another program, Windows or a skipped hooks
  folder); the embedded files present and the content-hash folder written
  once, atomically.
- **Go, `internal/cli`:** `send`'s flags, stdin, exit codes and `--wait`,
  against a test daemon as the other session verbs are tested.
- **Wire:** `TestGenerated_IsFresh`, `TestProtocolReference`.
- **e2e (`-tags e2e`):** a fake mod (a Go HTTP client) across a daemon stop
  and restart: it reconnects, says `hello` again, and a message sent after
  the restart is delivered.
- **Opt-in, `LOOM_TEST_REAL_CLAUDE=1`** (CI has no `claude`):
  `claude plugin validate` on the embedded mod, its call list pinned;
  `claude plugin test` with `*.test.ts` beside the module; and a real-Claude
  contract test on a private tmux server: `hello`, `Deliver`, `queued`,
  `started`, `complete`, and a delivery during an open dialog.

## Documentation

- New `CLAUDE.md` files: `session/claudemod` (never rewrite a content-hash
  folder; keep the module thin and its call list pinned) and
  `internal/modserver` (no model objects; reach the model only through
  `core.ModHost`).
- `internal/daemon/CLAUDE.md` and `README.md`: the mod socket's place,
  lifetime and non-fatal failure.
- `USAGE.md`: `loom session send`.
- `docs/specs/scripting.md`: `inst:send_prompt`'s new semantics.
- `docs/ARCHITECTURE.md`: the mod, its socket and the env it gets.
- The generated protocol reference.

## Out of scope

Each is a later spec on this foundation:

- `tool.call` guards for loom's worktree rules.
- Live context through `prompt.compose`, replacing
  `--append-system-prompt-file`.
- Pushed usage (`session.measure`); loom already probes plan usage every 2
  minutes.
- `/loom` slash commands, a band above the prompt, model-facing tools for
  scrum.
- Routing the interactive sends (overlay, quick bar, workbench) through the
  mod.
- Persisting the outbox across daemon restarts; pruning old mod folders.
- Status from the mod; the classic hooks folder stays the status source.

## Risks

- **The API is early access** and may change between releases without
  notice. Failing closed (absent, then today's behaviour) bounds the damage
  to send, which refuses visibly.
- **A Claude too old for `--plugin-dir`** would fail to launch. The flag
  has shipped for about a year; accepted.
- **The grace is a timer.** A mod slower than 10 s to say `hello` after
  `SessionStart` reads as absent until it does; a late `hello` recovers it.
