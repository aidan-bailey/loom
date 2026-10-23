# internal/web

The daemon's web layer: what `loom serve` starts so a phone can see the fleet and act on it. It is a client of the daemon's own `rpc.Server`, joined over `net.Pipe` as the `web` role, plus `core.WebHost` for the device records the model keeps in `web.json`. It serves an embedded app, a JSON API and a live stream on a loopback port that a Cloudflare tunnel reaches, and sends Web Push. It holds no session state, and logs under `subsystem=web`. Rules for changing it: [`CLAUDE.md`](CLAUDE.md).

## Files

| File | Holds |
|---|---|
| `web.go` | `Options`, `Start`, `Layer` and `Close`; the drain; `fail` and `recoverPanic`; the `sink` and `mux` seams each part registers through |
| `server.go` | the listener (`bind`), `guard` and the origin rule |
| `auth.go` | pairing (`routePair`), `authed`, the device cookie |
| `jsonio.go` | `readJSON`, `writeJSON`, `writeError` |
| `fleet.go` | the projection: `sessionJSON`, `workspaceJSON` and which workspaces the fleet shows (`layout`), `usageJSON`, `deviceJSON`, `snapshotJSON`, `noticeJSON`, `groupOf`, `deviceOf`, `noticeOf` |
| `markdown.go` | the message tree: `buildTree` parses a last message with goldmark into the plain nodes the app draws (`mdNode`), with raw HTML as text and only `http(s)` links (`safeHref`) |
| `stream.go` | the hub (`publish`, `sendTo`, `closeDevice`), the `/api/v1/stream` handler, recent notices |
| `api.go`, `reqs.go` | `routeAPI` and the action handlers; `reqs`, which makes the layer's request IDs and waits for each `Reply` |
| `reads.go` | the read routes (`GET …/turns`, `GET …/diff`): `read`, which waits for the Reply and stops tracking it (`reqs.drop`); `turnsJSON` (each text item as its tree) and `patchJSON` |
| `static.go` | `routeStatic`: the embedded app, with the build ID written into `sw.js` and `index.html`, and the shell's file list into `sw.js`, as they are served |
| `push.go`, `triggers.go` | the sender goroutine and `routePush` (`GET /api/v1/push-key`); the waiting and finished triggers |
| `webpush/` | VAPID (ES256), RFC 8291 encryption (aes128gcm, one record) and the request to a push service; standard library only |
| `static/` | the app: `index.html`, `app.css`, `sw.js`, `manifest.webmanifest`, `icons/`, `lib/preact-htm.mjs` and the ES modules in `js/` |
| `testmain_test.go` | isolates the loom dirs and tmux for the package's tests |
| `e2e/web_test.go` | a sandbox daemon with the web enabled: pair, stream, create, pause, resume and kill over HTTP |

## How it runs

`daemon.Serve` calls `web.Start` after the mod server listens and `Layer.Close` first when it stops, before the bye. `Start` joins the server as the `web` role, which the server does not count as a client, so a notice raised while only the layer is connected waits for the next TUI. It builds its parts (`startParts`) and then drains the client as the TUI does: on each wake it takes `Sync`'s events, hands them to each sink in turn, and applies the `WebView` a `ModelChanged` carries.

The layer listens only while `WebView.Listen` is set. It acts on `WebView.Rev`, which the model bumps on every `ConfigureWeb` and on nothing else: whenever `Rev` differs from the last one the layer acted on, it closes the listener, which ends every stream, and binds `Listen` unless it is empty (then it stays closed). Two `ConfigureWeb`s that one `Sync` coalesces still differ from that `Rev`, so a disable and an enable are never missed. What happened goes back to the model through `WebHost.WebServing`, which leaves `Rev` alone, so a report never binds. A listen that fails (the port is taken) leaves the web off with the reason in `WebView.Error` and a notice for the next TUI, and is tried again only by the next `ConfigureWeb`, the same values included: `loom web enable` again. The listener takes loopback IP literals only, whatever `web.json` says.

A panic in the drain or the listener stops the layer, tells the model (the same notice) and leaves the daemon running. Each goroutine the layer starts defers `recoverPanic` first, which calls `fail`; `fail` returns at once and stops the layer on a goroutine of its own, so a part can call it from the goroutine that panicked. `Close` runs the same stop, once between them: whichever comes first stops the layer, and the other waits until it is complete. `Close` still has to run at the daemon's stop.

## Every request

`guard` stands in front of every route:

1. The security headers go on the answer, errors included.
2. The `Host` must be the origin's host (default port dropped, any case), else 421.
3. A method other than GET or HEAD must carry exactly one `Origin` header equal to the origin, else 403.
4. The body is capped at `maxBody` (1 MiB).

With no listener bound there is no origin rule, and `guard` refuses everything.

## Pairing and the device cookie

`POST /api/v1/pair` takes `{"code": "K7QM-2XPA"}` and asks the host to redeem it. The model checks the code, consumes it and makes the device; the layer sets the token it returns as the cookie `__Host-loom` (Secure, HttpOnly, SameSite=Strict, Path=/) and answers `{"device": "<name>"}`. The refusals are `{"error": <text>}` with a status the app keys on: a wrong, used or expired code is 400 (never 401, which the app reads as "not paired"), too many wrong codes 429, a name already paired or 16 devices paired 409, and a web that is off 503; any other failure is a 500 with a fixed text.

`authed` wraps every route after pairing: it reads the cookie, decodes it as unpadded base64url, refuses (401, without asking the host) a value that is not exactly 32 bytes, and asks the host which device holds the SHA-256 of those 32 bytes. No device is a 401; otherwise the device is touched (last seen) and the handler runs with its name.

## Tests

The tests boot a model on its loop, serve it with the real `rpc.Server`, start the layer on it and talk to it over HTTP on a loopback port (`webStack` in `web_test.go`). `TestMain` points the loom dirs and tmux at throwaway ones. They pin both sides of each guard (the right and a wrong `Host`, the same and a cross `Origin`, a valid and a revoked cookie), the groups, each push transition once, RFC 8291's example and the VAPID token verified with its public key, and the static files (everything `sw.js` precaches is embedded, the manifest parses). `e2e/web_test.go` runs under `-tags e2e` and needs tmux.

## Routes and the API

Every route under `/api/v1` except `POST /api/v1/pair` needs the device cookie. The app shell and its static files are public and hold no data.

| Route | Does |
|---|---|
| `POST /api/v1/pair` | `{code}`: sets the cookie, answers `{"device": "<name>"}` |
| `GET /api/v1/stream` | the live stream |
| `POST /api/v1/sessions` | create from `{workspace, title, prompt}` and the optional `account`, `model`, `permission_mode`, `effort`; 201 `{"id", "notices"}` |
| `GET /api/v1/launch-defaults?workspace=<id>` | `{account, accounts, model, models, permission_mode, permission_modes, effort, efforts}`: what the new-session sheet pre-fills (`account` is the account the launch modal would pre-fill, which `core.ChooseLaunchAccount` picks: the workspace's own account when its settings name a registered one, otherwise the registry's default, and `""` when the workspace's program is not Claude or the model has no account registry) and the choices it offers, which are the registry's account names, `config.ClaudeModelAliases()`, `config.ClaudePermissionModes` and `config.ClaudeEfforts`, so the app hard-codes none |
| `POST /api/v1/sessions/{id}/send` | `{text}`: `Send` with `core.OriginWeb`; a reply that is an allowed slash command runs it, and its result arrives as a notice |
| `POST /api/v1/sessions/{id}/pause`, `resume`, `kill`, `push` | the request of the same name |
| `POST /api/v1/sessions/{id}/merge` | `{source}`: `Merge` with the session in the URL as the target |
| `GET /api/v1/sessions/{id}/message` | `{text, blocks, truncated}`: the session's last message as a tree of plain nodes (`markdown.go`), built from the text the web client's replica holds; the app draws it as elements, and only while `text` is the message it shows |
| `GET /api/v1/sessions/{id}/turns?conversation=&before=&after=` | a page of the session's conversation (`ReadTurns`): `{conversation, turns, earlier, reset, skipped}`. A turn is `{id, at, from, name, text, cut, command, partial, items}`, `from` one of `you`, `plugin`, `peer`, `task`; an item's fields follow its `kind`: `text` has `blocks` (its tree, `markdown.go`) and `truncated`, `steps` has `total`, `failed` and `steps` (`{tool, summary, at, state}`, `state` one of `pending`, `done`, `failed`, `interrupted`), `note` has `text` and `error`, `compacted` only `at`. `before` (the page before, from a page's `earlier`) and `after` (the turns from a turn's `id` to the end) are byte offsets, at most one set; `earlier` is null at the start of the file and on an `after` page; `reset` says the page replaces every turn held |
| `GET /api/v1/sessions/{id}/diff` | the session's worktree diff against its base commit (`ReadDiff`): `{base, files}`, a file `{path, old_path, status, binary, added, removed, hunks, omitted}`, `status` one of `A`, `M`, `D`, `R`, `C`, a hunk `{header, lines, cut}` (each line with its `+`, `-`, space or `\` prefix; `cut` the indexes of the lines cut at 2,000 bytes), `omitted` `lines` or `budget` when a file's hunks were dropped |
| `PUT /api/v1/device` | `{waiting, finished, pause}`: notification settings, each field optional and a missing one left as it is; `pause` is `"1h"` (until an hour from now) or `"none"` (ends a pause), any other value is 400; answers the device |
| `PUT /api/v1/quick-replies` | `{replies}`: the quick replies every paired phone offers above its reply box, or `null` (or no field) for the defaults (`core.DefaultQuickReplies`); a list `core.NormalizeQuickReplies` refuses is 400 with its words; answers `{"quick_replies"}`, the list as the app reads it |
| `POST /api/v1/device/push` | the browser's `PushSubscription.toJSON()`; answers the device |
| `DELETE /api/v1/device/push` | drops this phone's subscription, which the model records as expired, when the app finds on a launch that the phone holds none or may not notify; 204 |
| `GET /api/v1/push-key` | `{"key"}`: the VAPID public key the app subscribes with |
| `DELETE /api/v1/device` | unpairs this phone; 204 |

Responses are JSON. An error is `{"error": "<text>"}` with 400, 401, 403, 404, 405, 409, 421, 429, 500, 503 or 504; 403 and 421 are the guard's (another origin, another host), 405 a HEAD on the stream. A refusal by the model is 409 with its text; a stopping daemon is 503. A read (turns, diff) answers its data; one the model does not answer within `replyWait` is 504, and its request is no longer tracked once answered, since its Reply carries the data and no notice follows it. An action answers `{"notices": [...]}`; a request still running after `replyWait` (30 s) answers 202 `{"pending": true}` and its outcome arrives on the stream. The notices a response carries are not sent on the stream again: they are kept among the recent notices the snapshot carries, so a response the phone never got loses none. A handler takes the layer's next request ID, makes the request and waits for the `Reply` the drain routes back. Create checks the workspace and the title as `loom session new` does and resolves the launch options as it does.

A session as JSON: `id`, `workspace`, `title`, `branch`, `worktree` and `tmux` (the worktree's path and the tmux session's name, which the Kill sheet names; each is left out while empty), `group`, `label`, `status`, `since`, `wait` (`reason`, `detail`), `last_message`, `diff` (`added`, `removed`), `ahead`, `behind`, `parity_known`, `pr` (`number`, `state`, `checks`, and `url` when `gh` gave an `https` one; or null), `account`, `agents`, `remote_url` (`https://claude.ai/code/<RemoteSession>`, or empty) and `claude` (the session runs Claude, so the app offers its conversation). IDs are JSON numbers. A workspace is `id`, `name`, `label` and `shown`.

Groups: Prompting is `needs_you`; a started Ready is `your_turn`; Running, Loading and Deleting are `working`; Paused, Recoverable and an unstarted Ready are `paused`, each with its own label. Workspace terminals are left out.

## The stream

`GET /api/v1/stream` is server-sent events. Every connect starts with a `snapshot` (the build, the host name, the workspaces in the order the app lists them, each flagged `shown` when it is open as a tab in loom or holds a session that isn't paused, or when no tab is open; the sessions by workspace; the device; `badge` (the sessions that need you across the served workspaces); the last `recentNotices` (20) notices; `usage`, each account's plan usage, with `usage_stale_after_s` (`core.UsageStaleAfter`), the age past which the app dims a sample; and `quick_replies`, the list the app offers), so nothing is replayed. After it: `sessions` (one workspace's whole list when it changes, at most four a second: `publishEvery`), `workspaces` (also when a `shown` flag or the order changes), `usage`, `quick_replies`, `device`, `notice`, `ping` every `pingEvery` (20 s, since Cloudflare closes a stream idle for 125 s) and `bye` when the daemon stops. `sessions` and `workspaces` both carry `badge`, the same fleet-wide count as the snapshot's, sent even at 0 so the app clears its icon. The stream sets the device cookie again when it connects, so a device in use never expires, and a revoked device's streams end as soon as the device list is published. At most `maxStreamsPerDevice` (4) are open per device, a fifth closing that device's oldest, and `maxStreams` (32) in all, a 33rd being refused with 503. The stream has no write timeout over its whole life: each event's write is bounded at `streamWriteTimeout` (30 s), and the stream pings instead.

## The app

`index.html` loads `js/app.mjs`, which routes `#` (the fleet), `#s/<id>` (a session), `#s/<id>/turns` (its conversation, `js/turns.mjs`), `#s/<id>/diff` (its changes, `js/changes.mjs`), `#new`, `#settings` and `#pair` and renders each screen from `screens` (`internal/web/static/js/screens.mjs`). `js/api.mjs` makes the calls and reconnects the stream with a backoff from 1 s to 30 s, drops a stream silent for 45 s, and opens a fresh one whenever the page becomes visible again, since a page iOS resumes can sit on a stream that delivers nothing; `js/store.mjs` keeps the last snapshot in IndexedDB, drawn at once and marked stale with every action off until the stream is back, and cleared by a 401 or an unpairing. The service worker (`sw.js`) caches the shell only, never the API, and updates when a snapshot's `build` differs from the shell's. The app sets the Home Screen badge (`navigator.setAppBadge`, cleared at 0) from the `badge` of each snapshot, `sessions` and `workspaces` event, and the worker sets it from a push's. Opened in Safari rather than from the Home Screen, the app shows the Add to Home Screen steps instead of pairing. There is no build step and no JavaScript test harness: the modules are checked with `node --check`, and the logic stays in Go. The Preact + htm file is pinned by a SHA-256 in a test, and its licence is in `NOTICE.md`.

The skin is loom's TUI on the phone (spec `docs/superpowers/specs/2026-10-23-pwa-skin-glance-design.md`): the afterglow colours as `app.css`'s tokens, dark only, `ui-monospace` type (SF Mono on the iPhone), glyph rows, and boxes whose label sits in their top border (`js/skin.mjs` holds the glyphs and icons). The fleet lists the `shown` workspaces under count tiles and the usage boxes (`js/usage.mjs` formats a window), with a status line at the bottom; a session's screen fetches its message's tree on demand and draws it with `js/markdown.mjs`, showing the plain text until it arrives, and fills its reply box from the quick-reply chips, edited in Settings. The conversation screen reads its turns on open and refreshes the newest turn (`after`) when the stream changes the session's status or last message and every 5 s while it is Running; the changes screen refreshes when the session's counts or status change and every 10 s while it is Running. Both session screens end in `js/reply.mjs`'s reply box.

## Push

Turned on by a tap in the app: it subscribes with the VAPID public key and posts the subscription, and the button waits while the phone subscribes, which can take minutes. Once a launch the app checks its subscription against the device's, since Apple answers 201 to a subscription the phone has dropped: while the state is `ok` or none it posts its subscription again, and holding none, or no permission to notify, while the device has one, it has the model drop it (`DELETE /api/v1/device/push`, recorded as expired) and says so. Triggers come from the projection's transitions: *waiting* when a session enters Prompting or stays in it with a reason or tool summary that differs from the last non-empty one pushed in that stretch (a cleared one never pushes, nor does its return; leaving Prompting ends the stretch), *finished* when a started session goes from Running to Ready. The first projection after the layer starts is a baseline and pushes nothing. A device gets the kinds its settings allow, and nothing while paused or while the web is off (an empty `listen`). The subscriptions and the VAPID key stay while the web is off, so enabling it again resumes pushes without the phone subscribing again. The payload (`kind`, `session`, `title`, `body`, `badge`, `url`) is encrypted per RFC 8291; waiting goes with `Urgency: high`, finished with `normal`, both with a one-hour TTL and a `Topic` per session: the session ID's 8 bytes as unpadded base64url, since Apple refuses a `Topic` whose length is 1 mod 4 (400 `BadWebPushTopic`). The sender is a goroutine of its own with a queue of at most 100: it posts with a 10 s timeout, at most 30 a minute per device, retries a 429, a 5xx or a transport error up to three times, honouring a `Retry-After` of at most 30 s (a longer one is given up on), drops a subscription answered 404 or 410 and records it as expired, records any other status as failing with it, and only logs a transport error that outlasts its retries. A result names the endpoint it was sent to, and changes nothing once the device holds another subscription. `loom web devices` shows the state.

## Limits

Bodies are capped at 1 MiB (`maxBody`). A conversation page holds at most 10 turns and, past its newest, 256 KiB of text (a prompt 16 KiB, a text item 64 KiB, a fold its newest 200 steps), from at most 16 MiB of the transcript; a diff keeps at most 1 MiB of lines, 5,000 lines a file and 2,000 bytes a line. A reply is 1 byte to 64 KiB after trimming, a new session's prompt at most 64 KiB, and its title as `core.CheckNewTitle` says. A launch override must be an account the registry holds, a model in `config.ClaudeModelAliases()`, a permission mode in `config.ClaudePermissionModes` and an effort in `config.ClaudeEfforts`. A push subscription is an `https` endpoint of at most 2 KiB on `web.push.apple.com`, `fcm.googleapis.com`, `updates.push.services.mozilla.com` or a host ending in `.notify.windows.com`, with no user and no port but 443. Ids in paths are decimal and served by the model: an unknown session is 404, an unknown workspace 400. Settings: `listen` is exactly `127.0.0.1:<port>` or `[::1]:<port>` with a port from 1 to 65535 (0 is refused), and `origin` is at most 255 bytes; disabling the web voids every pending pairing code.
