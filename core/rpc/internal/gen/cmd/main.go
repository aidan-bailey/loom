// Command cmd writes core/rpc/methods_gen.go from core/iface.go. Run it
// with go generate in core/rpc, after any change to core.Core.
package main

import (
	"fmt"
	"os"

	"github.com/aidan-bailey/loom/core/rpc/internal/gen"
)

func main() {
	src, err := os.ReadFile("../iface.go")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	out, err := gen.Generate(src)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
	if err := os.WriteFile("methods_gen.go", out, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
