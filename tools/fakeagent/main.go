// Command fakeagent is a deterministic, token-free stand-in for an AI
// coding agent, used by loom's dev sandbox (tools/loomdev). The sandbox
// installs it under persona names (claude, aider); loom's adapter registry
// matches on the program's basename and applies the real adapter to it.
// Command-line flags are ignored except --settings, whose hooks it fires
// the way Claude does, --resume, and --trust-first, which makes it stop at
// its trust prompt on launch. `fakeagent agents …` answers loom's
// roster query with no sessions, so the sandbox's status is hook-driven.
// With LOOM_MOD_SOCKET and LOOM_LAUNCH set it plays the loom mod
// (session/claudemod), running the daemon's messages as prompts.
package main

import (
	"fmt"
	"os"
)

func main() {
	dir, err := os.Getwd()
	if err != nil {
		dir = "."
	}
	opts := parseArgs(os.Args[1:])
	if opts.agents {
		fmt.Println("[]")
		return
	}
	a := newFakeAgent(personaFor(os.Args[0]), os.Stdin, os.Stdout, dir)
	emitter, err := newHookEmitter(opts, dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fakeagent: hooks disabled: %v\n", err)
	}
	a.hooks = emitter
	a.mod = newModClient(os.Getenv)
	a.trustFirst = opts.trustFirst
	os.Exit(a.run())
}
