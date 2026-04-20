// Package config owns Loom's on-disk configuration, app state,
// profile catalog, and workspace registry.
//
// The root config directory defaults to ~/.loom (overridable via
// $LOOM_HOME). [WorkspaceContext] carries the resolved directory for
// a given invocation and is threaded through the app rather than
// relying on globals, so per-workspace configs at <repo>/.loom/ can
// coexist with the global config. [LoadConfigFrom]/[LoadStateFrom]
// accept an empty path to mean "use the default directory."
//
// [MigrateLegacyHome] performs the one-shot rename from
// ~/.claude-squad to ~/.loom that covers users upgrading across the
// rebrand; it is idempotent and safe to call on every startup.
package config
