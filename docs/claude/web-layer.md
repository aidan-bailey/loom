# Changing the web layer

`internal/web` is a `core.Core` client inside the daemon that serves a phone app, a JSON API, a server-sent stream and Web Push behind device pairing. Read this before adding a route, a stream event or a push kind, before changing the JSON the app reads, and before trying a change on a phone. The package's rules are in `internal/web/CLAUDE.md`; this is the procedure and the traps.

## Adding an API route

1. Register it in `routeAPI` (`internal/web/api.go`) on the mux the registrar gets, wrapped in `l.authed(…)`: only `POST /api/v1/pair` is open. A handler takes the device name `authed` hands it and logs the device, action, session and result (`subsystem=web`).
2. Make the request through the layer's `core.Core` client and wait with `l.reqs.next()` and `l.reqs.wait(ctx, req, device)`. Answer 200 with `{"notices": [...]}`; a `Reply` that refuses is 409 with the model's text, a stopping daemon (`TakeRefused`) 503, a request still running after `replyWait` 202 `{"pending": true}`. Never copy the model's preconditions into the handler: `admit` decides, and the app's buttons only follow the session's `status`. A read, whose `Reply` carries data, goes through `l.read` (`internal/web/reads.go`): 200 with the data, the model's refusal as an action's, 504 when `replyWait` runs out, and the request dropped once answered (`reqs.drop`).
3. Check what the request carries as the Limits section of `internal/web/README.md` says (a body of at most 1 MiB, ids decimal and served by the model, a reply of 1 byte to 64 KiB, a launch override from `config.ClaudeModelAliases()`, `config.ClaudePermissionModes` and `config.ClaudeEfforts`). An unknown session is 404, an unknown workspace 400.
4. Add the call to the app (`internal/web/static/js/`), through `act(method, path, body)` so its notices and errors become toasts. A request that can lose work (kill, push, merge) gets a confirmation sheet (`ConfirmSheet`, `internal/web/static/js/sheets.mjs`) that names exactly what happens.
5. Add the route to the API table of `internal/web/README.md`, and to `e2e/web_test.go` when it is a lifecycle action.

## Adding a stream event

1. Publish it from a sink (`apply(evs []core.Event)`) through `l.hub.publish(event, data)` or `l.hub.sendTo(device, event, data)`. A sink never blocks and does no network I/O: the drain hands it every `Sync`'s events on one goroutine, and a stalled sink stalls every stream.
2. If a reconnecting phone needs the state, put it in the `snapshot` too: every connect starts with one and nothing is replayed.
3. Add the event's handler where `internal/web/static/js/app.mjs` calls `connect(handlers)` (`internal/web/static/js/api.mjs`), and keep in mind that `internal/web/static/js/store.mjs` saves the last snapshot for the next start.
4. Document the event in the stream section of `internal/web/README.md`.

## Adding a push kind

1. Work the trigger out in `internal/web/triggers.go` from the projection's transitions, one push per transition. The first projection after the layer starts is a baseline and pushes nothing, and workspace terminals never push.
2. Add the kind to the payload (`kind`, `session`, `title`, `body`, `badge`, `url`), to the device's notification settings (`core.NotifySettings`, `PUT /api/v1/device`) and to the settings screen.
3. Choose `Urgency`, and keep the per-session `Topic` that `internal/web/triggers.go` makes (the session ID's 8 bytes as unpadded base64url), so an offline phone gets only the latest: Apple refuses a `Topic` whose length is 1 mod 4 with 400 `BadWebPushTopic`, and `webpush.NewRequest` refuses one too.
4. Show it in `internal/web/static/sw.js`: the notification is tagged by session, because iOS requires every push to show something and loom cannot clear a stale one.

## Changing the JSON the app reads

The API is `/api/v1` and an installed app may be older than the daemon for a while. Add fields; never rename, retype or drop one in place. The service worker updates the phone when the build ID differs from the snapshot's `build`, so a daemon upgrade upgrades the phone, but only after it has loaded the old shell once.

## Changing the app

- Modules are `.mjs` (Node 22's `node --check` catches syntax errors in those and misses them in `.js`): `for f in internal/web/static/js/*.mjs; do node --check "$f"; done`. CI has no Node, so no Go test depends on it.
- No inline script and no `style` attribute: the Content-Security-Policy is `default-src 'self'`, and a blocked handler fails silently on the phone.
- The Preact + htm module is one pinned file, `internal/web/static/lib/preact-htm.mjs`: replace it and its pinned SHA-256 together, with the version and source URL in its header comment and the licence in `NOTICE.md`. It sits in `lib/`, not `vendor/`, because Go's module zips drop files below a nested `vendor/` and a `go install` build would embed nothing.
- `sw.js` carries the placeholders `__LOOM_BUILD__` and `__LOOM_SHELL__`, and `index.html` the build ID in its `loom-build` meta tag (`__LOOM_BUILD__`); `routeStatic` fills them when serving; the precache list therefore follows the embedded files, and the worker caches the shell only, never `/api/`.
- The skin is dark only, its tokens on `app.css`'s `:root` (`TestStatic_TheSkinIsDarkWithEveryToken`). A form control's text is 16px: iOS zooms into a smaller one.
- A last message is drawn from its tree (`GET /api/v1/sessions/{id}/message`), never from an HTML string. A node the app needs is added in `internal/web/markdown.go`, with a case in `TestBuildTree_EachNodeType`.
- To see a screen without a phone, run `go run ./tools/webshot -out <dir outside the worktree>` from the repo root: it serves `internal/web/static/` from disk against a canned loom (`tools/webshot/fixture`), renders each screen in headless Chromium as an iPhone 15 Home Screen app, the owner's phone (393×852 points at @3x, safe-area insets 59 and 34, `navigator.standalone`, service workers bypassed), and writes a PNG per screen framed with the status bar, Dynamic Island and home indicator. `-screens fleet,turns` picks screens, `-guides` tints the unsafe areas, `-bare` writes the viewport only. It prints console errors, uncaught exceptions and any API route the fixture can't answer, and exits 1 on any; a route a new screen reads needs a fixture and an entry in `apiRoutes` (`tools/webshot/canned.go`). The capture is the viewport, so a screen that scrolls itself on load (the conversation opens at its newest turn) shows what the phone shows. The link states (CONNECTING over a cached fleet, RESTARTING, OFFLINE) are not canned: `serve.py` in `docs/superpowers/plans/2026-11-01-pwa-header-footer.md` (Task 1, step 1) has a mode for each, and headless Chromium keeps no saved snapshot from one run to the next, so CONNECTING over a cached fleet is seen only on the phone.
- Inside a flex container (a `<summary>` laid out with `display: flex`, a tile's figure) a text's leading space next to an element's edge is dropped: "14 steps" and a `<span> · 1 failed</span>` render as "14 steps· 1 failed". Start such a span with a no-break space (`" · " + …`), as the mockups do with `&nbsp;`.
- The header is pinned (`.sc-chrome`, sticky), so an in-page jump (`scrollIntoView`, an anchor) lands under it unless the target's `scroll-margin-top` clears it, as `.sc-group-title`'s does. A screen that fills the space under the header takes `flex: 1` in `main`'s column; a `100dvh` height adds the header's height to every page, which then always scrolls.

## Answering an ask

The dialogs a session's Claude holds open (`InstanceView.Asks`, `core/asks.go`) reach the phone through the loom mod: it posts each ask to the mod socket and polls for its answer while Claude's own dialog stays up (spec `docs/superpowers/specs/2026-11-03-pwa-answer-asks-design.md`, whose Outcome records what Claude does). What to keep when changing any part of it:

- **The model composes, the mod returns.** `Core.Answer` checks the answer against the ask and builds Claude's exact `result`, `deny` or `decision` (`composeAnswer`); the route only bounds the body, and the app only fills it. A shape Claude refuses is a hook Claude skips, so the dialog stays up and the phone was told it worked.
- **The Reply comes when the mod takes the answer, not when the model stores it.** An answer no poll takes within `askTakeTimeout` is withdrawn (`askTick`) and refused, so a late poll never delivers what the phone was told failed; one whose ask closes first is refused "answered in Claude first".
- **An ask closes when Claude's dialog answered it:** the mod's close (a question's `next` resolved; a permission's paired call settled), or for a main-loop permission the session leaving Prompting after the model saw it Prompting with the ask open (`asksSeeStatus`'s `seenPrompting`). A permission hook's `next.signal` never aborts on a person's answer, and the roster reads `busy` while a background agent's prompt waits, so a subagent's permission closes only with its call. A closed ask leaves a tombstone on its link: the mod posts again on a 404, never on a 410.
- **Every refusal reaches the phone as 409 with the model's words.** Only three errors keep their identity across the wire, so the route can't tell an answered ask from a malformed answer.
- **Changing the module means the opt-in real-Claude runs:** `TestRealClaude_ValidatesTheMod` and `TestRealClaude_AnswersAsksThroughTheDaemon` (`LOOM_TEST_REAL_CLAUDE=1`); nothing in CI loads the mod.

## Trying a change

Go tests cover the layer over the production path (a booted model, the real `rpc.Server`, a client over `net.Pipe`, `httptest`); the JavaScript has no harness, so its logic stays in Go. For the phone, run a dev build through the `loom-dev` skill (`go run ./tools/loomdev`), never `./loom` or `loom serve stop` in a loom pane, and work through these steps: install, pair, turn on notifications, receive both push kinds with the app open and closed, Open in Claude, reply, kill with its confirmation, revoke.

## Traps

- **A field added to what the model publishes is a wire change.** `WebView` rides `ModelChanged`, and `InstanceView` carries `github.State` and the rest, so a new field there needs no `rpc.Protocol` bump but changes `docs/specs/protocol.md`: rerun `go test ./core/rpc -run TestProtocolReference -update`, or `TestProtocolReference` fails the suite (and the Nix build). Keep nil and empty apart only if both survive JSON: a nil slice crosses as `null`, an empty one as `[]`.
- **iOS keeps a Home Screen app's storage apart from Safari's, and sends Web Push only to Home Screen apps (iOS 16.4 and later).** A pairing done in Safari does not carry over to the installed app.
- **Every request is checked against the configured origin.** The `Host` must equal the origin's host and port (421 otherwise), and a request other than GET or HEAD must carry an `Origin` equal to it (403). A test that builds its own request sets both.
- **Pairing codes live in the model's memory.** A daemon restart voids them, five wrong codes void every pending one, and a code is bound to the device name it was made for.
- **A `web.json` that fails to load is never overwritten.** Unreadable, a newer version, or a stored `listen`, `origin`, device name, token hash or VAPID key that fails validation all count. The web stays off, `ConfigureWeb` and `RevokeDevice` refuse, `PairDevice` answers that the web is off, and a notice says why; fix or move the file by hand.
- **A push service answers 201 to any ciphertext it accepts and cannot read it.** A wrong encryption shows only as a phone that stays quiet, which is why `internal/web/webpush` is checked against RFC 8291's own example.
- **Cloudflare terminates TLS, so it can read the traffic and the device token.** Revocable tokens limit the damage; never log a token, cookie, pairing code or push subscription.
- **The layer holds no session state.** A copy of a view that outlives its replica goes stale: derive from the client's replica and the model's device records.
