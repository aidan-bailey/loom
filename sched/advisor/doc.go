// Package advisor is the work scheduler's agent (smart scheduler spec
// §5.6): a headless claude -p that estimates a command no run has measured
// (a prior) and explains an outcome that surprised its belief (a note). It
// holds the agent block's settings, the filter that keeps secrets out of
// what the model sees, the requests and the answers' checks, and the CLI
// call. The model in core decides when to call it; nothing here keeps
// state. It is "advisor", not "agent", because loom says agent for a
// session's program.
package advisor
