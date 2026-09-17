// Command fakeagent is a deterministic, token-free stand-in for an AI
// coding agent, used by loom's dev sandbox (tools/loomdev). The sandbox
// installs it under persona names (claude, aider); loom's adapter registry
// matches on the program's basename and applies the real adapter to it.
// Command-line flags — such as loom's Claude launch flags — are ignored.
package main

import "os"

func main() {
	dir, err := os.Getwd()
	if err != nil {
		dir = "."
	}
	os.Exit(newFakeAgent(personaFor(os.Args[0]), os.Stdin, os.Stdout, dir).run())
}
