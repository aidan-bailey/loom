// Package feedback is the inbox loom's session agents write to (session
// feedback spec, docs/superpowers/specs/2026-11-05-session-feedback-design.md):
// the notes, the threads a headless grouper files them into, the store
// (feedback.json), ranking, the grouper's request and answer, the fix
// session's title and prompt, and the grouper's call log. It is pure logic
// over values: the model in core owns the inbox and calls it on its loop.
package feedback
