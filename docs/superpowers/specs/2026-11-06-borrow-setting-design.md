# Job borrowing in the settings menu

Status: approved 2026-11-06. Builds on [memory borrowing](2026-11-06-memory-borrowing-design.md) (§9, `scheduler.borrow`) and follows [the build sweep's settings row](2026-11-04-build-sweep-setting-design.md).

## Problem

`scheduler.borrow` lives only in the global `~/.loom/config.json`, so the owner turns borrowing on by editing that file by hand. The settings menu (`S`) edits the focused workspace's `config.json`. Its save, `SaveSettings`, already takes the scheduler block from disk rather than from the client, so a plain row there would never reach the file the scheduler reads.

## Decisions

- **The row.** `Job Borrowing`, after Build Sweep, shows `off (all workspaces)` or `on (all workspaces)`. Enter toggles it; there is no text edit. The setting is global, whichever workspace is focused.
- **The overlay never writes the setting into the workspace's settings.** A toggle becomes a pending request the app polls (`TakeBorrowRequest`), as Build Sweep's does, and the app supplies the value the row shows (`SetBorrow`).
- **A new request, `Core.ConfigureBorrow(on bool) error`.** It refuses while the queue has no directory (`errQueueNotLoaded`). Otherwise it reads the queue's global `config.json` strictly (`readGlobalConfig`: a file that can't be read or parsed is an error and is left as it is), sets only `scheduler.borrow`, saves atomically (`config.SaveConfigTo`), gives a served workspace on that dir the block in memory (as `setSchedulerCaps` does), and runs an admission pass, which rereads the block. When the read or the write fails, nothing changes and the error says why.
- **No new query.** The row's value is `QueueView.Settings.Borrow`, which every client already holds. The app sets it when the overlay opens and on every `QueueChanged`, so another TUI's toggle reaches an open overlay.
- **Wire:** one additive method; `rpc.Protocol` stays. `methods_gen.go` and `docs/specs/protocol.md` are regenerated.
- `USAGE.md` documents the row beside `borrow`'s table row.

## Out of scope

Per-workspace borrowing; a CLI to set it; the other scheduler keys in the menu (the work view's caps form keeps the caps).

## Tests

- **Overlay:** the row renders `off (all workspaces)` and `on (all workspaces)`. Enter on it makes a request for the opposite value and leaves the workspace config untouched, and a second Take returns nothing.
- **App:** the request reaches `ConfigureBorrow`, and the row shows the value the model then reports. An error goes to the error line. A `QueueChanged` refreshes an open overlay.
- **Core:** the field lands in the global `config.json` with the other scheduler keys and top-level fields kept, and the policy follows. A corrupt global file is refused and left byte for byte. A queue that isn't loaded is refused.
- **Wire:** `TestMethods_CoverCore`, `TestLoopForwardsEachMethodToItsNamesake`, `TestGenerated_IsFresh` and `TestProtocolReference` pass. The request round-trips through the client.
