# Changing the instance schema (`session.InstanceData`)

**Symptom:** after an upgrade, sessions lose a setting they had; after a downgrade and an upgrade back, records come back stripped; `loom workspace migrate` silently drops a field; or a new field round-trips in tests and is empty after a restart.

**Cause:** `session.InstanceData` (`session/storage.go`) is persisted in `state.json`'s `instances` array and read by binaries of every version the user runs. Old records reach the new binary through `session/storage_migrate.go:Migrate`; new records reach an old binary, which skips what it can't decode and writes it back verbatim; and `loom workspace migrate` reads and writes them through its own typed mirror (`cmd/workspace_migrate.go`), not through `session`.

**Rule:** every persisted field change bumps the schema version and adds an upgrade step, and the mirror follows. A transient field (one that is never serialized, such as `Instance.waitReason`) is not a schema change: leave it out of `InstanceData` and bump nothing.

## Checklist

1. **Change the field** on `InstanceData` (or `GitWorktreeData`, `DiffStatsData`), with its JSON tag. Pick a zero value that is the right default for every existing record, or the upgrade step must set one.
2. **Carry it both ways**: write it in `Instance.Snapshot` (`session/instance.go`; `ToInstanceData` only wraps it) and read it in `FromInstanceData`. *Silent:* a field missing here saves as zero and restores as zero. Add a round-trip test, as `TestClaudeSession_PersistsThroughInstanceData` does. An orphan recovered from disk is built by `InstanceDataFromOrphan` (`session/orphan.go`), not from a record: decide what the field should be there too.
3. **Bump `CurrentSchemaVersion`** (`session/storage.go`). *Silent:* nothing fails if you forget, and old and new records then share a version.
4. **Add the upgrade step** to the switch in `Migrate`: a `case` for the previous version that adjusts the payload if needed and stamps the next version. A missing step fails every old record ("no upgrade path"). *Enforced:* `TestMigrate_V0Upgrades` walks a version-0 record through every step, so a gap fails it; add a `TestMigrate_…` case for what your step sets (`session/storage_migrate_test.go`), and `TestMigrate_Idempotent` keeps current records unchanged.
5. **Update the `workspace migrate` mirror**: the struct in `cmd/workspace_migrate.go` and the JSON fixture in `cmd/workspace_migrate_shape_test.go`. *Enforced* for the struct by `TestMigrationInstance_TypeDriftGuard`, which compares the mirror with `InstanceData` by reflection. The fixture is *silent*: `TestMigrationInstance_MirrorsInstanceData_JSON` round-trips the fixture through the mirror and never looks at `InstanceData`, so a field missing from it passes.
6. **Keep `Storage`'s writes on `writeLocked`** (`session/storage.go`); add no path that writes `state.json` around it.
7. **If the TUI shows it**, add a `core.InstanceView` field and its line in `viewOf` (`core/views.go`), then rewrite the protocol reference (`go test ./core/rpc -run TestProtocolReference -update`; *enforced* by `TestProtocolReference`). The view field is *silent*: without it the TUI never sees the value.

## Downgrade safety

A record whose `schema_version` is newer than the running binary fails `Migrate` (`TestMigrate_FutureVersionRejected`). `MigrateAll` skips it and `Storage` keeps its raw bytes, appending them verbatim to every save, delete and update, and claims its worktree path and title so the orphan sweeps leave its sessions alone (`TestStorage_UndecodableRecord_SurvivesSave`, `TestStorage_UndecodableRecord_WorktreeIsClaimed`). So an old binary leaves a new binary's records intact for when it returns, but shows those sessions nowhere meanwhile. Only a top-level payload that isn't a JSON array fails the load, and then every write is refused (`ErrStorageLoadFailed`) until a later load succeeds.

## Traps

- **`loom workspace migrate` is lossy for a newer record**: its typed mirror knows only this binary's fields, so a record from a newer loom passing through it loses every field this binary doesn't know. The drift guard keeps the mirror equal to this binary's `InstanceData`, not to future ones.
- **The version stamp is the contract.** Changing a field's meaning without a bump makes an old binary misread new records and a new binary misread old ones, with no error.
- **A field the model needs before the first save** (a launch flag, an account) must survive `Recoverable` adoption too: an orphan recovered from disk is rebuilt by `InstanceDataFromOrphan` from what discovery finds, not from a record, so a field it doesn't set comes back as its zero value (a dead orphan's account comes back as the default this way).

## What the gates won't tell you

- Whether the zero value is the right default for every existing record.
- Whether the field is carried through `Instance.Snapshot`/`FromInstanceData`, unless you wrote the round-trip test.
- Whether you bumped the version, or updated the shape fixture.
- Whether an older release still in use reads your records sensibly: only the skip-and-preserve path is tested, not an old binary itself.
