# Daemon Stage 3R: Live Reconnect

> **For agentic workers:** REQUIRED SUB-SKILL: use superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans. The plan has four coarse packages (A–D). Each package gets one implementer, one
> review and one commit. The numbered steps inside a package are checkpoints, not commits.

**Goal:** a TUI survives the daemon going away. It keeps showing and attaching panes, refuses what needs the
model, and rejoins a daemon when one answers. It spawns a daemon itself only after a crash.

**Architecture:**

- **IDs become stable.** They are derived from the disk, not counted, so two daemons over one disk agree.
- **A graceful stop says goodbye.** The daemon sends a `bye` frame, refuses new requests, and lets in-flight jobs
  publish their replies before it closes.
- **The TUI keeps a link state.** It is connected, stopping, waiting or reconnecting.
- **Offline, the TUI fails stranded requests and gates keys.** Every request waiting on a `Reply` gets a synthetic
  one, and an offline whitelist decides which keys still work.
- **Rejoin swaps clients.** `main` hands `app.Run` a rejoin function (the startup join, made quiet). On success the
  TUI swaps in the new client and resyncs from its replica.

**Spec:** `docs/superpowers/specs/2026-10-03-loom-daemon-design.md`, the "3R, live reconnect" entry under Rollout
(committed in bfbcb40), plus its amendments to §1, §2, §4, §7 and Testing.

**Tech stack:** Go 1.25, `core/rpc` (NDJSON over unix sockets), Bubble Tea v2, tmux.

---

## Decisions

The user's (2026-10-09):

1. **When the daemon goes away, respawn only after a crash.**
   - **Graceful stop or replacement:** the daemon says `bye`. The TUI waits for a daemon to appear and never spawns
     one on its own; `ctrl+r` starts one.
   - **Crash or fatal** (no `bye`): the TUI redials on a backoff (1s doubling to 30s) and spawns a daemon when the
     lock is free. After three failed spawns it drops to waiting.
2. **On rejoin, the newer build wins, as at startup.**
   - The same build is rejoined.
   - A newer daemon makes the TUI exit: "the loom daemon was replaced by a newer loom (X); run loom again".
   - An older daemon is replaced once, under `replaceGuard`.
3. **Stable IDs.**
   - A `WorkspaceID` is a hash of the canonical config dir.
   - An `InstanceID` is a hash of the workspace's canonical config dir, the title and `created_at`.
   - Both are masked to 53 bits, are never 0, and a collision takes the next free value. There is no schema bump.

The coordinator's, from the approved design:

- **States:**
  - *connected*.
  - *stopping*: a `bye` arrived. Requests are refused, and in-flight replies still arrive.
  - *waiting*: the connection closed after a `bye`. The TUI polls for a daemon and never spawns one.
  - *reconnecting*: the connection closed with no `bye`, or a fatal arrived. The TUI backs off and spawns.
  - A banner names the state.
- **Offline keys:** a whitelist (as overview's `overviewKeyAllowed`) passes only what talks to tmux or stays in the
  TUI. Every other key shows an info line. As a backstop, the client refuses requests locally with a new
  `core.ErrUnavailable`, which matches `ErrRefused`.
- **Stranded requests:** every pending `ReqID` that nothing will answer gets a synthetic
  `Reply{Err: ErrUnavailable}` through `handleReply`.
  - While *stopping*: only requests numbered after the `bye` (`ReqID` greater than the watermark recorded then).
  - At the loss: all of them.
- **No `Protocol` bump.**
  - `bye` is an optional `Frame` field. A 3B client's reader decodes past the unknown field, and the frame falls
    through its switch.
  - The `unavailable` wire code is new. An older peer sees only its message.
- **New stop order:** `bye`, stop listening, `Quiesce` with the connections open, `SaveForQuit`, then a final
  publish, flush and close (`Server.Close`).
- **Casts while stopping:** a stopping daemon takes nothing new. The server drops casts as well as refusing requests.
- **Offline UI prefs:** offline, a pref change (the rail, the terminal pane, the view mode, the split ratios) applies
  locally and is remembered as unsent; the resync sends it. Before 3R a failed `SetUIPrefs` dropped the change
  entirely.
- **Quit while offline:** the open-list and ratio writes are model requests, so they fail; the failure is logged
  (`quit.offline_unsaved`).
- **Resync:** the reseed is the new client's first `Sync` drained through the existing appliers.
  - The snapshot arrives as state events, so the `WorkspacesChanged` and `ViewsChanged` appliers do the reseed.
  - The resync adds the steps no applier covers: closing tabs whose workspace is no longer served, `Open`, the
    selection, the tmux re-pin and the pane release.

## Packages

Run them in order: **A → B → C → D**. C depends on A (two stacks must agree on IDs) and on B (`Stopping`,
`ErrUnavailable`, the no-spawn connect). A and B both edit `core/model.go`, so they run one after the other, never in
parallel in one tree.

Every package's gate (run in the foreground):

```bash
gofmt -l $(git ls-files '*.go' | grep -v '^vendor/')     # empty
CGO_ENABLED=0 go vet ./...
CGO_ENABLED=0 GOOS=windows go build ./...
CGO_ENABLED=0 go test ./...
CC=clang CGO_ENABLED=1 go test -race ./core/... ./app/... ./internal/daemon/... .
go test -tags e2e ./e2e/...                               # D, and before the final review
```

Each package also names its **RED checks**: mutations to the new code that make its new tests fail. Report the
failure output of each.

---

### A — stable IDs (core)

**Files:**

- Create `core/ids.go`: `stableID`, `probe`, `wsKey`.
- Create `core/ids_test.go`.
- Modify `core/views.go:10-21` (`idOf`) and `publishViews` (prune the holders).
- Modify `core/workspace_views.go:8-19` (`wsIDOf`) and `publishWorkspaces` (prune the holders).
- Modify `core/model.go:163-174` and `:211-213`: drop `nextID`/`nextWSID`, add `idHolders`/`wsHolders`.
- Fix the tests that assume counted IDs, wherever the suite fails. Expect `core/state_views_test.go`,
  `core/rpc/replica_test.go` and `core/rpc/route_test.go`. Read IDs from views or `IDOfForTest` instead of writing
  `1`, `2`.

**A1. The hash and the probe** (`core/ids.go`):

```go
// idMask keeps an ID within 53 bits, which any JSON client holds exactly
// (a float64's mantissa).
const idMask = 1<<53 - 1

// idHash derives an ID from parts: a var so a test can force a collision.
var idHash = stableID

// stableID hashes parts (NUL-separated) into a non-zero ID within idMask.
// Two daemons over one disk derive the same ID for the same workspace or
// record, so a client that rejoins another daemon keeps naming the same
// things.
func stableID(parts ...string) uint64 {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	id := binary.BigEndian.Uint64(h.Sum(nil)[:8]) & idMask
	if id == 0 {
		id = 1
	}
	return id
}

// probe returns want, or the first value after it that taken refuses,
// wrapping within idMask and skipping 0 (the draft row's ID).
func probe(want uint64, taken func(uint64) bool) uint64 {
	id := want
	for taken(id) {
		id = (id + 1) & idMask
		if id == 0 {
			id = 1
		}
	}
	return id
}

// wsKey is the part of an ID a workspace contributes: its canonical config
// dir ("" for a workspace with no context, a test's).
func wsKey(ws *Workspace) string {
	if ws == nil || ws.ctx == nil {
		return ""
	}
	return canonicalDir(ws.ctx.ConfigDir)
}
```

**A2. `idOf` and `wsIDOf`.**

- Each caches by pointer, as now.
- On a miss, each derives the ID and probes past a holder that is still served.
- A holder that is no longer served gives its ID up. This is how a record rebuilt as a new instance (a retried
  load, a recovery whose title is unchanged) takes back its own ID.
- An instance no served workspace holds gets its would-be ID, uncached: its workspace is part of the ID.

```go
func (m *Model) idOf(inst *session.Instance) InstanceID {
	if id, ok := m.ids[inst]; ok {
		return id
	}
	ws := m.holding(inst)
	want := idHash("instance", wsKey(ws), inst.Title, inst.CreatedAt.UTC().Format(time.RFC3339Nano))
	if ws == nil {
		return InstanceID(want)
	}
	if m.ids == nil {
		m.ids = make(map[*session.Instance]InstanceID)
	}
	if m.idHolders == nil {
		m.idHolders = make(map[InstanceID]*session.Instance)
	}
	id := InstanceID(probe(want, func(v uint64) bool {
		holder, ok := m.idHolders[InstanceID(v)]
		switch {
		case !ok || holder == inst:
			return false
		case m.holding(holder) != nil:
			return true
		}
		delete(m.ids, holder)
		delete(m.idHolders, InstanceID(v))
		return false
	}))
	m.ids[inst], m.idHolders[id] = id, inst
	return id
}
```

`wsIDOf` follows the same shape:

- The key is `idHash("workspace", wsKey(ws))`.
- The holders are `m.wsHolders`.
- "Still served" is `slices.Contains(m.workspaces, holder)`.

`publishViews` and `publishWorkspaces` prune both maps where they prune `ids`/`wsIDs` today. Leave `lookup` as it
is: it runs on every pane event, and its walk is still correct.

Rewrite the comments that promise "never reused within the process" to say what is true now:

- An ID names one record (its config dir, title and `created_at`), on every daemon.
- An instance recreated under a removed one's title has a new `created_at`, so it gets a new ID.

Places to update:

- `core/model.go` field docs.
- `views.go`.
- `workspace_views.go`.
- The `InstanceID`/`WorkspaceID` type docs (`core/view.go:17`, `core/workspace_view.go:9`).

**A3. Tests** (`core/ids_test.go`). Use the boot fixtures in `core/boot_test.go` (`workspaceDef`, `bootModel`,
`openDef`):

- `TestStableID_IsWithin53BitsAndNeverZero`: run many inputs; also check that `probe` wraps at `idMask` to 1.
- `TestProbe_TakesTheNextFreeValue`.
- `TestIDs_TwoModelsOverOneDiskAgree`.
  - Boot two models over the same workspace defs with instances.
  - The second model's registry lists one extra workspace first, so per-model counters would disagree.
  - Every shared workspace's ID (matched by config dir) and every instance's ID (matched by title) must be equal.
- `TestIDs_ARecordRecreatedUnderAKilledTitleGetsANewID`.
- `TestIDs_ARetriedLoadKeepsItsInstancesIDs`: fail a load, then `Open` once it can load (`retryLoad`).
- `TestIDs_ARecoveredOrphanKeepsItsPlaceholdersID`: `Recover` a `Recoverable`; the `Reply.ID`/`Recovered.ID` equals
  the placeholder's.
- `TestIDs_ACollisionProbesPastALiveHolder`.
  - Swap `idHash` for a constant: two live instances get `want` and `want+1`.
  - Remove the first and add a third: it takes `want` back.

**RED checks:**

- Make `idOf` count (`m.next++`): `TwoModelsOverOneDiskAgree` fails.
- Drop the "holder no longer served" branch: `RecoveredOrphanKeepsItsPlaceholdersID` fails.
- Drop `wsKey` from the instance hash: a collision test with two workspaces sharing a title and `created_at` fails.
  Write that test.

**Acceptance:** the package gate passes. `go test ./core/rpc -run TestProtocolReference` passes unchanged: IDs are
values, not wire shape.

---

### B — the wire and the daemon: bye, unavailable, stop order, no-spawn connect

**Files:**

- Modify `core/requests.go:20-35` to add `ErrUnavailable`.
- Modify `core/wire_error.go` to add `CodeUnavailable`, `Is`, and `ToWire`'s order.
- Modify `core/rpc/rpc.go:101` (`Frame.Bye`).
- Modify `core/rpc/server.go`: `Bye`, the stopping refusals, the late joiner, and `Close` flushing.
- Modify `core/rpc/server_conn.go` (a bounded flush).
- Modify `core/rpc/client.go`: the reader, `Stopping`, the local refusals, and the loss error.
- Modify `core/rpc/wire_test.go` (the protocol reference text), then regenerate `docs/specs/protocol.md`.
- Modify `internal/daemon/serve.go:142-155` (the stop order).
- Modify `internal/daemon/connect.go`: `ConnectNoSpawn`, `ErrNoDaemon`, `ErrDidNotStart`.
- Modify `core/model.go` `Options` and `core/requests.go:442`: `Options.GHExec`, `FetchIssue`'s executor (nil means
  `internalexec.Default{}`). Existing DI pattern; it gives the daemon test a foreground job it can hold.
- Create `core/rpc/bye_test.go`.
- Extend `internal/daemon/serve_test.go` and `connect_test.go`.

**B1. `ErrUnavailable`.**

```go
// ErrUnavailable refuses a request the model cannot take: the daemon is
// stopping, or the client has lost it. It matches ErrRefused, so the TUI's
// refusal path shows it.
var ErrUnavailable error = refusedError{errors.New("the loom daemon is unavailable")}
```

- `CodeUnavailable = "unavailable"`.
- `WireError.Is`: `CodeUnavailable` matches `ErrUnavailable` and `ErrRefused`.
- `ToWire` tests `ErrUnavailable` before `ErrRefused`.

**B2. The frame and the server.**

- `Frame` gains `Bye string \`json:"bye,omitempty"\`` (the reason: `"stopping"`).
- `Server.Bye()`:
  - Under `s.mu`, it sets `s.stopping` and queues `Frame{Bye: "stopping"}` on every connection in `s.conns`.
  - A connection that finishes its hello while stopping gets the bye after its snapshot and backlog (in
    `serveConn`).
- `handle`, while stopping:
  - A request (`f.ID != 0`, ping included) is answered
    `&core.WireError{Code: core.CodeUnavailable, Message: "the loom daemon is stopping"}` without being
    dispatched. It still goes through `publishLocked`, so ordering holds.
  - A cast is dropped and logged at debug (`server.cast_while_stopping`).
- `Close` becomes graceful:
  1. Under the lock: close `done`. If not fatal, `publishLocked()` once more, so every result the loop delivered
     before Close is published even if `wakeLoop` has not run yet. Snapshot the connections.
  2. Unlock, then let every connection's writer flush in parallel. Each flush is bounded by `closeFlushTimeout`; add
     `(*serverConn).flush()`, the first half of `close()`.
  3. Close every `nc`.

  The fatal path uses `Close` too, so the `fatal` frame now reaches the peer instead of racing the close.

**B3. The client** (`core/rpc/client.go`):

- `stopping bool` under `c.mu`. The reader sets it on `f.Bye != ""` and signals. Put the `case f.Bye != "":` ahead
  of `f.ID != 0` in `read`'s switch.
- `func (c *Client) Stopping() bool`.
- **The loss error tells graceful from crash.** In `fail`, when the loss is not the client's own Close:
  - If `stopping`, then `c.fatal = &core.WireError{Code: core.CodeUnavailable, Message: "rpc: the loom daemon stopped"}`.
  - Otherwise keep `CodeError` "rpc: connection to the model lost: …".
  - A fatal frame keeps its own code (`panic`, `protocol`).
  - The TUI reads `errors.Is(c.Err(), core.ErrUnavailable)` as "graceful".
- **Local refusals** (non-raising clients only; an in-process client still raises): once stopping, lost or closed,
  `request` returns at once, without writing:

  ```go
  unavailable(fatal) = &core.WireError{Code: core.CodeUnavailable, Message: "the loom daemon is " + why}
  ```

  - `why` is "stopping", or "unavailable: " plus the loss's message.
  - `cast` drops (as it does after a loss today).
  - `requestNoErr` keeps logging.
  - `Err()` is unchanged: nil until the loss. Stopping is not a loss.
- `handshake`'s ping: a `bye` met before the ping's reply makes `Dial` fail (the ping is refused locally). A
  stopping daemon can't be joined.

**B4. The stop order** (`internal/daemon/serve.go`, the `<-o.Stop` case):

```go
case <-o.Stop:
	log.For("serve").Info("serve.stopping")
	srv.Bye()   // every client hears it first, and nothing new is taken
	l.close()   // no new connections
	// The jobs in flight finish and publish their replies to the connections
	// still open; then every workspace is saved.
	if err := stopModel(loop, o.QuiesceTimeout); err != nil {
		log.For("serve").Error("serve.save_failed", "err", err)
	}
	srv.Close() // a last publish, each connection flushed, then closed
	loop.Stop()
	l.remove()
	log.For("serve").Info("serve.stopped")
	return nil
```

The fatal case keeps its order: no bye, since a crash is what reconnecting is for.

**B5. The no-spawn connect** (`internal/daemon/connect.go`):

- Split `Connect`'s body into `connect(globalDir string, timeout time.Duration, spawnOK bool)`.
  - `Connect(globalDir, timeout)` calls `connect(…, true)`; its behaviour is unchanged.
  - `ConnectNoSpawn(globalDir, timeout)` calls `connect(…, false)`.
- With no spawn:
  - When nothing holds the lock, the `default:` branch returns `ErrNoDaemon` at once.
  - It never extends to `bootTimeout`: a booting daemon is polled again by the caller.
- `ErrDidNotStart`: keep the message text, so the "did not start" error becomes
  `fmt.Errorf("%w: %s%s%s", ErrDidNotStart, …)`.

```go
// ErrNoDaemon: ConnectNoSpawn found no daemon holding the lock.
var ErrNoDaemon = errors.New("no loom daemon is running")

// ErrDidNotStart: a daemon Connect spawned exited before it served.
var ErrDidNotStart = errors.New("the loom daemon did not start")
```

**B6. Tests:**

- `core/rpc/bye_test.go`, over `core.StartForTest` loops served by `NewServer` with production `Dial` clients over
  `net.Pipe`:
  - `TestBye_ReachesEveryConnection`: two clients both report `Stopping()`; neither reports `Err()`.
  - `TestBye_ARequestAfterItIsRefusedLocally`: `errors.Is` `ErrUnavailable` and `ErrRefused`. A recording backend
    proves nothing reached the server.
  - `TestBye_TheServerRefusesARequestThatCrossedIt`: a raw conn writes a request after the bye and gets
    `CodeUnavailable`. The backend recorded no call.
  - `TestBye_InFlightReplyArrivesBeforeClose`.
    - A `FetchIssue` with a `ReqID`, whose job the loop holds (`JobsForTest`), then `Bye`.
    - Run the job and `DeliverForTest` its result, then `srv.Close()`.
    - The client's `Sync` holds the `Reply` for that request, and only then does `Err()` report the loss.
  - `TestBye_LossAfterItIsGraceful`: `errors.Is(Err(), ErrUnavailable)`. And `TestLoss_WithoutByeIsNot`.
  - `TestBye_AJoinerWhileStoppingFailsToDial`.
  - `TestClient_IgnoresAnUnknownFrameField`: the scripted server (`scripted_test.go`) sends
    `{"bye":"stopping","zzz":1}` and then an event; the event still applies. This is the compatibility argument for
    a 3B client.
- `internal/daemon/serve_test.go`:
  - `TestServe_StopSaysByeAndAnswersInFlightRequests`.
    - An in-process daemon whose model's `GHExec` blocks `gh issue view` until released.
    - The client sends `FetchIssue` with a `ReqID`, then the stop closes.
    - The client reports `Stopping()`. Release the executor.
    - The `Reply` arrives, then the loss (graceful).
    - `serve.log` has `serve.stopped` and no `serve.stopped_with_jobs_in_flight`.
- `internal/daemon/connect_test.go`:
  - `TestConnectNoSpawn_FindsNoDaemon`: returns `ErrNoDaemon` fast; `spawn` was never called.
  - `TestConnectNoSpawn_DialsARunningDaemon`.
  - `TestConnect_ADaemonThatDidNotStartIsErrDidNotStart`.
- Regenerate the protocol reference (`go test ./core/rpc -run TestProtocolReference -update`). The generator's text
  in `wire_test.go` now says:
  - `{"bye":"stopping"}` says the daemon is stopping: requests are refused (`unavailable`), replies to requests in
    flight still come, then the connection closes.
  - It also lists the `unavailable` code.

**RED checks:**

- Revert the stop order (`srv.Close()` before `stopModel`): the in-flight test fails.
- Drop `Close`'s final publish/flush: `InFlightReplyArrivesBeforeClose` fails.
- Drop the reader's bye case: the `Stopping` tests fail.
- Make `ConnectNoSpawn` spawn: its test fails.

**Acceptance:** the package gate passes, and so does the existing `core/rpc`, `internal/daemon` and root suite. A 3B
TUI still works against a 3R daemon: it ignores the bye and exits on the close, as before.

---

### C — the TUI: link states, offline, rejoin, resync

**Files:**

- Create `app/link.go`, holding:
  - the link state and banner;
  - the offline whitelist;
  - `failStranded`;
  - rejoin scheduling and its messages;
  - `resync`.
- Create `app/link_test.go` and `app/reconnect_test.go`.
- Modify `app/app.go`:
  - `home` fields (`:134-154`).
  - `Update` (`:637-660`): check the link after the drain.
  - `mutateUIPrefs` (`:609`) for offline.
  - The banner in `View`.
- Modify `app/accounts.go:204`: `topChromeHeight` counts the banner row.
- Modify `app/app_init.go`:
  - `Run`'s signature.
  - Store `p.Send`.
  - `defer func() { h.stopCore() }()`.
  - Retire `ErrDaemonGone`; add `ErrDaemonNewer` and `ErrDaemonDidNotStart`.
- Modify `app/state_default.go`: `ctrl+r`, and the offline gate ahead of the workbench and overview branches.
- Modify `app/requests.go`: `refusal` prefixes its `what` for `ErrUnavailable`, as for `ErrNoSession`.
- Modify `app/core_glue.go`: `forwardWakes` per client.
- Modify `app/app.go` `handleQuit`: log `quit.offline_unsaved` when offline.
- Modify `main.go:151-158` (rejoin, and the newer-daemon message) and `daemon_client.go` (`join(spawn)`, quiet,
  `newerDaemonError`, `rejoinDaemon`).
- Rewrite `app/daemon_client_test.go:22` `TestDaemonGone_QuitsCleanly` as `TestDaemonGone_GoesOffline`.
- Extend the root's `daemon_client_test.go`.

**C1. The joins** (root package):

```go
type daemonLink struct {
	own        rpc.Hello
	dial       func(spawn bool) (*rpc.Client, rpc.Hello, int, error)
	stop       func(pid int) error
	mayReplace func(daemonTmux string) error
	say        func(string) // a replacement announced: stderr at startup, the banner on rejoin
}

// join(spawn): the first dial spawns only when spawn; after stopping an older
// daemon it always spawns, since this build must start the replacement.
func (d daemonLink) join(spawn bool) (*rpc.Client, error)

// newerDaemonError: the daemon's build is newer than this loom's. Its
// message is today's ("…: upgrade loom, or run `loom serve stop`"); it
// matches app.ErrDaemonNewer, which makes a rejoining TUI exit.
type newerDaemonError struct{ peer, own rpc.Hello }
```

- `dialDaemon(globalDir string, spawn, quiet bool)`:
  - `spawn` selects `daemon.Connect(globalDir, connectTimeout)`; otherwise
    `daemon.ConnectNoSpawn(globalDir, waitDialTimeout)` with `waitDialTimeout = 2 * time.Second`.
  - `quiet` drops the "waiting for the loom daemon…" line. Under the alt screen it would draw over the TUI.
- `joinDaemon` (startup) is `join(true)`, with `say` writing to stderr.
- `rejoinDaemon(globalDir, spawn, say)` is `join(spawn)`, quiet. It wraps `daemon.ErrDidNotStart` as
  `fmt.Errorf("%w: %w", app.ErrDaemonDidNotStart, err)` and `daemon.ErrNoDaemon` as-is. It is logged under
  `subsystem=app` (`rejoin.failed`) at debug for `ErrNoDaemon` and at warn otherwise.
- `main.go`:
  - It passes `func(spawn bool, say func(string)) (*rpc.Client, error) { return rejoinDaemon(globalDir, spawn, say) }`
    to `app.Run`.
  - `errors.As(err, &newer)` prints
    `loom: the loom daemon was replaced by a newer loom (<describeBuild(newer.peer)>); run loom again`.
  - The `ErrDaemonGone` message goes.

**C2. The link state** (`app/link.go`):

```go
// Rejoin connects to a daemon again: the startup join, quiet. spawn lets it
// start one; say reports progress for the banner.
type Rejoin func(spawn bool, say func(string)) (*rpc.Client, error)

type linkState int

const (
	linkConnected    linkState = iota
	linkStopping               // a bye arrived: requests refused, replies in flight still come
	linkWaiting                // closed after a bye: poll for a daemon, never spawn (ctrl+r spawns)
	linkReconnecting           // closed with no bye, or a fatal: redial on a backoff, spawning
)

type link struct {
	state      linkState
	byeReq     core.ReqID // m.nextReq when the bye arrived
	attempt    int        // reconnect attempts since the loss
	spawnFails int        // consecutive rejoins whose spawned daemon did not start
	gen        int        // bumped on every state change: a stale tick or result is dropped
	busy       bool       // a rejoin is running
	note       string     // the banner's detail: the last rejoin's progress or error
}

const maxSpawnFails = 3
const waitPoll = time.Second

// rejoinBackoff is the delay before reconnect attempt n (1-based): 1s, 2s,
// 4s, … capped at 30s.
func rejoinBackoff(n int) time.Duration
```

- **`home` fields.** `home` gains:
  - `conn *rpc.Client`, replacing `wakes` and `coreLost`. It is nil in bare test homes, which skip every link
    check.
  - `link link`.
  - `rejoin Rejoin` (nil in tests that don't set one: offline then stays offline).
  - `send func(tea.Msg)` (Run's `p.Send`; nil in tests).
  - `daemonTmux string` (the peer's tmux server at the last join).
  - `stopCore` stays: the client's `Close`, or a test stack's stop.
- **`Update`:**

  ```go
  model, cmd := m.update(msg)
  cmd = tea.Batch(cmd, m.drainCore(), m.checkLink())
  m.publishSelection()
  ```

  The link is checked after the drain, so replies that arrived before a close are applied first.
- **`checkLink`:**
  - *connected*:
    - `conn.Err() != nil` → `lose`.
    - Else `conn.Stopping()` → *stopping*, with `byeReq = m.nextReq`, the banner shown and a re-layout.
  - *stopping*: `Err()` → `lose`; else `failStranded(m.link.byeReq)`.
  - *waiting* or *reconnecting*: `failStranded(0)`.
- **`lose`:**
  1. Drain once more.
  2. `m.stopCore()`. Its wakes close, so the old `forwardWakes` ends.
  3. The state becomes *waiting* when `errors.Is(err, core.ErrUnavailable)` (graceful), else *reconnecting*.
  4. `failStranded(0)`.
  5. Schedule the first tick:
     - *reconnecting*: `tea.Tick(rejoinBackoff(1), …)`.
     - *waiting*: `tea.Tick(waitPoll, …)`.
- **`failStranded(after)`:**
  - Every pending request numbered above `after`, in ascending order, gets
    `m.handleReply(core.Reply{Req: req, Err: core.ErrUnavailable})`.
  - The flows end as their replies do:
    - A `pendingOp` shows "kill x: the loom daemon is unavailable".
    - A send releases its hold.
    - A Lua call resumes raising.
    - A create shows its error.
    - An issue fetch ends with its message.
- **Messages:**
  - `rejoinTickMsg{gen}`: when `gen` is current, the TUI is offline and not busy, it sets `busy` and returns a Cmd.
    - The Cmd closes over `rejoin`, `spawn` and a `say` that sends `rejoinNoteMsg{gen, text}` when `m.send` is set.
      All three are captured on Update.
    - It returns `rejoinedMsg{gen, spawn, client, err}`.
    - `spawn` is true in *reconnecting*.
  - `rejoinedMsg`:
    - Clear `busy`.
    - Success → `resync(client)`.
    - `errors.Is(err, ErrDaemonNewer)` → `m.exitErr = err; return tea.Quit`.
    - In *reconnecting*, `errors.Is(err, ErrDaemonDidNotStart)` increments `spawnFails`. At `maxSpawnFails` the state
      drops to *waiting*, and the note names `serve.log` and `ctrl+r`.
    - Otherwise, schedule the next tick: the backoff for `attempt+1`, or `waitPoll` while waiting.
    - Show any other error as the banner's note; log the rest. `ErrNoDaemon` while waiting is the normal case.
  - `rejoinNoteMsg`: when `gen` is current, its text becomes the banner's note (for example "replacing the loom
    daemon (…) with this build…").
- **`ctrl+r`.** Handled in `handleStateDefaultKey` right after `ctrl+c`, and only while offline.
  - In *waiting* or *reconnecting* and not busy: reset `spawnFails` and start an attempt now with `spawn = true`.
  - In *stopping*: the info line says the daemon is still stopping.
  - While connected it falls through to the scripts (unbound by default).
- **The banner.** One row above the tab bar while not connected, counted in `topChromeHeight`. Every state change
  re-lays out through `updateHandleWindowSizeEvent` with the last size. Its style is theme-hooked, like the account
  strip's warning, using roles only. The texts:
  - *stopping*: `⚠ the loom daemon is stopping: finishing its jobs in flight`
  - *waiting*: `⚠ the loom daemon stopped: waiting for one to start (ctrl+r starts it)` + note
  - *reconnecting*: `⚠ lost the loom daemon: reconnecting (attempt N)` + note `· ctrl+r retries now`

**C3. The offline gate.** `offlineKeyAllowed` sits in `state_default.go`, beside `overviewKeyAllowed`, and applies
after `ctrl+c`/`ctrl+r` and before the workbench and overview branches.

- **Allowed** (tmux or TUI-only):
  - `up`, `k`, `down`, `j`, `]`, `[`, `tab`, `\`, `T`, `ctrl+up`, `ctrl+down`, `{`, `l`, `}`, `;`
  - `i`, `ctrl+a`, `ctrl+t`, `alt+a`, `alt+t`, `t`, `d`, `f`, `c`, `e`
  - `?`, `q`, `enter`, `esc`, `z`
  - `pgup`, `pgdown`, `home`, `end`, `ctrl+u`, `ctrl+d`, `shift+up`, `shift+down`, `alt+pgup`, `alt+pgdown`
  - `K`, `J`, `g`, `G`
  - `1`–`5`, `ctrl+left`, `ctrl+right`
- **Refused:** everything else, including `n N I D p s m r R W S a` and keys user scripts bound. A refused key sets
  the info line `the loom daemon is <stopping|stopped|unreachable>: <key> needs it` and dispatches nothing.
- **Only the default state is gated.** An overlay already open when the link drops keeps its keys, and its commit
  fails through the client's local refusal, shown as an error.
- **UI prefs.** Offline, `mutateUIPrefs` sets `m.info.UIPrefs` and marks the slot `prefsUnsent`. `resync` sends each
  marked slot's prefs after the reseed, so the TUI's own value wins.
- **Quit.** `handleQuit` offline logs `quit.offline_unsaved` (open list, ratios) and quits.

**C4. `resync(c *rpc.Client)`** (on Update):

1. Swap in the new client:
   - `m.core`, `m.conn = c`.
   - `m.stopCore = c.Close`.
   - When `m.send` is set, `go forwardWakes(c.Wakes(), m.send)`.
   - Bump `link.gen`.
2. `ReloadRegistry` (as `startHome` does), then `drainCore()`: the new client's first `Sync` carries its snapshot,
   which the `WorkspacesChanged`/`ViewsChanged` appliers apply. The bells and ladder prune; the selection is kept by
   ID (stable, A).
3. **Workspaces the new daemon doesn't serve** (unregistered meanwhile):
   - A tab showing one is closed (`deactivateWorkspace`). The last tab goes to global mode (`enterGlobalMode`).
   - A classic slot showing one falls back to global (`enterGlobalMode`).
   - Each gets a notice: "<name> is no longer registered; closed its tab".
4. `Open` every slot shown (`openSlots()`). This is the new daemon's first open, so its terminal and GitHub polling
   start. Show a failed open's error (`handleError`), and keep `failedOpen` as it was.
5. Send the unsent UI prefs (C3).
6. `m.sentSelected = 0`, so `publishSelection` re-sends it.
7. **A different tmux server.** When `c.Peer().Tmux != m.daemonTmux` (the old server died): the join already pinned
   the new one, so release every pane client (`m.panes.Retain(nil)` → `releaseClientsCmd`) and every slot's
   terminal-pane clients (`DetachAll`). Then `ensureSlotPanes` for each open slot, and set `m.daemonTmux`.
8. Reset the link (*connected*, counters zeroed), hide the banner, re-lay out, and show the info
   "reconnected to the loom daemon".

Drafts, overlays, the workbench, the review and scroll positions are not touched.

**C5. Tests.** These use production `rpc.Dial` clients over `net.Pipe`, as `TestDaemonGone_QuitsCleanly` does.

- **Disk sharing:** a test gets its second stack by booting `core.NewForTest` over the same `LOOM_GLOBAL_DIR` and
  workspaces, then `StartForTest`, `NewServer`, a pipe and `Dial`. `home.rejoin` returns it.
- **Synchronizing:** since these clients aren't synchronous, wait with `require.Eventually` on `conn.Stopping()`/
  `conn.Err()`, then `Update(coreWakeMsg{})`.
- **Ticks:** tests never run `tea.Tick` Cmds. They send `rejoinTickMsg{gen}` directly, and run the attempt Cmd
  synchronously.

`app/link_test.go`:

- `TestRejoinBackoff`: 1, 2, 4 … capped at 30s.
- `TestLink_ByeEntersStopping`:
  - The banner shows.
  - `j` moves the selection.
  - `D` shows the offline info and sends nothing (the model's selection set and request log are unchanged).
- `TestLink_StrandedRequestsFail`. One of each pending kind (`op`, `send`, `script`, `create`, `issue`):
  - At the bye, only those numbered after the watermark fail.
  - At the loss, the rest fail.
  - The send hold is released.
  - The Lua coroutine resumes with an error naming the daemon.
  - The create's error is shown.
- `TestLink_GracefulLossWaitsAndNeverSpawns`: the tick calls rejoin with `spawn=false`.
- `TestLink_CrashReconnectsSpawning`: no bye, so `spawn=true`, and the backoff grows.
- `TestLink_ThreeFailedSpawnsDropToWaiting`.
- `TestLink_CtrlRSpawnsWhileWaiting`.
- `TestLink_ANewerDaemonExits`: `exitErr` matches `ErrDaemonNewer`, and the result is `tea.Quit`.
- `TestOfflineKeyGate`: a table of allowed and refused keys.
- `TestOffline_UIPrefsApplyAndAreSentOnResync`.
- `TestDaemonGone_GoesOffline`: the old test's body. Renders, local reads and keys don't panic. The TUI no longer
  quits; it reconnects.

`app/reconnect_test.go`:

- `TestReconnect_KeepsTabsSelectionAndDraft`. Two tabs, a selected row and an open draft. After a crash-loss and a
  rejoin onto stack B:
  - The same slots (same IDs) and the same selected ID.
  - The draft is still there.
  - Stack B's model opened both workspaces (add a read-only seam if none exists, e.g. `Loop.OpenedForTest(id)` in
    `core/seams.go`).
  - B's selection set (`SelectedForTest`) holds the ID.
  - The info line says reconnected.
- `TestReconnect_ClosesATabTheNewDaemonDoesNotServe`: stack B's registry lacks one workspace.
- `TestReconnect_ReleasesPanesOnAnotherTmuxServer`: B's server `SetTmux` names another path. Every client is
  released and the pin is the new path.
- `TestReconnect_AFailedOpenShowsItsError`.

Root `daemon_client_test.go`:

- `TestJoin_SpawnFlag`: the first dial gets the caller's flag; the dial after a replacement spawns.
- `TestJoin_ANewerDaemonIsNewerDaemonError`: `errors.Is(err, app.ErrDaemonNewer)`, and the message is unchanged.
- `TestRejoin_IsQuiet`: nothing is written to stderr; `say` hears the replacement.

**RED checks:**

- Check the link before the drain: a reply that arrived before the close is failed instead of applied.
- Drop `failStranded` at the loss: the stranded test fails.
- Make the graceful loss reconnect: `GracefulLossWaitsAndNeverSpawns` fails.
- Drop `sentSelected = 0`: the reconnect test's selection assertion fails.
- Drop the offline gate: `TestOfflineKeyGate` fails.

**Acceptance:** the package gate passes. `TestTUIHoldsNoModelObject` passes; the TUI still holds only a client.

---

### D — e2e, loomdev, docs

**Files:**

- Modify `e2e/daemon_test.go`: the `daemonGone` const (`:31`), `TestE2E_Daemon_KilledUnderAnOpenTUI` (`:238`) and
  `TestE2E_Daemon_NewerBuildReplacesIt` (`:306`), plus the new scenarios.
- Modify `tools/loomdev/cmds.go`: `loomdev daemon`, plus its tests in `tools/loomdev/main_test.go`.
- Modify `.claude/skills/loom-dev/` (the new subcommand).
- Docs (main split CLAUDE.md per package; keep `go test ./tools/claudemd/...` green, since it fails on stale
  identifiers):
  - The root `CLAUDE.md` (CLI usage and how the daemon's loss is described).
  - `app/CLAUDE.md`, `core/CLAUDE.md`, `core/rpc/CLAUDE.md`, `internal/daemon/CLAUDE.md`, `e2e/CLAUDE.md`,
    `internal/devsandbox/CLAUDE.md`, `tools/CLAUDE.md`.
  - Any `docs/claude/*.md` naming `ErrDaemonGone` or "Run loom again".
  - `USAGE.md`: the offline behaviour, the banner, `ctrl+r`, and quitting offline.
  - The spec's 3R amendments, turned from "planned" into "as built".
  - This plan's outcome.

**D1. e2e** (`go test -tags e2e ./e2e/...`):

- `TestE2E_Daemon_KilledUnderAnOpenTUIReconnects`:
  1. Create `keeper` and select it. SIGKILL the daemon.
  2. The banner says reconnecting.
  3. A new daemon serves, with a new pid.
  4. The banner goes, `keeper` is listed and still selected (`Agent · dev/keeper`), and the agent's pid is
     unchanged. The TUI did not exit.
- `TestE2E_Daemon_ServeStopWaitsAndCtrlRStartsOne`:
  1. Run `loom serve stop` through `sb.Cmd`.
  2. The banner says stopped.
  3. For 3s no process holds the lock, so nothing respawned.
  4. Press `C-r`: a daemon serves and the sessions are listed with their agents' pids.
- `TestE2E_Daemon_NewerBuildReplacesIt`: the older TUI now exits with "replaced by a newer loom" (no panic).
- `TestE2E_Daemon_PauseAcrossAStop`:
  1. Press `s` on a session and run `loom serve stop` immediately.
  2. Press `C-r`.
  3. The session reads Paused, never stuck in Loading.
  4. `serve.log` has `serve.stopped` and no `serve.stopped_with_jobs_in_flight`.
- Update the other scenarios still matching `daemonGone`: keep the ones whose TUI really exits, otherwise match the
  banner.

**D2. loomdev:** `loomdev daemon [--stop | --kill]`.

- With no flag it prints the sandbox daemon's record (pid, socket, build, tmux).
- `--stop` stops it gracefully, leaving the TUI running (`StopDaemon`).
- `--kill` SIGKILLs it, but only once `runsSandboxLoom` proves the pid runs a build from the sandbox's bin dir.
- The smoke run uses it.

**D3. Docs**, written from a fact sheet of what was built (as `facts-E.md` was in 3B). Record:

- **The link states** (connected, stopping, waiting, reconnecting) and what each does.
- **The offline key gate** and the client's local refusal.
- **The stop order:** bye, quiesce with connections open, save, then close.
- **Stable IDs:** the 1C promise that an ID is "never reused within the process" becomes "an ID names one record
  on every daemon".
- **Resync:** what it rereads and what it keeps.
- **Gotchas:**
  - `Update` checks the link after the drain.
  - `Run`'s deferred close is a closure.
  - A new modal state commits through the backstop.

**Acceptance:** e2e green twice in a row. `go test ./tools/claudemd/...` is green.

---

## Execution

The user approved this execution (2026-10-09, "looks good"). The setup is the same as 3B's:

- **Roles:**
  - An implementer per package, writing from a brief.
  - One standing Opus reviewer that also re-checks every fix round.
  - A fresh final cross-cutting reviewer.
- **Smoke run:** before the final review, a sandbox smoke run (`loomdev`) against a base sandbox built from
  `6b8af29`. It covers:
  - a SIGKILL under two TUIs (both reconnect; one spawns, the other joins);
  - `loomdev daemon --stop` (both wait), then `ctrl+r`;
  - a pane rendered and attached while offline;
  - a Lua script waiting across a stop.
- **Rules** (memory `loom-subagent-review-lessons.md`):
  - Tests run in the foreground.
  - No `git stash`, amends or rebases.
  - Commit with explicit `-- <paths>`, and check that a file the plan calls new doesn't exist before writing it.
  - Each package is committed when green, with its RED checks reported. Then it is reviewed, and the filer
    re-checks each fix wave.
- **Afterwards:** update memory `loom-scrum-daemon-direction.md` with the 3R outcome.

## Outcome and follow-ups

Executed 2026-10-10. To save tokens, each package had one implementer and one combined spec-and-quality review, and
the implementer fixed what its review found. D was reviewed in the final review. Then came a sandbox smoke run and the
final cross-cutting review, whose findings were fixed and re-checked. The gate is green: gofmt, vet, the Windows
build, `go test ./...`, the race run and e2e (17 tests, twice).

| Commit | What |
|---|---|
| f7370bf | the plan |
| 951cfc2, 4d1a83b | A: stable IDs; an orphan placeholder's ID from its worktree's timestamp |
| 8ae2af4, e069edb | B: bye, the stop order, the no-spawn connect; `Bye` waits for the replies of the calls it let in |
| 24b3bd5, 0e83e31 | C: link states, the offline gate, rejoin, resync; requests refused around the bye fail at once |
| be4241a, 5c1bef6 | D: e2e and `loomdev daemon`; the docs |
| 0c2d4d6, 9d221d6 | the final review's fixes |

### What the reviews found

- **A:**
  - A Recoverable placeholder took its `created_at` from the time it was found, so its ID differed on every daemon.
    It now comes from the worktree's `_<hex>` suffix.
  - One test passed with counted IDs.
- **B:**
  - `Bye` counted a call done before its reply was queued, so a stop could close a connection ahead of that reply.
  - Two READMEs were stale.
- **C:**
  - A request refused in the moment of the bye, or one that crossed it, stayed pending until the loss (up to 30s
    plus the save). The client now records the ReqIDs it refused (`TakeRefused`, emitted by the generator).
  - An offline `SetLastUsed` was never resent.
  - The classic-slot fallback was untested.
- **Final review:**
  - **Important:** every successful rejoin reset the backoff and the spawn-failure count, so a daemon dying soon
    after each join was respawned about once a second, forever. A loss within 30s of a rejoin (`stableLink`) now
    counts as a failed start.
  - **Important:** `PauseAcrossAStop` raced the stop against the pause. Its hook now writes a marker the test
    waits for.
  - The docs wrongly said every script-bound key is refused offline.
  - `loomdev daemon --kill` could report a kill that did not happen.
- **Smoke run** (sandbox, 5 scenarios, all passed):
  - a SIGKILL under one TUI, and under two (both rejoined one new daemon);
  - `daemon --stop`, then waiting with no respawn, offline keys, and `ctrl+r`;
  - an inline attach while offline;
  - a quit while offline.

### Deviations from the plan

- `app` imports `internal/daemon` for `ErrNoDaemon`.
- The workbench editor and review take keys before the offline gate. Review `S` cannot send.
- The generated client records refused ReqIDs, and `requestNoErr` returns its error.
- `home.now` is a clock seam, `Server.afterCall` a test hook, `core.Options.GHExec` an executor, and
  `Loop.OpenedForTest` a seam.

### Follow-ups

- `TestE2E_Daemon_StaleSocket` flakes ("the dead daemon's lock is free"). The flake predates 3R.
- A worktree with no timestamp suffix (made by an old loom) still gives its orphan placeholder a per-daemon ID.
- `ctrl+r` is the app's only while offline. It is not in the keymap or help.
- `app/CLAUDE.md` is at about 19.3k of its 20k budget.
- When the third early death drops the TUI to waiting, the log line still says `state=reconnecting`.
- A 3R TUI on a 3B daemon (possible only on a build tie) treats `serve stop` as a crash and respawns.
- 3C is next: the subcommands as clients, and the duties a daemon has with no TUI.
