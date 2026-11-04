# The build sweep in the settings menu

Status: approved 2026-11-04. Builds on [`2026-11-03-build-sweep-design.md`](2026-11-03-build-sweep-design.md).

## Problem

The build sweep's threshold, `build_sweep_after_hours`, is read from the global `~/.loom/config.json` only: disk space is machine-wide and one daemon serves every workspace. The only way to set it is to edit that file by hand. The settings menu (`S`) edits the focused workspace's own `config.json` (`<repo>/.loom/config.json`) and saves it whole through `Core.SaveSettings`, so a plain row there would write a value the sweep never reads.

## Decisions

- **Scope stays global.** The row sets the one machine-wide threshold, whichever workspace is focused, and says so.
- **The row:** `Build Sweep`, after Theme, showing `off` or `<n>h`, followed by `(all workspaces)`. Enter opens a one-line edit holding the current value: a whole number of hours, 0 for off, at most 8760. Anything else is refused with an error (the overlay's `TakeError`) and nothing is sent.
- **The overlay never writes the threshold into the workspace's settings.** A valid edit becomes a pending request the app polls (`TakeBuildSweepRequest`), as the Accounts screen's requests are; the app supplies the value the row shows (`SetBuildSweep`).
- **A new request, `Core.ConfigureBuildSweep(afterHours int) error`,** modelled on `ConfigureWeb`. The model validates the value as the overlay does, then rewrites the global `config.json`: read strictly (`config.ReadConfigFrom`: a missing file gives the defaults; a file that can't be read or parsed is an error, never quarantined), set the one field, save atomically (`config.SaveConfigTo`). When the read or the write fails, nothing changes and the error says why. A served global workspace (`isGlobalWS`) gets the new value in its in-memory config too.
- **`SaveSettings` never changes the threshold.** It carries the field over instead of taking the client's copy: from the model's current value for the global workspace, from the workspace's own config for any other. A whole-settings save from an overlay opened before the change can no longer write an old threshold back.
- **A local query, `Core.BuildSweep() BuildSweepView // rpc:local`,** answered from `ModelView.BuildSweep` (`BuildSweepView{AfterHours int}`). The model reads the threshold from the global config at boot, takes it from each periodic sweep job's own read, and sets it on `ConfigureBuildSweep`, so a hand edit of `config.json` reaches every client within one sweep interval. The periodic job runs even when no session is eligible, so that read happens every interval.
- **Other TUIs follow:** on `ModelChanged` an open settings overlay refreshes the row.
- **Wire:** one method and one `ModelView` field, both additive; `rpc.Protocol` stays. `methods_gen.go` and `docs/specs/protocol.md` are regenerated.
- `loom sweep`'s "the build sweep is off" error names the settings menu. `USAGE.md` documents the row.

## Out of scope

Per-workspace thresholds; a CLI to set the threshold; presets.

## Tests

- Overlay: the row renders `off` and `12h (all workspaces)`; Enter edits the current value; a valid value becomes a request and leaves the workspace config untouched; `abc`, `-1` and `8761` are refused with no request.
- App: a request reaches `ConfigureBuildSweep` and the row shows the new value; its error is shown; a change made elsewhere refreshes an open overlay.
- Core: the value lands in the global `config.json` with the other fields kept; a corrupt global file is refused and left byte for byte; out-of-range values are refused; a served global workspace's config follows; `SaveSettings` with an old copy keeps the threshold; the periodic job's read updates the view, with no eligible session too.
- Wire: `TestReplica_AnswersAsTheModel` and `TestModelView_CloneKeepsEveryField` cover the view; a request round-trips through the client; `TestGenerated_IsFresh` and `TestProtocolReference` pass.
