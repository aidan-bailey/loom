package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/aidan-bailey/loom/sched"
)

// jobShimCmd is the process a scheduled job runs under (spec §5.3): the
// daemon starts it with sched.Runner.Start, and it lives until the job's
// command exits. Hidden, since only the daemon runs it.
var jobShimCmd = &cobra.Command{
	Use:    "job-shim <job dir>",
	Short:  "Run one scheduled job (started by the daemon)",
	Hidden: true,
	Args:   cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		os.Exit(sched.RunShim(args[0]))
	},
}
