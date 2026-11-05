# session/buildsweep

The build sweep: it deletes the Rust executables in a session worktree's cargo target dir that have gone unused for the threshold (`Sweeper.Run`), which the model runs every 30 minutes on idle and paused sessions and on request (`core.SweepBuilds`, `loom sweep`). Full reference in [`../README.md`](../README.md).

## Rules when modifying this package

- **Let `session/buildsweep` delete only hashed executables in a profile dir's `deps/` and `examples/` (with their uplifted links) past the threshold, and skip a worktree on any doubt: a `target/` with neither `CACHEDIR.TAG` nor `.rustc_info.json` (cargo tags only a target dir it creates), a mount that records no access times (or `relatime` under 24h), a cargo lock held, a `cargo` process inside it.** Libraries, proc-macros and build scripts are used rarely and look unused, and losing them rebuilds chains of crates; `cargo test` drops its locks before running the tests, so only the process check covers a binary cargo found fresh but hasn't run yet. A wrong delete fails a test run or costs minutes of rebuild, and the sweep's log doesn't say why. **Enforced** by `TestSweep_DeletesOnlyExecutablesUnusedPastTheThreshold`, `TestSweep_SweepsOnlyWhereAccessTimesAreRecorded`, `TestSweep_SkipsWhileCargoHoldsALock`, `TestSweep_SkipsWhileCargoRunsInTheWorktree`, `TestSweep_NeedsCargosMarkOnTheTargetDir`, `TestSweep_RealCargoBuild` and `TestSweep_RealCargoBuildIntoATargetDirThatExisted`.

## Pointers

- [`../CLAUDE.md`](../CLAUDE.md) — the session rules, this package's purity among them.
- [`../../docs/ARCHITECTURE.md`](../../docs/ARCHITECTURE.md) — where this package sits.
