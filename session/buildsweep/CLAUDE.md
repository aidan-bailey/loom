# session/buildsweep

The build sweep: it deletes the Rust executables in a session worktree's cargo target dir that have gone unused for the threshold (`Sweeper.Run`), which the model runs every 30 minutes on idle and paused sessions and on request (`core.SweepBuilds`, `loom sweep`). Full reference in [`../README.md`](../README.md).

## Rules when modifying this package

- **Delete only executables of runnable targets past the threshold, each with its uplifted links: hashed names in a profile dir's `deps/` and `examples/`, and in cargo 1.100's layout v2 the `out/` of a `build/<pkg>/<hash>/` unit whose `fingerprint/` names only `runnableKinds` (bins, tests, benches, examples).** Libraries, proc-macros and build scripts are used rarely and look unused, and losing them rebuilds chains of crates; v2 names a build script `build_script_build`, hashless like a bin, so only its unit's kind tells them apart. A wrong delete costs minutes of rebuild, and the sweep's log doesn't say why. **Enforced** by `TestSweep_DeletesOnlyExecutablesUnusedPastTheThreshold`, `TestSweep_LayoutV2`, `TestSweep_LayoutV2LocksAndAnUpgradedProfile` and, against real cargo in both layouts, `TestSweep_RealCargoBuild` and `TestSweep_RealCargoBuildIntoATargetDirThatExisted`.
- **Skip a worktree on any doubt: a `target/` with neither `CACHEDIR.TAG` nor `.rustc_info.json` (cargo tags only a target dir it creates), a mount that records no access times (or `relatime` under 24h), a cargo lock held, a `cargo` process inside it.** `cargo test` drops its locks before running the tests, so only the process check covers a binary cargo found fresh but hasn't run yet; a delete under it fails the test run. **Enforced** by `TestSweep_SweepsOnlyWhereAccessTimesAreRecorded`, `TestSweep_SkipsWhileCargoHoldsALock`, `TestSweep_SkipsWhileCargoRunsInTheWorktree` and `TestSweep_NeedsCargosMarkOnTheTargetDir`.

## Pointers

- [`../CLAUDE.md`](../CLAUDE.md) — the session rules, this package's purity among them.
- [`../../docs/ARCHITECTURE.md`](../../docs/ARCHITECTURE.md) — where this package sits.
