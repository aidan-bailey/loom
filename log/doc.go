// Package log is Loom's centralized logging surface.
//
// All records land in {configDir}/logs/loom.log, which is rotated
// once to loom.log.1 at startup when it exceeds 5 MB. Two logger
// families share the file:
//
//   - Structured (slog-backed): [For], [InfoKV], [WarnKV], [ErrorKV],
//     [DebugKV]. Records carry a subsystem tag plus component=daemon
//     when emitted by the daemon child, so a single grep scopes
//     output to a subsystem.
//   - Legacy (*log.Logger): [InfoLog], [WarningLog], [ErrorLog] plus
//     the Infof/Warnf/Errorf helpers. Retained for callers not yet
//     migrated; routed through the same writer and gated by the
//     same level.
//
// Both families respect LOOM_LOG_LEVEL (debug|info|warn|error) and
// the --log-level CLI flag. Debug records are emitted only through
// the Structured logger. [Every] provides a minimal rate limiter
// for hot paths that would otherwise flood the log.
package log
