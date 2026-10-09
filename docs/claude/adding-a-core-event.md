# Adding a `core.Event`

**Symptom:** the model emits the event and nothing happens in the TUI; or it reaches every client when it should reach one; or a burst of them arrives as one, or one is lost under load. The app suite stays green throughout.

**Cause:** an event crosses four layers. The model emits it into its outbox (`core/model.go:emit`); the server decides per connection whether and how to send it (`core/rpc/route.go:forConn`) and whether a newer one replaces it in a queue (`core/rpc/server.go:coalesceKey`); the client's replica keeps it as state or queues it (`core/rpc/replica.go`); and the TUI's `applyCoreEvent` (`app/core_glue.go`) acts on it. `applyCoreEvent` returns nil for an event it has no case for, so a missing applier is silent.

**Rule:** decide what kind of event it is, then touch every point below. Write the applier's test so that it fails without the applier.

## Decide the kind

- **A state event** carries a published view whole (`WorkspacesChanged`, `ModelChanged`, `AccountsChanged`, `GitHubChanged`, `ViewsChanged`). Only the newest matters: the server coalesces it, the replica keeps it, and `Sync` hands out one per kind in the model's publish order. The model publishes it by diffing in `syncEvents` (`core/views.go`) or `publishState` (`core/state_publish.go`), never by hand.
- **An event naming a request** carries `Req` (`Started`, `Recovered`, `Notice`, `Reply`). The server routes it so only the requester sees its own ID.
- **Any other event** is delivered once, in order, to every client, and never dropped or coalesced.

## Checklist

1. **Declare the type** in `core/events.go` with a `coreEvent()` method, holding plain data only. *Enforced:* `TestCoreIsValueTyped` (`core/value_boundary_test.go`).
2. **List it in `EventTypes()`** (`core/events.go`): the wire names and decodes events by that list, so an unlisted event can't cross. *Enforced:* `TestAllEventsListsEveryEvent`, which finds every `coreEvent` method.
3. **Give an error field a wire form** in `core/events_json.go`, as `Notice` and `Reply` do (a `core.WireError` through `ToWire`/`FromWire`); a plain `error` field encodes as `{}`. *Enforced:* `TestWire_EveryFieldSurvives` (`core/rpc/wire_test.go`) fills every field of every listed event by reflection and round-trips it.
4. **A state event:** add its key to `coalesceKey` (`core/rpc/server.go`), keep it in `replica.apply` and hand it out from `replica.events` (`core/rpc/replica.go`), and publish it from the diff in `syncEvents`/`publishState`, in its place in the publish order. *Enforced* both ways by `TestCoalesceKeys_MatchTheReplicasStateEvents`: a key without the replica keeping it, or the reverse, fails.
5. **An event naming a request:** stamp `Req` in `emit` (`core/model.go`) while a cause is ambient, list it in `routed` and give it its case in `forConn` (`core/rpc/route.go`): to the requester alone, or to every client with `Req` set only for the requester. *Enforced:* `TestForConn_CoversEveryEventNamingARequest`; the stamping in `emit` is *silent* unless a `TestCause_…` case (`core/cause_test.go`) covers it.
6. **The TUI's applier:** a case in `applyCoreEvent` (`app/core_glue.go`). *Silent:* an unknown event is ignored. See the trap below for the test.
7. **Rewrite the protocol reference:** `go test ./core/rpc -run TestProtocolReference -update`. *Enforced:* `TestProtocolReference`.
8. **No `rpc.Protocol` bump** for a new event: an older peer logs and drops an event it doesn't know (`client.unknown_event`). Renaming or removing one, or changing a field's meaning, needs the bump.

## Traps

- **A fixture that rebuilds every view hides a missing applier.** `wireCore` (`app/testcore_test.go`) seeds each slot's views and workspace view fresh after the model is installed, so a test that changes the model and then reads through the TUI sees the change whether or not the event was applied. The app suite once stayed green without any `WorkspacesChanged` handling, while production's classic slot, named before its load, went stale. Write the applier's test so the model changes after the fixture is built, then drain through `home.Update` (a `coreWakeMsg`) without calling `syncViews` or `syncWorkspaces`, and assert on what the applier alone changed.
- **Synchronous test clients ping before every read**, so a test can't see a coalescing or ordering bug. Ordering belongs in `core/rpc`'s tests, which use production clients.
- **A cast publishes nothing.** An event the model emits while handling a cast reaches clients only with the next publish.
- **An event emitted while no client is connected** is lost unless it is a `Notice`, which the server's backlog keeps for the next connection.

## What the gates won't tell you

- Whether the applier does the right thing, or exists at all.
- Whether a state event's diff fires on every change: a field the diff doesn't compare never republishes.
- Whether an event the model should emit is emitted on every path that changes the state behind it.
