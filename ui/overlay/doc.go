// Package overlay implements the modal dialogs layered above the main
// TUI — the text-input overlay for new instances and prompts, the
// confirmation overlay for destructive actions, and the branch,
// profile, and workspace pickers.
//
// Every app-level overlay satisfies the [Overlay] interface so the app
// model can hold a single active overlay pointer rather than one
// nullable field per dialog type. Specialized surfaces (Submitted /
// Canceled flags, selected branch/profile/workspace) remain on the
// concrete types for callers that need richer signals.
package overlay
