package sched

import (
	"fmt"
	"os"
)

// ShimTestEnv, set to "1" in a process's environment, makes MaybeRunTestShim
// turn that process into a job shim.
const ShimTestEnv = "LOOM_SCHED_TEST_SHIM"

// MaybeRunTestShim runs the shim and exits, when ShimTestEnv is "1": the last
// argument is the job dir, as Runner.Start passes it. It is the first
// statement of a TestMain whose package starts real shims, since a test
// binary has no job-shim command; a binary started as the shim without it
// would run the whole suite again, and start more shims.
func MaybeRunTestShim() {
	if os.Getenv(ShimTestEnv) != "1" {
		return
	}
	os.Exit(RunShim(os.Args[len(os.Args)-1]))
}

// TestRunner is a Runner that starts the running test binary as the shim,
// with ShimTestEnv set to "1".
func TestRunner() Runner {
	exe, err := os.Executable()
	if err != nil {
		panic(fmt.Sprintf("sched.TestRunner: finding the test binary: %v", err))
	}
	return Runner{Shim: []string{exe, "job-shim"}, Env: []string{ShimTestEnv + "=1"}}
}
