# internal/modserver

The daemon's server for the loom mod (`session/claudemod`): HTTP on the mod socket, which `internal/daemon.ModSocketPath` places beside the daemon's own. `POST /v<n>/hello`, `GET /v<n>/inbox?launch=L` (answered at once with the waiting messages, or held up to `DefaultHold` and then 204) and `POST /v<n>/events`, `n` being `claudemod.Version`, each naming the launch the mod runs in; JSON bodies capped at `maxBody`. Every request goes to the model through `core.ModHost`, which `*core.Loop` implements.

## Rules when modifying this package

- **Hold no state, and reach the model only through `core.ModHost`.** The model owns sessions; a copy here goes stale, and the loop serializes every change. **Convention** — a second owner of who is live and what is queued.
- **End every held poll on a wake, a stop or the client leaving, and answer 503 on a stop or a stopped model (a nil channel).** The mod polls again at once after any answer, so a hold that outlives its reason only delays a message, and one that outlives the daemon keeps `Shutdown` waiting. **Enforced** by `TestInbox_HoldsUntilWoken`, `TestInbox_AStopEndsAHoldWith503` and `TestInbox_AStoppedModelIs503`.
- **Keep `DefaultHold` at least 5 s under the 30 s at which Claude's host aborts a `$.http.fetch`, and the hold plus the mod's 5 s longest backoff under `core.ModPollTimeout`.** An aborted poll is a failure to the mod, which backs off and says hello again, so every quiet session would cycle through errors; a hold past the timeout makes the model count a working mod absent, refusing and pasting what it would have delivered. **Enforced** for the numbers by `TestDefaultHold_FitsClaudesFetchAndTheModelsPollTimeout`; the 30 s was measured once (plan package A), and only an opt-in real-Claude run re-measures it.
- **Answer an unknown launch with 404.** The mod retries every failure alike, but a 404 in the log reads as a stale or not-yet-adopted launch, normal after a daemon restart, where a 500 reads as a bug. **Enforced** by `TestHello`, `TestInbox_AnUnknownLaunchIs404` and `TestEvents`.
- **Build the routes from `claudemod.Version` (`route`), never a literal `/v1`, and tell the model the mod is served once `Listen` listens (`ModsServing`).** The module builds its URLs from the same version, so a literal path would 404 every real mod after a bump; and a grace timed from before the listen can run out while a mod is still backing off. **Enforced** by `TestHello`, `TestEvents` and `TestInbox_HandsOutWaitingMessagesAtOnce`, whose URLs come from `claudemod.Version`, and by `TestListen_SaysTheModIsServed`.
- **Listen again only when the socket file is gone or replaced, or the wanted path moved (`Watch`), and remove a socket file only while it is still the one listened on (`removeOwn`).** Re-listening an intact socket would cut nothing but churns the model's grace (`ModsServing`), and removing a file put in its place deletes another's socket. **Enforced** by the `TestWatch_…` tests and `TestClose_LeavesAFileThatIsNotItsOwn`.
- **Keep the socket 0600 in the daemon's private directory.** Anyone who can post here can make every Claude loom runs submit a prompt. **Enforced** by `TestListen_TheSocketIsPrivate`.

## Pointers

- [`../daemon/CLAUDE.md`](../daemon/CLAUDE.md) — where the socket lives and when it is served.
- [`../../session/claudemod/CLAUDE.md`](../../session/claudemod/CLAUDE.md) — the module on the other end.
