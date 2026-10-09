# Adding or changing a `core.Core` method

**Symptom:** a method works in a test that drives `core.Model` directly, and in the TUI it returns a zero value, an old value, or `unsupported`; or a query answers one way in process and another through the daemon.

**Cause:** a `Core` method lives in five places at once. `core/iface.go` declares it and its line comment says how a client serves it; `*core.Model` implements it; `*core.Loop` forwards it (`core/loop_core.go`); `core/rpc/methods_gen.go`, generated from `core/iface.go`, carries it over the wire; and a local query is answered on the client by the replica (`core/rpc/replica.go`) from a published state view, not by the model. Each copy can drift from the others.

**Rule:** pick the method's kind first, then touch every point below. The compiler catches some; the tests catch most, but only once their case exists.

## Pick the kind (the line comment in `core/iface.go`)

| Comment | Served | Use it for |
|---|---|---|
| `// rpc:local` | from the client's replica of the published state | a query; whatever it reads must be in a published view |
| `// rpc:cast` | one way, no reply; the server publishes nothing after it | a trigger that changes no published state (it marks, names or starts what publishes on its own when it lands) |
| `// rpc:client` | never leaves the client | `Sync` only |
| none | a request; its reply follows the events it produced | anything that changes published state, or needs an answer |

A comment in the method's doc comment instead of on its line, or an unknown `rpc:` word, makes the generator refuse. Parameter names are the wire's field names: pick them as you would a JSON field.

## Checklist

1. **Declare it in `core.Core`** (`core/iface.go`) with its line comment. Parameters and results are plain data: no func, chan, `sync` type, interface but `error`/`Event`, or model object. *Enforced:* `TestCoreIsValueTyped` (`core/value_boundary_test.go`).
2. **Implement it on `*core.Model`**, under the same name (`Sync` is `syncEvents`). A request that names an instance runs `admit`, and its `precondition` mirrors the key's gate in `app/intents.go`; a `ReqID` parameter gets a `Reply` (`track` when a job answers it). *Silent* until you add a case to `TestRequests_RefuseWhatTheTUIRefuses` (`core/requests_test.go`).
3. **Forward it from `*core.Loop`** in `core/loop_core.go`, calling only its namesake. *Compiler-enforced* that one exists (`var _ Core = (*Loop)(nil)` in `core/iface.go`); *enforced* by `TestLoopForwardsEachMethodToItsNamesake` that each forwarder calls its namesake and nothing else, and that `core/loop_core.go` holds exactly as many forwarders as `Core` has methods.
4. **Regenerate the wire:** `go generate ./core/rpc`. Never edit `core/rpc/methods_gen.go` by hand. *Enforced:* `TestGenerated_IsFresh`, `TestMethods_CoverCore`; `*rpc.Client` is *compiler-checked* against `Core` (`core/rpc/client.go`).
5. **A `ReqID` travels only as a parameter of its own type**, never inside a slice, map, pointer or struct: dispatch tags only a top-level `ReqID` with the connection's number. *Enforced:* the generator refuses it (`TestGenerate_RefusesARequestIDTheServerCannotTag`).
6. **For `rpc:local`:**
   - put the data in a published view (`ModelView`, `AccountsView`, `GitHubView` in `core/state_views.go`, or the workspace views), set where the model changes it, so it publishes. `ModelView` and `AccountsView` are diffed whole by `publishState` (`core/state_publish.go`; `AccountsView` also republishes when `usageGen` moves), and the workspace views by `syncEvents`, but `GitHubView` republishes only when `ghGen` moves, which only `deliverGH` (`core/github.go`) bumps: a GitHub field set anywhere else never reaches a client (*silent*). A field `Clone` drops reaches no client either: *enforced only for the fields their fixtures set* by `TestModelView_CloneKeepsEveryField` and `TestGitHubView_CloneKeepsEveryField`; `AccountsView.Clone` has no such test (*silent*);
   - add the view method, replicating the model's own query;
   - add the replica method in `core/rpc/replica.go` that calls it (*compiler-enforced*: the generated client calls `r.<Name>`);
   - add cases to `TestStateViews_AnswerAsTheModel` or `TestWorkspacesView_AnswersAsTheModel` (`core/state_views_test.go`) and to `TestReplica_AnswersAsTheModel` (`core/rpc/client_test.go`), misses included. *Silent* until the cases exist: a view method that disagrees with the model compiles and answers wrongly through the daemon only.
7. **For a cast:** check it changes no published state. *Silent:* the change would reach clients only with some later, unrelated publish.
8. **Rewrite the protocol reference:** `go test ./core/rpc -run TestProtocolReference -update`. *Enforced:* `TestProtocolReference`, which also runs in the Nix build.
9. **Bump `rpc.Protocol`** (`core/rpc/rpc.go`) only if you removed or renamed a method or parameter, or changed what one means; adding a method is compatible (an older server answers `unsupported`). *Silent:* a rename without a bump decodes as zero values between mixed builds.
10. **Call it from the TUI** through `home.core`. A request that should answer with a `Reply` takes a `ReqID` from the TUI's request book (`m.newReq`, or `m.opReq` for a lifecycle request, `app/requests.go`), which routes the Reply to whatever waits on it; a Reply with an ID the book doesn't hold is dropped and logged as `reply.unexpected` (*silent* to the user). A read right after a request sees its effect (the server sends a request's events before its reply); a read after a cast does not.

## Traps

- **The model's own query and the view method are two implementations.** The model's stays because several run on every tick; the parity tests are the only link between them.
- **A `time.Time` loses its monotonic reading on the wire.** Fixtures compared after the wire use `time.Now().Round(0)`.
- **An error result crosses as a `core.WireError`**: only `ErrNoSession`, `ErrRefused` and `session.ErrStorageLoadFailed` survive `errors.Is`; any other error keeps only its message.
- **Removing a method** leaves its forwarder, view method, replica method and parity cases behind. The compiler accepts a leftover forwarder (an extra method on `*Loop` is legal); `TestLoopForwardsEachMethodToItsNamesake`'s count catches it, and `TestGenerated_IsFresh` the wire. A leftover view or replica method is caught by nothing (its parity cases stop compiling only if they call the removed model method).

## What the gates won't tell you

- Whether the kind is right: a state-changing method declared `rpc:cast` passes every test that reads back through a synchronous test client.
- Whether a new request's `precondition` matches the key's gate in the TUI, unless you added the case.
- Whether a new result type survives JSON: `TestWire_EveryFieldSurvives` (`core/rpc/wire_test.go`) fills and round-trips every field of every event in `EventTypes()` and of the views it lists, so a field added to those is covered, but a request's new result type is checked only by whatever test calls it through the client.
