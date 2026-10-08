# Daemon Stage 2: the Wire and the Client Replica — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The TUI talks to the model over a connection.
- `core/rpc` carries `core.Core` as newline-delimited JSON.
- A `Server` serves the model's `core.Loop`.
- A `Client` implements `core.Core`. It keeps a full replica of the model's published state, so only actions cross the wire.
- In this stage the connection is an in-memory pipe inside the TUI's own process.

**Architecture:**
- **Published state (core).** The model publishes everything a query answers from.
  - **What:** the workspace views (now saying whether they are the classic workspace), the instance views, and three new views (`ModelView`, `AccountsView`, `GitHubView`), carried by `ModelChanged`, `AccountsChanged` and `GitHubChanged`.
  - **When:** each is diffed at `Sync`. `Snapshot` hands a new client all of them.
  - **Shared logic:** the view types' methods hold the queries' logic, and parity tests prove they answer as the model does.
- **The wire (`core/rpc`).**
  - **Frames:** a hello (protocol 1), requests and replies, one-way casts, events tagged by type name, and a fatal frame.
  - **Generated code:** each method's named parameters, the method table, the server's dispatch and the client's methods are generated from `core/iface.go`.
  - **Errors:** they cross as `core.WireError`, which keeps the sentinels the TUI tests.
- **The server.**
  - **Ordering:** it publishes after every request and before the reply, so a request's events reach the client ahead of its reply. It also publishes on every loop wake.
  - **Casts:** they publish nothing.
  - **Queue:** each connection's outbound queue coalesces state events.
- **The client.**
  - **Replica:** it answers the 25 `rpc:local` queries from its replica.
  - **Calls:** it waits for each request's reply and sends casts one way.
  - **Sync and Wakes:** `Sync` returns the newest state, then the other events in order. Wakes signal arrivals.
  - **Panics:** it re-raises the model's panics.
- **The TUI.** `newHome` starts the in-process stack (`rpc.InProcess`), and app's tests run over the same pipe.

**Tech Stack:** Go 1.25, encoding/json, go/ast and go/format (the generator), net.Pipe, testify.

---

## Where this stage fits

The daemon spec is `docs/superpowers/specs/2026-10-03-loom-daemon-design.md`, Rollout stage 2. Stage 1 (1A–1E) put the model behind a value-typed `core.Core` on its own goroutine (`core.Loop`). This stage puts a wire between the two.

Stage 3 adds the daemon process: `loom serve`, the socket and lock, the newer-side-wins handshake, reconnect and respawn, and the subcommands as clients.

The user's long-term direction is a cloud loom server talking to many hosts' daemons. The wire is shaped not to close that off.

## Decisions

1. **The client keeps a full replica (the user's choice, 2026-10-08: "the most robust for the direction we're going in").**
   - Every query is answered from the replica, and only actions cross the wire.
   - The alternatives were synchronous round trips everywhere, or a replica of the instance and workspace views only.
2. **Named parameters, generated from `core/iface.go` (the user's choice).**
   - **Generator:** `core/rpc/internal/gen` reads the `Core` interface and writes `core/rpc/methods_gen.go`: each method's `…Params` and `…Result` types, the `methods` table, `dispatch`, and the client's methods. `TestGenerated_IsFresh` fails when it is stale.
   - **Names:** a method's wire name is its Go name, and its fields are its parameter names. Terse names are renamed because they are now wire keys: `a`→`auth`, `s`→`settings`, `p`→`prefs` or `program`, `n`→`number`, `def`→`workspace`.
   - **Results:** a result encodes as `{"value":…,"ok":…}`, and an error travels in the reply.
   - **Evolution:** a newer peer can add an optional field without breaking an older one.
3. **How a client serves a method is a line comment in `iface.go`:**
   - `// rpc:local` (25 queries) answers from the replica;
   - `// rpc:cast` (8 notifications) is one way;
   - `// rpc:client` (`Sync`) never leaves the client;
   - every other method (34) is a request.

   The server answers local methods as requests too, for clients without a replica.
4. **The published state.**
   - **New views:** `ModelView` (agent program, default remote-control auth, registry, restore-failed and open names), `AccountsView` (names, loaded and extra flags, Claude program, accounts, auth, link reports, usage and its errors) and `GitHubView` (snapshots, poll errors, availability).
   - **Classic flag:** `WorkspacesChanged` gains `Classic`.
   - **Publishing:** `syncEvents` publishes, in order, the workspaces, then the model, account and GitHub views (`publishState`), then the instance views. The model and account views are diffed whole. The GitHub view is republished when a poll lands: `ghGen`, bumped in `deliverGH`, the only place its state changes.
   - **Removed emits:** the explicit `AccountsChanged{}` and `GitHubChanged{}` emits go, replaced by the diffs. They now also fire on every change, the default auth included.
   - **Snapshot:** `Model.Snapshot()` and `Loop.Snapshot()` return all of it without moving the diff baseline.
   - **Event list:** `EventTypes()` is the production list (it was a test-only `allEvents`).
5. **The model keeps its own queries.**
   - The view types' methods replicate their logic. The model's own queries stay as they are, because several run on every tick and rebuilding a view for each would be waste.
   - Parity is proven twice. `core/state_views_test.go` compares view and model directly. `core/rpc/client_test.go` compares client and loop over the wire, misses included. Both were shown to bite.
6. **Errors on the wire.**
   - **Codes:** `core.WireError{Code, Message}` uses `not_found` (`ErrNoSession`, which is also `ErrRefused`), `refused`, `storage` (`session.ErrStorageLoadFailed`), `error`, `panic`, `protocol` and `mismatch`. `ToWire` and `FromWire` convert, and `FromWire(nil)` is a nil interface, never a typed nil.
   - **Custom encodings:** `Notice`, `Reply` and `git.DiffStats` encode their `error` fields this way. The TUI shows errors only as text and tests only those three sentinels.
   - **No conflict with persistence:** `DiffStats`'s persisted form is `DiffStatsData`, which is unchanged.
7. **A request's events arrive before its reply.**
   - The server publishes after the call and before queueing the reply, so a client that reads right after a request sees its effect.
   - The read-after-write sites 1E kept (`syncViews`, `syncWorkspaces`, the drains after transitions, the nested drain in `newLaunchOptionsOverlay`) keep working, with no extra round trip.
   - A `rpc.Ping` request is the barrier: publish, then reply.
8. **Casts publish nothing.**
   - Every cast was checked. `SetSelected`, `MarkOutput`, `PaneOutput`, `PaneQuiet`, `VerifyDead`, `ExpediteGitHub`, `RequestAccountsRefresh` and `RequestUsageProbe` set flags or start jobs, and those jobs publish when they land, on the loop's wake.
   - Pane events cast up to about 60 times a second per session, so skipping the publish saves a full view build per cast.
   - `iface.go` states the rule for future casts.
9. **Coalescing instead of drop-and-resync (amends spec §4).**
   - On the server, each connection's outbound queue replaces a queued state event with a newer one of the same kind, in place, so a slow client queues at most one of each.
   - On the client, `Sync` hands out the newest state once per kind, then the other events in arrival order.
   - Replies and non-state events are never dropped: a resync could not rebuild a lost `Reply`, and a script would hang on it.
10. **Panics.**
    - The server recovers a panic in a call, `Sync` or `Snapshot`, replies with code `panic`, and broadcasts a `Fatal` frame.
    - The client panics in the caller, and in every later call, local reads included. The TUI's Bubble Tea recovery then restores the terminal, as in 1E.
11. **Hello.**
    - Each side sends `{protocol, build}`, and a different protocol gets `mismatch`. The build string comes from `debug.ReadBuildInfo`.
    - The newer-side-wins handshake is stage 3's.
12. **The in-process topology.**
    - `rpc.InProcess(model)` runs `core.Start(model)`, a `Server`, `net.Pipe` and `Dial`, and returns the client and a stop function, which closes the client, the server and the loop.
    - `newHome` calls it through `startCore`, and the TUI holds the client as its `core.Core`, with `wakes` from `Client.Wakes()`.
13. **Tests run over the pipe** (spec "Testing": the TUI keeps its tests against an in-process Core over `net.Pipe`).
    - **App tests:** they use `rpc.InProcessForTest`, a hold-mode loop and a *synchronous* client that pings before every local read and after every cast, so tests meet the model as they did in 1E. The race detector found three tests reading the model right after a cast, and this fixed all three.
    - **`core/rpc` tests:** these use production (non-synchronous) clients.
    - **`TestRealLoop_…`:** this runs the TUI on the production stack.
14. **Time over the wire.** A `time.Time` keeps its instant but loses its monotonic reading, which `reflect.DeepEqual` compares. Fixtures compared through the client use `time.Now().Round(0)`. One app test needed it.
15. **What stage 2 leaves for stage 3 and later.**
    - **Stage 3:** the socket, the handshake's version policy, reconnect and resubscribe (a re-dial gets a fresh snapshot), several clients, a selection per client, and the subcommands as clients.
    - **Bounds:** the non-state queue is unbounded, though replies are bounded by in-flight requests.
    - **Event size:** `ViewsChanged` re-sends a whole workspace's views on any change, the selected session's full diff included. Per-instance deltas would trim that.

## Out of scope

- The daemon process, the unix socket, the lock, spawn on demand, the handshake's version policy, reconnect, and subcommands as clients (all stage 3).
- A network transport, auth, and a cloud hub. The wire doesn't preclude them: it is transport-agnostic, assumes nothing about who dialed, and uses named fields.
- Pane streaming. Panes still attach to local tmux.

## Packages

| Pkg | Delivers | Commit |
|---|---|---|
| **A** | Core: the published state a replica needs (views, events, publishing, Snapshot, EventTypes), wire errors, JSON for error fields, iface directives and renames | `feat(core): publish the whole state a replica needs` |
| **B** | `core/rpc`: the generator, frames and codec, Server, Client and replica, InProcess, the protocol reference | `feat(rpc): the wire, a server and a replicating client` |
| **C** | The TUI over the pipe: `newHome` on `rpc.InProcess`; app tests on `rpc.InProcessForTest` | `refactor(app): the TUI talks to its model over the wire` |
| **D** | CLAUDE.md, the spec, full verification, sandbox smoke run, outcome | `docs: CLAUDE.md and spec for the wire (daemon stage 2)` |

## Conventions for every package

- **Build and test.** Work from the repo root. Build with `CGO_ENABLED=0`. For race runs, use `CC=clang CGO_ENABLED=1 go test -race …`. Format with `gofmt -w $(git ls-files '*.go' | grep -v '^vendor/') <new files>`, never `gofmt -w .`. Run `go vet`, not golangci-lint. Run e2e with `-count=1`: it builds the binary at run time, so a cached pass proves nothing.
- **Generated files.** Never edit `core/rpc/methods_gen.go` by hand. After any change to `core.Core`, run `cd core/rpc && CGO_ENABLED=0 go run ./internal/gen/cmd`. After any wire change, run `CGO_ENABLED=0 go test ./core/rpc -run TestProtocolReference -update`, and bump `rpc.Protocol` if the change is incompatible.
- **Git.**
  - Never use `git stash`. Stay on the branch. Never run `./loom` (use `go run ./tools/loomdev`). No test may reach the developer's tmux server or `~/.loom`. Never write the word `exec` immediately followed by `(` in any file.
  - This environment marks new files intent-to-add. A scratch file that is created and then deleted leaves a ` D` entry; clear it with `git rm --cached`.
  - Before writing a file this plan calls new, check it doesn't exist (`git ls-files <path>`).
- **Applying the code.** Each package gives the exact code that was compiled, tested and race-checked against `58261b9` while this plan was written.
  - **Modified files:** a unified diff. Save it to the scratchpad and run `git apply --check` first, then `git apply`.
  - **New files:** given in full.
  - **If an apply fails** because the tree moved, apply the hunks by hand and record the deviation.
- **Assertions.** Never weaken or delete one. Get the count with `git grep -h 'assert\.\|require\.' -- '*_test.go' ':!vendor' | wc -l` (8618 at `58261b9`; 8741 with this plan's code). List every assertion whose meaning changed.
- **The plan is a first draft.** Adapt minimally where the code differs and record the deviation. If a difference needs a design decision, stop and ask.
- **Tests run in the foreground** with a generous timeout.
- **Commit trailers.** Commits end with:
  ```
  Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_016BYqFKpSvoVwusd7gJdYPr
  ```

## File structure

| File | Pkg | Responsibility |
|---|---|---|
| `core/wire_error.go` (new) | A | `WireError`, the codes, `ToWire`, `FromWire` |
| `core/state_views.go` (new) | A | `WorkspacesView`, `ModelView`, `AccountsView`, `GitHubView` and their query methods; `Clone`s; `CloneViews`, `CloneWorkspaceViews` |
| `core/state_publish.go` (new) | A | `workspacesView`, `modelView`, `accountsView`, `githubView`, `publishState`, `Snapshot` |
| `core/events_json.go` (new) | A | JSON for `Notice` and `Reply` |
| `core/events.go`, `model.go`, `views.go`, `workspace_views.go`, `accounts.go`, `usage.go`, `github.go`, `loop.go`, `iface.go` | A | The events' payloads, `ModelChanged`, `EventTypes`, publish order, `Classic`, the removed emits, `ghGen`, `Loop.Snapshot`, the directives and renames |
| `session/git/diff.go` | A | `DiffStats` JSON |
| `core/rpc/internal/gen/gen.go`, `cmd/main.go` (new) | B | The generator |
| `core/rpc/methods_gen.go` (generated) | B | Wire types, `methods`, `dispatch`, the client's methods |
| `core/rpc/rpc.go` (new) | B | Package doc, `Protocol`, `Frame`, `Hello`, kinds, the event codec |
| `core/rpc/server.go`, `server_conn.go` (new) | B | `Server`, `Backend`, publishing, the coalescing queue |
| `core/rpc/client.go`, `replica.go` (new) | B | `Client`, the replica |
| `core/rpc/inprocess.go`, `seams.go` (new) | B | `InProcess`; `InProcessForTest`, `FlushForTest` |
| `docs/specs/protocol.md` (generated) | B | The wire's reference |
| `app/app_init.go`, `app/testcore_test.go`, `app/app_test.go`, `app/core_wake_test.go`, `app/usage_test.go` | C | `startCore`; the test stack |
| `CLAUDE.md`, the daemon spec, this plan | D | Docs, outcome |

---
## Package A: the published state a replica needs (core)

Core only. App keeps calling `core.Loop` directly and needs no change. One commit at the end.

### A1. Modified files

- [ ] **Step 1:** Save this diff as `$SCRATCH/pkgA.diff` (any scratch path), then run `git apply --check $SCRATCH/pkgA.diff && git apply $SCRATCH/pkgA.diff`. It changes:
  - **`model.go`:** the published baselines and `ghGen`;
  - **`events.go`:** the payloads, `ModelChanged`, `EventTypes`;
  - **`workspace_views.go`:** `Classic`;
  - **`views.go`:** the publish order;
  - **`accounts.go`, `usage.go`, `github.go`:** the removed emits and `ghGen++`;
  - **`loop.go`:** `Snapshot`;
  - **`iface.go`:** the directives, the renames and the doc;
  - **`session/git/diff.go`:** the JSON;
  - **the tests:** `value_boundary_test.go` (`allEvents = EventTypes()`), `accounts_test.go` (count through `Sync`, which publishes, not `Drain`), and `view_test.go` (the new publish order, and `instanceEvents` dropping the state events).

```diff
diff --git a/core/accounts.go b/core/accounts.go
index bae4bc8..02e128d 100644
--- a/core/accounts.go
+++ b/core/accounts.go
@@ -167,16 +167,15 @@ func (m *Model) accountDirs() map[string]string {
 	return m.accounts.Dirs()
 }
 
-// publishAccounts hands the registry to session (launch env) and the TUI
-// (badges, through AccountsChanged). Call after every registry change.
-// Loop goroutine only.
+// publishAccounts hands the registry to session (launch env); the TUI
+// learns of the change from the next Sync's AccountsChanged. Call after
+// every registry change. Loop goroutine only.
 func (m *Model) publishAccounts() {
 	var loadErr error
 	if m.accounts != nil {
 		loadErr = m.accounts.LoadErr()
 	}
 	session.SetAccountDirs(m.accountDirs(), loadErr)
-	m.emit(AccountsChanged{})
 }
 
 // ReloadAccounts re-reads accounts.json, which another loom or a `loom
@@ -404,7 +403,6 @@ func (m *Model) deliverAccountsRefreshed(msg accountsRefreshed) {
 	for name, err := range msg.errs {
 		log.For("account").Warn("sync.failed", "account", name, "err", err.Error())
 	}
-	m.emit(AccountsChanged{})
 }
 
 // AccountsLoaded reports whether the registry exists and loaded, so its
diff --git a/core/accounts_test.go b/core/accounts_test.go
index 1e51c62..e509a5b 100644
--- a/core/accounts_test.go
+++ b/core/accounts_test.go
@@ -227,17 +227,17 @@ func TestMaybeReloadAccounts_AnUnchangedFileIsNotReread(t *testing.T) {
 }
 
 // TestReloadAccounts_AChangeIsReportedOnce: a change another process made
-// reaches the TUI as one AccountsChanged (publishAccounts emits it), and
-// a reload that finds nothing changed as none.
+// reaches the TUI as one AccountsChanged (the next Sync publishes the
+// changed account view), and a reload that finds nothing changed as none.
 func TestReloadAccounts_AChangeIsReportedOnce(t *testing.T) {
 	m := NewForTest(Options{})
 	main := withAccounts(t, m)
-	m.Drain()
+	m.Sync()
 	_, _, err := otherTerminal(t, m).Create("max-2", main)
 	require.NoError(t, err)
 	accountsChanged := func() int {
 		n := 0
-		for _, ev := range m.Drain().Events {
+		for _, ev := range m.Sync().Events {
 			if _, ok := ev.(AccountsChanged); ok {
 				n++
 			}
@@ -404,8 +404,8 @@ func TestAccountRequests_RefuseWithoutARegistry(t *testing.T) {
 }
 
 // TestInitAccounts_FillsTheStripOnce: the startup publication tells the
-// TUI once (AccountsChanged), which fills the strip when newHome drains;
-// a second event only repeated the same refresh.
+// TUI once (AccountsChanged, at the first Sync), which fills the strip
+// when newHome drains; a second event only repeated the same refresh.
 func TestInitAccounts_FillsTheStripOnce(t *testing.T) {
 	noCredentialOverride(t)
 	global := t.TempDir()
@@ -419,7 +419,7 @@ func TestInitAccounts_FillsTheStripOnce(t *testing.T) {
 	m.InitAccounts()
 
 	changed := 0
-	for _, ev := range m.Drain().Events {
+	for _, ev := range m.Sync().Events {
 		if _, ok := ev.(AccountsChanged); ok {
 			changed++
 		}
diff --git a/core/events.go b/core/events.go
index 97fb3fb..caa5d78 100644
--- a/core/events.go
+++ b/core/events.go
@@ -117,17 +117,34 @@ type HealthChecked struct{}
 
 func (HealthChecked) coreEvent() {}
 
-// GitHubChanged reports a GitHub poll applied: the TUI refreshes an open
-// issue picker.
-type GitHubChanged struct{}
+// GitHubChanged carries the GitHub poll's state whenever a poll lands
+// (and on the first Sync): the TUI refreshes an open issue picker, and a
+// replica answers the GitHub queries from it.
+type GitHubChanged struct {
+	View GitHubView
+}
 
 func (GitHubChanged) coreEvent() {}
 
-// AccountsChanged reports that the account registry, an account's auth,
-// sync report or usage changed: the TUI refreshes every view showing
-// accounts (refreshAccountViews) and whether account UI shows at all
-// (ui.SetShowAccounts).
-type AccountsChanged struct{}
+// AccountsChanged carries the account state whenever the registry, an
+// account's auth, sync report or usage, or the Claude program changed
+// (and on the first Sync): the TUI refreshes every view showing accounts
+// (refreshAccountViews) and whether account UI shows at all
+// (ui.SetShowAccounts), and a replica answers the account queries from
+// it.
+type AccountsChanged struct {
+	View AccountsView
+}
+
+// ModelChanged carries the model's own state (the agent program, the
+// default account's remote-control auth, the registry, the workspaces
+// that failed to restore) whenever it changed, and on the first Sync. The
+// TUI does nothing with it; a replica answers those queries from it.
+type ModelChanged struct {
+	View ModelView
+}
+
+func (ModelChanged) coreEvent() {}
 
 func (AccountsChanged) coreEvent() {}
 
@@ -137,6 +154,8 @@ func (AccountsChanged) coreEvent() {}
 // everything after it see the new workspace views.
 type WorkspacesChanged struct {
 	Views []WorkspaceView
+	// Classic says Views is the classic workspace alone (no tab is open).
+	Classic bool
 }
 
 func (WorkspacesChanged) coreEvent() {}
@@ -170,3 +189,15 @@ type Reply struct {
 }
 
 func (Reply) coreEvent() {}
+
+// EventTypes returns a zero value of every concrete Event type: what a
+// codec decodes events into, by type name. TestAllEventsListsEveryEvent
+// keeps it complete.
+func EventTypes() []Event {
+	return []Event{
+		AccountsChanged{}, Alive{}, ClientsStale{}, GitHubChanged{},
+		HealthChecked{}, InstancesChanged{}, ModelChanged{}, Notice{},
+		Reactivated{}, Recovered{}, Reply{}, SessionLaunched{}, Started{},
+		StatusesChanged{}, ViewsChanged{}, WorkspacesChanged{},
+	}
+}
diff --git a/core/github.go b/core/github.go
index 702464d..9922b1d 100644
--- a/core/github.go
+++ b/core/github.go
@@ -189,7 +189,8 @@ func ghPollJob(req ghPollRequest, r internalexec.Executor) Job {
 }
 
 // deliverGH applies a poll result: replaces ghState wholesale and
-// re-joins every instance.
+// re-joins every instance. It is the one place the GitHub state changes,
+// so it bumps ghGen, which republishes GitHubChanged.
 func (m *Model) deliverGH(msg ghResult) {
 	m.ghAvailable = msg.available
 	for repo, err := range msg.errs {
@@ -201,7 +202,7 @@ func (m *Model) deliverGH(msg ghResult) {
 		m.ghBases = msg.bases
 	}
 	m.applyGitHubState()
-	m.emit(GitHubChanged{})
+	m.ghGen++
 }
 
 // baseFor returns the resolved base ref for repo, or "" before the
diff --git a/core/iface.go b/core/iface.go
index f8faf09..6c68049 100644
--- a/core/iface.go
+++ b/core/iface.go
@@ -14,29 +14,44 @@ import (
 // model's own objects (TestCoreIsValueTyped). Instances are named by
 // InstanceID and seen as InstanceView values, workspaces by WorkspaceID
 // and WorkspaceView; the registry and accounts cross as copies, and every
-// change is a request. *Loop, the model on its own goroutine, is the only
-// implementation: every call is a round trip over that goroutine, the
-// model runs its own jobs and tick, and Loop.Wakes says when to Sync.
+// change is a request. *Loop, the model on its own goroutine, implements
+// it in process: every call is a round trip over that goroutine, the
+// model runs its own jobs and tick, and Loop.Wakes says when to Sync. The
+// core/rpc client implements it over a connection (daemon stage 2).
+//
+// The rpc package is generated from this file. A method's line comment
+// says how a client serves it:
+//   - rpc:local answers from the client's replica of the published state
+//     (Snapshot, then the state events);
+//   - rpc:cast is sent one way, with no reply, and publishes nothing, so a
+//     cast must change no published state: it marks, names or starts what
+//     publishes on its own when it lands;
+//   - rpc:client never leaves the client (Sync returns the events it
+//     received);
+//   - every other method is a request, whose reply follows the events it
+//     produced.
+//
+// Parameter names are the wire's field names.
 type Core interface {
 	// The loop: the TUI drains the model when the loop wakes it and after
 	// every message (Sync), and starts its first background jobs and its
 	// health tick (Begin).
-	Sync() []Event
+	Sync() []Event // rpc:client
 	Begin()
 
 	// Startup: the classic workspace's load, the account registry and the
 	// default account's remote-control auth, which newHome sets up.
 	LoadClassic(sweepTmux bool) error
 	InitAccounts()
-	SetRCAuth(a session.RemoteControlAuth)
+	SetRCAuth(auth session.RemoteControlAuth)
 
 	// Workspaces: the loaded ones, their transitions, saves and the
 	// registry.
 	StayGlobal()
 	RestoreSaved(saved []config.Workspace) int
-	RestoreFailed() []string
+	RestoreFailed() []string // rpc:local
 	KeepRestoreFailed(desired map[string]bool)
-	OpenNames() []string
+	OpenNames() []string // rpc:local
 	PersistOpenList()
 	Register(name, dir string) (config.Workspace, error)
 	SetLastUsed(name string) error
@@ -45,24 +60,24 @@ type Core interface {
 	// Workspaces by ID: their views, transitions and saves, the registry
 	// as a copy, and the requests that change a workspace's settings, UI
 	// prefs and help screens.
-	Workspace(id WorkspaceID) (WorkspaceView, bool)
-	Classic() (WorkspaceView, bool)
-	Tabs() []WorkspaceView
-	IsLoaded(id WorkspaceID) bool
-	OpenTab(def config.Workspace) (WorkspaceView, error)
+	Workspace(id WorkspaceID) (WorkspaceView, bool) // rpc:local
+	Classic() (WorkspaceView, bool)                 // rpc:local
+	Tabs() []WorkspaceView                          // rpc:local
+	IsLoaded(id WorkspaceID) bool                   // rpc:local
+	OpenTab(workspace config.Workspace) (WorkspaceView, error)
 	CloseTab(name string) error
 	EnterGlobal(focused WorkspaceID) (WorkspaceView, error)
 	Save(id WorkspaceID) error
-	Registry() RegistryView
+	Registry() RegistryView // rpc:local
 	ReloadRegistry() error
-	SaveSettings(id WorkspaceID, s config.Settings) error
-	SetUIPrefs(id WorkspaceID, p config.UIPrefs) error
+	SaveSettings(id WorkspaceID, settings config.Settings) error
+	SetUIPrefs(id WorkspaceID, prefs config.UIPrefs) error
 	SetHelpScreensSeen(id WorkspaceID, seen uint32) error
 
 	// Instances: their views, and every lifecycle action as a request by
 	// ID, answered by a Reply when it carries a ReqID.
-	Views(id WorkspaceID) []InstanceView
-	View(id InstanceID) (InstanceView, bool)
+	Views(id WorkspaceID) []InstanceView     // rpc:local
+	View(id InstanceID) (InstanceView, bool) // rpc:local
 	Create(id WorkspaceID, spec NewInstance, req ReqID)
 	Kill(id InstanceID, req ReqID)
 	Pause(id InstanceID, req ReqID)
@@ -72,43 +87,43 @@ type Core interface {
 	Merge(target, source InstanceID, req ReqID)
 	Push(id InstanceID, req ReqID)
 	SendPrompt(id InstanceID, text string, req ReqID)
-	FetchIssue(repo string, n int, req ReqID)
+	FetchIssue(repo string, number int, req ReqID)
 
 	// Claude status and the tick: what the TUI's pane events tell the
 	// model, and the selected row the model's health tick favours.
-	SetSelected(id InstanceID)
-	MarkOutput(sessionName string)
-	PaneOutput(id InstanceID)
-	PaneQuiet(id InstanceID)
-	VerifyDead(id InstanceID)
+	SetSelected(id InstanceID)     // rpc:cast
+	MarkOutput(sessionName string) // rpc:cast
+	PaneOutput(id InstanceID)      // rpc:cast
+	PaneQuiet(id InstanceID)       // rpc:cast
+	VerifyDead(id InstanceID)      // rpc:cast
 
 	// The agent program, and the remote-control auth it launches with.
-	Program() string
-	SetProgram(p string)
-	RCAuth() session.RemoteControlAuth
+	Program() string // rpc:local
+	SetProgram(program string)
+	RCAuth() session.RemoteControlAuth // rpc:local
 
 	// GitHub: the poll's results, and a poll sooner.
-	GitHubSnapshot(repo string) (github.Snapshot, bool)
-	GitHubErr(repo string) error
-	GitHubUnavailable() bool
-	GitHubUnavailableReason() string
-	ExpediteGitHub()
+	GitHubSnapshot(repo string) (github.Snapshot, bool) // rpc:local
+	GitHubErr(repo string) error                        // rpc:local
+	GitHubUnavailable() bool                            // rpc:local
+	GitHubUnavailableReason() string                    // rpc:local
+	ExpediteGitHub()                                    // rpc:cast
 
 	// Accounts: the registry, each account's auth, sync, usage and env,
 	// and the account requests.
-	AccountNames() AccountNames
-	AccountsLoaded() bool
-	HasExtraAccounts() bool
-	Account(name string) (account.Account, bool)
-	RCAuthFor(acct string) session.RemoteControlAuth
-	AccountLoggedOut(acct string) bool
-	AccountSync(name string) (account.SyncReport, bool)
-	AccountUsage(name string) (account.Usage, error)
-	AccountEnv(name string) ([]string, error)
-	ClaudeProgram() string
+	AccountNames() AccountNames                         // rpc:local
+	AccountsLoaded() bool                               // rpc:local
+	HasExtraAccounts() bool                             // rpc:local
+	Account(name string) (account.Account, bool)        // rpc:local
+	RCAuthFor(acct string) session.RemoteControlAuth    // rpc:local
+	AccountLoggedOut(acct string) bool                  // rpc:local
+	AccountSync(name string) (account.SyncReport, bool) // rpc:local
+	AccountUsage(name string) (account.Usage, error)    // rpc:local
+	AccountEnv(name string) ([]string, error)           // rpc:local
+	ClaudeProgram() string                              // rpc:local
 	ReloadAccounts()
-	RequestAccountsRefresh(withDefault bool)
-	RequestUsageProbe()
+	RequestAccountsRefresh(withDefault bool) // rpc:cast
+	RequestUsageProbe()                      // rpc:cast
 	AddAccount(name string) (string, error)
 	RemoveAccount(name string) error
 	SetDefaultAccount(name string) error
diff --git a/core/loop.go b/core/loop.go
index 8aa6350..8303837 100644
--- a/core/loop.go
+++ b/core/loop.go
@@ -260,6 +260,11 @@ func (l *Loop) Begin() {
 	})
 }
 
+// Snapshot is the model's whole published state as events
+// (Model.Snapshot): what a server sends a client that subscribes, before
+// the diffs.
+func (l *Loop) Snapshot() []Event { return get(l, (*Model).Snapshot) }
+
 // do runs f on the loop and waits for it. A panic in f, or one an earlier
 // step raised, is re-raised here. After Stop, f does not run.
 func (l *Loop) do(f func(*Model)) {
diff --git a/core/model.go b/core/model.go
index 09fe353..f9629fb 100644
--- a/core/model.go
+++ b/core/model.go
@@ -156,8 +156,19 @@ type Model struct {
 	wsIDs    map[*Workspace]WorkspaceID
 	nextWSID WorkspaceID
 	// publishedWS is every loaded workspace's view as last published
-	// (Sync), in Loaded order.
-	publishedWS []WorkspaceView
+	// (Sync), in Loaded order, and publishedClassic whether they were the
+	// classic workspace.
+	publishedWS      []WorkspaceView
+	publishedClassic bool
+	// publishedModel and publishedAccounts are the model and account views
+	// as last published (publishState); nil before the first.
+	publishedModel    *ModelView
+	publishedAccounts *AccountsView
+	// ghGen counts the GitHub polls applied (deliverGH), and
+	// ghPublishedGen is the count GitHubChanged last published (ghPublished
+	// once it has been).
+	ghGen, ghPublishedGen uint64
+	ghPublished           bool
 
 	out Out
 }
diff --git a/core/usage.go b/core/usage.go
index 85fd7b3..213540e 100644
--- a/core/usage.go
+++ b/core/usage.go
@@ -110,7 +110,6 @@ func (m *Model) deliverUsage(msg usageResult) {
 		m.usage[name] = cur
 		log.For("account").Debug("usage.probe_failed", "account", name, "err", err.Error())
 	}
-	m.emit(AccountsChanged{})
 	if reread {
 		m.RequestAccountsRefresh(rereadDefault)
 	}
diff --git a/core/value_boundary_test.go b/core/value_boundary_test.go
index 95633d3..60ec709 100644
--- a/core/value_boundary_test.go
+++ b/core/value_boundary_test.go
@@ -18,14 +18,10 @@ import (
 	"github.com/stretchr/testify/require"
 )
 
-// allEvents holds a zero value of every concrete Event type, for
-// TestCoreIsValueTyped; TestAllEventsListsEveryEvent keeps it complete.
-var allEvents = []Event{
-	AccountsChanged{}, Alive{}, ClientsStale{}, GitHubChanged{},
-	HealthChecked{}, InstancesChanged{}, Notice{}, Reactivated{},
-	Recovered{}, Reply{}, SessionLaunched{}, Started{},
-	StatusesChanged{}, ViewsChanged{}, WorkspacesChanged{},
-}
+// allEvents holds a zero value of every concrete Event type (EventTypes),
+// for TestCoreIsValueTyped; TestAllEventsListsEveryEvent keeps it
+// complete.
+var allEvents = EventTypes()
 
 // modelOwned are the named types a client must never receive: the model's
 // own objects, whose memory it shares with the model.
@@ -146,8 +142,9 @@ func TestPlainProblem_Bites(t *testing.T) {
 	}
 }
 
-// TestAllEventsListsEveryEvent keeps allEvents complete: every type in the
-// package's non-test files with a coreEvent method is in it.
+// TestAllEventsListsEveryEvent keeps EventTypes (allEvents) complete:
+// every type in the package's non-test files with a coreEvent method is in
+// it, once.
 func TestAllEventsListsEveryEvent(t *testing.T) {
 	fset := token.NewFileSet()
 	entries, err := os.ReadDir(".")
@@ -176,5 +173,5 @@ func TestAllEventsListsEveryEvent(t *testing.T) {
 	}
 	sort.Strings(declared)
 	sort.Strings(listed)
-	assert.Equal(t, declared, listed, "allEvents must list every Event type")
+	assert.Equal(t, declared, listed, "EventTypes must list every Event type, once")
 }
diff --git a/core/view_test.go b/core/view_test.go
index c87d483..da3255f 100644
--- a/core/view_test.go
+++ b/core/view_test.go
@@ -62,13 +62,19 @@ func TestSync_PublishesChangedWorkspacesFirst(t *testing.T) {
 
 	m.notifyInfo("hello")
 	out := m.Sync()
-	require.Len(t, out.Events, 3)
+	require.Len(t, out.Events, 6)
 	_, ok := out.Events[0].(WorkspacesChanged)
 	require.True(t, ok, "workspace views first")
-	vc, ok := out.Events[1].(ViewsChanged)
+	_, ok = out.Events[1].(ModelChanged)
+	require.True(t, ok, "then the model's state")
+	_, ok = out.Events[2].(AccountsChanged)
+	require.True(t, ok, "the account state")
+	_, ok = out.Events[3].(GitHubChanged)
+	require.True(t, ok, "and the GitHub state, each published the first time")
+	vc, ok := out.Events[4].(ViewsChanged)
 	require.True(t, ok, "instance views next")
 	assert.Equal(t, m.wsIDOf(ws), vc.WS)
-	assert.Equal(t, Notice{Info: "hello"}, out.Events[2])
+	assert.Equal(t, Notice{Info: "hello"}, out.Events[5])
 
 	assert.Empty(t, m.Sync().Events, "nothing changed")
 
@@ -145,12 +151,15 @@ func TestCloneViews_CopiesSubagents(t *testing.T) {
 	assert.Nil(t, cloneViews([]InstanceView{{}})[0].Subagents, "nil stays nil")
 }
 
-// instanceEvents drops the WorkspacesChanged events from events: the
+// instanceEvents drops the workspace and state events (WorkspacesChanged,
+// ModelChanged, AccountsChanged, GitHubChanged) from events: the
 // instance-view tests count only what they publish.
 func instanceEvents(events []Event) []Event {
 	var out []Event
 	for _, ev := range events {
-		if _, ok := ev.(WorkspacesChanged); !ok {
+		switch ev.(type) {
+		case WorkspacesChanged, ModelChanged, AccountsChanged, GitHubChanged:
+		default:
 			out = append(out, ev)
 		}
 	}
diff --git a/core/views.go b/core/views.go
index 2f8ddef..40ee16f 100644
--- a/core/views.go
+++ b/core/views.go
@@ -157,11 +157,15 @@ func cloneViews(views []InstanceView) []InstanceView {
 }
 
 // syncEvents publishes the workspace views that changed (WorkspacesChanged,
-// first), then the instance views that changed (ViewsChanged), and returns
-// them ahead of every other event produced since the last call, which it
-// forgets. It leaves the jobs: the loop starts those after every step.
+// first), then the model, account and GitHub views that changed
+// (publishState), then the instance views that changed (ViewsChanged), and
+// returns them ahead of every other event produced since the last call,
+// which it forgets. It leaves the jobs: the loop starts those after every
+// step.
 func (m *Model) syncEvents() []Event {
-	published := append(m.publishWorkspaces(), m.publishViews()...)
+	published := m.publishWorkspaces()
+	published = append(published, m.publishState()...)
+	published = append(published, m.publishViews()...)
 	events := m.out.Events
 	m.out.Events = nil
 	return append(published, events...)
diff --git a/core/workspace_views.go b/core/workspace_views.go
index 139e21f..56a68fb 100644
--- a/core/workspace_views.go
+++ b/core/workspace_views.go
@@ -80,13 +80,14 @@ func (m *Model) publishWorkspaces() []Event {
 			delete(m.wsIDs, ws)
 		}
 	}
-	if m.publishedWS != nil && reflect.DeepEqual(m.publishedWS, views) {
+	classic := m.classicShown()
+	if m.publishedWS != nil && m.publishedClassic == classic && reflect.DeepEqual(m.publishedWS, views) {
 		return nil
 	}
-	m.publishedWS = views
+	m.publishedWS, m.publishedClassic = views, classic
 	// The event gets its own copy, as ViewsChanged does: the TUI keeps
 	// the views, which must not alias what the next publish compares.
-	return []Event{WorkspacesChanged{Views: cloneWorkspaceViews(views)}}
+	return []Event{WorkspacesChanged{Views: cloneWorkspaceViews(views), Classic: classic}}
 }
 
 // cloneWorkspaceViews deep-copies views: the slice and every field that
diff --git a/session/git/diff.go b/session/git/diff.go
index fadbc29..5d1393b 100644
--- a/session/git/diff.go
+++ b/session/git/diff.go
@@ -2,6 +2,8 @@ package git
 
 import (
 	"context"
+	"encoding/json"
+	"errors"
 	"fmt"
 	"strings"
 	"time"
@@ -58,6 +60,36 @@ type DiffStats struct {
 	Error error
 }
 
+// diffStatsJSON is DiffStats as it crosses a process boundary: the error
+// as its message, which is all a client shows of it.
+type diffStatsJSON struct {
+	Content        string
+	Added, Removed int
+	Error          string `json:",omitempty"`
+}
+
+// MarshalJSON encodes d with its error as text.
+func (d DiffStats) MarshalJSON() ([]byte, error) {
+	j := diffStatsJSON{Content: d.Content, Added: d.Added, Removed: d.Removed}
+	if d.Error != nil {
+		j.Error = d.Error.Error()
+	}
+	return json.Marshal(j)
+}
+
+// UnmarshalJSON decodes a DiffStats MarshalJSON encoded.
+func (d *DiffStats) UnmarshalJSON(b []byte) error {
+	var j diffStatsJSON
+	if err := json.Unmarshal(b, &j); err != nil {
+		return err
+	}
+	*d = DiffStats{Content: j.Content, Added: j.Added, Removed: j.Removed}
+	if j.Error != "" {
+		d.Error = errors.New(j.Error)
+	}
+	return nil
+}
+
 // IsEmpty reports whether the diff contains no added lines, no
 // removed lines, and no body content — the state presented when a
 // session has not yet diverged from its base commit.
```

### A2. New files

- [ ] **Step 1:** Write each in full.

**File: `core/wire_error.go`** (new)

```go
package core

import (
	"errors"

	"github.com/aidan-bailey/loom/session"
)

// Wire error codes: what a client can still tell about an error once it
// has crossed a process boundary as text (see WireError).
const (
	// CodeNotFound: the request named a session no loaded workspace holds
	// (ErrNoSession).
	CodeNotFound = "not_found"
	// CodeRefused: the model refused the request (ErrRefused).
	CodeRefused = "refused"
	// CodeStorage: the workspace's storage refuses writes
	// (session.ErrStorageLoadFailed).
	CodeStorage = "storage"
	// CodeError: any other failure; only its message survives.
	CodeError = "error"
	// CodePanic: the model panicked (LoopPanic); the client re-raises it.
	CodePanic = "panic"
	// CodeProtocol: a frame the other side could not use.
	CodeProtocol = "protocol"
	// CodeMismatch: the two sides speak different protocol versions.
	CodeMismatch = "mismatch"
)

// WireError is an error as it crosses a process boundary: its message,
// and a code that keeps the identities clients test with errors.Is
// (ErrNoSession, ErrRefused, session.ErrStorageLoadFailed). Every other
// error arrives as text only, which is all the TUI does with one.
type WireError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *WireError) Error() string { return e.Message }

// Is keeps the sentinel identities a client checks: a not_found error is
// ErrNoSession (and so ErrRefused, as ErrNoSession is a refusal), a
// refused one ErrRefused, a storage one session.ErrStorageLoadFailed.
func (e *WireError) Is(target error) bool {
	switch e.Code {
	case CodeNotFound:
		return target == ErrNoSession || target == ErrRefused
	case CodeRefused:
		return target == ErrRefused
	case CodeStorage:
		return target == session.ErrStorageLoadFailed
	}
	return false
}

// ToWire encodes err for the wire: nil for nil, a WireError unchanged,
// anything else its message under the code of the first sentinel it
// matches (CodeError for none).
func ToWire(err error) *WireError {
	if err == nil {
		return nil
	}
	var w *WireError
	if errors.As(err, &w) {
		return w
	}
	code := CodeError
	switch {
	case errors.Is(err, ErrNoSession):
		code = CodeNotFound
	case errors.Is(err, ErrRefused):
		code = CodeRefused
	case errors.Is(err, session.ErrStorageLoadFailed):
		code = CodeStorage
	}
	return &WireError{Code: code, Message: err.Error()}
}

// FromWire decodes a wire error: nil (a nil interface, never a typed nil
// pointer) for nil.
func FromWire(w *WireError) error {
	if w == nil {
		return nil
	}
	return w
}
```

**File: `core/state_views.go`** (new)

```go
package core

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
)

// The published state a client keeps a replica of (daemon stage 2): every
// query in Core is answered from these views, the instance views
// (ViewsChanged) and the workspace views (WorkspacesChanged). The model
// publishes each when it changes (publishState, at Sync) and all of them
// on demand (Snapshot). Their methods hold the queries' logic, which the
// model's own query methods share, so a replica answers exactly as the
// model would.

// WorkspacesView is every loaded workspace's view in Loaded order;
// Classic says they are the classic workspace (no tab is open).
type WorkspacesView struct {
	Views   []WorkspaceView
	Classic bool
}

// Workspace is the view of the loaded workspace id (Core.Workspace).
func (w WorkspacesView) Workspace(id WorkspaceID) (WorkspaceView, bool) {
	if id == 0 {
		return WorkspaceView{}, false
	}
	for _, v := range w.Views {
		if v.ID == id {
			return cloneWorkspaceViews([]WorkspaceView{v})[0], true
		}
	}
	return WorkspaceView{}, false
}

// ClassicView is the classic workspace's view while no tab is open
// (Core.Classic).
func (w WorkspacesView) ClassicView() (WorkspaceView, bool) {
	if !w.Classic || len(w.Views) == 0 {
		return WorkspaceView{}, false
	}
	return cloneWorkspaceViews(w.Views[:1])[0], true
}

// Tabs are the open tabs' views, in tab order (Core.Tabs): never nil.
func (w WorkspacesView) Tabs() []WorkspaceView {
	if w.Classic {
		return []WorkspaceView{}
	}
	return cloneWorkspaceViews(w.Views)
}

// IsLoaded reports whether the workspace id is loaded (Core.IsLoaded).
func (w WorkspacesView) IsLoaded(id WorkspaceID) bool {
	_, ok := w.Workspace(id)
	return ok
}

// ModelView is the model's own state: the agent program, the default
// account's remote-control auth, the workspace registry, and the
// workspaces that failed to restore (published as ModelChanged).
type ModelView struct {
	Program       string
	RCAuth        session.RemoteControlAuth
	Registry      RegistryView
	RestoreFailed []string
	OpenNames     []string
}

// Clone deep-copies v's slices.
func (v ModelView) Clone() ModelView {
	v.Registry = RegistryView{
		Workspaces: slices.Clone(v.Registry.Workspaces),
		Open:       slices.Clone(v.Registry.Open),
	}
	v.RestoreFailed = slices.Clone(v.RestoreFailed)
	v.OpenNames = slices.Clone(v.OpenNames)
	return v
}

// AccountsView is the account state (published as AccountsChanged): the
// registry's accounts and default, each account's remote-control auth,
// link report, usage and launch env, and the Claude program they run.
type AccountsView struct {
	// Names is AccountNames(): Present is false without a registry.
	Names AccountNames
	// Loaded: the registry exists and loaded without error.
	Loaded bool
	// Extra: an account besides default is registered.
	Extra bool
	// ClaudeProgram is the Claude program the accounts run with ("" when
	// neither the agent program nor an active session is Claude).
	ClaudeProgram string
	// Accounts are the registered accounts, by name.
	Accounts map[string]account.Account
	// DefaultAuth is the default account's remote-control auth, and Auth
	// each extra account's that a refresh has read.
	DefaultAuth session.RemoteControlAuth
	Auth        map[string]session.RemoteControlAuth
	// Sync is each extra account's last link report.
	Sync map[string]account.SyncReport
	// Usage is each account's latest usage sample, and UsageErr its last
	// probe's failure, if any.
	Usage    map[string]account.Usage
	UsageErr map[string]string
}

// AccountNames is the registry's names, default first (Core.AccountNames).
func (v AccountsView) AccountNames() AccountNames {
	n := v.Names
	n.Names = slices.Clone(n.Names)
	return n
}

// AccountsLoaded reports that the registry exists and loaded
// (Core.AccountsLoaded).
func (v AccountsView) AccountsLoaded() bool { return v.Loaded }

// HasExtraAccounts reports that an account besides default is registered
// (Core.HasExtraAccounts).
func (v AccountsView) HasExtraAccounts() bool { return v.Extra }

// Account is the registered account called name (Core.Account).
func (v AccountsView) Account(name string) (account.Account, bool) {
	a, ok := v.Accounts[name]
	return a, ok
}

// RCAuthFor is acct's remote-control auth: the default account's for ""
// or default, an extra account's as its last refresh read it, else
// Unknown (Core.RCAuthFor).
func (v AccountsView) RCAuthFor(acct string) session.RemoteControlAuth {
	if acct == "" || acct == account.DefaultName {
		return v.DefaultAuth
	}
	if a, ok := v.Auth[acct]; ok {
		return a
	}
	return session.RemoteControlAuth{State: session.RemoteControlAuthUnknown}
}

// AccountLoggedOut reports that acct's last auth read found it logged
// out (Core.AccountLoggedOut).
func (v AccountsView) AccountLoggedOut(acct string) bool {
	id := v.RCAuthFor(acct).Identity
	return id.ConfigDir != "" && !id.LoggedIn
}

// AccountSync is name's last link report (Core.AccountSync).
func (v AccountsView) AccountSync(name string) (account.SyncReport, bool) {
	rep, ok := v.Sync[name]
	return rep.Clone(), ok
}

// AccountUsage is name's latest usage sample and its last probe's
// failure (Core.AccountUsage).
func (v AccountsView) AccountUsage(name string) (account.Usage, error) {
	var err error
	if msg, ok := v.UsageErr[name]; ok {
		err = errors.New(msg)
	}
	return v.Usage[name].Clone(), err
}

// AccountEnv is the environment that runs Claude as name: nil for the
// default account, CLAUDE_CONFIG_DIR for a registered one, an error
// otherwise (Core.AccountEnv; account.Registry.Env).
func (v AccountsView) AccountEnv(name string) ([]string, error) {
	if !v.Names.Present {
		return nil, errNoRegistry
	}
	if name == "" || name == account.DefaultName {
		return nil, nil
	}
	a, ok := v.Accounts[name]
	if !ok {
		return nil, fmt.Errorf("account %q is not registered", name)
	}
	return account.EnvFor(a.Dir), nil
}

// Clone deep-copies v's maps and the reports and samples in them.
func (v AccountsView) Clone() AccountsView {
	v.Names.Names = slices.Clone(v.Names.Names)
	v.Accounts = maps.Clone(v.Accounts)
	v.Auth = maps.Clone(v.Auth)
	if v.Sync != nil {
		sync := make(map[string]account.SyncReport, len(v.Sync))
		for k, rep := range v.Sync {
			sync[k] = rep.Clone()
		}
		v.Sync = sync
	}
	if v.Usage != nil {
		usage := make(map[string]account.Usage, len(v.Usage))
		for k, u := range v.Usage {
			usage[k] = u.Clone()
		}
		v.Usage = usage
	}
	v.UsageErr = maps.Clone(v.UsageErr)
	return v
}

// GitHubView is the GitHub poll's state (published as GitHubChanged):
// each open repo's snapshot or its last poll's failure, and whether gh is
// usable.
type GitHubView struct {
	Snapshots map[string]github.Snapshot
	Errs      map[string]string
	// Unavailable: gh was checked and found unusable, for Reason.
	Unavailable bool
	Reason      string
}

// GitHubSnapshot is repo's latest snapshot (Core.GitHubSnapshot).
func (v GitHubView) GitHubSnapshot(repo string) (github.Snapshot, bool) {
	s, ok := v.Snapshots[repo]
	return s.Clone(), ok
}

// GitHubErr is repo's last poll failure (Core.GitHubErr).
func (v GitHubView) GitHubErr(repo string) error {
	if msg, ok := v.Errs[repo]; ok {
		return errors.New(msg)
	}
	return nil
}

// GitHubUnavailable reports that gh was found unusable
// (Core.GitHubUnavailable).
func (v GitHubView) GitHubUnavailable() bool { return v.Unavailable }

// GitHubUnavailableReason is why gh was found unusable
// (Core.GitHubUnavailableReason).
func (v GitHubView) GitHubUnavailableReason() string { return v.Reason }

// Clone deep-copies v's maps and snapshots.
func (v GitHubView) Clone() GitHubView {
	if v.Snapshots != nil {
		snaps := make(map[string]github.Snapshot, len(v.Snapshots))
		for k, s := range v.Snapshots {
			snaps[k] = s.Clone()
		}
		v.Snapshots = snaps
	}
	v.Errs = maps.Clone(v.Errs)
	return v
}

// CloneViews deep-copies instance views (cloneViews), for a client that
// hands out its replica's views: the copies share no memory with it.
func CloneViews(views []InstanceView) []InstanceView { return cloneViews(views) }

// CloneWorkspaceViews deep-copies workspace views (cloneWorkspaceViews).
func CloneWorkspaceViews(views []WorkspaceView) []WorkspaceView {
	return cloneWorkspaceViews(views)
}
```

**File: `core/state_publish.go`** (new)

```go
package core

import (
	"reflect"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
)

// workspacesView is every loaded workspace's view, and whether they are
// the classic workspace.
func (m *Model) workspacesView() WorkspacesView {
	loaded := m.Loaded()
	views := make([]WorkspaceView, len(loaded))
	for i, ws := range loaded {
		views[i] = m.wsViewOf(ws)
	}
	return WorkspacesView{Views: views, Classic: m.classicShown()}
}

// classicShown reports that the loaded workspace is the classic one: no
// tab is open.
func (m *Model) classicShown() bool { return len(m.tabs) == 0 && m.classic != nil }

// modelView is the model's own state as a client sees it.
func (m *Model) modelView() ModelView {
	return ModelView{
		Program:       m.program,
		RCAuth:        m.rcAuth,
		Registry:      m.Registry(),
		RestoreFailed: m.RestoreFailed(),
		OpenNames:     m.OpenNames(),
	}
}

// accountsView is the account state as a client sees it; the model's own
// account queries answer from it too, so the two never disagree.
func (m *Model) accountsView() AccountsView {
	v := AccountsView{
		DefaultAuth:   m.rcAuth,
		ClaudeProgram: m.ClaudeProgram(),
	}
	if m.accounts != nil {
		v.Names = AccountNames{Present: true, Default: m.accounts.Default(), Names: m.accounts.Names()}
		v.Loaded = m.accounts.LoadErr() == nil
		v.Extra = m.accounts.HasExtra()
		v.Accounts = make(map[string]account.Account, len(m.accounts.Accounts))
		for _, a := range m.accounts.Accounts {
			v.Accounts[a.Name] = a
		}
	}
	if len(m.accountAuth) > 0 {
		v.Auth = make(map[string]session.RemoteControlAuth, len(m.accountAuth))
		for k, a := range m.accountAuth {
			v.Auth[k] = a
		}
	}
	if len(m.accountSync) > 0 {
		v.Sync = make(map[string]account.SyncReport, len(m.accountSync))
		for k, rep := range m.accountSync {
			v.Sync[k] = rep.Clone()
		}
	}
	if len(m.usage) > 0 {
		v.Usage = make(map[string]account.Usage, len(m.usage))
		for k, u := range m.usage {
			v.Usage[k] = u.last.Clone()
			if u.err != nil {
				if v.UsageErr == nil {
					v.UsageErr = map[string]string{}
				}
				v.UsageErr[k] = u.err.Error()
			}
		}
	}
	return v
}

// githubView is the GitHub poll's state as a client sees it; the model's
// own GitHub queries answer from it too.
func (m *Model) githubView() GitHubView {
	v := GitHubView{
		Unavailable: m.ghAvailable.checked && !m.ghAvailable.ok,
		Reason:      m.ghAvailable.reason,
	}
	if len(m.ghState) > 0 {
		v.Snapshots = make(map[string]github.Snapshot, len(m.ghState))
		for repo, s := range m.ghState {
			v.Snapshots[repo] = s.Clone()
		}
	}
	if len(m.ghErrs) > 0 {
		v.Errs = make(map[string]string, len(m.ghErrs))
		for repo, err := range m.ghErrs {
			v.Errs[repo] = err.Error()
		}
	}
	return v
}

// publishState returns a ModelChanged, an AccountsChanged and a
// GitHubChanged for each of those views that changed since the last
// publish (always on the first). The model and account views are
// compared whole; the GitHub view, whose snapshots can be large, is
// republished when a poll lands (ghGen, which deliverGH bumps: the one
// place its state changes).
func (m *Model) publishState() []Event {
	var events []Event
	mv := m.modelView()
	if m.publishedModel == nil || !reflect.DeepEqual(*m.publishedModel, mv) {
		m.publishedModel = &mv
		events = append(events, ModelChanged{View: mv.Clone()})
	}
	av := m.accountsView()
	if m.publishedAccounts == nil || !reflect.DeepEqual(*m.publishedAccounts, av) {
		m.publishedAccounts = &av
		events = append(events, AccountsChanged{View: av.Clone()})
	}
	if !m.ghPublished || m.ghPublishedGen != m.ghGen {
		m.ghPublished, m.ghPublishedGen = true, m.ghGen
		events = append(events, GitHubChanged{View: m.githubView()})
	}
	return events
}

// Snapshot is the whole published state as events, in Sync's order: the
// workspace views, the model, account and GitHub views, and every loaded
// workspace's instance views. It leaves what the next Sync diffs against
// alone: it is what a client needs when it subscribes, before the diffs.
func (m *Model) Snapshot() []Event {
	ws := m.workspacesView()
	events := []Event{
		WorkspacesChanged{Views: ws.Views, Classic: ws.Classic},
		ModelChanged{View: m.modelView()},
		AccountsChanged{View: m.accountsView()},
		GitHubChanged{View: m.githubView()},
	}
	for _, w := range m.Loaded() {
		views := make([]InstanceView, len(w.insts))
		for i, inst := range w.insts {
			views[i] = m.viewOf(inst)
		}
		events = append(events, ViewsChanged{WS: m.wsIDOf(w), Views: views})
	}
	return events
}
```

**File: `core/events_json.go`** (new)

```go
package core

import (
	"encoding/json"

	"github.com/aidan-bailey/loom/session/github"
)

// The events whose fields hold an error encode it as a WireError, which
// keeps the message and the sentinel identities clients test (errors.Is).
// Every other event encodes as plain JSON.

type noticeJSON struct {
	Err  *WireError `json:",omitempty"`
	Info string     `json:",omitempty"`
}

// MarshalJSON encodes n with its error as a WireError.
func (n Notice) MarshalJSON() ([]byte, error) {
	return json.Marshal(noticeJSON{Err: ToWire(n.Err), Info: n.Info})
}

// UnmarshalJSON decodes a Notice MarshalJSON encoded.
func (n *Notice) UnmarshalJSON(b []byte) error {
	var j noticeJSON
	if err := json.Unmarshal(b, &j); err != nil {
		return err
	}
	*n = Notice{Err: FromWire(j.Err), Info: j.Info}
	return nil
}

type replyJSON struct {
	Req    ReqID
	ID     InstanceID
	Err    *WireError `json:",omitempty"`
	Notice *WireError `json:",omitempty"`
	Issue  github.Issue
}

// MarshalJSON encodes r with its errors as WireErrors.
func (r Reply) MarshalJSON() ([]byte, error) {
	return json.Marshal(replyJSON{Req: r.Req, ID: r.ID, Err: ToWire(r.Err), Notice: ToWire(r.Notice), Issue: r.Issue})
}

// UnmarshalJSON decodes a Reply MarshalJSON encoded.
func (r *Reply) UnmarshalJSON(b []byte) error {
	var j replyJSON
	if err := json.Unmarshal(b, &j); err != nil {
		return err
	}
	*r = Reply{Req: j.Req, ID: j.ID, Err: FromWire(j.Err), Notice: FromWire(j.Notice), Issue: j.Issue}
	return nil
}
```

**File: `core/state_views_test.go`** (new)

```go
package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stateEvents picks the model, account and GitHub events out of events.
func stateEvents(events []Event) (model, accounts, gh int) {
	for _, ev := range events {
		switch ev.(type) {
		case ModelChanged:
			model++
		case AccountsChanged:
			accounts++
		case GitHubChanged:
			gh++
		}
	}
	return
}

// TestPublishState_OnceThenOnChange: the first Sync publishes the model,
// account and GitHub views; later ones publish only a view that changed.
func TestPublishState_OnceThenOnChange(t *testing.T) {
	m := NewForTest(Options{Program: "aider"})
	model, accounts, gh := stateEvents(m.Sync().Events)
	assert.Equal(t, [3]int{1, 1, 1}, [3]int{model, accounts, gh}, "each published the first time")
	model, accounts, gh = stateEvents(m.Sync().Events)
	assert.Equal(t, [3]int{0, 0, 0}, [3]int{model, accounts, gh}, "nothing changed")

	m.SetProgram("claude")
	model, accounts, gh = stateEvents(m.Sync().Events)
	assert.Equal(t, 1, model, "the program is the model's state")
	assert.Equal(t, 1, accounts, "and the Claude program the accounts run with changed too")
	assert.Zero(t, gh)

	m.Deliver(GitHubResultForTest(true, "", map[string]github.Snapshot{"/r": {}}, nil))
	model, accounts, gh = stateEvents(m.Sync().Events)
	assert.Equal(t, [3]int{0, 0, 1}, [3]int{model, accounts, gh}, "a poll republishes the GitHub view")

	m.Deliver(UsageResultForTest(nil, map[string]error{account.DefaultName: errors.New("probe failed")}))
	model, accounts, gh = stateEvents(m.Sync().Events)
	assert.Equal(t, [3]int{0, 1, 0}, [3]int{model, accounts, gh}, "a usage probe republishes the account view")
}

// TestPublishState_CarriesTheState: each event carries the view a replica
// answers from.
func TestPublishState_CarriesTheState(t *testing.T) {
	m := NewForTest(Options{Program: "aider"})
	m.Deliver(GitHubResultForTest(false, "gh is not logged in", nil, map[string]error{"/r": errors.New("boom")}))
	for _, ev := range m.Sync().Events {
		switch ev := ev.(type) {
		case ModelChanged:
			assert.Equal(t, "aider", ev.View.Program)
		case GitHubChanged:
			assert.True(t, ev.View.Unavailable)
			assert.Equal(t, "gh is not logged in", ev.View.Reason)
			assert.Equal(t, map[string]string{"/r": "boom"}, ev.View.Errs)
		}
	}
}

// TestWorkspacesChanged_SaysWhenClassic: the published workspaces say
// whether they are the classic workspace or the tabs.
func TestWorkspacesChanged_SaysWhenClassic(t *testing.T) {
	m := NewForTest(Options{})
	m.SetWorkspacesForTest(storedWorkspace(t, "classic"), nil)
	ev := m.Sync().Events[0].(WorkspacesChanged)
	assert.True(t, ev.Classic)
	require.Len(t, ev.Views, 1)

	m.SetWorkspacesForTest(nil, []*Workspace{storedWorkspace(t, "tab")})
	ev = m.Sync().Events[0].(WorkspacesChanged)
	assert.False(t, ev.Classic)
	require.Len(t, ev.Views, 1)
}

// TestSnapshot_IsTheWholeStateAndLeavesTheDiffAlone: Snapshot carries
// every published view, and the next Sync still publishes what changed
// since the last Sync.
func TestSnapshot_IsTheWholeStateAndLeavesTheDiffAlone(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	ws.add(pausedInst(t, "x"))
	m.SetWorkspacesForTest(nil, []*Workspace{ws})
	m.Sync()

	snap := m.Snapshot()
	require.Len(t, snap, 5)
	assert.IsType(t, WorkspacesChanged{}, snap[0])
	assert.IsType(t, ModelChanged{}, snap[1])
	assert.IsType(t, AccountsChanged{}, snap[2])
	assert.IsType(t, GitHubChanged{}, snap[3])
	vc := snap[4].(ViewsChanged)
	assert.Equal(t, m.wsIDOf(ws), vc.WS)
	require.Len(t, vc.Views, 1)
	assert.Equal(t, "x", vc.Views[0].Title)

	assert.Empty(t, m.Sync().Events, "a snapshot is no publish: nothing changed since the last Sync")
}

// TestStateViews_AnswerAsTheModel: every query a replica answers from the
// state views gives what the model's own query gives, for known names and
// misses alike.
func TestStateViews_AnswerAsTheModel(t *testing.T) {
	noCredentialOverride(t)
	for _, setup := range []struct {
		name  string
		build func(*Model)
	}{
		{"bare", func(*Model) {}},
		{"accounts and GitHub", func(m *Model) {
			withAccounts(t, m, "max-2", "max-3")
			m.SetRCAuth(session.RemoteControlAuth{State: session.RemoteControlAuthOK, Identity: account.Identity{ConfigDir: "/c", LoggedIn: true}})
			m.SetAccountAuthForTest(map[string]session.RemoteControlAuth{
				"max-2": {State: session.RemoteControlAuthBlocked, Reason: "logged out", Identity: account.Identity{ConfigDir: "/a2"}},
			})
			m.SetAccountSyncForTest("max-2", account.SyncReport{Linked: []string{"projects"}, Diverged: []string{"settings.json"}})
			m.SetAccountUsageForTest("max-3", account.Usage{Available: true, Plan: "max", At: time.Unix(10, 0)}, errors.New("probe failed"))
			m.Deliver(GitHubResultForTest(true, "", map[string]github.Snapshot{
				"/r": {PRs: map[string]github.PR{"dev/x": {Number: 4}}, Issues: map[int]github.Issue{7: {Number: 7, Title: "t"}}},
			}, map[string]error{"/s": errors.New("no remote")}))
		}},
	} {
		t.Run(setup.name, func(t *testing.T) {
			m := NewForTest(Options{Program: "claude"})
			setup.build(m)
			av, gv := m.accountsView(), m.githubView()
			for _, name := range []string{"", account.DefaultName, "max-2", "max-3", "nobody"} {
				assert.Equal(t, m.RCAuthFor(name), av.RCAuthFor(name), "RCAuthFor(%q)", name)
				assert.Equal(t, m.AccountLoggedOut(name), av.AccountLoggedOut(name), "AccountLoggedOut(%q)", name)
				a, ok := m.Account(name)
				va, vok := av.Account(name)
				assert.Equal(t, [2]any{a, ok}, [2]any{va, vok}, "Account(%q)", name)
				rep, ok := m.AccountSync(name)
				vrep, vok := av.AccountSync(name)
				assert.Equal(t, [2]any{rep, ok}, [2]any{vrep, vok}, "AccountSync(%q)", name)
				u, err := m.AccountUsage(name)
				vu, verr := av.AccountUsage(name)
				assert.Equal(t, u, vu, "AccountUsage(%q)", name)
				assert.Equal(t, fmt.Sprint(err), fmt.Sprint(verr), "AccountUsage(%q) error", name)
				env, err := m.AccountEnv(name)
				venv, verr := av.AccountEnv(name)
				assert.Equal(t, env, venv, "AccountEnv(%q)", name)
				assert.Equal(t, fmt.Sprint(err), fmt.Sprint(verr), "AccountEnv(%q) error", name)
			}
			assert.Equal(t, m.AccountNames(), av.AccountNames())
			assert.Equal(t, m.AccountsLoaded(), av.AccountsLoaded())
			assert.Equal(t, m.HasExtraAccounts(), av.HasExtraAccounts())
			assert.Equal(t, m.ClaudeProgram(), av.ClaudeProgram)
			for _, repo := range []string{"/r", "/s", "/nowhere"} {
				s, ok := m.GitHubSnapshot(repo)
				vs, vok := gv.GitHubSnapshot(repo)
				assert.Equal(t, [2]any{s, ok}, [2]any{vs, vok}, "GitHubSnapshot(%q)", repo)
				assert.Equal(t, fmt.Sprint(m.GitHubErr(repo)), fmt.Sprint(gv.GitHubErr(repo)), "GitHubErr(%q)", repo)
			}
			assert.Equal(t, m.GitHubUnavailable(), gv.GitHubUnavailable())
			assert.Equal(t, m.GitHubUnavailableReason(), gv.GitHubUnavailableReason())
		})
	}
}

// TestWorkspacesView_AnswersAsTheModel: the workspace queries a replica
// answers from the published workspaces give what the model gives.
func TestWorkspacesView_AnswersAsTheModel(t *testing.T) {
	for _, tabs := range []bool{false, true} {
		m := NewForTest(Options{})
		a, b := storedWorkspace(t, "a"), storedWorkspace(t, "b")
		if tabs {
			m.SetWorkspacesForTest(nil, []*Workspace{a, b})
		} else {
			m.SetWorkspacesForTest(a, nil)
		}
		w := m.workspacesView()
		c, ok := m.Classic()
		vc, vok := w.ClassicView()
		assert.Equal(t, [2]any{c, ok}, [2]any{vc, vok}, "Classic, tabs=%v", tabs)
		assert.Equal(t, m.Tabs(), w.Tabs(), "Tabs, tabs=%v", tabs)
		for _, id := range []WorkspaceID{0, m.wsIDOf(a), m.wsIDOf(b), 99} {
			v, ok := m.Workspace(id)
			vv, vok := w.Workspace(id)
			assert.Equal(t, [2]any{v, ok}, [2]any{vv, vok}, "Workspace(%d), tabs=%v", id, tabs)
			assert.Equal(t, m.IsLoaded(id), w.IsLoaded(id), "IsLoaded(%d), tabs=%v", id, tabs)
		}
	}
}

// TestWireError_KeepsTheSentinels: an error that crossed the wire still
// matches the sentinels clients test, and nothing else.
func TestWireError_KeepsTheSentinels(t *testing.T) {
	for _, tc := range []struct {
		err       error
		code      string
		refused   bool
		noSession bool
		storage   bool
	}{
		{ErrNoSession, CodeNotFound, true, true, false},
		{refusedError{errors.New("kill x: busy")}, CodeRefused, true, false, false},
		{fmt.Errorf("save: %w", session.ErrStorageLoadFailed), CodeStorage, false, false, true},
		{errors.New("git failed"), CodeError, false, false, false},
	} {
		w := ToWire(tc.err)
		assert.Equal(t, tc.code, w.Code, "%v", tc.err)
		assert.Equal(t, tc.err.Error(), w.Error())
		var back error = w
		assert.Equal(t, tc.refused, errors.Is(back, ErrRefused), "%v is ErrRefused", tc.err)
		assert.Equal(t, tc.noSession, errors.Is(back, ErrNoSession), "%v is ErrNoSession", tc.err)
		assert.Equal(t, tc.storage, errors.Is(back, session.ErrStorageLoadFailed), "%v is ErrStorageLoadFailed", tc.err)
	}
	assert.Nil(t, ToWire(nil))
	assert.Nil(t, FromWire(nil), "no typed nil")
	w := &WireError{Code: CodeRefused, Message: "m"}
	assert.Same(t, w, ToWire(w), "a WireError passes through")
}

// TestEventsWithErrors_RoundTripJSON: Notice and Reply keep their errors'
// text and sentinels through JSON, and a nil error stays nil.
func TestEventsWithErrors_RoundTripJSON(t *testing.T) {
	n := Notice{Err: refusedError{errors.New("pause y: busy")}, Info: "i"}
	b, err := json.Marshal(n)
	require.NoError(t, err)
	var back Notice
	require.NoError(t, json.Unmarshal(b, &back))
	assert.Equal(t, "pause y: busy", back.Err.Error())
	assert.True(t, errors.Is(back.Err, ErrRefused))
	assert.Equal(t, "i", back.Info)

	r := Reply{Req: 3, ID: 9, Notice: errors.New("stash forgotten"), Issue: github.Issue{Number: 5}}
	b, err = json.Marshal(r)
	require.NoError(t, err)
	var rback Reply
	require.NoError(t, json.Unmarshal(b, &rback))
	assert.Equal(t, ReqID(3), rback.Req)
	assert.Equal(t, InstanceID(9), rback.ID)
	assert.Nil(t, rback.Err, "nil stays a nil interface")
	assert.Equal(t, "stash forgotten", rback.Notice.Error())
	assert.Equal(t, 5, rback.Issue.Number)
}
```

**File: `session/git/diff_json_test.go`** (new)

```go
package git

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDiffStats_RoundTripsJSON: diff stats keep their counts, content and
// error text through JSON (a client shows the text), and no error stays
// none.
func TestDiffStats_RoundTripsJSON(t *testing.T) {
	for _, d := range []DiffStats{
		{Content: "+a\n", Added: 1, Removed: 2},
		{Error: errors.New("base commit missing")},
	} {
		b, err := json.Marshal(d)
		require.NoError(t, err)
		var back DiffStats
		require.NoError(t, json.Unmarshal(b, &back))
		assert.Equal(t, d.Content, back.Content)
		assert.Equal(t, d.Added, back.Added)
		assert.Equal(t, d.Removed, back.Removed)
		if d.Error == nil {
			assert.Nil(t, back.Error)
		} else {
			assert.EqualError(t, back.Error, d.Error.Error())
		}
	}
}
```

### A3. Verify and commit

- [ ] **Step 1:** Run `gofmt -l core session` (it must be empty), then `CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test ./...`. All green, app included, since `AccountsChanged`/`GitHubChanged` are still handled by type.
- [ ] **Step 2: Check the parity test bites,** reverting each change:
  - delete the `if a, ok := v.Auth[acct]; ok { return a }` lines in `AccountsView.RCAuthFor`, and `TestStateViews_AnswerAsTheModel` fails on `RCAuthFor("max-2")`;
  - make `GitHubView.GitHubErr` look up `repo+"x"`, and it fails.
- [ ] **Step 3:** Run `CC=clang CGO_ENABLED=1 go test -race ./core/` (green), then commit `feat(core): publish the whole state a replica needs`. The body names the views, the events and their publish order, `Snapshot`, `WireError`, the directives and the renames. End with both trailers.

---

## Package B: `core/rpc`, the wire, a server and a replicating client

New code only: app is untouched until C. One commit at the end.

### B1. The generator

- [ ] **Step 1:** Write the generator and its command.

**File: `core/rpc/internal/gen/gen.go`** (new)

```go
// Package gen generates core/rpc's wire code from core.Core (core/iface.go):
// every method's parameter and result types, the method table, the
// server's dispatch, and the client's methods. One source of truth keeps
// the two sides of the wire from drifting; TestGenerated_IsFresh fails when
// methods_gen.go no longer matches iface.go.
package gen

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Kinds of method, from a method's line comment in iface.go.
const (
	kindRequest = "request"
	kindLocal   = "local"
	kindCast    = "cast"
	kindClient  = "client"
)

type param struct{ name, typ string }

type method struct {
	name    string
	kind    string
	params  []param
	values  []string // non-error results
	withErr bool     // the last result is an error
}

// builtin are the predeclared types iface.go uses unqualified.
var builtin = map[string]bool{
	"bool": true, "string": true, "int": true, "int64": true, "uint32": true,
	"uint64": true, "float64": true, "byte": true, "rune": true, "any": true, "error": true,
}

// Generate returns methods_gen.go for the Core interface in src
// (core/iface.go's source).
func Generate(src []byte) ([]byte, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "iface.go", src, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	imports := map[string]string{}
	for _, imp := range file.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		name := path[strings.LastIndex(path, "/")+1:]
		if imp.Name != nil {
			name = imp.Name.Name
		}
		imports[name] = path
	}
	iface := findCore(file)
	if iface == nil {
		return nil, fmt.Errorf("no Core interface in iface.go")
	}
	used := map[string]bool{}
	var methods []method
	for _, f := range iface.Methods.List {
		ft, ok := f.Type.(*ast.FuncType)
		if !ok || len(f.Names) != 1 {
			return nil, fmt.Errorf("Core must hold methods only")
		}
		m := method{name: f.Names[0].Name, kind: kindRequest}
		if f.Comment != nil {
			switch c := strings.TrimSpace(f.Comment.Text()); c {
			case "rpc:local":
				m.kind = kindLocal
			case "rpc:cast":
				m.kind = kindCast
			case "rpc:client":
				m.kind = kindClient
			default:
				return nil, fmt.Errorf("%s: unknown line comment %q", m.name, c)
			}
		}
		for _, p := range ft.Params.List {
			typ, err := render(p.Type, used)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", m.name, err)
			}
			if len(p.Names) == 0 {
				return nil, fmt.Errorf("%s: unnamed parameter: its name is its wire field", m.name)
			}
			for _, n := range p.Names {
				m.params = append(m.params, param{name: n.Name, typ: typ})
			}
		}
		if ft.Results != nil {
			for _, r := range ft.Results.List {
				typ, err := render(r.Type, used)
				if err != nil {
					return nil, fmt.Errorf("%s: %w", m.name, err)
				}
				for range max(1, len(r.Names)) {
					if typ == "error" {
						m.withErr = true
					} else {
						if m.withErr {
							return nil, fmt.Errorf("%s: the error must be the last result", m.name)
						}
						m.values = append(m.values, typ)
					}
				}
			}
		}
		if len(m.values) > 2 || (len(m.values) == 2 && m.values[1] != "bool") || (len(m.values) == 2 && m.withErr) {
			return nil, fmt.Errorf("%s: results must be (), (T), (T, bool), with an optional error last", m.name)
		}
		if m.kind == kindCast && (len(m.values) > 0 || m.withErr) {
			return nil, fmt.Errorf("%s: a cast returns nothing", m.name)
		}
		methods = append(methods, m)
	}
	return emit(methods, imports, used)
}

// findCore returns the Core interface's type.
func findCore(file *ast.File) *ast.InterfaceType {
	for _, d := range file.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, s := range gd.Specs {
			ts, ok := s.(*ast.TypeSpec)
			if !ok || ts.Name.Name != "Core" {
				continue
			}
			if it, ok := ts.Type.(*ast.InterfaceType); ok {
				return it
			}
		}
	}
	return nil
}

// render writes a type expression as package rpc names it: core's own
// types qualified with core., other packages' kept, and records the
// packages it used.
func render(e ast.Expr, used map[string]bool) (string, error) {
	switch e := e.(type) {
	case *ast.Ident:
		if builtin[e.Name] {
			return e.Name, nil
		}
		used["core"] = true
		return "core." + e.Name, nil
	case *ast.SelectorExpr:
		pkg, ok := e.X.(*ast.Ident)
		if !ok {
			return "", fmt.Errorf("unsupported selector type")
		}
		used[pkg.Name] = true
		return pkg.Name + "." + e.Sel.Name, nil
	case *ast.ArrayType:
		if e.Len != nil {
			return "", fmt.Errorf("arrays are not supported, use a slice")
		}
		elt, err := render(e.Elt, used)
		return "[]" + elt, err
	case *ast.MapType:
		k, err := render(e.Key, used)
		if err != nil {
			return "", err
		}
		v, err := render(e.Value, used)
		return "map[" + k + "]" + v, err
	case *ast.StarExpr:
		x, err := render(e.X, used)
		return "*" + x, err
	}
	return "", fmt.Errorf("unsupported type %T", e)
}

// field is a parameter's Go field name: its name exported ("id" is ID).
func field(name string) string {
	if name == "id" {
		return "ID"
	}
	r := []rune(name)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

func emit(methods []method, imports map[string]string, used map[string]bool) ([]byte, error) {
	var b bytes.Buffer
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	p("// Code generated by core/rpc/internal/gen from core/iface.go; DO NOT EDIT.\n\npackage rpc\n\nimport (\n")
	p("\t\"encoding/json\"\n\n")
	used["core"] = true
	var paths []string
	for name := range used {
		path := imports[name]
		if name == "core" {
			path = "github.com/aidan-bailey/loom/core"
		}
		if path == "" {
			return nil, fmt.Errorf("package %s is not imported by iface.go", name)
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		p("\t%q\n", path)
	}
	p(")\n\n")

	for _, m := range methods {
		if m.kind == kindClient {
			continue
		}
		p("// %sParams are %s's parameters on the wire.\ntype %sParams struct {\n", m.name, m.name, m.name)
		for _, a := range m.params {
			p("\t%s %s `json:%q`\n", field(a.name), a.typ, a.name)
		}
		p("}\n\n")
		p("// %sResult is %s's result on the wire; an error travels in the reply.\ntype %sResult struct {\n", m.name, m.name, m.name)
		if len(m.values) > 0 {
			p("\tValue %s `json:\"value\"`\n", m.values[0])
		}
		if len(m.values) > 1 {
			p("\tOK bool `json:\"ok\"`\n")
		}
		p("}\n\n")
	}

	p("// methods are the Core methods the wire carries, in iface.go's order, with\n// how a client serves each (its line comment there).\nvar methods = []methodInfo{\n")
	for _, m := range methods {
		if m.kind == kindClient {
			continue
		}
		p("\t{Name: %q, Kind: kind%s, Params: %sParams{}, Result: %sResult{}},\n", m.name, strings.ToUpper(m.kind[:1])+m.kind[1:], m.name, m.name)
	}
	p("}\n\n")

	p("// dispatch calls method on b with params decoded, returning its result\n// for the reply and its error; found is false for a method the wire does\n// not carry.\nfunc dispatch(b core.Core, method string, params json.RawMessage) (result any, err error, found bool) {\n\tswitch method {\n")
	for _, m := range methods {
		if m.kind == kindClient {
			continue
		}
		p("\tcase %q:\n\t\tvar p %sParams\n\t\tif err := decodeParams(params, &p); err != nil {\n\t\t\treturn nil, err, true\n\t\t}\n", m.name, m.name)
		var args []string
		for _, a := range m.params {
			args = append(args, "p."+field(a.name))
		}
		call := fmt.Sprintf("b.%s(%s)", m.name, strings.Join(args, ", "))
		switch {
		case len(m.values) == 0 && !m.withErr:
			p("\t\t%s\n\t\treturn %sResult{}, nil, true\n", call, m.name)
		case len(m.values) == 0:
			p("\t\terr := %s\n\t\treturn %sResult{}, err, true\n", call, m.name)
		case len(m.values) == 1 && !m.withErr:
			p("\t\treturn %sResult{Value: %s}, nil, true\n", m.name, call)
		case len(m.values) == 1:
			p("\t\tv, err := %s\n\t\treturn %sResult{Value: v}, err, true\n", call, m.name)
		default:
			p("\t\tv, ok := %s\n\t\treturn %sResult{Value: v, OK: ok}, nil, true\n", call, m.name)
		}
	}
	p("\t}\n\treturn nil, nil, false\n}\n\n")

	for _, m := range methods {
		if m.kind == kindClient {
			continue
		}
		var sig, args, wire []string
		for _, a := range m.params {
			sig = append(sig, a.name+" "+a.typ)
			args = append(args, a.name)
			wire = append(wire, field(a.name)+": "+a.name)
		}
		results := append([]string(nil), m.values...)
		if m.withErr {
			results = append(results, "error")
		}
		ret := ""
		switch len(results) {
		case 0:
		case 1:
			ret = " " + results[0]
		default:
			ret = " (" + strings.Join(results, ", ") + ")"
		}
		p("// %s is core.Core's %s (%s).\nfunc (c *Client) %s(%s)%s {\n", m.name, m.name, m.kind, m.name, strings.Join(sig, ", "), ret)
		params := fmt.Sprintf("%sParams{%s}", m.name, strings.Join(wire, ", "))
		switch m.kind {
		case kindCast:
			p("\tc.cast(%q, %s)\n", m.name, params)
		case kindLocal:
			var vars []string
			for i, v := range m.values {
				p("\tvar v%d %s\n", i, v)
				vars = append(vars, fmt.Sprintf("v%d", i))
			}
			if m.withErr {
				p("\tvar err error\n")
				vars = append(vars, "err")
			}
			p("\tc.local(func(r *replica) { %s = r.%s(%s) })\n", strings.Join(vars, ", "), m.name, strings.Join(args, ", "))
			p("\treturn %s\n", strings.Join(vars, ", "))
		default:
			p("\tvar r %sResult\n", m.name)
			switch {
			case len(m.values) == 0 && !m.withErr:
				p("\t_ = c.request(%q, %s, &r)\n", m.name, params)
			case len(m.values) == 0:
				p("\treturn c.request(%q, %s, &r)\n", m.name, params)
			case len(m.values) == 1 && !m.withErr:
				p("\t_ = c.request(%q, %s, &r)\n\treturn r.Value\n", m.name, params)
			case len(m.values) == 1:
				p("\terr := c.request(%q, %s, &r)\n\treturn r.Value, err\n", m.name, params)
			default:
				p("\t_ = c.request(%q, %s, &r)\n\treturn r.Value, r.OK\n", m.name, params)
			}
		}
		p("}\n\n")
	}
	return format.Source(b.Bytes())
}
```

**File: `core/rpc/internal/gen/cmd/main.go`** (new)

```go
// Command cmd writes core/rpc/methods_gen.go from core/iface.go. Run it
// with go generate in core/rpc, after any change to core.Core.
package main

import (
	"fmt"
	"os"

	"github.com/aidan-bailey/loom/core/rpc/internal/gen"
)

func main() {
	src, err := os.ReadFile("../iface.go")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	out, err := gen.Generate(src)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
	if err := os.WriteFile("methods_gen.go", out, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

### B2. The package

- [ ] **Step 1:** Write each file in full.

**File: `core/rpc/rpc.go`** (new)

```go
// Package rpc carries core.Core over a connection (daemon stage 2):
// newline-delimited JSON frames, a Server serving a core.Loop, and a
// Client implementing core.Core.
//
// The client keeps a replica of the model's published state: it is sent
// the whole state when it connects (core.Model.Snapshot) and every change
// after (the state events Sync publishes), and answers every query from
// it, so only actions cross the wire. A request's reply follows the
// events the request produced, so a client that reads right after a
// request sees its effect. methods_gen.go, generated from core/iface.go,
// holds each method's wire types, the method table, the server's dispatch
// and the client's methods.
package rpc

//go:generate go run ./internal/gen/cmd

import (
	"encoding/json"
	"fmt"
	"reflect"
	"runtime/debug"

	"github.com/aidan-bailey/loom/core"
)

// Protocol is the wire's version, bumped on any incompatible change. Both
// sides send it in their hello and refuse a different one (stage 3 adds
// the newer-side-wins handshake).
const Protocol = 1

// pingMethod is the request that publishes and replies, and nothing else:
// a client's barrier (every frame the server wrote before the reply has
// been read when the reply arrives).
const pingMethod = "rpc.Ping"

// kind is how a client serves a method (core/iface.go's line comments).
type kind int

const (
	kindRequest kind = iota // a request, answered by a reply after its events
	kindLocal               // answered from the client's replica
	kindCast                // sent one way, with no reply
)

func (k kind) String() string {
	return [...]string{"request", "local", "cast"}[k]
}

// methodInfo describes one method the wire carries (methods).
type methodInfo struct {
	Name   string
	Kind   kind
	Params any
	Result any
}

// decodeParams decodes a request's parameters into p; none decode as
// zero values.
func decodeParams(params json.RawMessage, p any) error {
	if len(params) == 0 {
		return nil
	}
	if err := json.Unmarshal(params, p); err != nil {
		return &core.WireError{Code: core.CodeProtocol, Message: fmt.Sprintf("decode params: %v", err)}
	}
	return nil
}

// Frame is one line on the wire. Exactly one of its shapes is set:
//   - a hello: Hello;
//   - a request: Method, Params and a non-zero ID (a cast has no ID);
//   - a reply: ID, Result and Error;
//   - an event: Event (its Go type name) and Data;
//   - a fatal error: Fatal, which every later call re-raises.
type Frame struct {
	Hello  *Hello          `json:"hello,omitempty"`
	ID     uint64          `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *core.WireError `json:"error,omitempty"`
	Event  string          `json:"event,omitempty"`
	Data   json.RawMessage `json:"data,omitempty"`
	Fatal  *core.WireError `json:"fatal,omitempty"`
}

// Hello is each side's first frame.
type Hello struct {
	Protocol int    `json:"protocol"`
	Build    string `json:"build"`
}

// build names the binary: its module version and VCS revision, as Go
// stamps them.
func build() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	b := info.Main.Version
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			b += " " + s.Value
		case "vcs.modified":
			if s.Value == "true" {
				b += "+dirty"
			}
		}
	}
	return b
}

// eventTypes maps each event's wire name (its Go type name) to its type.
var eventTypes = func() map[string]reflect.Type {
	m := map[string]reflect.Type{}
	for _, ev := range core.EventTypes() {
		t := reflect.TypeOf(ev)
		m[t.Name()] = t
	}
	return m
}()

// encodeEvent is ev's frame.
func encodeEvent(ev core.Event) (Frame, error) {
	data, err := json.Marshal(ev)
	if err != nil {
		return Frame{}, err
	}
	return Frame{Event: reflect.TypeOf(ev).Name(), Data: data}, nil
}

// decodeEvent is the event an event frame carries.
func decodeEvent(f Frame) (core.Event, error) {
	t, ok := eventTypes[f.Event]
	if !ok {
		return nil, fmt.Errorf("unknown event %q", f.Event)
	}
	v := reflect.New(t)
	if err := json.Unmarshal(f.Data, v.Interface()); err != nil {
		return nil, fmt.Errorf("decode %s: %w", f.Event, err)
	}
	return v.Elem().Interface().(core.Event), nil
}
```

**File: `core/rpc/server.go`** (new)

```go
package rpc

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/log"
)

// Backend is what a Server serves: the model's Core, its whole state for
// a client that connects (Snapshot), and its wakes (core.Loop has all
// three).
type Backend interface {
	core.Core
	Snapshot() []core.Event
	Wakes() <-chan struct{}
}

// Server serves a Backend to any number of connections. After every call
// and every wake it publishes (Backend.Sync) to every connection, and a
// request's reply follows the events its call produced. A connection is
// sent the whole state (Backend.Snapshot) when it connects.
type Server struct {
	b Backend

	// mu orders publishing: a Sync and the frames it sends, the
	// connections, the fatal error, and each reply after the publish that
	// follows its call.
	mu    sync.Mutex
	conns map[*serverConn]bool
	fatal *core.WireError

	done      chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup
}

// NewServer serves b; it starts publishing on b's wakes.
func NewServer(b Backend) *Server {
	s := &Server{b: b, conns: map[*serverConn]bool{}, done: make(chan struct{})}
	s.wg.Add(1)
	go s.wakeLoop()
	return s
}

// wakeLoop publishes on every wake, until the backend stops or the server
// closes.
func (s *Server) wakeLoop() {
	defer s.wg.Done()
	for {
		select {
		case _, ok := <-s.b.Wakes():
			if !ok {
				return
			}
			s.mu.Lock()
			s.publishLocked()
			s.mu.Unlock()
		case <-s.done:
			return
		}
	}
}

// Close ends every connection and stops publishing.
func (s *Server) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		s.mu.Lock()
		for c := range s.conns {
			c.nc.Close()
		}
		s.mu.Unlock()
	})
	s.wg.Wait()
}

// Serve serves nc on a goroutine of its own until it closes (serveConn).
func (s *Server) Serve(nc io.ReadWriteCloser) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.serveConn(nc)
	}()
}

// serveConn serves one connection until it closes: the hello, the
// snapshot, then each request and cast in the order they arrive.
func (s *Server) serveConn(nc io.ReadWriteCloser) {
	c := newServerConn(nc)
	defer c.close()
	dec := json.NewDecoder(nc)
	var hello Frame
	if err := dec.Decode(&hello); err != nil {
		return
	}
	if hello.Hello == nil || hello.Hello.Protocol != Protocol {
		c.sendFrame(Frame{Error: &core.WireError{Code: core.CodeMismatch,
			Message: fmt.Sprintf("rpc: protocol mismatch: this server speaks %d", Protocol)}})
		return
	}
	c.sendFrame(Frame{Hello: &Hello{Protocol: Protocol, Build: build()}})

	s.mu.Lock()
	select {
	case <-s.done:
		s.mu.Unlock()
		return
	default:
	}
	if s.fatal == nil {
		snap, p := s.snapshot()
		if p != nil {
			s.setFatalLocked(p)
		}
		for _, ev := range snap {
			c.sendEvent(ev)
		}
	}
	if s.fatal != nil {
		c.sendFrame(Frame{Fatal: s.fatal})
	}
	s.conns[c] = true
	s.mu.Unlock()

	for {
		var f Frame
		if err := dec.Decode(&f); err != nil {
			break
		}
		s.handle(c, f)
	}
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
}

// handle runs one request, then publishes, then replies; or runs one
// cast, which publishes nothing: a cast changes no published state (it
// marks output, names the selection, or starts jobs, whose results
// publish when they land, on the loop's wake), and pane events cast up to
// ~60 times a second per session.
func (s *Server) handle(c *serverConn, f Frame) {
	var result any = struct{}{}
	var err error
	if f.Method != pingMethod {
		var found bool
		result, err, found = s.call(f.Method, f.Params)
		if !found {
			err = &core.WireError{Code: core.CodeProtocol, Message: fmt.Sprintf("rpc: unknown method %q", f.Method)}
		}
	}
	if f.ID == 0 {
		if err != nil {
			log.For("rpc").Warn("server.cast_failed", "method", f.Method, "err", err)
		}
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.publishLocked()
	c.sendReply(f.ID, result, err)
}

// call dispatches method, turning a panic into a fatal error that is also
// the call's error.
func (s *Server) call(method string, params json.RawMessage) (result any, err error, found bool) {
	defer func() {
		if r := recover(); r != nil {
			p := panicWire(r)
			s.mu.Lock()
			s.setFatalLocked(p)
			s.mu.Unlock()
			result, err, found = nil, p, true
		}
	}()
	return dispatch(s.b, method, params)
}

// snapshot is the backend's whole state, or the panic it raised.
func (s *Server) snapshot() (events []core.Event, p *core.WireError) {
	defer func() {
		if r := recover(); r != nil {
			events, p = nil, panicWire(r)
		}
	}()
	return s.b.Snapshot(), nil
}

// publishLocked sends what the backend produced since the last publish to
// every connection. s.mu is held.
func (s *Server) publishLocked() {
	if s.fatal != nil {
		return
	}
	events, p := s.sync()
	if p != nil {
		s.setFatalLocked(p)
		return
	}
	for _, ev := range events {
		f, err := encodeEvent(ev)
		if err != nil {
			log.For("rpc").Error("server.encode_event_failed", "event", fmt.Sprintf("%T", ev), "err", err)
			continue
		}
		for c := range s.conns {
			c.enqueue(f, coalesceKey(ev))
		}
	}
}

// sync is the backend's Sync, or the panic it raised.
func (s *Server) sync() (events []core.Event, p *core.WireError) {
	defer func() {
		if r := recover(); r != nil {
			events, p = nil, panicWire(r)
		}
	}()
	return s.b.Sync(), nil
}

// setFatalLocked records the backend's panic and tells every connection.
// s.mu is held.
func (s *Server) setFatalLocked(p *core.WireError) {
	if s.fatal != nil {
		return
	}
	s.fatal = p
	for c := range s.conns {
		c.sendFrame(Frame{Fatal: p})
	}
}

// panicWire is a recovered panic as a wire error: a core.LoopPanic keeps
// its value and stack in its message.
func panicWire(r any) *core.WireError {
	msg := fmt.Sprint(r)
	if err, ok := r.(error); ok {
		msg = err.Error()
	}
	return &core.WireError{Code: core.CodePanic, Message: msg}
}

// coalesceKey names a state event's slot in a connection's queue: a newer
// one replaces an older one still queued, which a client would only
// overwrite. Other events keep their place ("").
func coalesceKey(ev core.Event) string {
	switch ev := ev.(type) {
	case core.WorkspacesChanged:
		return "workspaces"
	case core.ModelChanged:
		return "model"
	case core.AccountsChanged:
		return "accounts"
	case core.GitHubChanged:
		return "github"
	case core.ViewsChanged:
		return fmt.Sprintf("views:%d", ev.WS)
	}
	return ""
}
```

**File: `core/rpc/server_conn.go`** (new)

```go
package rpc

import (
	"encoding/json"
	"io"
	"sync"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/log"
)

// serverConn is one connection's outbound side: a queue a writer
// goroutine drains, so a server never blocks on a slow client. A state
// event replaces an older one of the same key still queued, in its place,
// so the queue holds at most one of each; replies and other events keep
// their order.
type serverConn struct {
	nc io.ReadWriteCloser

	mu     sync.Mutex
	queue  []queued
	closed bool
	signal chan struct{}
	done   chan struct{}
}

type queued struct {
	key   string
	frame Frame
}

func newServerConn(nc io.ReadWriteCloser) *serverConn {
	c := &serverConn{nc: nc, signal: make(chan struct{}, 1), done: make(chan struct{})}
	go c.writeLoop()
	return c
}

// enqueue queues f, replacing a queued frame with the same non-empty key.
func (c *serverConn) enqueue(f Frame, key string) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	replaced := false
	if key != "" {
		for i := range c.queue {
			if c.queue[i].key == key {
				c.queue[i].frame, replaced = f, true
				break
			}
		}
	}
	if !replaced {
		c.queue = append(c.queue, queued{key: key, frame: f})
	}
	c.mu.Unlock()
	select {
	case c.signal <- struct{}{}:
	default:
	}
}

// sendFrame queues f, never coalesced.
func (c *serverConn) sendFrame(f Frame) { c.enqueue(f, "") }

// sendEvent queues ev's frame under its coalescing key.
func (c *serverConn) sendEvent(ev core.Event) {
	f, err := encodeEvent(ev)
	if err != nil {
		log.For("rpc").Error("server.encode_event_failed", "err", err)
		return
	}
	c.enqueue(f, coalesceKey(ev))
}

// sendReply queues the reply to request id.
func (c *serverConn) sendReply(id uint64, result any, err error) {
	f := Frame{ID: id, Error: core.ToWire(err)}
	if result != nil {
		data, merr := json.Marshal(result)
		if merr != nil {
			f.Result, f.Error = nil, &core.WireError{Code: core.CodeProtocol, Message: "rpc: encode result: " + merr.Error()}
		} else {
			f.Result = data
		}
	}
	c.sendFrame(f)
}

// writeLoop writes queued frames until the connection closes.
func (c *serverConn) writeLoop() {
	defer close(c.done)
	enc := json.NewEncoder(c.nc)
	for {
		c.mu.Lock()
		batch := c.queue
		c.queue = nil
		closed := c.closed
		c.mu.Unlock()
		for _, q := range batch {
			if err := enc.Encode(q.frame); err != nil {
				c.nc.Close()
				return
			}
		}
		if closed && len(batch) == 0 {
			return
		}
		if len(batch) == 0 {
			<-c.signal
		}
	}
}

// close stops queuing, lets the writer flush what is queued, and closes
// the connection.
func (c *serverConn) close() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	select {
	case c.signal <- struct{}{}:
	default:
	}
	<-c.done
	c.nc.Close()
}
```

**File: `core/rpc/replica.go`** (new)

```go
package rpc

import (
	"slices"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
)

// replica is a client's copy of the model's published state, kept by the
// state events (and the snapshot it is sent when it connects). It
// answers every rpc:local query, through the same view methods the
// model's own queries agree with, and hands out copies.
type replica struct {
	workspaces core.WorkspacesView
	model      core.ModelView
	accounts   core.AccountsView
	github     core.GitHubView
	views      map[core.WorkspaceID][]core.InstanceView
}

// state is what changed in the replica since the client's last Sync: the
// state events to hand the TUI next, newest state only.
type state struct {
	workspaces, model, accounts, github bool
	views                               map[core.WorkspaceID]bool
}

// apply takes a state event into r, marking it in changed, and reports
// whether ev was one.
func (r *replica) apply(ev core.Event, changed *state) bool {
	switch ev := ev.(type) {
	case core.WorkspacesChanged:
		r.workspaces = core.WorkspacesView{Views: ev.Views, Classic: ev.Classic}
		loaded := map[core.WorkspaceID]bool{}
		for _, v := range ev.Views {
			loaded[v.ID] = true
		}
		for id := range r.views {
			if !loaded[id] {
				delete(r.views, id)
			}
		}
		changed.workspaces = true
	case core.ModelChanged:
		r.model, changed.model = ev.View, true
	case core.AccountsChanged:
		r.accounts, changed.accounts = ev.View, true
	case core.GitHubChanged:
		r.github, changed.github = ev.View, true
	case core.ViewsChanged:
		if r.views == nil {
			r.views = map[core.WorkspaceID][]core.InstanceView{}
		}
		r.views[ev.WS] = ev.Views
		if changed.views == nil {
			changed.views = map[core.WorkspaceID]bool{}
		}
		changed.views[ev.WS] = true
	default:
		return false
	}
	return true
}

// events are the state events for what changed, newest state, in the
// order the model publishes them: the workspaces, the model, account and
// GitHub state, then each changed workspace's instance views, in
// workspace order.
func (r *replica) events(changed state) []core.Event {
	var out []core.Event
	if changed.workspaces {
		out = append(out, core.WorkspacesChanged{Views: core.CloneWorkspaceViews(r.workspaces.Views), Classic: r.workspaces.Classic})
	}
	if changed.model {
		out = append(out, core.ModelChanged{View: r.model.Clone()})
	}
	if changed.accounts {
		out = append(out, core.AccountsChanged{View: r.accounts.Clone()})
	}
	if changed.github {
		out = append(out, core.GitHubChanged{View: r.github.Clone()})
	}
	for _, w := range r.workspaces.Views {
		if changed.views[w.ID] {
			out = append(out, core.ViewsChanged{WS: w.ID, Views: core.CloneViews(r.views[w.ID])})
		}
	}
	return out
}

// The rpc:local queries (core.Core), answered from the replica.

func (r *replica) RestoreFailed() []string { return slices.Clone(r.model.RestoreFailed) }
func (r *replica) OpenNames() []string     { return slices.Clone(r.model.OpenNames) }
func (r *replica) Workspace(id core.WorkspaceID) (core.WorkspaceView, bool) {
	return r.workspaces.Workspace(id)
}
func (r *replica) Classic() (core.WorkspaceView, bool) { return r.workspaces.ClassicView() }
func (r *replica) Tabs() []core.WorkspaceView          { return r.workspaces.Tabs() }
func (r *replica) IsLoaded(id core.WorkspaceID) bool   { return r.workspaces.IsLoaded(id) }
func (r *replica) Registry() core.RegistryView         { return r.model.Clone().Registry }

// Views is the loaded workspace id's instance views; nil for one not
// loaded, as the model answers.
func (r *replica) Views(id core.WorkspaceID) []core.InstanceView {
	if !r.workspaces.IsLoaded(id) {
		return nil
	}
	views, ok := r.views[id]
	if !ok {
		return []core.InstanceView{}
	}
	return core.CloneViews(views)
}

// View is the view of the instance id, which a loaded workspace holds.
func (r *replica) View(id core.InstanceID) (core.InstanceView, bool) {
	for _, w := range r.workspaces.Views {
		for _, v := range r.views[w.ID] {
			if v.ID == id {
				return core.CloneViews([]core.InstanceView{v})[0], true
			}
		}
	}
	return core.InstanceView{}, false
}

func (r *replica) Program() string                   { return r.model.Program }
func (r *replica) RCAuth() session.RemoteControlAuth { return r.model.RCAuth }
func (r *replica) GitHubSnapshot(repo string) (github.Snapshot, bool) {
	return r.github.GitHubSnapshot(repo)
}
func (r *replica) GitHubErr(repo string) error       { return r.github.GitHubErr(repo) }
func (r *replica) GitHubUnavailable() bool           { return r.github.GitHubUnavailable() }
func (r *replica) GitHubUnavailableReason() string   { return r.github.GitHubUnavailableReason() }
func (r *replica) AccountNames() core.AccountNames   { return r.accounts.AccountNames() }
func (r *replica) AccountsLoaded() bool              { return r.accounts.AccountsLoaded() }
func (r *replica) HasExtraAccounts() bool            { return r.accounts.HasExtraAccounts() }
func (r *replica) ClaudeProgram() string             { return r.accounts.ClaudeProgram }
func (r *replica) AccountLoggedOut(acct string) bool { return r.accounts.AccountLoggedOut(acct) }
func (r *replica) Account(name string) (account.Account, bool) {
	return r.accounts.Account(name)
}
func (r *replica) RCAuthFor(acct string) session.RemoteControlAuth {
	return r.accounts.RCAuthFor(acct)
}
func (r *replica) AccountSync(name string) (account.SyncReport, bool) {
	return r.accounts.AccountSync(name)
}
func (r *replica) AccountUsage(name string) (account.Usage, error) {
	return r.accounts.AccountUsage(name)
}
func (r *replica) AccountEnv(name string) ([]string, error) { return r.accounts.AccountEnv(name) }
```

**File: `core/rpc/client.go`** (new)

```go
package rpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/log"
)

// errClosed is a call's error once the connection is gone.
var errClosed = errors.New("rpc: connection closed")

// Client is core.Core over a connection to a Server. It keeps a replica
// of the model's published state (sent whole when it connects, then every
// change), answers every rpc:local query from it, sends casts one way,
// and waits for each request's reply, which follows the events the
// request produced: a read right after a request sees its effect. Sync
// returns the events received since the last Sync, the state events
// coalesced to the newest state. Wakes signals when events arrive.
//
// A panic the model raised reaches the caller that met it, and every
// call after it panics too, so the TUI's own recovery restores the
// terminal.
type Client struct {
	nc  io.ReadWriteCloser
	wmu sync.Mutex
	enc *json.Encoder

	// synchronous makes every rpc:local read (Sync included) a ping first,
	// so it sees everything the model published by then, and every cast a
	// ping after, so it has reached the model when it returns: a test seam
	// (InProcessForTest), for tests that change or read the model directly.
	synchronous bool

	mu      sync.Mutex
	rep     replica
	changed state
	queue   []core.Event
	pending map[uint64]chan Frame
	nextID  uint64
	fatal   *core.WireError
	closed  bool

	wake       chan struct{}
	done       chan struct{}
	readerDone chan struct{}
	closeOnce  sync.Once
	wakeOnce   sync.Once
}

// Dial says hello on nc, starts reading, and returns once the replica
// holds the server's snapshot.
func Dial(nc io.ReadWriteCloser) (*Client, error) { return dial(nc, false) }

func dial(nc io.ReadWriteCloser, synchronous bool) (*Client, error) {
	c := &Client{
		nc:          nc,
		enc:         json.NewEncoder(nc),
		synchronous: synchronous,
		pending:     map[uint64]chan Frame{},
		wake:        make(chan struct{}, 1),
		done:        make(chan struct{}),
		readerDone:  make(chan struct{}),
	}
	if err := c.write(Frame{Hello: &Hello{Protocol: Protocol, Build: build()}}); err != nil {
		nc.Close()
		return nil, err
	}
	dec := json.NewDecoder(nc)
	var hello Frame
	if err := dec.Decode(&hello); err != nil {
		nc.Close()
		return nil, fmt.Errorf("rpc: read hello: %w", err)
	}
	if hello.Error != nil {
		nc.Close()
		return nil, hello.Error
	}
	if hello.Hello == nil || hello.Hello.Protocol != Protocol {
		nc.Close()
		return nil, &core.WireError{Code: core.CodeMismatch, Message: "rpc: the server speaks another protocol"}
	}
	go c.read(dec)
	if err := c.ping(); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// read is the client's reader: it applies each event to the replica (or
// queues it), records a fatal error, and hands each reply to its caller,
// in the order the server wrote them.
func (c *Client) read(dec *json.Decoder) {
	defer close(c.readerDone)
	for {
		var f Frame
		if err := dec.Decode(&f); err != nil {
			c.fail(err)
			return
		}
		switch {
		case f.Event != "":
			ev, err := decodeEvent(f)
			if err != nil {
				log.For("rpc").Error("client.bad_event", "err", err)
				continue
			}
			c.mu.Lock()
			if !c.rep.apply(ev, &c.changed) {
				c.queue = append(c.queue, ev)
			}
			c.mu.Unlock()
			c.signal()
		case f.Fatal != nil:
			c.mu.Lock()
			c.fatal = f.Fatal
			c.mu.Unlock()
			c.signal()
		case f.ID != 0:
			c.mu.Lock()
			ch := c.pending[f.ID]
			delete(c.pending, f.ID)
			c.mu.Unlock()
			if ch != nil {
				ch <- f
			}
		}
	}
}

// fail closes the client after its connection failed or was closed:
// every waiting call returns errClosed.
func (c *Client) fail(err error) {
	c.mu.Lock()
	wasOpen := !c.closed
	c.closed = true
	c.mu.Unlock()
	if wasOpen && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrClosedPipe) {
		log.For("rpc").Warn("client.connection_failed", "err", err)
	}
	c.closeOnce.Do(func() { close(c.done) })
}

// signal wakes the client's user: at most one wake waits.
func (c *Client) signal() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// Wakes signals that events arrived for Sync. Wakes coalesce; Close
// closes it.
func (c *Client) Wakes() <-chan struct{} { return c.wake }

// Close ends the connection and waits for the reader, the only sender on
// Wakes, then closes Wakes.
func (c *Client) Close() {
	c.nc.Close()
	<-c.readerDone
	c.wakeOnce.Do(func() { close(c.wake) })
}

// write sends one frame.
func (c *Client) write(f Frame) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.enc.Encode(f)
}

// checkFatalLocked re-raises the model's panic. c.mu is held.
func (c *Client) checkFatalLocked() {
	if c.fatal != nil {
		panic(c.fatal)
	}
}

// request sends method with params, waits for its reply and decodes its
// result into result; it returns the method's error. A panic the model
// raised is re-raised here.
func (c *Client) request(method string, params, result any) error {
	data, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("rpc: encode %s: %w", method, err)
	}
	c.mu.Lock()
	c.checkFatalLocked()
	if c.closed {
		c.mu.Unlock()
		log.For("rpc").Warn("client.call_after_close", "method", method)
		return errClosed
	}
	c.nextID++
	id := c.nextID
	ch := make(chan Frame, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	if err := c.write(Frame{ID: id, Method: method, Params: data}); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return errClosed
	}
	var reply Frame
	select {
	case reply = <-ch:
	case <-c.done:
		return errClosed
	}
	if reply.Error != nil && reply.Error.Code == core.CodePanic {
		c.mu.Lock()
		c.fatal = reply.Error
		c.mu.Unlock()
		panic(reply.Error)
	}
	if result != nil && len(reply.Result) > 0 {
		if err := json.Unmarshal(reply.Result, result); err != nil {
			return fmt.Errorf("rpc: decode %s result: %w", method, err)
		}
	}
	return core.FromWire(reply.Error)
}

// cast sends method with params one way.
func (c *Client) cast(method string, params any) {
	data, err := json.Marshal(params)
	if err != nil {
		log.For("rpc").Error("client.encode_failed", "method", method, "err", err)
		return
	}
	c.mu.Lock()
	c.checkFatalLocked()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return
	}
	_ = c.write(Frame{Method: method, Params: data})
	if c.synchronous {
		_ = c.ping()
	}
}

// ping is the barrier: when it returns, everything the server published
// before answering it is in the replica.
func (c *Client) ping() error { return c.request(pingMethod, struct{}{}, nil) }

// local runs f on the replica, after a ping when synchronous.
func (c *Client) local(f func(*replica)) {
	if c.synchronous {
		_ = c.ping()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checkFatalLocked()
	f(&c.rep)
}

// Sync returns the events received since the last Sync (rpc:client):
// first the state events, coalesced to the newest state, in the model's
// order, then the others in the order they arrived.
func (c *Client) Sync() []core.Event {
	var out []core.Event
	c.local(func(r *replica) {
		out = append(r.events(c.changed), c.queue...)
		c.changed, c.queue = state{}, nil
	})
	return out
}

var _ core.Core = (*Client)(nil)
```

**File: `core/rpc/inprocess.go`** (new)

```go
package rpc

import (
	"net"

	"github.com/aidan-bailey/loom/core"
)

// InProcess runs model as a daemon will (stage 3), inside this process:
// on its own loop (core.Start), served by a Server over one end of an
// in-memory pipe, with a Client on the other. It returns the client,
// which implements core.Core, and the function that stops all three.
func InProcess(model *core.Model) (*Client, func(), error) {
	return inProcess(core.Start(model), false)
}

// inProcess serves loop to a client over a pipe; synchronous makes the
// client ping around its local reads and casts (InProcessForTest).
func inProcess(loop *core.Loop, synchronous bool) (*Client, func(), error) {
	srv := NewServer(loop)
	a, b := net.Pipe()
	srv.Serve(a)
	c, err := dial(b, synchronous)
	if err != nil {
		srv.Close()
		loop.Stop()
		return nil, nil, err
	}
	stop := func() {
		c.Close()
		srv.Close()
		loop.Stop()
	}
	return c, stop, nil
}
```

**File: `core/rpc/seams.go`** (new)

```go
package rpc

import "github.com/aidan-bailey/loom/core"

// InProcessForTest is InProcess for a test of the client's user (the
// TUI): model runs on a loop that holds its jobs (core.StartForTest,
// returned for its seams), and the client is synchronous, as the TUI's
// in-process calls were in stage 1E: it pings before every local read, so
// a read sees whatever the model published by then (a change the test
// made to the model directly included), and after every cast, so a cast
// has reached the model before the test reads it. Production code never
// calls it.
func InProcessForTest(model *core.Model) (*Client, *core.Loop, func(), error) {
	loop := core.StartForTest(model)
	c, stop, err := inProcess(loop, true)
	return c, loop, stop, err
}

// FlushForTest is the client's barrier (a ping): when it returns,
// everything the server published before answering is in the replica.
func (c *Client) FlushForTest() error { return c.ping() }
```

- [ ] **Step 2: Generate.** Run `cd core/rpc && CGO_ENABLED=0 go run ./internal/gen/cmd && cd ../..`. It writes `core/rpc/methods_gen.go`, about 1600 lines: a `…Params`/`…Result` pair per method, `methods`, `dispatch`, and the `Client`'s request, cast and local methods. Then run `CGO_ENABLED=0 go build ./core/rpc && CGO_ENABLED=0 go vet ./core/rpc`, which must be clean.

### B3. Tests

- [ ] **Step 1: `TestMain`.** Run `sed 's/^package core$/package rpc/' core/testmain_test.go > core/rpc/testmain_test.go`. This gives a private tmux server and throwaway loom dirs, which `TestEveryConfigReachingPackageIsolatesLoomDirs` requires.
- [ ] **Step 2:** Write the tests.

**File: `core/rpc/generate_test.go`** (new)

```go
package rpc

import (
	"os"
	"reflect"
	"testing"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/core/rpc/internal/gen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGenerated_IsFresh fails when methods_gen.go no longer matches
// core/iface.go: run `go generate ./core/rpc` after changing core.Core.
func TestGenerated_IsFresh(t *testing.T) {
	src, err := os.ReadFile("../iface.go")
	require.NoError(t, err)
	want, err := gen.Generate(src)
	require.NoError(t, err)
	got, err := os.ReadFile("methods_gen.go")
	require.NoError(t, err)
	assert.Equal(t, string(want), string(got), "methods_gen.go is stale: run go generate ./core/rpc")
}

// TestMethods_CoverCore: the wire carries every core.Core method but Sync,
// which a client answers itself.
func TestMethods_CoverCore(t *testing.T) {
	iface := reflect.TypeOf((*core.Core)(nil)).Elem()
	var want []string
	for i := 0; i < iface.NumMethod(); i++ {
		if name := iface.Method(i).Name; name != "Sync" {
			want = append(want, name)
		}
	}
	var got []string
	for _, m := range methods {
		got = append(got, m.Name)
	}
	assert.ElementsMatch(t, want, got)
}
```

**File: `core/rpc/wire_test.go`** (new)

```go
package rpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/git"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "rewrite docs/specs/protocol.md")

// TestEvents_RoundTrip: every event survives its frame, the ones holding
// errors with their text and sentinels.
func TestEvents_RoundTrip(t *testing.T) {
	samples := core.EventTypes()
	samples = append(samples,
		core.Notice{Err: core.ErrNoSession},
		core.Reply{Req: 2, ID: 3, Err: errors.New("kill x: worktree locked")},
		core.ViewsChanged{WS: 1, Views: []core.InstanceView{{
			ID: 4, Title: "x", Status: session.Running, StatusSince: time.Unix(100, 0),
			Diff: git.DiffStats{Added: 1, Error: errors.New("no base")}, HasDiff: true,
		}}},
		core.GitHubChanged{View: core.GitHubView{
			Snapshots: map[string]github.Snapshot{"/r": {Issues: map[int]github.Issue{7: {Number: 7}}}},
			Errs:      map[string]string{"/s": "no remote"},
		}},
	)
	for _, ev := range samples {
		f, err := encodeEvent(ev)
		require.NoError(t, err)
		line, err := json.Marshal(f)
		require.NoError(t, err)
		var back Frame
		require.NoError(t, json.Unmarshal(line, &back))
		got, err := decodeEvent(back)
		require.NoError(t, err)
		assert.Equal(t, fmt.Sprintf("%+v", ev), fmt.Sprintf("%+v", got), "%T", ev)
	}
	got, err := decodeEvent(mustFrame(t, core.Notice{Err: core.ErrNoSession}))
	require.NoError(t, err)
	assert.True(t, errors.Is(got.(core.Notice).Err, core.ErrNoSession), "a sentinel survives the wire")
}

func mustFrame(t *testing.T, ev core.Event) Frame {
	f, err := encodeEvent(ev)
	require.NoError(t, err)
	return f
}

// TestProtocolReference keeps docs/specs/protocol.md, the wire's reference,
// in step with the code: every method's kind, parameters and result, and
// every event, as their frames encode zero values. Run with -update to
// rewrite it after a change (and bump Protocol if the change is
// incompatible).
func TestProtocolReference(t *testing.T) {
	var b bytes.Buffer
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	p("# Loom wire protocol, version %d\n\n", Protocol)
	p("Generated by `go test ./core/rpc -run TestProtocolReference -update`; do not edit.\n\n")
	p("Newline-delimited JSON frames (`core/rpc.Frame`). Each side's first frame is a hello\n")
	p("(`{\"hello\":{\"protocol\":%d,\"build\":\"…\"}}`). A request is `{\"id\":N,\"method\":M,\"params\":{…}}`;\n", Protocol)
	p("its reply `{\"id\":N,\"result\":{…},\"error\":{\"code\":…,\"message\":…}}` follows the events the\n")
	p("request produced. A cast has no id and no reply. An event is `{\"event\":Name,\"data\":{…}}`.\n")
	p("`{\"fatal\":{…}}` says the model panicked; every later call fails with it. A client is sent\n")
	p("the whole published state when it connects, then every change.\n\n")
	p("Kinds: **request** (a reply follows), **cast** (one way), **local** (a client answers it from its\n")
	p("replica of the published state; the server answers it as a request too).\n\n")
	p("## Methods\n\n")
	for _, m := range methods {
		p("### %s (%s)\n\nParams: `%s`\n\nResult: `%s`\n\n", m.Name, m.Kind, compact(t, m.Params), compact(t, m.Result))
	}
	p("## Events\n\n")
	for _, ev := range core.EventTypes() {
		p("### %s\n\n`%s`\n\n", reflect.TypeOf(ev).Name(), compact(t, ev))
	}
	const path = "../../docs/specs/protocol.md"
	if *update {
		require.NoError(t, os.WriteFile(path, b.Bytes(), 0o644))
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "run with -update to write it")
	assert.Equal(t, string(want), b.String(), "the protocol changed: run with -update, and bump Protocol if it is incompatible")
}

func compact(t *testing.T, v any) string {
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return string(data)
}
```

**File: `core/rpc/client_test.go`** (new)

```go
package rpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/account"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// workspace is a fixture workspace over a fresh config dir, holding insts.
func workspace(t *testing.T, name string, insts ...*session.Instance) *core.Workspace {
	t.Helper()
	dir := t.TempDir()
	state := config.LoadStateFrom(dir)
	storage, err := session.NewStorage(state, dir)
	require.NoError(t, err)
	ws := core.NewWorkspace(core.WorkspaceParts{
		Ctx:     &config.WorkspaceContext{Name: name, ConfigDir: dir, RepoPath: t.TempDir()},
		Storage: storage, Config: config.DefaultConfig(), State: state,
	})
	for _, inst := range insts {
		ws.AddForTest(inst)
	}
	return ws
}

// running is a fixture instance marked Running, never started.
func running(t *testing.T, title string) *session.Instance {
	t.Helper()
	inst, err := session.NewInstance(session.InstanceOptions{Title: title, Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	require.NoError(t, inst.TransitionTo(session.Running))
	return inst
}

// pair serves loop to a client over a pipe (not synchronous: what
// production gets), both stopped when the test ends.
func pair(t *testing.T, loop *core.Loop) *Client {
	t.Helper()
	c, stop, err := inProcess(loop, false)
	require.NoError(t, err)
	t.Cleanup(stop)
	return c
}

// overWire is each of vs as it reads after a trip over the wire, decoded
// into its own type: what a replica's answers are compared with.
func overWire(t *testing.T, vs []any) []any {
	t.Helper()
	out := make([]any, len(vs))
	for i, v := range vs {
		if v == nil {
			continue
		}
		data, err := json.Marshal(v)
		require.NoError(t, err)
		back := reflect.New(reflect.TypeOf(v))
		require.NoError(t, json.Unmarshal(data, back.Interface()))
		out[i] = back.Elem().Interface()
	}
	return out
}

// TestReplica_AnswersAsTheModel: every rpc:local query the client answers
// from its replica gives what the model gives, over the wire, for known
// names and misses alike.
func TestReplica_AnswersAsTheModel(t *testing.T) {
	model := core.NewForTest(core.Options{Program: "claude"})
	x, y := running(t, "x"), running(t, "y")
	a, b := workspace(t, "a", x), workspace(t, "b", y)
	model.SetWorkspacesForTest(nil, []*core.Workspace{a, b})
	reg := account.LoadRegistry(t.TempDir())
	_, _, err := reg.Create("max-2", t.TempDir())
	require.NoError(t, err)
	model.AdoptAccountsForTest(reg)
	t.Cleanup(func() { session.SetAccountDirs(nil, nil) })
	model.SetAccountAuthForTest(map[string]session.RemoteControlAuth{"max-2": {State: session.RemoteControlAuthBlocked, Reason: "r"}})
	model.SetAccountUsageForTest("max-2", account.Usage{Available: true, Plan: "max", At: time.Unix(10, 0)}, errors.New("probe failed"))
	loop := core.StartForTest(model)
	loop.DeliverForTest(core.GitHubResultForTest(true, "", map[string]github.Snapshot{
		"/r": {Issues: map[int]github.Issue{7: {Number: 7, Title: "t"}}},
	}, map[string]error{"/s": errors.New("no remote")}))
	c := pair(t, loop)

	wsIDs := []core.WorkspaceID{0, 99}
	for _, v := range loop.Tabs() {
		wsIDs = append(wsIDs, v.ID)
	}
	var instIDs []core.InstanceID
	for _, id := range wsIDs {
		for _, v := range loop.Views(id) {
			instIDs = append(instIDs, v.ID)
		}
	}
	require.Len(t, instIDs, 2, "fixture: both sessions are listed")
	instIDs = append(instIDs, 0, 99)

	same := func(name string, ask func(core.Core) []any) {
		t.Helper()
		assert.Equal(t, overWire(t, ask(loop)), ask(c), name)
	}
	errText := func(err error) string { return fmt.Sprint(err) }
	same("Tabs", func(k core.Core) []any { return []any{k.Tabs()} })
	same("Classic", func(k core.Core) []any { v, ok := k.Classic(); return []any{v, ok} })
	for _, id := range wsIDs {
		same(fmt.Sprintf("Workspace(%d)", id), func(k core.Core) []any { v, ok := k.Workspace(id); return []any{v, ok} })
		same(fmt.Sprintf("IsLoaded(%d)", id), func(k core.Core) []any { return []any{k.IsLoaded(id)} })
		same(fmt.Sprintf("Views(%d)", id), func(k core.Core) []any { return []any{k.Views(id)} })
	}
	for _, id := range instIDs {
		same(fmt.Sprintf("View(%d)", id), func(k core.Core) []any { v, ok := k.View(id); return []any{v, ok} })
	}
	same("Registry", func(k core.Core) []any { return []any{k.Registry()} })
	same("RestoreFailed", func(k core.Core) []any { return []any{k.RestoreFailed()} })
	same("OpenNames", func(k core.Core) []any { return []any{k.OpenNames()} })
	same("Program", func(k core.Core) []any { return []any{k.Program()} })
	same("RCAuth", func(k core.Core) []any { return []any{k.RCAuth()} })
	same("AccountNames", func(k core.Core) []any { return []any{k.AccountNames()} })
	same("AccountsLoaded", func(k core.Core) []any { return []any{k.AccountsLoaded()} })
	same("HasExtraAccounts", func(k core.Core) []any { return []any{k.HasExtraAccounts()} })
	same("ClaudeProgram", func(k core.Core) []any { return []any{k.ClaudeProgram()} })
	for _, name := range []string{"", account.DefaultName, "max-2", "nobody"} {
		same("Account "+name, func(k core.Core) []any { v, ok := k.Account(name); return []any{v, ok} })
		same("RCAuthFor "+name, func(k core.Core) []any { return []any{k.RCAuthFor(name)} })
		same("AccountLoggedOut "+name, func(k core.Core) []any { return []any{k.AccountLoggedOut(name)} })
		same("AccountSync "+name, func(k core.Core) []any { v, ok := k.AccountSync(name); return []any{v, ok} })
		same("AccountUsage "+name, func(k core.Core) []any { v, err := k.AccountUsage(name); return []any{v, errText(err)} })
		same("AccountEnv "+name, func(k core.Core) []any { v, err := k.AccountEnv(name); return []any{v, errText(err)} })
	}
	for _, repo := range []string{"/r", "/s", "/nowhere"} {
		same("GitHubSnapshot "+repo, func(k core.Core) []any { v, ok := k.GitHubSnapshot(repo); return []any{v, ok} })
		same("GitHubErr "+repo, func(k core.Core) []any { return []any{errText(k.GitHubErr(repo))} })
	}
	same("GitHubUnavailable", func(k core.Core) []any { return []any{k.GitHubUnavailable()} })
	same("GitHubUnavailableReason", func(k core.Core) []any { return []any{k.GitHubUnavailableReason()} })
}

// TestRequest_ItsEventsArriveBeforeItsReply: a read right after a request
// sees the request's effect, with no barrier.
func TestRequest_ItsEventsArriveBeforeItsReply(t *testing.T) {
	model := core.NewForTest(core.Options{})
	x := running(t, "x")
	model.SetWorkspacesForTest(workspace(t, "a", x), nil)
	loop := core.StartForTest(model)
	c := pair(t, loop)
	id := model.IDOfForTest(x)

	c.Kill(id, 0)
	v, ok := c.View(id)
	require.True(t, ok)
	assert.Equal(t, session.Deleting, v.Status, "the kill's Deleting reached the replica before its reply")
}

// TestCast_ReachesTheModel: a cast is applied in order, with no reply.
func TestCast_ReachesTheModel(t *testing.T) {
	model := core.NewForTest(core.Options{})
	loop := core.StartForTest(model)
	c := pair(t, loop)

	c.MarkOutput("loom_x")
	require.NoError(t, c.FlushForTest())
	assert.True(t, model.OutputMarkedForTest("loom_x"))
}

// TestSync_CoalescesStateAndKeepsTheRestInOrder: Sync hands out the newest
// state, once per kind, ahead of the other events in the order they came.
func TestSync_CoalescesStateAndKeepsTheRestInOrder(t *testing.T) {
	model := core.NewForTest(core.Options{Program: "a"})
	loop := core.StartForTest(model)
	c := pair(t, loop)
	c.Sync() // the snapshot

	c.SetProgram("b")
	loop.DeliverForTest(core.RosterResultForTest(nil, errors.New("no roster"), time.Now()))
	c.SetProgram("c")
	events := c.Sync()
	var models []string
	for _, ev := range events {
		if m, ok := ev.(core.ModelChanged); ok {
			models = append(models, m.View.Program)
		}
	}
	assert.Equal(t, []string{"c"}, models, "one ModelChanged, the newest")
	_, first := events[0].(core.ModelChanged)
	assert.True(t, first, "state first")
}

// TestWake_ALoopResultReachesTheClient: on a production loop, a job's
// result lands on the loop, the server publishes on its wake, and the
// client wakes with the events.
func TestWake_ALoopResultReachesTheClient(t *testing.T) {
	model := core.NewForTest(core.Options{})
	// An empty model polls GitHub for the cwd's repository: hold that
	// poll, so the probe is the tick's only job and nothing runs gh.
	model.SetGateForTest("github", true, time.Now())
	loop := core.Start(model)
	c := pair(t, loop)
	c.Sync()

	loop.TickForTest() // its probe runs on a goroutine of its own
	deadline := time.After(10 * time.Second)
	for {
		select {
		case <-c.Wakes():
		case <-deadline:
			t.Fatal("no wake")
		}
		for _, ev := range c.Sync() {
			if _, ok := ev.(core.HealthChecked); ok {
				return
			}
		}
	}
}

// panicky is a backend whose SetProgram panics.
type panicky struct{ *core.Loop }

func (panicky) SetProgram(string) { panic("boom") }

// TestPanic_ReachesTheCallerAndEveryLaterCall: the model's panic is raised
// in the caller, and every call after it panics too.
func TestPanic_ReachesTheCallerAndEveryLaterCall(t *testing.T) {
	loop := core.StartForTest(core.NewForTest(core.Options{}))
	t.Cleanup(loop.Stop)
	srv := NewServer(panicky{loop})
	t.Cleanup(srv.Close)
	a, b := net.Pipe()
	srv.Serve(a)
	c, err := Dial(b)
	require.NoError(t, err)
	t.Cleanup(c.Close)

	catch := func(f func()) (p any) {
		defer func() { p = recover() }()
		f()
		return nil
	}
	p := catch(func() { c.SetProgram("x") })
	require.NotNil(t, p, "the caller panics")
	w, ok := p.(*core.WireError)
	require.True(t, ok, "with the wire's panic, got %T", p)
	assert.Equal(t, core.CodePanic, w.Code)
	assert.Contains(t, w.Message, "boom")
	assert.NotNil(t, catch(func() { c.Program() }), "a later local read panics")
	assert.NotNil(t, catch(func() { c.Sync() }), "and Sync")
}

// TestHello_RefusesAnotherProtocol: a server answers a client speaking
// another protocol with a mismatch.
func TestHello_RefusesAnotherProtocol(t *testing.T) {
	loop := core.StartForTest(core.NewForTest(core.Options{}))
	t.Cleanup(loop.Stop)
	srv := NewServer(loop)
	t.Cleanup(srv.Close)
	a, b := net.Pipe()
	srv.Serve(a)
	t.Cleanup(func() { b.Close() })
	require.NoError(t, json.NewEncoder(b).Encode(Frame{Hello: &Hello{Protocol: Protocol + 1}}))
	var f Frame
	require.NoError(t, json.NewDecoder(b).Decode(&f))
	require.NotNil(t, f.Error)
	assert.Equal(t, core.CodeMismatch, f.Error.Code)
}

// TestClosed_CallsReturn: once the server is gone, calls return at once.
func TestClosed_CallsReturn(t *testing.T) {
	loop := core.StartForTest(core.NewForTest(core.Options{}))
	t.Cleanup(loop.Stop)
	srv := NewServer(loop)
	a, b := net.Pipe()
	srv.Serve(a)
	c, err := Dial(b)
	require.NoError(t, err)
	t.Cleanup(c.Close)
	srv.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		c.Kill(1, 0)
		assert.Error(t, c.Save(1))
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a call blocked on a closed connection")
	}
}
```

**File: `core/rpc/server_conn_test.go`** (new)

```go
package rpc

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestServerConn_CoalescesQueuedState: a newer state event replaces an
// older one still queued, in its place; replies and other events keep
// their order.
func TestServerConn_CoalescesQueuedState(t *testing.T) {
	c := &serverConn{signal: make(chan struct{}, 1)}
	c.enqueue(Frame{Event: "ViewsChanged", Data: []byte(`"a"`)}, "views:1")
	c.enqueue(Frame{ID: 7}, "")
	c.enqueue(Frame{Event: "ViewsChanged", Data: []byte(`"b"`)}, "views:1")
	c.enqueue(Frame{Event: "ViewsChanged", Data: []byte(`"c"`)}, "views:2")
	c.enqueue(Frame{Event: "Notice"}, "")
	var got []string
	for _, q := range c.queue {
		got = append(got, q.key+"="+string(q.frame.Data))
	}
	assert.Equal(t, []string{`views:1="b"`, "=", `views:2="c"`, "="}, got)
	assert.Equal(t, uint64(7), c.queue[1].frame.ID)
}
```

- [ ] **Step 3: The protocol reference.** Run `mkdir -p docs/specs && CGO_ENABLED=0 go test ./core/rpc -run TestProtocolReference -update`. It writes `docs/specs/protocol.md`; read it once to check it lists every method with its kind, and every event.
- [ ] **Step 4:** Run `CGO_ENABLED=0 go test ./core/rpc -v`. All 13 tests pass:
  - the generator's two;
  - `TestEvents_RoundTrip` and `TestProtocolReference`;
  - the replica's parity;
  - events-before-reply;
  - casts, coalescing, the wake on a production loop, panics, the hello and a closed connection;
  - the server's queue.
- [ ] **Step 5: Check the tests bite,** reverting each change:
  1. **Events before the reply.** In `Server.handle`, queue the reply before `publishLocked`. `TestRequest_ItsEventsArriveBeforeItsReply` fails.
  2. **Replica parity.** In `replica.View`, match `id+1`. `TestReplica_AnswersAsTheModel` fails.
  3. **Coalescing.** In `Client.read`, queue state events as well as applying them. `TestSync_CoalescesStateAndKeepsTheRestInOrder` fails.
  4. **Panic forwarding.** In `Server.call`, never recover. `TestPanic_…` crashes the test binary with `panic: boom`.
- [ ] **Step 6:** Run `CC=clang CGO_ENABLED=1 go test -race -count=10 ./core/rpc/` (clean), then `CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test ./...` (green). Commit `feat(rpc): the wire, a server and a replicating client` with both trailers.

---

## Package C: the TUI talks to its model over the wire

### C1. The change

- [ ] **Step 1:** Save this diff and apply it (`git apply --check`, then `git apply`). It changes:
  - **`app_init.go`:** `startCore = rpc.InProcess` replaces `startModel = core.Start`, and `newHome` holds the client, its wakes and the stack's stop.
  - **`app_test.go`:** `TestMain` sets `startCore = startTestCore`.
  - **`testcore_test.go`:** `testStack`, `testStacks`, `startTestCore`, `testLoop`, `stackOf` and `loopOf`.
  - **`core_wake_test.go`:** `TestRealLoop_…` runs on `rpc.InProcess`.
  - **`usage_test.go`:** `time.Now().Round(0)`, decision 14.

```diff
diff --git a/app/app_init.go b/app/app_init.go
index d946556..5332c2f 100644
--- a/app/app_init.go
+++ b/app/app_init.go
@@ -6,6 +6,7 @@ import (
 	cmd2 "github.com/aidan-bailey/loom/cmd"
 	"github.com/aidan-bailey/loom/config"
 	"github.com/aidan-bailey/loom/core"
+	"github.com/aidan-bailey/loom/core/rpc"
 	"github.com/aidan-bailey/loom/internal/takeover"
 	"github.com/aidan-bailey/loom/log"
 	"github.com/aidan-bailey/loom/session"
@@ -44,11 +45,13 @@ const scriptShutdownTimeout = 1500 * time.Millisecond
 //     when it couldn't be taken: loom then runs unlocked and serves no
 //     takeovers.
 //
-// startModel puts the model on its own loop: core.Start, which runs its
-// jobs on goroutines of their own and ticks on its own timer. App's tests
-// replace it with core.StartForTest, whose loop keeps every job for the
-// test to run.
-var startModel = core.Start
+// startCore starts the model as the TUI talks to it: rpc.InProcess runs
+// it on its own loop, serves it over an in-memory pipe, and returns the
+// client (a core.Core keeping a replica of the model's published state)
+// and the function that stops all three. App's tests replace it with
+// rpc.InProcessForTest, whose loop keeps every job for the test to run and
+// whose client is synchronous.
+var startCore = rpc.InProcess
 
 func Run(ctx context.Context, wsCtx *config.WorkspaceContext, registry *config.WorkspaceRegistry, appConfig *config.Config, program string, pendingDir string, noScripts bool, uiLock *takeover.Lock) error {
 	// Activate the configured theme before any component renders.
@@ -127,15 +130,19 @@ func newHome(ctx context.Context, wsCtx *config.WorkspaceContext, registry *conf
 	if err != nil {
 		return nil, err
 	}
-	// From here on only the loop touches the model.
-	loop := startModel(model)
+	// From here on only the model's loop touches the model, and the TUI
+	// reaches it through the client.
+	client, stopCore, err := startCore(model)
+	if err != nil {
+		return nil, err
+	}
 	sp := ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane())
-	classic, _ := loop.Classic()
+	classic, _ := client.Classic()
 	h := &home{
 		ctx:        ctx,
-		core:       loop,
-		wakes:      loop.Wakes(),
-		stopCore:   loop.Stop,
+		core:       client,
+		wakes:      client.Wakes(),
+		stopCore:   stopCore,
 		fullScreen: &foregroundAttach{},
 		workspaceSlot: &workspaceSlot{
 			id:        classic.ID,
diff --git a/app/app_test.go b/app/app_test.go
index f73ec42..e101b26 100644
--- a/app/app_test.go
+++ b/app/app_test.go
@@ -32,9 +32,10 @@ func runTests(m *testing.M) int {
 	_ = log.Initialize("", false)
 	defer log.Close()
 
-	// Every home's model runs on a loop that keeps its jobs for the test
-	// (newHome included): core.StartForTest.
-	startModel = core.StartForTest
+	// Every home's model runs on a loop that keeps its jobs for the test,
+	// served to a client that pings before every read (newHome included):
+	// rpc.InProcessForTest, through startTestCore.
+	startCore = startTestCore
 
 	// Belt and suspenders: LOOM_TMUX_SOCKET is the only variable
 	// tmux.Command consults (an explicit -L outranks $TMUX), but any test
diff --git a/app/core_wake_test.go b/app/core_wake_test.go
index c9e8bd7..96e5738 100644
--- a/app/core_wake_test.go
+++ b/app/core_wake_test.go
@@ -5,7 +5,7 @@ import (
 	"time"
 
 	tea "charm.land/bubbletea/v2"
-	"github.com/aidan-bailey/loom/core"
+	"github.com/aidan-bailey/loom/core/rpc"
 	"github.com/stretchr/testify/assert"
 	"github.com/stretchr/testify/require"
 )
@@ -62,21 +62,22 @@ func TestSelection_ReachesTheModel(t *testing.T) {
 	assert.Equal(t, idOf(m, inst), testModel(m).SelectedForTest())
 }
 
-// TestRealLoop_AJobsResultReachesTheTUIByWake: on a production loop, a
-// request's job runs on a goroutine of its own, its result lands on the
-// loop, and the loop's wake brings it to the TUI, with no test-driven
-// delivery. Run it under -race.
+// TestRealLoop_AJobsResultReachesTheTUIByWake: on the production stack
+// (rpc.InProcess), a request's job runs on a goroutine of its own, its
+// result lands on the loop, the server publishes on the loop's wake, and
+// the client's wake brings it to the TUI, with no test-driven delivery.
+// Run it under -race.
 func TestRealLoop_AJobsResultReachesTheTUIByWake(t *testing.T) {
 	m := homeWithAppState(t)
 	addReadyInstance(t, m) // never started: the kill's job fails (no worktree)
 	m.errBox.SetSize(400, 1)
 	m.syncViews()
-	held := loopOf(m)
-	model := held.ModelForTest()
-	held.Stop()
-	l := core.Start(model)
-	t.Cleanup(l.Stop)
-	m.core, m.wakes = l, l.Wakes()
+	model := testModel(m)
+	stackOf(m).stop()
+	c, stop, err := rpc.InProcess(model)
+	require.NoError(t, err)
+	t.Cleanup(stop)
+	m.core, m.wakes = c, c.Wakes()
 	m.aliveProbe = func(string) bool { return true } // the TUI's probe must not read the model's instances while its loop runs
 
 	_, _ = runKillSelectedNoConfirm(m)
diff --git a/app/testcore_test.go b/app/testcore_test.go
index 9cb5597..a4bd40e 100644
--- a/app/testcore_test.go
+++ b/app/testcore_test.go
@@ -2,10 +2,12 @@ package app
 
 import (
 	"reflect"
+	"sync"
 	"testing"
 
 	"github.com/aidan-bailey/loom/config"
 	"github.com/aidan-bailey/loom/core"
+	"github.com/aidan-bailey/loom/core/rpc"
 	"github.com/aidan-bailey/loom/session"
 	"github.com/aidan-bailey/loom/ui"
 	"github.com/stretchr/testify/require"
@@ -359,17 +361,53 @@ func deliver(t *testing.T, m *home, result any) tea.Cmd {
 	return cmd
 }
 
-// testLoop runs model on a loop that keeps its jobs for the test
-// (core.StartForTest), stopped when the test ends.
+// testStack is a test client's loop (for its seams) and the function
+// stopping the client, its server and the loop.
+type testStack struct {
+	loop *core.Loop
+	stop func()
+}
+
+// testStacks maps each test client (a home's core) to its stack.
+var testStacks sync.Map
+
+// startTestCore is startCore for app's tests: rpc.InProcessForTest, whose
+// loop keeps its jobs for the test and whose client is synchronous (a ping
+// before every read and after every cast), so the TUI meets the model as
+// it did in stage 1E. It records the stack for loopOf.
+func startTestCore(model *core.Model) (*rpc.Client, func(), error) {
+	c, loop, stop, err := rpc.InProcessForTest(model)
+	if err != nil {
+		return nil, nil, err
+	}
+	testStacks.Store(core.Core(c), testStack{loop: loop, stop: stop})
+	return c, func() {
+		testStacks.Delete(core.Core(c))
+		stop()
+	}, nil
+}
+
+// testLoop serves model to a test client (startTestCore), stopped when
+// the test ends.
 func testLoop(t *testing.T, model *core.Model) core.Core {
 	t.Helper()
-	l := core.StartForTest(model)
-	t.Cleanup(l.Stop)
-	return l
+	c, stop, err := startTestCore(model)
+	require.NoError(t, err)
+	t.Cleanup(stop)
+	return c
+}
+
+// stackOf returns the stack behind the home's client.
+func stackOf(m *home) testStack {
+	s, ok := testStacks.Load(m.core)
+	if !ok {
+		panic("the home's core is no test client (testLoop)")
+	}
+	return s.(testStack)
 }
 
-// loopOf returns the home's loop, for its seams.
-func loopOf(m *home) *core.Loop { return m.core.(*core.Loop) }
+// loopOf returns the loop behind the home's client, for its seams.
+func loopOf(m *home) *core.Loop { return stackOf(m).loop }
 
 // testModel returns the home's model, for its seams. Its loop is idle
 // between calls (core.StartForTest), so the test may reach it directly.
diff --git a/app/usage_test.go b/app/usage_test.go
index 046a59a..e4785f6 100644
--- a/app/usage_test.go
+++ b/app/usage_test.go
@@ -16,7 +16,9 @@ import (
 func TestUsageReady_KeepsTheLastGoodSampleOnError(t *testing.T) {
 	m := homeWithAppState(t)
 	withAccounts(t, m, "max-2")
-	good := account.Usage{Available: true, At: time.Now(), FiveHour: &account.Window{Pct: 12}}
+	// Round(0): over the wire a time keeps its instant but not its
+	// monotonic reading, which reflect.DeepEqual compares.
+	good := account.Usage{Available: true, At: time.Now().Round(0), FiveHour: &account.Window{Pct: 12}}
 
 	deliver(t, m, core.UsageResultForTest(map[string]account.Usage{"max-2": good}, nil))
 	deliver(t, m, core.UsageResultForTest(nil, map[string]error{"max-2": errors.New("timeout")}))
```

### C2. Verify and commit

- [ ] **Step 1:** Run `gofmt -l app`, then `CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test ./...`. All green: the whole app suite now runs over the pipe.
- [ ] **Step 2:** Run `CC=clang CGO_ENABLED=1 go test -race ./...` (every package clean), `CC=clang CGO_ENABLED=1 go test -race -count=3 ./app/ ./core/rpc/`, and `CGO_ENABLED=0 go test -count=1 -tags e2e ./e2e/...`. The e2e run drives the real binary, whose TUI talks to its model over the pipe.
- [ ] **Step 3: Assertion count.** It must read 8741 or more. The one meaning change in C is none: `Round(0)` only strips the monotonic reading.
- [ ] **Step 4:** Commit `refactor(app): the TUI talks to its model over the wire` with both trailers.

---
## Package D: documentation and verification

### D1. CLAUDE.md and the spec

- [ ] **Step 1: CLAUDE.md.**
  - **Core Flow.**
    - `newHome` starts the model through `startCore` (`rpc.InProcess`): the loop, a `Server` and a `Client` over `net.Pipe`. The TUI holds the client as its `core.Core`.
    - The client keeps a full replica of the published state, so every query is local and only actions cross the wire.
    - A request's events arrive before its reply. Casts are one way. `Sync` returns the coalesced state, then the other events. Wakes come from `Client.Wakes()`.
    - The model's panics cross as a `panic` reply or `Fatal` frame, and the client re-raises them.
  - **Key Packages.** Add a `core/rpc/` bullet:
    - the frames and hello (`Protocol`);
    - the generator (`internal/gen`, `methods_gen.go`, `go generate`, `TestGenerated_IsFresh`);
    - `Server` (publishing, events before reply, casts publish nothing, the coalescing queue, panics);
    - `Client` and `replica`;
    - `InProcess` and `InProcessForTest`;
    - the protocol reference (`docs/specs/protocol.md`, `TestProtocolReference -update`).
  - **`core/` bullet.**
    - The state views (`WorkspacesView`, `ModelView`, `AccountsView`, `GitHubView`) and their events.
    - The publish order and `Snapshot`.
    - `WireError` and its codes, and `EventTypes`.
    - The `rpc:` directives in `iface.go`.
    - The parity tests.
  - **The no-model-mutation gotcha.** Add the wire's rules:
    - reads are local;
    - read-after-write holds through events-before-reply;
    - a cast must change no published state;
    - a `time.Time` loses its monotonic reading on the wire.
  - **Testing Patterns.** App tests run over the pipe (`startTestCore`, `rpc.InProcessForTest`): the loop holds its jobs, and the client is synchronous (a ping before reads, after casts). `loopOf(m)` finds the loop through `testStacks`, and `TestRealLoop_…` uses the production stack.
  - **Persistent State.** None changes. Note `docs/specs/protocol.md` under Documentation.
- [ ] **Step 2: The spec.**
  - **Rollout stage 2:** link this plan with a one-line summary. Record the user's decisions (full replica; named generated parameters) and the amendment to §4: coalescing instead of drop-and-resync, since a resync can't rebuild a lost `Reply`.
  - **§2 "Requests":** wire names are Go method names, and fields are parameter names.
  - **§4:** the frame shapes, pointing at `docs/specs/protocol.md`.
  - **Stage 3's line:** reconnect is a re-dial, and the snapshot rebuilds the replica.
- [ ] **Step 3:** Commit `docs: CLAUDE.md and spec for the wire (daemon stage 2)` with both trailers.

### D2. Verification and smoke run

- [ ] **Step 1: The suite.** Run `go vet`, `go test ./...`, `-race ./...`, e2e (`-count=1`) and gofmt. All must be green.
- [ ] **Step 2: Sandbox smoke run.** Use the loom-dev skill, with 1E's safety rules:
  - build once at a named SHA, then `start --no-build`;
  - build a baseline from `58261b9` (`git archive` into the scratchpad, made a throwaway repo, since loomdev needs a checkout);
  - point `CLAUDE_CONFIG_DIR` at a throwaway dir;
  - never touch `~/.loom`, `~/.claude`, the user's tmux server, or another session's sandbox.

  Rerun 1E's smoke checks 1–5 and 7–9: kill, create with a prompt (time it against the baseline), diff stats after output, hook status, dead agent, idle CPU, snapshot path, and quit mid-kill. Then rerun the lifecycle and 1D checks: pause, resume, recover with the recovery summary, the theme in `config.json`, a UI pref in `state.json`, and the registry from a second shell. Then these new ones:
  1. **Accounts round trip.** Add an account from a second shell (`loom account add` against the sandbox's global dir, `--no-login`). The accounts strip appears without a keypress (an `AccountsChanged` diff). Settings → Accounts lists it.
  2. **Issue picker state.** With gh unavailable in the sandbox, `I` shows the unavailable reason. That reason comes from the replicated `GitHubView`.
  3. **A large diff.** Write a 2 MB file into a session's worktree, then make the agent print output. The pane title shows the stats, the workbench diff tab renders it, and keys stay responsive. The selected session's full diff crosses the wire in `ViewsChanged`.
  4. **loom.log.** It has no `subsystem=rpc` warnings or errors after the run.

  Check every oddity against the baseline before calling it a regression.
- [ ] **Step 3: Report** the test totals, each smoke check's outcome, and every deviation from this plan.

### D3. Outcome

- [ ] Append "Outcome and follow-ups" to this plan. Update the `loom-scrum-daemon-direction` memory: stage 2 is done, and the next step is the stage 3 plan (the daemon process).
