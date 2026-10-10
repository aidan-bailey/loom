# core/rpc

The wire: `core.Core` carried over a connection as newline-delimited JSON, between the TUI and the daemon over the daemon's unix socket, to any number of clients at once. The full frame and method reference is [`docs/specs/protocol.md`](../../docs/specs/protocol.md), generated from this package; everything logs under `subsystem=rpc`.

## Files

| File | Holds |
|---|---|
| `rpc.go` | `Frame`, `Hello`, `rpc.Protocol`, the `//go:generate` line |
| `build.go` | `rpc.Build`, `rpc.Self()`, `rpc.SetVersion`, `rpc.commitUnix`, `CompareBuilds`, `knownExeTime` |
| `methods_gen.go` | generated from `core/iface.go` by `internal/gen`: each method's `…Params` and `…Result`, the `methods` table, the server's `dispatch`, the client's methods |
| `server.go`, `server_conn.go` | `NewServer`, `Server`, `Backend`, publishing, the notice backlog, `coalesceKey`, `serverConn` |
| `route.go` | `tagReq`, `local`, `forConn`, `routed`, `reqFor`, `connBackend` |
| `client.go`, `replica.go` | `Dial`, `Client`, `handshake`, `requestNoErr`, the `replica` |
| `inprocess.go` | `InProcess` |
| `seams.go` | `InProcessForTest`, `FlushForTest` |

## Frames

Each side's first frame is a hello (`Hello`):
- `protocol`: `rpc.Protocol`, bumped on any incompatible change;
- `build`: from `debug.ReadBuildInfo` (`rpc.Build`);
- optional: `version` (main's release, `rpc.SetVersion`), `time` (`vcs.time`, else the commit time stamped at link time into `rpc.commitUnix`, which `flake.nix` sets from `self.lastModified`, since a Nix build has no VCS information), `modified` (`vcs.modified`), `exe` (the SHA-256 of the running executable, read through `/proc/self/exe` where there is one, so a rebuild in place doesn't change a running process's answer), `exe_time` (that file's mtime), and, from a server only, `tmux`, the tmux server its sessions run on (`Server.SetTmux`).

`rpc.Self()` is this binary's hello. The server sends its hello first, even to a peer of another protocol, then answers that peer `mismatch`, so `Dial`'s `*MismatchError` carries the server's hello and a newer client can still replace an older daemon.

After the hellos come requests (`id`, `method`, `params`), replies (`id`, `result`, `error` as a `core.WireError`), casts (a request with no `id`, which gets no reply), events (`event`, the event's Go type name, and `data`) `fatal` (the model panicked, or the server could not encode an event) and `bye` (the server is stopping gracefully: `{"bye":"stopping"}`, under Server below). Request-ID routing is in the reference's "Request IDs" section.

## Builds

`CompareBuilds` (`build.go`) says which of two hellos is newer: the same `exe` is the same build; otherwise the higher `version` (as semver), then the later `time`, then a modified tree over a clean one, then the higher `protocol` (a peer that sent none, 0, is older), then, only when both name their commit time, the newer `exe_time` (a dev build rebuilt at one commit replaces its sandbox's daemon). A field either side lacks decides nothing, and neither does an `exe_time` at or before 1970-01-01T00:00:01Z (`knownExeTime`: a Nix store dates every file there). A full tie is the same build, so two installs of one release keep whichever daemon runs, a Nix build and a release binary of one commit keep each other's, and two Nix builds are ordered by their stamped commit times. A build that names no commit (a plain `go build` from a tarball) at the release of the running daemon keeps it; `loom serve stop` switches. The policy that acts on it is the TUI's join (`daemon_client.go`, [`../../internal/daemon/README.md`](../../internal/daemon/README.md)).

## Generated code

`methods_gen.go` is generated from `core/iface.go` by `internal/gen` (`go generate ./core/rpc`). It holds each method's `…Params` (one field per parameter, named after it) and `…Result` (`{"value":…,"ok":…}`; an error travels in the reply's `error`), the `methods` table with each method's kind, the server's `dispatch` (which takes a `tag func(*core.ReqID) error` and calls it on every `core.ReqID` parameter before the call), and the client's methods. A method's wire name is its Go name.

The generator refuses an `rpc:` directive in a doc comment, an unknown line comment, and a parameter carrying a `ReqID` anywhere but as its own type (in a slice, map or pointer, or a field of a core struct, at any depth: `nestedReqID`, over core's type declarations, which `gen.CoreSources` reads beside `core/iface.go`), since dispatch tags only a parameter of type `ReqID` and a nested one would reach the model as its client numbered it. Tests: `TestGenerate_TagsEveryRequestID`, `TestGenerate_RefusesARequestIDTheServerCannotTag` (`internal/gen/gen_test.go`), `TestGenerated_IsFresh` (stale `methods_gen.go`), `TestMethods_CoverCore` (a `Core` method missing from the table).

Compatibility: adding a method, an event or an optional field needs no bump (a peer answers a method it lacks with `unsupported`, ignores a field it does not know, and drops an event it does not know); removing or renaming a method, event or field, or changing what a field means or a frame's shape, needs a `Protocol` bump (its comment in `rpc.go` states the rule).

## Server

The server (`server.go`, `server_conn.go`) serves a `Backend` (`core.Core` plus `SyncAndSnapshot`, `SetSelection` and `Wakes`, all of which `*core.Loop` has) to any number of connections, which it numbers (`serverConn.n`). It publishes (the backend's `Sync`, to every connection) after every request and on every loop wake, and queues a request's reply only after that publish, so a request's events reach the client ahead of its reply. `rpc.Ping` is a request that only publishes and replies: the client's barrier. A cast publishes nothing (pane events cast many times a second per session), and its failure is only logged (`server.cast_failed`). The server answers `rpc:local` methods as requests too, for a client without a replica. A peer that sends no hello within `helloTimeout` is dropped.

A connection is sent its hello, then its snapshot, then the notices no connection was there for. The snapshot comes from one loop call, `Loop.SyncAndSnapshot`: its `Sync`'s events go to the connections already there and its `Snapshot` to the new one, so a late joiner's replica starts from the state the next `Sync` diffs against (`TestServe_ALateJoinerStartsFromThePublishedBaseline`).

**The notice backlog.** A `Notice` published while no client is connected, and those a daemon hands over with `Keep` (its boot's, raised before any client could be), wait for the next connection; a request's `Notice` whose client has gone goes to the connections still there, and is kept only when there are none. The backlog keeps the newest `maxBacklog` and logs each notice it keeps (`server.notice_kept`), so even one no client reads is on record (`server_backlog_test.go`).

**Queues.** Each connection's outbound side is a queue its own writer goroutine drains (`serverConn`), so a slow client never blocks the server. A state event replaces a queued one of the same kind in place (`coalesceKey`: workspaces, model, accounts, github, and each workspace's views), so the queue holds at most one of each. Replies and other events are never dropped or reordered, because a resync could not rebuild a lost `Reply` and a script would hang on it; the non-state queue is unbounded (replies are bounded by the requests in flight).

**Panics and close.** The server recovers a panic in a call, a `Sync` or the snapshot: the request's reply carries code `panic`, a `Fatal` frame goes to every connection, a later one included, and `Server.Fatal()` closes (`FatalError` says why), on which the daemon exits. An event that will not encode is fatal too (`CodeProtocol`, naming the event, `server.encode_event_failed`), because dropping it could strand a `Reply`'s requester. The server tracks every connection from `Serve` on, so `Close` ends even one that has not said hello, and `Serve` after `Close` closes its connection at once. A connection's close waits at most `closeFlushTimeout` for a peer that never reads.

**Stopping.** `Server.Bye` starts a graceful stop. It sends every connection a `bye` frame (an optional `Frame` field, so a peer that does not know it decodes past it and the frame matches nothing it reads: no `Protocol` bump), and from then on the server takes nothing new: `handle` answers a request `unavailable` (`core.CodeUnavailable`, queued after a publish like any reply, so the ping barrier included) without reaching the backend, and drops a cast (`server.cast_while_stopping`). A connection that joins meanwhile is sent the bye after its snapshot. `Bye` returns once the calls already let in (`Server.calls`, counted under the lock while not stopping) have their replies queued, so the `Loop.Quiesce` the daemon runs next waits on every job those calls started, and their replies still reach their clients as the jobs land. `Server.Close` then publishes once more (a reply no wake has published yet), flushes each connection in parallel (`serverConn.flush`) and closes them. A model that failed gets no bye, only the `Fatal` frame and the close, so its clients take it for a crash (`TestBye_ReachesEveryConnection`, `TestBye_WaitsForTheRepliesOfTheCallsItLetIn`, `TestBye_TheServerRefusesARequestThatCrossedIt`, `TestBye_InFlightReplyArrivesBeforeClose`, `TestBye_AJoinerWhileStoppingFailsToDial`).

## Routing

Each client numbers its requests itself, from 1 and below 2^32, so request IDs collide across clients. The server keeps them apart without a table: dispatch's tag (`tagReq`, `route.go`) ORs the connection's number into the high 32 bits of every request ID it hands the model (0, no Reply wanted, stays 0; an ID at or above 2^32 is a `protocol` error), and at publish each connection is sent each event as `forConn` says, with the number masked out again (`local`): a `Reply`, and a request's `Notice`, go to the client that made it alone, with its own ID; `Started` and `Recovered` go to every client, since each attaches the session's pane, naming the request (`Req`) only to the one that made it (`reqFor`; 0 for every other). A table would leak: a `Create`'s `Reply` comes at once and its `Started` much later, so it would never know when to forget an entry. The events reach the right request because the model stamps them (`Model.cause`). `routed` lists the events that name a request.

**Selection per connection.** The server calls a connection's methods through `connBackend`, whose `SetSelected` records that connection's selected row (`setSelected`; 0 forgets it) and hands the model every connection's, in connection order without repeats, through `Backend.SetSelection` (`Loop.SetSelection`); a closed connection's is dropped (`dropSelection`).

## Client

`Dial` (`client.go`) sends its hello, checks the server's (`Peer`), starts the reader, and returns after a ping (`handshake`), by which time the replica (`replica.go`) holds the snapshot. A fatal frame met during that ping is `Dial`'s error; one landing after it is the client's loss.

The reader handles every frame in the order the server wrote them. It applies each state event to the `replica`, marking what changed; queues the other events; records a fatal; and hands each reply to the call waiting for it. An unknown event name (a newer peer's) is logged and dropped (`client.unknown_event`). A known event that fails to decode is fatal (`CodeProtocol`), since it may be a `Reply` someone waits for or state the replica would hold stale.

The replica answers every `rpc:local` query through the state views' methods and hands out copies. It ignores a `ViewsChanged` for a workspace it does not hold: the server's queue coalesces a `WorkspacesChanged` in place, so one that drops a workspace can arrive ahead of a `ViewsChanged` queued for it, which would otherwise bring the dropped workspace's views back for good. `Sync` hands out the newest state once per kind, in the model's publish order, then the other events in arrival order. `Wakes` signals arrivals; wakes coalesce, and `Close` closes the channel once the reader has stopped.

A request returns the method's error (`core.FromWire`). A request for a method with no error to return logs its failure (`requestNoErr`, `client.request_failed`). A reply with code `panic` or `protocol` is fatal; an `unsupported` one is the method's ordinary error. A connection lost without `Close` is fatal the same way (`client.connection_lost`). What fatal means depends on the client:
- a daemon's client (`Dial`) panics nowhere: it records the loss (`Err`) and signals `Wakes`; local reads answer from the last replica, requests are refused at once as unavailable (`core.ErrUnavailable`, a refusal, its message naming the loss) and casts are dropped, so the TUI can go offline cleanly (`TestPanic_ADaemonsClientReportsIt`, `TestConnectionLost_IsFatal`). A loss after the server's bye is a stop, and `Err` matches `core.ErrUnavailable`; one without is a crash (`TestBye_LossAfterItIsGraceful`, `TestLoss_WithoutByeIsNot`);
- an in-process client (`InProcess`, which sets `raise`) panics with the `WireError` in the caller, and so does every later call, local reads included (`TestInProcess_RaisesTheModelsPanic`).

A server that says bye (`Stopping`) makes the client refuse every request as unavailable without writing it and drop every cast, while replies to the requests in flight still arrive. A request refused as unavailable, by the client or by a server it crossed the bye with, is recorded by its `ReqID` (`noteRefused`) and handed out once by `TakeRefused`: no `Reply` will ever answer it, and its requester fails it itself (`TestBye_ARequestAfterItIsRefusedLocally`, `TestClient_RecordsTheRequestsRefusedAsUnavailable`). `Close` marks the client closing first, so its own close is not a loss, and a request after it is refused as unavailable (naming `errClosed`) and a cast dropped. `request` and `cast` copy the fatal and closed state under `c.mu` and panic only after unlocking.

## In process

`InProcess(model)` (`inprocess.go`) takes a model booted already (`core.Model.Boot`) and runs `core.Start(model)`, `NewServer`, `net.Pipe` and a raising client, then `Loop.Begin` once the client has dialled (`TestInProcess_BeginsTheLoop` checks that the loop ticks). It returns the client and a stop function that closes the client, the server and the loop. No production code calls it; tests use it for the production stack in one process (`app`'s `TestRealLoop_…`, this package's client tests). `InProcessForTest` (`seams.go`) is the same over a `core.StartForTest` loop, which holds its jobs and is returned for its seams, with a synchronous client; `FlushForTest` is the ping.

## Tests

This package's tests run production (non-synchronous) clients; `app`'s use the synchronous `InProcessForTest` client.
- `TestReplica_AnswersAsTheModel` compares every local query through the client with the loop's raw answer (times as UTC instants, errors as text), so a field the codec drops shows.
- `TestWire_EveryFieldSurvives` fills every field of every event and state view by reflection and round-trips it.
- `TestCoalesceKeys_MatchTheReplicasStateEvents` keeps the server's coalescing keys in step with the events the replica keeps.
- A scripted server end (`scripted_test.go`) sends frames a real `Server` never does: an unknown event, one that won't decode, a `protocol` reply.
- Others cover events before replies, casts, coalescing, wakes, panics, a lost connection against `Close`, encode failures, the hello and the late joiner (`Loop.SyncAndSnapshot` has its own, in `core/loop_test.go`). `route_test.go` covers routing (colliding IDs, a request's `Notice` and `Started` naming it only to its client, every client's selection reaching the model), `build_test.go` covers `CompareBuilds` and `Self`, and `server_backlog_test.go` the notice backlog, `Fatal` and the hello deadline.
- `docs/specs/protocol.md` is written from the method table and `core.EventTypes()` by `go test ./core/rpc -run TestProtocolReference -update`; `TestProtocolReference` fails when it is stale.
