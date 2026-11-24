# internal/memguard

The TUI's memory guard: `FromEnv` reads `LOOM_TUI_MEMORY_LIMIT` (a size, 6 GiB by default, or `off`), samples the Go runtime's own accounting every 2 s (`Read`: all it mapped less what it released, so swapped pages and goroutine stacks count), writes heap and goroutine profiles beside the TUI's log at half the limit (`memguard.profiled`), and at the limit writes them again (`memguard.limit`) and hands the `Breach` to its caller. `app.Run` starts it once its program exists, and its breach handler stores the `Breach` for `Run` to return and calls `Quit`.

## Rules when modifying this package

- **Arm `Quit`'s exit before it calls kill, and never wait on its stderr write for more than a second.** Neither the program's shutdown nor the terminal can be trusted to return: a terminal that stopped reading holds Bubble Tea's renderer, every `Update` waits on the renderer, and `Kill` restores the terminal by writing to it, so a quit that waits on either never comes and the TUI keeps growing. **Enforced** by `TestQuit_ExitsWhenKillNeverReturns` and `TestQuit_ExitsWhenStderrBlocks`.
- **Quit at the limit even when the profiles fail, and keep the default limit when `LOOM_TUI_MEMORY_LIMIT` can't be read.** The profiles are evidence; the quit is what protects the host, whose earlyoom otherwise kills agents first (2026-11-24: about 48 of them, for a 30 GB TUI). **Enforced** by `TestGuard_AFailedWriteStillQuits` and `TestFromEnv_ABadLimitKeepsTheDefault`.
- **Act once per stage: profiles once at half the limit, and one breach.** A guard that rewrites profiles on every sample fills `~/.loom/logs/` with copies while the process grows. **Enforced** by `TestGuard_ProfilesOnceAtHalfTheLimit`, `TestGuard_QuitsAtTheLimitWithItsProfiles` and `TestGuard_AJumpPastBothWritesOneSetOfProfiles`.
- **Measure through `runtime/metrics`, never the process's RSS.** RSS misses swapped pages (6 GB of the 2026-11-24 TUI's 30 GB) and `/proc` doesn't exist on the Windows build. **Enforced** for stacks by `TestRead_CountsGoroutineStacks`; **Convention** for the rest.
- **Run a guard only in the TUI, never in the daemon, the job shim or a subcommand.** The daemon owns every session's state and serves every TUI; quitting it at a threshold takes the fleet offline, while a TUI quits for the price of a restart. **Convention** — a daemon that stops itself under load.
- **Write profiles mode 0600 and with `O_EXCL`.** A goroutine profile names what the process was doing, and a stale file of the same name must not be overwritten. **Enforced** for the mode by `TestWriteProfiles_WritesHeapAndGoroutines`.

## Pointers

- [`../../docs/claude/debugging-freezes.md`](../../docs/claude/debugging-freezes.md) — reading the profiles, starving a TUI's output in a sandbox, and the 2026-11-24 incident.
- [`../pprofserve/CLAUDE.md`](../pprofserve/CLAUDE.md) — profiles on demand (`LOOM_PPROF`).
- [`../../docs/ARCHITECTURE.md`](../../docs/ARCHITECTURE.md) — where this package sits.
