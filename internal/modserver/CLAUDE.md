# internal/modserver

The daemon's server for the loom mod (`session/claudemod`): HTTP on the mod socket, which `internal/daemon.ModSocketPath` places beside the daemon's own. `POST /v1/hello`, `GET /v1/inbox?launch=L` (answered at once with the waiting messages, or held up to `DefaultHold` and then 204) and `POST /v1/events`, each naming the launch the mod runs in; JSON bodies capped at `maxBody`. Every request goes to the model through `core.ModHost`, which `*core.Loop` implements.

## Rules when modifying this package

- **Hold no state, and reach the model only through `core.ModHost`.** The model owns sessions; a copy here goes stale, and the loop serializes every change. **Convention** — a second owner of who is live and what is queued.
- **End every held poll on a wake, a stop or the client leaving, and answer 503 on a stop or a stopped model (a nil channel).** The mod polls again at once after any answer, so a hold that outlives its reason only delays a message, and one that outlives the daemon keeps `Shutdown` waiting. **Enforced** by `TestInbox_HoldsUntilWoken`, `TestInbox_AStopEndsAHoldWith503` and `TestInbox_AStoppedModelIs503`.
- **Keep `DefaultHold` at least 5 s under the 30 s at which Claude's host aborts a `$.http.fetch`.** An aborted poll is a failure to the mod, which backs off and says hello again, so every quiet session would cycle through errors. **Convention** — measured once (plan package A), and only an opt-in real-Claude run sees it.
- **Answer an unknown launch with 404.** The mod retries every failure alike, but a 404 in the log reads as a stale or not-yet-adopted launch, normal after a daemon restart, where a 500 reads as a bug. **Enforced** by `TestHello`, `TestInbox_AnUnknownLaunchIs404` and `TestEvents`.
- **Keep the socket 0600 in the daemon's private directory.** Anyone who can post here can make every Claude loom runs submit a prompt. **Enforced** by `TestListen_TheSocketIsPrivate`.

## Pointers

- [`../daemon/CLAUDE.md`](../daemon/CLAUDE.md) — where the socket lives and when it is served.
- [`../../session/claudemod/CLAUDE.md`](../../session/claudemod/CLAUDE.md) — the module on the other end.
