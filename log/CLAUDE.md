# log

Centralized logging: the structured `slog` logger (`log.For`, `InfoKV`/`WarnKV`/`ErrorKV`/`DebugKV`), the legacy `InfoLog`/`WarningLog`/`ErrorLog` `*log.Logger`s (whose remaining `InfoLog.Printf` call sites are all in `ui/split_pane.go`) and the `Every` rate limiter, writing the TUI's `{configDir}/logs/loom.log` or, for the daemon, `<globalDir>/logs/serve.log` (`ServeLogFileName`). Package doc: `log/doc.go`.

## Rules when modifying this package

- **Emit debug records only through the structured logger** (`log.Debugf`, `log.DebugKV`). `LOOM_LOG_LEVEL`/`--log-level` gate debug through slog's level, and the legacy writers have no debug tier, so a debug line written through `InfoLog` shows at the default level. **Convention** — debug noise in every user's `loom.log`.
- **Gate every legacy writer at the writer layer.** Each legacy `*log.Logger` writes through a `levelWriter`, so records below the level are dropped as slog drops them; a legacy logger built on the raw rotator ignores the level. **Enforced** by `TestLevelGateSilencesLegacyLoggers` (`log/log_test.go`).
- **Initialize the daemon with `Initialize(dir, true)`, and keep its differences.** It writes `serve.log`, tags records `component=daemon` and legacy lines `[DAEMON]`, never rotates at startup (a `loom serve` that loses the lock race would rename the live daemon's log out from under it; the writer still rotates as it grows), and falls back to `io.Discard`, never stderr, since a spawned daemon's stdio is /dev/null. **Enforced** for `serve.log`, the startup rotation and the component tag by `TestInitialize_TheDaemonWritesServeLog`, `TestInitialize_TheDaemonsLogIsNotRotatedAtStartup` and `TestNewStructured_DaemonTagsComponent` (`log/log_test.go`); **Convention** for the `io.Discard` fallback and the `[DAEMON]` prefix — a daemon writing to a closed stderr, or legacy lines nobody can tell from the TUI's.
- **Leave the loggers usable when `Initialize` fails.** It returns the error but installs a fallback sink, so callers keep logging in a degraded mode instead of crashing. **Enforced** by `TestInitialize_ReturnsErrorWhenLogFileUnopenable`.

## Pointers

- [`../docs/ARCHITECTURE.md`](../docs/ARCHITECTURE.md) — where this package sits.
