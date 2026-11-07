// Package attention judges a stopped Claude turn from its last message: the
// session needs its person (a question, a choice, a go-ahead, an action), is
// done, waits on its own background work, or has merely stopped. It holds
// the classifier's settings, request, schema, prompt, answer check and call
// log; the model in core decides when to call it and owns the verdicts
// (docs/superpowers/specs/2026-11-07-stop-verdicts-design.md).
package attention
