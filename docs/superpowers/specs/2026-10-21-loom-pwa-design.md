# Loom PWA: the fleet from an iPhone

**Date:** 2026-10-21
**Status:** Approved design. Brainstormed with the user. The architecture
figure and the screen mockups are on a private page:
https://claude.ai/artifact/7F1PAfjvSJ3yhiNsfStreX

## Problem

Loom's sessions keep running on fresco when nobody is at the desk, but
seeing them or acting on them needs a terminal there: the TUI, or the
`loom session` commands. Away from the desk, Claude's Remote Control
reaches one session at a time (loom launches every Claude with
`--remote-control <title>` unless the setting turns it off), but nothing
shows the fleet: which sessions wait on you and why, which finished, what
each said last. Nothing starts, pauses, resumes, kills, pushes or merges
one.

This spec adds an installable web app (a PWA) for an iPhone, served by the
daemon and reached through a Cloudflare tunnel. It shows the fleet across
every workspace, why a session waits and what it said last; it replies
through the loom mod, runs the lifecycle actions, pushes a notification
when a session starts waiting or finishes, and hands off to the session's
Remote Control for approvals and the full chat.

It reverses one non-goal of `2026-10-03-loom-daemon-design.md` (remote
access, a network transport, authentication beyond filesystem
permissions), opt-in and behind device pairing.

## Findings

Checked on fresco on 2026-10-21, with Claude Code 2.1.306.

- Claude writes `<config dir>/sessions/<pid>.json` for each interactive
  session: `cwd`, `sessionId`, `status`, `tmux` and, while Remote Control
  is connected, `bridgeSessionId` (`session_01…`), the ID in the session's
  `https://claude.ai/code/<id>` link. Loom's roster
  (`session/claude_roster.go`) runs `claude agents --json`, whose
  interactive entries carry `pid`, `sessionId`, `cwd` and `status` but not
  the bridge ID, so the bridge ID is only in that file.
- A `PermissionRequest` hook payload carries the tool's input, but
  `hooks.Event` keeps only `ToolName` (`session/hooks/event.go`), so loom's
  wait reason reads "permission: Bash" without the command.
- The existing tunnel runs on medesco (`~/System/modules/cloudflared.nix`,
  in `htpcModules` only), as the system user `cloudflared`, and reaches
  fresco over the LAN. Fresco has only the `cloudflared` client its SSH
  config uses.
- `rpc.Server.Serve` and `rpc.Dial` take any `io.ReadWriteCloser`, so a
  client in the daemon's own process can dial the daemon's server over
  `net.Pipe`, as `rpc.InProcess` does in tests.
- The server keeps a `Notice` for the next client only while no
  connection exists (`core/rpc/server.go`), so a client that is always
  connected would take the notices meant for the next TUI.
- The daemon already serves HTTP: `internal/modserver`, on the private mod
  socket, reaching the model through `core.ModHost`.
- `Core.Merge(target, source)` merges source's branch into target's
  worktree, and `Core.Push` commits the worktree before pushing.

## Decisions

| # | Decision |
|---|---|
| 1 | **Job:** control from a phone, away from the desk: the fleet, why each session waits, last messages, a reply, the lifecycle actions. No terminal. |
| 2 | **Phone:** iPhone, with the app installed to the Home Screen. iOS sends Web Push only to Home Screen apps (iOS 16.4+) and keeps their storage apart from Safari's. |
| 3 | **Reach:** a second Cloudflare tunnel, run by a cloudflared service on fresco, to loom on a loopback port. medesco's tunnel is unchanged. |
| 4 | **Auth:** device pairing. `loom web pair` prints a one-time code; the phone trades it for a revocable per-device token that loom checks on every request. |
| 5 | **Answering:** a reply box that sends through the loom mod (`Core.Send`, queued behind the turn), and Open in Claude, a link to the session's Remote Control for approvals and the full chat. Loom answers no permission prompt itself. |
| 6 | **Push:** when a session starts waiting on you, and when its turn ends. |
| 7 | **Actions:** send, new session, pause, resume, kill, push, merge. Kill, push and merge ask first. |
| 8 | **Shape:** inside the daemon, opt-in. `internal/web` is a `core.Core` client over `net.Pipe` to the daemon's own `rpc.Server`, plus `core.WebHost` for device records. A separate service and a hub were weighed (Alternatives). |
| 9 | **Fleet:** grouped by what needs you, across workspaces: Needs you, Your turn, Working, Paused, with chips to filter by workspace. |
| 10 | **The app has no build step:** ES modules and one vendored Preact + htm file, embedded in the binary. |
| 11 | **Settings:** the web settings live in `web.json`, set by `loom web enable` and applied at once, not in a block of `config.json` read at the daemon's start: `config.Settings` is per workspace, and a hand edit of a file the model writes races its saves. |
| 12 | **Listener:** loopback TCP only. No unix socket: cloudflared runs as its own user and could not reach a private one. |

## Architecture

```
iPhone: the installed app and its service worker
  │ HTTPS to the configured origin
Cloudflare edge ── tunnel ──> cloudflared on fresco (its own system user)
  │ http://127.0.0.1:<port>
loom serve
  internal/web ── rpc over net.Pipe ──> rpc.Server <── TUIs and CLI (private socket)
       │                                    │
       └── core.WebHost ──────────> core.Model on core.Loop (state.json, web.json)
                                            │ queued sends
  modserver <── polls ── the loom mod in each Claude session
Push:           internal/web ──> Apple's Web Push service ──> the iPhone
Open in Claude: the iPhone ──> claude.ai/code/<bridge id> ──> the session
```

### `internal/web`

- `daemon.Serve` starts it after `modserver.Listen` and stops it first
  when `Options.Stop` closes, before `Server.Bye`.
- It dials the daemon's server over `net.Pipe` with the hello's `role` set
  to `web`, drains its client as the TUI does (`Wakes`, then `Sync`), and
  keeps one projection: each workspace's sessions as display-ready JSON.
- It watches the published `WebView`. When `listen` or `origin` changes it
  closes its listener, which ends every stream, and listens again; an
  empty `listen` means off. It reports what happened through
  `WebHost.WebServing`.
- It holds no session state. The projection derives from its replica, and
  device records live in the model.
- A panic in its goroutines (the drain, the stream hub, the push sender)
  stops the web layer and keeps a notice for the next TUI; the daemon goes
  on. Go's HTTP server recovers panics in handlers itself.
- It logs under `subsystem=web`.

### `web.json`

In the global dir, mode 0600, written by the model only, as `accounts.json`
is:
- `version`, `listen`, `origin`;
- the VAPID key pair (P-256), made when the web is first enabled;
- each device: name, the SHA-256 of its token, when it was paired and last
  seen, its notification settings (`waiting`, `finished`, `paused_until`),
  its push subscription (endpoint, `p256dh`, `auth`) and that
  subscription's state (`ok`, `expired`, or `failing` with the status).

Pairing codes live in the model's memory only, so a daemon restart voids
them. A `web.json` that fails to load, or names a newer `version` than the
build knows, is never overwritten: the web stays off and a notice says
why.

### Changes outside `internal/web`

- **core:**
  - Requests `ConfigureWeb(listen, origin string) error`, which validates
    (below) and writes, with an empty `listen` disabling; `PairDevice(name
    string) (Pairing, error)`, where `Pairing` is the code and its expiry;
    and `RevokeDevice(name string) error`.
  - The query `Web() WebView` (`rpc:local`): the settings, whether the
    listener serves and why not, and each device without its token hash.
    It travels in `ModelView` (published as `ModelChanged`), so no new
    event is needed.
  - `core.WebHost`, implemented by `*core.Loop` and never on the wire:
    redeeming a code, finding a device by token hash, touching last seen,
    setting a device's notifications and push subscription, the push
    targets, recording a push result, the VAPID key, and `WebServing`.
  - `OriginWeb` for `Message.Origin`, with the CLI's rules (refused without
    a live mod, for an empty message, or for a program other than Claude)
    and no "From" line.
- **core/rpc:** an optional hello field, `role`. A connection whose role
  is `web` doesn't count when the server decides whether a notice reached
  a client, so a notice raised while only the web layer is connected also
  waits in the backlog for the next TUI. An optional field needs no
  `Protocol` bump.
- **session:**
  - The roster query keeps each interactive entry's `pid` and reads
    `<config dir>/sessions/<pid>.json` beside it, in the dir the query ran
    under: the account's `CLAUDE_CONFIG_DIR`, else the daemon's own
    `CLAUDE_CONFIG_DIR`, else `~/.claude`. It takes `bridgeSessionId` only
    when that file's `sessionId` equals the entry's (a reused pid names
    another session), and fails closed: a missing, unreadable, malformed or
    oversized file gives no bridge ID. The model reads it from its roster
    when it builds a view, so `InstanceView.RemoteSession` clears when the
    roster does, as the roster's status does.
  - `hooks.Event` keeps a `ToolSummary` for `PermissionRequest`: Bash's
    `command` (its first line); the `file_path` of Edit, Write and
    MultiEdit; NotebookEdit's `notebook_path`; WebFetch's `url`; empty for
    other tools; at most 200 characters, cut with "…". The compact event
    form keeps it. The instance keeps it beside the wait reason, set and
    cleared with it, and `InstanceView.WaitDetail` publishes it.
- **internal/cli:** `loom web`, a daemon client through `cli.Join` like
  `loom account`:
  - `enable --listen 127.0.0.1:<port> --origin https://<host>` and
    `disable`;
  - `pair [--name <device>]`, which prints the code, its expiry and the
    origin;
  - `devices`: name, paired, last seen, notifications, push state;
  - `revoke <name>`, or `--all`;
  - `status`: the settings, whether it serves or why not, the device count.

### Validation

- `listen`: a loopback IP literal and a port, `127.0.0.1:<port>` or
  `[::1]:<port>`. Any other address, a host name such as `localhost`, and
  a unix socket are refused.
- `origin`: `https://<host>[:<port>]` with no path, query or fragment, or
  for development `http://127.0.0.1:<port>` or `http://localhost:<port>`.
- A refused value fails the request. A stored value that fails to listen
  later (the port is taken) leaves the web off, sets the reason in
  `WebView`, and keeps a notice for the next TUI.

## Pairing and auth

Pairing:
1. `loom web pair --name iPhone` sends `PairDevice`. The model refuses a
   name a device already has, and makes a code of 8 characters from
   `23456789ABCDEFGHJKMNPQRSTUVWXYZ` (shown as `K7QM-2XPA`), bound to that
   name. It works once, for 10 minutes. A second `pair` for the same name
   replaces its pending code; at most 4 codes wait at once, and a fifth
   voids the oldest.
2. The phone posts the code alone, and the device takes the name the code
   was made for. A code matches ignoring case, dashes and spaces. On a
   match the model consumes the code, makes the device with a 32-byte
   random token, stores the token's SHA-256, and returns the name and the
   token once. The web layer sets the token as the cookie `__Host-loom`:
   Secure, HttpOnly, SameSite=Strict, Path=/, Max-Age 400 days.
3. Five wrong codes since the last `pair` void every pending code. The app
   says to run `loom web pair` again.

Every request:
- The `Host` must equal the origin's host and port, which defeats DNS
  rebinding; otherwise 421.
- A request other than GET or HEAD must carry an `Origin` equal to the
  origin; otherwise 403.
- The app shell and its static files are public. Every route under
  `/api/v1` except `/pair` needs the cookie, whose SHA-256 must name a
  device; otherwise 401, on which the app clears its cached snapshot and
  shows pairing.
- The stream sets the cookie again when it connects, so a device in use
  never expires. Last seen is written at most once a minute per device.
- Revoking a device ends its streams as soon as the device list is
  published, and deletes its push subscription.
- Headers: `Content-Security-Policy: default-src 'self'; img-src 'self'
  data:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'`,
  `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`. The
  markup has no inline script and no style attributes.
- Bodies are capped at 1 MiB, the mod socket's own cap. The server sets a
  10 s read-header timeout and a 120 s idle timeout; the stream has no
  write timeout and pings instead.
- Every action is logged with its device, action, session and result.

Cloudflare terminates TLS at its edge, and cloudflared reaches loom over
plain HTTP on loopback, so Cloudflare can read the traffic, token
included. The user accepted this; revocable tokens limit the damage, and
Cloudflare Access could later sit in front with no loom code.

## Limits

Every outside service, and every value a user or another program
supplies, has a bound.

- **Push services:** a 10 s timeout per push, at most 30 pushes a minute
  per device (the rest are dropped and logged), and failures as Push says.
- **Claude's session files:** read only beside a roster entry, at most
  64 KiB each; anything else gives no bridge ID.
- **Requests:** bodies of at most 1 MiB. At most 4 open streams per device,
  where a fifth closes that device's oldest, and 32 in all, where a 33rd is
  refused with 503.
- **Pairing:** a code is 8 characters of the alphabet once dashes and
  spaces are dropped, in any case; at most 4 codes waiting at once; 5
  wrong codes void every pending one; at most 16 devices.
- **Device names:** 1 to 32 characters after trimming, no control
  characters, unique.
- **Replies and prompts:** a reply of 1 byte to 64 KiB after trimming; a
  new session's prompt of at most 64 KiB, and its title as
  `core.CheckNewTitle` says.
- **Launch overrides:** an account the registry holds, a model in
  `config.ClaudeModelAliases()`, a permission mode in
  `config.ClaudePermissionModes` and an effort in `config.ClaudeEfforts`,
  as `loom session new` checks its flags.
- **Notification pause:** an hour from now, or none.
- **Push subscriptions:** an `https` endpoint of at most 2 KiB whose host
  is `web.push.apple.com`, `fcm.googleapis.com` or
  `updates.push.services.mozilla.com`, or ends in `.notify.windows.com`.
  The daemon posts to it, so no other host is taken. `p256dh` is a 65-byte
  uncompressed P-256 point and `auth` 16 bytes, both base64url.
- **IDs in paths and queries:** decimal, and served by the model. An
  unknown session is 404, an unknown workspace 400.
- **Settings:** a port from 1 to 65535, and an origin of at most 255
  bytes.

## API

| Route (`/api/v1` unless shown) | Does |
|---|---|
| `GET /` and the static files | The app shell: public, with no data in it. |
| `POST /pair` | `{code}`; sets the device cookie and answers the device's name. |
| `GET /stream` | The live stream (below). |
| `POST /sessions` | Create from `{workspace, title, prompt}`, with optional `account`, `model`, `permission_mode` and `effort` overriding the defaults. |
| `GET /launch-defaults?workspace=<id>` | What the new-session sheet pre-fills, and the account names. |
| `POST /sessions/{id}/send` | `{text}`: `Send` with `OriginWeb`. |
| `POST /sessions/{id}/pause`, `resume`, `kill`, `push` | The request of the same name. |
| `POST /sessions/{id}/merge` | `{source}`: `Merge` with the session in the URL as the target, as the TUI's picker does. |
| `PUT /device` | Notification settings, and pausing them for an hour. |
| `POST /device/push` | Stores the push subscription, in the shape the browser's `PushSubscription.toJSON()` gives. |
| `DELETE /device/push` | Drops this phone's push subscription, when the app finds it has none. |
| `GET /push-key` | The VAPID public key the app subscribes with. |
| `DELETE /device` | Unpairs this phone. |

**Responses** are JSON. An error is `{"error": "<text>"}`, with the status
Requests gives. An action answers `{"notices": [...]}`; Create answers
`{"id": <new id>, "notices": [...]}` with 201; a request still running
after 30 s answers `{"pending": true}` with 202. A notice is
`{"text", "error", "at"}`.

**Sessions as JSON:** `id`, `workspace`, `title`, `branch`, `group`, the
loom `status` and `since`, `wait` (`reason`, `detail`), `last_message`,
`diff` (`added`, `removed`), `ahead` and `behind` when known, `pr` (number,
state, checks) when there is one, `account`, `agents` (running subagents),
and `remote_url` when there is a bridge ID. IDs are JSON numbers: they fit
in 53 bits, exact in JavaScript. Workspaces are `id`, `name` and `label`.

**Groups:** Prompting is Needs you; a started Ready is Your turn; Running,
Loading and Deleting are Working; Paused, Recoverable and an unstarted
Ready are Paused, each with its own label. Workspace terminals are left
out.

**Requests:** a handler takes the web client's next request ID, makes the
request, and waits for the `Reply` the drain routes back.
- Success is 200. A refusal is 409 with the model's text: `admit` stays
  the only authority, and the app's buttons only follow status. A request
  refused as unavailable (the daemon is stopping, `TakeRefused`) is 503.
- After 30 s without a `Reply` the answer is 202, and the outcome arrives
  on the stream.
- Create checks the workspace and the title as `loom session new` does
  (`core.CheckRoom`, `core.CheckWritable`, `core.CheckNewTitle`, and a
  title the workspace already holds is refused), resolves the launch
  options as it does (`launch.FromSettings`, then
  `core.ChooseLaunchAccount`), and answers on the `Reply` with the new ID.
  The start plays out on the stream.
- A request's notices come back in its response. One that arrives after
  the response, such as a failed start's, goes to the requesting device's
  stream. Kill's and Resume's stash notices name a SHA that may be the only
  record of the work, so the app always shows them.

## The stream

`GET /api/v1/stream`, as server-sent events:
- `snapshot`: the daemon's build, the host name, workspaces, sessions, the
  device (name, notification settings, push state), the badge count, and
  the last 20 notices, which the web layer keeps for the app's list;
- `sessions`: one workspace's whole list when it changes, at most four a
  second;
- `workspaces`, `device`, `notice`;
- `ping` every 20 s, since Cloudflare closes an idle stream (after 125 s in
  the spike);
- `bye` when the daemon stops.

Every connect starts with a snapshot of a few KB, so nothing is replayed.

## The app

- Files in `internal/web/static/`, embedded with `go:embed`: `index.html`,
  `app.css`, the app's ES modules under `js/` with the `.mjs` extension
  (Node 22's `node --check` misses syntax errors in a `.js` module and
  catches them in a `.mjs` one), `sw.js`, `manifest.webmanifest`, icons at
  180, 192 and 512 px drawn by a small Go tool, and one vendored Preact +
  htm module: htm 3.1.1's `preact/standalone.module.js`, pinned by its
  SHA-256 in a test, with its version and source URL in a header comment
  and its licence in `NOTICE.md`.
- The server writes the build ID and the shell's file list into `sw.js`
  when it serves it, so the service worker's precache list can't drift
  from the embedded files.
- Screens, as in the mockups: the fleet, a session, new session, the
  confirmation sheets, pairing, the Add to Home Screen steps, and settings
  (notifications, pause for an hour, unpair).
- The fleet: Needs you, longest wait first; Your turn, with a dot when the
  turn ended after this phone last opened the session (the app keeps that
  time per session); Working; Paused, folded away. The phone remembers its
  workspace filter.
- A session leads with why it waits, its tool summary and Open in Claude,
  then its last message, its changes, branch, PR and account, the actions,
  and the reply box. Open in Claude shows only with a `remote_url`;
  otherwise the card says Remote Control is off for that session.
- The confirmation sheets name exactly what happens: Kill names the
  worktree, tmux session and branch it removes; Push says it commits
  first; Merge asks which session's branch to merge in. Pause and resume
  don't ask.
- Opened in Safari rather than from the Home Screen, the app shows the
  Add to Home Screen steps instead of pairing.
- The last snapshot is kept in IndexedDB and drawn at once, marked stale
  with every action off until the stream connects; a 401 or unpairing
  clears it. The service worker caches the shell only, never the API. A
  snapshot whose build differs from the shell's makes the worker update
  and the app reload, so a loom upgrade upgrades the phone.
- The app reconnects the stream whenever it becomes visible again, and
  whenever no event has arrived for 40 s (two pings): in the spike, a page
  on iOS sat on a stream that delivered nothing.
- iOS details: `display: standalone`, `apple-touch-icon`, `viewport-fit=cover`
  and the safe-area insets.
- loom's colors, light or dark with the phone; amber marks only what needs
  you, as in the TUI.

## Push

- Turned on from a tap in the app, as iOS requires: the app subscribes with
  the VAPID public key and posts the subscription. The button shows that it
  is waiting and takes no more taps until `subscribe()` settles, which took
  iOS over two minutes after an unsubscribe. A `subscribe()` refused because
  a subscription made with another key exists (after `web.json` is reset)
  is tried once more after unsubscribing.
- Apple answers 201 to a subscription the app has dropped, and drops the
  push, so a 404 or 410 can't be relied on to find one. On each launch the
  app posts its current subscription again, which changes nothing when it
  is the same, or, having none while loom holds one, sends
  `DELETE /device/push` and shows "Notifications stopped; turn them on
  again".
- Triggers, worked out in the web layer from its projection's transitions:
  - **waiting:** a session enters Prompting, or stays in it with a new
    reason or tool summary;
  - **finished:** a started session goes from Running to Ready.
  - The first projection after the web layer starts is a baseline and
    pushes nothing, so a daemon restart sends no flood. Workspace
    terminals never push.
- A device gets the kinds its settings allow, and nothing while paused.
- The payload (kind, session ID, title, body, badge count) is encrypted
  per RFC 8291 (aes128gcm) with the standard library's crypto, so Apple
  sees only ciphertext. The VAPID JWT is ES256, with the configured origin
  as `sub`. Waiting is sent with `Urgency: high`, finished with `normal`,
  both with a one-hour TTL and a `Topic` per session, so an offline phone
  gets only each session's latest. The `Topic` is the session ID's 8 bytes,
  big-endian, as unpadded base64url (11 characters): Apple refuses a
  `Topic` whose length is 1 mod 4 with 400 `BadWebPushTopic`, which RFC
  8030 allows, so `s<id>` fails for some IDs.
- The service worker shows each notification tagged by session, so a
  session shows at most one, its latest, and sets the Home Screen badge to
  the count of sessions that need you. Tapping one opens that session.
  iOS requires every push to show something, so loom can't clear a stale
  notification; tapping it shows the current state.
- A sender goroutine in the web layer, never on the loop, posts each push
  with a 10 s timeout. It retries a 429 or a 5xx up to three times,
  honouring `Retry-After`; it drops a subscription answered 404 or 410 and
  records it as expired; any other failure records the subscription as
  failing, with the status. Both show in `loom web devices` and in the app
  ("Notifications stopped; turn them on again"). Its queue holds at most
  100 pushes and logs any it drops.

## Failure modes

The web part fails closed, and the daemon keeps running.
- Bad or unusable settings: as Validation says.
- A panic in the web layer: it stops with a notice (`internal/web`).
- The daemon stopping: streams get `bye`, and the app shows "loom is
  restarting" and reconnects with backoff from 1 s to 30 s.
- No daemon: the tunnel answers 502, and the app shows "Can't reach loom on
  fresco" with when it last updated.
- A stale view: every action is off until the stream is back.

## Testing

- Go tests over the production path: a booted model, the real
  `rpc.Server`, a client over `net.Pipe`, and the web handler on
  `httptest`.
  - Auth, testing both sides of each guard: the right and a wrong `Host`;
    the same and a cross `Origin`; a code used once, twice, after expiry,
    and after five misses; a valid and a revoked cookie, where revoking
    ends the open stream.
  - API: each route makes its `Core` request; a refusal answers 409 with
    the model's text and a stopping daemon 503; notices come back in the
    response, or on the device's stream after it.
  - The stream: a snapshot, then changes, then pings.
  - Groups: each status lands in its group, and workspace terminals are
    left out.
  - Push triggers: each transition pushes once; the first projection and a
    republished unchanged state push nothing.
  - Push crypto: RFC 8291's own example, and the VAPID JWT verified with
    its public key.
  - The push request: a session's `Topic` never has a length of 1 mod 4,
    for IDs of every size, and the request refuses a `Topic` that does.
  - The sender: 404 and 410 drop the subscription; a 429 honours
    `Retry-After`, on a fake clock.
  - rpc: a notice raised while only a web client is connected waits for
    the next TUI, and a TUI connection still receives it.
  - session: the roster's `bridgeSessionId`; the tool summary for each
    tool kind, and its truncation.
  - Validation: each accepted and each refused `listen` and `origin`.
  - The static files: everything `sw.js` precaches exists in the embedded
    files, and the manifest parses.
- e2e (`-tags e2e`): a sandbox daemon with the web enabled pairs a device,
  opens the stream, and creates, pauses, resumes and kills a fakeagent
  session over HTTP.
- The JavaScript has no test harness; the logic stays in Go. The plan
  carries a manual checklist for the iPhone: install, pair, turn on
  notifications, receive both kinds, Open in Claude, reply, kill with
  confirmation, and revoke.
- Repo rules: `internal/web` gets its own `CLAUDE.md` and `README.md` (the
  coverage lint requires one), its `TestMain` isolates loom's dirs, and
  the Windows build is checked (`GOOS=windows CGO_ENABLED=0 go build ./...`).

## Spike, as a gate

The owner runs it before `/flow-supervise`, outside the plan's stories,
with the fresco tunnel in place (the owner's change in `~/System`) and
throwaway files outside the repo:
1. Serve a minimal installable page through the tunnel, install it on the
   iPhone, subscribe, and send a push with a standard Web Push tool (such
   as the `web-push` CLI) using a VAPID key, aes128gcm and the configured
   origin as the JWT's `sub`. Apple must answer 201 and the phone must show
   it, with the app open and closed. Record Apple's answers to a bad JWT
   and an expired subscription. The plan's own Go code is then checked by
   RFC 8291's example and the final iPhone checklist.
2. Open `https://claude.ai/code/<bridgeSessionId>` on the iPhone for a
   running loom session. It must reach that session, in the Claude app or
   Safari, and a permission prompt must be answerable there.
3. Stream server-sent events through the tunnel with the 20 s ping. Events
   must arrive as sent, and the stream must survive five idle minutes.

If 1 fails, push is redesigned before package D. If 2 fails, Open in Claude
links to `https://claude.ai/code` and names the session's title instead. If
3 fails, the stream is redesigned before package B.

### Outcome, 2026-10-22

Run on fresco and an iPhone through the `fresco-loom` tunnel
(`https://loom.aidanbailey.me` to `http://127.0.0.1:5666`), with a
throwaway server and the `web-push` library 3.6.7. All three steps passed,
so none of the fallbacks above applies.
- The tunnel passes the public `Host` and `X-Forwarded-Proto: https`, so
  the `Host` check holds behind it.
- 1, push: Apple answered 201, and the phone showed the push with the app
  open, with it closed, and with the phone locked. A JWT signed by a key
  the subscription wasn't made with got 400
  `{"reason":"VapidPkHashMismatch"}`; one with a corrupted signature, 403
  `{"reason":"BadJwtToken"}`. A subscription the app had dropped got 201,
  again 5 and 15 minutes later, and the phone showed nothing. A `Topic`
  whose length is 1 mod 4 got 400 `{"reason":"BadWebPushTopic"}` (5, 9 and
  13 characters; 4, 6, 8, 10 and 32 passed). Subscribing again after an
  unsubscribe took iOS about 2m18s, and gave a new endpoint. fresco reaches
  Apple's push service in about 235 ms (TCP) and 470 ms (TLS).
- 2, Open in Claude: from the installed app,
  `https://claude.ai/code/<bridge id>` opened the Claude app on that
  session, as a plain link and with `target=_blank`, and a permission
  prompt was answered there.
- 3, the stream: with the 20 s ping, a stream from fresco lasted 700 s and
  one on the phone over 10 minutes, every event in order, and the events
  after 5m30s and 10m30s idle arrived within about 100 ms. Without pings,
  Cloudflare closed the stream after 125 s. A first run on the phone lost
  both late events, although the server wrote them into a stream that
  stayed open; that run recorded nothing on the phone's side, so where
  they went is unknown. Each reopening of the app left its old stream open
  on the server for 60 to 240 s.

What it changed, in the sections above: the `Topic`'s format and its test
(Push, Testing); the app's check of its own subscription, and
`DELETE /device/push` (Push, API); the waiting subscribe button and the
retry after another key (Push); the app's reconnects (The app); and 125 s
for Cloudflare's idle cut (The stream). Delete the spike's Home Screen app
before installing the real one: on the same origin, it holds a
subscription made with the spike's key.

## Work packages

- **A. Groundwork:** `web.json`, `ConfigureWeb`, `PairDevice`,
  `RevokeDevice`, `WebView`, `core.WebHost`, `OriginWeb`, the hello `role`
  and the backlog rule, the bridge ID, the tool summary, and `loom web`.
- **B. `internal/web`:** the daemon wiring, the listener and its
  reconfiguration, auth, the projection, the API, the stream, notices.
- **C. The app:** the shell, the screens, the service worker, the manifest
  and icons, and the install and pairing flow.
- **D. Push:** VAPID, the encryption, the sender, the triggers, the
  device settings, the badge.
- **E. Docs:** `internal/web`'s `CLAUDE.md` and `README.md`,
  `docs/ARCHITECTURE.md`, `USAGE.md` (`loom web`, the tunnel, the systemd
  unit), and the regenerated `docs/specs/protocol.md`. Check the budgets
  first (`go run ./tools/claudemd -v`): `core/CLAUDE.md` was at its byte
  budget on 2026-10-14, so core's new rules may need a guide.

A comes first, then B, then C; D needs B and the spike; E closes.

## Deployment, outside the repo

- `~/System`: a cloudflared service on fresco with its own tunnel and
  credentials, an ingress from the app's host name to
  `http://localhost:<port>`, and the DNS route.
- The app answers only while the daemon runs. A systemd user unit for
  `loom serve` keeps it up after a reboot. That unit must import the
  session's environment (`PATH`, `SSH_AUTH_SOCK` and the rest), because the
  daemon passes its environment to every agent it launches.
- `USAGE.md` documents both.

## Alternatives considered

- **A separate `loom web` service:** isolates crashes and keeps the daemon
  local, but adds a second service that every daemon upgrade drops (a
  newer daemon refuses an older client, and a Nix-pinned unit keeps
  restarting the old binary until a switch). It needs the TUI's reconnect
  logic, and push stops while it's down.
- **A hub the host dials out to:** the federation path, but by far the
  most work and a second deployment, where the tunnel already avoids
  inbound ports.
- **Cloudflare Access instead of pairing:** ties loom to Cloudflare and
  leaves the loopback port open to every local process.
- **Routing through medesco's tunnel:** loom would listen on the LAN, and
  the device token would cross it in the clear.
- **The fleet grouped by workspace:** mirrors the TUI's overview, but
  finding what waits means scanning every group.

## Out of scope

- A terminal view of a pane; answering permission prompts in loom.
- Branch and issue pickers; recover and discard; accounts and settings;
  the workbench, diffs and review.
- Quiet while at the desk: a session a TUI shows on screen still pushes.
- Android; more than one host; Cloudflare Access.

## What it keeps open for federation

- `internal/web` is a `Core` client, so it can move into its own process,
  or behind a hub, by changing its wiring, not its code.
- The API is versioned (`/api/v1`), and its IDs are stable per disk; a hub
  would add the host to each.
- Pairing is per host; a hub would pair a device once and hold a
  credential per host.
