// Package testpty gives tests a stand-in for a tmux attach client's PTY.
// A real client's PTY stays open, its reads blocking, until the session
// ends; /dev/null or a regular file reads EOF at once, which a client
// takes for a session that died. Pair's attach end blocks until the test
// closes its peer, and it is pollable, so a deadline or Close interrupts
// a blocked read and releasing a client stays fast.
//
// Only test code imports this package.
package testpty
